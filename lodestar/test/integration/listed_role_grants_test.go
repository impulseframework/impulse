package integration

// listed_role_grants_test: the crew's custom roles' grants as a page lists them. The
// generated list reads every row of the grants table and says of each whether the
// permission engine leaves it out when it loads the policy, and why, in the words the
// engine's own listing of a role's grants (access.UserManager.RoleGrants) says it in. The
// grants table itself has no route.

import (
	"net/http"
	"testing"

	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
)

// TestListedRoleGrants_saysWhichGrantsTheEngineLeavesOut holds the list to the engine: a
// custom role of Anvil's with a grant this release declares and one it does not, the
// second put in the store as a release that declared the resource would have left it
// there. A person the role file grants the list reads both rows at its route: the
// declared grant's row says nothing, and the undeclared one's says why the engine leaves
// it out, as RoleGrants says it for the same row. The table's own route answers 404.
func TestListedRoleGrants_saysWhichGrantsTheEngineLeavesOut(t *testing.T) {
	t.Parallel()
	w := newIdentityWorld(t)
	ctx := t.Context()
	manager := w.engine.UserManager()

	const (
		role       accesstypes.Role     = "SectorAuditor"
		declared   accesstypes.Resource = "Missions"
		undeclared accesstypes.Resource = "Ledgers"
	)
	held := accesstypes.DomainPolicyScope(anvil)
	if err := manager.AddRole(ctx, held, role, "Audits a sector's missions."); err != nil {
		t.Fatalf("access.UserManager.AddRole() error = %v", err)
	}
	if err := manager.AddRoleGrants(ctx, held, role, access.GrantRow{Permission: accesstypes.List, Resource: declared}); err != nil {
		t.Fatalf("access.UserManager.AddRoleGrants() error = %v", err)
	}
	earlier := engineOfAReleaseThatDeclared(t, w.db, map[accesstypes.Permission][]accesstypes.Resource{accesstypes.List: {undeclared}})
	if err := earlier.UserManager().AddRoleGrant(ctx, held, role, accesstypes.List, undeclared, ""); err != nil {
		t.Fatalf("access.UserManager.AddRoleGrant() by the engine of a release that declared %s, error = %v", undeclared, err)
	}

	// What the engine says of the role's two rows when it lists them itself.
	grants, err := manager.RoleGrants(ctx, held, role)
	if err != nil {
		t.Fatalf("access.UserManager.RoleGrants() error = %v", err)
	}
	if listed := grants[accesstypes.List][declared]; len(listed) != 1 || listed[0].Skipped != "" {
		t.Fatalf("RoleGrants() lists List on %s as %+v, want one grant the role holds", declared, listed)
	}
	listed := grants[accesstypes.List][undeclared]
	if len(listed) != 1 || listed[0].Skipped == "" {
		t.Fatalf("RoleGrants() lists List on %s as %+v, want one grant the engine leaves out, with why", undeclared, listed)
	}
	why := listed[0].Skipped

	// Headquarters is the role the role file grants the list to.
	reader := "grants-" + identitySuffix()
	w.seedCrewUser(ctx, t, reader, "Headquarters")
	status, body := doRequestAs(t, w.h, accesstypes.User(reader), http.MethodGet, consoleAPI+"/crew-listed-role-grants?limit=200", "")
	assertStatus(t, status, http.StatusOK, body)
	rowOf := func(resource accesstypes.Resource) map[string]any {
		t.Helper()

		for _, row := range decodeRows(t, body) {
			if row["role"] == string(role) && row["permission"] == string(accesstypes.List) && row["resourceName"] == string(resource) {
				return row
			}
		}
		t.Fatalf("the list of the custom roles' grants carries no row of %s's List on %s: %s", role, resource, body)

		return nil
	}
	if got, _ := rowOf(declared)["skipped"].(string); got != "" {
		t.Errorf("the row of the grant the release declares says skipped %q, want nothing: the role holds it", got)
	}
	if got, _ := rowOf(undeclared)["skipped"].(string); got != why {
		t.Errorf("the row of the grant the release does not declare says skipped %q, want %q, as RoleGrants says it", got, why)
	}

	status, body = doRequestAs(t, w.h, accesstypes.User(reader), http.MethodGet, consoleAPI+"/crew-role-grants?limit=200", "")
	assertStatus(t, status, http.StatusNotFound, body)
}
