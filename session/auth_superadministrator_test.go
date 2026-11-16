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

// TestAuth_SetSuperAdministrators pins the rule the storage is handed once the Auth
// knows its super administrators: the application's rule with the second factor of a
// super administrator after it, whichever of the two was set first, and with no rule of
// the application's at all; without the super administrators the application's rule
// alone, as before.
func TestAuth_SetSuperAdministrators(t *testing.T) {
	t.Parallel()

	userID := impulse.Must(impulse.NewUUID())
	allow := func(context.Context, mockTx, *sessioninfo.NewSessionRequest) (*signin.Decision, error) {
		return signin.Allow(), nil
	}
	tests := []struct {
		name string
		// set wires the Auth; super says whether pat holds the role.
		set   func(a *Auth[NoCustomData, NoCustomData, mockTx], check signin.SuperAdministratorCheck)
		super bool
		// wantPolicy is whether the storage is handed a rule at all, and wantOutcome
		// what it decides for pat.
		wantPolicy  bool
		wantOutcome signin.PolicyOutcome
		wantFactors []sessioninfo.AuthMethod
	}{
		{
			name: "no rule and the super administrators: a super administrator is held for the mailed code",
			set: func(a *Auth[NoCustomData, NoCustomData, mockTx], check signin.SuperAdministratorCheck) {
				a.SetSuperAdministrators(check)
			},
			super: true, wantPolicy: true, wantOutcome: signin.HoldForMFA, wantFactors: []sessioninfo.AuthMethod{sessioninfo.MethodEmailCode},
		},
		{
			name: "no rule and the super administrators: anyone else is allowed",
			set: func(a *Auth[NoCustomData, NoCustomData, mockTx], check signin.SuperAdministratorCheck) {
				a.SetSuperAdministrators(check)
			},
			wantPolicy: true, wantOutcome: signin.AllowSignIn,
		},
		{
			name: "a rule that asks nobody, set first",
			set: func(a *Auth[NoCustomData, NoCustomData, mockTx], check signin.SuperAdministratorCheck) {
				a.SetSignInPolicy(allow)
				a.SetSuperAdministrators(check)
			},
			super: true, wantPolicy: true, wantOutcome: signin.HoldForMFA, wantFactors: []sessioninfo.AuthMethod{sessioninfo.MethodEmailCode},
		},
		{
			name: "a rule that asks nobody, set after",
			set: func(a *Auth[NoCustomData, NoCustomData, mockTx], check signin.SuperAdministratorCheck) {
				a.SetSuperAdministrators(check)
				a.SetSignInPolicy(allow)
			},
			super: true, wantPolicy: true, wantOutcome: signin.HoldForMFA, wantFactors: []sessioninfo.AuthMethod{sessioninfo.MethodEmailCode},
		},
		{
			name: "a rule and no super administrators: the rule alone decides",
			set: func(a *Auth[NoCustomData, NoCustomData, mockTx], _ signin.SuperAdministratorCheck) {
				a.SetSignInPolicy(allow)
			},
			super: true, wantPolicy: true, wantOutcome: signin.AllowSignIn,
		},
		{
			name: "neither: the library's own checks decide",
			set:  func(*Auth[NoCustomData, NoCustomData, mockTx], signin.SuperAdministratorCheck) {},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newAuthStoreMock(gomock.NewController(t))
			a, err := NewAuth[NoCustomData, NoCustomData](store, cookieKey)
			if err != nil {
				t.Fatalf("NewAuth() error = %v", err)
			}
			tt.set(a, func(_ context.Context, user accesstypes.User) (bool, error) {
				return tt.super && user == accesstypes.User(userID.String()), nil
			})
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

// TestAuth_SetSuperAdministrators_refused pins the wiring mistakes: no check, a second
// one, and one set once the Auth is in use.
func TestAuth_SetSuperAdministrators_refused(t *testing.T) {
	t.Parallel()

	check := func(context.Context, accesstypes.User) (bool, error) {
		return false, nil
	}
	tests := []struct {
		name    string
		before  func(a *Auth[NoCustomData, NoCustomData, mockTx])
		set     func(a *Auth[NoCustomData, NoCustomData, mockTx])
		wantHas string
	}{
		{
			name:    "no check",
			set:     func(a *Auth[NoCustomData, NoCustomData, mockTx]) { a.SetSuperAdministrators(nil) },
			wantHas: "given nil",
		},
		{
			name:    "set twice",
			before:  func(a *Auth[NoCustomData, NoCustomData, mockTx]) { a.SetSuperAdministrators(check) },
			set:     func(a *Auth[NoCustomData, NoCustomData, mockTx]) { a.SetSuperAdministrators(check) },
			wantHas: "set twice",
		},
		{
			name: "set after the Auth served a request",
			before: func(a *Auth[NoCustomData, NoCustomData, mockTx]) {
				r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
				a.StartSession(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(httptest.NewRecorder(), r)
			},
			set:     func(a *Auth[NoCustomData, NoCustomData, mockTx]) { a.SetSuperAdministrators(check) },
			wantHas: "after the Auth served a request",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newAuthStoreMock(gomock.NewController(t))
			a, err := NewAuth[NoCustomData, NoCustomData](store, cookieKey)
			if err != nil {
				t.Fatalf("NewAuth() error = %v", err)
			}
			if tt.before != nil {
				tt.before(a)
			}
			defer func() {
				got, _ := recover().(string)
				if !strings.Contains(got, tt.wantHas) {
					t.Errorf("panic = %q, want one containing %q", got, tt.wantHas)
				}
			}()
			tt.set(a)
		})
	}
}
