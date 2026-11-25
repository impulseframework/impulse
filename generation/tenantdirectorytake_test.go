package generation

import (
	"go/format"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/resource"
)

// Test_tenantDirectoryTakeTemplate pins the handler of the method by which a tenant takes
// the directory its caller is signed in through: it asks its grant, then holds the
// session's directory to the rules (FromSession), whose refusals answer the call and the
// request for its warning alike, before the statement is said or anything is written; it
// answers the statement, said as the tenant that stands, in place of running the call; it
// writes the row through the step that sets a tenant's directory from a sign-in, rolls
// back on a dry run, and rereads the roster and signals the tenants kind once the
// transaction has committed. The tenant's key is handed over as the directories record's
// tenant column takes it: an id, or the id's text.
func Test_tenantDirectoryTakeTemplate(t *testing.T) {
	t.Parallel()

	const rules = "NewHarborDirectoryRules().NamedBy(a.StaffIdentity().DirectoryName)"
	data := func(uuid bool) *directoryTakeData {
		return &directoryTakeData{
			Source: "resources", Package: "app", Name: "Harbor", Word: "harbor", Directories: "HarborDirectory",
			Take: tenantOwnMethod{Name: "TakeHarborDirectory", Typed: resource.TakeDirectoryPhrase}, TenantIsUUID: uuid,
			DomainRoute: "harbors/{harborID}", Rules: rules,
			LocalPackageImports: `"example.com/app/pkg/resources"` + "\n\t" + `"example.com/app/pkg/router"`,
			ResourcePackage:     "resources", RouterPackage: "router", ApplicationName: "App", ReceiverName: "a",
		}
	}
	tests := []struct {
		name            string
		data            *directoryTakeData
		wantInOrder     []string
		wantContains    []string
		wantNotContains []string
	}{
		{
			name: "the grant, the directory the session came through held to the rules, then the statement or the row, then the roster",
			data: data(true),
			wantInOrder: []string{
				"// TakeHarborDirectory serves the method by which a harbor takes the directory its caller is\n// signed in through as its own,",
				"func (a *App) TakeHarborDirectory() http.HandlerFunc {",
				`resource.MustNewRPCDecoder[request](a, "TakeHarborDirectory", accesstypes.Execute).WithCollection(router.Collection())`,
				"rules := " + rules + "\n",
				"domain := httpio.Param[accesstypes.Domain](r, router.Domain)",
				"r.Body = http.MaxBytesReader(w, r.Body, router.BodyLimit)",
				"_, caller, err := decoder.DecodeCaller(r, accesstypes.DomainScope(domain))",
				"ctx = resource.WithCaller(ctx, caller)",
				"asksWarning, err := resource.AsksWarning(r)",
				"tenant, err := impulse.UUIDFromString(string(domain))",
				`httpio.NewNotFoundMessage("This harbor does not exist.")`,
				"dryRun, err := resource.IsDryRun(r)\n\t\tif err != nil {\n\t\t\treturn httpio.NewEncoder(w).ClientMessage(ctx, err)\n\t\t}\n\t\tif err := a.ResourceClient().ExecuteFunc(",
				"directory, err := rules.FromSession(ctx, txn, domain)\n\t\t\tif err != nil {\n\t\t\t\treturn errors.Wrap(err, \"resource.TenantDirectoryRules.FromSession()\")\n\t\t\t}",
				"if asksWarning {",
				"said := rules.TakeDirectoryWarning(directory, false)",
				`if err := resource.CheckWarning("TakeHarborDirectory", said); err != nil {`,
				"return resource.ErrWarningAsked",
				"if err := SetHarborDirectory(ctx, txn, tenant, directory, eventSource); err != nil {\n\t\t\t\treturn errors.Wrap(err, \"SetHarborDirectory()\")\n\t\t\t}",
				"if dryRun {\n\t\t\t\treturn resource.ErrDryRun\n\t\t\t}",
				"if asksWarning && resource.WarningAsked(err) {",
				"return httpio.NewEncoder(w).Ok(warning)",
				"if dryRun && resource.DryRunRolledBack(err) {",
				"live.Publish(ctx, w, a.LiveService(), liveDerivations, domain, touched)",
				"if err := a.TenantRoster().Reload(ctx); err != nil {",
				"if err := a.LiveService().Signal(ctx, resource.KindTenants); err != nil {",
				"return httpio.NewEncoder(w).Ok(nil)",
			},
			wantContains: []string{
				"POST <prefix>/harbors/{harborID}/take-harbor-directory with an empty body",
				`which a person types "I am authorized" under`,
				"(resource.TenantDirectoryRules.TakeDirectoryWarning)",
				"the HarborDirectory record's own create is\n// refused.",
			},
			// The directory is never typed: the body carries nothing, and no column of
			// the record is set by the handler itself.
			wantNotContains: []string{"SetMethod(", "SetConnection(", "NewHarborDirectoryCreatePatch", "TakeDirectoryWarning(directory, true)"},
		},
		{
			name:            "a tenant column that holds the id's text takes the domain as it is",
			data:            data(false),
			wantContains:    []string{"tenant := string(domain)", "if err := SetHarborDirectory(ctx, txn, tenant, directory, eventSource); err != nil {"},
			wantNotContains: []string{"impulse.UUIDFromString", "does not exist"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &client{}
			out, err := c.generateTemplateOutput("tenantDirectoryTakeTemplate", tenantDirectoryTakeTemplate, tt.data)
			if err != nil {
				t.Fatalf("generateTemplateOutput() error = %v", err)
			}
			formatted, err := format.Source(out)
			if err != nil {
				t.Fatalf("the rendered file does not parse: %v\n%s", err, out)
			}
			if len(tt.wantInOrder) > 0 {
				assertInOrder(t, string(formatted), tt.wantInOrder...)
			}
			assertContains(t, "the take file", string(formatted), tt.wantContains, tt.wantNotContains)
		})
	}
}

// Test_directoryTake pins where the method that takes a tenant's directory is served:
// for every tenant record that declares a directories record, whatever columns beside
// the directory it carries and whatever auth the outlets sign people in through, on
// every outlet that serves sessions and the directories record, and on no other; none
// where the package declares no directories record. It is registered with Execute in the
// tenant's scope, classed access, and the read of the tenant's sign-in settings is asked
// under its grant too.
func Test_directoryTake(t *testing.T) {
	t.Parallel()

	takeOf := func(record string) []string {
		return []string{"Take" + record + "Directory"}
	}
	tests := []struct {
		name    string
		structs []string
		// directoriesOn are the outlets the directories record is served on, every
		// outlet where it is empty.
		directoriesOn []string
		// want are the outlets that serve the method.
		want []string
		// wantMethods are the take methods registered, and wantGrants the tenant's own
		// methods the read of its sign-in settings is asked under.
		wantMethods []string
		wantGrants  []string
	}{
		{
			name:        "a directories record with the default seat and the second-factor column",
			structs:     []string{"Harbor", "HarborDirectory"},
			want:        []string{defaultOutletName, "members"},
			wantMethods: takeOf("Harbor"),
			wantGrants: []string{
				"TakeHarborDirectory", "MakeHarborDirectoryOnly", "EndHarborDirectoryOnly",
				"VouchForHarborDirectorySecondFactor", "StopVouchingForHarborDirectorySecondFactor",
			},
		},
		{
			name:        "a directories record with the default seat alone",
			structs:     []string{"Sector", "Mole"},
			want:        []string{defaultOutletName, "members"},
			wantMethods: takeOf("Sector"),
			wantGrants:  takeOf("Sector"),
		},
		{
			name:        "a directories record with neither column still takes a directory, and the read is served under the method alone",
			structs:     []string{"Sector", "Breakwater"},
			want:        []string{defaultOutletName, "members"},
			wantMethods: takeOf("Sector"),
			wantGrants:  takeOf("Sector"),
		},
		{
			name:          "an outlet that does not serve the directories record serves no method that takes one",
			structs:       []string{"Sector", "Breakwater"},
			directoriesOn: []string{defaultOutletName},
			want:          []string{defaultOutletName},
			wantMethods:   takeOf("Sector"),
			wantGrants:    takeOf("Sector"),
		},
		{
			name:    "a package that declares no directories record takes none",
			structs: []string{"Sector"},
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
			r.defaultOutlet = routerOutlet{name: defaultOutletName, prefix: "api", servesSessions: true}
			r.extraOutlets = []routerOutlet{
				{name: "members", prefix: "members", servesSessions: true},
				{name: "machines", prefix: "machines", apiKey: true},
			}
			for _, res := range r.resources {
				res.OutletNames = []string{defaultOutletName, "members", "machines"}
				if len(tt.structs) > 1 && res.Name() == tt.structs[1] && len(tt.directoriesOn) > 0 {
					res.OutletNames = tt.directoriesOn
				}
			}

			var got []string
			for _, outlet := range r.allOutlets() {
				if r.directoryTake().servedOn(outlet.name) {
					got = append(got, outlet.name)
				}
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("the outlets that serve the method mismatch (-want +got):\n%s", diff)
			}

			b := resource.NewCollectionBuilder()
			if err := r.collectDirectoryOnlyRegistrations(b); err != nil {
				t.Fatalf("collectDirectoryOnlyRegistrations() error = %v", err)
			}
			var methods []string
			for _, res := range b.Data().Resources {
				if res.Name != accesstypes.Resource("Take"+tt.structs[0]+"Directory") {
					continue
				}
				methods = append(methods, string(res.Name))
				if res.Scope != accesstypes.DomainPermissionScope || !cmp.Equal(res.Permissions, []accesstypes.Permission{accesstypes.Execute}) || !cmp.Equal(res.Classes, []accesstypes.ActionClass{accesstypes.ClassAccess}) {
					t.Errorf("%s is registered as %v; want Execute in the tenant's scope, classed access", res.Name, res)
				}
			}
			if diff := cmp.Diff(tt.wantMethods, methods); diff != "" {
				t.Errorf("the collection's take methods mismatch (-want +got):\n%s", diff)
			}

			var grants []string
			for _, method := range r.signInSettingsGrants() {
				grants = append(grants, method.Name)
			}
			if diff := cmp.Diff(tt.wantGrants, grants); diff != "" {
				t.Errorf("signInSettingsGrants() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
