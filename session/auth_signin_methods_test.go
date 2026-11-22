package session

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/session/oidc"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
	gomock "go.uber.org/mock/gomock"
)

func TestAuthAPI_SignInMethods(t *testing.T) {
	t.Parallel()
	f := newAuthFixture(t, gomock.NewController(t))
	// One registration says what identifies whom it admits, and one gives names alone.
	lakeside := signin.Audience{Organizations: []string{"lakeside.edu"}, Identities: []string{"google-workspace-domain:lakeside.edu"}}
	anyone := signin.Audience{Accounts: "a Google account"}
	register := func(name string, audience signin.Audience, rule signin.AccountRule[mockTx]) {
		f.auth.RegisterExternal(&signin.Registration[mockTx]{
			Name: name, Method: sessioninfo.MethodGoogle, AccountRule: rule,
			Build: func(signin.StateCookies) (*signin.Method, error) {
				return &signin.Method{Name: sessioninfo.MethodGoogle, Audience: audience}, nil
			},
		})
	}
	// One registration leaves who may sign in to what an administrator chooses, and one
	// carries an account rule of its own, which decides it in code.
	register("lakeside", lakeside, nil)
	register("partners", anyone, func(context.Context, mockTx, *sessioninfo.NewSessionRequest) (*signin.Resolution, error) {
		return signin.Reject(""), nil
	})

	got := f.auth.API().SignInMethods()

	want := []RegisteredMethod{
		{Name: "lakeside", Method: sessioninfo.MethodGoogle, Audience: lakeside},
		{Name: "partners", Method: sessioninfo.MethodGoogle, Audience: anyone, OwnRule: true},
		{Name: "password", Method: sessioninfo.MethodPassword, Audience: signin.Audience{Accounts: "an email address", Identities: []string{"password:any-email-address"}}},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("SignInMethods() (-want +got):\n%s", diff)
	}
	// The password method's audience is kept by what it is, never by the words for it.
	if key, want := got[2].Audience.Key(), `{"admits":["password:any-email-address"]}`; key != want {
		t.Errorf("Audience.Key() of the password method = %s, want %s", key, want)
	}
}

// TestAuthAPI_SignInMethods_providerSaidByItsLabel holds the words that say whom an
// organization's own identity provider admits to the label the login page gives its
// registration: "anyone with an account at Acme sign-in", and never the connection the
// provider is registered under, which is the application's key for it. The label is
// words alone: the audience is compared by the provider's issuer, so a label that
// changes leaves a method opened to sign-up open, and a registration the page gives no
// label keeps the name its method gives.
func TestAuthAPI_SignInMethods_providerSaidByItsLabel(t *testing.T) {
	t.Parallel()
	f := newAuthFixture(t, gomock.NewController(t))
	for _, name := range []string{"sso", "partner"} {
		oidc.SignIn(f.auth, name, resolveAll[oidc.Identity](), &oidc.Config{
			RoleSync: signin.DisableRoleSync(), Issuer: "https://id." + name + ".example", Connection: name, ClientID: "client", ClientSecret: "secret", RedirectURL: "https://app/" + name + "/callback",
		})
	}
	f.auth.Methods(&MethodList{
		Methods: []AdvertisedMethod{
			{Name: "password", Label: "Password", Path: "/api/user/password/login"},
			{Name: "sso", Label: "Acme sign-in", Path: "/api/user/sso/login"},
		},
		ChangePassword: "/api/user/password",
	})

	said := map[string]signin.Audience{}
	for _, method := range f.auth.API().SignInMethods() {
		said[method.Name] = method.Audience
	}
	if got, want := said["sso"].Words(), "anyone with an account at Acme sign-in"; got != want {
		t.Errorf("the labeled provider's audience reads %q, want %q", got, want)
	}
	if got, want := said["sso"].Key(), `{"admits":["openid-connect-issuer:https://id.sso.example"]}`; got != want {
		t.Errorf("the labeled provider's audience key = %s, want %s: the label is words, and the issuer is what the audience is compared by", got, want)
	}
	if got, want := said["partner"].Words(), "anyone with an account at partner"; got != want {
		t.Errorf("the audience of a provider the page gives no label reads %q, want %q", got, want)
	}
}
