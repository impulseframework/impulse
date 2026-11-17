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
	"github.com/impulseframework/impulse/session/azure"
	"github.com/impulseframework/impulse/session/google"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/session/sessionstorage/mock/mock_sessionstorage"
	"github.com/impulseframework/impulse/session/signin"
	gomock "go.uber.org/mock/gomock"
)

// signInMeansAuth is an Auth over a mocked store with a Google registration and two
// Entra registrations beside the password method, and the methods list a login page is
// given: Google under the words "Fleet Google", then the password, then one of the
// Entra registrations. The second Entra registration is registered and not listed. The
// store is its own mock: the shared fixture's answers every read of a user's identities
// with none, and these tests say what the user is linked to.
func signInMeansAuth(t *testing.T) (*Auth[NoCustomData, NoCustomData, mockTx], *mock_sessionstorage.MockAuthStore[mockTx]) {
	t.Helper()

	store := mock_sessionstorage.NewMockAuthStore[mockTx](gomock.NewController(t))
	store.EXPECT().CustomUserDataType().Return(nil).AnyTimes()
	store.EXPECT().SetUsernameIsEmail(gomock.Any()).AnyTimes()
	a, err := NewAuth[NoCustomData, NoCustomData](store, cookieKey, NoSuperAdministrators())
	if err != nil {
		t.Fatalf("NewAuth() error = %v", err)
	}
	PasswordSignIn(a)
	google.SignIn(a, "google", resolveAll[google.Identity](), &google.Config{RoleSync: google.DisableRoleSync(), ClientID: "client", ClientSecret: "secret", RedirectURL: "https://app/google/callback", Workspaces: google.Workspace("fleet", "fleet.example")})
	entra := func(name string) {
		azure.SignIn(a, name, resolveAll[azure.Identity](), &azure.Config{RoleSync: signin.DisableRoleSync(), Authority: testAuthority, Tenants: azure.Tenant(tenantLake), ClientID: "client", ClientSecret: "secret", RedirectURL: "https://app/" + name + "/callback"})
	}
	entra("lakeside-entra")
	entra("unlisted-entra")
	a.Methods(&MethodList{
		Methods: []AdvertisedMethod{
			{Name: "google", Label: "Fleet Google", Path: "/api/user/google/login"},
			{Name: "password", Label: "Password", Path: "/api/user/password/login"},
			{Name: "lakeside-entra", Label: "Lakeside", Path: "/api/user/lakeside-entra/login"},
		},
		ChangePassword: "/api/user/password/change",
		RemovePassword: "/api/user/password/remove",
		SignInMeans:    "/api/user/sign-in-means",
	})

	return a, store
}

// TestAuthAPI_SignInMeans pins what a user is said to sign in with: whether they hold a
// password, and the listed sign-in methods their identity links sign in through, by the
// label each button carries and in the page's order.
func TestAuthAPI_SignInMeans(t *testing.T) {
	t.Parallel()

	userID := impulse.Must(impulse.NewUUID())
	link := func(method sessioninfo.AuthMethod, connection string) *sessionstorage.SessionIdentity {
		return &sessionstorage.SessionIdentity{ID: impulse.Must(impulse.NewUUID()), UserID: userID, Method: method, Connection: connection, Subject: "sub"}
	}
	fleetGoogle := LinkedDirectory{Name: "google", Label: "Fleet Google"}
	lakeside := LinkedDirectory{Name: "lakeside-entra", Label: "Lakeside"}

	tests := []struct {
		name     string
		password bool
		links    []*sessionstorage.SessionIdentity
		want     *SignInMeans
	}{
		{
			name:     "a person with a password and a link",
			password: true,
			links:    []*sessionstorage.SessionIdentity{link(sessioninfo.MethodGoogle, "fleet")},
			want:     &SignInMeans{Password: true, Directories: []LinkedDirectory{fleetGoogle}},
		},
		{
			name:     "a person with a password alone",
			password: true,
			want:     &SignInMeans{Password: true, Directories: []LinkedDirectory{}},
		},
		{
			name:  "a person with a link alone",
			links: []*sessionstorage.SessionIdentity{link(sessioninfo.MethodGoogle, "fleet")},
			want:  &SignInMeans{Directories: []LinkedDirectory{fleetGoogle}},
		},
		{
			name:     "links of two methods answer in the page's order, each method once however many links carry it, and the registration the page does not list is left out",
			password: true,
			links: []*sessionstorage.SessionIdentity{
				link(sessioninfo.MethodAzure, tenantLake), link(sessioninfo.MethodGoogle, "fleet"), link(sessioninfo.MethodGoogle, "other"),
			},
			want: &SignInMeans{Password: true, Directories: []LinkedDirectory{fleetGoogle, lakeside}},
		},
		{
			name:     "a link of a method the page offers no button for names nothing",
			password: true,
			links:    []*sessionstorage.SessionIdentity{link(sessioninfo.MethodWorkOS, "conn")},
			want:     &SignInMeans{Password: true, Directories: []LinkedDirectory{}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a, store := signInMeansAuth(t)
			user := &sessionstorage.SessionUser{ID: userID, Username: "pat"}
			if tt.password {
				user.PasswordHash = hashed(t, "kept")
			}
			store.EXPECT().User(gomock.Any(), userID).Return(user, nil)
			store.EXPECT().IdentitiesByUser(gomock.Any(), userID).Return(tt.links, nil)

			got, err := a.API().SignInMeans(context.Background(), userID)
			if err != nil {
				t.Fatalf("SignInMeans() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("SignInMeans() mismatch (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("a user that does not exist is not found", func(t *testing.T) {
		t.Parallel()
		a, store := signInMeansAuth(t)
		store.EXPECT().User(gomock.Any(), userID).Return(nil, httpio.NewNotFoundMessage("no user"))

		if _, err := a.API().SignInMeans(context.Background(), userID); !httpio.HasNotFound(err) {
			t.Errorf("SignInMeans() error = %v, want NotFound", err)
		}
	})
}

// TestAuth_SignInMeans pins the handler a person's account page reads from: it answers
// for the session's user and nobody else, validates the session itself, and writes
// whether a password is held and the labels of the linked directory sign-ins, never the
// hash.
func TestAuth_SignInMeans(t *testing.T) {
	t.Parallel()

	userID := impulse.Must(impulse.NewUUID())
	sessionID := impulse.Must(impulse.NewUUID())
	request := func() *http.Request {
		ctx := context.WithValue(context.Background(), sessioninfo.CTXSessionID, sessionID)

		return httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/user/sign-in-means", http.NoBody)
	}
	session := func() *sessioninfo.SessionData {
		return &sessioninfo.SessionData{SessionInfo: &sessioninfo.SessionInfo{ID: sessionID, Username: "pat", UpdatedAt: time.Now()}, UserID: impulse.NullUUIDFromUUID(userID)}
	}

	t.Run("the session's user is answered, with nothing of the hash", func(t *testing.T) {
		t.Parallel()
		a, store := signInMeansAuth(t)
		kept := hashed(t, "kept")
		store.EXPECT().Session(gomock.Any(), sessionID).Return(session(), nil)
		// The session's validation and the read each read the user.
		store.EXPECT().User(gomock.Any(), userID).Return(&sessionstorage.SessionUser{ID: userID, Username: "pat", PasswordHash: kept}, nil).Times(2)
		store.EXPECT().IdentitiesByUser(gomock.Any(), userID).
			Return([]*sessionstorage.SessionIdentity{{ID: impulse.Must(impulse.NewUUID()), UserID: userID, Method: sessioninfo.MethodGoogle, Connection: "fleet", Subject: "sub"}}, nil)

		rr := httptest.NewRecorder()
		a.SignInMeans().ServeHTTP(rr, request())
		if rr.Code != http.StatusOK {
			t.Fatalf("SignInMeans() = %d %s, want 200", rr.Code, rr.Body.String())
		}
		var got map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}
		want := map[string]any{"password": true, "directories": []any{map[string]any{"name": "google", "label": "Fleet Google"}}}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("SignInMeans() answer mismatch (-want +got):\n%s", diff)
		}
		text, err := kept.MarshalText()
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(rr.Body.String(), string(text)) {
			t.Error("SignInMeans() answered the password's hash, want whether one is held and nothing more")
		}
	})

	t.Run("a person with no link is answered an empty list, not a missing one", func(t *testing.T) {
		t.Parallel()
		a, store := signInMeansAuth(t)
		store.EXPECT().Session(gomock.Any(), sessionID).Return(session(), nil)
		store.EXPECT().User(gomock.Any(), userID).Return(&sessionstorage.SessionUser{ID: userID, Username: "pat"}, nil).Times(2)
		store.EXPECT().IdentitiesByUser(gomock.Any(), userID).Return(nil, nil)

		rr := httptest.NewRecorder()
		a.SignInMeans().ServeHTTP(rr, request())
		if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != `{"password":false,"directories":[]}` {
			t.Errorf("SignInMeans() = %d %s, want 200 with no password and an empty list", rr.Code, rr.Body.String())
		}
	})

	t.Run("a request without a session is answered nothing", func(t *testing.T) {
		t.Parallel()
		a, store := signInMeansAuth(t)
		// The mock refuses a call it was not told of: no user and no identity is read.
		store.EXPECT().Session(gomock.Any(), sessionID).Return(nil, httpio.NewNotFoundMessage("no session"))

		rr := httptest.NewRecorder()
		a.SignInMeans().ServeHTTP(rr, request())
		if rr.Code != http.StatusUnauthorized || strings.Contains(rr.Body.String(), "directories") {
			t.Errorf("SignInMeans() = %d %s, want 401 and nothing of anyone's sign-in", rr.Code, rr.Body.String())
		}
	})
}

// TestAuth_Methods_signInMeans pins the route the account page is told: the path of the
// SignInMeans handler where the list names it, nothing where it does not, and a path
// that is not one of the application's refused where the routes are wired.
func TestAuth_Methods_signInMeans(t *testing.T) {
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
		got := answered(t, &MethodList{Methods: password, ChangePassword: "/api/user/password/change", SignInMeans: "/api/user/sign-in-means"})
		if got["signInMeans"] != "/api/user/sign-in-means" {
			t.Errorf("Methods() answered signInMeans = %v, want the route", got["signInMeans"])
		}
	})

	t.Run("a list that names no route answers none", func(t *testing.T) {
		t.Parallel()
		if got := answered(t, &MethodList{Methods: password, ChangePassword: "/api/user/password/change"}); got["signInMeans"] != nil {
			t.Errorf("Methods() answered signInMeans = %v, want none", got["signInMeans"])
		}
	})

	t.Run("a route off-site is refused where the routes are wired", func(t *testing.T) {
		t.Parallel()
		a := registerThree(t)

		recovered := registrationPanic(func() {
			a.Methods(&MethodList{Methods: password, ChangePassword: "/password", SignInMeans: "https://evil.example/means"})
		})
		if msg, _ := recovered.(string); !strings.Contains(msg, "sign-in means route") {
			t.Errorf("Methods() panic = %v, want it to name the sign-in means route", recovered)
		}
	})
}
