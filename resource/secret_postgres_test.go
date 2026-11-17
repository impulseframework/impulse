package resource

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
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
	if len(stored) != SecretOverhead+len(first) || storedVersion(stored) != 1 || bytes.Contains(stored, []byte(first)) {
		t.Errorf("the column holds %d bytes naming version %d, carrying the value as given: %v", len(stored), storedVersion(stored), bytes.Contains(stored, []byte(first)))
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

// TestSecret_retireOnPostgres retires a version of the application secret against a
// real PostgreSQL: every stored value that names the version is written again under the
// current one, more than one round's worth of them, a value under the current version
// and a row with no value are left as they were, every value then names the current
// version and opens to what it was set to, a second run writes nothing, the current
// version is refused, and a value that does not open fails the retirement naming its
// row. The rows are written with the driver, as TestSecret_onPostgres writes them.
func TestSecret_retireOnPostgres(t *testing.T) {
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
	db, err := container.CreateDatabase(ctx, "sealed-rows-retire")
	if err != nil {
		t.Fatalf("CreateDatabase() error = %v", err)
	}
	t.Cleanup(db.Close)
	if _, err := db.Exec(ctx, `CREATE TABLE "SealedRows" ("Id" text PRIMARY KEY, "Title" text NOT NULL, "AccessCode" bytea)`); err != nil {
		t.Fatalf("creating the table: %v", err)
	}

	const rows = secretRetireBatch + 5
	// write resolves a patch as the write path does, with the keys a service holds, and
	// stores its columns.
	write := func(keys *SecretKeys, id string, code *string) {
		t.Helper()

		p := NewPatchSet(NewMetadata[retiringRow]()).SetPatchType(CreatePatchType)
		p.SetKey("ID", id)
		p.Set("Title", "sealed")
		p.Set("AccessCode", code)
		columns, err := p.Resolve(PostgresDBType, keys)
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		if _, err := db.Exec(ctx, `INSERT INTO "SealedRows" ("Id", "Title", "AccessCode") VALUES ($1, $2, $3)
			ON CONFLICT ("Id") DO UPDATE SET "AccessCode" = EXCLUDED."AccessCode"`,
			columns["Id"], columns["Title"], columns["AccessCode"]); err != nil {
			t.Fatalf("writing the row: %v", err)
		}
	}
	stored := func() map[string][]byte {
		t.Helper()

		found, err := db.Query(ctx, `SELECT "Id", "AccessCode" FROM "SealedRows"`)
		if err != nil {
			t.Fatalf("reading the stored rows: %v", err)
		}
		defer found.Close()
		columns := map[string][]byte{}
		for found.Next() {
			var (
				id   string
				code []byte
			)
			if err := found.Scan(&id, &code); err != nil {
				t.Fatalf("reading a stored row: %v", err)
			}
			columns[id] = code
		}
		if err := found.Err(); err != nil {
			t.Fatalf("reading the stored rows: %v", err)
		}

		return columns
	}
	value := func(i int) string {
		return "code-of-row-" + strings.Repeat("0", 3-len(strconv.Itoa(i))) + strconv.Itoa(i)
	}
	rowID := func(i int) string {
		return "row-" + strings.Repeat("0", 3-len(strconv.Itoa(i))) + strconv.Itoa(i)
	}
	opens := func(keys *SecretKeys, version int) {
		t.Helper()

		columns := stored()
		for i := range rows {
			code := columns[rowID(i)]
			if storedVersion(code) != version {
				t.Fatalf("%s names version %d, want %d", rowID(i), storedVersion(code), version)
			}
			if got, err := keys.Open(rowKey(rowID(i)), Secret{sealed: code}); err != nil || got != value(i) {
				t.Fatalf("%s opens to another value, error %v", rowID(i), err)
			}
		}
	}

	// Before the rotation the service holds version 1 alone; after it, version 2 is the
	// current one, and a value set since names it.
	beforeKeys, rotatedKeys := testSecretKeys(t, 1), testSecretKeys(t, 1, 2)
	for i := range rows {
		write(beforeKeys, rowID(i), ptr(value(i)))
	}
	write(beforeKeys, "no-value", nil)
	write(rotatedKeys, "set-since", ptr("set-since-the-rotation"))
	setSince := stored()["set-since"]
	opens(rotatedKeys, 1)

	rotated := NewPostgresClient(db.Pool, WithSecretKeys(rotatedKeys))
	holder := SecretHolderOf[retiringRow]()
	for name, client := range map[string]Client{"APP_SECRET_KEY_2": rotated, "APP_SECRET_KEY_1": NewPostgresClient(db.Pool, WithSecretKeys(beforeKeys))} {
		if _, err := RetireSecretVersion(ctx, client, name, holder); err == nil || !strings.Contains(err.Error(), name+" is the current version of the application secret") {
			t.Fatalf("RetireSecretVersion(%s) by a service whose current version it is: error = %v, want a refusal", name, err)
		}
	}

	got, err := RetireSecretVersion(ctx, rotated, "APP_SECRET_KEY_1", holder)
	if err != nil {
		t.Fatalf("RetireSecretVersion() error = %v", err)
	}
	if got.Version != 1 || got.Current != 2 || got.Rewritten() != rows {
		t.Errorf("RetireSecretVersion() = %+v, want version 1 retired under version 2 with %d values rewritten", got, rows)
	}
	opens(rotatedKeys, 2)
	opens(testSecretKeys(t, 2), 2)
	after := stored()
	if !bytes.Equal(after["set-since"], setSince) {
		t.Errorf("the value already under the current version was written again")
	}
	if code, ok := after["no-value"]; !ok || code != nil {
		t.Errorf("the row with no value holds %d bytes, want none", len(code))
	}

	// A second run finds nothing to write.
	again, err := RetireSecretVersion(ctx, rotated, "APP_SECRET_KEY_1", holder)
	if err != nil || again.Rewritten() != 0 {
		t.Errorf("a second RetireSecretVersion() rewrote %v value(s), error %v; want none and no error", again, err)
	}

	// A value that does not open: bytes sealed under version 1 for one row, moved onto
	// another. The retirement fails naming the row, the value beside it is written all
	// the same, and the retirement passes once the row is cleared.
	write(beforeKeys, "moved-from", ptr("a-value-to-move"))
	if _, err := db.Exec(ctx, `INSERT INTO "SealedRows" ("Id", "Title", "AccessCode") SELECT 'moved-to', 'sealed', "AccessCode" FROM "SealedRows" WHERE "Id" = 'moved-from'`); err != nil {
		t.Fatalf("moving the stored bytes: %v", err)
	}
	_, err = RetireSecretVersion(ctx, rotated, "APP_SECRET_KEY_1", holder)
	if err == nil || !strings.Contains(err.Error(), "1 stored value(s) name version 1 and do not open") || !strings.Contains(err.Error(), "the row keyed ID moved-to") {
		t.Fatalf("RetireSecretVersion() over a value that does not open: error = %v, want a refusal naming the row", err)
	}
	if strings.Contains(err.Error(), "a-value-to-move") {
		t.Errorf("the refusal carries a value: %v", err)
	}
	if code := stored()["moved-from"]; storedVersion(code) != 2 {
		t.Errorf("the value beside the one that does not open still names version 1")
	}
	if _, err := db.Exec(ctx, `UPDATE "SealedRows" SET "AccessCode" = NULL WHERE "Id" = 'moved-to'`); err != nil {
		t.Fatalf("clearing the row: %v", err)
	}
	if again, err := RetireSecretVersion(ctx, rotated, "APP_SECRET_KEY_1", holder); err != nil || again.Rewritten() != 0 {
		t.Errorf("RetireSecretVersion() once the row is cleared rewrote %v value(s), error %v; want none and no error", again, err)
	}

	// The versions one byte cannot name. A deployment's version numbers never repeat, so
	// in time a secret's versions pass 255. Values are sealed under versions 256, 258
	// and 65535, the last there is, beside every value above, which names version 2.
	// Version 256 shares its high byte with 258, and 258 its low byte with 2: the
	// statement that finds a version's values tells each from the others by its whole
	// number.
	wideKeys := testSecretKeys(t, 2, 256, 258, 65535)
	wide := NewPostgresClient(db.Pool, WithSecretKeys(wideKeys))
	write(testSecretKeys(t, 2, 256), "under-256", ptr("sealed-under-256"))
	write(testSecretKeys(t, 2, 256, 258), "under-258", ptr("sealed-under-258"))
	write(wideKeys, "under-65535", ptr("sealed-under-65535"))
	// names asserts one row's stored bytes name the version and open to the row's value,
	// and answers them.
	names := func(id string, version int) []byte {
		t.Helper()

		code := stored()[id]
		if storedVersion(code) != version {
			t.Fatalf("%s names version %d, want %d", id, storedVersion(code), version)
		}
		if got, err := wideKeys.Open(rowKey(id), Secret{sealed: code}); err != nil || got != "sealed-"+id {
			t.Fatalf("%s opens to another value, error %v", id, err)
		}

		return code
	}
	names("under-256", 256)
	under258, under65535 := names("under-258", 258), names("under-65535", 65535)
	// untouched asserts the values a retirement had no business with are as they were.
	untouched := func(step string, want map[string][]byte) {
		t.Helper()

		now := stored()
		for id, code := range want {
			if !bytes.Equal(now[id], code) {
				t.Errorf("%s wrote %s, which names another version", step, id)
			}
		}
	}

	// Retiring version 256 writes its one value again under version 65535, and no other.
	got, err = RetireSecretVersion(ctx, wide, "APP_SECRET_KEY_256", holder)
	if err != nil {
		t.Fatalf("RetireSecretVersion(APP_SECRET_KEY_256) error = %v", err)
	}
	if got.Version != 256 || got.Current != 65535 || got.Rewritten() != 1 {
		t.Errorf("RetireSecretVersion(APP_SECRET_KEY_256) = %+v, want version 256 retired under version 65535 with 1 value rewritten", got)
	}
	names("under-256", 65535)
	opens(wideKeys, 2)
	untouched("retiring version 256", map[string][]byte{"under-258": under258, "under-65535": under65535})

	// Retiring version 2 writes every value that names it, the rows above and the two
	// set beside them, and leaves the value under version 258.
	got, err = RetireSecretVersion(ctx, wide, "APP_SECRET_KEY_2", holder)
	if err != nil {
		t.Fatalf("RetireSecretVersion(APP_SECRET_KEY_2) error = %v", err)
	}
	if got.Version != 2 || got.Current != 65535 || got.Rewritten() != rows+2 {
		t.Errorf("RetireSecretVersion(APP_SECRET_KEY_2) = %+v, want version 2 retired under version 65535 with %d values rewritten", got, rows+2)
	}
	opens(wideKeys, 65535)
	untouched("retiring version 2", map[string][]byte{"under-258": under258, "under-65535": under65535})

	// Version 258 last: every stored value then names version 65535, and a service
	// handed that version alone opens every one.
	got, err = RetireSecretVersion(ctx, wide, "APP_SECRET_KEY_258", holder)
	if err != nil || got.Rewritten() != 1 {
		t.Fatalf("RetireSecretVersion(APP_SECRET_KEY_258) = %+v, error %v; want 1 value rewritten", got, err)
	}
	names("under-258", 65535)
	untouched("retiring version 258", map[string][]byte{"under-65535": under65535})
	opens(testSecretKeys(t, 65535), 65535)
	for id, code := range stored() {
		if code != nil && storedVersion(code) != 65535 {
			t.Errorf("%s names version %d once every older version is retired, want 65535", id, storedVersion(code))
		}
	}

	// A second run of each writes nothing, and the last version there is cannot be
	// retired, being the current one.
	settled := stored()
	for _, variable := range []string{"APP_SECRET_KEY_2", "APP_SECRET_KEY_256", "APP_SECRET_KEY_258"} {
		if again, err := RetireSecretVersion(ctx, wide, variable, holder); err != nil || again.Rewritten() != 0 {
			t.Errorf("a second RetireSecretVersion(%s) rewrote %v value(s), error %v; want none and no error", variable, again, err)
		}
	}
	if _, err := RetireSecretVersion(ctx, wide, "APP_SECRET_KEY_65535", holder); err == nil || !strings.Contains(err.Error(), "APP_SECRET_KEY_65535 is the current version of the application secret") {
		t.Errorf("RetireSecretVersion(APP_SECRET_KEY_65535): error = %v, want the refusal of the current version", err)
	}
	for id, code := range stored() {
		if !bytes.Equal(code, settled[id]) {
			t.Errorf("the second runs changed the stored bytes of %s", id)
		}
	}
}
