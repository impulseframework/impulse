package app

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/ettle/strcase"
	"github.com/go-playground/errors/v5"

	"github.com/impulseframework/impulse/cloud"
	"github.com/impulseframework/impulse/internal/apptree"
	"github.com/impulseframework/impulse/internal/gocomments"
)

// The rules a reading of the application's tree goes by (which directories a walk leaves
// alone, which Go files the Go tool reads, which packages are its auths) are the apptree
// package's, where a generation run reads them too; the scan asks them under the names
// below.

// passedOverByGo reports whether the Go tool passes over a directory or a file of this
// name: one whose name begins with "_" or ".", and a directory named testdata (go help
// packages). No build reads such a file. The pattern ./... matches no package under such
// a directory, so what is there is no part of the application (a scratch program, a
// test's fixture, a tool's own directory) unless a package the pattern does match imports
// it by its path, which followImports reads.
func passedOverByGo(name string, isDir bool) bool {
	return apptree.PassedOverByGo(name, isDir)
}

// GoToolReads reports whether a file of this name is a Go file the Go tool reads: its
// name ends in ".go" and does not begin with "_" or "." (go help packages). It is the one
// place the scan, the checks and impulse brand decide a Go file by its name, so that a
// scratch file kept beside a package's real ones (_scratch.go, .draft.go), which no build
// compiles, is read by none of them. It is asked by the scan of every file it walks
// (scanFile, scanPackageDir, StructColumns) and of a generator program's directory (generate.go); by
// each check that reads one directory's Go files for itself (the check package); and by
// impulse brand of the file it writes an auth's mail name into and of the files it looks
// for a main in (the brand package). The commands that change an application (impulse
// add, impulse remove) and the reading of an application's shape list Go files by
// themselves still. A test file and a generated file are Go files the Go tool reads; a
// reader that leaves them out says so itself.
func GoToolReads(name string) bool {
	return apptree.GoToolReads(name)
}

// skipsDir reports whether a walk of the application leaves a directory of this name
// alone: one that holds dependencies or build output, and one the Go tool passes over. The
// application's root is the caller's to exempt, whatever it is called.
func skipsDir(name string) bool {
	return apptree.SkipsDir(name)
}

// holdsModule reports whether a directory holds a go.mod of its own.
func holdsModule(abs string) bool {
	return apptree.HoldsModule(abs)
}

var (
	emulatorImageRE   = regexp.MustCompile(`cloud-spanner-emulator/emulator:(\d[A-Za-z0-9.\-]*)`)
	emulatorHarnessRE = regexp.MustCompile(`NewSpannerContainer\(\s*[^,()]+,\s*"([^"]+)"`)
	// firestoreImageRE matches the Cloud SDK emulators image tag the Firestore emulator
	// runs from, in a process file's command or a test harness's image constant.
	firestoreImageRE = regexp.MustCompile(`cloudsdktool/google-cloud-cli:(\d[A-Za-z0-9.]*)-emulators`)
	// firestoreHarnessRE matches a test harness starting the Firestore emulator through
	// db-initiator, which takes the Cloud SDK version and runs the same image.
	firestoreHarnessRE = regexp.MustCompile(`NewFirestoreContainer\(\s*[^,()]+,\s*"([^"]+)"`)
	processFileRE      = regexp.MustCompile(`^(Procfile.*|process-compose.*\.ya?ml)$`)
)

// scan walks the tree once and records every declaration the checks read. Its Go files
// are the ones go build ./... and go test ./... compile from the application's module:
//
//   - A directory ./... matches nothing under (its name begins with "_" or ".", or it is
//     testdata) is not descended into, so a scratch program kept beside the application
//     fails no check; a package under one that the application imports by its path is
//     compiled all the same, and is read (followImports).
//   - A Go file whose own name begins with "_" or "." is not read.
//   - A directory under the root that holds a go.mod is another module, where ./... stops:
//     its Go files are not read, while what the scan reads besides Go (a browser
//     application's angular.json, a process file) still is, since the skeleton's web
//     directory is such a module.
//   - A file placed directly in a directory the Go tool does read, tmp among them, is
//     read, as go build ./... compiles it.
//
// A file a build constraint leaves out (a //go:build line, a _windows.go name) is read:
// some build compiles it. A directory named vendor or dist is left alone as dependencies
// and build output, as it was before the Go tool's rule was followed.
//
// The checks find a call where the source writes it with its package's name, so a Go file
// the scan reads is refused in three forms: when it dot-imports a package of the framework
// or of the application's own module, whose calls it would then write without that name;
// when it imports such a package a second time, since the checks read its calls under one
// name alone; and when it names one of the functions whose calls the checks read
// (calledByName) without calling it, since a call made later through that value is
// written where no check looks.
func (a *App) scan() error {
	a.walked = map[string]bool{".": true}
	err := filepath.WalkDir(a.Root, func(abs string, d fs.DirEntry, err error) error {
		if err != nil {
			return errors.Wrap(err, "filepath.WalkDir()")
		}
		if !d.IsDir() {
			return a.scanFile(abs, d.Name())
		}
		if abs == a.Root {
			return nil
		}
		if skipsDir(d.Name()) {
			a.leftAlone = true

			return filepath.SkipDir
		}
		rel := a.Rel(abs)
		a.walked[rel] = true
		if holdsModule(abs) {
			a.nestedModules = append(a.nestedModules, rel)
		}

		return nil
	})
	if err != nil {
		return errors.Wrap(err, "filepath.WalkDir()")
	}
	if err := a.followImports(); err != nil {
		return err
	}
	if err := versionedSecretClash(a.EnvTags); err != nil {
		return err
	}

	for _, g := range a.Generators {
		g.ReadsWarnings = a.readsWarnings(g)
	}

	a.attachPackageMethods()

	return a.authPackages()
}

func (a *App) scanFile(abs, name string) error {
	rel := a.Rel(abs)
	switch {
	case name == "angular.json":
		a.WebApps = append(a.WebApps, WebApp{Dir: filepath.ToSlash(filepath.Dir(rel))})
	case processFileRE.MatchString(name):
		data, err := os.ReadFile(abs)
		if err != nil {
			return errors.Wrap(err, "os.ReadFile()")
		}
		a.EmulatorImages = append(a.EmulatorImages, findRefs(rel, data, emulatorImageRE)...)
		a.FirestoreEmulatorImages = append(a.FirestoreEmulatorImages, findRefs(rel, data, firestoreImageRE)...)
	case strings.HasSuffix(name, ".go") && (!GoToolReads(name) || a.inNestedModule(rel)):
		// A Go file no build of the application reads, test file or not: one the Go tool
		// passes over by its name, and one in a module of its own under the root.
	case strings.HasSuffix(name, "_test.go"):
		a.goRead = append(a.goRead, rel)
		data, err := os.ReadFile(abs)
		if err != nil {
			return errors.Wrap(err, "os.ReadFile()")
		}
		if err := a.refuseHiddenCalls(rel, data); err != nil {
			return err
		}
		a.EmulatorHarnesses = append(a.EmulatorHarnesses, findRefs(rel, data, emulatorHarnessRE)...)
		a.FirestoreEmulatorImages = append(a.FirestoreEmulatorImages, findRefs(rel, data, firestoreImageRE)...)
		a.FirestoreEmulatorImages = append(a.FirestoreEmulatorImages, findRefs(rel, data, firestoreHarnessRE)...)
		if bytes.Contains(data, []byte(validateRolesFunc)) {
			validations, err := parseRoleValidations(rel, data)
			if err != nil {
				return err
			}
			a.RoleValidations = append(a.RoleValidations, validations...)
		}
		if bytes.Contains(data, []byte(ProvesGrantFunc)) {
			proofs, err := parseGrantProofs(rel, data)
			if err != nil {
				return err
			}
			a.GrantProofs = append(a.GrantProofs, proofs...)
		}
	case strings.HasSuffix(name, ".go"):
		a.goRead = append(a.goRead, rel)

		return a.scanGoFile(abs, rel)
	}

	return nil
}

// inNestedModule reports whether a root-relative path lies in a module of its own under
// the root: in or under a directory the walk found a go.mod in.
func (a *App) inNestedModule(rel string) bool {
	for _, dir := range a.nestedModules {
		if within(dir, rel) {
			return true
		}
	}

	return false
}

// underOwnModule reports whether a root-relative directory the walk did not read lies in
// a module of its own: the directory, or one above it short of the root, holds a go.mod.
func (a *App) underOwnModule(dir string) bool {
	for dir != "." && dir != "/" {
		if holdsModule(a.Abs(dir)) {
			return true
		}
		dir = path.Dir(dir)
	}

	return false
}

// followImports reads the packages the walk left alone that the application imports. The
// walk leaves alone what ./... matches nothing under, yet go build ./... compiles every
// package the matched ones import, wherever it lives, and go test ./... every package
// their tests import: a package under internal/_cfg or under a testdata directory that
// another package names by its import path is the application's, and so is what it
// imports in turn. Each such package's own directory is read, never what lies under it.
// A tree in which the walk left nothing alone has nothing to follow and is not read
// again.
func (a *App) followImports() error {
	if !a.leftAlone {
		return nil
	}
	// The list grows as packages are read, so what they import is followed too.
	for i := 0; i < len(a.goRead); i++ {
		for _, importPath := range importPaths(a.Abs(a.goRead[i])) {
			dir, inModule := a.ModuleDir(importPath)
			if !inModule || a.walked[dir] {
				continue
			}
			a.walked[dir] = true
			if a.underOwnModule(dir) {
				continue
			}
			if err := a.scanPackageDir(dir); err != nil {
				return err
			}
		}
	}

	return nil
}

// importPaths reads the import paths a Go file names. A file that does not parse names
// none here: what is wrong with it is the compiler's to say.
func importPaths(abs string) []string {
	f, err := parser.ParseFile(token.NewFileSet(), abs, nil, parser.ImportsOnly)
	if f == nil || err != nil && len(f.Imports) == 0 {
		return nil
	}
	paths := make([]string, 0, len(f.Imports))
	for _, spec := range f.Imports {
		if importPath, err := strconv.Unquote(spec.Path.Value); err == nil {
			paths = append(paths, importPath)
		}
	}

	return paths
}

// scanPackageDir reads the Go files of one package directory the walk left alone, as the
// walk reads a Go file; a directory the tree does not hold is an import the compiler
// reports, and reads as nothing.
func (a *App) scanPackageDir(dir string) error {
	entries, err := os.ReadDir(a.Abs(dir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}

		return errors.Wrap(err, "os.ReadDir()")
	}
	for _, entry := range entries {
		if entry.IsDir() || !GoToolReads(entry.Name()) {
			continue
		}
		if err := a.scanFile(filepath.Join(a.Abs(dir), entry.Name()), entry.Name()); err != nil {
			return err
		}
	}

	return nil
}

// scanGoFile reads one non-test Go file for a generator program and for env tags.
func (a *App) scanGoFile(abs, rel string) error {
	data, err := os.ReadFile(abs)
	if err != nil {
		return errors.Wrap(err, "os.ReadFile()")
	}
	if err := a.refuseHiddenCalls(rel, data); err != nil {
		return err
	}

	if bytes.Contains(data, []byte("NewResourceGenerator")) {
		g, err := parseGenerator(rel, data)
		if err != nil {
			return err
		}
		if g != nil {
			a.Generators = append(a.Generators, g)
		}
	}

	generated := strings.HasPrefix(filepath.Base(rel), "zz_gen_")
	if !generated && declaresEnv(data) {
		tags, err := parseEnvTags(rel, data)
		if err != nil {
			return err
		}
		a.EnvTags = append(a.EnvTags, tags...)
	}

	if !generated {
		if err := a.scanAnnotations(rel, data); err != nil {
			return err
		}
		if err := a.scanFeatures(rel, data); err != nil {
			return err
		}
	}

	if bytes.Contains(data, []byte("//go:generate")) {
		a.GoGenerate = append(a.GoGenerate, findDirectives(rel, data)...)
	}

	if bytes.Contains(data, []byte(ciDirective)) && !generated {
		ci, err := findCIDirective(rel, data)
		if err != nil {
			return err
		}
		if ci != nil {
			if a.CI != nil {
				return errors.Newf("%s:%d: a second %s line; the application has one, at %s:%d", rel, ci.Line, ciDirective, a.CI.File, a.CI.Line)
			}
			a.CI = ci
		}
	}

	if bytes.HasPrefix(bytes.TrimSpace(stripLeadingComments(data)), []byte("package main")) {
		dir := path.Dir(rel)
		if len(a.MainPackages) == 0 || a.MainPackages[len(a.MainPackages)-1] != dir {
			a.MainPackages = append(a.MainPackages, dir)
		}
	}

	if bytes.Contains(data, []byte(sessionImportPath)) {
		auths, unattached, err := parseAuths(rel, data, a.packagePath(path.Dir(rel)))
		if err != nil {
			return err
		}
		a.Auths = append(a.Auths, auths...)
		a.unattached = append(a.unattached, unattached...)
	}

	a.goFiles = append(a.goFiles, rel)

	if err := a.scanSecretRetirement(rel, data, generated); err != nil {
		return err
	}

	return a.scanAccess(rel, data)
}

// The retirement of a version of the application secret, as the scan reads it: the
// generated file listing a package's resources that store a secret, the function in it,
// the library's function declaring one holder, the function that retires a version, how
// many arguments it takes before the holders, and the standard library's package and
// function that join several packages' lists.
const (
	generatedHoldersFile = "zz_gen_secret_holders.go"

	secretHoldersFunc  = "SecretHolders"
	secretHolderOfFunc = "SecretHolderOf"
	retireSecretFunc   = "RetireSecretVersion"
	retireSecretFixed  = 3
	slicesImportPath   = "slices"
	slicesConcatFunc   = "Concat"
	appendBuiltin      = "append"
	nilIdentifier      = "nil"
)

// scanSecretRetirement records what one Go file says of the retirement of a version of
// the application secret: a package's generated list of the resources that store a
// secret, or, in a file the author wrote, the calls that retire a version.
func (a *App) scanSecretRetirement(rel string, data []byte, generated bool) error {
	switch {
	case filepath.Base(rel) == generatedHoldersFile:
		a.SecretHolders = append(a.SecretHolders, SecretHolders{File: rel, Package: a.packagePath(path.Dir(rel)), Stored: bytes.Contains(data, []byte(secretHolderOfFunc+"["))})
	case !generated && bytes.Contains(data, []byte(retireSecretFunc)):
		retirements, err := parseSecretRetirements(rel, data, a.packagePath(path.Dir(rel)))
		if err != nil {
			return err
		}
		a.SecretRetirements = append(a.SecretRetirements, retirements...)
	}

	return nil
}

// parseSecretRetirements returns every resource.RetireSecretVersion call in the file,
// each with the packages whose generated SecretHolders() it is handed. The holders are
// read where the call names them: a package's SecretHolders() itself, several joined by
// slices.Concat or append, or a variable of the calling function assigned from those.
func parseSecretRetirements(rel string, src []byte, ownPkg string) ([]SecretRetirement, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, errors.Wrap(err, "parser.ParseFile()")
	}
	pkg := localImportName(f, resourceImportPath)
	if pkg == "" {
		return nil, nil
	}

	var retirements []SecretRetirement
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !isQualified(call.Fun, pkg, retireSecretFunc) {
				return true
			}
			r := holdersReader{imports: fileImports(f), slices: localImportName(f, slicesImportPath), ownPkg: ownPkg, body: fn.Body, followed: map[string]bool{}, packages: map[string]bool{}, unread: false}
			if len(call.Args) > retireSecretFixed {
				for _, arg := range call.Args[retireSecretFixed:] {
					r.read(arg)
				}
			}
			retirement := SecretRetirement{File: rel, Line: fset.Position(call.Pos()).Line, Packages: nil, Unread: r.unread}
			for p := range r.packages {
				retirement.Packages = append(retirement.Packages, p)
			}
			slices.Sort(retirement.Packages)
			retirements = append(retirements, retirement)

			return true
		})
	}

	return retirements, nil
}

// holdersReader reads the secret holders one retirement call is handed: the packages
// whose SecretHolders() reach it, and whether any holder comes from where it cannot read.
type holdersReader struct {
	imports  map[string]string
	slices   string
	ownPkg   string
	body     *ast.BlockStmt
	followed map[string]bool
	packages map[string]bool
	unread   bool
}

// read reads one expression holding holders.
func (r *holdersReader) read(expr ast.Expr) {
	switch e := expr.(type) {
	case *ast.ParenExpr:
		r.read(e.X)
	case *ast.Ident:
		if e.Name != nilIdentifier {
			r.follow(e.Name)
		}
	case *ast.CallExpr:
		switch {
		case r.holdersOf(e):
		case isQualified(e.Fun, r.slices, slicesConcatFunc), calleeIs(e.Fun, appendBuiltin):
			for _, arg := range e.Args {
				r.read(arg)
			}
		default:
			r.unread = true
		}
	default:
		r.unread = true
	}
}

// holdersOf records a package's SecretHolders() call, and reports whether the call is
// one: on an imported package, or unqualified in the package that declares it.
func (r *holdersReader) holdersOf(call *ast.CallExpr) bool {
	if len(call.Args) != 0 {
		return false
	}
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		if fun.Name == secretHoldersFunc {
			r.packages[r.ownPkg] = true

			return true
		}
	case *ast.SelectorExpr:
		if id, ok := fun.X.(*ast.Ident); ok && fun.Sel.Name == secretHoldersFunc && r.imports[id.Name] != "" {
			r.packages[r.imports[id.Name]] = true

			return true
		}
	}

	return false
}

// follow reads what a variable of the calling function was assigned. A name the
// function assigns nowhere is a parameter or a package's variable, which is not read.
func (r *holdersReader) follow(name string) {
	if r.followed[name] {
		return
	}
	r.followed[name] = true
	assigned := false
	bind := func(target, value ast.Expr) {
		if id, ok := target.(*ast.Ident); ok && id.Name == name {
			assigned = true
			r.read(value)
		}
	}
	ast.Inspect(r.body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			if len(n.Lhs) == len(n.Rhs) {
				for i := range n.Lhs {
					bind(n.Lhs[i], n.Rhs[i])
				}
			}
		case *ast.ValueSpec:
			if len(n.Names) == len(n.Values) {
				for i := range n.Names {
					bind(n.Names[i], n.Values[i])
				}
			}
		}

		return true
	})
	if !assigned {
		r.unread = true
	}
}

// calleeIs reports whether a call's function is the bare name given.
func calleeIs(fun ast.Expr, name string) bool {
	id, ok := fun.(*ast.Ident)

	return ok && id.Name == name
}

// The permission engine's constructor and the option handing it a change signal.
const (
	engineNewFunc    = "New"
	changeSignalFunc = "WithChangeSignal"
)

// parseEngines returns every access.New call in the file, each with whether an
// access.WithChangeSignal option is among its arguments and whether it forwards options
// it cannot see.
func parseEngines(rel string, src []byte) ([]Engine, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, errors.Wrap(err, "parser.ParseFile()")
	}
	pkg := localImportName(f, accessImportPath)
	if pkg == "" {
		return nil, nil
	}

	var engines []Engine
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !isQualified(call.Fun, pkg, engineNewFunc) {
			return true
		}
		e := Engine{File: rel, Line: fset.Position(call.Pos()).Line, Package: path.Dir(rel), OptionsForwarded: call.Ellipsis.IsValid()}
		for _, arg := range call.Args {
			if option, ok := arg.(*ast.CallExpr); ok && isQualified(option.Fun, pkg, changeSignalFunc) {
				e.ChangeSignal = true
			}
		}
		engines = append(engines, e)

		return true
	})

	return engines, nil
}

// scanAnnotations records the file's struct annotations (@permissionScope(domain),
// @tenant, @tenantDirectory, @outlet), its constants carrying @manualAddResource or
// @manualAddPermission, and its constructions of a tenant roster.
func (a *App) scanAnnotations(rel string, data []byte) error {
	if bytes.Contains(data, []byte("@"+permissionScopeKeyword)) || bytes.Contains(data, []byte("@"+outletKeyword)) || bytes.Contains(data, []byte("@"+tenantKeyword)) {
		docs, err := parseStructDocs(rel, data)
		if err != nil {
			return err
		}
		for _, d := range docs {
			if domainScopeRE.MatchString(d.Doc) {
				a.DomainResources = append(a.DomainResources, domainResource(rel, &d))
			}
			if m := tenantRE.FindStringSubmatch(d.Doc); m != nil {
				a.TenantRecords = append(a.TenantRecords, TenantRecord{File: rel, Line: d.Line, Name: d.Name})
			}
			if tenantDirectoryRE.MatchString(d.Doc) {
				a.TenantDirectories = append(a.TenantDirectories, directoryRecord(rel, &d))
			}
			for _, m := range outletRE.FindAllStringSubmatch(d.Doc, -1) {
				member := OutletMember{File: rel, Line: d.Line, Name: d.Name}
				for _, name := range strings.Split(m[1], ",") {
					if name = strings.TrimSpace(name); name != "" {
						member.Outlets = append(member.Outlets, name)
					}
				}
				a.OutletMembers = append(a.OutletMembers, member)
			}
		}
	}

	if bytes.Contains(data, []byte("@"+manualAddResourceKeyword)) || bytes.Contains(data, []byte("@"+manualAddPermissionKeyword)) {
		annotations, loose, err := parseConstantAnnotations(rel, data)
		if err != nil {
			return err
		}
		a.ConstantAnnotations = append(a.ConstantAnnotations, annotations...)
		a.LooseAnnotations = append(a.LooseAnnotations, loose...)
	}

	if bytes.Contains(data, []byte(rosterConstructorSuffix)) {
		rosters, err := parseRosterConstructions(rel, data)
		if err != nil {
			return err
		}
		a.RosterConstructions = append(a.RosterConstructions, rosters...)
	}

	return nil
}

// domainResource reads a struct annotated @permissionScope(domain): the resource, whether
// its rows are a table's (@resource), and how they are bound to their tenant, by the
// field annotated @domain: the column that holds the tenant's key on the row itself, or
// the join path through another row with the column the row leaves by.
func domainResource(rel string, d *structDoc) DomainResource {
	r := DomainResource{File: rel, Line: d.Line, Name: d.Name, Table: resourceRE.MatchString(d.Doc)}
	for i := range d.Fields {
		f := &d.Fields[i]
		if domainBareRE.MatchString(f.Doc) {
			r.DomainColumn, _, _ = strings.Cut(reflect.StructTag(f.Tag).Get(spannerTag), ",")

			break
		}
		if via := domainViaRE.FindStringSubmatch(f.Doc); via != nil {
			r.DomainVia = via[1]
			r.ViaColumn, _, _ = strings.Cut(reflect.StructTag(f.Tag).Get(spannerTag), ",")

			break
		}
	}

	return r
}

// directoryRecord reads a struct annotated @tenantDirectory: the record, and the field
// annotated @directoryDefaultSeat when it carries one.
func directoryRecord(rel string, d *structDoc) DirectoryRecord {
	record := DirectoryRecord{File: rel, Line: d.Line, Name: d.Name}
	for i := range d.Fields {
		if f := &d.Fields[i]; directoryDefaultSeatRE.MatchString(f.Doc) {
			record.DefaultSeatField = f.Name
			record.DefaultSeatName = wireName(f)

			break
		}
	}

	return record
}

// The generated tenant roster constructor's shape (New<Record>Roster) and the option
// handing it the tenants signal.
const (
	rosterConstructorSuffix = "Roster"
	tenantSignalsFunc       = "WithTenantSignals"
)

// rosterConstructorRE matches a generated tenant roster constructor's name.
var rosterConstructorRE = regexp.MustCompile(`^New[A-Z][A-Za-z0-9]*` + rosterConstructorSuffix + `$`)

// parseRosterConstructions returns every call in the file to a generated tenant roster
// constructor, each with the name its result is bound to and whether a
// resource.WithTenantSignals option is among its arguments.
func parseRosterConstructions(rel string, src []byte) ([]RosterConstruction, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, errors.Wrap(err, "parser.ParseFile()")
	}

	holders := rosterHolders(f)
	var rosters []RosterConstruction
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := calleeName(call.Fun)
		if !rosterConstructorRE.MatchString(name) {
			return true
		}
		r := RosterConstruction{File: rel, Line: fset.Position(call.Pos()).Line, Package: path.Dir(rel), Constructor: name, Holder: holders[call]}
		for _, arg := range call.Args {
			if option, ok := ast.Unparen(arg).(*ast.CallExpr); ok && calleeName(option.Fun) == tenantSignalsFunc {
				r.Signals = true
			}
		}
		rosters = append(rosters, r)

		return true
	})

	return rosters, nil
}

// rosterHolders maps each call that is the right side of a definition or assignment, the
// value of a declared variable, or the value of a composite literal element to the name
// its result is bound to.
func rosterHolders(f *ast.File) map[*ast.CallExpr]string {
	holders := map[*ast.CallExpr]string{}
	bind := func(target ast.Expr, value ast.Expr) {
		call, ok := ast.Unparen(value).(*ast.CallExpr)
		if !ok {
			return
		}
		if name := calleeName(target); name != "" {
			holders[call] = name
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			if len(n.Lhs) == len(n.Rhs) {
				for i := range n.Lhs {
					bind(n.Lhs[i], n.Rhs[i])
				}
			}
		case *ast.ValueSpec:
			if len(n.Names) == len(n.Values) {
				for i := range n.Names {
					bind(n.Names[i], n.Values[i])
				}
			}
		case *ast.KeyValueExpr:
			bind(n.Key, n.Value)
		}

		return true
	})

	return holders
}

// calleeName is the final name of an identifier or selector expression, looking through
// parentheses around it: the name a call is made by ((F)(...) is made by F), or the name a
// value is bound to (tenants for conf.tenants); empty for any other expression.
func calleeName(fun ast.Expr) string {
	switch fun := ast.Unparen(fun).(type) {
	case *ast.Ident:
		return fun.Name
	case *ast.SelectorExpr:
		return fun.Sel.Name
	default:
		return ""
	}
}

// scanFeatures records the file's feature flag declarations (resource.Feature constants)
// and its @feature annotations.
func (a *App) scanFeatures(rel string, data []byte) error {
	if bytes.Contains(data, []byte(resourceImportPath)) && bytes.Contains(data, []byte(featureTypeName)) {
		flags, err := parseFeatureFlags(rel, data, a.packagePath(path.Dir(rel)))
		if err != nil {
			return err
		}
		a.Features = append(a.Features, flags...)
	}
	if bytes.Contains(data, []byte("@"+featureKeyword)) {
		gates, err := parseFeatureGates(rel, data)
		if err != nil {
			return err
		}
		a.FeatureGates = append(a.FeatureGates, gates...)
	}

	return nil
}

// scanAccess records what the file does with the permission engine: its constructions
// (access.New), its access.WithDefaultRoles calls and its CheckPolicy calls.
func (a *App) scanAccess(rel string, data []byte) error {
	if bytes.Contains(data, []byte(accessImportPath)) {
		engines, err := parseEngines(rel, data)
		if err != nil {
			return err
		}
		a.Engines = append(a.Engines, engines...)
	}
	if bytes.Contains(data, []byte(defaultRolesFunc)) {
		calls, err := parseDefaultRoles(rel, data, a.packagePath(path.Dir(rel)))
		if err != nil {
			return err
		}
		a.DefaultRoles = append(a.DefaultRoles, calls...)
	}
	if bytes.Contains(data, []byte(checkPolicyFunc)) {
		checks, err := parsePolicyChecks(rel, data)
		if err != nil {
			return err
		}
		a.PolicyChecks = append(a.PolicyChecks, checks...)
	}

	return nil
}

// The role-policy entry points the scan reads: the option handing a role file to the
// engine, the deploy's policy check, the validation a test runs, and the function an auth
// package exports its embedded role file through.
const (
	defaultRolesFunc  = "WithDefaultRoles"
	checkPolicyFunc   = "CheckPolicy"
	validateRolesFunc = "ValidateRoles"
	rolesFunc         = "Roles"
	authNameConst     = "Name"
)

// parseDefaultRoles returns every access.WithDefaultRoles call in the file, each carrying
// the package whose Roles() it hands over. ownPkg is the file's package import path, which
// an unqualified Roles() names. The role file is the call's first argument, and the one
// after it is the auth's name: each call carries the package whose Name it is too, so a
// file handed over under another package's name can be told, and the argument as the
// source writes it, so a finding can quote a name that is no package's Name. Each call
// says too whether it is written among the arguments of an access.New call, the engine it
// goes to. The file is parsed with its objects resolved, so that an unqualified Roles or
// Name a function declares for itself (a parameter, a result, a variable), which hides
// the package's, is read as some other way; one the file declares at its top level, or
// one it does not declare, is the package's own.
func parseDefaultRoles(rel string, src []byte, ownPkg string) ([]DefaultRoles, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, src, 0)
	if err != nil {
		return nil, errors.Wrap(err, "parser.ParseFile()")
	}
	pkg := localImportName(f, accessImportPath)
	if pkg == "" {
		return nil, nil
	}
	imports := fileImports(f)
	inFunction := declaredInFunction(f)

	var calls []DefaultRoles
	inEngine := map[*ast.CallExpr]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if isQualified(call.Fun, pkg, engineNewFunc) {
			for _, arg := range call.Args {
				if option, ok := ast.Unparen(arg).(*ast.CallExpr); ok {
					inEngine[option] = true
				}
			}
		}
		if !isQualified(call.Fun, pkg, defaultRolesFunc) {
			return true
		}
		d := DefaultRoles{File: rel, Line: fset.Position(call.Pos()).Line, InEngine: inEngine[call]}
		if len(call.Args) > 0 {
			if roles, ok := call.Args[0].(*ast.CallExpr); !ok || !inFunction(roles.Fun) {
				d.RolesPackage = rolesCallPackage(call.Args[0], imports, ownPkg)
			}
		}
		if len(call.Args) > 1 {
			if !inFunction(call.Args[1]) {
				d.NamePackage = authNamePackage(call.Args[1], imports, ownPkg)
			}
			d.NameText = string(src[fset.Position(call.Args[1].Pos()).Offset:fset.Position(call.Args[1].End()).Offset])
		}
		calls = append(calls, d)

		return true
	})

	return calls, nil
}

// declaredInFunction returns the test of whether an identifier of a file parsed with its
// objects resolved names something a function of the file declares (a parameter, a
// result, a variable, a constant): one whose declaration is not among the file's
// top-level ones. An identifier the file declares at its top level, or does not declare
// at all, does not.
func declaredInFunction(f *ast.File) func(ast.Expr) bool {
	topLevel := map[any]bool{}
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			topLevel[d] = true
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				topLevel[spec] = true
			}
		}
	}

	return func(expr ast.Expr) bool {
		id, ok := expr.(*ast.Ident)

		return ok && id.Obj != nil && !topLevel[id.Obj.Decl]
	}
}

// rolesCallPackage reads a Roles() call: the import path of the package it is called on,
// ownPkg for an unqualified call, or empty for anything else.
func rolesCallPackage(expr ast.Expr, imports map[string]string, ownPkg string) string {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return ""
	}
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		if fun.Name == rolesFunc {
			return ownPkg
		}
	case *ast.SelectorExpr:
		if id, ok := fun.X.(*ast.Ident); ok && fun.Sel.Name == rolesFunc {
			return imports[id.Name]
		}
	}

	return ""
}

// authNamePackage reads the auth's name a role file is handed over under: the import path
// of the package whose Name it is, ownPkg for Name unqualified, or empty for anything
// else (a literal, a variable, a call), which nothing here can tie to a package.
func authNamePackage(expr ast.Expr, imports map[string]string, ownPkg string) string {
	switch name := expr.(type) {
	case *ast.Ident:
		if name.Name == authNameConst {
			return ownPkg
		}
	case *ast.SelectorExpr:
		if id, ok := name.X.(*ast.Ident); ok && name.Sel.Name == authNameConst {
			return imports[id.Name]
		}
	}

	return ""
}

// parsePolicyChecks returns every CheckPolicy call in the file: the deploy's policy check
// on the auth's engine, whatever the receiver is called.
func parsePolicyChecks(rel string, src []byte) ([]PolicyCheck, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, errors.Wrap(err, "parser.ParseFile()")
	}
	var checks []PolicyCheck
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr); ok && sel.Sel.Name == checkPolicyFunc {
			checks = append(checks, PolicyCheck{File: rel, Line: fset.Position(call.Pos()).Line})
		}

		return true
	})

	return checks, nil
}

// parseRoleValidations returns every access.ValidateRoles call in a test file, each
// carrying the auth packages whose Roles() the file calls: a test that validates a role
// file parses the file its auth package embeds, so the call names the file the validation
// is over.
func parseRoleValidations(rel string, src []byte) ([]RoleValidation, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, errors.Wrap(err, "parser.ParseFile()")
	}
	pkg := localImportName(f, accessImportPath)
	if pkg == "" {
		return nil, nil
	}

	imports := fileImports(f)
	var rolesPackages []string
	var calls []RoleValidation
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if isQualified(call.Fun, pkg, validateRolesFunc) {
			calls = append(calls, RoleValidation{File: rel, Line: fset.Position(call.Pos()).Line})
		}
		if p := rolesCallPackage(call, imports, ""); p != "" && !slices.Contains(rolesPackages, p) {
			rolesPackages = append(rolesPackages, p)
		}

		return true
	})
	slices.Sort(rolesPackages)
	for i := range calls {
		calls[i].RolesPackages = rolesPackages
	}

	return calls, nil
}

// fileImports maps the names a file imports packages under to their import paths.
func fileImports(f *ast.File) map[string]string {
	imports := map[string]string{}
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		local := path.Base(p)
		if imp.Name != nil {
			local = imp.Name.Name
		}
		imports[local] = p
	}

	return imports
}

// packagePath is the import path of a root-relative directory, or empty without a module
// directive.
func (a *App) packagePath(dir string) string {
	if a.GoMod == nil || a.GoMod.Module == nil {
		return ""
	}
	if dir == "." {
		return a.GoMod.Module.Mod.Path
	}

	return a.GoMod.Module.Mod.Path + "/" + dir
}

// findRefs returns one EmulatorRef per line of data matching re, whose first group is the
// version.
func findRefs(rel string, data []byte, re *regexp.Regexp) []EmulatorRef {
	var refs []EmulatorRef
	line := 0
	for l := range bytes.Lines(data) {
		line++
		for _, m := range re.FindAllSubmatch(l, -1) {
			refs = append(refs, EmulatorRef{File: rel, Line: line, Version: string(m[1])})
		}
	}

	return refs
}

// goGenerateRE matches a //go:generate directive at the start of a line.
var goGenerateRE = regexp.MustCompile(`^//go:generate\s+(.+?)\s*$`)

// findDirectives returns the //go:generate directives in the file.
func findDirectives(rel string, data []byte) []Directive {
	var directives []Directive
	line := 0
	for l := range bytes.Lines(data) {
		line++
		if m := goGenerateRE.FindSubmatch(bytes.TrimRight(l, "\r\n")); m != nil {
			directives = append(directives, Directive{File: rel, Line: line, Command: string(m[1])})
		}
	}

	return directives
}

// ciDirective opens the //impulse:ci line.
const ciDirective = "//impulse:ci"

// ciDirectiveRE matches the line at the start of a line, with its settings after it.
var ciDirectiveRE = regexp.MustCompile(`^//impulse:ci(?:\s+(.*?))?\s*$`)

// The //impulse:ci settings.
const (
	ciLargeRunner   = "large-runner"
	ciTestCache     = "test-cache"
	ciDefaultBranch = "default-branch"
)

// branchNameRE is the shape of a branch name the line may declare: git's rules in
// outline (no leading dot or dash, no spaces, no .., no trailing slash).
var branchNameRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._/-]*[A-Za-z0-9_]$|^[A-Za-z0-9_]$`)

// findCIDirective returns the file's //impulse:ci line, parsed, nil without one, and an
// error for a second line in the file or a setting it cannot read.
func findCIDirective(rel string, data []byte) (*CIDirective, error) {
	var found *CIDirective
	line := 0
	for l := range bytes.Lines(data) {
		line++
		m := ciDirectiveRE.FindSubmatch(bytes.TrimRight(l, "\r\n"))
		if m == nil {
			continue
		}
		if found != nil {
			return nil, errors.Newf("%s:%d: a second %s line; the application has one, at %s:%d", rel, line, ciDirective, found.File, found.Line)
		}
		d, err := parseCIDirective(string(m[1]))
		if err != nil {
			return nil, errors.Wrapf(err, "%s:%d: %s", rel, line, ciDirective)
		}
		d.File, d.Line = rel, line
		found = d
	}

	return found, nil
}

// parseCIDirective reads the settings after //impulse:ci: whitespace-separated
// name=value pairs, each name at most once.
func parseCIDirective(settings string) (*CIDirective, error) {
	d := &CIDirective{}
	seen := map[string]bool{}
	for _, field := range strings.Fields(settings) {
		name, value, ok := strings.Cut(field, "=")
		if !ok || value == "" {
			return nil, errors.Newf("%q is not a name=value setting (the settings are %s=<job>,<job>,... or none, %s=on or off, and %s=<branch>)", field, ciLargeRunner, ciTestCache, ciDefaultBranch)
		}
		if seen[name] {
			return nil, errors.Newf("%s is set twice", name)
		}
		seen[name] = true
		switch name {
		case ciLargeRunner:
			d.LargeRunner = []string{}
			if value != "none" {
				d.LargeRunner = strings.Split(value, ",")
			}
		case ciTestCache:
			switch value {
			case "on", "off":
				on := value == "on"
				d.TestCache = &on
			default:
				return nil, errors.Newf("%s=%s: the value is on or off", name, value)
			}
		case ciDefaultBranch:
			if !branchNameRE.MatchString(value) || strings.Contains(value, "..") {
				return nil, errors.Newf("%s=%s: the value is a branch name", name, value)
			}
			d.DefaultBranch = value
		default:
			return nil, errors.Newf("%q is not a setting (the settings are %s, %s and %s)", name, ciLargeRunner, ciTestCache, ciDefaultBranch)
		}
	}

	return d, nil
}

// stripLeadingComments drops the comment lines and blank lines before a file's package
// clause, so the clause can be read without a full parse.
func stripLeadingComments(data []byte) []byte {
	for {
		trimmed := bytes.TrimLeft(data, " \t\r\n")
		switch {
		case bytes.HasPrefix(trimmed, []byte("//")):
			i := bytes.IndexByte(trimmed, '\n')
			if i < 0 {
				return nil
			}
			data = trimmed[i+1:]
		case bytes.HasPrefix(trimmed, []byte("/*")):
			i := bytes.Index(trimmed, []byte("*/"))
			if i < 0 {
				return nil
			}
			data = trimmed[i+2:]
		default:
			return trimmed
		}
	}
}

// The annotations and import the scan reads.
const (
	permissionScopeKeyword      = "permissionScope"
	outletKeyword               = "outlet"
	tenantKeyword               = "tenant"
	tenantDirectoryKeyword      = "tenantDirectory"
	resourceKeyword             = "resource"
	domainKeyword               = "domain"
	spannerTag                  = "spanner"
	directoryDefaultSeatKeyword = "directoryDefaultSeat"
	manualAddResourceKeyword    = "manualAddResource"
	manualAddPermissionKeyword  = "manualAddPermission"
	accessImportPath            = "github.com/impulseframework/impulse/access"
)

// domainScopeRE matches the @permissionScope(domain) struct annotation; outletRE
// captures the names an @outlet(...) annotation lists; tenantRE matches the @tenant
// struct annotation, a line of its own in the doc comment, with or without arguments;
// tenantDirectoryRE matches the @tenantDirectory struct annotation, a line of its own,
// and directoryDefaultSeatRE the @directoryDefaultSeat field annotation; resourceRE
// matches the @resource struct annotation, a line of its own, domainBareRE the bare
// @domain field annotation, a line of its own, and domainViaRE captures the join path of
// @domain(via: ...); constantAnnotationRE captures the keyword of a line that starts
// with @manualAddResource or @manualAddPermission, as generation reads the two off a
// constant's comment (a longer word, @manualAddResourceSet, is not a match).
var (
	domainScopeRE          = regexp.MustCompile(`@` + permissionScopeKeyword + `\(\s*domain\s*\)`)
	outletRE               = regexp.MustCompile(`@` + outletKeyword + `\(([^)]*)\)`)
	tenantRE               = regexp.MustCompile(`(?m)^\s*@` + tenantKeyword + `\s*(?:\(([^)]*)\))?\s*$`)
	tenantDirectoryRE      = regexp.MustCompile(`(?m)^\s*@` + tenantDirectoryKeyword + `\s*$`)
	directoryDefaultSeatRE = regexp.MustCompile(`@` + directoryDefaultSeatKeyword + `\b`)
	resourceRE             = regexp.MustCompile(`(?m)^\s*@` + resourceKeyword + `\s*(?:\([^)]*\))?\s*$`)
	domainBareRE           = regexp.MustCompile(`(?m)^\s*@` + domainKeyword + `\s*$`)
	domainViaRE            = regexp.MustCompile(`(?m)^\s*@` + domainKeyword + `\(\s*via:\s*([^)]*?)\s*\)\s*$`)
	constantAnnotationRE   = regexp.MustCompile(`(?m)^\s*@(` + manualAddResourceKeyword + `|` + manualAddPermissionKeyword + `)\b`)
)

// structDoc is one struct type declaration with its doc comment and its fields' comments.
type structDoc struct {
	Name   string
	Line   int
	Doc    string
	Fields []fieldDoc
}

// fieldDoc is one named field of a struct: its name, its doc comment and trailing
// comment together, and its tag.
type fieldDoc struct {
	Name string
	Doc  string
	Tag  string
}

// parseStructDocs returns every struct type in the file that has a doc comment.
func parseStructDocs(rel string, src []byte) ([]structDoc, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, errors.Wrap(err, "parser.ParseFile()")
	}

	var docs []structDoc
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			st, isStruct := ts.Type.(*ast.StructType)
			if !isStruct {
				continue
			}
			doc := ts.Doc
			if doc == nil && len(gen.Specs) == 1 {
				doc = gen.Doc
			}
			if doc == nil {
				continue
			}
			docs = append(docs, structDoc{Name: ts.Name.Name, Line: fset.Position(ts.Pos()).Line, Doc: doc.Text(), Fields: fieldDocs(st)})
		}
	}

	return docs, nil
}

// parseConstantAnnotations returns the file's package-level constants whose comment
// starts a line with @manualAddResource or @manualAddPermission: the comment above the
// constant, the one at the end of its line, and the one above its const declaration,
// grouped or not, so a group's comment counts for every constant of the group. One
// record for each constant and keyword, keywords in the order the comments first name
// them. It returns as well each such line in a comment that belongs to no declaration
// (gocomments.Loose, the definition generation goes by), one record for each line. A
// declaration's own comment where the declaration is not a constant (a function's, which
// may be prose that starts a line with the annotation) is not read.
func parseConstantAnnotations(rel string, src []byte) ([]ConstantAnnotation, []LooseAnnotation, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, nil, errors.Wrap(err, "parser.ParseFile()")
	}

	var annotations []ConstantAnnotation
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			var keywords []string
			for _, group := range []*ast.CommentGroup{gen.Doc, vs.Doc, vs.Comment} {
				for _, m := range constantAnnotationRE.FindAllStringSubmatch(group.Text(), -1) {
					if !slices.Contains(keywords, m[1]) {
						keywords = append(keywords, m[1])
					}
				}
			}
			for _, name := range vs.Names {
				for _, keyword := range keywords {
					annotations = append(annotations, ConstantAnnotation{File: rel, Line: fset.Position(name.Pos()).Line, Constant: name.Name, Keyword: keyword})
				}
			}
		}
	}

	var loose []LooseAnnotation
	for _, group := range gocomments.Loose(f) {
		for line := range strings.Lines(group.Text()) {
			if m := constantAnnotationRE.FindStringSubmatch(line); m != nil {
				loose = append(loose, LooseAnnotation{File: rel, Line: fset.Position(group.Pos()).Line, Keyword: m[1], Text: strings.TrimSpace(line)})
			}
		}
	}

	return annotations, loose, nil
}

// StructColumns reads the structs of the Go package in a root-relative directory, test
// files apart: for each struct type, the column each of its named fields maps to by its
// spanner tag. A join path (@domain(via: ...)) names a Go field on each row it passes,
// and this is how a reader of the schema finds that field's column.
func (a *App) StructColumns(dir string) (map[string]map[string]string, error) {
	entries, err := os.ReadDir(a.Abs(dir))
	if err != nil {
		return nil, errors.Wrap(err, "os.ReadDir()")
	}
	columns := map[string]map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !GoToolReads(e.Name()) || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		rel := path.Join(dir, e.Name())
		src, err := os.ReadFile(a.Abs(rel))
		if err != nil {
			return nil, errors.Wrap(err, "os.ReadFile()")
		}
		f, err := parser.ParseFile(token.NewFileSet(), rel, src, parser.SkipObjectResolution)
		if err != nil {
			return nil, errors.Wrap(err, "parser.ParseFile()")
		}
		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				st, isStruct := ts.Type.(*ast.StructType)
				if !isStruct {
					continue
				}
				fields := map[string]string{}
				for _, field := range fieldDocs(st) {
					if column, _, _ := strings.Cut(reflect.StructTag(field.Tag).Get(spannerTag), ","); column != "" {
						fields[field.Name] = column
					}
				}
				columns[ts.Name.Name] = fields
			}
		}
	}

	return columns, nil
}

// fieldDocs reads a struct's named fields with their comments: the doc comment above a
// field and the comment on its line, where an annotation may stand.
func fieldDocs(st *ast.StructType) []fieldDoc {
	var fields []fieldDoc
	for _, f := range st.Fields.List {
		doc := f.Doc.Text()
		if f.Comment != nil {
			doc += f.Comment.Text()
		}
		tag := ""
		if f.Tag != nil {
			if s, err := strconv.Unquote(f.Tag.Value); err == nil {
				tag = s
			}
		}
		for _, name := range f.Names {
			fields = append(fields, fieldDoc{Name: name.Name, Doc: doc, Tag: tag})
		}
	}

	return fields
}

// wireName is a field's name on the wire as the generator writes it: its json tag, or
// its name in camel case.
func wireName(f *fieldDoc) string {
	if name, _, _ := strings.Cut(reflect.StructTag(f.Tag).Get("json"), ","); name != "" && name != "-" {
		return name
	}

	return strcase.ToCamel(f.Name)
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

// declaresEnv reports whether a file may declare environment variables: it writes an env
// tag, or imports a package declaring a framework settings struct it may embed.
func declaresEnv(data []byte) bool {
	if bytes.Contains(data, []byte(`env:"`)) {
		return true
	}
	for _, d := range frameworkSettings {
		if bytes.Contains(data, []byte(strconv.Quote(d.Path))) {
			return true
		}
	}

	return false
}

// parseEnvTags returns every env struct tag in the file, in source order, and the
// variables a framework settings struct the file embeds declares, as if the application
// declared them at the embedding. Tags without a name (such as the prefix-only tags on
// embedded structs of the application) are skipped.
func parseEnvTags(rel string, src []byte) ([]EnvTag, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, errors.Wrap(err, "parser.ParseFile()")
	}

	r := &envTagReader{rel: rel, fset: fset, file: f}
	ast.Inspect(f, func(n ast.Node) bool {
		if st, ok := n.(*ast.StructType); ok && r.bad == nil {
			r.readStruct(st)
		}

		return true
	})
	if r.bad != nil {
		return nil, errors.Newf("%s:%d: secret:%q on %s: the secret tag takes %q, %q, %q or %q", rel, r.bad.Line, r.badValue, r.bad.Name, secretTrue, secretGenerated, secretVersioned, secretFalse)
	}

	return r.tags, nil
}

// envTagReader collects the env tags of one file's struct types.
type envTagReader struct {
	rel  string
	fset *token.FileSet
	file *ast.File
	tags []EnvTag
	// bad is the first malformed secret tag: the field's variable, line and value.
	bad      *EnvTag
	badValue string
}

// readStruct reads a struct type's fields in order: a field's env tag, or the variables
// the framework settings struct an embedded field names declares.
func (r *envTagReader) readStruct(st *ast.StructType) {
	for _, field := range st.Fields.List {
		if r.bad != nil {
			return
		}
		line := r.fset.Position(field.Pos()).Line
		if len(field.Names) == 0 {
			if d, ok := r.embedded(field.Type); ok {
				r.tags = append(r.tags, declaredTags(d, r.rel, line)...)

				continue
			}
		}
		r.readField(field, line)
	}
}

// readField reads a field's env tag, and the secret tag beside it.
func (r *envTagReader) readField(field *ast.Field, line int) {
	if field.Tag == nil {
		return
	}
	raw := reflect.StructTag(strings.Trim(field.Tag.Value, "`"))
	value, ok := raw.Lookup("env")
	if !ok {
		return
	}
	tag, ok := parseEnvTag(value)
	if !ok {
		return
	}
	tag.File = r.rel
	tag.Line = line
	if secret, present := raw.Lookup(secretTag); present {
		switch secret {
		case secretTrue:
			tag.Secret = true
		case secretGenerated:
			tag.Secret, tag.Generated = true, true
		case secretVersioned:
			tag.Secret, tag.Generated, tag.Versioned = true, true, true
		case secretFalse:
		default:
			r.bad, r.badValue = &tag, secret
		}
	}
	r.tags = append(r.tags, tag)
}

// embedded resolves an embedded field's type to the framework settings struct it names,
// when it is one: a type of another package, which the file imports under the path the
// struct's declaration gives, by its alias or its name. Any other embedded type, of the
// application's own package or of a package the framework does not declare, is not
// expanded: the scan does not load the application's packages.
func (r *envTagReader) embedded(expr ast.Expr) (cloud.Declaration, bool) {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return cloud.Declaration{}, false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return cloud.Declaration{}, false
	}
	for _, d := range frameworkSettings {
		if d.Name == sel.Sel.Name && localImportName(r.file, d.Path) == pkg.Name {
			return d, true
		}
	}

	return cloud.Declaration{}, false
}

// declaredTags are the env tags a framework settings struct declares, as the application
// declares them by embedding the struct at the line: each at the embedding, naming the
// struct it came from.
func declaredTags(d cloud.Declaration, rel string, line int) []EnvTag {
	tags := make([]EnvTag, 0, len(d.Fields))
	for _, f := range d.Fields {
		tag, ok := parseEnvTag(f.Tag)
		if !ok {
			continue
		}
		tag.File, tag.Line, tag.Origin = rel, line, d.Path+"."+d.Name
		tags = append(tags, tag)
	}

	return tags
}

// versionedSecretClash refuses a variable named as a version of a versioned secret is:
// beside APP_SECRET_KEY declared secret:"versioned", APP_SECRET_KEY_1 is the variable its
// first version is handed over in, so a field declaring that name would be handed the
// version, or would hide it.
func versionedSecretClash(tags []EnvTag) error {
	for _, versioned := range tags {
		if !versioned.Versioned {
			continue
		}
		for _, tag := range tags {
			number, found := strings.CutPrefix(tag.Name, versioned.Name+"_")
			if !found || number == "" || strings.Trim(number, "0123456789") != "" {
				continue
			}

			return errors.Newf("%s:%d: %s is the variable version %s of %s is handed over in (%s:%d declares %s secret:%q, one variable for each version): give the field another name", tag.File, tag.Line, tag.Name, number, versioned.Name, versioned.File, versioned.Line, versioned.Name, secretVersioned)
		}
	}

	return nil
}

// secretTag is the struct tag beside the env tag that declares a secret: secretTrue a
// value someone puts in place, secretGenerated one the tooling makes, secretVersioned one
// the tooling makes and keeps in versions, every enabled one of which the service is
// handed (the application secret, which seals stored secret fields), and secretFalse a
// plain value whose name sounds like a credential.
const (
	secretTag       = "secret"
	secretTrue      = "true"
	secretGenerated = "generated"
	secretVersioned = "versioned"
	secretFalse     = "false"
)

// parseEnvTag interprets an envconfig tag value: NAME[,required][,default=VALUE][,...].
func parseEnvTag(value string) (EnvTag, bool) {
	parts := strings.Split(value, ",")
	name := strings.TrimSpace(parts[0])
	if name == "" {
		return EnvTag{}, false
	}
	tag := EnvTag{Name: name}
	for _, opt := range parts[1:] {
		opt = strings.TrimSpace(opt)
		switch {
		case opt == "required":
			tag.Required = true
		case strings.HasPrefix(opt, "default="):
			tag.HasDefault = true
		}
	}

	return tag, true
}
