package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/go-playground/errors/v5"
)

// frameworkModulePath is the import path of the framework's module: a package of it is
// one the scan may read calls into.
const frameworkModulePath = "github.com/impulseframework/impulse"

// newGeneratorFunc is the generation package's function a generator program is opened
// with.
const newGeneratorFunc = "NewResourceGenerator"

// calledByName holds, per package, the functions whose calls the checks read where the
// source writes them, by the name the file imports the package under: a function among
// them taken as a value would carry the call somewhere no check looks.
var calledByName = map[string][]string{
	accessImportPath:     {engineNewFunc, defaultRolesFunc, changeSignalFunc, validateRolesFunc},
	resourceImportPath:   {retireSecretFunc},
	generationImportPath: {newGeneratorFunc},
	sessionImportPath:    {authConstructor},
}

// refuseHiddenCalls refuses the two forms of Go that would hide a call the checks read
// from them: a dot import of a package of the framework or of the application's own
// module, whose functions the file then calls without the package's name, and one of the
// functions in calledByName named without being called. The first of either in the file
// is said, at its line. A file that does not parse is the compiler's to report, and is
// refused nothing here.
func (a *App) refuseHiddenCalls(rel string, data []byte) error {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, data, parser.ImportsOnly|parser.SkipObjectResolution)
	if err != nil {
		return nil
	}
	listed := map[string][]string{}
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		local := path.Base(p)
		if imp.Name != nil {
			local = imp.Name.Name
		}
		switch local {
		case ".":
			if a.readsCallsInto(p) {
				return errors.Newf("%s:%d: import . %q brings the package's names in without the package's name, and impulse finds a call by the name of its package, so it would see none of this file's calls into it: import the package by its name and write each call with it", rel, fset.Position(imp.Pos()).Line, p)
			}
		case "_":
		default:
			if funcs, ok := calledByName[p]; ok {
				listed[local] = funcs
			}
		}
	}
	if len(listed) == 0 {
		return nil
	}

	f, err = parser.ParseFile(fset, rel, data, parser.SkipObjectResolution)
	if err != nil {
		return nil
	}

	return functionValue(rel, fset, f, listed)
}

// readsCallsInto reports whether the scan may read calls into the package at an import
// path: one of the framework's module, or of the application's own.
func (a *App) readsCallsInto(importPath string) bool {
	if importPath == frameworkModulePath || strings.HasPrefix(importPath, frameworkModulePath+"/") {
		return true
	}
	_, own := a.ModuleDir(importPath)

	return own
}

// functionValue refuses the first function of listed (by the name the file imports its
// package under) that the file names anywhere but as the function of a call.
func functionValue(rel string, fset *token.FileSet, f *ast.File, listed map[string][]string) error {
	called := map[*ast.SelectorExpr]bool{}
	var refusal error
	ast.Inspect(f, func(n ast.Node) bool {
		if refusal != nil {
			return false
		}
		switch e := n.(type) {
		case *ast.CallExpr:
			if sel, ok := calledFunction(e.Fun).(*ast.SelectorExpr); ok {
				called[sel] = true
			}
		case *ast.SelectorExpr:
			pkg, ok := e.X.(*ast.Ident)
			if !ok || called[e] || !slices.Contains(listed[pkg.Name], e.Sel.Name) {
				return true
			}
			refusal = errors.Newf("%s:%d: %s.%s is named here without being called, and impulse reads a call where the source writes it, so it would not see a call made through this value: call %s.%s where the call is made",
				rel, fset.Position(e.Pos()).Line, pkg.Name, e.Sel.Name, pkg.Name, e.Sel.Name)

			return false
		}

		return true
	})

	return refusal
}

// calledFunction is the function a call is made to as the source writes it, looking
// through parentheses and a generic instantiation such as pkg.Name[T, U].
func calledFunction(fun ast.Expr) ast.Expr {
	for {
		switch e := fun.(type) {
		case *ast.ParenExpr:
			fun = e.X
		case *ast.IndexExpr:
			fun = e.X
		case *ast.IndexListExpr:
			fun = e.X
		default:
			return fun
		}
	}
}
