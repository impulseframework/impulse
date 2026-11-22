package transition

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/authpkg"
	"github.com/impulseframework/impulse/cmd/impulse/internal/names"
)

// defaultEngineRE finds the statement of the base's UserPermissions that answers a
// session's permissions from the App's one engine.
var defaultEngineRE = regexp.MustCompile(`(?m)^\treturn (\w+\.SessionPermissions\((\w+)\.Context\(\), )(\w+)\.(\w+)\.ForUser, \w+\.\w+\.ForRole, `)

// authWiring is the Go side of an application that an added auth is wired into: what the
// regenerated router and the generated contract ask of the App once an outlet names the
// auth, written where the application is the base's still.
type authWiring struct {
	au     Auth
	src    *authSource
	outlet *app.Outlet
	// authPath is the import path of the auth's package.
	authPath string
	// identityDir is the tree the program generates the auth's management into, and
	// identityPath the import path of the auth's package in it; both are empty where the
	// program generates none.
	identityDir, identityPath string
	// srcPath is the import path of the first auth's package, and appPath of the package
	// that declares the Configurer: a harness that opens the first auth and builds the App
	// over it imports both.
	srcPath, appPath string
	// page is the login page of the browser application the auth's outlet serves, empty
	// where it serves none, and tenantSettings are the settings of the application's
	// tenant record that the auth's New refuses nil.
	page           string
	tenantSettings []string
}

// tenantSettingsOf names the settings an auth's Settings carry over the application's
// tenant record, each of which its New refuses nil: the reader of what a tenant
// declares, the reader of the tenants' directories and what makes a tenant.
func tenantSettingsOf(spec *authpkg.Spec) []string {
	var settings []string
	if spec.TenantPolicy != nil {
		settings = append(settings, "TenantDeclarations")
	}
	if spec.DirectoryAdmission() {
		settings = append(settings, "TenantDirectories")
	}
	if spec.MakesTenants() {
		settings = append(settings, "TenantMaker")
	}

	return settings
}

// wire gives the application's Go side what the regeneration asks of it for the added
// auth, so the application the command leaves builds and serves the auth at its outlet.
// The Configurer hands the App the auth and its identity client, the App holds the
// auth's session handlers, its identity client and its permission engine and answers a
// session's permissions from the engine of the auth the request came through, which the
// regenerated router binds each outlet's requests to (resource.BoundAuth), the outlet's
// accessor returns the added auth's handlers in place of the auth the outlet served
// before, the data level answers the identity client, and every test configurer answers
// both, from the added auth where the harness opens the first auth and serves the
// application (openInHarness). An application whose App is not the base's keeps what it
// has, and the change lists what is left to its author.
func (au Auth) wire(a *app.App, choice *OutletChoice, src *authSource, spec *authpkg.Spec, ch *Change) error {
	if !choice.Site.GeneratedRouter {
		return nil
	}
	module := modulePath(a)
	w := &authWiring{
		au: au, src: src, outlet: &choice.Outlet,
		authPath: path.Join(module, src.AuthDir, au.Name),
		srcPath:  path.Join(module, src.Dir), page: outletLoginPage(&choice.Outlet), tenantSettings: tenantSettingsOf(spec),
	}
	if spec.IdentityDir != "" {
		w.identityDir, w.identityPath = spec.IdentityDir, path.Join(module, spec.IdentityDir, au.Name+"identity")
	}
	rel, data, mode, err := findDeclaringFile(a, configurerType)
	if err != nil {
		return err
	}
	if rel == "" {
		ch.skipf("no file declares a %[1]s interface, so the App is not given the %[2]s auth: the generated router's Handlers requires the %[3]s outlet's session handlers from the %[2]s auth (%[2]s.Auth.Handlers())%[4]s, and the library answers a request the router binds to the %[2]s auth only from that auth's permission engine; the application does not compile until the App holds them", configurerType, au.Name, au.Outlet, w.identityLeft())

		return nil
	}
	previous := ""
	if choice.Outlet.Auth != nil {
		if previous, _, err = handlersField(rel, data, appType, choice.Outlet.Auth.ImportPath); err != nil {
			return err
		}
	}
	w.appPath = path.Join(module, path.Dir(rel))
	e := &sourceEdit{rel: rel, src: data}
	methods, err := methodsOfType(a, path.Dir(rel), appType)
	if err != nil {
		return err
	}
	if err := w.ask(e); err != nil {
		return err
	}
	engine, err := w.hold(e, methods)
	if err != nil {
		return err
	}
	if len(e.gained) > 0 {
		if err := os.WriteFile(a.Abs(rel), e.src, mode); err != nil {
			return errors.Wrap(err, "os.WriteFile()")
		}
		ch.didf("%s: %s, so the App serves the %s auth and answers its sessions from its own store", rel, joinAnd(e.gained), au.Name)
	}
	e.report(ch)
	if err := w.declare(a, e, methods, engine, ch); err != nil {
		return err
	}
	if err := w.serveOutlet(a, choice, path.Dir(rel), previous, ch); err != nil {
		return err
	}
	if err := w.answerOnData(a, ch); err != nil {
		return err
	}

	return w.answerInHarnesses(a, ch)
}

// identityLeft is the words a skipped line says the identity client's accessor in, or
// nothing where the program generates no management of the auth.
func (w *authWiring) identityLeft() string {
	if w.identityPath == "" {
		return ""
	}

	return fmt.Sprintf(", and the generated contract asks the handlers for %sIdentity() *%sidentity.Client (%s.Auth.Identity())", w.au.Pascal(), w.au.Name, w.au.Name)
}

// ask gives the Configurer the two accessors the App is built from: the auth, and its
// identity client where the program generates the auth's management.
func (w *authWiring) ask(e *sourceEdit) error {
	name, pascal := w.au.Name, w.au.Pascal()
	if err := e.apply(app.AddImport(e.rel, e.src, w.authPath)); err != nil {
		return err
	}
	out, err := app.AddInterfaceLine(e.rel, e.src, configurerType, fmt.Sprintf("// %[1]s returns the %[2]s auth, whose session handlers the App serves the %[3]s outlet's\n// session group from and whose permission engine answers for the group's requests.\n%[1]s() *%[2]s.Auth", pascal, name, w.au.Outlet))
	if err := e.gain(out, err, configurerType+" asks for "+pascal+"()", fmt.Sprintf("no %s interface to ask for %s() *%s.Auth on", configurerType, pascal, name)); err != nil {
		return err
	}
	if w.identityPath == "" {
		return nil
	}
	if err := e.apply(app.AddImport(e.rel, e.src, w.identityPath)); err != nil {
		return err
	}
	out, err = app.AddInterfaceLine(e.rel, e.src, configurerType, fmt.Sprintf("// %[1]sIdentity is the %[2]s auth's identity client (%[3]s/%[2]sidentity, generated): what\n// the generated methods that manage the auth's people run over. The data level builds\n// it over the auth's permission engine and its session manager.\n%[1]sIdentity() *%[2]sidentity.Client", pascal, name, w.identityDir))

	return e.gain(out, err, configurerType+" asks for "+pascal+"Identity()", fmt.Sprintf("no %s interface to ask for %sIdentity() *%sidentity.Client on", configurerType, pascal, name))
}

// hold gives the App the auth's session handlers, its identity client and its permission
// engine, set where the App is built, and answers a session's permissions from the
// engine of the auth the request came through. It returns the App's field for the
// default auth's engine, empty where the App holds none the command can read.
func (w *authWiring) hold(e *sourceEdit, methods []string) (engine string, err error) {
	name, pascal := w.au.Name, w.au.Pascal()
	if err := e.apply(app.AddImport(e.rel, e.src, accessImport)); err != nil {
		return "", err
	}
	access, err := app.ImportName(e.rel, e.src, accessImport)
	if err != nil {
		return "", err
	}
	engine, err = app.StructFieldOfValueType(e.rel, e.src, appType, accessImport, "Controller")
	if err != nil {
		return "", err
	}
	out, err := app.AddStructField(e.rel, e.src, appType, fmt.Sprintf("// %[1]sHandlers is the %[1]s auth's session handlers, which the %[2]s outlet's session\n// group is served from.\n%[1]sHandlers *%[1]s.Handlers", name, w.au.Outlet))
	if err := e.gain(out, err, appType+" holds "+name+"Handlers", fmt.Sprintf("no %s struct to hold the %s auth's session handlers in (*%s.Handlers)", appType, name, name)); err != nil {
		return "", err
	}
	out, err = app.AddStructField(e.rel, e.src, appType, fmt.Sprintf("// engines holds the permission engine of every auth beside the default one, by the\n// auth's name, for the requests of a session group bound to that auth.\nengines map[string]%s.Controller", access))
	if err := e.gain(out, err, appType+" holds engines", fmt.Sprintf("no %s struct to hold the %s auth's permission engine in", appType, name)); err != nil {
		return "", err
	}
	if w.identityPath != "" {
		out, err = app.AddStructField(e.rel, e.src, appType, fmt.Sprintf("// %[1]sIdentity is the %[1]s auth's identity client, which the generated handlers of\n// %[2]s read through %[3]sIdentity.\n%[1]sIdentity *%[1]sidentity.Client", name, w.identityDir, pascal))
		if err := e.gain(out, err, appType+" holds "+name+"Identity", fmt.Sprintf("no %s struct to hold the %s auth's identity client in (*%sidentity.Client)", appType, name, name)); err != nil {
			return "", err
		}
	}
	cfg, err := parameterOfType(e.rel, e.src, appConstructor, configurerType)
	if err != nil {
		return "", err
	}
	built := regexp.MustCompile(`(?m)^\s*(\w+) := &` + appType + `\{`).FindSubmatch(e.src)
	left := fmt.Sprintf("%[1]s builds no %[2]s literal from a %[3]s, so the App is not given the %[4]s auth; where it is built, set %[4]sHandlers to the auth's Handlers() and engines[%[4]s.Name] to its Access() when the %[3]s's %[5]s() is not nil%[6]s", appConstructor, appType, configurerType, name, pascal, w.identityLeft())
	if cfg == "" || built == nil {
		e.skipped = append(e.skipped, e.rel+": "+left)

		return engine, nil
	}
	variable := string(built[1])
	out, err = app.AddLiteralElement(e.rel, e.src, appConstructor, appType, fmt.Sprintf("engines: map[string]%s.Controller{}", access))
	if err := e.gain(out, err, appConstructor+" makes engines", left); err != nil {
		return "", err
	}
	if w.identityPath != "" {
		out, err = app.AddLiteralElement(e.rel, e.src, appConstructor, appType, fmt.Sprintf("%sIdentity: %s.%sIdentity()", name, cfg, pascal))
		if err := e.gain(out, err, fmt.Sprintf("%s sets %sIdentity (%s.%sIdentity())", appConstructor, name, cfg, pascal), left); err != nil {
			return "", err
		}
	}
	statements := fmt.Sprintf("if %[1]sAuth := %[2]s.%[3]s(); %[1]sAuth != nil {\n%[4]s.%[1]sHandlers = %[1]sAuth.Handlers()\n%[4]s.engines[%[1]s.Name] = %[1]sAuth.Access()\n}\n", name, cfg, pascal, variable)
	out, err = app.AddStatementsBeforeCall(e.rel, e.src, "", appConstructor, cfg+"."+w.src.Pascal, statements)
	if err == nil && bytes.Index(out, []byte("if "+name+"Auth := ")) < bytes.Index(out, built[0]) {
		err = errors.Wrapf(app.ErrNoAnchor, "%s: %s reads %s.%s() before it builds the %s", e.rel, appConstructor, cfg, w.src.Pascal, appType)
	}
	if err := e.gain(out, err, fmt.Sprintf("%s takes the auth's handlers and its engine from %s.%s()", appConstructor, cfg, pascal), fmt.Sprintf("%[1]s reads the %[2]s auth from %[3]s.%[4]s() in no statement after it builds the %[5]s, so the %[6]s auth's handlers and engine are not set; set %[6]sHandlers to the auth's Handlers() and engines[%[6]s.Name] to its Access() when %[3]s.%[7]s() is not nil", appConstructor, w.src.Name, cfg, w.src.Pascal, appType, name, pascal)); err != nil {
		return "", err
	}

	return engine, w.answerPermissions(e, methods, engine)
}

// answerPermissions makes the base's UserPermissions answer from the engine of the auth
// the request came through, where it answers from the App's one engine and the App has,
// or is about to gain, the method that finds the engine.
func (w *authWiring) answerPermissions(e *sourceEdit, methods []string, engine string) error {
	if engine == "" && !slices.Contains(methods, "engine") {
		return nil
	}
	m := defaultEngineRE.FindSubmatch(e.src)
	if len(m) != 5 || string(m[4]) != engine {
		if !bytes.Contains(e.src, []byte(".engine(")) {
			e.skipped = append(e.skipped, fmt.Sprintf("%s: no UserPermissions answers a session's permissions from the App's one engine as the base's does, so a request of the %s auth may be asked of the default auth's engine, which the library refuses; answer from the engine of the auth the request came through (engine(ctx), over resource.BoundAuth)", e.rel, w.au.Name))
		}

		return nil
	}
	receiver, request := string(m[3]), string(m[2])
	out := defaultEngineRE.ReplaceAll(e.src, []byte("\tengine := "+receiver+".engine("+request+".Context())\n\n\treturn ${1}engine.ForUser, engine.ForRole, "))

	return e.gain(out, nil, "UserPermissions answers from the engine of the auth the request came through", "")
}

// declare writes the methods the App lacks for the auth into a file of its own beside
// the App: the engine of the auth a request came through, which the regenerated router
// binds each outlet's requests to (resource.BoundAuth), and the identity client's
// accessor the generated contract asks for.
func (w *authWiring) declare(a *app.App, e *sourceEdit, methods []string, engine string, ch *Change) error {
	name, pascal, dir := w.au.Name, w.au.Pascal(), path.Dir(e.rel)
	access, err := app.ImportName(e.rel, e.src, accessImport)
	if err != nil {
		return err
	}
	file := &appMethods{}
	switch {
	case slices.Contains(methods, "engine"):
		// The App chooses a request's engine itself.
	case engine != "" && access != "":
		file.imports = append(file.imports, "context", accessImport, resourceImport)
		file.declare("engine", fmt.Sprintf("\n// engine returns the permission engine of the auth the request came through, which\n// the generated router binds each outlet's requests to (resource.BoundAuth). A request\n// bound to no auth gets the default auth's engine.\nfunc (a *%[1]s) engine(ctx context.Context) %[2]s.Controller {\n\tif engine, ok := a.engines[resource.BoundAuth(ctx)]; ok {\n\t\treturn engine\n\t}\n\n\treturn a.%[3]s\n}\n", appType, access, engine))
	default:
		ch.skipf("%s: %s holds no default permission engine the command can read, so engine is not declared; answer a request from the engine of the auth it came through (resource.BoundAuth names it): the library refuses a request of the %s auth asked of another auth's engine", e.rel, appType, name)
	}
	if w.identityPath != "" && !slices.Contains(methods, pascal+"Identity") {
		file.imports = append(file.imports, w.identityPath)
		file.declare(pascal+"Identity", fmt.Sprintf("\n// %[1]sIdentity returns the %[2]s auth's identity client, the generated contract's\n// accessor: what the generated handlers of %[3]s manage the auth's people through.\nfunc (a *%[4]s) %[1]sIdentity() *%[2]sidentity.Client {\n\treturn a.%[2]sIdentity\n}\n", pascal, name, w.identityDir, appType))
	}
	if len(file.declared) == 0 {
		return nil
	}
	rel := path.Join(dir, name+"_auth.go")
	if exists(a, rel) {
		ch.skipf("%s: the file is there already and %s declares no %s, so nothing was written; declare what is missing, which the regenerated router and the generated contract require for the %s auth", rel, appType, joinAnd(file.declared), name)

		return nil
	}
	pkg, err := app.PackageName(e.rel, e.src)
	if err != nil {
		return err
	}
	slices.Sort(file.imports)
	if err := writeGo(a, rel, goFile(pkg, slices.Compact(file.imports), file.body.String())); err != nil {
		return err
	}
	ch.didf("%s: %s declares %s, which the regenerated router and the generated contract require of it for the %s auth", rel, appType, joinAnd(file.declared), name)

	return nil
}

// serveOutlet makes the outlet's accessor on the App return the added auth's session
// handlers, in place of the handlers of the auth the outlet served before: the accessor
// impulse add outlet wrote is written again for the auth. An accessor somebody changed,
// and the default outlet's session surface, which is the handlers the App embeds, are
// left as they are, and the change says what to return.
func (w *authWiring) serveOutlet(a *app.App, choice *OutletChoice, dir, previous string, ch *Change) error {
	name := w.au.Name
	o := Outlet{Name: choice.Outlet.Name}
	file := outletFile(dir, o.Name)
	left := fmt.Sprintf("the %[1]s outlet still serves the session handlers of the auth it served before, so the %[2]s auth's people cannot sign in there; serve the outlet's session group from the %[2]s auth's handlers (the App's %[2]sHandlers)", w.au.Outlet, name)
	if choice.Outlet.Name == app.DefaultOutletName || choice.Outlet.Auth == nil || previous == "" || !exists(a, file) {
		ch.skipf("%s: no accessor of the outlet's session handlers as impulse add outlet writes one, so %s", dir, left)

		return nil
	}
	data, mode, err := readFile(a, file)
	if err != nil {
		return err
	}
	router, _ := routerQualifier(a, choice.Site.Generator, dir)
	before := sessionAccessor(o.pascal(), o.Name, path.Base(choice.Outlet.Auth.ImportPath), previous, router)
	if !bytes.Contains(data, []byte(before)) {
		ch.skipf("%s: %s() is not as impulse add outlet writes it, so %s", file, o.pascal(), left)

		return nil
	}
	after := sessionAccessor(o.pascal(), o.Name, name, name+"Handlers", router)
	if err := os.WriteFile(a.Abs(file), bytes.Replace(data, []byte(before), []byte(after), 1), mode); err != nil {
		return errors.Wrap(err, "os.WriteFile()")
	}
	ch.didf("%s: %s() returns the %s auth's session handlers, so the people of the %s auth sign in at the %s outlet and nobody else's session opens there", file, o.pascal(), name, name, w.au.Outlet)

	return nil
}

// answerOnData gives the data level the accessor of the auth's identity client, beside
// the accessor of the auth: what the Configurer asks for.
func (w *authWiring) answerOnData(a *app.App, ch *Change) error {
	if w.identityPath == "" {
		return nil
	}
	name, pascal := w.au.Name, w.au.Pascal()
	rel, _, _, err := findDeclaringFile(a, "DataConfiguration")
	if err != nil {
		return err
	}
	file := path.Join(path.Dir(rel), name+".go")
	if rel == "" || !exists(a, file) {
		ch.skipf("no data level answers %[1]sIdentity() *%[2]sidentity.Client, which the %[3]s now asks for; return the %[2]s auth's identity client (%[2]s.Auth.Identity()) where the auth is held", pascal, name, configurerType)

		return nil
	}
	data, mode, err := readFile(a, file)
	if err != nil {
		return err
	}
	if bytes.Contains(data, []byte(") "+pascal+"Identity() ")) {
		return nil
	}
	accessor := fmt.Sprintf("\n// %[1]sIdentity returns the %[2]s auth's identity client, which the auth built when it\n// opened: what the generated methods that manage the auth's people run over.\nfunc (c *DataConfiguration) %[1]sIdentity() *%[2]sidentity.Client {\n\treturn c.%[2]s.Identity()\n}\n", pascal, name)
	text, beside := string(data), fmt.Sprintf("func (c *DataConfiguration) %s() *%s.Auth {\n\treturn c.%s\n}\n", pascal, name, name)
	if at := strings.Index(text, beside); at >= 0 {
		text = text[:at+len(beside)] + accessor + text[at+len(beside):]
	} else {
		text = strings.TrimRight(text, "\n") + "\n" + accessor
	}
	out, err := app.AddImport(file, []byte(text), w.identityPath)
	if err != nil {
		return err
	}
	if err := os.WriteFile(a.Abs(file), out, mode); err != nil {
		return errors.Wrap(err, "os.WriteFile()")
	}
	ch.didf("%s: the data level answers %sIdentity(), the %s auth's identity client", file, pascal, name)

	return nil
}

// answerInHarnesses gives every test configurer the two accessors the Configurer gained,
// so every test package compiles and its suites run. A harness whose configurer holds the
// first auth serves what the application serves, the added auth's outlet with it, and the
// router asks the App for that outlet's session handlers as it mounts the outlet: there
// the command opens the added auth beside the first one (openInHarness), and the
// configurer answers the auth and its identity client from it. A configurer that holds no
// auth answers none (answerNone). A harness that holds the first auth where the command
// cannot read how it is opened is left answering none too, and a Skipped line says what
// its suites need.
func (w *authWiring) answerInHarnesses(a *app.App, ch *Change) error {
	name, pascal := w.au.Name, w.au.Pascal()
	harnesses, err := starterWiring{}.harnessFiles(a)
	if err != nil {
		return err
	}
	accessors := pascal + "()"
	if w.identityPath != "" {
		accessors += " and " + pascal + "Identity()"
	}
	var answered []string
	for i := range harnesses {
		h := &harnesses[i]
		if slices.Contains(h.methods, pascal) {
			continue
		}
		src, mode, err := readFile(a, h.file)
		if err != nil {
			return err
		}
		e := &sourceEdit{rel: h.file, src: src}
		held, err := app.StructFieldOfType(h.file, src, h.typeName, w.srcPath, "Auth")
		if err != nil {
			return err
		}
		opened := false
		if held != "" {
			if opened, err = w.openInHarness(h, e); err != nil {
				return err
			}
		}
		if !opened {
			if err := w.answerNone(h, e); err != nil {
				return err
			}
		}
		if err := os.WriteFile(a.Abs(h.file), e.src, mode); err != nil {
			return errors.Wrap(err, "os.WriteFile()")
		}
		switch {
		case opened:
			links := ""
			if w.page != "" {
				links = fmt.Sprintf(", and its links open the %s outlet's login page, %s", w.au.Outlet, w.page)
			}
			ch.didf("%s: the harness opens the %s auth beside the %s auth, over the same database and with its own settings (its mail goes to a recorder of its own, %sSent%s), and %s answers %s from it, so the suites serve the %s outlet's session group as the application does", h.file, name, w.src.Name, name, links, h.typeName, accessors, w.au.Outlet)
		case held != "":
			ch.skipf("%[1]s (%[2]s): the configurer holds the %[3]s auth, and the command does not read how the harness opens it: it reads a function that opens the auth with %[3]s.New(..., &%[3]s.Settings{...}) and then builds the App from a %[2]s literal, where every function that builds the App from one does so. It opened no %[4]s auth there and the configurer answers none. The router asks the App for the %[5]s outlet's session handlers as it mounts the outlet, so the suites there that build the router stop with a nil pointer until the harness opens the %[4]s auth as the %[3]s auth is opened, with its own Mail and MailLoginURL, and answers %[6]s from it", h.file, h.typeName, w.src.Name, name, w.au.Outlet, accessors)
		default:
			answered = append(answered, fmt.Sprintf("%s (%s)", h.file, h.typeName))
		}
		e.report(ch)
	}
	if len(answered) > 0 {
		ch.didf("the test configurers that hold no auth answer %s() with none, and its identity client as they build the %s auth's where they build one without an auth: %s", pascal, w.src.Name, strings.Join(answered, "; "))
	}

	return nil
}

// answerNone gives a test configurer the two accessors where its harness opens no added
// auth: the auth is nil, and the identity client is built as the harness builds the
// first auth's where it builds one without an auth, and is nil where the harness reads
// it from an auth.
func (w *authWiring) answerNone(h *harness, e *sourceEdit) error {
	name, pascal := w.au.Name, w.au.Pascal()
	built := regexp.MustCompile(`(?s)\nfunc \(` + regexp.QuoteMeta(h.receiver) + `\) ` + w.src.Pascal + `Identity\(\) [^\n]*\{\n.*?\n\}\n`).Find(e.src)
	copied := w.identityPath != "" && bytes.Contains(built, []byte("NewClient("))
	if copied {
		if err := e.apply(app.CopyMethod(h.file, e.src, h.typeName, w.src.Pascal+"Identity", w.renamed)); err != nil {
			return err
		}
	}
	text := strings.TrimRight(string(e.src), "\n") + fmt.Sprintf("\n\n// %[1]s answers no %[2]s auth: the harness opens none. The router asks the App for the\n// %[3]s outlet's session handlers as it mounts the outlet, so a suite that builds the\n// router needs the harness to open the %[2]s auth and answer it here.\nfunc (%[4]s) %[1]s() *%[2]s.Auth {\n\treturn nil\n}\n", pascal, name, w.au.Outlet, h.receiver)
	if w.identityPath != "" && !copied {
		text += fmt.Sprintf("\n// %[1]sIdentity answers no identity client: the harness opens no %[2]s auth to build one.\nfunc (%[3]s) %[1]sIdentity() *%[2]sidentity.Client {\n\treturn nil\n}\n", pascal, name, h.receiver)
	}
	if err := e.apply(app.AddImport(h.file, []byte(text), w.authPath)); err != nil {
		return err
	}
	if w.identityPath == "" {
		return nil
	}

	return e.apply(app.AddImport(h.file, e.src, w.identityPath))
}

// renamed is a method written for the first auth as the added auth's copy reads.
func (w *authWiring) renamed(text string) string {
	return names.Rename(text, w.src.Name, w.au.Name)
}
