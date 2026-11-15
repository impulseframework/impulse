// Demonstrates: warning.required-field.
package integration

// A create writes the fields its caller's Create grants name and no others, and the
// commit refuses a row whose NOT NULL column nothing filled. So a role's Create grant
// has to cover every field a create must be given, or the create the page offers the
// role can never succeed. The flight lead's grant on sorties once left out the launch
// time; the role file's validation now names such a grant (access.RequiredFieldWarning,
// pinned at none in pkg/deploy), and this suite holds the grant to an actual create,
// where the capability answer alone ("the lead may add a sortie here") had held it
// before.

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
)

func TestFlightLeadAddsASortie(t *testing.T) {
	t.Parallel()

	_, _, h := demoWorld(t)

	provesGrant(t, crew.Roles(), "FlightLead", "Create", "Sorties", "state = 'underway'")

	add := func(mission string, value string) (int, []byte) {
		t.Helper()

		return doRequestAs(t, h, "lead", http.MethodPatch, "/console/api/resources",
			fmt.Sprintf(`[{"op":"add","path":%q,"value":%s}]`, opPath(anvil, "sorties"), fmt.Sprintf(value, mission)))
	}
	sortiesOf := func(mission string) int {
		t.Helper()
		status, body := doRequestAs(t, h, "marshal", http.MethodGet, sectorPath(anvil, "sorties?limit=100&filter=missionId:eq:"+mission), "")
		assertStatus(t, status, http.StatusOK, body)

		return len(decodeRows(t, body))
	}

	const sortie = `{"missionId":%q,"shipId":"` + shipKingfisherID + `","pilotUserId":"lead","launchedAt":"2027-03-01T08:00:00Z"}`

	// The convoy is underway: the lead adds a sortie to it, with everything a sortie
	// must be given.
	before := sortiesOf(missionConvoyID)
	status, body := add(missionConvoyID, sortie)
	assertStatus(t, status, http.StatusOK, body)
	if got := sortiesOf(missionConvoyID); got != before+1 {
		t.Errorf("the convoy holds %d sortie(s) after the lead's add, want %d", got, before+1)
	}

	// The Corvid mission is claimed, not underway: the same create is refused there, and
	// writes nothing.
	before = sortiesOf(missionCorvidID)
	status, body = add(missionCorvidID, sortie)
	if status != http.StatusForbidden {
		t.Errorf("the lead's add on a mission that is not underway: status = %d, want 403: %s", status, body)
	}
	if got := sortiesOf(missionCorvidID); got != before {
		t.Errorf("the claimed mission holds %d sortie(s) after the refused add, want %d", got, before)
	}

	// Without the launch time the create cannot be completed, whoever sends it: the
	// column takes no NULL and nothing fills it.
	status, body = add(missionConvoyID, `{"missionId":%q,"shipId":"`+shipKingfisherID+`","pilotUserId":"lead"}`)
	if status == http.StatusOK {
		t.Errorf("a sortie with no launch time was created: %s", body)
	}
}
