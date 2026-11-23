// Demonstrates: delete.rows-under-refusal.
package integration

// This suite pins what a delete of a record with rows under it answers when those rows
// keep it from being deleted: a 409 before the commit, in a sentence that names the
// record's kind and counts each kind in the way. The kinds are the ones the record lists
// under refusedBy (@deleteCascade), and they refuse the delete whatever the request says:
// a mission assigned to a squadron refuses the squadron's delete, and the wing's that
// would take the squadron; a sortie flown on a ship and a droid's report filed on it
// refuse the ship's, and the hangar's that would take the ship; a mission and a
// consignment refuse their client's. A kind the record lists as going with it is not in
// the way: a client's contacts are deleted with the client, which asks first
// (TestDeleteCascade). The count is made for the person who asked: a kind they may not
// list is never named or counted, and where they may list none of the kinds in the way
// the sentence says only that there are records they can't see. A row the same request
// deleted first is not counted (TestCommitRefusal deletes a mission and then the squadron
// it was assigned to, in one request), and neither is a row that went with another
// record the request deleted first: a ship is deleted after the mission its one sortie
// was flown on (TestDeleteRefusal_leavesOutWhatTheRequestTook). The count is the Spanner
// write path's; PostgreSQL has no write path to refuse on.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"google.golang.org/grpc/codes"

	"github.com/impulseframework/impulse/accesstypes"
)

func TestDeleteRefusal(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	db, err := prepareDatabase(ctx, t, migrationsSource, demoSeedSource)
	if err != nil {
		t.Fatal(err)
	}

	remove := func(sector, rest string) string {
		return fmt.Sprintf(`{"op":"remove","path":%q}`, opPath(sector, rest))
	}
	const batch = "/console/api/resources"
	// A client is excluded from consolidation and global: its deletes ride its own PATCH.
	const clients = "/console/api/clients"
	client := func(id string) string {
		return fmt.Sprintf(`[{"op":"remove","path":"/%s"}]`, id)
	}

	tests := []struct {
		name string
		// lists are the kinds the person may list, beside Delete on what the request deletes.
		lists       []accesstypes.Resource
		target      string
		ops         string
		wantStatus  int
		wantMessage string
	}{
		{
			// Hammer has eight missions assigned to it. A mission's assigned squadron may be
			// empty, so the missions can be moved, but a squadron lists a mission under
			// refusedBy, and the sentence says only that they keep it.
			name:        "a squadron with missions assigned to it says how many, and that they keep it from being deleted",
			lists:       []accesstypes.Resource{missionsResource},
			target:      batch,
			ops:         "[" + remove(anvil, "squadrons/"+squadronHammerID) + "]",
			wantStatus:  http.StatusConflict,
			wantMessage: "This squadron has 8 missions, so it can't be deleted.",
		},
		{
			// Portcullis has the patrol alone.
			name:        "a squadron with one mission says it of the one",
			lists:       []accesstypes.Resource{missionsResource},
			target:      batch,
			ops:         "[" + remove(bastion, "squadrons/"+squadronPortcullisID) + "]",
			wantStatus:  http.StatusConflict,
			wantMessage: "This squadron has 1 mission, so it can't be deleted.",
		},
		{
			// The Stubborn Mule flew two sorties and nine droid reports read it. Ship is
			// change-tracked, and the sentence names nothing of the change history.
			name:        "a ship that has flown and been reported on counts each kind",
			lists:       []accesstypes.Resource{"Sorties", "DroidReports"},
			target:      batch,
			ops:         "[" + remove(anvil, "ships/"+shipStubbornMuleID) + "]",
			wantStatus:  http.StatusConflict,
			wantMessage: "This ship has 2 sorties and 9 droid reports, so it can't be deleted.",
		},
		{
			name:        "a kind the person may not list is not counted for them",
			lists:       []accesstypes.Resource{"Sorties"},
			target:      batch,
			ops:         "[" + remove(anvil, "ships/"+shipStubbornMuleID) + "]",
			wantStatus:  http.StatusConflict,
			wantMessage: "This ship has 2 sorties, so it can't be deleted.",
		},
		{
			name:        "a person who may list none of the kinds in the way is told only that there are some",
			target:      batch,
			ops:         "[" + remove(anvil, "ships/"+shipStubbornMuleID) + "]",
			wantStatus:  http.StatusConflict,
			wantMessage: "This ship has records under it that you can't see, so it can't be deleted.",
		},
		{
			// The first delete the request made that is refused answers, in its own words.
			name:        "two refused deletes in one request answer for the first",
			lists:       []accesstypes.Resource{missionsResource, "Sorties", "DroidReports"},
			target:      batch,
			ops:         "[" + remove(anvil, "squadrons/"+squadronHammerID) + "," + remove(anvil, "ships/"+shipKingfisherID) + "]",
			wantStatus:  http.StatusConflict,
			wantMessage: "This squadron has 8 missions, so it can't be deleted.",
		},
		{
			// Anvil Dock One's ships have flown twelve sorties and been read by 68 droid
			// reports. Hangar is excluded from consolidation, so the delete rides its
			// standalone PATCH, and the sentence counts what is under the ships it would
			// take, never the ships.
			name:        "a hangar whose ships have flown or been reported on counts what is under them",
			lists:       []accesstypes.Resource{"Ships", "Sorties", "DroidReports"},
			target:      sectorPath(anvil, "hangars"),
			ops:         fmt.Sprintf(`[{"op":"remove","path":"/%s","value":{"everythingUnder":true}}]`, hangarAnvilDockID),
			wantStatus:  http.StatusConflict,
			wantMessage: "This hangar has 12 sorties and 68 droid reports, so it can't be deleted.",
		},
		{
			// Forge Wing's squadrons, Hammer and Tongs, have fourteen missions assigned.
			name:        "a wing whose squadrons have missions assigned counts the missions",
			lists:       []accesstypes.Resource{"Squadrons", missionsResource},
			target:      batch,
			ops:         fmt.Sprintf(`[{"op":"remove","path":%q,"value":{"everythingUnder":true}}]`, opPath(anvil, "wings/"+wingForgeID)),
			wantStatus:  http.StatusConflict,
			wantMessage: "This wing has 14 missions, so it can't be deleted.",
		},
		{
			// Halvard Freight has ten missions and five consignments, the kinds a client
			// lists under refusedBy, and a contact, which is not named beside them.
			name:        "a client with a mission or a consignment can't be deleted, and says what refuses it",
			lists:       []accesstypes.Resource{missionsResource, "Consignments", "ClientContacts"},
			target:      clients,
			ops:         client(clientHalvardID),
			wantStatus:  http.StatusConflict,
			wantMessage: "This client has 10 missions and 5 consignments, so it can't be deleted.",
		},
		{
			name:        "what refuses a client's delete is counted only where the person may list it",
			lists:       []accesstypes.Resource{"Consignments"},
			target:      clients,
			ops:         client(clientHalvardID),
			wantStatus:  http.StatusConflict,
			wantMessage: "This client has 5 consignments, so it can't be deleted.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newTestApp(db, grants{
				accesstypes.Delete: {"Ships", "Squadrons", "Hangars", "Wings", "Clients"},
				accesstypes.List:   tt.lists,
			})
			status, body := doRequest(t, h, http.MethodPatch, tt.target, tt.ops)
			assertStatus(t, status, tt.wantStatus, body)
			if got := refusalMessage(t, body); got != tt.wantMessage {
				t.Errorf("message = %q, want %q", got, tt.wantMessage)
			}
		})
	}
}

// TestDeleteRefusal_leavesOutWhatTheRequestTook pins that the count never refuses a
// delete over a row that went with another record the same request deleted first. The
// Lantern, which the seed never flew, is given one sortie on the stood-down bullion run.
// Deleted alone the ship is refused over the sortie, and so it is ahead of the mission in
// one request, while the sortie still stands. Deleted after the mission, which takes its
// sorties (@deleteCascade), the ship goes, and the refit it is in the bay for goes with
// it: both deletes say they take what is under them.
func TestDeleteRefusal_leavesOutWhatTheRequestTook(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	db, err := prepareDatabase(ctx, t, migrationsSource, demoSeedSource)
	if err != nil {
		t.Fatal(err)
	}
	const sortieLanternID = "90000000-0000-4000-8000-0000000000a1"
	if _, err := db.Apply(ctx, []*spanner.Mutation{spanner.InsertMap("Sorties", map[string]any{
		"Id": sortieLanternID, "MissionId": missionBullionID, "ShipId": shipLanternID, "PilotUserId": "f0000000-0000-4000-8000-000000000004", "LaunchedAt": time.Now().UTC(),
	})}); err != nil {
		t.Fatalf("spanner.Apply(Sorties): %v", err)
	}
	stands := func(table, id string) bool {
		t.Helper()
		_, err := db.Single().ReadRow(ctx, table, spanner.Key{id}, []string{"Id"})
		switch {
		case err == nil:
			return true
		case spanner.ErrCode(err) == codes.NotFound:
			return false
		default:
			t.Fatalf("ReadRow(%s, %s): %v", table, id, err)

			return false
		}
	}
	withAllUnder := func(rest string) string {
		return fmt.Sprintf(`{"op":"remove","path":%q,"value":{"everythingUnder":true}}`, opPath(anvil, rest))
	}
	ship, mission := withAllUnder("ships/"+shipLanternID), withAllUnder("missions/"+missionBullionID)
	h := newTestApp(db, grants{
		accesstypes.Delete: {"Ships", "Refits", missionsResource, "Sorties"},
		accesstypes.List:   {"Sorties", "DroidReports"},
	})
	rows := map[string]string{"Ships": shipLanternID, "Refits": refitLanternID, "Missions": missionBullionID, "Sorties": sortieLanternID}

	for _, ops := range []string{"[" + ship + "]", "[" + ship + "," + mission + "]"} {
		status, body := doRequest(t, h, http.MethodPatch, "/console/api/resources", ops)
		assertStatus(t, status, http.StatusConflict, body)
		if got, want := refusalMessage(t, body), "This ship has 1 sortie, so it can't be deleted."; got != want {
			t.Errorf("message = %q, want %q", got, want)
		}
		for table, id := range rows {
			if !stands(table, id) {
				t.Fatalf("%s %s is gone after the refused delete %s", table, id, ops)
			}
		}
	}

	status, body := doRequest(t, h, http.MethodPatch, "/console/api/resources", "["+mission+","+ship+"]")
	assertStatus(t, status, http.StatusOK, body)
	for table, id := range rows {
		if stands(table, id) {
			t.Errorf("%s %s stands after the mission and the ship were deleted together", table, id)
		}
	}
}

// refusalMessage reads the sentence of a refusal's body.
func refusalMessage(t *testing.T, body []byte) string {
	t.Helper()

	var resp struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("json.Unmarshal(%s): %v", body, err)
	}

	return resp.Message
}
