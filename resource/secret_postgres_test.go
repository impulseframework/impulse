package resource

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-playground/errors/v5"
	initiator "github.com/impulseframework/impulse/db"
)

// TestSecret_onPostgres runs the write path's handling of a secret against a real
// PostgreSQL: the columns a patch resolves to for PostgreSQL carry the secret sealed,
// the driver stores the sealed bytes in a bytea column unchanged and reads them back
// into the resource struct's type, the column holds nothing of the value as it was
// given, and the change set names the field with neither value. The PostgreSQL client's
// own transactions are not built yet, so the columns are written with the driver.
func TestSecret_onPostgres(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("requires a PostgreSQL container")
	}
	ctx := context.Background()
	container, err := initiator.NewPostgresContainer(ctx, "16")
	if err != nil {
		t.Fatalf("initiator.NewPostgresContainer() error = %v", err)
	}
	t.Cleanup(func() {
		container.Close()
		if err := container.Terminate(context.Background()); err != nil {
			t.Error(err)
		}
	})
	db, err := container.CreateDatabase(ctx, "sealed-rows")
	if err != nil {
		t.Fatalf("CreateDatabase() error = %v", err)
	}
	t.Cleanup(db.Close)
	if _, err := db.Exec(ctx, `CREATE TABLE "SealedRows" ("Id" text PRIMARY KEY, "Title" text NOT NULL, "AccessCode" bytea)`); err != nil {
		t.Fatalf("creating the table: %v", err)
	}

	const first, second = "open-sesame", "close-sesame"
	keys := testSecretKeys(t, 1)
	patch := func(patchType PatchType, id string, code *string) *PatchSet[sealedRow] {
		p := NewPatchSet(NewMetadata[sealedRow]()).SetPatchType(patchType)
		p.SetKey("ID", id)
		p.Set("Title", "sealed")
		p.Set("AccessCode", code)

		return p
	}
	// write resolves the patch as the write path does and stores its columns.
	write := func(p *PatchSet[sealedRow]) {
		t.Helper()

		columns, err := p.Resolve(PostgresDBType, keys)
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		if _, err := db.Exec(ctx, `INSERT INTO "SealedRows" ("Id", "Title", "AccessCode") VALUES ($1, $2, $3)
			ON CONFLICT ("Id") DO UPDATE SET "Title" = EXCLUDED."Title", "AccessCode" = EXCLUDED."AccessCode"`,
			columns["Id"], columns["Title"], columns["AccessCode"]); err != nil {
			t.Fatalf("writing the row: %v", err)
		}
	}
	// column reads the stored bytes as the database holds them.
	column := func(id string) []byte {
		t.Helper()

		var stored []byte
		if err := db.QueryRow(ctx, `SELECT "AccessCode" FROM "SealedRows" WHERE "Id" = $1`, id).Scan(&stored); err != nil {
			t.Fatalf("reading the stored bytes: %v", err)
		}

		return stored
	}
	// read reads the row into the resource struct, whose secret field is a pointer.
	read := func(id string) sealedRow {
		t.Helper()

		var row sealedRow
		if err := db.QueryRow(ctx, `SELECT "Id", "Title", "AccessCode" FROM "SealedRows" WHERE "Id" = $1`, id).Scan(&row.ID, &row.Title, &row.AccessCode); err != nil {
			t.Fatalf("reading the row: %v", err)
		}

		return row
	}

	// The create's change set names the secret and holds nothing of it.
	create := patch(CreatePatchType, "row-1", ptr(first))
	changes, err := create.insertChangeSet()
	if err != nil {
		t.Fatalf("insertChangeSet() error = %v", err)
	}
	record, err := json.Marshal(changes)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if !strings.Contains(string(record), `"AccessCode":{"Old":null,"New":null,"Secret":true}`) || strings.Contains(string(record), first) {
		t.Errorf("the create's change set = %s, want the secret named with neither value", record)
	}

	write(create)
	stored := column("row-1")
	if len(stored) != SecretOverhead+len(first) || stored[0] != 1 || bytes.Contains(stored, []byte(first)) {
		t.Errorf("the column holds %d bytes opening with version %d, carrying the value as given: %v", len(stored), stored[0], bytes.Contains(stored, []byte(first)))
	}
	row := read("row-1")
	if row.AccessCode == nil || !bytes.Equal(row.AccessCode.sealed, stored) {
		t.Fatal("the driver did not read the column's bytes back unchanged")
	}
	if opened, err := keys.OpenNullable(rowKey(row.ID), row.AccessCode); err != nil || opened == nil || *opened != first {
		t.Errorf("the stored value opens to %v, error %v, want %q", opened, err, first)
	}

	// An update's change set, against the row as it is stored: the same value is no
	// change, another is one, named with neither value.
	if same, err := patch(UpdatePatchType, "row-1", ptr(first)).diff(&row, keys); err != nil || same["AccessCode"].Secret {
		t.Errorf("the value the row holds, set again: changes %v, error %v, want none of the secret", same, err)
	}
	update := patch(UpdatePatchType, "row-1", ptr(second))
	changed, err := update.diff(&row, keys)
	if err != nil || !changed["AccessCode"].Secret || changed["AccessCode"].Old != nil || changed["AccessCode"].New != nil {
		t.Errorf("another value: changes %v, error %v, want the secret named with neither value", changed, err)
	}
	write(update)
	if stored := column("row-1"); bytes.Contains(stored, []byte(second)) || len(stored) != SecretOverhead+len(second) {
		t.Errorf("after the update the column holds %d bytes, carrying the value as given: %v", len(stored), bytes.Contains(stored, []byte(second)))
	}
	if opened, err := keys.OpenNullable(rowKey("row-1"), read("row-1").AccessCode); err != nil || opened == nil || *opened != second {
		t.Errorf("after the update the stored value opens to %v, error %v, want %q", opened, err, second)
	}

	// No value is NULL, which reads back as no Secret.
	write(patch(CreatePatchType, "row-2", nil))
	if got := read("row-2").AccessCode; got != nil {
		t.Errorf("a row with no secret read back %d stored bytes", len(got.sealed))
	}
	var plain Secret
	if err := db.QueryRow(ctx, `SELECT "AccessCode" FROM "SealedRows" WHERE "Id" = 'row-2'`).Scan(&plain); err != nil || !plain.IsZero() {
		t.Errorf("NULL read into a Secret: holds a value %v, error %v", !plain.IsZero(), err)
	}

	// Bytes moved onto another row, past the write path, do not open there.
	if _, err := db.Exec(ctx, `UPDATE "SealedRows" SET "AccessCode" = (SELECT "AccessCode" FROM "SealedRows" WHERE "Id" = 'row-1') WHERE "Id" = 'row-2'`); err != nil {
		t.Fatalf("moving the bytes: %v", err)
	}
	moved := read("row-2")
	if _, err := keys.OpenNullable(rowKey(moved.ID), moved.AccessCode); !errors.Is(err, ErrSecretNotOpened) {
		t.Errorf("opening bytes moved from another row: error = %v, want ErrSecretNotOpened", err)
	}
}
