package check

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/impulseframework/impulse/app"
)

// exitStatus is the error a fake command exits non-zero with, carrying the status as the
// error of a process does.
type exitStatus int

func (e exitStatus) Error() string {
	return "exit status " + strconv.Itoa(int(e))
}

func (e exitStatus) ExitCode() int {
	return int(e)
}

// prettierRun is what a fake prettier answers in one web root: the listing's output and
// exit status, and the output and error of a write.
type prettierRun struct {
	listing    string
	status     int
	writeOut   string
	writeFails bool
}

// fakePrettier stands in for the web roots' prettier: it answers by the root it is run in
// and records every command line, the root first.
type fakePrettier struct {
	t     *testing.T
	root  string
	runs  map[string]prettierRun
	calls []string
}

func (f *fakePrettier) Run(_ context.Context, dir string, _ []string, name string, args ...string) ([]byte, error) {
	rel, err := filepath.Rel(f.root, dir)
	if err != nil {
		f.t.Fatal(err)
	}
	rel = filepath.ToSlash(rel)
	if want := filepath.Join(dir, filepath.FromSlash(prettierBin)); name != want {
		f.t.Fatalf("ran %q in %s, want the root's own prettier %q", name, rel, want)
	}
	f.calls = append(f.calls, rel+": prettier "+strings.Join(args, " "))
	run, ok := f.runs[rel]
	if !ok {
		f.t.Fatalf("prettier ran in %s, which the case does not expect", rel)
	}
	if args[0] == "--write" {
		if run.writeFails {
			return []byte(run.writeOut), exitStatus(2)
		}

		return []byte(run.writeOut), nil
	}
	if run.status != 0 {
		return []byte(run.listing), exitStatus(run.status)
	}

	return []byte(run.listing), nil
}

// TestPrettier holds the check's reading of a web root and of what its prettier answers:
// a root is checked only where its package.json pins a prettier and the install has put
// it there, the files prettier lists fail by name under their root, --fix writes those
// files and no others, a run that could not read the root fails in prettier's words, and
// an application none of whose roots is installed is said to be not checked.
func TestPrettier(t *testing.T) {
	t.Parallel()

	const pinned = `{"name": "x-web", "devDependencies": {"prettier": "3.9.10"}}`
	const unpinned = `{"name": "x-web", "devDependencies": {"typescript": "~6.0.3"}}`
	tests := []struct {
		name string
		// roots are the web roots' package.json files by web directory; installed are the
		// roots whose install has put prettier there.
		roots     map[string]string
		installed []string
		runs      map[string]prettierRun
		fix       bool

		wantStatus  Status
		wantSummary string
		wantDetails []string
		wantCalls   []string
	}{
		{
			name:        "every root formatted",
			roots:       map[string]string{"web": pinned, "apps/portal/web": pinned},
			installed:   []string{"web", "apps/portal/web"},
			runs:        map[string]prettierRun{"web": {}, "apps/portal/web": {}},
			wantStatus:  Pass,
			wantSummary: "the browser files of 2 web root(s) are formatted as its prettier formats them",
			wantCalls:   []string{"apps/portal/web: prettier --list-different .", "web: prettier --list-different ."},
		},
		{
			name:        "the files prettier would change fail by name, under their root",
			roots:       map[string]string{"web": pinned},
			installed:   []string{"web"},
			runs:        map[string]prettierRun{"web": {listing: "console/src/app/app.routes.ts\nconsole/src/app/components/ui/sign-in/tenant-sign-in.component.html\n", status: 1}},
			wantStatus:  Fail,
			wantSummary: "2 browser file(s) not formatted as the web root's prettier formats them (--fix formats them)",
			wantDetails: []string{
				"web/console/src/app/app.routes.ts: not formatted as prettier formats it",
				"web/console/src/app/components/ui/sign-in/tenant-sign-in.component.html: not formatted as prettier formats it",
			},
			wantCalls: []string{"web: prettier --list-different ."},
		},
		{
			name:        "a root whose packages are not installed is said to be not checked, and nothing fails",
			roots:       map[string]string{"web": pinned},
			wantStatus:  Skip,
			wantSummary: "not run: the web roots' packages are not installed, and prettier comes with them; install them, then run impulse check --only prettier --fix once",
			wantDetails: []string{"web: not checked, its packages are not installed (no node_modules/.bin/prettier)"},
		},
		{
			name:        "an installed root is checked beside one that is not, which is named",
			roots:       map[string]string{"apps/console/web": pinned, "apps/portal/web": pinned},
			installed:   []string{"apps/console/web"},
			runs:        map[string]prettierRun{"apps/console/web": {}},
			wantStatus:  Pass,
			wantSummary: "the browser files of 1 web root(s) are formatted as its prettier formats them",
			wantDetails: []string{"apps/portal/web: not checked, its packages are not installed (no node_modules/.bin/prettier)"},
			wantCalls:   []string{"apps/console/web: prettier --list-different ."},
		},
		{
			name:        "a root pinning no prettier has nothing to be held to",
			roots:       map[string]string{"web": unpinned},
			installed:   []string{"web"},
			wantStatus:  Skip,
			wantSummary: "not run: no web root pins a prettier to hold its files to",
			wantDetails: []string{"web: not checked, its package.json pins no prettier (the skeletons pin one in devDependencies)"},
		},
		{
			name:        "--fix formats the files prettier listed and says which",
			roots:       map[string]string{"web": pinned},
			installed:   []string{"web"},
			runs:        map[string]prettierRun{"web": {listing: "[warn] a note beside the listing\nconsole/src/app/app.routes.ts\n", status: 1, writeOut: "console/src/app/app.routes.ts 12ms\n"}},
			fix:         true,
			wantStatus:  Pass,
			wantSummary: "1 browser file(s) formatted; 1 web root(s) checked",
			wantDetails: []string{"web/console/src/app/app.routes.ts"},
			wantCalls:   []string{"web: prettier --list-different .", "web: prettier --write console/src/app/app.routes.ts"},
		},
		{
			name:        "a run that could not read the root fails in prettier's words",
			roots:       map[string]string{"web": pinned},
			installed:   []string{"web"},
			runs:        map[string]prettierRun{"web": {listing: "[error] console/src/app/app.routes.ts: SyntaxError: ';' expected. (3:9)\n", status: 2}},
			wantStatus:  Fail,
			wantSummary: "prettier could not read the files of web (it exited 2)",
			wantDetails: []string{"[error] console/src/app/app.routes.ts: SyntaxError: ';' expected. (3:9)"},
			wantCalls:   []string{"web: prettier --list-different ."},
		},
		{
			name:        "a write that fails is a failure, in prettier's words",
			roots:       map[string]string{"web": pinned},
			installed:   []string{"web"},
			runs:        map[string]prettierRun{"web": {listing: "console/src/app/app.routes.ts\n", status: 1, writeOut: "[error] EACCES: permission denied\n", writeFails: true}},
			fix:         true,
			wantStatus:  Fail,
			wantSummary: "prettier could not format the files of web",
			wantDetails: []string{"[error] EACCES: permission denied"},
			wantCalls:   []string{"web: prettier --list-different .", "web: prettier --write console/src/app/app.routes.ts"},
		},
		{
			name:        "no browser apps",
			wantStatus:  Skip,
			wantSummary: "no browser apps",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			a := &app.App{Root: root}
			for dir, pkg := range tt.roots {
				a.WebApps = append(a.WebApps, app.WebApp{Dir: dir})
				writeFile(t, filepath.Join(root, filepath.FromSlash(dir), "package.json"), pkg)
			}
			for _, dir := range tt.installed {
				writeFile(t, filepath.Join(root, filepath.FromSlash(dir), filepath.FromSlash(prettierBin)), "#!/usr/bin/env node\n")
			}
			exec := &fakePrettier{t: t, root: root, runs: tt.runs}

			got := prettierCheck{}.Run(context.Background(), &Env{App: a, Exec: exec, Fix: tt.fix})
			if got.Status != tt.wantStatus {
				t.Fatalf("Status = %s, want %s (%s: %v)", got.Status, tt.wantStatus, got.Summary, got.Details)
			}
			if got.Summary != tt.wantSummary {
				t.Errorf("Summary = %q, want %q", got.Summary, tt.wantSummary)
			}
			if diff := cmp.Diff(tt.wantDetails, got.Details); diff != "" {
				t.Errorf("Details mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantCalls, exec.calls); diff != "" {
				t.Errorf("prettier's runs mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// writeFile writes content at an absolute path, creating its directories.
func writeFile(t *testing.T, abs, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// requirePrettier, set in the environment, makes the test below fail where it would skip:
// CI sets it in a job that has installed the browser packages, so the refusal is proven
// there on every run and a missing prettier never passes for one.
const requirePrettier = "IMPULSE_REQUIRE_PRETTIER"

// TestPrettierRefusesALineItWouldWrap runs the check with a real prettier, the one the
// framework's own browser packages pin (web/, the version the skeletons pin), over a web
// root holding a line longer than the root's print width: the check fails naming that
// file and no other, --fix wraps the line, and the check then passes. It is the proof
// that the check judges something; the table above holds only how its answer is read. It
// needs the browser packages installed (bun install in web/) and skips without them,
// unless IMPULSE_REQUIRE_PRETTIER is set.
func TestPrettierRefusesALineItWouldWrap(t *testing.T) {
	t.Parallel()

	installed, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "web", filepath.FromSlash(prettierBin)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(installed); err != nil {
		if os.Getenv(requirePrettier) != "" {
			t.Fatalf("%s is set and the framework's browser packages are not installed (%s): run bun install in web/", requirePrettier, installed)
		}
		t.Skipf("the framework's browser packages are not installed (%s); run bun install in web/ to run this test", installed)
	}

	const fine = "export const greeting = 'hello';\n"
	const long = "export const routes = [\n  {\n    path: 'signed-out',\n    loadComponent: () => import('@impulseframework/resource-angular/auth-sign-out').then((comp) => comp.SignOutComponent),\n  },\n];\n"
	const wrapped = "export const routes = [\n  {\n    path: 'signed-out',\n    loadComponent: () =>\n      import('@impulseframework/resource-angular/auth-sign-out').then((comp) => comp.SignOutComponent),\n  },\n];\n"

	root := t.TempDir()
	web := filepath.Join(root, "web")
	writeFile(t, filepath.Join(web, "package.json"), "{\n  \"name\": \"x-web\",\n  \"devDependencies\": {\n    \"prettier\": \"3.9.10\"\n  }\n}\n")
	writeFile(t, filepath.Join(web, ".prettierrc"), "{\n  \"singleQuote\": true,\n  \"trailingComma\": \"all\",\n  \"printWidth\": 120\n}\n")
	writeFile(t, filepath.Join(web, "console", "src", "app", "greeting.ts"), fine)
	writeFile(t, filepath.Join(web, "console", "src", "app", "app.routes.ts"), long)
	// The generated client is the generator's, and the root's ignore file keeps it out.
	writeFile(t, filepath.Join(web, ".prettierignore"), "console/src/app/core/service/zz_gen_*.ts\n")
	writeFile(t, filepath.Join(web, "console", "src", "app", "core", "service", "zz_gen_api.ts"), "export const generated = {a:1,\n  b:2}\n")
	bin := filepath.Join(web, filepath.FromSlash(prettierBin))
	if err := os.MkdirAll(filepath.Dir(bin), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(installed, bin); err != nil {
		t.Fatal(err)
	}
	a := &app.App{Root: root, WebApps: []app.WebApp{{Dir: "web"}}}

	refused := prettierCheck{}.Run(context.Background(), &Env{App: a, Exec: OSExec{}})
	if refused.Status != Fail {
		t.Fatalf("the check over a line prettier would wrap: Status = %s, want %s (%s: %v)", refused.Status, Fail, refused.Summary, refused.Details)
	}
	if diff := cmp.Diff([]string{"web/console/src/app/app.routes.ts: not formatted as prettier formats it"}, refused.Details); diff != "" {
		t.Errorf("the refusal's details mismatch (-want +got):\n%s", diff)
	}

	fixed := prettierCheck{}.Run(context.Background(), &Env{App: a, Exec: OSExec{}, Fix: true})
	if fixed.Status != Pass || fixed.Summary != "1 browser file(s) formatted; 1 web root(s) checked" {
		t.Fatalf("--fix: %s %q %v, want PASS with one file formatted", fixed.Status, fixed.Summary, fixed.Details)
	}
	got, err := os.ReadFile(filepath.Join(web, "console", "src", "app", "app.routes.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(wrapped, string(got)); diff != "" {
		t.Errorf("the file after --fix mismatch (-want +got):\n%s", diff)
	}

	clean := prettierCheck{}.Run(context.Background(), &Env{App: a, Exec: OSExec{}})
	if clean.Status != Pass {
		t.Errorf("the check after --fix: Status = %s, want %s (%s: %v)", clean.Status, Pass, clean.Summary, clean.Details)
	}
}
