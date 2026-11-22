package shape

import (
	"testing"

	"github.com/impulseframework/impulse/app"
)

// TestReading_judgeTenants names the shape of an auth in an application with tenants,
// from the three things the code says: whether a person belongs to one tenant, whether
// the tenants can have directories, and whether a method of the auth goes through an
// organization's directory with the application giving the roles. Where the tenants
// have no directories the shape is the invite-only one whatever the auth's methods are,
// said in one sentence that a method through a directory extends and never changes.
func TestReading_judgeTenants(t *testing.T) {
	t.Parallel()

	var (
		password = app.Method{Kind: app.MethodPassword, Name: "password"}
		google   = app.Method{Kind: app.MethodGoogle, Name: "google", Authority: app.AuthorityApplication}
		entra    = app.Method{Kind: app.MethodAzure, Name: "entra", Authority: app.AuthorityApplication}
		okta     = app.Method{Kind: app.MethodOIDC, Name: "okta", Authority: app.AuthorityApplication}
		persona  = app.Method{Kind: app.MethodPersona, Name: "persona", Authority: app.AuthorityApplication}
		// The two methods Tenant-owned users, by directory, starts with, and one as wide
		// as any Google account.
		anyWorkspace = app.Method{Kind: app.MethodGoogle, Name: "google", Authority: app.AuthorityApplication, Workspaces: app.WorkspacesAny}
		anyTenant    = app.Method{Kind: app.MethodAzure, Name: "azure", Authority: app.AuthorityApplication, Tenants: app.TenantsAny}
		anyAccount   = app.Method{Kind: app.MethodGoogle, Name: "google", Authority: app.AuthorityApplication, Workspaces: app.WorkspacesAny, GoogleAccounts: true}
	)
	const (
		none     = "The tenants have no directories (the tenant record declares none), so a tenant's people come by invitation alone"
		declared = "The tenants can have directories, but no method of this auth goes through an organization's directory, so its people come by invitation alone; a directory method whose roles the application gives (impulse add method) makes it "
		chosen   = " Whether a tenant takes a directory, and whether that directory's people get an account without an invitation, is each tenant's choice, made while the application runs on the tenant's Sign-in page."
		// What the reading says where a person belongs to one tenant: the tenant takes
		// its directory when it is made, and only opening it is the tenant's choice.
		takes = " A tenant takes its directory when it is made: the organization its founder signs in from. Whether that directory's people get an account without an invitation is each tenant's choice, made while the application runs on the tenant's Sign-in page."
		// What the reading says of a width the shape does not typically have, after the
		// method's name and whom it admits.
		notTypical = ", which works and is not the typical width of this shape (the accounts of any Google Workspace or of any Microsoft Entra tenant, with no personal accounts)."
		byHand     = "remove pkg/auth/staff/password.go and the registerPassword call in pkg/auth/staff/staff.go, take generation.PasswordMethod() out of the auth's declaration on its outlets in the generator program, and run go generate ./..."
	)
	tests := []struct {
		name        string
		directories bool
		oneTenant   bool
		methods     []app.Method
		wantShape   string
		wantSays    string
	}{
		{name: "no directories, a password alone", methods: []app.Method{password}, wantShape: "global-users-invite-only", wantSays: none + "."},
		{name: "no directories, one tenant, a password alone", oneTenant: true, methods: []app.Method{password}, wantShape: "tenant-owned-users-invite-only", wantSays: none + "."},
		{
			name: "no directories, a Google method", methods: []app.Method{password, google},
			wantShape: "global-users-invite-only", wantSays: none + ", and the google method is a way for an invited person to sign in and nothing more.",
		},
		{
			name: "no directories, one tenant, a Google method", oneTenant: true, methods: []app.Method{password, google},
			wantShape: "tenant-owned-users-invite-only", wantSays: none + ", and the google method is a way for an invited person to sign in and nothing more.",
		},
		{
			name: "no directories, three directory methods", oneTenant: true, methods: []app.Method{google, entra, okta},
			wantShape: "tenant-owned-users-invite-only", wantSays: none + ", and the google, entra and okta methods are each a way for an invited person to sign in and nothing more.",
		},
		{name: "no directories, the persona method is no organization's", methods: []app.Method{password, persona}, wantShape: "global-users-invite-only", wantSays: none + "."},
		{
			name: "directories, a password alone", directories: true, methods: []app.Method{password},
			wantShape: "global-users-invite-only", wantSays: declared + "8 Global users, each tenant chooses.",
		},
		{
			// Tenant-owned users, by directory, has no password sign-in, so a directory
			// method beside this auth's password would not make it that shape.
			name: "directories, one tenant, a password alone", directories: true, oneTenant: true, methods: []app.Method{password},
			wantShape: "tenant-owned-users-invite-only",
			wantSays:  "The tenants can have directories, but no method of this auth goes through an organization's directory, so its people come by invitation alone. 6 Tenant-owned users, by directory has no password sign-in, so a directory method beside the password method (impulse add method) leaves this auth outside the eight shapes until the password method is taken out by hand.",
		},
		{
			name: "directories, a Google method", directories: true, methods: []app.Method{password, google},
			wantShape: "global-users-each-tenant-chooses", wantSays: "A tenant's people can come through its organization's directory (the google method)." + chosen,
		},
		{
			name: "directories, one tenant, the two methods the shape starts with", directories: true, oneTenant: true, methods: []app.Method{anyWorkspace, anyTenant},
			wantShape: "tenant-owned-users-by-directory", wantSays: "A tenant's people can come through its organization's directory (the google and azure methods)." + takes,
		},
		{
			name: "directories, one tenant, the persona method beside the two changes nothing", directories: true, oneTenant: true, methods: []app.Method{anyWorkspace, anyTenant, persona},
			wantShape: "tenant-owned-users-by-directory", wantSays: "A tenant's people can come through its organization's directory (the google and azure methods)." + takes,
		},
		{
			// An application made before the shape lost its password: named outside the
			// shape, with the hand step, and refused by nothing.
			name: "directories, one tenant, a password beside a Google method is outside the shape", directories: true, oneTenant: true, methods: []app.Method{password, anyWorkspace},
			wantSays: "its password method keeps it outside 6 Tenant-owned users, by directory, the shape its tenants' choices and the google method make: in that shape every person of a tenant signs in through the tenant's own directory, so a password could enter no tenant. No command removes a sign-in method; the password method is taken out by hand (" + byHand + ")",
		},
		{
			name: "directories, one tenant, a method as wide as any Google account works and is not typical", directories: true, oneTenant: true, methods: []app.Method{anyAccount, anyTenant},
			wantShape: "tenant-owned-users-by-directory",
			wantSays:  "A tenant's people can come through its organization's directory (the google and azure methods)." + takes + " The google method admits any Google account" + notTypical,
		},
		{
			name: "directories, one tenant, another identity provider works and is not typical", directories: true, oneTenant: true, methods: []app.Method{okta},
			wantShape: "tenant-owned-users-by-directory",
			wantSays:  "A tenant's people can come through its organization's directory (the okta method)." + takes + " The okta method admits the accounts of one organization's identity provider" + notTypical,
		},
		{
			name: "directories, one tenant, a method whose width is not read is not judged for it", directories: true, oneTenant: true, methods: []app.Method{google},
			wantShape: "tenant-owned-users-by-directory", wantSays: "A tenant's people can come through its organization's directory (the google method)." + takes,
		},
		{
			// Global users, each tenant chooses, keeps its password and its widths.
			name: "directories, several tenants, a password beside a method as wide as any Google account", directories: true, methods: []app.Method{password, anyAccount},
			wantShape: "global-users-each-tenant-chooses", wantSays: "A tenant's people can come through its organization's directory (the google method)." + chosen,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reading := &Reading{Tenants: &app.TenantRecord{Name: "Tenant"}}
			if tt.directories {
				reading.Directories = &app.DirectoryRecord{Name: "TenantDirectory"}
			}
			auth := &Auth{Name: "staff", Dir: "pkg/auth/staff", File: "pkg/auth/staff/staff.go"}
			if tt.oneTenant {
				auth.OneTenant = "pkg/auth/staff/staff.go:400"
			}
			reading.judgeTenants(auth, tt.methods)
			named := ""
			if auth.Shape != nil {
				named = auth.Shape.Key
			}
			if named != tt.wantShape {
				t.Fatalf("judgeTenants() named %q, want %q (it says %q)", named, tt.wantShape, auth.Says)
			}
			if auth.Says != tt.wantSays {
				t.Errorf("judgeTenants() says %q, want %q", auth.Says, tt.wantSays)
			}
		})
	}
}

// TestReading_judge_passwordAndPersona holds the two things the reading says of a
// method that is part of no shape's choices. A password method beside a method whose
// directory gives the roles is outside Directory roles, which has no password sign-in,
// and the reading names the password as what is outside; any other second method keeps
// the sentence it had. The persona method is the test environments': the shape is named
// as without it, with one sentence that says where the method is registered, and an
// auth that adds up to no shape is given no such sentence.
func TestReading_judge_passwordAndPersona(t *testing.T) {
	t.Parallel()

	var (
		password  = app.Method{Kind: app.MethodPassword, Name: "password"}
		synced    = app.Method{Kind: app.MethodGoogle, Name: "workspace", Authority: app.AuthorityDirectory, Workspaces: "one", File: "pkg/auth/staff/workspace.go", Line: 31}
		workspace = app.Method{Kind: app.MethodGoogle, Name: "google", Authority: app.AuthorityApplication, Workspaces: app.WorkspacesAny}
		tenant    = app.Method{Kind: app.MethodAzure, Name: "azure", Authority: app.AuthorityApplication, Tenants: app.TenantsAny}
		persona   = app.Method{Kind: app.MethodPersona, Name: "persona", Authority: app.AuthorityApplication}
	)
	const (
		personaSaid = "The persona method is registered in the test environments alone, where the persona provider is configured, and is part of no shape."
		chosen      = "A tenant's people can come through its organization's directory (the google and azure methods). A tenant takes its directory when it is made: the organization its founder signs in from. Whether that directory's people get an account without an invitation is each tenant's choice, made while the application runs on the tenant's Sign-in page."
	)
	tests := []struct {
		name      string
		tenants   bool
		methods   []app.Method
		wantShape string
		wantSays  string
	}{
		{name: "a directory that gives the roles, alone", methods: []app.Method{synced}, wantShape: "directory-roles"},
		{
			name: "a password beside it is named as what is outside the shape", methods: []app.Method{password, synced},
			wantSays: "its password method is outside 3 Directory roles, the shape its workspace method makes: the directory gives every role, so a person who signed in with a password would hold none, and a method whose roles come from the directory stands alone in its auth (impulse check refuses it). " +
				// The reading gives the step, since no command takes a method out: the
				// password goes, or the application gives the roles.
				"No command removes a sign-in method, so one of the two is changed by hand. To have the directory give the roles, take the password method out (remove pkg/auth/staff/password.go and the registerPassword call in pkg/auth/staff/staff.go, take generation.PasswordMethod() out of the auth's declaration on its outlets in the generator program, and run go generate ./...). " +
				"To keep the password, have the application give the roles instead: set the workspace method's RoleSync to DisableRoleSync (pkg/auth/staff/workspace.go:31), take SyncsRoles() off that method in the auth's declaration on its outlets in the generator program, and run go generate ./...; impulse audit then names the shape, and impulse check says what an auth whose roles the application gives still needs",
		},
		{
			name: "any other method beside it keeps its sentence", methods: []app.Method{synced, persona},
			wantSays: "its workspace method takes the roles from the directory beside another method, and a method whose roles come from the directory stands alone in its auth (impulse check refuses it)",
		},
		{
			name: "the persona method beside a password", methods: []app.Method{password, persona}, wantShape: "invite-only",
			wantSays: "It starts so, and stays so until an administrator opens a method to sign-up on the Sign-in page, which is chosen while the application runs: opening password makes it 4 Open sign-up. " + personaSaid,
		},
		{
			name: "the persona method beside the two methods of an application with tenants", tenants: true, methods: []app.Method{workspace, tenant, persona},
			wantShape: "tenant-owned-users-by-directory", wantSays: chosen + " " + personaSaid,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reading := &Reading{}
			auth := &Auth{Name: "staff", Dir: "pkg/auth/staff", File: "pkg/auth/staff/staff.go"}
			if tt.tenants {
				reading.Tenants, reading.Directories = &app.TenantRecord{Name: "Tenant"}, &app.DirectoryRecord{Name: "TenantDirectory"}
				auth.OneTenant = "pkg/auth/staff/staff.go:400"
			}
			reading.judge(auth, tt.methods)
			named := ""
			if auth.Shape != nil {
				named = auth.Shape.Key
			}
			if named != tt.wantShape {
				t.Fatalf("judge() named %q, want %q (it says %q)", named, tt.wantShape, auth.Says)
			}
			if auth.Says != tt.wantSays {
				t.Errorf("judge() says %q, want %q", auth.Says, tt.wantSays)
			}
		})
	}
}
