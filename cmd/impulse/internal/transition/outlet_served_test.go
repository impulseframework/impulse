package transition

import (
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/authpkg"
)

// portalOutletFile is the file add outlet writes into the base's App package for a session
// outlet named portal: the three methods the generated router's Handlers requires.
const portalOutletFile = `package app

import (
	"net/http"

	"example.com/acme/depot/pkg/router"
)

// Portal returns the portal outlet's session handlers (router.PortalSession): the staff auth's,
// the auth the outlet binds to, whose people sign in under the outlet's prefix.
func (a *App) Portal() router.PortalSession {
	if a.Handlers == nil {
		return nil
	}

	return a.Handlers
}

// PortalDeepLink rewrites the portal's Angular routes under /portal/ to its entry document, so
// a bookmarked frontend route loads the single-page application: a request whose last
// segment has no extension is served index.html, and a path with an extension is a file
// or a 404.
func (a *App) PortalDeepLink(next http.Handler) http.Handler {
	return a.portalApp.DeepLink(next)
}

// PortalAssets serves the portal's built Angular application: a hashed file is immutable for
// a year, every other file (the entry document, the worker files, the manifest, the
// icons) is revalidated on each use, which is what the service worker needs under any
// cache.
func (a *App) PortalAssets() http.HandlerFunc {
	return a.portalApp.Assets()
}
`

// applied applies an outlet to the application with a go generate that writes nothing, and
// returns the application read again with the change.
func applied(t *testing.T, a *app.App, o Outlet) (*app.App, *Change) {
	t.Helper()

	ch, err := o.Apply(t.Context(), a, &fakeExec{})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	a, err = app.Discover(a.Root)
	if err != nil {
		t.Fatalf("app.Discover() error = %v", err)
	}

	return a, ch
}

// wantHolds fails the test for each text the file lacks.
func wantHolds(t *testing.T, a *app.App, rel string, wants ...string) {
	t.Helper()

	wantContains(t, a, rel, wants, nil)
}

// wantDid fails the test for each line the change does not report as made.
func wantDid(t *testing.T, ch *Change, wants ...string) {
	t.Helper()

	for _, want := range wants {
		if !slices.Contains(ch.Did, want) {
			t.Errorf("Did lacks %q; it is:\n%s", want, strings.Join(ch.Did, "\n"))
		}
	}
}

// wantGoAsGofmtWritesIt holds every hand-written Go file of the application to parsing and
// to gofmt's layout, which impulse check holds a new application to.
func wantGoAsGofmtWritesIt(t *testing.T, a *app.App) {
	t.Helper()

	root, err := os.OpenRoot(a.Root)
	if err != nil {
		t.Fatalf("os.OpenRoot() error = %v", err)
	}
	defer root.Close()

	err = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") {
			return err
		}
		src, err := root.ReadFile(p)
		if err != nil {
			t.Fatalf("os.Root.ReadFile(%s) error = %v", p, err)
		}
		formatted, err := format.Source(src)
		if err != nil {
			t.Errorf("%s does not parse: %v", p, err)

			return nil
		}
		if diff := cmp.Diff(string(formatted), string(src)); diff != "" {
			t.Errorf("%s is not as gofmt writes it (-want +got):\n%s", p, diff)
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestOutletApplyServesASessionOutlet pins what add outlet writes for a session outlet
// beyond the declaration, on the application impulse new renders: the App answers what
// the regenerated router's Handlers asks of it for the outlet, and the application's own
// sources follow the console off the root, so the application builds, serves the console
// where the router mounts it, mails links to a login page that is there, and its tests
// ask the API where it is. Nothing is left for the author.
func TestOutletApplyServesASessionOutlet(t *testing.T) {
	t.Parallel()

	a, ch := applied(t, newBase(t, "staff"), Outlet{Name: "portal", Prefix: "portal/api", Sessions: true})

	if diff := cmp.Diff([]string(nil), ch.Skipped); diff != "" {
		t.Errorf("Skipped mismatch (-want +got):\n%s", diff)
	}
	wantDid(t, ch,
		`app/app.go:124: the App builds the console's served browser app under /console, where the generated router mounts it now: NewBrowserApp(..., "/console") in place of "/"`,
		`pkg/config/data.go: the staff auth is told the console's pages where they are now, under /console: MailLoginURL "/console/login"`,
		`app/app.go: Configurer asks for PortalDist(), App holds portalApp, and New builds it (resource.NewBrowserApp(cfg.PortalDist(), "/portal")), so the App serves the portal's built bundle under /portal`,
		"app/portal_outlet.go: App declares Portal(), PortalDeepLink, and PortalAssets, which the generated router's Handlers requires of it for the portal outlet",
		"pkg/config/site.go: siteConfig reads APP_PORTAL_DIST (web/dist/portal by default) and SiteConfiguration answers PortalDist()",
		".envrc.template: names APP_PORTAL_DIST, the directory the portal's built bundle is served from, with its default web/dist/portal",
		"the test configurers answer PortalDist() with no directory, as they answer the console's, since no suite serves a built bundle: test/authz/harness_test.go (testConfigurer); test/integration/harness_test.go (servedConfigurer)",
	)
	// The rewrite of the old prefix is reported as one line that names every line it
	// changed, for the author to read: the prose and the tests are in it.
	renamed := slices.IndexFunc(ch.Did, func(d string) bool {
		return strings.HasPrefix(d, "the console's API was under /api and is under /console/api, and the application's own Go, README, process files and environment template say so where they named the old path; they were rewritten as text, so read each line: Procfile:")
	})
	if renamed < 0 {
		t.Fatalf("Did reports no rewrite of the old prefix; it is:\n%s", strings.Join(ch.Did, "\n"))
	}
	for _, want := range []string{"; README.md:", "; cmd/generate/resourcegenerator/generator.go:", "; test/integration/harness_test.go:", "; test/integration/login_test.go:"} {
		if !strings.Contains(ch.Did[renamed], want) {
			t.Errorf("the rewrite's line lacks %q: %s", want, ch.Did[renamed])
		}
	}

	if diff := cmp.Diff(portalOutletFile, read(t, a, "app/portal_outlet.go")); diff != "" {
		t.Errorf("app/portal_outlet.go mismatch (-want +got):\n%s", diff)
	}
	wantHolds(t, a, "app/app.go",
		"\tConsoleDist() string\n\tPortalDist() string\n}\n",
		"\t// console serves the console's built bundle under /console: the resource package's served\n",
		"\t// portalApp serves the portal's built bundle under /portal, as the console's is served: the\n\t// resource package's served browser app holds the deep-link rewrite, the prefix strip,\n\t// and the two cache classes a service worker needs.\n\tportalApp *resource.BrowserApp\n}\n",
		"\t\tconsole:        resource.NewBrowserApp(cfg.ConsoleDist(), \"/console\"),\n",
		"\t\tportalApp:      resource.NewBrowserApp(cfg.PortalDist(), \"/portal\"),\n\t}\n",
	)
	wantHolds(t, a, "pkg/config/site.go",
		"// PortalDist returns the directory the portal's built Angular bundle is served from.\nfunc (c *SiteConfiguration) PortalDist() string {\n\treturn c.env.PortalDist\n}\n",
		"\t// PortalDist is the directory holding the portal's built Angular bundle.\n\tPortalDist string `env:\"APP_PORTAL_DIST,default=web/dist/portal\"`\n}\n",
	)
	wantHolds(t, a, ".envrc.template", "# export APP_CONSOLE_DIST=web/dist/console\n# export APP_PORTAL_DIST=web/dist/portal\n")
	wantHolds(t, a, "test/authz/harness_test.go", "func (c *testConfigurer) ConsoleDist() string { return \"\" }\n\nfunc (c *testConfigurer) PortalDist() string { return \"\" }\n")
	// The login page a mailed link opens moves with the console where the server builds
	// the auth's settings; the harness's own address, which no browser opens, stays.
	wantHolds(t, a, "pkg/config/data.go", "\t\tMailLoginURL:   env.PublicURL + \"/console/login\",\n")
	wantHolds(t, a, "test/integration/harness_test.go",
		"func (c *servedConfigurer) ConsoleDist() string { return \"\" }\n\nfunc (c *servedConfigurer) PortalDist() string { return \"\" }\n",
		"MailLoginURL: \"http://solo.test/login\"",
		"return b.do(ctx, http.MethodPost, \"/console/api/user/password/login\", credentials)",
	)
	wantHolds(t, a, "cmd/generate/resourcegenerator/generator.go",
		"\t\t// outlet, the staff auth's password sessions under /console/api with its browser application\n\t\t// at /console.\n",
	)
	wantHolds(t, a, "README.md", "`GET /console/api/features` lists what is on", "proxying `/console/api` to the server.")
	wantHolds(t, a, "Procfile", "then ng serve with /console/api proxied to it.")
	for _, rel := range []string{"test/integration/login_test.go", "test/integration/harness_test.go", "README.md", "Procfile"} {
		if got := read(t, a, rel); strings.Contains(got, "\"/api/") || strings.Contains(got, " /api ") || strings.Contains(got, "`/api") {
			t.Errorf("%s still names /api, the console's old prefix", rel)
		}
	}
	wantGoAsGofmtWritesIt(t, a)
}

// TestOutletApplyIssuesAMachineOutletsTokens pins what add outlet writes for a machine
// outlet beyond the declaration, on the application impulse new renders: the auth file is
// the one the tool renders for an auth that issues the outlet's tokens, every construction
// of the auth's settings names the device page (the Auth's construction refuses an empty
// one), the console mounts that page, and the App answers the token check the regenerated
// router's Handlers asks of it. Nothing is left for the author.
func TestOutletApplyIssuesAMachineOutletsTokens(t *testing.T) {
	t.Parallel()

	a, ch := applied(t, newBase(t, "staff"), Outlet{Name: "devices", Prefix: "devices/api"})

	if diff := cmp.Diff([]string(nil), ch.Skipped); diff != "" {
		t.Errorf("Skipped mismatch (-want +got):\n%s", diff)
	}
	wantDid(t, ch,
		"pkg/auth/staff/staff.go: the staff auth issues the devices outlet's tokens: the file names the outlet (DevicesOutlet), Settings carries DevicePage, New passes it as the device verification path (session.WithDeviceVerificationURL), Handlers holds the outlet (devices *session.MachineOutlet), DevicesTokens() returns it, and New registers it (session.RegisterMachineOutlet)",
		`pkg/config/data.go, test/integration/harness_test.go: every staff.Settings names the device page, DevicePage: "/device", the page of the console where a signed-in person approves a command-line client`,
		"web/console/src/app/app.routes.ts: the console mounts the device page at /device, among its signed-in pages, where a command-line client sends the person who approves it; the page is web/console/src/app/components/ui/device/, from the reference: the library's device approval component, which reads the code the client printed, shows which program asks and for which outlet, and posts the person's answer",
		"app/devices_outlet.go: App declares Devices(), which the generated router's Handlers requires of it for the devices outlet",
	)

	// The auth file is edited in place into what the tool renders for the auth with the
	// outlet, so the two ways to an auth that issues tokens write one file.
	spec := authpkg.FirstAuth("example.com/acme/depot", "staff")
	spec.MachineOutlets = []string{"devices"}
	// The test's base is rendered under no name of its own, so its mail says the
	// skeleton's.
	spec.Application = "Solo"
	rendered, err := authpkg.Files(&spec)
	if err != nil {
		t.Fatalf("authpkg.Files() error = %v", err)
	}
	if diff := cmp.Diff(string(rendered["staff.go"]), read(t, a, "pkg/auth/staff/staff.go")); diff != "" {
		t.Errorf("pkg/auth/staff/staff.go is not the file the tool renders for the auth with the outlet (-want +got):\n%s", diff)
	}
	wantHolds(t, a, "pkg/config/data.go", "\t\tMailLoginURL:   env.PublicURL + \"/login\",\n\t\tDevicePage:     \"/device\",\n\t}\n")
	wantHolds(t, a, "test/integration/harness_test.go", "MailLoginURL: \"http://solo.test/login\", DevicePage: \"/device\"})")
	wantHolds(t, a, "app/devices_outlet.go",
		"import (\n\t\"example.com/acme/depot/pkg/router\"\n)\n",
		// The App embeds the auth's handlers, so the token check is the App's own method, as
		// the base's reference writes it: a selector through the embedded field is what the
		// application's own lint refuses.
		"func (a *App) Devices() router.DevicesTokens {\n\tif a.Handlers == nil {\n\t\treturn nil\n\t}\n\n\treturn a.DevicesTokens()\n}\n",
	)
	wantHolds(t, a, "web/console/src/app/app.routes.ts",
		"    canActivate: [LoginAuthenticationGuard],\n    children: [\n"+
			"      // The device page, where a command-line client sends the person for approval\n"+
			"      // (/device?userCode=..., the staff auth's DevicePage). It is no menu's and\n"+
			"      // no place's: the link lands here whichever place the person works in.\n"+
			"      {\n        path: 'device',\n"+
			"        loadComponent: () => import('./components/ui/device/device.component').then((comp) => comp.DeviceComponent),\n"+
			"      },\n      {\n        path: 'dashboard',\n",
	)
	for _, name := range deviceFiles {
		want, err := referenceText(ReferenceCandidate, deviceDir+name)
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(want, read(t, a, "web/console/src/"+deviceDir+name)); diff != "" {
			t.Errorf("%s is not the reference's (-want +got):\n%s", name, diff)
		}
	}
	wantGoAsGofmtWritesIt(t, a)
}

// TestOutletApplyReadsTheTokenCheckWhereTheAppHoldsIt pins the body of the method add
// outlet declares on the App for a machine outlet, by how the App holds the auth's
// handlers. Embedded, as the base's App holds them, their methods are the App's own and the
// token check is read as one, which is what the base's reference writes and what the
// application's lint passes: a selector through the embedded field is refused there. A
// field with a name is read through the name, since nothing is promoted from it.
func TestOutletApplyReadsTheTokenCheckWhereTheAppHoldsIt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// field is what the App's struct holds the handlers as, in place of the base's
		// embedded field; empty leaves the base's.
		field string
		want  string
	}{
		{
			name: "the App embeds the handlers",
			want: "func (a *App) Devices() router.DevicesTokens {\n\tif a.Handlers == nil {\n\t\treturn nil\n\t}\n\n\treturn a.DevicesTokens()\n}\n",
		},
		{
			name:  "the App holds them in a field with a name",
			field: "staffAuth",
			want:  "func (a *App) Devices() router.DevicesTokens {\n\tif a.staffAuth == nil {\n\t\treturn nil\n\t}\n\n\treturn a.staffAuth.DevicesTokens()\n}\n",
		},
		{
			name:  "the App holds them in a field the author named Handlers",
			field: "Handlers",
			want:  "func (a *App) Devices() router.DevicesTokens {\n\tif a.Handlers == nil {\n\t\treturn nil\n\t}\n\n\treturn a.Handlers.DevicesTokens()\n}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := newBase(t, "staff")
			if tt.field != "" {
				src := read(t, a, "app/app.go")
				held := strings.Replace(src, "\t*staff.Handlers\n", "\t"+tt.field+" *staff.Handlers\n", 1)
				if held == src {
					t.Fatal("the base's App does not embed *staff.Handlers, which the case renames")
				}
				held = strings.ReplaceAll(held, "a.Handlers = ", "a."+tt.field+" = ")
				if err := os.WriteFile(a.Abs("app/app.go"), []byte(held), 0o644); err != nil {
					t.Fatal(err)
				}
				discovered, err := app.Discover(a.Root)
				if err != nil {
					t.Fatal(err)
				}
				a = discovered
			}

			a, _ = applied(t, a, Outlet{Name: "devices", Prefix: "devices/api"})

			wantHolds(t, a, "app/devices_outlet.go", tt.want)
		})
	}
}

// TestOutletApplyBothKinds pins a machine outlet added to an application whose console
// left the root for a second browser application, which is what impulse new makes of
// --outlet with --api-outlet: the device page is the console's where the console is now,
// and an application under an auth of another name gets its own names throughout.
func TestOutletApplyBothKinds(t *testing.T) {
	t.Parallel()

	a, _ := applied(t, newBase(t, "members"), Outlet{Name: "portal", Prefix: "portal/api", Sessions: true})
	a, ch := applied(t, a, Outlet{Name: "devices", Prefix: "devices/api"})

	if diff := cmp.Diff([]string(nil), ch.Skipped); diff != "" {
		t.Errorf("Skipped mismatch (-want +got):\n%s", diff)
	}
	wantDid(t, ch, `pkg/config/data.go, test/integration/harness_test.go: every members.Settings names the device page, DevicePage: "/console/device", the page of the console where a signed-in person approves a command-line client`)
	wantHolds(t, a, "pkg/config/data.go", "\t\tMailLoginURL:   env.PublicURL + \"/console/login\",\n\t\tDevicePage:     \"/console/device\",\n\t}\n")
	wantHolds(t, a, "test/integration/harness_test.go", "DevicePage: \"/console/device\"})")
	wantHolds(t, a, "web/console/src/app/app.routes.ts", "      // (/console/device?userCode=..., the members auth's DevicePage). It is no menu's and\n")
	wantHolds(t, a, "web/console/src/"+deviceDir+"device.component.ts", "the members auth names the page as its DevicePage")
	wantHolds(t, a, "app/devices_outlet.go", "// members auth registered under its DevicesOutlet,")
	wantHolds(t, a, "app/portal_outlet.go", "(router.PortalSession): the members auth's,")
	wantGoAsGofmtWritesIt(t, a)

	// The portal's copy was taken before the console had the device page, and the device
	// page is the console's alone.
	if exists(a, "web/portal/src/"+deviceDir+"device.component.ts") {
		t.Error("the portal carries the device page, which is the console's")
	}
}

// TestOutletApplyKeepsWhatIsThere pins that the Go half writes over nothing: an outlet
// whose file of the App's package is there already leaves it as it is and says what the
// App still lacks, and a console that routes to a device page already keeps its routes.
func TestOutletApplyKeepsWhatIsThere(t *testing.T) {
	t.Parallel()

	a := newBase(t, "staff")
	const theirs = "package app\n\n// The author's own file.\n"
	if err := os.WriteFile(a.Abs("app/devices_outlet.go"), []byte(theirs), 0o644); err != nil {
		t.Fatal(err)
	}
	routes := strings.Replace(read(t, a, "web/console/src/app/app.routes.ts"), "      {\n        path: 'dashboard',\n", "      { path: 'device', redirectTo: 'dashboard' },\n      {\n        path: 'dashboard',\n", 1)
	if err := os.WriteFile(a.Abs("web/console/src/app/app.routes.ts"), []byte(routes), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := app.Discover(a.Root)
	if err != nil {
		t.Fatal(err)
	}

	a, ch := applied(t, a, Outlet{Name: "devices", Prefix: "devices/api"})

	want := []string{"app/devices_outlet.go: the file is there already and App declares no Devices(), so nothing was written; declare what is missing, which the generated router's Handlers requires for the devices outlet"}
	if diff := cmp.Diff(want, ch.Skipped); diff != "" {
		t.Errorf("Skipped mismatch (-want +got):\n%s", diff)
	}
	if got := read(t, a, "app/devices_outlet.go"); got != theirs {
		t.Errorf("app/devices_outlet.go = %q, want it left as it was", got)
	}
	if got := read(t, a, "web/console/src/app/app.routes.ts"); got != routes {
		t.Errorf("the routes changed, though they route to a device page already:\n%s", got)
	}
	if exists(a, "web/console/src/"+deviceDir+"device.component.ts") {
		t.Error("a device page was written beside the routes' own")
	}
}

// TestOutletValidateRefusesAHeldName pins that a machine outlet is refused a name the
// auth's Handlers holds something else under, where the outlet's token check would be
// held: the base's auth holds its password method's handle as password.
func TestOutletValidateRefusesAHeldName(t *testing.T) {
	t.Parallel()

	a := newBase(t, "staff")
	err := Outlet{Name: "password", Prefix: "password/api"}.Validate(a)
	want := `outlet name "password": pkg/auth/staff/staff.go holds something else under that name in its Handlers (a sign-in method's handle), where the outlet's token check would be held; name the outlet otherwise`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("Validate() error = %v, want %q", err, want)
	}
	// A session outlet holds nothing on the auth's Handlers, so the name is free to it.
	if err := (Outlet{Name: "password", Prefix: "password/api", Sessions: true}).Validate(a); err != nil {
		t.Errorf("Validate() of a session outlet error = %v", err)
	}
	if err := (Outlet{Name: "devices", Prefix: "devices/api"}).Validate(a); err != nil {
		t.Errorf("Validate() error = %v", err)
	}
}

// TestMountDevicePage pins where the command leaves the console's device page to the
// author, and what it keeps: the page of another outlet's browser application and a
// console whose routes do not open their signed-in pages as the skeletons write them are
// left as they are with what to mount said, an auth no browser application binds to is
// said nothing here (the device page's setting says it), and a console that has the page's
// component already keeps it and gains the route alone.
func TestMountDevicePage(t *testing.T) {
	t.Parallel()

	const (
		routesFile = "web/console/src/app/app.routes.ts"
		component  = "web/console/src/" + deviceDir + "device.component.ts"
		left       = "mount the library's device approval page (DeviceApprovalComponent of @impulseframework/resource-angular/tokens) at %s, among the signed-in pages of the browser application that binds to the staff auth, as the reference's console does (app/components/ui/device/device.component.ts and the device route of its app/app.routes.ts)"
		theirs     = "// The author's own device page.\n"
		route      = "        path: 'device',\n"
	)
	tests := []struct {
		name string
		// prepare changes the base, and the site as the command read it, before the page
		// is mounted.
		prepare       func(t *testing.T, a *app.App, site *app.Site)
		wantDid       []string
		wantSkipped   []string
		wantRoute     bool
		wantComponent string
	}{
		{
			name: "the page of another outlet's browser application",
			prepare: func(_ *testing.T, _ *app.App, site *app.Site) {
				staff := site.Default.Auth
				site.Default.Auth = nil
				site.Outlets = append(site.Outlets, app.Outlet{Name: "portal", Prefix: "portal/api", ServesSessions: true, Auth: staff, WebApp: "/portal"})
			},
			wantSkipped: []string{"the device page is the portal outlet's browser application's, and the command mounts it in the default outlet's alone, so it is not mounted: " + fmt.Sprintf(left, "/portal/device")},
		},
		{
			name: "routes that do not open the frame's children as the skeletons write them",
			prepare: func(t *testing.T, a *app.App, _ *app.Site) {
				t.Helper()

				routes := strings.Replace(read(t, a, routesFile), "canActivate: [LoginAuthenticationGuard],\n", "canActivate: [LoginAuthenticationGuard, SecondGuard],\n", 1)
				if err := os.WriteFile(a.Abs(routesFile), []byte(routes), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			wantSkipped: []string{routesFile + ": the routes do not open their signed-in pages as the skeletons write them (the children of the frame behind LoginAuthenticationGuard), so the device page is not mounted: " + fmt.Sprintf(left, "/device")},
		},
		{
			name: "an auth no browser application binds to",
			prepare: func(_ *testing.T, _ *app.App, site *app.Site) {
				site.Default.WebApp = ""
			},
		},
		{
			name: "a console that has the page's component already",
			prepare: func(t *testing.T, a *app.App, _ *app.Site) {
				t.Helper()

				if err := os.MkdirAll(filepath.Dir(a.Abs(component)), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(a.Abs(component), []byte(theirs), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			wantDid:       []string{routesFile + ": the console mounts the device page at /device, among its signed-in pages, where a command-line client sends the person who approves it"},
			wantRoute:     true,
			wantComponent: theirs,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := newBase(t, "staff")
			site := a.Profile().Sites[0]
			pkg, _ := authFileAt(a, site.Default.Auth.ImportPath)
			if pkg == nil {
				t.Fatal("the base has no staff auth package")
			}
			tt.prepare(t, a, &site)
			before := read(t, a, routesFile)

			ch := &Change{}
			if err := (Outlet{Name: "devices", Prefix: "devices/api", Auth: "staff"}).mountDevicePage(a, &site, pkg, ch); err != nil {
				t.Fatalf("mountDevicePage() error = %v", err)
			}

			if diff := cmp.Diff(tt.wantDid, ch.Did); diff != "" {
				t.Errorf("Did mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantSkipped, ch.Skipped); diff != "" {
				t.Errorf("Skipped mismatch (-want +got):\n%s", diff)
			}
			after := read(t, a, routesFile)
			if got := strings.Contains(after, route); got != tt.wantRoute {
				t.Errorf("the routes hold the device route: %v, want %v", got, tt.wantRoute)
			}
			if !tt.wantRoute && after != before {
				t.Errorf("the routes changed, though the page is not mounted:\n%s", after)
			}
			var got string
			if exists(a, component) {
				got = read(t, a, component)
			}
			if got != tt.wantComponent {
				t.Errorf("%s = %q, want %q", component, got, tt.wantComponent)
			}
			for _, name := range deviceFiles[1:] {
				if rel := "web/console/src/" + deviceDir + name; exists(a, rel) {
					t.Errorf("%s was written, though the page was not copied", rel)
				}
			}
		})
	}
}

func TestRenamePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		line        string
		old         string
		want        string
		wantChanged bool
	}{
		{name: "a string literal that opens with the path", line: `	b.do(ctx, http.MethodGet, "/api/user/session", nil)`, old: "/api", want: `	b.do(ctx, http.MethodGet, "/console/api/user/session", nil)`, wantChanged: true},
		{name: "the path alone in prose", line: "The console on `/api`.", old: "/api", want: "The console on `/console/api`.", wantChanged: true},
		{name: "after a host's port, which reads as a longer path's segment", line: "GET http://127.0.0.1:8090/api/features", old: "/api", want: "GET http://127.0.0.1:8090/api/features"},
		{name: "twice on a line", line: "/api and /api/x", old: "/api", want: "/console/api and /console/api/x", wantChanged: true},
		{name: "at the end of a line", line: "proxied to /api\n", old: "/api", want: "proxied to /console/api\n", wantChanged: true},
		{name: "inside a longer path", line: "the portal on `/portal/api`", old: "/api", want: "the portal on `/portal/api`"},
		{name: "inside a module path", line: "google.golang.org/api is not it", old: "/api", want: "google.golang.org/api is not it"},
		{name: "a longer name", line: "/apiary and /api-keys and /api_v2", old: "/api", want: "/apiary and /api-keys and /api_v2"},
		{name: "one that names it beside one that does not", line: "/portal/api, /api", old: "/api", want: "/portal/api, /console/api", wantChanged: true},
		{name: "the root is every path's start", line: `resource.NewBrowserApp(dist, "/")`, old: "/", want: `resource.NewBrowserApp(dist, "/")`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, changed := renamePath(tt.line, tt.old, "/console"+tt.old)
			if got != tt.want || changed != tt.wantChanged {
				t.Errorf("renamePath(%q) = %q, %v; want %q, %v", tt.line, got, changed, tt.want, tt.wantChanged)
			}
		})
	}
}

func TestPagesUnder(t *testing.T) {
	t.Parallel()

	const src = `package config

import (
	"example.com/acme/depot/pkg/auth/members"
	"example.com/acme/depot/pkg/auth/staff"
)

func settings(public string) (*staff.Settings, *members.Settings, *staff.Settings) {
	return &staff.Settings{
		LoginURL:     "/login",
		MailLoginURL: public + "/login",
		DevicePage:   "/device",
		Name:         "/login",
	}, &members.Settings{
		LoginURL: "/login",
	}, &staff.Settings{
		LoginURL:     "/console/login",
		MailLoginURL: "https://depot.example/login",
		DevicePage:   public,
	}
}
`
	const want = `package config

import (
	"example.com/acme/depot/pkg/auth/members"
	"example.com/acme/depot/pkg/auth/staff"
)

func settings(public string) (*staff.Settings, *members.Settings, *staff.Settings) {
	return &staff.Settings{
		LoginURL:     "/console/login",
		MailLoginURL: public + "/console/login",
		DevicePage:   "/console/device",
		Name:         "/login",
	}, &members.Settings{
		LoginURL: "/login",
	}, &staff.Settings{
		LoginURL:     "/console/login",
		MailLoginURL: "https://depot.example/login",
		DevicePage:   public,
	}
}
`
	got, moved, err := pagesUnder("pkg/config/data.go", []byte(src), "example.com/acme/depot/pkg/auth/staff", "/console")
	if err != nil {
		t.Fatalf("pagesUnder() error = %v", err)
	}
	if diff := cmp.Diff(want, string(got)); diff != "" {
		t.Errorf("pagesUnder() mismatch (-want +got):\n%s", diff)
	}
	wantMoved := []string{`LoginURL "/console/login"`, `MailLoginURL "/console/login"`, `DevicePage "/console/device"`}
	if diff := cmp.Diff(wantMoved, moved); diff != "" {
		t.Errorf("moved mismatch (-want +got):\n%s", diff)
	}

	// A file that does not import the auth builds none of its settings.
	got, moved, err = pagesUnder("pkg/config/data.go", []byte(src), "example.com/acme/depot/pkg/auth/partners", "/console")
	if err != nil || string(got) != src || moved != nil {
		t.Errorf("pagesUnder() for an auth the file does not import = %q, %v, %v; want the file as it was", got, moved, err)
	}
}
