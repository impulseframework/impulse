package transition

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/go-playground/errors/v5"

	"github.com/impulseframework/impulse/app"
)

// The application's own sources after its console moved off the root. The generator
// program, the workspace and the project's files move with the declaration; what the
// application wrote around them named the old place still, so an application that was
// made with a second browser application did not serve its console where the router
// mounted it, mailed links to a login page that was gone, and failed its own tests on
// every path under the old prefix. The command rewrites what it can read for certain:
// the App's served browser app built at the root, the login pages the default outlet's
// auth is told, and the old prefix where the application's Go and prose name it as a
// path. What it cannot read is listed for the author as before.

var (
	// browserAppAtRootRE matches the App building a served browser app at the root, the
	// mount path being the call's last argument.
	browserAppAtRootRE = regexp.MustCompile(`(NewBrowserApp\(.*,\s*)"/"\)`)
	// programAtRootRE matches the generator program's comment saying where the default
	// outlet's browser application is, as the base writes it.
	programAtRootRE = regexp.MustCompile(`(browser application\s+(?://\s*)?at )/\.`)
)

// bundleAtRoot is how the base's App says where the console's bundle is served.
const bundleAtRoot = "built bundle at /:"

// consoleMove rewrites the application's own sources for a console that moved from the
// root to its mount path, and keeps what it rewrote for the change.
type consoleMove struct {
	oldPrefix, prefix, mount string
	// mounted are the lines that built the console's served browser app at the root, and
	// renamed the lines that named the old prefix as a path.
	mounted, renamed mentions
}

// followConsole brings the application's own sources after the console: the App's served
// browser app is built under the mount path, the login pages the default outlet's auth is
// told are under it, and hand-written Go, the README, the process files and the
// environment template name the new prefix where they named the old one as a path.
func followConsole(a *app.App, site *app.Site, oldPrefix, prefix, mount string, ch *Change) error {
	mv := &consoleMove{oldPrefix: oldPrefix, prefix: prefix, mount: mount, mounted: mentions{}, renamed: mentions{}}
	err := walkOwnSources(a, func(root *os.Root, rel string, mode fs.FileMode, isGo, _ bool) error {
		local := filepath.FromSlash(rel)
		data, err := root.ReadFile(local)
		if err != nil {
			return errors.Wrap(err, "os.Root.ReadFile()")
		}
		out := mv.rewrite(rel, data, isGo, rel == site.Generator.File)
		if bytes.Equal(out, data) {
			return nil
		}
		if err := root.WriteFile(local, out, mode); err != nil {
			return errors.Wrap(err, "os.Root.WriteFile()")
		}

		return nil
	})
	if err != nil {
		return err
	}
	mv.report(ch)

	return moveLoginPages(a, site, mount, ch)
}

// rewrite is one file after the move. Hand-written Go outside the tests builds the
// console's served browser app under the mount path where it built it at the root, every
// file names the new prefix where it named the old one as a path, and the generator
// program's comment says where the browser application is now.
func (mv *consoleMove) rewrite(rel string, data []byte, isGo, isProgram bool) []byte {
	served := isGo && !inTestFile(rel)
	var b strings.Builder
	n := 0
	for line := range strings.Lines(string(data)) {
		n++
		if served && browserAppAtRootRE.MatchString(line) {
			line = browserAppAtRootRE.ReplaceAllString(line, "${1}"+strconv.Quote(mv.mount)+")")
			mv.mounted.add(rel, n)
		}
		if renamed, changed := renamePath(line, "/"+mv.oldPrefix, "/"+mv.prefix); changed {
			line = renamed
			mv.renamed.add(rel, n)
		}
		b.WriteString(line)
	}
	out := b.String()
	if served {
		out = strings.ReplaceAll(out, bundleAtRoot, "built bundle under "+mv.mount+":")
	}
	if isProgram {
		out = programAtRootRE.ReplaceAllString(out, "${1}"+mv.mount+".")
	}

	return []byte(out)
}

// renamePath writes a path in place of another wherever a line names the other on its
// own: not inside a longer path or a name, which is how the scan reads the old prefix in
// prose (/portal/api and google.golang.org/api do not name /api). A root, which is every
// path's start, is not rewritten.
func renamePath(line, old, renamed string) (string, bool) {
	if old == "/" {
		return line, false
	}
	var b strings.Builder
	at, changed := 0, false
	for {
		i := strings.Index(line[at:], old)
		if i < 0 {
			break
		}
		i += at
		end := i + len(old)
		b.WriteString(line[at:i])
		if (i == 0 || !partOfPath(line[i-1], true)) && (end == len(line) || !partOfPath(line[end], false)) {
			b.WriteString(renamed)
			changed = true
		} else {
			b.WriteString(old)
		}
		at = end
	}
	b.WriteString(line[at:])

	return b.String(), changed
}

// partOfPath reports whether a byte beside a path continues it: a letter, a digit, an
// underscore or a hyphen on either side, and a dot or a slash before it.
func partOfPath(c byte, before bool) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_', c == '-':
		return true
	default:
		return before && (c == '.' || c == '/')
	}
}

// report records what the move rewrote: the served browser app by file and line, and the
// old prefix's lines in one entry, since they were rewritten as text and are to be read.
func (mv *consoleMove) report(ch *Change) {
	for _, m := range mv.mounted.sorted() {
		ch.didf("%s:%s: the App builds the console's served browser app under %s, where the generated router mounts it now: NewBrowserApp(..., %q) in place of \"/\"", m.file, lineList(m.lines), mv.mount, mv.mount)
	}
	renamed := mv.renamed.sorted()
	if len(renamed) == 0 {
		return
	}
	lines := make([]string, 0, len(renamed))
	for _, m := range renamed {
		lines = append(lines, m.file+":"+lineList(m.lines))
	}
	ch.didf("the console's API was under /%s and is under /%s, and the application's own Go, README, process files and environment template say so where they named the old path; they were rewritten as text, so read each line: %s", mv.oldPrefix, mv.prefix, strings.Join(lines, "; "))
}

// inTestFile reports whether a file is one of a package's tests.
func inTestFile(file string) bool {
	return strings.HasSuffix(file, "_test.go")
}

// The settings of an auth that name a page of the browser application that binds to it:
// the login page a refused sign-in returns to, the login page a mailed link opens, and
// the device page.
var pageSettings = []string{"LoginURL", "MailLoginURL", "DevicePage"}

// moveLoginPages moves the pages the default outlet's auth is told under the console's
// mount path, in every construction of the auth's settings outside the tests: the login
// page a refused sign-in returns to, the one a mailed link opens, and the device page.
// The tests' constructions name addresses of their own, which no browser opens.
func moveLoginPages(a *app.App, site *app.Site, mount string, ch *Change) error {
	if site.Default.Auth == nil {
		return nil
	}
	pkg, _ := authFileAt(a, site.Default.Auth.ImportPath)
	if pkg == nil {
		return nil
	}
	for _, rel := range pkg.References("Settings", inTestFile) {
		src, mode, err := readFile(a, rel)
		if err != nil {
			return err
		}
		out, moved, err := pagesUnder(rel, src, pkg.Path, mount)
		if err != nil {
			return err
		}
		if len(moved) == 0 {
			continue
		}
		if err := os.WriteFile(a.Abs(rel), out, mode); err != nil {
			return errors.Wrap(err, "os.WriteFile()")
		}
		ch.didf("%s: the %s auth is told the console's pages where they are now, under %s: %s", rel, pkg.Name, mount, joinAnd(moved))
	}

	return nil
}

// pagesUnder rewrites the pages an auth's settings name, in every literal of the settings
// the file builds, to the same pages under the mount path. A page is read as the
// login-pages check reads it: the string that ends the setting's value, so the public
// address a mailed link's page is joined to stays as it is. A page under the mount path
// already, and a value that ends in no string or in a whole address, are left alone.
func pagesUnder(rel string, src []byte, authPath, mount string) (out []byte, moved []string, err error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, nil, errors.Wrap(err, "parser.ParseFile()")
	}
	local, err := app.ImportName(rel, src, authPath)
	if err != nil || local == "" {
		return src, nil, err
	}
	type page struct {
		start, end int
		text       string
	}
	var pages []page
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok || !isSettingsOf(lit.Type, local) {
			return true
		}
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok || !slices.Contains(pageSettings, key.Name) {
				continue
			}
			value := lastString(kv.Value)
			if value == nil {
				continue
			}
			named, err := strconv.Unquote(value.Value)
			if err != nil || !strings.HasPrefix(named, "/") || named == mount || strings.HasPrefix(named, mount+"/") {
				continue
			}
			pages = append(pages, page{fset.Position(value.Pos()).Offset, fset.Position(value.End()).Offset, strconv.Quote(mount + named)})
			moved = append(moved, fmt.Sprintf("%s %q", key.Name, mount+named))
		}

		return true
	})
	if len(pages) == 0 {
		return src, nil, nil
	}
	out = slices.Clone(src)
	for _, p := range slices.Backward(pages) {
		out = slices.Concat(out[:p.start], []byte(p.text), out[p.end:])
	}
	out, err = format.Source(out)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "format.Source(): %s after the edit", rel)
	}

	return out, moved, nil
}

// isSettingsOf reports whether a literal's type is the Settings of the package the file
// knows by the given name.
func isSettingsOf(expr ast.Expr, local string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Settings" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)

	return ok && pkg.Name == local
}

// lastString is the string literal that ends an expression: the literal itself, or the
// last operand of a concatenation.
func lastString(expr ast.Expr) *ast.BasicLit {
	switch e := expr.(type) {
	case *ast.ParenExpr:
		return lastString(e.X)
	case *ast.BinaryExpr:
		if e.Op == token.ADD {
			return lastString(e.Y)
		}
	case *ast.BasicLit:
		if e.Kind == token.STRING {
			return e
		}
	}

	return nil
}

// walkOwnSources visits the files of the application that may name the console's place:
// hand-written Go, and the prose that names routes (the README, the process files and the
// environment template). Generated files, build products, and the repository's own
// directory are not visited; the tree is opened as a root, so the walk stays inside it.
func walkOwnSources(a *app.App, visit func(root *os.Root, rel string, mode fs.FileMode, isGo, isProse bool) error) error {
	root, err := os.OpenRoot(a.Root)
	if err != nil {
		return errors.Wrap(err, "os.OpenRoot()")
	}
	defer root.Close()

	err = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return errors.Wrap(err, "fs.WalkDir()")
		}
		if d.IsDir() {
			if p != "." && (skippedNames[d.Name()] || d.Name() == ".git") {
				return fs.SkipDir
			}

			return nil
		}
		name := d.Name()
		isGo := strings.HasSuffix(name, ".go") && !strings.HasPrefix(name, "zz_gen_")
		isProse := name == readmeFile || processFileRE.MatchString(name) || (a.EnvTemplate != "" && p == a.EnvTemplate)
		if !isGo && !isProse {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return errors.Wrap(err, "fs.DirEntry.Info()")
		}

		return visit(root, p, info.Mode().Perm(), isGo, isProse)
	})
	if err != nil {
		return errors.Wrap(err, "fs.WalkDir()")
	}

	return nil
}
