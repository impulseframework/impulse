package identity

import (
	"context"
	"strings"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session/sessioninfo"
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
// bare or refused, at least one role, each written as a membership under the new user's
// id beside the row in the same transaction, and the id of the buffered row answered.
func TestManager_CreateUser(t *testing.T) {
	t.Parallel()

	steward := []accesstypes.Role{"Steward"}
	id := createdID.String()

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
			wantWrites:  []string{"insert user dana <none>", "insert membership global/ " + id + " Steward"},
			wantChanged: 1,
		},
		{
			name:        "an address is written bare",
			account:     NewAccount{Username: "dana", Email: ptr("  Dana Reyes <dana@example.com> "), Scope: globalScope, Roles: steward},
			wantWrites:  []string{"insert user dana dana@example.com", "insert membership global/ " + id + " Steward"},
			wantChanged: 1,
		},
		{
			name:        "a name is written without the space around it",
			account:     NewAccount{Username: "dana", Name: ptr("  Dana Reyes "), Scope: globalScope, Roles: steward},
			wantWrites:  []string{"insert user dana <none> name=Dana Reyes", "insert membership global/ " + id + " Steward"},
			wantChanged: 1,
		},
		{
			name:        "a name of nothing but space is no name",
			account:     NewAccount{Username: "dana", Name: ptr("   "), Scope: globalScope, Roles: steward},
			wantWrites:  []string{"insert user dana <none>", "insert membership global/ " + id + " Steward"},
			wantChanged: 1,
		},
		{
			name:    "a name too long to keep is refused, and nothing is written",
			account: NewAccount{Username: "dana", Name: ptr(strings.Repeat("n", sessioninfo.MaxNameLength+1)), Scope: globalScope, Roles: steward},
			wantErr: httpio.HasBadRequest,
		},
		{
			name:        "the username is written without the space around it, and the membership under the id, whatever the name",
			account:     NewAccount{Username: "  dana ", Scope: alphaScope, Roles: []accesstypes.Role{"Reader"}},
			wantWrites:  []string{"insert user dana <none>", "insert membership domain/alpha " + id + " Reader"},
			wantChanged: 1,
		},
		{
			name:        "a role held in every domain",
			account:     NewAccount{Username: "dana", Scope: everyScope, Roles: []accesstypes.Role{"Reader"}},
			wantWrites:  []string{"insert user dana <none>", "insert membership every/ " + id + " Reader"},
			wantChanged: 1,
		},
		{
			name:        "a role named twice is one membership",
			account:     NewAccount{Username: "dana", Scope: globalScope, Roles: []accesstypes.Role{"Steward", "Steward"}},
			wantWrites:  []string{"insert user dana <none>", "insert membership global/ " + id + " Steward"},
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

// TestManager_SetName pins a person's name written by an operator: without the space
// around it, taken off by nil or by nothing but space, refused when it is too long, and
// not found for a user that does not exist. Nothing follows a name: the one write is
// the user's row.
func TestManager_SetName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		user       impulse.UUID
		person     *string
		failOn     string
		wantErr    func(error) bool
		wantWrites []string
	}{
		{name: "a name is written without the space around it", user: userID, person: ptr("  Dana Reyes "), wantWrites: []string{"update user " + userID.String() + " name=Dana Reyes"}},
		{name: "no name takes the one held off", user: userID, wantWrites: []string{"update user " + userID.String() + " name=<none>"}},
		{name: "nothing but space takes the one held off", user: userID, person: ptr(" \t "), wantWrites: []string{"update user " + userID.String() + " name=<none>"}},
		{name: "the longest name is kept", user: userID, person: ptr(strings.Repeat("n", sessioninfo.MaxNameLength)), wantWrites: []string{"update user " + userID.String() + " name=" + strings.Repeat("n", sessioninfo.MaxNameLength)}},
		{name: "a name too long to keep is refused", user: userID, person: ptr(strings.Repeat("n", sessioninfo.MaxNameLength+1)), wantErr: httpio.HasBadRequest},
		{name: "a user that does not exist is not found", user: createdID, person: ptr("Dana Reyes"), wantErr: httpio.HasNotFound},
		{name: "a failed read fails the call", user: userID, person: ptr("Dana Reyes"), failOn: "Username", wantErr: func(err error) bool { return errors.Is(err, errStore) }},
		{name: "a failed write fails the call", user: userID, person: ptr("Dana Reyes"), failOn: "UpdateUser", wantErr: func(err error) bool { return errors.Is(err, errStore) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{usernames: map[impulse.UUID]string{userID: "dana"}, failOn: tt.failOn}
			m, _ := newManager(t, store, nil)

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.SetName(ctx, txn, tt.user, tt.person)
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("SetName() error = %v, want the refusal", err)
				}
			} else if err != nil {
				t.Fatalf("SetName() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
		})
	}
}

// TestManager_SetEmail pins an administrator's change of an address: nothing is written,
// and once the transaction has committed the session library is asked to mail the link
// that confirms the new address, bare, saying who asked; the account keeps an address
// always, so none is refused; and the caller is told how the mailing went.
func TestManager_SetEmail(t *testing.T) {
	t.Parallel()

	inAlpha := func(ctx context.Context) context.Context {
		return resource.WithCaller(ctx, &resource.Caller{Permissions: callerPermissions{user: erin}, Scope: accesstypes.DomainScope("alpha")})
	}
	refused := httpio.NewConflictMessage("This person signs in through their organization's directory, which keeps their email address. Change it there")
	tests := []struct {
		name       string
		user       impulse.UUID
		email      string
		caller     func(ctx context.Context) context.Context
		failOn     string
		mailErr    error
		wantErr    func(error) bool
		wantAsked  []string
		wantSent   bool
		wantReason string
	}{
		{
			name: "the link is asked for after the commit, to the bare address, by the application for a call in no tenant", user: userID, email: "  Dana Reyes <dana@example.com> ",
			wantAsked: []string{"address change of " + userID.String() + " to dana@example.com asked by the application"}, wantSent: true,
		},
		{
			name: "a tenant's administrator is named by the tenant", user: userID, email: "dana@example.com", caller: inAlpha,
			wantAsked: []string{"address change of " + userID.String() + " to dana@example.com asked by tenant alpha"}, wantSent: true,
		},
		{name: "no address is refused: an account always keeps one", user: userID, email: "  ", wantErr: httpio.HasBadRequest},
		{name: "a value that is no address is refused", user: userID, email: "dana", wantErr: httpio.HasBadRequest},
		{name: "a user that does not exist is not found", user: createdID, email: "dana@example.com", wantErr: httpio.HasNotFound},
		{name: "a failed read fails the call", user: userID, email: "dana@example.com", failOn: "Username", wantErr: func(err error) bool { return errors.Is(err, errStore) }},
		{
			name: "a refusal the session library words is passed on as the reason", user: userID, email: "dana@example.com", mailErr: refused,
			wantReason: "This person signs in through their organization's directory, which keeps their email address. Change it there",
		},
		{
			name: "any other failure is a message that could not be sent, in words that carry none of it", user: userID, email: "dana@example.com", mailErr: errStore,
			wantReason: "The message could not be sent. Try again in a few minutes",
		},
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
				err := m.SetEmail(ctx, txn, tt.user, tt.email, func(s bool, r string) { told, sent, reason = told+1, s, r })
				if len(mail.asked) != 0 || told != 0 {
					t.Errorf("before the commit: asked %v, told %d time(s), want nothing mailed until the transaction commits", mail.asked, told)
				}

				return err
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("SetEmail() error = %v, want the refusal", err)
				}
				if len(mail.asked) != 0 || told != 0 {
					t.Errorf("a refused change: asked %v, told %d time(s), want nothing", mail.asked, told)
				}

				return
			}
			if err != nil {
				t.Fatalf("SetEmail() error = %v", err)
			}
			if len(store.writes) != 0 {
				t.Errorf("buffered writes = %v, want none: the account keeps its address until the new one confirms", store.writes)
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

// TestManager_RequireUser pins the question a method asks before it gives a role to a
// person a request names: a user of the auth holds the id, or the answer is NotFound;
// a failed read is the failure, and no refusal of the person.
func TestManager_RequireUser(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		user    impulse.UUID
		failOn  string
		wantErr func(error) bool
	}{
		{name: "an id a user of the auth holds", user: userID},
		{name: "an id no user holds is not found", user: createdID, wantErr: httpio.HasNotFound},
		{name: "the nil id is not found", user: impulse.NilUUID, wantErr: httpio.HasNotFound},
		{name: "a failed read fails the call, and is no refusal of the person", user: userID, failOn: "Username", wantErr: func(err error) bool { return errors.Is(err, errStore) && !httpio.HasNotFound(err) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{usernames: map[impulse.UUID]string{userID: "dana"}, failOn: tt.failOn}
			m, _ := newManager(t, store, nil)

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.RequireUser(ctx, txn, tt.user)
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("RequireUser() error = %v, want the refusal", err)
				}
			} else if err != nil {
				t.Fatalf("RequireUser() error = %v", err)
			}
			if len(store.writes) != 0 {
				t.Errorf("buffered writes = %v, want none: the question writes nothing", store.writes)
			}
		})
	}
}
