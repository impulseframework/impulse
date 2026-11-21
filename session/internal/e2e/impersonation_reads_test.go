package e2e

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/session/signin"
)

// The kind of the read scenarios that acts as a role, and its grant.
const (
	kindRole                       = "role"
	grantRole accesstypes.Resource = "ActAsRole"
)

// overridingStore is an Auth store an application wrote over the library's. It embeds
// the library's store, which is how an application adds to one, and answers Session,
// User and UserByUserName itself: each asks the library's store and then hands on what a
// scenario tells it to, as a store that keeps a cache, or one that means harm, would.
// Each keeps what it was asked, so a scenario can show the session library never asked
// it for what decides a session that acts as someone else.
type overridingStore[Tx any] struct {
	sessionstorage.AuthStore[Tx]

	mu sync.Mutex
	// session changes what Session hands on, on every call; startSession on the calls
	// made while a session that acts as someone else is being started.
	session, startSession func(sess *sessioninfo.SessionData) *sessioninfo.SessionData
	// userInstead names the account User hands on in place of the one it was asked for;
	// nameInstead the same for UserByUserName.
	userInstead map[impulse.UUID]impulse.UUID
	nameInstead map[string]impulse.UUID
	// account changes what User and UserByUserName hand on, on every call.
	account func(u *sessionstorage.SessionUser) *sessionstorage.SessionUser
	// cache, when made, stands in front of Session: an answer once given is given again.
	cache map[impulse.UUID]*sessioninfo.SessionData
	// starting is set while the application's route is inside StartImpersonatedSession.
	starting bool

	sessionsAsked, sessionsAskedStarting []impulse.UUID
	usersAsked                           []impulse.UUID
	namesAsked                           []string
}

// Session is the application's own read of a session, in front of the library's.
func (s *overridingStore[Tx]) Session(ctx context.Context, sessionID impulse.UUID) (*sessioninfo.SessionData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sessionsAsked = append(s.sessionsAsked, sessionID)
	if s.starting {
		s.sessionsAskedStarting = append(s.sessionsAskedStarting, sessionID)
	}
	if kept, ok := s.cache[sessionID]; ok {
		return kept, nil
	}

	sess, err := s.AuthStore.Session(ctx, sessionID)
	if err != nil {
		return nil, errors.Wrap(err, "sessionstorage.AuthStore.Session()")
	}
	if s.session != nil {
		sess = s.session(sess)
	}
	if s.starting && s.startSession != nil {
		sess = s.startSession(sess)
	}
	if s.cache != nil {
		s.cache[sessionID] = sess
	}

	return sess, nil
}

// User is the application's own read of an account, in front of the library's.
func (s *overridingStore[Tx]) User(ctx context.Context, id impulse.UUID) (*sessionstorage.SessionUser, error) {
	s.mu.Lock()
	s.usersAsked = append(s.usersAsked, id)
	instead, replaced := s.userInstead[id]
	account := s.account
	s.mu.Unlock()

	if replaced {
		id = instead
	}
	u, err := s.user(ctx, id)
	if err != nil || account == nil {
		return u, err
	}

	return account(u), nil
}

// UserByUserName is the application's own read of an account by its name, in front of
// the library's.
func (s *overridingStore[Tx]) UserByUserName(ctx context.Context, username string) (*sessionstorage.SessionUser, error) {
	s.mu.Lock()
	s.namesAsked = append(s.namesAsked, username)
	instead, replaced := s.nameInstead[username]
	account := s.account
	s.mu.Unlock()

	if replaced {
		return s.user(ctx, instead)
	}

	u, err := s.AuthStore.UserByUserName(ctx, username)
	if err != nil {
		return nil, errors.Wrap(err, "sessionstorage.AuthStore.UserByUserName()")
	}
	if account != nil {
		u = account(u)
	}

	return u, nil
}

// user is the library's store's read of an account.
func (s *overridingStore[Tx]) user(ctx context.Context, id impulse.UUID) (*sessionstorage.SessionUser, error) {
	u, err := s.AuthStore.User(ctx, id)
	if err != nil {
		return nil, errors.Wrap(err, "sessionstorage.AuthStore.User()")
	}

	return u, nil
}

// set changes what the store hands on.
func (s *overridingStore[Tx]) set(fn func(s *overridingStore[Tx])) {
	s.mu.Lock()
	defer s.mu.Unlock()

	fn(s)
}

// start runs fn as the part of a request that starts a session that acts as someone
// else.
func (s *overridingStore[Tx]) start(fn func()) {
	s.set(func(s *overridingStore[Tx]) {
		s.starting = true
	})
	defer s.set(func(s *overridingStore[Tx]) {
		s.starting = false
	})

	fn()
}

// asked is what the store's reads were asked so far.
func (s *overridingStore[Tx]) asked() askedReads {
	s.mu.Lock()
	defer s.mu.Unlock()

	return askedReads{
		sessions:         slices.Clone(s.sessionsAsked),
		sessionsStarting: slices.Clone(s.sessionsAskedStarting),
		users:            slices.Clone(s.usersAsked),
		names:            slices.Clone(s.namesAsked),
	}
}

// askedReads is what an overridingStore's reads were asked, each list in the order of
// the asking: the sessions its Session was asked for, the ones among them asked while a
// session that acts as someone else was being started, the accounts its User was asked
// for, and the names its UserByUserName was asked for.
type askedReads struct {
	sessions, sessionsStarting, users []impulse.UUID
	names                             []string
}

// readsApp is an application on one Auth over a store of its own that wraps the
// library's (overridingStore), with password sign-in and three kinds of impersonation:
// a view of a person that reads only, a person acted as with no mask, and a role acted
// as.
type readsApp[Tx any] struct {
	store  *overridingStore[Tx]
	server *httptest.Server
	api    *session.AuthAPI[session.NoCustomData, session.NoCustomData, Tx]
}

// viewMask is the mask of the read scenarios' view kind.
func viewMask() accesstypes.PermissionMask {
	return accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.List, accesstypes.Read)
}

// readsSession is GET /session's answer: the validated session as the application sees
// it, with what decides a session that acts as someone else.
type readsSession struct {
	SessionID    string `json:"sessionId"`
	Username     string `json:"username"`
	UserID       string `json:"userId"`
	User         string `json:"user"`
	Impersonated bool   `json:"impersonated"`
	Actor        string `json:"actor"`
	ActorID      string `json:"actorId"`
	Principal    string `json:"principal"`
	Mask         string `json:"mask"`
}

func newReadsApp[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) *readsApp[Tx] {
	t.Helper()

	_, library := be.newStore(ctx, t)
	store := &overridingStore[Tx]{AuthStore: library}
	auth, err := session.NewAuth[session.NoCustomData, session.NoCustomData, Tx](store, cookieKey, session.NoSuperAdministrators())
	if err != nil {
		t.Fatalf("session.NewAuth() over a store that wraps the library's error = %v", err)
	}
	api := auth.API()
	password := session.PasswordSignIn(auth)
	droids := session.RegisterMachineOutlet(auth, "droids")
	impersonations := session.MustNewImpersonations(session.NamedActions(grantView, grantFull, grantRole),
		session.ImpersonatesUser(kindView, grantView, viewMask()),
		session.ImpersonatesUser(kindFull, grantFull, accesstypes.AllowAll()),
		session.ImpersonatesRole(kindRole, grantRole, accesstypes.AllowAll()),
	)

	r := chi.NewRouter()
	r.Use(auth.StartSession, auth.SetXSRFToken)
	r.Get("/authenticated", auth.Authenticated())
	r.Group(func(r chi.Router) {
		r.Use(auth.ValidateXSRFToken)
		r.Post("/login", password.Login())

		r.Group(func(r chi.Router) {
			r.Use(auth.ValidateSession)
			r.Get("/session", func(w http.ResponseWriter, r *http.Request) {
				data, _ := r.Context().Value(sessioninfo.CtxSessionInfo).(*sessioninfo.SessionData)
				view := readsSession{SessionID: data.ID.String(), Username: data.Username, User: sessioninfo.UserFromRequest(r).ID.String()}
				if data.UserID.Valid {
					view.UserID = data.UserID.String()
				}
				view.Principal = sessioninfo.PrincipalFromCtx(r.Context()).String()
				if imp := data.Impersonation; imp != nil {
					view.Impersonated = true
					view.Actor = imp.Actor
					view.Mask = imp.Mask.Text()
					if imp.ActorID.Valid {
						view.ActorID = imp.ActorID.String()
					}
				}
				_ = httpio.NewEncoder(w).Ok(view)
			})
			r.Post("/tokens", auth.ApplicationTokens().Create())
			// A write of the application's, behind the backstop that refuses one in a
			// session that reads only.
			r.With(auth.EnforceReadOnlyMask).Post("/resource", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			})
			r.Post("/impersonate", func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Kind      string `json:"kind"`
					Principal string `json:"principal"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)

					return
				}
				req, err := impersonations.Request(r.Context(), &session.ImpersonationAsk{Kind: body.Kind, Principal: body.Principal}, anyoneMay)
				if err != nil {
					_ = httpio.NewEncoder(w).ClientMessage(r.Context(), err)

					return
				}
				var id impulse.UUID
				store.start(func() {
					id, err = api.StartImpersonatedSession(r.Context(), w, req)
				})
				if err != nil {
					_ = httpio.NewEncoder(w).ClientMessage(r.Context(), err)

					return
				}
				_ = httpio.NewEncoder(w).Ok(map[string]string{"sessionId": id.String()})
			})
			r.Post("/end", func(w http.ResponseWriter, r *http.Request) {
				restored, err := api.EndImpersonation(r.Context(), w)
				if err != nil {
					_ = httpio.NewEncoder(w).ClientMessage(r.Context(), err)

					return
				}
				_ = httpio.NewEncoder(w).Ok(map[string]bool{"restored": restored})
			})
		})
	})

	// The machine outlet sits outside StartSession: no cookie is read or written there.
	root := chi.NewRouter()
	root.Route("/droids", func(r chi.Router) {
		r.Use(droids.ValidateToken)
		r.Get("/whoami", outletWhoami)
	})
	root.Mount("/", r)

	server := httptest.NewTLSServer(root)
	t.Cleanup(server.Close)

	return &readsApp[Tx]{store: store, server: server, api: api}
}

// seed creates a user with seedPassword.
func (a *readsApp[Tx]) seed(ctx context.Context, t *testing.T, username string) impulse.UUID {
	t.Helper()

	pw := seedPassword
	id, err := a.api.CreateSessionUser(ctx, &session.CreateUserRequest{Username: username, Password: &pw})
	if err != nil {
		t.Fatalf("CreateSessionUser(%q) error = %v", username, err)
	}

	return id
}

// signedIn is a browser in which username has signed in with their password.
func (a *readsApp[Tx]) signedIn(ctx context.Context, t *testing.T, username string) *browser {
	t.Helper()

	b := newBrowser(t, a.server)
	b.prime(ctx)
	b.expect(ctx, http.StatusOK, http.MethodPost, "/login", map[string]string{"username": username, "password": seedPassword})

	return b
}

// asProgram asks the droids outlet whose request a bearer token's is, as a program
// does: no cookie jar, the token alone. It answers the status, and the account the
// request ran as when it was let through.
func (a *readsApp[Tx]) asProgram(ctx context.Context, t *testing.T, bearer string) (status int, user string) {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.server.URL+"/droids/whoami", http.NoBody)
	if err != nil {
		t.Fatalf("http.NewRequestWithContext() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := a.server.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /droids/whoami error = %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("io.ReadAll() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, ""
	}
	view := &outletView{}
	if err := json.Unmarshal(body, view); err != nil {
		t.Fatalf("json.Unmarshal(%s) error = %v", body, err)
	}

	return resp.StatusCode, view.User
}

// startAs has the person signed in in b start a session of kind as principal, and
// returns the session's id. The browser holds the new session's cookies afterwards.
func (b *browser) startAs(ctx context.Context, kind, principal string) impulse.UUID {
	b.t.Helper()

	var body struct {
		SessionID impulse.UUID `json:"sessionId"`
	}
	if err := json.Unmarshal(b.expect(ctx, http.StatusOK, http.MethodPost, "/impersonate", map[string]string{"kind": kind, "principal": principal}), &body); err != nil {
		b.t.Fatalf("json.Unmarshal() error = %v", err)
	}

	return body.SessionID
}

// readsSession reads the validated session.
func (b *browser) readsSession(ctx context.Context) *readsSession {
	b.t.Helper()

	v := &readsSession{}
	if err := json.Unmarshal(b.expect(ctx, http.StatusOK, http.MethodGet, "/session", nil), v); err != nil {
		b.t.Fatalf("json.Unmarshal() error = %v", err)
	}

	return v
}

// TestSealedReads_wrapperDecidesNoImpersonatedSession runs, on PostgreSQL and on Spanner
// alike, an application whose session store wraps the library's and answers Session,
// User and UserByUserName itself. What decides a session that acts as someone else is
// read through the library's own store: who started it, whom it acts as, its mask, and
// whether it has ended. So the wrapper's reads are never asked for any of it, and the
// session is the one the declaration decided, whatever the wrapper would have answered.
func TestSealedReads_wrapperDecidesNoImpersonatedSession(t *testing.T) {
	t.Parallel()

	t.Run(postgresBackend.name, func(t *testing.T) {
		t.Parallel()
		runSealedReads(t, postgresBackend)
	})
	t.Run(spannerBackend.name, func(t *testing.T) {
		t.Parallel()
		runSealedReads(t, spannerBackend)
	})
}

func runSealedReads[Tx any](t *testing.T, be backend[Tx]) {
	tests := []struct {
		name string
		run  func(ctx context.Context, t *testing.T, a *readsApp[Tx])
	}{
		{
			name: "a wrapper whose Session clears the mask of a view is never asked for the view, which stays reading only",
			run:  readsMaskCleared[Tx],
		},
		{
			name: "a wrapper whose Session names another account when the start reads the actor's session is never asked, and the record names the person who started it",
			run:  readsActorChangedAtTheStart[Tx],
		},
		{
			name: "a session the wrapper hands every request as another account's is refused as an invalid session, and starts no session that acts as someone else",
			run:  readsActorChangedOnEveryRequest[Tx],
		},
		{
			name: "a wrapper that answers one person's session with another person's live session has the request refused, so nothing is started as that person and no end hands their session over",
			run:  readsAnothersSession[Tx],
		},
		{
			name: "a wrapper whose User answers another account is never asked whom the session acts as: it acts as the person the request named",
			run:  readsAccountChanged[Tx],
		},
		{
			name: "a wrapper whose UserByUserName answers another account is never asked whose a session that acts as a role is: the person who started it",
			run:  readsRoleSessionAccountChanged[Tx],
		},
		{
			name: "a wrapper that keeps a cache of sessions does not keep a session that acts as someone else alive after it was ended",
			run:  readsEndedSessionCached[Tx],
		},
		{
			name: "a wrapper whose User answers an ordinary session with another account, or under another username, has the request refused as an invalid session",
			run:  readsOrdinaryAccountChanged[Tx],
		},
		{
			name: "a wrapper that keeps the accounts it has read does not keep a disabled account's session working, and still disables one itself",
			run:  readsDisabledAccountKept[Tx],
		},
		{
			name: "a wrapper that keeps the accounts it has read does not keep a disabled account's application token working, and does not run a token as another account",
			run:  readsTokenAccountKept[Tx],
		},
		{
			name: "a wrapper that keeps the accounts it has read does not have a disabled account issued an application token, and has none made for another account",
			run:  readsTokenIssueHeld[Tx],
		},
		{
			name: "a wrapper that keeps the accounts it has read does not have a disabled account's password accepted: the sign-in is refused, and so is the check of the credentials alone",
			run:  readsDisabledAccountSignIn[Tx],
		},
		{
			name: "a wrapper whose Session sets a principal on an ordinary session does not decide whom its checks are made for",
			run:  readsOrdinaryPrincipalSet[Tx],
		},
		{
			name: "a wrapper that keeps a cache of sessions does not keep an ordinary session alive after it was signed out",
			run:  readsOrdinarySessionCached[Tx],
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()

			tt.run(ctx, t, newReadsApp(ctx, t, be))
		})
	}
}

// readsMaskCleared is a wrapper that clears a mask: its Session hands on every session
// that acts as someone else with no mask.
func readsMaskCleared[Tx any](ctx context.Context, t *testing.T, a *readsApp[Tx]) {
	t.Helper()

	pat := a.seed(ctx, t, "pat@lakeside.edu")
	sam := a.seed(ctx, t, "sam@lakeside.edu")
	a.store.set(func(s *overridingStore[Tx]) {
		s.session = func(sess *sessioninfo.SessionData) *sessioninfo.SessionData {
			if sess.Impersonation == nil {
				return sess
			}
			unmasked := *sess.Impersonation
			unmasked.Mask = accesstypes.AllowAll()
			changed := *sess
			changed.Impersonation = &unmasked

			return &changed
		}
	})

	viewing := a.signedIn(ctx, t, "pat@lakeside.edu")
	id := viewing.startAs(ctx, kindView, sam.String())

	v := viewing.readsSession(ctx)
	if !v.Impersonated || v.SessionID != id.String() || v.Mask != viewMask().Text() {
		t.Errorf("the view's session = %+v, want session %s under the view kind's mask %q", v, id, viewMask().Text())
	}
	if v.User != sam.String() || v.UserID != sam.String() || v.Actor != "pat@lakeside.edu" || v.ActorID != pat.String() {
		t.Errorf("the view's session = %+v, want %s viewed by pat@lakeside.edu (%s)", v, sam, pat)
	}
	// What the mask refuses is refused: the view writes nothing.
	viewing.expect(ctx, http.StatusForbidden, http.MethodPost, "/resource", nil)
	if slices.Contains(a.store.asked().sessions, id) {
		t.Errorf("the wrapper's Session was asked for the view's session %s, want it never asked: what a session that acts as someone else is, is the library's own store's to say", id)
	}

	// The wrapper's Session is there and does clear the mask, so no call counted above
	// is the library not asking it, and not a wrapper that changes nothing.
	cleared, err := a.store.Session(ctx, id)
	if err != nil {
		t.Fatalf("the wrapper's Session(%s), called by the test, error = %v", id, err)
	}
	if cleared.Impersonation == nil || !cleared.Impersonation.Mask.IsZero() {
		t.Errorf("the wrapper's Session(%s), called by the test, answered %+v, want the session with its mask cleared", id, cleared.Impersonation)
	}
}

// asAccount is a change to a session that is nobody's impersonation: the same session,
// held under the account other.
func asAccount(other impulse.UUID) func(sess *sessioninfo.SessionData) *sessioninfo.SessionData {
	return func(sess *sessioninfo.SessionData) *sessioninfo.SessionData {
		if sess.Impersonation != nil {
			return sess
		}
		changed := *sess
		changed.UserID = impulse.NullUUID{UUID: other, Valid: true}

		return &changed
	}
}

// readsActorChangedAtTheStart is a wrapper that changes who started a session, and
// waits for the start to do it: the actor's own session, read again while a session
// that acts as someone else is started, is handed on under another account.
func readsActorChangedAtTheStart[Tx any](ctx context.Context, t *testing.T, a *readsApp[Tx]) {
	t.Helper()

	pat := a.seed(ctx, t, "pat@lakeside.edu")
	sam := a.seed(ctx, t, "sam@lakeside.edu")
	carol := a.seed(ctx, t, "carol@lakeside.edu")
	a.store.set(func(s *overridingStore[Tx]) {
		s.startSession = asAccount(carol)
	})

	acting := a.signedIn(ctx, t, "pat@lakeside.edu")
	own := acting.readsSession(ctx)
	id := acting.startAs(ctx, kindFull, sam.String())

	if starting := a.store.asked().sessionsStarting; len(starting) != 0 {
		t.Errorf("the wrapper's Session was asked for %v while the session was started, want it never asked: the actor's own session is read through the library's own store", starting)
	}
	v := acting.readsSession(ctx)
	if v.Actor != "pat@lakeside.edu" || v.ActorID != pat.String() {
		t.Errorf("the session's record names the actor %q (%s), want pat@lakeside.edu (%s), who started it", v.Actor, v.ActorID, pat)
	}
	records, err := a.api.ActiveImpersonations(ctx, nil)
	if err != nil {
		t.Fatalf("ActiveImpersonations() error = %v", err)
	}
	if len(records) != 1 || records[0].SessionID != id || records[0].ActorID != (impulse.NullUUID{UUID: pat, Valid: true}) {
		t.Errorf("ActiveImpersonations() = %+v, want the one record of session %s, started by %s", records, id, pat)
	}

	// The wrapper's Session is there, and during a start it does hand the actor's own
	// session on under another account.
	sourceID, err := impulse.UUIDFromString(own.SessionID)
	if err != nil {
		t.Fatalf("impulse.UUIDFromString(%q) error = %v", own.SessionID, err)
	}
	a.store.start(func() {
		changed, err := a.store.Session(ctx, sourceID)
		if err != nil {
			t.Fatalf("the wrapper's Session(%s), called by the test, error = %v", sourceID, err)
		}
		if changed.UserID.UUID != carol {
			t.Errorf("the wrapper's Session(%s), called by the test as a start would, answered the session of %s, want it handed on as %s's", sourceID, changed.UserID, carol)
		}
	})
}

// readsActorChangedOnEveryRequest is a wrapper that changes who started a session on
// every request: its Session hands the actor's own session on under another account,
// always. The library's own store holds the session under the account that signed in,
// so the answer is not that session's and the request is refused before any handler
// runs, the start among them.
func readsActorChangedOnEveryRequest[Tx any](ctx context.Context, t *testing.T, a *readsApp[Tx]) {
	t.Helper()

	a.seed(ctx, t, "pat@lakeside.edu")
	sam := a.seed(ctx, t, "sam@lakeside.edu")
	carol := a.seed(ctx, t, "carol@lakeside.edu")

	acting := a.signedIn(ctx, t, "pat@lakeside.edu")
	a.store.set(func(s *overridingStore[Tx]) {
		s.session = asAccount(carol)
	})

	acting.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/session", nil)
	acting.expect(ctx, http.StatusUnauthorized, http.MethodPost, "/impersonate", map[string]string{"kind": kindFull, "principal": sam.String()})
	records, err := a.api.ActiveImpersonations(ctx, nil)
	if err != nil {
		t.Fatalf("ActiveImpersonations() error = %v", err)
	}
	if len(records) != 0 {
		t.Errorf("ActiveImpersonations() = %+v, want none: the start was refused", records)
	}

	// The wrapper's Session is there and does hand the session on under the other
	// account, so the refusals above are the library's and not a wrapper that changes
	// nothing.
	a.store.set(func(s *overridingStore[Tx]) {
		s.session = nil
	})
	own := acting.readsSession(ctx)
	sourceID, err := impulse.UUIDFromString(own.SessionID)
	if err != nil {
		t.Fatalf("impulse.UUIDFromString(%q) error = %v", own.SessionID, err)
	}
	a.store.set(func(s *overridingStore[Tx]) {
		s.session = asAccount(carol)
	})
	changed, err := a.store.Session(ctx, sourceID)
	if err != nil {
		t.Fatalf("the wrapper's Session(%s), called by the test, error = %v", sourceID, err)
	}
	if changed.UserID.UUID != carol {
		t.Errorf("the wrapper's Session(%s), called by the test, answered the session of %s, want it handed on as %s's", sourceID, changed.UserID, carol)
	}
}

// readsAnothersSession is a wrapper that answers one person's session with another
// person's: a read cache in front of Session keyed by something other than the session's
// id would, and so would a wrapper that means harm. Bob's cookie names his own session,
// and the wrapper answers it with the live session of alice, who may start a session
// that acts as someone else.
//
// Left alone, bob's request would run as alice, a session he started would record alice
// as the person who started it, and its end would hand his browser the cookie of
// alice's own session, which the library's store answers with no wrapper involved. So
// the answer is refused: it is not the session bob's cookie names. Nothing is started,
// there is nothing to end, and bob's browser still holds bob's session once the wrapper
// answers truly again.
func readsAnothersSession[Tx any](ctx context.Context, t *testing.T, a *readsApp[Tx]) {
	t.Helper()

	a.seed(ctx, t, "alice@lakeside.edu")
	bob := a.seed(ctx, t, "bob@lakeside.edu")
	sam := a.seed(ctx, t, "sam@lakeside.edu")

	alices := a.signedIn(ctx, t, "alice@lakeside.edu")
	alicesSession := alices.readsSession(ctx)
	bobs := a.signedIn(ctx, t, "bob@lakeside.edu")
	bobsSession := bobs.readsSession(ctx)
	if bobsSession.SessionID == alicesSession.SessionID || bobsSession.UserID != bob.String() {
		t.Fatalf("the two sessions = %+v and %+v, want one of alice's and one of bob's (%s)", alicesSession, bobsSession, bob)
	}
	alicesSessionID, err := impulse.UUIDFromString(alicesSession.SessionID)
	if err != nil {
		t.Fatalf("impulse.UUIDFromString(%q) error = %v", alicesSession.SessionID, err)
	}
	bobsSessionID, err := impulse.UUIDFromString(bobsSession.SessionID)
	if err != nil {
		t.Fatalf("impulse.UUIDFromString(%q) error = %v", bobsSession.SessionID, err)
	}
	// The row of alice's live session, as the library's store holds it.
	alicesRow, err := a.store.AuthStore.Session(ctx, alicesSessionID)
	if err != nil {
		t.Fatalf("the library's store's Session(%s) error = %v", alicesSessionID, err)
	}
	anothers := func(sess *sessioninfo.SessionData) *sessioninfo.SessionData {
		if sess.ID != bobsSessionID {
			return sess
		}

		return alicesRow
	}
	a.store.set(func(s *overridingStore[Tx]) {
		s.session = anothers
	})

	// Bob's request does not run as alice.
	if status, body := bobs.do(ctx, http.MethodGet, "/session", nil); status != http.StatusUnauthorized {
		t.Errorf("GET /session with bob's cookie, answered by the wrapper with alice's session = %d, want %d: %s", status, http.StatusUnauthorized, body)
	}
	// Bob starts nothing as alice.
	if status, body := bobs.do(ctx, http.MethodPost, "/impersonate", map[string]string{"kind": kindFull, "principal": sam.String()}); status != http.StatusUnauthorized {
		t.Errorf("POST /impersonate with bob's cookie, answered by the wrapper with alice's session = %d, want %d: %s", status, http.StatusUnauthorized, body)
	}
	records, err := a.api.ActiveImpersonations(ctx, nil)
	if err != nil {
		t.Fatalf("ActiveImpersonations() error = %v", err)
	}
	if len(records) != 0 {
		t.Errorf("ActiveImpersonations() = %+v, want none: nothing was started", records)
	}

	// Whatever bob's browser holds now, ending it hands him no session of alice's. The
	// wrapper answers truly from here on, as a cache that was emptied would.
	_, _ = bobs.do(ctx, http.MethodPost, "/end", nil)
	a.store.set(func(s *overridingStore[Tx]) {
		s.session = nil
	})
	if v := bobs.readsSession(ctx); v.SessionID != bobsSession.SessionID || v.Username != "bob@lakeside.edu" || v.UserID != bob.String() || v.Impersonated {
		t.Errorf("the session bob's browser holds after the end = %+v, want his own session %s: it is never handed alice's session %s", v, bobsSession.SessionID, alicesSession.SessionID)
	}
	// Alice's own session is untouched.
	if v := alices.readsSession(ctx); v.SessionID != alicesSession.SessionID || v.Username != "alice@lakeside.edu" {
		t.Errorf("alice's session = %+v, want her own session %s", v, alicesSession.SessionID)
	}

	// The wrapper's Session is there and does answer bob's session with alice's.
	a.store.set(func(s *overridingStore[Tx]) {
		s.session = anothers
	})
	answered, err := a.store.Session(ctx, bobsSessionID)
	if err != nil {
		t.Fatalf("the wrapper's Session(%s), called by the test, error = %v", bobsSessionID, err)
	}
	if answered.ID != alicesSessionID {
		t.Errorf("the wrapper's Session(%s), called by the test, answered session %s, want alice's session %s", bobsSessionID, answered.ID, alicesSessionID)
	}
}

// readsAccountChanged is a wrapper that changes whom a session acts as: its User, asked
// for the person the request names, answers another account.
func readsAccountChanged[Tx any](ctx context.Context, t *testing.T, a *readsApp[Tx]) {
	t.Helper()

	a.seed(ctx, t, "pat@lakeside.edu")
	sam := a.seed(ctx, t, "sam@lakeside.edu")
	carol := a.seed(ctx, t, "carol@lakeside.edu")
	a.store.set(func(s *overridingStore[Tx]) {
		s.userInstead = map[impulse.UUID]impulse.UUID{sam: carol}
	})

	acting := a.signedIn(ctx, t, "pat@lakeside.edu")
	acting.startAs(ctx, kindFull, sam.String())

	v := acting.readsSession(ctx)
	if v.Username != "sam@lakeside.edu" || v.UserID != sam.String() || v.User != sam.String() {
		t.Errorf("the session = %+v, want sam@lakeside.edu's (%s), whom the request named", v, sam)
	}
	if want := accesstypes.UserPrincipal(accesstypes.User(sam.String())).String(); v.Principal != want {
		t.Errorf("the session's principal = %q, want %q", v.Principal, want)
	}
	if slices.Contains(a.store.asked().users, sam) {
		t.Errorf("the wrapper's User was asked for %s, want it never asked: the account a session acts as is read through the library's own store", sam)
	}

	// The wrapper's User is there and does answer the other account.
	other, err := a.store.User(ctx, sam)
	if err != nil {
		t.Fatalf("the wrapper's User(%s), called by the test, error = %v", sam, err)
	}
	if other.ID != carol {
		t.Errorf("the wrapper's User(%s), called by the test, answered the account %s, want %s", sam, other.ID, carol)
	}
}

// readsRoleSessionAccountChanged is the third sequence for a session that acts as a
// role, which answers for the person who started it: the wrapper's UserByUserName,
// asked for that person's name, answers another account.
func readsRoleSessionAccountChanged[Tx any](ctx context.Context, t *testing.T, a *readsApp[Tx]) {
	t.Helper()

	pat := a.seed(ctx, t, "pat@lakeside.edu")
	carol := a.seed(ctx, t, "carol@lakeside.edu")

	acting := a.signedIn(ctx, t, "pat@lakeside.edu")
	a.store.set(func(s *overridingStore[Tx]) {
		s.nameInstead = map[string]impulse.UUID{"pat@lakeside.edu": carol}
	})
	before := a.store.asked().names
	acting.startAs(ctx, kindRole, "auditor")

	v := acting.readsSession(ctx)
	if !v.Impersonated || v.Username != "pat@lakeside.edu" || v.User != pat.String() || v.ActorID != pat.String() {
		t.Errorf("the session that acts as a role = %+v, want pat@lakeside.edu's (%s), who started it", v, pat)
	}
	if names := a.store.asked().names; len(names) != len(before) {
		t.Errorf("the wrapper's UserByUserName was asked for %v by the session that acts as a role, want it never asked: whose such a session is, is read through the library's own store", names[len(before):])
	}

	// The wrapper's UserByUserName is there and does answer the other account.
	other, err := a.store.UserByUserName(ctx, "pat@lakeside.edu")
	if err != nil {
		t.Fatalf("the wrapper's UserByUserName(pat@lakeside.edu), called by the test, error = %v", err)
	}
	if other.ID != carol {
		t.Errorf("the wrapper's UserByUserName(pat@lakeside.edu), called by the test, answered the account %s, want %s", other.ID, carol)
	}
}

// readsEndedSessionCached is a wrapper with no bad intent: a read cache in front of
// Session, which holds a session that acts as someone else from before it was ended.
func readsEndedSessionCached[Tx any](ctx context.Context, t *testing.T, a *readsApp[Tx]) {
	t.Helper()

	a.seed(ctx, t, "pat@lakeside.edu")
	sam := a.seed(ctx, t, "sam@lakeside.edu")
	a.store.set(func(s *overridingStore[Tx]) {
		s.cache = map[impulse.UUID]*sessioninfo.SessionData{}
	})

	viewing := a.signedIn(ctx, t, "pat@lakeside.edu")
	id := viewing.startAs(ctx, kindView, sam.String())
	// The application reads the session through its store, as a page that lists who is
	// being viewed might, and the cache keeps the answer.
	if live, err := a.store.Session(ctx, id); err != nil || live.Expired || live.Impersonation == nil {
		t.Fatalf("the wrapper's Session(%s), called by the test, = %+v, %v, want the live view", id, live, err)
	}
	if v := viewing.readsSession(ctx); v.SessionID != id.String() || !v.Impersonated {
		t.Fatalf("the view's session = %+v, want session %s", v, id)
	}

	// An operator ends the view.
	if err := a.api.DestroyImpersonatedSession(ctx, id); err != nil {
		t.Fatalf("DestroyImpersonatedSession(%s) error = %v", id, err)
	}
	// The cache still answers the view as it was.
	if kept, err := a.store.Session(ctx, id); err != nil || kept.Expired || kept.Impersonation == nil || kept.Impersonation.EndedAt != nil {
		t.Fatalf("the wrapper's Session(%s) after the view was ended = %+v, %v, want the cache's answer from before: a live view", id, kept, err)
	}

	// The view's next request is refused all the same.
	viewing.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/session", nil)
	sessions := a.store.asked().sessions
	if n := len(slices.DeleteFunc(sessions, func(asked impulse.UUID) bool {
		return asked != id
	})); n != 2 {
		t.Errorf("the wrapper's Session was asked for the view's session %s %d times, want 2, the test's own two calls: the library never asks it for a session that acts as someone else", id, n)
	}
}

// readsOrdinaryAccountChanged is a wrapper whose User answers the account of a person's
// own session wrongly: first with another person's account, as a cache keyed by
// something other than the account's id would, and then with the right account under
// another name. Each request is refused as an invalid session, so no route for a
// person's own account acts on the other one, and the session is the person's again
// once the wrapper answers as the library's store does.
func readsOrdinaryAccountChanged[Tx any](ctx context.Context, t *testing.T, a *readsApp[Tx]) {
	t.Helper()

	pat := a.seed(ctx, t, "pat@lakeside.edu")
	sam := a.seed(ctx, t, "sam@lakeside.edu")
	own := a.signedIn(ctx, t, "pat@lakeside.edu")
	if v := own.readsSession(ctx); v.User != pat.String() || v.Impersonated {
		t.Fatalf("the session = %+v, want pat@lakeside.edu's own (%s)", v, pat)
	}

	a.store.set(func(s *overridingStore[Tx]) {
		s.userInstead = map[impulse.UUID]impulse.UUID{pat: sam}
	})
	own.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/session", nil)
	if !slices.Contains(a.store.asked().users, pat) {
		t.Errorf("the wrapper's User was never asked for %s: the account of an ordinary session is the store's to answer", pat)
	}

	a.store.set(func(s *overridingStore[Tx]) {
		s.userInstead = nil
		s.account = func(u *sessionstorage.SessionUser) *sessionstorage.SessionUser {
			renamed := *u
			renamed.Username = "sam@lakeside.edu"

			return &renamed
		}
	})
	own.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/session", nil)

	a.store.set(func(s *overridingStore[Tx]) {
		s.account = nil
	})
	if v := own.readsSession(ctx); v.User != pat.String() || v.Username != "pat@lakeside.edu" {
		t.Errorf("the session once the wrapper answers as the library's store does = %+v, want pat@lakeside.edu's own (%s)", v, pat)
	}
}

// readsDisabledAccountKept is a wrapper that answers every account as enabled, as a
// store that keeps the accounts it has read would answer one disabled since. The
// account is disabled in the library's store, and the person's next request is refused:
// whether an account is disabled is for the library's own row to say as well. Enabled
// again there, the session is the person's as before, so it was the row that refused
// it. A wrapper that answers an account as disabled which the library's row holds
// enabled is refused too: either says so.
func readsDisabledAccountKept[Tx any](ctx context.Context, t *testing.T, a *readsApp[Tx]) {
	t.Helper()

	pat := a.seed(ctx, t, "pat@lakeside.edu")
	own := a.signedIn(ctx, t, "pat@lakeside.edu")

	a.store.set(keptAs[Tx](false))
	if v := own.readsSession(ctx); v.User != pat.String() || v.Impersonated {
		t.Fatalf("the session = %+v, want pat@lakeside.edu's own (%s)", v, pat)
	}

	if err := a.store.DeactivateUser(ctx, pat); err != nil {
		t.Fatalf("DeactivateUser(%s) on the library's store error = %v", pat, err)
	}
	own.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/session", nil)
	if !slices.Contains(a.store.asked().users, pat) {
		t.Errorf("the wrapper's User was never asked for %s: the account of an ordinary session is the store's to answer", pat)
	}

	if err := a.store.ActivateUser(ctx, pat); err != nil {
		t.Fatalf("ActivateUser(%s) on the library's store error = %v", pat, err)
	}
	if v := own.readsSession(ctx); v.User != pat.String() {
		t.Errorf("the session once the library's store holds the account enabled again = %+v, want pat@lakeside.edu's own (%s)", v, pat)
	}

	a.store.set(keptAs[Tx](true))
	own.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/session", nil)

	a.store.set(func(s *overridingStore[Tx]) {
		s.account = nil
	})
	if v := own.readsSession(ctx); v.User != pat.String() || v.Username != "pat@lakeside.edu" {
		t.Errorf("the session once the wrapper answers as the library's store does = %+v, want pat@lakeside.edu's own (%s)", v, pat)
	}
}

// keptAs is a wrapper that keeps the accounts it has read: its User and its
// UserByUserName answer every account as disabled, or as not disabled, whatever the
// library's own row says since.
func keptAs[Tx any](disabled bool) func(*overridingStore[Tx]) {
	return func(s *overridingStore[Tx]) {
		s.account = func(u *sessionstorage.SessionUser) *sessionstorage.SessionUser {
			kept := *u
			kept.Disabled = disabled

			return &kept
		}
	}
}

// readsTokenAccountKept is a wrapper that answers the account of an application token
// wrongly, as it does a session's. A token's request is run as the account its row
// names, and the store's User is asked for that account: the answer is held to the
// library's own row as a session's is. The account is disabled in the library's store
// while the wrapper still answers it enabled, and the token's next request is refused;
// enabled again there, the token works as before, so it was the row that refused it. A
// wrapper that says the account is disabled refuses the token too: either says so. And
// a wrapper that answers the token's account with another person's, or under another
// username, has the request refused: a token is never run as another account.
func readsTokenAccountKept[Tx any](ctx context.Context, t *testing.T, a *readsApp[Tx]) {
	t.Helper()

	pat := a.seed(ctx, t, "pat@lakeside.edu")
	sam := a.seed(ctx, t, "sam@lakeside.edu")
	token := a.signedIn(ctx, t, "pat@lakeside.edu").createToken(ctx, "laptop", "droids").Token
	runsAs := func(when string, wantStatus int) {
		t.Helper()

		status, user := a.asProgram(ctx, t, token)
		if status != wantStatus || (status == http.StatusOK && user != pat.String()) {
			t.Errorf("the token's request %s = %d as %q, want %d (as pat@lakeside.edu, %s, when let through)", when, status, user, wantStatus, pat)
		}
	}

	a.store.set(keptAs[Tx](false))
	runsAs("while the account is enabled", http.StatusOK)

	if err := a.store.DeactivateUser(ctx, pat); err != nil {
		t.Fatalf("DeactivateUser(%s) on the library's store error = %v", pat, err)
	}
	runsAs("once the library's store holds the account disabled and the wrapper still answers it enabled", http.StatusUnauthorized)
	if !slices.Contains(a.store.asked().users, pat) {
		t.Errorf("the wrapper's User was never asked for %s: the account of a token is the store's to answer", pat)
	}

	if err := a.store.ActivateUser(ctx, pat); err != nil {
		t.Fatalf("ActivateUser(%s) on the library's store error = %v", pat, err)
	}
	runsAs("once the library's store holds the account enabled again", http.StatusOK)

	a.store.set(keptAs[Tx](true))
	runsAs("while the wrapper answers the account as disabled", http.StatusUnauthorized)

	a.store.set(func(s *overridingStore[Tx]) {
		s.account = nil
		s.userInstead = map[impulse.UUID]impulse.UUID{pat: sam}
	})
	runsAs("while the wrapper answers the token's account with another person's", http.StatusUnauthorized)

	a.store.set(func(s *overridingStore[Tx]) {
		s.userInstead = nil
		s.account = func(u *sessionstorage.SessionUser) *sessionstorage.SessionUser {
			renamed := *u
			renamed.Username = "sam@lakeside.edu"

			return &renamed
		}
	})
	runsAs("while the wrapper answers the token's account under another username", http.StatusUnauthorized)

	a.store.set(func(s *overridingStore[Tx]) {
		s.account = nil
	})
	runsAs("once the wrapper answers as the library's store does", http.StatusOK)
}

// readsTokenIssueHeld is a wrapper that keeps the accounts it has read, at the issue of
// an application token (CreateApplicationToken): the account is disabled in the
// library's store and the wrapper's User still answers it enabled. No token is issued,
// refused with the words a disabled account's issue gets, where one was issued that its
// first request refused. Enabled again in the library's store, the token is issued and
// its request runs as the person. A wrapper that says the account is disabled refuses
// the issue too: either says so. And a wrapper that answers the account asked for with
// another person's has the issue fail, with no token made for either of them.
func readsTokenIssueHeld[Tx any](ctx context.Context, t *testing.T, a *readsApp[Tx]) {
	t.Helper()

	pat := a.seed(ctx, t, "pat@lakeside.edu")
	sam := a.seed(ctx, t, "sam@lakeside.edu")
	issue := func() (*session.IssuedToken, error) {
		return a.api.CreateApplicationToken(ctx, &session.ApplicationTokenRequest{UserID: pat, Outlet: "droids", Name: "laptop"})
	}
	tokens := func(of impulse.UUID) int {
		t.Helper()

		rows, err := a.api.Tokens(ctx, of)
		if err != nil {
			t.Fatalf("Tokens(%s) error = %v", of, err)
		}

		return len(rows)
	}
	refused := func(when string) {
		t.Helper()

		if _, err := issue(); !httpio.HasBadRequest(err) || httpio.Message(err) != "the user is disabled" {
			t.Errorf("CreateApplicationToken() %s error = %v, want the refusal of a disabled account's issue", when, err)
		}
		if n := tokens(pat); n != 0 {
			t.Errorf("pat@lakeside.edu holds %d token(s) %s, want none", n, when)
		}
	}

	a.store.set(keptAs[Tx](false))
	if err := a.store.DeactivateUser(ctx, pat); err != nil {
		t.Fatalf("DeactivateUser(%s) on the library's store error = %v", pat, err)
	}
	refused("once the library's store holds the account disabled and the wrapper still answers it enabled")
	if !slices.Contains(a.store.asked().users, pat) {
		t.Errorf("the wrapper's User was never asked for %s: the account a token is issued for is the store's to answer", pat)
	}

	if err := a.store.ActivateUser(ctx, pat); err != nil {
		t.Fatalf("ActivateUser(%s) on the library's store error = %v", pat, err)
	}
	a.store.set(keptAs[Tx](true))
	refused("while the wrapper answers the account as disabled")

	a.store.set(func(s *overridingStore[Tx]) {
		s.account = nil
		s.userInstead = map[impulse.UUID]impulse.UUID{pat: sam}
	})
	if issued, err := issue(); err == nil {
		t.Errorf("CreateApplicationToken() while the wrapper answers the account asked for with another person's issued a token for %s, want none issued", issued.Token.UserID)
	}
	if p, s := tokens(pat), tokens(sam); p != 0 || s != 0 {
		t.Errorf("tokens held while the wrapper answers the account asked for with another person's: pat@lakeside.edu %d, sam@lakeside.edu %d; want none", p, s)
	}

	a.store.set(func(s *overridingStore[Tx]) {
		s.userInstead = nil
	})
	issued, err := issue()
	if err != nil {
		t.Fatalf("CreateApplicationToken() once the wrapper answers as the library's store does error = %v", err)
	}
	if status, user := a.asProgram(ctx, t, issued.Secret); status != http.StatusOK || user != pat.String() {
		t.Errorf("the issued token's request = %d as %q, want 200 as pat@lakeside.edu (%s)", status, user, pat)
	}
	if p, s := tokens(pat), tokens(sam); p != 1 || s != 0 {
		t.Errorf("tokens held once the wrapper answers as the library's store does: pat@lakeside.edu %d, sam@lakeside.edu %d; want one and none", p, s)
	}
}

// readsDisabledAccountSignIn is a wrapper that keeps the accounts it has read, at the
// sign-in: the account is disabled in the library's store and the wrapper's
// UserByUserName still answers it enabled. The password sign-in is refused there and
// then, with the words a disabled account's sign-in gets, and no session is made; the
// check of a username and a password that starts no session (ValidateCredentials) is
// refused the same way, where nothing after it reads the library's row. Enabled again
// in the library's store, the same password signs the person in. A wrapper that says
// the account is disabled refuses both too: either says so.
func readsDisabledAccountSignIn[Tx any](ctx context.Context, t *testing.T, a *readsApp[Tx]) {
	t.Helper()

	pat := a.seed(ctx, t, "pat@lakeside.edu")
	refused := func(when string) {
		t.Helper()

		b := newBrowser(t, a.server)
		b.prime(ctx)
		status, _, body := b.postLogin(ctx, "pat@lakeside.edu", seedPassword)
		var answer struct {
			Code sessioninfo.LoginRefusalCode `json:"code"`
		}
		_ = json.Unmarshal(body, &answer)
		if status != http.StatusUnauthorized || answer.Code != sessioninfo.RefusedUserDisabled {
			t.Errorf("the password sign-in %s = %d %s, want 401 with the code %s", when, status, body, sessioninfo.RefusedUserDisabled)
		}
		b.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/session", nil)
		if id, err := a.api.ValidateCredentials(ctx, "pat@lakeside.edu", seedPassword); !errors.Is(err, signin.ErrUserDisabled) {
			t.Errorf("ValidateCredentials() %s = %s, %v; want the refusal of a disabled account's sign-in", when, id, err)
		}
	}

	a.store.set(keptAs[Tx](false))
	if err := a.store.DeactivateUser(ctx, pat); err != nil {
		t.Fatalf("DeactivateUser(%s) on the library's store error = %v", pat, err)
	}
	refused("once the library's store holds the account disabled and the wrapper still answers it enabled")
	if !slices.Contains(a.store.asked().names, "pat@lakeside.edu") {
		t.Error("the wrapper's UserByUserName was never asked for pat@lakeside.edu: the account of a password sign-in is the store's to answer")
	}

	if err := a.store.ActivateUser(ctx, pat); err != nil {
		t.Fatalf("ActivateUser(%s) on the library's store error = %v", pat, err)
	}
	if id, err := a.api.ValidateCredentials(ctx, "pat@lakeside.edu", seedPassword); err != nil || id != pat {
		t.Errorf("ValidateCredentials() once the library's store holds the account enabled again = %s, %v; want %s", id, err, pat)
	}
	if v := a.signedIn(ctx, t, "pat@lakeside.edu").readsSession(ctx); v.User != pat.String() {
		t.Errorf("the session of the sign-in once the library's store holds the account enabled again = %+v, want pat@lakeside.edu's own (%s)", v, pat)
	}

	a.store.set(keptAs[Tx](true))
	refused("while the wrapper answers the account as disabled")
}

// readsOrdinaryPrincipalSet is a wrapper whose Session hands every ordinary session on
// with a principal of its choosing, another person's. No resolver is configured, so the
// checks of the session are made for the session's own user: a principal is the
// resolver's to choose, and a store's answer does not carry one.
func readsOrdinaryPrincipalSet[Tx any](ctx context.Context, t *testing.T, a *readsApp[Tx]) {
	t.Helper()

	pat := a.seed(ctx, t, "pat@lakeside.edu")
	sam := a.seed(ctx, t, "sam@lakeside.edu")
	a.store.set(func(s *overridingStore[Tx]) {
		s.session = func(sess *sessioninfo.SessionData) *sessioninfo.SessionData {
			if sess.Impersonation != nil {
				return sess
			}
			changed := *sess
			changed.Principal = accesstypes.UserPrincipal(accesstypes.User(sam.String()))

			return &changed
		}
	})

	own := a.signedIn(ctx, t, "pat@lakeside.edu")
	v := own.readsSession(ctx)
	if want := accesstypes.UserPrincipal(accesstypes.User(pat.String())).String(); v.Principal != want {
		t.Errorf("the session's principal = %q, want %q: the session's own user, whatever the wrapper's Session set", v.Principal, want)
	}
	if v.User != pat.String() {
		t.Errorf("the session = %+v, want pat@lakeside.edu's own (%s)", v, pat)
	}
}

// readsOrdinarySessionCached is readsEndedSessionCached for a person's own session: the
// wrapper keeps every session it has answered, the session is signed out in the
// library's store, and the cache still answers it as live. The next request is refused
// all the same, since whether a session has ended is the library's own row's to say.
func readsOrdinarySessionCached[Tx any](ctx context.Context, t *testing.T, a *readsApp[Tx]) {
	t.Helper()

	a.seed(ctx, t, "pat@lakeside.edu")
	a.store.set(func(s *overridingStore[Tx]) {
		s.cache = map[impulse.UUID]*sessioninfo.SessionData{}
	})

	own := a.signedIn(ctx, t, "pat@lakeside.edu")
	id, err := impulse.UUIDFromString(own.readsSession(ctx).SessionID)
	if err != nil {
		t.Fatalf("impulse.UUIDFromString() error = %v", err)
	}

	// The session is signed out where the library keeps it, past the wrapper.
	if err := a.store.DestroySession(ctx, id); err != nil {
		t.Fatalf("DestroySession(%s) error = %v", id, err)
	}
	if kept, err := a.store.Session(ctx, id); err != nil || kept.Expired {
		t.Fatalf("the wrapper's Session(%s) after the sign-out = %+v, %v, want the cache's answer from before: a live session", id, kept, err)
	}

	own.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/session", nil)
}
