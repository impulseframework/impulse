package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// registrationFile renders an auth package's file constructing one Auth and registering
// a method on it: the statements given are the registering function's body after the
// construction, the last of them the registration call.
func registrationFile(statements string, decls ...string) string {
	return `package members

import (
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/azure"
	"github.com/impulseframework/impulse/session/google"
	"github.com/impulseframework/impulse/session/oidc"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/session/signin"
)

func register(client any, key string, settings *Settings) {
	authSession, _ := session.NewAuth[session.NoCustomData, session.NoCustomData](sessionstorage.NewSpannerAuth(client), key, session.NoSuperAdministrators())
` + statements + `
}
` + strings.Join(decls, "\n")
}

// TestReadTenants reads a Microsoft registration's tenant shape from the slot function
// its configuration's Tenants slot calls, as impulse add method writes one for each slot
// kind: one tenant, listed tenants parsed out of their setting, any tenant, personal
// accounts, and personal accounts beside the tenants, with the administered tenants
// declared attesting inside the function. A slot written any other way, a building block
// in the configuration itself included, leaves the shape empty, and a Google registration
// has none.
func TestReadTenants(t *testing.T) {
	t.Parallel()

	const (
		// The registering statements as impulse writes them: a fallible slot function,
		// and one that cannot fail.
		fallible = `	tenants, err := directorySlot(settings)
	if err != nil {
		return
	}
	_, _ = azure.SignIn(authSession, "directory", nil, &azure.Config{Tenants: tenants, RoleSync: signin.DisableRoleSync()})`
		infallible = `	tenants := directorySlot(settings)
	_, _ = azure.SignIn(authSession, "directory", nil, &azure.Config{Tenants: tenants, RoleSync: signin.DisableRoleSync()})`
		// The administered tenants' parser every slot function but the personal one calls.
		parseAdministered = `func parseDirectoryAdministered(value string) []string {
	var ids []string
	for _, id := range strings.Split(value, ",") {
		if id = strings.ToLower(strings.TrimSpace(id)); id != "" {
			ids = append(ids, id)
		}
	}

	return ids
}`
		// The one-tenant slot function, returning the tenant alone or beside personal accounts.
		oneTenant = `func directorySlot(settings *Settings) (azure.TenantSet, error) {
	tenant := azure.Tenant(settings.Directory.TenantID)
	for _, id := range parseDirectoryAdministered(settings.Directory.AdministeredTenants) {
		if !strings.EqualFold(id, settings.Directory.TenantID) {
			return azure.TenantSet{}, errors.Newf("DirectoryRegistration: the administered tenant %q is not the tenant admitted, %s", id, settings.Directory.TenantID)
		}
		tenant = tenant.Attesting()
	}

	return RETURN, nil
}`
		// The open slot function, admitting any tenant with the administered ones attesting beside it.
		anyTenant = `func directorySlot(settings *Settings) azure.TenantSet {
	administered := parseDirectoryAdministered(settings.Directory.AdministeredTenants)
	sets := make([]azure.TenantSet, 0, len(administered)+1)
	sets = append(sets, OPEN)
	for _, id := range administered {
		sets = append(sets, azure.Tenant(id).Attesting())
	}

	return azure.Tenants(sets...)
}`
		// The listed slot function and the parser of its setting.
		listedTenants = `func directorySlot(settings *Settings) (azure.TenantSet, error) {
	return parseDirectoryTenants(settings.Directory.Tenants, parseDirectoryAdministered(settings.Directory.AdministeredTenants))
}

func parseDirectoryTenants(value string, administered []string) (azure.TenantSet, error) {
	var sets []azure.TenantSet
	for _, entry := range strings.Split(value, ",") {
		name, id, ok := strings.Cut(entry, "=")
		if !ok {
			return azure.TenantSet{}, errors.Newf("DirectoryRegistration: tenant %q is not <name>=<tenant id>", entry)
		}
		tenant := azure.NamedTenant(id, name)
		if slices.Contains(administered, strings.ToLower(id)) {
			tenant = tenant.Attesting()
		}
		sets = append(sets, tenant)
	}

	return azure.Tenants(sets...), nil
}`
	)
	tests := []struct {
		name          string
		statements    string
		decls         []string
		wantTenants   string
		wantPersonal  bool
		wantAuthority string
	}{
		{
			name:          "one tenant from its setting",
			statements:    fallible,
			decls:         []string{strings.Replace(oneTenant, "RETURN", "tenant", 1), parseAdministered},
			wantTenants:   TenantsOne,
			wantAuthority: AuthorityApplication,
		},
		{
			name:          "one tenant and personal accounts",
			statements:    fallible,
			decls:         []string{strings.Replace(oneTenant, "RETURN", "azure.Tenants(tenant, azure.PersonalAccounts())", 1), parseAdministered},
			wantTenants:   TenantsOne,
			wantPersonal:  true,
			wantAuthority: AuthorityApplication,
		},
		{
			name:          "listed tenants parsed out of their setting",
			statements:    fallible,
			decls:         []string{listedTenants, parseAdministered},
			wantTenants:   TenantsListed,
			wantAuthority: AuthorityApplication,
		},
		{
			name:          "any tenant",
			statements:    infallible,
			decls:         []string{strings.Replace(anyTenant, "OPEN", "azure.AnyTenant()", 1), parseAdministered},
			wantTenants:   TenantsAny,
			wantAuthority: AuthorityApplication,
		},
		{
			name:          "any tenant and personal accounts",
			statements:    infallible,
			decls:         []string{strings.Replace(anyTenant, "OPEN", "azure.AnyTenant(), azure.PersonalAccounts()", 1), parseAdministered},
			wantTenants:   TenantsAny,
			wantPersonal:  true,
			wantAuthority: AuthorityApplication,
		},
		{
			name: "personal accounts alone",
			statements: `	tenants := personalSlot()
	_, _ = azure.SignIn(authSession, "personal", nil, &azure.Config{Tenants: tenants, RoleSync: signin.DisableRoleSync()})`,
			decls: []string{`func personalSlot() azure.TenantSet {
	return azure.PersonalAccounts()
}`},
			wantTenants:   TenantsPersonal,
			wantPersonal:  true,
			wantAuthority: AuthorityApplication,
		},
		{
			name: "the configuration assigned to an identifier first",
			statements: `	tenants := directorySlot(settings)
	cfg := &azure.Config{Tenants: tenants, RoleSync: signin.RoleSync(nil)}
	_, _ = azure.SignIn(authSession, "directory", nil, cfg)`,
			decls:         []string{strings.Replace(anyTenant, "OPEN", "azure.AnyTenant()", 1), parseAdministered},
			wantTenants:   TenantsAny,
			wantAuthority: AuthorityDirectory,
		},
		{
			name:       "a slot function listing the tenants beside any tenant admits any",
			statements: infallible,
			decls: []string{`func directorySlot(settings *Settings) azure.TenantSet {
	return azure.Tenants(azure.NamedTenant("0b3b2a5c-1111-4ec5-9c15-0e2a2a1d4b55", "contoso"), azure.AnyTenant())
}`},
			wantTenants:   TenantsAny,
			wantAuthority: AuthorityApplication,
		},
		{
			name:          "a block in the configuration itself is not read",
			statements:    `	_, _ = azure.SignIn(authSession, "directory", nil, &azure.Config{Tenants: azure.Tenant(settings.TenantID), RoleSync: signin.DisableRoleSync()})`,
			wantAuthority: AuthorityApplication,
		},
		{
			name: "a slot assigned from the library's blocks is not read",
			statements: `	set := azure.Tenants(azure.NamedTenant("0b3b2a5c-1111-4ec5-9c15-0e2a2a1d4b55", "contoso"), azure.NamedTenant("5d7e6f80-2222-4b2b-8c3d-1f2e3d4c5b6a", "fabrikam"))
	_, _ = azure.SignIn(authSession, "directory", nil, &azure.Config{Tenants: set})`,
		},
		{
			name:       "a slot function building no block is not read",
			statements: infallible,
			decls: []string{`func directorySlot(settings *Settings) azure.TenantSet {
	return settings.Directory.Tenants
}`},
			wantAuthority: AuthorityApplication,
		},
		{
			name:       "a slot function of nothing is not read",
			statements: infallible,
			decls: []string{`func directorySlot(settings *Settings) azure.TenantSet {
	return azure.Tenants()
}`},
			wantAuthority: AuthorityApplication,
		},
		{
			name:          "a slot function the file does not declare is not read",
			statements:    infallible,
			wantAuthority: AuthorityApplication,
		},
		{
			name:       "a configuration built elsewhere is not read",
			statements: `	_, _ = azure.SignIn(authSession, "directory", nil, directoryConfig(settings))`,
		},
		{
			name:          "a Google registration has no tenant shape",
			statements:    `	_, _ = google.SignIn(authSession, "workspace", nil, &google.Config{Workspaces: google.AnyWorkspace(), RoleSync: google.RoleSync(nil, "", nil)})`,
			wantAuthority: AuthorityDirectory,
		},
		{
			name:          "a persona registration has no tenant shape and leaves the roles to the application",
			statements:    `	_ = oidc.SignIn(authSession, "persona", nil, &oidc.Config{Issuer: settings.Persona.Issuer, ClientID: settings.Persona.ClientID, RedirectURL: settings.Persona.RedirectURL, Connection: "persona", RoleSync: signin.DisableRoleSync(), Accounts: signin.InvitedUsersOnly(), LoginURL: settings.LoginURL})`,
			wantAuthority: AuthorityApplication,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			auths, unattached, err := parseAuths("pkg/auth/members/members.go", []byte(registrationFile(tt.statements, tt.decls...)), "example.com/harbor/pkg/auth/members")
			if err != nil {
				t.Fatalf("parseAuths() error = %v", err)
			}
			if len(auths) != 1 || len(auths[0].Methods) != 1 || len(unattached) != 0 {
				t.Fatalf("parseAuths() = %d auths, %d unattached; want one auth with one method", len(auths), len(unattached))
			}
			m := auths[0].Methods[0]
			if m.Tenants != tt.wantTenants || m.PersonalAccounts != tt.wantPersonal {
				t.Errorf("Tenants = %q, PersonalAccounts = %v; want %q, %v", m.Tenants, m.PersonalAccounts, tt.wantTenants, tt.wantPersonal)
			}
			if m.Authority != tt.wantAuthority {
				t.Errorf("Authority = %q, want %q", m.Authority, tt.wantAuthority)
			}
		})
	}
}

// TestReadWorkspaces reads a Google registration's Workspace shape from the building
// blocks its Workspaces slot is made of: in the slot function, as impulse add method
// writes one for each slot kind (one Workspace, listed Workspaces parsed out of their
// setting, any Workspace, any Google account, and one Workspace beside any Google
// account), and in the configuration itself, as an auth written by hand has it. A slot
// built anywhere else leaves the shape empty, and a Microsoft registration has none.
func TestReadWorkspaces(t *testing.T) {
	t.Parallel()

	const (
		// The registering statements as impulse writes them: a fallible slot function,
		// and one that cannot fail.
		fallible = `	workspaces, err := workspaceSlot(settings)
	if err != nil {
		return
	}
	_, _ = google.SignIn(authSession, "workspace", nil, &google.Config{Workspaces: workspaces, RoleSync: google.DisableRoleSync()})`
		infallible = `	workspaces := workspaceSlot(settings)
	_, _ = google.SignIn(authSession, "workspace", nil, &google.Config{Workspaces: workspaces, RoleSync: google.DisableRoleSync()})`
		// The one-Workspace slot function, returning the Workspace alone or beside any
		// Google account.
		oneWorkspace = `func workspaceSlot(settings *Settings) (google.WorkspaceSet, error) {
	workspace := google.Workspace(WorkspaceRegistration, settings.Workspace.HostedDomain)
	for _, domain := range parseWorkspaceAdministered(settings.Workspace.AdministeredWorkspaces) {
		if !strings.EqualFold(domain, settings.Workspace.HostedDomain) {
			return google.WorkspaceSet{}, errors.Newf("the administered Workspace %q is not the Workspace admitted", domain)
		}
		workspace = workspace.Attesting()
	}

	return RETURN, nil
}`
		// The open slot function, with the administered Workspaces attesting beside it.
		anyWorkspace = `func workspaceSlot(settings *Settings) google.WorkspaceSet {
	administered := parseWorkspaceAdministered(settings.Workspace.AdministeredWorkspaces)
	sets := make([]google.WorkspaceSet, 0, len(administered)+2)
	sets = append(sets, OPEN)
	for _, domain := range administered {
		sets = append(sets, google.Workspace(domain, domain).Attesting())
	}

	return google.Workspaces(sets...)
}`
		// The listed slot function and the parser of its setting.
		listedWorkspaces = `func workspaceSlot(settings *Settings) (google.WorkspaceSet, error) {
	return parseWorkspaceWorkspaces(settings.Workspace.Workspaces)
}

func parseWorkspaceWorkspaces(value string) (google.WorkspaceSet, error) {
	var sets []google.WorkspaceSet
	for _, entry := range strings.Split(value, ",") {
		name, domains, ok := strings.Cut(entry, "=")
		if !ok {
			return google.WorkspaceSet{}, errors.Newf("Workspace %q is not <name>=<domain>", entry)
		}
		sets = append(sets, google.Workspace(name, strings.Split(domains, "+")...))
	}

	return google.Workspaces(sets...), nil
}`
	)
	tests := []struct {
		name           string
		statements     string
		decls          []string
		wantWorkspaces string
		wantAccounts   bool
	}{
		{
			name:           "one Workspace",
			statements:     fallible,
			decls:          []string{strings.Replace(oneWorkspace, "RETURN", "workspace", 1)},
			wantWorkspaces: WorkspacesOne,
		},
		{
			name:           "one Workspace and any Google account",
			statements:     fallible,
			decls:          []string{strings.Replace(oneWorkspace, "RETURN", "google.Workspaces(workspace, google.AnyGoogleAccount())", 1)},
			wantWorkspaces: WorkspacesOne,
			wantAccounts:   true,
		},
		{
			name:           "listed Workspaces parsed out of their setting, each given the domains its entry names",
			statements:     fallible,
			decls:          []string{listedWorkspaces},
			wantWorkspaces: WorkspacesListed,
		},
		{
			name:       "one Workspace of two domains is one Workspace",
			statements: infallible,
			decls: []string{`func workspaceSlot(settings *Settings) google.WorkspaceSet {
	return google.Workspace("fleet", "fleet.example", "fleet.test")
}`},
			wantWorkspaces: WorkspacesOne,
		},
		{
			name:       "one Workspace given a spread of domains is one Workspace",
			statements: infallible,
			decls: []string{`func workspaceSlot(settings *Settings) google.WorkspaceSet {
	return google.Workspace("fleet", settings.Workspace.Domains...)
}`},
			wantWorkspaces: WorkspacesOne,
		},
		{
			name:           "a Workspace of several domains beside any Google account",
			statements:     `	_, _ = google.SignIn(authSession, "workspace", nil, &google.Config{Workspaces: google.Workspaces(google.Workspace("fleet", "fleet.example", "fleet.test"), google.AnyGoogleAccount()), RoleSync: google.DisableRoleSync()})`,
			wantWorkspaces: WorkspacesOne,
			wantAccounts:   true,
		},
		{
			name:           "any Workspace",
			statements:     infallible,
			decls:          []string{strings.Replace(anyWorkspace, "OPEN", "google.AnyWorkspace()", 1)},
			wantWorkspaces: WorkspacesAny,
		},
		{
			name:           "any Google account, with any Workspace named beside it",
			statements:     infallible,
			decls:          []string{strings.Replace(anyWorkspace, "OPEN", "google.AnyWorkspace(), google.AnyGoogleAccount()", 1)},
			wantWorkspaces: WorkspacesAny,
			wantAccounts:   true,
		},
		{
			name:           "any Google account alone, built in the configuration itself",
			statements:     `	_, _ = google.SignIn(authSession, "sign-up", nil, &google.Config{Workspaces: google.AnyGoogleAccount(), RoleSync: google.DisableRoleSync()})`,
			wantWorkspaces: WorkspacesAccounts,
			wantAccounts:   true,
		},
		{
			name: "a slot the registering function assigned from the library's blocks",
			statements: `	set := google.Workspaces(google.Workspace("fleet", "fleet.example"), google.Workspace("yard", "yard.example"))
	_, _ = google.SignIn(authSession, "workspace", nil, &google.Config{Workspaces: set, RoleSync: google.DisableRoleSync()})`,
			wantWorkspaces: WorkspacesListed,
		},
		{
			name:       "a slot the settings build is not read",
			statements: `	_, _ = google.SignIn(authSession, "workspace", nil, &google.Config{Workspaces: settings.Directory.Workspace(), RoleSync: google.DisableRoleSync()})`,
		},
		{
			name:       "a configuration with no slot is not read",
			statements: `	_, _ = google.SignIn(authSession, "workspace", nil, &google.Config{RoleSync: google.DisableRoleSync()})`,
		},
		{
			name:       "a configuration built elsewhere is not read",
			statements: `	_, _ = google.SignIn(authSession, "workspace", nil, workspaceConfig(settings))`,
		},
		{
			name: "a Microsoft registration has no Workspace shape",
			statements: `	tenants := directorySlot()
	_, _ = azure.SignIn(authSession, "directory", nil, &azure.Config{Tenants: tenants, RoleSync: signin.DisableRoleSync()})`,
			decls: []string{`func directorySlot() azure.TenantSet {
	return azure.PersonalAccounts()
}`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			auths, unattached, err := parseAuths("pkg/auth/members/members.go", []byte(registrationFile(tt.statements, tt.decls...)), "example.com/harbor/pkg/auth/members")
			if err != nil {
				t.Fatalf("parseAuths() error = %v", err)
			}
			if len(auths) != 1 || len(auths[0].Methods) != 1 || len(unattached) != 0 {
				t.Fatalf("parseAuths() = %d auths, %d unattached; want one auth with one method", len(auths), len(unattached))
			}
			m := auths[0].Methods[0]
			if m.Workspaces != tt.wantWorkspaces || m.GoogleAccounts != tt.wantAccounts {
				t.Errorf("Workspaces = %q, GoogleAccounts = %v; want %q, %v", m.Workspaces, m.GoogleAccounts, tt.wantWorkspaces, tt.wantAccounts)
			}
		})
	}
}

// TestPersonaRegistration reads the persona method from the generic OpenID Connect
// package's constructor (oidc.SignIn): the method's kind is persona under the
// registration name the call gives it.
func TestPersonaRegistration(t *testing.T) {
	t.Parallel()

	auths, unattached, err := parseAuths("pkg/auth/members/members.go", []byte(registrationFile(`	_ = oidc.SignIn(authSession, "reviewers", nil, &oidc.Config{Issuer: settings.Reviewers.Issuer, RoleSync: signin.DisableRoleSync()})`)), "example.com/harbor/pkg/auth/members")
	if err != nil {
		t.Fatalf("parseAuths() error = %v", err)
	}
	if len(auths) != 1 || len(auths[0].Methods) != 1 || len(unattached) != 0 {
		t.Fatalf("parseAuths() = %d auths, %d unattached; want one auth with one method", len(auths), len(unattached))
	}
	m := auths[0].Methods[0]
	if m.Kind != MethodPersona || m.Name != "reviewers" || m.Authority != AuthorityApplication {
		t.Errorf("method = %s %q under %q; want persona reviewers under the application authority", m.Kind, m.Name, m.Authority)
	}
}

// TestOIDCRegistration reads an organization's identity provider from the same
// constructor (oidc.SignIn): a configuration that sets the client's secret is the generic
// method's, whatever the secret's value, under the registration name the call gives it
// and the authority its RoleSync names.
func TestOIDCRegistration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		statement     string
		wantKind      string
		wantAuthority string
	}{
		{
			name:          "a configuration with a client secret, the roles the application's",
			statement:     `	_ = oidc.SignIn(authSession, "okta", nil, &oidc.Config{Issuer: settings.Okta.Issuer, ClientID: settings.Okta.ClientID, ClientSecret: settings.Okta.ClientSecret, RedirectURL: settings.Okta.RedirectURL, Connection: "okta", RoleSync: signin.DisableRoleSync(), Accounts: signin.InvitedUsersOnly, LoginURL: settings.LoginURL})`,
			wantKind:      MethodOIDC,
			wantAuthority: AuthorityApplication,
		},
		{
			name:          "a configuration with a client secret, the roles the provider's",
			statement:     `	_ = oidc.SignIn(authSession, "okta", nil, &oidc.Config{Issuer: settings.Okta.Issuer, ClientSecret: settings.Okta.ClientSecret, RoleSync: signin.RoleSync(nil)})`,
			wantKind:      MethodOIDC,
			wantAuthority: AuthorityDirectory,
		},
		{
			name:          "a configuration without one is the persona method's",
			statement:     `	_ = oidc.SignIn(authSession, "okta", nil, &oidc.Config{Issuer: settings.Okta.Issuer, ClientID: settings.Okta.ClientID, RoleSync: signin.DisableRoleSync()})`,
			wantKind:      MethodPersona,
			wantAuthority: AuthorityApplication,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			auths, unattached, err := parseAuths("pkg/auth/members/members.go", []byte(registrationFile(tt.statement)), "example.com/harbor/pkg/auth/members")
			if err != nil {
				t.Fatalf("parseAuths() error = %v", err)
			}
			if len(auths) != 1 || len(auths[0].Methods) != 1 || len(unattached) != 0 {
				t.Fatalf("parseAuths() = %d auths, %d unattached; want one auth with one method", len(auths), len(unattached))
			}
			m := auths[0].Methods[0]
			if m.Kind != tt.wantKind || m.Name != "okta" || m.Authority != tt.wantAuthority {
				t.Errorf("method = %s %q under %q; want %s okta under %q", m.Kind, m.Name, m.Authority, tt.wantKind, tt.wantAuthority)
			}
		})
	}
}

// TestReadSuperAdministrators reads what an Auth construction is told about its super
// administrators: the form of the argument session.NewAuth takes after the cookie key,
// and, for a check, the permission engine the constructor opens behind it with the
// package whose role file that engine is handed. The check is followed through the
// identifiers the function assigns, and an engine opened anywhere else does not read.
func TestReadSuperAdministrators(t *testing.T) {
	t.Parallel()

	const (
		ownPkg  = "example.com/harbor/pkg/auth/members"
		session = "github.com/impulseframework/impulse/session"
		signin  = "github.com/impulseframework/impulse/session/signin"
		res     = "github.com/impulseframework/impulse/resource"
		// The engine as an auth package opens it, on line 15 of the file, and the
		// construction of the Auth over an argument.
		ownEngine = `	accessClient, err := access.New(store, settings.Collection, access.WithDefaultRoles(Roles(), Name), access.WithChangeSignal(settings.Signals))
	if err != nil {
		return
	}
`
		construct = `	_, _ = session.NewAuth[session.NoCustomData, session.NoCustomData](sessionstorage.NewSpannerAuth(client), key, %s, session.WithCookieName("members"))
`
		scaffolded = `session.SuperAdministratorsBy(signin.SuperAdministrators(resource.SignInRoles(accessClient.UserManager())))`
	)
	tests := []struct {
		name string
		body string
		want SuperAdministrators
	}{
		{
			name: "nobody is a super administrator",
			body: fmt.Sprintf(construct, `session.NoSuperAdministrators()`),
			want: SuperAdministrators{Form: SuperAdministratorsNone},
		},
		{
			name: "a check over the engine the constructor opens with its own role file, as add auth writes it",
			body: ownEngine + fmt.Sprintf(construct, scaffolded),
			want: SuperAdministrators{Form: SuperAdministratorsChecked, EngineLine: 15, RolesPackage: ownPkg, Packages: []string{res, session, signin}},
		},
		{
			name: "a check assigned before the call, over a user manager assigned before it",
			body: ownEngine + `	userManager := accessClient.UserManager()
	superAdministrators := session.SuperAdministratorsBy(signin.SuperAdministrators(userManager))
` + fmt.Sprintf(construct, `superAdministrators`),
			want: SuperAdministrators{Form: SuperAdministratorsChecked, EngineLine: 15, RolesPackage: ownPkg, Packages: []string{session, signin}},
		},
		{
			name: "an engine handed another auth package's role file",
			body: strings.Replace(ownEngine, "Roles()", "staff.Roles()", 1) + fmt.Sprintf(construct, scaffolded),
			want: SuperAdministrators{Form: SuperAdministratorsChecked, EngineLine: 15, RolesPackage: "example.com/harbor/pkg/auth/staff", Packages: []string{res, session, signin}},
		},
		{
			name: "an engine handed its own role file with the auth's name in a setting",
			body: strings.Replace(ownEngine, "Roles(), Name)", "Roles(), settings.Auth)", 1) + fmt.Sprintf(construct, scaffolded),
			want: SuperAdministrators{Form: SuperAdministratorsChecked, EngineLine: 15, RolesPackage: ownPkg, Packages: []string{res, session, signin}},
		},
		{
			name: "an engine handed no role file here",
			body: strings.Replace(ownEngine, "access.WithDefaultRoles(Roles(), Name), ", "", 1) + fmt.Sprintf(construct, scaffolded),
			want: SuperAdministrators{Form: SuperAdministratorsChecked, EngineLine: 15, Packages: []string{res, session, signin}},
		},
		{
			name: "a check taken from another auth package",
			body: fmt.Sprintf(construct, `session.SuperAdministratorsBy(staff.SuperAdministrators())`),
			want: SuperAdministrators{Form: SuperAdministratorsChecked, Packages: []string{"example.com/harbor/pkg/auth/staff", session}},
		},
		{
			name: "an engine the constructor is handed does not read",
			body: fmt.Sprintf(construct, `session.SuperAdministratorsBy(signin.SuperAdministrators(resource.SignInRoles(settings.Engine.UserManager())))`),
			want: SuperAdministrators{Form: SuperAdministratorsChecked, Packages: []string{res, session, signin}},
		},
		{
			name: "a check written in place is not entered, whatever its body names",
			body: ownEngine + fmt.Sprintf(construct, `session.SuperAdministratorsBy(func(context.Context, string) (bool, error) { _ = accessClient; return false, nil })`),
			want: SuperAdministrators{Form: SuperAdministratorsChecked, Packages: []string{session}},
		},
		{
			name: "an argument the constructor is handed has no form",
			body: fmt.Sprintf(construct, `settings.SuperAdministrators`),
		},
		{
			name: "the two-argument form, which no longer compiles, has no form",
			body: `	_, _ = session.NewAuth[session.NoCustomData, session.NoCustomData](sessionstorage.NewSpannerAuth(client), key)
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			src := `package members

import (
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/session/signin"

	"example.com/harbor/pkg/auth/staff"
)

func open(client any, key string, store access.Store, settings *Settings) {
	_ = staff.Name
` + tt.body + `}
`
			auths, _, err := parseAuths("pkg/auth/members/members.go", []byte(src), ownPkg)
			if err != nil {
				t.Fatalf("parseAuths() error = %v", err)
			}
			if len(auths) != 1 {
				t.Fatalf("parseAuths() = %d auths, want one", len(auths))
			}
			if diff := cmp.Diff(tt.want, auths[0].SuperAdministrators); diff != "" {
				t.Errorf("SuperAdministrators mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
