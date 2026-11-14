package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/tenanted/pkg/auth/staff"
)

// TestTenantPeople_aTenantAdministratorActsOnTheTenantsOwnPeopleAlone proves the
// conditions the role file puts on a tenant administrator's grants over the tenant's
// people, on the served stack.
//
// An account is one row for the whole application: a person who holds a role in two
// tenants is one account, and so is a person who holds a role of the whole application
// beside a tenant's. The methods a tenant acts on an account through (a temporary
// password, a new name, a delete) change that one account, so the role file grants them
// to a tenant's administrator for the people the tenant holds alone and nobody else
// (heldElsewhere = false AND heldGlobally = false). Without the condition north's
// administrator could set a password on an account that also manages the application,
// and sign in as it. The same reasoning hides the address and the directory's subject
// of a person another tenant holds too (heldElsewhere = false), leaves a role held in
// every tenant for the application's administrator to take (everyDomain = false), and
// lets an invitation be withdrawn while it stands and not after (pending = true).
//
// The people: nora holds a role in north alone; sam in north and in south; gail in
// north and, for the whole application, FeatureAdministrator. The member is north's
// administrator and nothing else, so every answer here is the role file's.
func TestTenantPeople_aTenantAdministratorActsOnTheTenantsOwnPeopleAlone(t *testing.T) {
	t.Parallel()

	// The conditional grants this test proves, pinned to the role file: impulse check
	// reads these calls to find a conditional grant no test names.
	provesGrant(t, staff.Roles(), "TenantAdministrator", "List", "StaffTenantUsers", "heldElsewhere = false")
	provesGrant(t, staff.Roles(), "TenantAdministrator", "Read", "StaffTenantUsers", "heldElsewhere = false")
	provesGrant(t, staff.Roles(), "TenantAdministrator", "List", "StaffTenantIdentities", "heldElsewhere = false")
	provesGrant(t, staff.Roles(), "TenantAdministrator", "Read", "StaffTenantIdentities", "heldElsewhere = false")
	provesGrant(t, staff.Roles(), "TenantAdministrator", "Execute", "DisableStaffTenantUser", "heldElsewhere = false AND heldGlobally = false")
	provesGrant(t, staff.Roles(), "TenantAdministrator", "Execute", "EnableStaffTenantUser", "heldElsewhere = false AND heldGlobally = false")
	provesGrant(t, staff.Roles(), "TenantAdministrator", "Execute", "DeleteStaffTenantUser", "heldElsewhere = false AND heldGlobally = false")
	provesGrant(t, staff.Roles(), "TenantAdministrator", "Execute", "RenameStaffTenantUser", "heldElsewhere = false AND heldGlobally = false")
	provesGrant(t, staff.Roles(), "TenantAdministrator", "Execute", "SetStaffTenantUserEmail", "heldElsewhere = false AND heldGlobally = false")
	provesGrant(t, staff.Roles(), "TenantAdministrator", "Execute", "SetStaffTenantTemporaryPassword", "heldElsewhere = false AND heldGlobally = false")
	provesGrant(t, staff.Roles(), "TenantAdministrator", "Execute", "RequireStaffTenantPasswordChange", "heldElsewhere = false AND heldGlobally = false")
	provesGrant(t, staff.Roles(), "TenantAdministrator", "Execute", "RevokeStaffTenantUserTrustedDevices", "heldElsewhere = false AND heldGlobally = false")
	provesGrant(t, staff.Roles(), "TenantAdministrator", "Execute", "EndStaffTenantUserSessions", "heldElsewhere = false AND heldGlobally = false")
	provesGrant(t, staff.Roles(), "TenantAdministrator", "Execute", "UnlinkStaffTenantSessionIdentity", "heldElsewhere = false AND heldGlobally = false")
	provesGrant(t, staff.Roles(), "TenantAdministrator", "Execute", "RemoveStaffTenantRole", "everyDomain = false")
	provesGrant(t, staff.Roles(), "TenantAdministrator", "Execute", "WithdrawStaffTenantInvitation", "pending = true")

	ctx := t.Context()
	s := newServed(ctx, t)
	admin := signedIn(ctx, t, s, adminUser)
	member := signedIn(ctx, t, s, memberUser)

	// North's administrator creates the three in north; the application's administrator
	// then gives sam a role in south and gail one of the whole application.
	ids := map[string]string{}
	for _, name := range []string{"nora", "sam", "gail"} {
		status, body := tenantPost(ctx, member, north, "create-staff-tenant-user", map[string]any{
			"username": name, "email": name + "@example.com", "password": "first-day-pass", "roles": []string{"TenantAdministrator"},
		})
		if status != http.StatusOK {
			t.Fatalf("creating %s in %s: status %d: %s", name, north, status, body)
		}
		var created struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(body, &created); err != nil || created.ID == "" {
			t.Fatalf("creating %s answered %s (%v), want the new account's id", name, body, err)
		}
		ids[name] = created.ID
	}
	// A tenant's own method gives a role to a person the tenant already holds, so the
	// role in south is given through the whole application's method, which names the tenant.
	if status, body := admin.do(ctx, http.MethodPost, "/api/assign-staff-role", []byte(`{"username":"sam","role":"TenantAdministrator","domain":"`+south+`"}`)); status != http.StatusNoContent {
		t.Fatalf("giving sam a role in %s: status %d: %s", south, status, body)
	}
	if status, body := admin.do(ctx, http.MethodPost, "/api/assign-staff-role", roleOf("gail", "FeatureAdministrator", false)); status != http.StatusNoContent {
		t.Fatalf("giving gail a role of the whole application: status %d: %s", status, body)
	}
	// sam is one of south's people now, as its administrator reads them; and south is
	// no tenant north's administrator reaches: holding nothing there, they are answered
	// as if it did not exist.
	if row := tenantRows(ctx, t, admin, south, "staff-tenant-users")[ids["sam"]]; row["username"] != "sam" {
		t.Fatalf("sam among south's people = %v, want the account north created", row)
	}
	if status, body := tenantPost(ctx, member, south, "disable-staff-tenant-user", map[string]any{"userId": ids["sam"]}); status != http.StatusNotFound {
		t.Fatalf("north's administrator calling a method of south's: status %d, want 404: %s", status, body)
	}
	// One identity link each, written as a directory sign-in writes one.
	links := map[string]string{}
	linkRows := make([]*spanner.Mutation, 0, len(ids))
	for name, userID := range ids {
		id, err := impulse.NewUUID()
		if err != nil {
			t.Fatalf("impulse.NewUUID() error = %v", err)
		}
		links[name] = id.String()
		linkRows = append(linkRows, spanner.InsertMap("StaffSessionIdentities", map[string]any{
			"Id": id.String(), "UserId": userID, "Method": "google", "Connection": "example.com", "Subject": "subject-of-" + name,
			"EmailAtLink": name + "@example.com", "CreatedAt": spanner.CommitTimestamp, "LastUsedAt": spanner.CommitTimestamp,
		}))
	}
	if _, err := s.db.Apply(ctx, linkRows); err != nil {
		t.Fatalf("writing the identity links: %v", err)
	}

	// The private fields of a person are answered where no other tenant holds them. gail
	// holds a role of the whole application and none in another tenant, so the condition
	// on the fields (heldElsewhere = false) holds for her as for nora.
	for name, want := range map[string]bool{"nora": true, "gail": true, "sam": false} {
		people := tenantRows(ctx, t, member, north, "staff-tenant-users")
		for where, row := range map[string]map[string]any{
			"list": people[ids[name]],
			"read": tenantRow(ctx, t, member, north, "staff-tenant-users", ids[name]),
		} {
			if row["username"] != name {
				t.Errorf("%s: the %s of north's people answers %v, want the person with the fields the grant carries with no condition", name, where, row)
			}
			if _, got := row["email"]; got != want {
				t.Errorf("%s: the address in the %s is answered = %v, want %v: %v", name, where, got, want, row)
			}
		}
		identities := tenantRows(ctx, t, member, north, "staff-tenant-identities")
		for where, row := range map[string]map[string]any{
			"list": identities[links[name]],
			"read": tenantRow(ctx, t, member, north, "staff-tenant-identities", links[name]),
		} {
			if row["method"] != "google" {
				t.Errorf("%s: the %s of north's identities answers %v, want the link with the fields the grant carries with no condition", name, where, row)
			}
			if _, got := row["subject"]; got != want {
				t.Errorf("%s: the directory's subject in the %s is answered = %v, want %v: %v", name, where, got, want, row)
			}
		}
	}

	// The methods that act on an account, each with the body it takes.
	user := func(name string) map[string]any {
		return map[string]any{"userId": ids[name]}
	}
	accountMethods := []struct {
		method string
		body   func(name string) map[string]any
	}{
		{"set-staff-tenant-user-email", func(name string) map[string]any {
			return map[string]any{"userId": ids[name], "email": name + ".new@example.com"}
		}},
		{"rename-staff-tenant-user", func(name string) map[string]any {
			return map[string]any{"userId": ids[name], "username": name + "-renamed"}
		}},
		{"set-staff-tenant-temporary-password", func(name string) map[string]any {
			return map[string]any{"userId": ids[name], "password": "second-day-pass"}
		}},
		{"require-staff-tenant-password-change", user},
		{"end-staff-tenant-user-sessions", user},
		{"revoke-staff-tenant-user-trusted-devices", user},
		{"disable-staff-tenant-user", user},
		{"enable-staff-tenant-user", user},
		{"unlink-staff-tenant-session-identity", func(name string) map[string]any {
			return map[string]any{"identityId": links[name]}
		}},
		{"delete-staff-tenant-user", user},
	}

	// An account another tenant or the whole application holds too is not north's to
	// change: every method is refused for sam and for gail.
	for _, m := range accountMethods {
		for _, name := range []string{"sam", "gail"} {
			if status, body := tenantPost(ctx, member, north, m.method, m.body(name)); status != http.StatusForbidden {
				t.Errorf("%s on %s: status %d, want 403: %s", m.method, name, status, body)
			}
		}
	}
	// Nothing a refused call asked for was done: both are listed as they were, by the
	// name they were created with, and their links stand.
	people := tenantRows(ctx, t, member, north, "staff-tenant-users")
	identities := tenantRows(ctx, t, member, north, "staff-tenant-identities")
	for _, name := range []string{"sam", "gail"} {
		if row := people[ids[name]]; row["username"] != name || row["disabled"] != false {
			t.Errorf("%s after the refused calls = %v, want the account as it was", name, row)
		}
		if identities[links[name]] == nil {
			t.Errorf("%s's identity link is gone after a refused call", name)
		}
	}

	// An account north holds alone is north's to change: every method goes through for
	// nora, in the order above.
	for _, m := range accountMethods {
		if status, body := tenantPost(ctx, member, north, m.method, m.body("nora")); status != http.StatusNoContent {
			t.Fatalf("%s on nora: status %d, want 204: %s", m.method, status, body)
		}
	}
	// The last of the methods deleted the account.
	if row, listed := tenantRows(ctx, t, member, north, "staff-tenant-users")[ids["nora"]]; listed {
		t.Errorf("nora is still one of north's people after the delete: %v", row)
	}

	// A role held in every tenant is not one tenant's to take. The application's
	// administrator holds TenantAdministrator in every tenant: one membership, which
	// north's list of the roles held shows and north does not own.
	adminID := staffUserID(ctx, t, admin, adminUser)
	if status, body := tenantPost(ctx, member, north, "remove-staff-tenant-role", map[string]any{"userId": adminID, "role": "TenantAdministrator"}); status != http.StatusForbidden {
		t.Errorf("north taking a role held in every tenant: status %d, want 403: %s", status, body)
	}
	if roles := tenantRolesOf(ctx, t, member, north, adminUser); len(roles) != 1 {
		t.Errorf("the administrator's roles in north after the refusal = %v, want the one held in every tenant", roles)
	}
	// A role held in north itself is north's to take, whatever else the person holds:
	// sam keeps the account and the role in south.
	if status, body := tenantPost(ctx, member, north, "remove-staff-tenant-role", map[string]any{"userId": ids["sam"], "role": "TenantAdministrator"}); status != http.StatusNoContent {
		t.Fatalf("north taking the role sam holds in north: status %d, want 204: %s", status, body)
	}
	if roles := tenantRolesOf(ctx, t, member, north, "sam"); len(roles) != 0 {
		t.Errorf("sam's roles in north after the role was taken = %v, want none", roles)
	}
	if roles := tenantRolesOf(ctx, t, admin, south, "sam"); len(roles) != 1 {
		t.Errorf("sam's roles in south = %v, want the one given there", roles)
	}

	// An invitation is withdrawn while it stands.
	status, body := tenantPost(ctx, member, north, "invite-staff-tenant-user", map[string]any{"email": "regret@example.com", "roles": []string{"TenantAdministrator"}})
	if status != http.StatusOK {
		t.Fatalf("inviting into north: status %d: %s", status, body)
	}
	var invited struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &invited); err != nil || invited.ID == "" {
		t.Fatalf("the invitation answered %s (%v), want its id", body, err)
	}
	withdraw := map[string]any{"invitationId": invited.ID}
	if status, body := tenantPost(ctx, member, north, "withdraw-staff-tenant-invitation", withdraw); status != http.StatusNoContent {
		t.Fatalf("withdrawing the invitation: status %d, want 204: %s", status, body)
	}
	// Withdrawn, it stands no more, and the grant no longer reaches it.
	if status, body := tenantPost(ctx, member, north, "withdraw-staff-tenant-invitation", withdraw); status != http.StatusForbidden {
		t.Errorf("withdrawing it a second time: status %d, want 403: %s", status, body)
	}
}

// tenantPost calls a method of the tenant's as the browser b, with the body as JSON.
func tenantPost(ctx context.Context, b *browser, tenant, method string, body map[string]any) (status int, respBody []byte) {
	b.t.Helper()

	encoded, err := json.Marshal(body)
	if err != nil {
		b.t.Fatal(err)
	}

	return b.do(ctx, http.MethodPost, "/api/tenants/"+tenant+"/"+method, encoded)
}

// tenantRows reads one of the tenant's lists as the browser b and answers its rows by
// id, each with the fields the browser's roles let it see.
func tenantRows(ctx context.Context, t *testing.T, b *browser, tenant, list string) map[string]map[string]any {
	t.Helper()

	status, body := b.do(ctx, http.MethodGet, "/api/tenants/"+tenant+"/"+list+"?limit=200", nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s's %s: status %d: %s", tenant, list, status, body)
	}
	var rows []map[string]any
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("%s's %s are not a list: %v: %s", tenant, list, err, body)
	}
	byID := make(map[string]map[string]any, len(rows))
	for _, row := range rows {
		id, _ := row["id"].(string)
		byID[id] = row
	}

	return byID
}

// tenantRow reads one row of one of the tenant's lists by its id, as the browser b.
func tenantRow(ctx context.Context, t *testing.T, b *browser, tenant, list, id string) map[string]any {
	t.Helper()

	status, body := b.do(ctx, http.MethodGet, "/api/tenants/"+tenant+"/"+list+"/"+id, nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s's %s/%s: status %d: %s", tenant, list, id, status, body)
	}
	var row map[string]any
	if err := json.Unmarshal(body, &row); err != nil {
		t.Fatalf("%s's %s/%s is not a row: %v: %s", tenant, list, id, err, body)
	}

	return row
}
