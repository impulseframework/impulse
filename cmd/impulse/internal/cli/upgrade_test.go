package cli

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/check"
	"github.com/impulseframework/impulse/cmd/impulse/internal/framework"
	"github.com/impulseframework/impulse/cmd/impulse/internal/handoff"
	"github.com/impulseframework/impulse/cmd/impulse/internal/ledger"
	transition_ "github.com/impulseframework/impulse/cmd/impulse/internal/transition"
)

const (
	markerFile = "pkg/marker.txt"
	// cleanReport is what the pinned impulse's check answers when nothing fails.
	cleanReport = "PASS  pins  ok\n"
	// toolGet is the command that moves the framework pin, the release appended: go get
	// -tool on impulse's package, since the tool and the libraries are one module.
	toolGet = "go get -tool " + check.ImpulseModule + "@"
)

// upgradeGoMod is an application's go.mod requiring the framework at the pin given, with
// the tool directive on impulse's package and one requirement of its own; "" is a go.mod
// that requires no framework at all, an application before the ledger's first step.
func upgradeGoMod(pin string) string {
	if pin == "" {
		return "module example.com/acme/beacon\n\ngo 1.26.6\n\nrequire github.com/go-chi/chi/v5 v5.3.2\n"
	}

	return "module example.com/acme/beacon\n\ngo 1.26.6\n\nrequire (\n\t" + ledger.FrameworkModule + " " + pin + "\n\tgithub.com/go-chi/chi/v5 v5.3.2\n)\n\ntool " + check.ImpulseModule + "\n"
}

// upgradeGenerator is a generator program declaring one auth as given, at line 15.
func upgradeGenerator(auth string) string {
	return `package main

import (
	"context"

	"github.com/impulseframework/impulse/generation"
)

func newGenerator(ctx context.Context) (*generation.ResourceGenerator, error) {
	return generation.NewResourceGenerator(
		ctx,
		"pkg/resources",
		[]string{"file://schema/migrations"},
		generation.GenerateRoutes("pkg/router", "api",
			` + auth + `,
			generation.WebApp("/"),
		),
	)
}
`
}

// markerRecipe moves the marker file from "old" to "new": the old form is the word old.
type markerRecipe struct{}

func (markerRecipe) Name() string { return "marker" }

func (markerRecipe) Detect(_ context.Context, a *app.App) ([]string, error) {
	data, err := os.ReadFile(a.Abs(markerFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}

		return nil, errors.Wrap(err, "os.ReadFile()")
	}
	if strings.TrimSpace(string(data)) != "old" {
		return nil, nil
	}

	return []string{markerFile + ":1: the marker is in the old form"}, nil
}

func (markerRecipe) Apply(_ context.Context, a *app.App, _ check.Execer) (*transition_.Change, error) {
	if err := os.WriteFile(a.Abs(markerFile), []byte("new\n"), 0o600); err != nil {
		return nil, errors.Wrap(err, "os.WriteFile()")
	}

	return &transition_.Change{Did: []string{"moved the marker to its new form"}}, nil
}

func (markerRecipe) Meaning() string { return "The marker names the form the step expects." }

// upgradeExec records the commands of a walk and answers git as a clean repository, with
// go get -tool moving the framework pin in go.mod as the real one would: to the release
// asked for, or to where hold says the module lands whatever is asked, as go lands a
// module another requirement decides; refusal is what go get answers, exiting 1, when it
// refuses the release. go mod tidy drops the requirement when drop is set, as the real one
// does when nothing in the application needs the module. At each commit it reads where
// go.mod stands (the step its pin reaches and the version it holds), so a test holds every
// commit to its step. go generate answers a schema warning, or fails with generate as its
// output when it is set. The pinned impulse's check answers with report, and fails as the
// command would when the report has a failing line, with the failing check's exit status;
// a command named by fail could not run, and exits as such.
type upgradeExec struct {
	root     string
	steps    []ledger.Step
	hold     string
	drop     bool
	refusal  string
	generate string
	report   string
	calls    []string
	commits  []string
	dirty    string
	fail     string
	// noBun has bun --version fail as a command not found does; bunFails has bun install
	// exit 1 with an error line. bunDirs records the directories bun install ran in,
	// relative to the root.
	noBun    bool
	bunFails bool
	bunDirs  []string
}

// exitStatus is the error a fake command exits non-zero with, carrying the status as the
// error of a process does.
type exitStatus struct {
	code int
	line string
}

func (e exitStatus) Error() string {
	return e.line
}

func (e exitStatus) ExitCode() int {
	return e.code
}

func (f *upgradeExec) Run(_ context.Context, dir string, _ []string, name string, args ...string) ([]byte, error) {
	line := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, line)
	if f.fail != "" && strings.HasPrefix(line, f.fail) {
		return []byte("refused: " + line + "\n"), exitStatus{code: 2, line: line}
	}
	switch {
	case line == "bun --version":
		if f.noBun {
			return nil, &osexec.Error{Name: "bun", Err: osexec.ErrNotFound}
		}

		return []byte("1.4.3\n"), nil
	case line == "bun install":
		rel, err := filepath.Rel(f.root, dir)
		if err != nil {
			return nil, errors.Wrap(err, "filepath.Rel()")
		}
		f.bunDirs = append(f.bunDirs, filepath.ToSlash(rel))
		if f.bunFails {
			return []byte("error: package \"@impulseframework/resource-angular@0.3.0\" not found\n"), exitStatus{code: 1, line: line}
		}

		return []byte("3 packages installed\n"), nil
	case line == "git status --porcelain --untracked-files=all":
		return []byte(f.dirty), nil
	case line == "git diff --cached --name-only":
		return []byte("go.mod\n"), nil
	case strings.HasPrefix(line, toolGet):
		if f.refusal != "" {
			return []byte(f.refusal + "\n"), exitStatus{code: 1, line: line}
		}
		version := strings.TrimPrefix(line, toolGet)
		if f.hold != "" {
			version = f.hold
		}

		return nil, f.pin(version)
	case line == "go mod tidy":
		if f.drop {
			return nil, f.pin("")
		}
	case line == "go generate ./...":
		if f.generate != "" {
			return []byte(f.generate), exitStatus{code: 1, line: line}
		}

		return []byte("Warning: Lantern resolves its tenant through HarborId\n"), nil
	case strings.HasPrefix(line, "go tool impulse check"):
		if check.Failed(check.ParseReport([]byte(f.report))) {
			return []byte(f.report), exitStatus{code: failedExit, line: line}
		}

		return []byte(f.report), nil
	case strings.HasPrefix(line, "git commit "):
		return nil, f.record()
	}

	return nil, nil
}

// Stream runs the command as Run does, its output written through as well.
func (f *upgradeExec) Stream(ctx context.Context, dir string, out io.Writer, name string, args ...string) ([]byte, error) {
	held, err := f.Run(ctx, dir, nil, name, args...)
	if _, werr := out.Write(held); werr != nil {
		return held, errors.Wrap(werr, "out.Write()")
	}

	return held, err
}

// pin writes the framework pin into go.mod, as go get -tool does through the build list;
// "" drops the requirement, as go mod tidy does when nothing needs the module.
func (f *upgradeExec) pin(version string) error {
	p := filepath.Join(f.root, "go.mod")
	data, err := os.ReadFile(p)
	if err != nil {
		return errors.Wrap(err, "os.ReadFile()")
	}
	var lines []string
	for l := range strings.Lines(string(data)) {
		if fields := strings.Fields(l); len(fields) >= 2 && fields[0] == ledger.FrameworkModule {
			if version == "" {
				continue
			}
			l = "\t" + ledger.FrameworkModule + " " + version + "\n"
		}
		lines = append(lines, l)
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "")), 0o600); err != nil {
		return errors.Wrap(err, "os.WriteFile()")
	}

	return nil
}

// record notes where go.mod stands at a commit: the step its pin reaches and the version
// it holds.
func (f *upgradeExec) record() error {
	data, err := os.ReadFile(filepath.Join(f.root, "go.mod"))
	if err != nil {
		return errors.Wrap(err, "os.ReadFile()")
	}
	mod, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return errors.Wrap(err, "modfile.Parse()")
	}
	pin := ledger.Pin(mod)
	f.commits = append(f.commits, fmt.Sprintf("step %d, impulse %s", ledger.Position(f.steps, pin)+1, pin))

	return nil
}

// agentCleared is the body line a step the agent cleared carries in its commit.
const agentCleared = "The step's move left the check failing; the agent did the wiring under the brief, and its work verified clean."

// upgradeLedger is three steps: the first release, which the application stands at, the
// second with the marker recipe, added by v0.2.0, the third a pin move alone, added by
// v0.3.0. Every release is at or after handoffStagedSince, as every release of the
// framework as one module is, so a pinned release checking a step hands the staged tree
// to its own handoff.
func upgradeLedger() []ledger.Step {
	return []ledger.Step{
		{Impulse: "v0.1.0", Note: "the first release"},
		{Impulse: "v0.2.0", Recipes: []ledger.Recipe{markerRecipe{}}, Note: "the marker moves to its new form"},
		{Impulse: "v0.3.0", Note: "no code change is needed"},
	}
}

// earlyLedger is upgradeLedger with its releases before handoffStagedSince. No such
// release is recorded, so the path a pinned release without handoff --staged takes (the
// walk reads its report back and writes the brief itself, and the agent is left to the
// user) is held by a ledger the test writes.
func earlyLedger(t *testing.T) []ledger.Step {
	t.Helper()

	steps := upgradeLedger()
	for i, release := range []string{"v0.0.5", "v0.0.6", "v0.0.7"} {
		if semver.Compare(release, handoffStagedSince) >= 0 {
			t.Fatalf("%s is not before handoffStagedSince %s", release, handoffStagedSince)
		}
		steps[i].Impulse = release
	}

	return steps
}

// stagedLater is a release after handoffStagedSince, its minor bumped.
func stagedLater(t *testing.T) string {
	t.Helper()

	major, minor, ok := strings.Cut(strings.TrimPrefix(semver.MajorMinor(handoffStagedSince), "v"), ".")
	n, err := strconv.Atoi(minor)
	if !ok || err != nil {
		t.Fatalf("handoffStagedSince = %q: no major.minor to bump", handoffStagedSince)
	}

	return "v" + major + "." + strconv.Itoa(n+1) + ".0"
}

// webAt lists the browser packages' pins at a version as the plan and the commit name
// them, in package order.
func webAt(version string) string {
	return "@impulseframework/resource " + version + ", @impulseframework/resource-angular " + version + ", @impulseframework/resource-firestore " + version
}

// webPackageJSON is a web root's package.json at the three versions, formatted as the
// skeleton's is, the client pinned in the dependencies and the overrides both.
func webPackageJSON(resource, angular, firestore string) string {
	return `{
  "name": "beacon-web",
  "private": true,
  "dependencies": {
    "@angular/core": "^21.1.1",
    "@impulseframework/resource": "` + resource + `",
    "@impulseframework/resource-angular":   "` + angular + `",
    "@impulseframework/resource-firestore": "` + firestore + `",
    "rxjs": "~7.8.2"
  },
  "overrides": {
    "@impulseframework/resource": "` + resource + `"
  }
}
`
}

// clientPackageJSON is a web root's package.json naming the client alone, at the version.
func clientPackageJSON(resource string) string {
	return "{\n  \"name\": \"beacon-kiosk\",\n  \"dependencies\": {\n    \"@impulseframework/resource\": \"" + resource + "\"\n  }\n}\n"
}

// webRoot is a web root's files at the versions: angular.json, which makes it a browser
// application, and its package.json.
func webRoot(dir, resource, angular, firestore string) map[string]string {
	return map[string]string{dir + "/angular.json": "{}", dir + "/package.json": webPackageJSON(resource, angular, firestore)}
}

func passing(context.Context, *check.Env) []check.Result {
	return []check.Result{{Name: "pins", Status: check.Pass, Summary: "ok"}}
}

// The commands a walk runs, by step: a step moves the framework pin with go get -tool on
// impulse's package and tidies, and the pinned impulse renders and checks it; the pin-only
// move to the running release comes last and is checked in process.
var (
	markerStepCalls = []string{
		toolGet + "v0.2.0", "go mod tidy",
		"go tool impulse render", "go generate ./...", "go tool impulse check --fix", "git add -A",
		"git commit -q -m upgrade: impulse v0.2.0 (recipe marker) -m the marker moves to its new form\n\nRecipes applied by impulse upgrade: marker.",
	}
	lastStepCalls = []string{
		toolGet + "v0.3.0", "go mod tidy",
		"go tool impulse render", "go generate ./...", "go tool impulse check --fix", "git add -A",
		"git commit -q -m upgrade: impulse v0.3.0 -m no code change is needed",
	}
	toolMoveCalls = []string{
		toolGet + "v0.3.1", "go mod tidy", "go generate ./...", "git add -A",
		"git commit -q -m upgrade: impulse v0.3.1 -m " + toolPinNote,
	}
)

// TestUpgrade walks an application through the ledger: the plan, the dry run (before the
// first step too), each step's recipes, pin move, web pins, owned files, regeneration,
// check and commit in order with the pin-only move last, every commit at exactly its step,
// a step the running release added checked in process, the recipe idempotent, a checkout
// build making no pin-only move, the web pins moved in every web root behind them (a root
// at them left, a pin ahead left, the subject naming the packages alone when the versions
// run it long, no web root at all noted, bun missing or failing refused), a failing check
// stopping with the brief and the step staged (by the pinned release through handoff
// --staged, with the agent when asked, which commits the step the agent cleared and walks
// on; from the pinned check's report at a release before the flag), a failing go generate
// stopping the same way with a brief that quotes the generation and names what the step
// moved (in process, by the pinned release, and on the pin-only move), the pin after the
// move held to the step (a release go refuses stops the step with go's words; a pin below
// the step's version, or dropped by the tidy, is refused naming what go.mod holds; one
// carried beyond it stays, noted), and the refusals (a legacy auth, a dirty tree, a pin
// newer than the running impulse, a pinned render, check or handoff that could not run).
func TestUpgrade(t *testing.T) {
	t.Parallel()

	failingReport := "FAIL  paging  1 offset\n      pkg/x.go:3: Offset\n"
	markerChange := "`impulse upgrade (recipe marker, step 2)` made these changes and staged them:\n\n- moved the marker to its new form\n"
	generateOut := "pkg/generate/main.go:12:5: undefined: generation.PasswordMethod\nexit status 1\ngenerate.go:3: running \"go\": exit status 1\n"
	generateReport := "FAIL  regen  go generate ./... failed\n      pkg/generate/main.go:12:5: undefined: generation.PasswordMethod\n      exit status 1\n      generate.go:3: running \"go\": exit status 1\n"
	generateChange := func(moved string) string {
		return "`impulse upgrade` moved " + moved + " and staged the step; then `go generate ./...` failed, so `impulse check` did not run. The generation's last lines are under \"The failing checks\". The generator program, and what it generates from, build against the moved pins, so what they declare may need the form the step brings, described under \"What it means\"."
	}
	tests := []struct {
		name   string
		goMod  string
		marker string
		// files are further files of the application, by path.
		files        map[string]string
		steps        []ledger.Step
		running      check.Build
		dryRun       bool
		skipGenerate bool
		agent        bool
		agentArgs    []string
		dirty        string
		fail         string
		hold         string
		drop         bool
		refusal      string
		generate     string
		report       string
		verify       func(context.Context, *check.Env) []check.Result
		noBun        bool
		bunFails     bool
		// wantOut are fragments of the output in order, wantNoOut fragments it must not
		// hold; wantCalls the commands run (the git status, rev-parse and diff --cached
		// reads left out); wantCommits where go.mod stood at each commit; wantMarker the
		// marker after the walk; wantFiles the content of files after it, by path;
		// wantBunDirs the directories bun install ran in; wantBrief whether the handoff
		// brief exists after, wantInBrief a fragment of it; wantErr the error.
		wantOut     []string
		wantNoOut   []string
		wantCalls   []string
		wantCommits []string
		wantMarker  string
		wantFiles   map[string]string
		wantBunDirs []string
		wantBrief   bool
		wantInBrief string
		wantErr     string
	}{
		{
			name:    "a dry run lists the web roots and the moves, a package a root does not name included, and changes nothing",
			goMod:   upgradeGoMod("v0.2.0"),
			files:   concatFiles(webRoot("web", "0.2.0", "0.2.0", "0.2.0"), webRoot("apps/portal/web", "0.3.0", "0.3.0", "0.3.0"), map[string]string{"apps/kiosk/web/angular.json": "{}", "apps/kiosk/web/package.json": clientPackageJSON("0.2.0")}),
			steps:   upgradeLedger(),
			running: check.Build{Version: "v0.3.0", FromModule: true},
			dryRun:  true,
			wantOut: []string{
				"beacon stands at step 2 of 3 and pins impulse v0.2.0; the walk:\n  step 3, impulse v0.3.0, web " + webAt("0.3.0") + ": no code change is needed\n",
				"The browser packages' pins move in 3 web root(s):\n" +
					"  apps/kiosk/web/package.json: @impulseframework/resource 0.2.0 to 0.3.0, @impulseframework/resource-angular is not named, so nothing moves it (the step pins 0.3.0), @impulseframework/resource-firestore is not named, so nothing moves it (the step pins 0.3.0)\n" +
					"  apps/portal/web/package.json: at the web pins already\n" +
					"  web/package.json: @impulseframework/resource 0.2.0 to 0.3.0, @impulseframework/resource-angular 0.2.0 to 0.3.0, @impulseframework/resource-firestore 0.2.0 to 0.3.0\n",
			},
			wantFiles: map[string]string{"web/package.json": webPackageJSON("0.2.0", "0.2.0", "0.2.0"), "apps/kiosk/web/package.json": clientPackageJSON("0.2.0")},
		},
		{
			name:    "a step's web pins move in every web root behind them: package.json edited in place, bun install run there, both in the commit; a root at them already is left; the subject names the packages alone, the versions go in the body",
			goMod:   upgradeGoMod("v0.2.0"),
			files:   concatFiles(webRoot("web", "0.2.0", "0.2.0", "0.2.0"), webRoot("apps/portal/web", "0.3.0", "0.3.0", "0.3.0")),
			steps:   upgradeLedger(),
			running: check.Build{Version: "v0.3.0", FromModule: true},
			verify:  passing,
			wantOut: []string{
				"Pin github.com/impulseframework/impulse to v0.3.0.\n",
				"@impulseframework/resource in apps/portal/web/package.json is at 0.3.0 already.\n@impulseframework/resource-angular in apps/portal/web/package.json is at 0.3.0 already.\n@impulseframework/resource-firestore in apps/portal/web/package.json is at 0.3.0 already.\n",
				"Pin @impulseframework/resource in web/package.json to 0.3.0.\nPin @impulseframework/resource-angular in web/package.json to 0.3.0.\nPin @impulseframework/resource-firestore in web/package.json to 0.3.0.\nRunning bun install in web\n",
				"Committed: upgrade: impulse, @impulseframework/resource, @impulseframework/resource-angular, @impulseframework/resource-firestore.",
			},
			wantCalls: []string{
				"bun --version",
				toolGet + "v0.3.0", "go mod tidy", "bun install", "go generate ./...", "git add -A",
				"git commit -q -m upgrade: impulse, @impulseframework/resource, @impulseframework/resource-angular, @impulseframework/resource-firestore -m no code change is needed\n\nPins moved: impulse v0.3.0, " + webAt("0.3.0") + ".",
			},
			wantCommits: []string{"step 3, impulse v0.3.0"},
			wantFiles:   map[string]string{"web/package.json": webPackageJSON("0.3.0", "0.3.0", "0.3.0"), "apps/portal/web/package.json": webPackageJSON("0.3.0", "0.3.0", "0.3.0")},
			wantBunDirs: []string{"web"},
		},
		{
			name:    "a web pin bumped by hand ahead of the step stays, and the others move; a subject within the limit keeps the versions",
			goMod:   upgradeGoMod("v0.2.0"),
			files:   webRoot("web", "0.2.0", "0.2.0", "0.4.0"),
			steps:   upgradeLedger(),
			running: check.Build{Version: "v0.3.0", FromModule: true},
			verify:  passing,
			wantOut: []string{
				"The browser packages' pins move in 1 web root(s):\n  web/package.json: @impulseframework/resource 0.2.0 to 0.3.0, @impulseframework/resource-angular 0.2.0 to 0.3.0, @impulseframework/resource-firestore 0.4.0 stays, ahead of 0.3.0\n",
				"Pin @impulseframework/resource in web/package.json to 0.3.0.\nPin @impulseframework/resource-angular in web/package.json to 0.3.0.\n@impulseframework/resource-firestore in web/package.json is at 0.4.0 already.\nRunning bun install in web\n",
				"Committed: upgrade: impulse v0.3.0, @impulseframework/resource 0.3.0, @impulseframework/resource-angular 0.3.0.",
			},
			wantCalls: []string{
				"bun --version",
				toolGet + "v0.3.0", "go mod tidy", "bun install", "go generate ./...", "git add -A",
				"git commit -q -m upgrade: impulse v0.3.0, @impulseframework/resource 0.3.0, @impulseframework/resource-angular 0.3.0 -m no code change is needed",
			},
			wantFiles:   map[string]string{"web/package.json": webPackageJSON("0.3.0", "0.3.0", "0.4.0")},
			wantBunDirs: []string{"web"},
		},
		{
			name:    "no web root names the browser packages: the plan says so, and no bun runs",
			goMod:   upgradeGoMod("v0.2.0"),
			steps:   upgradeLedger(),
			running: check.Build{Version: "v0.3.0", FromModule: true},
			verify:  passing,
			wantOut: []string{"No web root names the browser packages, so their pins have nowhere to move.\n", "Committed: upgrade: impulse v0.3.0."},
			wantCalls: []string{
				toolGet + "v0.3.0", "go mod tidy", "go generate ./...", "git add -A",
				"git commit -q -m upgrade: impulse v0.3.0 -m no code change is needed",
			},
		},
		{
			name:      "bun missing: the walk refuses before anything moves",
			goMod:     upgradeGoMod("v0.2.0"),
			files:     webRoot("web", "0.2.0", "0.2.0", "0.2.0"),
			steps:     upgradeLedger(),
			running:   check.Build{Version: "v0.3.0", FromModule: true},
			noBun:     true,
			wantCalls: []string{"bun --version"},
			wantFiles: map[string]string{"web/package.json": webPackageJSON("0.2.0", "0.2.0", "0.2.0")},
			wantErr:   "bun could not run (bun --version: exec: \"bun\": executable file not found in $PATH): the walk moves the browser packages' pins in each web root's package.json and runs bun install there, so bun.lock records them; install bun (https://bun.sh) or put it on PATH, and run impulse upgrade again; nothing moved",
		},
		{
			name:        "a failing bun install stops the step with the edit in the tree and the install's words",
			goMod:       upgradeGoMod("v0.2.0"),
			files:       webRoot("web", "0.2.0", "0.2.0", "0.2.0"),
			steps:       upgradeLedger(),
			running:     check.Build{Version: "v0.3.0", FromModule: true},
			bunFails:    true,
			wantCalls:   []string{"bun --version", toolGet + "v0.3.0", "go mod tidy", "bun install"},
			wantFiles:   map[string]string{"web/package.json": webPackageJSON("0.3.0", "0.3.0", "0.3.0")},
			wantBunDirs: []string{"web"},
			wantErr:     `bun install in web failed: error: package "@impulseframework/resource-angular@0.3.0" not found; web/package.json holds the step's web pins and bun.lock does not yet: fix what the install reports, run bun install there, and run impulse upgrade again once the tree is committed or restored`,
		},
		{
			name:    "an application on a single-method session type is refused before anything moves, a dry run too",
			goMod:   upgradeGoMod("v0.1.0"),
			files:   map[string]string{"cmd/generate/resourcegenerator/generator.go": upgradeGenerator(`generation.Auth("example.com/acme/beacon/pkg/auth/members", generation.Password)`)},
			steps:   upgradeLedger(),
			running: check.Build{Version: "v0.3.1", FromModule: true},
			dryRun:  true,
			wantErr: "beacon declares the auth example.com/acme/beacon/pkg/auth/members with generation.Password (cmd/generate/resourcegenerator/generator.go:15), a single-method session type of the framework's earlier releases, which this module does not carry: no recipe moves an application off it. Re-create the application from the skeleton (impulse new, then impulse add for its options) and bring its resources over; nothing moved",
		},
		{
			name:    "an application on a single-method session type is refused with no command run",
			goMod:   upgradeGoMod("v0.1.0"),
			files:   map[string]string{"cmd/generate/resourcegenerator/generator.go": upgradeGenerator(`generation.Auth("example.com/acme/beacon/pkg/auth/members", generation.Password)`)},
			steps:   upgradeLedger(),
			running: check.Build{Version: "v0.3.1", FromModule: true},
			wantErr: "no recipe moves an application off it",
		},
		{
			name:    "an application declaring its methods walks as before",
			goMod:   upgradeGoMod("v0.3.0"),
			files:   map[string]string{"cmd/generate/resourcegenerator/generator.go": upgradeGenerator(`generation.Auth("example.com/acme/beacon/pkg/auth/staff", generation.PasswordMethod())`)},
			steps:   upgradeLedger(),
			running: check.Build{Version: "v0.3.0", FromModule: true},
			wantOut: []string{"beacon stands at step 3 of 3, the ledger's last, and pins impulse v0.3.0, the running impulse: nothing to do."},
		},
		{
			name:    "at the last step and the running impulse: nothing to do",
			goMod:   upgradeGoMod("v0.3.0"),
			steps:   upgradeLedger(),
			running: check.Build{Version: "v0.3.0", FromModule: true},
			wantOut: []string{"beacon stands at step 3 of 3, the ledger's last, and pins impulse v0.3.0, the running impulse: nothing to do."},
		},
		{
			name:    "a dry run before the first step: an application requiring no framework walks every step",
			goMod:   upgradeGoMod(""),
			steps:   upgradeLedger(),
			running: check.Build{Version: "v0.3.0", FromModule: true},
			dryRun:  true,
			wantOut: []string{
				"beacon stands before the ledger's first step and pins no impulse; the walk:\n" +
					"  step 1, impulse v0.1.0, web " + webAt("0.1.0") + ": the first release\n" +
					"  step 2, impulse v0.2.0, web " + webAt("0.2.0") + " (recipe marker): the marker moves to its new form\n" +
					"  step 3, impulse v0.3.0, web " + webAt("0.3.0") + ": no code change is needed\n",
				"No web root names the browser packages, so their pins have nowhere to move.\n",
			},
		},
		{
			name:    "a dry run prints the plan, the steps first and the pin-only move last, and changes nothing",
			goMod:   upgradeGoMod("v0.1.0"),
			marker:  "old\n",
			steps:   upgradeLedger(),
			running: check.Build{Version: "v0.3.1", FromModule: true},
			dryRun:  true,
			wantOut: []string{
				"beacon stands at step 1 of 3 and pins impulse v0.1.0; the walk:",
				"  step 2, impulse v0.2.0, web " + webAt("0.2.0") + " (recipe marker): the marker moves to its new form",
				"  step 3, impulse v0.3.0, web " + webAt("0.3.0") + ": no code change is needed",
				"  impulse v0.3.1: " + toolPinNote,
			},
			wantMarker: "old\n",
		},
		{
			name:    "the walk: each step's recipes, pin, owned files, generate and check by its release, commit; the pin-only move last; every commit at its step",
			goMod:   upgradeGoMod("v0.1.0"),
			marker:  "old\n",
			steps:   upgradeLedger(),
			running: check.Build{Version: "v0.3.1", FromModule: true},
			verify:  passing,
			wantOut: []string{
				"=== step 2: the marker moves to its new form ===",
				"Recipe marker finds 1 place(s) in the old form:\n  - pkg/marker.txt:1: the marker is in the old form",
				"moved the marker to its new form",
				"Pin github.com/impulseframework/impulse to v0.2.0.",
				"The pinned impulse v0.2.0 renders the owned files and checks the step.",
				"Running go generate ./...\n  Warning: Lantern resolves its tenant through HarborId",
				"PASS  pins  ok",
				"Committed: upgrade: impulse v0.2.0 (recipe marker).",
				"=== step 3: no code change is needed ===",
				"Pin github.com/impulseframework/impulse to v0.3.0.",
				"The pinned impulse v0.3.0 renders the owned files and checks the step.",
				"Committed: upgrade: impulse v0.3.0.",
				"=== impulse v0.3.1: " + toolPinNote + " ===\nPin github.com/impulseframework/impulse to v0.3.1.",
				"Committed: upgrade: impulse v0.3.1.",
				"beacon stands at step 3 of 3 and pins impulse v0.3.1. Review the commits and open the pull request.",
			},
			wantCalls:   concat(markerStepCalls, lastStepCalls, toolMoveCalls),
			wantCommits: []string{"step 2, impulse v0.2.0", "step 3, impulse v0.3.0", "step 3, impulse v0.3.1"},
			wantMarker:  "new\n",
		},
		{
			name:    "the step the running release added is checked in process",
			goMod:   upgradeGoMod("v0.1.0"),
			marker:  "old\n",
			steps:   upgradeLedger()[:2],
			running: check.Build{Version: "v0.2.0", FromModule: true},
			verify:  passing,
			wantOut: []string{
				"Pin github.com/impulseframework/impulse to v0.2.0.\nRunning go generate ./...",
				"Committed: upgrade: impulse v0.2.0 (recipe marker).",
				"beacon stands at step 2 of 2 and pins impulse v0.2.0. Review the commits and open the pull request.",
			},
			wantCalls: []string{
				toolGet + "v0.2.0", "go mod tidy", "go generate ./...", "git add -A",
				"git commit -q -m upgrade: impulse v0.2.0 (recipe marker) -m the marker moves to its new form\n\nRecipes applied by impulse upgrade: marker.",
			},
			wantCommits: []string{"step 2, impulse v0.2.0"},
			wantMarker:  "new\n",
		},
		{
			name:       "a recipe finds nothing in an application already in the new form, and the step commits the pin move alone",
			goMod:      upgradeGoMod("v0.1.0"),
			marker:     "new\n",
			steps:      upgradeLedger()[:2],
			running:    check.Build{Version: "v0.2.0", FromModule: true},
			verify:     passing,
			wantOut:    []string{"Recipe marker: nothing in the old form.", "Pin github.com/impulseframework/impulse to v0.2.0.", "Committed: upgrade: impulse v0.2.0 (recipe marker)."},
			wantCalls:  []string{toolGet + "v0.2.0", "go mod tidy", "go generate ./...", "git add -A", "git commit -q -m upgrade: impulse v0.2.0 (recipe marker) -m the marker moves to its new form\n\nRecipes applied by impulse upgrade: marker."},
			wantMarker: "new\n",
		},
		{
			name:    "a step that moves the pin alone: the note describes the hand change, and the step commits it",
			goMod:   upgradeGoMod("v0.3.0"),
			steps:   append(upgradeLedger(), ledger.Step{Impulse: "v0.3.1", Note: "the sign-out route is added by hand"}),
			running: check.Build{Version: "v0.3.1", FromModule: true},
			verify:  passing,
			wantOut: []string{
				"beacon stands at step 3 of 4 and pins impulse v0.3.0; the walk:\n  step 4, impulse v0.3.1, web " + webAt("0.3.1") + ": the sign-out route is added by hand",
				"Committed: upgrade: impulse v0.3.1.",
			},
			wantCalls: []string{
				toolGet + "v0.3.1", "go mod tidy", "go generate ./...", "git add -A",
				"git commit -q -m upgrade: impulse v0.3.1 -m the sign-out route is added by hand",
			},
			wantCommits: []string{"step 4, impulse v0.3.1"},
		},
		{
			name:         "the walk's --skip-generate reaches the pinned check, and no generate runs",
			goMod:        upgradeGoMod("v0.2.0"),
			steps:        upgradeLedger(),
			running:      check.Build{Version: "v0.3.1", FromModule: true},
			skipGenerate: true,
			verify:       passing,
			wantCalls: []string{
				toolGet + "v0.3.0", "go mod tidy",
				"go tool impulse render", "go tool impulse check --fix --skip-generate", "git add -A",
				"git commit -q -m upgrade: impulse v0.3.0 -m no code change is needed",
				toolGet + "v0.3.1", "go mod tidy", "git add -A",
				"git commit -q -m upgrade: impulse v0.3.1 -m " + toolPinNote,
			},
			wantCommits: []string{"step 3, impulse v0.3.0", "step 3, impulse v0.3.1"},
		},
		{
			name:    "a checkout build walks the steps, each pinning its release, and makes no pin-only move",
			goMod:   upgradeGoMod("v0.2.0"),
			steps:   upgradeLedger(),
			running: check.Build{Version: "v0.3.1-0.20261008120000-0702ef849363+dirty"},
			dryRun:  true,
			wantOut: []string{
				"beacon stands at step 2 of 3 and pins impulse v0.2.0; the walk:\n  step 3, impulse v0.3.0, web " + webAt("0.3.0") + ": no code change is needed\n",
				"This impulse was built from a checkout (v0.3.1-0.20261008120000-0702ef849363+dirty), so the pin-only move does not apply: the pin ends where the last step leaves it.",
			},
		},
		{
			name:    "a failing check by a pinned release before handoff --staged stops the walk with the brief written from its report and the step staged; the agent is left to the user",
			goMod:   upgradeGoMod("v0.0.5"),
			marker:  "old\n",
			steps:   earlyLedger(t),
			running: check.Build{Version: "(devel)"},
			agent:   true,
			report:  failingReport,
			wantOut: []string{
				"=== step 2",
				"The pinned impulse v0.0.6 renders the owned files and checks the step.",
				"FAIL  paging  1 offset\n      pkg/x.go:3: Offset",
				"The agent is not launched at a step checked by the pinned impulse v0.0.6, a release before " + handoffStagedSince + "; run it on the brief by hand.",
				"Wrote the brief to .impulse-handoff.md: 1 obligation(s) under 1 failing check(s).",
			},
			wantCalls:   []string{toolGet + "v0.0.6", "go mod tidy", "go tool impulse render", "go generate ./...", "go tool impulse check --fix", "git add -A"},
			wantMarker:  "new\n",
			wantBrief:   true,
			wantInBrief: "## What changed\n\n" + markerChange + "\nThe change under review is staged in the index, 1 path(s):\n\n- go.mod\n\n## What it means\n\nThe marker names the form the step expects.\n\n## The failing checks\n\nThis is the output of `impulse check`, failing checks only. Each line under a check is one obligation.\n\n```\nFAIL  paging  1 offset\n      pkg/x.go:3: Offset\n```",
			wantErr:     "step 2 (impulse v0.0.6) left the check failing; its changes are staged and the brief is at .impulse-handoff.md. When the check is clean, commit and run impulse upgrade again: it resumes from go.mod",
		},
		{
			name:      "a failing check by a pinned release with handoff --staged: the staged step is handed to it with the recipe's change and meaning, and the agent when asked; the step the agent cleared is committed and the walk continues",
			goMod:     upgradeGoMod("v0.1.0"),
			marker:    "old\n",
			steps:     upgradeLedger(),
			running:   check.Build{Version: "(devel)"},
			agent:     true,
			agentArgs: []string{"--model", "sonnet"},
			report:    failingReport,
			wantOut: []string{
				"=== step 2",
				"The pinned impulse v0.2.0 renders the owned files and checks the step.",
				"FAIL  paging  1 offset\n      pkg/x.go:3: Offset",
				"The pinned impulse v0.2.0 writes the brief, runs the agent and verifies its work, through go tool impulse handoff --staged.",
				"Committed: upgrade: impulse v0.2.0 (recipe marker) (the agent cleared the step).",
				"=== step 3",
				"Committed: upgrade: impulse v0.3.0 (the agent cleared the step).",
			},
			wantNoOut: []string{"The agent is not launched", "Wrote the brief", "left the check failing"},
			wantCalls: []string{
				toolGet + "v0.2.0", "go mod tidy", "go tool impulse render", "go generate ./...", "go tool impulse check --fix", "git add -A",
				"go tool impulse handoff --staged --change " + markerChange + " --meaning The marker names the form the step expects. --agent --agent-command claude --agent-arg --model --agent-arg sonnet",
				"git add -A",
				"git commit -q -m upgrade: impulse v0.2.0 (recipe marker) -m the marker moves to its new form\n\nRecipes applied by impulse upgrade: marker.\n\n" + agentCleared,
				toolGet + "v0.3.0", "go mod tidy", "go tool impulse render", "go generate ./...", "go tool impulse check --fix", "git add -A",
				"go tool impulse handoff --staged --agent --agent-command claude --agent-arg --model --agent-arg sonnet",
				"git add -A",
				"git commit -q -m upgrade: impulse v0.3.0 -m no code change is needed\n\n" + agentCleared,
			},
			wantCommits: []string{"step 2, impulse v0.2.0", "step 3, impulse v0.3.0"},
			wantMarker:  "new\n",
		},
		{
			name:    "a failing check by a pinned release with handoff --staged, the agent asked for and its work not verifying: the walk stops at the handoff's word, nothing committed",
			goMod:   upgradeGoMod("v0.1.0"),
			marker:  "old\n",
			steps:   upgradeLedger(),
			running: check.Build{Version: "(devel)"},
			agent:   true,
			report:  failingReport,
			fail:    "go tool impulse handoff",
			wantOut: []string{
				"=== step 2",
				"The pinned impulse v0.2.0 writes the brief, runs the agent and verifies its work, through go tool impulse handoff --staged.",
			},
			wantNoOut: []string{"Committed:", "=== step 3"},
			wantCalls: []string{
				toolGet + "v0.2.0", "go mod tidy", "go tool impulse render", "go generate ./...", "go tool impulse check --fix", "git add -A",
				"go tool impulse handoff --staged --change " + markerChange + " --meaning The marker names the form the step expects. --agent --agent-command claude",
			},
			wantMarker: "new\n",
			wantErr:    "go tool impulse handoff: ",
		},
		{
			name:         "a failing check at a pin move by a pinned release with handoff --staged, without the agent; the walk's --skip-generate reaches the handoff",
			goMod:        upgradeGoMod("v0.2.0"),
			steps:        upgradeLedger(),
			running:      check.Build{Version: "(devel)"},
			skipGenerate: true,
			report:       failingReport,
			wantOut:      []string{"The pinned impulse v0.3.0 writes the brief, through go tool impulse handoff --staged."},
			wantCalls: []string{
				toolGet + "v0.3.0", "go mod tidy",
				"go tool impulse render", "go tool impulse check --fix --skip-generate", "git add -A",
				"go tool impulse handoff --staged --skip-generate",
			},
			wantErr: "step 3 (impulse v0.3.0) left the check failing; its changes are staged and the brief is at .impulse-handoff.md",
		},
		{
			name:    "a failing check on the pin-only move; the brief lists the staged paths",
			goMod:   upgradeGoMod("v0.3.0"),
			steps:   upgradeLedger(),
			running: check.Build{Version: "v0.3.1", FromModule: true},
			verify: func(context.Context, *check.Env) []check.Result {
				return []check.Result{{Name: "ci-workflow", Status: check.Fail, Summary: "differs"}}
			},
			wantCalls:   []string{toolGet + "v0.3.1", "go mod tidy", "go generate ./...", "git add -A"},
			wantBrief:   true,
			wantInBrief: "## What changed\n\nThe change under review is staged in the index, 1 path(s):\n\n- go.mod\n\n## The failing checks",
			wantErr:     "the impulse v0.3.1 pin step left the check failing",
		},
		{
			name:     "a failing go generate at a step checked in process stops the walk with the brief quoting the generation and naming what the step moved, and the step staged",
			goMod:    upgradeGoMod("v0.1.0"),
			marker:   "old\n",
			steps:    upgradeLedger()[:2],
			running:  check.Build{Version: "v0.2.0", FromModule: true},
			generate: generateOut,
			wantOut: []string{
				"Pin github.com/impulseframework/impulse to v0.2.0.\nRunning go generate ./...\n" + generateReport,
				"Wrote the brief to .impulse-handoff.md: 3 obligation(s) under 1 failing check(s).",
			},
			wantCalls:   []string{toolGet + "v0.2.0", "go mod tidy", "go generate ./...", "git add -A"},
			wantMarker:  "new\n",
			wantBrief:   true,
			wantInBrief: "## What changed\n\n" + markerChange + "\n" + generateChange("impulse v0.2.0") + "\n\nThe change under review is staged in the index, 1 path(s):\n\n- go.mod\n\n## What it means\n\nThe marker names the form the step expects.\n\nWhat the step changes for the application: the marker moves to its new form.\n\n## The failing checks\n\nThis is the output of `impulse check`, failing checks only. Each line under a check is one obligation.\n\n```\n" + generateReport + "```",
			wantErr:     "step 2 (impulse v0.2.0) left go generate ./... failing; its changes are staged and the brief is at .impulse-handoff.md. When the check is clean, commit and run impulse upgrade again: it resumes from go.mod",
		},
		{
			name:     "a failing go generate at a step checked by a pinned release with handoff --staged hands the staged step to it, what the step moved passed through as a change; no brief is written here",
			goMod:    upgradeGoMod("v0.1.0"),
			marker:   "old\n",
			steps:    upgradeLedger(),
			running:  check.Build{Version: "(devel)"},
			generate: generateOut,
			wantOut: []string{
				"The pinned impulse v0.2.0 renders the owned files and checks the step.",
				generateReport,
				"The pinned impulse v0.2.0 writes the brief, through go tool impulse handoff --staged.",
			},
			wantNoOut: []string{"Wrote the brief"},
			wantCalls: []string{
				toolGet + "v0.2.0", "go mod tidy", "go tool impulse render", "go generate ./...", "git add -A",
				"go tool impulse handoff --staged --change " + markerChange + " --change " + generateChange("impulse v0.2.0") + " --meaning The marker names the form the step expects. --meaning What the step changes for the application: the marker moves to its new form.",
			},
			wantMarker: "new\n",
			wantErr:    "step 2 (impulse v0.2.0) left go generate ./... failing; its changes are staged and the brief is at .impulse-handoff.md",
		},
		{
			name:        "a failing go generate on the pin-only move writes the brief with the pin as what moved",
			goMod:       upgradeGoMod("v0.3.0"),
			steps:       upgradeLedger(),
			running:     check.Build{Version: "v0.3.1", FromModule: true},
			generate:    generateOut,
			wantCalls:   []string{toolGet + "v0.3.1", "go mod tidy", "go generate ./...", "git add -A"},
			wantBrief:   true,
			wantInBrief: "## What changed\n\n" + generateChange("impulse v0.3.1") + "\n\nThe change under review is staged in the index, 1 path(s):\n\n- go.mod\n\n## What it means\n\nWhat the step changes for the application: " + toolPinNote + ".\n\n## The failing checks",
			wantErr:     "the impulse v0.3.1 pin step left go generate ./... failing; its changes are staged and the brief is at .impulse-handoff.md",
		},
		{
			name:      "a release go refuses stops the step with go's words, nothing committed",
			goMod:     upgradeGoMod("v0.2.0"),
			steps:     upgradeLedger(),
			running:   check.Build{Version: "v0.3.0", FromModule: true},
			refusal:   "go: github.com/impulseframework/impulse@v0.3.0: reading github.com/impulseframework/impulse/go.mod at revision v0.3.0: unknown revision v0.3.0",
			wantOut:   []string{"Pin github.com/impulseframework/impulse to v0.3.0."},
			wantCalls: []string{toolGet + "v0.3.0"},
			wantErr:   toolGet + "v0.3.0: go: github.com/impulseframework/impulse@v0.3.0: reading github.com/impulseframework/impulse/go.mod at revision v0.3.0: unknown revision v0.3.0",
		},
		{
			name:      "a pin below the step's version after the move is refused, naming what go.mod holds; nothing is committed",
			goMod:     upgradeGoMod("v0.2.0"),
			steps:     upgradeLedger(),
			running:   check.Build{Version: "v0.3.0", FromModule: true},
			hold:      "v0.2.5",
			wantCalls: []string{toolGet + "v0.3.0", "go mod tidy"},
			wantErr:   "the step pins github.com/impulseframework/impulse at v0.3.0 but go.mod holds v0.2.5 after the move: another requirement holds the pin back",
		},
		{
			name:      "a requirement the tidy dropped is refused: nothing in the application needs the framework",
			goMod:     upgradeGoMod("v0.2.0"),
			steps:     upgradeLedger(),
			running:   check.Build{Version: "v0.3.0", FromModule: true},
			drop:      true,
			wantCalls: []string{toolGet + "v0.3.0", "go mod tidy"},
			wantErr:   "the step pins github.com/impulseframework/impulse at v0.3.0 but go.mod holds no requirement for it after the move: go mod tidy dropped it, so nothing in the application needs the framework, and the step cannot be read from the pin",
		},
		{
			name:    "a pin carried beyond the step's version by another requirement stays, noted, and that impulse checks the step",
			goMod:   upgradeGoMod("v0.2.0"),
			steps:   upgradeLedger(),
			running: check.Build{Version: "v0.3.0", FromModule: true},
			hold:    "v0.3.1",
			wantOut: []string{
				"Pin github.com/impulseframework/impulse to v0.3.0.\ngithub.com/impulseframework/impulse moved to v0.3.1, beyond the step's v0.3.0: another requirement carries it.",
				"The pinned impulse v0.3.1 renders the owned files and checks the step.",
				"Committed: upgrade: impulse v0.3.0.",
			},
			wantCalls: []string{
				toolGet + "v0.3.0", "go mod tidy", "go tool impulse render", "go generate ./...", "go tool impulse check --fix", "git add -A",
				"git commit -q -m upgrade: impulse v0.3.0 -m no code change is needed",
			},
			wantCommits: []string{"step 3, impulse v0.3.1"},
		},
		{
			name:    "a dirty tree is refused",
			goMod:   upgradeGoMod("v0.1.0"),
			steps:   upgradeLedger(),
			running: check.Build{Version: "v0.3.0", FromModule: true},
			dirty:   " M pkg/x.go\n",
			wantErr: "the working tree is not clean (1 path(s)): commit or stash first, so each step is one commit",
		},
		{
			name:    "a pin newer than the running impulse is refused",
			goMod:   upgradeGoMod("v0.3.0"),
			steps:   upgradeLedger(),
			running: check.Build{Version: "v0.2.0", FromModule: true},
			wantErr: "go.mod pins the framework at v0.3.0, newer than the running impulse v0.2.0: run the pinned one (go tool impulse upgrade)",
		},
		{
			name:      "a pinned render that could not run stops the step",
			goMod:     upgradeGoMod("v0.1.0"),
			marker:    "new\n",
			steps:     upgradeLedger(),
			running:   check.Build{Version: "(devel)"},
			fail:      "go tool impulse render",
			wantCalls: []string{toolGet + "v0.2.0", "go mod tidy", "go tool impulse render"},
			wantErr:   "go tool impulse render: refused: go tool impulse render",
		},
		{
			name:      "a pinned check that could not run, by a release before handoff --staged, stops the step without a brief",
			goMod:     upgradeGoMod("v0.0.5"),
			marker:    "new\n",
			steps:     earlyLedger(t),
			running:   check.Build{Version: "(devel)"},
			fail:      "go tool impulse check",
			wantCalls: []string{toolGet + "v0.0.6", "go mod tidy", "go tool impulse render", "go generate ./...", "go tool impulse check --fix"},
			wantErr:   "go tool impulse check: refused: go tool impulse check --fix",
		},
		{
			name:      "a pinned check that could not run, by a release with handoff --staged, stops the step before the handoff: its exit status is not a failing check's",
			goMod:     upgradeGoMod("v0.1.0"),
			marker:    "new\n",
			steps:     upgradeLedger(),
			running:   check.Build{Version: "(devel)"},
			fail:      "go tool impulse check",
			wantCalls: []string{toolGet + "v0.2.0", "go mod tidy", "go tool impulse render", "go generate ./...", "go tool impulse check --fix"},
			wantErr:   "go tool impulse check: refused: go tool impulse check --fix",
		},
		{
			name:    "a pinned handoff that could not run stops the step",
			goMod:   upgradeGoMod("v0.1.0"),
			marker:  "new\n",
			steps:   upgradeLedger(),
			running: check.Build{Version: "(devel)"},
			fail:    "go tool impulse handoff",
			report:  failingReport,
			wantCalls: []string{
				toolGet + "v0.2.0", "go mod tidy", "go tool impulse render", "go generate ./...", "go tool impulse check --fix", "git add -A",
				"go tool impulse handoff --staged",
			},
			wantErr: "go tool impulse handoff: refused: go tool impulse handoff --staged",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			files := map[string]string{"go.mod": tt.goMod}
			if tt.marker != "" {
				files[markerFile] = tt.marker
			}
			maps.Copy(files, tt.files)
			for rel, content := range files {
				if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			report := tt.report
			if report == "" {
				report = cleanReport
			}
			exec := &upgradeExec{root: root, steps: tt.steps, hold: tt.hold, drop: tt.drop, refusal: tt.refusal, generate: tt.generate, report: report, dirty: tt.dirty, fail: tt.fail, noBun: tt.noBun, bunFails: tt.bunFails}
			var out, errOut strings.Builder
			u := &upgrader{
				exec: exec, steps: tt.steps, running: tt.running, verify: tt.verify,
				owned: func(*app.App) ([]string, error) { return nil, nil },
				out:   &out, err: &errOut,
			}
			err := u.run(t.Context(), &transitionFlags{appDir: root, agent: tt.agent, agentCommand: handoff.DefaultCommand, agentArgs: tt.agentArgs, skipGenerate: tt.skipGenerate}, tt.dryRun)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("run() error = %v, want %q; output:\n%s", err, tt.wantErr, out.String())
				}
			} else if err != nil {
				t.Fatalf("run() error = %v; output:\n%s", err, out.String())
			}
			containsInOrder(t, out.String(), tt.wantOut)
			for _, fragment := range tt.wantNoOut {
				if strings.Contains(out.String(), fragment) {
					t.Errorf("output holds %q; output:\n%s", fragment, out.String())
				}
			}
			var calls []string
			for _, c := range exec.calls {
				if strings.HasPrefix(c, "git status") || strings.HasPrefix(c, "git rev-parse") || strings.HasPrefix(c, "git diff --cached") {
					continue
				}
				calls = append(calls, c)
			}
			if tt.wantCalls != nil {
				if diff := cmp.Diff(tt.wantCalls, calls); diff != "" {
					t.Errorf("commands mismatch (-want +got):\n%s", diff)
				}
			} else if len(calls) > 0 {
				t.Errorf("commands run = %v, want none", calls)
			}
			if tt.wantCommits != nil {
				if diff := cmp.Diff(tt.wantCommits, exec.commits); diff != "" {
					t.Errorf("commits mismatch (-want +got):\n%s", diff)
				}
			}
			if tt.wantMarker != "" {
				data, err := os.ReadFile(filepath.Join(root, markerFile))
				if err != nil {
					t.Fatal(err)
				}
				if string(data) != tt.wantMarker {
					t.Errorf("marker = %q, want %q", data, tt.wantMarker)
				}
			}
			for rel, want := range tt.wantFiles {
				data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
				if err != nil {
					t.Fatal(err)
				}
				if diff := cmp.Diff(want, string(data)); diff != "" {
					t.Errorf("%s mismatch (-want +got):\n%s", rel, diff)
				}
			}
			if diff := cmp.Diff(tt.wantBunDirs, exec.bunDirs); diff != "" {
				t.Errorf("bun install directories mismatch (-want +got):\n%s", diff)
			}
			brief, err := os.ReadFile(filepath.Join(root, handoff.File))
			if (err == nil) != tt.wantBrief {
				t.Errorf("brief present = %v, want %v", err == nil, tt.wantBrief)
			}
			if tt.wantInBrief != "" && !strings.Contains(string(brief), tt.wantInBrief) {
				t.Errorf("brief lacks %q:\n%s", tt.wantInBrief, brief)
			}
		})
	}
}

// TestHandoffStaged holds handoffStagedSince to a release, and reads which pinned
// impulse writes its own brief: one at or after it; one before it, or a commit between
// the release before and it, is left to the walk's report path.
func TestHandoffStaged(t *testing.T) {
	t.Parallel()

	if !ledger.IsRelease(handoffStagedSince) {
		t.Fatalf("handoffStagedSince = %q, not a release", handoffStagedSince)
	}
	tests := []struct {
		name   string
		pinned string
		want   bool
	}{
		{name: "the release that added the flag", pinned: handoffStagedSince, want: true},
		{name: "a later release", pinned: stagedLater(t), want: true},
		{name: "a release before it", pinned: earlyLedger(t)[2].Impulse, want: false},
		{name: "a commit before it, as a pseudo-version", pinned: semver.Canonical(handoffStagedSince) + "-0.20261010000000-abcdef123456", want: false},
		{name: "no pin", pinned: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := handoffStaged(tt.pinned); got != tt.want {
				t.Errorf("handoffStaged(%q) = %v, want %v", tt.pinned, got, tt.want)
			}
		})
	}
}

// TestPlanLine says what each step of a ledger does for the plan: the first moves the
// pin and every web pin from nothing, a step with a recipe names it, and a step without
// one is the pin move alone.
func TestPlanLine(t *testing.T) {
	t.Parallel()

	steps := upgradeLedger()
	tests := []struct {
		name string
		i    int
		want string
	}{
		{name: "the first step moves the pin and the web pins from nothing", i: 0, want: "step 1, impulse v0.1.0, web " + webAt("0.1.0") + ": the first release"},
		{name: "a step with a recipe names it", i: 1, want: "step 2, impulse v0.2.0, web " + webAt("0.2.0") + " (recipe marker): the marker moves to its new form"},
		{name: "a pin move alone", i: 2, want: "step 3, impulse v0.3.0, web " + webAt("0.3.0") + ": no code change is needed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := planLine(steps, tt.i); got != tt.want {
				t.Errorf("planLine() = %q, want %q", got, tt.want)
			}
		})
	}
}

// concatFiles joins file sets.
func concatFiles(sets ...map[string]string) map[string]string {
	all := map[string]string{}
	for _, set := range sets {
		maps.Copy(all, set)
	}

	return all
}

// TestRewriteWebPins moves the pins in place: every spec behind the version, in every
// section, takes it, with the file's formatting kept; a spec at or beyond it, a package
// not pinned and everything else in the file stay.
func TestRewriteWebPins(t *testing.T) {
	t.Parallel()

	pins := framework.WebPins("v0.2.0")
	tests := []struct {
		name      string
		data      string
		want      string
		wantMoved map[string]string
	}{
		{
			name:      "every section behind moves, the formatting kept",
			data:      webPackageJSON("0.1.0", "0.1.0", "0.1.0"),
			want:      webPackageJSON("0.2.0", "0.2.0", "0.2.0"),
			wantMoved: map[string]string{"@impulseframework/resource": "0.1.0", "@impulseframework/resource-angular": "0.1.0", "@impulseframework/resource-firestore": "0.1.0"},
		},
		{
			name:      "a range and a yalc attachment take the version; one ahead stays",
			data:      webPackageJSON("^0.1.0", "file:.yalc/@impulseframework/resource-angular", "0.3.0"),
			want:      webPackageJSON("0.2.0", "0.2.0", "0.3.0"),
			wantMoved: map[string]string{"@impulseframework/resource": "^0.1.0", "@impulseframework/resource-angular": "file:.yalc/@impulseframework/resource-angular"},
		},
		{
			name:      "at the pins: nothing changes",
			data:      webPackageJSON("0.2.0", "0.2.0", "0.2.0"),
			want:      webPackageJSON("0.2.0", "0.2.0", "0.2.0"),
			wantMoved: map[string]string{},
		},
		{
			name:      "a package not named is not added; the client's name is not the Angular package's",
			data:      "{\n  \"dependencies\": {\"@impulseframework/resource\": \"0.1.0\"}\n}\n",
			want:      "{\n  \"dependencies\": {\"@impulseframework/resource\": \"0.2.0\"}\n}\n",
			wantMoved: map[string]string{"@impulseframework/resource": "0.1.0"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, moved := rewriteWebPins([]byte(tt.data), pins)
			if diff := cmp.Diff(tt.want, string(got)); diff != "" {
				t.Errorf("rewriteWebPins() mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantMoved, moved); diff != "" {
				t.Errorf("moved mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// concat joins command lists.
func concat(lists ...[]string) []string {
	var all []string
	for _, l := range lists {
		all = append(all, l...)
	}

	return all
}

// containsInOrder fails unless each fragment appears in the text after the one before it.
func containsInOrder(t *testing.T, text string, fragments []string) {
	t.Helper()

	rest := text
	for _, f := range fragments {
		i := strings.Index(rest, f)
		if i < 0 {
			t.Errorf("output lacks %q (in order); output:\n%s", f, text)

			return
		}
		rest = rest[i+len(f):]
	}
}
