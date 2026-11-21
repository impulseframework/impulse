//go:build skipAuth

package integration

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	cloudspanner "cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
)

// TestTenants_aDeletedTenantTakesItsDirectoryAndItsRows proves a tenant is deleted in
// one request whatever it holds. Ember is directory only from the start, made by an
// invitation accepted through the Workspace, which is its directory; it seats its
// founder, who is in no other tenant, and a commuter who is also seated in south; and
// it holds an announcement. The administrator's one delete takes the directory's row and
// the announcement with the tenant: nothing comes before it, neither turning directory
// only off nor removing the directory, which the tenant's own page asks for in that
// order when the tenant stays. The directory's removal leaves its change event, and
// another tenant's directory stands. The commuter signs in through the Workspace as
// before and reaches south, and the Workspace is free again: the commuter, south's
// administrator, makes it south's directory, which was refused while Ember had it.
//
// Under the session library's skipAuth build tag Google is simulated: the callback signs
// in whoever APP_GOOGLE_USERNAME names, from one Workspace. The test sets that
// variable, so it does not run in parallel.
func TestTenants_aDeletedTenantTakesItsDirectoryAndItsRows(t *testing.T) {
	ctx := t.Context()
	s := newServed(ctx, t)
	admin := signedIn(ctx, t, s, adminUser)

	// The commuter comes through the Workspace while it is no tenant's directory, so the
	// Google method, opened to sign-up, gives them an account; south seats them.
	if status, body := admin.do(ctx, http.MethodPost, "/api/open-staff-sign-up", []byte(`{"name":"google"}`)); status != http.StatusNoContent {
		t.Fatalf("POST /api/open-staff-sign-up for the Google method: status %d, want 204: %s", status, body)
	}
	const commuterUser = "commuter"
	t.Setenv("APP_GOOGLE_USERNAME", commuterUser)
	signedInThroughGoogle(ctx, t, s)

	// Ember: directory only, made by its founder's acceptance through the Workspace.
	const emberFounder = "ember-founder@example.com"
	if status, body, _ := inviteTenant(ctx, t, admin, map[string]any{"email": emberFounder, "tenantName": "Ember", "tenantDirectoryOnly": true}); status != http.StatusOK {
		t.Fatalf("the directory-only invitation: status %d: %s", status, body)
	}
	t.Setenv("APP_GOOGLE_USERNAME", emberFounder)
	founder := newBrowser(t, s)
	if status, page := showInvitation(ctx, t, founder, mailedInvitation(t, s, emberFounder)); status != http.StatusOK {
		t.Fatalf("opening the invitation: status %d: %v", status, page)
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
	if made == nil || made["directoryOnly"] != true {
		t.Fatalf("Ember as made = %v, want a tenant that is directory only", made)
	}
	ember, _ := made["id"].(string)
	workspace := sessionDirectory(ctx, t, founder)
	if workspace == nil {
		t.Fatal("the founder's session reports no directory, want the Workspace the sign-in came through")
	}
	if rows := directories(ctx, t, admin, ember); len(rows) != 1 || rows[0] != *workspace {
		t.Fatalf("Ember's directory = %+v, want the Workspace %+v its founder signed in through", rows, *workspace)
	}

	// Its people: the founder, seated in Ember alone, and the commuter, seated in Ember
	// and in south, who enters both through the Workspace.
	seat(ctx, t, s, commuterUser, south)
	seatInMadeTenant(ctx, t, s, commuterUser, accesstypes.Domain(ember))
	t.Setenv("APP_GOOGLE_USERNAME", commuterUser)
	commuter := signedInThroughGoogle(ctx, t, s)
	both := []string{south, ember}
	slices.Sort(both)
	if domains := userDomains(ctx, t, commuter); !slices.Equal(domains, both) {
		t.Fatalf("the commuter's tenants before the delete = %v, want Ember and south", domains)
	}

	// Its rows: an announcement.
	if status, body := admin.do(ctx, http.MethodPatch, "/api/resources", []byte(`[{"op":"add","path":"/tenants/`+ember+`/announcements","value":{"title":"Embers","body":"The last notice."}}]`)); status != http.StatusOK {
		t.Fatalf("posting an announcement in Ember: status %d: %s", status, body)
	}
	if n := rowsOf(ctx, t, s, "Announcements", ember); n != 1 {
		t.Fatalf("Ember holds %d announcement(s) before the delete, want 1", n)
	}

	// The Workspace is Ember's, so south is refused it; and north has a directory of its
	// own, written as a row, which the delete of Ember must leave alone.
	makeSouths := `[{"op":"add","path":"/tenants/` + south + `/tenant-directories","value":{}}]`
	if status, body := commuter.do(ctx, http.MethodPatch, "/api/resources", []byte(makeSouths)); status != http.StatusConflict || !strings.Contains(string(body), "already has a tenant here, and a directory belongs to one tenant") {
		t.Fatalf("making the Workspace south's directory while Ember has it: status %d, want 409 saying the organization has a tenant: %s", status, body)
	}
	northsDirectory, err := impulse.NewUUID()
	if err != nil {
		t.Fatalf("impulse.NewUUID() error = %v", err)
	}
	if _, err := s.db.Apply(ctx, []*cloudspanner.Mutation{
		cloudspanner.Insert("TenantDirectories", []string{"Id", "TenantId", "Method", "Connection"}, []any{northsDirectory.String(), north, "azure", "6f2c1b7e-0000-4000-8000-000000000001"}),
	}); err != nil {
		t.Fatalf("writing north's directory: %v", err)
	}
	before := changeEvents(ctx, t, s, "TenantDirectories")["TenantDirectories"]

	// A request that does not say it takes what is under the tenant is asked the
	// question, and nothing is gone. The question counts the kinds the person may list
	// in the scope the delete is asked in, and a tenant's delete is asked for the whole
	// application, where the administrator's roles list none of a tenant's kinds: it
	// says "everything under it" and no count.
	status, body := admin.do(ctx, http.MethodPatch, "/api/resources", []byte(`[{"op":"remove","path":"/tenants/`+ember+`"}]`))
	if status != http.StatusPreconditionRequired || !strings.Contains(string(body), "Delete this tenant and everything under it? This can't be undone.") {
		t.Fatalf("deleting Ember without saying it takes what is under it: status %d, want 428 with the question: %s", status, body)
	}
	if tenantNamed(ctx, t, admin, "Ember") == nil || rowsOf(ctx, t, s, "TenantDirectories", ember) != 1 || rowsOf(ctx, t, s, "Announcements", ember) != 1 {
		t.Fatal("Ember, its directory or its announcement is gone after the question: a delete that is asked the question deletes nothing")
	}
	if after := changeEvents(ctx, t, s, "TenantDirectories")["TenantDirectories"]; after != before {
		t.Errorf("change events of TenantDirectories: %d before the question and %d after, want no more", before, after)
	}

	// One request that says so, with nothing before it.
	if status, body := admin.do(ctx, http.MethodPatch, "/api/resources", []byte(`[{"op":"remove","path":"/tenants/`+ember+`","value":{"everythingUnder":true}}]`)); status != http.StatusOK {
		t.Fatalf("deleting Ember, directory only, with its directory, its people and an announcement: status %d, want 200: %s", status, body)
	}

	if tenantNamed(ctx, t, admin, "Ember") != nil {
		t.Error("Ember is still among the tenants after the delete")
	}
	if n := rowsOf(ctx, t, s, "TenantDirectories", ember); n != 0 {
		t.Errorf("Ember's directory after the delete: %d row(s), want none: the directory goes with its tenant", n)
	}
	if n := rowsOf(ctx, t, s, "Announcements", ember); n != 0 {
		t.Errorf("Ember's announcements after the delete: %d row(s), want none: a tenant's rows go with it", n)
	}
	if n := rowsOf(ctx, t, s, "TenantDirectories", north); n != 1 {
		t.Errorf("north's directory after Ember's delete: %d row(s), want the 1 it had", n)
	}
	if after := changeEvents(ctx, t, s, "TenantDirectories")["TenantDirectories"]; after != before+1 {
		t.Errorf("change events of TenantDirectories: %d before the delete and %d after, want one more: the directory's removal is in the audit", before, after)
	}
	// The founder held roles in Ember alone and goes with it; the commuter stays.
	if accountExists(ctx, t, s, emberFounder) {
		t.Error("the account of Ember's founder, who held roles in Ember alone, still exists, want it deleted with the tenant")
	}
	if !accountExists(ctx, t, s, commuterUser) {
		t.Fatal("the commuter's account is gone, want it kept: the commuter holds a role in south")
	}

	// The commuter signs in as before, through the Workspace, and reaches south.
	commuter = signedInThroughGoogle(ctx, t, s)
	if domains := userDomains(ctx, t, commuter); !slices.Equal(domains, []string{south}) {
		t.Errorf("the commuter's tenants after Ember is deleted = %v, want south alone", domains)
	}
	if status, body := commuter.do(ctx, http.MethodGet, "/api/tenants/"+south+"/announcements", nil); status != http.StatusOK {
		t.Errorf("GET south announcements as the commuter after Ember is deleted: status %d, want 200: %s", status, body)
	}

	// The Workspace is free for another tenant.
	if status, body := commuter.do(ctx, http.MethodPatch, "/api/resources", []byte(makeSouths)); status != http.StatusOK {
		t.Fatalf("making the Workspace south's directory once Ember is deleted: status %d, want 200: %s", status, body)
	}
	if rows := directories(ctx, t, admin, south); len(rows) != 1 || rows[0] != *workspace {
		t.Errorf("south's directory = %+v, want the Workspace %+v Ember had", rows, *workspace)
	}
}

// seatInMadeTenant makes the person an administrator of a tenant the test made, and
// waits until the engine's snapshot shows the grant: seat waits on the development
// tenants alone.
func seatInMadeTenant(ctx context.Context, t *testing.T, s *served, username string, tenant accesstypes.Domain) {
	t.Helper()

	user := accesstypes.User(accountID(ctx, t, s, username))
	if err := s.access.UserManager().AddUserRoles(ctx, accesstypes.DomainPolicyScope(tenant), user, "TenantAdministrator"); err != nil {
		t.Fatalf("AddUserRoles(%s, %s) error = %v", username, tenant, err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		has, err := s.access.UserHasGrants(ctx, user, accesstypes.DomainScope(tenant))
		if err == nil && has {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the grant of %s in %s never became visible in the policy snapshot (err %v)", username, tenant, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// rowsOf counts the rows of a tenant-scoped table that name the tenant, as committed.
func rowsOf(ctx context.Context, t *testing.T, s *served, table, tenant string) int64 {
	t.Helper()

	iter := s.db.Client.Single().Query(ctx, cloudspanner.Statement{
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
