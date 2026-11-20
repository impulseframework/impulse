package session

import (
	"testing"

	"github.com/impulseframework/impulse/session/azure"
	"github.com/impulseframework/impulse/session/google"
	"github.com/impulseframework/impulse/session/internal/workos"
	"github.com/impulseframework/impulse/session/oidc"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage/mock/mock_sessionstorage"
	"github.com/impulseframework/impulse/session/signin"
	gomock "go.uber.org/mock/gomock"
)

// TestAuthAPI_DirectoryName pins what the Auth's registrations call a directory, for a
// sentence that names it to a person: the name a registration that takes the connection
// gives it, and the label of the button of the first registration of the login page
// that takes it. A tenant the registrations know by an id alone has no name, and a
// method that names no connection has none either; each is then said by the label, and
// never by the connection. A name is taken from a registration the page does not list
// too, since it is the application's own name for the organization. An organization's
// own identity provider is said by the label the login page gives its registration, and
// never by the connection it is registered under, which is the application's key for it;
// one the page gives no label keeps the application's name for it.
func TestAuthAPI_DirectoryName(t *testing.T) {
	t.Parallel()

	const stranger = "44444444-4444-4444-4444-444444444444"
	tests := []struct {
		name       string
		method     sessioninfo.AuthMethod
		connection string
		wantName   string
		wantLabel  string
	}{
		{name: "a Workspace the Google registration names", method: sessioninfo.MethodGoogle, connection: "fleet", wantName: "fleet", wantLabel: "Google"},
		{name: "a Workspace admitted as any Workspace is its domain", method: sessioninfo.MethodGoogle, connection: "harbor.example", wantName: "harbor.example", wantLabel: "Google"},
		{name: "a Google account in no Workspace, which the registration does not admit", method: sessioninfo.MethodGoogle, connection: google.AnyAccountConnection},
		{name: "a tenant an Entra registration names", method: sessioninfo.MethodAzure, connection: tenantLake, wantName: "Lakeside", wantLabel: "Lakeside staff"},
		{name: "a tenant listed by its id alone has the label of its registration and no name", method: sessioninfo.MethodAzure, connection: tenantRiver, wantLabel: "Lakeside staff"},
		{name: "a tenant admitted as any tenant has the label of that registration and no name", method: sessioninfo.MethodAzure, connection: stranger, wantLabel: "Microsoft"},
		{
			name:   "a tenant named by a registration the page does not list keeps the name, under the label of the one that is listed",
			method: sessioninfo.MethodAzure, connection: tenantOther, wantName: "Harbor", wantLabel: "Microsoft",
		},
		{name: "a method that names no connection has its label alone", method: sessioninfo.MethodWorkOS, connection: "conn_01H", wantLabel: "Single sign-on"},
		{name: "an identity provider is said by the label of its registration, not by the key it is registered under", method: sessioninfo.MethodOIDC, connection: "sso", wantName: "Acme sign-in", wantLabel: "Acme sign-in"},
		{name: "an identity provider the page gives no label keeps the application's name for it", method: sessioninfo.MethodOIDC, connection: "partner", wantName: "partner"},
		{name: "a provider no registration takes", method: sessioninfo.MethodOIDC, connection: "https://id.example"},
		{name: "the password is no directory", method: sessioninfo.MethodPassword, connection: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := mock_sessionstorage.NewMockAuthStore[mockTx](gomock.NewController(t))
			store.EXPECT().CustomUserDataType().Return(nil).AnyTimes()
			store.EXPECT().SetUsernameIsEmail(gomock.Any()).AnyTimes()
			a, err := NewAuth[NoCustomData, NoCustomData](store, cookieKey, NoSuperAdministrators(), quietMail())
			if err != nil {
				t.Fatalf("NewAuth() error = %v", err)
			}
			PasswordSignIn(a)
			google.SignIn(a, "google", resolveAll[google.Identity](), &google.Config{
				RoleSync: google.DisableRoleSync(), ClientID: "client", ClientSecret: "secret", RedirectURL: "https://app/google/callback",
				Workspaces: google.Workspaces(google.Workspace("fleet", "fleet.example"), google.AnyWorkspace()),
			})
			entra := func(name string, tenants azure.TenantSet) {
				azure.SignIn(a, name, resolveAll[azure.Identity](), &azure.Config{
					RoleSync: signin.DisableRoleSync(), Authority: testAuthority, Tenants: tenants, ClientID: "client", ClientSecret: "secret", RedirectURL: "https://app/" + name + "/callback",
				})
			}
			entra("lakeside-entra", azure.Tenants(azure.NamedTenant(tenantLake, "Lakeside"), azure.Tenant(tenantRiver)))
			entra("any-entra", azure.AnyTenant())
			entra("harbor-entra", azure.NamedTenant(tenantOther, "Harbor"))
			workos.SignIn(a, "workos", resolveAll[workos.Identity](), &workos.Config{APIKey: "sk", ClientID: "client", RedirectURL: "https://app/sso/callback"})
			provider := func(name, issuer string) {
				oidc.SignIn(a, name, resolveAll[oidc.Identity](), &oidc.Config{
					RoleSync: signin.DisableRoleSync(), Issuer: issuer, Connection: name, ClientID: "client", ClientSecret: "secret", RedirectURL: "https://app/" + name + "/callback",
				})
			}
			provider("sso", "https://id.acme.example")
			provider("partner", "https://id.partner.example")
			a.Methods(&MethodList{
				Methods: []AdvertisedMethod{
					{Name: "google", Label: "Google", Path: "/api/user/google/login"},
					{Name: "password", Label: "Password", Path: "/api/user/password/login"},
					{Name: "lakeside-entra", Label: "Lakeside staff", Path: "/api/user/lakeside-entra/login"},
					{Name: "any-entra", Label: "Microsoft", Path: "/api/user/any-entra/login"},
					{Name: "workos", Label: "Single sign-on", Path: "/api/user/workos/login"},
					{Name: "sso", Label: "Acme sign-in", Path: "/api/user/sso/login"},
				},
				ChangePassword: "/api/user/password/change",
			})

			name, label := a.API().DirectoryName(tt.method, tt.connection)
			if name != tt.wantName || label != tt.wantLabel {
				t.Errorf("DirectoryName(%s, %q) = name %q under the label %q, want name %q under the label %q", tt.method, tt.connection, name, label, tt.wantName, tt.wantLabel)
			}
		})
	}
}
