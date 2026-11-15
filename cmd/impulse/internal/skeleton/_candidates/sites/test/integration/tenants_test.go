package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
)

// TestTenants_aDeletedTenantTakesItsRolesAndInvitations proves a tenant that is deleted
// leaves nothing of itself behind, on either site: the roles people held in it are
// removed and the invitations into it that still stood are withdrawn, in the transaction
// that deletes the row, so nothing is left for a later tenant to pick up; a person who
// held roles in it alone is deleted with it, by the rule on an account nothing admits,
// and a person who holds a role elsewhere stays. The tenant is created on the console by
// its name alone, and the create path answers the id it generated.
func TestTenants_aDeletedTenantTakesItsRolesAndInvitations(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	admin := signedIn(ctx, t, s, s.console, adminUser)
	member := signedIn(ctx, t, s, s.console, memberUser)
	portalMember := signedIn(ctx, t, s, s.portal, memberUser)

	east := createTenant(ctx, t, admin, "East")
	if _, err := impulse.UUIDFromString(east); err != nil {
		t.Fatalf("the new tenant's key is %q, want an id the create path generated: %v", east, err)
	}
	eastSegment := "/api/tenants/" + east + "/announcements"

	// The member, north's administrator, is given a role in East; a person is created in
	// East who holds a role nowhere else; and an invitation into East stands.
	if status, body := admin.do(ctx, http.MethodPost, "/api/assign-staff-role", []byte(`{"userId":"`+s.memberID+`","role":"TenantAdministrator","domain":"`+east+`"}`)); status != http.StatusNoContent {
		t.Fatalf("giving the member a role in East: status %d: %s", status, body)
	}
	status, body := tenantPost(ctx, admin, east, "create-staff-tenant-user", map[string]any{"username": "easterner", "password": "a-temporary-password", "roles": []string{"TenantAdministrator"}})
	if status != http.StatusOK {
		t.Fatalf("creating a person in East: status %d: %s", status, body)
	}
	var easterner struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &easterner); err != nil || easterner.ID == "" {
		t.Fatalf("creating a person in East answered %s (%v), want the new account's id", body, err)
	}
	status, body = tenantPost(ctx, admin, east, "invite-staff-tenant-user", map[string]any{"email": "invited@example.com", "roles": []string{"TenantAdministrator"}})
	if status != http.StatusOK {
		t.Fatalf("inviting into East: status %d: %s", status, body)
	}
	var invited struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &invited); err != nil || invited.ID == "" {
		t.Fatalf("the invitation answered %s (%v), want its id", body, err)
	}
	// The sites share the roster and the auth, so the member stands in East on both.
	for site, b := range map[string]*browser{"console": member, "portal": portalMember} {
		if name := tenantNames(ctx, t, b)[east]; name != "East" {
			t.Fatalf("the member's tenant list on the %s once given a role in East names it %q, want East", site, name)
		}
	}
	if !accountExists(ctx, t, s, "easterner") {
		t.Fatal("the person created in East has no account")
	}
	// The permission store holds a role in East for the two who were given one there,
	// under the id of the person's account, which is how it names anyone: the member's
	// and the person's who was created there.
	scope := accesstypes.DomainPolicyScope(accesstypes.Domain(east))
	type holder struct {
		name string
		user accesstypes.User
	}
	seated := []holder{
		{"the member", accesstypes.User(s.memberID)},
		{"the person created there", accesstypes.User(easterner.ID)},
	}
	for _, h := range seated {
		if roles := rolesHeld(ctx, t, s, h.user, scope); !slices.Contains(roles, "TenantAdministrator") {
			t.Fatalf("the roles the permission store holds for %s in East = %v, want TenantAdministrator", h.name, roles)
		}
	}

	if status, body := admin.do(ctx, http.MethodPatch, "/api/resources", []byte(`[{"op":"remove","path":"/tenants/`+east+`"}]`)); status != http.StatusOK {
		t.Fatalf("deleting the tenant: status %d: %s", status, body)
	}

	// Gone at once, for the administrator and for the member alike, on the portal as on
	// the console.
	for name, b := range map[string]*browser{"the administrator": admin, "the member": member, "the member on the portal": portalMember} {
		if status, body := b.do(ctx, http.MethodGet, eastSegment, nil); status != http.StatusNotFound {
			t.Errorf("GET %s as %s after the tenant is deleted: status %d, want 404: %s", eastSegment, name, status, body)
		}
	}
	for site, b := range map[string]*browser{"console": member, "portal": portalMember} {
		if names := tenantNames(ctx, t, b); len(names) != 1 || names[north] != "North" {
			t.Errorf("the member's tenants on the %s after East is deleted = %v, want North alone", site, names)
		}
	}

	// The permission store holds no role in the deleted tenant: not the member's, not
	// the person's who was created there, not the creator's.
	for _, h := range append(seated, holder{"the creator", accesstypes.User(s.adminID)}) {
		if roles := rolesHeld(ctx, t, s, h.user, scope); len(roles) != 0 {
			t.Errorf("the roles the permission store holds for %s in the deleted tenant = %v, want none", h.name, roles)
		}
	}
	// The member holds north still and keeps the account; the person who held roles in
	// East alone is deleted with it.
	if !accountExists(ctx, t, s, memberUser) {
		t.Error("the member's account is gone, want it kept: the member holds a role in north")
	}
	if accountExists(ctx, t, s, "easterner") {
		t.Error("the account of the person who held roles in East alone still exists, want it deleted with the tenant")
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

// createTenant creates a tenant by its name through the record's create path, as the
// browser b on the console, and answers the id the create path generated for it.
func createTenant(ctx context.Context, t *testing.T, b *browser, name string) string {
	t.Helper()

	add, err := json.Marshal([]map[string]any{{"op": "add", "path": "/tenants", "value": map[string]string{"name": name}}})
	if err != nil {
		t.Fatal(err)
	}
	status, body := b.do(ctx, http.MethodPatch, "/api/resources", add)
	if status != http.StatusOK {
		t.Fatalf("creating the tenant %s: status %d: %s", name, status, body)
	}
	var created struct {
		Tenants []string `json:"tenants"`
	}
	if err := json.Unmarshal(body, &created); err != nil || len(created.Tenants) != 1 {
		t.Fatalf("creating the tenant %s answered %s (%v), want the one new id", name, body, err)
	}

	return created.Tenants[0]
}

// tenantNames reads the browser's tenant list from the generated user-domains route of
// the site the browser is signed in to: each tenant's name by its id.
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

	iter := s.db.Client.Single().Query(ctx, spanner.Statement{
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
