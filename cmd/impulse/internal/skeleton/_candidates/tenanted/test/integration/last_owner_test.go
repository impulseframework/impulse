package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestLastOwner_anAdministratorLeavesOnceAnotherRemains proves the last-owner rules on
// the served stack: the application always has a super administrator who can sign in, and
// a tenant an administrator of its own. The administrator, the one holder of the Super
// Administrator role, which the framework defines in every auth, is refused giving it up
// in the rule's words, and still refused while another person holds Administrator alone,
// the role file's own role, which makes nobody a super administrator; a role of the
// whole application that is not that one goes freely, since a super administrator gives
// it back, as does TenantAdministrator held in every tenant; in a tenant an invitation
// made, whose first sign-in made the invited person its administrator, that person is
// refused giving the role up as its last holder, and still refused while the
// administrator holds it in every tenant, which makes nobody an administrator of the
// tenant's own, and gives it up once the member holds it in the tenant too; and once the
// member is a super administrator beside them, the administrator gives that up too. So an
// operator brings a customer's tenant in
// by inviting its first administrator and leaves, and neither the application nor a
// tenant is left with nobody to give the next role. Every
// method names its person by the id of their account, and the rule knows the caller by
// theirs.
func TestLastOwner_anAdministratorLeavesOnceAnotherRemains(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	admin := signedIn(ctx, t, s, adminUser)
	adminID := staffUserID(ctx, t, admin, adminUser)
	const lastSuperAdministrator = "You are the last super administrator who can sign in: give another person the Super Administrator role before you remove your own"

	// The last holder of the Super Administrator role is refused, in the rule's words.
	status, body := admin.do(ctx, http.MethodPost, "/api/remove-staff-role", roleOf(adminID, "SuperAdministrator", false))
	if status != http.StatusForbidden || !strings.Contains(string(body), lastSuperAdministrator) {
		t.Fatalf("the administrator giving up the Super Administrator role as its one holder: status %d, want 403 naming the rule: %s", status, body)
	}
	if roles := globalRolesOf(ctx, t, admin, adminID); !slices.Contains(roles, "SuperAdministrator") {
		t.Fatalf("the administrator's roles after the refusal = %v, want SuperAdministrator kept", roles)
	}

	// A person who holds Administrator alone manages the application's people by the
	// role file and is no super administrator: with the member holding it, the
	// administrator is still the last one and is refused as before.
	if status, body := admin.do(ctx, http.MethodPost, "/api/assign-staff-role", roleOf(s.memberID, "Administrator", false)); status != http.StatusNoContent {
		t.Fatalf("giving the member Administrator: status %d: %s", status, body)
	}
	status, body = admin.do(ctx, http.MethodPost, "/api/remove-staff-role", roleOf(adminID, "SuperAdministrator", false))
	if status != http.StatusForbidden || !strings.Contains(string(body), lastSuperAdministrator) {
		t.Fatalf("the administrator giving up the Super Administrator role while the member holds Administrator alone: status %d, want 403 naming the rule: %s", status, body)
	}
	if roles := globalRolesOf(ctx, t, admin, adminID); !slices.Contains(roles, "SuperAdministrator") {
		t.Fatalf("the administrator's roles after the second refusal = %v, want SuperAdministrator kept", roles)
	}

	// Any other role of the whole application goes freely, the last holder's included:
	// a super administrator gives it back.
	if status, body := admin.do(ctx, http.MethodPost, "/api/remove-staff-role", roleOf(adminID, "FeatureAdministrator", false)); status != http.StatusNoContent {
		t.Fatalf("the administrator giving up FeatureAdministrator as its one holder: status %d, want 204: %s", status, body)
	}
	if status, body := admin.do(ctx, http.MethodPost, "/api/assign-staff-role", roleOf(adminID, "FeatureAdministrator", false)); status != http.StatusNoContent {
		t.Fatalf("the administrator taking FeatureAdministrator again: status %d, want 204: %s", status, body)
	}

	// A role that is no administrator role where it is held goes freely:
	// TenantAdministrator held in every tenant is no role of the whole application.
	if status, body := admin.do(ctx, http.MethodPost, "/api/remove-staff-role", roleOf(adminID, "TenantAdministrator", true)); status != http.StatusNoContent {
		t.Fatalf("the administrator giving up TenantAdministrator held in every tenant: status %d, want 204: %s", status, body)
	}

	// In a tenant: the invitation's first sign-in made the invited person west's
	// administrator, and they are its last one, refused in the tenant's words. The
	// administrator holding the role in every tenant again changes nothing: a holder in
	// every tenant is no administrator of the tenant's own, the one a tenant that admits
	// its people through its directory only still lets in. Once the member holds the
	// role in west itself, the founder gives up their own.
	west, founder := makeTenant(ctx, t, s, admin, "West", "founder")
	founderID := staffUserID(ctx, t, admin, "founder")
	westRole := "/api/tenants/" + west + "/remove-staff-tenant-role"
	const lastAdministrator = "you are the last administrator of this tenant: give another person the role before you remove your own"
	status, body = founder.do(ctx, http.MethodPost, westRole, tenantRoleOf(founderID, "TenantAdministrator"))
	if status != http.StatusForbidden || !strings.Contains(string(body), lastAdministrator) {
		t.Fatalf("the founder giving up West's role as its one holder: status %d, want 403 naming the rule: %s", status, body)
	}
	if status, body := admin.do(ctx, http.MethodPost, "/api/assign-staff-role", roleOf(adminID, "TenantAdministrator", true)); status != http.StatusNoContent {
		t.Fatalf("the administrator taking TenantAdministrator in every tenant again: status %d: %s", status, body)
	}
	status, body = founder.do(ctx, http.MethodPost, westRole, tenantRoleOf(founderID, "TenantAdministrator"))
	if status != http.StatusForbidden || !strings.Contains(string(body), lastAdministrator) {
		t.Fatalf("the founder giving up West's role while the administrator holds it in every tenant alone: status %d, want 403 naming the rule: %s", status, body)
	}
	if status, body := admin.do(ctx, http.MethodPost, "/api/assign-staff-role", []byte(`{"userId":"`+s.memberID+`","role":"TenantAdministrator","domain":"`+west+`"}`)); status != http.StatusNoContent {
		t.Fatalf("giving the member the role in West: status %d: %s", status, body)
	}
	if status, body := founder.do(ctx, http.MethodPost, westRole, tenantRoleOf(founderID, "TenantAdministrator")); status != http.StatusNoContent {
		t.Fatalf("the founder giving up West's role once the member holds it there: status %d, want 204: %s", status, body)
	}

	// Another holder given the role, the administrator gives it up.
	if status, body := admin.do(ctx, http.MethodPost, "/api/assign-staff-role", roleOf(s.memberID, "SuperAdministrator", false)); status != http.StatusNoContent {
		t.Fatalf("making the member a super administrator: status %d: %s", status, body)
	}
	if status, body := admin.do(ctx, http.MethodPost, "/api/remove-staff-role", roleOf(adminID, "SuperAdministrator", false)); status != http.StatusNoContent {
		t.Fatalf("the administrator giving up the Super Administrator role once the member holds it: status %d, want 204: %s", status, body)
	}
	member := signedIn(ctx, t, s, memberUser)
	if roles := globalRolesOf(ctx, t, member, adminID); slices.Contains(roles, "SuperAdministrator") {
		t.Errorf("the administrator's roles once they gave it up = %v, want SuperAdministrator gone", roles)
	}
}

// TestLastOwner_theLastAdministratorsOwnPeriod proves on the served stack that the rule
// on a tenant's last administrator judges the dates a person gives their own role as it
// judges the role's removal, and counts what is in force today and nothing else. The
// founder of a tenant an invitation made is its one administrator: they are refused a
// start that has not come and an end on their own role, in the words that refuse them
// its removal, since either would leave the tenant with no administrator of its own;
// once the member holds the role in the tenant too, the founder gives their own an end.
// And a role that has ended makes nobody an administrator: the administrator holds the
// role in a second tenant for a few seconds and takes it from that tenant's founder, and
// once it has ended removes their own, though nobody administers the tenant any more.
func TestLastOwner_theLastAdministratorsOwnPeriod(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	admin := signedIn(ctx, t, s, adminUser)
	adminID := staffUserID(ctx, t, admin, adminUser)
	west, founder := makeTenant(ctx, t, s, admin, "West", "founder")
	founderID := staffUserID(ctx, t, admin, "founder")
	const lastAdministrator = "you are the last administrator of this tenant: give another person the role before you remove your own"

	// The last administrator gives their own role a start a year ahead, which takes it
	// away today, and then an end an hour ahead, which takes it away then.
	now := time.Now().UTC().Truncate(time.Second)
	for name, period := range map[string]map[string]any{
		"a start that has not come": {"startsAt": now.Add(365 * 24 * time.Hour)},
		"an end":                    {"endsAt": now.Add(time.Hour)},
	} {
		given := map[string]any{"userId": founderID, "role": "TenantAdministrator"}
		for field, at := range period {
			given[field] = at
		}
		status, body := tenantPost(ctx, founder, west, "assign-staff-tenant-role", given)
		if status != http.StatusForbidden || !strings.Contains(string(body), lastAdministrator) {
			t.Fatalf("the founder giving their own role %s as West's one administrator: status %d, want 403 naming the rule: %s", name, status, body)
		}
	}
	if roles := tenantRolesOf(ctx, t, founder, west, "founder"); !slices.Contains(roles, "TenantAdministrator") {
		t.Fatalf("the founder's roles in West after the refusals = %v, want TenantAdministrator kept", roles)
	}

	// Another person administers West: the founder's own role takes an end.
	if status, body := admin.do(ctx, http.MethodPost, "/api/assign-staff-role", []byte(`{"userId":"`+s.memberID+`","role":"TenantAdministrator","domain":"`+west+`"}`)); status != http.StatusNoContent {
		t.Fatalf("giving the member the role in West: status %d: %s", status, body)
	}
	ending := map[string]any{"userId": founderID, "role": "TenantAdministrator", "endsAt": now.Add(time.Hour)}
	if status, body := tenantPost(ctx, founder, west, "assign-staff-tenant-role", ending); status != http.StatusNoContent {
		t.Fatalf("the founder giving their own role an end once the member holds it in West: status %d, want 204: %s", status, body)
	}

	// A role that has ended: the administrator holds East's for a few seconds and is
	// left its one holder, and removes it once it is over.
	east, _ := makeTenant(ctx, t, s, admin, "East", "eastfounder")
	eastFounderID := staffUserID(ctx, t, admin, "eastfounder")
	ends := time.Now().UTC().Add(3 * time.Second)
	brief, err := json.Marshal(map[string]any{"userId": adminID, "role": "TenantAdministrator", "domain": east, "endsAt": ends})
	if err != nil {
		t.Fatal(err)
	}
	if status, body := admin.do(ctx, http.MethodPost, "/api/assign-staff-role", brief); status != http.StatusNoContent {
		t.Fatalf("the administrator taking East's role for a few seconds: status %d: %s", status, body)
	}
	inEast := []byte(`{"userId":"` + eastFounderID + `","role":"TenantAdministrator","domain":"` + east + `"}`)
	if status, body := admin.do(ctx, http.MethodPost, "/api/remove-staff-role", inEast); status != http.StatusNoContent {
		t.Fatalf("the administrator taking East's role from its founder: status %d: %s", status, body)
	}
	// Nothing is written when a period is over: the role ends by the clock.
	if wait := time.Until(ends); wait > 0 {
		time.Sleep(wait + 200*time.Millisecond)
	}
	own := []byte(`{"userId":"` + adminID + `","role":"TenantAdministrator","domain":"` + east + `"}`)
	if status, body := admin.do(ctx, http.MethodPost, "/api/remove-staff-role", own); status != http.StatusNoContent {
		t.Fatalf("the administrator removing their own role in East once it has ended: status %d, want 204: %s", status, body)
	}
}

// roleOf is the body of the methods that give and take a role for the whole application:
// the person by the id of their account and the role, held for the whole application or,
// with everyTenant, in every tenant.
func roleOf(userID, role string, everyTenant bool) []byte {
	body, err := json.Marshal(map[string]any{"userId": userID, "role": role, "everyDomain": everyTenant})
	if err != nil {
		panic(err)
	}

	return body
}

// tenantRoleOf is the body of the methods that give and take a role in one tenant: the
// person by the id of their account and the role.
func tenantRoleOf(userID, role string) []byte {
	body, err := json.Marshal(map[string]string{"userId": userID, "role": role})
	if err != nil {
		panic(err)
	}

	return body
}

// staffUserID finds a staff member's id by username in the list of the auth's accounts,
// as the browser b (one who may list them) reads it.
func staffUserID(ctx context.Context, t *testing.T, b *browser, username string) string {
	t.Helper()

	status, body := b.do(ctx, http.MethodGet, "/api/staff-session-users?limit=200", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /api/staff-session-users: status %d: %s", status, body)
	}
	var rows []struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	}
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("the accounts are not a list: %v: %s", err, body)
	}
	for _, row := range rows {
		if row.Username == username {
			return row.ID
		}
	}
	t.Fatalf("no staff member %q", username)

	return ""
}

// globalRolesOf reads the roles the person holds for the whole application, from the
// list of the roles held as it answers the browser b: the list is the permission store's
// memberships, whose user is the id of the person's account.
func globalRolesOf(ctx context.Context, t *testing.T, b *browser, userID string) []string {
	t.Helper()

	status, body := b.do(ctx, http.MethodGet, "/api/staff-user-roles?limit=200", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /api/staff-user-roles: status %d: %s", status, body)
	}
	var rows []struct {
		Kind   string `json:"kind"`
		Role   string `json:"role"`
		UserID string `json:"userId"`
	}
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("the roles held are not a list: %v: %s", err, body)
	}
	var roles []string
	for _, row := range rows {
		if row.UserID == userID && row.Kind == "global" {
			roles = append(roles, row.Role)
		}
	}

	return roles
}
