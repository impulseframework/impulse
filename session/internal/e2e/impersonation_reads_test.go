package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
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
		return nil, err
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
	s.mu.Unlock()

	if replaced {
		id = instead
	}

	return s.AuthStore.User(ctx, id)
}

// UserByUserName is the application's own read of an account by its name, in front of
// the library's.
func (s *overridingStore[Tx]) UserByUserName(ctx context.Context, username string) (*sessionstorage.SessionUser, error) {
	s.mu.Lock()
	s.namesAsked = append(s.namesAsked, username)
	instead, replaced := s.nameInstead[username]
	s.mu.Unlock()

	if replaced {
		return s.AuthStore.User(ctx, instead)
	}

	return s.AuthStore.UserByUserName(ctx, username)
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
func (s *overridingStore[Tx]) asked() (sessions, sessionsStarting, users []impulse.UUID, names []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Clone(s.sessionsAsked), slices.Clone(s.sessionsAskedStarting), slices.Clone(s.usersAsked), slices.Clone(s.namesAsked)
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
				if imp := data.Impersonation; imp != nil {
					view.Impersonated = true
					view.Actor = imp.Actor
					view.Principal = imp.Principal.String()
					view.Mask = imp.Mask.Text()
					if imp.ActorID.Valid {
						view.ActorID = imp.ActorID.String()
					}
				}
				_ = httpio.NewEncoder(w).Ok(view)
			})
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
		})
	})

	server := httptest.NewTLSServer(r)
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
			name: "a session the wrapper hands every request as another account's starts no session that acts as someone else",
			run:  readsActorChangedOnEveryRequest[Tx],
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
	if sessions, _, _, _ := a.store.asked(); slices.Contains(sessions, id) {
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

	if _, starting, _, _ := a.store.asked(); len(starting) != 0 {
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
// always. The request then arrives as that account's, and the library's own store says
// otherwise.
func readsActorChangedOnEveryRequest[Tx any](ctx context.Context, t *testing.T, a *readsApp[Tx]) {
	t.Helper()

	a.seed(ctx, t, "pat@lakeside.edu")
	sam := a.seed(ctx, t, "sam@lakeside.edu")
	carol := a.seed(ctx, t, "carol@lakeside.edu")

	acting := a.signedIn(ctx, t, "pat@lakeside.edu")
	a.store.set(func(s *overridingStore[Tx]) {
		s.session = asAccount(carol)
	})
	if own := acting.readsSession(ctx); own.UserID != carol.String() || own.Username != "pat@lakeside.edu" {
		t.Fatalf("the actor's own session = %+v, want the wrapper's answer: pat@lakeside.edu's session under %s", own, carol)
	}

	acting.expect(ctx, http.StatusForbidden, http.MethodPost, "/impersonate", map[string]string{"kind": kindFull, "principal": sam.String()})
	records, err := a.api.ActiveImpersonations(ctx, nil)
	if err != nil {
		t.Fatalf("ActiveImpersonations() error = %v", err)
	}
	if len(records) != 0 {
		t.Errorf("ActiveImpersonations() = %+v, want none: the start was refused", records)
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
	if _, _, users, _ := a.store.asked(); slices.Contains(users, sam) {
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
	_, _, _, before := a.store.asked()
	acting.startAs(ctx, kindRole, "auditor")

	v := acting.readsSession(ctx)
	if !v.Impersonated || v.Username != "pat@lakeside.edu" || v.User != pat.String() || v.ActorID != pat.String() {
		t.Errorf("the session that acts as a role = %+v, want pat@lakeside.edu's (%s), who started it", v, pat)
	}
	if _, _, _, names := a.store.asked(); len(names) != len(before) {
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
	sessions, _, _, _ := a.store.asked()
	if n := len(slices.DeleteFunc(sessions, func(asked impulse.UUID) bool {
		return asked != id
	})); n != 2 {
		t.Errorf("the wrapper's Session was asked for the view's session %s %d times, want 2, the test's own two calls: the library never asks it for a session that acts as someone else", id, n)
	}
}
