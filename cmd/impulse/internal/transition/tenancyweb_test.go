package transition

import (
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

// TestTenancyBrowser pins the browser half of add tenancy over the base skeleton's
// console: the tenant menus ahead of the others in the menu file, the feature flags entry
// in the place outside every tenant, Configuration named in the frame's configuration,
// the routes in the tenant shape, the reference's, and a sign-in arriving at the root.
func TestTenancyBrowser(t *testing.T) {
	t.Parallel()

	a, ch := applyTenancy(t, "Tenants", tenancyWithBrowser(t, nil))

	menus := "web/console/src/app/app.menus.ts"
	wantContains(t, a, menus, []string{
		"import { inject } from '@angular/core';\n",
		"import { MatDialog } from '@angular/material/dialog';\n",
		"import { resourcePage } from '@impulseframework/resource-angular/resource-route-generator';\n",
		"import { openNewTenantDialog } from '@components/ui/new-tenant/new-tenant-dialog.component';\n",
		"import { tenantSignInPage } from '@components/ui/sign-in/sign-in.routes';\n",
		"import { tenantsConfig } from './configs/tenants.config';\n",
		"export function appMenus(actions: FrameActions): Menu[] {\n  const dialog = inject(MatDialog);\n  return [\n    {\n      label: 'Tenants',",
		"            resourcePage(tenantsConfig, resourceMeta),\n",
		"              run: () => void openNewTenantDialog(dialog, { selfService: false }),\n              place: 'outside',\n              permission: { resource: Resources.Tenants, permission: Permissions.Create },\n",
		"...identityMenuPages(apiDescriptor, resourceMeta, { auth: 'Staff', scope: 'domain' }),",
		"      label: 'Tenant',\n      groups: [{ entries: [tenantSignInPage] }],\n    },\n    {\n      label: 'System',",
		"              run: () => actions.featureFlags(),\n              place: 'outside',\n              permission: { resource: Resources.FeatureFlags, permission: Permissions.List },\n",
	}, nil)
	if got := read(t, a, menus); strings.Count(got, "import { identityMenuPages }") != 1 || strings.Count(got, "label: 'System'") != 1 {
		t.Errorf("%s repeats an import or a menu:\n%s", menus, got)
	}

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

// TestTenancyBrowserAuth pins the browser half for an application whose auth is not the
// skeletons' placeholder, as impulse new --auth people renders the base: the copied
// pages read the auth's own role resource, the routes are found as rendered and written
// in the tenant shape, and the menus ask the library for the auth's pages and speak of
// it. Nothing the step writes names the placeholder.
func TestTenancyBrowserAuth(t *testing.T) {
	t.Parallel()

	a, ch := applyTenancy(t, "ServiceAreas", tenancyWithBrowser(t, func(files map[string]string) {
		for _, rel := range boundWebFiles {
			files["web/console/src/"+rel] = names.Rename(files["web/console/src/"+rel], skeleton.PlaceholderAuth, "people")
		}
	}))
	src := "web/console/src/app/"
	placeholder := []string{"Staff", "staff", "STAFF"}

	// The page names no role resource of its own: the role is picked in the method's
	// dialog, which reads the picker from the generated metadata. Its spec scripts the
	// auth's list of a tenant's roles.
	wantContains(t, a, src+"components/ui/sign-in/tenant-sign-in.component.ts", []string{
		"import { ServiceAreaDirectories, ServiceAreas } from '@app/service/zz_gen_resources';",
	}, placeholder)
	wantContains(t, a, src+"components/ui/sign-in/tenant-sign-in.component.spec.ts", []string{
		"PeopleServiceAreaRoles: { List: 'granted' },",
		"case `${environment.apiUrl}/service-areas/${acmeID}/people-service-area-roles`:",
	}, placeholder)
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
	}, placeholder)
	wantContains(t, a, src+"app.menus.ts", []string{
		"side of the People auth's generated management.",
		"...identityMenuPages(apiDescriptor, resourceMeta, { auth: 'People', scope: 'domain' }),",
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

// TestTenancyBrowserRecordWords pins the record's words in everything the browser half
// writes, for a record of two words: every name the generator derives from the record
// (its resources, their bodies' types, the client's properties, the routes, the route
// parameter, the directories record's tenant field), every word the screen shows, and the
// library's own words left as they are (a Microsoft Entra tenant, its tenant id, the
// copied components' names).
func TestTenancyBrowserRecordWords(t *testing.T) {
	t.Parallel()

	a, ch := applyTenancy(t, "ServiceAreas", tenancyWithBrowser(t, nil))
	src := "web/console/src/app/"

	wantContains(t, a, src+"components/ui/sign-in/sign-in.routes.ts", []string{
		"permission: { resource: Resources.ServiceAreaDirectories, permission: Permissions.List },",
		"export const tenantSignInPage = page({",
	}, []string{"TenantDirectories"})
	wantContains(t, a, src+"components/ui/sign-in/tenant-sign-in.component.ts", []string{
		"import { ServiceAreaDirectories, ServiceAreas } from '@app/service/zz_gen_resources';",
		"serviceAreaID = input.required<string>();",
		"this.api.serviceAreas.read([params.tenant])",
		"this.api.domain(params.tenant as Domain).serviceAreaDirectories.list()",
		"private own = computed(() => this.api.domain(this.serviceAreaID() as Domain));",
		// The record's own methods, each named for the record: the two that turn the
		// switch and the two that say how it admits its directory's people.
		"canMakeOnly = computed(() => this.permissions() && this.own().makeServiceAreaDirectoryOnly.can());",
		"canEndOnly = computed(() => this.permissions() && this.own().endServiceAreaDirectoryOnly.can());",
		"canOpen = computed(() => this.permissions() && this.own().openServiceAreaToDirectory.can());",
		"canInviteOnly = computed(() => this.permissions() && this.own().makeServiceAreaInviteOnly.can());",
		"Methods.MakeServiceAreaDirectoryOnly,",
		"() => this.own().makeServiceAreaDirectoryOnly.warning({}),",
		"Methods.OpenServiceAreaToDirectory,",
		"await this.run(Methods.EndServiceAreaDirectoryOnly, 'Turn directory only off', {",
		"await this.run(Methods.MakeServiceAreaInviteOnly, 'Make invite only', {",
		"export class TenantSignInComponent {",
		// The directory's own word stays: a Microsoft Entra tenant is not the record's.
		"azure: 'Microsoft Entra tenant',",
		"await this.own().serviceAreaDirectories.create({});",
	}, []string{"tenantID", ".tenants.", "Resources.Tenants", "TenantDirectoryOnly", "TenantToDirectory", "TenantInviteOnly"})
	wantContains(t, a, src+"components/ui/sign-in/tenant-sign-in.component.html", []string{
		"<h1>Who may enter {{ tenantName() || 'this service area' }}</h1>",
		"Google Workspace or a Microsoft Entra tenant. A service area has one directory, and a directory belongs to one service area.",
		"Make {{ mine.connection }} this service area's directory",
		"Your role in this service area does not cover its directory.",
		"@if (directoryOnly() !== undefined) {",
		"An administrator of this service area sets this, in the service area.",
	}, nil)
	wantContains(t, a, src+"components/ui/sign-in/tenant-sign-in.component.spec.ts", []string{
		"const workspace: ServiceAreaDirectories = { id: 'd-1', serviceAreaId: acmeID, method: 'google', connection: 'acme.example' };",
		"StaffServiceAreaRoles: { List: 'granted' },",
		"global: { ServiceAreas: { Read: 'granted', Update: 'granted' } },",
		"case `${environment.apiUrl}/service-areas/${acmeID}/service-area-directories`:",
		"case `${environment.apiUrl}/service-areas/${acmeID}/staff-service-area-roles`:",
		"MakeServiceAreaDirectoryOnly: { Execute: 'granted' },",
		"EndServiceAreaDirectoryOnly: { Execute: 'granted' },",
		"OpenServiceAreaToDirectory: { Execute: 'granted' },",
		"MakeServiceAreaInviteOnly: { Execute: 'granted' },",
		"'make-service-area-directory-only',",
		"'end-service-area-directory-only',",
		"'open-service-area-to-directory',",
		"'make-service-area-invite-only',",
		"expect(calls('open-service-area-to-directory')).toEqual([{ role: 'TenantAdministrator' }]);",
		"fixture.componentRef.setInput('serviceAreaID', acmeID);",
		"'Your role in this service area does not cover its directory.',",
		"expect(texts('.make-directory')).toEqual([\"Make acme.example this service area's directory\"]);",
	}, []string{"tenantID", "/tenants/", "TenantDirectoryOnly", "tenant-directory-only", "TenantToDirectory", "tenant-to-directory", "TenantInviteOnly", "tenant-invite-only"})
	wantContains(t, a, src+"configs/tenants.config.ts", []string{
		"import { Resources, ServiceAreas } from '@app/service/zz_gen_constants';",
		"export const tenantsConfig = rootConfig({",
		"page: { label: 'All service areas', icon: 'domain' },",
		"title: 'Service areas',",
		"createTitle: 'Service area',",
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
		"await this.api.serviceAreas.create(body)",
		"export function openNewTenantDialog(",
	}, nil)
	wantContains(t, a, src+"components/ui/new-tenant/new-tenant-dialog.component.html", []string{
		"<h2 mat-dialog-title>{{ data.selfService ? 'Create your service area' : 'New service area' }}</h2>",
		"The service area is served the moment it exists, and you are its first administrator.",
		"What people call the service area, wherever it is listed. It can be changed later.",
		"Create service area</button>",
	}, nil)
	wantContains(t, a, src+"components/ui/new-tenant/new-tenant-dialog.component.spec.ts", []string{
		"toContain('New service area')",
		"{ serviceAreas: [west] }",
		"path: '/service-areas'",
		"['/', 'service-areas', west]",
	}, nil)
	wantContains(t, a, src+"app.menus.ts", []string{
		"      label: 'Service areas',",
		"              label: 'New service area…',",
		"permission: { resource: Resources.ServiceAreas, permission: Permissions.Create },",
		"      label: 'Service area',\n      groups: [{ entries: [tenantSignInPage] }],",
	}, []string{"Resources.Tenants"})
	wantContains(t, a, src+"app.routes.ts", []string{
		"// The tenant segment, service-areas/:serviceAreaID, mirroring the API's",
		"resourceRoutes(tenantsConfig, resourceMeta)",
	}, []string{"tenants/:tenantID"})

	did := strings.Join(ch.Did, "\n")
	// The generator expects the record in the file named for it in snake case.
	if !strings.Contains(did, "pkg/resources/service_areas.go: the ServiceArea resource struct") {
		t.Errorf("Did does not write the record into service_areas.go:\n%s", did)
	}
	if !strings.Contains(did, "the service area's sign-in page") || !strings.Contains(did, "with every name and word of the reference's Tenant record the ServiceArea record's") {
		t.Errorf("Did does not speak of service areas:\n%s", did)
	}
}
