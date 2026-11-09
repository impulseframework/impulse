package cli

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"

	"github.com/impulseframework/impulse/cmd/impulse/internal/check"
	"github.com/impulseframework/impulse/cmd/impulse/internal/framework"
	"github.com/impulseframework/impulse/cmd/impulse/internal/ledger"
	transition_ "github.com/impulseframework/impulse/cmd/impulse/internal/transition"
)

// fakeGo records the commands impulse new runs, each as its directory's name, its extra
// environment and its words, and fails the command fail names by its first words ("go get
// -tool", "go mod tidy") as go does when the module proxy is off.
type fakeGo struct {
	mu    sync.Mutex
	calls []string
	fail  string
}

func (f *fakeGo) Run(_ context.Context, dir string, extraEnv []string, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	words := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, strings.Join(append(append([]string{filepath.Base(dir)}, extraEnv...), words), " "))
	if f.fail != "" && strings.HasPrefix(words, f.fail) {
		return []byte("go: module lookup disabled by GOPROXY=off"), errors.New("exit status 1")
	}

	return nil, nil
}

// goModPins reads a rendered application's go.mod: the version it requires the framework
// at, whether it holds the tool directive on impulse's package, and the path it replaces
// the framework module with, "" when it does not.
func goModPins(t *testing.T, dir string) (version string, tool bool, replace string) {
	t.Helper()

	file := filepath.Join(dir, goModFile)
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	mod, err := modfile.Parse(file, data, nil)
	if err != nil {
		t.Fatalf("modfile.Parse(%s) error = %v", file, err)
	}
	for _, tl := range mod.Tool {
		if tl.Path == check.ImpulseModule {
			tool = true
		}
	}
	for _, r := range mod.Replace {
		if r.Old.Path == framework.Module {
			replace = r.New.Path
		}
	}

	return ledger.Pin(mod), tool, replace
}

// webSpecs reads the specs the rendered application's web roots pin the browser packages
// at, in every section, distinct and sorted: one spec when the rendering wrote one version
// everywhere.
func webSpecs(t *testing.T, dir string) []string {
	t.Helper()

	roots, err := check.WebRoots(mustDiscover(t, dir), framework.WebPackages)
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) == 0 {
		t.Fatal("the rendered application has no web root naming the browser packages")
	}
	specs := map[string]bool{}
	for i := range roots {
		for _, p := range roots[i].Pins {
			specs[p.Version] = true
		}
	}

	return slices.Sorted(maps.Keys(specs))
}

// frameworkCheckout is a directory that passes for a checkout of the framework: a go.mod
// declaring its module, which the rendering under --dev-root holds the checkout to.
func frameworkCheckout(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, goModFile), []byte("module "+framework.Module+"\n\ngo 1.27\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	return dir
}

// TestNew runs impulse new over the builds an impulse can be: a release pins the framework
// at its own version, the browser packages at it, and resolves the pin with go get -tool
// before go mod tidy settles the requirements; an impulse installed from a commit pins
// that commit's pseudo-version and the browser packages at the latest release's; a build
// from a checkout, or one reporting (devel), is refused before anything is written; with
// --dev-root the framework is pinned at the zero pseudo-version and replaced by the
// checkout, whatever the build, and tidy alone runs; and a go get or a tidy that fails
// stops before the first commit, the tree rendered.
func TestNew(t *testing.T) {
	t.Parallel()

	const (
		release = "v0.4.0"
		commit  = "v0.0.0-20261004053859-89401d630235"
	)
	latestWeb := framework.WebVersion(ledger.Latest())
	refused := func(version string) string {
		return "an application pins the framework at the impulse that renders it, and this impulse is a build from a checkout (" + version + "), which names no version the module proxy serves: install impulse from a release or a pushed commit (go install github.com/impulseframework/impulse/cmd/impulse@<version|commit>) and run it again, or name the framework checkout to build against with --dev-root; nothing was written"
	}
	tests := []struct {
		name    string
		build   check.Build
		devRoot bool
		fail    string
		// wantPin is go.mod's framework pin after the run, wantWeb the one spec every web
		// root pins the browser packages at; wantCalls the go commands run; wantLine a
		// line of the report and wantNoLine a fragment it must not hold; rendered says a
		// refused run leaves the rendered tree in place; wantErr the error.
		wantPin    string
		wantWeb    string
		wantCalls  []string
		wantLine   string
		wantNoLine string
		rendered   bool
		wantErr    string
	}{
		{
			name:      "a released impulse pins its own version, the browser packages at it, and resolves the pin before tidying",
			build:     check.Build{Version: release, FromModule: true},
			wantPin:   release,
			wantWeb:   "0.4.0",
			wantCalls: []string{"app GOWORK=off go get -tool github.com/impulseframework/impulse/cmd/impulse@" + release, "app GOWORK=off go mod tidy"},
			wantLine:  "Pinned the framework in go.mod at " + release + ", the impulse that rendered the application, so CI's go tool impulse check runs the same code; the web roots pin the browser packages to match.",
		},
		{
			name:      "an impulse installed from a commit pins that commit's pseudo-version, the browser packages at the latest release's",
			build:     check.Build{Version: commit, FromModule: true},
			wantPin:   commit,
			wantWeb:   latestWeb,
			wantCalls: []string{"app GOWORK=off go get -tool github.com/impulseframework/impulse/cmd/impulse@" + commit, "app GOWORK=off go mod tidy"},
			wantLine:  "Pinned the framework in go.mod at " + commit + ", the impulse that rendered the application, so CI's go tool impulse check runs the same code; the web roots pin the browser packages to match.",
		},
		{
			name:    "a build from a checkout is refused, whatever version control stamped on it",
			build:   check.Build{Version: commit + "+dirty"},
			wantErr: refused(commit + "+dirty"),
		},
		{
			name:    "a build that reports (devel) is refused",
			build:   check.Build{Version: "(devel)"},
			wantErr: refused("(devel)"),
		},
		{
			name:       "with --dev-root a build from a checkout pins the zero pseudo-version, replaces the module with the checkout, and tidies alone",
			build:      check.Build{Version: "(devel)"},
			devRoot:    true,
			wantPin:    module.ZeroPseudoVersion(""),
			wantWeb:    latestWeb,
			wantCalls:  []string{"app GOWORK=off go mod tidy"},
			wantNoLine: "Pinned the framework",
		},
		{
			name:       "with --dev-root a release builds against the checkout too",
			build:      check.Build{Version: release, FromModule: true},
			devRoot:    true,
			wantPin:    module.ZeroPseudoVersion(""),
			wantWeb:    latestWeb,
			wantCalls:  []string{"app GOWORK=off go mod tidy"},
			wantNoLine: "Pinned the framework",
		},
		{
			name:      "a pin the module proxy cannot resolve stops before the first commit, the tree rendered",
			build:     check.Build{Version: commit, FromModule: true},
			fail:      "go get -tool",
			wantCalls: []string{"app GOWORK=off go get -tool github.com/impulseframework/impulse/cmd/impulse@" + commit},
			rendered:  true,
			wantErr:   "go get -tool github.com/impulseframework/impulse/cmd/impulse@" + commit + " could not resolve the framework pin through the module proxy (exit status 1): go: module lookup disabled by GOPROXY=off. ",
		},
		{
			name:      "a tidy that cannot settle the requirements stops before the first commit, the tree rendered",
			build:     check.Build{Version: release, FromModule: true},
			fail:      "go mod tidy",
			wantCalls: []string{"app GOWORK=off go get -tool github.com/impulseframework/impulse/cmd/impulse@" + release, "app GOWORK=off go mod tidy"},
			rendered:  true,
			wantErr:   "go mod tidy could not settle the requirements of the rendered application (exit status 1): go: module lookup disabled by GOPROXY=off. ",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := filepath.Join(t.TempDir(), "app")
			run := &fakeGo{fail: tt.fail}
			cmd := newNewWith(newDeps{build: func() check.Build { return tt.build }, run: run})
			args := []string{dir, "--module", "example.com/acme/beacon", "--auth", "staff", "--skip-git"}
			checkout := ""
			if tt.devRoot {
				checkout = frameworkCheckout(t)
				args = append(args, "--dev-root", checkout)
			}
			cmd.SetArgs(args)
			var out strings.Builder
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			err := cmd.Execute()
			if diff := cmp.Diff(tt.wantCalls, run.calls); diff != "" {
				t.Errorf("go commands mismatch (-want +got):\n%s", diff)
			}
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Execute() error = %v, want containing %q", err, tt.wantErr)
				}
				if tt.rendered {
					if !strings.Contains(err.Error(), dir+" is rendered but not committed") {
						t.Errorf("Execute() error = %v, want it to say %s is rendered but not committed", err, dir)
					}

					return
				}
				if _, statErr := os.Stat(dir); !errors.Is(statErr, os.ErrNotExist) {
					t.Errorf("the refused run wrote %s (stat error %v); want nothing written", dir, statErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			version, tool, replace := goModPins(t, dir)
			if version != tt.wantPin || !tool {
				t.Errorf("go.mod pins the framework at %q with the tool directive %v; want %q with it", version, tool, tt.wantPin)
			}
			if diff := cmp.Diff([]string{tt.wantWeb}, webSpecs(t, dir)); diff != "" {
				t.Errorf("web roots' specs mismatch (-want +got):\n%s", diff)
			}
			wantLine := tt.wantLine
			switch {
			case tt.devRoot && replace == "":
				t.Errorf("go.mod replaces the framework with nothing; want the checkout %s", checkout)
			case tt.devRoot:
				got := filepath.FromSlash(replace)
				if !filepath.IsAbs(got) {
					got = filepath.Join(dir, got)
				}
				if got != checkout {
					t.Errorf("go.mod replaces the framework with %s, which resolves to %s; want %s", replace, got, checkout)
				}
				wantLine = "go.mod replaces the framework module with the checkout at " + replace + ", so the application builds against it; that line is not for committing."
			case replace != "":
				t.Errorf("go.mod replaces the framework with %s; want no replace", replace)
			}
			if wantLine != "" && !strings.Contains(out.String(), wantLine+"\n") {
				t.Errorf("output lacks the line %q:\n%s", wantLine, out.String())
			}
			if tt.wantNoLine != "" && strings.Contains(out.String(), tt.wantNoLine) {
				t.Errorf("output holds %q:\n%s", tt.wantNoLine, out.String())
			}
		})
	}
}

// TestFrameworkPins reads what a rendering pins the framework at from the running build:
// a release its own version for the module and the browser packages, resolved through
// the proxy; a commit its pseudo-version, the browser packages at the latest release's; a
// checkout build nothing, refused; and under a dev root the zero pseudo-version, not
// resolved, the browser packages at the latest release's, whatever the build.
func TestFrameworkPins(t *testing.T) {
	t.Parallel()

	const (
		release = "v0.4.0"
		commit  = "v0.0.0-20261004053859-89401d630235"
	)
	latestWeb := framework.WebVersion(ledger.Latest())
	tests := []struct {
		name    string
		build   check.Build
		devRoot string
		want    frameworkPin
		wantErr string
	}{
		{name: "a release", build: check.Build{Version: release, FromModule: true}, want: frameworkPin{version: release, web: "0.4.0", resolve: true}},
		{name: "a commit", build: check.Build{Version: commit, FromModule: true}, want: frameworkPin{version: commit, web: latestWeb, resolve: true}},
		{name: "a checkout build stamped by version control", build: check.Build{Version: commit + "+dirty"}, wantErr: "this impulse is a build from a checkout (" + commit + "+dirty), which names no version the module proxy serves"},
		{name: "a checkout build reporting (devel)", build: check.Build{Version: "(devel)"}, wantErr: "this impulse is a build from a checkout ((devel)), which names no version the module proxy serves"},
		{name: "a dev root under a checkout build", build: check.Build{Version: "(devel)"}, devRoot: "../impulse", want: frameworkPin{version: module.ZeroPseudoVersion(""), web: latestWeb}},
		{name: "a dev root under a release", build: check.Build{Version: release, FromModule: true}, devRoot: "../impulse", want: frameworkPin{version: module.ZeroPseudoVersion(""), web: latestWeb}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := frameworkPins(tt.build, tt.devRoot)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("frameworkPins() error = %v, want containing %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("frameworkPins() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got, cmp.AllowUnexported(frameworkPin{})); diff != "" {
				t.Errorf("frameworkPins() mismatch (-want +got):\n%s", diff)
			}
			wantResolved := ""
			if tt.want.resolve {
				wantResolved = tt.want.version
			}
			if resolved := got.resolved(); resolved != wantResolved {
				t.Errorf("resolved() = %q, want %q", resolved, wantResolved)
			}
		})
	}
}

func TestComposedOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		opts          composedOptions
		want          []transition
		wantErr       string
		wantDescribe  string
		wantReference string
	}{
		{name: "nothing composed", opts: composedOptions{tenantTable: "Tenants"}, wantReference: transition_.TenancyReferenceCandidate},
		{
			name: "tenancy alone", opts: composedOptions{tenancy: true, tenantTable: "Clients"},
			want:         []transition{transition_.Tenancy{Table: "Clients"}},
			wantDescribe: "tenancy (Clients)", wantReference: transition_.TenancyReferenceCandidate,
		},
		{
			name: "tenancy then outlets, in the order given", opts: composedOptions{tenancy: true, tenantTable: "Tenants", outlets: []string{"portal=portal/api", "kiosk=kiosk/api"}, apiOutlets: []string{"machines=machines"}},
			want: []transition{
				transition_.Tenancy{Table: "Tenants"},
				transition_.Outlet{Name: "portal", Prefix: "portal/api", Sessions: true},
				transition_.Outlet{Name: "kiosk", Prefix: "kiosk/api", Sessions: true},
				transition_.Outlet{Name: "machines", Prefix: "machines"},
			},
			wantDescribe:  "tenancy (Tenants), the session outlet portal=portal/api, the session outlet kiosk=kiosk/api, the machine outlet machines=machines",
			wantReference: transition_.ReferenceCandidate,
		},
		{
			name: "sites promote the base and add the rest", opts: composedOptions{tenancy: true, tenantTable: "Tenants", sites: []string{"console", "portal", "kiosk"}},
			want: []transition{
				transition_.Tenancy{Table: "Tenants"},
				transition_.Site{Name: "portal", Existing: "console"},
				transition_.Site{Name: "kiosk"},
			},
			wantDescribe:  "tenancy (Tenants), the sites console, portal, kiosk (the base site becomes console)",
			wantReference: transition_.SitesReference,
		},
		{
			name: "the file store composes after the outlets and before the sites", opts: composedOptions{files: true, apiOutlets: []string{"machines=machines"}, sites: []string{"console", "portal"}},
			want: []transition{
				transition_.Outlet{Name: "machines", Prefix: "machines"},
				transition_.Files{},
				transition_.Site{Name: "portal", Existing: "console"},
			},
			wantDescribe:  "the machine outlet machines=machines, the file store, the sites console, portal (the base site becomes console)",
			wantReference: transition_.SitesReference,
		},
		{name: "one site is no layout", opts: composedOptions{sites: []string{"console"}}, wantErr: "name at least two sites"},
		{name: "an outlet without a prefix", opts: composedOptions{outlets: []string{"portal"}}, wantErr: `--outlet "portal": name the outlet and its prefix`},
		{name: "an API outlet without a name", opts: composedOptions{apiOutlets: []string{"=machines"}}, wantErr: `--api-outlet "=machines"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := tt.opts.transitions()
			switch {
			case tt.wantErr != "":
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("transitions() error = %v, want %q", err, tt.wantErr)
				}

				return
			case err != nil:
				t.Fatalf("transitions() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("transitions() mismatch (-want +got):\n%s", diff)
			}
			if d := tt.opts.describe(); d != tt.wantDescribe {
				t.Errorf("describe() = %q, want %q", d, tt.wantDescribe)
			}
			if r := tt.opts.reference(); r != tt.wantReference {
				t.Errorf("reference() = %q, want %q", r, tt.wantReference)
			}
		})
	}
}
