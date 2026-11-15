package session

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/internal/sessioncore"
	"github.com/impulseframework/impulse/session/internal/workos"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/session/signin"
	gomock "go.uber.org/mock/gomock"
)

// TestAuth_ChangeName proves the handler a person writes their own name through: it
// validates the session itself, writes the name on the session's user, and answers the
// name as the store kept it.
func TestAuth_ChangeName(t *testing.T) {
	t.Parallel()

	userID := impulse.Must(impulse.NewUUID())
	sessionID := impulse.Must(impulse.NewUUID())
	user := func(name *string) *sessionstorage.SessionUser {
		return &sessionstorage.SessionUser{ID: userID, Username: "pat", Name: name}
	}
	request := func(ctx context.Context, body string) *http.Request {
		r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/user/name", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")

		return r
	}
	signedIn := func() context.Context {
		return context.WithValue(context.Background(), sessioninfo.CTXSessionID, sessionID)
	}
	session := func() *sessioninfo.SessionData {
		return &sessioninfo.SessionData{SessionInfo: &sessioninfo.SessionInfo{ID: sessionID, Username: "pat", UpdatedAt: time.Now()}, UserID: impulse.NullUUIDFromUUID(userID)}
	}
	nameIn := func(t *testing.T, rr *httptest.ResponseRecorder) (name string, present bool) {
		t.Helper()
		var body map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("json.Unmarshal(%s) error = %v", rr.Body.String(), err)
		}
		value, present := body["name"]
		name, _ = value.(string)

		return name, present
	}

	t.Run("the name is written on the session's user and answered as it is kept", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		kept := "Pat Example"
		f.store.EXPECT().Session(gomock.Any(), sessionID).Return(session(), nil)
		gomock.InOrder(
			f.store.EXPECT().User(gomock.Any(), userID).Return(user(nil), nil),
			f.store.EXPECT().SetUserName(gomock.Any(), userID, gomock.Any()).DoAndReturn(func(_ context.Context, _ impulse.UUID, name *string) error {
				if name == nil || *name != "  Pat Example " {
					t.Errorf("SetUserName() name = %v, want the name as it was sent: the store trims it", name)
				}

				return nil
			}),
			f.store.EXPECT().User(gomock.Any(), userID).Return(user(&kept), nil),
		)

		rr := httptest.NewRecorder()
		f.auth.ChangeName().ServeHTTP(rr, request(signedIn(), `{"name":"  Pat Example "}`))
		if rr.Code != http.StatusOK {
			t.Fatalf("ChangeName() = %d %s, want 200", rr.Code, rr.Body.String())
		}
		if name, _ := nameIn(t, rr); name != kept {
			t.Errorf("ChangeName() answered the name %q, want %q", name, kept)
		}
	})

	t.Run("an empty name takes the name off, and the answer carries none", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		f.store.EXPECT().Session(gomock.Any(), sessionID).Return(session(), nil)
		f.store.EXPECT().User(gomock.Any(), userID).Return(user(nil), nil).Times(2)
		f.store.EXPECT().SetUserName(gomock.Any(), userID, gomock.Any()).Return(nil)

		rr := httptest.NewRecorder()
		f.auth.ChangeName().ServeHTTP(rr, request(signedIn(), `{"name":""}`))
		if rr.Code != http.StatusOK {
			t.Fatalf("ChangeName() = %d %s, want 200", rr.Code, rr.Body.String())
		}
		if name, present := nameIn(t, rr); present {
			t.Errorf("ChangeName() answered the name %q, want none", name)
		}
	})

	t.Run("a name the store refuses is the store's refusal", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		f.store.EXPECT().Session(gomock.Any(), sessionID).Return(session(), nil)
		f.store.EXPECT().User(gomock.Any(), userID).Return(user(nil), nil)
		f.store.EXPECT().SetUserName(gomock.Any(), userID, gomock.Any()).Return(httpio.NewBadRequestMessage("a name is at most 200 characters"))

		rr := httptest.NewRecorder()
		f.auth.ChangeName().ServeHTTP(rr, request(signedIn(), `{"name":"`+strings.Repeat("a", 201)+`"}`))
		if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "at most 200 characters") {
			t.Errorf("ChangeName() = %d %s, want 400 with the store's message", rr.Code, rr.Body.String())
		}
	})

	t.Run("a request without a session writes nothing", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		f.store.EXPECT().Session(gomock.Any(), sessionID).Return(nil, httpio.NewNotFoundMessage("no session"))

		rr := httptest.NewRecorder()
		f.auth.ChangeName().ServeHTTP(rr, request(signedIn(), `{"name":"Pat"}`))
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("ChangeName() = %d %s, want 401", rr.Code, rr.Body.String())
		}
	})

	t.Run("a person under the password change mark is refused until the password is changed", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		marked := user(nil)
		marked.PasswordChangeRequired = true
		f.store.EXPECT().Session(gomock.Any(), sessionID).Return(session(), nil)
		f.store.EXPECT().User(gomock.Any(), userID).Return(marked, nil)

		rr := httptest.NewRecorder()
		f.auth.ChangeName().ServeHTTP(rr, request(signedIn(), `{"name":"Pat"}`))
		if rr.Code != http.StatusForbidden {
			t.Errorf("ChangeName() = %d %s, want 403", rr.Code, rr.Body.String())
		}
	})

	t.Run("an impersonated session is refused: the name is the person's own to write", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		ctx := impersonatedCtx(sessionID, "pat", &sessioninfo.Impersonation{SessionID: sessionID, Actor: "admin"})
		ctx = context.WithValue(ctx, sessioninfo.CtxUserInfo, &sessioninfo.UserInfo{ID: userID, Username: "pat"})

		rr := httptest.NewRecorder()
		f.auth.ChangeName().ServeHTTP(rr, request(ctx, `{"name":"Pat"}`))
		if rr.Code != http.StatusForbidden || !strings.Contains(rr.Body.String(), "not permitted in an impersonated session") {
			t.Errorf("ChangeName() = %d %s, want 403 naming the impersonated session", rr.Code, rr.Body.String())
		}
	})

	t.Run("a body with a field the request does not have is refused", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		f.store.EXPECT().Session(gomock.Any(), sessionID).Return(session(), nil)
		f.store.EXPECT().User(gomock.Any(), userID).Return(user(nil), nil)

		rr := httptest.NewRecorder()
		f.auth.ChangeName().ServeHTTP(rr, request(signedIn(), `{"name":"Pat","username":"other"}`))
		if rr.Code != http.StatusBadRequest {
			t.Errorf("ChangeName() = %d %s, want 400", rr.Code, rr.Body.String())
		}
	})
}

// TestAuthAPI_SetSessionUserName proves the operator's entry of a name goes to the
// store as given, and the store's refusal comes back as it is.
func TestAuthAPI_SetSessionUserName(t *testing.T) {
	t.Parallel()

	userID := impulse.Must(impulse.NewUUID())
	name := "  Pat Example "

	t.Run("a name is written on the user", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		f.store.EXPECT().SetUserName(gomock.Any(), userID, &name).Return(nil)

		if err := f.auth.API().SetSessionUserName(context.Background(), userID, &name); err != nil {
			t.Errorf("SetSessionUserName() error = %v", err)
		}
	})

	t.Run("no name takes the one held off", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		f.store.EXPECT().SetUserName(gomock.Any(), userID, (*string)(nil)).Return(nil)

		if err := f.auth.API().SetSessionUserName(context.Background(), userID, nil); err != nil {
			t.Errorf("SetSessionUserName() error = %v", err)
		}
	})

	t.Run("a user that does not exist is the store's NotFound", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		f.store.EXPECT().SetUserName(gomock.Any(), userID, &name).Return(httpio.NewNotFoundMessage("no such user"))

		if err := f.auth.API().SetSessionUserName(context.Background(), userID, &name); !httpio.HasNotFound(err) {
			t.Errorf("SetSessionUserName() error = %v, want a NotFound", err)
		}
	})
}

// TestAuth_AuthenticatedName proves the session read answers the person's name, and
// leaves it out for a person who holds none.
func TestAuth_AuthenticatedName(t *testing.T) {
	t.Parallel()

	userID := impulse.Must(impulse.NewUUID())
	sessionID := impulse.Must(impulse.NewUUID())
	name := "Pat Example"
	for _, tt := range []struct {
		label string
		name  *string
		want  any
	}{
		{label: "a person with a name", name: &name, want: "Pat Example"},
		{label: "a person without one", name: nil, want: nil},
	} {
		t.Run(tt.label, func(t *testing.T) {
			t.Parallel()
			f := newAuthFixture(t, gomock.NewController(t))
			f.store.EXPECT().Session(gomock.Any(), sessionID).Return(&sessioninfo.SessionData{
				SessionInfo: &sessioninfo.SessionInfo{ID: sessionID, Username: "pat", UpdatedAt: time.Now()}, UserID: impulse.NullUUIDFromUUID(userID),
			}, nil)
			f.store.EXPECT().User(gomock.Any(), userID).Return(&sessionstorage.SessionUser{ID: userID, Username: "pat", Name: tt.name}, nil)

			rr := httptest.NewRecorder()
			ctx := context.WithValue(context.Background(), sessioninfo.CTXSessionID, sessionID)
			f.auth.Authenticated().ServeHTTP(rr, httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/user/session", http.NoBody))
			var body map[string]any
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil || rr.Code != http.StatusOK {
				t.Fatalf("Authenticated() = %d %s (%v), want 200", rr.Code, rr.Body.String(), err)
			}
			if got := body["name"]; got != tt.want {
				t.Errorf("Authenticated() name = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestAuth_signIn_theNameADirectoryGave: the name a directory gave reaches the store as
// a user row would keep it, or not at all, in the sign-in's request and in the pending
// identity a held sign-in keeps, so a name a row would not keep never fails the sign-in
// it came with.
func TestAuth_signIn_theNameADirectoryGave(t *testing.T) {
	t.Parallel()

	userID := impulse.Must(impulse.NewUUID())
	tests := []struct {
		name     string
		asserted string
		want     string
	}{
		{name: "a name is kept without the space around it", asserted: "  Pat Example ", want: "Pat Example"},
		{name: "a name too long to keep is dropped", asserted: strings.Repeat("a", sessioninfo.MaxNameLength+1)},
		{name: "a name with a NUL in it is dropped", asserted: "Pat\x00Example"},
		{name: "a name over two lines is dropped", asserted: "Pat\nExample"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newAuthFixture(t, gomock.NewController(t))
			workos.SignIn(f.auth, "workos", resolveAll[workos.Identity](), &workos.Config{APIKey: "sk", ClientID: "client", RedirectURL: "https://app/sso/callback"})
			identity := &sessioninfo.Identity{Method: sessioninfo.MethodWorkOS, Connection: "conn_1", Subject: "user_1", Email: "pat@example.com", EmailVerified: true, Name: tt.asserted}
			f.expectSignIn(impulse.Must(impulse.NewUUID()), func(req *sessioninfo.NewSessionRequest) {
				if req.Identity.Name != tt.want {
					t.Errorf("the sign-in's request carries the name %q, want %q", req.Identity.Name, tt.want)
				}
			}, func(*sessioninfo.NewSessionRequest) error {
				return &signin.PendingSignInError{Reason: sessioninfo.PendingMFA, UserID: impulse.NullUUIDFromUUID(userID), Username: "pat"}
			})
			pendingID := f.expectHold("pat")

			if _, err := f.auth.signIn(context.Background(), httptest.NewRecorder(), &signInAttempt{identity: identity, reason: sessioninfo.ReasonLogin, registration: "workos", sameSite: sessioncore.SameSiteNone}); err != nil {
				t.Fatalf("signIn() error = %v", err)
			}
			if held := f.held[pendingID]; held == nil || held.Identity.Name != tt.want {
				t.Errorf("the pending identity = %+v, want one held with the name %q", held, tt.want)
			}
		})
	}
}
