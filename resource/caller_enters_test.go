package resource

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
)

// The fixture of a row whose struct names the tenants a caller's session must enter
// before a method is offered on it (TenantsToEnter). The row's struct is generic over
// what its TenantsToEnter answers, so each declaration a test needs is a resource of
// its own.
const (
	heldResourceName accesstypes.Resource = "heldResources"
	heldViewName     accesstypes.Resource = "heldViews"
)

// heldQuery is the TenantsToEnter query of the statement-text tests.
const heldQuery = "SELECT KeeperId AS RowKey, Tenant FROM KeeperPosts WHERE Kind = @postKind"

// heldDeclaration is what a held fixture's TenantsToEnter answers.
type heldDeclaration interface {
	declaration() TenantsToEnter
}

// heldKeepers is the declaration that renders: the query, its parameter and the row's
// key.
type heldKeepers struct{}

func (heldKeepers) declaration() TenantsToEnter {
	return TenantsToEnter{Query: heldQuery, Params: map[string]any{"postKind": "keeper"}, Match: map[string]string{"RowKey": "Id"}}
}

// heldPairsQuery is the TenantsToEnter query of a row named by two columns: it selects a
// column it does not match beside its two key columns and Tenant.
const heldPairsQuery = "SELECT KeeperId AS RowKey, KeeperName, Station, Tenant FROM KeeperPosts WHERE Kind = @postKind"

// heldPairs is a declaration that matches its query on two columns of the row, given in
// the reverse of their names' order.
type heldPairs struct{}

func (heldPairs) declaration() TenantsToEnter {
	return TenantsToEnter{Query: heldPairsQuery, Params: map[string]any{"postKind": "keeper"}, Match: map[string]string{"RowKey": "Id", "KeeperName": "Name"}}
}

// The declarations the statement refuses.
type (
	heldEmptyQuery  struct{}
	heldEmptyColumn struct{}
	heldEmptyName   struct{}
	heldWith        struct{}
	heldUnderscore  struct{}
	heldCollision   struct{}
	heldViewParam   struct{}
)

func (heldEmptyQuery) declaration() TenantsToEnter {
	return TenantsToEnter{Query: "  ", Match: map[string]string{"RowKey": "Id"}}
}

func (heldEmptyColumn) declaration() TenantsToEnter {
	return TenantsToEnter{Query: heldQuery, Params: map[string]any{"postKind": "keeper"}}
}

// heldEmptyName pairs the query's RowKey with a column of the row and a column of the
// row with no key column of the query.
func (heldEmptyName) declaration() TenantsToEnter {
	return TenantsToEnter{Query: heldQuery, Params: map[string]any{"postKind": "keeper"}, Match: map[string]string{"RowKey": "Id", " ": "Name"}}
}

func (heldWith) declaration() TenantsToEnter {
	return TenantsToEnter{Query: "WITH posts AS (SELECT KeeperId, Tenant FROM KeeperPosts) SELECT KeeperId AS RowKey, Tenant FROM posts", Match: map[string]string{"RowKey": "Id"}}
}

func (heldUnderscore) declaration() TenantsToEnter {
	return TenantsToEnter{Query: heldQuery, Params: map[string]any{"_postKind": "keeper"}, Match: map[string]string{"RowKey": "Id"}}
}

// heldCollision binds the name the statement's tenancy predicate binds.
func (heldCollision) declaration() TenantsToEnter {
	return TenantsToEnter{Query: heldQuery, Params: map[string]any{"domain": "keeper"}, Match: map[string]string{"RowKey": "Id"}}
}

// heldViewParam binds the name the view's own query binds.
func (heldViewParam) declaration() TenantsToEnter {
	return TenantsToEnter{Query: heldQuery, Params: map[string]any{"viewKind": "keeper"}, Match: map[string]string{"RowKey": "Id"}}
}

// heldResource is a table whose struct names the tenants by D.
type heldResource[D heldDeclaration] struct {
	ID      impulse.UUID `spanner:"Id"      postgres:"Id"`
	Name    string       `spanner:"Name"    postgres:"Name"`
	Station string       `spanner:"Station" postgres:"Station"`
}

func (heldResource[D]) Resource() accesstypes.Resource {
	return heldResourceName
}

func (heldResource[D]) DefaultConfig() Config {
	return Config{}
}

func (heldResource[D]) TenantsToEnter() TenantsToEnter {
	var d D

	return d.declaration()
}

// heldView is a view (a @virtual resource) whose struct names the tenants by D.
type heldView[D heldDeclaration] struct {
	ID      impulse.UUID `spanner:"Id"      postgres:"Id"`
	Name    string       `spanner:"Name"    postgres:"Name"`
	Station string       `spanner:"Station" postgres:"Station"`
}

func (heldView[D]) Resource() accesstypes.Resource {
	return heldViewName
}

func (heldView[D]) DefaultConfig() Config {
	return Config{}
}

func (heldView[D]) Subquery() (query string, params map[string]any) {
	return "SELECT Id, Name, Station FROM Keepers WHERE Kind = @viewKind", map[string]any{"viewKind": "keeper"}
}

func (heldView[D]) TenantsToEnter() TenantsToEnter {
	var d D

	return d.declaration()
}

type heldReadRequest struct {
	ID      impulse.UUID `json:"id"   perm:"-"`
	Name    string       `json:"name"`
	Station string       `json:"-"`
}

// heldCollection registers res with the owner and state attributes and its tenant
// column, and four methods that name its rows: RetireKeeper and RenameKeeper plain,
// SealKeeper a transition, all three marked @callerEnters when marked says so, and
// GreetKeeper plain and never marked.
func heldCollection(t *testing.T, res accesstypes.Resource, marked bool) *GeneratedCollection {
	t.Helper()

	g, err := NewGeneratedCollection(CollectionData{Resources: []CollectionResource{
		{
			Name:        res,
			Scope:       accesstypes.DomainPermissionScope,
			Permissions: []accesstypes.Permission{accesstypes.Read},
			Attributes: []AttributeData{
				{Name: "owner", Column: "Owner", Type: AttributeTypeString},
				{Name: StateAttribute, Column: "State", Type: AttributeTypeString},
			},
			Domain: &DomainBindingData{Column: "Station"},
		},
		{Name: "GreetKeeper", Scope: accesstypes.DomainPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute}, Target: res},
		{Name: "RenameKeeper", Scope: accesstypes.DomainPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute}, Target: res, CallerEnters: marked},
		{Name: "RetireKeeper", Scope: accesstypes.DomainPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute}, Target: res, CallerEnters: marked},
		{
			Name: "SealKeeper", Scope: accesstypes.DomainPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute},
			Transition: &TransitionData{Target: res, From: []string{"open"}, To: "sealed"}, CallerEnters: marked,
		},
	}})
	if err != nil {
		t.Fatalf("NewGeneratedCollection() error = %v", err)
	}

	return g
}

// heldPermissions is capStubPermissions that also answers the tenants the session does
// not enter, as the permissions SessionPermissions composes do, and counts how often it
// is asked.
type heldPermissions struct {
	capStubPermissions
	unentered []accesstypes.Domain
	asked     *atomic.Int32
}

func (p heldPermissions) UnenteredDomains(context.Context) ([]accesstypes.Domain, error) {
	p.asked.Add(1)

	return append([]accesstypes.Domain{}, p.unentered...), nil
}

// heldRead is the read of R with capabilities=Execute (when execute says so) as the
// decoder would arm it, checked: the statement's QuerySet, and the check's error.
func heldRead[R Resourcer](t *testing.T, dbType DBType, collection *GeneratedCollection, perms UserPermissions, execute bool) (*QuerySet[R], error) {
	t.Helper()

	rSet, err := NewSet[R, heldReadRequest](accesstypes.Read)
	if err != nil {
		t.Fatalf("NewSet() error = %v", err)
	}
	q := NewQuerySet(NewMetadata[R]())
	q.env = accesstypes.EnvironmentAt(time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC))
	q.jsonNames = map[accesstypes.Field]string{"ID": "id", "Name": "name"}
	q.collection = collection
	q.EnableUserPermissionEnforcement(rSet, perms, testScope, accesstypes.Read)
	for _, field := range []accesstypes.Field{"ID", "Name"} {
		q.AddField(field)
	}
	if execute {
		q.RequestCapabilities(accesstypes.Execute)
	}
	err = q.checkPermissions(t.Context(), dbType)

	return q, err
}

// heldStmt renders the statement of an Execute capability read of R.
func heldStmt[R Resourcer](t *testing.T, dbType DBType, collection *GeneratedCollection, perms UserPermissions) (*Statement, error) {
	t.Helper()

	q, err := heldRead[R](t, dbType, collection, perms, true)
	if err != nil {
		return nil, err
	}

	return q.stmt(dbType)
}

// heldTerm is the entering term as it reads in a normalized statement, over the row
// row names and with the tenants bound under placeholder.
func heldTerm(dbType DBType, row accesstypes.Resource, placeholder string) string {
	if dbType == PostgresDBType {
		return `NOT EXISTS (SELECT 1 FROM (` + heldQuery + ` ) AS "ca1" WHERE "ca1"."RowKey" = "` + string(row) + `"."Id" AND "ca1"."Tenant" = ANY(` + placeholder + `))`
	}

	return "NOT EXISTS (SELECT 1 FROM (" + heldQuery + " ) AS `ca1` WHERE `ca1`.`RowKey` = `" + string(row) + "`.`Id` AND `ca1`.`Tenant` IN UNNEST(" + placeholder + "))"
}

// heldSelect is the statement over heldResources with the checks item, or none.
func heldSelect(dbType DBType, checks string) string {
	if dbType == PostgresDBType {
		item := ""
		if checks != "" {
			item = `, ARRAY[` + checks + `] AS "zzCapabilityChecks"`
		}

		return `SELECT "Id", "Name"` + item + ` FROM heldResources WHERE ("heldResources"."Station" = @domain)`
	}
	item := ""
	if checks != "" {
		item = ", ARRAY<BOOL>[" + checks + "] AS zzCapabilityChecks"
	}

	return "SELECT Id, Name" + item + " FROM heldResources WHERE (`heldResources`.`Station` = @domain)"
}

// TestQuerySet_stmt_callerEnters pins the Execute affordance of a method a row offers
// only to a caller whose session enters the row's tenants (@callerEnters), on both
// databases: while the session enters every tenant the statement is the one without the
// mark, byte for byte; while it does not enter one, a held method's boolean is the
// entering term alone or its own expression ANDed with the term, shared by every held
// method with the same expression; an unheld method and a denied one are as without
// the mark; and the query's parameters join the statement's.
func TestQuerySet_stmt_callerEnters(t *testing.T) {
	t.Parallel()

	bastion := []accesstypes.Domain{"bastion"}
	owner := map[DBType]string{SpannerDBType: "`heldResources`.`Owner` = @subject", PostgresDBType: `"heldResources"."Owner" = @subject`}
	state := map[DBType]string{SpannerDBType: "`heldResources`.`State` IN (@_c1)", PostgresDBType: `"heldResources"."State" IN (@_c1)`}

	tests := []struct {
		name      string
		decisions accesstypes.Decisions
		unentered []accesstypes.Domain
		// checks renders the statement's checks on dbType, "" for none.
		checks func(DBType) string
		// rows are the booleans of a row, each with the methods it lists.
		rows []heldRow
		// wantParams are the statement's parameters beyond domain.
		wantParams map[string]any
	}{
		{
			name:      "a held plain method is offered by the entering term alone",
			decisions: accesstypes.Decisions{"RetireKeeper": accesstypes.Granted()},
			unentered: bastion,
			checks: func(db DBType) string {
				return "(" + heldTerm(db, heldResourceName, "@_c1") + ")"
			},
			rows: []heldRow{
				{checks: []bool{true}, methods: []string{"RetireKeeper"}},
				{checks: []bool{false}, methods: []string{}},
			},
			wantParams: map[string]any{"_c1": []string{"bastion"}, "postKind": "keeper"},
		},
		{
			name:      "a held method under a conditional grant ANDs the condition with the term",
			decisions: accesstypes.Decisions{"RetireKeeper": conditionalOn("RetireKeeper", "owner = subject")},
			unentered: bastion,
			checks: func(db DBType) string {
				return "((" + owner[db] + ") AND " + heldTerm(db, heldResourceName, "@_c1") + ")"
			},
			rows: []heldRow{
				{checks: []bool{true}, methods: []string{"RetireKeeper"}},
				{checks: []bool{false}, methods: []string{}},
			},
			wantParams: map[string]any{"_c1": []string{"bastion"}, "postKind": "keeper", "subject": "u1"},
		},
		{
			name:      "a held transition ANDs its state test with the term",
			decisions: accesstypes.Decisions{"SealKeeper": accesstypes.Granted()},
			unentered: bastion,
			checks: func(db DBType) string {
				return "((" + state[db] + ") AND " + heldTerm(db, heldResourceName, "@_c2") + ")"
			},
			rows: []heldRow{
				{checks: []bool{true}, methods: []string{"SealKeeper"}},
				{checks: []bool{false}, methods: []string{}},
			},
			wantParams: map[string]any{"_c1": "open", "_c2": []string{"bastion"}, "postKind": "keeper"},
		},
		{
			name:      "two held plain methods share one boolean",
			decisions: accesstypes.Decisions{"RenameKeeper": accesstypes.Granted(), "RetireKeeper": accesstypes.Granted()},
			unentered: bastion,
			checks: func(db DBType) string {
				return "(" + heldTerm(db, heldResourceName, "@_c1") + ")"
			},
			rows: []heldRow{
				{checks: []bool{true}, methods: []string{"RenameKeeper", "RetireKeeper"}},
				{checks: []bool{false}, methods: []string{}},
			},
			wantParams: map[string]any{"_c1": []string{"bastion"}, "postKind": "keeper"},
		},
		{
			name:      "an unheld method beside a held one is offered as without the mark",
			decisions: accesstypes.Decisions{"GreetKeeper": accesstypes.Granted(), "RetireKeeper": accesstypes.Granted()},
			unentered: bastion,
			checks: func(db DBType) string {
				return "(" + heldTerm(db, heldResourceName, "@_c1") + ")"
			},
			rows: []heldRow{
				{checks: []bool{true}, methods: []string{"GreetKeeper", "RetireKeeper"}},
				{checks: []bool{false}, methods: []string{"GreetKeeper"}},
			},
			wantParams: map[string]any{"_c1": []string{"bastion"}, "postKind": "keeper"},
		},
		{
			name:      "a denied held method renders nothing",
			decisions: accesstypes.Decisions{"GreetKeeper": accesstypes.Granted()},
			unentered: bastion,
			checks: func(DBType) string {
				return ""
			},
			rows:       []heldRow{{methods: []string{"GreetKeeper"}}},
			wantParams: map[string]any{},
		},
	}
	for _, tt := range tests {
		for _, dbType := range []DBType{SpannerDBType, PostgresDBType} {
			t.Run(tt.name+"/"+string(dbType), func(t *testing.T) {
				t.Parallel()

				perms := heldPermissions{capStubPermissions: capStubPermissions{byPerm: map[accesstypes.Permission]accesstypes.Decisions{accesstypes.Execute: tt.decisions}}, unentered: tt.unentered, asked: &atomic.Int32{}}
				stmt, err := heldStmt[heldResource[heldKeepers]](t, dbType, heldCollection(t, heldResourceName, true), perms)
				if err != nil {
					t.Fatalf("QuerySet.stmt() error = %v", err)
				}
				if got, want := normalizeSQL(stmt.SQL), heldSelect(dbType, tt.checks(dbType)); got != want {
					t.Errorf("QuerySet.stmt() SQL =\n%s\nwant\n%s", got, want)
				}
				wantParams := map[string]any{"domain": "testDomain"}
				for name, value := range tt.wantParams {
					wantParams[name] = value
				}
				if diff := cmp.Diff(wantParams, stmt.Params); diff != "" {
					t.Errorf("QuerySet.stmt() params mismatch (-want +got):\n%s", diff)
				}
				for _, row := range tt.rows {
					got := stmt.capabilityPlan.assemble(row.checks)
					if diff := cmp.Diff(map[accesstypes.Permission]any{accesstypes.Execute: row.methods}, got); diff != "" {
						t.Errorf("capabilityPlan.assemble(%v) mismatch (-want +got):\n%s", row.checks, diff)
					}
				}
				if n := perms.asked.Load(); n != 1 {
					t.Errorf("the tenants not entered were asked %d times, want once", n)
				}
			})
		}
	}
}

// heldRow is one row's booleans and the methods its Execute list then holds.
type heldRow struct {
	checks  []bool
	methods []string
}

// TestQuerySet_stmt_callerEntersEntersEvery pins that a session that enters every
// tenant reads the statement it read without the mark, byte for byte, with the same
// parameters and the same answer, on both databases: nothing is rendered for the mark.
func TestQuerySet_stmt_callerEntersEntersEvery(t *testing.T) {
	t.Parallel()

	decisions := accesstypes.Decisions{
		"GreetKeeper":  accesstypes.Granted(),
		"RetireKeeper": accesstypes.Granted(),
		"RenameKeeper": conditionalOn("RenameKeeper", "owner = subject"),
		"SealKeeper":   accesstypes.Granted(),
	}
	for _, dbType := range []DBType{SpannerDBType, PostgresDBType} {
		t.Run(string(dbType), func(t *testing.T) {
			t.Parallel()

			stmts := make([]*Statement, 0, 2)
			for _, marked := range []bool{true, false} {
				perms := heldPermissions{capStubPermissions: capStubPermissions{byPerm: map[accesstypes.Permission]accesstypes.Decisions{accesstypes.Execute: decisions}}, asked: &atomic.Int32{}}
				stmt, err := heldStmt[heldResource[heldKeepers]](t, dbType, heldCollection(t, heldResourceName, marked), perms)
				if err != nil {
					t.Fatalf("QuerySet.stmt(marked=%v) error = %v", marked, err)
				}
				stmts = append(stmts, stmt)
			}
			marked, unmarked := stmts[0], stmts[1]
			if marked.SQL != unmarked.SQL {
				t.Errorf("the statement with the mark =\n%s\nwant the one without it\n%s", marked.SQL, unmarked.SQL)
			}
			if diff := cmp.Diff(unmarked.Params, marked.Params); diff != "" {
				t.Errorf("the parameters with the mark differ from those without it (-without +with):\n%s", diff)
			}
			checks := []bool{true, false}
			if diff := cmp.Diff(unmarked.capabilityPlan.assemble(checks), marked.capabilityPlan.assemble(checks)); diff != "" {
				t.Errorf("the answer with the mark differs from the one without it (-without +with):\n%s", diff)
			}
		})
	}
}

// TestQuerySet_stmt_callerEntersView pins that the term against a view names the view,
// the name the statement gives the view's query, and that the view's parameters and the
// TenantsToEnter query's both join the statement's.
func TestQuerySet_stmt_callerEntersView(t *testing.T) {
	t.Parallel()

	perms := heldPermissions{
		capStubPermissions: capStubPermissions{byPerm: map[accesstypes.Permission]accesstypes.Decisions{accesstypes.Execute: {"RetireKeeper": accesstypes.Granted()}}},
		unentered:          []accesstypes.Domain{"anvil", "bastion"},
		asked:              &atomic.Int32{},
	}
	stmt, err := heldStmt[heldView[heldKeepers]](t, SpannerDBType, heldCollection(t, heldViewName, true), perms)
	if err != nil {
		t.Fatalf("QuerySet.stmt() error = %v", err)
	}
	want := "SELECT Id, Name, ARRAY<BOOL>[(" + heldTerm(SpannerDBType, heldViewName, "@_c1") + ")] AS zzCapabilityChecks " +
		"FROM (SELECT Id, Name, Station FROM Keepers WHERE Kind = @viewKind ) AS heldViews WHERE (`heldViews`.`Station` = @domain)"
	if got := normalizeSQL(stmt.SQL); got != want {
		t.Errorf("QuerySet.stmt() SQL =\n%s\nwant\n%s", got, want)
	}
	wantParams := map[string]any{"domain": "testDomain", "viewKind": "keeper", "postKind": "keeper", "_c1": []string{"anvil", "bastion"}}
	if diff := cmp.Diff(wantParams, stmt.Params); diff != "" {
		t.Errorf("QuerySet.stmt() params mismatch (-want +got):\n%s", diff)
	}
}

// TestQuerySet_stmt_callerEntersPairs pins the term of a row named by two columns, on
// both databases: an equality for each pair of Match, in the order of the query columns'
// names, then the tenant test, all joined with AND.
func TestQuerySet_stmt_callerEntersPairs(t *testing.T) {
	t.Parallel()

	want := map[DBType]string{
		SpannerDBType:  "NOT EXISTS (SELECT 1 FROM (" + heldPairsQuery + " ) AS `ca1` WHERE `ca1`.`KeeperName` = `heldResources`.`Name` AND `ca1`.`RowKey` = `heldResources`.`Id` AND `ca1`.`Tenant` IN UNNEST(@_c1))",
		PostgresDBType: `NOT EXISTS (SELECT 1 FROM (` + heldPairsQuery + ` ) AS "ca1" WHERE "ca1"."KeeperName" = "heldResources"."Name" AND "ca1"."RowKey" = "heldResources"."Id" AND "ca1"."Tenant" = ANY(@_c1))`,
	}
	for _, dbType := range []DBType{SpannerDBType, PostgresDBType} {
		t.Run(string(dbType), func(t *testing.T) {
			t.Parallel()

			perms := heldPermissions{
				capStubPermissions: capStubPermissions{byPerm: map[accesstypes.Permission]accesstypes.Decisions{accesstypes.Execute: {"RetireKeeper": accesstypes.Granted()}}},
				unentered:          []accesstypes.Domain{"bastion"},
				asked:              &atomic.Int32{},
			}
			stmt, err := heldStmt[heldResource[heldPairs]](t, dbType, heldCollection(t, heldResourceName, true), perms)
			if err != nil {
				t.Fatalf("QuerySet.stmt() error = %v", err)
			}
			if got, want := normalizeSQL(stmt.SQL), heldSelect(dbType, "("+want[dbType]+")"); got != want {
				t.Errorf("QuerySet.stmt() SQL =\n%s\nwant\n%s", got, want)
			}
		})
	}
}

// TestQuerySet_stmt_callerEntersRefusals pins the refusals of a declaration the
// statement cannot render, each naming the resource, and of a marked method on a row
// whose struct declares no TenantsToEnter.
func TestQuerySet_stmt_callerEntersRefusals(t *testing.T) {
	t.Parallel()

	held := func() heldPermissions {
		return heldPermissions{
			capStubPermissions: capStubPermissions{byPerm: map[accesstypes.Permission]accesstypes.Decisions{accesstypes.Execute: {"RetireKeeper": accesstypes.Granted()}}},
			unentered:          []accesstypes.Domain{"bastion"},
			asked:              &atomic.Int32{},
		}
	}
	tests := []struct {
		name    string
		stmt    func(t *testing.T) error
		wantErr string
	}{
		{
			name: "an empty query",
			stmt: func(t *testing.T) error {
				_, err := heldStmt[heldResource[heldEmptyQuery]](t, SpannerDBType, heldCollection(t, heldResourceName, true), held())

				return err
			},
			wantErr: "the TenantsToEnter query of heldResources is empty",
		},
		{
			name: "an empty Match",
			stmt: func(t *testing.T) error {
				_, err := heldStmt[heldResource[heldEmptyColumn]](t, SpannerDBType, heldCollection(t, heldResourceName, true), held())

				return err
			},
			wantErr: "the TenantsToEnter of heldResources matches no column of its row: Match pairs each key column of the query with the row's column it is matched to",
		},
		{
			name: "a pair in Match with an empty name",
			stmt: func(t *testing.T) error {
				_, err := heldStmt[heldResource[heldEmptyName]](t, SpannerDBType, heldCollection(t, heldResourceName, true), held())

				return err
			},
			wantErr: "the TenantsToEnter of heldResources has a pair in Match with an empty name: each pair names a key column of the query and a column of the row",
		},
		{
			name: "a query that starts with WITH",
			stmt: func(t *testing.T) error {
				_, err := heldStmt[heldResource[heldWith]](t, SpannerDBType, heldCollection(t, heldResourceName, true), held())

				return err
			},
			wantErr: "the TenantsToEnter query of heldResources starts with WITH; write it with no WITH, as a SELECT or a UNION of SELECTs",
		},
		{
			name: "a parameter in the statement's own namespace",
			stmt: func(t *testing.T) error {
				_, err := heldStmt[heldResource[heldUnderscore]](t, SpannerDBType, heldCollection(t, heldResourceName, true), held())

				return err
			},
			wantErr: "TenantsToEnter params for heldResources can not start with an _",
		},
		{
			name: "a parameter the statement binds",
			stmt: func(t *testing.T) error {
				_, err := heldStmt[heldResource[heldCollision]](t, PostgresDBType, heldCollection(t, heldResourceName, true), held())

				return err
			},
			wantErr: `named parameter collision: the TenantsToEnter query of heldResources and the statement both contain named parameter "domain"`,
		},
		{
			name: "a parameter the view's query binds",
			stmt: func(t *testing.T) error {
				_, err := heldStmt[heldView[heldViewParam]](t, SpannerDBType, heldCollection(t, heldViewName, true), held())

				return err
			},
			wantErr: `named parameter collision: the TenantsToEnter query of heldViews and the statement both contain named parameter "viewKind"`,
		},
		{
			name: "an empty query, for a session that enters every tenant",
			stmt: func(t *testing.T) error {
				perms := held()
				perms.unentered = nil
				_, err := heldStmt[heldResource[heldEmptyQuery]](t, SpannerDBType, heldCollection(t, heldResourceName, true), perms)

				return err
			},
			wantErr: "the TenantsToEnter query of heldResources is empty",
		},
		{
			name: "a marked method on a row whose struct declares no TenantsToEnter",
			stmt: func(t *testing.T) error {
				_, err := heldRead[enforcementResource](t, SpannerDBType, heldCollection(t, enforcedResource, true), held(), true)

				return err
			},
			wantErr: "method RenameKeeper is offered on a row of enforcementResources only to a caller who enters the row's tenants, and enforcementResources declares no TenantsToEnter; regenerate",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.stmt(t)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

// TestQuerySet_callerEntersAsks pins what a request pays: the tenants the session does
// not enter are asked once for a read with capabilities=Execute of a row one of whose
// methods is marked, however many are and however often the read is checked (the list
// handler counts, then reads the page), and never for a read that asks for no
// capabilities or of a row none of whose methods is marked.
func TestQuerySet_callerEntersAsks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		marked  bool
		execute bool
		want    int32
	}{
		{name: "capabilities=Execute of a row with three marked methods, checked twice, asks once", marked: true, execute: true, want: 1},
		{name: "a read that asks for no capabilities does not ask", marked: true},
		{name: "a row none of whose methods is marked does not ask", execute: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			grants := accesstypes.Decisions{"GreetKeeper": accesstypes.Granted(), "RenameKeeper": accesstypes.Granted(), "RetireKeeper": accesstypes.Granted(), "SealKeeper": accesstypes.Granted()}
			perms := heldPermissions{
				capStubPermissions: capStubPermissions{byPerm: map[accesstypes.Permission]accesstypes.Decisions{accesstypes.Execute: grants}},
				unentered:          []accesstypes.Domain{"bastion"},
				asked:              &atomic.Int32{},
			}
			q, err := heldRead[heldResource[heldKeepers]](t, SpannerDBType, heldCollection(t, heldResourceName, tt.marked), perms, tt.execute)
			if err != nil {
				t.Fatalf("QuerySet.checkPermissions() error = %v", err)
			}
			if err := q.checkPermissions(t.Context(), SpannerDBType); err != nil {
				t.Fatalf("QuerySet.checkPermissions() again: error = %v", err)
			}
			if _, err := q.stmt(SpannerDBType); err != nil {
				t.Fatalf("QuerySet.stmt() error = %v", err)
			}
			if got := perms.asked.Load(); got != tt.want {
				t.Errorf("the tenants not entered were asked %d times, want %d", got, tt.want)
			}
		})
	}
}
