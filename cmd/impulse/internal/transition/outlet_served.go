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
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"

	"github.com/impulseframework/impulse/app"
)

// The Go half of add outlet under the generated router. The regenerated router's Handlers
// asks the App for more the moment the program declares an outlet: a session outlet's
// session handlers and the pair serving its browser application, a machine outlet's token
// check. An application that did not answer stopped building, and with it every check it
// has, so the command writes the answer where the application has the base's anchors: the
// Configurer and the App, the site's configuration, the test configurers, and the auth
// file. An anchor that is not there is left to the author, and the brief says what to
// write in its place.

// consoleDistAccessor is the accessor the base's configuration serves the console's
// bundle directory through, which an outlet's is copied from.
const consoleDistAccessor = "ConsoleDist"

// pascal is the outlet's name as the generated router's identifiers open with it.
func (o Outlet) pascal() string {
	return strings.ToUpper(o.Name[:1]) + o.Name[1:]
}

// outletFile is the file of the App's package that declares what the generated router
// asks of the App for an outlet. The outlet's name leads, so the Go tool reads no build
// constraint from a name that is an operating system's or an architecture's.
func outletFile(dir, name string) string {
	return path.Join(dir, strings.ToLower(name)+"_outlet.go")
}

// gain takes an edit's result: it records what the edit gained the file where it changed
// it, and what is left to the author where the file has no anchor for it.
func (e *sourceEdit) gain(src []byte, err error, gained, left string) error {
	switch {
	case errors.Is(err, app.ErrNoAnchor):
		e.skipped = append(e.skipped, e.rel+": "+left)

		return nil
	case err != nil:
		return err
	case !bytes.Equal(src, e.src):
		e.gained = append(e.gained, gained)
	}
	e.src = src

	return nil
}

// serve writes what the generated router's Handlers asks of the application for the
// outlet, after go generate wrote the router that asks: the served browser application of
// a session outlet, or the registration and the token check of a machine outlet. Under a
// hand-written router the App is asked nothing, and nothing is written.
func (o Outlet) serve(a *app.App, site *app.Site, auth *app.OutletAuth, ch *Change) error {
	if !site.GeneratedRouter || auth == nil {
		return nil
	}
	if o.Sessions {
		return o.serveBrowserApp(a, site, auth, ch)
	}

	return o.issueTokens(a, site, auth, ch)
}

// serveBrowserApp wires a session outlet's browser application into the Go side: the
// Configurer asks for the bundle's directory and the App serves it under the outlet's
// mount path, a new file declares the three methods the generated Handlers requires, the
// site's configuration reads the directory from the environment, the environment template
// names the variable, and every test configurer answers it.
func (o Outlet) serveBrowserApp(a *app.App, site *app.Site, auth *app.OutletAuth, ch *Change) error {
	pascal, dist := o.pascal(), o.distDir(a, site.Generator)
	rel, src, mode, err := findDeclaringFile(a, configurerType)
	if err != nil {
		return err
	}
	if rel == "" {
		ch.skipf("no file declares a %s interface, so the App does not serve the %s outlet yet: the generated router's Handlers requires %s() returning the %s auth's session handlers (router.%sSession), and %sDeepLink and %sAssets serving the outlet's built bundle from a directory the configuration names (resource.NewBrowserApp(<directory>, %q), with %s as the directory by default)", configurerType, o.Name, pascal, o.Auth, pascal, pascal, pascal, "/"+o.Name, dist)

		return nil
	}
	edit := &sourceEdit{rel: rel, src: src}
	field, err := o.holdBundle(edit)
	if err != nil {
		return err
	}
	if len(edit.gained) > 0 {
		if err := os.WriteFile(a.Abs(rel), edit.src, mode); err != nil {
			return errors.Wrap(err, "os.WriteFile()")
		}
		ch.didf("%s: %s, so the App serves the %s's built bundle under /%s", rel, joinAnd(edit.gained), o.Name, o.Name)
	}
	edit.report(ch)
	if err := o.declareSessionMethods(a, site, auth, edit, field, ch); err != nil {
		return err
	}
	if err := o.readDist(a, dist, ch); err != nil {
		return err
	}
	if err := o.documentDist(a, dist, ch); err != nil {
		return err
	}

	return o.answerDistInHarnesses(a, ch)
}

// distDir is the directory the outlet's built bundle is served from by default: the
// project's output in the workspace the console's project is in, as the console's is.
func (o Outlet) distDir(a *app.App, g *app.Generator) string {
	webDir := "web"
	if target := defaultTarget(g); target != nil {
		if w, ok := a.WebAppFor(target.Dir); ok {
			webDir = w.Dir
		}
	}

	return path.Join(webDir, "dist", o.Name)
}

// distVariable is the environment variable the outlet's bundle directory is read from.
func (o Outlet) distVariable() string {
	return "APP_" + strings.ToUpper(o.Name) + "_DIST"
}

// holdBundle gives the App the outlet's served browser application: the Configurer asks
// the configuration for the bundle's directory, the App carries the resource package's
// served browser app, and New builds it over the directory under the outlet's mount
// path. It returns the App's field.
func (o Outlet) holdBundle(e *sourceEdit) (string, error) {
	pascal, field, mount := o.pascal(), o.Name+"App", "/"+o.Name
	out, err := app.AddInterfaceLine(e.rel, e.src, configurerType, pascal+"Dist() string")
	if err := e.gain(out, err, configurerType+" asks for "+pascal+"Dist()", "no "+configurerType+" interface to ask for "+pascal+"Dist() string on"); err != nil {
		return "", err
	}
	if err := e.apply(app.AddImport(e.rel, e.src, resourceImportPath)); err != nil {
		return "", err
	}
	resource, err := app.ImportName(e.rel, e.src, resourceImportPath)
	if err != nil {
		return "", err
	}
	browserApp := fmt.Sprintf("%s.NewBrowserApp(<the directory>, %q)", resource, mount)
	out, err = app.AddStructField(e.rel, e.src, appType, fmt.Sprintf("// %[1]s serves the %[2]s's built bundle under %[3]s, as the console's is served: the\n// resource package's served browser app holds the deep-link rewrite, the prefix strip,\n// and the two cache classes a service worker needs.\n%[1]s *%[4]s.BrowserApp", field, o.Name, mount, resource))
	if err := e.gain(out, err, appType+" holds "+field, fmt.Sprintf("no %s struct to hold the %s's served browser application in; hold a *%s.BrowserApp built as %s", appType, o.Name, resource, browserApp)); err != nil {
		return "", err
	}
	cfg, err := parameterOfType(e.rel, e.src, appConstructor, configurerType)
	if err != nil {
		return "", err
	}
	left := fmt.Sprintf("%s builds no %s literal from a %s, so the %s field is not set; set it to %s, the directory being the configuration's %sDist()", appConstructor, appType, configurerType, field, browserApp, pascal)
	if cfg == "" {
		e.skipped = append(e.skipped, e.rel+": "+left)

		return field, nil
	}
	out, err = app.AddLiteralElement(e.rel, e.src, appConstructor, appType, fmt.Sprintf("%s: %s.NewBrowserApp(%s.%sDist(), %q)", field, resource, cfg, pascal, mount))
	if err := e.gain(out, err, fmt.Sprintf("%s builds it (%s.NewBrowserApp(%s.%sDist(), %q))", appConstructor, resource, cfg, pascal, mount), left); err != nil {
		return "", err
	}

	return field, nil
}

// declareSessionMethods writes the three methods the generated Handlers requires of the
// App for a session outlet, where the App declares none of the name already: the outlet's
// session handlers, which are the handlers of the auth the outlet binds to, and the pair
// serving its browser application.
func (o Outlet) declareSessionMethods(a *app.App, site *app.Site, auth *app.OutletAuth, e *sourceEdit, field string, ch *Change) error {
	dir, pascal := path.Dir(e.rel), o.pascal()
	methods, err := methodsOfType(a, dir, appType)
	if err != nil {
		return err
	}
	holder, _, err := handlersField(e.rel, e.src, appType, auth.ImportPath)
	if err != nil {
		return err
	}
	file := &appMethods{}
	router, routerPath := routerQualifier(a, site.Generator, dir)
	switch {
	case slices.Contains(methods, pascal):
	case holder == "":
		ch.skipf("%s: %s holds no *%s.Handlers, so %s() is not declared; return the %s auth's session handlers from it (%s%sSession), which the generated router's Handlers requires", e.rel, appType, path.Base(auth.ImportPath), pascal, o.Auth, router, pascal)
	default:
		file.imports = append(file.imports, routerPath...)
		file.declare(pascal+"()", sessionAccessor(pascal, o.Name, o.Auth, holder, router))
	}
	if !slices.Contains(methods, pascal+"DeepLink") {
		file.imports = append(file.imports, "net/http")
		file.declare(pascal+"DeepLink", fmt.Sprintf("\n// %[1]sDeepLink rewrites the %[2]s's Angular routes under /%[2]s/ to its entry document, so\n// a bookmarked frontend route loads the single-page application: a request whose last\n// segment has no extension is served index.html, and a path with an extension is a file\n// or a 404.\nfunc (a *%[4]s) %[1]sDeepLink(next http.Handler) http.Handler {\n\treturn a.%[3]s.DeepLink(next)\n}\n", pascal, o.Name, field, appType))
	}
	if !slices.Contains(methods, pascal+"Assets") {
		file.imports = append(file.imports, "net/http")
		file.declare(pascal+"Assets", fmt.Sprintf("\n// %[1]sAssets serves the %[2]s's built Angular application: a hashed file is immutable for\n// a year, every other file (the entry document, the worker files, the manifest, the\n// icons) is revalidated on each use, which is what the service worker needs under any\n// cache.\nfunc (a *%[4]s) %[1]sAssets() http.HandlerFunc {\n\treturn a.%[3]s.Assets()\n}\n", pascal, o.Name, field, appType))
	}

	return o.writeAppMethods(a, e, file, ch)
}

// sessionAccessor is the App's accessor of a session outlet's handlers as the command
// writes it: the handlers of the auth the outlet binds to, read from the App's field
// that holds them. impulse add outlet writes it for the auth the outlet is laid in with,
// and impulse add auth writes it again for the auth it binds the outlet to.
func sessionAccessor(pascal, outlet, auth, holder, router string) string {
	return fmt.Sprintf("\n// %[1]s returns the %[2]s outlet's session handlers (%[5]s%[1]sSession): the %[3]s auth's,\n// the auth the outlet binds to, whose people sign in under the outlet's prefix.\nfunc (a *%[6]s) %[1]s() %[5]s%[1]sSession {\n\tif a.%[4]s == nil {\n\t\treturn nil\n\t}\n\n\treturn a.%[4]s\n}\n", pascal, outlet, auth, holder, router, appType)
}

// appMethods collects the methods an outlet asks of the App that the App lacks, for the
// file that declares them.
type appMethods struct {
	imports  []string
	body     strings.Builder
	declared []string
}

// declare adds a method to the file, under the name the change reports it by.
func (m *appMethods) declare(name, text string) {
	m.declared = append(m.declared, name)
	m.body.WriteString(text)
}

// routerQualifier is how a file of the App's package names the generated router's types:
// by the router package's name, with the import the file then needs, or by nothing where
// the router is generated into the App's own package.
func routerQualifier(a *app.App, g *app.Generator, dir string) (qualifier string, imports []string) {
	routes := g.RoutesDir()
	if routes == dir {
		return "", nil
	}

	return path.Base(routes) + ".", []string{modulePath(a) + "/" + routes}
}

// writeAppMethods writes the collected methods into the outlet's file of the App's
// package. A file of that name that is there already is left as it is, and the brief names
// the methods the App still lacks.
func (o Outlet) writeAppMethods(a *app.App, e *sourceEdit, m *appMethods, ch *Change) error {
	if len(m.declared) == 0 {
		return nil
	}
	file := outletFile(path.Dir(e.rel), o.Name)
	if exists(a, file) {
		ch.skipf("%s: the file is there already and %s declares no %s, so nothing was written; declare what is missing, which the generated router's Handlers requires for the %s outlet", file, appType, strings.Join(m.declared, ", no "), o.Name)

		return nil
	}
	pkg, err := app.PackageName(e.rel, e.src)
	if err != nil {
		return err
	}
	slices.Sort(m.imports)
	if err := writeGo(a, file, goFile(pkg, slices.Compact(m.imports), m.body.String())); err != nil {
		return err
	}
	ch.didf("%s: %s declares %s, which the generated router's Handlers requires of it for the %s outlet", file, appType, joinAnd(m.declared), o.Name)

	return nil
}

// readDist gives the site's configuration the outlet's bundle directory: a field of the
// site's environment struct, read from the outlet's variable with the project's output
// as its default, and the accessor the Configurer asks for, copied from the console's.
func (o Outlet) readDist(a *app.App, dist string, ch *Change) error {
	pascal, variable := o.pascal(), o.distVariable()
	rel, src, mode, err := findDeclaringFile(a, siteConfigType)
	if err != nil {
		return err
	}
	if rel == "" {
		ch.skipf("no file declares a %s struct, so the configuration does not answer %sDist() yet: return the directory the %s's built bundle is served from, read from %s and %s by default", siteConfigType, pascal, o.Name, variable, dist)

		return nil
	}
	edit := &sourceEdit{rel: rel, src: src}
	field := fmt.Sprintf("// %[1]sDist is the directory holding the %[2]s's built Angular bundle.\n%[1]sDist string `env:\"%[3]s,default=%[4]s\"`", pascal, o.Name, variable, dist)
	leftField := fmt.Sprintf("the site's environment struct is not built as the base builds it (env := &<type>{}), so %s is not read; read the directory the %s's built bundle is served from, %s by default", variable, o.Name, dist)
	if m := envStructRE.FindSubmatch(src); m != nil {
		out, err := app.AddStructField(rel, edit.src, string(m[1]), field)
		if err := edit.gain(out, err, fmt.Sprintf("%s reads %s (%s by default)", string(m[1]), variable, dist), leftField); err != nil {
			return err
		}
	} else {
		edit.skipped = append(edit.skipped, rel+": "+leftField)
	}
	declared, err := app.MethodNames(rel, edit.src, siteConfigType)
	if err != nil {
		return err
	}
	if !slices.Contains(declared, pascal+"Dist") {
		out, err := app.CopyMethod(rel, edit.src, siteConfigType, consoleDistAccessor, strings.NewReplacer(consoleDistAccessor, pascal+"Dist", "the console's", "the "+o.Name+"'s").Replace)
		if err := edit.gain(out, err, fmt.Sprintf("%s answers %sDist()", siteConfigType, pascal), fmt.Sprintf("%s declares no %s() to copy, so %sDist() is not declared; return the directory read from %s, which the app's %s asks for", siteConfigType, consoleDistAccessor, pascal, variable, configurerType)); err != nil {
			return err
		}
	}
	if len(edit.gained) > 0 {
		if err := os.WriteFile(a.Abs(rel), edit.src, mode); err != nil {
			return errors.Wrap(err, "os.WriteFile()")
		}
		ch.didf("%s: %s", rel, joinAnd(edit.gained))
	}
	edit.report(ch)

	return nil
}

// distVariableRE matches an environment template's line naming a bundle directory, set or
// commented out, as the base's names the console's.
var distVariableRE = regexp.MustCompile(`(?m)^.*\bAPP_[A-Z0-9_]+_DIST=.*\n`)

// documentDist names the outlet's bundle directory in the environment template, after the
// line that names the console's and written as that line is, or at the end.
func (o Outlet) documentDist(a *app.App, dist string, ch *Change) error {
	variable := o.distVariable()
	if a.EnvTemplate == "" {
		ch.skipf("no environment template (.envrc.template, .env.template, .env.example) to name %s in; the variable is the directory the %s's built bundle is served from, %s by default", variable, o.Name, dist)

		return nil
	}
	data, mode, err := readFile(a, a.EnvTemplate)
	if err != nil {
		return err
	}
	text := string(data)
	if strings.Contains(text, variable+"=") {
		return nil
	}
	if all := distVariableRE.FindAllStringIndex(text, -1); len(all) > 0 {
		last := all[len(all)-1]
		// The new line opens as the last one does, commented out or set.
		opening, _, _ := strings.Cut(text[last[0]:last[1]], "APP_")
		text = text[:last[1]] + opening + variable + "=" + dist + "\n" + text[last[1]:]
	} else {
		text = strings.TrimRight(text, "\n") + "\n\n# " + variable + " is the directory the " + o.Name + "'s built bundle is served from.\n# export " + variable + "=" + dist + "\n"
	}
	if err := os.WriteFile(a.Abs(a.EnvTemplate), []byte(text), mode); err != nil {
		return errors.Wrap(err, "os.WriteFile()")
	}
	ch.didf("%s: names %s, the directory the %s's built bundle is served from, with its default %s", a.EnvTemplate, variable, o.Name, dist)

	return nil
}

// answerDistInHarnesses gives every test configurer the accessor the Configurer gained,
// answering no directory as it answers the console's, since no suite serves a built
// bundle: beside the configurer's other empty directory where it has one, written as that
// one is.
func (o Outlet) answerDistInHarnesses(a *app.App, ch *Change) error {
	harnesses, err := starterWiring{}.harnessFiles(a)
	if err != nil {
		return err
	}
	accessor := o.pascal() + "Dist"
	var edited []string
	for _, h := range harnesses {
		if slices.Contains(h.methods, accessor) {
			continue
		}
		src, mode, err := readFile(a, h.file)
		if err != nil {
			return err
		}
		text := string(src)
		beside := regexp.MustCompile(`(?m)^func \(` + regexp.QuoteMeta(h.receiver) + `\) \w+Dist\(\) string \{ return "" \}\n`)
		if all := beside.FindAllStringIndex(text, -1); len(all) > 0 {
			at := all[len(all)-1][1]
			text = text[:at] + "\nfunc (" + h.receiver + ") " + accessor + "() string { return \"\" }\n" + text[at:]
		} else {
			text = strings.TrimRight(text, "\n") + "\n\n// " + accessor + " is empty: no suite serves a built bundle.\nfunc (" + h.receiver + ") " + accessor + "() string {\n\treturn \"\"\n}\n"
		}
		if err := os.WriteFile(a.Abs(h.file), []byte(text), mode); err != nil {
			return errors.Wrap(err, "os.WriteFile()")
		}
		edited = append(edited, fmt.Sprintf("%s (%s)", h.file, h.typeName))
	}
	if len(edited) > 0 {
		ch.didf("the test configurers answer %s() with no directory, as they answer the console's, since no suite serves a built bundle: %s", accessor, strings.Join(edited, "; "))
	}

	return nil
}

// handlersField names the field of the struct that holds an auth's handlers, a
// *<auth>.Handlers: Handlers where the struct embeds them, as the base's App does, or the
// field's own name. It is empty where the struct holds none. embedded says the struct
// embeds them, so their methods are the struct's own: a field the author named Handlers
// holds them under the same name without that.
func handlersField(rel string, src []byte, structName, authPath string) (name string, embedded bool, err error) {
	local, err := app.ImportName(rel, src, authPath)
	if err != nil || local == "" {
		return "", false, err
	}
	f, err := parser.ParseFile(token.NewFileSet(), rel, src, parser.SkipObjectResolution)
	if err != nil {
		return "", false, errors.Wrap(err, "parser.ParseFile()")
	}
	var found string
	ast.Inspect(f, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name.Name != structName {
			return found == ""
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return false
		}
		for _, field := range st.Fields.List {
			star, ok := field.Type.(*ast.StarExpr)
			if !ok {
				continue
			}
			sel, ok := star.X.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Handlers" {
				continue
			}
			if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != local {
				continue
			}
			found, embedded = "Handlers", true
			if len(field.Names) > 0 {
				found, embedded = field.Names[0].Name, false
			}

			break
		}

		return false
	})

	return found, embedded, nil
}

// parameterOfType names the parameter of the function that has the type, itself or
// through a pointer. It is empty where the function takes none or does not name it.
func parameterOfType(rel string, src []byte, funcName, typeName string) (string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), rel, src, parser.SkipObjectResolution)
	if err != nil {
		return "", errors.Wrap(err, "parser.ParseFile()")
	}
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Recv != nil || fd.Name.Name != funcName {
			continue
		}
		for _, p := range fd.Type.Params.List {
			if receiverTypeName(p.Type) == typeName && len(p.Names) > 0 {
				return p.Names[0].Name, nil
			}
		}
	}

	return "", nil
}

// The anchors of an auth file as the tool renders it (authpkg's template), which a
// machine outlet's registration goes by, as a sign-in method's goes by New's last
// statement. A file that has lost one is left there, and the brief says what to write.
var (
	// tableNamesEndRE ends at the last of the names the auth file's const block gives
	// the Auth: the trusted devices' table, and the machine outlets declared after it.
	tableNamesEndRE = regexp.MustCompile(`(?m)^\ttrustedDevicesTable\s*=\s*"[^"\n]*"\n(?:\n(?:\t//.*\n)*\t\w+Outlet\s*=\s*"[^"\n]*"\n)*`)
	// mailLoginFieldRE matches the setting the device page is declared after.
	mailLoginFieldRE = regexp.MustCompile(`(?m)^\tMailLoginURL\s+string\n`)
	// devicePageFieldRE matches the device page among the settings.
	devicePageFieldRE = regexp.MustCompile(`(?m)^\tDevicePage\s+string\b`)
	// handlersTypeRE ends after the Handlers struct and the token checks declared on it.
	handlersTypeRE = regexp.MustCompile(`(?m)^type Handlers struct \{\n(?:[\t ].*\n|\n)*\}\n(?:\n(?://.*\n)*func \(h \*Handlers\) \w+Tokens\(\) \*session\.MachineOutlet \{\n\treturn h\.\w+\n\}\n)*`)
	// newAuthEndRE ends with the parenthesis closing the Auth's construction, whose
	// options are written one to a line.
	newAuthEndRE = regexp.MustCompile(`(?s)session\.NewAuth\[.*?\n\t\)\n`)
	// registrationsRE ends after the last registration New makes on the Auth, and
	// captures the Auth's variable.
	registrationsRE = regexp.MustCompile(`(?m)^\thandlers := &Handlers\{Session: (\w+)\}\n(?:(?:\t//.*\n)*\thandlers\.\w+(?:, err)? = .*\n(?:\tif err != nil \{\n\t\treturn nil, err\n\t\}\n)?)*`)
)

// The auth file's text for a machine outlet, as authpkg's template renders it: the
// outlet's name (1) in Pascal case and (2) as the program declares it, and (3) the Auth's
// variable.
const (
	outletNameText     = "\n\t// %[1]sOutlet is a machine outlet whose tokens the auth issues: the generator\n\t// program declares it with Tokens of this package, so the generated router composes\n\t// the auth's token check in front of the outlet's routes, mounts the device flow's\n\t// endpoints under it, and serves the token routes on the auth's session outlet. A\n\t// request there carries a bearer token the auth issued: an application token a person\n\t// issued, or one a command-line client obtained with a person's approval. New\n\t// registers the outlet on the Auth before the first request, as it registers the\n\t// sign-in methods.\n\t%[1]sOutlet = %[2]q\n"
	devicePageText     = "// DevicePage is the console page where a signed-in person approves a command-line\n// client's request to act as them, which a device authorization tells the client\n// (verification_uri): a path in the application, the mount path of the surface that\n// binds to this auth joined with /device (\"/console/device\"), where the console\n// mounts the library's device approval page.\nDevicePage string"
	checkMethodText    = "\n// %[1]sTokens returns the %[2]s outlet's token check (router.%[1]sTokens): the\n// machine outlet registered on the Auth under %[1]sOutlet, whose ValidateToken binds\n// each request under the outlet's prefix to the person its bearer token acts for.\nfunc (h *Handlers) %[1]sTokens() *session.MachineOutlet {\n\treturn h.%[2]s\n}\n"
	registrationText   = "\t// The %[2]s outlet, registered before the first request as the methods are: its\n\t// token check is what the generated router composes in front of the outlet's routes.\n\t// Registration closes with the first use of the Auth, so it happens here rather than\n\t// where the App is built, which a bootstrap or a test harness may reach after\n\t// creating users through the API.\n\thandlers.%[2]s = session.RegisterMachineOutlet(%[3]s, %[1]sOutlet)\n"
	verificationOption = "\t\tsession.WithDeviceVerificationURL(settings.DevicePage),\n"
	handlersDocEnd     = "// (in the method's own file).\n"
	handlersDocChecks  = "// (in the method's own file), and the token check of each machine outlet whose tokens\n// the auth issues.\n"
)

// issueTokens wires a machine outlet into the application: the auth whose tokens the
// outlet carries registers it on its Auth and names the device page, every construction
// of the auth's settings sets that page, the console mounts it, and a new file of the
// App's package declares the token check the generated Handlers requires.
func (o Outlet) issueTokens(a *app.App, site *app.Site, auth *app.OutletAuth, ch *Change) error {
	pkg, file := authFileAt(a, auth.ImportPath)
	if file == "" {
		ch.skipf("no file of the %s auth constructs its Auth (session.NewAuth), so the %s outlet is not registered on it: register it once, before the first request (session.RegisterMachineOutlet over the Auth, under the outlet's name %q), pass the device page to the construction (session.WithDeviceVerificationURL), and return the registered outlet from the App's %s(), which the generated router's Handlers requires", o.Auth, o.Name, o.Name, o.pascal())

		return nil
	}
	if err := o.registerOnAuth(a, file, ch); err != nil {
		return err
	}
	if err := o.nameDevicePage(a, site, pkg, ch); err != nil {
		return err
	}
	if err := o.mountDevicePage(a, site, pkg, ch); err != nil {
		return err
	}

	return o.declareTokenCheck(a, site, auth, ch)
}

// authFileAt finds the auth package at an import path and the file that constructs its
// Auth, which is empty where the scan read none.
func authFileAt(a *app.App, importPath string) (pkg *app.AuthPackage, file string) {
	for i := range a.AuthPackages {
		if a.AuthPackages[i].Path != importPath {
			continue
		}
		pkg = &a.AuthPackages[i]
		for j := range a.Auths {
			if path.Dir(a.Auths[j].File) == pkg.Dir {
				return pkg, a.Auths[j].File
			}
		}

		return pkg, ""
	}

	return nil, ""
}

// holderFree refuses a machine outlet whose name the auth's Handlers holds something else
// under: the outlet's token check is held in a field of the outlet's name, beside the
// sign-in methods' handles, so an outlet named as a method's field is cannot be held.
func (o Outlet) holderFree(a *app.App, auth *app.OutletAuth) error {
	_, file := authFileAt(a, auth.ImportPath)
	if file == "" {
		return nil
	}
	src, _, err := readFile(a, file)
	if err != nil {
		return err
	}
	if bytes.Contains(src, []byte(") "+o.pascal()+"Tokens() ")) {
		return nil
	}
	with, err := app.AddStructField(file, src, "Handlers", o.Name+" *session.MachineOutlet")
	switch {
	case errors.Is(err, app.ErrNoAnchor):
		// A file without the struct has nothing to clash with: it is the author's, and
		// the change says what to write in it.
		return nil
	case err != nil:
		return err
	case !bytes.Equal(with, src):
		return nil
	}

	return errors.Newf("outlet name %q: %s holds something else under that name in its Handlers (a sign-in method's handle), where the outlet's token check would be held; name the outlet otherwise", o.Name, file)
}

// registerOnAuth registers the outlet in the auth file as the tool renders one for an
// auth that issues a machine outlet's tokens: the outlet's name beside the Auth's table
// names, the device page among the settings and passed to the Auth's construction, the
// outlet's handle on Handlers with the method returning it, and the registration in New
// after the sign-in methods'.
func (o Outlet) registerOnAuth(a *app.App, rel string, ch *Change) error {
	src, mode, err := readFile(a, rel)
	if err != nil {
		return err
	}
	e := &sourceEdit{rel: rel, src: src}
	if err := o.declareOnAuth(e); err != nil {
		return err
	}
	if err := o.holdOnAuth(e); err != nil {
		return err
	}
	if len(e.gained) > 0 {
		if err := os.WriteFile(a.Abs(rel), e.src, mode); err != nil {
			return errors.Wrap(err, "os.WriteFile()")
		}
		ch.didf("%s: the %s auth issues the %s outlet's tokens: the file %s", rel, o.Auth, o.Name, joinAnd(e.gained))
	}
	e.report(ch)

	return nil
}

// declareOnAuth gives the auth file what the outlet is declared by: its name beside the
// Auth's table names, and the device page, a setting New passes to the Auth's
// construction as the device verification path.
func (o Outlet) declareOnAuth(e *sourceEdit) error {
	pascal := o.pascal()
	if !regexp.MustCompile(`(?m)^\t?(?:const )?` + pascal + `Outlet\s*=`).Match(e.src) {
		out, err := insertAfter(e.rel, e.src, tableNamesEndRE, 0, fmt.Sprintf(outletNameText, pascal, o.Name))
		if err := e.gain(out, err, "names the outlet ("+pascal+"Outlet)", fmt.Sprintf("the const block does not end with the Auth's table names as the tool writes them, so the outlet's name is not declared; declare %sOutlet = %q", pascal, o.Name)); err != nil {
			return err
		}
	}
	if !devicePageFieldRE.Match(e.src) {
		out, err := insertAfter(e.rel, e.src, mailLoginFieldRE, 0, devicePageText+"\n")
		if errors.Is(err, app.ErrNoAnchor) {
			out, err = app.AddStructField(e.rel, e.src, "Settings", devicePageText)
		}
		if err := e.gain(out, err, "Settings carries DevicePage", "no Settings struct to carry the device page in; declare DevicePage string on the auth's settings"); err != nil {
			return err
		}
	}
	if bytes.Contains(e.src, []byte("WithDeviceVerificationURL(")) {
		return nil
	}
	var out []byte
	err := error(errors.Wrapf(app.ErrNoAnchor, "%s: New takes no settings *Settings", e.rel))
	if bytes.Contains(e.src, []byte("settings *Settings")) {
		out, err = insertAfter(e.rel, e.src, newAuthEndRE, len("\t)\n"), verificationOption)
	}

	return e.gain(out, err, "New passes it as the device verification path (session.WithDeviceVerificationURL)", "the Auth's construction is not written as the tool writes it (session.NewAuth[...] in New(..., settings *Settings), one option to a line), so the device page is not passed to it; add session.WithDeviceVerificationURL(<the settings' DevicePage>) to its options")
}

// holdOnAuth gives the auth file the registered outlet: the handle on Handlers, the method
// the App reads it through, and the registration in New after the sign-in methods'.
func (o Outlet) holdOnAuth(e *sourceEdit) error {
	pascal := o.pascal()
	e.src = bytes.Replace(e.src, []byte(handlersDocEnd), []byte(handlersDocChecks), 1)
	out, err := app.AddStructField(e.rel, e.src, "Handlers", o.Name+" *session.MachineOutlet")
	if err := e.gain(out, err, "Handlers holds the outlet ("+o.Name+" *session.MachineOutlet)", fmt.Sprintf("no Handlers struct to hold the outlet in; hold the *session.MachineOutlet that session.RegisterMachineOutlet returns and return it from a %sTokens() method", pascal)); err != nil {
		return err
	}
	if !bytes.Contains(e.src, []byte(") "+pascal+"Tokens() ")) {
		out, err := insertAfter(e.rel, e.src, handlersTypeRE, 0, fmt.Sprintf(checkMethodText, pascal, o.Name))
		if err := e.gain(out, err, pascal+"Tokens() returns it", fmt.Sprintf("Handlers is not declared as the tool writes it, so %[1]sTokens() is not declared; declare func (h *Handlers) %[1]sTokens() *session.MachineOutlet returning the outlet registered under %[1]sOutlet", pascal)); err != nil {
			return err
		}
	}
	if bytes.Contains(e.src, []byte(", "+pascal+"Outlet)")) {
		return nil
	}
	out, err = nil, error(errors.Wrapf(app.ErrNoAnchor, "%s: New builds no handlers", e.rel))
	if m := registrationsRE.FindSubmatch(e.src); m != nil {
		out, err = insertAfter(e.rel, e.src, registrationsRE, 0, fmt.Sprintf(registrationText, pascal, o.Name, string(m[1])))
	}

	return e.gain(out, err, "New registers it (session.RegisterMachineOutlet)", fmt.Sprintf("New does not build its handlers as the tool writes it (handlers := &Handlers{Session: <the Auth>} and the sign-in methods' registrations after it), so the outlet is not registered; register it there, before the first request: handlers.%s = session.RegisterMachineOutlet(<the Auth>, %sOutlet)", o.Name, pascal))
}

// insertAfter puts text in where the first match of the pattern ends, or back bytes
// before that, and formats the file. A file the pattern does not match has no anchor.
func insertAfter(rel string, src []byte, re *regexp.Regexp, back int, text string) ([]byte, error) {
	loc := re.FindIndex(src)
	if loc == nil {
		return nil, errors.Wrapf(app.ErrNoAnchor, "%s has nothing matching %s", rel, re)
	}
	at := loc[1] - back
	out, err := format.Source(slices.Concat(src[:at], []byte(text), src[at:]))
	if err != nil {
		return nil, errors.Wrapf(err, "format.Source(): %s after the edit", rel)
	}

	return out, nil
}

// nameDevicePage sets the device page in every construction of the auth's settings the
// application's own Go makes, the data level's and a test harness's alike: New passes the
// page to the Auth's construction, which refuses an empty one, so a construction left
// without it would stop the server and every suite that builds the auth.
func (o Outlet) nameDevicePage(a *app.App, site *app.Site, pkg *app.AuthPackage, ch *Change) error {
	page := devicePage(site, pkg.Path)
	if page == "" {
		ch.skipf("no outlet that binds to the %s auth serves a browser application, so no construction of %s.Settings names the device page: set DevicePage to the page where a signed-in person approves a command-line client, a path of the application (New refuses an empty one)", o.Auth, pkg.Name)

		return nil
	}
	element := fmt.Sprintf("DevicePage: %q", page)
	var set []string
	err := walkOwnSources(a, func(root *os.Root, rel string, mode fs.FileMode, isGo, _ bool) error {
		if !isGo {
			return nil
		}
		file := filepath.FromSlash(rel)
		src, err := root.ReadFile(file)
		if err != nil {
			return errors.Wrap(err, "os.Root.ReadFile()")
		}
		if !bytes.Contains(src, []byte(pkg.Path)) {
			return nil
		}
		local, err := app.ImportName(rel, src, pkg.Path)
		if err != nil || local == "" {
			return err
		}
		out, err := app.AddLiteralElementToEachOfType(rel, src, local+".Settings", element)
		switch {
		case errors.Is(err, app.ErrNoAnchor):
			return nil
		case err != nil:
			return err
		case bytes.Equal(out, src):
			return nil
		}
		if err := root.WriteFile(file, out, mode); err != nil {
			return errors.Wrap(err, "os.Root.WriteFile()")
		}
		set = append(set, rel)

		return nil
	})
	if err != nil {
		return err
	}
	if len(set) == 0 {
		ch.skipf("no file builds a %s.Settings literal, so the device page is not set: set DevicePage to %q where the auth's settings are built, the tests' included (New refuses an empty one)", pkg.Name, page)

		return nil
	}
	ch.didf("%s: every %s.Settings names the device page, DevicePage: %q, the page of the console where a signed-in person approves a command-line client", strings.Join(set, ", "), pkg.Name, page)

	return nil
}

// declareTokenCheck writes the method the generated Handlers requires of the App for a
// machine outlet, where the App declares none of the name already: the token check of the
// outlet the auth registered, read from the auth's handlers the App holds.
func (o Outlet) declareTokenCheck(a *app.App, site *app.Site, auth *app.OutletAuth, ch *Change) error {
	pascal := o.pascal()
	required := fmt.Sprintf("the generated router's Handlers requires %[1]s() returning the outlet's token check (%[1]sTokens), which the %[2]s auth's handlers return from %[1]sTokens()", pascal, o.Auth)
	rel, src, _, err := findDeclaringFile(a, appType)
	if err != nil {
		return err
	}
	if rel == "" {
		ch.skipf("no file declares an %s struct, so the %s outlet's token check is not declared on it: %s", appType, o.Name, required)

		return nil
	}
	dir := path.Dir(rel)
	methods, err := methodsOfType(a, dir, appType)
	if err != nil {
		return err
	}
	if slices.Contains(methods, pascal) {
		return nil
	}
	holder, embedded, err := handlersField(rel, src, appType, auth.ImportPath)
	if err != nil {
		return err
	}
	if holder == "" {
		ch.skipf("%s: %s holds no *%s.Handlers, so %s() is not declared: %s", rel, appType, path.Base(auth.ImportPath), pascal, required)

		return nil
	}
	// Handlers the App embeds give it the token check as a method of its own, which is how
	// the base's reference reads it and what the application's lint asks for (a selector
	// through the embedded field is refused); a field with a name is read through the name.
	tokens := "a." + holder + "." + pascal + "Tokens()"
	if embedded {
		tokens = "a." + pascal + "Tokens()"
	}
	router, routerPath := routerQualifier(a, site.Generator, dir)
	file := &appMethods{imports: routerPath}
	file.declare(pascal+"()", fmt.Sprintf("\n// %[1]s returns the %[2]s outlet's token check (%[5]s%[1]sTokens): the machine outlet the\n// %[3]s auth registered under its %[1]sOutlet, whose check requires \"Authorization: Bearer\n// <token>\", answers a request without one with the challenge naming the outlet, and binds\n// the request to the person the token acts for, the way the session middleware binds a\n// browser request to its user, so the handlers behind it run the same fail-closed\n// permission checks. A person signed in through the %[3]s auth issues an application token\n// for the outlet, or approves a command-line client's device authorization; the token\n// holds that person's roles. There is no configured key: the application issues every\n// token.\nfunc (a *%[6]s) %[1]s() %[5]s%[1]sTokens {\n\tif a.%[4]s == nil {\n\t\treturn nil\n\t}\n\n\treturn %[7]s\n}\n", pascal, o.Name, o.Auth, holder, router, appType, tokens))

	return o.writeAppMethods(a, &sourceEdit{rel: rel, src: src}, file, ch)
}
