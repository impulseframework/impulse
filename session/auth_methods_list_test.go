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
	"github.com/impulseframework/impulse/session/azure"
	"github.com/impulseframework/impulse/session/google"
	"github.com/impulseframework/impulse/session/mail"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/session/signin"
	gomock "go.uber.org/mock/gomock"
)

// registerThree registers the password method and a Google and an Azure registration
// on a bare Auth.
func registerThree(t *testing.T) *Auth[NoCustomData, NoCustomData, mockTx] {
	t.Helper()

	a := newBareAuth(t, gomock.NewController(t))
	PasswordSignIn(a)
	google.SignIn(a, "google", resolveAll[google.Identity](), &google.Config{RoleSync: google.DisableRoleSync(), ClientID: "client", ClientSecret: "secret", RedirectURL: "https://app/google/callback", Workspaces: google.Workspace("example", "example.com")})
	azure.SignIn(a, "lakeside-entra", resolveAll[azure.Identity](), &azure.Config{RoleSync: signin.DisableRoleSync(), Authority: testAuthority, Tenants: azure.Tenant(tenantLake), ClientID: "client", ClientSecret: "secret", RedirectURL: "https://app/entra/callback"})

	return a
}

// registerTwoDirectories registers a Google and an Azure registration and no password
// method on a bare Auth: the Auth of an application where nobody signs in with a
// password, which asks nobody for one.
func registerTwoDirectories(t *testing.T) *Auth[NoCustomData, NoCustomData, mockTx] {
	t.Helper()

	a := newBareAuth(t, gomock.NewController(t))
	google.SignIn(a, "google", resolveAll[google.Identity](), &google.Config{RoleSync: google.DisableRoleSync(), ClientID: "client", ClientSecret: "secret", RedirectURL: "https://app/google/callback", Workspaces: google.Workspace("example", "example.com")})
	azure.SignIn(a, "lakeside-entra", resolveAll[azure.Identity](), &azure.Config{RoleSync: signin.DisableRoleSync(), Authority: testAuthority, Tenants: azure.Tenant(tenantLake), ClientID: "client", ClientSecret: "secret", RedirectURL: "https://app/entra/callback"})

	return a
}

// listAuth is the Auth a list is checked against: the one with the password method
// beside the two directories, or, for noPassword, the one with the directories alone.
func listAuth(t *testing.T, noPassword bool) *Auth[NoCustomData, NoCustomData, mockTx] {
	t.Helper()

	if noPassword {
		return registerTwoDirectories(t)
	}

	return registerThree(t)
}

func TestAuth_Methods(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		noPassword bool
		list       *MethodList
		want       map[string]any
	}{
		{
			name: "every listed method in the list's order, the password one a form, the external ones redirects, with the pending routes",
			list: &MethodList{
				Methods: []AdvertisedMethod{
					{Name: "google", Label: "Google", Path: "/api/user/google/login"},
					{Name: "password", Label: "Password", Path: "/api/user/password/login"},
					{Name: "lakeside-entra", Label: "Lakeside", Path: "/api/user/lakeside-entra/login"},
				},
				Pending:          PendingRoutes{Status: "/api/user/pending", Confirm: "/api/user/pending/confirm", Cancel: "/api/user/pending/cancel"},
				ChangePassword:   "/api/user/password",
				PasswordStrength: "/api/user/password/strength",
				ForgetBrowser:    "/api/user/browser/forget",
				ChangeName:       "/api/user/name",
				ChangeLook:       "/api/user/look",
			},
			want: map[string]any{
				"methods": []any{
					map[string]any{"name": "google", "method": "google", "kind": "redirect", "label": "Google", "path": "/api/user/google/login"},
					map[string]any{"name": "password", "method": "password", "kind": "password", "label": "Password", "path": "/api/user/password/login"},
					map[string]any{"name": "lakeside-entra", "method": "azure", "kind": "redirect", "label": "Lakeside", "path": "/api/user/lakeside-entra/login"},
				},
				"pending":          map[string]any{"status": "/api/user/pending", "confirm": "/api/user/pending/confirm", "cancel": "/api/user/pending/cancel"},
				"changePassword":   "/api/user/password",
				"passwordStrength": "/api/user/password/strength",
				"forgetBrowser":    "/api/user/browser/forget",
				"changeName":       "/api/user/name",
				"changeLook":       "/api/user/look",
			},
		},
		{
			name: "a registered method that is not listed is not offered, and no pending routes means no pending block",
			list: &MethodList{Methods: []AdvertisedMethod{{Name: "password", Label: "Sign in", Path: "/login"}}, ChangePassword: "/password", PasswordStrength: "/password/strength"},
			want: map[string]any{
				"methods":          []any{map[string]any{"name": "password", "method": "password", "kind": "password", "label": "Sign in", "path": "/login"}},
				"changePassword":   "/password",
				"passwordStrength": "/password/strength",
			},
		},
		{
			name: "external methods alone are offered without a password change route",
			list: &MethodList{Methods: []AdvertisedMethod{{Name: "google", Label: "Google", Path: "/api/user/google/login"}}},
			want: map[string]any{
				"methods": []any{map[string]any{"name": "google", "method": "google", "kind": "redirect", "label": "Google", "path": "/api/user/google/login"}},
			},
		},
		{
			name:       "a list of an Auth that registers no password method may leave the confirm path out, and the page is then told of none",
			noPassword: true,
			list: &MethodList{
				Methods: []AdvertisedMethod{{Name: "google", Label: "Google", Path: "/api/user/google/login"}},
				Pending: PendingRoutes{
					Status: "/api/user/pending", Cancel: "/api/user/pending/cancel",
					SendCode: "/api/user/pending/code", VerifyCode: "/api/user/pending/code/verify", EnrollEmail: "/api/user/pending/email",
				},
			},
			want: map[string]any{
				"methods": []any{map[string]any{"name": "google", "method": "google", "kind": "redirect", "label": "Google", "path": "/api/user/google/login"}},
				"pending": map[string]any{
					"status": "/api/user/pending", "cancel": "/api/user/pending/cancel",
					"sendCode": "/api/user/pending/code", "verifyCode": "/api/user/pending/code/verify", "enrollEmail": "/api/user/pending/email",
				},
			},
		},
		{
			name: "a list that does not offer the password method names the confirm path where the Auth registers the method, and the page is told it: " +
				"a sign-in through the directory may wait for a password",
			list: &MethodList{
				Methods: []AdvertisedMethod{{Name: "google", Label: "Google", Path: "/api/user/google/login"}},
				Pending: PendingRoutes{Status: "/api/user/pending", Confirm: "/api/user/pending/confirm", Cancel: "/api/user/pending/cancel"},
			},
			want: map[string]any{
				"methods": []any{map[string]any{"name": "google", "method": "google", "kind": "redirect", "label": "Google", "path": "/api/user/google/login"}},
				"pending": map[string]any{"status": "/api/user/pending", "confirm": "/api/user/pending/confirm", "cancel": "/api/user/pending/cancel"},
			},
		},
		{
			name:       "a list of an Auth that registers no password method that names the confirm path is told it: an account rule of the application's own may ask for a confirmation",
			noPassword: true,
			list: &MethodList{
				Methods: []AdvertisedMethod{{Name: "google", Label: "Google", Path: "/api/user/google/login"}},
				Pending: PendingRoutes{Status: "/api/user/pending", Confirm: "/api/user/pending/confirm", Cancel: "/api/user/pending/cancel"},
			},
			want: map[string]any{
				"methods": []any{map[string]any{"name": "google", "method": "google", "kind": "redirect", "label": "Google", "path": "/api/user/google/login"}},
				"pending": map[string]any{"status": "/api/user/pending", "confirm": "/api/user/pending/confirm", "cancel": "/api/user/pending/cancel"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := listAuth(t, tt.noPassword)

			rr := httptest.NewRecorder()
			a.Methods(tt.list).ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/user/methods", http.NoBody))
			if rr.Code != http.StatusOK {
				t.Fatalf("Methods() status = %d, want 200: %s", rr.Code, rr.Body.String())
			}
			var got map[string]any
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Methods() answer mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestAuth_MethodsRefused(t *testing.T) {
	t.Parallel()

	pending := PendingRoutes{Status: "/api/user/pending", Confirm: "/api/user/pending/confirm", Cancel: "/api/user/pending/cancel"}
	tests := []struct {
		name       string
		noPassword bool
		list       *MethodList
		want       string
	}{
		{name: "no list", list: nil, want: "given no sign-in method"},
		{name: "an empty list", list: &MethodList{}, want: "given no sign-in method"},
		{name: "a method without a name", list: &MethodList{Methods: []AdvertisedMethod{{Label: "x", Path: "/login"}}}, want: "without a name"},
		{name: "a method that is not registered", list: &MethodList{Methods: []AdvertisedMethod{{Name: "workos", Label: "SSO", Path: "/sso"}}}, want: `"workos", which is not registered on this Auth`},
		{name: "a method listed twice", list: &MethodList{Methods: []AdvertisedMethod{{Name: "password", Label: "a", Path: "/a"}, {Name: "password", Label: "b", Path: "/b"}}, ChangePassword: "/password", PasswordStrength: "/password/strength"}, want: `"password" twice`},
		{name: "a method without a label", list: &MethodList{Methods: []AdvertisedMethod{{Name: "password", Path: "/login"}}}, want: "without a label"},
		{name: "a relative path", list: &MethodList{Methods: []AdvertisedMethod{{Name: "password", Label: "x", Path: "api/user/login"}}}, want: "not a canonical path"},
		{name: "a scheme-relative path", list: &MethodList{Methods: []AdvertisedMethod{{Name: "google", Label: "x", Path: "//evil.example/login"}}}, want: "not a canonical path"},
		{name: "a dot segment", list: &MethodList{Methods: []AdvertisedMethod{{Name: "google", Label: "x", Path: "/api/../login"}}}, want: "not a canonical path"},
		{name: "the root", list: &MethodList{Methods: []AdvertisedMethod{{Name: "google", Label: "x", Path: "/"}}}, want: "not a canonical path"},
		{name: "some of the pending routes", list: &MethodList{Methods: []AdvertisedMethod{{Name: "password", Label: "x", Path: "/login"}}, Pending: PendingRoutes{Status: "/api/user/pending"}, ChangePassword: "/password", PasswordStrength: "/password/strength"}, want: "name the status, confirm and cancel paths together"},
		{name: "a pending route off-site", list: &MethodList{Methods: []AdvertisedMethod{{Name: "password", Label: "x", Path: "/login"}}, Pending: PendingRoutes{Status: pending.Status, Confirm: "https://evil.example/confirm", Cancel: pending.Cancel}, ChangePassword: "/password", PasswordStrength: "/password/strength"}, want: "name the status, confirm and cancel paths together"},
		{name: "the password method without the confirm path", list: &MethodList{Methods: []AdvertisedMethod{{Name: "password", Label: "x", Path: "/login"}}, Pending: PendingRoutes{Status: pending.Status, Cancel: pending.Cancel}, ChangePassword: "/password", PasswordStrength: "/password/strength"}, want: "name the status, confirm and cancel paths together"},
		{name: "the password method beside a directory without the confirm path", list: &MethodList{Methods: []AdvertisedMethod{{Name: "google", Label: "x", Path: "/google"}, {Name: "password", Label: "x", Path: "/login"}}, Pending: PendingRoutes{Status: pending.Status, Cancel: pending.Cancel}, ChangePassword: "/password", PasswordStrength: "/password/strength"}, want: "name the status, confirm and cancel paths together"},
		{name: "a list that does not offer the password method without the confirm path, on an Auth that registers the method", list: &MethodList{Methods: []AdvertisedMethod{{Name: "google", Label: "x", Path: "/google"}}, Pending: PendingRoutes{Status: pending.Status, Cancel: pending.Cancel}}, want: "this Auth registers the password method"},
		{name: "no password method and some of the pending routes", noPassword: true, list: &MethodList{Methods: []AdvertisedMethod{{Name: "google", Label: "x", Path: "/login"}}, Pending: PendingRoutes{Status: pending.Status}}, want: "name the status and cancel paths together"},
		{name: "no password method and a cancel path off-site", noPassword: true, list: &MethodList{Methods: []AdvertisedMethod{{Name: "google", Label: "x", Path: "/login"}}, Pending: PendingRoutes{Status: pending.Status, Cancel: "https://evil.example/cancel"}}, want: "this Auth registers no password method, so the list may leave the confirm path out"},
		{name: "no password method and a confirm path off-site", noPassword: true, list: &MethodList{Methods: []AdvertisedMethod{{Name: "google", Label: "x", Path: "/login"}}, Pending: PendingRoutes{Status: pending.Status, Confirm: "https://evil.example/confirm", Cancel: pending.Cancel}}, want: "name the status, confirm and cancel paths together"},
		{name: "no password method and the mailed code's routes alone", noPassword: true, list: &MethodList{Methods: []AdvertisedMethod{{Name: "google", Label: "x", Path: "/login"}}, Pending: PendingRoutes{SendCode: "/api/user/pending/code", VerifyCode: "/api/user/pending/code/verify", EnrollEmail: "/api/user/pending/email"}}, want: "name the status and cancel paths together"},
		{name: "the password method without the password change route", list: &MethodList{Methods: []AdvertisedMethod{{Name: "password", Label: "x", Path: "/login"}}}, want: "lists the password method with the password change route \"\""},
		{name: "a password change route off-site", list: &MethodList{Methods: []AdvertisedMethod{{Name: "password", Label: "x", Path: "/login"}}, ChangePassword: "https://evil.example/password", PasswordStrength: "/password/strength"}, want: "lists the password method with the password change route"},
		{name: "the password method without the password strength route", list: &MethodList{Methods: []AdvertisedMethod{{Name: "password", Label: "x", Path: "/login"}}, ChangePassword: "/password"}, want: "lists the password method with the password strength route \"\": give it the path its Strength handler is mounted at"},
		{name: "a password strength route off-site", list: &MethodList{Methods: []AdvertisedMethod{{Name: "password", Label: "x", Path: "/login"}}, ChangePassword: "/password", PasswordStrength: "https://evil.example/strength"}, want: "lists the password method with the password strength route \"https://evil.example/strength\""},
		{name: "a password strength route without the password method", list: &MethodList{Methods: []AdvertisedMethod{{Name: "google", Label: "x", Path: "/login"}}, PasswordStrength: "/password/strength"}, want: "is given the password strength route \"/password/strength\" but lists no password method"},
		{name: "a forget-this-browser route off-site", list: &MethodList{Methods: []AdvertisedMethod{{Name: "password", Label: "x", Path: "/login"}}, ChangePassword: "/password", PasswordStrength: "/password/strength", ForgetBrowser: "https://evil.example/forget"}, want: "forget-this-browser route"},
		{name: "a change-your-name route off-site", list: &MethodList{Methods: []AdvertisedMethod{{Name: "password", Label: "x", Path: "/login"}}, ChangePassword: "/password", PasswordStrength: "/password/strength", ChangeName: "https://evil.example/name"}, want: "change-your-name route"},
		{name: "a keep-your-look route off-site", list: &MethodList{Methods: []AdvertisedMethod{{Name: "password", Label: "x", Path: "/login"}}, ChangePassword: "/password", PasswordStrength: "/password/strength", ChangeLook: "https://evil.example/look"}, want: "keep-your-look route"},
		{name: "a password change route without the password method", list: &MethodList{Methods: []AdvertisedMethod{{Name: "google", Label: "x", Path: "/login"}}, ChangePassword: "/password"}, want: "lists no password method"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := listAuth(t, tt.noPassword)

			recovered := registrationPanic(func() { a.Methods(tt.list) })
			if recovered == nil {
				t.Fatal("Methods() did not panic")
			}
			if msg, _ := recovered.(string); !strings.Contains(msg, tt.want) {
				t.Errorf("Methods() panic = %v, want it to contain %q", recovered, tt.want)
			}
		})
	}

	t.Run("a valid list leaves registration open until the first request", func(t *testing.T) {
		t.Parallel()
		a := registerThree(t)

		h := a.Methods(&MethodList{Methods: []AdvertisedMethod{{Name: "password", Label: "Password", Path: "/login"}}, Pending: pending, ChangePassword: "/password", PasswordStrength: "/password/strength"})
		if a.methods.inUse.Load() {
			t.Fatal("Methods() closed registration at wiring")
		}
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/methods", http.NoBody))
		if !a.methods.inUse.Load() {
			t.Error("the first request through Methods() did not close registration")
		}
	})
}

// invitationList is a list naming the password method and the invitation routes.
var invitationList = &MethodList{
	Methods:          []AdvertisedMethod{{Name: "password", Label: "Password", Path: "/api/user/password/login"}},
	ChangePassword:   "/api/user/password",
	PasswordStrength: "/api/user/password/strength",
	Invitation:       InvitationRoutes{Open: "/api/user/invitation", Accept: "/api/user/invitation/accept", SignUp: "/api/user/invitation/sign-up"},
}

// openedInvitation invites pat into lakeside and opens the invitation in the browser,
// returning the cookies the browser then carries.
func openedInvitation(t *testing.T, f *authFixture, links *mailLinks) []*http.Cookie {
	t.Helper()

	recorder, ok := f.auth.settings.mail.sender.(*mail.Recorder)
	if !ok {
		t.Fatal("the fixture's mail is not a recorder")
	}
	if _, err := f.auth.API().Invite(context.Background(), &InvitationRequest{Email: "pat@lakeside.edu", TenantID: "lakeside", RoleNames: []string{"Teacher"}}); err != nil {
		t.Fatalf("Invite() error = %v", err)
	}

	return invitationCookies(t, f, links, tokenIn(t, recorder.Messages()[0].Text, "invitation"))
}

// invitationBlock serves the methods listing to a browser carrying cookies, through
// StartSession as the application routes it, and returns the answer's invitation block.
func invitationBlock(t *testing.T, f *authFixture, list *MethodList, cookies []*http.Cookie) map[string]any {
	t.Helper()

	rr := f.serve(f.auth.Methods(list), http.MethodGet, "/api/user/methods", nil, cookies)
	if rr.Code != http.StatusOK {
		t.Fatalf("Methods() status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	block, _ := decoded(t, rr)["invitation"].(map[string]any)

	return block
}

func TestAuth_MethodsStandingInvitation(t *testing.T) {
	t.Parallel()

	t.Run("a browser without the invitation cookie gets the invitation block without a standing invitation", func(t *testing.T) {
		t.Parallel()
		f, _, _ := newMailFixture(t, gomock.NewController(t))

		want := map[string]any{"open": "/api/user/invitation", "accept": "/api/user/invitation/accept", "signUp": "/api/user/invitation/sign-up"}
		if diff := cmp.Diff(want, invitationBlock(t, f, invitationList, nil)); diff != "" {
			t.Errorf("invitation block mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("a browser carrying a live invitation is told it: the address, the tenant, when it expires and whether the username is the address", func(t *testing.T) {
		t.Parallel()
		f, _, links := newMailFixture(t, gomock.NewController(t))
		cookies := openedInvitation(t, f, links)

		want := map[string]any{
			"open": "/api/user/invitation", "accept": "/api/user/invitation/accept", "signUp": "/api/user/invitation/sign-up",
			"standing": map[string]any{
				"email": "pat@lakeside.edu", "tenantId": "lakeside", "newTenant": false, "directoryOnly": false, "expiresAt": links.last(t).ExpiresAt.Format(time.RFC3339Nano),
				"usernameIsEmail": false, "methods": []any{}, "accountExists": false, "hasPassword": false,
			},
		}
		if diff := cmp.Diff(want, invitationBlock(t, f, invitationList, cookies)); diff != "" {
			t.Errorf("invitation block mismatch (-want +got):\n%s", diff)
		}
		if got := invitationBlock(t, f, invitationList, nil); got["standing"] != nil {
			t.Errorf("a request without the cookie after one with it got %v, want no standing invitation: the answer is not shared between requests", got["standing"])
		}
	})

	t.Run("the standing invitation carries the name the inviter gave, as Open() does", func(t *testing.T) {
		t.Parallel()
		f, recorder, links := newMailFixture(t, gomock.NewController(t))
		if _, err := f.auth.API().Invite(context.Background(), &InvitationRequest{Email: "pat@lakeside.edu", Name: "Pat Reyes", TenantID: "lakeside", RoleNames: []string{"Teacher"}}); err != nil {
			t.Fatalf("Invite() error = %v", err)
		}
		cookies := invitationCookies(t, f, links, tokenIn(t, recorder.Messages()[0].Text, "invitation"))

		standing, _ := invitationBlock(t, f, invitationList, cookies)["standing"].(map[string]any)
		if standing["name"] != "Pat Reyes" {
			t.Errorf("standing invitation = %v, want the name Pat Reyes", standing)
		}
	})

	t.Run("the standing invitation says whether an account holds the address and whether it signs in with a password", func(t *testing.T) {
		t.Parallel()
		f, _, links := newMailFixture(t, gomock.NewController(t))
		cookies := openedInvitation(t, f, links)

		links.holds("pat@lakeside.edu", &sessionstorage.SessionUser{ID: impulse.Must(impulse.NewUUID()), Username: "pat", PasswordHash: hashed(t, "pw")})
		standing, _ := invitationBlock(t, f, invitationList, cookies)["standing"].(map[string]any)
		if standing["accountExists"] != true || standing["hasPassword"] != true {
			t.Errorf("standing = %v, want accountExists true and hasPassword true for an account with a password", standing)
		}
		links.holds("PAT@lakeside.edu", &sessionstorage.SessionUser{ID: impulse.Must(impulse.NewUUID()), Username: "pat"})
		standing, _ = invitationBlock(t, f, invitationList, cookies)["standing"].(map[string]any)
		if standing["accountExists"] != true || standing["hasPassword"] != false {
			t.Errorf("standing = %v, want accountExists true and hasPassword false for an account without a password", standing)
		}
	})

	t.Run("where the password method declares the username as the address, the standing invitation says so, as Open() does", func(t *testing.T) {
		t.Parallel()
		f, _, links := newMailFixture(t, gomock.NewController(t), UsernameIsEmail())
		cookies := openedInvitation(t, f, links)

		standing, _ := invitationBlock(t, f, invitationList, cookies)["standing"].(map[string]any)
		if standing["usernameIsEmail"] != true {
			t.Errorf("standing = %v, want usernameIsEmail true", standing)
		}
	})

	t.Run("a browser whose invitation is dead gets the block without it", func(t *testing.T) {
		t.Parallel()
		f, _, links := newMailFixture(t, gomock.NewController(t))
		cookies := openedInvitation(t, f, links)
		if err := f.auth.API().RevokeInvitation(context.Background(), links.last(t).ID); err != nil {
			t.Fatalf("RevokeInvitation() error = %v", err)
		}

		if got := invitationBlock(t, f, invitationList, cookies); got["standing"] != nil {
			t.Errorf("invitation block = %v, want no standing invitation for a revoked one", got)
		}
	})

	t.Run("a listing without the invitation routes reports no invitation, cookie or not", func(t *testing.T) {
		t.Parallel()
		f, _, links := newMailFixture(t, gomock.NewController(t))
		cookies := openedInvitation(t, f, links)
		list := &MethodList{Methods: invitationList.Methods, ChangePassword: invitationList.ChangePassword, PasswordStrength: invitationList.PasswordStrength}

		if got := invitationBlock(t, f, list, cookies); got != nil {
			t.Errorf("invitation block = %v, want none", got)
		}
	})

	t.Run("the standing invitation names the listed registrations that could admit its address, as Open() does", func(t *testing.T) {
		t.Parallel()
		for _, tt := range []struct {
			email string
			want  []any
		}{
			{email: "pat@lakeside.edu", want: []any{"google-lakeside"}},
			{email: "pat@elsewhere.example", want: []any{}},
		} {
			f, recorder, links := newDirectoryFixture(t)
			if _, err := f.auth.API().Invite(context.Background(), &InvitationRequest{Email: tt.email}); err != nil {
				t.Fatalf("Invite(%s) error = %v", tt.email, err)
			}
			cookies := invitationCookies(t, f, links, tokenIn(t, recorder.Messages()[0].Text, "invitation"))

			standing, _ := invitationBlock(t, f, directoryList, cookies)["standing"].(map[string]any)
			if diff := cmp.Diff(tt.want, standing["methods"]); diff != "" {
				t.Errorf("standing.methods for %s mismatch (-want +got):\n%s", tt.email, diff)
			}
		}
	})

	t.Run("the listing reads the cookie itself when it is routed without StartSession", func(t *testing.T) {
		t.Parallel()
		f, _, links := newMailFixture(t, gomock.NewController(t))
		cookies := openedInvitation(t, f, links)

		r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/user/methods", http.NoBody)
		for _, c := range cookies {
			r.AddCookie(c)
		}
		rr := httptest.NewRecorder()
		f.auth.Methods(invitationList).ServeHTTP(rr, r)
		if rr.Code != http.StatusOK {
			t.Fatalf("Methods() status = %d, want 200: %s", rr.Code, rr.Body.String())
		}
		block, _ := decoded(t, rr)["invitation"].(map[string]any)
		standing, _ := block["standing"].(map[string]any)
		if standing["email"] != "pat@lakeside.edu" {
			t.Errorf("standing = %v, want pat's invitation", standing)
		}
	})
}
