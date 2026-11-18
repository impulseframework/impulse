package generation

import (
	"slices"
	"strings"
	"testing"
)

// countingSQL is how a list of people reads that a row counts now, under the alias the
// row is read by: its period has begun and has not ended.
func countingSQL(alias string) string {
	return "(" + alias + ".StartsAt IS NULL OR " + alias + ".StartsAt <= CURRENT_TIMESTAMP()) AND " + standingSQL(alias)
}

// standingSQL is how a list of roles or of places reads that a row has not ended, under
// the alias the row is read by.
func standingSQL(alias string) string {
	return "(" + alias + ".EndsAt IS NULL OR " + alias + ".EndsAt > CURRENT_TIMESTAMP())"
}

// requireLines fails for each line a file of the fixture's tree lacks.
func requireLines(t *testing.T, files map[string][]string) {
	t.Helper()

	for file, lines := range files {
		content := readTreeFile(t, file)
		for _, line := range lines {
			if !strings.Contains(content, line) {
				t.Errorf("%s lacks %q:\n%s", file, line, content)
			}
		}
	}
}

// Test_prepareIdentity_periods pins the period of an assignment in what an auth is
// written: the two columns on the declarations over the memberships, the group members
// and the group roles; the where: part of the subject set of a person's groups; the two
// optional times of each method that gives a role, a place in a group or a group's role,
// handed to the rule that gives it for a period; the store's reads and writes of the
// periods, the removal of ended rows under the source that says they ended by
// themselves; the client's hook for a sign-in that stands; and the lists, which carry
// the period and leave out a row that has ended. An auth whose directory writes its
// roles gives none by hand: it has no method that takes a period and no hook.
func Test_prepareIdentity_periods(t *testing.T) {
	// As Test_prepareIdentity: the working directory is the process's, no t.Parallel.
	t.Chdir(t.TempDir())

	r := identityFixtureGenerator(t, slices.Concat(crewEveryTable, crewGroupTables, membersIdentityTables, membersGroupTables)...)
	r.extraOutlets[1].auth = &outletAuth{importPath: membersAuthPath, methods: []AuthMethod{AzureMethod("directory").SyncsRoles()}}
	if err := r.prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() error = %v", err)
	}

	const (
		periodFields = "StartsAt *time.Time\n\t// EndsAt is the first instant it no longer counts. Left out, it counts until it is\n\t// taken away.\n\tEndsAt *time.Time\n}"
		// A person's role carries the reason after its period (identity_holdings_test.go).
		periodAndReasonFields = "StartsAt *time.Time\n\t// EndsAt is the first instant it no longer counts. Left out, it counts until it is\n\t// taken away.\n\tEndsAt *time.Time\n\t// Reason says why the role is given"
		requestPeriod         = "period, err := identity.RequestPeriod(m.StartsAt, m.EndsAt)\n\tif err != nil {\n\t\treturn errors.Wrap(err, \"identity.RequestPeriod()\")\n\t}"
	)
	requireLines(t, map[string][]string{
		"internal/crewresources/crew_user_roles.go": {
			"CreatedAt time.Time  `spanner:\"CreatedAt\"`\n\t\tStartsAt  *time.Time `spanner:\"StartsAt\"`\n\t\tEndsAt    *time.Time `spanner:\"EndsAt\"`\n\t\tReason    *string    `spanner:\"Reason\"`\n\t}",
		},
		"internal/crewresources/crew_group_members.go": {
			// A place counts in a condition inside its period alone, as it does on a check.
			"// @subjectSet(groups, value: GroupID, where: (StartsAt IS NULL OR StartsAt <= now) AND (EndsAt IS NULL OR EndsAt > now))",
			"StartsAt  *time.Time `spanner:\"StartsAt\"`\n\t\tEndsAt    *time.Time `spanner:\"EndsAt\"`\n\t}",
		},
		"internal/crewresources/crew_group_roles.go": {
			"StartsAt  *time.Time   `spanner:\"StartsAt\"`\n\t\tEndsAt    *time.Time   `spanner:\"EndsAt\"`\n\t}",
		},
		"crewidentity/assign_crew_role.go": {
			periodAndReasonFields, requestPeriod, "client.manager.AssignRoleFor(ctx, txn, scope, m.UserID, accesstypes.Role(m.Role), period, identity.RequestReason(m.Reason))",
			"The Super\n// Administrator role takes no period: there is always one.",
		},
		"crewidentity/add_crew_group_member.go": {periodFields, requestPeriod, "client.manager.AddGroupMemberFor(ctx, txn, m.GroupID, m.UserID, period)"},
		"crewidentity/give_crew_group_role.go":  {periodFields, requestPeriod, "client.manager.GiveGroupRoleFor(ctx, txn, m.GroupID, scope, accesstypes.Role(m.Role), period)"},
		"crewidentity/store.go": {
			// Giving again replaces the period: both columns are written, an absent one as NULL.
			"crewresources.NewCrewUserRoleUpdatePatch(key.Kind, key.Axis, key.Domain, string(key.Role), string(key.User)).\n\t\tSetStartsAt(period.StartPtr()).\n\t\tSetEndsAt(period.EndPtr())",
			"crewresources.NewCrewGroupMemberUpdatePatch(id, string(key.User)).\n\t\tSetStartsAt(period.StartPtr()).\n\t\tSetEndsAt(period.EndPtr())",
			"crewresources.NewCrewGroupRoleUpdatePatch(id, key.Kind, key.Axis, key.Domain, string(key.Role)).\n\t\tSetStartsAt(period.StartPtr()).\n\t\tSetEndsAt(period.EndPtr())",
			// The sign-in's one read is of the person's own rows, by the index that leads with them.
			"func (store) UserPeriods(ctx context.Context, txn resource.ReadOnlyTransaction, userID impulse.UUID) (*identity.UserPeriods, error) {",
			"AddColumns(crewresources.NewCrewUserRoleColumns().Kind().Axis().DomainID().Role().UserID().StartsAt().EndsAt().Reason()).\n\t\tWhere(crewresources.NewCrewUserRoleQueryClause().UserID().Equal(userID.String()))",
			"AddColumns(crewresources.NewCrewGroupMemberColumns().GroupID().UserID().StartsAt().EndsAt()).\n\t\tWhere(crewresources.NewCrewGroupMemberQueryClause().UserID().Equal(userID.String()))",
			"func (store) GroupPeriods(ctx context.Context, txn resource.ReadOnlyTransaction, id impulse.UUID) (*identity.GroupPeriods, error) {",
			// An ended row goes under the source that says nobody removed it.
			"patch := crewresources.NewCrewUserRoleDeletePatch(key.Kind, key.Axis, key.Domain, string(key.Role), string(key.User))\n\t\tif err := patch.Buffer(ctx, txn, identity.EndedByItself); err != nil {",
			"crewresources.NewCrewGroupMemberDeletePatch(id, string(place.User)).Buffer(ctx, txn, identity.EndedByItself)",
		},
		"crewidentity/client.go": {
			"func (c *Client) SignedIn(db resource.Client) session.SignedInHook {\n\treturn c.manager.SignedIn(\"Crew\", db)\n}",
		},
		"internal/crewresources/crew_group_users.go": {
			"m.CreatedAt AS CreatedAt,\n  m.StartsAt AS StartsAt,\n  m.EndsAt AS EndsAt\nFROM CrewGroups g",
			"JOIN CrewSessionUsers u ON u.Id = m.UserId\nWHERE " + standingSQL("m") + "\n`",
		},
		"internal/crewresources/crew_user_group_roles.go": {
			// The period a person holds a role for through a group is where the place's and
			// the group role's meet.
			"COALESCE(GREATEST(r.StartsAt, m.StartsAt), r.StartsAt, m.StartsAt) AS StartsAt,\n  COALESCE(LEAST(r.EndsAt, m.EndsAt), r.EndsAt, m.EndsAt) AS EndsAt\nFROM CrewGroupRoles r",
			"  AND " + standingSQL("r") + "\n  AND " + standingSQL("m") + "\n  AND (r.EndsAt IS NULL OR m.StartsAt IS NULL OR m.StartsAt < r.EndsAt)\n  AND (m.EndsAt IS NULL OR r.StartsAt IS NULL OR r.StartsAt < m.EndsAt)\n`",
		},
		// A directory writes the Members auth's roles: its memberships hold the two
		// columns as every policy store's do, and its store reads them, but nothing of it
		// gives a period and it keeps no groups.
		"internal/membersresources/members_user_roles.go": {"StartsAt  *time.Time `spanner:\"StartsAt\"`"},
		"membersidentity/store.go": {
			"membersresources.NewMembersUserRoleUpdatePatch(key.Kind, key.Axis, key.Domain, string(key.Role), string(key.User))",
			"func (store) SetGroupMemberPeriod(context.Context, resource.ReadWriteTransaction, *access.GroupMemberKey, accesstypes.Period) error {\n\treturn errNoGroups\n}",
			"func (store) GroupPeriods(context.Context, resource.ReadOnlyTransaction, impulse.UUID) (*identity.GroupPeriods, error) {\n\treturn &identity.GroupPeriods{}, nil\n}",
			"if len(places) != 0 {\n\t\treturn errNoGroups\n\t}",
		},
	})
	if client := readTreeFile(t, "membersidentity/client.go"); strings.Contains(client, "SignedIn") {
		t.Errorf("membersidentity/client.go answers a hook that clears ended roles, and a directory writes the auth's roles:\n%s", client)
	}
	for _, file := range identityTreeFiles(t, "pkg/identity") {
		if strings.HasPrefix(file, "membersidentity/") && file != "membersidentity/store.go" && strings.Contains(readTreeFile(t, file), "StartsAt") {
			t.Errorf("%s takes a period, and a directory writes the Members auth's roles", file)
		}
	}
}

// Test_prepareIdentity_periods_tenant pins the period on a tenant's side: its three
// methods that give a role, a place in a group and a group's role take the two times;
// its list of people holds who holds something at the moment, a role or a place outside
// its period putting nobody there; the two facts about a person read what has not
// ended; and its lists of roles carry the period and leave out a row that has ended.
func Test_prepareIdentity_periods_tenant(t *testing.T) {
	// As Test_prepareIdentity: the working directory is the process's, no t.Parallel.
	t.Chdir(t.TempDir())

	r := identityFixtureGenerator(t, slices.Concat(crewEveryTable, crewGroupTables, membersIdentityTables)...)
	r.resource = "pkg/resources"
	r.tableMap["Ships"] = shipsTable()
	writeResourcesPackage(t, "pkg/resources", map[string]string{"ships.go": shipTenantSource})
	if err := r.prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() error = %v", err)
	}

	const requestPeriod = "period, err := identity.RequestPeriod(m.StartsAt, m.EndsAt)"
	requireLines(t, map[string][]string{
		"crewidentity/assign_crew_ship_role.go":      {"\tStartsAt *time.Time\n", "\tEndsAt *time.Time\n\t// Reason says why the role is given", requestPeriod, "tenant.AssignRoleFor(ctx, txn, m.UserID, accesstypes.Role(m.Role), period, identity.RequestReason(m.Reason))"},
		"crewidentity/add_crew_ship_group_member.go": {"\tStartsAt *time.Time\n", "\tEndsAt *time.Time\n}", requestPeriod, "tenant.AddGroupMemberFor(ctx, txn, m.GroupID, m.UserID, period)"},
		"crewidentity/give_crew_ship_group_role.go":  {"\tStartsAt *time.Time\n", "\tEndsAt *time.Time\n}", requestPeriod, "tenant.GiveGroupRoleFor(ctx, txn, m.GroupID, accesstypes.Role(m.Role), period)"},
		"internal/crewresources/crew_ship_users.go": {
			// Who holds a role of their own in the tenant, or in every tenant, now.
			"FROM CrewUserRoles m\n  WHERE m.Kind = 'domain' AND m.Axis = ''\n    AND " + countingSQL("m") + "\n  UNION DISTINCT",
			"CROSS JOIN Ships s\n  WHERE m.Kind = 'every' AND m.Axis = ''\n    AND " + countingSQL("m") + "\n  UNION DISTINCT",
			// Who a group puts there now: the place counts, and so does the group's role.
			"WHERE g.Kind = 'domain' AND g.Axis = ''\n    AND " + countingSQL("gm") + "\n  UNION DISTINCT",
			"AND r.Role != 'SuperAdministrator'\n    AND " + countingSQL("r") + "\n    AND " + countingSQL("gm") + "\n  UNION DISTINCT",
			// The facts read what has not ended: a role that will count is one held there.
			"WHERE g.UserId = u.Id AND g.Kind = 'global'\n      AND " + standingSQL("g") + "\n  )",
		},
		"internal/crewresources/crew_ship_user_roles.go": {
			"CreatedAt   time.Time  `spanner:\"CreatedAt\"`\n\t\tStartsAt    *time.Time `spanner:\"StartsAt\"`\n\t\tEndsAt      *time.Time `spanner:\"EndsAt\"`\n\t\tReason      *string    `spanner:\"Reason\"`\n\t}",
			"t.CreatedAt AS CreatedAt,\n  t.StartsAt AS StartsAt,\n  t.EndsAt AS EndsAt,\n  t.Reason AS Reason\nFROM (",
			"WHERE m.Kind = 'every' AND m.Axis = ''\n    AND " + standingSQL("m") + "\n    AND NOT EXISTS (",
			// A row of the tenant's own that has ended does not stand in front of the one
			// held in every tenant.
			"AND d.Role = m.Role AND d.UserId = m.UserId\n        AND " + standingSQL("d") + "\n    )",
		},
		"internal/crewresources/crew_ship_group_users.go": {
			"m.StartsAt AS StartsAt,\n  m.EndsAt AS EndsAt\nFROM CrewGroups g", "WHERE g.Kind = 'domain' AND g.Axis = ''\n  AND " + standingSQL("m") + "\n`",
		},
		"internal/crewresources/crew_ship_group_roles.go": {
			"r.StartsAt AS StartsAt,\n  r.EndsAt AS EndsAt\nFROM CrewGroups g", "AND r.DomainId = g.DomainId\n  AND " + standingSQL("r") + "\n`",
		},
		"internal/crewresources/crew_ship_user_group_roles.go": {
			"COALESCE(GREATEST(t.StartsAt, m.StartsAt), t.StartsAt, m.StartsAt) AS StartsAt,\n  COALESCE(LEAST(t.EndsAt, m.EndsAt), t.EndsAt, m.EndsAt) AS EndsAt\nFROM (",
			// A group that holds a role both ways is one row, the one that counts now: the
			// tenant's own row gives way while it has not begun and the one held in every
			// tenant counts, and stands in front of it otherwise.
			"WHERE r.Kind = 'domain' AND r.Axis = ''\n    AND " + standingSQL("r") + "\n    AND ((" + countingSQL("r") + ") OR NOT EXISTS (\n" +
				"      SELECT 1 FROM CrewGroupRoles e\n      WHERE e.GroupId = r.GroupId AND e.Kind = 'every' AND e.Axis = r.Axis AND e.Role = r.Role\n        AND " + countingSQL("e") + "\n    ))\n  UNION ALL",
			"AND d.DomainId = s.Id AND d.Role = r.Role\n        AND " + standingSQL("d") + "\n        AND ((" + countingSQL("d") + ") OR NOT (" + countingSQL("r") + "))\n    )",
			"AND g.DomainId = t.DomainId))\n  AND " + standingSQL("m") + "\n  AND (t.EndsAt IS NULL OR m.StartsAt IS NULL OR m.StartsAt < t.EndsAt)\n  AND (m.EndsAt IS NULL OR t.StartsAt IS NULL OR t.StartsAt < m.EndsAt)\n`",
		},
	})
}

// Test_prepareIdentity_periods_absent pins the store of an auth whose schema holds no
// group tables: its memberships carry a period, and it answers that no place and no
// group's role has one.
func Test_prepareIdentity_periods_absent(t *testing.T) {
	// As Test_prepareIdentity: the working directory is the process's, no t.Parallel.
	t.Chdir(t.TempDir())

	r := identityFixtureGenerator(t, slices.Concat(crewEveryTable, membersIdentityTables)...)
	if err := r.prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() error = %v", err)
	}

	requireLines(t, map[string][]string{
		"crewidentity/store.go": {
			"func (store) SetMembershipPeriod(ctx context.Context, txn resource.ReadWriteTransaction, key *access.MembershipKey, period accesstypes.Period, reason string) error {",
			"func (store) SetGroupRolePeriod(context.Context, resource.ReadWriteTransaction, *access.GroupRoleKey, accesstypes.Period) error {\n\treturn errNoGroups\n}",
			"if len(places) != 0 {\n\t\treturn errNoGroups\n\t}",
		},
	})
	if store := readTreeFile(t, "crewidentity/store.go"); strings.Contains(store, "GroupMemberQuery") {
		t.Errorf("crewidentity/store.go reads a group table the schema does not hold:\n%s", store)
	}
}
