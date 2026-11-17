package generation

import (
	"go/format"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/session"
)

// Test_tenantRecord_updateHoldsDirectoryOnly pins the tenant record's generated update
// for a record that declares the directory-only switch: a write of the switch is held to
// its rules before the patch is buffered, the caller's grants asked first, in the
// record's own handler and in the consolidated one; a record that declares no switch
// asks nothing.
func Test_tenantRecord_updateHoldsDirectoryOnly(t *testing.T) {
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
				if !tt.held {
					assertContains(t, name, out, nil, []string{"DirectoryRules().DirectoryOnly(", `patchSet.IsSet("DirectoryOnly")`})

					continue
				}
				// The caller's grants answer before the rules do, and the rules before the
				// write is buffered.
				assertInOrder(t, out,
					"case resource.OperationUpdate:",
					`if patchSet.IsSet("DirectoryOnly") {`,
					"if err := patchSet.Permitted(ctx, txn); err != nil {",
					`if err := NewHarborDirectoryRules().DirectoryOnly(ctx, txn, accesstypes.Domain(id.String()), patchSet.Get("DirectoryOnly")); err != nil {`,
					`return errors.Wrap(err, "resource.TenantDirectoryRules.DirectoryOnly()")`,
					".NewHarborUpdatePatchFromPatchSet(id, patchSet).Buffer(ctx, txn, eventSource)",
				)
			}
		})
	}
}

// Test_tenantsTemplate_directoryOnly pins the read of what the directory-only switch
// would do in the tenants file: the handler delegates to the library's with the record's
// rules, its resource and the switch's field as grants name them, and every auth's
// reader of the people the switch keeps out; a record that declares no switch gets no
// handler.
func Test_tenantsTemplate_directoryOnly(t *testing.T) {
	t.Parallel()

	data := func(directoryOnly *directoryOnlyData) *tenantsData {
		return &tenantsData{
			Source: "resources", Package: "app", Name: "Harbor", Table: "Harbors", KeyColumn: "Id", RecordLower: "harbor", Directories: "HarborDirectory",
			Declaration:         &tenantDeclarationColumns{DirectoryOnly: "DirectoryOnly", DirectoriesTable: "HarborDirectories", TenantColumn: "HarborId", MethodColumn: "Method", ConnectionColumn: "Connection"},
			Directory:           harborDirectoryWrite(),
			Word:                "harbor",
			ResourcePackage:     "resources",
			LocalPackageImports: `"example.com/app/pkg/resources"`,
			ApplicationName:     "App",
			ReceiverName:        "a",
			DirectoryOnly:       directoryOnly,
		}
	}
	tests := []struct {
		name            string
		data            *tenantsData
		wantContains    []string
		wantNotContains []string
	}{
		{
			name: "the handler asks every auth's client who the switch keeps out",
			data: data(&directoryOnlyData{HandlerFunc: "DirectoryOnly", Route: "directory-only", Resource: "Harbors", Tag: "directoryOnly", Field: "DirectoryOnly", Outsiders: []string{"StaffIdentity", "MembersIdentity"}}),
			wantContains: []string{
				`"net/http"`,
				"func (a *App) DirectoryOnly() http.HandlerFunc {",
				`return resource.DirectoryOnlyHandler(a.ResourceClient(), NewHarborDirectoryRules(), a.UserPermissions, "Harbors", "directoryOnly",`,
				"a.StaffIdentity().DirectoryOutsiders,",
				"a.MembersIdentity().DirectoryOutsiders,",
				"GET <prefix>/directory-only?domain=<harbor>",
			},
		},
		{
			name: "a program that generates no auth's management names no person",
			data: data(&directoryOnlyData{HandlerFunc: "DirectoryOnly", Route: "directory-only", Resource: "Harbors", Tag: "directoryOnly", Field: "DirectoryOnly"}),
			wantContains: []string{
				"func (a *App) DirectoryOnly() http.HandlerFunc {",
				"The program generates no auth's management, so the read names no person",
			},
			wantNotContains: []string{".DirectoryOutsiders,"},
		},
		{
			name:            "a record with no switch served to sessions gets no handler",
			data:            data(nil),
			wantNotContains: []string{"DirectoryOnlyHandler", `"net/http"`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &client{}
			out, err := c.generateTemplateOutput("tenantsTemplate", tenantsTemplate, tt.data)
			if err != nil {
				t.Fatalf("generateTemplateOutput() error = %v", err)
			}
			if _, err := format.Source(out); err != nil {
				t.Fatalf("the rendered tenants file does not parse: %v\n%s", err, out)
			}
			assertContains(t, "the tenants file", string(out), tt.wantContains, tt.wantNotContains)
		})
	}
}

// Test_accumulateDirectoryOnlyRoute pins the read's registration: a GET under the prefix
// of every session outlet the tenant record is on, none on an API-key outlet, and none
// at all for a record that declares no switch.
func Test_accumulateDirectoryOnlyRoute(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		structs    []string
		wantRoutes map[string]*generatedRoute
	}{
		{
			name:    "a record that declares the switch, on every session outlet it is on",
			structs: []string{"Harbor", "HarborDirectory"},
			wantRoutes: map[string]*generatedRoute{
				defaultOutletName: {Method: "GET", Path: "/api/directory-only", HandlerFunc: "DirectoryOnly", TestURL: "/api/directory-only"},
				"members":         {Method: "GET", Path: "/members/directory-only", HandlerFunc: "DirectoryOnly", TestURL: "/members/directory-only"},
			},
		},
		{
			name:       "a record that declares no switch registers no route",
			structs:    []string{"Sector", "HarborDirectory"},
			wantRoutes: map[string]*generatedRoute{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r, err := declarationResources(t, tt.structs...)
			if err != nil {
				t.Fatalf("declarationResources() error = %v", err)
			}
			r.defaultOutlet = routerOutlet{name: defaultOutletName, prefix: "api", servesSessions: true}
			for _, res := range r.resources {
				res.OutletNames = []string{defaultOutletName, "members", "machines"}
			}
			r.extraOutlets = []routerOutlet{
				{name: "members", prefix: "members", servesSessions: true},
				{name: "machines", prefix: "machines", apiKey: true},
			}
			outlets := r.allOutlets()
			outletRoutes := make([]*outletRouteData, len(outlets))
			for i, outlet := range outlets {
				outletRoutes[i] = &outletRouteData{Name: outlet.name, RoutesMap: map[string][]*generatedRoute{}}
			}
			testRoutes := r.accumulateDirectoryOnlyRoute(outlets, outletRoutes)

			gotRoutes := map[string]*generatedRoute{}
			for _, outlet := range outletRoutes {
				routes := outlet.RoutesMap["DirectoryOnly"]
				if len(routes) == 0 {
					continue
				}
				if len(routes) != 1 {
					t.Errorf("outlet %s registers %d routes, want 1", outlet.Name, len(routes))
				}
				gotRoutes[outlet.Name] = routes[0]
			}
			if diff := cmp.Diff(tt.wantRoutes, gotRoutes, cmp.AllowUnexported(generatedRoute{})); diff != "" {
				t.Errorf("registered routes mismatch (-want +got):\n%s", diff)
			}
			if got, want := len(testRoutes), len(tt.wantRoutes); got != want {
				t.Errorf("the dispatch test takes %d routes, want %d", got, want)
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
				"JOIN Sectors ts ON ts.Id = t.DomainId",
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
