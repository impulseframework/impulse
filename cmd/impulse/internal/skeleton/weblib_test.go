package skeleton

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"

	"github.com/impulseframework/impulse/cmd/impulse/internal/check"
)

// browserPackages are the three packages weblib.sh attaches, in the order it names them.
var browserPackages = []string{"@impulseframework/resource", "@impulseframework/resource-angular", "@impulseframework/resource-firestore"}

// weblibRoot is a web root of a candidate that carries a weblib.sh.
type weblibRoot struct {
	candidate string
	// web is the web root, from the application's root.
	web string
}

// weblibRoots names every web root that carries a weblib.sh, so a script that is added or
// moved fails TestWeblibLocalAttachesEveryPin rather than going untested.
var weblibRoots = []weblibRoot{
	{candidate: "solo", web: "web"},
	{candidate: "tenanted", web: "web"},
	{candidate: "outlets", web: "web"},
	{candidate: "sites", web: "apps/console/web"},
	{candidate: "sites", web: "apps/portal/web"},
}

// standInYalc stands in for yalc, which the tests never reach: the real one publishes to
// the one store of the user and pushes into every application attached on the machine. It
// records each call, and on add does what yalc does to the application and no more: it sets
// each named package's spec in the dependencies section alone to its file:.yalc spec and
// makes .yalc and yalc.lock.
const standInYalc = `#!/usr/bin/env bash
set -euo pipefail
printf 'yalc\t%s\t%s\n' "$(pwd -P)" "$*" >> "$WEBLIB_RECORD/calls"
case "$*" in
  "publish --push" | "remove --all")
    ;;
  add\ *)
    shift
    for name in "$@"; do
      sed "/^  \"dependencies\": {/,/^  }/ s|\"$name\": \"[^\"]*\"|\"$name\": \"file:.yalc/$name\"|" package.json > package.json.yalc
      mv package.json.yalc package.json
      mkdir -p ".yalc/$name"
      echo "$name" >> yalc.lock
    done
    ;;
  *)
    echo "the yalc stand-in has no case for: $*" >&2
    exit 1
    ;;
esac
`

// standInBun stands in for bun. It records each call; on install it also keeps, under the
// number of the call, the package.json it saw and the bun.lock it found, and then, unless
// the lockfile is frozen, writes a bun.lock that names each spec package.json holds for
// the framework's packages, as the real one records the yalc copies of an attached install.
const standInBun = `#!/usr/bin/env bash
set -euo pipefail
printf 'bun\t%s\t%s\n' "$(pwd -P)" "$*" >> "$WEBLIB_RECORD/calls"
case "$*" in
  "run build")
    ;;
  install | install\ *)
    seen="$WEBLIB_RECORD/seen-$(wc -l < "$WEBLIB_RECORD/calls" | tr -d ' ')"
    mkdir "$seen"
    cp package.json "$seen/package.json"
    if [ -e bun.lock ]; then
      cp bun.lock "$seen/bun.lock"
    fi
    case " $* " in
      *" --frozen-lockfile "*)
        ;;
      *)
        grep -o '"@impulseframework/[^"]*": *"[^"]*"' package.json > bun.lock || true
        ;;
    esac
    ;;
  *)
    echo "the bun stand-in has no case for: $*" >&2
    exit 1
    ;;
esac
`

// weblibCall is one call a stand-in recorded.
type weblibCall struct {
	Tool string
	Dir  string
	Args string
}

// weblibPins is what the tests read of a package.json: the sections that can name the
// browser packages.
type weblibPins struct {
	Dependencies map[string]string `json:"dependencies"`
	Overrides    map[string]string `json:"overrides"`
	Resolutions  map[string]string `json:"resolutions"`
}

// weblibApp is a candidate rendered into a git repository with everything committed, a
// made-up framework checkout holding the three packages' builds, and the stand-ins for yalc
// and bun first on PATH, for running one web root's weblib.sh as an application runs it.
// Every directory is held with its symbolic links resolved, as the stand-ins record them.
type weblibApp struct {
	t *testing.T
	// dir is the application's root, a git repository.
	dir string
	// web is the web root, from dir, in slash form.
	web string
	// impulse is the made-up framework checkout.
	impulse string
	// record is where the stand-ins record their calls and what bun install saw.
	record string
	// standIns is the directory of the stand-ins.
	standIns string
}

// newWeblibApp renders the root's candidate, lets edit change the web root before
// everything is committed (edit may be nil), and makes the checkout and the stand-ins.
func newWeblibApp(t *testing.T, root weblibRoot, edit func(webDir string)) *weblibApp {
	t.Helper()

	a := &weblibApp{t: t, web: root.web}
	a.dir = filepath.Join(resolvedTempDir(t), "beacon")
	if _, err := Render(&Options{Version: "v0.1.0", WebVersion: "0.1.0", Candidate: root.candidate, Dir: a.dir, ModulePath: targetModule}); err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if edit != nil {
		edit(a.webDir())
	}
	a.git("init", "-q")
	a.git("add", "-A")
	a.git("-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "-m", "the application")

	a.impulse = resolvedTempDir(t)
	for _, build := range []string{"resource", "resource-angular", "resource-firestore"} {
		if err := os.MkdirAll(filepath.Join(a.impulse, "web", "dist", build), 0o750); err != nil {
			t.Fatalf("os.MkdirAll() error = %v", err)
		}
	}
	a.record = resolvedTempDir(t)
	a.standIns = resolvedTempDir(t)
	for name, script := range map[string]string{"yalc": standInYalc, "bun": standInBun} {
		// A stand-in is found on PATH, so it must be executable.
		if err := os.WriteFile(filepath.Join(a.standIns, name), []byte(script), 0o700); err != nil { //nolint:gosec // a stand-in the script finds on PATH must be executable
			t.Fatalf("os.WriteFile() error = %v", err)
		}
	}

	return a
}

// webDir returns the web root's directory.
func (a *weblibApp) webDir() string {
	return filepath.Join(a.dir, filepath.FromSlash(a.web))
}

// git runs git in the application and returns its output.
func (a *weblibApp) git(args ...string) string {
	a.t.Helper()

	out, err := check.OSExec{}.Run(a.t.Context(), a.dir, nil, "git", args...)
	if err != nil {
		a.t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}

	return string(out)
}

// run runs the web root's weblib.sh with one argument, the checkout as IMPULSE and the
// stand-ins first on PATH, and fails the test unless it exits 0.
func (a *weblibApp) run(arg string) {
	a.t.Helper()

	env := []string{
		"IMPULSE=" + a.impulse,
		"WEBLIB_RECORD=" + a.record,
		"PATH=" + a.standIns + string(os.PathListSeparator) + os.Getenv("PATH"),
	}
	out, err := check.OSExec{}.Run(a.t.Context(), a.webDir(), env, filepath.Join(a.webDir(), "weblib.sh"), arg)
	if err != nil {
		a.t.Fatalf("weblib.sh %s: %v\n%s", arg, err, out)
	}
}

// calls returns the calls the stand-ins recorded, in the order they were made.
func (a *weblibApp) calls() []weblibCall {
	a.t.Helper()

	raw, err := os.ReadFile(filepath.Join(a.record, "calls"))
	if err != nil {
		a.t.Fatalf("os.ReadFile() error = %v", err)
	}
	var calls []weblibCall
	for line := range strings.Lines(string(raw)) {
		fields := strings.Split(strings.TrimSuffix(line, "\n"), "\t")
		if len(fields) != 3 {
			a.t.Fatalf("a stand-in recorded %q, want a tool, a directory and the arguments", line)
		}
		calls = append(calls, weblibCall{Tool: fields[0], Dir: fields[1], Args: fields[2]})
	}

	return calls
}

// seen returns what bun install saw at the nth recorded call (counted from 1): the
// package.json, and the bun.lock with whether there was one.
func (a *weblibApp) seen(n int) (packageJSON, lockfile []byte, locked bool) {
	a.t.Helper()

	dir := filepath.Join(a.record, "seen-"+strconv.Itoa(n))
	packageJSON, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		a.t.Fatalf("call %d is not an install that recorded what it saw: %v", n, err)
	}
	lockfile, err = os.ReadFile(filepath.Join(dir, "bun.lock"))
	if errors.Is(err, fs.ErrNotExist) {
		return packageJSON, nil, false
	}
	if err != nil {
		a.t.Fatalf("os.ReadFile() error = %v", err)
	}

	return packageJSON, lockfile, true
}

// committed returns a file of the web root as the application's commit holds it.
func (a *weblibApp) committed(name string) []byte {
	a.t.Helper()

	return []byte(a.git("show", "HEAD:"+path.Join(a.web, name)))
}

// resolvedTempDir returns a new temporary directory with its symbolic links resolved.
func resolvedTempDir(t *testing.T) string {
	t.Helper()

	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("filepath.EvalSymlinks() error = %v", err)
	}

	return dir
}

// readPins reads the sections of a package.json that can name the browser packages.
func readPins(t *testing.T, packageJSON []byte) weblibPins {
	t.Helper()

	var pins weblibPins
	if err := json.Unmarshal(packageJSON, &pins); err != nil {
		t.Fatalf("json.Unmarshal() error = %v\n%s", err, packageJSON)
	}

	return pins
}

// attachedSpec is the spec weblib.sh local gives a browser package.
var attachedSpec = regexp.MustCompile(`file:\.yalc/@impulseframework/[a-z-]+`)

// TestWeblibLocalAttachesEveryPin runs weblib.sh local in each web root that carries one.
// yalc add attaches the dependencies alone; the script attaches every other spec of the
// three packages, the overrides entry first of all, or bun installs the registry's client
// beneath the attached library and the build fails. Nothing else in package.json changes.
func TestWeblibLocalAttachesEveryPin(t *testing.T) {
	t.Parallel()

	// The web roots run are named, so a weblib.sh that is added or moved fails here.
	var found []string
	err := fs.WalkDir(root, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return errors.Wrap(err, "fs.WalkDir()")
		}
		if !d.IsDir() && d.Name() == "weblib.sh" {
			found = append(found, strings.TrimPrefix(path.Dir(p), dir+"/"))
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	named := make([]string, 0, len(weblibRoots))
	for _, r := range weblibRoots {
		named = append(named, path.Join(r.candidate, r.web))
	}
	slices.Sort(named)
	slices.Sort(found)
	if diff := cmp.Diff(named, found); diff != "" {
		t.Fatalf("the web roots that carry a weblib.sh mismatch (-named +found):\n%s", diff)
	}

	for _, r := range weblibRoots {
		t.Run(path.Join(r.candidate, r.web), func(t *testing.T) {
			t.Parallel()

			a := newWeblibApp(t, r, nil)
			a.run("local")

			calls := a.calls()
			packageJSON, _, _ := a.seen(len(calls))
			pins := readPins(t, packageJSON)
			for _, name := range browserPackages {
				if got, want := pins.Dependencies[name], "file:.yalc/"+name; got != want {
					t.Errorf("dependencies[%s] when bun install ran = %q, want %q", name, got, want)
				}
			}
			if got, want := pins.Overrides[browserPackages[0]], "file:.yalc/"+browserPackages[0]; got != want {
				t.Errorf("overrides[%s] when bun install ran = %q, want %q: bun installs the registry's client in place of the attached one", browserPackages[0], got, want)
			}

			if diff := cmp.Diff(string(a.committed("package.json")), attachedSpec.ReplaceAllString(string(packageJSON), "0.1.0")); diff != "" {
				t.Errorf("package.json when bun install ran, with the published version back in each attached spec, differs from the committed one (-committed +ran):\n%s", diff)
			}

			webDir := a.webDir()
			want := []weblibCall{
				{Tool: "bun", Dir: filepath.Join(a.impulse, "web"), Args: "run build"},
				{Tool: "yalc", Dir: filepath.Join(a.impulse, "web", "dist", "resource"), Args: "publish --push"},
				{Tool: "yalc", Dir: filepath.Join(a.impulse, "web", "dist", "resource-angular"), Args: "publish --push"},
				{Tool: "yalc", Dir: filepath.Join(a.impulse, "web", "dist", "resource-firestore"), Args: "publish --push"},
				{Tool: "yalc", Dir: webDir, Args: "add " + strings.Join(browserPackages, " ")},
				{Tool: "bun", Dir: webDir, Args: "install"},
			}
			if diff := cmp.Diff(want, calls); diff != "" {
				t.Errorf("the calls weblib.sh local made mismatch (-want +got):\n%s", diff)
			}

			wantStatus := " M " + path.Join(r.web, "package.json") + "\n?? " + path.Join(r.web, "bun.lock") + "\n"
			if diff := cmp.Diff(wantStatus, a.git("status", "--porcelain")); diff != "" {
				t.Errorf("git status --porcelain after weblib.sh local mismatch (-want +got):\n%s", diff)
			}
		})
	}

	// A spec in any other section is attached too.
	t.Run("solo/web with resolutions", func(t *testing.T) {
		t.Parallel()

		a := newWeblibApp(t, weblibRoots[0], func(webDir string) {
			p := filepath.Join(webDir, "package.json")
			raw, err := os.ReadFile(p)
			if err != nil {
				t.Fatalf("os.ReadFile() error = %v", err)
			}
			withResolutions := strings.Replace(string(raw), "  \"overrides\": {", "  \"resolutions\": {\n    \"@impulseframework/resource\": \"0.1.0\"\n  },\n  \"overrides\": {", 1)
			if withResolutions == string(raw) {
				t.Fatalf("%s holds no overrides section to put resolutions before", p)
			}
			if err := os.WriteFile(p, []byte(withResolutions), 0o600); err != nil {
				t.Fatalf("os.WriteFile() error = %v", err)
			}
		})
		if got := readPins(t, a.committed("package.json")).Resolutions[browserPackages[0]]; got != "0.1.0" {
			t.Fatalf("the committed resolutions[%s] = %q, want 0.1.0", browserPackages[0], got)
		}
		a.run("local")

		packageJSON, _, _ := a.seen(len(a.calls()))
		if got, want := readPins(t, packageJSON).Resolutions[browserPackages[0]], "file:.yalc/"+browserPackages[0]; got != want {
			t.Errorf("resolutions[%s] when bun install ran = %q, want %q", browserPackages[0], got, want)
		}
	})
}

// TestWeblibRestoreGivesThePinsBack runs weblib.sh local and then weblib.sh restore in
// each web root that carries a weblib.sh: restore detaches, puts the committed package.json
// back with every pin at the published version, and installs from the registry pins.
func TestWeblibRestoreGivesThePinsBack(t *testing.T) {
	t.Parallel()

	const lockfile = "{\n  \"lockfileVersion\": 1,\n  \"@impulseframework/resource\": \"0.1.0\"\n}\n"
	tests := []struct {
		name string
		// lockfile is the web root's bun.lock the application commits, or "" for none.
		lockfile string
		// install is the arguments of the install restore ends with.
		install string
	}{
		{name: "the lockfile is committed", lockfile: lockfile, install: "install --frozen-lockfile"},
		// An application made with --dev-root starts with weblib.sh local and commits no
		// lockfile until restore leaves one.
		{name: "no lockfile is committed", install: "install"},
	}
	for _, tt := range tests {
		for _, r := range weblibRoots {
			t.Run(tt.name+"/"+path.Join(r.candidate, r.web), func(t *testing.T) {
				t.Parallel()

				var edit func(string)
				if tt.lockfile != "" {
					edit = func(webDir string) {
						write(t, webDir, "bun.lock", tt.lockfile)
					}
				}
				a := newWeblibApp(t, r, edit)
				committed := a.committed("package.json")
				a.run("local")
				a.run("restore")

				for _, name := range []string{".yalc", "yalc.lock"} {
					if _, err := os.Stat(filepath.Join(a.webDir(), name)); !errors.Is(err, fs.ErrNotExist) {
						t.Errorf("%s after weblib.sh restore: %v, want it gone", name, err)
					}
				}

				calls := a.calls()
				last := len(calls)
				for last > 0 && calls[last-1].Tool != "bun" {
					last--
				}
				if last == 0 {
					t.Fatalf("weblib.sh restore made no bun call: %v", calls)
				}
				if got, want := calls[last-1], (weblibCall{Tool: "bun", Dir: a.webDir(), Args: tt.install}); got != want {
					t.Errorf("the last bun call = %+v, want %+v", got, want)
				}
				packageJSON, lock, locked := a.seen(last)
				if diff := cmp.Diff(string(committed), string(packageJSON)); diff != "" {
					t.Errorf("package.json when the install ran differs from the committed one (-committed +ran):\n%s", diff)
				}
				if locked != (tt.lockfile != "") || string(lock) != tt.lockfile {
					t.Errorf("bun.lock when the install ran: there %t, holding %q; want there %t, holding %q", locked, lock, tt.lockfile != "", tt.lockfile)
				}

				after, err := os.ReadFile(filepath.Join(a.webDir(), "package.json"))
				if err != nil {
					t.Fatalf("os.ReadFile() error = %v", err)
				}
				if diff := cmp.Diff(string(committed), string(after)); diff != "" {
					t.Errorf("package.json after weblib.sh restore differs from the committed one (-committed +after):\n%s", diff)
				}
				wantStatus := ""
				if tt.lockfile == "" {
					// The install wrote the lockfile of the registry pins, the application's to commit.
					wantStatus = "?? " + path.Join(r.web, "bun.lock") + "\n"
					written, err := os.ReadFile(filepath.Join(a.webDir(), "bun.lock"))
					if err != nil {
						t.Fatalf("os.ReadFile() error = %v", err)
					}
					if bytes.Contains(written, []byte("file:.yalc/")) {
						t.Errorf("bun.lock after weblib.sh restore records a yalc copy:\n%s", written)
					}
				}
				if diff := cmp.Diff(wantStatus, a.git("status", "--porcelain")); diff != "" {
					t.Errorf("git status --porcelain after weblib.sh restore mismatch (-want +got):\n%s", diff)
				}
			})
		}
	}
}
