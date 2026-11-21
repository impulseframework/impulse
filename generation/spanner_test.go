package generation

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// Test_createTableMapUsingQuery reads the index-shapes fixture schema through the
// emulator and pins what the information schema yields: every index's composition,
// the primary key and Spanner's foreign-key backing indexes included, the column flags
// derived from it, the cascade facts the audit pass reads: each table's parent and
// delete action, and each foreign key's delete rule, and each column's default
// expression. Orders and Seats share the column
// name Note and every table carries a PRIMARY_KEY index, so an index joined to its
// columns on index name alone would leak across tables here. Requires the Spanner
// emulator (podman/docker).
func Test_createTableMapUsingQuery(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("the schema read requires the Spanner emulator")
	}

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() failed")
	}
	migrations := "file://" + filepath.Join(filepath.Dir(thisFile), "testdata", "migrations")

	ctx := context.Background()
	db, spannerContainer, err := createSpannerDB(ctx, "1.5.56", []string{migrations})
	if err != nil {
		t.Fatalf("createSpannerDB() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.DropDatabase(context.Background()); err != nil {
			t.Errorf("DropDatabase() error = %v", err)
		}
		if err := db.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
		releaseSpannerContainer(context.Background(), spannerContainer)
	})

	tableMap, err := createTableMapUsingQuery(ctx, db.Client)
	if err != nil {
		t.Fatalf("createTableMapUsingQuery() error = %v", err)
	}

	// Spanner names the indexes it manages for foreign keys itself, so those compare
	// by shape with the name blanked.
	unnamed := cmp.Transformer("managed", func(index indexMeta) indexMeta {
		if index.Managed {
			index.Name = ""
		}

		return index
	})

	tests := []struct {
		name        string
		table       string
		wantIndexes []indexMeta
		wantFlags   map[string][2]bool // column -> IsIndex, IsUniqueIndex
		wantTypes   map[string]string  // column -> SpannerType, as the information schema spells it
		// wantParent and wantCascade are the interleave facts; wantDeleteRules maps a
		// column to its foreign key's delete rule, "" where it has none.
		wantParent      string
		wantCascade     bool
		wantDeleteRules map[string]string
		// wantForeignKeys are the table's foreign keys whole, in constraint name order,
		// compared where a case names any.
		wantForeignKeys []foreignKeyMeta
		// wantDefaults maps a column to its default expression as the information
		// schema spells it, "" where the column declares none.
		wantDefaults map[string]string
	}{
		{
			name:        "a table with only its primary key, sharing the key column name with Orders",
			table:       "Tenants",
			wantIndexes: []indexMeta{{Name: "PRIMARY_KEY", PrimaryKey: true, Unique: true, Key: []indexColumn{{Column: "Id"}}}},
			wantFlags:   map[string][2]bool{"Id": {true, true}},
			wantTypes:   map[string]string{"Id": "STRING(36)"},
		},
		{
			name:  "a table with composite, unique, null-filtered unique, null-filtered, and backing indexes",
			table: "Orders",
			wantIndexes: []indexMeta{
				{Managed: true, Key: []indexColumn{{Column: "TenantId"}}},
				{Name: "OrdersByExternalRef", Unique: true, NullFiltered: true, Key: []indexColumn{{Column: "ExternalRef"}}},
				{Name: "OrdersByNote", NullFiltered: true, Key: []indexColumn{{Column: "Note"}}},
				{Name: "OrdersByReference", Unique: true, Key: []indexColumn{{Column: "Reference"}}},
				{Name: "OrdersByTenantIdPlacedAt", Key: []indexColumn{{Column: "TenantId"}, {Column: "PlacedAt", Descending: true}}, Storing: []string{"Note"}},
				{Name: "PRIMARY_KEY", PrimaryKey: true, Unique: true, Key: []indexColumn{{Column: "Id"}}},
			},
			// PlacedAt trails TenantId in the composite index and leads nothing, so it is
			// not indexed at table level. Note is stored by that index, which alone would
			// not index it, but it leads OrdersByNote.
			wantFlags: map[string][2]bool{
				"Id": {true, true}, "TenantId": {true, false}, "PlacedAt": {false, false},
				"Reference": {true, true}, "ExternalRef": {true, true}, "Note": {true, false},
			},
			wantTypes: map[string]string{"TenantId": "STRING(36)", "PlacedAt": "TIMESTAMP", "Reference": "STRING(MAX)"},
			// A top-level table: no parent, and its one foreign key keeps the default rule.
			wantDeleteRules: map[string]string{"TenantId": "NO ACTION", "PlacedAt": ""},
		},
		{
			// The foreign key on OrderId needs no backing index: the primary key leads
			// with it, so Spanner manages none. Neither the composite key nor the
			// composite unique index identifies a row by one column, and their trailing
			// columns UserId and Row lead nothing, so they are not indexed.
			name:  "a composite key and a composite unique index identify a row by no single column",
			table: "Seats",
			wantIndexes: []indexMeta{
				{Name: "PRIMARY_KEY", PrimaryKey: true, Unique: true, Key: []indexColumn{{Column: "OrderId"}, {Column: "UserId"}}},
				{Name: "SeatsByNote", Key: []indexColumn{{Column: "Note"}}},
				{Name: "SeatsByOrderIdRow", Unique: true, Key: []indexColumn{{Column: "OrderId"}, {Column: "Row"}}},
			},
			wantFlags: map[string][2]bool{"OrderId": {true, false}, "UserId": {false, false}, "Row": {false, false}, "Note": {true, false}},
			wantTypes: map[string]string{"Row": "INT64", "Note": "STRING(MAX)"},
		},
		{
			name:        "an interleaved child on cascade reports its parent and the action",
			table:       "OrderLines",
			wantIndexes: []indexMeta{{Name: "PRIMARY_KEY", PrimaryKey: true, Unique: true, Key: []indexColumn{{Column: "Id"}, {Column: "LineNumber"}}}},
			wantFlags:   map[string][2]bool{"Id": {true, false}, "LineNumber": {false, false}},
			wantTypes:   map[string]string{"StoreKey": "STRING(36)"},
			wantParent:  "Orders",
			wantCascade: true,
		},
		{
			name:        "an interleaved child on no action reports its parent alone",
			table:       "OrderNotes",
			wantIndexes: []indexMeta{{Name: "PRIMARY_KEY", PrimaryKey: true, Unique: true, Key: []indexColumn{{Column: "Id"}, {Column: "NoteNumber"}}}},
			wantFlags:   map[string][2]bool{"Id": {true, false}},
			wantParent:  "Orders",
		},
		{
			// Two foreign keys, one cascading: each column reads its own rule.
			name:  "a table with a cascading foreign key and a plain one reports each rule",
			table: "Attachments",
			wantIndexes: []indexMeta{
				{Managed: true, Key: []indexColumn{{Column: "OrderId"}}},
				{Managed: true, Key: []indexColumn{{Column: "TenantId"}}},
				{Name: "PRIMARY_KEY", PrimaryKey: true, Unique: true, Key: []indexColumn{{Column: "Id"}}},
			},
			wantFlags:       map[string][2]bool{"Id": {true, true}, "OrderId": {true, false}, "TenantId": {true, false}},
			wantDeleteRules: map[string]string{"OrderId": "CASCADE", "TenantId": "NO ACTION", "StoreKey": ""},
			wantForeignKeys: []foreignKeyMeta{
				{Name: "FK_Attachments_OrderId", Columns: []string{"OrderId"}, ReferencedTable: "Orders", ReferencedColumns: []string{"Id"}, DeleteRule: "CASCADE"},
				{Name: "FK_Attachments_TenantId", Columns: []string{"TenantId"}, ReferencedTable: "Tenants", ReferencedColumns: []string{"Id"}, DeleteRule: "NO ACTION"},
			},
		},
		{
			// The gate's key is Id, and the pass names the gate by its Code: the foreign
			// key reads with the table and the column it references all the same.
			name:  "a foreign key to a column that is not the other table's key reads whole",
			table: "Passes",
			wantIndexes: []indexMeta{
				{Managed: true, Key: []indexColumn{{Column: "GateCode"}}},
				{Managed: true, Unique: true, Key: []indexColumn{{Column: "Zone"}, {Column: "Number"}}},
				{Name: "PRIMARY_KEY", PrimaryKey: true, Unique: true, Key: []indexColumn{{Column: "Id"}}},
			},
			wantForeignKeys: []foreignKeyMeta{
				{Name: "FK_Passes_GateCode", Columns: []string{"GateCode"}, ReferencedTable: "Gates", ReferencedColumns: []string{"Code"}, DeleteRule: "NO ACTION"},
			},
		},
		{
			// Two columns to two columns that are no key, each beside the column it
			// references, and a foreign key to the same table's key next to it.
			name:  "a foreign key of two columns to columns that are no key reads each beside the column it references",
			table: "Stubs",
			wantIndexes: []indexMeta{
				{Managed: true, Key: []indexColumn{{Column: "PassId"}}},
				{Managed: true, Key: []indexColumn{{Column: "PassZone"}, {Column: "PassNumber"}}},
				{Name: "PRIMARY_KEY", PrimaryKey: true, Unique: true, Key: []indexColumn{{Column: "Id"}}},
			},
			wantForeignKeys: []foreignKeyMeta{
				{Name: "FK_Stubs_Pass", Columns: []string{"PassZone", "PassNumber"}, ReferencedTable: "Passes", ReferencedColumns: []string{"Zone", "Number"}, DeleteRule: "NO ACTION"},
				{Name: "FK_Stubs_PassId", Columns: []string{"PassId"}, ReferencedTable: "Passes", ReferencedColumns: []string{"Id"}, DeleteRule: "NO ACTION"},
			},
		},
		{
			// The expression comes back as the migration wrote it, in its own case and
			// without the parentheses the DDL wraps it in.
			name:        "each column's default is read as the schema declares it",
			table:       "Switches",
			wantIndexes: []indexMeta{{Name: "PRIMARY_KEY", PrimaryKey: true, Unique: true, Key: []indexColumn{{Column: "Id"}}}},
			wantFlags:   map[string][2]bool{"Id": {true, true}},
			wantTypes:   map[string]string{"Lit": "BOOL", "Maybe": "BOOL", "Count": "INT64"},
			wantDefaults: map[string]string{
				"Id": "", "Lit": "TRUE", "Dark": "FALSE", "Low": "false", "Open": "", "Odd": "1 = 1", "Maybe": "TRUE", "Count": "0",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			table, ok := tableMap[tt.table]
			if !ok {
				t.Fatalf("table %s not in the table map", tt.table)
			}
			sortByName := cmpopts.SortSlices(func(a, b indexMeta) bool {
				return a.Name < b.Name
			})
			if diff := cmp.Diff(tt.wantIndexes, table.Indexes, unnamed, sortByName, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("%s indexes mismatch (-want +got):\n%s", tt.table, diff)
			}
			for column, want := range tt.wantFlags {
				meta, ok := table.Columns[column]
				if !ok {
					t.Errorf("column %s.%s not in the table map", tt.table, column)

					continue
				}
				if got := [2]bool{meta.IsIndex, meta.IsUniqueIndex}; got != want {
					t.Errorf("%s.%s flags (IsIndex, IsUniqueIndex) = %v, want %v", tt.table, column, got, want)
				}
			}
			for column, want := range tt.wantTypes {
				if got := table.Columns[column].SpannerType; got != want {
					t.Errorf("%s.%s SpannerType = %q, want %q", tt.table, column, got, want)
				}
			}
			if table.ParentTable != tt.wantParent || table.OnDeleteCascade != tt.wantCascade || table.IsInterleaved != (tt.wantParent != "") {
				t.Errorf("%s (ParentTable, OnDeleteCascade, IsInterleaved) = (%q, %v, %v), want (%q, %v, %v)", tt.table, table.ParentTable, table.OnDeleteCascade, table.IsInterleaved, tt.wantParent, tt.wantCascade, tt.wantParent != "")
			}
			for column, want := range tt.wantDeleteRules {
				if got := table.Columns[column].DeleteRule; got != want {
					t.Errorf("%s.%s DeleteRule = %q, want %q", tt.table, column, got, want)
				}
			}
			if tt.wantForeignKeys != nil {
				if diff := cmp.Diff(tt.wantForeignKeys, table.ForeignKeys); diff != "" {
					t.Errorf("%s foreign keys mismatch (-want +got):\n%s", tt.table, diff)
				}
			}
			for column, want := range tt.wantDefaults {
				meta := table.Columns[column]
				if meta.Default != want || meta.HasDefault != (want != "") {
					t.Errorf("%s.%s (Default, HasDefault) = (%q, %v), want (%q, %v)", tt.table, column, meta.Default, meta.HasDefault, want, want != "")
				}
			}
		})
	}
}

// Test_fetchEnumValues pins what makes a table an enumeration: an Id column beside the
// Description column. The fixture's Flags (keyed by name) and Roles (keyed by scope and
// name) carry a Description too and are no enumeration. Requires the Spanner emulator
// (podman/docker).
func Test_fetchEnumValues(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("the schema read requires the Spanner emulator")
	}

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() failed")
	}
	migrations := "file://" + filepath.Join(filepath.Dir(thisFile), "testdata", "migrations")

	ctx := context.Background()
	db, spannerContainer, err := createSpannerDB(ctx, "1.5.56", []string{migrations})
	if err != nil {
		t.Fatalf("createSpannerDB() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.DropDatabase(context.Background()); err != nil {
			t.Errorf("DropDatabase() error = %v", err)
		}
		if err := db.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
		releaseSpannerContainer(context.Background(), spannerContainer)
	})

	got, err := fetchEnumValues(ctx, db.Client)
	if err != nil {
		t.Fatalf("fetchEnumValues() error = %v", err)
	}
	want := map[string][]*enumData{
		"WidgetKinds": {{ID: "gear", Description: "Gear"}, {ID: "lever", Description: "Lever"}},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("fetchEnumValues() mismatch (-want +got):\n%s", diff)
	}
}
