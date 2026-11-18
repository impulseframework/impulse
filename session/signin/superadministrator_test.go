package signin

import (
	"context"
	"slices"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// globalRoleManager is a role store that answers the memberships held in the global
// partition and records the scopes it was asked for.
type globalRoleManager struct {
	UserRoleManager
	held  accesstypes.RoleCollection
	err   error
	asked [][]accesstypes.PolicyScope
}

func (g *globalRoleManager) UserRoles(_ context.Context, _ accesstypes.User, scopes ...accesstypes.PolicyScope) (accesstypes.RoleCollection, error) {
	g.asked = append(g.asked, scopes)

	return g.held, g.err
}

// TestSuperAdministrators pins the check an Auth is given: a person is a super
// administrator when the role store holds the Super Administrator role for them in the
// global partition, which is all the check reads, and a store error is the answer's.
func TestSuperAdministrators(t *testing.T) {
	t.Parallel()

	global := accesstypes.GlobalPolicyScope()
	tests := []struct {
		name    string
		held    accesstypes.RoleCollection
		err     error
		want    bool
		wantErr bool
	}{
		{name: "a holder of the role", held: accesstypes.RoleCollection{global: {"Administrator", accesstypes.SuperAdministrator}}, want: true},
		{name: "a holder of another global role", held: accesstypes.RoleCollection{global: {"Administrator"}}},
		{name: "a person who holds nothing there", held: accesstypes.RoleCollection{}},
		{name: "a store error", err: errors.New("store blip"), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			roles := &globalRoleManager{held: tt.held, err: tt.err}
			got, err := SuperAdministrators(roles)(t.Context(), "pat")
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Errorf("SuperAdministrators()() = (%v, %v), want %v, error %v", got, err, tt.want, tt.wantErr)
			}
			if want := [][]accesstypes.PolicyScope{{global}}; !slices.EqualFunc(roles.asked, want, slices.Equal[[]accesstypes.PolicyScope]) {
				t.Errorf("the role store was asked for %v, want the global partition alone, once", roles.asked)
			}
		})
	}
}

// TestSuperAdministratorFactor pins the rule a super administrator's second factor is:
// after the application's rule, a plain allow becomes a hold for the code the library
// mails when the person holds the role; whatever else the application's rule decided
// stands, and nobody is asked who is a super administrator for a sign-in it did not
// allow; with no rule of the application's every sign-in is decided as allowed. The
// library's rule stands down for a sign-in through a method that synchronizes roles,
// where the directory is the authority: the application's rule decides for a holder as
// for anyone, and nobody is asked who is a super administrator.
func TestSuperAdministratorFactor(t *testing.T) {
	t.Parallel()

	patID := impulse.Must(impulse.UUIDFromString("9a700000-0000-4000-8000-000000000001"))
	answer := func(decision *Decision, err error) Policy[fakeTx] {
		return func(context.Context, fakeTx, *sessioninfo.NewSessionRequest) (*Decision, error) {
			return decision, err
		}
	}
	tests := []struct {
		name   string
		policy Policy[fakeTx]
		super  bool
		// roleNames are the names the sign-in's method asserted: nil for a method that
		// synchronizes no roles, never nil for one that does.
		roleNames []string
		checkErr  error
		want      *Decision
		wantErr   bool
		wantAsked bool
	}{
		{name: "no rule: a super administrator is held for the mailed code", super: true, want: RequireMFA(sessioninfo.MethodEmailCode), wantAsked: true},
		{name: "no rule: anyone else is allowed", want: Allow(), wantAsked: true},
		{name: "a rule that allows: a super administrator is held for the mailed code", policy: answer(Allow(), nil), super: true, want: RequireMFA(sessioninfo.MethodEmailCode), wantAsked: true},
		{name: "a rule that allows: anyone else is allowed", policy: answer(Allow(), nil), want: Allow(), wantAsked: true},
		{name: "a rule that denies stands, and nobody is asked who the person is", policy: answer(Deny("app_denied"), nil), super: true, want: Deny("app_denied")},
		{name: "a rule that holds for a factor of its own stands", policy: answer(RequireMFA("app-totp"), nil), super: true, want: RequireMFA("app-totp")},
		{name: "a rule that answers nothing stands as nothing, which the flow denies", policy: answer(nil, nil), super: true},
		{name: "a rule's error fails the sign-in", policy: answer(nil, errors.New("rule blip")), super: true, wantErr: true},
		{name: "the check's error fails the sign-in", checkErr: errors.New("store blip"), wantErr: true, wantAsked: true},
		{name: "a directory that writes the roles, no rule: a super administrator is allowed, and nobody is asked", super: true, roleNames: []string{"SuperAdministrator"}, want: Allow()},
		{name: "a directory that writes the roles and names none: the rule stands down all the same", super: true, roleNames: []string{}, want: Allow()},
		{
			name:   "a directory that writes the roles, a rule that allows: a super administrator is allowed",
			policy: answer(Allow(), nil), super: true, roleNames: []string{"SuperAdministrator"}, want: Allow(),
		},
		{
			name:   "a directory that writes the roles: the application's own hold for a factor stands for a super administrator as for anyone",
			policy: answer(RequireMFA(sessioninfo.MethodEmailCode), nil), super: true, roleNames: []string{"SuperAdministrator"}, want: RequireMFA(sessioninfo.MethodEmailCode),
		},
		{
			name:   "a directory that writes the roles: the application's refusal stands",
			policy: answer(Deny("app_denied"), nil), super: true, roleNames: []string{"SuperAdministrator"}, want: Deny("app_denied"),
		},
		{name: "a directory that writes the roles: a check that fails is not asked, and fails nothing", roleNames: []string{"admin"}, checkErr: errors.New("store blip"), want: Allow()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var asked []accesstypes.User
			check := func(_ context.Context, user accesstypes.User) (bool, error) {
				asked = append(asked, user)

				return tt.super, tt.checkErr
			}
			policy := SuperAdministratorFactor(tt.policy, check)

			got, err := policy(t.Context(), fakeTx{}, &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, UserID: patID, Username: "pat", RoleNames: tt.roleNames})
			if (err != nil) != tt.wantErr {
				t.Fatalf("policy() error = %v, wantErr %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(tt.want, got, cmp.AllowUnexported(Decision{})); diff != "" {
				t.Errorf("decision (-want +got):\n%s", diff)
			}
			var wantAsked []accesstypes.User
			if tt.wantAsked {
				wantAsked = []accesstypes.User{accesstypes.User(patID.String())}
			}
			if !slices.Equal(asked, wantAsked) {
				t.Errorf("the check was asked for %v, want %v", asked, wantAsked)
			}
		})
	}
}

// TestSuperAdministrator_refused pins the wiring mistakes found at startup.
func TestSuperAdministrator_refused(t *testing.T) {
	t.Parallel()

	for name, build := range map[string]func(){
		"no role store": func() { SuperAdministrators(nil) },
		"no check":      func() { SuperAdministratorFactor[fakeTx](nil, nil) },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			defer func() {
				if recover() == nil {
					t.Error("want a panic: a wiring mistake is found at startup")
				}
			}()
			build()
		})
	}
}
