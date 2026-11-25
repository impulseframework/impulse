package generation

import (
	"regexp"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/session"
)

// tenantKindsTable is a table of the fixtures below: its key, its indexes besides it,
// and its foreign keys.
func tenantKindsTable(key string, indexes []indexMeta, foreignKeys ...foreignKeyMeta) *tableMetadata {
	table := &tableMetadata{
		Columns:     map[string]columnMeta{key: {IsPrimaryKey: true, KeyOrdinalPosition: 1}},
		Indexes:     append([]indexMeta{{Name: "PRIMARY_KEY", PrimaryKey: true, Key: []indexColumn{{Column: key}}}}, indexes...),
		ForeignKeys: foreignKeys,
	}

	return table
}

// indexOn is an index over the columns, storing the stored ones.
func indexOn(name string, key []string, stored ...string) indexMeta {
	columns := make([]indexColumn, 0, len(key))
	for _, column := range key {
		columns = append(columns, indexColumn{Column: column})
	}

	return indexMeta{Name: name, Key: columns, Storing: stored}
}

// crewTablesInstance is the crew auth's instance over every table the schema holds.
func crewTablesInstance(t *testing.T, kinds ...identityTable) *identityInstance {
	t.Helper()
	sessionTables, err := session.TablesWithPrefix("Crew")
	if err != nil {
		t.Fatalf("session.TablesWithPrefix() error = %v", err)
	}
	policyTables, err := access.TablesWithPrefix("Crew", "")
	if err != nil {
		t.Fatalf("access.TablesWithPrefix() error = %v", err)
	}
	inst := &identityInstance{auth: "Crew", session: sessionTables, policy: policyTables, tables: map[identityTable]bool{}}
	for _, kind := range kinds {
		inst.tables[kind] = true
	}

	return inst
}

// Test_identityDeleteCosts pins what the store's DeleteCosts says a row of each of the
// auth's tables a tenant's delete takes costs: the delete and an entry of each index
// besides the key, with no change record; the withdrawal of an invitation its two columns
// and an entry of each index that holds one of them, in its key or stored. A table the
// schema does not hold has no field.
func Test_identityDeleteCosts(t *testing.T) {
	t.Parallel()

	c := &client{tableMap: map[string]*tableMetadata{
		"CrewUserRoles":         tenantKindsTable("Kind", []indexMeta{indexOn("CrewUserRolesByUserId", []string{"UserId"})}),
		"CrewRoles":             tenantKindsTable("Kind", nil),
		"CrewGroups":            tenantKindsTable("Id", []indexMeta{indexOn("CrewGroupsByName", []string{"Kind", "Name"})}),
		"CrewGroupMembers":      tenantKindsTable("GroupId", []indexMeta{indexOn("CrewGroupMembersByUserId", []string{"UserId"}), {Name: "IDX_FK", Managed: true}}),
		"CrewGroupRoles":        tenantKindsTable("GroupId", []indexMeta{indexOn("CrewGroupRolesByScope", []string{"Kind"})}),
		"CrewSessionIdentities": tenantKindsTable("Id", []indexMeta{indexOn("ByKey", []string{"Method"}), indexOn("ByUserId", []string{"UserId"})}),
		"CrewSessionUsers":      tenantKindsTable("Id", []indexMeta{indexOn("ByUsername", []string{"Username"}), indexOn("ByEmail", []string{"Email"})}),
		"CrewSessionMailLinks": tenantKindsTable("Id", []indexMeta{
			indexOn("ByTokenHash", []string{"TokenHash"}),
			indexOn("ByKind", []string{"Kind", "ConsumedAt", "ExpiresAt"}),
			indexOn("ByTenantId", []string{"TenantId", "Kind"}, "Email", "ConsumedAt", "WithdrawnAt"),
		}),
		"CrewSessions": tenantKindsTable("Id", []indexMeta{
			indexOn("ByUserId", []string{"UserId", "Expired"}),
			indexOn("ByUsername", []string{"Username"}),
		}),
		"CrewSessionImpersonations": tenantKindsTable("SessionId", []indexMeta{
			indexOn("ByActor", []string{"ActorUsername"}, "EndedAt"),
			indexOn("BySubject", []string{"SubjectUsername"}),
		}),
	}}
	every := crewTablesInstance(t, identityUsers, identitySessions, identityIdentities, identityMailLinks, identityMemberships, identityRoles,
		identityGroups, identityGroupMembers, identityGroupRoles, identityImpersonations)
	want := []identityDeleteCost{
		{Field: "Membership", Cost: "resource.DeleteCost{IndexEntries: 1}.Mutations(false)"},
		{Field: "Group", Cost: "resource.DeleteCost{IndexEntries: 1}.Mutations(false)"},
		{Field: "GroupMember", Cost: "resource.DeleteCost{IndexEntries: 2}.Mutations(false)"},
		{Field: "GroupRole", Cost: "resource.DeleteCost{IndexEntries: 1}.Mutations(false)"},
		{Field: "Role", Cost: "resource.DeleteCost{IndexEntries: 0}.Mutations(false)"},
		{Field: "Identity", Cost: "resource.DeleteCost{IndexEntries: 2}.Mutations(false)"},
		{Field: "User", Cost: "resource.DeleteCost{IndexEntries: 2}.Mutations(false)"},
		// ConsumedAt and WithdrawnAt, the index by kind and the index by tenant.
		{Field: "Invitation", Cost: "4"},
		// Expired and UpdatedAt, and the index by user, which holds Expired.
		{Field: "SessionEnd", Cost: "3"},
		// EndedAt and EndReason, and the index by actor, which stores EndedAt.
		{Field: "ImpersonationEnd", Cost: "3"},
	}
	if diff := cmp.Diff(want, c.identityDeleteCosts(every)); diff != "" {
		t.Errorf("identityDeleteCosts() of every table (-want +got):\n%s", diff)
	}

	bare := crewTablesInstance(t, identityUsers, identitySessions)
	wantBare := []identityDeleteCost{
		{Field: "User", Cost: "resource.DeleteCost{IndexEntries: 2}.Mutations(false)"},
		{Field: "SessionEnd", Cost: "3"},
	}
	if diff := cmp.Diff(wantBare, c.identityDeleteCosts(bare)); diff != "" {
		t.Errorf("identityDeleteCosts() of the users and sessions alone (-want +got):\n%s", diff)
	}
}

// Test_identityDeleteCosts_heldToTheStore holds the columns the costs of a session's end
// and of an impersonation record's end count to the store's writes of them: each list is
// the columns its update in the store's template sets, so a column added to the write and
// not to the list fails here.
func Test_identityDeleteCosts_heldToTheStore(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		method  string
		columns []string
	}{
		{method: "EndSession", columns: sessionEndColumns},
		{method: "EndImpersonation", columns: impersonationEndColumns},
	} {
		at := strings.Index(identityStoreTemplate, "func (store) "+tt.method+"(ctx ")
		if at < 0 {
			t.Fatalf("the store's template holds no %s that writes", tt.method)
		}
		body, _, found := strings.Cut(identityStoreTemplate[at:], "\n}\n")
		if !found {
			t.Fatalf("the store's template's %s has no end", tt.method)
		}
		var set []string
		for _, match := range regexp.MustCompile(`\.Set([A-Z][A-Za-z]*)\(`).FindAllStringSubmatch(body, -1) {
			set = append(set, match[1])
		}
		if diff := cmp.Diff(tt.columns, set); diff != "" {
			t.Errorf("the columns %s sets and its cost counts (-counted +set):\n%s", tt.method, diff)
		}
	}
}

// Test_identityUserCascades pins what the store's RowsWithUser counts: the rows of each
// table with a foreign key declared ON DELETE CASCADE to the users table, and of each
// table with one to such a table, each with its indexes besides its key. A key that
// refuses the delete, and a cascade that does not lead to the users, are not counted.
func Test_identityUserCascades(t *testing.T) {
	t.Parallel()

	cascades := func(column, table string) foreignKeyMeta {
		return foreignKeyMeta{Columns: []string{column}, ReferencedTable: table, ReferencedColumns: []string{"Id"}, DeleteRule: cascadeDeleteAction}
	}
	c := &client{tableMap: map[string]*tableMetadata{
		"CrewSessionUsers":      tenantKindsTable("Id", nil, cascades("InvitedById", "CrewSessionUsers")),
		"CrewSessionIdentities": tenantKindsTable("Id", []indexMeta{indexOn("ByKey", []string{"Method"}), indexOn("ByUserId", []string{"UserId"})}, cascades("UserId", "CrewSessionUsers")),
		// An application that has not yet dropped the sessions' rule: their events go too.
		"CrewSessions":           tenantKindsTable("Id", []indexMeta{indexOn("ByUserId", []string{"UserId"})}, cascades("UserId", "CrewSessionUsers")),
		"CrewSessionAuthEvents":  tenantKindsTable("Id", nil, cascades("SessionId", "CrewSessions")),
		"CrewSessionPending":     tenantKindsTable("Id", nil),
		"CrewSessionFactorCodes": tenantKindsTable("Id", nil, cascades("PendingId", "CrewSessionPending")),
		"Notes": tenantKindsTable("Id", nil, foreignKeyMeta{
			Columns: []string{"AuthorId"}, ReferencedTable: "CrewSessionUsers", ReferencedColumns: []string{"Id"}, DeleteRule: "NO ACTION",
		}),
	}}
	want := []identityUserCascade{
		{
			Table:        "CrewSessionAuthEvents",
			Count:        "(SELECT COUNT(*) FROM CrewSessionAuthEvents c0 WHERE EXISTS (SELECT 1 FROM CrewSessions c1 WHERE c1.Id = c0.SessionId AND c1.UserId = @userId))",
			IndexEntries: 0,
		},
		{Table: "CrewSessionIdentities", Count: "(SELECT COUNT(*) FROM CrewSessionIdentities c0 WHERE c0.UserId = @userId)", IndexEntries: 2},
		{Table: "CrewSessions", Count: "(SELECT COUNT(*) FROM CrewSessions c0 WHERE c0.UserId = @userId)", IndexEntries: 1},
	}
	got := c.identityUserCascades("CrewSessionUsers")
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("identityUserCascades() (-want +got):\n%s", diff)
	}
	data := &identitySourceData{identityInstance: &identityInstance{userCascades: got}}
	wantSQL := `"SELECT (SELECT COUNT(*) FROM CrewSessionAuthEvents c0 WHERE EXISTS (SELECT 1 FROM CrewSessions c1 WHERE c1.Id = c0.SessionId AND c1.UserId = @userId)), ` +
		`(SELECT COUNT(*) FROM CrewSessionIdentities c0 WHERE c0.UserId = @userId), (SELECT COUNT(*) FROM CrewSessions c0 WHERE c0.UserId = @userId)"`
	if got := data.RowsWithUserSQL(); got != wantSQL {
		t.Errorf("RowsWithUserSQL() = %s, want %s", got, wantSQL)
	}
	if got := (&client{tableMap: map[string]*tableMetadata{"CrewSessionUsers": tenantKindsTable("Id", nil)}}).identityUserCascades("CrewSessionUsers"); got != nil {
		t.Errorf("identityUserCascades() of a schema with no rule = %v, want none", got)
	}
}

// Test_identityStoreTemplate_tenantKinds pins the four reads and answers the identity
// package takes a tenant's rows by, each where the schema holds its tables and as a stub
// that answers nothing where it does not, the custom roles' delete that leaves their
// grants to the database's rule, and the client's TenantKinds in place of the clearing.
func Test_identityStoreTemplate_tenantKinds(t *testing.T) {
	t.Parallel()

	directory := &identityTenantDirectory{OnlyColumn: "DirectoryOnly", Table: "SectorDirectories", TenantColumn: "SectorId", MethodColumn: "Method", ConnectionColumn: "Connection"}
	data := func(tenant *identityTenant, costs []identityDeleteCost, cascades []identityUserCascade, tables ...identityTable) *identitySourceData {
		inst := &identityInstance{auth: "Crew", tenant: tenant, tables: map[identityTable]bool{}, deleteCosts: costs, userCascades: cascades}
		for _, kind := range tables {
			inst.tables[kind] = true
		}

		return &identitySourceData{
			Source: "Auth", Package: "crewidentity", identityInstance: inst, ResourcesPackage: "crewresources", MethodsPackage: "crewidentity", Outlets: "default",
			Tables: session.Tables{Users: "CrewSessionUsers", Identities: "CrewSessionIdentities"},
			Policy: access.Tables{UserRoles: "CrewUserRoles", Roles: "CrewRoles"},
		}
	}
	sector := &identityTenant{name: "Sector", table: "Sectors", key: "Id", word: "sector", directory: directory}
	costs := []identityDeleteCost{
		{Field: "Membership", Cost: "resource.DeleteCost{IndexEntries: 1}.Mutations(false)"},
		{Field: "User", Cost: "resource.DeleteCost{IndexEntries: 2}.Mutations(false)"},
		{Field: "Invitation", Cost: "4"},
	}
	cascades := []identityUserCascade{{Table: "CrewSessionIdentities", Count: "(SELECT COUNT(*) FROM CrewSessionIdentities c0 WHERE c0.UserId = @userId)", IndexEntries: 2}}

	const (
		directoryUsers = "func (store) DirectoryUsers(ctx context.Context, txn resource.ReadWriteTransaction, domain accesstypes.Domain) ([]impulse.UUID, error) {\n" +
			"\tquery := crewresources.NewCrewSectorUserQuery().\n\t\tAddColumns(crewresources.NewCrewSectorUserColumns().ID()).\n" +
			"\t\tWhere(crewresources.NewCrewSectorUserQueryClause().DomainID().Equal(string(domain)).And().ManagedHere().Equal(true))"
		noDirectoryUsers = "func (store) DirectoryUsers(context.Context, resource.ReadWriteTransaction, accesstypes.Domain) ([]impulse.UUID, error) {\n\treturn nil, nil\n}"
		tenantRoles      = "func (store) TenantRoles(ctx context.Context, txn resource.ReadWriteTransaction, domain accesstypes.Domain) (int, error) {"
		noTenantRoles    = "func (store) TenantRoles(context.Context, resource.ReadWriteTransaction, accesstypes.Domain) (int, error) {\n\treturn 0, nil\n}"
		deleteCosts      = "func (store) DeleteCosts() identity.TenantDeleteCosts {\n\treturn identity.TenantDeleteCosts{\n" +
			"\t\tMembership: resource.DeleteCost{IndexEntries: 1}.Mutations(false),\n" +
			"\t\tUser: resource.DeleteCost{IndexEntries: 2}.Mutations(false),\n" +
			"\t\tInvitation: 4,\n\t}\n}"
		bareCosts    = "func (store) DeleteCosts() identity.TenantDeleteCosts {\n\treturn identity.TenantDeleteCosts{\n\t\tUser: resource.DeleteCost{IndexEntries: 2}.Mutations(false),\n\t}\n}"
		rowsWithUser = "func (store) RowsWithUser(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) (int, error) {\n" +
			"\tstmt := spanner.Statement{\n\t\tSQL: \"SELECT (SELECT COUNT(*) FROM CrewSessionIdentities c0 WHERE c0.UserId = @userId)\",\n" +
			"\t\tParams: map[string]any{\"userId\": userID.String()},\n\t}\n" +
			"\tcosts := []int{\n\t\tresource.DeleteCost{IndexEntries: 2}.Mutations(false),\n\t}"
		noRowsWithUser = "func (store) RowsWithUser(context.Context, resource.ReadWriteTransaction, impulse.UUID) (int, error) {\n\treturn 0, nil\n}"
		grantDelete    = "RoleGrantDeletePatch"
	)
	tests := []struct {
		name            string
		data            *identitySourceData
		wantContains    []string
		wantNotContains []string
	}{
		{
			name: "a tenant with a directory, an auth that keeps identities and memberships, a rule on the users",
			data: data(sector, costs, cascades, identityUsers, identitySessions, identityIdentities, identityMemberships, identityRoles, identityRoleGrants, identityMailLinks),
			wantContains: []string{
				directoryUsers, tenantRoles, deleteCosts, rowsWithUser,
				"// (identity.Store.RowsWithUser). The tables: CrewSessionIdentities.",
				"// DeleteTenantRoles removes the custom roles made in one tenant: the one write here to\n// the roles table,",
			},
			wantNotContains: []string{noDirectoryUsers, noTenantRoles, noRowsWithUser, grantDelete},
		},
		{
			name:            "no tenant record, the users and sessions alone, no rule on the users",
			data:            data(nil, costs[1:2], nil, identityUsers, identitySessions),
			wantContains:    []string{noDirectoryUsers, noTenantRoles, bareCosts, noRowsWithUser},
			wantNotContains: []string{directoryUsers, tenantRoles, rowsWithUser},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &client{}
			out, err := c.generateTemplateOutput("identityStoreTemplate", identityStoreTemplate, tt.data)
			if err != nil {
				t.Fatalf("generateTemplateOutput(identityStoreTemplate) error = %v", err)
			}
			assertContains(t, "the store", squashSpace(string(out)), squashEach(tt.wantContains), squashEach(tt.wantNotContains))

			client, err := c.generateTemplateOutput("identityClientTemplate", identityClientTemplate, tt.data)
			if err != nil {
				t.Fatalf("generateTemplateOutput(identityClientTemplate) error = %v", err)
			}
			kinds := "func (c *Client) TenantKinds(domain accesstypes.Domain) []resource.TakenKind {\n\treturn c.manager.TenantKinds(domain)\n}"
			assertContains(t, "the client", squashSpace(string(client)), squashEach([]string{kinds}), []string{"ClearTenant"})
		})
	}
}
