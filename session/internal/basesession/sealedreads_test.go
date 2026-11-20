package basesession

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/cookie"
	"github.com/impulseframework/impulse/session/internal/mockstore"
	"github.com/impulseframework/impulse/session/mock/mock_cookie"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/session/sessionstorage/mock/mock_sessionstorage"
	gomock "go.uber.org/mock/gomock"
)

// wrappedStore is a store an application wraps around one the library made: it embeds
// the store, so it has the library's write and reads by promotion, and it declares a
// Session of its own, as a wrapper may. answer is what that Session returns, and asked
// records every session it was asked for.
type wrappedStore struct {
	*mockstore.BaseStore
	answer func(sessionID impulse.UUID) (*sessioninfo.SessionData, error)
	asked  []impulse.UUID
}

// Session is the wrapper's own answer, in front of the store it embeds.
func (w *wrappedStore) Session(_ context.Context, sessionID impulse.UUID) (*sessioninfo.SessionData, error) {
	w.asked = append(w.asked, sessionID)

	return w.answer(sessionID)
}

// impersonatedSession is a live session of bob that alice views, reading only.
func impersonatedSession(id impulse.UUID) *sessioninfo.SessionData {
	return &sessioninfo.SessionData{
		SessionInfo: &sessioninfo.SessionInfo{ID: id, Username: "bob", UpdatedAt: time.Now()},
		Impersonation: &sessioninfo.Impersonation{
			SessionID: id,
			Actor:     "alice",
			Principal: accesstypes.UserPrincipal("bob"),
			Mask:      accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.List, accesstypes.Read),
			StartedAt: time.Now().Add(-time.Minute),
			ExpiresAt: time.Now().Add(time.Hour),
		},
	}
}

// TestBaseSession_ValidateSessionAPI_whatAWrapperDecides holds what decides an
// impersonated session to the library's own store. A store that wraps the library's
// declares a Session of its own; the library reads the session of a request through its
// own read first, which the wrapper has by promotion and cannot replace. A session that
// read finds impersonated is that session, and the wrapper is never asked: it cannot
// clear the mask, and a cache it keeps cannot serve an ended session. Every other
// session is the wrapper's to answer, as an ordinary one alone, and as the session that
// was asked for: its id, and where the library's own store holds it, its account and
// its username.
func TestBaseSession_ValidateSessionAPI_whatAWrapperDecides(t *testing.T) {
	t.Parallel()

	sessionID := impulse.Must(impulse.NewUUID())
	otherSessionID := impulse.Must(impulse.NewUUID())
	bobID := impulse.Must(impulse.NewUUID())
	aliceID := impulse.Must(impulse.NewUUID())

	// unmasked is the stored session as a wrapper would hand it on with its mask
	// cleared: the zero mask keeps every permission.
	unmasked := func(impulse.UUID) (*sessioninfo.SessionData, error) {
		sess := impersonatedSession(sessionID)
		sess.Impersonation.Mask = accesstypes.PermissionMask{}

		return sess, nil
	}
	ordinary := func(username string) func(impulse.UUID) (*sessioninfo.SessionData, error) {
		return func(impulse.UUID) (*sessioninfo.SessionData, error) {
			return ordinarySession(sessionID, username, time.Now()), nil
		}
	}
	// anothers is a wrapper that answers the session asked for with alice's own live
	// session, as a read cache keyed by something other than the session's id would.
	anothers := func(impulse.UUID) (*sessioninfo.SessionData, error) {
		return accountSession(otherSessionID, "alice", aliceID, time.Now()), nil
	}

	tests := []struct {
		name string
		// stored is what the library's own store holds, and storedErr its error.
		stored    *sessioninfo.SessionData
		storedErr error
		// answer is the wrapper's Session.
		answer func(impulse.UUID) (*sessioninfo.SessionData, error)
		// wantAsked is whether the wrapper's Session is asked.
		wantAsked        bool
		wantUnauthorized bool
		wantUsername     string
		wantImpersonated bool
	}{
		{
			name:             "an impersonated session is the stored one: a wrapper that clears the mask is never asked",
			stored:           impersonatedSession(sessionID),
			answer:           unmasked,
			wantUsername:     "bob",
			wantImpersonated: true,
		},
		{
			name: "an ended impersonated session is refused: a wrapper that kept the live one is never asked",
			stored: func() *sessioninfo.SessionData {
				sess := impersonatedSession(sessionID)
				sess.Expired = true
				ended := time.Now()
				sess.Impersonation.EndedAt = &ended
				sess.Impersonation.EndReason = sessioninfo.ImpersonationEndedByRevocation

				return sess
			}(),
			answer: func(impulse.UUID) (*sessioninfo.SessionData, error) {
				return impersonatedSession(sessionID), nil
			},
			wantUnauthorized: true,
		},
		{
			name:         "an ordinary session is the wrapper's to answer",
			stored:       ordinarySession(sessionID, "bob", time.Now().Add(-time.Second)),
			answer:       ordinary("bob"),
			wantAsked:    true,
			wantUsername: "bob",
		},
		{
			name:             "a wrapper cannot answer an ordinary session under another username than the library's store holds it under",
			stored:           ordinarySession(sessionID, "bob", time.Now()),
			answer:           ordinary("robert"),
			wantAsked:        true,
			wantUnauthorized: true,
		},
		{
			name:   "a wrapper cannot answer an ordinary session as another account's than the library's store holds it under",
			stored: accountSession(sessionID, "bob", bobID, time.Now()),
			answer: func(impulse.UUID) (*sessioninfo.SessionData, error) {
				return accountSession(sessionID, "bob", aliceID, time.Now()), nil
			},
			wantAsked:        true,
			wantUnauthorized: true,
		},
		{
			name:             "a wrapper cannot answer an ordinary session with another person's session",
			stored:           accountSession(sessionID, "bob", bobID, time.Now()),
			answer:           anothers,
			wantAsked:        true,
			wantUnauthorized: true,
		},
		{
			name:             "a wrapper cannot answer a session the library's store does not hold with another session",
			storedErr:        httpio.NewNotFoundMessagef("session %q not found", sessionID),
			answer:           anothers,
			wantAsked:        true,
			wantUnauthorized: true,
		},
		{
			name:         "a session the library's store does not hold is the wrapper's to answer",
			storedErr:    httpio.NewNotFoundMessagef("session %q not found", sessionID),
			answer:       ordinary("robert"),
			wantAsked:    true,
			wantUsername: "robert",
		},
		{
			name:             "a wrapper cannot make an ordinary session an impersonated one",
			stored:           ordinarySession(sessionID, "bob", time.Now()),
			answer:           unmasked,
			wantAsked:        true,
			wantUnauthorized: true,
		},
		{
			name:             "a wrapper cannot make a session the library's store does not hold an impersonated one",
			storedErr:        httpio.NewNotFoundMessagef("session %q not found", sessionID),
			answer:           unmasked,
			wantAsked:        true,
			wantUnauthorized: true,
		},
		{
			name:             "a read of the library's own that fails is the request's failure, and the wrapper is not asked",
			storedErr:        errors.New("the database is away"),
			answer:           ordinary("robert"),
			wantUnauthorized: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			inner := mockstore.NewBaseStore(ctrl)
			inner.EXPECT().Session(gomock.Any(), sessionID).Return(tt.stored, tt.storedErr)
			storage := &wrappedStore{BaseStore: inner, answer: tt.answer}

			s := &BaseSession{SessionTimeout: time.Minute, Storage: storage}
			ctx := context.WithValue(context.Background(), sessioninfo.CTXSessionID, sessionID)
			gotCtx, err := s.ValidateSessionAPI(ctx)
			if httpio.HasUnauthorized(err) != tt.wantUnauthorized || (err != nil) != tt.wantUnauthorized {
				t.Fatalf("ValidateSessionAPI() error = %v, wantUnauthorized %v", err, tt.wantUnauthorized)
			}
			if asked := len(storage.asked) != 0; asked != tt.wantAsked {
				t.Errorf("the wrapper's Session was asked for %v, want asked = %v", storage.asked, tt.wantAsked)
			}
			if tt.wantUnauthorized {
				return
			}

			if got := sessioninfo.FromCtx(gotCtx).Username; got != tt.wantUsername {
				t.Errorf("the session's username = %q, want %q", got, tt.wantUsername)
			}
			imp, impersonated := sessioninfo.ImpersonationFromCtx(gotCtx)
			if impersonated != tt.wantImpersonated {
				t.Fatalf("the session is impersonated = %v, want %v", impersonated, tt.wantImpersonated)
			}
			if !impersonated {
				return
			}
			if imp != tt.stored.Impersonation {
				t.Errorf("the session's record is not the one the library's store holds")
			}
			if mask := sessioninfo.MaskFromCtx(gotCtx); !mask.ReadOnly() {
				t.Errorf("the session's mask = %q, want the stored one, reading only", mask.Words())
			}
		})
	}
}

// TestBaseSession_ValidateSessionAPI_aStoreWithNoLibraryStore holds a session of a
// store that has none of the library's reads (a mock of the store, and a store of an
// application's own, which embeds sessionstorage.NoImpersonatedSessions) to an ordinary
// one: its Session answers, and an answer that carries an impersonation record is
// refused, since no store of the library's says the session is impersonated.
func TestBaseSession_ValidateSessionAPI_aStoreWithNoLibraryStore(t *testing.T) {
	t.Parallel()

	sessionID := impulse.Must(impulse.NewUUID())
	stores := map[string]func(ctrl *gomock.Controller, answer *sessioninfo.SessionData) sessionstorage.BaseStore{
		"a mock of the store": func(ctrl *gomock.Controller, answer *sessioninfo.SessionData) sessionstorage.BaseStore {
			storage := mock_sessionstorage.NewMockBaseStore(ctrl)
			storage.EXPECT().Session(gomock.Any(), sessionID).Return(answer, nil)

			return storage
		},
		"a store of an application's own": func(ctrl *gomock.Controller, answer *sessioninfo.SessionData) sessionstorage.BaseStore {
			methods := mock_sessionstorage.NewMockBaseStoreMethods(ctrl)
			methods.EXPECT().Session(gomock.Any(), sessionID).Return(answer, nil)

			return struct {
				*mock_sessionstorage.MockBaseStoreMethods
				sessionstorage.NoImpersonatedSessions
			}{MockBaseStoreMethods: methods}
		},
	}
	for name, store := range stores {
		t.Run(name+": an ordinary session is validated", func(t *testing.T) {
			t.Parallel()
			s := &BaseSession{SessionTimeout: time.Minute, Storage: store(gomock.NewController(t), ordinarySession(sessionID, "bob", time.Now()))}
			gotCtx, err := s.ValidateSessionAPI(context.WithValue(context.Background(), sessioninfo.CTXSessionID, sessionID))
			if err != nil {
				t.Fatalf("ValidateSessionAPI() error = %v, want none", err)
			}
			if got := sessioninfo.FromCtx(gotCtx).Username; got != "bob" {
				t.Errorf("the session's username = %q, want bob", got)
			}
		})
		t.Run(name+": a session answered with another session is refused", func(t *testing.T) {
			t.Parallel()
			another := ordinarySession(impulse.Must(impulse.NewUUID()), "alice", time.Now())
			s := &BaseSession{SessionTimeout: time.Minute, Storage: store(gomock.NewController(t), another)}
			_, err := s.ValidateSessionAPI(context.WithValue(context.Background(), sessioninfo.CTXSessionID, sessionID))
			if !httpio.HasUnauthorized(err) {
				t.Fatalf("ValidateSessionAPI() error = %v, want the session refused as invalid", err)
			}
		})
		t.Run(name+": a session answered with an impersonation record is refused", func(t *testing.T) {
			t.Parallel()
			s := &BaseSession{SessionTimeout: time.Minute, Storage: store(gomock.NewController(t), impersonatedSession(sessionID))}
			_, err := s.ValidateSessionAPI(context.WithValue(context.Background(), sessioninfo.CTXSessionID, sessionID))
			if !httpio.HasUnauthorized(err) {
				t.Fatalf("ValidateSessionAPI() error = %v, want the session refused as invalid", err)
			}
		})
	}
}

// TestBaseSession_StartImpersonatedSession_theActorIsTheStoredOne holds who is recorded
// as the actor to the library's own store. The actor's own session is read through the
// library's read, so the Session of a store that wraps the library's is never asked for
// it and cannot name another account as the actor. And the session the call arrived on
// must be, to the library's own store, the session of the account it reached the
// request as: a wrapper that answered it as another account's starts nothing.
func TestBaseSession_StartImpersonatedSession_theActorIsTheStoredOne(t *testing.T) {
	t.Parallel()

	sessionID := impulse.Must(impulse.NewUUID())
	sourceID := impulse.Must(impulse.NewUUID())
	aliceID := impulse.Must(impulse.NewUUID())
	malloryID := impulse.Must(impulse.NewUUID())

	onSession := func(sess *sessioninfo.SessionData) context.Context {
		ctx := context.WithValue(context.Background(), sessioninfo.CTXSessionID, sess.ID)

		return context.WithValue(ctx, sessioninfo.CtxSessionInfo, sess)
	}
	tests := []struct {
		name string
		// arrivedAs is the session the call arrived on, as validation put it in the
		// request.
		arrivedAs     *sessioninfo.SessionData
		wantForbidden bool
	}{
		{
			name:      "the record takes the actor's account from the library's own row, and the wrapper is never asked",
			arrivedAs: accountSession(sourceID, "alice", aliceID, time.Now()),
		},
		{
			name:          "a session that reached the request as another account's than the store holds it under starts nothing",
			arrivedAs:     accountSession(sourceID, "alice", malloryID, time.Now()),
			wantForbidden: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			inner := mockstore.NewBaseStore(ctrl)
			inner.EXPECT().Session(gomock.Any(), sourceID).Return(accountSession(sourceID, "alice", aliceID, time.Now()), nil)
			cookieHandler := mock_cookie.NewMockHandler(ctrl)
			if !tt.wantForbidden {
				inner.Sealed.EXPECT().CreateImpersonatedSession(gomock.Any(), gomock.Any(), gomock.Any()).Return(sessionID, nil)
				cookieHandler.EXPECT().NewAuthCookie(gomock.Any(), true, sessionID).Return(cookie.NewValues())
				cookieHandler.EXPECT().CreateXSRFTokenCookie(gomock.Any(), sessionID)
			}
			// The wrapper's Session answers the actor's session as the session of another
			// account, under the actor's username.
			storage := &wrappedStore{BaseStore: inner, answer: func(impulse.UUID) (*sessioninfo.SessionData, error) {
				return accountSession(sourceID, "alice", malloryID, time.Now()), nil
			}}

			s := New(storage)
			s.SessionTimeout = 10 * time.Minute
			s.CookieHandler = cookieHandler
			imp := &sessioninfo.Impersonation{Actor: "alice", Principal: accesstypes.RolePrincipal("Editor")}
			req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonImpersonation, Username: "alice"}
			_, err := s.StartImpersonatedSession(onSession(tt.arrivedAs), httptest.NewRecorder(), req, imp)
			if httpio.HasForbidden(err) != tt.wantForbidden || (err != nil) != tt.wantForbidden {
				t.Fatalf("StartImpersonatedSession() error = %v, wantForbidden %v", err, tt.wantForbidden)
			}
			if diff := cmp.Diff([]impulse.UUID(nil), storage.asked); diff != "" {
				t.Errorf("the wrapper's Session was asked (-want +got):\n%s", diff)
			}
			if tt.wantForbidden {
				return
			}
			if want := impulse.NullUUIDFromUUID(aliceID); imp.ActorID != want {
				t.Errorf("the record's ActorID = %v, want %v, the account the library's own row names", imp.ActorID, want)
			}
		})
	}
}

// TestBaseSession_StartImpersonatedSession_theSourceIsTheCookiesSession holds the session
// a person starts from to the one the request's cookie names. A request validated as
// another session than its cookie names, which is what a store that answered one
// person's cookie with another person's live session would make of it, starts nothing:
// the library's store is asked for no session, and nothing is written. And a start that
// names no source session takes the cookie's.
func TestBaseSession_StartImpersonatedSession_theSourceIsTheCookiesSession(t *testing.T) {
	t.Parallel()

	sessionID := impulse.Must(impulse.NewUUID())
	bobsSessionID := impulse.Must(impulse.NewUUID())
	alicesSessionID := impulse.Must(impulse.NewUUID())
	aliceID := impulse.Must(impulse.NewUUID())

	t.Run("a request validated as another session than its cookie names starts nothing", func(t *testing.T) {
		t.Parallel()
		// No read and no write is expected of the library's store.
		storage := &wrappedStore{BaseStore: mockstore.NewBaseStore(gomock.NewController(t))}
		s := New(storage)
		s.SessionTimeout = 10 * time.Minute

		// Bob's cookie names his session, and the request runs as alice's.
		ctx := context.WithValue(context.Background(), sessioninfo.CTXSessionID, bobsSessionID)
		ctx = context.WithValue(ctx, sessioninfo.CtxSessionInfo, accountSession(alicesSessionID, "alice", aliceID, time.Now()))
		for name, source := range map[string]impulse.NullUUID{
			"naming no source session":        {},
			"naming alice's session":          {UUID: alicesSessionID, Valid: true},
			"naming the cookie's own session": {UUID: bobsSessionID, Valid: true},
		} {
			imp := &sessioninfo.Impersonation{Actor: "alice", SourceSessionID: source, Principal: accesstypes.RolePrincipal("Editor")}
			req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonImpersonation, Username: "alice"}
			if _, err := s.StartImpersonatedSession(ctx, httptest.NewRecorder(), req, imp); !httpio.HasForbidden(err) {
				t.Errorf("StartImpersonatedSession() %s error = %v, want it refused (403)", name, err)
			}
		}
	})

	t.Run("a start that names no source session takes the cookie's", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		inner := mockstore.NewBaseStore(ctrl)
		inner.EXPECT().Session(gomock.Any(), alicesSessionID).Return(accountSession(alicesSessionID, "alice", aliceID, time.Now()), nil)
		inner.Sealed.EXPECT().CreateImpersonatedSession(gomock.Any(), gomock.Any(), gomock.Any()).Return(sessionID, nil)
		cookieHandler := mock_cookie.NewMockHandler(ctrl)
		cookieHandler.EXPECT().NewAuthCookie(gomock.Any(), true, sessionID).Return(cookie.NewValues())
		cookieHandler.EXPECT().CreateXSRFTokenCookie(gomock.Any(), sessionID)
		s := New(&wrappedStore{BaseStore: inner})
		s.SessionTimeout = 10 * time.Minute
		s.CookieHandler = cookieHandler

		ctx := context.WithValue(context.Background(), sessioninfo.CTXSessionID, alicesSessionID)
		ctx = context.WithValue(ctx, sessioninfo.CtxSessionInfo, accountSession(alicesSessionID, "alice", aliceID, time.Now()))
		imp := &sessioninfo.Impersonation{Actor: "alice", Principal: accesstypes.RolePrincipal("Editor")}
		req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonImpersonation, Username: "alice"}
		if _, err := s.StartImpersonatedSession(ctx, httptest.NewRecorder(), req, imp); err != nil {
			t.Fatalf("StartImpersonatedSession() error = %v, want none", err)
		}
		if want := (impulse.NullUUID{UUID: alicesSessionID, Valid: true}); imp.SourceSessionID != want {
			t.Errorf("the record's SourceSessionID = %v, want %v, the session the request's cookie names", imp.SourceSessionID, want)
		}
	})
}

// TestBaseSession_StartImpersonatedSession_aLocalActorOverAStoreWithNoLibraryStore holds
// the start of a session by a person of this application, over a store that has none of
// the library's reads, to the sealed refusal: the actor's own session cannot be read
// there, and the mock's Session is never asked for it.
func TestBaseSession_StartImpersonatedSession_aLocalActorOverAStoreWithNoLibraryStore(t *testing.T) {
	t.Parallel()

	sourceID := impulse.Must(impulse.NewUUID())
	s := New(mock_sessionstorage.NewMockBaseStore(gomock.NewController(t)))
	s.SessionTimeout = 10 * time.Minute
	imp := &sessioninfo.Impersonation{
		Actor:           "alice",
		SourceSessionID: impulse.NullUUID{UUID: sourceID, Valid: true},
		Principal:       accesstypes.RolePrincipal("Editor"),
	}
	req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonImpersonation, Username: "alice"}
	if _, err := s.StartImpersonatedSession(context.Background(), httptest.NewRecorder(), req, imp); !errors.Is(err, sessionstorage.ErrImpersonationSealed) {
		t.Fatalf("StartImpersonatedSession() error = %v, want %v", err, sessionstorage.ErrImpersonationSealed)
	}
}

// TestBaseSession_EndImpersonationAPI_theActorReturnsToTheStoredSession holds the
// session an actor is returned to, when the session that acts as someone else ends, to
// the library's own store: the Session of a store that wraps the library's is never
// asked for it.
func TestBaseSession_EndImpersonationAPI_theActorReturnsToTheStoredSession(t *testing.T) {
	t.Parallel()

	sessionID := impulse.Must(impulse.NewUUID())
	sourceID := impulse.Must(impulse.NewUUID())

	ctrl := gomock.NewController(t)
	inner := mockstore.NewBaseStore(ctrl)
	inner.EXPECT().DestroyImpersonatedSession(gomock.Any(), sessionID, sessioninfo.ImpersonationEndedByRelease).Return(nil)
	// The library's own store holds the actor's session as destroyed.
	destroyed := ordinarySession(sourceID, "alice", time.Now())
	destroyed.Expired = true
	inner.EXPECT().Session(gomock.Any(), sourceID).Return(destroyed, nil)
	// The wrapper would answer it as live.
	storage := &wrappedStore{BaseStore: inner, answer: func(impulse.UUID) (*sessioninfo.SessionData, error) {
		return ordinarySession(sourceID, "alice", time.Now()), nil
	}}

	s := New(storage)
	s.SessionTimeout = 10 * time.Minute
	sess := impersonatedSession(sessionID)
	sess.Impersonation.SourceSessionID = impulse.NullUUID{UUID: sourceID, Valid: true}
	ctx := context.WithValue(context.Background(), sessioninfo.CtxSessionInfo, sess)
	restored, err := s.EndImpersonationAPI(ctx, httptest.NewRecorder())
	if err != nil {
		t.Fatalf("EndImpersonationAPI() error = %v, want none", err)
	}
	if restored {
		t.Errorf("EndImpersonationAPI() returned the actor to a session the library's own store holds as destroyed")
	}
	if len(storage.asked) != 0 {
		t.Errorf("the wrapper's Session was asked for %v, want it never asked", storage.asked)
	}
}
