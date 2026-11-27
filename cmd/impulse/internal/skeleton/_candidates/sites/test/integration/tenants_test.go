package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/accesstypes"
)

// TestTenants_aDeletedTenantTakesItsRolesAndInvitations proves a tenant that is deleted
// leaves nothing of itself behind, on either site. Disabled first, its delete asks with a
// count of its rows and of what the auth holds for it, and the confirmed request takes it
// all: the roles people held in it are removed, the invitations into it that still stood
// are withdrawn, and the accounts that belonged to it alone are deleted, in the
// transaction that deletes the row, so nothing is left for a later tenant to pick up. A
// person who holds a role elsewhere stays, and so does a person whom another tenant's
// directory manages. The tenant is made the way one comes to exist, by an invitation sent
// from the console and its first sign-in (makeTenant), so its founder is among the people
// who held roles in it alone. The tenant's directory and the rows of its own tables go
// with it in the same request: the directory through the record's generated delete, the
// rows in code, each through its resource's own delete.
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
	if _, err := s.db.Apply(ctx, []*spanner.Mutation{
		spanner.Insert("TenantDirectories", []string{"Id", "TenantId", "Method", "Connection"}, []any{"e0000000-0000-4000-8000-000000000011", north, "azure", northsConnection}),
		spanner.InsertMap("StaffSessionIdentities", map[string]any{
			"Id": "e0000000-0000-4000-8000-000000000012", "UserId": northerner, "Method": "azure", "Connection": northsConnection, "Subject": "subject-of-northerner",
			"CreatedAt": spanner.CommitTimestamp, "LastUsedAt": spanner.CommitTimestamp,
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
	// The sites share the roster and the auth, so the member stands in East on both.
	for site, b := range map[string]*browser{"console": member, "portal": portalMember} {
		if name := tenantNames(ctx, t, b)[east]; name != "East" {
			t.Fatalf("the member's tenant list on the %s once given a role in East names it %q, want East", site, name)
		}
	}
	if !accountExists(ctx, t, s, "easterner") {
		t.Fatal("the person of East alone has no account")
	}
	// The permission store holds a role in East for the three who were given one there,
	// under the id of the person's account, which is how it names anyone: the member's,
	// the founder's (the acceptance seated them) and the person's who holds one there alone.
	scope := accesstypes.DomainPolicyScope(accesstypes.Domain(east))
	type holder struct {
		name string
		user accesstypes.User
	}
	seated := []holder{
		{"the member", accesstypes.User(s.memberID)},
		{"the founder", accesstypes.User(founderID)},
		{"the person of East alone", accesstypes.User(easterner)},
		{"the person north's directory signs in", accesstypes.User(northerner)},
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

	// A tenant is disabled before it is deleted.
	setTenantDisabled(ctx, t, admin, east, true)

	// A tenant that has rows under it is not deleted by a request that does not say so:
	// the plain remove is answered with the question a person is asked first, and
	// nothing is gone. The question counts every kind of the tenant's rows and of what the
	// auth holds for it, for whoever may delete the tenant: the directory, the
	// announcement, the two accounts of East alone (its founder's and the easterner's),
	// the four roles held in it and the invitation.
	status, body = admin.do(ctx, http.MethodPatch, "/api/resources", []byte(`[{"op":"remove","path":"/tenants/`+east+`"}]`))
	if status != http.StatusPreconditionRequired || !strings.Contains(string(body), "Delete this tenant and everything under it: 1 announcement, 1 tenant directory, 2 accounts, 4 roles held, 1 invitation. This can't be undone.") {
		t.Fatalf("deleting the tenant without saying it takes what is under it: status %d, want 428 with the question: %s", status, body)
	}
	for _, table := range []string{"TenantDirectories", "Announcements"} {
		if n := rowsOf(ctx, t, s, table, east); n != 1 {
			t.Fatalf("%s holds %d row(s) of East after the question, want the one written: a delete that is asked the question deletes nothing", table, n)
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
	// the founder's, not the person's who held a role there alone.
	for _, h := range seated {
		if roles := rolesHeld(ctx, t, s, h.user, scope); len(roles) != 0 {
			t.Errorf("the roles the permission store holds for %s in the deleted tenant = %v, want none", h.name, roles)
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

// setTenantDisabled disables the tenant, or enables it again, through the record's
// update in the browser's session, and fails the test unless it answers 200.
func setTenantDisabled(ctx context.Context, t *testing.T, b *browser, tenant string, disabled bool) {
	t.Helper()

	body := fmt.Appendf(nil, `[{"op":"patch","path":"/tenants/%s","value":{"disabled":%t}}]`, tenant, disabled)
	if status, answer := b.do(ctx, http.MethodPatch, "/api/resources", body); status != http.StatusOK {
		t.Fatalf("setting the disabled field of tenant %s to %t: status %d, want 200: %s", tenant, disabled, status, answer)
	}
}
