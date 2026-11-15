package integration

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"testing"

	cloudspanner "cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
)

// TestTenants_createdThroughTheAPIAreUsableAtOnce proves the tenant roster follows the
// Tenant record's write paths without a restart, and that a tenant is known to people by
// its name while its key is an id nobody types. The administrator creates a tenant by
// its name alone, through the consolidated handler: the create path generates the id and
// answers it, the tenant is served under its segment at once (the generated guard asks
// the roster, which the commit updated), named in the administrator's tenant list (a
// domain role held in every domain is a foothold there too) and concealed from the
// member who holds nothing there. The create path seats the creator as the tenant's
// administrator (TenantAdministrator, the role TenantAdministratorRoles names) in the
// create's own transaction, so the tenant's own list of the roles held names them from
// the first request. A key in the path is refused; a second tenant of the same name is
// refused by the unique index on the name; and a new name reaches every tenant list at
// once.
func TestTenants_createdThroughTheAPIAreUsableAtOnce(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	admin := signedIn(ctx, t, s, adminUser)
	member := signedIn(ctx, t, s, memberUser)

	// Nobody names a tenant's key: an add that carries one in its path is refused, and
	// writes nothing.
	typed := `[{"op":"add","path":"/tenants/east","value":{"name":"East"}}]`
	if status, body := admin.do(ctx, http.MethodPatch, "/api/resources", []byte(typed)); status != http.StatusBadRequest {
		t.Fatalf("creating a tenant under a typed key: status %d, want 400: %s", status, body)
	}
	if names := tenantNames(ctx, t, admin); slices.Contains(slices.Collect(maps.Values(names)), "East") {
		t.Fatalf("the administrator's tenants after the refused create = %v, want no East", names)
	}

	east := createTenant(ctx, t, admin, "East")
	if _, err := impulse.UUIDFromString(east); err != nil {
		t.Fatalf("the new tenant's key is %q, want an id the create path generated: %v", east, err)
	}
	eastSegment := "/api/tenants/" + east + "/announcements"

	// The reads happen while the tenant exists, so they are rows of one test rather than
	// subtests, which would run after the delete below.
	reads := []struct {
		name       string
		browser    *browser
		wantStatus int
	}{
		{name: "the administrator reads the new tenant's segment at once", browser: admin, wantStatus: http.StatusOK},
		{name: "the member holds nothing there and is answered not-found", browser: member, wantStatus: http.StatusNotFound},
	}
	for _, read := range reads {
		if status, body := read.browser.do(ctx, http.MethodGet, eastSegment, nil); status != read.wantStatus {
			t.Errorf("%s: GET %s: status %d, want %d: %s", read.name, eastSegment, status, read.wantStatus, body)
		}
	}

	// The creator holds the tenant's administrator seat there: the create path seated
	// them in the transaction that wrote the row, and the tenant's list of the roles
	// held names them with it.
	if roles := tenantRolesOf(ctx, t, admin, east, adminUser); !slices.Contains(roles, "TenantAdministrator") {
		t.Errorf("the administrator's roles in East = %v, want TenantAdministrator among them: the create path seats the creator", roles)
	}

	// The administrator's tenant list names the new tenant by its name: the roster lists
	// it, and the domain role held in every domain is a foothold in it.
	if name := tenantNames(ctx, t, admin)[east]; name != "East" {
		t.Errorf("the administrator's tenant list names the new tenant %q, want East", name)
	}

	// A name is one tenant's: the unique index on the name refuses a second East.
	again := `[{"op":"add","path":"/tenants","value":{"name":"East"}}]`
	if status, body := admin.do(ctx, http.MethodPatch, "/api/resources", []byte(again)); status != http.StatusConflict {
		t.Errorf("creating a second tenant named East: status %d, want 409: %s", status, body)
	}

	// The name is a field like any other: changed, it is the name every tenant list
	// shows at once, and the tenant's key and segment are what they were.
	rename := `[{"op":"patch","path":"/tenants/` + east + `","value":{"name":"Far East"}}]`
	if status, body := admin.do(ctx, http.MethodPatch, "/api/resources", []byte(rename)); status != http.StatusOK {
		t.Fatalf("renaming the tenant: status %d: %s", status, body)
	}
	if name := tenantNames(ctx, t, admin)[east]; name != "Far East" {
		t.Errorf("the administrator's tenant list names the renamed tenant %q, want Far East", name)
	}
	if status, body := admin.do(ctx, http.MethodGet, eastSegment, nil); status != http.StatusOK {
		t.Errorf("GET %s after the rename: status %d, want 200: %s", eastSegment, status, body)
	}
}

// TestTenants_aDeletedTenantTakesItsRolesAndInvitations proves a tenant that is deleted
// leaves nothing of itself behind: the roles people held in it are removed and the
// invitations into it that still stood are withdrawn, in the transaction that deletes
// the row, so nothing is left for a later tenant to pick up; a person who held roles in
// it alone is deleted with it, by the rule on an account nothing admits, and a person
// who holds a role elsewhere stays.
func TestTenants_aDeletedTenantTakesItsRolesAndInvitations(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	admin := signedIn(ctx, t, s, adminUser)
	member := signedIn(ctx, t, s, memberUser)

	east := createTenant(ctx, t, admin, "East")
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
	if domains := userDomains(ctx, t, member); !slices.Contains(domains, east) {
		t.Fatalf("the member's tenants once given a role in East = %v, want East among them", domains)
	}
	if !accountExists(ctx, t, s, "easterner") {
		t.Fatal("the person created in East has no account")
	}
	// The permission store holds a role in East for each of the three, under the id of
	// the person's account, which is how it names anyone: the member's, the creator's
	// (the create path seated them) and the person's who was created there.
	scope := accesstypes.DomainPolicyScope(accesstypes.Domain(east))
	holders := []struct {
		name string
		user accesstypes.User
	}{
		{"the member", accesstypes.User(s.memberID)},
		{"the creator", accesstypes.User(s.adminID)},
		{"the person created there", accesstypes.User(easterner.ID)},
	}
	for _, holder := range holders {
		if roles := rolesHeld(ctx, t, s, holder.user, scope); !slices.Contains(roles, "TenantAdministrator") {
			t.Fatalf("the roles the permission store holds for %s in East = %v, want TenantAdministrator", holder.name, roles)
		}
	}

	if status, body := admin.do(ctx, http.MethodPatch, "/api/resources", []byte(`[{"op":"remove","path":"/tenants/`+east+`"}]`)); status != http.StatusOK {
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
	// the creator's, not the person's who was created there.
	for _, holder := range holders {
		if roles := rolesHeld(ctx, t, s, holder.user, scope); len(roles) != 0 {
			t.Errorf("the roles the permission store holds for %s in the deleted tenant = %v, want none", holder.name, roles)
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

// createTenant creates a tenant by its name through the record's create path, as the
// browser b, and answers the id the create path generated for it.
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
// and the second factor every session carries (browser.signIn).
func signedIn(ctx context.Context, t *testing.T, s *served, user string) *browser {
	t.Helper()

	b := newBrowser(t, s)
	b.signIn(ctx, user)

	return b
}
