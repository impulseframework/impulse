//go:build !skipAuth

// The window between the code a person enters and the write of the address it confirms,
// through the real handlers on both backends: an administrator's change of the address
// that lands in it stands, and the code is dead.

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
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/mail"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/session/signin"
)

// windowStore is an application's session store that embeds the library's, and plays an
// administrator's move inside a sign-in's completion: right before the write of the
// address the code confirms, or right after the code is consumed. The write is hooked
// under both of its names (the write the completion takes only while the account still
// holds what it held, and the write with no condition), so a completion that takes
// either meets the move at the same moment. Each move is played once.
type windowStore[Tx any] struct {
	*sessionstorage.Auth[Tx]

	mu           sync.Mutex
	beforeWrite  func()
	afterConsume func()
	played       int
}

// beforeTheWrite arms move to be played right before the next write of a confirmed
// address.
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

// VerifyUserEmailIfHeld is the library's write, with the move armed before it played
// first.
func (s *windowStore[Tx]) VerifyUserEmailIfHeld(ctx context.Context, id impulse.UUID, held *string, email string) error {
	s.play(&s.beforeWrite)
	if err := s.Auth.VerifyUserEmailIfHeld(ctx, id, held, email); err != nil {
		return errors.Wrap(err, "sessionstorage.Auth.VerifyUserEmailIfHeld()")
	}

	return nil
}

// VerifyUserEmail is the library's write, with the move armed before it played first.
func (s *windowStore[Tx]) VerifyUserEmail(ctx context.Context, id impulse.UUID, email string) error {
	s.play(&s.beforeWrite)
	if err := s.Auth.VerifyUserEmail(ctx, id, email); err != nil {
		return errors.Wrap(err, "sessionstorage.Auth.VerifyUserEmail()")
	}

	return nil
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
		verified: window.Auth.VerifyUserEmail, setEmail: window.SetUserEmail,
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
func (a *codeWindowApp[Tx]) holds(ctx context.Context, t *testing.T, userID impulse.UUID, want *string) {
	t.Helper()

	user, err := a.api.User(ctx, userID)
	if err != nil {
		t.Fatalf("User(%s) error = %v", userID, err)
	}
	switch {
	case want == nil && user.Email != nil:
		t.Errorf("account %s holds %q, want no address: the administrator's change stands", userID, *user.Email)
	case want != nil && (user.Email == nil || *user.Email != *want):
		t.Errorf("account %s holds %v, want %q: the administrator's change stands", userID, user.Email, *want)
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
