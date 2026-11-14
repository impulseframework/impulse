package resource

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
)

// Over the emulator: a secret is stored as it was given, and every change record of its
// row (the create's, an update's, the delete's) names the field and keeps neither value.
// An update that sets the secret to what it holds changes nothing and writes no record.
func TestSpannerClient_secret_changeRecords(t *testing.T) {
	t.Parallel()

	db := forkDatabase(t, "sealed-rows")
	ctx := context.Background()
	client := NewSpannerClient(db)

	const first, second = "open-sesame", "close-sesame"
	sealed := func(patchType PatchType, title, code string) *PatchSet[sealedRow] {
		p := NewPatchSet(NewMetadata[sealedRow]()).SetPatchType(patchType)
		p.SetKey("ID", "row-1")
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
	stored := func() string {
		t.Helper()

		var code spanner.NullString
		stmt := spanner.NewStatement("SELECT AccessCode FROM SealedRows WHERE Id = 'row-1'")
		if err := db.Single().Query(ctx, stmt).Do(func(row *spanner.Row) error {
			return row.Column(0, &code)
		}); err != nil {
			t.Fatalf("reading the stored row: %v", err)
		}

		return code.StringVal
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

	if err := sealed(CreatePatchType, "sealed", first).Apply(ctx, client, "test"); err != nil {
		t.Fatalf("the create: Apply() error = %v", err)
	}
	kept("after the create", 1)
	if got := stored(); got != first {
		t.Errorf("the stored secret = %q, want it as it was given", got)
	}

	if err := sealed(UpdatePatchType, "", second).Apply(ctx, client, "test"); err != nil {
		t.Fatalf("the update: Apply() error = %v", err)
	}
	kept("after the update", 2)
	if got := stored(); got != second {
		t.Errorf("the stored secret after the update = %q, want the new one", got)
	}

	// The same secret again changes nothing: no error, and no record.
	if err := sealed(UpdatePatchType, "", second).Apply(ctx, client, "test"); err != nil {
		t.Fatalf("the update that changes nothing: Apply() error = %v", err)
	}
	kept("after the update that changes nothing", 2)

	if err := sealed(DeletePatchType, "", "").Apply(ctx, client, "test"); err != nil {
		t.Fatalf("the delete: Apply() error = %v", err)
	}
	kept("after the delete", 3)
}
