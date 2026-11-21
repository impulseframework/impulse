package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/mail"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// The second factor the library runs, through the real handlers on both backends: the
// code mailed to the user's address, the browser remembered after it, the
// administrator's reset, the enrollment of an address at the step, and the limits.

// codePattern finds the spaced code in a code message's subject.
var codePattern = regexp.MustCompile(`\b(\d{3}) (\d{3})\b`)

// lastCode is the code of the last message the app sent, which must be a code message
// to address.
func (a *authApp[Tx]) lastCode(t *testing.T, to string) string {
	t.Helper()

	sent := a.mail.Messages()
	if len(sent) == 0 {
		t.Fatal("no mail was sent, want the code message")
	}
	last := sent[len(sent)-1]
	m := codePattern.FindStringSubmatch(last.Subject)
	if last.To != to || m == nil || !strings.Contains(last.Text, m[0]) {
		t.Fatalf("the last message = %+v, want a code message to %s with the code in its subject and text", last, to)
	}

	return m[1] + m[2]
}

// pendingStatus is the pending status as the page reads it.
type pendingStatus struct {
	Reason    string   `json:"reason"`
	Factors   []string `json:"factors"`
	EmailCode *struct {
		CodeEmail     string     `json:"codeEmail"`
		EnrollEmail   bool       `json:"enrollEmail"`
		CodeSent      bool       `json:"codeSent"`
		CodeExpiresAt *time.Time `json:"codeExpiresAt"`
	} `json:"emailCode"`
}

func (b *browser) pendingStatus(ctx context.Context) *pendingStatus {
	b.t.Helper()

	status := &pendingStatus{}
	if err := json.Unmarshal(b.expect(ctx, http.StatusOK, http.MethodGet, "/pending", nil), status); err != nil {
		b.t.Fatalf("json.Unmarshal() error = %v", err)
	}

	return status
}

// authenticatedName reads the session read's name, empty for none.
func (b *browser) authenticatedName(ctx context.Context) string {
	b.t.Helper()

	var body struct {
		Name *string `json:"name"`
	}
	if err := json.Unmarshal(b.expect(ctx, http.StatusOK, http.MethodGet, "/authenticated", nil), &body); err != nil {
		b.t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if body.Name == nil {
		return ""
	}

	return *body.Name
}

// authenticatedLook reads the session read's look, and whether the answer carries one.
func (b *browser) authenticatedLook(ctx context.Context) (look sessioninfo.Look, present bool) {
	b.t.Helper()

	var body struct {
		Look *sessioninfo.Look `json:"look"`
	}
	if err := json.Unmarshal(b.expect(ctx, http.StatusOK, http.MethodGet, "/authenticated", nil), &body); err != nil {
		b.t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if body.Look == nil {
		return sessioninfo.Look{}, false
	}

	return *body.Look, true
}

// authenticatedEmail reads the session read's address and whether it is verified.
func (b *browser) authenticatedEmail(ctx context.Context) (email string, verified bool) {
	b.t.Helper()

	var body struct {
		Email         *string `json:"email"`
		EmailVerified bool    `json:"emailVerified"`
	}
	if err := json.Unmarshal(b.expect(ctx, http.StatusOK, http.MethodGet, "/authenticated", nil), &body); err != nil {
		b.t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if body.Email == nil {
		return "", false
	}

	return *body.Email, body.EmailVerified
}

// seedWithEmail creates a user with seedPassword and email, unverified, as an operator
// enters it.
func (a *authApp[Tx]) seedWithEmail(ctx context.Context, t *testing.T, username, email string) impulse.UUID {
	t.Helper()

	pw := seedPassword
	id, err := a.api.CreateSessionUser(ctx, &session.CreateUserRequest{Username: username, Password: &pw, Email: &email})
	if err != nil {
		t.Fatalf("CreateSessionUser(%q) error = %v", username, err)
	}

	return id
}

// requireEmailCode is the policy of an application whose every session carries a
// second factor: the library's email code, which an attestation or a trusted device
// stands in for.
func requireEmailCode(*sessioninfo.NewSessionRequest) *signin.Decision {
	return signin.RequireMFA(sessioninfo.MethodEmailCode)
}

func seamEmailCode[Tx any](ctx context.Context, t *testing.T, a *authApp[Tx], _ *fakeWorkOS) {
	t.Helper()

	pat := a.seedWithEmail(ctx, t, "pat@lakeside.edu", "pat@lakeside.edu")
	a.hooks.set(func(h *hooks[Tx]) { h.policy = requireEmailCode })

	b := a.browser(t)
	b.prime(ctx)
	anonymous := b.sessionID(ctx)
	if status, mfa := b.passwordLogin(ctx, seedPassword); status != http.StatusOK || !mfa {
		t.Fatalf("password login = %d (mfaIsRequired %v), want 200 requiring MFA", status, mfa)
	}
	b.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/whoami", nil)

	// The status says where the code goes, masked, and that none stands yet.
	status := b.pendingStatus(ctx)
	if status.Reason != "mfa" || len(status.Factors) != 1 || status.Factors[0] != "email-code" || status.EmailCode == nil ||
		status.EmailCode.CodeSent || status.EmailCode.EnrollEmail || status.EmailCode.CodeEmail != "p•••@lakeside.edu" {
		t.Fatalf("pending status = %+v (emailCode %+v), want an MFA wait taking the email code at p•••@lakeside.edu, none sent", status, status.EmailCode)
	}
	// A code entered before one was sent: none stands, so the page asks for one.
	if status, body := b.do(ctx, http.MethodPost, "/pending/code/verify", map[string]any{"code": "000000"}); status != http.StatusUnauthorized || !strings.Contains(string(body), `"code":"code_expired"`) {
		t.Errorf("verify before a code was sent = %d %s, want 401 code_expired", status, body)
	}

	b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/send", nil)
	code := a.lastCode(t, "pat@lakeside.edu")
	status = b.pendingStatus(ctx)
	if status.EmailCode == nil || !status.EmailCode.CodeSent || status.EmailCode.CodeExpiresAt == nil || status.EmailCode.CodeEmail != "p•••@lakeside.edu" {
		t.Errorf("pending status after the send = %+v, want the code standing at p•••@lakeside.edu", status.EmailCode)
	}

	// A wrong code counts and leaves the code standing.
	wrong := "000000"
	if wrong == code {
		wrong = "111111"
	}
	if status, body := b.do(ctx, http.MethodPost, "/pending/code/verify", map[string]any{"code": wrong}); status != http.StatusUnauthorized || !strings.Contains(string(body), `"code":"invalid_code"`) {
		t.Errorf("a wrong code = %d %s, want 401 invalid_code", status, body)
	}
	replay := b.clone()

	// The right code, spaced as the mail shows it, completes the sign-in and
	// remembers the browser.
	spaced := code[:3] + " " + code[3:]
	if body := b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/verify", map[string]any{"code": spaced, "rememberDevice": true}); !strings.Contains(string(body), `"mfaIsRequired":false`) {
		t.Fatalf("verify = %s, want mfaIsRequired false", body)
	}
	v := b.view(ctx)
	if v.UserID != pat.String() || v.SessionID == anonymous {
		t.Errorf("session = %+v, want user %s under a session ID other than %s", v, pat, anonymous)
	}
	assertEventsSeen(t, v, "password", "email-code")
	// The code proved the address an operator entered: it is verified now.
	if email, verified := b.authenticatedEmail(ctx); email != "pat@lakeside.edu" || !verified {
		t.Errorf("the session read = email %q verified %v, want pat@lakeside.edu verified by the code", email, verified)
	}

	// The pending identity and the code were consumed: a copy of the cookies
	// completes nothing.
	replay.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/pending", nil)
	replay.expect(ctx, http.StatusUnauthorized, http.MethodPost, "/pending/code/verify", map[string]any{"code": code})
	replay.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/whoami", nil)

	seamEmailCodeTrustedDevice(ctx, t, a, b, pat)
}

// seamEmailCodeTrustedDevice is the rest of seamEmailCode: b was remembered when pat
// entered the code.
func seamEmailCodeTrustedDevice[Tx any](ctx context.Context, t *testing.T, a *authApp[Tx], b *browser, pat impulse.UUID) {
	t.Helper()

	// The remembered browser signs in without the factor: the trusted device stands
	// in for it, and the session says so.
	b.expect(ctx, http.StatusOK, http.MethodPost, "/logout", nil)
	b.prime(ctx)
	if status, mfa := b.passwordLogin(ctx, seedPassword); status != http.StatusOK || mfa {
		t.Fatalf("password login from the remembered browser = %d (mfaIsRequired %v), want 200 with no MFA", status, mfa)
	}
	assertEventsSeen(t, b.view(ctx), "password", "trusted-device")

	// Another browser is not remembered.
	other := a.browser(t)
	other.prime(ctx)
	if status, mfa := other.passwordLogin(ctx, seedPassword); status != http.StatusOK || !mfa {
		t.Errorf("password login from another browser = %d (mfaIsRequired %v), want 200 requiring MFA", status, mfa)
	}

	// The administrator's reset forgets the remembered browser: its next sign-in
	// asks for the factor again.
	if err := a.api.ResetFactors(ctx, pat); err != nil {
		t.Fatalf("ResetFactors() error = %v", err)
	}
	b.expect(ctx, http.StatusOK, http.MethodPost, "/logout", nil)
	b.prime(ctx)
	if status, mfa := b.passwordLogin(ctx, seedPassword); status != http.StatusOK || !mfa {
		t.Errorf("password login after the reset = %d (mfaIsRequired %v), want 200 requiring MFA", status, mfa)
	}
	sent := len(a.mail.Messages())
	b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/send", nil)
	if got := len(a.mail.Messages()); got != sent+1 {
		t.Errorf("messages sent after the second code = %d, want %d", got, sent+1)
	}
	b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/verify", map[string]any{"code": a.lastCode(t, "pat@lakeside.edu")})
	assertEventsSeen(t, b.view(ctx), "password", "email-code")
}

// loginAs posts a password login for username with the seed password and returns the
// answer's status and whether MFA is required.
func (b *browser) loginAs(ctx context.Context, username string) (status int, mfaIsRequired bool) {
	b.t.Helper()

	status, _, body := b.postLogin(ctx, username, seedPassword)
	var answer struct {
		MFAIsRequired bool `json:"mfaIsRequired"`
	}
	_ = json.Unmarshal(body, &answer)

	return status, answer.MFAIsRequired
}

// seamTrustedDevicePerUser: two people share one browser. Each is remembered after the
// code, and switching between them asks neither for it again (the cookie holds one
// entry per user, and a sign-in reads the signing-in user's alone); a third person held
// for the code on that browser forgets neither; a person who forgets the browser is
// asked again while the other stays remembered.
func seamTrustedDevicePerUser[Tx any](ctx context.Context, t *testing.T, a *authApp[Tx], _ *fakeWorkOS) {
	t.Helper()

	pat := a.seedWithEmail(ctx, t, "pat@lakeside.edu", "pat@lakeside.edu")
	sam := a.seedWithEmail(ctx, t, "sam@lakeside.edu", "sam@lakeside.edu")
	a.seedWithEmail(ctx, t, "kim@lakeside.edu", "kim@lakeside.edu")
	a.hooks.set(func(h *hooks[Tx]) { h.policy = requireEmailCode })
	b := a.browser(t)

	// remember signs username in through the code and has the browser remembered.
	remember := func(username string) {
		t.Helper()
		b.prime(ctx)
		if status, mfa := b.loginAs(ctx, username); status != http.StatusOK || !mfa {
			t.Fatalf("%s: password login = %d (mfaIsRequired %v), want 200 requiring MFA", username, status, mfa)
		}
		b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/send", nil)
		b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/verify", map[string]any{"code": a.lastCode(t, username), "rememberDevice": true})
		assertEventsSeen(t, b.view(ctx), "password", "email-code")
		b.expect(ctx, http.StatusOK, http.MethodPost, "/logout", nil)
	}
	// withoutCode signs username in and expects the trusted device to stand in.
	withoutCode := func(username string, id impulse.UUID) {
		t.Helper()
		b.prime(ctx)
		if status, mfa := b.loginAs(ctx, username); status != http.StatusOK || mfa {
			t.Fatalf("%s: password login from the remembered browser = %d (mfaIsRequired %v), want 200 with no MFA", username, status, mfa)
		}
		v := b.view(ctx)
		if v.UserID != id.String() {
			t.Errorf("%s: session user = %s, want %s", username, v.UserID, id)
		}
		assertEventsSeen(t, v, "password", "trusted-device")
	}
	// heldThenCancelled signs username in, expects the code step, and cancels it.
	heldThenCancelled := func(username string) {
		t.Helper()
		b.prime(ctx)
		if status, mfa := b.loginAs(ctx, username); status != http.StatusOK || !mfa {
			t.Fatalf("%s: password login = %d (mfaIsRequired %v), want 200 requiring MFA", username, status, mfa)
		}
		b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/cancel", nil)
	}

	// pat is remembered, then sam on the same browser: sam's remembering does not
	// forget pat, and each signs in without the code from then on, in turn.
	remember("pat@lakeside.edu")
	remember("sam@lakeside.edu")
	withoutCode("pat@lakeside.edu", pat)
	b.expect(ctx, http.StatusOK, http.MethodPost, "/logout", nil)
	withoutCode("sam@lakeside.edu", sam)
	b.expect(ctx, http.StatusOK, http.MethodPost, "/logout", nil)
	withoutCode("pat@lakeside.edu", pat)
	b.expect(ctx, http.StatusOK, http.MethodPost, "/logout", nil)

	// kim, never remembered here, is held for the code: pat and sam stay remembered.
	heldThenCancelled("kim@lakeside.edu")
	withoutCode("sam@lakeside.edu", sam)
	b.expect(ctx, http.StatusOK, http.MethodPost, "/logout", nil)
	withoutCode("pat@lakeside.edu", pat)

	// pat forgets the browser from the account page: the session read said it is
	// remembered for pat and says so no longer; pat is asked for the code again, sam is not.
	if !browserRemembered(ctx, t, b) {
		t.Fatal("the session read of pat on the remembered browser says it is not remembered")
	}
	b.expect(ctx, http.StatusOK, http.MethodPost, "/forget-browser", nil)
	if browserRemembered(ctx, t, b) {
		t.Error("the session read after pat forgot the browser still says it is remembered")
	}
	b.expect(ctx, http.StatusOK, http.MethodPost, "/logout", nil)
	heldThenCancelled("pat@lakeside.edu")
	withoutCode("sam@lakeside.edu", sam)
	b.expect(ctx, http.StatusOK, http.MethodPost, "/logout", nil)

	// The administrator's reset of sam forgets sam's browsers and no one else's: pat,
	// remembered again, is still remembered after it.
	remember("pat@lakeside.edu")
	if err := a.api.ResetFactors(ctx, sam); err != nil {
		t.Fatalf("ResetFactors() error = %v", err)
	}
	heldThenCancelled("sam@lakeside.edu")
	withoutCode("pat@lakeside.edu", pat)
}

// browserRemembered reads the session and answers whether it says the browser is
// remembered for the person signed in.
func browserRemembered(ctx context.Context, t *testing.T, b *browser) bool {
	t.Helper()

	var answer struct {
		BrowserRemembered bool `json:"browserRemembered"`
	}
	if err := json.Unmarshal(b.expect(ctx, http.StatusOK, http.MethodGet, "/authenticated", nil), &answer); err != nil {
		t.Fatalf("decoding the session read: %v", err)
	}

	return answer.BrowserRemembered
}

func seamEmailCodeEnrollment[Tx any](ctx context.Context, t *testing.T, a *authApp[Tx], _ *fakeWorkOS) {
	t.Helper()

	// kim holds the address the enrolling person will try first: it is told by mail,
	// and the screen says nothing.
	a.seedWithEmail(ctx, t, "kim", "kim@lakeside.edu")
	pat := a.seed(ctx, t, "pat")
	a.hooks.set(func(h *hooks[Tx]) { h.policy = requireEmailCode })

	b := a.browser(t)
	b.prime(ctx)
	b.expect(ctx, http.StatusOK, http.MethodPost, "/login", map[string]string{"username": "pat", "password": seedPassword})
	status := b.pendingStatus(ctx)
	if status.EmailCode == nil || !status.EmailCode.EnrollEmail || status.EmailCode.CodeEmail != "" {
		t.Fatalf("pending status = %+v, want the step asking for an address", status.EmailCode)
	}
	if status, body := b.do(ctx, http.MethodPost, "/pending/code/send", nil); status != http.StatusConflict {
		t.Errorf("send for a user without an address = %d %s, want 409", status, body)
	}
	if status, body := b.do(ctx, http.MethodPost, "/pending/email", map[string]string{"email": "not an address"}); status != http.StatusBadRequest {
		t.Errorf("enroll with no address = %d %s, want 400", status, body)
	}

	// Someone else's address: answered as sent, the holder told, no code.
	b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/email", map[string]string{"email": "KIM@lakeside.edu"})
	sent := a.mail.Messages()
	if last := sent[len(sent)-1]; last.To != "KIM@lakeside.edu" || !strings.Contains(last.Subject, "Your email address and") {
		t.Errorf("the last message = %+v, want the address-held notice to the holder", last)
	}
	if status, body := b.do(ctx, http.MethodPost, "/pending/code/verify", map[string]any{"code": "123456"}); status != http.StatusUnauthorized || !strings.Contains(string(body), `"code":"code_expired"`) {
		t.Errorf("verify after an enrollment of a held address = %d %s, want 401 code_expired: no code was sent", status, body)
	}

	// Their own: the code goes there, and the address is theirs once entered.
	b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/email", map[string]string{"email": "pat@lakeside.edu"})
	code := a.lastCode(t, "pat@lakeside.edu")
	if status := b.pendingStatus(ctx); status.EmailCode == nil || !status.EmailCode.CodeSent || status.EmailCode.CodeEmail != "p•••@lakeside.edu" || status.EmailCode.EnrollEmail {
		t.Errorf("pending status after the enrollment = %+v, want the code standing at p•••@lakeside.edu", status.EmailCode)
	}
	b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/verify", map[string]any{"code": code})
	v := b.view(ctx)
	if v.UserID != pat.String() {
		t.Errorf("session user = %s, want %s", v.UserID, pat)
	}
	assertEventsSeen(t, v, "password", "email-code")
	if email, verified := b.authenticatedEmail(ctx); email != "pat@lakeside.edu" || !verified {
		t.Errorf("the session read = email %q verified %v, want pat@lakeside.edu verified by the code", email, verified)
	}

	// The next sign-in finds the address on the user: no enrollment, the code goes
	// there.
	b.expect(ctx, http.StatusOK, http.MethodPost, "/logout", nil)
	b.prime(ctx)
	b.expect(ctx, http.StatusOK, http.MethodPost, "/login", map[string]string{"username": "pat", "password": seedPassword})
	if status := b.pendingStatus(ctx); status.EmailCode == nil || status.EmailCode.EnrollEmail || status.EmailCode.CodeEmail != "p•••@lakeside.edu" {
		t.Errorf("pending status at the next sign-in = %+v, want the code going to p•••@lakeside.edu", status.EmailCode)
	}
	if status, body := b.do(ctx, http.MethodPost, "/pending/email", map[string]string{"email": "other@lakeside.edu"}); status != http.StatusConflict {
		t.Errorf("enroll for a user holding an address = %d %s, want 409", status, body)
	}
}

// seamEmailCodeAddressTakenOff: the sign-in is the account's own (the person proved its
// password) and waits for the code mailed to the address the account holds. An
// administrator takes the address off before the code is entered. The code was mailed
// before the change, so it is dead and writes nothing back; the person gives the
// address at the step, as a person whose account holds none does, and that code writes
// it.
func seamEmailCodeAddressTakenOff[Tx any](ctx context.Context, t *testing.T, a *authApp[Tx], _ *fakeWorkOS) {
	t.Helper()

	pat := a.seedWithEmail(ctx, t, "pat", "pat@lakeside.edu")
	a.hooks.set(func(h *hooks[Tx]) { h.policy = requireEmailCode })

	b := a.browser(t)
	b.prime(ctx)
	b.expect(ctx, http.StatusOK, http.MethodPost, "/login", map[string]string{"username": "pat", "password": seedPassword})
	b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/send", nil)
	mailedBefore := a.lastCode(t, "pat@lakeside.edu")
	if err := a.setEmail(ctx, pat, nil); err != nil {
		t.Fatalf("SetUserEmail(nil) error = %v", err)
	}

	if status, body := b.do(ctx, http.MethodPost, "/pending/code/verify", map[string]any{"code": mailedBefore}); status != http.StatusUnauthorized || !strings.Contains(string(body), `"code":"code_expired"`) {
		t.Fatalf("verify of the code mailed before the address was taken off = %d %s, want 401 code_expired", status, body)
	}
	b.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/whoami", nil)
	if user, err := a.api.User(ctx, pat); err != nil || user.Email != nil {
		t.Fatalf("the account after the dead code = %+v, %v, want it holding no address: the administrator's change stands", user, err)
	}
	if status := b.pendingStatus(ctx); status.EmailCode == nil || !status.EmailCode.EnrollEmail || status.EmailCode.CodeSent {
		t.Errorf("pending status after the address was taken off = %+v, want the step asking for an address and no code standing", status.EmailCode)
	}

	b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/email", map[string]string{"email": "pat@lakeside.edu"})
	given := a.lastCode(t, "pat@lakeside.edu")
	b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/verify", map[string]any{"code": given})
	if email, verified := b.authenticatedEmail(ctx); email != "pat@lakeside.edu" || !verified {
		t.Errorf("the session read = email %q verified %v, want pat@lakeside.edu verified by the code for the address the person gave", email, verified)
	}
}

// seamEmailCodeUsedResetsLimit: a person who signs in with each code they are sent
// never meets the address's mail limit. Seven sign-ins in a row, more than the limit
// allows unused, each finished with its code, are each mailed one.
func seamEmailCodeUsedResetsLimit[Tx any](ctx context.Context, t *testing.T, a *authApp[Tx], _ *fakeWorkOS) {
	t.Helper()

	a.seedWithEmail(ctx, t, "pat@lakeside.edu", "pat@lakeside.edu")
	a.hooks.set(func(h *hooks[Tx]) { h.policy = requireEmailCode })
	b := a.browser(t)

	for i := 1; i <= 7; i++ {
		b.prime(ctx)
		if status, mfa := b.loginAs(ctx, "pat@lakeside.edu"); status != http.StatusOK || !mfa {
			t.Fatalf("sign-in %d: password login = %d (mfaIsRequired %v), want 200 requiring MFA", i, status, mfa)
		}
		if status, body := b.do(ctx, http.MethodPost, "/pending/code/send", nil); status != http.StatusOK {
			t.Fatalf("sign-in %d: send = %d %s, want 200: each used code resets the address", i, status, body)
		}
		b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/verify", map[string]any{"code": a.lastCode(t, "pat@lakeside.edu")})
		assertEventsSeen(t, b.view(ctx), "password", "email-code")
		b.expect(ctx, http.StatusOK, http.MethodPost, "/logout", nil)
	}
}

// seamEmailCodeGuessesAtOnce: the code's attempts hold under guesses made at once,
// through the real handlers and the database's own counting. A dozen wrong guesses
// sent together each take an attempt: four are told the code is wrong, every other
// that no code stands, none is answered with a failure of the server, and the right
// code is dead after them. A dozen copies of the right code sent together sign one
// browser in.
func seamEmailCodeGuessesAtOnce[Tx any](ctx context.Context, t *testing.T, a *authApp[Tx], _ *fakeWorkOS) {
	t.Helper()

	a.seedWithEmail(ctx, t, "pat@lakeside.edu", "pat@lakeside.edu")
	a.hooks.set(func(h *hooks[Tx]) { h.policy = requireEmailCode })

	const guesses = 12
	// atOnce enters code from a dozen copies of b's cookies together and returns what
	// each was answered.
	atOnce := func(b *browser, code string) (statuses []int, bodies []string) {
		copies := make([]*browser, guesses)
		for i := range copies {
			copies[i] = b.clone()
		}
		statuses, bodies = make([]int, guesses), make([]string, guesses)
		failed := make([]error, guesses)
		var wg sync.WaitGroup
		for i, c := range copies {
			wg.Go(func() {
				status, body, err := c.send(ctx, http.MethodPost, "/pending/code/verify", map[string]any{"code": code})
				statuses[i], bodies[i], failed[i] = status, string(body), err
			})
		}
		wg.Wait()
		for i, err := range failed {
			if err != nil {
				t.Fatalf("guess %d of %d at once: %v", i, guesses, err)
			}
		}

		return statuses, bodies
	}

	b := a.browser(t)
	b.prime(ctx)
	b.expect(ctx, http.StatusOK, http.MethodPost, "/login", map[string]string{"username": "pat@lakeside.edu", "password": seedPassword})
	b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/send", nil)
	code := a.lastCode(t, "pat@lakeside.edu")
	wrong := "000000"
	if wrong == code {
		wrong = "111111"
	}

	statuses, bodies := atOnce(b, wrong)
	invalid, dead := 0, 0
	for i, status := range statuses {
		switch {
		case status == http.StatusUnauthorized && strings.Contains(bodies[i], `"code":"invalid_code"`):
			invalid++
		case status == http.StatusUnauthorized && strings.Contains(bodies[i], `"code":"code_expired"`):
			dead++
		default:
			t.Errorf("wrong guess %d of %d at once = %d %s, want 401 invalid_code or code_expired", i, guesses, status, bodies[i])
		}
	}
	if invalid != 4 || dead != guesses-4 {
		t.Errorf("%d wrong guesses at once: %d were told the code is wrong and %d that none stands, want 4 and %d", guesses, invalid, dead, guesses-4)
	}
	if status, body := b.do(ctx, http.MethodPost, "/pending/code/verify", map[string]any{"code": code}); status != http.StatusUnauthorized || !strings.Contains(string(body), `"code":"code_expired"`) {
		t.Errorf("the right code after %d wrong guesses at once = %d %s, want 401 code_expired", guesses, status, body)
	}

	// A new code, entered right from a dozen copies of the browser at once: one is
	// signed in, and every other is refused.
	b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/send", nil)
	statuses, bodies = atOnce(b, a.lastCode(t, "pat@lakeside.edu"))
	accepted := 0
	for i, status := range statuses {
		switch status {
		case http.StatusOK:
			accepted++
		case http.StatusUnauthorized:
		default:
			t.Errorf("right guess %d of %d at once = %d %s, want 200 or 401", i, guesses, status, bodies[i])
		}
	}
	if accepted != 1 {
		t.Errorf("%d copies of the right code at once: %d were accepted, want one", guesses, accepted)
	}
}

// mailedCodes are the codes of the code messages among messages.
func mailedCodes(messages []mail.Message) map[string]bool {
	codes := make(map[string]bool, len(messages))
	for _, m := range messages {
		if code := codePattern.FindStringSubmatch(m.Subject); code != nil {
			codes[code[1]+code[2]] = true
		}
	}

	return codes
}

// unmailedCode is a code that is none of mailed: a wrong guess, whichever code stands.
func unmailedCode(mailed map[string]bool) string {
	for n := 0; ; n++ {
		if code := fmt.Sprintf("%06d", n); !mailed[code] {
			return code
		}
	}
}

// seamEmailCodesAskedAtOnce: the address's mail limit holds under codes asked for at
// once, through the real handlers and the database's own counting. A dozen requests for
// a code sent together for one pending sign-in each take their place under the
// address's limit before anything is mailed: five are mailed, every other is refused
// with too_many_emails, and none is answered with a failure of the server. One of the
// five codes stands, and it signs the person in.
func seamEmailCodesAskedAtOnce[Tx any](ctx context.Context, t *testing.T, a *authApp[Tx], _ *fakeWorkOS) {
	t.Helper()

	a.seedWithEmail(ctx, t, "pat@lakeside.edu", "pat@lakeside.edu")
	a.hooks.set(func(h *hooks[Tx]) { h.policy = requireEmailCode })
	b := a.browser(t)
	b.prime(ctx)
	b.expect(ctx, http.StatusOK, http.MethodPost, "/login", map[string]string{"username": "pat@lakeside.edu", "password": seedPassword})

	const (
		requests = 12
		limit    = 5
	)
	copies := make([]*browser, requests)
	for i := range copies {
		copies[i] = b.clone()
	}
	statuses, bodies := make([]int, requests), make([]string, requests)
	failed := make([]error, requests)
	var wg sync.WaitGroup
	for i, c := range copies {
		wg.Go(func() {
			status, body, err := c.send(ctx, http.MethodPost, "/pending/code/send", nil)
			statuses[i], bodies[i], failed[i] = status, string(body), err
		})
	}
	wg.Wait()
	sent, refused := 0, 0
	for i, status := range statuses {
		switch {
		case failed[i] != nil:
			t.Fatalf("request %d of %d at once: %v", i, requests, failed[i])
		case status == http.StatusOK:
			sent++
		case status == http.StatusTooManyRequests && strings.Contains(bodies[i], `"code":"too_many_emails"`):
			refused++
		default:
			t.Errorf("request %d of %d at once = %d %s, want 200 or 429 too_many_emails", i, requests, status, bodies[i])
		}
	}
	messages := a.mail.Messages()
	if len(messages) != limit {
		t.Errorf("%d codes asked for at once: %d messages were mailed, want %d: the address's limit", requests, len(messages), limit)
	}
	if sent != limit || refused != requests-limit {
		t.Errorf("%d codes asked for at once: %d were answered as sent and %d refused, want %d and %d", requests, sent, refused, limit, requests-limit)
	}

	// One of the codes mailed stands: entered in turn, the others are told they are
	// wrong, and the one that stands signs the person in within its attempts.
	signedIn := false
	for code := range mailedCodes(messages) {
		status, body := b.do(ctx, http.MethodPost, "/pending/code/verify", map[string]any{"code": code})
		if status == http.StatusOK {
			signedIn = true

			break
		}
		if status != http.StatusUnauthorized || !strings.Contains(string(body), `"code":"invalid_code"`) {
			t.Fatalf("a code mailed at once with the others = %d %s, want 200 or 401 invalid_code", status, body)
		}
	}
	if !signedIn {
		t.Fatal("none of the codes mailed signed the person in, want the one that stands to")
	}
	assertEventsSeen(t, b.view(ctx), "password", "email-code")
}

// seamEmailCodesRecordedAtOnce: one code stands for a pending sign-in however many are
// recorded at once, through the real handlers. A person who holds no address gives a
// dozen addresses at the step together, each mailed a code under its own address's
// limit, so nothing but the database orders the dozen codes recorded for the one
// pending sign-in. One of them stands: five wrong guesses end it, and after them none
// of the dozen codes mailed is left to be guessed at.
func seamEmailCodesRecordedAtOnce[Tx any](ctx context.Context, t *testing.T, a *authApp[Tx], _ *fakeWorkOS) {
	t.Helper()

	a.seed(ctx, t, "pat")
	a.hooks.set(func(h *hooks[Tx]) { h.policy = requireEmailCode })
	b := a.browser(t)
	b.prime(ctx)
	b.expect(ctx, http.StatusOK, http.MethodPost, "/login", map[string]string{"username": "pat", "password": seedPassword})

	const addresses = 12
	copies := make([]*browser, addresses)
	for i := range copies {
		copies[i] = b.clone()
	}
	statuses, bodies := make([]int, addresses), make([]string, addresses)
	failed := make([]error, addresses)
	var wg sync.WaitGroup
	for i, c := range copies {
		wg.Go(func() {
			status, body, err := c.send(ctx, http.MethodPost, "/pending/email", map[string]string{"email": fmt.Sprintf("pat%d@lakeside.edu", i)})
			statuses[i], bodies[i], failed[i] = status, string(body), err
		})
	}
	wg.Wait()
	for i, status := range statuses {
		if failed[i] != nil || status != http.StatusOK {
			t.Fatalf("address %d of %d given at once = %d %s (%v), want 200", i, addresses, status, bodies[i], failed[i])
		}
	}
	mailed := mailedCodes(a.mail.Messages())
	if got := len(a.mail.Messages()); got != addresses {
		t.Fatalf("%d addresses given at once: %d messages were mailed, want one code to each", addresses, got)
	}

	wrong := unmailedCode(mailed)
	for i := 1; i <= 4; i++ {
		if status, body := b.do(ctx, http.MethodPost, "/pending/code/verify", map[string]any{"code": wrong}); status != http.StatusUnauthorized || !strings.Contains(string(body), `"code":"invalid_code"`) {
			t.Fatalf("wrong code #%d = %d %s, want 401 invalid_code", i, status, body)
		}
	}
	if status, body := b.do(ctx, http.MethodPost, "/pending/code/verify", map[string]any{"code": wrong}); status != http.StatusUnauthorized || !strings.Contains(string(body), `"code":"code_expired"`) {
		t.Errorf("the fifth wrong code = %d %s, want 401 code_expired", status, body)
	}
	for code := range mailed {
		if status, body := b.do(ctx, http.MethodPost, "/pending/code/verify", map[string]any{"code": code}); status != http.StatusUnauthorized || !strings.Contains(string(body), `"code":"code_expired"`) {
			t.Errorf("a code mailed at once with the others, entered after five wrong guesses = %d %s, want 401 code_expired: one code stood, and it is ended", status, body)
		}
	}
	if status := b.pendingStatus(ctx); status.EmailCode == nil || status.EmailCode.CodeSent {
		t.Errorf("pending status after the code was ended = %+v, want none standing", status.EmailCode)
	}
}

func seamEmailCodeLimits[Tx any](ctx context.Context, t *testing.T, a *authApp[Tx], _ *fakeWorkOS) {
	t.Helper()

	a.seedWithEmail(ctx, t, "pat@lakeside.edu", "pat@lakeside.edu")
	a.hooks.set(func(h *hooks[Tx]) { h.policy = requireEmailCode })

	b := a.browser(t)
	b.prime(ctx)
	b.expect(ctx, http.StatusOK, http.MethodPost, "/login", map[string]string{"username": "pat@lakeside.edu", "password": seedPassword})
	b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/send", nil)
	code := a.lastCode(t, "pat@lakeside.edu")
	wrong := "000000"
	if wrong == code {
		wrong = "111111"
	}
	for i := 1; i <= 4; i++ {
		if status, body := b.do(ctx, http.MethodPost, "/pending/code/verify", map[string]any{"code": wrong}); status != http.StatusUnauthorized || !strings.Contains(string(body), `"code":"invalid_code"`) {
			t.Fatalf("wrong code #%d = %d %s, want 401 invalid_code", i, status, body)
		}
	}
	// The fifth wrong code kills the code: the right one is dead too.
	if status, body := b.do(ctx, http.MethodPost, "/pending/code/verify", map[string]any{"code": wrong}); status != http.StatusUnauthorized || !strings.Contains(string(body), `"code":"code_expired"`) {
		t.Errorf("the fifth wrong code = %d %s, want 401 code_expired", status, body)
	}
	if status, body := b.do(ctx, http.MethodPost, "/pending/code/verify", map[string]any{"code": code}); status != http.StatusUnauthorized || !strings.Contains(string(body), `"code":"code_expired"`) {
		t.Errorf("the right code after five wrong ones = %d %s, want 401 code_expired", status, body)
	}
	if status := b.pendingStatus(ctx); status.EmailCode == nil || status.EmailCode.CodeSent {
		t.Errorf("pending status after the code died = %+v, want none standing", status.EmailCode)
	}

	// A new code replaces it, and the old one is dead with it.
	b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/send", nil)
	fresh := a.lastCode(t, "pat@lakeside.edu")
	b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/send", nil)
	freshest := a.lastCode(t, "pat@lakeside.edu")
	if fresh != freshest {
		if status, body := b.do(ctx, http.MethodPost, "/pending/code/verify", map[string]any{"code": fresh}); status != http.StatusUnauthorized {
			t.Errorf("the code a newer one replaced = %d %s, want 401", status, body)
		}
	}
	// Five codes sent within the window and none used: the sixth is the address's
	// limit, refused with its own code and the wait.
	b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/send", nil)
	b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/send", nil)
	last := a.lastCode(t, "pat@lakeside.edu")
	sent := len(a.mail.Messages())
	if status, body := b.do(ctx, http.MethodPost, "/pending/code/send", nil); status != http.StatusTooManyRequests || !strings.Contains(string(body), `"code":"too_many_emails"`) || !strings.Contains(string(body), `"retryAfter":`) {
		t.Errorf("the sixth code within fifteen minutes = %d %s, want 429 too_many_emails with the wait", status, body)
	}
	if got := len(a.mail.Messages()); got != sent {
		t.Errorf("messages after the refused send = %d, want %d: nothing is mailed", got, sent)
	}
	b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/verify", map[string]any{"code": last})
	assertEventsSeen(t, b.view(ctx), "password", "email-code")

	// A policy that accepts the application's own factor only offers no code: the
	// handlers refuse, and the status says nothing of it.
	a.hooks.set(func(h *hooks[Tx]) {
		h.policy = func(*sessioninfo.NewSessionRequest) *signin.Decision { return signin.RequireMFA("totp") }
	})
	other := a.browser(t)
	other.prime(ctx)
	other.expect(ctx, http.StatusOK, http.MethodPost, "/login", map[string]string{"username": "pat@lakeside.edu", "password": seedPassword})
	if status := other.pendingStatus(ctx); status.EmailCode != nil || len(status.Factors) != 1 || status.Factors[0] != "totp" {
		t.Errorf("pending status under a totp-only policy = %+v, want the factor and no email code", status)
	}
	if status, body := other.do(ctx, http.MethodPost, "/pending/code/send", nil); status != http.StatusConflict {
		t.Errorf("send under a totp-only policy = %d %s, want 409", status, body)
	}
}
