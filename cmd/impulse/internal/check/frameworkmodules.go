package check

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"

	"github.com/go-playground/errors/v5"
	"golang.org/x/mod/modfile"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/framework"
)

// frameworkModules verifies that the application names no module under the framework's
// import path but the framework's own. The go command lets a package import a package
// under an internal directory when the importer stands in the tree the internal
// directory stands in, and it judges that by the importer's import path, not by where
// its code is kept. A module whose path is under the framework's is therefore inside
// the framework to the go command, wherever a replace or a workspace finds its code:
// its packages may import the packages the framework keeps to itself, the write of an
// impersonated session and the reads that decide one among them
// (session/internal/sealed), and make that write with no declaration, no grant and no
// mask.
//
// The framework is one module (FrameworkModule); its repository holds its test
// application's two beside it (framework.OwnModules). Any other module path under the
// framework's is refused in every place the application's module files name one: the
// application's own module path, a requirement (direct or indirect) and the replaced
// side of a replace in go.mod; in a go.work at the application's root, the replaced
// side of a replace; and, in the go.mod of every directory that go.work uses, the
// module's own path, a requirement and the replaced side of a replace. In a workspace
// the go command applies the replaces of every module it uses to the whole build, so
// a used directory's go.mod decides the build as the application's own does.
//
// What the check reads is those files as they stand: the application's go.mod, a
// go.work at its root, and the go.mod of each directory that go.work uses, inside the
// application's root or outside it. A build told to read files from elsewhere (the
// -modfile flag, which names another module file, and the -overlay flag, which replaces
// or adds source files from anywhere, each on the command line or in the GOFLAGS
// variable), a workspace file above the application's root or one the GOWORK variable
// names, and the code of the modules the application requires are not read here.
type frameworkModules struct{}

// frameworkModulesName is the check's name.
const frameworkModulesName = "framework-modules"

// The module files the check reads: a module's own, and the workspace file the go
// command reads at a module's root or above.
const (
	goModFile  = "go.mod"
	goWorkFile = "go.work"
)

// insideTheFramework is why a module under the framework's path is refused, as every
// line of the check says it.
const insideTheFramework = "to the go command its packages are inside the framework and may import the packages the framework keeps to itself (its internal directories)"

func (frameworkModules) Name() string {
	return frameworkModulesName
}

func (frameworkModules) Describe() string {
	return "go.mod and go.work name no module under the framework's import path but the framework's own"
}

func (frameworkModules) Meaning() string {
	return "The framework keeps some of its code to itself in internal directories, which the go command opens to any package whose import path is under the framework's, whoever wrote it: a module of the application's own given such a path could import them, the write of an impersonated session among them, and go around the declaration, the grant and the permission mask the session API holds that write to. " +
		"The framework is the one module " + FrameworkModule + ". Take out each module the check names (the requirement or the replace in go.mod, the use or the replace in go.work, and what imports it), give the code a module path of the application's own, and reach the framework through what its packages export. " +
		"If the application needs something a package of the framework keeps to itself, that is a change to ask of the framework."
}

func (c frameworkModules) Run(_ context.Context, env *Env) Result {
	a := env.App
	details := append(c.ownPath(a.GoMod), c.inGoMod(a.GoMod, goModFile, "")...)
	inWork, err := c.inGoWork(a)
	if err != nil {
		return fail(c.Name(), err.Error())
	}
	details = append(details, inWork...)
	if len(details) > 0 {
		return fail(c.Name(), fmt.Sprintf("%d module path(s) under the framework's (%s) that are not the framework's", len(details), FrameworkModule), details...)
	}

	return pass(c.Name(), fmt.Sprintf("no module under the framework's path (%s) but the framework's own", FrameworkModule))
}

// refuses reports whether a module path is under the framework's and is not one of the
// framework's own modules.
func (frameworkModules) refuses(modulePath string) bool {
	return framework.Under(modulePath) && !slices.Contains(framework.OwnModules, modulePath)
}

// ownPath refuses the application's own module path when it is under the framework's.
func (c frameworkModules) ownPath(mod *modfile.File) []string {
	if mod == nil || mod.Module == nil || !c.refuses(mod.Module.Mod.Path) {
		return nil
	}

	return []string{fmt.Sprintf(
		"go.mod: the application's module path %s is under the framework's (%s): %s. Give the module a path of the application's own",
		mod.Module.Mod.Path, FrameworkModule, insideTheFramework,
	)}
}

// inGoMod lists the refused module paths a go.mod names besides its own: each
// requirement's and the replaced side of each replace. file is how a line names the
// go.mod, and also is what a line adds for a go.mod that is not the application's own:
// the other way to clear the finding, which is to take its directory out of the
// workspace.
func (c frameworkModules) inGoMod(mod *modfile.File, file, also string) []string {
	if mod == nil {
		return nil
	}
	var details []string
	for _, r := range mod.Require {
		if c.refuses(r.Mod.Path) {
			details = append(details, fmt.Sprintf(
				"%s requires %s, a module under the framework's path that is not the framework's: %s. The framework is the one module %s; remove the requirement and what imports it%s",
				file, r.Mod.Path, insideTheFramework, FrameworkModule, also,
			))
		}
	}
	for _, r := range mod.Replace {
		if c.refuses(r.Old.Path) {
			details = append(details, fmt.Sprintf(
				"%s replaces %s with %s, and the replaced module is under the framework's path and is not the framework's: %s. Remove the replace%s",
				file, r.Old.Path, r.New.Path, insideTheFramework, also,
			))
		}
	}

	return details
}

// inGoWork lists the refused module paths a go.work at the application's root names:
// the replaced side of each of its replaces, and what the go.mod of each directory it
// uses names (the module's own path, each requirement and the replaced side of each
// replace, which the go command applies to the whole build in a workspace). An
// application with no workspace file names none.
func (c frameworkModules) inGoWork(a *app.App) ([]string, error) {
	data, err := os.ReadFile(a.Abs(goWorkFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.Wrap(err, "os.ReadFile()")
	}
	work, err := modfile.ParseWork(goWorkFile, data, nil)
	if err != nil {
		return nil, errors.Wrapf(err, "%s does not parse", goWorkFile)
	}
	var details []string
	for _, use := range work.Use {
		used, err := c.inUsedDirectory(a, use.Path)
		if err != nil {
			return nil, err
		}
		details = append(details, used...)
	}
	for _, r := range work.Replace {
		if c.refuses(r.Old.Path) {
			details = append(details, fmt.Sprintf(
				"go.work replaces %s with %s, and the replaced module is under the framework's path and is not the framework's: %s. Remove the replace",
				r.Old.Path, r.New.Path, insideTheFramework,
			))
		}
	}

	return details, nil
}

// inUsedDirectory lists the refused module paths the go.mod of one directory a go.work
// uses names. use is the directory as go.work writes it. The application's own
// directory is go.mod's to answer for, and a directory with no go.mod is the go
// command's to refuse.
func (c frameworkModules) inUsedDirectory(a *app.App, use string) ([]string, error) {
	dir := use
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(a.Root, filepath.FromSlash(dir))
	}
	if filepath.Clean(dir) == filepath.Clean(a.Root) {
		return nil, nil
	}
	data, err := os.ReadFile(filepath.Join(dir, goModFile)) //nolint:gosec // G703: a directory the application's own go.work uses, read as the go command reads it
	if err != nil {
		return nil, nil //nolint:nilerr // a directory with no go.mod is the go command's to refuse
	}
	file := path.Join(filepath.ToSlash(use), goModFile)
	mod, err := modfile.Parse(file, data, nil)
	if err != nil {
		return nil, errors.Wrapf(err, "%s, the go.mod of a directory %s uses, does not parse", file, goWorkFile)
	}
	var details []string
	if mod.Module != nil && c.refuses(mod.Module.Mod.Path) {
		details = append(details, fmt.Sprintf(
			"go.work uses %s, whose module is %s, under the framework's path and not the framework's: %s. Take the directory out of the workspace",
			use, mod.Module.Mod.Path, insideTheFramework,
		))
	}
	also := fmt.Sprintf(", or take %s out of the workspace: %s uses it, and in a workspace the go command reads the requirements and applies the replaces of every module it uses", use, goWorkFile)

	return append(details, c.inGoMod(mod, file, also)...), nil
}
