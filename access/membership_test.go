package access

// These tests pin what an application needs to write a membership in a transaction of its
// own: the row key a scope is spelled as, the rule a row to be written is held to (the one
// AddUserRoles holds its own rows to), and the engine learning of a row committed outside
// the manager's write path only when it is told.

import (
	"context"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

// TestNewMembershipKey pins the key each kind of scope is spelled as: the columns the
// stores write, in key order.
func TestNewMembershipKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		scope accesstypes.PolicyScope
		want  MembershipKey
	}{
		{
			name:  "the global partition",
			scope: globalPolicy,
			want:  MembershipKey{Kind: "global", Role: "Steward", User: "dana"},
		},
		{
			name:  "one domain",
			scope: tenant1Policy,
			want:  MembershipKey{Kind: "domain", Domain: "tenant1", Role: "Steward", User: "dana"},
		},
		{
			name:  "every domain",
			scope: everyDomainPolicy,
			want:  MembershipKey{Kind: "every", Role: "Steward", User: "dana"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.want, NewMembershipKey(tt.scope, "dana", "Steward")); diff != "" {
				t.Errorf("NewMembershipKey() (-want +got):\n%s", diff)
			}
		})
	}
}

// Test_userManager_Membership pins the rule: the row is answered for a role that exists in
// the scope (a default of the scope's kind, a custom role held there, a custom role held in
// every domain for one domain) and refused as AddUserRoles refuses it otherwise; nothing is
// written either way.
func Test_userManager_Membership(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		scope   accesstypes.PolicyScope
		user    accesstypes.User
		role    accesstypes.Role
		want    MembershipKey
		wantErr func(error) bool
		// wantReads bounds the reads of the roles table: none for a default role, one
		// for the scope itself and one more for every domain when the scope is a domain.
		wantReads int
	}{
		{
			name:  "a global default in the global partition",
			scope: globalPolicy, user: "dana", role: globalDefault,
			want: MembershipKey{Kind: "global", Role: globalDefault, User: "dana"},
		},
		{
			name:  "a domain default in one domain",
			scope: tenant1Policy, user: "dana", role: domainDefault,
			want: MembershipKey{Kind: "domain", Domain: "tenant1", Role: domainDefault, User: "dana"},
		},
		{
			name:  "a domain default in a domain the store holds no row in",
			scope: neverSeenScope.PolicyScope(), user: "dana", role: domainDefault,
			want: MembershipKey{Kind: "domain", Domain: "never-seen", Role: domainDefault, User: "dana"},
		},
		{
			name:  "a domain default in every domain",
			scope: everyDomainPolicy, user: "dana", role: domainDefault,
			want: MembershipKey{Kind: "every", Role: domainDefault, User: "dana"},
		},
		{
			name:  "a custom role held in the domain",
			scope: tenant1Policy, user: "dana", role: "Zed",
			want:      MembershipKey{Kind: "domain", Domain: "tenant1", Role: "Zed", User: "dana"},
			wantReads: 1,
		},
		{
			name:  "a custom role held in every domain, for one domain",
			scope: tenant2Policy, user: "dana", role: "Everywhere",
			want:      MembershipKey{Kind: "domain", Domain: "tenant2", Role: "Everywhere", User: "dana"},
			wantReads: 2,
		},
		{
			name:  "a custom role held in one domain is refused in every domain",
			scope: everyDomainPolicy, user: "dana", role: "Zed",
			wantErr:   httpio.HasNotFound,
			wantReads: 1,
		},
		{
			name:  "a custom role held in one domain is refused in another",
			scope: tenant2Policy, user: "dana", role: "Zed",
			wantErr:   httpio.HasNotFound,
			wantReads: 2,
		},
		{
			name:  "a global default is refused in one domain",
			scope: tenant1Policy, user: "dana", role: globalDefault,
			wantErr:   httpio.HasNotFound,
			wantReads: 2,
		},
		{
			name:  "a domain default is refused in the global partition",
			scope: globalPolicy, user: "dana", role: domainDefault,
			wantErr:   httpio.HasNotFound,
			wantReads: 1,
		},
		{
			name:  "a role nothing defines is refused",
			scope: globalPolicy, user: "dana", role: "Nobody",
			wantErr:   httpio.HasNotFound,
			wantReads: 1,
		},
		{
			name:  "another spelling of a role is refused: the row carries the spelling the store holds",
			scope: globalPolicy, user: "dana", role: "steward",
			wantErr:   httpio.HasNotFound,
			wantReads: 1,
		},
		{
			name:  "an empty user is refused",
			scope: globalPolicy, user: "", role: globalDefault,
			wantErr: httpio.HasBadRequest,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			m, store := defaultsManager(t)

			reads := 0
			got, err := m.Membership(ctx, tt.scope, tt.user, tt.role, func(ctx context.Context, key RoleKey) (bool, error) {
				reads++

				return storeRoleReader(store)(ctx, key)
			})
			if reads > tt.wantReads {
				t.Errorf("Membership() read the roles table %d times, want at most %d", reads, tt.wantReads)
			}
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("Membership() error = %v, want the refusal AddUserRoles gives", err)
				}
				if got != (MembershipKey{}) {
					t.Errorf("Membership() = %+v beside an error, want the zero key", got)
				}
			} else {
				if err != nil {
					t.Fatalf("Membership() error = %v", err)
				}
				if diff := cmp.Diff(tt.want, got); diff != "" {
					t.Errorf("Membership() (-want +got):\n%s", diff)
				}
			}

			// The same call through AddUserRoles is refused or accepted alike: one rule.
			addErr := m.AddUserRoles(ctx, tt.scope, tt.user, tt.role)
			if (addErr != nil) != (tt.wantErr != nil) || (tt.wantErr != nil && !tt.wantErr(addErr)) {
				t.Errorf("AddUserRoles() error = %v, Membership() error = %v: the two must agree", addErr, err)
			}
			if tt.wantErr != nil && len(store.memberships) != 0 {
				t.Errorf("store holds %d memberships after a refusal, want none", len(store.memberships))
			}
		})
	}
}

// Test_userManager_Membership_writesNothing pins that answering the row writes nothing: the
// caller's transaction does.
func Test_userManager_Membership_writesNothing(t *testing.T) {
	t.Parallel()
	m, store := defaultsManager(t)

	if _, err := m.Membership(context.Background(), globalPolicy, "dana", globalDefault, storeRoleReader(store)); err != nil {
		t.Fatalf("Membership() error = %v", err)
	}
	if len(store.memberships) != 0 {
		t.Errorf("store holds %d memberships, want none: Membership() only answers the row", len(store.memberships))
	}
}

// TestClient_PolicyChanged pins the word to the engine: a membership committed outside the
// manager's write path (here straight into the store, as an application's own transaction
// writes it) is not seen while the engine answers from the snapshot it holds, and is seen
// by the very next check once the committer says the policy changed.
func TestClient_PolicyChanged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	store := newFakeStore()
	client := newPolicyClient(t, store, WithDefaultRoles(grammarCollection{}, grammarRoleFile), WithHeartbeatInterval(time.Hour))
	if err := client.WaitReady(ctx); err != nil {
		t.Fatalf("Client.WaitReady() error = %v", err)
	}
	var controller Controller = client

	granted := func() bool {
		t.Helper()
		decisions, err := controller.CheckUserResources(ctx, accesstypes.NewEnvironment(), "dana", accesstypes.GlobalScope(), "Execute", "DoThing")
		if err != nil {
			t.Fatalf("CheckUserResources() error = %v", err)
		}

		return decisions["DoThing"].IsGranted()
	}
	if granted() {
		t.Fatal("dana executes DoThing before any membership exists")
	}

	key, err := controller.UserManager().Membership(ctx, globalPolicy, "dana", "Steward", storeRoleReader(store))
	if err != nil {
		t.Fatalf("Membership() error = %v", err)
	}
	if err := store.InsertUserRole(ctx, globalPolicy, key.User, key.Role); err != nil {
		t.Fatalf("InsertUserRole() error = %v", err)
	}
	if granted() {
		t.Fatal("dana executes DoThing before the engine was told the policy changed: the test no longer shows what PolicyChanged does")
	}

	controller.PolicyChanged()
	if !granted() {
		t.Error("dana is refused DoThing after PolicyChanged(), want the very next check to see the committed membership")
	}
}

// storeRoleReader reads the roles table of the fake store, standing where a caller's
// reader over its own transaction stands.
func storeRoleReader(store *fakeStore) RoleReader {
	return func(ctx context.Context, key RoleKey) (bool, error) {
		scope, err := policy.ScopeFromColumns(key.Kind, key.Axis, key.Domain)
		if err != nil {
			return false, errors.Wrap(err, "policy.ScopeFromColumns()")
		}

		return store.RoleExists(ctx, scope, key.Role)
	}
}

// Test_userManager_Membership_reader pins what the rule does with the caller's reader: a
// failure to read is the caller's failure, never a refusal of the role, and a call
// without a reader is refused before anything is decided.
func Test_userManager_Membership_reader(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, _ := defaultsManager(t)

	failed := errors.New("the transaction was aborted")
	_, err := m.Membership(ctx, globalPolicy, "dana", "Nobody", func(context.Context, RoleKey) (bool, error) {
		return false, failed
	})
	if !errors.Is(err, failed) || httpio.HasNotFound(err) {
		t.Errorf("Membership() error = %v, want the reader's failure and no refusal of the role", err)
	}

	if _, err := m.Membership(ctx, globalPolicy, "dana", globalDefault, nil); err == nil {
		t.Error("Membership() with no reader error = nil, want a refusal")
	}
}
