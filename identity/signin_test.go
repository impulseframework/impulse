package identity

import (
	"context"
	"strings"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// TestManager_giveInvitedRoles pins what an accepted invitation gives: its roles in its
// tenant, or, for an invitation into the application, each role where it is held, every
// one under the id of the person's account; a role that can no longer be given fails
// the transaction, in an error that names the person by username, and a failed write is
// not taken for a role that does not exist.
func TestManager_giveInvitedRoles(t *testing.T) {
	t.Parallel()

	id := userID.String()

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
			inv:         &sessioninfo.Invitation{Email: "dana@example.com", TenantID: "alpha", RoleNames: []string{"Reader"}},
			wantWrites:  []string{"insert membership domain/alpha " + id + " Reader"},
			wantChanged: 1,
		},
		{
			name: "an invitation into the application gives a global role globally and a tenant's role in every tenant, and the engine is told once",
			inv:  &sessioninfo.Invitation{Email: "dana@example.com", RoleNames: []string{"Steward", "Reader"}},
			// Reader is asked for globally first, where the role file does not hold it, so the
			// roles table is read there before every tenant is tried.
			wantReads:   []string{"role global/ Reader"},
			wantWrites:  []string{"insert membership global/ " + id + " Steward", "insert membership every/ " + id + " Reader"},
			wantChanged: 1,
		},
		{
			name:       "a role the person holds already is left as it is, and the others are given",
			inv:        &sessioninfo.Invitation{Email: "dana@example.com", RoleNames: []string{"Steward", "Reader"}},
			held:       []access.MembershipKey{access.NewMembershipKey(globalScope, dana, "Steward")},
			wantReads:  []string{"role global/ Reader"},
			wantWrites: []string{"insert membership every/ " + id + " Reader"},
			// One membership was written, so the engine is told.
			wantChanged: 1,
		},
		{
			name:    "a role that exists nowhere any more fails the transaction, so the role given before it is not committed",
			inv:     &sessioninfo.Invitation{Email: "dana@example.com", RoleNames: []string{"Steward", "Retired"}},
			wantErr: httpio.HasNotFound,
			// Retired is read from the roles table in both places it could be held.
			wantReads:  []string{"role global/ Retired", "role every/ Retired"},
			wantWrites: []string{"insert membership global/ " + id + " Steward"},
		},
		{
			name:    "a role of a tenant's invitation that does not exist in the tenant fails the transaction",
			inv:     &sessioninfo.Invitation{Email: "dana@example.com", TenantID: "alpha", RoleNames: []string{"Steward"}},
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
				return m.giveInvitedRoles(ctx, txn, userID, "dana", tt.inv)
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("giveInvitedRoles() error = %v, want the refusal", err)
				}
				if !strings.Contains(err.Error(), `gives "dana" the role`) {
					t.Errorf("giveInvitedRoles() error = %v, want one that names the person by username", err)
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
// their first sign-in: each role in its tenant, under the id of the person's account,
// and a role that cannot be given fails the transaction that creates the person, in an
// error that names them by username.
func TestManager_giveDirectoryRoles(t *testing.T) {
	t.Parallel()

	id := userID.String()

	tests := []struct {
		name  string
		seats []signin.DirectorySeat
		// held are the memberships the person holds before the sign-in, as committed.
		held        []access.MembershipKey
		custom      []access.RoleKey
		wantErr     func(error) bool
		wantWrites  []string
		wantGiven   []signin.DirectorySeat
		wantChanged int
	}{
		{
			name:        "a person who holds a role in another tenant is given the default role in the directory's tenant",
			seats:       []signin.DirectorySeat{{Tenant: "alpha", Role: "Reader"}},
			held:        []access.MembershipKey{access.NewMembershipKey(accesstypes.DomainPolicyScope("beta"), User(userID), "Reader")},
			wantWrites:  []string{"insert membership domain/alpha " + id + " Reader"},
			wantGiven:   []signin.DirectorySeat{{Tenant: "alpha", Role: "Reader"}},
			wantChanged: 1,
		},
		{
			name:  "a person who holds a role in the tenant keeps what they hold and is given nothing",
			seats: []signin.DirectorySeat{{Tenant: "alpha", Role: "Reader"}},
			held:  []access.MembershipKey{access.NewMembershipKey(accesstypes.DomainPolicyScope("alpha"), User(userID), "Editor")},
		},
		{
			name:  "a person who holds the default role itself in the tenant is given nothing",
			seats: []signin.DirectorySeat{{Tenant: "alpha", Role: "Reader"}},
			held:  []access.MembershipKey{access.NewMembershipKey(accesstypes.DomainPolicyScope("alpha"), User(userID), "Reader")},
		},
		{
			name:        "a role held for the whole application is no role in the tenant: the default role is given",
			seats:       []signin.DirectorySeat{{Tenant: "alpha", Role: "Reader"}},
			held:        []access.MembershipKey{access.NewMembershipKey(accesstypes.GlobalPolicyScope(), User(userID), "Operator")},
			wantWrites:  []string{"insert membership domain/alpha " + id + " Reader"},
			wantGiven:   []signin.DirectorySeat{{Tenant: "alpha", Role: "Reader"}},
			wantChanged: 1,
		},
		{
			name:  "each default role is given in its tenant, and the engine is told once",
			seats: []signin.DirectorySeat{{Tenant: "alpha", Role: "Reader"}, {Tenant: "beta", Role: "Auditor"}},
			// beta's role is a custom one, read from the roles table.
			custom:      []access.RoleKey{{Kind: "domain", Domain: "beta", Role: "Auditor"}},
			wantWrites:  []string{"insert membership domain/alpha " + id + " Reader", "insert membership domain/beta " + id + " Auditor"},
			wantGiven:   []signin.DirectorySeat{{Tenant: "alpha", Role: "Reader"}, {Tenant: "beta", Role: "Auditor"}},
			wantChanged: 1,
		},
		{
			name:       "a default role that does not exist in its tenant fails the transaction",
			seats:      []signin.DirectorySeat{{Tenant: "alpha", Role: "Reader"}, {Tenant: "beta", Role: "Auditor"}},
			wantErr:    httpio.HasNotFound,
			wantWrites: []string{"insert membership domain/alpha " + id + " Reader"},
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
			for _, key := range tt.held {
				store.memberships[key] = true
			}
			m, engine := newManager(t, store, engineRoles)

			var given []signin.DirectorySeat
			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				var err error
				given, err = m.giveDirectoryRoles(ctx, txn, userID, "dana", tt.seats)

				return err
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("giveDirectoryRoles() error = %v, want the refusal", err)
				}
				if !strings.Contains(err.Error(), `gives "dana" the role`) {
					t.Errorf("giveDirectoryRoles() error = %v, want one that names the person by username", err)
				}
			} else if err != nil {
				t.Fatalf("giveDirectoryRoles() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
			if tt.wantErr == nil {
				if diff := cmp.Diff(tt.wantGiven, given); diff != "" {
					t.Errorf("seats given (-want +got):\n%s", diff)
				}
			}
			if engine.changed != tt.wantChanged {
				t.Errorf("the engine was told the policy changed %d time(s), want %d", engine.changed, tt.wantChanged)
			}
		})
	}
}

// TestManager_signInHooks pins the two hooks as the session library calls them: the
// roles are given under the id of the account the library hands the hook, never under
// the username beside it; nothing is told to the engine until the committed function
// runs, a run whose transaction did not commit (Spanner ran the closure again) tells it
// nothing, and a hook that fails answers no committed function.
func TestManager_signInHooks(t *testing.T) {
	t.Parallel()

	inv := &sessioninfo.Invitation{Email: "dana@example.com", TenantID: "alpha", RoleNames: []string{"Reader"}}
	seats := []signin.DirectorySeat{{Tenant: "alpha", Role: "Reader"}}

	hooks := map[string]func(m *Manager) func(ctx context.Context) (func(ctx context.Context), error){
		"an accepted invitation": func(m *Manager) func(ctx context.Context) (func(ctx context.Context), error) {
			hook := m.InvitationAccepted("Staff")

			return func(ctx context.Context) (func(ctx context.Context), error) {
				// The store here is in memory and reads nothing off the transaction, so
				// the hook is handed none.
				return hook(ctx, nil, userID, "dana", inv)
			}
		},
		"a directory's default role": func(m *Manager) func(ctx context.Context) (func(ctx context.Context), error) {
			hook := m.DirectorySeated("Staff")

			return func(ctx context.Context) (func(ctx context.Context), error) {
				return hook(ctx, nil, userID, "dana", seats)
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
			given := "insert membership domain/alpha " + userID.String() + " Reader"
			want := []string{given, given}
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
