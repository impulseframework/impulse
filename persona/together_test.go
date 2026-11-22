package persona_test

// Requests that arrive at the same moment: two exchanges of one code, two instances that
// start together on an empty store. A gate in front of the store holds the callers until all
// have arrived, so they reach the store's decision together. Nothing here prints a code, a token or a key: only statuses, the OAuth error
// names and counts.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/go-chi/chi/v5"
	"github.com/impulseframework/impulse/persona"
	"github.com/impulseframework/impulse/persona/spannerstore"
)

// gate holds its callers until n have arrived, or a timeout passes.
type gate struct {
	n       int
	mu      sync.Mutex
	arrived int
	open    chan struct{}
	timeout atomic.Int32
}

func newGate(n int) *gate {
	return &gate{n: n, open: make(chan struct{})}
}

// wait holds the caller until n callers have arrived, or counts a timeout.
func (g *gate) wait() {
	g.mu.Lock()
	g.arrived++
	if g.arrived == g.n {
		close(g.open)
	}
	g.mu.Unlock()

	select {
	case <-g.open:
	case <-time.After(15 * time.Second):
		g.timeout.Add(1)
	}
}

// gatedStore is the Spanner store with a gate in front of DeleteRequest and
// AddFirstSigningKey, so their callers reach the decision together.
type gatedStore struct {
	*spannerstore.Store
	deleteGate   atomic.Pointer[gate]
	firstKeyGate atomic.Pointer[gate]
	deletes      atomic.Int32
}

func (s *gatedStore) DeleteRequest(ctx context.Context, id string) error {
	s.deletes.Add(1)
	if g := s.deleteGate.Load(); g != nil {
		g.wait()
	}

	return s.Store.DeleteRequest(ctx, id)
}

func (s *gatedStore) AddFirstSigningKey(ctx context.Context, key *persona.SigningKey) error {
	if g := s.firstKeyGate.Load(); g != nil {
		g.wait()
	}

	return s.Store.AddFirstSigningKey(ctx, key)
}

// newDatabase makes a database with the persona schema; it goes with the test.
func newDatabase(ctx context.Context, t *testing.T) *spanner.Client {
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

	return database.Client
}

// newHostOn is a host application with the persona sign-in mounted at /persona, over a given
// store and database: a session cookie naming the person, a login page that sets it, and the
// service. It may run beside another, so it reports a failure and returns nil rather than
// stop the test.
func newHostOn(ctx context.Context, t *testing.T, client *spanner.Client, store persona.Store) *testHost {
	t.Helper()

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
	th := &testHost{server: server, issuer: server.URL + "/persona", host: host, clock: clk, db: client}
	svc, err := persona.New(ctx, host, store, &persona.Config{
		Issuer: th.issuer, Actor: actorFromContext, LoginURL: "/login", Deployers: fakeDeployers{issuer: th.issuer}, AllowInsecureIssuer: true, Now: clk.Now,
	})
	if err != nil {
		t.Errorf("persona.New() error = %v", err)

		return nil
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

// grant is one code with its verifier.
type grant struct {
	code, verifier string
}

// getCode walks the hinted sign-in to the application's callback and returns the code.
func getCode(ctx context.Context, t *testing.T, th *testHost, b *browser) grant {
	t.Helper()

	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	verifier := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"client_id": {clientID}, "redirect_uri": {appCallback}, "response_type": {"code"}, "scope": {"openid profile"},
		"state": {"s1"}, "nonce": {"n1"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"},
		"login_hint": {"cadet"},
	}
	resp, _ := b.get(ctx, th.issuer+"/authorize?"+q.Encode())
	loc := redirectedTo(t, resp)

	return grant{code: loc.Query().Get("code"), verifier: verifier}
}

// outcome is what one exchange got: the status, the OAuth error, whether tokens came, and
// the ID token's key ID.
type outcome struct {
	status      int
	oauthError  string
	accessToken bool
	idToken     bool
	kid         string
}

// postCode posts a code to the token endpoint, the form changed by mutate when it is not nil,
// and returns the status and the decoded answer; -1 and an error name when no answer came.
func postCode(ctx context.Context, th *testHost, g grant, mutate func(url.Values)) (status int, body map[string]any) {
	form := url.Values{"grant_type": {"authorization_code"}, "code": {g.code}, "redirect_uri": {appCallback}, "client_id": {clientID}, "code_verifier": {g.verifier}}
	if mutate != nil {
		mutate(form)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, th.issuer+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return -1, map[string]any{"error": "request"}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return -1, map[string]any{"error": "transport"}
	}
	defer func() { _ = resp.Body.Close() }()

	_ = json.NewDecoder(resp.Body).Decode(&body)

	return resp.StatusCode, body
}

// exchange posts a code to the token endpoint, the form changed by mutate when it is not nil,
// and says what came back without the tokens themselves.
func exchange(ctx context.Context, th *testHost, g grant, mutate func(url.Values)) outcome {
	status, body := postCode(ctx, th, g, mutate)
	o := outcome{status: status}
	if e, ok := body["error"].(string); ok {
		o.oauthError = e
	}
	if a, ok := body["access_token"].(string); ok && a != "" {
		o.accessToken = true
	}
	if id, ok := body["id_token"].(string); ok && id != "" {
		o.idToken = true
		o.kid = keyID(id)
	}

	return o
}

// accessTokenOf exchanges a code and returns the access token, which the test holds in memory
// for its next request and never prints.
func accessTokenOf(ctx context.Context, t *testing.T, th *testHost, g grant) string {
	t.Helper()

	status, body := postCode(ctx, th, g, nil)
	token, _ := body["access_token"].(string)
	if status != http.StatusOK || token == "" {
		t.Fatalf("the exchange answered %d with error %v, want an access token", status, body["error"])
	}

	return token
}

// userinfoEndpoint is the userinfo endpoint's address in the discovery document.
func (th *testHost) userinfoEndpoint(ctx context.Context, t *testing.T) string {
	t.Helper()

	status, answer := th.call(ctx, t, http.MethodGet, th.issuer+"/.well-known/openid-configuration", "", nil)
	var doc struct {
		UserinfoEndpoint string `json:"userinfo_endpoint"`
	}
	if status != http.StatusOK || json.Unmarshal([]byte(answer), &doc) != nil || doc.UserinfoEndpoint == "" {
		t.Fatalf("the discovery document = %d %s, want one with a userinfo endpoint", status, answer)
	}

	return doc.UserinfoEndpoint
}

// keyID is the key ID in a token's header.
func keyID(token string) string {
	header, err := base64.RawURLEncoding.DecodeString(strings.SplitN(token, ".", 2)[0])
	if err != nil {
		return ""
	}
	var h struct {
		Kid string `json:"kid"`
	}
	_ = json.Unmarshal(header, &h)

	return h.Kid
}

// count is the number a query that counts rows returns.
func count(ctx context.Context, t *testing.T, client *spanner.Client, sql string) int64 {
	t.Helper()

	iter := client.Single().Query(ctx, spanner.Statement{SQL: sql})
	defer iter.Stop()
	row, err := iter.Next()
	if err != nil {
		t.Fatalf("spanner.RowIterator.Next() error = %v", err)
	}
	var n int64
	if err := row.Columns(&n); err != nil {
		t.Fatalf("spanner.Row.Columns() error = %v", err)
	}

	return n
}

// winners sorts outcomes into exchanges that got both tokens, refusals with invalid_grant and
// no token, and anything else.
func winners(outs []outcome) (won, refused int, other []outcome) {
	for _, o := range outs {
		switch {
		case o.status == http.StatusOK && o.accessToken && o.idToken:
			won++
		case o.status == http.StatusBadRequest && o.oauthError == "invalid_grant" && !o.accessToken && !o.idToken:
			refused++
		default:
			other = append(other, o)
		}
	}

	return won, refused, other
}

// TestSignIn_oneCodeTwoExchanges holds two exchanges of one code at the store's
// DeleteRequest until both have arrived, past every check and with their tokens made: one
// leaves with tokens, the other is refused with neither, and so is a third afterwards.
func TestSignIn_oneCodeTwoExchanges(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	client := newDatabase(ctx, t)
	store := &gatedStore{Store: spannerstore.New(client)}
	th := newHostOn(ctx, t, client, store)
	if th == nil {
		t.FailNow()
	}
	th.mustRegister(ctx, t)
	b := th.browser(t)
	b.signInToHost(ctx, th, "u-pat")
	g := getCode(ctx, t, th, b)

	gt := newGate(2)
	store.deleteGate.Store(gt)
	outs := make([]outcome, 2)
	var wg sync.WaitGroup
	for i := range outs {
		wg.Go(func() { outs[i] = exchange(ctx, th, g, nil) })
	}
	wg.Wait()
	store.deleteGate.Store(nil)

	if n := gt.timeout.Load(); n != 0 {
		t.Errorf("%d of the two exchanges did not reach DeleteRequest together", n)
	}
	if won, refused, other := winners(outs); won != 1 || refused != 1 || len(other) != 0 {
		t.Errorf("exchanges: %d with tokens, %d refused with invalid_grant, others %+v; want one and one", won, refused, other)
	}
	if o := exchange(ctx, th, g, nil); o.status != http.StatusBadRequest || o.oauthError != "invalid_grant" || o.accessToken || o.idToken {
		t.Errorf("a later exchange = %+v, want invalid_grant and no token", o)
	}
}

// TestSignIn_ownFailuresKeepTheCode: an exchange refused for its own fault never uses the
// code up; the good exchange after them gets its tokens.
func TestSignIn_ownFailuresKeepTheCode(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	client := newDatabase(ctx, t)
	store := &gatedStore{Store: spannerstore.New(client)}
	th := newHostOn(ctx, t, client, store)
	if th == nil {
		t.FailNow()
	}
	th.mustRegister(ctx, t)
	b := th.browser(t)
	b.signInToHost(ctx, th, "u-pat")
	g := getCode(ctx, t, th, b)

	failures := []struct {
		name   string
		mutate func(url.Values)
	}{
		{name: "a wrong verifier", mutate: func(f url.Values) { f.Set("code_verifier", strings.Repeat("a", 43)) }},
		{name: "a wrong redirect address", mutate: func(f url.Values) { f.Set("redirect_uri", "https://"+appCallbackHost+"/other") }},
		{name: "an unknown client", mutate: func(f url.Values) { f.Set("client_id", "nobody-tst") }},
	}
	for _, failure := range failures {
		if o := exchange(ctx, th, g, failure.mutate); o.status == http.StatusOK || o.accessToken || o.idToken {
			t.Errorf("an exchange with %s = %+v, want a refusal", failure.name, o)
		}
	}
	if n := store.deletes.Load(); n != 0 {
		t.Errorf("the refused exchanges called DeleteRequest %d times, want 0", n)
	}
	if o := exchange(ctx, th, g, nil); o.status != http.StatusOK || !o.accessToken || !o.idToken {
		t.Errorf("the good exchange after the refusals = %+v, want both tokens", o)
	}
}

// TestSignIn_firstKeyOnce starts two instances on one empty store, each held at
// AddFirstSigningKey until both have arrived: the store keeps one key, and both instances
// publish it and sign with it. A round shows a broken rule only when both instances look
// before either inserts, which the gate makes likely and cannot make certain, so there are
// three.
func TestSignIn_firstKeyOnce(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	for range 3 {
		t.Run("round", func(t *testing.T) {
			t.Parallel()

			client := newDatabase(ctx, t)
			gt := newGate(2)
			stores := []*gatedStore{{Store: spannerstore.New(client)}, {Store: spannerstore.New(client)}}
			for _, s := range stores {
				s.firstKeyGate.Store(gt)
			}
			hosts := make([]*testHost, 2)
			var wg sync.WaitGroup
			for i := range hosts {
				wg.Go(func() { hosts[i] = newHostOn(ctx, t, client, stores[i]) })
			}
			wg.Wait()
			if t.Failed() {
				t.FailNow()
			}

			if n := gt.timeout.Load(); n != 0 {
				t.Errorf("%d of the two instances did not reach AddFirstSigningKey together", n)
			}
			if n := count(ctx, t, client, "SELECT COUNT(*) FROM PersonaKeys"); n != 1 {
				t.Errorf("the store holds %d keys, want 1", n)
			}
			hosts[0].mustRegister(ctx, t)
			kids := make([]string, len(hosts))
			for i, th := range hosts {
				if n := th.keyCount(ctx, t); n != 1 {
					t.Errorf("instance %d publishes %d keys, want 1", i, n)
				}
				b := th.browser(t)
				b.signInToHost(ctx, th, "u-pat")
				o := exchange(ctx, th, getCode(ctx, t, th, b), nil)
				if o.status != http.StatusOK || !o.idToken {
					t.Fatalf("the exchange at instance %d = %+v, want tokens", i, o)
				}
				kids[i] = o.kid
			}
			if kids[0] == "" || kids[0] != kids[1] {
				t.Errorf("the two instances' ID tokens carry different key IDs")
			}

			if err := hosts[1].svc.RotateSigningKey(ctx); err != nil {
				t.Fatalf("Service.RotateSigningKey() error = %v", err)
			}
			if n := count(ctx, t, client, "SELECT COUNT(*) FROM PersonaKeys"); n != 2 {
				t.Errorf("the store holds %d keys after a rotation, want 2", n)
			}
			if n := hosts[1].keyCount(ctx, t); n != 2 {
				t.Errorf("the rotating instance publishes %d keys, want 2", n)
			}
		})
	}
}

// TestSignIn_tokensReadAcrossARotation: an instance started after a key rotation answers an
// access token an instance started before it issued.
func TestSignIn_tokensReadAcrossARotation(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	client := newDatabase(ctx, t)

	hostA := newHostOn(ctx, t, client, spannerstore.New(client))
	if hostA == nil {
		t.FailNow()
	}
	hostA.mustRegister(ctx, t)
	b := hostA.browser(t)
	b.signInToHost(ctx, hostA, "u-pat")
	token := accessTokenOf(ctx, t, hostA, getCode(ctx, t, hostA, b))
	if err := hostA.svc.RotateSigningKey(ctx); err != nil {
		t.Fatalf("Service.RotateSigningKey() error = %v", err)
	}

	hostB := newHostOn(ctx, t, client, spannerstore.New(client))
	if hostB == nil {
		t.FailNow()
	}
	status, answer := hostB.call(ctx, t, http.MethodGet, hostB.userinfoEndpoint(ctx, t), token, nil)
	var info struct {
		Subject string `json:"sub"`
	}
	_ = json.Unmarshal([]byte(answer), &info)
	if status != http.StatusOK || info.Subject != "cadet" {
		t.Errorf("instance B's userinfo answered A's access token with %d and subject %q, want 200 and cadet", status, info.Subject)
	}
}
