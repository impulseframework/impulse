package cli

import (
	"bytes"
	"go/format"
	"io/fs"
	"os"
	osexec "os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/handoff"
	"github.com/impulseframework/impulse/cmd/impulse/internal/shape"
)

// runAuthored, set in the environment, runs TestAuthoredApplication. The test makes an
// application with the commands themselves, which start the Spanner emulator for each
// regeneration, and runs the application's own lint, so it is asked for: CI sets it in
// the job that has the emulator and golangci-lint at hand.
const runAuthored = "IMPULSE_AUTHORED"

// authoredModule is the module path the applications the test makes are under, each by
// its name.
const authoredModule = "example.com/ci"

// vetFileRE reads a file the compiler stopped at from the go command's output.
var vetFileRE = regexp.MustCompile(`(?m)^(?:vet: )?(\S+\.go):\d+:\d+: `)

// authoredApplication is an application made the way its author makes one, with the
// commands and nothing else: where it is, and the briefs the commands left.
type authoredApplication struct {
	dir string
	// newBrief is the brief impulse new left, and standing the brief as the last
	// command left it.
	newBrief, standing string
	// madeAs is the staff auth as impulse audit read it once impulse new was done,
	// before any method was added.
	madeAs *shape.Auth
}

// read reads a file of the application by its path from the root.
func (a *authoredApplication) read(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(a.dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}

	return string(data)
}

// author makes an application under the name with impulse new, given the sign-in
// shape and what else the shape's start takes (the provider and the organization of
// Directory roles; nothing for the other shapes), and one impulse add method for each of
// the methods, a commit after each as an author checkpoints the work, and holds what the
// commands left to the application's own formatting and lint and to the brief:
//
//   - every Go file is formatted, by gofmt and by the formatters the application's lint
//     configuration names;
//   - the application builds;
//   - impulse check finds nothing failing in the application as impulse new made it,
//     and the brief impulse new left is there all the same, for what the start listed
//     itself;
//   - the tests that do not compile yet are files the standing brief names, and the
//     brief impulse new left says why each suite the base carried was taken out: the two
//     tests of sign-up where the application has tenants, and the first administrator's
//     too where it has no password sign-in;
//   - the application's own lint, under both of its tag sets, finds nothing in any
//     package that compiles;
//   - an auth sets one sign-in rule, the brief is in no commit, and, where the shape has
//     tenants, neither the README nor the App still says the application has none and
//     the role file grants nothing the application stopped serving.
func author(t *testing.T, checkout, name, shapeKey string, start []string, methods [][]string) *authoredApplication {
	t.Helper()

	made := &authoredApplication{dir: filepath.Join(t.TempDir(), name)}
	dir := made.dir
	run := func(name string, args ...string) (string, error) {
		cmd := osexec.CommandContext(t.Context(), name, args...) //nolint:gosec // the test's own commands: go, git and golangci-lint, with the arguments written below
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()

		return string(out), err
	}
	must := func(name string, args ...string) string {
		t.Helper()
		out, err := run(name, args...)
		if err != nil {
			t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
		}

		return out
	}
	brief := func() string {
		t.Helper()

		return made.read(t, handoff.File)
	}
	checkpoint := func(message string) {
		t.Helper()
		must("git", "add", "-A")
		must("git", "commit", "-q", "-m", message)
		if out, err := run("git", "ls-files", "--error-unmatch", handoff.File); err == nil {
			t.Errorf("the brief is in the commit %q: %s", message, out)
		}
	}

	startsFrom, err := shape.Find(shapeKey, shape.OfNew(), "impulse new")
	if err != nil {
		t.Fatalf("shape.Find() error = %v", err)
	}

	// The author's commands.
	var said bytes.Buffer
	create := newNew()
	create.SetOut(&said)
	create.SetErr(&said)
	create.SetArgs(append([]string{dir, "--module", authoredModule + "/" + name, "--auth", "staff", "--shape", shapeKey, "--dev-root", checkout}, start...))
	if err := create.Execute(); err != nil {
		t.Fatalf("impulse new error = %v\n%s", err, said.String())
	}
	made.newBrief = brief()
	made.standing = made.newBrief
	// A new application passes its own check the moment it is made, so the brief is
	// there for what the start listed itself and reports no failing check.
	if want := "reports no FAIL. The obligations are the lines listed under \"What changed\""; !strings.Contains(made.newBrief, want) {
		t.Errorf("the brief impulse new left does not say %q: impulse check fails in the application as it was made\n%s", want, said.String())
	}
	checkpoint("checkpoint: the application as impulse new composed it")
	made.madeAs = readShape(t, dir)
	for _, method := range methods {
		said.Reset()
		add := newAddMethod()
		add.SetOut(&said)
		add.SetErr(&said)
		add.SetArgs(append(slices.Clone(method), "--app", dir))
		if err := add.Execute(); err != nil {
			t.Fatalf("impulse add method %s error = %v\n%s", strings.Join(method, " "), err, said.String())
		}
		made.standing = brief()
		checkpoint("checkpoint: the " + method[1] + " method")
	}

	// Formatting: gofmt over every Go file, then the application's own formatters.
	tree, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()
	err = fs.WalkDir(tree.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return errors.Wrap(err, "fs.WalkDir()")
		}
		if d.IsDir() || path.Ext(p) != ".go" {
			return nil
		}
		src, err := fs.ReadFile(tree.FS(), p)
		if err != nil {
			return errors.Wrap(err, "fs.ReadFile()")
		}
		formatted, err := format.Source(src)
		if err != nil {
			t.Errorf("%s does not parse: %v", p, err)

			return nil
		}
		if !bytes.Equal(formatted, src) {
			t.Errorf("%s is not gofmt-clean", p)
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if out, err := run("golangci-lint", "fmt", "--diff", "./..."); err != nil {
		t.Errorf("the application's formatters would change what the commands wrote: %v\n%s", err, out)
	}

	// The application builds, and what does not compile yet is a test the brief names.
	must("go", "build", "./...")
	// Every error of every test package is listed (-gcflags=-e), not the first of each
	// alone, so a second file that stopped compiling is held to the brief as the first is.
	vet, _ := run("go", "test", "-run", "^$", "-tags", "skipAuth", "-gcflags=-e", "./...")
	var failing []string
	for _, m := range vetFileRE.FindAllStringSubmatch(vet, -1) {
		if file := strings.TrimPrefix(m[1], "./"); !slices.Contains(failing, file) {
			failing = append(failing, file)
		}
	}
	for _, file := range failing {
		if !strings.HasSuffix(file, "_test.go") {
			t.Errorf("%s does not compile, and it is no test: the commands leave an application that builds\n%s", file, vet)
		}
		if !strings.Contains(made.standing, file) {
			t.Errorf("%s does not compile, and the standing brief does not name it:\n%s", file, vet)
		}
	}
	// The base's tests of what the application does not have are taken out, each with the
	// reason in the brief impulse new left: the two of sign-up where the application has
	// tenants or no password sign-in (nobody signs up to either), and the first
	// administrator's too where it has no password sign-in. A suite that proves what the
	// application has stays.
	noSignUp := startsFrom.HasTenants() || startsFrom.NoPassword()
	for _, suite := range []struct {
		file string
		gone bool
	}{
		{file: "test/integration/sign_up_test.go", gone: noSignUp},
		{file: "test/integration/sign_up_role_test.go", gone: noSignUp},
		{file: "test/integration/first_administrator_test.go", gone: startsFrom.NoPassword()},
	} {
		_, err := os.Stat(filepath.Join(dir, filepath.FromSlash(suite.file)))
		switch {
		case !suite.gone && err != nil:
			t.Errorf("%s is not there (%v): a suite the base carried that proves what the application has", suite.file, err)
		case suite.gone && err == nil:
			t.Errorf("%s is still there: a suite the base carried that proves what the application does not have", suite.file)
		case suite.gone && !strings.Contains(made.newBrief, suite.file+" ("):
			t.Errorf("the brief impulse new left does not say why %s was taken out, a suite the base carried", suite.file)
		}
	}

	// The application's own lint over every package that compiles, under both tag sets.
	// The head of this checkout is built against, so go.mod carries the replace line
	// --dev-root writes, which the lint rightly refuses and CI sets aside the same way.
	var healthy []string
	for pkg := range strings.Lines(must("go", "list", "./...")) {
		rel := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(pkg), authoredModule+"/"+name), "/")
		if rel == "" {
			rel = "."
		}
		if !slices.ContainsFunc(failing, func(file string) bool { return filepath.ToSlash(filepath.Dir(file)) == rel }) {
			healthy = append(healthy, "./"+rel)
		}
	}
	if len(healthy) == 0 {
		t.Fatal("no package of the application compiles with its tests")
	}
	for _, tags := range [][]string{nil, {"--build-tags", "skipAuth"}} {
		args := append([]string{"run", "--timeout", "10m", "--disable", "gomoddirectives"}, tags...)
		if out, err := run("golangci-lint", append(args, healthy...)...); err != nil {
			t.Errorf("the application's lint (%s) refuses what the commands wrote: %v\n%s", strings.Join(tags, " "), err, out)
		}
	}

	// What the commands say of the application is true of it.
	if n := strings.Count(made.read(t, "pkg/auth/staff/staff.go"), ".SetSignInPolicy("); n != 1 {
		t.Errorf("pkg/auth/staff/staff.go sets the sign-in rule %d time(s), want once: an Auth has one rule and a second call panics when the auth opens", n)
	}
	if !startsFrom.HasTenants() {
		return made
	}
	if readme := made.read(t, "README.md"); strings.Contains(readme, "no tenancy") || !strings.Contains(readme, "tenancy as data: the `Tenant` record") {
		t.Errorf("README.md still describes an application without tenants:\n%s", readme[:min(len(readme), 500)])
	}
	if appFile := made.read(t, "app/app.go"); strings.Contains(appFile, "The application has no tenants") {
		t.Error("app/app.go still says the application has no tenants")
	}
	// An application with tenants lists no roles for a person who signs up, and the
	// permission engine refuses a role file that grants a resource nothing serves.
	if roles := made.read(t, "pkg/auth/staff/roles.json"); strings.Contains(roles, `"StaffSignUpRoles"`) {
		t.Error("pkg/auth/staff/roles.json still grants StaffSignUpRoles, a list the generator does not emit for an application with tenants: the application would not start")
	}

	return made
}

// vetsAsMade holds an application impulse new just made from a shape with no password
// sign-in to passing go vet as it stands, under the default build and under the
// simulated directory's tag, which compiles every test of every package: the start
// writes the application's tests for the sign-in it has, so none of them waits for the
// author to compile.
func vetsAsMade(t *testing.T, made *authoredApplication) {
	t.Helper()

	for _, args := range [][]string{{"vet", "./..."}, {"vet", "-tags", "skipAuth", "./..."}} {
		cmd := osexec.CommandContext(t.Context(), "go", args...) //nolint:gosec // the test's own command, with the arguments written above
		cmd.Dir = made.dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("go %s: %v: an application that starts with no password sign-in passes go vet as impulse new made it\n%s", strings.Join(args, " "), err, out)
		}
	}
}

// testsAsMade holds an application impulse new just made from a shape with no password
// sign-in to passing its own tests as it stands, under the simulated directory's tag,
// which is how its Procfile and its CI run them: the start wrote them for the sign-in
// the application has, so they prove it the moment it is made, the two served suites
// included. Those start the Spanner emulator, which the job that asks for this test has;
// one application's run is about twenty-five seconds.
func testsAsMade(t *testing.T, made *authoredApplication) {
	t.Helper()

	cmd := osexec.CommandContext(t.Context(), "go", "test", "-tags", "skipAuth", "-count=1", "./...") //nolint:gosec // the test's own command, with the arguments written here
	cmd.Dir = made.dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("go test -tags skipAuth ./...: %v: an application that starts with no password sign-in passes its own tests as impulse new made it\n%s", err, out)
	}
}

// holdsNoPassword holds an application that started from a shape with no password
// sign-in to that: impulse audit reads the staff auth's methods as wanted, each as
// "name: whom it admits; who gives the roles", and none of them a password; the auth
// package carries no file of the password method; the generated router mounts no route
// of it and names none in what the methods route answers; the generator emitted no
// handler that sets, resets or removes a password; the bootstrap seeds no account; and
// the brief says the application started with no password sign-in.
func holdsNoPassword(t *testing.T, made *authoredApplication, wantMethods []string) {
	t.Helper()

	var methods []string
	for i := range made.madeAs.Methods {
		m := &made.madeAs.Methods[i]
		if m.Kind == app.MethodPassword {
			t.Errorf("impulse audit reads the %s method on the staff auth, a password method in a shape with none", m.Name)
		}
		methods = append(methods, m.Name+": "+m.Width.Words+"; "+m.Roles)
	}
	if diff := cmp.Diff(wantMethods, methods); diff != "" {
		t.Errorf("the staff auth's methods as impulse audit reads them mismatch (-want +got):\n%s", diff)
	}
	if _, err := os.Stat(filepath.Join(made.dir, "pkg", "auth", "staff", "password.go")); err == nil {
		t.Error("pkg/auth/staff/password.go is still there: the auth package carries the password method's file")
	}
	router := made.read(t, "pkg/router/zz_gen_router.go")
	for _, word := range []string{"/api/user/password", "/api/user/sign-up", "ChangePassword:", "RemovePassword:", "PasswordReset:", "SignUp:"} {
		if strings.Contains(router, word) {
			t.Errorf("pkg/router/zz_gen_router.go names %s, a route of the password method", word)
		}
	}
	err := filepath.WalkDir(made.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return errors.Wrap(err, "filepath.WalkDir()")
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" {
				return fs.SkipDir
			}

			return nil
		}
		if name := d.Name(); strings.HasPrefix(name, "zz_gen_") && strings.Contains(name, "password") {
			t.Errorf("%s is a generated file of a password, in an application with no password sign-in", p)
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if users := made.read(t, "cmd/bootstrap/users.json"); !strings.Contains(users, `"users": []`) {
		t.Errorf("cmd/bootstrap/users.json seeds an account, and the base's is one made ahead with a password:\n%s", users)
	}
	if want := "a shape with no password sign-in"; !strings.Contains(made.newBrief, want) {
		t.Errorf("the brief impulse new left does not say %q", want)
	}
}

// TestAuthoredApplication makes four applications the way their author does, with the
// commands and nothing else, and holds what the commands left to each application's own
// formatting and lint and to the brief (author). The commands leave obligations, and
// the brief is where each is named, so the test holds that nothing else is left.
//
// The first is an application whose tenants can have directories: impulse new with the
// shape Global users, each tenant chooses, then a Google method and an organization's
// identity provider added. Its auth package's doc says what the auth is now, and the
// words given for the provider are on its declaration, in the file the brief names.
//
// The second is an application whose tenants have no directories: impulse new with the
// shape Tenant-owned users, invite only, then a Google method added. It is read as that
// shape before the method is added and after it, the reading names the method as a way
// for an invited person to sign in, and nothing the commands wrote or generated names a
// tenant's directory: no record, no reader, no admission, no route.
//
// The third is an application whose tenants are their directories and that has no
// password sign-in: impulse new with the shape Tenant-owned users, by directory, and
// nothing said of a provider. Its auth registers a Google and a Microsoft method, each
// open to any organization, and the persona method of the test environments; nothing
// the commands wrote or generated serves a password; and a development run signs its
// administrator in as the cast's persona. Its tests are the start's own, written for
// that sign-in: go vet passes under both tag sets as the application is made, and the
// tests pass under the simulated directory's tag.
//
// The fourth is an application one directory gives every role of, with no password
// sign-in either: impulse new with the shape Directory roles, Google and one Workspace
// named. Its auth registers that one method, nobody is invited, no generated handler
// gives a role or invites a person, and a development run signs in the person and the
// roles the environment template names. Its tests are the start's own too: go vet
// passes under both tag sets as the application is made, and the tests pass under the
// tag.
//
// It costs about eight minutes and under eight gigabytes on a machine whose Go caches and
// emulator image are warm: each command regenerates against the emulator (about
// twenty-five seconds), each of the two applications with no password sign-in runs its
// tests (about as long), and the rest is each application's build, vet and lint runs.
func TestAuthoredApplication(t *testing.T) {
	if os.Getenv(runAuthored) == "" {
		t.Skipf("set %s=1 to run: it runs impulse new and impulse add method against this checkout (the Spanner emulator, go mod tidy), the application's lint (golangci-lint) and the tests of the applications with no password sign-in, about eight minutes", runAuthored)
	}
	if _, err := osexec.LookPath("golangci-lint"); err != nil {
		t.Fatalf("%s is set and golangci-lint is not on the PATH: the application's own lint is what the result is held to", runAuthored)
	}
	checkout, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	// The commits are made under an identity of the test's own, whatever the machine has.
	t.Setenv("GIT_AUTHOR_NAME", "ci")
	t.Setenv("GIT_AUTHOR_EMAIL", "ci@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "ci")
	t.Setenv("GIT_COMMITTER_EMAIL", "ci@example.com")

	// The applications are made one after the other: each regenerates against the emulator
	// and runs its own lint, and side by side they would need several times the memory.
	t.Run("tenants that can have directories", func(t *testing.T) { //nolint:paralleltest // made one after the other, as the comment above says
		made := author(t, checkout, "wharf", "global-users-each-tenant-chooses", nil, [][]string{
			{"staff", "google", "--slot", "any", "--authority", "application"},
			{"staff", "oidc", "sso", "--authority", "application", "--label", "Lakeside ID"},
		})
		auth := made.read(t, "pkg/auth/staff/staff.go")
		if want := "// Package staff is the staff auth: the people who sign in to the console with a password,\n// or with any Google account, or through the organization's own identity provider, who\n// hold their roles in the staff permission store.\n//\n"; !strings.HasPrefix(auth, want) {
			t.Errorf("pkg/auth/staff/staff.go starts %q, want %q", auth[:min(len(auth), 320)], want)
		}
		program := "cmd/generate/resourcegenerator/generator.go"
		if !strings.Contains(made.read(t, program), `generation.OIDCMethod("sso").Labeled("Lakeside ID")`) {
			t.Errorf("%s does not carry the words given for the provider:\n%s", program, made.read(t, program))
		}
		if want := program + `: people read the sso method as "Lakeside ID"`; !strings.Contains(made.standing, want) {
			t.Errorf("the brief does not name the file that holds the provider's words (%q):\n%s", want, made.standing)
		}
		// Until a method through an organization's directory is added, no tenant's
		// people can come through one, and the application reads as the invite-only shape.
		if want := "7 Global users, invite only"; made.madeAs.Shape == nil || made.madeAs.Shape.String() != want {
			t.Errorf("before a method is added, impulse audit reads the staff auth as %v, want %s", made.madeAs.Shape, want)
		}
		if got, want := readShape(t, made.dir), "8 Global users, each tenant chooses"; got.Shape == nil || got.Shape.String() != want {
			t.Errorf("impulse audit reads the staff auth as %v, want %s", got.Shape, want)
		}
	})

	t.Run("tenants with no directories", func(t *testing.T) { //nolint:paralleltest // made one after the other, as the comment above the first says
		made := author(t, checkout, "yard", "tenant-owned-users-invite-only", nil, [][]string{
			{"staff", "google", "--slot", "any", "--authority", "application"},
		})
		// The application is the shape it was made as, before the Google method is on it
		// and after.
		const noDirectories = "The tenants have no directories (the tenant record declares none), so a tenant's people come by invitation alone"
		if want := "5 Tenant-owned users, invite only"; made.madeAs.Shape == nil || made.madeAs.Shape.String() != want {
			t.Errorf("before a method is added, impulse audit reads the staff auth as %v, want %s", made.madeAs.Shape, want)
		}
		if want := noDirectories + "."; made.madeAs.Says != want {
			t.Errorf("before a method is added, impulse audit says %q of the staff auth, want %q", made.madeAs.Says, want)
		}
		got := readShape(t, made.dir)
		if want := "5 Tenant-owned users, invite only"; got.Shape == nil || got.Shape.String() != want {
			t.Errorf("impulse audit reads the staff auth as %v, want %s: a method through an organization's directory does not change the shape of an application whose tenants have no directories", got.Shape, want)
		}
		if want := noDirectories + ", and the google method is a way for an invited person to sign in and nothing more."; got.Says != want {
			t.Errorf("impulse audit says %q of the staff auth, want %q", got.Says, want)
		}
		if !slices.ContainsFunc(got.Methods, func(m shape.Method) bool { return m.Name == "google" }) {
			t.Errorf("impulse audit reads no google method on the staff auth: %+v", got.Methods)
		}
		// The brief the tenancy step left says the tenants have no directories, and that
		// laying them in is the author's act.
		for _, want := range []string{"No tenant has a directory in this application (`--tenant-directories=false`)", "Giving the application tenant directories later is the author's act and no command's"} {
			if !strings.Contains(made.newBrief, want) {
				t.Errorf("the tenancy brief does not say %q", want)
			}
		}
		// Nothing the commands wrote or the generator emitted gives a tenant a directory:
		// no record or table, no switch on the tenant record, no reader or admission in
		// the auth or the data level, no route and no page. (The generated code that every
		// application carries still says in its comments what the library does with a
		// directory, and the generated maker refuses an invitation that asks for one.)
		tree, err := os.OpenRoot(made.dir)
		if err != nil {
			t.Fatal(err)
		}
		defer tree.Close()
		// absent are the words no file under the path may hold.
		absent := []struct {
			under string
			words []string
		}{
			{under: "schema/", words: []string{"TenantDirector", "DirectoryOnly"}},
			{under: "pkg/resources/", words: []string{"TenantDirector", "DirectoryOnly", "@tenantDirectory", "@directoryOnly"}},
			{under: "pkg/auth/", words: []string{"TenantDirectories", "SetDirectoryAdmission", "DirectoryReader"}},
			{under: "pkg/config/", words: []string{"TenantDirectories", "DirectoryReader"}},
			{under: "pkg/router/", words: []string{"TenantDirector", "tenant-director", "DirectoryOnly", "directory-only", "to-directory"}},
			{under: "web/", words: []string{"TenantDirector", "tenantDirector", "tenant-director", "directoryOnly", "DirectoryOnly", "tenantSignInPage", "tenantSignInRoute", "sign-in.routes"}},
		}
		err = fs.WalkDir(tree.FS(), ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return errors.Wrap(err, "fs.WalkDir()")
			}
			if d.IsDir() {
				if d.Name() == ".git" || d.Name() == "node_modules" {
					return fs.SkipDir
				}

				return nil
			}
			if name := d.Name(); name == "tenant_directories.go" || strings.HasPrefix(name, "zz_gen_tenant_director") || strings.HasPrefix(name, "tenant-sign-in.") {
				t.Errorf("%s is a file of a tenant's directory, in an application whose tenants have no directories", p)
			}
			if !slices.Contains([]string{".go", ".ts", ".html", ".sql", ".json"}, path.Ext(p)) {
				return nil
			}
			src, err := fs.ReadFile(tree.FS(), p)
			if err != nil {
				return errors.Wrap(err, "fs.ReadFile()")
			}
			for _, held := range absent {
				if !strings.HasPrefix(p, held.under) {
					continue
				}
				for _, word := range held.words {
					if bytes.Contains(src, []byte(word)) {
						t.Errorf("%s names %s, in an application whose tenants have no directories", p, word)
					}
				}
			}

			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})

	t.Run("tenants by their directories, with no password sign-in", func(t *testing.T) { //nolint:paralleltest // made one after the other, as the comment above the first says
		made := author(t, checkout, "quay", "tenant-owned-users-by-directory", nil, nil)
		if want := "6 Tenant-owned users, by directory"; made.madeAs.Shape == nil || made.madeAs.Shape.String() != want {
			t.Errorf("impulse audit reads the staff auth as %v, want %s", made.madeAs.Shape, want)
		}
		// Both providers, each open to any organization and to no personal account, the
		// application giving the roles, and the persona method of the test environments.
		holdsNoPassword(t, made, []string{
			"azure: the accounts of any Microsoft Entra tenant; the application gives the roles",
			"google: the accounts of any Google Workspace; the application gives the roles",
			"persona: the personas of the test environments' persona provider; the application gives the roles",
		})
		vetsAsMade(t, made)
		testsAsMade(t, made)
		// A development run signs its administrator in as the cast's persona, whose
		// account the bootstrap seeds.
		if env, want := made.read(t, ".envrc.template"), "\nexport APP_PERSONA_USERNAME=admin\n"; !strings.Contains(env, want) {
			t.Errorf(".envrc.template does not name the development administrator's persona (%q)", want)
		}
		if bootstrap, want := made.read(t, "cmd/bootstrap/main.go"), "data.Staff().SeedCast(ctx, os.Stdout)"; !strings.Contains(bootstrap, want) {
			t.Errorf("cmd/bootstrap/main.go does not seed the cast (%s)", want)
		}
		// The brief says the application is registered with both providers before it
		// deploys, and names the cast's seeding in a deployment as the author's.
		for _, want := range []string{"Before the application deploys it is registered with both providers", "Seed the cast where a deployment takes the seed"} {
			if !strings.Contains(made.newBrief, want) {
				t.Errorf("the brief impulse new left does not say %q", want)
			}
		}
	})

	t.Run("roles from one directory, with no password sign-in", func(t *testing.T) { //nolint:paralleltest // made one after the other, as the comment above the first says
		made := author(t, checkout, "pier", "directory-roles", []string{"--method", "google", "--organization", "fleet.example"}, nil)
		if want := "3 Directory roles"; made.madeAs.Shape == nil || made.madeAs.Shape.String() != want {
			t.Errorf("impulse audit reads the staff auth as %v, want %s", made.madeAs.Shape, want)
		}
		holdsNoPassword(t, made, []string{"google: the accounts of one Google Workspace; the directory gives the roles"})
		vetsAsMade(t, made)
		testsAsMade(t, made)
		// Nobody is invited and nothing in the application gives a role: the auth has no
		// foothold, and its generated management neither gives a role nor makes or
		// invites a person.
		if _, err := os.Stat(filepath.Join(made.dir, "pkg", "auth", "staff", "foothold.go")); err == nil {
			t.Error("pkg/auth/staff/foothold.go is still there: an auth whose directory gives the roles invites no first administrator")
		}
		for _, handler := range []string{"assign_staff_role", "remove_staff_role", "give_staff_group_role", "create_staff_user", "invite_staff_user"} {
			if _, err := os.Stat(filepath.Join(made.dir, "pkg", "identity", "staffhandlers", "zz_gen_"+handler+".go")); err == nil {
				t.Errorf("pkg/identity/staffhandlers/zz_gen_%s.go is generated: the application gives a role or makes a person, in an auth whose directory does both", handler)
			}
		}
		// A development run signs in the person the environment template names, with the
		// roles it lists there, in the one organization the author named.
		env := made.read(t, ".envrc.template")
		for _, want := range []string{"\nexport APP_ROLES=SuperAdministrator,", "\nexport APP_STAFF_GOOGLE_HOSTED_DOMAIN=fleet.example\n"} {
			if !strings.Contains(env, want) {
				t.Errorf(".envrc.template does not hold %q", want)
			}
		}
		if want := "nobody is invited"; !strings.Contains(made.newBrief, want) {
			t.Errorf("the brief impulse new left does not say %q", want)
		}
	})
}

// readShape reads the staff auth of the application as impulse audit does.
func readShape(t *testing.T, dir string) *shape.Auth {
	t.Helper()

	a, err := app.Discover(dir)
	if err != nil {
		t.Fatalf("app.Discover() error = %v", err)
	}
	reading, err := shape.Read(a)
	if err != nil {
		t.Fatalf("shape.Read() error = %v", err)
	}
	for i := range reading.Auths {
		if reading.Auths[i].Name == "staff" {
			return &reading.Auths[i]
		}
	}
	t.Fatalf("impulse audit reads no staff auth in %s", dir)

	return nil
}
