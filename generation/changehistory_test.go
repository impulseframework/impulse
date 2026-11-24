package generation

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/resource"
)

// historyRoutes answers the change history routes among routes, as METHOD path handler.
func historyRoutes(routes []*generatedRoute) []string {
	var out []string
	for _, route := range routes {
		if route.HandlerType == changeHistoryHandler {
			out = append(out, route.Method+" "+route.Path+" "+route.HandlerFunc)
		}
	}

	return out
}

// Test_resourceChangeHistoryRoutes pins which resources serve a record's change history
// and where: a table-backed resource with a read route, under that route with its
// parameters, on every outlet the resource is on; no view, no resource whose read is
// suppressed and no computed resource.
func Test_resourceChangeHistoryRoutes(t *testing.T) {
	t.Parallel()

	c, structs := acrossFixture(t)
	c.resources = acrossFixtureTablesOf(t, c, structs, "Shipment", "Drum", "Ledger", "Crate")
	shipment, drum, ledger, crate := c.resources[0], c.resources[1], c.resources[2], c.resources[3]
	// Shipment is served on both outlets; Ledger is a view with a key, which has a read
	// route; Crate suppresses its read.
	shipment.OutletNames = []string{defaultOutletName, "droids"}
	ledger.IsVirtual = true
	crate.SuppressedHandlers = []HandlerType{ReadHandler}
	r := &resourceGenerator{
		client:             c,
		genRoutes:          true,
		routePrefix:        "api",
		domainRouteSegment: "tenants",
		domainRouteParam:   "tenantID",
		applicationName:    "TestApp",
		receiverName:       "a",
		extraOutlets:       []routerOutlet{{name: "droids", prefix: "droids"}},
	}

	t.Run("under the read route, with its parameters and its tenant's guard", func(t *testing.T) {
		t.Parallel()

		for _, res := range []*resourceInfo{shipment, drum} {
			read, err := r.resourceRoute(res, ReadHandler, "api")
			if err != nil {
				t.Fatalf("resourceRoute(%s) error = %v", res.Name(), err)
			}
			routes, err := r.resourceChangeHistoryRoutes(res, "api")
			if err != nil {
				t.Fatalf("resourceChangeHistoryRoutes(%s) error = %v", res.Name(), err)
			}
			want := []*generatedRoute{{
				Method:       "GET",
				Path:         read.Path + "/" + resource.ChangeHistoryRoute,
				HandlerFunc:  res.Name() + "ChangeHistory",
				HandlerType:  changeHistoryHandler,
				DomainScoped: true,
				TestURL:      read.TestURL + "/" + resource.ChangeHistoryRoute,
				TestParams:   read.TestParams,
				Source:       res.Name(),
			}}
			if diff := cmp.Diff(want, routes, cmp.AllowUnexported(generatedRoute{})); diff != "" {
				t.Errorf("resourceChangeHistoryRoutes(%s) mismatch (-want +got):\n%s", res.Name(), diff)
			}
			if diff := cmp.Diff([]string{"http.MethodGet"}, routes[0].TestMethods()); diff != "" {
				t.Errorf("TestMethods() mismatch (-want +got):\n%s", diff)
			}
		}
		paths := map[string]string{}
		for _, res := range []*resourceInfo{shipment, drum} {
			routes, _ := r.resourceChangeHistoryRoutes(res, "api")
			paths[res.Name()] = routes[0].Path
		}
		if diff := cmp.Diff(map[string]string{
			"Shipment": "/api/tenants/{tenantID}/shipments/{shipmentID}/changes",
			"Drum":     "/api/tenants/{tenantID}/drums/{drumTenantID}/{drumSequence}/changes",
		}, paths); diff != "" {
			t.Errorf("the history routes' paths mismatch (-want +got):\n%s", diff)
		}
	})
	t.Run("on every outlet the resource is on, and for no view or suppressed read", func(t *testing.T) {
		t.Parallel()

		outlets := r.allOutlets()
		outletRoutes := make([]*outletRouteData, len(outlets))
		for i, outlet := range outlets {
			outletRoutes[i] = &outletRouteData{Name: outlet.name, Prefix: outlet.prefix, RoutesMap: make(map[string][]*generatedRoute)}
		}
		_, testRoutes, err := r.accumulateResourceRoutes(outlets, outletRoutes)
		if err != nil {
			t.Fatalf("accumulateResourceRoutes() error = %v", err)
		}
		want := map[string][]string{
			"default Shipment": {"GET /api/tenants/{tenantID}/shipments/{shipmentID}/changes ShipmentChangeHistory"},
			"droids Shipment":  {"GET /droids/tenants/{tenantID}/shipments/{shipmentID}/changes ShipmentChangeHistory"},
			"default Drum":     {"GET /api/tenants/{tenantID}/drums/{drumTenantID}/{drumSequence}/changes DrumChangeHistory"},
		}
		got := map[string][]string{}
		for _, outlet := range outletRoutes {
			for name, routes := range outlet.RoutesMap {
				if served := historyRoutes(routes); len(served) > 0 {
					got[outlet.Name+" "+name] = served
				}
			}
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("the history routes mismatch (-want +got):\n%s", diff)
		}
		if read, _ := r.resourceRoute(ledger, ReadHandler, "api"); !strings.Contains(strings.Join(func() []string {
			var paths []string
			for _, route := range outletRoutes[0].RoutesMap["Ledger"] {
				paths = append(paths, route.Path)
			}

			return paths
		}(), " "), read.Path) {
			t.Errorf("the view Ledger has no read route %s: the case proves nothing", read.Path)
		}
		if len(historyRoutes(testRoutes)) != 3 {
			t.Errorf("the dispatch test's routes hold %v, want the three history routes", historyRoutes(testRoutes))
		}
	})
	t.Run("no computed resource", func(t *testing.T) {
		t.Parallel()

		computed := fixtureComputedResource(t, fixtureStructs(loadCollectionFixture(t)), "Summary")
		g := &resourceGenerator{client: &client{computedResources: []*computedResource{computed}}, genRoutes: true, routePrefix: "api"}
		outlets := g.allOutlets()
		outletRoutes := []*outletRouteData{{Name: outlets[0].name, Prefix: outlets[0].prefix, RoutesMap: make(map[string][]*generatedRoute)}}
		_, routes, err := g.accumulateComputedRoutes(outlets[:1], outletRoutes)
		if err != nil {
			t.Fatalf("accumulateComputedRoutes() error = %v", err)
		}
		if len(routes) == 0 {
			t.Fatal("the computed resource has no route: the case proves nothing")
		}
		if served := historyRoutes(routes); len(served) != 0 {
			t.Errorf("the computed resource serves %v, want no change history", served)
		}
	})
	t.Run("the authorization matrix asks the record's Read grant", func(t *testing.T) {
		t.Parallel()

		routes, _ := r.resourceChangeHistoryRoutes(shipment, "api")
		c, ok, err := queryRouteCase(routes[0], resourcePKTypes(shipment), "")
		if err != nil || !ok {
			t.Fatalf("queryRouteCase() = %v, %v, want a case", ok, err)
		}
		if c.Permission != "Read" || c.DeniedOnly || !strings.HasSuffix(c.URL, "/changes") || strings.Contains(c.URL, "{") {
			t.Errorf("queryRouteCase() = %+v, want a Read case at the history route with its key filled", c)
		}
		cases, err := r.resourceAuthzCases()
		if err != nil {
			t.Fatalf("resourceAuthzCases() error = %v", err)
		}
		var names []string
		for _, c := range cases {
			if strings.HasSuffix(strings.Fields(c.Name)[0], changeHistorySuffix) {
				names = append(names, c.Name)
			}
		}
		if diff := cmp.Diff([]string{"ShipmentChangeHistory", "ShipmentChangeHistory (droids)", "DrumChangeHistory"}, names); diff != "" {
			t.Errorf("the matrix's history cases mismatch (-want +got):\n%s", diff)
		}
	})
}

// Test_concealDeniedStatuses pins what an application that conceals its tenants expects
// of a caller with no grant: 404 from a route the tenant guard wraps, 400 from a
// consolidated operation below a tenant, and the plain refusal (403) from a route the
// guard does not wrap, the tenant record's history among them, though its path goes on
// below the tenant's key.
func Test_concealDeniedStatuses(t *testing.T) {
	t.Parallel()

	c, structs := acrossFixture(t)
	shipment := acrossFixtureTablesOf(t, c, structs, "Shipment")[0]
	r := &resourceGenerator{client: c, genRoutes: true, routePrefix: "api", domainRouteSegment: "tenants", domainRouteParam: "tenantID"}
	routes, err := r.resourceChangeHistoryRoutes(shipment, "api")
	if err != nil {
		t.Fatalf("resourceChangeHistoryRoutes() error = %v", err)
	}
	guarded, _, err := queryRouteCase(routes[0], resourcePKTypes(shipment), "")
	if err != nil {
		t.Fatalf("queryRouteCase(guarded) error = %v", err)
	}
	tenantHistory := &generatedRoute{
		Method:      "GET",
		Path:        "/api/tenants/{tenantID}/changes",
		HandlerFunc: "TenantChangeHistory",
		HandlerType: changeHistoryHandler,
		TestParams:  []routeTestParam{{Key: "tenantID"}},
	}
	tenant, _, err := queryRouteCase(tenantHistory, []pkParamType{{declared: "impulse.UUID"}}, "")
	if err != nil {
		t.Fatalf("queryRouteCase(tenant) error = %v", err)
	}
	operation := authzCase{Name: "ShipmentsPatch", Body: `[{"op":"add","path":"/tenants/00000000-0000-0000-0000-000000000001/shipments"}]`}

	cases := []authzCase{guarded, tenant, operation}
	r.concealDeniedStatuses(cases)
	got := map[string]string{}
	for _, c := range cases {
		got[c.Name] = c.DeniedStatus
	}
	if diff := cmp.Diff(map[string]string{
		"ShipmentChangeHistory": "http.StatusNotFound",
		"TenantChangeHistory":   "",
		"ShipmentsPatch":        "http.StatusBadRequest",
	}, got); diff != "" {
		t.Errorf("concealDeniedStatuses() denied statuses mismatch (-want +got):\n%s", diff)
	}
	if tenant.URL != "/api/tenants/00000000-0000-0000-0000-000000000001/changes" {
		t.Errorf("the tenant record's history case asks %s: the case proves nothing", tenant.URL)
	}
}

// Test_resourceFeatureTests_changeHistory pins the history route of a resource behind a
// flag: it takes the read route's flag, so it is not served while the flag is off, and the
// generated feature gate tests drive it in both states of the flag.
func Test_resourceFeatureTests_changeHistory(t *testing.T) {
	t.Parallel()

	r := acrossFixtureGenerator(t)
	shipment := r.resources[0]
	gate := &featureGate{Constant: "Freight", Name: "freight"}
	shipment.Feature = gate

	routes, err := r.resourceChangeHistoryRoutes(shipment, "api")
	if err != nil {
		t.Fatalf("resourceChangeHistoryRoutes() error = %v", err)
	}
	if len(routes) != 1 || routes[0].Feature != gate {
		t.Fatalf("resourceChangeHistoryRoutes() = %+v, want one route behind the shipment's flag", routes)
	}
	read, err := r.resourceRoute(shipment, ReadHandler, "api")
	if err != nil {
		t.Fatalf("resourceRoute() error = %v", err)
	}
	// The read's own gate case, whose address holds the key's placeholder value.
	readCase, err := gateTestOf(read, resourcePKTypes(shipment), "", "Freight", r.allOutlets()[0])
	if err != nil {
		t.Fatalf("gateTestOf() error = %v", err)
	}

	data := &featureTestsData{}
	if err := r.resourceFeatureTests(data, shipment, r.allOutlets()[0]); err != nil {
		t.Fatalf("resourceFeatureTests() error = %v", err)
	}
	want := featureGateTest{Name: "ShipmentChangeHistory", Method: "http.MethodGet", URL: readCase.URL + "/" + resource.ChangeHistoryRoute, Constant: "Freight", Permission: "Read"}
	found := false
	for _, test := range data.Routes {
		found = found || test == want
	}
	if !found {
		t.Errorf("the gate tests lack %+v: %+v", want, data.Routes)
	}
}

// Test_validateChangeHistoryRoutes pins the two refusals of a record's change history:
// served where another route of the outlet answers, and named as another handler is.
func Test_validateChangeHistoryRoutes(t *testing.T) {
	t.Parallel()

	history := &generatedRoute{Method: "GET", Path: "/api/shipments/{shipmentID}/changes", HandlerFunc: "ShipmentChangeHistory", HandlerType: changeHistoryHandler, Source: "Shipment"}
	tests := []struct {
		name    string
		routes  map[string][]*generatedRoute
		wantErr string
	}{
		{
			name: "beside the resource's other routes",
			routes: map[string][]*generatedRoute{
				"Shipment": {
					{Method: "GET", Path: "/api/shipments/{shipmentID}", HandlerFunc: "Shipment", HandlerType: ReadHandler},
					{Method: "GET", Path: "/api/shipments/{shipmentID}/content", HandlerFunc: "ShipmentContent", HandlerType: fileHandler},
					history,
				},
			},
		},
		{
			name: "a stored file's segment named changes",
			routes: map[string][]*generatedRoute{
				"Shipment": {
					{Method: "GET", Path: "/api/shipments/{shipmentID}/changes", HandlerFunc: "ShipmentChanges", HandlerType: fileHandler},
					history,
				},
			},
			wantErr: "struct Shipment: the record's change history would be served at GET /api/shipments/{shipmentID}/changes, where ShipmentChanges already answers on the default outlet; GET <read route>/changes is the history of every table-backed resource, so name that route's segment something else",
		},
		{
			name: "a resource named as the history's handler",
			routes: map[string][]*generatedRoute{
				"Shipment":              {history},
				"ShipmentChangeHistory": {{Method: "GET", Path: "/api/shipment-change-histories/{shipmentChangeHistoryID}", HandlerFunc: "ShipmentChangeHistory", HandlerType: ReadHandler}},
			},
			wantErr: "struct Shipment: the handler of the record's change history would be named ShipmentChangeHistory, which a handler of ShipmentChangeHistory already is on the default outlet; rename one of the two structs",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateChangeHistoryRoutes([]*outletRouteData{{Name: defaultOutletName, RoutesMap: tt.routes}})
			switch {
			case tt.wantErr == "" && err != nil:
				t.Errorf("validateChangeHistoryRoutes() error = %v, want none", err)
			case tt.wantErr != "" && (err == nil || !strings.HasSuffix(err.Error(), tt.wantErr)):
				t.Errorf("validateChangeHistoryRoutes() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

// TestChangeHistoryHandlerContent pins the handler of a record's change history: the
// read's request type and decoder, the record named by the read route's key in the
// request's tenant, every field the read never returns named as such (a write-only
// field, a stored file's key and the secrets, in the order of the resource's fields, and
// no other; none for a resource with none of them), and no live read.
func TestChangeHistoryHandlerContent(t *testing.T) {
	t.Parallel()

	collapse := func(s string) string {
		return strings.Join(strings.Fields(s), " ")
	}
	r := acrossFixtureGenerator(t)
	c, structs := acrossFixture(t)
	drum := acrossFixtureTablesOf(t, c, structs, "Drum")[0]
	// Lockbox holds two secrets and a write-only field; its Name stands in for a stored
	// file's key column, which the read never returns either.
	box := lockbox(t, fixtureStructs(loadFixture(t, "secretfixture")), func(res *resourceInfo) {
		secretField(t, res, "Name").IsFileKey = true
	})

	tests := []struct {
		name    string
		res     *resourceInfo
		want    []string
		notWant []string
	}{
		{
			name: "a global resource with fields the read never returns",
			res:  box,
			want: []string{
				"// LockboxChangeHistory answers a page of one lockbox's change records, newest first, to whoever may read the record, with the fields they may see on it. It answers 404 while the resource does not track its changes.",
				"func (a *TestApp) LockboxChangeHistory() http.HandlerFunc {",
				".Lockbox, response](a, accesstypes.Read)",
				"id := httpio.Param[impulse.UUID](r, router.LockboxID)",
				`history, err := decoder.DecodeChangeHistory(r, a.UserPermissions(r), accesstypes.GlobalScope(), "Name", "Combination", "Hint", "Manifest")`,
				"NewLockboxQueryFromQuerySet(history.QuerySet()).SetID(id)",
				"page, err := history.Read(ctx, a.ResourceClient())",
				"return httpio.NewEncoder(w).Ok(page)",
			},
			notWant: []string{`"ID"`, "live.", "router.Domain"},
		},
		{
			name: "a tenant's resource with a key of two columns",
			res:  drum,
			want: []string{
				"tenantID := httpio.Param[string](r, router.DrumTenantID)",
				"sequence := httpio.Param[int64](r, router.DrumSequence)",
				domainParamLine,
				"history, err := decoder.DecodeChangeHistory(r, a.UserPermissions(r), accesstypes.DomainScope(domain))",
				"NewDrumQueryFromQuerySet(history.QuerySet()).SetTenantID(tenantID).SetSequence(sequence)",
			},
			notWant: []string{"live."},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out, err := r.changeHistoryHandlerContent(tt.res)
			if err != nil {
				t.Fatalf("changeHistoryHandlerContent() error = %v", err)
			}
			got := collapse(string(out))
			for _, want := range tt.want {
				if !strings.Contains(got, collapse(want)) {
					t.Errorf("the handler lacks %q:\n%s", want, out)
				}
			}
			for _, notWant := range tt.notWant {
				if strings.Contains(got, notWant) {
					t.Errorf("the handler holds %q:\n%s", notWant, out)
				}
			}
		})
	}
}
