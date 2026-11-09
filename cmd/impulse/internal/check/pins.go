package check

import (
	"context"
	"fmt"
	"runtime/debug"

	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"

	"github.com/impulseframework/impulse/cmd/impulse/internal/framework"
)

// FrameworkModule is the framework's module path: the one framework requirement in an
// application's go.mod.
const FrameworkModule = framework.Module

// ImpulseModule is impulse's own package path in the framework module: the tool directive
// every application's go.mod carries, so that CI runs go tool impulse check at the release
// go.mod names, verified by Go's checksum database, with no version written into the
// workflow. The tool and the libraries are one module, so one requirement pins both.
const ImpulseModule = framework.Tool

// develVersion is what the build information reports for a binary built from a
// checkout (go build, go run) rather than from a module version.
const develVersion = "(devel)"

// pins reads the framework pin in go.mod: the version the application requires the
// framework module at. A missing requirement is a failure. A pseudo-version or a local
// replace warns: the application builds against unreleased framework code, which is
// legitimate while a change is in flight and a problem once it is not. The impulse tool
// directive is a failure when it is missing, since CI's go tool impulse check has nothing
// to run, and the pin is a failure when the running impulse was built from a module version
// (go tool, go install module@version) other than it, since the two would render the owned
// files differently; a build from a checkout is a development build, noted and not
// compared. The remedy for a pin behind the running impulse depends on where the
// application stands against the upgrade ledger (Env.Ledger): at its last step, moving the
// pin alone is right; behind it, impulse upgrade moves the pin with each step, so the
// steps' recipes run against the shape each step expects.
type pins struct {
	// build answers the running impulse's build; nil reads the build information
	// (RunningBuild). Tests inject one.
	build func() Build
}

func (pins) Name() string { return "pins" }

func (pins) Describe() string {
	return "go.mod requires the framework module at a released version and holds the impulse tool directive, at the running impulse's version"
}

// Build is what the running impulse's build information says about it.
type Build struct {
	// Version is the main module's version: a release or pseudo-version when the binary
	// was built from a module version, what version control stamped on a build from a
	// checkout (a pseudo-version, +dirty when the tree was), or "(devel)".
	Version string
	// FromModule reports a binary built from a module version (go tool, go install
	// module@version), which carries the module's checksum; a build from a checkout (go
	// build, go run) carries none, whatever version control stamped on it.
	FromModule bool
}

// RunningBuild reads the running impulse's build information.
func RunningBuild() Build {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" {
		return Build{Version: develVersion}
	}

	return Build{Version: info.Main.Version, FromModule: info.Main.Sum != ""}
}

// RunningVersion is the running impulse's version: what impulse --version prints, and the
// version the pins check compares go.mod's impulse pin with when the build is a module's.
func RunningVersion() string {
	return RunningBuild().Version
}

func (c pins) Run(_ context.Context, env *Env) Result {
	mod := env.App.GoMod
	var warnings []string
	pinned := ""
	for _, r := range mod.Require {
		if r.Mod.Path != FrameworkModule {
			continue
		}
		pinned = r.Mod.Version
		if module.IsPseudoVersion(r.Mod.Version) {
			warnings = append(warnings, fmt.Sprintf("%s is pinned to pseudo-version %s", r.Mod.Path, r.Mod.Version))
		}
	}
	for _, r := range mod.Replace {
		if r.Old.Path != FrameworkModule {
			continue
		}
		if r.New.Version == "" {
			warnings = append(warnings, fmt.Sprintf("%s is replaced by local path %s", r.Old.Path, r.New.Path))
		} else {
			warnings = append(warnings, fmt.Sprintf("%s is replaced by %s %s", r.Old.Path, r.New.Path, r.New.Version))
		}
	}
	failures, notes := c.directive(env, pinned)

	switch {
	case len(failures) > 0:
		return fail(c.Name(), fmt.Sprintf("%d framework pin problem(s)", len(failures)), append(append(failures, warnings...), notes...)...)
	case len(warnings) > 0:
		return warn(c.Name(), fmt.Sprintf("the framework pin (%s) points at unreleased code", pinned), append(warnings, notes...)...)
	case len(notes) > 0:
		return passWithDetails(c.Name(), fmt.Sprintf("the framework pin is the released version %s; go.mod holds the impulse tool directive", pinned), notes...)
	default:
		return pass(c.Name(), fmt.Sprintf("the framework pin is the released version %s, the running impulse's; go.mod holds the impulse tool directive", pinned))
	}
}

// directive applies the pin rule: go.mod requires the framework module and carries tool
// github.com/impulseframework/impulse/cmd/impulse, and the pin is the running impulse's
// version when that impulse was built from a module version. It returns the failures and
// the notes.
func (c pins) directive(env *Env, pinned string) (failures, notes []string) {
	behind := env.Ledger != nil && !env.Ledger.AtLastStep(env.App.GoMod)
	held := false
	for _, t := range env.App.GoMod.Tool {
		if t.Path == ImpulseModule {
			held = true
		}
	}
	running := c.running()
	released := running.FromModule && semver.IsValid(running.Version)
	switch {
	case pinned == "" || !held:
		at := "<version>"
		if released {
			at = running.Version
		}
		missing := "requires no " + FrameworkModule
		if pinned != "" {
			missing = "has no tool directive for " + ImpulseModule
		}

		return []string{fmt.Sprintf("go.mod %s, so CI's go tool impulse check has nothing to run: run go get -tool %s@%s, which requires the framework module and adds the directive, then go tool impulse render, then go tool impulse check", missing, ImpulseModule, at)}, nil
	case !released:
		return nil, []string{fmt.Sprintf("go.mod pins the framework at %s; the running impulse is a development build (%s, built from a checkout), so the pin is not compared with it", pinned, running.Version)}
	case running.Version != pinned && behind:
		return []string{fmt.Sprintf("go.mod pins the framework at %s and the running impulse is %s, and the pin stands behind the ledger's last step: walk the steps with impulse upgrade (go run %s@%s upgrade), which moves the pin with each step and runs each step's recipes against the shape it expects; go get -tool ahead of the walk would skip them", pinned, running.Version, ImpulseModule, running.Version)}, nil
	case running.Version != pinned:
		return []string{fmt.Sprintf("go.mod pins the framework at %s and the running impulse is %s: move the pin with go get -tool %s@%s, then go tool impulse render, then go tool impulse check", pinned, running.Version, ImpulseModule, running.Version)}, nil
	default:
		return nil, nil
	}
}

// running is the running impulse's build, from the injected reader or the build
// information.
func (c pins) running() Build {
	if c.build != nil {
		return c.build()
	}

	return RunningBuild()
}
