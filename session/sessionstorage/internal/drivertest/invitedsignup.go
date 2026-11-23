package drivertest

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/securehash"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// A sign-up with a password through an invitation makes nobody until the code mailed to
// the invited address is entered. The sign-in that carries the code carries the account
// to make (the rules' NewAccount): its row is written in the transaction that consumes
// the invitation and runs the acceptor, so the account exists only with the acceptance.

// invitedSignUpPassword is the password every sign-up of these tests chooses.
const invitedSignUpPassword = "correct horse battery staple"

// invitedSignUp is the sign-in that completes a sign-up with a password through the
// invitation link as username, once the code mailed to the invited address was entered:
// a password identity with the invited address, naming the account by the id it will
// have, the invitation proven by the code, the row the sign-up makes under that id with
// the invited address confirmed and the invitation's name, and the proof of the
// password the sign-up chose.
func invitedSignUp(t *testing.T, link *dbtype.MailLink, username string) (*sessioninfo.NewSessionRequest, *dbtype.InsertSessionUser, dbtype.PasswordProof) {
	t.Helper()

	hash, err := securehash.New(securehash.Argon2()).Hash(invitedSignUpPassword)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := dbtype.ProofOf(hash)
	if err != nil {
		t.Fatal(err)
	}
	id := impulse.Must(impulse.NewUUID())
	account := &dbtype.InsertSessionUser{
		ID: impulse.NullUUIDFromUUID(id), Username: username, PasswordHash: hash, Email: strPtr(link.Email), EmailVerified: true, Name: strPtr("Pat Lee"),
	}
	req := &sessioninfo.NewSessionRequest{
		Reason: sessioninfo.ReasonStepUp, Username: username, UserID: id,
		Identity:   &sessioninfo.Identity{Method: sessioninfo.MethodPassword, Subject: id.String(), Email: link.Email, EmailVerified: true},
		AuthEvents: []sessioninfo.AuthEvent{{Method: sessioninfo.MethodPassword}, {Method: sessioninfo.MethodEmailCode}},
		Invitation: invitationOf(link),
	}
	req.Invitation.AddressProven = true

	return req, account, proof
}

// signUpInvited runs the sign-in that completes the sign-up req and account describe.
func signUpInvited(ctx context.Context, in *AuthInstance, req *sessioninfo.NewSessionRequest, account *dbtype.InsertSessionUser, proof dbtype.PasswordProof) (impulse.UUID, error) {
	id, err := in.SignUpInvited(ctx, newInsertSession(req.Username), req, account, proof)
	if err != nil {
		return impulse.NilUUID, errors.Wrap(err, "AuthInstance.SignUpInvited()")
	}

	return id, nil
}

// countedAcceptances is acceptances that also counts the runs of the committed
// function it asks for.
type countedAcceptances struct {
	*acceptances
	mu        sync.Mutex
	committed int
}

func (c *countedAcceptances) hook(ctx context.Context, tx HookTx, userID impulse.UUID, username string, inv *sessioninfo.Invitation) (func(ctx context.Context), error) {
	committed, err := c.acceptances.hook(ctx, tx, userID, username, inv)
	if err != nil {
		return nil, err
	}

	return func(ctx context.Context) {
		committed(ctx)
		c.mu.Lock()
		defer c.mu.Unlock()
		c.committed++
	}, nil
}

// committedRuns answers how often the committed function ran.
func (c *countedAcceptances) committedRuns() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.committed
}

// assertNoAccountMade fails unless no account stands under the account's id or
// username, nor under the invited address.
func assertNoAccountMade(ctx context.Context, t *testing.T, what string, d AuthDriver, account *dbtype.InsertSessionUser) {
	t.Helper()

	if user, err := d.User(ctx, account.ID.UUID); !httpio.HasNotFound(err) {
		t.Errorf("%s: User() of the sign-up's id = %+v, %v; want NotFound: no account may be made", what, user, err)
	}
	if user, err := d.UserByUserName(ctx, account.Username); !httpio.HasNotFound(err) {
		t.Errorf("%s: UserByUserName(%q) = %+v, %v; want NotFound: no account may be made", what, account.Username, user, err)
	}
	if user, err := d.UserByEmail(ctx, *account.Email); !httpio.HasNotFound(err) {
		t.Errorf("%s: UserByEmail(%q) = %+v, %v; want NotFound: no account may be made", what, *account.Email, user, err)
	}
}

// assertNothingCommitted fails unless the invitation link stands, the sign-in did not
// accept it, and nothing the acceptor gives was committed or told.
func assertNothingCommitted(ctx context.Context, t *testing.T, what string, h *AuthHarness, in *AuthInstance, rec *acceptances, link *dbtype.MailLink, inv *sessioninfo.Invitation) {
	t.Helper()

	if _, err := in.Driver.MailLinkByToken(ctx, link.TokenHash, time.Now()); err != nil {
		t.Errorf("%s: MailLinkByToken() error = %v, want the invitation still live", what, err)
	}
	if inv.Accepted {
		t.Errorf("%s: Invitation.Accepted = true, want false", what)
	}
	if value, found := h.Recorded(ctx, t, in.Raw, "accepted:"+link.Email); found {
		t.Errorf("%s: HookRecords[accepted:%s] = %q, want nothing the acceptor gives committed", what, link.Email, value)
	}
	if value, found := rec.recorded("accepted:" + link.Email); found {
		t.Errorf("%s: the acceptor's committed function ran with %q, want it never run", what, value)
	}
}

// assertConflictSays fails unless err is a Conflict that says want.
func assertConflictSays(t *testing.T, what string, err error, want string) {
	t.Helper()

	if !httpio.HasConflict(err) {
		t.Fatalf("%s: error = %v, want a Conflict", what, err)
	}
	if got := httpio.Message(err); got != want {
		t.Errorf("%s: the Conflict says %q, want %q", what, got, want)
	}
}

func testInvitedSignUp(ctx context.Context, t *testing.T, h *AuthHarness) {
	rec := &countedAcceptances{acceptances: newAcceptances()}
	in := h.New(ctx, t, AuthWithAppTables, AuthConfig{Auth: true, Accept: rec.hook})

	// A live invitation, the address and the username free: the account is made under
	// the id the sign-in names, with the password the sign-up chose, the invited address
	// confirmed and the invitation's name; the invitation is consumed and accepted, the
	// acceptor given the account by id and username, and the session inserted for it.
	link := insertInvitation(ctx, t, in.Driver, "pat@lakeside.edu", "lakeside", []string{"Teacher"}, tokenHash(1))
	req, account, proof := invitedSignUp(t, link, "patl")
	sessionID, err := signUpInvited(ctx, in, req, account, proof)
	if err != nil {
		t.Fatalf("the sign-in that completes the sign-up error = %v", err)
	}
	pat := mustUser(ctx, t, in.Driver, account.ID.UUID)
	if pat.Username != "patl" || pat.Name == nil || *pat.Name != "Pat Lee" {
		t.Errorf("the account made = %q named %v, want patl named Pat Lee", pat.Username, pat.Name)
	}
	if _, err := securehash.New(securehash.Argon2()).Compare(pat.PasswordHash, invitedSignUpPassword); err != nil {
		t.Errorf("the account made holds a password that is not the one the sign-up chose: %v", err)
	}
	assertEmail(t, "the account made", pat, strPtr("pat@lakeside.edu"), true)
	if !req.Invitation.Accepted {
		t.Error("Invitation.Accepted = false after the sign-up, want accepted")
	}
	if consumedAt := h.MailLinkConsumedAt(ctx, t, in.Raw, link.TokenHash); consumedAt == nil {
		t.Error("the invitation's ConsumedAt = nil after the sign-up, want consumed")
	}
	want := pat.ID.String() + " patl lakeside Teacher"
	assertAccepted(t, rec.acceptances, "accepted:pat@lakeside.edu", want)
	assertRecorded(ctx, t, h, in.Raw, "accepted:pat@lakeside.edu", want)
	if runs := rec.committedRuns(); runs != 1 {
		t.Errorf("the acceptor's committed function ran %d time(s), want once", runs)
	}
	if session := mustSession(ctx, t, in.Driver, sessionID); !session.UserID.Valid || session.UserID.UUID != pat.ID {
		t.Errorf("the session's user = %v, want the account made %s", session.UserID, pat.ID)
	}
	if req.UserID != pat.ID || req.Username != "patl" || req.User == nil || req.User.Source != sessioninfo.UserNamed {
		t.Errorf("req = user %v %q (%+v), want the account made, named by the sign-in", req.UserID, req.Username, req.User)
	}

	// An invitation used, withdrawn or run out by the time the code is entered: the
	// sign-in is refused invitation_expired, and nothing is made or given.
	deadLink := insertInvitation(ctx, t, in.Driver, "dana@lakeside.edu", "lakeside", []string{"Teacher"}, tokenHash(2))
	if err := in.Driver.ConsumeMailLink(ctx, deadLink.TokenHash, time.Now()); err != nil {
		t.Fatalf("ConsumeMailLink() error = %v", err)
	}
	req, account, proof = invitedSignUp(t, deadLink, "dana")
	_, err = signUpInvited(ctx, in, req, account, proof)
	assertRefused(t, err, sessioninfo.RefusedInvitationExpired, signin.ErrInvitationExpired)
	assertNoAccountMade(ctx, t, "a dead invitation", in.Driver, account)
	if req.Invitation.Accepted {
		t.Error("Invitation.Accepted = true for a dead invitation, want false")
	}
	if _, found := rec.recorded("accepted:dana@lakeside.edu"); found {
		t.Error("the acceptor's committed function ran for a dead invitation, want nothing given")
	}

	invitedSignUpTaken(ctx, t, h, in, rec)
}

// invitedSignUpTaken is testInvitedSignUp's cases of an address or a username another
// account holds by the time the code is entered.
func invitedSignUpTaken(ctx context.Context, t *testing.T, h *AuthHarness, in *AuthInstance, rec *countedAcceptances) {
	t.Helper()

	// Another account holds the invited address, confirmed or not: a Conflict in the
	// words the sign-up is answered with before its code, and nothing is made or given.
	for _, confirmed := range []bool{true, false} {
		email := map[bool]string{true: "sam@lakeside.edu", false: "lee@lakeside.edu"}[confirmed]
		what := map[bool]string{true: "an address held confirmed", false: "an address held unconfirmed"}[confirmed]
		holder := userHolding(ctx, t, in.Driver, "holder-"+email, email, confirmed)
		heldLink := insertInvitation(ctx, t, in.Driver, email, "lakeside", []string{"Teacher"}, tokenHash(map[bool]byte{true: 3, false: 4}[confirmed]))
		req, account, proof := invitedSignUp(t, heldLink, "new-"+email)
		_, err := signUpInvited(ctx, in, req, account, proof)
		assertConflictSays(t, what, err, "the email address \""+email+"\" is already in use")
		if user, err := in.Driver.User(ctx, account.ID.UUID); !httpio.HasNotFound(err) {
			t.Errorf("%s: User() of the sign-up's id = %+v, %v; want NotFound", what, user, err)
		}
		assertNoUser(ctx, t, in.Driver, account.Username)
		if user, err := in.Driver.UserByEmail(ctx, email); err != nil || user.ID != holder.ID {
			t.Errorf("%s: UserByEmail(%q) = %+v, %v; want the account that held it", what, email, user, err)
		}
		assertNothingCommitted(ctx, t, what, h, in, rec.acceptances, heldLink, req.Invitation)
	}

	// Another account holds the username: a Conflict in the same words, and no second
	// account under it.
	kim := createUser(ctx, t, in.Driver, "kim")
	kimLink := insertInvitation(ctx, t, in.Driver, "kim@lakeside.edu", "lakeside", []string{"Teacher"}, tokenHash(5))
	req, account, proof := invitedSignUp(t, kimLink, "kim")
	_, err := signUpInvited(ctx, in, req, account, proof)
	assertConflictSays(t, "a username taken", err, `username "kim" already exists`)
	if user, err := in.Driver.UserByUserName(ctx, "kim"); err != nil || user.ID != kim.ID {
		t.Errorf("UserByUserName(kim) = %+v, %v; want the account that held the username", user, err)
	}
	if user, err := in.Driver.User(ctx, account.ID.UUID); !httpio.HasNotFound(err) {
		t.Errorf("a username taken: User() of the sign-up's id = %+v, %v; want NotFound", user, err)
	}
	if user, err := in.Driver.UserByEmail(ctx, "kim@lakeside.edu"); !httpio.HasNotFound(err) {
		t.Errorf("a username taken: UserByEmail(kim@lakeside.edu) = %+v, %v; want NotFound", user, err)
	}
	assertNothingCommitted(ctx, t, "a username taken", h, in, rec.acceptances, kimLink, req.Invitation)
}

func testInvitedSignUpHookFails(ctx context.Context, t *testing.T, h *AuthHarness) {
	// The acceptor's error is a NotFound, as a role that no longer exists is: it must
	// not be taken for the invitation's own NotFound, a link that died.
	failure := httpio.NewNotFoundMessage("the role the invitation names no longer exists")
	acceptor := &failingAcceptor{fail: failure}
	in := h.New(ctx, t, AuthWithAppTables, AuthConfig{Auth: true, Accept: acceptor.hook})

	// The acceptor fails the transaction that would make the account: the sign-in ends
	// with its error, no account is made, and the invitation stands.
	link := insertInvitation(ctx, t, in.Driver, "pat@lakeside.edu", "lakeside", []string{"Teacher"}, tokenHash(1))
	req, account, proof := invitedSignUp(t, link, "patl")
	_, err := signUpInvited(ctx, in, req, account, proof)
	assertNothingGiven(ctx, t, h, in, acceptor, err, failure, link, req.Invitation)
	assertNoAccountMade(ctx, t, "an acceptor that fails", in.Driver, account)
}

func testInvitedSignUpMisused(ctx context.Context, t *testing.T, h *AuthHarness) {
	rec := newAcceptances()
	in := h.New(ctx, t, AuthWithAppTables, AuthConfig{Auth: true, Accounts: InviteOnly, Accept: rec.hook})

	// An account to make goes only with the sign-in that entered the code mailed for the
	// invitation it carries, names the account by the request's id, holds the invited
	// address confirmed, and signs in by its password: anything else is an error, and
	// nothing is made, consumed or given.
	tests := []struct {
		name   string
		change func(req *sessioninfo.NewSessionRequest, account *dbtype.InsertSessionUser)
	}{
		{name: "no invitation on the request", change: func(req *sessioninfo.NewSessionRequest, _ *dbtype.InsertSessionUser) {
			req.Invitation = nil
		}},
		{name: "an invitation whose code was not entered", change: func(req *sessioninfo.NewSessionRequest, _ *dbtype.InsertSessionUser) {
			req.Invitation.AddressProven = false
		}},
		{name: "an account under another id than the request's", change: func(_ *sessioninfo.NewSessionRequest, account *dbtype.InsertSessionUser) {
			account.ID = impulse.NullUUIDFromUUID(impulse.Must(impulse.NewUUID()))
		}},
		{name: "an account with no id", change: func(_ *sessioninfo.NewSessionRequest, account *dbtype.InsertSessionUser) {
			account.ID = impulse.NullUUID{}
		}},
		{name: "an account holding another address", change: func(_ *sessioninfo.NewSessionRequest, account *dbtype.InsertSessionUser) {
			account.Email = strPtr("mallory@elsewhere.test")
		}},
		{name: "an account holding the invited address unconfirmed", change: func(_ *sessioninfo.NewSessionRequest, account *dbtype.InsertSessionUser) {
			account.EmailVerified = false
		}},
		{name: "an external identity", change: func(req *sessioninfo.NewSessionRequest, _ *dbtype.InsertSessionUser) {
			req.Identity = entra("oid-pat", req.Identity.Email)
			req.Identity.EmailVerified = true
		}},
	}
	for i, tt := range tests {
		// Each case invites its own address and chooses its own username, so that what
		// one case wrongly makes is not what the next one finds.
		address, username := fmt.Sprintf("pat%d@lakeside.edu", i), fmt.Sprintf("patl%d", i)
		link := insertInvitation(ctx, t, in.Driver, address, "lakeside", []string{"Teacher"}, tokenHash(byte(i+1)))
		req, account, proof := invitedSignUp(t, link, username)
		inv := req.Invitation
		tt.change(req, account)
		_, err := signUpInvited(ctx, in, req, account, proof)
		var refusal *sessioninfo.LoginRefusal
		if err == nil || errors.As(err, &refusal) {
			t.Errorf("%s: error = %v, want an error that is no refusal", tt.name, err)
		}
		assertNoUser(ctx, t, in.Driver, username)
		assertNoUser(ctx, t, in.Driver, address)
		if user, err := in.Driver.UserByEmail(ctx, address); !httpio.HasNotFound(err) {
			t.Errorf("%s: UserByEmail() = %+v, %v; want NotFound", tt.name, user, err)
		}
		assertInvitationKept(ctx, t, in, rec, link, inv)
	}
}
