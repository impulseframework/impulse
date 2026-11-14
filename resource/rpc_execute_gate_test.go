package resource

import (
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"google.golang.org/api/iterator"
)

// TestExecuteGate_checkStatement pins the gate's check-SELECT (§12): the
// carried Execute condition lowered against the target resource's bindings as
// one boolean, the row located by its primary key, and the reserved
// parameters bound to the same values the permission check folded with.
func TestExecuteGate_checkStatement(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		source     string
		wantSQL    string
		wantParams map[string]any
	}{
		{
			name:       "subject comparison binds @subject",
			source:     "owner = subject",
			wantSQL:    "SELECT (`enforcementResources`.`Owner` = @subject) AS g0 FROM enforcementResources WHERE Id = @zzTargetKey",
			wantParams: map[string]any{"zzTargetKey": "task-1", "subject": "dana"},
		},
		{
			name:       "state exclusion lowers against the uniform state binding",
			source:     "state NOT IN ('closed', 'canceled')",
			wantSQL:    "SELECT (`enforcementResources`.`State` NOT IN (@_c1, @_c2)) AS g0 FROM enforcementResources WHERE Id = @zzTargetKey",
			wantParams: map[string]any{"zzTargetKey": "task-1", "_c1": "closed", "_c2": "canceled"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gate := &ExecuteGate{
				method:     "NudgeTask",
				user:       "dana",
				scope:      testScope,
				env:        accesstypes.EnvironmentAt(now),
				collection: executeCollection(t),
				cond:       mustCondition(tt.source).Expr(),
			}

			stmt, err := gate.checkStatement(ExecuteTarget{Resource: enforcedResource, Label: "EnforcementResource", PKColumns: []string{"Id"}}, &targetKey{columns: []string{"Id"}, values: []any{"task-1"}})
			if err != nil {
				t.Fatalf("ExecuteGate.checkStatement() error = %v", err)
			}
			if got := normalizeSQL(stmt.SQL); got != tt.wantSQL {
				t.Errorf("ExecuteGate.checkStatement() SQL =\n%s\nwant\n%s", got, tt.wantSQL)
			}
			if diff := cmp.Diff(tt.wantParams, stmt.Params); diff != "" {
				t.Errorf("ExecuteGate.checkStatement() params mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// gateViewResource is the view the gate's view tests target, and gateView the value
// whose Subquery stands for the view's query.
const gateViewResource = accesstypes.Resource("gateViews")

type gateView struct {
	query  string
	params map[string]any
}

func (v gateView) Subquery() (query string, params map[string]any) {
	return v.query, v.params
}

// gateViewCollection is a collection holding the view in the given scope with one
// fact, and its tenant column when it has one.
func gateViewCollection(t *testing.T, scope accesstypes.PermissionScope, domain *DomainBindingData) *GeneratedCollection {
	t.Helper()

	g, err := NewGeneratedCollection(CollectionData{Resources: []CollectionResource{
		{
			Name:        gateViewResource,
			Scope:       scope,
			Permissions: []accesstypes.Permission{accesstypes.List, accesstypes.Read},
			Attributes:  []AttributeData{{Name: "grounded", Column: "Grounded", Type: AttributeTypeBool}},
			Domain:      domain,
		},
		{
			Name:        "NudgeView",
			Scope:       scope,
			Permissions: []accesstypes.Permission{accesstypes.Execute},
			Target:      gateViewResource,
		},
	}})
	if err != nil {
		t.Fatalf("NewGeneratedCollection() error = %v", err)
	}

	return g
}

// TestExecuteGate_checkStatement_view pins the check-SELECT over a row of a view: it
// selects from the view's query under the resource's name, as the view's lists and
// reads do, by the key and, for a tenant-scoped view, the request's tenant, so a key
// that names a row in two tenants is one row in each; a grant with no condition still
// selects, since the row has to be found; and the query's WITH clause and its
// parameters travel with it.
func TestExecuteGate_checkStatement_view(t *testing.T) {
	t.Parallel()

	const query = "SELECT s.Id, s.Station, s.Grounded FROM Ships s"
	tenantColumn := &DomainBindingData{Column: "Station"}

	tests := []struct {
		name        string
		view        gateView
		scope       accesstypes.Scope
		collScope   accesstypes.PermissionScope
		domain      *DomainBindingData
		cond        string
		key         *targetKey // the row's key; the one column Id when nil
		wantSQL     string
		wantParams  map[string]any
		wantErrPart string
	}{
		{
			name:       "a compound key names the row by each of its columns, within the tenant",
			view:       gateView{query: query},
			scope:      testScope,
			collScope:  accesstypes.DomainPermissionScope,
			domain:     tenantColumn,
			cond:       "grounded = false",
			key:        &targetKey{columns: []string{"Id", "Berth"}, values: []any{"row-1", "b-7"}},
			wantSQL:    "SELECT (`gateViews`.`Grounded` = @_c1) AS g0 FROM (SELECT s.Id, s.Station, s.Grounded FROM Ships s ) AS gateViews WHERE Id = @zzTargetKey AND Berth = @zzTargetKey1 AND Station = @domain",
			wantParams: map[string]any{"zzTargetKey": "row-1", "zzTargetKey1": "b-7", "_c1": false, "domain": "testDomain"},
		},
		{
			name:       "a condition on a fact, within the tenant",
			view:       gateView{query: query},
			scope:      testScope,
			collScope:  accesstypes.DomainPermissionScope,
			domain:     tenantColumn,
			cond:       "grounded = false",
			wantSQL:    "SELECT (`gateViews`.`Grounded` = @_c1) AS g0 FROM (SELECT s.Id, s.Station, s.Grounded FROM Ships s ) AS gateViews WHERE Id = @zzTargetKey AND Station = @domain",
			wantParams: map[string]any{"zzTargetKey": "row-1", "_c1": false, "domain": "testDomain"},
		},
		{
			name:       "no condition: the row is still located within the tenant",
			view:       gateView{query: query},
			scope:      testScope,
			collScope:  accesstypes.DomainPermissionScope,
			domain:     tenantColumn,
			wantSQL:    "SELECT (TRUE) AS g0 FROM (SELECT s.Id, s.Station, s.Grounded FROM Ships s ) AS gateViews WHERE Id = @zzTargetKey AND Station = @domain",
			wantParams: map[string]any{"zzTargetKey": "row-1", "domain": "testDomain"},
		},
		{
			name:       "a global view is located by its key alone",
			view:       gateView{query: query},
			scope:      accesstypes.GlobalScope(),
			collScope:  accesstypes.GlobalPermissionScope,
			cond:       "grounded = false",
			wantSQL:    "SELECT (`gateViews`.`Grounded` = @_c1) AS g0 FROM (SELECT s.Id, s.Station, s.Grounded FROM Ships s ) AS gateViews WHERE Id = @zzTargetKey",
			wantParams: map[string]any{"zzTargetKey": "row-1", "_c1": false},
		},
		{
			name: "the query's WITH clause leads the statement and its parameters are bound",
			view: gateView{
				query:  "WITH grounded AS (SELECT ShipId FROM Refits WHERE Kind = @kind) SELECT s.Id, s.Station, s.Id IN (SELECT ShipId FROM grounded) AS Grounded FROM Ships s",
				params: map[string]any{"kind": "overhaul"},
			},
			scope:      testScope,
			collScope:  accesstypes.DomainPermissionScope,
			domain:     tenantColumn,
			cond:       "grounded = false",
			wantSQL:    "WITH grounded AS (SELECT ShipId FROM Refits WHERE Kind = @kind) SELECT (`gateViews`.`Grounded` = @_c1) AS g0 FROM ( SELECT s.Id, s.Station, s.Id IN (SELECT ShipId FROM grounded) AS Grounded FROM Ships s ) AS gateViews WHERE Id = @zzTargetKey AND Station = @domain",
			wantParams: map[string]any{"zzTargetKey": "row-1", "_c1": false, "domain": "testDomain", "kind": "overhaul"},
		},
		{
			name:        "a tenant-scoped view in a global request fails loud",
			view:        gateView{query: query},
			scope:       accesstypes.GlobalScope(),
			collScope:   accesstypes.DomainPermissionScope,
			domain:      tenantColumn,
			wantErrPart: "requires a partitioned request",
		},
		{
			name:        "a tenant-scoped view with no tenant column fails loud",
			view:        gateView{query: query},
			scope:       testScope,
			collScope:   accesstypes.DomainPermissionScope,
			wantErrPart: "is a tenant-scoped view with no domain binding",
		},
		{
			name:        "a view whose tenant is a join path fails loud",
			view:        gateView{query: query},
			scope:       testScope,
			collScope:   accesstypes.DomainPermissionScope,
			domain:      &DomainBindingData{Column: "BerthId", Path: []BindingHop{{Table: "Berths", JoinColumn: "Id", Column: "StationId"}}},
			wantErrPart: "a view's tenant is a column of its own projection",
		},
		{
			name:        "a query parameter that takes the statement's own name is refused",
			view:        gateView{query: query, params: map[string]any{"domain": "elsewhere"}},
			scope:       testScope,
			collScope:   accesstypes.DomainPermissionScope,
			domain:      tenantColumn,
			wantErrPart: `both contain named parameter "domain"`,
		},
		{
			name:        "a query parameter in the statement's namespace is refused",
			view:        gateView{query: query, params: map[string]any{"_kind": "overhaul"}},
			scope:       testScope,
			collScope:   accesstypes.DomainPermissionScope,
			domain:      tenantColumn,
			wantErrPart: "can not start with an _",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gate := &ExecuteGate{
				method:     "NudgeView",
				user:       "dana",
				scope:      tt.scope,
				collection: gateViewCollection(t, tt.collScope, tt.domain),
			}
			if tt.cond != "" {
				gate.cond = mustCondition(tt.cond).Expr()
			}

			key := tt.key
			if key == nil {
				key = &targetKey{columns: []string{"Id"}, values: []any{"row-1"}}
			}
			stmt, err := gate.checkStatement(ExecuteTarget{Resource: gateViewResource, Label: "GateView", PKColumns: key.columns, View: tt.view}, key)
			if tt.wantErrPart != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrPart) {
					t.Fatalf("ExecuteGate.checkStatement() error = %v, want containing %q", err, tt.wantErrPart)
				}

				return
			}
			if err != nil {
				t.Fatalf("ExecuteGate.checkStatement() error = %v", err)
			}
			if got := normalizeSQL(stmt.SQL); got != tt.wantSQL {
				t.Errorf("ExecuteGate.checkStatement() SQL =\n%s\nwant\n%s", got, tt.wantSQL)
			}
			if diff := cmp.Diff(tt.wantParams, stmt.Params); diff != "" {
				t.Errorf("ExecuteGate.checkStatement() params mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Test_readCheck pins how a check-SELECT's rows are read: no row is not found, one
// row is its boolean (NULL included), and a second row is an error, never a row to
// pick between.
func Test_readCheck(t *testing.T) {
	t.Parallel()

	row := func(t *testing.T, value any) *spanner.Row {
		t.Helper()

		r, err := spanner.NewRow([]string{"g0"}, []any{value})
		if err != nil {
			t.Fatalf("spanner.NewRow() error = %v", err)
		}

		return r
	}
	errBroken := errors.New("the stream broke")

	tests := []struct {
		name       string
		rows       func(t *testing.T) []*spanner.Row
		streamErr  error
		wantAnswer spanner.NullBool
		wantFound  bool
		wantErr    error
	}{
		{
			name: "no row",
			rows: func(*testing.T) []*spanner.Row { return nil },
		},
		{
			name:       "one row, true",
			rows:       func(t *testing.T) []*spanner.Row { return []*spanner.Row{row(t, true)} },
			wantAnswer: spanner.NullBool{Bool: true, Valid: true},
			wantFound:  true,
		},
		{
			name:       "one row, false",
			rows:       func(t *testing.T) []*spanner.Row { return []*spanner.Row{row(t, false)} },
			wantAnswer: spanner.NullBool{Valid: true},
			wantFound:  true,
		},
		{
			name:      "one row, NULL",
			rows:      func(t *testing.T) []*spanner.Row { return []*spanner.Row{row(t, spanner.NullBool{})} },
			wantFound: true,
		},
		{
			name:    "two rows",
			rows:    func(t *testing.T) []*spanner.Row { return []*spanner.Row{row(t, true), row(t, true)} },
			wantErr: errCheckMatchedRows,
		},
		{
			name:      "the stream breaks before a row",
			rows:      func(*testing.T) []*spanner.Row { return nil },
			streamErr: errBroken,
			wantErr:   errBroken,
		},
		{
			name:      "the stream breaks after the row",
			rows:      func(t *testing.T) []*spanner.Row { return []*spanner.Row{row(t, true)} },
			streamErr: errBroken,
			wantErr:   errBroken,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rows := tt.rows(t)
			next := func() (*spanner.Row, error) {
				if len(rows) == 0 {
					if tt.streamErr != nil {
						return nil, tt.streamErr
					}

					return nil, iterator.Done
				}
				r := rows[0]
				rows = rows[1:]

				return r, nil
			}

			answer, found, err := readCheck(next)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("readCheck() error = %v, want %v", err, tt.wantErr)
			}
			if answer != tt.wantAnswer || found != tt.wantFound {
				t.Errorf("readCheck() = (%+v, %v), want (%+v, %v)", answer, found, tt.wantAnswer, tt.wantFound)
			}
		})
	}
}

// TestExecuteGate_tenancyStatement pins the tenancy check-SELECT (§12): the
// target's @domain binding rendered as one boolean over the row located by
// its primary key — the join-path form walks the binding hops as the same
// EXISTS chain conditions lower through, and the helper serves the bare form
// too, though the generated frame reads that one off the located row instead.
func TestExecuteGate_tenancyStatement(t *testing.T) {
	t.Parallel()

	collectionWithDomain := func(domain *DomainBindingData) func(*testing.T) *GeneratedCollection {
		return func(t *testing.T) *GeneratedCollection {
			t.Helper()

			g, err := NewGeneratedCollection(CollectionData{Resources: []CollectionResource{{
				Name:        enforcedResource,
				Scope:       accesstypes.DomainPermissionScope,
				Permissions: []accesstypes.Permission{accesstypes.Read},
				Domain:      domain,
			}}})
			if err != nil {
				t.Fatalf("NewGeneratedCollection() error = %v", err)
			}

			return g
		}
	}

	tests := []struct {
		name        string
		collection  func(*testing.T) *GeneratedCollection
		scope       accesstypes.Scope
		wantSQL     string
		wantParams  map[string]any
		wantErrPart string
	}{
		{
			name:       "bare-column binding compares the tenant column",
			collection: executeCollection,
			scope:      testScope,
			wantSQL:    "SELECT (`enforcementResources`.`Station` = @domain) AS g0 FROM enforcementResources WHERE Id = @zzTargetKey",
			wantParams: map[string]any{"zzTargetKey": "task-1", "domain": "testDomain"},
		},
		{
			name: "join-path binding walks the hops",
			collection: collectionWithDomain(&DomainBindingData{
				Column: "BerthId",
				Path:   []BindingHop{{Table: "Berths", JoinColumn: "Id", Column: "StationId"}},
			}),
			scope:      testScope,
			wantSQL:    "SELECT (EXISTS (SELECT 1 FROM `Berths` `ca1` WHERE `ca1`.`Id` = `enforcementResources`.`BerthId` AND `ca1`.`StationId` = @domain)) AS g0 FROM enforcementResources WHERE Id = @zzTargetKey",
			wantParams: map[string]any{"zzTargetKey": "task-1", "domain": "testDomain"},
		},
		{
			name:        "a target with no domain binding fails loud",
			collection:  collectionWithDomain(nil),
			scope:       testScope,
			wantErrPart: "declares no domain binding",
		},
		{
			name:        "a global request fails loud",
			collection:  executeCollection,
			scope:       accesstypes.GlobalScope(),
			wantErrPart: "requires a partitioned request",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gate := &ExecuteGate{
				method:     "NudgeTask",
				user:       "dana",
				scope:      tt.scope,
				collection: tt.collection(t),
			}

			stmt, err := gate.tenancyStatement(ExecuteTarget{Resource: enforcedResource, Label: "EnforcementResource", PKColumns: []string{"Id"}}, &targetKey{columns: []string{"Id"}, values: []any{"task-1"}})
			if tt.wantErrPart != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrPart) {
					t.Fatalf("ExecuteGate.tenancyStatement() error = %v, want containing %q", err, tt.wantErrPart)
				}

				return
			}
			if err != nil {
				t.Fatalf("ExecuteGate.tenancyStatement() error = %v", err)
			}
			if got := normalizeSQL(stmt.SQL); got != tt.wantSQL {
				t.Errorf("ExecuteGate.tenancyStatement() SQL =\n%s\nwant\n%s", got, tt.wantSQL)
			}
			if diff := cmp.Diff(tt.wantParams, stmt.Params); diff != "" {
				t.Errorf("ExecuteGate.tenancyStatement() params mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestExecuteGate_Enforce_noCondition pins the no-op paths: a nil gate and a
// gate whose grant was unconditional enforce nothing and touch no transaction.
func TestExecuteGate_Enforce_noCondition(t *testing.T) {
	t.Parallel()

	var gate *ExecuteGate
	if err := gate.Enforce(t.Context(), nil, ExecuteTarget{}, "id"); err != nil {
		t.Errorf("nil gate Enforce() error = %v, want nil", err)
	}

	gate = &ExecuteGate{method: "NudgeTask"}
	if err := gate.Enforce(t.Context(), nil, ExecuteTarget{}, "id"); err != nil {
		t.Errorf("condition-free gate Enforce() error = %v, want nil", err)
	}
}

// TestExecuteGate_newTargetKey pins how the gate pairs a target's key columns with the
// values the frame hands it: one value for each column, in the key's order, named in a
// message as the one value or the values in order; a target with no key column, and a
// count of values other than the columns', are generated code out of step with the
// library and are refused as that.
func TestExecuteGate_newTargetKey(t *testing.T) {
	t.Parallel()

	gate := &ExecuteGate{method: "NudgeView"}
	tests := []struct {
		name        string
		columns     []string
		values      []any
		wantWhere   string
		wantString  string
		wantParams  map[string]any
		wantErrPart string
	}{
		{
			name:       "one column",
			columns:    []string{"Id"},
			values:     []any{"row-1"},
			wantWhere:  "Id = @zzTargetKey",
			wantString: "row-1",
			wantParams: map[string]any{"zzTargetKey": "row-1"},
		},
		{
			name:       "two columns, in the key's order",
			columns:    []string{"UserId", "Role"},
			values:     []any{"u-1", "Pilot"},
			wantWhere:  "UserId = @zzTargetKey AND Role = @zzTargetKey1",
			wantString: "u-1, Pilot",
			wantParams: map[string]any{"zzTargetKey": "u-1", "zzTargetKey1": "Pilot"},
		},
		{name: "no key column", values: []any{"row-1"}, wantErrPart: "names no key column"},
		{name: "fewer values than columns", columns: []string{"UserId", "Role"}, values: []any{"u-1"}, wantErrPart: "is keyed by 2 column(s) and the frame handed 1 value(s)"},
		{name: "more values than columns", columns: []string{"Id"}, values: []any{"u-1", "Pilot"}, wantErrPart: "is keyed by 1 column(s) and the frame handed 2 value(s)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			key, err := gate.newTargetKey(ExecuteTarget{Resource: gateViewResource, PKColumns: tt.columns}, tt.values)
			if tt.wantErrPart != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrPart) {
					t.Fatalf("ExecuteGate.newTargetKey() error = %v, want containing %q", err, tt.wantErrPart)
				}

				return
			}
			if err != nil {
				t.Fatalf("ExecuteGate.newTargetKey() error = %v", err)
			}
			if got := key.where(); got != tt.wantWhere {
				t.Errorf("targetKey.where() = %q, want %q", got, tt.wantWhere)
			}
			if got := key.String(); got != tt.wantString {
				t.Errorf("targetKey.String() = %q, want %q", got, tt.wantString)
			}
			params := map[string]any{}
			key.bind(params)
			if diff := cmp.Diff(tt.wantParams, params); diff != "" {
				t.Errorf("targetKey.bind() params mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestExecuteGate_EnforceView_refusals pins what EnforceView refuses before it
// touches a transaction: a view's row cannot be located without the decoder's gate,
// and a table target is not its to locate.
func TestExecuteGate_EnforceView_refusals(t *testing.T) {
	t.Parallel()

	view := ExecuteTarget{Resource: gateViewResource, Label: "GateView", PKColumns: []string{"Id"}, View: gateView{query: "SELECT 1"}}

	var gate *ExecuteGate
	if err := gate.EnforceView(t.Context(), nil, view, "id"); err == nil || !strings.Contains(err.Error(), "requires the decoder's ExecuteGate") {
		t.Errorf("nil gate EnforceView() error = %v, want the gate requirement", err)
	}

	gate = &ExecuteGate{method: "NudgeView"}
	table := ExecuteTarget{Resource: enforcedResource, Label: "EnforcementResource", PKColumns: []string{"Id"}}
	if err := gate.EnforceView(t.Context(), nil, table, "id"); err == nil || !strings.Contains(err.Error(), "carries no view query") {
		t.Errorf("EnforceView() on a table target error = %v, want the view requirement", err)
	}
}
