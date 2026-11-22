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

// TestSignIn_anAddressTheMethodDidNotVerifyAcceptsNoInvitation pins what the address a
// directory asserts is worth to an invitation. An invitation is mailed to one address and
// accepted by the person who reads that mailbox. A method that verifies the address
// (Google) answers for it. One that does not (Microsoft Entra ID's email claim, which
// whoever runs a directory sets to any address they like) answers for nothing: a person
// who holds the link and a directory of their own would otherwise take the invitation,
// with its roles, and an account under the invited person's address. So such a sign-in
// is the sign-in of a directory that asserts no address: it waits for the code mailed to
// the invited address, naming no user and writing nothing, whether the address it claims
// is the invited one or another, and an account it is already linked to that holds
// another confirmed address is refused.
func TestSignIn_anAddressTheMethodDidNotVerifyAcceptsNoInvitation(t *testing.T) {
	t.Parallel()

	const (
		invited   = "nell@north.example"
		elsewhere = "mallory@rogue.example"
	)
	// claims is an identity of a directory anybody could run, which asserts address and
	// does not verify it.
	claims := func(address string) *sessioninfo.Identity {
		return &sessioninfo.Identity{Method: sessioninfo.MethodAzure, Connection: "tenant-rogue", Subject: "oid-mallory", Email: address, SignInName: elsewhere}
	}
	rulesOf := func(hooks *roleHooks) Rules[*memTx] {
		return Rules[*memTx]{Policy: hooks.policy, Accept: hooks.accept, ConfirmAddress: true}
	}
	assertWaitsForTheCode := func(t *testing.T, err error) {
		t.Helper()

		var wait *signin.PendingSignInError
		if !errors.As(err, &wait) || wait.Reason != sessioninfo.PendingMFA {
			t.Fatalf("SignIn() error = %v, want the sign-in held for the code mailed to the invited address", err)
		}
		if wait.UserID.Valid || wait.Username != "" {
			t.Errorf("the wait names the user %v (%q), want it to name nobody", wait.UserID, wait.Username)
		}
		if diff := cmp.Diff([]sessioninfo.AuthMethod{sessioninfo.MethodEmailCode}, wait.Factors); diff != "" {
			t.Errorf("the factors the wait accepts (-want +got):\n%s", diff)
		}
	}

	for _, claimed := range []string{invited, elsewhere} {
		t.Run("a person no account was made for, whose directory claims "+claimed+", waits for the code and nothing is made", func(t *testing.T) {
			t.Parallel()

			store := newMemStore()
			hooks := &roleHooks{store: store}
			inv := store.invite(invited, "lakeside", "Teacher")
			req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Identity: claims(claimed), Invitation: inv}

			err := signInWith(t, &memDriver{store: store}, req, rulesOf(hooks))

			assertWaitsForTheCode(t, err)
			assertNothingCommitted(t, store, hooks, inv)
		})
	}

	// The code is what accepts, so the account holds the address the code was mailed to,
	// whichever address the directory claims.
	for _, claimed := range []string{invited, elsewhere} {
		t.Run("the sign-in that carries the code is accepted though its directory claims "+claimed+", and the account holds the invited address", func(t *testing.T) {
			t.Parallel()

			store := newMemStore()
			hooks := &roleHooks{store: store}
			inv := store.invite(invited, "lakeside", "Teacher")
			inv.AddressProven = true
			req := &sessioninfo.NewSessionRequest{
				Reason: sessioninfo.ReasonStepUp, Identity: claims(claimed), Invitation: inv,
				AuthEvents: []sessioninfo.AuthEvent{{Method: sessioninfo.MethodAzure, Connection: "tenant-rogue"}, {Method: sessioninfo.MethodEmailCode}},
			}

			if err := signInWith(t, &memDriver{store: store}, req, rulesOf(hooks)); err != nil {
				t.Fatalf("SignIn() error = %v, want the invitation accepted", err)
			}
			if !inv.Accepted || store.invitations[inv.ID] {
				t.Errorf("Invitation.Accepted = %v, still live = %v, want it accepted and consumed", inv.Accepted, store.invitations[inv.ID])
			}
			made := store.userNamed(elsewhere)
			if made == nil || made.email != invited {
				t.Fatalf("the account the acceptance made = %+v, want one that holds %s", made, invited)
			}
			if diff := cmp.Diff([]string{"Teacher"}, store.granted[elsewhere]); diff != "" {
				t.Errorf("the roles given (-want +got):\n%s", diff)
			}
		})
	}

	linked := []struct {
		name string
		// confirmed says the account the identity is linked to holds a confirmed address,
		// which is not the invited one, and held is the address it holds unconfirmed, ""
		// for none; proven says the sign-in carries the code mailed to the invited address.
		confirmed bool
		held      string
		proven    bool
	}{
		{name: "an account the identity is linked to, which holds another confirmed address, is refused though its directory claims the invited one", confirmed: true},
		{name: "an account the identity is linked to, which has confirmed no address, waits for the code"},
		{name: "an account the identity is linked to, which holds another address unconfirmed, waits for the code", held: elsewhere},
		{name: "an account the identity is linked to, which holds another address unconfirmed, accepts with the code and holds the invited address confirmed", held: elsewhere, proven: true},
	}
	for _, tt := range linked {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newMemStore()
			hooks := &roleHooks{store: store}
			identity := claims(invited)
			userID := impulse.Must(impulse.NewUUID())
			store.users[userID] = &memUser{username: "mallory", email: tt.held, confirmed: tt.confirmed}
			store.links[linkKey(identity.Method, identity.Connection, identity.Subject)] = userID
			inv := store.invite(invited, "lakeside", "Teacher")
			req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Identity: identity, Invitation: inv}
			if tt.proven {
				inv.AddressProven = true
				req.Reason = sessioninfo.ReasonStepUp
				req.AuthEvents = []sessioninfo.AuthEvent{{Method: sessioninfo.MethodAzure, Connection: "tenant-rogue"}, {Method: sessioninfo.MethodEmailCode}}
			}

			err := signInWith(t, &memDriver{store: store}, req, rulesOf(hooks))

			if tt.proven {
				if err != nil {
					t.Fatalf("SignIn() error = %v, want the invitation accepted", err)
				}
				if !inv.Accepted || store.invitations[inv.ID] || store.sessions != 1 {
					t.Errorf("Invitation.Accepted = %v, still live = %v, sessions = %d, want it accepted and consumed, and a session", inv.Accepted, store.invitations[inv.ID], store.sessions)
				}
				if user := store.users[userID]; user.email != invited || !user.confirmed {
					t.Errorf("the account holds %q (confirmed %v), want the invited address confirmed", user.email, user.confirmed)
				}

				return
			}
			if tt.confirmed {
				if got := sessioninfo.LoginRefusalCodeOf(err); got != sessioninfo.RefusedInvitationAddress || !errors.Is(err, signin.ErrInvitationAddress) {
					t.Fatalf("SignIn() error = %v (code %q), want the refusal of an acceptance with another address", err, got)
				}
			} else {
				assertWaitsForTheCode(t, err)
			}
			if !store.invitations[inv.ID] || inv.Accepted || len(store.granted) != 0 || store.sessions != 0 {
				t.Errorf("the invitation is live = %v and accepted = %v, with grants %v and %d session(s), want it standing and nothing given", store.invitations[inv.ID], inv.Accepted, store.granted, store.sessions)
			}
			if got := store.users[userID].email; got != tt.held {
				t.Errorf("the account holds %q, want %q as it was", got, tt.held)
			}
		})
	}

	t.Run("a directory that verifies the address accepts with no code", func(t *testing.T) {
		t.Parallel()

		store := newMemStore()
		hooks := &roleHooks{store: store}
		inv := store.invite(invited, "lakeside", "Teacher")
		google := &sessioninfo.Identity{Method: sessioninfo.MethodGoogle, Connection: "north.example", Subject: "g-nell", Email: invited, EmailVerified: true, SignInName: invited}
		req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Identity: google, Invitation: inv}

		if err := signInWith(t, &memDriver{store: store}, req, rulesOf(hooks)); err != nil {
			t.Fatalf("SignIn() error = %v, want the invitation accepted", err)
		}
		if !inv.Accepted {
			t.Error("Invitation.Accepted = false, want accepted")
		}
	})

	t.Run("where no code is mailed, a registration whose accounts the deployment seeds, the address its own provider asserts is compared as before", func(t *testing.T) {
		t.Parallel()

		store := newMemStore()
		hooks := &roleHooks{store: store}
		rules := rulesOf(hooks)
		rules.ConfirmAddress = false

		other := store.invite(invited, "lakeside", "Teacher")
		req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Identity: claims(elsewhere), Invitation: other}
		err := signInWith(t, &memDriver{store: store}, req, rules)
		if got := sessioninfo.LoginRefusalCodeOf(err); got != sessioninfo.RefusedInvitationAddress {
			t.Fatalf("SignIn() as another address error = %v (code %q), want the refusal of an acceptance with another address", err, got)
		}
		assertNothingCommitted(t, store, hooks, other)

		req = &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Identity: claims(invited), Invitation: other}
		if err := signInWith(t, &memDriver{store: store}, req, rules); err != nil {
			t.Fatalf("SignIn() as the invited address error = %v, want the invitation accepted", err)
		}
		if !other.Accepted {
			t.Error("Invitation.Accepted = false, want accepted")
		}
	})
}

// TestSignIn_aDirectorySignInThatAPasswordConfirmedAcceptsNothingOnceItChanged pins the
// resolution of an identity whose link the account's password confirmed, and whose
// sign-in was then held, to that password: the resolution reads the user and checks the
// sign-in's proof (Rules.Proven) before anything is touched, consumed or accepted. A
// sign-in whose password changed meanwhile ends with dbtype.ErrPasswordChanged, as the
// transaction that inserts the session would end it, and the invitation it carries
// stands: no role is given, the account's address is as it was and no session starts,
// whether the acceptance rests on the code mailed to the invited address or on an
// address the method vouches for. A proof the user still holds resolves as before.
func TestSignIn_aDirectorySignInThatAPasswordConfirmedAcceptsNothingOnceItChanged(t *testing.T) {
	t.Parallel()

	const invited = "nell@north.example"
	proof := dbtype.ProofOfStored("the password hash the password was compared against")
	tests := []struct {
		name string
		// vouched says the identity is of a method that vouches for the invited address
		// it asserts; it is of one that asserts none, and the sign-in carries the code
		// mailed to the invited address, otherwise. changed says the password changed
		// while the sign-in waited.
		vouched bool
		changed bool
	}{
		{name: "the code mailed to the invited address, the password still held: accepted, and a session"},
		{name: "the code mailed to the invited address, the password changed: nothing accepted, and no session", changed: true},
		{name: "an address the method vouches for, the password changed: nothing accepted, and no session", vouched: true, changed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newMemStore()
			hooks := &roleHooks{store: store}
			userID := impulse.Must(impulse.NewUUID())
			user := &memUser{username: "nell", proof: proof}
			if tt.changed {
				user.proof = dbtype.ProofOfStored("the hash of the password set meanwhile")
			}
			store.users[userID] = user
			inv := store.invite(invited, "lakeside", "Teacher")
			identity := &sessioninfo.Identity{Method: sessioninfo.MethodAzure, Connection: "tenant-north", Subject: "oid-nell"}
			events := []sessioninfo.AuthEvent{{Method: sessioninfo.MethodAzure, Connection: "tenant-north"}, {Method: sessioninfo.MethodEmailCode}}
			if tt.vouched {
				identity = &sessioninfo.Identity{Method: sessioninfo.MethodGoogle, Connection: "north.example", Subject: "g-nell", Email: invited, EmailVerified: true}
				events = []sessioninfo.AuthEvent{{Method: sessioninfo.MethodGoogle, Connection: "north.example"}, {Method: sessioninfo.AuthMethod("totp")}}
			} else {
				inv.AddressProven = true
			}
			store.links[linkKey(identity.Method, identity.Connection, identity.Subject)] = userID
			req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonStepUp, Identity: identity, Invitation: inv, AuthEvents: events}
			rules := Rules[*memTx]{Policy: hooks.policy, Accept: hooks.accept, ConfirmAddress: true, Proven: proof}

			err := signInWith(t, &memDriver{store: store}, req, rules)

			if !tt.changed {
				if err != nil {
					t.Fatalf("SignIn() error = %v, want the invitation accepted", err)
				}
				if !inv.Accepted || store.invitations[inv.ID] || store.sessions != 1 {
					t.Errorf("Invitation.Accepted = %t, still live = %t, sessions = %d; want it accepted and consumed, and a session", inv.Accepted, store.invitations[inv.ID], store.sessions)
				}
				if diff := cmp.Diff(map[string][]string{"nell": {"Teacher"}}, store.granted); diff != "" {
					t.Errorf("the roles given (-want +got):\n%s", diff)
				}
				if got := store.users[userID]; got.email != invited || !got.confirmed {
					t.Errorf("the account holds %q (confirmed %t), want the invited address confirmed", got.email, got.confirmed)
				}

				return
			}
			wantCause(dbtype.ErrPasswordChanged)(t, err)
			if inv.Accepted || !store.invitations[inv.ID] || len(store.granted) != 0 || hooks.committed != 0 || store.sessions != 0 {
				t.Errorf("Invitation.Accepted = %t, still live = %t, grants %v, %d committed run(s), %d session(s); want the invitation standing and nothing given",
					inv.Accepted, store.invitations[inv.ID], store.granted, hooks.committed, store.sessions)
			}
			if got := store.users[userID]; got.email != "" || got.confirmed {
				t.Errorf("the account holds %q (confirmed %t), want no address, as it was", got.email, got.confirmed)
			}
		})
	}
}
