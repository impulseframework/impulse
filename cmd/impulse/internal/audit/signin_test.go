package audit

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/authpkg"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton"
)

// rendered renders a skeleton candidate into a temporary directory, applies the edits to
// its files (a nil edit removes the file, and an edit of a file the candidate does not
// have writes it) and reads it.
func rendered(t *testing.T, candidate string, edits map[string]func(text string) string) *app.App {
	t.Helper()

	dir := filepath.Join(t.TempDir(), candidate)
	if _, err := skeleton.Render(&skeleton.Options{Version: "v0.1.0", WebVersion: "0.1.0", Candidate: candidate, Dir: dir, ModulePath: "example.com/acme/" + candidate}); err != nil {
		t.Fatalf("skeleton.Render() error = %v", err)
	}
	for rel, edit := range edits {
		file := filepath.Join(dir, filepath.FromSlash(rel))
		if edit == nil {
			if err := os.Remove(file); err != nil {
				t.Fatal(err)
			}

			continue
		}
		before, err := os.ReadFile(file)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			t.Fatal(err)
		}
		after := edit(string(before))
		if after == string(before) {
			t.Fatalf("%s: the edit changed nothing; the candidate no longer has what this test rewrites", rel)
		}
		if err := os.WriteFile(file, []byte(after), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	a, err := app.Discover(dir)
	if err != nil {
		t.Fatalf("app.Discover() error = %v", err)
	}

	return a
}

// byDirectory is the edits that make the tenanted skeleton an application of
// Tenant-owned users, by directory, as impulse new starts one: a person belongs to one
// tenant (oneTenant, the edit of the auth file), the staff auth has no password method,
// its Google method admits the accounts of any Google Workspace and no other Google
// account, a Microsoft method open to any Microsoft Entra tenant stands beside it, and
// the persona method beside both, the sign-in of the test environments a development
// run signs its administrator in through; the two are rendered as the command renders a
// method's file.
func byDirectory(t *testing.T, oneTenant func(text string) string) map[string]func(text string) string {
	t.Helper()

	spec := &authpkg.Spec{
		Module: "example.com/acme/tenanted", AuthDir: "pkg/auth", Name: "staff",
		Methods: []authpkg.Method{
			{Kind: app.MethodAzure, Name: "azure", Slot: authpkg.SlotAny, Authority: app.AuthorityApplication},
			{Kind: app.MethodPersona, Name: authpkg.PersonaRegistration, Authority: app.AuthorityApplication},
		},
	}
	files := map[string]func(text string) string{
		"pkg/auth/staff/staff.go":    oneTenant,
		"pkg/auth/staff/password.go": nil,
		"pkg/auth/staff/google.go": func(text string) string {
			return strings.Replace(text, "google.AnyWorkspace(), google.AnyGoogleAccount()", "google.AnyWorkspace()", 1)
		},
	}
	for i := range spec.Methods {
		m := &spec.Methods[i]
		file, err := authpkg.MethodFile(spec, m)
		if err != nil {
			t.Fatalf("authpkg.MethodFile() error = %v", err)
		}
		files["pkg/auth/staff/"+m.Name+".go"] = func(string) string {
			return string(file)
		}
	}

	return files
}

// lineRE matches the line of a file position, which moves with every edit of the file
// it is in: the read-outs over the skeletons and Lodestar are compared without it.
var lineRE = regexp.MustCompile(`\.go:\d+`)

// signIn is the application's sign-in section, with the line numbers of its positions
// replaced by N.
func signIn(a *app.App) string {
	var out strings.Builder
	WriteSignIn(&out, a)

	return lineRE.ReplaceAllString(out.String(), ".go:N")
}

// The lines the read-outs below share.
const (
	passwordStaff = "    password (pkg/auth/staff/password.go:N): admits anyone with an email address; the application gives the roles; invite only until an administrator opens it to sign-up on the Sign-in page, which is chosen while the application runs\n"
	several       = "    a person can be in several tenants (the auth's package hands its identity client no identity.WithOneTenantPerPerson)\n"
	staffMakes    = "    who creates a tenant: nobody directly; an administrator of the staff auth invites one person, whose first sign-in makes the tenant, and a person who signed up to the staff auth creates their own while one of its methods is open to sign-up, which is chosen while the application runs\n"
	noDirectory   = "The tenants can have directories, but no method of this auth goes through an organization's directory, so its people come by invitation alone; a directory method whose roles the application gives (impulse add method) makes it "
	noDirectories = "The tenants have no directories (the tenant record declares none), so a tenant's people come by invitation alone"
	eachChooses   = " Whether a tenant takes a directory, and whether that directory's people get an account without an invitation, is each tenant's choice, made while the application runs on the tenant's Sign-in page.\n"
)

// TestWriteSignIn_skeletonsAndLodestar reads the sign-in section of the four skeleton
// candidates and of Lodestar: each auth with its methods' three choices in words and
// the shape they add up to by number and name, then the application's own choices. The
// choice made while the application runs, Open or Invite only and what a tenant does
// with its directory, is said to be so and never guessed. Lodestar's members auth takes
// its roles from a directory in an application with tenants, which is none of the eight
// shapes, and reads "no named shape".
func TestWriteSignIn_skeletonsAndLodestar(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		app  func(t *testing.T) *app.App
		want string
	}{
		{
			name: "solo: one auth with a password, no tenants",
			app:  func(t *testing.T) *app.App { t.Helper(); return rendered(t, "solo", nil) },
			want: "sign-in\n" +
				"  staff (pkg/auth/staff)\n" +
				passwordStaff +
				"    shape: 1 Invite only. It starts so, and stays so until an administrator opens a method to sign-up on the Sign-in page, which is chosen while the application runs: opening password makes it 4 Open sign-up.\n" +
				"  the application\n" +
				"    tenants: none\n",
		},
		{
			name: "tenanted: a password and any Google account, tenants with directories",
			app:  func(t *testing.T) *app.App { t.Helper(); return rendered(t, "tenanted", nil) },
			want: "sign-in\n" +
				"  staff (pkg/auth/staff)\n" +
				"    google (pkg/auth/staff/google.go:N): admits any Google account; the application gives the roles; invite only until an administrator opens it to sign-up on the Sign-in page, which is chosen while the application runs\n" +
				passwordStaff +
				several +
				"    shape: 8 Global users, each tenant chooses. A tenant's people can come through its organization's directory (the google method)." + eachChooses +
				"  the application\n" +
				"    tenants: the Tenant record (pkg/resources/tenants.go:N)\n" +
				"    a tenant's own directory: a tenant can have one, set from a sign-in through it (the TenantDirectory record, pkg/resources/tenant_directories.go:N)\n" +
				staffMakes,
		},
		{
			name: "outlets: the staff by password, the members through one Microsoft tenant",
			app:  func(t *testing.T) *app.App { t.Helper(); return rendered(t, "outlets", nil) },
			want: "sign-in\n" +
				"  members (pkg/auth/members)\n" +
				"    directory, azure (pkg/auth/members/directory.go:N): admits the accounts of one Microsoft Entra tenant; the application gives the roles; invite only and never opened to sign-up, since a person who signed up to this auth could be given no tenant\n" +
				several +
				"    shape: 8 Global users, each tenant chooses. A tenant's people can come through its organization's directory (the directory method)." + eachChooses +
				"  staff (pkg/auth/staff)\n" +
				passwordStaff +
				several +
				"    shape: 7 Global users, invite only. " + noDirectory + "8 Global users, each tenant chooses.\n" +
				"  the application\n" +
				"    tenants: the Tenant record (pkg/resources/tenants.go:N)\n" +
				"    a tenant's own directory: a tenant can have one, set from a sign-in through it (the TenantDirectory record, pkg/resources/tenant_directories.go:N)\n" +
				staffMakes,
		},
		{
			name: "sites: one auth by password over two sites, the console's tenants",
			app:  func(t *testing.T) *app.App { t.Helper(); return rendered(t, "sites", nil) },
			want: "sign-in\n" +
				"  staff (pkg/auth/staff)\n" +
				passwordStaff +
				several +
				"    shape: 7 Global users, invite only. " + noDirectory + "8 Global users, each tenant chooses.\n" +
				"  the application\n" +
				"    tenants: the Tenant record (apps/console/pkg/resources/tenants.go:N)\n" +
				"    a tenant's own directory: a tenant can have one, set from a sign-in through it (the TenantDirectory record, apps/console/pkg/resources/tenant_directories.go:N)\n" +
				staffMakes,
		},
		{
			name: "Lodestar: the crew's four methods, and the members' directory, which gives the roles",
			app: func(t *testing.T) *app.App {
				t.Helper()

				a, err := app.Discover(filepath.Join("..", "..", "..", "..", "lodestar"))
				if err != nil {
					t.Fatalf("app.Discover() error = %v", err)
				}

				return a
			},
			want: "sign-in\n" +
				"  crew (pkg/auth/crew)\n" +
				"    password (pkg/auth/crew/crew.go:N): admits anyone with an email address; the application gives the roles; invite only until an administrator opens it to sign-up on the Sign-in page, which is chosen while the application runs\n" +
				"    google (pkg/auth/crew/crew.go:N): how wide it is open is not read here; the application gives the roles; invite only until an administrator opens it to sign-up on the Sign-in page, which is chosen while the application runs\n" +
				"    sign-up, google (pkg/auth/crew/crew.go:N): admits any Google account; the application gives the roles; invite only until an administrator opens it to sign-up on the Sign-in page, which is chosen while the application runs\n" +
				"    persona (pkg/auth/crew/crew.go:N): admits the personas of the test environments' persona provider; the application gives the roles; left invite only, since its accounts are the cast the deployment seeds\n" +
				several +
				"    shape: 8 Global users, each tenant chooses. A tenant's people can come through its organization's directory (the google and sign-up methods)." + strings.TrimSuffix(eachChooses, "\n") +
				" The persona method is registered in the test environments alone, where the persona provider is configured, and is part of no shape.\n" +
				"  members (pkg/auth/members)\n" +
				"    directory, google (pkg/auth/members/members.go:N): how wide it is open is not read here; the directory gives the roles; no invite-only or open choice, since everyone the directory signs in becomes a user with the roles it names\n" +
				several +
				"    shape: no named shape: its directory method takes the roles from the directory, and no shape has both tenants and roles from a directory\n" +
				"  the application\n" +
				"    tenants: the Sector record (pkg/resources/sectors.go:N)\n" +
				"    a tenant's own directory: a tenant can have one, set from a sign-in through it (the SectorDirectory record, pkg/resources/sector_directories.go:N)\n" +
				"    who creates a tenant: nobody directly; an administrator of the crew auth invites one person, whose first sign-in makes the tenant, and a person who signed up to the crew auth creates their own while one of its methods is open to sign-up, which is chosen while the application runs\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.want, signIn(tt.app(t))); diff != "" {
				t.Errorf("WriteSignIn() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestWriteSignIn_tenantShapes reads the shapes of an application with tenants that no
// skeleton has as rendered: a person held to one tenant, with and without a way through
// an organization's directory, and tenants that have no directories, which are read as
// the invite-only shape with a directory method as without one: the reading says in one
// sentence that the tenants have no directories, and a Google method is named in it as
// a way for an invited person to sign in.
func TestWriteSignIn_tenantShapes(t *testing.T) {
	t.Parallel()

	// oneTenant hands the staff auth's identity client the option that says a person
	// belongs to one tenant, as impulse add tenancy --one-tenant-per-person writes it.
	oneTenant := func(text string) string {
		return strings.Replace(text, "identity.WithAdministratorRoles(tenantAdministrators))", "identity.WithAdministratorRoles(tenantAdministrators), identity.WithOneTenantPerPerson())", 1)
	}
	tests := []struct {
		name      string
		candidate string
		edits     map[string]func(text string) string
		want      []string
	}{
		{
			// The shape as impulse new starts it: no password method, a Google method
			// open to any Google Workspace and a Microsoft method open to any Microsoft
			// Entra tenant, and the persona method of the test environments, which is
			// part of no shape and keeps the auth out of none.
			name: "one tenant per person and the two methods through a directory", candidate: "tenanted",
			edits: byDirectory(t, oneTenant),
			want: []string{
				"    azure (pkg/auth/staff/azure.go:N): admits the accounts of any Microsoft Entra tenant; the application gives the roles;",
				"    google (pkg/auth/staff/google.go:N): admits the accounts of any Google Workspace; the application gives the roles;",
				"    persona (pkg/auth/staff/persona.go:N): admits the personas of the test environments' persona provider; the application gives the roles; left invite only, since its accounts are the cast the deployment seeds\n",
				"    a person belongs to one tenant (identity.WithOneTenantPerPerson, pkg/auth/staff/staff.go:N)\n",
				"    shape: 6 Tenant-owned users, by directory. A tenant's people can come through its organization's directory (the azure and google methods)." + strings.TrimSuffix(eachChooses, "\n") + " The persona method is registered in the test environments alone, where the persona provider is configured, and is part of no shape.\n",
			},
		},
		{
			// An application made before the shape lost its password: nothing refuses
			// it, and the reading names the password method as what keeps it outside.
			name: "one tenant per person and a method through a directory beside a password", candidate: "tenanted",
			edits: map[string]func(text string) string{"pkg/auth/staff/staff.go": oneTenant},
			want: []string{
				"    a person belongs to one tenant (identity.WithOneTenantPerPerson, pkg/auth/staff/staff.go:N)\n",
				"    shape: no named shape: its password method keeps it outside 6 Tenant-owned users, by directory, the shape its tenants' choices and the google method make: in that shape every person of a tenant signs in through the tenant's own directory, so a password could enter no tenant. No command removes a sign-in method; the password method is taken out by hand (remove pkg/auth/staff/password.go and the registerPassword call in pkg/auth/staff/staff.go, take generation.PasswordMethod() out of the auth's declaration on its outlets in the generator program, and run go generate ./...)\n",
			},
		},
		{
			name: "one tenant per person, no password, and a method as wide as any Google account", candidate: "tenanted",
			edits: map[string]func(text string) string{"pkg/auth/staff/staff.go": oneTenant, "pkg/auth/staff/password.go": nil},
			want: []string{
				"    shape: 6 Tenant-owned users, by directory. A tenant's people can come through its organization's directory (the google method)." + strings.TrimSuffix(eachChooses, "\n") +
					" The google method admits any Google account, which works and is not the typical width of this shape (the accounts of any Google Workspace or of any Microsoft Entra tenant, with no personal accounts).\n",
			},
		},
		{
			name: "one tenant per person and a password alone", candidate: "sites",
			edits: map[string]func(text string) string{"pkg/auth/staff/staff.go": oneTenant},
			want: []string{
				"    a person belongs to one tenant (identity.WithOneTenantPerPerson, pkg/auth/staff/staff.go:N)\n",
				"    shape: 5 Tenant-owned users, invite only. The tenants can have directories, but no method of this auth goes through an organization's directory, so its people come by invitation alone. 6 Tenant-owned users, by directory has no password sign-in, so a directory method beside the password method (impulse add method) leaves this auth outside the eight shapes until the password method is taken out by hand.\n",
			},
		},
		{
			name: "tenants with no directories and a Google method", candidate: "tenanted",
			edits: map[string]func(text string) string{"pkg/resources/tenant_directories.go": nil},
			want: []string{
				"    google (pkg/auth/staff/google.go:N): admits any Google account; the application gives the roles;",
				several + "    shape: 7 Global users, invite only. " + noDirectories + ", and the google method is a way for an invited person to sign in and nothing more.\n",
				"    a tenant's own directory: none (no record is annotated @tenantDirectory)\n",
			},
		},
		{
			name: "tenants with no directories, one tenant per person and a Google method", candidate: "tenanted",
			edits: map[string]func(text string) string{"pkg/resources/tenant_directories.go": nil, "pkg/auth/staff/staff.go": oneTenant},
			want: []string{
				"    a person belongs to one tenant (identity.WithOneTenantPerPerson, pkg/auth/staff/staff.go:N)\n",
				"    shape: 5 Tenant-owned users, invite only. " + noDirectories + ", and the google method is a way for an invited person to sign in and nothing more.\n",
				"    a tenant's own directory: none (no record is annotated @tenantDirectory)\n",
			},
		},
		{
			name: "tenants with no directories and a password alone", candidate: "sites",
			edits: map[string]func(text string) string{"apps/console/pkg/resources/tenant_directories.go": nil},
			want: []string{
				several + "    shape: 7 Global users, invite only. " + noDirectories + ".\n",
				"    a tenant's own directory: none (no record is annotated @tenantDirectory)\n",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := signIn(rendered(t, tt.candidate, tt.edits))
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("WriteSignIn() lacks %q:\n%s", want, got)
				}
			}
		})
	}
}

// authFile renders an auth package's file constructing one Auth and registering the
// methods the statements name on it.
func authFile(pkg, statements string, decls ...string) string {
	return `package ` + pkg + `

import (
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/azure"
	"github.com/impulseframework/impulse/session/google"
	"github.com/impulseframework/impulse/session/oidc"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/session/signin"
)

func New(client any, key string, settings *Settings) {
	authSession, _ := session.NewAuth[session.NoCustomData, session.NoCustomData](sessionstorage.NewSpannerAuth(client), key, session.NoSuperAdministrators(), settings.Mail)
` + statements + `
}
` + strings.Join(decls, "\n")
}

// TestWriteSignIn reads the sign-in section of applications without tenants, written
// here: no auth; an auth whose directory gives the roles, which is Directory roles; an
// auth with a method of each width, which starts as Invite only and says which method
// makes it Directory members and which Open sign-up once an administrator opens it; and
// the combinations outside the eight shapes, which read "no named shape" with why and
// are no error.
func TestWriteSignIn(t *testing.T) {
	t.Parallel()

	const (
		password  = "\t_ = session.PasswordSignIn(authSession)\n"
		workspace = "\tworkspaces, _ := workspaceSlot(settings)\n\t_, _ = google.SignIn(authSession, \"workspace\", nil, &google.Config{Workspaces: workspaces, RoleSync: google.DisableRoleSync()})\n"
		synced    = "\t_, _ = google.SignIn(authSession, \"workspace\", nil, &google.Config{Workspaces: google.Workspace(\"fleet\", \"fleet.example\"), RoleSync: google.RoleSync(nil, \"\", nil)})\n"
		anyTenant = "\ttenants := entraSlot()\n\t_, _ = azure.SignIn(authSession, \"entra\", nil, &azure.Config{Tenants: tenants, RoleSync: signin.DisableRoleSync()})\n"
		provider  = "\t_ = oidc.SignIn(authSession, \"okta\", nil, &oidc.Config{Issuer: settings.Okta.Issuer, ClientSecret: settings.Okta.ClientSecret, RoleSync: signin.DisableRoleSync()})\n"
		unread    = "\t_ = oidc.SignIn(authSession, \"okta\", nil, oktaConfig(settings))\n"

		workspaceSlot = "func workspaceSlot(settings *Settings) (google.WorkspaceSet, error) {\n\treturn google.Workspace(\"workspace\", settings.Workspace.HostedDomain), nil\n}\n"
		entraSlot     = "func entraSlot() azure.TenantSet {\n\treturn azure.Tenants(azure.AnyTenant(), azure.PersonalAccounts())\n}\n"

		noTenants = "  the application\n    tenants: none\n"
		running   = "invite only until an administrator opens it to sign-up on the Sign-in page, which is chosen while the application runs\n"
		starts    = "    shape: 1 Invite only. It starts so, and stays so until an administrator opens a method to sign-up on the Sign-in page, which is chosen while the application runs: "
	)
	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{
			name: "no auth",
			want: "sign-in\n  no auth (no session Auth is constructed outside tests)\n" + noTenants,
		},
		{
			name:  "a directory that gives the roles, alone in its auth",
			files: map[string]string{"pkg/auth/members/members.go": authFile("members", synced)},
			want: "sign-in\n  members (pkg/auth/members)\n" +
				"    workspace, google (pkg/auth/members/members.go:14): admits the accounts of one Google Workspace; the directory gives the roles; no invite-only or open choice, since everyone the directory signs in becomes a user with the roles it names\n" +
				"    shape: 3 Directory roles\n" + noTenants,
		},
		{
			name:  "a method of each width, which an administrator may open",
			files: map[string]string{"pkg/auth/staff/staff.go": authFile("staff", password+workspace+anyTenant+provider, workspaceSlot, entraSlot)},
			want: "sign-in\n  staff (pkg/auth/staff)\n" +
				"    password (pkg/auth/staff/staff.go:14): admits anyone with an email address; the application gives the roles; " + running +
				"    workspace, google (pkg/auth/staff/staff.go:16): admits the accounts of one Google Workspace; the application gives the roles; " + running +
				"    entra, azure (pkg/auth/staff/staff.go:18): admits the accounts of any Microsoft Entra tenant, and Microsoft personal accounts; the application gives the roles; " + running +
				"    okta, oidc (pkg/auth/staff/staff.go:19): admits the accounts of one organization's identity provider; the application gives the roles; " + running +
				starts + "opening workspace or okta makes it 2 Directory members, and opening password or entra makes it 4 Open sign-up.\n" + noTenants,
		},
		{
			name: "two auths, each with its own shape",
			files: map[string]string{
				"pkg/auth/members/members.go": authFile("members", synced),
				"pkg/auth/staff/staff.go":     authFile("staff", password),
			},
			want: "sign-in\n  members (pkg/auth/members)\n" +
				"    workspace, google (pkg/auth/members/members.go:14): admits the accounts of one Google Workspace; the directory gives the roles; no invite-only or open choice, since everyone the directory signs in becomes a user with the roles it names\n" +
				"    shape: 3 Directory roles\n" +
				"  staff (pkg/auth/staff)\n" +
				"    password (pkg/auth/staff/staff.go:14): admits anyone with an email address; the application gives the roles; " + running +
				starts + "opening password makes it 4 Open sign-up.\n" + noTenants,
		},
		{
			name:  "outside the eight: a directory that gives the roles beside another method",
			files: map[string]string{"pkg/auth/members/members.go": authFile("members", password+synced)},
			want: "sign-in\n  members (pkg/auth/members)\n" +
				"    password (pkg/auth/members/members.go:14): admits anyone with an email address; the application gives the roles; " + running +
				"    workspace, google (pkg/auth/members/members.go:15): admits the accounts of one Google Workspace; the directory gives the roles; no invite-only or open choice, since everyone the directory signs in becomes a user with the roles it names\n" +
				"    shape: no named shape: its password method is outside 3 Directory roles, the shape its workspace method makes: the directory gives every role, so a person who signed in with a password would hold none, and a method whose roles come from the directory stands alone in its auth (impulse check refuses it). " +
				"No command removes a sign-in method, so one of the two is changed by hand. To have the directory give the roles, take the password method out (remove pkg/auth/members/password.go and the registerPassword call in pkg/auth/members/members.go, take generation.PasswordMethod() out of the auth's declaration on its outlets in the generator program, and run go generate ./...). " +
				"To keep the password, have the application give the roles instead: set the workspace method's RoleSync to DisableRoleSync (pkg/auth/members/members.go:15), take SyncsRoles() off that method in the auth's declaration on its outlets in the generator program, and run go generate ./...; impulse audit then names the shape, and impulse check says what an auth whose roles the application gives still needs\n" + noTenants,
		},
		{
			name:  "outside the eight: a method whose choices are written where they are not read",
			files: map[string]string{"pkg/auth/staff/staff.go": authFile("staff", unread)},
			want: "sign-in\n  staff (pkg/auth/staff)\n" +
				"    okta, persona (pkg/auth/staff/staff.go:14): how wide it is open is not read here; who gives the roles is not read here; whether it can be opened to sign-up follows who gives the roles\n" +
				"    shape: no named shape: who gives the roles of its okta method is not read here, and the shape follows it\n" + noTenants,
		},
		{
			name:  "an Auth constructed outside an auth package",
			files: map[string]string{"pkg/config/session.go": authFile("config", password)},
			want: "sign-in\n  the Auth constructed at pkg/config/session.go:13, outside an auth package\n" +
				"    password (pkg/config/session.go:14): admits anyone with an email address; the application gives the roles; " + running +
				starts + "opening password makes it 4 Open sign-up.\n" + noTenants,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			files := map[string]string{"go.mod": "module example.com/harbor\n\ngo 1.26.6\n"}
			for rel, content := range tt.files {
				files[rel] = content
			}
			for rel, content := range files {
				if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			a, err := app.Discover(root)
			if err != nil {
				t.Fatalf("app.Discover() error = %v", err)
			}
			var out strings.Builder
			WriteSignIn(&out, a)
			if diff := cmp.Diff(tt.want, out.String()); diff != "" {
				t.Errorf("WriteSignIn() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
