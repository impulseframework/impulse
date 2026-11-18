package session

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
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
			f.store.EXPECT().ChangeUserPassword(gomock.Any(), userID, old, gomock.Not(gomock.Nil())).Return(nil)
			var got []sessioninfo.AuthEvent
			createSession(f.store, impulse.Must(impulse.NewUUID()), func(req *sessioninfo.NewSessionRequest) {
				if req.Reason != sessioninfo.ReasonRegeneration || req.UserID != userID {
					t.Errorf("CreateSession() request = %+v, want the regeneration of the user's session", req)
				}
				got = req.AuthEvents
			})

			rr := httptest.NewRecorder()
			f.password.ChangeUserPassword().ServeHTTP(rr, passwordChangeRequest(sessionID, map[string]string{"oldPassword": "the old one", "newPassword": "the new one"}))
			if rr.Code != http.StatusOK {
				t.Fatalf("ChangeUserPassword() = %d %s, want 200", rr.Code, rr.Body.String())
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("the auth events of the session after the change (-want +got):\n%s", diff)
			}
		})
	}
}

// TestAuthAPI_ChangeSessionUserPassword_nothingToContinue pins the change made with no
// session of the person's own in the context: another person's session, and one that
// views the application as the person, are not continued, and the new session records
// the password the change proved.
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newAuthFixture(t, gomock.NewController(t))
			old := hashed(t, "the old one")
			f.store.EXPECT().User(gomock.Any(), userID).Return(&sessionstorage.SessionUser{ID: userID, Username: "pat", PasswordHash: old}, nil)
			f.store.EXPECT().ChangeUserPassword(gomock.Any(), userID, old, gomock.Not(gomock.Nil())).Return(nil)
			createSession(f.store, impulse.Must(impulse.NewUUID()), func(req *sessioninfo.NewSessionRequest) {
				if diff := cmp.Diff([]sessioninfo.AuthEvent{{Method: sessioninfo.MethodPassword}}, req.AuthEvents); diff != "" {
					t.Errorf("the auth events of the new session (-want +got):\n%s", diff)
				}
			})

			ctx := context.WithValue(context.Background(), sessioninfo.CtxSessionInfo, tt.session)
			err := f.auth.API().ChangeSessionUserPassword(ctx, httptest.NewRecorder(), userID, &ChangeSessionUserPasswordRequest{OldPassword: "the old one", NewPassword: "the new one"})
			if err != nil {
				t.Fatalf("ChangeSessionUserPassword() error = %v", err)
			}
		})
	}
}

// TestAuth_passwordRule_everyPath pins the one rule for a password's shape on every path
// that sets one: a password shorter than eight characters is refused a person's own
// change, on the route and through the API, a password set for them and one reset, an
// account created with one, the hash the generated user management asks for, and a
// sign-up; nothing is written (the mock refuses a write it was not told of). A person's
// first password is held to it in TestAuth_FirstPassword.
func TestAuth_passwordRule_everyPath(t *testing.T) {
	t.Parallel()

	const short = "seven77"
	refused := func(t *testing.T, err error) {
		t.Helper()
		if !httpio.HasBadRequest(err) || !strings.Contains(httpio.Message(err), "the password is at least 8 characters") {
			t.Errorf("error = %v, want 400 saying the password is at least 8 characters", err)
		}
	}
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
		f.password.ChangeUserPassword().ServeHTTP(rr, passwordChangeRequest(sessionID, map[string]string{"oldPassword": "the old one", "newPassword": short}))
		if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "the password is at least 8 characters") {
			t.Errorf("ChangeUserPassword() = %d %s, want 400 saying the password is at least 8 characters", rr.Code, rr.Body.String())
		}
	})
	t.Run("a person's own change, through the API", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		old(t, f, 1)
		refused(t, f.auth.API().ChangeSessionUserPassword(context.Background(), httptest.NewRecorder(), userID, &ChangeSessionUserPasswordRequest{OldPassword: "the old one", NewPassword: short}))
	})
	t.Run("a password set for a person by a flow of the application's own", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		old(t, f, 1)
		refused(t, f.auth.API().SetSessionUserPassword(context.Background(), userID, short))
	})
	t.Run("a password an operator resets", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		old(t, f, 1)
		refused(t, f.auth.API().ResetSessionUserPassword(context.Background(), userID, short))
	})
	t.Run("an account created with a password", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		password := short
		_, err := f.auth.API().CreateSessionUser(context.Background(), &CreateUserRequest{Username: "pat", Password: &password})
		refused(t, err)
	})
	t.Run("the hash the generated user management asks for, of a temporary password", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		_, err := f.auth.API().HashPassword(short)
		refused(t, err)
	})
	t.Run("a sign-up and the acceptance of an invitation", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		_, err := f.auth.newAccount("pat@lakeside.edu", "pat", short, "")
		refused(t, err)
	})
	t.Run("eight characters are a password on each", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		if _, err := f.auth.API().HashPassword("eight888"); err != nil {
			t.Errorf("HashPassword() of eight characters: error = %v, want a hash", err)
		}
	})
	t.Run("the length is counted in characters: seven that take two bytes each are refused, eight are a password", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		_, err := f.auth.API().HashPassword(strings.Repeat("é", 7))
		refused(t, err)
		if _, err := f.auth.API().HashPassword(strings.Repeat("é", 8)); err != nil {
			t.Errorf("HashPassword() of eight characters of two bytes each: error = %v, want a hash", err)
		}
	})
}
