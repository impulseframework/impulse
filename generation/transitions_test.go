package generation

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/generation/parser"
	"github.com/impulseframework/impulse/generation/parser/genlang"
)

// resolveFixtureTransition runs the transition resolution on one fixture RPC
// struct the way structsToRPCMethods does: scope first, then the declaration
// against the client's resolved resources.
func resolveFixtureTransition(t *testing.T, c *client, structs map[string]*parser.Struct, name string) (*rpcMethodInfo, error) {
	t.Helper()

	s := structs[name]
	if s == nil {
		t.Fatalf("struct %q not found in fixture package", name)
	}
	annotations, err := genlang.NewScanner(resourceKeywords()).ScanStruct(s)
	if err != nil {
		t.Fatalf("ScanStruct(%s) error = %v", name, err)
	}
	rpcMethod := &rpcMethodInfo{Struct: s, Form: fixtureForm(t, s)}
	if err := resolvePermissionScope(annotations, &rpcMethod.PermissionScope); err != nil {
		t.Fatalf("resolvePermissionScope(%s) error = %v", name, err)
	}

	return rpcMethod, c.resolveTransition(rpcMethod, s, annotations)
}

// fixtureForm classifies a fixture struct's Execute the way extraction does; a
// struct without one (a resource fixture standing in for a plain struct) stays
// unclassified.
func fixtureForm(t *testing.T, s *parser.Struct) rpcForm {
	t.Helper()

	if !s.HasMethod("Execute") {
		return rpcFormUnclassified
	}

	signature, err := classifyExecute(s)
	if err != nil {
		t.Fatalf("classifyExecute(%s) error = %v", s.Name(), err)
	}

	return signature.form
}

// transitionFixtureViews are the fixture's views a method may name as its target,
// well-formed or not.
var transitionFixtureViews = []string{"TaskDigest", "FleetDigest", "KeylessDigest", "CompoundDigest", "CompoundFleetDigest"}

// transitionFixtureClient is the state fixture client with the workflow root
// (and the stateless Ship) resolved into c.resources, the shape the RPC
// extraction sees — plus the two domain-scoped roots with their tenancy
// bindings resolved, one per @domain form, and the fixture's views, declared in
// a virtual package of their own.
func transitionFixtureClient(t *testing.T, structs map[string]*parser.Struct) *client {
	t.Helper()

	c := stateFixtureClient()
	c.virtual = "pkg/virtualresources"
	root := buildWorkflowFixture(t, c, structs, "StatefulTask")
	ship := buildWorkflowFixture(t, c, structs, "Ship")
	maintenance, err := resolveFixtureBindings(t, c, structs, "MaintenanceTask")
	if err != nil {
		t.Fatalf("resolveFixtureBindings(MaintenanceTask) error = %v", err)
	}
	pathTenant, err := resolveFixtureBindings(t, c, structs, "PathTenantTask")
	if err != nil {
		t.Fatalf("resolveFixtureBindings(PathTenantTask) error = %v", err)
	}
	seat := buildWorkflowFixture(t, c, structs, "CrewSeat")
	c.resources = []*resourceInfo{root, ship, maintenance, pathTenant, seat}
	for _, name := range transitionFixtureViews {
		views, err := c.structsToVirtualResources([]*parser.Struct{structs[name]})
		if err != nil {
			t.Fatalf("structsToVirtualResources(%s) error = %v", name, err)
		}
		c.resources = append(c.resources, views...)
	}

	return c
}

// TestResolveTransition pins the @transition/@target contract: the resolved
// declaration carries everything the handler frame renders from, and each
// malformed shape is rejected with the reason named.
func TestResolveTransition(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "bindingfixture"))

	t.Run("well-formed declaration resolves", func(t *testing.T) {
		t.Parallel()

		c := transitionFixtureClient(t, structs)
		rpcMethod, err := resolveFixtureTransition(t, c, structs, "ApproveTask")
		if err != nil {
			t.Fatalf("resolveTransition() error = %v", err)
		}
		want := &rpcTransition{
			rpcTarget: rpcTarget{
				RootStruct:   "StatefulTask",
				RootResource: "StatefulTasks",
				Keys:         []rpcTargetKey{{TargetField: "TaskID", RootPKField: "ID", RootPKColumn: "Id"}},
				StateField:   "State",
			},
			From: []string{"open"},
			To:   "approved",
		}
		if diff := cmp.Diff(want, rpcMethod.Transition, cmp.AllowUnexported(rpcTransition{})); diff != "" {
			t.Errorf("resolveTransition() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("multi-valued from set resolves in declaration order", func(t *testing.T) {
		t.Parallel()

		c := transitionFixtureClient(t, structs)
		rpcMethod, err := resolveFixtureTransition(t, c, structs, "CloseTask")
		if err != nil {
			t.Fatalf("resolveTransition() error = %v", err)
		}
		if got, want := rpcMethod.Transition.From, []string{"open", "approved"}; !cmp.Equal(want, got) {
			t.Errorf("From = %v, want %v", got, want)
		}
		if got, want := rpcMethod.Transition.FromCases(), `"open", "approved"`; got != want {
			t.Errorf("FromCases() = %q, want %q", got, want)
		}
	})

	t.Run("plain RPC resolves to no transition", func(t *testing.T) {
		t.Parallel()

		c := transitionFixtureClient(t, structs)
		plain, err := resolveFixtureTransition(t, c, structs, "StatefulTask")
		if err != nil {
			t.Fatalf("resolveTransition() on an unannotated struct error = %v", err)
		}
		if plain.Transition != nil {
			t.Errorf("unannotated struct resolved a transition: %+v", plain.Transition)
		}
		if plain.Target != nil {
			t.Errorf("unannotated struct resolved a target: %+v", plain.Target)
		}
	})

	t.Run("transition sets the target alias", func(t *testing.T) {
		t.Parallel()

		c := transitionFixtureClient(t, structs)
		rpcMethod, err := resolveFixtureTransition(t, c, structs, "ApproveTask")
		if err != nil {
			t.Fatalf("resolveTransition() error = %v", err)
		}
		if rpcMethod.Target == nil || rpcMethod.Target != &rpcMethod.Transition.rpcTarget {
			t.Errorf("Target = %+v, want the transition's embedded target", rpcMethod.Target)
		}
	})

	t.Run("plain @target(Root) resolves the located-row form", func(t *testing.T) {
		t.Parallel()

		c := transitionFixtureClient(t, structs)
		rpcMethod, err := resolveFixtureTransition(t, c, structs, "PlainTargetTask")
		if err != nil {
			t.Fatalf("resolveTransition(PlainTargetTask) error = %v", err)
		}
		if rpcMethod.Transition != nil {
			t.Errorf("plain target resolved a transition: %+v", rpcMethod.Transition)
		}
		want := &rpcTarget{
			RootStruct:   "StatefulTask",
			RootResource: "StatefulTasks",
			Keys:         []rpcTargetKey{{TargetField: "TaskID", RootPKField: "ID", RootPKColumn: "Id"}},
			// The plain form never touches state, even on a stateful root.
			StateField: "",
		}
		if diff := cmp.Diff(want, rpcMethod.Target); diff != "" {
			t.Errorf("resolveTransition(PlainTargetTask) target mismatch (-want +got):\n%s", diff)
		}
		if got, want := rpcMethod.Target.LocateColumnCalls(), ".ID()"; got != want {
			t.Errorf("LocateColumnCalls() = %q, want %q (the key itself when no state or tenant column is read)", got, want)
		}
	})

	t.Run("domain tenancy forms", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name       string
			fixture    string
			want       *rpcTarget
			wantLocate string
		}{
			{
				name:    "bare-column binding reads the located row's tenant key",
				fixture: "PlainTargetMaintenance",
				want: &rpcTarget{
					RootStruct:   "MaintenanceTask",
					RootResource: "MaintenanceTasks",
					Keys:         []rpcTargetKey{{TargetField: "TaskID", RootPKField: "ID", RootPKColumn: "Id"}},
					TenantField:  "StationID",
				},
				wantLocate: ".StationID()",
			},
			{
				name:    "a table keyed by two columns is named by one field for each",
				fixture: "PlainTargetCrewSeat",
				want: &rpcTarget{
					RootStruct:   "CrewSeat",
					RootResource: "CrewSeats",
					Keys: []rpcTargetKey{
						{TargetField: "GroupID", RootPKField: "GroupID", RootPKColumn: "GroupId"},
						{TargetField: "UserID", RootPKField: "UserID", RootPKColumn: "UserId"},
					},
				},
				wantLocate: ".GroupID()",
			},
			{
				name:    "join-path binding verifies tenancy through the gate",
				fixture: "PlainTargetPathTenant",
				want: &rpcTarget{
					RootStruct:   "PathTenantTask",
					RootResource: "PathTenantTasks",
					Keys:         []rpcTargetKey{{TargetField: "TaskID", RootPKField: "ID", RootPKColumn: "Id"}},
					TenantByPath: true,
				},
				wantLocate: ".ID()",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				c := transitionFixtureClient(t, structs)
				rpcMethod, err := resolveFixtureTransition(t, c, structs, tt.fixture)
				if err != nil {
					t.Fatalf("resolveTransition(%s) error = %v", tt.fixture, err)
				}
				if diff := cmp.Diff(tt.want, rpcMethod.Target); diff != "" {
					t.Errorf("resolveTransition(%s) target mismatch (-want +got):\n%s", tt.fixture, diff)
				}
				if got := rpcMethod.Target.LocateColumnCalls(); got != tt.wantLocate {
					t.Errorf("LocateColumnCalls() = %q, want %q", got, tt.wantLocate)
				}
			})
		}
	})

	t.Run("a view's row", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name    string
			fixture string
			want    *rpcTarget
		}{
			{
				// The gate holds the row to the tenant by the view's @domain column,
				// so the target carries no tenant field for the frame to read.
				name:    "of a tenant-scoped view is named in the view's package",
				fixture: "PlainTargetDigest",
				want: &rpcTarget{
					RootStruct:   "TaskDigest",
					RootResource: "TaskDigests",
					Keys:         []rpcTargetKey{{TargetField: "TaskID", RootPKField: "ID", RootPKColumn: "Id"}},
					ViewPackage:  "virtualresources",
				},
			},
			{
				name:    "of a global view is named in the view's package",
				fixture: "PlainTargetFleetDigest",
				want: &rpcTarget{
					RootStruct:   "FleetDigest",
					RootResource: "FleetDigests",
					Keys:         []rpcTargetKey{{TargetField: "ShipID", RootPKField: "ID", RootPKColumn: "Id"}},
					ViewPackage:  "virtualresources",
				},
			},
			{
				name:    "named by the one key column it carries, which it need not name",
				fixture: "TargetKeyNamesTheKey",
				want: &rpcTarget{
					RootStruct:   "FleetDigest",
					RootResource: "FleetDigests",
					Keys:         []rpcTargetKey{{TargetField: "ShipID", RootPKField: "ID", RootPKColumn: "Id"}},
					ViewPackage:  "virtualresources",
				},
			},
			{
				// The fields are declared out of the key's order; the pairs follow the key's.
				name:    "of a view keyed by two fields is named by one field for each, in the key's order",
				fixture: "PlainTargetCompoundFleetDigest",
				want: &rpcTarget{
					RootStruct:   "CompoundFleetDigest",
					RootResource: "CompoundFleetDigests",
					Keys: []rpcTargetKey{
						{TargetField: "ShipID", RootPKField: "ID", RootPKColumn: "Id"},
						{TargetField: "Slot", RootPKField: "Slot", RootPKColumn: "Slot"},
					},
					ViewPackage: "virtualresources",
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				c := transitionFixtureClient(t, structs)
				rpcMethod, err := resolveFixtureTransition(t, c, structs, tt.fixture)
				if err != nil {
					t.Fatalf("resolveTransition(%s) error = %v", tt.fixture, err)
				}
				if rpcMethod.Transition != nil {
					t.Errorf("a view target resolved a transition: %+v", rpcMethod.Transition)
				}
				if diff := cmp.Diff(tt.want, rpcMethod.Target); diff != "" {
					t.Errorf("resolveTransition(%s) target mismatch (-want +got):\n%s", tt.fixture, diff)
				}
			})
		}
	})

	rejections := []struct {
		name        string
		fixture     string
		wantContain string
	}{
		{
			name:        "a view with no key",
			fixture:     "TargetKeylessDigest",
			wantContain: "struct TargetKeylessDigest: @target(KeylessDigest): the view declares no @primarykey, so no row of it can be addressed — declare @primarykey on the field, or the fields, that name a row",
		},
		{
			name:        "a tenant-scoped view keyed by its tenant column and an id",
			fixture:     "TargetCompoundDigest",
			wantContain: "struct TargetCompoundDigest: @target(CompoundDigest): the view's key includes its tenant column StationID — in a tenant-scoped view the tenant column is not part of the key, since @domain already holds a row to its tenant, so declare @primarykey on the fields that name a row within a tenant",
		},
		{
			name:        "a key of two columns named by a field that does not say which it carries",
			fixture:     "TargetCompoundFleetDigest",
			wantContain: "struct TargetCompoundFleetDigest field ID: @target(CompoundFleetDigest): the target's key spans 2 columns (ID, Slot), so each @target field says which one it carries — @target(CompoundFleetDigest, key: ID)",
		},
		{
			name:        "a key of two columns with one left uncarried",
			fixture:     "TargetCompoundMissingColumn",
			wantContain: "struct TargetCompoundMissingColumn: @target(CompoundFleetDigest): no field carries Slot — a method names its target row by the whole key (ID, Slot), one @target(CompoundFleetDigest, key: Field) field for each column",
		},
		{
			name:        "a field that names a key field the target does not have",
			fixture:     "TargetCompoundUnknownKey",
			wantContain: "struct TargetCompoundUnknownKey field Slot: @target(CompoundFleetDigest, key: Berth): Berth is no key field of CompoundFleetDigest, which is keyed by ID, Slot",
		},
		{
			name:        "two fields that carry the same key column",
			fixture:     "TargetCompoundSameKeyTwice",
			wantContain: "struct TargetCompoundSameKeyTwice: fields ShipID and OtherID both carry CompoundFleetDigest.ID — each column of the target's key is carried by one field",
		},
		{
			name:        "a field of another type than the key column it carries",
			fixture:     "TargetCompoundTypeMismatch",
			wantContain: "struct TargetCompoundTypeMismatch field Slot: @target type string does not match the root key CompoundFleetDigest.Slot (int64)",
		},
		{
			name:        "two fields that name two resources",
			fixture:     "TargetTwoRoots",
			wantContain: "struct TargetTwoRoots: @target names CompoundFleetDigest on field ShipID and FleetDigest on field OtherID — a method addresses exactly one target row, so every @target field names the same resource",
		},
		{
			name:        "two fields on a target keyed by one column",
			fixture:     "TargetTwoFieldsOneColumn",
			wantContain: "struct TargetTwoFieldsOneColumn: @target appears on fields ShipID and OtherID — FleetDigest is keyed by one column (ID), and a method addresses exactly one target row, so one field carries its key",
		},
		{
			name:        "a field that names a key field on a target keyed by another",
			fixture:     "TargetKeyNamesNoKey",
			wantContain: "struct TargetKeyNamesNoKey field ShipID: @target(FleetDigest, key: Slot): FleetDigest is keyed by ID",
		},
		{
			name:        "a view as a workflow root",
			fixture:     "TransitionOnDigest",
			wantContain: "struct TransitionOnDigest: @transition(TaskDigest): TaskDigest is a view, which has no @state column to move — @transition names a table-backed workflow root; a method that takes a view's row as its target declares @target(TaskDigest) on the field carrying the row's key, with no @transition",
		},
		{name: "a global method over a tenant-scoped view", fixture: "TargetDigestScopeMismatch", wantContain: "permission scopes differ"},
		{name: "a key of another type than the view's", fixture: "TargetDigestTypeMismatch", wantContain: "does not match the root key FleetDigest.ID"},
		{name: "unknown root", fixture: "TransitionUnknownRoot", wantContain: "unknown resource struct"},
		{name: "stateless root", fixture: "TransitionStatelessRoot", wantContain: "carries no @state"},
		{name: "from outside the enum", fixture: "TransitionBadFrom", wantContain: `"bogus" is not a value`},
		{name: "duplicate from value", fixture: "TransitionDuplicateFrom", wantContain: "appears twice in the from set"},
		{name: "to outside the enum", fixture: "TransitionBadTo", wantContain: `"bogus" is not a value`},
		{name: "no target field", fixture: "TransitionNoTarget", wantContain: "exactly one @target"},
		{name: "two target fields", fixture: "TransitionTwoTargets", wantContain: "struct TransitionTwoTargets: @target appears on fields TaskID and OtherID — a @transition moves one row of a workflow root, which is keyed by one column, so one field carries its key"},
		{name: "target type mismatch", fixture: "TransitionTargetTypeMismatch", wantContain: "does not match the root key"},
		{name: "scope mismatch", fixture: "TransitionScopeMismatch", wantContain: "permission scopes differ"},
		{name: "bare target without transition", fixture: "TargetWithoutTransition", wantContain: "names the row resource the handler locates"},
		{name: "target argument with transition", fixture: "TransitionTargetWithArg", wantContain: "takes no argument"},
	}
	for _, tt := range rejections {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := transitionFixtureClient(t, structs)
			_, err := resolveFixtureTransition(t, c, structs, tt.fixture)
			if err == nil {
				t.Fatalf("resolveTransition(%s) expected an error containing %q, got nil", tt.fixture, tt.wantContain)
			}
			if !strings.Contains(err.Error(), tt.wantContain) {
				t.Errorf("resolveTransition(%s) error = %q, want containing %q", tt.fixture, err, tt.wantContain)
			}
		})
	}

	t.Run("client form is rejected", func(t *testing.T) {
		t.Parallel()

		// TransitionClientForm's Execute takes resource.Client, so it runs
		// outside the transaction the located-row checks need.
		c := transitionFixtureClient(t, structs)
		_, err := resolveFixtureTransition(t, c, structs, "TransitionClientForm")
		if err == nil || !strings.Contains(err.Error(), "requires the transaction form") {
			t.Errorf("resolveTransition(TransitionClientForm) error = %v, want the transaction-form requirement", err)
		}
	})

	t.Run("suppressed handler is rejected", func(t *testing.T) {
		t.Parallel()

		c := transitionFixtureClient(t, structs)
		s := structs["ApproveTask"]
		annotations, err := genlang.NewScanner(resourceKeywords()).ScanStruct(s)
		if err != nil {
			t.Fatalf("ScanStruct() error = %v", err)
		}
		rpcMethod := &rpcMethodInfo{Struct: s, Form: fixtureForm(t, s), SuppressHandler: true}
		err = c.resolveTransition(rpcMethod, s, annotations)
		if err == nil || !strings.Contains(err.Error(), "generated handler") {
			t.Errorf("resolveTransition() with a suppressed handler error = %v, want the generated-handler requirement", err)
		}
	})
}

// Test_rpcHandlerTemplate_transition pins the generated transition frame: the
// pre-body row location within the tenancy predicate, the pre-image state
// check, and the post-body stamp — and that a plain RPC method renders none
// of it.
func Test_rpcHandlerTemplate_transition(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "bindingfixture"))
	c := transitionFixtureClient(t, structs)

	buildMethod := func(t *testing.T, name string) *rpcMethodInfo {
		t.Helper()

		rpcMethod, err := resolveFixtureTransition(t, c, structs, name)
		if err != nil {
			t.Fatalf("resolveFixtureTransition(%s) error = %v", name, err)
		}
		for _, field := range rpcMethod.Struct.Fields() {
			rpcMethod.Fields = append(rpcMethod.Fields, &rpcField{Field: field})
		}

		return rpcMethod
	}

	render := func(t *testing.T, rpcMethod *rpcMethodInfo) string {
		t.Helper()

		out, err := c.generateTemplateOutput("rpcHandlerTemplate", rpcHandlerTemplate, &rpcHandlerData{
			Source:           "pkg/rpc",
			RPCMethod:        rpcMethod,
			Package:          "app",
			ApplicationName:  "App",
			ReceiverName:     "a",
			ResourcesPackage: "resources",
		})
		if err != nil {
			t.Fatalf("generateTemplateOutput() error = %v", err)
		}

		return string(out)
	}

	t.Run("declared transition renders the frame", func(t *testing.T) {
		t.Parallel()

		out := render(t, buildMethod(t, "CloseTask"))
		for _, want := range []string{
			"decoder := NewTargetedRPCDecoder[bindingfixture.CloseTask, request](a, accesstypes.Execute)",
			"params, gate, err := decoder.Decode(r, accesstypes.GlobalScope())",
			"resources.NewStatefulTaskQuery().",
			"AddColumns(resources.NewStatefulTaskColumns().State()).",
			"SetID(p.TaskID).",
			`httpio.NewNotFoundMessagef("StatefulTask %s does not exist", p.TaskID)`,
			"switch row.Data.State {",
			`case "open", "approved":`,
			// One refusal shape for the state check and the grant condition:
			// the wire never says which said no (§12).
			`httpio.NewForbiddenMessagef("CloseTask may not run against StatefulTask %s", p.TaskID)`,
			`gate.Enforce(ctx, txn, resource.ExecuteTarget{Resource: "StatefulTasks", Label: "StatefulTask", PKColumns: []string{"Id"}}, p.TaskID)`,
			`resources.NewStatefulTaskUpdatePatch(p.TaskID).SetState("closed").Buffer(ctx, txn, resource.UserEvent(ctx))`,
		} {
			if !strings.Contains(out, want) {
				t.Errorf("transition handler missing %q:\n%s", want, out)
			}
		}
		// A global-scoped root has no tenant key to compare.
		if strings.Contains(out, "TenantKeyEquals") {
			t.Errorf("global-scoped transition must not render a tenancy comparison:\n%s", out)
		}
	})

	t.Run("a table keyed by two columns is located by both, and named by both in a refusal", func(t *testing.T) {
		t.Parallel()

		out := render(t, buildMethod(t, "PlainTargetCrewSeat"))
		for _, want := range []string{
			"AddColumns(resources.NewCrewSeatColumns().GroupID()).",
			"SetGroupID(p.GroupID).\n\t\t\t\t\tSetUserID(p.UserID).\n\t\t\t\t\tRead(ctx, txn)",
			`httpio.NewNotFoundMessagef("CrewSeat %s, %s does not exist", p.GroupID, p.UserID)`,
			`gate.Enforce(ctx, txn, resource.ExecuteTarget{Resource: "CrewSeats", Label: "CrewSeat", PKColumns: []string{"GroupId", "UserId"}}, p.GroupID, p.UserID)`,
		} {
			if !strings.Contains(out, want) {
				t.Errorf("compound-key target handler missing %q:\n%s", want, out)
			}
		}
	})

	t.Run("plain @target renders the located-row form", func(t *testing.T) {
		t.Parallel()

		out := render(t, buildMethod(t, "PlainTargetTask"))
		for _, want := range []string{
			"decoder := NewTargetedRPCDecoder[bindingfixture.PlainTargetTask, request](a, accesstypes.Execute)",
			"params, gate, err := decoder.Decode(r, accesstypes.GlobalScope())",
			"AddColumns(resources.NewStatefulTaskColumns().ID()).",
			`httpio.NewNotFoundMessagef("StatefulTask %s does not exist", p.TaskID)`,
			`gate.Enforce(ctx, txn, resource.ExecuteTarget{Resource: "StatefulTasks", Label: "StatefulTask", PKColumns: []string{"Id"}}, p.TaskID)`,
		} {
			if !strings.Contains(out, want) {
				t.Errorf("plain-target handler missing %q:\n%s", want, out)
			}
		}
		// No state check, no stamp: the plain form moves no state.
		for _, notWant := range []string{"switch row.Data", "UpdatePatch", "TenantKeyEquals"} {
			if strings.Contains(out, notWant) {
				t.Errorf("plain-target handler must not contain %q:\n%s", notWant, out)
			}
		}
	})

	t.Run("domain-scoped tenancy forms", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name     string
			mutate   func(*rpcMethodInfo)
			wants    []string
			notWants []string
		}{
			{
				name:   "bare column compares the located row's tenant key",
				mutate: func(m *rpcMethodInfo) { m.Transition.TenantField = "StationID" },
				wants: []string{
					"AddColumns(resources.NewStatefulTaskColumns().State().StationID()).",
					"resource.TenantKeyEquals(row.Data.StationID, domain)",
				},
				notWants: []string{"VerifyTenancy"},
			},
			{
				name:   "join path verifies tenancy through the gate",
				mutate: func(m *rpcMethodInfo) { m.Transition.TenantByPath = true },
				wants: []string{
					"AddColumns(resources.NewStatefulTaskColumns().State()).",
					`gate.VerifyTenancy(ctx, txn, resource.ExecuteTarget{Resource: "StatefulTasks", Label: "StatefulTask", PKColumns: []string{"Id"}}, p.TaskID)`,
				},
				notWants: []string{"TenantKeyEquals"},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				rpcMethod := buildMethod(t, "CloseTask")
				tt.mutate(rpcMethod)
				out := render(t, rpcMethod)
				for _, want := range tt.wants {
					if !strings.Contains(out, want) {
						t.Errorf("domain-scoped transition handler missing %q:\n%s", want, out)
					}
				}
				for _, notWant := range tt.notWants {
					if strings.Contains(out, notWant) {
						t.Errorf("domain-scoped transition handler must not contain %q:\n%s", notWant, out)
					}
				}
			})
		}
	})

	t.Run("a view target renders the gate's located-row check", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name    string
			fixture string
			wants   []string
		}{
			{
				name:    "a tenant-scoped view",
				fixture: "PlainTargetDigest",
				wants: []string{
					"decoder := NewTargetedRPCDecoder[bindingfixture.PlainTargetDigest, request](a, accesstypes.Execute)",
					"within the request's tenant and judges any row-referencing",
					`gate.EnforceView(ctx, txn, resource.ExecuteTarget{Resource: "TaskDigests", Label: "TaskDigest", PKColumns: []string{"Id"}, View: virtualresources.TaskDigest{}}, p.TaskID)`,
				},
			},
			{
				name:    "a global view",
				fixture: "PlainTargetFleetDigest",
				wants: []string{
					"params, gate, err := decoder.Decode(r, accesstypes.GlobalScope())",
					`gate.EnforceView(ctx, txn, resource.ExecuteTarget{Resource: "FleetDigests", Label: "FleetDigest", PKColumns: []string{"Id"}, View: virtualresources.FleetDigest{}}, p.ShipID)`,
				},
			},
			{
				name:    "a view keyed by two fields hands the gate both columns and both values, in the key's order",
				fixture: "PlainTargetCompoundFleetDigest",
				wants: []string{
					`gate.EnforceView(ctx, txn, resource.ExecuteTarget{Resource: "CompoundFleetDigests", Label: "CompoundFleetDigest", PKColumns: []string{"Id", "Slot"}, View: virtualresources.CompoundFleetDigest{}}, p.ShipID, p.Slot)`,
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				out := render(t, buildMethod(t, tt.fixture))
				for _, want := range tt.wants {
					if !strings.Contains(out, want) {
						t.Errorf("view-target handler missing %q:\n%s", want, out)
					}
				}
				// The frame reads no row of a view: no query builder, in the resources
				// package or the view's, no tenant key off a row, no second check.
				for _, notWant := range []string{"Query()", "row, err :=", "row.Data", "TenantKeyEquals", "VerifyTenancy", "gate.Enforce(", "UpdatePatch"} {
					if strings.Contains(out, notWant) {
						t.Errorf("view-target handler must not contain %q:\n%s", notWant, out)
					}
				}
			})
		}
	})

	t.Run("the publish names the home's live derivations", func(t *testing.T) {
		t.Parallel()

		rpcMethod := &rpcMethodInfo{Struct: structs["ApproveTask"], Form: fixtureForm(t, structs["ApproveTask"])}
		for _, field := range rpcMethod.Struct.Fields() {
			rpcMethod.Fields = append(rpcMethod.Fields, &rpcField{Field: field})
		}
		crew := &identityInstance{auth: "Crew", resources: "pkg/identity/internal/crewresources", methods: "pkg/identity/crewidentity", handlers: "pkg/identity/crewhandlers"}
		home := (&resourceGenerator{client: c}).homeOf(crew)

		// An auth's handlers publish with the application's derivations, kept in the
		// field the application constructed them with.
		out, err := c.generateTemplateOutput("rpcHandlerTemplate", rpcHandlerTemplate, &rpcHandlerData{
			Source:           "pkg/identity/crewidentity",
			RPCMethod:        rpcMethod,
			Package:          home.dir.Package(),
			ApplicationName:  home.applicationName,
			ReceiverName:     home.receiverName,
			ResourcesPackage: home.resources.Package(),
			Home:             home,
		})
		if err != nil {
			t.Fatalf("generateTemplateOutput() error = %v", err)
		}
		if want := `live.Publish(ctx, h.LiveService(), h.liveDerivations, "", touched)`; !strings.Contains(string(out), want) {
			t.Errorf("an auth's handler lacks %q:\n%s", want, out)
		}

		// The application's own name the assembly in their package.
		if want := `live.Publish(ctx, a.LiveService(), liveDerivations, "", touched)`; !strings.Contains(render(t, rpcMethod), want) {
			t.Errorf("the application's handler lacks %q", want)
		}
	})

	t.Run("plain RPC renders no frame", func(t *testing.T) {
		t.Parallel()

		rpcMethod := &rpcMethodInfo{Struct: structs["ApproveTask"], Form: fixtureForm(t, structs["ApproveTask"])}
		for _, field := range rpcMethod.Struct.Fields() {
			rpcMethod.Fields = append(rpcMethod.Fields, &rpcField{Field: field})
		}
		out := render(t, rpcMethod)
		for _, notWant := range []string{"Declared target", "TenantKeyEquals", "UpdatePatch", "NotFoundMessagef", "gate", "Targeted"} {
			if strings.Contains(out, notWant) {
				t.Errorf("plain RPC handler must not contain %q:\n%s", notWant, out)
			}
		}
	})
}
