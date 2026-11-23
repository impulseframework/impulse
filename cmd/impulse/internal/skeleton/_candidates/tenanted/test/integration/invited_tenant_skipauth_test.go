//go:build skipAuth

package integration

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"testing"

	"github.com/impulseframework/impulse/session/sessioninfo"
)

// organizationAccountSentence is what a password is told at an invitation that is
// accepted through an organization's directory alone.
const organizationAccountSentence = "This invitation is accepted with your organization's account, not with a password or a personal account. Sign in with your organization's account (Google Workspace or Microsoft Entra)."

// TestInvitedTenant_acceptedThroughADirectory proves what an organization's directory is
// to an invitation that makes a tenant. An invitation that says directory only is
// accepted through a directory alone: its page offers no account with a password, a
// sign-up with one is refused with what to sign in with, and nothing is made. Accepted
// through the staff auth's google registration, the tenant is made directory only with
// the Workspace the person came through as its directory, carrying the role the
// invitation names for the directory's people, and the person is its administrator. A
// directory belongs to one tenant: a second invitation accepted through the same
// Workspace is refused at the sign-in, nothing is made, and the invitation stands.
//
// Under the session library's skipAuth build tag Google is simulated: the callback signs
// in whoever APP_GOOGLE_USERNAME names, from one Workspace. The test sets that
// variable, so it does not run in parallel.
func TestInvitedTenant_acceptedThroughADirectory(t *testing.T) {
	ctx := t.Context()
	s := newServed(ctx, t)
	admin := signedIn(ctx, t, s, adminUser)
	tenants := func() []string {
		return slices.Sorted(maps.Values(tenantNames(ctx, t, admin)))
	}

	// The invitation: directory only, with the role the directory's people start with.
	const emberFounder = "ember-founder@example.com"
	status, body, closed := inviteTenant(ctx, t, admin, map[string]any{"email": emberFounder, "tenantName": "Ember", "tenantDirectoryOnly": true, "defaultRole": "TenantAdministrator"})
	if status != http.StatusOK {
		t.Fatalf("the directory-only invitation: status %d: %s", status, body)
	}
	token := mailedInvitation(t, s, emberFounder)
	if row := invitedTenant(ctx, t, admin, closed.ID); row == nil || row["tenantDirectoryOnly"] != true || row["defaultRole"] != "TenantAdministrator" {
		t.Errorf("the directory-only invitation as the administrator lists it = %v, want it directory only with TenantAdministrator to start with", row)
	}

	// The page says so and offers no account with a password; a sign-up with one is
	// refused, with what to sign in with, and nothing is made.
	founder := newBrowser(t, s)
	status, page := showInvitation(ctx, t, founder, token)
	if status != http.StatusOK || page["newTenant"] != true || page["tenantName"] != "Ember" || page["directoryOnly"] != true || page["signUp"] != false {
		t.Errorf("the directory-only invitation's page: status %d: %v, want a new tenant Ember, directory only, with no sign-up by password", status, page)
	}
	status, body = founder.do(ctx, http.MethodPost, "/api/user/invitation/sign-up", []byte(`{"username":"ember-founder","password":"`+invitedPassword+`"}`))
	var refused struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &refused); err != nil || status != http.StatusUnauthorized || refused.Code != string(sessioninfo.RefusedOrganizationAccountRequired) || refused.Message != organizationAccountSentence {
		t.Errorf("a password at a directory-only invitation: status %d %s (%v), want 401 %s saying what to sign in with", status, body, err, sessioninfo.RefusedOrganizationAccountRequired)
	}
	if got := tenants(); !slices.Equal(got, []string{"North", "South"}) {
		t.Errorf("the tenants after the password was refused = %v, want North and South alone", got)
	}
	if invitedTenant(ctx, t, admin, closed.ID) == nil {
		t.Error("the invitation no longer stands after the password was refused")
	}
	if accountExists(ctx, t, s, "ember-founder") {
		t.Error("an account was made for the person whose password was refused")
	}

	// Accepted through the Workspace: the same link, followed again, then the sign-in
	// through the google registration and the second factor the auth's sign-in setting
	// requires.
	t.Setenv("APP_GOOGLE_USERNAME", emberFounder)
	founder = newBrowser(t, s)
	if status, page := showInvitation(ctx, t, founder, token); status != http.StatusOK {
		t.Fatalf("opening the invitation again: status %d: %v", status, page)
	}
	status, location := founder.redirect(ctx, "/api/user/google/login?returnUrl="+url.QueryEscape("/dashboard"))
	if status != http.StatusFound {
		t.Fatalf("GET /api/user/google/login: status %d, want %d to the directory", status, http.StatusFound)
	}
	if status, location = founder.redirect(ctx, location); status != http.StatusFound || location != "/login?pending=mfa&returnUrl="+url.QueryEscape("/dashboard") {
		t.Fatalf("the callback answered %d to %q, want %d to the login page's code step", status, location, http.StatusFound)
	}
	founder.secondFactor(ctx, emberFounder)
	made := tenantNamed(ctx, t, admin, "Ember")
	if made == nil {
		t.Fatalf("the tenants after the acceptance through the Workspace = %v, want Ember among them", tenants())
	}
	emberID, _ := made["id"].(string)
	if made["directoryOnly"] != true {
		t.Errorf("Ember as made = %v, want it directory only", made)
	}
	workspace := sessionDirectory(ctx, t, founder)
	if workspace == nil {
		t.Fatal("the founder's session reports no directory, want the Workspace the sign-in came through")
	}
	if rows := directories(ctx, t, admin, emberID); len(rows) != 1 || rows[0] != *workspace {
		t.Errorf("Ember's directory = %+v, want the Workspace %+v its founder signed in through", rows, *workspace)
	}
	if seats := defaultSeats(ctx, t, admin, emberID); !slices.Equal(seats, []string{"TenantAdministrator"}) {
		t.Errorf("the role Ember's directory gives its people = %v, want TenantAdministrator, as the invitation named", seats)
	}
	if roles := tenantRolesOf(ctx, t, admin, emberID, emberFounder); !slices.Equal(roles, []string{"TenantAdministrator"}) {
		t.Errorf("the founder's roles in Ember = %v, want TenantAdministrator alone", roles)
	}
	if invitedTenant(ctx, t, admin, closed.ID) != nil {
		t.Error("the accepted invitation still stands")
	}

	// The Workspace is Ember's now, and a directory belongs to one tenant: a second
	// invitation accepted through it is refused at the sign-in, the person told their
	// organization already has a tenant, and nothing is made.
	const flintFounder = "flint-founder@example.com"
	status, body, second := inviteTenant(ctx, t, admin, map[string]any{"email": flintFounder, "tenantName": "Flint"})
	if status != http.StatusOK {
		t.Fatalf("the second invitation: status %d: %s", status, body)
	}
	t.Setenv("APP_GOOGLE_USERNAME", flintFounder)
	rival := newBrowser(t, s)
	if status, page := showInvitation(ctx, t, rival, mailedInvitation(t, s, flintFounder)); status != http.StatusOK {
		t.Fatalf("opening the second invitation: status %d: %v", status, page)
	}
	status, location = rival.redirect(ctx, "/api/user/google/login?returnUrl="+url.QueryEscape("/dashboard"))
	if status != http.StatusFound {
		t.Fatalf("GET /api/user/google/login: status %d, want %d to the directory", status, http.StatusFound)
	}
	if status, location = rival.redirect(ctx, location); status != http.StatusFound || location != "/login?code="+string(sessioninfo.RefusedOrganizationHasTenant) {
		t.Fatalf("accepting through a directory Ember has: status %d to %q, want %d to the login page naming %s", status, location, http.StatusFound, sessioninfo.RefusedOrganizationHasTenant)
	}
	if got := tenants(); !slices.Equal(got, []string{"Ember", "North", "South"}) {
		t.Errorf("the tenants after the refused acceptance = %v, want no Flint", got)
	}
	if invitedTenant(ctx, t, admin, second.ID) == nil {
		t.Error("the refused invitation no longer stands")
	}
}
