// Demonstrates: permission-digest, create-form-narrowing.
package integration

// Bootstrap parity for the permission digest endpoint: the shipped personas fetch
// their structural grant enumeration through the real engine — the service card's
// source. The digest is advisory and non-folding, so these pins are pure functions of
// the shipped role config.

import (
	"encoding/json"
	"maps"
	"net/http"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
)

func TestPermissionDigest(t *testing.T) {
	t.Parallel()

	_, h, _ := sharedWorld(t)

	tests := []struct {
		name        string
		user        accesstypes.User
		target      string
		wantEntries accesstypes.PermissionDigest
		absentKeys  []accesstypes.Resource
		wantEmpty   bool
	}{
		{
			name:   "the cadet's Anvil digest carries the tri-state per grant",
			user:   "cadet",
			target: "/console/api/permission-digest?domain=" + anvil,
			wantEntries: accesstypes.PermissionDigest{
				"Missions":              {"List": accesstypes.DigestConditional, "Read": accesstypes.DigestConditional},
				"Missions.hazard":       {"List": accesstypes.DigestConditional, "Read": accesstypes.DigestConditional},
				"DistressCalls":         {"Create": accesstypes.DigestGranted, "List": accesstypes.DigestConditional, "Read": accesstypes.DigestConditional},
				"DistressCalls.summary": {"Create": accesstypes.DigestGranted, "List": accesstypes.DigestConditional, "Read": accesstypes.DigestConditional},
				"ClaimMission":          {"Execute": accesstypes.DigestConditional},
				"Wings":                 {"List": accesstypes.DigestGranted, "Read": accesstypes.DigestGranted},
			},
			// The partial-width Create grant enumerates exactly its fields: the PII
			// contact and transcript are absent, so the call-log form renders two inputs.
			absentKeys: []accesstypes.Resource{"DistressCalls.callerContact", "DistressCalls.transcript", "Refits", "Sectors", "Missions.settlement"},
		},
		{
			name:   "the dispatcher's two update groups both read conditional",
			user:   "dispatcher",
			target: "/console/api/permission-digest?domain=" + anvil,
			wantEntries: accesstypes.PermissionDigest{
				"Missions.assignedSquadronId": {"List": accesstypes.DigestConditional, "Read": accesstypes.DigestConditional, "Update": accesstypes.DigestConditional},
				"Missions.notes":              {"List": accesstypes.DigestConditional, "Read": accesstypes.DigestConditional, "Update": accesstypes.DigestConditional},
			},
		},
		{
			name:   "the dockmaster's windowed grant reads conditional on time: the window reads the clock alone",
			user:   "dock",
			target: "/console/api/permission-digest?domain=" + anvil,
			wantEntries: accesstypes.PermissionDigest{
				"Refits": {"List": accesstypes.DigestConditionalOnTime, "Read": accesstypes.DigestConditionalOnTime},
			},
		},
		{
			name:   "the marshal's global digest is the global roles' structure",
			user:   "marshal",
			target: "/console/api/permission-digest",
			wantEntries: accesstypes.PermissionDigest{
				"Sectors":       {"List": accesstypes.DigestGranted, "Read": accesstypes.DigestGranted},
				"IssueBulletin": {"Execute": accesstypes.DigestConditionalOnTime},
				"ViewAsUser":    {"Execute": accesstypes.DigestGranted},
				"AssumeRole":    {"Execute": accesstypes.DigestGranted},
				"Clients":       {"List": accesstypes.DigestConditional},
				// No global role names Missions: the one word the global digest has of them
				// is that she may list them in a sector she reaches, which is who the page
				// across sectors is for. List itself stays the sector's digest's to answer.
				"Missions": {"ListAcrossTenants": accesstypes.DigestGranted},
			},
			absentKeys: []accesstypes.Resource{"Refits"},
		},
		{
			name:      "a sector without a foothold digests to nothing",
			user:      "cadet",
			target:    "/console/api/permission-digest?domain=" + cinder,
			wantEmpty: true,
		},
		{
			name:      "an unknown sector digests to nothing, indistinguishably",
			user:      "cadet",
			target:    "/console/api/permission-digest?domain=nowhere",
			wantEmpty: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status, body := doRequestAs(t, h, tt.user, http.MethodGet, tt.target, "")
			assertStatus(t, status, http.StatusOK, body)

			var got accesstypes.PermissionDigest
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("unmarshaling digest %s: %v", body, err)
			}
			if tt.wantEmpty && len(got) != 0 {
				t.Errorf("digest = %v, want empty", got)
			}
			for res, want := range tt.wantEntries {
				if !maps.Equal(want, got[res]) {
					t.Errorf("digest[%s] = %v, want %v", res, got[res], want)
				}
			}
			for _, res := range tt.absentKeys {
				if _, ok := got[res]; ok {
					t.Errorf("digest carries %s = %v, want absent (denied is absence)", res, got[res])
				}
			}
		})
	}

	// One state alone, each read against the role file. The hazard analyst holds the
	// hazard board under an expiry and nothing else, a condition that reads the time of
	// the request alone: the board and its ship read conditional on time. The Flight
	// Lead holds the manifests on the rows of the missions their squadrons fly
	// (assignedSquadron IN subject.squadrons): the title reads conditional.
	t.Run("the hazard analyst's expiring grant reads conditional on time", func(t *testing.T) {
		t.Parallel()

		digest := digestAs(t, h, "hazards", "/console/api/permission-digest?domain="+anvil)
		if got := digest["SectorHazardBoards"]["List"]; got != accesstypes.DigestConditionalOnTime {
			t.Errorf("digest[SectorHazardBoards][List] = %q, want conditionalOnTime (now < '2099-01-01T00:00:00Z')", got)
		}
		if got := digest["SectorHazardBoards.shipName"]["List"]; got != accesstypes.DigestConditionalOnTime {
			t.Errorf("digest[SectorHazardBoards.shipName][List] = %q, want conditionalOnTime (now < '2099-01-01T00:00:00Z')", got)
		}
	})

	t.Run("the Flight Lead's grant on the manifests reads conditional", func(t *testing.T) {
		t.Parallel()

		digest := digestAs(t, h, "lead", "/console/api/permission-digest?domain="+anvil)
		if got := digest["ExpenseManifests.title"]["List"]; got != accesstypes.DigestConditional {
			t.Errorf("digest[ExpenseManifests.title][List] = %q, want conditional (assignedSquadron IN subject.squadrons)", got)
		}
	})
}

// digestAs fetches the permission digest at target as user.
func digestAs(t *testing.T, h http.Handler, user accesstypes.User, target string) accesstypes.PermissionDigest {
	t.Helper()

	status, body := doRequestAs(t, h, user, http.MethodGet, target, "")
	assertStatus(t, status, http.StatusOK, body)
	var digest accesstypes.PermissionDigest
	if err := json.Unmarshal(body, &digest); err != nil {
		t.Fatalf("decoding digest %s: %v", body, err)
	}

	return digest
}
