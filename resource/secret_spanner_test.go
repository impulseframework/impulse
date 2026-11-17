package resource

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
)

// Over the emulator: a secret is stored sealed, never as it was given; it reads back
// into the resource struct and opens on its row; every change record of its row (the
// create's, an update's, the delete's) names the field and keeps neither value; and an
// update that sets the secret to what it holds changes nothing and writes no record.
func TestSpannerClient_secret_storedSealed(t *testing.T) {
	t.Parallel()

	db := forkDatabase(t, "sealed-rows")
	ctx := context.Background()
	keys := testSecretKeys(t, 1)
	client := NewSpannerClient(db, WithSecretKeys(keys))

	const first, second = "open-sesame", "close-sesame"
	sealed := func(patchType PatchType, id, title, code string) *PatchSet[sealedRow] {
		p := NewPatchSet(NewMetadata[sealedRow]()).SetPatchType(patchType)
		p.SetKey("ID", id)
		if title != "" {
			p.Set("Title", title)
		}
		if code != "" {
			p.Set("AccessCode", &code)
		}

		return p
	}
	records := func() []string {
		t.Helper()

		var sets []string
		stmt := spanner.NewStatement("SELECT TO_JSON_STRING(ChangeSet) FROM DataChangeEvents WHERE TableName = 'SealedRows' AND RowId = 'row-1' ORDER BY Sequence")
		if err := db.Single().Query(ctx, stmt).Do(func(row *spanner.Row) error {
			var set string
			if err := row.Column(0, &set); err != nil {
				return fmt.Errorf("reading the change set: %w", err)
			}
			sets = append(sets, set)

			return nil
		}); err != nil {
			t.Fatalf("reading the change records: %v", err)
		}

		return sets
	}
	// column reads the stored bytes as the database holds them, past the resource layer.
	column := func(id string) []byte {
		t.Helper()

		var code []byte
		stmt := spanner.NewStatement("SELECT AccessCode FROM SealedRows WHERE Id = @id")
		stmt.Params["id"] = id
		if err := db.Single().Query(ctx, stmt).Do(func(row *spanner.Row) error {
			return row.Column(0, &code)
		}); err != nil {
			t.Fatalf("reading the stored row: %v", err)
		}

		return code
	}
	// read reads the row through the resource layer, as application code does.
	read := func(id string) *sealedRow {
		t.Helper()

		q := NewQuerySet(NewMetadata[sealedRow]())
		q.SetKey("ID", id)
		q.AddField("ID")
		q.AddField("AccessCode")
		row, err := q.Read(ctx, client)
		if err != nil {
			t.Fatalf("reading the row through the resource layer: %v", err)
		}

		return &row.Data
	}
	// kept checks the records written so far: their number, the last one naming the
	// secret as changed, and none carrying a value the secret ever held.
	kept := func(step string, want int) {
		t.Helper()

		sets := records()
		if len(sets) != want {
			t.Fatalf("%s: the row has %d change record(s), want %d: %v", step, len(sets), want, sets)
		}
		if last := strings.ReplaceAll(sets[len(sets)-1], " ", ""); !strings.Contains(last, `"AccessCode":{"New":null,"Old":null,"Secret":true}`) {
			t.Errorf("%s: the record does not name the secret as changed, with neither value: %s", step, last)
		}
		for _, set := range sets {
			if strings.Contains(set, first) || strings.Contains(set, second) {
				t.Errorf("%s: a change record carries the secret: %s", step, set)
			}
		}
	}
	// holds checks the column: the sealed layout, no value as it was given, and bytes
	// that read back into the resource struct and open on the row to the value.
	holds := func(step, plain string) {
		t.Helper()

		stored := column("row-1")
		if len(stored) != SecretOverhead+len(plain) || stored[0] != 1 {
			t.Errorf("%s: the column holds %d bytes opening with version %d, want %d opening with 1", step, len(stored), stored[0], SecretOverhead+len(plain))
		}
		if bytes.Contains(stored, []byte(plain)) || bytes.Contains(stored, []byte(first)) || bytes.Contains(stored, []byte(second)) {
			t.Errorf("%s: the column holds the secret as it was given", step)
		}
		row := read("row-1")
		if row.AccessCode == nil || !bytes.Equal(row.AccessCode.sealed, stored) {
			t.Fatalf("%s: the resource layer did not read the column's bytes back unchanged", step)
		}
		if opened, err := keys.OpenNullable(rowKey(row.ID), row.AccessCode); err != nil || opened == nil || *opened != plain {
			t.Errorf("%s: the stored value opens to %v, error %v, want %q", step, opened, err, plain)
		}
	}

	if err := sealed(CreatePatchType, "row-1", "sealed", first).Apply(ctx, client, "test"); err != nil {
		t.Fatalf("the create: Apply() error = %v", err)
	}
	kept("after the create", 1)
	holds("after the create", first)

	if err := sealed(UpdatePatchType, "row-1", "", second).Apply(ctx, client, "test"); err != nil {
		t.Fatalf("the update: Apply() error = %v", err)
	}
	kept("after the update", 2)
	holds("after the update", second)

	// The same secret again changes nothing: no error, and no record. The column is
	// written again all the same, as other bytes that open to the same value.
	before := column("row-1")
	if err := sealed(UpdatePatchType, "row-1", "", second).Apply(ctx, client, "test"); err != nil {
		t.Fatalf("the update that changes nothing: Apply() error = %v", err)
	}
	kept("after the update that changes nothing", 2)
	holds("after the update that changes nothing", second)
	if bytes.Equal(before, column("row-1")) {
		t.Error("the same secret set again left the column's bytes as they were, want them sealed afresh")
	}

	// Bytes moved onto another row, past the resource layer, do not open there.
	if err := sealed(CreatePatchType, "row-2", "another", first).Apply(ctx, client, "test"); err != nil {
		t.Fatalf("the second row: Apply() error = %v", err)
	}
	if _, err := db.Apply(ctx, []*spanner.Mutation{spanner.Update("SealedRows", []string{"Id", "AccessCode"}, []any{"row-2", column("row-1")})}); err != nil {
		t.Fatalf("moving the bytes: %v", err)
	}
	moved := read("row-2")
	if _, err := keys.OpenNullable(rowKey(moved.ID), moved.AccessCode); !errors.Is(err, ErrSecretNotOpened) {
		t.Errorf("opening bytes moved from another row: error = %v, want ErrSecretNotOpened", err)
	}

	// A client built with no keys refuses the write, and stores nothing.
	unkeyed := NewSpannerClient(db)
	err := sealed(CreatePatchType, "row-3", "refused", first).Apply(ctx, unkeyed, "test")
	if err == nil || !strings.Contains(err.Error(), "SealedRows.AccessCode is a secret and the transaction holds no secret keys") {
		t.Errorf("a create through a client with no keys: error = %v", err)
	}
	if got := column("row-3"); got != nil {
		t.Errorf("a client with no keys stored %d bytes", len(got))
	}

	if err := sealed(DeletePatchType, "row-1", "", "").Apply(ctx, client, "test"); err != nil {
		t.Fatalf("the delete: Apply() error = %v", err)
	}
	kept("after the delete", 3)
}

// Over the emulator: after the application secret is rotated a value sealed under the
// old version still opens, a value set is sealed under the new one, and a value opened
// inside a read-write transaction is written back under the new one, with no change
// record, so the old version can be retired.
func TestSpannerClient_secret_rotation(t *testing.T) {
	t.Parallel()

	db := forkDatabase(t, "sealed-rows-rotation")
	ctx := context.Background()
	const plain = "open-sesame"

	create := func(client Client, id string) {
		t.Helper()

		p := NewPatchSet(NewMetadata[sealedRow]()).SetPatchType(CreatePatchType)
		p.SetKey("ID", id)
		p.Set("Title", "sealed")
		p.Set("AccessCode", ptr(plain))
		if err := p.Apply(ctx, client, "test"); err != nil {
			t.Fatalf("creating %s: Apply() error = %v", id, err)
		}
	}
	version := func(id string) int {
		t.Helper()

		var code []byte
		stmt := spanner.NewStatement("SELECT AccessCode FROM SealedRows WHERE Id = @id")
		stmt.Params["id"] = id
		if err := db.Single().Query(ctx, stmt).Do(func(row *spanner.Row) error {
			return row.Column(0, &code)
		}); err != nil || len(code) == 0 {
			t.Fatalf("reading the stored row %s: %d bytes, error %v", id, len(code), err)
		}

		return int(code[0])
	}
	read := func(txn ReadOnlyTransaction, id string) *sealedRow {
		t.Helper()

		q := NewQuerySet(NewMetadata[sealedRow]())
		q.SetKey("ID", id)
		q.AddField("ID")
		q.AddField("AccessCode")
		row, err := q.Read(ctx, txn)
		if err != nil {
			t.Fatalf("reading %s: %v", id, err)
		}

		return &row.Data
	}
	recordCount := func() int64 {
		t.Helper()

		var count int64
		if err := db.Single().Query(ctx, spanner.NewStatement("SELECT COUNT(*) FROM DataChangeEvents WHERE TableName = 'SealedRows'")).Do(func(row *spanner.Row) error {
			return row.Column(0, &count)
		}); err != nil {
			t.Fatalf("counting the change records: %v", err)
		}

		return count
	}

	// Before the rotation: two rows sealed under version 1.
	before := NewSpannerClient(db, WithSecretKeys(testSecretKeys(t, 1)))
	create(before, "row-1")
	create(before, "row-2")

	// After it the service holds versions 1 and 2.
	rotatedKeys := testSecretKeys(t, 1, 2)
	rotated := NewSpannerClient(db, WithSecretKeys(rotatedKeys))
	if opened, err := rotatedKeys.OpenNullable(rowKey("row-1"), read(rotated, "row-1").AccessCode); err != nil || opened == nil || *opened != plain {
		t.Fatalf("a version 1 value after the rotation opens to %v, error %v", opened, err)
	}
	if version("row-1") != 1 {
		t.Errorf("opening outside a transaction rewrote the value: version %d", version("row-1"))
	}
	create(rotated, "row-3")
	if version("row-3") != 2 {
		t.Errorf("a value set after the rotation is sealed under version %d, want 2", version("row-3"))
	}

	// Opened inside a read-write transaction, row-1's value is written back under
	// version 2, and no change record says the secret changed, since it did not.
	records := recordCount()
	if err := rotated.ExecuteFunc(ctx, func(ctx context.Context, txn ReadWriteTransaction) error {
		row := read(txn, "row-1")
		opened, err := OpenNullableSecretIn[sealedRow](ctx, txn, rowKey(row.ID), "AccessCode", row.AccessCode)
		if err != nil {
			return err
		}
		if opened == nil || *opened != plain {
			return errors.Newf("opened %v, want %q", opened, plain)
		}

		return nil
	}); err != nil {
		t.Fatalf("opening inside a transaction: %v", err)
	}
	if version("row-1") != 2 {
		t.Errorf("a value opened inside a read-write transaction stays under version %d, want it rewritten under 2", version("row-1"))
	}
	if got := recordCount(); got != records {
		t.Errorf("the rewrite wrote %d change record(s), want none", got-records)
	}

	// The old version retired: the rewritten value and the new one open, and the one
	// never rewritten names the version that is gone.
	retiredKeys := testSecretKeys(t, 2)
	retired := NewSpannerClient(db, WithSecretKeys(retiredKeys))
	for _, id := range []string{"row-1", "row-3"} {
		if opened, err := retiredKeys.OpenNullable(rowKey(id), read(retired, id).AccessCode); err != nil || opened == nil || *opened != plain {
			t.Errorf("%s after version 1 is retired opens to %v, error %v", id, opened, err)
		}
	}
	var unknown *UnknownSecretVersionError
	if _, err := retiredKeys.OpenNullable(rowKey("row-2"), read(retired, "row-2").AccessCode); !errors.As(err, &unknown) || unknown.Version != 1 {
		t.Errorf("row-2, never rewritten, after version 1 is retired: error = %v, want an UnknownSecretVersionError naming 1", err)
	}
}
