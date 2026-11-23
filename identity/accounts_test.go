package identity

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// Fixed ids: a session that acts as a role, one that views as another user, and two
// trusted devices.
var (
	roleSession = mustUUID("d1d1d1d1-d1d1-4d1d-8d1d-d1d1d1d1d1d1")
	viewSession = mustUUID("e1e1e1e1-e1e1-4e1e-8e1e-e1e1e1e1e1e1")
	deviceA     = mustUUID("a3a3a3a3-a3a3-4a3a-8a3a-a3a3a3a3a3a3")
	deviceB     = mustUUID("b3b3b3b3-b3b3-4b3b-8b3b-b3b3b3b3b3b3")
)

// TestManager_RenameUser pins the rename: the user row, every live session that carries
// the name (the person's own and the ones they hold that act as a role), and the actor's
// name on every standing record they hold, found under the old name. No membership is
// written: the permission store names the person by the id of their account, so the
// roles stay where they are and the engine is told nothing. A name another user holds
// is a conflict, and the same name is nothing to do.
func TestManager_RenameUser(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		username   string
		prepare    func(store *fakeStore)
		failOn     string
		wantErr    func(error) bool
		wantWrites []string
	}{
		{
			name:     "a person with sessions, records and roles: the sessions and the records carry the new name, and no membership is written",
			username: "  dana.r ",
			prepare: func(store *fakeStore) {
				store.sessions = map[impulse.UUID][]impulse.UUID{userID: {sessionA}}
				store.heldBy = map[string][]impulse.UUID{"dana": {roleSession, viewSession}}
				store.actsAsRole = map[impulse.UUID]bool{roleSession: true}
				store.memberships = map[access.MembershipKey]bool{
					access.NewMembershipKey(globalScope, dana, "Steward"): true,
					access.NewMembershipKey(alphaScope, dana, "Reader"):   true,
				}
			},
			wantWrites: []string{
				"update user " + userID.String() + " username=dana.r",
				"rename session " + sessionA.String() + " dana.r",
				"rename session " + roleSession.String() + " dana.r",
				"rename actor " + roleSession.String() + " dana.r",
				"rename actor " + viewSession.String() + " dana.r",
			},
		},
		{
			name:     "a session that acts as a role and has ended keeps its name; its record follows",
			username: "dana.r",
			prepare: func(store *fakeStore) {
				store.heldBy = map[string][]impulse.UUID{"dana": {roleSession}}
				store.actsAsRole = map[impulse.UUID]bool{roleSession: true}
				store.ended = map[impulse.UUID]bool{roleSession: true}
			},
			wantWrites: []string{
				"update user " + userID.String() + " username=dana.r",
				"rename actor " + roleSession.String() + " dana.r",
			},
		},
		{
			name:       "a person with no session, record or role: the user row alone",
			username:   "dana.r",
			wantWrites: []string{"update user " + userID.String() + " username=dana.r"},
		},
		{
			name:       "the same name in another case is the person's own",
			username:   "Dana",
			wantWrites: []string{"update user " + userID.String() + " username=Dana"},
		},
		{
			name:     "the name the person holds already is nothing to do",
			username: "dana",
		},
		{
			name:     "a name another user holds is a conflict",
			username: "erin",
			wantErr: func(err error) bool {
				return httpio.HasConflict(err) && httpio.Message(err) == "Another account already has this username. Choose another."
			},
		},
		{
			name:     "a blank name is refused",
			username: "  ",
			wantErr:  httpio.HasBadRequest,
		},
		{
			name:     "a name with a control character in it is refused in words",
			username: "dana\x00r",
			wantErr:  refusedInWords("A username is text on one line, with no tab or other control character."),
		},
		{
			name:     "a name longer than a typed username may be is refused in words",
			username: strings.Repeat("d", sessioninfo.MaxUsernameLength+1),
			wantErr:  refusedInWords(fmt.Sprintf("A username is at most %d characters.", sessioninfo.MaxUsernameLength)),
		},
		{
			name:     "a failed write fails the call",
			username: "dana.r",
			failOn:   "UpdateUser",
			wantErr:  func(err error) bool { return errors.Is(err, errStore) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{usernames: map[impulse.UUID]string{userID: "dana", otherUser: "erin"}, failOn: tt.failOn}
			if tt.prepare != nil {
				tt.prepare(store)
			}
			m, engine := newManager(t, store, engineRoles)

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.RenameUser(ctx, txn, userID, tt.username)
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("RenameUser() error = %v, want the refusal", err)
				}
			} else {
				if err != nil {
					t.Fatalf("RenameUser() error = %v", err)
				}
				if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
					t.Errorf("buffered writes (-want +got):\n%s", diff)
				}
			}
			if engine.changed != 0 {
				t.Errorf("the engine was told %d times, want none: a rename changes nothing the engine holds", engine.changed)
			}
		})
	}

	t.Run("a user that does not exist is not found", func(t *testing.T) {
		t.Parallel()

		m, _ := newManager(t, &fakeStore{}, engineRoles)
		err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			return m.RenameUser(ctx, txn, userID, "dana.r")
		})
		if !httpio.HasNotFound(err) {
			t.Errorf("RenameUser() error = %v, want not found", err)
		}
	})
}

// refusedInWords is a check of an error: a Bad Request that says words to the person.
func refusedInWords(words string) func(error) bool {
	return func(err error) bool {
		return httpio.HasBadRequest(err) && httpio.Message(err) == words
	}
}

// TestManager_RenameUser_rolesStay pins what a rename leaves alone: the memberships. The
// permission store names a person by the id of their account, which a rename does not
// change, so every membership the person holds, in the global scope, in a tenant and in
// every tenant, is theirs after the rename as it was before, none is removed and none is
// written under any name, another person's are untouched, and the permission engine is
// told nothing for a rename alone.
func TestManager_RenameUser_rolesStay(t *testing.T) {
	t.Parallel()

	held := []access.MembershipKey{
		access.NewMembershipKey(alphaScope, dana, "Reader"),
		access.NewMembershipKey(everyScope, dana, "Reader"),
		access.NewMembershipKey(globalScope, dana, "Steward"),
	}
	store := &fakeStore{
		usernames:   map[impulse.UUID]string{userID: "dana", otherUser: "erin"},
		memberships: map[access.MembershipKey]bool{access.NewMembershipKey(globalScope, erin, "Steward"): true},
	}
	for _, key := range held {
		store.memberships[key] = true
	}
	m, engine := newManager(t, store, engineRoles)

	var after []access.MembershipKey
	if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		if err := m.RenameUser(ctx, txn, userID, "dana.r"); err != nil {
			return err
		}
		var err error
		after, err = store.Memberships(ctx, txn, userID)

		return err
	}); err != nil {
		t.Fatalf("RenameUser() error = %v", err)
	}
	if diff := cmp.Diff([]string{"update user " + userID.String() + " username=dana.r"}, store.writes); diff != "" {
		t.Errorf("buffered writes (-want +got): the user row alone, and no membership\n%s", diff)
	}
	if diff := cmp.Diff(held, after); diff != "" {
		t.Errorf("the memberships the person holds after the rename (-want +got):\n%s", diff)
	}
	if engine.changed != 0 {
		t.Errorf("the engine was told %d times, want none: a rename changes nothing the engine holds", engine.changed)
	}
}

// TestManager_DeleteUser pins the delete: the sessions ended, the identity links and the
// row removed, every membership held under the account's id removed with them and the
// engine told; nobody deletes themselves; a user that does not exist is not found.
func TestManager_DeleteUser(t *testing.T) {
	t.Parallel()

	whole := []string{
		"end session " + sessionA.String(),
		"delete identity " + identityA.String(),
		"delete user " + userID.String(),
		"delete membership domain/alpha " + userID.String() + " Reader",
		"delete membership global/ " + userID.String() + " Steward",
	}

	tests := []struct {
		name        string
		caller      accesstypes.User
		missing     bool
		noRoles     bool
		wantErr     func(error) bool
		wantWrites  []string
		wantChanged int
	}{
		{name: "another person's account goes with everything that names it", caller: erin, wantWrites: whole, wantChanged: 1},
		{name: "a call outside any request is the application's act", wantWrites: whole, wantChanged: 1},
		{name: "a person who holds no role: the engine is not told", caller: erin, noRoles: true, wantWrites: whole[:3]},
		{name: "the caller's own account is refused: the caller carries the account's id", caller: dana, wantErr: httpio.HasForbidden},
		{name: "a user that does not exist is not found", caller: erin, missing: true, wantErr: httpio.HasNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{
				usernames:   map[impulse.UUID]string{userID: "dana"},
				sessions:    map[impulse.UUID][]impulse.UUID{userID: {sessionA}},
				identities:  map[impulse.UUID]impulse.UUID{identityA: userID},
				memberships: map[access.MembershipKey]bool{},
			}
			if !tt.noRoles {
				store.memberships[access.NewMembershipKey(globalScope, dana, "Steward")] = true
				store.memberships[access.NewMembershipKey(alphaScope, dana, "Reader")] = true
			}
			if tt.missing {
				store.usernames = nil
			}
			m, engine := newManager(t, store, engineRoles)

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.DeleteUser(asCaller(ctx, tt.caller), txn, userID)
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("DeleteUser() error = %v, want the refusal", err)
				}
			} else if err != nil {
				t.Fatalf("DeleteUser() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
			if engine.changed != tt.wantChanged {
				t.Errorf("the engine was told %d times, want %d", engine.changed, tt.wantChanged)
			}
		})
	}
}

// TestManager_RevokeTrustedDevices pins the second factor's reset: every browser the
// person asked to be remembered that is not revoked yet is revoked, and nothing else is
// touched.
func TestManager_RevokeTrustedDevices(t *testing.T) {
	t.Parallel()

	store := &fakeStore{
		devices:  map[impulse.UUID][]impulse.UUID{userID: {deviceA, deviceB}},
		sessions: map[impulse.UUID][]impulse.UUID{userID: {sessionA}},
	}
	m, _ := newManager(t, store, nil)
	if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		return m.RevokeTrustedDevices(ctx, txn, userID)
	}); err != nil {
		t.Fatalf("RevokeTrustedDevices() error = %v", err)
	}
	want := []string{"revoke device " + deviceA.String(), "revoke device " + deviceB.String()}
	if diff := cmp.Diff(want, store.writes); diff != "" {
		t.Errorf("buffered writes (-want +got):\n%s", diff)
	}

	// The person is told once the reset has committed, and not when it did not.
	mail := &fakeMail{}
	store = &fakeStore{devices: map[impulse.UUID][]impulse.UUID{userID: {deviceA}}}
	told, err := New(managedAuth, store, &fakeEngine{}, fakeSessions{mail: mail})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		err := told.RevokeTrustedDevices(ctx, txn, userID)
		if len(mail.asked) != 0 {
			t.Errorf("before the commit the session library was asked %v, want nothing", mail.asked)
		}

		return err
	}); err != nil {
		t.Fatalf("RevokeTrustedDevices() error = %v", err)
	}
	if diff := cmp.Diff([]string{"factor reset notice to " + userID.String()}, mail.asked); diff != "" {
		t.Errorf("notices after the commit (-want +got):\n%s", diff)
	}
	mail.asked = nil
	if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		if err := told.RevokeTrustedDevices(ctx, txn, userID); err != nil {
			return err
		}

		return errStore
	}); !errors.Is(err, errStore) {
		t.Fatalf("a transaction that fails after the reset: error = %v, want its failure", err)
	}
	if len(mail.asked) != 0 {
		t.Errorf("notices of a reset that did not commit = %v, want none", mail.asked)
	}

	store = &fakeStore{failOn: "RevokeTrustedDevice", devices: map[impulse.UUID][]impulse.UUID{userID: {deviceA}}}
	m, _ = newManager(t, store, nil)
	err = inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		return m.RevokeTrustedDevices(ctx, txn, userID)
	})
	if !errors.Is(err, errStore) {
		t.Errorf("RevokeTrustedDevices() error = %v, want the store's failure", err)
	}
}

// TestManager_UnlockSignIn pins the cleared lockout: the row of the name is removed,
// keyed as the limiter keys it, and a blank name is refused.
func TestManager_UnlockSignIn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		typed      string
		wantErr    func(error) bool
		wantWrites []string
	}{
		{name: "the name's count is cleared", typed: "dana", wantWrites: []string{"clear attempts dana"}},
		{name: "the name is keyed without case, as the limiter keys it", typed: "Dana@Example.COM", wantWrites: []string{"clear attempts dana@example.com"}},
		// The list also shows the session library's own counts, under keys no name folds
		// to: such a row is cleared as it is listed.
		{name: "a listed count of the mail an address was sent is cleared as it is listed", typed: "MAIL:dana@example.com", wantWrites: []string{"clear attempts MAIL:dana@example.com"}},
		{name: "a listed count of an address's reset links is cleared as it is listed", typed: "RESET:dana@example.com", wantWrites: []string{"clear attempts RESET:dana@example.com"}},
		{name: "a name that spells such a key without its capitals is a name", typed: "Reset:dana@example.com", wantWrites: []string{"clear attempts reset:dana@example.com"}},
		{name: "a blank name is refused", typed: "  ", wantErr: httpio.HasBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{}
			m, _ := newManager(t, store, nil)
			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.UnlockSignIn(ctx, txn, tt.typed)
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("UnlockSignIn() error = %v, want the refusal", err)
				}
			} else if err != nil {
				t.Fatalf("UnlockSignIn() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
		})
	}
}

// TestManager_SendPasswordResetLink pins an administrator's reset link: nothing about the
// account is written, the session library is asked to mail the link once the
// transaction has committed, saying who asked, and the caller is told how it went.
func TestManager_SendPasswordResetLink(t *testing.T) {
	t.Parallel()

	inAlpha := func(ctx context.Context) context.Context {
		return resource.WithCaller(ctx, &resource.Caller{Permissions: callerPermissions{user: erin}, Scope: accesstypes.DomainScope("alpha")})
	}
	noPassword := httpio.NewConflictMessage("No link was sent: the account has no password to reset, or no confirmed email address to send the link to")
	tests := []struct {
		name       string
		user       impulse.UUID
		caller     func(ctx context.Context) context.Context
		failOn     string
		mailErr    error
		wantErr    func(error) bool
		wantAsked  []string
		wantSent   bool
		wantReason string
	}{
		{name: "an administrator of the application", user: userID, wantAsked: []string{"password reset of " + userID.String() + " asked by the application"}, wantSent: true},
		{name: "an administrator of a tenant is named by the tenant", user: userID, caller: inAlpha, wantAsked: []string{"password reset of " + userID.String() + " asked by tenant alpha"}, wantSent: true},
		{name: "a user that does not exist is not found, and nothing is mailed", user: createdID, wantErr: httpio.HasNotFound},
		{name: "a failed read fails the call", user: userID, failOn: "Username", wantErr: func(err error) bool { return errors.Is(err, errStore) }},
		{
			name: "an account with nothing to reset is said in the session library's sentence", user: userID, mailErr: noPassword,
			wantReason: "No link was sent: the account has no password to reset, or no confirmed email address to send the link to",
		},
		{name: "any other failure is a message that could not be sent", user: userID, mailErr: errStore, wantReason: "The message could not be sent. Try again in a few minutes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{usernames: map[impulse.UUID]string{userID: "dana"}, failOn: tt.failOn}
			mail := &fakeMail{err: tt.mailErr}
			m, err := New(managedAuth, store, &fakeEngine{}, fakeSessions{mail: mail})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			told, sent, reason := 0, false, ""
			err = inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				if tt.caller != nil {
					ctx = tt.caller(ctx)
				}
				err := m.SendPasswordResetLink(ctx, txn, tt.user, func(s bool, r string) { told, sent, reason = told+1, s, r })
				if len(mail.asked) != 0 || told != 0 {
					t.Errorf("before the commit: asked %v, told %d time(s), want nothing mailed until the transaction commits", mail.asked, told)
				}

				return err
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("SendPasswordResetLink() error = %v, want the refusal", err)
				}
				if len(mail.asked) != 0 || told != 0 {
					t.Errorf("a refused send: asked %v, told %d time(s), want nothing", mail.asked, told)
				}

				return
			}
			if err != nil {
				t.Fatalf("SendPasswordResetLink() error = %v", err)
			}
			if len(store.writes) != 0 {
				t.Errorf("buffered writes = %v, want none: nothing about the account changes when the link is sent", store.writes)
			}
			if diff := cmp.Diff(tt.wantAsked, mail.asked); diff != "" {
				t.Errorf("asked of the session library (-want +got):\n%s", diff)
			}
			if told != 1 || sent != tt.wantSent || reason != tt.wantReason {
				t.Errorf("told %d time(s): sent %v, reason %q; want once: sent %v, reason %q", told, sent, reason, tt.wantSent, tt.wantReason)
			}
		})
	}
}

// TestManager_passwordWritesEndResetLinks pins what a write of a person's password owes
// the reset links mailed before it: once the write has committed, and not when it did
// not, the session library is asked to withdraw them, so a link mailed for the password
// before does not reset the one after.
func TestManager_passwordWritesEndResetLinks(t *testing.T) {
	t.Parallel()

	acts := map[string]func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error{
		"an administrator removes the password of a person who signs in through a directory": func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
			return m.RemovePassword(ctx, txn, userID)
		},
	}
	for name, act := range acts {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{
				usernames:  map[impulse.UUID]string{userID: "dana"},
				passwords:  map[impulse.UUID]bool{userID: true},
				identities: map[impulse.UUID]impulse.UUID{identityA: userID},
			}
			mail := &fakeMail{}
			m, err := New(managedAuth, store, &fakeEngine{}, fakeSessions{mail: mail})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				err := act(ctx, txn, m)
				if len(mail.asked) != 0 {
					t.Errorf("before the commit the session library was asked %v, want nothing", mail.asked)
				}

				return err
			}); err != nil {
				t.Fatalf("error = %v", err)
			}
			if diff := cmp.Diff([]string{"end the password reset links of " + userID.String()}, mail.asked); diff != "" {
				t.Errorf("asked of the session library after the commit (-want +got):\n%s", diff)
			}

			mail.asked = nil
			if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				if err := act(ctx, txn, m); err != nil {
					return err
				}

				return errStore
			}); !errors.Is(err, errStore) {
				t.Fatalf("a transaction that fails after the write: error = %v, want its failure", err)
			}
			if len(mail.asked) != 0 {
				t.Errorf("asked of the session library for a write that did not commit = %v, want nothing", mail.asked)
			}
		})
	}
}
