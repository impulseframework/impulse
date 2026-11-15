package transition

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/app"
)

// tenancyWithBrowser is the files of an untenanted application whose console's menu
// file, configuration and routes are the base skeleton's, as rendered.
func tenancyWithBrowser(t *testing.T, edit func(files map[string]string)) map[string]string {
	t.Helper()

	files := tenancyFiles()
	for _, rel := range boundWebFiles {
		files["web/console/src/"+rel] = skeletonWeb(t, rel)
	}
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
// and the routes in the tenant shape, the reference's.
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
		"              run: () => void openNewTenantDialog(dialog),\n              place: 'outside',\n              permission: { resource: Resources.Tenants, permission: Permissions.Create },\n",
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

	did := strings.Join(ch.Did, "\n")
	for _, want := range []string{
		"web/console/src/app/app.menus.ts: the tenant menus ahead of the others",
		"web/console/src/app/app.config.ts: the frame names the place outside every tenant, Configuration with a gear",
		"web/console/src/app/app.routes.ts: the routes in the tenant shape, as the reference's",
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
		"import { StaffServiceAreaRoles, ServiceAreaDirectories, ServiceAreas } from '@app/service/zz_gen_resources';",
		"serviceAreaID = input.required<string>();",
		"this.api.serviceAreas.read([params.tenant])",
		"this.api.domain(params.tenant as Domain).serviceAreaDirectories.list()",
		"this.api.domain(params.tenant as Domain).staffServiceAreaRoles.list()",
		"this.api.can(Permissions.Update, Resources.ServiceAreas)",
		"export class TenantSignInComponent {",
		"connection: \"the Entra tenant's id\"",
	}, []string{"tenantID", ".tenants.", "Resources.Tenants"})
	wantContains(t, a, src+"components/ui/sign-in/tenant-sign-in.component.html", []string{
		"<h1>Who may enter {{ tenant.value()?.name || serviceAreaID() }}</h1>",
		"A service area declares who may enter it.",
		"or a Microsoft Entra tenant, named\n  by its tenant id.",
		"Your role in this service area does not cover its directories.",
		"@if (tenant.value(); as row) {",
	}, nil)
	wantContains(t, a, src+"components/ui/sign-in/tenant-sign-in.component.spec.ts", []string{
		"const workspace: ServiceAreaDirectories = { id: 'd-1', serviceAreaId: 'acme', method: 'google', connection: 'acme.example' };",
		"StaffServiceAreaRoles: { List: 'granted' },",
		"global: { ServiceAreas: { Read: 'granted', Update: 'granted' } },",
		"case `${environment.apiUrl}/service-areas/acme/service-area-directories`:",
		"case `${environment.apiUrl}/service-areas/acme/staff-service-area-roles`:",
		"fixture.componentRef.setInput('serviceAreaID', 'acme');",
		"'Your role in this service area does not cover its directories.',",
	}, []string{"tenantID", "/tenants/"})
	wantContains(t, a, src+"configs/tenants.config.ts", []string{
		"import { Resources, ServiceAreas } from '@app/service/zz_gen_constants';",
		"export const tenantsConfig = rootConfig({",
		"page: { label: 'All service areas', icon: 'domain' },",
		"title: 'Service areas',",
		"createTitle: 'Service area',",
		"primaryResource: Resources.ServiceAreas,",
		"{ id: ServiceAreas.fieldName.id },",
		"label: 'Service area',",
	}, nil)
	wantContains(t, a, src+"components/ui/new-tenant/new-tenant-dialog.component.ts", []string{
		"import { ServiceAreasCreate } from '@app/service/zz_gen_api';",
		"const body: ServiceAreasCreate = this.form.getRawValue();",
		"await this.api.serviceAreas.create(body)",
		"export function openNewTenantDialog(",
	}, nil)
	wantContains(t, a, src+"components/ui/new-tenant/new-tenant-dialog.component.html", []string{
		"<h2 mat-dialog-title>New service area</h2>",
		"The service area is served the moment it exists.",
		"Create service area</button>",
	}, nil)
	wantContains(t, a, src+"components/ui/new-tenant/new-tenant-dialog.component.spec.ts", []string{
		"toContain('New service area')",
		"{ serviceAreas: ['west'] }",
		"path: '/service-areas/west'",
		"['/', 'service-areas', 'west']",
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
