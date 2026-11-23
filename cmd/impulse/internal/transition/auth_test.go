package transition

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/impulseframework/impulse/cmd/impulse/internal/authpkg"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/names"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton"
)

// staffPackage is the base's staff auth package, read from the embedded skeleton: the
// auth the new one is written beside.
func staffPackage(t *testing.T, file string) string {
	t.Helper()

	sub, err := skeleton.FS("solo")
	if err != nil {
		t.Fatal(err)
	}
	data, err := fs.ReadFile(sub, "pkg/auth/staff/"+file)
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

// authConfig is a data level constructing the staff auth.
const authConfig = `package config

import (
	"context"

	cloudspanner "cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"

	"example.com/acme/beacon/pkg/auth/staff"
)

// DataConfiguration is the second level.
type DataConfiguration struct {
	spannerClient *cloudspanner.Client
	staff         *staff.Auth
}

// NewDataConfiguration opens the clients.
func NewDataConfiguration(ctx context.Context) (*DataConfiguration, error) {
	spannerClient, cookieKey, err := open(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "open()")
	}

	staffAuth, err := staff.New(ctx, spannerClient, &staff.Settings{CookieKey: cookieKey})
	if err != nil {
		return nil, errors.Wrap(err, "staff.New()")
	}

	return &DataConfiguration{
		spannerClient: spannerClient,
		staff:         staffAuth,
	}, nil
}
`

// authConfigEnv is a data level reading its settings from an environment struct, as the
// base does.
const authConfigEnv = `package config

import (
	"context"
	"time"

	cloudspanner "cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"

	"example.com/acme/beacon/pkg/auth/staff"
)

// DataConfiguration is the second level.
type DataConfiguration struct {
	spannerClient *cloudspanner.Client
	staff         *staff.Auth
}

// NewDataConfiguration opens the clients.
func NewDataConfiguration(ctx context.Context) (*DataConfiguration, error) {
	env := &dataConfig{}
	spannerClient, cookieKey, err := open(ctx, env)
	if err != nil {
		return nil, errors.Wrap(err, "open()")
	}

	staffAuth, err := staff.New(ctx, spannerClient, &staff.Settings{CookieKey: cookieKey, SessionTimeout: env.SessionTimeout})
	if err != nil {
		return nil, errors.Wrap(err, "staff.New()")
	}

	return &DataConfiguration{
		spannerClient: spannerClient,
		staff:         staffAuth,
	}, nil
}

// dataConfig holds the environment every database-opening process reads.
type dataConfig struct {
	// SessionTimeout is the idle timeout of a browser session.
	SessionTimeout time.Duration ` + "`" + `env:"APP_DEFAULT_SESSION_TIMEOUT,default=10m"` + "`" + `
}
`

// authConfigSettings is a data level building the staff auth's settings in a method of
// its environment struct, as the base does.
const authConfigSettings = `package config

import (
	"context"
	"time"

	cloudspanner "cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"

	"example.com/acme/beacon/pkg/auth/staff"
)

// DataConfiguration is the second level.
type DataConfiguration struct {
	spannerClient *cloudspanner.Client
	staff         *staff.Auth
}

// NewDataConfiguration opens the clients.
func NewDataConfiguration(ctx context.Context) (*DataConfiguration, error) {
	env := &dataConfig{}
	spannerClient, cookieKey, err := open(ctx, env)
	if err != nil {
		return nil, errors.Wrap(err, "open()")
	}

	staffAuth, err := staff.New(ctx, spannerClient, env.staffSettings(cookieKey))
	if err != nil {
		return nil, errors.Wrap(err, "staff.New()")
	}

	return &DataConfiguration{
		spannerClient: spannerClient,
		staff:         staffAuth,
	}, nil
}

// dataConfig holds the environment every database-opening process reads.
type dataConfig struct {
	// SessionTimeout is the idle timeout of a browser session.
	SessionTimeout time.Duration ` + "`" + `env:"APP_DEFAULT_SESSION_TIMEOUT,default=10m"` + "`" + `
}

// staffSettings builds the staff auth's settings: the cookie key and the session
// timeout. A directory registration's settings join the literal here.
func (env *dataConfig) staffSettings(cookieKey string) *staff.Settings {
	return &staff.Settings{CookieKey: cookieKey, SessionTimeout: env.SessionTimeout}
}
`

// authRouterProgram is a generator program under the generated router: the console on
// the default outlet, a portal outlet, both bound to the staff auth, and an API-key
// outlet.
const authRouterProgram = `// Package main is the beacon resource generator program.
package main

import (
	"context"
	"log"

	"github.com/impulseframework/impulse/generation"
)

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	generator, err := generation.NewResourceGenerator(
		ctx,
		"pkg/resources",
		[]string{"file://schema/migrations"},
		generation.GenerateHandlers("app"),
		generation.GenerateRouter(),
		generation.GenerateRoutes("pkg/router", "console/api",
			generation.Auth("example.com/acme/beacon/pkg/auth/staff", generation.PasswordMethod()),
			generation.WebApp("/console"),
		),
		generation.WithRouterOutlet("machines", "machines", generation.Tokens("example.com/acme/beacon/pkg/auth/staff")),
		generation.WithRouterOutlet("portal", "portal/api",
			generation.Auth("example.com/acme/beacon/pkg/auth/staff", generation.PasswordMethod()),
			generation.WebApp("/portal"),
		),
		generation.GenerateHandlerTests("test/authz"),
		generation.WithSpannerEmulatorVersion("1.5.56"),
		generation.GenerateTypescript("web/console/src/app/core/service",
			generation.GenerateMetadata(),
			generation.GeneratePermissions(),
			generation.GenerateEnums(),
		),
	)
	if err != nil {
		return err
	}
	defer generator.Close()

	return generator.Generate()
}
`

// programFile is the generator program's path in the beacon fixture.
const programFile = "cmd/generate/resourcegenerator/main.go"

func authFiles(t *testing.T) map[string]string {
	t.Helper()

	return map[string]string{
		"pkg/auth/staff/staff.go":                             staffPackage(t, "staff.go"),
		"pkg/auth/staff/password.go":                          staffPackage(t, "password.go"),
		"pkg/config/data.go":                                  authConfig,
		"pkg/auth/staff/roles.json":                           "{\n  \"auth\": \"staff\",\n  \"roles\": {\n    \"global\": [],\n    \"domain\": []\n  }\n}\n",
		"schema/migrations/000001_DataChangeEvents.up.sql":    "CREATE TABLE DataChangeEvents (Id STRING(36) NOT NULL) PRIMARY KEY (Id);\n",
		"schema/migrations/000002_StaffAccess.up.sql":         "CREATE TABLE StaffRoles (\n  Role STRING(128) NOT NULL,\n) PRIMARY KEY (Role);\n\nCREATE TABLE StaffUserRoles (\n  Role STRING(128) NOT NULL,\n) PRIMARY KEY (Role);\n",
		"schema/migrations/000002_StaffAccess.down.sql":       "DROP TABLE StaffUserRoles;\nDROP TABLE StaffRoles;\n",
		"schema/migrations/000003_StaffSessionUsers.up.sql":   "CREATE TABLE StaffSessionUsers (Id STRING(36) NOT NULL) PRIMARY KEY (Id);\n",
		"schema/migrations/000003_StaffSessionUsers.down.sql": "DROP TABLE StaffSessionUsers;\n",
		"schema/migrations/000004_StaffSessions.up.sql":       "CREATE TABLE StaffSessions (Id STRING(36) NOT NULL) PRIMARY KEY (Id);\nCREATE TABLE StaffSessionIdentities (Id STRING(36) NOT NULL) PRIMARY KEY (Id);\n",
		"schema/migrations/000004_StaffSessions.down.sql":     "DROP TABLE StaffSessionIdentities;\nDROP TABLE StaffSessions;\n",
	}
}

func TestAuthCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		auth Auth
		want string
	}{
		{name: "password", auth: Auth{Name: "partners", Outlet: "default"}, want: "impulse add auth partners --outlet default"},
		{name: "directory methods with their authority", auth: Auth{Name: "members", Methods: []authpkg.Method{{Kind: app.MethodPassword}, {Kind: app.MethodGoogle, Name: "workspace", Authority: AuthorityApplication}}, Outlet: "portal"}, want: "impulse add auth members --method password --method google:workspace --authority application --outlet portal"},
		{name: "a directory method whose directory gives the roles", auth: Auth{Name: "members", Methods: []authpkg.Method{{Kind: app.MethodAzure, Name: "directory", Authority: AuthorityDirectory}}, Outlet: "portal"}, want: "impulse add auth members --method azure:directory --authority directory --outlet portal"},
		{name: "a starting point, which is what says the slot its method was written with", auth: Auth{Name: "members", Methods: []authpkg.Method{{Kind: app.MethodGoogle, Name: "google", Authority: AuthorityApplication, Slot: authpkg.SlotAny}}, Outlet: "portal", Shape: "open-sign-up"}, want: "impulse add auth members --method google:google --authority application --outlet portal --shape open-sign-up"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.auth.Command(); got != tt.want {
				t.Errorf("Command() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAuthMeaning(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		auth  Auth
		wants []string
		never []string
	}{
		{
			name: "a password auth starts invite only, and the brief names the grants the choice needs",
			auth: Auth{Name: "partners", Outlet: "default"},
			wants: []string{
				"generation.PasswordMethod()",
				"Decide who may open a sign-in method to sign-up. Every method of the partners auth starts invite only",
				"`OpenPartnersSignUp` opens one, after a warning that says who that admits for a method this wide, and `MakePartnersInviteOnly` closes it again",
				"Grant List on `PartnersSignInMethods` to the roles that may see the choice, and Execute on the two methods to the role that may make it; a role without the grant sees no action",
				"The password method's sign-up routes (`user/sign-up`, `/open`, `/complete`) are served whenever the auth has the method, and answer 404 while it is invite only",
			},
			never: []string{".InvitedUsers()", ".OpenSignUp()", "--accounts", "@tenant(creation"},
		},
		{
			name:  "a directory auth admitting members of the directory",
			auth:  Auth{Name: "partners", Methods: []authpkg.Method{{Kind: app.MethodAzure, Name: "directory", Authority: AuthorityDirectory}}, Outlet: "portal"},
			wants: []string{`generation.AzureMethod("directory").SyncsRoles())`, "(`azure.SignIn`), role membership the directory's", "its declaration chains `.SyncsRoles()` (the method file's `RoleSync`)"},
			// The directory decides who the auth's people are: nobody opens its method.
			never: []string{"Decide who may open a sign-in method to sign-up", ".InvitedUsers()"},
		},
		{
			name:  "a directory auth whose roles the application assigns names the grants the invitations need",
			auth:  Auth{Name: "partners", Methods: []authpkg.Method{{Kind: app.MethodGoogle, Name: "workspace", Authority: AuthorityApplication}}, Outlet: "portal"},
			wants: []string{`generation.GoogleMethod("workspace"))`, "(`google.SignIn`), role membership the application's", "Every method of the partners auth starts invite only", "the generated management of the partners auth sends it (the `InvitePartnersUser` method), so grant Execute on it to the role that invites in the role file"},
			never: []string{".InvitedUsers()", "signin.InvitedUsersOnly"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			meaning := tt.auth.Meaning()
			for _, want := range tt.wants {
				if !strings.Contains(meaning, want) {
					t.Errorf("Meaning() lacks %q:\n%s", want, meaning)
				}
			}
			for _, unwanted := range tt.never {
				if strings.Contains(meaning, unwanted) {
					t.Errorf("Meaning() carries %q:\n%s", unwanted, meaning)
				}
			}
		})
	}
}

func TestAuthValidate(t *testing.T) {
	t.Parallel()

	routed := map[string]string{programFile: authRouterProgram}
	tests := []struct {
		name    string
		auth    Auth
		bare    bool
		extra   map[string]string
		wantErr string
	}{
		{name: "a password auth", auth: Auth{Name: "partners", Outlet: "default"}},
		{name: "a password auth, said so", auth: Auth{Name: "partners", Methods: []authpkg.Method{{Kind: app.MethodPassword}}, Outlet: "default"}},
		{name: "a bad name", auth: Auth{Name: "Partners", Outlet: "default"}, wantErr: `auth name "Partners"`},
		{name: "a directory-authority method beside the password method", auth: Auth{Name: "partners", Methods: []authpkg.Method{{Kind: app.MethodPassword}, {Kind: app.MethodGoogle, Name: "workspace", Authority: AuthorityDirectory}}, Outlet: "default"}, wantErr: "a registration whose directory writes the roles stands alone on its auth"},
		{name: "two directory-authority methods", auth: Auth{Name: "partners", Methods: []authpkg.Method{{Kind: app.MethodAzure, Name: "directory", Authority: AuthorityDirectory}, {Kind: app.MethodGoogle, Name: "workspace", Authority: AuthorityDirectory}}, Outlet: "default"}, wantErr: "stands alone on its auth"},
		{name: "a directory auth with the directory as authority", auth: Auth{Name: "partners", Methods: []authpkg.Method{{Kind: app.MethodAzure, Name: "directory", Authority: AuthorityDirectory}}, Outlet: "default"}},
		{name: "a directory auth with the application as authority", auth: Auth{Name: "partners", Methods: []authpkg.Method{{Kind: app.MethodAzure, Name: "directory", Authority: AuthorityApplication}}, Outlet: "default"}},
		{name: "a directory auth without an authority", auth: Auth{Name: "partners", Methods: []authpkg.Method{{Kind: app.MethodAzure, Name: "directory"}}, Outlet: "default"}, wantErr: "the directory method needs --authority"},
		{name: "a directory auth with a made-up authority", auth: Auth{Name: "partners", Methods: []authpkg.Method{{Kind: app.MethodAzure, Name: "directory", Authority: "nobody"}}, Outlet: "default"}, wantErr: `authority "nobody"`},
		{name: "two methods", auth: Auth{Name: "partners", Methods: []authpkg.Method{{Kind: app.MethodPassword}, {Kind: app.MethodGoogle, Name: "google", Authority: AuthorityApplication}}, Outlet: "default"}},
		{name: "a registration given twice", auth: Auth{Name: "partners", Methods: []authpkg.Method{{Kind: app.MethodAzure, Name: "directory", Authority: AuthorityApplication}, {Kind: app.MethodGoogle, Name: "directory", Authority: AuthorityApplication}}, Outlet: "default"}, wantErr: "given twice"},
		{name: "an unknown method", auth: Auth{Name: "partners", Methods: []authpkg.Method{{Kind: "ldap", Name: "ldap", Authority: AuthorityApplication}}, Outlet: "default"}, wantErr: `"ldap" is not a sign-in method`},
		{name: "the auth exists", auth: Auth{Name: "staff", Outlet: "default"}, wantErr: "the staff auth already exists"},
		{name: "no auth package to write beside", auth: Auth{Name: "partners", Outlet: "default"}, bare: true, wantErr: "no auth package to write beside"},
		{name: "no outlet", auth: Auth{Name: "partners"}, extra: routed, wantErr: "--outlet is required: the outlet the partners auth serves, whose declaration is pointed at the auth; the outlets are default, portal (impulse add outlet adds one)"},
		{name: "an outlet the program does not declare", auth: Auth{Name: "partners", Outlet: "kiosk"}, extra: routed, wantErr: "no kiosk outlet for the partners auth to serve; the outlets are default, portal (impulse add outlet adds one)"},
		{name: "a machine outlet", auth: Auth{Name: "partners", Outlet: "machines"}, extra: routed, wantErr: "the machines outlet is a machine outlet (Tokens()), a surface with no sign-in; an auth serves a session outlet, and the outlets are default, portal"},
		{name: "a session outlet under the generated router", auth: Auth{Name: "partners", Outlet: "portal"}, extra: routed},
		{name: "the default outlet under the generated router", auth: Auth{Name: "partners", Outlet: "default"}, extra: routed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var files map[string]string
			if !tt.bare {
				files = authFiles(t)
				for rel, content := range tt.extra {
					files[rel] = content
				}
			}
			err := tt.auth.Validate(beacon(t, files))
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

func TestAuthApply(t *testing.T) {
	t.Parallel()

	wantConfig := strings.Replace(authConfig, "\t\"example.com/acme/beacon/pkg/auth/staff\"\n", "\t\"example.com/acme/beacon/pkg/auth/partners\"\n\t\"example.com/acme/beacon/pkg/auth/staff\"\n", 1)
	wantConfig = strings.Replace(wantConfig, "\tstaff         *staff.Auth\n}", "\tstaff         *staff.Auth\n\tpartners      *partners.Auth\n}", 1)
	wantConfig = strings.Replace(wantConfig,
		"\treturn &DataConfiguration{\n\t\tspannerClient: spannerClient,\n\t\tstaff:         staffAuth,\n\t}, nil\n",
		"\tpartnersAuth, err := partners.New(ctx, spannerClient, &partners.Settings{CookieKey: cookieKey})\n\tif err != nil {\n\t\treturn nil, errors.Wrap(err, \"partners.New()\")\n\t}\n\n\treturn &DataConfiguration{\n\t\tspannerClient: spannerClient,\n\t\tstaff:         staffAuth,\n\t\tpartners:      partnersAuth,\n\t}, nil\n", 1)

	// The data level after add tenancy: WrapReturn assigned the literal and inserted the
	// roster load, so the constructor no longer ends in "return &DataConfiguration{...}, nil".
	tenantedConfig := strings.Replace(authConfig,
		"\treturn &DataConfiguration{\n\t\tspannerClient: spannerClient,\n\t\tstaff:         staffAuth,\n\t}, nil\n",
		"\tconf := &DataConfiguration{\n\t\tspannerClient: spannerClient,\n\t\tstaff:         staffAuth,\n\t}\n\tif err := conf.loadTenants(ctx); err != nil {\n\t\treturn nil, errors.Wrap(err, \"loadTenants()\")\n\t}\n\n\treturn conf, nil\n", 1)

	// The data level of a tenanted application: the staff auth's settings literal carries
	// the generated reader of the tenant record's declaration, which the copy keeps.
	declaredConfig := strings.Replace(authConfig, "&staff.Settings{CookieKey: cookieKey}", "&staff.Settings{CookieKey: cookieKey, TenantDeclarations: app.NewTenantDeclarationReader()}", 1)
	declaredConfig = strings.Replace(declaredConfig, "\t\"example.com/acme/beacon/pkg/auth/staff\"\n", "\t\"example.com/acme/beacon/app\"\n\t\"example.com/acme/beacon/pkg/auth/staff\"\n", 1)
	const tenantRecord = "package resources\n\ntype (\n\t// Tenant is the tenant record.\n\t//\n\t// @resource\n\t// @tenant\n\tTenant struct {\n\t\tID string `spanner:\"Id\"`\n\t}\n)\n"
	// tenantDirectoriesRecord is the directories record of an application whose tenants
	// can have a directory of their own.
	const tenantDirectoriesRecord = "package resources\n\ntype (\n\t// @resource\n\t// @permissionScope(domain)\n\t// @tenantDirectory\n\tTenantDirectory struct {\n\t\t// @domain\n\t\tTenantID string `spanner:\"TenantId\"`\n\t\t// @directoryMethod\n\t\tMethod string `spanner:\"Method\"`\n\t\t// @directoryConnection\n\t\tConnection string `spanner:\"Connection\"`\n\t}\n)\n"

	// A data level whose constructor builds no literal at all: the auth can be declared
	// and imported, but not constructed.
	builtElsewhereConfig := strings.Replace(authConfig,
		"\treturn &DataConfiguration{\n\t\tspannerClient: spannerClient,\n\t\tstaff:         staffAuth,\n\t}, nil\n",
		"\treturn assemble(spannerClient, staffAuth), nil\n", 1)

	// What the foothold file leaves its author: the roles of the first administrator's
	// invitation where the application generates the auth's management, whose
	// acceptance hook gives the invitation's roles, and no first administrator where it
	// generates none. What invites one outside the package is the command's to write,
	// and a data level that keeps no environment struct, as this application's, gets
	// none of it and the account of what to write by hand.
	const (
		invited    = "pkg/auth/partners/foothold.go: the first administrator the migrate command invites while nobody of the auth holds the Super Administrator role, by an invitation mailed to the address the placement names (APP_PARTNERS_ADMINISTRATOR) that gives the Super Administrator role and the first administrator's roles; no account and no password are made ahead of the person, who signs in from the link by a method of the auth. Author AdministratorRoles (the roles whose holder can give a role back) and FirstAdministratorRoles (what the invitation gives) with the role file"
		notInvited = "pkg/auth/partners/foothold.go: AdministratorRoles, the roles whose holder can give a role back, to author with the role file. The deployment invites no first administrator for the partners auth: the application generates no management of the auth's people, so nothing would give an accepted invitation its roles. impulse check refuses the auth as it stands (first-administrator), since nobody can be invited as its first administrator and an account seeded by hand is refused too: have the program generate the management (generation.GenerateIdentity), build the auth's identity client from it in New, and invite the first administrator as the tool writes foothold.go for such an auth"
	)
	const handed = "pkg/config/data.go: DataConfiguration keeps no environment struct (a field env *T) to read the address into, so nothing that invites the partners auth's first administrator was written outside its package, and no release invites one. Read the address on the data level (APP_PARTNERS_ADMINISTRATOR, a setting with no secret tag, behind a PartnersAdministrator() accessor), name the auth in the deployment package's AdministratorAuths and administratorOf, and have the migrate command call deploy.InviteAdministrators after its role check and deploy.ReinviteAdministrator from a -reinvite-administrator flag, as impulse new writes them for the auth an application starts with"
	const packageDid = "pkg/auth/partners: the partners auth package (foothold.go, partners.go, password.go), rendered from the skeleton's auth source: the Auth session over tables prefixed Partners, the partners permission store, and the methods password, each in its own file"
	const migrationsDid = "schema/migrations: 000005_PartnersAccess, 000006_PartnersSessionUsers, 000007_PartnersSessions, the partners auth's tables: the access tables copied from the staff auth's under the Partners prefix, and the Auth session tables (sessions, users with their addresses, identities, auth events, pending sign-ins, sign-in attempts, sign-in methods, mail links)"
	const rolesDid = "pkg/auth/partners/roles.json: the partners auth's role file, empty: author its roles when its surfaces are bound"
	const configDid = "pkg/config/data.go: the partners auth constructed on DataConfiguration beside the staff auth (field, construction, import), its settings in the construction's literal as the staff auth's are (an application composed before the staffSettings method); pkg/config/partners.go: its accessor Partners()"
	const configSettingsDid = "pkg/config/data.go: the partners auth constructed on DataConfiguration beside the staff auth (field, construction, import), its settings built by dataConfig.partnersSettings, copied from staffSettings; pkg/config/partners.go: its accessor Partners()"

	// The data level in the skeleton's shape after add auth: the construction copied with
	// its settings call renamed, and the settings function copied under the new name.
	wantSettingsConfig := strings.Replace(authConfigSettings, "\t\"example.com/acme/beacon/pkg/auth/staff\"\n", "\t\"example.com/acme/beacon/pkg/auth/partners\"\n\t\"example.com/acme/beacon/pkg/auth/staff\"\n", 1)
	wantSettingsConfig = strings.Replace(wantSettingsConfig, "\tstaff         *staff.Auth\n}", "\tstaff         *staff.Auth\n\tpartners      *partners.Auth\n}", 1)
	wantSettingsConfig = strings.Replace(wantSettingsConfig,
		"\treturn &DataConfiguration{\n\t\tspannerClient: spannerClient,\n\t\tstaff:         staffAuth,\n\t}, nil\n",
		"\tpartnersAuth, err := partners.New(ctx, spannerClient, env.partnersSettings(cookieKey))\n\tif err != nil {\n\t\treturn nil, errors.Wrap(err, \"partners.New()\")\n\t}\n\n\treturn &DataConfiguration{\n\t\tspannerClient: spannerClient,\n\t\tstaff:         staffAuth,\n\t\tpartners:      partnersAuth,\n\t}, nil\n", 1)
	wantSettingsConfig += "\n// partnersSettings builds the partners auth's settings: the cookie key and the session\n// timeout. A directory registration's settings join the literal here.\nfunc (env *dataConfig) partnersSettings(cookieKey string) *partners.Settings {\n\treturn &partners.Settings{CookieKey: cookieKey, SessionTimeout: env.SessionTimeout}\n}\n"

	// A settings call whose receiver the level never builds with env := &T{}: the struct
	// the function is declared on is unknown, so the copy is the agent's.
	unnamedSettingsConfig := strings.Replace(authConfigSettings, "\tenv := &dataConfig{}\n\tspannerClient, cookieKey, err := open(ctx, env)\n", "\tenv, spannerClient, cookieKey, err := open(ctx)\n", 1)

	routed := map[string]string{programFile: authRouterProgram}
	tests := []struct {
		name        string
		auth        Auth
		extra       map[string]string
		wantDid     []string
		wantSkipped []string
		// neverDid are words no did line carries.
		neverDid []string
		// neverSkipped are words no skipped line carries.
		neverSkipped []string
		check        func(t *testing.T, a *app.App)
		// tidiedGoMod is the go.mod the tidy script leaves; empty leaves go.mod as it is.
		tidiedGoMod string
		generateErr error
		wantErr     string
	}{
		{
			name:    "a password auth written beside staff",
			auth:    Auth{Name: "partners", Outlet: "default"},
			wantDid: []string{packageDid, migrationsDid, rolesDid, configDid, "ran go generate ./..."},
			wantSkipped: []string{
				notInvited,
				"pkg/config/data.go: Close releases the staff auth only; release the partners auth too",
				programFile + ": the default outlet is served by a hand-written router, so its declaration carries no Auth to point at the partners auth; mount the auth's session group under /api in the router",
			},
			check: func(t *testing.T, a *app.App) {
				t.Helper()
				if got, want := read(t, a, "pkg/auth/partners/roles.json"), "{\n  \"auth\": \"partners\",\n  \"roles\": {\n    \"global\": [],\n    \"domain\": []\n  }\n}\n"; got != want {
					t.Errorf("pkg/auth/partners/roles.json = %q, want %q: the new auth's empty file, carrying its name", got, want)
				}
				pkg := read(t, a, "pkg/auth/partners/partners.go")
				for _, want := range []string{"package partners", `Name = "partners"`, `TablePrefix = "Partners"`, "session.NewAuth[session.NoCustomData, session.NoCustomData](", `sessionsTable       = "PartnersSessions"`, "handlers.password = registerPassword(authSession)"} {
					if !strings.Contains(pkg, want) {
						t.Errorf("partners.go lacks %q", want)
					}
				}
				if strings.Contains(pkg, "staff") || strings.Contains(pkg, "Staff") {
					t.Error("partners.go still mentions staff")
				}
				if got := read(t, a, "pkg/auth/partners/password.go"); !strings.Contains(got, "func registerPassword(authSession *Session) *session.PasswordMethod") {
					t.Errorf("password.go = %q", got)
				}
				if diff := cmp.Diff(wantConfig, read(t, a, "pkg/config/data.go")); diff != "" {
					t.Errorf("data.go mismatch (-want +got):\n%s", diff)
				}
				if got := read(t, a, "schema/migrations/000007_PartnersSessions.up.sql"); !strings.Contains(got, "CREATE TABLE PartnersSessions") || !strings.Contains(got, "CREATE TABLE PartnersSessionPendingSignIns") || !strings.Contains(got, "CREATE TABLE PartnersSessionSignInAttempts") {
					t.Errorf("sessions migration = %q", got)
				}
				if got := read(t, a, "schema/migrations/000006_PartnersSessionUsers.down.sql"); !strings.Contains(got, "DROP TABLE PartnersSessionUsers;") {
					t.Errorf("users down migration = %q", got)
				}
				if got := read(t, a, "schema/migrations/000005_PartnersAccess.down.sql"); !strings.Contains(got, "DROP TABLE PartnersRoles;") {
					t.Errorf("access down migration = %q", got)
				}
				if got := read(t, a, "pkg/config/partners.go"); !strings.Contains(got, "func (c *DataConfiguration) Partners() *partners.Auth") {
					t.Errorf("accessor = %q", got)
				}
			},
		},
		{
			name:    "a password auth written beside staff copies the staff settings function",
			auth:    Auth{Name: "partners", Outlet: "default"},
			extra:   map[string]string{"pkg/config/data.go": authConfigSettings},
			wantDid: []string{packageDid, migrationsDid, rolesDid, configSettingsDid, "ran go generate ./..."},
			wantSkipped: []string{
				"pkg/config/data.go: Close releases the staff auth only; release the partners auth too",
			},
			check: func(t *testing.T, a *app.App) {
				t.Helper()
				if diff := cmp.Diff(wantSettingsConfig, read(t, a, "pkg/config/data.go")); diff != "" {
					t.Errorf("data.go mismatch (-want +got):\n%s", diff)
				}
			},
		},
		{
			name:  "a directory auth's registration joins the copied settings function",
			auth:  Auth{Name: "partners", Methods: []authpkg.Method{{Kind: app.MethodAzure, Name: "directory", Authority: AuthorityApplication}}, Outlet: "default"},
			extra: map[string]string{"pkg/config/data.go": authConfigSettings},
			wantDid: []string{
				"pkg/config/data.go: dataConfig reads the partners auth's directory registration from APP_PARTNERS_DIRECTORY_TENANT_ID, APP_PARTNERS_DIRECTORY_ADMINISTERED_TENANTS, APP_PARTNERS_DIRECTORY_CLIENT_ID, APP_PARTNERS_DIRECTORY_CLIENT_SECRET, APP_PARTNERS_DIRECTORY_REDIRECT_URL",
				configSettingsDid,
			},
			check: func(t *testing.T, a *app.App) {
				t.Helper()
				got := read(t, a, "pkg/config/data.go")
				for _, want := range []string{
					"\tpartnersAuth, err := partners.New(ctx, spannerClient, env.partnersSettings(cookieKey))\n",
					"\tPartnersDirectoryTenantID            string `env:\"APP_PARTNERS_DIRECTORY_TENANT_ID\"`\n",
					"\tPartnersDirectoryClientSecret        string `env:\"APP_PARTNERS_DIRECTORY_CLIENT_SECRET\"        secret:\"true\"`\n",
					"// partnersSettings builds the partners auth's settings: the cookie key and the session\n// timeout. A directory registration's settings join the literal here.\nfunc (env *dataConfig) partnersSettings(cookieKey string) *partners.Settings {\n\treturn &partners.Settings{\n\t\tCookieKey:      cookieKey,\n\t\tSessionTimeout: env.SessionTimeout,\n\t\tLoginURL:       \"/login\",\n\t\tDirectory: partners.DirectorySettings{\n\t\t\tTenantID:            env.PartnersDirectoryTenantID,\n\t\t\tAdministeredTenants: env.PartnersDirectoryAdministeredTenants,\n\t\t\tClientID:            env.PartnersDirectoryClientID,\n\t\t\tClientSecret:        env.PartnersDirectoryClientSecret,\n\t\t\tRedirectURL:         env.PartnersDirectoryRedirectURL,\n\t\t},\n\t}\n}\n",
				} {
					if !strings.Contains(got, want) {
						t.Errorf("data.go lacks %q:\n%s", want, got)
					}
				}
				// The staff auth's function is as it was: one line, no registration.
				if !strings.Contains(got, "func (env *dataConfig) staffSettings(cookieKey string) *staff.Settings {\n\treturn &staff.Settings{CookieKey: cookieKey, SessionTimeout: env.SessionTimeout}\n}\n") {
					t.Errorf("the staff settings function changed:\n%s", got)
				}
			},
		},
		{
			name:    "a settings call whose struct the level does not name leaves the copy to the agent",
			auth:    Auth{Name: "partners", Outlet: "default"},
			extra:   map[string]string{"pkg/config/data.go": unnamedSettingsConfig},
			wantDid: []string{"pkg/config/data.go: the partners auth constructed on DataConfiguration beside the staff auth (field, construction, import); pkg/config/partners.go: its accessor Partners()"},
			wantSkipped: []string{
				"pkg/config/data.go: the construction reads the staff auth's settings from env.staffSettings(...), but no env := &T{} names the struct the method is declared on, so partnersSettings was not copied; write it for the partners auth beside staffSettings, and pass the partners auth's directory registrations in it",
			},
			check: func(t *testing.T, a *app.App) {
				t.Helper()
				got := read(t, a, "pkg/config/data.go")
				if !strings.Contains(got, "\tpartnersAuth, err := partners.New(ctx, spannerClient, env.partnersSettings(cookieKey))\n") || strings.Contains(got, "func (env *dataConfig) partnersSettings") {
					t.Errorf("data.go = %q", got)
				}
			},
		},
		{
			name:        "a password auth added after tenancy is constructed before the assigned literal",
			auth:        Auth{Name: "partners", Outlet: "default"},
			extra:       map[string]string{"pkg/config/data.go": tenantedConfig},
			wantDid:     []string{packageDid, migrationsDid, rolesDid, configDid, "ran go generate ./..."},
			wantSkipped: []string{"pkg/config/data.go: Close releases the staff auth only; release the partners auth too"},
			check: func(t *testing.T, a *app.App) {
				t.Helper()
				got := read(t, a, "pkg/config/data.go")
				if !strings.Contains(got, "\tpartnersAuth, err := partners.New(ctx, spannerClient, &partners.Settings{CookieKey: cookieKey})\n\tif err != nil {\n\t\treturn nil, errors.Wrap(err, \"partners.New()\")\n\t}\n\n\tconf := &DataConfiguration{\n\t\tspannerClient: spannerClient,\n\t\tstaff:         staffAuth,\n\t\tpartners:      partnersAuth,\n\t}\n") {
					t.Errorf("data.go = %q", got)
				}
			},
		},
		{
			name: "a password auth serving a tenanted site sets the sign-in rule over the record's declaration",
			auth: Auth{Name: "partners", Outlet: "default"},
			// The program generates each auth's management, so the auth builds its identity
			// client in New and sets the hooks a sign-in gives roles through from it. The
			// site has tenant directories, so the auth carries the directory reader and sets
			// the directory admission.
			extra: map[string]string{"pkg/config/data.go": declaredConfig, "pkg/resources/tenants.go": tenantRecord, "pkg/resources/tenant_directories.go": tenantDirectoriesRecord, programFile: strings.Replace(authRouterProgram, "\t\tgeneration.GenerateRouter(),\n", "\t\tgeneration.GenerateRouter(),\n\t\tgeneration.GenerateIdentity(\"pkg/identity\"),\n", 1)},
			wantDid: []string{
				packageDid + ", and the sign-in rule over the Tenant record's declaration (signin.TenantPolicy through the reader its settings carry, app.NewTenantDeclarationReader), admitting a person who holds no tenant yet (signin.AdmitWithoutTenant), since a person who signs up through a method an administrator opened creates their own, and the directory admission over the record's directory rows (session.Auth.SetDirectoryAdmission through the directory reader its settings carry, app.NewTenantDirectoryReader: a stranger from a declared directory gets the row's default seat or is refused by invitation only), and what makes a tenant from an accepted invitation (TenantMaker in its settings, app.NewTenantMaker, which New refuses nil and hands the identity client: staff of this auth bring a tenant in by inviting one person)",
				migrationsDid, rolesDid, configDid, "ran go generate ./...",
			},
			// The default outlet serves the tenant record, so a tenant is made through the
			// auth bound there now: the copy of the staff auth's construction, which made
			// none, hands the new auth no maker, and the wiring is the agent's.
			wantSkipped: []string{
				"pkg/config/data.go: a tenant is made through the partners auth, whose outlet serves the Tenant record, and the construction copied from the staff auth hands it nothing to make one with, so it refuses to start (partners.Settings.TenantMaker is nil); build the roster before the auth and pass app.NewTenantMaker(<the roster>, <the live service>) in its Settings",
			},
			check: func(t *testing.T, a *app.App) {
				t.Helper()
				pkg := read(t, a, "pkg/auth/partners/partners.go")
				for _, want := range []string{
					"\tTenantDeclarations signin.DeclarationReader[*cloudspanner.ReadWriteTransaction]\n",
					"\tTenantDirectories signin.DirectoryReader[*cloudspanner.ReadWriteTransaction]\n",
					"\tTenantMaker identity.TenantMaker\n}",
					"\tif settings.TenantMaker == nil {\n\t\treturn nil, errors.New(\"partners.Settings.TenantMaker is nil: a tenant is made by an invitation's first sign-in, through the generated maker over the Tenant record (app.NewTenantMaker), which the data level hands in\")\n\t}\n\tstore, err := spannerstore.New(",
					"\tif settings.TenantDeclarations == nil {\n\t\treturn nil, errors.New(\"partners.Settings.TenantDeclarations is nil: the sign-in rule reads what each tenant declares about who may enter it through the generated reader (app.NewTenantDeclarationReader), which the data level hands in\")\n\t}\n",
					"\tif settings.TenantDirectories == nil {\n\t\treturn nil, errors.New(\"partners.Settings.TenantDirectories is nil: the directory admission reads what the tenants declare about one directory, its default seat included, through the generated reader (app.NewTenantDirectoryReader), which the data level hands in\")\n\t}\n\tif settings.TenantMaker == nil {",
					"\tauthSession.SetSignInPolicy(signin.TenantPolicy(resource.SignInRoles(accessClient.UserManager()), settings.TenantDeclarations, signin.AdmitWithoutTenant()))\n" + passwordDisabledCheck + "\n\t// The auth's identity client (pkg/identity, generated)",
					"\t\tsettings.CookieKey,\n\t\tsession.SuperAdministratorsBy(signin.SuperAdministrators(resource.SignInRoles(accessClient.UserManager()))),\n\t\tsession.MailSettings{Sender: settings.Mail, Application: Application, LoginURL: settings.MailLoginURL},\n\t\tsession.WithSessionTableName(sessionsTable),\n",
					"\t_, tenantAdministrators := AdministratorRoles()\n\taccessClient, err := access.New(store, settings.Collection,\n\t\taccess.WithDefaultRoles(Roles(), Name),\n",
					"\t\taccess.WithSuperAdministratorMethods(partnersidentity.SuperAdministratorMethods()...),\n",
					"\t\t// its directory has a problem; the sign-in rule and each request ask the engine.\n\t\taccess.WithTenantAdministratorRoles(tenantAdministrators...),\n\t)",
					"\tidentityClient, err := partnersidentity.NewClient(accessClient, authSession.API(), settings.TenantMaker, identity.WithFirstAdministratorRoles(FirstAdministratorRoles()), identity.WithAdministratorRoles(tenantAdministrators))\n\tif err != nil {\n\t\treturn nil, errors.Wrap(err, \"partnersidentity.NewClient()\")\n\t}\n\n\t// A role, a place in a group and a group's role may each be given for a period",
					// What a sign-in clears is set before what a sign-in gives: the identity
					// client's hook for a sign-in that stands, over the application's database.
					"\tauthSession.SetSignedInHook(identityClient.SignedIn(resource.NewSpannerClient(db)))\n\n\t// A person's own tenants, on their account page",
					"\tauthSession.SetOwnTenants(identityClient.OwnTenants(resource.NewSpannerClient(db)))\n\n\t// The directory admission",
					"\tauthSession.SetDirectoryAdmission(settings.TenantDirectories, identityClient.DirectorySeated())\n\n\t// An accepted invitation gives its roles",
					"\tauthSession.SetInvitationAccepted(identityClient.InvitationAccepted())\n",
					// The hook that gives a person who signs up their place: here they create a
					// tenant of their own, so the comment says an open method names no role.
					"\t// Here a person who signs up holds no role of the whole application: they create a\n\t// Tenant of their own, and an open method names no default role.\n\tauthSession.SetSignUpSeated(identityClient.SignedUp())\n\n\treturn &Auth{access: accessClient, handlers: handlers, identity: identityClient}, nil\n",
					"func (a *Auth) Identity() *partnersidentity.Client {\n\treturn a.identity\n}\n",
					"\t\"github.com/impulseframework/impulse/session/signin\"\n",
				} {
					if !strings.Contains(pkg, want) {
						t.Errorf("partners.go lacks %q:\n%s", want, pkg)
					}
				}
				if got := read(t, a, "pkg/config/data.go"); !strings.Contains(got, "\tpartnersAuth, err := partners.New(ctx, spannerClient, &partners.Settings{CookieKey: cookieKey, TenantDeclarations: app.NewTenantDeclarationReader()})\n") {
					t.Errorf("data.go = %q", got)
				}
			},
		},
		{
			name: "a password auth serving a tenanted site whose tenants have no directories carries no directory reader and sets no directory admission",
			auth: Auth{Name: "partners", Outlet: "default"},
			// The resource package declares a tenant record and no directories record: the
			// application is one of the invite-only shapes, whatever methods its auths have.
			extra: map[string]string{"pkg/config/data.go": declaredConfig, "pkg/resources/tenants.go": tenantRecord, programFile: strings.Replace(authRouterProgram, "\t\tgeneration.GenerateRouter(),\n", "\t\tgeneration.GenerateRouter(),\n\t\tgeneration.GenerateIdentity(\"pkg/identity\"),\n", 1)},
			wantDid: []string{
				packageDid + ", and the sign-in rule over the Tenant record's declaration (signin.TenantPolicy through the reader its settings carry, app.NewTenantDeclarationReader), admitting a person who holds no tenant yet (signin.AdmitWithoutTenant), since a person who signs up through a method an administrator opened creates their own, and what makes a tenant from an accepted invitation (TenantMaker in its settings, app.NewTenantMaker, which New refuses nil and hands the identity client: staff of this auth bring a tenant in by inviting one person)",
				migrationsDid, rolesDid, configDid, "ran go generate ./...",
			},
			wantSkipped: []string{
				"pkg/config/data.go: a tenant is made through the partners auth, whose outlet serves the Tenant record, and the construction copied from the staff auth hands it nothing to make one with, so it refuses to start (partners.Settings.TenantMaker is nil); build the roster before the auth and pass app.NewTenantMaker(<the roster>, <the live service>) in its Settings",
			},
			check: func(t *testing.T, a *app.App) {
				t.Helper()
				pkg := read(t, a, "pkg/auth/partners/partners.go")
				for _, want := range []string{
					"\tTenantDeclarations signin.DeclarationReader[*cloudspanner.ReadWriteTransaction]\n",
					"\t// (app.NewTenantDeclarationReader), which answers nothing for every tenant, since\n\t// the application has no tenant directories. Required; New refuses nil.\n",
					"\tTenantMaker identity.TenantMaker\n}",
					"\tif settings.TenantDeclarations == nil {\n",
					"\tauthSession.SetSignInPolicy(signin.TenantPolicy(resource.SignInRoles(accessClient.UserManager()), settings.TenantDeclarations, signin.AdmitWithoutTenant()))\n" + passwordDisabledCheck + "\n\t// The auth's identity client (pkg/identity, generated)",
					"\tidentityClient, err := partnersidentity.NewClient(accessClient, authSession.API(), settings.TenantMaker, identity.WithFirstAdministratorRoles(FirstAdministratorRoles()), identity.WithAdministratorRoles(tenantAdministrators))\n",
					// With no directory admission between them, what a sign-in clears is followed
					// at once by what an accepted invitation gives.
					"\tauthSession.SetSignedInHook(identityClient.SignedIn(resource.NewSpannerClient(db)))\n\n\t// A person's own tenants, on their account page",
					"\tauthSession.SetOwnTenants(identityClient.OwnTenants(resource.NewSpannerClient(db)))\n\n\t// An accepted invitation gives its roles",
					"\tauthSession.SetInvitationAccepted(identityClient.InvitationAccepted())\n",
				} {
					if !strings.Contains(pkg, want) {
						t.Errorf("partners.go lacks %q:\n%s", want, pkg)
					}
				}
				for _, unwanted := range []string{"TenantDirectories", "SetDirectoryAdmission", "DirectoryReader", "DirectorySeated"} {
					if strings.Contains(pkg, unwanted) {
						t.Errorf("partners.go names %s in an application whose tenants have no directories", unwanted)
					}
				}
			},
		},
		{
			name:    "a constructor that builds no literal keeps the file and its earlier edits",
			auth:    Auth{Name: "partners", Outlet: "default"},
			extra:   map[string]string{"pkg/config/data.go": builtElsewhereConfig},
			wantDid: []string{packageDid, migrationsDid, rolesDid, configDid, "ran go generate ./..."},
			wantSkipped: []string{
				"pkg/config/data.go: NewDataConfiguration builds no &DataConfiguration{...} literal, so the partners auth is declared but not constructed; construct it beside the staff auth and set the field",
				"pkg/config/data.go: Close releases the staff auth only; release the partners auth too",
			},
			check: func(t *testing.T, a *app.App) {
				t.Helper()
				got := read(t, a, "pkg/config/data.go")
				if !strings.Contains(got, "\tpartners      *partners.Auth\n") || !strings.Contains(got, "return assemble(spannerClient, staffAuth), nil") {
					t.Errorf("data.go = %q", got)
				}
			},
		},
		{
			name:  "a directory auth, the application its authority, read from the environment struct",
			auth:  Auth{Name: "partners", Methods: []authpkg.Method{{Kind: app.MethodAzure, Name: "directory", Authority: AuthorityApplication}}, Outlet: "default"},
			extra: map[string]string{"pkg/config/data.go": authConfigEnv},
			wantDid: []string{
				"pkg/auth/partners: the partners auth package (directory.go, foothold.go, partners.go), rendered from the skeleton's auth source: the Auth session over tables prefixed Partners, the partners permission store, and the methods directory (azure, role membership the application's), each in its own file",
				migrationsDid, rolesDid,
				"pkg/config/data.go: dataConfig reads the partners auth's directory registration from APP_PARTNERS_DIRECTORY_TENANT_ID, APP_PARTNERS_DIRECTORY_ADMINISTERED_TENANTS, APP_PARTNERS_DIRECTORY_CLIENT_ID, APP_PARTNERS_DIRECTORY_CLIENT_SECRET, APP_PARTNERS_DIRECTORY_REDIRECT_URL",
				configDid,
				"Procfile: 2 go run command(s) build with -tags skipAuth, so the partners auth's directory is simulated in development",
				"ran go generate ./...",
			},
			wantSkipped: []string{
				notInvited,
				"pkg/config/data.go: Close releases the staff auth only; release the partners auth too",
				"no environment template (.envrc.template, .env.template, .env.example) to add the directory method's variables to (APP_PARTNERS_DIRECTORY_TENANT_ID, APP_PARTNERS_DIRECTORY_ADMINISTERED_TENANTS, APP_PARTNERS_DIRECTORY_CLIENT_ID, APP_PARTNERS_DIRECTORY_CLIENT_SECRET, APP_PARTNERS_DIRECTORY_REDIRECT_URL)",
			},
			check: func(t *testing.T, a *app.App) {
				t.Helper()
				pkg := read(t, a, "pkg/auth/partners/directory.go")
				for _, want := range []string{`const DirectoryRegistration = "directory"`, "type DirectorySettings struct", "azure.Tenant(settings.Directory.TenantID)", "signin.DisableRoleSync()", "the method is invite only until an administrator opens it to sign-up", "func (h *Handlers) DirectoryFrontChannelLogout()"} {
					if !strings.Contains(pkg, want) {
						t.Errorf("directory.go lacks %q", want)
					}
				}
				main := read(t, a, "pkg/auth/partners/partners.go")
				for _, want := range []string{"\tdirectory *azure.Method\n", "handlers.directory, err = registerDirectory(authSession, accessClient, settings)", "\tLoginURL string\n", "\tDirectory DirectorySettings\n"} {
					if !strings.Contains(main, want) {
						t.Errorf("partners.go lacks %q", want)
					}
				}
				got := read(t, a, "pkg/config/data.go")
				for _, want := range []string{
					"\tPartnersDirectoryTenantID            string `env:\"APP_PARTNERS_DIRECTORY_TENANT_ID\"`\n",
					"\tPartnersDirectoryClientSecret        string `env:\"APP_PARTNERS_DIRECTORY_CLIENT_SECRET\"        secret:\"true\"`\n",
					"partnersAuth, err := partners.New(ctx, spannerClient, &partners.Settings{\n\t\tCookieKey:      cookieKey,\n\t\tSessionTimeout: env.SessionTimeout,\n\t\tLoginURL:       \"/login\",\n\t\tDirectory: partners.DirectorySettings{\n\t\t\tTenantID:            env.PartnersDirectoryTenantID,\n\t\t\tAdministeredTenants: env.PartnersDirectoryAdministeredTenants,\n\t\t\tClientID:            env.PartnersDirectoryClientID,\n\t\t\tClientSecret:        env.PartnersDirectoryClientSecret,\n\t\t\tRedirectURL:         env.PartnersDirectoryRedirectURL,\n\t\t},\n\t})",
				} {
					if !strings.Contains(got, want) {
						t.Errorf("data.go lacks %q:\n%s", want, got)
					}
				}
				if procfile := read(t, a, "Procfile"); strings.Count(procfile, "go run -tags skipAuth ") != 2 {
					t.Errorf("Procfile = %q", procfile)
				}
			},
		},
		{
			name:  "a directory auth served by the default outlet gets its callback as the console's development address",
			auth:  Auth{Name: "partners", Methods: []authpkg.Method{{Kind: app.MethodAzure, Name: "directory", Authority: AuthorityApplication}}, Outlet: "default"},
			extra: map[string]string{programFile: authRouterProgram, ".envrc.template": "export PORT=8090\n"},
			wantDid: []string{
				".envrc.template: the directory method's variables (APP_PARTNERS_DIRECTORY_TENANT_ID, APP_PARTNERS_DIRECTORY_ADMINISTERED_TENANTS, APP_PARTNERS_DIRECTORY_CLIENT_ID, APP_PARTNERS_DIRECTORY_CLIENT_SECRET, APP_PARTNERS_DIRECTORY_REDIRECT_URL), to fill in; APP_PARTNERS_DIRECTORY_REDIRECT_URL is set to the console project's development callback, http://127.0.0.1:4300/console/api/user/directory/callback",
			},
			check: func(t *testing.T, a *app.App) {
				t.Helper()
				if env := read(t, a, ".envrc.template"); !strings.Contains(env, "export APP_PARTNERS_DIRECTORY_REDIRECT_URL=http://127.0.0.1:4300/console/api/user/directory/callback\n") {
					t.Errorf(".envrc.template lacks the callback:\n%s", env)
				}
			},
		},
		{
			name:  "a Google directory auth with the directory as authority reads roles from the groups, served by the portal outlet",
			auth:  Auth{Name: "partners", Methods: []authpkg.Method{{Kind: app.MethodGoogle, Name: "workspace", Authority: AuthorityDirectory}}, Outlet: "portal"},
			extra: routed,
			wantDid: []string{
				"pkg/auth/partners: the partners auth package (partners.go, workspace.go), rendered from the skeleton's auth source: the Auth session over tables prefixed Partners, the partners permission store, and the methods workspace (google, role membership the directory's), each in its own file",
				programFile + `: the portal outlet's declaration names the partners auth with its methods, generation.Auth("example.com/acme/beacon/pkg/auth/partners", generation.GoogleMethod("workspace").SyncsRoles()), in place of the staff auth's, so the regenerated router mounts the auth's session group under /portal/api: one login route per method, the methods route its login page renders from, and the pending sign-in routes`,
			},
			// The flat layout's browser projects are left as they were: rebinding the
			// portal's is the agent's, as the brief says.
			neverDid: []string{"app.routes.ts", "XSRF cookie"},
			wantSkipped: []string{
				"pkg/config/data.go: no environment struct (env := &T{}) to add the partners auth's registration variables to; read them and pass them in partners.Settings",
				`test/integration/harness_test.go and cmd/deployment/migrate/main_test.go: the workspace registration validates its organization slot and its role groups' prefix at registration whether or not the directory is simulated, so give the partners auth's construction in the harness Workspace: partners.WorkspaceSettings{HostedDomain: "example.com", RedirectURL: <the served callback>, GroupPrefix: "partners-"} and set APP_PARTNERS_WORKSPACE_HOSTED_DOMAIN=example.com and APP_PARTNERS_WORKSPACE_GROUP_PREFIX=partners- where the migrate run's environment is`,
			},
			check: func(t *testing.T, a *app.App) {
				t.Helper()
				pkg := read(t, a, "pkg/auth/partners/workspace.go")
				for _, want := range []string{"google.Workspace(WorkspaceRegistration, settings.Workspace.HostedDomain)", "google.RoleSync(accessClient.UserManager(), settings.Workspace.GroupPrefix, lookup)", "google.ParseGroupLookup(settings.Workspace.GroupLookup)", "\tGroupPrefix string\n"} {
					if !strings.Contains(pkg, want) {
						t.Errorf("workspace.go lacks %q", want)
					}
				}
				if got := read(t, a, "pkg/config/data.go"); !strings.Contains(got, "partnersAuth, err := partners.New(ctx, spannerClient, &partners.Settings{\n\t\tCookieKey: cookieKey,\n\t\tLoginURL:  \"/portal/login\",\n\t})") {
					t.Errorf("data.go construction = %q", got)
				}
				program := read(t, a, programFile)
				if want := "\t\tgeneration.WithRouterOutlet(\"portal\", \"portal/api\",\n\t\t\tgeneration.Auth(\"example.com/acme/beacon/pkg/auth/partners\", generation.GoogleMethod(\"workspace\").SyncsRoles()),\n\t\t\tgeneration.WebApp(\"/portal\"),\n\t\t),"; !strings.Contains(program, want) {
					t.Errorf("program lacks %q:\n%s", want, program)
				}
				if want := "\t\tgeneration.GenerateRoutes(\"pkg/router\", \"console/api\",\n\t\t\tgeneration.Auth(\"example.com/acme/beacon/pkg/auth/staff\", generation.PasswordMethod()),"; !strings.Contains(program, want) {
					t.Errorf("the console's declaration moved:\n%s", program)
				}
			},
		},
		{
			// The program generates each auth's management: the new auth's packages come
			// with the regeneration, and the accessor the generated contract asks for is
			// named as the agent's to write.
			name:  "an auth bound under a program that generates each auth's management names the accessor left to write",
			auth:  Auth{Name: "partners", Outlet: "portal"},
			extra: map[string]string{programFile: strings.Replace(authRouterProgram, "\t\tgeneration.GenerateRouter(),\n", "\t\tgeneration.GenerateRouter(),\n\t\tgeneration.GenerateIdentity(\"pkg/identity\"),\n", 1)},
			wantSkipped: []string{
				invited,
				handed,
				"no file declares a Configurer interface, so the App is not given the partners auth: the generated router's Handlers requires the portal outlet's session handlers from the partners auth (partners.Auth.Handlers()), and the generated contract asks the handlers for PartnersIdentity() *partnersidentity.Client (partners.Auth.Identity()), and the library answers a request the router binds to the partners auth only from that auth's permission engine; the application does not compile until the App holds them",
			},
			// The default outlet still declares the staff auth, so its management stays.
			neverSkipped: []string{"no outlet declares"},
		},
		{
			// The portal was the one outlet declaring its auth: bound to another, the auth
			// has no declaration left, and what names its generated packages is named as
			// the agent's to remove.
			name:  "an outlet bound away from the one declaration of its auth names what the regeneration leaves broken",
			auth:  Auth{Name: "partners", Outlet: "portal"},
			extra: map[string]string{programFile: strings.Replace(strings.Replace(authRouterProgram, "\t\tgeneration.GenerateRouter(),\n", "\t\tgeneration.GenerateRouter(),\n\t\tgeneration.GenerateIdentity(\"pkg/identity\"),\n", 1), "generation.WithRouterOutlet(\"portal\", \"portal/api\",\n\t\t\tgeneration.Auth(\"example.com/acme/beacon/pkg/auth/staff\"", "generation.WithRouterOutlet(\"portal\", \"portal/api\",\n\t\t\tgeneration.Auth(\"example.com/acme/beacon/pkg/auth/members\"", 1)},
			wantSkipped: []string{
				programFile + ": no outlet declares the members auth any longer, so the regeneration removes its generated management under pkg/identity (membersidentity, membershandlers and internal/membersresources); remove what names it, which no longer compiles: the client the auth's package builds in New with the hooks it sets from it and its Identity method (or the package, where nothing signs in through the auth any longer), and MembersIdentity() on the Configurer, on the App and on every test configurer",
			},
		},
		{
			name:  "a Google auth whose roles the application assigns declares no account rule, and its method file names none",
			auth:  Auth{Name: "partners", Methods: []authpkg.Method{{Kind: app.MethodGoogle, Name: "workspace", Authority: AuthorityApplication}}, Outlet: "portal"},
			extra: routed,
			wantDid: []string{
				"pkg/auth/partners: the partners auth package (foothold.go, partners.go, workspace.go), rendered from the skeleton's auth source: the Auth session over tables prefixed Partners, the partners permission store, and the methods workspace (google, role membership the application's), each in its own file",
				programFile + `: the portal outlet's declaration names the partners auth with its methods, generation.Auth("example.com/acme/beacon/pkg/auth/partners", generation.GoogleMethod("workspace")), in place of the staff auth's, so the regenerated router mounts the auth's session group under /portal/api: one login route per method, the methods route its login page renders from, and the pending sign-in routes`,
			},
			check: func(t *testing.T, a *app.App) {
				t.Helper()
				pkg := read(t, a, "pkg/auth/partners/workspace.go")
				for _, want := range []string{"the method is invite only until an administrator opens it to sign-up", "google.DisableRoleSync()", "google.Workspace(WorkspaceRegistration, settings.Workspace.HostedDomain)"} {
					if !strings.Contains(pkg, want) {
						t.Errorf("workspace.go lacks %q", want)
					}
				}
				for _, retired := range []string{"Accounts:", "signin.MembersOfDirectory", "signin.InvitedUsersOnly", "signin.OpenSignUp"} {
					if strings.Contains(pkg, retired) {
						t.Errorf("workspace.go names an account rule (%s), which no registration takes:\n%s", retired, pkg)
					}
				}
				program := read(t, a, programFile)
				if want := "\t\tgeneration.WithRouterOutlet(\"portal\", \"portal/api\",\n\t\t\tgeneration.Auth(\"example.com/acme/beacon/pkg/auth/partners\", generation.GoogleMethod(\"workspace\")),\n\t\t\tgeneration.WebApp(\"/portal\"),\n\t\t),"; !strings.Contains(program, want) {
					t.Errorf("program lacks %q:\n%s", want, program)
				}
			},
		},
		{
			name: "an identity provider's auth whose token names the roles, where the tenants declare their directories",
			auth: Auth{Name: "employees", Methods: []authpkg.Method{{Kind: app.MethodOIDC, Name: "company-sso", Authority: AuthorityDirectory}}, Outlet: "portal"},
			extra: map[string]string{
				programFile:                           authRouterProgram,
				"pkg/resources/tenant_directories.go": tenantDirectoriesRecord,
			},
			wantDid: []string{
				// The auth's identity provider was given no words: the brief says what people
				// read for it and names the file where its own name goes.
				programFile + `: people read the company-sso method as "Company Sso", words made from its registration name, on the login page's button (Sign in with Company Sso) and in the lists of a person's ways to sign in; to show the provider's own name, chain .Labeled("<words>") on the declaration (generation.OIDCMethod("company-sso").Labeled("Lakeside ID")) and run go generate ./...`,
			},
			wantSkipped: []string{
				`test/integration/harness_test.go and cmd/deployment/migrate/main_test.go: the company-sso registration validates its issuer at registration whether or not the directory is simulated, so give the employees auth's construction in the harness CompanySso: employees.CompanySsoSettings{Issuer: "https://provider.example.com", RedirectURL: <the served callback>} and set APP_EMPLOYEES_COMPANY_SSO_ISSUER=https://provider.example.com where the migrate run's environment is`,
				`pkg/resources/tenant_directories.go: the tenants declare the directory their people enter through (TenantDirectory), and a sign-in through company-sso is a directory's to the sign-in policy, method oidc under the connection "company-sso": a tenant's administrator who is signed in through it makes it the tenant's directory on the console's Sign-in page, and a tenant that is directory only then admits the people who sign in through it and nobody else, while a tenant that is not admits everyone it seats, however they signed in. The record's method column must admit oidc for a tenant to take the provider: add tenancy composes the column's check with google, azure, workos and oidc, so a table whose check names fewer needs a migration that adds oidc, and the Sign-in page names the directory by what its directoryKinds calls the method`,
			},
			check: func(t *testing.T, a *app.App) {
				t.Helper()
				pkg := read(t, a, "pkg/auth/employees/company-sso.go")
				for _, want := range []string{`const CompanySsoRegistration = "company-sso"`, "ClientSecret: settings.CompanySso.ClientSecret,", "RoleSync:     signin.RoleSync(accessClient.UserManager()),", "the provider decides who\n// the method's people are, so the method has no invite-only or open choice"} {
					if !strings.Contains(pkg, want) {
						t.Errorf("company-sso.go lacks %q:\n%s", want, pkg)
					}
				}
				program := read(t, a, programFile)
				if want := `generation.Auth("example.com/acme/beacon/pkg/auth/employees", generation.OIDCMethod("company-sso").SyncsRoles())`; !strings.Contains(program, want) {
					t.Errorf("program lacks %q:\n%s", want, program)
				}
			},
		},
		{
			name:  "a password auth served by the default outlet under the generated router",
			auth:  Auth{Name: "partners", Outlet: "default"},
			extra: routed,
			wantDid: []string{
				programFile + `: the default outlet's declaration names the partners auth with its methods, generation.Auth("example.com/acme/beacon/pkg/auth/partners", generation.PasswordMethod()), in place of the staff auth's, so the regenerated router mounts the auth's session group under /console/api: one login route per method, the methods route its login page renders from, and the pending sign-in routes`,
			},
			check: func(t *testing.T, a *app.App) {
				t.Helper()
				program := read(t, a, programFile)
				if want := "\t\tgeneration.GenerateRoutes(\"pkg/router\", \"console/api\",\n\t\t\tgeneration.Auth(\"example.com/acme/beacon/pkg/auth/partners\", generation.PasswordMethod()),\n\t\t\tgeneration.WebApp(\"/console\"),\n\t\t),"; !strings.Contains(program, want) {
					t.Errorf("program lacks %q:\n%s", want, program)
				}
				if strings.Count(program, "pkg/auth/staff") != 2 {
					t.Errorf("the staff auth is named %d times, want twice (the portal's Auth and the machines outlet's Tokens):\n%s", strings.Count(program, "pkg/auth/staff"), program)
				}
			},
		},
		{
			name:        "a directory auth on a module that never required the OIDC library",
			auth:        Auth{Name: "partners", Methods: []authpkg.Method{{Kind: app.MethodGoogle, Name: "workspace", Authority: AuthorityApplication}}, Outlet: "portal"},
			extra:       routed,
			tidiedGoMod: beaconGoMod + "\nrequire github.com/coreos/go-oidc/v3 v3.21.0 // indirect\n",
			wantDid: []string{
				"go.mod: requires github.com/coreos/go-oidc/v3 v3.21.0, the OIDC library the session library's directory methods sign in through (go mod tidy)",
				"ran go generate ./...",
			},
		},
		{
			name:        "generation fails",
			auth:        Auth{Name: "partners", Methods: []authpkg.Method{{Kind: app.MethodGoogle, Name: "workspace", Authority: AuthorityApplication}}, Outlet: "portal"},
			extra:       routed,
			generateErr: errors.New("exit status 1"),
			wantErr:     "go generate ./... failed, so the outlet's routes for the auth are not generated yet; fix the cause and run it:\ngeneration: boom",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			files := authFiles(t)
			for rel, content := range tt.extra {
				files[rel] = content
			}
			a := beacon(t, files)
			exec := &fakeExec{err: tt.generateErr}
			if tt.tidiedGoMod != "" {
				exec.tidy = tidied(a, tt.tidiedGoMod)
			}
			ch, err := tt.auth.Apply(t.Context(), a, exec)
			// An auth rendered with its identity client imports a package the regeneration
			// writes, so the generators run between a tidy that tolerates the missing
			// package and the one that holds the module whole.
			wantCalls := regenerated
			if strings.Contains(files[programFile], "GenerateIdentity") {
				wantCalls = []string{"go mod tidy -e", "go generate ./...", "go mod tidy"}
			}
			if diff := cmp.Diff(wantCalls, exec.calls); diff != "" {
				t.Errorf("exec calls mismatch (-want +got):\n%s", diff)
			}
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Apply() error = %v, want %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("Apply() error = %v", err)
			}
			for _, want := range tt.wantDid {
				if !slices.Contains(ch.Did, want) {
					t.Errorf("Did lacks %q; got:\n%s", want, strings.Join(ch.Did, "\n"))
				}
			}
			for _, want := range tt.wantSkipped {
				if !slices.Contains(ch.Skipped, want) {
					t.Errorf("Skipped lacks %q; got:\n%s", want, strings.Join(ch.Skipped, "\n"))
				}
			}
			for _, never := range tt.neverSkipped {
				for _, skipped := range ch.Skipped {
					if strings.Contains(skipped, never) {
						t.Errorf("Skipped carries %q: %s", never, skipped)
					}
				}
			}
			for _, never := range tt.neverDid {
				for _, did := range ch.Did {
					if strings.Contains(did, never) {
						t.Errorf("Did carries %q: %s", never, did)
					}
				}
			}
			if tt.tidiedGoMod == "" {
				noGoModLine(t, ch.Did)
			}
			if tt.check != nil {
				tt.check(t, a)
			}
		})
	}
}

// siteRouterProgram is a site's generator program in the sites layout: the generated
// router, the site's default outlet bound to the staff auth and serving its browser
// application at the root, and the generated client in the site's workspace.
func siteRouterProgram(site string) string {
	return strings.ReplaceAll(`// Package main is the <site> site's resource generator program.
package main

import (
	"context"
	"log"

	"github.com/impulseframework/impulse/generation"
)

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	generator, err := generation.NewResourceGenerator(
		ctx,
		"apps/<site>/pkg/resources",
		[]string{"file://schema/migrations"},
		generation.GenerateHandlers("apps/<site>/app"),
		generation.GenerateRouter(),
		generation.GenerateRoutes("apps/<site>/pkg/router", "api",
			generation.Auth("example.com/acme/beacon/pkg/auth/staff", generation.PasswordMethod()),
			generation.WebApp("/"),
		),
		generation.GenerateHandlerTests("apps/<site>/test/authz"),
		generation.WithSpannerEmulatorVersion("1.5.56"),
		generation.GenerateTypescript("apps/<site>/web/<site>/src/app/core/service",
			generation.GenerateMetadata(),
			generation.GeneratePermissions(),
			generation.GenerateEnums(),
		),
	)
	if err != nil {
		return err
	}
	defer generator.Close()

	return generator.Generate()
}
`, "<site>", site)
}

// The browser files bound to an auth, by their path under a project's source root: the
// application configuration, which names the auth's XSRF cookie, the routes, which ask
// the library for the pages of the auth's generated management, and the menu file, which
// places them.
var boundWebFiles = []string{
	"app/app.config.ts",
	"app/app.routes.ts",
	"app/app.menus.ts",
}

// skeletonWeb reads one file of the base skeleton's console project by its path under
// the project's source root, as the skeleton renders it for the staff auth.
func skeletonWeb(t *testing.T, rel string) string {
	t.Helper()

	sub, err := skeleton.FS(skeleton.Base)
	if err != nil {
		t.Fatal(err)
	}
	data, err := fs.ReadFile(sub, "web/console/src/"+rel)
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

// sitesApp writes an application in the sites layout, the console and the portal, each
// with its generator program bound to the staff auth, its browser workspace with the
// project named for the site, and the skeleton's routes and application configuration:
// the console's as the skeleton renders them, its program generating the management of
// the auth's people, and the portal's as impulse add site lays a copy in, without the
// management and its pages; plus the staff auth's files, and with the edits applied to
// the named files. Then it discovers the application.
func sitesApp(t *testing.T, edits map[string]func(text string) string) *app.App {
	t.Helper()

	files := authFiles(t)
	files["go.mod"] = "module example.com/acme/beacon\n\ngo 1.26\n"
	for _, site := range []string{"console", "portal"} {
		files["cmd/generate/"+site+"generator/main.go"] = siteRouterProgram(site)
		if site == "console" {
			// The first site's program generates the management; the copy does not.
			files["cmd/generate/consolegenerator/main.go"] = strings.Replace(siteRouterProgram(site), "\t\tgeneration.GenerateRouter(),\n", "\t\tgeneration.GenerateIdentity(\"apps/console/pkg/identity\"),\n\t\tgeneration.GenerateRouter(),\n", 1)
		}
		files["apps/"+site+"/pkg/resources/doc.go"] = "package resources\n"
		files["apps/"+site+"/app/doc.go"] = "package app\n"
		files["apps/"+site+"/pkg/router/router.go"] = "package router\n"
		files["apps/"+site+"/web/angular.json"] = strings.ReplaceAll(beaconAngular, "console", site)
		files["apps/"+site+"/web/package.json"] = strings.ReplaceAll(beaconPackage, "console", site)
		source := "apps/" + site + "/web/" + site + "/src/"
		files[source+"app/core/service/zz_gen_api.ts"] = "// generated\n"
		for _, rel := range boundWebFiles {
			files[source+rel] = skeletonWeb(t, rel)
		}
		if site == "portal" {
			// As impulse add site lays the copy in: without the pages of the management.
			for _, rel := range []string{appRoutesFile, appMenusFile} {
				edited, found := dropIdentityRoutes(files[source+rel])
				if !found {
					t.Fatalf("the base skeleton's %s asks for no pages of the auth's management: the fixture is not the copy add site makes", rel)
				}
				files[source+rel] = edited
			}
		}
	}
	for rel, edit := range edits {
		files[rel] = edit(files[rel])
	}
	root := t.TempDir()
	for rel, content := range files {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a, err := app.Discover(root)
	if err != nil {
		t.Fatalf("app.Discover() error = %v", err)
	}

	return a
}

func TestAuthApplyRebindsSiteWeb(t *testing.T) {
	t.Parallel()

	const (
		portalSource  = "apps/portal/web/portal/src/"
		portalRoutes  = portalSource + "app/app.routes.ts"
		portalMenus   = portalSource + "app/app.menus.ts"
		portalConfig  = portalSource + "app/app.config.ts"
		portalProgram = "cmd/generate/portalgenerator/main.go"
		consoleSource = "apps/console/web/console/src/"
		consoleRoutes = consoleSource + "app/app.routes.ts"
		consoleMenus  = consoleSource + "app/app.menus.ts"
		consoleConfig = consoleSource + "app/app.config.ts"
	)
	members := Auth{Name: "members", Methods: []authpkg.Method{{Kind: app.MethodGoogle, Name: "google", Authority: AuthorityApplication}}, Outlet: "portal"}
	partners := Auth{Name: "partners", Outlet: "console"}
	const (
		renderedConfig = portalConfig + ": its HttpClient names the members auth's XSRF cookie (members-xsrf) in place of the staff auth's; the rest of the file is as it was"
		ownTreeDid     = portalProgram + `: generation.GenerateIdentity("apps/portal/pkg/identity") added: the program generated no management, and the members auth is the only auth it declares, so the management of the auth's people is generated into apps/portal/pkg/identity and the auth's package builds its identity client from it`
		noPages        = portalRoutes + ": asks the library for no pages of the staff auth's management, so nothing was rewritten and the members auth's people have no page; spread identityRoutes(apiDescriptor, resourceMeta, { auth: 'Members', scope: 'global' }) among the routes behind the login guard, as the base skeleton's routes do: the library builds the accounts, the invitations, the sessions and the failed sign-ins from the API description"
		noMenuPages    = portalMenus + ": asks the library for no pages of the staff auth's management, so nothing was rewritten and the members auth's people have no page; spread identityMenuPages(apiDescriptor, resourceMeta, { auth: 'Members', scope: 'global' }) among a menu's entries, as the base skeleton's menu file does: the library builds the accounts, the invitations, the sessions and the failed sign-ins from the API description"
		staffPages     = "      ...identityRoutes(apiDescriptor, resourceMeta, { auth: 'Staff', scope: 'global' }),\n"
	)
	// A provider of the site's own in its application configuration, and a page of the
	// site's own among its routes: the files are no longer the skeleton's.
	provider := func(text string) string {
		return strings.Replace(text, "    provideNativeDateAdapter(),\n", "    provideNativeDateAdapter(),\n    providePortalTheme(),\n", 1)
	}
	page := func(text string) string {
		return strings.Replace(text, "      { path: '**', redirectTo: 'dashboard' },\n", "      { path: 'orders', loadComponent: () => import('./orders').then((comp) => comp.Orders) },\n      { path: '**', redirectTo: 'dashboard' },\n", 1)
	}
	// A portal whose program declares the staff auth on a second outlet too, and whose
	// routes still ask for the staff's pages: the program gains no tree for the members,
	// since it would generate the staff's management a second time.
	secondOutlet := func(text string) string {
		return strings.Replace(text, "\t\tgeneration.GenerateHandlerTests(", "\t\tgeneration.WithRouterOutlet(\"desk\", \"desk/api\",\n\t\t\tgeneration.Auth(\"example.com/acme/beacon/pkg/auth/staff\", generation.PasswordMethod()),\n\t\t),\n\t\tgeneration.GenerateHandlerTests(", 1)
	}
	keptPages := func(string) string {
		return skeletonWeb(t, appRoutesFile)
	}
	keptMenus := func(string) string {
		return skeletonWeb(t, appMenusFile)
	}

	tests := []struct {
		name        string
		auth        Auth
		edits       map[string]func(text string) string
		wantDid     []string
		wantSkipped []string
		// wantText and neverText are the substrings each file carries and lacks after
		// the change.
		wantText  map[string][]string
		neverText map[string][]string
	}{
		{
			name:        "the portal, a copy of the console that serves no management, takes a tree of its own and the auth's cookie",
			auth:        members,
			wantDid:     []string{ownTreeDid, renderedConfig},
			wantSkipped: []string{noPages, noMenuPages},
			wantText: map[string][]string{
				portalProgram:                 {"\t\tgeneration.GenerateHandlers(\"apps/portal/app\"),\n\t\t// The management of the members auth's people is generated into apps/portal/pkg/identity, a tree this\n", "\t\tgeneration.GenerateIdentity(\"apps/portal/pkg/identity\"),\n"},
				portalConfig:                  {"withXsrfConfiguration({ cookieName: 'members-xsrf' })", "the members auth's (pkg/auth/members, XSRFCookie)"},
				"pkg/auth/members/members.go": {`"example.com/acme/beacon/apps/portal/pkg/identity/membersidentity"`, "identityClient, err := membersidentity.NewClient("},
				consoleRoutes:                 {staffPages},
				consoleConfig:                 {"cookieName: 'staff-xsrf'"},
			},
			neverText: map[string][]string{
				portalConfig: {"staff"},
				portalRoutes: {"identityRoutes"},
				portalMenus:  {"identityMenuPages"},
			},
		},
		{
			name:    "an application configuration with providers of its own keeps them and names the auth's cookie",
			auth:    members,
			edits:   map[string]func(string) string{portalConfig: provider},
			wantDid: []string{portalConfig + ": its HttpClient names the members auth's XSRF cookie (members-xsrf) in place of the staff auth's; the rest of the file is as it was"},
			wantText: map[string][]string{
				portalConfig: {"    providePortalTheme(),\n", "withXsrfConfiguration({ cookieName: 'members-xsrf' })", "the members auth's (pkg/auth/members, XSRFCookie)"},
			},
			neverText: map[string][]string{portalConfig: {"staff"}},
		},
		{
			name:        "an application configuration naming no cookie of the auth is left to the agent",
			auth:        members,
			edits:       map[string]func(string) string{portalConfig: func(string) string { return "export const appConfig = { providers: [] };\n" }},
			wantSkipped: []string{portalConfig + ": names no XSRF cookie of the staff auth, so nothing was rewritten; name the members auth's (members-xsrf) in withXsrfConfiguration, since the browser echoes that cookie in X-XSRF-TOKEN and the server verifies the echo"},
		},
		{
			name:  "a program that declares another auth gains no tree, and the pages of the auth it no longer serves are taken out",
			auth:  members,
			edits: map[string]func(string) string{portalProgram: secondOutlet, portalRoutes: keptPages, portalMenus: keptMenus},
			wantDid: []string{
				portalRoutes + ": without the pages of the staff auth's management (the identityRoutes spreads and what they alone used): the outlet's program generates no management of the members auth, so its API description names no such page, and the library refuses a request for one when the application loads",
				portalMenus + ": without the pages of the staff auth's management (the identityMenuPages spreads and what they alone used): the outlet's program generates no management of the members auth, so its API description names no such page, and the library refuses a request for one when the application loads",
				renderedConfig,
			},
			wantSkipped: []string{portalProgram + ": the program generates no management of its auths' people (no generation.GenerateIdentity) and declares another auth beside the members auth, so the tool adds none: the members auth is rendered without an identity client, and nothing creates its people, gives them a role or invites an address until the application does; impulse check refuses an auth whose roles the application gives while it builds no identity client (first-administrator). To have it generated, add generation.GenerateIdentity with a tree of the program's own: it generates the management of every auth the program declares, and each auth's package builds its client from that tree in New, as an auth the tool renders for such a program does"},
			// The generated metadata stays imported: the Feature flags page, which is no page
			// of the management, reads it in both files.
			wantText: map[string][]string{
				portalRoutes: {"import { resourceMeta } from '@app/service/zz_gen_resources';\n", "      featureFlagsRoute(resourceMeta),\n"},
				portalMenus:  {"import { resourceMeta } from '@app/service/zz_gen_resources';\n", "            featureFlagsPage(resourceMeta),\n"},
			},
			neverText: map[string][]string{
				portalProgram:                 {"GenerateIdentity"},
				portalRoutes:                  {"identityRoutes", "apiDescriptor"},
				portalMenus:                   {"identityMenuPages", "apiDescriptor"},
				"pkg/auth/members/members.go": {"identityClient", "membersidentity"},
			},
		},
		{
			name: "the console's outlet rebound rewrites the console's browser application and leaves the portal's",
			auth: partners,
			wantDid: []string{
				consoleRoutes + ": rendered again from the skeleton's for the partners auth: it asks the library for the pages of the partners auth's management (identityRoutes(apiDescriptor, resourceMeta, { auth: 'Partners', scope: 'global' })) in place of the staff auth's",
				consoleMenus + ": rendered again from the skeleton's for the partners auth: it asks the library for the pages of the partners auth's management (identityMenuPages(apiDescriptor, resourceMeta, { auth: 'Partners', scope: 'global' })) in place of the staff auth's",
				consoleConfig + ": its HttpClient names the partners auth's XSRF cookie (partners-xsrf) in place of the staff auth's; the rest of the file is as it was",
			},
			wantText: map[string][]string{
				consoleRoutes:                   {"      ...identityRoutes(apiDescriptor, resourceMeta, { auth: 'Partners', scope: 'global' }),\n", "// The partners's accounts"},
				consoleMenus:                    {"            ...identityMenuPages(apiDescriptor, resourceMeta, { auth: 'Partners', scope: 'global' }),\n", "// The partners's accounts"},
				consoleConfig:                   {"cookieName: 'partners-xsrf'"},
				portalConfig:                    {"cookieName: 'staff-xsrf'"},
				"pkg/auth/partners/partners.go": {`"example.com/acme/beacon/apps/console/pkg/identity/partnersidentity"`},
			},
			neverText: map[string][]string{consoleRoutes: {"Staff", "staff"}, consoleMenus: {"Staff", "staff"}},
		},
		{
			name:    "routes edited since the skeleton rendered them are rewritten in place",
			auth:    partners,
			edits:   map[string]func(string) string{consoleRoutes: page},
			wantDid: []string{consoleRoutes + ": its requests for the pages of an auth's management name the partners auth (auth: 'Partners') in place of the staff auth's; the rest of the file, edited since the skeleton rendered it, is as it was"},
			wantText: map[string][]string{
				consoleRoutes: {"      ...identityRoutes(apiDescriptor, resourceMeta, { auth: 'Partners', scope: 'global' }),\n", "{ path: 'orders', loadComponent: () => import('./orders').then((comp) => comp.Orders) },"},
			},
			neverText: map[string][]string{consoleRoutes: {"auth: 'Staff'"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := sitesApp(t, tt.edits)
			ch, err := tt.auth.Apply(t.Context(), a, &fakeExec{})
			if err != nil {
				t.Fatalf("Apply() error = %v", err)
			}
			for _, want := range tt.wantDid {
				if !slices.Contains(ch.Did, want) {
					t.Errorf("Did lacks %q; got:\n%s", want, strings.Join(ch.Did, "\n"))
				}
			}
			for _, want := range tt.wantSkipped {
				if !slices.Contains(ch.Skipped, want) {
					t.Errorf("Skipped lacks %q; got:\n%s", want, strings.Join(ch.Skipped, "\n"))
				}
			}
			for rel, wants := range tt.wantText {
				text := read(t, a, rel)
				for _, want := range wants {
					if !strings.Contains(text, want) {
						t.Errorf("%s lacks %q:\n%s", rel, want, text)
					}
				}
			}
			for rel, nevers := range tt.neverText {
				text := read(t, a, rel)
				for _, never := range nevers {
					if strings.Contains(text, never) {
						t.Errorf("%s still carries %q:\n%s", rel, never, text)
					}
				}
			}
		})
	}
}

// TestAuthApplyRebindsDialogsSpec pins what add auth does to the test of the dialogs in
// the browser application it rebinds. The base skeleton's test of the dialog that invites
// a person asks the library for the pages of the base auth's management and expects that
// auth's invite method. Where nobody edited it, it is rendered again for the new auth, or
// written as the outlets reference's, which has no test of that dialog, where the outlet's
// program generates no management of the new auth. One edited since is never rewritten in
// place: where it still asks for the base auth's pages it is left and a skipped line says
// what to change, and any other is left without a word.
func TestAuthApplyRebindsDialogsSpec(t *testing.T) {
	t.Parallel()

	const (
		portalSpec    = "apps/portal/web/portal/src/" + appDialogsSpecFile
		portalProgram = "cmd/generate/portalgenerator/main.go"
	)
	members := Auth{Name: "members", Methods: []authpkg.Method{{Kind: app.MethodGoogle, Name: "google", Authority: AuthorityApplication}}, Outlet: "portal"}
	base := skeletonWeb(t, appDialogsSpecFile)
	staffRequest := "auth: 'Staff'"
	if !strings.Contains(base, staffRequest) {
		t.Fatalf("the base skeleton's %s holds no %s: the fixture is not the test this one pins", appDialogsSpecFile, staffRequest)
	}
	reference, err := referenceText(ReferenceCandidate, appDialogsSpecFile)
	if err != nil {
		t.Fatal(err)
	}
	// A portal whose program declares the staff auth on a second outlet too, so the
	// program gains no tree for the members and generates no management of them.
	unmanaged := func(text string) string {
		return strings.Replace(text, "\t\tgeneration.GenerateHandlerTests(", "\t\tgeneration.WithRouterOutlet(\"desk\", \"desk/api\",\n\t\t\tgeneration.Auth(\"example.com/acme/beacon/pkg/auth/staff\", generation.PasswordMethod()),\n\t\t),\n\t\tgeneration.GenerateHandlerTests(", 1)
	}
	spec := func(text string) func(string) string {
		return func(string) string {
			return text
		}
	}
	edited := "// The portal's own case, added after the skeleton rendered the file.\n" + base
	own := "// the application's own dialogs test\n"

	tests := []struct {
		name        string
		edits       map[string]func(text string) string
		wantText    string
		wantDid     string
		wantSkipped string
	}{
		{
			name: "no test of the dialogs is left without a word",
		},
		{
			name:     "the skeleton's test, where the program generates the auth's management, is rendered again for the auth",
			edits:    map[string]func(string) string{portalSpec: spec(base)},
			wantText: names.Rename(base, skeleton.PlaceholderAuth, "members"),
			wantDid:  portalSpec + ": rendered again from the skeleton's for the members auth: its test of the dialog that invites a person asks the library for the pages of the members auth's management (identityPages(apiDescriptor, resourceMeta, { auth: 'Members', scope: 'global' })) and expects that auth's invite method, in place of the staff auth's",
		},
		{
			name:     "the skeleton's test, where the program generates no management of the auth, is written without the test of the invite dialog",
			edits:    map[string]func(string) string{portalSpec: spec(base), portalProgram: unmanaged},
			wantText: reference,
			wantDid:  portalSpec + ": written again without its test of the dialog that invites a person: the outlet's program generates no management of the members auth, so its API description names no such page, and the library refuses a request for one",
		},
		{
			name:        "an edited test that asks for the old auth's pages, where the program generates the auth's management, is left with what to change",
			edits:       map[string]func(string) string{portalSpec: spec(edited)},
			wantText:    edited,
			wantSkipped: portalSpec + " was edited since the skeleton rendered it, so it was left as it is: it asks the library for the pages of the staff auth's management (auth: 'Staff'), which the outlet's API description no longer carries; name the members auth there (auth: 'Members') and expect its methods, or take the test out: the web-wired check refuses the application until then",
		},
		{
			name:        "an edited test that asks for the old auth's pages, where the program generates no management of the auth, is left with what to take out",
			edits:       map[string]func(string) string{portalSpec: spec(edited), portalProgram: unmanaged},
			wantText:    edited,
			wantSkipped: portalSpec + " was edited since the skeleton rendered it, so it was left as it is: it asks the library for the pages of the staff auth's management (auth: 'Staff'), which the outlet's API description no longer carries, and the outlet's program generates no management of the members auth to ask for in its place; take the test of those pages out: the web-wired check refuses the application until then",
		},
		{
			name:     "a test of the application's own that asks for no auth's pages is left without a word",
			edits:    map[string]func(string) string{portalSpec: spec(own)},
			wantText: own,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := sitesApp(t, tt.edits)
			ch, err := members.Apply(t.Context(), a, &fakeExec{})
			if err != nil {
				t.Fatalf("Apply() error = %v", err)
			}
			if tt.wantText == "" {
				if _, err := os.Stat(a.Abs(portalSpec)); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("%s was written where the application had none (stat error %v)", portalSpec, err)
				}
			} else if diff := cmp.Diff(tt.wantText, read(t, a, portalSpec)); diff != "" {
				t.Errorf("dialogs test mismatch (-want +got):\n%s", diff)
			}
			var said []string
			for _, line := range slices.Concat(ch.Did, ch.Skipped) {
				if strings.Contains(line, appDialogsSpecFile) {
					said = append(said, line)
				}
			}
			var want []string
			for _, line := range []string{tt.wantDid, tt.wantSkipped} {
				if line != "" {
					want = append(want, line)
				}
			}
			if diff := cmp.Diff(want, said); diff != "" {
				t.Errorf("what the brief says of the dialogs test mismatch (-want +got):\n%s", diff)
			}
			if tt.wantDid != "" && !slices.Contains(ch.Did, tt.wantDid) {
				t.Errorf("Did lacks %q; got:\n%s", tt.wantDid, strings.Join(ch.Did, "\n"))
			}
			if tt.wantSkipped != "" && !slices.Contains(ch.Skipped, tt.wantSkipped) {
				t.Errorf("Skipped lacks %q; got:\n%s", tt.wantSkipped, strings.Join(ch.Skipped, "\n"))
			}
		})
	}
}

// TestOutletMoveLeavesDialogsSpec pins that an outlet's move leaves the base skeleton's
// test of the dialogs as it is: in the console the default outlet's move rewrites in
// place, and in the project an outlet's copy makes from that console. The commands that
// write the file again compare it with the base's as rendered, so the file a move leaves
// must still be that text: the test names its API URL through the application's
// environment, which the move rewrites in its place.
func TestOutletMoveLeavesDialogsSpec(t *testing.T) {
	t.Parallel()

	base := skeletonWeb(t, appDialogsSpecFile)
	console := moveRewrites(apiSegment, "/", "console/"+apiSegment, "/console")
	copied := moveRewrites("console/"+apiSegment, "/console", "partners/"+apiSegment, "/partners")
	for _, tt := range []struct {
		name  string
		moves [][]rewrite
	}{
		{name: "the console the default outlet's move rewrites in place", moves: [][]rewrite{console}},
		{name: "the project an outlet's copy makes from that console", moves: [][]rewrite{console, copied}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Each move applies its rewrites in order, as copyProject does.
			moved := base
			for _, rewrites := range tt.moves {
				for _, r := range rewrites {
					moved = strings.ReplaceAll(moved, r.old, r.new)
				}
			}
			if diff := cmp.Diff(base, moved); diff != "" {
				t.Errorf("an outlet's move rewrites the base skeleton's %s (-base +moved):\n%s\nimpulse add tenancy, impulse add auth and a Directory start compare this file with the base's as rendered, so it must name its API URL through the environment (environment.apiUrl), never as a prefix of its own", appDialogsSpecFile, diff)
			}
		})
	}
}

// Test_servesTenantRecord pins which auth's people create a tenant of their own: the
// auth bound to an outlet the tenant record is served on, which is the default outlet
// when the record names none. An auth bound to any other outlet has nobody who signs
// up, so its sign-in rule refuses a person who holds no tenant.
func Test_servesTenantRecord(t *testing.T) {
	t.Parallel()

	record := &app.TenantRecord{File: "pkg/resources/tenants.go", Name: "Tenant"}
	tests := []struct {
		name    string
		members []app.OutletMember
		site    *app.Site
		outlet  string
		want    bool
	}{
		{name: "the default outlet, the record naming none", site: &app.Site{TenantRecord: record}, outlet: app.DefaultOutletName, want: true},
		{name: "another outlet, the record naming none", site: &app.Site{TenantRecord: record}, outlet: "portal"},
		{
			name:    "an outlet the record names",
			members: []app.OutletMember{{File: "pkg/resources/tenants.go", Name: "Tenant", Outlets: []string{"default", "portal"}}},
			site:    &app.Site{TenantRecord: record}, outlet: "portal", want: true,
		},
		{
			name:    "an outlet the record does not name",
			members: []app.OutletMember{{File: "pkg/resources/tenants.go", Name: "Tenant", Outlets: []string{"portal"}}},
			site:    &app.Site{TenantRecord: record}, outlet: app.DefaultOutletName,
		},
		{
			name:    "another struct's outlets say nothing of the record",
			members: []app.OutletMember{{File: "pkg/resources/ships.go", Name: "Ship", Outlets: []string{"portal"}}},
			site:    &app.Site{TenantRecord: record}, outlet: "portal",
		},
		{name: "a site without tenants", site: &app.Site{}, outlet: app.DefaultOutletName},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := &app.App{OutletMembers: tt.members}
			if got := servesTenantRecord(a, tt.site, &app.Outlet{Name: tt.outlet}); got != tt.want {
				t.Errorf("servesTenantRecord() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestWithoutRegistrations pins what a copy of the settings function of an auth that signs
// in through a directory leaves behind: the login page the directory sends a person back
// to and the registration's settings, elements of the function's Settings literal as the
// outlets skeleton's data level writes them for its members auth. The Settings of an auth
// with no directory method have neither field, so a copy that kept them would not
// compile. Everything else of the copy stands, the page a mailed link opens among it, and
// so does a literal of another auth's.
func TestWithoutRegistrations(t *testing.T) {
	t.Parallel()

	sub, err := skeleton.FS("outlets")
	if err != nil {
		t.Fatal(err)
	}
	data, err := fs.ReadFile(sub, "pkg/config/data.go")
	if err != nil {
		t.Fatal(err)
	}
	level := string(data)
	const (
		page         = "\t\tLoginURL:       \"/portal/login\",\n"
		registration = "\t\tDirectory: members.DirectorySettings{\n\t\t\tTenantID:            env.MembersDirectoryTenantID,\n\t\t\tAdministeredTenants: env.MembersDirectoryAdministeredTenants,\n\t\t\tClientID:            env.MembersDirectoryClientID,\n\t\t\tClientSecret:        env.MembersDirectoryClientSecret,\n\t\t\tRedirectURL:         env.MembersDirectoryRedirectURL,\n\t\t},\n"
		before       = "\t\tMailLoginURL:   env.PublicURL + \"/portal/login\",\n"
		after        = "\t\tTenantDeclarations: app.NewTenantDeclarationReader(),\n"
	)
	if carried := before + page + registration + after; !strings.Contains(level, carried) {
		t.Fatalf("the outlets skeleton's data level no longer carries the members auth's registration as %q, which withoutRegistrations reads", carried)
	}

	got := withoutRegistrations(level, "members")
	if !strings.Contains(got, before+after) {
		t.Errorf("withoutRegistrations() does not leave %q:\n%s", before+after, got)
	}
	if want := strings.Replace(level, page+registration, "", 1); got != want {
		t.Errorf("withoutRegistrations() took out more or less than the page and the registration (-want +got):\n%s", cmp.Diff(want, got))
	}

	// Under another auth's name the registration is not the copy's own to lose: the page
	// alone goes.
	if got, want := withoutRegistrations(level, "staff"), strings.Replace(level, page, "", 1); got != want {
		t.Errorf("withoutRegistrations() under another auth's name (-want +got):\n%s", cmp.Diff(want, got))
	}
}

// TestAuthApply_settingsBesideADirectoryAuth holds the settings function impulse add auth
// copies for the new auth from an auth that signs in through a directory, in an
// application started with Directory roles: the copy carries nothing of the staff auth's
// registration, and the new auth's own registration joins it once, so the data level
// compiles whichever way the new auth signs in. The staff auth's function keeps its own.
func TestAuthApply_settingsBesideADirectoryAuth(t *testing.T) {
	t.Parallel()

	const base = "func (env *dataConfig) partnersSettings(collection access.PermissionCollection, signals partners.Signals, cookieKey string, sender mail.Sender) *partners.Settings {\n\treturn &partners.Settings{\n\t\tCollection:     collection,\n\t\tSignals:        signals,\n\t\tCookieKey:      cookieKey,\n\t\tSessionTimeout: env.SessionTimeout,\n\t\tMail:           sender,\n\t\tMailLoginURL:   env.PublicURL + \"/login\",\n"
	tests := []struct {
		name    string
		methods []authpkg.Method
		// want is the function's Settings literal after the elements every auth has.
		want string
	}{
		{
			name: "an auth whose people sign in with a password takes no registration",
			want: "\t}\n}\n",
		},
		{
			name:    "an auth with a registration of its own takes its own alone",
			methods: []authpkg.Method{{Kind: app.MethodGoogle, Name: "workspace", Authority: AuthorityApplication}},
			want:    "\t\tLoginURL:       \"/login\",\n\t\tWorkspace: partners.WorkspaceSettings{\n\t\t\tHostedDomain:           env.PartnersWorkspaceHostedDomain,\n\t\t\tAdministeredWorkspaces: env.PartnersWorkspaceAdministeredWorkspaces,\n\t\t\tClientID:               env.PartnersWorkspaceClientID,\n\t\t\tClientSecret:           env.PartnersWorkspaceClientSecret,\n\t\t\tRedirectURL:            env.PartnersWorkspaceRedirectURL,\n\t\t},\n\t}\n}\n",
		},
		{
			name:    "an auth with a registration named as the staff auth's takes its own once, with its own fields",
			methods: []authpkg.Method{{Kind: app.MethodGoogle, Name: "google", Authority: AuthorityApplication}},
			want:    "\t\tLoginURL:       \"/login\",\n\t\tGoogle: partners.GoogleSettings{\n\t\t\tHostedDomain:           env.PartnersGoogleHostedDomain,\n\t\t\tAdministeredWorkspaces: env.PartnersGoogleAdministeredWorkspaces,\n\t\t\tClientID:               env.PartnersGoogleClientID,\n\t\t\tClientSecret:           env.PartnersGoogleClientSecret,\n\t\t\tRedirectURL:            env.PartnersGoogleRedirectURL,\n\t\t},\n\t}\n}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := directoryRoles(t)
			staffs := "\t\tLoginURL:       \"/login\",\n\t\tGoogle: staff.GoogleSettings{\n\t\t\tHostedDomain:           env.StaffGoogleHostedDomain,\n"
			if level := read(t, a, baseDataLevel); !strings.Contains(level, staffs) {
				t.Fatalf("the start's data level does not carry the staff auth's registration as %q:\n%s", staffs, level)
			}
			added(t, a, &Auth{Name: "partners", Methods: tt.methods, Outlet: app.DefaultOutletName})

			level := read(t, a, baseDataLevel)
			if want := base + tt.want; !strings.Contains(level, want) {
				t.Errorf("%s does not hold the partners auth's settings as %q:\n%s", baseDataLevel, want, level)
			}
			if !strings.Contains(level, staffs) {
				t.Errorf("%s lost the staff auth's registration:\n%s", baseDataLevel, level)
			}
			wellFormed(t, a, baseDataLevel)
		})
	}
}

// TestWithoutMaker pins what a copy of the wiring of the auth a tenant is made through
// leaves behind for an auth that makes none: the settings function's parameter and the
// paragraph of its comment that names it, the element of its Settings literal, and the
// construction's argument, as the tenanted skeleton's data level writes them. The Settings
// of an auth a tenant is not made through have no TenantMaker field, so a copy that kept
// any of them would not compile. Everything else of the copy stands.
func TestWithoutMaker(t *testing.T) {
	t.Parallel()

	sub, err := skeleton.FS("tenanted")
	if err != nil {
		t.Fatal(err)
	}
	data, err := fs.ReadFile(sub, "pkg/config/data.go")
	if err != nil {
		t.Fatal(err)
	}
	level := string(data)
	for _, carried := range []string{
		"//\n// tenants is what makes a tenant from an accepted invitation",
		"sender mail.Sender, tenants identity.TenantMaker) *staff.Settings {",
		"\t\tTenantMaker:        tenants,\n",
		"sender, app.NewTenantMaker(tenants, liveService)))\n",
	} {
		if !strings.Contains(level, carried) {
			t.Fatalf("the tenanted skeleton's data level no longer carries the maker as %q, which withoutMaker reads", carried)
		}
	}

	got := withoutMaker(level)
	for _, left := range []string{"TenantMaker", "NewTenantMaker", "tenants is what makes a tenant"} {
		if strings.Contains(got, left) {
			t.Errorf("withoutMaker() left %q behind:\n%s", left, got)
		}
	}
	for _, kept := range []string{
		"// so the constructor stays one call per auth.\nfunc (env *dataConfig) staffSettings(collection access.PermissionCollection, signals staff.Signals, cookieKey string, sender mail.Sender) *staff.Settings {\n",
		"\t\tTenantDeclarations: app.NewTenantDeclarationReader(),\n\t\tTenantDirectories:  app.NewTenantDirectoryReader(),\n\t\tSessionTimeout:     env.SessionTimeout,\n",
		"\tstaffAuth, err := staff.New(ctx, db.SpannerClient, env.staffSettings(router.Collection(), liveService, cookieKey, sender))\n",
		"\ttenants := app.NewTenantRoster(db.ResourceClient, resource.WithTenantSignals(liveService))\n",
	} {
		if !strings.Contains(got, kept) {
			t.Errorf("withoutMaker() result lacks %q:\n%s", kept, got)
		}
	}

	// A construction carrying its Settings literal inline, as an application composed
	// before the settings function has it.
	inline := "\tpartnersAuth, err := partners.New(ctx, spannerClient, &partners.Settings{CookieKey: cookieKey, TenantDeclarations: app.NewTenantDeclarationReader(), TenantMaker: app.NewTenantMaker(tenants, liveService)})\n"
	if got, want := withoutMaker(inline), "\tpartnersAuth, err := partners.New(ctx, spannerClient, &partners.Settings{CookieKey: cookieKey, TenantDeclarations: app.NewTenantDeclarationReader()})\n"; got != want {
		t.Errorf("withoutMaker() of an inline literal = %q, want %q", got, want)
	}
}
