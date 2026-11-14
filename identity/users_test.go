package identity

import (
	"context"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
)

// Fixed ids: a user and two of their sessions.
var (
	userID    = mustUUID("11111111-1111-4111-8111-111111111111")
	sessionA  = mustUUID("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	sessionB  = mustUUID("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	createdID = mustUUID("22222222-2222-4222-8222-222222222222")
)

func mustUUID(s string) impulse.UUID {
	id, err := impulse.UUIDFromString(s)
	if err != nil {
		panic(err)
	}

	return id
}

func ptr[T any](v T) *T {
	return &v
}

// TestManager_CreateUser pins the rule on a new user: a username, an address written
// bare or refused, at least one role, each written as a membership beside the row in
// the same transaction, and the id of the buffered row answered.
func TestManager_CreateUser(t *testing.T) {
	t.Parallel()

	steward := []accesstypes.Role{"Steward"}

	tests := []struct {
		name        string
		account     NewAccount
		failOn      string
		wantErr     func(error) bool
		wantWrites  []string
		wantChanged int
	}{
		{
			name:        "a user without an address, holding a global role",
			account:     NewAccount{Username: "dana", Scope: globalScope, Roles: steward},
			wantWrites:  []string{"insert user dana <none>", "insert membership global/ dana Steward"},
			wantChanged: 1,
		},
		{
			name:        "an address is written bare",
			account:     NewAccount{Username: "dana", Email: ptr("  Dana Reyes <dana@example.com> "), Scope: globalScope, Roles: steward},
			wantWrites:  []string{"insert user dana dana@example.com", "insert membership global/ dana Steward"},
			wantChanged: 1,
		},
		{
			name:        "the username is written without the space around it, and the membership under the same name",
			account:     NewAccount{Username: "  dana ", Scope: alphaScope, Roles: []accesstypes.Role{"Reader"}},
			wantWrites:  []string{"insert user dana <none>", "insert membership domain/alpha dana Reader"},
			wantChanged: 1,
		},
		{
			name:        "a role held in every domain",
			account:     NewAccount{Username: "dana", Scope: everyScope, Roles: []accesstypes.Role{"Reader"}},
			wantWrites:  []string{"insert user dana <none>", "insert membership every/ dana Reader"},
			wantChanged: 1,
		},
		{
			name:        "a role named twice is one membership",
			account:     NewAccount{Username: "dana", Scope: globalScope, Roles: []accesstypes.Role{"Steward", "Steward"}},
			wantWrites:  []string{"insert user dana <none>", "insert membership global/ dana Steward"},
			wantChanged: 1,
		},
		{
			name:    "no role is refused: nothing is written",
			account: NewAccount{Username: "dana", Scope: globalScope},
			wantErr: httpio.HasBadRequest,
		},
		{
			name:    "a role that does not exist where it is given is refused",
			account: NewAccount{Username: "dana", Scope: globalScope, Roles: []accesstypes.Role{"Reader"}},
			wantErr: httpio.HasNotFound,
			// The row was buffered before the role was judged; the error rolls the
			// transaction back, so it is never written.
			wantWrites: []string{"insert user dana <none>"},
		},
		{
			name:    "a value that is no address is refused",
			account: NewAccount{Username: "dana", Email: ptr("dana at example"), Scope: globalScope, Roles: steward},
			wantErr: httpio.HasBadRequest,
		},
		{
			name:    "an empty address is refused, not written as none",
			account: NewAccount{Username: "dana", Email: ptr(""), Scope: globalScope, Roles: steward},
			wantErr: httpio.HasBadRequest,
		},
		{
			name:    "a blank username is refused",
			account: NewAccount{Username: "   ", Scope: globalScope, Roles: steward},
			wantErr: httpio.HasBadRequest,
		},
		{
			name:    "the store's failure is the answer",
			account: NewAccount{Username: "dana", Scope: globalScope, Roles: steward},
			failOn:  "InsertUser",
			wantErr: func(err error) bool { return errors.Is(err, errStore) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{nextID: createdID, failOn: tt.failOn}
			m, engine := newManager(t, store, engineRoles)

			var got impulse.UUID
			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				id, err := m.CreateUser(ctx, txn, &tt.account)
				got = id

				return err
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("CreateUser() error = %v, want the refusal", err)
				}
				if got != impulse.NilUUID {
					t.Errorf("CreateUser() = %s beside an error, want the nil id", got)
				}
			} else {
				if err != nil {
					t.Fatalf("CreateUser() error = %v", err)
				}
				if got != createdID {
					t.Errorf("CreateUser() = %s, want the id the row is created under (%s)", got, createdID)
				}
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

// TestManager_DisableUser pins the shared rule: disabling a user ends every session they
// hold, in the same transaction, and a failure anywhere is the transaction's.
func TestManager_DisableUser(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		sessions   []impulse.UUID
		heldBy     []impulse.UUID
		failOn     string
		wantErr    bool
		wantWrites []string
	}{
		{
			name:     "the user is disabled and each live session ended",
			sessions: []impulse.UUID{sessionA, sessionB},
			wantWrites: []string{
				"end session " + sessionA.String(),
				"end session " + sessionB.String(),
				"update user " + userID.String() + " disabled=true",
			},
		},
		{
			name:     "the sessions the user holds as someone else end too, with their records",
			sessions: []impulse.UUID{sessionA},
			heldBy:   []impulse.UUID{sessionB},
			wantWrites: []string{
				"end session " + sessionA.String(),
				"end session " + sessionB.String(),
				"end impersonation " + sessionB.String() + " Revoked",
				"update user " + userID.String() + " disabled=true",
			},
		},
		{
			name:       "a user with no session is disabled all the same",
			wantWrites: []string{"update user " + userID.String() + " disabled=true"},
		},
		{
			name:     "a failed read of the sessions buffers nothing",
			sessions: []impulse.UUID{sessionA},
			failOn:   "LiveSessions",
			wantErr:  true,
		},
		{
			name:     "a failed session end fails the call",
			sessions: []impulse.UUID{sessionA},
			failOn:   "EndSession",
			wantErr:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{
				sessions:  map[impulse.UUID][]impulse.UUID{userID: tt.sessions},
				usernames: map[impulse.UUID]string{userID: "dana"},
				heldBy:    map[string][]impulse.UUID{"dana": tt.heldBy},
				records:   map[impulse.UUID]bool{},
				failOn:    tt.failOn,
			}
			for _, id := range tt.heldBy {
				store.records[id] = true
			}
			m, _ := newManager(t, store, nil)

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.DisableUser(ctx, txn, userID)
			})
			if (err != nil) != tt.wantErr {
				t.Fatalf("DisableUser() error = %v, wantErr %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
		})
	}
}

// TestManager_EnableUser pins the inverse: the user is enabled, and no session is
// touched, since the ones that ended stay ended.
func TestManager_EnableUser(t *testing.T) {
	t.Parallel()

	store := &fakeStore{sessions: map[impulse.UUID][]impulse.UUID{userID: {sessionA}}}
	m, _ := newManager(t, store, nil)

	if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		return m.EnableUser(ctx, txn, userID)
	}); err != nil {
		t.Fatalf("EnableUser() error = %v", err)
	}
	if diff := cmp.Diff([]string{"update user " + userID.String() + " disabled=false"}, store.writes); diff != "" {
		t.Errorf("buffered writes (-want +got):\n%s", diff)
	}
}

// TestManager_SetEmail pins an address entered by hand: written bare, taken off by nil,
// refused when it is no address, and not found for a user that does not exist.
func TestManager_SetEmail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		user       impulse.UUID
		email      *string
		failOn     string
		wantErr    func(error) bool
		wantWrites []string
	}{
		{name: "an address is written bare", user: userID, email: ptr("  Dana Reyes <dana@example.com> "), wantWrites: []string{"update user " + userID.String() + " email=dana@example.com"}},
		{name: "no address takes the one held off", user: userID, wantWrites: []string{"update user " + userID.String() + " email=<none>"}},
		{name: "a value that is no address is refused", user: userID, email: ptr("dana"), wantErr: httpio.HasBadRequest},
		{name: "an empty value is no address", user: userID, email: ptr(""), wantErr: httpio.HasBadRequest},
		{name: "a user that does not exist is not found", user: createdID, email: ptr("dana@example.com"), wantErr: httpio.HasNotFound},
		{name: "a failed read fails the call", user: userID, email: ptr("dana@example.com"), failOn: "Username", wantErr: func(err error) bool { return errors.Is(err, errStore) }},
		{name: "a failed write fails the call", user: userID, email: ptr("dana@example.com"), failOn: "UpdateUser", wantErr: func(err error) bool { return errors.Is(err, errStore) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{usernames: map[impulse.UUID]string{userID: "dana"}, failOn: tt.failOn}
			m, _ := newManager(t, store, nil)

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.SetEmail(ctx, txn, tt.user, tt.email)
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("SetEmail() error = %v, want the refusal", err)
				}
			} else if err != nil {
				t.Fatalf("SetEmail() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
		})
	}
}
