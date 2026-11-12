// Package ledger records the steps that take an application from the framework release an
// earlier impulse pinned to the one the current impulse pins. The framework is one module,
// so an application's position is one version: the version its go.mod requires
// github.com/impulseframework/impulse at, which the tool directive on impulse's own
// package shares, since the tool lives in the module. A step names the release that added
// it, together with the recipes that take an application from the step before it, or a
// note that none is needed; the release's browser packages (@impulseframework/resource and
// the rest) carry the module's version, so a step's web pins follow from its release too.
// impulse upgrade walks the ledger step by step, reading where the application stands from
// the pin in its go.mod and committing each step as it goes; the pin is the checkpoint,
// and nothing else records progress. A release that changes nothing an application has to
// do adds no step, and the walk moves the pin to it last, by itself.
//
// A step is appended when a release needs something of an application beyond the pin
// move: a recipe, or a change made by hand that the note describes. A breaking change in
// the framework is not done until the step that carries it records a recipe for it here,
// or says none is needed. The repository's CI holds the last step's release to the
// release being cut, on release-please's branch where the release manifest carries it, so
// a step never names a release that the release being cut leaves behind.
//
// At a step, every web root of the application (each directory holding an angular.json
// whose package.json names the browser packages) has its pins rewritten to the release's
// version and bun install run there, so bun.lock records the registry versions, and the
// step's commit carries both files. A pin at or beyond the step's stays, as the Go pin
// does. The web-pins check holds an application's web roots to the release it stands at.
package ledger

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/go-playground/errors/v5"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/check"
	"github.com/impulseframework/impulse/cmd/impulse/internal/framework"
	"github.com/impulseframework/impulse/cmd/impulse/internal/transition"
)

// FrameworkModule is the framework's module path, whose pin in an application's go.mod
// says which step the application stands at.
const FrameworkModule = framework.Module

// WebPackages are the browser packages a step pins in every web root's package.json.
var WebPackages = framework.WebPackages

// Step is one step the ledger records.
type Step struct {
	// Impulse is the release that added the step, "v0.2.0": what an application at the
	// step builds against, the version its go.mod requires the framework at and pins the
	// impulse tool at, since the two are one module, and the version of its browser
	// packages. The release's own pull request adds the step, so a step names the release
	// that pull request will take.
	Impulse string
	// Recipes are the code changes the step needs of an application at the step before
	// it, applied in order; none when the pin move is the whole step.
	Recipes []Recipe
	// Note says in one line what the step changes for an application, for the plan and
	// the commit; "no code change is needed" when the pin is all that moves.
	Note string
}

// Recipe is one code change a step needs: a detector that names what in the application
// is in the old form, and an edit that moves only what it finds, so the recipe is safe to
// run twice and safe on an application whose pin was bumped by hand ahead of its code.
type Recipe interface {
	// Name is the recipe's short name (paging).
	Name() string
	// Detect names what in the application is in the old form, one line each (file:line:
	// what); none when the recipe has nothing to do here.
	Detect(ctx context.Context, a *app.App) ([]string, error)
	// Apply edits the application where the old form is found and reports what it did and
	// what it left to the agent.
	Apply(ctx context.Context, a *app.App, exec check.Execer) (*transition.Change, error)
	// Meaning says what the change means in this framework, for the brief.
	Meaning() string
}

// Steps are the recorded steps, oldest first. The first is the framework's first release
// as one module.
var Steps = []Step{
	{
		Impulse: release("v0.1.0"),
		Note:    "the framework's first release as one module, github.com/impulseframework/impulse: the libraries, the generator, the command line and the browser packages release together at one version, an application requires the module once and its tool directive names impulse in it, and its web roots pin the browser packages at the same version; no code change is needed",
	},
}

// Pins are the framework pins an application at the step builds against, by module path:
// the one module at the step's release.
func (s *Step) Pins() map[string]string {
	return map[string]string{FrameworkModule: s.Impulse}
}

// Web are the browser packages' versions an application at the step installs, by package:
// each at the step's release.
func (s *Step) Web() map[string]string {
	return framework.WebPins(s.Impulse)
}

// Reached reports whether an application pinned at pin stands at or beyond the step: the
// pin is at the step's release or a later version. "" (no pin) reaches nothing.
func (s *Step) Reached(pin string) bool {
	return pin != "" && semver.Compare(pin, s.Impulse) >= 0
}

// Moves lists the pins the step moves from the pin given, "impulse v0.2.0", for the plan
// and the commit: the framework pin when it is missing or behind the step's release. A pin
// already at or beyond the step's is not moved.
func (s *Step) Moves(pin string) []string {
	if s.Reached(pin) {
		return nil
	}

	return []string{framework.Label(s.Impulse)}
}

// WebNames lists the packages a step pins, sorted.
func (s *Step) WebNames() []string {
	return WebNames(s.Web())
}

// WebNames lists the packages of web pins, sorted.
func WebNames(pins map[string]string) []string {
	names := make([]string, 0, len(pins))
	for name := range pins {
		names = append(names, name)
	}
	sort.Strings(names)

	return names
}

// WebMoves lists the web pins the step moves from the specs given, "@impulseframework/resource
// 0.2.0" each in package order: a package the specs lack, hold at an earlier version or
// hold at a spec that is no exact release (a range, a tag, a yalc attachment). A pin at or
// beyond the step's is not moved.
func (s *Step) WebMoves(from map[string]string) []string {
	return WebMovesTo(s.Web(), from)
}

// WebMovesTo lists the moves from the specs given to the pins, as WebMoves does for a step.
func WebMovesTo(pins, from map[string]string) []string {
	var moves []string
	for _, name := range WebNames(pins) {
		if have, ok := from[name]; !ok || check.WebBehind(have, pins[name]) {
			moves = append(moves, name+" "+pins[name])
		}
	}

	return moves
}

// Current is the recorded ledger as the checks ask it (check.Ledger).
type Current struct{}

// AtLastStep reports whether the framework pin in go.mod reaches the recorded ledger's
// last step: no step is pending for the application, so moving the pin alone is right;
// behind it, impulse upgrade is what moves the pin.
func (Current) AtLastStep(mod *modfile.File) bool {
	return Position(Steps, Pin(mod)) == len(Steps)-1
}

// WebPins is the browser packages' versions of the step the application stands at, read
// from go.mod as the walk reads it (Position); nil before the first step.
func (Current) WebPins(mod *modfile.File) map[string]string {
	i := Position(Steps, Pin(mod))
	if i < 0 {
		return nil
	}

	return Steps[i].Web()
}

// Latest is the release the ledger's last step names: the newest release an application
// is walked to by a step, and the version of the browser packages an impulse built from a
// commit or a checkout pins, since no package is published for those.
func Latest() string {
	return Steps[len(Steps)-1].Impulse
}

// Pin reads the framework pin from an application's go.mod: the version it requires the
// framework module at, "" when it does not.
func Pin(mod *modfile.File) string {
	if mod == nil {
		return ""
	}
	for _, r := range mod.Require {
		if r.Mod.Path == FrameworkModule {
			return r.Mod.Version
		}
	}

	return ""
}

// Position is the index of the latest step the application stands at or beyond, -1 when
// it reaches none: the pin is at the step's release or a later version.
func Position(steps []Step, pin string) int {
	position := -1
	for i := range steps {
		if steps[i].Reached(pin) {
			position = i
		}
	}

	return position
}

// Pending lists the steps after the application's position, in order: none when it
// stands at the last.
func Pending(steps []Step, pin string) []Step {
	return steps[Position(steps, pin)+1:]
}

// Names labels each step by the release it moves the pin to, "impulse v0.2.0", for the
// plan and the tests.
func Names(steps []Step) []string {
	names := make([]string, 0, len(steps))
	for i := range steps {
		names = append(names, framework.Label(steps[i].Impulse))
	}

	return names
}

// Validate checks the ledger's shape: at least one step, each naming the release that
// added it (a canonical version with no pre-release or build part) and a note, no release
// behind the step before's, one step per release (the position is read from the one pin,
// which cannot tell two steps of a release apart, so a release's recipes share its step),
// and no recipe named twice in one step. The ledger's test holds Steps to it.
func Validate(steps []Step) error {
	if len(steps) == 0 {
		return errors.New("the ledger records no step; the first is the framework's first release")
	}
	for i := range steps {
		s := &steps[i]
		at := fmt.Sprintf("step %d", i+1)
		switch {
		case s.Impulse == "":
			return errors.Newf("%s names no release: a step names the release that added it (v0.2.0), the version its pull request will take", at)
		case !IsRelease(s.Impulse):
			return errors.Newf("%s names impulse %q, which is not a release: a step names the tagged version of the impulse that added it (v0.2.0), the version its pull request will take", at, s.Impulse)
		case strings.TrimSpace(s.Note) == "":
			return errors.Newf("%s has no note", at)
		}
		if i > 0 {
			prev := &steps[i-1]
			switch {
			case semver.Compare(s.Impulse, prev.Impulse) < 0:
				return errors.Newf("%s names impulse %s, behind step %d's %s; the ledger walks forward only", at, s.Impulse, i, prev.Impulse)
			case s.Impulse == prev.Impulse:
				return errors.Newf("%s names impulse %s, the release of step %d: a release is one step, read from the one pin, so its recipes go in that step", at, s.Impulse, i)
			}
		}
		seen := map[string]bool{}
		for _, recipe := range s.Recipes {
			if seen[recipe.Name()] {
				return errors.Newf("%s names the recipe %s twice", at, recipe.Name())
			}
			seen[recipe.Name()] = true
		}
	}

	return nil
}

// IsRelease reports whether the version is a release: canonical, with no pre-release or
// build part, so a pushed commit's pseudo-version is not one.
func IsRelease(version string) bool {
	return semver.IsValid(version) && semver.Canonical(version) == version && semver.Prerelease(version) == ""
}

// release reads a step's release, "v0.2.0". The ledger is a table the build checks, so a
// malformed version stops the program at start rather than reading as a release.
func release(version string) string {
	if !IsRelease(version) {
		panic(fmt.Sprintf("ledger: malformed impulse release %q", version))
	}

	return version
}
