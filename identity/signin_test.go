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
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// TestManager_giveInvitedRoles pins what an accepted invitation gives: its roles in its
// tenant, or, for an invitation into the application, each role where it is held; a role
// that can no longer be given fails the transaction, and a failed write is not taken
// for a role that does not exist.
func TestManager_giveInvitedRoles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		inv         *sessioninfo.Invitation
		held        []access.MembershipKey
		failOn      string
		wantErr     func(error) bool
		wantWrites  []string
		wantReads   []string
		wantChanged int
	}{
		{
			name:        "an invitation into a tenant gives its roles in that tenant",
			inv:         &sessioninfo.Invitation{Email: "dana@example.com", Tenant: "alpha", RoleNames: []string{"Reader"}},
			wantWrites:  []string{"insert membership domain/alpha dana Reader"},
			wantChanged: 1,
		},
		{
			name: "an invitation into the application gives a global role globally and a tenant's role in every tenant, and the engine is told once",
			inv:  &sessioninfo.Invitation{Email: "dana@example.com", RoleNames: []string{"Steward", "Reader"}},
			// Reader is asked for globally first, where the role file does not hold it, so the
			// roles table is read there before every tenant is tried.
			wantReads:   []string{"role global/ Reader"},
			wantWrites:  []string{"insert membership global/ dana Steward", "insert membership every/ dana Reader"},
			wantChanged: 1,
		},
		{
			name:       "a role the person holds already is left as it is, and the others are given",
			inv:        &sessioninfo.Invitation{Email: "dana@example.com", RoleNames: []string{"Steward", "Reader"}},
			held:       []access.MembershipKey{access.NewMembershipKey(globalScope, "dana", "Steward")},
			wantReads:  []string{"role global/ Reader"},
			wantWrites: []string{"insert membership every/ dana Reader"},
			// One membership was written, so the engine is told.
			wantChanged: 1,
		},
		{
			name:    "a role that exists nowhere any more fails the transaction, so the role given before it is not committed",
			inv:     &sessioninfo.Invitation{Email: "dana@example.com", RoleNames: []string{"Steward", "Retired"}},
			wantErr: httpio.HasNotFound,
			// Retired is read from the roles table in both places it could be held.
			wantReads:  []string{"role global/ Retired", "role every/ Retired"},
			wantWrites: []string{"insert membership global/ dana Steward"},
		},
		{
			name:    "a role of a tenant's invitation that does not exist in the tenant fails the transaction",
			inv:     &sessioninfo.Invitation{Email: "dana@example.com", Tenant: "alpha", RoleNames: []string{"Steward"}},
			wantErr: httpio.HasNotFound,
			// The global role is not looked for anywhere but in the tenant.
			wantReads: []string{"role domain/alpha Steward"},
		},
		{
			name:    "a failed write fails the transaction and is not taken for a role that does not exist",
			inv:     &sessioninfo.Invitation{Email: "dana@example.com", RoleNames: []string{"Steward"}},
			failOn:  "InsertMembership",
			wantErr: func(err error) bool { return errors.Is(err, errStore) && !httpio.HasNotFound(err) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{memberships: map[access.MembershipKey]bool{}, roles: map[access.RoleKey]bool{}, failOn: tt.failOn}
			for _, key := range tt.held {
				store.memberships[key] = true
			}
			m, engine := newManager(t, store, engineRoles)

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.giveInvitedRoles(ctx, txn, "dana", tt.inv)
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("giveInvitedRoles() error = %v, want the refusal", err)
				}
			} else if err != nil {
				t.Fatalf("giveInvitedRoles() error = %v", err)
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

// TestManager_giveDirectoryRoles pins what a directory's default role gives a person at
// their first sign-in: each role in its tenant, and a role that cannot be given fails
// the transaction that creates the person.
func TestManager_giveDirectoryRoles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		seats       []signin.DirectorySeat
		custom      []access.RoleKey
		wantErr     func(error) bool
		wantWrites  []string
		wantChanged int
	}{
		{
			name:  "each default role is given in its tenant, and the engine is told once",
			seats: []signin.DirectorySeat{{Tenant: "alpha", Role: "Reader"}, {Tenant: "beta", Role: "Auditor"}},
			// beta's role is a custom one, read from the roles table.
			custom:      []access.RoleKey{{Kind: "domain", Domain: "beta", Role: "Auditor"}},
			wantWrites:  []string{"insert membership domain/alpha dana Reader", "insert membership domain/beta dana Auditor"},
			wantChanged: 1,
		},
		{
			name:       "a default role that does not exist in its tenant fails the transaction",
			seats:      []signin.DirectorySeat{{Tenant: "alpha", Role: "Reader"}, {Tenant: "beta", Role: "Auditor"}},
			wantErr:    httpio.HasNotFound,
			wantWrites: []string{"insert membership domain/alpha dana Reader"},
		},
		{
			name: "no default role gives nothing and tells the engine nothing",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{memberships: map[access.MembershipKey]bool{}, roles: map[access.RoleKey]bool{}}
			for _, key := range tt.custom {
				store.roles[key] = true
			}
			m, engine := newManager(t, store, engineRoles)

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.giveDirectoryRoles(ctx, txn, "dana", tt.seats)
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("giveDirectoryRoles() error = %v, want the refusal", err)
				}
			} else if err != nil {
				t.Fatalf("giveDirectoryRoles() error = %v", err)
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

// TestManager_signInHooks pins the two hooks as the session library calls them: nothing
// is told to the engine until the committed function runs, a run whose transaction did
// not commit (Spanner ran the closure again) tells it nothing, and a hook that fails
// answers no committed function.
func TestManager_signInHooks(t *testing.T) {
	t.Parallel()

	inv := &sessioninfo.Invitation{Email: "dana@example.com", Tenant: "alpha", RoleNames: []string{"Reader"}}
	seats := []signin.DirectorySeat{{Tenant: "alpha", Role: "Reader"}}

	hooks := map[string]func(m *Manager) func(ctx context.Context) (func(ctx context.Context), error){
		"an accepted invitation": func(m *Manager) func(ctx context.Context) (func(ctx context.Context), error) {
			hook := m.InvitationAccepted("Staff")

			return func(ctx context.Context) (func(ctx context.Context), error) {
				// The store here is in memory and reads nothing off the transaction, so
				// the hook is handed none.
				return hook(ctx, nil, impulse.NilUUID, "dana", inv)
			}
		},
		"a directory's default role": func(m *Manager) func(ctx context.Context) (func(ctx context.Context), error) {
			hook := m.DirectorySeated("Staff")

			return func(ctx context.Context) (func(ctx context.Context), error) {
				return hook(ctx, nil, impulse.NilUUID, "dana", seats)
			}
		},
	}
	for name, build := range hooks {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{memberships: map[access.MembershipKey]bool{}, roles: map[access.RoleKey]bool{}}
			m, engine := newManager(t, store, engineRoles)
			hook := build(m)

			// The first run's transaction aborts: its committed function is never called.
			if _, err := hook(t.Context()); err != nil {
				t.Fatalf("the hook's first run error = %v", err)
			}
			committed, err := hook(t.Context())
			if err != nil {
				t.Fatalf("the hook's second run error = %v", err)
			}
			if engine.changed != 0 {
				t.Fatalf("the engine was told %d time(s) before any commit", engine.changed)
			}
			committed(t.Context())
			if engine.changed != 1 {
				t.Errorf("the engine was told the policy changed %d time(s) after the commit, want 1", engine.changed)
			}
			want := []string{"insert membership domain/alpha dana Reader", "insert membership domain/alpha dana Reader"}
			if diff := cmp.Diff(want, store.writes); diff != "" {
				t.Errorf("buffered writes of the two runs (-want +got):\n%s", diff)
			}
		})

		t.Run(name+" that cannot be given", func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{memberships: map[access.MembershipKey]bool{}, roles: map[access.RoleKey]bool{}}
			// An engine whose role file holds no role: the role the hook gives does not exist.
			m, engine := newManager(t, store, map[accesstypes.PolicyScope][]accesstypes.Role{})

			committed, err := build(m)(t.Context())
			if !httpio.HasNotFound(err) {
				t.Fatalf("the hook's error = %v, want NotFound", err)
			}
			if committed != nil {
				t.Error("a hook that failed answered a committed function")
			}
			if engine.changed != 0 || len(store.writes) != 0 {
				t.Errorf("a hook that failed told the engine %d time(s) and buffered %v", engine.changed, store.writes)
			}
		})
	}
}

// renamedRoles is an engine's membership store for the rename hook: the roles each
// person holds by where they are held, with a write that can be made to fail. Every
// other method panics through the embedded nil interface.
type renamedRoles struct {
	access.UserManager
	held    map[accesstypes.User]accesstypes.RoleCollection
	failAdd bool
}

func (r *renamedRoles) UserRoles(_ context.Context, user accesstypes.User, _ ...accesstypes.PolicyScope) (accesstypes.RoleCollection, error) {
	return r.held[user], nil
}

func (r *renamedRoles) AddUserRoles(_ context.Context, scope accesstypes.PolicyScope, user accesstypes.User, roles ...accesstypes.Role) error {
	if r.failAdd {
		return errStore
	}
	if r.held[user] == nil {
		r.held[user] = accesstypes.RoleCollection{}
	}
	r.held[user][scope] = append(r.held[user][scope], roles...)

	return nil
}

func (r *renamedRoles) DeleteUserRoles(_ context.Context, scope accesstypes.PolicyScope, user accesstypes.User, _ ...accesstypes.Role) error {
	delete(r.held[user], scope)

	return nil
}

// TestRolesFollowRename pins the hook a rename by the session library is reported to:
// every role the person held under the old name is held under the new one, in each
// place it was held, and none is left under the old; a role that could not be given
// under the new name stops the move with the old name's roles kept, so running the
// hook again finishes it.
func TestRolesFollowRename(t *testing.T) {
	t.Parallel()

	alpha := accesstypes.DomainPolicyScope("alpha")
	holds := func() accesstypes.RoleCollection {
		return accesstypes.RoleCollection{globalScope: {"Steward"}, alpha: {"Reader", "Auditor"}}
	}

	t.Run("the roles move to the new name", func(t *testing.T) {
		t.Parallel()

		store := &renamedRoles{held: map[accesstypes.User]accesstypes.RoleCollection{"pat@example.com": holds()}}
		if err := RolesFollowRename(store)(t.Context(), impulse.NilUUID, "pat@example.com", "pat.lee@example.com"); err != nil {
			t.Fatalf("RolesFollowRename() error = %v", err)
		}
		if diff := cmp.Diff(holds(), store.held["pat.lee@example.com"]); diff != "" {
			t.Errorf("the roles under the new name (-want +got):\n%s", diff)
		}
		if left := store.held["pat@example.com"]; len(left) != 0 {
			t.Errorf("the old name still holds %v", left)
		}
	})

	t.Run("a role that cannot be given stops the move, and the old name keeps its roles", func(t *testing.T) {
		t.Parallel()

		store := &renamedRoles{held: map[accesstypes.User]accesstypes.RoleCollection{"pat@example.com": holds()}, failAdd: true}
		err := RolesFollowRename(store)(t.Context(), impulse.NilUUID, "pat@example.com", "pat.lee@example.com")
		if !errors.Is(err, errStore) {
			t.Fatalf("RolesFollowRename() error = %v, want the store's", err)
		}
		if diff := cmp.Diff(holds(), store.held["pat@example.com"]); diff != "" {
			t.Errorf("the roles under the old name after the failed move (-want +got):\n%s", diff)
		}
	})

	t.Run("a person who holds nothing moves nothing", func(t *testing.T) {
		t.Parallel()

		store := &renamedRoles{held: map[accesstypes.User]accesstypes.RoleCollection{}}
		if err := RolesFollowRename(store)(t.Context(), impulse.NilUUID, "pat@example.com", "pat.lee@example.com"); err != nil {
			t.Fatalf("RolesFollowRename() error = %v", err)
		}
		if len(store.held) != 0 {
			t.Errorf("the store holds %v, want nothing written", store.held)
		}
	})
}
