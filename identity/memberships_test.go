package identity

import (
	"context"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
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

// TestUser pins how the permission store names a person: by the id of their account, as
// text, which reads back as the same id; a user that is no id (a username, the name of a
// session's actor from another application, nothing at all) names no account.
func TestUser(t *testing.T) {
	t.Parallel()

	if got, want := User(userID), accesstypes.User("11111111-1111-4111-8111-111111111111"); got != want {
		t.Errorf("User(%s) = %q, want the id as text %q", userID, got, want)
	}
	for _, id := range []impulse.UUID{userID, otherUser, createdID} {
		got, ok := UserID(User(id))
		if !ok || got != id {
			t.Errorf("UserID(User(%s)) = %s, %t; want the same id, true", id, got, ok)
		}
	}
	if User(userID) == User(otherUser) {
		t.Error("two accounts are named the same")
	}

	for _, user := range []accesstypes.User{"dana", "dana@example.com", "", "11111111-1111-4111-8111", "role:Auditor"} {
		got, ok := UserID(user)
		if ok || got != impulse.NilUUID {
			t.Errorf("UserID(%q) = %s, %t; want the nil id, false: it is no account's id", user, got, ok)
		}
	}
}

// TestManager_AssignRole pins the membership write: the permission engine's rule first,
// a membership already held left as it is, the row buffered under the id of the
// person's account, and the engine told once the transaction has committed and never
// when it has not.
func TestManager_AssignRole(t *testing.T) {
	t.Parallel()

	held := access.NewMembershipKey(globalScope, dana, "Steward")
	id := userID.String()

	tests := []struct {
		name   string
		scope  accesstypes.PolicyScope
		role   accesstypes.Role
		held   []access.MembershipKey
		custom []access.RoleKey
		failOn string
		// rollBack fails the transaction after the call returned.
		rollBack    bool
		wantErr     func(error) bool
		wantWrites  []string
		wantReads   []string
		wantChanged int
	}{
		{
			name:  "a global role",
			scope: globalScope, role: "Steward",
			wantWrites:  []string{"insert membership global/ " + id + " Steward"},
			wantChanged: 1,
		},
		{
			name:  "a role in one domain",
			scope: alphaScope, role: "Reader",
			wantWrites:  []string{"insert membership domain/alpha " + id + " Reader"},
			wantChanged: 1,
		},
		{
			name:  "a role in every domain",
			scope: everyScope, role: "Reader",
			wantWrites:  []string{"insert membership every/ " + id + " Reader"},
			wantChanged: 1,
		},
		{
			name:  "a membership already held is left as it is and the engine is not told",
			scope: globalScope, role: "Steward",
			held: []access.MembershipKey{held},
		},
		{
			name:  "a custom role is read from the roles table in the transaction",
			scope: alphaScope, role: "Auditor",
			custom:      []access.RoleKey{{Kind: "domain", Domain: "alpha", Role: "Auditor"}},
			wantReads:   []string{"role domain/alpha Auditor"},
			wantWrites:  []string{"insert membership domain/alpha " + id + " Auditor"},
			wantChanged: 1,
		},
		{
			name:  "a role neither the role file nor the roles table holds is refused before any membership is read",
			scope: alphaScope, role: "Steward",
			failOn:    "InsertMembership",
			wantReads: []string{"role domain/alpha Steward"},
			wantErr:   httpio.HasNotFound,
		},
		{
			name:  "a failed read of the roles table fails the call, and is no refusal of the role",
			scope: alphaScope, role: "Auditor",
			failOn:  "HasRole",
			wantErr: func(err error) bool { return errors.Is(err, errStore) && !httpio.HasNotFound(err) },
		},
		{
			name:  "a failed write fails the call and the engine is not told",
			scope: globalScope, role: "Steward",
			failOn:  "InsertMembership",
			wantErr: func(err error) bool { return errors.Is(err, errStore) },
		},
		{
			name:  "a transaction that does not commit tells the engine nothing",
			scope: globalScope, role: "Steward",
			rollBack:   true,
			wantErr:    func(err error) bool { return errors.Is(err, resource.ErrDryRun) },
			wantWrites: []string{"insert membership global/ " + id + " Steward"},
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
				if err := m.AssignRole(ctx, txn, tt.scope, userID, tt.role); err != nil {
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

// TestManager_AssignRole_nobody pins that a role is given to somebody: the nil id is
// refused before anything is read or written.
func TestManager_AssignRole_nobody(t *testing.T) {
	t.Parallel()

	store := &fakeStore{memberships: map[access.MembershipKey]bool{}, roles: map[access.RoleKey]bool{}}
	m, engine := newManager(t, store, engineRoles)
	err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		return m.AssignRole(ctx, txn, globalScope, impulse.NilUUID, "Steward")
	})
	if !httpio.HasBadRequest(err) {
		t.Fatalf("AssignRole(the nil id) error = %v, want a BadRequest", err)
	}
	if len(store.writes) != 0 || engine.changed != 0 {
		t.Errorf("writes = %v, the engine told %d time(s), want nothing", store.writes, engine.changed)
	}
}

// TestManager_RemoveRole pins the removal: the membership held under the id of the
// person's account, no rule on the role, a membership not held no error, and the engine
// told once the transaction has committed.
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
			wantWrites:  []string{"delete membership global/ " + userID.String() + " Steward"},
			wantChanged: 1,
		},
		{
			name: "a membership of a role that no longer exists is removed like any other",
			role: "Retired", held: true,
			wantWrites:  []string{"delete membership global/ " + userID.String() + " Retired"},
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
				store.memberships[access.NewMembershipKey(globalScope, dana, tt.role)] = true
			}
			m, engine := newManager(t, store, engineRoles)

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.RemoveRole(ctx, txn, globalScope, userID, tt.role)
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
		access.NewMembershipKey(everyScope, erin, "Reader"): true,
	}}
	m, engine := newManager(t, store, engineRoles)

	if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		if err := m.AssignRole(ctx, txn, globalScope, userID, "Steward"); err != nil {
			return err
		}
		if err := m.AssignRole(ctx, txn, alphaScope, userID, "Reader"); err != nil {
			return err
		}

		return m.RemoveRole(ctx, txn, everyScope, otherUser, "Reader")
	}); err != nil {
		t.Fatalf("the transaction failed: %v", err)
	}
	want := []string{
		"insert membership global/ " + userID.String() + " Steward",
		"insert membership domain/alpha " + userID.String() + " Reader",
		"delete membership every/ " + otherUser.String() + " Reader",
	}
	if diff := cmp.Diff(want, store.writes); diff != "" {
		t.Errorf("buffered writes (-want +got):\n%s", diff)
	}
	if engine.changed != 1 {
		t.Errorf("the engine was told the policy changed %d time(s), want once", engine.changed)
	}
}
