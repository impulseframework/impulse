//go:build !skipAuth

// The password method's sign-up through the real handlers on both backends: whether a
// person with no account may sign up is what an administrator chose for the method
// while the application runs, read as each request arrives and again in the transaction
// that makes the account.

package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/mail"
	"github.com/impulseframework/impulse/session/sessionstorage"
)

// signUpApp is an application with the password method and mail: the sign-up routes
// are mounted, as they are in every such application, and the login page's list names
// them.
type signUpApp[Tx any] struct {
	*authApp[Tx]
	store *sessionstorage.Auth[Tx]
}

func newSignUpApp[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) *signUpApp[Tx] {
	t.Helper()

	h := &hooks[Tx]{rows: be.rows}
	db, store := be.newStore(ctx, t)
	recorder := &mail.Recorder{}
	auth, err := session.NewAuth[appSessionData, appUserData](withCustomData(t, store, h), cookieKey, session.NoSuperAdministrators(),
		session.MailSettings{Sender: recorder, Application: "Lakeside", LoginURL: "https://app.example/login"})
	if err != nil {
		t.Fatalf("session.NewAuth() error = %v", err)
	}
	auth.SetSignInPolicy(h.signInPolicy)
	// The default role of the open method is written as the new user's membership row,
	// in the transaction that makes the account.
	auth.SetSignUpSeated(func(ctx context.Context, tx Tx, userID impulse.UUID, _, _, role string) (func(ctx context.Context), error) {
		return nil, be.rows.addMember(ctx, tx, userID, role)
	})
	password := session.PasswordSignIn(auth)

	r := chi.NewRouter()
	mountAuth(r, auth, func(r chi.Router) {
		r.With(auth.ValidateXSRFToken).Post("/login", password.Login())
		r.With(auth.ValidateXSRFToken).Post("/password", password.ChangeUserPassword())
		r.With(auth.ValidateXSRFToken).Post("/sign-up", auth.SignUp().Start())
		r.With(auth.ValidateXSRFToken).Post("/sign-up/open", auth.SignUp().Open())
		r.With(auth.ValidateXSRFToken).Post("/sign-up/complete", auth.SignUp().Complete())
		r.Get("/methods", auth.Methods(&session.MethodList{
			Methods:          []session.AdvertisedMethod{{Name: "password", Label: "Password", Path: "/login"}},
			ChangePassword:   "/password",
			PasswordStrength: "/password/strength",
			SignUp:           session.SignUpRoutes{Start: "/sign-up", Open: "/sign-up/open", Complete: "/sign-up/complete"},
		}))
	})
	server := httptest.NewTLSServer(r)
	t.Cleanup(server.Close)

	return &signUpApp[Tx]{
		authApp: &authApp[Tx]{db: db, rows: be.rows, server: server, api: auth.API(), hooks: h, mail: recorder},
		store:   store,
	}
}

// offersSignUp reports whether the login page's list names the sign-up routes now.
func (a *signUpApp[Tx]) offersSignUp(ctx context.Context, b *browser) bool {
	b.t.Helper()

	var list struct {
		SignUp *struct {
			Start string `json:"start"`
		} `json:"signUp"`
	}
	if err := json.Unmarshal(b.expect(ctx, http.StatusOK, http.MethodGet, "/methods", nil), &list); err != nil {
		b.t.Fatalf("json.Unmarshal() error = %v", err)
	}

	return list.SignUp != nil && list.SignUp.Start == "/sign-up"
}

// lastSignUpToken is the token of the sign-up link the recorder received last.
func lastSignUpToken(t *testing.T, recorder *mail.Recorder) string {
	t.Helper()

	messages := recorder.Messages()
	if len(messages) == 0 {
		t.Fatal("mailed messages = 0, want a sign-up link")
	}
	m := regexp.MustCompile(`https://app\.example/login#signUp=([A-Za-z0-9_-]+)`).FindStringSubmatch(messages[len(messages)-1].Text)
	if m == nil {
		t.Fatalf("no sign-up link in the message %q: a message's text holds its link and is not printed", messages[len(messages)-1].Subject)
	}

	return m[1]
}

// TestAuthSeams_SignUp runs the sign-up scenario on each backend.
func TestAuthSeams_SignUp(t *testing.T) {
	t.Parallel()

	t.Run(postgresBackend.name, func(t *testing.T) {
		t.Parallel()
		seamSignUpChoice(t.Context(), t, postgresBackend)
	})
	t.Run(spannerBackend.name, func(t *testing.T) {
		t.Parallel()
		seamSignUpChoice(t.Context(), t, spannerBackend)
	})
}

// seamSignUpChoice: until an administrator opens the password method nobody signs up,
// and nothing is mailed or written; once it is open a person signs up and holds the
// method's default role from their first session; and a link mailed while the method
// was open makes nothing once it is invite only again, and is still good when the
// method is opened once more.
func seamSignUpChoice[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	t.Helper()

	a := newSignUpApp(ctx, t, be)

	// No row: the method is invite only. The page is not told the routes, the routes
	// answer 404, and no mail goes out.
	b := a.browser(t)
	b.prime(ctx)
	if a.offersSignUp(ctx, b) {
		t.Error("the login page is offered sign-up before anyone opened the method, want none")
	}
	b.expect(ctx, http.StatusNotFound, http.MethodPost, "/sign-up", map[string]string{"email": "pat@lakeside.edu"})
	b.expect(ctx, http.StatusNotFound, http.MethodPost, "/sign-up/complete", map[string]string{"token": "none", "password": seedPassword, "username": "pat"})
	if messages := a.mail.Messages(); len(messages) != 0 {
		t.Fatalf("mailed messages = %d, want none while the method is invite only", len(messages))
	}

	// An administrator opens it with the default role Member: the page is told the
	// routes, and a person signs up and holds the role in their first session.
	openMethod(ctx, t, a.store, a.api, "password", "Member")
	if !a.offersSignUp(ctx, b) {
		t.Error("the login page is not offered sign-up once the method is open, want the routes")
	}
	b.expect(ctx, http.StatusOK, http.MethodPost, "/sign-up", map[string]string{"email": "pat@lakeside.edu"})
	token := lastSignUpToken(t, a.mail)
	b.expect(ctx, http.StatusOK, http.MethodPost, "/sign-up/open", map[string]string{"token": token})
	b.expect(ctx, http.StatusOK, http.MethodPost, "/sign-up/complete", map[string]string{"token": token, "password": seedPassword, "username": "pat"})
	signedInAs(ctx, t, b, "pat")
	if got := b.sessionData(ctx).Partner; got != "Member" {
		t.Errorf("the new account holds %q, want the method's default role Member", got)
	}
	// The link made one account.
	a.browser(t).primed(ctx).expect(ctx, http.StatusNotFound, http.MethodPost, "/sign-up/complete", map[string]string{"token": token, "password": seedPassword, "username": "pat2"})

	// A second person starts while the method is open, and the administrator makes it
	// invite only before they finish: the link they hold makes nothing.
	late := a.browser(t)
	late.prime(ctx)
	late.expect(ctx, http.StatusOK, http.MethodPost, "/sign-up", map[string]string{"email": "sam@lakeside.edu"})
	lateToken := lastSignUpToken(t, a.mail)
	closeMethod(ctx, t, a.store, "password")
	if a.offersSignUp(ctx, late) {
		t.Error("the login page is offered sign-up after the method was made invite only, want none")
	}
	late.expect(ctx, http.StatusNotFound, http.MethodPost, "/sign-up/open", map[string]string{"token": lateToken})
	late.expect(ctx, http.StatusNotFound, http.MethodPost, "/sign-up/complete", map[string]string{"token": lateToken, "password": seedPassword, "username": "sam"})
	a.assertNoRow(ctx, t, late, "sam")

	// The person who signed up keeps the account.
	again := a.browser(t)
	again.prime(ctx)
	if status, _, body := again.postLogin(ctx, "pat", seedPassword); status != http.StatusOK {
		t.Errorf("password login of the person who signed up, after the method was made invite only = %d %s, want 200", status, body)
	}

	// The refusal spent nothing: once the method is open again the same link finishes.
	openMethod(ctx, t, a.store, a.api, "password", "")
	late.expect(ctx, http.StatusOK, http.MethodPost, "/sign-up/complete", map[string]string{"token": lateToken, "password": seedPassword, "username": "sam"})
	signedInAs(ctx, t, late, "sam")
	if got := late.sessionData(ctx).Partner; got != "" {
		t.Errorf("an account made through a method with no default role holds %q, want nothing", got)
	}
}
