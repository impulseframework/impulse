package signinflow

import (
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"

	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// TestSignIn_anAccountThatCannotHoldItsAddressIsRefused pins the refusal of a sign-in
// to an account that could not hold its address: the method vouches for the address,
// which spares the sign-in the wait for the mailed code because the address is written
// on the account as the session starts, and another account holds that address,
// confirmed, so it would be written on nobody. An account created ahead under the
// address as its sign-in name is reached so while another account holds the address.
// The sign-in is refused (identity_rejected, signin.ErrAddressHeldByAnother): no session
// is started, and a first sign-in links nothing.
func TestSignIn_anAccountThatCannotHoldItsAddressIsRefused(t *testing.T) {
	t.Parallel()

	const address = "pat@gmail.com"
	vouched := &sessioninfo.Identity{Method: sessioninfo.MethodGoogle, Connection: "google", Subject: "g-1", SignInName: address, Email: address, EmailVerified: true}
	unvouched := &sessioninfo.Identity{Method: sessioninfo.MethodGoogle, Connection: "google", Subject: "g-1", SignInName: address, Email: address}

	tests := []struct {
		name string
		// own is the address the account under the sign-in name holds, "" for none,
		// and ownConfirmed whether its person has confirmed it.
		own          string
		ownConfirmed bool
		// other is the address the other account holds, and otherConfirmed whether its
		// person has confirmed it.
		other          string
		otherConfirmed bool
		// linked says the identity was linked to the account before the sign-in.
		linked   bool
		identity *sessioninfo.Identity
		events   []sessioninfo.AuthEvent
		rules    Rules[*memTx]
		// wantRefused says the sign-in is refused; it starts a session otherwise.
		wantRefused bool
	}{
		{
			name:  "an account reached by its name, with no address, while another account holds the vouched address confirmed",
			other: address, otherConfirmed: true, identity: vouched, rules: Rules[*memTx]{ConfirmAddress: true}, wantRefused: true,
		},
		{
			name: "an account reached by its name that holds another address, unconfirmed",
			own:  "pat@elsewhere.example", other: address, otherConfirmed: true, identity: vouched, rules: Rules[*memTx]{ConfirmAddress: true}, wantRefused: true,
		},
		{
			name:  "an account the identity was linked to before is refused as well",
			other: address, otherConfirmed: true, linked: true, identity: vouched, rules: Rules[*memTx]{ConfirmAddress: true}, wantRefused: true,
		},
		{
			name:     "nobody else holds the address: the account is signed in to, and takes it",
			identity: vouched, rules: Rules[*memTx]{ConfirmAddress: true},
		},
		{
			name:  "the other account holds the address unconfirmed: the account is signed in to",
			other: address, identity: vouched, rules: Rules[*memTx]{ConfirmAddress: true},
		},
		{
			name: "an account that holds a confirmed address of its own is signed in to",
			own:  "pat@elsewhere.example", ownConfirmed: true, other: address, otherConfirmed: true, linked: true, identity: vouched, rules: Rules[*memTx]{ConfirmAddress: true},
		},
		{
			name: "a method that does not vouch for the address is the mailed code's to answer: the sign-in that carries the code's event goes on",
			own:  "pat@elsewhere.example", other: address, otherConfirmed: true, identity: unvouched,
			events: []sessioninfo.AuthEvent{{Method: sessioninfo.MethodEmailCode}}, rules: Rules[*memTx]{ConfirmAddress: true},
		},
		{
			name:  "rules that hold nobody for an address refuse nobody over one",
			other: address, otherConfirmed: true, linked: true, identity: vouched, rules: Rules[*memTx]{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newMemStore()
			userID, otherID := impulse.Must(impulse.NewUUID()), impulse.Must(impulse.NewUUID())
			store.users[userID] = &memUser{username: address, email: tt.own, confirmed: tt.ownConfirmed}
			store.users[otherID] = &memUser{username: "pat", email: tt.other, confirmed: tt.otherConfirmed}
			identity := *tt.identity
			key := linkKey(identity.Method, identity.Connection, identity.Subject)
			if tt.linked {
				store.links[key] = userID
			}
			req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, AuthEvents: tt.events, Identity: &identity}

			err := SignIn(t.Context(), &memDriver{store: store}, impulse.Must(impulse.NewUUID()), &dbtype.InsertSession{}, req, tt.rules)

			if !tt.wantRefused {
				if err != nil {
					t.Fatalf("SignIn() error = %v, want a session", err)
				}
				if linked, ok := store.links[key]; !ok || linked != userID || store.sessions != 1 {
					t.Errorf("the identity is linked to %v (linked %t) and the store holds %d session(s), want the account under the name, %s, and one session", linked, ok, store.sessions, userID)
				}

				return
			}
			var refusal *sessioninfo.LoginRefusal
			if !errors.As(err, &refusal) || refusal.Code() != sessioninfo.RefusedIdentityRejected || !errors.Is(err, signin.ErrAddressHeldByAnother) {
				t.Fatalf("SignIn() error = %v, want the sign-in refused (identity_rejected) with the cause %v", err, signin.ErrAddressHeldByAnother)
			}
			if store.sessions != 0 {
				t.Errorf("the store holds %d session(s), want none", store.sessions)
			}
			if linked, ok := store.links[key]; ok != tt.linked {
				t.Errorf("the identity is linked to %v (linked %t), want the links as they were before the sign-in (linked %t)", linked, ok, tt.linked)
			}
		})
	}
}
