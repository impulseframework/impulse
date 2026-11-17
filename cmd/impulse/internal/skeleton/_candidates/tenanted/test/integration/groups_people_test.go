package integration

import (
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/tenanted/pkg/auth/staff"
)

// TestGroups_aTenantPutsItsOwnPeopleInItsGroups proves who a tenant's administrator puts
// in the tenant's groups, on the served stack: a person the tenant already has.
//
// A group gives nobody a power that giving the role directly would not, and a tenant's
// administrator gives roles to the tenant's own people. So an account that is not one of
// north's people is answered as a person not found there, exactly as an id no account
// holds is: north's administrator pulls nobody into north through a group, and learns
// nothing of who belongs elsewhere. Whoever manages the whole application's people puts
// such an account in north's group, through the whole application's method, and the
// person is one of north's people from then on.
func TestGroups_aTenantPutsItsOwnPeopleInItsGroups(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	admin := signedIn(ctx, t, s, adminUser)
	member := signedIn(ctx, t, s, memberUser)

	status, body := tenantPost(ctx, member, north, "create-staff-tenant-group", map[string]any{"name": "Night shift", "description": "Keeps north through the night."})
	if status != http.StatusOK {
		t.Fatalf("north's administrator making a group in north: status %d: %s", status, body)
	}
	night := createdGroup(t, body)

	// Sol is created in south by the application's administrator, and is none of north's.
	status, body = tenantPost(ctx, admin, south, "create-staff-tenant-user", map[string]any{
		"username": "sol", "name": "Sol of south", "email": "sol@example.com", "password": "first-day-pass", "roles": []string{"TenantAdministrator"},
	})
	if status != http.StatusOK {
		t.Fatalf("creating sol in south: status %d: %s", status, body)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &created); err != nil || created.ID == "" {
		t.Fatalf("creating sol answered %s (%v), want the new account's id", body, err)
	}
	sol := created.ID
	if _, listed := tenantRows(ctx, t, admin, north, "staff-tenant-users")[sol]; listed {
		t.Fatal("sol is one of north's people before anything is given, and the case needs an account from outside the tenant")
	}
	inNight := func() bool {
		return anyRow(slices.Collect(maps.Values(tenantRows(ctx, t, admin, north, "staff-tenant-group-users"))), "groupName", "Night shift", "username", "sol")
	}

	// North's administrator does not bring sol in, and an id no account holds is answered
	// the same but for the id.
	const nobody = "99999999-9999-4999-8999-999999999999"
	status, refused := tenantPost(ctx, member, north, "add-staff-tenant-group-member", map[string]any{"groupId": night, "userId": sol})
	if status != http.StatusNotFound {
		t.Fatalf("north's administrator putting south's person in north's group: status %d, want 404: %s", status, refused)
	}
	status, unknown := tenantPost(ctx, member, north, "add-staff-tenant-group-member", map[string]any{"groupId": night, "userId": nobody})
	if status != http.StatusNotFound {
		t.Fatalf("north's administrator putting an id no account holds in north's group: status %d, want 404: %s", status, unknown)
	}
	if got, want := strings.ReplaceAll(string(refused), sol, "ID"), strings.ReplaceAll(string(unknown), nobody, "ID"); got != want {
		t.Errorf("an account from outside the tenant is answered %s, and an id no account holds %s: the two differ beyond the id", got, want)
	}
	if inNight() {
		t.Fatal("sol is in north's group after the refusal")
	}

	// The application's administrator puts sol in north's group through the whole
	// application's method. Sol is one of north's people from then on.
	if status, body := admin.do(ctx, http.MethodPost, "/api/add-staff-group-member", []byte(`{"groupId":"`+night+`","userId":"`+sol+`"}`)); status != http.StatusNoContent {
		t.Fatalf("the application's administrator putting sol in north's group: status %d: %s", status, body)
	}
	if !inNight() {
		t.Error("north's group leaves out sol, whom the application's administrator put there")
	}
	if row := tenantRows(ctx, t, admin, north, "staff-tenant-users")[sol]; row["username"] != "sol" {
		t.Errorf("sol among north's people = %v, want the account south created", row)
	}

	// A person north has is put in its group by north's administrator.
	if status, body := tenantPost(ctx, member, north, "add-staff-tenant-group-member", map[string]any{"groupId": night, "userId": s.memberID}); status != http.StatusNoContent {
		t.Fatalf("putting a person of north in its group: status %d: %s", status, body)
	}

	// A change to a group is judged as the role changes it makes. The group is given a
	// role in north, which north's administrator may give each person in it; taking sol
	// out then takes the role from sol, which the role file lets a tenant's administrator
	// do for a role held in the tenant itself (the condition on RemoveStaffTenantRole).
	provesGrant(t, staff.Roles(), "TenantAdministrator", "Execute", "RemoveStaffTenantRole", "everyDomain = false")
	if status, body := tenantPost(ctx, member, north, "give-staff-tenant-group-role", map[string]any{"groupId": night, "role": "TenantAdministrator"}); status != http.StatusNoContent {
		t.Fatalf("giving north's group a role in north: status %d: %s", status, body)
	}
	if status, body := tenantPost(ctx, member, north, "remove-staff-tenant-group-member", map[string]any{"groupId": night, "userId": sol}); status != http.StatusNoContent {
		t.Fatalf("north's administrator taking sol out of north's group: status %d: %s", status, body)
	}
	if inNight() {
		t.Error("sol is in north's group after north's administrator took them out")
	}
}
