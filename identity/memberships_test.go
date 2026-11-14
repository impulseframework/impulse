package identity

import (
	"context"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
)

var (
	globalScope = accesstypes.GlobalPolicyScope()
	alphaScope  = accesstypes.DomainPolicyScope("alpha")
	everyScope  = accesstypes.EveryDomainPolicyScope()

	// The roles the fake engine holds: a global one, and a domain one held in alpha and
	// in every domain.
	engineRoles = map[accesstypes.PolicyScope][]accesstypes.Role{
		globalScope: {"Steward"},
		alphaScope:  {"Reader"},
		everyScope:  {"Reader"},
	}
)

// TestManager_AssignRole pins the membership write: the permission engine's rule first,
// a membership already held left as it is, the row buffered, and the engine told once
// the transaction has committed and never when it has not.
func TestManager_AssignRole(t *testing.T) {
	t.Parallel()

	held := access.NewMembershipKey(globalScope, "dana", "Steward")

	tests := []struct {
		name     string
		scope    accesstypes.PolicyScope
		username string
		role     accesstypes.Role
		held     []access.MembershipKey
		custom   []access.RoleKey
		failOn   string
		// rollBack fails the transaction after the call returned.
		rollBack    bool
		wantErr     func(error) bool
		wantWrites  []string
		wantReads   []string
		wantChanged int
	}{
		{
			name:  "a global role",
			scope: globalScope, username: "dana", role: "Steward",
			wantWrites:  []string{"insert membership global/ dana Steward"},
			wantChanged: 1,
		},
		{
			name:  "a role in one domain",
			scope: alphaScope, username: "dana", role: "Reader",
			wantWrites:  []string{"insert membership domain/alpha dana Reader"},
			wantChanged: 1,
		},
		{
			name:  "a role in every domain",
			scope: everyScope, username: "dana", role: "Reader",
			wantWrites:  []string{"insert membership every/ dana Reader"},
			wantChanged: 1,
		},
		{
			name:  "a membership already held is left as it is and the engine is not told",
			scope: globalScope, username: "dana", role: "Steward",
			held: []access.MembershipKey{held},
		},
		{
			name:  "a custom role is read from the roles table in the transaction",
			scope: alphaScope, username: "dana", role: "Auditor",
			custom:      []access.RoleKey{{Kind: "domain", Domain: "alpha", Role: "Auditor"}},
			wantReads:   []string{"role domain/alpha Auditor"},
			wantWrites:  []string{"insert membership domain/alpha dana Auditor"},
			wantChanged: 1,
		},
		{
			name:  "a role neither the role file nor the roles table holds is refused before any membership is read",
			scope: alphaScope, username: "dana", role: "Steward",
			failOn:    "HasMembership",
			wantReads: []string{"role domain/alpha Steward"},
			wantErr:   httpio.HasNotFound,
		},
		{
			name:  "a failed read of the roles table fails the call, and is no refusal of the role",
			scope: alphaScope, username: "dana", role: "Auditor",
			failOn:  "HasRole",
			wantErr: func(err error) bool { return errors.Is(err, errStore) && !httpio.HasNotFound(err) },
		},
		{
			name:  "an empty username is refused",
			scope: globalScope, username: "", role: "Steward",
			wantErr: httpio.HasBadRequest,
		},
		{
			name:  "a failed write fails the call and the engine is not told",
			scope: globalScope, username: "dana", role: "Steward",
			failOn:  "InsertMembership",
			wantErr: func(err error) bool { return errors.Is(err, errStore) },
		},
		{
			name:  "a transaction that does not commit tells the engine nothing",
			scope: globalScope, username: "dana", role: "Steward",
			rollBack:   true,
			wantErr:    func(err error) bool { return errors.Is(err, resource.ErrDryRun) },
			wantWrites: []string{"insert membership global/ dana Steward"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{memberships: map[access.MembershipKey]bool{}, roles: map[access.RoleKey]bool{}, failOn: tt.failOn}
			for _, key := range tt.held {
				store.memberships[key] = true
			}
			for _, key := range tt.custom {
				store.roles[key] = true
			}
			m, engine := newManager(t, store, engineRoles)

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				if err := m.AssignRole(ctx, txn, tt.scope, tt.username, tt.role); err != nil {
					return err
				}
				if engine.changed != 0 {
					t.Errorf("the engine was told %d time(s) before the commit", engine.changed)
				}
				if tt.rollBack {
					return resource.ErrDryRun
				}

				return nil
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("AssignRole() error = %v, want the refusal", err)
				}
			} else if err != nil {
				t.Fatalf("AssignRole() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantReads, store.reads); diff != "" {
				t.Errorf("reads of the roles table (-want +got):\n%s", diff)
			}
			if engine.changed != tt.wantChanged {
				t.Errorf("the engine was told the policy changed %d time(s), want %d", engine.changed, tt.wantChanged)
			}
		})
	}
}

// TestManager_RemoveRole pins the removal: no rule on the role, a membership not held no
// error, and the engine told once the transaction has committed.
func TestManager_RemoveRole(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		role        accesstypes.Role
		held        bool
		failOn      string
		wantErr     bool
		wantWrites  []string
		wantChanged int
	}{
		{
			name: "a membership held is removed",
			role: "Steward", held: true,
			wantWrites:  []string{"delete membership global/ dana Steward"},
			wantChanged: 1,
		},
		{
			name: "a membership of a role that no longer exists is removed like any other",
			role: "Retired", held: true,
			wantWrites:  []string{"delete membership global/ dana Retired"},
			wantChanged: 1,
		},
		{
			name: "a membership not held is no error and the engine is not told",
			role: "Steward",
		},
		{
			name: "a failed write fails the call",
			role: "Steward", held: true,
			failOn:  "DeleteMembership",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{memberships: map[access.MembershipKey]bool{}, failOn: tt.failOn}
			if tt.held {
				store.memberships[access.NewMembershipKey(globalScope, "dana", tt.role)] = true
			}
			m, engine := newManager(t, store, engineRoles)

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.RemoveRole(ctx, txn, globalScope, "dana", tt.role)
			})
			if (err != nil) != tt.wantErr {
				t.Fatalf("RemoveRole() error = %v, wantErr %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
			if engine.changed != tt.wantChanged {
				t.Errorf("the engine was told the policy changed %d time(s), want %d", engine.changed, tt.wantChanged)
			}
		})
	}
}

// TestManager_memberships_oneTransaction pins the word to the engine as owed once: a
// transaction that writes several memberships tells it a single time, after the commit.
func TestManager_memberships_oneTransaction(t *testing.T) {
	t.Parallel()

	store := &fakeStore{memberships: map[access.MembershipKey]bool{
		access.NewMembershipKey(everyScope, "erin", "Reader"): true,
	}}
	m, engine := newManager(t, store, engineRoles)

	if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		if err := m.AssignRole(ctx, txn, globalScope, "dana", "Steward"); err != nil {
			return err
		}
		if err := m.AssignRole(ctx, txn, alphaScope, "dana", "Reader"); err != nil {
			return err
		}

		return m.RemoveRole(ctx, txn, everyScope, "erin", "Reader")
	}); err != nil {
		t.Fatalf("the transaction failed: %v", err)
	}
	want := []string{
		"insert membership global/ dana Steward",
		"insert membership domain/alpha dana Reader",
		"delete membership every/ erin Reader",
	}
	if diff := cmp.Diff(want, store.writes); diff != "" {
		t.Errorf("buffered writes (-want +got):\n%s", diff)
	}
	if engine.changed != 1 {
		t.Errorf("the engine was told the policy changed %d time(s), want once", engine.changed)
	}
}
