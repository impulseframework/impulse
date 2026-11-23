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

// TestSignIn_aLinkOnTheNameAloneWaitsForTheAccountsCode pins the rule on the first link
// of an account that stood before the sign-in, where nothing but the sign-in name ties
// the two and the method does not vouch for the name: a Google account made with an
// outside address, another identity provider's and Microsoft's alike. The sign-in waits
// for the code mailed to the address the account holds, whether or not the account's
// person has confirmed it, and nothing is linked while it waits; the sign-in the
// account's own code completes makes the link and goes on. An account that holds no address
// is refused, since a code mailed to an address typed at the sign-in would show only
// that the person reads their own mail. A name Google vouches for links at once where it
// is the account's own: its username as the library compares two addresses, or the
// address the account holds confirmed. A vouched name that a store matching more widely
// would find under another spelling (the Kelvin sign for a k) is not the account's own,
// and waits as a name the method does not vouch for: the shipped stores find no account
// under it, and this test's driver matches more widely so that the flow's own check is
// what the case holds. A registration of the deployment's own seeded accounts links on
// the name. The wait is the link's own: it does not turn on whether the rules hold a
// person for an address (Rules.ConfirmAddress).
func TestSignIn_aLinkOnTheNameAloneWaitsForTheAccountsCode(t *testing.T) {
	t.Parallel()

	const name = "alice@corp.example"
	outside := &sessioninfo.Identity{Method: sessioninfo.MethodGoogle, Connection: "google", Subject: "g-1", SignInName: name, Email: name}
	workspace := &sessioninfo.Identity{Method: sessioninfo.MethodGoogle, Connection: "corp.example", Subject: "g-2", SignInName: name, Email: name, EmailVerified: true}
	provider := &sessioninfo.Identity{Method: sessioninfo.MethodOIDC, Connection: "https://id.example", Subject: "p-1", SignInName: name, Email: name}
	microsoft := &sessioninfo.Identity{Method: sessioninfo.MethodAzure, Connection: "tenant-1", Subject: "a-1", SignInName: name, Email: name}
	const kim, kelvinKim = "kim@lakeside.edu", "\u212Aim@lakeside.edu"
	upperKim := &sessioninfo.Identity{Method: sessioninfo.MethodGoogle, Connection: "lakeside.edu", Subject: "g-3", SignInName: "KIM@lakeside.edu", Email: "KIM@lakeside.edu", EmailVerified: true}
	lookalike := &sessioninfo.Identity{Method: sessioninfo.MethodGoogle, Connection: "lakeside.edu", Subject: "g-4", SignInName: kelvinKim, Email: kelvinKim, EmailVerified: true}
	gmail := &sessioninfo.Identity{Method: sessioninfo.MethodGoogle, Connection: "google", Subject: "g-5", SignInName: "pat@gmail.com", Email: "pat@gmail.com", EmailVerified: true}
	code := []sessioninfo.AuthEvent{{Method: sessioninfo.MethodGoogle}, {Method: sessioninfo.MethodEmailCode}}
	tests := []struct {
		name string
		// identity is who signs in, linked to nobody; username the account's username,
		// name when empty; address the address the account holds, "" for none, and
		// confirmed whether its person confirmed it.
		identity  *sessioninfo.Identity
		username  string
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
			identity: outside, address: name, confirmed: true, reason: sessioninfo.ReasonStepUp, events: code,
			rules: Rules[*memTx]{ConfirmAddress: true, CodeAddress: &dbtype.CodeAddress{Held: addressOf(name), Email: name}},
		},
		{
			name:     "the code's event with no address the code confirms and no invitation it proved links nothing: the sign-in waits for the code mailed to the account's address",
			identity: outside, address: name, confirmed: true, reason: sessioninfo.ReasonStepUp, events: code, rules: Rules[*memTx]{ConfirmAddress: true}, wantHeld: true,
		},
		{
			name:     "the code's event with neither, to an account that holds no address, is refused the link",
			identity: outside, reason: sessioninfo.ReasonStepUp, events: code, rules: Rules[*memTx]{ConfirmAddress: true}, wantRefused: signin.ErrLinkNeedsAddress,
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
			name:     "rules that hold nobody for an address wait for the link's code all the same",
			identity: outside, address: name, confirmed: true, rules: Rules[*memTx]{}, wantHeld: true,
		},
		{
			name:     "a vouched name that is the account's username in other ASCII capitals links at once",
			identity: upperKim, username: kim, rules: Rules[*memTx]{ConfirmAddress: true},
		},
		{
			name:     "a vouched name that reads as the account's username only under Unicode's folding waits for the code mailed to the account's address",
			identity: lookalike, username: kim, address: kim, rules: Rules[*memTx]{ConfirmAddress: true}, wantHeld: true,
		},
		{
			name:     "a vouched look-alike name waits as well where the account holds its own address confirmed: that address is not the one vouched for",
			identity: lookalike, username: kim, address: kim, confirmed: true, rules: Rules[*memTx]{ConfirmAddress: true}, wantHeld: true,
		},
		{
			name:     "a vouched look-alike name to an account that holds no address is refused",
			identity: lookalike, username: kim, rules: Rules[*memTx]{ConfirmAddress: true}, wantRefused: signin.ErrLinkNeedsAddress,
		},
		{
			name:     "a vouched look-alike name is linked by the code mailed to the account's address",
			identity: lookalike, username: kim, address: kim, reason: sessioninfo.ReasonStepUp, events: code,
			rules: Rules[*memTx]{ConfirmAddress: true, CodeAddress: &dbtype.CodeAddress{Held: addressOf(kim), Email: kim}},
		},
		{
			name:     "a vouched address the account holds confirmed links at once under a username that is no address",
			identity: gmail, username: "pat", address: "pat@gmail.com", confirmed: true, rules: Rules[*memTx]{ConfirmAddress: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newMemStore()
			userID := impulse.Must(impulse.NewUUID())
			username := tt.username
			if username == "" {
				username = name
			}
			store.users[userID] = &memUser{username: username, email: tt.address, confirmed: tt.confirmed}
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
				if !wait.UserID.Valid || wait.UserID.UUID != userID || wait.Username != username {
					t.Errorf("the wait names the user %v (%q), want %s (%q): the code goes to that account's address", wait.UserID, wait.Username, userID, username)
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

// TestSignIn_aWaitLinksOnlyTheUserItWaitedOn proves the sign-in that completes a wait
// links its identity to the user the wait was for, or to nobody. The step the person
// answered was that user's: the code went to their address. The name the identity
// asserts may have moved to another account during the wait, and the rules decide the
// identity again at the completion, so their answer is held to the wait's user before
// anything is written: a link to another user, and an account made new, are refused
// with identity_rejected, and no link, account or session is left behind.
func TestSignIn_aWaitLinksOnlyTheUserItWaitedOn(t *testing.T) {
	t.Parallel()

	const name = "alice@corp.example"
	outside := &sessioninfo.Identity{Method: sessioninfo.MethodGoogle, Connection: "google", Subject: "g-1", SignInName: name, Email: name}
	code := []sessioninfo.AuthEvent{{Method: sessioninfo.MethodGoogle}, {Method: sessioninfo.MethodEmailCode}}
	tests := []struct {
		name string
		// moved says the name went to another account during the wait. rule is the
		// registration's own account rule, given the wait's user and the other account;
		// nil is the standard rule.
		moved       bool
		rule        func(waited, other impulse.UUID) signin.AccountRule[*memTx]
		wantRefused bool
	}{
		{name: "the account the wait was for is still the one under the name: it is linked"},
		{name: "the name moved to another account during the wait: nothing is linked", moved: true, wantRefused: true},
		{
			name: "a rule of the registration's own that links the wait's user is applied",
			rule: func(waited, _ impulse.UUID) signin.AccountRule[*memTx] {
				return func(context.Context, *memTx, *sessioninfo.NewSessionRequest) (*signin.Resolution, error) {
					return signin.Link(waited, true), nil
				}
			},
		},
		{
			name: "a rule of the registration's own that links another user is refused",
			rule: func(_, other impulse.UUID) signin.AccountRule[*memTx] {
				return func(context.Context, *memTx, *sessioninfo.NewSessionRequest) (*signin.Resolution, error) {
					return signin.Link(other, true), nil
				}
			},
			wantRefused: true,
		},
		{
			name: "a rule that would make a new account for the identity is refused: no account is made",
			rule: func(_, _ impulse.UUID) signin.AccountRule[*memTx] {
				return func(context.Context, *memTx, *sessioninfo.NewSessionRequest) (*signin.Resolution, error) {
					return signin.Provision(&signin.NewUser{Username: "alice.new@corp.example"}, nil), nil
				}
			},
			wantRefused: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newMemStore()
			waited, other := impulse.Must(impulse.NewUUID()), impulse.Must(impulse.NewUUID())
			store.users[waited] = &memUser{username: name, email: name, confirmed: true}
			store.users[other] = &memUser{username: "bob@corp.example", email: "bob@corp.example", confirmed: true}
			if tt.moved {
				store.users[waited].username, store.users[other].username = "alice.old@corp.example", name
			}
			// The completion of the wait carries the address the account's own code
			// confirms, as every completion of that code does.
			rules := Rules[*memTx]{ConfirmAddress: true, WaitedOn: impulse.NullUUIDFromUUID(waited), CodeAddress: &dbtype.CodeAddress{Held: addressOf(name), Email: name}}
			if tt.rule != nil {
				rules.Account = tt.rule(waited, other)
			}
			identity := *outside
			req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonStepUp, AuthEvents: code, Identity: &identity}

			err := SignIn(t.Context(), &memDriver{store: store}, impulse.Must(impulse.NewUUID()), &dbtype.InsertSession{}, req, rules)

			if !tt.wantRefused {
				if err != nil {
					t.Fatalf("SignIn() error = %v, want the identity linked and a session", err)
				}
				if linked, ok := store.links[linkKey(identity.Method, identity.Connection, identity.Subject)]; !ok || linked != waited {
					t.Errorf("the identity is linked to %v (linked %t), want the user the wait was for, %s", linked, ok, waited)
				}

				return
			}
			var refusal *sessioninfo.LoginRefusal
			if !errors.As(err, &refusal) || refusal.Code() != sessioninfo.RefusedIdentityRejected || !errors.Is(err, signin.ErrIdentityRejected) {
				t.Errorf("SignIn() error = %v, want the completion refused (identity_rejected)", err)
			}
			if len(store.links) != 0 {
				t.Errorf("the store holds %d identity link(s), want none: the identity is linked to the wait's user or to nobody", len(store.links))
			}
			if len(store.users) != 2 {
				t.Errorf("the store holds %d users, want the two that stood: no account is made for the identity", len(store.users))
			}
			if store.sessions != 0 {
				t.Errorf("the store holds %d session(s), want none", store.sessions)
			}
		})
	}
}

// TestSignIn_anInvitationsCodeLinksAnAccountMadeAheadOnlyWhereTheInvitationIsItsOwn pins
// what the code mailed for an invitation is worth to the first link of an account made
// ahead, in an application with no tenant directories, where the sign-in's name is all
// that ties the two. The code proves the person reads the invited mailbox, and nothing
// else: it links the account under the name only where the invitation is that account's
// own, mailed to its username or to the address it holds, confirmed or not, and the
// invitation is then accepted as it always is. An account that holds another address, or
// none, is refused as an acceptance with another address is: nothing is linked, the
// account's address is as it was, and the invitation stands for the person it was mailed
// to.
func TestSignIn_anInvitationsCodeLinksAnAccountMadeAheadOnlyWhereTheInvitationIsItsOwn(t *testing.T) {
	t.Parallel()

	const (
		invited = "nell@north.example"
		vera    = "vera@lakeside.edu"
	)
	tests := []struct {
		name string
		// username is the account's username, the name the directory signs in under;
		// address the address it holds, "" for none, and confirmed whether its person
		// confirmed it.
		username  string
		address   string
		confirmed bool
		wantLink  bool
	}{
		{name: "an account that holds the invited address unconfirmed is linked, and the invitation accepted", username: vera, address: invited, wantLink: true},
		{name: "an account that holds the invited address confirmed is linked, and the invitation accepted", username: vera, address: invited, confirmed: true, wantLink: true},
		{name: "an account under the invited address that holds no address is linked, and the invitation accepted", username: invited, wantLink: true},
		{name: "an account that holds another address is refused, with nothing linked and the invitation standing", username: vera, address: vera},
		{name: "an account that holds another address confirmed is refused the same", username: vera, address: vera, confirmed: true},
		{name: "an account that holds no address is refused the same", username: vera},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newMemStore()
			hooks := &roleHooks{store: store}
			userID := impulse.Must(impulse.NewUUID())
			store.users[userID] = &memUser{username: tt.username, email: tt.address, confirmed: tt.confirmed}
			inv := store.invite(invited, "lakeside", "Teacher")
			inv.AddressProven = true
			// A Microsoft directory signs the person in under the account's name and
			// sends no address; the sign-in entered the code mailed to the invited one.
			identity := &sessioninfo.Identity{Method: sessioninfo.MethodAzure, Connection: "tenant-1", Subject: "oid-1", SignInName: tt.username}
			req := &sessioninfo.NewSessionRequest{
				Reason: sessioninfo.ReasonStepUp, Identity: identity, Invitation: inv,
				AuthEvents: []sessioninfo.AuthEvent{{Method: sessioninfo.MethodAzure, Connection: "tenant-1"}, {Method: sessioninfo.MethodEmailCode}},
			}

			err := signInWith(t, &memDriver{store: store}, req, Rules[*memTx]{Policy: hooks.policy, Accept: hooks.accept, ConfirmAddress: true})

			user := store.users[userID]
			if tt.wantLink {
				if err != nil {
					t.Fatalf("SignIn() error = %v, want the account linked and the invitation accepted", err)
				}
				if linked, ok := store.links[linkKey(identity.Method, identity.Connection, identity.Subject)]; !ok || linked != userID {
					t.Errorf("the identity is linked to %v (linked %t), want the account under the name, %s", linked, ok, userID)
				}
				if !inv.Accepted || store.invitations[inv.ID] {
					t.Errorf("Invitation.Accepted = %v, still live = %v, want it accepted and consumed", inv.Accepted, store.invitations[inv.ID])
				}
				if user.email != invited || !user.confirmed {
					t.Errorf("the account holds %q (confirmed %v), want the invited address confirmed", user.email, user.confirmed)
				}
				if store.sessions != 1 {
					t.Errorf("the store holds %d session(s), want one", store.sessions)
				}

				return
			}
			if got := sessioninfo.LoginRefusalCodeOf(err); got != sessioninfo.RefusedInvitationAddress || !errors.Is(err, signin.ErrInvitationAddress) {
				t.Fatalf("SignIn() error = %v (code %q), want the refusal of an acceptance with another address", err, got)
			}
			if len(store.links) != 0 || store.sessions != 0 || len(store.granted) != 0 {
				t.Errorf("the store holds %d link(s), %d session(s) and grants %v, want nothing", len(store.links), store.sessions, store.granted)
			}
			if user.email != tt.address || user.confirmed != tt.confirmed {
				t.Errorf("the account holds %q (confirmed %v), want %q (confirmed %v) as it was", user.email, user.confirmed, tt.address, tt.confirmed)
			}
			if !store.invitations[inv.ID] || inv.Accepted {
				t.Errorf("the invitation is live = %v, Accepted = %v, want it standing and not accepted", store.invitations[inv.ID], inv.Accepted)
			}
		})
	}
}
