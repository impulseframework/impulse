package generation

import (
	"go/format"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session"
)

// Test_tenantRecord_switchIsOutputOnly pins that the tenant record's own write paths
// never write the directory-only switch, for a record that declares it: the field
// decodes output-only, so neither the record's generated create nor its update takes it
// from the wire, in the record's own handler and in the consolidated one, and neither
// asks the rules of the switch, which the tenant's own method asks. The record still
// shows the flag.
func Test_tenantRecord_switchIsOutputOnly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		structs []string
		record  string
		held    bool
	}{
		{name: "a record that declares the switch and its directories record", structs: []string{"Harbor", "HarborDirectory"}, record: "Harbor", held: true},
		{name: "a record with a directories record and no switch", structs: []string{"Sector", "HarborDirectory"}, record: "Sector"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r, err := declarationResources(t, tt.structs...)
			if err != nil {
				t.Fatalf("declarationResources() error = %v", err)
			}
			r.resource = "testdata/tenantfixture"
			var record *resourceInfo
			for _, res := range r.resources {
				if res.Name() == tt.record {
					record = res
				}
			}
			if record == nil {
				t.Fatalf("the fixture resolved no %s record", tt.record)
			}
			if got := record.HoldsDirectoryOnly(); got != tt.held {
				t.Fatalf("HoldsDirectoryOnly() = %v, want %v", got, tt.held)
			}
			if tt.held {
				field := record.DirectoryOnlyField
				if !field.IsOutputOnly() {
					t.Errorf("the switch's field %s is not output only: the record's global write paths could write it", field.Name())
				}
				if got := reflect.StructTag(field.JSONTagForPatch()).Get(jsonTagKey); got != "-" {
					t.Errorf("the switch's field %s is %q in a create or update request, want \"-\": the wire must not write it", field.Name(), got)
				}
				if got := reflect.StructTag(field.JSONTag()).Get(jsonTagKey); got != "directoryOnly" {
					t.Errorf("the switch's field %s reads as %q, want directoryOnly: the record still shows the flag", field.Name(), got)
				}
			}

			standalone, err := r.handlerContent(PatchHandler, record)
			if err != nil {
				t.Fatalf("handlerContent() error = %v", err)
			}
			if _, err := format.Source(append([]byte("package app\n\n"), standalone...)); err != nil {
				t.Fatalf("the rendered handler does not parse: %v\n%s", err, standalone)
			}
			c := &client{}
			consolidated, err := c.generateTemplateOutput("consolidatedPatchTemplate", consolidatedPatchTemplate, consolidatedPatchData{
				Resources:           []*resourceInfo{record},
				GlobalCases:         []consolidatedCaseData{{resourceInfo: record, ResourcePackage: "resources", ReceiverName: "a"}},
				LocalPackageImports: `"example.com/app/pkg/resources"` + "\n\t" + `"example.com/app/pkg/router"`,
				Package:             "app",
				ResourcePackage:     "resources",
				ApplicationName:     "App",
				ReceiverName:        "a",
				HandlerName:         "PatchResources",
			})
			if err != nil {
				t.Fatalf("generateTemplateOutput() error = %v", err)
			}
			if _, err := format.Source(consolidated); err != nil {
				t.Fatalf("the rendered consolidated handler does not parse: %v\n%s", err, consolidated)
			}
			for name, out := range map[string]string{"the record's own handler": string(standalone), "the consolidated handler": string(consolidated)} {
				assertContains(t, name, out, nil, []string{"DirectoryRules().DirectoryOnly(", `patchSet.IsSet("DirectoryOnly")`, "`json:\"directoryOnly\""})
			}
		})
	}
}

// Test_tenantDirectoryOnlyTemplate pins the handlers of the tenant record's
// directory-only switch: the method runs in the tenant's scope to its own Execute
// grant, asks the rules of the switch and then writes the flag through the record's
// update patch, reloads the roster and signals the tenants kind; the two reads are the
// library's, asked under the method's grant, with every auth's reader of the people
// the switch keeps out and the list that decides whether the caller is told who they
// are.
func Test_tenantDirectoryOnlyTemplate(t *testing.T) {
	t.Parallel()

	data := func(people ...*directoryOnlyPeople) *directoryOnlyData {
		return &directoryOnlyData{
			Source: "resources", Package: "app", Name: "Harbor", Word: "harbor", Field: "DirectoryOnly",
			Method: "SetHarborDirectoryOnly", MethodRoute: "set-harbor-directory-only",
			StateFunc: "DirectoryOnly", ConsequenceFunc: "DirectoryOnlyConsequence", StateRoute: "directory-only", ConsequenceRoute: "directory-only/consequence",
			DomainRoute: "harbors/{harborID}", KeyType: "impulse.UUID", KeyIsUUID: true, People: people,
			LocalPackageImports: `"example.com/app/pkg/resources"` + "\n\t" + `"example.com/app/pkg/router"`,
			ResourcePackage:     "resources", RouterPackage: "router", ApplicationName: "App", ReceiverName: "a",
		}
	}
	textKey := data()
	textKey.KeyType, textKey.KeyIsUUID, textKey.FlagType = "string", false, "resources.Flag"
	tests := []struct {
		name            string
		data            *directoryOnlyData
		wantInOrder     []string
		wantContains    []string
		wantNotContains []string
	}{
		{
			name: "the method asks its grant, then the rules, then writes the flag; the reads name each auth's people list",
			data: data(
				&directoryOnlyPeople{Accessor: "StaffIdentity", List: "StaffHarborUsers", Fields: []string{"username", "name"}},
				&directoryOnlyPeople{Accessor: "MembersIdentity", List: "MemberHarborUsers", Fields: []string{"username", "name"}},
			),
			wantInOrder: []string{
				"func (a *App) SetHarborDirectoryOnly() http.HandlerFunc {",
				`resource.MustNewRPCDecoder[resource.SetDirectoryOnlyRequest](a, "SetHarborDirectoryOnly", accesstypes.Execute).WithCollection(router.Collection())`,
				"domain := httpio.Param[accesstypes.Domain](r, router.Domain)",
				"req, err := decoder.Decode(r, accesstypes.DomainScope(domain))",
				"only, err := req.Turn()",
				"id, err := impulse.UUIDFromString(string(domain))",
				"if err := rules.DirectoryOnly(ctx, txn, domain, only); err != nil {",
				"if err := resources.NewHarborUpdatePatch(id).SetDirectoryOnly(only).Buffer(ctx, txn, eventSource); err != nil {",
				"return resource.ErrDryRun",
				"if err := a.TenantRoster().Reload(ctx); err != nil {",
				"if err := a.LiveService().Signal(ctx, resource.KindTenants); err != nil {",
				"func (a *App) DirectoryOnly() http.HandlerFunc {",
				"return harborDirectoryOnlyReads(a).State()",
				"func (a *App) DirectoryOnlyConsequence() http.HandlerFunc {",
				"return harborDirectoryOnlyReads(a).Consequence()",
				`return resource.NewDirectoryOnlyReads(a.ResourceClient(), NewHarborDirectoryRules(), a.UserPermissions, "SetHarborDirectoryOnly", router.Domain,`,
				`resource.DirectoryPeople{List: "StaffHarborUsers", Fields: []accesstypes.Tag{"username", "name"}, Outsiders: a.StaffIdentity().DirectoryOutsiders},`,
				`resource.DirectoryPeople{List: "MemberHarborUsers", Fields: []accesstypes.Tag{"username", "name"}, Outsiders: a.MembersIdentity().DirectoryOutsiders},`,
			},
			wantContains: []string{
				"POST <prefix>/harbors/{harborID}/set-harbor-directory-only",
				"GET\n// <prefix>/harbors/{harborID}/directory-only",
				"GET <prefix>/harbors/{harborID}/directory-only/consequence",
			},
		},
		{
			name:            "a program that generates no auth's management counts and names no person",
			data:            data(),
			wantContains:    []string{"The program generates no auth's management, so the read counts and names no person"},
			wantNotContains: []string{"resource.DirectoryPeople{"},
		},
		{
			name:            "a record keyed by text, with a flag of a type named over a boolean",
			data:            textKey,
			wantContains:    []string{"id := string(domain)", "resources.NewHarborUpdatePatch(id).SetDirectoryOnly(resources.Flag(only)).Buffer(ctx, txn, eventSource)"},
			wantNotContains: []string{"impulse.UUIDFromString"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &client{}
			out, err := c.generateTemplateOutput("tenantDirectoryOnlyTemplate", tenantDirectoryOnlyTemplate, tt.data)
			if err != nil {
				t.Fatalf("generateTemplateOutput() error = %v", err)
			}
			if _, err := format.Source(out); err != nil {
				t.Fatalf("the rendered file does not parse: %v\n%s", err, out)
			}
			if len(tt.wantInOrder) > 0 {
				assertInOrder(t, string(out), tt.wantInOrder...)
			}
			assertContains(t, "the switch's file", string(out), tt.wantContains, tt.wantNotContains)
		})
	}

	// The tenants file no longer carries a read of the switch: the switch has its own file.
	if strings.Contains(tenantsTemplate, "DirectoryOnlyHandler") || strings.Contains(tenantsTemplate, "NewDirectoryOnlyReads") {
		t.Error("tenantsTemplate still serves a read of the directory-only switch")
	}
}

// Test_directoryOnlyRoutes pins where the switch is served and what registers it: the
// method and its two reads under the tenant's own route, behind the domain guard, on
// every session outlet the tenant record is on and on no API-key outlet; a denied case
// for each in the authorization matrix, the method's dry run included; the method in
// the collection with Execute in the tenant's scope; and nothing at all for a record
// that declares no switch. An application's own method or registration under the
// method's name is refused.
func Test_directoryOnlyRoutes(t *testing.T) {
	t.Parallel()

	generator := func(t *testing.T, structs ...string) *resourceGenerator {
		t.Helper()

		r, err := declarationResources(t, structs...)
		if err != nil {
			t.Fatalf("declarationResources() error = %v", err)
		}
		r.genRoutes = true
		r.defaultOutlet = routerOutlet{name: defaultOutletName, prefix: "api", servesSessions: true}
		for _, res := range r.resources {
			res.OutletNames = []string{defaultOutletName, "members", "machines"}
		}
		r.extraOutlets = []routerOutlet{
			{name: "members", prefix: "members", servesSessions: true},
			{name: "machines", prefix: "machines", apiKey: true},
		}

		return r
	}
	routesUnder := func(prefix string) []*generatedRoute {
		base, testBase := "/"+prefix+"/harbors/{harborID}", "/"+prefix+"/harbors/"+domainTestValue

		return []*generatedRoute{
			{Method: "GET", Path: base + "/directory-only", HandlerFunc: "DirectoryOnly", DomainScoped: true, TestURL: testBase + "/directory-only"},
			{Method: "GET", Path: base + "/directory-only/consequence", HandlerFunc: "DirectoryOnlyConsequence", DomainScoped: true, TestURL: testBase + "/directory-only/consequence"},
			{Method: "POST", Path: base + "/set-harbor-directory-only", HandlerFunc: "SetHarborDirectoryOnly", DomainScoped: true, TestURL: testBase + "/set-harbor-directory-only", SelfBounded: true},
		}
	}
	tests := []struct {
		name       string
		structs    []string
		wantRoutes map[string][]*generatedRoute
		wantCases  []string
		wantMethod bool
	}{
		{
			name:       "a record that declares the switch, on every session outlet it is on",
			structs:    []string{"Harbor", "HarborDirectory"},
			wantRoutes: map[string][]*generatedRoute{defaultOutletName: routesUnder("api"), "members": routesUnder("members")},
			wantCases: []string{
				"GET /api/harbors/" + domainTestValue + "/directory-only",
				"GET /api/harbors/" + domainTestValue + "/directory-only/consequence",
				"POST /api/harbors/" + domainTestValue + "/set-harbor-directory-only {}",
				"POST /api/harbors/" + domainTestValue + "/set-harbor-directory-only {} dry run",
				"GET /members/harbors/" + domainTestValue + "/directory-only",
				"GET /members/harbors/" + domainTestValue + "/directory-only/consequence",
				"POST /members/harbors/" + domainTestValue + "/set-harbor-directory-only {}",
				"POST /members/harbors/" + domainTestValue + "/set-harbor-directory-only {} dry run",
			},
			wantMethod: true,
		},
		{
			name:       "a record that declares no switch registers nothing",
			structs:    []string{"Sector", "HarborDirectory"},
			wantRoutes: map[string][]*generatedRoute{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := generator(t, tt.structs...)
			outlets := r.allOutlets()
			outletRoutes := make([]*outletRouteData, len(outlets))
			for i, outlet := range outlets {
				outletRoutes[i] = &outletRouteData{Name: outlet.name, RoutesMap: map[string][]*generatedRoute{}}
			}
			testRoutes := r.accumulateDirectoryOnlyRoute(outlets, outletRoutes)

			gotRoutes := map[string][]*generatedRoute{}
			registered := 0
			for _, outlet := range outletRoutes {
				if routes := outlet.RoutesMap[directoryOnlyRoutesKey]; len(routes) > 0 {
					gotRoutes[outlet.Name] = routes
					registered += len(routes)
				}
			}
			if diff := cmp.Diff(tt.wantRoutes, gotRoutes, cmp.AllowUnexported(generatedRoute{})); diff != "" {
				t.Errorf("registered routes mismatch (-want +got):\n%s", diff)
			}
			if got := len(testRoutes); got != registered {
				t.Errorf("the dispatch test takes %d routes, want every registered one, %d", got, registered)
			}

			var gotCases []string
			for _, c := range r.directoryOnlyAuthzCases() {
				if !c.DeniedOnly {
					t.Errorf("the case %s carries a granted half; the matrix cannot set up a session through a directory", c.Name)
				}
				line := strings.TrimPrefix(c.Method, "http.Method")
				line = strings.ToUpper(line) + " " + c.URL
				if c.Body != "" {
					line += " " + c.Body
				}
				if len(c.Headers) > 0 {
					line += " dry run"
				}
				gotCases = append(gotCases, line)
			}
			if diff := cmp.Diff(tt.wantCases, gotCases); diff != "" {
				t.Errorf("the matrix's cases mismatch (-want +got):\n%s", diff)
			}

			b := resource.NewCollectionBuilder()
			if err := r.collectDirectoryOnlyRegistrations(b); err != nil {
				t.Fatalf("collectDirectoryOnlyRegistrations() error = %v", err)
			}
			var gotMethods []resource.CollectionResource
			for _, res := range b.Data().Resources {
				gotMethods = append(gotMethods, resource.CollectionResource{Name: res.Name, Scope: res.Scope, Permissions: res.Permissions})
			}
			var wantMethods []resource.CollectionResource
			if tt.wantMethod {
				wantMethods = []resource.CollectionResource{{Name: "SetHarborDirectoryOnly", Scope: accesstypes.DomainPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute}}}
			}
			if diff := cmp.Diff(wantMethods, gotMethods); diff != "" {
				t.Errorf("the collection's registrations mismatch (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("an application's registration under the method's name is refused", func(t *testing.T) {
		t.Parallel()

		r := generator(t, "Harbor", "HarborDirectory")
		r.manualRegistrations = []ManualRegistration{{Resource: "SetHarborDirectoryOnly", Permission: accesstypes.Execute}}
		err := r.collectDirectoryOnlyRegistrations(resource.NewCollectionBuilder())
		if err == nil || !strings.Contains(err.Error(), "SetHarborDirectoryOnly is the method that turns the Harbor record's directory-only switch") {
			t.Errorf("collectDirectoryOnlyRegistrations() error = %v, want the refusal naming the method and the record", err)
		}
	})

	t.Run("a run that generates no routes registers no method", func(t *testing.T) {
		t.Parallel()

		r := generator(t, "Harbor", "HarborDirectory")
		r.genRoutes = false
		b := resource.NewCollectionBuilder()
		if err := r.collectDirectoryOnlyRegistrations(b); err != nil {
			t.Fatalf("collectDirectoryOnlyRegistrations() error = %v", err)
		}
		if got := b.Data().Resources; len(got) != 0 {
			t.Errorf("the collection carries %v, want nothing", got)
		}
	})
}

// Test_directoryOnlyClient pins the switch's method on a generated browser client. The
// browser generator reads each struct on its own and resolves no declaration across
// them, so it finds the method by the record's own flag and by the collection the
// handler generator computed: on the client of an outlet that serves the record, in the
// tenant's scope, with the body that says which way to turn; on no client where the
// application serves no such method, and left out of the constants of an outlet the
// record is not on.
func Test_directoryOnlyClient(t *testing.T) {
	t.Parallel()

	// The handler generator's run over the record and its directories record: the
	// collection carries the method.
	r, err := declarationResources(t, "Harbor", "HarborDirectory")
	if err != nil {
		t.Fatalf("declarationResources() error = %v", err)
	}
	r.genRoutes = true
	r.defaultOutlet = routerOutlet{name: defaultOutletName, prefix: "api", servesSessions: true}
	served := resource.NewCollectionBuilder()
	if err := r.collectDirectoryOnlyRegistrations(served); err != nil {
		t.Fatalf("collectDirectoryOnlyRegistrations() error = %v", err)
	}
	// parsed is the same package as the browser generator parses it.
	parsed := func(t *testing.T) *client {
		t.Helper()

		c := &client{tableMap: tenantFixtureTables()}
		resources, err := c.structsToResources(tenantFixtureStructs(t, fixtureStructs(loadFixture(t, "tenantfixture")), "Harbor", "HarborDirectory"))
		if err != nil {
			t.Fatalf("structsToResources() error = %v", err)
		}
		for _, res := range resources {
			if res.HasDeclaration() {
				t.Fatalf("%s carries a resolved declaration, want none: the browser generator resolves no declaration across structs", res.Name())
			}
		}
		c.resources = resources

		return c
	}

	tests := []struct {
		name       string
		collection resource.CollectionData
		outlet     string
		want       *tsDirectoryOnly
	}{
		{name: "the client of the outlet that serves the record", collection: served.Data(), want: &tsDirectoryOnly{Method: "SetHarborDirectoryOnly", Word: "harbor"}},
		{name: "an application that serves no such method", collection: resource.NewCollectionBuilder().Data()},
		{name: "the client of an outlet the record is not on", collection: served.Data(), outlet: "members"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gen := &typescriptGenerator{client: parsed(t), rc: resource.MustNewGeneratedCollection(tt.collection), outletName: tt.outlet}
			// The filter narrows the parsed set to the outlet's members, as a run does
			// before it writes the client.
			filtered, _, err := gen.applyOutletFilter(slices.Clone(gen.resources), nil)
			if err != nil {
				t.Fatalf("applyOutletFilter() error = %v", err)
			}
			gen.resources = filtered
			got := gen.directoryOnlyClientData()
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Fatalf("directoryOnlyClientData() mismatch (-want +got):\n%s", diff)
			}
			excluded := slices.Contains(gen.outletExcluded, accesstypes.Resource("SetHarborDirectoryOnly"))
			if wantExcluded := tt.outlet != ""; excluded != wantExcluded {
				t.Errorf("the method is left out of the outlet's constants = %t, want %t", excluded, wantExcluded)
			}
			if tt.want == nil {
				return
			}

			methods, err := gen.generateTemplateOutput(typescriptMethodsTemplate, typescriptMethodsTemplate, tsMethodsData{File: gen, GenPrefix: "zz_gen", DirectoryOnly: got})
			if err != nil {
				t.Fatalf("generateTemplateOutput(methods) error = %v", err)
			}
			assertContains(t, "typescriptMethodsTemplate", string(methods), []string{
				"export interface SetHarborDirectoryOnlyConfig {\n  directoryOnly: boolean | FieldPointer;\n}",
				"export interface SetHarborDirectoryOnly {\n  directoryOnly: boolean;\n}",
				"[Methods.SetHarborDirectoryOnly]: {\n    route: 'set-harbor-directory-only',\n    dryRun: true,\n    fields: [\n      { fieldName: 'directoryOnly', displayType: 'boolean' },",
			}, nil)
			want := &tsAPIMethod{Name: "SetHarborDirectoryOnly", Property: "setHarborDirectoryOnly", Route: "set-harbor-directory-only", Scope: accesstypes.DomainPermissionScope}
			if diff := cmp.Diff(want, directoryOnlyAPIMethod(got.Method)); diff != "" {
				t.Errorf("directoryOnlyAPIMethod() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Test_tenantDirectoryIn pins the reading of the directory-only switch and the
// directories record off a package's syntax, before the packages are loaded: the
// columns behind the annotated fields, and nothing where the record declares no switch,
// the package no directories record, or a column cannot be read.
func Test_tenantDirectoryIn(t *testing.T) {
	t.Parallel()

	const record = `// Harbor is the tenant record.
	//
	// @resource
	// @tenant
	Harbor struct {
		ID string ` + "`spanner:\"Id\"`" + `
		// Closed says the harbor's people enter through its directory only.
		// @directoryOnly
		Closed bool ` + "`spanner:\"DirectoryOnly\"`" + `
	}
`
	const recordWithoutSwitch = `// Harbor is the tenant record.
	//
	// @resource
	// @tenant
	Harbor struct {
		ID string ` + "`spanner:\"Id\"`" + `
	}
`
	const directories = `// HarborDirectory is the directories record.
	//
	// @resource
	// @permissionScope(domain)
	// @tenantDirectory
	HarborDirectory struct {
		ID string ` + "`spanner:\"Id\"`" + `
		// @domain
		HarborID string ` + "`spanner:\"HarborId\"`" + `
		// @directoryMethod
		Method string ` + "`spanner:\"Method\"`" + `
		// @directoryConnection
		Connection string ` + "`spanner:\"Connection\"`" + `
	}
`
	const directoriesUntagged = `// HarborDirectory is the directories record.
	//
	// @resource
	// @permissionScope(domain)
	// @tenantDirectory
	HarborDirectory struct {
		ID string ` + "`spanner:\"Id\"`" + `
		// @domain
		HarborID string ` + "`spanner:\"HarborId\"`" + `
		// @directoryMethod
		Method string
		// @directoryConnection
		Connection string ` + "`spanner:\"Connection\"`" + `
	}
`
	tests := []struct {
		name    string
		structs []string
		want    *identityTenantDirectory
	}{
		{
			name:    "the switch's column and the directories record's table and columns",
			structs: []string{record, directories},
			want:    &identityTenantDirectory{OnlyColumn: "DirectoryOnly", Table: "HarborDirectories", TenantColumn: "HarborId", MethodColumn: "Method", ConnectionColumn: "Connection"},
		},
		{name: "a record that declares no switch", structs: []string{recordWithoutSwitch, directories}},
		{name: "a package with no directories record", structs: []string{record}},
		{name: "a column the syntax does not name", structs: []string{record, directoriesUntagged}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			source := "package resources\n\ntype (\n\t" + strings.Join(tt.structs, "\n\t") + ")\n"
			if err := os.WriteFile(filepath.Join(dir, "resources.go"), []byte(source), 0o600); err != nil {
				t.Fatalf("os.WriteFile() error = %v", err)
			}
			got, err := tenantDirectoryIn(dir, "Harbor", func(name string) string {
				return strings.TrimSuffix(name, "y") + "ies"
			})
			if err != nil {
				t.Fatalf("tenantDirectoryIn() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("tenantDirectoryIn() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Test_identityTenantUsersTemplate_directoryFacts pins what a tenant's list of its
// people says of the directory-only switch: where the tenant record declares it, the
// list carries whether the person has no sign-in through the tenant's directory and,
// while the tenant is directory only, the sentence that says so, a super administrator
// outside no directory; where the record declares none, the list says nothing of it.
// The client lists the people the switch keeps out from the same fact.
func Test_identityTenantUsersTemplate_directoryFacts(t *testing.T) {
	t.Parallel()

	directory := &identityTenantDirectory{OnlyColumn: "DirectoryOnly", Table: "SectorDirectories", TenantColumn: "SectorId", MethodColumn: "Method", ConnectionColumn: "Connection"}
	data := func(directory *identityTenantDirectory, tables ...identityTable) *identitySourceData {
		inst := &identityInstance{auth: "Crew", tenant: &identityTenant{name: "Sector", table: "Sectors", key: "Id", word: "sector", directory: directory}, tables: map[identityTable]bool{}}
		for _, kind := range tables {
			inst.tables[kind] = true
		}

		return &identitySourceData{
			Source: "Auth", Package: "crewresources", identityInstance: inst, ResourcesPackage: "crewresources", Outlets: "default",
			Tables: session.Tables{Users: "CrewSessionUsers", Identities: "CrewSessionIdentities"},
			Policy: access.Tables{UserRoles: "CrewUserRoles"},
		}
	}
	tests := []struct {
		name            string
		data            *identitySourceData
		wantContains    []string
		wantNotContains []string
	}{
		{
			name: "a tenant record that declares the switch",
			data: data(directory, identityUsers, identityMemberships, identityIdentities),
			wantContains: []string{
				"OutsideDirectory bool ",
				"CannotEnter      *string ",
				// Outer joins both: the facts drop no row of the list.
				"\nLEFT JOIN Sectors ts ON ts.Id = t.DomainId",
				"LEFT JOIN SectorDirectories td ON td.SectorId = ts.Id",
				"SELECT 1 FROM CrewSessionIdentities di",
				"WHERE di.UserId = u.Id AND di.Method = td.Method AND di.Connection = td.Connection",
				"WHERE sa.UserId = u.Id AND sa.Kind = 'global' AND sa.Role = 'SuperAdministrator'",
				") AS OutsideDirectory,",
				"IF(ts.DirectoryOnly AND td.SectorId IS NOT NULL",
				"CONCAT('Has not signed in through ', td.Connection, ', so cannot enter while this sector is directory only')",
				"NULL) AS CannotEnter",
			},
		},
		{
			name: "an auth that keeps no identities signs nobody in through a directory",
			data: data(directory, identityUsers, identityMemberships),
			wantContains: []string{
				"(td.SectorId IS NOT NULL AND NOT EXISTS (\n    SELECT 1 FROM CrewUserRoles sa",
			},
			wantNotContains: []string{"CrewSessionIdentities di"},
		},
		{
			name:            "a tenant record that declares no switch",
			data:            data(nil, identityUsers, identityMemberships, identityIdentities),
			wantContains:    []string{"HeldGlobally bool "},
			wantNotContains: []string{"OutsideDirectory", "CannotEnter", " ts ON ", " td ON "},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &client{}
			out, err := c.generateTemplateOutput("identityTenantUsersTemplate", identityTenantUsersTemplate, tt.data)
			if err != nil {
				t.Fatalf("generateTemplateOutput() error = %v", err)
			}
			if _, err := format.Source(out); err != nil {
				t.Fatalf("the rendered list does not parse: %v\n%s", err, out)
			}
			assertContains(t, "the people's list", string(out), tt.wantContains, tt.wantNotContains)
		})
	}

	// The client answers who the switch keeps out where the record declares it and the
	// instance carries the people's list, and not otherwise.
	listed := data(directory, identityUsers, identityMemberships, identityIdentities)
	if !listed.ListsDirectoryOutsiders() {
		t.Error("ListsDirectoryOutsiders() = false for a record that declares the switch and an auth that lists its people")
	}
	if unlisted := data(nil, identityUsers, identityMemberships); unlisted.ListsDirectoryOutsiders() {
		t.Error("ListsDirectoryOutsiders() = true for a record that declares no switch")
	}
	for _, want := range []string{
		"func (c *Client) DirectoryOutsiders(ctx context.Context, txn resource.ReadWriteTransaction, tenant accesstypes.Domain) ([]resource.DirectoryOutsider, error) {",
		"QueryClause().DomainID().Equal(string(tenant)).And().OutsideDirectory().Equal(true))",
	} {
		if !strings.Contains(identityStoreTemplate, want) {
			t.Errorf("identityStoreTemplate is missing %q", want)
		}
	}
}

// Test_holdIdentityTenantDirectory pins the check of the directory-only switch and the
// directories record the people's list was written for against the ones the load
// found: the two agree or the run stops, and the check runs where the run resolves the
// record's declaration, once it is whole. Asked before, the loaded package would seem to
// declare no switch.
func Test_holdIdentityTenantDirectory(t *testing.T) {
	t.Parallel()

	harbor := &identityTenantDirectory{OnlyColumn: "DirectoryOnly", Table: "HarborDirectories", TenantColumn: "HarborId", MethodColumn: "Method", ConnectionColumn: "Connection"}
	tests := []struct {
		name    string
		structs []string
		record  string
		written *identityTenantDirectory
		wantErr string
	}{
		{name: "the same switch and directories record", structs: []string{"Harbor", "HarborDirectory"}, record: "Harbor", written: harbor},
		{name: "no switch either way", structs: []string{"Sector", "HarborDirectory"}, record: "Sector"},
		{
			name:    "written for a switch the load does not find",
			structs: []string{"Sector", "HarborDirectory"},
			record:  "Sector",
			written: harbor,
			wantErr: "was written for the switch DirectoryOnly over the directories table HarborDirectories (HarborId, Method, Connection)",
		},
		{
			name:    "written for none, and the load finds a switch",
			structs: []string{"Harbor", "HarborDirectory"},
			record:  "Harbor",
			wantErr: "was written for no directory-only switch",
		},
		{
			name:    "written for another column",
			structs: []string{"Harbor", "HarborDirectory"},
			record:  "Harbor",
			written: &identityTenantDirectory{OnlyColumn: "Closed", Table: "HarborDirectories", TenantColumn: "HarborId", MethodColumn: "Method", ConnectionColumn: "Connection"},
			wantErr: "was written for the switch Closed over the directories table HarborDirectories",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			structs := fixtureStructs(loadFixture(t, "tenantfixture"))
			c := &client{tableMap: tenantFixtureTables()}
			resources, err := c.structsToResources(tenantFixtureStructs(t, structs, tt.structs...))
			if err != nil {
				t.Fatalf("structsToResources() error = %v", err)
			}
			c.resources = resources
			r := &resourceGenerator{client: c, routePrefix: "api", applicationName: "App", receiverName: "a", domainRouteSegment: defaultDomainRouteSegment, domainRouteParam: defaultDomainRouteParam}
			r.resource = "testdata/tenantfixture"
			r.identityTree = "pkg/identity"
			r.identity = []*identityInstance{{auth: "Crew", tenant: &identityTenant{name: tt.record, directory: tt.written}}}

			// The run's own step: the record's declaration resolves, then the reading
			// before the load is held to it.
			err = r.resolveCrossKindDeclarations()
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("resolveCrossKindDeclarations() error = %v", err)
				}

				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) || !strings.Contains(err.Error(), "fault of the generator") {
				t.Errorf("resolveCrossKindDeclarations() error = %v, want one containing %q", err, tt.wantErr)
			}
		})
	}

	t.Run("a run with no generated management holds nothing", func(t *testing.T) {
		t.Parallel()

		r, err := declarationResources(t, "Harbor", "HarborDirectory")
		if err != nil {
			t.Fatalf("declarationResources() error = %v", err)
		}
		if err := r.holdIdentityTenantDirectory(); err != nil {
			t.Errorf("holdIdentityTenantDirectory() error = %v", err)
		}
	})
}
