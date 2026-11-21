package resource

import (
	"context"
	"slices"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
)

// TestTakenReader_refusing reads, over the emulator, the rows that refuse a record's
// delete from under the rows a walk found: the kind's rows whose columns hold what the
// link references of one of the rows above, each named by its key, a key of two columns
// in one name, and a row the transaction itself deleted left out.
func TestTakenReader_refusing(t *testing.T) {
	t.Parallel()

	db := forkDatabase(t, "refusing-rows")
	ctx := context.Background()
	if _, err := db.Apply(ctx, []*spanner.Mutation{
		spanner.InsertMap("TrackedForkRows", map[string]any{"Id": "row-1", "Title": "alpha"}),
		spanner.InsertMap("TrackedForkRows", map[string]any{"Id": "row-2", "Title": "beta"}),
		spanner.InsertMap("TrackedForkRows", map[string]any{"Id": "row-3", "Title": "gamma"}),
		spanner.InsertMap("TrackedForkRows", map[string]any{"Id": "row-4", "Title": "alpha"}),
		spanner.InsertMap("SealedPairRows", map[string]any{"Vault": "alpha", "Shelf": "s1"}),
		spanner.InsertMap("SealedPairRows", map[string]any{"Vault": "alpha", "Shelf": "s2"}),
		spanner.InsertMap("SealedPairRows", map[string]any{"Vault": "gamma", "Shelf": "s1"}),
	}); err != nil {
		t.Fatalf("spanner.Client.Apply() error = %v", err)
	}

	tree := &DeleteTree{Record: "Shelves", Words: DeleteWords{One: "shelf", Many: "shelves"}, Key: []string{"Name"}}
	above := []map[string]any{{"Name": "alpha"}, {"Name": "beta"}}

	tests := []struct {
		name string
		kind *DeleteKind
		link DeleteLink
		// deleted is the key of a row of the kind the transaction deleted before the read.
		deleted map[string]any
		want    []string
	}{
		{
			name: "the rows that name one of the rows above",
			kind: &DeleteKind{Resource: "TrackedForkRows", Words: DeleteWords{One: "row", Many: "rows"}, Key: []string{"Id"}},
			link: DeleteLink{Columns: []string{"Title"}, Table: "Shelves", References: []string{"Name"}},
			want: []string{"row-1", "row-2", "row-4"},
		},
		{
			name:    "a row the transaction deleted is left out",
			kind:    &DeleteKind{Resource: "TrackedForkRows", Words: DeleteWords{One: "row", Many: "rows"}, Key: []string{"Id"}},
			link:    DeleteLink{Columns: []string{"Title"}, Table: "Shelves", References: []string{"Name"}},
			deleted: map[string]any{"Id": "row-4"},
			want:    []string{"row-1", "row-2"},
		},
		{
			name:    "a key of two columns names its row in one text",
			kind:    &DeleteKind{Resource: "SealedPairRows", Words: DeleteWords{One: "pair", Many: "pairs"}, Key: []string{"Vault", "Shelf"}},
			link:    DeleteLink{Columns: []string{"Vault"}, Table: "Shelves", References: []string{"Name"}},
			deleted: map[string]any{"Vault": "alpha", "Shelf": "s2"},
			want:    []string{"alpha\x00s1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got []string
			err := NewSpannerClient(db).ExecuteFunc(ctx, func(ctx context.Context, txn ReadWriteTransaction) error {
				spannerTxn, ok := txn.(*SpannerReadWriteTransaction)
				if !ok {
					return errors.Newf("the transaction is a %T, want Spanner's", txn)
				}
				if tt.deleted != nil {
					spannerTxn.recordWritten(tt.kind.Resource, tt.deleted, true)
				}
				reader := &takenReader{tree: tree, txn: spannerTxn}
				var err error
				got, err = reader.refusing(ctx, tt.kind, tt.link, above)

				return err
			})
			if err != nil {
				t.Fatalf("refusing() error = %v", err)
			}
			slices.Sort(got)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("refusing() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
