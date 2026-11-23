package transition

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/names"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton"
)

// tenancyWithBrowser is the files of an untenanted application whose console's menu
// file, configuration, routes and sign-in page are the base skeleton's, as rendered.
func tenancyWithBrowser(t *testing.T, edit func(files map[string]string)) map[string]string {
	t.Helper()

	files := tenancyFiles()
	for _, rel := range boundWebFiles {
		files["web/console/src/"+rel] = skeletonWeb(t, rel)
	}
	files["web/console/src/"+loginPageFile] = skeletonWeb(t, loginPageFile)
	if edit != nil {
		edit(files)
	}

	return files
}

// applyTenancy applies add tenancy for the table over the files.
func applyTenancy(t *testing.T, table string, files map[string]string) (*app.App, *Change) {
	t.Helper()

	a := beacon(t, files)
	ch, err := Tenancy{Table: table}.Apply(t.Context(), a, &fakeExec{})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	return a, ch
}

// wantContains reports each substring the file lacks, and each it must not hold.
func wantContains(t *testing.T, a *app.App, rel string, want, notWant []string) {
	t.Helper()

	got := read(t, a, rel)
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("%s lacks %q", rel, w)
		}
	}
	for _, w := range notWant {
		if strings.Contains(got, w) {
			t.Errorf("%s holds %q", rel, w)
		}
	}
}

// wantNoSignInPageOfItsOwn reports a file the browser half wrote of a tenant's Sign-in
// settings page of the application's own: the library draws the page, and the routes and
// the menu file place it, so nothing lies under the reference's old place for it and no
// file names its component.
func wantNoSignInPageOfItsOwn(t *testing.T, a *app.App) {
	t.Helper()

	if _, err := os.Stat(a.Abs("web/console/src/app/components/ui/sign-in")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("web/console/src/app/components/ui/sign-in/ was written (stat error %v): the library draws the tenant's Sign-in settings page", err)
	}
	var written []string
	err := filepath.WalkDir(a.Abs("web"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			written = append(written, p)
		}

		return err
	})
	if err != nil {
		t.Fatalf("filepath.WalkDir() error = %v", err)
	}
	for _, p := range written {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("os.ReadFile() error = %v", err)
		}
		if strings.Contains(filepath.Base(p), "tenant-sign-in") || strings.Contains(string(data), "tenant-sign-in") {
			t.Errorf("%s is or names a tenant sign-in page of the application's own", p)
		}
	}
}

// TestTenancyBrowser pins the browser half of add tenancy over the base skeleton's
// console: the tenant menus ahead of the others in the menu file, the Feature flags page
// placed as it was, Configuration named in the frame's configuration,
// the routes in the tenant shape, the reference's, and a sign-in arriving at the root.
func TestTenancyBrowser(t *testing.T) {
	t.Parallel()

	a, ch := applyTenancy(t, "Tenants", tenancyWithBrowser(t, nil))

	menus := "web/console/src/app/app.menus.ts"
	// The Tenants menu holds the list alone: nobody creates a tenant directly, so the menu
	// file gains no entry that would, and no dialog service for one. The page staff bring
	// a tenant in from is among the auth's pages the System menu already places.
	wantContains(t, a, menus, []string{
		"import { resourcePage } from '@impulseframework/resource-angular/resource-route-generator';\n",
		"import { identityMenuPages, tenantSignInPage } from '@impulseframework/resource-angular/identity-pages';\n",
		"import { tenantsConfig } from './configs/tenants.config';\n",
		"export function appMenus(): Menu[] {\n  return [\n    {\n      label: 'Tenants',",
		"          entries: [resourcePage(tenantsConfig, resourceMeta)],\n",
		"...identityMenuPages(apiDescriptor, resourceMeta, { auth: 'Staff', scope: 'domain' }),",
		"...identityMenuPages(apiDescriptor, resourceMeta, { auth: 'Staff', scope: 'global' }),",
		"      label: 'Tenant',\n      groups: [{ entries: [tenantSignInPage(apiDescriptor, { auth: 'Staff' })] }],\n    },\n    {\n      label: 'System',",
		"            featureFlagsPage(resourceMeta),\n",
	}, []string{"New tenant", "openNewTenantDialog", "MatDialog", "Permissions.Create", "FrameActions", "place: 'outside'"})
	// The library's identity pages give the menu file both the auth's pages and the
	// tenant's Sign-in settings page, through one import statement.
	if got := read(t, a, menus); strings.Count(got, "from '@impulseframework/resource-angular/identity-pages';") != 1 || strings.Count(got, "label: 'System'") != 1 {
		t.Errorf("%s repeats an import or a menu:\n%s", menus, got)
	}
	wantNoSignInPageOfItsOwn(t, a)

	wantContains(t, a, "web/console/src/app/app.config.ts", []string{
		"    provideFrame({\n      name: 'Solo',\n      home: 'dashboard',\n      outside: { label: 'Configuration', icon: 'settings' },\n      menus: appMenus,\n    }),\n",
		tenantedSentence,
	}, []string{untenantedSentence})

	reference, err := referenceText(TenancyReferenceCandidate, appRoutesFile)
	if err != nil {
		t.Fatal(err)
	}
	wantRoutes := strings.Replace(reference, selfServiceSentence, outsideOnlySentence, 1)
	if diff := cmp.Diff(wantRoutes, read(t, a, "web/console/src/app/app.routes.ts")); diff != "" {
		t.Errorf("routes mismatch (-want +got):\n%s", diff)
	}
	wantContains(t, a, "web/console/src/app/app.routes.ts", []string{
		"import { identityRoutes, tenantSignInRoute } from '@impulseframework/resource-angular/identity-pages';\n",
		"          tenantSignInRoute(apiDescriptor, { auth: 'Staff' }),\n",
	}, nil)

	// The sign-in page is the base's but for the landing: the rest of it, the application's
	// name among it, is as it was.
	login := "web/console/src/" + loginPageFile
	wantLogin := strings.Replace(skeletonWeb(t, loginPageFile), `home="/dashboard"`, `home="/"`, 1)
	if diff := cmp.Diff(wantLogin, read(t, a, login)); diff != "" {
		t.Errorf("sign-in page mismatch (-want +got):\n%s", diff)
	}
	if !strings.Contains(wantLogin, `<impulse-login-methods #methods home="/" (signedIn)="go($event)" />`) {
		t.Errorf("the base skeleton's sign-in page no longer hands the sign-in component the dashboard:\n%s", wantLogin)
	}

	did := strings.Join(ch.Did, "\n")
	for _, want := range []string{
		"web/console/src/app/app.menus.ts: the tenant menus ahead of the others",
		"web/console/src/app/app.config.ts: the frame names the place outside every tenant, Configuration with a gear",
		"web/console/src/app/app.routes.ts: the routes in the tenant shape, as the reference's",
		`web/console/src/app/components/login/login.component.html: a sign-in arrives at the root (home="/"), where the home guard sends the person to the place they last worked in`,
	} {
		if !strings.Contains(did, want) {
			t.Errorf("Did lacks %q:\n%s", want, did)
		}
	}
}

// Test_addImports pins how the menu file gains the reference's imports: a statement it
// holds is left, names from a module it imports names from already join that statement,
// on one line where it fits the skeletons' width and a name a line where it does not, and
// a statement of another module goes after its last import.
func Test_addImports(t *testing.T) {
	t.Parallel()

	const (
		pages = "import { identityMenuPages } from '@impulseframework/resource-angular/identity-pages';\n"
		frame = "import { Menu } from '@impulseframework/resource-angular/auth-frame';\n"
		body  = "\nexport function appMenus(): Menu[] {\n  return [];\n}\n"
	)
	tests := []struct {
		name       string
		text, want string
		imports    []string
	}{
		{
			name:    "a statement the file holds is left",
			text:    frame + pages + body,
			imports: []string{pages},
			want:    frame + pages + body,
		},
		{
			name:    "names from a module the file imports from join its statement",
			text:    frame + pages + body,
			imports: []string{"import { identityMenuPages, tenantSignInPage } from '@impulseframework/resource-angular/identity-pages';\n"},
			want:    frame + "import { identityMenuPages, tenantSignInPage } from '@impulseframework/resource-angular/identity-pages';\n" + body,
		},
		{
			name:    "a statement that would pass the width takes a name a line",
			text:    pages + frame + body,
			imports: []string{"import { tenantSignInPage, tenantSignInRoute, directoryGroupsPage } from '@impulseframework/resource-angular/identity-pages';\n"},
			want:    "import {\n  identityMenuPages,\n  tenantSignInPage,\n  tenantSignInRoute,\n  directoryGroupsPage,\n} from '@impulseframework/resource-angular/identity-pages';\n" + frame + body,
		},
		{
			name:    "a statement of another module goes after the last import",
			text:    pages + frame + body,
			imports: []string{"import { tenantsConfig } from './configs/tenants.config';\n"},
			want:    pages + frame + "import { tenantsConfig } from './configs/tenants.config';\n" + body,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.want, addImports(tt.text, tt.imports)); diff != "" {
				t.Errorf("addImports() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestTenancyBrowserEdited pins what is left: routes edited since the skeleton rendered
// them stay as they are and the brief says so, and a menu file that places the sign-in
// page already is not given the menus twice.
func TestTenancyBrowserEdited(t *testing.T) {
	t.Parallel()

	edited := "// the application's own routes\n"
	placed := "export const appMenus = [tenantSignInPage];\n"
	a, ch := applyTenancy(t, "Tenants", tenancyWithBrowser(t, func(files map[string]string) {
		files["web/console/src/app/app.routes.ts"] = edited
		files["web/console/src/app/app.menus.ts"] = placed
	}))
	if got := read(t, a, "web/console/src/app/app.routes.ts"); got != edited {
		t.Errorf("routes = %q, want them left", got)
	}
	if got := read(t, a, "web/console/src/app/app.menus.ts"); got != placed {
		t.Errorf("menus = %q, want them left", got)
	}
	skipped := strings.Join(ch.Skipped, "\n")
	for _, want := range []string{
		"web/console/src/app/app.routes.ts was edited since the skeleton rendered it, so it was left as it is: put the routes in the tenant shape, as the brief says",
		"web/console/src/app/app.menus.ts places the tenant's sign-in page already, so the tenant menus were left as they are",
	} {
		if !strings.Contains(skipped, want) {
			t.Errorf("Skipped lacks %q:\n%s", want, skipped)
		}
	}
}

// TestTenancyBrowserMenuSpec pins what add tenancy does to the menu file's test. The
// library draws the page staff bring a tenant in from for the auth a tenant is made
// through, among that auth's pages the System menu places, so the base skeleton's test,
// which lists those pages, gains it after Mailed links, in the record's words. A console
// whose auth makes no tenant has no such page and its test is left; a test that lists the
// pages in another form is left and the brief says what it gains.
func TestTenancyBrowserMenuSpec(t *testing.T) {
	t.Parallel()

	spec := "web/console/src/" + appMenusSpecFile
	base := skeletonWeb(t, appMenusSpecFile)
	listed := "      'Mailed links',\n      'Sessions',\n"
	if !strings.Contains(base, listed) {
		t.Fatalf("the base skeleton's menu test does not list the System menu's pages as this test reads them (%q):\n%s", listed, base)
	}
	// through is an application a tenant is made through the staff auth of: the auth is
	// bound to the default outlet, which serves the tenant record.
	through := func(specText string) func(files map[string]string) {
		return func(files map[string]string) {
			files["pkg/config/data.go"] = makerConfig
			files["pkg/auth/staff/staff.go"] = staffPackage(t, "staff.go")
			files["cmd/generate/resourcegenerator/main.go"] = boundProgram(t)
			files[spec] = specText
		}
	}

	t.Run("the skeleton's test gains the page after Mailed links, in the record's words", func(t *testing.T) {
		t.Parallel()

		a, ch := applyTenancy(t, "ServiceAreas", tenancyWithBrowser(t, through(base)))
		want := strings.Replace(base, listed, "      'Mailed links',\n      'Invited service areas',\n      'Sessions',\n", 1)
		if diff := cmp.Diff(want, read(t, a, spec)); diff != "" {
			t.Errorf("menu test mismatch (-want +got):\n%s", diff)
		}
		if did := strings.Join(ch.Did, "\n"); !strings.Contains(did, spec+": the pages the System menu places gain Invited service areas, the page staff bring a service area in from, after Mailed links") {
			t.Errorf("Did does not say the menu test gained the page:\n%s", did)
		}
	})

	t.Run("a console whose auth makes no tenant keeps its test", func(t *testing.T) {
		t.Parallel()

		a, ch := applyTenancy(t, "ServiceAreas", tenancyWithBrowser(t, func(files map[string]string) {
			files[spec] = base
		}))
		if diff := cmp.Diff(base, read(t, a, spec)); diff != "" {
			t.Errorf("menu test mismatch (-want +got):\n%s", diff)
		}
		if said := strings.Join(append(ch.Did, ch.Skipped...), "\n"); strings.Contains(said, appMenusSpecFile) {
			t.Errorf("the brief speaks of a menu test that was left as it is:\n%s", said)
		}
	})

	t.Run("a test in another form is left, and the brief says what it gains", func(t *testing.T) {
		t.Parallel()

		own := "// the application's own menu test\n"
		a, ch := applyTenancy(t, "ServiceAreas", tenancyWithBrowser(t, through(own)))
		if got := read(t, a, spec); got != own {
			t.Errorf("menu test = %q, want it left", got)
		}
		if skipped := strings.Join(ch.Skipped, "\n"); !strings.Contains(skipped, spec+" does not list the pages the System menu places as the skeleton's test does ('Mailed links', then 'Sessions'), so it was left as it is: the System menu gains Invited service areas, the page staff bring a service area in from, after Mailed links") {
			t.Errorf("Skipped does not say what the menu test gains:\n%s", skipped)
		}
	})
}

// TestTenancyBrowserDialogsSpec pins what add tenancy does to the test of the dialogs.
// The base skeleton's test expects the dialog that invites a person to ask for no place,
// and once there are tenants that dialog asks for the tenant, so a test still as the
// skeleton rendered it, for the console's auth, is written again as the tenanted
// skeleton's, which has no test of that dialog. A test of the application's own is left
// and the brief says what it must expect; an application with no such test, or with the
// tenanted skeleton's already, is left without a word.
func TestTenancyBrowserDialogsSpec(t *testing.T) {
	t.Parallel()

	spec := "web/console/src/" + appDialogsSpecFile
	base := skeletonWeb(t, appDialogsSpecFile)
	reference, err := referenceText(TenancyReferenceCandidate, appDialogsSpecFile)
	if err != nil {
		t.Fatal(err)
	}
	// The reference's test names no tenant and no auth, so in the record's words and for
	// any auth it is the reference's as it is.
	for _, word := range []string{"tenant", "Tenant", "Staff", "staff"} {
		if strings.Contains(reference, word) {
			t.Fatalf("the %s skeleton's %s holds %q, which this test expects it not to:\n%s", TenancyReferenceCandidate, appDialogsSpecFile, word, reference)
		}
	}
	with := func(specText string) func(files map[string]string) {
		return func(files map[string]string) {
			files[spec] = specText
		}
	}
	said := func(ch *Change) string {
		return strings.Join(append(append([]string{}, ch.Did...), ch.Skipped...), "\n")
	}
	wantDid := spec + ": written again as the tenanted skeleton's, which has no test of the dialog that invites a person: the base skeleton's test expects that dialog to ask for no place, and with service areas it asks for the service area"

	t.Run("the skeleton's test is written again as the tenanted skeleton's, in the record's words", func(t *testing.T) {
		t.Parallel()

		a, ch := applyTenancy(t, "ServiceAreas", tenancyWithBrowser(t, with(base)))
		if diff := cmp.Diff(reference, read(t, a, spec)); diff != "" {
			t.Errorf("dialogs test mismatch (-want +got):\n%s", diff)
		}
		if did := strings.Join(ch.Did, "\n"); !strings.Contains(did, wantDid) {
			t.Errorf("Did does not say the dialogs test was written again:\n%s", did)
		}
	})

	t.Run("the skeleton's test as impulse new renders it for another auth is written again for that auth", func(t *testing.T) {
		t.Parallel()

		a, ch := applyTenancy(t, "ServiceAreas", tenancyWithBrowser(t, func(files map[string]string) {
			for _, rel := range boundWebFiles {
				files["web/console/src/"+rel] = names.Rename(files["web/console/src/"+rel], skeleton.PlaceholderAuth, "people")
			}
			files[spec] = names.Rename(base, skeleton.PlaceholderAuth, "people")
		}))
		got := read(t, a, spec)
		if diff := cmp.Diff(reference, got); diff != "" {
			t.Errorf("dialogs test mismatch (-want +got):\n%s", diff)
		}
		for _, placeholder := range []string{"Staff", "staff", "STAFF"} {
			if strings.Contains(got, placeholder) {
				t.Errorf("%s names the placeholder %q", spec, placeholder)
			}
		}
		if did := strings.Join(ch.Did, "\n"); !strings.Contains(did, wantDid) {
			t.Errorf("Did does not say the dialogs test was written again:\n%s", did)
		}
	})

	t.Run("a test of the application's own is left, and the brief says what it must expect", func(t *testing.T) {
		t.Parallel()

		own := "// the application's own dialogs test\n"
		a, ch := applyTenancy(t, "ServiceAreas", tenancyWithBrowser(t, with(own)))
		if got := read(t, a, spec); got != own {
			t.Errorf("dialogs test = %q, want it left", got)
		}
		if skipped := strings.Join(ch.Skipped, "\n"); !strings.Contains(skipped, spec+" was edited since the skeleton rendered it, so it was left as it is: the dialog that invites a person now asks for the service area, so a test that expects it to ask for no place, as the base skeleton's does, fails until it expects the service area or is taken out") {
			t.Errorf("Skipped does not say what the dialogs test must expect:\n%s", skipped)
		}
	})

	t.Run("no such test, or the tenanted skeleton's already, is left without a word", func(t *testing.T) {
		t.Parallel()

		a, ch := applyTenancy(t, "ServiceAreas", tenancyWithBrowser(t, nil))
		if _, err := os.Stat(a.Abs(spec)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s was written where the application had none (stat error %v)", spec, err)
		}
		if got := said(ch); strings.Contains(got, appDialogsSpecFile) {
			t.Errorf("the brief speaks of a dialogs test the application does not have:\n%s", got)
		}

		a, ch = applyTenancy(t, "ServiceAreas", tenancyWithBrowser(t, with(reference)))
		if diff := cmp.Diff(reference, read(t, a, spec)); diff != "" {
			t.Errorf("dialogs test mismatch (-want +got):\n%s", diff)
		}
		if got := said(ch); strings.Contains(got, appDialogsSpecFile) {
			t.Errorf("the brief speaks of a dialogs test that was left as it is:\n%s", got)
		}
	})
}

// TestTenancyBrowserAuth pins the browser half for an application whose auth is not the
// skeletons' placeholder, as impulse new --auth people renders the base: the routes are
// found as rendered and written in the tenant shape, and the menus ask the library for
// the auth's pages, the tenant's Sign-in settings page among them, and speak of it.
// Nothing the step writes names the placeholder.
func TestTenancyBrowserAuth(t *testing.T) {
	t.Parallel()

	a, ch := applyTenancy(t, "ServiceAreas", tenancyWithBrowser(t, func(files map[string]string) {
		for _, rel := range boundWebFiles {
			files["web/console/src/"+rel] = names.Rename(files["web/console/src/"+rel], skeleton.PlaceholderAuth, "people")
		}
	}))
	src := "web/console/src/app/"
	placeholder := []string{"Staff", "staff", "STAFF"}

	for _, f := range tenantWebFiles {
		wantContains(t, a, "web/console/src/"+f.rel, nil, placeholder)
	}

	reference, err := referenceText(TenancyReferenceCandidate, appRoutesFile)
	if err != nil {
		t.Fatal(err)
	}
	tn := Tenancy{Table: "ServiceAreas"}
	wantRoutes := tn.inRecordWords(appRoutesFile, names.Rename(strings.Replace(reference, selfServiceSentence, outsideOnlySentence, 1), skeleton.PlaceholderAuth, "people"))
	if diff := cmp.Diff(wantRoutes, read(t, a, src+"app.routes.ts")); diff != "" {
		t.Errorf("routes mismatch (-want +got):\n%s", diff)
	}
	wantContains(t, a, src+"app.routes.ts", []string{
		"// The people's accounts across the whole application",
		"...identityRoutes(apiDescriptor, resourceMeta, { auth: 'People', scope: 'global' }),",
		"...identityRoutes(apiDescriptor, resourceMeta, { auth: 'People', scope: 'domain' }).map(underTenant),",
		"tenantSignInRoute(apiDescriptor, { auth: 'People' }),",
	}, placeholder)
	wantContains(t, a, src+"app.menus.ts", []string{
		"side of the People auth's generated management.",
		"...identityMenuPages(apiDescriptor, resourceMeta, { auth: 'People', scope: 'domain' }),",
		"groups: [{ entries: [tenantSignInPage(apiDescriptor, { auth: 'People' })] }],",
	}, placeholder)

	if skipped := strings.Join(ch.Skipped, "\n"); strings.Contains(skipped, "web/console/") {
		t.Errorf("Skipped speaks of the browser, which was as impulse new rendered it:\n%s", skipped)
	}
	if did := strings.Join(ch.Did, "\n"); !strings.Contains(did, "the people auth's people across the application") {
		t.Errorf("Did does not name the people auth in the routes it wrote:\n%s", did)
	}
}

// TestConsoleAuth pins whose people the console's pages are taken to be: the auth its
// routes ask the library's pages of, else the one its menu file asks them of, else the
// one the site signs its people in through, else the skeletons' placeholder.
func TestConsoleAuth(t *testing.T) {
	t.Parallel()

	const (
		source  = "web/console/src"
		asks    = "...identityRoutes(apiDescriptor, resourceMeta, { auth: 'Partners', scope: 'global' }),\n"
		menuAsk = "...identityMenuPages(apiDescriptor, resourceMeta, { auth: 'Members', scope: 'global' }),\n"
	)
	signsIn := &app.Site{Default: app.Outlet{Auth: &app.OutletAuth{ImportPath: "example.com/acme/beacon/pkg/auth/people"}}}
	tests := []struct {
		name  string
		files map[string]string
		site  *app.Site
		want  string
	}{
		{
			name:  "the auth the routes ask the library's pages of",
			files: map[string]string{source + "/" + appRoutesFile: asks, source + "/" + appMenusFile: menuAsk},
			site:  signsIn,
			want:  "partners",
		},
		{
			name:  "the auth the menu file asks them of, where the routes ask for none",
			files: map[string]string{source + "/" + appRoutesFile: "// the application's own routes\n", source + "/" + appMenusFile: menuAsk},
			site:  signsIn,
			want:  "members",
		},
		{
			name: "the auth the site signs its people in through, where neither file asks",
			site: signsIn,
			want: "people",
		},
		{
			name: "the placeholder, where the site binds no auth",
			site: &app.Site{},
			want: skeleton.PlaceholderAuth,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			files := tenancyFiles()
			for rel, text := range tt.files {
				files[rel] = text
			}
			if got := consoleAuth(beacon(t, files), tt.site, source); got != tt.want {
				t.Errorf("consoleAuth() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestTenancyBrowserSignIn pins what add tenancy does with a sign-in page that is not as
// the base skeleton rendered it: one that arrives at the root already is left without a
// word, and one that names another landing, binds it, or draws no sign-in component is
// left as it is and told what to change.
func TestTenancyBrowserSignIn(t *testing.T) {
	t.Parallel()

	const (
		rel    = "web/console/src/" + loginPageFile
		advice = `so it was left as it is: have a sign-in arrive at the root (home="/" on impulse-login-methods), where the home guard sends the person to the place they last worked in`
	)
	tests := []struct {
		name string
		page string
		// skipped is what Skipped says of the page; empty where nothing is said.
		skipped string
	}{
		{
			name: "a page that arrives at the root already",
			page: `<impulse-login-methods #methods home="/" [labels]="labels" (signedIn)="go($event)" />` + "\n",
		},
		{
			name: "a page that names no landing arrives at the root, the component's own",
			page: `<impulse-login-methods (signedIn)="go($event)" />` + "\n",
		},
		{
			name:    "a page that names another landing",
			page:    `<impulse-login-methods #methods home="/orders" (signedIn)="go($event)" />` + "\n",
			skipped: rel + ` hands the sign-in component another landing than the skeleton rendered (home="/orders"), ` + advice,
		},
		{
			name:    "the skeleton's landing on a component edited since",
			page:    `<impulse-login-methods #methods home="/dashboard" [labels]="labels" (signedIn)="go($event)" />` + "\n",
			skipped: rel + ` hands the sign-in component another landing than the skeleton rendered (home="/dashboard"), ` + advice,
		},
		{
			name:    "a landing the page binds",
			page:    `<impulse-login-methods #methods [home]="home" (signedIn)="go($event)" />` + "\n",
			skipped: rel + ` hands the sign-in component another landing than the skeleton rendered ([home]="home"), ` + advice,
		},
		{
			name:    "a page of the application's own",
			page:    "<form>the application's own sign-in</form>\n",
			skipped: rel + " draws no impulse-login-methods, " + advice,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a, ch := applyTenancy(t, "Tenants", tenancyWithBrowser(t, func(files map[string]string) {
				files[rel] = tt.page
			}))
			if got := read(t, a, rel); got != tt.page {
				t.Errorf("sign-in page = %q, want it left as %q", got, tt.page)
			}
			if did := strings.Join(ch.Did, "\n"); strings.Contains(did, loginPageFile) {
				t.Errorf("Did speaks of the sign-in page:\n%s", did)
			}
			skipped := strings.Join(ch.Skipped, "\n")
			switch {
			case tt.skipped == "" && strings.Contains(skipped, loginPageFile):
				t.Errorf("Skipped speaks of the sign-in page:\n%s", skipped)
			case !strings.Contains(skipped, tt.skipped):
				t.Errorf("Skipped lacks %q:\n%s", tt.skipped, skipped)
			}
		})
	}
}

// TestTenancyWords pins the record's words as the screen shows them and inside a
// sentence: a record whose name ends in a plural acronym keeps its letters where there
// are several, and a record of two words is spelled as before.
func TestTenancyWords(t *testing.T) {
	t.Parallel()

	tests := []struct {
		table                                          string
		wantOne, wantMany, wantOneLower, wantManyLower string
	}{
		{table: "PartnerAPIs", wantOne: "Partner api", wantMany: "Partner APIs", wantOneLower: "partner api", wantManyLower: "partner APIs"},
		{table: "ServiceAreas", wantOne: "Service area", wantMany: "Service areas", wantOneLower: "service area", wantManyLower: "service areas"},
	}
	for _, tt := range tests {
		t.Run(tt.table, func(t *testing.T) {
			t.Parallel()

			tn := Tenancy{Table: tt.table}
			got := []string{tn.one(), tn.many(), tn.oneLower(), tn.manyLower()}
			want := []string{tt.wantOne, tt.wantMany, tt.wantOneLower, tt.wantManyLower}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("one(), many(), oneLower(), manyLower() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestTenancyBrowserRecordWords pins the record's words in everything the browser half
// writes, for a record of two words: every name the generator derives from the record
// (its resources, their bodies' types, the client's properties, the routes, the route
// parameter), every word the screen shows, and the copied components' names left as they
// are.
func TestTenancyBrowserRecordWords(t *testing.T) {
	t.Parallel()

	a, ch := applyTenancy(t, "ServiceAreas", tenancyWithBrowser(t, nil))
	src := "web/console/src/app/"

	// The library draws the service area's Sign-in settings page from the generated
	// description, which carries the record's names; the menu file and the routes place
	// it, and nothing of it is the application's own.
	wantNoSignInPageOfItsOwn(t, a)
	wantContains(t, a, src+"configs/tenants.config.ts", []string{
		"import { Resources, ServiceAreas } from '@app/service/zz_gen_constants';",
		"export const tenantsConfig = rootConfig({",
		"page: { label: 'All service areas', icon: 'domain' },",
		"title: 'Service areas',",
		"primaryResource: Resources.ServiceAreas,",
		// The list names a service area; its id, which nobody types, shows on the row
		// page alone, read only.
		"{ id: ServiceAreas.fieldName.name },",
		"field({ name: ServiceAreas.fieldName.id, label: 'Id', cols: 4, readOnly: true }),",
		"label: 'Service area',",
	}, nil)
	wantContains(t, a, src+"components/ui/new-tenant/new-tenant-dialog.component.ts", []string{
		"import { apiDescriptor, CreateServiceArea, CreateServiceAreaAnswer, createServiceAreaRoute } from '@app/service/zz_gen_api';",
		"const body: CreateServiceArea = this.form.getRawValue();",
		// The person who signed up creates their own through the generated route.
		"this.http.post<CreateServiceAreaAnswer>(`${environment.apiUrl}/${createServiceAreaRoute}`, body)",
		// A name another service area carries is refused in the record's word, which the
		// generated description carries; the reference's word is never written here.
		"named ${name} already exists. Choose another name.`;",
		"apiDescriptor.domainRoute?.word?.one",
		"export function openNewTenantDialog(dialog: MatDialog)",
		// Nobody creates the record directly: the dialog holds no create through the client.
	}, []string{".create(body)", "selfService", "injectApi"})
	wantContains(t, a, src+"components/ui/new-tenant/new-tenant-dialog.component.html", []string{
		"<h2 mat-dialog-title>Create your service area</h2>",
		"You are signed in and hold no service area. Name yours:",
		"What people call the service area, wherever it is listed. It can be changed later.",
		"Create service area</button>",
	}, []string{"New service area", "you are its first administrator"})
	wantContains(t, a, src+"components/ui/new-tenant/new-tenant-dialog.component.spec.ts", []string{
		"toContain('Create your service area')",
		"http.expectOne(`${environment.apiUrl}/${createServiceAreaRoute}`)",
		"expect(fixture.componentInstance.error()).toBe('A service area named West already exists. Choose another name.');",
	}, []string{"New service area", "path: '/service-areas'"})
	wantContains(t, a, src+"app.menus.ts", []string{
		"      label: 'Service areas',",
		"          entries: [resourcePage(tenantsConfig, resourceMeta)],",
		"      label: 'Service area',\n      groups: [{ entries: [tenantSignInPage(apiDescriptor, { auth: 'Staff' })] }],",
	}, []string{"Resources.Tenants", "New service area", "Permissions.Create"})
	wantContains(t, a, src+"app.routes.ts", []string{
		"// The tenant segment, service-areas/:serviceAreaID, mirroring the API's",
		"resourceRoutes(tenantsConfig, resourceMeta)",
		"          tenantSignInRoute(apiDescriptor, { auth: 'Staff' }),\n",
	}, []string{"tenants/:tenantID"})

	did := strings.Join(ch.Did, "\n")
	// The generator expects the record in the file named for it in snake case.
	if !strings.Contains(did, "pkg/resources/service_areas.go: the ServiceArea resource struct") {
		t.Errorf("Did does not write the record into service_areas.go:\n%s", did)
	}
	if !strings.Contains(did, "the library draws the service area's Sign-in settings page") || !strings.Contains(did, "with every name and word of the reference's Tenant record the ServiceArea record's") {
		t.Errorf("Did does not speak of service areas:\n%s", did)
	}
}

// TestRecordWordsInCode pins what the rename does to a TypeScript file that is not a
// spec: the record's word is renamed in the text of its string literals, quoted or
// template, since that is where a component holds a sentence it says from its code, and
// nowhere else: not in its code, not in a template literal's expressions, not in its
// comments, and not where a literal holds a name in code or the directory's own word.
func TestRecordWordsInCode(t *testing.T) {
	t.Parallel()

	tn := Tenancy{Table: "ServiceAreas"}
	for _, tt := range []struct {
		name string
		in   string
		want string
	}{
		{
			name: "a template literal's sentence, around its expression",
			in:   "words: `${this.nameOf(directory)} will no longer be this tenant's directory, and it is free for another tenant.`,",
			want: "words: `${this.nameOf(directory)} will no longer be this service area's directory, and it is free for another service area.`,",
		},
		{
			name: "a quoted sentence, single and double, one and many",
			in:   "title = 'Remove this tenant?';\nhint = \"The tenant's people keep their roles in other tenants.\";\nheading = 'Tenant and directory';",
			want: "title = 'Remove this service area?';\nhint = \"The service area's people keep their roles in other service areas.\";\nheading = 'Service area and directory';",
		},
		{
			name: "an expression is code, and a literal inside it is a literal",
			in:   "return `${this.tenant.value()?.name ?? 'this tenant'} has no directory. Ask the tenant's administrator.`;",
			want: "return `${this.tenant.value()?.name ?? 'this service area'} has no directory. Ask the service area's administrator.`;",
		},
		{
			name: "an expression that holds braces and a template literal of its own",
			in:   "return `Open to ${names.map((n) => { return `the tenant ${n}`; }).join(', ')} and to no other tenant.`;",
			want: "return `Open to ${names.map((n) => { return `the service area ${n}`; }).join(', ')} and to no other service area.`;",
		},
		{
			name: "the code and its comments are left, and an apostrophe in a comment opens no literal",
			in:   "/** The tenant's row: a tenant has one. */\ntenant = resource({ params: () => ({ tenant: this.tenantID() }) });\n// the tenant's name, or 'this tenant'\nlabel = 'Every tenant';",
			want: "/** The tenant's row: a tenant has one. */\ntenant = resource({ params: () => ({ tenant: this.serviceAreaID() }) });\n// the tenant's name, or 'this tenant'\nlabel = 'Every service area';",
		},
		{
			name: "a name in code that a literal holds is left, and so is the directory's own word",
			in:   "selector: 'app-tenant-sign-in',\ntemplateUrl: './tenant-sign-in.component.html',\nazure: 'Microsoft Entra tenant',\nhint: 'Its tenant id, as Microsoft shows it.',",
			want: "selector: 'app-tenant-sign-in',\ntemplateUrl: './tenant-sign-in.component.html',\nazure: 'Microsoft Entra tenant',\nhint: 'Its tenant id, as Microsoft shows it.',",
		},
		{
			name: "an escaped quote does not end a literal",
			in:   "a = 'the tenant\\'s own';\nb = `a \\` in a tenant ${x} \\${tenant}`;",
			want: "a = 'the service area\\'s own';\nb = `a \\` in a service area ${x} \\${service area}`;",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tn.inRecordWords("app/components/ui/sign-in/tenant-sign-in.component.ts", tt.in); got != tt.want {
				t.Errorf("inRecordWords() =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}
