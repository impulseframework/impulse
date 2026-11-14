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

// callerPermissions is the checker a generated handler stamps on the context, as far as
// the rules read it: who the caller is. Every other method is the embedded nil
// interface's, and panics if a rule reaches for it.
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

// administrators are the roles the tests name as the administrators': Steward in the
// global scope and Warden in a tenant.
var administrators = WithAdministratorRoles([]accesstypes.Role{"Steward"}, []accesstypes.Role{"Warden"})

// TestManager_DisableUser_ownAccount pins the rule that nobody disables themselves: the
// request's caller is refused their own account, under whatever case the name is
// written, and anyone else's goes through, as does a call outside any request.
func TestManager_DisableUser_ownAccount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		caller     accesstypes.User
		wantErr    func(error) bool
		wantWrites []string
	}{
		{name: "the caller's own account is refused", caller: "dana", wantErr: httpio.HasForbidden},
		{name: "the name is compared without case", caller: "DANA", wantErr: httpio.HasForbidden},
		{name: "another person's account is disabled", caller: "erin", wantWrites: []string{"update user " + userID.String() + " disabled=true"}},
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
// from themselves: an administrator role nobody else would hold where it is held stays,
// and every other role goes; without the administrator roles a person removes no role
// from themselves; and the rule judges nobody but the caller.
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
			name: "the last administrator of the application keeps the role",
			opts: []Option{administrators}, caller: "dana", scope: globalScope, role: "Steward",
			held:    []access.MembershipKey{membership(globalScope, "dana", "Steward"), membership(globalScope, "erin", "Reader")},
			wantErr: httpio.HasForbidden,
		},
		{
			name: "an administrator gives the role up once another holds it",
			opts: []Option{administrators}, caller: "dana", scope: globalScope, role: "Steward",
			held:        []access.MembershipKey{membership(globalScope, "dana", "Steward"), membership(globalScope, "erin", "Steward")},
			wantRemoved: true,
		},
		{
			name: "a role that is no administrator role goes",
			opts: []Option{administrators}, caller: "dana", scope: globalScope, role: "Reader",
			held:        []access.MembershipKey{membership(globalScope, "dana", "Reader"), membership(globalScope, "dana", "Steward")},
			wantRemoved: true,
		},
		{
			name: "the last administrator of a tenant keeps the role",
			opts: []Option{administrators}, caller: "dana", scope: alphaScope, role: "Warden",
			held:    []access.MembershipKey{membership(alphaScope, "dana", "Warden"), membership(accesstypes.DomainPolicyScope("beta"), "erin", "Warden")},
			wantErr: httpio.HasForbidden,
		},
		{
			name: "a tenant's administrator leaves once another holds the role there",
			opts: []Option{administrators}, caller: "dana", scope: alphaScope, role: "Warden",
			held:        []access.MembershipKey{membership(alphaScope, "dana", "Warden"), membership(alphaScope, "erin", "Warden")},
			wantRemoved: true,
		},
		{
			name: "a person who holds the role in every tenant is the tenant's administrator too",
			opts: []Option{administrators}, caller: "dana", scope: alphaScope, role: "Warden",
			held:        []access.MembershipKey{membership(alphaScope, "dana", "Warden"), membership(everyScope, "erin", "Warden")},
			wantRemoved: true,
		},
		{
			name: "the caller's own membership in every tenant keeps them the tenant's administrator",
			opts: []Option{administrators}, caller: "dana", scope: alphaScope, role: "Warden",
			held:        []access.MembershipKey{membership(alphaScope, "dana", "Warden"), membership(everyScope, "dana", "Warden")},
			wantRemoved: true,
		},
		{
			name: "a role held in every tenant is no administrator role of the application",
			opts: []Option{administrators}, caller: "dana", scope: everyScope, role: "Warden",
			held:        []access.MembershipKey{membership(everyScope, "dana", "Warden"), membership(globalScope, "dana", "Reader")},
			wantRemoved: true,
		},
		{
			name:   "without the administrator roles a person removes no role from themselves",
			caller: "dana", scope: globalScope, role: "Reader",
			held:    []access.MembershipKey{membership(globalScope, "dana", "Reader"), membership(globalScope, "dana", "Steward")},
			wantErr: httpio.HasForbidden,
		},
		{
			name: "the rule judges nobody but the caller: another person removes the last administrator's role",
			opts: []Option{administrators}, caller: "erin", scope: globalScope, role: "Steward",
			held:        []access.MembershipKey{membership(globalScope, "dana", "Steward"), membership(globalScope, "dana", "Reader")},
			wantRemoved: true,
		},
		{
			name: "a call outside any request is the application's act",
			opts: []Option{administrators}, scope: globalScope, role: "Steward",
			held:        []access.MembershipKey{membership(globalScope, "dana", "Steward"), membership(globalScope, "dana", "Reader")},
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
				return m.RemoveRole(asCaller(ctx, tt.caller), txn, tt.scope, "dana", tt.role)
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
				removed := membership(tt.scope, "dana", tt.role)
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

// TestManager_RemoveRole_lastRole pins the rule on an account nothing admits: the
// removal of a person's last role anywhere deletes the account in the same transaction,
// its sessions ended and its identity links removed first, whoever removed the role; a
// person who holds a role somewhere else stays, and a name no user holds has no account
// to delete.
func TestManager_RemoveRole_lastRole(t *testing.T) {
	t.Parallel()

	steward := access.NewMembershipKey(globalScope, "dana", "Steward")
	reader := access.NewMembershipKey(alphaScope, "dana", "Reader")
	deleted := []string{
		"end session " + sessionA.String(),
		"delete identity " + identityA.String(),
		"delete user " + userID.String(),
		"delete membership global/ dana Steward",
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
			held: []access.MembershipKey{steward}, account: true, caller: "erin",
			wantWrites: deleted,
		},
		{
			name: "a call outside any request deletes the account the same",
			held: []access.MembershipKey{steward}, account: true,
			wantWrites: deleted,
		},
		{
			name: "a person who holds a role in a tenant stays",
			held: []access.MembershipKey{steward, reader}, account: true, caller: "erin",
			wantWrites: []string{"delete membership global/ dana Steward"},
		},
		{
			name:       "a name no user holds has no account to delete",
			held:       []access.MembershipKey{steward},
			wantWrites: []string{"delete membership global/ dana Steward"},
		},
		{
			name: "a failed delete fails the call",
			held: []access.MembershipKey{steward}, account: true, caller: "erin",
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
				return m.RemoveRole(asCaller(ctx, tt.caller), txn, globalScope, "dana", "Steward")
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

// TestManager_moveMemberships pins the rule a rename relies on: every membership held
// under the old name is removed and written under the new one, wherever it is held, and
// one the new name holds already is not written twice.
func TestManager_moveMemberships(t *testing.T) {
	t.Parallel()

	store := &fakeStore{memberships: map[access.MembershipKey]bool{
		access.NewMembershipKey(globalScope, "dana", "Steward"): true,
		access.NewMembershipKey(alphaScope, "dana", "Reader"):   true,
		access.NewMembershipKey(everyScope, "dana", "Reader"):   true,
		access.NewMembershipKey(alphaScope, "dana.r", "Reader"): true,
		access.NewMembershipKey(globalScope, "erin", "Steward"): true,
	}}
	m, _ := newManager(t, store, engineRoles)

	var moved bool
	if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		var err error
		moved, err = m.moveMemberships(ctx, txn, "dana", "dana.r")

		return err
	}); err != nil {
		t.Fatalf("moveMemberships() error = %v", err)
	}
	if !moved {
		t.Error("moveMemberships() moved = false, want true")
	}
	want := []string{
		"delete membership domain/alpha dana Reader",
		"delete membership every/ dana Reader",
		"delete membership global/ dana Steward",
		"insert membership every/ dana.r Reader",
		"insert membership global/ dana.r Steward",
	}
	if diff := cmp.Diff(want, store.writes); diff != "" {
		t.Errorf("buffered writes (-want +got):\n%s", diff)
	}

	store = &fakeStore{memberships: map[access.MembershipKey]bool{}}
	m, _ = newManager(t, store, engineRoles)
	if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		var err error
		moved, err = m.moveMemberships(ctx, txn, "dana", "dana.r")

		return err
	}); err != nil {
		t.Fatalf("moveMemberships() error = %v", err)
	}
	if moved || len(store.writes) != 0 {
		t.Errorf("a person who holds no role: moved = %t, writes = %v, want nothing", moved, store.writes)
	}
}
