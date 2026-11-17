package integration

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
)

// TestGroups_whoManagesPeopleManagesTheirGroups proves the role file's grants over groups
// of people, on the served stack. A group is kept in one tenant or for the whole
// application, holds roles the way a person does, and a person in it holds the group's
// roles wherever the group holds them.
//
// Whoever manages a tenant's people manages that tenant's groups, their members and
// their roles: north's administrator makes a group in north, puts a person in it, gives
// it a role, and reads all three back. Whoever manages the whole application's people
// manages the groups kept for the whole application, and reads every group there is,
// north's among them. A person without the grant is refused: north's administrator
// holds nothing over the whole application's groups, and south is no tenant of theirs.
// And no group holds the Super Administrator role, whoever asks: it is given to a
// person, so that only a super administrator makes another.
func TestGroups_whoManagesPeopleManagesTheirGroups(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	admin := newBrowser(t, s, consoleAPI)
	admin.signIn(ctx, adminUser)
	member := newBrowser(t, s, consoleAPI)
	member.signIn(ctx, memberUser)

	// North's administrator keeps a group in north.
	status, body := tenantPost(ctx, member, north, "create-staff-tenant-group", map[string]any{"name": "Night shift", "description": "Keeps north through the night."})
	if status != http.StatusOK {
		t.Fatalf("north's administrator making a group in north: status %d: %s", status, body)
	}
	night := createdGroup(t, body)
	if row := tenantRows(ctx, t, member, north, "staff-tenant-groups")[night]; row["name"] != "Night shift" {
		t.Fatalf("north's groups hold %v under the new group's id, want the Night shift", row)
	}
	if status, body := tenantPost(ctx, member, north, "add-staff-tenant-group-member", map[string]any{"groupId": night, "userId": s.memberID}); status != http.StatusNoContent {
		t.Fatalf("putting a person of north in its group: status %d: %s", status, body)
	}
	if status, body := tenantPost(ctx, member, north, "give-staff-tenant-group-role", map[string]any{"groupId": night, "role": "TenantAdministrator"}); status != http.StatusNoContent {
		t.Fatalf("giving north's group a role in north: status %d: %s", status, body)
	}
	for _, list := range []struct{ name, column, want string }{
		{"staff-tenant-group-users", "username", memberUser},
		{"staff-tenant-group-roles", "role", "TenantAdministrator"},
		{"staff-tenant-user-group-roles", "username", memberUser},
	} {
		if !anyRow(slices.Collect(maps.Values(tenantRows(ctx, t, member, north, list.name))), "groupName", "Night shift", list.column, list.want) {
			t.Errorf("north's %s hold no row of the Night shift with %s = %s", list.name, list.column, list.want)
		}
	}

	// The application's administrator reads every group, north's among them, and keeps one
	// for the whole application.
	if !anyRow(groupRows(ctx, t, admin, consoleAPI+"/staff-groups"), "id", night, "name", "Night shift") {
		t.Error("the whole application's groups leave out the one north made")
	}
	status, body = admin.do(ctx, http.MethodPost, consoleAPI+"/create-staff-group", []byte(`{"name":"Support","description":"Helps every tenant."}`))
	if status != http.StatusOK {
		t.Fatalf("the application's administrator making a group for the whole application: status %d: %s", status, body)
	}
	support := createdGroup(t, body)
	if status, body := admin.do(ctx, http.MethodPost, consoleAPI+"/give-staff-group-role", []byte(`{"groupId":"`+support+`","role":"TenantAdministrator","domain":"`+south+`"}`)); status != http.StatusNoContent {
		t.Fatalf("giving the whole application's group a role in south: status %d: %s", status, body)
	}
	status, body = admin.do(ctx, http.MethodPost, consoleAPI+"/give-staff-group-role", []byte(`{"groupId":"`+support+`","role":"`+string(accesstypes.SuperAdministrator)+`"}`))
	if status != http.StatusBadRequest || !strings.Contains(string(body), "never to a group") {
		t.Errorf("giving a group the Super Administrator role, as a super administrator: status %d: %s, want 400 saying the role is never a group's", status, body)
	}

	// A person without the grant is refused. North's administrator holds nothing over the
	// whole application's groups, and south is answered as if it did not exist.
	if status, body := member.do(ctx, http.MethodGet, consoleAPI+"/staff-groups", nil); status != http.StatusForbidden {
		t.Errorf("north's administrator listing the whole application's groups: status %d, want 403: %s", status, body)
	}
	if status, body := member.do(ctx, http.MethodPost, consoleAPI+"/create-staff-group", []byte(`{"name":"Mine","description":"Not theirs to make."}`)); status != http.StatusForbidden {
		t.Errorf("north's administrator making a group for the whole application: status %d, want 403: %s", status, body)
	}
	if status, body := tenantPost(ctx, member, south, "create-staff-tenant-group", map[string]any{"name": "Day shift", "description": "South's."}); status != http.StatusNotFound {
		t.Errorf("north's administrator making a group in south: status %d, want 404: %s", status, body)
	}
}

// TestGroups_aTenantsAdministratorManagesItsMembersGroups proves the members' role file
// grants the same over the groups of a tenant's members, on the portal: north's
// administrator of the members auth makes a group of north's members, puts a member in
// it, gives it a role, and reads all three back. The members' groups are their own
// auth's: the staff's lists hold none of them.
func TestGroups_aTenantsAdministratorManagesItsMembersGroups(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	client := newBrowser(t, s, portalAPI)
	if status, location := client.loginPortal(ctx, clientUser); status != http.StatusFound || location != portalPage {
		t.Fatalf("the client's portal login: status %d to %q, want %d to %q", status, location, http.StatusFound, portalPage)
	}

	status, body := tenantPost(ctx, client, north, "create-members-tenant-group", map[string]any{"name": "Committee", "description": "Speaks for north's members."})
	if status != http.StatusOK {
		t.Fatalf("north's administrator of the members making a group in north: status %d: %s", status, body)
	}
	committee := createdGroup(t, body)
	if row := tenantRows(ctx, t, client, north, "members-tenant-groups")[committee]; row["name"] != "Committee" {
		t.Fatalf("north's groups of members hold %v under the new group's id, want the Committee", row)
	}
	if status, body := tenantPost(ctx, client, north, "add-members-tenant-group-member", map[string]any{"groupId": committee, "userId": s.clientID}); status != http.StatusNoContent {
		t.Fatalf("putting a member of north in its group: status %d: %s", status, body)
	}
	if status, body := tenantPost(ctx, client, north, "give-members-tenant-group-role", map[string]any{"groupId": committee, "role": "TenantAdministrator"}); status != http.StatusNoContent {
		t.Fatalf("giving north's group of members a role in north: status %d: %s", status, body)
	}
	for _, list := range []struct{ name, column, want string }{
		{"members-tenant-group-users", "username", clientUser},
		{"members-tenant-group-roles", "role", "TenantAdministrator"},
		{"members-tenant-user-group-roles", "username", clientUser},
	} {
		if !anyRow(slices.Collect(maps.Values(tenantRows(ctx, t, client, north, list.name))), "groupName", "Committee", list.column, list.want) {
			t.Errorf("north's %s hold no row of the Committee with %s = %s", list.name, list.column, list.want)
		}
	}

	admin := newBrowser(t, s, consoleAPI)
	admin.signIn(ctx, adminUser)
	if anyRow(groupRows(ctx, t, admin, consoleAPI+"/staff-groups"), "id", committee, "name", "Committee") {
		t.Error("the staff's groups hold the members' group: each auth keeps its own")
	}
}

// createdGroup reads the id of the group a create method answered.
func createdGroup(t *testing.T, body []byte) string {
	t.Helper()

	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &created); err != nil || created.ID == "" {
		t.Fatalf("creating a group answered %s (%v), want the new group's id", body, err)
	}

	return created.ID
}

// groupRows reads one of the whole application's lists as the browser b: its rows, each
// with the fields the browser's roles let it see.
func groupRows(ctx context.Context, t *testing.T, b *browser, path string) []map[string]any {
	t.Helper()

	status, body := b.do(ctx, http.MethodGet, path+"?limit=200", nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s: status %d: %s", path, status, body)
	}
	var rows []map[string]any
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("%s is not a list: %v: %s", path, err, body)
	}

	return rows
}

// anyRow reports whether one of the rows holds both values: the first under one column,
// the second under another.
func anyRow(rows []map[string]any, column, value, other, otherValue string) bool {
	for _, row := range rows {
		if row[column] == value && row[other] == otherValue {
			return true
		}
	}

	return false
}
