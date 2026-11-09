package skeleton

import (
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/go-playground/errors/v5"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"

	"github.com/impulseframework/impulse/cmd/impulse/internal/framework"
	"github.com/impulseframework/impulse/cmd/impulse/internal/names"
)

// Options describe one rendering of an embedded template.
type Options struct {
	// Candidate names the embedded skeleton to render.
	Candidate string
	// Dir is the directory to render into. It must not exist or must be empty.
	Dir string
	// ModulePath is the rendered application's module path. Every import and the go.mod
	// files are rewritten from the template's placeholder path to it.
	ModulePath string
	// Version is the framework version the rendered go.mod requires
	// github.com/impulseframework/impulse at, in place of the template's placeholder
	// (VersionPlaceholder): the running impulse's release, or the pseudo-version of the
	// commit it was installed from. Required.
	Version string
	// WebVersion is the browser packages' version every web root's package.json pins, in
	// place of the template's placeholder (WebVersionPlaceholder): the release's own
	// version, or the latest release's for an impulse no release published packages for.
	// Required.
	WebVersion string
	// DevRoot, when set, is a checkout of the framework (a directory whose go.mod declares
	// github.com/impulseframework/impulse). The rendering then adds a replace directive to
	// go.mod pointing the framework module at it, so the application builds against local
	// framework work instead of the pin; the pin itself names nothing the module proxy
	// serves, so the replace is what makes the module resolvable.
	DevRoot string
	// Auth names the application's first auth. The template carries a placeholder auth
	// (PlaceholderAuth) in its paths and contents; the rendering substitutes the name
	// everywhere the placeholder stands as a name or starts an identifier. Empty keeps
	// the placeholder.
	Auth string
	// Name is the application's name: what the candidate's own name stands for in the
	// template, in the web workspace's package name (<candidate>-web), the environment
	// template's service name and development database ids (<candidate>, <candidate>-dev),
	// the README heading, the browser projects' titles and manifests, and the auth
	// packages' Application constant, the name the mail an auth sends says. Rendering
	// substitutes it in those slots (names.RenameApp) and the templates name the
	// application nowhere else. Empty keeps the candidate's name.
	Name string
}

// PlaceholderAuth is the auth every template carries: the name render substitutes when
// Options.Auth is set. The templates keep it because they are also the reference fixtures
// the handoff brief points at.
const PlaceholderAuth = "staff"

// Base is the skeleton every application starts from: flat, one auth, nothing else on.
const Base = "solo"

// Rendered reports what a rendering produced.
type Rendered struct {
	// Files counts the files written.
	Files int
	// Replace is the path go.mod replaces the framework module with, as written, when
	// DevRoot was set; empty otherwise.
	Replace string
}

// VersionPlaceholder stands for the framework version in every candidate's go.mod.tmpl,
// and WebVersionPlaceholder for the browser packages' version in every web root's
// package.json: the templates carry no release of their own, the rendering writes the
// one it is for.
const (
	VersionPlaceholder    = "v0.0.0-skeleton"
	WebVersionPlaceholder = "0.0.0-skeleton"
)

const (
	goExt       = ".go"
	packageJSON = "package.json"
)

// Render copies an embedded template into opts.Dir under opts.ModulePath: go.mod.tmpl
// becomes go.mod with the framework version written in, the placeholder module path is
// rewritten everywhere it appears, the browser packages' version is written into every
// package.json, the application name replaces the candidate's where the template carries
// it, Go files are reformatted (the rewrite changes line lengths), and shell scripts
// regain their execute bit, which embedding drops.
func Render(opts *Options) (*Rendered, error) {
	if err := module.CheckPath(opts.ModulePath); err != nil {
		return nil, errors.Wrapf(err, "module path %q", opts.ModulePath)
	}
	if !semver.IsValid(opts.Version) {
		return nil, errors.Newf("framework version %q is not a version: the rendering writes the version go.mod requires %s at", opts.Version, framework.Module)
	}
	if opts.WebVersion == "" {
		return nil, errors.Newf("no browser packages version: the rendering writes the version every web root pins %s at", strings.Join(framework.WebPackages, ", "))
	}
	if opts.Name != "" {
		if err := names.ValidateApp(opts.Name); err != nil {
			return nil, err
		}
	}
	candidate, err := candidateByName(opts.Candidate)
	if err != nil {
		return nil, err
	}
	if err := ensureEmptyDir(opts.Dir); err != nil {
		return nil, err
	}
	sub, err := FS(candidate.Name)
	if err != nil {
		return nil, err
	}
	// Every write goes through a root scoped to the target directory, so no template
	// path, however named, can land outside it.
	target, err := os.OpenRoot(opts.Dir)
	if err != nil {
		return nil, errors.Wrap(err, "os.OpenRoot()")
	}
	defer target.Close()

	rendered := &Rendered{}
	err = fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return errors.Wrap(err, "fs.WalkDir()")
		}
		if d.IsDir() {
			return nil
		}
		if err := renderFile(sub, target, p, opts, candidate); err != nil {
			return err
		}
		rendered.Files++

		return nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "fs.WalkDir()")
	}

	if opts.DevRoot != "" {
		if err := writeReplace(opts, target, rendered); err != nil {
			return nil, err
		}
	}

	return rendered, nil
}

// renderFile writes one template file into the target tree. The rendered tree is a
// project a person works in, so it gets the conventional modes: 0755 directories, 0644
// files, 0755 shell scripts. With Options.Auth, the placeholder auth is renamed in the
// file's path and its text; with Options.Name, the application name replaces the
// candidate's in the slots that carry it.
func renderFile(sub fs.FS, target *os.Root, p string, opts *Options, candidate Candidate) error {
	src, err := fs.ReadFile(sub, p)
	if err != nil {
		return errors.Wrapf(err, "fs.ReadFile(): %s", p)
	}

	rel := p
	if path.Base(p) == ModFile {
		rel = path.Join(path.Dir(p), "go.mod")
	}
	rename := opts.Auth != "" && opts.Auth != PlaceholderAuth
	if rename {
		rel = names.Rename(rel, PlaceholderAuth, opts.Auth)
	}

	out := src
	if utf8.Valid(src) {
		text := strings.ReplaceAll(string(src), candidate.ModulePath, opts.ModulePath)
		switch path.Base(p) {
		case ModFile:
			// The application's own go.mod requires the framework; a web root's go.mod,
			// which keeps Go's tooling out of the workspace, requires nothing.
			if p == ModFile && !strings.Contains(text, VersionPlaceholder) {
				return errors.Newf("%s: the template names no %s for the framework version", p, VersionPlaceholder)
			}
			text = strings.ReplaceAll(text, VersionPlaceholder, opts.Version)
		case packageJSON:
			text = strings.ReplaceAll(text, WebVersionPlaceholder, opts.WebVersion)
		}
		if opts.Name != "" && opts.Name != candidate.Name {
			text = names.RenameApp(rel, text, candidate.Name, opts.Name)
		}
		if rename {
			text = names.Rename(text, PlaceholderAuth, opts.Auth)
		}
		if path.Base(rel) == lintConfig {
			text = dropCoveredAllowEntry(text, opts.ModulePath)
		}
		out = []byte(text)
		if path.Ext(rel) == goExt {
			formatted, err := format.Source(out)
			if err != nil {
				return errors.Wrapf(err, "format.Source(): %s", rel)
			}
			out = formatted
		}
	}

	dst := filepath.FromSlash(rel)
	if dir := filepath.Dir(dst); dir != "." {
		if err := target.MkdirAll(dir, 0o755); err != nil {
			return errors.Wrap(err, "os.Root.MkdirAll()")
		}
	}
	mode := os.FileMode(0o644)
	if path.Ext(rel) == ".sh" {
		mode = 0o755
	}
	if err := target.WriteFile(dst, out, mode); err != nil {
		return errors.Wrap(err, "os.Root.WriteFile()")
	}

	return nil
}

// writeReplace adds the --dev-root replace to go.mod: the framework module is replaced by
// the checkout at DevRoot, whose go.mod must declare it. The path is relative when the
// application lives beside or under the checkout's parent directory, so the pair survives
// a move together, and absolute otherwise.
func writeReplace(opts *Options, target *os.Root, rendered *Rendered) error {
	checkout, err := filepath.Abs(opts.DevRoot)
	if err != nil {
		return errors.Wrap(err, "filepath.Abs()")
	}
	declared, err := os.ReadFile(filepath.Join(checkout, "go.mod"))
	if err != nil {
		return errors.Newf("dev root %s is not a checkout of the framework: no go.mod there (%v)", opts.DevRoot, err)
	}
	if m := modfile.ModulePath(declared); m != framework.Module {
		return errors.Newf("dev root %s is not a checkout of the framework: its go.mod declares module %q, not %s", opts.DevRoot, m, framework.Module)
	}
	data, err := target.ReadFile("go.mod")
	if err != nil {
		return errors.Wrap(err, "os.Root.ReadFile()")
	}
	goModPath := filepath.Join(opts.Dir, "go.mod")
	mod, err := modfile.Parse(goModPath, data, nil)
	if err != nil {
		return errors.Wrap(err, "modfile.Parse()")
	}
	replace := checkout
	if underDir(filepath.Dir(checkout), opts.Dir) {
		if rel, err := filepath.Rel(opts.Dir, checkout); err == nil {
			replace = filepath.ToSlash(rel)
		}
	}
	if err := mod.AddReplace(framework.Module, "", replace, ""); err != nil {
		return errors.Wrap(err, "modfile.File.AddReplace()")
	}
	mod.Cleanup()
	formatted, err := mod.Format()
	if err != nil {
		return errors.Wrap(err, "modfile.File.Format()")
	}
	if err := target.WriteFile("go.mod", formatted, 0o644); err != nil {
		return errors.Wrap(err, "os.Root.WriteFile()")
	}
	rendered.Replace = replace

	return nil
}

// underDir reports whether dir lies inside root (after making both absolute).
func underDir(root, dir string) bool {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absRoot, absDir)
	if err != nil {
		return false
	}

	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// candidateByName finds one embedded skeleton.
func candidateByName(name string) (Candidate, error) {
	candidates, err := Candidates()
	if err != nil {
		return Candidate{}, err
	}
	for _, c := range candidates {
		if c.Name == name {
			return c, nil
		}
	}
	known := make([]string, 0, len(candidates))
	for _, c := range candidates {
		known = append(known, c.Name)
	}

	return Candidate{}, errors.Newf("unknown candidate %q: choose one of %s", name, strings.Join(known, ", "))
}

// ensureEmptyDir creates dir when absent and refuses a non-empty one, so a rendering
// never writes over an existing application.
func ensureEmptyDir(dir string) error {
	entries, err := os.ReadDir(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return errors.Wrap(err, "os.MkdirAll()")
		}

		return nil
	case err != nil:
		return errors.Wrap(err, "os.ReadDir()")
	case len(entries) > 0:
		return errors.Newf("%s is not empty: render into a new or empty directory", dir)
	default:
		return nil
	}
}

// Reserved lists the names an auth cannot take in an application rendered from the
// candidate: every package the template imports (by its last path segment and any local
// name), every package the template declares, and every directory in the tree, since the
// auth becomes a package and a directory of its own. The placeholder auth is not reserved:
// it is what the name replaces.
func Reserved(candidate string) (map[string]bool, error) {
	sub, err := FS(candidate)
	if err != nil {
		return nil, err
	}
	reserved := map[string]bool{}
	fset := token.NewFileSet()
	err = fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return errors.Wrap(err, "fs.WalkDir()")
		}
		if d.IsDir() {
			if p != "." && d.Name() != PlaceholderAuth {
				reserved[d.Name()] = true
			}

			return nil
		}
		if path.Ext(p) != goExt {
			return nil
		}
		src, err := fs.ReadFile(sub, p)
		if err != nil {
			return errors.Wrapf(err, "fs.ReadFile(): %s", p)
		}
		f, err := parser.ParseFile(fset, p, src, parser.ImportsOnly)
		if err != nil {
			return errors.Wrapf(err, "parser.ParseFile(): %s", p)
		}
		if f.Name.Name != PlaceholderAuth {
			reserved[f.Name.Name] = true
		}
		for _, imp := range f.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			if base := path.Base(importPath); base != PlaceholderAuth {
				reserved[base] = true
			}
			if imp.Name != nil && imp.Name.Name != "_" && imp.Name.Name != "." {
				reserved[imp.Name.Name] = true
			}
		}

		return nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "fs.WalkDir()")
	}

	return reserved, nil
}

// lintConfig is the golangci-lint configuration the template carries.
const lintConfig = ".golangci.yml"

// dropCoveredAllowEntry removes the module's own entry from the lint config's
// depguard allow list when another entry already covers it as a path prefix.
// Depguard matches an import against the sorted allow list by adjacency, so a
// module path listed beside the org prefix it lives under — github.com/impulseframework/lantern
// beside github.com/impulseframework — shadows every sibling library that sorts after it
// (httpio, logger, session) and the rendered application fails its own lint. A module
// outside every listed prefix (example.com/acme/beacon) keeps its entry.
func dropCoveredAllowEntry(text, modulePath string) string {
	lines := strings.Split(text, "\n")
	covered := false
	for _, line := range lines {
		entry, ok := allowEntry(line)
		if !ok || entry == modulePath || strings.HasPrefix(entry, "$") {
			continue
		}
		if strings.HasPrefix(modulePath, entry+"/") {
			covered = true

			break
		}
	}
	if !covered {
		return text
	}
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if entry, ok := allowEntry(line); ok && entry == modulePath {
			continue
		}
		kept = append(kept, line)
	}

	return strings.Join(kept, "\n")
}

// allowEntry reads one "- path" list item, as depguard's allow list writes them.
func allowEntry(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "- ") {
		return "", false
	}

	return strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")), true
}
