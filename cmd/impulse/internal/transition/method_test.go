package transition

import (
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/authpkg"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton"
)

func TestMethodCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method Method
		want   string
	}{
		{name: "password", method: Method{Auth: "staff", Spec: authpkg.Method{Kind: app.MethodPassword}}, want: "impulse add method staff password"},
		{name: "a directory method with its shape", method: Method{Auth: "members", Spec: authpkg.Method{Kind: app.MethodGoogle, Name: "workspace", Authority: AuthorityApplication, Slot: authpkg.SlotListed}}, want: "impulse add method members google workspace --authority application --slot listed"},
		{name: "an organization's identity provider under its name", method: Method{Auth: "staff", Spec: authpkg.Method{Kind: app.MethodOIDC, Name: "okta", Authority: AuthorityApplication}}, want: "impulse add method staff oidc okta --authority application"},
		{name: "an organization's identity provider with the words its people read", method: Method{Auth: "staff", Spec: authpkg.Method{Kind: app.MethodOIDC, Name: "company-sso", Authority: AuthorityApplication, Label: "Lakeside ID"}}, want: `impulse add method staff oidc company-sso --authority application --label "Lakeside ID"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.method.Command(); got != tt.want {
				t.Errorf("Command() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMethodValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		method  Method
		wantErr string
	}{
		{name: "a Google method on the staff auth", method: Method{Auth: "staff", Spec: authpkg.Method{Kind: app.MethodGoogle, Name: "workspace", Authority: AuthorityApplication}}},
		{name: "no such auth", method: Method{Auth: "partners", Spec: authpkg.Method{Kind: app.MethodPassword}}, wantErr: "no auth package pkg/auth/partners"},
		{name: "the registration exists", method: Method{Auth: "staff", Spec: authpkg.Method{Kind: app.MethodPassword}}, wantErr: "the staff auth registers password already"},
		{name: "a directory method without an authority", method: Method{Auth: "staff", Spec: authpkg.Method{Kind: app.MethodAzure, Name: "directory"}}, wantErr: "the directory method needs --authority"},
		{name: "a slot the kind lacks", method: Method{Auth: "staff", Spec: authpkg.Method{Kind: app.MethodAzure, Name: "directory", Authority: AuthorityApplication, Slot: "galaxy"}}, wantErr: "galaxy"},
		{name: "the directory authority with any organization", method: Method{Auth: "staff", Spec: authpkg.Method{Kind: app.MethodAzure, Name: "directory", Authority: AuthorityDirectory, Slot: authpkg.SlotAny}}, wantErr: "role synchronization follows one tenant's roles"},
		{name: "the directory authority beside the password method", method: Method{Auth: "staff", Spec: authpkg.Method{Kind: app.MethodGoogle, Name: "workspace", Authority: AuthorityDirectory}}, wantErr: "a registration whose directory writes the roles stands alone on its auth"},
		{name: "an identity provider on the staff auth", method: Method{Auth: "staff", Spec: authpkg.Method{Kind: app.MethodOIDC, Name: "okta", Authority: AuthorityApplication}}},
		{name: "an identity provider without an authority", method: Method{Auth: "staff", Spec: authpkg.Method{Kind: app.MethodOIDC, Name: "okta"}}, wantErr: "the okta method needs --authority"},
		{name: "an identity provider whose token names the roles, beside the password method", method: Method{Auth: "staff", Spec: authpkg.Method{Kind: app.MethodOIDC, Name: "okta", Authority: AuthorityDirectory}}, wantErr: "a registration whose directory writes the roles stands alone on its auth"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			files := authFiles(t)
			files["cmd/generate/resourcegenerator/main.go"] = beaconRouterProgram
			err := tt.method.Validate(beacon(t, files))
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

func TestMethodApply(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method Method
		extra  map[string]string
		// without are fixture files the case's application does not have.
		without     []string
		wantDid     []string
		wantSkipped []string
		// wantNotSkipped are words no skipped line carries: an obligation the shape does
		// not have.
		wantNotSkipped []string
		check          func(t *testing.T, a *app.App)
		// tidiedGoMod is the go.mod the tidy script leaves; empty leaves go.mod as it is.
		tidiedGoMod string
		tidyErr     error
		generateErr error
		wantErr     string
	}{
		{
			name:   "a Google method under the application authority",
			method: Method{Auth: "staff", Spec: authpkg.Method{Kind: app.MethodGoogle, Name: "workspace", Authority: AuthorityApplication}},
			wantDid: []string{
				"pkg/auth/staff/workspace.go: the workspace method (google, one Workspace, role membership the application's): its registration constant, its settings, its handlers (WorkspaceLogin and the rest), and registerWorkspace, which registers it on the Auth",
				"pkg/auth/staff/staff.go: Handlers holds the workspace method (workspace *google.Method), New registers it (registerWorkspace), and Settings carries its WorkspaceSettings and the LoginURL, and the package doc lists it, and its first sentence says the auth's people also sign in with an account of the organization's Google Workspace",
				`cmd/generate/resourcegenerator/main.go: generation.GoogleMethod("workspace") declared on 1 outlet Auth option(s) bound to the staff auth, so the generated router mounts user/workspace/login and the callback and the methods route offers it`,
				"Procfile: 2 go run command(s) build with -tags skipAuth, so the staff auth's directory is simulated in development",
				"ran go generate ./..., so the router mounts the method's routes and the login page offers it",
			},
			wantSkipped: []string{
				"pkg/config/data.go: no environment struct (env := &T{}) to add the workspace method's variables to; read APP_STAFF_WORKSPACE_HOSTED_DOMAIN, APP_STAFF_WORKSPACE_ADMINISTERED_WORKSPACES, APP_STAFF_WORKSPACE_CLIENT_ID, APP_STAFF_WORKSPACE_CLIENT_SECRET, APP_STAFF_WORKSPACE_REDIRECT_URL and pass them in staff.Settings.Workspace",
				"no environment template (.envrc.template, .env.template, .env.example) to add the workspace method's variables to (APP_STAFF_WORKSPACE_HOSTED_DOMAIN, APP_STAFF_WORKSPACE_ADMINISTERED_WORKSPACES, APP_STAFF_WORKSPACE_CLIENT_ID, APP_STAFF_WORKSPACE_CLIENT_SECRET, APP_STAFF_WORKSPACE_REDIRECT_URL)",
				`test/integration/harness_test.go and cmd/deployment/migrate/main_test.go: the workspace registration validates its organization slot at registration whether or not the directory is simulated, so give the staff auth's construction in the harness Workspace: staff.WorkspaceSettings{HostedDomain: "example.com", RedirectURL: <the served callback>} and set APP_STAFF_WORKSPACE_HOSTED_DOMAIN=example.com where the migrate run's environment is`,
			},
			wantNotSkipped: []string{"GroupPrefix", "GROUP_PREFIX", "role groups' prefix"},
			check: func(t *testing.T, a *app.App) {
				t.Helper()
				file := read(t, a, "pkg/auth/staff/workspace.go")
				for _, want := range []string{"package staff", `const WorkspaceRegistration = "workspace"`, "type WorkspaceSettings struct", "func (h *Handlers) WorkspaceLogin() http.HandlerFunc", "func (h *Handlers) WorkspaceCallback() http.HandlerFunc", "func registerWorkspace(authSession *Session, accessClient *access.Client, settings *Settings) (*google.Method, error)", "google.Workspace(WorkspaceRegistration, settings.Workspace.HostedDomain)", "the method is invite only until an administrator opens it to sign-up", "google.DisableRoleSync()"} {
					if !strings.Contains(file, want) {
						t.Errorf("workspace.go lacks %q:\n%s", want, file)
					}
				}
				main := read(t, a, "pkg/auth/staff/staff.go")
				for _, want := range []string{"\tpassword  *session.PasswordMethod\n\tworkspace *google.Method\n", "\thandlers.password = registerPassword(authSession)\n", "\tauthSession.SetSignUpSeated(identityClient.SignedUp())\n\thandlers.workspace, err = registerWorkspace(authSession, accessClient, settings)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n\n\treturn &Auth{access: accessClient, handlers: handlers, identity: identityClient}, nil", "\tWorkspace WorkspaceSettings\n", "\tLoginURL string\n", "\t\"github.com/impulseframework/impulse/session/google\"\n"} {
					if !strings.Contains(main, want) {
						t.Errorf("staff.go lacks %q:\n%s", want, main)
					}
				}
				if program := read(t, a, "cmd/generate/resourcegenerator/main.go"); !strings.Contains(program, `generation.Auth("example.com/acme/beacon/pkg/auth/staff", generation.PasswordMethod(), generation.GoogleMethod("workspace"))`) {
					t.Errorf("program = %s", program)
				}
			},
		},
		{
			name:   "a Google method whose roles the application assigns starts invite only, and the brief names the grants",
			method: Method{Auth: "staff", Spec: authpkg.Method{Kind: app.MethodGoogle, Name: "workspace", Authority: AuthorityApplication}},
			wantDid: []string{
				"pkg/auth/staff/workspace.go: the workspace method (google, one Workspace, role membership the application's): its registration constant, its settings, its handlers (WorkspaceLogin and the rest), and registerWorkspace, which registers it on the Auth",
				`cmd/generate/resourcegenerator/main.go: generation.GoogleMethod("workspace") declared on 1 outlet Auth option(s) bound to the staff auth, so the generated router mounts user/workspace/login and the callback and the methods route offers it`,
			},
			check: func(t *testing.T, a *app.App) {
				t.Helper()
				file := read(t, a, "pkg/auth/staff/workspace.go")
				for _, retired := range []string{"Accounts:", "signin.InvitedUsersOnly", "signin.MembersOfDirectory", "signin.OpenSignUp"} {
					if strings.Contains(file, retired) {
						t.Errorf("workspace.go names an account rule (%s), which no registration takes:\n%s", retired, file)
					}
				}
				if program := read(t, a, "cmd/generate/resourcegenerator/main.go"); !strings.Contains(program, `generation.Auth("example.com/acme/beacon/pkg/auth/staff", generation.PasswordMethod(), generation.GoogleMethod("workspace"))`) {
					t.Errorf("program = %s", program)
				}
				meaning := (&Method{Auth: "staff", Spec: authpkg.Method{Kind: app.MethodGoogle, Name: "workspace", Authority: AuthorityApplication}}).Meaning()
				if !strings.Contains(meaning, "the generated management of the staff auth makes both (the `InviteStaffUser` and `CreateStaffUser` methods), so grant Execute on them to the role that invites in the role file") {
					t.Errorf("Meaning() lacks the grants on the methods that invite and create:\n%s", meaning)
				}
			},
		},
		{
			name:   "a Google method admitting any Google account is as wide as the slot says and no wider in code: it starts invite only too",
			method: Method{Auth: "staff", Spec: authpkg.Method{Kind: app.MethodGoogle, Name: "sign-up", Slot: authpkg.SlotAny, Authority: AuthorityApplication}},
			wantDid: []string{
				"pkg/auth/staff/sign-up.go: the sign-up method (google, any Google account, role membership the application's): its registration constant, its settings, its handlers (SignUpLogin and the rest), and registerSignUp, which registers it on the Auth",
				`cmd/generate/resourcegenerator/main.go: generation.GoogleMethod("sign-up") declared on 1 outlet Auth option(s) bound to the staff auth, so the generated router mounts user/sign-up/login and the callback and the methods route offers it`,
			},
			check: func(t *testing.T, a *app.App) {
				t.Helper()
				file := read(t, a, "pkg/auth/staff/sign-up.go")
				for _, retired := range []string{"Accounts:", "signin.OpenSignUp", "signin.MembersOfDirectory"} {
					if strings.Contains(file, retired) {
						t.Errorf("sign-up.go names an account rule (%s), which no registration takes:\n%s", retired, file)
					}
				}
				if program := read(t, a, "cmd/generate/resourcegenerator/main.go"); !strings.Contains(program, `generation.Auth("example.com/acme/beacon/pkg/auth/staff", generation.PasswordMethod(), generation.GoogleMethod("sign-up"))`) {
					t.Errorf("program = %s", program)
				}
				meaning := (&Method{Auth: "staff", Spec: authpkg.Method{Kind: app.MethodGoogle, Name: "sign-up", Slot: authpkg.SlotAny, Authority: AuthorityApplication}}).Meaning()
				for _, want := range []string{
					"Its organization slot is any Google account, which is how wide the method is.",
					"`OpenStaffSignUp` opens one, after a warning that says who that admits for a method this wide",
					"with tenants they hold no role and create a tenant of their own through the generated create-your-tenant route, which refuses while no method is open",
				} {
					if !strings.Contains(meaning, want) {
						t.Errorf("Meaning() lacks %q:\n%s", want, meaning)
					}
				}
				for _, retired := range []string{"@tenant(creation", ".OpenSignUp()", "signin.OpenSignUp"} {
					if strings.Contains(meaning, retired) {
						t.Errorf("Meaning() names %s, which is no longer declared:\n%s", retired, meaning)
					}
				}
			},
		},
		{
			name:   "an Entra method under the application authority, read from the environment struct",
			method: Method{Auth: "staff", Spec: authpkg.Method{Kind: app.MethodAzure, Name: "directory", Authority: AuthorityApplication}},
			extra:  map[string]string{"pkg/config/data.go": authConfigEnv},
			wantDid: []string{
				"pkg/auth/staff/directory.go: the directory method (azure, one tenant, role membership the application's): its registration constant, its settings, its handlers (DirectoryLogin and the rest), and registerDirectory, which registers it on the Auth",
				"pkg/config/data.go: dataConfig reads the staff auth's directory registration from APP_STAFF_DIRECTORY_TENANT_ID, APP_STAFF_DIRECTORY_ADMINISTERED_TENANTS, APP_STAFF_DIRECTORY_CLIENT_ID, APP_STAFF_DIRECTORY_CLIENT_SECRET, APP_STAFF_DIRECTORY_REDIRECT_URL, and the staff.Settings literal in NewDataConfiguration (an application composed before the staffSettings method) passes them",
				`cmd/generate/resourcegenerator/main.go: generation.AzureMethod("directory") declared on 1 outlet Auth option(s) bound to the staff auth, so the generated router mounts user/directory/login, the callback and the front-channel logout and the methods route offers it`,
			},
			wantSkipped: []string{
				"pkg/config/data.go: LoginURL is set to /login when the construction had none; make it the login page of the surface bound to the staff auth",
				`test/integration/harness_test.go and cmd/deployment/migrate/main_test.go: the directory registration validates its organization slot at registration whether or not the directory is simulated, so give the staff auth's construction in the harness Directory: staff.DirectorySettings{TenantID: "00000000-0000-0000-0000-000000000000", RedirectURL: <the served callback>} and set APP_STAFF_DIRECTORY_TENANT_ID=00000000-0000-0000-0000-000000000000 where the migrate run's environment is`,
			},
			wantNotSkipped: []string{"GroupPrefix", "GROUP_PREFIX", "role groups' prefix"},
			check: func(t *testing.T, a *app.App) {
				t.Helper()
				file := read(t, a, "pkg/auth/staff/directory.go")
				for _, want := range []string{"azure.Tenant(settings.Directory.TenantID)", "signin.DisableRoleSync()", "func (h *Handlers) DirectoryFrontChannelLogout() http.HandlerFunc"} {
					if !strings.Contains(file, want) {
						t.Errorf("directory.go lacks %q:\n%s", want, file)
					}
				}
				data := read(t, a, "pkg/config/data.go")
				for _, want := range []string{
					"\tStaffDirectoryTenantID            string `env:\"APP_STAFF_DIRECTORY_TENANT_ID\"`\n",
					"\tStaffDirectoryClientSecret        string `env:\"APP_STAFF_DIRECTORY_CLIENT_SECRET\"        secret:\"true\"`\n",
					"\t\tDirectory: staff.DirectorySettings{\n\t\t\tTenantID:            env.StaffDirectoryTenantID,\n\t\t\tAdministeredTenants: env.StaffDirectoryAdministeredTenants,\n\t\t\tClientID:            env.StaffDirectoryClientID,\n\t\t\tClientSecret:        env.StaffDirectoryClientSecret,\n\t\t\tRedirectURL:         env.StaffDirectoryRedirectURL,\n\t\t},\n",
					"\t\tLoginURL:       \"/login\",\n",
				} {
					if !strings.Contains(data, want) {
						t.Errorf("data.go lacks %q:\n%s", want, data)
					}
				}
			},
		},
		{
			name:   "an Entra method's settings join the staff auth's settings function",
			method: Method{Auth: "staff", Spec: authpkg.Method{Kind: app.MethodAzure, Name: "directory", Authority: AuthorityApplication}},
			extra:  map[string]string{"pkg/config/data.go": authConfigSettings},
			wantDid: []string{
				"pkg/config/data.go: dataConfig reads the staff auth's directory registration from APP_STAFF_DIRECTORY_TENANT_ID, APP_STAFF_DIRECTORY_ADMINISTERED_TENANTS, APP_STAFF_DIRECTORY_CLIENT_ID, APP_STAFF_DIRECTORY_CLIENT_SECRET, APP_STAFF_DIRECTORY_REDIRECT_URL, and dataConfig.staffSettings passes them",
			},
			wantSkipped: []string{
				"pkg/config/data.go: LoginURL is set to /login when the construction had none; make it the login page of the surface bound to the staff auth",
			},
			check: func(t *testing.T, a *app.App) {
				t.Helper()
				data := read(t, a, "pkg/config/data.go")
				for _, want := range []string{
					// The constructor is as it was: the settings come from the function.
					"\tstaffAuth, err := staff.New(ctx, spannerClient, env.staffSettings(cookieKey))\n",
					"\tStaffDirectoryTenantID            string `env:\"APP_STAFF_DIRECTORY_TENANT_ID\"`\n",
					"func (env *dataConfig) staffSettings(cookieKey string) *staff.Settings {\n\treturn &staff.Settings{\n\t\tCookieKey:      cookieKey,\n\t\tSessionTimeout: env.SessionTimeout,\n\t\tLoginURL:       \"/login\",\n\t\tDirectory: staff.DirectorySettings{\n\t\t\tTenantID:            env.StaffDirectoryTenantID,\n\t\t\tAdministeredTenants: env.StaffDirectoryAdministeredTenants,\n\t\t\tClientID:            env.StaffDirectoryClientID,\n\t\t\tClientSecret:        env.StaffDirectoryClientSecret,\n\t\t\tRedirectURL:         env.StaffDirectoryRedirectURL,\n\t\t},\n\t}\n}\n",
				} {
					if !strings.Contains(data, want) {
						t.Errorf("data.go lacks %q:\n%s", want, data)
					}
				}
			},
		},
		{
			name:   "an organization's identity provider where the tenants declare their directories: the declaration has to admit it",
			method: Method{Auth: "staff", Spec: authpkg.Method{Kind: app.MethodOIDC, Name: "okta", Authority: AuthorityApplication}},
			extra: map[string]string{
				"pkg/config/data.go":                  authConfigSettings,
				"pkg/resources/tenant_directories.go": "package resources\n\ntype (\n\t// @resource\n\t// @permissionScope(domain)\n\t// @tenantDirectory\n\tTenantDirectory struct {\n\t\t// @domain\n\t\tTenantID string `spanner:\"TenantId\"`\n\t\t// @directoryMethod\n\t\tMethod string `spanner:\"Method\"`\n\t\t// @directoryConnection\n\t\tConnection string `spanner:\"Connection\"`\n\t}\n)\n",
			},
			wantSkipped: []string{
				`pkg/resources/tenant_directories.go: the tenants declare the directory their people enter through (TenantDirectory), and a sign-in through okta is a directory's to the sign-in policy, method oidc under the connection "okta": a tenant's administrator who is signed in through it makes it the tenant's directory on the console's Sign-in page, and a tenant that is directory only then admits the people who sign in through it and nobody else, while a tenant that is not admits everyone it seats, however they signed in. The record's method column must admit oidc for a tenant to take the provider: add tenancy composes the column's check with google, azure, workos and oidc, so a table whose check names fewer needs a migration that adds oidc, and the Sign-in page names the directory by what its directoryKinds calls the method`,
			},
		},
		{
			name:   "an organization's identity provider, registered in every environment, with a secret and an issuer to give the harness",
			method: Method{Auth: "staff", Spec: authpkg.Method{Kind: app.MethodOIDC, Name: "okta", Authority: AuthorityApplication}},
			extra:  map[string]string{"pkg/config/data.go": authConfigSettings},
			wantDid: []string{
				"pkg/auth/staff/okta.go: the okta method (oidc, one organization's identity provider, role membership the application's): its registration constant, its settings, its handlers (OktaLogin and the rest), and registerOkta, which registers it on the Auth",
				"pkg/auth/staff/staff.go: Handlers holds the okta method (okta *oidc.Method), New registers it (registerOkta), and Settings carries its OktaSettings and the LoginURL, and the package doc lists it, and its first sentence says the auth's people also sign in through the organization's own identity provider",
				"pkg/config/data.go: dataConfig reads the staff auth's okta registration from APP_STAFF_OKTA_ISSUER, APP_STAFF_OKTA_CLIENT_ID, APP_STAFF_OKTA_CLIENT_SECRET, APP_STAFF_OKTA_REDIRECT_URL, and dataConfig.staffSettings passes them",
				`cmd/generate/resourcegenerator/main.go: generation.OIDCMethod("okta") declared on 1 outlet Auth option(s) bound to the staff auth, so the generated router mounts user/okta/login and the callback and the methods route offers it`,
				// No words were given: the brief says what people read and names the file
				// where the provider's own name goes.
				`cmd/generate/resourcegenerator/main.go: people read the okta method as "Okta", words made from its registration name, on the login page's button (Sign in with Okta) and in the lists of a person's ways to sign in; to show the provider's own name, chain .Labeled("<words>") on the declaration (generation.OIDCMethod("okta").Labeled("Lakeside ID")) and run go generate ./...`,
				"Procfile: 2 go run command(s) build with -tags skipAuth, so the staff auth's directory is simulated in development",
				"ran go generate ./..., so the router mounts the method's routes and the login page offers it",
			},
			wantSkipped: []string{
				"pkg/config/data.go: LoginURL is set to /login when the construction had none; make it the login page of the surface bound to the staff auth",
				`test/integration/harness_test.go and cmd/deployment/migrate/main_test.go: the okta registration validates its issuer at registration whether or not the directory is simulated, so give the staff auth's construction in the harness Okta: staff.OktaSettings{Issuer: "https://provider.example.com", RedirectURL: <the served callback>} and set APP_STAFF_OKTA_ISSUER=https://provider.example.com where the migrate run's environment is`,
			},
			// Registered everywhere: no cast, no placement block, nothing said of where the
			// provider is configured, and no organization slot; and no tenant declares
			// directories here, so nothing is said of a declaration.
			wantNotSkipped: []string{"cast.json", "placement.json", "ADMINISTERED", "organization slot", "connection_not_in_tenant"},
			check: func(t *testing.T, a *app.App) {
				t.Helper()
				file := read(t, a, "pkg/auth/staff/okta.go")
				for _, want := range []string{"package staff", `const OktaRegistration = "okta"`, "type OktaSettings struct", "func (h *Handlers) OktaLogin() http.HandlerFunc {\n\treturn h.okta.Login()\n}", "func (h *Handlers) OktaCallback() http.HandlerFunc", "func registerOkta(authSession *Session, accessClient *access.Client, settings *Settings) *oidc.Method", "oidc.SignIn(authSession, OktaRegistration", "\t\tIssuer:       settings.Okta.Issuer,\n", "\t\tClientSecret: settings.Okta.ClientSecret,\n", "\t\tConnection:   OktaRegistration,\n", "\t\tRoleSync:     signin.DisableRoleSync(),\n\t\tLoginURL:     settings.LoginURL,\n", "the method is invite only until an administrator opens it to sign-up"} {
					if !strings.Contains(file, want) {
						t.Errorf("okta.go lacks %q:\n%s", want, file)
					}
				}
				for _, unwanted := range []string{"Attesting", "return nil"} {
					if strings.Contains(file, unwanted) {
						t.Errorf("okta.go carries %q, the persona method's:\n%s", unwanted, file)
					}
				}
				main := read(t, a, "pkg/auth/staff/staff.go")
				// The package doc's first sentence said the auth's people sign in with a
				// password; it says the provider too, and is wrapped again.
				if want := "// Package staff is the staff auth: the people who sign in to the console with a password,\n// or through the organization's own identity provider, who hold their roles in the staff\n// permission store.\n//\n"; !strings.HasPrefix(main, want) {
					t.Errorf("staff.go starts %q, want %q", main[:min(len(main), 300)], want)
				}
				for _, want := range []string{"\tpassword *session.PasswordMethod\n\tokta     *oidc.Method\n", "\thandlers.okta = registerOkta(authSession, accessClient, settings)\n", "\tOkta OktaSettings\n", "\tLoginURL string\n", "\t\"github.com/impulseframework/impulse/session/oidc\"\n"} {
					if !strings.Contains(main, want) {
						t.Errorf("staff.go lacks %q:\n%s", want, main)
					}
				}
				data := read(t, a, "pkg/config/data.go")
				for _, want := range []string{
					"\tStaffOktaIssuer       string `env:\"APP_STAFF_OKTA_ISSUER\"`\n",
					"\tStaffOktaClientSecret string `env:\"APP_STAFF_OKTA_CLIENT_SECRET\" secret:\"true\"`\n",
					"\t\tOkta: staff.OktaSettings{\n\t\t\tIssuer:       env.StaffOktaIssuer,\n\t\t\tClientID:     env.StaffOktaClientID,\n\t\t\tClientSecret: env.StaffOktaClientSecret,\n\t\t\tRedirectURL:  env.StaffOktaRedirectURL,\n\t\t},\n",
				} {
					if !strings.Contains(data, want) {
						t.Errorf("data.go lacks %q:\n%s", want, data)
					}
				}
				if program := read(t, a, "cmd/generate/resourcegenerator/main.go"); !strings.Contains(program, `generation.Auth("example.com/acme/beacon/pkg/auth/staff", generation.PasswordMethod(), generation.OIDCMethod("okta"))`) {
					t.Errorf("program = %s", program)
				}
			},
		},
		{
			name:   "an organization's identity provider added with the words its people read: the declaration carries them and the brief names the file",
			method: Method{Auth: "staff", Spec: authpkg.Method{Kind: app.MethodOIDC, Name: "company-sso", Authority: AuthorityApplication, Label: "Lakeside ID"}},
			extra:  map[string]string{"pkg/config/data.go": authConfigSettings},
			wantDid: []string{
				`cmd/generate/resourcegenerator/main.go: generation.OIDCMethod("company-sso").Labeled("Lakeside ID") declared on 1 outlet Auth option(s) bound to the staff auth, so the generated router mounts user/company-sso/login and the callback and the methods route offers it`,
				`cmd/generate/resourcegenerator/main.go: people read the company-sso method as "Lakeside ID", on the login page's button (Sign in with Lakeside ID) and in the lists of a person's ways to sign in; the words are the declaration's .Labeled("Lakeside ID"), so change them there and run go generate ./...`,
			},
			check: func(t *testing.T, a *app.App) {
				t.Helper()
				if program := read(t, a, "cmd/generate/resourcegenerator/main.go"); !strings.Contains(program, `generation.Auth("example.com/acme/beacon/pkg/auth/staff", generation.PasswordMethod(), generation.OIDCMethod("company-sso").Labeled("Lakeside ID"))`) {
					t.Errorf("program = %s", program)
				}
				// The words are the page's alone: the registration name stays the key in the
				// method file, its routes and its variables.
				file := read(t, a, "pkg/auth/staff/company-sso.go")
				for _, want := range []string{`const CompanySsoRegistration = "company-sso"`, "Connection:   CompanySsoRegistration,"} {
					if !strings.Contains(file, want) {
						t.Errorf("company-sso.go lacks %q:\n%s", want, file)
					}
				}
				if strings.Contains(file, "Lakeside") {
					t.Errorf("company-sso.go carries the words, which the declaration alone holds:\n%s", file)
				}
			},
		},
		{
			name:   "a persona method, registered where the provider is configured, with its cast",
			method: Method{Auth: "staff", Spec: authpkg.Method{Kind: app.MethodPersona, Name: "persona"}},
			wantDid: []string{
				"pkg/auth/staff/persona.go: the persona method (persona, the persona provider, role membership the application's): its registration constant, its settings, its handlers (PersonaLogin and the rest), and registerPersona, which registers it on the Auth",
				"pkg/auth/staff/cast.json and pkg/auth/staff/cast.go: the cast of the persona sign-in, empty, and the file that embeds it, reads it (Cast) and seeds its accounts (SeedCast)",
				"pkg/auth/staff/staff.go: Handlers holds the persona method (persona *oidc.Method), New registers it (registerPersona), and Settings carries its PersonaSettings and the LoginURL, and the package doc lists it, and its first sentence says the auth's people also sign in as a persona of the test environments",
				`cmd/generate/resourcegenerator/main.go: generation.PersonaMethod("persona") declared on 1 outlet Auth option(s) bound to the staff auth, so the generated router mounts user/persona/login and the callback and the methods route offers it, where the provider is configured`,
				"ran go generate ./..., so the router mounts the method's routes and the login page offers it",
			},
			wantSkipped: []string{
				"pkg/auth/staff/cast.json: name the personas (login, name, email, roles from roles.json); impulse check and bedrock check refuse an empty cast (persona-cast), since a persona sign-in with nobody to sign in as deploys nothing useful",
				"cmd/deployment/migrate: seed the cast under -seed, once the data level is open and before InviteAdministrators: data.Staff().SeedCast(ctx, out), so the environments that take the seed hold the personas' accounts for the sign-in to link, and one whose cast gives a persona the Super Administrator role invites no first administrator and needs no mailbox. A persona seeded earlier is given the roles its entry names that it does not hold, each said on out, and nothing is taken: a role that left the cast stays on the account",
				`placement.json: name the persona provider under persona ({"issuer": the provider's URL, "clientId": the application's client ID root, both from the provider's registration of the application}), so bedrock sets APP_STAFF_PERSONA_ISSUER in the first environment and its pull requests, derives APP_STAFF_PERSONA_CLIENT_ID and APP_STAFF_PERSONA_REDIRECT_URL, and registers each deployment with the provider after traffic; without it the method is registered nowhere`,
				"pkg/config/data.go: no environment struct (env := &T{}) to add the persona method's variables to; read APP_STAFF_PERSONA_ISSUER, APP_STAFF_PERSONA_CLIENT_ID, APP_STAFF_PERSONA_REDIRECT_URL and pass them in staff.Settings.Persona",
			},
			// No secret, no organization slot to validate in the harness, no directory shape.
			wantNotSkipped: []string{"CLIENT_SECRET", "harness_test.go", "GroupPrefix", "ADMINISTERED"},
			check: func(t *testing.T, a *app.App) {
				t.Helper()
				file := read(t, a, "pkg/auth/staff/persona.go")
				for _, want := range []string{"package staff", `const PersonaRegistration = "persona"`, "type PersonaSettings struct", "func (h *Handlers) PersonaLogin() http.HandlerFunc", "func (h *Handlers) PersonaCallback() http.HandlerFunc", "func registerPersona(authSession *Session, accessClient *access.Client, settings *Settings) *oidc.Method", "if settings.Persona.Issuer == \"\" {\n\t\treturn nil\n\t}", "oidc.SignIn(authSession, PersonaRegistration", "signin.DisableRoleSync()", "The method is left invite only", "LoginURL:    settings.LoginURL"} {
					if !strings.Contains(file, want) {
						t.Errorf("persona.go lacks %q:\n%s", want, file)
					}
				}
				cast := read(t, a, "pkg/auth/staff/cast.go")
				for _, want := range []string{"//go:embed cast.json", "func Cast() (*cast.Cast, error)", "func (a *Auth) SeedCast(ctx context.Context, out io.Writer) (int, error)", "httpio.HasNotFound(err)", "accesstypes.GlobalPolicyScope()", "accesstypes.EveryDomainPolicyScope()"} {
					if !strings.Contains(cast, want) {
						t.Errorf("cast.go lacks %q:\n%s", want, cast)
					}
				}
				if got := read(t, a, "pkg/auth/staff/cast.json"); got != "{\n  \"personas\": []\n}\n" {
					t.Errorf("cast.json = %q", got)
				}
				main := read(t, a, "pkg/auth/staff/staff.go")
				for _, want := range []string{"\tpassword *session.PasswordMethod\n\tpersona  *oidc.Method\n", "\thandlers.persona = registerPersona(authSession, accessClient, settings)\n", "\tPersona PersonaSettings\n", "\tLoginURL string\n", "\t\"github.com/impulseframework/impulse/session/oidc\"\n"} {
					if !strings.Contains(main, want) {
						t.Errorf("staff.go lacks %q:\n%s", want, main)
					}
				}
				if program := read(t, a, "cmd/generate/resourcegenerator/main.go"); !strings.Contains(program, `generation.Auth("example.com/acme/beacon/pkg/auth/staff", generation.PasswordMethod(), generation.PersonaMethod("persona"))`) {
					t.Errorf("program = %s", program)
				}
			},
		},
		{
			// A role is held by the id of the account, so a rename moves nothing: the
			// registration sets no hook for a verified change of address.
			name:   "a password method whose sign-in name is the address sets no rename hook",
			method: Method{Auth: "staff", Spec: authpkg.Method{Kind: app.MethodPassword, UsernameIsEmail: true}},
			extra: map[string]string{
				"pkg/auth/staff/staff.go": strings.Replace(strings.Replace(staffPackage(t, "staff.go"),
					"\tpassword *session.PasswordMethod\n", "", 1),
					"\thandlers.password = registerPassword(authSession)\n", "", 1),
			},
			without: []string{"pkg/auth/staff/password.go"},
			check: func(t *testing.T, a *app.App) {
				t.Helper()
				main := read(t, a, "pkg/auth/staff/staff.go")
				if want := "\thandlers.password = registerPassword(authSession)\n"; !strings.Contains(main, want) {
					t.Errorf("staff.go lacks %q:\n%s", want, main)
				}
				for _, gone := range []string{"SetUserRenamed", "RolesFollowRename"} {
					if strings.Contains(main, gone) {
						t.Errorf("staff.go carries %q, which the identity package no longer has a use for:\n%s", gone, main)
					}
				}
				if file := read(t, a, "pkg/auth/staff/password.go"); !strings.Contains(file, "session.UsernameIsEmail()") {
					t.Errorf("password.go does not register the method with the address as the sign-in name:\n%s", file)
				}
			},
		},
		{
			name:   "a second registration joins the function under the first",
			method: Method{Auth: "staff", Spec: authpkg.Method{Kind: app.MethodGoogle, Name: "workspace", Authority: AuthorityApplication}},
			extra: map[string]string{"pkg/config/data.go": strings.Replace(authConfigSettings,
				"\treturn &staff.Settings{CookieKey: cookieKey, SessionTimeout: env.SessionTimeout}\n",
				"\treturn &staff.Settings{\n\t\tCookieKey:      cookieKey,\n\t\tSessionTimeout: env.SessionTimeout,\n\t\tLoginURL:       \"/console/login\",\n\t\tDirectory: staff.DirectorySettings{\n\t\t\tTenantID: env.StaffDirectoryTenantID,\n\t\t},\n\t}\n", 1)},
			wantDid: []string{
				"pkg/config/data.go: dataConfig reads the staff auth's workspace registration from APP_STAFF_WORKSPACE_HOSTED_DOMAIN, APP_STAFF_WORKSPACE_ADMINISTERED_WORKSPACES, APP_STAFF_WORKSPACE_CLIENT_ID, APP_STAFF_WORKSPACE_CLIENT_SECRET, APP_STAFF_WORKSPACE_REDIRECT_URL, and dataConfig.staffSettings passes them",
			},
			check: func(t *testing.T, a *app.App) {
				t.Helper()
				data := read(t, a, "pkg/config/data.go")
				// The login page the literal had is kept, and the new registration follows the first.
				if want := "\t\tLoginURL:       \"/console/login\",\n\t\tDirectory: staff.DirectorySettings{\n\t\t\tTenantID: env.StaffDirectoryTenantID,\n\t\t},\n\t\tWorkspace: staff.WorkspaceSettings{\n\t\t\tHostedDomain:           env.StaffWorkspaceHostedDomain,\n\t\t\tAdministeredWorkspaces: env.StaffWorkspaceAdministeredWorkspaces,\n\t\t\tClientID:               env.StaffWorkspaceClientID,\n\t\t\tClientSecret:           env.StaffWorkspaceClientSecret,\n\t\t\tRedirectURL:            env.StaffWorkspaceRedirectURL,\n\t\t},\n\t}\n}\n"; !strings.Contains(data, want) {
					t.Errorf("data.go lacks %q:\n%s", want, data)
				}
			},
		},
		{
			name:   "a level with neither the function nor the literal reads the variables and says what to pass",
			method: Method{Auth: "staff", Spec: authpkg.Method{Kind: app.MethodAzure, Name: "directory", Authority: AuthorityApplication}},
			extra:  map[string]string{"pkg/config/data.go": strings.Replace(authConfigEnv, "&staff.Settings{CookieKey: cookieKey, SessionTimeout: env.SessionTimeout}", "settings(env, cookieKey)", 1)},
			wantDid: []string{
				"pkg/config/data.go: dataConfig reads the staff auth's directory registration from APP_STAFF_DIRECTORY_TENANT_ID, APP_STAFF_DIRECTORY_ADMINISTERED_TENANTS, APP_STAFF_DIRECTORY_CLIENT_ID, APP_STAFF_DIRECTORY_CLIENT_SECRET, APP_STAFF_DIRECTORY_REDIRECT_URL",
			},
			wantSkipped: []string{
				"pkg/config/data.go: dataConfig has no staffSettings method and NewDataConfiguration builds no staff.Settings{...} literal, so the directory method's settings are read into dataConfig but not passed; set staff.Settings.Directory from them",
			},
			wantNotSkipped: []string{"LoginURL is set to /login"},
		},
		{
			name:        "a directory method on a module that never required the OIDC library",
			method:      Method{Auth: "staff", Spec: authpkg.Method{Kind: app.MethodGoogle, Name: "workspace", Authority: AuthorityApplication}},
			tidiedGoMod: beaconGoMod + "\nrequire github.com/coreos/go-oidc/v3 v3.21.0 // indirect\n",
			wantDid: []string{
				"go.mod: requires github.com/coreos/go-oidc/v3 v3.21.0, the OIDC library the session library's directory methods sign in through (go mod tidy)",
				"ran go generate ./..., so the router mounts the method's routes and the login page offers it",
			},
		},
		{
			name:   "a directory method's callback is the bound console's development address",
			method: Method{Auth: "staff", Spec: authpkg.Method{Kind: app.MethodGoogle, Name: "google", Authority: AuthorityApplication, Slot: authpkg.SlotAny}},
			extra:  map[string]string{".envrc.template": "export PORT=8090\n"},
			wantDid: []string{
				".envrc.template: the google method's variables (APP_STAFF_GOOGLE_ADMINISTERED_WORKSPACES, APP_STAFF_GOOGLE_CLIENT_ID, APP_STAFF_GOOGLE_CLIENT_SECRET, APP_STAFF_GOOGLE_REDIRECT_URL), to fill in; APP_STAFF_GOOGLE_REDIRECT_URL is set to the console project's development callback, http://127.0.0.1:4300/api/user/google/callback",
			},
			check: func(t *testing.T, a *app.App) {
				t.Helper()
				env := read(t, a, ".envrc.template")
				for _, want := range []string{
					"# development the console project's dev server (its development port in angular.json), which\n",
					"export APP_STAFF_GOOGLE_REDIRECT_URL=http://127.0.0.1:4300/api/user/google/callback\n",
				} {
					if !strings.Contains(env, want) {
						t.Errorf(".envrc.template lacks %q:\n%s", want, env)
					}
				}
			},
		},
		{
			name:   "a directory method's callback is left to fill in where the console has no development port",
			method: Method{Auth: "staff", Spec: authpkg.Method{Kind: app.MethodGoogle, Name: "google", Authority: AuthorityApplication, Slot: authpkg.SlotAny}},
			extra:  map[string]string{".envrc.template": "export PORT=8090\n", "web/angular.json": strings.Replace(beaconAngular, "              \"port\": 4300,\n", "", 1)},
			wantDid: []string{
				".envrc.template: the google method's variables (APP_STAFF_GOOGLE_ADMINISTERED_WORKSPACES, APP_STAFF_GOOGLE_CLIENT_ID, APP_STAFF_GOOGLE_CLIENT_SECRET, APP_STAFF_GOOGLE_REDIRECT_URL), to fill in",
			},
			check: func(t *testing.T, a *app.App) {
				t.Helper()
				if env := read(t, a, ".envrc.template"); !strings.Contains(env, "such as\n# http://127.0.0.1:4300/api/user/google/callback through the dev proxy; read under the\n# simulated directory too, since it is where the simulated sign-in returns.\nexport APP_STAFF_GOOGLE_REDIRECT_URL=\n") {
					t.Errorf(".envrc.template does not leave the callback to fill in:\n%s", env)
				}
			},
		},
		{
			name:    "tidy fails",
			method:  Method{Auth: "staff", Spec: authpkg.Method{Kind: app.MethodGoogle, Name: "workspace", Authority: AuthorityApplication}},
			tidyErr: errors.New("exit status 1"),
			wantErr: "go mod tidy failed, so go.mod does not require what the change imports; fix the cause and run it, then go generate ./...:\ngo: tidy: boom",
		},
		{
			name:        "generation fails",
			method:      Method{Auth: "staff", Spec: authpkg.Method{Kind: app.MethodGoogle, Name: "workspace", Authority: AuthorityApplication}},
			generateErr: errors.New("exit status 1"),
			wantErr:     "go generate ./... failed; fix the cause and run it:\ngeneration: boom",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			files := authFiles(t)
			files["cmd/generate/resourcegenerator/main.go"] = beaconRouterProgram
			for rel, content := range tt.extra {
				files[rel] = content
			}
			for _, rel := range tt.without {
				delete(files, rel)
			}
			a := beacon(t, files)
			exec := &fakeExec{err: tt.generateErr}
			switch {
			case tt.tidyErr != nil:
				exec.tidy = func() error {
					return tt.tidyErr
				}
			case tt.tidiedGoMod != "":
				exec.tidy = tidied(a, tt.tidiedGoMod)
			}
			ch, err := tt.method.Apply(t.Context(), a, exec)
			wantCalls := regenerated
			if tt.tidyErr != nil {
				// A failed tidy refuses before the regeneration.
				wantCalls = regenerated[:1]
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
				if !containsLine(ch.Did, want) {
					t.Errorf("Did lacks %q; got:\n%s", want, strings.Join(ch.Did, "\n"))
				}
			}
			for _, want := range tt.wantSkipped {
				if !containsLine(ch.Skipped, want) {
					t.Errorf("Skipped lacks %q; got:\n%s", want, strings.Join(ch.Skipped, "\n"))
				}
			}
			for _, unwanted := range tt.wantNotSkipped {
				if skipped := strings.Join(ch.Skipped, "\n"); strings.Contains(skipped, unwanted) {
					t.Errorf("Skipped carries %q; got:\n%s", unwanted, skipped)
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

func containsLine(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}

	return false
}

// TestMethodMeaningIdentityProvider holds the brief of an organization's identity provider
// to what the method is: one provider named in the registration's settings, registered in
// every environment, and, where its token names the roles, the claim the provider has to
// send.
func TestMethodMeaningIdentityProvider(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		spec     authpkg.Method
		want     []string
		unwanted []string
	}{
		{
			name: "the application gives the roles",
			spec: authpkg.Method{Kind: app.MethodOIDC, Name: "okta", Authority: AuthorityApplication},
			want: []string{
				"the staff auth now offers `okta`, an organization's identity provider over OpenID Connect (`oidc.SignIn`",
				"It signs in through one organization's identity provider, a provider known by its issuer alone, such as Okta or Keycloak",
				"Role membership stays the application's",
				"APP_STAFF_OKTA_ISSUER, APP_STAFF_OKTA_CLIENT_ID, APP_STAFF_OKTA_CLIENT_SECRET, APP_STAFF_OKTA_REDIRECT_URL",
				"Register it with the identity provider",
				"Decide who may open a sign-in method to sign-up. Every method of the staff auth starts invite only",
				"A newcomer needs an invitation until an administrator opens the method to sign-up",
				// No words were given, so the brief says what people read and where the
				// provider's own name goes.
				"Give the method the name its people know. Nothing said what to call the provider, so the login page's button says \"Sign in with Okta\", words made from the registration name",
				"Chain `.Labeled(\"<words>\")` on the method's declaration in the generator program (`generation.OIDCMethod(\"okta\").Labeled(\"Lakeside ID\")`; the change's lines above name the file, where an outlet declares the auth) and run `go generate ./...`",
			},
			unwanted: []string{"organization slot", "cast", "placement.json", "Have the provider send the roles", "Read the method's name where people do"},
		},
		{
			name: "the provider was added with the words its people read",
			spec: authpkg.Method{Kind: app.MethodOIDC, Name: "company-sso", Authority: AuthorityApplication, Label: "Lakeside ID"},
			want: []string{
				"Read the method's name where people do. The login page's button says \"Sign in with Lakeside ID\"",
				"The words are the declaration's `.Labeled(\"Lakeside ID\")` in the generator program (the change's lines above name the file, where an outlet declares the auth): change them there and run `go generate ./...`",
				"The registration name (`company-sso`) stays the key in the routes, the variables and the stored links",
			},
			unwanted: []string{"Give the method the name its people know", "Company Sso"},
		},
		{
			name: "the provider's token names the roles",
			spec: authpkg.Method{Kind: app.MethodOIDC, Name: "company-sso", Authority: AuthorityDirectory},
			want: []string{
				"Role membership is the directory's",
				"Have the provider send the roles. The method reads a person's roles from the ID token's `roles` claim",
			},
			// The provider decides who its people are: nobody opens the method.
			unwanted: []string{"organization slot", "Decide who may open a sign-in method to sign-up"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			meaning := (&Method{Auth: "staff", Spec: tt.spec}).Meaning()
			for _, want := range tt.want {
				if !strings.Contains(meaning, want) {
					t.Errorf("Meaning() lacks %q:\n%s", want, meaning)
				}
			}
			for _, unwanted := range tt.unwanted {
				if strings.Contains(meaning, unwanted) {
					t.Errorf("Meaning() carries %q:\n%s", unwanted, meaning)
				}
			}
		})
	}
}

// Test_retellPopulation holds the package doc's first sentence to the methods the auth
// has: the base's says its people sign in with a password, and each method added joins
// it, in the words the tenanted skeleton's own sentence carries. A sentence that names
// no way, one that says the way already, and a file with no such paragraph are left as
// they are.
func Test_retellPopulation(t *testing.T) {
	t.Parallel()

	const (
		rest     = "//\n// An auth is a package.\npackage staff\n"
		base     = "// Package staff is the staff auth: the people who sign in to the console with a password, who hold their roles in the\n// staff permission store.\n" + rest
		generic  = "// Package members is the members auth: the people who sign in to the application, who hold their roles in the\n// members permission store.\n" + rest
		withBoth = "// Package staff is the staff auth: the people who sign in to the console with a password,\n// or with any Google account, who hold their roles in the staff permission store.\n" + rest
	)
	google := &authpkg.Method{Kind: app.MethodGoogle, Name: "google", Slot: authpkg.SlotAny, Authority: AuthorityApplication}
	provider := &authpkg.Method{Kind: app.MethodOIDC, Name: "sso", Authority: AuthorityApplication}
	tests := []struct {
		name   string
		src    string
		method *authpkg.Method
		want   string
		told   bool
	}{
		{name: "the base gains a Google method", src: base, method: google, want: withBoth, told: true},
		{
			name: "and then an organization's identity provider", src: withBoth, method: provider, told: true,
			want: "// Package staff is the staff auth: the people who sign in to the console with a password,\n// or with any Google account, or through the organization's own identity provider, who\n// hold their roles in the staff permission store.\n" + rest,
		},
		{name: "a way the sentence says already is not said twice", src: withBoth, method: google, want: withBoth},
		{name: "a sentence that names no way is left as it is", src: generic, method: google, want: generic},
		{name: "a file with no package paragraph is left as it is", src: "package staff\n", method: google, want: "package staff\n"},
		{
			// The paragraph's end is looked for in the comment that opens the file: an
			// empty comment line in a comment further down is not it.
			name:   "a package comment of one paragraph is left as it is, whatever comments follow",
			src:    "// Package staff is the staff auth: the people who sign in to the console with a password, who hold their roles in the\n// staff permission store.\npackage staff\n\n// New opens the auth.\n//\n// It refuses a nil client.\nfunc New() {}\n",
			method: google,
			want:   "// Package staff is the staff auth: the people who sign in to the console with a password, who hold their roles in the\n// staff permission store.\npackage staff\n\n// New opens the auth.\n//\n// It refuses a nil client.\nfunc New() {}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, told := retellPopulation([]byte(tt.src), tt.method)
			if told != tt.told {
				t.Errorf("retellPopulation() told = %v, want %v", told, tt.told)
			}
			if diff := cmp.Diff(tt.want, string(got)); diff != "" {
				t.Errorf("retellPopulation() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Test_retellPopulation_theSkeletons holds the sentence add method writes to the one the
// tenanted skeleton carries: the base's auth file with any Google account added says,
// word for word, what the tenanted skeleton's says.
func Test_retellPopulation_theSkeletons(t *testing.T) {
	t.Parallel()

	paragraph := func(candidate string) string {
		t.Helper()
		tree, err := skeleton.FS(candidate)
		if err != nil {
			t.Fatalf("skeleton.FS(%s) error = %v", candidate, err)
		}
		src, err := fs.ReadFile(tree, "pkg/auth/staff/staff.go")
		if err != nil {
			t.Fatalf("%s: reading the auth file: %v", candidate, err)
		}
		if candidate == skeleton.Base {
			retold, told := retellPopulation(src, &authpkg.Method{Kind: app.MethodGoogle, Name: "google", Slot: authpkg.SlotAny, Authority: AuthorityApplication})
			if !told {
				t.Fatalf("the base's auth file was not retold:\n%s", src[:min(len(src), 300)])
			}
			src = retold
		}
		first, _, _ := strings.Cut(string(src), "\n//\n")

		return strings.Join(strings.Fields(strings.ReplaceAll(first, "//", "")), " ")
	}
	if got, want := paragraph(skeleton.Base), paragraph("tenanted"); got != want {
		t.Errorf("the base's first sentence with a Google method = %q, want the tenanted skeleton's %q", got, want)
	}
}
