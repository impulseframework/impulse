package session

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/session/signin"
	"go.uber.org/mock/gomock"
)

// TestNewAuth_superAdministrators pins the rule the storage is handed by an Auth built
// with its super administrators: the application's rule with the second factor of a
// super administrator after it, and the second factor alone where the application set no
// rule. An Auth built for an application with no permission engine
// (NoSuperAdministrators) hands the application's rule alone, and none where none was
// set.
func TestNewAuth_superAdministrators(t *testing.T) {
	t.Parallel()

	userID := impulse.Must(impulse.NewUUID())
	allow := func(context.Context, mockTx, *sessioninfo.NewSessionRequest) (*signin.Decision, error) {
		return signin.Allow(), nil
	}
	tests := []struct {
		name string
		// none builds the Auth with NoSuperAdministrators; super says whether pat holds
		// the role; rule sets a sign-in rule that asks nobody for a factor.
		none  bool
		super bool
		rule  bool
		// wantPolicy is whether the storage is handed a rule at all, and wantOutcome
		// what it decides for pat.
		wantPolicy  bool
		wantOutcome signin.PolicyOutcome
		wantFactors []sessioninfo.AuthMethod
	}{
		{
			name:  "no rule: a super administrator is held for the mailed code",
			super: true, wantPolicy: true, wantOutcome: signin.HoldForMFA, wantFactors: []sessioninfo.AuthMethod{sessioninfo.MethodEmailCode},
		},
		{
			name:       "no rule: anyone else is allowed",
			wantPolicy: true, wantOutcome: signin.AllowSignIn,
		},
		{
			name:  "a rule that asks nobody: a super administrator is held all the same",
			super: true, rule: true, wantPolicy: true, wantOutcome: signin.HoldForMFA, wantFactors: []sessioninfo.AuthMethod{sessioninfo.MethodEmailCode},
		},
		{
			name:       "a rule that asks nobody: anyone else is allowed",
			rule:       true,
			wantPolicy: true, wantOutcome: signin.AllowSignIn,
		},
		{
			name: "an application with no permission engine: the rule alone decides",
			none: true, super: true, rule: true, wantPolicy: true, wantOutcome: signin.AllowSignIn,
		},
		{
			name: "an application with no permission engine and no rule: the library's own checks decide",
			none: true, super: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newAuthStoreMock(gomock.NewController(t))
			superAdministrators := SuperAdministratorsBy(func(_ context.Context, user accesstypes.User) (bool, error) {
				return tt.super && user == accesstypes.User(userID.String()), nil
			})
			if tt.none {
				superAdministrators = NoSuperAdministrators()
			}
			a, err := NewAuth[NoCustomData, NoCustomData](store, cookieKey, superAdministrators, quietMail())
			if err != nil {
				t.Fatalf("NewAuth() error = %v", err)
			}
			if tt.rule {
				a.SetSignInPolicy(allow)
			}
			var handed signin.Policy[mockTx]
			store.EXPECT().SignIn(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, _ *sessioninfo.NewSessionRequest, rules sessionstorage.SignInRules[mockTx]) (impulse.UUID, error) {
					handed = rules.Policy

					return userID, nil
				}).AnyTimes()
			store.EXPECT().TouchUserSignIn(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

			r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", http.NoBody)
			if _, err := a.signIn(r.Context(), httptest.NewRecorder(), &signInAttempt{
				identity: &sessioninfo.Identity{Method: sessioninfo.MethodPassword, Subject: userID.String()}, reason: sessioninfo.ReasonLogin, userID: userID, username: "pat",
			}); err != nil {
				t.Fatalf("signIn() error = %v", err)
			}

			if (handed != nil) != tt.wantPolicy {
				t.Fatalf("the storage was handed a rule: %t, want %t", handed != nil, tt.wantPolicy)
			}
			if handed == nil {
				return
			}
			decision, err := handed(context.Background(), mockTx{}, &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, UserID: userID, Username: "pat"})
			if err != nil {
				t.Fatalf("the rule's error = %v", err)
			}
			if decision.Outcome() != tt.wantOutcome {
				t.Errorf("Outcome() = %v, want %v", decision.Outcome(), tt.wantOutcome)
			}
			if got := decision.Factors(); len(got) != len(tt.wantFactors) || (len(got) == 1 && got[0] != tt.wantFactors[0]) {
				t.Errorf("Factors() = %v, want %v", got, tt.wantFactors)
			}
		})
	}
}

// TestNewAuth_superAdministrators_refused pins that no Auth is built without a word on
// its super administrators: the zero value and a nil check are refused, and each refusal
// says what to pass. A check, and the word that there are none, build the Auth.
func TestNewAuth_superAdministrators_refused(t *testing.T) {
	t.Parallel()

	check := func(context.Context, accesstypes.User) (bool, error) {
		return false, nil
	}
	tests := []struct {
		name                string
		superAdministrators SuperAdministrators
		// wantHas is a part of the refusal, "" for an Auth that is built.
		wantHas string
	}{
		{
			name:                "nothing said: the zero value",
			superAdministrators: SuperAdministrators{},
			wantHas:             "NewAuth was not told who the Auth's super administrators are: pass session.SuperAdministratorsBy(signin.SuperAdministrators(...)) over the access library's user manager",
		},
		{
			name:                "no check",
			superAdministrators: SuperAdministratorsBy(nil),
			wantHas:             "session.SuperAdministratorsBy was given no check: pass signin.SuperAdministrators over the access library's user manager",
		},
		{
			name:                "super administrators by a check",
			superAdministrators: SuperAdministratorsBy(check),
		},
		{
			name:                "no super administrators",
			superAdministrators: NoSuperAdministrators(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := NewAuth[NoCustomData, NoCustomData](newAuthStoreMock(gomock.NewController(t)), cookieKey, tt.superAdministrators, quietMail())
			switch {
			case tt.wantHas == "" && err != nil:
				t.Errorf("NewAuth() error = %v, want the Auth built", err)
			case tt.wantHas != "" && (err == nil || !strings.Contains(err.Error(), tt.wantHas)):
				t.Errorf("NewAuth() error = %v, want one containing %q", err, tt.wantHas)
			}
		})
	}
}
