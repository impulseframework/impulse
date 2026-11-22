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

	h := newTestApp(db, grants{accesstypes.Update: withFields("SortieExpenses", "sortieId")})
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
