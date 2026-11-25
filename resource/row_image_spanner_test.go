package resource

import (
	"context"
	"encoding/json"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/eventsource"
	"github.com/impulseframework/impulse/httpio"
	"google.golang.org/grpc/codes"
)

// changeRecord is one change record of a row as the database holds it.
type changeRecord struct {
	Sequence  int64
	ChangeSet map[accesstypes.Field]DiffElem
	TenantID  string
}

// changeRecords reads past the library the change records of one row, oldest first.
func changeRecords(t *testing.T, db *spanner.Client, table, row string) []changeRecord {
	t.Helper()

	stmt := spanner.NewStatement("SELECT Sequence, TO_JSON_STRING(ChangeSet), TenantId FROM DataChangeEvents WHERE TableName = @table AND RowId = @row ORDER BY EventTime, Sequence")
	stmt.Params["table"] = table
	stmt.Params["row"] = row
	records := []changeRecord{}
	if err := db.Single().Query(context.Background(), stmt).Do(func(r *spanner.Row) error {
		var record changeRecord
		var changeSet string
		var tenant spanner.NullString
		if err := r.Columns(&record.Sequence, &changeSet, &tenant); err != nil {
			return errors.Wrap(err, "spanner.Row.Columns()")
		}
		record.TenantID = tenant.StringVal
		if err := json.Unmarshal([]byte(changeSet), &record.ChangeSet); err != nil {
			return errors.Wrap(err, "json.Unmarshal()")
		}
		records = append(records, record)

		return nil
	}); err != nil {
		t.Fatalf("reading the change records of %s %s: %v", table, row, err)
	}

	return records
}

// storedNotice reads past the library the body a Notices row holds, and whether it is
// stored.
func storedNotice(t *testing.T, db *spanner.Client, id string) (body string, stored bool) {
	t.Helper()

	row, err := db.Single().ReadRow(context.Background(), "Notices", spanner.Key{id}, []string{"Body"})
	if err != nil {
		if spanner.ErrCode(err) == codes.NotFound {
			return "", false
		}
		t.Fatalf("spanner.ReadOnlyTransaction.ReadRow(%s) error = %v", id, err)
	}
	if err := row.Columns(&body); err != nil {
		t.Fatalf("spanner.Row.Columns(%s) error = %v", id, err)
	}

	return body, true
}

// notice builds a patch of one Notices row; an empty body sets nothing.
func notice(patchType PatchType, id, body string) *PatchSet[changeNotice] {
	var fields map[accesstypes.Field]any
	if body != "" {
		fields = map[accesstypes.Field]any{"Body": body}
	}

	return changePatch[changeNotice](patchType, id, fields)
}

// seedNotice writes a Notices row whose body is "first" past the library, so it has no
// change records.
func seedNotice(t *testing.T, db *spanner.Client, id string) {
	t.Helper()

	seedChangeRows(t, db, spanner.InsertMap("Notices", map[string]any{"Id": id, "Body": "first"}))
}

// changed is a field's entry in a change record, as the record reads back.
func changed(oldV, newV any) DiffElem {
	return DiffElem{Old: oldV, New: newV}
}

// TestChangeRecordOldValues_spanner pins, through the library's own writes on the
// emulator, the old values of the change records of a row one transaction writes more
// than once: each record's old values are the row as that transaction's earlier patches
// left it, a row it created is there though no read sees it, and a row it deleted is not.
func TestChangeRecordOldValues_spanner(t *testing.T) {
	t.Parallel()

	db := changeDatabase(t, "change-old-values")
	client := NewSpannerClient(db)
	north, south := changeYardNorth, changeYardSouth

	t.Run("created and updated: the update's old values are the create's", func(t *testing.T) {
		t.Parallel()

		applyTogether(t, client, notice(CreatePatchType, "n-1", "first"), notice(UpdatePatchType, "n-1", "second"))
		want := []changeRecord{
			{Sequence: 1, ChangeSet: map[accesstypes.Field]DiffElem{"Body": changed(nil, "first")}},
			{Sequence: 2, ChangeSet: map[accesstypes.Field]DiffElem{"Body": changed("first", "second")}},
		}
		if diff := cmp.Diff(want, changeRecords(t, db, "Notices", "n-1")); diff != "" {
			t.Errorf("records mismatch (-want +got):\n%s", diff)
		}
		if body, _ := storedNotice(t, db, "n-1"); body != "second" {
			t.Errorf("the row holds %q, want %q", body, "second")
		}
	})

	t.Run("created and updated to the value it was created with: one record", func(t *testing.T) {
		t.Parallel()

		applyTogether(t, client, notice(CreatePatchType, "n-2", "first"), notice(UpdatePatchType, "n-2", "first"))
		want := []changeRecord{{Sequence: 1, ChangeSet: map[accesstypes.Field]DiffElem{"Body": changed(nil, "first")}}}
		if diff := cmp.Diff(want, changeRecords(t, db, "Notices", "n-2")); diff != "" {
			t.Errorf("records mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("created and deleted: both records, and nothing stored", func(t *testing.T) {
		t.Parallel()

		applyTogether(t, client, notice(CreatePatchType, "n-3", "first"), notice(DeletePatchType, "n-3", ""))
		want := []changeRecord{
			{Sequence: 1, ChangeSet: map[accesstypes.Field]DiffElem{"Body": changed(nil, "first")}},
			{Sequence: 2, ChangeSet: map[accesstypes.Field]DiffElem{"ID": changed("n-3", nil), "Body": changed("first", nil)}},
		}
		if diff := cmp.Diff(want, changeRecords(t, db, "Notices", "n-3")); diff != "" {
			t.Errorf("records mismatch (-want +got):\n%s", diff)
		}
		if _, stored := storedNotice(t, db, "n-3"); stored {
			t.Error("the row is stored, want none")
		}
	})

	t.Run("a stored row updated twice: a record for each step", func(t *testing.T) {
		t.Parallel()

		seedNotice(t, db, "n-4")
		applyTogether(t, client, notice(UpdatePatchType, "n-4", "second"), notice(UpdatePatchType, "n-4", "third"))
		want := []changeRecord{
			{Sequence: 1, ChangeSet: map[accesstypes.Field]DiffElem{"Body": changed("first", "second")}},
			{Sequence: 2, ChangeSet: map[accesstypes.Field]DiffElem{"Body": changed("second", "third")}},
		}
		if diff := cmp.Diff(want, changeRecords(t, db, "Notices", "n-4")); diff != "" {
			t.Errorf("records mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("a stored row updated and put back: the change back is recorded", func(t *testing.T) {
		t.Parallel()

		seedNotice(t, db, "n-5")
		applyTogether(t, client, notice(UpdatePatchType, "n-5", "second"), notice(UpdatePatchType, "n-5", "first"))
		want := []changeRecord{
			{Sequence: 1, ChangeSet: map[accesstypes.Field]DiffElem{"Body": changed("first", "second")}},
			{Sequence: 2, ChangeSet: map[accesstypes.Field]DiffElem{"Body": changed("second", "first")}},
		}
		if diff := cmp.Diff(want, changeRecords(t, db, "Notices", "n-5")); diff != "" {
			t.Errorf("records mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("a stored row updated and deleted: the delete's old values are the update's", func(t *testing.T) {
		t.Parallel()

		seedNotice(t, db, "n-6")
		applyTogether(t, client, notice(UpdatePatchType, "n-6", "second"), notice(DeletePatchType, "n-6", ""))
		want := []changeRecord{
			{Sequence: 1, ChangeSet: map[accesstypes.Field]DiffElem{"Body": changed("first", "second")}},
			{Sequence: 2, ChangeSet: map[accesstypes.Field]DiffElem{"ID": changed("n-6", nil), "Body": changed("second", nil)}},
		}
		if diff := cmp.Diff(want, changeRecords(t, db, "Notices", "n-6")); diff != "" {
			t.Errorf("records mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("a stored row deleted and then updated: the update is not found", func(t *testing.T) {
		t.Parallel()

		seedNotice(t, db, "n-7")
		var updateErr error
		err := client.ExecuteFunc(context.Background(), func(ctx context.Context, txn ReadWriteTransaction) error {
			if err := notice(DeletePatchType, "n-7", "").Buffer(ctx, txn, eventsource.ProcessEvent("test")); err != nil {
				return err
			}
			updateErr = notice(UpdatePatchType, "n-7", "second").Buffer(ctx, txn, eventsource.ProcessEvent("test"))

			return updateErr
		})
		if updateErr == nil || !httpio.HasNotFound(updateErr) {
			t.Errorf("the update's Buffer() error = %v, want not found", updateErr)
		}
		if err == nil {
			t.Error("ExecuteFunc() error = nil, want the update's")
		}
		if body, stored := storedNotice(t, db, "n-7"); !stored || body != "first" {
			t.Errorf("the row holds %q (stored %t), want %q", body, stored, "first")
		}
	})

	t.Run("a stored dock deleted and written again by an insert-or-update: an insert's record", func(t *testing.T) {
		t.Parallel()

		seedChangeRows(t, db, spanner.InsertMap("Docks", map[string]any{"Id": "dock-8", "YardId": north, "Name": "North dock"}))
		applyTogether(t, client,
			changePatch[changeDock](DeletePatchType, "dock-8", nil),
			changePatch[changeDock](CreateOrUpdatePatchType, "dock-8", map[accesstypes.Field]any{"YardID": north, "Name": "Again"}),
		)
		want := []changeRecord{
			{Sequence: 1, TenantID: north, ChangeSet: map[accesstypes.Field]DiffElem{
				"ID": changed("dock-8", nil), "YardID": changed(north, nil), "Name": changed("North dock", nil),
			}},
			{Sequence: 2, TenantID: north, ChangeSet: map[accesstypes.Field]DiffElem{
				"YardID": changed(nil, north), "Name": changed(nil, "Again"),
			}},
		}
		if diff := cmp.Diff(want, changeRecords(t, db, "Docks", "dock-8")); diff != "" {
			t.Errorf("records mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("a stored row deleted twice: one record", func(t *testing.T) {
		t.Parallel()

		seedNotice(t, db, "n-9")
		applyTogether(t, client, notice(DeletePatchType, "n-9", ""), notice(DeletePatchType, "n-9", ""))
		want := []changeRecord{{Sequence: 1, ChangeSet: map[accesstypes.Field]DiffElem{"ID": changed("n-9", nil), "Body": changed("first", nil)}}}
		if diff := cmp.Diff(want, changeRecords(t, db, "Notices", "n-9")); diff != "" {
			t.Errorf("records mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("a row not stored, written by an insert-or-update and updated: the update's old values are the insert-or-update's", func(t *testing.T) {
		t.Parallel()

		applyTogether(t, client, notice(CreateOrUpdatePatchType, "n-10", "first"), notice(UpdatePatchType, "n-10", "second"))
		want := []changeRecord{
			{Sequence: 1, ChangeSet: map[accesstypes.Field]DiffElem{"Body": changed(nil, "first")}},
			{Sequence: 2, ChangeSet: map[accesstypes.Field]DiffElem{"Body": changed("first", "second")}},
		}
		if diff := cmp.Diff(want, changeRecords(t, db, "Notices", "n-10")); diff != "" {
			t.Errorf("records mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("docks created and written again in the south yard: every record carries the yard", func(t *testing.T) {
		t.Parallel()

		applyTogether(t, client,
			changePatch[changeDock](CreatePatchType, "dock-11", map[accesstypes.Field]any{"YardID": south, "Name": "Eleventh"}),
			changePatch[changeDock](UpdatePatchType, "dock-11", map[accesstypes.Field]any{"Name": "Renamed"}),
		)
		applyTogether(t, client,
			changePatch[changeDock](CreatePatchType, "dock-11b", map[accesstypes.Field]any{"YardID": south, "Name": "Eleventh"}),
			changePatch[changeDock](DeletePatchType, "dock-11b", nil),
		)
		renamed := []changeRecord{
			{Sequence: 1, TenantID: south, ChangeSet: map[accesstypes.Field]DiffElem{"YardID": changed(nil, south), "Name": changed(nil, "Eleventh")}},
			{Sequence: 2, TenantID: south, ChangeSet: map[accesstypes.Field]DiffElem{"Name": changed("Eleventh", "Renamed")}},
		}
		if diff := cmp.Diff(renamed, changeRecords(t, db, "Docks", "dock-11")); diff != "" {
			t.Errorf("records of the renamed dock mismatch (-want +got):\n%s", diff)
		}
		deleted := []changeRecord{
			{Sequence: 1, TenantID: south, ChangeSet: map[accesstypes.Field]DiffElem{"YardID": changed(nil, south), "Name": changed(nil, "Eleventh")}},
			{Sequence: 2, TenantID: south, ChangeSet: map[accesstypes.Field]DiffElem{
				"ID": changed("dock-11b", nil), "YardID": changed(south, nil), "Name": changed("Eleventh", nil),
			}},
		}
		if diff := cmp.Diff(deleted, changeRecords(t, db, "Docks", "dock-11b")); diff != "" {
			t.Errorf("records of the deleted dock mismatch (-want +got):\n%s", diff)
		}
	})
}

// TestChangeRecordOldValues_spanner_secret pins, on the emulator, the records of a row
// with a secret that one transaction creates and then updates: the update's old value of
// the secret is the one the create sealed, so the same secret again writes no record and
// another writes one that names the field and keeps neither value.
func TestChangeRecordOldValues_spanner_secret(t *testing.T) {
	t.Parallel()

	db := forkDatabase(t, "change-old-values-sealed")
	client := NewSpannerClient(db, WithSecretKeys(testSecretKeys(t, 1)))
	const first, second = "open-sesame", "close-sesame"
	sealed := func(patchType PatchType, id string, title, code *string) *PatchSet[sealedRow] {
		p := NewPatchSet(NewMetadata[sealedRow]()).SetPatchType(patchType)
		p.SetKey("ID", id)
		if title != nil {
			p.Set("Title", *title)
		}
		p.Set("AccessCode", code)

		return p
	}
	title, same, other := "sealed", first, second

	applyTogether(t, client, sealed(CreatePatchType, "same", &title, &same), sealed(UpdatePatchType, "same", nil, &same))
	want := []changeRecord{{Sequence: 1, ChangeSet: map[accesstypes.Field]DiffElem{"Title": changed(nil, "sealed"), "AccessCode": {Secret: true}}}}
	if diff := cmp.Diff(want, changeRecords(t, db, "SealedRows", "same")); diff != "" {
		t.Errorf("records given the same secret again mismatch (-want +got):\n%s", diff)
	}

	applyTogether(t, client, sealed(CreatePatchType, "other", &title, &same), sealed(UpdatePatchType, "other", nil, &other))
	want = []changeRecord{
		{Sequence: 1, ChangeSet: map[accesstypes.Field]DiffElem{"Title": changed(nil, "sealed"), "AccessCode": {Secret: true}}},
		{Sequence: 2, ChangeSet: map[accesstypes.Field]DiffElem{"AccessCode": {Secret: true}}},
	}
	if diff := cmp.Diff(want, changeRecords(t, db, "SealedRows", "other")); diff != "" {
		t.Errorf("records given another secret mismatch (-want +got):\n%s", diff)
	}
}

// TestSpannerReadWriteTransaction_changeRecordsNotNoted pins, on the emulator, that the
// transaction's record of the rows it wrote keeps nothing of a change record: after a
// tracked create and update of one row in one transaction it holds the row's two writes
// and nothing under DataChangeEvents, and the row's change records are written as before.
func TestSpannerReadWriteTransaction_changeRecordsNotNoted(t *testing.T) {
	t.Parallel()

	db := changeDatabase(t, "transaction-rows-records")
	client := NewSpannerClient(db)

	if err := client.ExecuteFunc(t.Context(), func(ctx context.Context, txn ReadWriteTransaction) error {
		for _, p := range []*PatchSet[changeNotice]{notice(CreatePatchType, "n-noted", "first"), notice(UpdatePatchType, "n-noted", "second")} {
			if err := p.Buffer(ctx, txn, eventsource.ProcessEvent("test")); err != nil {
				return err
			}
		}
		rw, ok := txn.(*SpannerReadWriteTransaction)
		if !ok {
			t.Fatalf("the transaction is %T, want *SpannerReadWriteTransaction", txn)
		}
		var kinds []PatchType
		for _, write := range rw.rows.writes["Notices"] {
			kinds = append(kinds, write.patchType)
		}
		if diff := cmp.Diff([]PatchType{CreatePatchType, UpdatePatchType}, kinds); diff != "" {
			t.Errorf("the record of the transaction's writes of Notices mismatch (-want +got):\n%s", diff)
		}
		if got := rw.rows.writes["DataChangeEvents"]; len(got) != 0 {
			t.Errorf("the record of the transaction's writes holds %d of DataChangeEvents, want none", len(got))
		}
		if got := rw.rows.images["DataChangeEvents"]; len(got) != 0 {
			t.Errorf("the record of the transaction's row images holds %d of DataChangeEvents, want none", len(got))
		}

		return nil
	}); err != nil {
		t.Fatalf("ExecuteFunc() error = %v", err)
	}

	want := []changeRecord{
		{Sequence: 1, ChangeSet: map[accesstypes.Field]DiffElem{"Body": changed(nil, "first")}},
		{Sequence: 2, ChangeSet: map[accesstypes.Field]DiffElem{"Body": changed("first", "second")}},
	}
	if diff := cmp.Diff(want, changeRecords(t, db, "Notices", "n-noted")); diff != "" {
		t.Errorf("the change records mismatch (-want +got):\n%s", diff)
	}
}
