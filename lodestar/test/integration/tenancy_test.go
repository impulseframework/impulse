// Demonstrates: tenancy.tenant-record, tenancy.concealed, @domain.join-path, @domain.
package integration

// This suite pins write-side structural row tenancy end to end: a mutation locates
// its target row WITHIN the tenant predicate, so a cross-sector primary key is
// NotFound — indistinguishable from a row that does not exist — even under
// unconditional grants; a create's proposed parent must land in the route's partition
// (the join-path proof, two hops deep for a sortie expense); and the stamped tenant
// key means a create needs no tenant field on the wire at all.

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
	"google.golang.org/grpc/codes"
)

func TestWriteTenancy(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	db, err := prepareDatabase(ctx, t, migrationsSource, demoSeedSource)
	if err != nil {
		t.Fatal(err)
	}

	full := grants{
		accesstypes.Update: withFields(missionsResource, "hazard"),
		accesstypes.Delete: {missionsResource},
		accesstypes.Create: withFields("SortieExpenses", "sortieId", "category", "amount"),
	}

	tests := []struct {
		name        string
		body        string
		wantStatus  int
		wantMessage string
		verify      func(t *testing.T)
	}{
		{
			name:        "update via another sector's route is NotFound",
			body:        fmt.Sprintf(`[{"op":"patch","path":%q,"value":{"hazard":5}}]`, opPath(bastion, "missions/"+missionHaulerID)),
			wantStatus:  http.StatusNotFound,
			wantMessage: "This mission does not exist.",
			verify: func(t *testing.T) {
				t.Helper()
				if hazard := readColumn[int64](ctx, t, db, "Missions", spanner.Key{missionHaulerID}, "Hazard"); hazard != 2 {
					t.Errorf("Mission hazard = %d, want unchanged 2", hazard)
				}
			},
		},
		{
			name:        "delete via another sector's route is NotFound",
			body:        fmt.Sprintf(`[{"op":"remove","path":%q}]`, opPath(anvil, "missions/"+missionBeaconID)),
			wantStatus:  http.StatusNotFound,
			wantMessage: "This mission does not exist.",
			verify: func(t *testing.T) {
				t.Helper()
				if _, err := db.Single().ReadRow(ctx, "Missions", spanner.Key{missionBeaconID}, []string{"Title"}); err != nil {
					t.Errorf("the Bastion mission should survive the cross-sector delete: %v", err)
				}
			},
		},
		{
			name:        "create under another sector's parent, two hops deep, is NotFound",
			body:        fmt.Sprintf(`[{"op":"add","path":%q,"value":{"sortieId":%q,"category":"fuel","amount":1}}]`, opPath(bastion, "sortie-expenses"), sortieConvoyID),
			wantStatus:  http.StatusNotFound,
			wantMessage: "A record this one refers to does not exist.",
			verify: func(t *testing.T) {
				t.Helper()
				iter := db.Single().Query(ctx, spanner.Statement{SQL: "SELECT COUNT(*) FROM SortieExpenses WHERE SortieId = @s AND Category = 'fuel' AND Amount = 1", Params: map[string]any{"s": sortieConvoyID}})
				defer iter.Stop()
				row, err := iter.Next()
				if err != nil {
					t.Fatalf("count: %v", err)
				}
				var n int64
				if err := row.Columns(&n); err != nil {
					t.Fatal(err)
				}
				if n != 0 {
					t.Errorf("cross-sector expense exists (%d), want none", n)
				}
			},
		},
		{
			name:       "the same mutations in the row's own sector succeed",
			body:       fmt.Sprintf(`[{"op":"patch","path":%q,"value":{"hazard":3}}]`, opPath(anvil, "missions/"+missionHaulerID)),
			wantStatus: http.StatusOK,
			verify: func(t *testing.T) {
				t.Helper()
				if hazard := readColumn[int64](ctx, t, db, "Missions", spanner.Key{missionHaulerID}, "Hazard"); hazard != 3 {
					t.Errorf("Mission hazard = %d, want updated 3", hazard)
				}
			},
		},
	}

	// The cases share seeded rows and run sequentially: the final case mutates the
	// row the first case proved unreachable.
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestApp(db, full)

			status, body := doRequest(t, h, http.MethodPatch, "/console/api/resources", tt.body)
			assertStatus(t, status, tt.wantStatus, body)
			if tt.wantMessage != "" && !strings.Contains(string(body), fmt.Sprintf(`"message":%q`, tt.wantMessage)) {
				t.Errorf("body = %s, want the message %q", body, tt.wantMessage)
			}
			tt.verify(t)
		})
	}

	_ = codes.NotFound
}

// TestWriteTenancy_moveUnderAnotherSector pins the update's join-path proof: an expense
// reads its sector through its sortie and that sortie's mission, and an update that moves
// it under a sortie of another sector is answered as one that moves it under a sortie that
// is not there, and leaves it where it was. Moved under another sortie of its own sector,
// it moves.
func TestWriteTenancy_moveUnderAnotherSector(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	db, err := prepareDatabase(ctx, t, migrationsSource, demoSeedSource)
	if err != nil {
		t.Fatal(err)
	}

	// A sortie of Bastion, on Bastion's open mission.
	const bastionSortieID = "9b000000-0000-4000-8000-000000000001"
	if _, err := db.Apply(ctx, []*spanner.Mutation{spanner.Insert("Sorties",
		[]string{"Id", "MissionId", "ShipId", "PilotUserId", "LaunchedAt"},
		[]any{bastionSortieID, missionBeaconID, shipBastionWatchID, "f0000000-0000-4000-8000-000000000004", time.Now().UTC()},
	)}); err != nil {
		t.Fatalf("spanner.Apply(Sorties): %v", err)
	}

	// An expense names its sortie, so its writer lists the sorties.
	h := newTestApp(db, grants{accesstypes.Update: withFields("SortieExpenses", "sortieId"), accesstypes.List: {"Sorties"}})
	move := func(t *testing.T, expense, sortie string) (int, []byte) {
		t.Helper()

		body := fmt.Sprintf(`[{"op":"patch","path":%q,"value":{"sortieId":%q}}]`, opPath(anvil, "sortie-expenses/"+expense), sortie)

		return doRequest(t, h, http.MethodPatch, "/console/api/resources", body)
	}

	t.Run("an expense of Anvil moved under a sortie of Bastion is refused as one moved under a sortie that is not there", func(t *testing.T) {
		t.Parallel()

		for _, sortie := range []string{bastionSortieID, "9b000000-0000-4000-8000-0000000000ff"} {
			status, body := move(t, expenseConvoyFuelID, sortie)
			assertStatus(t, status, http.StatusNotFound, body)
			if got, want := refusalMessage(t, body), "A record this one refers to does not exist."; got != want {
				t.Errorf("moved under %s: message = %q, want %q", sortie, got, want)
			}
		}
		if got := readColumn[string](ctx, t, db, "SortieExpenses", spanner.Key{expenseConvoyFuelID}, "SortieId"); got != sortieConvoyID {
			t.Errorf("SortieId = %s, want unchanged %s", got, sortieConvoyID)
		}
	})

	t.Run("an expense of Anvil moved under another sortie of Anvil is moved", func(t *testing.T) {
		t.Parallel()

		status, body := move(t, expenseConvoyMedID, sortieCourierID)
		assertStatus(t, status, http.StatusOK, body)
		if got := readColumn[string](ctx, t, db, "SortieExpenses", spanner.Key{expenseConvoyMedID}, "SortieId"); got != sortieCourierID {
			t.Errorf("SortieId = %s, want %s", got, sortieCourierID)
		}
	})
}

// TestWriteTenancy_notThereBeforeTheCondition pins the order of the answers of a create
// under a grant's condition that reads through the row the record names: an expense's
// `state = 'underway'` reads its sortie's mission. An expense created under a sortie of
// another sector is answered as one created under a sortie that is not there, before the
// condition is judged, so the answer cannot tell the two apart. Under a sortie of its own
// sector, the condition is judged as before.
func TestWriteTenancy_notThereBeforeTheCondition(t *testing.T) {
	t.Parallel()

	ctx, db, h := demoWorld(t)

	// The grant this suite proves, pinned to the roles file (conditions-proven).
	provesGrant(t, crew.Roles(), "Quartermaster", "Create", "SortieExpenses", "state = 'underway'")

	// A sortie of Bastion, on Bastion's open mission.
	const bastionSortieID = "9b000000-0000-4000-8000-000000000002"
	if _, err := db.Apply(ctx, []*spanner.Mutation{spanner.Insert("Sorties",
		[]string{"Id", "MissionId", "ShipId", "PilotUserId", "LaunchedAt"},
		[]any{bastionSortieID, missionBeaconID, shipBastionWatchID, "f0000000-0000-4000-8000-000000000004", time.Now().UTC()},
	)}); err != nil {
		t.Fatalf("spanner.Apply(Sorties): %v", err)
	}

	tests := []struct {
		name       string
		sortie     string
		wantStatus int
		wantText   string
	}{
		{
			name:       "under a sortie of Bastion, whose mission is not underway, it is answered as under no sortie",
			sortie:     bastionSortieID,
			wantStatus: http.StatusNotFound,
			wantText:   "A record this one refers to does not exist.",
		},
		{
			name:       "under an id that names no sortie, it is answered so",
			sortie:     "9b000000-0000-4000-8000-0000000000fe",
			wantStatus: http.StatusNotFound,
			wantText:   "A record this one refers to does not exist.",
		},
		{
			name:       "under a sortie of Anvil whose mission is on hold, the condition refuses it",
			sortie:     sortieCourierID,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "under a sortie of Anvil whose mission is underway, it is created",
			sortie:     sortieConvoyID,
			wantStatus: http.StatusOK,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status, body := doRequestAs(t, h, "quartermaster", http.MethodPatch, "/console/api/resources",
				fmt.Sprintf(`[{"op":"add","path":%q,"value":{"sortieId":%q,"category":"fuel","amount":7}}]`, opPath(anvil, "sortie-expenses"), tt.sortie))
			assertStatus(t, status, tt.wantStatus, body)
			if tt.wantText == "" {
				return
			}
			if got := refusalMessage(t, body); got != tt.wantText {
				t.Errorf("message = %q, want %q", got, tt.wantText)
			}
		})
	}
}

// TestWriteTenancy_keyNamesARowOfItsOwnSector pins the two keys between tables that keep
// the sector in a column: a pass's key to its hangar and an answer's key to the relay it
// answers hold the sector beside the row's id. A pass or an answer that names another
// sector's row is refused as one that names a row that is not there, in the same status
// and the same sentence, and nothing is written; a pass moved to another sector's hangar
// keeps its hangar. The same writes within the sector are made. The writer lists hangars,
// calls and relays, and the sector's list shows none of another sector's, so each refusal
// is the 404 a write gets before the commit, where the key would refuse it too.
func TestWriteTenancy_keyNamesARowOfItsOwnSector(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	db, err := prepareDatabase(ctx, t, migrationsSource, demoSeedSource)
	if err != nil {
		t.Fatal(err)
	}

	const (
		person        = "f0000000-0000-4000-8000-000000000004"
		noHangarID    = "60000000-0000-4000-8000-0000000000ff"
		bastionPassID = "61000000-0000-4000-8000-0000000000d1" // Bastion's, on Bastion's slip
		anvilCall     = "dc000000-0000-4000-8000-0000000c0031"
		bastionCall   = "dc000000-0000-4000-8000-0000000c0032"
		anvilNumber   = "DC-KEY-SECTOR-0001"
		bastionNumber = "DC-KEY-SECTOR-0002"
		anvilRelay    = "dc100000-0000-4000-8000-0000000c0031" // on Anvil's call
		noRelayID     = "dc100000-0000-4000-8000-0000000c00ff"
		batch         = "/console/api/resources"
	)
	if _, err := db.Apply(ctx, []*spanner.Mutation{
		spanner.Insert("HangarPasses", []string{"Id", "SectorId", "HangarId", "UserId", "LevelId"}, []any{bastionPassID, bastion, hangarBastionSlipID, person, "visit"}),
		spanner.InsertMap("DistressCalls", map[string]any{
			"Id": anvilCall, "SectorId": anvil, "Summary": "Key call", "Severity": 1, "CaseNumber": anvilNumber,
			"FiledByUserId": person, "FiledByName": "marshal",
		}),
		spanner.InsertMap("DistressCalls", map[string]any{
			"Id": bastionCall, "SectorId": bastion, "Summary": "Key call", "Severity": 1, "CaseNumber": bastionNumber,
			"FiledByUserId": person, "FiledByName": "marshal",
		}),
		spanner.InsertMap("DistressRelays", map[string]any{
			"Id": anvilRelay, "SectorId": anvil, "CaseNumber": anvilNumber, "Message": "Heard on the inner lane",
			"RelayedByName": "marshal", "RelayedAt": spanner.CommitTimestamp,
		}),
	}); err != nil {
		t.Fatalf("spanner.Apply(): %v", err)
	}

	h := newTestApp(db, grants{
		accesstypes.Create: append(withFields("HangarPasses", "hangarId", "userId", "levelId"), withFields("DistressRelays", "caseNumber", "message", "answersRelayId")...),
		accesstypes.Update: withFields("HangarPasses", "hangarId"),
		accesstypes.List:   {"Hangars", "DistressCalls", "DistressRelays"},
	})
	addPass := func(t *testing.T, hangar string) (int, []byte) {
		t.Helper()

		return doRequest(t, h, http.MethodPatch, batch, fmt.Sprintf(`[{"op":"add","path":%q,"value":{"hangarId":%q,"userId":%q,"levelId":"visit"}}]`,
			opPath(bastion, "hangar-passes"), hangar, person))
	}
	addAnswer := func(t *testing.T, sector, number, answers string) (int, []byte) {
		t.Helper()

		return doRequest(t, h, http.MethodPatch, batch, fmt.Sprintf(`[{"op":"add","path":%q,"value":{"caseNumber":%q,"message":"Heard again","answersRelayId":%q}}]`,
			opPath(sector, "distress-relays"), number, answers))
	}
	count := func(t *testing.T, sql string, params map[string]any) int64 {
		t.Helper()

		var n int64
		if err := db.Single().Query(ctx, spanner.Statement{SQL: sql, Params: params}).Do(func(row *spanner.Row) error { return row.Column(0, &n) }); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}

		return n
	}
	refused := func(t *testing.T, status int, body []byte) {
		t.Helper()

		assertStatus(t, status, http.StatusNotFound, body)
		if got, want := refusalMessage(t, body), "A record this one refers to does not exist."; got != want {
			t.Errorf("message = %q, want %q", got, want)
		}
	}

	t.Run("a pass added in Bastion for a hangar of Anvil is refused as one for a hangar that is not there, and nothing is written", func(t *testing.T) {
		t.Parallel()

		for _, hangar := range []string{hangarAnvilDockID, noHangarID} {
			status, body := addPass(t, hangar)
			refused(t, status, body)
		}
		if n := count(t, "SELECT COUNT(*) FROM HangarPasses WHERE SectorId = @sector AND HangarId IN UNNEST(@hangars)",
			map[string]any{"sector": bastion, "hangars": []string{hangarAnvilDockID, noHangarID}}); n != 0 {
			t.Errorf("Bastion holds %d pass(es) on Anvil's hangar or on no hangar, want none", n)
		}
	})

	t.Run("the same pass added in Bastion for a hangar of Bastion is created", func(t *testing.T) {
		t.Parallel()

		status, body := addPass(t, hangarBastionSlipID)
		assertStatus(t, status, http.StatusOK, body)
		if n := count(t, "SELECT COUNT(*) FROM HangarPasses WHERE SectorId = @sector AND HangarId = @hangar AND UserId = @person AND Id != @staged",
			map[string]any{"sector": bastion, "hangar": hangarBastionSlipID, "person": person, "staged": bastionPassID}); n != 1 {
			t.Errorf("Bastion holds %d new pass(es) on its slip, want the one created", n)
		}
	})

	t.Run("a pass of Bastion moved to a hangar of Anvil is refused, and keeps its hangar", func(t *testing.T) {
		t.Parallel()

		status, body := doRequest(t, h, http.MethodPatch, batch, fmt.Sprintf(`[{"op":"patch","path":%q,"value":{"hangarId":%q}}]`,
			opPath(bastion, "hangar-passes/"+bastionPassID), hangarAnvilDockID))
		refused(t, status, body)
		if got := readColumn[string](ctx, t, db, "HangarPasses", spanner.Key{bastionPassID}, "HangarId"); got != hangarBastionSlipID {
			t.Errorf("HangarId = %s, want unchanged %s", got, hangarBastionSlipID)
		}
	})

	t.Run("a relay added in Bastion that answers a relay of Anvil is refused as one that answers no relay, and nothing is written", func(t *testing.T) {
		t.Parallel()

		for _, answers := range []string{anvilRelay, noRelayID} {
			status, body := addAnswer(t, bastion, bastionNumber, answers)
			refused(t, status, body)
		}
		if n := count(t, "SELECT COUNT(*) FROM DistressRelays WHERE SectorId = @sector AND CaseNumber = @number",
			map[string]any{"sector": bastion, "number": bastionNumber}); n != 0 {
			t.Errorf("Bastion holds %d relay(s) on its call, want none", n)
		}
	})

	t.Run("the same answer added in Anvil, on Anvil's call, is created", func(t *testing.T) {
		t.Parallel()

		status, body := addAnswer(t, anvil, anvilNumber, anvilRelay)
		assertStatus(t, status, http.StatusOK, body)
		if n := count(t, "SELECT COUNT(*) FROM DistressRelays WHERE SectorId = @sector AND AnswersRelayId = @answers",
			map[string]any{"sector": anvil, "answers": anvilRelay}); n != 1 {
			t.Errorf("Anvil holds %d answer(s) to its relay, want the one created", n)
		}
	})
}
