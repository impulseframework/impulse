package generation

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/generation/parser"
	"github.com/impulseframework/impulse/resource"
)

// acrossFixtureTables is the synthetic schema behind the acrossfixture structs: every
// table carries its PRIMARY_KEY index and the index shape the struct's doc comment
// names, with the per-column flags derived from the index list the way the schema read
// derives them.
func acrossFixtureTables() map[string]*tableMetadata {
	pk := columnMeta{IsPrimaryKey: true}
	tenant := columnMeta{IsForeignKey: true, ReferencedTable: "Tenants", ReferencedColumn: "Id"}
	plain := columnMeta{}
	primaryKey := indexMeta{Name: "PRIMARY_KEY", PrimaryKey: true, Unique: true, Key: []indexColumn{{Column: "Id"}}}
	backing := func(table string) indexMeta {
		return indexMeta{Name: "IDX_" + table + "_TenantId", Managed: true, Key: []indexColumn{{Column: "TenantId"}}}
	}
	dated := map[string]columnMeta{"Id": pk, "TenantId": tenant, "DueAt": plain, "Label": plain}

	tables := map[string]*tableMetadata{
		"Shipments": {PkCount: 1, Columns: dated, Indexes: []indexMeta{primaryKey, backing("Shipments"), {
			Name:    "ShipmentsByDueAt",
			Key:     []indexColumn{{Column: "DueAt", Descending: true}},
			Storing: []string{"TenantId"},
		}}},
		"Parcels": {PkCount: 1, Columns: dated, Indexes: []indexMeta{primaryKey, backing("Parcels"), {
			Name: "ParcelsByTenantIdDueAtLabel",
			Key:  []indexColumn{{Column: "TenantId"}, {Column: "DueAt"}, {Column: "Label", Descending: true}},
		}}},
		"Cartons": {PkCount: 1, Columns: dated, Indexes: []indexMeta{primaryKey, backing("Cartons"), {
			Name: "CartonsByTenantIdDueAt",
			Key:  []indexColumn{{Column: "TenantId"}, {Column: "DueAt"}},
		}, {
			Name: "CartonsByDueAt",
			Key:  []indexColumn{{Column: "DueAt"}},
		}}},
		"Pallets": {PkCount: 1, Columns: dated, Indexes: []indexMeta{primaryKey, backing("Pallets"), {
			Name: "PalletsByTenantIdDueAt",
			Key:  []indexColumn{{Column: "TenantId"}, {Column: "DueAt"}},
		}, {
			Name: "PalletsByDueAtTenantId",
			Key:  []indexColumn{{Column: "DueAt"}, {Column: "TenantId"}},
		}}},
		"Drums": {PkCount: 2, Columns: map[string]columnMeta{
			"TenantId": {IsPrimaryKey: true, IsForeignKey: true, ReferencedTable: "Tenants", ReferencedColumn: "Id"},
			"Sequence": {IsPrimaryKey: true, KeyOrdinalPosition: 1},
			"Label":    plain,
		}, Indexes: []indexMeta{
			{Name: "PRIMARY_KEY", PrimaryKey: true, Unique: true, Key: []indexColumn{{Column: "TenantId"}, {Column: "Sequence"}}},
			{Name: "DrumsByTenantIdLabel", Key: []indexColumn{{Column: "TenantId"}, {Column: "Label"}}},
			{Name: "DrumsByLabel", Key: []indexColumn{{Column: "Label"}}},
		}},
		"Crates": {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "TenantId": tenant, "Label": plain}, Indexes: []indexMeta{primaryKey, backing("Crates")}},
		"Seals": {PkCount: 1, Columns: map[string]columnMeta{
			"Id": pk, "CrateId": {IsForeignKey: true, ReferencedTable: "Crates", ReferencedColumn: "Id"}, "Mark": plain,
		}, Indexes: []indexMeta{primaryKey, {Name: "IDX_Seals_CrateId", Managed: true, Key: []indexColumn{{Column: "CrateId"}}}}},
		"Ledgers": {PkCount: 1, Columns: dated, Indexes: []indexMeta{primaryKey, backing("Ledgers"), {
			Name: "LedgersByTenantIdDueAt",
			Key:  []indexColumn{{Column: "TenantId"}, {Column: "DueAt"}},
		}}},
		"Depots": {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "Name": plain}, Indexes: []indexMeta{primaryKey}},
		"Vaults": {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "TenantId": tenant}, Indexes: []indexMeta{primaryKey, backing("Vaults")}},
	}
	for _, table := range tables {
		table.deriveIndexFlags()
	}

	return tables
}

// acrossFixture loads the acrossfixture structs over their schema.
func acrossFixture(t *testing.T) (c *client, structs map[string]*parser.Struct) {
	t.Helper()

	return &client{tableMap: acrossFixtureTables()}, fixtureStructs(loadFixture(t, "acrossfixture"))
}

// acrossFixtureTablesOf extracts the named table-backed fixture structs.
func acrossFixtureTablesOf(t *testing.T, c *client, structs map[string]*parser.Struct, names ...string) []*resourceInfo {
	t.Helper()

	picked := make([]*parser.Struct, 0, len(names))
	for _, name := range names {
		if structs[name] == nil {
			t.Fatalf("struct %q not found in fixture package", name)
		}
		picked = append(picked, structs[name])
	}
	resources, err := c.structsToResources(picked)
	if err != nil {
		t.Fatalf("structsToResources(%v) error = %v", names, err)
	}

	return resources
}

// acrossFixtureGenerator builds the generator the route tests share: Shipment, listed
// across tenants, and Ledger, which is not, on the default outlet beside an extra one
// neither is on.
func acrossFixtureGenerator(t *testing.T) *resourceGenerator {
	t.Helper()

	c, structs := acrossFixture(t)
	c.resources = acrossFixtureTablesOf(t, c, structs, "Shipment", "Ledger")

	return &resourceGenerator{
		client:             c,
		genRoutes:          true,
		routePrefix:        "api",
		domainRouteSegment: "tenants",
		domainRouteParam:   "tenantID",
		applicationName:    "TestApp",
		receiverName:       "a",
		extraOutlets:       []routerOutlet{{name: "droids", prefix: "droids"}},
	}
}

// Test_resolveListAcrossTenants pins the declaration: accepted on a tenant-scoped
// table and view with a list, and refused, saying what to do, on a global resource, on
// one whose list is suppressed, on a computed resource and on an RPC method.
func Test_resolveListAcrossTenants(t *testing.T) {
	t.Parallel()

	c, structs := acrossFixture(t)

	t.Run("a tenant-scoped table declares it", func(t *testing.T) {
		t.Parallel()

		resources := acrossFixtureTablesOf(t, c, structs, "Shipment", "Ledger")
		if !resources[0].ListsAcrossTenants {
			t.Error("Shipment declares @listAcrossTenants, and ListsAcrossTenants is false")
		}
		if resources[1].ListsAcrossTenants {
			t.Error("Ledger declares no @listAcrossTenants, and ListsAcrossTenants is true")
		}
	})

	t.Run("a tenant-scoped view declares it", func(t *testing.T) {
		t.Parallel()

		views, err := c.structsToVirtualResources([]*parser.Struct{structs["Board"]})
		if err != nil {
			t.Fatalf("structsToVirtualResources() error = %v", err)
		}
		if !views[0].ListsAcrossTenants {
			t.Error("Board declares @listAcrossTenants, and ListsAcrossTenants is false")
		}
	})

	refusals := []struct {
		name    string
		extract func() error
		want    string
	}{
		{
			name: "a global table",
			extract: func() error {
				_, err := c.structsToResources([]*parser.Struct{structs["Depot"]})

				return err
			},
			want: "struct Depot: @listAcrossTenants on a global resource; a global resource is not served under a tenant's segment and its list already reads every row, so remove the annotation, or scope the resource to a tenant with @permissionScope(domain)",
		},
		{
			name: "a table whose list is suppressed",
			extract: func() error {
				_, err := c.structsToResources([]*parser.Struct{structs["Vault"]})

				return err
			},
			want: "struct Vault: @listAcrossTenants on a resource whose list is suppressed (@suppress); the list across tenants is the resource's own list asked without the tenant's segment, so keep the list or remove the annotation",
		},
		{
			name: "a global view",
			extract: func() error {
				_, err := c.structsToVirtualResources([]*parser.Struct{structs["GlobalBoard"]})

				return err
			},
			want: "struct GlobalBoard: @listAcrossTenants on a global resource",
		},
		{
			name: "a computed resource",
			extract: func() error {
				_, err := c.structsToCompResources([]*parser.Struct{structs["Tally"]})

				return err
			},
			want: "struct Tally: @listAcrossTenants on a computed resource; its rows come from the application's own function, which answers for the one tenant it is handed, so remove the annotation: a list across tenants is declared on a table-backed (@resource) or view (@virtual) resource",
		},
		{
			name: "an RPC method",
			extract: func() error {
				_, err := c.structsToRPCMethods([]*parser.Struct{structs["Recount"]})

				return err
			},
			want: "struct Recount: @listAcrossTenants on a RPC method; it is declared on a tenant-scoped resource with a list, table-backed (@resource) or a view (@virtual), so remove the annotation",
		},
	}
	for _, tt := range refusals {
		t.Run(tt.name+" is refused", func(t *testing.T) {
			t.Parallel()

			err := tt.extract()
			if err == nil {
				t.Fatal("the declaration was accepted, want it refused")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to say %q", err, tt.want)
			}
		})
	}
}

// Test_resourceAcrossTenantsRoutes pins the route of a list across tenants: the list's
// path without the tenant's segment pair, on the handler named after the list handler,
// outside the tenant's guard, answering GET and POST as the list does; and none for a
// resource that does not declare it.
func Test_resourceAcrossTenantsRoutes(t *testing.T) {
	t.Parallel()

	r := acrossFixtureGenerator(t)
	shipment, ledger := r.resources[0], r.resources[1]

	want := []*generatedRoute{{
		Method:      "GET",
		Path:        "/api/shipments",
		HandlerFunc: "ShipmentsAcrossTenants",
		HandlerType: acrossTenantsHandler,
		TestURL:     "/api/shipments",
	}}
	got := r.resourceAcrossTenantsRoutes(shipment, "api")
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(generatedRoute{})); diff != "" {
		t.Errorf("resourceAcrossTenantsRoutes(Shipment) mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"http.MethodGet", "http.MethodPost"}, got[0].TestMethods()); diff != "" {
		t.Errorf("TestMethods() mismatch (-want +got):\n%s", diff)
	}
	if routes := r.resourceAcrossTenantsRoutes(ledger, "api"); len(routes) != 0 {
		t.Errorf("resourceAcrossTenantsRoutes(Ledger) = %v, want none: Ledger does not declare the list", routes)
	}

	// The tenant's own list stays where it was.
	list, err := r.resourceRoute(shipment, ListHandler, "api")
	if err != nil {
		t.Fatalf("resourceRoute() error = %v", err)
	}
	if list.Path != "/api/tenants/{tenantID}/shipments" || !list.DomainScoped {
		t.Errorf("the tenant's list = %s (domain-scoped %t), want /api/tenants/{tenantID}/shipments under the guard", list.Path, list.DomainScoped)
	}
}

// Test_accumulateResourceRoutes_acrossTenants pins where the route lands: in the
// resource's routes on every outlet it is on, in the dispatch test's routes, and, for
// an outlet it is not on, among the URLs that must answer 404 there.
func Test_accumulateResourceRoutes_acrossTenants(t *testing.T) {
	t.Parallel()

	r := acrossFixtureGenerator(t)
	outlets := r.allOutlets()
	outletRoutes := make([]*outletRouteData, len(outlets))
	for i, outlet := range outlets {
		outletRoutes[i] = &outletRouteData{Name: outlet.name, Prefix: outlet.prefix, RoutesMap: make(map[string][]*generatedRoute)}
	}
	_, testRoutes, err := r.accumulateResourceRoutes(outlets, outletRoutes)
	if err != nil {
		t.Fatalf("accumulateResourceRoutes() error = %v", err)
	}

	served := func(routes []*generatedRoute) []string {
		var out []string
		for _, route := range routes {
			out = append(out, route.Method+" "+route.Path+" "+route.HandlerFunc)
		}

		return out
	}
	wantShipment := []string{
		"GET /api/tenants/{tenantID}/shipments Shipments",
		"GET /api/tenants/{tenantID}/shipments/{shipmentID} Shipment",
		"PATCH /api/tenants/{tenantID}/shipments PatchShipments",
		"GET /api/shipments ShipmentsAcrossTenants",
		"GET /api/tenants/{tenantID}/shipments/{shipmentID}/changes ShipmentChangeHistory",
	}
	if diff := cmp.Diff(wantShipment, served(outletRoutes[0].RoutesMap["Shipment"])); diff != "" {
		t.Errorf("Shipment's routes mismatch (-want +got):\n%s", diff)
	}
	for _, line := range served(outletRoutes[0].RoutesMap["Ledger"]) {
		if strings.HasSuffix(line, acrossTenantsSuffix) {
			t.Errorf("Ledger gained %s, and it does not declare the list", line)
		}
	}
	if len(outletRoutes[1].RoutesMap) != 0 {
		t.Errorf("the droids outlet serves %v, want nothing: neither resource is on it", outletRoutes[1].RoutesMap)
	}
	if !strings.Contains(strings.Join(served(testRoutes), "\n"), "GET /api/shipments ShipmentsAcrossTenants") {
		t.Errorf("the dispatch test's routes lack the list across tenants: %v", served(testRoutes))
	}

	negative, err := r.negativeTestsForOutlet(outlets[1])
	if err != nil {
		t.Fatalf("negativeTestsForOutlet() error = %v", err)
	}
	for _, want := range []negativeRouterTest{
		{Method: "http.MethodGet", URL: "/droids/shipments"},
		{Method: "http.MethodPost", URL: "/droids/shipments"},
	} {
		found := false
		for _, test := range negative {
			found = found || test == want
		}
		if !found {
			t.Errorf("the droids outlet's 404 cases lack %v", want)
		}
	}
}

// Test_validateAcrossTenantsRoutes pins the refusal of a list across tenants served
// where another route of the outlet answers: an RPC method named as the resource's
// plural takes the path's POST.
func Test_validateAcrossTenantsRoutes(t *testing.T) {
	t.Parallel()

	across := &generatedRoute{Method: "GET", Path: "/api/shipments", HandlerFunc: "ShipmentsAcrossTenants", HandlerType: acrossTenantsHandler}
	tests := []struct {
		name    string
		routes  map[string][]*generatedRoute
		wantErr string
	}{
		{
			name: "a method's route at another path leaves the list alone",
			routes: map[string][]*generatedRoute{
				"Shipment": {across},
				"Reroute":  {{Method: "POST", Path: "/api/reroute", HandlerFunc: "Reroute"}},
			},
		},
		{
			name: "a method named as the plural takes the list's POST",
			routes: map[string][]*generatedRoute{
				"Shipment":  {across},
				"Shipments": {{Method: "POST", Path: "/api/shipments", HandlerFunc: "Shipments"}},
			},
			wantErr: "@listAcrossTenants: the list across tenants ShipmentsAcrossTenants would be served at POST /api/shipments, where Shipments already answers on the default outlet; rename one of the two, or remove the annotation",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateAcrossTenantsRoutes([]*outletRouteData{{Name: defaultOutletName, RoutesMap: tt.routes}})
			switch {
			case tt.wantErr == "" && err != nil:
				t.Errorf("validateAcrossTenantsRoutes() error = %v, want none", err)
			case tt.wantErr != "" && (err == nil || !strings.HasSuffix(err.Error(), tt.wantErr)):
				t.Errorf("validateAcrossTenantsRoutes() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

// Test_acrossTenantsHandlerContent pins the handler of a list across tenants: it
// decodes over the set of tenants the session reaches, never the route's tenant, puts
// each row's tenant on the row, and registers no live subscription.
func Test_acrossTenantsHandlerContent(t *testing.T) {
	t.Parallel()

	r := acrossFixtureGenerator(t)
	out, err := r.acrossTenantsHandlerContent(r.resources[0])
	if err != nil {
		t.Fatalf("acrossTenantsHandlerContent() error = %v", err)
	}
	assertContains(t, "listAcrossTenantsTemplate", string(out), []string{
		"func (a *TestApp) ShipmentsAcrossTenants() http.HandlerFunc {",
		"querySet, err := decoder.DecodeAcrossTenants(r, a.UserPermissions(r))",
		"rmap[resource.TenantProperty] = row.Tenant()",
		"if err := page.Count(ctx, a.ResourceClient()); err != nil {",
		"if err := page.WriteHeaders(w, r); err != nil {",
		// Plain unless the page asks: the one subscription over the set of tenants,
		// registered before the query, and the refetch's answer cacheable through what
		// the registration answered, which says when the list is served plain.
		"liveness := live.SubscribeAcrossTenants(ctx, w, r, a.LiveService(), querySet)\n\n\t\tres := ",
		"liveness.SetCacheControl(w, r)",
	}, []string{
		// Never marked cacheable whatever the registration answered.
		"live.SetCacheControl(w, r)",
		"router.Domain",
		"accesstypes.DomainScope(",
		// Never one tenant's subscription, and never the record in every tenant.
		"live.Subscribe(",
		"live.ListSubscription(",
		"resource.CapabilitiesProperty",
	})
}

// Test_routesTemplate_acrossTenants pins the registration: the list across tenants
// answers GET and POST on its own handler, outside the tenant's guard its resource's
// other routes are wrapped in, and inside the feature guard when the resource is gated.
func Test_routesTemplate_acrossTenants(t *testing.T) {
	t.Parallel()

	freight := &featureGate{Constant: "Freight", Name: "freight"}
	out, err := (&client{}).generateTemplateOutput("routesTemplate", routesTemplate, &routerFileData{
		ServesSessions: true,
		Package:        "router",
		RoutesMap: map[string][]*generatedRoute{
			"Shipment": {
				{Method: "GET", Path: "/api/tenants/{tenantID}/shipments", HandlerFunc: "Shipments", HandlerType: ListHandler, DomainScoped: true},
				{Method: "GET", Path: "/api/shipments", HandlerFunc: "ShipmentsAcrossTenants", HandlerType: acrossTenantsHandler},
			},
			"Parcel": {
				{Method: "GET", Path: "/api/parcels", HandlerFunc: "ParcelsAcrossTenants", HandlerType: acrossTenantsHandler, Feature: freight},
			},
		},
		HasDomainScoped:       true,
		HasDomainScopedRoutes: true,
		HasGatedRoutes:        true,
		StubFeatureGuard:      true,
		DomainRouteParam:      "tenantID",
		ResourcePackage:       "resources",
	})
	if err != nil {
		t.Fatalf("generateTemplateOutput() error = %v", err)
	}
	assertContains(t, "routesTemplate", string(out), []string{
		"ShipmentsAcrossTenants() http.HandlerFunc",
		"shipmentsHandler := domainGuard(h.Shipments())",
		"shipmentsAcrossTenantsHandler := h.ShipmentsAcrossTenants()",
		`bounded.Get("/api/shipments", shipmentsAcrossTenantsHandler)`,
		`bounded.Post("/api/shipments", shipmentsAcrossTenantsHandler)`,
		"parcelsAcrossTenantsHandler := featureGuard(resources.Freight)(h.ParcelsAcrossTenants())",
	}, []string{
		"domainGuard(h.ShipmentsAcrossTenants())",
		"domainGuard(h.ParcelsAcrossTenants())",
	})
}

// Test_resourceAuthzCases_acrossTenants pins the matrix's case for a list across
// tenants and what the suite renders from it: no denied case, since the route refuses
// nobody at its gate, and the two answers the matrix can see.
func Test_resourceAuthzCases_acrossTenants(t *testing.T) {
	t.Parallel()

	r := acrossFixtureGenerator(t)
	cases, err := r.resourceAuthzCases()
	if err != nil {
		t.Fatalf("resourceAuthzCases() error = %v", err)
	}
	var across []authzCase
	for _, c := range cases {
		if c.AcrossTenants {
			across = append(across, c)
		}
	}
	want := []authzCase{{Name: "ShipmentsAcrossTenants", Method: "http.MethodGet", URL: "/api/shipments", Permission: "List", AcrossTenants: true, OutsideTenantGuard: true}}
	if diff := cmp.Diff(want, across, cmp.AllowUnexported(authzCase{})); diff != "" {
		t.Fatalf("the across-tenants cases mismatch (-want +got):\n%s", diff)
	}

	out, err := (&client{}).generateTemplateOutput("authzTestTemplate", authzTestTemplate, &authzTestData{Package: "authz", Cases: across})
	if err != nil {
		t.Fatalf("generateTemplateOutput() error = %v", err)
	}
	assertContains(t, "authzTestTemplate", string(out), []string{
		"name:         \"ShipmentsAcrossTenants reads no tenant without a grant\",\n\t\t\tmethod:       http.MethodGet,\n\t\t\ttarget:       \"/api/shipments\",\n\t\t\twantStatuses: []int{http.StatusOK},",
		"name:         \"ShipmentsAcrossTenants granted\",\n\t\t\tgrants:       grants{accesstypes.List: true},\n\t\t\tmethod:       http.MethodGet,\n\t\t\ttarget:       \"/api/shipments\",\n\t\t\twantStatuses: []int{http.StatusOK},",
	}, []string{"ShipmentsAcrossTenants denied"})
}

// Test_resourceFeatureTests_acrossTenants pins the gate test of a gated resource's
// list across tenants: the route is absent while the flag is off, like the resource's
// others.
func Test_resourceFeatureTests_acrossTenants(t *testing.T) {
	t.Parallel()

	r := acrossFixtureGenerator(t)
	shipment := r.resources[0]
	shipment.Feature = &featureGate{Constant: "Freight", Name: "freight"}

	data := &featureTestsData{}
	if err := r.resourceFeatureTests(data, shipment, r.allOutlets()[0]); err != nil {
		t.Fatalf("resourceFeatureTests() error = %v", err)
	}
	want := featureGateTest{Name: "ShipmentsAcrossTenants", Method: "http.MethodGet", URL: "/api/shipments", Constant: "Freight", Permission: "List"}
	found := false
	for _, test := range data.Routes {
		found = found || test == want
	}
	if !found {
		t.Errorf("the gate tests lack %+v: %+v", want, data.Routes)
	}
}

// Test_apiClientData_acrossTenants pins the browser client's side: the descriptor's
// word on a declared resource alone, and the AcrossTenantsApi interface the client is
// typed with, written only when a resource of the outlet declares the list.
func Test_apiClientData_acrossTenants(t *testing.T) {
	t.Parallel()

	typed := func(resources []*resourceInfo) []*resourceInfo {
		for _, res := range resources {
			for _, f := range res.Fields {
				f.typescriptType = "string"
			}
		}

		return resources
	}

	tests := []struct {
		name            string
		resources       []string
		wantContains    []string
		wantNotContains []string
	}{
		{
			name:      "a declared resource",
			resources: []string{"Shipment", "Ledger"},
			wantContains: []string{
				"import { AcrossTenantsHandle } from '@impulseframework/resource';",
				"      route: 'shipments',\n      scope: 'domain',",
				"      acrossTenants: true,\n    },\n    [Resources.Ledgers]: {",
				"export interface AcrossTenantsApi {\n  shipments: AcrossTenantsHandle<Shipments, ShipmentsKey>;\n}",
				"export type Api = Client<GlobalApi, DomainApi, AcrossTenantsApi>;",
				"return createClient<GlobalApi, DomainApi, AcrossTenantsApi>(apiDescriptor, options);",
			},
			wantNotContains: []string{"ledgers: AcrossTenantsHandle"},
		},
		{
			name:      "no declared resource",
			resources: []string{"Ledger"},
			wantContains: []string{
				"export type Api = Client<GlobalApi, DomainApi>;",
				"return createClient<GlobalApi, DomainApi>(apiDescriptor, options);",
			},
			wantNotContains: []string{"acrossTenants", "AcrossTenants"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, structs := acrossFixture(t)
			c.resources = typed(acrossFixtureTablesOf(t, c, structs, tt.resources...))
			g := &typescriptGenerator{client: c}
			out, err := g.generateTemplateOutput("typescriptAPITemplate", typescriptAPITemplate, g.apiClientData())
			if err != nil {
				t.Fatalf("generateTemplateOutput() error = %v", err)
			}
			assertContains(t, "typescriptAPITemplate", string(out), tt.wantContains, tt.wantNotContains)
		})
	}
}

// TestSchemaWarnings_acrossTenants pins the index check of a list across tenants:
// which declared resources warn, the values a warning carries, and that the check
// speaks of a declared resource alone.
func TestSchemaWarnings_acrossTenants(t *testing.T) {
	t.Parallel()

	c, structs := acrossFixture(t)
	tables := acrossFixtureTablesOf(t, c, structs, "Shipment", "Parcel", "Carton", "Pallet", "Drum", "Crate", "Seal", "Ledger")
	views, err := c.structsToVirtualResources([]*parser.Struct{structs["Board"]})
	if err != nil {
		t.Fatalf("structsToVirtualResources() error = %v", err)
	}

	across := make(map[string]AcrossTenantsIndexWarning)
	others := make(map[string][]Warning)
	for _, w := range c.schemaWarnings(append(tables, views...)) {
		switch w := w.(type) {
		case AcrossTenantsIndexWarning:
			across[w.Resource] = w
		case IndexWarning:
			others[w.Resource] = append(others[w.Resource], w)
		case JoinPathWarning:
			others[w.Resource] = append(others[w.Resource], w)
		default:
			t.Fatalf("unexpected warning kind %T", w)
		}
	}

	tests := []struct {
		name     string
		resource string
		want     AcrossTenantsIndexWarning
	}{
		{name: "an index in the list's order storing the tenant column is silent", resource: "Shipment"},
		{
			name:     "the tenant's own index alone warns, naming the index wanted",
			resource: "Parcel",
			want: AcrossTenantsIndexWarning{
				Resource: "Parcel", Table: "Parcels", TenantColumn: "TenantId",
				Order: []resource.SortField{{Field: "DueAt", Direction: resource.SortAscending}, {Field: "Label", Direction: resource.SortDescending}},
				Index: "CREATE INDEX ParcelsByDueAtLabel ON Parcels(DueAt, Label DESC) STORING (TenantId)",
			},
		},
		{
			name:     "an index in the list's order that does not carry the tenant column warns",
			resource: "Carton",
			want: AcrossTenantsIndexWarning{
				Resource: "Carton", Table: "Cartons", TenantColumn: "TenantId",
				Order: []resource.SortField{{Field: "DueAt", Direction: resource.SortAscending}},
				Index: "CREATE INDEX CartonsByDueAt ON Cartons(DueAt) STORING (TenantId)",
			},
		},
		{name: "the tenant column later in the index's key is silent", resource: "Pallet"},
		{name: "the tenant column in the primary key rides every index and is silent", resource: "Drum"},
		{name: "no declared order lists by primary key and is silent", resource: "Crate"},
		{name: "a join path carries its own warning and none of this kind", resource: "Seal"},
		{name: "an undeclared resource is silent", resource: "Ledger"},
		{name: "a view is silent", resource: "Board"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.want, across[tt.resource]); diff != "" {
				t.Errorf("the across-tenants warning for %s mismatch (-want +got):\n%s", tt.resource, diff)
			}
		})
	}

	// The tenant's own list is judged as before: Parcel's and Ledger's index serves
	// it, Shipment's does not, and Seal carries the join-path warning.
	if len(others["Parcel"]) != 0 || len(others["Ledger"]) != 0 {
		t.Errorf("the tenant's own list of Parcel and Ledger is served, and they warn: %v %v", others["Parcel"], others["Ledger"])
	}
	if len(others["Shipment"]) != 1 {
		t.Errorf("Shipment has no index leading with its tenant column, and raised %d warnings for the tenant's own list, want 1", len(others["Shipment"]))
	}
	if len(others["Seal"]) != 1 {
		t.Errorf("Seal resolves its tenant through a join path, and raised %d warnings, want 1", len(others["Seal"]))
	}
}

// TestAcrossTenantsIndexWarning_String pins the one-line text a runner prints.
func TestAcrossTenantsIndexWarning_String(t *testing.T) {
	t.Parallel()

	w := AcrossTenantsIndexWarning{
		Resource: "Mission", Table: "Missions", TenantColumn: "SectorId",
		Order: []resource.SortField{{Field: "StartsAt", Direction: resource.SortDescending}},
		Index: "CREATE INDEX MissionsByStartsAt ON Missions(StartsAt DESC) STORING (SectorId)",
	}
	want := "Mission is listed across tenants in StartsAt DESC order with no index leading with those columns and carrying SectorId, so every page reads and sorts the rows of every tenant the person reaches; wanted: CREATE INDEX MissionsByStartsAt ON Missions(StartsAt DESC) STORING (SectorId)"
	if got := w.String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

// Test_collectAcrossTenantsRegistrations pins the declaration the running application
// reads: the collection marks the resources declared for listing across tenants, and no
// other.
func Test_collectAcrossTenantsRegistrations(t *testing.T) {
	t.Parallel()

	r := acrossFixtureGenerator(t)
	b := resource.NewCollectionBuilder()
	for _, res := range r.resources {
		if err := b.AddResource(scopeOrGlobal(res.PermissionScope), accesstypes.List, accesstypes.Resource(r.pluralize(res.Name()))); err != nil {
			t.Fatalf("AddResource(%s) error = %v", res.Name(), err)
		}
	}
	r.collectAcrossTenantsRegistrations(b)

	want := make(map[accesstypes.Resource]bool, len(r.resources))
	for _, res := range r.resources {
		want[accesstypes.Resource(r.pluralize(res.Name()))] = res.ListsAcrossTenants
	}
	if !want["Shipments"] || want["Ledgers"] {
		t.Fatalf("the fixture declares %v, want Shipments declared and Ledgers not", want)
	}
	got := make(map[accesstypes.Resource]bool, len(r.resources))
	for _, res := range b.Data().Resources {
		got[res.Name] = res.ListsAcrossTenants
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("the collection's marks mismatch (-want +got):\n%s", diff)
	}
	if !r.listsAcrossTenants() {
		t.Error("listsAcrossTenants() = false, want true: the fixture declares Shipments")
	}
	none := &resourceGenerator{client: &client{}}
	none.resources = []*resourceInfo{r.resources[1]}
	if none.listsAcrossTenants() {
		t.Error("listsAcrossTenants() = true for an application that declares none")
	}
}

// Test_declarationTemplates_acrossTenants pins where the generated code hands the
// declaration on: the collection's data carries the mark, and the digest handler and
// the live renewal are given the collection, in an application that declares a list
// across tenants. An application that declares none generates what it always has.
func Test_declarationTemplates_acrossTenants(t *testing.T) {
	t.Parallel()

	render := func(t *testing.T, name, text string, data any) string {
		t.Helper()

		out, err := (&resourceGenerator{client: &client{}}).generateTemplateOutput(name, text, data)
		if err != nil {
			t.Fatalf("generateTemplateOutput(%s) error = %v", name, err)
		}

		return string(out)
	}
	wrapper := func(declares bool) *permissionsData {
		return &permissionsData{
			Package: "app", ApplicationName: "App", ReceiverName: "a", RoutePrefix: "api", DefaultServesSessions: true,
			LocalPackageImports: `"example.com/app/router"`, ResourcePackage: "resources", RouterPackage: "router",
			ListsAcrossTenants: declares,
		}
	}

	t.Run("the collection's data marks a declared resource", func(t *testing.T) {
		t.Parallel()

		out := render(t, "collectionTemplate", collectionTemplate, &collectionFileData{
			Source:  "pkg/resources",
			Package: "router",
			Data: resource.CollectionData{Resources: []resource.CollectionResource{
				{Name: "Ledgers", Scope: accesstypes.DomainPermissionScope, Permissions: []accesstypes.Permission{accesstypes.List}},
				{Name: "Shipments", Scope: accesstypes.DomainPermissionScope, Permissions: []accesstypes.Permission{accesstypes.List}, ListsAcrossTenants: true},
			}},
		})
		if got := strings.Count(out, "ListsAcrossTenants: true,"); got != 1 {
			t.Errorf("the collection file marks %d resources, want Shipments alone:\n%s", got, out)
		}
		_, shipments, found := strings.Cut(out, `Name:  "Shipments"`)
		if !found || !strings.Contains(shipments, "ListsAcrossTenants: true,") {
			t.Errorf("the mark is not on Shipments:\n%s", out)
		}
	})

	t.Run("the digest handler is given the collection that says which resources are declared", func(t *testing.T) {
		t.Parallel()

		assertContains(t, "permissionsTemplate", render(t, "permissionsTemplate", permissionsTemplate, wrapper(true)), []string{
			"resource.WithFormerNames(router.Collection()), resource.WithListsAcrossTenants(router.Collection()))",
			"whether the person may list it in at least one tenant the session reaches",
		}, nil)
		assertContains(t, "permissionsTemplate", render(t, "permissionsTemplate", permissionsTemplate, wrapper(false)), []string{
			"resource.WithFormerNames(router.Collection()))\n",
		}, []string{"WithListsAcrossTenants", "listed across tenants"})
	})

	t.Run("the live renewal is given it too", func(t *testing.T) {
		t.Parallel()

		assertContains(t, "liveTemplate", render(t, "liveTemplate", liveTemplate, wrapper(true)), []string{
			"return live.RenewHandler(a.LiveService(), a.UserPermissions, live.WithListsAcrossTenants(router.Collection()))",
			`"example.com/app/router"`,
			"A list across tenants is kept for a resource declared for it",
		}, nil)
		assertContains(t, "liveTemplate", render(t, "liveTemplate", liveTemplate, wrapper(false)), []string{
			"return live.RenewHandler(a.LiveService(), a.UserPermissions)\n",
		}, []string{"WithListsAcrossTenants", "example.com/app/router", "A list across tenants"})
	})
}
