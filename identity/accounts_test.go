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

// Fixed ids: a session that acts as a role, one that views as another user, and two
// trusted devices.
var (
	roleSession = mustUUID("d1d1d1d1-d1d1-4d1d-8d1d-d1d1d1d1d1d1")
	viewSession = mustUUID("e1e1e1e1-e1e1-4e1e-8e1e-e1e1e1e1e1e1")
	deviceA     = mustUUID("a3a3a3a3-a3a3-4a3a-8a3a-a3a3a3a3a3a3")
	deviceB     = mustUUID("b3b3b3b3-b3b3-4b3b-8b3b-b3b3b3b3b3b3")
)

// TestManager_RenameUser pins the rename: the user row, every live session that carries
// the name (the person's own and the ones they hold that act as a role), the actor's
// name on every standing record they hold, and every membership, moved to the new name;
// a name another user holds is a conflict, the same name is nothing to do, and the
// engine is told when a membership moved.
func TestManager_RenameUser(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		username    string
		prepare     func(store *fakeStore)
		failOn      string
		wantErr     func(error) bool
		wantWrites  []string
		wantChanged int
	}{
		{
			name:     "a person with sessions, records and roles keeps all of them under the new name",
			username: "  dana.r ",
			prepare: func(store *fakeStore) {
				store.sessions = map[impulse.UUID][]impulse.UUID{userID: {sessionA}}
				store.heldBy = map[string][]impulse.UUID{"dana": {roleSession, viewSession}}
				store.actsAsRole = map[impulse.UUID]bool{roleSession: true}
				store.memberships = map[access.MembershipKey]bool{
					access.NewMembershipKey(globalScope, "dana", "Steward"): true,
					access.NewMembershipKey(alphaScope, "dana", "Reader"):   true,
				}
			},
			wantWrites: []string{
				"delete membership domain/alpha dana Reader",
				"delete membership global/ dana Steward",
				"insert membership domain/alpha dana.r Reader",
				"insert membership global/ dana.r Steward",
				"update user " + userID.String() + " username=dana.r",
				"rename session " + sessionA.String() + " dana.r",
				"rename session " + roleSession.String() + " dana.r",
				"rename actor " + roleSession.String() + " dana.r",
				"rename actor " + viewSession.String() + " dana.r",
			},
			wantChanged: 1,
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
			name:       "a person who holds no role: the engine is not told",
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
			wantErr:  httpio.HasConflict,
		},
		{
			name:     "a blank name is refused",
			username: "  ",
			wantErr:  httpio.HasBadRequest,
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
			if engine.changed != tt.wantChanged {
				t.Errorf("the engine was told %d times, want %d", engine.changed, tt.wantChanged)
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

// TestManager_DeleteUser pins the delete: the sessions ended, the identity links and the
// row removed, every membership the person holds removed with them and the engine told;
// nobody deletes themselves; a user that does not exist is not found.
func TestManager_DeleteUser(t *testing.T) {
	t.Parallel()

	whole := []string{
		"end session " + sessionA.String(),
		"delete identity " + identityA.String(),
		"delete user " + userID.String(),
		"delete membership domain/alpha dana Reader",
		"delete membership global/ dana Steward",
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
		{name: "another person's account goes with everything that names it", caller: "erin", wantWrites: whole, wantChanged: 1},
		{name: "a call outside any request is the application's act", wantWrites: whole, wantChanged: 1},
		{name: "a person who holds no role: the engine is not told", caller: "erin", noRoles: true, wantWrites: whole[:3]},
		{name: "the caller's own account is refused", caller: "Dana", wantErr: httpio.HasForbidden},
		{name: "a user that does not exist is not found", caller: "erin", missing: true, wantErr: httpio.HasNotFound},
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
				store.memberships[access.NewMembershipKey(globalScope, "dana", "Steward")] = true
				store.memberships[access.NewMembershipKey(alphaScope, "dana", "Reader")] = true
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

// TestManager_SetTemporaryPassword pins the temporary password: the session library's
// hash written with the mark that the person must change it, their sessions ended, and
// the count of failed sign-ins kept under their name cleared, the name keyed as the
// limiter keys it; an empty password and a user that does not exist are refused.
func TestManager_SetTemporaryPassword(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		username   string
		password   string
		wantErr    func(error) bool
		wantWrites []string
	}{
		{
			name:     "the password is set, the sessions end and the name's count is cleared",
			username: "Dana", password: "first-day",
			wantWrites: []string{
				"end session " + sessionA.String(),
				"set password " + userID.String() + " hash(first-day) changeRequired=true",
				"clear attempts dana",
			},
		},
		{name: "an empty password is refused", username: "dana", wantErr: httpio.HasBadRequest},
		{name: "a user that does not exist is not found", password: "first-day", wantErr: httpio.HasNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{sessions: map[impulse.UUID][]impulse.UUID{userID: {sessionA}}}
			if tt.username != "" {
				store.usernames = map[impulse.UUID]string{userID: tt.username}
			}
			m, _ := newManager(t, store, nil)

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.SetTemporaryPassword(ctx, txn, userID, tt.password)
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("SetTemporaryPassword() error = %v, want the refusal", err)
				}
			} else if err != nil {
				t.Fatalf("SetTemporaryPassword() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
		})
	}
}

// TestManager_CreateUser_password pins the password at a create: a temporary password
// is written with the mark beside the row; none is a person who signs in another way;
// where the password is the only way in one is required; an empty one is never a
// password.
func TestManager_CreateUser_password(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		password   *string
		required   bool
		wantErr    func(error) bool
		wantWrites []string
	}{
		{
			name:     "a temporary password is written beside the row, to be changed",
			password: ptr("first-day"),
			wantWrites: []string{
				"insert user dana <none>",
				"set password " + createdID.String() + " hash(first-day) changeRequired=true",
				"insert membership global/ dana Steward",
			},
		},
		{
			name:       "no password is a person who signs in another way",
			wantWrites: []string{"insert user dana <none>", "insert membership global/ dana Steward"},
		},
		{name: "an empty password is refused", password: ptr(""), wantErr: httpio.HasBadRequest},
		{name: "where the password is the only way in, none is refused", required: true, wantErr: httpio.HasBadRequest},
		{name: "where the password is the only way in, an empty one is refused", required: true, password: ptr(""), wantErr: httpio.HasBadRequest},
		{
			name:     "where the password is the only way in, one is written",
			required: true, password: ptr("first-day"),
			wantWrites: []string{
				"insert user dana <none>",
				"set password " + createdID.String() + " hash(first-day) changeRequired=true",
				"insert membership global/ dana Steward",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{nextID: createdID}
			m, _ := newManager(t, store, engineRoles)

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				_, err := m.CreateUser(ctx, txn, &NewAccount{
					Username: "dana", Scope: globalScope, Roles: []accesstypes.Role{"Steward"},
					Password: tt.password, PasswordRequired: tt.required,
				})

				return err
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("CreateUser() error = %v, want the refusal", err)
				}
			} else if err != nil {
				t.Fatalf("CreateUser() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
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

	store = &fakeStore{failOn: "RevokeTrustedDevice", devices: map[impulse.UUID][]impulse.UUID{userID: {deviceA}}}
	m, _ = newManager(t, store, nil)
	err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
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
