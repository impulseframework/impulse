package drivertest

// An identity the application has not seen is linked to an account on a name alone only
// as that account's first link. The standard account rule finds an account by the
// sign-in name the identity carries, and a name proves nothing: where a username is not
// an address, a person may hold an account under a colleague's sign-in name. Such a
// person signs up with a password under that name, signs in through the directory as
// themselves, confirms the link with the password, and removes the password. The
// account then has no password to confirm a link with and one identity on the
// directory's connection, and the colleague's first sign-in through the same directory
// arrives under the account's name. Linked there, the colleague would work inside an
// account another person signs in to. So the standard rule's match is never linked as
// a second identity: a second identity is linked on proof, to a user who holds the
// identity's verified address (testResolveByVerifiedAddress), by a user who confirms it
// with a password, by a registration's own rule on its own evidence, or by the
// application itself. The cases run through the real sign-up, sign-in and confirmation
// on both drivers.

import (
	"context"
	"testing"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// colleagueName is the name a colleague signs in with at the directory, which another
// person took as the username of an account of their own.
const colleagueName = "jordan.kim@lakeside.edu"

// takenName makes the account the cases start from, the way a person makes it for
// themselves: they sign up with a password under colleagueName, the address the mailed
// link proved being their own (signUpPerson); they sign in through the directory as
// themselves, which finds the account by that address and asks for the password; the
// password confirms the link; and they remove the password, which the link allows. It
// answers the account, which has no password and one identity, own.
func takenName(ctx context.Context, t *testing.T, in *AuthInstance, own *sessioninfo.Identity) *dbtype.SessionUser {
	t.Helper()

	password := passwordHashes(t, "a password of their own")[0]
	link := signUpLink(ctx, t, in.Driver, tokenHash(1))
	id, err := in.SignUp(ctx, &dbtype.InsertSessionUser{Username: colleagueName, Email: strPtr(signUpPerson), EmailVerified: true, PasswordHash: password}, link.ID)
	if err != nil {
		t.Fatalf("SignUp() under a colleague's sign-in name error = %v", err)
	}
	account := mustUser(ctx, t, in.Driver, id)

	// Their own identity carries their own name, which no account is under, and their
	// own address, which the account holds verified: the account is theirs, and its
	// password confirms the link.
	req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: own.SignInName, Identity: own}
	assertConfirmationPending(ctx, t, in.Driver, signInRequest(ctx, in, req), own, account, own.Organization)
	if err := in.LinkConfirmed(ctx, account.ID, account.Username, own, own.Organization, heldProof(ctx, t, in.Driver, account.ID)); err != nil {
		t.Fatalf("LinkConfirmed() on proof of the account's password error = %v", err)
	}
	linked, err := in.Driver.Identity(ctx, own.Method, own.Connection, own.Subject)
	if err != nil {
		t.Fatalf("Identity() of the confirmed link error = %v", err)
	}
	if err := in.Driver.RemoveProvenUserPassword(ctx, account.ID, mustUser(ctx, t, in.Driver, account.ID).PasswordHash, []impulse.UUID{linked.ID}); err != nil {
		t.Fatalf("RemoveProvenUserPassword() error = %v", err)
	}
	account = mustUser(ctx, t, in.Driver, account.ID)
	if account.PasswordHash != nil {
		t.Fatal("the account still has a password, want it removed")
	}

	return account
}

// assertOneIdentity fails unless the account holds exactly the one identity, own.
func assertOneIdentity(ctx context.Context, t *testing.T, d AuthDriver, account *dbtype.SessionUser, own *sessioninfo.Identity) {
	t.Helper()

	links, err := d.IdentitiesByUser(ctx, account.ID)
	if err != nil {
		t.Fatalf("IdentitiesByUser() error = %v", err)
	}
	if len(links) != 1 || links[0].Method != own.Method || links[0].Connection != own.Connection || links[0].Subject != own.Subject {
		t.Errorf("the account holds %d identities, want the one its person linked", len(links))
	}
}

func testSecondIdentityByName(_ context.Context, t *testing.T, h *AuthHarness) {
	t.Run("a colleague's first sign-in through the directory is not linked into an account another person made under the colleague's name: it is refused, nothing is linked, and the account's own person signs in as before", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		for _, accounts := range []Accounts{InviteOnly, Open} {
			in := h.New(ctx, t, Auth, AuthConfig{Auth: true, Accounts: Open})
			own := google("google-own", signUpPerson)
			account := takenName(ctx, t, in, own)
			if accounts == InviteOnly {
				// The choice for the method decides a person with no account, and this
				// name has one: the answer is the same once the method is closed again.
				if err := in.Driver.SetSignInMethod(ctx, &dbtype.SignInMethod{Name: MethodName, OpenSignUp: false}); err != nil {
					t.Fatalf("SetSignInMethod() error = %v", err)
				}
			}

			colleague := google("google-colleague", colleagueName)
			req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: colleagueName, Identity: colleague}
			assertRefused(t, signInRequest(ctx, in, req), sessioninfo.RefusedIdentityRejected, signin.ErrLinkSecondIdentity)
			assertNoLink(ctx, t, in.Driver, colleague)
			assertOneIdentity(ctx, t, in.Driver, account, own)

			// The account's own person signs in by the link they made.
			again := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: own.SignInName, Identity: own}
			if err := signInRequest(ctx, in, again); err != nil || again.UserID != account.ID || again.User.Source != sessioninfo.UserExistingLink {
				t.Errorf("the sign-in of the account's own identity = user %v (%+v), %v; want the account %v by its link", again.UserID, again.User, err, account.ID)
			}
		}
	})

	t.Run("an account's first link is still made on the name, and its second identity is not: a directory that issues a person a new subject under the same name is refused until the identity is linked for them", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		in := h.New(ctx, t, Auth, AuthConfig{Auth: true, Accounts: InviteOnly})

		// An account created ahead, with no password: its first
		// sign-in links it on the name, as before.
		const name = "pat.lee@lakeside.edu"
		pat := createUser(ctx, t, in.Driver, name)
		first := google("google-pat", name)
		req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: first}
		assertNewlyLinked(ctx, t, in.Driver, signInRequest(ctx, in, req), req, first, pat, "lakeside")

		// The directory issues the person a new subject under the same name (their
		// directory account was deleted and made again): the name alone links nothing.
		reissued := google("google-pat-reissued", name)
		req = &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: reissued}
		assertRefused(t, signInRequest(ctx, in, req), sessioninfo.RefusedIdentityRejected, signin.ErrLinkSecondIdentity)
		assertNoLink(ctx, t, in.Driver, reissued)
		assertOneIdentity(ctx, t, in.Driver, pat, first)

		// The application links the new identity itself (the store's LinkIdentity), and
		// the person signs in by it.
		mustLink(ctx, t, in.Driver, pat.ID, reissued)
		req = &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: reissued}
		if err := signInRequest(ctx, in, req); err != nil || req.UserID != pat.ID || req.User.Source != sessioninfo.UserExistingLink {
			t.Errorf("the sign-in of the identity an administrator linked = user %v (%+v), %v; want the account %v by its link", req.UserID, req.User, err, pat.ID)
		}
	})

	t.Run("a registration's own rule that links on its own evidence, and a trusted link, still add an identity on a connection the account signs in through; the rule's answer of Standard is held to the rule on a name", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		const name = "sam.ortiz@lakeside.edu"
		for _, tt := range []struct {
			what    string
			resolve func(user impulse.UUID) *signin.Resolution
			linked  bool
		}{
			{what: "the rule's own untrusted link", resolve: func(user impulse.UUID) *signin.Resolution { return signin.Link(user, false) }, linked: true},
			{what: "the rule's own trusted link", resolve: func(user impulse.UUID) *signin.Resolution { return signin.Link(user, true) }, linked: true},
			{what: "the rule's answer of Standard", resolve: func(impulse.UUID) *signin.Resolution { return signin.Standard() }},
		} {
			var sam *dbtype.SessionUser
			in := h.New(ctx, t, Auth, AuthConfig{Auth: true, Accounts: InviteOnly, Resolve: func(context.Context, HookTx, *sessioninfo.NewSessionRequest) (*signin.Resolution, error) {
				return tt.resolve(sam.ID), nil
			}})
			sam = createUser(ctx, t, in.Driver, name)
			first := google("google-sam", name)
			mustLink(ctx, t, in.Driver, sam.ID, first)

			second := google("google-sam-second", name)
			req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: second}
			err := signInRequest(ctx, in, req)
			if tt.linked {
				if err != nil || req.UserID != sam.ID {
					t.Errorf("%s: sign-in = user %v, %v; want the account %v linked", tt.what, req.UserID, err, sam.ID)
				}

				continue
			}
			assertRefused(t, err, sessioninfo.RefusedIdentityRejected, signin.ErrLinkSecondIdentity)
			assertNoLink(ctx, t, in.Driver, second)
			assertOneIdentity(ctx, t, in.Driver, sam, first)
		}
	})

	t.Run("an address proves the person only where the method verified it: an account that holds its address verified is still linked no second identity by a method that verifies none", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		in := h.New(ctx, t, Auth, AuthConfig{Auth: true, Accounts: InviteOnly})

		// The account holds its address verified, and signs in through an Entra tenant,
		// which verifies no address: a second identity under the account's name asserts
		// the account's address, and the assertion is the directory's word alone.
		const address = "pat.lee@lakeside.edu"
		holder := addressHolder(ctx, t, in.Driver, address, true, false)
		first := entra("oid-pat", addressHolderName)
		mustLink(ctx, t, in.Driver, holder.ID, first)

		second := entra("oid-pat-second", addressHolderName)
		second.Email = address
		req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: addressHolderName, Identity: second}
		assertRefused(t, signInRequest(ctx, in, req), sessioninfo.RefusedIdentityRejected, signin.ErrLinkSecondIdentity)
		assertNoLink(ctx, t, in.Driver, second)
		assertOneIdentity(ctx, t, in.Driver, holder, first)
	})

	t.Run("an identity from another connection under the account's name keeps the refusal it had, and a person with a password confirms a second identity with it", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		in := h.New(ctx, t, Auth, AuthConfig{Auth: true, Accounts: InviteOnly})

		const name = "lee.park@lakeside.edu"
		lee := createUser(ctx, t, in.Driver, name)
		first := google("google-lee", name)
		mustLink(ctx, t, in.Driver, lee.ID, first)

		other := entra("oid-lee", name)
		req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: other}
		assertRefused(t, signInRequest(ctx, in, req), sessioninfo.RefusedIdentityRejected, signin.ErrLinkCrossConnection)
		assertNoLink(ctx, t, in.Driver, other)

		// With a password, the person proves the account is theirs: the sign-in waits
		// for it, and the confirmed link is a second identity on the same connection.
		if err := in.Driver.ReplaceUserPassword(ctx, lee.ID, passwordHashes(t, "a password")[0], false); err != nil {
			t.Fatalf("ReplaceUserPassword() error = %v", err)
		}
		second := google("google-lee-second", name)
		req = &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: second}
		assertConfirmationPending(ctx, t, in.Driver, signInRequest(ctx, in, req), second, lee, "lakeside")
		if err := in.LinkConfirmed(ctx, lee.ID, lee.Username, second, "lakeside", heldProof(ctx, t, in.Driver, lee.ID)); err != nil {
			t.Fatalf("LinkConfirmed() on proof of the password error = %v", err)
		}
		assertLinkedTo(ctx, t, in.Driver, second, lee.ID, "lakeside")
	})
}
