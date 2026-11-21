package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/accesstypes"
)

// TestTenants_aDeletedTenantTakesItsRolesAndInvitations proves a tenant that is deleted
// leaves nothing of itself behind, on either site: the roles people held in it are
// removed and the invitations into it that still stood are withdrawn, in the transaction
// that deletes the row, so nothing is left for a later tenant to pick up; a person who
// held roles in it alone is deleted with it, by the rule on an account nothing admits,
// and a person who holds a role elsewhere stays. The tenant is made the way one comes to
// exist, by an invitation sent from the console and its first sign-in (makeTenant), so
// its founder is among the people who held roles in it alone. The tenant's directory and
// the rows of its own tables go with it in the same request: the directory through the
// record's generated delete, the rows by the delete rule on the table's key to the
// tenant table.
func TestTenants_aDeletedTenantTakesItsRolesAndInvitations(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	admin := signedIn(ctx, t, s, s.console, adminUser)
	member := signedIn(ctx, t, s, s.console, memberUser)
	portalMember := signedIn(ctx, t, s, s.portal, memberUser)

	east, _ := makeTenant(ctx, t, s, admin, "East", "founder")
	founderID := staffAccountID(ctx, t, admin, "founder")
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
	// The permission store holds a role in East for the three who were given one there,
	// under the id of the person's account, which is how it names anyone: the member's,
	// the founder's (the acceptance seated them) and the person's who was created there.
	scope := accesstypes.DomainPolicyScope(accesstypes.Domain(east))
	type holder struct {
		name string
		user accesstypes.User
	}
	seated := []holder{
		{"the member", accesstypes.User(s.memberID)},
		{"the founder", accesstypes.User(founderID)},
		{"the person created there", accesstypes.User(easterner.ID)},
	}
	for _, h := range seated {
		if roles := rolesHeld(ctx, t, s, h.user, scope); !slices.Contains(roles, "TenantAdministrator") {
			t.Fatalf("the roles the permission store holds for %s in East = %v, want TenantAdministrator", h.name, roles)
		}
	}

	// East holds a directory and rows of its own by now, written here as rows: an announcement and the directory of an organization.
	// The one delete takes them with the tenant, once it says so.
	if _, err := s.db.Apply(ctx, []*spanner.Mutation{
		spanner.Insert("TenantDirectories", []string{"Id", "TenantId", "Method", "Connection"}, []any{"e0000000-0000-4000-8000-000000000001", east, "azure", "6f2c1b7e-0000-4000-8000-000000000001"}),
		spanner.Insert("Announcements", []string{"Id", "TenantId", "KindId", "Title", "Body"}, []any{"e0000000-0000-4000-8000-000000000002", east, "notice", "Easterly", "The last notice."}),
	}); err != nil {
		t.Fatalf("writing East's directory and its rows: %v", err)
	}

	// A tenant that has rows under it is not deleted by a request that does not say so:
	// the plain remove is answered with the question a person is asked first, and
	// nothing is gone. The question counts the kinds the person may list in the scope
	// the delete is asked in, and a tenant's delete is asked for the whole application,
	// where the administrator's roles list none of a tenant's kinds: it says
	// "everything under it" and no count.
	status, body = admin.do(ctx, http.MethodPatch, "/api/resources", []byte(`[{"op":"remove","path":"/tenants/`+east+`"}]`))
	if status != http.StatusPreconditionRequired || !strings.Contains(string(body), "Delete this tenant and everything under it? This can't be undone.") {
		t.Fatalf("deleting the tenant without saying it takes what is under it: status %d, want 428 with the question: %s", status, body)
	}
	for _, table := range []string{"TenantDirectories", "Announcements"} {
		if n := rowsOf(ctx, t, s, table, east); n != 1 {
			t.Fatalf("%s holds %d row(s) of East after the question, want the one written: a delete that is asked the question deletes nothing", table, n)
		}
	}
	if status, body := admin.do(ctx, http.MethodPatch, "/api/resources", []byte(`[{"op":"remove","path":"/tenants/`+east+`","value":{"everythingUnder":true}}]`)); status != http.StatusOK {
		t.Fatalf("deleting the tenant: status %d: %s", status, body)
	}
	for _, table := range []string{"TenantDirectories", "Announcements"} {
		if n := rowsOf(ctx, t, s, table, east); n != 0 {
			t.Errorf("%s holds %d row(s) of the deleted tenant, want none: a tenant's directory and its rows go with it", table, n)
		}
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
	// the founder's, not the person's who was created there.
	for _, h := range seated {
		if roles := rolesHeld(ctx, t, s, h.user, scope); len(roles) != 0 {
			t.Errorf("the roles the permission store holds for %s in the deleted tenant = %v, want none", h.name, roles)
		}
	}
	// The member holds north still and keeps the account; the people who held roles in
	// East alone, its founder and the person created there, are deleted with it.
	if !accountExists(ctx, t, s, memberUser) {
		t.Error("the member's account is gone, want it kept: the member holds a role in north")
	}
	for _, username := range []string{"founder", "easterner"} {
		if accountExists(ctx, t, s, username) {
			t.Errorf("the account of %s, who held roles in East alone, still exists, want it deleted with the tenant", username)
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

// rowsOf counts the rows of a tenant-scoped table that name the tenant, as committed.
func rowsOf(ctx context.Context, t *testing.T, s *served, table, tenant string) int64 {
	t.Helper()

	iter := s.db.Client.Single().Query(ctx, spanner.Statement{
		SQL:    "SELECT COUNT(*) FROM " + table + " WHERE TenantId = @tenant",
		Params: map[string]any{"tenant": tenant},
	})
	defer iter.Stop()
	row, err := iter.Next()
	if err != nil {
		t.Fatalf("counting the rows of %s in %s: %v", table, tenant, err)
	}
	var n int64
	if err := row.Columns(&n); err != nil {
		t.Fatalf("reading the count of %s in %s: %v", table, tenant, err)
	}

	return n
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
