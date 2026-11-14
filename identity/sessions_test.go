package identity

import (
	"context"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
)

// A third session: one the user established as someone else.
var sessionC = mustUUID("cccccccc-cccc-4ccc-8ccc-cccccccccccc")

// TestManager_EndSession pins the end of one session: the session where it stands, its
// impersonation record where that stands, with the administrator's reason, and nothing
// for a session that has ended already; a session that does not exist is refused.
func TestManager_EndSession(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		store      *fakeStore
		wantErr    func(error) bool
		wantWrites []string
	}{
		{
			name:       "a person's own session",
			store:      &fakeStore{},
			wantWrites: []string{"end session " + sessionA.String()},
		},
		{
			name:  "a session established as someone else ends with its record",
			store: &fakeStore{records: map[impulse.UUID]bool{sessionA: true}},
			wantWrites: []string{
				"end session " + sessionA.String(),
				"end impersonation " + sessionA.String() + " Revoked",
			},
		},
		{
			name:  "a session that has ended is left as it is",
			store: &fakeStore{ended: map[impulse.UUID]bool{sessionA: true}},
		},
		{
			name:       "a record still standing beside an ended session is ended",
			store:      &fakeStore{ended: map[impulse.UUID]bool{sessionA: true}, records: map[impulse.UUID]bool{sessionA: true}},
			wantWrites: []string{"end impersonation " + sessionA.String() + " Revoked"},
		},
		{
			name:    "a session that does not exist is refused",
			store:   &fakeStore{missing: map[impulse.UUID]bool{sessionA: true}},
			wantErr: httpio.HasNotFound,
		},
		{
			name:    "a failed read buffers nothing",
			store:   &fakeStore{failOn: "SessionExpired"},
			wantErr: func(err error) bool { return errors.Is(err, errStore) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m, _ := newManager(t, tt.store, nil)
			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.EndSession(ctx, txn, sessionA)
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("EndSession() error = %v, want the refusal", err)
				}
			} else if err != nil {
				t.Fatalf("EndSession() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantWrites, tt.store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
		})
	}
}

// TestManager_EndImpersonation pins the end of an impersonated session: it acts only
// where a record stands, refuses a session that was never impersonated, and leaves a
// record that has ended.
func TestManager_EndImpersonation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		store      *fakeStore
		wantErr    func(error) bool
		wantWrites []string
	}{
		{
			name:  "the session and its record end",
			store: &fakeStore{records: map[impulse.UUID]bool{sessionA: true}},
			wantWrites: []string{
				"end session " + sessionA.String(),
				"end impersonation " + sessionA.String() + " Revoked",
			},
		},
		{
			name:       "a record whose session row is gone ends alone",
			store:      &fakeStore{records: map[impulse.UUID]bool{sessionA: true}, missing: map[impulse.UUID]bool{sessionA: true}},
			wantWrites: []string{"end impersonation " + sessionA.String() + " Revoked"},
		},
		{
			name:  "a record that has ended is left as it is",
			store: &fakeStore{records: map[impulse.UUID]bool{sessionA: false}},
		},
		{
			name:    "a session that was never impersonated is refused, and stays",
			store:   &fakeStore{},
			wantErr: httpio.HasNotFound,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m, _ := newManager(t, tt.store, nil)
			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.EndImpersonation(ctx, txn, sessionA)
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("EndImpersonation() error = %v, want the refusal", err)
				}
			} else if err != nil {
				t.Fatalf("EndImpersonation() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantWrites, tt.store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
		})
	}
}

// TestManager_EndUserSessions pins which sessions are a user's to end: the user's own,
// and the ones the user established as someone else, found by the user's name in their
// records; each ended once, its standing record with it.
func TestManager_EndUserSessions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		store      *fakeStore
		wantErr    bool
		wantWrites []string
	}{
		{
			name:  "the user's own sessions",
			store: &fakeStore{sessions: map[impulse.UUID][]impulse.UUID{userID: {sessionA, sessionB}}, usernames: map[impulse.UUID]string{userID: "dana"}},
			wantWrites: []string{
				"end session " + sessionA.String(),
				"end session " + sessionB.String(),
			},
		},
		{
			name: "someone viewing as the user holds a session that is the user's, and it ends with its record",
			store: &fakeStore{
				sessions:  map[impulse.UUID][]impulse.UUID{userID: {sessionA}},
				usernames: map[impulse.UUID]string{userID: "dana"},
				records:   map[impulse.UUID]bool{sessionA: true},
			},
			wantWrites: []string{
				"end session " + sessionA.String(),
				"end impersonation " + sessionA.String() + " Revoked",
			},
		},
		{
			name: "a session the user established as someone else is found by the user's name",
			store: &fakeStore{
				sessions:  map[impulse.UUID][]impulse.UUID{userID: {sessionA}},
				usernames: map[impulse.UUID]string{userID: "dana"},
				records:   map[impulse.UUID]bool{sessionC: true},
				heldBy:    map[string][]impulse.UUID{"dana": {sessionC}},
			},
			wantWrites: []string{
				"end session " + sessionA.String(),
				"end session " + sessionC.String(),
				"end impersonation " + sessionC.String() + " Revoked",
			},
		},
		{
			name: "a session that is the user's own and held by the user ends once",
			store: &fakeStore{
				sessions:  map[impulse.UUID][]impulse.UUID{userID: {sessionA}},
				usernames: map[impulse.UUID]string{userID: "dana"},
				records:   map[impulse.UUID]bool{sessionA: true},
				heldBy:    map[string][]impulse.UUID{"dana": {sessionA}},
			},
			wantWrites: []string{
				"end session " + sessionA.String(),
				"end impersonation " + sessionA.String() + " Revoked",
			},
		},
		{
			name: "a held session that has ended, or whose row is gone, loses its record alone",
			store: &fakeStore{
				usernames: map[impulse.UUID]string{userID: "dana"},
				records:   map[impulse.UUID]bool{sessionB: true, sessionC: true},
				heldBy:    map[string][]impulse.UUID{"dana": {sessionB, sessionC}},
				ended:     map[impulse.UUID]bool{sessionB: true},
				missing:   map[impulse.UUID]bool{sessionC: true},
			},
			wantWrites: []string{
				"end impersonation " + sessionB.String() + " Revoked",
				"end impersonation " + sessionC.String() + " Revoked",
			},
		},
		{
			name:  "a user that does not exist holds none",
			store: &fakeStore{heldBy: map[string][]impulse.UUID{"": {sessionC}}},
		},
		{
			name: "a failed read buffers nothing",
			store: &fakeStore{
				sessions:  map[impulse.UUID][]impulse.UUID{userID: {sessionA}},
				usernames: map[impulse.UUID]string{userID: "dana"},
				failOn:    "ImpersonationsHeldBy",
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m, _ := newManager(t, tt.store, nil)
			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.EndUserSessions(ctx, txn, userID)
			})
			if (err != nil) != tt.wantErr {
				t.Fatalf("EndUserSessions() error = %v, wantErr %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(tt.wantWrites, tt.store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
		})
	}
}
