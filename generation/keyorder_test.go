package generation

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/spxscan"
)

// keyPosition is one foreign key column as INFORMATION_SCHEMA.KEY_COLUMN_USAGE lists it:
// its position in the foreign key, and what the database answers as its position in the
// constraint the foreign key references.
type keyPosition struct {
	Constraint string `spanner:"CONSTRAINT_NAME"`
	Column     string `spanner:"COLUMN_NAME"`
	Ordinal    int64  `spanner:"ORDINAL_POSITION"`
	InUnique   int64  `spanner:"POSITION_IN_UNIQUE_CONSTRAINT"`
}

// Test_createTableMapUsingQuery_keyOrder reads foreign keys of two columns through the
// emulator and pins how each column is paired with the column it references.
//
// Google's documentation of INFORMATION_SCHEMA.KEY_COLUMN_USAGE defines the pairing
// (https://docs.cloud.google.com/spanner/docs/information-schema): a foreign key
// column's POSITION_IN_UNIQUE_CONSTRAINT is "the ordinal position of the column within
// the unique constraint", so the column pairs with the column at that position of the
// constraint the foreign key references, whatever order the foreign key lists its own
// columns in. The generator's queries pair by that column, on the foreign key itself and
// on the foreign key a referenced column is in turn part of.
//
// The emulator answers that column against the documentation. It gives each foreign key
// column its own position in the foreign key, the same number as ORDINAL_POSITION, and
// it lists the referenced columns in the referenced table's own order, not the order the
// foreign key names them in. So a foreign key that names its columns in another order
// than the key it references reads through the emulator with its columns crossed. The
// cases that set declared pin that as what the emulator does, beside what the schema
// declares, which is what a database that answers as documented reads. Requires the
// Spanner emulator (podman/docker).
func Test_createTableMapUsingQuery_keyOrder(t *testing.T) {
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

	t.Run("the emulator answers each foreign key column's own position", func(t *testing.T) {
		t.Parallel()

		var got []keyPosition
		stmt := spanner.Statement{SQL: `SELECT CONSTRAINT_NAME, COLUMN_NAME, ORDINAL_POSITION, POSITION_IN_UNIQUE_CONSTRAINT
			FROM INFORMATION_SCHEMA.KEY_COLUMN_USAGE
			WHERE CONSTRAINT_NAME IN ('FK_Moorings_Berth', 'FK_Hawsers_Berth', 'FK_Fenders_Berth')
			ORDER BY CONSTRAINT_NAME, ORDINAL_POSITION`}
		if err := spxscan.Select(ctx, db.Single(), &got, stmt); err != nil {
			t.Fatalf("spxscan.Select() error = %v", err)
		}
		// As documented, a hawser's BerthSlot is at position 2 of the berth's key (Dock,
		// Slot) and its BerthDock at position 1, and a fender's columns are crossed the
		// same way against the berth's (Name, Tag).
		want := []keyPosition{
			{Constraint: "FK_Fenders_Berth", Column: "BerthTag", Ordinal: 1, InUnique: 1},
			{Constraint: "FK_Fenders_Berth", Column: "BerthName", Ordinal: 2, InUnique: 2},
			{Constraint: "FK_Hawsers_Berth", Column: "BerthSlot", Ordinal: 1, InUnique: 1},
			{Constraint: "FK_Hawsers_Berth", Column: "BerthDock", Ordinal: 2, InUnique: 2},
			{Constraint: "FK_Moorings_Berth", Column: "BerthDock", Ordinal: 1, InUnique: 1},
			{Constraint: "FK_Moorings_Berth", Column: "BerthSlot", Ordinal: 2, InUnique: 2},
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("the emulator's positions changed; if it now answers as documented, the cases below that set declared read as declared (-want +got):\n%s", diff)
		}
	})

	tableMap, err := createTableMapUsingQuery(ctx, db.Client)
	if err != nil {
		t.Fatalf("createTableMapUsingQuery() error = %v", err)
	}

	tests := []struct {
		name  string
		table string
		// wantReferences maps a column to the table and column it reads as referencing.
		wantReferences  map[string]string
		wantForeignKeys []foreignKeyMeta
		// declared is what the schema declares, set where the emulator's answers cross
		// the columns: a database that answers as documented reads this.
		declared map[string]string
	}{
		{
			name:           "a foreign key that names its columns in the key's order",
			table:          "Moorings",
			wantReferences: map[string]string{"BerthDock": "Berths.Dock", "BerthSlot": "Berths.Slot"},
			wantForeignKeys: []foreignKeyMeta{
				{Name: "FK_Moorings_Berth", Columns: []string{"BerthDock", "BerthSlot"}, ReferencedTable: "Berths", ReferencedColumns: []string{"Dock", "Slot"}, DeleteRule: "NO ACTION"},
			},
		},
		{
			// The mooring's slot is the second column of the mooring's own foreign key, so
			// it leads on to the second column of the berth's key, whatever position the
			// line's column has in the line's foreign key.
			name:           "a column that references the second column of another foreign key reads on to what that column references",
			table:          "Lines",
			wantReferences: map[string]string{"MooringSlot": "Berths.Slot"},
			wantForeignKeys: []foreignKeyMeta{
				{Name: "FK_Lines_MooringSlot", Columns: []string{"MooringSlot"}, ReferencedTable: "Moorings", ReferencedColumns: []string{"BerthSlot"}, DeleteRule: "NO ACTION"},
			},
		},
		{
			name:           "a foreign key that names its columns in another order than the key it references",
			table:          "Hawsers",
			wantReferences: map[string]string{"BerthSlot": "Berths.Dock", "BerthDock": "Berths.Slot"},
			wantForeignKeys: []foreignKeyMeta{
				{Name: "FK_Hawsers_Berth", Columns: []string{"BerthSlot", "BerthDock"}, ReferencedTable: "Berths", ReferencedColumns: []string{"Dock", "Slot"}, DeleteRule: "NO ACTION"},
			},
			declared: map[string]string{"BerthSlot": "Berths.Slot", "BerthDock": "Berths.Dock"},
		},
		{
			name:           "a foreign key to columns that are no key, in another order than the table's columns",
			table:          "Fenders",
			wantReferences: map[string]string{"BerthTag": "Berths.Name", "BerthName": "Berths.Tag"},
			wantForeignKeys: []foreignKeyMeta{
				{Name: "FK_Fenders_Berth", Columns: []string{"BerthTag", "BerthName"}, ReferencedTable: "Berths", ReferencedColumns: []string{"Name", "Tag"}, DeleteRule: "NO ACTION"},
			},
			declared: map[string]string{"BerthTag": "Berths.Tag", "BerthName": "Berths.Name"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			table, ok := tableMap[tt.table]
			if !ok {
				t.Fatalf("table %s not in the table map", tt.table)
			}
			got := make(map[string]string, len(tt.wantReferences))
			for column := range tt.wantReferences {
				got[column] = table.Columns[column].ReferencedTable + "." + table.Columns[column].ReferencedColumn
			}
			if diff := cmp.Diff(tt.wantReferences, got); diff != "" {
				t.Errorf("%s references mismatch (-want +got):\n%s", tt.table, diff)
				if tt.declared != nil {
					t.Logf("the schema declares %v: the pin here is the emulator's crossed answer, and moves to that once the emulator answers as documented", tt.declared)
				}
			}
			if diff := cmp.Diff(tt.wantForeignKeys, table.ForeignKeys); diff != "" {
				t.Errorf("%s foreign keys mismatch (-want +got):\n%s", tt.table, diff)
			}
		})
	}
}
