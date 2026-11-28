package generation

import (
	"testing"
)

// Test_structsToResources_keyWithOrWithoutTenant pins that extraction takes a foreign key
// between two tables that each keep their tenant in a column whether or not the key holds
// the tenant: which of the two a link is written with is the schema's choice.
func Test_structsToResources_keyWithOrWithoutTenant(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		key  foreignKeyMeta
	}{
		{
			name: "a key on the other row's id alone",
			key:  foreignKeyMeta{Name: "FK_Permits_WingId", Columns: []string{"WingId"}, ReferencedTable: "Wings", ReferencedColumns: []string{"Id"}},
		},
		{
			name: "a key that holds the tenant",
			key:  foreignKeyMeta{Name: "FK_Permits_WingIdBaseId", Columns: []string{"WingId", "BaseId"}, ReferencedTable: "Wings", ReferencedColumns: []string{"Id", "BaseId"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			structs := fixtureStructs(loadFixture(t, "namefixture"))
			c := &client{tableMap: nameFixtureTables(), enumerateTables: map[string]string{"Grades": "Grade"}}
			c.tableMap["Permits"].ForeignKeys = []foreignKeyMeta{tt.key}

			if _, err := c.structsToResources(tenantFixtureStructs(t, structs, "Base", "Wing", "Bay", "Berth", "Kit", "Crate", "Permit")); err != nil {
				t.Fatalf("structsToResources() error = %v", err)
			}
		})
	}
}
