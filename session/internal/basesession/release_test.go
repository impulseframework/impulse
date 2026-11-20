package basesession

import (
	"context"
	"encoding/json"
	"net/http"
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

// ordinarySession is a non-impersonated session row for username, last active at updatedAt.
func ordinarySession(id impulse.UUID, username string, updatedAt time.Time) *sessioninfo.SessionData {
	return &sessioninfo.SessionData{SessionInfo: &sessioninfo.SessionInfo{ID: id, Username: username, UpdatedAt: updatedAt}}
}

// accountSession is ordinarySession for a session type whose sessions belong to an
// account: the row carries the account's id beside the username.
func accountSession(id impulse.UUID, username string, account impulse.UUID, updatedAt time.Time) *sessioninfo.SessionData {
	sess := ordinarySession(id, username, updatedAt)
	sess.UserID = impulse.NullUUIDFromUUID(account)

	return sess
}

func TestBaseSession_StartImpersonatedSession_LocalActor(t *testing.T) {
	t.Parallel()

	sessionID := impulse.Must(impulse.NewUUID())
	sourceID := impulse.Must(impulse.NewUUID())
	source := impulse.NullUUID{UUID: sourceID, Valid: true}

	otherID := impulse.Must(impulse.NewUUID())
	// aliceID is alice's account, for the session type whose sessions belong to one.
	aliceID := impulse.Must(impulse.NewUUID())
	// onSession is a context carrying the validated, non-impersonated session the
	// establishing call arrives on.
	onSession := func(sess *sessioninfo.SessionData) context.Context {
		ctx := context.WithValue(context.Background(), sessioninfo.CTXSessionID, sess.ID)

		return context.WithValue(ctx, sessioninfo.CtxSessionInfo, sess)
	}
	// createdWithSource asserts the record written names sourceID as the source session.
	createdWithSource := func(storage *mockstore.BaseStore, cookieHandler *mock_cookie.MockHandler) {
		storage.Sealed.EXPECT().CreateImpersonatedSession(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ *sessioninfo.NewSessionRequest, imp *sessioninfo.Impersonation) (impulse.UUID, error) {
				if imp.SourceSessionID != source {
					return impulse.NilUUID, errors.Newf("SourceSessionID = %v, want %v", imp.SourceSessionID, source)
				}

				return sessionID, nil
			})
		cookieHandler.EXPECT().NewAuthCookie(gomock.Any(), true, sessionID).Return(cookie.NewValues())
		cookieHandler.EXPECT().CreateXSRFTokenCookie(gomock.Any(), sessionID)
	}

	tests := []struct {
		name    string
		ctx     context.Context
		imp     *sessioninfo.Impersonation
		prepare func(storage *mockstore.BaseStore, cookieHandler *mock_cookie.MockHandler)
		// wantActorID is the actor's account on the record of an established session.
		wantActorID    impulse.NullUUID
		wantBadRequest bool
		wantForbidden  bool
	}{
		{
			name: "on a validated session, the actor and source are bound to it and verified as usual",
			ctx:  onSession(ordinarySession(sourceID, "alice", time.Now())),
			imp:  &sessioninfo.Impersonation{Actor: "alice", SourceSessionID: source, Principal: accesstypes.RolePrincipal("Editor")},
			prepare: func(storage *mockstore.BaseStore, cookieHandler *mock_cookie.MockHandler) {
				storage.EXPECT().Session(gomock.Any(), sourceID).Return(ordinarySession(sourceID, "alice", time.Now()), nil)
				createdWithSource(storage, cookieHandler)
			},
		},
		{
			name: "on a validated session, an omitted source defaults to that session",
			ctx:  onSession(ordinarySession(sourceID, "alice", time.Now())),
			imp:  &sessioninfo.Impersonation{Actor: "alice", Principal: accesstypes.RolePrincipal("Editor")},
			prepare: func(storage *mockstore.BaseStore, cookieHandler *mock_cookie.MockHandler) {
				storage.EXPECT().Session(gomock.Any(), sourceID).Return(ordinarySession(sourceID, "alice", time.Now()), nil)
				createdWithSource(storage, cookieHandler)
			},
		},
		{
			name:          "on a validated session, naming another user as the actor is refused before any lookup",
			ctx:           onSession(ordinarySession(sourceID, "carol", time.Now())),
			imp:           &sessioninfo.Impersonation{Actor: "alice", SourceSessionID: source, Principal: accesstypes.RolePrincipal("Editor")},
			prepare:       func(*mockstore.BaseStore, *mock_cookie.MockHandler) {},
			wantForbidden: true,
		},
		{
			name:          "on a validated session, naming another session as the source is refused before any lookup",
			ctx:           onSession(ordinarySession(otherID, "alice", time.Now())),
			imp:           &sessioninfo.Impersonation{Actor: "alice", SourceSessionID: source, Principal: accesstypes.RolePrincipal("Editor")},
			prepare:       func(*mockstore.BaseStore, *mock_cookie.MockHandler) {},
			wantForbidden: true,
		},
		{
			name:          "on a validated session, a foreign realm is refused: an actor logged in here is local",
			ctx:           onSession(ordinarySession(sourceID, "alice", time.Now())),
			imp:           &sessioninfo.Impersonation{Actor: "alice", ActorRealm: "admin-portal", Principal: accesstypes.RolePrincipal("Editor")},
			prepare:       func(*mockstore.BaseStore, *mock_cookie.MockHandler) {},
			wantForbidden: true,
		},
		{
			name: "a foreign actor is not checked here",
			imp:  &sessioninfo.Impersonation{Actor: "alice", ActorRealm: "admin-portal", Principal: accesstypes.RolePrincipal("Editor")},
			prepare: func(storage *mockstore.BaseStore, cookieHandler *mock_cookie.MockHandler) {
				storage.Sealed.EXPECT().CreateImpersonatedSession(gomock.Any(), gomock.Any(), gomock.Any()).Return(sessionID, nil)
				cookieHandler.EXPECT().NewAuthCookie(gomock.Any(), true, sessionID).Return(cookie.NewValues())
				cookieHandler.EXPECT().CreateXSRFTokenCookie(gomock.Any(), sessionID)
			},
		},
		{
			name: "a local actor with a live source session of their own is verified",
			imp:  &sessioninfo.Impersonation{Actor: "alice", SourceSessionID: source, Principal: accesstypes.RolePrincipal("Editor")},
			prepare: func(storage *mockstore.BaseStore, cookieHandler *mock_cookie.MockHandler) {
				storage.EXPECT().Session(gomock.Any(), sourceID).Return(ordinarySession(sourceID, "alice", time.Now()), nil)
				storage.Sealed.EXPECT().CreateImpersonatedSession(gomock.Any(), gomock.Any(), gomock.Any()).Return(sessionID, nil)
				cookieHandler.EXPECT().NewAuthCookie(gomock.Any(), true, sessionID).Return(cookie.NewValues())
				cookieHandler.EXPECT().CreateXSRFTokenCookie(gomock.Any(), sessionID)
			},
		},
		{
			name: "a local actor's record takes the actor's account from their source session, before it is written",
			imp:  &sessioninfo.Impersonation{Actor: "alice", SourceSessionID: source, Principal: accesstypes.RolePrincipal("Editor")},
			prepare: func(storage *mockstore.BaseStore, cookieHandler *mock_cookie.MockHandler) {
				storage.EXPECT().Session(gomock.Any(), sourceID).Return(accountSession(sourceID, "alice", aliceID, time.Now()), nil)
				storage.Sealed.EXPECT().CreateImpersonatedSession(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ context.Context, _ *sessioninfo.NewSessionRequest, imp *sessioninfo.Impersonation) (impulse.UUID, error) {
						if want := impulse.NullUUIDFromUUID(aliceID); imp.ActorID != want || imp.ActorUser() != accesstypes.User(aliceID.String()) {
							return impulse.NilUUID, errors.Newf("record written with ActorID = %v (ActorUser %q), want %v", imp.ActorID, imp.ActorUser(), want)
						}

						return sessionID, nil
					})
				cookieHandler.EXPECT().NewAuthCookie(gomock.Any(), true, sessionID).Return(cookie.NewValues())
				cookieHandler.EXPECT().CreateXSRFTokenCookie(gomock.Any(), sessionID)
			},
			wantActorID: impulse.NullUUIDFromUUID(aliceID),
		},
		{
			name:           "a local actor without a source session is a bad request",
			imp:            &sessioninfo.Impersonation{Actor: "alice", Principal: accesstypes.RolePrincipal("Editor")},
			prepare:        func(*mockstore.BaseStore, *mock_cookie.MockHandler) {},
			wantBadRequest: true,
		},
		{
			name: "a source session that is not here is refused",
			imp:  &sessioninfo.Impersonation{Actor: "alice", SourceSessionID: source, Principal: accesstypes.RolePrincipal("Editor")},
			prepare: func(storage *mockstore.BaseStore, _ *mock_cookie.MockHandler) {
				storage.EXPECT().Session(gomock.Any(), sourceID).Return(nil, httpio.NewNotFoundMessage("no such session"))
			},
			wantForbidden: true,
		},
		{
			name: "a source session of another user is refused",
			imp:  &sessioninfo.Impersonation{Actor: "alice", SourceSessionID: source, Principal: accesstypes.RolePrincipal("Editor")},
			prepare: func(storage *mockstore.BaseStore, _ *mock_cookie.MockHandler) {
				storage.EXPECT().Session(gomock.Any(), sourceID).Return(ordinarySession(sourceID, "carol", time.Now()), nil)
			},
			wantForbidden: true,
		},
		{
			name: "a destroyed source session is refused",
			imp:  &sessioninfo.Impersonation{Actor: "alice", SourceSessionID: source, Principal: accesstypes.RolePrincipal("Editor")},
			prepare: func(storage *mockstore.BaseStore, _ *mock_cookie.MockHandler) {
				src := ordinarySession(sourceID, "alice", time.Now())
				src.Expired = true
				storage.EXPECT().Session(gomock.Any(), sourceID).Return(src, nil)
			},
			wantForbidden: true,
		},
		{
			name: "an idle-expired source session is refused",
			imp:  &sessioninfo.Impersonation{Actor: "alice", SourceSessionID: source, Principal: accesstypes.RolePrincipal("Editor")},
			prepare: func(storage *mockstore.BaseStore, _ *mock_cookie.MockHandler) {
				storage.EXPECT().Session(gomock.Any(), sourceID).Return(ordinarySession(sourceID, "alice", time.Now().Add(-time.Hour)), nil)
			},
			wantForbidden: true,
		},
		{
			name: "an impersonated source session is refused",
			imp:  &sessioninfo.Impersonation{Actor: "alice", SourceSessionID: source, Principal: accesstypes.RolePrincipal("Editor")},
			prepare: func(storage *mockstore.BaseStore, _ *mock_cookie.MockHandler) {
				src := ordinarySession(sourceID, "alice", time.Now())
				src.Impersonation = &sessioninfo.Impersonation{SessionID: sourceID, Actor: "root", Principal: accesstypes.UserPrincipal("alice")}
				storage.EXPECT().Session(gomock.Any(), sourceID).Return(src, nil)
			},
			wantForbidden: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			storage := mockstore.NewBaseStore(ctrl)
			cookieHandler := mock_cookie.NewMockHandler(ctrl)
			tt.prepare(storage, cookieHandler)

			ctx := tt.ctx
			if ctx == nil {
				ctx = context.Background()
			}
			s := New(storage)
			s.SessionTimeout = 10 * time.Minute
			s.CookieHandler = cookieHandler
			req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonImpersonation, Username: "alice"}
			_, err := s.StartImpersonatedSession(ctx, httptest.NewRecorder(), req, tt.imp)
			wantErr := tt.wantBadRequest || tt.wantForbidden
			if (err != nil) != wantErr {
				t.Fatalf("StartImpersonatedSession() error = %v, wantErr %v", err, wantErr)
			}
			if httpio.HasBadRequest(err) != tt.wantBadRequest || httpio.HasForbidden(err) != tt.wantForbidden {
				t.Errorf("error = %v; want badRequest=%v forbidden=%v", err, tt.wantBadRequest, tt.wantForbidden)
			}
			// The actor's account is the source session's: null for a session type whose
			// sessions belong to no account, and for a foreign actor, who has none here.
			if err == nil && tt.imp.ActorID != tt.wantActorID {
				t.Errorf("record ActorID = %v, want %v", tt.imp.ActorID, tt.wantActorID)
			}
		})
	}
}

// TestBaseSession_StartImpersonatedSession_onlyTheBuiltSessionWrites holds the write of
// an impersonated session to the base session the library built, over a store the
// library made. Reflection can find this type through a session type's field and make a
// value of it over a store of the program's choosing: that session is refused before
// the store is reached (the mock fails the test on any call it was not told to expect).
// And a store that has no write of the library's, which is what a mock of the store and
// a store of an application's own hold, refuses the session the library built.
func TestBaseSession_StartImpersonatedSession_onlyTheBuiltSessionWrites(t *testing.T) {
	t.Parallel()

	sessionID := impulse.Must(impulse.NewUUID())
	tests := []struct {
		name string
		// session builds the session over a store, with what the store is to expect.
		session    func(ctrl *gomock.Controller) *BaseSession
		wantSealed bool
	}{
		{
			name: "the session New built reaches the write of the store",
			session: func(ctrl *gomock.Controller) *BaseSession {
				storage := mockstore.NewBaseStore(ctrl)
				storage.Sealed.EXPECT().CreateImpersonatedSession(gomock.Any(), gomock.Any(), gomock.Any()).Return(sessionID, nil)

				return New(storage)
			},
		},
		{
			name: "a session nothing built is refused before the store is reached",
			session: func(ctrl *gomock.Controller) *BaseSession {
				return &BaseSession{Storage: mockstore.NewBaseStore(ctrl)}
			},
			wantSealed: true,
		},
		{
			name: "a store with no write of the library's refuses the session New built",
			session: func(ctrl *gomock.Controller) *BaseSession {
				return New(mock_sessionstorage.NewMockBaseStore(ctrl))
			},
			wantSealed: true,
		},
		{
			name: "a store of an application's own, which embeds the write that refuses, refuses the session New built",
			session: func(ctrl *gomock.Controller) *BaseSession {
				return New(struct {
					*mock_sessionstorage.MockBaseStoreMethods
					sessionstorage.NoImpersonatedSessions
				}{MockBaseStoreMethods: mock_sessionstorage.NewMockBaseStoreMethods(ctrl)})
			},
			wantSealed: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			cookieHandler := mock_cookie.NewMockHandler(ctrl)
			if !tt.wantSealed {
				cookieHandler.EXPECT().NewAuthCookie(gomock.Any(), true, sessionID).Return(cookie.NewValues())
				cookieHandler.EXPECT().CreateXSRFTokenCookie(gomock.Any(), sessionID)
			}

			s := tt.session(ctrl)
			s.CookieHandler = cookieHandler
			imp := &sessioninfo.Impersonation{Actor: "alice", ActorRealm: "admin-portal", Principal: accesstypes.RolePrincipal("Editor")}
			req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonImpersonation, Username: "alice"}
			_, err := s.StartImpersonatedSession(context.Background(), httptest.NewRecorder(), req, imp)
			if !tt.wantSealed && err != nil {
				t.Fatalf("StartImpersonatedSession() error = %v, want none", err)
			}
			if tt.wantSealed && !errors.Is(err, sessionstorage.ErrImpersonationSealed) {
				t.Fatalf("StartImpersonatedSession() error = %v, want %v", err, sessionstorage.ErrImpersonationSealed)
			}
		})
	}
}

func TestBaseSession_ValidateSessionAPI_RefreshesLocalActorSource(t *testing.T) {
	t.Parallel()

	sessionID := impulse.Must(impulse.NewUUID())
	sourceID := impulse.Must(impulse.NewUUID())

	newSession := func(imp *sessioninfo.Impersonation) *sessioninfo.SessionData {
		imp.SessionID = sessionID
		imp.ExpiresAt = time.Now().Add(time.Hour)

		return &sessioninfo.SessionData{
			// Last activity is past the five-second rate limit, so activity is written.
			SessionInfo:   &sessioninfo.SessionInfo{ID: sessionID, Username: "alice", UpdatedAt: time.Now().Add(-10 * time.Second)},
			Impersonation: imp,
		}
	}

	tests := []struct {
		name    string
		session *sessioninfo.SessionData
		prepare func(storage *mockstore.BaseStore)
	}{
		{
			name:    "a local actor's source session is refreshed with the impersonated session",
			session: newSession(&sessioninfo.Impersonation{Actor: "alice", SourceSessionID: impulse.NullUUID{UUID: sourceID, Valid: true}, Principal: accesstypes.RolePrincipal("Editor")}),
			prepare: func(storage *mockstore.BaseStore) {
				storage.EXPECT().UpdateSessionActivity(gomock.Any(), sessionID).Return(nil)
				storage.EXPECT().UpdateSessionActivity(gomock.Any(), sourceID).Return(nil)
			},
		},
		{
			name:    "a foreign actor's source session lives elsewhere and is not touched",
			session: newSession(&sessioninfo.Impersonation{Actor: "alice", ActorRealm: "admin-portal", SourceSessionID: impulse.NullUUID{UUID: sourceID, Valid: true}, Principal: accesstypes.RolePrincipal("Editor")}),
			prepare: func(storage *mockstore.BaseStore) {
				storage.EXPECT().UpdateSessionActivity(gomock.Any(), sessionID).Return(nil)
			},
		},
		{
			name:    "a failed source refresh is logged, not returned",
			session: newSession(&sessioninfo.Impersonation{Actor: "alice", SourceSessionID: impulse.NullUUID{UUID: sourceID, Valid: true}, Principal: accesstypes.UserPrincipal("bob")}),
			prepare: func(storage *mockstore.BaseStore) {
				storage.EXPECT().UpdateSessionActivity(gomock.Any(), sessionID).Return(nil)
				storage.EXPECT().UpdateSessionActivity(gomock.Any(), sourceID).Return(httpio.NewNotFoundMessage("gone"))
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			storage := mockstore.NewBaseStore(ctrl)
			storage.EXPECT().Session(gomock.Any(), sessionID).Return(tt.session, nil)
			tt.prepare(storage)

			s := &BaseSession{SessionTimeout: 10 * time.Minute, Storage: storage}
			ctx := context.WithValue(context.Background(), sessioninfo.CTXSessionID, sessionID)
			if _, err := s.ValidateSessionAPI(ctx); err != nil {
				t.Fatalf("ValidateSessionAPI() error = %v", err)
			}
		})
	}
}

func TestBaseSession_EndImpersonationAPI(t *testing.T) {
	t.Parallel()

	sessionID := impulse.Must(impulse.NewUUID())
	sourceID := impulse.Must(impulse.NewUUID())
	source := impulse.NullUUID{UUID: sourceID, Valid: true}
	// The accounts of alice and of somebody else, for the session type whose sessions
	// belong to one.
	aliceID := impulse.Must(impulse.NewUUID())
	otherID := impulse.Must(impulse.NewUUID())
	alice := impulse.NullUUIDFromUUID(aliceID)

	impersonated := func(imp *sessioninfo.Impersonation) *sessioninfo.SessionData {
		imp.SessionID = sessionID

		return &sessioninfo.SessionData{SessionInfo: &sessioninfo.SessionInfo{ID: sessionID, Username: "alice"}, Impersonation: imp}
	}
	ended := func(storage *mockstore.BaseStore) {
		storage.EXPECT().DestroyImpersonatedSession(gomock.Any(), sessionID, sessioninfo.ImpersonationEndedByRelease).Return(nil)
	}

	tests := []struct {
		name           string
		session        *sessioninfo.SessionData
		prepare        func(storage *mockstore.BaseStore, cookieHandler *mock_cookie.MockHandler)
		wantRestored   bool
		wantBadRequest bool
		wantErr        bool
		wantEvents     []sessioninfo.ImpersonationEventKind
	}{
		{
			name:           "an ordinary session is not impersonated",
			session:        ordinarySession(sessionID, "alice", time.Now()),
			prepare:        func(*mockstore.BaseStore, *mock_cookie.MockHandler) {},
			wantBadRequest: true,
			wantErr:        true,
		},
		{
			name:    "a local actor returns to their live source session",
			session: impersonated(&sessioninfo.Impersonation{Actor: "alice", SourceSessionID: source, Principal: accesstypes.RolePrincipal("Editor")}),
			prepare: func(storage *mockstore.BaseStore, cookieHandler *mock_cookie.MockHandler) {
				ended(storage)
				storage.EXPECT().Session(gomock.Any(), sourceID).Return(ordinarySession(sourceID, "alice", time.Now()), nil)
				cookieHandler.EXPECT().NewAuthCookie(gomock.Any(), true, sourceID).Return(cookie.NewValues())
				cookieHandler.EXPECT().CreateXSRFTokenCookie(gomock.Any(), sourceID)
			},
			wantRestored: true,
			wantEvents:   []sessioninfo.ImpersonationEventKind{sessioninfo.ImpersonationEnded},
		},
		{
			name:    "a source session that has since expired cannot be returned to",
			session: impersonated(&sessioninfo.Impersonation{Actor: "alice", SourceSessionID: source, Principal: accesstypes.UserPrincipal("bob")}),
			prepare: func(storage *mockstore.BaseStore, _ *mock_cookie.MockHandler) {
				ended(storage)
				src := ordinarySession(sourceID, "alice", time.Now())
				src.Expired = true
				storage.EXPECT().Session(gomock.Any(), sourceID).Return(src, nil)
			},
			wantEvents: []sessioninfo.ImpersonationEventKind{sessioninfo.ImpersonationEnded},
		},
		{
			name:    "a source session renamed away from the actor cannot be returned to",
			session: impersonated(&sessioninfo.Impersonation{Actor: "alice", SourceSessionID: source, Principal: accesstypes.RolePrincipal("Editor")}),
			prepare: func(storage *mockstore.BaseStore, _ *mock_cookie.MockHandler) {
				ended(storage)
				storage.EXPECT().Session(gomock.Any(), sourceID).Return(ordinarySession(sourceID, "alicia", time.Now()), nil)
			},
			wantEvents: []sessioninfo.ImpersonationEventKind{sessioninfo.ImpersonationEnded},
		},
		{
			name:    "an actor named by account returns to the source session of that account, under whatever username it now carries",
			session: impersonated(&sessioninfo.Impersonation{Actor: "alice", ActorID: alice, SourceSessionID: source, Principal: accesstypes.RolePrincipal("Editor")}),
			prepare: func(storage *mockstore.BaseStore, cookieHandler *mock_cookie.MockHandler) {
				ended(storage)
				storage.EXPECT().Session(gomock.Any(), sourceID).Return(accountSession(sourceID, "alicia", aliceID, time.Now()), nil)
				cookieHandler.EXPECT().NewAuthCookie(gomock.Any(), true, sourceID).Return(cookie.NewValues())
				cookieHandler.EXPECT().CreateXSRFTokenCookie(gomock.Any(), sourceID)
			},
			wantRestored: true,
			wantEvents:   []sessioninfo.ImpersonationEventKind{sessioninfo.ImpersonationEnded},
		},
		{
			name:    "a source session of another account cannot be returned to, though it carries the actor's username",
			session: impersonated(&sessioninfo.Impersonation{Actor: "alice", ActorID: alice, SourceSessionID: source, Principal: accesstypes.RolePrincipal("Editor")}),
			prepare: func(storage *mockstore.BaseStore, _ *mock_cookie.MockHandler) {
				ended(storage)
				storage.EXPECT().Session(gomock.Any(), sourceID).Return(accountSession(sourceID, "alice", otherID, time.Now()), nil)
			},
			wantEvents: []sessioninfo.ImpersonationEventKind{sessioninfo.ImpersonationEnded},
		},
		{
			name:    "an actor named by account cannot be returned to a source session that belongs to none",
			session: impersonated(&sessioninfo.Impersonation{Actor: "alice", ActorID: alice, SourceSessionID: source, Principal: accesstypes.RolePrincipal("Editor")}),
			prepare: func(storage *mockstore.BaseStore, _ *mock_cookie.MockHandler) {
				ended(storage)
				storage.EXPECT().Session(gomock.Any(), sourceID).Return(ordinarySession(sourceID, "alice", time.Now()), nil)
			},
			wantEvents: []sessioninfo.ImpersonationEventKind{sessioninfo.ImpersonationEnded},
		},
		{
			name:    "a record that names no account cannot be returned to a source session that belongs to one",
			session: impersonated(&sessioninfo.Impersonation{Actor: "alice", SourceSessionID: source, Principal: accesstypes.RolePrincipal("Editor")}),
			prepare: func(storage *mockstore.BaseStore, _ *mock_cookie.MockHandler) {
				ended(storage)
				storage.EXPECT().Session(gomock.Any(), sourceID).Return(accountSession(sourceID, "alice", aliceID, time.Now()), nil)
			},
			wantEvents: []sessioninfo.ImpersonationEventKind{sessioninfo.ImpersonationEnded},
		},
		{
			name:    "a source session that is gone cannot be returned to",
			session: impersonated(&sessioninfo.Impersonation{Actor: "alice", SourceSessionID: source, Principal: accesstypes.RolePrincipal("Editor")}),
			prepare: func(storage *mockstore.BaseStore, _ *mock_cookie.MockHandler) {
				ended(storage)
				storage.EXPECT().Session(gomock.Any(), sourceID).Return(nil, httpio.NewNotFoundMessage("gone"))
			},
			wantEvents: []sessioninfo.ImpersonationEventKind{sessioninfo.ImpersonationEnded},
		},
		{
			name:    "a foreign actor's session ends without a lookup: their own session is in another application",
			session: impersonated(&sessioninfo.Impersonation{Actor: "alice", ActorRealm: "admin-portal", SourceSessionID: source, Principal: accesstypes.RolePrincipal("Editor")}),
			prepare: func(storage *mockstore.BaseStore, _ *mock_cookie.MockHandler) {
				ended(storage)
			},
			wantEvents: []sessioninfo.ImpersonationEventKind{sessioninfo.ImpersonationEnded},
		},
		{
			name:    "a failed end fails the call before any restore, and announces nothing",
			session: impersonated(&sessioninfo.Impersonation{Actor: "alice", SourceSessionID: source, Principal: accesstypes.RolePrincipal("Editor")}),
			prepare: func(storage *mockstore.BaseStore, _ *mock_cookie.MockHandler) {
				storage.EXPECT().DestroyImpersonatedSession(gomock.Any(), sessionID, sessioninfo.ImpersonationEndedByRelease).Return(errors.New("db down"))
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			storage := mockstore.NewBaseStore(ctrl)
			cookieHandler := mock_cookie.NewMockHandler(ctrl)
			tt.prepare(storage, cookieHandler)

			var events []sessioninfo.ImpersonationEventKind
			s := &BaseSession{
				SessionTimeout: 10 * time.Minute,
				Storage:        storage,
				CookieHandler:  cookieHandler,
				ImpersonationAudit: func(_ context.Context, event sessioninfo.ImpersonationEvent) error {
					events = append(events, event.Kind)
					if event.Impersonation.EndReason != sessioninfo.ImpersonationEndedByRelease || event.Impersonation.EndedAt == nil {
						return errors.Newf("unexpected end %+v", event.Impersonation)
					}

					return nil
				},
			}

			ctx := context.WithValue(context.Background(), sessioninfo.CTXSessionID, sessionID)
			ctx = context.WithValue(ctx, sessioninfo.CtxSessionInfo, tt.session)
			restored, err := s.EndImpersonationAPI(ctx, httptest.NewRecorder())
			if (err != nil) != tt.wantErr {
				t.Fatalf("EndImpersonationAPI() error = %v, wantErr %v", err, tt.wantErr)
			}
			if httpio.HasBadRequest(err) != tt.wantBadRequest {
				t.Errorf("error = %v; want badRequest=%v", err, tt.wantBadRequest)
			}
			if restored != tt.wantRestored {
				t.Errorf("restored = %v, want %v", restored, tt.wantRestored)
			}
			if diff := cmp.Diff(tt.wantEvents, events); diff != "" {
				t.Errorf("events mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestBaseSession_EndImpersonation(t *testing.T) {
	t.Parallel()

	sessionID := impulse.Must(impulse.NewUUID())
	sourceID := impulse.Must(impulse.NewUUID())

	ctrl := gomock.NewController(t)
	storage := mockstore.NewBaseStore(ctrl)
	storage.EXPECT().DestroyImpersonatedSession(gomock.Any(), sessionID, sessioninfo.ImpersonationEndedByRelease).Return(nil)
	storage.EXPECT().Session(gomock.Any(), sourceID).Return(ordinarySession(sourceID, "alice", time.Now()), nil)
	cookieHandler := mock_cookie.NewMockHandler(ctrl)
	cookieHandler.EXPECT().NewAuthCookie(gomock.Any(), true, sourceID).Return(cookie.NewValues())
	cookieHandler.EXPECT().CreateXSRFTokenCookie(gomock.Any(), sourceID)

	s := &BaseSession{
		SessionTimeout: 10 * time.Minute,
		Storage:        storage,
		CookieHandler:  cookieHandler,
		Handle: func(handler func(w http.ResponseWriter, r *http.Request) error) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) { _ = handler(w, r) }
		},
	}

	session := &sessioninfo.SessionData{
		SessionInfo:   &sessioninfo.SessionInfo{ID: sessionID, Username: "alice"},
		Impersonation: &sessioninfo.Impersonation{SessionID: sessionID, Actor: "alice", SourceSessionID: impulse.NullUUID{UUID: sourceID, Valid: true}, Principal: accesstypes.RolePrincipal("Editor")},
	}
	ctx := context.WithValue(context.Background(), sessioninfo.CTXSessionID, sessionID)
	ctx = context.WithValue(ctx, sessioninfo.CtxSessionInfo, session)
	rr := httptest.NewRecorder()
	s.EndImpersonation().ServeHTTP(rr, httptest.NewRequestWithContext(ctx, http.MethodPost, "/end", http.NoBody))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	var body EndImpersonationResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if !body.Restored {
		t.Errorf("Restored = false, want true")
	}
}
