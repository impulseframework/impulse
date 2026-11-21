package generation

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
)

// losingSQL is the count of the people who lose a group's role, as a list of the roles
// groups hold selects it for the row read under r of the group read under g, over the
// fixture's Crew tables.
func losingSQL() string {
	gives := func(group, role string) string {
		return role + ".Role != 'SuperAdministrator' AND (" + group + ".Kind = 'global' OR (" + role + ".Kind = " + group + ".Kind AND " + role + ".Axis = " + group + ".Axis AND " + role + ".DomainId = " + group + ".DomainId))"
	}
	where := func(held string) string {
		return held + ".Axis = r.Axis AND ((" + held + ".Kind = r.Kind AND " + held + ".DomainId = r.DomainId) OR (r.Kind = 'domain' AND " + held + ".Kind = 'every'))"
	}

	return strings.Join([]string{
		"  (SELECT COUNT(*) FROM CrewGroupMembers lm",
		"   WHERE lm.GroupId = r.GroupId",
		"     AND " + countingSQL("lm"),
		"     AND " + countingSQL("r"),
		"     AND " + gives("g", "r"),
		"     AND NOT EXISTS (",
		"       SELECT 1 FROM CrewUserRoles@{FORCE_INDEX=CrewUserRolesByUserId} lo",
		"       WHERE lo.UserId = lm.UserId AND lo.Role = r.Role",
		"         AND " + countingSQL("lo"),
		"         AND " + where("lo"),
		"     )",
		"     AND NOT EXISTS (",
		"       SELECT 1 FROM CrewGroupMembers@{FORCE_INDEX=CrewGroupMembersByUserId} lp",
		"       JOIN CrewGroupRoles lr ON lr.GroupId = lp.GroupId",
		"       JOIN CrewGroups lg ON lg.Id = lp.GroupId",
		"       WHERE lp.UserId = lm.UserId AND lr.Role = r.Role",
		"         AND " + countingSQL("lp"),
		"         AND " + countingSQL("lr"),
		"         AND " + where("lr"),
		"         AND " + gives("lg", "lr"),
		"         AND NOT (lr.GroupId = r.GroupId AND lr.Kind = r.Kind AND lr.Axis = r.Axis AND lr.DomainId = r.DomainId)",
		"     )",
		"  ) AS Losing",
	}, "\n")
}

// Test_prepareIdentity_holdings pins what an auth is written of what is held and why,
// for the whole application: the reason on the declaration over the memberships and on
// the method that gives a role, with the store's write of it; a person's roles as one
// list, a role of their own beside one that comes through a group, the group part of
// what names a row; the groups listed with how many people each has and the roles each holds,
// over what has not ended, which the whole application's methods name a group by; a
// group's roles with how many people lose each, counted among what counts now; the roles
// held for a set time with a reason; and the taking of a role with the two lists its
// form picks from. An auth whose directory writes its roles takes none and keeps no
// groups: its people's roles are one list all the same.
func Test_prepareIdentity_holdings(t *testing.T) {
	// As Test_prepareIdentity: the working directory is the process's, no t.Parallel.
	t.Chdir(t.TempDir())

	r := identityFixtureGenerator(t, slices.Concat(crewEveryTable, crewGroupTables, membersIdentityTables, membersGroupTables)...)
	r.extraOutlets[1].auth = &outletAuth{importPath: membersAuthPath, methods: []AuthMethod{AzureMethod("directory").SyncsRoles()}}
	if err := r.prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() error = %v", err)
	}

	requireLines(t, map[string][]string{
		"internal/crewresources/crew_user_roles.go": {"Reason    *string    `spanner:\"Reason\"`", "Reason is why the role was given"},
		"crewidentity/assign_crew_role.go": {
			"\t// Reason says why the role is given: one line of plain text, kept with the role and\n\t// shown wherever the person's roles are listed. Left out, the role carries none.\n\tReason *string\n}",
			"identity.RequestReason(m.Reason)",
		},
		"crewidentity/store.go": {
			"func (store) SetMembershipPeriod(ctx context.Context, txn resource.ReadWriteTransaction, key *access.MembershipKey, period accesstypes.Period, reason string) error {\n\tvar why *string\n\tif reason != \"\" {\n\t\twhy = &reason\n\t}",
			"SetStartsAt(period.StartPtr()).\n\t\tSetEndsAt(period.EndPtr()).\n\t\tSetReason(why)",
			"if row.Data.Reason != nil && *row.Data.Reason != \"\" {\n\t\t\tperiods.Reasons[key] = *row.Data.Reason\n\t\t}",
		},
		"internal/crewresources/crew_held_roles.go": {
			"// @virtual\n\t// @suppress(readHandler)\n\t// @outlet(default)\n\t// @order(Username asc, Role asc, GroupName asc)",
			"// @primarykey\n\t\tRole string `spanner:\"Role\" index:\"true\"`",
			"// @enumerate(CrewListedGroups)\n\t\t// @primarykey\n\t\tGroupID   string     `spanner:\"GroupId\"   index:\"true\"`",
			"Reason    *string    `spanner:\"Reason\"`",
			"  m.Role AS Role,\n  '' AS GroupId,\n  '' AS GroupName,",
			"  m.Reason AS Reason\nFROM CrewUserRoles m\nJOIN CrewSessionUsers u ON u.Id = m.UserId\nWHERE " + standingSQL("m") + "\nUNION ALL\nSELECT",
			"  h.Role AS Role,\n  h.GroupId AS GroupId,\n  h.GroupName AS GroupName,",
			"  CAST(NULL AS STRING) AS Reason\nFROM (` + crewUserGroupRoleQuery + `) h\n`",
		},
		"internal/crewresources/crew_user_group_roles.go": {
			"CreatedAt time.Time  `spanner:\"CreatedAt\"`",
			"  GREATEST(r.CreatedAt, m.CreatedAt) AS CreatedAt,\n",
		},
		"internal/crewresources/crew_listed_groups.go": {
			"// @virtual\n\t// @outlet(default)\n\t// @order(Name asc)",
			"// @primarykey\n\t\t// @attribute(group)\n\t\tID          impulse.UUID `spanner:\"Id\"          index:\"true\"`",
			"People      int64        `spanner:\"People\"`\n\t\tRoles       []string     `spanner:\"Roles\"`",
			"  (SELECT COUNT(*) FROM CrewGroupMembers cm WHERE cm.GroupId = g.Id AND " + standingSQL("cm") + ") AS People,\n",
			// An application with no tenant record holds every role where its groups are kept.
			"  ARRAY(SELECT cr.Role FROM CrewGroupRoles cr WHERE cr.GroupId = g.Id AND " + standingSQL("cr") + " ORDER BY cr.Role) AS Roles\nFROM CrewGroups g\n`",
		},
		"internal/crewresources/crew_listed_group_roles.go": {
			"// @virtual\n\t// @suppress(readHandler)",
			"Losing    int64      `spanner:\"Losing\"`",
			"  r.EndsAt AS EndsAt,\n" + losingSQL() + "\nFROM CrewGroupRoles r\nJOIN CrewGroups g ON g.Id = r.GroupId\nWHERE " + standingSQL("r") + "\n`",
		},
		"internal/crewresources/crew_taken_roles.go": {
			"// @virtual\n\t// @suppress(readHandler)\n\t// @outlet(default)\n\t// @order(EndsAt desc, Username asc)",
			"Reason   string     `spanner:\"Reason\"`",
			"Ended    bool       `spanner:\"Ended\"    index:\"true\"`",
			"  (m.EndsAt <= CURRENT_TIMESTAMP()) AS Ended\nFROM CrewUserRoles m\nJOIN CrewSessionUsers u ON u.Id = m.UserId\nWHERE m.EndsAt IS NOT NULL AND m.Reason IS NOT NULL AND m.Reason != ''\n`",
		},
		// With no tenant record the taking names no tenant: the role is the whole
		// application's.
		"crewidentity/take_crew_role.go": {
			"// @rpc\n// @outlet(default)\n// @answers(204)\n// @class(access)\ntype TakeCrewRole struct {\n\t// Role is the role taken",
			"// @attribute(role)\n\t// @enumerate(CrewTakeableRoles)\n\tRole string `validate:\"required\"`",
			"// @enumerate(CrewTakeLengths)\n\tLength string `validate:\"required\"`",
			"Reason string `validate:\"required\"`\n}",
			"scope := accesstypes.GlobalPolicyScope()\n\tif err := client.manager.TakeRole(ctx, txn, scope, accesstypes.Role(m.Role), identity.TakeLength(m.Length), m.Reason); err != nil {",
		},
		"crewidentity/crew_takeable_roles.go": {
			"// @computed\n// @suppress(readHandler)\n// @outlet(default)\n// @order(Name asc)\ntype CrewTakeableRole struct {",
			"roles, err := client.manager.RolesToTake(ctx, AssignCrewRole{}.Method(), false)",
		},
		"crewidentity/crew_take_lengths.go": {
			"// @order(Minutes asc)\ntype CrewTakeLength struct {\n\t// @primarykey\n\tName    string `spanner:\"Name\"`\n\tMinutes int64  `spanner:\"Minutes\"`",
			"for _, length := range identity.TakeLengths() {",
		},
		// The taking is judged as the role given directly, so the client is handed the role
		// file's answers.
		"crewidentity/client.go": {"identity.WithRoleGrants(grants{})"},
		// A directory writes the Members auth's roles: a person's roles are one list, with
		// no group to come through and nothing taken by hand.
		"internal/membersresources/members_held_roles.go": {
			"  m.Reason AS Reason\nFROM MembersUserRoles m\nJOIN MembersSessionUsers u ON u.Id = m.UserId\nWHERE " + standingSQL("m") + "\n`",
		},
	})
	if held := readTreeFile(t, "internal/membersresources/members_held_roles.go"); strings.Contains(held, "UNION ALL") || strings.Contains(held, "@enumerate") {
		t.Errorf("the Members auth's one list of roles reads roles through a group or links to one, and the auth keeps none:\n%s", held)
	}
	for _, file := range identityTreeFiles(t, "pkg/identity") {
		for _, taken := range []string{"members_taken_roles", "take_members_role", "members_takeable_roles", "members_take_lengths", "members_listed_group"} {
			if strings.Contains(file, taken) {
				t.Errorf("%s is written, and a directory writes the Members auth's roles: nobody takes one by hand and it keeps no groups", file)
			}
		}
	}
	// The whole application's methods name a group by the list a page shows.
	for _, file := range []string{"add_crew_group_member.go", "give_crew_group_role.go", "describe_crew_group.go", "delete_crew_group.go"} {
		if content := readTreeFile(t, "crewidentity/"+file); !strings.Contains(content, "// @target(CrewListedGroup)\n\tGroupID") {
			t.Errorf("crewidentity/%s does not name its group by a row of CrewListedGroup:\n%s", file, content)
		}
	}
}

// Test_prepareIdentity_holdings_noGroups pins what an auth that keeps no groups is
// written of what is held, where the application gives the roles by hand and its schema
// holds no group tables: no list of the groups with what each holds and none of a
// group's roles with who loses each, a person's roles as one list that names no group to
// come through, and the taking of a role all the same.
func Test_prepareIdentity_holdings_noGroups(t *testing.T) {
	// As Test_prepareIdentity: the working directory is the process's, no t.Parallel.
	t.Chdir(t.TempDir())

	r := identityFixtureGenerator(t, slices.Concat(crewEveryTable, membersIdentityTables)...)
	if err := r.prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() error = %v", err)
	}

	for _, file := range identityTreeFiles(t, "pkg/identity") {
		if strings.Contains(file, "listed_group") {
			t.Errorf("%s is written, and the schema holds no group tables: neither auth keeps groups", file)
		}
	}
	if held := readTreeFile(t, "internal/crewresources/crew_held_roles.go"); strings.Contains(held, "UNION ALL") || strings.Contains(held, "@enumerate") {
		t.Errorf("the Crew auth's one list of roles reads roles through a group or links to one, and the auth keeps none:\n%s", held)
	}
	requireLines(t, map[string][]string{
		"internal/crewresources/crew_held_roles.go": {"  m.Reason AS Reason\nFROM CrewUserRoles m\nJOIN CrewSessionUsers u ON u.Id = m.UserId\nWHERE " + standingSQL("m") + "\n`"},
		"crewidentity/take_crew_role.go":            {"Reason string `validate:\"required\"`\n}"},
	})
}

// Test_prepareIdentity_holdings_tenant pins the same for an application with a tenant
// record: a tenant has one list of the roles held there, a list of its own beside the
// roles people hold themselves, a row named by its person, its role and the group it
// comes through and never read by its key alone, which the tenant's removal names its
// row by and refuses for a role that comes through a group; the tenant's roles carry the
// reason each was given for; a tenant's groups carry what each holds, and its groups' roles how many people lose each; the
// whole application's list of groups says where a role is held when that is not where
// the group is kept, in the application's word for a tenant; and the taking names the
// tenant and picks from the roles that exist in every one.
func Test_prepareIdentity_holdings_tenant(t *testing.T) {
	// As Test_prepareIdentity: the working directory is the process's, no t.Parallel.
	t.Chdir(t.TempDir())

	r := identityFixtureGenerator(t, slices.Concat(crewEveryTable, crewGroupTables, membersIdentityTables)...)
	r.resource = "pkg/resources"
	r.tableMap["Ships"] = shipsTable()
	writeResourcesPackage(t, "pkg/resources", map[string]string{"ships.go": shipTenantSource})
	if err := r.prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() error = %v", err)
	}

	requireLines(t, map[string][]string{
		"internal/crewresources/crew_ship_held_roles.go": {
			"// @virtual\n\t// @suppress(readHandler)\n\t// @permissionScope(domain)\n",
			"// @primarykey\n\t\t// @attribute(role)\n\t\tRole string `spanner:\"Role\" index:\"true\"`",
			"// @primarykey\n\t\tGroupID     string `spanner:\"GroupId\"     index:\"true\"`\n\t\tGroupName   string `spanner:\"GroupName\"   index:\"true\"`\n\t\tGlobalGroup bool   `spanner:\"GlobalGroup\"`",
			// A row opens the page of a group the tenant keeps, and no other.
			"// @enumerate(CrewShipGroups)\n\t\tGroupPage string     `spanner:\"GroupPage\"`",
			"  o.Role AS Role,\n  o.EveryDomain AS EveryDomain,\n  '' AS GroupId,\n  '' AS GroupName,\n  FALSE AS GlobalGroup,\n  '' AS GroupPage,",
			"  o.Reason AS Reason\nFROM (` + crewShipUserRoleQuery + `) o\nUNION ALL\nSELECT",
			"  IF(h.GlobalGroup, '', h.GroupId) AS GroupPage,",
			"  h.Role AS Role,\n  h.EveryDomain AS EveryDomain,\n  h.GroupId AS GroupId,",
			"  CAST(NULL AS STRING) AS Reason\nFROM (` + crewShipUserGroupRoleQuery + `) h\n`",
		},
		// The tenant's own list of the roles people hold themselves stays as it was, a row
		// named by its person and its role, with the reason beside the period.
		"internal/crewresources/crew_ship_user_roles.go": {
			"// @primarykey\n\t\t// @attribute(role)\n\t\tRole string `spanner:\"Role\" index:\"true\"`\n\t\t// @attribute(everyDomain)\n\t\tEveryDomain bool       `spanner:\"EveryDomain\" index:\"true\"`\n\t\tCreatedAt",
			"EndsAt      *time.Time `spanner:\"EndsAt\"`\n\t\tReason      *string    `spanner:\"Reason\"`\n\t}",
			"  t.EndsAt AS EndsAt,\n  t.Reason AS Reason\nFROM (",
		},
		"internal/crewresources/crew_ship_user_group_roles.go": {"  GREATEST(t.CreatedAt, m.CreatedAt) AS CreatedAt,\n"},
		"crewidentity/remove_crew_ship_role.go": {
			"// @target(CrewShipHeldRole, key: UserID)\n\tUserID impulse.UUID\n\t// @target(CrewShipHeldRole, key: Role)\n\tRole string `validate:\"required\"`",
			"// @target(CrewShipHeldRole, key: GroupID)\n\tGroupID string\n}",
			"if m.GroupID != \"\" {\n\t\treturn httpio.NewBadRequestMessage(\"that role comes to the person through a group",
		},
		"crewidentity/assign_crew_ship_role.go": {"\tReason *string\n}", "identity.RequestReason(m.Reason)"},
		"internal/crewresources/crew_ship_groups.go": {
			"People      int64     `spanner:\"People\"`\n\t\tRoles       []string  `spanner:\"Roles\"`",
			"  (SELECT COUNT(*) FROM CrewGroupMembers cm WHERE cm.GroupId = g.Id AND " + standingSQL("cm") + ") AS People,\n",
			"      WHEN cr.Kind = g.Kind AND cr.Axis = g.Axis AND cr.DomainId = g.DomainId THEN cr.Role\n",
			"    WHERE cr.GroupId = g.Id AND " + standingSQL("cr") + "\n    ORDER BY cr.Role, cr.Kind, cr.DomainId\n  ) AS Roles\nFROM CrewGroups g\nWHERE g.Kind = 'domain' AND g.Axis = ''",
		},
		"internal/crewresources/crew_listed_groups.go": {
			"      WHEN cr.Kind = 'every' THEN CONCAT(cr.Role, ' in every ship')\n      WHEN cr.Kind = 'domain' THEN CONCAT(cr.Role, ' in ship ', cr.DomainId)\n      ELSE CONCAT(cr.Role, ' for the whole application')\n    END\n    FROM CrewGroupRoles cr\n",
		},
		"internal/crewresources/crew_ship_group_roles.go": {
			"Losing    int64      `spanner:\"Losing\"`",
			"  r.EndsAt AS EndsAt,\n" + losingSQL() + "\nFROM CrewGroups g\nJOIN CrewGroupRoles r ON r.GroupId = g.Id\n",
		},
		"crewidentity/take_crew_role.go": {
			"type TakeCrewRole struct {\n\t// Domain is the ship the role is taken in",
			"Domain string `validate:\"required\"`\n\t// Role is the role taken",
			"scope := accesstypes.DomainPolicyScope(accesstypes.Domain(m.Domain))\n\tif err := client.manager.TakeRole(ctx, txn, scope, accesstypes.Role(m.Role), identity.TakeLength(m.Length), m.Reason); err != nil {",
		},
		"crewidentity/crew_takeable_roles.go": {"a role that exists in every ship", "roles, err := client.manager.RolesToTake(ctx, AssignCrewRole{}.Method(), true)"},
	})
}

// Test_identityInstance_rolesFrom pins the words of the directories that decide an
// auth's roles: the labels of the registrations that write the roles of the people they
// sign in, each once and in declaration order, joined as a sentence says them, and
// nothing for an auth whose roles the application gives by hand.
func Test_identityInstance_rolesFrom(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		signIns []identitySignIn
		want    string
	}{
		{name: "roles given by hand", signIns: []identitySignIn{{Name: "password", Label: "Password"}, {Name: "google", Label: "Google"}}},
		{name: "one directory", signIns: []identitySignIn{{Name: "password", Label: "Password"}, {Name: "google", Label: "Google", syncsRoles: true}}, want: "Google"},
		{
			name:    "two directories",
			signIns: []identitySignIn{{Name: "google", Label: "Google", syncsRoles: true}, {Name: "entra", Label: "Microsoft", syncsRoles: true}},
			want:    "Google or Microsoft",
		},
		{
			name: "three, one label twice",
			signIns: []identitySignIn{
				{Name: "google", Label: "Google", syncsRoles: true},
				{Name: "lakeside", Label: "Google", syncsRoles: true},
				{Name: "entra", Label: "Microsoft", syncsRoles: true},
				{Name: "okta", Label: "Okta", syncsRoles: true},
			},
			want: "Google, Microsoft or Okta",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			inst := &identityInstance{signIns: tt.signIns}
			if diff := cmp.Diff(tt.want, inst.rolesFrom()); diff != "" {
				t.Errorf("rolesFrom() (-want +got):\n%s", diff)
			}
		})
	}
}

// Test_identityOn_holdings pins what the generated API description says of what is held
// and why: the whole application's side names a person's roles by the one list, the
// groups and their roles by the lists that say what each holds, and the roles held for
// a set time with the method that takes one, each only where the outlet serves it; and
// an auth whose directory writes its roles names the directory, in the words of its
// login page, a quote in them written so the description still reads.
func Test_identityOn_holdings(t *testing.T) {
	t.Parallel()

	tables := map[identityTable]bool{identityUsers: true, identityMemberships: true, identityGroups: true, identityGroupMembers: true, identityGroupRoles: true}
	crew := &identityInstance{auth: "Crew", outlets: []string{"console"}, tables: tables}
	members := &identityInstance{
		auth: "Members", outlets: []string{"console"}, rolesSynced: true,
		tables:  map[identityTable]bool{identityUsers: true, identityMemberships: true},
		signIns: []identitySignIn{{Name: "google", Label: "Lakeside's Google", syncsRoles: true}, {Name: "entra", Label: "Microsoft", syncsRoles: true}},
	}
	served := []string{
		"CrewSessionUsers", "CrewUserRoles", "CrewHeldRoles", "CrewGroups", "CrewListedGroups", "CrewGroupRoles", "CrewListedGroupRoles", "CrewTakenRoles",
		"MembersSessionUsers", "MembersUserRoles", "MembersHeldRoles",
	}
	data := &tsAPIData{Methods: []*tsAPIMethod{{Name: "TakeCrewRole"}, {Name: "DeleteCrewGroup"}}}
	for _, name := range served {
		data.Resources = append(data.Resources, &tsAPIResource{Name: name})
	}
	part := func(key, name string) identityPart {
		return identityPart{Key: key, Name: name}
	}

	generator := &typescriptGenerator{client: &client{identity: []*identityInstance{crew, members}}, outletName: "console"}
	want := []*tsAPIIdentity{
		{
			Auth: "Crew", Label: "Crew",
			Sides: []*tsAPIIdentitySide{{
				Scope: accesstypes.GlobalPermissionScope,
				Lists: []identityPart{
					part("users", "CrewSessionUsers"), part("roles", "CrewHeldRoles"), part("groups", "CrewListedGroups"),
					part("groupRoles", "CrewListedGroupRoles"), part("takenRoles", "CrewTakenRoles"),
				},
				Methods: []identityPart{part("deleteGroup", "DeleteCrewGroup"), part("takeRole", "TakeCrewRole")},
			}},
		},
		{
			Auth: "Members", Label: "Members", RolesFrom: `Lakeside\'s Google or Microsoft`,
			Sides: []*tsAPIIdentitySide{{
				Scope: accesstypes.GlobalPermissionScope,
				Lists: []identityPart{part("users", "MembersSessionUsers"), part("roles", "MembersHeldRoles")},
			}},
		},
	}
	if diff := cmp.Diff(want, generator.identityOn(data), cmp.AllowUnexported(identityPart{})); diff != "" {
		t.Errorf("identityOn() (-want +got):\n%s", diff)
	}
}
