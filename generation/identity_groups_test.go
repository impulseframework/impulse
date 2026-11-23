package generation

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
)

// The group tables of the fixture's two auths, as the permission store names them under
// each prefix.
var (
	crewGroupTables    = []string{"CrewGroups", "CrewGroupMembers", "CrewGroupRoles"}
	membersGroupTables = []string{"MembersGroups", "MembersGroupMembers", "MembersGroupRoles"}
)

// groupFiles are the files of the tree that hold something of the groups of people.
func groupFiles(t *testing.T, tree string) []string {
	t.Helper()

	var files []string
	for _, file := range identityTreeFiles(t, tree) {
		if strings.Contains(file, "group") {
			files = append(files, file)
		}
	}

	return files
}

// readTreeFile reads one file of the fixture's tree.
func readTreeFile(t *testing.T, file string) string {
	t.Helper()

	content, err := os.ReadFile(filepath.Join("pkg", "identity", file))
	if err != nil {
		t.Fatalf("os.ReadFile(%s) error = %v", file, err)
	}

	return string(content)
}

// Test_prepareIdentity_groups pins what an auth that keeps groups is written: the
// declarations over the three group tables, the members' carrying the subject set of a
// person's groups; the lists of a group's people and of the roles held through a group;
// the seven methods; and the store's reads and writes. An auth whose directory writes
// its roles keeps none, whatever tables the schema holds: nothing of groups is written
// for it, and its store answers that nobody is in a group.
func Test_prepareIdentity_groups(t *testing.T) {
	// As Test_prepareIdentity: the working directory is the process's, no t.Parallel.
	t.Chdir(t.TempDir())

	r := identityFixtureGenerator(t, slices.Concat(crewEveryTable, crewGroupTables, membersIdentityTables, membersGroupTables)...)
	r.extraOutlets[1].auth = &outletAuth{importPath: membersAuthPath, methods: []AuthMethod{AzureMethod("directory").SyncsRoles()}}
	if err := r.prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() error = %v", err)
	}

	want := []string{
		"crewidentity/add_crew_group_member.go",
		"crewidentity/create_crew_group.go",
		"crewidentity/delete_crew_group.go",
		"crewidentity/describe_crew_group.go",
		"crewidentity/give_crew_group_role.go",
		"crewidentity/remove_crew_group_member.go",
		"crewidentity/take_crew_group_role.go",
		"internal/crewresources/crew_group_members.go",
		"internal/crewresources/crew_group_roles.go",
		"internal/crewresources/crew_group_users.go",
		"internal/crewresources/crew_groups.go",
		"internal/crewresources/crew_listed_group_roles.go",
		"internal/crewresources/crew_listed_groups.go",
		"internal/crewresources/crew_user_group_roles.go",
	}
	if diff := cmp.Diff(want, groupFiles(t, "pkg/identity")); diff != "" {
		t.Errorf("the files of the groups (-want +got):\n%s", diff)
	}

	for file, lines := range map[string][]string{
		"internal/crewresources/crew_groups.go": {
			generationHeader, "// @resource", "// @suppress(patchHandler)", "// @outlet(default)", "// @order(Name asc)", "CrewGroup struct {",
			"// @attribute(group)\n\t\tID          impulse.UUID `spanner:\"Id\"`",
			// The store finds a group by where it is kept and its name, the key of the
			// table's unique index, whose leading column alone has a seek of its own.
			"Axis        string       `spanner:\"Axis\"        allow_filter:\"true\"`", "Name        string       `spanner:\"Name\"        allow_filter:\"true\"`",
		},
		"internal/crewresources/crew_group_members.go": {
			// A grant's condition reads the group of a row (group), and the set the same
			// table declares is what it is tested against (group IN subject.crewGroups).
			"// @suppress(readHandler)", "CrewGroupMember struct {", "// @attribute(group)\n\t\tGroupID impulse.UUID `spanner:\"GroupId\"`",
			// The one auth that keeps groups declares the set under its own name, and
			// the set names the auth whose groups they are.
			"// @subjectSet(crewGroups, value: GroupID, domain: GroupID.DomainID, auth: crew, where: (StartsAt IS NULL OR StartsAt <= now) AND (EndsAt IS NULL OR EndsAt > now))\n\t\tUserID    string     `spanner:\"UserId\"`",
		},
		"internal/crewresources/crew_group_roles.go": {"// @suppress(readHandler)", "CrewGroupRole struct {", "// @roleOf(Crew)\n\t\tRole      string     `spanner:\"Role\"`"},
		"internal/crewresources/crew_group_users.go": {
			"// @virtual", "CrewGroupUser struct {", "// @primarykey\n\t\t// @attribute(group)\n\t\tGroupID impulse.UUID", "// @primarykey\n\t\tUserID    impulse.UUID",
			"FROM CrewGroups g\nJOIN CrewGroupMembers m ON m.GroupId = g.Id\nJOIN CrewSessionUsers u ON u.Id = m.UserId\nWHERE " + standingSQL("m") + "\n`",
		},
		"internal/crewresources/crew_user_group_roles.go": {
			"// @virtual", "CrewUserGroupRole struct {", "// @order(Username asc, GroupName asc, Role asc)",
			"FROM CrewGroupRoles r\nJOIN CrewGroups g ON g.Id = r.GroupId\nJOIN CrewGroupMembers m ON m.GroupId = r.GroupId\nJOIN CrewSessionUsers u ON u.Id = m.UserId",
			// The list shows what the permission engine gives: no Super Administrator
			// through a group, and a tenant's group gives its roles in its tenant alone.
			"WHERE r.Role != 'SuperAdministrator'\n  AND (g.Kind = 'global' OR (r.Kind = g.Kind AND r.Axis = g.Axis AND r.DomainId = g.DomainId))",
		},
		"crewidentity/create_crew_group.go": {
			"// @rpc\n\t// @outlet(default)\n\t// @class(access)\n\tCreateCrewGroup struct {",
			"client.manager.CreateGroup(ctx, txn, accesstypes.GlobalPolicyScope(), m.Name, m.Description)", "return &CreateCrewGroupResult{ID: id}, nil",
		},
		"crewidentity/describe_crew_group.go": {"// @answers(204)\ntype DescribeCrewGroup struct {\n\t// @target(CrewListedGroup)\n\tGroupID     impulse.UUID", "client.manager.DescribeGroup(ctx, txn, m.GroupID, m.Description)"},
		"crewidentity/delete_crew_group.go":   {"type DeleteCrewGroup struct {\n\t// @target(CrewListedGroup)\n\tGroupID impulse.UUID\n}", "client.manager.DeleteGroup(ctx, txn, m.GroupID)"},
		"crewidentity/add_crew_group_member.go": {
			"type AddCrewGroupMember struct {\n\t// @target(CrewListedGroup)\n\tGroupID impulse.UUID", "// @enumerate(CrewSessionUsers)\n\tUserID impulse.UUID",
			// The served method names a person someone picked, so the account is read.
			"client.manager.RequireUser(ctx, txn, m.UserID)", "client.manager.AddGroupMemberFor(ctx, txn, m.GroupID, m.UserID, period)",
		},
		"crewidentity/remove_crew_group_member.go": {
			"// @target(CrewGroupUser, key: GroupID)\n\tGroupID impulse.UUID\n\t// @target(CrewGroupUser, key: UserID)\n\tUserID impulse.UUID",
			"client.manager.RemoveGroupMember(ctx, txn, m.GroupID, m.UserID)",
			// Asked first, the server says who leaves which group, and whether the account goes.
			"// @answers(204)\n// @warning\n", "client.manager.GroupMemberRemovalWarning(ctx, txn, m.GroupID, m.UserID)",
		},
		"crewidentity/give_crew_group_role.go": {
			// The role is picked from the roles the caller could give. The application has
			// no tenants, so the request does not say where.
			"type GiveCrewGroupRole struct {\n\t// @target(CrewListedGroup)\n\tGroupID impulse.UUID\n\t// Role is the role the group is given,",
			"// @attribute(role)\n\t// @enumerate(CrewGivableRoles)\n\tRole string `validate:\"required\"`",
			"client.manager.GiveGroupRoleFor(ctx, txn, m.GroupID, scope, accesstypes.Role(m.Role), period)",
		},
		// The whole application's method names no row: a role is held in a scope whose
		// columns are empty for the whole application, so a page fills it from a row.
		"crewidentity/take_crew_group_role.go": {"// @answers(204)\n// @class(access)\ntype TakeCrewGroupRole struct {\n\tGroupID impulse.UUID\n\tRole    string `validate:\"required\"`\n}", "client.manager.TakeGroupRole(ctx, txn, m.GroupID, scope, accesstypes.Role(m.Role))"},
		"crewidentity/store.go": {
			"var _ identity.Store = store{}",
			"func (store) InsertGroup(ctx context.Context, txn resource.ReadWriteTransaction, group *access.GroupRow) (impulse.UUID, error) {",
			"Where(clause.Kind().Equal(place.Kind).And().Axis().Equal(place.Axis).And().DomainID().Equal(place.Domain).And().Name().Equal(place.Name))",
			"crewresources.NewCrewGroupMemberQueryClause().UserID().Equal(userID.String())",
			"crewresources.NewCrewGroupMemberCreatePatch(id, string(key.User)).\n\t\tSetCreatedAt(spanner.CommitTimestamp).\n\t\tOrSkip()",
			"crewresources.NewCrewGroupRoleDeletePatch(id, key.Kind, key.Axis, key.Domain, string(key.Role))",
			"SetKind(at.Kind).SetAxis(at.Axis).SetDomainID(at.Domain)",
		},
		// The directory's own groups do the job for an auth whose roles it writes: the
		// store stands for identity.Store and answers that nobody is in a group, which
		// the rules every auth keeps ask.
		"membersidentity/store.go": {
			"var _ identity.Store = store{}",
			"a directory writes the Members auth's roles at every",
			`var errNoGroups = errors.New("the Members auth keeps no groups of people: its groups are not managed here")`,
			"func (store) UserGroups(context.Context, resource.ReadWriteTransaction, impulse.UUID) ([]identity.GroupPlace, error) {\n\treturn nil, nil\n}",
			"func (store) InsertGroupMember(context.Context, resource.ReadWriteTransaction, *access.GroupMemberKey) (bool, error) {\n\treturn false, errNoGroups\n}",
		},
	} {
		content := readTreeFile(t, file)
		for _, line := range lines {
			if !strings.Contains(content, line) {
				t.Errorf("%s lacks %q:\n%s", file, line, content)
			}
		}
	}
	if store := readTreeFile(t, "crewidentity/store.go"); strings.Contains(store, "errNoGroups") {
		t.Errorf("crewidentity/store.go answers that the auth keeps no groups, and it does:\n%s", store)
	}
	if store := readTreeFile(t, "membersidentity/store.go"); strings.Contains(store, "NewMembersGroup") {
		t.Errorf("membersidentity/store.go reads the group tables of an auth whose directory writes its roles:\n%s", store)
	}

	// No group gives the Super Administrator role, so the methods a super administrator
	// always executes stay the two that give and take a person's own role.
	if client := readTreeFile(t, "crewidentity/client.go"); !strings.Contains(client, "return []accesstypes.Resource{AssignCrewRole{}.Method(), RemoveCrewRole{}.Method()}") {
		t.Errorf("crewidentity/client.go: the methods a super administrator always executes changed:\n%s", client)
	}
}

// Test_prepareIdentity_groups_absent pins an auth whose schema holds no group tables:
// nothing of groups is written, its store says so, and the lists a tenant keeps of its
// people read no group table.
func Test_prepareIdentity_groups_absent(t *testing.T) {
	// As Test_prepareIdentity: the working directory is the process's, no t.Parallel.
	t.Chdir(t.TempDir())

	r := identityFixtureGenerator(t, slices.Concat(crewEveryTable, membersIdentityTables)...)
	r.resource = "pkg/resources"
	r.tableMap["Ships"] = shipsTable()
	writeResourcesPackage(t, "pkg/resources", map[string]string{"ships.go": shipTenantSource})
	if err := r.prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() error = %v", err)
	}

	if got := groupFiles(t, "pkg/identity"); len(got) != 0 {
		t.Errorf("the files of the groups = %v, want none for a schema that holds no group tables", got)
	}
	store := readTreeFile(t, "crewidentity/store.go")
	for _, line := range []string{"the schema holds no group tables for the Crew auth", "CrewGroups, CrewGroupMembers and CrewGroupRoles", "return impulse.NilUUID, errNoGroups"} {
		if !strings.Contains(store, line) {
			t.Errorf("crewidentity/store.go lacks %q:\n%s", line, store)
		}
	}
	for _, file := range []string{"internal/crewresources/crew_ship_users.go", "internal/crewresources/crew_ship_identities.go"} {
		if content := readTreeFile(t, file); strings.Contains(content, "Group") {
			t.Errorf("%s reads a group table the schema does not hold:\n%s", file, content)
		}
	}
}

// Test_prepareIdentity_groups_tenant pins a tenant's side of the groups: its own lists
// (the groups kept in it, their people, their roles, the roles its people hold through a
// group) and its seven methods, each of the tenant's scope and naming a row of one of the
// lists; and the tenant's list of people, which holds who a group puts there.
func Test_prepareIdentity_groups_tenant(t *testing.T) {
	// As Test_prepareIdentity: the working directory is the process's, no t.Parallel.
	t.Chdir(t.TempDir())

	r := identityFixtureGenerator(t, slices.Concat(crewEveryTable, crewGroupTables, membersIdentityTables)...)
	r.resource = "pkg/resources"
	r.tableMap["Ships"] = shipsTable()
	writeResourcesPackage(t, "pkg/resources", map[string]string{"ships.go": shipTenantSource})
	if err := r.prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() error = %v", err)
	}

	want := []string{
		"crewidentity/add_crew_group_member.go",
		"crewidentity/add_crew_ship_group_member.go",
		"crewidentity/create_crew_group.go",
		"crewidentity/create_crew_ship_group.go",
		"crewidentity/delete_crew_group.go",
		"crewidentity/delete_crew_ship_group.go",
		"crewidentity/describe_crew_group.go",
		"crewidentity/describe_crew_ship_group.go",
		"crewidentity/give_crew_group_role.go",
		"crewidentity/give_crew_ship_group_role.go",
		"crewidentity/remove_crew_group_member.go",
		"crewidentity/remove_crew_ship_group_member.go",
		"crewidentity/take_crew_group_role.go",
		"crewidentity/take_crew_ship_group_role.go",
		"internal/crewresources/crew_group_members.go",
		"internal/crewresources/crew_group_roles.go",
		"internal/crewresources/crew_group_users.go",
		"internal/crewresources/crew_groups.go",
		"internal/crewresources/crew_listed_group_roles.go",
		"internal/crewresources/crew_listed_groups.go",
		"internal/crewresources/crew_ship_group_roles.go",
		"internal/crewresources/crew_ship_group_users.go",
		"internal/crewresources/crew_ship_groups.go",
		"internal/crewresources/crew_ship_user_group_roles.go",
		"internal/crewresources/crew_user_group_roles.go",
	}
	if diff := cmp.Diff(want, groupFiles(t, "pkg/identity")); diff != "" {
		t.Errorf("the files of the groups (-want +got):\n%s", diff)
	}

	for file, lines := range map[string][]string{
		"internal/crewresources/crew_ship_groups.go": {
			"// @virtual", "// @permissionScope(domain)", "CrewShipGroup struct {", "// @primarykey\n\t\t// @attribute(group)\n\t\tID impulse.UUID", "// @domain\n\t\tDomainID    string",
			// A group kept for the whole application is no tenant's.
			"FROM CrewGroups g\nWHERE g.Kind = 'domain' AND g.Axis = ''\n`",
		},
		"internal/crewresources/crew_ship_group_users.go": {
			"// @permissionScope(domain)", "CrewShipGroupUser struct {", "// @domain\n\t\tDomainID string",
			"JOIN CrewSessionUsers u ON u.Id = m.UserId\nWHERE g.Kind = 'domain' AND g.Axis = ''\n  AND " + standingSQL("m") + "\n`",
		},
		"internal/crewresources/crew_ship_group_roles.go": {
			"CrewShipGroupRole struct {", "// @primarykey\n\t\t// @attribute(role)\n\t\t// @roleOf(Crew)\n\t\tRole      string",
			// A tenant's group holds roles in its own tenant alone.
			"AND r.Kind = g.Kind AND r.Axis = g.Axis AND r.DomainId = g.DomainId\n  AND " + standingSQL("r") + "\n`",
		},
		"internal/crewresources/crew_ship_user_group_roles.go": {
			"CrewShipUserGroupRole struct {", "GlobalGroup bool", "EveryDomain bool",
			"SELECT s.Id AS DomainId, r.GroupId AS GroupId, r.Role AS Role, TRUE AS EveryDomain, r.CreatedAt AS CreatedAt, r.StartsAt AS StartsAt, r.EndsAt AS EndsAt\n  FROM CrewGroupRoles r\n  CROSS JOIN Ships s",
			"WHERE t.Role != 'SuperAdministrator'\n  AND (g.Kind = 'global' OR (NOT t.EveryDomain AND g.Kind = 'domain' AND g.Axis = '' AND g.DomainId = t.DomainId))",
		},
		// Who a group puts in a tenant is one of the tenant's people: the members of its
		// own groups, and of a group kept for the whole application that holds a role there.
		"internal/crewresources/crew_ship_users.go": {
			"WHERE m.Kind = 'every' AND m.Axis = ''\n    AND " + countingSQL("m") + "\n  UNION DISTINCT\n  SELECT g.DomainId AS DomainId, gm.UserId AS UserId\n  FROM CrewGroups g\n  JOIN CrewGroupMembers gm ON gm.GroupId = g.Id\n  WHERE g.Kind = 'domain' AND g.Axis = ''\n    AND " + countingSQL("gm"),
			"WHERE r.Kind = 'domain' AND r.Axis = '' AND g.Kind = 'global' AND r.Role != 'SuperAdministrator'",
			"CROSS JOIN Ships s\n  WHERE r.Kind = 'every' AND r.Axis = '' AND g.Kind = 'global'\n    AND " + countingSQL("r") + "\n    AND " + countingSQL("gm") + "\n) t",
			// A place in another tenant's group is a role held elsewhere, and a place in
			// a group kept for the whole application makes the person one of its own.
			"WHERE gm.UserId = u.Id AND og.Kind = 'domain' AND og.Axis = '' AND og.DomainId != t.DomainId\n      AND " + standingSQL("gm") + "\n  )) AS HeldElsewhere,",
			"WHERE gm.UserId = u.Id AND gg.Kind = 'global'\n      AND " + standingSQL("gm") + "\n  )) AS HeldGlobally",
			// A place in one of the tenant's own groups is something held in the tenant
			// itself, and a place in a group kept for the whole application that holds a role
			// in every tenant is a role held in every tenant.
			// Each is written with EXISTS alone, as it adds no join to the query (below).
			"SELECT 1 FROM CrewGroupMembers gm\n    WHERE gm.UserId = u.Id AND " + standingSQL("gm") + "\n      AND EXISTS (\n        SELECT 1 FROM CrewGroups hg\n        WHERE hg.Id = gm.GroupId AND hg.Kind = 'domain' AND hg.Axis = '' AND hg.DomainId = t.DomainId\n      )\n  )) AS HeldHere,",
			"SELECT 1 FROM CrewGroupMembers gm\n    WHERE gm.UserId = u.Id AND " + standingSQL("gm") + "\n      AND EXISTS (\n        SELECT 1 FROM CrewGroups eg\n        WHERE eg.Id = gm.GroupId AND eg.Kind = 'global'\n      )\n      AND EXISTS (\n        SELECT 1 FROM CrewGroupRoles er\n        WHERE er.GroupId = gm.GroupId AND er.Kind = 'every' AND er.Axis = ''\n          AND " + standingSQL("er") + "\n      )\n  )) AS HeldInEveryTenant",
		},
		"crewidentity/create_crew_ship_group.go": {
			"// @rpc\n\t// @permissionScope(domain)\n\t// @outlet(default)\n\t// @class(access)\n\tCreateCrewShipGroup struct {", "tenant.CreateGroup(ctx, txn, m.Name, m.Description)",
		},
		"crewidentity/describe_crew_ship_group.go": {"// @permissionScope(domain)", "// @target(CrewShipGroup)\n\tGroupID     impulse.UUID", "tenant.DescribeGroup(ctx, txn, m.GroupID, m.Description)"},
		"crewidentity/delete_crew_ship_group.go":   {"// @permissionScope(domain)", "// @target(CrewShipGroup)\n\tGroupID impulse.UUID", "tenant.DeleteGroup(ctx, txn, m.GroupID)"},
		"crewidentity/add_crew_ship_group_member.go": {
			"// @permissionScope(domain)", "// @target(CrewShipGroup)\n\tGroupID impulse.UUID", "// @enumerate(CrewShipUsers)\n\tUserID impulse.UUID",
			"tenant.AddGroupMemberFor(ctx, txn, m.GroupID, m.UserID, period)",
		},
		"crewidentity/remove_crew_ship_group_member.go": {
			"// @target(CrewShipGroupUser, key: GroupID)\n\tGroupID impulse.UUID\n\t// @target(CrewShipGroupUser, key: UserID)\n\tUserID impulse.UUID",
			"tenant.RemoveGroupMember(ctx, txn, m.GroupID, m.UserID)",
			"// @answers(204)\n// @warning\n", "tenant.GroupMemberRemovalWarning(ctx, txn, m.GroupID, m.UserID)",
		},
		"crewidentity/give_crew_ship_group_role.go": {
			"// @target(CrewShipGroup)\n\tGroupID impulse.UUID", "// @attribute(role)\n\t// @enumerate(CrewShipRoles)\n\tRole string `validate:\"required\"`",
			"tenant.GiveGroupRoleFor(ctx, txn, m.GroupID, accesstypes.Role(m.Role), period)",
		},
		"crewidentity/take_crew_ship_group_role.go": {
			"// @target(CrewShipGroupRole, key: GroupID)\n\tGroupID impulse.UUID\n\t// @target(CrewShipGroupRole, key: Role)\n\tRole string `validate:\"required\"`",
			"tenant.TakeGroupRole(ctx, txn, m.GroupID, accesstypes.Role(m.Role))",
		},
	} {
		content := readTreeFile(t, file)
		for _, line := range lines {
			if !strings.Contains(content, line) {
				t.Errorf("%s lacks %q:\n%s", file, line, content)
			}
		}
	}

	// The two facts of what a person holds in the tenant itself and in every tenant add no
	// join to the list of the tenant's people: Spanner refuses a query of more than twenty
	// joins, and the list of a tenant whose record declares the directory-only switch,
	// for an auth that keeps groups, holds eighteen without these two.
	users := readTreeFile(t, "internal/crewresources/crew_ship_users.go")
	from, to := strings.Index(users, ") AS HeldGlobally,"), strings.Index(users, ") AS HeldInEveryTenant")
	if from < 0 || to < from {
		t.Errorf("crew_ship_users.go lacks HeldGlobally before HeldInEveryTenant:\n%s", users)
	} else if facts := users[from:to]; strings.Contains(facts, "JOIN") {
		t.Errorf("crew_ship_users.go joins a table in HeldHere or HeldInEveryTenant, want EXISTS alone:\n%s", facts)
	}

	// A tenant's method takes no tenant in its request: the tenant is the one the
	// request is made under.
	for _, file := range []string{"crewidentity/create_crew_ship_group.go", "crewidentity/give_crew_ship_group_role.go", "crewidentity/take_crew_ship_group_role.go"} {
		if content := readTreeFile(t, file); strings.Contains(content, "Domain ") || strings.Contains(content, "EveryDomain") {
			t.Errorf("%s takes a tenant in its request:\n%s", file, content)
		}
	}
}

// Test_prepareIdentity_grants pins what an auth that keeps groups is written to judge a
// change to a group by: the answers of the role file's grants on the methods that give a
// role and take one away (identity.RoleGrants), which its client hands the library, and
// the store's reads the rules of a group ask. An auth that keeps no groups is written
// none of it.
func Test_prepareIdentity_grants(t *testing.T) {
	// As Test_prepareIdentity: the working directory is the process's, no t.Parallel.
	t.Chdir(t.TempDir())

	r := identityFixtureGenerator(t, slices.Concat(crewEveryTable, crewGroupTables, membersIdentityTables)...)
	if err := r.prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() error = %v", err)
	}

	requireLines(t, map[string][]string{
		"crewidentity/grants.go": {
			generationHeader, "var _ identity.RoleGrants = grants{}",
			// The whole application's methods name no row: the request's fields settle the
			// grant, the role in question among them. The application has no tenants, so a
			// request names no place, and a scope that is not the whole application is a no.
			"if !scope.IsGlobal() {\n\t\treturn false, nil\n\t}\n\tgate, err := caller.ExecuteGate(ctx, AssignCrewRole{}.Method(), &AssignCrewRole{UserID: userID, Role: string(role)})",
			"return settled(gate, \"AssignCrewRole\")",
			"if !scope.IsGlobal() {\n\t\treturn false, nil\n\t}\n\tgate, err := caller.ExecuteGate(ctx, RemoveCrewRole{}.Method(), &RemoveCrewRole{UserID: userID, Role: string(role)})",
			"return settled(gate, \"RemoveCrewRole\")",
			// The list the whole application's role fields pick from asks the one method
			// whose dialog picks, with the role, held for the whole application: a role that method
			// takes from the caller is listed for it, and for no other method.
			"func (g grants) CouldGive(ctx context.Context, method accesstypes.Resource, scope accesstypes.PolicyScope, role accesstypes.Role) (bool, error) {\n\tcaller := resource.CallerFrom(ctx)\n\t// The application has no tenants: a role is held for the whole application, and\n\t// the methods give none anywhere else.\n\tif !scope.IsGlobal() {\n\t\treturn false, nil\n\t}\n\tswitch method {",
			"\tcase AssignCrewRole{}.Method():\n\t\treturn couldGiveBy(ctx, caller, method, &AssignCrewRole{Role: string(role)})\n" +
				"\tcase InviteCrewUser{}.Method():\n\t\treturn couldGiveBy(ctx, caller, method, &InviteCrewUser{Roles: []string{string(role)}})\n" +
				"\tcase GiveCrewGroupRole{}.Method():\n\t\treturn couldGiveBy(ctx, caller, method, &GiveCrewGroupRole{Role: string(role)})\n\t}",
			// A method that picks no role from the list is no answer.
			"return false, errors.Newf(\"%s is no method of the whole application that gives a role by hand: AssignCrewRole, InviteCrewUser, GiveCrewGroupRole\", method)",
			// One method is asked: its refusal is the answer, and a grant that admits the
			// role, or leaves a row to decide, is a yes.
			"func couldGiveBy(ctx context.Context, caller *resource.Caller, method accesstypes.Resource, request any) (bool, error) {\n\tgate, err := caller.ExecuteGate(ctx, method, request)\n\tif err != nil {\n\t\treturn granted(err)\n\t}\n\tmay, settled := gate.Settled()\n\n\treturn may || !settled, nil\n}",
			// A refusal is an answer, and anything else is an error, never a pass.
			"case httpio.HasForbidden(err), httpio.HasNotFound(err):\n\t\treturn false, nil\n\tdefault:\n\t\treturn false, errors.Wrap(err, \"resource.Caller.ExecuteGate()\")",
		},
		"crewidentity/client.go": {"identity.WithRoleGrants(grants{})"},
		"crewidentity/store.go": {
			"func (store) GroupsHolding(ctx context.Context, txn resource.ReadWriteTransaction, key *access.RoleKey) ([]impulse.UUID, error) {",
			// No tenant, so nobody is a tenant's person.
			"func (store) TenantPerson(context.Context, resource.ReadWriteTransaction, accesstypes.Domain, impulse.UUID) (bool, error) {\n\treturn false, nil\n}",
			// What has ended is the database's to say, by its own clock.
			"FROM CrewUserRoles@{FORCE_INDEX=CrewUserRolesByUserId} WHERE UserId = @user AND EndsAt <= CURRENT_TIMESTAMP()",
			"FROM CrewGroupMembers@{FORCE_INDEX=CrewGroupMembersByUserId} WHERE UserId = @user AND EndsAt <= CURRENT_TIMESTAMP()",
		},
		// A group is no part of an auth that keeps none: nothing asks its grants.
		"membersidentity/store.go": {
			"func (store) GroupsHolding(context.Context, resource.ReadWriteTransaction, *access.RoleKey) ([]impulse.UUID, error) {\n\treturn nil, nil\n}",
			"func (store) TenantPerson(context.Context, resource.ReadWriteTransaction, accesstypes.Domain, impulse.UUID) (bool, error) {\n\treturn false, nil\n}",
		},
	})
	for _, line := range []string{"Ship", "EnforceView", "EnforceProposed"} {
		if content := readTreeFile(t, "crewidentity/grants.go"); strings.Contains(content, line) {
			t.Errorf("crewidentity/grants.go holds %q, a tenant's side, and the program declares no tenant:\n%s", line, content)
		}
	}
	// The members keep no groups, and the application gives their roles: their package
	// answers the role file's grants for the list their roles are picked from, and asks
	// nothing of a group.
	members := readTreeFile(t, "membersidentity/grants.go")
	for _, line := range []string{"GroupRole", "EnforceView", "EnforceProposed"} {
		if strings.Contains(members, line) {
			t.Errorf("membersidentity/grants.go holds %q, and the Members auth keeps no groups and declares no tenant:\n%s", line, members)
		}
	}
	assertFileContains(t, "membersidentity/client.go", []string{"identity.WithRoleGrants(grants{})"}, nil)
}

// Test_prepareIdentity_grants_tenant pins a tenant's side of the same: a request made
// under a tenant is answered by the grants of the tenant's own two methods, the one that
// gives a role read against the person's row of the tenant's people and the one that
// takes a role away against the row the removal would name; and the store reads whether
// a person is one of the tenant's people from the tenant's own list.
func Test_prepareIdentity_grants_tenant(t *testing.T) {
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
		"crewidentity/grants.go": {
			// A request made under a tenant changes that tenant's roles alone.
			"if tenant, inTenant := caller.Scope.Domain(); inTenant {\n\t\t// A request made under a tenant gives a role in that tenant alone.\n\t\tif scope != accesstypes.DomainPolicyScope(tenant) {\n\t\t\treturn false, nil\n\t\t}",
			"caller.ExecuteGate(ctx, AssignCrewShipRole{}.Method(), &AssignCrewShipRole{UserID: userID, Role: string(role)})",
			"people := resource.ExecuteTarget{Resource: \"CrewShipUsers\", Label: \"CrewShipUser\", PKColumns: []string{\"Id\"}, View: crewresources.CrewShipUser{}}",
			"return granted(gate.EnforceView(ctx, txn, people, userID))",
			"caller.ExecuteGate(ctx, RemoveCrewShipRole{}.Method(), &RemoveCrewShipRole{UserID: userID, Role: string(role)})",
			"held := resource.ExecuteTarget{Resource: \"CrewShipHeldRoles\", Label: \"CrewShipHeldRole\", PKColumns: []string{\"UserId\", \"Role\", \"GroupId\"}, View: nil}",
			"return granted(gate.EnforceProposed(ctx, txn, held, map[string]any{\"role\": string(role), \"everyDomain\": false}))",
			// The whole application's request is answered by its own methods still.
			"caller.ExecuteGate(ctx, AssignCrewRole{}.Method(), &AssignCrewRole{UserID: userID, Role: string(role), Domain: domain, EveryDomain: everyDomain})",
			// The list a tenant's role is picked from asks the one method whose dialog
			// picks, with the role and nothing else: a refusal is that method's no, and a
			// grant that admits the role, or leaves the row to decide, is a yes. A method
			// of the whole application is no method of a tenant.
			"func (g grants) CouldGive(ctx context.Context, method accesstypes.Resource, scope accesstypes.PolicyScope, role accesstypes.Role) (bool, error) {",
			"\t\tswitch method {\n\t\tcase AssignCrewShipRole{}.Method():\n\t\t\treturn couldGiveBy(ctx, caller, method, &AssignCrewShipRole{Role: string(role)})\n" +
				"\t\tcase InviteCrewShipUser{}.Method():\n\t\t\treturn couldGiveBy(ctx, caller, method, &InviteCrewShipUser{Roles: []string{string(role)}})\n" +
				"\t\tcase GiveCrewShipGroupRole{}.Method():\n\t\t\treturn couldGiveBy(ctx, caller, method, &GiveCrewShipGroupRole{Role: string(role)})\n\t\t}\n\n" +
				"\t\treturn false, errors.Newf(\"%s is no method of a tenant that gives a role by hand: AssignCrewShipRole, InviteCrewShipUser, GiveCrewShipGroupRole\", method)\n\t}",
			"gate, err := caller.ExecuteGate(ctx, method, request)\n\tif err != nil {\n\t\treturn granted(err)\n\t}",
		},
		"crewidentity/store.go": {
			"func (store) TenantPerson(ctx context.Context, txn resource.ReadWriteTransaction, domain accesstypes.Domain, userID impulse.UUID) (bool, error) {",
			"clause := crewresources.NewCrewShipUserQueryClause()",
			"clause.ID().Equal(userID).And().DomainID().Equal(string(domain))",
		},
		// The tenant's method holds the person to the tenant's people in the library, so
		// it reads no account of its own.
		"crewidentity/add_crew_ship_group_member.go": {"The person is one the tenant already has: a row of CrewShipUser"},
	})
	if content := readTreeFile(t, "crewidentity/add_crew_ship_group_member.go"); strings.Contains(content, "RequireUser") {
		t.Errorf("crewidentity/add_crew_ship_group_member.go reads the account of any id, and the tenant's method takes one of the tenant's people:\n%s", content)
	}
	// The attributes a condition on the grant of the tenant's removal reads are the two
	// the row the grants propose carries: a third would be one the proposed row lacks.
	roles := readTreeFile(t, "internal/crewresources/crew_ship_user_roles.go")
	if got := strings.Count(roles, "// @attribute("); got != 2 || !strings.Contains(roles, "// @attribute(role)") || !strings.Contains(roles, "// @attribute(everyDomain)") {
		t.Errorf("internal/crewresources/crew_ship_user_roles.go declares %d attributes, want role and everyDomain alone, the two the proposed row of a removal carries:\n%s", got, roles)
	}
}

// Test_identityInstance_groupTables pins which instances keep groups: one whose schema
// holds the three group tables beside its memberships. The tables are migrated together,
// so some of them without the others is refused; an auth whose directory writes its
// roles, and one with no memberships to fold a group's roles into, keep none.
func Test_identityInstance_groupTables(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		tables    []string
		synced    bool
		wantKeeps bool
		wantErr   string
	}{
		{name: "the three tables beside the memberships", tables: slices.Concat(crewIdentityTables, crewGroupTables), wantKeeps: true},
		{name: "no group tables", tables: crewIdentityTables},
		{
			name: "some of the tables and not the others", tables: slices.Concat(crewIdentityTables, []string{"CrewGroups", "CrewGroupMembers"}),
			wantErr: "the schema holds some of CrewGroups, CrewGroupMembers and CrewGroupRoles and not the others",
		},
		{name: "a directory writes the roles", tables: slices.Concat(crewIdentityTables, crewGroupTables), synced: true},
		{name: "no memberships", tables: slices.Concat([]string{"CrewSessionUsers", "CrewSessions"}, crewGroupTables)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := identityFixtureGenerator(t, slices.Concat(tt.tables, membersIdentityTables)...)
			auth := &outletAuth{importPath: crewAuthPath}
			if tt.synced {
				auth.methods = []AuthMethod{AzureMethod("directory").SyncsRoles()}
			}
			inst, err := r.newIdentityInstance(auth)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("newIdentityInstance() error = %v, want one holding %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("newIdentityInstance() error = %v", err)
			}
			if got := inst.keepsGroups(); got != tt.wantKeeps {
				t.Errorf("keepsGroups() = %v, want %v", got, tt.wantKeeps)
			}
			for _, kind := range identityGroupTables() {
				if inst.has(kind) != tt.wantKeeps {
					t.Errorf("has(%s) = %v, want %v", kind, inst.has(kind), tt.wantKeeps)
				}
			}
		})
	}
}

// Test_nameGroupSets pins the name of the subject set of a person's groups: it is named
// after its auth, in an application where one auth keeps groups as where several do, and
// an auth that keeps none declares nothing.
func Test_nameGroupSets(t *testing.T) {
	t.Parallel()

	keeping := func(auth string) *identityInstance {
		return &identityInstance{auth: auth, tables: map[identityTable]bool{identityGroups: true, identityGroupMembers: true, identityGroupRoles: true}}
	}
	none := &identityInstance{auth: "Members", tables: map[identityTable]bool{}}

	one := []*identityInstance{keeping("Crew"), none}
	nameGroupSets(one)
	if one[0].groupSet != "crewGroups" || one[1].groupSet != "" {
		t.Errorf("one auth keeping groups: the sets = %q and %q, want crewGroups and none", one[0].groupSet, one[1].groupSet)
	}

	staff := []*identityInstance{keeping("staff")}
	nameGroupSets(staff)
	if staff[0].groupSet != "staffGroups" {
		t.Errorf("the one auth staff keeping groups: the set = %q, want staffGroups", staff[0].groupSet)
	}

	several := []*identityInstance{keeping("Crew"), keeping("FleetStaff")}
	nameGroupSets(several)
	if several[0].groupSet != "crewGroups" || several[1].groupSet != "fleetStaffGroups" {
		t.Errorf("two auths keeping groups: the sets = %q and %q, want crewGroups and fleetStaffGroups", several[0].groupSet, several[1].groupSet)
	}
}

// Test_refuseListNamesTaken_groups pins the tenant record's names that would give a
// tenant's list the name of something the groups declare: a tenant record named Group
// names the roles a tenant gives as the declaration over the group roles table is named,
// and one named UserGroup names them as the whole application's list of the roles held
// through groups is.
func Test_refuseListNamesTaken_groups(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		tenant  string
		wantErr string
	}{
		{tenant: "Sector"},
		{tenant: "Group", wantErr: "CrewGroupRole"},
		{tenant: "UserGroup", wantErr: "CrewUserGroupRole"},
	} {
		inst := &identityInstance{importPath: crewAuthPath, auth: "Crew", tenant: &identityTenant{name: tt.tenant}}
		err := inst.refuseListNamesTaken()
		switch {
		case tt.wantErr == "" && err != nil:
			t.Errorf("tenant record %s: refuseListNamesTaken() error = %v, want none", tt.tenant, err)
		case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), "the name "+tt.wantErr+",")):
			t.Errorf("tenant record %s: refuseListNamesTaken() error = %v, want a refusal naming %s", tt.tenant, err, tt.wantErr)
		}
	}
}

// Test_identityOn_groups pins what the generated API description says of an auth's
// groups: each side's lists and methods over them under the key a page knows each by,
// only where the outlet serves them.
func Test_identityOn_groups(t *testing.T) {
	t.Parallel()

	crew := &identityInstance{auth: "Crew", outlets: []string{"console"}, tenant: &identityTenant{name: "Sector", table: "Sectors", key: "Id"}}
	served := []string{
		"CrewSessionUsers", "CrewGroups", "CrewGroupUsers", "CrewGroupRoles", "CrewUserGroupRoles",
		"CrewSectorUsers", "CrewSectorGroups", "CrewSectorGroupUsers", "CrewSectorGroupRoles", "CrewSectorUserGroupRoles",
	}
	methods := []string{
		"CreateCrewGroup", "DescribeCrewGroup", "DeleteCrewGroup", "AddCrewGroupMember", "RemoveCrewGroupMember", "GiveCrewGroupRole", "TakeCrewGroupRole",
		"CreateCrewSectorGroup", "AddCrewSectorGroupMember", "TakeCrewSectorGroupRole",
	}
	data := &tsAPIData{}
	for _, name := range served {
		data.Resources = append(data.Resources, &tsAPIResource{Name: name})
	}
	for _, name := range methods {
		data.Methods = append(data.Methods, &tsAPIMethod{Name: name})
	}
	part := func(key, name string) identityPart {
		return identityPart{Key: key, Name: name}
	}

	generator := &typescriptGenerator{client: &client{identity: []*identityInstance{crew}}, outletName: "console"}
	want := []*tsAPIIdentity{{
		Auth: "Crew", Label: "Crew", Tenant: "Sector",
		Sides: []*tsAPIIdentitySide{
			{
				Scope: accesstypes.GlobalPermissionScope,
				Lists: []identityPart{
					part("users", "CrewSessionUsers"), part("groups", "CrewGroups"), part("groupUsers", "CrewGroupUsers"),
					part("groupRoles", "CrewGroupRoles"), part("rolesThroughGroups", "CrewUserGroupRoles"),
				},
				Methods: []identityPart{
					part("createGroup", "CreateCrewGroup"), part("describeGroup", "DescribeCrewGroup"), part("deleteGroup", "DeleteCrewGroup"),
					part("addGroupMember", "AddCrewGroupMember"), part("removeGroupMember", "RemoveCrewGroupMember"),
					part("giveGroupRole", "GiveCrewGroupRole"), part("takeGroupRole", "TakeCrewGroupRole"),
				},
			},
			{
				Scope: accesstypes.DomainPermissionScope,
				Lists: []identityPart{
					part("users", "CrewSectorUsers"), part("groups", "CrewSectorGroups"), part("groupUsers", "CrewSectorGroupUsers"),
					part("groupRoles", "CrewSectorGroupRoles"), part("rolesThroughGroups", "CrewSectorUserGroupRoles"),
				},
				// The outlet serves three of the tenant's methods over groups.
				Methods: []identityPart{
					part("createGroup", "CreateCrewSectorGroup"), part("addGroupMember", "AddCrewSectorGroupMember"), part("takeGroupRole", "TakeCrewSectorGroupRole"),
				},
			},
		},
	}}
	if diff := cmp.Diff(want, generator.identityOn(data), cmp.AllowUnexported(identityPart{})); diff != "" {
		t.Errorf("identityOn() (-want +got):\n%s", diff)
	}
}

// Test_resolveRunSubjectSets pins where a run that generates an auth's management
// checks what a who: names. The application's resources are extracted before the
// management's, and the management declares the set of a person's groups, so a who:
// that names it is not refused where the application's resources are extracted alone:
// the run checks once it holds both, and still refuses a set nothing declares.
func Test_resolveRunSubjectSets(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "subjectsetfixture"))
	// WhoUndeclaredSet tests its TeamID against subject.squads, which no struct of the
	// fixture declares.
	names := []string{"TeamMember", "Assignment", "WhoUndeclaredSet"}

	alone := subjectSetFixtureClient(t, structs)
	if _, err := alone.structsToResources(subjectSetStructs(t, structs, names...)); err == nil || !strings.Contains(err.Error(), "subject.squads, which no @subjectSet declares") {
		t.Fatalf("a run with no generated management: structsToResources() error = %v, want the set refused where the resources are extracted", err)
	}

	c := subjectSetFixtureClient(t, structs)
	c.identity = []*identityInstance{{auth: "Crew"}}
	resources, err := c.structsToResources(subjectSetStructs(t, structs, names...))
	if err != nil {
		t.Fatalf("a run with a generated management: structsToResources() error = %v, want the check left to the run", err)
	}
	if err := c.resolveRunSubjectSets(resources); err == nil || !strings.Contains(err.Error(), "subject.squads, which no @subjectSet declares") {
		t.Errorf("resolveRunSubjectSets() over the application's resources alone: error = %v, want the set refused", err)
	}

	// The management's declaration of the set, as its own extraction answers it.
	declared := &resourceInfo{SubjectSets: []*subjectBinding{{Name: "squads", Type: "string"}}}
	if err := c.resolveRunSubjectSets(append(slices.Clone(resources), declared)); err != nil {
		t.Errorf("resolveRunSubjectSets() with the set declared by the management: error = %v, want none", err)
	}
}

// Test_groupTenantColumn_isTheColumnTheDeclarationNames holds the column a domain: must
// end at on an auth's groups table (groupTenantColumn) to the generated declaration of
// a group: the field the members' own set ends at, DomainID, is declared over that
// column. A change to either that leaves the other behind fails here, before a run
// refuses its own management.
func Test_groupTenantColumn_isTheColumnTheDeclarationNames(t *testing.T) {
	t.Parallel()

	declared := regexp.MustCompile(`\bDomainID\s+string\s+` + "`" + `spanner:"` + groupTenantColumn + `"`)
	if !declared.MatchString(identityGroupsTemplate) {
		t.Errorf("the generated declaration of a group no longer declares DomainID over the column %s, which is where a domain: on the groups table must end", groupTenantColumn)
	}
	if !strings.Contains(identityGroupMembersTemplate, "domain: GroupID.DomainID") {
		t.Errorf("the generated group members no longer end their set's domain: at DomainID")
	}
}

// Test_groupSetTenantFilter_restsOnOneAxis holds what the tenant filter of the set of a
// person's groups rests on. The set says where a row's tenant is found (domain:
// GroupID.DomainID), and its filter compares that tenant's key with the request's and
// never the axis kept beside it. A key says which tenant only on one axis, so the filter
// is sound exactly while every group is kept on the default axis, and that is all there
// is today: no scope can name another axis, so a group to be created never does, a group
// row that names one is refused when it is read, and the generated lists compare the
// default axis too. The day a tenant can be on a second axis this test fails, and the set
// must compare the axis in the same change: without that a group of one axis counts in
// the tenant that has the same key on the other.
func Test_groupSetTenantFilter_restsOnOneAxis(t *testing.T) {
	t.Parallel()

	// The set's way to a row's tenant is the tenant's key alone.
	if !strings.Contains(identityGroupMembersTemplate, "// @subjectSet({{ .GroupSet }}, value: GroupID, domain: GroupID.DomainID, ") {
		t.Fatalf("the generated group members no longer write domain: GroupID.DomainID on the set of a person's groups: hold this test to what the set compares now")
	}

	tests := []struct {
		name  string
		scope accesstypes.PolicyScope
	}{
		{name: "a tenant's group", scope: accesstypes.DomainPolicyScope("north")},
		{name: "a group kept for the whole application", scope: accesstypes.GlobalPolicyScope()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			row, err := access.NewGroup(tt.scope, "Bay keepers", "The people who keep the bays.")
			if err != nil {
				t.Fatalf("NewGroup() error = %v", err)
			}
			if row.Axis != "" {
				t.Errorf("NewGroup() keeps the group on axis %q, want the default axis: the set of a person's groups compares a tenant's key and no axis", row.Axis)
			}
			if got, err := access.GroupScope(row.Kind, "region", row.Domain); err == nil {
				t.Errorf("GroupScope(%q, %q, %q) = %v, want the row refused: a group can now be kept on a second axis, and the set of a person's groups compares a tenant's key and no axis", row.Kind, "region", row.Domain, got)
			}
		})
	}

	// The lists drawn of a tenant's groups compare the same axis the stores write.
	if got := (&identitySourceData{}).ScopeAxis(); got != "''" {
		t.Errorf("ScopeAxis() = %s, want '': the generated lists read tenants on the default axis alone", got)
	}
}
