package transition

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/app"
)

// The tests add tenancy writes again for an application whose tests are the base's.
var passwordTenantsWritten = []string{
	"test/authz/harness_test.go",
	"test/integration/grants_test.go",
	"test/integration/harness_test.go",
}

// What the base's other tests share with its tests of sign-up, which the harness an
// application with tenants is given carries once those tests are gone.
var sharedWithSignUp = []string{"prime", "signInMethod", "signInMethods", "mailedToken", "factorIfHeld", "invited"}

// tenantedAfter applies tenancy to a new base that prepare changed first, and returns the
// application with the change.
func tenantedAfter(t *testing.T, tn Tenancy, auth string, prepare func(t *testing.T, a *app.App)) (*app.App, *Change) {
	t.Helper()

	a := newBase(t, auth)
	if prepare != nil {
		prepare(t, a)
	}
	run := &tenantedRegeneration{collection: referenceFile(t, collectionFile)}
	run.a = a
	run.removes = []string{"pkg/identity/" + auth + "handlers/zz_gen_" + auth + "_sign_up_roles.go"}
	ch, err := tn.Apply(t.Context(), a, run)
	if err != nil {
		t.Fatalf("Tenancy.Apply() error = %v", err)
	}

	return a, ch
}

// declaredIn counts, by name, the top-level declarations of a package's test files: a
// name two files declare does not compile, and neither does one no file declares.
func declaredIn(t *testing.T, a *app.App, dir string) map[string]int {
	t.Helper()

	files, err := filepath.Glob(filepath.Join(a.Abs(dir), "*_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	declared := map[string]int{}
	for _, file := range files {
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("%s does not parse: %v", file, err)
		}
		for _, decl := range parsed.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				declared[d.Name.Name]++
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						declared[s.Name.Name]++
					case *ast.ValueSpec:
						for _, name := range s.Names {
							declared[name.Name]++
						}
					}
				}
			}
		}
	}

	return declared
}

// appended adds a line to a file of the application, as an author who changed it would.
func appended(rel string) func(t *testing.T, a *app.App) {
	return func(t *testing.T, a *app.App) {
		t.Helper()

		src, err := os.ReadFile(a.Abs(rel))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(a.Abs(rel), append(src, []byte("\n// The application's own.\n")...), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// TestTenancy_Apply_writesTheTests pins that an application whose tests are the base's
// passes go vet and its own tests the moment it gains tenants: the two harnesses build
// what the tenants need, the test of the role files holds a tenant's role in a tenant,
// the tests of sign-up are gone with what the others shared with them kept, and the test
// of the way back in to an account opens sign-up with no role. Without it the two
// harnesses do not compile, and every suite of the two packages with them.
func TestTenancy_Apply_writesTheTests(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		tn   Tenancy
		auth string
		// wantRoster is the roster the harnesses build, and wantHarness what the
		// integration harness holds besides.
		wantRoster  string
		wantHarness []string
		notHarness  []string
	}{
		{
			name:        "tenants that can have a directory",
			tn:          Tenancy{Table: "Tenants"},
			auth:        "staff",
			wantRoster:  "NewTenantRoster(",
			wantHarness: []string{"TenantMaker", "Declarations"},
		},
		{
			name:        "an auth of another name, tenants of another, no directories, one to a person",
			tn:          Tenancy{Table: "ServiceAreas", NoDirectories: true, OneTenantPerPerson: true},
			auth:        "crew",
			wantRoster:  "NewServiceAreaRoster(",
			wantHarness: []string{"TenantMaker", "crew.Settings{"},
			notHarness:  []string{"staff.", "pkg/auth/staff"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a, ch := tenantedAfter(t, tt.tn, tt.auth, nil)
			for _, rel := range passwordTenantsWritten {
				src := read(t, a, rel)
				if _, err := parser.ParseFile(token.NewFileSet(), rel, src, parser.ParseComments); err != nil {
					t.Errorf("%s does not parse: %v", rel, err)
				}
				for _, stale := range []string{"_candidates", "[["} {
					if strings.Contains(src, stale) {
						t.Errorf("%s contains %q", rel, stale)
					}
				}
				if !strings.Contains(src, "example.com/acme/depot/") {
					t.Errorf("%s imports nothing of the application's module", rel)
				}
			}
			if !tt.tn.harnessesBuilt(a) {
				t.Errorf("the harnesses do not both build the roster (%s)", tt.wantRoster)
			}
			harness := read(t, a, integrationHarness)
			for _, want := range append([]string{tt.wantRoster}, tt.wantHarness...) {
				if !strings.Contains(harness, want) {
					t.Errorf("%s does not contain %q", integrationHarness, want)
				}
			}
			for _, not := range tt.notHarness {
				if strings.Contains(harness, not) {
					t.Errorf("%s contains %q", integrationHarness, not)
				}
			}
			for _, gone := range []string{signUpSuite, signUpRoleSuite} {
				if exists(a, gone) {
					t.Errorf("%s was not taken out", gone)
				}
			}
			declared := declaredIn(t, a, integrationSuite)
			for _, name := range sharedWithSignUp {
				if declared[name] != 1 {
					t.Errorf("%s declares %s %d time(s), want once: the base's other tests read it", integrationSuite, name, declared[name])
				}
			}
			for name, times := range declared {
				if times > 1 {
					t.Errorf("%s declares %s %d times", integrationSuite, name, times)
				}
			}

			reset := read(t, a, passwordResetSuite)
			for _, want := range []string{"[]byte(`{\"name\":\"password\"}`)", "application with\n// tenants, how", "len(got) != 1 || !got[0].OpenSignUp {"} {
				if !strings.Contains(reset, want) {
					t.Errorf("%s does not contain %q", passwordResetSuite, want)
				}
			}
			for _, not := range []string{"defaultRole", "DefaultRole", "without\n// tenants"} {
				if strings.Contains(reset, not) {
					t.Errorf("%s still contains %q, and an application with tenants refuses a sign-up opened with a role", passwordResetSuite, not)
				}
			}

			if got := linesWith(ch.Did, strings.Join(passwordTenantsWritten, ", ")+": the base's tests written again for an application with ", nil); len(got) != 1 {
				t.Errorf("the change does not say once that it wrote the tests; it made:\n%s", strings.Join(ch.Did, "\n"))
			}
			if got := linesWith(ch.Did, "the base's tests of what an application with ", []string{signUpSuite, signUpRoleSuite}); len(got) != 1 {
				t.Errorf("the change does not say once that it took the tests of sign-up out; it made:\n%s", strings.Join(ch.Did, "\n"))
			}
			if got := linesWith(ch.Did, passwordResetSuite+": the test opens the password method to sign-up with no role", nil); len(got) != 1 {
				t.Errorf("the change does not say once what it changed in %s; it made:\n%s", passwordResetSuite, strings.Join(ch.Did, "\n"))
			}
			for _, line := range ch.Skipped {
				if strings.HasPrefix(line, "test/") {
					t.Errorf("the change leaves a test to the author, and wrote every one: %s", line)
				}
			}
			item := tt.tn.harnessItem(a)
			if !strings.HasPrefix(item, "The test harnesses. Both build the roster already") {
				t.Errorf("the brief still hands the harnesses on: %s", item)
			}
		})
	}
}

// TestTenancy_Apply_testsNotTheBases pins what add tenancy does with tests an author has
// changed: it writes over nothing that is not the base's. A changed harness or a changed
// test of sign-up leaves both harnesses and both tests of sign-up as they are, each named
// with what becomes of it, since the harness the command writes carries what the tests of
// sign-up held; a changed test of the role files or of the way back in is left alone, and
// the change says what to do in it.
func TestTenancy_Apply_testsNotTheBases(t *testing.T) {
	t.Parallel()

	const (
		grants    = "test/integration/grants_test.go"
		authz     = "test/authz/harness_test.go"
		ownReset  = "package integration\n\n// The application's own test of the way back in.\n"
		roleReset = "package integration\n\n// It opens sign-up with a defaultRole of its own.\n"
	)
	tests := []struct {
		name    string
		prepare func(t *testing.T, a *app.App)
		// wantKept are the files left byte for byte, wantWritten whether the harnesses
		// were written, and wantSkipped the start of each line the change leaves the
		// author.
		wantKept    []string
		wantWritten bool
		wantSkipped []string
		notSkipped  []string
	}{
		{
			name:        "a changed harness leaves both and the tests of sign-up",
			prepare:     appended(authz),
			wantKept:    []string{authz, integrationHarness, signUpSuite, signUpRoleSuite, grants, firstAdministratorSuite},
			wantSkipped: []string{signUpSuite + " proves sign-up", signUpRoleSuite + " proves the list", firstAdministratorSuite + " proves what tenancy leaves"},
		},
		{
			name:        "a changed test of sign-up leaves the harnesses, which would declare what it holds twice",
			prepare:     appended(signUpSuite),
			wantKept:    []string{authz, integrationHarness, signUpSuite, signUpRoleSuite, grants},
			wantSkipped: []string{signUpSuite + " proves sign-up", signUpRoleSuite + " proves the list"},
		},
		{
			name:        "a changed test of the role files is left, with what to hold in it",
			prepare:     appended(grants),
			wantKept:    []string{grants},
			wantWritten: true,
			wantSkipped: []string{grants + ": not as the base writes it, so it was left as it is: where it refuses a role held in a tenant, hold that role's grants to the engine in a tenant of the development seed"},
			notSkipped:  []string{signUpSuite, firstAdministratorSuite},
		},
		{
			name: "a test of the way back in that names a role another way is left, with what to change",
			prepare: func(t *testing.T, a *app.App) {
				t.Helper()

				if err := os.WriteFile(a.Abs(passwordResetSuite), []byte(roleReset), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantKept:    []string{passwordResetSuite},
			wantWritten: true,
			wantSkipped: []string{passwordResetSuite + ": not as the base writes it ("},
		},
		{
			name: "a test of the way back in that names no role is left, and nothing is said of it",
			prepare: func(t *testing.T, a *app.App) {
				t.Helper()

				if err := os.WriteFile(a.Abs(passwordResetSuite), []byte(ownReset), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantKept:    []string{passwordResetSuite},
			wantWritten: true,
			notSkipped:  []string{passwordResetSuite},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tn := Tenancy{Table: "Tenants"}
			before := map[string]string{}
			a, ch := tenantedAfter(t, tn, "staff", func(t *testing.T, a *app.App) {
				t.Helper()

				tt.prepare(t, a)
				for _, rel := range tt.wantKept {
					before[rel] = read(t, a, rel)
				}
			})
			for _, rel := range tt.wantKept {
				if !exists(a, rel) {
					t.Errorf("%s was taken out", rel)

					continue
				}
				if read(t, a, rel) != before[rel] {
					t.Errorf("%s was written over", rel)
				}
			}
			if got := tn.harnessesBuilt(a); got != tt.wantWritten {
				t.Errorf("the harnesses build the roster = %t, want %t", got, tt.wantWritten)
			}
			if got := len(linesWith(ch.Did, "", []string{"the base's tests written again"})) == 1; got != tt.wantWritten {
				t.Errorf("the change says it wrote the tests = %t, want %t; it made:\n%s", got, tt.wantWritten, strings.Join(ch.Did, "\n"))
			}
			for _, want := range tt.wantSkipped {
				if got := skippedLine(ch, want); len(got) != 1 {
					t.Errorf("the change does not leave the author one line that starts %q; it left:\n%s", want, strings.Join(ch.Skipped, "\n"))
				}
			}
			for _, not := range tt.notSkipped {
				if got := skippedLine(ch, not); len(got) != 0 {
					t.Errorf("the change leaves the author %q", got)
				}
			}
			if !tt.wantWritten && !slices.Contains(sharedDeclarers(t, a), filepath.Base(signUpSuite)) {
				t.Errorf("%s no longer declares what the other tests share with it", signUpSuite)
			}
		})
	}
}

// sharedDeclarers lists the files of the integration suite that declare the browser's
// first request, which the base's other tests share with its tests of sign-up.
func sharedDeclarers(t *testing.T, a *app.App) []string {
	t.Helper()

	files, err := filepath.Glob(filepath.Join(a.Abs(integrationSuite), "*_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	var declarers []string
	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(src), "func (b *browser) prime(") {
			declarers = append(declarers, filepath.Base(file))
		}
	}

	return declarers
}
