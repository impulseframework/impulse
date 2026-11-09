package access

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
)

// TestClient_ForRole pins the role-context seam: ForRole binds exactly the
// role, and Check is a pure delegate to CheckRoleResources — the role's own
// grants answer, with no member involved.
func TestClient_ForRole(t *testing.T) {
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
	if err := manager.AddRolePermissionResources(ctx, tenant1.PolicyScope(), "Editor", "Read", "employees"); err != nil {
		t.Fatalf("AddRolePermissionResources() error = %v", err)
	}

	tests := []struct {
		name      string
		role      accesstypes.Role
		perm      accesstypes.Permission
		resources []accesstypes.Resource
		want      accesstypes.Decisions
	}{
		{
			name:      "bound role's grant is granted",
			role:      "Editor",
			perm:      "Read",
			resources: []accesstypes.Resource{"employees"},
			want:      accesstypes.Decisions{"employees": accesstypes.Granted()},
		},
		{
			name:      "unheld permission fails closed to denied",
			role:      "Editor",
			perm:      "Delete",
			resources: []accesstypes.Resource{"employees"},
			want:      accesstypes.Decisions{"employees": accesstypes.Denied()},
		},
		{
			name:      "unknown bound role fails closed to denied",
			role:      "Ghost",
			perm:      "Read",
			resources: []accesstypes.Resource{"employees"},
			want:      accesstypes.Decisions{"employees": accesstypes.Denied()},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			checker := client.ForRole(tt.role)
			if got := checker.Role(); got != tt.role {
				t.Errorf("RoleChecker.Role() = %v, want %v", got, tt.role)
			}

			got, err := checker.Check(t.Context(), accesstypes.NewEnvironment(), tenant1, tt.perm, tt.resources...)
			if err != nil {
				t.Fatalf("RoleChecker.Check() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got, cmp.AllowUnexported(accesstypes.Decision{}, accesstypes.Condition{})); diff != "" {
				t.Errorf("RoleChecker.Check() (-want +got):\n%s", diff)
			}
		})
	}
}

// TestRoleChecker_PermissionDigest pins the digest delegate: the bound role's
// structural enumeration flows through ForRole unchanged — granted for an
// unconditional grant, conditional for a condition-limited one, absence for
// everything else, with nothing folded.
func TestRoleChecker_PermissionDigest(t *testing.T) {
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
	if err := manager.AddRoleGrant(ctx, tenant1.PolicyScope(), "Editor", "Update", "employees.name", "status = 'open'"); err != nil {
		t.Fatalf("AddRoleGrant() error = %v", err)
	}

	tests := []struct {
		name  string
		role  accesstypes.Role
		scope accesstypes.Scope
		want  accesstypes.PermissionDigest
	}{
		{
			name:  "bound role's grants enumerate structurally",
			role:  "Editor",
			scope: tenant1,
			want: accesstypes.PermissionDigest{
				"employees":      {"Read": accesstypes.DigestGranted},
				"employees.name": {"Read": accesstypes.DigestGranted, "Update": accesstypes.DigestConditional},
			},
		},
		{
			name:  "unknown bound role gets an empty digest",
			role:  "Ghost",
			scope: tenant1,
			want:  accesstypes.PermissionDigest{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := client.ForRole(tt.role).PermissionDigest(t.Context(), tt.scope)
			if err != nil {
				t.Fatalf("RoleChecker.PermissionDigest() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("RoleChecker.PermissionDigest() (-want +got):\n%s", diff)
			}
		})
	}
}

// TestRoleChecker_HasGrants pins the foothold delegate for a session
// operating as a role: the bound role has a foothold exactly where its grants
// reach, and none in a scope it holds nothing in or as an unknown role.
func TestRoleChecker_HasGrants(t *testing.T) {
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
	tenant2 := accesstypes.DomainScope("tenant2")
	global := accesstypes.GlobalScope()
	manager := client.UserManager()
	for _, seed := range []struct {
		scope accesstypes.Scope
		role  accesstypes.Role
		perm  accesstypes.Permission
		res   accesstypes.Resource
	}{
		{scope: tenant2, role: "Editor", perm: "Read", res: "employees"},
		{scope: tenant1, role: "Editor", perm: "Read", res: "employees"},
		{scope: global, role: "Auditor", perm: "List", res: "reports"},
	} {
		if err := manager.AddRole(ctx, seed.scope.PolicyScope(), seed.role, aDescription); err != nil {
			t.Fatalf("AddRole() error = %v", err)
		}
		if err := manager.AddRolePermissionResources(ctx, seed.scope.PolicyScope(), seed.role, seed.perm, seed.res); err != nil {
			t.Fatalf("AddRolePermissionResources() error = %v", err)
		}
	}

	tests := []struct {
		name  string
		role  accesstypes.Role
		scope accesstypes.Scope
		want  bool
	}{
		{name: "bound role's grant is a foothold", role: "Editor", scope: tenant1, want: true},
		{name: "a foothold in each tenant it is granted in", role: "Editor", scope: tenant2, want: true},
		{name: "a global role has a foothold in the global partition", role: "Auditor", scope: global, want: true},
		{name: "a global role has none in a tenant", role: "Auditor", scope: tenant1, want: false},
		{name: "a tenant role has none in the global partition", role: "Editor", scope: global, want: false},
		{name: "an unknown bound role has none", role: "Ghost", scope: tenant1, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := client.ForRole(tt.role).HasGrants(t.Context(), tt.scope)
			if err != nil {
				t.Fatalf("RoleChecker.HasGrants() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("RoleChecker.HasGrants(%s) = %v, want %v", tt.scope, got, tt.want)
			}
		})
	}
}
