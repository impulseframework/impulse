package resource

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
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
		if len(stored) != SecretOverhead+len(plain) || storedVersion(stored) != 1 {
			t.Errorf("%s: the column holds %d bytes naming version %d, want %d naming version 1", step, len(stored), storedVersion(stored), SecretOverhead+len(plain))
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

		return storedVersion(code)
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

// Over the emulator: retiring a version of the application secret writes every stored
// value that names it again under the current version, more than one transaction's
// worth of them, and leaves a value under the current version, a row with no value and
// the change records as they were; every value then names the current version and opens
// to what it was set to, a service handed the current version alone opens them, a second
// run writes nothing, the current version is refused, and a value that does not open
// fails the retirement naming its row and never a value.
func TestSpannerClient_secret_retire(t *testing.T) {
	t.Parallel()

	db := forkDatabase(t, "sealed-rows-retire")
	ctx := context.Background()
	const rows = secretRetireBatch + 5

	write := func(client Client, patchType PatchType, id string, code *string) {
		t.Helper()

		p := NewPatchSet(NewMetadata[retiringRow]()).SetPatchType(patchType)
		p.SetKey("ID", id)
		p.Set("Title", "sealed")
		p.Set("AccessCode", code)
		if err := p.Apply(ctx, client, "test"); err != nil {
			t.Fatalf("writing %s: Apply() error = %v", id, err)
		}
	}
	// stored reads every row's stored bytes as the database holds them.
	stored := func() map[string][]byte {
		t.Helper()

		columns := map[string][]byte{}
		if err := db.Single().Query(ctx, spanner.NewStatement("SELECT Id, AccessCode FROM SealedRows")).Do(func(row *spanner.Row) error {
			var (
				id   string
				code []byte
			)
			if err := row.Columns(&id, &code); err != nil {
				return fmt.Errorf("reading the stored row: %w", err)
			}
			columns[id] = code

			return nil
		}); err != nil {
			t.Fatalf("reading the stored rows: %v", err)
		}

		return columns
	}
	records := func() int64 {
		t.Helper()

		var count int64
		if err := db.Single().Query(ctx, spanner.NewStatement("SELECT COUNT(*) FROM DataChangeEvents")).Do(func(row *spanner.Row) error {
			return row.Column(0, &count)
		}); err != nil {
			t.Fatalf("counting the change records: %v", err)
		}

		return count
	}
	value := func(i int) string {
		return fmt.Sprintf("code-of-row-%03d", i)
	}
	// opens asserts every stored value names the version and opens to its own value.
	opens := func(keys *SecretKeys, version int) {
		t.Helper()

		columns := stored()
		for i := range rows {
			id := fmt.Sprintf("row-%03d", i)
			code := columns[id]
			if storedVersion(code) != version {
				t.Fatalf("%s names version %d, want %d", id, storedVersion(code), version)
			}
			if got, err := keys.Open(rowKey(id), Secret{sealed: code}); err != nil || got != value(i) {
				t.Fatalf("%s opens to another value, error %v", id, err)
			}
		}
	}

	// Before the rotation the service holds version 1 alone.
	before := NewSpannerClient(db, WithSecretKeys(testSecretKeys(t, 1)))
	for i := range rows {
		write(before, CreatePatchType, fmt.Sprintf("row-%03d", i), ptr(value(i)))
	}
	write(before, CreatePatchType, "no-value", nil)
	opens(testSecretKeys(t, 1), 1)

	// The rotation: version 2 is the current one, and a value set since names it.
	rotatedKeys := testSecretKeys(t, 1, 2)
	rotated := NewSpannerClient(db, WithSecretKeys(rotatedKeys))
	write(rotated, CreatePatchType, "set-since", ptr("set-since-the-rotation"))
	setSince := stored()["set-since"]
	holder := SecretHolderOf[retiringRow]()
	recorded := records()

	// The current version is never retired, by the rotated service or by one that was
	// handed version 1 alone.
	for name, client := range map[string]Client{"APP_SECRET_KEY_2": rotated, "APP_SECRET_KEY_1": before} {
		if _, err := RetireSecretVersion(ctx, client, name, holder); err == nil || !strings.Contains(err.Error(), name+" is the current version of the application secret") {
			t.Fatalf("RetireSecretVersion(%s) by a service whose current version it is: error = %v, want a refusal", name, err)
		}
	}
	opens(rotatedKeys, 1)

	got, err := RetireSecretVersion(ctx, rotated, "APP_SECRET_KEY_1", holder)
	if err != nil {
		t.Fatalf("RetireSecretVersion() error = %v", err)
	}
	if got.Version != 1 || got.Current != 2 || got.Rewritten() != rows || len(got.Fields) != 1 || got.Fields[0].Resource != "SealedRows" || got.Fields[0].Field != "AccessCode" {
		t.Errorf("RetireSecretVersion() = %+v, want version 1 retired under version 2 with %d values of SealedRows.AccessCode rewritten", got, rows)
	}
	opens(rotatedKeys, 2)
	// A service handed version 2 alone, version 1 disabled, opens every value.
	opens(testSecretKeys(t, 2), 2)
	after := stored()
	if !bytes.Equal(after["set-since"], setSince) {
		t.Errorf("the value already under the current version was written again")
	}
	if code, ok := after["no-value"]; !ok || code != nil {
		t.Errorf("the row with no value holds %d bytes, want none", len(code))
	}
	if now := records(); now != recorded {
		t.Errorf("the retirement wrote %d change record(s), want none: no value changed", now-recorded)
	}

	// A second run finds nothing to write.
	again, err := RetireSecretVersion(ctx, rotated, "APP_SECRET_KEY_1", holder)
	if err != nil || again.Rewritten() != 0 {
		t.Errorf("a second RetireSecretVersion() rewrote %v value(s), error %v; want none and no error", again, err)
	}
	if now := stored(); !cmp.Equal(now, after) {
		t.Errorf("the second run changed stored bytes")
	}

	// A value that does not open: bytes sealed under version 1 for one row, moved onto
	// another past the resource layer. The retirement fails naming the row, the value
	// beside it is written all the same, and the retirement passes once the row is
	// cleared.
	write(before, CreatePatchType, "moved-from", ptr("a-value-to-move"))
	write(before, CreatePatchType, "moved-to", nil)
	if _, err := db.Apply(ctx, []*spanner.Mutation{
		spanner.Update("SealedRows", []string{"Id", "AccessCode"}, []any{"moved-to", stored()["moved-from"]}),
	}); err != nil {
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
	write(rotated, UpdatePatchType, "moved-to", nil)
	if again, err := RetireSecretVersion(ctx, rotated, "APP_SECRET_KEY_1", holder); err != nil || again.Rewritten() != 0 {
		t.Errorf("RetireSecretVersion() once the row is cleared rewrote %v value(s), error %v; want none and no error", again, err)
	}

	// The versions one byte cannot name. A deployment's version numbers never repeat, so
	// in time a secret's versions pass 255. Values are sealed under versions 256, 258
	// and 65535, the last there is, beside every value above, which names version 2.
	// Version 256 shares its high byte with 258, and 258 its low byte with 2: a
	// retirement tells each from the others by its whole number.
	wideKeys := testSecretKeys(t, 2, 256, 258, 65535)
	wide := NewSpannerClient(db, WithSecretKeys(wideKeys))
	write(NewSpannerClient(db, WithSecretKeys(testSecretKeys(t, 2, 256))), CreatePatchType, "under-256", ptr("sealed-under-256"))
	write(NewSpannerClient(db, WithSecretKeys(testSecretKeys(t, 2, 256, 258))), CreatePatchType, "under-258", ptr("sealed-under-258"))
	write(wide, CreatePatchType, "under-65535", ptr("sealed-under-65535"))
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
	// counted asserts the retirement's own count of the values that name each version:
	// what its last read holds a version to before it is disabled.
	counted := func(step string, want map[int]int) {
		t.Helper()

		for version, values := range want {
			if got, err := holder.named(ctx, wide, "AccessCode", version); err != nil || got != values {
				t.Errorf("%s: the retirement counts %d value(s) naming version %d, error %v; want %d", step, got, version, err, values)
			}
		}
	}
	counted("before the retirements", map[int]int{1: 0, 2: rows + 2, 256: 1, 258: 1, 65535: 1})
	recorded = records()

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

	counted("once every older version is retired", map[int]int{1: 0, 2: 0, 256: 0, 258: 0, 65535: rows + 5})

	// A second run of each writes nothing, the last version there is cannot be retired,
	// being the current one, and no retirement wrote a change record.
	after = stored()
	for _, variable := range []string{"APP_SECRET_KEY_2", "APP_SECRET_KEY_256", "APP_SECRET_KEY_258"} {
		if again, err := RetireSecretVersion(ctx, wide, variable, holder); err != nil || again.Rewritten() != 0 {
			t.Errorf("a second RetireSecretVersion(%s) rewrote %v value(s), error %v; want none and no error", variable, again, err)
		}
	}
	if _, err := RetireSecretVersion(ctx, wide, "APP_SECRET_KEY_65535", holder); err == nil || !strings.Contains(err.Error(), "APP_SECRET_KEY_65535 is the current version of the application secret") {
		t.Errorf("RetireSecretVersion(APP_SECRET_KEY_65535): error = %v, want the refusal of the current version", err)
	}
	if now := stored(); !cmp.Equal(now, after) {
		t.Errorf("the second runs changed stored bytes")
	}
	if now := records(); now != recorded {
		t.Errorf("the retirements wrote %d change record(s), want none: no value changed", now-recorded)
	}
}
