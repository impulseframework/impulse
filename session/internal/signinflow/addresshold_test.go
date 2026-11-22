package signinflow

import (
	"context"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// TestSignIn_anUnconfirmedAddressIsConfirmedFirst pins the rule that every account a
// person signs in to holds a confirmed address, on the flow every sign-in runs: a person
// who comes through a directory that does not vouch for their address, and whose account
// holds no confirmed one, waits for the code the library mails, whatever the sign-in
// rule asks, and no session starts. Neither the directory's attestation to a second
// factor nor a remembered browser stands in for it, since neither says anything of the
// address. The sign-in that completes the wait with the account's own code carries the
// address the code confirms (Rules.CodeAddress) and goes on, and so does every sign-in
// of an account that holds a confirmed address; one that carries the code's event and no
// such address is held for the code again. A directory that vouches for the address
// is not held, nor is a password sign-in, nor any sign-in where the Auth does not ask
// for it (the registration is the deployment's own provider).
func TestSignIn_anUnconfirmedAddressIsConfirmedFirst(t *testing.T) {
	t.Parallel()

	microsoft := &sessioninfo.Identity{Method: sessioninfo.MethodAzure, Connection: "tenant-1", Subject: "a-1", Email: "pat@lakeside.edu"}
	google := &sessioninfo.Identity{Method: sessioninfo.MethodGoogle, Connection: "lakeside.edu", Subject: "g-1", Email: "pat@lakeside.edu", EmailVerified: true}
	asksNobody := func(context.Context, *memTx, *sessioninfo.NewSessionRequest) (*signin.Decision, error) {
		return signin.Allow(), nil
	}
	asksEveryone := func(context.Context, *memTx, *sessioninfo.NewSessionRequest) (*signin.Decision, error) {
		return signin.RequireMFA(), nil
	}
	tests := []struct {
		name string
		// identity is who signs in, linked to the account already; confirmed whether the
		// account holds a confirmed address; off that the Auth does not ask for one.
		identity  *sessioninfo.Identity
		confirmed bool
		off       bool
		policy    signin.Policy[*memTx]
		trusted   bool
		reason    sessioninfo.NewSessionReason
		events    []sessioninfo.AuthEvent
		claim     *dbtype.CodeAddress
		// wantHeld says the sign-in waits for the code, and no session starts.
		wantHeld bool
	}{
		{name: "a directory that does not vouch for the address: the person waits for the code though the rule asks nobody", identity: microsoft, policy: asksNobody, wantHeld: true},
		{name: "with no sign-in rule at all the person waits for the code", identity: microsoft, wantHeld: true},
		{
			name: "the directory's attestation to a second factor does not confirm an address", identity: microsoft, policy: asksEveryone, wantHeld: true,
			events: []sessioninfo.AuthEvent{{Method: sessioninfo.MethodAzure}, {Method: sessioninfo.MethodAttestedByEvidence}},
		},
		{name: "a remembered browser does not confirm an address", identity: microsoft, policy: asksEveryone, trusted: true, wantHeld: true},
		{
			name: "the code the person entered completes the sign-in", identity: microsoft, policy: asksNobody, reason: sessioninfo.ReasonStepUp,
			events: []sessioninfo.AuthEvent{{Method: sessioninfo.MethodAzure}, {Method: sessioninfo.MethodEmailCode}},
			claim:  &dbtype.CodeAddress{Email: "pat@lakeside.edu"},
		},
		{
			name: "the code's event without the address it confirms is held for the code again", identity: microsoft, policy: asksNobody, reason: sessioninfo.ReasonStepUp,
			events: []sessioninfo.AuthEvent{{Method: sessioninfo.MethodAzure}, {Method: sessioninfo.MethodEmailCode}}, wantHeld: true,
		},
		{name: "an account that holds a confirmed address is not held", identity: microsoft, confirmed: true, policy: asksNobody},
		{name: "a directory that vouches for the address is not held: the address is written as the session starts", identity: google, policy: asksNobody},
		{name: "an Auth that does not ask for it holds nobody", identity: microsoft, off: true, policy: asksNobody},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newMemStore()
			userID := impulse.Must(impulse.NewUUID())
			store.users[userID] = &memUser{username: "pat", confirmed: tt.confirmed}
			store.links[linkKey(tt.identity.Method, tt.identity.Connection, tt.identity.Subject)] = userID
			d := &memDriver{store: store}
			rules := Rules[*memTx]{
				Policy: tt.policy, ConfirmAddress: !tt.off, CodeAddress: tt.claim,
				TrustedDevice: func(context.Context, impulse.UUID) (bool, error) { return tt.trusted, nil },
			}
			reason := tt.reason
			if reason == "" {
				reason = sessioninfo.ReasonLogin
			}
			identity := *tt.identity
			req := &sessioninfo.NewSessionRequest{Reason: reason, AuthEvents: tt.events, Identity: &identity}

			err := SignIn(t.Context(), d, impulse.Must(impulse.NewUUID()), &dbtype.InsertSession{}, req, rules)

			if !tt.wantHeld {
				if err != nil {
					t.Fatalf("SignIn() error = %v, want a session", err)
				}
				if store.sessions != 1 {
					t.Errorf("the store holds %d session(s), want one", store.sessions)
				}

				return
			}
			var wait *signin.PendingSignInError
			if !errors.As(err, &wait) || wait.Reason != sessioninfo.PendingMFA {
				t.Fatalf("SignIn() error = %v, want the sign-in held for the code that confirms the address", err)
			}
			if diff := cmp.Diff([]sessioninfo.AuthMethod{sessioninfo.MethodEmailCode}, wait.Factors); diff != "" {
				t.Errorf("the factors the wait accepts (-want +got):\n%s", diff)
			}
			if !wait.UserID.Valid || wait.UserID.UUID != userID {
				t.Errorf("the wait names the user %v, want %s", wait.UserID, userID)
			}
			if store.sessions != 0 {
				t.Errorf("the store holds %d session(s), want none while the address is unconfirmed", store.sessions)
			}
		})
	}

	t.Run("a password sign-in is not held: the account was given its address where it was made", func(t *testing.T) {
		t.Parallel()

		store := newMemStore()
		userID := impulse.Must(impulse.NewUUID())
		store.users[userID] = &memUser{username: "pat"}
		req := &sessioninfo.NewSessionRequest{
			Reason: sessioninfo.ReasonLogin, UserID: userID, Username: "pat", Identity: &sessioninfo.Identity{Method: sessioninfo.MethodPassword, Subject: userID.String()},
		}
		if err := SignIn(t.Context(), &memDriver{store: store}, impulse.Must(impulse.NewUUID()), &dbtype.InsertSession{}, req, Rules[*memTx]{ConfirmAddress: true}); err != nil {
			t.Fatalf("SignIn() error = %v, want a session", err)
		}
	})
}
