package signinflow

import (
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// TestAcceptance_theLinkAloneAcceptsNothing pins what an invitation's link is worth to
// an account that names itself: nothing. A mailed link can be forwarded, so holding it
// is no proof of reading the mailbox it was sent to. An account that signs in with its
// password, or in a session the application starts, or whose person is signed in and
// accepts by hand, accepts at once only where it holds the invited address confirmed.
// One that holds no address, or holds the invited one and has not confirmed it, is held
// to the code mailed to the invited address: the sign-in waits for it, naming the
// account, and the acceptance by hand, which cannot wait, is refused with what to do.
// Until the code is entered nothing is written: the invitation stands, no role is given
// and the account takes no address. Where the store's usernames are the addresses, an
// account under another name could never take the invited address, so it is refused as
// an acceptance with another address is and no code is mailed for it, whatever it has
// entered; the account under the invited address is held to the code like any other.
func TestAcceptance_theLinkAloneAcceptsNothing(t *testing.T) {
	t.Parallel()

	const invited = "sam@lakeside.edu"
	type account struct {
		name string
		// username is the name the account signs in under, "sam" where a case names
		// none, and addresses whether the store's usernames are the addresses
		// (UsernameIsEmail).
		username  string
		addresses bool
		// held is the address the account holds, "" for none, and confirmed whether its
		// person has confirmed it.
		held      string
		confirmed bool
		// elsewhere says another account holds the invited address.
		elsewhere bool
		// proven says the sign-in entered the code mailed to the invited address.
		proven bool
		// want is what the acceptance comes to: accepted, held for the code, or refused
		// as an acceptance with another address is.
		want string
	}
	accounts := []account{
		{name: "an account with no address", want: "code"},
		{name: "an account that holds the invited address and has not confirmed it", held: invited, want: "code"},
		{name: "an account that holds the invited address confirmed", held: invited, confirmed: true, want: "accepted"},
		{name: "an account with no address whose sign-in entered the code", proven: true, want: "accepted"},
		{name: "an account that holds the invited address unconfirmed and whose sign-in entered the code", held: invited, proven: true, want: "accepted"},
		{name: "an account with no address while another account holds the invited one", elsewhere: true, want: "refused"},
		{name: "an account with no address that entered the code while another account holds the invited one", elsewhere: true, proven: true, want: "refused"},
		{name: "an account under another address, where the usernames are the addresses", username: "sam@harbor.example", addresses: true, want: "refused"},
		{
			name:     "an account under another address that entered the code, where the usernames are the addresses",
			username: "sam@harbor.example", addresses: true, proven: true, want: "refused",
		},
		{name: "the account under the invited address, where the usernames are the addresses", username: invited, addresses: true, want: "code"},
		{
			name:     "the account under the invited address whose sign-in entered the code, where the usernames are the addresses",
			username: invited, addresses: true, proven: true, want: "accepted",
		},
	}
	for i := range accounts {
		if accounts[i].username == "" {
			accounts[i].username = "sam"
		}
	}
	// seed is a store that holds the account of a case, an invitation to the invited
	// address and, where the case says so, another account that holds that address.
	seed := func(tt *account, proven bool) (*memStore, impulse.UUID, *sessioninfo.Invitation) {
		store := newMemStore()
		id := impulse.Must(impulse.NewUUID())
		store.users[id] = &memUser{username: tt.username, email: tt.held, confirmed: tt.confirmed}
		if tt.elsewhere {
			store.users[impulse.Must(impulse.NewUUID())] = &memUser{username: "samantha", email: invited}
		}
		inv := store.invite(invited, "lakeside", "Coach")
		inv.AddressProven = proven

		return store, id, inv
	}
	assertNothingWritten := func(t *testing.T, store *memStore, hooks *roleHooks, id impulse.UUID, held string, inv *sessioninfo.Invitation) {
		t.Helper()

		if !store.invitations[inv.ID] || inv.Accepted || len(store.granted) != 0 || hooks.committed != 0 || store.sessions != 0 {
			t.Errorf("the invitation is live = %t and accepted = %t, with grants %v, %d committed run(s) and %d session(s); want it standing and nothing given",
				store.invitations[inv.ID], inv.Accepted, store.granted, hooks.committed, store.sessions)
		}
		if got := store.users[id].email; got != held {
			t.Errorf("the account's address = %q, want it left as %q", got, held)
		}
	}
	assertAccepted := func(t *testing.T, store *memStore, id impulse.UUID, username string, inv *sessioninfo.Invitation) {
		t.Helper()

		if !inv.Accepted || store.invitations[inv.ID] {
			t.Errorf("Accepted = %t, live = %t; want the invitation accepted and consumed", inv.Accepted, store.invitations[inv.ID])
		}
		if user := store.users[id]; user.email != invited || !user.confirmed {
			t.Errorf("the account holds %q, confirmed = %t; want the invited address, confirmed", user.email, user.confirmed)
		}
		if diff := cmp.Diff(map[string][]string{username: {"Coach"}}, store.granted); diff != "" {
			t.Errorf("the roles given (-want +got):\n%s", diff)
		}
	}
	assertAnotherAddress := func(t *testing.T, err error) {
		t.Helper()

		if got := sessioninfo.LoginRefusalCodeOf(err); got != sessioninfo.RefusedInvitationAddress || !errors.Is(err, signin.ErrInvitationAddress) || errors.Is(err, signin.ErrInvitationCode) {
			t.Fatalf("error = %v (code %q), want the refusal of an acceptance with another address, and no word of a code", err, got)
		}
		var wait *signin.PendingSignInError
		if errors.As(err, &wait) {
			t.Fatalf("error = %v, want a refusal and no wait: a sign-in held for the code has one mailed to the invited address", err)
		}
	}

	identities := []struct {
		name   string
		method sessioninfo.AuthMethod
	}{
		{name: "a password sign-in", method: sessioninfo.MethodPassword},
		{name: "a session the application starts", method: sessioninfo.MethodApplication},
	}
	for _, identity := range identities {
		for _, tt := range accounts {
			t.Run(identity.name+" by "+tt.name, func(t *testing.T) {
				t.Parallel()

				store, id, inv := seed(&tt, tt.proven)
				hooks := &roleHooks{store: store}
				req := &sessioninfo.NewSessionRequest{
					Reason: sessioninfo.ReasonLogin, Username: tt.username, UserID: id,
					Identity: &sessioninfo.Identity{Method: identity.method, Subject: id.String(), Email: tt.held, EmailVerified: tt.confirmed}, Invitation: inv,
				}

				err := signInWith(t, &memDriver{store: store, usernamesAreAddresses: tt.addresses}, req, Rules[*memTx]{Policy: hooks.policy, Accept: hooks.accept})

				switch tt.want {
				case "accepted":
					if err != nil {
						t.Fatalf("SignIn() error = %v, want the invitation accepted", err)
					}
					assertAccepted(t, store, id, tt.username, inv)
					if store.sessions != 1 {
						t.Errorf("sessions = %d, want one", store.sessions)
					}
				case "code":
					var wait *signin.PendingSignInError
					if !errors.As(err, &wait) || wait.Reason != sessioninfo.PendingMFA {
						t.Fatalf("SignIn() error = %v, want the sign-in held for the code mailed to the invited address", err)
					}
					if !wait.UserID.Valid || wait.UserID.UUID != id || wait.Username != tt.username {
						t.Errorf("the wait names the user %v (%q), want the account that signed in", wait.UserID, wait.Username)
					}
					if diff := cmp.Diff([]sessioninfo.AuthMethod{sessioninfo.MethodEmailCode}, wait.Factors); diff != "" {
						t.Errorf("the factors the wait accepts (-want +got):\n%s", diff)
					}
					assertNothingWritten(t, store, hooks, id, tt.held, inv)
				default:
					assertAnotherAddress(t, err)
					assertNothingWritten(t, store, hooks, id, tt.held, inv)
				}
			})
		}
	}

	for _, tt := range accounts {
		if tt.proven {
			// A person signed in enters no code: the acceptance by hand carries no proof.
			continue
		}
		t.Run("an acceptance by hand by "+tt.name, func(t *testing.T) {
			t.Parallel()

			store, id, inv := seed(&tt, false)
			hooks := &roleHooks{store: store}

			err := AcceptInvitation(t.Context(), &memDriver{store: store, usernamesAreAddresses: tt.addresses}, id, tt.username, tt.held, inv, hooks.accept)

			switch tt.want {
			case "accepted":
				if err != nil {
					t.Fatalf("AcceptInvitation() error = %v, want the invitation accepted", err)
				}
				assertAccepted(t, store, id, tt.username, inv)
			case "code":
				if got := sessioninfo.LoginRefusalCodeOf(err); got != sessioninfo.RefusedInvitationAddress || !errors.Is(err, signin.ErrInvitationCode) || !errors.Is(err, signin.ErrInvitationAddress) {
					t.Fatalf("AcceptInvitation() error = %v (code %q), want the refusal that sends the person to sign in again for the code", err, got)
				}
				assertNothingWritten(t, store, hooks, id, tt.held, inv)
			default:
				assertAnotherAddress(t, err)
				assertNothingWritten(t, store, hooks, id, tt.held, inv)
			}
		})
	}

	t.Run("an invitation that died is skipped by a sign-in that would have waited for its code, and the sign-in goes on", func(t *testing.T) {
		t.Parallel()

		store, id, inv := seed(&account{username: "sam"}, false)
		store.invitations[inv.ID] = false
		hooks := &roleHooks{store: store}
		req := &sessioninfo.NewSessionRequest{
			Reason: sessioninfo.ReasonLogin, Username: "sam", UserID: id,
			Identity: &sessioninfo.Identity{Method: sessioninfo.MethodPassword, Subject: id.String()}, Invitation: inv,
		}

		if err := signInWith(t, &memDriver{store: store}, req, Rules[*memTx]{Policy: hooks.policy, Accept: hooks.accept}); err != nil {
			t.Fatalf("SignIn() error = %v, want the sign-in to go on without the dead invitation", err)
		}
		if inv.Accepted || len(store.granted) != 0 || store.sessions != 1 {
			t.Errorf("Accepted = %t, granted = %v, sessions = %d; want nothing given and one session", inv.Accepted, store.granted, store.sessions)
		}
	})
}

// TestInvitedCodeMessage holds the sentence a person signed in is shown when their
// acceptance by hand needs the code: it says what to do and names the address the code
// goes to.
func TestInvitedCodeMessage(t *testing.T) {
	t.Parallel()

	got := InvitedCodeMessage(&sessioninfo.Invitation{Email: "sam@lakeside.edu"})
	want := "To accept this invitation, sign in again from the invitation's link. A code will be sent to sam@lakeside.edu to confirm the address is yours."
	if got != want {
		t.Errorf("InvitedCodeMessage() = %q, want %q", got, want)
	}
}
