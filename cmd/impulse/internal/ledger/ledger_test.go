package ledger

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/check"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton"
	"github.com/impulseframework/impulse/cmd/impulse/internal/transition"
)

// noopRecipe is a recipe with a name and nothing to do, for the ledger's shape tests.
type noopRecipe struct{ name string }

func (r noopRecipe) Name() string { return r.name }

func (noopRecipe) Detect(context.Context, *app.App) ([]string, error) { return nil, nil }

func (noopRecipe) Apply(context.Context, *app.App, check.Execer) (*transition.Change, error) {
	return &transition.Change{}, nil
}

func (noopRecipe) Meaning() string { return "" }

// testLedger is three steps: the first release, a second that carries a recipe, and a
// third that moves the pin alone.
func testLedger() []Step {
	return []Step{
		{Impulse: "v0.1.0", Note: "the first release"},
		{Impulse: "v0.2.0", Recipes: []Recipe{noopRecipe{name: "paging"}}, Note: "pages by cursor"},
		{Impulse: "v0.3.0", Note: "no code change is needed"},
	}
}

// TestPosition reads where a pin stands: before every step, at one, between two, beyond
// the last, and at a pushed commit past a release.
func TestPosition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		pin  string
		want int
	}{
		{name: "no framework pin", pin: "", want: -1},
		{name: "before the first step", pin: "v0.0.9", want: -1},
		{name: "at the first", pin: "v0.1.0", want: 0},
		{name: "between the second and the third", pin: "v0.2.5", want: 1},
		{name: "a pseudo-version past the third", pin: "v0.3.1-0.20261005064211-ce3bc37a7307", want: 2},
		{name: "beyond the last", pin: "v1.0.0", want: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := Position(testLedger(), tt.pin); got != tt.want {
				t.Errorf("Position() = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestPending lists the steps to walk: every later one, none when the application is at
// the last.
func TestPending(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		pin  string
		want []string
	}{
		{name: "before the first: all three", pin: "", want: []string{"impulse v0.1.0", "impulse v0.2.0", "impulse v0.3.0"}},
		{name: "at the first: the two after", pin: "v0.1.0", want: []string{"impulse v0.2.0", "impulse v0.3.0"}},
		{name: "at the last: none", pin: "v0.3.0", want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.want, Names(Pending(testLedger(), tt.pin)), cmp.Transformer("nil", func(s []string) []string {
				if s == nil {
					return []string{}
				}

				return s
			})); diff != "" {
				t.Errorf("Pending() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestMoves lists what a step moves from a pin: the framework pin when it is missing or
// behind, nothing when it is at or beyond the step's release.
func TestMoves(t *testing.T) {
	t.Parallel()

	step := testLedger()[2]
	tests := []struct {
		name string
		from string
		want []string
	}{
		{name: "from nothing", from: "", want: []string{"impulse v0.3.0"}},
		{name: "from the step before", from: "v0.2.0", want: []string{"impulse v0.3.0"}},
		{name: "at the step", from: "v0.3.0", want: nil},
		{name: "bumped by hand beyond the step", from: "v0.4.0", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.want, step.Moves(tt.from)); diff != "" {
				t.Errorf("Moves() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// webPins is a web root's specs for the three packages, in package order.
func webPins(resource, angular, firestore string) map[string]string {
	return map[string]string{"@impulseframework/resource": resource, "@impulseframework/resource-angular": angular, "@impulseframework/resource-firestore": firestore}
}

// TestWeb reads a step's web pins: every package at the step's release without its v.
func TestWeb(t *testing.T) {
	t.Parallel()

	if diff := cmp.Diff(webPins("0.2.0", "0.2.0", "0.2.0"), testLedger()[1].Web()); diff != "" {
		t.Errorf("Web() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(map[string]string{FrameworkModule: "v0.2.0"}, testLedger()[1].Pins()); diff != "" {
		t.Errorf("Pins() mismatch (-want +got):\n%s", diff)
	}
}

// TestWebMoves lists what a step moves in a web root's specs: a package missing, behind,
// or held at a spec that is no exact release, never one at or beyond the step's.
func TestWebMoves(t *testing.T) {
	t.Parallel()

	step := Step{Impulse: "v0.2.0"}
	tests := []struct {
		name string
		from map[string]string
		want []string
	}{
		{name: "from nothing: every pin", from: nil, want: []string{"@impulseframework/resource 0.2.0", "@impulseframework/resource-angular 0.2.0", "@impulseframework/resource-firestore 0.2.0"}},
		{name: "from the step before: every pin moves", from: webPins("0.1.0", "0.1.0", "0.1.0"), want: []string{"@impulseframework/resource 0.2.0", "@impulseframework/resource-angular 0.2.0", "@impulseframework/resource-firestore 0.2.0"}},
		{name: "one bumped by hand ahead and one at the step: the one behind alone", from: webPins("0.3.0", "0.2.0", "0.1.0"), want: []string{"@impulseframework/resource-firestore 0.2.0"}},
		{name: "a range and a yalc attachment move", from: webPins("^0.2.0", "file:.yalc/@impulseframework/resource-angular", "0.2.0"), want: []string{"@impulseframework/resource 0.2.0", "@impulseframework/resource-angular 0.2.0"}},
		{name: "at the step: nothing", from: step.Web(), want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.want, step.WebMoves(tt.from)); diff != "" {
				t.Errorf("WebMoves() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestNames labels the steps by their releases.
func TestNames(t *testing.T) {
	t.Parallel()

	want := []string{"impulse v0.1.0", "impulse v0.2.0", "impulse v0.3.0"}
	if diff := cmp.Diff(want, Names(testLedger())); diff != "" {
		t.Errorf("Names() mismatch (-want +got):\n%s", diff)
	}
}

// TestPin reads the framework pin of a go.mod and nothing else.
func TestPin(t *testing.T) {
	t.Parallel()

	mod, err := modfile.Parse("go.mod", []byte("module example.com/acme/beacon\n\ngo 1.27\n\nrequire (\n\tgithub.com/impulseframework/impulse v0.1.2\n\tgithub.com/go-chi/chi/v5 v5.3.2\n)\n\ntool github.com/impulseframework/impulse/cmd/impulse\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := Pin(mod); got != "v0.1.2" {
		t.Errorf("Pin() = %q, want v0.1.2", got)
	}
	if got := Pin(nil); got != "" {
		t.Errorf("Pin(nil) = %q, want none", got)
	}
	none, err := modfile.Parse("go.mod", []byte("module example.com/acme/beacon\n\ngo 1.27\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := Pin(none); got != "" {
		t.Errorf("Pin() of a go.mod without the framework = %q, want none", got)
	}
}

// TestValidate holds a ledger to its shape, and the recorded ledger to it.
func TestValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		steps   []Step
		wantErr string
	}{
		{name: "the recorded ledger", steps: Steps},
		{name: "the test ledger", steps: testLedger()},
		{name: "no step", steps: []Step{}, wantErr: "the ledger records no step; the first is the framework's first release"},
		{name: "no release", steps: []Step{{Note: "n"}}, wantErr: "step 1 names no release"},
		{name: "no note", steps: []Step{{Impulse: "v0.1.0"}}, wantErr: "step 1 has no note"},
		{name: "a release that is not a version", steps: []Step{{Impulse: "latest", Note: "n"}}, wantErr: `step 1 names impulse "latest", which is not a release`},
		{name: "a pushed commit named as the release", steps: []Step{{Impulse: "v0.3.1-0.20261009052330-7bd8478e0ccf", Note: "n"}}, wantErr: `step 1 names impulse "v0.3.1-0.20261009052330-7bd8478e0ccf", which is not a release`},
		{name: "a pre-release named as the release", steps: []Step{{Impulse: "v0.3.0-rc.1", Note: "n"}}, wantErr: `step 1 names impulse "v0.3.0-rc.1", which is not a release`},
		{name: "a release with build metadata", steps: []Step{{Impulse: "v0.3.0+dirty", Note: "n"}}, wantErr: `step 1 names impulse "v0.3.0+dirty", which is not a release`},
		{name: "a release moving backward", steps: []Step{{Impulse: "v0.3.0", Note: "n"}, {Impulse: "v0.2.0", Note: "n"}}, wantErr: "step 2 names impulse v0.2.0, behind step 1's v0.3.0; the ledger walks forward only"},
		{name: "a second step at the release of the step before", steps: []Step{{Impulse: "v0.2.0", Note: "n"}, {Impulse: "v0.2.0", Note: "n", Recipes: []Recipe{noopRecipe{name: "paging"}}}}, wantErr: "step 2 names impulse v0.2.0, the release of step 1: a release is one step, read from the one pin, so its recipes go in that step"},
		{name: "a recipe named twice", steps: []Step{{Impulse: "v0.1.0", Note: "n", Recipes: []Recipe{noopRecipe{name: "paging"}, noopRecipe{name: "paging"}}}}, wantErr: "step 1 names the recipe paging twice"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := Validate(tt.steps)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Validate() error = %v, want %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

// TestSkeletonPins holds the skeleton to the one-module shape: every candidate's go.mod
// requires the framework module at the version placeholder the rendering fills, with the
// tool directive on impulse's package, and requires no other module of the framework's
// organization, which would be a second pin the ledger does not read.
func TestSkeletonPins(t *testing.T) {
	t.Parallel()

	candidates, err := skeleton.Candidates()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range candidates {
		sub, err := skeleton.FS(c.Name)
		if err != nil {
			t.Fatal(err)
		}
		data, err := fs.ReadFile(sub, skeleton.ModFile)
		if err != nil {
			t.Fatal(err)
		}
		mod, err := modfile.Parse(skeleton.ModFile, data, nil)
		if err != nil {
			t.Fatalf("candidate %s: %v", c.Name, err)
		}
		if pin := Pin(mod); pin != skeleton.VersionPlaceholder {
			t.Errorf("candidate %s requires %s at %q, want the placeholder %s the rendering fills", c.Name, FrameworkModule, pin, skeleton.VersionPlaceholder)
		}
		for _, r := range mod.Require {
			if r.Mod.Path != FrameworkModule && strings.HasPrefix(r.Mod.Path, "github.com/impulseframework/") {
				t.Errorf("candidate %s requires %s %s: the framework is one module, %s", c.Name, r.Mod.Path, r.Mod.Version, FrameworkModule)
			}
		}
		held := false
		for _, tool := range mod.Tool {
			held = held || tool.Path == check.ImpulseModule
		}
		if !held {
			t.Errorf("candidate %s carries no tool directive for %s", c.Name, check.ImpulseModule)
		}
	}
}

// TestSkeletonWebPins holds the skeleton's web roots to the one-module shape: every
// package.json a candidate carries that names a browser package pins each of the three at
// the version placeholder the rendering fills, in every section (dependencies,
// overrides), and names no other package of the organization.
func TestSkeletonWebPins(t *testing.T) {
	t.Parallel()

	candidates, err := skeleton.Candidates()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range candidates {
		sub, err := skeleton.FS(c.Name)
		if err != nil {
			t.Fatal(err)
		}
		roots := 0
		err = fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return errors.Wrap(err, "fs.WalkDir()")
			}
			if d.IsDir() || d.Name() != "package.json" || strings.Contains(p, "node_modules") {
				return nil
			}
			data, err := fs.ReadFile(sub, p)
			if err != nil {
				return errors.Wrap(err, "fs.ReadFile()")
			}
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(data, &raw); err != nil {
				return errors.Wrapf(err, "json.Unmarshal(): %s", p)
			}
			sections := map[string]map[string]string{}
			for section, entries := range raw {
				var specs map[string]string
				if json.Unmarshal(entries, &specs) == nil {
					sections[section] = specs
				}
			}
			named := false
			for section, specs := range sections {
				for name, spec := range specs {
					if !strings.HasPrefix(name, "@impulseframework/") {
						continue
					}
					named = true
					switch {
					case !contains(WebPackages, name):
						t.Errorf("candidate %s: %s %s names %s, which is not a browser package of the framework (%s)", c.Name, p, section, name, strings.Join(WebPackages, ", "))
					case spec != skeleton.WebVersionPlaceholder:
						t.Errorf("candidate %s: %s %s pins %s at %q, want the placeholder %s the rendering fills", c.Name, p, section, name, spec, skeleton.WebVersionPlaceholder)
					}
				}
			}
			if !named {
				return nil
			}
			roots++
			for _, name := range WebPackages {
				if _, ok := sections["dependencies"][name]; !ok {
					t.Errorf("candidate %s: %s names no %s among its dependencies; a web root pins every browser package", c.Name, p, name)
				}
			}

			return nil
		})
		if err != nil {
			t.Fatalf("candidate %s: %v", c.Name, err)
		}
		if roots == 0 {
			t.Errorf("candidate %s carries no web root naming the browser packages", c.Name)
		}
	}
}

func contains(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}

	return false
}

// TestCurrent reads the recorded ledger as the checks do: go.mod at the last step's
// release is at the last step and gets its web pins; one before the first step is not and
// gets none; one without the framework is neither.
func TestCurrent(t *testing.T) {
	t.Parallel()

	goMod := func(pin string) string {
		if pin == "" {
			return "module example.com/acme/beacon\n\ngo 1.27\n"
		}

		return "module example.com/acme/beacon\n\ngo 1.27\n\nrequire " + FrameworkModule + " " + pin + "\n\ntool " + check.ImpulseModule + "\n"
	}
	last := &Steps[len(Steps)-1]
	tests := []struct {
		name    string
		gomod   string
		wantAt  bool
		wantWeb map[string]string
	}{
		{name: "at the last step", gomod: goMod(last.Impulse), wantAt: true, wantWeb: last.Web()},
		{name: "beyond the last step, at a pushed commit", gomod: goMod(semver.Canonical(last.Impulse) + "-0.20261005064211-ce3bc37a7307"), wantAt: false, wantWeb: nil},
		{name: "before the first step", gomod: goMod("v0.0.1"), wantAt: false, wantWeb: nil},
		{name: "no framework pin", gomod: goMod(""), wantAt: false, wantWeb: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mod, err := modfile.Parse("go.mod", []byte(tt.gomod), nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := (Current{}).AtLastStep(mod); got != tt.wantAt {
				t.Errorf("AtLastStep() = %v, want %v", got, tt.wantAt)
			}
			if diff := cmp.Diff(tt.wantWeb, (Current{}).WebPins(mod)); diff != "" {
				t.Errorf("WebPins() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestLastStepAgainstManifest holds the ledger's last step to the repository's release
// manifest: a step names the release its pull request takes, so the last step is at or
// ahead of the version released last, never behind it. The repository's CI holds the same
// on release-please's branch (.github/scripts/release-ledger.sh), where the manifest moves.
func TestLastStepAgainstManifest(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(filepath.Join("..", "..", "..", "..", ".release-please-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]string
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	released := "v" + manifest["."]
	if !semver.IsValid(released) {
		t.Fatalf("the release manifest names %q for the module, not a version", manifest["."])
	}
	if last := Latest(); semver.Compare(last, released) < 0 {
		t.Errorf("the ledger's last step names %s and the manifest says %s was released: a step names the release its pull request takes, never one behind the last release", last, released)
	}
}
