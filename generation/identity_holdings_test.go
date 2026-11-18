package generation

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
)

// Test_prepareIdentity_holdings pins what an auth is written of the reason a role is
// given for and of the roles held for a set time, for the whole application: the reason
// on the declaration over the memberships and on the method that gives a role, with the
// store's write of it; the roles held for a set time with a reason; and the taking of a
// role with the two lists its form picks from. An auth whose directory writes its roles
// takes none.
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
		"internal/crewresources/crew_taken_roles.go": {
			"// @virtual\n\t// @suppress(readHandler)\n\t// @outlet(default)\n\t// @order(EndsAt desc, Username asc)",
			"Reason   string     `spanner:\"Reason\"`",
			"Ended    bool       `spanner:\"Ended\"    index:\"true\"`",
			"  (m.EndsAt <= CURRENT_TIMESTAMP()) AS Ended\nFROM CrewUserRoles m\nJOIN CrewSessionUsers u ON u.Id = m.UserId\nWHERE m.EndsAt IS NOT NULL AND m.Reason IS NOT NULL AND m.Reason != ''\n`",
		},
		// With no tenant record the taking names no tenant: the role is the whole
		// application's.
		"crewidentity/take_crew_role.go": {
			"// @rpc\n// @outlet(default)\n// @answers(204)\ntype TakeCrewRole struct {\n\t// Role is the role taken",
			"// @attribute(role)\n\t// @enumerate(CrewTakeableRoles)\n\tRole string `validate:\"required\"`",
			"// @enumerate(CrewTakeLengths)\n\tLength string `validate:\"required\"`",
			"Reason string `validate:\"required\"`\n}",
			"scope := accesstypes.GlobalPolicyScope()\n\tif err := client.manager.TakeRole(ctx, txn, scope, accesstypes.Role(m.Role), identity.TakeLength(m.Length), m.Reason); err != nil {",
		},
		"crewidentity/crew_takeable_roles.go": {
			"// @computed\n// @suppress(readHandler)\n// @outlet(default)\n// @order(Name asc)\ntype CrewTakeableRole struct {",
			"roles, err := client.manager.RolesToTake(ctx, false)",
		},
		"crewidentity/crew_take_lengths.go": {
			"// @order(Minutes asc)\ntype CrewTakeLength struct {\n\t// @primarykey\n\tName    string `spanner:\"Name\"`\n\tMinutes int64  `spanner:\"Minutes\"`",
			"for _, length := range identity.TakeLengths() {",
		},
		// The taking is judged as the role given directly, so the client is handed the role
		// file's answers.
		"crewidentity/client.go": {"identity.WithRoleGrants(grants{})"},
	})
	// A directory writes the Members auth's roles: nothing is taken by hand.
	for _, file := range identityTreeFiles(t, "pkg/identity") {
		for _, taken := range []string{"members_taken_roles", "take_members_role", "members_takeable_roles", "members_take_lengths"} {
			if strings.Contains(file, taken) {
				t.Errorf("%s is written, and a directory writes the Members auth's roles: nobody takes one by hand", file)
			}
		}
	}
}

// Test_prepareIdentity_holdings_tenant pins the same for an application with a tenant
// record: the tenant's roles carry the reason each was given for, and the taking names
// the tenant and picks from the roles that exist in every one.
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
		// The tenant's own list of the roles people hold, a row named by its person and its
		// role, with the reason beside the period.
		"internal/crewresources/crew_ship_user_roles.go": {
			"// @primarykey\n\t\t// @attribute(role)\n\t\tRole string `spanner:\"Role\" index:\"true\"`\n\t\t// @attribute(everyDomain)\n\t\tEveryDomain bool       `spanner:\"EveryDomain\" index:\"true\"`\n\t\tCreatedAt",
			"EndsAt      *time.Time `spanner:\"EndsAt\"`\n\t\tReason      *string    `spanner:\"Reason\"`\n\t}",
			"  t.EndsAt AS EndsAt,\n  t.Reason AS Reason\nFROM (",
		},
		"crewidentity/assign_crew_ship_role.go": {"\tReason *string\n}", "identity.RequestReason(m.Reason)"},
		"crewidentity/take_crew_role.go": {
			"type TakeCrewRole struct {\n\t// Domain is the ship the role is taken in",
			"Domain string `validate:\"required\"`\n\t// Role is the role taken",
			"scope := accesstypes.DomainPolicyScope(accesstypes.Domain(m.Domain))\n\tif err := client.manager.TakeRole(ctx, txn, scope, accesstypes.Role(m.Role), identity.TakeLength(m.Length), m.Reason); err != nil {",
		},
		"crewidentity/crew_takeable_roles.go": {"a role that exists in every ship", "roles, err := client.manager.RolesToTake(ctx, true)"},
	})
}

// Test_identityOn_holdings pins what the generated API description says of the roles
// held for a set time: the whole application's side names their list and the method
// that takes one, each only where the outlet serves it, and an auth whose directory
// writes its roles names neither.
func Test_identityOn_holdings(t *testing.T) {
	t.Parallel()

	tables := map[identityTable]bool{identityUsers: true, identityMemberships: true, identityGroups: true, identityGroupMembers: true, identityGroupRoles: true}
	crew := &identityInstance{auth: "Crew", outlets: []string{"console"}, tables: tables}
	members := &identityInstance{
		auth: "Members", outlets: []string{"console"}, rolesSynced: true,
		tables: map[identityTable]bool{identityUsers: true, identityMemberships: true},
	}
	served := []string{"CrewSessionUsers", "CrewUserRoles", "CrewGroups", "CrewGroupRoles", "CrewTakenRoles", "MembersSessionUsers", "MembersUserRoles"}
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
					part("users", "CrewSessionUsers"), part("roles", "CrewUserRoles"), part("groups", "CrewGroups"),
					part("groupRoles", "CrewGroupRoles"), part("takenRoles", "CrewTakenRoles"),
				},
				Methods: []identityPart{part("deleteGroup", "DeleteCrewGroup"), part("takeRole", "TakeCrewRole")},
			}},
		},
		{
			Auth: "Members", Label: "Members",
			Sides: []*tsAPIIdentitySide{{
				Scope: accesstypes.GlobalPermissionScope,
				Lists: []identityPart{part("users", "MembersSessionUsers"), part("roles", "MembersUserRoles")},
			}},
		},
	}
	if diff := cmp.Diff(want, generator.identityOn(data), cmp.AllowUnexported(identityPart{})); diff != "" {
		t.Errorf("identityOn() (-want +got):\n%s", diff)
	}
}
