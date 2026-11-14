// Demonstrates: computed.subject-fact, computed.row-condition, @file.rendered.
package integration

// This suite pins a grant on a computed resource whose condition reads a fact about the
// caller held in a table. ExpenseManifests is computed: its rows are folded by the
// application's functions and no statement of the library's selects them. The Flight
// Lead's List and Read grants carry assignedSquadron IN subject.squadrons, where
// assignedSquadron is the manifest's own field (@attribute) and subject.squadrons the
// squadrons the caller's membership rows name. The library reads the caller's squadrons
// once for the request and judges each manifest the functions yield, so the Flight Lead
// lists, reads and downloads the manifests of the missions Hammer flies and no other; the
// Purser's grants carry no condition and reach every manifest of the sector. Every
// expectation is read off the seed: the Hammer missions are the ones the condition
// language suite pins for the Flight Lead's missions.

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
	"github.com/impulseframework/impulse/resource"
)

func TestComputedSubjectFact(t *testing.T) {
	t.Parallel()

	_, h, _ := sharedWorld(t)

	// The grants this suite proves, pinned to the roles file (conditions-proven).
	provesGrant(t, crew.Roles(), "FlightLead", "List", "ExpenseManifests", "assignedSquadron IN subject.squadrons")
	provesGrant(t, crew.Roles(), "FlightLead", "Read", "ExpenseManifests", "assignedSquadron IN subject.squadrons")

	// Hammer's missions in Anvil: the squadron the Flight Lead flies with.
	hammer := append([]string{missionCorvidID, missionConvoyID, missionPodID}, missionIDs(14, 18, 20, 22, 28)...)
	slices.Sort(hammer)
	const manifests = "expense-manifests"

	listed := func(t *testing.T, user accesstypes.User, query string) []string {
		t.Helper()
		status, body := doRequestAs(t, h, user, http.MethodGet, sectorPath(anvil, manifests+query), "")
		assertStatus(t, status, http.StatusOK, body)
		var ids []string
		for _, row := range decodeRows(t, body) {
			ids = append(ids, cell[string](t, row, "missionId"))
		}
		slices.Sort(ids)

		return ids
	}

	t.Run("the Flight Lead lists the manifests of the missions Hammer flies", func(t *testing.T) {
		t.Parallel()

		if got := listed(t, "lead", "?limit=200"); !slices.Equal(got, hammer) {
			t.Errorf("the Flight Lead's manifests = %v, want Hammer's missions %v", got, hammer)
		}
	})

	t.Run("a list of the key alone and the count are trimmed the same way", func(t *testing.T) {
		t.Parallel()

		if got := listed(t, "lead", "?columns=missionId&limit=200"); !slices.Equal(got, hammer) {
			t.Errorf("the Flight Lead's key-only list = %v, want Hammer's missions %v", got, hammer)
		}
		rec := doRequestRecordedAs(t, h, "lead", http.MethodGet, sectorPath(anvil, manifests+"?limit=1&count=true"), "")
		assertStatus(t, rec.Code, http.StatusOK, rec.Body.Bytes())
		if got, want := rec.Header().Get(resource.TotalCountHeader), strconv.Itoa(len(hammer)); got != want {
			t.Errorf("Total-Count = %q, want %s", got, want)
		}
	})

	t.Run("the Purser, whose grant carries no condition, lists every manifest of the sector", func(t *testing.T) {
		t.Parallel()

		all := listed(t, "purser", "?limit=200")
		if len(all) != 30 {
			t.Errorf("the Purser lists %d manifests, want the sector's 30", len(all))
		}
		for _, id := range hammer {
			if !slices.Contains(all, id) {
				t.Errorf("the Purser's list lacks Hammer's mission %s", id)
			}
		}
	})

	t.Run("the Flight Lead reads a Hammer mission's manifest, with the squadron it is assigned to", func(t *testing.T) {
		t.Parallel()

		status, body := doRequestAs(t, h, "lead", http.MethodGet, sectorPath(anvil, manifests+"/"+missionPodID), "")
		assertStatus(t, status, http.StatusOK, body)
		row := decodeRow(t, body)
		if cell[string](t, row, "title") != "Recover the Lantern cargo pod" || cell[string](t, row, "assignedSquadronId") != squadronHammerID {
			t.Errorf("pod manifest = %v, want the pod mission's title and Hammer", row)
		}
	})

	t.Run("another squadron's manifest reads as a row that is not there", func(t *testing.T) {
		t.Parallel()

		// The hauler mission is open, assigned to no squadron; the Purser reads it.
		status, body := doRequestAs(t, h, "purser", http.MethodGet, sectorPath(anvil, manifests+"/"+missionHaulerID), "")
		assertStatus(t, status, http.StatusOK, body)
		if cell[string](t, decodeRow(t, body), "missionId") != missionHaulerID {
			t.Fatalf("the Purser reads no hauler manifest: %s", body)
		}
		// A mission of another sector is no manifest of this one: the answer an
		// absent row gets, which the refused row's answer has to equal.
		absentStatus, absent := doRequestAs(t, h, "lead", http.MethodGet, sectorPath(anvil, manifests+"/"+missionBeaconID), "")
		status, body = doRequestAs(t, h, "lead", http.MethodGet, sectorPath(anvil, manifests+"/"+missionHaulerID), "")
		if status != absentStatus || string(body) != string(absent) {
			t.Errorf("the Flight Lead reads the hauler manifest as %d %q, want the answer an absent row gets: %d %q", status, body, absentStatus, absent)
		}
		if strings.Contains(string(body), missionHaulerID) {
			t.Errorf("the refused manifest's answer names the mission: %s", body)
		}
	})

	t.Run("the rendered file follows the same grant: Hammer's sheet downloads, another squadron's is 404", func(t *testing.T) {
		t.Parallel()

		sheet := fileRequestAs(t, h, "lead", sectorPath(anvil, manifests+"/"+missionPodID+"/content"), nil)
		assertStatus(t, sheet.Code, http.StatusOK, sheet.Body.Bytes())
		if !strings.HasPrefix(sheet.Body.String(), "sortie,pilot,launchedAt,category,amount,memo") {
			t.Errorf("the pod mission's sheet = %q, want the manifest's CSV", sheet.Body.String())
		}

		refused := fileRequestAs(t, h, "lead", sectorPath(anvil, manifests+"/"+missionHaulerID+"/content"), nil)
		assertStatus(t, refused.Code, http.StatusNotFound, refused.Body.Bytes())
		// The Purser downloads the sheet the Flight Lead is refused.
		open := fileRequestAs(t, h, "purser", sectorPath(anvil, manifests+"/"+missionHaulerID+"/content"), nil)
		assertStatus(t, open.Code, http.StatusOK, open.Body.Bytes())
	})
}
