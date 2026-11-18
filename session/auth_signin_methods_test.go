package session

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
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
