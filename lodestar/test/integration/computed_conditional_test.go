// Demonstrates: computed.conditional-grant, condition.now, @computed.
package integration

// computed_conditional_test (design plan §9): the hazard board answers under the
// analyst's row-free `now < '2099-01-01T00:00:00Z'` grant before the certification
// instant and refuses after it (pinned through the engine at two instants), the
// analyst's search box, sort and filter by the ship are answered, since the check settles
// the board's condition, and a row-bearing condition on the same grant is refused when the
// role file is validated.

import (
	"net/http"
	"testing"
	"time"

	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
	"github.com/impulseframework/impulse/lodestar/pkg/router"
)

func TestComputedConditionalGrant(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	_, h, client := sharedWorld(t)

	// The grant this suite proves, pinned to the roles file (conditions-proven).
	provesGrant(t, crew.Roles(), "HazardAnalyst", "List", "SectorHazardBoards", "now < '2099-01-01T00:00:00Z'")

	t.Run("the board answers today", func(t *testing.T) {
		t.Parallel()

		status, body := doRequestAs(t, h, "hazards", http.MethodGet, sectorPath(anvil, "sector-hazard-boards?limit=all"), "")
		assertStatus(t, status, http.StatusOK, body)
		rows := decodeRows(t, body)
		if len(rows) != 32 { // eight Anvil ships with readings on up to four subsystems; the Bastion Watch reading stays at Bastion
			t.Fatalf("rows = %d, want 32: %s", len(rows), body)
		}
		worst := map[string]float64{}
		for _, row := range rows {
			ship, _ := row["shipName"].(string)
			sub, _ := row["subsystem"].(string)
			reading, _ := row["worstReading"].(float64)
			worst[ship+"/"+sub] = reading
		}
		if worst["Kingfisher/hull"] != 0.61 {
			t.Errorf("Kingfisher hull worst = %v, want the higher reading 0.61", worst["Kingfisher/hull"])
		}
	})

	t.Run("the analyst's search box is answered: the check settles the board's condition from the time of the request", func(t *testing.T) {
		t.Parallel()

		// The board is searched over its rows' real values, so a search needs every searched
		// field granted with no condition left once the check has run. The analyst's
		// condition reads the time of the request alone, so the check settles it and grants
		// the ship's name. The box travels in the URL, as the browser sends it.
		status, body := doRequestAs(t, h, "hazards", http.MethodGet, sectorPath(anvil, "sector-hazard-boards?"+filter(`*:search:"king"`)), "")
		assertStatus(t, status, http.StatusOK, body)
	})

	t.Run("the analyst's sort by the ship is answered", func(t *testing.T) {
		t.Parallel()

		// A sort shares the search's rule: the check settles the board's condition from the
		// time of the request and grants the ship's name, so the board is ordered by it.
		status, body := doRequestAs(t, h, "hazards", http.MethodGet, sectorPath(anvil, "sector-hazard-boards?sort=shipName"), "")
		assertStatus(t, status, http.StatusOK, body)
	})

	t.Run("the analyst's filter by the ship is answered", func(t *testing.T) {
		t.Parallel()

		status, body := doRequestAs(t, h, "hazards", http.MethodGet, sectorPath(anvil, "sector-hazard-boards?filter=shipName:eq:Kingfisher"), "")
		assertStatus(t, status, http.StatusOK, body)
	})

	t.Run("the board is dark in a sector where the analyst holds no role", func(t *testing.T) {
		t.Parallel()

		status, body := doRequestAs(t, h, "hazards", http.MethodGet, sectorPath(bastion, "sector-hazard-boards"), "")
		assertStatus(t, status, http.StatusNotFound, body)
	})

	t.Run("the certification lapses at the instant", func(t *testing.T) {
		t.Parallel()

		checker := client.ForUser(account(t, "hazards"))
		before, err := checker.Check(ctx, accesstypes.EnvironmentAt(time.Date(2098, 12, 31, 23, 59, 0, 0, time.UTC)),
			accesstypes.DomainScope(anvil), accesstypes.List, "SectorHazardBoards")
		if err != nil {
			t.Fatalf("Check() before error = %v", err)
		}
		if !before["SectorHazardBoards"].IsGranted() {
			t.Errorf("before the instant: %v, want granted (the row-free term folds true)", before["SectorHazardBoards"])
		}
		after, err := checker.Check(ctx, accesstypes.EnvironmentAt(time.Date(2099, 1, 1, 0, 0, 1, 0, time.UTC)),
			accesstypes.DomainScope(anvil), accesstypes.List, "SectorHazardBoards")
		if err != nil {
			t.Fatalf("Check() after error = %v", err)
		}
		if !after["SectorHazardBoards"].IsDenied() {
			t.Errorf("after the instant: %v, want denied", after["SectorHazardBoards"])
		}
	})
}

// TestComputedRowConditionRefused pins the deploy invariant: a computed resource has
// no data layer to evaluate a row term against, so the validation a role file goes
// through (access.ValidateRoles, the check access.New runs on the file an auth opens
// with) refuses a row-referencing condition on its grant, and a release carrying one
// does not start.
func TestComputedRowConditionRefused(t *testing.T) {
	t.Parallel()

	conf := &access.RoleConfig{Auth: crew.Name, Roles: access.ScopedRoles{Domain: []*access.Role{{
		Name:        "BadAnalyst",
		Description: "Reads the hazard board by a row's reading.",
		Permissions: map[accesstypes.Permission][]access.Grant{
			accesstypes.List: {{Resource: "SectorHazardBoards", Fields: []accesstypes.Tag{"shipName"}, Condition: "worstReading > 0.5"}},
		},
	}}}}
	if _, err := access.ValidateRoles(router.Collection(), conf, crew.Name); err == nil {
		t.Fatal("access.ValidateRoles() accepted a row-bearing condition on a computed resource; want a refusal")
	}
}
