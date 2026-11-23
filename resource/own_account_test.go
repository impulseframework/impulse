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
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// The accounts of the own-account fixtures: the session's, the real person's behind a
// session that views as someone else, and a third person's.
const (
	ownSessionAccount = "11111111-1111-4111-8111-111111111111"
	ownActorAccount   = "22222222-2222-4222-8222-222222222222"
	ownOtherAccount   = "33333333-3333-4333-8333-333333333333"
)

// ownCollection registers res as heldCollection does, with five methods that name its
// rows: DeleteKeeper and DisableKeeper kept off the row of the caller's own account by
// its Id, ReassignKeeper by its Owner, RetireKeeper by its Id and offered only to a
// caller whose session enters the row's tenants (@callerEnters), and GreetKeeper with
// no mark.
func ownCollection(t *testing.T, res accesstypes.Resource) *GeneratedCollection {
	t.Helper()

	execute := []accesstypes.Permission{accesstypes.Execute}
	g, err := NewGeneratedCollection(CollectionData{Resources: []CollectionResource{
		heldRowResource(res),
		{Name: "DeleteKeeper", Scope: accesstypes.DomainPermissionScope, Permissions: execute, Target: res, NotOnOwnAccount: "Id"},
		{Name: "DisableKeeper", Scope: accesstypes.DomainPermissionScope, Permissions: execute, Target: res, NotOnOwnAccount: "Id"},
		{Name: "GreetKeeper", Scope: accesstypes.DomainPermissionScope, Permissions: execute, Target: res},
		{Name: "ReassignKeeper", Scope: accesstypes.DomainPermissionScope, Permissions: execute, Target: res, NotOnOwnAccount: "Owner"},
		{Name: "RetireKeeper", Scope: accesstypes.DomainPermissionScope, Permissions: execute, Target: res, CallerEnters: true, NotOnOwnAccount: "Id"},
	}})
	if err != nil {
		t.Fatalf("NewGeneratedCollection() error = %v", err)
	}

	return g
}

// ownSession is a context whose session runs as user, an account's id or "" for a
// session with no account, and, where actor is not "", views as user on behalf of the
// real person whose account actor is.
func ownSession(t *testing.T, user, actor string) context.Context {
	t.Helper()

	sess := &sessioninfo.SessionData{SessionInfo: &sessioninfo.SessionInfo{Username: "keeper"}}
	if user != "" {
		id, err := impulse.NullUUIDFromString(user)
		if err != nil {
			t.Fatalf("NullUUIDFromString(%q) error = %v", user, err)
		}
		sess.UserID = id
	}
	if actor != "" {
		id, err := impulse.NullUUIDFromString(actor)
		if err != nil {
			t.Fatalf("NullUUIDFromString(%q) error = %v", actor, err)
		}
		sess.Impersonation = &sessioninfo.Impersonation{Actor: "warden", ActorID: id}
	}

	return context.WithValue(t.Context(), sessioninfo.CtxSessionInfo, sess)
}

// ownStmt renders the statement of an Execute capability read of R in ctx, as heldStmt
// does in a context with no session.
func ownStmt[R Resourcer](t *testing.T, ctx context.Context, dbType DBType, collection *GeneratedCollection, perms UserPermissions) (*Statement, error) {
	t.Helper()

	rSet, err := NewSet[R, heldReadRequest](accesstypes.Read)
	if err != nil {
		t.Fatalf("NewSet() error = %v", err)
	}
	q := NewQuerySet(NewMetadata[R]())
	q.env = accesstypes.EnvironmentAt(time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC))
	q.jsonNames = map[accesstypes.Field]string{"ID": "id", "Name": "name"}
	q.collection = collection
	q.EnableUserPermissionEnforcement(rSet, perms, testScope, accesstypes.Read)
	for _, field := range []accesstypes.Field{"ID", "Name"} {
		q.AddField(field)
	}
	q.RequestCapabilities(accesstypes.Execute)
	if err := q.checkPermissions(ctx, dbType); err != nil {
		return nil, err
	}

	return q.stmt(dbType)
}

// ownTerm is the own-account term as it reads in a normalized statement, over the
// column of heldResources and with the accounts bound under placeholder.
func ownTerm(dbType DBType, column, placeholder string) string {
	if dbType == PostgresDBType {
		ref := `"heldResources"."` + column + `"`

		return "(" + ref + " IS NULL OR " + ref + "::text <> ALL(" + placeholder + "))"
	}
	ref := "`heldResources`.`" + column + "`"

	return "(" + ref + " IS NULL OR CAST(" + ref + " AS STRING) NOT IN UNNEST(" + placeholder + "))"
}

// TestQuerySet_stmt_notOnOwnAccount pins the Execute affordance of a method a row keeps
// off the caller's own account (@notOnOwnAccount), on both databases: the own-account
// term is a boolean of its own, one a column, the accounts the session's own and the
// real person's behind a session that views as someone else, bound once; a marked
// method under a condition is listed where the condition's boolean and the own-account
// boolean both hold, and shares the condition's boolean with an unmarked method under
// the same condition; the entering term is a boolean of its own beside it; a method
// without the mark is listed as before.
func TestQuerySet_stmt_notOnOwnAccount(t *testing.T) {
	t.Parallel()

	bastion := []accesstypes.Domain{"bastion"}
	owner := map[DBType]string{SpannerDBType: "`heldResources`.`Owner` = @subject", PostgresDBType: `"heldResources"."Owner" = @subject`}

	tests := []struct {
		name        string
		user, actor string
		decisions   accesstypes.Decisions
		unentered   []accesstypes.Domain
		// checks renders the statement's checks on dbType.
		checks func(DBType) string
		// rows are the booleans of a row, each with the methods it lists.
		rows []heldRow
		// wantParams are the statement's parameters beyond domain.
		wantParams map[string]any
	}{
		{
			name:      "the row of the session's account leaves the marked method out, and a method without the mark is listed on every row",
			user:      ownSessionAccount,
			decisions: accesstypes.Decisions{"DisableKeeper": accesstypes.Granted(), "GreetKeeper": accesstypes.Granted()},
			checks: func(db DBType) string {
				return "(" + ownTerm(db, "Id", "@_c1") + ")"
			},
			rows: []heldRow{
				{checks: []bool{true}, methods: []string{"DisableKeeper", "GreetKeeper"}},
				{checks: []bool{false}, methods: []string{"GreetKeeper"}},
			},
			wantParams: map[string]any{"_c1": []string{ownSessionAccount}},
		},
		{
			name:      "a session that views as someone else keeps it off the row of the person viewed as and off the real person's",
			user:      ownSessionAccount,
			actor:     ownActorAccount,
			decisions: accesstypes.Decisions{"DisableKeeper": accesstypes.Granted()},
			checks: func(db DBType) string {
				return "(" + ownTerm(db, "Id", "@_c1") + ")"
			},
			rows: []heldRow{
				{checks: []bool{true}, methods: []string{"DisableKeeper"}},
				{checks: []bool{false}, methods: []string{}},
			},
			wantParams: map[string]any{"_c1": []string{ownSessionAccount, ownActorAccount}},
		},
		{
			name:      "a session that acts as a role keeps it off the row of the real person behind it",
			actor:     ownActorAccount,
			decisions: accesstypes.Decisions{"DisableKeeper": accesstypes.Granted()},
			checks: func(db DBType) string {
				return "(" + ownTerm(db, "Id", "@_c1") + ")"
			},
			rows:       []heldRow{{checks: []bool{true}, methods: []string{"DisableKeeper"}}},
			wantParams: map[string]any{"_c1": []string{ownActorAccount}},
		},
		{
			name:      "a session that views as the real person's own account names it once",
			user:      ownActorAccount,
			actor:     ownActorAccount,
			decisions: accesstypes.Decisions{"DisableKeeper": accesstypes.Granted()},
			checks: func(db DBType) string {
				return "(" + ownTerm(db, "Id", "@_c1") + ")"
			},
			rows:       []heldRow{{checks: []bool{true}, methods: []string{"DisableKeeper"}}},
			wantParams: map[string]any{"_c1": []string{ownActorAccount}},
		},
		{
			name:      "a conditional grant takes its condition's boolean and the term's, and lists the method where both hold",
			user:      ownSessionAccount,
			decisions: accesstypes.Decisions{"DisableKeeper": conditionalOn("DisableKeeper", "owner = subject")},
			checks: func(db DBType) string {
				return "(" + owner[db] + "), (" + ownTerm(db, "Id", "@_c1") + ")"
			},
			rows: []heldRow{
				{checks: []bool{true, true}, methods: []string{"DisableKeeper"}},
				{checks: []bool{false, true}, methods: []string{}},
				{checks: []bool{true, false}, methods: []string{}},
			},
			wantParams: map[string]any{"_c1": []string{ownSessionAccount}, "subject": "u1"},
		},
		{
			name:      "a marked method and an unmarked one under the same condition share the condition's boolean",
			user:      ownSessionAccount,
			decisions: accesstypes.Decisions{"DisableKeeper": conditionalOn("DisableKeeper", "owner = subject"), "GreetKeeper": conditionalOn("GreetKeeper", "owner = subject")},
			checks: func(db DBType) string {
				return "(" + owner[db] + "), (" + ownTerm(db, "Id", "@_c1") + ")"
			},
			rows: []heldRow{
				{checks: []bool{true, true}, methods: []string{"DisableKeeper", "GreetKeeper"}},
				{checks: []bool{true, false}, methods: []string{"GreetKeeper"}},
				{checks: []bool{false, true}, methods: []string{}},
			},
			wantParams: map[string]any{"_c1": []string{ownSessionAccount}, "subject": "u1"},
		},
		{
			name: "two marked methods with one column share one boolean, and one with another column takes its own",
			user: ownSessionAccount,
			decisions: accesstypes.Decisions{
				"DeleteKeeper": accesstypes.Granted(), "DisableKeeper": accesstypes.Granted(), "ReassignKeeper": accesstypes.Granted(),
			},
			checks: func(db DBType) string {
				return "(" + ownTerm(db, "Id", "@_c1") + "), (" + ownTerm(db, "Owner", "@_c1") + ")"
			},
			rows: []heldRow{
				{checks: []bool{true, true}, methods: []string{"DeleteKeeper", "DisableKeeper", "ReassignKeeper"}},
				{checks: []bool{false, true}, methods: []string{"ReassignKeeper"}},
				{checks: []bool{true, false}, methods: []string{"DeleteKeeper", "DisableKeeper"}},
			},
			wantParams: map[string]any{"_c1": []string{ownSessionAccount}},
		},
		{
			name:      "a method with @callerEnters and the mark, in a session that does not enter a tenant, takes the entering term's boolean and the own-account term's",
			user:      ownSessionAccount,
			decisions: accesstypes.Decisions{"RetireKeeper": accesstypes.Granted()},
			unentered: bastion,
			checks: func(db DBType) string {
				return "(" + heldTerm(db, heldResourceName, "@_c1") + "), (" + ownTerm(db, "Id", "@_c2") + ")"
			},
			rows: []heldRow{
				{checks: []bool{true, true}, methods: []string{"RetireKeeper"}},
				{checks: []bool{false, true}, methods: []string{}},
				{checks: []bool{true, false}, methods: []string{}},
			},
			wantParams: map[string]any{"_c1": []string{"bastion"}, "_c2": []string{ownSessionAccount}, "postKind": "keeper"},
		},
		{
			name:      "the marked methods with @callerEnters and without it share the own-account boolean, and the one with @callerEnters needs the entering boolean too",
			user:      ownSessionAccount,
			decisions: accesstypes.Decisions{"DisableKeeper": accesstypes.Granted(), "RetireKeeper": accesstypes.Granted()},
			unentered: bastion,
			checks: func(db DBType) string {
				return "(" + ownTerm(db, "Id", "@_c1") + "), (" + heldTerm(db, heldResourceName, "@_c2") + ")"
			},
			rows: []heldRow{
				{checks: []bool{true, true}, methods: []string{"DisableKeeper", "RetireKeeper"}},
				{checks: []bool{true, false}, methods: []string{"DisableKeeper"}},
				{checks: []bool{false, true}, methods: []string{}},
				{checks: []bool{false, false}, methods: []string{}},
			},
			wantParams: map[string]any{"_c1": []string{ownSessionAccount}, "_c2": []string{"bastion"}, "postKind": "keeper"},
		},
		{
			name:      "a method with @callerEnters and the mark, in a session that enters every tenant, takes the own-account term alone",
			user:      ownSessionAccount,
			decisions: accesstypes.Decisions{"RetireKeeper": accesstypes.Granted()},
			checks: func(db DBType) string {
				return "(" + ownTerm(db, "Id", "@_c1") + ")"
			},
			rows:       []heldRow{{checks: []bool{false}, methods: []string{}}},
			wantParams: map[string]any{"_c1": []string{ownSessionAccount}},
		},
	}
	for _, tt := range tests {
		for _, dbType := range []DBType{SpannerDBType, PostgresDBType} {
			t.Run(tt.name+"/"+string(dbType), func(t *testing.T) {
				t.Parallel()

				perms := heldPermissions{capStubPermissions: capStubPermissions{byPerm: map[accesstypes.Permission]accesstypes.Decisions{accesstypes.Execute: tt.decisions}}, unentered: tt.unentered, asked: &atomic.Int32{}}
				stmt, err := ownStmt[heldResource[heldKeepers]](t, ownSession(t, tt.user, tt.actor), dbType, ownCollection(t, heldResourceName), perms)
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
			})
		}
	}
}

// TestQuerySet_stmt_callerEntersReadOnce pins, on both databases, that a statement whose
// methods are held to the tenants the caller's session enters (@callerEnters) under
// two different conditions, beside a third held and kept off the caller's own account
// (@notOnOwnAccount), reads those tenants once: the TenantsToEnter query is in the
// statement once, as the one entering boolean, beside a boolean for each condition and
// the own-account boolean; and each method is listed where its condition's boolean and
// each of its marks' hold.
func TestQuerySet_stmt_callerEntersReadOnce(t *testing.T) {
	t.Parallel()

	execute := []accesstypes.Permission{accesstypes.Execute}
	owner := map[DBType]string{SpannerDBType: "`heldResources`.`Owner` = @subject", PostgresDBType: `"heldResources"."Owner" = @subject`}
	state := map[DBType]string{SpannerDBType: "`heldResources`.`State` IN (@_c3)", PostgresDBType: `"heldResources"."State" IN (@_c3)`}
	decisions := accesstypes.Decisions{
		"RenameKeeper": conditionalOn("RenameKeeper", "owner = subject"),
		"RetireKeeper": accesstypes.Granted(),
		"SealKeeper":   accesstypes.Granted(),
	}
	rows := []heldRow{
		{checks: []bool{true, true, true, true}, methods: []string{"RenameKeeper", "RetireKeeper", "SealKeeper"}},
		{checks: []bool{true, false, true, true}, methods: []string{}},
		{checks: []bool{true, true, false, true}, methods: []string{"RenameKeeper", "SealKeeper"}},
		{checks: []bool{false, true, true, false}, methods: []string{"RetireKeeper"}},
		{checks: []bool{false, true, false, true}, methods: []string{"SealKeeper"}},
	}

	for _, dbType := range []DBType{SpannerDBType, PostgresDBType} {
		t.Run(string(dbType), func(t *testing.T) {
			t.Parallel()

			collection, err := NewGeneratedCollection(CollectionData{Resources: []CollectionResource{
				heldRowResource(heldResourceName),
				{Name: "RenameKeeper", Scope: accesstypes.DomainPermissionScope, Permissions: execute, Target: heldResourceName, CallerEnters: true},
				{Name: "RetireKeeper", Scope: accesstypes.DomainPermissionScope, Permissions: execute, Target: heldResourceName, CallerEnters: true, NotOnOwnAccount: "Id"},
				{
					Name: "SealKeeper", Scope: accesstypes.DomainPermissionScope, Permissions: execute,
					Transition: &TransitionData{Target: heldResourceName, From: []string{"open"}, To: "sealed"}, CallerEnters: true,
				},
			}})
			if err != nil {
				t.Fatalf("NewGeneratedCollection() error = %v", err)
			}
			perms := heldPermissions{capStubPermissions: capStubPermissions{byPerm: map[accesstypes.Permission]accesstypes.Decisions{accesstypes.Execute: decisions}}, unentered: []accesstypes.Domain{"bastion"}, asked: &atomic.Int32{}}
			stmt, err := ownStmt[heldResource[heldKeepers]](t, ownSession(t, ownSessionAccount, ""), dbType, collection, perms)
			if err != nil {
				t.Fatalf("QuerySet.stmt() error = %v", err)
			}
			got := normalizeSQL(stmt.SQL)
			if n := strings.Count(got, heldQuery); n != 1 {
				t.Errorf("QuerySet.stmt() SQL holds the TenantsToEnter query %d times, want once:\n%s", n, got)
			}
			checks := "(" + owner[dbType] + "), (" + heldTerm(dbType, heldResourceName, "@_c1") + "), (" + ownTerm(dbType, "Id", "@_c2") + "), (" + state[dbType] + ")"
			if want := heldSelect(dbType, checks); got != want {
				t.Errorf("QuerySet.stmt() SQL =\n%s\nwant\n%s", got, want)
			}
			wantParams := map[string]any{"domain": "testDomain", "_c1": []string{"bastion"}, "_c2": []string{ownSessionAccount}, "_c3": "open", "postKind": "keeper", "subject": "u1"}
			if diff := cmp.Diff(wantParams, stmt.Params); diff != "" {
				t.Errorf("QuerySet.stmt() params mismatch (-want +got):\n%s", diff)
			}
			for _, row := range rows {
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

// TestQuerySet_stmt_notOnOwnAccountNoAccount pins that a request with no session, and a
// session with no account, read the statement of a collection without the mark, byte
// for byte, with no ownAccounts parameter, on both databases: every row lists the
// method.
func TestQuerySet_stmt_notOnOwnAccountNoAccount(t *testing.T) {
	t.Parallel()

	decisions := accesstypes.Decisions{"DisableKeeper": accesstypes.Granted(), "GreetKeeper": accesstypes.Granted(), "ReassignKeeper": accesstypes.Granted()}
	tests := []struct {
		name string
		ctx  func(t *testing.T) context.Context
	}{
		{
			name: "no session",
			ctx: func(t *testing.T) context.Context {
				return t.Context()
			},
		},
		{
			name: "a session with no account",
			ctx: func(t *testing.T) context.Context {
				return ownSession(t, "", "")
			},
		},
	}
	for _, tt := range tests {
		for _, dbType := range []DBType{SpannerDBType, PostgresDBType} {
			t.Run(tt.name+"/"+string(dbType), func(t *testing.T) {
				t.Parallel()

				perms := heldPermissions{capStubPermissions: capStubPermissions{byPerm: map[accesstypes.Permission]accesstypes.Decisions{accesstypes.Execute: decisions}}, asked: &atomic.Int32{}}
				stmt, err := ownStmt[heldResource[heldKeepers]](t, tt.ctx(t), dbType, ownCollection(t, heldResourceName), perms)
				if err != nil {
					t.Fatalf("QuerySet.stmt() error = %v", err)
				}
				if got, want := normalizeSQL(stmt.SQL), heldSelect(dbType, ""); got != want {
					t.Errorf("QuerySet.stmt() SQL =\n%s\nwant\n%s", got, want)
				}
				if diff := cmp.Diff(map[string]any{"domain": "testDomain"}, stmt.Params); diff != "" {
					t.Errorf("QuerySet.stmt() params mismatch (-want +got):\n%s", diff)
				}
				want := map[accesstypes.Permission]any{accesstypes.Execute: []string{"DisableKeeper", "GreetKeeper", "ReassignKeeper"}}
				if diff := cmp.Diff(want, stmt.capabilityPlan.assemble(nil)); diff != "" {
					t.Errorf("capabilityPlan.assemble() mismatch (-want +got):\n%s", diff)
				}
			})
		}
	}
}

// TestGeneratedCollection_methodNotOnOwnAccount pins the @notOnOwnAccount carriage: the
// column the builder records for a method, the second call's, comes back from
// MethodsTargeting as OwnAccountColumn through Data() and a load of that data and in the
// builder's own state, beside a method on the same row with none; a method offered on
// rows carries none; and a load refuses the mark on a method that names no row.
func TestGeneratedCollection_methodNotOnOwnAccount(t *testing.T) {
	t.Parallel()

	b := NewCollectionBuilder()
	if err := b.AddResource(accesstypes.DomainPermissionScope, accesstypes.Read, "Keepers"); err != nil {
		t.Fatalf("AddResource(Keepers) error = %v", err)
	}
	for _, method := range []accesstypes.Resource{"DisableKeeper", "GreetKeeper", "SealKeeper", "SweepKeepers"} {
		if err := b.AddMethodResource(accesstypes.DomainPermissionScope, accesstypes.Execute, method); err != nil {
			t.Fatalf("AddMethodResource(%s) error = %v", method, err)
		}
	}
	b.SetMethodTarget(accesstypes.DomainPermissionScope, "DisableKeeper", "Keepers")
	b.SetMethodTarget(accesstypes.DomainPermissionScope, "GreetKeeper", "Keepers")
	b.SetMethodTransition(accesstypes.DomainPermissionScope, "SealKeeper", TransitionData{Target: "Keepers", From: []string{"open"}, To: "sealed"})
	b.SetMethodOfferedOn(accesstypes.DomainPermissionScope, "SweepKeepers", []accesstypes.Resource{"Keepers"})
	b.SetMethodNotOnOwnAccount(accesstypes.DomainPermissionScope, "DisableKeeper", "Owner")
	b.SetMethodNotOnOwnAccount(accesstypes.DomainPermissionScope, "DisableKeeper", "Id")
	b.SetMethodNotOnOwnAccount(accesstypes.DomainPermissionScope, "SealKeeper", "Id")

	want := map[accesstypes.Resource]string{"DisableKeeper": "Id", "GreetKeeper": "", "SealKeeper": "Id"}
	columns := func(g *GeneratedCollection) map[accesstypes.Resource]string {
		got := map[accesstypes.Resource]string{}
		for _, tm := range g.MethodsTargeting("Keepers") {
			got[tm.Method] = tm.OwnAccountColumn
		}

		return got
	}

	data := b.Data()
	for _, res := range data.Resources {
		if got, want := res.NotOnOwnAccount, want[res.Name]; got != want {
			t.Errorf("Data() NotOnOwnAccount of %s = %q, want %q", res.Name, got, want)
		}
	}
	loaded, err := NewGeneratedCollection(data)
	if err != nil {
		t.Fatalf("NewGeneratedCollection(Data()) error = %v", err)
	}
	if diff := cmp.Diff(want, columns(loaded)); diff != "" {
		t.Errorf("MethodsTargeting(Keepers) OwnAccountColumn after a load (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(want, columns(b.g)); diff != "" {
		t.Errorf("MethodsTargeting(Keepers) OwnAccountColumn on the builder's own state (-want +got):\n%s", diff)
	}
	for _, tm := range executeCandidates(loaded, "Keepers") {
		if tm.Method == "SweepKeepers" && tm.OwnAccountColumn != "" {
			t.Errorf("executeCandidates(Keepers) gives SweepKeepers, which names no row, the column %q", tm.OwnAccountColumn)
		}
	}

	_, err = NewGeneratedCollection(CollectionData{Resources: []CollectionResource{{
		Name:            "PruneKeepers",
		Scope:           accesstypes.DomainPermissionScope,
		Permissions:     []accesstypes.Permission{accesstypes.Execute},
		NotOnOwnAccount: "Id",
	}}})
	wantErr := `method resource "PruneKeepers" is kept off the row of the caller's own account but names no target row`
	if err == nil || !strings.Contains(err.Error(), wantErr) {
		t.Errorf("NewGeneratedCollection() of a marked method with no target error = %v, want %q", err, wantErr)
	}
}
