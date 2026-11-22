package generation

import (
	"context"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// tenantKeyFixture extracts the namefixture's tenant record and the resources around it
// over the fixture's tables, which declare no whole foreign key, so nothing is refused.
func tenantKeyFixture(t *testing.T) (*client, []*resourceInfo) {
	t.Helper()

	structs := fixtureStructs(loadFixture(t, "namefixture"))
	c := &client{tableMap: nameFixtureTables(), enumerateTables: map[string]string{"Grades": "Grade"}}
	resources, err := c.structsToResources(tenantFixtureStructs(t, structs, "Base", "Wing", "Bay", "Berth", "Kit", "Crate", "Permit"))
	if err != nil {
		t.Fatalf("structsToResources() error = %v", err)
	}

	return c, resources
}

func Test_refuseKeysWithoutTenant(t *testing.T) {
	t.Parallel()

	// Every table keeps its tenant in BaseId but a bay, which reads it through its wing.
	key := func(name string, columns []string, table string, referencedColumns ...string) foreignKeyMeta {
		return foreignKeyMeta{Name: name, Columns: columns, ReferencedTable: table, ReferencedColumns: referencedColumns}
	}
	const (
		permitToWing = "table Permits: foreign key FK_Permits_WingId (WingId) to Wings (Id) leaves out the tenant, so a row of one tenant could name a row of another. " +
			"Write it with the tenant column of each table: FOREIGN KEY (WingId, BaseId) REFERENCES Wings (Id, BaseId)"
		crateToCrate = "table Crates: foreign key FK_Crates_InsideCrateId (InsideCrateId) to Crates (Id) leaves out the tenant, so a row of one tenant could name a row of another. " +
			"Write it with the tenant column of each table: FOREIGN KEY (InsideCrateId, BaseId) REFERENCES Crates (Id, BaseId)"
	)

	tests := []struct {
		name string
		// keys are the foreign keys set on each table.
		keys map[string][]foreignKeyMeta
		// keyedByTenant keys the wings by (BaseId, Id).
		keyedByTenant bool
		// wantErr holds a sentence for each refusal, in order; none means the schema passes.
		wantErr []string
	}{
		{
			name:    "a key to another tenant's table on its id alone is refused",
			keys:    map[string][]foreignKeyMeta{"Permits": {key("FK_Permits_WingId", []string{"WingId"}, "Wings", "Id")}},
			wantErr: []string{permitToWing},
		},
		{
			name: "a key that holds the tenant passes",
			keys: map[string][]foreignKeyMeta{"Permits": {key("FK_Permits_BaseIdWingId", []string{"BaseId", "WingId"}, "Wings", "BaseId", "Id")}},
		},
		{
			name: "a key that holds the tenant in another order passes, its order is not this rule's",
			keys: map[string][]foreignKeyMeta{"Permits": {key("FK_Permits_WingIdBaseId", []string{"WingId", "BaseId"}, "Wings", "Id", "BaseId")}},
		},
		{
			name: "a key that holds the tenant column beside another column is refused",
			keys: map[string][]foreignKeyMeta{"Permits": {key("FK_Permits_BaseIdWingId", []string{"BaseId", "WingId"}, "Wings", "CallSign", "Id")}},
			wantErr: []string{
				"table Permits: foreign key FK_Permits_BaseIdWingId (BaseId, WingId) to Wings (CallSign, Id) leaves out the tenant, so a row of one tenant could name a row of another. " +
					"Write it with the tenant column of each table: FOREIGN KEY (WingId, BaseId) REFERENCES Wings (Id, BaseId)",
			},
		},
		{
			name:          "a key to a table keyed by its tenant and its id is printed in that key's order",
			keys:          map[string][]foreignKeyMeta{"Permits": {key("FK_Permits_WingId", []string{"WingId"}, "Wings", "Id")}},
			keyedByTenant: true,
			wantErr: []string{
				"table Permits: foreign key FK_Permits_WingId (WingId) to Wings (Id) leaves out the tenant, so a row of one tenant could name a row of another. " +
					"Write it with the tenant column of each table: FOREIGN KEY (BaseId, WingId) REFERENCES Wings (BaseId, Id)",
			},
		},
		{
			name:    "a key to the table's own rows on their id alone is refused",
			keys:    map[string][]foreignKeyMeta{"Crates": {key("FK_Crates_InsideCrateId", []string{"InsideCrateId"}, "Crates", "Id")}},
			wantErr: []string{crateToCrate},
		},
		{
			name: "a key to a table that is no tenant's passes",
			keys: map[string][]foreignKeyMeta{"Permits": {key("FK_Permits_KitId", []string{"KitId"}, "Kits", "Id")}},
		},
		{
			name: "a key to the tenant record passes",
			keys: map[string][]foreignKeyMeta{"Permits": {key("FK_Permits_BaseId", []string{"BaseId"}, "Bases", "Id")}},
		},
		{
			name: "a key to a table that reads its tenant through its rows is not judged",
			keys: map[string][]foreignKeyMeta{"Berths": {key("FK_Berths_BayId", []string{"BayId"}, "Bays", "Id")}},
		},
		{
			name: "a key from a table that reads its tenant through its rows is not judged",
			keys: map[string][]foreignKeyMeta{"Bays": {key("FK_Bays_WingId", []string{"WingId"}, "Wings", "Id")}},
		},
		{
			name: "a key to a table no resource declares passes",
			keys: map[string][]foreignKeyMeta{"Permits": {key("FK_Permits_CrateId", []string{"CrateId"}, "Hulls", "Id")}},
		},
		{
			name: "a key the read could not match passes",
			keys: map[string][]foreignKeyMeta{"Permits": {{Name: "FK_Permits_WingId", Columns: []string{"WingId"}}}},
		},
		{
			name: "every refusal of the run is reported, table by table",
			keys: map[string][]foreignKeyMeta{
				"Permits": {key("FK_Permits_WingId", []string{"WingId"}, "Wings", "Id")},
				"Crates":  {key("FK_Crates_InsideCrateId", []string{"InsideCrateId"}, "Crates", "Id")},
			},
			wantErr: []string{crateToCrate, permitToWing},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, resources := tenantKeyFixture(t)
			for table, keys := range tt.keys {
				c.tableMap[table].ForeignKeys = keys
			}
			if tt.keyedByTenant {
				wings := c.tableMap["Wings"]
				wings.PkCount = 2
				for column, ordinal := range map[string]int64{"BaseId": 1, "Id": 2} {
					meta := wings.Columns[column]
					meta.IsPrimaryKey, meta.KeyOrdinalPosition = true, ordinal
					wings.Columns[column] = meta
				}
			}

			err := c.refuseKeysWithoutTenant(resources)
			if len(tt.wantErr) == 0 {
				if err != nil {
					t.Fatalf("refuseKeysWithoutTenant() error = %v, want none", err)
				}

				return
			}
			if err == nil {
				t.Fatalf("refuseKeysWithoutTenant() error = nil, want %q", tt.wantErr)
			}
			got := err.Error()
			if !strings.Contains(got, "a foreign key between two tables that keep their tenant in a column") {
				t.Errorf("refuseKeysWithoutTenant() error = %v, want it wrapped once for the run", err)
			}
			if n := strings.Count(got, "leaves out the tenant"); n != len(tt.wantErr) {
				t.Errorf("refuseKeysWithoutTenant() error = %v, want %d refusals, got %d", err, len(tt.wantErr), n)
			}
			// Each sentence whole, and each after the one before it.
			at := 0
			for _, want := range tt.wantErr {
				i := strings.Index(got[at:], want)
				if i < 0 {
					t.Fatalf("refuseKeysWithoutTenant() error = %v, want %q after what came before it", err, want)
				}
				at += i + len(want)
			}
		})
	}
}

// Test_structsToResources_keyWithoutTenant pins that extraction holds the schema to the
// rule once every resource is extracted.
func Test_structsToResources_keyWithoutTenant(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "namefixture"))
	c := &client{tableMap: nameFixtureTables(), enumerateTables: map[string]string{"Grades": "Grade"}}
	c.tableMap["Permits"].ForeignKeys = []foreignKeyMeta{{Name: "FK_Permits_WingId", Columns: []string{"WingId"}, ReferencedTable: "Wings", ReferencedColumns: []string{"Id"}}}

	_, err := c.structsToResources(tenantFixtureStructs(t, structs, "Base", "Wing", "Bay", "Berth", "Kit", "Crate", "Permit"))
	want := "table Permits: foreign key FK_Permits_WingId (WingId) to Wings (Id) leaves out the tenant, so a row of one tenant could name a row of another. " +
		"Write it with the tenant column of each table: FOREIGN KEY (WingId, BaseId) REFERENCES Wings (Id, BaseId)"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("structsToResources() error = %v, want one containing %q", err, want)
	}
}

// Test_createTableMapUsingQuery_tenantKeyOrder proves that a key written as the refusal
// prints it is the key the database takes: each fixture's foreign keys of two columns
// hold the tenant, and each is written as keyHoldingTenant prints the key with the tenant
// taken out. The emulator reads every one as declared, so the order check refuses none.
// The tenantkeys fixture holds a key to a table keyed by its tenant and its id and a key
// to a table keyed by its id with a unique index on its tenant and its id; the
// sharedcolumn fixture keys to another table's id and to the table's own rows. Requires the Spanner emulator (podman/docker).
func Test_createTableMapUsingQuery_tenantKeyOrder(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("the schema read requires the Spanner emulator")
	}

	tests := []struct {
		fixture string
		// tenantColumn is the column every table of the fixture keeps its tenant in.
		tenantColumn string
		// wantKeys is the fixture's foreign keys of two columns.
		wantKeys []string
	}{
		{
			fixture:      "tenantkeys",
			tenantColumn: "RealmId",
			wantKeys:     []string{"FK_Bins_RealmIdDepotId", "FK_Shelves_VaultIdRealmId"},
		},
		{
			fixture:      "sharedcolumn",
			tenantColumn: "HarborId",
			wantKeys: []string{
				"FK_Docks_HarborIdShedId", "FK_Piers_HarborIdWharfId", "FK_Piers_HarborIdYardId", "FK_Sheds_HarborIdYardId",
				"FK_Yards_HarborIdWharfId", "FK_Zones_HarborIdInsideZoneId", "FK_Zones_HarborIdYardId",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			db := keyOrderDatabase(ctx, t, tt.fixture)

			tableMap, err := createTableMapUsingQuery(ctx, db)
			if err != nil {
				t.Fatalf("createTableMapUsingQuery() error = %v", err)
			}
			ddl, err := db.DDL(ctx)
			if err != nil {
				t.Fatalf("DDL() error = %v", err)
			}
			declared := declaredForeignKeys(ddl)

			var keys []string
			for _, table := range slices.Sorted(maps.Keys(tableMap)) {
				for i := range tableMap[table].ForeignKeys {
					key := &tableMap[table].ForeignKeys[i]
					if len(key.Columns) != 2 {
						continue
					}
					keys = append(keys, key.Name)

					declaration := key.declaration(table, declared)
					if declaration == nil {
						t.Errorf("table %s: foreign key %s is not in the DDL", table, key.Name)

						continue
					}
					// The key with its tenant taken out, as a key that leaves out the tenant
					// would be read.
					withoutTenant := foreignKeyMeta{Name: key.Name, ReferencedTable: key.ReferencedTable}
					for j, column := range key.Columns {
						if column == tt.tenantColumn && key.ReferencedColumns[j] == tt.tenantColumn {
							continue
						}
						withoutTenant.Columns = append(withoutTenant.Columns, column)
						withoutTenant.ReferencedColumns = append(withoutTenant.ReferencedColumns, key.ReferencedColumns[j])
					}
					if len(withoutTenant.Columns) != 1 {
						t.Errorf("table %s: foreign key %s (%s) does not hold the tenant", table, key.Name, strings.Join(key.Columns, ", "))

						continue
					}

					columns, referencedColumns := tableMap[key.ReferencedTable].keyHoldingTenant(&withoutTenant, tt.tenantColumn, tt.tenantColumn)
					got := foreignKeyClause(columns, key.ReferencedTable, referencedColumns)
					want := foreignKeyClause(declaration.Columns, declaration.ReferencedTable, declaration.ReferencedColumns)
					if got != want {
						t.Errorf("table %s: foreign key %s is printed %s, want it as the DDL declares it, %s", table, key.Name, got, want)
					}
				}
			}
			if diff := cmp.Diff(tt.wantKeys, keys); diff != "" {
				t.Errorf("foreign keys of two columns mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
