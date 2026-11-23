package session

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/securehash"
	internalcookie "github.com/impulseframework/impulse/session/internal/cookie"
	"github.com/impulseframework/impulse/session/internal/signinflow"
	"github.com/impulseframework/impulse/session/mail"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/session/signin"
	gomock "go.uber.org/mock/gomock"
)

// An invitation's link only opens the invitation: whoever holds it may have been
// forwarded it. These tests hold the sign-up with a password through an invitation to
// that. The sign-up makes nobody and accepts nothing; it waits for the code mailed to
// the invited address, and the account is made, holding that address verified, once the
// code is entered.

// factorCodes is the factor codes table as the mocked store holds it: one code stands
// for a pending sign-in, the one last inserted.
type factorCodes struct {
	mu   sync.Mutex
	rows []*sessionstorage.FactorCode
}

// expectFactorCodes gives the fixture's store a factor codes table and returns it.
func expectFactorCodes(f *authFixture) *factorCodes {
	codes := &factorCodes{}
	f.store.EXPECT().InsertFactorCode(gomock.Any(), gomock.Any()).DoAndReturn(codes.insert).AnyTimes()
	f.store.EXPECT().FactorCode(gomock.Any(), gomock.Any()).DoAndReturn(codes.standing).AnyTimes()
	f.store.EXPECT().RecordFactorCodeAttempt(gomock.Any(), gomock.Any()).DoAndReturn(codes.attempt).AnyTimes()
	f.store.EXPECT().ConsumeFactorCode(gomock.Any(), gomock.Any()).DoAndReturn(codes.consume).AnyTimes()

	return codes
}

func (c *factorCodes) insert(_ context.Context, code *sessionstorage.FactorCode) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for _, row := range c.rows {
		if row.PendingID == code.PendingID && row.ConsumedAt == nil {
			row.ConsumedAt = &now
		}
	}
	code.ID = impulse.Must(impulse.NewUUID())
	row := *code
	c.rows = append(c.rows, &row)

	return nil
}

func (c *factorCodes) standing(_ context.Context, pendingID impulse.UUID) (*sessionstorage.FactorCode, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, row := range c.rows {
		if row.PendingID == pendingID && row.ConsumedAt == nil && time.Now().Before(row.ExpiresAt) {
			found := *row

			return &found, nil
		}
	}

	return nil, httpio.NewNotFoundMessage("no live factor code")
}

func (c *factorCodes) attempt(_ context.Context, id impulse.UUID) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, row := range c.rows {
		if row.ID == id && row.ConsumedAt == nil {
			row.Attempts++

			return row.Attempts, nil
		}
	}

	return 0, httpio.NewNotFoundMessage("no live factor code")
}

func (c *factorCodes) consume(_ context.Context, id impulse.UUID) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, row := range c.rows {
		if row.ID == id && row.ConsumedAt == nil {
			now := time.Now()
			row.ConsumedAt = &now

			return nil
		}
	}

	return httpio.NewNotFoundMessage("no live factor code")
}

// sentTo are the addresses the table's codes were recorded for, oldest first.
func (c *factorCodes) sentTo() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var to []string
	for _, row := range c.rows {
		to = append(to, row.Email)
	}

	return to
}

// The subjects of the two mails a code of the second step comes in: the code an account
// signs in with, and the code that accepts an invitation.
var (
	signInCodeSubject     = regexp.MustCompile(`^Your Harbor sign-in code is (\d{3}) (\d{3})$`)
	invitationCodeSubject = regexp.MustCompile(`^Your code to accept your invitation to Harbor is (\d{3}) (\d{3})$`)
)

// invitationCodeIn is the code the mail of an invitation's code carries, as a person
// would type it.
func invitationCodeIn(t *testing.T, msg mail.Message) string {
	t.Helper()

	m := invitationCodeSubject.FindStringSubmatch(msg.Subject)
	if m == nil {
		t.Fatalf("the message's subject = %q, want the one of a code that accepts an invitation", msg.Subject)
	}

	return m[1] + m[2]
}

// invitedPat is the fixture of one invitation mailed to pat@lakeside.edu and opened by
// a browser, with the cookies that browser holds.
func invitedPat(t *testing.T, passwordOptions ...PasswordOption) (f *authFixture, recorder *mail.Recorder, links *mailLinks, cookies []*http.Cookie) {
	t.Helper()

	f, recorder, links = newMailFixture(t, gomock.NewController(t), passwordOptions...)
	links.expectAccounts(f)
	if _, err := f.auth.API().Invite(context.Background(), &InvitationRequest{Email: "pat@lakeside.edu", TenantID: "lakeside"}); err != nil {
		t.Fatal(err)
	}

	return f, recorder, links, invitationCookies(t, f, links, tokenIn(t, recorder.Messages()[0].Text, "invitation"))
}

// signUpInvited posts the sign-up with a password through the invitation cookies carry,
// which the store is expected to hold as a wait that names nobody, and returns the
// wait's ID with the cookies the browser holds after it: the ones it had and the
// pending one. username is the name the store is asked about, free.
func signUpInvited(t *testing.T, f *authFixture, cookies []*http.Cookie, body map[string]string, username string) (pendingID impulse.UUID, after []*http.Cookie) {
	t.Helper()

	f.store.EXPECT().UserByUserName(gomock.Any(), username).Return(nil, httpio.NewNotFoundMessage("no such user"))
	pendingID = f.expectHold("")
	rr := f.serve(f.auth.Invitation().SignUp(), http.MethodPost, "/invitation/sign-up", body, cookies)
	if rr.Code != http.StatusOK || decoded(t, rr)["mfaIsRequired"] != true {
		t.Fatalf("SignUp() = %d %s, want 200 with mfaIsRequired: the sign-up waits for the code", rr.Code, rr.Body.String())
	}
	pending := cookieNamed(rr, defaultPendingCookieName)
	if pending == nil || pending.Value == "" {
		t.Fatal("SignUp() set no pending cookie, want the wait carried by the browser")
	}
	if cookieNamed(rr, internalcookie.AuthCookieName) != nil {
		t.Error("SignUp() set a session cookie, want nobody signed in before the code")
	}
	if c := cookieNamed(rr, defaultInvitationCookieName); c != nil && c.Value == "" {
		t.Error("SignUp() deleted the invitation cookie, want it kept: nothing is accepted before the code")
	}

	return pendingID, append(slices.Clone(cookies), pending)
}

// enterCode posts code to the pending wait the cookies carry.
func (f *authFixture) enterCode(cookies []*http.Cookie, code string) *httptest.ResponseRecorder {
	return f.serve(f.auth.Pending().VerifyCode(), http.MethodPost, "/pending/verify-code", map[string]any{"code": code}, cookies)
}

func TestAuth_InvitedSignUp(t *testing.T) {
	t.Parallel()

	t.Run("the sign-up makes nobody and accepts nothing: it is held as a wait that names nobody, for the code that goes to the invited address", invitedSignUpWaits)
	t.Run("the code is mailed to the invited address under the invitation's own count, and entering it makes the account, the address verified, and signs it in with the invitation proven", invitedSignUpByCode)
	t.Run("where the username is the address, the account the code makes takes the invitation's address as its username", invitedSignUpUsernameIsEmail)
	t.Run("an invitation that ends as the code is entered is refused invitation_expired, with no session and no account", invitedSignUpEndedAtTheCode)
	t.Run("where the username is the address, the password is judged against the address as its username, as the page asks about it", invitedSignUpJudgedAsAsked)
	t.Run("a wrong code makes nobody and leaves the wait", invitedSignUpWrongCode)
	t.Run("an address or a username another account holds is 409 in the store's words before anything is held or mailed", invitedSignUpTaken)
	t.Run("a chosen username with an @ in it has to be the invited address, refused before anything is held or mailed", invitedSignUpAddressAsUsername)
	t.Run("the application's own step cannot complete a sign-up: 409, the wait left as it was", invitedSignUpNotTheApplications)
	t.Run("the account is made only from what the sign-up kept, for the invitation it was started for, by a code mailed to the invited address", invitedSignUpAttemptRefusals)
}

func invitedSignUpWaits(t *testing.T) {
	t.Parallel()
	f, recorder, links, cookies := invitedPat(t)
	expectFactorCodes(f)

	// The mocked store expects no CreateUser and no SignIn: either call fails the test.
	pendingID, cookies := signUpInvited(t, f, cookies, map[string]string{"password": "correct horse battery staple", "username": "patl"}, "patl")

	held := f.held[pendingID]
	if held.UserID.Valid || held.Username != "" || held.Identity.Subject != "" || held.PasswordProof != nil {
		t.Errorf("the wait held names the user %v (%q), the subject %q and a password proof %t; want nobody: no account exists before the code",
			held.UserID, held.Username, held.Identity.Subject, held.PasswordProof != nil)
	}
	if held.Identity.Method != sessioninfo.MethodPassword || held.Identity.Email != "pat@lakeside.edu" || held.Identity.EmailVerified {
		t.Errorf("the wait's identity = %+v, want a password identity with the invited address, not verified", held.Identity)
	}
	if diff := cmp.Diff([]sessioninfo.AuthMethod{sessioninfo.MethodEmailCode}, held.Factors); diff != "" || held.Step != sessioninfo.PendingMFA {
		t.Errorf("the wait is for %q and accepts (-want +got):\n%s", held.Step, diff)
	}
	if got := len(recorder.Messages()); got != 1 {
		t.Errorf("%d messages after the sign-up, want the invitation alone: the page asks for the code", got)
	}
	if row := links.rows[0]; row.ConsumedAt != nil {
		t.Error("the invitation was consumed by the sign-up, want it standing until the code")
	}

	status := decoded(t, f.serve(f.auth.Pending().Status(), http.MethodGet, "/pending", nil, cookies))
	code, _ := status["emailCode"].(map[string]any)
	if code["codeEmail"] != "p•••@lakeside.edu" || code["enrollEmail"] == true || code["codeSent"] == true {
		t.Errorf("Status() = %v, want the code going to the invited address, masked, none standing and no address asked for", status)
	}

	// Nobody types the address the invitation's code goes to.
	rr := f.serve(f.auth.Pending().EnrollEmail(), http.MethodPost, "/pending/enroll-email", map[string]string{"email": "mallory@elsewhere.test"}, cookies)
	if rr.Code != http.StatusConflict {
		t.Errorf("EnrollEmail() for a sign-up through an invitation = %d %s, want 409: the code goes to the invited address", rr.Code, rr.Body.String())
	}
	if got := len(recorder.Messages()); got != 1 {
		t.Errorf("%d messages after an address was typed, want none mailed to it", got-1)
	}
}

func invitedSignUpByCode(t *testing.T) {
	t.Parallel()
	_, sessionID := newIDs()
	f, recorder, links, cookies := invitedPat(t)
	codes := expectFactorCodes(f)
	pendingID, cookies := signUpInvited(t, f, cookies, map[string]string{"password": "correct horse battery staple", "username": "patl"}, "patl")
	inv := invitationOf(links.rows[0])

	if rr := f.serve(f.auth.Pending().SendCode(), http.MethodPost, "/pending/send-code", nil, cookies); rr.Code != http.StatusOK || decoded(t, rr)["codeEmail"] != "p•••@lakeside.edu" {
		t.Fatalf("SendCode() = %d %s, want the code sent to the invited address", rr.Code, rr.Body.String())
	}
	msgs := recorder.Messages()
	if len(msgs) != 2 || msgs[1].To != "pat@lakeside.edu" {
		t.Fatalf("the messages after SendCode() = %d, the last to %q; want one code mailed to the invited address", len(msgs), msgs[len(msgs)-1].To)
	}
	if diff := cmp.Diff([]string{"pat@lakeside.edu"}, codes.sentTo()); diff != "" {
		t.Errorf("the codes recorded went to (-want +got):\n%s", diff)
	}
	if record := f.attemptsRecord(invitationCodeLimitKey(inv)); record == nil || record.Failures != 1 {
		t.Errorf("the invitation's count of codes = %+v, want one", record)
	}
	if own, other := f.attemptsRecord(codeLimitKey("pat@lakeside.edu")), f.attemptsRecord(mailLimitKey("pat@lakeside.edu")); own != nil || other != nil {
		t.Errorf("the address's own sign-in codes = %+v and its other mail = %+v, want neither counted: the holder of a link uses up nothing of the invited person's", own, other)
	}

	// The inviter's name for the person, which the account carries.
	links.mu.Lock()
	links.rows[0].Name = "Pat Lee"
	links.mu.Unlock()

	// The mocked store expects no CreateUser: the account goes to the store with the
	// sign-in, which makes it with the invitation's acceptance.
	f.expectConsume(pendingID)
	f.expectSignIn(sessionID, func(req *sessioninfo.NewSessionRequest) {
		if req.Invitation == nil || req.Invitation.ID != inv.ID || !req.Invitation.AddressProven {
			t.Fatalf("SignIn() given the invitation %+v, want the opened one with its address proven by the code", req.Invitation)
		}
		if req.UserID.IsNil() || req.Identity.Method != sessioninfo.MethodPassword || req.Identity.Subject != req.UserID.String() ||
			req.Identity.Email != "pat@lakeside.edu" || !req.Identity.EmailVerified {
			t.Errorf("SignIn() given the user %s and the identity %+v, want the new account's password identity, its id the subject, with the invited address verified", req.UserID, req.Identity)
		}
		want := []sessioninfo.AuthEvent{{Method: sessioninfo.MethodPassword}, {Method: sessioninfo.MethodEmailCode}}
		if diff := cmp.Diff(want, req.AuthEvents, eventOpts); diff != "" {
			t.Errorf("SignIn() given the events (-want +got):\n%s", diff)
		}
		req.Invitation.Accepted = true
	}, nil)

	rr := f.enterCode(cookies, invitationCodeIn(t, msgs[1]))

	if rr.Code != http.StatusOK || decoded(t, rr)["mfaIsRequired"] != false || cookieNamed(rr, internalcookie.AuthCookieName) == nil {
		t.Errorf("VerifyCode() = %d %s, want a session", rr.Code, rr.Body.String())
	}
	signedIn := f.signedIn
	if signedIn == nil {
		t.Fatal("no SignIn() reached the store, want the sign-in that carries the account")
	}
	account := signedIn.newAccount
	if account == nil {
		t.Fatal("SignIn() given no account to make, want the sign-up's")
	}
	if !account.ID.Valid || account.ID.UUID != signedIn.req.UserID || account.Username != "patl" || account.PasswordHash == nil ||
		account.Email == nil || *account.Email != "pat@lakeside.edu" || !account.EmailVerified || account.Name == nil || *account.Name != "Pat Lee" {
		t.Errorf("SignIn() given the account %+v, want the chosen username and a hash under the sign-in's user id, with the invited address verified and the invitation's name", account)
	}
	if _, err := securehash.New(securehash.Argon2()).Compare(account.PasswordHash, "correct horse battery staple"); err != nil {
		t.Errorf("SignIn() given an account whose hash is not the chosen password's: %v", err)
	}
	if signedIn.proven == nil {
		t.Error("SignIn() given no password proof, want the sign-in to rest on the chosen password")
	}
	if c := cookieNamed(rr, defaultInvitationCookieName); c == nil || c.Value != "" {
		t.Error("VerifyCode() left the invitation cookie, want it deleted")
	}
	if !pendingDeleted(rr) {
		t.Error("VerifyCode() left the pending cookie, want it deleted: it held the chosen password's hash")
	}
	if record := f.attemptsRecord(invitationCodeLimitKey(inv)); record != nil {
		t.Errorf("the invitation's count of codes after one was entered = %+v, want it reset", record)
	}
}

func invitedSignUpUsernameIsEmail(t *testing.T) {
	t.Parallel()
	_, sessionID := newIDs()
	f, _, _, cookies := invitedPat(t, UsernameIsEmail())
	pendingID, cookies := signUpInvited(t, f, cookies, map[string]string{"password": "correct horse battery staple"}, "pat@lakeside.edu")
	expectFactorCode(f, &pendingState{ID: pendingID})
	f.expectConsume(pendingID)
	f.expectSignIn(sessionID, nil, nil)

	if rr := f.enterCode(cookies, standingCode); rr.Code != http.StatusOK {
		t.Errorf("VerifyCode() = %d %s, want 200", rr.Code, rr.Body.String())
	}
	if f.signedIn == nil || f.signedIn.newAccount == nil {
		t.Fatal("SignIn() given no account to make, want the sign-up's")
	}
	if account := f.signedIn.newAccount; account.Username != "pat@lakeside.edu" || !account.EmailVerified {
		t.Errorf("SignIn() given the account %+v, want the address as the username, verified", account)
	}
}

func invitedSignUpEndedAtTheCode(t *testing.T) {
	t.Parallel()
	f, _, _, cookies := invitedPat(t)
	pendingID, cookies := signUpInvited(t, f, cookies, map[string]string{"password": "correct horse battery staple", "username": "patl"}, "patl")
	expectFactorCode(f, &pendingState{ID: pendingID})
	f.expectConsume(pendingID)

	// The invitation was withdrawn, used or ran out as the code was entered: the store
	// makes no account and refuses the sign-in. The mocked store expects no CreateUser.
	f.expectSignIn(impulse.NilUUID, nil, func(*sessioninfo.NewSessionRequest) error {
		return signinflow.Refusal("", sessioninfo.RefusedInvitationExpired, signin.ErrInvitationExpired, signinflow.InvitationEnded)
	})

	rr := f.enterCode(cookies, standingCode)

	if refusalCode(t, rr) != sessioninfo.RefusedInvitationExpired {
		t.Errorf("VerifyCode() as the invitation ended = %d %s, want the invitation_expired refusal", rr.Code, rr.Body.String())
	}
	if cookieNamed(rr, internalcookie.AuthCookieName) != nil {
		t.Error("VerifyCode() set a session cookie, want nobody signed in")
	}
	if f.signedIn == nil || f.signedIn.newAccount == nil {
		t.Error("SignIn() given no account to make, want the sign-up's, which the refusal leaves unmade")
	}
}

func invitedSignUpJudgedAsAsked(t *testing.T) {
	t.Parallel()
	const address, name, password = "qz@lakeside.edu", "Quentin Zabrowski", "Quentin#2077x"
	f, recorder, links := newMailFixture(t, gomock.NewController(t), UsernameIsEmail())
	links.expectAccounts(f)
	if _, err := f.auth.API().Invite(context.Background(), &InvitationRequest{Email: address, Name: name, TenantID: "lakeside"}); err != nil {
		t.Fatal(err)
	}
	cookies := invitationCookies(t, f, links, tokenIn(t, recorder.Messages()[0].Text, "invitation"))

	asked := map[string]string{"password": password, "username": address, "email": address, "name": name}
	if got := strengthAnswered(t, askStrength(f.auth, f.password, strengthBody(t, asked), "10.0.0.9", "")); got["strongEnough"] != true {
		t.Fatalf("the strength route asked as the page asks = %v, want strong enough: the case proves nothing", got)
	}
	signUpInvited(t, f, cookies, map[string]string{"password": password}, address)
}

func invitedSignUpWrongCode(t *testing.T) {
	t.Parallel()
	f, _, links, cookies := invitedPat(t)
	pendingID, cookies := signUpInvited(t, f, cookies, map[string]string{"password": "correct horse battery staple", "username": "patl"}, "patl")
	expectFactorCode(f, &pendingState{ID: pendingID})

	// The mocked store expects no ConsumePendingSignIn, no CreateUser and no SignIn: any
	// of them fails the test.
	rr := f.enterCode(cookies, "271828")

	if rr.Code != http.StatusUnauthorized || refusalCode(t, rr) != sessioninfo.RefusedInvalidCode {
		t.Errorf("VerifyCode() of a wrong code = %d %s, want 401 invalid_code", rr.Code, rr.Body.String())
	}
	if cookieNamed(rr, internalcookie.AuthCookieName) != nil {
		t.Error("VerifyCode() of a wrong code set a session cookie, want nobody signed in")
	}
	if row := links.rows[0]; row.ConsumedAt != nil || f.held[pendingID].ConsumedAt != nil {
		t.Error("a wrong code ended the invitation or the wait, want both standing")
	}
}

func invitedSignUpTaken(t *testing.T) {
	t.Parallel()

	t.Run("the invited address", func(t *testing.T) {
		t.Parallel()
		f, recorder, links, cookies := invitedPat(t)
		links.holds("pat@lakeside.edu", &sessionstorage.SessionUser{ID: impulse.Must(impulse.NewUUID()), Username: "pat", Email: strPtr("pat@lakeside.edu")})

		// The mocked store expects no HoldSignIn: a call fails the test.
		rr := f.serve(f.auth.Invitation().SignUp(), http.MethodPost, "/invitation/sign-up", map[string]string{"password": "correct horse battery staple", "username": "patl"}, cookies)

		if rr.Code != http.StatusConflict || decoded(t, rr)["message"] != `the email address "pat@lakeside.edu" is already in use` {
			t.Errorf("SignUp() for an address an account holds = %d %s, want 409 saying the address is in use", rr.Code, rr.Body.String())
		}
		if cookieNamed(rr, defaultPendingCookieName) != nil || len(recorder.Messages()) != 1 {
			t.Errorf("SignUp() set a pending cookie (%t) or mailed (%d messages), want neither", cookieNamed(rr, defaultPendingCookieName) != nil, len(recorder.Messages())-1)
		}
	})

	t.Run("the chosen username", func(t *testing.T) {
		t.Parallel()
		f, recorder, _, cookies := invitedPat(t)
		f.store.EXPECT().UserByUserName(gomock.Any(), "patl").Return(&sessionstorage.SessionUser{ID: impulse.Must(impulse.NewUUID()), Username: "patl"}, nil)

		rr := f.serve(f.auth.Invitation().SignUp(), http.MethodPost, "/invitation/sign-up", map[string]string{"password": "correct horse battery staple", "username": "patl"}, cookies)

		if rr.Code != http.StatusConflict || decoded(t, rr)["message"] != `username "patl" already exists` {
			t.Errorf("SignUp() with a username an account holds = %d %s, want 409 saying the username exists", rr.Code, rr.Body.String())
		}
		if cookieNamed(rr, defaultPendingCookieName) != nil || len(recorder.Messages()) != 1 {
			t.Errorf("SignUp() set a pending cookie (%t) or mailed (%d messages), want neither", cookieNamed(rr, defaultPendingCookieName) != nil, len(recorder.Messages())-1)
		}
	})
}

func invitedSignUpAddressAsUsername(t *testing.T) {
	t.Parallel()

	t.Run("another address is refused: nothing is held, mailed or looked up", func(t *testing.T) {
		t.Parallel()
		f, recorder, _, cookies := invitedPat(t)

		// The mocked store expects no UserByUserName and no HoldSignIn: a call fails the
		// test.
		rr := f.serve(f.auth.Invitation().SignUp(), http.MethodPost, "/invitation/sign-up", map[string]string{"password": "correct horse battery staple", "username": "sam@lakeside.edu"}, cookies)

		if rr.Code != http.StatusBadRequest || messageOf(t, rr) != ownAddressWords {
			t.Errorf("SignUp() with another address as the username = %d %s, want 400 %q", rr.Code, rr.Body.String(), ownAddressWords)
		}
		if cookieNamed(rr, defaultPendingCookieName) != nil || len(recorder.Messages()) != 1 {
			t.Errorf("SignUp() set a pending cookie (%t) or mailed (%d messages), want neither", cookieNamed(rr, defaultPendingCookieName) != nil, len(recorder.Messages())-1)
		}
	})

	t.Run("the invited address in another case is held for the code", func(t *testing.T) {
		t.Parallel()
		f, _, _, cookies := invitedPat(t)

		signUpInvited(t, f, cookies, map[string]string{"password": "correct horse battery staple", "username": "PAT@lakeside.edu"}, "PAT@lakeside.edu")
	})
}

func invitedSignUpNotTheApplications(t *testing.T) {
	t.Parallel()
	f, _, _, cookies := invitedPat(t)
	pendingID, cookies := signUpInvited(t, f, cookies, map[string]string{"password": "correct horse battery staple", "username": "patl"}, "patl")

	// The mocked store expects no ConsumePendingSignIn, no CreateUser and no SignIn.
	var err error
	f.serve(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err = f.auth.API().CompletePending(r.Context(), w, sessioninfo.AuthEvent{Method: sessioninfo.MethodEmailCode})
	}), http.MethodPost, "/mfa", nil, cookies)

	if !httpio.HasConflict(err) {
		t.Errorf("CompletePending() of a sign-up through an invitation error = %v, want Conflict: only the mailed code completes it", err)
	}
	if f.held[pendingID].ConsumedAt != nil {
		t.Error("CompletePending() consumed the wait, want it left for the code")
	}
}

func invitedSignUpAttemptRefusals(t *testing.T) {
	t.Parallel()

	const invited = "pat@lakeside.edu"
	// Each case changes what a sign-up's completion is given: the context a browser
	// that started the sign-up for the invitation and still carries it would bring,
	// and the address the code entered was mailed to.
	for _, tt := range []struct {
		name string
		// carries builds the completion's context from the sign-up's own.
		carries func(ctx context.Context, f *authFixture, links *mailLinks, kept *heldSignUp) context.Context
		// codeTo is the address the code went to; "" for the invited one.
		codeTo string
		want   sessioninfo.LoginRefusalCode
	}{
		{
			name: "a browser that carries nothing of the sign-up is answered that the wait ran out",
			carries: func(ctx context.Context, _ *authFixture, _ *mailLinks, _ *heldSignUp) context.Context {
				return context.WithValue(ctx, heldSignUpCtxKey{}, (*heldSignUp)(nil))
			},
			want: sessioninfo.RefusedPendingExpired,
		},
		{
			name: "a browser that carries a sign-up with no password is answered that the wait ran out",
			carries: func(ctx context.Context, _ *authFixture, _ *mailLinks, kept *heldSignUp) context.Context {
				return context.WithValue(ctx, heldSignUpCtxKey{}, &heldSignUp{InvitationID: kept.InvitationID, Username: kept.Username})
			},
			want: sessioninfo.RefusedPendingExpired,
		},
		{
			name: "a browser that no longer carries the invitation is answered that the invitation ended",
			carries: func(ctx context.Context, _ *authFixture, _ *mailLinks, _ *heldSignUp) context.Context {
				return context.WithValue(ctx, invitationCtxKey{}, nil)
			},
			want: sessioninfo.RefusedInvitationExpired,
		},
		{
			name: "an invitation that was used or withdrawn since is answered that the invitation ended",
			carries: func(ctx context.Context, _ *authFixture, links *mailLinks, _ *heldSignUp) context.Context {
				now := time.Now()
				links.rows[0].ConsumedAt = &now

				return ctx
			},
			want: sessioninfo.RefusedInvitationExpired,
		},
		{
			name: "a browser that carries another invitation than the sign-up's is answered that the wait ran out",
			carries: func(ctx context.Context, f *authFixture, links *mailLinks, _ *heldSignUp) context.Context {
				hash := hashPendingToken([]byte("another invitation's token"))
				links.insert(&sessionstorage.MailLink{Kind: sessionstorage.MailLinkInvitation, TokenHash: hash, Email: "sam@lakeside.edu", ExpiresAt: time.Now().Add(time.Hour)})

				return context.WithValue(ctx, invitationCtxKey{}, hash)
			},
			codeTo: "sam@lakeside.edu",
			want:   sessioninfo.RefusedPendingExpired,
		},
		{
			name: "a code that was mailed to another address than the invited one is answered as a code that died",
			carries: func(ctx context.Context, _ *authFixture, _ *mailLinks, _ *heldSignUp) context.Context {
				return ctx
			},
			codeTo: "mallory@elsewhere.test",
			want:   sessioninfo.RefusedCodeExpired,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f, recorder, links := newMailFixture(t, gomock.NewController(t))
			if _, err := f.auth.API().Invite(context.Background(), &InvitationRequest{Email: invited, TenantID: "lakeside"}); err != nil {
				t.Fatal(err)
			}
			hash, ok := linkTokenHash(tokenIn(t, recorder.Messages()[0].Text, "invitation"))
			if !ok {
				t.Fatal("the invitation's link carries no token")
			}
			kept := &heldSignUp{InvitationID: links.rows[0].ID, Username: "patl", PasswordHash: hashed(t, "correct horse battery staple")}
			ctx := context.WithValue(context.WithValue(t.Context(), invitationCtxKey{}, hash), heldSignUpCtxKey{}, kept)
			pending := &pendingState{ID: impulse.Must(impulse.NewUUID()), Step: sessioninfo.PendingMFA, Identity: sessioninfo.Identity{Method: sessioninfo.MethodPassword, Email: invited}}
			codeTo := invited
			if tt.codeTo != "" {
				codeTo = tt.codeTo
			}

			// The mocked store expects no ConsumePendingSignIn and no CreateUser: either
			// call fails the test.
			at, err := f.auth.invitedSignUpAttempt(tt.carries(ctx, f, links, kept), pending, codeTo)

			if at != nil || sessioninfo.LoginRefusalCodeOf(err) != tt.want {
				t.Errorf("invitedSignUpAttempt() = %v, %v; want no sign-in and the refusal %q", at, err, tt.want)
			}
		})
	}
}

// TestAuth_codeDestination holds where the code of a wait goes and whose count it is
// held to. The account's own address comes first: a person past the account's first
// step is sent the account's code. A wait whose account holds no address, or that names
// no account, in a browser that carries a live invitation is sent the invitation's
// code, at the invited address, which nobody types.
func TestAuth_codeDestination(t *testing.T) {
	t.Parallel()

	const invited = "pat@lakeside.edu"
	for _, tt := range []struct {
		name string
		// held is the address the wait's account holds; named says the wait names one.
		named bool
		held  string
		// carried says the browser carries a live invitation.
		carried bool
		// wantEmail is where the code goes, and wantInvitation whether it is the
		// invitation's. wantErr is the refusal of a wait that has nowhere to send one.
		wantEmail      string
		wantInvitation bool
		wantErr        sessioninfo.LoginRefusalCode
	}{
		{name: "an account that holds an address is sent its own code there, whatever invitation the browser carries", named: true, held: "own@lakeside.edu", carried: true, wantEmail: "own@lakeside.edu"},
		{name: "an account that holds an address and carries no invitation is sent its own code there", named: true, held: "own@lakeside.edu", wantEmail: "own@lakeside.edu"},
		{name: "an account with no address that carries an invitation is sent the invitation's code at the invited address", named: true, carried: true, wantEmail: invited, wantInvitation: true},
		{name: "an account with no address and no invitation has nowhere to send a code: it gives an address", named: true},
		{name: "a wait that names nobody is sent the invitation's code at the invited address", carried: true, wantEmail: invited, wantInvitation: true},
		{name: "a wait that names nobody in a browser with no live invitation has run out", wantErr: sessioninfo.RefusedPendingExpired},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f, recorder, links := newMailFixture(t, gomock.NewController(t))
			links.expectAccounts(f)
			f.store.EXPECT().InsertFactorCode(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
			ctx := t.Context()
			if tt.carried {
				if _, err := f.auth.API().Invite(ctx, &InvitationRequest{Email: invited, TenantID: "lakeside"}); err != nil {
					t.Fatal(err)
				}
				hash, ok := linkTokenHash(tokenIn(t, recorder.Messages()[0].Text, "invitation"))
				if !ok {
					t.Fatal("the invitation's link carries no token")
				}
				ctx = context.WithValue(ctx, invitationCtxKey{}, hash)
			}
			pending := &pendingState{ID: impulse.Must(impulse.NewUUID()), Step: sessioninfo.PendingMFA}
			if tt.named {
				user := &sessionstorage.SessionUser{ID: impulse.Must(impulse.NewUUID()), Username: "pat"}
				if tt.held != "" {
					user.Email = strPtr(tt.held)
				}
				f.store.EXPECT().User(gomock.Any(), user.ID).Return(user, nil).AnyTimes()
				pending.UserID = impulse.NullUUIDFromUUID(user.ID)
			}

			email, inv, err := f.auth.codeDestination(ctx, pending)

			if tt.wantErr == "" && err != nil || tt.wantErr != "" && sessioninfo.LoginRefusalCodeOf(err) != tt.wantErr {
				t.Fatalf("codeDestination() error = %v, want the refusal %q", err, tt.wantErr)
			}
			if email != tt.wantEmail || (inv != nil) != tt.wantInvitation {
				t.Errorf("codeDestination() = %q, the invitation's = %t; want %q, %t", email, inv != nil, tt.wantEmail, tt.wantInvitation)
			}
			if tt.wantErr != "" {
				return
			}

			// What the destination decides: the count a code is held to, and whether an
			// address may be typed for it.
			sent := len(recorder.Messages())
			_, err = f.auth.sendCodeToUser(ctx, pending)
			switch {
			case tt.wantEmail == "":
				if !httpio.HasConflict(err) || len(recorder.Messages()) != sent {
					t.Errorf("sendCodeToUser() error = %v with %d message(s), want 409 and nothing mailed: the account holds no address", err, len(recorder.Messages())-sent)
				}
			case err != nil:
				t.Errorf("sendCodeToUser() error = %v, want the code mailed", err)
			default:
				msgs := recorder.Messages()
				if len(msgs) != sent+1 || msgs[sent].To != tt.wantEmail {
					t.Fatalf("sendCodeToUser() mailed %d message(s), want one to %s", len(msgs)-sent, tt.wantEmail)
				}
				// The mail says what its code does. A code that accepts an invitation
				// comes in the invitation's own words, which say that somebody opened
				// the invitation and speak of no password: the person invited may have
				// no account yet. An account's own code comes in the sign-in code's.
				switch msg := msgs[sent]; {
				case !tt.wantInvitation && !signInCodeSubject.MatchString(msg.Subject):
					t.Errorf("the subject of an account's own code = %q, want the sign-in code's", msg.Subject)
				case tt.wantInvitation && !invitationCodeSubject.MatchString(msg.Subject):
					t.Errorf("the subject of a code that accepts an invitation = %q, want the invitation's own: a sign-in code's tells a person with no account to change a password", msg.Subject)
				case tt.wantInvitation && (strings.Contains(strings.ToLower(msg.Text), "password") || !strings.Contains(msg.Text, "someone else has its link")):
					t.Errorf("the mail of a code that accepts an invitation speaks of a password (%t), want none, and want it to say that someone else has the link where the person did not open it",
						strings.Contains(strings.ToLower(msg.Text), "password"))
				}
				key := codeLimitKey(tt.wantEmail)
				if tt.wantInvitation {
					key = invitationCodeLimitKey(invitationOf(links.rows[0]))
				}
				if record := f.attemptsRecord(key); record == nil || record.Failures != 1 {
					t.Errorf("the count the code is held to (%s) = %+v, want one", key, record)
				}
			}
			if tt.wantInvitation {
				if _, err := f.auth.enrollEmail(ctx, pending, "mallory@elsewhere.test"); !httpio.HasConflict(err) {
					t.Errorf("enrollEmail() for a wait that accepts an invitation error = %v, want 409: the code goes to the invited address", err)
				}
			}
		})
	}
}

// TestStandingRefusalSentence holds what a person signed in is told when the invitation
// their browser carries was not accepted because of its address: to sign in again for
// the code where the code is what is missing, and with the invited address otherwise.
func TestStandingRefusalSentence(t *testing.T) {
	t.Parallel()

	inv := &sessioninfo.Invitation{Email: "pat@lakeside.edu"}
	another := signinflow.Refusal("", sessioninfo.RefusedInvitationAddress, signin.ErrInvitationAddress, "the invitation was sent to another address")
	code := signinflow.Refusal("", sessioninfo.RefusedInvitationAddress, fmt.Errorf("%w: %w", signin.ErrInvitationAddress, signin.ErrInvitationCode), "the invitation waits for its code")

	if got, want := standingRefusalSentence(code, inv), signinflow.InvitedCodeMessage(inv); got != want {
		t.Errorf("standingRefusalSentence() of an acceptance that needs the code = %q, want %q", got, want)
	}
	if got, want := standingRefusalSentence(another, inv), signinflow.InvitedAddressMessage(inv); got != want {
		t.Errorf("standingRefusalSentence() of an acceptance with another address = %q, want %q", got, want)
	}
}
