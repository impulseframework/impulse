//go:build !skipAuth

// The window between the code a person enters and the write of the address it confirms,
// through the real handlers on both backends: an administrator's change of the address
// that lands in it stands, and the code is dead. A sign-in refused after its code was
// entered leaves the account's address as it was.

package e2e

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/mail"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/session/signin"
)

// windowStore is an application's session store that embeds the library's, and plays an
// administrator's move inside a sign-in's completion: right before the write of the
// address the code confirms, which is made inside SignIn, in the transaction that
// inserts the session, so the move is played as SignIn starts; or right after the code
// is consumed. Each move is played once.
type windowStore[Tx any] struct {
	*sessionstorage.Auth[Tx]

	mu           sync.Mutex
	beforeWrite  func()
	afterConsume func()
	played       int
}

// beforeTheWrite arms move to be played as the next sign-in starts, right before the
// write of the address its code confirms.
func (s *windowStore[Tx]) beforeTheWrite(move func()) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.beforeWrite = move
}

// afterTheConsume arms move to be played right after the next code is consumed.
func (s *windowStore[Tx]) afterTheConsume(move func()) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.afterConsume = move
}

// play plays the move armed at at, if any, and disarms it.
func (s *windowStore[Tx]) play(at *func()) {
	s.mu.Lock()
	move := *at
	*at = nil
	if move != nil {
		s.played++
	}
	s.mu.Unlock()

	if move != nil {
		move()
	}
}

// assertPlayed fails unless every move armed was played: the completion reached the call
// the move was armed at.
func (s *windowStore[Tx]) assertPlayed(t *testing.T, want int) {
	t.Helper()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.played != want || s.beforeWrite != nil || s.afterConsume != nil {
		t.Errorf("the administrator's moves played = %d, want %d: the completion did not reach the call the move was armed at", s.played, want)
	}
}

// SignIn is the library's sign-in, which writes the address the code confirms, with the
// move armed before the write played first.
func (s *windowStore[Tx]) SignIn(ctx context.Context, req *sessioninfo.NewSessionRequest, rules sessionstorage.SignInRules[Tx]) (impulse.UUID, error) {
	s.play(&s.beforeWrite)
	id, err := s.Auth.SignIn(ctx, req, rules)
	if err != nil {
		return impulse.NilUUID, errors.Wrap(err, "sessionstorage.Auth.SignIn()")
	}

	return id, nil
}

// ConsumeFactorCode is the library's consume, with the move armed after it played once
// the code is consumed.
func (s *windowStore[Tx]) ConsumeFactorCode(ctx context.Context, id impulse.UUID) error {
	if err := s.Auth.ConsumeFactorCode(ctx, id); err != nil {
		return errors.Wrap(err, "sessionstorage.Auth.ConsumeFactorCode()")
	}
	s.play(&s.afterConsume)

	return nil
}

// codeWindowApp is an application over a windowStore with two sign-in methods: the
// password, and a redirect method of its own registered through signin.Register under
// the name "partner", invite only under the standard rule, whose round trip verifies
// the identity the scenario sets (verifies).
type codeWindowApp[Tx any] struct {
	*authApp[Tx]

	store *windowStore[Tx]

	mu       sync.Mutex
	identity *sessioninfo.Identity
}

// verifies sets the identity the method's round trip verifies from here on.
func (a *codeWindowApp[Tx]) verifies(identity *sessioninfo.Identity) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.identity = identity
}

// complete is the method's Complete: the round trip verified the scenario's identity.
func (a *codeWindowApp[Tx]) complete(context.Context, http.ResponseWriter, *http.Request) (*signin.Verified, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	return &signin.Verified{Identity: a.identity, ReturnURL: "/home"}, nil
}

func newCodeWindowApp[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) *codeWindowApp[Tx] {
	t.Helper()

	h := &hooks[Tx]{rows: be.rows}
	db, store := be.newStore(ctx, t)
	window := &windowStore[Tx]{Auth: withCustomData(t, store, h)}
	recorder := &mail.Recorder{}
	auth, err := session.NewAuth[appSessionData, appUserData](window, cookieKey, session.NoSuperAdministrators(),
		session.MailSettings{Sender: recorder, Application: "Lakeside", LoginURL: "https://app.example/login"},
		session.WithIdentityLinked(h.identityLinked))
	if err != nil {
		t.Fatalf("session.NewAuth() error = %v", err)
	}
	auth.SetSignInPolicy(h.signInPolicy)

	a := &codeWindowApp[Tx]{store: window}
	password := session.PasswordSignIn(auth)
	partner := signin.Register[Tx](auth, &signin.Registration[Tx]{
		Name: "partner", Method: "partner",
		Build: func(signin.StateCookies) (*signin.Method, error) {
			return &signin.Method{Name: "partner", LoginURL: func() string { return "/login" }, Complete: a.complete}, nil
		},
	})

	r := chi.NewRouter()
	mountAuth(r, auth, func(r chi.Router) {
		r.With(auth.ValidateXSRFToken).Post("/login", password.Login())
		r.Get("/partner/callback", partner.Callback)
	})
	server := httptest.NewTLSServer(r)
	t.Cleanup(server.Close)
	a.authApp = &authApp[Tx]{
		db: db, rows: be.rows, server: server, api: auth.API(), hooks: h, mail: recorder,
		verified: window.VerifyUserEmail, setEmail: window.SetUserEmail,
	}

	return a
}

// TestAuthSeams_CodeAddressWindow runs the scenarios of an administrator's change that
// lands while a mailed code is being entered, on each backend.
func TestAuthSeams_CodeAddressWindow(t *testing.T) {
	t.Parallel()

	t.Run(postgresBackend.name, func(t *testing.T) {
		t.Parallel()
		runCodeWindowSeams(t, postgresBackend)
	})
	t.Run(spannerBackend.name, func(t *testing.T) {
		t.Parallel()
		runCodeWindowSeams(t, spannerBackend)
	})
}

func runCodeWindowSeams[Tx any](t *testing.T, be backend[Tx]) {
	tests := []struct {
		name string
		run  func(ctx context.Context, t *testing.T, be backend[Tx])
	}{
		{
			name: "an address an administrator moves to another account right before the write of a first link's code stays where they put it: the code is dead, and nobody is linked or signed in",
			run:  seamCodeWindowAddressMoved[Tx],
		},
		{
			name: "an address an administrator takes off the account right before the write of a first link's code is not written back: the code is dead, and nobody is linked or signed in",
			run:  seamCodeWindowAddressTakenOff[Tx],
		},
		{
			name: "an address an administrator takes off the account right after a password sign-in's code is consumed is not written back: the code is dead, and nobody is signed in",
			run:  seamCodeWindowTakenOffAfterTheConsume[Tx],
		},
	}
	// The sign-ins refused after their code was entered, for an account that holds no
	// address and is given one at the step, and for one that holds an address nobody
	// confirmed.
	for _, shape := range []struct {
		name    string
		holding bool
	}{
		{name: "an account given its address at the step"},
		{name: "an account that holds an address nobody confirmed", holding: true},
	} {
		tests = append(tests, []struct {
			name string
			run  func(ctx context.Context, t *testing.T, be backend[Tx])
		}{
			{
				name: shape.name + ": the password changed while the sign-in waited refuses the right code, and the account's address is as it was",
				run:  seamRefusedAfterTheCode(shape.holding, changesThePassword[Tx], ""),
			},
			{
				name: shape.name + ": the user disabled while the sign-in waited is refused the right code, and the account's address is as it was",
				run:  seamRefusedAfterTheCode(shape.holding, disablesTheUser[Tx], "user_disabled"),
			},
			{
				name: shape.name + ": the policy that denies the completion refuses the right code, and the account's address is as it was",
				run:  seamRefusedAfterTheCode(shape.holding, deniesTheCompletion[Tx], ""),
			},
			{
				name: shape.name + ": a tenant switched to directory only while a member's sign-in waited refuses the right code with directory_only, and the account's address is as it was",
				run:  seamDirectoryOnlyAfterTheCode[Tx](shape.holding),
			},
		}...)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tt.run(t.Context(), t, be)
		})
	}
}

// waitOnTheName has a person whose identity at the partner's provider carries the name
// of pat's account sign in there in b: nothing but the name ties the two, so the sign-in
// waits for the code mailed to the address the account holds, which is asked for and
// returned.
func (a *codeWindowApp[Tx]) waitOnTheName(ctx context.Context, t *testing.T, b *browser, address string) string {
	t.Helper()

	a.verifies(&sessioninfo.Identity{Method: "partner", Connection: "partner", Subject: "s-1", SignInName: "pat@lakeside.edu"})
	b.prime(ctx)
	assertRedirect(t, b.location(ctx, "/partner/callback?code=c&state=s"), "/login", url.Values{"pending": {"mfa"}, "returnUrl": {"/home"}})
	b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/send", nil)

	return a.lastCode(t, address)
}

// deadCode fails unless entering code in b is refused as a code that expired, with no
// session in the browser.
func deadCode(ctx context.Context, t *testing.T, b *browser, code string) {
	t.Helper()

	if status, body := b.do(ctx, http.MethodPost, "/pending/code/verify", map[string]any{"code": code}); status != http.StatusUnauthorized || !strings.Contains(string(body), `"code":"code_expired"`) {
		t.Errorf("verify of the code while the administrator changed the address = %d %s, want 401 code_expired", status, body)
	}
	if status, body := b.do(ctx, http.MethodGet, "/whoami", nil); status != http.StatusUnauthorized {
		t.Errorf("the browser's session = %d %s, want none", status, body)
	}
}

// holds fails unless userID's account holds want (nil for none), confirmed by nobody.
func (a *authApp[Tx]) holds(ctx context.Context, t *testing.T, userID impulse.UUID, want *string) {
	t.Helper()

	user, err := a.api.User(ctx, userID)
	if err != nil {
		t.Fatalf("User(%s) error = %v", userID, err)
	}
	switch {
	case want == nil && user.Email != nil:
		t.Errorf("account %s holds %q, want no address", userID, *user.Email)
	case want != nil && (user.Email == nil || *user.Email != *want):
		t.Errorf("account %s holds %v, want %q", userID, user.Email, *want)
	case user.EmailVerified():
		t.Errorf("account %s holds its address confirmed, want it confirmed by nobody", userID)
	}
}

// linksNothing fails unless userID's account has no identity linked.
func (a *codeWindowApp[Tx]) linksNothing(ctx context.Context, t *testing.T, userID impulse.UUID) {
	t.Helper()

	if links := a.links(ctx, t, userID.String()); len(links) != 0 {
		t.Errorf("identity links of account %s = %d, want none: nobody entered a live code", userID, len(links))
	}
}

// seamCodeWindowAddressMoved: pat's account, made ahead of its person, holds an address
// nobody has confirmed, and a first sign-in on the name alone waits for the code mailed
// to it. The code is entered, and right before its address is written an administrator
// moves the address to sam's account.
func seamCodeWindowAddressMoved[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newCodeWindowApp(ctx, t, be)
	const address = "pat@lakeside.edu"
	pat := a.createUserAt(ctx, t, "pat@lakeside.edu", address)
	sam := a.createUser(ctx, t, "sam@lakeside.edu")

	b := a.browser(t)
	code := a.waitOnTheName(ctx, t, b, address)
	a.store.beforeTheWrite(func() {
		if err := a.setEmail(ctx, pat, nil); err != nil {
			t.Errorf("SetUserEmail(pat, nil) error = %v", err)
		}
		moved := address
		if err := a.setEmail(ctx, sam, &moved); err != nil {
			t.Errorf("SetUserEmail(sam, %q) error = %v", address, err)
		}
	})

	deadCode(ctx, t, b, code)
	a.store.assertPlayed(t, 1)
	a.linksNothing(ctx, t, pat)
	a.holds(ctx, t, pat, nil)
	a.holds(ctx, t, sam, new(address))
}

// seamCodeWindowAddressTakenOff: the same wait, and right before the code's address is
// written an administrator takes it off pat's account.
func seamCodeWindowAddressTakenOff[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newCodeWindowApp(ctx, t, be)
	const address = "pat@lakeside.edu"
	pat := a.createUserAt(ctx, t, "pat@lakeside.edu", address)

	b := a.browser(t)
	code := a.waitOnTheName(ctx, t, b, address)
	a.store.beforeTheWrite(func() {
		if err := a.setEmail(ctx, pat, nil); err != nil {
			t.Errorf("SetUserEmail(pat, nil) error = %v", err)
		}
	})

	deadCode(ctx, t, b, code)
	a.store.assertPlayed(t, 1)
	a.linksNothing(ctx, t, pat)
	a.holds(ctx, t, pat, nil)
}

// seamCodeWindowTakenOffAfterTheConsume: pat proved the account's password, and the
// sign-in waits for the code mailed to the address the account holds, which nobody has
// confirmed. The code is entered, and right after it is consumed an administrator takes
// the address off the account: the account then reads as one that holds none, as an
// account whose person gives an address at the step does.
func seamCodeWindowTakenOffAfterTheConsume[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newCodeWindowApp(ctx, t, be)
	const address = "pat@lakeside.edu"
	pat := a.seedWithEmail(ctx, t, "pat", address)
	a.hooks.set(func(h *hooks[Tx]) { h.policy = requireEmailCode })

	b := a.browser(t)
	b.prime(ctx)
	if body := b.expect(ctx, http.StatusOK, http.MethodPost, "/login", map[string]string{"username": "pat", "password": seedPassword}); !strings.Contains(string(body), `"mfaIsRequired":true`) {
		t.Fatalf("password login = %s, want the sign-in held for the code", body)
	}
	b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/send", nil)
	code := a.lastCode(t, address)
	a.store.afterTheConsume(func() {
		if err := a.setEmail(ctx, pat, nil); err != nil {
			t.Errorf("SetUserEmail(pat, nil) error = %v", err)
		}
	})

	deadCode(ctx, t, b, code)
	a.store.assertPlayed(t, 1)
	a.holds(ctx, t, pat, nil)
}

// heldForTheCode signs username in by its password in b, under a policy that asks for
// the mailed code, and has the code mailed: to the address the account holds (holding),
// or to address, which the person gives at the step where the account holds none. The
// code is returned.
func (a *authApp[Tx]) heldForTheCode(ctx context.Context, t *testing.T, b *browser, username, address string, holding bool) string {
	t.Helper()

	b.prime(ctx)
	if body := b.expect(ctx, http.StatusOK, http.MethodPost, "/login", map[string]string{"username": username, "password": seedPassword}); !strings.Contains(string(body), `"mfaIsRequired":true`) {
		t.Fatalf("password login = %s, want the sign-in held for the code", body)
	}
	if holding {
		b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/send", nil)
	} else {
		b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/email", map[string]string{"email": address})
	}

	return a.lastCode(t, address)
}

// refusedTheRightCode fails unless entering code in b is refused, with wantCode where it
// is not "", and leaves no session in the browser. It returns the refusal's body.
func refusedTheRightCode(ctx context.Context, t *testing.T, b *browser, code, wantCode string) []byte {
	t.Helper()

	status, body := b.do(ctx, http.MethodPost, "/pending/code/verify", map[string]any{"code": code})
	if status < http.StatusBadRequest || (wantCode != "" && !strings.Contains(string(body), `"code":"`+wantCode+`"`)) {
		t.Errorf("verify of the right code = %d %s, want it refused %s", status, body, wantCode)
	}
	if status, body := b.do(ctx, http.MethodGet, "/whoami", nil); status != http.StatusUnauthorized {
		t.Errorf("the browser's session = %d %s, want none", status, body)
	}

	return body
}

// seamRefusedAfterTheCode: pat proved the account's password, and the sign-in waits for
// the mailed code, at the address the account holds, which nobody confirmed (holding),
// or at the address pat gives at the step, where the account holds none. move is made
// while the sign-in waits, once the code is mailed. The right code is then refused, no
// session starts, and the account's address is as it was before the sign-in: the
// address a code confirms is written with the session or not at all. The refusal is
// wantCode where it is not "", and never code_expired: the code was good, and the
// sign-in was refused for what move did.
func seamRefusedAfterTheCode[Tx any](holding bool, move func(ctx context.Context, t *testing.T, a *codeWindowApp[Tx], pat impulse.UUID), wantCode string) func(ctx context.Context, t *testing.T, be backend[Tx]) {
	return func(ctx context.Context, t *testing.T, be backend[Tx]) {
		a := newCodeWindowApp(ctx, t, be)
		const address = "pat@lakeside.edu"
		var pat impulse.UUID
		var before *string
		if holding {
			pat, before = a.seedWithEmail(ctx, t, "pat", address), new(address)
		} else {
			pat = a.seed(ctx, t, "pat")
		}
		a.hooks.set(func(h *hooks[Tx]) { h.policy = requireEmailCode })

		b := a.browser(t)
		code := a.heldForTheCode(ctx, t, b, "pat", address, holding)
		move(ctx, t, a, pat)

		if body := refusedTheRightCode(ctx, t, b, code, wantCode); strings.Contains(string(body), `"code":"code_expired"`) {
			t.Errorf("verify of the right code = %s, want a refusal other than code_expired", body)
		}
		a.holds(ctx, t, pat, before)
	}
}

// changesThePassword changes pat's password, as the person does from another session.
func changesThePassword[Tx any](ctx context.Context, t *testing.T, a *codeWindowApp[Tx], pat impulse.UUID) {
	t.Helper()

	req := &session.ChangeSessionUserPasswordRequest{OldPassword: seedPassword, NewPassword: "a-new-password-of-my-own"}
	if err := a.api.ChangeSessionUserPassword(ctx, httptest.NewRecorder(), pat, req); err != nil {
		t.Fatalf("ChangeSessionUserPassword(pat) error = %v", err)
	}
}

// disablesTheUser has an administrator disable pat.
func disablesTheUser[Tx any](ctx context.Context, t *testing.T, a *codeWindowApp[Tx], pat impulse.UUID) {
	t.Helper()

	if err := a.api.DeactivateSessionUser(ctx, pat); err != nil {
		t.Fatalf("DeactivateSessionUser(pat) error = %v", err)
	}
}

// deniesTheCompletion sets the application's sign-in policy to deny every sign-in.
func deniesTheCompletion[Tx any](_ context.Context, _ *testing.T, a *codeWindowApp[Tx], _ impulse.UUID) {
	a.hooks.set(func(h *hooks[Tx]) {
		h.policy = func(*sessioninfo.NewSessionRequest) *signin.Decision { return signin.Deny("") }
	})
}

// seamDirectoryOnlyAfterTheCode: in the tenanted application, kim, a member of a tenant
// that admits passwords, proved the account's password and waits for the mailed code
// the application's own rule asks for, at the address the account holds, which nobody
// confirmed (holding), or at the one kim gives at the step. The tenant is switched to
// directory only while the sign-in waits: the right code is refused with directory_only,
// and the account's address is as it was.
func seamDirectoryOnlyAfterTheCode[Tx any](holding bool) func(ctx context.Context, t *testing.T, be backend[Tx]) {
	return func(ctx context.Context, t *testing.T, be backend[Tx]) {
		a := newTenantApp(ctx, t, be)
		a.db.declareTenant(ctx, t, tenantLakeside, false, lakesideEntra)
		const kim = "kim@lakeside.edu"
		var id impulse.UUID
		var before *string
		if holding {
			id, before = a.seedWithEmail(ctx, t, kim, kim), new(kim)
		} else {
			id = a.seed(ctx, t, kim)
		}
		a.grant(ctx, t, kim, accesstypes.DomainPolicyScope(tenantLakeside), member...)
		a.hooks.set(func(h *hooks[Tx]) { h.policy = requireEmailCode })

		b := a.browser(t)
		code := a.heldForTheCode(ctx, t, b, kim, kim, holding)
		a.db.declareTenant(ctx, t, tenantLakeside, true, lakesideEntra)

		refusedTheRightCode(ctx, t, b, code, "directory_only")
		a.holds(ctx, t, id, before)
	}
}
