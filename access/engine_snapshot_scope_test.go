package access

import (
	"context"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
)

// everyTenantEditorStore returns a fakeStore in which Editor holds Read on
// employees in every tenant and erin is an Editor in every tenant: before the
// zero scope was refused, a check asked in it fell to these grants and was
// answered Granted.
func everyTenantEditorStore(t *testing.T) *fakeStore {
	t.Helper()
	ctx := context.Background()
	store := newFakeStore()
	for _, err := range []error{
		store.InsertRole(ctx, everyDomainPolicy, "Editor", aDescription),
		store.InsertGrant(ctx, everyDomainPolicy, "Editor", "Read", "employees", "", ""),
		store.InsertUserRole(ctx, everyDomainPolicy, "erin", "Editor", accesstypes.Period{}, ""),
	} {
		if err != nil {
			t.Fatalf("seeding fake store: %v", err)
		}
	}

	return store
}

// scopedEngineCalls asks each of the engine's ten scoped methods about erin,
// or the Editor role, and Read on employees, in scope, and returns each one's
// error by the method's name.
func scopedEngineCalls(e *snapshotEngine, scope accesstypes.Scope) map[string]error {
	ctx := context.Background()
	env := accesstypes.NewEnvironment()
	errs := make(map[string]error, 10)
	_, errs["checkUser"] = e.checkUser(ctx, env, "erin", scope, "Read")
	_, errs["checkUserResources"] = e.checkUserResources(ctx, env, "erin", scope, "Read", "employees")
	_, errs["userDigest"] = e.userDigest(ctx, "erin", scope)
	_, errs["userHasGrants"] = e.userHasGrants(ctx, "erin", scope)
	_, errs["userPermissions"] = e.userPermissions(ctx, "erin", scope)
	_, errs["checkRole"] = e.checkRole(ctx, "Editor", scope, "Read")
	_, errs["checkRoleResources"] = e.checkRoleResources(ctx, "Editor", scope, "Read", "employees")
	_, errs["roleDigest"] = e.roleDigest(ctx, "Editor", scope)
	_, errs["roleHasGrants"] = e.roleHasGrants(ctx, "Editor", scope)
	_, errs["rolePermissions"] = e.rolePermissions(ctx, "Editor", scope)

	return errs
}

func Test_snapshotEngine_refusesTheZeroScope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		scope     accesstypes.Scope
		storeDown bool
	}{
		{name: "the zero Scope", scope: accesstypes.Scope{}},
		{name: "the scope of an empty tenant", scope: accesstypes.DomainScope("")},
		{name: "the zero Scope with the store down", scope: accesstypes.Scope{}, storeDown: true},
		{name: "the scope of an empty tenant with the store down", scope: accesstypes.DomainScope(""), storeDown: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := everyTenantEditorStore(t)
			if tt.storeDown {
				store.setFail(errors.New("store down"))
			}
			e := testEngine(t, store, nil)

			errs := scopedEngineCalls(e, tt.scope)
			if len(errs) != 10 {
				t.Fatalf("asked %d methods, want the ten that take a scope", len(errs))
			}
			for method, err := range errs {
				if !errors.Is(err, accesstypes.ErrNoScope) {
					t.Errorf("%s() error = %v, want accesstypes.ErrNoScope", method, err)
				}
			}
			if !tt.storeDown {
				stillGrantsInATenant(t, e)
			}
		})
	}
}

// stillGrantsInATenant checks that the engine that refused the zero scope
// answers every scoped method in tenant1, and grants erin and the Editor role
// Read on employees there.
func stillGrantsInATenant(t *testing.T, e *snapshotEngine) {
	t.Helper()
	ctx := context.Background()
	for method, err := range scopedEngineCalls(e, tenant1Scope) {
		if err != nil {
			t.Errorf("%s() in tenant1 error = %v, want an answer", method, err)
		}
	}

	users, err := e.checkUserResources(ctx, accesstypes.NewEnvironment(), "erin", tenant1Scope, "Read", "employees")
	if err != nil {
		t.Fatalf("checkUserResources() in tenant1 error = %v", err)
	}
	if len(users) != 1 || !users[0].granted {
		t.Errorf("checkUserResources() in tenant1 = %v, want employees granted", users)
	}
	roles, err := e.checkRoleResources(ctx, "Editor", tenant1Scope, "Read", "employees")
	if err != nil {
		t.Fatalf("checkRoleResources() in tenant1 error = %v", err)
	}
	if len(roles) != 1 || !roles[0].granted {
		t.Errorf("checkRoleResources() in tenant1 = %v, want employees granted", roles)
	}
}
