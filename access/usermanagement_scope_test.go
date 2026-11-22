package access

import (
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

// Test_userManager_RolePermissions_refusesTheZeroScope pins that a role's
// permissions asked in no scope are refused before the role is looked for: the
// answer is accesstypes.ErrNoScope, not that the role does not exist, and not the
// store's error when the store is down.
func Test_userManager_RolePermissions_refusesTheZeroScope(t *testing.T) {
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
			m, store := seededManager(t)
			if tt.storeDown {
				store.setFail(errors.New("store down"))
			}

			_, err := m.RolePermissions(t.Context(), tt.scope, "Ghost")
			if !errors.Is(err, accesstypes.ErrNoScope) {
				t.Errorf("RolePermissions() error = %v, want accesstypes.ErrNoScope", err)
			}
		})
	}
}

// Test_userManager_RolePermissions_roleNotFoundInATenant pins the answer the
// refusal of no scope leaves alone: in a tenant's scope a role that does not exist
// is not found.
func Test_userManager_RolePermissions_roleNotFoundInATenant(t *testing.T) {
	t.Parallel()
	m, _ := seededManager(t)

	_, err := m.RolePermissions(t.Context(), tenant1Scope, "Ghost")
	if err == nil || !httpio.HasNotFound(err) {
		t.Fatalf("RolePermissions() error = %v, want the role not found", err)
	}
	if errors.Is(err, accesstypes.ErrNoScope) {
		t.Errorf("RolePermissions() error = %v, want the role not found, not accesstypes.ErrNoScope", err)
	}
}
