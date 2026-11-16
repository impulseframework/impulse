package check

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/go-playground/errors/v5"

	"github.com/impulseframework/impulse/app"
)

// prettierCheck verifies that every browser file the application's author owns is
// formatted as the prettier its web root pins formats it, as gofmtCheck holds the Go
// files to gofmt. Nothing else does: the browser apps' lint does not run prettier, and
// the commands that write browser files under the application's own names (impulse new,
// impulse add tenancy) cannot wrap the lines again, since the formatter exists only once
// the web root's packages are installed and those commands install nothing. So a web root
// whose packages are not installed is said to be not checked and never fails, and one
// whose packages are installed is held to its own prettier (node_modules/.bin/prettier),
// run over the whole root with the root's .prettierrc, .prettierignore and .gitignore, so
// the generated client and the build's output stay out of it.
type prettierCheck struct{}

func (prettierCheck) Name() string {
	return "prettier"
}

func (prettierCheck) Describe() string {
	return "every web root's browser files are formatted as the prettier it pins formats them, where its packages are installed (--fix formats them)"
}

func (prettierCheck) Meaning() string {
	return "The web root pins a prettier and nothing but this check holds the browser files to it, so a file a command wrote under a longer name, or an edit that left a line unwrapped, stays that way until it is formatted; format the files it names (impulse check --only prettier --fix, once the web root's packages are installed) and change nothing else in them."
}

// prettierBin is the formatter as a web root's install leaves it, relative to the root;
// prettierPackage is the name the root's package.json pins it under.
const (
	prettierBin     = "node_modules/.bin/prettier"
	prettierPackage = "prettier"
)

// The exit statuses prettier ends a listing with: every file formatted, and at least one
// file it would change. Any other status is a run that could not read what it was given.
const (
	prettierClean     = 0
	prettierDifferent = 1
)

func (c prettierCheck) Run(ctx context.Context, env *Env) Result {
	a := env.App
	if len(a.WebApps) == 0 {
		return skip(c.Name(), "no browser apps")
	}
	dirs := make([]string, 0, len(a.WebApps))
	for _, w := range a.WebApps {
		dirs = append(dirs, w.Dir)
	}
	sort.Strings(dirs)

	var unformatted, fixed, notRun []string
	checked, uninstalled := 0, 0
	for _, dir := range dirs {
		reason, installable, err := c.unready(a, dir)
		if err != nil {
			return fail(c.Name(), err.Error())
		}
		if reason != "" {
			notRun = append(notRun, dir+": not checked, "+reason)
			if installable {
				uninstalled++
			}

			continue
		}
		files, failure := c.different(ctx, env, dir)
		if failure != nil {
			return *failure
		}
		checked++
		if len(files) == 0 {
			continue
		}
		if !env.Fix {
			for _, f := range files {
				unformatted = append(unformatted, path.Join(dir, f)+": not formatted as prettier formats it")
			}

			continue
		}
		if out, err := env.Exec.Run(ctx, a.Abs(dir), nil, a.Abs(path.Join(dir, prettierBin)), append([]string{"--write"}, files...)...); err != nil {
			return fail(c.Name(), fmt.Sprintf("prettier could not format the files of %s", dir), outputLines(out, 20)...)
		}
		for _, f := range files {
			fixed = append(fixed, path.Join(dir, f))
		}
	}

	switch {
	case len(unformatted) > 0:
		return fail(c.Name(), fmt.Sprintf("%d browser file(s) not formatted as the web root's prettier formats them (--fix formats them)", len(unformatted)), append(unformatted, notRun...)...)
	case checked == 0 && uninstalled > 0:
		return Result{Name: c.Name(), Status: Skip, Summary: "not run: the web roots' packages are not installed, and prettier comes with them; install them, then run impulse check --only prettier --fix once", Details: notRun}
	case checked == 0:
		return Result{Name: c.Name(), Status: Skip, Summary: "not run: no web root pins a prettier to hold its files to", Details: notRun}
	case len(fixed) > 0:
		return passWithDetails(c.Name(), fmt.Sprintf("%d browser file(s) formatted; %d web root(s) checked", len(fixed), checked), append(fixed, notRun...)...)
	default:
		return passWithDetails(c.Name(), fmt.Sprintf("the browser files of %d web root(s) are formatted as its prettier formats them", checked), notRun...)
	}
}

// unready says why a web root cannot be held to its prettier, or nothing when it can: its
// package.json pins none, or pins one the install has not put there yet, which installable
// reports, since an install is then all the root lacks.
func (prettierCheck) unready(a *app.App, dir string) (reason string, installable bool, err error) {
	data, err := os.ReadFile(a.Abs(path.Join(dir, "package.json")))
	if errors.Is(err, os.ErrNotExist) {
		return "it has no package.json to pin a prettier in", false, nil
	}
	if err != nil {
		return "", false, errors.Wrap(err, "os.ReadFile()")
	}
	var pkg struct {
		Dependencies    map[string]json.RawMessage `json:"dependencies"`
		DevDependencies map[string]json.RawMessage `json:"devDependencies"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return "", false, errors.Wrapf(err, "json.Unmarshal(): %s/package.json", dir)
	}
	_, dev := pkg.DevDependencies[prettierPackage]
	_, dep := pkg.Dependencies[prettierPackage]
	if !dev && !dep {
		return "its package.json pins no prettier (the skeletons pin one in devDependencies)", false, nil
	}
	if !exists(a.Abs(path.Join(dir, prettierBin))) {
		return "its packages are not installed (no " + prettierBin + ")", true, nil
	}

	return "", false, nil
}

// different lists the files of a web root prettier would change, relative to the root, or
// the failure of a run that could not read the root.
func (c prettierCheck) different(ctx context.Context, env *Env, dir string) ([]string, *Result) {
	a := env.App
	out, err := env.Exec.Run(ctx, a.Abs(dir), nil, a.Abs(path.Join(dir, prettierBin)), "--list-different", ".")
	status := prettierClean
	if err != nil {
		var exited interface{ ExitCode() int }
		if !errors.As(err, &exited) {
			failure := fail(c.Name(), fmt.Sprintf("prettier could not run in %s: %v", dir, errors.Cause(err)), outputLines(out, 20)...)

			return nil, &failure
		}
		status = exited.ExitCode()
	}
	if status != prettierClean && status != prettierDifferent {
		failure := fail(c.Name(), fmt.Sprintf("prettier could not read the files of %s (it exited %d)", dir, status), outputLines(out, 20)...)

		return nil, &failure
	}

	var files []string
	for line := range strings.Lines(string(out)) {
		line = strings.TrimSpace(line)
		// What is not a file is a note prettier wrote beside the listing ([warn] ...).
		if line == "" || strings.HasPrefix(line, "[") {
			continue
		}
		files = append(files, line)
	}

	return files, nil
}
