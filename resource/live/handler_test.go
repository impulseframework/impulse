package live

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// The auths the tests sign people in through: a password auth and a directory auth over
// one database, as an application with two populations has.
const (
	crewAuth    = "crew"
	membersAuth = "members"
)

// withAuth binds the auth's name the way an outlet's Subscribing middleware does.
func withAuth(ctx context.Context, auth string) context.Context {
	return resource.WithAuth(ctx, auth)
}

// withUser seeds the session identity the way the session middleware would, with no
// auth bound: what a request carries before the outlet's Subscribing runs. The session
// belongs to no account, so its person is named by the username (withAccountSession
// seeds one that does).
func withUser(ctx context.Context, user string) context.Context {
	return context.WithValue(ctx, sessioninfo.CtxSessionInfo, &sessioninfo.SessionData{
		SessionInfo: &sessioninfo.SessionInfo{Username: user},
	})
}

// withSession seeds the session identity behind an outlet bound to auth.
func withSession(ctx context.Context, auth, user string) context.Context {
	return withUser(withAuth(ctx, auth), user)
}

// withAccountSession seeds the session of a user of the auth, behind an outlet bound to
// it: the session belongs to the account, whose id names the person, and carries the
// username for people to read.
func withAccountSession(ctx context.Context, auth, user string, account impulse.UUID) context.Context {
	return context.WithValue(withAuth(ctx, auth), sessioninfo.CtxSessionInfo, &sessioninfo.SessionData{
		SessionInfo: &sessioninfo.SessionInfo{Username: user},
		UserID:      impulse.NullUUIDFromUUID(account),
	})
}

// withRoleSession seeds a session established as a role by actor, behind an outlet bound
// to auth.
func withRoleSession(ctx context.Context, auth, actor string, role accesstypes.Role) context.Context {
	return context.WithValue(withAuth(ctx, auth), sessioninfo.CtxSessionInfo, &sessioninfo.SessionData{
		SessionInfo: &sessioninfo.SessionInfo{Username: actor},
		Principal:   accesstypes.RolePrincipal(role),
	})
}

// withViewAsSession seeds a session in which actor views the application as user,
// behind an outlet bound to auth.
func withViewAsSession(ctx context.Context, auth, actor, user string) context.Context {
	return context.WithValue(withAuth(ctx, auth), sessioninfo.CtxSessionInfo, &sessioninfo.SessionData{
		SessionInfo: &sessioninfo.SessionInfo{Username: actor},
		Principal:   accesstypes.UserPrincipal(accesstypes.User(user)),
	})
}

// longName is a user name that makes the crew auth's principal id exactly n bytes.
func longName(n int) string {
	return strings.Repeat("a", n-len(crewAuth+AuthSeparator))
}

// overLimitWords is what a person whose principal id is over the uid limit is told.
const overLimitWords = "Pages do not update on their own for your sign-in. Reload a page to see what has changed."

// gateFunc is a Gate answering through a function.
type gateFunc func(ctx context.Context) (bool, error)

func (g gateFunc) Permitted(ctx context.Context) (bool, error) {
	return g(ctx)
}

// namedQuery is a Query whose gate answers through a function and whose names are read
// from the lists sources names, or which fails to say with err.
type namedQuery struct {
	gate    gateFunc
	sources []resource.NameSource
	err     error
}

func (q namedQuery) Permitted(ctx context.Context) (bool, error) {
	return q.gate(ctx)
}

func (q namedQuery) NameSources(context.Context) ([]resource.NameSource, error) {
	return q.sources, q.err
}

// registerCounted is the Fake keeping what each Register call was handed, so a test sees
// how many writes a request made to the record and what each held.
type registerCounted struct {
	*Fake

	mu    sync.Mutex
	calls [][]Subscription
}

func (c *registerCounted) Register(ctx context.Context, subs []Subscription) error {
	c.mu.Lock()
	c.calls = append(c.calls, slices.Clone(subs))
	c.mu.Unlock()

	return c.Fake.Register(ctx, subs)
}

// writes returns what each Register call was handed, in the order of the calls.
func (c *registerCounted) writes() [][]Subscription {
	c.mu.Lock()
	defer c.mu.Unlock()

	return slices.Clone(c.calls)
}

// checkOneWrite checks that the request wrote the record once, with want in that order
// and every subscription under the expiry of the first, or, for no want, not at all.
func checkOneWrite(t *testing.T, svc *registerCounted, want []Subscription) {
	t.Helper()

	writes := svc.writes()
	if len(want) == 0 {
		if len(writes) != 0 {
			t.Errorf("Register() calls = %v, want none", writes)
		}

		return
	}
	if len(writes) != 1 {
		t.Fatalf("Register() calls = %v, want one write to the record", writes)
	}
	if diff := cmp.Diff(want, writes[0], cmpopts.IgnoreFields(Subscription{}, "Expiry")); diff != "" {
		t.Errorf("Register() mismatch (-want +got):\n%s", diff)
	}
	for _, sub := range writes[0] {
		if sub.Expiry.IsZero() || !sub.Expiry.Equal(writes[0][0].Expiry) {
			t.Errorf("subscription %s of %s expires at %v, want the expiry of the request's own, %v", sub.ID(), sub.Resource, sub.Expiry, writes[0][0].Expiry)
		}
	}
}

// namesOn reads NamesHeader off an answer: nil where the answer carries none.
func namesOn(t *testing.T, header http.Header) *Names {
	t.Helper()

	value := header.Get(NamesHeader)
	if value == "" {
		return nil
	}
	decoded, err := url.PathUnescape(value)
	if err != nil {
		t.Fatalf("url.PathUnescape(%s) error = %v", NamesHeader, err)
	}
	var names Names
	if err := json.Unmarshal([]byte(decoded), &names); err != nil {
		t.Fatalf("json.Unmarshal(%s) error = %v", NamesHeader, err)
	}

	return &names
}

// lockedBuffer is a buffer the standard logger may write from any goroutine.
type lockedBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// takeStandardLog puts a buffer on the standard logger, which a request with no logger
// of its own logs through, until the test ends. One test of the package takes it; the
// lines of tests running beside it land in it too, and it looks for its own.
func takeStandardLog(t *testing.T) *lockedBuffer {
	t.Helper()

	logged := new(lockedBuffer)
	previous := log.Writer()
	log.SetOutput(logged)
	t.Cleanup(func() {
		log.SetOutput(previous)
	})

	return logged
}

// permit, refuse and failing are the gates the subscribe tests answer with: the
// request allowed, the request refused, and the permission check itself failing.
func permit(context.Context) (bool, error) {
	return true, nil
}

func refuse(context.Context) (bool, error) {
	return false, nil
}

func failing(context.Context) (bool, error) {
	return false, errors.New("engine down")
}

// passing is a handler that answers 200 and records that it ran.
func passing(ran *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*ran = true
		w.WriteHeader(http.StatusOK)
	})
}

func TestSubscribing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		auth       string
		header     string
		wantStatus int
		wantRan    bool
		wantID     string
		wantBody   string
	}{
		{name: "a request without the header passes with the auth bound", auth: crewAuth, wantStatus: http.StatusOK, wantRan: true, wantID: "crew|dispatcher"},
		{name: "a subscribing request passes with the auth bound", auth: crewAuth, header: "tab-1", wantStatus: http.StatusOK, wantRan: true, wantID: "crew|dispatcher"},
		{name: "another outlet binds its own auth", auth: membersAuth, header: "tab-1", wantStatus: http.StatusOK, wantRan: true, wantID: "members|dispatcher"},
		{name: "a malformed tab is refused naming the header", auth: crewAuth, header: "tab 1", wantStatus: http.StatusBadRequest, wantBody: "invalid X-Subscribe value"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ran, gotID := false, ""
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ran = true
				id, err := PrincipalID(r.Context())
				if err != nil {
					t.Errorf("PrincipalID() error = %v", err)
				}
				gotID = id
				w.WriteHeader(http.StatusOK)
			})
			req := httptest.NewRequestWithContext(withUser(t.Context(), "dispatcher"), http.MethodGet, "/api/ships", http.NoBody)
			if tt.header != "" {
				req.Header.Set(SubscribeHeader, tt.header)
			}
			rr := httptest.NewRecorder()
			Subscribing(tt.auth)(next).ServeHTTP(rr, req)

			if rr.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d: %s", rr.Code, tt.wantStatus, rr.Body.String())
			}
			if ran != tt.wantRan {
				t.Errorf("handler ran = %v, want %v", ran, tt.wantRan)
			}
			if gotID != tt.wantID {
				t.Errorf("principal id = %q, want %q", gotID, tt.wantID)
			}
			if tt.wantBody != "" && !strings.Contains(rr.Body.String(), tt.wantBody) {
				t.Errorf("body = %q, want it to contain %q", rr.Body.String(), tt.wantBody)
			}
		})
	}
}

// TestSubscribing_groupBound proves what Subscribing does with a request its outlet's
// group bound (resource.BindAuth): where the group bound the auth Subscribing was given,
// the request passes on and what answers for it reads that auth; where the group bound
// another auth, the request is refused with a 500 before the next handler, and the
// cause logged names both auths.
func TestSubscribing_groupBound(t *testing.T) {
	t.Parallel()

	const refusal = "this request is bound to the crew auth, and the routes it reached were mounted for the members auth's sessions " +
		"(live.Subscribing, which an outlet's generated routes run under). A request comes through one auth, the one its outlet's group binds: " +
		"mount these routes under the members auth's own group, or give the group's resource.BindAuth and its routes one auth's name"
	tests := []struct {
		name       string
		group      string
		wantStatus int
		wantRan    bool
		wantCause  string
	}{
		{name: "a request its group bound to the routes' auth passes on with that auth", group: membersAuth, wantStatus: http.StatusOK, wantRan: true},
		{name: "a request its group bound to another auth is refused", group: crewAuth, wantStatus: http.StatusInternalServerError, wantCause: refusal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ran, gotAuth, gotOK, gotBound := false, "", false, ""
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ran = true
				gotAuth, gotOK = resource.AuthFrom(r.Context())
				gotBound = resource.BoundAuth(r.Context())
				w.WriteHeader(http.StatusOK)
			})
			var grouped context.Context
			bind := resource.BindAuth(tt.group)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				grouped = r.Context()
				Subscribing(membersAuth)(next).ServeHTTP(w, r)
			}))
			req := httptest.NewRequestWithContext(withUser(t.Context(), "dispatcher"), http.MethodGet, "/api/ships", http.NoBody)
			rr := httptest.NewRecorder()
			bind.ServeHTTP(rr, req)

			if rr.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d: %s", rr.Code, tt.wantStatus, rr.Body.String())
			}
			if ran != tt.wantRan {
				t.Errorf("handler ran = %v, want %v", ran, tt.wantRan)
			}
			if tt.wantRan && (gotAuth != membersAuth || !gotOK || gotBound != membersAuth) {
				t.Errorf("resource.AuthFrom() = %q, %v and resource.BoundAuth() = %q, want %q, true and %q", gotAuth, gotOK, gotBound, membersAuth, membersAuth)
			}
			err := groupAuthRefusal(grouped, membersAuth)
			switch {
			case tt.wantCause == "" && err != nil:
				t.Errorf("groupAuthRefusal() error = %v, want nil", err)
			case tt.wantCause != "" && err == nil:
				t.Error("groupAuthRefusal() error = nil, want the refusal")
			case tt.wantCause != "" && !strings.HasSuffix(err.Error(), "error="+tt.wantCause):
				t.Errorf("groupAuthRefusal() error = %v, want the cause %q", err, tt.wantCause)
			}
		})
	}
}

// TestSubscribing_authName proves the refusal of an auth name that cannot begin a
// principal id: it panics as the routes are registered, naming what is wrong.
func TestSubscribing_authName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		auth      string
		wantPanic string
	}{
		{name: "an auth's name is accepted", auth: crewAuth},
		{name: "a name with a hyphen is accepted", auth: "field-crew"},
		{name: "an empty name is refused", auth: "", wantPanic: "the auth's name is empty"},
		{name: "a name carrying the separator is refused", auth: "crew|night", wantPanic: `cannot carry "|"`},
		{name: "a name carrying a slash is refused", auth: "crew/night", wantPanic: `cannot carry "/"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got string
			func() {
				defer func() {
					if r := recover(); r != nil {
						got = fmt.Sprint(r)
					}
				}()
				Subscribing(tt.auth)
			}()

			switch {
			case tt.wantPanic == "" && got != "":
				t.Errorf("Subscribing(%q) panicked: %s", tt.auth, got)
			case tt.wantPanic != "" && !strings.Contains(got, tt.wantPanic):
				t.Errorf("Subscribing(%q) panic = %q, want it to contain %q", tt.auth, got, tt.wantPanic)
			}
		})
	}
}

func TestRefusing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		header     string
		wantStatus int
		wantRan    bool
	}{
		{name: "a request without the header passes", wantStatus: http.StatusOK, wantRan: true},
		{name: "a subscribing request is refused on an outlet without sessions", header: "tab-1", wantStatus: http.StatusBadRequest},
		{name: "a malformed subscribing request is refused the same way", header: "tab 1", wantStatus: http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ran := false
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/droids/beacons", http.NoBody)
			if tt.header != "" {
				req.Header.Set(SubscribeHeader, tt.header)
			}
			rr := httptest.NewRecorder()
			Refusing()(passing(&ran)).ServeHTTP(rr, req)

			if rr.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rr.Code, tt.wantStatus)
			}
			if ran != tt.wantRan {
				t.Errorf("handler ran = %v, want %v", ran, tt.wantRan)
			}
			if tt.header != "" && !strings.Contains(rr.Body.String(), SubscribeHeader) {
				t.Errorf("body = %q, want it to name %s", rr.Body.String(), SubscribeHeader)
			}
		})
	}
}

func TestSubscribe(t *testing.T) {
	t.Parallel()

	squadrons := resource.NameSource{Resource: "Squadrons", Field: "name", Domain: "anvil"}
	tests := []struct {
		name      string
		ctx       func(ctx context.Context) context.Context
		header    string
		gate      gateFunc
		sources   []resource.NameSource
		sub       *Subscription
		want      []Subscription
		wantNames *Names
	}{
		{
			name:   "a permitted list request registers the list before the query, and names nothing",
			header: "tab-1",
			gate:   permit,
			sub:    ListSubscription("Ships", "anvil"),
			want:   []Subscription{{Principal: "crew|dispatcher", Tab: "tab-1", Resource: "Ships", Domain: "anvil"}},
		},
		{
			name:   "a permitted read registers the row, with no domain",
			header: "tab-1",
			gate:   permit,
			sub:    &Subscription{Resource: "Ships", Key: "s1", Domain: "anvil"},
			want:   []Subscription{{Principal: "crew|dispatcher", Tab: "tab-1", Resource: "Ships", Key: "s1"}},
		},
		{
			name:    "a list showing names read from two lists registers the three in one write and names the two",
			header:  "tab-1",
			gate:    permit,
			sources: []resource.NameSource{squadrons, {Resource: "Makers", Field: "name"}},
			sub:     ListSubscription("Ships", "anvil"),
			want: []Subscription{
				{Principal: "crew|dispatcher", Tab: "tab-1", Resource: "Ships", Domain: "anvil"},
				{Principal: "crew|dispatcher", Tab: "tab-1", Resource: "Squadrons", Domain: "anvil"},
				{Principal: "crew|dispatcher", Tab: "tab-1", Resource: "Makers"},
			},
			wantNames: &Names{
				Of:    SubscriptionRequest{Resource: "Ships", Domain: "anvil"},
				Names: []SubscriptionRequest{{Resource: "Squadrons", Domain: "anvil", NameField: "name"}, {Resource: "Makers", NameField: "name"}},
			},
		},
		{
			name:    "a read showing a name registers the row and the list; of names the row by its key and no domain",
			header:  "tab-1",
			gate:    permit,
			sources: []resource.NameSource{squadrons},
			sub:     &Subscription{Resource: "Ships", Key: "s1", Domain: "anvil"},
			want: []Subscription{
				{Principal: "crew|dispatcher", Tab: "tab-1", Resource: "Ships", Key: "s1"},
				{Principal: "crew|dispatcher", Tab: "tab-1", Resource: "Squadrons", Domain: "anvil"},
			},
			wantNames: &Names{
				Of:    SubscriptionRequest{Resource: "Ships", Key: "s1"},
				Names: []SubscriptionRequest{{Resource: "Squadrons", Domain: "anvil", NameField: "name"}},
			},
		},
		{
			name:    "a name that is its resource's key: the list, named with no name field",
			header:  "tab-1",
			gate:    permit,
			sources: []resource.NameSource{{Resource: "Bays", Domain: "anvil"}},
			sub:     ListSubscription("Ships", "anvil"),
			want: []Subscription{
				{Principal: "crew|dispatcher", Tab: "tab-1", Resource: "Ships", Domain: "anvil"},
				{Principal: "crew|dispatcher", Tab: "tab-1", Resource: "Bays", Domain: "anvil"},
			},
			wantNames: &Names{
				Of:    SubscriptionRequest{Resource: "Ships", Domain: "anvil"},
				Names: []SubscriptionRequest{{Resource: "Bays", Domain: "anvil"}},
			},
		},
		{
			name:    "a name read from the request's own list is neither registered twice nor named",
			header:  "tab-1",
			gate:    permit,
			sources: []resource.NameSource{{Resource: "Crews", Field: "name", Domain: "anvil"}, squadrons},
			sub:     ListSubscription("Crews", "anvil"),
			want: []Subscription{
				{Principal: "crew|dispatcher", Tab: "tab-1", Resource: "Crews", Domain: "anvil"},
				{Principal: "crew|dispatcher", Tab: "tab-1", Resource: "Squadrons", Domain: "anvil"},
			},
			wantNames: &Names{
				Of:    SubscriptionRequest{Resource: "Crews", Domain: "anvil"},
				Names: []SubscriptionRequest{{Resource: "Squadrons", Domain: "anvil", NameField: "name"}},
			},
		},
		{
			name:    "names read from the request's own list alone: the list once, and nothing named",
			header:  "tab-1",
			gate:    permit,
			sources: []resource.NameSource{{Resource: "Crews", Field: "name", Domain: "anvil"}},
			sub:     ListSubscription("Crews", "anvil"),
			want:    []Subscription{{Principal: "crew|dispatcher", Tab: "tab-1", Resource: "Crews", Domain: "anvil"}},
		},
		{
			name: "the same name through another auth registers under that auth",
			ctx: func(ctx context.Context) context.Context {
				return withSession(ctx, membersAuth, "dispatcher")
			},
			header: "tab-1",
			gate:   permit,
			sub:    ListSubscription("Ships", "anvil"),
			want:   []Subscription{{Principal: "members|dispatcher", Tab: "tab-1", Resource: "Ships", Domain: "anvil"}},
		},
		{
			name:    "a refused request registers nothing and names nothing",
			header:  "tab-1",
			gate:    refuse,
			sources: []resource.NameSource{squadrons},
			sub:     ListSubscription("Ships", "anvil"),
		},
		{
			name:    "a gate that fails registers nothing and the request goes on",
			header:  "tab-1",
			gate:    failing,
			sources: []resource.NameSource{squadrons},
			sub:     ListSubscription("Ships", "anvil"),
		},
		{
			name:    "a request without the header registers nothing",
			gate:    permit,
			sources: []resource.NameSource{squadrons},
			sub:     ListSubscription("Ships", "anvil"),
		},
		{
			name: "a principal id over the uid limit registers nothing and the request goes on",
			ctx: func(ctx context.Context) context.Context {
				return withSession(ctx, crewAuth, longName(MaxPrincipalIDLength+1))
			},
			header:  "tab-1",
			gate:    permit,
			sources: []resource.NameSource{squadrons},
			sub:     ListSubscription("Ships", "anvil"),
		},
		{
			name: "a request no auth was bound to registers nothing and the request goes on",
			ctx: func(ctx context.Context) context.Context {
				return withUser(ctx, "dispatcher")
			},
			header: "tab-1",
			gate:   permit,
			sub:    ListSubscription("Ships", "anvil"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := &registerCounted{Fake: NewFake()}
			ctx := withSession(t.Context(), crewAuth, "dispatcher")
			if tt.ctx != nil {
				ctx = tt.ctx(t.Context())
			}
			req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/ships", http.NoBody)
			if tt.header != "" {
				req.Header.Set(SubscribeHeader, tt.header)
			}
			w := httptest.NewRecorder()

			Subscribe(ctx, w, req, svc, namedQuery{gate: tt.gate, sources: tt.sources}, tt.sub)

			checkOneWrite(t, svc, tt.want)
			if diff := cmp.Diff(tt.wantNames, namesOn(t, w.Header())); diff != "" {
				t.Errorf("%s mismatch (-want +got):\n%s", NamesHeader, diff)
			}
		})
	}
}

// TestSubscribe_namesHeader pins the header's text, before its percent-encoding, for a
// list of ships in anvil that shows each ship's squadron by name.
func TestSubscribe_namesHeader(t *testing.T) {
	t.Parallel()

	ctx := withSession(t.Context(), crewAuth, "dispatcher")
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/ships", http.NoBody)
	req.Header.Set(SubscribeHeader, "tab-1")
	w := httptest.NewRecorder()

	Subscribe(ctx, w, req, NewFake(), namedQuery{gate: permit, sources: []resource.NameSource{{Resource: "Squadrons", Field: "name", Domain: "anvil"}}}, ListSubscription("Ships", "anvil"))

	got, err := url.PathUnescape(w.Header().Get(NamesHeader))
	if err != nil {
		t.Fatalf("url.PathUnescape() error = %v", err)
	}
	if want := `{"of":{"resource":"Ships","domain":"anvil"},"names":[{"resource":"Squadrons","domain":"anvil","nameField":"name"}]}`; got != want {
		t.Errorf("%s = %s, want %s", NamesHeader, got, want)
	}
}

// TestSubscribe_namesUnread pins a request whose names' lists cannot be read: its own
// subscription is registered alone, the answer names nothing, and the failure is logged.
func TestSubscribe_namesUnread(t *testing.T) {
	t.Parallel()

	logged := takeStandardLog(t)
	svc := &registerCounted{Fake: NewFake()}
	ctx := withSession(t.Context(), crewAuth, "dispatcher")
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/ships", http.NoBody)
	req.Header.Set(SubscribeHeader, "tab-unread")
	w := httptest.NewRecorder()

	Subscribe(ctx, w, req, svc, namedQuery{gate: permit, err: errors.New("the grants are down")}, ListSubscription("Ships", "anvil"))

	checkOneWrite(t, svc, []Subscription{{Principal: "crew|dispatcher", Tab: "tab-unread", Resource: "Ships", Domain: "anvil"}})
	if names := namesOn(t, w.Header()); names != nil {
		t.Errorf("%s = %+v, want none", NamesHeader, names)
	}
	want := "live: reading the lists the names of the Ships request of tab tab-unread are read from failed; the request's own subscription is registered without them: the grants are down"
	if !strings.Contains(logged.String(), want) {
		t.Errorf("the log =\n%s\nwant it to say:\n%s", logged.String(), want)
	}
}

func TestSubscribe_recordFailureIsLogged(t *testing.T) {
	t.Parallel()

	fake := NewFake()
	fake.Err = errors.New("record down")
	ctx := withSession(t.Context(), crewAuth, "dispatcher")
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/ships", http.NoBody)
	req.Header.Set(SubscribeHeader, "tab-1")
	w := httptest.NewRecorder()

	// The request is still answered: Subscribe returns, nothing panics, and the answer
	// names no list the record did not take.
	Subscribe(ctx, w, req, fake, namedQuery{gate: permit, sources: []resource.NameSource{{Resource: "Squadrons", Field: "name", Domain: "anvil"}}}, ListSubscription("Ships", "anvil"))

	if names := namesOn(t, w.Header()); names != nil {
		t.Errorf("%s = %+v, want none: the record took nothing", NamesHeader, names)
	}
}

// TestSubscribe_namesChange pins what registering the names' lists is for: a tab whose
// page lists ships in anvil by their squadrons' names is told, in its change set, of a
// write to a squadron in anvil, as a page listing the squadrons there is, and of none in
// cinder.
func TestSubscribe_namesChange(t *testing.T) {
	t.Parallel()

	fake := NewFake()
	ctx := withSession(t.Context(), crewAuth, "dispatcher")
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/ships", http.NoBody)
	req.Header.Set(SubscribeHeader, "tab-1")
	Subscribe(ctx, httptest.NewRecorder(), req, fake, namedQuery{gate: permit, sources: []resource.NameSource{{Resource: "Squadrons", Field: "name", Domain: "anvil"}}}, ListSubscription("Ships", "anvil"))

	for domain, write := range map[accesstypes.Domain]string{"anvil": "write-anvil", "cinder": "write-cinder"} {
		if _, err := fake.Publish(t.Context(), domain, Writes{ID: write, Rows: map[accesstypes.Resource][]resource.RowChange{"Squadrons": {{Key: "q1"}}}}); err != nil {
			t.Fatalf("Publish(%s) error = %v", domain, err)
		}
	}

	want := []FakeChange{{ChangeDocument: ChangeDocument{Kind: ListChange, Resource: "Squadrons", Domain: "anvil"}, Write: "write-anvil"}}
	if diff := cmp.Diff(want, fake.Changes("crew|dispatcher"), cmpopts.IgnoreFields(FakeChange{}, "ID", "At")); diff != "" {
		t.Errorf("Changes() mismatch (-want +got):\n%s", diff)
	}
}

func TestSetCacheControl(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		url         string
		wantControl string
		wantPragma  string
		wantExpires string
	}{
		{name: "a request carrying the version parameter is cacheable", url: "/api/ships?_v=1727900000123456", wantControl: CacheControl},
		{name: "a request without it keeps the outlet's no-cache headers", url: "/api/ships", wantControl: "no-cache, no-store, must-revalidate", wantPragma: "no-cache", wantExpires: "0"},
		{name: "another parameter is not the version", url: "/api/ships?v=1", wantControl: "no-cache, no-store, must-revalidate", wantPragma: "no-cache", wantExpires: "0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.url, http.NoBody)
			rr := httptest.NewRecorder()
			// What the outlet's NoCaching middleware set before the handler ran.
			rr.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			rr.Header().Set("Pragma", "no-cache")
			rr.Header().Set("Expires", "0")

			SetCacheControl(rr, req)

			if got := rr.Header().Get("Cache-Control"); got != tt.wantControl {
				t.Errorf("Cache-Control = %q, want %q", got, tt.wantControl)
			}
			if got := rr.Header().Get("Pragma"); got != tt.wantPragma {
				t.Errorf("Pragma = %q, want %q", got, tt.wantPragma)
			}
			if got := rr.Header().Get("Expires"); got != tt.wantExpires {
				t.Errorf("Expires = %q, want %q", got, tt.wantExpires)
			}
		})
	}
}

func TestPublish(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		writes []write
		// tables are the tables the request says it touched whole.
		tables []accesstypes.Resource
		domain accesstypes.Domain
		want   []FakePublish
	}{
		{
			name:   "a committed request publishes under its route domain",
			writes: []write{{key: "s1"}},
			domain: "anvil",
			want:   []FakePublish{{Domain: "anvil", Writes: Writes{Rows: map[accesstypes.Resource][]resource.RowChange{"Ships": {{Key: "s1"}}}}}},
		},
		{
			name:   "rows decoded in their own tenant publish under it",
			writes: []write{{domain: "bastion", key: "h1"}},
			domain: "",
			want:   []FakePublish{{Domain: "bastion", Writes: Writes{Rows: map[accesstypes.Resource][]resource.RowChange{"Hangars": {{Key: "h1"}}}}}},
		},
		{
			name:   "a request that wrote in two tenants publishes once under each, in the tenants' order",
			writes: []write{{domain: "bastion", key: "h1"}, {domain: "anvil", key: "h2"}},
			domain: "",
			want: []FakePublish{
				{Domain: "anvil", Writes: Writes{Rows: map[accesstypes.Resource][]resource.RowChange{"Hangars": {{Key: "h2"}}}}},
				{Domain: "bastion", Writes: Writes{Rows: map[accesstypes.Resource][]resource.RowChange{"Hangars": {{Key: "h1"}}}}},
			},
		},
		{
			name:   "the tables touched whole go into the route domain's publish, which is made though no row was written in it",
			writes: []write{{domain: "bastion", key: "h1"}},
			tables: []accesstypes.Resource{"Squadrons"},
			domain: "anvil",
			want: []FakePublish{
				{Domain: "anvil", Writes: Writes{Whole: []WholeChange{{Resource: "Squadrons", AllDomains: true}}}},
				{Domain: "bastion", Writes: Writes{Rows: map[accesstypes.Resource][]resource.RowChange{"Hangars": {{Key: "h1"}}}}},
			},
		},
		{name: "a request that committed nothing publishes nothing", domain: "anvil"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := NewFake()
			ctx, touched := commit(t, tt.writes...)
			for _, table := range tt.tables {
				touched.TouchTable(table)
			}

			Publish(ctx, httptest.NewRecorder(), fake, nil, tt.domain, touched)

			got := fake.Publishes()
			if diff := cmp.Diff(tt.want, got, cmpopts.EquateEmpty(), cmpopts.IgnoreFields(Writes{}, "ID")); diff != "" {
				t.Errorf("Publishes() mismatch (-want +got):\n%s", diff)
			}
			// The request is one write: every publish of it carries the same id.
			for _, publish := range got {
				if publish.Writes.ID == "" || publish.Writes.ID != got[0].Writes.ID {
					t.Errorf("the publish under %q carries the write id %q, want the request's one id %q, not empty", publish.Domain, publish.Writes.ID, got[0].Writes.ID)
				}
			}
		})
	}
}

// write is one row a test's request commits: a Hangar decoded in domain when one is
// given, a Ship built by hand otherwise.
type write struct {
	domain accesstypes.Domain
	key    string
}

// commit runs one request's transactions, one per write, under a collector, and returns
// the request's context and what it touched.
func commit(t *testing.T, writes ...write) (context.Context, *resource.TouchedRows) {
	t.Helper()

	ctx, touched := resource.CollectTouchedRows(t.Context())
	client := resource.NewMockClient(&bufferingTxn{}, nil, nil)
	for _, w := range writes {
		if err := client.ExecuteFunc(ctx, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			return bufferRow(ctx, txn, w.domain, w.key)
		}); err != nil {
			t.Fatalf("ExecuteFunc() error = %v", err)
		}
	}

	return ctx, touched
}

// answerOf reads the changes header off a write's answer; nil when the answer carries
// none.
func answerOf(t *testing.T, rr *httptest.ResponseRecorder) *Answer {
	t.Helper()

	value := rr.Header().Get(ChangesHeader)
	if value == "" {
		return nil
	}
	if len(value) > MaxChangesHeaderLength {
		t.Errorf("the %s header is %d bytes, over the limit of %d", ChangesHeader, len(value), MaxChangesHeaderLength)
	}
	for i := range len(value) {
		if value[i] <= ' ' || value[i] > '~' {
			t.Fatalf("the %s header carries the byte %#x at %d, which a header value outside visible ASCII cannot hold: %q", ChangesHeader, value[i], i, value)
		}
	}
	decoded, err := url.PathUnescape(value)
	if err != nil {
		t.Fatalf("url.PathUnescape(%q) error = %v", value, err)
	}
	var answer Answer
	if err := json.Unmarshal([]byte(decoded), &answer); err != nil {
		t.Fatalf("json.Unmarshal(%s) error = %v", decoded, err)
	}

	return &answer
}

// TestPublish_answer pins what a write's answer tells the tab that wrote: the write's
// id and the documents the write put into the caller's own change set, nobody else's,
// and nothing at all when the caller holds none of what changed live.
func TestPublish_answer(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	dispatcher := crewAuth + AuthSeparator + "dispatcher"
	harbormaster := crewAuth + AuthSeparator + "harbormaster"
	// A key and a tenant a header cannot carry as they are.
	const (
		oddKey    = "s/1 \"é\""
		oddDomain = accesstypes.Domain("añvil")
	)

	tests := []struct {
		name   string
		subs   []Subscription
		ctx    func(context.Context) context.Context
		writes []write
		domain accesstypes.Domain
		// want is the answer's documents; nil is an answer with no header.
		want []ChangeDocument
	}{
		{
			name: "the caller's list and row, and nobody else's",
			subs: []Subscription{
				{Principal: dispatcher, Tab: "t1", Resource: "Ships", Domain: "anvil"},
				{Principal: dispatcher, Tab: "t1", Resource: "Ships", Key: "s1"},
				{Principal: harbormaster, Tab: "t2", Resource: "Ships", Key: "s1"},
				{Principal: harbormaster, Tab: "t2", Resource: "Ships", Domain: "bastion"},
			},
			ctx: func(ctx context.Context) context.Context {
				return withSession(ctx, crewAuth, "dispatcher")
			},
			writes: []write{{key: "s1"}},
			domain: "anvil",
			want: []ChangeDocument{
				{Kind: ListChange, Resource: "Ships", Domain: "anvil"},
				{Kind: RowChange, Resource: "Ships", Key: "s1"},
			},
		},
		{
			name: "a caller subscribed through two tabs is named each document once",
			subs: []Subscription{
				{Principal: dispatcher, Tab: "t1", Resource: "Ships", Key: "s1"},
				{Principal: dispatcher, Tab: "t2", Resource: "Ships", Key: "s1"},
			},
			ctx: func(ctx context.Context) context.Context {
				return withSession(ctx, crewAuth, "dispatcher")
			},
			writes: []write{{key: "s1"}},
			domain: "anvil",
			want:   []ChangeDocument{{Kind: RowChange, Resource: "Ships", Key: "s1"}},
		},
		{
			name: "rows written in two tenants are named under each tenant, in the tenants' order",
			subs: []Subscription{
				{Principal: dispatcher, Tab: "t1", Resource: "Hangars", Domain: "bastion"},
				{Principal: dispatcher, Tab: "t1", Resource: "Hangars", Domain: "anvil"},
			},
			ctx: func(ctx context.Context) context.Context {
				return withSession(ctx, crewAuth, "dispatcher")
			},
			writes: []write{{domain: "bastion", key: "h1"}, {domain: "anvil", key: "h2"}},
			want: []ChangeDocument{
				{Kind: ListChange, Resource: "Hangars", Domain: "anvil"},
				{Kind: ListChange, Resource: "Hangars", Domain: "bastion"},
			},
		},
		{
			name: "a key and a tenant outside what a header holds travel encoded",
			subs: []Subscription{
				{Principal: dispatcher, Tab: "t1", Resource: "Ships", Key: oddKey},
				{Principal: dispatcher, Tab: "t1", Resource: "Ships", Domain: oddDomain},
			},
			ctx: func(ctx context.Context) context.Context {
				return withSession(ctx, crewAuth, "dispatcher")
			},
			writes: []write{{key: oddKey}},
			domain: oddDomain,
			want: []ChangeDocument{
				{Kind: ListChange, Resource: "Ships", Domain: oddDomain},
				{Kind: RowChange, Resource: "Ships", Key: oddKey},
			},
		},
		{
			name: "a caller who holds none of what changed gets no header",
			subs: []Subscription{{Principal: harbormaster, Tab: "t2", Resource: "Ships", Key: "s1"}},
			ctx: func(ctx context.Context) context.Context {
				return withSession(ctx, crewAuth, "dispatcher")
			},
			writes: []write{{key: "s1"}},
			domain: "anvil",
		},
		{
			name: "the same user name through another auth is another person",
			subs: []Subscription{{Principal: dispatcher, Tab: "t1", Resource: "Ships", Key: "s1"}},
			ctx: func(ctx context.Context) context.Context {
				return withSession(ctx, membersAuth, "dispatcher")
			},
			writes: []write{{key: "s1"}},
			domain: "anvil",
		},
		{
			name: "a request with no session principal gets no header",
			subs: []Subscription{{Principal: dispatcher, Tab: "t1", Resource: "Ships", Key: "s1"}},
			ctx: func(ctx context.Context) context.Context {
				return withAuth(ctx, crewAuth)
			},
			writes: []write{{key: "s1"}},
			domain: "anvil",
		},
		{
			name: "a request no auth is bound to gets no header",
			subs: []Subscription{{Principal: dispatcher, Tab: "t1", Resource: "Ships", Key: "s1"}},
			ctx: func(ctx context.Context) context.Context {
				return withUser(ctx, "dispatcher")
			},
			writes: []write{{key: "s1"}},
			domain: "anvil",
		},
		{
			name: "a request that committed nothing gets no header",
			subs: []Subscription{{Principal: dispatcher, Tab: "t1", Resource: "Ships", Key: "s1"}},
			ctx: func(ctx context.Context) context.Context {
				return withSession(ctx, crewAuth, "dispatcher")
			},
			domain: "anvil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := subscribed(t, now, tt.subs...)
			ctx, touched := commit(t, tt.writes...)
			rr := httptest.NewRecorder()

			Publish(tt.ctx(ctx), rr, fake, nil, tt.domain, touched)

			answer := answerOf(t, rr)
			if tt.want == nil {
				if answer != nil {
					t.Fatalf("the answer carries %s = %+v, want no header", ChangesHeader, *answer)
				}

				return
			}
			if answer == nil {
				t.Fatalf("the answer carries no %s header, want one naming %v", ChangesHeader, tt.want)
			}
			if diff := cmp.Diff(tt.want, answer.Changes); diff != "" {
				t.Errorf("the answer's changes mismatch (-want +got):\n%s", diff)
			}
			if answer.All {
				t.Error("the answer says all, want the documents named")
			}
			// The id the answer names is the one every document of the write carries, in
			// the caller's change set and in everybody else's.
			if answer.Write == "" {
				t.Fatal("the answer names no write")
			}
			for _, sub := range tt.subs {
				for _, change := range fake.Changes(sub.Principal) {
					if change.Write != answer.Write {
						t.Errorf("%s's document %s carries the write %q, want the answer's %q", sub.Principal, change.ID, change.Write, answer.Write)
					}
				}
			}
		})
	}
}

// TestAnswer_header pins the header's text for one answer. The browser client's test
// reads the same text (parseChangesHeader in web/projects/resource/src/live.spec.ts), so
// the two sides agree on what travels.
func TestAnswer_header(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		answer Answer
		want   string
	}{
		{
			name: "a list, a deleted row and a whole resource",
			answer: Answer{Write: "W1", Changes: []ChangeDocument{
				{Kind: ListChange, Resource: "Ships", Domain: "añvil"},
				{Kind: RowChange, Resource: "Ships", Key: `s/1 "é"`, Deleted: true},
				{Kind: ResourceChange, Resource: "MissionBoards"},
			}},
			want: "%7B%22write%22:%22W1%22%2C%22changes%22:%5B%7B%22kind%22:%22list%22%2C%22resource%22:%22Ships%22%2C%22domain%22:%22a%C3%B1vil%22%7D%2C" +
				"%7B%22kind%22:%22row%22%2C%22resource%22:%22Ships%22%2C%22key%22:%22s%2F1%20%5C%22%C3%A9%5C%22%22%2C%22deleted%22:true%7D%2C" +
				"%7B%22kind%22:%22resource%22%2C%22resource%22:%22MissionBoards%22%7D%5D%7D",
		},
		{
			name:   "more than the header holds",
			answer: Answer{Write: "W2", All: true},
			want:   "%7B%22write%22:%22W2%22%2C%22all%22:true%7D",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := tt.answer.header()
			if err != nil {
				t.Fatalf("header() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("header() = %s, want %s", got, tt.want)
			}
		})
	}
}

// TestPublish_answer_eachWriteItsOwnID pins that two requests are two writes: a tab
// that wrote one must not take the other's change for its own.
func TestPublish_answer_eachWriteItsOwnID(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	dispatcher := crewAuth + AuthSeparator + "dispatcher"
	fake := subscribed(t, now, Subscription{Principal: dispatcher, Tab: "t1", Resource: "Ships", Key: "s1"})

	ids := make([]string, 0, 2)
	for range 2 {
		ctx, touched := commit(t, write{key: "s1"})
		rr := httptest.NewRecorder()
		Publish(withSession(ctx, crewAuth, "dispatcher"), rr, fake, nil, "anvil", touched)
		answer := answerOf(t, rr)
		if answer == nil {
			t.Fatalf("the answer carries no %s header", ChangesHeader)
		}
		ids = append(ids, answer.Write)
	}
	if ids[0] == ids[1] {
		t.Errorf("two requests answered the same write id %q", ids[0])
	}
	// Both wrote one row within the fake's one second: one document, carrying the later
	// write's id, so the tab that wrote first sees somebody else's change in it.
	changes := fake.Changes(dispatcher)
	if len(changes) != 1 || changes[0].Write != ids[1] {
		t.Errorf("the change set = %+v, want one document carrying the later write %q", changes, ids[1])
	}
}

// TestPublish_answer_tooManyToName pins the header's bound: a write that reached more of
// the caller's subscriptions than the header holds answers the write's id with all set.
func TestPublish_answer_tooManyToName(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	dispatcher := crewAuth + AuthSeparator + "dispatcher"
	const rows = 60
	subs := make([]Subscription, 0, rows)
	writes := make([]write, 0, rows)
	for i := range rows {
		key := fmt.Sprintf("0b5e9a1c-0000-4000-8000-%012d", i)
		subs = append(subs, Subscription{Principal: dispatcher, Tab: "t1", Resource: "Ships", Key: key})
		writes = append(writes, write{key: key})
	}
	fake := subscribed(t, now, subs...)
	ctx, touched := commit(t, writes...)
	rr := httptest.NewRecorder()

	Publish(withSession(ctx, crewAuth, "dispatcher"), rr, fake, nil, "anvil", touched)

	answer := answerOf(t, rr)
	if answer == nil {
		t.Fatalf("the answer carries no %s header", ChangesHeader)
	}
	if !answer.All || len(answer.Changes) != 0 {
		t.Errorf("the answer = %+v, want all set and no document named", *answer)
	}
	if changes := fake.Changes(dispatcher); len(changes) != rows || changes[0].Write != answer.Write {
		t.Errorf("the change set holds %d documents, the first by write %q; want %d by the answer's write %q", len(changes), changes[0].Write, rows, answer.Write)
	}
}

// failingIn is a live service whose publishes under one domain fail after the others
// went through, as a backend that stops answering partway through a request would.
type failingIn struct {
	*Fake
	domain accesstypes.Domain
}

func (f failingIn) Publish(ctx context.Context, domain accesstypes.Domain, writes Writes) (Changes, error) {
	if domain == f.domain {
		return nil, errors.New("firestore down")
	}

	return f.Fake.Publish(ctx, domain, writes)
}

// TestPublish_answer_partFailed pins that a request any of whose publishes failed names
// nothing: a failed publish may have written some of its documents, which a tab told the
// write's id would take for changes it had fetched for.
func TestPublish_answer_partFailed(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	dispatcher := crewAuth + AuthSeparator + "dispatcher"
	fake := subscribed(t, now,
		Subscription{Principal: dispatcher, Tab: "t1", Resource: "Hangars", Domain: "anvil"},
		Subscription{Principal: dispatcher, Tab: "t1", Resource: "Hangars", Domain: "bastion"},
	)
	ctx, touched := commit(t, write{domain: "anvil", key: "h1"}, write{domain: "bastion", key: "h2"})
	rr := httptest.NewRecorder()

	Publish(withSession(ctx, crewAuth, "dispatcher"), rr, failingIn{Fake: fake, domain: "bastion"}, nil, "", touched)

	if got := rr.Header().Get(ChangesHeader); got != "" {
		t.Errorf("%s = %q after a publish that failed in part, want no header", ChangesHeader, got)
	}
	// What got through is in the change set, for the tab to hear as anyone's change.
	if got := len(fake.Changes(dispatcher)); got != 1 {
		t.Errorf("the change set holds %d documents, want the one of the tenant whose publish went through", got)
	}
}

func TestPublish_failureIsLogged(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	fake := subscribed(t, now, Subscription{Principal: crewAuth + AuthSeparator + "dispatcher", Tab: "t1", Resource: "Ships", Key: "s1"})
	fake.Err = errors.New("firestore down")
	ctx, touched := commit(t, write{key: "s1"})
	rr := httptest.NewRecorder()

	// The request still answers: Publish returns, nothing panics, and the answer names
	// nothing, since nothing was published.
	Publish(withSession(ctx, crewAuth, "dispatcher"), rr, fake, nil, "anvil", touched)

	if got := rr.Header().Get(ChangesHeader); got != "" {
		t.Errorf("%s = %q after a publish that failed, want no header", ChangesHeader, got)
	}
}

// TestAnnounce pins a publish that is no request's own write: the rows and the tables a
// collector holds reach a row subscriber, a list subscriber in the rows' tenant and a
// subscriber of a view in another tenant, each in one publish per domain, and no document
// names a write.
func TestAnnounce(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	fake := subscribed(t, now,
		Subscription{Principal: "harbormaster", Tab: "t1", Resource: "Ships", Key: "s1"},
		Subscription{Principal: "quartermaster", Tab: "t2", Resource: "Hangars", Domain: "bastion"},
		Subscription{Principal: "dispatcher", Tab: "t3", Resource: "MissionBoards", Domain: "anvil"},
		Subscription{Principal: "clerk", Tab: "t4", Resource: "Clients", Domain: "anvil"},
	)
	ctx, touched := commit(t, write{domain: "bastion", key: "h1"})
	touched.Touch("Ships", resource.RowChange{Key: "s1"})
	touched.TouchTable("Squadrons")

	Announce(ctx, fake, fixtureDerivations(), touched)

	want := []FakePublish{
		{Domain: "", Writes: Writes{
			Rows:  map[accesstypes.Resource][]resource.RowChange{"Ships": {{Key: "s1"}}, "FleetBoards": {{Key: "s1"}}},
			Whole: []WholeChange{{Resource: "MissionBoards", AllDomains: true}, {Resource: "Squadrons", AllDomains: true}},
		}},
		{Domain: "bastion", Writes: Writes{Rows: map[accesstypes.Resource][]resource.RowChange{"Hangars": {{Key: "h1"}}}}},
	}
	if diff := cmp.Diff(want, fake.Publishes(), cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("Publishes() mismatch (-want +got):\n%s", diff)
	}
	wantChanges := map[string][]ChangeDocument{
		"harbormaster":  {{Kind: RowChange, Resource: "Ships", Key: "s1"}},
		"quartermaster": {{Kind: ListChange, Resource: "Hangars", Domain: "bastion"}},
		"dispatcher":    {{Kind: ResourceChange, Resource: "MissionBoards"}},
	}
	for principal, wantDocs := range wantChanges {
		held := fake.Changes(principal)
		docs := make([]ChangeDocument, 0, len(held))
		for _, change := range held {
			if change.Write != "" {
				t.Errorf("%s was told %+v under the write %q, want no write named", principal, change.ChangeDocument, change.Write)
			}
			docs = append(docs, change.ChangeDocument)
		}
		if diff := cmp.Diff(wantDocs, docs); diff != "" {
			t.Errorf("the change set of %s mismatch (-want +got):\n%s", principal, diff)
		}
	}
	if got := fake.Changes("clerk"); len(got) != 0 {
		t.Errorf("clerk, holding a table nothing touched, was told %+v, want nothing", got)
	}
}

// TestAnnounce_nothing pins the announcements that publish nothing: an empty collector, a
// nil one, and a nil service.
func TestAnnounce_nothing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		touched func(t *testing.T) *resource.TouchedRows
		noSvc   bool
	}{
		{
			name: "an empty collector",
			touched: func(t *testing.T) *resource.TouchedRows {
				t.Helper()
				_, touched := resource.CollectTouchedRows(t.Context())

				return touched
			},
		},
		{
			name: "a nil collector",
			touched: func(*testing.T) *resource.TouchedRows {
				return nil
			},
		},
		{
			name: "a nil service",
			touched: func(t *testing.T) *resource.TouchedRows {
				t.Helper()
				_, touched := commit(t, write{key: "s1"})
				touched.TouchTable("Squadrons")

				return touched
			},
			noSvc: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := NewFake()
			var svc Service = fake
			if tt.noSvc {
				svc = nil
			}

			Announce(t.Context(), svc, fixtureDerivations(), tt.touched(t))

			if got := fake.Publishes(); len(got) != 0 {
				t.Errorf("Publishes() = %+v, want none", got)
			}
		})
	}
}

// TestAnnounce_failureIsLogged pins a publish that fails in one domain: the others are
// still made, and Announce returns.
func TestAnnounce_failureIsLogged(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	fake := subscribed(t, now, Subscription{Principal: "quartermaster", Tab: "t1", Resource: "Hangars", Domain: "bastion"})
	ctx, touched := commit(t, write{domain: "anvil", key: "h1"}, write{domain: "bastion", key: "h2"})

	Announce(ctx, failingIn{Fake: fake, domain: "anvil"}, nil, touched)

	if got := len(fake.Changes("quartermaster")); got != 1 {
		t.Errorf("the change set holds %d documents, want the one of the tenant whose publish went through", got)
	}
}

func TestPrincipalID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		ctx         context.Context
		want        string
		wantErr     string
		wantRefusal bool
	}{
		{name: "a password user is the user, qualified by the password auth", ctx: withSession(context.Background(), crewAuth, "mechanic"), want: "crew|mechanic"},
		{name: "a directory user is the email, qualified by the directory auth", ctx: withSession(context.Background(), membersAuth, "mechanic@example.com"), want: "members|mechanic@example.com"},
		{name: "a password user named like an email stays the password auth's", ctx: withSession(context.Background(), crewAuth, "mechanic@example.com"), want: "crew|mechanic@example.com"},
		{name: "a role session is the role, marked, qualified by its auth", ctx: withRoleSession(context.Background(), crewAuth, "alice", "Auditor"), want: "crew|role:Auditor"},
		{name: "a role session through the directory auth is that auth's", ctx: withRoleSession(context.Background(), membersAuth, "alice@example.com", "Auditor"), want: "members|role:Auditor"},
		{name: "a view-as session is the user viewed, qualified by the auth", ctx: withViewAsSession(context.Background(), crewAuth, "alice", "mechanic"), want: "crew|mechanic"},
		{name: "a user name holding the separator keeps it after the auth's", ctx: withSession(context.Background(), crewAuth, "night|shift"), want: "crew|night|shift"},
		{name: "an id of exactly the uid limit is served", ctx: withSession(context.Background(), crewAuth, longName(MaxPrincipalIDLength)), want: "crew|" + longName(MaxPrincipalIDLength)},
		{
			name:        "an id one byte over the uid limit is refused, never shortened",
			ctx:         withSession(context.Background(), crewAuth, longName(MaxPrincipalIDLength+1)),
			wantErr:     fmt.Sprintf("the principal id %q is 129 bytes, over Firebase's uid limit of 128", crewAuth+AuthSeparator+longName(MaxPrincipalIDLength+1)),
			wantRefusal: true,
		},
		{
			name:        "a role session over the uid limit is refused the same way",
			ctx:         withRoleSession(context.Background(), crewAuth, "alice", accesstypes.Role(longName(MaxPrincipalIDLength))),
			wantErr:     "over Firebase's uid limit of 128",
			wantRefusal: true,
		},
		{
			name:    "a request no auth was bound to fails",
			ctx:     withUser(context.Background(), "mechanic"),
			wantErr: "no auth is bound to the request",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := PrincipalID(tt.ctx)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("PrincipalID() error = %v", err)
				}
				if got != tt.want {
					t.Errorf("PrincipalID() = %q, want %q", got, tt.want)
				}
				if len(got) > MaxPrincipalIDLength {
					t.Errorf("PrincipalID() = %d bytes, over %d", len(got), MaxPrincipalIDLength)
				}

				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("PrincipalID() error = %v, want it to contain %q", err, tt.wantErr)
			}
			if got != "" {
				t.Errorf("PrincipalID() = %q alongside its error, want nothing", got)
			}
			if refused := httpio.Message(err) != ""; refused != tt.wantRefusal {
				t.Errorf("PrincipalID() client message = %q, want one: %v", httpio.Message(err), tt.wantRefusal)
			}
		})
	}
}

// TestPrincipalID_account pins the principal id of an auth's user: the auth, then the
// person as the permission store names them, by the id of their account and never by
// the username. A person renamed keeps their id, and so their subscriptions; two
// accounts that share a username are two principal ids, in one auth as across two; and
// a session in which an actor views the application as a user is the account viewed.
func TestPrincipalID_account(t *testing.T) {
	t.Parallel()

	mechanic := impulse.Must(impulse.UUIDFromString("0b5e0000-0000-4000-8000-000000000001"))
	other := impulse.Must(impulse.UUIDFromString("0b5e0000-0000-4000-8000-000000000002"))
	viewAs := context.WithValue(withAuth(context.Background(), crewAuth), sessioninfo.CtxSessionInfo, &sessioninfo.SessionData{
		SessionInfo: &sessioninfo.SessionInfo{Username: "mechanic"},
		UserID:      impulse.NullUUIDFromUUID(mechanic),
		Impersonation: &sessioninfo.Impersonation{
			Actor: "alice", ActorID: impulse.NullUUIDFromUUID(other), Principal: accesstypes.UserPrincipal(accesstypes.User(mechanic.String())),
		},
	})

	tests := []struct {
		name string
		ctx  context.Context
		want string
	}{
		{name: "a user of an auth is the id of their account, qualified by the auth", ctx: withAccountSession(context.Background(), crewAuth, "mechanic", mechanic), want: "crew|0b5e0000-0000-4000-8000-000000000001"},
		{name: "the person renamed keeps the id", ctx: withAccountSession(context.Background(), crewAuth, "mechanic.r", mechanic), want: "crew|0b5e0000-0000-4000-8000-000000000001"},
		{name: "another account under the same username is another id", ctx: withAccountSession(context.Background(), crewAuth, "mechanic", other), want: "crew|0b5e0000-0000-4000-8000-000000000002"},
		{name: "the same username in another auth is that auth's account", ctx: withAccountSession(context.Background(), membersAuth, "mechanic", other), want: "members|0b5e0000-0000-4000-8000-000000000002"},
		{name: "a session that views the application as a user is the account viewed", ctx: viewAs, want: "crew|0b5e0000-0000-4000-8000-000000000001"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := PrincipalID(tt.ctx)
			if err != nil {
				t.Fatalf("PrincipalID() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("PrincipalID() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestPrincipalID_twoAuthsOneName is the collision the qualification exists for: a
// password user and a directory user who share one name are two principal ids.
func TestPrincipalID_twoAuthsOneName(t *testing.T) {
	t.Parallel()

	const shared = "alice@example.com"
	tests := []struct {
		name string
		ctx  context.Context
		want string
	}{
		{name: "the password user", ctx: withSession(context.Background(), crewAuth, shared), want: "crew|alice@example.com"},
		{name: "the directory user", ctx: withSession(context.Background(), membersAuth, shared), want: "members|alice@example.com"},
	}

	seen := make(map[string]string, len(tests))
	for _, tt := range tests {
		got, err := PrincipalID(tt.ctx)
		if err != nil {
			t.Fatalf("%s: PrincipalID() error = %v", tt.name, err)
		}
		if got != tt.want {
			t.Errorf("%s: PrincipalID() = %q, want %q", tt.name, got, tt.want)
		}
		if prior, ok := seen[got]; ok {
			t.Errorf("%s and %s share the principal id %q", prior, tt.name, got)
		}
		seen[got] = tt.name
	}
}
