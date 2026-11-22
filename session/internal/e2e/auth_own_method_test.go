//go:build !skipAuth

// An application's own sign-in method and its own reports of a sign-in, through the real
// sign-in flow on both backends: what the library records as its own steps and its own
// sign-ins, no application's method and no application's report records.

package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/mail"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// ownMethodApp is an application whose one sign-in method is its own: a redirect method
// registered through signin.Register under the name "partner", as the method "partner",
// invite only under the standard rule. Its round trip verifies whatever identity the
// scenario says the method's code returns (verifies), so a scenario plays a method whose
// code names its identities badly. The application also completes a pending sign-in
// with no step of its own (POST /pending/complete) and reports a sign-in it says a
// person made (POST /reported).
type ownMethodApp[Tx any] struct {
	*authApp[Tx]

	mu       sync.Mutex
	identity *sessioninfo.Identity
}

// verifies sets the identity the method's round trip verifies from here on.
func (a *ownMethodApp[Tx]) verifies(identity *sessioninfo.Identity) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.identity = identity
}

// complete is the method's Complete: the round trip verified the scenario's identity.
func (a *ownMethodApp[Tx]) complete(context.Context, http.ResponseWriter, *http.Request) (*signin.Verified, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	return &signin.Verified{Identity: a.identity, ReturnURL: "/home"}, nil
}

// reportedSignIn is the body of POST /reported: the user the application says signed
// in, and the method and connection it reports the sign-in under.
type reportedSignIn struct {
	User       string `json:"user"`
	Method     string `json:"method"`
	Connection string `json:"connection"`
}

func newOwnMethodApp[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) *ownMethodApp[Tx] {
	t.Helper()

	h := &hooks[Tx]{rows: be.rows}
	db, store := be.newStore(ctx, t)
	recorder := &mail.Recorder{}
	auth, err := session.NewAuth[appSessionData, appUserData](withCustomData(t, store, h), cookieKey, session.NoSuperAdministrators(),
		session.MailSettings{Sender: recorder, Application: "Lakeside", LoginURL: "https://app.example/login"},
		session.WithIdentityLinked(h.identityLinked))
	if err != nil {
		t.Fatalf("session.NewAuth() error = %v", err)
	}
	auth.SetSignInPolicy(h.signInPolicy)

	a := &ownMethodApp[Tx]{}
	partner := signin.Register[Tx](auth, &signin.Registration[Tx]{
		Name: "partner", Method: "partner",
		Build: func(signin.StateCookies) (*signin.Method, error) {
			return &signin.Method{Name: "partner", LoginURL: func() string { return "/login" }, Complete: a.complete}, nil
		},
	})

	api := auth.API()
	r := chi.NewRouter()
	mountAuth(r, auth, func(r chi.Router) {
		r.Get("/partner/callback", partner.Callback)
		r.Post("/pending/complete", func(w http.ResponseWriter, r *http.Request) {
			if _, err := api.CompletePending(r.Context(), w); err != nil {
				_ = httpio.NewEncoder(w).ClientMessage(r.Context(), err)

				return
			}
			w.WriteHeader(http.StatusNoContent)
		})
		r.Post("/reported", func(w http.ResponseWriter, r *http.Request) {
			var body reportedSignIn
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)

				return
			}
			events := []sessioninfo.AuthEvent{{Method: sessioninfo.AuthMethod(body.Method), Connection: body.Connection}}
			if _, err := api.StartAuthenticatedSession(r.Context(), w, impulse.Must(impulse.UUIDFromString(body.User)), events); err != nil {
				_ = httpio.NewEncoder(w).ClientMessage(r.Context(), err)

				return
			}
			w.WriteHeader(http.StatusNoContent)
		})
	})
	server := httptest.NewTLSServer(r)
	t.Cleanup(server.Close)
	a.authApp = &authApp[Tx]{db: db, rows: be.rows, server: server, api: api, hooks: h, mail: recorder, verified: store.VerifyUserEmail, setEmail: store.SetUserEmail}

	return a
}

// nothingOfTheirs fails unless the sign-in in b left the account as it was: no session
// in the browser, no identity linked to userID, and nothing mailed.
func (a *ownMethodApp[Tx]) nothingOfTheirs(ctx context.Context, t *testing.T, b *browser, userID impulse.UUID) {
	t.Helper()

	if status, body := b.do(ctx, http.MethodGet, "/whoami", nil); status != http.StatusUnauthorized {
		t.Errorf("the browser's session = %d %s, want none: nobody entered a code mailed to the account's address", status, body)
	}
	if links := a.links(ctx, t, userID.String()); len(links) != 0 {
		t.Errorf("identity links = %d (the first under the method %q), want none: nothing but the name ties the identity to the account", len(links), links[0].Method)
	}
	if sent := a.mail.Messages(); len(sent) != 0 {
		t.Errorf("mail sent = %d messages, want none", len(sent))
	}
}

// TestAuthSeams_OwnMethod runs the scenarios of an application's own method and its
// own reports on each backend.
func TestAuthSeams_OwnMethod(t *testing.T) {
	t.Parallel()

	t.Run(postgresBackend.name, func(t *testing.T) {
		t.Parallel()
		runOwnMethodSeams(t, postgresBackend)
	})
	t.Run(spannerBackend.name, func(t *testing.T) {
		t.Parallel()
		runOwnMethodSeams(t, spannerBackend)
	})
}

func runOwnMethodSeams[Tx any](t *testing.T, be backend[Tx]) {
	tests := []struct {
		name string
		run  func(ctx context.Context, t *testing.T, be backend[Tx])
	}{
		{
			name: "an application's own method whose identity is under the mailed code's name and attested by the provider signs nobody in: the account under the sign-in name gains no link and no session, and nothing is mailed",
			run:  seamOwnMethodAttestedUnderTheCodesName[Tx],
		},
		{
			name: "an application's own method whose identity is under the mailed code's name is not held either, so a completion with no step of the application's own links nobody; under the method's own name the first link waits for the code mailed to the account's address",
			run:  seamOwnMethodHeldUnderTheCodesName[Tx],
		},
		{
			name: "an application reports no sign-in the library runs: a session reported as a directory's under a tenant's id, a password's or a registered method's is refused, and one under a name of the application's own starts",
			run:  seamOwnReportOfADirectorySignIn[Tx],
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tt.run(t.Context(), t, be)
		})
	}
}

// seamOwnMethodAttestedUnderTheCodesName is the scenario of a method whose code gives
// its identities the mailed code's name, for a sign-in the provider attests: the
// sign-in's events would be the mailed code's and the attestation, which the rule that
// holds a first link made on a name alone reads as the code entered.
func seamOwnMethodAttestedUnderTheCodesName[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newOwnMethodApp(ctx, t, be)
	alice := a.createUser(ctx, t, "alice@corp.example")
	a.confirmedAddress(ctx, t, alice, "alice@corp.example")

	// A person whose account at the partner's provider carries the name of alice's
	// account signs in there, with the provider's second factor.
	a.verifies(&sessioninfo.Identity{
		Method: sessioninfo.MethodEmailCode, Connection: "partner", Subject: "s-1", SignInName: "alice@corp.example",
		Attestation: sessioninfo.AttestationOf([]string{"mfa"}, false), IDPAMR: []string{"mfa"},
	})
	b := a.browser(t)
	b.prime(ctx)
	assertRedirect(t, b.location(ctx, "/partner/callback?code=c&state=s"), "/login", url.Values{"code": {"internal_error"}})
	a.nothingOfTheirs(ctx, t, b, alice)
}

// seamOwnMethodHeldUnderTheCodesName is the same method for a sign-in nothing attests.
// Were it held, its row would store the mailed code's event as the sign-in's own, and a
// completion with no step-up would run the rules again with that event.
func seamOwnMethodHeldUnderTheCodesName[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newOwnMethodApp(ctx, t, be)
	alice := a.createUser(ctx, t, "alice@corp.example")
	a.confirmedAddress(ctx, t, alice, "alice@corp.example")

	a.verifies(&sessioninfo.Identity{Method: sessioninfo.MethodEmailCode, Connection: "partner", Subject: "s-1", SignInName: "alice@corp.example"})
	b := a.browser(t)
	b.prime(ctx)
	assertRedirect(t, b.location(ctx, "/partner/callback?code=c&state=s"), "/login", url.Values{"code": {"internal_error"}})
	if status, body := b.do(ctx, http.MethodGet, "/pending", nil); status == http.StatusOK {
		t.Errorf("the sign-in is held, %s, want nothing held: its row would store the mailed code's event", body)
	}
	if status, body := b.do(ctx, http.MethodPost, "/pending/complete", nil); status == http.StatusNoContent {
		t.Errorf("a completion with no step of the application's own = %d %s, want nothing to complete", status, body)
	}
	a.nothingOfTheirs(ctx, t, b, alice)

	// The method's code is put right: its identities are under its own name. The name
	// still finds alice's account, and nothing but the name ties the two, so the
	// sign-in waits for the code mailed to the account's address, which a completion
	// with no step of the application's own does not answer.
	a.verifies(&sessioninfo.Identity{Method: "partner", Connection: "partner", Subject: "s-1", SignInName: "alice@corp.example"})
	own := a.browser(t)
	own.prime(ctx)
	assertRedirect(t, own.location(ctx, "/partner/callback?code=c&state=s"), "/login", url.Values{"pending": {"mfa"}, "returnUrl": {"/home"}})
	if status, body := own.do(ctx, http.MethodPost, "/pending/complete", nil); status == http.StatusNoContent {
		t.Errorf("a completion with no step of the application's own = %d %s, want the sign-in to wait for the code", status, body)
	}
	a.nothingOfTheirs(ctx, t, own, alice)
	a.answerAddressCode(ctx, own, "alice@corp.example")
	if v := signedInAs(ctx, t, own, "alice@corp.example"); v.UserID != alice.String() {
		t.Errorf("session user = %s, want the account under the name, %s", v.UserID, alice)
	} else {
		assertEventsSeen(t, v, "partner:partner", "email-code")
	}
	if links := a.links(ctx, t, alice.String()); len(links) != 1 || links[0].Method != "partner" || links[0].Subject != "s-1" {
		t.Errorf("identity links after the code = %d, want the one the code admitted, under the method's own name", len(links))
	}
}

// seamOwnReportOfADirectorySignIn is the scenario of an application that reports a
// person's sign-in itself (StartAuthenticatedSession). A session's first auth event
// says how the person signed in, and a tenant's "directory only" condition reads an
// event under a directory's method and the tenant's connection as a sign-in through
// that directory.
func seamOwnReportOfADirectorySignIn[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newOwnMethodApp(ctx, t, be)
	pat := a.createUser(ctx, t, "pat")
	b := a.browser(t)
	b.prime(ctx)

	for _, reported := range []reportedSignIn{
		{Method: string(sessioninfo.MethodAzure), Connection: azureTenant},
		{Method: string(sessioninfo.MethodGoogle), Connection: hostedDomain},
		{Method: string(sessioninfo.MethodWorkOS), Connection: "conn_1"},
		{Method: string(sessioninfo.MethodOIDC), Connection: "https://provider.example"},
		{Method: string(sessioninfo.MethodPassword)},
		{Method: "partner", Connection: "partner"},
	} {
		reported.User = pat.String()
		if status, body := b.do(ctx, http.MethodPost, "/reported", reported); status != http.StatusBadRequest || !strings.Contains(string(body), "a sign-in the library runs") {
			t.Errorf("a sign-in reported under %q = %d %s, want 400 saying the library runs that sign-in", reported.Method, status, body)
		}
		if status, body := b.do(ctx, http.MethodGet, "/whoami", nil); status != http.StatusUnauthorized {
			t.Fatalf("after a sign-in reported under %q the browser's session = %d %s, want none: the session says the person signed in through %q %q, and the library ran no such sign-in",
				reported.Method, status, body, reported.Method, reported.Connection)
		}
	}

	// A name of the application's own says nothing about a directory, whatever
	// connection it is reported under.
	b.expect(ctx, http.StatusNoContent, http.MethodPost, "/reported", reportedSignIn{User: pat.String(), Method: "kiosk", Connection: azureTenant})
	assertEventsSeen(t, signedInAs(ctx, t, b, "pat"), "kiosk:"+azureTenant)
}
