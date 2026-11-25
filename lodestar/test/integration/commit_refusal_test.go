// Demonstrates: commit.referential-refusal, commit.constraint-refusal.
package integration

// This suite pins what a commit Spanner refuses answers: a 4xx with a message the resource
// library composes from the records the transaction buffered, said in words, decided on the
// gRPC code alone. A delete of a record with rows under it does not reach the commit's refusal
// here. A ship and a squadron declare nothing of their delete, and the rows in their way refuse
// it before the commit, in a sentence that counts them (TestDeleteRefusal). A wing and a hangar
// declare what goes with them (@deleteCascade), which is deleted in code, and list under
// refusedBy the kinds that refuse them before the commit; a delete that would take a row the
// caller may not delete is refused before anything is deleted
// (TestDeleteAlone_refusesWhatTheCallerMayNotTake). The referential refusal (409) is reproduced
// by a create naming a client that does not exist, and beside a delete the same request makes,
// by the sentence covering both causes. A squadron and what is under it (the one mission
// assigned to it, and its memberships and pass), deleted in one transaction, are refused in
// either order, in the sentence that counts them: the squadron declares nothing, and its count
// reads what the database held before the transaction, so rows that are not declared to go
// with a record are deleted in an earlier request. A wing declares its delete, so it is judged
// as the delete is buffered: deleted ahead of the one mission assigned to its squadron, in one
// request, it is refused, and nothing is deleted. A refit task created under a task number the refit already
// holds (a client-supplied compound key) and a ship created under a seeded registry
// (ShipsByRegistry) are the duplicate-key and duplicate-unique-value refusals on a create
// (409); a client renamed to another client's name is the duplicate unique value on an update
// (409); a client updated under an id that does not exist is the missing row on an update
// (404), which reaches the commit because Client is global, untracked, and updated under
// unconditional grants; and a mission's hazard updated to 9 is the CHECK refusal (400), beside
// the create validator's own 400 for the same value, the two enforcement points of one rule.

import (
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

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

	// Wing Ward, written here, has one squadron, and the one mission assigned to it.
	const (
		wingWardID     = "40000000-0000-4000-8000-0000000000c1"
		squadronWardID = "50000000-0000-4000-8000-0000000000c1"
		missionWardID  = "80000000-0000-4000-8000-0000000000c1"
	)
	if _, err := db.Apply(ctx, []*spanner.Mutation{
		spanner.Insert("Wings", []string{"Id", "SectorId", "Name"}, []any{wingWardID, anvil, "Ward Wing"}),
		spanner.Insert("Squadrons", []string{"Id", "WingId", "Name"}, []any{squadronWardID, wingWardID, "Ward"}),
		spanner.InsertMap("Missions", map[string]any{
			"Id": missionWardID, "SectorId": anvil, "ClientId": clientHalvardID, "KindId": "courier", "Title": "Ward run", "Hazard": 1, "Fee": *big.NewRat(100, 1),
			"Deadline": time.Now().Add(24 * time.Hour), "BookedByUserId": "f0000000-0000-4000-8000-000000000004", "BookedByName": "marshal", "StatusId": "stood_down",
			"AssignedSquadronId": squadronWardID,
		}),
	}); err != nil {
		t.Fatalf("spanner.Apply(): %v", err)
	}

	h := newTestApp(db, grants{
		accesstypes.Delete: {"Wings", "Squadrons", missionsResource, "SquadronMemberships", "HangarPasses"},
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
		// The seeded members of Portcullis and its pass.
		memberPortcullisDispatcher = "f0000000-0000-4000-8000-000000000007"
		memberPortcullisPilot      = "f0000000-0000-4000-8000-000000000004"
		passPortcullisID           = "61000000-0000-4000-8000-000000000005"
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
			// Mission is change-tracked too; its client is not its tenancy hop, so the
			// missing row is found at commit, not by the tenancy check.
			name:        "a create naming a client that does not exist answers 409 with the write sentence",
			target:      "/console/api/resources",
			ops:         "[" + mission(missingClientID, "Booked for nobody", 1) + "]",
			wantStatus:  http.StatusConflict,
			wantMessage: "This mission refers to a record that does not exist, or holds a value that is too long for its field.",
		},
		{
			// The stood-down bullion run has nothing under it and is assigned to no
			// squadron, so its delete is buffered beside the create the commit refuses.
			// Mission is change-tracked: the change event buffered beside it is not named.
			name:        "a batch mixing a write and a delete answers the sentence covering both causes",
			target:      "/console/api/resources",
			ops:         "[" + mission(missingClientID, "Booked beside a delete", 1) + "," + remove("missions/"+missionBullionID) + "]",
			wantStatus:  http.StatusConflict,
			wantMessage: "The request could not be applied: a deleted record is still referenced, or a referenced record does not exist.",
		},
		{
			// The sweep is the one mission assigned to Ashfall, in Cinder: the mission
			// that names the squadron goes first, then the squadron it named. The
			// squadron's count reads the mission as the database held it, and refuses;
			// the caller may list none of the kinds under a squadron, so the sentence
			// says only that there are records.
			name:        "the row that names another, then the row it names, in one transaction are refused in the count's sentence",
			target:      "/console/api/resources",
			ops:         "[" + removeIn(cinder, "missions/"+missionSweepID) + "," + removeIn(cinder, "squadrons/"+squadronAshfallID) + "]",
			wantStatus:  http.StatusConflict,
			wantMessage: "This squadron has records under it that you can't see, so it can't be deleted.",
		},
		{
			// The patrol is the one mission assigned to Portcullis, in Bastion, beside its
			// two members and its pass into the Rampart: the squadron goes first, and its
			// count reads the mission, the memberships and the pass as the database held
			// them.
			name:   "the row another names, then the rows that name it, in one transaction are refused in the count's sentence",
			target: "/console/api/resources",
			ops: "[" + strings.Join([]string{
				removeIn(bastion, "squadrons/"+squadronPortcullisID), removeIn(bastion, "missions/"+missionPatrolID),
				removeIn(bastion, "squadron-memberships/"+squadronPortcullisID+"/"+memberPortcullisDispatcher),
				removeIn(bastion, "squadron-memberships/"+squadronPortcullisID+"/"+memberPortcullisPilot),
				removeIn(bastion, "hangar-passes/"+passPortcullisID),
			}, ",") + "]",
			wantStatus:  http.StatusConflict,
			wantMessage: "This squadron has records under it that you can't see, so it can't be deleted.",
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

	t.Run("a wing deleted ahead of the mission assigned to its squadron, in one transaction, is refused", func(t *testing.T) {
		t.Parallel()

		wing := fmt.Sprintf(`{"op":"remove","path":%q,"value":{"everythingUnder":true}}`, opPath(anvil, "wings/"+wingWardID))
		status, body := doRequest(t, h, http.MethodPatch, "/console/api/resources", "["+wing+","+remove("missions/"+missionWardID)+"]")
		assertStatus(t, status, http.StatusConflict, body)
		if got, want := refusalMessage(t, body), "This wing has records under it that you can't see, so it can't be deleted."; got != want {
			t.Errorf("message = %q, want %q", got, want)
		}
		for _, row := range [][2]string{{"Wings", wingWardID}, {"Squadrons", squadronWardID}, {"Missions", missionWardID}} {
			switch _, err := db.Single().ReadRow(ctx, row[0], spanner.Key{row[1]}, []string{"Id"}); {
			case spanner.ErrCode(err) == codes.NotFound:
				t.Errorf("%s %s is gone after the refused delete", row[0], row[1])
			case err != nil:
				t.Fatalf("ReadRow(%s, %s): %v", row[0], row[1], err)
			}
		}
	})
}

// TestDeleteAlone_refusesWhatTheCallerMayNotTake pins a hangar's delete by a caller who may
// delete hangars and nothing the hangar takes (@deleteCascade). The Quarantine Bay holds the
// Lantern and the Slow Boat, neither of which has flown or been reported on, each with a
// refit, and the Lantern's refit is given a task here. Every row under the hangar is checked
// as its own delete would check it, so the first the caller may not delete refuses the whole
// delete, plain or saying it takes everything, and nothing is deleted: the hangar, its passes,
// its ships, their refits and the task stand, as does the hangar across the dock. The caller
// may list none of the kinds, so the sentence names none (TestDeleteCascade_fourRecords takes a
// hangar's rows as a caller who may).
func TestDeleteAlone_refusesWhatTheCallerMayNotTake(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	db, err := prepareDatabase(ctx, t, migrationsSource, demoSeedSource)
	if err != nil {
		t.Fatal(err)
	}
	const (
		refitSlowBoatID = "a0000000-0000-4000-8000-000000000011"
		// passQuarantineID is one of the seeded passes into the Quarantine Bay.
		passQuarantineID = "61000000-0000-4000-8000-000000000002"
	)
	if _, err := db.Apply(ctx, []*spanner.Mutation{spanner.InsertMap("RefitTasks", map[string]any{
		"Id": refitLanternID, "TaskNumber": 1, "Instructions": "Seal the reactor housing", "Done": false,
	})}); err != nil {
		t.Fatalf("spanner.Apply(RefitTasks): %v", err)
	}
	rows := []struct {
		table string
		key   spanner.Key
	}{
		{"Hangars", spanner.Key{hangarQuarantineID}},
		{"HangarPasses", spanner.Key{passQuarantineID}},
		{"Ships", spanner.Key{shipLanternID}},
		{"Ships", spanner.Key{shipSlowBoatID}},
		{"Refits", spanner.Key{refitLanternID}},
		{"Refits", spanner.Key{refitSlowBoatID}},
		{"RefitTasks", spanner.Key{refitLanternID, 1}},
		{"Hangars", spanner.Key{hangarAnvilDockID}},
		{"Ships", spanner.Key{shipStubbornMuleID}},
		{"Refits", spanner.Key{refitMuleID}},
		{"RefitTasks", spanner.Key{refitMuleID, 1}},
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
	for _, value := range []string{"", `,"value":{"everythingUnder":true}`} {
		status, body := doRequest(t, h, http.MethodPatch, sectorPath(anvil, "hangars"), fmt.Sprintf(`[{"op":"remove","path":"/%s"%s}]`, hangarQuarantineID, value))
		assertStatus(t, status, http.StatusForbidden, body)
		if got, want := refusalMessage(t, body), "This hangar has records under it that you can't delete, so it can't be deleted."; got != want {
			t.Errorf("message = %q, want %q", got, want)
		}
	}

	for _, row := range rows {
		if !stands(row.table, row.key) {
			t.Errorf("%s %v is gone after the refused delete of the Quarantine Bay", row.table, row.key)
		}
	}
}
