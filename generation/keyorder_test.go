package generation

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	initiator "github.com/impulseframework/impulse/db"
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

// keyOrderDatabase migrates the named fixture directories of testdata, one layered over
// the other in the order given, into a database of their own on an emulator the test
// releases.
func keyOrderDatabase(ctx context.Context, t *testing.T, fixtures ...string) *initiator.SpannerDB {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() failed")
	}
	sources := make([]string, 0, len(fixtures))
	for _, fixture := range fixtures {
		sources = append(sources, "file://"+filepath.Join(filepath.Dir(thisFile), "testdata", fixture))
	}

	db, spannerContainer, err := createSpannerDB(ctx, "1.5.56", sources)
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

	return db
}

// Test_createTableMapUsingQuery_keyOrder reads foreign keys of two columns through the
// emulator and pins how each column is paired with the column it references, where the
// foreign key names its columns in the order of the key it references: named in the
// table, left without a name, and added once the table is there. Each is found in the
// DDL the database prints, which is what its order is checked against. Requires the
// Spanner emulator (podman/docker).
func Test_createTableMapUsingQuery_keyOrder(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("the schema read requires the Spanner emulator")
	}

	ctx := context.Background()
	db := keyOrderDatabase(ctx, t, "migrations")

	tableMap, err := createTableMapUsingQuery(ctx, db)
	if err != nil {
		t.Fatalf("createTableMapUsingQuery() error = %v", err)
	}

	tests := []struct {
		name  string
		table string
		// wantReferences maps a column to the table and column it reads as referencing.
		wantReferences  map[string]string
		wantForeignKeys []foreignKeyMeta
		// namedByDatabase marks a foreign key the schema gives no name: the database
		// names it, and the name is not compared.
		namedByDatabase bool
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
			name:           "a foreign key the schema gives no name",
			table:          "Springs",
			wantReferences: map[string]string{"BerthDock": "Berths.Dock", "BerthSlot": "Berths.Slot"},
			wantForeignKeys: []foreignKeyMeta{
				{Columns: []string{"BerthDock", "BerthSlot"}, ReferencedTable: "Berths", ReferencedColumns: []string{"Dock", "Slot"}, DeleteRule: "NO ACTION"},
			},
			namedByDatabase: true,
		},
		{
			name:           "a foreign key added once the table is there",
			table:          "Painters",
			wantReferences: map[string]string{"BerthDock": "Berths.Dock", "BerthSlot": "Berths.Slot"},
			wantForeignKeys: []foreignKeyMeta{
				{Name: "FK_Painters_Berth", Columns: []string{"BerthDock", "BerthSlot"}, ReferencedTable: "Berths", ReferencedColumns: []string{"Dock", "Slot"}, DeleteRule: "NO ACTION"},
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
			got := make(map[string]string, len(tt.wantReferences))
			for column := range tt.wantReferences {
				got[column] = table.Columns[column].ReferencedTable + "." + table.Columns[column].ReferencedColumn
			}
			if diff := cmp.Diff(tt.wantReferences, got); diff != "" {
				t.Errorf("%s references mismatch (-want +got):\n%s", tt.table, diff)
			}
			var options cmp.Options
			if tt.namedByDatabase {
				options = append(options, cmpopts.IgnoreFields(foreignKeyMeta{}, "Name"))
			}
			if diff := cmp.Diff(tt.wantForeignKeys, table.ForeignKeys, options); diff != "" {
				t.Errorf("%s foreign keys mismatch (-want +got):\n%s", tt.table, diff)
			}
		})
	}
}

// Test_createTableMapUsingQuery_sharedColumn reads a schema whose tenant column is in a
// foreign key of its own and in foreign keys of two columns beside another column, to
// another table and to the table's own rows, and pins that each column reads the table and
// column its own foreign key names, the tenant column the tenant's. Requires the Spanner
// emulator (podman/docker).
func Test_createTableMapUsingQuery_sharedColumn(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("the schema read requires the Spanner emulator")
	}

	ctx := context.Background()
	db := keyOrderDatabase(ctx, t, "sharedcolumn")

	tableMap, err := createTableMapUsingQuery(ctx, db)
	if err != nil {
		t.Fatalf("createTableMapUsingQuery() error = %v", err)
	}

	tests := []struct {
		name  string
		table string
		// wantReferences maps a column to the table and column it reads as referencing.
		wantReferences map[string]string
	}{
		{
			name:           "a tenant column in a key of its own, a key to another table and a key to its own rows",
			table:          "Zones",
			wantReferences: map[string]string{"HarborId": "Harbors.Id", "YardId": "Yards.Id", "InsideZoneId": "Zones.Id"},
		},
		{
			name:           "a tenant column in a key of its own and a key of two columns",
			table:          "Yards",
			wantReferences: map[string]string{"HarborId": "Harbors.Id", "WharfId": "Wharves.Id"},
		},
		{
			name:           "a tenant column in two keys of two columns and none of its own",
			table:          "Piers",
			wantReferences: map[string]string{"HarborId": "Harbors.Id"},
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
			}
		})
	}
}

// Test_createTableMapUsingQuery_crossedForeignKey reads a schema with foreign keys that
// name their columns in another order than the key they reference, and pins both why
// the generator refuses them and that it does.
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
// it lists the referenced columns in the order it holds them in, not the order the
// foreign key names them in. So a foreign key that names its columns in another order
// than the key it references reads through the emulator with its columns crossed, and
// the generator reads every schema through the emulator. The first subtest pins that
// answer. The DDL the emulator prints keeps the two lists of each foreign key as the
// schema wrote them, and a foreign key the read pairs otherwise than that is refused,
// with the line that says the same link in the key's order. Requires the Spanner
// emulator (podman/docker).
func Test_createTableMapUsingQuery_crossedForeignKey(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("the schema read requires the Spanner emulator")
	}

	ctx := context.Background()
	db := keyOrderDatabase(ctx, t, "migrations", "crossedkeys")

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
			t.Errorf("the emulator's positions changed; if it now answers as documented, the hawser's and the fender's foreign keys read as declared and are no longer refused (-want +got):\n%s", diff)
		}
	})

	t.Run("a foreign key in another order than the key it references is refused with the line to write", func(t *testing.T) {
		t.Parallel()

		tableMap, err := createTableMapUsingQuery(ctx, db)
		if err == nil {
			hawser, fender := tableMap["Hawsers"], tableMap["Fenders"]
			t.Fatalf("createTableMapUsingQuery() error = nil, want the two foreign keys refused; it read the hawser's as %+v and the fender's as %+v", hawser.ForeignKeys, fender.ForeignKeys)
		}
		for _, want := range []string{
			"table Hawsers: foreign key FK_Hawsers_Berth (BerthSlot, BerthDock) lists its columns in another order than the key it references in Berths. Write it in the key's order: FOREIGN KEY (BerthDock, BerthSlot) REFERENCES Berths (Dock, Slot)",
			"table Fenders: foreign key FK_Fenders_Berth (BerthTag, BerthName) lists its columns in another order than the key it references in Berths. Write it in the key's order: FOREIGN KEY (BerthName, BerthTag) REFERENCES Berths (Name, Tag)",
		} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("createTableMapUsingQuery() error = %v, want it to contain %q", err, want)
			}
		}
		// The foreign keys in the key's order are in the same schema, and none is named.
		for _, not := range []string{"Moorings", "Springs", "Painters", "Stubs", "Seals"} {
			if strings.Contains(err.Error(), "table "+not+":") {
				t.Errorf("createTableMapUsingQuery() error = %v, want it not to name the table %s", err, not)
			}
		}
	})
}
