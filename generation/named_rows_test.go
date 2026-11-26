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
// tenant; a field-scope @enumerate of a listed view, with the view's own tenant column,
// and of a computed list whose list the run serves, its List asked in the tenant for a
// tenant's list, each with the table its foreign key references where it has one; and
// not a key into an enumeration table (Grades, and a listed table the run takes for one),
// a table or a view the run does not list, a computed list with no list, the row's own
// tenant key, or a tenant's row named from a resource of no tenant.
func Test_resolveNamedRows(t *testing.T) {
	t.Parallel()

	wings := `{Field: "WingID", Resource: "Wings", Table: "Wings", KeyColumn: "Id", TenantColumn: "BaseId"}`
	members := `{Field: "MemberID", Resource: "Members", Table: "Members", KeyColumn: "Id", View: true}`
	hangars := `{Field: "HangarID", Resource: "Hangars", Table: "Hangars", KeyColumn: "Id", TenantColumn: "BaseId", View: true}`
	flights := `{Field: "WingID", Resource: "Flights", Table: "Flights", KeyColumn: "Id", TenantColumn: "BaseId", View: true, Stored: &resource.NamedRow{Table: "Wings", KeyColumn: "Id", TenantColumn: "BaseId"}}`
	drills := `{Field: "DrillID", Resource: "Drills", Table: "", KeyColumn: "", Computed: true}`
	shifts := `{Field: "ShiftID", Resource: "Shifts", Table: "", KeyColumn: "", Computed: true, GrantInTenant: true}`
	picking := []string{"Base", "Wing", "Hangar", "Member", "Flight", "Drill", "Shift", "Ration", "Rota", "Depot"}
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
					members,
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
					members,
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
					members,
				},
			},
		},
		{
			name:    "a resource of no tenant names no tenant's row",
			structs: []string{"Base", "Wing", "Kit", "Stock"},
			want:    map[string][]string{"Stock": {`{Field: "KitID", Resource: "Kits", Table: "Kits", KeyColumn: "Id"}`}},
		},
		{
			name:    "views and computed lists",
			structs: picking,
			want: map[string][]string{
				"Rota":  {hangars, members, flights, drills, shifts},
				"Depot": {drills},
			},
		},
		{
			name:     "a view the run does not list",
			structs:  picking,
			unlisted: []string{"Hangar"},
			want: map[string][]string{
				"Rota":  {members, flights, drills, shifts},
				"Depot": {drills},
			},
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
// with a column that names a listed row declares it for the library, a view's and a
// computed list's with what the library asks them by, and one with none declares no such
// method.
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

	c, err = nameFixtureResources(t, "Base", "Wing", "Hangar", "Member", "Flight", "Drill", "Shift", "Ration", "Rota")
	if err != nil {
		t.Fatalf("nameFixtureResources() error = %v", err)
	}
	(&resourceGenerator{client: c, genRoutes: true}).resolveNamedRows(c.resources)
	for _, res := range c.resources {
		if res.Name() != "Rota" {
			continue
		}
		out, err := (&client{}).generateTemplateOutput("resourceFile", resourceFileTemplate, &resourceFileData{Source: "fixture", Package: "resources", Resource: res})
		if err != nil {
			t.Fatalf("generateTemplateOutput(Rota) error = %v", err)
		}
		formatted, err := format.Source(out)
		if err != nil {
			t.Fatalf("format.Source(Rota) error = %v on:\n%s", err, out)
		}
		for _, line := range []string{
			"\t\t{Field: \"WingID\", Resource: \"Flights\", Table: \"Flights\", KeyColumn: \"Id\", TenantColumn: \"BaseId\", View: true, Stored: &resource.NamedRow{Table: \"Wings\", KeyColumn: \"Id\", TenantColumn: \"BaseId\"}},\n",
			"\t\t{Field: \"ShiftID\", Resource: \"Shifts\", Table: \"\", KeyColumn: \"\", Computed: true, GrantInTenant: true},\n",
		} {
			if !strings.Contains(string(formatted), line) {
				t.Errorf("Rota's NamedRows lacks\n%s\nin\n%s", line, formatted)
			}
		}
	}
}
