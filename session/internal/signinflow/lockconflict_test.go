package signinflow

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// TestSignIn_aTransactionTheDatabaseEndsForADeadlockRunsOnceMore pins the answer to a
// transaction the database ends to break a deadlock or a serialization failure
// (dbtype.LockConflict): the transaction that inserts the session, and an invitation's
// acceptance outside a resolution, run once more from the start, and are answered by
// what the second run finds, another sign-in's write committed between the two runs
// among it; a second such end is the store's error. Two sign-ins of one user that
// write the code's address at the same moment end so on PostgreSQL, each having shared
// the user row's lock before asking to write the row. The policy and the acceptor are
// called once a run, and what the acceptor asked to run after the commit runs once,
// for the run that committed.
func TestSignIn_aTransactionTheDatabaseEndsForADeadlockRunsOnceMore(t *testing.T) {
	t.Parallel()

	const (
		held    = "pat@lakeside.edu"
		another = "pat@north.example"
		invited = "pat@invited.example"
	)
	proof := dbtype.ProofOfStored("the password hash the password was compared against")
	tests := []struct {
		name string
		// invited says the sign-in carries an invitation, accepted on the invitation's code,
		// and the account holds no address; otherwise the account holds held, unconfirmed,
		// and the sign-in carries the claim of it its own code makes.
		invited bool
		// outcomes are what the store does with the commits, in order; meanwhile is what
		// another transaction commits while the first run ends, nil for nothing.
		outcomes  []commitOutcome
		meanwhile func(store *memStore, userID impulse.UUID)
		// wantErr checks the error; nil wants a session. wantAddress is the address the
		// account holds after the sign-in, and wantConfirmed whether it is confirmed;
		// wantWritten is the claim's Written. wantPolicy and wantAccept are the calls of the
		// policy and of the acceptor; wantAccepted says the invitation was accepted once.
		wantErr       func(t *testing.T, err error)
		wantAddress   string
		wantConfirmed bool
		wantWritten   bool
		wantPolicy    int
		wantAccept    int
		wantAccepted  bool
	}{
		{
			name:        "the second run finds the account as it was: the address is written, and the session inserted once",
			outcomes:    []commitOutcome{lockConflicts},
			wantAddress: held, wantConfirmed: true, wantWritten: true, wantPolicy: 2,
		},
		{
			name:     "the second run finds the address confirmed by the other sign-in: nothing is written, and the session inserted",
			outcomes: []commitOutcome{lockConflicts},
			meanwhile: func(store *memStore, userID impulse.UUID) {
				store.users[userID].confirmed = true
			},
			wantAddress: held, wantConfirmed: true, wantPolicy: 2,
		},
		{
			name:     "the second run finds the account holding another confirmed address: refused code_expired, no session",
			outcomes: []commitOutcome{lockConflicts},
			meanwhile: func(store *memStore, userID impulse.UUID) {
				store.users[userID].email, store.users[userID].confirmed = another, true
			},
			wantErr: wantRefusal(sessioninfo.RefusedCodeExpired), wantAddress: another, wantConfirmed: true, wantPolicy: 2,
		},
		{
			name:     "the second run finds the user disabled: refused user_disabled, as on a first run",
			outcomes: []commitOutcome{lockConflicts},
			meanwhile: func(store *memStore, userID impulse.UUID) {
				store.users[userID].disabled = true
			},
			wantErr: wantRefusal(sessioninfo.RefusedUserDisabled), wantAddress: held, wantPolicy: 1,
		},
		{
			name:     "the second run finds the password changed: the sign-in ends, as on a first run",
			outcomes: []commitOutcome{lockConflicts},
			meanwhile: func(store *memStore, userID impulse.UUID) {
				store.users[userID].proof = dbtype.ProofOfStored("the hash of the password set meanwhile")
			},
			wantErr: wantCause(dbtype.ErrPasswordChanged), wantAddress: held, wantPolicy: 1,
		},
		{
			name:     "the second run finds the policy denying: refused, as on a first run",
			outcomes: []commitOutcome{lockConflicts},
			meanwhile: func(store *memStore, _ impulse.UUID) {
				store.granted["pat"] = []string{"Suspended"}
			},
			wantErr: wantDenied, wantAddress: held, wantPolicy: 2,
		},
		{
			name:     "the second run ends in a lock conflict too: the store's error, no session",
			outcomes: []commitOutcome{lockConflicts, lockConflicts},
			wantErr:  wantCause(errLockConflict), wantAddress: held, wantPolicy: 2,
		},
		{
			name:    "an acceptance whose second run commits is accepted once: the invitation consumed, its roles given once",
			invited: true, outcomes: []commitOutcome{lockConflicts},
			wantAddress: invited, wantConfirmed: true, wantPolicy: 1, wantAccept: 2, wantAccepted: true,
		},
		{
			name:    "an acceptance whose second run ends in a lock conflict too: the store's error, the invitation standing and nothing given",
			invited: true, outcomes: []commitOutcome{lockConflicts, lockConflicts},
			wantErr: wantCause(errLockConflict), wantAccept: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newMemStore()
			store.next = tt.outcomes
			hooks := &roleHooks{store: store}
			userID := impulse.Must(impulse.NewUUID())
			address := held
			if tt.invited {
				address = ""
			}
			store.users[userID] = &memUser{username: "pat", email: address, proof: proof}
			if tt.meanwhile != nil {
				store.meanwhile = func() { tt.meanwhile(store, userID) }
			}
			// The policy denies a user granted Suspended, as one that reads the user's
			// roles in the transaction it is handed.
			policyCalls := 0
			policy := func(_ context.Context, _ *memTx, req *sessioninfo.NewSessionRequest) (*signin.Decision, error) {
				policyCalls++
				if slices.Contains(store.granted[req.Username], "Suspended") {
					return signin.Deny(""), nil
				}

				return signin.Allow(), nil
			}
			rules := Rules[*memTx]{ConfirmAddress: true, Accept: hooks.accept, Policy: policy, Proven: proof}
			if !tt.invited {
				rules.CodeAddress = &dbtype.CodeAddress{Held: addressOf(held), Email: held}
			}
			req := &sessioninfo.NewSessionRequest{
				Reason: sessioninfo.ReasonStepUp, UserID: userID, Username: "pat",
				Identity:   &sessioninfo.Identity{Method: sessioninfo.MethodPassword, Subject: userID.String()},
				AuthEvents: []sessioninfo.AuthEvent{{Method: sessioninfo.MethodPassword}, {Method: sessioninfo.MethodEmailCode}},
			}
			var inv *sessioninfo.Invitation
			if tt.invited {
				inv = store.invite(invited, "lakeside", "Coach")
				inv.AddressProven = true
				req.Invitation = inv
			}

			err := signInWith(t, &memDriver{store: store}, req, rules)

			wantSessions := 0
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("SignIn() error = %v, want a session", err)
				}
				wantSessions = 1
			} else {
				tt.wantErr(t, err)
			}
			if store.sessions != wantSessions {
				t.Errorf("the store holds %d session(s), want %d", store.sessions, wantSessions)
			}
			user := store.users[userID]
			if user.email != tt.wantAddress || user.confirmed != tt.wantConfirmed {
				t.Errorf("the account holds %q (confirmed %t), want %q (confirmed %t)", user.email, user.confirmed, tt.wantAddress, tt.wantConfirmed)
			}
			if claim := rules.CodeAddress; claim != nil && claim.Written != tt.wantWritten {
				t.Errorf("CodeAddress.Written = %t, want %t", claim.Written, tt.wantWritten)
			}
			if policyCalls != tt.wantPolicy {
				t.Errorf("the policy was called %d time(s), want %d: once a run that reaches it", policyCalls, tt.wantPolicy)
			}
			accepts := 0
			for _, step := range store.steps {
				if strings.HasPrefix(step, "accept in run") {
					accepts++
				}
			}
			if accepts != tt.wantAccept {
				t.Errorf("the acceptor was called %d time(s), want %d: once a run", accepts, tt.wantAccept)
			}
			if inv == nil {
				return
			}
			wantGranted, wantCommitted := map[string][]string{}, 0
			if tt.wantAccepted {
				wantGranted, wantCommitted = map[string][]string{"pat": {"Coach"}}, 1
			}
			if inv.Accepted != tt.wantAccepted || store.invitations[inv.ID] == tt.wantAccepted {
				t.Errorf("Invitation.Accepted = %t, still live = %t, want accepted %t", inv.Accepted, store.invitations[inv.ID], tt.wantAccepted)
			}
			if diff := cmp.Diff(wantGranted, store.granted); diff != "" {
				t.Errorf("the roles given (-want +got):\n%s", diff)
			}
			if hooks.committed != wantCommitted {
				t.Errorf("what the acceptor asked to run after the commit ran %d time(s), want %d", hooks.committed, wantCommitted)
			}
		})
	}
}
