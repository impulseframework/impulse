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
		for _, gone := range []string{"AdministratorVariable", "withHolder", "withUser", "noMailbox", "name one of staff", "accesstypes"} {
			if strings.Contains(migrate, gone) {
				t.Errorf("%s still contains %q", migrateTest, gone)
			}
		}
		for _, want := range []string{
			`t.Setenv("APP_STAFF_GOOGLE_HOSTED_DOMAIN", "fleet.example")`,
			`t.Setenv("APP_STAFF_GOOGLE_REDIRECT_URL", "http://solo.test/api/user/google/callback")`,
			`t.Setenv("APP_STAFF_GOOGLE_GROUP_PREFIX", "staff-")`,
			`wantErr:  "-reinvite-administrator staff: no auth of that name invites a first administrator",`,
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
			named := leftLine(ch, suite.file+" ")
			if want := strings.HasPrefix(suite.file, "web/"); (len(named) == 1) != want {
				t.Errorf("%s is named %d times among what the change leaves; named once = %v wanted: %q", suite.file, len(named), want, named)
			}
		}
		if !strings.Contains(ch.LeftNote, "were written for the sign-in the staff auth has") {
			t.Errorf("the change does not say the tests were written: %q", ch.LeftNote)
		}
		if got := skippedLine(ch, integrationHarness+" and "+migrateTest+": the google registration validates"); len(got) != 0 {
			t.Errorf("the change hands on the settings of a method, and the tests it wrote already give them: %q", got)
		}
		if got := linesWith(ch.Did, deployFile+", pkg/config and the environment template: what invites a first administrator was kept as it is", []string{"APP_STAFF_ADMINISTRATOR", "the cases of " + migrateTest + " prove that it refuses every name", "impulse add auth hands the first administrator of an auth it adds on to be wired as this is written, and writes none of it back"}); len(got) != 1 {
			t.Errorf("the change does not say once that what invites a first administrator was kept, why, and which tests go with it: %q", ch.Did)
		}
		if got := skippedLine(ch, deployFile+", pkg/config"); len(got) != 0 {
			t.Errorf("the change hands what invites a first administrator on to the author, and it is kept: %q", got)
		}
		if got := start.MeaningIn(a); !strings.Contains(got, "Leave what invites a first administrator where it is") || strings.Contains(got, "Take the first administrator out") {
			t.Error("the start's account of what is left does not say that what invites a first administrator stays")
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
				"test/integration/harness_test.go":               {"app.NewTenantRoster(", "app.NewTenantDeclarationReader()", "app.NewTenantDirectoryReader()", "app.NewTenantMaker(roster, svc)", "Persona: members.PersonaSettings{", "auth.SeedCast(ctx, io.Discard)", `north = "` + devTenantNorth + `"`},
				"test/integration/sign_in_skipauth_test.go":      {"//go:build skipAuth\n", `"APP_"+strings.ToUpper(registration)+"_USERNAME"`},
				"test/integration/login_test.go":                 {"members.PersonaRegistration", "members.GoogleRegistration", "members.AzureRegistration", "RefusedNotInvited"},
				"test/integration/invited_administrator_test.go": {"func TestFirstAdministrator_invitedThroughADirectory(t *testing.T) {"},
				"test/integration/groups_test.go":                {"s.auth.SeedCastFile(ctx, io.Discard,"},
				"test/integration/grants_test.go":                {"unconditionalRows(accesstypes.DomainScope(north), config.Roles.Domain)"},
			},
			never: []string{"staff.", "pkg/auth/staff", "has no tenants"},
		},
		{
			name:    "a record of another name, and a person in several tenants",
			tenants: Tenancy{Table: "ServiceAreas"},
			wants: map[string][]string{
				"test/authz/harness_test.go":       {"app.NewServiceAreaMaker(c.TenantRoster(), c.Live()), identity.WithAdministratorRoles(tenantAdministrators))", "app.NewServiceAreaRoster("},
				"test/integration/harness_test.go": {"app.NewServiceAreaRoster(", "app.NewServiceAreaDeclarationReader()", "app.NewServiceAreaDirectoryReader()", "app.NewServiceAreaMaker(roster, svc)"},
			},
			never: []string{"WithOneTenantPerPerson", "NewTenantRoster", "A person\n// belongs to one tenant"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			start := byDirectoryStart("members")
			start.Tenants = &tt.tenants
			a, ch := started(t, &start, "set_members_temporary_password")

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
				named := leftLine(ch, suite.file+" ")
				if want := strings.HasPrefix(suite.file, "web/"); (len(named) == 1) != want {
					t.Errorf("%s is named %d times among what the change leaves; named once = %v wanted: %q", suite.file, len(named), want, named)
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
		// wantTests is what the change says of the tests that go with what invites a
		// first administrator, which it keeps.
		wantTests string
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
			wantTests:   "the tests that name the staff auth's first administrator are among what the change leaves",
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
			if tt.wantTests != "" && len(linesWith(ch.Did, deployFile+", pkg/config", []string{tt.wantTests})) != 1 {
				t.Errorf("the change does not say once %q of what invites a first administrator: %q", tt.wantTests, ch.Did)
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
		{set: directoryRolesSuites, data: &suiteData{Method: "google", Field: "Google", Upper: "GOOGLE", Google: true}, want: directoryRolesWritten},
		{set: directoryRolesSuites, data: &suiteData{Method: "azure", Field: "Azure", Upper: "AZURE"}, want: directoryRolesWritten},
		{set: byDirectorySuites, data: &suiteData{Record: "Tenant", OneTenant: true, North: devTenantNorth}, want: byDirectoryWritten},
	}
	for _, tt := range tests {
		t.Run(tt.set+" "+tt.data.Method, func(t *testing.T) {
			t.Parallel()

			files, err := renderSuites(tt.set, tt.data, "example.com/acme/depot", "crew")
			if err != nil {
				t.Fatalf("renderSuites() error = %v", err)
			}
			var got []string
			for rel, src := range files {
				got = append(got, rel)
				if _, err := parser.ParseFile(token.NewFileSet(), rel, src, parser.ParseComments); err != nil {
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
