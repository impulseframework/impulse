package access

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
)

// TestClient_ForUser pins the user-context seam: ForUser binds exactly the
// user, and Check is a pure delegate to CheckUserResources — the same grants
// answer both, from the bound user's point of view.
func TestClient_ForUser(t *testing.T) {
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
	if err := manager.AddRoleUsers(ctx, tenant1.PolicyScope(), "Editor", "erin"); err != nil {
		t.Fatalf("AddRoleUsers() error = %v", err)
	}

	tests := []struct {
		name      string
		user      accesstypes.User
		perm      accesstypes.Permission
		resources []accesstypes.Resource
		want      accesstypes.Decisions
	}{
		{
			name:      "bound user's grant is granted",
			user:      "erin",
			perm:      "Read",
			resources: []accesstypes.Resource{"employees"},
			want:      accesstypes.Decisions{"employees": accesstypes.Granted()},
		},
		{
			name:      "unheld permission fails closed to denied",
			user:      "erin",
			perm:      "Delete",
			resources: []accesstypes.Resource{"employees"},
			want:      accesstypes.Decisions{"employees": accesstypes.Denied()},
		},
		{
			name:      "unknown bound user fails closed to denied",
			user:      "sam",
			perm:      "Read",
			resources: []accesstypes.Resource{"employees"},
			want:      accesstypes.Decisions{"employees": accesstypes.Denied()},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			checker := client.ForUser(tt.user)
			if got := checker.User(); got != tt.user {
				t.Errorf("UserChecker.User() = %v, want %v", got, tt.user)
			}

			got, err := checker.Check(t.Context(), accesstypes.NewEnvironment(), tenant1, tt.perm, tt.resources...)
			if err != nil {
				t.Fatalf("UserChecker.Check() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got, cmp.AllowUnexported(accesstypes.Decision{}, accesstypes.Condition{})); diff != "" {
				t.Errorf("UserChecker.Check() (-want +got):\n%s", diff)
			}
		})
	}
}

// TestUserChecker_PermissionDigest pins the digest delegate: the bound user's
// structural enumeration flows through ForUser unchanged — granted for an
// unconditional grant, conditional for a condition-limited one, absence for
// everything else, with nothing folded.
func TestUserChecker_PermissionDigest(t *testing.T) {
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
	if err := manager.AddRoleUsers(ctx, tenant1.PolicyScope(), "Editor", "erin"); err != nil {
		t.Fatalf("AddRoleUsers() error = %v", err)
	}

	tests := []struct {
		name  string
		user  accesstypes.User
		scope accesstypes.Scope
		want  accesstypes.PermissionDigest
	}{
		{
			name:  "bound user's grants enumerate structurally",
			user:  "erin",
			scope: tenant1,
			want: accesstypes.PermissionDigest{
				"employees":      {"Read": accesstypes.DigestGranted},
				"employees.name": {"Read": accesstypes.DigestGranted, "Update": accesstypes.DigestConditional},
			},
		},
		{
			name:  "unknown bound user gets an empty digest",
			user:  "sam",
			scope: tenant1,
			want:  accesstypes.PermissionDigest{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := client.ForUser(tt.user).PermissionDigest(t.Context(), tt.scope)
			if err != nil {
				t.Fatalf("UserChecker.PermissionDigest() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("UserChecker.PermissionDigest() (-want +got):\n%s", diff)
			}
		})
	}
}

// TestUserChecker_HasGrants pins the foothold delegate: the bound user has a
// foothold exactly where a grant reaches them — through a role, in the
// global partition too — and none in a scope their roles resolve to nothing
// in, or an unknown scope.
func TestUserChecker_HasGrants(t *testing.T) {
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
		users []accesstypes.User
	}{
		{scope: tenant2, role: "Editor", perm: "Read", res: "employees", users: []accesstypes.User{"erin"}},
		{scope: tenant1, role: "Chief", perm: "Read", res: "employees", users: []accesstypes.User{"erin"}},
		{scope: global, role: "Auditor", perm: "List", res: "reports", users: []accesstypes.User{"erin", "hana"}},
	} {
		if err := manager.AddRole(ctx, seed.scope.PolicyScope(), seed.role, aDescription); err != nil {
			t.Fatalf("AddRole() error = %v", err)
		}
		if err := manager.AddRolePermissionResources(ctx, seed.scope.PolicyScope(), seed.role, seed.perm, seed.res); err != nil {
			t.Fatalf("AddRolePermissionResources() error = %v", err)
		}
		if err := manager.AddRoleUsers(ctx, seed.scope.PolicyScope(), seed.role, seed.users...); err != nil {
			t.Fatalf("AddRoleUsers() error = %v", err)
		}
	}
	if err := manager.AddRole(ctx, tenant1.PolicyScope(), "Idler", aDescription); err != nil {
		t.Fatalf("AddRole() error = %v", err)
	}
	if err := manager.AddRoleUsers(ctx, tenant1.PolicyScope(), "Idler", "gale"); err != nil {
		t.Fatalf("AddRoleUsers() error = %v", err)
	}

	tests := []struct {
		name  string
		user  accesstypes.User
		scope accesstypes.Scope
		want  bool
	}{
		{name: "a role grant is a foothold", user: "erin", scope: tenant1, want: true},
		{name: "a foothold in another tenant", user: "erin", scope: tenant2, want: true},
		{name: "a global grant is a foothold in the global partition", user: "hana", scope: global, want: true},
		{name: "a global grant is no foothold in a tenant", user: "hana", scope: tenant1, want: false},
		{name: "membership in a grantless role is not", user: "gale", scope: tenant1, want: false},
		{name: "an unknown tenant has no footholds", user: "erin", scope: accesstypes.DomainScope("tenant9"), want: false},
		{name: "an unknown user has none", user: "nobody", scope: tenant1, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := client.ForUser(tt.user).HasGrants(t.Context(), tt.scope)
			if err != nil {
				t.Fatalf("UserChecker.HasGrants() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("UserChecker.HasGrants(%s) = %v, want %v", tt.scope, got, tt.want)
			}
		})
	}
}
