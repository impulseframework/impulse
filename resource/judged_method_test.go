package resource

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

// sendAgainRequest is the request of SendThingAgain: the row it names, and no field a
// condition reads.
type sendAgainRequest struct {
	ID impulse.UUID
}

// TestCaller_ExecuteGate_Judged pins the request's own check of a method that another
// method's grant judges: the engine is asked about the judging method alone, its grant
// admits or refuses the call, and a condition on it that reads the judging method's
// request is carried to the gate as a condition on the row the judged method names. A
// grant on the judged method itself admits nobody.
func TestCaller_ExecuteGate_Judged(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		perms       *fakeUserPermissions
		wantRefused bool
		wantErr     string
		wantGranted bool
		wantSettled bool
		// wantCond is the condition the gate carries to the row, where it is not settled.
		wantCond string
	}{
		{
			name:        "the judging method's grant with no condition",
			perms:       &fakeUserPermissions{granted: map[accesstypes.Permission][]accesstypes.Resource{accesstypes.Execute: {"RateThing"}}},
			wantGranted: true,
			wantSettled: true,
		},
		{
			name:        "no grant",
			perms:       &fakeUserPermissions{},
			wantRefused: true,
		},
		{
			name:     "the judging method's grant under a condition on its request",
			perms:    &fakeUserPermissions{conditions: map[accesstypes.Resource]string{"RateThing": "new.role != 'Chief'"}},
			wantCond: "role != 'Chief'",
		},
		{
			name:        "a grant on the judged method itself and none on the judging method",
			perms:       &fakeUserPermissions{granted: map[accesstypes.Permission][]accesstypes.Resource{accesstypes.Execute: {"SendThingAgain"}}},
			wantRefused: true,
		},
		{
			name:    "the judging method's grant compares its request to something other than a literal",
			perms:   &fakeUserPermissions{conditions: map[accesstypes.Resource]string{"RateThing": "new.role IN subject.ownRoles"}},
			wantErr: "method SendThingAgain is judged by the grant of RateThing, whose condition compares a request field to something other than a literal",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			caller := &Caller{Permissions: tt.perms, Scope: testScope, Env: RequestEnvironment(), collection: judgedMethodCollection(t, heldResourceName)}
			gate, err := caller.ExecuteGate(t.Context(), "SendThingAgain", &sendAgainRequest{})
			if diff := cmp.Diff([][]accesstypes.Resource{{"RateThing"}}, tt.perms.gotResources); diff != "" {
				t.Errorf("the engine was asked about (-want +got):\n%s", diff)
			}
			switch {
			case tt.wantRefused:
				if err == nil || !httpio.HasForbidden(err) {
					t.Fatalf("Caller.ExecuteGate() error = %v, want Forbidden", err)
				}

				return
			case tt.wantErr != "":
				if err == nil || httpio.HasForbidden(err) || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Caller.ExecuteGate() error = %v, want an error containing %q that is no refusal", err, tt.wantErr)
				}

				return
			case err != nil:
				t.Fatalf("Caller.ExecuteGate() error = %v", err)
			}
			granted, settled := gate.Settled()
			if granted != tt.wantGranted || settled != tt.wantSettled {
				t.Errorf("ExecuteGate.Settled() = (granted %v, settled %v), want (%v, %v)", granted, settled, tt.wantGranted, tt.wantSettled)
			}
			if gate.method != "SendThingAgain" {
				t.Errorf("the gate is of the method %s, want SendThingAgain", gate.method)
			}
			if tt.wantCond != "" && (gate.cond == nil || gate.cond.String() != tt.wantCond) {
				t.Errorf("the gate's condition = %v, want %s", gate.cond, tt.wantCond)
			}
		})
	}
}

// TestQuerySet_stmt_Judged pins the row's Execute answer of a method that another
// method's grant judges, on both databases: the judging method's grant with no
// condition lists the method on every row; a condition on it that reads the judging
// method's request reads the row's attribute of the same name, as a boolean of the
// statement, and lists the method where it holds; with no grant on the judging method
// the method is on no row, whatever is granted on the method itself; and a method of
// the same row that its own grant judges is answered as before.
func TestQuerySet_stmt_Judged(t *testing.T) {
	t.Parallel()

	role := map[DBType]string{SpannerDBType: "`heldResources`.`Role` <> @_c1", PostgresDBType: `"heldResources"."Role" <> @_c1`}
	owner := map[DBType]string{SpannerDBType: "`heldResources`.`Owner` = @subject", PostgresDBType: `"heldResources"."Owner" = @subject`}

	tests := []struct {
		name      string
		decisions accesstypes.Decisions
		// checks renders the statement's checks on dbType; "" for none.
		checks func(DBType) string
		// rows are the booleans of a row, each with the methods it lists.
		rows []heldRow
		// wantParams are the statement's parameters beyond domain.
		wantParams map[string]any
	}{
		{
			name:      "the judging method's grant with no condition lists the method on every row, with no check",
			decisions: accesstypes.Decisions{"RateThing": accesstypes.Granted(), "GreetKeeper": accesstypes.Granted()},
			checks:    func(DBType) string { return "" },
			rows:      []heldRow{{methods: []string{"GreetKeeper", "SendThingAgain"}}},
		},
		{
			name:      "a condition on the judging method's request reads the row's attribute",
			decisions: accesstypes.Decisions{"RateThing": conditionalOn("RateThing", "new.role != 'Chief'"), "GreetKeeper": accesstypes.Granted()},
			checks: func(db DBType) string {
				return "(" + role[db] + ")"
			},
			rows: []heldRow{
				{checks: []bool{true}, methods: []string{"GreetKeeper", "SendThingAgain"}},
				{checks: []bool{false}, methods: []string{"GreetKeeper"}},
			},
			wantParams: map[string]any{"_c1": "Chief"},
		},
		{
			name:      "with no grant on the judging method the method is on no row",
			decisions: accesstypes.Decisions{"GreetKeeper": accesstypes.Granted()},
			checks:    func(DBType) string { return "" },
			rows:      []heldRow{{methods: []string{"GreetKeeper"}}},
		},
		{
			name:      "a grant on the judged method itself lists it on no row",
			decisions: accesstypes.Decisions{"SendThingAgain": accesstypes.Granted()},
			checks:    func(DBType) string { return "" },
			rows:      []heldRow{{methods: []string{}}},
		},
		{
			name: "a method its own grant judges keeps its own condition beside the judged one's",
			decisions: accesstypes.Decisions{
				"RateThing":   conditionalOn("RateThing", "new.role != 'Chief'"),
				"GreetKeeper": conditionalOn("GreetKeeper", "owner = subject"),
			},
			checks: func(db DBType) string {
				return "(" + owner[db] + "), (" + role[db] + ")"
			},
			rows: []heldRow{
				{checks: []bool{true, true}, methods: []string{"GreetKeeper", "SendThingAgain"}},
				{checks: []bool{true, false}, methods: []string{"GreetKeeper"}},
				{checks: []bool{false, true}, methods: []string{"SendThingAgain"}},
			},
			wantParams: map[string]any{"_c1": "Chief", "subject": "u1"},
		},
	}
	for _, tt := range tests {
		for _, dbType := range []DBType{SpannerDBType, PostgresDBType} {
			t.Run(tt.name+"/"+string(dbType), func(t *testing.T) {
				t.Parallel()

				asked := map[accesstypes.Permission][]accesstypes.Resource{}
				perms := capStubPermissions{byPerm: map[accesstypes.Permission]accesstypes.Decisions{accesstypes.Execute: tt.decisions}, asked: asked}
				stmt, err := heldStmt[heldResource[heldKeepers]](t, dbType, judgedMethodCollection(t, heldResourceName), perms)
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
				if slices.Contains(asked[accesstypes.Execute], "SendThingAgain") || !slices.Contains(asked[accesstypes.Execute], "RateThing") {
					t.Errorf("the engine was asked about %v for Execute, want RateThing and never SendThingAgain", asked[accesstypes.Execute])
				}
			})
		}
	}
}
