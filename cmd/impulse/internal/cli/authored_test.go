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

	"github.com/impulseframework/impulse/cmd/impulse/internal/handoff"
)

// runAuthored, set in the environment, runs TestAuthoredApplication. The test makes an
// application with the commands themselves, which start the Spanner emulator for each
// regeneration, and runs the application's own lint, so it is asked for: CI sets it in
// the job that has the emulator and golangci-lint at hand.
const runAuthored = "IMPULSE_AUTHORED"

// authoredModule is the module path of the application the test makes.
const authoredModule = "example.com/ci/wharf"

// vetFileRE reads a file the compiler stopped at from the go command's output.
var vetFileRE = regexp.MustCompile(`(?m)^(?:vet: )?(\S+\.go):\d+:\d+: `)

// TestAuthoredApplication makes an application the way its author does, with the
// commands and nothing else, and holds what they left to the application's own
// formatting and lint and to the brief: impulse new with a sign-in shape that composes
// tenancy, then a Google method and an organization's identity provider added, a commit
// after each as an author checkpoints the work. The commands leave obligations, and
// the brief is where each is named, so the test holds that nothing else is left:
//
//   - every Go file is formatted, by gofmt and by the formatters the application's lint
//     configuration names;
//   - the application builds;
//   - the tests that do not compile yet are files the standing brief names, and the
//     tenancy brief says what becomes of each suite the base carried;
//   - the application's own lint, under both of its tag sets, finds nothing in any
//     package that compiles;
//   - an auth sets one sign-in rule, the brief is in no commit, the auth package's doc and
//     the README say what the application is now, and the words given for the provider
//     are on its declaration, in the file the brief names.
//
// It costs about a minute and a half and under four gigabytes on a machine whose Go
// caches and emulator image are warm: seventy seconds for the three commands (each
// regenerates against the emulator), the rest the build, the vet and the lint runs.
func TestAuthoredApplication(t *testing.T) {
	if os.Getenv(runAuthored) == "" {
		t.Skipf("set %s=1 to run: it runs impulse new and impulse add method against this checkout (the Spanner emulator, go mod tidy) and the application's lint (golangci-lint), about a minute and a half", runAuthored)
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
	dir := filepath.Join(t.TempDir(), "wharf")
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
		data, err := os.ReadFile(filepath.Join(dir, handoff.File))
		if err != nil {
			t.Fatalf("reading the brief: %v", err)
		}

		return string(data)
	}
	read := func(rel string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}

		return string(data)
	}
	checkpoint := func(message string) {
		t.Helper()
		must("git", "add", "-A")
		must("git", "commit", "-q", "-m", message)
		if out, err := run("git", "ls-files", "--error-unmatch", handoff.File); err == nil {
			t.Errorf("the brief is in the commit %q: %s", message, out)
		}
	}

	// The author's three commands.
	var said bytes.Buffer
	create := newNew()
	create.SetOut(&said)
	create.SetErr(&said)
	create.SetArgs([]string{dir, "--module", authoredModule, "--auth", "staff", "--shape", "global-users-each-tenant-chooses", "--dev-root", checkout})
	if err := create.Execute(); err != nil {
		t.Fatalf("impulse new error = %v\n%s", err, said.String())
	}
	tenancyBrief := brief()
	standing := tenancyBrief
	checkpoint("checkpoint: tenancy as impulse new composed it")
	for _, method := range [][]string{
		{"staff", "google", "--slot", "any", "--authority", "application"},
		{"staff", "oidc", "sso", "--authority", "application", "--label", "Lakeside ID"},
	} {
		said.Reset()
		add := newAddMethod()
		add.SetOut(&said)
		add.SetErr(&said)
		add.SetArgs(append(method, "--app", dir))
		if err := add.Execute(); err != nil {
			t.Fatalf("impulse add method %s error = %v\n%s", strings.Join(method, " "), err, said.String())
		}
		standing = brief()
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
		if !strings.Contains(standing, file) {
			t.Errorf("%s does not compile, and the standing brief does not name it:\n%s", file, vet)
		}
	}
	for _, suite := range []string{"test/integration/sign_up_test.go", "test/integration/sign_up_role_test.go", "test/integration/first_administrator_test.go"} {
		if !strings.Contains(tenancyBrief, "- "+suite+" ") {
			t.Errorf("the tenancy brief does not say what becomes of %s, a suite the base carried", suite)
		}
	}

	// The application's own lint over every package that compiles, under both tag sets.
	// The head of this checkout is built against, so go.mod carries the replace line
	// --dev-root writes, which the lint rightly refuses and CI sets aside the same way.
	var healthy []string
	for pkg := range strings.Lines(must("go", "list", "./...")) {
		rel := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(pkg), authoredModule), "/")
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
	auth := read("pkg/auth/staff/staff.go")
	if n := strings.Count(auth, ".SetSignInPolicy("); n != 1 {
		t.Errorf("pkg/auth/staff/staff.go sets the sign-in rule %d time(s), want once: an Auth has one rule and a second call panics when the auth opens", n)
	}
	if want := "// Package staff is the staff auth: the people who sign in to the console with a password,\n// or with any Google account, or through the organization's own identity provider, who\n// hold their roles in the staff permission store.\n//\n"; !strings.HasPrefix(auth, want) {
		t.Errorf("pkg/auth/staff/staff.go starts %q, want %q", auth[:min(len(auth), 320)], want)
	}
	if readme := read("README.md"); strings.Contains(readme, "no tenancy") || !strings.Contains(readme, "tenancy as data: the `Tenant` record") {
		t.Errorf("README.md still describes an application without tenants:\n%s", readme[:min(len(readme), 500)])
	}
	if app := read("app/app.go"); strings.Contains(app, "The application has no tenants") {
		t.Error("app/app.go still says the application has no tenants")
	}
	program := "cmd/generate/resourcegenerator/generator.go"
	if !strings.Contains(read(program), `generation.OIDCMethod("sso").Labeled("Lakeside ID")`) {
		t.Errorf("%s does not carry the words given for the provider:\n%s", program, read(program))
	}
	if want := program + `: people read the sso method as "Lakeside ID"`; !strings.Contains(standing, want) {
		t.Errorf("the brief does not name the file that holds the provider's words (%q):\n%s", want, standing)
	}
}
