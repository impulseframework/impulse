// Package apptree holds the rules a reading of an application's tree goes by: which
// directories a walk leaves alone, which Go files the Go tool reads, and which packages
// are the application's auths. The scan behind impulse check reads the tree, and so does
// a generation run that asks what the application's auths are; both go by the rules
// here, so the two never disagree on what an auth of the application is.
package apptree

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/go-playground/errors/v5"
)

// The names the Go tool goes by: the directory it keeps for a package's test fixtures,
// and the file that makes a directory the root of a module.
const (
	testdataDir = "testdata"
	goModFile   = "go.mod"
)

// AuthDir is the name of the directory the auth packages live directly under
// (pkg/auth/<name> in an application the skeleton made), one package per population
// that signs in one way and holds roles in one store. A package directly under a
// directory of this name, anywhere in the module, is an auth package when it
// constructs a session Auth (AuthPackages).
const AuthDir = "auth"

// The construction that makes a package under an auth directory an auth: the session
// library's Auth, built by its constructor.
const (
	// SessionImportPath is the import path of the session library's root package.
	SessionImportPath = "github.com/impulseframework/impulse/session"
	// AuthConstructor is the Auth's constructor in the session package.
	AuthConstructor = "NewAuth"
)

// HoldsDependenciesOrOutput reports whether a directory of this name holds dependencies
// or build output, not the application's own declarations: no reading of the application
// descends into one.
func HoldsDependenciesOrOutput(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "dist", ".angular":
		return true
	}

	return false
}

// PassedOverByGo reports whether the Go tool passes over a directory or a file of this
// name: one whose name begins with "_" or ".", and a directory named testdata (go help
// packages). No build reads such a file. The pattern ./... matches no package under such
// a directory, so what is there is no part of the application (a scratch program, a
// test's fixture, a tool's own directory) unless a package the pattern does match imports
// it by its path.
func PassedOverByGo(name string, isDir bool) bool {
	return strings.HasPrefix(name, "_") || strings.HasPrefix(name, ".") || (isDir && name == testdataDir)
}

// GoToolReads reports whether a file of this name is a Go file the Go tool reads: its
// name ends in ".go" and does not begin with "_" or "." (go help packages).
func GoToolReads(name string) bool {
	return strings.HasSuffix(name, ".go") && !PassedOverByGo(name, false)
}

// SkipsDir reports whether a walk of the application leaves a directory of this name
// alone: one that holds dependencies or build output, and one the Go tool passes over. The
// application's root is the caller's to exempt, whatever it is called.
func SkipsDir(name string) bool {
	return HoldsDependenciesOrOutput(name) || PassedOverByGo(name, true)
}

// HoldsModule reports whether a directory holds a go.mod of its own.
func HoldsModule(abs string) bool {
	info, err := os.Stat(filepath.Join(abs, goModFile))

	return err == nil && !info.IsDir()
}

// AuthPackageName returns the auth a file belongs to, from its place in the tree (a file
// of a package directly under an auth directory), or empty. file is a slash-separated
// path, as a reading of the tree names its files.
func AuthPackageName(file string) string {
	dir := path.Dir(file)
	if path.Base(path.Dir(dir)) != AuthDir {
		return ""
	}

	return path.Base(dir)
}

// AuthPackages lists the auths of the application whose tree is at root, by name and in
// name order. An auth is a package directly under an auth directory that constructs a
// session Auth, named as its directory is: the packages impulse check reads as the
// application's auths. The Go files read are the ones the walk of a build's packages
// reaches: a directory a walk leaves alone (SkipsDir) and a module of its own under the
// root are not descended into, and a test file and a file the Go tool passes over are not
// read. A package under a directory the walk leaves alone is no auth here even where the
// application imports it by its path, which the scan behind impulse check follows.
func AuthPackages(root string) ([]string, error) {
	var names []string
	err := filepath.WalkDir(root, func(abs string, d fs.DirEntry, err error) error {
		if err != nil {
			return errors.Wrap(err, "filepath.WalkDir()")
		}
		if d.IsDir() {
			if abs != root && (SkipsDir(d.Name()) || HoldsModule(abs)) {
				return filepath.SkipDir
			}

			return nil
		}
		name := AuthPackageName(filepath.ToSlash(abs))
		if name == "" || slices.Contains(names, name) || !GoToolReads(d.Name()) || strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		constructs, err := constructsAuth(abs)
		if err != nil {
			return err
		}
		if constructs {
			names = append(names, name)
		}

		return nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "filepath.WalkDir()")
	}
	slices.Sort(names)

	return names, nil
}

// constructsAuth reports whether the Go file at abs constructs a session Auth: it
// imports the session package and calls its constructor by the name it imports it under.
func constructsAuth(abs string) (bool, error) {
	src, err := os.ReadFile(abs)
	if err != nil {
		return false, errors.Wrap(err, "os.ReadFile()")
	}
	if !strings.Contains(string(src), SessionImportPath) {
		return false, nil
	}
	f, err := parser.ParseFile(token.NewFileSet(), abs, src, parser.SkipObjectResolution)
	if err != nil {
		return false, errors.Wrap(err, "parser.ParseFile()")
	}
	local := localImportName(f, SessionImportPath)
	if local == "" {
		return false, nil
	}
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := selectorOf(call.Fun); ok && sel.Sel.Name == AuthConstructor {
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == local {
				found = true
			}
		}

		return !found
	})

	return found, nil
}

// localImportName returns the name the file imports the path under, or empty when the
// file does not import it.
func localImportName(f *ast.File, importPath string) string {
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil || p != importPath {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name
		}

		return path.Base(p)
	}

	return ""
}

// selectorOf returns the pkg.Name selector a call is made through, looking through a
// generic instantiation such as pkg.Name[T, U].
func selectorOf(fun ast.Expr) (*ast.SelectorExpr, bool) {
	switch e := fun.(type) {
	case *ast.IndexExpr:
		return selectorOf(e.X)
	case *ast.IndexListExpr:
		return selectorOf(e.X)
	case *ast.SelectorExpr:
		if _, ok := e.X.(*ast.Ident); ok {
			return e, true
		}
	}

	return nil, false
}
