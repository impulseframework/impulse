package session

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	gomock "go.uber.org/mock/gomock"
)

// TestAuthAPI_RemoveSessionUserPassword pins the removal an administrator or the
// application makes: the store removes the password under its own rule, the name's
// sign-in attempts are reset, and no session of the user is ended; the store's refusal
// of a user's last means of sign-in reaches the caller as it is, and resets nothing.
func TestAuthAPI_RemoveSessionUserPassword(t *testing.T) {
	t.Parallel()

	userID := impulse.Must(impulse.NewUUID())
	pat := func() *sessionstorage.SessionUser {
		return &sessionstorage.SessionUser{ID: userID, Username: "Pat", PasswordHash: hashed(t, "kept"), PasswordChangeRequired: true}
	}

	t.Run("the password is removed, the name's attempts are reset and the user's sessions stand", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		gomock.InOrder(
			f.store.EXPECT().User(gomock.Any(), userID).Return(pat(), nil),
			f.store.EXPECT().RemoveUserPassword(gomock.Any(), userID).Return(nil),
		)

		// The mock refuses a call it was not told of: no session is destroyed.
		if err := f.auth.API().RemoveSessionUserPassword(context.Background(), userID); err != nil {
			t.Fatalf("RemoveSessionUserPassword() error = %v", err)
		}
		if _, _, clears := f.counts(); clears != 1 {
			t.Errorf("the name's sign-in attempts were cleared %d times, want once", clears)
		}
	})

	t.Run("a user with no identity link keeps the password, and the name's attempts stay", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		f.store.EXPECT().User(gomock.Any(), userID).Return(pat(), nil)
		f.store.EXPECT().RemoveUserPassword(gomock.Any(), userID).Return(httpio.NewConflictMessageWithError(sessionstorage.ErrLastSignInMethod, "last"))

		err := f.auth.API().RemoveSessionUserPassword(context.Background(), userID)
		if !errors.Is(err, sessionstorage.ErrLastSignInMethod) || !httpio.HasConflict(err) {
			t.Errorf("RemoveSessionUserPassword() error = %v, want ErrLastSignInMethod (Conflict)", err)
		}
		if _, _, clears := f.counts(); clears != 0 {
			t.Errorf("the name's sign-in attempts were cleared %d times, want none: nothing was removed", clears)
		}
	})

	t.Run("a user that does not exist is not found, and nothing is removed", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		f.store.EXPECT().User(gomock.Any(), userID).Return(nil, httpio.NewNotFoundMessage("no user"))

		if err := f.auth.API().RemoveSessionUserPassword(context.Background(), userID); !httpio.HasNotFound(err) {
			t.Errorf("RemoveSessionUserPassword() error = %v, want NotFound", err)
		}
	})
}

// TestAuth_RemoveUserPassword pins the handler a person removes their own password
// through: it validates the session itself, as the password change does, asks for the
// password the person holds, hands the store the hash that password matched, so that
// password and no other is removed, and answers the refusals of a last means of sign-in
// and of a password that changed meanwhile in words the person can act on.
func TestAuth_RemoveUserPassword(t *testing.T) {
	t.Parallel()

	userID := impulse.Must(impulse.NewUUID())
	sessionID := impulse.Must(impulse.NewUUID())
	kept := hashed(t, "kept")
	pat := func(marked bool) *sessionstorage.SessionUser {
		return &sessionstorage.SessionUser{ID: userID, Username: "pat", PasswordHash: kept, PasswordChangeRequired: marked}
	}
	request := func(ctx context.Context, body string) *http.Request {
		r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/user/password/remove", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")

		return r
	}
	signedIn := func() context.Context {
		return context.WithValue(context.Background(), sessioninfo.CTXSessionID, sessionID)
	}
	session := func() *sessioninfo.SessionData {
		return &sessioninfo.SessionData{SessionInfo: &sessioninfo.SessionInfo{ID: sessionID, Username: "pat", UpdatedAt: time.Now()}, UserID: impulse.NullUUIDFromUUID(userID)}
	}

	t.Run("the person's password is removed once they prove it, and their session is not ended", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		f.store.EXPECT().Session(gomock.Any(), sessionID).Return(session(), nil)
		// The session's validation and the proof each read the user. The store is handed
		// the hash the proof was made against; the mock refuses the removal that names
		// the user alone, and a session destroyed.
		f.store.EXPECT().User(gomock.Any(), userID).Return(pat(false), nil).Times(2)
		f.store.EXPECT().RemoveProvenUserPassword(gomock.Any(), userID, kept).Return(nil)

		rr := httptest.NewRecorder()
		f.password.RemoveUserPassword().ServeHTTP(rr, request(signedIn(), `{"password":"kept"}`))
		if rr.Code != http.StatusOK {
			t.Fatalf("RemoveUserPassword() = %d %s, want 200", rr.Code, rr.Body.String())
		}
		if _, _, clears := f.counts(); clears != 1 {
			t.Errorf("the name's sign-in attempts were cleared %d times, want once", clears)
		}
	})

	t.Run("a password that changed after the person entered theirs is not removed, and the person is told to try again", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		f.store.EXPECT().Session(gomock.Any(), sessionID).Return(session(), nil)
		f.store.EXPECT().User(gomock.Any(), userID).Return(pat(false), nil).Times(2)
		f.store.EXPECT().RemoveProvenUserPassword(gomock.Any(), userID, kept).
			Return(httpio.NewConflictMessageWithError(sessionstorage.ErrPasswordChanged, "the password has changed since it was checked: check it again and retry"))

		rr := httptest.NewRecorder()
		f.password.RemoveUserPassword().ServeHTTP(rr, request(signedIn(), `{"password":"kept"}`))
		want := "Your password has changed since you entered it, so it was not removed: try again with your current password"
		if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), want) {
			t.Errorf("RemoveUserPassword() of a password that changed meanwhile = %d %s, want 409 saying %q", rr.Code, rr.Body.String(), want)
		}
		if _, _, clears := f.counts(); clears != 0 {
			t.Errorf("the name's sign-in attempts were cleared %d times, want none: nothing was removed", clears)
		}
	})

	t.Run("a person under the password change mark removes the temporary password instead of changing it", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		f.store.EXPECT().Session(gomock.Any(), sessionID).Return(session(), nil)
		f.store.EXPECT().User(gomock.Any(), userID).Return(pat(true), nil).Times(2)
		f.store.EXPECT().RemoveProvenUserPassword(gomock.Any(), userID, kept).Return(nil)

		rr := httptest.NewRecorder()
		f.password.RemoveUserPassword().ServeHTTP(rr, request(signedIn(), `{"password":"kept"}`))
		if rr.Code != http.StatusOK {
			t.Fatalf("RemoveUserPassword() under the mark = %d %s, want 200", rr.Code, rr.Body.String())
		}
	})

	t.Run("a wrong password, or none, removes nothing", func(t *testing.T) {
		t.Parallel()
		for _, body := range []string{`{"password":"nope"}`, `{"password":""}`, `{}`} {
			f := newAuthFixture(t, gomock.NewController(t))
			f.store.EXPECT().Session(gomock.Any(), sessionID).Return(session(), nil)
			f.store.EXPECT().User(gomock.Any(), userID).Return(pat(false), nil).Times(2)

			rr := httptest.NewRecorder()
			f.password.RemoveUserPassword().ServeHTTP(rr, request(signedIn(), body))
			if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "Password incorrect") {
				t.Errorf("RemoveUserPassword(%s) = %d %s, want 400 saying the password is incorrect", body, rr.Code, rr.Body.String())
			}
		}
	})

	t.Run("a person who holds no password proves none, and nothing is removed", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		f.store.EXPECT().Session(gomock.Any(), sessionID).Return(session(), nil)
		f.store.EXPECT().User(gomock.Any(), userID).Return(&sessionstorage.SessionUser{ID: userID, Username: "pat"}, nil).Times(2)

		rr := httptest.NewRecorder()
		f.password.RemoveUserPassword().ServeHTTP(rr, request(signedIn(), `{"password":"anything"}`))
		if rr.Code != http.StatusBadRequest {
			t.Errorf("RemoveUserPassword() of a person with no password = %d %s, want 400", rr.Code, rr.Body.String())
		}
	})

	t.Run("a person with no directory link keeps the password, and is told what to do", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		f.store.EXPECT().Session(gomock.Any(), sessionID).Return(session(), nil)
		f.store.EXPECT().User(gomock.Any(), userID).Return(pat(false), nil).Times(2)
		f.store.EXPECT().RemoveProvenUserPassword(gomock.Any(), userID, kept).
			Return(httpio.NewConflictMessageWithError(sessionstorage.ErrLastSignInMethod, "the user has no other means of sign-in: link a directory sign-in first"))

		rr := httptest.NewRecorder()
		f.password.RemoveUserPassword().ServeHTTP(rr, request(signedIn(), `{"password":"kept"}`))
		want := "Your password is your only way to sign in: sign in through your directory first, which links it to your account, then remove the password"
		if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), want) {
			t.Errorf("RemoveUserPassword() of a person with no link = %d %s, want 409 saying %q", rr.Code, rr.Body.String(), want)
		}
		if _, _, clears := f.counts(); clears != 0 {
			t.Errorf("the name's sign-in attempts were cleared %d times, want none: nothing was removed", clears)
		}
	})

	t.Run("a request without a session removes nothing", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		f.store.EXPECT().Session(gomock.Any(), sessionID).Return(nil, httpio.NewNotFoundMessage("no session"))

		rr := httptest.NewRecorder()
		f.password.RemoveUserPassword().ServeHTTP(rr, request(signedIn(), `{"password":"kept"}`))
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("RemoveUserPassword() = %d %s, want 401", rr.Code, rr.Body.String())
		}
	})

	t.Run("an impersonated session is refused: the password is the person's own to remove", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		ctx := impersonatedCtx(sessionID, "pat", &sessioninfo.Impersonation{SessionID: sessionID, Actor: "admin"})
		ctx = context.WithValue(ctx, sessioninfo.CtxUserInfo, &sessioninfo.UserInfo{ID: userID, Username: "pat"})

		rr := httptest.NewRecorder()
		f.password.RemoveUserPassword().ServeHTTP(rr, request(ctx, `{"password":"kept"}`))
		if rr.Code != http.StatusForbidden || !strings.Contains(rr.Body.String(), "not permitted in an impersonated session") {
			t.Errorf("RemoveUserPassword() = %d %s, want 403 naming the impersonated session", rr.Code, rr.Body.String())
		}
	})
}

// TestAuth_Methods_removePassword pins the route the account page is told: the path of
// the password method's RemoveUserPassword handler where the list names it, nothing
// where it does not, and a wiring mistake refused where the routes are wired.
func TestAuth_Methods_removePassword(t *testing.T) {
	t.Parallel()

	password := []AdvertisedMethod{{Name: "password", Label: "Password", Path: "/api/user/password/login"}}
	answered := func(t *testing.T, list *MethodList) map[string]any {
		t.Helper()

		rr := httptest.NewRecorder()
		registerThree(t).Methods(list).ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/user/methods", http.NoBody))
		if rr.Code != http.StatusOK {
			t.Fatalf("Methods() status = %d, want 200: %s", rr.Code, rr.Body.String())
		}

		var got map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}

		return got
	}

	t.Run("the list names the route, and the answer carries it", func(t *testing.T) {
		t.Parallel()
		got := answered(t, &MethodList{Methods: password, ChangePassword: "/api/user/password/change", RemovePassword: "/api/user/password/remove"})
		want := map[string]any{
			"methods":        []any{map[string]any{"name": "password", "method": "password", "kind": "password", "label": "Password", "path": "/api/user/password/login"}},
			"changePassword": "/api/user/password/change",
			"removePassword": "/api/user/password/remove",
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("Methods() answer mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("a list that names no route answers none", func(t *testing.T) {
		t.Parallel()
		if got := answered(t, &MethodList{Methods: password, ChangePassword: "/api/user/password/change"}); got["removePassword"] != nil {
			t.Errorf("Methods() answered removePassword = %v, want none", got["removePassword"])
		}
	})

	for _, tt := range []struct {
		name string
		list *MethodList
		want string
	}{
		{
			name: "the route off-site",
			list: &MethodList{Methods: password, ChangePassword: "/password", RemovePassword: "https://evil.example/remove"},
			want: "remove-your-password route",
		},
		{
			name: "the route without the password method",
			list: &MethodList{Methods: []AdvertisedMethod{{Name: "google", Label: "Google", Path: "/login"}}, RemovePassword: "/password/remove"},
			want: "lists no password method",
		},
	} {
		t.Run(tt.name+" is refused where the routes are wired", func(t *testing.T) {
			t.Parallel()
			a := registerThree(t)

			recovered := registrationPanic(func() { a.Methods(tt.list) })
			if msg, _ := recovered.(string); !strings.Contains(msg, tt.want) {
				t.Errorf("Methods() panic = %v, want it to contain %q", recovered, tt.want)
			}
		})
	}
}
