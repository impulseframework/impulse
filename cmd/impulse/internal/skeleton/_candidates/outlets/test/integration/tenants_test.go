package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
)

// The tables each auth keeps its accounts in, as accountExists is asked about them.
const (
	staffUsers   = "StaffSessionUsers"
	membersUsers = "MembersSessionUsers"
)

// TestTenants_aDeletedTenantTakesItsRolesAndInvitations proves a tenant that is deleted
// leaves nothing of itself behind in either auth: the roles people held in it are
// removed, the staff auth's and the members auth's alike, and the invitations into it
// that still stood are withdrawn, in the transaction that deletes the row, so nothing is
// left for a later tenant to pick up; a person who held roles in it alone is deleted
// with it, by the rule on an account nothing admits, and a person who holds a role
// elsewhere stays. The tenant is created by its name alone, and the create path answers
// the id it generated.
func TestTenants_aDeletedTenantTakesItsRolesAndInvitations(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	admin := newBrowser(t, s, consoleAPI)
	admin.signIn(ctx, adminUser)
	member := newBrowser(t, s, consoleAPI)
	member.signIn(ctx, memberUser)

	east := createTenant(ctx, t, admin, "East")
	if _, err := impulse.UUIDFromString(east); err != nil {
		t.Fatalf("the new tenant's key is %q, want an id the create path generated: %v", east, err)
	}
	eastSegment := consoleAPI + "/tenants/" + east + "/announcements"

	// The staff auth: the member, north's administrator, is given a role in East; a
	// person is created in East who holds a role nowhere else; and an invitation into
	// East stands.
	if status, body := admin.do(ctx, http.MethodPost, consoleAPI+"/assign-staff-role", []byte(`{"userId":"`+s.memberID+`","role":"TenantAdministrator","domain":"`+east+`"}`)); status != http.StatusNoContent {
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
	if name := tenantNames(ctx, t, member)[east]; name != "East" {
		t.Fatalf("the member's tenant list once given a role in East names it %q, want East", name)
	}
	if !accountExists(ctx, t, s, staffUsers, "easterner") {
		t.Fatal("the person created in East has no account")
	}

	// The members auth: the portal's client, north's member, is given a role in East, and
	// a member holds a role in East alone. Members sign in through the directory and hold
	// no password, so the test writes the member's account to the auth's table and gives
	// the roles through the engine, each under the id of the member's account.
	newcomer, err := impulse.NewUUID()
	if err != nil {
		t.Fatalf("impulse.NewUUID() error = %v", err)
	}
	if _, err := s.db.Apply(ctx, []*spanner.Mutation{spanner.InsertMap(membersUsers, map[string]any{
		"Id": newcomer.String(), "Username": "eastern-member", "Email": "eastern-member@example.com", "CreatedAt": spanner.CommitTimestamp,
	})}); err != nil {
		t.Fatalf("writing the member's account: %v", err)
	}
	scope := accesstypes.DomainPolicyScope(accesstypes.Domain(east))
	for _, user := range []accesstypes.User{accesstypes.User(s.clientID), accesstypes.User(newcomer.String())} {
		if err := s.members.UserManager().AddUserRoles(ctx, scope, user, "TenantAdministrator"); err != nil {
			t.Fatalf("AddUserRoles(%s, East) in the members store error = %v", user, err)
		}
	}
	if !accountExists(ctx, t, s, membersUsers, "eastern-member") {
		t.Fatal("the member who holds a role in East alone has no account")
	}
	// Each permission store holds a role in East for those who were given one there,
	// under the id of the person's account, which is how it names anyone: the member's
	// and the created person's in the staff auth's, the client's and the member's in the
	// members auth's.
	type holder struct {
		name string
		user accesstypes.User
	}
	stores := []struct {
		auth   string
		engine *access.Client
		// seated are the people given a role in East; others, those the store is asked
		// about too once East is deleted.
		seated, others []holder
	}{
		{
			auth: "staff", engine: s.access,
			seated: []holder{
				{"the member", accesstypes.User(s.memberID)},
				{"the person created there", accesstypes.User(easterner.ID)},
			},
			others: []holder{{"the creator", accesstypes.User(s.adminID)}},
		},
		{
			auth: "members", engine: s.members,
			seated: []holder{
				{"the client", accesstypes.User(s.clientID)},
				{"the member who holds a role there alone", accesstypes.User(newcomer.String())},
			},
		},
	}
	for _, store := range stores {
		for _, h := range store.seated {
			if roles := rolesHeld(ctx, t, store.engine, h.user, scope); !slices.Contains(roles, "TenantAdministrator") {
				t.Fatalf("the roles the %s store holds for %s in East = %v, want TenantAdministrator", store.auth, h.name, roles)
			}
		}
	}

	if status, body := admin.do(ctx, http.MethodPatch, consoleAPI+"/resources", []byte(`[{"op":"remove","path":"/tenants/`+east+`"}]`)); status != http.StatusOK {
		t.Fatalf("deleting the tenant: status %d: %s", status, body)
	}

	// Gone at once, for the administrator and for the member alike.
	for name, b := range map[string]*browser{"the administrator": admin, "the member": member} {
		if status, body := b.do(ctx, http.MethodGet, eastSegment, nil); status != http.StatusNotFound {
			t.Errorf("GET %s as %s after the tenant is deleted: status %d, want 404: %s", eastSegment, name, status, body)
		}
	}
	if names := tenantNames(ctx, t, member); len(names) != 1 || names[north] != "North" {
		t.Errorf("the member's tenants after East is deleted = %v, want North alone", names)
	}

	// Neither permission store holds a role in the deleted tenant: not the member's, the
	// created person's or the creator's in the staff auth's, not the client's or the
	// member's in the members auth's.
	for _, store := range stores {
		for _, h := range slices.Concat(store.seated, store.others) {
			if roles := rolesHeld(ctx, t, store.engine, h.user, scope); len(roles) != 0 {
				t.Errorf("the roles the %s store holds for %s in the deleted tenant = %v, want none", store.auth, h.name, roles)
			}
		}
	}

	// The member holds north still and keeps the account, and the client keeps the role
	// in north; the person and the member who held roles in East alone are deleted with it.
	if !accountExists(ctx, t, s, staffUsers, memberUser) {
		t.Error("the member's account is gone, want it kept: the member holds a role in north")
	}
	if accountExists(ctx, t, s, staffUsers, "easterner") {
		t.Error("the account of the person who held roles in East alone still exists, want it deleted with the tenant")
	}
	if roles := rolesHeld(ctx, t, s.members, accesstypes.User(s.clientID), accesstypes.DomainPolicyScope(north)); len(roles) != 1 {
		t.Errorf("the roles the members store holds for the client in north = %v, want the one held there", roles)
	}
	if accountExists(ctx, t, s, membersUsers, "eastern-member") {
		t.Error("the account of the member who held roles in East alone still exists, want it deleted with the tenant")
	}

	// The invitation into East is withdrawn: its link is consumed.
	status, body = admin.do(ctx, http.MethodGet, consoleAPI+"/staff-session-mail-links/"+invited.ID, nil)
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

// rolesHeld reads the roles the permission store behind engine holds for the user in the
// scope, the user named as a store names a person: by the id of their account.
func rolesHeld(ctx context.Context, t *testing.T, engine *access.Client, user accesstypes.User, scope accesstypes.PolicyScope) []accesstypes.Role {
	t.Helper()

	held, err := engine.UserManager().UserRoles(ctx, user, scope)
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
	status, body := b.do(ctx, http.MethodPatch, consoleAPI+"/resources", add)
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
// the outlet the browser is signed in to: each tenant's name by its id.
func tenantNames(ctx context.Context, t *testing.T, b *browser) map[string]string {
	t.Helper()

	status, body := b.do(ctx, http.MethodGet, b.prefix+"/user-domains", nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s/user-domains: status %d: %s", b.prefix, status, body)
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

// accountExists reports whether an auth holds a user under the username: users is the
// table the auth keeps its accounts in (staffUsers, membersUsers).
func accountExists(ctx context.Context, t *testing.T, s *served, users, user string) bool {
	t.Helper()

	iter := s.db.Client.Single().Query(ctx, spanner.Statement{
		SQL:    "SELECT COUNT(*) FROM " + users + " WHERE Username = @user",
		Params: map[string]any{"user": user},
	})
	defer iter.Stop()
	row, err := iter.Next()
	if err != nil {
		t.Fatalf("counting the accounts named %s in %s: %v", user, users, err)
	}
	var n int64
	if err := row.Columns(&n); err != nil {
		t.Fatalf("reading the count of the accounts named %s in %s: %v", user, users, err)
	}

	return n > 0
}
