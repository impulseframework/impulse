// Demonstrates: delete.rows-under-refusal.
package integration

// This suite pins what a delete of a record with rows under it answers: a 409 before the
// commit, in a sentence that names the record's kind and counts each kind in the way.
// The kinds are the ones the generator found directly under the record on a reference
// that says nothing of a delete, so the database removes none of them with it: a mission
// names the squadron it is assigned to, a sortie the ship that flew it and a droid's
// report the ship it read, and a mission and a consignment name their client. A kind
// the record lists under refusedBy (@deleteCascade) answers first and keeps the record
// from being deleted at all. A kind the record lists as going with it is not in the way:
// a client's contacts are deleted with the client, which asks first (TestDeleteCascade).
// The count is made for the person who asked: a kind they may not list is never named
// or counted, and where they may list none of the kinds in the way the sentence says
// only that there are records they can't see. A row the same request deletes is not
// counted (TestCommitRefusal deletes a squadron and its mission in one request, in
// either order), and neither is a row that goes with another record the request
// deletes: a ship goes together with the mission its one sortie was flown on
// (TestDeleteRefusal_leavesTheDatabaseWhatItRemoves). The count is the Spanner write
// path's; PostgreSQL has no write path to refuse on.

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
			// Hammer has eight missions assigned to it, and declares nothing of its delete. A
			// mission's assigned squadron may be empty, so the missions can be moved.
			name:        "a squadron with missions assigned to it says how many, and that they can be moved",
			lists:       []accesstypes.Resource{missionsResource},
			target:      batch,
			ops:         "[" + remove(anvil, "squadrons/"+squadronHammerID) + "]",
			wantStatus:  http.StatusConflict,
			wantMessage: "This squadron has 8 missions. Move or delete them first.",
		},
		{
			// Portcullis has the patrol alone.
			name:        "a squadron with one mission says it of the one",
			lists:       []accesstypes.Resource{missionsResource},
			target:      batch,
			ops:         "[" + remove(bastion, "squadrons/"+squadronPortcullisID) + "]",
			wantStatus:  http.StatusConflict,
			wantMessage: "This squadron has 1 mission. Move or delete it first.",
		},
		{
			// The Stubborn Mule flew two sorties and nine droid reports read it. Ship is
			// change-tracked, and the sentence names nothing of the change history. A sortie
			// and a droid report always name a ship, so neither can be moved from under one.
			name:        "a ship that has flown and been reported on counts each kind",
			lists:       []accesstypes.Resource{"Sorties", "DroidReports"},
			target:      batch,
			ops:         "[" + remove(anvil, "ships/"+shipStubbornMuleID) + "]",
			wantStatus:  http.StatusConflict,
			wantMessage: "This ship has 9 droid reports and 2 sorties. Delete them first.",
		},
		{
			name:        "a kind the person may not list is not counted for them",
			lists:       []accesstypes.Resource{"Sorties"},
			target:      batch,
			ops:         "[" + remove(anvil, "ships/"+shipStubbornMuleID) + "]",
			wantStatus:  http.StatusConflict,
			wantMessage: "This ship has 2 sorties. Delete them first.",
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
			wantMessage: "This squadron has 8 missions. Move or delete them first.",
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
				accesstypes.Delete: {"Ships", "Squadrons", "Clients"},
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

// TestDeleteRefusal_leavesTheDatabaseWhatItRemoves pins that the count never refuses a
// delete over a row that goes with another record of the same request. The Lantern,
// which the seed never flew, is given one sortie on the stood-down bullion run. Deleted
// alone the ship is refused over the sortie. Deleted in one request with the mission,
// ahead of it, the ship goes: a sortie goes with its mission, so once the request
// deletes a mission the sorties under the ship are left to the commit, which finds
// none. The mission's delete says it takes what is under it, as a mission with a sortie
// must (@deleteCascade).
func TestDeleteRefusal_leavesTheDatabaseWhatItRemoves(t *testing.T) {
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
	remove := func(rest string) string {
		return fmt.Sprintf(`{"op":"remove","path":%q}`, opPath(anvil, rest))
	}
	h := newTestApp(db, grants{
		accesstypes.Delete: {"Ships", missionsResource, "Sorties"},
		accesstypes.List:   {"Sorties", "DroidReports"},
	})

	status, body := doRequest(t, h, http.MethodPatch, "/console/api/resources", "["+remove("ships/"+shipLanternID)+"]")
	assertStatus(t, status, http.StatusConflict, body)
	if got, want := refusalMessage(t, body), "This ship has 1 sortie. Delete it first."; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
	if !stands("Ships", shipLanternID) {
		t.Fatal("the Lantern is gone after its refused delete")
	}

	withItsSorties := fmt.Sprintf(`{"op":"remove","path":%q,"value":{"everythingUnder":true}}`, opPath(anvil, "missions/"+missionBullionID))
	status, body = doRequest(t, h, http.MethodPatch, "/console/api/resources", "["+remove("ships/"+shipLanternID)+","+withItsSorties+"]")
	assertStatus(t, status, http.StatusOK, body)
	for table, id := range map[string]string{"Ships": shipLanternID, "Missions": missionBullionID, "Sorties": sortieLanternID} {
		if stands(table, id) {
			t.Errorf("%s %s stands after the ship and the mission were deleted together", table, id)
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
