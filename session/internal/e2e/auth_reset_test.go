//go:build !skipAuth

// The password reset link and the change of an account's address through the real
// handlers on both backends: what a mailed link does, to which address it goes, what it
// leaves alone until it is followed, and the setting an administrator turns on and off
// while the application runs.

package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/mail"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
)

// chosenPassword is the password a person chooses at a reset link.
const chosenPassword = "a new password of my own"

// resetApp is an application with the password method and mail: the password reset and
// email routes are mounted, as they are in every such application, and the login page's
// list names them.
type resetApp[Tx any] struct {
	*authApp[Tx]
	store *sessionstorage.Auth[Tx]
}

func newResetApp[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) *resetApp[Tx] {
	t.Helper()

	h := &hooks[Tx]{rows: be.rows}
	db, store := be.newStore(ctx, t)
	recorder := &mail.Recorder{}
	auth, err := session.NewAuth[appSessionData, appUserData](withCustomData(t, store, h), cookieKey, session.NoSuperAdministrators(),
		session.WithMail(recorder, session.MailSettings{Application: "Lakeside", LoginURL: "https://app.example/login"}))
	if err != nil {
		t.Fatalf("session.NewAuth() error = %v", err)
	}
	auth.SetSignInPolicy(h.signInPolicy)
	password := session.PasswordSignIn(auth)

	r := chi.NewRouter()
	mountAuth(r, auth, func(r chi.Router) {
		r.With(auth.ValidateXSRFToken).Post("/login", password.Login())
		r.With(auth.ValidateXSRFToken).Post("/password", password.ChangeUserPassword())
		r.With(auth.ValidateXSRFToken).Post("/reset", auth.PasswordReset().Request())
		r.With(auth.ValidateXSRFToken).Post("/reset/open", auth.PasswordReset().Open())
		r.With(auth.ValidateXSRFToken).Post("/reset/complete", auth.PasswordReset().Complete())
		r.With(auth.ValidateXSRFToken).Post("/email/confirm", auth.Email().Confirm())
		r.With(auth.ValidateXSRFToken, auth.ValidateSession).Post("/email", auth.Email().Add())
		r.With(auth.ValidateXSRFToken, auth.ValidateSession).Post("/email/verify", auth.Email().Verify())
		r.Get("/methods", auth.Methods(&session.MethodList{
			Methods:        []session.AdvertisedMethod{{Name: "password", Label: "Password", Path: "/login"}},
			ChangePassword: "/password",
			PasswordReset:  session.PasswordResetRoutes{Request: "/reset", Open: "/reset/open", Complete: "/reset/complete"},
			Email:          session.EmailRoutes{Add: "/email", Verify: "/email/verify", Confirm: "/email/confirm"},
		}))
	})
	server := httptest.NewTLSServer(r)
	t.Cleanup(server.Close)

	return &resetApp[Tx]{
		authApp: &authApp[Tx]{db: db, rows: be.rows, server: server, api: auth.API(), hooks: h, mail: recorder, verified: store.VerifyUserEmail},
		store:   store,
	}
}

// seedConfirmed creates a user with seedPassword who holds email as their confirmed
// address: a person who came in by an invitation or a sign-up.
func (a *resetApp[Tx]) seedConfirmed(ctx context.Context, t *testing.T, username, email string) impulse.UUID {
	t.Helper()

	id := a.seedWithEmail(ctx, t, username, email)
	if err := a.store.VerifyUserEmail(ctx, id, email); err != nil {
		t.Fatalf("VerifyUserEmail(%q) error = %v", email, err)
	}

	return id
}

// setOwnReset turns self-service reset on or off, as an administrator does in the
// running application: the one field of the password method's row of the sign-in
// methods table.
func (a *resetApp[Tx]) setOwnReset(ctx context.Context, t *testing.T, on bool) {
	t.Helper()

	if err := a.api.SetSelfServiceReset(ctx, on); err != nil {
		t.Fatalf("SetSelfServiceReset(%v) error = %v", on, err)
	}
}

// offersOwnReset reports whether the login page's list names the path a person asks for
// their own reset link at, and fails unless it names where a link is followed.
func (a *resetApp[Tx]) offersOwnReset(ctx context.Context, b *browser) bool {
	b.t.Helper()

	var list struct {
		PasswordReset *struct {
			Request  string `json:"request"`
			Open     string `json:"open"`
			Complete string `json:"complete"`
		} `json:"passwordReset"`
	}
	if err := json.Unmarshal(b.expect(ctx, http.StatusOK, http.MethodGet, "/methods", nil), &list); err != nil {
		b.t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if list.PasswordReset == nil || list.PasswordReset.Open != "/reset/open" || list.PasswordReset.Complete != "/reset/complete" {
		b.t.Fatalf("the login page's password reset block = %+v, want where a link is followed, whatever self-service is set to", list.PasswordReset)
	}

	return list.PasswordReset.Request == "/reset"
}

// mailed are the messages the application has sent, once the ones it sends after a
// request is answered have gone.
func (a *resetApp[Tx]) mailed() []mail.Message {
	a.api.WaitForMail()

	return a.mail.Messages()
}

// lastLink is the last message the application sent, which must be to address and carry
// a link under param, and that link's token.
func (a *resetApp[Tx]) lastLink(t *testing.T, to, param string) (message mail.Message, token string) {
	t.Helper()

	messages := a.mailed()
	if len(messages) == 0 {
		t.Fatalf("mailed messages = 0, want a ?%s= link to %s", param, to)
	}
	last := messages[len(messages)-1]
	m := regexp.MustCompile(`https://app\.example/login\?` + param + `=([A-Za-z0-9_-]+)`).FindStringSubmatch(last.Text)
	if last.To != to || m == nil {
		t.Fatalf("the last message = %+v, want one to %s with a ?%s= link", last, to, param)
	}

	return last, m[1]
}

// TestAuthSeams_PasswordReset runs the reset link and address change scenarios on each
// backend.
func TestAuthSeams_PasswordReset(t *testing.T) {
	t.Parallel()

	t.Run(postgresBackend.name, func(t *testing.T) {
		t.Parallel()
		runResetSeams(t, postgresBackend)
	})
	t.Run(spannerBackend.name, func(t *testing.T) {
		t.Parallel()
		runResetSeams(t, spannerBackend)
	})
}

func runResetSeams[Tx any](t *testing.T, be backend[Tx]) {
	tests := []struct {
		name string
		run  func(ctx context.Context, t *testing.T, be backend[Tx])
	}{
		{
			name: "a reset link changes nothing until it is used, works once, then refuses the old password and ends the open sessions; a person asks for their own only while an administrator has that on, and an address with no account is answered the same and mailed nothing",
			run:  seamResetLink[Tx],
		},
		{
			name: "a sixth mail to one address within fifteen minutes is refused whoever asks, and a link that is used resets the count",
			run:  seamResetLimit[Tx],
		},
		{
			name: "an administrator's change of address does nothing until the new address confirms: the account keeps its address, the sign-in code still goes there and nothing is mailed to it; once confirmed the old address is told and the code goes to the new one",
			run:  seamAddressChange[Tx],
		},
		{
			name: "a person's own change of address takes effect when the new address confirms, and tells the old one then",
			run:  seamOwnAddressChange[Tx],
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.run(t.Context(), t, be)
		})
	}
}

func seamResetLink[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	t.Helper()

	a := newResetApp(ctx, t, be)
	pat := a.seedConfirmed(ctx, t, "pat", "pat@lakeside.edu")

	// Nobody has turned self-service on: the page is not told where to ask, the request
	// is refused, and nothing is mailed.
	b := a.browser(t)
	b.prime(ctx)
	if a.offersOwnReset(ctx, b) {
		t.Error("the login page is offered a person's own reset before anyone turned it on, want none")
	}
	b.expect(ctx, http.StatusNotFound, http.MethodPost, "/reset", map[string]string{"email": "pat@lakeside.edu"})
	if messages := a.mailed(); len(messages) != 0 {
		t.Fatalf("mailed messages = %d, want none while self-service reset is off", len(messages))
	}

	// An administrator sends one all the same, and the message says whose administrator
	// asked. Nothing about the account changed: the password it had signs in.
	if err := a.api.SendPasswordReset(ctx, pat, session.AskedByApplication()); err != nil {
		t.Fatalf("SendPasswordReset() error = %v", err)
	}
	message, adminToken := a.lastLink(t, "pat@lakeside.edu", "reset")
	if !strings.HasPrefix(message.Text, "An administrator of Lakeside asked us to send you a link to reset the password of your Lakeside account.") {
		t.Errorf("the administrator's link opens with %q, want it to name who asked", strings.SplitN(message.Text, "\n", 2)[0])
	}
	open := a.browser(t).primed(ctx)
	if status, _, body := open.postLogin(ctx, "pat", seedPassword); status != http.StatusOK {
		t.Fatalf("login with the old password while a link stands = %d %s, want 200: the link changes nothing until it is used", status, body)
	}
	signedInAs(ctx, t, open, "pat")

	// An administrator turns self-service on in the running application: the page is
	// told where to ask, and the person's own request mails a link that says they asked.
	a.setOwnReset(ctx, t, true)
	if !a.offersOwnReset(ctx, b) {
		t.Error("the login page is not offered a person's own reset once it is turned on, want the path")
	}
	if body := b.expect(ctx, http.StatusOK, http.MethodPost, "/reset", map[string]string{"email": "Pat@Lakeside.edu"}); strings.TrimSpace(string(body)) != "{}" {
		t.Errorf("the request's answer = %s, want {}", body)
	}
	message, ownToken := a.lastLink(t, "pat@lakeside.edu", "reset")
	if !strings.HasPrefix(message.Text, "You asked to reset the password of your Lakeside account.") {
		t.Errorf("the person's own link opens with %q, want it to say they asked", strings.SplitN(message.Text, "\n", 2)[0])
	}

	// An address with no account, and one an account holds unconfirmed, answer as pat's
	// did and are mailed nothing.
	a.seedWithEmail(ctx, t, "sam", "sam@lakeside.edu")
	before := len(a.mailed())
	for _, email := range []string{"nobody@lakeside.edu", "sam@lakeside.edu"} {
		if body := b.expect(ctx, http.StatusOK, http.MethodPost, "/reset", map[string]string{"email": email}); strings.TrimSpace(string(body)) != "{}" {
			t.Errorf("the request for %s answers %s, want {} as for an address with an account", email, body)
		}
	}
	if got := len(a.mailed()); got != before {
		t.Errorf("the requests for an address with no account and an unconfirmed one mailed %d messages, want none", got-before)
	}

	// The person follows the link: it opens to the name they sign in under, and the
	// password they choose replaces the old one.
	if body := b.expect(ctx, http.StatusOK, http.MethodPost, "/reset/open", map[string]string{"token": ownToken}); !strings.Contains(string(body), `"username":"pat"`) {
		t.Errorf("the opened link answers %s, want the username", body)
	}
	if status, _, body := a.browser(t).primed(ctx).postLogin(ctx, "pat", seedPassword); status != http.StatusOK {
		t.Fatalf("login with the old password after the link was opened = %d %s, want 200: opening it changes nothing", status, body)
	}
	b.expect(ctx, http.StatusBadRequest, http.MethodPost, "/reset/complete", map[string]string{"token": ownToken, "password": "short"})
	b.expect(ctx, http.StatusOK, http.MethodPost, "/reset/complete", map[string]string{"token": ownToken, "password": chosenPassword})
	b.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/whoami", nil)

	// The old password is refused, the session that was open is gone, and the new
	// password signs in.
	if status, _, _ := a.browser(t).primed(ctx).postLogin(ctx, "pat", seedPassword); status != http.StatusUnauthorized {
		t.Errorf("login with the old password after the link was used = %d, want 401", status)
	}
	open.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/whoami", nil)
	again := a.browser(t).primed(ctx)
	if status, _, body := again.postLogin(ctx, "pat", chosenPassword); status != http.StatusOK {
		t.Fatalf("login with the new password = %d %s, want 200", status, body)
	}
	signedInAs(ctx, t, again, "pat")

	// The link worked once, and the administrator's link that stood went with it: the
	// password the person chose stands.
	for name, token := range map[string]string{"the link that was used": ownToken, "the other link that stood": adminToken} {
		b.expect(ctx, http.StatusNotFound, http.MethodPost, "/reset/open", map[string]string{"token": token})
		b.expect(ctx, http.StatusNotFound, http.MethodPost, "/reset/complete", map[string]string{"token": token, "password": "another password"})
		if status, _, _ := a.browser(t).primed(ctx).postLogin(ctx, "pat", chosenPassword); status != http.StatusOK {
			t.Errorf("login with the chosen password after %s was tried again = %d, want 200", name, status)
		}
	}

	// Turned off again, the request is refused with no restart.
	a.setOwnReset(ctx, t, false)
	if a.offersOwnReset(ctx, b) {
		t.Error("the login page is offered a person's own reset after it was turned off, want none")
	}
	b.expect(ctx, http.StatusNotFound, http.MethodPost, "/reset", map[string]string{"email": "pat@lakeside.edu"})
}

func seamResetLimit[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	t.Helper()

	a := newResetApp(ctx, t, be)
	pat := a.seedConfirmed(ctx, t, "pat", "pat@lakeside.edu")
	a.setOwnReset(ctx, t, true)
	b := a.browser(t)
	b.prime(ctx)

	// Five to one address, asked for by the person and by two administrators.
	for range 3 {
		b.expect(ctx, http.StatusOK, http.MethodPost, "/reset", map[string]string{"email": "pat@lakeside.edu"})
	}
	if err := a.api.SendPasswordReset(ctx, pat, session.AskedByApplication()); err != nil {
		t.Fatalf("the fourth link error = %v", err)
	}
	if err := a.api.SendPasswordReset(ctx, pat, session.AskedByTenant("lakeside")); err != nil {
		t.Fatalf("the fifth link error = %v", err)
	}
	if got := len(a.mailed()); got != 5 {
		t.Fatalf("mailed messages = %d, want the 5 under the limit", got)
	}

	// The sixth is refused whoever asks, and nothing more is mailed.
	if status, body := b.do(ctx, http.MethodPost, "/reset", map[string]string{"email": "pat@lakeside.edu"}); status != http.StatusTooManyRequests || !strings.Contains(string(body), `"code":"too_many_emails"`) {
		t.Errorf("the person's sixth request = %d %s, want 429 too_many_emails", status, body)
	}
	err := a.api.SendPasswordReset(ctx, pat, session.AskedByApplication())
	if _, limited := sessioninfo.RetryAfterOf(err); !limited {
		t.Errorf("an administrator's sixth link error = %v, want the too_many_emails refusal with its wait", err)
	}
	// An address with no account meets the same limit, so the refusal says nothing of it.
	for range 5 {
		b.expect(ctx, http.StatusOK, http.MethodPost, "/reset", map[string]string{"email": "nobody@lakeside.edu"})
	}
	b.expect(ctx, http.StatusTooManyRequests, http.MethodPost, "/reset", map[string]string{"email": "nobody@lakeside.edu"})
	if got := len(a.mailed()); got != 5 {
		t.Errorf("mailed messages after the refusals = %d, want still 5", got)
	}

	// A link that is used resets the address: the next one is sent.
	_, token := a.lastLink(t, "pat@lakeside.edu", "reset")
	b.expect(ctx, http.StatusOK, http.MethodPost, "/reset/complete", map[string]string{"token": token, "password": chosenPassword})
	if err := a.api.SendPasswordReset(ctx, pat, session.AskedByApplication()); err != nil {
		t.Errorf("a link after one was used error = %v, want it sent: the used link reset the address's count", err)
	}
}

func seamAddressChange[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	t.Helper()

	a := newResetApp(ctx, t, be)
	pat := a.seedConfirmed(ctx, t, "pat", "pat@lakeside.edu")
	a.hooks.set(func(h *hooks[Tx]) { h.policy = requireEmailCode })

	if err := a.api.RequestAddressChange(ctx, pat, "pat.lee@lakeside.edu", session.AskedByApplication()); err != nil {
		t.Fatalf("RequestAddressChange() error = %v", err)
	}
	message, token := a.lastLink(t, "pat.lee@lakeside.edu", "address")
	if messages := a.mailed(); len(messages) != 1 || message.Subject != "Confirm your new email address for Lakeside" {
		t.Fatalf("mailed messages = %+v, want the one link at the new address and nothing at the old", messages)
	}

	// Nothing changed: the account holds the address it had, confirmed, and the code of
	// its sign-in still goes there.
	user, err := a.api.User(ctx, pat)
	if err != nil {
		t.Fatalf("User() error = %v", err)
	}
	if user.Email == nil || *user.Email != "pat@lakeside.edu" || !user.EmailVerified() {
		t.Fatalf("the account's address after the request = %v (confirmed %v), want pat@lakeside.edu still, confirmed", user.Email, user.EmailVerified())
	}
	b := a.browser(t)
	b.prime(ctx)
	if status, mfa := b.loginAs(ctx, "pat"); status != http.StatusOK || !mfa {
		t.Fatalf("password login = %d (mfaIsRequired %v), want 200 requiring MFA", status, mfa)
	}
	b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/send", nil)
	overtaken := a.lastCode(t, "pat@lakeside.edu")

	// The reader of the new address confirms, in a browser that is signed in to nothing.
	reader := a.browser(t).primed(ctx)
	if body := reader.expect(ctx, http.StatusOK, http.MethodPost, "/email/confirm", map[string]string{"token": token}); !strings.Contains(string(body), `"email":"pat.lee@lakeside.edu"`) {
		t.Errorf("the confirmation answers %s, want the new address", body)
	}
	user, err = a.api.User(ctx, pat)
	if err != nil {
		t.Fatalf("User() error = %v", err)
	}
	if user.Email == nil || *user.Email != "pat.lee@lakeside.edu" || !user.EmailVerified() {
		t.Errorf("the account's address after the confirmation = %v (confirmed %v), want pat.lee@lakeside.edu, confirmed", user.Email, user.EmailVerified())
	}
	messages := a.mailed()
	notice := messages[len(messages)-1]
	if notice.To != "pat@lakeside.edu" || notice.Subject != "The email address of your Lakeside account changed" || strings.Contains(notice.Text, "https://") {
		t.Errorf("the last message = %+v, want the notice at the old address, with no link in it", notice)
	}
	reader.expect(ctx, http.StatusNotFound, http.MethodPost, "/email/confirm", map[string]string{"token": token})

	// The code mailed to the address the account held before is dead with it: whoever
	// reads the old mailbox does not finish the sign-in, and the code does not put the
	// old address back.
	if status, body := b.do(ctx, http.MethodPost, "/pending/code/verify", map[string]any{"code": overtaken}); status != http.StatusUnauthorized || !strings.Contains(string(body), `"code":"code_expired"`) {
		t.Errorf("a code mailed to the address the account has replaced = %d %s, want 401 code_expired", status, body)
	}
	b.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/whoami", nil)
	if user, err = a.api.User(ctx, pat); err != nil {
		t.Fatalf("User() error = %v", err)
	}
	if user.Email == nil || *user.Email != "pat.lee@lakeside.edu" || !user.EmailVerified() {
		t.Errorf("the account's address after the old code was tried = %v (confirmed %v), want pat.lee@lakeside.edu still, confirmed", user.Email, user.EmailVerified())
	}

	// An address mistyped and asked for again: the first link is withdrawn, so the
	// reader of the mistyped mailbox confirms nothing, and the second link confirms.
	if err := a.api.RequestAddressChange(ctx, pat, "p.le@lakeside.edu", session.AskedByApplication()); err != nil {
		t.Fatalf("RequestAddressChange() error = %v", err)
	}
	_, mistyped := a.lastLink(t, "p.le@lakeside.edu", "address")
	if err := a.api.RequestAddressChange(ctx, pat, "p.lee@lakeside.edu", session.AskedByApplication()); err != nil {
		t.Fatalf("RequestAddressChange() error = %v", err)
	}
	_, right := a.lastLink(t, "p.lee@lakeside.edu", "address")
	reader.expect(ctx, http.StatusNotFound, http.MethodPost, "/email/confirm", map[string]string{"token": mistyped})
	reader.expect(ctx, http.StatusOK, http.MethodPost, "/email/confirm", map[string]string{"token": right})

	// A reset link mailed for one password does not reset the next: an operator sets
	// another, and the link is dead.
	if err := a.api.SendPasswordReset(ctx, pat, session.AskedByApplication()); err != nil {
		t.Fatalf("SendPasswordReset() error = %v", err)
	}
	_, reset := a.lastLink(t, "p.lee@lakeside.edu", "reset")
	reader.expect(ctx, http.StatusOK, http.MethodPost, "/reset/open", map[string]string{"token": reset})
	if err := a.api.SetSessionUserPassword(ctx, pat, seedPassword); err != nil {
		t.Fatalf("SetSessionUserPassword() error = %v", err)
	}
	reader.expect(ctx, http.StatusNotFound, http.MethodPost, "/reset/open", map[string]string{"token": reset})

	// From now on the code goes to the new address.
	c := a.browser(t).primed(ctx)
	if status, mfa := c.loginAs(ctx, "pat"); status != http.StatusOK || !mfa {
		t.Fatalf("password login = %d (mfaIsRequired %v), want 200 requiring MFA", status, mfa)
	}
	c.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/send", nil)
	a.lastCode(t, "p.lee@lakeside.edu")
}

func seamOwnAddressChange[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	t.Helper()

	a := newResetApp(ctx, t, be)
	pat := a.seedConfirmed(ctx, t, "pat", "pat@lakeside.edu")
	b := a.browser(t)
	b.prime(ctx)
	if status, _, body := b.postLogin(ctx, "pat", seedPassword); status != http.StatusOK {
		t.Fatalf("password login = %d %s, want 200", status, body)
	}

	b.expect(ctx, http.StatusOK, http.MethodPost, "/email", map[string]string{"email": "pat.lee@lakeside.edu"})
	_, token := a.lastLink(t, "pat.lee@lakeside.edu", "verify")
	if messages := a.mailed(); len(messages) != 1 {
		t.Fatalf("mailed messages after the request = %d, want the one link at the new address and nothing at the old", len(messages))
	}
	user, err := a.api.User(ctx, pat)
	if err != nil {
		t.Fatalf("User() error = %v", err)
	}
	if user.Email == nil || *user.Email != "pat@lakeside.edu" || !user.EmailVerified() {
		t.Fatalf("the account's address after the request = %v, want pat@lakeside.edu still, confirmed", user.Email)
	}

	b.expect(ctx, http.StatusOK, http.MethodPost, "/email/verify", map[string]string{"token": token})
	if email, verified := b.authenticatedEmail(ctx); email != "pat.lee@lakeside.edu" || !verified {
		t.Errorf("the session read after the confirmation = email %q verified %v, want the new address confirmed", email, verified)
	}
	messages := a.mailed()
	if notice := messages[len(messages)-1]; len(messages) != 2 || notice.To != "pat@lakeside.edu" || notice.Subject != "The email address of your Lakeside account changed" {
		t.Errorf("mailed messages after the confirmation = %+v, want the notice at the old address", messages)
	}
}
