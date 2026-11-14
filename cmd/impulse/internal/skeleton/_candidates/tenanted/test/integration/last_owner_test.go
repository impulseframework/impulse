package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// TestLastOwner_anAdministratorLeavesOnceAnotherRemains proves the last-owner rule on
// the served stack: a person gives up an administrator role once another holder remains
// where it is held, and the last one is refused. The administrator, the one holder of
// Administrator, the application's administrator role (staff.AdministratorRoles), is
// refused giving it up in the rule's words; a role that is no administrator role where
// it is held goes freely, TenantAdministrator held in every tenant; in a tenant they
// created, where the create path made them its administrator, they are refused giving
// the role up as its last holder and give it up once they hold it in every tenant again;
// and once the member holds Administrator beside them, they give that up too. So an
// operator creates a customer's tenant, makes its first administrator and leaves, and
// neither the application nor a tenant is left with nobody to give the next role.
func TestLastOwner_anAdministratorLeavesOnceAnotherRemains(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	admin := signedIn(ctx, t, s, adminUser)
	adminID := staffUserID(ctx, t, admin, adminUser)

	// The last holder of the application's administrator role is refused, in the rule's words.
	status, body := admin.do(ctx, http.MethodPost, "/api/remove-staff-role", roleOf(adminUser, "Administrator", false))
	if status != http.StatusForbidden || !strings.Contains(string(body), "you are the last administrator: give another person the role before you remove your own") {
		t.Fatalf("the administrator giving up Administrator as its one holder: status %d, want 403 naming the rule: %s", status, body)
	}
	if roles := globalRolesOf(ctx, t, admin, adminUser); !slices.Contains(roles, "Administrator") {
		t.Fatalf("the administrator's roles after the refusal = %v, want Administrator kept", roles)
	}

	// A role that is no administrator role where it is held goes freely:
	// TenantAdministrator held in every tenant is no role of the whole application.
	if status, body := admin.do(ctx, http.MethodPost, "/api/remove-staff-role", roleOf(adminUser, "TenantAdministrator", true)); status != http.StatusNoContent {
		t.Fatalf("the administrator giving up TenantAdministrator held in every tenant: status %d, want 204: %s", status, body)
	}

	// In a tenant: the create path made the creator west's administrator, and with no
	// holder in every tenant left, they are its last one, refused in the tenant's words;
	// holding the role in every tenant again, they give up west's own.
	const west = "west"
	if status, body := admin.do(ctx, http.MethodPatch, "/api/resources", []byte(`[{"op":"add","path":"/tenants/`+west+`","value":{"name":"West"}}]`)); status != http.StatusOK {
		t.Fatalf("creating %s: status %d: %s", west, status, body)
	}
	if roles := tenantRolesOf(ctx, t, admin, west, adminUser); !slices.Contains(roles, "TenantAdministrator") {
		t.Fatalf("the administrator's roles in %s = %v, want TenantAdministrator: the create path gives the creator the role", west, roles)
	}
	westRole := "/api/tenants/" + west + "/remove-staff-tenant-role"
	status, body = admin.do(ctx, http.MethodPost, westRole, tenantRoleOf(adminID, "TenantAdministrator"))
	if status != http.StatusForbidden || !strings.Contains(string(body), "you are the last administrator of "+west+": give another person the role before you remove your own") {
		t.Fatalf("the administrator giving up %s's role as its one holder: status %d, want 403 naming the rule: %s", west, status, body)
	}
	if status, body := admin.do(ctx, http.MethodPost, "/api/assign-staff-role", roleOf(adminUser, "TenantAdministrator", true)); status != http.StatusNoContent {
		t.Fatalf("the administrator taking TenantAdministrator in every tenant again: status %d: %s", status, body)
	}
	if status, body := admin.do(ctx, http.MethodPost, westRole, tenantRoleOf(adminID, "TenantAdministrator")); status != http.StatusNoContent {
		t.Fatalf("the administrator giving up %s's role while holding it in every tenant: status %d, want 204: %s", west, status, body)
	}

	// Another holder given the role, the administrator gives it up.
	if status, body := admin.do(ctx, http.MethodPost, "/api/assign-staff-role", roleOf(memberUser, "Administrator", false)); status != http.StatusNoContent {
		t.Fatalf("giving the member Administrator: status %d: %s", status, body)
	}
	if status, body := admin.do(ctx, http.MethodPost, "/api/remove-staff-role", roleOf(adminUser, "Administrator", false)); status != http.StatusNoContent {
		t.Fatalf("the administrator giving up Administrator once the member holds it: status %d, want 204: %s", status, body)
	}
	member := signedIn(ctx, t, s, memberUser)
	if roles := globalRolesOf(ctx, t, member, adminUser); slices.Contains(roles, "Administrator") {
		t.Errorf("the administrator's roles once they gave it up = %v, want Administrator gone", roles)
	}
}

// roleOf is the body of the methods that give and take a role for the whole application:
// the person by name and the role, held for the whole application or, with everyTenant,
// in every tenant.
func roleOf(username, role string, everyTenant bool) []byte {
	body, err := json.Marshal(map[string]any{"username": username, "role": role, "everyDomain": everyTenant})
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

// globalRolesOf reads the roles the user holds for the whole application, from the list
// of the roles held as it answers the browser b.
func globalRolesOf(ctx context.Context, t *testing.T, b *browser, username string) []string {
	t.Helper()

	status, body := b.do(ctx, http.MethodGet, "/api/staff-user-roles?limit=200", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /api/staff-user-roles: status %d: %s", status, body)
	}
	var rows []struct {
		Kind string `json:"kind"`
		Role string `json:"role"`
		User string `json:"user"`
	}
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("the roles held are not a list: %v: %s", err, body)
	}
	var roles []string
	for _, row := range rows {
		if row.User == username && row.Kind == "global" {
			roles = append(roles, row.Role)
		}
	}

	return roles
}
