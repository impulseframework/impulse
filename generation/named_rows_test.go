package generation

import (
	"go/format"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
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
// tenant key, or a tenant's row named from a resource of no tenant. A column picked from a
// list of roles (a computed list or a view whose key is marked @roleOf) is declared as any
// other: only a method's field is left to the method's own grant.
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
		{
			name:    "a column picked from a list of roles, a computed list's or a view's, is held as any other",
			structs: []string{"Base", "Duty", "Rank", "Watch"},
			want: map[string][]string{
				"Watch": {
					`{Field: "DutyName", Resource: "Duties", Table: "", KeyColumn: "", Computed: true}`,
					`{Field: "RankName", Resource: "Ranks", Table: "Ranks", KeyColumn: "Name", View: true}`,
				},
			},
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

// pickedRowsOf resolves the picked fields of the namefixture request Dispatch, on a method
// of a tenant unless global and whose handler is generated unless handWritten, in a run of
// the namefixture structs named with the List of each resource in unlisted suppressed and
// each table in enumerations an enumeration table besides Grades, and answers the fields
// declared as literals, in field order.
func pickedRowsOf(t *testing.T, global, handWritten bool, unlisted, enumerations []string, names ...string) []string {
	t.Helper()

	c, err := nameFixtureResources(t, names...)
	if err != nil {
		t.Fatalf("nameFixtureResources() error = %v", err)
	}
	for _, res := range c.resources {
		if slices.Contains(unlisted, res.Name()) {
			res.SuppressedHandlers = append(res.SuppressedHandlers, ListHandler)
		}
	}
	for _, table := range enumerations {
		c.enumerateTables[table] = table
	}
	dispatch := fixtureStructs(loadFixture(t, "namefixture"))["Dispatch"]
	annotations, err := scanStruct(dispatch)
	if err != nil {
		t.Fatalf("scanStruct(Dispatch) error = %v", err)
	}
	method := &rpcMethodInfo{Struct: dispatch, PermissionScope: accesstypes.DomainPermissionScope, SuppressHandler: handWritten}
	if global {
		method.PermissionScope = accesstypes.GlobalPermissionScope
	}
	for i, f := range dispatch.Fields() {
		field := &rpcField{Field: f}
		c.declareRPCEnumeration(field, annotations.Fields[i])
		method.Fields = append(method.Fields, field)
	}
	if dispatch.HasErrors() {
		t.Fatalf("Dispatch has errors:\n%s", dispatch.PrintErrors())
	}
	(&resourceGenerator{client: c, genRoutes: true}).resolvePickedRows([]*rpcMethodInfo{method})
	got := []string{}
	for _, row := range method.PickedRows() {
		got = append(got, row.Literal())
	}

	return got
}

// Test_resolvePickedRows pins which fields of a method's request the run declares as naming
// a row of another resource the application lists, as a column of a resource is declared: a
// field-scope @enumerate of a listed table, with its tenant facts, or none for a table of no
// tenant; of a listed view; of a computed list whose list the run serves, its List asked in
// the tenant for a tenant's list; and a field of several keys, once. A field picked from the
// tenant record is declared on a tenant's method, as a table of no tenant is. Never Stored.
// Not a field picked from an enumeration table (Grades, and a listed table the run takes for
// one), from a list of roles (a computed list or a view whose key is marked @roleOf), from a
// computed list with no list or a table the run does not list, a field picking nothing, a
// tenant's row or the tenant record on a method of the whole application, or any field of a
// method whose handler is written by hand.
func Test_resolvePickedRows(t *testing.T) {
	t.Parallel()

	bases := `{Field: "BaseID", Resource: "Bases", Table: "Bases", KeyColumn: "Id"}`
	wings := `{Field: "WingID", Resource: "Wings", Table: "Wings", KeyColumn: "Id", TenantColumn: "BaseId"}`
	kits := `{Field: "KitID", Resource: "Kits", Table: "Kits", KeyColumn: "Id"}`
	hangars := `{Field: "HangarID", Resource: "Hangars", Table: "Hangars", KeyColumn: "Id", TenantColumn: "BaseId", View: true}`
	drills := `{Field: "DrillID", Resource: "Drills", Table: "", KeyColumn: "", Computed: true}`
	shifts := `{Field: "ShiftID", Resource: "Shifts", Table: "", KeyColumn: "", Computed: true, GrantInTenant: true}`
	crates := `{Field: "CrateIDs", Resource: "Crates", Table: "Crates", KeyColumn: "Id", TenantColumn: "BaseId"}`
	sources := []string{"Base", "Wing", "Kit", "Crate", "Hangar", "Drill", "Shift", "Ration", "Duty", "Rank"}
	tests := []struct {
		name         string
		global       bool
		handWritten  bool
		unlisted     []string
		enumerations []string
		want         []string
	}{
		{
			name: "a method of a tenant picking from each kind of source, the tenant record among them",
			want: []string{bases, wings, kits, hangars, drills, shifts, crates},
		},
		{
			name:   "a method of the whole application names no tenant's row, and no tenant",
			global: true,
			want:   []string{kits, drills},
		},
		{
			name:     "a table the run does not list",
			unlisted: []string{"Crate"},
			want:     []string{bases, wings, kits, hangars, drills, shifts},
		},
		{
			name:         "a key into an enumeration table the run also lists",
			enumerations: []string{"Kits"},
			want:         []string{bases, wings, hangars, drills, shifts, crates},
		},
		{
			name:        "a method whose handler is written by hand",
			handWritten: true,
			want:        []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := pickedRowsOf(t, tt.global, tt.handWritten, tt.unlisted, tt.enumerations, sources...)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("the picked fields declared mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// requestField stands for a field of a method's request, by its Go name.
type requestField string

// Name is the field's Go name.
func (f requestField) Name() string {
	return string(f)
}

// Test_rpcHandlerTemplate_pickedRows pins the frame's hold on a method's picked fields:
// one HoldPicked call over every declared field, in field order, a field of several keys
// handed as its slice, on the decoded caller, or the gate's for a method with a target;
// once the request is decoded and before the warning is asked (and, for an upload, before
// the files stream), so a refused row answers 404 before anything runs. A method with no
// declared field renders no call.
func Test_rpcHandlerTemplate_pickedRows(t *testing.T) {
	t.Parallel()

	picked := []*namedRow{
		{Field: requestField("WingID"), Resource: "Wings", Table: "Wings", KeyColumn: "Id", TenantColumn: "BaseId"},
		{Field: requestField("CrateIDs"), Resource: "Crates", Table: "Crates", KeyColumn: "Id", TenantColumn: "BaseId"},
		{Field: requestField("DrillID"), Resource: "Drills", Computed: true},
	}
	held := func(caller, method string) string {
		return "if err := " + caller + ".HoldPicked(ctx, a.ResourceClient(), \"" + method + "\",\n" +
			"\t\t\tresource.PickedRow{Row: resource.NamedRow{Field: \"WingID\", Resource: \"Wings\", Table: \"Wings\", KeyColumn: \"Id\", TenantColumn: \"BaseId\"}, Value: p.WingID},\n" +
			"\t\t\tresource.PickedRow{Row: resource.NamedRow{Field: \"CrateIDs\", Resource: \"Crates\", Table: \"Crates\", KeyColumn: \"Id\", TenantColumn: \"BaseId\"}, Value: p.CrateIDs},\n" +
			"\t\t\tresource.PickedRow{Row: resource.NamedRow{Field: \"DrillID\", Resource: \"Drills\", Table: \"\", KeyColumn: \"\", Computed: true}, Value: p.DrillID},\n" +
			"\t\t); err != nil {\n" +
			"\t\t\treturn httpio.NewEncoder(w).ClientMessage(ctx, err)\n" +
			"\t\t}"
	}

	wire := fixtureStructs(loadFixture(t, "wirefixture"))
	// inspect is the wire fixture's transaction-form method, holding the fields picked, an
	// upload where upload.
	inspect := func(picked []*namedRow, upload bool) *rpcMethodInfo {
		method := fixtureAnsweringMethod(t, wire)
		method.pickedRows = picked
		if upload {
			method.Upload = &rpcUpload{MaxBytes: 5 << 20}
			method.takesFiles = true
		}

		return method
	}
	binding := fixtureStructs(loadFixture(t, "bindingfixture"))
	bindingClient := transitionFixtureClient(t, binding)
	closeTask, err := resolveFixtureTransition(t, bindingClient, binding, "CloseTask")
	if err != nil {
		t.Fatalf("resolveFixtureTransition(CloseTask) error = %v", err)
	}
	for _, field := range closeTask.Struct.Fields() {
		closeTask.Fields = append(closeTask.Fields, &rpcField{Field: field})
	}
	closeTask.pickedRows = picked

	tests := []struct {
		name      string
		client    *client
		template  string
		method    *rpcMethodInfo
		wantHeld  string
		wantOrder []string
	}{
		{
			name:      "a method with no target holds them on its caller",
			client:    &client{},
			template:  rpcHandlerTemplate,
			method:    inspect(picked, false),
			wantHeld:  held("caller", "Inspect"),
			wantOrder: []string{"decoder.DecodeCaller(r,", "\t\tp := ", "caller.HoldPicked(", "asksWarning, err := resource.AsksWarning(r)", "ExecuteFunc("},
		},
		{
			name:      "a method with a target holds them on the gate's caller",
			client:    bindingClient,
			template:  rpcHandlerTemplate,
			method:    closeTask,
			wantHeld:  held("gate.Caller()", "CloseTask"),
			wantOrder: []string{"decoder.Decode(r,", "\t\tp := ", "gate.Caller().HoldPicked(", "asksWarning, err := resource.AsksWarning(r)", "ExecuteFunc("},
		},
		{
			name:      "an upload holds them before the files stream",
			client:    &client{},
			template:  rpcUploadHandlerTemplate,
			method:    inspect(picked, true),
			wantHeld:  held("caller", "Inspect"),
			wantOrder: []string{"decoder.DecodeCaller(upload.Request(),", "\t\tp := ", "caller.HoldPicked(", "asksWarning, err := resource.AsksWarning(r)", "dryRun, err := resource.IsDryRun(r)", "ExecuteFunc("},
		},
		{
			name:     "a method with no declared field holds none",
			client:   &client{},
			template: rpcHandlerTemplate,
			method:   inspect(nil, false),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out, err := tt.client.generateTemplateOutput("handler", tt.template, &rpcHandlerData{
				Source:           "pkg/rpc",
				RPCMethod:        tt.method,
				Package:          "app",
				ApplicationName:  "App",
				ReceiverName:     "a",
				ResourcesPackage: "resources",
			})
			if err != nil {
				t.Fatalf("generateTemplateOutput() error = %v", err)
			}
			formatted, err := format.Source(out)
			if err != nil {
				t.Fatalf("format.Source() error = %v on:\n%s", err, out)
			}
			text := string(formatted)
			if tt.wantHeld == "" {
				if strings.Contains(text, "HoldPicked") {
					t.Errorf("handler holds a picked field it does not declare:\n%s", text)
				}

				return
			}
			if got := strings.Count(text, ".HoldPicked("); got != 1 {
				t.Errorf("handler calls HoldPicked %d times, want once:\n%s", got, text)
			}
			if !strings.Contains(text, tt.wantHeld) {
				t.Errorf("handler missing\n%s\nin\n%s", tt.wantHeld, text)
			}
			at := -1
			for _, next := range tt.wantOrder {
				i := strings.Index(text, next)
				if i < 0 {
					t.Fatalf("handler missing %q:\n%s", next, text)
				}
				if i <= at {
					t.Errorf("handler has %q before what must come first (%v):\n%s", next, tt.wantOrder, text)
				}
				at = i
			}
		})
	}
}
