package signinflow

import (
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
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

	t.Run("the sign-in that carries the code is accepted, and the account holds the invited address", func(t *testing.T) {
		t.Parallel()

		store := newMemStore()
		hooks := &roleHooks{store: store}
		inv := store.invite(invited, "lakeside", "Teacher")
		inv.AddressProven = true
		req := &sessioninfo.NewSessionRequest{
			Reason: sessioninfo.ReasonStepUp, Identity: claims(invited), Invitation: inv,
			AuthEvents: []sessioninfo.AuthEvent{{Method: sessioninfo.MethodAzure, Connection: "tenant-rogue"}, {Method: sessioninfo.MethodEmailCode}},
		}

		if err := signInWith(t, &memDriver{store: store}, req, rulesOf(hooks)); err != nil {
			t.Fatalf("SignIn() error = %v, want the invitation accepted", err)
		}
		if !inv.Accepted || store.invitations[inv.ID] {
			t.Errorf("Invitation.Accepted = %v, still live = %v, want it accepted and consumed", inv.Accepted, store.invitations[inv.ID])
		}
		_, made := store.userNamed(elsewhere)
		if made == nil || made.email != invited {
			t.Fatalf("the account the acceptance made = %+v, want one that holds %s", made, invited)
		}
		if diff := cmp.Diff([]string{"Teacher"}, store.granted[elsewhere]); diff != "" {
			t.Errorf("the roles given (-want +got):\n%s", diff)
		}
	})

	linked := []struct {
		name string
		// confirmed says the account the identity is linked to holds a confirmed address,
		// which is not the invited one.
		confirmed bool
	}{
		{name: "an account the identity is linked to, which holds another confirmed address, is refused though its directory claims the invited one", confirmed: true},
		{name: "an account the identity is linked to, which has confirmed no address, waits for the code"},
	}
	for _, tt := range linked {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newMemStore()
			hooks := &roleHooks{store: store}
			identity := claims(invited)
			userID := impulse.Must(impulse.NewUUID())
			store.users[userID] = &memUser{username: "mallory", confirmed: tt.confirmed}
			store.links[linkKey(identity.Method, identity.Connection, identity.Subject)] = userID
			inv := store.invite(invited, "lakeside", "Teacher")
			req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Identity: identity, Invitation: inv}

			err := signInWith(t, &memDriver{store: store}, req, rulesOf(hooks))

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
