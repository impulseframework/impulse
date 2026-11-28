package resource

import (
	"context"
	"maps"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/eventsource"
)

// changeDatabase makes a database with the change tenant fixture's tables on the shared
// emulator, with two yards and a dock in each, written past the library so they have no
// change records.
func changeDatabase(t *testing.T, name string) *spanner.Client {
	t.Helper()

	container := spannerEmulator(t)
	db, err := container.CreateDatabase(context.Background(), name)
	if err != nil {
		t.Fatalf("initiator.SpannerContainer.CreateDatabase() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.DropDatabase(context.Background()); err != nil {
			t.Error(err)
		}
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := db.MigrateUp("file://testdata/changetenant/schema"); err != nil {
		t.Fatalf("initiator.SpannerDB.MigrateUp() error = %v", err)
	}
	seedChangeRows(t, db.Client,
		spanner.InsertMap("Yards", map[string]any{"Id": changeYardNorth, "Name": "North"}),
		spanner.InsertMap("Yards", map[string]any{"Id": changeYardSouth, "Name": "South"}),
		spanner.InsertMap("Docks", map[string]any{"Id": "dock-north", "YardId": changeYardNorth, "Name": "North dock"}),
		spanner.InsertMap("Docks", map[string]any{"Id": "dock-south", "YardId": changeYardSouth, "Name": "South dock"}),
	)

	return db.Client
}

// seedChangeRows writes rows past the library, so they have no change records.
func seedChangeRows(t *testing.T, db *spanner.Client, rows ...*spanner.Mutation) {
	t.Helper()

	if _, err := db.Apply(context.Background(), rows); err != nil {
		t.Fatalf("spanner.Client.Apply() error = %v", err)
	}
}

// changeTenants reads past the library the tenant each change record of one row
// carries, oldest first.
func changeTenants(t *testing.T, db *spanner.Client, table, row string) []string {
	t.Helper()

	stmt := spanner.NewStatement("SELECT TenantId FROM DataChangeEvents WHERE TableName = @table AND RowId = @row ORDER BY EventTime, Sequence")
	stmt.Params["table"] = table
	stmt.Params["row"] = row
	tenants := []string{}
	if err := db.Single().Query(context.Background(), stmt).Do(func(r *spanner.Row) error {
		var tenant spanner.NullString
		if err := r.Column(0, &tenant); err != nil {
			return errors.Wrap(err, "spanner.Row.Column()")
		}
		tenants = append(tenants, tenant.StringVal)

		return nil
	}); err != nil {
		t.Fatalf("reading the change records of %s %s: %v", table, row, err)
	}

	return tenants
}

// changeWrite is one patch a case applies, alone or in a transaction with others.
type changeWrite interface {
	Apply(ctx context.Context, client Client, eventSource ...eventsource.EventSource) error
	Buffer(ctx context.Context, txn ReadWriteTransaction, eventSource ...eventsource.EventSource) error
}

// applyEach applies each patch in its own transaction.
func applyEach(t *testing.T, client Client, writes ...changeWrite) {
	t.Helper()

	for i, w := range writes {
		if err := w.Apply(context.Background(), client, eventsource.ProcessEvent("test")); err != nil {
			t.Fatalf("Apply() of write %d error = %v", i, err)
		}
	}
}

// applyTogether buffers the patches in one transaction, in order.
func applyTogether(t *testing.T, client Client, writes ...changeWrite) {
	t.Helper()

	if err := client.ExecuteFunc(context.Background(), func(ctx context.Context, txn ReadWriteTransaction) error {
		for _, w := range writes {
			if err := w.Buffer(ctx, txn, eventsource.ProcessEvent("test")); err != nil {
				return errors.Wrap(err, "changeWrite.Buffer()")
			}
		}

		return nil
	}); err != nil {
		t.Fatalf("ExecuteFunc() error = %v", err)
	}
}

// TestChangeRecordTenant_spanner pins, through the library's own writes on the
// emulator, the tenant each change record carries: the row's tenant after the write,
// and a deleted row's before it, found on the patch, the stored row, or what the same
// transaction buffered.
func TestChangeRecordTenant_spanner(t *testing.T) {
	t.Parallel()

	db := changeDatabase(t, "change-tenant")
	client := NewSpannerClient(db)
	dockNorth, dockSouth := "dock-north", "dock-south"
	north, south := changeYardNorth, changeYardSouth

	t.Run("a table that belongs to no tenant: empty", func(t *testing.T) {
		t.Parallel()

		applyEach(t, client,
			changePatch[changeNotice](CreatePatchType, "notice-1", map[accesstypes.Field]any{"Body": "first"}),
			changePatch[changeNotice](UpdatePatchType, "notice-1", map[accesstypes.Field]any{"Body": "second"}),
			changePatch[changeNotice](DeletePatchType, "notice-1", nil),
		)
		if diff := cmp.Diff([]string{"", "", ""}, changeTenants(t, db, "Notices", "notice-1")); diff != "" {
			t.Errorf("tenants mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("the tenant record, created, updated and deleted: its own key", func(t *testing.T) {
		t.Parallel()

		applyEach(t, client,
			changePatch[changeYard](CreatePatchType, "yard-own", map[accesstypes.Field]any{"Name": "Own"}),
			changePatch[changeYard](UpdatePatchType, "yard-own", map[accesstypes.Field]any{"Name": "Renamed"}),
			changePatch[changeYard](DeletePatchType, "yard-own", nil),
		)
		if diff := cmp.Diff([]string{"yard-own", "yard-own", "yard-own"}, changeTenants(t, db, "Yards", "yard-own")); diff != "" {
			t.Errorf("tenants mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("a row that carries its tenant: the patch's value, then the stored row's", func(t *testing.T) {
		t.Parallel()

		applyEach(t, client,
			changePatch[changeDock](CreatePatchType, "dock-3", map[accesstypes.Field]any{"YardID": south, "Name": "Third"}),
			changePatch[changeDock](UpdatePatchType, "dock-3", map[accesstypes.Field]any{"Name": "Renamed"}),
			changePatch[changeDock](DeletePatchType, "dock-3", nil),
		)
		if diff := cmp.Diff([]string{south, south, south}, changeTenants(t, db, "Docks", "dock-3")); diff != "" {
			t.Errorf("tenants mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("one hop and two hops under stored parents", func(t *testing.T) {
		t.Parallel()

		seedChangeRows(t, db, spanner.InsertMap("Berths", map[string]any{"Id": "berth-4-stored", "DockId": dockSouth, "Name": "Stored"}))
		applyEach(t, client,
			changePatch[changeBerth](CreatePatchType, "berth-4", map[accesstypes.Field]any{"DockID": &dockSouth, "Name": "Fourth"}),
			changePatch[changeBerth](UpdatePatchType, "berth-4", map[accesstypes.Field]any{"Name": "Renamed"}),
			changePatch[changeBerth](DeletePatchType, "berth-4", nil),
			changePatch[changeMooring](CreatePatchType, "mooring-4", map[accesstypes.Field]any{"BerthID": "berth-4-stored", "Name": "Fourth"}),
			changePatch[changeMooring](UpdatePatchType, "mooring-4", map[accesstypes.Field]any{"Name": "Renamed"}),
			changePatch[changeMooring](DeletePatchType, "mooring-4", nil),
		)
		for _, row := range []struct{ table, id string }{{"Berths", "berth-4"}, {"Moorings", "mooring-4"}} {
			if diff := cmp.Diff([]string{south, south, south}, changeTenants(t, db, row.table, row.id)); diff != "" {
				t.Errorf("tenants of %s mismatch (-want +got):\n%s", row.table, diff)
			}
		}
	})

	t.Run("parents created in the same transaction: the yard of the dock", func(t *testing.T) {
		t.Parallel()

		dock5, dock5b := "dock-5", "dock-5b"
		applyTogether(t, client,
			changePatch[changeDock](CreatePatchType, dock5, map[accesstypes.Field]any{"YardID": south, "Name": "Fifth"}),
			changePatch[changeBerth](CreatePatchType, "berth-5", map[accesstypes.Field]any{"DockID": &dock5, "Name": "Fifth"}),
		)
		applyTogether(t, client,
			changePatch[changeDock](CreatePatchType, dock5b, map[accesstypes.Field]any{"YardID": south, "Name": "Fifth"}),
			changePatch[changeBerth](CreatePatchType, "berth-5b", map[accesstypes.Field]any{"DockID": &dock5b, "Name": "Fifth"}),
			changePatch[changeMooring](CreatePatchType, "mooring-5b", map[accesstypes.Field]any{"BerthID": "berth-5b", "Name": "Fifth"}),
		)
		for _, row := range []struct{ table, id string }{{"Berths", "berth-5"}, {"Berths", "berth-5b"}, {"Moorings", "mooring-5b"}} {
			if diff := cmp.Diff([]string{south}, changeTenants(t, db, row.table, row.id)); diff != "" {
				t.Errorf("tenants of %s %s mismatch (-want +got):\n%s", row.table, row.id, diff)
			}
		}
	})

	t.Run("a berth moved to another yard's dock: the new yard", func(t *testing.T) {
		t.Parallel()

		seedChangeRows(t, db, spanner.InsertMap("Berths", map[string]any{"Id": "berth-6", "DockId": dockNorth, "Name": "Sixth"}))
		applyEach(t, client, changePatch[changeBerth](UpdatePatchType, "berth-6", map[accesstypes.Field]any{"DockID": &dockSouth}))
		if diff := cmp.Diff([]string{south}, changeTenants(t, db, "Berths", "berth-6")); diff != "" {
			t.Errorf("tenants mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("a berth updated after its dock moved yard in the same transaction: the new yard", func(t *testing.T) {
		t.Parallel()

		seedChangeRows(t, db,
			spanner.InsertMap("Docks", map[string]any{"Id": "dock-7", "YardId": north, "Name": "Seventh"}),
			spanner.InsertMap("Berths", map[string]any{"Id": "berth-7", "DockId": "dock-7", "Name": "Seventh"}),
		)
		applyTogether(t, client,
			changePatch[changeDock](UpdatePatchType, "dock-7", map[accesstypes.Field]any{"YardID": south}),
			changePatch[changeBerth](UpdatePatchType, "berth-7", map[accesstypes.Field]any{"Name": "Renamed"}),
		)
		if diff := cmp.Diff([]string{south}, changeTenants(t, db, "Berths", "berth-7")); diff != "" {
			t.Errorf("tenants mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("a dock moved to another yard, then renamed: both records carry the new yard", func(t *testing.T) {
		t.Parallel()

		seedChangeRows(t, db, spanner.InsertMap("Docks", map[string]any{"Id": "dock-11", "YardId": north, "Name": "Eleventh"}))
		applyTogether(t, client,
			changePatch[changeDock](UpdatePatchType, "dock-11", map[accesstypes.Field]any{"YardID": south}),
			changePatch[changeDock](UpdatePatchType, "dock-11", map[accesstypes.Field]any{"Name": "Renamed"}),
		)
		if diff := cmp.Diff([]string{south, south}, changeTenants(t, db, "Docks", "dock-11")); diff != "" {
			t.Errorf("tenants mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("a berth moved to another yard's dock, then renamed: both records carry the new yard", func(t *testing.T) {
		t.Parallel()

		seedChangeRows(t, db, spanner.InsertMap("Berths", map[string]any{"Id": "berth-12", "DockId": dockNorth, "Name": "Twelfth"}))
		applyTogether(t, client,
			changePatch[changeBerth](UpdatePatchType, "berth-12", map[accesstypes.Field]any{"DockID": &dockSouth}),
			changePatch[changeBerth](UpdatePatchType, "berth-12", map[accesstypes.Field]any{"Name": "Renamed"}),
		)
		if diff := cmp.Diff([]string{south, south}, changeTenants(t, db, "Berths", "berth-12")); diff != "" {
			t.Errorf("tenants mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("a berth created, then written by insert-or-update with its name alone: both carry the dock's yard", func(t *testing.T) {
		t.Parallel()

		applyTogether(t, client,
			changePatch[changeBerth](CreatePatchType, "berth-13", map[accesstypes.Field]any{"DockID": &dockSouth, "Name": "Thirteenth"}),
			changePatch[changeBerth](CreateOrUpdatePatchType, "berth-13", map[accesstypes.Field]any{"Name": "Again"}),
		)
		if diff := cmp.Diff([]string{south, south}, changeTenants(t, db, "Berths", "berth-13")); diff != "" {
			t.Errorf("tenants mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("a berth moved to another yard's dock, then deleted: both records carry the new yard", func(t *testing.T) {
		t.Parallel()

		seedChangeRows(t, db, spanner.InsertMap("Berths", map[string]any{"Id": "berth-14", "DockId": dockNorth, "Name": "Fourteenth"}))
		applyTogether(t, client,
			changePatch[changeBerth](UpdatePatchType, "berth-14", map[accesstypes.Field]any{"DockID": &dockSouth}),
			changePatch[changeBerth](DeletePatchType, "berth-14", nil),
		)
		if diff := cmp.Diff([]string{south, south}, changeTenants(t, db, "Berths", "berth-14")); diff != "" {
			t.Errorf("tenants mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("a crate under a stored bay, created, updated without the bay, deleted: the bay's yard", func(t *testing.T) {
		t.Parallel()

		bay := int64(1)
		seedChangeRows(t, db, spanner.InsertMap("Bays", map[string]any{"Number": bay, "YardId": north, "Name": "First"}))
		applyEach(t, client,
			numberPatch[changeCrate](CreatePatchType, 101, map[accesstypes.Field]any{"BayNumber": &bay, "Name": "First"}),
			numberPatch[changeCrate](UpdatePatchType, 101, map[accesstypes.Field]any{"Name": "Renamed"}),
			numberPatch[changeCrate](DeletePatchType, 101, nil),
		)
		if diff := cmp.Diff([]string{north, north, north}, changeTenants(t, db, "Crates", "101")); diff != "" {
			t.Errorf("tenants mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("a label created under a stored crate: the yard, through two number keys", func(t *testing.T) {
		t.Parallel()

		seedChangeRows(t, db,
			spanner.InsertMap("Bays", map[string]any{"Number": int64(2), "YardId": north, "Name": "Second"}),
			spanner.InsertMap("Crates", map[string]any{"Number": int64(102), "BayNumber": int64(2), "Name": "Second"}),
		)
		applyEach(t, client, changePatch[changeLabel](CreatePatchType, "label-2", map[accesstypes.Field]any{"CrateNumber": int64(102), "Name": "Second"}))
		if diff := cmp.Diff([]string{north}, changeTenants(t, db, "Labels", "label-2")); diff != "" {
			t.Errorf("tenants mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("a bay, a crate and a label created in one transaction: each carries the yard", func(t *testing.T) {
		t.Parallel()

		bay := int64(3)
		applyTogether(t, client,
			numberPatch[changeBay](CreatePatchType, bay, map[accesstypes.Field]any{"YardID": south, "Name": "Third"}),
			numberPatch[changeCrate](CreatePatchType, 103, map[accesstypes.Field]any{"BayNumber": &bay, "Name": "Third"}),
			changePatch[changeLabel](CreatePatchType, "label-3", map[accesstypes.Field]any{"CrateNumber": int64(103), "Name": "Third"}),
		)
		for _, row := range []struct{ table, id string }{{"Bays", "3"}, {"Crates", "103"}, {"Labels", "label-3"}} {
			if diff := cmp.Diff([]string{south}, changeTenants(t, db, row.table, row.id)); diff != "" {
				t.Errorf("tenants of %s %s mismatch (-want +got):\n%s", row.table, row.id, diff)
			}
		}
	})

	t.Run("a label renamed after its crate's bay moved yard in the same transaction: the new yard", func(t *testing.T) {
		t.Parallel()

		seedChangeRows(t, db,
			spanner.InsertMap("Bays", map[string]any{"Number": int64(4), "YardId": north, "Name": "Fourth"}),
			spanner.InsertMap("Crates", map[string]any{"Number": int64(104), "BayNumber": int64(4), "Name": "Fourth"}),
			spanner.InsertMap("Labels", map[string]any{"Id": "label-4", "CrateNumber": int64(104), "Name": "Fourth"}),
		)
		applyTogether(t, client,
			numberPatch[changeBay](UpdatePatchType, 4, map[accesstypes.Field]any{"YardID": south}),
			changePatch[changeLabel](UpdatePatchType, "label-4", map[accesstypes.Field]any{"Name": "Renamed"}),
		)
		if diff := cmp.Diff([]string{south}, changeTenants(t, db, "Labels", "label-4")); diff != "" {
			t.Errorf("tenants mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("a stored pier moved by its key, then a slip created on its code: the new yard", func(t *testing.T) {
		t.Parallel()

		seedChangeRows(t, db, spanner.InsertMap("Piers", map[string]any{"Id": "pier-1", "Code": "code-1", "YardId": north, "Name": "First"}))
		applyTogether(t, client,
			changePatch[changePier](UpdatePatchType, "pier-1", map[accesstypes.Field]any{"YardID": south}),
			changePatch[changeSlip](CreatePatchType, "slip-1", map[accesstypes.Field]any{"PierCode": "code-1", "Name": "First"}),
		)
		if diff := cmp.Diff([]string{south}, changeTenants(t, db, "Slips", "slip-1")); diff != "" {
			t.Errorf("tenants mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("a stored pier moved by its key, then a stored slip on its code renamed: the new yard", func(t *testing.T) {
		t.Parallel()

		seedChangeRows(t, db,
			spanner.InsertMap("Piers", map[string]any{"Id": "pier-2", "Code": "code-2", "YardId": north, "Name": "Second"}),
			spanner.InsertMap("Slips", map[string]any{"Id": "slip-2", "PierCode": "code-2", "Name": "Second"}),
		)
		applyTogether(t, client,
			changePatch[changePier](UpdatePatchType, "pier-2", map[accesstypes.Field]any{"YardID": south}),
			changePatch[changeSlip](UpdatePatchType, "slip-2", map[accesstypes.Field]any{"Name": "Renamed"}),
		)
		if diff := cmp.Diff([]string{south}, changeTenants(t, db, "Slips", "slip-2")); diff != "" {
			t.Errorf("tenants mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("a slip created, its pier moved by its key, a second slip created: the old yard, then the new", func(t *testing.T) {
		t.Parallel()

		seedChangeRows(t, db, spanner.InsertMap("Piers", map[string]any{"Id": "pier-3", "Code": "code-3", "YardId": north, "Name": "Third"}))
		applyTogether(t, client,
			changePatch[changeSlip](CreatePatchType, "slip-3a", map[accesstypes.Field]any{"PierCode": "code-3", "Name": "Third"}),
			changePatch[changePier](UpdatePatchType, "pier-3", map[accesstypes.Field]any{"YardID": south}),
			changePatch[changeSlip](CreatePatchType, "slip-3b", map[accesstypes.Field]any{"PierCode": "code-3", "Name": "Third"}),
		)
		if diff := cmp.Diff([]string{north}, changeTenants(t, db, "Slips", "slip-3a")); diff != "" {
			t.Errorf("tenants of the first slip mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]string{south}, changeTenants(t, db, "Slips", "slip-3b")); diff != "" {
			t.Errorf("tenants of the second slip mismatch (-want +got):\n%s", diff)
		}
	})

	// The move is an insert-or-update. The fixture leaves a pier's code and name
	// nullable, since the emulator holds an insert-or-update of a row it has not stored
	// to every column that may not be null.
	t.Run("a pier created with its code, moved by its key, then a slip created: the new yard", func(t *testing.T) {
		t.Parallel()

		applyTogether(t, client,
			changePatch[changePier](CreatePatchType, "pier-4", map[accesstypes.Field]any{"Code": "code-4", "YardID": north, "Name": "Fourth"}),
			changePatch[changePier](CreateOrUpdatePatchType, "pier-4", map[accesstypes.Field]any{"YardID": south}),
			changePatch[changeSlip](CreatePatchType, "slip-4", map[accesstypes.Field]any{"PierCode": "code-4", "Name": "Fourth"}),
		)
		if diff := cmp.Diff([]string{south}, changeTenants(t, db, "Slips", "slip-4")); diff != "" {
			t.Errorf("tenants mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("another pier renamed by its key, then a slip on an untouched pier's code: that pier's yard, one read", func(t *testing.T) {
		t.Parallel()

		seedChangeRows(t, db,
			spanner.InsertMap("Piers", map[string]any{"Id": "pier-5a", "Code": "code-5a", "YardId": north, "Name": "Fifth"}),
			spanner.InsertMap("Piers", map[string]any{"Id": "pier-5b", "Code": "code-5b", "YardId": south, "Name": "Fifth"}),
		)
		var rows *transactionRows
		if err := client.ExecuteFunc(context.Background(), func(ctx context.Context, txn ReadWriteTransaction) error {
			recorder, ok := txn.(rowsRecorder)
			if !ok {
				return errors.Newf("the transaction is a %T, which keeps no record of its point reads", txn)
			}
			for _, w := range []changeWrite{
				changePatch[changePier](UpdatePatchType, "pier-5a", map[accesstypes.Field]any{"Name": "Renamed"}),
				changePatch[changeSlip](CreatePatchType, "slip-5", map[accesstypes.Field]any{"PierCode": "code-5b", "Name": "Fifth"}),
			} {
				if err := w.Buffer(ctx, txn, eventsource.ProcessEvent("test")); err != nil {
					return errors.Wrap(err, "changeWrite.Buffer()")
				}
			}
			rows = recorder.transactionRows()

			return nil
		}); err != nil {
			t.Fatalf("ExecuteFunc() error = %v", err)
		}
		if diff := cmp.Diff([]string{south}, changeTenants(t, db, "Slips", "slip-5")); diff != "" {
			t.Errorf("tenants mismatch (-want +got):\n%s", diff)
		}
		// The slip's one step is one read, by the code, which names the pier by its key.
		if len(rows.rowReads) != 1 || len(rows.reads) != 0 || len(rows.keyReads) != 0 {
			t.Errorf("the transaction read the database %d time(s) by a code, %d by a key column and %d by a row's key, want once by the code alone", len(rows.rowReads), len(rows.reads), len(rows.keyReads))
		}
		if _, ok := rows.rowReads[pointRead{table: "Piers", keyColumn: "Code", value: "code-5b", column: "YardId"}]; !ok {
			t.Errorf("the transaction's reads by a code = %+v, want the slip's pier by code-5b", rows.rowReads)
		}
	})

	t.Run("a stored pier's code changed by its key, then a slip created on the new code: the pier's yard", func(t *testing.T) {
		t.Parallel()

		seedChangeRows(t, db, spanner.InsertMap("Piers", map[string]any{"Id": "pier-6", "Code": "code-6", "YardId": north, "Name": "Sixth"}))
		applyTogether(t, client,
			changePatch[changePier](UpdatePatchType, "pier-6", map[accesstypes.Field]any{"Code": "code-6b"}),
			changePatch[changeSlip](CreatePatchType, "slip-6", map[accesstypes.Field]any{"PierCode": "code-6b", "Name": "Sixth"}),
		)
		if diff := cmp.Diff([]string{north}, changeTenants(t, db, "Slips", "slip-6")); diff != "" {
			t.Errorf("tenants mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("a crate with no bay: empty, and the write succeeds", func(t *testing.T) {
		t.Parallel()

		applyEach(t, client,
			numberPatch[changeCrate](CreatePatchType, 105, map[accesstypes.Field]any{"Name": "Fifth"}),
			numberPatch[changeCrate](UpdatePatchType, 105, map[accesstypes.Field]any{"Name": "Renamed"}),
		)
		if diff := cmp.Diff([]string{"", ""}, changeTenants(t, db, "Crates", "105")); diff != "" {
			t.Errorf("tenants mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("a berth with no dock: empty, and the write succeeds", func(t *testing.T) {
		t.Parallel()

		applyEach(t, client,
			changePatch[changeBerth](CreatePatchType, "berth-8", map[accesstypes.Field]any{"Name": "Eighth"}),
			changePatch[changeBerth](UpdatePatchType, "berth-8", map[accesstypes.Field]any{"Name": "Renamed"}),
		)
		if diff := cmp.Diff([]string{"", ""}, changeTenants(t, db, "Berths", "berth-8")); diff != "" {
			t.Errorf("tenants mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("two moorings of one berth deleted together: both carry the yard, read once", func(t *testing.T) {
		t.Parallel()

		seedChangeRows(t, db,
			spanner.InsertMap("Berths", map[string]any{"Id": "berth-9", "DockId": dockNorth, "Name": "Ninth"}),
			spanner.InsertMap("Moorings", map[string]any{"Id": "mooring-9a", "BerthId": "berth-9", "Name": "Ninth"}),
			spanner.InsertMap("Moorings", map[string]any{"Id": "mooring-9b", "BerthId": "berth-9", "Name": "Ninth"}),
		)
		var reads map[pointRead]pointAnswer
		if err := client.ExecuteFunc(context.Background(), func(ctx context.Context, txn ReadWriteTransaction) error {
			recorder, ok := txn.(rowsRecorder)
			if !ok {
				return errors.Newf("the transaction is a %T, which keeps no record of its point reads", txn)
			}
			for _, id := range []string{"mooring-9a", "mooring-9b"} {
				if err := changePatch[changeMooring](DeletePatchType, id, nil).Buffer(ctx, txn, eventsource.ProcessEvent("test")); err != nil {
					return err
				}
			}
			reads = maps.Clone(recorder.transactionRows().reads)

			return nil
		}); err != nil {
			t.Fatalf("ExecuteFunc() error = %v", err)
		}
		for _, id := range []string{"mooring-9a", "mooring-9b"} {
			if diff := cmp.Diff([]string{north}, changeTenants(t, db, "Moorings", id)); diff != "" {
				t.Errorf("tenants of %s mismatch (-want +got):\n%s", id, diff)
			}
		}
		// Each answer is held as Spanner returned it; the test reads it by its spelling.
		got := map[pointRead]string{}
		for read, answer := range reads {
			text, err := columnText(answer.value)
			if err != nil || !answer.found {
				t.Fatalf("the remembered read %+v answered %+v, %v; want a row", read, answer, err)
			}
			got[read] = text
		}
		want := map[pointRead]string{
			{table: "Berths", keyColumn: "Id", value: "berth-9", column: "DockId"}: dockNorth,
			{table: "Docks", keyColumn: "Id", value: dockNorth, column: "YardId"}:  north,
		}
		if diff := cmp.Diff(want, got, cmp.AllowUnexported(pointRead{})); diff != "" {
			t.Errorf("the reads the transaction remembers mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("an update that changes nothing writes no record", func(t *testing.T) {
		t.Parallel()

		seedChangeRows(t, db, spanner.InsertMap("Docks", map[string]any{"Id": "dock-10", "YardId": north, "Name": "Same"}))
		applyEach(t, client, changePatch[changeDock](UpdatePatchType, "dock-10", map[accesstypes.Field]any{"Name": "Same"}))
		if diff := cmp.Diff([]string{}, changeTenants(t, db, "Docks", "dock-10")); diff != "" {
			t.Errorf("tenants mismatch (-want +got):\n%s", diff)
		}
	})
}
