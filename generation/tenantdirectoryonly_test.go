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
// directory-only switch: each of the two methods runs in the tenant's scope to its own
// Execute grant, with the caller stamped for the warning to ask whom it may list; the
// one that turns the switch on answers its warning in place of running the call, from
// the people the rules of the switch hold (every auth's reader of the people the switch
// keeps out and the list that decides whether the caller is told who they are), which
// the frame hands the warning none of; the one that turns it off declares no warning
// and refuses a request for one; each asks the rules of the switch and then writes the
// flag through the record's update patch, reloads the roster and signals the tenants
// kind. The file holds no read: how the switch stands is said by the read of the
// tenant's sign-in settings, which has a file of its own.
func Test_tenantDirectoryOnlyTemplate(t *testing.T) {
	t.Parallel()

	data := func(people ...*directoryOnlyPeople) *directoryOnlyData {
		return &directoryOnlyData{
			Source: "resources", Package: "app", Name: "Harbor", Word: "harbor", Field: "DirectoryOnly",
			Make: tenantOwnMethod{Name: "MakeHarborDirectoryOnly", Typed: "close"}, End: tenantOwnMethod{Name: "EndHarborDirectoryOnly"},
			DomainRoute: "harbors/{harborID}", KeyType: "impulse.UUID", KeyIsUUID: true, People: people,
			SwitchRules:         "NewHarborDirectoryRules()",
			LocalPackageImports: `"example.com/app/pkg/resources"` + "\n\t" + `"example.com/app/pkg/router"`,
			ResourcePackage:     "resources", RouterPackage: "router", ApplicationName: "App", ReceiverName: "a",
		}
	}
	textKey := data()
	textKey.KeyType, textKey.KeyIsUUID, textKey.FlagType = "string", false, "resources.Flag"
	// The rules of a program that generates two auths' management are named by both.
	const namedRules = "NewHarborDirectoryRules().NamedBy(a.StaffIdentity().DirectoryName, a.MembersIdentity().DirectoryName)"
	named := data(
		&directoryOnlyPeople{Accessor: "StaffIdentity", List: "StaffHarborUsers", Fields: []string{"username", "name"}},
		&directoryOnlyPeople{Accessor: "MembersIdentity", List: "MemberHarborUsers", Fields: []string{"username", "name"}},
	)
	// The frame that turns the switch asks each auth's client whether the caller has a
	// password, and its warning reads the auths' people from the rules' entries, which
	// say what a page calls each auth's people.
	const switchRules = namedRules + ".PasswordsBy(a.StaffIdentity().CallerPassword, a.MembersIdentity().CallerPassword)" +
		`.CountedBy(resource.DirectoryPeople{Label: "Staff", List: "StaffHarborUsers", Fields: []accesstypes.Tag{"username", "name"}, Seated: a.StaffIdentity().SeatedCount, Outsiders: a.StaffIdentity().DirectoryOutsiders, AdministratorsEnter: a.StaffIdentity().AdministratorsEnterWithPassword}, ` +
		`resource.DirectoryPeople{Label: "Member", List: "MemberHarborUsers", Fields: []accesstypes.Tag{"username", "name"}, Seated: a.MembersIdentity().SeatedCount, Outsiders: a.MembersIdentity().DirectoryOutsiders, AdministratorsEnter: a.MembersIdentity().AdministratorsEnterWithPassword})`
	named.SwitchRules = switchRules
	tests := []struct {
		name            string
		data            *directoryOnlyData
		wantInOrder     []string
		wantContains    []string
		wantNotContains []string
	}{
		{
			name: "each method asks its grant, then the warning or the rules, then writes the flag; the warning names each auth's people list",
			data: named,
			wantInOrder: []string{
				"func (a *App) MakeHarborDirectoryOnly() http.HandlerFunc {",
				`return harborDirectoryOnly(a, "MakeHarborDirectoryOnly", true)`,
				"func (a *App) EndHarborDirectoryOnly() http.HandlerFunc {",
				`return harborDirectoryOnly(a, "EndHarborDirectoryOnly", false)`,
				"func harborDirectoryOnly(a *App, method accesstypes.Resource, only bool) http.HandlerFunc {",
				`resource.MustNewRPCDecoder[request](a, method, accesstypes.Execute).WithCollection(router.Collection())`,
				// The rules' sentences name the directory as each auth's registrations do.
				"rules := " + switchRules + "\n",
				"domain := httpio.Param[accesstypes.Domain](r, router.Domain)",
				"_, caller, err := decoder.DecodeCaller(r, accesstypes.DomainScope(domain))",
				"ctx = resource.WithCaller(ctx, caller)",
				"asksWarning, err := resource.AsksWarning(r)\n\t\tif err != nil {\n\t\t\treturn httpio.NewEncoder(w).ClientMessage(ctx, err)\n\t\t}\n\t\tif asksWarning && !only {",
				`httpio.NewBadRequestMessagef("%s declares no warning: send the call without the X-Warning header", method)`,
				"id, err := impulse.UUIDFromString(string(domain))",
				"dryRun, err := resource.IsDryRun(r)\n\t\tif err != nil {\n\t\t\treturn httpio.NewEncoder(w).ClientMessage(ctx, err)\n\t\t}\n\t\tif err := a.ResourceClient().ExecuteFunc(",
				"if asksWarning {",
				"said, err := rules.DirectoryOnlyWarning(ctx, txn, domain)\n",
				"if err := resource.CheckWarning(string(method), said); err != nil {",
				"return resource.ErrWarningAsked",
				"if err := rules.DirectoryOnly(ctx, txn, domain, only); err != nil {",
				"if err := resources.NewHarborUpdatePatch(id).SetDirectoryOnly(only).Buffer(ctx, txn, eventSource); err != nil {",
				"return resource.ErrDryRun",
				"if asksWarning && resource.WarningAsked(err) {",
				"return httpio.NewEncoder(w).Ok(warning)",
				"if err := a.TenantRoster().Reload(ctx); err != nil {",
				"if err := a.LiveService().Signal(ctx, resource.KindTenants); err != nil {",
			},
			wantContains: []string{
				"POST <prefix>/harbors/{harborID}/make-harbor-directory-only",
				"POST <prefix>/harbors/{harborID}/end-harbor-directory-only",
				`which a person types "close" under`,
			},
			// The read of what turning the switch on would do is the method's warning now,
			// and the frame hands the warning no people of its own: the rules hold them.
			// The read of how the switch stands is the read of the tenant's sign-in
			// settings, in its own file.
			wantNotContains: []string{
				"DirectoryOnlyConsequence", "SetHarborDirectoryOnly", "req.Turn()", "DirectoryOnlyWarning(ctx, txn, domain,",
				"TenantSignInSettingsHandler", "func (a *App) DirectoryOnly() http.HandlerFunc", "GET\n// <prefix>", "GET <prefix>",
			},
		},
		{
			name:            "a program that generates no auth's management counts and names no person",
			data:            data(),
			wantContains:    []string{"The program generates no auth's management, so the warning counts and names no person", "said, err := rules.DirectoryOnlyWarning(ctx, txn, domain)", "rules := NewHarborDirectoryRules()\n"},
			wantNotContains: []string{"resource.DirectoryPeople{", "NamedBy(", "PasswordsBy("},
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
			formatted, err := format.Source(out)
			if err != nil {
				t.Fatalf("the rendered file does not parse: %v\n%s", err, out)
			}
			if len(tt.wantInOrder) > 0 {
				assertInOrder(t, string(formatted), tt.wantInOrder...)
			}
			assertContains(t, "the switch's file", string(formatted), tt.wantContains, tt.wantNotContains)
		})
	}

	// Neither the tenants file nor the switch's carries the read of the tenant's sign-in
	// settings: it has its own file.
	for name, template := range map[string]string{"tenantsTemplate": tenantsTemplate, "tenantDirectoryOnlyTemplate": tenantDirectoryOnlyTemplate} {
		if strings.Contains(template, "TenantSignInSettingsHandler") || strings.Contains(template, "{{ .StateFunc }}") {
			t.Errorf("%s still serves a read of the tenant's sign-in settings", name)
		}
	}
}

// Test_resourceGenerator_directoryOnlyPeople pins what the rules of the switch are given
// of each auth whose client answers who the switch keeps out: what a page calls the
// auth's people, the client's readers of who keeps the administrators' way in where the
// auth has the password method, and of the invitations that would make another where it
// also lists its invitations, with the list and the fields that name the person invited.
func Test_resourceGenerator_directoryOnlyPeople(t *testing.T) {
	t.Parallel()

	directory := &identityTenantDirectory{OnlyColumn: "DirectoryOnly", Table: "HarborDirectories", TenantColumn: "HarborId", MethodColumn: "Method", ConnectionColumn: "Connection"}
	instance := func(auth string, password bool, tables ...identityTable) *identityInstance {
		inst := &identityInstance{auth: auth, password: password, tenant: &identityTenant{name: "Harbor", table: "Harbors", key: "Id", word: "harbor", directory: directory}, tables: map[identityTable]bool{}}
		for _, kind := range tables {
			inst.tables[kind] = true
		}

		return inst
	}
	r := &resourceGenerator{client: &client{identity: []*identityInstance{
		instance("Staff", true, identityUsers, identityMemberships, identityMailLinks),
		instance("Member", true, identityUsers, identityMemberships),
		instance("Guest", false, identityUsers, identityMemberships, identityMailLinks),
		// What a page calls an auth's people is the auth's name in words.
		instance("NightCrew", false, identityUsers, identityMemberships),
		// An auth that lists no people answers nobody, and is no entry.
		instance("Visitor", true, identityMailLinks),
	}}}
	want := []*directoryOnlyPeople{
		{Accessor: "StaffIdentity", Label: "Staff", List: "StaffHarborUsers", Fields: []string{"username", "name"}, Administrators: true, InvitationList: "StaffHarborInvitations", InvitationFields: []string{"email", "name"}},
		{Accessor: "MemberIdentity", Label: "Member", List: "MemberHarborUsers", Fields: []string{"username", "name"}, Administrators: true},
		{Accessor: "GuestIdentity", Label: "Guest", List: "GuestHarborUsers", Fields: []string{"username", "name"}},
		{Accessor: "NightCrewIdentity", Label: "Night crew", List: "NightCrewHarborUsers", Fields: []string{"username", "name"}},
	}
	if diff := cmp.Diff(want, r.directoryOnlyPeople()); diff != "" {
		t.Errorf("directoryOnlyPeople() mismatch (-want +got):\n%s", diff)
	}
}

// Test_tenantDirectoryRoleTemplate pins the handlers of the two methods that say how a
// tenant admits its directory's people. The one that opens the tenant runs in the
// tenant's scope to its own Execute grant with the caller stamped, reads the tenant's
// directory, holds the role to the auth's rule of who may give it, answers its warning
// in place of running the call, and writes the role through the directories record's
// update patch; the warning is answered while no role is picked, and the call is not.
// The one that makes the tenant invite only declares no warning, refuses a request for
// one, and clears the role where there is one.
func Test_tenantDirectoryRoleTemplate(t *testing.T) {
	t.Parallel()

	data := func() *directoryRoleData {
		return &directoryRoleData{
			Source: "resources", Package: "app", Name: "Harbor", Word: "harbor", Directories: "HarborDirectory", Field: "DefaultSeat",
			Open: tenantOwnMethod{Name: "OpenHarborToDirectory", Typed: "open"}, InviteOnly: tenantOwnMethod{Name: "MakeHarborInviteOnly"},
			DomainRoute: "harbors/{harborID}", SeatIsPointer: true, Auth: "Staff", IdentityAccessor: "StaffIdentity", Picker: "StaffHarborDirectoryRoles",
			Rules:               "NewHarborDirectoryRules().NamedBy(a.StaffIdentity().DirectoryName)",
			LocalPackageImports: `"example.com/app/pkg/resources"` + "\n\t" + `"example.com/app/pkg/router"`,
			ResourcePackage:     "resources", RouterPackage: "router", ApplicationName: "App", ReceiverName: "a",
		}
	}
	plain := data()
	plain.SeatIsPointer = false
	tests := []struct {
		name            string
		data            *directoryRoleData
		wantInOrder     []string
		wantContains    []string
		wantNotContains []string
	}{
		{
			name: "opening asks its grant, the directory, the rule of who may give the role, then the warning or the write; invite only clears the role",
			data: data(),
			wantInOrder: []string{
				"func (a *App) OpenHarborToDirectory() http.HandlerFunc {",
				"Role string `json:\"role\"`",
				`resource.MustNewRPCDecoder[request](a, "OpenHarborToDirectory", accesstypes.Execute).WithCollection(router.Collection())`,
				// The warning names the directory as the auth's registrations do.
				"rules := NewHarborDirectoryRules().NamedBy(a.StaffIdentity().DirectoryName)\n",
				"req, caller, err := decoder.DecodeCaller(r, accesstypes.DomainScope(domain))",
				"ctx = resource.WithCaller(ctx, caller)",
				"role := accesstypes.Role(strings.TrimSpace(req.Role))",
				"asksWarning, err := resource.AsksWarning(r)\n\t\tif err != nil {\n\t\t\treturn httpio.NewEncoder(w).ClientMessage(ctx, err)\n\t\t}\n\t\tvar warning *resource.Warning\n\t\tif role == \"\" && !asksWarning {",
				"directory, err := rules.Directory(ctx, txn, domain)",
				`if role != "" {`,
				"if err := a.StaffIdentity().DirectoryRole(ctx, txn, domain, role); err != nil {",
				"if asksWarning {",
				"said := rules.OpenToDirectoryWarning(directory, role)",
				`if err := resource.CheckWarning("OpenHarborToDirectory", said); err != nil {`,
				"return resource.ErrWarningAsked",
				"id, err := impulse.UUIDFromString(directory.Key)",
				"if err := resources.NewHarborDirectoryUpdatePatch(id).SetDefaultSeat(&seat).Buffer(ctx, txn, eventSource); err != nil {",
				"return resource.ErrDryRun",
				"if asksWarning && resource.WarningAsked(err) {",
				"live.Publish(ctx, w, a.LiveService(), liveDerivations, domain, touched)",
				"func (a *App) MakeHarborInviteOnly() http.HandlerFunc {",
				`resource.MustNewRPCDecoder[request](a, "MakeHarborInviteOnly", accesstypes.Execute).WithCollection(router.Collection())`,
				"rules := NewHarborDirectoryRules().NamedBy(a.StaffIdentity().DirectoryName)\n",
				"_, caller, err := decoder.DecodeCaller(r, accesstypes.DomainScope(domain))",
				"asksWarning, err := resource.AsksWarning(r)\n\t\tif err != nil {\n\t\t\treturn httpio.NewEncoder(w).ClientMessage(ctx, err)\n\t\t}\n\t\tif asksWarning {",
				`httpio.NewBadRequestMessage("MakeHarborInviteOnly declares no warning: send the call without the X-Warning header")`,
				"directory, err := rules.Directory(ctx, txn, domain)",
				`if directory.DefaultRole != "" {`,
				"if err := resources.NewHarborDirectoryUpdatePatch(id).SetDefaultSeat(nil).Buffer(ctx, txn, eventSource); err != nil {",
				"return resource.ErrDryRun",
			},
			wantContains: []string{
				"POST <prefix>/harbors/{harborID}/open-harbor-to-directory",
				"POST <prefix>/harbors/{harborID}/make-harbor-invite-only",
				`which a person types "open" under`,
				"(StaffHarborDirectoryRoles)",
			},
		},
		{
			name:            "a default seat that is a plain string holds no role as the empty string",
			data:            plain,
			wantContains:    []string{"SetDefaultSeat(seat).Buffer(ctx, txn, eventSource)", `SetDefaultSeat("").Buffer(ctx, txn, eventSource)`},
			wantNotContains: []string{"SetDefaultSeat(&seat)", "SetDefaultSeat(nil)"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &client{}
			out, err := c.generateTemplateOutput("tenantDirectoryRoleTemplate", tenantDirectoryRoleTemplate, tt.data)
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
			assertContains(t, "the default role's file", string(formatted), tt.wantContains, tt.wantNotContains)
		})
	}
}

// Test_directoryOnlyRoutes pins where the tenant's own methods are served and what
// registers them. Each is under the tenant's own route, behind the domain guard, and
// the read of the tenant's sign-in settings comes first on every outlet that serves
// any of them. The switch's two methods are on every session outlet the tenant record
// is on and on no API-key outlet. The two methods that say how the tenant admits its directory's
// people are served where the session outlets that serve the directories record are
// all of one Auth whose management the program generates and whose roles are given by
// hand, and nowhere otherwise. The two methods of the tenant's word for its directory's
// second factor are served on every session outlet that serves a directories record
// that carries the column, whatever the auths, and nowhere where it carries none. Each
// route has a denied case in the authorization matrix, a method's dry run and its
// request for a warning included; each method is in the collection with Execute in the
// tenant's scope; and a record that declares neither registers nothing. An
// application's own method or registration under a method's name is refused.
func Test_directoryOnlyRoutes(t *testing.T) {
	t.Parallel()

	const staffAuth = "example.com/app/pkg/auth/staff"
	// staff is the generated management of the Staff auth: its people are listed in a
	// tenant, its roles are given by hand, and its sources were written for a
	// directories record that carries the default seat, so its client is handed the
	// role file's answers on giving a role.
	staff := func(synced bool) *identityInstance {
		return &identityInstance{
			importPath: staffAuth, auth: "Staff", rolesSynced: synced,
			tenant: &identityTenant{name: "Harbor", table: "Harbors", key: "Id", word: "harbor", directorySeat: "DefaultSeat"},
			tables: map[identityTable]bool{identityUsers: true, identityMemberships: true},
		}
	}
	generator := func(t *testing.T, auths []string, identity []*identityInstance, structs ...string) *resourceGenerator {
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
		if len(auths) > 0 {
			r.defaultOutlet.auth = &outletAuth{importPath: auths[0]}
			r.extraOutlets[0].auth = &outletAuth{importPath: auths[len(auths)-1]}
		}
		r.identity = identity

		return r
	}
	post := func(prefix, method, segment string) *generatedRoute {
		return &generatedRoute{
			Method: "POST", Path: "/" + prefix + "/harbors/{harborID}/" + segment, HandlerFunc: method, DomainScoped: true,
			TestURL: "/" + prefix + "/harbors/" + domainTestValue + "/" + segment, SelfBounded: true,
			// A method's frame answers a request for a dry run or a warning itself, so
			// its route registers ahead of the router's refusal of the two headers.
			ReadsDryRunAndWarning: true,
		}
	}
	readUnder := func(prefix string) []*generatedRoute {
		return []*generatedRoute{
			{Method: "GET", Path: "/" + prefix + "/harbors/{harborID}/sign-in-settings", HandlerFunc: "TenantSignInSettings", DomainScoped: true, TestURL: "/" + prefix + "/harbors/" + domainTestValue + "/sign-in-settings"},
		}
	}
	switchUnder := func(prefix string) []*generatedRoute {
		return []*generatedRoute{
			post(prefix, "MakeHarborDirectoryOnly", "make-harbor-directory-only"),
			post(prefix, "EndHarborDirectoryOnly", "end-harbor-directory-only"),
		}
	}
	roleUnder := func(prefix string) []*generatedRoute {
		return []*generatedRoute{
			post(prefix, "OpenHarborToDirectory", "open-harbor-to-directory"),
			post(prefix, "MakeHarborInviteOnly", "make-harbor-invite-only"),
		}
	}
	factorUnder := func(prefix string) []*generatedRoute {
		return []*generatedRoute{
			post(prefix, "VouchForHarborDirectorySecondFactor", "vouch-for-harbor-directory-second-factor"),
			post(prefix, "StopVouchingForHarborDirectorySecondFactor", "stop-vouching-for-harbor-directory-second-factor"),
		}
	}
	methodCases := func(prefix string, segments ...string) []string {
		var cases []string
		for _, segment := range segments {
			line := "POST /" + prefix + "/harbors/" + domainTestValue + "/" + segment + " {}"
			cases = append(cases, line, line+" X-Dry-Run", line+" X-Warning")
		}

		return cases
	}
	readCase := func(prefix string) []string {
		return []string{"GET /" + prefix + "/harbors/" + domainTestValue + "/sign-in-settings"}
	}
	switchCases := func(prefix string) []string {
		return methodCases(prefix, "make-harbor-directory-only", "end-harbor-directory-only")
	}
	roleCases := func(prefix string) []string {
		return methodCases(prefix, "open-harbor-to-directory", "make-harbor-invite-only")
	}
	factorCases := func(prefix string) []string {
		return methodCases(prefix, "vouch-for-harbor-directory-second-factor", "stop-vouching-for-harbor-directory-second-factor")
	}
	// The fixture's HarborDirectory record carries the second-factor column, so every
	// case on it serves the two methods of the word after the switch's and the role's,
	// each outlet's after the read of the tenant's sign-in settings.
	switchAndFactor := func() map[string][]*generatedRoute {
		return map[string][]*generatedRoute{
			defaultOutletName: slices.Concat(readUnder("api"), switchUnder("api"), factorUnder("api")),
			"members":         slices.Concat(readUnder("members"), switchUnder("members"), factorUnder("members")),
		}
	}
	switchAndFactorCases := slices.Concat(readCase("api"), switchCases("api"), factorCases("api"), readCase("members"), switchCases("members"), factorCases("members"))
	handlers := func(routes []*generatedRoute) []string {
		var names []string
		for _, route := range routes {
			names = append(names, route.HandlerFunc)
		}

		return names
	}
	// The two methods of the default role give and take away what a directory's people
	// may do, the two of the switch decide who may sign in to the tenant at all, and the
	// two of the word for the directory's second factor change what a sign-in asks of
	// them, so the action of each carries the class access.
	execute := func(names ...string) []resource.CollectionResource {
		var out []resource.CollectionResource
		for _, name := range names {
			out = append(out, resource.CollectionResource{
				Name: accesstypes.Resource(name), Scope: accesstypes.DomainPermissionScope,
				Permissions: []accesstypes.Permission{accesstypes.Execute}, Classes: []accesstypes.ActionClass{accesstypes.ClassAccess},
			})
		}

		return out
	}
	tests := []struct {
		name        string
		structs     []string
		auths       []string
		identity    []*identityInstance
		wantRoutes  map[string][]*generatedRoute
		wantCases   []string
		wantMethods []resource.CollectionResource
	}{
		{
			name:        "a record that declares the switch, on every session outlet it is on; no outlet auth, so no method of the default role, and the word for the second factor all the same",
			structs:     []string{"Harbor", "HarborDirectory"},
			wantRoutes:  switchAndFactor(),
			wantCases:   switchAndFactorCases,
			wantMethods: execute("EndHarborDirectoryOnly", "MakeHarborDirectoryOnly", "StopVouchingForHarborDirectorySecondFactor", "VouchForHarborDirectorySecondFactor"),
		},
		{
			name:     "session outlets of one auth whose management the program generates serve the default role's methods too",
			structs:  []string{"Harbor", "HarborDirectory"},
			auths:    []string{staffAuth},
			identity: []*identityInstance{staff(false)},
			wantRoutes: map[string][]*generatedRoute{
				defaultOutletName: slices.Concat(readUnder("api"), switchUnder("api"), roleUnder("api"), factorUnder("api")),
				"members":         slices.Concat(readUnder("members"), switchUnder("members"), roleUnder("members"), factorUnder("members")),
			},
			wantCases: slices.Concat(
				readCase("api"), switchCases("api"), roleCases("api"), factorCases("api"),
				readCase("members"), switchCases("members"), roleCases("members"), factorCases("members"),
			),
			wantMethods: execute(
				"EndHarborDirectoryOnly", "MakeHarborDirectoryOnly", "MakeHarborInviteOnly", "OpenHarborToDirectory",
				"StopVouchingForHarborDirectorySecondFactor", "VouchForHarborDirectorySecondFactor",
			),
		},
		{
			name:        "a record with a directories record and no switch serves the default role's methods and the word for the second factor alone",
			structs:     []string{"Sector", "HarborDirectory"},
			auths:       []string{staffAuth},
			identity:    []*identityInstance{{importPath: staffAuth, auth: "Staff", tenant: &identityTenant{name: "Sector", table: "Sectors", key: "Id", word: "sector", directorySeat: "DefaultSeat"}, tables: map[identityTable]bool{identityUsers: true, identityMemberships: true}}},
			wantRoutes:  map[string][]*generatedRoute{},
			wantMethods: execute("MakeSectorInviteOnly", "OpenSectorToDirectory", "StopVouchingForSectorDirectorySecondFactor", "VouchForSectorDirectorySecondFactor"),
		},
		{
			name:    "session outlets of two auths serve no method of the default role: one identity client judges the role",
			structs: []string{"Harbor", "HarborDirectory"},
			auths:   []string{staffAuth, "example.com/app/pkg/auth/members"},
			identity: []*identityInstance{staff(false), {
				importPath: "example.com/app/pkg/auth/members", auth: "Members", tenant: &identityTenant{name: "Harbor", table: "Harbors", key: "Id", word: "harbor", directorySeat: "DefaultSeat"},
				tables: map[identityTable]bool{identityUsers: true, identityMemberships: true},
			}},
			wantRoutes:  switchAndFactor(),
			wantCases:   switchAndFactorCases,
			wantMethods: execute("EndHarborDirectoryOnly", "MakeHarborDirectoryOnly", "StopVouchingForHarborDirectorySecondFactor", "VouchForHarborDirectorySecondFactor"),
		},
		{
			name:        "an auth whose management the program does not generate serves none",
			structs:     []string{"Harbor", "HarborDirectory"},
			auths:       []string{staffAuth},
			wantRoutes:  switchAndFactor(),
			wantCases:   switchAndFactorCases,
			wantMethods: execute("EndHarborDirectoryOnly", "MakeHarborDirectoryOnly", "StopVouchingForHarborDirectorySecondFactor", "VouchForHarborDirectorySecondFactor"),
		},
		{
			name:    "an auth whose own reading of the directories record carries no default seat serves none: its management judges no such role",
			structs: []string{"Harbor", "HarborDirectory"},
			auths:   []string{staffAuth},
			identity: []*identityInstance{{
				importPath: staffAuth, auth: "Staff", tenant: &identityTenant{name: "Harbor", table: "Harbors", key: "Id", word: "harbor"},
				tables: map[identityTable]bool{identityUsers: true, identityMemberships: true},
			}},
			wantRoutes:  switchAndFactor(),
			wantCases:   switchAndFactorCases,
			wantMethods: execute("EndHarborDirectoryOnly", "MakeHarborDirectoryOnly", "StopVouchingForHarborDirectorySecondFactor", "VouchForHarborDirectorySecondFactor"),
		},
		{
			name:        "an auth whose roles a directory writes serves none: nobody gives a role by hand",
			structs:     []string{"Harbor", "HarborDirectory"},
			auths:       []string{staffAuth},
			identity:    []*identityInstance{staff(true)},
			wantRoutes:  switchAndFactor(),
			wantCases:   switchAndFactorCases,
			wantMethods: execute("EndHarborDirectoryOnly", "MakeHarborDirectoryOnly", "StopVouchingForHarborDirectorySecondFactor", "VouchForHarborDirectorySecondFactor"),
		},
		{
			name:        "a directories record with no default seat and no second-factor column has no role to set and no word to say",
			structs:     []string{"Harbor", "Breakwater"},
			auths:       []string{staffAuth},
			identity:    []*identityInstance{staff(false)},
			wantRoutes:  map[string][]*generatedRoute{defaultOutletName: slices.Concat(readUnder("api"), switchUnder("api")), "members": slices.Concat(readUnder("members"), switchUnder("members"))},
			wantCases:   slices.Concat(readCase("api"), switchCases("api"), readCase("members"), switchCases("members")),
			wantMethods: execute("EndHarborDirectoryOnly", "MakeHarborDirectoryOnly"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := generator(t, tt.auths, tt.identity, tt.structs...)
			if tt.structs[0] == "Sector" {
				// The Sector fixture record declares no switch; the default role's
				// methods are still served where its directories record is, and so is
				// the read of the tenant's sign-in settings.
				tt.wantRoutes = map[string][]*generatedRoute{}
				for _, prefix := range []string{"api", "members"} {
					name := prefix
					if prefix == "api" {
						name = defaultOutletName
					}
					routes := slices.Concat(readUnder(prefix), roleUnder(prefix), factorUnder(prefix))
					for _, route := range routes {
						route.HandlerFunc = strings.Replace(route.HandlerFunc, "Harbor", "Sector", 1)
						route.Path = strings.NewReplacer("harbors/{harborID}", r.domainRouteSegment+"/{"+r.domainRouteParam+"}", "-harbor-", "-sector-").Replace(route.Path)
						route.TestURL = strings.NewReplacer("-harbor-", "-sector-").Replace(route.TestURL)
					}
					tt.wantRoutes[name] = routes
				}
			}
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
			if tt.structs[0] != "Sector" {
				if diff := cmp.Diff(tt.wantRoutes, gotRoutes, cmp.AllowUnexported(generatedRoute{})); diff != "" {
					t.Errorf("registered routes mismatch (-want +got):\n%s", diff)
				}
			} else if got := handlers(gotRoutes[defaultOutletName]); len(gotRoutes) != 2 || !slices.Equal(got, []string{
				"TenantSignInSettings", "OpenSectorToDirectory", "MakeSectorInviteOnly", "VouchForSectorDirectorySecondFactor", "StopVouchingForSectorDirectorySecondFactor",
			}) {
				t.Errorf("registered routes = %v, want the read of the tenant's sign-in settings, the two methods of the default role and the two of the word for the second factor on both session outlets and nothing of a switch", got)
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
				for _, header := range c.Headers {
					line += " " + header.Name
				}
				gotCases = append(gotCases, line)
			}
			if tt.structs[0] != "Sector" {
				if diff := cmp.Diff(tt.wantCases, gotCases); diff != "" {
					t.Errorf("the matrix's cases mismatch (-want +got):\n%s", diff)
				}
			} else if len(gotCases) != 26 {
				t.Errorf("the matrix carries %d cases, want 26: the read of the tenant's sign-in settings, and each of the four methods plain, on a dry run and on a request for its warning, on both session outlets: %v", len(gotCases), gotCases)
			}

			b := resource.NewCollectionBuilder()
			if err := r.collectDirectoryOnlyRegistrations(b); err != nil {
				t.Fatalf("collectDirectoryOnlyRegistrations() error = %v", err)
			}
			var gotMethods []resource.CollectionResource
			for _, res := range b.Data().Resources {
				gotMethods = append(gotMethods, resource.CollectionResource{Name: res.Name, Scope: res.Scope, Permissions: res.Permissions, Classes: res.Classes})
			}
			slices.SortFunc(gotMethods, func(a, b resource.CollectionResource) int { return strings.Compare(string(a.Name), string(b.Name)) })
			if diff := cmp.Diff(tt.wantMethods, gotMethods); diff != "" {
				t.Errorf("the collection's registrations mismatch (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("an application's registration under a method's name is refused", func(t *testing.T) {
		t.Parallel()

		for name, want := range map[string]string{
			"EndHarborDirectoryOnly": "EndHarborDirectoryOnly is a method of the Harbor record's directory-only switch (@directoryOnly), which the generator writes",
			"OpenHarborToDirectory":  "OpenHarborToDirectory is a method of the HarborDirectory record's default role (@directoryDefaultSeat), which the generator writes",
			"VouchForHarborDirectorySecondFactor": "VouchForHarborDirectorySecondFactor is a method of the HarborDirectory record's word for its directory's second factor (@directorySecondFactor), " +
				"which the generator writes",
		} {
			r := generator(t, []string{staffAuth}, []*identityInstance{staff(false)}, "Harbor", "HarborDirectory")
			r.manualRegistrations = []ManualRegistration{{Resource: accesstypes.Resource(name), Permission: accesstypes.Execute}}
			err := r.collectDirectoryOnlyRegistrations(resource.NewCollectionBuilder())
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("collectDirectoryOnlyRegistrations() with %s registered error = %v, want the refusal naming the method and the record", name, err)
			}
		}
	})

	t.Run("a run that generates no routes registers no method", func(t *testing.T) {
		t.Parallel()

		r := generator(t, []string{staffAuth}, []*identityInstance{staff(false)}, "Harbor", "HarborDirectory")
		r.genRoutes = false
		b := resource.NewCollectionBuilder()
		if err := r.collectDirectoryOnlyRegistrations(b); err != nil {
			t.Fatalf("collectDirectoryOnlyRegistrations() error = %v", err)
		}
		if got := b.Data().Resources; len(got) != 0 {
			t.Errorf("the collection carries %v, want nothing", got)
		}
	})

	t.Run("the list the role is picked from is written for the auth that serves the methods, and for no other", func(t *testing.T) {
		t.Parallel()

		served := []string{defaultOutletName, "members", "machines"}
		tests := []struct {
			name  string
			auths []string
			inst  func() *identityInstance
			// outlets are the outlets the sources name on the directories record.
			outlets []string
			want    bool
		}{
			{name: "every session outlet that serves the directories record signs people in through the auth", auths: []string{staffAuth}, inst: func() *identityInstance { return staff(false) }, outlets: served, want: true},
			{name: "another auth signs people in on one of them", auths: []string{staffAuth, "example.com/app/pkg/auth/members"}, inst: func() *identityInstance { return staff(false) }, outlets: served},
			{name: "a directory writes the auth's roles", auths: []string{staffAuth}, inst: func() *identityInstance { return staff(true) }, outlets: served},
			{name: "no session outlet serves the directories record", auths: []string{staffAuth}, inst: func() *identityInstance { return staff(false) }, outlets: []string{"machines"}},
			{
				name: "the directories record carries no default seat", auths: []string{staffAuth}, outlets: served,
				inst: func() *identityInstance {
					inst := staff(false)
					inst.tenant.directorySeat = ""

					return inst
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				inst := tt.inst()
				inst.tenant.directoryOutlets = tt.outlets
				r := generator(t, tt.auths, []*identityInstance{inst}, "Harbor", "HarborDirectory")
				if got := r.identityOpensToDirectory(inst); got != tt.want {
					t.Errorf("identityOpensToDirectory() = %v, want %v", got, tt.want)
				}
				// The reading before the load agrees with where the methods are served
				// once the packages are loaded, and the generator holds the two together.
				serves := r.directoryRole() != nil
				if tt.outlets[0] == defaultOutletName && serves != tt.want {
					t.Errorf("directoryRole() serves the methods = %v, and the list is written = %v: the two agree", serves, tt.want)
				}
				inst.opensToDirectory = serves
				if err := r.holdIdentityDirectoryRole(); err != nil {
					t.Errorf("holdIdentityDirectoryRole() error = %v where the two readings agree", err)
				}
				inst.opensToDirectory = !serves
				if err := r.holdIdentityDirectoryRole(); err == nil || !strings.Contains(err.Error(), "fault of the generator") {
					t.Errorf("holdIdentityDirectoryRole() error = %v where the two readings differ, want the generator's fault", err)
				}
			})
		}
	})

	t.Run("the role is picked from the roles the caller may give, of the auth that judges it", func(t *testing.T) {
		t.Parallel()

		r := generator(t, []string{staffAuth}, []*identityInstance{staff(false)}, "Harbor", "HarborDirectory")
		if got, want := r.directoryRolePicker(), "StaffHarborDirectoryRoles"; got != want {
			t.Errorf("directoryRolePicker() = %q, want %q", got, want)
		}
		none := generator(t, nil, nil, "Harbor", "HarborDirectory")
		if got := none.directoryRolePicker(); got != "" {
			t.Errorf("directoryRolePicker() = %q where the program writes no method, want none", got)
		}
	})
}

// Test_directoryDefaultSeat_isOutputOnly pins that the directories record's own write
// paths never write the default seat: the field decodes output-only, so neither the
// record's create nor its update takes it from the wire, and a grant of Update on it
// writes nothing. The tenant's own methods set and clear it. The record still shows it.
func Test_directoryDefaultSeat_isOutputOnly(t *testing.T) {
	t.Parallel()

	r, err := declarationResources(t, "Harbor", "HarborDirectory")
	if err != nil {
		t.Fatalf("declarationResources() error = %v", err)
	}
	field := r.tenantRecord().declaration.Directories.DirectoryDefaultSeatField
	if field == nil {
		t.Fatal("the fixture's directories record carries no default seat field")
	}
	if !field.IsOutputOnly() {
		t.Errorf("the default seat's field %s is not output only: the record's own update could write it, past the warning and the rule of who may give the role", field.Name())
	}
	if got := reflect.StructTag(field.JSONTagForPatch()).Get(jsonTagKey); got != "-" {
		t.Errorf("the default seat's field %s is %q in a create or update request, want \"-\": the wire must not write it", field.Name(), got)
	}
	if got := reflect.StructTag(field.JSONTag()).Get(jsonTagKey); got != "defaultSeat" {
		t.Errorf("the default seat's field %s reads as %q, want defaultSeat: the record still shows the role", field.Name(), got)
	}
	if got, want := r.declarationColumns(r.tenantRecord()).DirectoriesKeyColumn, "Id"; got != want {
		t.Errorf("the declaration's directories key column = %q, want %q: the methods write the tenant's row through it", got, want)
	}
}

// Test_directoryOnlyClient pins the tenant's own methods on a generated browser client.
// The browser generator reads each struct on its own and resolves no declaration across
// them, so it finds each method by the record's own fields and by the collection the
// handler generator computed: on the client of an outlet that serves the record, in the
// tenant's scope, the methods that warn with the word a person types and the one that
// opens the tenant with its role, picked from the list the handler generator chose, and
// the two of the tenant's word for its directory's second factor where the directories
// record carries the column; on no client where the application serves no such method,
// and left out of the constants of an outlet the record is not on.
func Test_directoryOnlyClient(t *testing.T) {
	t.Parallel()

	const staffAuth = "example.com/app/pkg/auth/staff"
	// The handler generator's run over the record and its directories record: the
	// collection carries the methods.
	r, err := declarationResources(t, "Harbor", "HarborDirectory")
	if err != nil {
		t.Fatalf("declarationResources() error = %v", err)
	}
	r.genRoutes = true
	r.defaultOutlet = routerOutlet{name: defaultOutletName, prefix: "api", servesSessions: true, auth: &outletAuth{importPath: staffAuth}}
	r.identity = []*identityInstance{{
		importPath: staffAuth, auth: "Staff", tenant: &identityTenant{name: "Harbor", table: "Harbors", key: "Id", word: "harbor", directorySeat: "DefaultSeat"},
		tables: map[identityTable]bool{identityUsers: true, identityMemberships: true},
	}}
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

	const (
		makeDoc   = "turns the harbor's directory-only switch on, which closes the harbor to every sign-in but one through its directory and its own administrators' password with a code sent to their email; the server refuses it until the harbor has its directory, unless the caller is signed in through it, and until the caller has a password where their auth has passwords, in a sentence the person can act on"
		endDoc    = "turns the harbor's directory-only switch off: every sign-in is admitted again"
		openDoc   = "opens the harbor to its directory's people: each of them is given the role at their first sign-in through the directory, with no invitation"
		inviteDoc = "makes the harbor invite only for its directory's people: a person from the directory enters once they are invited"
		vouchDoc  = "says the harbor's directory asks its people for a second factor of its own, so a person who signs in through it is not asked for the application's mailed code; " +
			"the person calling is signed in through that directory, and the method warns first"
		stopDoc = "takes back the harbor's word that its directory asks for a second factor: the people who sign in through it are asked for the mailed code again from their next sign-in"
	)
	all := func(picker string) []*tsTenantOwnMethod {
		return []*tsTenantOwnMethod{
			{Name: "MakeHarborDirectoryOnly", Typed: "close", Doc: makeDoc},
			{Name: "EndHarborDirectoryOnly", Doc: endDoc},
			{Name: "OpenHarborToDirectory", Typed: "open", Doc: openDoc, Fields: []tsTenantOwnField{{Name: "role", Enumerated: picker}}},
			{Name: "MakeHarborInviteOnly", Doc: inviteDoc},
			{Name: "VouchForHarborDirectorySecondFactor", Typed: "vouch", Doc: vouchDoc},
			{Name: "StopVouchingForHarborDirectorySecondFactor", Doc: stopDoc},
		}
	}
	tests := []struct {
		name       string
		collection resource.CollectionData
		outlet     string
		picker     string
		want       []*tsTenantOwnMethod
		wantMeta   []string
	}{
		{
			name: "the client of the outlet that serves the record", collection: served.Data(), picker: "StaffHarborDirectoryRoles", want: all("StaffHarborDirectoryRoles"),
			wantMeta: []string{
				"export interface MakeHarborDirectoryOnlyConfig {}\nexport interface MakeHarborDirectoryOnly {}",
				"export interface EndHarborDirectoryOnlyConfig {}\nexport interface EndHarborDirectoryOnly {}",
				"export interface OpenHarborToDirectoryConfig {\n  role: string | FieldPointer;\n}",
				"export interface OpenHarborToDirectory {\n  role: string;\n}",
				"export interface MakeHarborInviteOnlyConfig {}\nexport interface MakeHarborInviteOnly {}",
				"[Methods.MakeHarborDirectoryOnly]: {\n    route: 'make-harbor-directory-only',\n    dryRun: true,\n    warning: { typed: 'close' },\n    fields: [],\n  },",
				"[Methods.EndHarborDirectoryOnly]: {\n    route: 'end-harbor-directory-only',\n    dryRun: true,\n    fields: [],\n  },",
				"[Methods.OpenHarborToDirectory]: {\n    route: 'open-harbor-to-directory',\n    dryRun: true,\n    warning: { typed: 'open' },\n    fields: [\n      { fieldName: 'role', displayType: 'enumerated', enumeratedResource: Resources.StaffHarborDirectoryRoles, required: true },\n    ],\n  },",
				"[Methods.MakeHarborInviteOnly]: {\n    route: 'make-harbor-invite-only',\n    dryRun: true,\n    fields: [],\n  },",
				"export interface VouchForHarborDirectorySecondFactorConfig {}\nexport interface VouchForHarborDirectorySecondFactor {}",
				"export interface StopVouchingForHarborDirectorySecondFactorConfig {}\nexport interface StopVouchingForHarborDirectorySecondFactor {}",
				"[Methods.VouchForHarborDirectorySecondFactor]: {\n    route: 'vouch-for-harbor-directory-second-factor',\n    dryRun: true,\n    warning: { typed: 'vouch' },\n    fields: [],\n  },",
				"[Methods.StopVouchingForHarborDirectorySecondFactor]: {\n    route: 'stop-vouching-for-harbor-directory-second-factor',\n    dryRun: true,\n    fields: [],\n  },",
				"import { Methods, Resources } from './zz_gen_constants';",
			},
		},
		{
			name: "a generator built on its own knows no list to pick the role from, and takes it as text", collection: served.Data(), want: all(""),
			wantMeta: []string{
				"[Methods.OpenHarborToDirectory]: {\n    route: 'open-harbor-to-directory',\n    dryRun: true,\n    warning: { typed: 'open' },\n    fields: [\n      { fieldName: 'role', displayType: 'string', required: true },\n    ],\n  },",
				"import { Methods } from './zz_gen_constants';",
			},
		},
		{name: "an application that serves no such method", collection: resource.NewCollectionBuilder().Data()},
		{name: "the client of an outlet the record is not on", collection: served.Data(), outlet: "members"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gen := &typescriptGenerator{client: parsed(t), rc: resource.MustNewGeneratedCollection(tt.collection), outletName: tt.outlet, directoryRolePicker: tt.picker}
			// The filter narrows the parsed set to the outlet's members, as a run does
			// before it writes the client.
			filtered, _, err := gen.applyOutletFilter(slices.Clone(gen.resources), nil)
			if err != nil {
				t.Fatalf("applyOutletFilter() error = %v", err)
			}
			gen.resources = filtered
			got := gen.tenantOwnClientData()
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Fatalf("tenantOwnClientData() mismatch (-want +got):\n%s", diff)
			}
			for _, method := range []string{
				"MakeHarborDirectoryOnly", "EndHarborDirectoryOnly", "OpenHarborToDirectory", "MakeHarborInviteOnly",
				"VouchForHarborDirectorySecondFactor", "StopVouchingForHarborDirectorySecondFactor",
			} {
				excluded := slices.Contains(gen.outletExcluded, accesstypes.Resource(method))
				if wantExcluded := tt.outlet != ""; excluded != wantExcluded {
					t.Errorf("%s is left out of the outlet's constants = %t, want %t", method, excluded, wantExcluded)
				}
			}
			if tt.want == nil {
				return
			}

			methods, err := gen.generateTemplateOutput(typescriptMethodsTemplate, typescriptMethodsTemplate, tsMethodsData{File: gen, GenPrefix: "zz_gen", TenantOwn: got})
			if err != nil {
				t.Fatalf("generateTemplateOutput(methods) error = %v", err)
			}
			assertContains(t, "typescriptMethodsTemplate", string(methods), tt.wantMeta, nil)
			want := &tsAPIMethod{Name: "OpenHarborToDirectory", Property: "openHarborToDirectory", Route: "open-harbor-to-directory", Scope: accesstypes.DomainPermissionScope}
			if diff := cmp.Diff(want, tenantOwnAPIMethod("OpenHarborToDirectory")); diff != "" {
				t.Errorf("tenantOwnAPIMethod() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Test_tenantDirectoryIn pins the reading of the directory-only switch and the
// directories record off a package's syntax, before the packages are loaded: the
// columns behind the annotated fields, and nothing where the record declares no switch,
// the package no directories record, or a column cannot be read; and the directories
// record's default seat column, which is read with or without the switch.
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
	const directoriesSeated = `// HarborDirectory is the directories record.
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
		// Berth is the role everyone from the directory gets.
		// @directoryDefaultSeat
		Berth *string ` + "`spanner:\"DefaultSeat\"`" + `
	}
`
	tests := []struct {
		name    string
		structs []string
		want    *identityTenantDirectory
		// wantSeat is the directories record's default seat column, which is read
		// whether the record declares the switch or not.
		wantSeat string
		// wantOutlets are the outlets the directories record is served on, the default
		// outlet where it names none and nil where the package declares no such record.
		wantOutlets []string
		// wantRecord is the directories record's struct name, read whether the record
		// declares the switch or not, "" where the package declares no such record.
		wantRecord string
	}{
		{
			name:        "the switch's column and the directories record's table and columns",
			structs:     []string{record, directories},
			want:        &identityTenantDirectory{OnlyColumn: "DirectoryOnly", Table: "HarborDirectories", TenantColumn: "HarborId", MethodColumn: "Method", ConnectionColumn: "Connection"},
			wantOutlets: []string{defaultOutletName},
			wantRecord:  "HarborDirectory",
		},
		{
			name:        "the default seat column of a directories record that carries one, and the outlets it names",
			structs:     []string{record, strings.Replace(directoriesSeated, "// @tenantDirectory", "// @tenantDirectory\n\t// @outlet(console, portal)", 1)},
			want:        &identityTenantDirectory{OnlyColumn: "DirectoryOnly", Table: "HarborDirectories", TenantColumn: "HarborId", MethodColumn: "Method", ConnectionColumn: "Connection"},
			wantSeat:    "DefaultSeat",
			wantOutlets: []string{"console", "portal"},
			wantRecord:  "HarborDirectory",
		},
		{name: "a record that declares no switch", structs: []string{recordWithoutSwitch, directories}, wantOutlets: []string{defaultOutletName}, wantRecord: "HarborDirectory"},
		{
			name:    "a record that declares no switch keeps its directories record's default seat",
			structs: []string{recordWithoutSwitch, directoriesSeated}, wantSeat: "DefaultSeat", wantOutlets: []string{defaultOutletName}, wantRecord: "HarborDirectory",
		},
		{name: "a package with no directories record", structs: []string{record}},
		{name: "a column the syntax does not name", structs: []string{record, directoriesUntagged}, wantOutlets: []string{defaultOutletName}, wantRecord: "HarborDirectory"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			source := "package resources\n\ntype (\n\t" + strings.Join(tt.structs, "\n\t") + ")\n"
			if err := os.WriteFile(filepath.Join(dir, "resources.go"), []byte(source), 0o600); err != nil {
				t.Fatalf("os.WriteFile() error = %v", err)
			}
			got, directoriesRecord, seat, outlets, err := tenantDirectoryIn(dir, "Harbor", func(name string) string {
				return strings.TrimSuffix(name, "y") + "ies"
			})
			if err != nil {
				t.Fatalf("tenantDirectoryIn() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("tenantDirectoryIn() mismatch (-want +got):\n%s", diff)
			}
			if seat != tt.wantSeat {
				t.Errorf("tenantDirectoryIn() default seat column = %q, want %q", seat, tt.wantSeat)
			}
			if directoriesRecord != tt.wantRecord {
				t.Errorf("tenantDirectoryIn() directories record = %q, want %q", directoriesRecord, tt.wantRecord)
			}
			if diff := cmp.Diff(tt.wantOutlets, outlets); diff != "" {
				t.Errorf("tenantDirectoryIn() outlets of the directories record (-want +got):\n%s", diff)
			}
		})
	}
}

// Test_identityInstance_roleGrantsOfADirectorySeat pins who is handed the role file's
// answers on giving a role where no group and no default role asks for them: the auth
// whose roles in a tenant are given by hand. The role a tenant is opened to its
// directory's people with is judged by those answers, in an application whose
// directories record carries the default seat, and with or without one the list a
// tenant's role is picked from asks them which roles the caller could give. An auth
// whose directory writes its roles judges none, and neither does one with no
// memberships or no tenant record.
func Test_identityInstance_roleGrantsOfADirectorySeat(t *testing.T) {
	t.Parallel()

	seated := &identityTenant{name: "Harbor", table: "Harbors", key: "Id", outlets: []string{"default"}, directorySeat: "DefaultSeat"}
	unseated := &identityTenant{name: "Harbor", table: "Harbors", key: "Id", outlets: []string{"default"}}
	tests := []struct {
		name        string
		tenant      *identityTenant
		tables      []identityTable
		rolesSynced bool
		want        bool
	}{
		{name: "roles given by hand, and a default seat", tenant: seated, tables: []identityTable{identityUsers, identityMemberships}, want: true},
		{name: "no default seat: the list a tenant's role is picked from asks", tenant: unseated, tables: []identityTable{identityUsers, identityMemberships}, want: true},
		{name: "roles a directory writes: nobody gives one by hand", tenant: seated, tables: []identityTable{identityUsers, identityMemberships}, rolesSynced: true},
		{name: "no memberships: nobody holds a role", tenant: seated, tables: []identityTable{identityUsers}},
		// The whole application's role fields pick from the roles the caller could give,
		// with tenants or without.
		{name: "no tenant record: the list the whole application's roles are picked from asks", tables: []identityTable{identityUsers, identityMemberships}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			inst := &identityInstance{auth: "Crew", tenant: tt.tenant, rolesSynced: tt.rolesSynced, tables: make(map[identityTable]bool)}
			for _, kind := range tt.tables {
				inst.tables[kind] = true
			}
			if got := inst.judgesRoleChanges(); got != tt.want {
				t.Errorf("judgesRoleChanges() = %v, want %v", got, tt.want)
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
	withPassword := func(d *identitySourceData) *identitySourceData {
		d.password = true

		return d
	}
	withGroups := func(d *identitySourceData) *identitySourceData {
		d.groupSet = "crewGroups"
		d.tables[identityGroups], d.tables[identityGroupMembers], d.tables[identityGroupRoles] = true, true, true
		d.Policy.Groups, d.Policy.GroupMembers, d.Policy.GroupRoles = "CrewGroups", "CrewGroupMembers", "CrewGroupRoles"

		return d
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
				// The super administrator is the person who holds the role now: the
				// membership counts inside its period alone, as the sign-in rule reads it.
				"WHERE sa.UserId = u.Id AND sa.Kind = 'global' AND sa.Role = 'SuperAdministrator'\n      AND (sa.StartsAt IS NULL OR sa.StartsAt <= CURRENT_TIMESTAMP()) AND (sa.EndsAt IS NULL OR sa.EndsAt > CURRENT_TIMESTAMP())\n  )",
				") AS OutsideDirectory,",
				"IF(ts.DirectoryOnly AND td.SectorId IS NOT NULL",
				// The sentence speaks of the tenant's directory and never says its connection.
				`'Has not signed in through this sector\'s directory, so cannot enter while this sector is directory only',`,
				"NULL) AS CannotEnter",
			},
			// A connection is a key, for a Microsoft Entra tenant an id: no sentence is made of it.
			wantNotContains: []string{"CONCAT(", "through ', td.Connection"},
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
			name: "an auth with no password method keeps nobody's way in: the list reads no password and no administrator",
			data: data(directory, identityUsers, identityMemberships, identityIdentities),
			wantContains: []string{
				"return crewSectorUserQuery, nil",
			},
			wantNotContains: []string{"PasswordHash", "tenantAdministratorRoles", "AdministratorRoles(", "sync/atomic", "AdministratorWithPassword"},
		},
		{
			name: "an auth with the password method leaves the tenant's own administrator who has a password out of the people the switch keeps out",
			data: withPassword(data(directory, identityUsers, identityMemberships, identityIdentities)),
			wantContains: []string{
				// The person has a password, and holds one of the engine's roles in the
				// tenant itself, by a membership of their own, inside its period.
				"  ) AND NOT (u.PasswordHash IS NOT NULL AND (EXISTS (\n    SELECT 1 FROM CrewUserRoles ta\n    WHERE ta.UserId = u.Id AND ta.Kind = 'domain' AND ta.Axis = '' AND ta.DomainId = t.DomainId\n      AND ta.Role IN UNNEST(@tenantAdministratorRoles)\n      AND (ta.StartsAt IS NULL OR ta.StartsAt <= CURRENT_TIMESTAMP()) AND (ta.EndsAt IS NULL OR ta.EndsAt > CURRENT_TIMESTAMP())\n  )))) AS OutsideDirectory,",
				// The sentence follows the same fact.
				"  ))),\n    'Has not signed in through this sector\\'s directory, so cannot enter while this sector is directory only',",
				// The roles are the engine's, handed in by the client and read as a parameter.
				"var crewSectorUserAdministrators atomic.Pointer[[]string]",
				"func SetCrewSectorUserAdministratorRoles(roles []accesstypes.Role) {",
				"return crewSectorUserQuery, map[string]any{\"tenantAdministratorRoles\": administrators}",
				"administrators := []string{}",
				// The same fact is a column of its own, which the warning that turns the
				// switch on reads the administrators who keep their way in by.
				"AdministratorWithPassword bool `spanner:\"AdministratorWithPassword\" index:\"true\"`",
				"NULL) AS CannotEnter,\n  (u.PasswordHash IS NOT NULL AND (EXISTS (\n    SELECT 1 FROM CrewUserRoles ta\n    WHERE ta.UserId = u.Id AND ta.Kind = 'domain' AND ta.Axis = '' AND ta.DomainId = t.DomainId\n      AND ta.Role IN UNNEST(@tenantAdministratorRoles)\n      AND (ta.StartsAt IS NULL OR ta.StartsAt <= CURRENT_TIMESTAMP()) AND (ta.EndsAt IS NULL OR ta.EndsAt > CURRENT_TIMESTAMP())\n  ))) AS AdministratorWithPassword\n",
			},
			// An auth that keeps no groups reads none.
			wantNotContains: []string{"GroupRoles", "return crewSectorUserQuery, nil"},
		},
		{
			name: "an auth that keeps groups counts the tenant's own group, and no other",
			data: withGroups(withPassword(data(directory, identityUsers, identityMemberships, identityIdentities))),
			wantContains: []string{
				"  ) OR EXISTS (\n    SELECT 1 FROM CrewGroupRoles tr\n    JOIN CrewGroups tg ON tg.Id = tr.GroupId\n    JOIN CrewGroupMembers tm ON tm.GroupId = tr.GroupId\n    WHERE tm.UserId = u.Id\n      AND tg.Kind = 'domain' AND tg.Axis = '' AND tg.DomainId = t.DomainId\n      AND tr.Kind = 'domain' AND tr.Axis = '' AND tr.DomainId = t.DomainId\n      AND tr.Role IN UNNEST(@tenantAdministratorRoles)\n      AND (tr.StartsAt IS NULL OR tr.StartsAt <= CURRENT_TIMESTAMP()) AND (tr.EndsAt IS NULL OR tr.EndsAt > CURRENT_TIMESTAMP())\n      AND (tm.StartsAt IS NULL OR tm.StartsAt <= CURRENT_TIMESTAMP()) AND (tm.EndsAt IS NULL OR tm.EndsAt > CURRENT_TIMESTAMP())\n  )))) AS OutsideDirectory,",
				// The column reads the tenant's own group too.
				"      AND (tm.StartsAt IS NULL OR tm.StartsAt <= CURRENT_TIMESTAMP()) AND (tm.EndsAt IS NULL OR tm.EndsAt > CURRENT_TIMESTAMP())\n  ))) AS AdministratorWithPassword\n",
			},
		},
		{
			name:            "a tenant record that declares no switch",
			data:            withPassword(data(nil, identityUsers, identityMemberships, identityIdentities)),
			wantContains:    []string{"HeldGlobally bool ", "return crewSectorUserQuery, nil"},
			wantNotContains: []string{"OutsideDirectory", "CannotEnter", " ts ON ", " td ON ", "PasswordHash", "tenantAdministratorRoles", "AdministratorWithPassword"},
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
	// It reads the invitations that would make another administrator where the record
	// declares the switch, the auth has the password method and lists its people and its
	// invitations, and not otherwise.
	if invites := withPassword(data(directory, identityUsers, identityMemberships, identityMailLinks)); !invites.ListsDirectoryInvitations() {
		t.Error("ListsDirectoryInvitations() = false for an auth with the password method that lists its people and its invitations")
	}
	for name, without := range map[string]*identitySourceData{
		"without the invitations list": withPassword(data(directory, identityUsers, identityMemberships)),
		"without the password method":  data(directory, identityUsers, identityMemberships, identityMailLinks),
		"without the switch":           withPassword(data(nil, identityUsers, identityMemberships, identityMailLinks)),
	} {
		if without.ListsDirectoryInvitations() {
			t.Errorf("ListsDirectoryInvitations() = true %s", name)
		}
	}
	for _, want := range []string{
		"func (c *Client) DirectoryOutsiders(ctx context.Context, txn resource.ReadWriteTransaction, tenant accesstypes.Domain) ([]resource.DirectoryOutsider, error) {",
		"QueryClause().DomainID().Equal(string(tenant)).And().OutsideDirectory().Equal(true))",
		// The read of the switch counts the tenant's people over the same list, by its
		// rows for the tenant.
		"func (c *Client) SeatedCount(ctx context.Context, txn resource.ReadWriteTransaction, tenant accesstypes.Domain) (int, error) {",
		"Columns().ID()).\n\t\tWhere(",
		"QueryClause().DomainID().Equal(string(tenant)))\n\tseated := 0",
		"func (c *Client) DirectoryAdministrators(ctx context.Context, txn resource.ReadWriteTransaction, tenant accesstypes.Domain) ([]resource.DirectoryAdministrator, error) {",
		"QueryClause().DomainID().Equal(string(tenant)).And().AdministratorWithPassword().Equal(true))",
		"func (c *Client) DirectoryInvitations(ctx context.Context, txn resource.ReadWriteTransaction, tenant accesstypes.Domain) ([]resource.DirectoryInvitation, error) {",
		"QueryClause().DomainID().Equal(string(tenant)).And().Pending().Equal(true))",
		"for _, role := range c.manager.TenantAdministratorRoles() {",
	} {
		if !strings.Contains(identityStoreTemplate, want) {
			t.Errorf("identityStoreTemplate is missing %q", want)
		}
	}

	// The client hands the list the engine's roles where the list reads them, answers
	// the two questions the frame of the switch asks every auth's client, and answers
	// what the auth's sign-in settings ask of a second factor, which the rules of the
	// tenant's word for its directory's second factor ask.
	for _, tt := range []struct {
		name            string
		data            *identitySourceData
		wantContains    []string
		wantNotContains []string
	}{
		{
			name: "the client of an auth with the password method tells the list who administers",
			data: withPassword(data(directory, identityUsers, identityMemberships, identityIdentities)),
			wantContains: []string{
				"crewresources.SetCrewSectorUserAdministratorRoles(manager.TenantAdministratorRoles())",
				"crewresources.SetCrewSessionUserAdministratorRoles(manager.TenantAdministratorRoles())",
				"func (c *Client) CallerPassword(ctx context.Context, txn resource.ReadWriteTransaction) (has, applies bool, err error) {",
				"func (c *Client) AdministratorsEnterWithPassword() bool {",
				"func (c *Client) AuthSecondFactor(ctx context.Context, txn resource.ReadWriteTransaction) (required, applies bool, err error) {",
				"required, applies, err = c.manager.AuthSecondFactor(ctx, txn)",
			},
		},
		{
			name: "the client of an auth with no password method tells the people's list nothing, tells the rows who administers, and still answers the frame",
			data: data(directory, identityUsers, identityMemberships, identityIdentities),
			wantContains: []string{
				// A row offers the methods that change how a person signs in by the
				// engine's names whatever the auth's sign-ins (@callerEnters).
				"crewresources.SetCrewSessionUserAdministratorRoles(manager.TenantAdministratorRoles())",
				"func (c *Client) CallerPassword(ctx context.Context, txn resource.ReadWriteTransaction) (has, applies bool, err error) {",
				"func (c *Client) AdministratorsEnterWithPassword() bool {",
				"func (c *Client) AuthSecondFactor(ctx context.Context, txn resource.ReadWriteTransaction) (required, applies bool, err error) {",
				"required, applies, err = c.manager.AuthSecondFactor(ctx, txn)",
			},
			wantNotContains: []string{"SetCrewSectorUserAdministratorRoles(manager"},
		},
		{
			name:            "the client of an application whose tenant record declares no switch tells nobody who administers",
			data:            withPassword(data(nil, identityUsers, identityMemberships, identityIdentities)),
			wantNotContains: []string{"AdministratorRoles(manager"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &client{}
			out, err := c.generateTemplateOutput("identityClientTemplate", identityClientTemplate, tt.data)
			if err != nil {
				t.Fatalf("generateTemplateOutput() error = %v", err)
			}
			assertContains(t, "the client", string(out), tt.wantContains, tt.wantNotContains)
		})
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
		// seat is the default seat column the sources were written for: the fixture's
		// directories record carries DefaultSeat, and its Breakwater none.
		seat    string
		wantErr string
	}{
		{name: "the same switch and directories record", structs: []string{"Harbor", "HarborDirectory"}, record: "Harbor", written: harbor, seat: "DefaultSeat"},
		{name: "no switch either way", structs: []string{"Sector", "HarborDirectory"}, record: "Sector", seat: "DefaultSeat"},
		{
			name:    "written for a switch the load does not find",
			structs: []string{"Sector", "HarborDirectory"},
			record:  "Sector",
			written: harbor,
			seat:    "DefaultSeat",
			wantErr: "was written for the switch DirectoryOnly over the directories table HarborDirectories (HarborId, Method, Connection)",
		},
		{
			name:    "written for none, and the load finds a switch",
			structs: []string{"Harbor", "HarborDirectory"},
			record:  "Harbor",
			seat:    "DefaultSeat",
			wantErr: "was written for no directory-only switch",
		},
		{
			name:    "written for another column",
			structs: []string{"Harbor", "HarborDirectory"},
			record:  "Harbor",
			written: &identityTenantDirectory{OnlyColumn: "Closed", Table: "HarborDirectories", TenantColumn: "HarborId", MethodColumn: "Method", ConnectionColumn: "Connection"},
			seat:    "DefaultSeat",
			wantErr: "was written for the switch Closed over the directories table HarborDirectories",
		},
		{
			name:    "written for no default seat, and the load finds one",
			structs: []string{"Harbor", "HarborDirectory"},
			record:  "Harbor",
			written: harbor,
			wantErr: `the role grants were written for the directories record's default seat column "",`,
		},
		{
			name:    "written for a default seat the load does not find",
			structs: []string{"Harbor", "Breakwater"},
			record:  "Harbor",
			written: harbor,
			seat:    "DefaultSeat",
			wantErr: `the role grants were written for the directories record's default seat column "DefaultSeat",`,
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
			r.identity = []*identityInstance{{auth: "Crew", tenant: &identityTenant{name: tt.record, directory: tt.written, directorySeat: tt.seat}}}

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
