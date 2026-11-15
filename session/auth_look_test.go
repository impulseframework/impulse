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
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	gomock "go.uber.org/mock/gomock"
)

// TestAuth_ChangeLook proves the handler a person's pages keep the look they chose
// through: it validates the session itself, writes the look on the session's user as it
// was sent, and answers the look as the store kept it.
func TestAuth_ChangeLook(t *testing.T) {
	t.Parallel()

	userID := impulse.Must(impulse.NewUUID())
	sessionID := impulse.Must(impulse.NewUUID())
	user := func(scheme, palette *string) *sessionstorage.SessionUser {
		return &sessionstorage.SessionUser{ID: userID, Username: "pat", LookScheme: scheme, LookPalette: palette}
	}
	request := func(ctx context.Context, body string) *http.Request {
		r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/user/look", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")

		return r
	}
	signedIn := func() context.Context {
		return context.WithValue(context.Background(), sessioninfo.CTXSessionID, sessionID)
	}
	session := func() *sessioninfo.SessionData {
		return &sessioninfo.SessionData{SessionInfo: &sessioninfo.SessionInfo{ID: sessionID, Username: "pat", UpdatedAt: time.Now()}, UserID: impulse.NullUUIDFromUUID(userID)}
	}
	answered := func(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
		t.Helper()
		var body map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("json.Unmarshal(%s) error = %v", rr.Body.String(), err)
		}

		return body
	}

	t.Run("the look is written on the session's user and answered as it is kept", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		dark, workspace := "dark", "workspace"
		f.store.EXPECT().Session(gomock.Any(), sessionID).Return(session(), nil)
		gomock.InOrder(
			f.store.EXPECT().User(gomock.Any(), userID).Return(user(nil, nil), nil),
			f.store.EXPECT().SetUserLook(gomock.Any(), userID, sessioninfo.Look{Scheme: "dark", Palette: " workspace "}).Return(nil),
			f.store.EXPECT().User(gomock.Any(), userID).Return(user(&dark, &workspace), nil),
		)

		rr := httptest.NewRecorder()
		f.auth.ChangeLook().ServeHTTP(rr, request(signedIn(), `{"scheme":"dark","palette":" workspace "}`))
		if rr.Code != http.StatusOK {
			t.Fatalf("ChangeLook() = %d %s, want 200", rr.Code, rr.Body.String())
		}
		if body := answered(t, rr); body["scheme"] != "dark" || body["palette"] != "workspace" || len(body) != 2 {
			t.Errorf("ChangeLook() answered %v, want the scheme and the palette as kept and nothing else", body)
		}
	})

	t.Run("a look of nothing takes the one held off, and the answer carries neither half", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		f.store.EXPECT().Session(gomock.Any(), sessionID).Return(session(), nil)
		f.store.EXPECT().User(gomock.Any(), userID).Return(user(nil, nil), nil).Times(2)
		f.store.EXPECT().SetUserLook(gomock.Any(), userID, sessioninfo.Look{}).Return(nil)

		rr := httptest.NewRecorder()
		f.auth.ChangeLook().ServeHTTP(rr, request(signedIn(), `{}`))
		if rr.Code != http.StatusOK {
			t.Fatalf("ChangeLook() = %d %s, want 200", rr.Code, rr.Body.String())
		}
		if body := answered(t, rr); len(body) != 0 {
			t.Errorf("ChangeLook() answered %v, want an empty look", body)
		}
	})

	t.Run("a look the store refuses is the store's refusal", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		f.store.EXPECT().Session(gomock.Any(), sessionID).Return(session(), nil)
		f.store.EXPECT().User(gomock.Any(), userID).Return(user(nil, nil), nil)
		f.store.EXPECT().SetUserLook(gomock.Any(), userID, gomock.Any()).Return(httpio.NewBadRequestMessage("a scheme is light or dark"))

		rr := httptest.NewRecorder()
		f.auth.ChangeLook().ServeHTTP(rr, request(signedIn(), `{"scheme":"sepia"}`))
		if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "a scheme is light or dark") {
			t.Errorf("ChangeLook() = %d %s, want 400 with the store's message", rr.Code, rr.Body.String())
		}
	})

	t.Run("a request without a session writes nothing", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		f.store.EXPECT().Session(gomock.Any(), sessionID).Return(nil, httpio.NewNotFoundMessage("no session"))

		rr := httptest.NewRecorder()
		f.auth.ChangeLook().ServeHTTP(rr, request(signedIn(), `{"scheme":"dark"}`))
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("ChangeLook() = %d %s, want 401", rr.Code, rr.Body.String())
		}
	})

	t.Run("a person under the password change mark is refused until the password is changed", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		marked := user(nil, nil)
		marked.PasswordChangeRequired = true
		f.store.EXPECT().Session(gomock.Any(), sessionID).Return(session(), nil)
		f.store.EXPECT().User(gomock.Any(), userID).Return(marked, nil)

		rr := httptest.NewRecorder()
		f.auth.ChangeLook().ServeHTTP(rr, request(signedIn(), `{"scheme":"dark"}`))
		if rr.Code != http.StatusForbidden {
			t.Errorf("ChangeLook() = %d %s, want 403", rr.Code, rr.Body.String())
		}
	})

	t.Run("an impersonated session is refused: the look is the person's own to choose", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		ctx := impersonatedCtx(sessionID, "pat", &sessioninfo.Impersonation{SessionID: sessionID, Actor: "admin"})
		ctx = context.WithValue(ctx, sessioninfo.CtxUserInfo, &sessioninfo.UserInfo{ID: userID, Username: "pat"})

		rr := httptest.NewRecorder()
		f.auth.ChangeLook().ServeHTTP(rr, request(ctx, `{"scheme":"dark"}`))
		if rr.Code != http.StatusForbidden || !strings.Contains(rr.Body.String(), "not permitted in an impersonated session") {
			t.Errorf("ChangeLook() = %d %s, want 403 naming the impersonated session", rr.Code, rr.Body.String())
		}
	})

	t.Run("a body with a field the request does not have is refused", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		f.store.EXPECT().Session(gomock.Any(), sessionID).Return(session(), nil)
		f.store.EXPECT().User(gomock.Any(), userID).Return(user(nil, nil), nil)

		rr := httptest.NewRecorder()
		f.auth.ChangeLook().ServeHTTP(rr, request(signedIn(), `{"scheme":"dark","name":"Pat"}`))
		if rr.Code != http.StatusBadRequest {
			t.Errorf("ChangeLook() = %d %s, want 400", rr.Code, rr.Body.String())
		}
	})
}

// TestAuth_AuthenticatedLook proves the session read answers the look the person chose,
// each half where the user holds it, and leaves the look out for a person who chose none.
func TestAuth_AuthenticatedLook(t *testing.T) {
	t.Parallel()

	userID := impulse.Must(impulse.NewUUID())
	sessionID := impulse.Must(impulse.NewUUID())
	dark, workspace := "dark", "workspace"
	for _, tt := range []struct {
		label           string
		scheme, palette *string
		want            map[string]any
	}{
		{label: "a person who chose both", scheme: &dark, palette: &workspace, want: map[string]any{"scheme": "dark", "palette": "workspace"}},
		{label: "a person who chose a scheme alone", scheme: &dark, want: map[string]any{"scheme": "dark"}},
		{label: "a person who chose a palette alone", palette: &workspace, want: map[string]any{"palette": "workspace"}},
		{label: "a person who chose nothing", want: nil},
	} {
		t.Run(tt.label, func(t *testing.T) {
			t.Parallel()
			f := newAuthFixture(t, gomock.NewController(t))
			f.store.EXPECT().Session(gomock.Any(), sessionID).Return(&sessioninfo.SessionData{
				SessionInfo: &sessioninfo.SessionInfo{ID: sessionID, Username: "pat", UpdatedAt: time.Now()}, UserID: impulse.NullUUIDFromUUID(userID),
			}, nil)
			f.store.EXPECT().User(gomock.Any(), userID).Return(&sessionstorage.SessionUser{ID: userID, Username: "pat", LookScheme: tt.scheme, LookPalette: tt.palette}, nil)

			rr := httptest.NewRecorder()
			ctx := context.WithValue(context.Background(), sessioninfo.CTXSessionID, sessionID)
			f.auth.Authenticated().ServeHTTP(rr, httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/user/session", http.NoBody))
			var body map[string]any
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil || rr.Code != http.StatusOK {
				t.Fatalf("Authenticated() = %d %s (%v), want 200", rr.Code, rr.Body.String(), err)
			}
			look, present := body["look"]
			if tt.want == nil {
				if present {
					t.Errorf("Authenticated() look = %v, want none", look)
				}

				return
			}
			got, _ := look.(map[string]any)
			if len(got) != len(tt.want) {
				t.Fatalf("Authenticated() look = %v, want %v", look, tt.want)
			}
			for key, want := range tt.want {
				if got[key] != want {
					t.Errorf("Authenticated() look.%s = %v, want %v", key, got[key], want)
				}
			}
		})
	}
}
