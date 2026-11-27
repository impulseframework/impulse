package generation

import (
	"regexp"
	"strconv"
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

// tenantCostTables are the crew auth's tables the delete cost tests read: each table's
// key and its indexes besides it, as the schema holds them.
func tenantCostTables() map[string]*tableMetadata {
	return map[string]*tableMetadata{
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
	}
}

// Test_identityDeleteCosts pins what the store's DeleteCosts says a row of each of the
// auth's tables a tenant's delete takes costs: the delete and an entry of each index
// besides the key, with no change record; the withdrawal of an invitation, a session's
// end and an impersonation record's end what an update of the columns each writes costs.
// A table the schema does not hold has no field.
func Test_identityDeleteCosts(t *testing.T) {
	t.Parallel()

	c := &client{tableMap: tenantCostTables()}
	every := crewTablesInstance(t, identityUsers, identitySessions, identityIdentities, identityMailLinks, identityMemberships, identityRoles,
		identityGroups, identityGroupMembers, identityGroupRoles, identityImpersonations)
	want := []identityDeleteCost{
		{Field: "Membership", Cost: "resource.DeleteCost{IndexEntries: 1}.Mutations(0)"},
		{Field: "Group", Cost: "resource.DeleteCost{IndexEntries: 1}.Mutations(0)"},
		{Field: "GroupMember", Cost: "resource.DeleteCost{IndexEntries: 2}.Mutations(0)"},
		{Field: "GroupRole", Cost: "resource.DeleteCost{IndexEntries: 1}.Mutations(0)"},
		{Field: "Role", Cost: "resource.DeleteCost{IndexEntries: 0}.Mutations(0)"},
		{Field: "Identity", Cost: "resource.DeleteCost{IndexEntries: 2}.Mutations(0)"},
		{Field: "User", Cost: "resource.DeleteCost{IndexEntries: 2}.Mutations(0)"},
		// The key, ConsumedAt and WithdrawnAt, two for the index by kind, three for the
		// index by tenant.
		{Field: "Invitation", Cost: "8"},
		// The key, Expired and UpdatedAt, two for the index by user, which keys Expired.
		{Field: "SessionEnd", Cost: "5"},
		// The key, EndedAt and EndReason, two for the index by actor, which stores EndedAt.
		{Field: "ImpersonationEnd", Cost: "5"},
	}
	if diff := cmp.Diff(want, c.identityDeleteCosts(every)); diff != "" {
		t.Errorf("identityDeleteCosts() of every table (-want +got):\n%s", diff)
	}

	bare := crewTablesInstance(t, identityUsers, identitySessions)
	wantBare := []identityDeleteCost{
		{Field: "User", Cost: "resource.DeleteCost{IndexEntries: 2}.Mutations(0)"},
		{Field: "SessionEnd", Cost: "5"},
	}
	if diff := cmp.Diff(wantBare, c.identityDeleteCosts(bare)); diff != "" {
		t.Errorf("identityDeleteCosts() of the users and sessions alone (-want +got):\n%s", diff)
	}
}

// Test_updateMutations pins what an update of the columns on one row costs: one for each
// column of the key, one for each column written, and for each index besides the key that
// holds a written column, two where one is in the index's key and otherwise one and one
// for each written column it stores.
func Test_updateMutations(t *testing.T) {
	t.Parallel()

	tables := tenantCostTables()
	twoColumnKey := &tableMetadata{Columns: map[string]columnMeta{
		"TenantId": {IsPrimaryKey: true, KeyOrdinalPosition: 1},
		"Id":       {IsPrimaryKey: true, KeyOrdinalPosition: 2},
	}}
	storedOnly := tenantKindsTable("Id", []indexMeta{indexOn("ByName", []string{"Name"}, "Note")})
	for _, tt := range []struct {
		name    string
		table   *tableMetadata
		columns []string
		want    int
	}{
		{name: "an invitation's withdrawal", table: tables["CrewSessionMailLinks"], columns: withdrawnColumns, want: 8},
		{name: "a session's end", table: tables["CrewSessions"], columns: sessionEndColumns, want: 5},
		{name: "an impersonation record's end", table: tables["CrewSessionImpersonations"], columns: impersonationEndColumns, want: 5},
		{name: "a key of two columns and no index", table: twoColumnKey, columns: []string{"Name"}, want: 3},
		// The key, Note, and two for the index, which stores Note and keys none.
		{name: "an index that stores the column alone", table: storedOnly, columns: []string{"Note"}, want: 4},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.table.updateMutations(tt.columns); got != tt.want {
				t.Errorf("updateMutations(%v) = %d, want %d", tt.columns, got, tt.want)
			}
		})
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
			PersonCount:  "(SELECT COUNT(*) FROM CrewSessionAuthEvents c0 WHERE EXISTS (SELECT 1 FROM CrewSessions c1 WHERE c1.Id = c0.SessionId AND c1.UserId = p.UserId))",
			IndexEntries: 0,
		},
		{
			Table: "CrewSessionIdentities", Count: "(SELECT COUNT(*) FROM CrewSessionIdentities c0 WHERE c0.UserId = @userId)",
			PersonCount: "(SELECT COUNT(*) FROM CrewSessionIdentities c0 WHERE c0.UserId = p.UserId)", IndexEntries: 2,
		},
		{
			Table: "CrewSessions", Count: "(SELECT COUNT(*) FROM CrewSessions c0 WHERE c0.UserId = @userId)",
			PersonCount: "(SELECT COUNT(*) FROM CrewSessions c0 WHERE c0.UserId = p.UserId)", IndexEntries: 1,
		},
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
		{Field: "Membership", Cost: "resource.DeleteCost{IndexEntries: 1}.Mutations(0)"},
		{Field: "User", Cost: "resource.DeleteCost{IndexEntries: 2}.Mutations(0)"},
		{Field: "Invitation", Cost: "4"},
	}
	cascades := []identityUserCascade{{Table: "CrewSessionIdentities", Count: "(SELECT COUNT(*) FROM CrewSessionIdentities c0 WHERE c0.UserId = @userId)", IndexEntries: 2}}

	const (
		tenantHoldings   = "func (store) TenantHoldings(ctx context.Context, txn resource.ReadWriteTransaction, domain accesstypes.Domain) ([]identity.PersonHoldings, error) {"
		noTenantHoldings = "func (store) TenantHoldings(context.Context, resource.ReadWriteTransaction, accesstypes.Domain) ([]identity.PersonHoldings, error) {\n\treturn nil, nil\n}"
		tenantRoles      = "func (store) TenantRoles(ctx context.Context, txn resource.ReadWriteTransaction, domain accesstypes.Domain) (int, error) {"
		noTenantRoles    = "func (store) TenantRoles(context.Context, resource.ReadWriteTransaction, accesstypes.Domain) (int, error) {\n\treturn 0, nil\n}"
		deleteCosts      = "func (store) DeleteCosts() identity.TenantDeleteCosts {\n\treturn identity.TenantDeleteCosts{\n" +
			"\t\tMembership: resource.DeleteCost{IndexEntries: 1}.Mutations(0),\n" +
			"\t\tUser: resource.DeleteCost{IndexEntries: 2}.Mutations(0),\n" +
			"\t\tInvitation: 4,\n\t}\n}"
		bareCosts    = "func (store) DeleteCosts() identity.TenantDeleteCosts {\n\treturn identity.TenantDeleteCosts{\n\t\tUser: resource.DeleteCost{IndexEntries: 2}.Mutations(0),\n\t}\n}"
		rowsWithUser = "func (store) RowsWithUser(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) (int, error) {\n" +
			"\tstmt := spanner.Statement{\n\t\tSQL: \"SELECT (SELECT COUNT(*) FROM CrewSessionIdentities c0 WHERE c0.UserId = @userId)\",\n" +
			"\t\tParams: map[string]any{\"userId\": userID.String()},\n\t}\n" +
			"\tcosts := []int{\n\t\tresource.DeleteCost{IndexEntries: 2}.Mutations(0),\n\t}"
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
				tenantHoldings, tenantRoles, deleteCosts, rowsWithUser,
				"// (identity.Store.RowsWithUser). The tables: CrewSessionIdentities.",
				"// DeleteTenantRoles removes the custom roles made in one tenant: the one write here to\n// the roles table,",
			},
			wantNotContains: []string{noTenantHoldings, noTenantRoles, noRowsWithUser, grantDelete},
		},
		{
			name:            "no tenant record, the users and sessions alone, no rule on the users",
			data:            data(nil, costs[1:2], nil, identityUsers, identitySessions),
			wantContains:    []string{noTenantHoldings, noTenantRoles, bareCosts, noRowsWithUser},
			wantNotContains: []string{tenantHoldings, tenantRoles, rowsWithUser},
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
			// A record's delete is handed the roles held on it through the client too.
			recordKinds := "func (c *Client) RecordKinds(at accesstypes.RecordScope) []resource.TakenKind {\n\treturn c.manager.RecordKinds(at)\n}"
			assertContains(t, "the client", squashSpace(string(client)), squashEach([]string{kinds, recordKinds}), []string{"ClearTenant"})
		})
	}
}

// Test_identityStoreTemplate_tenantHoldings pins the store's read of the people a tenant's
// delete considers: one statement from the tenant's key, a branch of its union and a
// column for each table the store keeps a person's holdings in, each reached by the
// table's column of the user, and the directories table where the tenant record declares
// a directory and the auth keeps identities; and a second over the people it found, their
// sessions, the records they hold as actor where the store keeps the impersonations
// table, and a count of each table the database removes rows of with the user's row.
// Nothing reads the tenant's list of its people. A store that keeps none of the tables
// answers nobody.
func Test_identityStoreTemplate_tenantHoldings(t *testing.T) {
	t.Parallel()

	directory := &identityTenantDirectory{OnlyColumn: "DirectoryOnly", Table: "SectorDirectories", TenantColumn: "SectorId", MethodColumn: "Method", ConnectionColumn: "Connection"}
	identities := identityUserCascade{
		Table: "CrewSessionIdentities", Count: "(SELECT COUNT(*) FROM CrewSessionIdentities c0 WHERE c0.UserId = @userId)",
		PersonCount: "(SELECT COUNT(*) FROM CrewSessionIdentities c0 WHERE c0.UserId = p.UserId)", IndexEntries: 2,
	}
	data := func(tenant *identityTenant, cascades []identityUserCascade, tables ...identityTable) *identitySourceData {
		inst := &identityInstance{auth: "Crew", tenant: tenant, tables: map[identityTable]bool{}, heldRecords: []identityHeldRecord{{}}, userCascades: cascades}
		for _, kind := range tables {
			inst.tables[kind] = true
		}

		return &identitySourceData{
			Source: "Auth", Package: "crewidentity", identityInstance: inst, ResourcesPackage: "crewresources", MethodsPackage: "crewidentity", Outlets: "default",
			Tables: session.Tables{
				Users: "CrewSessionUsers", Identities: "CrewSessionIdentities", Sessions: "CrewSessions", Impersonations: "CrewSessionImpersonations",
			},
			Policy: access.Tables{
				UserRoles: "CrewUserRoles", Roles: "CrewRoles", Groups: "CrewGroups", GroupMembers: "CrewGroupMembers", RecordUserRoles: "CrewRecordUserRoles",
			},
		}
	}
	every := []identityTable{identityUsers, identitySessions, identityIdentities, identityMemberships, identityGroups, identityGroupMembers, identityRecordUserRoles}
	const (
		username    = "(SELECT u.Username FROM CrewSessionUsers u WHERE u.Id = p.UserId) AS Username"
		memberships = "ARRAY(SELECT AS STRUCT m.Kind, m.Axis, m.DomainId, m.Role FROM CrewUserRoles m WHERE m.UserId = p.UserId) AS Memberships"
		places      = "ARRAY(SELECT AS STRUCT g.Id, g.Kind, g.Axis, g.DomainId FROM CrewGroupMembers gm JOIN CrewGroups g ON g.Id = gm.GroupId WHERE gm.UserId = p.UserId) AS Places"
		roles       = "ARRAY(SELECT AS STRUCT r.Axis, r.DomainId, r.Resource, r.RecordId, r.Role, r.EndsAt FROM CrewRecordUserRoles r WHERE r.UserId = p.UserId) AS RecordRoles"
		links       = "ARRAY(SELECT AS STRUCT i.Id, i.Method, i.Connection, " +
			"ARRAY(SELECT d.SectorId FROM SectorDirectories d WHERE d.Method = i.Method AND d.Connection = i.Connection) AS Tenants " +
			"FROM CrewSessionIdentities i WHERE i.UserId = p.UserId) AS Links"
		plainLinks = "ARRAY(SELECT AS STRUCT i.Id, i.Method, i.Connection FROM CrewSessionIdentities i WHERE i.UserId = p.UserId) AS Links"
		heldHere   = "SELECT m.UserId FROM CrewUserRoles m WHERE m.Kind = @kind AND m.Axis = @axis AND m.DomainId = @domain"
		inGroups   = "SELECT gm.UserId FROM CrewGroups g JOIN CrewGroupMembers gm ON gm.GroupId = g.Id WHERE g.Kind = @groupKind AND g.Axis = @groupAxis AND g.DomainId = @groupDomain"
		onRecords  = "SELECT r.UserId FROM CrewRecordUserRoles r WHERE r.Axis = @recordAxis AND r.DomainId = @recordDomain"
		through    = "SELECT i.UserId FROM SectorDirectories d JOIN CrewSessionIdentities i ON i.Method = d.Method AND i.Connection = d.Connection WHERE d.SectorId = @tenant"
		signature  = "func (store) TenantHoldings(ctx context.Context, txn resource.ReadWriteTransaction, domain accesstypes.Domain) ([]identity.PersonHoldings, error) {"
		stub       = "func (store) TenantHoldings(context.Context, resource.ReadWriteTransaction, accesstypes.Domain) ([]identity.PersonHoldings, error) {\n\treturn nil, nil\n}"
		sessions   = "SELECT p.UserId, ARRAY(SELECT AS STRUCT s.Id"
		records    = ", EXISTS (SELECT 1 FROM CrewSessionImpersonations r WHERE r.SessionId = s.Id AND r.EndedAt IS NULL) AS Record"
		ownRows    = " FROM CrewSessions s WHERE s.UserId = p.UserId AND s.Expired = FALSE) AS Sessions"
		held       = ", ARRAY(SELECT AS STRUCT r.SessionId, EXISTS (SELECT 1 FROM CrewSessions s WHERE s.Id = r.SessionId AND s.Expired = FALSE) AS Session" +
			" FROM CrewSessionUsers u JOIN CrewSessionImpersonations r ON r.ActorUsername = u.Username WHERE u.Id = p.UserId AND r.ActorRealm IS NULL AND r.EndedAt IS NULL) AS Held"
		withUser = ", (SELECT COUNT(*) FROM CrewSessionIdentities c0 WHERE c0.UserId = p.UserId) AS WithUser0"
		overThem = " FROM (SELECT id AS UserId FROM UNNEST(@people) AS id) p ORDER BY p.UserId"
	)
	for _, tt := range []struct {
		name            string
		data            *identitySourceData
		wantSQL         string
		wantSessionsSQL string
		wantContains    []string
		wantNotContains []string
		// notInRead are what the read itself, from its signature to its end, holds none of.
		notInRead []string
	}{
		{
			name: "every table, a tenant record with a directory, a rule on the users",
			data: data(&identityTenant{name: "Sector", table: "Sectors", key: "Id", word: "sector", directory: directory},
				[]identityUserCascade{identities}, append(every, identityImpersonations)...),
			wantSQL: "SELECT p.UserId, " + strings.Join([]string{username, memberships, places, roles, links}, ", ") +
				" FROM (" + strings.Join([]string{heldHere, inGroups, onRecords, through}, " UNION DISTINCT ") + ") p ORDER BY p.UserId",
			wantSessionsSQL: sessions + records + ownRows + held + withUser + overThem,
			wantContains: []string{
				signature, "type tenantHoldingsMembership struct {", "type tenantHoldingsPlace struct {", "type tenantHoldingsRecordRole struct {",
				"Tenants []string `spanner:\"Tenants\"`", `stmt.Params["tenant"] = string(domain)`,
				"Record bool `spanner:\"Record\"`", "type tenantHoldingsHeld struct {", "costs := []int{\n\t\tresource.DeleteCost{IndexEntries: 2}.Mutations(0),\n\t}",
				`row.ColumnByName("WithUser"+strconv.Itoa(i), &count)`, "ids = append(ids, user)",
			},
			wantNotContains: []string{stub, "ManagingTenants", "DirectoryUsers", "people[i].ID.String()"},
			notInRead:       []string{"NewCrewSectorUserQuery", "CrewSectorUser"},
		},
		{
			name: "every table but the impersonations, a tenant record with no directory, no rule on the users",
			data: data(&identityTenant{name: "Sector", table: "Sectors", key: "Id", word: "sector"}, nil, every...),
			wantSQL: "SELECT p.UserId, " + strings.Join([]string{username, memberships, places, roles, plainLinks}, ", ") +
				" FROM (" + strings.Join([]string{heldHere, inGroups, onRecords}, " UNION DISTINCT ") + ") p ORDER BY p.UserId",
			wantSessionsSQL: sessions + ownRows + overThem,
			wantContains:    []string{signature, "type tenantHoldingsLink struct {", "type tenantHoldingsSession struct {"},
			wantNotContains: []string{
				stub, "SectorDirectories", "Tenants []string", `stmt.Params["tenant"]`, "tenantHoldingsHeld", "Record bool", `row.ColumnByName("WithUser"`,
			},
		},
		{
			// An auth that keeps identities and no memberships, under a tenant record with a
			// directory: the people are those who sign in through the tenant's directory.
			name: "the identities alone, a tenant record with a directory",
			data: data(&identityTenant{name: "Sector", table: "Sectors", key: "Id", word: "sector", directory: directory},
				nil, identityUsers, identitySessions, identityIdentities),
			wantSQL:         "SELECT p.UserId, " + username + ", " + links + " FROM (" + through + ") p ORDER BY p.UserId",
			wantSessionsSQL: sessions + ownRows + overThem,
			wantContains:    []string{signature, "Tenants []string `spanner:\"Tenants\"`", `stmt.Params["tenant"] = string(domain)`},
			wantNotContains: []string{stub, "tenantHoldingsMembership", "tenantHoldingsPlace", "tenantHoldingsRecordRole", `stmt.Params["kind"]`},
		},
		{
			name:            "the users and sessions alone",
			data:            data(nil, nil, identityUsers, identitySessions),
			wantContains:    []string{stub},
			wantNotContains: []string{signature, "tenantHoldings"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := ""
			if quoted := tt.data.TenantHoldingsSQL(); quoted != "" {
				var err error
				if got, err = strconv.Unquote(quoted); err != nil {
					t.Fatalf("TenantHoldingsSQL() = %s, not a Go string literal: %v", quoted, err)
				}
			}
			if got != tt.wantSQL {
				t.Errorf("TenantHoldingsSQL() =\n%s\nwant\n%s", got, tt.wantSQL)
			}
			if tt.wantSessionsSQL != "" {
				got, err := strconv.Unquote(tt.data.TenantHoldingsSessionsSQL())
				if err != nil || got != tt.wantSessionsSQL {
					t.Errorf("TenantHoldingsSessionsSQL() =\n%s (%v)\nwant\n%s", got, err, tt.wantSessionsSQL)
				}
			}
			c := &client{}
			out, err := c.generateTemplateOutput("identityStoreTemplate", identityStoreTemplate, tt.data)
			if err != nil {
				t.Fatalf("generateTemplateOutput(identityStoreTemplate) error = %v", err)
			}
			store := string(out)
			assertContains(t, "the store", squashSpace(store), squashEach(tt.wantContains), squashEach(tt.wantNotContains))
			if len(tt.notInRead) > 0 {
				at := strings.Index(store, signature)
				if at < 0 {
					t.Fatal("the store holds no TenantHoldings that reads")
				}
				read, _, _ := strings.Cut(store[at:], "\n}\n")
				assertContains(t, "the store's TenantHoldings", read, nil, tt.notInRead)
			}
		})
	}
}
