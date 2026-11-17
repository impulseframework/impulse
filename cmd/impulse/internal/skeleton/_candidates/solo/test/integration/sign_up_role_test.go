package integration

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/session"
)

// TestSignUp_theDefaultRoleIsOneTheOpenerMayGive proves, on the served stack, that
// opening a sign-in method gives nobody a power that giving a role directly would not.
//
// A default role is given to every person who signs up, so whoever opens a method with
// one gives that role to people nobody named. The greeter holds a custom role that
// opens sign-up and gives one role, the feature flags': its grant on AssignStaffRole
// carries the condition new.role = 'FeatureAdministrator', the way a role file limits
// which roles its holder hands out. The picker of the Open sign-up dialog lists the
// greeter that role alone, and lists the administrator every role. Opening the method
// with a role the greeter may not give is refused with 403 naming the role, at the
// warning as at the save, and writes nothing: the Administrator's role, and the
// greeter's own. With the role the greeter may give, the warning is answered and the
// method opens.
func TestSignUp_theDefaultRoleIsOneTheOpenerMayGive(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	admin := newBrowser(t, s)
	admin.signIn(ctx, adminUser)

	const role accesstypes.Role = "Greeter"
	global := accesstypes.GlobalPolicyScope()
	manager := s.access.UserManager()
	if err := manager.AddRole(ctx, global, role, "Decides who may sign up, and gives the feature flags' role."); err != nil {
		t.Fatalf("access.UserManager.AddRole() error = %v", err)
	}
	if err := manager.AddRoleGrants(ctx, global, role,
		access.GrantRow{Permission: accesstypes.Execute, Resource: "AssignStaffRole", Condition: "new.role = 'FeatureAdministrator'"},
		access.GrantRow{Permission: accesstypes.List, Resource: "StaffSignUpRoles", Condition: ""},
		access.GrantRow{Permission: accesstypes.List, Resource: "StaffSignUpRoles.description", Condition: ""},
		access.GrantRow{Permission: accesstypes.Execute, Resource: "OpenStaffSignUp", Condition: ""},
	); err != nil {
		t.Fatalf("access.UserManager.AddRoleGrants() error = %v", err)
	}
	password := adminPassword
	id, err := s.auth.Session().API().CreateSessionUser(ctx, &session.CreateUserRequest{Username: "greeter", Password: &password})
	if err != nil {
		t.Fatalf("CreateSessionUser(greeter) error = %v", err)
	}
	holder := accesstypes.User(id.String())
	if err := manager.AddUserRoles(ctx, global, holder, role); err != nil {
		t.Fatalf("AddUserRoles(greeter) error = %v", err)
	}
	// The store writes signal a reload and the swap is asynchronous: once the grant with
	// no condition shows, every answer below reads a policy that holds the whole role.
	if d := decisionFor(ctx, t, s.access.ForUser(holder), accesstypes.GlobalScope(), accesstypes.Execute, "OpenStaffSignUp", time.Now().Add(15*time.Second)); !d.IsGranted() {
		t.Fatalf("the greeter's Execute on OpenStaffSignUp = %s, want granted before the cases run", d)
	}
	greeter := newBrowser(t, s)
	greeter.signIn(ctx, "greeter")
	if status, body := greeter.do(ctx, http.MethodGet, "/api/permission-digest", nil); status != http.StatusOK {
		t.Fatalf("the greeter reading what they may do: status %d: %s", status, body)
	}

	// The picker lists a caller the roles they may give, each with its description.
	if roles := signUpRoles(ctx, t, greeter); len(roles) != 1 || roles["FeatureAdministrator"] == "" {
		t.Errorf("the roles the picker lists the greeter = %v, want FeatureAdministrator alone, the one role they may give", roles)
	}
	if roles := signUpRoles(ctx, t, admin); len(roles) != 3 || roles["Administrator"] == "" || roles["FeatureAdministrator"] == "" || roles[string(role)] == "" {
		t.Errorf("the roles the picker lists the administrator = %v, want Administrator, FeatureAdministrator and %s", roles, role)
	}

	// A role the greeter may not give is refused by name, at the warning and at the
	// save, and nothing is written.
	for _, refused := range []string{"Administrator", string(role)} {
		request := `{"name":"password","defaultRole":"` + refused + `"}`
		want := "gives every person who signs up the role " + refused + ", which you may not give"
		if status, body := warning(ctx, greeter, "/api/open-staff-sign-up", request); status != http.StatusForbidden || !strings.Contains(string(body), want) {
			t.Errorf("the warning of opening with the role %s, as the greeter: status %d: %s, want 403 saying %q", refused, status, body, want)
		}
		if status, body := greeter.do(ctx, http.MethodPost, "/api/open-staff-sign-up", []byte(request)); status != http.StatusForbidden || !strings.Contains(string(body), want) {
			t.Errorf("opening with the role %s, as the greeter: status %d: %s, want 403 saying %q", refused, status, body, want)
		}
	}
	if rows := countRows(ctx, t, s, "StaffSessionSignInMethods"); rows != 0 {
		t.Fatalf("rows of the sign-in methods table after the refused saves = %d, want none", rows)
	}

	// The role the greeter may give opens the method.
	request := `{"name":"password","defaultRole":"FeatureAdministrator"}`
	if status, body := warning(ctx, greeter, "/api/open-staff-sign-up", request); status != http.StatusOK || !strings.Contains(string(body), openConfirm) {
		t.Errorf("the warning of opening with the role the greeter may give: status %d: %s, want 200 with the button %q", status, body, openConfirm)
	}
	if status, body := greeter.do(ctx, http.MethodPost, "/api/open-staff-sign-up", []byte(request)); status != http.StatusNoContent {
		t.Fatalf("opening with the role the greeter may give: status %d: %s", status, body)
	}
	if got := signInMethods(ctx, t, admin); len(got) != 1 || !got[0].OpenSignUp || got[0].DefaultRole != "FeatureAdministrator" {
		t.Errorf("the sign-in methods once the greeter opened the password method = %+v, want it open with the role", got)
	}
}
