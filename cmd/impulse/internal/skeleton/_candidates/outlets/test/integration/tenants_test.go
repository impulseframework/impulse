package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

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
// elsewhere stays. The tenant is made the way one comes to exist, by an invitation's
// first sign-in (makeTenant), so its founder is among the people who held roles in it
// alone. The tenant's directory and the rows of its own tables go with it in the same
// request: the directory through the record's generated delete, the rows by the delete
// rule on each table's key to the tenant table.
func TestTenants_aDeletedTenantTakesItsRolesAndInvitations(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	admin := newBrowser(t, s, consoleAPI)
	admin.signIn(ctx, adminUser)
	member := newBrowser(t, s, consoleAPI)
	member.signIn(ctx, memberUser)

	east, _ := makeTenant(ctx, t, s, admin, "East", "founder")
	founderID := staffAccountID(ctx, t, admin, "founder")
	eastSegment := consoleAPI + "/tenants/" + east + "/announcements"

	// The staff auth: the member, north's administrator, is given a role in East; so is
	// a person who holds a role nowhere else; and an invitation into East stands.
	if status, body := admin.do(ctx, http.MethodPost, consoleAPI+"/assign-staff-role", []byte(`{"userId":"`+s.memberID+`","role":"TenantAdministrator","domain":"`+east+`"}`)); status != http.StatusNoContent {
		t.Fatalf("giving the member a role in East: status %d: %s", status, body)
	}
	easterner := staffAccount(ctx, t, s, "easterner", "Easterner")
	if status, body := admin.do(ctx, http.MethodPost, consoleAPI+"/assign-staff-role", []byte(`{"userId":"`+easterner+`","role":"TenantAdministrator","domain":"`+east+`"}`)); status != http.StatusNoContent {
		t.Fatalf("giving a person with no other role one in East: status %d: %s", status, body)
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
	if name := tenantNames(ctx, t, member)[east]; name != "East" {
		t.Fatalf("the member's tenant list once given a role in East names it %q, want East", name)
	}
	if !accountExists(ctx, t, s, staffUsers, "easterner") {
		t.Fatal("the person of East alone has no account")
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
	// under the id of the person's account, which is how it names anyone: the member's,
	// the founder's (the acceptance seated them) and the person's of East alone in the
	// staff auth's, the client's and the member's in the members auth's.
	type holder struct {
		name string
		user accesstypes.User
	}
	stores := []struct {
		auth   string
		engine *access.Client
		// seated are the people given a role in East.
		seated []holder
	}{
		{
			auth: "staff", engine: s.access,
			seated: []holder{
				{"the member", accesstypes.User(s.memberID)},
				{"the founder", accesstypes.User(founderID)},
				{"the person of East alone", accesstypes.User(easterner)},
			},
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

	// East holds a directory and rows of its own by now, written here as rows: an announcement, a reading and the directory of an organization.
	// The one delete takes them with the tenant, once it says so.
	if _, err := s.db.Apply(ctx, []*spanner.Mutation{
		spanner.Insert("TenantDirectories", []string{"Id", "TenantId", "Method", "Connection"}, []any{"e0000000-0000-4000-8000-000000000001", east, "azure", "6f2c1b7e-0000-4000-8000-000000000001"}),
		spanner.Insert("Announcements", []string{"Id", "TenantId", "Title", "Body"}, []any{"e0000000-0000-4000-8000-000000000002", east, "Easterly", "The last notice."}),
		spanner.Insert("Readings", []string{"Id", "TenantId", "Source", "Value", "RecordedAt"}, []any{"e0000000-0000-4000-8000-000000000003", east, "gauge", 1.5, time.Now()}),
	}); err != nil {
		t.Fatalf("writing East's directory and its rows: %v", err)
	}

	// A tenant that has rows under it is not deleted by a request that does not say so:
	// the plain remove is answered with the question a person is asked first, and
	// nothing is gone. The question counts each kind the person may list in the tenant
	// being deleted. The administrator holds the tenant's administrator role in every
	// tenant, which lists the directory and the announcement but not the reading.
	status, body = admin.do(ctx, http.MethodPatch, consoleAPI+"/resources", []byte(`[{"op":"remove","path":"/tenants/`+east+`"}]`))
	if status != http.StatusPreconditionRequired || !strings.Contains(string(body), "Delete this tenant and everything under it: 1 tenant directory, 1 announcement, and records you can't see. This can't be undone.") {
		t.Fatalf("deleting the tenant without saying it takes what is under it: status %d, want 428 with the question: %s", status, body)
	}
	for _, table := range []string{"TenantDirectories", "Announcements", "Readings"} {
		if n := rowsOf(ctx, t, s, table, east); n != 1 {
			t.Fatalf("%s holds %d row(s) of East after the question, want the one written: a delete that is asked the question deletes nothing", table, n)
		}
	}
	if status, body := admin.do(ctx, http.MethodPatch, consoleAPI+"/resources", []byte(`[{"op":"remove","path":"/tenants/`+east+`","value":{"everythingUnder":true}}]`)); status != http.StatusOK {
		t.Fatalf("deleting the tenant: status %d: %s", status, body)
	}
	for _, table := range []string{"TenantDirectories", "Announcements", "Readings"} {
		if n := rowsOf(ctx, t, s, table, east); n != 0 {
			t.Errorf("%s holds %d row(s) of the deleted tenant, want none: a tenant's directory and its rows go with it", table, n)
		}
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
	// founder's or the person's of East alone in the staff auth's, not the client's or
	// the member's in the members auth's.
	for _, store := range stores {
		for _, h := range store.seated {
			if roles := rolesHeld(ctx, t, store.engine, h.user, scope); len(roles) != 0 {
				t.Errorf("the roles the %s store holds for %s in the deleted tenant = %v, want none", store.auth, h.name, roles)
			}
		}
	}

	// The member holds north still and keeps the account, and the client keeps the role
	// in north; the founder, the person and the member who held roles in East alone are
	// deleted with it.
	if !accountExists(ctx, t, s, staffUsers, memberUser) {
		t.Error("the member's account is gone, want it kept: the member holds a role in north")
	}
	for _, username := range []string{"founder", "easterner"} {
		if accountExists(ctx, t, s, staffUsers, username) {
			t.Errorf("the account of %s, who held roles in East alone, still exists, want it deleted with the tenant", username)
		}
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
