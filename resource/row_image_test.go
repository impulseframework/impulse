package resource

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
)

// TestTransactionRows_noteImage pins the image a transaction keeps of a row: what each
// patch makes of the row, from no image, a row it changed, one it created and one it
// deleted.
func TestTransactionRows_noteImage(t *testing.T) {
	t.Parallel()

	const table, row = "Notices", "2:Id=3:n-1;"
	type write struct {
		patchType PatchType
		columns   map[string]any
	}
	created := []write{{CreatePatchType, map[string]any{"Id": "n-1", "Body": "old", "Title": "kept"}}}
	stored := []write{{UpdatePatchType, map[string]any{"Id": "n-1", "Body": "old", "Title": "kept"}}}
	upserted := []write{{CreateOrUpdatePatchType, map[string]any{"Id": "n-1", "Body": "old", "Title": "kept"}}}
	deleted := []write{{DeletePatchType, nil}}
	given := map[string]any{"Id": "n-1", "Body": "new"}
	laidOver := map[string]any{"Id": "n-1", "Body": "new", "Title": "kept"}
	empty := map[string]any{}

	tests := []struct {
		name   string
		before []write
		patch  write
		want   rowImage
	}{
		{"create, no image", nil, write{CreatePatchType, given}, rowImage{rowCreated, false, given}},
		{"create, stored", upserted, write{CreatePatchType, given}, rowImage{rowCreated, false, given}},
		{"create, created", created, write{CreatePatchType, given}, rowImage{rowCreated, false, given}},
		{"create, deleted", deleted, write{CreatePatchType, given}, rowImage{rowCreated, false, given}},
		{"update, no image", nil, write{UpdatePatchType, given}, rowImage{rowStored, false, given}},
		{"update, stored", stored, write{UpdatePatchType, given}, rowImage{rowStored, false, laidOver}},
		{"update, stored and upserted", upserted, write{UpdatePatchType, given}, rowImage{rowStored, true, laidOver}},
		{"update, created", created, write{UpdatePatchType, given}, rowImage{rowCreated, false, laidOver}},
		{"update, deleted", deleted, write{UpdatePatchType, given}, rowImage{rowDeleted, false, empty}},
		{"insert-or-update, no image", nil, write{CreateOrUpdatePatchType, given}, rowImage{rowStored, true, given}},
		{"insert-or-update, stored", stored, write{CreateOrUpdatePatchType, given}, rowImage{rowStored, true, laidOver}},
		{"insert-or-update, created", created, write{CreateOrUpdatePatchType, given}, rowImage{rowCreated, false, laidOver}},
		{"insert-or-update, deleted", deleted, write{CreateOrUpdatePatchType, given}, rowImage{rowCreated, false, given}},
		{"delete, no image", nil, write{DeletePatchType, nil}, rowImage{rowDeleted, false, empty}},
		{"delete, stored and upserted", upserted, write{DeletePatchType, nil}, rowImage{rowDeleted, false, empty}},
		{"delete, created", created, write{DeletePatchType, nil}, rowImage{rowDeleted, false, empty}},
		{"delete, deleted", deleted, write{DeletePatchType, nil}, rowImage{rowDeleted, false, empty}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rows := newTransactionRows()
			for _, w := range append(tt.before, tt.patch) {
				rows.note(table, w.patchType, row, nil, w.columns)
			}
			got, ok := rows.image(table, row)
			if !ok {
				t.Fatal("image() found no image of the row")
			}
			if diff := cmp.Diff(tt.want, got, cmp.AllowUnexported(rowImage{})); diff != "" {
				t.Errorf("image mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestTransactionRows_image pins that an image is a copy: a later note, and a change to
// the map a patch buffered, leave an image already handed back as it was; and that a
// patch whose row is empty, and a row no patch wrote, have none.
func TestTransactionRows_image(t *testing.T) {
	t.Parallel()

	const table, row = "Notices", "2:Id=3:n-1;"
	rows := newTransactionRows()
	columns := map[string]any{"Id": "n-1", "Body": "first"}
	rows.note(table, CreatePatchType, row, nil, columns)
	columns["Body"] = "changed by its caller"

	first, ok := rows.image(table, row)
	if !ok {
		t.Fatal("image() found no image of the row")
	}
	rows.note(table, UpdatePatchType, row, nil, map[string]any{"Body": "second"})
	first.columns["Title"] = "changed by its reader"

	want := rowImage{state: rowCreated, upserted: false, columns: map[string]any{"Id": "n-1", "Body": "first", "Title": "changed by its reader"}}
	if diff := cmp.Diff(want, first, cmp.AllowUnexported(rowImage{})); diff != "" {
		t.Errorf("the image handed back changed (-want +got):\n%s", diff)
	}
	second, _ := rows.image(table, row)
	want = rowImage{state: rowCreated, upserted: false, columns: map[string]any{"Id": "n-1", "Body": "second"}}
	if diff := cmp.Diff(want, second, cmp.AllowUnexported(rowImage{})); diff != "" {
		t.Errorf("the image after the update mismatch (-want +got):\n%s", diff)
	}

	rows.note(table, CreatePatchType, "", nil, columns)
	if _, ok := rows.image(table, ""); ok {
		t.Error("image() found an image of a patch whose row is empty")
	}
	if _, ok := rows.image(table, "2:Id=3:n-2;"); ok {
		t.Error("image() found an image of a row no patch wrote")
	}
}

// TestPatchSet_laidOver_null pins how a NULL an earlier patch of the transaction buffered
// reads back as an old value: the image holds it as the untyped nil, and it reads as the
// field's own nil, as the stored row gives it, so a patch that gives the field no value
// again changes nothing there.
func TestPatchSet_laidOver_null(t *testing.T) {
	t.Parallel()

	var none *string
	p := changePatch[changeBerth](UpdatePatchType, "berth-1", map[accesstypes.Field]any{"DockID": none, "Name": "Renamed"})
	image := &rowImage{state: rowCreated, upserted: false, columns: map[string]any{"Id": "berth-1", "DockId": nil, "Name": "First"}}

	old, err := p.laidOver(new(changeBerth), image, SpannerDBType)
	if err != nil {
		t.Fatalf("laidOver() error = %v", err)
	}
	got, err := p.diffValues(old, nil)
	if err != nil {
		t.Fatalf("diffValues() error = %v", err)
	}
	want := map[accesstypes.Field]DiffElem{"Name": {Old: "First", New: "Renamed"}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("change set mismatch (-want +got):\n%s", diff)
	}
}
