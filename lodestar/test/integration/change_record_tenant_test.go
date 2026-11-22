// Demonstrates: change-tracking.
package integration

// This suite pins the tenant each change record carries, by writer: a write through a
// sector's route takes the sector the request names, a method's body follows the row's
// way to its sector, one step (a ship's hangar) or two (a refit's ship and its hangar),
// and the sector's own record carries its own key. The records are read from the table
// past the library.

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/accesstypes"
	initiator "github.com/impulseframework/impulse/db"
	"google.golang.org/api/iterator"
)

// changeRecord is one DataChangeEvents row as this suite reads it: the changed row and
// the tenant the record carries.
type changeRecord struct {
	RowID    string `spanner:"RowId"`
	TenantID string `spanner:"TenantId"`
}

// changeRecordsOf reads the change records of a table, oldest first.
func changeRecordsOf(t *testing.T, db *initiator.SpannerDB, table string) []changeRecord {
	t.Helper()

	iter := db.Single().Query(t.Context(), spanner.Statement{
		SQL:    "SELECT RowId, TenantId FROM DataChangeEvents WHERE TableName = @table ORDER BY EventTime, Sequence",
		Params: map[string]any{"table": table},
	})
	defer iter.Stop()

	var records []changeRecord
	for {
		row, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			return records
		}
		if err != nil {
			t.Fatalf("reading the %s change records: %v", table, err)
		}
		var record changeRecord
		if err := row.ToStruct(&record); err != nil {
			t.Fatalf("row.ToStruct(): %v", err)
		}
		records = append(records, record)
	}
}

// changeRecordTenantsOf answers the tenant each change record of one row carries,
// oldest first.
func changeRecordTenantsOf(t *testing.T, db *initiator.SpannerDB, table, rowID string) []string {
	t.Helper()

	var tenants []string
	for _, record := range changeRecordsOf(t, db, table) {
		if record.RowID == rowID {
			tenants = append(tenants, record.TenantID)
		}
	}

	return tenants
}

func TestChangeRecordTenant(t *testing.T) {
	t.Parallel()

	db, err := prepareDatabase(t.Context(), t, migrationsSource, demoSeedSource)
	if err != nil {
		t.Fatal(err)
	}

	ships := accesstypes.Resource("Ships")
	sectors := accesstypes.Resource("Sectors")
	h := newTestApp(db, grants{
		accesstypes.Create:  withFields(missionsResource, "clientId", "kindId", "title", "hazard", "fee", "deadline"),
		accesstypes.Update:  append(withFields(ships, "name"), withFields(sectors, "region")...),
		accesstypes.Delete:  {missionsResource},
		accesstypes.Execute: {"HailShip", "InspectShip"},
	})
	write := func(t *testing.T, ops string) {
		t.Helper()

		status, body := doRequestAs(t, h, "tenant-writer", http.MethodPatch, resourcesRoute, ops)
		assertStatus(t, status, http.StatusOK, body)
	}
	call := func(t *testing.T, method, body string, wantStatus int) {
		t.Helper()

		status, respBody := doRequestAs(t, h, "tenant-writer", http.MethodPost, sectorPath(anvil, method), body)
		assertStatus(t, status, wantStatus, respBody)
	}
	wantTenants := func(t *testing.T, table, rowID string, want int) {
		t.Helper()

		tenants := changeRecordTenantsOf(t, db, table, rowID)
		if len(tenants) != want {
			t.Fatalf("%s %s has %d change record(s), want %d: %v", table, rowID, len(tenants), want, tenants)
		}
		for i, tenant := range tenants {
			if tenant != anvil {
				t.Errorf("%s %s change record %d carries the tenant %q, want Anvil's key", table, rowID, i, tenant)
			}
		}
	}

	// The mission the first step creates and the last deletes.
	var mission string
	t.Run("a mission created through its route carries the sector the request names", func(t *testing.T) {
		write(t, fmt.Sprintf(`[{"op":"add","path":%q,"value":{"clientId":%q,"kindId":"rescue","title":"Logged create","hazard":1,"fee":100,"deadline":%q}}]`, opPath(anvil, "missions"), clientHalvardID, deadline(365)))
		records := changeRecordsOf(t, db, "Missions")
		if len(records) != 1 {
			t.Fatalf("Missions has %d change record(s) after one create, want 1: %v", len(records), records)
		}
		mission = records[0].RowID
		wantTenants(t, "Missions", mission, 1)
	})
	t.Run("a ship updated through its route carries the sector of its hangar", func(t *testing.T) {
		write(t, fmt.Sprintf(`[{"op":"patch","path":%q,"value":{"name":"Kingfisher Renamed"}}]`, opPath(anvil, "ships/"+shipKingfisherID)))
		wantTenants(t, "Ships", shipKingfisherID, 1)
	})
	t.Run("a ship a method's body touches carries the sector of its hangar", func(t *testing.T) {
		call(t, "hail-ship", fmt.Sprintf(`{"shipId":%q}`, shipGoodSamaritanID), http.StatusNoContent)
		wantTenants(t, "Ships", shipGoodSamaritanID, 1)
	})
	t.Run("a refit a method's body stamps carries the sector of its ship's hangar", func(t *testing.T) {
		call(t, "inspect-ship", fmt.Sprintf(`{"refitId":%q}`, refitLanternID), http.StatusOK)
		tenants := changeRecordTenantsOf(t, db, "Refits", refitLanternID)
		if len(tenants) == 0 {
			t.Fatalf("Refits %s has no change record after the inspection", refitLanternID)
		}
		wantTenants(t, "Refits", refitLanternID, len(tenants))
	})
	t.Run("the sector's own record carries its own key", func(t *testing.T) {
		write(t, `[{"op":"patch","path":"/sectors/`+anvil+`","value":{"region":"Far frontier"}}]`)
		wantTenants(t, "Sectors", anvil, 1)
	})
	t.Run("a mission deleted through its route carries the sector, as its create does", func(t *testing.T) {
		if mission == "" {
			t.Fatal("no mission was created")
		}
		write(t, fmt.Sprintf(`[{"op":"remove","path":%q}]`, opPath(anvil, "missions/"+mission)))
		wantTenants(t, "Missions", mission, 2)
	})
}
