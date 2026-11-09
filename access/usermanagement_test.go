package access

import (
	"context"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

// seededManager returns a userManager over a fresh fake store, pre-seeded
// with roles, grants, and memberships through the manager's own write path.
func seededManager(t *testing.T) (*userManager, *fakeStore) {
	t.Helper()
	ctx := context.Background()

	store := newFakeStore()
	m := newUserManager(newStoreManager(store), nil, testEngine(t, store, nil))

	for _, seed := range []struct {
		scope accesstypes.PolicyScope
		role  accesstypes.Role
	}{
		{tenant1Policy, "Editor"},
		{tenant1Policy, "Viewer"},
		{tenant2Policy, "Viewer"},
		{accesstypes.GlobalPolicyScope(), "Auditor"},
	} {
		if err := m.AddRole(ctx, seed.scope, seed.role, aDescription); err != nil {
			t.Fatalf("AddRole(%q, %q) error = %v", seed.scope, seed.role, err)
		}
	}
	if err := m.AddRolePermissionResources(ctx, tenant1Policy, "Editor", "Read", "employees", "employees.name"); err != nil {
		t.Fatalf("AddRolePermissionResources() error = %v", err)
	}
	if err := m.AddRolePermission(ctx, accesstypes.GlobalPolicyScope(), "Auditor", "ViewUsers"); err != nil {
		t.Fatalf("AddRolePermissionResources() error = %v", err)
	}
	if err := m.AddRoleUsers(ctx, tenant1Policy, "Editor", "alice"); err != nil {
		t.Fatalf("AddRoleUsers() error = %v", err)
	}
	if err := m.AddUserRoles(ctx, tenant2Policy, "alice", "Viewer"); err != nil {
		t.Fatalf("AddUserRoles() error = %v", err)
	}

	return m, store
}

func Test_userManager_membership(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		op      func(ctx context.Context, m *userManager) error
		wantErr bool
		// verify runs after a successful op.
		verify func(ctx context.Context, t *testing.T, m *userManager)
	}{
		{
			name: "AddRoleUsers assigns members",
			op: func(ctx context.Context, m *userManager) error {
				return m.AddRoleUsers(ctx, tenant1Policy, "Editor", "bob", "carol")
			},
			verify: func(ctx context.Context, t *testing.T, m *userManager) {
				t.Helper()
				users, err := m.RoleUsers(ctx, tenant1Policy, "Editor")
				if err != nil {
					t.Fatalf("RoleUsers() error = %v", err)
				}
				if diff := cmp.Diff([]accesstypes.User{"alice", "bob", "carol"}, users); diff != "" {
					t.Errorf("RoleUsers() (-want +got):\n%s", diff)
				}
			},
		},
		{
			name: "AddRoleUsers rejects missing role",
			op: func(ctx context.Context, m *userManager) error {
				return m.AddRoleUsers(ctx, tenant1Policy, "Ghost", "bob")
			},
			wantErr: true,
		},
		{
			name: "AddRoleUsers is scope-scoped on role existence",
			op: func(ctx context.Context, m *userManager) error {
				return m.AddRoleUsers(ctx, tenant2Policy, "Editor", "bob") // Editor exists only in tenant1
			},
			wantErr: true,
		},
		{
			name: "AddRoleUsers rejects empty user",
			op: func(ctx context.Context, m *userManager) error {
				return m.AddRoleUsers(ctx, tenant1Policy, "Editor", "")
			},
			wantErr: true,
		},
		{
			name: "AddUserRoles assigns roles",
			op: func(ctx context.Context, m *userManager) error {
				return m.AddUserRoles(ctx, tenant1Policy, "bob", "Editor", "Viewer")
			},
			verify: func(ctx context.Context, t *testing.T, m *userManager) {
				t.Helper()
				roles, err := m.UserRoles(ctx, "bob", tenant1Policy)
				if err != nil {
					t.Fatalf("UserRoles() error = %v", err)
				}
				want := accesstypes.RoleCollection{tenant1Policy: {"Editor", "Viewer"}}
				if diff := cmp.Diff(want, roles); diff != "" {
					t.Errorf("UserRoles() (-want +got):\n%s", diff)
				}
			},
		},
		{
			name: "AddUserRoles rejects any missing role before writing",
			op: func(ctx context.Context, m *userManager) error {
				return m.AddUserRoles(ctx, tenant1Policy, "bob", "Editor", "Ghost")
			},
			wantErr: true,
		},
		{
			name: "AddUserRoles rejects empty user",
			op: func(ctx context.Context, m *userManager) error {
				return m.AddUserRoles(ctx, tenant1Policy, "", "Editor")
			},
			wantErr: true,
		},
		{
			name: "DeleteRoleUsers removes membership",
			op: func(ctx context.Context, m *userManager) error {
				return m.DeleteRoleUsers(ctx, tenant1Policy, "Editor", "alice")
			},
			verify: func(ctx context.Context, t *testing.T, m *userManager) {
				t.Helper()
				users, err := m.RoleUsers(ctx, tenant1Policy, "Editor")
				if err != nil {
					t.Fatalf("RoleUsers() error = %v", err)
				}
				if len(users) != 0 {
					t.Errorf("RoleUsers() = %v, want empty", users)
				}
			},
		},
		{
			name: "DeleteRoleUsers rejects missing role",
			op: func(ctx context.Context, m *userManager) error {
				return m.DeleteRoleUsers(ctx, tenant1Policy, "Ghost", "alice")
			},
			wantErr: true,
		},
		{
			name: "DeleteUserRoles succeeds for roles never held",
			op: func(ctx context.Context, m *userManager) error {
				return m.DeleteUserRoles(ctx, tenant1Policy, "alice", "Viewer")
			},
			verify: func(ctx context.Context, t *testing.T, m *userManager) {
				t.Helper()
				roles, err := m.UserRoles(ctx, "alice", tenant1Policy)
				if err != nil {
					t.Fatalf("UserRoles() error = %v", err)
				}
				want := accesstypes.RoleCollection{tenant1Policy: {"Editor"}}
				if diff := cmp.Diff(want, roles); diff != "" {
					t.Errorf("UserRoles() (-want +got):\n%s", diff)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			m, _ := seededManager(t)

			err := tt.op(ctx, m)
			if (err != nil) != tt.wantErr {
				t.Fatalf("op error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && tt.verify != nil {
				tt.verify(ctx, t, m)
			}
		})
	}
}

func Test_userManager_UserRoles_UserPermissions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		user accesstypes.User
		// scopes are where memberships are held, asked of UserRoles;
		// requestScopes are where a request is, asked of UserPermissions.
		scopes        []accesstypes.PolicyScope
		requestScopes []accesstypes.Scope
		wantErr       bool
		wantRoles     accesstypes.RoleCollection
		wantPerms     accesstypes.UserPermissionCollection
	}{
		{
			name:          "multi-scope collection",
			user:          "alice",
			scopes:        []accesstypes.PolicyScope{tenant1Policy, tenant2Policy},
			requestScopes: []accesstypes.Scope{tenant1Scope, tenant2Scope},
			wantRoles: accesstypes.RoleCollection{
				tenant1Policy: {"Editor"},
				tenant2Policy: {"Viewer"},
			},
			wantPerms: accesstypes.UserPermissionCollection{
				tenant1Scope: {Resources: map[accesstypes.Resource][]accesstypes.Permission{"employees": {"Read"}, "employees.name": {"Read"}}},
				tenant2Scope: {Resources: map[accesstypes.Resource][]accesstypes.Permission{}},
			},
		},
		{
			name:          "unknown tenant yields empty entries",
			user:          "alice",
			scopes:        []accesstypes.PolicyScope{accesstypes.DomainPolicyScope("no-such-tenant")},
			requestScopes: []accesstypes.Scope{accesstypes.DomainScope("no-such-tenant")},
			wantRoles: accesstypes.RoleCollection{
				accesstypes.DomainPolicyScope("no-such-tenant"): {},
			},
			wantPerms: accesstypes.UserPermissionCollection{
				accesstypes.DomainScope("no-such-tenant"): {Resources: map[accesstypes.Resource][]accesstypes.Permission{}},
			},
		},
		{
			name:   "no scopes lists every membership",
			user:   "alice",
			scopes: nil,
			wantRoles: accesstypes.RoleCollection{
				tenant1Policy: {"Editor"},
				tenant2Policy: {"Viewer"},
			},
		},
		{
			name:          "no request scopes is an error",
			user:          "alice",
			scopes:        []accesstypes.PolicyScope{tenant1Policy},
			requestScopes: nil,
			wantErr:       true,
			wantRoles:     accesstypes.RoleCollection{tenant1Policy: {"Editor"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			m, _ := seededManager(t)

			roles, err := m.UserRoles(ctx, tt.user, tt.scopes...)
			if err != nil {
				t.Fatalf("UserRoles() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantRoles, roles, cmpopts.EquateComparable(accesstypes.PolicyScope{})); diff != "" {
				t.Errorf("UserRoles() (-want +got):\n%s", diff)
			}
			if tt.wantPerms == nil && !tt.wantErr {
				return
			}
			perms, err := m.UserPermissions(ctx, tt.user, tt.requestScopes...)
			if (err != nil) != tt.wantErr {
				t.Fatalf("UserPermissions() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if diff := cmp.Diff(tt.wantPerms, perms, cmpopts.EquateComparable(accesstypes.Scope{})); diff != "" {
				t.Errorf("UserPermissions() (-want +got):\n%s", diff)
			}
		})
	}
}

func Test_userManager_roles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		op      func(ctx context.Context, m *userManager) error
		wantErr bool
		verify  func(ctx context.Context, t *testing.T, m *userManager)
	}{
		{
			name: "AddRole creates the role in its domain only",
			op: func(ctx context.Context, m *userManager) error {
				return m.AddRole(ctx, tenant2Policy, "Editor", aDescription)
			},
			verify: func(ctx context.Context, t *testing.T, m *userManager) {
				t.Helper()
				for scope, want := range map[accesstypes.PolicyScope][]RoleInfo{
					tenant1Policy: {{Name: "Editor", Description: aDescription}, {Name: "Viewer", Description: aDescription}},
					tenant2Policy: {{Name: "Editor", Description: aDescription}, {Name: "Viewer", Description: aDescription}},
				} {
					roles, err := m.Roles(ctx, scope)
					if err != nil {
						t.Fatalf("Roles(%q) error = %v", scope, err)
					}
					if diff := cmp.Diff(want, roles); diff != "" {
						t.Errorf("Roles(%q) (-want +got):\n%s", scope, diff)
					}
				}
			},
		},
		{
			name: "AddRole rejects duplicates",
			op: func(ctx context.Context, m *userManager) error {
				return m.AddRole(ctx, tenant1Policy, "Editor", aDescription)
			},
			wantErr: true,
		},
		{
			name: "AddRole rejects empty role",
			op: func(ctx context.Context, m *userManager) error {
				return m.AddRole(ctx, tenant1Policy, "", aDescription)
			},
			wantErr: true,
		},
		{
			name: "AddRole rejects a role without a description",
			op: func(ctx context.Context, m *userManager) error {
				return m.AddRole(ctx, tenant2Policy, "Editor", " \n")
			},
			wantErr: true,
		},
		{
			name: "DeleteRole refuses while users are assigned",
			op: func(ctx context.Context, m *userManager) error {
				_, err := m.DeleteRole(ctx, tenant1Policy, "Editor")

				return err
			},
			wantErr: true,
		},
		{
			name: "DeleteRole removes an unassigned role and its grants, scoped to the domain",
			op: func(ctx context.Context, m *userManager) error {
				if err := m.DeleteRoleUsers(ctx, tenant1Policy, "Editor", "alice"); err != nil {
					return err
				}
				deleted, err := m.DeleteRole(ctx, tenant1Policy, "Editor")
				if err != nil {
					return err
				}
				if !deleted {
					return errors.New("DeleteRole() reported nothing deleted")
				}

				return nil
			},
			verify: func(ctx context.Context, t *testing.T, m *userManager) {
				t.Helper()
				_, exists, err := m.ResolveRole(ctx, tenant1Policy, "Editor")
				if err != nil || exists {
					t.Errorf("ResolveRole() after delete = (%v, %v), want (false, nil)", exists, err)
				}
				// Viewer in tenant2 is untouched (delete is domain-scoped).
				if _, exists, err := m.ResolveRole(ctx, tenant2Policy, "Viewer"); err != nil || !exists {
					t.Errorf("ResolveRole(tenant2, Viewer) = (%v, %v), want (true, nil)", exists, err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			m, _ := seededManager(t)

			err := tt.op(ctx, m)
			if (err != nil) != tt.wantErr {
				t.Fatalf("op error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && tt.verify != nil {
				tt.verify(ctx, t, m)
			}
		})
	}
}

func Test_userManager_permissions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		op      func(ctx context.Context, m *userManager) error
		wantErr bool
		// wantPerms, when set, is compared against RolePermissions for the
		// (scope, role) below after a successful op.
		scope     accesstypes.Scope
		role      accesstypes.Role
		wantPerms accesstypes.RolePermissionCollection
	}{
		{
			// A scope-wide permission is a resource-less grant through the
			// dedicated method — there is no resource value that means it.
			name: "scope-wide permission via AddRolePermission",
			op: func(ctx context.Context, m *userManager) error {
				return m.AddRolePermission(ctx, tenant1Policy, "Viewer", "ViewReports")
			},
			scope: tenant1Scope,
			role:  "Viewer",
			wantPerms: accesstypes.RolePermissionCollection{
				"ViewReports": {ScopeWide: true},
			},
		},
		{
			name: "AddRolePermission rejects missing role",
			op: func(ctx context.Context, m *userManager) error {
				return m.AddRolePermission(ctx, tenant1Policy, "Ghost", "ViewReports")
			},
			wantErr: true,
		},
		{
			name: "AddRolePermissionResources adds resource and field grants",
			op: func(ctx context.Context, m *userManager) error {
				return m.AddRolePermissionResources(ctx, tenant1Policy, "Viewer", "Read", "widgets", "widgets.*")
			},
			scope: tenant1Scope,
			role:  "Viewer",
			wantPerms: accesstypes.RolePermissionCollection{
				"Read": {Resources: []accesstypes.Resource{"widgets", "widgets.*"}},
			},
		},
		{
			name: "AddRolePermissionResources rejects missing role",
			op: func(ctx context.Context, m *userManager) error {
				return m.AddRolePermissionResources(ctx, tenant1Policy, "Ghost", "Read", "widgets")
			},
			wantErr: true,
		},
		{
			name: "AddRolePermissionResources rejects empty resource",
			op: func(ctx context.Context, m *userManager) error {
				return m.AddRolePermissionResources(ctx, tenant1Policy, "Viewer", "Read", "")
			},
			wantErr: true,
		},
		{
			name: "AddRolePermissionResources enforces the dot invariant",
			op: func(ctx context.Context, m *userManager) error {
				return m.AddRolePermissionResources(ctx, tenant1Policy, "Viewer", "Read", "a.b.c")
			},
			wantErr: true,
		},
		{
			name: "DeleteRolePermissionResources removes grants",
			op: func(ctx context.Context, m *userManager) error {
				return m.DeleteRolePermissionResources(ctx, tenant1Policy, "Editor", "Read", "employees.name")
			},
			scope: tenant1Scope,
			role:  "Editor",
			wantPerms: accesstypes.RolePermissionCollection{
				"Read": {Resources: []accesstypes.Resource{"employees"}},
			},
		},
		{
			name: "DeleteRolePermission removes the scope-wide permission",
			op: func(ctx context.Context, m *userManager) error {
				return m.DeleteRolePermission(ctx, accesstypes.GlobalPolicyScope(), "Auditor", "ViewUsers")
			},
			scope:     accesstypes.GlobalScope(),
			role:      "Auditor",
			wantPerms: accesstypes.RolePermissionCollection{},
		},
		{
			name: "DeleteAllRolePermissions clears scope-wide grants",
			op: func(ctx context.Context, m *userManager) error {
				return m.DeleteAllRolePermissions(ctx, accesstypes.GlobalPolicyScope(), "Auditor")
			},
			scope:     accesstypes.GlobalScope(),
			role:      "Auditor",
			wantPerms: accesstypes.RolePermissionCollection{},
		},
		{
			// Regression: the casbin-era implementation only removed each
			// permission's global-resource grant, contradicting the method's
			// contract — resource and field grants silently survived.
			name: "DeleteAllRolePermissions clears resource-specific grants too",
			op: func(ctx context.Context, m *userManager) error {
				return m.DeleteAllRolePermissions(ctx, tenant1Policy, "Editor")
			},
			scope:     tenant1Scope,
			role:      "Editor",
			wantPerms: accesstypes.RolePermissionCollection{},
		},
		{
			name: "DeleteRolePermissionResources rejects missing role",
			op: func(ctx context.Context, m *userManager) error {
				return m.DeleteRolePermissionResources(ctx, tenant1Policy, "Ghost", "Read", "employees")
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			m, _ := seededManager(t)

			err := tt.op(ctx, m)
			if (err != nil) != tt.wantErr {
				t.Fatalf("op error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil || tt.wantPerms == nil {
				return
			}
			got, err := m.RolePermissions(ctx, tt.scope, tt.role)
			if err != nil {
				t.Fatalf("RolePermissions() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantPerms, got); diff != "" {
				t.Errorf("RolePermissions() (-want +got):\n%s", diff)
			}
		})
	}
}

func Test_userManager_RolePermissions_missingRole(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	m, _ := seededManager(t)
	if _, err := m.RolePermissions(ctx, tenant1Scope, "Ghost"); err == nil {
		t.Fatal("RolePermissions() expected error for missing role, got nil")
	}
}

// Test_userManager_storeErrorsPropagate pins the honest error path: a store
// failure surfaces as an error instead of reading as "role missing".
func Test_userManager_storeErrorsPropagate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	m, store := seededManager(t)
	store.setFail(errors.New("store down"))

	if _, _, err := m.ResolveRole(ctx, tenant1Policy, "Editor"); err == nil {
		t.Error("ResolveRole() expected error, got nil")
	}
	if err := m.AddRoleUsers(ctx, tenant1Policy, "Editor", "bob"); err == nil {
		t.Error("AddRoleUsers() expected error, got nil")
	}
	if _, err := m.UserRoles(ctx, "alice", tenant1Policy); err == nil {
		t.Error("UserRoles() expected error, got nil")
	}
}

// Test_userManager_AddRoleGrants pins the management surface of the bulk write:
// the role must exist, every row must name a resource, and the rows reach the
// store together.
func Test_userManager_AddRoleGrants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		role       accesstypes.Role
		rows       []GrantRow
		wantErr    bool
		wantStored []fakeGrant
	}{
		{
			name: "rows land on an existing role",
			role: "Viewer",
			rows: []GrantRow{{Permission: "Read", Resource: "widgets"}, {Permission: "Read", Resource: "widgets.name", Condition: "region = 'west'"}},
			wantStored: []fakeGrant{
				{scope: tenant1Policy, role: "Viewer", perm: "Read", resource: "widgets"},
				{scope: tenant1Policy, role: "Viewer", perm: "Read", resource: "widgets", field: "name", condition: "region = 'west'"},
			},
		},
		{name: "a missing role is refused", role: "Ghost", rows: []GrantRow{{Permission: "Read", Resource: "widgets"}}, wantErr: true},
		{name: "an empty resource is refused", role: "Viewer", rows: []GrantRow{{Permission: "Read", Resource: "widgets"}, {Permission: "Read"}}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			m, store := seededManager(t)
			writesBefore := store.batchWrites

			err := m.AddRoleGrants(ctx, tenant1Policy, tt.role, tt.rows...)
			if (err != nil) != tt.wantErr {
				t.Fatalf("AddRoleGrants() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				if store.batchWrites != writesBefore {
					t.Errorf("AddRoleGrants() wrote on a refused call")
				}

				return
			}
			if store.batchWrites != writesBefore+1 {
				t.Errorf("InsertGrants called %d times, want 1", store.batchWrites-writesBefore)
			}
			for _, want := range tt.wantStored {
				if _, ok := store.grants[want]; !ok {
					t.Errorf("AddRoleGrants() did not store %v", want)
				}
			}
		})
	}
}

// aDescription is the description the tests create custom roles with where
// the text does not matter.
const aDescription = "A custom role the tests create."

// Test_userManager_AddRole_nameGrammar pins the words a custom role's name
// outside the grammar is refused with: the role and the rule, the same
// sentence the role file is refused with, as a bad request.
func Test_userManager_AddRole_nameGrammar(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	m, store := seededManager(t)
	rolesBefore := len(store.roles)

	err := m.AddRole(ctx, tenant1Policy, "Tenant_Administrator", aDescription)
	if !httpio.HasBadRequest(err) {
		t.Fatalf("AddRole() error = %v, want a bad request", err)
	}
	want := "role Tenant_Administrator: a role name is letters and digits starting with a capital letter, such as Administrator or TenantAdministrator; the scope is never part of the name"
	if got := httpio.Message(err); got != want {
		t.Errorf("AddRole() message = %q, want %q", got, want)
	}
	if len(store.roles) != rolesBefore {
		t.Errorf("AddRole() wrote a role row on a refused call")
	}
}
