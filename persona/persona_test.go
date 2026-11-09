package persona_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/go-chi/chi/v5"
	"github.com/go-playground/errors/v5"
	"github.com/gofrs/uuid/v5"
	"github.com/impulseframework/impulse/db"
	"github.com/impulseframework/impulse/persona"
	"github.com/impulseframework/impulse/persona/spannerstore"
	"github.com/impulseframework/impulse/session/cookie"
	"github.com/impulseframework/impulse/session/oidc"
	"github.com/impulseframework/impulse/session/signin"
)

var container *db.SpannerContainer

func TestMain(m *testing.M) {
	ctx := context.Background()
	sc, err := db.NewSpannerContainer(ctx, "1.5.56")
	if err != nil {
		fmt.Println(err)
		os.Exit(2)
	}
	container = sc

	code := m.Run()

	if err := sc.Close(); err != nil {
		fmt.Println(err)
	}
	os.Exit(code)
}

const (
	// appCallback is the application's callback: a host the browser never reaches, so a
	// redirect to it ends the browser's walk.
	appCallbackHost = "lanterns-pr12.example.com"
	appCallback     = "https://" + appCallbackHost + "/api/user/persona/callback"
	deployer        = "lanterns-deploy@imp-tst.iam.gserviceaccount.com"
	clientID        = "lanterns-pr12"
)

// fakeHost is the host's records: applications by client ID root, and who may enter.
type fakeHost struct {
	mu   sync.Mutex
	apps map[string]*persona.Application
	deny map[string]bool
}

func (h *fakeHost) ApplicationByClient(_ context.Context, root string) (*persona.Application, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	app, ok := h.apps[root]
	if !ok {
		return nil, persona.ErrUnknownApplication
	}

	return app, nil
}

func (h *fakeHost) MayEnter(_ context.Context, actor *persona.Actor, app *persona.Application, _ *persona.Deployment) (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	return !h.deny[actor.ID+"@"+app.ID], nil
}

// fakeDeployers accepts tokens of the form deployer:<email> for the issuer.
type fakeDeployers struct {
	issuer string
}

func (d fakeDeployers) VerifyDeployer(_ context.Context, token, audience string) (string, error) {
	email, ok := strings.CutPrefix(token, "deployer:")
	if !ok || audience != d.issuer {
		return "", errors.New("not a deploy identity's token for this issuer")
	}

	return email, nil
}

// clock is the service's adjustable clock.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.t
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type actorKey struct{}

// people are the host's users, by the ID its session cookie carries.
var people = map[string]*persona.Actor{
	"u-pat": {ID: "u-pat", Name: "Pat Example", Email: "pat@example.com"},
	"u-sam": {ID: "u-sam", Name: "Sam Example", Email: "sam@example.com"},
}

// testHost is a host application with the persona sign-in mounted at /persona: a session
// cookie naming the person, a login page that sets it, and the service.
type testHost struct {
	server *httptest.Server
	issuer string
	svc    *persona.Service
	host   *fakeHost
	clock  *clock
	db     *spanner.Client
}

func newTestHost(ctx context.Context, t *testing.T) *testHost {
	t.Helper()

	sum := sha256.Sum256([]byte(t.Name()))
	database, err := container.CreateDatabase(ctx, "t"+hex.EncodeToString(sum[:])[:24])
	if err != nil {
		t.Fatalf("SpannerContainer.CreateDatabase() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("SpannerDB.Close() error = %v", err)
		}
	})
	if err := database.MigrateUp("file://schema/spanner/migrations"); err != nil {
		t.Fatalf("SpannerDB.MigrateUp() error = %v", err)
	}

	var handler http.Handler
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)

	host := &fakeHost{
		apps: map[string]*persona.Application{"lanterns": {ID: "app-1", Tenant: "lakeside", Name: "Lanterns", ClientID: "lanterns", DeployIdentities: []string{deployer}}},
		deny: map[string]bool{},
	}
	clk := &clock{t: time.Now()}
	th := &testHost{server: server, issuer: server.URL + "/persona", host: host, clock: clk, db: database.Client}
	svc, err := persona.New(ctx, host, spannerstore.New(database.Client), &persona.Config{
		Issuer: th.issuer, Actor: actorFromContext, LoginURL: "/login", Deployers: fakeDeployers{issuer: th.issuer}, AllowInsecureIssuer: true, Now: clk.Now,
	})
	if err != nil {
		t.Fatalf("persona.New() error = %v", err)
	}
	th.svc = svc

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if c, err := r.Cookie("actor"); err == nil {
				if a, ok := people[c.Value]; ok {
					r = r.WithContext(context.WithValue(r.Context(), actorKey{}, a))
				}
			}
			next.ServeHTTP(w, r)
		})
	})
	r.Get("/login", func(w http.ResponseWriter, r *http.Request) {
		as := r.URL.Query().Get("as")
		if _, ok := people[as]; !ok {
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, "<h1>Sign in to the host</h1>")

			return
		}
		http.SetCookie(w, &http.Cookie{Name: "actor", Value: as, Path: "/"})
		http.Redirect(w, r, r.URL.Query().Get("returnUrl"), http.StatusFound)
	})
	r.Mount("/persona", svc.Handler())
	handler = r

	return th
}

func actorFromContext(r *http.Request) (*persona.Actor, bool) {
	a, ok := r.Context().Value(actorKey{}).(*persona.Actor)

	return a, ok
}

// browser is a user agent with a cookie jar that stops at the application's callback.
type browser struct {
	t      *testing.T
	client *http.Client
}

func (th *testHost) browser(t *testing.T) *browser {
	t.Helper()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New() error = %v", err)
	}

	return &browser{t: t, client: &http.Client{Jar: jar, CheckRedirect: func(req *http.Request, _ []*http.Request) error {
		if req.URL.Host == appCallbackHost {
			return http.ErrUseLastResponse
		}

		return nil
	}}}
}

// get fetches a URL, following redirects within the host, and returns the final response
// with its body read.
func (b *browser) get(ctx context.Context, rawURL string) (resp *http.Response, body string) {
	b.t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		b.t.Fatalf("http.NewRequest() error = %v", err)
	}

	return b.do(req)
}

func (b *browser) post(ctx context.Context, rawURL string, form url.Values) (resp *http.Response, body string) {
	b.t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader(form.Encode()))
	if err != nil {
		b.t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	return b.do(req)
}

func (b *browser) do(req *http.Request) (resp *http.Response, body string) {
	b.t.Helper()

	resp, err := b.client.Do(req)
	if err != nil {
		b.t.Fatalf("http.Client.Do(%s) error = %v", req.URL, err)
	}
	raw, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		b.t.Fatalf("io.ReadAll() error = %v", err)
	}

	return resp, string(raw)
}

// signInToHost gives the browser the host's session as the person.
func (b *browser) signInToHost(ctx context.Context, th *testHost, who string) {
	b.t.Helper()

	resp, _ := b.get(ctx, th.server.URL+"/login?"+url.Values{"as": {who}, "returnUrl": {"/"}}.Encode())
	if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusOK {
		b.t.Fatalf("host sign-in ended with %d", resp.StatusCode)
	}
}

// stateCookies is the application's state cookie store for the round trip: what the
// session library's Auth gives a method, kept in memory here.
type stateCookies struct {
	mu     sync.Mutex
	values map[string]*cookie.Values
}

const stateCookieName = "OIDC-persona"

func (c *stateCookies) WriteStateCookie(w http.ResponseWriter, values *cookie.Values) {
	id := uuid.Must(uuid.NewV4()).String()
	c.mu.Lock()
	c.values[id] = values
	c.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: stateCookieName, Value: id, Path: "/"})
}

func (c *stateCookies) ReadStateCookie(r *http.Request) (*cookie.Values, string, error) {
	ck, err := r.Cookie(stateCookieName)
	if err != nil {
		return nil, "", nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	values, ok := c.values[ck.Value]
	if !ok {
		return nil, "", nil
	}

	return values, stateCookieName, nil
}

func (*stateCookies) DeleteStateCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, MaxAge: -1, Path: "/"})
}

// application is the relying party: the session library's generic verifier against the
// host's issuer as the public client clientID.
type application struct {
	verifier *oidc.Verifier
}

func (th *testHost) application(client string) *application {
	return &application{verifier: oidc.NewVerifier(&stateCookies{values: map[string]*cookie.Values{}}, th.issuer, client, "", appCallback, nil)}
}

// claims are what the application reads from the ID token.
type claims struct {
	Issuer            string `json:"iss"`
	Subject           string `json:"sub"`
	PreferredUsername string `json:"preferred_username"`
	Name              string `json:"name"`
	Email             string `json:"email"`
	EmailVerified     bool   `json:"email_verified"`
}

// begin starts the application's login: the authorization URL and the state cookie the
// callback must carry.
func (a *application) begin(ctx context.Context, t *testing.T, options signin.LoginOptions) (authURL string, stateCookie *http.Cookie) {
	t.Helper()

	rec := httptest.NewRecorder()
	authURL, err := a.verifier.AuthCodeURL(ctx, rec, "/home", options)
	if err != nil {
		t.Fatalf("Verifier.AuthCodeURL() error = %v", err)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == stateCookieName {
			stateCookie = c
		}
	}
	if stateCookie == nil {
		t.Fatal("AuthCodeURL() set no state cookie")
	}

	return authURL, stateCookie
}

// finish completes the application's login from the callback the browser was sent to.
func (a *application) finish(ctx context.Context, t *testing.T, callback *url.URL, stateCookie *http.Cookie) (got claims, returnURL string) {
	t.Helper()

	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/user/persona/callback?"+callback.RawQuery, http.NoBody)
	req.AddCookie(stateCookie)
	returnURL, err := a.verifier.Verify(ctx, httptest.NewRecorder(), req, &got)
	if err != nil {
		t.Fatalf("Verifier.Verify() error = %v", err)
	}

	return got, returnURL
}

// register sends a registration as a deploy would.
func (th *testHost) register(ctx context.Context, t *testing.T, client, token string, body *persona.RegistrationRequest) (status int, answer string) {
	t.Helper()

	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	return th.call(ctx, t, http.MethodPut, th.issuer+"/registrations/"+client, token, raw)
}

func (th *testHost) call(ctx context.Context, t *testing.T, method, rawURL, token string, body []byte) (status int, answer string) {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, method, rawURL, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("http.Client.Do() error = %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, string(raw)
}

func lanternsRegistration() persona.RegistrationRequest {
	return persona.RegistrationRequest{
		ApplicationClientID: "lanterns", Environment: "tst", PullRequest: 12, RedirectURIs: []string{appCallback},
		Cast: []persona.Persona{
			{Login: "cadet", Name: "Cadet Example", Email: "cadet@example.com", Roles: []string{"Cadet"}},
			{Login: "navigator", Name: "Navigator Example", Email: "navigator@example.com", Roles: []string{"Navigator", "Crew"}},
		},
	}
}

func (th *testHost) mustRegister(ctx context.Context, t *testing.T) {
	t.Helper()

	body := lanternsRegistration()
	if status, answer := th.register(ctx, t, clientID, "deployer:"+deployer, &body); status != http.StatusOK {
		t.Fatalf("register = %d %s, want 200", status, answer)
	}
}

// TestSignIn_pickAndHint drives the whole round trip the way an application and its
// person do: the deploy registers, the application sends the browser to the issuer, the
// host's session names the person, the picker shows the cast, a pick returns the browser
// to the application with a code, and the application's verifier reads the persona from
// the ID token. Then the same with the persona named in the login hint, which skips the
// picker.
func TestSignIn_pickAndHint(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	th := newTestHost(ctx, t)
	th.mustRegister(ctx, t)
	app := th.application(clientID)
	b := th.browser(t)
	b.signInToHost(ctx, th, "u-pat")

	// The picker.
	authURL, stateCookie := app.begin(ctx, t, signin.LoginOptions{})
	resp, page := b.get(ctx, authURL)
	if resp.StatusCode != http.StatusOK || !strings.Contains(page, "Sign in to Lanterns") || !strings.Contains(page, "Cadet Example") || !strings.Contains(page, "pull request 12") {
		t.Fatalf("the authorize walk ended at %s with %d, want the picker for Lanterns; page:\n%s", resp.Request.URL, resp.StatusCode, page)
	}
	// The picker's address names the request; its forms post back to it.
	if !strings.HasPrefix(resp.Request.URL.Path, "/persona/pick/") {
		t.Fatalf("the picker is at %s, want /persona/pick/{request}", resp.Request.URL.Path)
	}
	resp, _ = b.post(ctx, th.issuer+strings.TrimPrefix(resp.Request.URL.Path, "/persona"), url.Values{"login": {"cadet"}})
	callback := redirectedTo(t, resp)
	got, returnURL := app.finish(ctx, t, callback, stateCookie)
	want := claims{Issuer: th.issuer, Subject: "cadet", PreferredUsername: "cadet", Name: "Cadet Example", Email: "cadet@example.com", EmailVerified: true}
	if got != want || returnURL != "/home" {
		t.Errorf("claims = %+v, returnURL = %q; want %+v and /home", got, returnURL, want)
	}
	th.assertSignIns(ctx, t, 1, "cadet", "u-pat")

	// The hint: no picker, straight back to the application. A moment later, so the
	// sign-in log's order is the sign-ins' order.
	th.clock.Advance(time.Second)
	authURL, stateCookie = app.begin(ctx, t, signin.LoginOptions{LoginHint: "navigator"})
	resp, page = b.get(ctx, authURL)
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("the hinted walk ended at %s with %d, want the application's callback; page:\n%s", resp.Request.URL, resp.StatusCode, page)
	}
	got, _ = app.finish(ctx, t, redirectedTo(t, resp), stateCookie)
	if got.Subject != "navigator" || got.PreferredUsername != "navigator" || got.Email != "navigator@example.com" {
		t.Errorf("hinted claims = %+v, want navigator", got)
	}
	th.assertSignIns(ctx, t, 2, "navigator", "u-pat")

	// A hint naming no persona of the cast falls back to the picker.
	authURL, _ = app.begin(ctx, t, signin.LoginOptions{LoginHint: "nobody"})
	if resp, page = b.get(ctx, authURL); resp.StatusCode != http.StatusOK || !strings.Contains(page, "Pick who to sign in as") {
		t.Errorf("a hint outside the cast ended at %s with %d, want the picker", resp.Request.URL, resp.StatusCode)
	}

	// The key set publishes the signing key, and a rotation adds one.
	if keys := th.keyCount(ctx, t); keys != 1 {
		t.Errorf("key set = %d keys, want 1", keys)
	}
	if err := th.svc.RotateSigningKey(ctx); err != nil {
		t.Fatalf("RotateSigningKey() error = %v", err)
	}
	if keys := th.keyCount(ctx, t); keys != 2 {
		t.Errorf("key set after a rotation = %d keys, want 2", keys)
	}
}

// TestSignIn_hostSession proves the picker needs the host's session: a browser without
// one is sent to the host's login page with the picker as the return address, and comes
// back to the picker once signed in.
func TestSignIn_hostSession(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	th := newTestHost(ctx, t)
	th.mustRegister(ctx, t)
	app := th.application(clientID)
	b := th.browser(t)

	authURL, _ := app.begin(ctx, t, signin.LoginOptions{})
	resp, page := b.get(ctx, authURL)
	if resp.StatusCode != http.StatusOK || resp.Request.URL.Path != "/login" || !strings.Contains(page, "Sign in to the host") {
		t.Fatalf("a browser without a session ended at %s with %d, want the host's login page", resp.Request.URL, resp.StatusCode)
	}
	returnURL := resp.Request.URL.Query().Get("returnUrl")
	if !strings.HasPrefix(returnURL, "/persona/pick/") {
		t.Fatalf("returnUrl = %q, want the picker", returnURL)
	}
	resp, page = b.get(ctx, th.server.URL+"/login?"+url.Values{"as": {"u-sam"}, "returnUrl": {returnURL}}.Encode())
	if resp.StatusCode != http.StatusOK || !strings.Contains(page, "Navigator Example") {
		t.Errorf("after the host sign-in the browser ended at %s with %d, want the picker", resp.Request.URL, resp.StatusCode)
	}
}

// TestSignIn_refusals proves the refusals: a person the host does not admit, a client no
// deploy registered, and a deployment deregistered since.
func TestSignIn_refusals(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	th := newTestHost(ctx, t)
	th.mustRegister(ctx, t)
	b := th.browser(t)
	b.signInToHost(ctx, th, "u-sam")

	th.host.mu.Lock()
	th.host.deny["u-sam@app-1"] = true
	th.host.mu.Unlock()
	authURL, _ := th.application(clientID).begin(ctx, t, signin.LoginOptions{})
	if resp, page := b.get(ctx, authURL); resp.StatusCode != http.StatusForbidden || !strings.Contains(page, "You do not have access to the test environments of Lanterns.") {
		t.Errorf("a person without access ended with %d; page:\n%s", resp.StatusCode, page)
	}

	authURL, _ = th.application("nobody-tst").begin(ctx, t, signin.LoginOptions{})
	if resp, _ := b.get(ctx, authURL); resp.StatusCode < http.StatusBadRequest {
		t.Errorf("an unregistered client's authorize answered %d, want a refusal", resp.StatusCode)
	}

	if status, answer := th.call(ctx, t, http.MethodDelete, th.issuer+"/registrations/"+clientID, "deployer:"+deployer, nil); status != http.StatusNoContent {
		t.Fatalf("deregister = %d %s, want 204", status, answer)
	}
	authURL, _ = th.application(clientID).begin(ctx, t, signin.LoginOptions{})
	if resp, _ := b.get(ctx, authURL); resp.StatusCode < http.StatusBadRequest {
		t.Errorf("a deregistered client's authorize answered %d, want a refusal", resp.StatusCode)
	}
}

// TestRegistration_rules proves who may register what: the deploy identity's token for
// the issuer, an identity the application's record allows, the deployment's own client
// ID, and a well-formed body.
func TestRegistration_rules(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	th := newTestHost(ctx, t)

	tests := []struct {
		name       string
		client     string
		token      string
		body       func(r *persona.RegistrationRequest)
		wantStatus int
		wantHas    string
	}{
		{name: "no token", client: clientID, wantStatus: http.StatusUnauthorized, wantHas: "present the deploy identity's token"},
		{name: "a token not for this issuer", client: clientID, token: "stranger", wantStatus: http.StatusUnauthorized, wantHas: "not accepted"},
		{name: "an identity the application does not allow", client: clientID, token: "deployer:other@example.iam.gserviceaccount.com", wantStatus: http.StatusForbidden, wantHas: "is not a deploy identity of Lanterns"},
		{name: "an application the host never minted", client: "ghost-tst", token: "deployer:" + deployer, body: func(r *persona.RegistrationRequest) { r.ApplicationClientID = "ghost" }, wantStatus: http.StatusNotFound, wantHas: "no application has the client ID ghost"},
		{name: "a client ID that is not the deployment's", client: "lanterns-pr13", token: "deployer:" + deployer, wantStatus: http.StatusBadRequest, wantHas: "name lanterns-pr12"},
		{name: "no environment", client: "lanterns-pr12", token: "deployer:" + deployer, body: func(r *persona.RegistrationRequest) { r.Environment = "" }, wantStatus: http.StatusBadRequest, wantHas: "environment is required"},
		{name: "a relative redirect address", client: clientID, token: "deployer:" + deployer, body: func(r *persona.RegistrationRequest) { r.RedirectURIs = []string{"/callback"} }, wantStatus: http.StatusBadRequest, wantHas: "not an absolute https callback address"},
		{name: "a login twice", client: clientID, token: "deployer:" + deployer, body: func(r *persona.RegistrationRequest) { r.Cast[1].Login = "cadet" }, wantStatus: http.StatusBadRequest, wantHas: `the login \"cadet\" appears twice`},
		{name: "a persona without a login", client: clientID, token: "deployer:" + deployer, body: func(r *persona.RegistrationRequest) { r.Cast[0].Login = "" }, wantStatus: http.StatusBadRequest, wantHas: "needs a login"},
		{name: "the deployment's own registration", client: clientID, token: "deployer:" + deployer, wantStatus: http.StatusOK, wantHas: `"clientId":"lanterns-pr12"`},
	}
	// The cases run in order: the last one is the registration the read below expects.
	for _, tt := range tests {
		body := lanternsRegistration()
		if tt.body != nil {
			tt.body(&body)
		}
		status, answer := th.register(ctx, t, tt.client, tt.token, &body)
		if status != tt.wantStatus || !strings.Contains(answer, tt.wantHas) {
			t.Errorf("%s: register = %d %s, want %d containing %q", tt.name, status, answer, tt.wantStatus, tt.wantHas)
		}
	}

	// The registration is readable by a signed-in person of the host, and by no one else.
	if status, _ := th.call(ctx, t, http.MethodGet, th.issuer+"/registrations/"+clientID, "", nil); status != http.StatusUnauthorized {
		t.Errorf("GET registration without a session = %d, want 401", status)
	}
	b := th.browser(t)
	b.signInToHost(ctx, th, "u-pat")
	if resp, answer := b.get(ctx, th.issuer+"/registrations/"+clientID); resp.StatusCode != http.StatusOK || !strings.Contains(answer, `"login":"navigator"`) {
		t.Errorf("GET registration with a session = %d %s, want 200 with the cast", resp.StatusCode, answer)
	}
}

// TestSweep proves a registration not refreshed within the expiry goes, and its client
// with it; a refresh keeps it.
func TestSweep(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	th := newTestHost(ctx, t)
	th.mustRegister(ctx, t)

	th.clock.Advance(6 * 24 * time.Hour)
	if n, err := th.svc.Sweep(ctx); err != nil || n != 0 {
		t.Errorf("Sweep() before the expiry = %d, %v; want nothing removed", n, err)
	}
	th.mustRegister(ctx, t) // the redeploy refreshes
	th.clock.Advance(2 * 24 * time.Hour)
	if n, err := th.svc.Sweep(ctx); err != nil || n != 0 {
		t.Errorf("Sweep() after a refresh = %d, %v; want nothing removed", n, err)
	}
	th.clock.Advance(6 * 24 * time.Hour)
	if n, err := th.svc.Sweep(ctx); err != nil || n != 1 {
		t.Errorf("Sweep() past the expiry = %d, %v; want the registration removed", n, err)
	}
	b := th.browser(t)
	b.signInToHost(ctx, th, "u-pat")
	authURL, _ := th.application(clientID).begin(ctx, t, signin.LoginOptions{})
	if resp, _ := b.get(ctx, authURL); resp.StatusCode < http.StatusBadRequest {
		t.Errorf("an expired client's authorize answered %d, want a refusal", resp.StatusCode)
	}
}

// redirectedTo is where a 302 sends the browser.
func redirectedTo(t *testing.T, resp *http.Response) *url.URL {
	t.Helper()

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("response = %d at %s, want a redirect to the application", resp.StatusCode, resp.Request.URL)
	}
	loc, err := resp.Location()
	if err != nil {
		t.Fatalf("Response.Location() error = %v", err)
	}
	if loc.Host != appCallbackHost || loc.Query().Get("code") == "" || loc.Query().Get("state") == "" {
		t.Fatalf("redirect = %s, want the application's callback with a code and the state", loc)
	}

	return loc
}

// assertSignIns reads the log.
func (th *testHost) assertSignIns(ctx context.Context, t *testing.T, want int64, lastPersona, lastActor string) {
	t.Helper()

	iter := th.db.Single().Query(ctx, spanner.Statement{SQL: "SELECT Persona, ActorId FROM PersonaSignIns ORDER BY SignedInAt"})
	defer iter.Stop()
	var (
		count      int64
		who, actor string
	)
	for {
		row, err := iter.Next()
		if err != nil {
			break
		}
		count++
		if err := row.Columns(&who, &actor); err != nil {
			t.Fatalf("Row.Columns() error = %v", err)
		}
	}
	if count != want || who != lastPersona || actor != lastActor {
		t.Errorf("sign-in log = %d entries, last %s by %s; want %d, %s by %s", count, who, actor, want, lastPersona, lastActor)
	}
}

// keyCount reads the key set.
func (th *testHost) keyCount(ctx context.Context, t *testing.T) int {
	t.Helper()

	status, answer := th.call(ctx, t, http.MethodGet, th.issuer+"/keys", "", nil)
	if status != http.StatusOK {
		t.Fatalf("GET keys = %d %s", status, answer)
	}
	var set struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := json.Unmarshal([]byte(answer), &set); err != nil {
		t.Fatalf("json.Unmarshal(keys) error = %v", err)
	}

	return len(set.Keys)
}
