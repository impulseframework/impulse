package generation

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
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
		"internal/crewresources/crew_user_group_roles.go",
	}
	if diff := cmp.Diff(want, groupFiles(t, "pkg/identity")); diff != "" {
		t.Errorf("the files of the groups (-want +got):\n%s", diff)
	}

	for file, lines := range map[string][]string{
		"internal/crewresources/crew_groups.go": {
			generationHeader, "// @resource", "// @suppress(patchHandler)", "// @outlet(default)", "// @order(Name asc)", "CrewGroup struct {",
			"ID          impulse.UUID `spanner:\"Id\"`",
			// The store finds a group by where it is kept and its name, the key of the
			// table's unique index, whose leading column alone has a seek of its own.
			"Axis        string       `spanner:\"Axis\"        allow_filter:\"true\"`", "Name        string       `spanner:\"Name\"        allow_filter:\"true\"`",
		},
		"internal/crewresources/crew_group_members.go": {
			"// @suppress(readHandler)", "CrewGroupMember struct {", "GroupID impulse.UUID `spanner:\"GroupId\"`",
			// The one auth that keeps groups declares the set under the plain name.
			"// @subjectSet(groups, value: GroupID)\n\t\tUserID    string    `spanner:\"UserId\"`",
		},
		"internal/crewresources/crew_group_roles.go": {"// @suppress(readHandler)", "CrewGroupRole struct {", "Role      string       `spanner:\"Role\"`"},
		"internal/crewresources/crew_group_users.go": {
			"// @virtual", "CrewGroupUser struct {", "// @primarykey\n\t\tGroupID impulse.UUID", "// @primarykey\n\t\tUserID    impulse.UUID",
			"FROM CrewGroups g\nJOIN CrewGroupMembers m ON m.GroupId = g.Id\nJOIN CrewSessionUsers u ON u.Id = m.UserId\n`",
		},
		"internal/crewresources/crew_user_group_roles.go": {
			"// @virtual", "CrewUserGroupRole struct {", "// @order(Username asc, GroupName asc, Role asc)",
			"FROM CrewGroupRoles r\nJOIN CrewGroups g ON g.Id = r.GroupId\nJOIN CrewGroupMembers m ON m.GroupId = r.GroupId\nJOIN CrewSessionUsers u ON u.Id = m.UserId",
			// The list shows what the permission engine gives: no Super Administrator
			// through a group, and a tenant's group gives its roles in its tenant alone.
			"WHERE r.Role != 'SuperAdministrator'\n  AND (g.Kind = 'global' OR (r.Kind = g.Kind AND r.Axis = g.Axis AND r.DomainId = g.DomainId))",
		},
		"crewidentity/create_crew_group.go": {
			"// @rpc\n\t// @outlet(default)\n\tCreateCrewGroup struct {", "Domain      *string", "identity.Scope(m.Domain, false)",
			"client.manager.CreateGroup(ctx, txn, scope, m.Name, m.Description)", "return &CreateCrewGroupResult{ID: id}, nil",
		},
		"crewidentity/describe_crew_group.go": {"// @answers(204)\ntype DescribeCrewGroup struct {\n\t// @target(CrewGroup)\n\tGroupID     impulse.UUID", "client.manager.DescribeGroup(ctx, txn, m.GroupID, m.Description)"},
		"crewidentity/delete_crew_group.go":   {"type DeleteCrewGroup struct {\n\t// @target(CrewGroup)\n\tGroupID impulse.UUID\n}", "client.manager.DeleteGroup(ctx, txn, m.GroupID)"},
		"crewidentity/add_crew_group_member.go": {
			"type AddCrewGroupMember struct {\n\t// @target(CrewGroup)\n\tGroupID impulse.UUID", "// @enumerate(CrewSessionUsers)\n\tUserID impulse.UUID",
			// The served method names a person someone picked, so the account is read.
			"client.manager.RequireUser(ctx, txn, m.UserID)", "client.manager.AddGroupMember(ctx, txn, m.GroupID, m.UserID)",
		},
		"crewidentity/remove_crew_group_member.go": {
			"// @target(CrewGroupUser, key: GroupID)\n\tGroupID impulse.UUID\n\t// @target(CrewGroupUser, key: UserID)\n\tUserID impulse.UUID",
			"client.manager.RemoveGroupMember(ctx, txn, m.GroupID, m.UserID)",
		},
		"crewidentity/give_crew_group_role.go": {
			"type GiveCrewGroupRole struct {\n\t// @target(CrewGroup)\n\tGroupID impulse.UUID", "// @attribute(role)\n\tRole        string `validate:\"required\"`", "EveryDomain bool",
			"client.manager.GiveGroupRole(ctx, txn, m.GroupID, scope, accesstypes.Role(m.Role))",
		},
		"crewidentity/take_crew_group_role.go": {"type TakeCrewGroupRole struct {\n\t// @target(CrewGroup)\n\tGroupID     impulse.UUID", "client.manager.TakeGroupRole(ctx, txn, m.GroupID, scope, accesstypes.Role(m.Role))"},
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
			"// @virtual", "// @permissionScope(domain)", "CrewShipGroup struct {", "// @primarykey\n\t\tID impulse.UUID", "// @domain\n\t\tDomainID    string",
			// A group kept for the whole application is no tenant's.
			"FROM CrewGroups g\nWHERE g.Kind = 'domain' AND g.Axis = ''\n`",
		},
		"internal/crewresources/crew_ship_group_users.go": {
			"// @permissionScope(domain)", "CrewShipGroupUser struct {", "// @domain\n\t\tDomainID string",
			"JOIN CrewSessionUsers u ON u.Id = m.UserId\nWHERE g.Kind = 'domain' AND g.Axis = ''\n`",
		},
		"internal/crewresources/crew_ship_group_roles.go": {
			"CrewShipGroupRole struct {", "// @primarykey\n\t\t// @attribute(role)\n\t\tRole      string",
			// A tenant's group holds roles in its own tenant alone.
			"AND r.Kind = g.Kind AND r.Axis = g.Axis AND r.DomainId = g.DomainId\n`",
		},
		"internal/crewresources/crew_ship_user_group_roles.go": {
			"CrewShipUserGroupRole struct {", "GlobalGroup bool", "EveryDomain bool",
			"SELECT s.Id AS DomainId, r.GroupId AS GroupId, r.Role AS Role, TRUE AS EveryDomain\n  FROM CrewGroupRoles r\n  CROSS JOIN Ships s",
			"WHERE t.Role != 'SuperAdministrator'\n  AND (g.Kind = 'global' OR (NOT t.EveryDomain AND g.Kind = 'domain' AND g.Axis = '' AND g.DomainId = t.DomainId))",
		},
		// Who a group puts in a tenant is one of the tenant's people: the members of its
		// own groups, and of a group kept for the whole application that holds a role there.
		"internal/crewresources/crew_ship_users.go": {
			"WHERE m.Kind = 'every' AND m.Axis = ''\n  UNION DISTINCT\n  SELECT g.DomainId AS DomainId, gm.UserId AS UserId\n  FROM CrewGroups g\n  JOIN CrewGroupMembers gm ON gm.GroupId = g.Id\n  WHERE g.Kind = 'domain' AND g.Axis = ''",
			"WHERE r.Kind = 'domain' AND r.Axis = '' AND g.Kind = 'global' AND r.Role != 'SuperAdministrator'",
			"CROSS JOIN Ships s\n  WHERE r.Kind = 'every' AND r.Axis = '' AND g.Kind = 'global'\n) t",
			// A place in another tenant's group is a role held elsewhere, and a place in
			// a group kept for the whole application makes the person one of its own.
			"WHERE gm.UserId = u.Id AND og.Kind = 'domain' AND og.Axis = '' AND og.DomainId != t.DomainId\n  )) AS HeldElsewhere,",
			"WHERE gm.UserId = u.Id AND gg.Kind = 'global'\n  )) AS HeldGlobally",
		},
		"crewidentity/create_crew_ship_group.go": {
			"// @rpc\n\t// @permissionScope(domain)\n\t// @outlet(default)\n\tCreateCrewShipGroup struct {", "tenant.CreateGroup(ctx, txn, m.Name, m.Description)",
		},
		"crewidentity/describe_crew_ship_group.go": {"// @permissionScope(domain)", "// @target(CrewShipGroup)\n\tGroupID     impulse.UUID", "tenant.DescribeGroup(ctx, txn, m.GroupID, m.Description)"},
		"crewidentity/delete_crew_ship_group.go":   {"// @permissionScope(domain)", "// @target(CrewShipGroup)\n\tGroupID impulse.UUID", "tenant.DeleteGroup(ctx, txn, m.GroupID)"},
		"crewidentity/add_crew_ship_group_member.go": {
			"// @permissionScope(domain)", "// @target(CrewShipGroup)\n\tGroupID impulse.UUID", "// @enumerate(CrewShipUsers)\n\tUserID impulse.UUID",
			"tenant.AddGroupMember(ctx, txn, m.GroupID, m.UserID)",
		},
		"crewidentity/remove_crew_ship_group_member.go": {
			"// @target(CrewShipGroupUser, key: GroupID)\n\tGroupID impulse.UUID\n\t// @target(CrewShipGroupUser, key: UserID)\n\tUserID impulse.UUID",
			"tenant.RemoveGroupMember(ctx, txn, m.GroupID, m.UserID)",
		},
		"crewidentity/give_crew_ship_group_role.go": {
			"// @target(CrewShipGroup)\n\tGroupID impulse.UUID", "// @attribute(role)\n\t// @enumerate(CrewShipRoles)\n\tRole string `validate:\"required\"`",
			"tenant.GiveGroupRole(ctx, txn, m.GroupID, accesstypes.Role(m.Role))",
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

	// A tenant's method takes no tenant in its request: the tenant is the one the
	// request is made under.
	for _, file := range []string{"crewidentity/create_crew_ship_group.go", "crewidentity/give_crew_ship_group_role.go", "crewidentity/take_crew_ship_group_role.go"} {
		if content := readTreeFile(t, file); strings.Contains(content, "Domain ") || strings.Contains(content, "EveryDomain") {
			t.Errorf("%s takes a tenant in its request:\n%s", file, content)
		}
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

// Test_nameGroupSets pins the name of the subject set of a person's groups:
// subject.<name> is one namespace for the whole application, so the one auth that keeps
// groups declares "groups", several each declare their own, and an auth that keeps none
// declares nothing.
func Test_nameGroupSets(t *testing.T) {
	t.Parallel()

	keeping := func(auth string) *identityInstance {
		return &identityInstance{auth: auth, tables: map[identityTable]bool{identityGroups: true, identityGroupMembers: true, identityGroupRoles: true}}
	}
	none := &identityInstance{auth: "Members", tables: map[identityTable]bool{}}

	one := []*identityInstance{keeping("Crew"), none}
	nameGroupSets(one)
	if one[0].groupSet != "groups" || one[1].groupSet != "" {
		t.Errorf("one auth keeping groups: the sets = %q and %q, want \"groups\" and none", one[0].groupSet, one[1].groupSet)
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
