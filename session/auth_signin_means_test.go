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
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/azure"
	"github.com/impulseframework/impulse/session/google"
	"github.com/impulseframework/impulse/session/internal/workos"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/session/sessionstorage/mock/mock_sessionstorage"
	"github.com/impulseframework/impulse/session/signin"
	gomock "go.uber.org/mock/gomock"
)

// signInMeansAuth is an Auth over a mocked store with a Google registration, three
// Entra registrations of a tenant each and a WorkOS registration beside the password
// method, and the methods list a login page is given: Google under the words "Fleet
// Google", then the password, two of the Entra registrations and WorkOS. The third
// Entra registration is registered and not listed. The store is its own mock: the
// shared fixture's answers every read of a user's identities with none, and these tests
// say what the user is linked to.
func signInMeansAuth(t *testing.T) (*Auth[NoCustomData, NoCustomData, mockTx], *mock_sessionstorage.MockAuthStore[mockTx]) {
	t.Helper()

	a, store := directoriesAuth(t)
	a.Methods(signInMeansList("/api/user/sign-in-means", "Fleet Google", "Lakeside"))

	return a, store
}

// signInMeansList is the methods list of signInMeansAuth, naming the sign-in means
// route at path, Google under the words fleet and the Lakeside Entra registration
// under the words lakeside.
func signInMeansList(path, fleet, lakeside string) *MethodList {
	return &MethodList{
		Methods: []AdvertisedMethod{
			{Name: "google", Label: fleet, Path: "/api/user/google/login"},
			{Name: "password", Label: "Password", Path: "/api/user/password/login"},
			{Name: "lakeside-entra", Label: lakeside, Path: "/api/user/lakeside-entra/login"},
			{Name: "river-entra", Label: "Riverside", Path: "/api/user/river-entra/login"},
			{Name: "workos", Label: "Single sign-on", Path: "/api/user/workos/login"},
		},
		ChangePassword: "/api/user/password/change",
		RemovePassword: "/api/user/password/remove",
		SignInMeans:    path,
	}
}

// directoriesAuth is signInMeansAuth before any methods list is mounted: the
// registrations alone, the Google one taking the Workspace "fleet", the Entra ones the
// tenants tenantLake ("lakeside-entra"), tenantRiver ("river-entra") and tenantOther
// ("unlisted-entra"), and the WorkOS one, which cannot tell one connection from another.
func directoriesAuth(t *testing.T) (*Auth[NoCustomData, NoCustomData, mockTx], *mock_sessionstorage.MockAuthStore[mockTx]) {
	t.Helper()

	a, _, store := directoriesAuthWithPassword(t)

	return a, store
}

// directoriesAuthWithPassword is directoriesAuth with its password method, whose
// handlers a test serves. The store counts no failed attempt and forgets none.
func directoriesAuthWithPassword(t *testing.T) (*Auth[NoCustomData, NoCustomData, mockTx], *PasswordMethod, *mock_sessionstorage.MockAuthStore[mockTx]) {
	t.Helper()

	store := mock_sessionstorage.NewMockAuthStore[mockTx](gomock.NewController(t))
	store.EXPECT().CustomUserDataType().Return(nil).AnyTimes()
	store.EXPECT().SetUsernameIsEmail(gomock.Any()).AnyTimes()
	store.EXPECT().SignInAttempts(gomock.Any(), gomock.Any()).Return(nil, httpio.NewNotFoundMessage("no sign-in attempts recorded")).AnyTimes()
	store.EXPECT().ClearSignInAttempts(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	a, err := NewAuth[NoCustomData, NoCustomData](store, cookieKey, NoSuperAdministrators())
	if err != nil {
		t.Fatalf("NewAuth() error = %v", err)
	}
	password := PasswordSignIn(a)
	registerDirectories(a)

	return a, password, store
}

// registerDirectories registers directoriesAuth's external methods on a.
func registerDirectories(a *Auth[NoCustomData, NoCustomData, mockTx]) {
	google.SignIn(a, "google", resolveAll[google.Identity](), &google.Config{RoleSync: google.DisableRoleSync(), ClientID: "client", ClientSecret: "secret", RedirectURL: "https://app/google/callback", Workspaces: google.Workspace("fleet", "fleet.example")})
	entra := func(name, tenant string) {
		azure.SignIn(a, name, resolveAll[azure.Identity](), &azure.Config{RoleSync: signin.DisableRoleSync(), Authority: testAuthority, Tenants: azure.Tenant(tenant), ClientID: "client", ClientSecret: "secret", RedirectURL: "https://app/" + name + "/callback"})
	}
	entra("lakeside-entra", tenantLake)
	entra("river-entra", tenantRiver)
	entra("unlisted-entra", tenantOther)
	workos.SignIn(a, "workos", resolveAll[workos.Identity](), &workos.Config{APIKey: "sk", ClientID: "client", RedirectURL: "https://app/sso/callback"})
}

// TestAuth_signsInThrough pins the one judgment of whether a link counts as a means of
// sign-in: the registrations the login page offers of the link's method that still take
// its connection. A link of a method the page offers nothing of signs nobody in, and so
// does one whose connection no offered registration takes; two registrations of one
// method are told apart by the connection, and a method that cannot tell connections
// apart answers for every link of it.
func TestAuth_signsInThrough(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		method     sessioninfo.AuthMethod
		connection string
		want       []string
		// wantUnlisted is the answer of an Auth whose page was wired without a methods
		// list, which offers every registration.
		wantUnlisted []string
	}{
		{name: "a Workspace the Google registration names", method: sessioninfo.MethodGoogle, connection: "fleet", want: []string{"google"}, wantUnlisted: []string{"google"}},
		{name: "a Workspace the Google registration does not name", method: sessioninfo.MethodGoogle, connection: "other"},
		{name: "a Google account outside a Workspace, which the registration does not admit", method: sessioninfo.MethodGoogle, connection: google.AnyAccountConnection},
		{name: "the tenant of one of two Entra registrations names that one", method: sessioninfo.MethodAzure, connection: tenantLake, want: []string{"lakeside-entra"}, wantUnlisted: []string{"lakeside-entra"}},
		{name: "a tenant id is matched without regard to case", method: sessioninfo.MethodAzure, connection: strings.ToUpper(tenantRiver), want: []string{"river-entra"}, wantUnlisted: []string{"river-entra"}},
		{name: "the tenant of a registration the page does not list signs nobody in", method: sessioninfo.MethodAzure, connection: tenantOther, wantUnlisted: []string{"unlisted-entra"}},
		{name: "a tenant no Entra registration takes", method: sessioninfo.MethodAzure, connection: "44444444-4444-4444-4444-444444444444"},
		{name: "a method that cannot tell connections apart answers for every link of it", method: sessioninfo.MethodWorkOS, connection: "conn_anything", want: []string{"workos"}, wantUnlisted: []string{"workos"}},
		{name: "a method nothing is registered of", method: sessioninfo.MethodOIDC, connection: "okta"},
		{name: "the password is no link", method: sessioninfo.MethodPassword, connection: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			listed, _ := signInMeansAuth(t)
			if diff := cmp.Diff(tt.want, listed.API().SignsInThrough(tt.method, tt.connection), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("SignsInThrough() on the listed page mismatch (-want +got):\n%s", diff)
			}
			unlisted, _ := directoriesAuth(t)
			if diff := cmp.Diff(tt.wantUnlisted, unlisted.API().SignsInThrough(tt.method, tt.connection), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("SignsInThrough() with no methods list mismatch (-want +got):\n%s", diff)
			}
		})
	}
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
	riverside := LinkedDirectory{Name: "river-entra", Label: "Riverside"}
	sso := LinkedDirectory{Name: "workos", Label: "Single sign-on"}

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
			links:    []*sessionstorage.SessionIdentity{link(sessioninfo.MethodOIDC, "okta")},
			want:     &SignInMeans{Password: true, Directories: []LinkedDirectory{}},
		},
		{
			name:     "of two registrations of one method, a link names the one that takes its connection",
			password: true,
			links:    []*sessionstorage.SessionIdentity{link(sessioninfo.MethodAzure, tenantRiver)},
			want:     &SignInMeans{Password: true, Directories: []LinkedDirectory{riverside}},
		},
		{
			name:     "a link whose connection no listed registration takes names nothing: a tenant listed nowhere, the tenant of the unlisted registration, a Workspace not named",
			password: true,
			links: []*sessionstorage.SessionIdentity{
				link(sessioninfo.MethodAzure, "44444444-4444-4444-4444-444444444444"), link(sessioninfo.MethodAzure, tenantOther), link(sessioninfo.MethodGoogle, "other"),
			},
			want: &SignInMeans{Password: true, Directories: []LinkedDirectory{}},
		},
		{
			name:  "a method that cannot tell connections apart answers its registration for any link of it",
			links: []*sessionstorage.SessionIdentity{link(sessioninfo.MethodWorkOS, "conn_anything")},
			want:  &SignInMeans{Directories: []LinkedDirectory{sso}},
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

// TestAuth_SignInMeans_labelsOfTheListServedBeside pins the words the read answers in
// when one Auth is served on two outlets, each with a methods list of its own: the read
// served at the path a list names answers in that list's labels, whichever list was
// mounted last, a method that list does not name keeps the label it was first listed
// under, and a read with no route of its own (AuthAPI.SignInMeans), or one served at a
// path no list names, answers each method under the label it was first listed under.
func TestAuth_SignInMeans_labelsOfTheListServedBeside(t *testing.T) {
	t.Parallel()

	userID := impulse.Must(impulse.NewUUID())
	sessionID := impulse.Must(impulse.NewUUID())
	a, store := directoriesAuth(t)
	console := a.Methods(signInMeansList("/console/api/user/sign-in-means", "Fleet Google", "Lakeside"))
	// The portal's page offers Google alone, in its own words.
	portal := a.Methods(&MethodList{
		Methods:     []AdvertisedMethod{{Name: "google", Label: "Google for members", Path: "/portal/api/user/google/login"}},
		SignInMeans: "/portal/api/user/sign-in-means",
	})
	if console == nil || portal == nil {
		t.Fatal("Methods() = nil, want each list's handler")
	}
	store.EXPECT().Session(gomock.Any(), sessionID).
		Return(&sessioninfo.SessionData{SessionInfo: &sessioninfo.SessionInfo{ID: sessionID, Username: "pat", UpdatedAt: time.Now()}, UserID: impulse.NullUUIDFromUUID(userID)}, nil).AnyTimes()
	store.EXPECT().User(gomock.Any(), userID).Return(&sessionstorage.SessionUser{ID: userID, Username: "pat"}, nil).AnyTimes()
	store.EXPECT().IdentitiesByUser(gomock.Any(), userID).Return([]*sessionstorage.SessionIdentity{
		{ID: impulse.Must(impulse.NewUUID()), UserID: userID, Method: sessioninfo.MethodGoogle, Connection: "fleet", Subject: "sub"},
		{ID: impulse.Must(impulse.NewUUID()), UserID: userID, Method: sessioninfo.MethodAzure, Connection: tenantLake, Subject: "oid"},
	}, nil).AnyTimes()
	servedAt := func(t *testing.T, path string) []LinkedDirectory {
		t.Helper()

		ctx := context.WithValue(context.Background(), sessioninfo.CTXSessionID, sessionID)
		rr := httptest.NewRecorder()
		a.SignInMeans().ServeHTTP(rr, httptest.NewRequestWithContext(ctx, http.MethodGet, path, http.NoBody))
		if rr.Code != http.StatusOK {
			t.Fatalf("SignInMeans() at %s = %d %s, want 200", path, rr.Code, rr.Body.String())
		}
		var got SignInMeans
		if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}

		return got.Directories
	}
	firstListed := []LinkedDirectory{{Name: "google", Label: "Fleet Google"}, {Name: "lakeside-entra", Label: "Lakeside"}}

	tests := []struct {
		name string
		path string
		want []LinkedDirectory
	}{
		{name: "the console's read answers in the console's words, though the portal's list was mounted after it", path: "/console/api/user/sign-in-means", want: firstListed},
		{
			name: "the portal's read answers in the portal's words, and a method its list does not name keeps its first label",
			path: "/portal/api/user/sign-in-means",
			want: []LinkedDirectory{{Name: "google", Label: "Google for members"}, {Name: "lakeside-entra", Label: "Lakeside"}},
		},
		{name: "a read served at a path no list names answers each method under its first label", path: "/elsewhere/sign-in-means", want: firstListed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.want, servedAt(t, tt.path)); diff != "" {
				t.Errorf("SignInMeans() directories mismatch (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("the read with no route of its own answers each method under its first label", func(t *testing.T) {
		t.Parallel()

		got, err := a.API().SignInMeans(context.Background(), userID)
		if err != nil {
			t.Fatalf("SignInMeans() error = %v", err)
		}
		if diff := cmp.Diff(firstListed, got.Directories); diff != "" {
			t.Errorf("AuthAPI.SignInMeans() directories mismatch (-want +got):\n%s", diff)
		}
	})

	// Two more lists name one path between them: the read served there cannot tell
	// which it stands beside, and answers neither's words.
	for _, words := range []string{"Google on the first host", "Google on the second host"} {
		a.Methods(&MethodList{
			Methods:     []AdvertisedMethod{{Name: "google", Label: words, Path: "/shared/api/user/google/login"}},
			SignInMeans: "/shared/api/user/sign-in-means",
		})
	}
	t.Run("a read served at a path two lists name answers each method under its first label", func(t *testing.T) {
		t.Parallel()

		if diff := cmp.Diff(firstListed, servedAt(t, "/shared/api/user/sign-in-means")); diff != "" {
			t.Errorf("SignInMeans() directories mismatch (-want +got):\n%s", diff)
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
