package signinflow

import (
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// TestSignIn_aLinkOnTheNameAloneWaitsForTheAccountsCode pins the rule on the first link
// of an account that stood before the sign-in, where nothing but the sign-in name ties
// the two and the method does not vouch for the name: a Google account made with an
// outside address, another identity provider's and Microsoft's alike. The sign-in waits
// for the code mailed to the address the account holds, whether or not the account's
// person has confirmed it, and nothing is linked while it waits; the sign-in that
// carries the code's event makes the link and goes on. An account that holds no address
// is refused, since a code mailed to an address typed at the sign-in would show only
// that the person reads their own mail. A name Google vouches for links at once, and so
// does a registration of the deployment's own seeded accounts and an Auth that mails no
// code.
func TestSignIn_aLinkOnTheNameAloneWaitsForTheAccountsCode(t *testing.T) {
	t.Parallel()

	const name = "alice@corp.example"
	outside := &sessioninfo.Identity{Method: sessioninfo.MethodGoogle, Connection: "google", Subject: "g-1", SignInName: name, Email: name}
	workspace := &sessioninfo.Identity{Method: sessioninfo.MethodGoogle, Connection: "corp.example", Subject: "g-2", SignInName: name, Email: name, EmailVerified: true}
	provider := &sessioninfo.Identity{Method: sessioninfo.MethodOIDC, Connection: "https://id.example", Subject: "p-1", SignInName: name, Email: name}
	microsoft := &sessioninfo.Identity{Method: sessioninfo.MethodAzure, Connection: "tenant-1", Subject: "a-1", SignInName: name, Email: name}
	code := []sessioninfo.AuthEvent{{Method: sessioninfo.MethodGoogle}, {Method: sessioninfo.MethodEmailCode}}
	tests := []struct {
		name string
		// identity is who signs in, linked to nobody; address the address the account
		// under the name holds, "" for none, and confirmed whether its person confirmed it.
		identity  *sessioninfo.Identity
		address   string
		confirmed bool
		rules     Rules[*memTx]
		reason    sessioninfo.NewSessionReason
		events    []sessioninfo.AuthEvent
		// wantHeld says the sign-in waits for the code, wantRefused that it is refused
		// with that cause; neither is a link and a session.
		wantHeld    bool
		wantRefused error
	}{
		{
			name:     "an account that holds a confirmed address: the sign-in waits for the code mailed to it, and no session starts on the name",
			identity: outside, address: name, confirmed: true, rules: Rules[*memTx]{ConfirmAddress: true}, wantHeld: true,
		},
		{
			name:     "an account whose address nobody confirmed: the sign-in waits, and no link stays behind for a sign-in that never finishes",
			identity: outside, address: name, rules: Rules[*memTx]{ConfirmAddress: true}, wantHeld: true,
		},
		{
			name:     "an account that holds no address: the link is refused, and nobody is asked for an address to mail a code to",
			identity: outside, rules: Rules[*memTx]{ConfirmAddress: true}, wantRefused: signin.ErrLinkNeedsAddress,
		},
		{
			name:     "another identity provider's name waits as Google's outside address does",
			identity: provider, address: name, confirmed: true, rules: Rules[*memTx]{ConfirmAddress: true}, wantHeld: true,
		},
		{
			name:     "a Microsoft directory's name waits too",
			identity: microsoft, address: name, confirmed: true, rules: Rules[*memTx]{ConfirmAddress: true}, wantHeld: true,
		},
		{
			name:     "the code the person entered makes the link, and the sign-in goes on",
			identity: outside, address: name, confirmed: true, rules: Rules[*memTx]{ConfirmAddress: true}, reason: sessioninfo.ReasonStepUp, events: code,
		},
		{
			name:     "a name Google vouches for links at once",
			identity: workspace, address: name, confirmed: true, rules: Rules[*memTx]{ConfirmAddress: true},
		},
		{
			name:     "a registration of the deployment's own seeded accounts links on the name",
			identity: provider, rules: Rules[*memTx]{SeededUsers: true},
		},
		{
			name:     "an Auth that mails no code links on the name, as before",
			identity: outside, address: name, confirmed: true, rules: Rules[*memTx]{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newMemStore()
			userID := impulse.Must(impulse.NewUUID())
			store.users[userID] = &memUser{username: name, email: tt.address, confirmed: tt.confirmed}
			reason := tt.reason
			if reason == "" {
				reason = sessioninfo.ReasonLogin
			}
			identity := *tt.identity
			req := &sessioninfo.NewSessionRequest{Reason: reason, AuthEvents: tt.events, Identity: &identity}

			err := SignIn(t.Context(), &memDriver{store: store}, impulse.Must(impulse.NewUUID()), &dbtype.InsertSession{}, req, tt.rules)

			switch {
			case tt.wantRefused != nil:
				var refusal *sessioninfo.LoginRefusal
				if !errors.As(err, &refusal) || refusal.Code() != sessioninfo.RefusedIdentityRejected || !errors.Is(err, tt.wantRefused) {
					t.Fatalf("SignIn() error = %v, want the link refused (identity_rejected) with the cause %v", err, tt.wantRefused)
				}
			case tt.wantHeld:
				var wait *signin.PendingSignInError
				if !errors.As(err, &wait) || wait.Reason != sessioninfo.PendingMFA {
					t.Fatalf("SignIn() error = %v, want the sign-in held for the code mailed to the account's address", err)
				}
				if diff := cmp.Diff([]sessioninfo.AuthMethod{sessioninfo.MethodEmailCode}, wait.Factors); diff != "" {
					t.Errorf("the factors the wait accepts (-want +got):\n%s", diff)
				}
				if !wait.UserID.Valid || wait.UserID.UUID != userID || wait.Username != name {
					t.Errorf("the wait names the user %v (%q), want %s (%q): the code goes to that account's address", wait.UserID, wait.Username, userID, name)
				}
			default:
				if err != nil {
					t.Fatalf("SignIn() error = %v, want the identity linked and a session", err)
				}
				if linked, ok := store.links[linkKey(identity.Method, identity.Connection, identity.Subject)]; !ok || linked != userID {
					t.Errorf("the identity is linked to %v (linked %t), want the account under the name, %s", linked, ok, userID)
				}
				if store.sessions != 1 {
					t.Errorf("the store holds %d session(s), want one", store.sessions)
				}

				return
			}
			if len(store.links) != 0 {
				t.Errorf("the store holds %d identity link(s), want none: the link is written once the code is entered", len(store.links))
			}
			if store.sessions != 0 {
				t.Errorf("the store holds %d session(s), want none", store.sessions)
			}
		})
	}
}
