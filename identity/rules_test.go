package identity

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
)

// dana and erin are the two people of the tests as the permission store names them and
// as a request's caller carries them: by the id of their account. Their usernames are
// what the fake store's users table holds under the ids, for people to read.
var (
	dana = User(userID)
	erin = User(otherUser)
)

// callerPermissions is the checker a generated handler stamps on the context, as far as
// the rules read it: who the caller is, named as the permission store names a person.
// Every other method is the embedded nil interface's, and panics if a rule reaches for it.
type callerPermissions struct {
	resource.UserPermissions
	user accesstypes.User
}

func (c callerPermissions) User() accesstypes.User {
	return c.user
}

// asCaller is the context of a request made by the user, as a generated handler hands
// it to a method; an empty user is a call outside any request.
func asCaller(ctx context.Context, user accesstypes.User) context.Context {
	if user == "" {
		return ctx
	}

	return resource.WithCaller(ctx, &resource.Caller{Permissions: callerPermissions{user: user}})
}

// asCallerThrough is the context of a request made by the user on an outlet of the auth:
// the outlet's generated routes bind the auth's name, and the handler stamps the caller.
func asCallerThrough(ctx context.Context, auth string, user accesstypes.User) context.Context {
	return asCaller(resource.WithAuth(ctx, auth), user)
}

// administrators are the roles the tests name as a tenant's administrators': Warden.
var administrators = WithAdministratorRoles([]accesstypes.Role{"Warden"})

// TestManager_DisableUser_ownAccount pins the rule that nobody disables themselves: the
// request's caller is refused their own account, which is the one whose id the caller
// carries; a caller of another id goes through, and so does one who carries the
// account's username, which names nobody; a call outside any request goes through too.
func TestManager_DisableUser_ownAccount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		caller     accesstypes.User
		wantErr    func(error) bool
		wantWrites []string
	}{
		{name: "the caller's own account is refused: the caller carries the account's id", caller: dana, wantErr: httpio.HasForbidden},
		{name: "a caller of another id is another person: the account is disabled", caller: erin, wantWrites: []string{"update user " + userID.String() + " disabled=true"}},
		{name: "the account's username is not who a caller is", caller: "dana", wantWrites: []string{"update user " + userID.String() + " disabled=true"}},
		{name: "a call outside any request is the application's act", wantWrites: []string{"update user " + userID.String() + " disabled=true"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{usernames: map[impulse.UUID]string{userID: "dana"}}
			m, _ := newManager(t, store, nil)

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.DisableUser(asCaller(ctx, tt.caller), txn, userID)
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("DisableUser() error = %v, want the refusal", err)
				}
			} else if err != nil {
				t.Fatalf("DisableUser() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
		})
	}
}

// TestManager_RemoveRole_lastAdministrator pins the rule on a person removing a role
// from themselves: a tenant's administrator role nobody else would hold there stays,
// and every other role goes, a role of the global scope among them, since the
// application's last administrator is its last super administrator; without the
// administrator roles a person removes no role from themselves in a tenant; and the
// rule judges nobody but the caller.
func TestManager_RemoveRole_lastAdministrator(t *testing.T) {
	t.Parallel()

	membership := access.NewMembershipKey

	tests := []struct {
		name    string
		opts    []Option
		caller  accesstypes.User
		scope   accesstypes.PolicyScope
		role    accesstypes.Role
		held    []access.MembershipKey
		wantErr func(error) bool
		// wantRemoved is whether the membership is removed.
		wantRemoved bool
	}{
		{
			name: "a role of the global scope goes from its last holder: the application's last administrator is its last super administrator",
			opts: []Option{administrators}, caller: dana, scope: globalScope, role: "Steward",
			held:        []access.MembershipKey{membership(globalScope, dana, "Steward"), membership(globalScope, erin, "Reader")},
			wantRemoved: true,
		},
		{
			name:   "a role of the global scope goes without the administrator roles too",
			caller: dana, scope: globalScope, role: "Steward",
			held:        []access.MembershipKey{membership(globalScope, dana, "Steward"), membership(globalScope, dana, "Reader")},
			wantRemoved: true,
		},
		{
			name: "a role that is no administrator role goes",
			opts: []Option{administrators}, caller: dana, scope: globalScope, role: "Reader",
			held:        []access.MembershipKey{membership(globalScope, dana, "Reader"), membership(globalScope, dana, "Steward")},
			wantRemoved: true,
		},
		{
			name: "the last administrator of a tenant keeps the role",
			opts: []Option{administrators}, caller: dana, scope: alphaScope, role: "Warden",
			held:    []access.MembershipKey{membership(alphaScope, dana, "Warden"), membership(accesstypes.DomainPolicyScope("beta"), erin, "Warden")},
			wantErr: httpio.HasForbidden,
		},
		{
			name: "a tenant's administrator leaves once another holds the role there",
			opts: []Option{administrators}, caller: dana, scope: alphaScope, role: "Warden",
			held:        []access.MembershipKey{membership(alphaScope, dana, "Warden"), membership(alphaScope, erin, "Warden")},
			wantRemoved: true,
		},
		{
			name: "a person who holds the role in every tenant is the tenant's administrator too",
			opts: []Option{administrators}, caller: dana, scope: alphaScope, role: "Warden",
			held:        []access.MembershipKey{membership(alphaScope, dana, "Warden"), membership(everyScope, erin, "Warden")},
			wantRemoved: true,
		},
		{
			name: "the caller's own membership in every tenant keeps them the tenant's administrator",
			opts: []Option{administrators}, caller: dana, scope: alphaScope, role: "Warden",
			held:        []access.MembershipKey{membership(alphaScope, dana, "Warden"), membership(everyScope, dana, "Warden")},
			wantRemoved: true,
		},
		{
			name: "a role held in every tenant is no administrator role of the application",
			opts: []Option{administrators}, caller: dana, scope: everyScope, role: "Warden",
			held:        []access.MembershipKey{membership(everyScope, dana, "Warden"), membership(globalScope, dana, "Reader")},
			wantRemoved: true,
		},
		{
			name:   "without the administrator roles a person removes no role from themselves in a tenant",
			caller: dana, scope: alphaScope, role: "Reader",
			held:    []access.MembershipKey{membership(alphaScope, dana, "Reader"), membership(globalScope, dana, "Steward")},
			wantErr: httpio.HasForbidden,
		},
		{
			name:   "without the administrator roles a person removes no role from themselves in every tenant",
			caller: dana, scope: everyScope, role: "Reader",
			held:    []access.MembershipKey{membership(everyScope, dana, "Reader"), membership(globalScope, dana, "Steward")},
			wantErr: httpio.HasForbidden,
		},
		{
			name: "the rule judges nobody but the caller: another person removes the last administrator's role in the tenant",
			opts: []Option{administrators}, caller: erin, scope: alphaScope, role: "Warden",
			held:        []access.MembershipKey{membership(alphaScope, dana, "Warden"), membership(globalScope, dana, "Reader")},
			wantRemoved: true,
		},
		{
			name: "a call outside any request is the application's act",
			opts: []Option{administrators}, scope: alphaScope, role: "Warden",
			held:        []access.MembershipKey{membership(alphaScope, dana, "Warden"), membership(globalScope, dana, "Reader")},
			wantRemoved: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{memberships: map[access.MembershipKey]bool{}}
			for _, key := range tt.held {
				store.memberships[key] = true
			}
			m, engine := newManager(t, store, engineRoles, tt.opts...)

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.RemoveRole(asCaller(ctx, tt.caller), txn, tt.scope, userID, tt.role)
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("RemoveRole() error = %v, want the refusal", err)
				}
			} else if err != nil {
				t.Fatalf("RemoveRole() error = %v", err)
			}
			var wantWrites []string
			wantChanged := 0
			if tt.wantRemoved {
				removed := membership(tt.scope, dana, tt.role)
				wantWrites, wantChanged = []string{"delete membership " + membershipText(&removed)}, 1
			}
			if diff := cmp.Diff(wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
			if engine.changed != wantChanged {
				t.Errorf("the engine was told %d times, want %d", engine.changed, wantChanged)
			}
		})
	}
}

// TestManager_RemoveRole_lastAdministrator_sentence pins what the last administrator of
// a tenant reads: the sentence says the tenant is theirs to keep, in the application's
// word for a tenant, and does not print the tenant's key, an id nobody reads.
func TestManager_RemoveRole_lastAdministrator_sentence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts []Option
		want string
	}{
		{
			name: "an application that names no word says tenant",
			opts: []Option{administrators},
			want: "you are the last administrator of this tenant: give another person the role before you remove your own",
		},
		{
			name: "an application whose tenants are customers says customer",
			opts: []Option{administrators, WithTenantWord("customer")},
			want: "you are the last administrator of this customer: give another person the role before you remove your own",
		},
		{
			name: "an empty word leaves tenant",
			opts: []Option{administrators, WithTenantWord("")},
			want: "you are the last administrator of this tenant: give another person the role before you remove your own",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			const tenant = "0190a000-0000-7000-8000-000000000001"
			scope := accesstypes.DomainPolicyScope(tenant)
			store := &fakeStore{memberships: map[access.MembershipKey]bool{access.NewMembershipKey(scope, dana, "Warden"): true}}
			m, _ := newManager(t, store, engineRoles, tt.opts...)

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.RemoveRole(asCaller(ctx, dana), txn, scope, userID, "Warden")
			})
			if !httpio.HasForbidden(err) {
				t.Fatalf("RemoveRole() error = %v, want the refusal", err)
			}
			if got := httpio.Message(err); got != tt.want {
				t.Errorf("the refusal reads %q, want %q", got, tt.want)
			}
		})
	}
}

// TestManager_RemoveRole_lastRole pins the rule on an account nothing admits: the
// removal of a person's last role anywhere deletes the account in the same transaction,
// its sessions ended and its identity links removed first, whoever removed the role; a
// person who holds a role somewhere else stays, and an id no user holds has no account
// to delete.
func TestManager_RemoveRole_lastRole(t *testing.T) {
	t.Parallel()

	steward := access.NewMembershipKey(globalScope, dana, "Steward")
	reader := access.NewMembershipKey(alphaScope, dana, "Reader")
	removed := "delete membership global/ " + userID.String() + " Steward"
	deleted := []string{
		"end session " + sessionA.String(),
		"delete identity " + identityA.String(),
		"delete user " + userID.String(),
		removed,
	}

	tests := []struct {
		name       string
		held       []access.MembershipKey
		account    bool
		caller     accesstypes.User
		failOn     string
		wantErr    bool
		wantWrites []string
	}{
		{
			name: "the last role anywhere takes the account with it",
			held: []access.MembershipKey{steward}, account: true, caller: erin,
			wantWrites: deleted,
		},
		{
			name: "a call outside any request deletes the account the same",
			held: []access.MembershipKey{steward}, account: true,
			wantWrites: deleted,
		},
		{
			name: "a person who holds a role in a tenant stays",
			held: []access.MembershipKey{steward, reader}, account: true, caller: erin,
			wantWrites: []string{removed},
		},
		{
			name:       "an id no user holds has no account to delete",
			held:       []access.MembershipKey{steward},
			wantWrites: []string{removed},
		},
		{
			name: "a failed delete fails the call",
			held: []access.MembershipKey{steward}, account: true, caller: erin,
			failOn: "DeleteUser", wantErr: true,
			wantWrites: deleted[:2],
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{memberships: map[access.MembershipKey]bool{}, failOn: tt.failOn}
			for _, key := range tt.held {
				store.memberships[key] = true
			}
			if tt.account {
				store.usernames = map[impulse.UUID]string{userID: "dana"}
				store.sessions = map[impulse.UUID][]impulse.UUID{userID: {sessionA}}
				store.identities = map[impulse.UUID]impulse.UUID{identityA: userID}
			}
			m, _ := newManager(t, store, engineRoles, administrators)

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.RemoveRole(asCaller(ctx, tt.caller), txn, globalScope, userID, "Steward")
			})
			if (err != nil) != tt.wantErr {
				t.Fatalf("RemoveRole() error = %v, wantErr %t", err, tt.wantErr)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
		})
	}
}

// TestManager_callerOfAnotherAuth pins who the rules on acting on oneself take the caller
// for: the person of the account whose id the caller carries, when the request came
// through the auth managed. A caller who came through another auth, on an outlet that
// serves this auth's management, is nobody of this auth: carrying the very id of the
// person acted on they disable the account, delete it, and remove the last
// administrator's role, as any other caller may. A caller who came through this auth
// with that id is refused all three, and so is one whose request bound no auth at all,
// where the id is all there is to judge by. A caller of another id is another person,
// and so is one who carries the person's username: a name is what people read, and
// names nobody to the rules.
func TestManager_callerOfAnotherAuth(t *testing.T) {
	t.Parallel()

	// dana is the one administrator of the tenant and holds a second role, so the
	// removal of the first leaves the account in place.
	warden := access.NewMembershipKey(alphaScope, dana, "Warden")
	reader := access.NewMembershipKey(globalScope, dana, "Reader")
	acts := []struct {
		name       string
		act        func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error
		wantWrites []string
	}{
		{
			name: "disable",
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.DisableUser(ctx, txn, userID)
			},
			wantWrites: []string{"update user " + userID.String() + " disabled=true"},
		},
		{
			name: "delete",
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.DeleteUser(ctx, txn, userID)
			},
			wantWrites: []string{"delete user " + userID.String(), "delete membership " + membershipText(&warden), "delete membership " + membershipText(&reader)},
		},
		{
			name: "remove the last administrator's role",
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.RemoveRole(ctx, txn, alphaScope, userID, "Warden")
			},
			wantWrites: []string{"delete membership " + membershipText(&warden)},
		},
	}
	callers := []struct {
		name string
		ctx  func(context.Context) context.Context
		// wantRefused is whether the caller is taken for the person acted on.
		wantRefused bool
	}{
		{
			name: "through the auth managed, the caller of the account's id is the person",
			ctx: func(ctx context.Context) context.Context {
				return asCallerThrough(ctx, managedAuth, dana)
			},
			wantRefused: true,
		},
		{
			name: "through another auth, the same id is another auth's person",
			ctx: func(ctx context.Context) context.Context {
				return asCallerThrough(ctx, otherAuth, dana)
			},
		},
		{
			name: "with no auth bound, the id is judged alone",
			ctx: func(ctx context.Context) context.Context {
				return asCaller(ctx, dana)
			},
			wantRefused: true,
		},
		{
			name: "through the auth managed, another id is another person",
			ctx: func(ctx context.Context) context.Context {
				return asCallerThrough(ctx, managedAuth, erin)
			},
		},
		{
			name: "through the auth managed, the person's username is not the person",
			ctx: func(ctx context.Context) context.Context {
				return asCallerThrough(ctx, managedAuth, "dana")
			},
		},
	}
	for _, caller := range callers {
		for _, act := range acts {
			t.Run(caller.name+": "+act.name, func(t *testing.T) {
				t.Parallel()

				store := &fakeStore{
					usernames:   map[impulse.UUID]string{userID: "dana"},
					memberships: map[access.MembershipKey]bool{warden: true, reader: true},
				}
				m, _ := newManager(t, store, engineRoles, administrators)

				err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
					return act.act(caller.ctx(ctx), txn, m)
				})
				if caller.wantRefused {
					if err == nil || !httpio.HasForbidden(err) {
						t.Fatalf("error = %v, want the caller refused as the person acted on", err)
					}
					if len(store.writes) != 0 {
						t.Errorf("buffered writes = %v, want none", store.writes)
					}

					return
				}
				if err != nil {
					t.Fatalf("error = %v, want the act to go through", err)
				}
				if diff := cmp.Diff(act.wantWrites, store.writes); diff != "" {
					t.Errorf("buffered writes (-want +got):\n%s", diff)
				}
			})
		}
	}
}
