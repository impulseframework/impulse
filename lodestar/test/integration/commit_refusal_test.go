// Demonstrates: commit.referential-refusal, commit.constraint-refusal.
package integration

// This suite pins what a commit Spanner refuses answers: a 4xx with a message the resource
// library composes from the records the transaction buffered, said in words, decided on the
// gRPC code alone. A sector's rows go with it, so every link of a table's way to its sector
// carries the delete rule: a hangar takes its ships, a ship its refits, a mission its
// sorties and documents, a sortie its expenses, a wing its squadrons, whether the sector
// is deleted or the row alone (TestDeleteAlone_takesTheRowsOnItsWay). The referential
// refusal (409) is reproduced by the references that are on no table's way to its sector
// and say nothing of a delete: a sortie names the ship that flew it and a droid's report
// the ship it read, each a row of one sector named from outside its own way, and a mission
// names the squadron it is assigned to. A ship or a squadron named so is refused before
// the commit, in a sentence that counts what names it (TestDeleteRefusal). The commit's
// own refusal is what a delete reaches when the row in the way is under a row the
// database removes with the one deleted: the hangar that would take along a ship that
// has flown, and the wing that would take along a squadron a mission is assigned to. The
// combined deletes of a squadron and its mission prove that a child-and-parent delete
// stays legal in either order. A
// refit task created under a task number the refit already holds (a client-supplied compound
// key) and a ship created under a seeded registry (ShipsByRegistry) are the duplicate-key and
// duplicate-unique-value refusals on a create (409); a client renamed to another client's name
// is the duplicate unique value on an update (409); a client updated under an id that does not
// exist is the missing row on an update (404), which reaches the commit because Client is
// global, untracked, and updated under unconditional grants; and a mission's hazard updated to
// 9 is the CHECK refusal (400), beside the create validator's own 400 for the same value, the
// two enforcement points of one rule.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"cloud.google.com/go/spanner"
	"google.golang.org/grpc/codes"

	"github.com/impulseframework/impulse/accesstypes"
)

func TestCommitRefusal(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	db, err := prepareDatabase(ctx, t, migrationsSource, demoSeedSource)
	if err != nil {
		t.Fatal(err)
	}

	h := newTestApp(db, grants{
		accesstypes.Delete: {"Hangars", "Wings", "Squadrons", missionsResource},
		accesstypes.Create: append(
			append(
				withFields(missionsResource, "clientId", "kindId", "title", "hazard", "fee", "deadline"),
				withFields("RefitTasks", "instructions", "done")...),
			withFields("Ships", "hangarId", "classId", "registry", "name")...),
		accesstypes.Update: append(
			withFields(missionsResource, "hazard"),
			withFields("Clients", "name")...),
	})

	const (
		missingClientID = "10000000-0000-4000-8000-000000000099"
		missingID       = "10000000-0000-4000-8000-000000000098"
	)
	mission := func(clientID, title string, hazard int) string {
		return fmt.Sprintf(`{"op":"add","path":%q,"value":{"clientId":%q,"kindId":"courier","title":%q,"hazard":%d,"fee":100,"deadline":%q}}`, opPath(anvil, "missions"), clientID, title, hazard, deadline(365))
	}
	removeIn := func(sector, rest string) string {
		return fmt.Sprintf(`{"op":"remove","path":%q}`, opPath(sector, rest))
	}
	remove := func(rest string) string {
		return removeIn(anvil, rest)
	}

	tests := []struct {
		name        string
		target      string
		ops         string
		wantStatus  int
		wantMessage string
	}{
		{
			// Hangar is excluded from consolidation, so the refusal rides its standalone
			// PATCH surface. Anvil Dock One's ships would go with it, by the delete rule on
			// a ship's way to its sector, and several of them have flown and been reported
			// on: a sortie and a droid's report each name their ship from outside their own
			// way, so the commit is refused, and the message says the hangar, never the
			// ships it would have taken, the sorties or the reports.
			name:        "a delete of a hangar whose ships have flown or been reported on answers 409 saying the hangar",
			target:      sectorPath(anvil, "hangars"),
			ops:         fmt.Sprintf(`[{"op":"remove","path":"/%s"}]`, hangarAnvilDockID),
			wantStatus:  http.StatusConflict,
			wantMessage: "This hangar cannot be deleted while other records still refer to it.",
		},
		{
			// Forge Wing's squadrons would go with it, by the delete rule on a squadron's
			// way to its sector, and missions are assigned to both of them: a mission
			// names its squadron from outside its own way, so the commit is refused, and
			// the message says the wing, never the squadrons or the missions.
			name:        "a delete of a wing whose squadrons have missions assigned answers 409 saying the wing",
			target:      "/console/api/resources",
			ops:         "[" + remove("wings/"+wingForgeID) + "]",
			wantStatus:  http.StatusConflict,
			wantMessage: "This wing cannot be deleted while other records still refer to it.",
		},
		{
			// Mission is change-tracked: its delete buffers a DataChangeEvents row beside
			// it, which the message must not name. The stood-down bullion run has nothing
			// under it and is assigned to no squadron; the wing beside it is what the
			// commit refuses.
			name:        "deletes on two resources in a refused commit say both kinds in words in buffering order",
			target:      "/console/api/resources",
			ops:         "[" + remove("wings/"+wingForgeID) + "," + remove("missions/"+missionBullionID) + "]",
			wantStatus:  http.StatusConflict,
			wantMessage: "One of the wings or missions this request deletes cannot be deleted while other records still refer to it.",
		},
		{
			// Mission is change-tracked too; its client is not its tenancy hop, so the
			// missing row is found at commit, not by the tenancy check.
			name:        "a create naming a client that does not exist answers 409 with the write sentence",
			target:      "/console/api/resources",
			ops:         "[" + mission(missingClientID, "Booked for nobody", 1) + "]",
			wantStatus:  http.StatusConflict,
			wantMessage: "This mission refers to a record that does not exist, or holds a value that is too long for its field.",
		},
		{
			name:        "a batch mixing a write and a delete answers the sentence covering both causes",
			target:      "/console/api/resources",
			ops:         "[" + mission(missingClientID, "Booked beside a refused delete", 1) + "," + remove("wings/"+wingForgeID) + "]",
			wantStatus:  http.StatusConflict,
			wantMessage: "The request could not be applied: a deleted record is still referenced, or a referenced record does not exist.",
		},
		{
			// The sweep is the one mission assigned to Ashfall, in Cinder: the mission
			// that names the squadron goes first, then the squadron it named.
			name:       "the row that names another, then the row it names, in one transaction succeed",
			target:     "/console/api/resources",
			ops:        "[" + removeIn(cinder, "missions/"+missionSweepID) + "," + removeIn(cinder, "squadrons/"+squadronAshfallID) + "]",
			wantStatus: http.StatusOK,
		},
		{
			// The patrol is the one mission assigned to Portcullis, in Bastion: the
			// squadron goes first, while the mission still names it, and the commit
			// finds nothing left that does.
			name:       "the row another names, then the row that names it, in one transaction succeed",
			target:     "/console/api/resources",
			ops:        "[" + removeIn(bastion, "squadrons/"+squadronPortcullisID) + "," + removeIn(bastion, "missions/"+missionPatrolID) + "]",
			wantStatus: http.StatusOK,
		},
		{
			// The Good Samaritan's refit already has task 2: the client-supplied compound
			// key is taken, and the commit refuses it with AlreadyExists.
			name:        "a create under a task number the refit already holds answers 409 saying the refit task already exists",
			target:      "/console/api/resources",
			ops:         fmt.Sprintf(`[{"op":"add","path":%q,"value":{"instructions":"Recertify hull seals again","done":false}}]`, opPath(anvil, "refit-tasks/"+refitSamaritanID+"/2")),
			wantStatus:  http.StatusConflict,
			wantMessage: "This refit task already exists, or it has a value that must be unique and another record already has it.",
		},
		{
			// LS-101 is the Kingfisher's registry; Ship is tracked, so the change event is
			// buffered beside the create and must not be named.
			name:        "a ship created under a seeded registry answers 409 with the create sentence",
			target:      "/console/api/resources",
			ops:         fmt.Sprintf(`[{"op":"add","path":%q,"value":{"hangarId":%q,"classId":%q,"registry":"LS-101","name":"Kingfisher's Shadow"}}]`, opPath(anvil, "ships"), hangarAnvilDockID, shipClassKestrelID),
			wantStatus:  http.StatusConflict,
			wantMessage: "This ship already exists, or it has a value that must be unique and another record already has it.",
		},
		{
			// Client is excluded from consolidation and global: its standalone PATCH
			// surface has no sector. Halvard Freight is a seeded client's name.
			name:        "a client renamed to another client's name answers 409 with the update sentence",
			target:      "/console/api/clients",
			ops:         fmt.Sprintf(`[{"op":"patch","path":"/%s","value":{"name":"Halvard Freight"}}]`, clientMeridianID),
			wantStatus:  http.StatusConflict,
			wantMessage: "This client has a value that must be unique, and another record already has it.",
		},
		{
			name:        "a client updated under an id that does not exist answers 404",
			target:      "/console/api/clients",
			ops:         fmt.Sprintf(`[{"op":"patch","path":"/%s","value":{"name":"Nobody's Freight"}}]`, missingID),
			wantStatus:  http.StatusNotFound,
			wantMessage: "This client does not exist.",
		},
		{
			// The update path has no validator: the schema's CK_Missions_Hazard refuses
			// hazard 9 at commit, and the library answers it saying the mission in words.
			name:        "a mission's hazard updated to 9 answers 400 from the CHECK constraint",
			target:      "/console/api/resources",
			ops:         fmt.Sprintf(`[{"op":"patch","path":%q,"value":{"hazard":9}}]`, opPath(anvil, "missions/"+missionHaulerID)),
			wantStatus:  http.StatusBadRequest,
			wantMessage: "This mission has a value that is outside the range allowed.",
		},
		{
			// The same rule on a create is the validator's, answered naming the field
			// before anything is buffered.
			name:        "a mission created with hazard 9 answers 400 from the create validator",
			target:      "/console/api/resources",
			ops:         "[" + mission(clientHalvardID, "Too hazardous to book", 9) + "]",
			wantStatus:  http.StatusBadRequest,
			wantMessage: "hazard must be between 1 and 5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status, body := doRequest(t, h, http.MethodPatch, tt.target, tt.ops)
			assertStatus(t, status, tt.wantStatus, body)
			if tt.wantMessage == "" {
				return
			}

			var resp struct {
				Message string `json:"message"`
			}
			if err := json.Unmarshal(body, &resp); err != nil {
				t.Fatalf("json.Unmarshal(%s): %v", body, err)
			}
			if resp.Message != tt.wantMessage {
				t.Errorf("message = %q, want %q", resp.Message, tt.wantMessage)
			}
		})
	}
}

// TestDeleteAlone_takesTheRowsOnItsWay pins what the delete rule on a table's way to its
// sector does for a row deleted by itself: the rule is the database's, so it fires for
// the hangar as it does for the sector. The Quarantine Bay holds the Lantern and the Slow
// Boat, neither of which has flown or been reported on, each with a refit, and the
// Lantern's refit is given a task here. A caller who holds Delete on Hangars and on
// nothing else deletes the hangar: the ships, their refits and the task go with it, past
// the grants their own resources ask for, and the hangar across the dock keeps its ships.
func TestDeleteAlone_takesTheRowsOnItsWay(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	db, err := prepareDatabase(ctx, t, migrationsSource, demoSeedSource)
	if err != nil {
		t.Fatal(err)
	}
	const refitSlowBoatID = "a0000000-0000-4000-8000-000000000011"
	if _, err := db.Apply(ctx, []*spanner.Mutation{spanner.InsertMap("RefitTasks", map[string]any{
		"Id": refitLanternID, "TaskNumber": 1, "Instructions": "Seal the reactor housing", "Done": false,
	})}); err != nil {
		t.Fatalf("spanner.Apply(RefitTasks): %v", err)
	}
	rows := []struct {
		table string
		key   spanner.Key
		// taken is whether the row goes with the Quarantine Bay.
		taken bool
	}{
		{"Hangars", spanner.Key{hangarQuarantineID}, true},
		{"Ships", spanner.Key{shipLanternID}, true},
		{"Ships", spanner.Key{shipSlowBoatID}, true},
		{"Refits", spanner.Key{refitLanternID}, true},
		{"Refits", spanner.Key{refitSlowBoatID}, true},
		{"RefitTasks", spanner.Key{refitLanternID, 1}, true},
		{"Hangars", spanner.Key{hangarAnvilDockID}, false},
		{"Ships", spanner.Key{shipStubbornMuleID}, false},
		{"Refits", spanner.Key{refitMuleID}, false},
		{"RefitTasks", spanner.Key{refitMuleID, 1}, false},
	}
	stands := func(table string, key spanner.Key) bool {
		t.Helper()
		_, err := db.Single().ReadRow(ctx, table, key, []string{"Id"})
		switch {
		case err == nil:
			return true
		case spanner.ErrCode(err) == codes.NotFound:
			return false
		default:
			t.Fatalf("ReadRow(%s, %v): %v", table, key, err)

			return false
		}
	}
	for _, row := range rows {
		if !stands(row.table, row.key) {
			t.Fatalf("%s %v does not stand before the delete: the seed moved, and this test with it", row.table, row.key)
		}
	}

	h := newTestApp(db, grants{accesstypes.Delete: {"Hangars"}})
	status, body := doRequest(t, h, http.MethodPatch, sectorPath(anvil, "hangars"), fmt.Sprintf(`[{"op":"remove","path":"/%s"}]`, hangarQuarantineID))
	assertStatus(t, status, http.StatusOK, body)

	for _, row := range rows {
		if got := stands(row.table, row.key); got == row.taken {
			t.Errorf("%s %v stands after the delete of the Quarantine Bay = %t, want %t", row.table, row.key, got, !row.taken)
		}
	}
}
