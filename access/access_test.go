package access

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
)

func TestNew(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
	}{
		{name: "new"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := New(newFakeStore())
			if err != nil {
				t.Error(err)
			}
			t.Cleanup(func() {
				if err := got.Close(); err != nil {
					t.Errorf("Client.Close() error = %v", err)
				}
			})

			if got.userManager == nil {
				t.Error("userManager is nil")
			}
		})
	}
}

// TestClient_CheckUser_returnsDecision pins the ABAC-ready seam translation:
// the RBAC snapshot evaluator answers a bare allow/deny, and the re-signed
// seam surfaces it as a Granted/Denied Decision. Conditional cannot occur
// until the engine holds conditions.
func TestClient_CheckUser_returnsDecision(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	client, err := New(newFakeStore())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("Client.Close() error = %v", err)
		}
	})

	tenant1 := accesstypes.DomainScope("tenant1")
	manager := client.UserManager()
	if err := manager.AddRole(ctx, tenant1.PolicyScope(), "Editor", aDescription); err != nil {
		t.Fatalf("AddRole() error = %v", err)
	}
	if err := manager.AddRolePermission(ctx, tenant1.PolicyScope(), "Editor", "Read"); err != nil {
		t.Fatalf("AddRolePermission() error = %v", err)
	}
	if err := manager.AddRoleUsers(ctx, tenant1.PolicyScope(), "Editor", "erin"); err != nil {
		t.Fatalf("AddRoleUsers() error = %v", err)
	}

	tests := []struct {
		name string
		user accesstypes.User
		perm accesstypes.Permission
		want accesstypes.Decision
	}{
		{
			name: "scope-wide grant is granted",
			user: "erin",
			perm: "Read",
			want: accesstypes.Granted(),
		},
		{
			name: "unheld permission fails closed to denied",
			user: "erin",
			perm: "Delete",
			want: accesstypes.Denied(),
		},
		{
			name: "unknown user fails closed to denied",
			user: "sam",
			perm: "Read",
			want: accesstypes.Denied(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := client.CheckUser(t.Context(), accesstypes.NewEnvironment(), tt.user, tenant1, tt.perm)
			if err != nil {
				t.Fatalf("CheckUser() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got, cmp.AllowUnexported(accesstypes.Decision{}, accesstypes.Condition{})); diff != "" {
				t.Errorf("CheckUser() (-want +got):\n%s", diff)
			}
		})
	}
}

// TestClient_CheckUserResources_returnsDecisions pins the per-resource
// Decision translation: every checked resource gets an answer from one
// snapshot — Granted where a grant covers it, Denied otherwise.
func TestClient_CheckUserResources_returnsDecisions(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	client, err := New(newFakeStore())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("Client.Close() error = %v", err)
		}
	})

	tenant1 := accesstypes.DomainScope("tenant1")
	manager := client.UserManager()
	if err := manager.AddRole(ctx, tenant1.PolicyScope(), "Editor", aDescription); err != nil {
		t.Fatalf("AddRole() error = %v", err)
	}
	if err := manager.AddRolePermissionResources(ctx, tenant1.PolicyScope(), "Editor", "Read", "employees", "employees.name"); err != nil {
		t.Fatalf("AddRolePermissionResources() error = %v", err)
	}
	if err := manager.AddRoleUsers(ctx, tenant1.PolicyScope(), "Editor", "erin"); err != nil {
		t.Fatalf("AddRoleUsers() error = %v", err)
	}

	tests := []struct {
		name      string
		resources []accesstypes.Resource
		want      accesstypes.Decisions
	}{
		{
			name:      "granted resources",
			resources: []accesstypes.Resource{"employees", "employees.name"},
			want: accesstypes.Decisions{
				"employees":      accesstypes.Granted(),
				"employees.name": accesstypes.Granted(),
			},
		},
		{
			name:      "mixed grants answer per resource",
			resources: []accesstypes.Resource{"employees", "secrets"},
			want: accesstypes.Decisions{
				"employees": accesstypes.Granted(),
				"secrets":   accesstypes.Denied(),
			},
		},
		{
			name:      "uncovered resource fails closed to denied",
			resources: []accesstypes.Resource{"secrets"},
			want: accesstypes.Decisions{
				"secrets": accesstypes.Denied(),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := client.CheckUserResources(t.Context(), accesstypes.NewEnvironment(), "erin", tenant1, "Read", tt.resources...)
			if err != nil {
				t.Fatalf("CheckUserResources() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got, cmp.AllowUnexported(accesstypes.Decision{}, accesstypes.Condition{})); diff != "" {
				t.Errorf("CheckUserResources() (-want +got):\n%s", diff)
			}
		})
	}
}

// TestClient_CheckUserResources_conditionalDecision pins the Conditional leg
// of the public seam, including the shared-group emission: resources covered
// only by conditional grants surface as Conditional Decisions, and the ones
// sharing a covering set share ONE ConditionGroup listing every member —
// the same group value in each member's Decision — while a distinct set is
// its own group; an unconditional cover stays Granted regardless of other
// conditional grants, and no cover stays Denied. The groups' Condition
// payloads are the accesstypes placeholder until the expression language
// lands. Grants are seeded through the store directly: nothing on the
// management surface authors conditions yet.
func TestClient_CheckUserResources_conditionalDecision(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	store := newFakeStore()
	client, err := New(store)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("Client.Close() error = %v", err)
		}
	})

	tenant1 := accesstypes.DomainScope("tenant1")
	if err := store.InsertRole(ctx, tenant1.PolicyScope(), "Editor", aDescription); err != nil {
		t.Fatalf("InsertRole() error = %v", err)
	}
	for _, g := range []struct {
		resource, field, condition string
	}{
		{resource: "employees", field: ""},
		{resource: "employees", field: "name", condition: "owner = subject"},
		{resource: "employees", field: "email", condition: "owner = subject"},
		{resource: "employees", field: "salary", condition: "state = 'new'"},
		{resource: "widgets", field: "*"},
		{resource: "widgets", field: "name", condition: "owner = subject"},
	} {
		if err := store.InsertGrant(ctx, tenant1.PolicyScope(), "Editor", "Update", g.resource, g.field, g.condition); err != nil {
			t.Fatalf("InsertGrant(%v) error = %v", g, err)
		}
	}
	if err := store.InsertUserRole(ctx, tenant1.PolicyScope(), "erin", "Editor"); err != nil {
		t.Fatalf("InsertUserRole() error = %v", err)
	}

	got, err := client.CheckUserResources(ctx, accesstypes.NewEnvironment(), "erin", tenant1, "Update",
		"employees", "employees.name", "employees.email", "employees.salary", "widgets.name", "secrets")
	if err != nil {
		t.Fatalf("CheckUserResources() error = %v", err)
	}
	payload := func(source string) accesstypes.Condition {
		c, err := accesstypes.NewCondition(source)
		if err != nil {
			t.Fatalf("accesstypes.NewCondition(%q) error = %v", source, err)
		}

		return c
	}
	sharedGroup := accesstypes.ConditionGroup{
		Resources: []accesstypes.Resource{"employees.name", "employees.email"},
		Condition: payload("owner = subject"),
	}
	want := accesstypes.Decisions{
		"employees":       accesstypes.Granted(),
		"employees.name":  accesstypes.Conditional(sharedGroup),
		"employees.email": accesstypes.Conditional(sharedGroup),
		"employees.salary": accesstypes.Conditional(accesstypes.ConditionGroup{
			Resources: []accesstypes.Resource{"employees.salary"},
			Condition: payload("state = 'new'"),
		}),
		"widgets.name": accesstypes.Granted(),
		"secrets":      accesstypes.Denied(),
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(accesstypes.Decision{}, accesstypes.Condition{})); diff != "" {
		t.Errorf("CheckUserResources() (-want +got):\n%s", diff)
	}
}

// TestClient_CheckUserResources_unknownTenantFailsClosed pins the Domains
// decoupling: there is no tenant validation on the check path — an unknown
// tenant scope holds no grants, so everything comes back missing, without an
// error.
func TestClient_CheckUserResources_unknownTenantFailsClosed(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	store := newFakeStore()
	client, err := New(store)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("Client.Close() error = %v", err)
		}
	})

	tenant1 := accesstypes.DomainScope("tenant1")
	manager := client.UserManager()
	if err := manager.AddRole(ctx, tenant1.PolicyScope(), "Editor", aDescription); err != nil {
		t.Fatalf("AddRole() error = %v", err)
	}
	if err := manager.AddRolePermissionResources(ctx, tenant1.PolicyScope(), "Editor", "Read", "employees"); err != nil {
		t.Fatalf("AddRolePermissionResources() error = %v", err)
	}
	if err := manager.AddRoleUsers(ctx, tenant1.PolicyScope(), "Editor", "erin"); err != nil {
		t.Fatalf("AddRoleUsers() error = %v", err)
	}

	env := accesstypes.NewEnvironment()

	decisions, err := client.CheckUserResources(ctx, env, "erin", tenant1, "Read", "employees")
	if err != nil {
		t.Fatalf("CheckUserResources() error = %v", err)
	}
	want := accesstypes.Decisions{"employees": accesstypes.Granted()}
	if diff := cmp.Diff(want, decisions, cmp.AllowUnexported(accesstypes.Decision{}, accesstypes.Condition{})); diff != "" {
		t.Errorf("CheckUserResources() in known tenant (-want +got):\n%s", diff)
	}

	decisions, err = client.CheckUserResources(ctx, env, "erin", accesstypes.DomainScope("no-such-tenant"), "Read", "employees")
	if err != nil {
		t.Fatalf("CheckUserResources() unknown tenant error = %v, want fail-closed denial, not an error", err)
	}
	want = accesstypes.Decisions{"employees": accesstypes.Denied()}
	if diff := cmp.Diff(want, decisions, cmp.AllowUnexported(accesstypes.Decision{}, accesstypes.Condition{})); diff != "" {
		t.Errorf("CheckUserResources() in unknown tenant (-want +got):\n%s", diff)
	}
}

// TestClient_CheckUser_conditionFolding pins the scope-wide folding path: a
// row-free condition on a scope-wide grant folds against the request's
// Environment to a definite Granted or Denied, an absent referenced
// attribute is a check error, and a row-free condition needing data (a
// subject attribute) is a check error too — this path has nowhere to
// evaluate it.
func TestClient_CheckUser_conditionFolding(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	store := newFakeStore()
	client, err := New(store)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("Client.Close() error = %v", err)
		}
	})

	tenant1 := accesstypes.DomainScope("tenant1")
	if err := store.InsertRole(ctx, tenant1.PolicyScope(), "Chief", aDescription); err != nil {
		t.Fatalf("InsertRole() error = %v", err)
	}
	if err := store.InsertGrant(ctx, tenant1.PolicyScope(), "Chief", "Approve", "", "", "now < '2027-03-01T00:00:00Z'"); err != nil {
		t.Fatalf("InsertGrant() error = %v", err)
	}
	if err := store.InsertGrant(ctx, tenant1.PolicyScope(), "Chief", "Export", "", "", "now < subject.shiftEnd"); err != nil {
		t.Fatalf("InsertGrant() error = %v", err)
	}
	if err := store.InsertUserRole(ctx, tenant1.PolicyScope(), "erin", "Chief"); err != nil {
		t.Fatalf("InsertUserRole() error = %v", err)
	}

	window := time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		env     accesstypes.Environment
		perm    accesstypes.Permission
		want    accesstypes.Decision
		wantErr bool
	}{
		{
			name: "window open folds to granted",
			env:  accesstypes.EnvironmentAt(window.Add(-time.Hour)),
			perm: "Approve",
			want: accesstypes.Granted(),
		},
		{
			name: "window passed folds to denied",
			env:  accesstypes.EnvironmentAt(window.Add(time.Hour)),
			perm: "Approve",
			want: accesstypes.Denied(),
		},
		{
			name:    "environment without the referenced attribute is a check error",
			env:     accesstypes.NewEnvironment(),
			perm:    "Approve",
			wantErr: true,
		},
		{
			name:    "condition needing data no scope-wide check can reach is a check error",
			env:     accesstypes.EnvironmentAt(window),
			perm:    "Export",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := client.CheckUser(t.Context(), tt.env, "erin", tenant1, tt.perm)
			if (err != nil) != tt.wantErr {
				t.Fatalf("CheckUser() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if diff := cmp.Diff(tt.want, got, cmp.AllowUnexported(accesstypes.Decision{}, accesstypes.Condition{})); diff != "" {
				t.Errorf("CheckUser() (-want +got):\n%s", diff)
			}
		})
	}
}

// TestClient_CheckRole_returnsDecision pins the role twin of CheckUser: a
// role's scope-wide grant answers Granted for the role itself, with no member
// involved; an unheld permission and an unknown role fail closed to Denied.
func TestClient_CheckRole_returnsDecision(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	client, err := New(newFakeStore())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("Client.Close() error = %v", err)
		}
	})

	tenant1 := accesstypes.DomainScope("tenant1")
	manager := client.UserManager()
	if err := manager.AddRole(ctx, tenant1.PolicyScope(), "Editor", aDescription); err != nil {
		t.Fatalf("AddRole() error = %v", err)
	}
	if err := manager.AddRolePermission(ctx, tenant1.PolicyScope(), "Editor", "Read"); err != nil {
		t.Fatalf("AddRolePermission() error = %v", err)
	}

	tests := []struct {
		name string
		role accesstypes.Role
		perm accesstypes.Permission
		want accesstypes.Decision
	}{
		{
			name: "scope-wide grant is granted",
			role: "Editor",
			perm: "Read",
			want: accesstypes.Granted(),
		},
		{
			name: "unheld permission fails closed to denied",
			role: "Editor",
			perm: "Delete",
			want: accesstypes.Denied(),
		},
		{
			name: "unknown role fails closed to denied",
			role: "Ghost",
			perm: "Read",
			want: accesstypes.Denied(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := client.CheckRole(t.Context(), accesstypes.NewEnvironment(), tt.role, tenant1, tt.perm)
			if err != nil {
				t.Fatalf("CheckRole() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got, cmp.AllowUnexported(accesstypes.Decision{}, accesstypes.Condition{})); diff != "" {
				t.Errorf("CheckRole() (-want +got):\n%s", diff)
			}
		})
	}
}

// TestClient_CheckRoleResources_returnsDecisions pins the role twin of
// CheckUserResources: one Decision per resource from one snapshot — Granted
// where the role's grants cover it, Denied otherwise — the same answers its
// members get, with no member needed.
func TestClient_CheckRoleResources_returnsDecisions(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	client, err := New(newFakeStore())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("Client.Close() error = %v", err)
		}
	})

	tenant1 := accesstypes.DomainScope("tenant1")
	manager := client.UserManager()
	if err := manager.AddRole(ctx, tenant1.PolicyScope(), "Editor", aDescription); err != nil {
		t.Fatalf("AddRole() error = %v", err)
	}
	if err := manager.AddRolePermissionResources(ctx, tenant1.PolicyScope(), "Editor", "Read", "employees", "employees.name"); err != nil {
		t.Fatalf("AddRolePermissionResources() error = %v", err)
	}

	tests := []struct {
		name      string
		resources []accesstypes.Resource
		want      accesstypes.Decisions
	}{
		{
			name:      "granted resources",
			resources: []accesstypes.Resource{"employees", "employees.name"},
			want: accesstypes.Decisions{
				"employees":      accesstypes.Granted(),
				"employees.name": accesstypes.Granted(),
			},
		},
		{
			name:      "mixed grants answer per resource",
			resources: []accesstypes.Resource{"employees", "secrets"},
			want: accesstypes.Decisions{
				"employees": accesstypes.Granted(),
				"secrets":   accesstypes.Denied(),
			},
		},
		{
			name:      "uncovered resource fails closed to denied",
			resources: []accesstypes.Resource{"secrets"},
			want: accesstypes.Decisions{
				"secrets": accesstypes.Denied(),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := client.CheckRoleResources(t.Context(), accesstypes.NewEnvironment(), "Editor", tenant1, "Read", tt.resources...)
			if err != nil {
				t.Fatalf("CheckRoleResources() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got, cmp.AllowUnexported(accesstypes.Decision{}, accesstypes.Condition{})); diff != "" {
				t.Errorf("CheckRoleResources() (-want +got):\n%s", diff)
			}
		})
	}
}

// TestClient_CheckRoleResources_conditionalDecision pins the Conditional leg
// for a role, including the shared-group emission: coverage only by
// conditional grants surfaces as Conditional, an unconditional cover stays
// Granted, no cover stays Denied — and a subject term rides through unbound.
// A role has no identity of its own; the resource layer binds the session's
// effective identity at render time.
func TestClient_CheckRoleResources_conditionalDecision(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	store := newFakeStore()
	client, err := New(store)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("Client.Close() error = %v", err)
		}
	})

	tenant1 := accesstypes.DomainScope("tenant1")
	if err := store.InsertRole(ctx, tenant1.PolicyScope(), "Editor", aDescription); err != nil {
		t.Fatalf("InsertRole() error = %v", err)
	}
	for _, g := range []struct {
		resource, field, condition string
	}{
		{resource: "employees", field: ""},
		{resource: "employees", field: "name", condition: "owner = subject"},
		{resource: "employees", field: "email", condition: "owner = subject"},
		{resource: "employees", field: "salary", condition: "state = 'new'"},
		{resource: "widgets", field: "*"},
		{resource: "widgets", field: "name", condition: "owner = subject"},
	} {
		if err := store.InsertGrant(ctx, tenant1.PolicyScope(), "Editor", "Update", g.resource, g.field, g.condition); err != nil {
			t.Fatalf("InsertGrant(%v) error = %v", g, err)
		}
	}

	got, err := client.CheckRoleResources(ctx, accesstypes.NewEnvironment(), "Editor", tenant1, "Update",
		"employees", "employees.name", "employees.email", "employees.salary", "widgets.name", "secrets")
	if err != nil {
		t.Fatalf("CheckRoleResources() error = %v", err)
	}
	payload := func(source string) accesstypes.Condition {
		c, err := accesstypes.NewCondition(source)
		if err != nil {
			t.Fatalf("accesstypes.NewCondition(%q) error = %v", source, err)
		}

		return c
	}
	sharedGroup := accesstypes.ConditionGroup{
		Resources: []accesstypes.Resource{"employees.name", "employees.email"},
		Condition: payload("owner = subject"),
	}
	want := accesstypes.Decisions{
		"employees":       accesstypes.Granted(),
		"employees.name":  accesstypes.Conditional(sharedGroup),
		"employees.email": accesstypes.Conditional(sharedGroup),
		"employees.salary": accesstypes.Conditional(accesstypes.ConditionGroup{
			Resources: []accesstypes.Resource{"employees.salary"},
			Condition: payload("state = 'new'"),
		}),
		"widgets.name": accesstypes.Granted(),
		"secrets":      accesstypes.Denied(),
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(accesstypes.Decision{}, accesstypes.Condition{})); diff != "" {
		t.Errorf("CheckRoleResources() (-want +got):\n%s", diff)
	}
}

// TestClient_CheckRole_conditionFolding pins the scope-wide folding path for
// a role: a row-free condition folds against the request's Environment to a
// definite Granted or Denied, an absent referenced attribute is a check
// error, and a condition needing data — a subject attribute — is a check
// error too. The facts of a role check carry the instant and nothing else.
func TestClient_CheckRole_conditionFolding(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	store := newFakeStore()
	client, err := New(store)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("Client.Close() error = %v", err)
		}
	})

	tenant1 := accesstypes.DomainScope("tenant1")
	if err := store.InsertRole(ctx, tenant1.PolicyScope(), "Chief", aDescription); err != nil {
		t.Fatalf("InsertRole() error = %v", err)
	}
	if err := store.InsertGrant(ctx, tenant1.PolicyScope(), "Chief", "Approve", "", "", "now < '2027-03-01T00:00:00Z'"); err != nil {
		t.Fatalf("InsertGrant() error = %v", err)
	}
	if err := store.InsertGrant(ctx, tenant1.PolicyScope(), "Chief", "Export", "", "", "now < subject.shiftEnd"); err != nil {
		t.Fatalf("InsertGrant() error = %v", err)
	}

	window := time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		env     accesstypes.Environment
		perm    accesstypes.Permission
		want    accesstypes.Decision
		wantErr bool
	}{
		{
			name: "window open folds to granted",
			env:  accesstypes.EnvironmentAt(window.Add(-time.Hour)),
			perm: "Approve",
			want: accesstypes.Granted(),
		},
		{
			name: "window passed folds to denied",
			env:  accesstypes.EnvironmentAt(window.Add(time.Hour)),
			perm: "Approve",
			want: accesstypes.Denied(),
		},
		{
			name:    "environment without the referenced attribute is a check error",
			env:     accesstypes.NewEnvironment(),
			perm:    "Approve",
			wantErr: true,
		},
		{
			name:    "condition needing data no scope-wide check can reach is a check error",
			env:     accesstypes.EnvironmentAt(window),
			perm:    "Export",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := client.CheckRole(t.Context(), tt.env, "Chief", tenant1, tt.perm)
			if (err != nil) != tt.wantErr {
				t.Fatalf("CheckRole() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if diff := cmp.Diff(tt.want, got, cmp.AllowUnexported(accesstypes.Decision{}, accesstypes.Condition{})); diff != "" {
				t.Errorf("CheckRole() (-want +got):\n%s", diff)
			}
		})
	}
}
