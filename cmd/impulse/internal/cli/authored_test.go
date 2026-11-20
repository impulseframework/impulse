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
	// command left it. Both are empty where impulse new left none: a start that lists no
	// work of its own leaves an application whose check is clean, and says so.
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

// run runs a command in the application's root and gives back everything it wrote.
func (a *authoredApplication) run(t *testing.T, name string, args ...string) (string, error) {
	t.Helper()
	cmd := osexec.CommandContext(t.Context(), name, args...) //nolint:gosec // the test's own commands: go, git and golangci-lint, with the arguments its callers write
	cmd.Dir = a.dir
	out, err := cmd.CombinedOutput()

	return string(out), err
}

// unchanged holds the application's tree to what its last commit holds, after a step
// that is to leave it so.
func (a *authoredApplication) unchanged(t *testing.T, step string) {
	t.Helper()
	out, err := a.run(t, "git", "status", "--porcelain")
	if err != nil {
		t.Fatalf("git status --porcelain: %v\n%s", err, out)
	}
	if out != "" {
		t.Errorf("%s changed the application as it was committed:\n%s", step, out)
	}
}

// author makes an application under the name with impulse new, given the sign-in
// shape (none for an application that starts as the base does, with a password and no
// tenants) and what else the start takes (the provider and the organization of Directory
// roles; the outlets composed into the creation), and one impulse add method for each of
// the methods, a commit after each as an author checkpoints the work, and holds what the
// commands left to the application's own formatting and lint and to the brief:
//
//   - every Go file is formatted, by gofmt and by the formatters the application's lint
//     configuration names;
//   - the application builds;
//   - impulse check finds nothing failing in the application as impulse new made it:
//     the brief impulse new left is there all the same, for what the start listed itself,
//     and where the start listed nothing the command says the check is clean and leaves
//     no brief;
//   - the application passes go vet under both of its tag sets and its own tests as
//     impulse new made it, before any method is added (vetsAsMade, testsAsMade);
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
		t.Helper()

		return made.run(t, name, args...)
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

	// An application that names no shape starts as the base does: with a password, and
	// with no tenants.
	var startsFrom shape.Shape
	args := []string{dir, "--module", authoredModule + "/" + name, "--auth", "staff", "--dev-root", checkout}
	if shapeKey != "" {
		named, err := shape.Find(shapeKey, shape.OfNew(), "impulse new")
		if err != nil {
			t.Fatalf("shape.Find() error = %v", err)
		}
		startsFrom = named
		args = append(args, "--shape", shapeKey)
	}

	// The author's commands.
	var said bytes.Buffer
	create := newNew()
	create.SetOut(&said)
	create.SetErr(&said)
	create.SetArgs(append(args, start...))
	if err := create.Execute(); err != nil {
		t.Fatalf("impulse new error = %v\n%s", err, said.String())
	}
	// A new application passes its own check the moment it is made. Where the start
	// listed work of its own the brief is there for that, and reports no failing check;
	// where it listed none, the command says the check is clean and leaves no brief.
	switch _, err := os.Stat(filepath.Join(dir, handoff.File)); {
	case errors.Is(err, os.ErrNotExist):
		if want := "\nThe check is clean: "; !strings.Contains(said.String(), want) {
			t.Errorf("impulse new left no brief and did not say %q: impulse check fails in the application as it was made, and nothing hands it on\n%s", want, said.String())
		}
	case err != nil:
		t.Fatal(err)
	default:
		made.newBrief = brief()
		made.standing = made.newBrief
		if want := "reports no FAIL. The obligations are the lines listed under \"What changed\""; !strings.Contains(made.newBrief, want) {
			t.Errorf("the brief impulse new left does not say %q: impulse check fails in the application as it was made\n%s", want, said.String())
		}
	}
	checkpoint("checkpoint: the application as impulse new composed it")
	made.madeAs = readShape(t, dir)
	// The application as impulse new made it, before a method is added and while every
	// test in it is the start's own: it passes go vet and its tests.
	vetsAsMade(t, made)
	testsAsMade(t, made)
	menuSpecsNameRoutedPages(t, made)
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

// vetsAsMade holds an application impulse new just made to passing go vet as it stands,
// under the default build and under the simulated directory's tag, which compiles every
// test of every package: the start writes the application's tests for the sign-in and
// the tenants it has, so none of them waits for the author to compile.
func vetsAsMade(t *testing.T, made *authoredApplication) {
	t.Helper()

	for _, args := range [][]string{{"vet", "./..."}, {"vet", "-tags", "skipAuth", "./..."}} {
		if out, err := made.run(t, "go", args...); err != nil {
			t.Errorf("go %s: %v: an application passes go vet as impulse new made it\n%s", strings.Join(args, " "), err, out)
		}
	}
}

// testsAsMade holds an application impulse new just made to passing its own tests as it
// stands, under the simulated directory's tag, which is how its Procfile and its CI run
// them: the start wrote them for the sign-in and the tenants the application has, so
// they prove it the moment it is made, the two served suites included. Those start the
// Spanner emulator, which the job that asks for this test has; one application's run is
// about twenty-five seconds.
func testsAsMade(t *testing.T, made *authoredApplication) {
	t.Helper()

	if out, err := made.run(t, "go", "test", "-tags", "skipAuth", "-count=1", "./..."); err != nil {
		t.Errorf("go test -tags skipAuth ./...: %v: an application passes its own tests as impulse new made it\n%s", err, out)
	}
}

// listedPages are the pages the library builds for an auth in the whole application,
// each with the list of the generated API description it is built over (the library's
// identity pages): the routes carry the page where the description names the list, and
// nowhere else.
var listedPages = map[string]string{
	"Accounts":        "users",
	"Mailed links":    "invitations",
	"Sessions":        "sessions",
	"Failed sign-ins": "signInAttempts",
	"Sign-in":         "signInMethods",
	"Break glass":     "takenRoles",
}

// The lists a generated API description names for an auth's side of the whole
// application, and each list's key among them.
var (
	globalListsRE = regexp.MustCompile(`scope: 'global',\s*lists: \{([^}]*)\}`)
	listKeyRE     = regexp.MustCompile(`(\w+): Resources\.`)
)

// menuSpecsNameRoutedPages holds an application impulse new just made to tests of its
// menu files that expect no page the routes do not carry. A menu file's test pins the
// pages the routes carry and no menu places, and it runs in the application's browser
// tests, which this test does not run; the routes carry the pages the library builds
// from the generated API description beside the test. So a page the test names whose
// list the description does not name is a page the start left in the test of an
// application that has none, and the application fails its own browser tests as made.
func menuSpecsNameRoutedPages(t *testing.T, made *authoredApplication) {
	t.Helper()

	specs := 0
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
		if d.Name() != "app.menus.spec.ts" {
			return nil
		}
		specs++
		rel, err := filepath.Rel(made.dir, p)
		if err != nil {
			return errors.Wrap(err, "filepath.Rel()")
		}
		spec := made.read(t, rel)
		description := made.read(t, filepath.Join(filepath.Dir(rel), "core", "service", "zz_gen_api.ts"))
		described := map[string]bool{}
		for _, side := range globalListsRE.FindAllStringSubmatch(description, -1) {
			for _, key := range listKeyRE.FindAllStringSubmatch(side[1], -1) {
				described[key[1]] = true
			}
		}
		for page, list := range listedPages {
			if strings.Contains(spec, "'"+page+"'") && !described[list] {
				t.Errorf("%s expects the %s page of the routes, and the generated API description beside it names no %s list for the library to build the page over: the application fails its own browser tests as impulse new made it", rel, page, list)
			}
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if specs == 0 {
		t.Error("the application has no test of a menu file (app.menus.spec.ts), and the base it was made from has one")
	}
}

// everyStepAsMade holds an application impulse new just made to the rest of what its
// author and its CI run on it, each step leaving the tree as it is committed: go
// generate writes nothing new, so the application is committed as its generators write
// it; it builds under the simulated directory's tag as it does under none; impulse
// check, run as the author runs it, reports no failing check, says each of the lines
// wanted of the options the start composed, and fixes nothing; and its tests pass under
// no tag as well, the other way its CI runs them. With what author holds every
// application to, that is every step.
func everyStepAsMade(t *testing.T, made *authoredApplication, wantChecks []string) {
	t.Helper()

	if out, err := made.run(t, "go", "generate", "./..."); err != nil {
		t.Errorf("go generate ./...: %v\n%s", err, out)
	}
	made.unchanged(t, "go generate ./...")
	if out, err := made.run(t, "go", "build", "-tags", "skipAuth", "./..."); err != nil {
		t.Errorf("go build -tags skipAuth ./...: %v\n%s", err, out)
	}
	var said bytes.Buffer
	check := newCheck()
	check.SetOut(&said)
	check.SetErr(&said)
	check.SetArgs([]string{"--app", made.dir})
	if err := check.Execute(); err != nil {
		t.Errorf("impulse check error = %v: the application passes its own check as impulse new made it\n%s", err, said.String())
	}
	for _, want := range wantChecks {
		if !strings.Contains(said.String(), want) {
			t.Errorf("impulse check does not say %q of the application:\n%s", want, said.String())
		}
	}
	made.unchanged(t, "impulse check")
	if out, err := made.run(t, "go", "test", "-count=1", "./..."); err != nil {
		t.Errorf("go test ./...: %v: an application passes its own tests as impulse new made it\n%s", err, out)
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

// TestAuthoredApplication makes five applications the way their author does, with the
// commands and nothing else, and holds what the commands left to each application's own
// formatting and lint and to the brief (author). The commands leave obligations, and
// the brief is where each is named, so the test holds that nothing else is left. Each
// application passes go vet under both tag sets and its own tests under the simulated
// directory's tag as impulse new made it, before any method is added: the tests in it
// then are the start's own, written for the sign-in and the tenants it has.
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
// administrator in as the cast's persona.
//
// The fourth is an application one directory gives every role of, with no password
// sign-in either: impulse new with the shape Directory roles, Google and one Workspace
// named. Its auth registers that one method, nobody is invited, no generated handler
// gives a role or invites a person, and a development run signs in the person and the
// roles the environment template names.
//
// The fifth is an application with a second browser application and a machine outlet,
// both composed into the creation (--outlet, --api-outlet) and no shape named. The
// command wires both, so it leaves no brief, and the application is held to every step
// as it is made (everyStepAsMade): it regenerates to itself, builds and passes its tests
// under both tag sets, and passes impulse check, which reads the three outlets and the
// two browser applications.
//
// It costs about twelve minutes and under eight gigabytes on a machine whose Go caches and
// emulator image are warm: each command regenerates against the emulator (about
// twenty-five seconds), each application runs its tests as it is made (about as long, and
// the fifth twice), and the rest is each application's build, vet and lint runs.
func TestAuthoredApplication(t *testing.T) {
	if os.Getenv(runAuthored) == "" {
		t.Skipf("set %s=1 to run: it runs impulse new and impulse add method against this checkout (the Spanner emulator, go mod tidy), each application's lint (golangci-lint) and each application's tests as it is made, about twelve minutes", runAuthored)
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

	t.Run("a second browser application and a machine outlet", func(t *testing.T) { //nolint:paralleltest // made one after the other, as the comment above the first says
		made := author(t, checkout, "jetty", "", []string{"--outlet", "portal=portal/api", "--api-outlet", "devices=devices/api"}, nil)
		// The command wires both options itself, so it hands nothing on.
		if made.newBrief != "" {
			t.Errorf("impulse new left a brief: an application made with a second browser application and a machine outlet is wired as the command leaves it\n%s", made.newBrief)
		}
		// The start names no shape, so the staff auth is the base's: a password, and
		// nobody signs up until an administrator opens it.
		if want := "1 Invite only"; made.madeAs.Shape == nil || made.madeAs.Shape.String() != want {
			t.Errorf("impulse audit reads the staff auth as %v, want %s", made.madeAs.Shape, want)
		}
		// The console moved under its own path beside the second browser application, and
		// the machine outlet is mounted beside both.
		everyStepAsMade(t, made, []string{
			"flat layout, 1 site(s); not tenanted; outlets devices, portal (sessions); generated router",
			"3 outlet(s) mounted: default (/console/api, live), devices (/devices/api), portal (/portal/api, sessions, live)",
			"2 browser application(s) bound to their outlets' auths",
		})
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
