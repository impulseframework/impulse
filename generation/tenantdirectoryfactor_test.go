package generation

import (
	"go/format"
	"testing"
)

// Test_tenantDirectoryFactorTemplate pins the handlers of a tenant's word for its
// directory's second factor: each method asks its grant through one frame, which either
// answers the warning of saying the word or holds the call to its rule, writes the
// column through the directories record's update patch where the row's word is not
// already what the method says, and rolls back on a dry run. Taking the word back
// declares no warning. A column of a type named over a boolean is written converted.
func Test_tenantDirectoryFactorTemplate(t *testing.T) {
	t.Parallel()

	const rules = "NewHarborDirectoryRules().NamedBy(a.StaffIdentity().DirectoryName).PasswordsBy(a.StaffIdentity().CallerPassword).SecondFactorsBy(a.StaffIdentity().AuthSecondFactor)"
	data := func() *directoryFactorData {
		return &directoryFactorData{
			Source: "resources", Package: "app", Name: "Harbor", Word: "harbor", Directories: "HarborDirectory", Field: "AsksSecondFactor",
			Vouch: tenantOwnMethod{Name: "VouchForHarborDirectorySecondFactor", Typed: "vouch"}, Stop: tenantOwnMethod{Name: "StopVouchingForHarborDirectorySecondFactor"},
			DomainRoute: "harbors/{harborID}", Rules: rules,
			LocalPackageImports: `"example.com/app/pkg/resources"` + "\n\t" + `"example.com/app/pkg/router"`,
			ResourcePackage:     "resources", RouterPackage: "router", ApplicationName: "App", ReceiverName: "a",
		}
	}
	named := data()
	named.FlagType = "resources.Vouched"
	tests := []struct {
		name            string
		data            *directoryFactorData
		wantInOrder     []string
		wantContains    []string
		wantNotContains []string
	}{
		{
			name: "each method asks its grant, then the warning or its rule, then writes the column where the word changes",
			data: data(),
			wantInOrder: []string{
				"// VouchForHarborDirectorySecondFactor serves the method by which a harbor says its directory asks its\n// people for a second factor of its own,",
				"func (a *App) VouchForHarborDirectorySecondFactor() http.HandlerFunc {",
				`return harborDirectoryFactor(a, "VouchForHarborDirectorySecondFactor", true)`,
				"// StopVouchingForHarborDirectorySecondFactor serves the method by which a harbor takes that word back,",
				"func (a *App) StopVouchingForHarborDirectorySecondFactor() http.HandlerFunc {",
				`return harborDirectoryFactor(a, "StopVouchingForHarborDirectorySecondFactor", false)`,
				"func harborDirectoryFactor(a *App, method accesstypes.Resource, vouch bool) http.HandlerFunc {",
				`resource.MustNewRPCDecoder[request](a, method, accesstypes.Execute).WithCollection(router.Collection())`,
				"rules := " + rules + "\n",
				`hold, rule := rules.StopVouchingForDirectory, "resource.TenantDirectoryRules.StopVouchingForDirectory()"`,
				"if vouch {\n\t\thold, rule = rules.VouchForDirectory, \"resource.TenantDirectoryRules.VouchForDirectory()\"\n\t}",
				"domain := httpio.Param[accesstypes.Domain](r, router.Domain)",
				"r.Body = http.MaxBytesReader(w, r.Body, router.BodyLimit)",
				"_, caller, err := decoder.DecodeCaller(r, accesstypes.DomainScope(domain))",
				"ctx = resource.WithCaller(ctx, caller)\n\t\tctx = resource.WithRPC(ctx, method)",
				"asksWarning, err := resource.AsksWarning(r)\n\t\tif err != nil {\n\t\t\treturn httpio.NewEncoder(w).ClientMessage(ctx, err)\n\t\t}\n\t\tif asksWarning && !vouch {",
				`httpio.NewBadRequestMessagef("%s declares no warning: send the call without the X-Warning header", method)`,
				"dryRun, err := resource.IsDryRun(r)\n\t\tif err != nil {\n\t\t\treturn httpio.NewEncoder(w).ClientMessage(ctx, err)\n\t\t}\n\t\tif err := a.ResourceClient().ExecuteFunc(",
				"if asksWarning {",
				"said, err := rules.VouchForDirectoryWarning(ctx, txn, domain)",
				"if err := resource.CheckWarning(string(method), said); err != nil {",
				"return resource.ErrWarningAsked",
				"directory, err := hold(ctx, txn, domain)\n\t\t\tif err != nil {\n\t\t\t\treturn errors.Wrap(err, rule)\n\t\t\t}",
				"if directory.SecondFactor != vouch {",
				"id, err := impulse.UUIDFromString(directory.Key)",
				"if err := resources.NewHarborDirectoryUpdatePatch(id).SetAsksSecondFactor(vouch).Buffer(ctx, txn, eventSource); err != nil {",
				"if dryRun {\n\t\t\t\treturn resource.ErrDryRun\n\t\t\t}",
				"if asksWarning && resource.WarningAsked(err) {",
				"return httpio.NewEncoder(w).Ok(warning)",
				"if dryRun && resource.DryRunRolledBack(err) {",
				"live.Publish(ctx, w, a.LiveService(), liveDerivations, domain, touched)",
			},
			wantContains: []string{
				"POST <prefix>/harbors/{harborID}/vouch-for-harbor-directory-second-factor",
				"POST <prefix>/harbors/{harborID}/stop-vouching-for-harbor-directory-second-factor",
				`which a person types "vouch" under`,
				"(HarborDirectory.AsksSecondFactor, which\n// the sign-in reads in its own transaction)",
			},
			// The sign-in reads the row in its own transaction, so nothing rereads the
			// roster or signals the tenants kind.
			wantNotContains: []string{"TenantRoster().Reload", "resource.KindTenants", "SetAsksSecondFactor(resources."},
		},
		{
			name:            "a column of a type named over a boolean is written converted",
			data:            named,
			wantContains:    []string{"resources.NewHarborDirectoryUpdatePatch(id).SetAsksSecondFactor(resources.Vouched(vouch)).Buffer(ctx, txn, eventSource)"},
			wantNotContains: []string{"SetAsksSecondFactor(vouch)"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &client{}
			out, err := c.generateTemplateOutput("tenantDirectoryFactorTemplate", tenantDirectoryFactorTemplate, tt.data)
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
			assertContains(t, "the second factor's file", string(formatted), tt.wantContains, tt.wantNotContains)
		})
	}
}
