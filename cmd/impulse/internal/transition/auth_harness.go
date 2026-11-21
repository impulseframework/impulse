package transition

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-playground/errors/v5"

	"github.com/impulseframework/impulse/app"
)

// mailImport is the session library's mail package, whose Recorder a harness sends an
// auth's mail to.
const mailImport = "github.com/impulseframework/impulse/session/mail"

// sharedSettings are the settings an added auth takes, in a harness, as the first auth's
// opening writes them: the collection the release declares, the live service, the key
// that signs the suites' cookies and the session timeout, which are the application's
// and not one auth's.
var sharedSettings = []string{"Collection", "Signals", "CookieKey", "SessionTimeout"}

// harnessOpening is where a test harness opens the first auth and builds the App over
// it, as the command reads it there: the function, the value the function reports its
// failures through, the call that builds the App as the harness names it, the arguments
// the first auth's New takes before its settings, and the elements of the first auth's
// Settings literal by key, each as the harness writes it.
type harnessOpening struct {
	function  string
	reporter  string
	build     string
	arguments string
	settings  map[string]string
}

// readHarnessOpenings reads where a harness opens the first auth: each function of the
// file with a statement that assigns <auth>.New(..., &<auth>.Settings{...}) and a later
// statement that builds the App from a literal of the harness's configurer. It answers
// none where the file has no such function, and none where another function builds the
// App from such a literal over an auth the command does not read the opening of: a
// harness opened in some of its functions only would serve the added auth's outlet in
// some of its suites only.
func readHarnessOpenings(h *harness, src []byte, authPath, appPath string) ([]*harnessOpening, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, h.file, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, errors.Wrap(err, "parser.ParseFile()")
	}
	auth, err := app.ImportName(h.file, src, authPath)
	if err != nil {
		return nil, err
	}
	built, err := app.ImportName(h.file, src, appPath)
	if err != nil {
		return nil, err
	}
	if auth == "" || built == "" {
		return nil, nil
	}
	text := func(n ast.Node) string {
		return string(src[fset.Position(n.Pos()).Offset:fset.Position(n.End()).Offset])
	}
	var openings []*harnessOpening
	for _, d := range f.Decls {
		fd, isFunc := d.(*ast.FuncDecl)
		if !isFunc || fd.Body == nil {
			continue
		}
		o := &harnessOpening{function: fd.Name.Name, reporter: testingParameter(fd), build: built + "." + appConstructor}
		for _, stmt := range fd.Body.List {
			if !buildsFrom(stmt, o.build, h.typeName) {
				if o.settings == nil {
					o.read(stmt, auth, text)
				}

				continue
			}
			if fd.Recv != nil || o.reporter == "" || o.settings == nil {
				return nil, nil
			}
			openings = append(openings, o)

			break
		}
	}

	return openings, nil
}

// read takes the first auth's opening from a statement that is one: the arguments of its
// New before the settings, and the settings' elements.
func (o *harnessOpening) read(stmt ast.Stmt, auth string, text func(n ast.Node) string) {
	assign, ok := stmt.(*ast.AssignStmt)
	if !ok || len(assign.Rhs) != 1 {
		return
	}
	call, ok := assign.Rhs[0].(*ast.CallExpr)
	if !ok || !isSelector(call.Fun, auth, "New") || len(call.Args) < 2 {
		return
	}
	last := call.Args[len(call.Args)-1]
	if unary, isUnary := last.(*ast.UnaryExpr); isUnary && unary.Op == token.AND {
		last = unary.X
	}
	lit, ok := last.(*ast.CompositeLit)
	if !ok || !isSettingsOf(lit.Type, auth) {
		return
	}
	arguments := make([]string, 0, len(call.Args)-1)
	for _, arg := range call.Args[:len(call.Args)-1] {
		arguments = append(arguments, text(arg))
	}
	o.arguments = strings.Join(arguments, ", ")
	o.settings = map[string]string{}
	for _, elt := range lit.Elts {
		kv, isKeyed := elt.(*ast.KeyValueExpr)
		if !isKeyed {
			continue
		}
		if key, isName := kv.Key.(*ast.Ident); isName {
			o.settings[key.Name] = text(kv.Value)
		}
	}
}

// statements are the statements that open the added auth in the harness, beside the
// first auth and over the same database, with the added auth's own settings: what the
// application has one of as the first auth's opening writes it (sharedSettings, and the
// tenant settings the added auth's Settings carry), its mail to a recorder of its own,
// and its links to its outlet's login page. A directory auth's refused sign-ins return
// to that page too. The first auth's directory registrations stay behind, being the
// first auth's alone, as they do on the data level (withoutRegistrations).
func (o *harnessOpening) statements(w *authWiring, mailPackage string) string {
	name := w.au.Name
	elements := make([]string, 0, len(sharedSettings)+len(w.tenantSettings)+3)
	for _, key := range sharedSettings {
		if value, ok := o.settings[key]; ok {
			elements = append(elements, key+": "+value)
		}
	}
	elements = append(elements, "Mail: "+name+"Sent")
	if value, ok := o.settings["MailLoginURL"]; ok {
		elements = append(elements, "MailLoginURL: "+pageOf(value, w.page))
	}
	if w.au.directory() {
		page := w.page
		if page == "" {
			page = "/login"
		}
		elements = append(elements, fmt.Sprintf("LoginURL: %q", page))
	}
	for _, key := range w.tenantSettings {
		if value, ok := o.settings[key]; ok {
			elements = append(elements, key+": "+value)
		}
	}

	return fmt.Sprintf(`// The %[1]s auth, opened beside the %[2]s auth as the served application opens it,
// over the same database: the %[3]s outlet's session group is served from its
// handlers. Its mail goes to a recorder of its own.
%[1]sSent := &%[4]s.Recorder{}
%[1]sAuth, err := %[1]s.New(%[5]s, &%[1]s.Settings{%[6]s})
if err != nil {
	%[7]s.Fatalf("%[1]s.New() error = %%v", err)
}
%[7]s.Cleanup(func() {
	if err := %[1]sAuth.Close(); err != nil {
		%[7]s.Errorf("%[1]s.Auth.Close() error = %%v", err)
	}
})`, name, w.src.Name, w.au.Outlet, mailPackage, o.arguments, strings.Join(elements, ", "), o.reporter)
}

// pageOf is the first auth's MailLoginURL, as a harness writes it, with the added auth's
// login page in the place of the first auth's: the string that ends the value names the
// page, alone or at the end of an address. A value that ends in no string, and an auth
// with no page of its own, keep the value as it is.
func pageOf(value, page string) string {
	expr, err := parser.ParseExpr(value)
	if err != nil || page == "" {
		return value
	}
	lit := lastString(expr)
	if lit == nil || !strings.HasSuffix(value, lit.Value) {
		return value
	}
	named, err := strconv.Unquote(lit.Value)
	if err != nil {
		return value
	}
	if address, err := url.Parse(named); err == nil && address.Scheme != "" && address.Host != "" {
		address.Path = page
		page = address.String()
	}

	return strings.TrimSuffix(value, lit.Value) + strconv.Quote(page)
}

// lacks names the settings among those the added auth's New refuses nil that the first
// auth's opening does not set, so the added auth's opening has none to take.
func (o *harnessOpening) lacks(required []string) []string {
	var lacking []string
	for _, key := range required {
		if _, ok := o.settings[key]; !ok {
			lacking = append(lacking, key)
		}
	}

	return lacking
}

// testingParameter names the parameter a test helper reports through: the first of the
// function's parameters whose type is the testing package's (*testing.T, testing.TB).
func testingParameter(fd *ast.FuncDecl) string {
	for _, p := range fd.Type.Params.List {
		t := p.Type
		if star, ok := t.(*ast.StarExpr); ok {
			t = star.X
		}
		sel, ok := t.(*ast.SelectorExpr)
		if !ok || len(p.Names) == 0 {
			continue
		}
		if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "testing" {
			return p.Names[0].Name
		}
	}

	return ""
}

// buildsFrom reports a statement that calls build (app.New, as the file names it) with
// a literal of the named type among its arguments.
func buildsFrom(stmt ast.Stmt, build, typeName string) bool {
	pkg, function, _ := strings.Cut(build, ".")
	found := false
	ast.Inspect(stmt, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if found || !ok || !isSelector(call.Fun, pkg, function) {
			return !found
		}
		for _, arg := range call.Args {
			ast.Inspect(arg, func(n ast.Node) bool {
				if lit, ok := n.(*ast.CompositeLit); ok && receiverTypeName(lit.Type) == typeName {
					found = true
				}

				return !found
			})
		}

		return !found
	})

	return found
}

// isSelector reports an expression written as pkg.name.
func isSelector(expr ast.Expr, pkg, name string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	x, ok := sel.X.(*ast.Ident)

	return ok && x.Name == pkg
}

// openInHarness opens the added auth in a harness that opens the first auth and builds
// the App over it: the configurer gains a field for the auth, each function that opens
// the first auth opens the added one before it builds the App (harnessOpening.statements)
// and hands it to the configurer, and the configurer answers the auth and its identity
// client from the field. A setting the added auth's New refuses nil and the first auth's
// opening does not set is left to the author, and listed. opened is false, and the edit's
// source is as it was, where the command cannot read the harness's openings
// (readHarnessOpenings) or one of the edits finds no place.
func (w *authWiring) openInHarness(h *harness, e *sourceEdit) (opened bool, err error) {
	start := e.src
	openings, err := readHarnessOpenings(h, start, w.srcPath, w.appPath)
	if err != nil || len(openings) == 0 {
		return false, err
	}
	name, pascal := w.au.Name, w.au.Pascal()
	receiver, _, _ := strings.Cut(h.receiver, " ")
	imports := []string{w.authPath, mailImport}
	if w.identityPath != "" {
		imports = append(imports, w.identityPath)
	}
	for _, importPath := range imports {
		if err := e.apply(app.AddImport(e.rel, e.src, importPath)); err != nil {
			return false, err
		}
	}
	mailPackage, err := app.ImportName(e.rel, e.src, mailImport)
	if err != nil {
		return false, err
	}
	accessors := fmt.Sprintf("\n\n// %[1]s returns the %[2]s auth the harness opened beside the %[3]s auth: the App serves\n// the %[4]s outlet's session group from its handlers, as the served application does.\nfunc (%[5]s) %[1]s() *%[2]s.Auth {\n\treturn %[6]s.%[2]s\n}\n", pascal, name, w.src.Name, w.au.Outlet, h.receiver, receiver)
	if w.identityPath != "" {
		accessors += fmt.Sprintf("\n// %[1]sIdentity is the %[2]s auth's identity client, which the auth built when it opened,\n// so the suites drive its generated management as the served application does.\nfunc (%[3]s) %[1]sIdentity() *%[2]sidentity.Client {\n\treturn %[4]s.%[2]s.Identity()\n}\n", pascal, name, h.receiver, receiver)
	}
	edits := []func() ([]byte, error){
		func() ([]byte, error) {
			return app.AddStructField(e.rel, e.src, h.typeName, name+" *"+name+".Auth")
		},
		func() ([]byte, error) {
			return []byte(strings.TrimRight(string(e.src), "\n") + accessors), nil
		},
	}
	for _, o := range openings {
		edits = append(edits,
			func() ([]byte, error) {
				return app.AddStatementsBeforeCall(e.rel, e.src, "", o.function, o.build, o.statements(w, mailPackage))
			},
			func() ([]byte, error) {
				return app.AddLiteralElement(e.rel, e.src, o.function, h.typeName, name+": "+name+"Auth")
			},
		)
	}
	for _, edit := range edits {
		edited, err := edit()
		if errors.Is(err, app.ErrNoAnchor) {
			e.src = start

			return false, nil
		}
		if err := e.apply(edited, err); err != nil {
			return false, err
		}
	}
	if lacking := openings[0].lacks(w.tenantSettings); len(lacking) > 0 {
		e.skipped = append(e.skipped, fmt.Sprintf("%[1]s: the harness opens the %[2]s auth without %[3]s in its Settings, since the %[4]s auth's opening there sets none to copy, and %[2]s.New refuses a nil one, so the suites there stop at %[2]s.New() until the harness sets it", e.rel, name, joinAnd(lacking), w.src.Name))
	}

	return true, nil
}
