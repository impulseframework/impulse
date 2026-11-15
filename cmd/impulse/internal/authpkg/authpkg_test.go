package authpkg

import (
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
			{Kind: app.MethodAzure, Name: "entra-any-open", Slot: SlotAny, Accounts: AccountsOpen, Authority: app.AuthorityApplication},
			{Kind: app.MethodAzure, Name: "entra-personal", Slot: SlotPersonal, Accounts: AccountsInvited, Authority: app.AuthorityApplication},
			{Kind: app.MethodAzure, Name: "entra-one-personal", Slot: SlotOnePersonal, Accounts: AccountsInvited, Authority: app.AuthorityApplication},
			{Kind: app.MethodAzure, Name: "entra-any-personal", Slot: SlotAnyPersonal, Accounts: AccountsOpen, Authority: app.AuthorityApplication},
			{Kind: app.MethodGoogle, Name: "google-one", Authority: app.AuthorityDirectory},
			{Kind: app.MethodGoogle, Name: "google-listed", Slot: SlotListed, Accounts: AccountsInvited, Authority: app.AuthorityApplication},
			{Kind: app.MethodGoogle, Name: "google-anyws", Slot: SlotAnyWorkspace, Accounts: AccountsOpen, Authority: app.AuthorityApplication},
			{Kind: app.MethodGoogle, Name: "google-any", Slot: SlotAny, Accounts: AccountsInvited, Authority: app.AuthorityApplication},
			{Kind: app.MethodGoogle, Name: "google-mixed", Slot: SlotMixed, Accounts: AccountsOpen, Authority: app.AuthorityApplication},
		}},
		{Module: "example.com/beacon", AuthDir: "pkg/auth", Name: "tenants", Doc: "the people of the tenants", Methods: []Method{
			{Kind: app.MethodPassword},
			{Kind: app.MethodGoogle, Name: "google", Slot: SlotAny, Accounts: AccountsOpen, Authority: app.AuthorityApplication},
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
			{Kind: app.MethodOIDC, Name: "okta", Accounts: AccountsInvited, Authority: app.AuthorityApplication},
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
			{Kind: app.MethodOIDC, Name: "okta", Accounts: AccountsInvited, Authority: app.AuthorityApplication},
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

func TestValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		method  Method
		wantErr string
	}{
		{name: "password takes no slot", method: Method{Kind: app.MethodPassword, Slot: SlotAny}, wantErr: "takes no slot"},
		{name: "password takes open sign-up", method: Method{Kind: app.MethodPassword, Accounts: AccountsOpen, UsernameIsEmail: true}},
		{name: "password takes no invited rule", method: Method{Kind: app.MethodPassword, Accounts: AccountsInvited}, wantErr: "the password method takes open"},
		{name: "username-is-email on a directory method", method: Method{Kind: app.MethodGoogle, Name: "x", Authority: app.AuthorityApplication, UsernameIsEmail: true}, wantErr: "username-is-email describes the password method"},
		{name: "open sign-up needs an open slot", method: Method{Kind: app.MethodAzure, Name: "x", Accounts: AccountsOpen, Authority: app.AuthorityApplication}, wantErr: "open sign-up is for any organization"},
		{name: "members with any tenant", method: Method{Kind: app.MethodAzure, Name: "x", Slot: SlotAny, Authority: app.AuthorityApplication}, wantErr: "sign up the world"},
		{name: "role sync beyond one tenant", method: Method{Kind: app.MethodAzure, Name: "x", Slot: SlotListed, Authority: app.AuthorityDirectory}, wantErr: "role synchronization follows one tenant"},
		{name: "google slot on azure", method: Method{Kind: app.MethodAzure, Name: "x", Slot: SlotMixed, Authority: app.AuthorityApplication}, wantErr: "a azure method takes"},
		{name: "authority required", method: Method{Kind: app.MethodGoogle, Name: "x"}, wantErr: "authority"},
		{name: "bad registration name", method: Method{Kind: app.MethodGoogle, Name: "Dir", Authority: app.AuthorityApplication}, wantErr: "registration"},
		{name: "persona takes no slot", method: Method{Kind: app.MethodPersona, Name: "persona", Slot: SlotAny}, wantErr: "the persona method takes no slot"},
		{name: "persona under the directory authority", method: Method{Kind: app.MethodPersona, Name: "persona", Authority: app.AuthorityDirectory}, wantErr: "the persona method takes the application authority alone"},
		{name: "persona admitting open sign-up", method: Method{Kind: app.MethodPersona, Name: "persona", Accounts: AccountsOpen}, wantErr: "the persona method admits invited users only"},
		{name: "persona with username-is-email", method: Method{Kind: app.MethodPersona, Name: "persona", UsernameIsEmail: true}, wantErr: "a persona signs in as the login its cast names"},
		{name: "persona named password", method: Method{Kind: app.MethodPersona, Name: "password"}, wantErr: "is the password method's registration name"},
		{name: "persona with nothing but its name", method: Method{Kind: app.MethodPersona, Name: "persona"}},
		{name: "persona under the application authority, invited", method: Method{Kind: app.MethodPersona, Name: "reviewers", Authority: app.AuthorityApplication, Accounts: AccountsInvited}},
		{name: "an identity provider without a name", method: Method{Kind: app.MethodOIDC, Authority: app.AuthorityApplication}, wantErr: "an identity provider's registration needs a name"},
		{name: "an identity provider takes no slot", method: Method{Kind: app.MethodOIDC, Name: "okta", Slot: SlotOne, Authority: app.AuthorityApplication}, wantErr: "takes no slot: the provider is one organization's"},
		{name: "an identity provider without an authority", method: Method{Kind: app.MethodOIDC, Name: "okta"}, wantErr: "authority"},
		{name: "an identity provider admitting open sign-up", method: Method{Kind: app.MethodOIDC, Name: "okta", Accounts: AccountsOpen, Authority: app.AuthorityApplication}, wantErr: "open sign-up is for a method anyone can use"},
		{name: "an identity provider with username-is-email", method: Method{Kind: app.MethodOIDC, Name: "okta", Authority: app.AuthorityApplication, UsernameIsEmail: true}, wantErr: "a provider's people sign in as the provider names them"},
		{name: "an identity provider named password", method: Method{Kind: app.MethodOIDC, Name: "password", Authority: app.AuthorityApplication}, wantErr: "is the password method's registration name"},
		{name: "an identity provider whose name is not a route segment", method: Method{Kind: app.MethodOIDC, Name: "Okta", Authority: app.AuthorityApplication}, wantErr: "registration"},
		{name: "an identity provider under the application authority, invited", method: Method{Kind: app.MethodOIDC, Name: "okta", Accounts: AccountsInvited, Authority: app.AuthorityApplication}},
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
// kind, registration name, account rule and authority: .InvitedUsers() under the
// invited-users rule, .SyncsRoles() under the directory authority, .OpenSignUp() under
// open sign-up, and .UsernameIsEmail() on the password method that takes it.
func TestDeclaration(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		method Method
		want   string
	}{
		{name: "a Google Workspace whose groups write the roles", method: Method{Kind: app.MethodGoogle, Name: "directory", Authority: app.AuthorityDirectory}, want: `generation.GoogleMethod("directory").SyncsRoles()`},
		{name: "a Google Workspace whose roles the application assigns", method: Method{Kind: app.MethodGoogle, Name: "directory", Authority: app.AuthorityApplication}, want: `generation.GoogleMethod("directory")`},
		{name: "listed Workspaces admitting invited users", method: Method{Kind: app.MethodGoogle, Name: "listed", Slot: SlotListed, Accounts: AccountsInvited, Authority: app.AuthorityApplication}, want: `generation.GoogleMethod("listed").InvitedUsers()`},
		{name: "any Google account under open sign-up", method: Method{Kind: app.MethodGoogle, Name: "sign-up", Slot: SlotAny, Accounts: AccountsOpen, Authority: app.AuthorityApplication}, want: `generation.GoogleMethod("sign-up").OpenSignUp()`},
		{name: "one Workspace and any Google account, invited", method: Method{Kind: app.MethodGoogle, Name: "mixed", Slot: SlotMixed, Accounts: AccountsInvited, Authority: app.AuthorityApplication}, want: `generation.GoogleMethod("mixed").InvitedUsers()`},
		{name: "an Entra tenant whose application roles write the roles", method: Method{Kind: app.MethodAzure, Name: "entra", Authority: app.AuthorityDirectory}, want: `generation.AzureMethod("entra").SyncsRoles()`},
		{name: "an Entra tenant whose roles the application assigns", method: Method{Kind: app.MethodAzure, Name: "entra", Authority: app.AuthorityApplication}, want: `generation.AzureMethod("entra")`},
		{name: "an Entra tenant and personal accounts, invited", method: Method{Kind: app.MethodAzure, Name: "entra", Slot: SlotOnePersonal, Accounts: AccountsInvited, Authority: app.AuthorityApplication}, want: `generation.AzureMethod("entra").InvitedUsers()`},
		{name: "any Entra tenant under open sign-up", method: Method{Kind: app.MethodAzure, Name: "entra", Slot: SlotAny, Accounts: AccountsOpen, Authority: app.AuthorityApplication}, want: `generation.AzureMethod("entra").OpenSignUp()`},
		{name: "the password method", method: Method{Kind: app.MethodPassword, Name: app.PasswordRegistration}, want: `generation.PasswordMethod()`},
		{name: "the password method signing strangers up by their address", method: Method{Kind: app.MethodPassword, Name: app.PasswordRegistration, Accounts: AccountsOpen, UsernameIsEmail: true}, want: `generation.PasswordMethod().OpenSignUp().UsernameIsEmail()`},
		{name: "the persona method, whose personas are the invited users", method: Method{Kind: app.MethodPersona, Name: "persona"}, want: `generation.PersonaMethod("persona").InvitedUsers()`},
		{name: "the persona method under another registration name", method: Method{Kind: app.MethodPersona, Name: "reviewers", Authority: app.AuthorityApplication, Accounts: AccountsInvited}, want: `generation.PersonaMethod("reviewers").InvitedUsers()`},
		{name: "an identity provider whose members the application gives roles", method: Method{Kind: app.MethodOIDC, Name: "okta", Authority: app.AuthorityApplication}, want: `generation.OIDCMethod("okta")`},
		{name: "an identity provider admitting invited users", method: Method{Kind: app.MethodOIDC, Name: "okta", Accounts: AccountsInvited, Authority: app.AuthorityApplication}, want: `generation.OIDCMethod("okta").InvitedUsers()`},
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
// where the file registers the method with RoleSync, .InvitedUsers() exactly where it
// admits invited users only, and .OpenSignUp() exactly where it admits open sign-up.
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
			if got, want := strings.Contains(file, "signin.InvitedUsersOnly"), arg.InvitedUsers; got != want {
				t.Errorf("%s/%s.go admits invited users only = %v, and the declaration %s says InvitedUsers = %v", spec.Name, m.Name, got, m.Declaration(), want)
			}
			if got, want := strings.Contains(file, "signin.OpenSignUp") || strings.Contains(file, "session.OpenSignUp()"), arg.OpenSignUp; got != want {
				t.Errorf("%s/%s.go admits open sign-up = %v, and the declaration %s says OpenSignUp = %v", spec.Name, m.Name, got, m.Declaration(), want)
			}
		}
	}
}
