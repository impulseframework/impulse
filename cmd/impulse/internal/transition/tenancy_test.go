package transition

import (
	"errors"
	"io/fs"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton"
)

const beaconConfig = `package config

import (
	"context"

	cloudspanner "cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access"
)

// DataConfiguration is the second level.
type DataConfiguration struct {
	spannerClient *cloudspanner.Client
	access        *access.Client
}

// NewDataConfiguration opens the clients.
func NewDataConfiguration(ctx context.Context) (*DataConfiguration, error) {
	spannerClient, accessClient, err := open(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "open()")
	}

	return &DataConfiguration{
		spannerClient: spannerClient,
		access:        accessClient,
	}, nil
}
`

const beaconApp = `package app

// Configurer carries the dependencies for an App.
type Configurer interface {
	Access() string
	ConsoleDist() string
}

// App implements the handlers.
type App struct {
	access      string
	consoleDist string
}

// New constructs an App.
func New(cfg Configurer) *App {
	return &App{
		access:      cfg.Access(),
		consoleDist: cfg.ConsoleDist(),
	}
}
`

// liveConfig is the base's data level: a resource client and the live service the roster
// is handed as its signals.
const liveConfig = `package config

import (
	"context"

	cloudspanner "cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/resource"
	livefirestore "github.com/impulseframework/impulse/resource/live/firestore"
)

// DataConfiguration is the second level.
type DataConfiguration struct {
	spannerClient  *cloudspanner.Client
	resourceClient *resource.SpannerClient
	live           *livefirestore.Service
}

// NewDataConfiguration opens the clients.
func NewDataConfiguration(ctx context.Context) (*DataConfiguration, error) {
	spannerClient, liveService, err := open(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "open()")
	}

	return &DataConfiguration{
		spannerClient:  spannerClient,
		resourceClient: resource.NewSpannerClient(spannerClient),
		live:           liveService,
	}, nil
}
`

// permissionsApp is the base's App: its UserPermissions passes no roster, since an
// untenanted application lists no domain.
const permissionsApp = `package app

import (
	"net/http"

	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/resource"
)

// Configurer carries the dependencies for an App.
type Configurer interface {
	Access() access.Controller
	ConsoleDist() string
}

// App implements the handlers.
type App struct {
	access      access.Controller
	consoleDist string
	collection  *resource.GeneratedCollection
}

// New constructs an App.
func New(cfg Configurer) *App {
	return &App{
		access:      cfg.Access(),
		consoleDist: cfg.ConsoleDist(),
	}
}

// UserPermissions returns the permission checker for a request.
func (a *App) UserPermissions(r *http.Request) resource.UserPermissions {
	return resource.SessionPermissions(r.Context(), a.access.ForUser, a.access.ForRole, nil, nil, a.collection)
}
`

func tenancyFiles() map[string]string {
	return map[string]string{
		"pkg/config/data.go": beaconConfig,
		"app/app.go":         beaconApp,
		"schema/migrations/000001_Sessions.up.sql":   "CREATE TABLE Sessions (Id STRING(36) NOT NULL) PRIMARY KEY (Id);\n",
		"schema/migrations/000001_Sessions.down.sql": "DROP TABLE Sessions;\n",
		"schema/migrations/000002_Users.up.sql":      "CREATE TABLE SessionUsers (Id STRING(36) NOT NULL) PRIMARY KEY (Id);\n",
		"schema/migrations/000002_Users.down.sql":    "DROP TABLE SessionUsers;\n",
		"web/console/src/app/core/api/api.ts":        "export const api = 1;\n",
	}
}

func TestTenancyValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		table   string
		wantErr string
	}{
		{name: "the default table", table: "Tenants"},
		{name: "a two-word table", table: "BusinessUnits"},
		{name: "a singular table", table: "Tenant", wantErr: `table "Tenant"`},
		{name: "a lower-case table", table: "tenants", wantErr: `table "tenants"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := Tenancy{Table: tt.table}.Validate(beacon(t, tenancyFiles()))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}

				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Validate() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestTenancyNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		table, segment, record string
	}{
		{"Tenants", "tenants", "Tenant"},
		{"BusinessUnits", "business-units", "BusinessUnit"},
		{"Companies", "companies", "Company"},
		{"Addresses", "addresses", "Address"},
		{"Branches", "branches", "Branch"},
	}
	for _, tt := range tests {
		t.Run(tt.table, func(t *testing.T) {
			t.Parallel()

			tn := Tenancy{Table: tt.table}
			if got := tn.Segment(); got != tt.segment {
				t.Errorf("Segment() = %q, want %q", got, tt.segment)
			}
			if got := tn.Record(); got != tt.record {
				t.Errorf("Record() = %q, want %q", got, tt.record)
			}
		})
	}
}

func TestTenancyApply(t *testing.T) {
	t.Parallel()

	const program = "cmd/generate/resourcegenerator/main.go"
	wantProgram := strings.Replace(beaconProgram,
		"\t\tgeneration.GenerateRoutes(\"pkg/router\", \"api\"),\n",
		"\t\tgeneration.GenerateRoutes(\"pkg/router\", \"api\"),\n\t\tgeneration.WithConcealedDomains(),\n", 1)
	// The data level gains the roster field (and the resource import its type needs) and
	// the start where the configuration is built.
	withRosterField := func(config string) string {
		config = strings.Replace(config, "\t\"github.com/impulseframework/impulse/access\"\n", "\t\"github.com/impulseframework/impulse/access\"\n\t\"github.com/impulseframework/impulse/resource\"\n", 1)

		return strings.Replace(config, "\taccess        *access.Client\n}", "\taccess        *access.Client\n\ttenants       *resource.TenantRoster\n}", 1)
	}
	wantConfig := strings.Replace(withRosterField(beaconConfig),
		"\treturn &DataConfiguration{\n\t\tspannerClient: spannerClient,\n\t\taccess:        accessClient,\n\t}, nil\n",
		"\tconf := &DataConfiguration{\n\t\tspannerClient: spannerClient,\n\t\taccess:        accessClient,\n\t}\n\tif err := conf.startTenants(ctx); err != nil {\n\t\treturn nil, errors.Wrap(err, \"startTenants()\")\n\t}\n\n\treturn conf, nil\n", 1)
	wantApp := strings.Replace(beaconApp, "package app\n\n", "package app\n\nimport \"github.com/impulseframework/impulse/resource\"\n\n", 1)
	wantApp = strings.Replace(wantApp, "\tConsoleDist() string\n}", "\tConsoleDist() string\n\tTenancyConfigurer\n}", 1)
	wantApp = strings.Replace(wantApp, "\taccess      string\n\tconsoleDist string\n}", "\taccess       string\n\tconsoleDist  string\n\ttenants      *resource.TenantRoster\n\tdeclarations resource.TenantDeclarations\n}", 1)
	wantApp = strings.Replace(wantApp, "\t\taccess:      cfg.Access(),\n\t\tconsoleDist: cfg.ConsoleDist(),\n\t}", "\t\taccess:       cfg.Access(),\n\t\tconsoleDist:  cfg.ConsoleDist(),\n\t\ttenants:      cfg.TenantRoster(),\n\t\tdeclarations: cfg.TenantDeclarations(),\n\t}", 1)
	// The fixtures' data level holds no live service and their App composes no session
	// permissions, so the two edits that need them are recorded as the agent's.
	noSignals := "pkg/config/tenancy.go: DataConfiguration holds no *firestore.Service field (resource/live/firestore), so the roster is built without resource.WithTenantSignals and reloads at its backstop alone; pass the live service the level opens, so a tenant made on another instance reaches this one at once"
	noMenus := "web/console/src/app/app.menus.ts does not exist, so the tenant menus were not written: the Tenants menu (All tenants), People and Tenant (Sign-in settings)"
	noPermissions := "app/app.go: UserPermissions passes no nil roster to resource.SessionPermissions, so the roster's Domains and the declarations were not handed to it; pass a.tenants.Domains and a.declarations where the session permissions are composed, so a login's tenant list is the roster filtered by its footholds and by what each tenant declares"
	// The fixtures construct no auth, so the sign-in rule is the agent's.
	noAuth := "no auth package constructs a session.Auth, so no sign-in rule was set; where one is, set signin.TenantPolicy(resource.SignInRoles(<engine>.UserManager()), <handlers>.NewTenantDeclarationReader()) on the session with SetSignInPolicy before the first request, so a sign-in is admitted by what the person's tenants declare"
	// Without an auth whose roles the application assigns, the roles of a person who
	// creates a tenant of their own are the agent's too.
	noSeatAuth := "no auth whose roles the application assigns constructs a session.Auth the scan reads, so the roles a person who creates a tenant of their own holds in it were not exposed on the app; the generated create-your-own route of the Tenant record asks the handlers for TenantAdministratorRoles() []accesstypes.Role (the domain roles that person holds in the new tenant, the auth's AdministratorRoles held in every tenant) beside the auth's identity client (<Auth>Identity()); add them where the App is declared"

	tests := []struct {
		name        string
		files       map[string]string
		wantDid     []string
		wantSkipped []string
		check       func(t *testing.T, a *app.App)
	}{
		{
			name:  "the base's seams without a live service or session permissions",
			files: tenancyFiles(),
			wantDid: []string{
				"cmd/generate/resourcegenerator/main.go: added WithConcealedDomains(); the tenant segment /tenants derives from the Tenant record",
				"schema/migrations/000003_Tenants.up.sql and .down.sql: the Tenants table (a generated id as its primary key, a unique Name, and DirectoryOnly) and the TenantDirectories table (a tenant's directory with its DefaultSeat, held to one for a tenant and to one tenant by its two unique indexes)",
				"schema/devseed/000001_dev_tenants.up.sql and .down.sql: the development tenants North and South under fixed ids, a data migration for the bootstrap to apply before the logins",
				"pkg/resources/tenants.go: the Tenant resource struct, global, keyed by a generated id, listed in name order (@order(Name asc)), with its declaration's flag (DirectoryOnly) and change tracking",
				"pkg/resources/tenant_directories.go: the TenantDirectory resource struct, a tenant's directory, set from a sign-in and never typed, tenant-scoped under /tenants/{tenantID}/tenant-directories",
				"pkg/config/tenancy.go: the tenant roster (startTenants builds it with app.NewTenantRoster and starts it), TenantRoster() and TenantDeclarations() on DataConfiguration; pkg/config/data.go gained the field and the start",
				"app/tenancy.go: TenancyConfigurer (embedded in Configurer, asking TenantRoster() and TenantDeclarations()) and App.TenantRoster(); app/app.go gained the fields and their assignments",
				"web/console/src/app/components/ui/sign-in/, components/ui/new-tenant/ and configs/tenants.config.ts: the tenant's sign-in page (who may enter the tenant: its declared directories and whether they are the only way in, with its route and its page for the menu file in sign-in.routes.ts), the dialog a person who signed up creates their own tenant through (the create-your-own route) and the configuration of All tenants, the library's list page over the record, from the reference; the library's frame carries the tenant the URL names, the guard on the tenant segment, the refusal page and the place switcher",
				"ran go generate ./..., which emitted the Tenant and TenantDirectory resources, the tenant segment pair under /tenants/{tenantID}, the roster constructor NewTenantRoster and the declaration reader NewTenantDeclarationReader",
			},
			wantSkipped: []string{noSignals, noAuth, noSeatAuth, noPermissions, noMenus},
			check: func(t *testing.T, a *app.App) {
				t.Helper()
				if diff := cmp.Diff(wantConfig, read(t, a, "pkg/config/data.go")); diff != "" {
					t.Errorf("data.go mismatch (-want +got):\n%s", diff)
				}
				if diff := cmp.Diff(wantApp, read(t, a, "app/app.go")); diff != "" {
					t.Errorf("app.go mismatch (-want +got):\n%s", diff)
				}
				for rel, want := range map[string]string{
					"schema/migrations/000003_Tenants.up.sql":                                     "CREATE TABLE TenantDirectories (",
					"schema/migrations/000003_Tenants.down.sql":                                   "DROP INDEX TenantDirectoriesByMethodConnection;\nDROP INDEX TenantDirectoriesByTenantId;\nDROP TABLE TenantDirectories;\nDROP INDEX TenantsByName;\nDROP TABLE Tenants;",
					"schema/devseed/000001_dev_tenants.up.sql":                                    "INSERT INTO Tenants (Id, Name) VALUES ('11111111-1111-4111-8111-111111111111', 'North');",
					"pkg/resources/tenants.go":                                                    "package resources\n\nimport (\n\t\"github.com/impulseframework/impulse\"\n\t\"github.com/impulseframework/impulse/resource\"\n)\n\ntype (\n\t// Tenant is the tenant record (@tenant)",
					"pkg/resources/tenant_directories.go":                                         "\t// @resource\n\t// @permissionScope(domain)\n\t// @tenantDirectory\n\t// @order(Method asc, Connection asc)\n\tTenantDirectory struct {\n\t\tID impulse.UUID `spanner:\"Id\"`\n\t\t// @domain\n\t\tTenantID impulse.UUID `spanner:\"TenantId\"`\n\t\t// @directoryMethod\n\t\tMethod string `spanner:\"Method\"`\n\t\t// @directoryConnection\n\t\tConnection string `spanner:\"Connection\"`\n\t\t// @directoryDefaultSeat\n\t\tDefaultSeat *string `spanner:\"DefaultSeat\"`\n\t}",
					"web/console/src/app/components/ui/sign-in/tenant-sign-in.component.ts":       "export class TenantSignInComponent",
					"web/console/src/app/components/ui/sign-in/sign-in.routes.ts":                 "export const tenantSignInRoute",
					"pkg/config/tenancy.go":                                                       "package config\n",
					"app/tenancy.go":                                                              "package app\n",
					"web/console/src/app/components/ui/new-tenant/new-tenant-dialog.component.ts": "export function openNewTenantDialog(",
					"web/console/src/app/configs/tenants.config.ts":                               "export const tenantsConfig = rootConfig({",
				} {
					if got := read(t, a, rel); !strings.Contains(got, want) {
						t.Errorf("%s lacks %q:\n%s", rel, want, got)
					}
				}
				config := read(t, a, "pkg/config/tenancy.go")
				for _, want := range []string{"c.tenants = app.NewTenantRoster(resource.NewSpannerClient(c.spannerClient))", "// No live service field was found on DataConfiguration", "c.tenants.Start(ctx)", "func (c *DataConfiguration) TenantRoster() *resource.TenantRoster"} {
					if !strings.Contains(config, want) {
						t.Errorf("tenancy.go lacks %q:\n%s", want, config)
					}
				}
				resources := read(t, a, "pkg/resources/tenants.go")
				for _, want := range []string{"\t// @resource\n\t// @tenant\n\t// @order(Name asc)\n\tTenant struct {", "\t\t// @directoryOnly\n\t\tDirectoryOnly bool `spanner:\"DirectoryOnly\"`\n", "func (Tenant) Config() resource.Config {\n\treturn resource.Config{TrackChanges: true}\n}"} {
					if !strings.Contains(resources, want) {
						t.Errorf("tenants.go lacks %q:\n%s", want, resources)
					}
				}
				if config := read(t, a, "pkg/config/tenancy.go"); !strings.Contains(config, "func (c *DataConfiguration) TenantDeclarations() resource.TenantDeclarations {\n\treturn c.tenants.Declarations\n}") {
					t.Errorf("tenancy.go lacks the declarations accessor:\n%s", config)
				}
				if application := read(t, a, "app/tenancy.go"); !strings.Contains(application, "\tTenantDeclarations() resource.TenantDeclarations\n}") {
					t.Errorf("app/tenancy.go lacks the declarations in TenancyConfigurer:\n%s", application)
				}
			},
		},
		{
			name: "the skeleton's data level and app",
			files: func() map[string]string {
				files := tenancyFiles()
				files["pkg/config/data.go"] = liveConfig
				files["app/app.go"] = permissionsApp

				return files
			}(),
			wantDid: []string{
				"cmd/generate/resourcegenerator/main.go: added WithConcealedDomains(); the tenant segment /tenants derives from the Tenant record",
				"schema/migrations/000003_Tenants.up.sql and .down.sql: the Tenants table (a generated id as its primary key, a unique Name, and DirectoryOnly) and the TenantDirectories table (a tenant's directory with its DefaultSeat, held to one for a tenant and to one tenant by its two unique indexes)",
				"schema/devseed/000001_dev_tenants.up.sql and .down.sql: the development tenants North and South under fixed ids, a data migration for the bootstrap to apply before the logins",
				"pkg/resources/tenants.go: the Tenant resource struct, global, keyed by a generated id, listed in name order (@order(Name asc)), with its declaration's flag (DirectoryOnly) and change tracking",
				"pkg/resources/tenant_directories.go: the TenantDirectory resource struct, a tenant's directory, set from a sign-in and never typed, tenant-scoped under /tenants/{tenantID}/tenant-directories",
				"pkg/config/tenancy.go: the tenant roster (startTenants builds it with app.NewTenantRoster and starts it), TenantRoster() and TenantDeclarations() on DataConfiguration; pkg/config/data.go gained the field and the start",
				"app/tenancy.go: TenancyConfigurer (embedded in Configurer, asking TenantRoster() and TenantDeclarations()) and App.TenantRoster(); app/app.go gained the fields and their assignments",
				"app/app.go: UserPermissions passes the roster's Domains and the declarations to resource.SessionPermissions",
				"web/console/src/app/components/ui/sign-in/, components/ui/new-tenant/ and configs/tenants.config.ts: the tenant's sign-in page (who may enter the tenant: its declared directories and whether they are the only way in, with its route and its page for the menu file in sign-in.routes.ts), the dialog a person who signed up creates their own tenant through (the create-your-own route) and the configuration of All tenants, the library's list page over the record, from the reference; the library's frame carries the tenant the URL names, the guard on the tenant segment, the refusal page and the place switcher",
				"ran go generate ./..., which emitted the Tenant and TenantDirectory resources, the tenant segment pair under /tenants/{tenantID}, the roster constructor NewTenantRoster and the declaration reader NewTenantDeclarationReader",
			},
			wantSkipped: []string{noAuth, noSeatAuth, noMenus},
			check: func(t *testing.T, a *app.App) {
				t.Helper()
				config := read(t, a, "pkg/config/tenancy.go")
				if want := "c.tenants = app.NewTenantRoster(c.resourceClient, resource.WithTenantSignals(c.live))"; !strings.Contains(config, want) {
					t.Errorf("tenancy.go lacks %q:\n%s", want, config)
				}
				data := read(t, a, "pkg/config/data.go")
				for _, want := range []string{"\ttenants        *resource.TenantRoster\n", "\tif err := conf.startTenants(ctx); err != nil {\n\t\treturn nil, errors.Wrap(err, \"startTenants()\")\n\t}\n"} {
					if !strings.Contains(data, want) {
						t.Errorf("data.go lacks %q:\n%s", want, data)
					}
				}
				application := read(t, a, "app/app.go")
				for _, want := range []string{"\tTenancyConfigurer\n}", "\ttenants      *resource.TenantRoster\n", "\tdeclarations resource.TenantDeclarations\n", "\t\ttenants:      cfg.TenantRoster(),\n", "\t\tdeclarations: cfg.TenantDeclarations(),\n", "resource.SessionPermissions(r.Context(), a.access.ForUser, a.access.ForRole, a.tenants.Domains, a.declarations, a.collection)"} {
					if !strings.Contains(application, want) {
						t.Errorf("app.go lacks %q:\n%s", want, application)
					}
				}
			},
		},
		{
			name: "an application without the skeleton's seams",
			files: map[string]string{
				"schema/migrations/000001_Sessions.up.sql":   "CREATE TABLE Sessions (Id STRING(36) NOT NULL) PRIMARY KEY (Id);\n",
				"schema/migrations/000001_Sessions.down.sql": "DROP TABLE Sessions;\n",
				"schema/devseed/000001_seed.up.sql":          "INSERT INTO Sessions (Id) VALUES ('x');\n",
			},
			wantDid: []string{
				"cmd/generate/resourcegenerator/main.go: added WithConcealedDomains(); the tenant segment /tenants derives from the Tenant record",
				"schema/migrations/000002_Tenants.up.sql and .down.sql: the Tenants table (a generated id as its primary key, a unique Name, and DirectoryOnly) and the TenantDirectories table (a tenant's directory with its DefaultSeat, held to one for a tenant and to one tenant by its two unique indexes)",
				"pkg/resources/tenants.go: the Tenant resource struct, global, keyed by a generated id, listed in name order (@order(Name asc)), with its declaration's flag (DirectoryOnly) and change tracking",
				"pkg/resources/tenant_directories.go: the TenantDirectory resource struct, a tenant's directory, set from a sign-in and never typed, tenant-scoped under /tenants/{tenantID}/tenant-directories",
				"ran go generate ./..., which emitted the Tenant and TenantDirectory resources, the tenant segment pair under /tenants/{tenantID}, the roster constructor NewTenantRoster and the declaration reader NewTenantDeclarationReader",
			},
			wantSkipped: []string{
				"schema/devseed already exists, so no development tenants were seeded; add two to it",
				"no file declares a DataConfiguration struct, so the tenant roster was not added to the data level; build it where the database is opened with app.NewTenantRoster(client, resource.WithTenantSignals(<the live service>)), start it (Start) and fail the start on its error, and expose it as TenantRoster()",
				noAuth,
				"no file declares a Configurer interface, so the tenant roster was not exposed on the app; the generated code needs TenantRoster() *resource.TenantRoster on the handlers, answering the roster the data level built",
				"web/angular.json has no project rooted over console/src/app/core/service, so the browser was left as it was: the tenant pages (the tenant's sign-in page, the All tenants page's configuration and the dialog a person who signed up creates their own tenant through), the tenant menus and the tenant segment in the routes",
			},
			check: func(t *testing.T, a *app.App) {
				t.Helper()
				if _, err := os.Stat(a.Abs("pkg/config/tenancy.go")); !errors.Is(err, os.ErrNotExist) {
					t.Error("pkg/config/tenancy.go was written without a DataConfiguration")
				}
			},
		},
	}
	// The base as impulse new renders it: the data level holds the engine through the
	// auth package (*staff.Auth), not an *access.Client field.
	authShape := tenancyFiles()
	authShape["pkg/config/data.go"] = authConfig
	authShape["pkg/auth/staff/staff.go"] = staffPackage(t, "staff.go")
	// The auth package gains the reader in its Settings and the sign-in rule in New, and
	// the data level's settings literal passes the reader in.
	withAuthDid := slices.Clone(tests[0].wantDid)
	withAuthDid[6] = "app/tenancy.go: TenancyConfigurer (embedded in Configurer, asking TenantRoster() and TenantDeclarations()), App.TenantRoster() and App.TenantAdministratorRoles() (the staff auth's AdministratorRoles held in every tenant: the domain roles the Tenant record's generated create-your-own route gives the person who creates a tenant of their own, through the auth's identity client, which the generated contract asks the handlers for); app/app.go gained the fields and their assignments"
	withAuthDid = slices.Insert(withAuthDid, 6,
		"pkg/auth/staff/staff.go: Settings carries the declaration reader (TenantDeclarations) and the directory reader (TenantDirectories), New refuses a nil one, and New sets the sign-in rule on the session (signin.TenantPolicy over the reader, the memberships read through resource.SignInRoles) and the directory admission (session.Auth.SetDirectoryAdmission over the directory reader, the default role given by the identity client's DirectorySeated in the transaction that creates the person)",
		"pkg/auth/staff/staff.go: New's one sign-in rule is the tenant policy with the second factor chained last (.Then(requireSecondFactor)); the call that set the second factor alone is gone, since an Auth has one rule and a second SetSignInPolicy call panics when the auth opens",
		"pkg/config/data.go: the staff.Settings literal in NewDataConfiguration (an application composed before the staffSettings method) passes the declaration reader (app.NewTenantDeclarationReader()) and the directory reader (app.NewTenantDirectoryReader()) to the staff auth",
	)
	tests = append(tests, struct {
		name        string
		files       map[string]string
		wantDid     []string
		wantSkipped []string
		check       func(t *testing.T, a *app.App)
	}{
		name:        "the base with an auth package",
		files:       authShape,
		wantDid:     withAuthDid,
		wantSkipped: []string{noSignals, noPermissions, noMenus},
		check: func(t *testing.T, a *app.App) {
			t.Helper()
			config := read(t, a, "pkg/config/tenancy.go")
			if want := "c.tenants = app.NewTenantRoster(resource.NewSpannerClient(c.spannerClient))"; !strings.Contains(config, want) {
				t.Errorf("tenancy.go lacks %q:\n%s", want, config)
			}
			data := read(t, a, "pkg/config/data.go")
			for _, want := range []string{"\ttenants       *resource.TenantRoster\n", "conf.startTenants(ctx)", "&staff.Settings{CookieKey: cookieKey, TenantDeclarations: app.NewTenantDeclarationReader(), TenantDirectories: app.NewTenantDirectoryReader()}", "\t\"example.com/acme/beacon/app\"\n"} {
				if !strings.Contains(data, want) {
					t.Errorf("data.go lacks %q:\n%s", want, data)
				}
			}
			tenancy := read(t, a, "app/tenancy.go")
			for _, want := range []string{
				"\t\"github.com/impulseframework/impulse/accesstypes\"\n\t\"github.com/impulseframework/impulse/resource\"\n\t\"example.com/acme/beacon/pkg/auth/staff\"\n",
				"func (a *App) TenantAdministratorRoles() []accesstypes.Role {\n\t_, everyDomain := staff.AdministratorRoles()\n\n\treturn everyDomain\n}\n",
			} {
				if !strings.Contains(tenancy, want) {
					t.Errorf("app/tenancy.go lacks %q:\n%s", want, tenancy)
				}
			}
			staff := read(t, a, "pkg/auth/staff/staff.go")
			for _, want := range []string{
				"\tTenantDeclarations signin.DeclarationReader[*cloudspanner.ReadWriteTransaction]\n",
				"\tTenantDirectories signin.DirectoryReader[*cloudspanner.ReadWriteTransaction]\n}",
				"\tif settings.TenantDeclarations == nil {\n\t\treturn nil, errors.New(\"staff.Settings.TenantDeclarations is nil: the sign-in rule reads what each tenant declares about who may enter it through the generated reader (app.NewTenantDeclarationReader), which the data level hands in\")\n\t}\n",
				"\tif settings.TenantDirectories == nil {\n\t\treturn nil, errors.New(\"staff.Settings.TenantDirectories is nil: the directory admission reads what the tenants declare about one directory, its default seat included, through the generated reader (app.NewTenantDirectoryReader), which the data level hands in\")\n\t}\n\n\tstore, err := spannerstore.New(",
				// One rule: the tenant policy, with the second factor the base set alone
				// chained last.
				"\t// engine's read under the transaction's context as a nested transaction. A sign-in\n\t// the rule admits then carries a second factor (requireSecondFactor), chained last:\n\t// an Auth has one rule.\n\tauthSession.SetSignInPolicy(signin.TenantPolicy(resource.SignInRoles(accessClient.UserManager()), settings.TenantDeclarations).Then(requireSecondFactor))\n\n\t// The directory admission",
				"\tauthSession.SetDirectoryAdmission(settings.TenantDirectories, identityClient.DirectorySeated())\n\n\treturn &Auth{access: accessClient, handlers: handlers, identity: identityClient}, nil\n",
				"\t\"github.com/impulseframework/impulse/session/signin\"\n",
				"\t\"github.com/impulseframework/impulse/resource\"\n",
			} {
				if !strings.Contains(staff, want) {
					t.Errorf("staff.go lacks %q:\n%s", want, staff)
				}
			}
		},
	})
	// A data level whose constructor returns a configuration built elsewhere: the roster
	// field is added, the load is an obligation, and the file keeps its contents.
	builtElsewhere := tenancyFiles()
	builtElsewhere["pkg/config/data.go"] = strings.Replace(beaconConfig,
		"\treturn &DataConfiguration{\n\t\tspannerClient: spannerClient,\n\t\taccess:        accessClient,\n\t}, nil\n",
		"\treturn assemble(spannerClient, accessClient), nil\n", 1)
	wantBuiltElsewhere := withRosterField(builtElsewhere["pkg/config/data.go"])
	wantBuiltElsewhereDid := slices.Clone(tests[0].wantDid)
	wantBuiltElsewhereDid[5] = strings.Replace(wantBuiltElsewhereDid[5], "gained the field and the start", "gained the field", 1)
	tests = append(tests, struct {
		name        string
		files       map[string]string
		wantDid     []string
		wantSkipped []string
		check       func(t *testing.T, a *app.App)
	}{
		name:    "a constructor without the returned literal keeps the file and gains the field",
		files:   builtElsewhere,
		wantDid: wantBuiltElsewhereDid,
		wantSkipped: []string{
			"pkg/config/data.go: NewDataConfiguration does not end in \"return &DataConfiguration{...}, nil\", so the roster's start was not inserted; call conf.startTenants(ctx) once the configuration is built, and fail the start on its error",
			noSignals,
			noAuth,
			noSeatAuth,
			noPermissions,
			noMenus,
		},
		check: func(t *testing.T, a *app.App) {
			t.Helper()
			if diff := cmp.Diff(wantBuiltElsewhere, read(t, a, "pkg/config/data.go")); diff != "" {
				t.Errorf("data.go mismatch (-want +got):\n%s", diff)
			}
		},
	})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := beacon(t, tt.files)
			if tt.name == "an application without the skeleton's seams" {
				// A workspace whose project does not own the client target.
				if err := os.WriteFile(a.Abs("web/angular.json"), []byte(`{"version": 1, "projects": {"other": {"root": "other", "sourceRoot": "other/src"}}}`), 0o600); err != nil {
					t.Fatal(err)
				}
				var err error
				if a, err = app.Discover(a.Root); err != nil {
					t.Fatal(err)
				}
			}
			exec := &fakeExec{}
			ch, err := Tenancy{Table: "Tenants"}.Apply(t.Context(), a, exec)
			if err != nil {
				t.Fatalf("Apply() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantDid, ch.Did); diff != "" {
				t.Errorf("Did mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantSkipped, ch.Skipped); diff != "" {
				t.Errorf("Skipped mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(wantProgram, read(t, a, program)); diff != "" {
				t.Errorf("program mismatch (-want +got):\n%s", diff)
			}
			if tt.check != nil {
				tt.check(t, a)
			}
		})
	}
}

// TestClientExpression names the client the roster reads through, by what the data level
// holds: the database driver's resource client, a resource client of the level's own, a
// Spanner client wrapped as one, or nothing.
func TestClientExpression(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "the database driver",
			src:  "package config\n\nimport \"github.com/impulseframework/impulse/resource/database/spanner\"\n\ntype DataConfiguration struct {\n\tdatabase *spanner.Driver\n}\n",
			want: "c.database.ResourceClient",
		},
		{
			name: "the database driver under the alias the skeleton binds it by",
			src:  "package config\n\nimport database \"github.com/impulseframework/impulse/resource/database/spanner\"\n\ntype DataConfiguration struct {\n\tdatabase *database.Driver\n}\n",
			want: "c.database.ResourceClient",
		},
		{
			name: "a resource client of the level's own",
			src:  "package config\n\nimport \"github.com/impulseframework/impulse/resource\"\n\ntype DataConfiguration struct {\n\tresourceClient *resource.SpannerClient\n}\n",
			want: "c.resourceClient",
		},
		{
			name: "a Spanner client wrapped as a resource client",
			src:  "package config\n\nimport cloudspanner \"cloud.google.com/go/spanner\"\n\ntype DataConfiguration struct {\n\tspannerClient *cloudspanner.Client\n}\n",
			want: "resource.NewSpannerClient(c.spannerClient)",
		},
		{
			name: "neither",
			src:  "package config\n\ntype DataConfiguration struct {\n\tname string\n}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := clientExpression("pkg/config/data.go", []byte(tt.src))
			if err != nil {
				t.Fatalf("clientExpression() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("clientExpression() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestTenancyMeaningSaysToFormat holds the brief to the one step the command cannot take:
// it writes browser files under the record's and the auth's names without wrapping them
// again, so the brief says to format them once the web root's packages are installed.
func TestTenancyMeaningSaysToFormat(t *testing.T) {
	t.Parallel()

	meaning := Tenancy{Table: "ServiceAreas"}.Meaning()
	for _, want := range []string{"after the install, run `impulse check --only prettier --fix` once", "which wraps again any line those names made longer or shorter and changes nothing where they left every line as it was", "a file that needs it fails the `prettier` check until then"} {
		if !strings.Contains(meaning, want) {
			t.Errorf("Meaning() lacks %q:\n%s", want, meaning)
		}
	}
}

// TestTenancyApply_theBaseSuites pins what add tenancy says of the integration tests an
// application without tenants starts with: each one the application still carries is
// named with what becomes of it, so its author learns it from the change and not from
// one compile error after another; an application that carries none is told nothing.
func TestTenancyApply_theBaseSuites(t *testing.T) {
	t.Parallel()

	fates := map[string]string{
		"test/integration/sign_up_test.go":             "test/integration/sign_up_test.go proves sign-up in an application without tenants, where opening a sign-in method names the role each new person gets; a tenanted application opens a method with no role, and a person who signs up creates a tenant of their own, so remove the file and prove sign-up as the reference's self_service_test.go does",
		"test/integration/sign_up_role_test.go":        "test/integration/sign_up_role_test.go proves the list the role of a person who signs up is picked from, which the generator no longer writes once the application has tenants (its generated file is removed by the regeneration); remove the file",
		"test/integration/first_administrator_test.go": "test/integration/first_administrator_test.go still holds, since the first administrator signs in as before; it is written against the base's harness, so where the harness takes the reference's sign-in helpers this file's calls of them follow",
	}
	tests := []struct {
		name  string
		files []string
	}{
		{name: "the base's three suites are each named", files: []string{"test/integration/sign_up_test.go", "test/integration/sign_up_role_test.go", "test/integration/first_administrator_test.go"}},
		{name: "a suite the application no longer carries is not named", files: []string{"test/integration/first_administrator_test.go"}},
		{name: "an application with none of them is told nothing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			files := tenancyFiles()
			for _, file := range tt.files {
				files[file] = "package integration\n"
			}
			ch, err := Tenancy{Table: "Tenants"}.Apply(t.Context(), beacon(t, files), &fakeExec{})
			if err != nil {
				t.Fatalf("Apply() error = %v", err)
			}
			for file, fate := range fates {
				if got, want := slices.Contains(ch.Skipped, fate), slices.Contains(tt.files, file); got != want {
					t.Errorf("Skipped names %s = %t, want %t; got:\n%s", file, got, want, strings.Join(ch.Skipped, "\n"))
				}
			}
		})
	}
	// The brief's list of the tests to write points at those lines.
	if meaning := (Tenancy{Table: "Tenants"}).Meaning(); !strings.Contains(meaning, "under what the change could not make, say what becomes of `sign_up_test.go`, `sign_up_role_test.go` and `first_administrator_test.go`") {
		t.Errorf("Meaning() does not point at what becomes of the base's suites:\n%s", meaning)
	}
}

// TestBaseSuitesAreTheBasesOwn holds the suites the tenancy change says the fate of to
// the skeletons themselves: every suite of the served stack the base carries and the
// tenanted skeleton does not is one the change names, and it names no other. A suite
// added to the base alone therefore fails here until the change says what becomes of it
// in a tenanted application, before an author meets it as a compile error.
func TestBaseSuitesAreTheBasesOwn(t *testing.T) {
	t.Parallel()

	suites := func(candidate string) []string {
		t.Helper()
		tree, err := skeleton.FS(candidate)
		if err != nil {
			t.Fatalf("skeleton.FS(%s) error = %v", candidate, err)
		}
		entries, err := fs.ReadDir(tree, integrationSuite)
		if err != nil {
			t.Fatalf("%s: reading %s: %v", candidate, integrationSuite, err)
		}
		var names []string
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), "_test.go") {
				names = append(names, e.Name())
			}
		}

		return names
	}
	tenanted := suites(TenancyReferenceCandidate)
	var want []string
	for _, name := range suites(skeleton.Base) {
		if !slices.Contains(tenanted, name) {
			want = append(want, name)
		}
	}
	var got []string
	for _, suite := range baseSuites {
		got = append(got, suite.file)
	}
	slices.Sort(want)
	slices.Sort(got)
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("the suites the change names differ from the base's own, the ones the %s skeleton does not carry (-the skeletons +the change):\n%s", TenancyReferenceCandidate, diff)
	}
}

// TestTenancyApply_theReadme holds the README's opening to the change: the base's says
// the application has no tenancy and no resources, which add tenancy makes untrue, so
// the sentence says what the application has now in the record's own names, and the
// change says the rest of the README is the application's to bring up. The fixture is
// the base skeleton's own README, so the sentence the transition reads is the one a
// rendered application carries. A README in the application's own words is left alone.
func TestTenancyApply_theReadme(t *testing.T) {
	t.Parallel()

	tree, err := skeleton.FS(skeleton.Base)
	if err != nil {
		t.Fatalf("skeleton.FS() error = %v", err)
	}
	base, err := fs.ReadFile(tree, "README.md")
	if err != nil {
		t.Fatalf("reading the base's README: %v", err)
	}
	const (
		did  = "README.md: the opening says the application has tenancy (the ServiceArea record over ServiceAreas, its routes under /api/service-areas) where it said it had none"
		left = "README.md describes the application as it was rendered beyond that sentence: say what its service areas hold and who its people are as the tenant-scoped resources arrive (the reference's README is the pattern)"
		own  = "# beacon\n\nThe lighthouse keepers' rota.\n"
	)
	tests := []struct {
		name     string
		readme   string
		want     string
		wantSaid bool
	}{
		{
			name: "the base's opening", readme: string(base), wantSaid: true,
			want: "One site, one browser application, tenancy as data: the `ServiceArea` record (`@tenant`) over the\n`ServiceAreas` table is the domain universe, a tenant's key is an id the server generates, and\ntenant-scoped routes live under `/api/service-areas/{serviceAreaID}/`.\n",
		},
		{name: "a README in the application's own words", readme: own, want: own},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			files := tenancyFiles()
			files["README.md"] = tt.readme
			a := beacon(t, files)
			ch, err := Tenancy{Table: "ServiceAreas"}.Apply(t.Context(), a, &fakeExec{})
			if err != nil {
				t.Fatalf("Apply() error = %v", err)
			}
			got := read(t, a, "README.md")
			if !strings.Contains(got, tt.want) {
				t.Errorf("README.md lacks %q:\n%s", tt.want, got[:min(len(got), 700)])
			}
			if strings.Contains(got, "no tenancy") != (!tt.wantSaid && strings.Contains(tt.readme, "no tenancy")) {
				t.Errorf("README.md says the application has no tenancy = %t after the change:\n%s", strings.Contains(got, "no tenancy"), got[:min(len(got), 700)])
			}
			if got := slices.Contains(ch.Did, did); got != tt.wantSaid {
				t.Errorf("Did names the README's opening = %t, want %t; got:\n%s", got, tt.wantSaid, strings.Join(ch.Did, "\n"))
			}
			if got := slices.Contains(ch.Skipped, left); got != tt.wantSaid {
				t.Errorf("Skipped says the rest of the README is the application's = %t, want %t; got:\n%s", got, tt.wantSaid, strings.Join(ch.Skipped, "\n"))
			}
		})
	}
}

// TestTenancyApply_oneSignInRule pins the sign-in rule add tenancy leaves on an auth:
// one call. The base's auth package sets the second factor alone, and an Auth has one
// rule, a second SetSignInPolicy call panicking when the auth opens, so the transition
// takes that call out and writes the tenant policy with the second factor chained last,
// and says so. Where New set a rule in a form the tool does not read, both calls stand
// and the change says they must become one, in the words the second-factor check uses.
func TestTenancyApply_oneSignInRule(t *testing.T) {
	t.Parallel()

	chainedDid := "pkg/auth/staff/staff.go: New's one sign-in rule is the tenant policy with the second factor chained last (.Then(requireSecondFactor)); the call that set the second factor alone is gone, since an Auth has one rule and a second SetSignInPolicy call panics when the auth opens"
	twoRules := "pkg/auth/staff/staff.go: New set a sign-in rule before this change, in a form the tool does not read, and now sets the tenant policy beside it; an Auth has one rule and a second SetSignInPolicy call panics when the auth opens, so make the two one call: the tenant policy first, the rule that stood chained after it with .Then, and requireSecondFactor last"
	base := staffPackage(t, "staff.go")
	const lone = "authSession.SetSignInPolicy(requireSecondFactor)"
	if strings.Count(base, lone) != 1 {
		t.Fatalf("the base's auth package sets the second factor alone %d time(s), want once: the fixture of this test", strings.Count(base, lone))
	}
	// An auth package whose New ends in a form of its own: the tool writes no rule there.
	const skeletonEnd, ownEnd = "\treturn &Auth{access: accessClient, handlers: handlers, identity: identityClient}, nil\n", "\tauth := &Auth{access: accessClient, handlers: handlers, identity: identityClient}\n\n\treturn auth, nil\n"
	if strings.Count(base, skeletonEnd) != 1 {
		t.Fatalf("the base's auth package ends New as the skeleton does %d time(s), want once: the fixture of this test", strings.Count(base, skeletonEnd))
	}
	ownForm := strings.Replace(base, skeletonEnd, ownEnd, 1)
	oneAlready := "pkg/auth/staff/staff.go: New does not end in `return &Auth{access: accessClient, handlers: handlers, identity: identityClient}, nil` as the skeleton's does, so the sign-in rule was not changed, and New sets one already; an Auth has one rule and a second SetSignInPolicy call panics when the auth opens, so make the call New makes now the tenant policy with the rule that stood chained after it: authSession.SetSignInPolicy(signin.TenantPolicy(resource.SignInRoles(accessClient.UserManager()), settings.TenantDeclarations).Then(<the rule New sets now>))"
	tests := []struct {
		name        string
		staff       string
		wantCalls   int
		wantRule    string
		wantDid     bool
		wantSkipped bool
		// wantLeft is a line the change leaves for the reader, beside the two above.
		wantLeft string
	}{
		{
			// The reader is told to make the one call the tenant policy, never to add a
			// second call beside the rule that stands.
			name: "a New of another form keeps its one rule, and the change says to make that call the tenant policy", staff: ownForm, wantCalls: 1, wantLeft: oneAlready,
			wantRule: lone,
		},
		{
			name: "the base's rule is chained into the tenant policy", staff: base, wantCalls: 1, wantDid: true,
			wantRule: "authSession.SetSignInPolicy(signin.TenantPolicy(resource.SignInRoles(accessClient.UserManager()), settings.TenantDeclarations).Then(requireSecondFactor))",
		},
		{
			name: "a rule of another form is left, and the change says the two must become one", staff: strings.Replace(base, lone, "authSession.SetSignInPolicy(houseRule)", 1), wantCalls: 2, wantSkipped: true,
			wantRule: "authSession.SetSignInPolicy(signin.TenantPolicy(resource.SignInRoles(accessClient.UserManager()), settings.TenantDeclarations))",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			files := tenancyFiles()
			files["pkg/config/data.go"] = authConfig
			files["pkg/auth/staff/staff.go"] = tt.staff
			a := beacon(t, files)
			ch, err := Tenancy{Table: "Tenants"}.Apply(t.Context(), a, &fakeExec{})
			if err != nil {
				t.Fatalf("Apply() error = %v", err)
			}
			staff := read(t, a, "pkg/auth/staff/staff.go")
			if n := strings.Count(staff, ".SetSignInPolicy("); n != tt.wantCalls {
				t.Errorf("staff.go calls SetSignInPolicy %d time(s), want %d:\n%s", n, tt.wantCalls, staff)
			}
			if !strings.Contains(staff, tt.wantRule) {
				t.Errorf("staff.go lacks %q:\n%s", tt.wantRule, staff)
			}
			if got := slices.Contains(ch.Did, chainedDid); got != tt.wantDid {
				t.Errorf("Did says the rule was chained = %t, want %t; got:\n%s", got, tt.wantDid, strings.Join(ch.Did, "\n"))
			}
			if got := slices.Contains(ch.Skipped, twoRules); got != tt.wantSkipped {
				t.Errorf("Skipped says the two rules must become one = %t, want %t; got:\n%s", got, tt.wantSkipped, strings.Join(ch.Skipped, "\n"))
			}
			if tt.wantLeft != "" && !slices.Contains(ch.Skipped, tt.wantLeft) {
				t.Errorf("Skipped lacks %q; got:\n%s", tt.wantLeft, strings.Join(ch.Skipped, "\n"))
			}
			for _, line := range ch.Skipped {
				if tt.wantLeft != "" && strings.Contains(line, "so the sign-in rule was not set; call ") {
					t.Errorf("the change tells the reader to call SetSignInPolicy beside the rule that stands, which panics when the auth opens: %s", line)
				}
			}
		})
	}
}

// Test_retellPermissions pins the UserPermissions comment add tenancy leaves: the base
// skeleton's says the application has no tenants, and once the function is handed the
// roster and the declarations it says what the reference's says. A comment that is not
// the skeleton's is left as its author wrote it.
func Test_retellPermissions(t *testing.T) {
	t.Parallel()

	comment := func(candidate string) string {
		t.Helper()
		sub, err := skeleton.FS(candidate)
		if err != nil {
			t.Fatal(err)
		}
		data, err := fs.ReadFile(sub, "app/app.go")
		if err != nil {
			t.Fatal(err)
		}
		src := string(data)
		start, end := strings.Index(src, "// UserPermissions returns"), strings.Index(src, "func (a *App) UserPermissions(")
		if start < 0 || end < start {
			t.Fatalf("the %s skeleton's app/app.go carries no UserPermissions comment", candidate)
		}

		return src[start:end]
	}
	base, reference := comment("solo"), comment(TenancyReferenceCandidate)
	if got := string(retellPermissions([]byte(base))); got != reference {
		t.Errorf("the base's comment retold (-want +got):\n%s", cmp.Diff(reference, got))
	}
	own := "// UserPermissions answers who may do what.\n"
	if got := string(retellPermissions([]byte(own))); got != own {
		t.Errorf("an author's own comment was rewritten to %q", got)
	}
}

// TestTenancyApply_theCommentFollowsTheDeclarations holds the UserPermissions comment to
// what the function was handed: it is retold as the reference's once the roster and the
// declarations are passed, and it is left as it was where the declarations could not be
// handed over, since a comment saying a tenant is offered by its declaration would not
// be true of that function.
func TestTenancyApply_theCommentFollowsTheDeclarations(t *testing.T) {
	t.Parallel()

	tree, err := skeleton.FS(skeleton.Base)
	if err != nil {
		t.Fatal(err)
	}
	data, err := fs.ReadFile(tree, "app/app.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	start, end := strings.Index(src, "// UserPermissions returns"), strings.Index(src, "func (a *App) UserPermissions(")
	if start < 0 || end < start {
		t.Fatal("the base skeleton's app/app.go carries no UserPermissions comment")
	}
	const plain, untenanted, retold = "// UserPermissions returns the permission checker for a request.\n", "The application has no tenants", "A tenant is offered only when the"
	both := strings.Replace(permissionsApp, plain, src[start:end], 1)
	if both == permissionsApp || !strings.Contains(both, untenanted) {
		t.Fatal("the fixture App no longer carries the comment this test replaces with the base's")
	}
	rosterAlone := strings.Replace(both, "a.access.ForRole, nil, nil, a.collection)", "a.access.ForRole, nil, a.collection)", 1)
	if rosterAlone == both {
		t.Fatal("the fixture App no longer passes the two nils this test reduces to one")
	}
	tests := []struct {
		name       string
		app        string
		wantRetold bool
	}{
		{name: "the roster and the declarations are handed over", app: both, wantRetold: true},
		{name: "the declarations could not be handed over", app: rosterAlone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			files := tenancyFiles()
			files["app/app.go"] = tt.app
			a := beacon(t, files)
			ch, err := Tenancy{Table: "Tenants"}.Apply(t.Context(), a, &fakeExec{})
			if err != nil {
				t.Fatalf("Apply() error = %v", err)
			}
			got := read(t, a, "app/app.go")
			if strings.Contains(got, retold) != tt.wantRetold || strings.Contains(got, untenanted) == tt.wantRetold {
				t.Errorf("the comment is retold = %t and says the application has no tenants = %t, want retold = %t:\n%s", strings.Contains(got, retold), strings.Contains(got, untenanted), tt.wantRetold, got)
			}
			left := slices.ContainsFunc(ch.Skipped, func(line string) bool { return strings.Contains(line, "so the declarations were not handed to it") })
			if left == tt.wantRetold {
				t.Errorf("the change says the declarations were not handed over = %t, want %t; got:\n%s", left, !tt.wantRetold, strings.Join(ch.Skipped, "\n"))
			}
		})
	}
}

// TestTenancyApply_whoIsAdmittedWithoutATenant pins which auth's sign-in rule admits a
// person who holds no tenant once add tenancy has written the record: the auth the
// default outlet binds, since the record is served there and a person who signs up to
// it creates a tenant of their own through the generated route. An auth no outlet binds
// keeps the refusal (TestTenancyApply pins it, on a program that declares no Auth).
func TestTenancyApply_whoIsAdmittedWithoutATenant(t *testing.T) {
	t.Parallel()

	files := tenancyFiles()
	files["pkg/config/data.go"] = authConfig
	files["pkg/auth/staff/staff.go"] = staffPackage(t, "staff.go")
	bound := strings.Replace(beaconProgram,
		"\t\tgeneration.GenerateRoutes(\"pkg/router\", \"api\"),\n",
		"\t\tgeneration.GenerateRoutes(\"pkg/router\", \"api\", generation.Auth(\"example.com/acme/beacon/pkg/auth/staff\", generation.PasswordMethod())),\n", 1)
	if bound == beaconProgram {
		t.Fatal("the fixture program no longer declares its routes as this test rewrites them")
	}
	files["cmd/generate/resourcegenerator/main.go"] = bound

	a := beacon(t, files)
	if _, err := (Tenancy{Table: "Tenants"}).Apply(t.Context(), a, &fakeExec{}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	staff := read(t, a, "pkg/auth/staff/staff.go")
	for _, want := range []string{
		"\t// A person who holds no tenant is admitted too (signin.AdmitWithoutTenant): a\n\t// person who signs up through a sign-in method an administrator opened signs in\n\t// first and creates a tenant of their own, and keeps their account when the method\n\t// is made invite only again.",
		"\tauthSession.SetSignInPolicy(signin.TenantPolicy(resource.SignInRoles(accessClient.UserManager()), settings.TenantDeclarations, signin.AdmitWithoutTenant()).Then(requireSecondFactor))\n",
	} {
		if !strings.Contains(staff, want) {
			t.Errorf("staff.go lacks %q:\n%s", want, staff)
		}
	}
	// An Auth has one rule: the call that set the second factor alone is gone, and a
	// second call would panic when the auth opens.
	if n := strings.Count(staff, ".SetSignInPolicy("); n != 1 {
		t.Errorf("staff.go calls SetSignInPolicy %d time(s), want once:\n%s", n, staff)
	}
	if lone := "authSession.SetSignInPolicy(requireSecondFactor)"; strings.Contains(staff, lone) {
		t.Errorf("staff.go still holds %q beside the tenant policy", lone)
	}
	if refused := "A person who holds no tenant is refused (no_tenant)"; strings.Contains(staff, refused) {
		t.Errorf("staff.go says %q of the auth whose people create a tenant of their own", refused)
	}
}

// makerConfig is a data level in the skeleton's shape: the database driver, the live
// service, and the staff auth built from the settings function on the environment struct.
const makerConfig = `package config

import (
	"context"

	"github.com/go-playground/errors/v5"
	database "github.com/impulseframework/impulse/resource/database/spanner"
	livefirestore "github.com/impulseframework/impulse/resource/live/firestore"

	"example.com/acme/beacon/pkg/auth/staff"
)

// DataConfiguration is the second level.
type DataConfiguration struct {
	database *database.Driver
	staff    *staff.Auth
	live     *livefirestore.Service
}

// NewDataConfiguration opens the clients.
func NewDataConfiguration(ctx context.Context) (*DataConfiguration, error) {
	env := &dataConfig{}
	db, liveService, cookieKey, err := open(ctx, env)
	if err != nil {
		return nil, errors.Wrap(err, "open()")
	}

	staffAuth, err := staff.New(ctx, db.SpannerClient, env.staffSettings(liveService, cookieKey))
	if err != nil {
		return nil, errors.Wrap(err, "staff.New()")
	}

	return &DataConfiguration{
		database: db,
		staff:    staffAuth,
		live:     liveService,
	}, nil
}

// staffSettings builds the staff auth's settings.
func (env *dataConfig) staffSettings(signals staff.Signals, cookieKey string) *staff.Settings {
	return &staff.Settings{
		Signals:   signals,
		CookieKey: cookieKey,
	}
}
`

// boundProgram is the fixture program with the staff auth bound to its default outlet,
// which serves the tenant record: a tenant is made through that auth.
func boundProgram(t *testing.T) string {
	t.Helper()

	bound := strings.Replace(beaconProgram,
		"\t\tgeneration.GenerateRoutes(\"pkg/router\", \"api\"),\n",
		"\t\tgeneration.GenerateRoutes(\"pkg/router\", \"api\", generation.Auth(\"example.com/acme/beacon/pkg/auth/staff\", generation.PasswordMethod())),\n", 1)
	if bound == beaconProgram {
		t.Fatal("the fixture program no longer declares its routes as this test rewrites them")
	}

	return bound
}

// TestTenancyApply_theAuthATenantIsMadeThrough pins what add tenancy gives the auth whose
// outlets serve the tenant record, where the application assigns its roles and generates
// its management: nobody creates a tenant directly, staff of that auth invite one person
// and that person's first sign-in makes the tenant, so the auth's Settings carry the
// generated maker, New refuses a nil one and hands it to the identity client as the
// generated client's third argument. On a data level in the skeleton's shape the roster
// is built ahead of the auth, the maker is passed through the settings function, and
// startTenants only starts the roster. On a level the tool cannot follow (here one with
// no live service, which the maker is constructed over) the roster is built in
// startTenants as before and handing the maker in is recorded as the agent's, since the
// auth refuses to start without it. An auth no outlet binds makes no tenant and gains
// none of it.
func TestTenancyApply_theAuthATenantIsMadeThrough(t *testing.T) {
	t.Parallel()

	inAuth := []string{
		"\t// TenantMaker makes a Tenant from an accepted invitation: the generated maker over\n\t// the record's own create patch (app.NewTenantMaker). Nobody creates a tenant\n\t// directly: staff invite one person with its details, which the auth's identity\n\t// client checks with the maker when the invitation is sent, and that person's first\n\t// sign-in makes the tenant with it. Required; New refuses nil.\n\tTenantMaker identity.TenantMaker\n}",
		"\tif settings.TenantMaker == nil {\n\t\treturn nil, errors.New(\"staff.Settings.TenantMaker is nil: a tenant is made by an invitation's first sign-in, through the generated maker over the Tenant record (app.NewTenantMaker), which the data level hands in\")\n\t}\n\n\tstore, err := spannerstore.New(",
		"\tidentityClient, err := staffidentity.NewClient(accessClient, authSession.API(), settings.TenantMaker, identity.WithAdministratorRoles(tenantAdministrators))\n",
	}
	carried := "pkg/auth/staff/staff.go: Settings carries the declaration reader (TenantDeclarations), the directory reader (TenantDirectories) and what makes a tenant from an accepted invitation (TenantMaker, which New hands the identity client), New refuses a nil one, and New sets the sign-in rule on the session"
	obligation := "the staff auth is not handed what makes a tenant from an accepted invitation, and refuses to start without it (staff.Settings.TenantMaker is nil): a tenant is made through this auth, by the first sign-in of a person its staff invited, so build the roster before the auth where the data level is opened (app.NewTenantRoster(<the client>, resource.WithTenantSignals(<the live service>)), which startTenants then only starts) and pass app.NewTenantMaker(<the roster>, <the live service>) in the auth's Settings"

	t.Run("the skeleton's data level builds the roster ahead of the auth and passes the maker", func(t *testing.T) {
		t.Parallel()

		files := tenancyFiles()
		files["pkg/config/data.go"] = makerConfig
		files["pkg/auth/staff/staff.go"] = staffPackage(t, "staff.go")
		files["cmd/generate/resourcegenerator/main.go"] = boundProgram(t)
		a := beacon(t, files)
		ch, err := Tenancy{Table: "Tenants"}.Apply(t.Context(), a, &fakeExec{})
		if err != nil {
			t.Fatalf("Apply() error = %v", err)
		}
		staff := read(t, a, "pkg/auth/staff/staff.go")
		for _, want := range inAuth {
			if !strings.Contains(staff, want) {
				t.Errorf("staff.go lacks %q:\n%s", want, staff)
			}
		}
		data := read(t, a, "pkg/config/data.go")
		for _, want := range []string{
			"\t// The tenant roster is built ahead of the staff auth: a tenant is made through that\n",
			"\ttenants := app.NewTenantRoster(db.ResourceClient, resource.WithTenantSignals(liveService))\n\n\tstaffAuth, err := staff.New(ctx, db.SpannerClient, env.staffSettings(liveService, cookieKey, app.NewTenantMaker(tenants, liveService)))\n",
			"\t\ttenants:  tenants,\n",
			"\tif err := conf.startTenants(ctx); err != nil {\n",
			"func (env *dataConfig) staffSettings(signals staff.Signals, cookieKey string, tenants identity.TenantMaker) *staff.Settings {\n",
			"\t\tTenantMaker:        tenants,\n",
			"\t\"github.com/impulseframework/impulse/identity\"\n",
			"\t\"example.com/acme/beacon/app\"\n",
		} {
			if !strings.Contains(data, want) {
				t.Errorf("data.go lacks %q:\n%s", want, data)
			}
		}
		// startTenants starts the roster the level built and constructs none, so its file
		// imports no handlers package.
		tenancy := read(t, a, "pkg/config/tenancy.go")
		for _, want := range []string{
			"// startTenants starts the tenant roster, built over the Tenant record with the generated\n// constructor where the level is opened, ahead of the auth a tenant is made through",
			// The function builds nothing here, and opens with the start: no blank line
			// stands after its brace, which the application's own lint refuses.
			"func (c *DataConfiguration) startTenants(ctx context.Context) error {\n\tif err := c.tenants.Start(ctx); err != nil {\n",
			"\tc.staff.Session().SetTenantNames(c.tenants)\n",
		} {
			if !strings.Contains(tenancy, want) {
				t.Errorf("tenancy.go lacks %q:\n%s", want, tenancy)
			}
		}
		for _, notWant := range []string{"NewTenantRoster(", "example.com/acme/beacon/app"} {
			if strings.Contains(tenancy, notWant) {
				t.Errorf("tenancy.go holds %q, want the roster built in NewDataConfiguration alone:\n%s", notWant, tenancy)
			}
		}
		for _, want := range []string{
			"pkg/config/tenancy.go: the tenant roster (NewDataConfiguration builds it with app.NewTenantRoster ahead of the staff auth, through which a tenant is made, and startTenants starts it), TenantRoster() and TenantDeclarations() on DataConfiguration; pkg/config/data.go gained the field and the start and the roster's construction",
			"pkg/config/data.go: dataConfig.staffSettings passes the declaration reader (app.NewTenantDeclarationReader()), the directory reader (app.NewTenantDirectoryReader()) and what makes a tenant from an accepted invitation (app.NewTenantMaker(tenants, liveService), over the roster built ahead of the auth) to the staff auth",
		} {
			if !slices.Contains(ch.Did, want) {
				t.Errorf("Did lacks %q; got:\n%s", want, strings.Join(ch.Did, "\n"))
			}
		}
		if !slices.ContainsFunc(ch.Did, func(did string) bool { return strings.HasPrefix(did, carried) }) {
			t.Errorf("Did lacks a line starting %q; got:\n%s", carried, strings.Join(ch.Did, "\n"))
		}
		if slices.Contains(ch.Skipped, obligation) {
			t.Errorf("Skipped records the maker as the agent's on a level the command wired:\n%s", strings.Join(ch.Skipped, "\n"))
		}
	})

	t.Run("a data level with no live service leaves handing the maker in to the agent", func(t *testing.T) {
		t.Parallel()

		files := tenancyFiles()
		files["pkg/config/data.go"] = authConfig
		files["pkg/auth/staff/staff.go"] = staffPackage(t, "staff.go")
		files["cmd/generate/resourcegenerator/main.go"] = boundProgram(t)
		a := beacon(t, files)
		ch, err := Tenancy{Table: "Tenants"}.Apply(t.Context(), a, &fakeExec{})
		if err != nil {
			t.Fatalf("Apply() error = %v", err)
		}
		staff := read(t, a, "pkg/auth/staff/staff.go")
		for _, want := range inAuth {
			if !strings.Contains(staff, want) {
				t.Errorf("staff.go lacks %q:\n%s", want, staff)
			}
		}
		if !slices.Contains(ch.Skipped, obligation) {
			t.Errorf("Skipped lacks %q; got:\n%s", obligation, strings.Join(ch.Skipped, "\n"))
		}
		if data := read(t, a, "pkg/config/data.go"); strings.Contains(data, "TenantMaker") || strings.Contains(data, "tenants := ") {
			t.Errorf("data.go was handed a maker over a roster the level does not build ahead of the auth:\n%s", data)
		}
		if tenancy := read(t, a, "pkg/config/tenancy.go"); !strings.Contains(tenancy, "c.tenants = app.NewTenantRoster(resource.NewSpannerClient(c.spannerClient))") {
			t.Errorf("tenancy.go does not build the roster in startTenants:\n%s", tenancy)
		}
	})

	t.Run("an auth no outlet binds makes no tenant", func(t *testing.T) {
		t.Parallel()

		files := tenancyFiles()
		files["pkg/config/data.go"] = makerConfig
		files["pkg/auth/staff/staff.go"] = staffPackage(t, "staff.go")
		a := beacon(t, files)
		ch, err := Tenancy{Table: "Tenants"}.Apply(t.Context(), a, &fakeExec{})
		if err != nil {
			t.Fatalf("Apply() error = %v", err)
		}
		for rel, notWant := range map[string]string{"pkg/auth/staff/staff.go": "TenantMaker", "pkg/config/data.go": "TenantMaker"} {
			if got := read(t, a, rel); strings.Contains(got, notWant) {
				t.Errorf("%s holds %q for an auth a tenant is not made through:\n%s", rel, notWant, got)
			}
		}
		if tenancy := read(t, a, "pkg/config/tenancy.go"); !strings.Contains(tenancy, "c.tenants = app.NewTenantRoster(c.database.ResourceClient, resource.WithTenantSignals(c.live))") {
			t.Errorf("tenancy.go does not build the roster in startTenants:\n%s", tenancy)
		}
		if slices.Contains(ch.Skipped, obligation) {
			t.Errorf("Skipped records a maker for an auth a tenant is not made through:\n%s", strings.Join(ch.Skipped, "\n"))
		}
	})
}

// TestTenancyApply_oneTenantPerPerson pins what add tenancy writes when a person belongs
// to one tenant: every auth whose roles the application assigns builds its identity
// client with the identity package's option, as its last argument, after the maker and
// the administrator roles the command hands the client of the auth a tenant is made
// through. Without the choice the client is left as it was, so a person can be in
// several tenants. An auth whose New builds the client in a form the tool does not read
// is recorded as the agent's, with the rule the option holds.
func TestTenancyApply_oneTenantPerPerson(t *testing.T) {
	t.Parallel()

	const (
		option = "identity.WithOneTenantPerPerson()"
		did    = "pkg/auth/staff/staff.go: the staff auth's identity client is built with identity.WithOneTenantPerPerson(), so a person belongs to one tenant: a role in a second tenant is refused however it would be given, and the application's own people, who hold a role for the whole application or in every tenant, are outside the rule"
	)
	fixture := func(t *testing.T, staff string) *app.App {
		t.Helper()

		files := tenancyFiles()
		files["pkg/config/data.go"] = makerConfig
		files["pkg/auth/staff/staff.go"] = staff
		files["cmd/generate/resourcegenerator/main.go"] = boundProgram(t)

		return beacon(t, files)
	}

	t.Run("each auth whose roles the application assigns builds its identity client with the option", func(t *testing.T) {
		t.Parallel()

		a := fixture(t, staffPackage(t, "staff.go"))
		tn := Tenancy{Table: "Tenants", OneTenantPerPerson: true}
		ch, err := tn.Apply(t.Context(), a, &fakeExec{})
		if err != nil {
			t.Fatalf("Apply() error = %v", err)
		}
		staff := read(t, a, "pkg/auth/staff/staff.go")
		if want := "\tidentityClient, err := staffidentity.NewClient(accessClient, authSession.API(), settings.TenantMaker, identity.WithAdministratorRoles(tenantAdministrators), " + option + ")\n"; !strings.Contains(staff, want) {
			t.Errorf("staff.go lacks %q:\n%s", want, staff)
		}
		if !slices.Contains(ch.Did, did) {
			t.Errorf("Did lacks %q; got:\n%s", did, strings.Join(ch.Did, "\n"))
		}
		if want := "impulse add tenancy --tenant-table Tenants --one-tenant-per-person"; ch.Command != want {
			t.Errorf("Command = %q, want %q", ch.Command, want)
		}
		if want := "One tenant per person. This command declared it where it could"; !strings.Contains(tn.Meaning(), want) {
			t.Errorf("Meaning() lacks %q", want)
		}
	})

	t.Run("without the choice a person can be in several tenants", func(t *testing.T) {
		t.Parallel()

		a := fixture(t, staffPackage(t, "staff.go"))
		tn := Tenancy{Table: "Tenants"}
		ch, err := tn.Apply(t.Context(), a, &fakeExec{})
		if err != nil {
			t.Fatalf("Apply() error = %v", err)
		}
		if staff := read(t, a, "pkg/auth/staff/staff.go"); strings.Contains(staff, option) {
			t.Errorf("staff.go holds %q, which nobody chose:\n%s", option, staff)
		}
		if want := "impulse add tenancy --tenant-table Tenants"; ch.Command != want {
			t.Errorf("Command = %q, want %q", ch.Command, want)
		}
		if strings.Contains(tn.Meaning(), "One tenant per person.") {
			t.Error("Meaning() speaks of one tenant per person, which nobody chose")
		}
	})

	t.Run("an auth whose New builds its client another way is the agent's", func(t *testing.T) {
		t.Parallel()

		built := "\tidentityClient, err := staffidentity.NewClient("
		staff := staffPackage(t, "staff.go")
		if !strings.Contains(staff, built) {
			t.Fatal("the skeleton's staff auth no longer builds its identity client as this test rewrites it")
		}
		a := fixture(t, strings.Replace(staff, built, "\tidentityClient, err := staffidentity.NewClient(\n\t\t", 1))
		ch, err := Tenancy{Table: "Tenants", OneTenantPerPerson: true}.Apply(t.Context(), a, &fakeExec{})
		if err != nil {
			t.Fatalf("Apply() error = %v", err)
		}
		if got := read(t, a, "pkg/auth/staff/staff.go"); strings.Contains(got, option) {
			t.Errorf("staff.go holds %q in a call the tool does not read:\n%s", option, got)
		}
		want := "pkg/auth/staff/staff.go: New builds no identity client in the form the tool reads (identityClient, err := staffidentity.NewClient(...) on one line), so one tenant per person was not declared on the staff auth; pass identity.WithOneTenantPerPerson() among the options of its identity client: a role in a second tenant is refused however it would be given, and the application's own people, who hold a role for the whole application or in every tenant, are outside the rule"
		if !slices.Contains(ch.Skipped, want) {
			t.Errorf("Skipped lacks %q; got:\n%s", want, strings.Join(ch.Skipped, "\n"))
		}
		if slices.Contains(ch.Did, did) {
			t.Errorf("Did says the option was declared:\n%s", strings.Join(ch.Did, "\n"))
		}
	})
}
