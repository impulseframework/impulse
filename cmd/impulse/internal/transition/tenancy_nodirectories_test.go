package transition

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/authpkg"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton"
)

// tenancyOverTheBase applies the tenancy step over the base skeleton's own auth
// package, App, browser files and a data level in the skeleton's shape, with the staff
// auth bound to the default outlet, so a tenant is made through it.
func tenancyOverTheBase(t *testing.T, tn Tenancy) (*app.App, *Change) {
	t.Helper()

	files := tenancyWithBrowser(t, nil)
	files["pkg/config/data.go"] = makerConfig
	files["pkg/auth/staff/staff.go"] = staffPackage(t, "staff.go")
	files["cmd/generate/resourcegenerator/main.go"] = boundProgram(t)
	files["app/app.go"] = strings.ReplaceAll(skeletonFile(t, "app/app.go"), skeletonModule, "example.com/acme/beacon")
	a := beacon(t, files)
	ch, err := tn.Apply(t.Context(), a, &fakeExec{})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	return a, ch
}

// TestTenancyApply_tenantDirectories pins the one choice that tells an application
// whose tenants can have a directory from one whose tenants come by invitation alone.
// With tenant directories (the step's default) the directories record and its table,
// the tenant record's directory-only switch, the auth's directory reader and admission
// and the tenant's Sign-in page are written. Without them none of it is: no file, table,
// column, setting, route or menu names a tenant's directory, while everything else of a
// tenanted application stands, the sign-in rule over the generated reader and the maker
// a tenant is made with among it. Each fragment is asked of both applications, so one
// that drifts out of the files fails here for the application that should carry it.
func TestTenancyApply_tenantDirectories(t *testing.T) {
	t.Parallel()

	const src = "web/console/src/app/"
	// directoryFiles exist with tenant directories alone. The tenant's Sign-in settings
	// page is the library's in both applications, and no file of either's own.
	directoryFiles := []string{
		"pkg/resources/tenant_directories.go",
	}
	// directory are fragments of files both applications have, held with tenant
	// directories alone.
	directory := map[string][]string{
		"schema/migrations/000003_Tenants.up.sql":   {"CREATE TABLE TenantDirectories (", "DirectoryOnly BOOL NOT NULL", "TenantDirectoriesByTenantId", "TenantDirectoriesByMethodConnection"},
		"schema/migrations/000003_Tenants.down.sql": {"DROP TABLE TenantDirectories;"},
		"pkg/resources/tenants.go":                  {"\t\t// @directoryOnly\n\t\tDirectoryOnly bool `spanner:\"DirectoryOnly\"`\n", "TenantDirectory"},
		"pkg/auth/staff/staff.go": {
			"\tTenantDirectories signin.DirectoryReader[*cloudspanner.ReadWriteTransaction]\n",
			"\tif settings.TenantDirectories == nil {\n",
			"\tauthSession.SetDirectoryAdmission(settings.TenantDirectories, identityClient.DirectorySeated())\n",
		},
		"pkg/config/data.go":              {"app.NewTenantDirectoryReader()"},
		src + "configs/tenants.config.ts": {"Tenants.fieldName.directoryOnly", "Directory only"},
		src + "app.menus.ts":              {"tenantSignInPage(apiDescriptor, { auth: 'Staff' })", "label: 'Tenant',"},
		src + "app.routes.ts":             {"tenantSignInRoute(apiDescriptor, { auth: 'Staff' })"},
	}
	// everywhere are fragments both applications hold: what tenancy is without a
	// tenant's directory.
	everywhere := map[string][]string{
		"schema/migrations/000003_Tenants.up.sql":   {"CREATE TABLE Tenants (", "CREATE UNIQUE INDEX TenantsByName ON Tenants(Name);"},
		"schema/migrations/000003_Tenants.down.sql": {"DROP INDEX TenantsByName;\nDROP TABLE Tenants;"},
		"pkg/resources/tenants.go":                  {"\t// @resource\n\t// @tenant\n\t// @order(Name asc)\n\tTenant struct {", "\t\t// @tenantName\n\t\tName string `spanner:\"Name\"`\n", "return resource.Config{TrackChanges: true}"},
		"pkg/auth/staff/staff.go": {
			"\tTenantDeclarations signin.DeclarationReader[*cloudspanner.ReadWriteTransaction]\n",
			"\tif settings.TenantDeclarations == nil {\n",
			"\tTenantMaker identity.TenantMaker\n}",
			"\tidentityClient, err := staffidentity.NewClient(accessClient, authSession.API(), settings.TenantMaker, identity.WithFirstAdministratorRoles(FirstAdministratorRoles()), identity.WithAdministratorRoles(tenantAdministrators)",
			"\tauthSession.SetSignInPolicy(signin.TenantPolicy(resource.SignInRoles(accessClient.UserManager()), settings.TenantDeclarations, signin.AdmitWithoutTenant()).Then(requireSecondFactor))\n" + passwordDisabledCheck,
		},
		"pkg/config/data.go":              {"app.NewTenantDeclarationReader()", "app.NewTenantMaker(tenants, liveService)"},
		"pkg/config/tenancy.go":           {"func (c *DataConfiguration) TenantDeclarations() resource.TenantDeclarations {\n\treturn c.tenants.Declarations\n}"},
		"app/tenancy.go":                  {"\tTenantDeclarations() resource.TenantDeclarations\n", "func (a *App) TenantAdministratorRoles() []accesstypes.Role {"},
		"app/app.go":                      {"\treturn resource.SessionPermissions(r.Context(), a.access.ForUser, a.access.ForRole, a.tenants.Domains, a.declarations, a.collection)\n"},
		src + "configs/tenants.config.ts": {"export const tenantsConfig = rootConfig({", "listColumns: [{ id: Tenants.fieldName.name }"},
		src + "components/ui/new-tenant/new-tenant-dialog.component.ts": {"export function openNewTenantDialog("},
		src + "app.menus.ts":  {"resourcePage(tenantsConfig, resourceMeta)", "scope: 'domain'"},
		src + "app.routes.ts": {"path: DomainRoutePath,", "resourceRoutes(tenantsConfig, resourceMeta)"},
	}
	// none are what the application whose tenants have no directories says in their
	// place.
	none := map[string][]string{
		"pkg/resources/tenants.go": {"\t// No tenant has a directory in this application: the package declares no\n\t// directories record and this record carries no directory-only switch, so a\n\t// tenant's people come by invitation alone"},
		"pkg/auth/staff/staff.go": {
			"\t// over the Tenant record (app.NewTenantDeclarationReader), which declares no\n\t// directories, so the reader answers nothing for every tenant. Required; New refuses\n\t// nil.\n",
			"\t// they signed in: a sign-in method through an organization's directory is a way\n\t// for an invited person to sign in and nothing more.",
		},
		"pkg/config/tenancy.go":           {"// roster's copy. The Tenant record declares no directories, so every tenant declares\n// nothing"},
		"app/tenancy.go":                  {"application knows it: the roster's copy. The tenant record declares no\n\t// directories, so every tenant declares nothing"},
		"app/app.go":                      {"No tenant has a directory in this\n// application, so no tenant declares who may enter it and none is closed to a\n// session: every tenant that seats the person is offered, however they signed in.\n"},
		src + "configs/tenants.config.ts": {"listColumns: [{ id: Tenants.fieldName.name }],\n"},
	}
	did := map[bool][]string{
		false: {
			"schema/migrations/000003_Tenants.up.sql and .down.sql: the Tenants table (a generated id as its primary key, a unique Name, and DirectoryOnly) and the TenantDirectories table (a tenant's directory with its DefaultSeat, held to one for a tenant and to one tenant by its two unique indexes)",
			"pkg/resources/tenant_directories.go: the TenantDirectory resource struct, a tenant's directory, set from a sign-in and never typed, tenant-scoped under /tenants/{tenantID}/tenant-directories",
			"pkg/config/data.go: dataConfig.staffSettings passes the declaration reader (app.NewTenantDeclarationReader()), the directory reader (app.NewTenantDirectoryReader()) and what makes a tenant from an accepted invitation (app.NewTenantMaker(tenants, liveService), over the roster built ahead of the auth) to the staff auth",
			"ran go generate ./..., which emitted the Tenant and TenantDirectory resources, the tenant segment pair under /tenants/{tenantID}, the roster constructor NewTenantRoster and the declaration reader NewTenantDeclarationReader",
		},
		true: {
			"schema/migrations/000003_Tenants.up.sql and .down.sql: the Tenants table (a generated id as its primary key and a unique Name; no tenant has a directory, so the table carries no directory-only switch and no directories table is written)",
			"pkg/resources/tenants.go: the Tenant resource struct, global, keyed by a generated id, listed in name order (@order(Name asc)), with change tracking and no directory-only switch, since no tenant has a directory",
			"pkg/auth/staff/staff.go: Settings carries the declaration reader (TenantDeclarations) and what makes a tenant from an accepted invitation (TenantMaker, which New hands the identity client), New refuses a nil one, and New sets the sign-in rule on the session (signin.TenantPolicy over the reader, the memberships read through resource.SignInRoles) with the password-disabled check beside it (signin.PasswordsDisabledByTenants over the same reader, through resource.OutsideSignIn)",
			"pkg/config/data.go: dataConfig.staffSettings passes the declaration reader (app.NewTenantDeclarationReader()) and what makes a tenant from an accepted invitation (app.NewTenantMaker(tenants, liveService), over the roster built ahead of the auth) to the staff auth",
			"web/console/src/app/components/ui/new-tenant/ and web/console/src/app/configs/tenants.config.ts: the dialog a person who signed up creates their own tenant through (the create-your-own route) and the configuration of All tenants, the library's list page over the record, from the reference, without the directory-only column and field, which the record does not carry; the library's frame carries the tenant the URL names, the guard on the tenant segment, the refusal page and the place switcher. No Sign-in settings page is placed: that page is where a tenant's directory is set and what it is made the only way in of, and no tenant has one",
			"web/console/src/app/app.menus.ts: the tenant menus ahead of the others, as the reference's: Tenants (All tenants, in the place outside every tenant) and People (the tenant's side of the auth's people), each showing in its place only, and no menu for Sign-in settings, since no tenant has a directory to set there; Feature flags, whose address has no tenant in it, is a page of the place outside every one",
			"ran go generate ./..., which emitted the Tenant resource, the tenant segment pair under /tenants/{tenantID}, the roster constructor NewTenantRoster and the declaration reader NewTenantDeclarationReader",
		},
	}

	tests := []struct {
		name        string
		tenancy     Tenancy
		wantCommand string
	}{
		{name: "a tenant can have a directory", tenancy: Tenancy{Table: "Tenants"}, wantCommand: "impulse add tenancy --tenant-table Tenants"},
		{name: "no tenant has a directory", tenancy: Tenancy{Table: "Tenants", NoDirectories: true}, wantCommand: "impulse add tenancy --tenant-table Tenants --tenant-directories=false"},
		{
			name: "no tenant has a directory, and a person belongs to one tenant", tenancy: Tenancy{Table: "Tenants", OneTenantPerPerson: true, NoDirectories: true},
			wantCommand: "impulse add tenancy --tenant-table Tenants --one-tenant-per-person --tenant-directories=false",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.tenancy.Command(); got != tt.wantCommand {
				t.Errorf("Command() = %q, want %q", got, tt.wantCommand)
			}
			directories := !tt.tenancy.NoDirectories
			a, ch := tenancyOverTheBase(t, tt.tenancy)
			wantNoSignInPageOfItsOwn(t, a)
			for _, rel := range directoryFiles {
				_, err := os.Stat(a.Abs(rel))
				if got := err == nil; got != directories {
					t.Errorf("%s exists = %v, want %v", rel, got, directories)
				}
			}
			held := func(fragments map[string][]string, want bool, what string) {
				t.Helper()
				for rel, texts := range fragments {
					got := read(t, a, rel)
					for _, text := range texts {
						if strings.Contains(got, text) != want {
							t.Errorf("%s holds %q = %v, want %v (%s)", rel, text, !want, want, what)
						}
					}
				}
			}
			held(everywhere, true, "every tenanted application")
			held(directory, directories, "with tenant directories alone")
			held(none, !directories, "without tenant directories alone")
			for _, want := range did[tt.tenancy.NoDirectories] {
				if !slices.Contains(ch.Did, want) {
					t.Errorf("Did lacks %q; got:\n%s", want, strings.Join(ch.Did, "\n"))
				}
			}
			for _, line := range slices.Concat(ch.Did, ch.Skipped) {
				if !directories && (strings.Contains(line, "TenantDirector") || strings.Contains(line, "draws the tenant's Sign-in settings page")) {
					t.Errorf("the change says %q of an application whose tenants have no directories", line)
				}
			}
			if directories {
				return
			}
			// Nothing the step wrote names a tenant's directory by an identifier of the
			// directory's own: the generated reader, the admission, the record, the page.
			for _, rel := range []string{
				"schema/migrations/000003_Tenants.up.sql", "schema/migrations/000003_Tenants.down.sql", "pkg/resources/tenants.go",
				"pkg/auth/staff/staff.go", "pkg/config/data.go", "pkg/config/tenancy.go", "app/tenancy.go", "app/app.go",
				src + "configs/tenants.config.ts", src + "app.menus.ts", src + "app.routes.ts",
			} {
				got := read(t, a, rel)
				for _, word := range []string{"TenantDirector", "DirectoryReader", "SetDirectoryAdmission", "DirectorySeated", "DirectoryOnly", "directoryOnly", "@tenantDirectory", "tenantSignIn"} {
					if strings.Contains(got, word) {
						t.Errorf("%s names %s in an application whose tenants have no directories", rel, word)
					}
				}
			}
		})
	}
}

// TestTenancyApply_overAMethodThroughADirectory applies the step to an application
// whose auth already signs in with any Google account, the application giving the
// roles: the application add tenancy takes with either kind of shape. With tenant
// directories the auth is given the directory reader and the admission, so a tenant can
// take the Workspace its administrator signs in through. Without them it is given
// neither, as an auth that has only a password is: the method stays what it was, a way
// for an invited person to sign in, and the sign-in rule is set all the same.
func TestTenancyApply_overAMethodThroughADirectory(t *testing.T) {
	t.Parallel()

	for _, noDirectories := range []bool{false, true} {
		files := tenancyWithBrowser(t, nil)
		// The data level carries its environment struct, which the method's settings join.
		files["pkg/config/data.go"] = makerConfig + "\n// dataConfig holds the environment every database-opening process reads.\ntype dataConfig struct {\n\t// Project is the cloud project.\n\tProject string `env:\"APP_PROJECT\"`\n}\n"
		files["pkg/auth/staff/staff.go"] = staffPackage(t, "staff.go")
		files["pkg/auth/staff/password.go"] = staffPackage(t, "password.go")
		files["cmd/generate/resourcegenerator/main.go"] = boundProgram(t)
		files["app/app.go"] = strings.ReplaceAll(skeletonFile(t, "app/app.go"), skeletonModule, "example.com/acme/beacon")
		a := beacon(t, files)
		google := Method{Auth: "staff", Spec: authpkg.Method{Kind: app.MethodGoogle, Name: "google", Authority: AuthorityApplication, Slot: authpkg.SlotAny}}
		if _, err := google.Apply(t.Context(), a, &fakeExec{}); err != nil {
			t.Fatalf("no directories %v: adding the Google method: Apply() error = %v", noDirectories, err)
		}
		a, err := app.Discover(a.Root)
		if err != nil {
			t.Fatalf("app.Discover() error = %v", err)
		}
		if _, err := (Tenancy{Table: "Tenants", NoDirectories: noDirectories}).Apply(t.Context(), a, &fakeExec{}); err != nil {
			t.Fatalf("no directories %v: Apply() error = %v", noDirectories, err)
		}

		auth := read(t, a, "pkg/auth/staff/staff.go")
		// The method is registered as it was, and the sign-in rule and the password-disabled
		// check are set, in both.
		for _, want := range []string{
			"\thandlers.google = registerGoogle(",
			"\tauthSession.SetSignInPolicy(signin.TenantPolicy(resource.SignInRoles(accessClient.UserManager()), settings.TenantDeclarations, signin.AdmitWithoutTenant())",
			"\tauthSession.SetPasswordDisabledCheck(signin.PasswordsDisabledByTenants(accessClient.UserManager(), resource.OutsideSignIn(db, settings.TenantDeclarations)))\n",
		} {
			if !strings.Contains(auth, want) {
				t.Errorf("no directories %v: pkg/auth/staff/staff.go lacks %q:\n%s", noDirectories, want, auth)
			}
		}
		// The directory reader and the admission are the tenant directories' alone.
		for _, text := range []string{
			"\tTenantDirectories signin.DirectoryReader[*cloudspanner.ReadWriteTransaction]\n",
			"\tauthSession.SetDirectoryAdmission(settings.TenantDirectories, identityClient.DirectorySeated())\n",
		} {
			if got := strings.Contains(auth, text); got == noDirectories {
				t.Errorf("no directories %v: pkg/auth/staff/staff.go holds %q = %v", noDirectories, text, got)
			}
		}
		if _, err := os.Stat(a.Abs("pkg/resources/tenant_directories.go")); (err == nil) == noDirectories {
			t.Errorf("no directories %v: pkg/resources/tenant_directories.go exists = %v", noDirectories, err == nil)
		}
		if !noDirectories {
			continue
		}
		for _, rel := range []string{"pkg/auth/staff/staff.go", "pkg/auth/staff/google.go", "pkg/config/data.go"} {
			got := read(t, a, rel)
			for _, word := range []string{"TenantDirectories", "DirectoryReader", "SetDirectoryAdmission", "DirectorySeated"} {
				if strings.Contains(got, word) {
					t.Errorf("%s names %s in an application whose tenants have no directories", rel, word)
				}
			}
		}
	}
}

// TestTenancyApply_noAdmissionToMiss applies the step to an auth whose New builds no
// identity client the tool reads. Where a tenant can have a directory, the change says
// the directory admission was not set and how to set it. Where no tenant has one there
// is no admission to set, and the change says nothing of one.
func TestTenancyApply_noAdmissionToMiss(t *testing.T) {
	t.Parallel()

	built := "\tidentityClient, err := staffidentity.NewClient("
	staff := staffPackage(t, "staff.go")
	if !strings.Contains(staff, built) {
		t.Fatal("the skeleton's staff auth no longer builds its identity client as this test rewrites it")
	}
	const missed = "pkg/auth/staff/staff.go: New builds no identity client (identityClient, err := staffidentity.NewClient(...), the client of the auth's generated management), so the directory admission was not set"
	for _, noDirectories := range []bool{false, true} {
		files := tenancyFiles()
		files["pkg/config/data.go"] = makerConfig
		// The client goes by another name, so the tool reads none.
		files["pkg/auth/staff/staff.go"] = strings.Replace(staff, built, "\tmanagement, err := staffidentity.NewClient(", 1)
		files["cmd/generate/resourcegenerator/main.go"] = boundProgram(t)
		a := beacon(t, files)
		ch, err := Tenancy{Table: "Tenants", NoDirectories: noDirectories}.Apply(t.Context(), a, &fakeExec{})
		if err != nil {
			t.Fatalf("no directories %v: Apply() error = %v", noDirectories, err)
		}
		said := slices.ContainsFunc(ch.Skipped, func(line string) bool {
			return strings.HasPrefix(line, missed)
		})
		if said == noDirectories {
			t.Errorf("no directories %v: Skipped says the directory admission was not set = %v; got:\n%s", noDirectories, said, strings.Join(ch.Skipped, "\n"))
		}
		for _, line := range slices.Concat(ch.Did, ch.Skipped) {
			if noDirectories && (strings.Contains(line, "SetDirectoryAdmission") || strings.Contains(line, "directory admission")) {
				t.Errorf("the change says %q of an application whose tenants have no directories", line)
			}
		}
		// The sign-in rule is set on such an auth all the same.
		if got := read(t, a, "pkg/auth/staff/staff.go"); !strings.Contains(got, "SetSignInPolicy(signin.TenantPolicy(") {
			t.Errorf("no directories %v: pkg/auth/staff/staff.go sets no sign-in rule:\n%s", noDirectories, got)
		}
	}
}

// TestTenancyBrowserEdited_noDirectories pins what the step leaves alone in the browser
// of an application whose tenants have no directories. Such an application has no
// tenant Sign-in page for the step to know its own work by, so it knows it by the
// configuration of the All tenants page: where that file exists the tenant pages are
// not laid in again, and a menu file that places the page already is not given the
// menus twice.
func TestTenancyBrowserEdited_noDirectories(t *testing.T) {
	t.Parallel()

	const (
		src    = "web/console/src/app/"
		config = "// the application's own configuration of All tenants\n"
		placed = "export const appMenus = [resourcePage(tenantsConfig, resourceMeta)];\n"
	)
	a := beacon(t, tenancyWithBrowser(t, func(files map[string]string) {
		files[src+"configs/tenants.config.ts"] = config
		files[src+"app.menus.ts"] = placed
	}))
	ch, err := Tenancy{Table: "Tenants", NoDirectories: true}.Apply(t.Context(), a, &fakeExec{})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if got := read(t, a, src+"configs/tenants.config.ts"); got != config {
		t.Errorf("the configuration of All tenants = %q, want it left", got)
	}
	if got := read(t, a, src+"app.menus.ts"); got != placed {
		t.Errorf("menus = %q, want them left", got)
	}
	if _, err := os.Stat(a.Abs(src + "components/ui/new-tenant")); err == nil {
		t.Error("the new-tenant dialog was laid in beside a configuration the step left alone")
	}
	skipped := strings.Join(ch.Skipped, "\n")
	for _, want := range []string{
		src + "configs/tenants.config.ts already exists and was left alone",
		src + "app.menus.ts places the All tenants page already, so the tenant menus were left as they are",
	} {
		if !strings.Contains(skipped, want) {
			t.Errorf("Skipped lacks %q:\n%s", want, skipped)
		}
	}

	// Where a tenant can have a directory the step knows its work by the tenant's
	// Sign-in page, as it always has, and a menu file that places All tenants alone
	// still gains the menus.
	b := beacon(t, tenancyWithBrowser(t, nil))
	menus := read(t, b, src+"app.menus.ts")
	if err := os.WriteFile(b.Abs(src+"app.menus.ts"), []byte("// tenantsConfig is named here\n"+menus), 0o644); err != nil {
		t.Fatal(err)
	}
	ch, err = Tenancy{Table: "Tenants"}.Apply(t.Context(), b, &fakeExec{})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if got := read(t, b, src+"app.menus.ts"); !strings.Contains(got, "tenantSignInPage") {
		t.Errorf("with tenant directories, a menu file that names tenantsConfig was not given the tenant menus:\n%s", got)
	}
	if skipped := strings.Join(ch.Skipped, "\n"); strings.Contains(skipped, "places the All tenants page already") {
		t.Errorf("with tenant directories, Skipped says the All tenants page is placed:\n%s", skipped)
	}
}

// TestTenancyMeaning_tenantDirectories holds the handoff brief to the choice. With
// tenant directories it explains the declaration, its grants and its page. Without
// them it says no tenant has a directory, that a directory method is a way for an
// invited person to sign in and nothing more, what the integration tests prove of that,
// and that laying the directories in afterward is the author's act; it names no grant,
// page or route of a directory as work left to do.
func TestTenancyMeaning_tenantDirectories(t *testing.T) {
	t.Parallel()

	with := []string{
		"Who may enter a tenant is the record's declaration, data of the application rather than deploy configuration:",
		"The declaration. Grants: the tenant's administrator role",
		"`tenantSignInPage`",
		"its directory and its rows with it, in one request, whether it is directory only or not, and its directory is then free for another tenant",
	}
	without := []string{
		"No tenant has a directory in this application (`--tenant-directories=false`): the resource package declares no directories record and `Tenant` carries no directory-only switch, so nothing is taken when a tenant is made, no route sets a tenant's directory, no tenant is opened to a directory or made directory only, and the browser has no tenant Sign-in page. A tenant's people come by invitation alone.",
		"is a way for an invited person to sign in and nothing more: a person from an organization's directory who has no account and no invitation joins no tenant, whichever methods the application has or gets (refused as a stranger while the method is invite only, as every method starts; where an administrator opened it to sign-up, given an account that holds no tenant), and `impulse audit` reads the application as an invite-only shape.",
		"Giving the application tenant directories later is the author's act and no command's: the directories record (`@tenantDirectory`) and its table with its two unique indexes, the directory-only column on `Tenant` (`@directoryOnly`), each auth's directory reader and admission (`SetDirectoryAdmission`), the grants on the generated methods and the tenant's Sign-in page, as an application made with tenant directories has them (the tenanted skeleton); no `impulse add method` does it.",
		"where the auth has a sign-in method through an organization's directory, an invited person accepts through it and is seated, a person of the same organization with no invitation is refused as a stranger while the method is invite only, with no account made, and no route of the API sets a tenant's directory.",
		"and no menu for Sign-in settings, since no tenant has a directory to set there, each showing in its place only;",
		"Sign-up. No tenant declares who may enter it, so there are no grants to give on a directories record and no Sign-in page to place.",
		"that person's first sign-in makes the tenant and gives them the roles `TenantAdministratorRoles()` names, in one transaction, however they signed in.",
	}
	everywhere := []string{
		"Tenancy is data. The Tenants table is the domain universe",
		"Left to wire, in this order:\n\n1. The bootstrap and the deployment.",
		"How a tenant comes to exist. Nobody creates one directly: the record's create answers 400 with a sentence saying so.",
		"`tenant-delete-rule`",
		"A table that reaches its tenant through another row (`// @domain(via: ...)` on the column that names that row) carries `ON DELETE CASCADE` on that column's foreign key too, as every link of its way to the tenant table does",
	}
	for _, noDirectories := range []bool{false, true} {
		meaning := Tenancy{Table: "Tenants", NoDirectories: noDirectories}.Meaning()
		for _, text := range everywhere {
			if !strings.Contains(meaning, text) {
				t.Errorf("no directories %v: the brief lacks %q", noDirectories, text)
			}
		}
		for _, text := range with {
			if got := strings.Contains(meaning, text); got == noDirectories {
				t.Errorf("no directories %v: the brief holds %q = %v", noDirectories, text, got)
			}
		}
		for _, text := range without {
			if got := strings.Contains(meaning, text); got != noDirectories {
				t.Errorf("no directories %v: the brief holds %q = %v", noDirectories, text, got)
			}
		}
		if noDirectories {
			for _, word := range []string{"TenantDirectories", "DirectoryOnly`", "directory only (", "tenantSignInPage", "OpenTenantToDirectory", "MakeTenantDirectoryOnly"} {
				if strings.Contains(meaning, word) {
					t.Errorf("the brief of an application whose tenants have no directories names %s", word)
				}
			}
		}
	}

	// The one-tenant rule names the ways a role is given, and a directory's default
	// role is one only where a tenant can have a directory.
	const byDirectory = "refused however it would be given, by hand, by an invitation sent or accepted, or by a directory's default role, with a refusal"
	const byHand = "refused however it would be given, by hand or by an invitation sent or accepted, with a refusal"
	for _, noDirectories := range []bool{false, true} {
		meaning := Tenancy{Table: "Tenants", OneTenantPerPerson: true, NoDirectories: noDirectories}.Meaning()
		want, not := byDirectory, byHand
		if noDirectories {
			want, not = byHand, byDirectory
		}
		if !strings.Contains(meaning, want) || strings.Contains(meaning, not) {
			t.Errorf("no directories %v: the one-tenant item does not say %q", noDirectories, want)
		}
		if noDirectories && strings.Contains(meaning, "default role") {
			t.Error("the brief of an application whose tenants have no directories speaks of a directory's default role")
		}
	}
}

// TestTenancyBrowser_noDirectoriesInTheRecordsWords writes the browser half for a
// record of two words in an application whose tenants have no directories: what the
// reference says of a tenant's directory is cut from the reference's own text before
// the record's words go in, so the cut holds whatever the record is called.
func TestTenancyBrowser_noDirectoriesInTheRecordsWords(t *testing.T) {
	t.Parallel()

	a := beacon(t, tenancyWithBrowser(t, nil))
	if _, err := (Tenancy{Table: "ServiceAreas", NoDirectories: true}).Apply(t.Context(), a, &fakeExec{}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	const src = "web/console/src/app/"
	wantContains(t, a, src+"configs/tenants.config.ts", []string{
		"listColumns: [{ id: ServiceAreas.fieldName.name }],\n",
		"changes\n// its name. A ",
	}, []string{"directoryOnly", "Directory only", "directories only"})
	wantContains(t, a, src+"app.menus.ts", []string{"      label: 'Service areas',", "resourcePage(tenantsConfig, resourceMeta)"}, []string{"tenantSignIn", "label: 'Service area',"})
	wantContains(t, a, src+"app.routes.ts", []string{"resourceRoutes(tenantsConfig, resourceMeta)"}, []string{"tenantSignIn", "directory-only flag"})
	wantNoSignInPageOfItsOwn(t, a)
}

// Test_forTenants holds the cuts to the reference: every stretch an application whose
// tenants have no directories is written without is in the reference's file exactly
// once, so the cut text is the reference's and a change to the reference is refused
// here by name, never written half cut. An application with tenant directories takes
// the reference as it is.
func Test_forTenants(t *testing.T) {
	t.Parallel()

	// Each text is read from the skeleton the command takes it from: the configuration
	// of the All tenants page from the one its table of copied files names, the menu
	// file and the routes from the ones their edits read.
	configFrom := ""
	for _, f := range tenantWebFiles {
		if f.rel == tenantsConfigPath {
			configFrom = f.candidate
		}
	}
	if configFrom == "" {
		t.Fatalf("the files the command copies no longer name %s", tenantsConfigPath)
	}
	for _, tt := range []struct {
		rel, candidate string
	}{
		{rel: tenantsConfigPath, candidate: configFrom},
		{rel: appMenusFile, candidate: tenancyAdminCandidate},
		{rel: appRoutesFile, candidate: TenancyReferenceCandidate},
	} {
		reference, err := referenceText(tt.candidate, tt.rel)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := (Tenancy{}).forTenants(tt.rel, reference); err != nil || got != reference {
			t.Errorf("%s: forTenants() with tenant directories changed the reference (error %v)", tt.rel, err)
		}
		got, err := Tenancy{NoDirectories: true}.forTenants(tt.rel, reference)
		if err != nil {
			t.Fatalf("%s: forTenants() error = %v", tt.rel, err)
		}
		cuts := 0
		for _, cut := range withoutDirectories {
			if cut.rel != tt.rel {
				continue
			}
			cuts++
			if strings.Contains(got, cut.has) {
				t.Errorf("%s: forTenants() left %q", tt.rel, cut.has)
			}
			// A reference that lost the stretch, or carries it twice, is refused by name.
			for _, altered := range []string{strings.Replace(reference, cut.has, "", 1), reference + cut.has} {
				_, err := Tenancy{NoDirectories: true}.forTenants(tt.rel, altered)
				if want := "the reference's " + tt.rel + " no longer carries"; err == nil || !strings.Contains(err.Error(), want) {
					t.Errorf("%s: forTenants() over a reference without %q once, error = %v, want it to say %q", tt.rel, cut.has, err, want)
				}
			}
		}
		if cuts == 0 {
			t.Errorf("%s: no stretch is cut for an application whose tenants have no directories", tt.rel)
		}
		for _, word := range []string{"directoryOnly", "tenantSignIn", "sign-in.routes"} {
			if strings.Contains(got, word) {
				t.Errorf("%s: forTenants() left %q in the text", tt.rel, word)
			}
		}
	}
}

// Test_tenantPolicy_directoriesAreTheApplications holds the question an auth's package
// is rendered by, whether its tenants can have directories, to the application and not
// to one site. In the sites layout the portal declares the tenant record the console
// serves and no directories record of its own, and an auth bound there carries the
// directory reader and sets the directory admission all the same, as the console's
// does. Only an application in which no site declares a directories record has none.
func Test_tenantPolicy_directoriesAreTheApplications(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// without is a file of the rendered sites skeleton the application does not have.
		without string
		want    bool
	}{
		{name: "the console declares the directories record, and the portal none", want: true},
		{name: "no site declares a directories record", without: "apps/console/pkg/resources/tenant_directories.go"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := filepath.Join(t.TempDir(), "sites")
			if _, err := skeleton.Render(&skeleton.Options{Version: "v0.1.0", WebVersion: "0.1.0", Candidate: "sites", Dir: dir, ModulePath: "example.com/acme/sites"}); err != nil {
				t.Fatalf("skeleton.Render() error = %v", err)
			}
			if tt.without != "" {
				if err := os.Remove(filepath.Join(dir, filepath.FromSlash(tt.without))); err != nil {
					t.Fatal(err)
				}
			}
			a, err := app.Discover(dir)
			if err != nil {
				t.Fatalf("app.Discover() error = %v", err)
			}
			profile := a.Profile()
			sites := map[string]*app.Site{}
			for i := range profile.Sites {
				sites[profile.Sites[i].Name] = &profile.Sites[i]
			}
			portal, console := sites["portal"], sites["console"]
			if portal == nil || console == nil || !portal.Tenanted() || !console.Tenanted() {
				t.Fatalf("the sites skeleton's portal and console are not read as tenanted sites: %v", profile.Sites)
			}
			if portal.Directories != nil {
				t.Fatal("the portal's package declares a directories record: this test needs a site that declares the record another serves")
			}
			for name, site := range sites {
				policy := tenantPolicy(a, site, false)
				if policy == nil || policy.Directories != tt.want {
					t.Errorf("tenantPolicy() for the %s site = %+v, want Directories %v", name, policy, tt.want)
				}
			}
		})
	}
}
