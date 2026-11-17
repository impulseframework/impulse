package authpkg

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/app"
)

// smokeSpecs are the auth packages the rendering is held to: the skeleton's two, and one
// of every slot and rule shape the proof application composes.
func smokeSpecs() []*Spec {
	return []*Spec{
		{Module: "example.com/beacon", AuthDir: "pkg/auth", Name: "staff", Doc: "the people who sign in to the console with a password", Methods: []Method{{Kind: app.MethodPassword}}},
		{Module: "example.com/beacon", AuthDir: "pkg/auth", Name: "members", Doc: "the people who sign in through the organization's directory", Methods: []Method{
			{Kind: app.MethodAzure, Name: "directory", Authority: app.AuthorityApplication},
		}},
		{Module: "example.com/beacon", AuthDir: "pkg/auth", Name: "proof", Methods: []Method{
			{Kind: app.MethodPassword},
			{Kind: app.MethodAzure, Name: "entra-one", Authority: app.AuthorityDirectory},
			{Kind: app.MethodAzure, Name: "entra-listed", Slot: SlotListed, Authority: app.AuthorityApplication},
			{Kind: app.MethodAzure, Name: "entra-any-open", Slot: SlotAny, Authority: app.AuthorityApplication},
			{Kind: app.MethodAzure, Name: "entra-personal", Slot: SlotPersonal, Authority: app.AuthorityApplication},
			{Kind: app.MethodAzure, Name: "entra-one-personal", Slot: SlotOnePersonal, Authority: app.AuthorityApplication},
			{Kind: app.MethodAzure, Name: "entra-any-personal", Slot: SlotAnyPersonal, Authority: app.AuthorityApplication},
			{Kind: app.MethodGoogle, Name: "google-one", Authority: app.AuthorityDirectory},
			{Kind: app.MethodGoogle, Name: "google-listed", Slot: SlotListed, Authority: app.AuthorityApplication},
			{Kind: app.MethodGoogle, Name: "google-anyws", Slot: SlotAnyWorkspace, Authority: app.AuthorityApplication},
			{Kind: app.MethodGoogle, Name: "google-any", Slot: SlotAny, Authority: app.AuthorityApplication},
			{Kind: app.MethodGoogle, Name: "google-mixed", Slot: SlotMixed, Authority: app.AuthorityApplication},
		}},
		{Module: "example.com/beacon", AuthDir: "pkg/auth", Name: "tenants", Doc: "the people of the tenants", Methods: []Method{
			{Kind: app.MethodPassword},
			{Kind: app.MethodGoogle, Name: "google", Slot: SlotAny, Authority: app.AuthorityApplication},
		}, TenantPolicy: &TenantPolicy{Record: "Tenant", Reader: "app.NewTenantDeclarationReader", AdmitWithoutTenant: true}},
		{Module: "example.com/beacon", AuthDir: "pkg/auth", Name: "closed", Methods: []Method{
			{Kind: app.MethodAzure, Name: "directory", Authority: app.AuthorityApplication},
		}, TenantPolicy: &TenantPolicy{Record: "Tenant", Reader: "app.NewTenantDeclarationReader"}},
		{Module: "example.com/beacon", AuthDir: "pkg/auth", Name: "crew", Doc: "the people who sign in to the console, and the personas of the test environments", Methods: []Method{
			{Kind: app.MethodPassword},
			{Kind: app.MethodPersona},
		}},
		{Module: "example.com/beacon", AuthDir: "pkg/auth", Name: "partners", Doc: "the people of a partner organization, who sign in through its identity provider beside the invited", Methods: []Method{
			{Kind: app.MethodPassword},
			{Kind: app.MethodOIDC, Name: "okta", Authority: app.AuthorityApplication},
			{Kind: app.MethodOIDC, Name: "keycloak", Authority: app.AuthorityApplication},
			{Kind: app.MethodPersona},
		}},
		{Module: "example.com/beacon", AuthDir: "pkg/auth", Name: "employees", Doc: "the people the organization's identity provider gives a role", Methods: []Method{
			{Kind: app.MethodOIDC, Name: "company-sso", Authority: app.AuthorityDirectory},
		}},
	}
}

// renderedFile is one Go file of a smoke spec's auth package, parsed.
type renderedFile struct {
	spec, name string
	file       *ast.File
}

// importNames are the names a file's imports bring into it: an import's own name where
// it gives one, else the last element of its path, passing over a major version
// (errors/v5 is errors). An import made for its effect alone brings no name.
func importNames(t *testing.T, file *ast.File) map[string]bool {
	t.Helper()

	names := map[string]bool{}
	for _, spec := range file.Imports {
		if spec.Name != nil {
			if spec.Name.Name != "_" && spec.Name.Name != "." {
				names[spec.Name.Name] = true
			}

			continue
		}
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Fatalf("strconv.Unquote(%s) error = %v", spec.Path.Value, err)
		}
		elements := strings.Split(path, "/")
		name := elements[len(elements)-1]
		if len(elements) > 1 && len(name) > 1 && name[0] == 'v' && strings.Trim(name[1:], "0123456789") == "" {
			name = elements[len(elements)-2]
		}
		names[name] = true
	}

	return names
}

// requireImportsNamed fails the test for every package a rendered file names and does
// not import. render formats what a template wrote and no more, and a file with a
// missing import formats cleanly: without this the file that does not compile is found
// by the first application that composes the method. A name is taken for a package's
// when some rendered file imports a package of that name, the file itself does not
// declare the name, and the file selects from it.
func requireImportsNamed(t *testing.T, files []renderedFile) {
	t.Helper()

	packages := map[string]bool{}
	for _, f := range files {
		maps.Copy(packages, importNames(t, f.file))
	}
	for _, f := range files {
		imported := importNames(t, f.file)
		reported := map[string]bool{}
		ast.Inspect(f.file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			// An identifier the file declares (a variable, a parameter, a receiver)
			// carries its declaration and is no package.
			base, ok := sel.X.(*ast.Ident)
			if !ok || base.Obj != nil || !packages[base.Name] || imported[base.Name] || reported[base.Name] {
				return true
			}
			reported[base.Name] = true
			t.Errorf("%s of the %s auth names %s.%s and imports no package %s", f.name, f.spec, base.Name, sel.Sel.Name, base.Name)

			return true
		})
	}
}

// TestFiles renders the smoke specs and, when AUTHPKG_OUT names a directory, writes each
// package under it as pkg/auth/<name>/ so the output can be compiled against the
// framework pins. Every rendered file parses and imports each package it names.
func TestFiles(t *testing.T) {
	t.Parallel()
	out := os.Getenv("AUTHPKG_OUT")
	fset := token.NewFileSet()
	var rendered []renderedFile
	for _, spec := range smokeSpecs() {
		files, err := Files(spec)
		if err != nil {
			t.Fatalf("Files(%s) error = %v", spec.Name, err)
		}
		if got, want := len(files), len(FileNames(spec)); got != want {
			t.Errorf("Files(%s) wrote %d files, want %d", spec.Name, got, want)
		}
		for name, data := range files {
			if !strings.HasPrefix(string(data), "// Package "+spec.Name) && !strings.HasPrefix(string(data), "package "+spec.Name) {
				t.Errorf("%s/%s does not start with the package clause", spec.Name, name)
			}
			if strings.HasSuffix(name, ".go") {
				// The parser's own resolution of identifiers is what tells a
				// variable from a package: it is left on.
				file, err := parser.ParseFile(fset, filepath.Join(spec.Name, name), data, parser.AllErrors)
				if err != nil {
					t.Fatalf("parser.ParseFile(%s/%s) error = %v", spec.Name, name, err)
				}
				rendered = append(rendered, renderedFile{spec: spec.Name, name: name, file: file})
			}
			if out == "" {
				continue
			}
			dir := filepath.Join(out, spec.AuthDir, spec.Name)
			if err := os.MkdirAll(dir, 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "roles.json"), []byte("{}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if name == CastGoFile {
				if err := os.WriteFile(filepath.Join(dir, "cast.json"), []byte(`{"personas": [{"login": "cadet", "name": "Cadet Example", "roles": []}]}`+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	requireImportsNamed(t, rendered)
}

// TestFiles_castRefusesTheFirstAdministratorsName holds the persona seed apart from the
// first administrator's: the two derive an account's id from its name, so where the
// auth seeds a first administrator with a password the cast refuses a persona of that
// name, and where it seeds none there is no name to refuse.
func TestFiles_castRefusesTheFirstAdministratorsName(t *testing.T) {
	t.Parallel()
	const refusal = "if id == session.SeedUserID(Name, Administrator) {"
	tests := []struct {
		name string
		spec *Spec
		want bool
	}{
		{name: "a password first administrator", want: true, spec: &Spec{Module: "example.com/beacon", AuthDir: "pkg/auth", Name: "crew", Methods: []Method{
			{Kind: app.MethodPassword},
			{Kind: app.MethodPersona},
		}}},
		{name: "no password first administrator", want: false, spec: &Spec{Module: "example.com/beacon", AuthDir: "pkg/auth", Name: "reviewers", Methods: []Method{
			{Kind: app.MethodOIDC, Name: "okta", Authority: app.AuthorityApplication},
			{Kind: app.MethodPersona},
		}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			files, err := Files(tt.spec)
			if err != nil {
				t.Fatalf("Files(%s) error = %v", tt.spec.Name, err)
			}
			cast, ok := files[CastGoFile]
			if !ok {
				t.Fatalf("Files(%s) wrote no %s", tt.spec.Name, CastGoFile)
			}
			if got := strings.Contains(string(cast), refusal); got != tt.want {
				t.Errorf("%s refuses a persona named as the first administrator = %v, want %v", CastGoFile, got, tt.want)
			}
		})
	}
}

// TestFiles_superAdministrator holds what every auth package writes for the role the
// framework defines in every auth: the Auth is built told who its super administrators
// are, so their sign-in carries a second factor whatever the sign-in rule asks; the engine is
// built with the methods that give and take the auth's roles where the application
// generates them; the first administrator is seeded holding the role; and a persona the
// cast names a super administrator is seated in the global partition.
func TestFiles_superAdministrator(t *testing.T) {
	t.Parallel()
	const (
		told    = "\t\tsettings.CookieKey,\n\t\tsession.SuperAdministratorsBy(signin.SuperAdministrators(resource.SignInRoles(accessClient.UserManager()))),\n"
		methods = "access.WithSuperAdministratorMethods(%sidentity.SuperAdministratorMethods()...),"
		seeded  = "global = append([]accesstypes.Role{accesstypes.SuperAdministrator}, global...)"
		seated  = "case role == accesstypes.SuperAdministrator, definesRole(roles.Roles.Global, role):"
		tenants = "identity.WithAdministratorRoles(tenantAdministrators)"
	)
	tests := []struct {
		name string
		spec *Spec
		// wantMethods says the engine is handed the generated management's methods,
		// wantSeeded that a first administrator is seeded, and wantSeated that the
		// package holds a cast.
		wantMethods, wantSeeded, wantSeated bool
	}{
		{
			name: "a password auth with its generated management", wantMethods: true, wantSeeded: true,
			spec: &Spec{Module: "example.com/beacon", AuthDir: "pkg/auth", Name: "staff", IdentityDir: "pkg/identity", Methods: []Method{{Kind: app.MethodPassword}}},
		},
		{
			name: "a password auth with no generated management", wantSeeded: true,
			spec: &Spec{Module: "example.com/beacon", AuthDir: "pkg/auth", Name: "staff", Methods: []Method{{Kind: app.MethodPassword}}},
		},
		{
			name: "a directory auth whose roles the application gives", wantMethods: true, wantSeeded: true,
			spec: &Spec{Module: "example.com/beacon", AuthDir: "pkg/auth", Name: "members", IdentityDir: "pkg/identity", Methods: []Method{
				{Kind: app.MethodAzure, Name: "directory", Authority: app.AuthorityApplication},
			}},
		},
		{
			name: "a directory auth whose roles the directory gives seeds nobody", wantMethods: true,
			spec: &Spec{Module: "example.com/beacon", AuthDir: "pkg/auth", Name: "employees", IdentityDir: "pkg/identity", Methods: []Method{
				{Kind: app.MethodOIDC, Name: "company-sso", Authority: app.AuthorityDirectory},
			}},
		},
		{
			name: "an auth with a cast", wantMethods: true, wantSeeded: true, wantSeated: true,
			spec: &Spec{Module: "example.com/beacon", AuthDir: "pkg/auth", Name: "crew", IdentityDir: "pkg/identity", Methods: []Method{
				{Kind: app.MethodPassword},
				{Kind: app.MethodPersona},
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			files, err := Files(tt.spec)
			if err != nil {
				t.Fatalf("Files(%s) error = %v", tt.spec.Name, err)
			}
			auth := string(files[tt.spec.Name+".go"])
			if !strings.Contains(auth, told) {
				t.Errorf("%s.go does not tell the Auth who its super administrators are: it lacks %q", tt.spec.Name, told)
			}
			if got := strings.Contains(auth, fmt.Sprintf(methods, tt.spec.Name)); got != tt.wantMethods {
				t.Errorf("%s.go hands the engine the methods that give and take the auth's roles = %v, want %v", tt.spec.Name, got, tt.wantMethods)
			}
			if got := strings.Contains(auth, tenants); got != (tt.wantMethods && tt.wantSeeded) {
				t.Errorf("%s.go names the tenant's administrator roles alone to the identity client = %v, want %v", tt.spec.Name, got, tt.wantMethods && tt.wantSeeded)
			}
			foothold, ok := files[footholdFile]
			if ok != tt.wantSeeded {
				t.Fatalf("Files(%s) wrote a %s = %v, want %v", tt.spec.Name, footholdFile, ok, tt.wantSeeded)
			}
			if ok && !strings.Contains(string(foothold), seeded) {
				t.Errorf("%s does not seed the first administrator with the Super Administrator role: it lacks %q", footholdFile, seeded)
			}
			cast, ok := files[CastGoFile]
			if ok != tt.wantSeated {
				t.Fatalf("Files(%s) wrote a %s = %v, want %v", tt.spec.Name, CastGoFile, ok, tt.wantSeated)
			}
			if ok && !strings.Contains(string(cast), seated) {
				t.Errorf("%s does not seat a super administrator persona in the global partition: it lacks %q", CastGoFile, seated)
			}
		})
	}
}

// TestFiles_signedInHook holds what an auth package sets for the roles given for a
// period: the generated identity client's hook for a sign-in that stands, which removes
// a person's ended roles, over the application's database. An auth whose roles the
// application gives sets it where it has a generated management; one whose directory
// writes the roles gives none for a period and sets none, and neither does one with no
// generated management, which has no client to answer it.
func TestFiles_signedInHook(t *testing.T) {
	t.Parallel()
	const hook = "authSession.SetSignedInHook(identityClient.SignedIn(resource.NewSpannerClient(db)))"
	tests := []struct {
		name string
		spec *Spec
		want bool
	}{
		{
			name: "a password auth with its generated management", want: true,
			spec: &Spec{Module: "example.com/beacon", AuthDir: "pkg/auth", Name: "staff", IdentityDir: "pkg/identity", Methods: []Method{{Kind: app.MethodPassword}}},
		},
		{
			name: "a directory auth whose roles the application gives", want: true,
			spec: &Spec{Module: "example.com/beacon", AuthDir: "pkg/auth", Name: "members", IdentityDir: "pkg/identity", Methods: []Method{
				{Kind: app.MethodAzure, Name: "directory", Authority: app.AuthorityApplication},
			}},
		},
		{
			name: "a directory auth whose roles the directory gives",
			spec: &Spec{Module: "example.com/beacon", AuthDir: "pkg/auth", Name: "employees", IdentityDir: "pkg/identity", Methods: []Method{
				{Kind: app.MethodOIDC, Name: "company-sso", Authority: app.AuthorityDirectory},
			}},
		},
		{
			name: "a password auth with no generated management",
			spec: &Spec{Module: "example.com/beacon", AuthDir: "pkg/auth", Name: "staff", Methods: []Method{{Kind: app.MethodPassword}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			files, err := Files(tt.spec)
			if err != nil {
				t.Fatalf("Files(%s) error = %v", tt.spec.Name, err)
			}
			if got := strings.Contains(string(files[tt.spec.Name+".go"]), hook); got != tt.want {
				t.Errorf("%s.go sets the hook that clears a person's ended roles at their sign-in = %v, want %v", tt.spec.Name, got, tt.want)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		method  Method
		wantErr string
	}{
		{name: "password takes no slot", method: Method{Kind: app.MethodPassword, Slot: SlotAny}, wantErr: "takes no slot"},
		{name: "username-is-email on a directory method", method: Method{Kind: app.MethodGoogle, Name: "x", Authority: app.AuthorityApplication, UsernameIsEmail: true}, wantErr: "username-is-email describes the password method"},
		{name: "role sync beyond one tenant", method: Method{Kind: app.MethodAzure, Name: "x", Slot: SlotListed, Authority: app.AuthorityDirectory}, wantErr: "role synchronization follows one tenant"},
		{name: "google slot on azure", method: Method{Kind: app.MethodAzure, Name: "x", Slot: SlotMixed, Authority: app.AuthorityApplication}, wantErr: "a azure method takes"},
		{name: "authority required", method: Method{Kind: app.MethodGoogle, Name: "x"}, wantErr: "authority"},
		{name: "bad registration name", method: Method{Kind: app.MethodGoogle, Name: "Dir", Authority: app.AuthorityApplication}, wantErr: "registration"},
		{name: "persona takes no slot", method: Method{Kind: app.MethodPersona, Name: "persona", Slot: SlotAny}, wantErr: "the persona method takes no slot"},
		{name: "persona under the directory authority", method: Method{Kind: app.MethodPersona, Name: "persona", Authority: app.AuthorityDirectory}, wantErr: "the persona method takes the application authority alone"},
		{name: "persona with username-is-email", method: Method{Kind: app.MethodPersona, Name: "persona", UsernameIsEmail: true}, wantErr: "a persona signs in as the login its cast names"},
		{name: "persona named password", method: Method{Kind: app.MethodPersona, Name: "password"}, wantErr: "is the password method's registration name"},
		{name: "persona with nothing but its name", method: Method{Kind: app.MethodPersona, Name: "persona"}},
		{name: "persona under the application authority", method: Method{Kind: app.MethodPersona, Name: "reviewers", Authority: app.AuthorityApplication}},
		{name: "an identity provider without a name", method: Method{Kind: app.MethodOIDC, Authority: app.AuthorityApplication}, wantErr: "an identity provider's registration needs a name"},
		{name: "an identity provider takes no slot", method: Method{Kind: app.MethodOIDC, Name: "okta", Slot: SlotOne, Authority: app.AuthorityApplication}, wantErr: "takes no slot: the provider is one organization's"},
		{name: "an identity provider without an authority", method: Method{Kind: app.MethodOIDC, Name: "okta"}, wantErr: "authority"},
		{name: "an identity provider with username-is-email", method: Method{Kind: app.MethodOIDC, Name: "okta", Authority: app.AuthorityApplication, UsernameIsEmail: true}, wantErr: "a provider's people sign in as the provider names them"},
		{name: "an identity provider named password", method: Method{Kind: app.MethodOIDC, Name: "password", Authority: app.AuthorityApplication}, wantErr: "is the password method's registration name"},
		{name: "an identity provider whose name is not a route segment", method: Method{Kind: app.MethodOIDC, Name: "Okta", Authority: app.AuthorityApplication}, wantErr: "registration"},
		{name: "an identity provider under the application authority", method: Method{Kind: app.MethodOIDC, Name: "okta", Authority: app.AuthorityApplication}},
		{name: "an identity provider whose token names the roles", method: Method{Kind: app.MethodOIDC, Name: "company-sso", Authority: app.AuthorityDirectory}},
		{name: "a method impulse does not compose", method: Method{Kind: "ldap", Name: "ldap"}, wantErr: "password, azure, google, oidc or persona"},
		{name: "fine", method: Method{Kind: app.MethodGoogle, Name: "directory", Authority: app.AuthorityDirectory}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.method.Validate()
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("Validate() error = %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("Validate() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

// TestDeclaration holds the declaration a generator program carries for a method to its
// kind, registration name and authority: .SyncsRoles() under the directory authority,
// and .UsernameIsEmail() on the password method that takes it. How wide a method is
// chains nothing, since whether it admits a person with no invitation is chosen in the
// running application.
func TestDeclaration(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		method Method
		want   string
	}{
		{name: "a Google Workspace whose groups write the roles", method: Method{Kind: app.MethodGoogle, Name: "directory", Authority: app.AuthorityDirectory}, want: `generation.GoogleMethod("directory").SyncsRoles()`},
		{name: "a Google Workspace whose roles the application assigns", method: Method{Kind: app.MethodGoogle, Name: "directory", Authority: app.AuthorityApplication}, want: `generation.GoogleMethod("directory")`},
		{name: "any Google account, however wide", method: Method{Kind: app.MethodGoogle, Name: "sign-up", Slot: SlotAny, Authority: app.AuthorityApplication}, want: `generation.GoogleMethod("sign-up")`},
		{name: "an Entra tenant whose application roles write the roles", method: Method{Kind: app.MethodAzure, Name: "entra", Authority: app.AuthorityDirectory}, want: `generation.AzureMethod("entra").SyncsRoles()`},
		{name: "an Entra tenant whose roles the application assigns", method: Method{Kind: app.MethodAzure, Name: "entra", Authority: app.AuthorityApplication}, want: `generation.AzureMethod("entra")`},
		{name: "any Entra tenant, however wide", method: Method{Kind: app.MethodAzure, Name: "entra", Slot: SlotAny, Authority: app.AuthorityApplication}, want: `generation.AzureMethod("entra")`},
		{name: "the password method", method: Method{Kind: app.MethodPassword, Name: app.PasswordRegistration}, want: `generation.PasswordMethod()`},
		{name: "the password method whose sign-in name is the address", method: Method{Kind: app.MethodPassword, Name: app.PasswordRegistration, UsernameIsEmail: true}, want: `generation.PasswordMethod().UsernameIsEmail()`},
		{name: "the persona method", method: Method{Kind: app.MethodPersona, Name: "persona"}, want: `generation.PersonaMethod("persona")`},
		{name: "the persona method under another registration name", method: Method{Kind: app.MethodPersona, Name: "reviewers", Authority: app.AuthorityApplication}, want: `generation.PersonaMethod("reviewers")`},
		{name: "an identity provider whose members the application gives roles", method: Method{Kind: app.MethodOIDC, Name: "okta", Authority: app.AuthorityApplication}, want: `generation.OIDCMethod("okta")`},
		{name: "an identity provider whose token names the roles", method: Method{Kind: app.MethodOIDC, Name: "company-sso", Authority: app.AuthorityDirectory}, want: `generation.OIDCMethod("company-sso").SyncsRoles()`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := tt.method.Validate(); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			if got := tt.method.Declaration(); got != tt.want {
				t.Errorf("Declaration() = %s, want %s", got, tt.want)
			}
		})
	}
}

// TestDeclarationMatchesMethodFile holds the declaration and the method file to the same
// rule for every method of the smoke specs: the declaration chains .SyncsRoles() exactly
// where the file registers the method with RoleSync. No method file names an account
// rule: the registration takes none, and every method starts invite only. The persona
// method's file alone declares seeded users (SeededUsers), which takes the provider's
// names for the application's own.
func TestDeclarationMatchesMethodFile(t *testing.T) {
	t.Parallel()
	for _, spec := range smokeSpecs() {
		files, err := Files(spec)
		if err != nil {
			t.Fatalf("Files(%s) error = %v", spec.Name, err)
		}
		for i := range spec.Methods {
			m := &spec.Methods[i]
			file := string(files[m.Name+".go"])
			arg := m.Arg()
			if got, want := strings.Contains(file, ".RoleSync("), arg.SyncsRoles; got != want {
				t.Errorf("%s/%s.go registers RoleSync = %v, and the declaration %s says SyncsRoles = %v", spec.Name, m.Name, got, m.Declaration(), want)
			}
			for _, retired := range []string{"Accounts:", "signin.InvitedUsersOnly", "signin.MembersOfDirectory", "signin.OpenSignUp", "session.OpenSignUp()"} {
				if strings.Contains(file, retired) {
					t.Errorf("%s/%s.go names %s: whether a method admits a person with no invitation is chosen in the running application, never in the method file", spec.Name, m.Name, retired)
				}
			}
			if got, want := strings.Contains(file, "SeededUsers: true"), m.Kind == app.MethodPersona; got != want {
				t.Errorf("%s/%s.go takes the provider's names for the application's own (SeededUsers) = %v, want %v: the persona method alone does", spec.Name, m.Name, got, want)
			}
		}
	}
}
