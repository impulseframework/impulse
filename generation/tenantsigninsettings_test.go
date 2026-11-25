package generation

import (
	"go/format"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// Test_signInSettingsMethods pins where the read of a tenant's sign-in settings is served
// and the grants it is asked under. It is on every session outlet that serves one of the
// tenant's own sign-in methods, first among the outlet's routes of them, and that outlet
// names exactly the methods it serves, in the order of the one that takes the directory,
// the switch's, the default role's and the word for the second factor's: for a record
// that declares the switch beside a directories record with neither column, a directories
// record with the default seat alone, one with the second-factor column alone, and all
// three. The application's one handler is asked under every method an outlet serves. A
// tenant record with no switch beside a directories record that carries neither column
// is read under the method that takes the directory alone; one with no directories record
// and no switch has no read. The matrix refuses the read on each outlet it is on.
func Test_signInSettingsMethods(t *testing.T) {
	t.Parallel()

	const staffAuth = "example.com/app/pkg/auth/staff"
	// staff is the generated management of the Staff auth, whose people are listed in a
	// tenant and whose roles are given by hand, written for a directories record that
	// carries the default seat.
	staff := func(tenant string) []*identityInstance {
		return []*identityInstance{{
			importPath: staffAuth, auth: "Staff",
			tenant: &identityTenant{name: tenant, table: tenant + "s", key: "Id", word: strings.ToLower(tenant), directorySeat: "DefaultSeat"},
			tables: map[identityTable]bool{identityUsers: true, identityMemberships: true},
		}}
	}
	harborTake := []string{"TakeHarborDirectory"}
	harborSwitch := []string{"MakeHarborDirectoryOnly", "EndHarborDirectoryOnly"}
	harborRole := []string{"OpenHarborToDirectory", "MakeHarborInviteOnly"}
	harborFactor := []string{"VouchForHarborDirectorySecondFactor", "StopVouchingForHarborDirectorySecondFactor"}
	harborAll := slices.Concat(harborTake, harborSwitch, harborRole, harborFactor)
	sectorTake := []string{"TakeSectorDirectory"}
	sectorRole := slices.Concat(sectorTake, []string{"OpenSectorToDirectory", "MakeSectorInviteOnly"})
	sectorFactor := slices.Concat(sectorTake, []string{"VouchForSectorDirectorySecondFactor", "StopVouchingForSectorDirectorySecondFactor"})
	tests := []struct {
		name    string
		structs []string
		// directoriesOn are the outlets the directories record is served on, every
		// outlet where it is empty.
		directoriesOn []string
		// want are the methods each outlet serves, by outlet name, and wantGrants the
		// ones the handler is asked under.
		want       map[string][]string
		wantGrants []string
	}{
		{
			name:       "a record that declares the switch, beside a directories record with neither column",
			structs:    []string{"Harbor", "Breakwater"},
			want:       map[string][]string{defaultOutletName: slices.Concat(harborTake, harborSwitch), "members": slices.Concat(harborTake, harborSwitch)},
			wantGrants: slices.Concat(harborTake, harborSwitch),
		},
		{
			name:       "a record with no switch, beside a directories record with the default seat alone",
			structs:    []string{"Sector", "Mole"},
			want:       map[string][]string{defaultOutletName: sectorRole, "members": sectorRole},
			wantGrants: sectorRole,
		},
		{
			name:       "a record with no switch, beside a directories record with the second-factor column alone",
			structs:    []string{"Sector", "Landing"},
			want:       map[string][]string{defaultOutletName: sectorFactor, "members": sectorFactor},
			wantGrants: sectorFactor,
		},
		{
			name:       "a record that declares the switch, beside a directories record with both columns",
			structs:    []string{"Harbor", "HarborDirectory"},
			want:       map[string][]string{defaultOutletName: harborAll, "members": harborAll},
			wantGrants: harborAll,
		},
		{
			name:          "an outlet that serves the tenant record and not its directories record names the switch's methods alone",
			structs:       []string{"Harbor", "HarborDirectory"},
			directoriesOn: []string{defaultOutletName},
			want:          map[string][]string{defaultOutletName: harborAll, "members": harborSwitch},
			wantGrants:    harborAll,
		},
		{
			name:       "a record with no switch, beside a directories record with neither column, is read under the method that takes the directory alone",
			structs:    []string{"Sector", "Breakwater"},
			want:       map[string][]string{defaultOutletName: sectorTake, "members": sectorTake},
			wantGrants: sectorTake,
		},
		{
			name:       "a record with no switch and no directories record has no read",
			structs:    []string{"Sector"},
			want:       map[string][]string{},
			wantGrants: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r, err := declarationResources(t, tt.structs...)
			if err != nil {
				t.Fatalf("declarationResources() error = %v", err)
			}
			r.genRoutes = true
			r.defaultOutlet = routerOutlet{name: defaultOutletName, prefix: "api", servesSessions: true, auth: &outletAuth{importPath: staffAuth}}
			r.extraOutlets = []routerOutlet{
				{name: "members", prefix: "members", servesSessions: true, auth: &outletAuth{importPath: staffAuth}},
				{name: "machines", prefix: "machines", apiKey: true},
			}
			for _, res := range r.resources {
				res.OutletNames = []string{defaultOutletName, "members", "machines"}
				if len(tt.directoriesOn) > 0 && res.Name() == tt.structs[1] {
					res.OutletNames = tt.directoriesOn
				}
			}
			r.identity = staff(tt.structs[0])

			outlets := r.allOutlets()
			outletRoutes := make([]*outletRouteData, len(outlets))
			for i, outlet := range outlets {
				outletRoutes[i] = &outletRouteData{Name: outlet.name, RoutesMap: map[string][]*generatedRoute{}}
			}
			r.accumulateDirectoryOnlyRoute(outlets, outletRoutes)

			got := map[string][]string{}
			var wantDenied []string
			for i, outlet := range outlets {
				var names []string
				for _, method := range r.signInSettingsMethods(outlet.name) {
					names = append(names, method.Name)
				}
				routes := outletRoutes[i].RoutesMap[directoryOnlyRoutesKey]
				if len(names) == 0 {
					if len(routes) > 0 {
						t.Errorf("the outlet %s serves no method and registers %d routes of the tenant's own sign-in settings", outlet.name, len(routes))
					}

					continue
				}
				got[outlet.name] = names
				// The read comes first among the outlet's routes, under the tenant's own
				// route, and the methods the outlet serves follow it.
				path := "/" + outlet.prefix + "/" + r.domainRouteSegment + "/{" + r.domainRouteParam + "}/sign-in-settings"
				if len(routes) == 0 || routes[0].Method != "GET" || routes[0].Path != path || routes[0].HandlerFunc != tenantSignInSettingsHandlerFunc || !routes[0].DomainScoped {
					t.Errorf("the outlet %s registers %v first, want the read GET %s served by %s", outlet.name, routes, path, tenantSignInSettingsHandlerFunc)

					continue
				}
				var served []string
				for _, route := range routes[1:] {
					served = append(served, route.HandlerFunc)
				}
				if diff := cmp.Diff(names, served); diff != "" {
					t.Errorf("the outlet %s registers methods other than the ones the read names (-names +registered):\n%s", outlet.name, diff)
				}
				wantDenied = append(wantDenied, "GET "+routes[0].TestURL)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("signInSettingsMethods() mismatch (-want +got):\n%s", diff)
			}

			var grants []string
			for _, method := range r.signInSettingsGrants() {
				grants = append(grants, method.Name)
			}
			if diff := cmp.Diff(tt.wantGrants, grants); diff != "" {
				t.Errorf("signInSettingsGrants() mismatch (-want +got):\n%s", diff)
			}
			if len(grants) == 0 {
				// A run with no grant to ask the read under writes no file of it: the
				// generator returns before it reaches the handler package.
				if err := r.generateTenantSignInSettings(); err != nil {
					t.Errorf("generateTenantSignInSettings() error = %v, want none and no file", err)
				}
			}

			var denied []string
			for _, c := range r.directoryOnlyAuthzCases() {
				if c.Method == "http.MethodGet" {
					denied = append(denied, "GET "+c.URL)
				}
			}
			if diff := cmp.Diff(wantDenied, denied); diff != "" {
				t.Errorf("the matrix's refusals of the read mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Test_tenantSignInSettingsTemplate pins the handler of the read of a tenant's sign-in
// settings: the library's, given the rules the tenant's own methods are given and asked
// under the grant of each method the application serves, in the order given, and no
// other.
func Test_tenantSignInSettingsTemplate(t *testing.T) {
	t.Parallel()

	const rules = "NewHarborDirectoryRules().NamedBy(a.StaffIdentity().DirectoryName).PasswordsBy(a.StaffIdentity().CallerPassword)"
	data := func(methods ...string) *tenantSignInSettingsData {
		d := &tenantSignInSettingsData{
			Source: "resources", Package: "app", Name: "Harbor", Word: "harbor",
			Func: tenantSignInSettingsHandlerFunc, Route: "sign-in-settings", DomainRoute: "harbors/{harborID}", Rules: rules,
			LocalPackageImports: `"example.com/app/pkg/router"`, RouterPackage: "router", ApplicationName: "App", ReceiverName: "a",
		}
		for _, method := range methods {
			d.Methods = append(d.Methods, tenantOwnMethod{Name: method})
		}

		return d
	}
	tests := []struct {
		name            string
		data            *tenantSignInSettingsData
		wantContains    []string
		wantNotContains []string
	}{
		{
			name: "a record that declares the switch and a directories record with the default seat",
			data: data("MakeHarborDirectoryOnly", "EndHarborDirectoryOnly", "OpenHarborToDirectory", "MakeHarborInviteOnly"),
			wantContains: []string{
				"// TenantSignInSettings serves a harbor's sign-in settings as they stand, GET\n// <prefix>/harbors/{harborID}/sign-in-settings: what the harbor's Sign-in settings page shows a person\n",
				"who does not read the global Harbor record",
				"own sign-in methods the application serves, Execute in\n// the harbor, and writes nothing (resource.TenantSignInSettingsHandler)",
				"The application\n// has this one handler of the read for every outlet that serves one of those methods, so a\n// person who holds any of them is answered on each of those outlets.",
				"func (a *App) TenantSignInSettings() http.HandlerFunc {",
				"return resource.TenantSignInSettingsHandler(a.ResourceClient(), " + rules +
					`, a.UserPermissions, router.Domain, "MakeHarborDirectoryOnly", "EndHarborDirectoryOnly", "OpenHarborToDirectory", "MakeHarborInviteOnly")` + "\n",
			},
		},
		{
			name: "a record with no switch names the methods of the second factor alone",
			data: data("VouchForHarborDirectorySecondFactor", "StopVouchingForHarborDirectorySecondFactor"),
			wantContains: []string{
				`router.Domain, "VouchForHarborDirectorySecondFactor", "StopVouchingForHarborDirectorySecondFactor")` + "\n",
			},
			wantNotContains: []string{"DirectoryOnly\"", "ToDirectory\""},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &client{}
			out, err := c.generateTemplateOutput("tenantSignInSettingsTemplate", tenantSignInSettingsTemplate, tt.data)
			if err != nil {
				t.Fatalf("generateTemplateOutput() error = %v", err)
			}
			formatted, err := format.Source(out)
			if err != nil {
				t.Fatalf("the rendered file does not parse: %v\n%s", err, out)
			}
			assertContains(t, "the read's file", string(formatted), tt.wantContains, tt.wantNotContains)
		})
	}
}
