package resource

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
)

// TestTakenReader_refusing reads, over the emulator, the rows that refuse a record's
// delete from under the rows a walk found: the kind's rows whose columns hold what the
// link references of one of the rows above, each named by its key, a key of two columns
// in one name, a row the transaction itself deleted left out, and a row it changed read
// as it is stored.
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
		// changed is the key of a row of the kind the transaction updated before the read.
		changed map[string]any
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
			name:    "a row the transaction changed is read as it is stored",
			kind:    &DeleteKind{Resource: "TrackedForkRows", Words: DeleteWords{One: "row", Many: "rows"}, Key: []string{"Id"}},
			link:    DeleteLink{Columns: []string{"Title"}, Table: "Shelves", References: []string{"Name"}},
			changed: map[string]any{"Id": "row-4"},
			want:    []string{"row-1", "row-2", "row-4"},
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
				if tt.changed != nil {
					spannerTxn.recordWritten(tt.kind.Resource, tt.changed, false)
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

// TestTakenReader_anotherTenantsRow reads, over the emulator, the rows under a record of a
// tenant where a row of the record's tenant and a row of another tenant both name it: the
// walk's read, the read of the rows that refuse the delete, the count before the commit
// and the walk itself each find the first row alone. It holds for a kind that holds its
// tenant in a column and for one whose rows reach their tenant through another table,
// under the tenant record, whose key holds its tenant, and under a record whose row is
// read for it. With no tenant on the record, or on the kind, both rows are under it.
func TestTakenReader_anotherTenantsRow(t *testing.T) {
	t.Parallel()

	db := forkDatabase(t, "another-tenants-row")
	ctx := context.Background()
	if _, err := db.Apply(ctx, []*spanner.Mutation{
		// The record whose row is read for its tenant, and what is under it.
		spanner.InsertMap("ForkRows", map[string]any{"Id": "f1", "Title": "record", "CreatedBy": "anvil"}),
		spanner.InsertMap("SealedPairRows", map[string]any{"Vault": "anvil", "Shelf": "f1"}),
		spanner.InsertMap("SealedPairRows", map[string]any{"Vault": "bastion", "Shelf": "f1"}),
		spanner.InsertMap("TrackedForkRows", map[string]any{"Id": "ta", "Title": "f1"}),
		spanner.InsertMap("TrackedForkRows", map[string]any{"Id": "tb", "Title": "f1"}),
		// What is under the tenant record anvil.
		spanner.InsertMap("ForkRows", map[string]any{"Id": "k1", "Title": "anvil", "CreatedBy": "anvil"}),
		spanner.InsertMap("ForkRows", map[string]any{"Id": "k2", "Title": "anvil", "CreatedBy": "bastion"}),
		spanner.InsertMap("TrackedForkRows", map[string]any{"Id": "va", "Title": "anvil"}),
		spanner.InsertMap("TrackedForkRows", map[string]any{"Id": "vb", "Title": "anvil"}),
		// The rows a tracked row reaches its tenant through, by its own key.
		spanner.InsertMap("ForkRows", map[string]any{"Id": "ta", "Title": "way", "CreatedBy": "anvil"}),
		spanner.InsertMap("ForkRows", map[string]any{"Id": "tb", "Title": "way", "CreatedBy": "bastion"}),
		spanner.InsertMap("ForkRows", map[string]any{"Id": "va", "Title": "way", "CreatedBy": "anvil"}),
		spanner.InsertMap("ForkRows", map[string]any{"Id": "vb", "Title": "way", "CreatedBy": "bastion"}),
	}); err != nil {
		t.Fatalf("spanner.Client.Apply() error = %v", err)
	}

	byCreator := &DomainBindingData{Column: "CreatedBy"}
	throughForkRows := &DomainBindingData{Column: "Id", Path: []BindingHop{{Table: "ForkRows", JoinColumn: "Id", Column: "CreatedBy"}}}
	text := reflect.TypeFor[string]()
	columns := map[accesstypes.Resource]map[string]takenColumn{
		"ForkRows":        {"Id": {typ: text}},
		"SealedPairRows":  {"Vault": {typ: text}, "Shelf": {typ: text}},
		"TrackedForkRows": {"Id": {typ: text}},
	}

	type under struct {
		kind *DeleteKind
		// mine and theirs name the row of the record's tenant and the row of the other.
		mine, theirs string
	}
	tests := []struct {
		name string
		tree DeleteTree
		key  map[string]any
		kind under
	}{
		{
			name: "a kind with its tenant in a column, under a record whose row is read",
			tree: DeleteTree{Record: "ForkRows", Words: DeleteWords{One: "row", Many: "rows"}, Key: []string{"Id"}, Tenant: byCreator},
			key:  map[string]any{"Id": "f1"},
			kind: under{
				kind: &DeleteKind{
					Resource: "SealedPairRows", Words: DeleteWords{One: "pair", Many: "pairs"}, Key: []string{"Vault", "Shelf"}, Tenant: &DomainBindingData{Column: "Vault"},
					Paths: []DeletePath{{{Columns: []string{"Shelf"}, Table: "ForkRows", References: []string{"Id"}}}},
				},
				mine: "anvil\x00f1", theirs: "bastion\x00f1",
			},
		},
		{
			name: "a kind through another table, under a record whose row is read",
			tree: DeleteTree{Record: "ForkRows", Words: DeleteWords{One: "row", Many: "rows"}, Key: []string{"Id"}, Tenant: byCreator},
			key:  map[string]any{"Id": "f1"},
			kind: under{
				kind: &DeleteKind{
					Resource: "TrackedForkRows", Words: DeleteWords{One: "tracked row", Many: "tracked rows"}, Key: []string{"Id"}, Tenant: throughForkRows,
					Paths: []DeletePath{{{Columns: []string{"Title"}, Table: "ForkRows", References: []string{"Id"}}}},
				},
				mine: "ta", theirs: "tb",
			},
		},
		{
			name: "a kind with its tenant in a column, under the tenant record",
			tree: DeleteTree{Record: "Vaults", Words: DeleteWords{One: "vault", Many: "vaults"}, Key: []string{"Id"}, Tenant: &DomainBindingData{Column: "Id"}},
			key:  map[string]any{"Id": "anvil"},
			kind: under{
				kind: &DeleteKind{
					Resource: "ForkRows", Words: DeleteWords{One: "row", Many: "rows"}, Key: []string{"Id"}, Tenant: byCreator,
					Paths: []DeletePath{{{Columns: []string{"Title"}, Table: "Vaults", References: []string{"Id"}}}},
				},
				mine: "k1", theirs: "k2",
			},
		},
		{
			name: "a kind through another table, under the tenant record",
			tree: DeleteTree{Record: "Vaults", Words: DeleteWords{One: "vault", Many: "vaults"}, Key: []string{"Id"}, Tenant: &DomainBindingData{Column: "Id"}},
			key:  map[string]any{"Id": "anvil"},
			kind: under{
				kind: &DeleteKind{
					Resource: "TrackedForkRows", Words: DeleteWords{One: "tracked row", Many: "tracked rows"}, Key: []string{"Id"}, Tenant: throughForkRows,
					Paths: []DeletePath{{{Columns: []string{"Title"}, Table: "Vaults", References: []string{"Id"}}}},
				},
				mine: "va", theirs: "vb",
			},
		},
	}

	for _, tt := range tests {
		for _, variant := range []struct {
			name            string
			noRecordsTenant bool
			noKindsTenant   bool
			wantTheirsUnder bool
		}{
			{name: "the record's tenant alone"},
			{name: "no tenant on the record", noRecordsTenant: true, wantTheirsUnder: true},
			{name: "no tenant on the kind", noKindsTenant: true, wantTheirsUnder: true},
		} {
			t.Run(tt.name+", "+variant.name, func(t *testing.T) {
				t.Parallel()

				tree, kind := tt.tree, *tt.kind.kind
				if variant.noRecordsTenant {
					tree.Tenant = nil
				}
				if variant.noKindsTenant {
					kind.Tenant = nil
				}
				tree.Taken = []DeleteKind{kind}
				link := kind.Paths[0][0]
				want := []string{tt.kind.mine}
				if variant.wantTheirsUnder {
					want = append(want, tt.kind.theirs)
				}
				slices.Sort(want)

				var underRows, refusing, walked []string
				var counted int64
				err := NewSpannerClient(db).ExecuteFunc(ctx, func(ctx context.Context, txn ReadWriteTransaction) error {
					spannerTxn, ok := txn.(*SpannerReadWriteTransaction)
					if !ok {
						return errors.Newf("the transaction is a %T, want Spanner's", txn)
					}
					reader := &takenReader{tree: &tree, key: tt.key, txn: spannerTxn, columns: columns}
					recordRow, err := reader.recordRow(ctx, tt.key)
					if err != nil {
						return err
					}

					rows, err := reader.under(ctx, &kind, link, []map[string]any{recordRow}, 10)
					if err != nil {
						return err
					}
					underRows = rowNamesOf(kind.Key, rows)

					if refusing, err = reader.refusing(ctx, &kind, link, []map[string]any{recordRow}); err != nil {
						return err
					}

					statement, ok := newRowsUnderStatement(SpannerDBType, &tree, &kind, tt.key, nil)
					if !ok {
						return errors.New("newRowsUnderStatement() wrote no count")
					}
					if counted, err = spannerTxn.countRows(ctx, statement); err != nil {
						return err
					}

					walk := &takenWalk{
						tree:  &tree,
						limit: DeleteMutationLimit,
						tracked: func(accesstypes.Resource) bool {
							return false
						},
						under:   reader.under,
						deleted: spannerTxn.deleted.deletedRow,
						changed: spannerTxn.deleted.changedRow,
					}
					found, err := walk.find(ctx, recordRow)
					if err != nil {
						return err
					}
					walked = rowNamesOf(kind.Key, found.rows[kind.Resource])

					return nil
				})
				if err != nil {
					t.Fatalf("NewSpannerClient().ExecuteFunc() error = %v", err)
				}
				slices.Sort(refusing)
				if diff := cmp.Diff(want, underRows); diff != "" {
					t.Errorf("takenReader.under() mismatch (-want +got):\n%s", diff)
				}
				if diff := cmp.Diff(want, refusing); diff != "" {
					t.Errorf("takenReader.refusing() mismatch (-want +got):\n%s", diff)
				}
				if counted != int64(len(want)) {
					t.Errorf("the count of the rows under the record = %d, want %d", counted, len(want))
				}
				if diff := cmp.Diff(want, walked); diff != "" {
					t.Errorf("takenWalk.find() mismatch (-want +got):\n%s", diff)
				}
			})
		}
	}
}

// rowNamesOf names each row read by the values of its key in one text, as rowNames does,
// in sorted order.
func rowNamesOf(key []string, rows []map[string]any) []string {
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		values := make([]string, 0, len(key))
		for _, column := range key {
			values = append(values, fmt.Sprint(row[column]))
		}
		names = append(names, strings.Join(values, "\x00"))
	}
	slices.Sort(names)

	return names
}
