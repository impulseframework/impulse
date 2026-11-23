package transition

import (
	"context"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton"
)

// The files each start writes for the sign-in it has, and the base's tests each takes out.
var (
	directoryRolesWritten = []string{
		"test/integration/harness_test.go",
		"test/integration/login_test.go",
		"test/integration/second_factor_test.go",
		"test/integration/sign_in_skipauth_test.go",
		"test/integration/sign_in_test.go",
	}
	byDirectoryWritten = []string{
		"test/authz/harness_test.go",
		"test/integration/founded_tenant_test.go",
		"test/integration/grants_test.go",
		"test/integration/groups_test.go",
		"test/integration/harness_test.go",
		"test/integration/invited_administrator_test.go",
		"test/integration/login_test.go",
		"test/integration/second_factor_test.go",
		"test/integration/sign_in_skipauth_test.go",
		"test/integration/sign_in_test.go",
	}
)

// withLoginPageSpec is the Go tests a start with no password sign-in writes and, after
// them, the spec of the browser application's login page.
func withLoginPageSpec(tests []string) []string {
	return append(slices.Clone(tests), loginPageSpec)
}

// checkWritten holds each file a start wrote to being the application's own Go: it
// parses, it names the application's module and never the candidate's, and it posts no
// password.
func checkWritten(t *testing.T, a *app.App, files []string) {
	t.Helper()

	candidate, err := baseModulePath()
	if err != nil {
		t.Fatalf("baseModulePath() error = %v", err)
	}
	for _, rel := range files {
		if !exists(a, rel) {
			t.Errorf("%s was not written", rel)

			continue
		}
		src := read(t, a, rel)
		if _, err := parser.ParseFile(token.NewFileSet(), rel, src, parser.ParseComments); err != nil {
			t.Errorf("%s does not parse: %v", rel, err)
		}
		for _, stale := range []string{candidate, "[[", "/api/user/password/login", "adminPassword"} {
			if strings.Contains(src, stale) {
				t.Errorf("%s contains %q", rel, stale)
			}
		}
		if strings.Contains(src, "example.com/") && !strings.Contains(src, "example.com/acme/depot/") {
			t.Errorf("%s imports nothing of the application's module", rel)
		}
	}
}

// TestDirectoryStart_Apply_directoryRolesSuites holds the start of Directory roles to
// writing the application's tests for the sign-in it has: the served suites sign in
// through the simulated directory, the base's tests of what the auth does not have are
// gone, and the deployment's tests name no foothold.
func TestDirectoryStart_Apply_directoryRolesSuites(t *testing.T) {
	t.Parallel()

	start := directoryRolesStart(app.MethodGoogle, "fleet.example")
	a, ch := started(t, &start)

	t.Run("the served suites are written for the directory", func(t *testing.T) {
		t.Parallel()

		checkWritten(t, a, directoryRolesWritten)
		for rel, wants := range map[string][]string{
			"test/integration/harness_test.go":          {"Google: staff.GoogleSettings{HostedDomain: organization, RedirectURL:", `GroupPrefix: "staff-"`, "httptest.NewUnstartedServer(nil)"},
			"test/integration/sign_in_skipauth_test.go": {"//go:build skipAuth\n", `"APP_GOOGLE_USERNAME"`, `"APP_GOOGLE_ROLES"`, `"/api/user/google/login?returnUrl="`},
			"test/integration/sign_in_test.go":          {"//go:build !skipAuth\n", "b.t.Skip("},
			"test/integration/login_test.go":            {"func TestLogin(t *testing.T) {", "RefusedNoRoles"},
			"test/integration/second_factor_test.go":    {"func TestSecondFactor(t *testing.T) {"},
		} {
			src := read(t, a, rel)
			for _, want := range wants {
				if !strings.Contains(src, want) {
					t.Errorf("%s does not contain %q", rel, want)
				}
			}
		}
	})

	t.Run("the base's tests of what the auth does not have are taken out, each with the reason", func(t *testing.T) {
		t.Parallel()

		for _, suite := range directoryRolesRemoved {
			if exists(a, suite.file) {
				t.Errorf("%s is still there", suite.file)
			}
			if got := linesWith(ch.Did, "the base's tests of what the staff auth does not have were taken out", []string{suite.file + " (" + suite.why + ")"}); len(got) != 1 {
				t.Errorf("%s: the change does not say once that it was taken out and why: %q", suite.file, got)
			}
		}
	})

	t.Run("the deployment's tests name no foothold", func(t *testing.T) {
		t.Parallel()

		deploy := read(t, a, deployTest)
		for _, gone := range []string{"FirstAdministratorRoles", "AdministratorRoles", "\"slices\""} {
			if strings.Contains(deploy, gone) {
				t.Errorf("%s still contains %q", deployTest, gone)
			}
		}
		if !strings.Contains(deploy, "func TestRoles_validateAgainstTheCollection(") {
			t.Errorf("%s lost the test that validates the role file", deployTest)
		}
		migrate := read(t, a, migrateTest)
		for _, gone := range []string{"AdministratorVariable", "withHolder", "withUser", "noMailbox", "name one of staff", "accesstypes", "no auth of that name invites", "goes with no other flag\",\n\t\t},\n\t\t{\n\t\t\tname:     \"a fresh invitation", "flag needs an argument: -reinvite-administrator"} {
			if strings.Contains(migrate, gone) {
				t.Errorf("%s still contains %q", migrateTest, gone)
			}
		}
		for _, want := range []string{
			`t.Setenv("APP_STAFF_GOOGLE_HOSTED_DOMAIN", "fleet.example")`,
			`t.Setenv("APP_STAFF_GOOGLE_REDIRECT_URL", "http://solo.test/api/user/google/callback")`,
			`t.Setenv("APP_STAFF_GOOGLE_GROUP_PREFIX", "staff-")`,
			`name:     "a fresh invitation is no flag of the command: nobody is invited to the staff auth",`,
			`wantErr:  "flag provided but not defined: -reinvite-administrator",`,
			"the migrations with the seed, the report on a fresh, a migrated and a dirty\n",
			"// release invites no first administrator and asks for no mailbox on that account, and\n// the command has no -reinvite-administrator flag.",
			"apply every schema and data migration and invite nobody",
			"func TestRetirementProblem(",
		} {
			if !strings.Contains(migrate, want) {
				t.Errorf("%s does not contain %q", migrateTest, want)
			}
		}
		for _, rel := range []string{deployTest, migrateTest} {
			if _, err := parser.ParseFile(token.NewFileSet(), rel, read(t, a, rel), parser.ParseComments); err != nil {
				t.Errorf("%s does not parse: %v", rel, err)
			}
		}
	})

	t.Run("the change leaves the author the tests it did not write, and no other", func(t *testing.T) {
		t.Parallel()

		for _, suite := range passwordSuites {
			if named := leftLine(ch, suite.file+" "); len(named) != 0 {
				t.Errorf("%s is among what the change leaves, and the start settled it: %q", suite.file, named)
			}
		}
		if len(ch.Left) != 0 || ch.LeftNote != "" {
			t.Errorf("the change leaves the author %q under %q, and the start wrote every test for the sign-in the auth has", ch.Left, ch.LeftNote)
		}
		if got := skippedLine(ch, integrationHarness+" and "+migrateTest+": the google registration validates"); len(got) != 0 {
			t.Errorf("the change hands on the settings of a method, and the tests it wrote already give them: %q", got)
		}
		if got := linesWith(ch.Did, migrateTest+": the cases that invite or reinvite a first administrator were taken out", []string{"two cases prove that a release invites nobody and that the command has no flag for a fresh invitation"}); len(got) != 1 {
			t.Errorf("the change does not say once what became of the migrate command's test: %q", ch.Did)
		}
		got := start.MeaningIn(a)
		for _, kept := range []string{"Leave what invites a first administrator", "kept as it is", "names no auth", "It stays because"} {
			if strings.Contains(got, kept) {
				t.Errorf("the start's account still says %q, and what invites a first administrator was taken out", kept)
			}
		}
		if !strings.Contains(got, "The deployment invites no first administrator for it: nothing in the deployment package, the migrate command or the data level speaks of one, and `impulse add auth` writes the invitation for an auth added later that invites one.") {
			t.Errorf("the start's account does not say that nothing in the application invites a first administrator:\n%s", got)
		}
		if got := start.MeaningIn(a); !strings.Contains(got, "1. Read the tests the start wrote.") {
			t.Error("the start's account of what is left does not say the tests were written")
		}
	})
}

// TestDirectoryStart_Apply_directoryRolesSuitesMicrosoft holds the Microsoft form of the
// same tests: the method's own settings, variables and routes.
func TestDirectoryStart_Apply_directoryRolesSuitesMicrosoft(t *testing.T) {
	t.Parallel()

	start := directoryRolesStart(app.MethodAzure, "3f2504e0-4f89-11d3-9a0c-0305e82c3301")
	a, _ := started(t, &start)

	checkWritten(t, a, directoryRolesWritten)
	for rel, wants := range map[string][]string{
		"test/integration/harness_test.go":          {"Azure: staff.AzureSettings{TenantID: directoryTenant, RedirectURL:", "directoryTenant = "},
		"test/integration/sign_in_skipauth_test.go": {`"APP_AZURE_USERNAME"`, `"APP_AZURE_ROLES"`, `"/api/user/azure/login?returnUrl="`},
		migrateTest: {`t.Setenv("APP_STAFF_AZURE_TENANT_ID", `, `t.Setenv("APP_STAFF_AZURE_REDIRECT_URL", "http://solo.test/api/user/azure/callback")`},
	} {
		src := read(t, a, rel)
		for _, want := range wants {
			if !strings.Contains(src, want) {
				t.Errorf("%s does not contain %q", rel, want)
			}
		}
		for _, other := range []string{"GoogleSettings", "APP_GOOGLE_", "GROUP_PREFIX"} {
			if strings.Contains(src, other) {
				t.Errorf("%s contains %q, which is another method's", rel, other)
			}
		}
	}
}

// TestDirectoryStart_Apply_directoryRolesSuitesProvider holds the same tests in the form
// of another identity provider: the method's own settings, with the issuer its
// registration validates, and its variables and routes.
func TestDirectoryStart_Apply_directoryRolesSuitesProvider(t *testing.T) {
	t.Parallel()

	start := providerStart(providerIssuer)
	a, _ := started(t, &start)

	checkWritten(t, a, directoryRolesWritten)
	for rel, wants := range map[string][]string{
		"test/integration/harness_test.go":          {"Okta: staff.OktaSettings{Issuer: directoryIssuer, RedirectURL:", "directoryIssuer = \"https://id.fleet.example\""},
		"test/integration/sign_in_skipauth_test.go": {`"APP_OKTA_USERNAME"`, `"APP_OKTA_ROLES"`, `"/api/user/okta/login?returnUrl="`},
		migrateTest: {`t.Setenv("APP_STAFF_OKTA_ISSUER", "https://id.fleet.example")`, `t.Setenv("APP_STAFF_OKTA_REDIRECT_URL", "http://solo.test/api/user/okta/callback")`},
	} {
		src := read(t, a, rel)
		for _, want := range wants {
			if !strings.Contains(src, want) {
				t.Errorf("%s does not contain %q", rel, want)
			}
		}
		for _, other := range []string{"GoogleSettings", "AzureSettings", "HostedDomain", "TenantID", "directoryTenant", "APP_GOOGLE_", "APP_AZURE_", "GROUP_PREFIX"} {
			if strings.Contains(src, other) {
				t.Errorf("%s contains %q, which is another method's", rel, other)
			}
		}
	}
}

// TestDirectoryStart_Apply_byDirectorySuites holds the start of Tenant-owned users, by
// directory, to writing the application's tests for the sign-in it has and the tenants it
// is given after it: the harnesses build the roster and the maker by the record's name,
// the suites sign in as the cast's persona, and the first auth's name is the
// application's.
func TestDirectoryStart_Apply_byDirectorySuites(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		tenants Tenancy
		wants   map[string][]string
		never   []string
	}{
		{
			name:    "one tenant for each person",
			tenants: Tenancy{Table: "Tenants", OneTenantPerPerson: true},
			wants: map[string][]string{
				"test/authz/harness_test.go":                     {"app.NewTenantMaker(c.TenantRoster(), c.Live()), identity.WithAdministratorRoles(tenantAdministrators), identity.WithOneTenantPerPerson())", "app.NewTenantRoster(resource.NewSpannerClient(db.Client))"},
				"test/integration/harness_test.go":               {"func newDeployed(ctx context.Context, t *testing.T) *served {", "app.NewTenantRoster(", "app.NewTenantDeclarationReader()", "app.NewTenantDirectoryReader()", "app.NewTenantMaker(roster, svc)", "personaSettings = members.PersonaSettings{Issuer: ", "Persona: personaSettings,", "auth.SeedCast(ctx, io.Discard)", `north = "` + devTenantNorth + `"`},
				"test/integration/sign_in_skipauth_test.go":      {"//go:build skipAuth\n", `"APP_"+strings.ToUpper(registration)+"_USERNAME"`},
				"test/integration/login_test.go":                 {"members.PersonaRegistration", "members.GoogleRegistration", "members.AzureRegistration", "RefusedNotInvited", "func TestLoginMethods(t *testing.T) {", "stack: newDeployed"},
				"test/integration/invited_administrator_test.go": {"func TestFirstAdministrator_invitedThroughADirectory(t *testing.T) {"},
				"test/integration/groups_test.go":                {"s.auth.SeedCastFile(ctx, io.Discard,"},
				"test/integration/grants_test.go":                {"unconditionalRows(accesstypes.DomainScope(north), config.Roles.Domain)"},
				"test/integration/founded_tenant_test.go":        {"func TestFoundedTenant_takesItsFoundersDirectory(t *testing.T) {", `"/api/invite-members-tenant"`, `"tenantName": f.tenant`, `"/api/tenants?limit=200"`, `"/api/tenants/"+id+"/tenant-directories"`, "members.GoogleRegistration", "members.AzureRegistration"},
			},
			never: []string{"staff.", "pkg/auth/staff", "has no tenants"},
		},
		{
			name:    "a record of another name, and a person in several tenants",
			tenants: Tenancy{Table: "ServiceAreas"},
			wants: map[string][]string{
				"test/authz/harness_test.go":              {"app.NewServiceAreaMaker(c.TenantRoster(), c.Live()), identity.WithAdministratorRoles(tenantAdministrators))", "app.NewServiceAreaRoster("},
				"test/integration/harness_test.go":        {"app.NewServiceAreaRoster(", "app.NewServiceAreaDeclarationReader()", "app.NewServiceAreaDirectoryReader()", "app.NewServiceAreaMaker(roster, svc)"},
				"test/integration/founded_tenant_test.go": {`"/api/invite-members-service-area"`, `"serviceAreaName": f.tenant`, `"/api/service-areas?limit=200"`, `"/api/service-areas/"+id+"/service-area-directories"`},
			},
			never: []string{"WithOneTenantPerPerson", "NewTenantRoster", "A person\n// belongs to one tenant"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			start := byDirectoryStart("members")
			start.Tenants = &tt.tenants
			a, ch := started(t, &start, "send_members_user_password_reset_link")

			checkWritten(t, a, byDirectoryWritten)
			for rel, wants := range tt.wants {
				src := read(t, a, rel)
				for _, want := range wants {
					if !strings.Contains(src, want) {
						t.Errorf("%s does not contain %q", rel, want)
					}
				}
			}
			for _, rel := range byDirectoryWritten {
				src := read(t, a, rel)
				for _, never := range tt.never {
					if strings.Contains(src, never) {
						t.Errorf("%s contains %q", rel, never)
					}
				}
			}
			for _, suite := range byDirectoryRemoved {
				if exists(a, suite.file) {
					t.Errorf("%s is still there", suite.file)
				}
			}
			for _, suite := range passwordSuites {
				if named := leftLine(ch, suite.file+" "); len(named) != 0 {
					t.Errorf("%s is among what the change leaves, and the start settled it: %q", suite.file, named)
				}
			}
			// Tenancy names the role a tenant's administrator holds, so the start leaves
			// no step of it to the author and says of no suite that it waits for one.
			if got := skippedLine(ch, "test/integration and test/authz: "); len(got) != 0 {
				t.Errorf("the change says the served suites wait for a step: %q", got)
			}
			if strings.Contains(start.Meaning(), "administrator role first") {
				t.Error("the start's account of what is left still opens its tests with a role to name by hand")
			}
		})
	}
}

// TestDirectoryStart_Apply_loginPageSpec holds a start with no password sign-in to
// writing the spec of the browser application's login page for the sign-in the auth has.
// The base's spec answers the methods route with the password method and expects its
// form, which the page of an auth with no password never draws: left as it is, it is a
// spec the author rewrites before the application's browser tests say anything of the
// application.
func TestDirectoryStart_Apply_loginPageSpec(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		start   func() DirectoryStart
		removes []string
		// wantMethods is what the spec answers the methods route with, in the list's
		// order: each method's registration, the library's method behind it and the
		// words the login page shows for it.
		wantMethods [][3]string
		wantButtons string
		wantLinks   string
		// wantDeployed is what the spec holds of the page where no persona provider is
		// configured, where the start registers the persona method.
		wantDeployed []string
	}{
		{
			name: "Directory roles through Google",
			start: func() DirectoryStart {
				return directoryRolesStart(app.MethodGoogle, "fleet.example")
			},
			wantMethods: [][3]string{{"google", "google", "Google"}},
			wantButtons: "expect(redirects).toEqual(['Sign in with Google']);",
			wantLinks:   "await arriveWith({ verify: 'token' });",
		},
		{
			name: "Directory roles through Microsoft",
			start: func() DirectoryStart {
				return directoryRolesStart(app.MethodAzure, "3f2504e0-4f89-11d3-9a0c-0305e82c3301")
			},
			wantMethods: [][3]string{{"azure", "azure", "Microsoft"}},
			wantButtons: "expect(redirects).toEqual(['Sign in with Microsoft']);",
			wantLinks:   "await arriveWith({ verify: 'token' });",
		},
		{
			name: "Directory roles through another identity provider",
			start: func() DirectoryStart {
				return providerStart(providerIssuer)
			},
			wantMethods: [][3]string{{"okta", "oidc", "Okta"}},
			wantButtons: "expect(redirects).toEqual(['Sign in with Okta']);",
			wantLinks:   "await arriveWith({ verify: 'token' });",
		},
		{
			name: "Tenant-owned users, by directory, under another auth name",
			start: func() DirectoryStart {
				start := byDirectoryStart("members")
				start.Tenants = &Tenancy{Table: "Tenants", OneTenantPerPerson: true}

				return start
			},
			removes:     []string{"send_members_user_password_reset_link"},
			wantMethods: [][3]string{{"google", "google", "Google"}, {"azure", "azure", "Microsoft"}, {"persona", "oidc", "Persona"}},
			wantButtons: "expect(redirects).toEqual(['Sign in with Google', 'Sign in with Microsoft', 'Sign in with Persona']);",
			wantLinks:   "const links: Record<string, string>[] = [{ invitation: 'token' }, { verify: 'token' }];",
			wantDeployed: []string{
				"methods: METHODS.methods.filter((method) => method.name !== 'persona'),",
				"describe('LoginComponent, where no persona provider is configured', () => {",
				"? { status: 200, body: DEPLOYED_METHODS }",
				"expect(redirects).toEqual(['Sign in with Google', 'Sign in with Microsoft']);",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			start := tt.start()
			base, err := baseRendering(loginPageSpec, "example.com/acme/depot", start.Auth)
			if err != nil {
				t.Fatalf("baseRendering() error = %v", err)
			}
			a, ch := started(t, &start, tt.removes...)
			spec := read(t, a, loginPageSpec)
			if spec == string(base) {
				t.Fatalf("%s is the base's still: it answers the methods route with the password method", loginPageSpec)
			}

			// The spec answers the methods route with the methods the start registers, each
			// under the label the generator gives a method of its kind, and no other.
			registered := start.methods()
			if len(registered) != len(tt.wantMethods) {
				t.Fatalf("the start registers %d method(s), and the case names %d", len(registered), len(tt.wantMethods))
			}
			at := 0
			for i, want := range tt.wantMethods {
				if registered[i].Name != want[0] || methodLabel(&registered[i]) != want[2] {
					t.Errorf("the start's method %d is %s, shown as %q; the case names %s, shown as %q", i, registered[i].Name, methodLabel(&registered[i]), want[0], want[2])
				}
				entry := "    {\n      name: '" + want[0] + "',\n      method: '" + want[1] + "',\n      kind: 'redirect',\n      label: '" + want[2] + "',\n      path: `${environment.apiUrl}/user/" + want[0] + "/login`,\n    },\n"
				found := strings.Index(spec[at:], entry)
				if found < 0 {
					t.Fatalf("%s does not answer the methods route with the %s method after the ones before it:\n%s", loginPageSpec, want[0], entry)
				}
				at += found + len(entry)
			}
			if got := strings.Count(spec, "kind: '"); got != len(tt.wantMethods) {
				t.Errorf("%s answers the methods route with %d method(s), want %d", loginPageSpec, got, len(tt.wantMethods))
			}
			for _, want := range append([]string{tt.wantButtons, tt.wantLinks, "expect(element.querySelector('impulse-login-methods form')).toBeNull();"}, tt.wantDeployed...) {
				if !strings.Contains(spec, want) {
					t.Errorf("%s does not contain %q", loginPageSpec, want)
				}
			}
			// The confirmation of a pending sign-in is a password's, and the generated router
			// of an auth with no password method serves none: the spec answers the methods
			// route as that server does, with the status and the cancel alone.
			for _, never := range []string{"kind: 'password'", `input[name="password"]`, "/user/password/login", "/user/pending/confirm", "confirm:", "signUp", "[[", "staff", "solo"} {
				if strings.Contains(spec, never) {
					t.Errorf("%s contains %q", loginPageSpec, never)
				}
			}
			for _, want := range []string{"status: `${environment.apiUrl}/user/pending`,", "cancel: `${environment.apiUrl}/user/pending/cancel`,"} {
				if !strings.Contains(spec, want) {
					t.Errorf("%s does not name the pending route %q", loginPageSpec, want)
				}
			}

			labels := make([]string, 0, len(tt.wantMethods))
			for _, want := range tt.wantMethods {
				labels = append(labels, want[2])
			}
			if got := linesWith(ch.Did, loginPageSpec+": the spec of the browser application's login page written for the sign-in the "+start.Auth+" auth has", []string{"(" + strings.Join(labels, ", ") + ")", "a button for each and no password form"}); len(got) != 1 {
				t.Errorf("the change does not say once that the login page's spec was written, and with which methods: %q", ch.Did)
			}
			if got := leftLine(ch, loginPageSpec+" "); len(got) != 0 {
				t.Errorf("the change leaves the login page's spec to the author, and the start wrote it: %q", got)
			}
			if got := start.MeaningIn(a); !strings.Contains(got, "The spec of the browser application's login page (`"+loginPageSpec+"`) is the start's too") || strings.Contains(got, "still answers the methods route with the password method") {
				t.Error("the start's account of what is left does not say the login page's spec was written, or still hands it on")
			}
		})
	}

	t.Run("a spec that is not the base's is left as it is, and named with what becomes of it", func(t *testing.T) {
		t.Parallel()

		start := directoryRolesStart(app.MethodGoogle, "fleet.example")
		a := newBase(t, start.Auth)
		own := "describe('LoginComponent', () => {\n  it('is another spec', () => {\n    expect(true).toBe(true);\n  });\n});\n"
		if err := os.WriteFile(a.Abs(loginPageSpec), []byte(own), 0o644); err != nil {
			t.Fatalf("os.WriteFile() error = %v", err)
		}
		ch, err := start.Apply(context.Background(), a, &fakeExec{})
		if err != nil {
			t.Fatalf("DirectoryStart.Apply() error = %v", err)
		}
		if read(t, a, loginPageSpec) != own {
			t.Errorf("%s was written over, and it was not the base's", loginPageSpec)
		}
		if got := leftLine(ch, loginPageSpec+" ", "answer it with the methods the auth registers and expect their buttons and no password form"); len(got) != 1 || len(ch.Left) != 1 {
			t.Errorf("the change does not leave the author the login page's spec, and nothing else: %q", ch.Left)
		}
		if !strings.Contains(ch.LeftNote, "were written for the sign-in the staff auth has") {
			t.Errorf("the change does not say the other tests were written: %q", ch.LeftNote)
		}
		if got := linesWith(ch.Did, loginPageSpec+": ", nil); len(got) != 0 {
			t.Errorf("the change says it wrote the login page's spec: %q", got)
		}
		if !exists(a, simulatedSignIn) {
			t.Errorf("%s was not written, and the application's Go tests are the base's", simulatedSignIn)
		}
	})
}

// menuSpecPages reads the pages a menu file's test expects the routes to carry outside
// the System menu, as the base's test lists them: each on a line of its own.
func menuSpecPages(t *testing.T, spec string) []string {
	t.Helper()

	list := regexp.MustCompile(`(?s)unplacedPages\(routes, withoutSystem\)\.map\(\(page\) => page\.label\)\)\.toEqual\(\[\n(.*?)\n\s*\]\);`).FindStringSubmatch(spec)
	if list == nil {
		t.Fatalf("the menu file's test does not list the pages the System menu alone places:\n%s", spec)
	}
	var pages []string
	for _, line := range strings.Split(list[1], "\n") {
		pages = append(pages, strings.Trim(strings.TrimSpace(line), "',"))
	}

	return pages
}

// TestDirectoryStart_Apply_menuSpec holds a Directory roles start to a test of the menu
// file that names the pages its routes carry. The base's names the Break glass page,
// which the library builds over a list the generator describes for an auth that gives
// its own roles: an auth whose directory gives every role does not have it, so the
// base's test fails in the application's browser tests as the application was made.
// The Sign-in page stays: every auth has it, for its second factor.
func TestDirectoryStart_Apply_menuSpec(t *testing.T) {
	t.Parallel()

	base, err := baseRendering(menuSpec, "example.com/acme/depot", "staff")
	if err != nil {
		t.Fatalf("baseRendering() error = %v", err)
	}
	basePages := menuSpecPages(t, string(base))
	for _, page := range directoryRolesAbsentPages {
		if !slices.Contains(basePages, page.label) {
			t.Fatalf("the base's %s does not name the %s page (%q): the start takes out a page the base no longer has", menuSpec, page.label, basePages)
		}
	}
	wantPages := slices.DeleteFunc(slices.Clone(basePages), func(page string) bool {
		return page == "Break glass"
	})

	tests := []struct {
		name  string
		start DirectoryStart
	}{
		{name: "through Google", start: directoryRolesStart(app.MethodGoogle, "fleet.example")},
		{name: "through Microsoft", start: directoryRolesStart(app.MethodAzure, "3f2504e0-4f89-11d3-9a0c-0305e82c3301")},
		{name: "through another identity provider", start: providerStart(providerIssuer)},
	}
	for _, tt := range tests {
		t.Run("Directory roles "+tt.name, func(t *testing.T) {
			t.Parallel()

			a, ch := started(t, &tt.start)
			spec := read(t, a, menuSpec)
			if diff := cmp.Diff(wantPages, menuSpecPages(t, spec)); diff != "" {
				t.Errorf("the pages %s expects of the routes mismatch (-want +got):\n%s", menuSpec, diff)
			}
			// Nothing else of the test changed: the one line is all the start took out.
			if want := strings.Replace(string(base), "      'Break glass',\n", "", 1); spec != want {
				t.Errorf("%s is not the base's less the Break glass page (-want +got):\n%s", menuSpec, cmp.Diff(want, spec))
			}
			prefix := menuSpec + ": the pages the menu file's test expects of the routes lose the ones the staff auth has no route for: "
			if got := linesWith(ch.Did, prefix+"Break glass (", nil); len(got) != 1 {
				t.Errorf("the change does not say once that the menu file's test lost the Break glass page: %q", ch.Did)
			}
			if got := linesWith(ch.Did, prefix, []string{"Sign-in ("}); len(got) != 0 {
				t.Errorf("the change says the menu file's test lost the Sign-in page, which every auth's routes carry: %q", got)
			}
			if got := leftLine(ch, menuSpec+" "); len(got) != 0 {
				t.Errorf("the change leaves the menu file's test to the author, and the start brought it to the routes: %q", got)
			}
		})
	}

	t.Run("a start whose application gives the roles keeps both pages", func(t *testing.T) {
		t.Parallel()

		start := byDirectoryStart("members")
		a, ch := started(t, &start, "send_members_user_password_reset_link")
		base, err := baseRendering(menuSpec, "example.com/acme/depot", start.Auth)
		if err != nil {
			t.Fatalf("baseRendering() error = %v", err)
		}
		if got := read(t, a, menuSpec); got != string(base) {
			t.Errorf("%s was changed, and the auth's routes carry every page the base's do (-want +got):\n%s", menuSpec, cmp.Diff(string(base), got))
		}
		if got := linesWith(ch.Did, menuSpec+": ", nil); len(got) != 0 {
			t.Errorf("the change says it changed the menu file's test: %q", got)
		}
	})

	t.Run("a test that names a page some other way is left as it is, and named with what to do", func(t *testing.T) {
		t.Parallel()

		start := directoryRolesStart(app.MethodGoogle, "fleet.example")
		own := "it('names the pages', () => {\n  expect(pages()).toEqual(['Accounts', 'Sign-in', 'Break glass', 'Feature flags']);\n});\n"
		a, ch := startedOver(t, &start, func(a *app.App) {
			if err := os.WriteFile(a.Abs(menuSpec), []byte(own), 0o644); err != nil {
				t.Fatalf("os.WriteFile() error = %v", err)
			}
		})
		if got := read(t, a, menuSpec); got != own {
			t.Errorf("%s was changed, and it does not list the pages as the base's does:\n%s", menuSpec, got)
		}
		if got := leftLine(ch, menuSpec+" names the Break glass page, which the application's routes do not carry", "take the page out of the ones the test expects"); len(got) != 1 {
			t.Errorf("the change does not leave the author the page the test still names: %q", ch.Left)
		}
		if got := leftLine(ch, menuSpec+" names the Sign-in page"); len(got) != 0 {
			t.Errorf("the change names a page the application's routes carry: %q", got)
		}
		if got := linesWith(ch.Did, menuSpec+": ", nil); len(got) != 0 {
			t.Errorf("the change says it changed the menu file's test: %q", got)
		}
	})

	t.Run("an application with no such test is left without a word", func(t *testing.T) {
		t.Parallel()

		start := directoryRolesStart(app.MethodGoogle, "fleet.example")
		_, ch := startedOver(t, &start, func(a *app.App) {
			if err := os.Remove(a.Abs(menuSpec)); err != nil {
				t.Fatalf("os.Remove() error = %v", err)
			}
		})
		if got := append(linesWith(ch.Did, menuSpec, nil), leftLine(ch, menuSpec)...); len(got) != 0 {
			t.Errorf("the change speaks of a menu file's test the application does not have: %q", got)
		}
	})
}

// TestDirectoryStart_Apply_dialogsSpec holds a Directory roles start to a test of the
// dialogs that compiles in the application it makes. The base's tests the dialog that
// invites a person by the auth's invite method, which the generator does not write for
// an auth whose directory gives every role, so the base's test fails in the
// application's browser tests as the application was made.
func TestDirectoryStart_Apply_dialogsSpec(t *testing.T) {
	t.Parallel()

	base, err := baseRendering(dialogsSpec, "example.com/acme/depot", "staff")
	if err != nil {
		t.Fatalf("baseRendering() error = %v", err)
	}
	if !strings.Contains(string(base), "InviteStaffUser") {
		t.Fatalf("the base's %s names no InviteStaffUser: the fixture is not the test this one pins", dialogsSpec)
	}
	reference, err := referenceText(ReferenceCandidate, appDialogsSpecFile)
	if err != nil {
		t.Fatalf("referenceText() error = %v", err)
	}
	if strings.Contains(reference, "Invite") {
		t.Fatalf("the outlets reference's %s names an invite method: it is not the test without the invite dialog's that the start writes", appDialogsSpecFile)
	}
	did := dialogsSpec + ": written again without its test of the dialog that invites a person: the staff auth's directory gives every role, so the generator writes no method that invites a person, and the base's test names one"
	left := dialogsSpec + " names the staff auth's invite method (InviteStaffUser), which the generator does not write where the directory gives every role: take the test of the dialog that invites a person out of it"
	unsaid := func(t *testing.T, ch *Change) {
		t.Helper()
		if got := append(linesWith(ch.Did, dialogsSpec, nil), leftLine(ch, dialogsSpec)...); len(got) != 0 {
			t.Errorf("the change speaks of the test of the dialogs: %q", got)
		}
	}

	t.Run("a start whose directory gives every role writes the reference's test over the base's", func(t *testing.T) {
		t.Parallel()

		start := directoryRolesStart(app.MethodGoogle, "fleet.example")
		a, ch := started(t, &start)
		if diff := cmp.Diff(forAuth(reference, start.Auth), read(t, a, dialogsSpec)); diff != "" {
			t.Errorf("%s is not the outlets reference's (-want +got):\n%s", dialogsSpec, diff)
		}
		if !slices.Contains(ch.Did, did) {
			t.Errorf("Did lacks %q; got:\n%s", did, strings.Join(ch.Did, "\n"))
		}
		if got := leftLine(ch, dialogsSpec); len(got) != 0 {
			t.Errorf("the change leaves the test of the dialogs to the author, and the start wrote it: %q", got)
		}
	})

	t.Run("a start whose application gives the roles leaves the base's test", func(t *testing.T) {
		t.Parallel()

		start := byDirectoryStart("members")
		a, ch := started(t, &start, "send_members_user_password_reset_link")
		base, err := baseRendering(dialogsSpec, "example.com/acme/depot", start.Auth)
		if err != nil {
			t.Fatalf("baseRendering() error = %v", err)
		}
		if got := read(t, a, dialogsSpec); got != string(base) {
			t.Errorf("%s was changed, and the auth's application gives the roles, so its invite method is written (-want +got):\n%s", dialogsSpec, cmp.Diff(string(base), got))
		}
		unsaid(t, ch)
	})

	t.Run("a test of the application's own that names the invite method is left, and named with what to do", func(t *testing.T) {
		t.Parallel()

		start := directoryRolesStart(app.MethodGoogle, "fleet.example")
		own := "it('invites a person', () => {\n  open(Methods.InviteStaffUser);\n});\n"
		a, ch := startedOver(t, &start, func(a *app.App) {
			if err := os.WriteFile(a.Abs(dialogsSpec), []byte(own), 0o644); err != nil {
				t.Fatalf("os.WriteFile() error = %v", err)
			}
		})
		if got := read(t, a, dialogsSpec); got != own {
			t.Errorf("%s was changed, and it is not the base's:\n%s", dialogsSpec, got)
		}
		if !slices.Contains(ch.Left, left) {
			t.Errorf("Left lacks %q; got:\n%s", left, strings.Join(ch.Left, "\n"))
		}
		if got := linesWith(ch.Did, dialogsSpec, nil); len(got) != 0 {
			t.Errorf("the change says it changed the test of the dialogs: %q", got)
		}
	})

	t.Run("an application with no such test is left without a word", func(t *testing.T) {
		t.Parallel()

		start := directoryRolesStart(app.MethodGoogle, "fleet.example")
		_, ch := startedOver(t, &start, func(a *app.App) {
			if err := os.Remove(a.Abs(dialogsSpec)); err != nil {
				t.Fatalf("os.Remove() error = %v", err)
			}
		})
		unsaid(t, ch)
	})
}

// TestDirectoryStart_Apply_suitesNotWritten holds the start to leaving the tests as they
// are, each named with what becomes of it, where it does not know them whole: no tenants
// told, tenants without directories, or tests that are not the base's.
func TestDirectoryStart_Apply_suitesNotWritten(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		start   func() DirectoryStart
		prepare func(t *testing.T, a *app.App)
		// wantLeft is, by file, what the change says becomes of a test it leaves, and
		// wantSkipped a line it says of what the author gives the tests.
		wantLeft    map[string]string
		wantSkipped string
		// wantTaken is whether the change says that what invites a first administrator
		// was taken out, which a start whose directory gives the roles does whatever
		// becomes of its tests.
		wantTaken bool
	}{
		{
			name: "the start is not told the tenants",
			start: func() DirectoryStart {
				return byDirectoryStart("staff")
			},
		},
		{
			name: "the tenants have no directories",
			start: func() DirectoryStart {
				start := byDirectoryStart("staff")
				start.Tenants = &Tenancy{Table: "Tenants", NoDirectories: true}

				return start
			},
		},
		{
			name: "the application has no integration harness",
			start: func() DirectoryStart {
				return directoryRolesStart(app.MethodGoogle, "fleet.example")
			},
			prepare: func(t *testing.T, a *app.App) {
				t.Helper()

				if err := os.Remove(a.Abs(integrationHarness)); err != nil {
					t.Fatalf("os.Remove() error = %v", err)
				}
			},
			wantLeft: map[string]string{
				deployTest:  "staff.FirstAdministratorRoles, staff.AdministratorRoles",
				migrateTest: "staff.AdministratorVariable, which the auth no longer has",
				"test/integration/first_administrator_test.go": "nobody is invited to this auth, whose directory gives every role, so remove it",
			},
			wantSkipped: integrationHarness + " and " + migrateTest + ": the google registration validates its organization slot and its role groups' prefix",
			wantTaken:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			start := tt.start()
			a := newBase(t, start.Auth)
			if tt.prepare != nil {
				tt.prepare(t, a)
			}
			before := read(t, a, "test/integration/login_test.go")
			ch, err := start.Apply(context.Background(), a, &fakeExec{})
			if err != nil {
				t.Fatalf("DirectoryStart.Apply() error = %v", err)
			}
			if read(t, a, "test/integration/login_test.go") != before {
				t.Error("the base's login test was written again")
			}
			if exists(a, "test/integration/sign_in_skipauth_test.go") {
				t.Error("a sign-in through the simulated directory was written")
			}
			if !exists(a, "test/integration/password_reset_test.go") {
				t.Error("a test of the base was taken out")
			}
			if got := leftLine(ch, "test/integration/login_test.go "); len(got) != 1 {
				t.Errorf("the login test is not named once among what the change leaves: %q", got)
			}
			if strings.Contains(ch.LeftNote, "were written for the sign-in") {
				t.Errorf("the change says the tests were written: %q", ch.LeftNote)
			}
			for file, want := range tt.wantLeft {
				if got := leftLine(ch, file+" ", want); len(got) != 1 {
					t.Errorf("%s: the change leaves %d lines containing %q, want one: %q", file, len(got), want, leftLine(ch, file+" "))
				}
			}
			if tt.wantSkipped != "" && len(skippedLine(ch, tt.wantSkipped)) != 1 {
				t.Errorf("the change does not say once %q: %q", tt.wantSkipped, ch.Skipped)
			}
			if got := linesWith(ch.Did, deployFile+", ", []string{"what invites a first administrator was taken out"}); (len(got) == 1) != tt.wantTaken {
				t.Errorf("the change says %q of what invites a first administrator, want it said once: %t", got, tt.wantTaken)
			}
			if got := start.MeaningIn(a); !strings.Contains(got, "1. Bring the tests to the sign-in the auth has.") || strings.Contains(got, "Read the tests the start wrote") {
				t.Error("the start's account of what is left does not hand the tests on, and it wrote none")
			}
		})
	}
}

// TestDirectoryStart_Apply_deploymentTestNotTheBases holds the start to leaving a
// deployment test it cannot read as the base's: the file is as it was, the change says
// why, and it is named among what is left.
func TestDirectoryStart_Apply_deploymentTestNotTheBases(t *testing.T) {
	t.Parallel()

	start := directoryRolesStart(app.MethodGoogle, "fleet.example")
	a := newBase(t, start.Auth)
	own := "package deploy\n\nimport \"testing\"\n\nfunc TestOwn(t *testing.T) {\n\tt.Parallel()\n}\n"
	if err := os.WriteFile(a.Abs(deployTest), []byte(own), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	ch, err := start.Apply(context.Background(), a, &fakeExec{})
	if err != nil {
		t.Fatalf("DirectoryStart.Apply() error = %v", err)
	}
	if read(t, a, deployTest) != own {
		t.Errorf("%s was changed", deployTest)
	}
	if got := skippedLine(ch, deployTest+": not as the base writes it", "TestFirstAdministratorRoles_areInTheRoleFile", "left as it is"); len(got) != 1 {
		t.Errorf("the change does not say once why %s was left: %q", deployTest, ch.Skipped)
	}
	if got := leftLine(ch, deployTest+" "); len(got) != 1 {
		t.Errorf("%s is not named once among what the change leaves: %q", deployTest, got)
	}
	if strings.Contains(read(t, a, migrateTest), "AdministratorVariable") {
		t.Errorf("%s was not derived, and it is the base's", migrateTest)
	}
}

// Test_cutSource holds the cut to refusing a file that is not what it was written for,
// and to cutting what it names with the comment above it.
func Test_cutSource(t *testing.T) {
	t.Parallel()

	const src = `package p

import "testing"

// kept stays.
const kept = 1

// gone goes, with this comment.
const gone = 2

func TestTable(t *testing.T) {
	tests := []struct {
		name string
		n    int
	}{
		{
			name: "keeps",
			n:    kept,
		},
		{
			name: "goes",
			n:    gone,
		},
	}
	for _, tt := range tests {
		t.Log(tt.name, tt.n)
	}
}

func TestPlain(t *testing.T) {
	t.Log(kept)
}
`
	tests := []struct {
		name     string
		gone     []string
		fn       string
		wantErr  string
		wants    []string
		wantsNot []string
	}{
		{
			name:     "a declaration and the cases that name it are cut, and a case is put in",
			gone:     []string{"gone"},
			fn:       "TestTable",
			wants:    []string{"const kept = 1", `name: "keeps"`, `name: "added"`},
			wantsNot: []string{"gone", `name: "goes"`},
		},
		{name: "a name the file does not declare is refused", gone: []string{"absent"}, fn: "TestTable", wantErr: "absent"},
		{name: "a function the file does not declare is refused", gone: []string{"gone"}, fn: "TestOther", wantErr: "it declares no TestOther"},
		{name: "a function that assigns no table is refused", gone: []string{"gone"}, fn: "TestPlain", wantErr: "TestPlain assigns no table to tests"},
		{name: "a table that goes with its own function is refused", gone: []string{"TestTable"}, fn: "TestTable", wantErr: "TestTable's table is declared inside what is taken out"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out, err := cutSource("p_test.go", []byte(src), tt.gone, tt.fn, regexp.MustCompile(`\bgone\b`), "\t\t{\n\t\t\tname: \"added\",\n\t\t},\n")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("cutSource() error = %v, want one naming %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("cutSource() error = %v", err)
			}
			for _, want := range tt.wants {
				if !strings.Contains(string(out), want) {
					t.Errorf("the cut source does not contain %q:\n%s", want, out)
				}
			}
			for _, not := range tt.wantsNot {
				if strings.Contains(string(out), not) {
					t.Errorf("the cut source still contains %q:\n%s", not, out)
				}
			}
		})
	}
}

// Test_renderSuites holds every tree of tests to rendering for an auth of another name:
// each file parses, and nothing of the placeholder auth or the candidate's module is left.
func Test_renderSuites(t *testing.T) {
	t.Parallel()

	tests := []struct {
		set  string
		data *suiteData
		want []string
	}{
		{set: directoryRolesSuites, data: &suiteData{Method: "google", Field: "Google", Upper: "GOOGLE", Google: true, Kind: "google", Label: "Google"}, want: withLoginPageSpec(directoryRolesWritten)},
		{set: directoryRolesSuites, data: &suiteData{Method: "azure", Field: "Azure", Upper: "AZURE", Kind: "azure", Label: "Microsoft"}, want: withLoginPageSpec(directoryRolesWritten)},
		{set: directoryRolesSuites, data: &suiteData{Method: "okta", Field: "Okta", Upper: "OKTA", Provider: true, Kind: "oidc", Label: "Okta"}, want: withLoginPageSpec(directoryRolesWritten)},
		{set: byDirectorySuites, data: &suiteData{Record: "Tenant", OneTenant: true, North: devTenantNorth, Kebab: "tenant", Camel: "tenant", Segment: "tenants"}, want: withLoginPageSpec(byDirectoryWritten)},
		{set: byDirectorySuites, data: &suiteData{Record: "ServiceArea", OneTenant: true, North: devTenantNorth, Kebab: "service-area", Camel: "serviceArea", Segment: "service-areas"}, want: withLoginPageSpec(byDirectoryWritten)},
		{set: passwordTenantsSuites, data: &suiteData{Record: "Tenant", Directories: true, North: devTenantNorth}, want: passwordTenantsWritten},
		{set: passwordTenantsSuites, data: &suiteData{Record: "ServiceArea", OneTenant: true, North: devTenantNorth}, want: passwordTenantsWritten},
		{set: tenantPeopleSuites, data: &suiteData{Record: "Tenant", Kebab: "tenant", Segment: "tenants", South: devTenantSouth, Password: true}, want: []string{tenantPeopleTest}},
		{set: tenantPeopleSuites, data: &suiteData{Record: "ServiceArea", OneTenant: true, Kebab: "service-area", Segment: "service-areas", South: devTenantSouth}, want: []string{tenantPeopleTest}},
	}
	for _, tt := range tests {
		t.Run(tt.set+" "+tt.data.Method+tt.data.Record, func(t *testing.T) {
			t.Parallel()

			files, err := renderSuites(tt.set, tt.data, "example.com/acme/depot", "crew")
			if err != nil {
				t.Fatalf("renderSuites() error = %v", err)
			}
			var got []string
			for rel, src := range files {
				got = append(got, rel)
				if _, err := parser.ParseFile(token.NewFileSet(), rel, src, parser.ParseComments); err != nil && strings.HasSuffix(rel, ".go") {
					t.Errorf("%s does not parse: %v", rel, err)
				}
				for _, stale := range []string{skeleton.PlaceholderAuth + ".", "pkg/auth/" + skeleton.PlaceholderAuth, "_candidates", "[["} {
					if strings.Contains(string(src), stale) {
						t.Errorf("%s contains %q", rel, stale)
					}
				}
			}
			slices.Sort(got)
			if !slices.Equal(got, tt.want) {
				t.Errorf("renderSuites() wrote %q, want %q", got, tt.want)
			}
		})
	}
}
