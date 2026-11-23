package session

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/internal/mockstore"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	gomock "go.uber.org/mock/gomock"
)

// passwordDisabledSentence is the refusal of a first password to a person whose password
// the tenants' sign-in settings disable.
const passwordDisabledSentence = "Your organization does not sign in with a password here, so there is none to choose. Keep signing in the way your organization has set up"

// passwordCheck is a password-disabled check that answers disabled, or fails with err,
// and records whom it was asked about.
type passwordCheck struct {
	mu       sync.Mutex
	disabled bool
	err      error
	asked    []impulse.UUID
}

// check is the check the Auth is given (SetPasswordDisabledCheck).
func (c *passwordCheck) check(_ context.Context, userID impulse.UUID) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.asked = append(c.asked, userID)

	return c.disabled, c.err
}

// askedAbout is whom the check was asked about, in order.
func (c *passwordCheck) askedAbout() []impulse.UUID {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.asked
}

// TestAuth_SetPasswordDisabledCheck proves the password-disabled check is set once,
// before the Auth serves: anything else is a wiring mistake and panics in words.
func TestAuth_SetPasswordDisabledCheck(t *testing.T) {
	t.Parallel()

	check := (&passwordCheck{}).check
	tests := []struct {
		name    string
		prepare func(f *authFixture)
		check   func(ctx context.Context, userID impulse.UUID) (bool, error)
		want    string
	}{
		{
			name:  "no check",
			check: nil,
			want:  "session: SetPasswordDisabledCheck was given nil: pass signin.PasswordsDisabledByTenants over the role store and the declarations the tenant policy reads, or set none",
		},
		{
			name:    "a second check",
			prepare: func(f *authFixture) { f.auth.SetPasswordDisabledCheck(check) },
			check:   check,
			want:    "session: the password-disabled check was set twice: one Auth has one",
		},
		{
			name: "a check set once the Auth is in use",
			prepare: func(f *authFixture) {
				f.serve(f.auth.Authenticated(), http.MethodGet, "/authenticated", nil, nil)
			},
			check: check,
			want:  "session: the password-disabled check was set after the Auth served a request: set it before serving",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newAuthFixture(t, gomock.NewController(t))
			f.store.EXPECT().Session(gomock.Any(), gomock.Any()).Return(nil, httpio.NewNotFoundMessage("no session")).AnyTimes()
			if tt.prepare != nil {
				tt.prepare(f)
			}

			recovered := registrationPanic(func() { f.auth.SetPasswordDisabledCheck(tt.check) })
			if msg, _ := recovered.(string); msg != tt.want {
				t.Errorf("SetPasswordDisabledCheck() panic = %v, want %q", recovered, tt.want)
			}
		})
	}
}

// directoriesAuthWithoutPassword is directoriesAuth with no password method: the Auth a
// registration that synchronizes roles stands alone on.
func directoriesAuthWithoutPassword(t *testing.T) (*Auth[NoCustomData, NoCustomData, mockTx], *mockstore.AuthStore[mockTx]) {
	t.Helper()

	store := mockstore.NewAuthStore[mockTx](gomock.NewController(t))
	store.EXPECT().CustomUserDataType().Return(nil).AnyTimes()
	store.EXPECT().SetUsernameIsEmail(gomock.Any()).AnyTimes()
	a, err := NewAuth[NoCustomData, NoCustomData](store, cookieKey, NoSuperAdministrators(), quietMail())
	if err != nil {
		t.Fatalf("NewAuth() error = %v", err)
	}
	registerDirectories(a)

	return a, store
}

// TestAuth_SignInMeans_passwordDisabled pins what the read of a person's sign-in means
// says of a password the tenants' sign-in settings disable: the Auth's check, asked about
// the person the read is for, whether or not they hold a password, on the person's own
// read and the API's alike; false where the check says so, where the Auth holds none, and
// on an Auth with no password method, which never asks it; and a check that fails fails
// the read, which answers nothing of the person's means.
func TestAuth_SignInMeans_passwordDisabled(t *testing.T) {
	t.Parallel()

	userID := impulse.Must(impulse.NewUUID())
	sessionID := impulse.Must(impulse.NewUUID())
	person := func(holdsPassword bool) *sessionstorage.SessionUser {
		user := &sessionstorage.SessionUser{ID: userID, Username: "pat"}
		if holdsPassword {
			user.PasswordHash = hashed(t, "kept")
		}

		return user
	}
	googleLink := []*sessionstorage.SessionIdentity{{ID: impulse.Must(impulse.NewUUID()), UserID: userID, Method: sessioninfo.MethodGoogle, Connection: "fleet", Subject: "sub"}}
	ownRead := func(t *testing.T, a *Auth[NoCustomData, NoCustomData, mockTx]) *httptest.ResponseRecorder {
		t.Helper()

		ctx := context.WithValue(context.Background(), sessioninfo.CTXSessionID, sessionID)
		rr := httptest.NewRecorder()
		a.SignInMeans().ServeHTTP(rr, httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/user/sign-in-means", http.NoBody))

		return rr
	}
	session := func() *sessioninfo.SessionData {
		return &sessioninfo.SessionData{SessionInfo: &sessioninfo.SessionInfo{ID: sessionID, Username: "pat", UpdatedAt: time.Now()}, UserID: impulse.NullUUIDFromUUID(userID)}
	}

	for _, holdsPassword := range []bool{true, false} {
		name := "a person who holds no password"
		if holdsPassword {
			name = "a person who holds a password"
		}

		t.Run(name+": a check that says disabled is answered on the person's own read", func(t *testing.T) {
			t.Parallel()
			a, store := signInMeansAuth(t)
			check := &passwordCheck{disabled: true}
			a.SetPasswordDisabledCheck(check.check)
			store.EXPECT().Session(gomock.Any(), sessionID).Return(session(), nil)
			store.EXPECT().User(gomock.Any(), userID).Return(person(holdsPassword), nil).Times(2)
			store.EXPECT().IdentitiesByUser(gomock.Any(), userID).Return(googleLink, nil)

			rr := ownRead(t, a)
			if rr.Code != http.StatusOK {
				t.Fatalf("SignInMeans() = %d %s, want 200", rr.Code, rr.Body.String())
			}
			var got map[string]any
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
			want := map[string]any{
				"password": holdsPassword, "directories": []any{map[string]any{"name": "google", "label": "Fleet Google"}},
				"directoryAddress": true, "passwordDisabled": true,
			}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("SignInMeans() answer mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff([]impulse.UUID{userID}, check.askedAbout()); diff != "" {
				t.Errorf("whom the check was asked about mismatch (-want +got):\n%s", diff)
			}
		})

		t.Run(name+": a check that says disabled is answered by the API", func(t *testing.T) {
			t.Parallel()
			a, store := signInMeansAuth(t)
			check := &passwordCheck{disabled: true}
			a.SetPasswordDisabledCheck(check.check)
			store.EXPECT().User(gomock.Any(), userID).Return(person(holdsPassword), nil)
			store.EXPECT().IdentitiesByUser(gomock.Any(), userID).Return(googleLink, nil)

			got, err := a.API().SignInMeans(context.Background(), userID)
			if err != nil {
				t.Fatalf("SignInMeans() error = %v", err)
			}
			want := &SignInMeans{Password: holdsPassword, Directories: []LinkedDirectory{{Name: "google", Label: "Fleet Google"}}, DirectoryAddress: true, PasswordDisabled: true}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("SignInMeans() mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff([]impulse.UUID{userID}, check.askedAbout()); diff != "" {
				t.Errorf("whom the check was asked about mismatch (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("a check that says not disabled is answered false", func(t *testing.T) {
		t.Parallel()
		a, store := signInMeansAuth(t)
		check := &passwordCheck{}
		a.SetPasswordDisabledCheck(check.check)
		store.EXPECT().User(gomock.Any(), userID).Return(person(false), nil)
		store.EXPECT().IdentitiesByUser(gomock.Any(), userID).Return(googleLink, nil)

		got, err := a.API().SignInMeans(context.Background(), userID)
		if err != nil {
			t.Fatalf("SignInMeans() error = %v", err)
		}
		if got.PasswordDisabled {
			t.Error("SignInMeans().PasswordDisabled = true, want false where the check says so")
		}
		if diff := cmp.Diff([]impulse.UUID{userID}, check.askedAbout()); diff != "" {
			t.Errorf("whom the check was asked about mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("an Auth with no check answers false", func(t *testing.T) {
		t.Parallel()
		a, store := signInMeansAuth(t)
		store.EXPECT().User(gomock.Any(), userID).Return(person(false), nil)
		store.EXPECT().IdentitiesByUser(gomock.Any(), userID).Return(googleLink, nil)

		got, err := a.API().SignInMeans(context.Background(), userID)
		if err != nil {
			t.Fatalf("SignInMeans() error = %v", err)
		}
		if got.PasswordDisabled {
			t.Error("SignInMeans().PasswordDisabled = true, want false on an Auth with no check")
		}
	})

	t.Run("an Auth with no password method answers false and never asks the check", func(t *testing.T) {
		t.Parallel()
		a, store := directoriesAuthWithoutPassword(t)
		check := &passwordCheck{disabled: true}
		a.SetPasswordDisabledCheck(check.check)
		store.EXPECT().User(gomock.Any(), userID).Return(person(false), nil)
		store.EXPECT().IdentitiesByUser(gomock.Any(), userID).Return(googleLink, nil)

		got, err := a.API().SignInMeans(context.Background(), userID)
		if err != nil {
			t.Fatalf("SignInMeans() error = %v", err)
		}
		if got.PasswordDisabled {
			t.Error("SignInMeans().PasswordDisabled = true, want false on an Auth with no password method")
		}
		if asked := check.askedAbout(); len(asked) != 0 {
			t.Errorf("the check was asked about %v, want never on an Auth with no password method", asked)
		}
	})

	t.Run("a check that fails fails the read, which answers nothing of the person's means", func(t *testing.T) {
		t.Parallel()
		a, store := signInMeansAuth(t)
		check := &passwordCheck{err: errors.New("roles blip")}
		a.SetPasswordDisabledCheck(check.check)
		store.EXPECT().Session(gomock.Any(), sessionID).Return(session(), nil)
		store.EXPECT().User(gomock.Any(), userID).Return(person(true), nil).AnyTimes()
		store.EXPECT().IdentitiesByUser(gomock.Any(), userID).Return(googleLink, nil).AnyTimes()

		rr := ownRead(t, a)
		if rr.Code != http.StatusInternalServerError || strings.Contains(rr.Body.String(), "directories") || strings.Contains(rr.Body.String(), "passwordDisabled") {
			t.Errorf("SignInMeans() with a failing check = %d %s, want 500 and nothing of the person's means", rr.Code, rr.Body.String())
		}
		if got, err := a.API().SignInMeans(context.Background(), userID); err == nil || got != nil {
			t.Errorf("AuthAPI.SignInMeans() with a failing check = %v, %v, want no means and the error", got, err)
		}
	})
}

// TestAuth_FirstPassword_passwordDisabled pins the refusal of a first password to a
// person whose password the tenants' sign-in settings disable: both routes answer 403 in
// words, before a code is mailed or recorded and before a password is written; an account
// that holds a password is still answered that it holds one, without the check; and a
// check that fails fails the request, with nothing mailed.
func TestAuth_FirstPassword_passwordDisabled(t *testing.T) {
	t.Parallel()

	t.Run("a person whose password is disabled is refused the code and the password, with nothing mailed or written", func(t *testing.T) {
		t.Parallel()
		userID, sessionID := newIDs()
		f, recorder, links := newMailFixture(t, gomock.NewController(t))
		check := &passwordCheck{disabled: true}
		f.auth.SetPasswordDisabledCheck(check.check)
		f.expectSession(sessionID, directoryPerson(userID))
		// The store is given no SetFirstUserPassword: the mock fails the test on a call.

		rr := f.asUser(f.auth.FirstPassword().SendCode(), sessionID, nil, nil)
		if rr.Code != http.StatusForbidden || !strings.Contains(rr.Body.String(), passwordDisabledSentence) {
			t.Errorf("SendCode() for a person whose password is disabled = %d %s, want 403 with %q", rr.Code, rr.Body.String(), passwordDisabledSentence)
		}
		rr = f.asUser(f.auth.FirstPassword().Set(), sessionID, map[string]string{"code": "123456", "password": chosenPassword}, nil)
		if rr.Code != http.StatusForbidden || !strings.Contains(rr.Body.String(), passwordDisabledSentence) {
			t.Errorf("Set() for a person whose password is disabled = %d %s, want 403 with %q", rr.Code, rr.Body.String(), passwordDisabledSentence)
		}
		if got := len(recorder.Messages()); got != 0 {
			t.Errorf("%d messages were sent, want none", got)
		}
		links.mu.Lock()
		inserted := len(links.rows)
		links.mu.Unlock()
		if inserted != 0 {
			t.Errorf("%d mail link rows were inserted, want none", inserted)
		}
		if diff := cmp.Diff([]impulse.UUID{userID, userID}, check.askedAbout()); diff != "" {
			t.Errorf("whom the check was asked about mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("an account that holds a password is answered that it holds one, and the check is not asked", func(t *testing.T) {
		t.Parallel()
		userID, sessionID := newIDs()
		f, recorder, _ := newMailFixture(t, gomock.NewController(t))
		check := &passwordCheck{disabled: true}
		f.auth.SetPasswordDisabledCheck(check.check)
		user := directoryPerson(userID)
		user.PasswordHash = hashed(t, "the one I have")
		f.expectSession(sessionID, user)

		rr := f.asUser(f.auth.FirstPassword().SendCode(), sessionID, nil, nil)
		if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "already has a password") {
			t.Errorf("SendCode() for an account that holds a password = %d %s, want 409 saying it has one", rr.Code, rr.Body.String())
		}
		rr = f.asUser(f.auth.FirstPassword().Set(), sessionID, map[string]string{"code": "123456", "password": chosenPassword}, nil)
		if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "already has a password") {
			t.Errorf("Set() for an account that holds a password = %d %s, want 409 saying it has one", rr.Code, rr.Body.String())
		}
		if got := len(recorder.Messages()); got != 0 {
			t.Errorf("%d messages were sent, want none", got)
		}
		if asked := check.askedAbout(); len(asked) != 0 {
			t.Errorf("the check was asked about %v, want not asked once the account is known to hold a password", asked)
		}
	})

	t.Run("a check that fails fails the request, with nothing mailed or written", func(t *testing.T) {
		t.Parallel()
		userID, sessionID := newIDs()
		f, recorder, links := newMailFixture(t, gomock.NewController(t))
		f.auth.SetPasswordDisabledCheck((&passwordCheck{err: errors.New("roles blip")}).check)
		f.expectSession(sessionID, directoryPerson(userID))

		rr := f.asUser(f.auth.FirstPassword().SendCode(), sessionID, nil, nil)
		if rr.Code != http.StatusInternalServerError {
			t.Errorf("SendCode() with a failing check = %d %s, want 500", rr.Code, rr.Body.String())
		}
		rr = f.asUser(f.auth.FirstPassword().Set(), sessionID, map[string]string{"code": "123456", "password": chosenPassword}, nil)
		if rr.Code != http.StatusInternalServerError {
			t.Errorf("Set() with a failing check = %d %s, want 500", rr.Code, rr.Body.String())
		}
		if got := len(recorder.Messages()); got != 0 {
			t.Errorf("%d messages were sent, want none", got)
		}
		links.mu.Lock()
		inserted := len(links.rows)
		links.mu.Unlock()
		if inserted != 0 {
			t.Errorf("%d mail link rows were inserted, want none", inserted)
		}
	})
}
