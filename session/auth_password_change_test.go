package session

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/auditlog"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/securehash"
	internalcookie "github.com/impulseframework/impulse/session/internal/cookie"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	gomock "go.uber.org/mock/gomock"
)

// passwordChangeRequest is a request of the password change route made in the session.
func passwordChangeRequest(sessionID impulse.UUID, body map[string]string) *http.Request {
	raw, _ := json.Marshal(body)
	r := httptest.NewRequestWithContext(context.WithValue(context.Background(), sessioninfo.CTXSessionID, sessionID), http.MethodPost, "/api/user/password/change", strings.NewReader(string(raw)))
	r.Header.Set("Content-Type", "application/json")

	return r
}

// TestAuth_ChangeUserPassword_continuesItsSession pins the session a person holds after
// their own password change: it is the session the change was made in, continued, with
// the establishment and the factors that session recorded, as the store holds them. A
// tenant's administrator who entered with a password and the mailed code still carries
// the code, so a directory-only tenant still admits them; a person who came through
// their organization's directory still holds a directory's session; and a session that
// recorded nothing starts one that records what the change proved.
func TestAuth_ChangeUserPassword_continuesItsSession(t *testing.T) {
	t.Parallel()

	signedIn := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	tests := []struct {
		name   string
		events []sessioninfo.AuthEvent
		want   []sessioninfo.AuthEvent
	}{
		{
			name: "a tenant's administrator who entered with a password and the mailed code keeps the code",
			events: []sessioninfo.AuthEvent{
				{Method: sessioninfo.MethodPassword, At: signedIn},
				{Method: sessioninfo.MethodEmailCode, At: signedIn.Add(time.Minute)},
			},
			want: []sessioninfo.AuthEvent{
				{Method: sessioninfo.MethodPassword, At: signedIn},
				{Method: sessioninfo.MethodEmailCode, At: signedIn.Add(time.Minute)},
			},
		},
		{
			name: "a person who came through their organization's directory still holds the directory's session",
			events: []sessioninfo.AuthEvent{
				{Method: sessioninfo.MethodGoogle, Connection: "lakeside.edu", At: signedIn},
				{Method: sessioninfo.MethodPassword, At: signedIn.Add(time.Minute)},
			},
			want: []sessioninfo.AuthEvent{
				{Method: sessioninfo.MethodGoogle, Connection: "lakeside.edu", At: signedIn},
				{Method: sessioninfo.MethodPassword, At: signedIn.Add(time.Minute)},
			},
		},
		{
			name: "a session that recorded no event starts one that records the password the change proved",
			want: []sessioninfo.AuthEvent{{Method: sessioninfo.MethodPassword}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			userID, sessionID := newIDs()
			f := newAuthFixture(t, gomock.NewController(t))
			old := hashed(t, "the old one")
			user := &sessionstorage.SessionUser{ID: userID, Username: "pat", PasswordHash: old}
			f.store.EXPECT().Session(gomock.Any(), sessionID).Return(&sessioninfo.SessionData{
				SessionInfo: &sessioninfo.SessionInfo{ID: sessionID, Username: "pat", UpdatedAt: time.Now()},
				UserID:      impulse.NullUUIDFromUUID(userID), AuthEvents: tt.events,
			}, nil)
			f.store.EXPECT().User(gomock.Any(), userID).Return(user, nil).Times(2)
			f.store.EXPECT().ChangeUserPassword(gomock.Any(), userID, old, gomock.Not(gomock.Nil()), gomock.Nil()).Return(nil)
			var got []sessioninfo.AuthEvent
			createSession(f.store, impulse.Must(impulse.NewUUID()), func(req *sessioninfo.NewSessionRequest) {
				if req.Reason != sessioninfo.ReasonRegeneration || req.UserID != userID {
					t.Errorf("CreateSession() request = %+v, want the regeneration of the user's session", req)
				}
				got = req.AuthEvents
			})

			rr := httptest.NewRecorder()
			f.password.ChangeUserPassword().ServeHTTP(rr, passwordChangeRequest(sessionID, map[string]string{"oldPassword": "the old one", "newPassword": "the new one, chosen with care"}))
			if rr.Code != http.StatusOK {
				t.Fatalf("ChangeUserPassword() = %d %s, want 200", rr.Code, rr.Body.String())
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("the auth events of the session after the change (-want +got):\n%s", diff)
			}
		})
	}
}

// TestAuth_ChangeUserPassword_madeWhenItsSessionCannotStart pins a change whose
// password the store wrote, ending every session of the person with it, and whose
// continued session then could not be started (here, the store refuses its insert): the
// change is answered as made, on the route (200) and through the API (nil), with the new
// password the one written and no session cookie set, so the person signs in again with
// the password they now hold.
func TestAuth_ChangeUserPassword_madeWhenItsSessionCannotStart(t *testing.T) {
	t.Parallel()

	const oldPassword, newPassword = "the old one", "the new one, chosen with care"
	// expectWrite has the store write the password and refuse the continued session's
	// insert, and returns where the hash written is kept.
	expectWrite := func(t *testing.T, f *authFixture, userID impulse.UUID, users int) **securehash.Hash {
		t.Helper()
		old := hashed(t, oldPassword)
		written := new(*securehash.Hash)
		f.store.EXPECT().User(gomock.Any(), userID).Return(&sessionstorage.SessionUser{ID: userID, Username: "pat", PasswordHash: old}, nil).Times(users)
		f.store.EXPECT().ChangeUserPassword(gomock.Any(), userID, old, gomock.Not(gomock.Nil()), gomock.Nil()).DoAndReturn(
			func(_ context.Context, _ impulse.UUID, _, hash *securehash.Hash, _ *auditlog.Record) error {
				*written = hash

				return nil
			})
		f.store.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(impulse.NilUUID, errors.New("the store refused the session's insert"))

		return written
	}
	// madeWithNoSession checks that the hash written is the new password's and that the
	// response carries no session.
	madeWithNoSession := func(t *testing.T, written *securehash.Hash, rr *httptest.ResponseRecorder) {
		t.Helper()
		if written == nil {
			t.Fatal("ChangeUserPassword() was given no hash, want the new password's")
		}
		if _, err := securehash.New(securehash.Argon2()).Compare(written, newPassword); err != nil {
			t.Errorf("the hash written is not the new password's: %v", err)
		}
		if c := cookieNamed(rr, internalcookie.AuthCookieName); c != nil && c.Value != "" {
			t.Error("the response sets a session cookie, want none: the session that goes on from the change could not be started")
		}
	}

	t.Run("on the route, 200", func(t *testing.T) {
		t.Parallel()

		userID, sessionID := newIDs()
		f := newAuthFixture(t, gomock.NewController(t))
		f.store.EXPECT().Session(gomock.Any(), sessionID).Return(&sessioninfo.SessionData{
			SessionInfo: &sessioninfo.SessionInfo{ID: sessionID, Username: "pat", UpdatedAt: time.Now()},
			UserID:      impulse.NullUUIDFromUUID(userID),
		}, nil)
		written := expectWrite(t, f, userID, 2)

		rr := httptest.NewRecorder()
		f.password.ChangeUserPassword().ServeHTTP(rr, passwordChangeRequest(sessionID, map[string]string{"oldPassword": oldPassword, "newPassword": newPassword}))

		if rr.Code != http.StatusOK {
			t.Errorf("ChangeUserPassword() = %d %s, want 200: the password was changed", rr.Code, rr.Body.String())
		}
		madeWithNoSession(t, *written, rr)
	})

	t.Run("through the API, nil", func(t *testing.T) {
		t.Parallel()

		userID, sessionID := newIDs()
		f := newAuthFixture(t, gomock.NewController(t))
		written := expectWrite(t, f, userID, 1)
		ctx := context.WithValue(context.Background(), sessioninfo.CtxSessionInfo, &sessioninfo.SessionData{
			SessionInfo: &sessioninfo.SessionInfo{ID: sessionID, Username: "pat"}, UserID: impulse.NullUUIDFromUUID(userID),
		})

		rr := httptest.NewRecorder()
		if err := f.auth.API().ChangeSessionUserPassword(ctx, rr, userID, &ChangeSessionUserPasswordRequest{OldPassword: oldPassword, NewPassword: newPassword}); err != nil {
			t.Errorf("ChangeSessionUserPassword() error = %v, want nil: the password was changed", err)
		}
		madeWithNoSession(t, *written, rr)
	})
}

// TestAuthAPI_ChangeSessionUserPassword_nothingToContinue pins the change made with no
// session of the person's own in the context: no session at all, as in a call of the
// application's, another person's session, one that views the application as the
// person, and a request a bearer token stands in for. The password is changed and the
// person's sessions are ended, and no session is started, so none rests on a password
// the sign-in policy was not asked about: the store is given no CreateSession (the mock
// refuses a call it was not told of), the response sets no cookie, and the call returns
// nil. The person signs in again, where the sign-in policy decides.
func TestAuthAPI_ChangeSessionUserPassword_nothingToContinue(t *testing.T) {
	t.Parallel()

	userID, sessionID := newIDs()
	other := impulse.Must(impulse.NewUUID())
	strong := []sessioninfo.AuthEvent{{Method: sessioninfo.MethodGoogle, Connection: "lakeside.edu"}, {Method: sessioninfo.MethodAttestedByEvidence}}
	tests := []struct {
		name    string
		session *sessioninfo.SessionData
	}{
		{
			name:    "a session of another person's is not this person's to continue",
			session: &sessioninfo.SessionData{SessionInfo: &sessioninfo.SessionInfo{ID: sessionID, Username: "sam"}, UserID: impulse.NullUUIDFromUUID(other), AuthEvents: strong},
		},
		{
			name: "a session that views the application as the person is not theirs either",
			session: &sessioninfo.SessionData{
				SessionInfo: &sessioninfo.SessionInfo{ID: sessionID, Username: "pat"}, UserID: impulse.NullUUIDFromUUID(userID), AuthEvents: strong,
				Impersonation: &sessioninfo.Impersonation{Actor: "sam"},
			},
		},
		{
			name: "a request a bearer token of the person's stands in for is no session to continue",
			session: &sessioninfo.SessionData{
				SessionInfo: &sessioninfo.SessionInfo{ID: sessionID, Username: "pat"}, UserID: impulse.NullUUIDFromUUID(userID), AuthEvents: strong,
				Token: &sessioninfo.TokenInfo{},
			},
		},
		{
			name: "a call of the application's, with no session in the context, starts none",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newAuthFixture(t, gomock.NewController(t))
			old := hashed(t, "the old one")
			f.store.EXPECT().User(gomock.Any(), userID).Return(&sessionstorage.SessionUser{ID: userID, Username: "pat", PasswordHash: old}, nil)
			f.store.EXPECT().ChangeUserPassword(gomock.Any(), userID, old, gomock.Not(gomock.Nil()), gomock.Nil()).Return(nil)
			f.store.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Times(0)

			ctx := context.Background()
			if tt.session != nil {
				ctx = context.WithValue(ctx, sessioninfo.CtxSessionInfo, tt.session)
			}
			rr := httptest.NewRecorder()
			err := f.auth.API().ChangeSessionUserPassword(ctx, rr, userID, &ChangeSessionUserPasswordRequest{OldPassword: "the old one", NewPassword: "the new one, chosen with care"})
			if err != nil {
				t.Fatalf("ChangeSessionUserPassword() error = %v", err)
			}
			if cookies := rr.Header().Values("Set-Cookie"); len(cookies) != 0 {
				t.Errorf("the response sets %d cookies, want none: no session is started", len(cookies))
			}
		})
	}
}

// The sentences a password the judgment refuses is answered with.
const (
	tooEasyToGuess  = "This password is too easy to guess. Make it longer, or use several words that do not go together."
	builtOnDetails  = "This password is built on your name, username or email address. Choose one that is not."
	foundInBreaches = "This password is on a list of passwords found in data breaches. Choose a different one."
)

// The passwords the tests of the judgment set, each as the judgment measures it for a
// person it is not built on. Each is longer than eight characters, so a test of one fails
// where a password is held to its length alone.
const (
	// weakPassword is too easy to guess under the default minimum of 30 bits, reaches 10
	// and is on no list.
	weakPassword = "kestrelvaughn"
	// strongAt30 reaches the default minimum and falls short of 60 bits.
	strongAt30 = "correcthorsebatterystaple"
	// listedPassword is on the list of passwords found in breaches.
	listedPassword = "password1"
)

// refusedWith checks that err is the 400 that says sentence.
func refusedWith(t *testing.T, err error, sentence string) {
	t.Helper()
	if !httpio.HasBadRequest(err) || httpio.Message(err) != sentence {
		t.Errorf("error = %v, want 400 saying %q", err, sentence)
	}
}

// answeredWith checks that rr is the 400 that says sentence.
func answeredWith(t *testing.T, rr *httptest.ResponseRecorder, sentence string) {
	t.Helper()
	if rr.Code != http.StatusBadRequest || decoded(t, rr)["message"] != sentence {
		t.Errorf("answer = %d %s, want 400 saying %q", rr.Code, rr.Body.String(), sentence)
	}
}

// TestAuth_passwordRule_everyPath pins the one judgment of a password's strength on every
// path that sets one: a password too easy to guess under the default minimum is refused
// 400 with the sentence that says so at a person's own change, on the route and through
// the API, at an account created with one, at a sign-up and the acceptance of an
// invitation, and at the end of a reset link; nothing is written (the mock refuses a
// write it was not told of), and the reset link stands. A person's first password is
// held to it in TestAuth_FirstPassword.
func TestAuth_passwordRule_everyPath(t *testing.T) {
	t.Parallel()

	userID, sessionID := newIDs()
	old := func(t *testing.T, f *authFixture, times int) {
		t.Helper()
		f.store.EXPECT().User(gomock.Any(), userID).Return(&sessionstorage.SessionUser{ID: userID, Username: "pat", PasswordHash: hashed(t, "the old one")}, nil).Times(times)
	}

	t.Run("a person's own change, on the route", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		f.store.EXPECT().Session(gomock.Any(), sessionID).Return(&sessioninfo.SessionData{
			SessionInfo: &sessioninfo.SessionInfo{ID: sessionID, Username: "pat", UpdatedAt: time.Now()}, UserID: impulse.NullUUIDFromUUID(userID),
		}, nil)
		old(t, f, 2)

		rr := httptest.NewRecorder()
		f.password.ChangeUserPassword().ServeHTTP(rr, passwordChangeRequest(sessionID, map[string]string{"oldPassword": "the old one", "newPassword": weakPassword}))
		answeredWith(t, rr, tooEasyToGuess)
	})
	t.Run("a person's own change, through the API", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		old(t, f, 1)
		refusedWith(t, f.auth.API().ChangeSessionUserPassword(context.Background(), httptest.NewRecorder(), userID, &ChangeSessionUserPasswordRequest{OldPassword: "the old one", NewPassword: weakPassword}), tooEasyToGuess)
	})
	t.Run("an account created with a password", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		password := weakPassword
		_, err := f.auth.API().CreateSessionUser(context.Background(), &CreateUserRequest{Username: "pat", Password: &password})
		refusedWith(t, err, tooEasyToGuess)
	})
	t.Run("a sign-up and the acceptance of an invitation", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		_, err := f.auth.newAccount("pat@lakeside.edu", "pat", weakPassword, "")
		refusedWith(t, err, tooEasyToGuess)
	})
	t.Run("the end of a reset link", func(t *testing.T) {
		t.Parallel()
		f, recorder, links := newMailFixture(t, gomock.NewController(t))
		f.store.EXPECT().User(gomock.Any(), userID).Return(passwordPerson(t, userID), nil).AnyTimes()
		token := mailedReset(t, f, recorder, userID)

		answeredWith(t, f.serve(f.auth.PasswordReset().Complete(), http.MethodPost, "/reset/complete", map[string]string{"token": token, "password": weakPassword}, nil), tooEasyToGuess)
		if links.rows[0].ConsumedAt != nil {
			t.Error("a refused password spent the reset link, want it standing")
		}
	})
}

// TestAuth_passwordRule_theirOwnDetailsAndTheList pins the judgment's two other refusals,
// on a path of a person signed in (their own change) and one of a person before an
// account is theirs (a sign-up through an invitation): a password that is the person's
// username is refused with the sentence that names their details, and a password on the
// list of passwords found in breaches with the sentence that names the list. Nothing is
// written, held or mailed.
func TestAuth_passwordRule_theirOwnDetailsAndTheList(t *testing.T) {
	t.Parallel()

	const username = "vexillumtrek"
	tests := []struct {
		name     string
		password string
		want     string
	}{
		{name: "the username", password: username, want: builtOnDetails},
		{name: "a password found in breaches", password: listedPassword, want: foundInBreaches},
	}
	for _, tt := range tests {
		t.Run(tt.name+", at a person's own change", func(t *testing.T) {
			t.Parallel()
			userID, _ := newIDs()
			f := newAuthFixture(t, gomock.NewController(t))
			f.store.EXPECT().User(gomock.Any(), userID).Return(&sessionstorage.SessionUser{ID: userID, Username: username, PasswordHash: hashed(t, "the old one")}, nil)

			// The mocked store expects no ChangeUserPassword: a call fails the test.
			err := f.auth.API().ChangeSessionUserPassword(context.Background(), httptest.NewRecorder(), userID, &ChangeSessionUserPasswordRequest{OldPassword: "the old one", NewPassword: tt.password})
			refusedWith(t, err, tt.want)
		})
		t.Run(tt.name+", at a sign-up through an invitation", func(t *testing.T) {
			t.Parallel()
			f, recorder, _, cookies := invitedPat(t)

			// The mocked store expects no HoldSignIn: a call fails the test.
			rr := f.serve(f.auth.Invitation().SignUp(), http.MethodPost, "/invitation/sign-up", map[string]string{"password": tt.password, "username": username}, cookies)
			answeredWith(t, rr, tt.want)
			if cookieNamed(rr, defaultPendingCookieName) != nil || len(recorder.Messages()) != 1 {
				t.Errorf("SignUp() set a pending cookie (%t) or mailed (%d messages), want neither", cookieNamed(rr, defaultPendingCookieName) != nil, len(recorder.Messages())-1)
			}
		})
	}
}

// TestAuth_CreateSessionUser_judgedAgainstTheRequest pins the person an account created
// with a password is judged for: the one the request names. A password built on the
// request's name is refused with the sentence that names their details, and nothing is
// created.
func TestAuth_CreateSessionUser_judgedAgainstTheRequest(t *testing.T) {
	t.Parallel()

	f := newAuthFixture(t, gomock.NewController(t))
	password, name := "Zabrowski2077", "Quentin Zabrowski"

	// The mocked store expects no CreateUser: a call fails the test.
	_, err := f.auth.API().CreateSessionUser(context.Background(), &CreateUserRequest{Username: "pat", Name: &name, Password: &password})
	refusedWith(t, err, builtOnDetails)
}

// TestAuth_passwordRule_theApplicationsMinimum pins the minimum the judgment applies: the
// password method's. A password the default of 30 bits takes is refused under
// MinimumPasswordStrength(60), and one the default refuses is taken under
// MinimumPasswordStrength(10); an Auth with no password method judges under the default.
func TestAuth_passwordRule_theApplicationsMinimum(t *testing.T) {
	t.Parallel()

	create := func(f *authFixture, password string) error {
		_, err := f.auth.API().CreateSessionUser(context.Background(), &CreateUserRequest{Username: "pat", Password: &password})

		return err
	}
	created := func(f *authFixture) {
		f.store.EXPECT().CreateUser(gomock.Any(), gomock.Any(), nil).Return(&sessionstorage.SessionUser{ID: impulse.Must(impulse.NewUUID()), Username: "pat"}, nil)
	}

	t.Run("a raised minimum refuses a password the default takes", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixtureWith(t, gomock.NewController(t), []PasswordOption{MinimumPasswordStrength(60)})
		refusedWith(t, create(f, strongAt30), tooEasyToGuess)
	})
	t.Run("a lowered minimum takes a password the default refuses", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixtureWith(t, gomock.NewController(t), []PasswordOption{MinimumPasswordStrength(10)})
		created(f)
		if err := create(f, weakPassword); err != nil {
			t.Errorf("CreateSessionUser() under a minimum of 10 bits: error = %v, want the account", err)
		}
	})
	t.Run("an Auth with no password method judges under the default", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixtureWithoutPassword(t, gomock.NewController(t))
		refusedWith(t, create(f, weakPassword), tooEasyToGuess)
		created(f)
		if err := create(f, strongAt30); err != nil {
			t.Errorf("CreateSessionUser() of a password that reaches 30 bits: error = %v, want the account", err)
		}
	})
}

func TestPasswordSignIn_MinimumStrengthRefused(t *testing.T) {
	t.Parallel()

	for _, bits := range []int{0, -1} {
		t.Run(fmt.Sprint(bits), func(t *testing.T) {
			t.Parallel()
			a, err := NewAuth[NoCustomData, NoCustomData](newAuthStoreMock(gomock.NewController(t)), cookieKey, NoSuperAdministrators(), quietMail())
			if err != nil {
				t.Fatal(err)
			}

			want := fmt.Sprintf("MinimumPasswordStrength(%d): the minimum is at least 1 bit", bits)
			defer func() {
				err, _ := recover().(error)
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Errorf("PasswordSignIn() panic = %v, want %q", err, want)
				}
			}()
			PasswordSignIn(a, MinimumPasswordStrength(bits))
		})
	}
}
