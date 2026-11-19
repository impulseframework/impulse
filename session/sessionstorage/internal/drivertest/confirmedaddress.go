package drivertest

import (
	"context"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/securehash"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// testAddressConfirmedFirst runs the flow's hold for an unconfirmed address
// (signinflow.addressHold) against the row as each driver reads it in the sign-in's
// transaction (UserIn). A stranger an open method admits through a directory that does
// not vouch for their address is given an account and no session: the sign-in waits for
// the code. It waits again while the row holds no address, and while it holds one
// nobody confirmed; once the row holds a confirmed address the same identity signs in.
// A directory that vouches for the address is not held at all.
func testAddressConfirmedFirst(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true, Accounts: Open, ConfirmAddress: true})
	pat := entra("entra-pat", "pat@lakeside.edu")

	held := func(what string) *signin.PendingSignInError {
		t.Helper()
		_, _, err := signIn(ctx, in, pat, sessioninfo.ReasonLogin)
		var wait *signin.PendingSignInError
		if !errors.As(err, &wait) || wait.Reason != sessioninfo.PendingMFA || !wait.UserID.Valid {
			t.Fatalf("%s: sign-in error = %v, want the sign-in held for the code that confirms the address", what, err)
		}
		if len(wait.Factors) != 1 || wait.Factors[0] != sessioninfo.MethodEmailCode {
			t.Errorf("%s: the wait accepts %v, want the mailed code alone", what, wait.Factors)
		}

		return wait
	}

	// The first sign-in makes the account, which holds no address, and no session.
	wait := held("an account that holds no address")
	user := mustUser(ctx, t, in.Driver, wait.UserID.UUID)
	assertEmail(t, "User() after the held sign-in", user, nil, false)

	// An address on the row that nobody confirmed changes nothing.
	if err := in.Driver.SetUserEmail(ctx, user.ID, strPtr("pat@lakeside.edu")); err != nil {
		t.Fatalf("SetUserEmail() error = %v", err)
	}
	if again := held("an account that holds an unconfirmed address"); again.UserID.UUID != user.ID {
		t.Errorf("the second held sign-in names user %s, want the account the first made, %s", again.UserID.UUID, user.ID)
	}

	// The code's completion writes the address confirmed (VerifyUserEmail): from then
	// on the person signs in.
	if err := in.Driver.VerifyUserEmail(ctx, user.ID, "pat@lakeside.edu"); err != nil {
		t.Fatalf("VerifyUserEmail() error = %v", err)
	}
	if _, _, err := signIn(ctx, in, pat, sessioninfo.ReasonLogin); err != nil {
		t.Errorf("sign-in of an account that holds a confirmed address error = %v, want a session", err)
	}

	// A directory that vouches for the address is not held: the address is claimed as
	// the session starts.
	if _, _, err := signIn(ctx, in, google("google-sam", "sam@lakeside.edu"), sessioninfo.ReasonLogin); err != nil {
		t.Errorf("first sign-in through a directory that vouches for the address error = %v, want a session", err)
	}
}

// testUnverifiedClaimFindsNoAccount holds the account a sign-in finds by an address to
// the address the method vouched for. A directory that asserts an address it does not
// vouch for (Microsoft Entra ID's email claim) names an address anybody's directory
// could write: it finds no account, even one that holds that address confirmed and that
// nobody signs in to yet, which would otherwise take the identity as its first way in.
// An open method makes the person an account of their own, and the account that holds
// the address is left as it was.
func testUnverifiedClaimFindsNoAccount(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true, Accounts: Open})
	const address = "ivy@north.example"
	ivy, err := in.Driver.CreateUser(ctx, &dbtype.InsertSessionUser{Username: "ivy", Email: strPtr(address), EmailVerified: true}, nil)
	if err != nil {
		t.Fatalf("CreateUser(%q) error = %v", "ivy", err)
	}
	claims := entra("oid-ivy-claim", "ivy.claim@corp.example")
	claims.Email = address

	_, req, err := signIn(ctx, in, claims, sessioninfo.ReasonLogin)
	assertProvisioned(ctx, t, in.Driver, err, req, claims, "tenant-1")
	if req.UserID == ivy.ID {
		t.Errorf("the sign-in that claims %s unverified signed in as the account that holds it, want an account of its own", address)
	}
	links, err := in.Driver.IdentitiesByUser(ctx, ivy.ID)
	if err != nil {
		t.Fatalf("IdentitiesByUser() error = %v", err)
	}
	if len(links) != 0 {
		t.Errorf("the account that holds the address holds %d links after a sign-in that only claimed it, want none", len(links))
	}
	assertEmail(t, "the account that holds the address", mustUser(ctx, t, in.Driver, ivy.ID), strPtr(address), true)
}

// noAddress is a person the directory presents under signInName and asserts no address
// for, as Microsoft Entra ID does unless its registration is set up to send one.
func noAddress(subject, signInName string) *sessioninfo.Identity {
	identity := entra(subject, signInName)
	identity.Email = ""

	return identity
}

// withInvitedCode is the sign-in of identity that carries the code mailed to the invited
// address of the invitation link: the one that completes the wait of an acceptance that
// rests on the code.
func withInvitedCode(identity *sessioninfo.Identity, link *dbtype.MailLink) *sessioninfo.NewSessionRequest {
	req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonStepUp, Identity: identity, Invitation: invitationOf(link), AuthEvents: []sessioninfo.AuthEvent{{Method: sessioninfo.MethodAzure, Connection: "tenant-1"}, {Method: sessioninfo.MethodEmailCode}}}
	req.Invitation.AddressProven = true

	return req
}

// userHolding creates a user named username who holds email, confirmed when confirmed
// says so, with no password and no identity: an account nobody signs in to yet.
func userHolding(ctx context.Context, t *testing.T, d AuthDriver, username, email string, confirmed bool) *dbtype.SessionUser {
	t.Helper()

	user, err := d.CreateUser(ctx, &dbtype.InsertSessionUser{Username: username, Email: &email, EmailVerified: confirmed}, nil)
	if err != nil {
		t.Fatalf("CreateUser(%q) error = %v", username, err)
	}

	return user
}

// assertInvitationStands fails unless link is still live on the instance and the sign-in
// did not accept it. It does not read the acceptor's record as assertInvitationKept
// does: the record is kept under the address, which an earlier acceptance of the same
// test may hold.
func assertInvitationStands(ctx context.Context, t *testing.T, what string, on *AuthInstance, link *dbtype.MailLink, inv *sessioninfo.Invitation) {
	t.Helper()

	if _, err := on.Driver.MailLinkByToken(ctx, link.TokenHash, time.Now()); err != nil {
		t.Errorf("%s: MailLinkByToken() error = %v, want the invitation still live", what, err)
	}
	if inv.Accepted {
		t.Errorf("%s: Invitation.Accepted = true, want false", what)
	}
}

// testInvitationByCode runs the acceptance of an invitation through a directory that
// asserts no address, as Microsoft Entra ID does unless its registration is set up to
// send one, on an Auth that sends mail. Such a sign-in has no address to compare with
// the invited one, so the acceptance rests on the code the library mails to the invited
// address: until the sign-in carries that proof (Invitation.AddressProven) it waits,
// naming no user, and nothing is made, linked, consumed or granted. The sign-in that
// carries it accepts in its own transaction: the account is made with the invited
// address confirmed, the identity linked, the invitation consumed and its roles given.
//
// A person the identity is already linked to accepts with the account they have: at
// once when it holds the invited address confirmed, with the code when it has confirmed
// none, and never when it holds another confirmed address or another account holds the
// invited one.
//
// The rest of the acceptance by code is held by the tests beside this one: what the
// code does for an account that already holds the invited address
// (testInvitationByCodeAddressHeld), which account is not the one invited
// (testInvitationByCodeNotTheInvited), and that the code proves its invitation's
// acceptance and nothing else (testInvitationByCodeAlone).
func testInvitationByCode(ctx context.Context, t *testing.T, h *AuthHarness) {
	rec := newAcceptances()
	in := h.New(ctx, t, AuthWithAppTables, AuthConfig{Auth: true, Accounts: InviteOnly, ConfirmAddress: true, Accept: rec.hook})

	// A new person: held, with nothing written, until the sign-in carries the proof.
	const invited = "nell@north.example"
	nell := noAddress("oid-nell", "nell@corp.example")
	link := insertInvitation(ctx, t, in.Driver, invited, "lakeside", []string{"Teacher"}, tokenHash(1))
	req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Identity: nell, Invitation: invitationOf(link)}
	assertWaitsForInvitedCode(t, "a new person without the code", signInRequest(ctx, in, req))
	assertInvitationKept(ctx, t, in, rec, link, req.Invitation)
	assertNoUser(ctx, t, in.Driver, nell.SignInName)
	assertNoLink(ctx, t, in.Driver, nell)

	req = withInvitedCode(nell, link)
	err := signInRequest(ctx, in, req)
	assertProvisioned(ctx, t, in.Driver, err, req, nell, "tenant-1")
	if !req.Invitation.Accepted {
		t.Error("Invitation.Accepted = false after the sign-in that carried the code, want accepted")
	}
	made := mustUser(ctx, t, in.Driver, req.UserID)
	assertEmail(t, "the account the acceptance made", made, strPtr(invited), true)
	assertAccepted(t, rec, "accepted:"+invited, made.ID.String()+" "+nell.SignInName+" lakeside Teacher")
	if consumedAt := h.MailLinkConsumedAt(ctx, t, in.Raw, link.TokenHash); consumedAt == nil {
		t.Error("the invitation's ConsumedAt = nil after the acceptance, want consumed")
	}
	if req.Identity.Email != "" || req.Identity.EmailVerified {
		t.Errorf("the identity the sign-in carried was changed to %q (verified %v), want it left as the directory asserted it", req.Identity.Email, req.Identity.EmailVerified)
	}

	// The account they have now holds the invited address confirmed: a second
	// invitation to it is accepted at once, with no code.
	again := insertInvitation(ctx, t, in.Driver, invited, "", []string{"Admin"}, tokenHash(2))
	req = &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Identity: nell, Invitation: invitationOf(again)}
	if err := signInRequest(ctx, in, req); err != nil || !req.Invitation.Accepted {
		t.Errorf("an invitation to the address the linked account holds confirmed: error = %v, accepted = %v, want it accepted with no code", err, req.Invitation.Accepted)
	}

	// An invitation to another address is not that account's, with the code or without.
	const elsewhere = "nell@south.example"
	other := insertInvitation(ctx, t, in.Driver, elsewhere, "", []string{"Admin"}, tokenHash(3))
	for _, proven := range []bool{false, true} {
		req = &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Identity: nell, Invitation: invitationOf(other)}
		req.Invitation.AddressProven = proven
		assertRefused(t, signInRequest(ctx, in, req), sessioninfo.RefusedInvitationAddress, signin.ErrInvitationAddress)
		assertInvitationKept(ctx, t, in, rec, other, req.Invitation)
	}

	// A linked account that has confirmed no address waits for the code, and the
	// acceptance that carries it writes the invited address on the account.
	const late = "omar@north.example"
	omar := createUser(ctx, t, in.Driver, "omar@corp.example")
	omarIdentity := noAddress("oid-omar", "omar@corp.example")
	mustLink(ctx, t, in.Driver, omar.ID, omarIdentity)
	lateLink := insertInvitation(ctx, t, in.Driver, late, "", []string{"Admin"}, tokenHash(4))
	req = &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Identity: omarIdentity, Invitation: invitationOf(lateLink)}
	assertWaitsForInvitedCode(t, "a linked account with no confirmed address, without the code", signInRequest(ctx, in, req))
	assertInvitationKept(ctx, t, in, rec, lateLink, req.Invitation)
	assertEmail(t, "the linked account before the code", mustUser(ctx, t, in.Driver, omar.ID), nil, false)
	req = withInvitedCode(omarIdentity, lateLink)
	if err := signInRequest(ctx, in, req); err != nil || !req.Invitation.Accepted {
		t.Fatalf("the linked account's acceptance with the code: error = %v, accepted = %v, want accepted", err, req.Invitation.Accepted)
	}
	assertEmail(t, "the linked account after the code", mustUser(ctx, t, in.Driver, omar.ID), strPtr(late), true)

	// A linked account with no confirmed address is not the one invited where another
	// account holds the invited address.
	quinn := createUser(ctx, t, in.Driver, "quinn@corp.example")
	quinnIdentity := noAddress("oid-quinn", "quinn@corp.example")
	mustLink(ctx, t, in.Driver, quinn.ID, quinnIdentity)
	held := insertInvitation(ctx, t, in.Driver, late, "", []string{"Admin"}, tokenHash(5))
	req = &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Identity: quinnIdentity, Invitation: invitationOf(held)}
	req.Invitation.AddressProven = true
	assertRefused(t, signInRequest(ctx, in, req), sessioninfo.RefusedInvitationAddress, signin.ErrInvitationAddress)
	assertInvitationStands(ctx, t, "the invitation to an address another account holds", in, held, req.Invitation)
}

// testInvitationByCodeAddressHeld runs the acceptance that rests on the mailed code
// (testInvitationByCode) where an account the identity is not linked to already holds
// the invited address. That account is the person's under the rules of any link, and
// the code adds no way in to an account somebody can already sign in to: with a
// password it waits for the password; without one, an account that signs in through an
// identity is refused the new one, of another connection or of its own; and an account
// nobody signs in to yet takes its first identity from the person who holds its
// invitation.
//
// The account that holds the invited address is the one invited whether or not the
// address was ever confirmed (an administrator entered it): the code finds that account
// and makes no second one.
func testInvitationByCodeAddressHeld(ctx context.Context, t *testing.T, h *AuthHarness) {
	rec := newAcceptances()
	in := h.New(ctx, t, AuthWithAppTables, AuthConfig{Auth: true, Accounts: InviteOnly, ConfirmAddress: true, Accept: rec.hook})

	// With a password, the new connection's identity waits for the password.
	guarded := h.New(ctx, t, AuthWithAppTables, AuthConfig{Auth: true, Accounts: InviteOnly, ConfirmAddress: true, Accept: rec.hook})
	const taken = "pat@north.example"
	holder := addressHolder(ctx, t, guarded.Driver, taken, true, true)
	stranger := noAddress("oid-pat", "pat@corp.example")
	takenLink := insertInvitation(ctx, t, guarded.Driver, taken, "", []string{"Admin"}, tokenHash(6))
	req := withInvitedCode(stranger, takenLink)
	assertConfirmationPending(ctx, t, guarded.Driver, signInRequest(ctx, guarded, req), stranger, holder, "tenant-1")
	assertInvitationKept(ctx, t, guarded, rec, takenLink, req.Invitation)

	// Without a password, an account that signs in through another connection has
	// nothing to confirm the link with, and the identity is refused.
	const ruthAddress = "ruth@north.example"
	ruth := userHolding(ctx, t, in.Driver, "ruth", ruthAddress, true)
	mustLink(ctx, t, in.Driver, ruth.ID, google("google-ruth", ruthAddress))
	ruthIdentity := noAddress("oid-ruth", "ruth@corp.example")
	ruthLink := insertInvitation(ctx, t, in.Driver, ruthAddress, "", []string{"Admin"}, tokenHash(7))
	req = withInvitedCode(ruthIdentity, ruthLink)
	assertRefused(t, signInRequest(ctx, in, req), sessioninfo.RefusedIdentityRejected, signin.ErrLinkCrossConnection)
	assertInvitationKept(ctx, t, in, rec, ruthLink, req.Invitation)
	assertNoLink(ctx, t, in.Driver, ruthIdentity)

	// Nor is a second identity of the connection the account already signs in through
	// linked on the code: that takes the directory's own word for the address.
	const samAddress = "sam@north.example"
	sam := userHolding(ctx, t, in.Driver, "sam", samAddress, true)
	mustLink(ctx, t, in.Driver, sam.ID, noAddress("oid-sam-first", "sam.first@corp.example"))
	samIdentity := noAddress("oid-sam-second", "sam.second@corp.example")
	samLink := insertInvitation(ctx, t, in.Driver, samAddress, "", []string{"Admin"}, tokenHash(8))
	req = withInvitedCode(samIdentity, samLink)
	assertRefused(t, signInRequest(ctx, in, req), sessioninfo.RefusedIdentityRejected, signin.ErrLinkSecondIdentity)
	assertInvitationKept(ctx, t, in, rec, samLink, req.Invitation)
	assertNoLink(ctx, t, in.Driver, samIdentity)

	// An account nobody signs in to yet (no password, no identity) takes its first
	// identity from the person who holds its own invitation, as it does through any
	// method, and no second account is made for the address.
	const toriAddress = "tori@north.example"
	tori := userHolding(ctx, t, in.Driver, "tori", toriAddress, true)
	toriIdentity := noAddress("oid-tori", "tori@corp.example")
	toriLink := insertInvitation(ctx, t, in.Driver, toriAddress, "", []string{"Admin"}, tokenHash(9))
	req = withInvitedCode(toriIdentity, toriLink)
	if err := signInRequest(ctx, in, req); err != nil || !req.Invitation.Accepted || req.UserID != tori.ID {
		t.Fatalf("the first identity of an account nobody signs in to: error = %v, accepted = %v, user = %s, want %s accepted", err, req.Invitation.Accepted, req.UserID, tori.ID)
	}
	assertLinkedTo(ctx, t, in.Driver, toriIdentity, tori.ID, "tenant-1")
	assertNoUser(ctx, t, in.Driver, toriIdentity.SignInName)

	// An address an administrator entered, which nobody confirmed yet, is kept for its
	// account, and the invitation mailed to it is that account's own: the code finds
	// the account as it finds one that holds the address confirmed, and makes no second
	// one. An account nobody signs in to takes the identity and holds the address
	// confirmed from then on.
	const umaAddress = "uma@north.example"
	uma := userHolding(ctx, t, in.Driver, "uma", umaAddress, false)
	umaIdentity := noAddress("oid-uma", "uma@corp.example")
	umaLink := insertInvitation(ctx, t, in.Driver, umaAddress, "", []string{"Admin"}, tokenHash(13))
	req = withInvitedCode(umaIdentity, umaLink)
	if err := signInRequest(ctx, in, req); err != nil || !req.Invitation.Accepted || req.UserID != uma.ID {
		t.Fatalf("the first identity of an account that holds the invited address unconfirmed: error = %v, accepted = %v, user = %s, want %s accepted", err, req.Invitation.Accepted, req.UserID, uma.ID)
	}
	assertLinkedTo(ctx, t, in.Driver, umaIdentity, uma.ID, "tenant-1")
	assertNoUser(ctx, t, in.Driver, umaIdentity.SignInName)
	assertEmail(t, "the account that held the invited address unconfirmed", mustUser(ctx, t, in.Driver, uma.ID), strPtr(umaAddress), true)

	// With a password, such an account waits for the password too.
	wary := h.New(ctx, t, AuthWithAppTables, AuthConfig{Auth: true, Accounts: InviteOnly, ConfirmAddress: true, Accept: rec.hook})
	const valAddress = "val@north.example"
	val := addressHolder(ctx, t, wary.Driver, valAddress, false, true)
	valIdentity := noAddress("oid-val", "val@corp.example")
	valLink := insertInvitation(ctx, t, wary.Driver, valAddress, "", []string{"Admin"}, tokenHash(14))
	req = withInvitedCode(valIdentity, valLink)
	assertConfirmationPending(ctx, t, wary.Driver, signInRequest(ctx, wary, req), valIdentity, val, "tenant-1")
	assertInvitationStands(ctx, t, "the invitation to an unconfirmed address of an account with a password", wary, valLink, req.Invitation)
	assertNoUser(ctx, t, wary.Driver, valIdentity.SignInName)
}

// testInvitationByCodeNotTheInvited holds the acceptance that rests on the mailed code
// (testInvitationByCode) to the account the invitation was mailed to. While another
// account holds the invited address, confirmed or not, no other account is the one
// invited: not one the identity is linked to, and not one the directory's name finds,
// which with a password is refused before its password is asked for. Where the
// usernames are the addresses, the invited address is the account's under that name
// alone.
func testInvitationByCodeNotTheInvited(ctx context.Context, t *testing.T, h *AuthHarness) {
	rec := newAcceptances()
	in := h.New(ctx, t, AuthWithAppTables, AuthConfig{Auth: true, Accounts: InviteOnly, ConfirmAddress: true, Accept: rec.hook})

	// A linked account is not the one invited while another account holds the invited
	// address, confirmed or not.
	const xanAddress = "xan@north.example"
	userHolding(ctx, t, in.Driver, "xan", xanAddress, false)
	yan := createUser(ctx, t, in.Driver, "yan@corp.example")
	yanIdentity := noAddress("oid-yan", "yan@corp.example")
	mustLink(ctx, t, in.Driver, yan.ID, yanIdentity)
	xanLink := insertInvitation(ctx, t, in.Driver, xanAddress, "", []string{"Admin"}, tokenHash(15))
	req := withInvitedCode(yanIdentity, xanLink)
	assertRefused(t, signInRequest(ctx, in, req), sessioninfo.RefusedInvitationAddress, signin.ErrInvitationAddress)
	assertInvitationStands(ctx, t, "the invitation to an address another account holds unconfirmed", in, xanLink, req.Invitation)
	assertEmail(t, "the linked account that is not the one invited", mustUser(ctx, t, in.Driver, yan.ID), nil, false)

	// Nor is an account the directory's name finds: it would spend an invitation that is
	// the address holder's own, and could not take the address the code proved.
	bea := createUser(ctx, t, in.Driver, "bea@corp.example")
	beaIdentity := noAddress("oid-bea", "bea@corp.example")
	req = withInvitedCode(beaIdentity, xanLink)
	assertRefused(t, signInRequest(ctx, in, req), sessioninfo.RefusedInvitationAddress, signin.ErrInvitationAddress)
	assertInvitationStands(ctx, t, "the invitation to an address another account holds, carried under the name of an account nobody signs in to", in, xanLink, req.Invitation)
	assertNoLink(ctx, t, in.Driver, beaIdentity)
	assertEmail(t, "the account found under the directory's name", mustUser(ctx, t, in.Driver, bea.ID), nil, false)

	// An account with a password the directory's name finds is refused the same way,
	// before its password is asked for: entering that password could not make the
	// invitation its own.
	calHash, err := securehash.New(securehash.Argon2()).Hash("password")
	if err != nil {
		t.Fatal(err)
	}
	cal, err := in.Driver.CreateUser(ctx, &dbtype.InsertSessionUser{Username: "cal@corp.example", PasswordHash: calHash}, nil)
	if err != nil {
		t.Fatalf("CreateUser(%q) error = %v", "cal@corp.example", err)
	}
	calIdentity := noAddress("oid-cal", "cal@corp.example")
	req = withInvitedCode(calIdentity, xanLink)
	assertRefused(t, signInRequest(ctx, in, req), sessioninfo.RefusedInvitationAddress, signin.ErrInvitationAddress)
	assertInvitationStands(ctx, t, "the invitation to an address another account holds, carried under the name of an account with a password", in, xanLink, req.Invitation)
	assertNoLink(ctx, t, in.Driver, calIdentity)
	assertEmail(t, "the account with a password found under the directory's name", mustUser(ctx, t, in.Driver, cal.ID), nil, false)

	// Where the usernames are the addresses, an account's address is its name. The
	// invited address is the account's under that name and no other's: a person the
	// directory presents under another name would get an account that cannot hold the
	// invited address, so the code makes none; one presented under the invited address
	// gets the account, confirmed.
	named := h.New(ctx, t, AuthUsernameIsEmail, AuthConfig{Auth: true, Accounts: InviteOnly, ConfirmAddress: true, Accept: rec.hook})
	const zoeAddress = "zoe@north.example"
	zoeLink := insertInvitation(ctx, t, named.Driver, zoeAddress, "", []string{"Admin"}, tokenHash(16))
	elsewhereNamed := noAddress("oid-zoe-other", "zoe@corp.example")
	req = withInvitedCode(elsewhereNamed, zoeLink)
	assertRefused(t, signInRequest(ctx, named, req), sessioninfo.RefusedInvitationAddress, signin.ErrInvitationAddress)
	assertInvitationStands(ctx, t, "the invitation where the usernames are the addresses", named, zoeLink, req.Invitation)
	assertNoUser(ctx, t, named.Driver, elsewhereNamed.SignInName)
	assertNoLink(ctx, t, named.Driver, elsewhereNamed)
	zoe := noAddress("oid-zoe", zoeAddress)
	req = withInvitedCode(zoe, zoeLink)
	if err := signInRequest(ctx, named, req); err != nil || !req.Invitation.Accepted {
		t.Fatalf("the acceptance under the invited address, where the usernames are the addresses: error = %v, accepted = %v, want accepted", err, req.Invitation.Accepted)
	}
	assertEmail(t, "the account made under the invited address", mustUser(ctx, t, named.Driver, req.UserID), strPtr(zoeAddress), true)
}

// testInvitationByCodeAlone holds the mailed code (testInvitationByCode) to what it
// proves: the acceptance of the invitation it was mailed for, and nothing else. The
// sign-in that carries it stands on the invitation: where the invitation is dead by the
// time the sign-in runs, it is refused and nothing is made, linked or confirmed, though
// the person would be admitted without any invitation.
//
// An Auth that sends no mail has no code to mail: such a sign-in is refused there, as
// one whose address is not the invited one.
func testInvitationByCodeAlone(ctx context.Context, t *testing.T, h *AuthHarness) {
	rec := newAcceptances()
	in := h.New(ctx, t, AuthWithAppTables, AuthConfig{Auth: true, Accounts: InviteOnly, ConfirmAddress: true, Accept: rec.hook})

	// Without the invitation each of these two would be admitted: a method open to
	// sign-up gives a stranger an account, and a person the identity is linked to has
	// one.
	open := h.New(ctx, t, AuthWithAppTables, AuthConfig{Auth: true, Accounts: Open, ConfirmAddress: true, Accept: rec.hook})
	vic := noAddress("oid-vic", "vic@corp.example")
	dead := insertInvitation(ctx, t, open.Driver, "vic@north.example", "", []string{"Admin"}, tokenHash(11))
	if err := open.Driver.ConsumeMailLink(ctx, dead.TokenHash, time.Now()); err != nil {
		t.Fatalf("ConsumeMailLink() error = %v", err)
	}
	req := withInvitedCode(vic, dead)
	assertRefused(t, signInRequest(ctx, open, req), sessioninfo.RefusedInvitationExpired, signin.ErrInvitationExpired)
	assertNoUser(ctx, t, open.Driver, vic.SignInName)
	assertNoLink(ctx, t, open.Driver, vic)

	wes := createUser(ctx, t, in.Driver, "wes@corp.example")
	wesIdentity := noAddress("oid-wes", "wes@corp.example")
	mustLink(ctx, t, in.Driver, wes.ID, wesIdentity)
	spent := insertInvitation(ctx, t, in.Driver, "wes@north.example", "", []string{"Admin"}, tokenHash(12))
	if err := in.Driver.ConsumeMailLink(ctx, spent.TokenHash, time.Now()); err != nil {
		t.Fatalf("ConsumeMailLink() error = %v", err)
	}
	req = withInvitedCode(wesIdentity, spent)
	assertRefused(t, signInRequest(ctx, in, req), sessioninfo.RefusedInvitationExpired, signin.ErrInvitationExpired)
	assertEmail(t, "the linked account whose invitation was dead", mustUser(ctx, t, in.Driver, wes.ID), nil, false)

	// An Auth that sends no mail has no code to rest the acceptance on: the sign-in is
	// refused as one whose address is not the invited one, whatever the request says of
	// a proof, and nothing is written.
	silent := h.New(ctx, t, AuthWithAppTables, AuthConfig{Auth: true, Accounts: InviteOnly, Accept: rec.hook})
	una := noAddress("oid-una", "una@corp.example")
	unaLink := insertInvitation(ctx, t, silent.Driver, "una@north.example", "", []string{"Admin"}, tokenHash(10))
	for _, proven := range []bool{false, true} {
		req = &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Identity: una, Invitation: invitationOf(unaLink)}
		req.Invitation.AddressProven = proven
		assertRefused(t, signInRequest(ctx, silent, req), sessioninfo.RefusedInvitationAddress, signin.ErrInvitationAddress)
		assertInvitationStands(ctx, t, "the invitation on an Auth that sends no mail", silent, unaLink, req.Invitation)
		assertNoUser(ctx, t, silent.Driver, una.SignInName)
	}
}

// assertWaitsForInvitedCode fails unless err is the wait of an acceptance that rests on
// the code mailed to the invited address: held for the mailed code alone, naming no
// user.
func assertWaitsForInvitedCode(t *testing.T, what string, err error) {
	t.Helper()

	var wait *signin.PendingSignInError
	if !errors.As(err, &wait) || wait.Reason != sessioninfo.PendingMFA || wait.UserID.Valid || wait.Username != "" {
		t.Errorf("%s: sign-in error = %v, want the sign-in held for the code, naming no user", what, err)

		return
	}
	if len(wait.Factors) != 1 || wait.Factors[0] != sessioninfo.MethodEmailCode {
		t.Errorf("%s: the wait accepts %v, want the mailed code alone", what, wait.Factors)
	}
}

// testInvitationUnverifiedAddress holds the acceptance of an invitation, through a
// directory that asserts an address and does not verify it, to the code mailed to the
// invited address. Microsoft Entra ID's email claim is such an address: whoever runs a
// directory writes it, so a person who holds an invitation's link and a directory of
// their own could claim the invited address. Were the claim taken at its word, that
// person would take the invitation with its roles and an account under the invited
// person's address, or spend it with an account of their own that holds another
// address, or beside the account an administrator made for the invited person. So the
// claim counts for nothing, as an address the directory did not assert: the sign-in
// waits for the code, naming no user and writing nothing, whatever address it claims.
// The person who reads the invited mailbox enters the code and accepts.
func testInvitationUnverifiedAddress(ctx context.Context, t *testing.T, h *AuthHarness) {
	rec := newAcceptances()
	in := h.New(ctx, t, AuthWithAppTables, AuthConfig{Auth: true, Accounts: InviteOnly, ConfirmAddress: true, Accept: rec.hook})
	// claims is a person a directory presents under signInName and claims address for.
	claims := func(subject, signInName, address string) *sessioninfo.Identity {
		identity := entra(subject, signInName)
		identity.Email = address

		return identity
	}

	// A person no account was made for, whose directory claims the invited address, is
	// held with nothing written; so is one whose directory claims another address.
	const invited = "nell@north.example"
	link := insertInvitation(ctx, t, in.Driver, invited, "lakeside", []string{"Teacher"}, tokenHash(1))
	for _, claimed := range []string{invited, "mallory@rogue.example"} {
		mallory := claims("oid-mallory", "mallory@rogue.example", claimed)
		req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Identity: mallory, Invitation: invitationOf(link)}
		assertWaitsForInvitedCode(t, "a new person whose directory claims "+claimed, signInRequest(ctx, in, req))
		assertInvitationKept(ctx, t, in, rec, link, req.Invitation)
		assertNoUser(ctx, t, in.Driver, mallory.SignInName)
		assertNoLink(ctx, t, in.Driver, mallory)
	}

	// The person who reads the invited mailbox enters the code, and the account is made
	// with the invited address confirmed.
	nell := claims("oid-nell", "nell@corp.example", invited)
	req := withInvitedCode(nell, link)
	err := signInRequest(ctx, in, req)
	assertProvisioned(ctx, t, in.Driver, err, req, nell, "tenant-1")
	if !req.Invitation.Accepted {
		t.Error("Invitation.Accepted = false after the sign-in that carried the code, want accepted")
	}
	made := mustUser(ctx, t, in.Driver, req.UserID)
	assertEmail(t, "the account the acceptance made", made, strPtr(invited), true)
	assertAccepted(t, rec, "accepted:"+invited, made.ID.String()+" "+nell.SignInName+" lakeside Teacher")

	// An account the identity is already linked to, which holds another confirmed
	// address, is not the one invited because its directory now claims the invited
	// address.
	const second = "omar@north.example"
	ruth := userHolding(ctx, t, in.Driver, "ruth", "ruth@south.example", true)
	ruthIdentity := claims("oid-ruth", "ruth@rogue.example", second)
	mustLink(ctx, t, in.Driver, ruth.ID, ruthIdentity)
	secondLink := insertInvitation(ctx, t, in.Driver, second, "", []string{"Admin"}, tokenHash(2))
	req = &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Identity: ruthIdentity, Invitation: invitationOf(secondLink)}
	assertRefused(t, signInRequest(ctx, in, req), sessioninfo.RefusedInvitationAddress, signin.ErrInvitationAddress)
	assertInvitationKept(ctx, t, in, rec, secondLink, req.Invitation)
	assertEmail(t, "the linked account that holds another address", mustUser(ctx, t, in.Driver, ruth.ID), strPtr("ruth@south.example"), true)

	// A linked account that has confirmed no address waits for the code too: the claim
	// does not write the invited address on it.
	sam := createUser(ctx, t, in.Driver, "sam@rogue.example")
	samIdentity := claims("oid-sam", "sam@rogue.example", second)
	mustLink(ctx, t, in.Driver, sam.ID, samIdentity)
	req = &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Identity: samIdentity, Invitation: invitationOf(secondLink)}
	assertWaitsForInvitedCode(t, "a linked account with no confirmed address whose directory claims the invited one", signInRequest(ctx, in, req))
	assertInvitationKept(ctx, t, in, rec, secondLink, req.Invitation)
	assertEmail(t, "the linked account before the code", mustUser(ctx, t, in.Driver, sam.ID), nil, false)

	// The invitation mailed to an account an administrator made is that account's own:
	// a stranger whose directory claims its address is given no account beside it, and
	// does not spend the invitation.
	const umaAddress = "uma@north.example"
	uma := userHolding(ctx, t, in.Driver, "uma", umaAddress, false)
	umaLink := insertInvitation(ctx, t, in.Driver, umaAddress, "lakeside", []string{"Coach"}, tokenHash(3))
	stranger := claims("oid-stranger", "stranger@rogue.example", umaAddress)
	req = &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Identity: stranger, Invitation: invitationOf(umaLink)}
	assertWaitsForInvitedCode(t, "a stranger whose directory claims the address of an account an administrator made", signInRequest(ctx, in, req))
	assertInvitationKept(ctx, t, in, rec, umaLink, req.Invitation)
	assertNoUser(ctx, t, in.Driver, stranger.SignInName)
	assertNoLink(ctx, t, in.Driver, stranger)
	links, err := in.Driver.IdentitiesByUser(ctx, uma.ID)
	if err != nil {
		t.Fatalf("IdentitiesByUser() error = %v", err)
	}
	if len(links) != 0 {
		t.Errorf("the account an administrator made holds %d links after a sign-in that only claimed its address, want none", len(links))
	}
}
