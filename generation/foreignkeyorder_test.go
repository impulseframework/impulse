package generation

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func Test_declaredForeignKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ddl  []string
		want []declaredForeignKey
	}{
		{
			name: "a named foreign key of a table, as the database prints it",
			ddl: []string{`CREATE TABLE Moorings (
  Id STRING(36) NOT NULL,
  BerthDock STRING(16) NOT NULL,
  BerthSlot INT64 NOT NULL,
  CONSTRAINT FK_Moorings_Berth FOREIGN KEY(BerthDock, BerthSlot) REFERENCES Berths(Dock, Slot),
) PRIMARY KEY(Id)`},
			want: []declaredForeignKey{
				{Table: "Moorings", Name: "FK_Moorings_Berth", Columns: []string{"BerthDock", "BerthSlot"}, ReferencedTable: "Berths", ReferencedColumns: []string{"Dock", "Slot"}},
			},
		},
		{
			name: "the lists of a clause are kept in the order it wrote them",
			ddl:  []string{`CREATE TABLE Hawsers (Id STRING(36), CONSTRAINT FK_Hawsers_Berth FOREIGN KEY(BerthSlot, BerthDock) REFERENCES Berths(Slot, Dock)) PRIMARY KEY(Id)`},
			want: []declaredForeignKey{
				{Table: "Hawsers", Name: "FK_Hawsers_Berth", Columns: []string{"BerthSlot", "BerthDock"}, ReferencedTable: "Berths", ReferencedColumns: []string{"Slot", "Dock"}},
			},
		},
		{
			name: "a foreign key with no name, beside a check that has one, and a second foreign key with its delete rule",
			ddl: []string{`CREATE TABLE Springs (
  Id STRING(36) NOT NULL,
  BerthDock STRING(16) NOT NULL,
  BerthSlot INT64 NOT NULL,
  OwnerId STRING(36) NOT NULL,
  CONSTRAINT CK_Springs_Slot CHECK(BerthSlot > 0),
  FOREIGN KEY (BerthDock, BerthSlot) REFERENCES Berths (Dock, Slot),
  CONSTRAINT FK_Springs_Owner FOREIGN KEY(OwnerId) REFERENCES Owners(Id) ON DELETE CASCADE,
) PRIMARY KEY(Id)`},
			want: []declaredForeignKey{
				{Table: "Springs", Columns: []string{"BerthDock", "BerthSlot"}, ReferencedTable: "Berths", ReferencedColumns: []string{"Dock", "Slot"}},
				{Table: "Springs", Name: "FK_Springs_Owner", Columns: []string{"OwnerId"}, ReferencedTable: "Owners", ReferencedColumns: []string{"Id"}},
			},
		},
		{
			name: "a foreign key added to a table that is already there",
			ddl:  []string{"ALTER TABLE Painters ADD CONSTRAINT FK_Painters_Berth FOREIGN KEY (BerthDock, BerthSlot) REFERENCES Berths (Dock, Slot)"},
			want: []declaredForeignKey{
				{Table: "Painters", Name: "FK_Painters_Berth", Columns: []string{"BerthDock", "BerthSlot"}, ReferencedTable: "Berths", ReferencedColumns: []string{"Dock", "Slot"}},
			},
		},
		{
			name: "names in backquotes and a table under a named schema read as the information schema spells them",
			ddl:  []string{"create table `yard`.`Order` (\n  `Key` STRING(36),\n  constraint `FK_Order_Berth` foreign key(`Dock`, `Slot`) references yard.Berths(`Dock`, `Slot`),\n) PRIMARY KEY(`Key`)"},
			want: []declaredForeignKey{
				{Table: "Order", Name: "FK_Order_Berth", Columns: []string{"Dock", "Slot"}, ReferencedTable: "Berths", ReferencedColumns: []string{"Dock", "Slot"}},
			},
		},
		{
			name: "a statement that declares no table's foreign key holds none",
			ddl: []string{
				"CREATE TABLE Berths (\n  Dock STRING(16) NOT NULL,\n  Slot INT64 NOT NULL,\n) PRIMARY KEY(Dock, Slot)",
				"CREATE INDEX BerthsBySlot ON Berths(Slot)",
				"CREATE VIEW Docks SQL SECURITY INVOKER AS SELECT Berths.Dock FROM Berths",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.want, declaredForeignKeys(tt.ddl)); diff != "" {
				t.Errorf("declaredForeignKeys() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func Test_refuseCrossedForeignKeys(t *testing.T) {
	t.Parallel()

	// A berth is keyed (Dock, Slot), which is the order the read lists the referenced
	// columns in whatever order the foreign key wrote them.
	berth := func(name string, columns ...string) foreignKeyMeta {
		return foreignKeyMeta{Name: name, Columns: columns, ReferencedTable: "Berths", ReferencedColumns: []string{"Dock", "Slot"}}
	}
	inKeyOrder := declaredForeignKey{Table: "Moorings", Name: "FK_Moorings_Berth", Columns: []string{"BerthDock", "BerthSlot"}, ReferencedTable: "Berths", ReferencedColumns: []string{"Dock", "Slot"}}
	crossed := declaredForeignKey{Table: "Hawsers", Name: "FK_Hawsers_Berth", Columns: []string{"BerthSlot", "BerthDock"}, ReferencedTable: "Berths", ReferencedColumns: []string{"Slot", "Dock"}}

	tests := []struct {
		name     string
		tableMap map[string]*tableMetadata
		declared []declaredForeignKey
		// wantErr holds a sentence for each refusal; none means the schema passes.
		wantErr []string
		// wantNot is what the refusal must not name.
		wantNot []string
	}{
		{
			name:     "a foreign key written in the key's order passes",
			tableMap: map[string]*tableMetadata{"Moorings": {ForeignKeys: []foreignKeyMeta{berth("FK_Moorings_Berth", "BerthDock", "BerthSlot")}}},
			declared: []declaredForeignKey{inKeyOrder},
		},
		{
			// The read lists the hawser's columns as it wrote them, (BerthSlot, BerthDock),
			// beside the berth's key as the database holds it, (Dock, Slot): crossed.
			name: "a foreign key written in another order than the key is refused with the line to write, and the one in the key's order is not named",
			tableMap: map[string]*tableMetadata{
				"Hawsers":  {ForeignKeys: []foreignKeyMeta{berth("FK_Hawsers_Berth", "BerthSlot", "BerthDock")}},
				"Moorings": {ForeignKeys: []foreignKeyMeta{berth("FK_Moorings_Berth", "BerthDock", "BerthSlot")}},
			},
			declared: []declaredForeignKey{inKeyOrder, crossed},
			wantErr: []string{
				"table Hawsers: foreign key FK_Hawsers_Berth (BerthSlot, BerthDock) lists its columns in another order than the key it references in Berths. Write it in the key's order: FOREIGN KEY (BerthDock, BerthSlot) REFERENCES Berths (Dock, Slot)",
			},
			wantNot: []string{"Moorings"},
		},
		{
			name: "every foreign key in another order is named, table by table",
			tableMap: map[string]*tableMetadata{
				"Springs": {ForeignKeys: []foreignKeyMeta{berth("FK_Springs_Berths_5C0FFEE_1", "BerthSlot", "BerthDock")}},
				"Hawsers": {ForeignKeys: []foreignKeyMeta{berth("FK_Hawsers_Berth", "BerthSlot", "BerthDock")}},
			},
			declared: []declaredForeignKey{
				crossed,
				// The schema gave the spring's foreign key no name: the database made one.
				{Table: "Springs", Columns: []string{"BerthSlot", "BerthDock"}, ReferencedTable: "Berths", ReferencedColumns: []string{"Slot", "Dock"}},
			},
			wantErr: []string{
				"table Hawsers: foreign key FK_Hawsers_Berth (BerthSlot, BerthDock) lists its columns in another order than the key it references in Berths.",
				"table Springs: foreign key FK_Springs_Berths_5C0FFEE_1 (BerthSlot, BerthDock) lists its columns in another order than the key it references in Berths. Write it in the key's order: FOREIGN KEY (BerthDock, BerthSlot) REFERENCES Berths (Dock, Slot)",
			},
		},
		{
			// A database that answers as documented lists the referenced columns beside
			// the columns that reference them, whatever order the foreign key has.
			name: "a foreign key in another order that the read pairs as declared passes",
			tableMap: map[string]*tableMetadata{"Hawsers": {ForeignKeys: []foreignKeyMeta{
				{Name: "FK_Hawsers_Berth", Columns: []string{"BerthSlot", "BerthDock"}, ReferencedTable: "Berths", ReferencedColumns: []string{"Slot", "Dock"}},
			}}},
			declared: []declaredForeignKey{crossed},
		},
		{
			name: "a foreign key of one column and one the read could not match are not looked at",
			tableMap: map[string]*tableMetadata{"Lines": {ForeignKeys: []foreignKeyMeta{
				{Name: "FK_Lines_MooringSlot", Columns: []string{"MooringSlot"}, ReferencedTable: "Moorings", ReferencedColumns: []string{"BerthSlot"}},
				{Name: "FK_Lines_Lost", Columns: []string{"LostDock", "LostSlot"}},
			}}},
		},
		{
			name:     "a foreign key of two columns that no clause declares is refused",
			tableMap: map[string]*tableMetadata{"Moorings": {ForeignKeys: []foreignKeyMeta{berth("FK_Moorings_Berth", "BerthDock", "BerthSlot")}}},
			declared: []declaredForeignKey{crossed},
			wantErr:  []string{"table Moorings: foreign key FK_Moorings_Berth is not in the DDL the database prints, so the order of its columns could not be checked against the key it references"},
		},
		{
			// Two foreign keys over the same columns to the same key, one pairing them
			// each way: the named one is checked against the clause of its name, though
			// the unnamed clause over the same columns comes first.
			name: "a named foreign key is checked against the clause of its name",
			tableMap: map[string]*tableMetadata{"Swaps": {ForeignKeys: []foreignKeyMeta{
				{Name: "FK_Swaps_Pair", Columns: []string{"A", "B"}, ReferencedTable: "Pairs", ReferencedColumns: []string{"Left", "Right"}},
				{Name: "FK_Swaps_Pairs_5C0FFEE_1", Columns: []string{"A", "B"}, ReferencedTable: "Pairs", ReferencedColumns: []string{"Left", "Right"}},
			}}},
			declared: []declaredForeignKey{
				{Table: "Swaps", Columns: []string{"A", "B"}, ReferencedTable: "Pairs", ReferencedColumns: []string{"Right", "Left"}},
				{Table: "Swaps", Name: "FK_Swaps_Pair", Columns: []string{"A", "B"}, ReferencedTable: "Pairs", ReferencedColumns: []string{"Left", "Right"}},
			},
			wantErr: []string{"table Swaps: foreign key FK_Swaps_Pairs_5C0FFEE_1 (A, B) lists its columns in another order than the key it references in Pairs. Write it in the key's order: FOREIGN KEY (B, A) REFERENCES Pairs (Left, Right)"},
			wantNot: []string{"FK_Swaps_Pair "},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := refuseCrossedForeignKeys(tt.tableMap, tt.declared)
			if len(tt.wantErr) == 0 {
				if err != nil {
					t.Fatalf("refuseCrossedForeignKeys() error = %v, want none", err)
				}

				return
			}
			if err == nil {
				t.Fatalf("refuseCrossedForeignKeys() error = nil, want one containing %q", tt.wantErr)
			}
			for _, want := range tt.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refuseCrossedForeignKeys() error = %v, want it to contain %q", err, want)
				}
			}
			for _, not := range tt.wantNot {
				if strings.Contains(err.Error(), not) {
					t.Errorf("refuseCrossedForeignKeys() error = %v, want it not to name %q", err, not)
				}
			}
		})
	}
}
