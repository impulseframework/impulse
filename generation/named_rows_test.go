package generation

import (
	"go/format"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// namedRowsOf resolves the named rows of the namefixture structs named, in a run that
// generates routes unless noRoutes, with the List of each resource in unlisted
// suppressed and each table in enumerations an enumeration table besides Grades, and
// answers each resource's declared columns as literals.
func namedRowsOf(t *testing.T, noRoutes bool, unlisted, enumerations []string, names ...string) map[string][]string {
	t.Helper()

	c, err := nameFixtureResources(t, names...)
	if err != nil {
		t.Fatalf("nameFixtureResources() error = %v", err)
	}
	for _, table := range enumerations {
		// Grade, the fixture's struct for Grades, marks a name the generator refuses, so
		// a table the run lists stands for an enumeration table that is a resource of
		// the run.
		c.enumerateTables[table] = table
	}
	for _, res := range c.resources {
		for _, name := range unlisted {
			if res.Name() == name {
				res.SuppressedHandlers = append(res.SuppressedHandlers, ListHandler)
			}
		}
	}
	r := &resourceGenerator{client: c, genRoutes: !noRoutes}
	r.resolveNamedRows(c.resources)
	got := map[string][]string{}
	for _, res := range c.resources {
		for _, row := range res.NamedRows() {
			got[res.Name()] = append(got[res.Name()], row.Literal())
		}
	}

	return got
}

// Test_resolveNamedRows pins which columns a run declares as naming a row of another
// resource the application lists: a foreign key to a listed table, and a field-scope
// @enumerate of one, each with the named table's tenant facts, or none for a table of no
// tenant; and not a key into an enumeration table (Grades, and a listed table the run
// takes for one), a table the run does not list, a view, the row's own tenant key, or a
// tenant's row named from a resource of no tenant.
func Test_resolveNamedRows(t *testing.T) {
	t.Parallel()

	wings := `{Field: "WingID", Resource: "Wings", Table: "Wings", KeyColumn: "Id", TenantColumn: "BaseId"}`
	tests := []struct {
		name         string
		structs      []string
		noRoutes     bool
		unlisted     []string
		enumerations []string
		want         map[string][]string
	}{
		{
			name:    "a tenant's resource pointing to each kind of row",
			structs: []string{"Base", "Wing", "Kit", "Crate", "Member", "Permit"},
			want: map[string][]string{
				"Permit": {
					wings,
					`{Field: "LeadWing", Resource: "Wings", Table: "Wings", KeyColumn: "Id", TenantColumn: "BaseId"}`,
					`{Field: "KitID", Resource: "Kits", Table: "Kits", KeyColumn: "Id"}`,
					`{Field: "CrateID", Resource: "Crates", Table: "Crates", KeyColumn: "Id", TenantColumn: "BaseId"}`,
				},
			},
		},
		{
			name:    "a row that reaches its tenant through another table is held to it that way",
			structs: []string{"Base", "Wing", "Bay", "Berth"},
			want: map[string][]string{
				"Bay":   {wings},
				"Berth": {`{Field: "BayID", Resource: "Bays", Table: "Bays", KeyColumn: "Id", TenantColumn: "WingId", TenantPath: []resource.BindingHop{{Table: "Wings", JoinColumn: "Id", Column: "BaseId"}}}`},
			},
		},
		{
			name:     "a table the run does not list",
			structs:  []string{"Base", "Wing", "Kit", "Crate", "Member", "Permit"},
			unlisted: []string{"Crate"},
			want: map[string][]string{
				"Permit": {
					wings,
					`{Field: "LeadWing", Resource: "Wings", Table: "Wings", KeyColumn: "Id", TenantColumn: "BaseId"}`,
					`{Field: "KitID", Resource: "Kits", Table: "Kits", KeyColumn: "Id"}`,
				},
			},
		},
		{
			name:         "a key into an enumeration table the run also lists",
			structs:      []string{"Base", "Wing", "Kit", "Crate", "Member", "Permit"},
			enumerations: []string{"Kits"},
			want: map[string][]string{
				"Permit": {
					wings,
					`{Field: "LeadWing", Resource: "Wings", Table: "Wings", KeyColumn: "Id", TenantColumn: "BaseId"}`,
					`{Field: "CrateID", Resource: "Crates", Table: "Crates", KeyColumn: "Id", TenantColumn: "BaseId"}`,
				},
			},
		},
		{
			name:    "a resource of no tenant names no tenant's row",
			structs: []string{"Base", "Wing", "Kit", "Stock"},
			want:    map[string][]string{"Stock": {`{Field: "KitID", Resource: "Kits", Table: "Kits", KeyColumn: "Id"}`}},
		},
		{
			name:     "a run that generates no routes lists nothing",
			structs:  []string{"Base", "Wing", "Kit", "Crate", "Member", "Permit"},
			noRoutes: true,
			want:     map[string][]string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := namedRowsOf(t, tt.noRoutes, tt.unlisted, tt.enumerations, tt.structs...)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("the named rows declared mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Test_resourceFileTemplate_namedRows pins the generated NamedRows method: a resource
// with a column that names a listed row declares it for the library, and one with none
// declares no such method.
func Test_resourceFileTemplate_namedRows(t *testing.T) {
	t.Parallel()

	c, err := nameFixtureResources(t, "Base", "Wing", "Bay", "Berth")
	if err != nil {
		t.Fatalf("nameFixtureResources() error = %v", err)
	}
	(&resourceGenerator{client: c, genRoutes: true}).resolveNamedRows(c.resources)

	rendered := make(map[string]string)
	for _, res := range c.resources {
		out, err := (&client{}).generateTemplateOutput("resourceFile", resourceFileTemplate, &resourceFileData{Source: "fixture", Package: "resources", Resource: res})
		if err != nil {
			t.Fatalf("generateTemplateOutput(%s) error = %v", res.Name(), err)
		}
		formatted, err := format.Source(out)
		if err != nil {
			t.Fatalf("format.Source(%s) error = %v on:\n%s", res.Name(), err, out)
		}
		rendered[res.Name()] = string(formatted)
	}

	want := "// NamedRows names the columns that name a row of another resource the application lists\n" +
		"// (BayID): a create or an update sets one only to a row its caller can list, so the\n" +
		"// library answers any other as a row that is not there.\n" +
		"func (Berth) NamedRows() []resource.NamedRow {\n" +
		"\treturn []resource.NamedRow{\n" +
		"\t\t{Field: \"BayID\", Resource: \"Bays\", Table: \"Bays\", KeyColumn: \"Id\", TenantColumn: \"WingId\", TenantPath: []resource.BindingHop{{Table: \"Wings\", JoinColumn: \"Id\", Column: \"BaseId\"}}},\n" +
		"\t}\n" +
		"}"
	if !strings.Contains(rendered["Berth"], want) {
		t.Errorf("resource file mismatch: want\n%s\nin\n%s", want, rendered["Berth"])
	}
	if strings.Contains(rendered["Wing"], "NamedRows") {
		t.Errorf("Wing, which names no listed row but its tenant, declares NamedRows:\n%s", rendered["Wing"])
	}
}
