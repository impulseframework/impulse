package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	cloudspanner "cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/accesstypes"
)

// TestTenants_aDisabledTenantShutsItsPeopleOut proves the application's administrator
// disables a tenant with the record's update, and from their next request its people
// hold nothing in it: the member, north's administrator, is answered as by a tenant that
// does not exist, and the member's list of tenants keeps north with the reason ("North
// is disabled."). Enabled again, north answers the member as before.
func TestTenants_aDisabledTenantShutsItsPeopleOut(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	admin := signedIn(ctx, t, s, adminUser)
	member := signedIn(ctx, t, s, memberUser)
	northSegment := "/api/tenants/" + north + "/announcements"
	if status, body := member.do(ctx, http.MethodGet, northSegment, nil); status != http.StatusOK {
		t.Fatalf("GET %s as the member before north is disabled: status %d, want 200: %s", northSegment, status, body)
	}

	setTenantDisabled(ctx, t, admin, north, true)
	if status, body := member.do(ctx, http.MethodGet, northSegment, nil); status != http.StatusNotFound {
		t.Errorf("GET %s as the member once north is disabled: status %d, want 404: %s", northSegment, status, body)
	}
	const reason = "North is disabled."
	if unavailable := unavailableDomains(ctx, t, member); len(unavailable) != 1 || unavailable[north] != reason {
		t.Errorf("the tenants the member's list keeps as not available once north is disabled = %v, want north alone, with %q", unavailable, reason)
	}
	if domains := userDomains(ctx, t, member); len(domains) != 0 {
		t.Errorf("the member's tenants once north is disabled = %v, want none", domains)
	}

	setTenantDisabled(ctx, t, admin, north, false)
	if status, body := member.do(ctx, http.MethodGet, northSegment, nil); status != http.StatusOK {
		t.Errorf("GET %s as the member once north is enabled again: status %d, want 200: %s", northSegment, status, body)
	}
	if domains := userDomains(ctx, t, member); !slices.Equal(domains, []string{north}) {
		t.Errorf("the member's tenants once north is enabled again = %v, want north", domains)
	}
	if unavailable := unavailableDomains(ctx, t, member); len(unavailable) != 0 {
		t.Errorf("the tenants the member's list keeps as not available once north is enabled again = %v, want none", unavailable)
	}
}

// setTenantDisabled disables the tenant, or enables it again, through the record's
// update in the browser's session, and fails the test unless it answers 200.
func setTenantDisabled(ctx context.Context, t *testing.T, b *browser, tenant string, disabled bool) {
	t.Helper()

	body := fmt.Appendf(nil, `[{"op":"patch","path":"/tenants/%s","value":{"disabled":%t}}]`, tenant, disabled)
	if status, answer := b.do(ctx, http.MethodPatch, "/api/resources", body); status != http.StatusOK {
		t.Fatalf("setting the disabled field of tenant %s to %t: status %d, want 200: %s", tenant, disabled, status, answer)
	}
}

// TestTenants_aDeletedTenantTakesItsRolesAndInvitations proves a tenant that is deleted
// leaves nothing of itself behind. Disabled first, its delete asks with a count of what
// the auth holds for it, and the confirmed request takes it all: the roles people held
// in it are removed, the invitations into it that still stood are withdrawn, and the
// accounts that belonged to it alone are deleted, in the transaction that deletes the
// row, so nothing is left for a later tenant to pick up. A person who holds a role
// elsewhere stays, and so does a person whom another tenant's directory manages. The
// tenant is made the way one comes to exist, by an invitation's first sign-in
// (makeTenant), so its founder is among the people who held roles in it alone.
func TestTenants_aDeletedTenantTakesItsRolesAndInvitations(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	admin := signedIn(ctx, t, s, adminUser)
	member := signedIn(ctx, t, s, memberUser)

	east, _ := makeTenant(ctx, t, s, admin, "East", "founder")
	founderID := staffUserID(ctx, t, admin, "founder")
	eastSegment := "/api/tenants/" + east + "/announcements"

	// The member, north's administrator, is given a role in East; so is a person who
	// holds a role nowhere else, and a person north's directory signs in; and an
	// invitation into East stands.
	if status, body := admin.do(ctx, http.MethodPost, "/api/assign-staff-role", []byte(`{"userId":"`+s.memberID+`","role":"TenantAdministrator","domain":"`+east+`"}`)); status != http.StatusNoContent {
		t.Fatalf("giving the member a role in East: status %d: %s", status, body)
	}
	easterner := staffAccount(ctx, t, s, "easterner", "Easterner")
	if status, body := admin.do(ctx, http.MethodPost, "/api/assign-staff-role", []byte(`{"userId":"`+easterner+`","role":"TenantAdministrator","domain":"`+east+`"}`)); status != http.StatusNoContent {
		t.Fatalf("giving a person with no other role one in East: status %d: %s", status, body)
	}
	northerner := staffAccount(ctx, t, s, "northerner", "Northerner")
	if status, body := admin.do(ctx, http.MethodPost, "/api/assign-staff-role", []byte(`{"userId":"`+northerner+`","role":"TenantAdministrator","domain":"`+east+`"}`)); status != http.StatusNoContent {
		t.Fatalf("giving the person north's directory signs in a role in East: status %d: %s", status, body)
	}
	// North's directory, written as a row, and the northerner's identity in it: north
	// manages the northerner's account.
	const northsConnection = "6f2c1b7e-0000-4000-8000-000000000002"
	if _, err := s.db.Apply(ctx, []*cloudspanner.Mutation{
		cloudspanner.Insert("TenantDirectories", []string{"Id", "TenantId", "Method", "Connection"}, []any{"e0000000-0000-4000-8000-000000000011", north, "azure", northsConnection}),
		cloudspanner.InsertMap("StaffSessionIdentities", map[string]any{
			"Id": "e0000000-0000-4000-8000-000000000012", "UserId": northerner, "Method": "azure", "Connection": northsConnection, "Subject": "subject-of-northerner",
			"CreatedAt": cloudspanner.CommitTimestamp, "LastUsedAt": cloudspanner.CommitTimestamp,
		}),
	}); err != nil {
		t.Fatalf("writing north's directory and the northerner's identity: %v", err)
	}
	status, body := tenantPost(ctx, admin, east, "invite-staff-tenant-user", map[string]any{"email": "invited@example.com", "roles": []string{"TenantAdministrator"}})
	if status != http.StatusOK {
		t.Fatalf("inviting into East: status %d: %s", status, body)
	}
	var invited struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &invited); err != nil || invited.ID == "" {
		t.Fatalf("the invitation answered %s (%v), want its id", body, err)
	}
	if domains := userDomains(ctx, t, member); !slices.Contains(domains, east) {
		t.Fatalf("the member's tenants once given a role in East = %v, want East among them", domains)
	}
	if !accountExists(ctx, t, s, "easterner") {
		t.Fatal("the person of East alone has no account")
	}
	// The permission store holds a role in East for each of the three, under the id of
	// the person's account, which is how it names anyone: the member's, the founder's
	// (the acceptance seated them) and the person's who holds a role there alone.
	scope := accesstypes.DomainPolicyScope(accesstypes.Domain(east))
	holders := []struct {
		name string
		user accesstypes.User
	}{
		{"the member", accesstypes.User(s.memberID)},
		{"the founder", accesstypes.User(founderID)},
		{"the person of East alone", accesstypes.User(easterner)},
		{"the person north's directory signs in", accesstypes.User(northerner)},
	}
	for _, holder := range holders {
		if roles := rolesHeld(ctx, t, s, holder.user, scope); !slices.Contains(roles, "TenantAdministrator") {
			t.Fatalf("the roles the permission store holds for %s in East = %v, want TenantAdministrator", holder.name, roles)
		}
	}

	// A tenant is disabled before it is deleted. The delete is asked first, with a count
	// of what goes: the accounts of East alone (its founder's and the easterner's), the
	// four roles held in it and the invitation; nothing is gone. The request that says it
	// takes them deletes the tenant.
	setTenantDisabled(ctx, t, admin, east, true)
	status, body = admin.do(ctx, http.MethodPatch, "/api/resources", []byte(`[{"op":"remove","path":"/tenants/`+east+`"}]`))
	if status != http.StatusPreconditionRequired || !strings.Contains(string(body), "Delete this tenant and everything under it: 2 accounts, 4 roles held, 1 invitation. This can't be undone.") {
		t.Fatalf("deleting the tenant without saying it takes what is under it: status %d, want 428 with the question: %s", status, body)
	}
	for _, username := range []string{"founder", "easterner", "northerner"} {
		if !accountExists(ctx, t, s, username) {
			t.Fatalf("the account of %s is gone after the question: a delete that is asked the question deletes nothing", username)
		}
	}
	// The delete that goes ahead brings the token the question gave, in this session.
	var asked struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &asked); err != nil || asked.Token == "" {
		t.Fatalf("the question carries no token (%v): %s", err, body)
	}
	if status, body := admin.do(ctx, http.MethodPatch, "/api/resources", []byte(`[{"op":"remove","path":"/tenants/`+east+`","value":{"everythingUnder":true,"token":"`+asked.Token+`"}}]`)); status != http.StatusOK {
		t.Fatalf("deleting the tenant: status %d: %s", status, body)
	}

	// Gone at once, for the administrator and for the member alike.
	for name, b := range map[string]*browser{"the administrator": admin, "the member": member} {
		if status, body := b.do(ctx, http.MethodGet, eastSegment, nil); status != http.StatusNotFound {
			t.Errorf("GET %s as %s after the tenant is deleted: status %d, want 404: %s", eastSegment, name, status, body)
		}
	}
	if domains := userDomains(ctx, t, member); !slices.Equal(domains, []string{north}) {
		t.Errorf("the member's tenants after East is deleted = %v, want north alone", domains)
	}

	// The permission store holds no role in the deleted tenant: not the member's, not
	// the founder's, not the person's who held a role there alone, not the northerner's.
	for _, holder := range holders {
		if roles := rolesHeld(ctx, t, s, holder.user, scope); len(roles) != 0 {
			t.Errorf("the roles the permission store holds for %s in the deleted tenant = %v, want none", holder.name, roles)
		}
	}
	// The member holds north still and keeps the account, and the northerner, whom
	// north's directory signs in, keeps theirs; the accounts that belonged to East alone,
	// its founder's and the person's given a role there, are deleted with it.
	if !accountExists(ctx, t, s, memberUser) {
		t.Error("the member's account is gone, want it kept: the member holds a role in north")
	}
	if !accountExists(ctx, t, s, "northerner") {
		t.Error("the northerner's account is gone, want it kept: north's directory manages it")
	}
	for _, username := range []string{"founder", "easterner"} {
		if accountExists(ctx, t, s, username) {
			t.Errorf("the account of %s still exists, want it deleted with the tenant: it belonged to East alone", username)
		}
	}

	// The invitation into East is withdrawn: its link is consumed.
	status, body = admin.do(ctx, http.MethodGet, "/api/staff-session-mail-links/"+invited.ID, nil)
	if status != http.StatusOK {
		t.Fatalf("GET the invitation: status %d: %s", status, body)
	}
	var link struct {
		ConsumedAt *string `json:"consumedAt"`
	}
	if err := json.Unmarshal(body, &link); err != nil {
		t.Fatalf("the invitation is not a row: %v: %s", err, body)
	}
	if link.ConsumedAt == nil {
		t.Errorf("the invitation into the deleted tenant still stands, want it withdrawn: %s", body)
	}
}

// tenantNames reads the browser's tenant list from the generated user-domains route:
// each tenant's name by its id.
func tenantNames(ctx context.Context, t *testing.T, b *browser) map[string]string {
	t.Helper()

	status, body := b.do(ctx, http.MethodGet, "/api/user-domains", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /api/user-domains: status %d: %s", status, body)
	}
	var listed []struct {
		Domain string `json:"domain"`
		Name   string `json:"name"`
	}
	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatalf("user-domains is not a list: %v: %s", err, body)
	}
	names := make(map[string]string, len(listed))
	for _, entry := range listed {
		names[entry.Domain] = entry.Name
	}

	return names
}

// accountExists reports whether the staff auth holds a user under the username.
func accountExists(ctx context.Context, t *testing.T, s *served, user string) bool {
	t.Helper()

	iter := s.db.Client.Single().Query(ctx, cloudspanner.Statement{
		SQL:    "SELECT COUNT(*) FROM StaffSessionUsers WHERE Username = @user",
		Params: map[string]any{"user": user},
	})
	defer iter.Stop()
	row, err := iter.Next()
	if err != nil {
		t.Fatalf("counting accounts named %s: %v", user, err)
	}
	var n int64
	if err := row.Columns(&n); err != nil {
		t.Fatalf("reading the count of accounts named %s: %v", user, err)
	}

	return n > 0
}

// rolesHeld reads the roles the permission store holds for the user in the scope, the
// user named as the store names a person: by the id of their account.
func rolesHeld(ctx context.Context, t *testing.T, s *served, user accesstypes.User, scope accesstypes.PolicyScope) []accesstypes.Role {
	t.Helper()

	held, err := s.access.UserManager().UserRoles(ctx, user, scope)
	if err != nil {
		t.Fatalf("UserRoles(%s) error = %v", user, err)
	}

	return held[scope]
}

// tenantRolesOf reads the roles the user holds in the tenant, by the username the
// tenant's list of the roles held shows them under, as it answers the browser b; nil
// when the user holds none there.
func tenantRolesOf(ctx context.Context, t *testing.T, b *browser, tenant, user string) []string {
	t.Helper()

	status, body := b.do(ctx, http.MethodGet, "/api/tenants/"+tenant+"/staff-tenant-user-roles?limit=200", nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s's roles held: status %d: %s", tenant, status, body)
	}
	var rows []struct {
		Username string `json:"username"`
		Role     string `json:"role"`
	}
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("the roles held are not a list: %v: %s", err, body)
	}
	var roles []string
	for _, row := range rows {
		if row.Username == user {
			roles = append(roles, row.Role)
		}
	}

	return roles
}

// signedIn returns a browser signed in as the development login, through the password
// and the second factor the auth's sign-in setting requires (browser.signIn).
func signedIn(ctx context.Context, t *testing.T, s *served, user string) *browser {
	t.Helper()

	b := newBrowser(t, s)
	b.signIn(ctx, user)

	return b
}
