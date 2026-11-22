package authpkg

import (
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/generation"
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
		}, TenantPolicy: &TenantPolicy{Record: "Tenant", Reader: "app.NewTenantDeclarationReader", Directories: true, AdmitWithoutTenant: true}},
		{Module: "example.com/beacon", AuthDir: "pkg/auth", Name: "invited", IdentityDir: "pkg/identity", Doc: "the people of tenants that have no directories", Methods: []Method{
			{Kind: app.MethodPassword},
			{Kind: app.MethodGoogle, Name: "google", Slot: SlotAny, Authority: app.AuthorityApplication},
		}, TenantPolicy: &TenantPolicy{Record: "Tenant", Reader: "app.NewTenantDeclarationReader", AdmitWithoutTenant: true}},
		{Module: "example.com/beacon", AuthDir: "pkg/auth", Name: "closed", Methods: []Method{
			{Kind: app.MethodAzure, Name: "directory", Authority: app.AuthorityApplication},
		}, TenantPolicy: &TenantPolicy{Record: "Tenant", Reader: "app.NewTenantDeclarationReader", Directories: true}},
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

// TestFiles_firstAdministratorInvited holds how an auth's first administrator comes in:
// by an invitation mailed to the address the placement names, whichever methods the auth
// has. The foothold file asks the session library for the invitation
// (session.AuthAPI.InviteFirstAdministrator) under the convention's variable, hands it
// the user manager of the auth's own permission engine to ask who holds the Super
// Administrator role, creates no account and no password, and is the same code for a
// password auth, for a directory of one organization and for a directory method open to
// any number of them, an organization's identity provider or several methods: the
// invitation, and not the method's width, ties the person's first sign-in to the roles.
// An auth with no generated management invites nobody, since nothing would give the
// invitation's roles, and says so; an auth whose directory writes the roles has no
// foothold file.
func TestFiles_firstAdministratorInvited(t *testing.T) {
	t.Parallel()

	const (
		invites   = "a.handlers.API().InviteFirstAdministrator(ctx, a.access.UserManager(), &session.FirstAdministratorRequest{Email: email, RoleNames: roles})"
		reinvites = "a.handlers.API().ReinviteFirstAdministrator(ctx, a.access.UserManager(), &session.FirstAdministratorRequest{Email: email, RoleNames: roles})"
		variable  = `const AdministratorVariable = "APP_MEMBERS_ADMINISTRATOR"`
		nobody    = "The deployment invites no first administrator for this auth."
	)
	application := func(kind, name, slot string) Method {
		return Method{Kind: kind, Name: name, Slot: slot, Authority: app.AuthorityApplication}
	}
	tests := []struct {
		name    string
		methods []Method
		// noManagement renders the auth for an application that generates no management
		// of its people.
		noManagement bool
		// wantFoothold says the package holds a foothold file, and wantInvites that it
		// invites a first administrator.
		wantFoothold, wantInvites bool
	}{
		{name: "the password method", methods: []Method{{Kind: app.MethodPassword}}, wantFoothold: true, wantInvites: true},
		{name: "one Microsoft Entra tenant", methods: []Method{application(app.MethodAzure, "directory", SlotOne)}, wantFoothold: true, wantInvites: true},
		{name: "one Google Workspace", methods: []Method{application(app.MethodGoogle, "google", SlotOne)}, wantFoothold: true, wantInvites: true},
		{name: "any tenant", methods: []Method{application(app.MethodAzure, "directory", SlotAny)}, wantFoothold: true, wantInvites: true},
		{name: "listed tenants", methods: []Method{application(app.MethodAzure, "directory", SlotListed)}, wantFoothold: true, wantInvites: true},
		{name: "one tenant and personal accounts", methods: []Method{application(app.MethodAzure, "directory", SlotOnePersonal)}, wantFoothold: true, wantInvites: true},
		{name: "any Workspace", methods: []Method{application(app.MethodGoogle, "google", SlotAnyWorkspace)}, wantFoothold: true, wantInvites: true},
		{name: "any Google account", methods: []Method{application(app.MethodGoogle, "google", SlotAny)}, wantFoothold: true, wantInvites: true},
		{name: "an organization's identity provider", methods: []Method{application(app.MethodOIDC, "okta", "")}, wantFoothold: true, wantInvites: true},
		{
			name:    "several directory methods",
			methods: []Method{application(app.MethodAzure, "partners", SlotOne), application(app.MethodGoogle, "google", SlotAny), application(app.MethodOIDC, "okta", "")}, wantFoothold: true, wantInvites: true,
		},
		{
			name:    "the password method beside a directory",
			methods: []Method{{Kind: app.MethodPassword}, application(app.MethodAzure, "directory", SlotAny)}, wantFoothold: true, wantInvites: true,
		},
		{name: "the persona method beside a directory", methods: []Method{application(app.MethodPersona, "", ""), application(app.MethodAzure, "directory", SlotOne)}, wantFoothold: true, wantInvites: true},
		{name: "no generated management: nothing would give the invitation's roles", methods: []Method{{Kind: app.MethodPassword}}, noManagement: true, wantFoothold: true},
		{
			name:    "a directory that writes the roles ranks its people itself",
			methods: []Method{{Kind: app.MethodAzure, Name: "staff", Slot: SlotOne, Authority: app.AuthorityDirectory}},
		},
	}
	// The code of the foothold file of every auth that invites is one text: nothing in
	// it turns on the auth's methods.
	var code string
	for _, tt := range tests {
		spec := &Spec{Module: "example.com/beacon", AuthDir: "pkg/auth", Name: "members", IdentityDir: "pkg/identity", Methods: tt.methods}
		if tt.noManagement {
			spec.IdentityDir = ""
		}
		files, err := Files(spec)
		if err != nil {
			t.Fatalf("%s: Files() error = %v", tt.name, err)
		}
		if got := Foothold(spec); got != tt.wantFoothold {
			t.Errorf("%s: Foothold() = %v, want %v", tt.name, got, tt.wantFoothold)
		}
		if got := InvitesAdministrator(spec); got != tt.wantInvites {
			t.Errorf("%s: InvitesAdministrator() = %v, want %v", tt.name, got, tt.wantInvites)
		}
		foothold, ok := files[footholdFile]
		if ok != tt.wantFoothold {
			t.Fatalf("%s: Files() wrote a %s = %v, want %v", tt.name, footholdFile, ok, tt.wantFoothold)
		}
		if !ok {
			continue
		}
		text := string(foothold)
		for _, want := range []string{invites, reinvites, variable} {
			if got := strings.Contains(text, want); got != tt.wantInvites {
				t.Errorf("%s: %s holds %q = %v, want %v", tt.name, footholdFile, want, got, tt.wantInvites)
			}
		}
		if got := strings.Contains(text, nobody); got == tt.wantInvites {
			t.Errorf("%s: %s says the deployment invites nobody = %v, want %v", tt.name, footholdFile, got, !tt.wantInvites)
		}
		// Nothing of the account the deployment used to create: no user, no password, no
		// id derived for a seed, and no registration named for a sign-in name.
		for _, gone := range []string{"CreateSessionUser", "CreateUserRequest", "SeedUserID", "SeedAdministrator", "password string", "usersTable"} {
			if strings.Contains(text, gone) {
				t.Errorf("%s: %s still holds %q: the first administrator is invited, and no account is created ahead of them", tt.name, footholdFile, gone)
			}
		}
		if !tt.wantInvites {
			continue
		}
		body := functionsOf(t, foothold, "InviteAdministrator", "ReinviteAdministrator", "firstAdministratorRoleNames")
		if code == "" {
			code = body
		}
		if body != code {
			t.Errorf("%s: the foothold's code differs from the first auth's, want one text whatever the methods (-first +this):\n%s", tt.name, cmp.Diff(code, body))
		}
	}
	if code == "" {
		t.Fatal("no case rendered a foothold that invites")
	}
}

// functionsOf renders the named functions of a rendered Go file, bodies and no comments,
// in the order given: what the file does, apart from what it says.
func functionsOf(t *testing.T, src []byte, names ...string) string {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, footholdFile, src, parser.AllErrors)
	if err != nil {
		t.Fatalf("parser.ParseFile(%s) error = %v", footholdFile, err)
	}
	var b strings.Builder
	for _, name := range names {
		found := false
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name != name {
				continue
			}
			found = true
			fn.Doc = nil
			if err := format.Node(&b, fset, fn); err != nil {
				t.Fatalf("format.Node(%s) error = %v", name, err)
			}
			b.WriteString("\n")
		}
		if !found {
			t.Fatalf("%s declares no %s", footholdFile, name)
		}
	}

	return b.String()
}

// TestFiles_superAdministrator holds what every auth package writes for the role the
// framework defines in every auth: the Auth is built told who its super administrators
// are, so their sign-in carries a second factor whatever the sign-in rule asks; the engine is
// built with the methods that give and take the auth's roles where the application
// generates them; the first administrator's invitation gives the role; and a persona the
// cast names a super administrator is seated in the global partition. An auth whose
// roles the directory writes says the directory decides who is a super administrator,
// and promises no second factor of the library's own.
func TestFiles_superAdministrator(t *testing.T) {
	t.Parallel()
	const (
		told    = "\t\tsettings.CookieKey,\n\t\tsession.SuperAdministratorsBy(signin.SuperAdministrators(resource.SignInRoles(accessClient.UserManager()))),\n"
		methods = "access.WithSuperAdministratorMethods(%sidentity.SuperAdministratorMethods()...),"
		invited = "names := []string{string(accesstypes.SuperAdministrator)}"
		seated  = "case role == accesstypes.SuperAdministrator, definesRole(roles.Roles.Global, role):"
		tenants = "identity.WithAdministratorRoles(tenantAdministrators)"
		// What the package says of the holder's sign-in: the library's factor where
		// the application gives the roles, the directory's word where it writes them.
		factor    = "signs in with a second factor whatever"
		directory = "the directory decides who is a super administrator"
	)
	tests := []struct {
		name string
		spec *Spec
		// wantMethods says the engine is handed the generated management's methods,
		// wantFoothold that the application gives the auth's roles, wantInvited that a
		// first administrator is invited, and wantSeated that the package holds a cast.
		wantMethods, wantFoothold, wantInvited, wantSeated bool
	}{
		{
			name: "a password auth with its generated management", wantMethods: true, wantFoothold: true, wantInvited: true,
			spec: &Spec{Module: "example.com/beacon", AuthDir: "pkg/auth", Name: "staff", IdentityDir: "pkg/identity", Methods: []Method{{Kind: app.MethodPassword}}},
		},
		{
			name: "a password auth with no generated management invites nobody", wantFoothold: true,
			spec: &Spec{Module: "example.com/beacon", AuthDir: "pkg/auth", Name: "staff", Methods: []Method{{Kind: app.MethodPassword}}},
		},
		{
			name: "a directory auth whose roles the application gives", wantMethods: true, wantFoothold: true, wantInvited: true,
			spec: &Spec{Module: "example.com/beacon", AuthDir: "pkg/auth", Name: "members", IdentityDir: "pkg/identity", Methods: []Method{
				{Kind: app.MethodAzure, Name: "directory", Authority: app.AuthorityApplication},
			}},
		},
		{
			name: "a directory auth whose roles the directory gives invites nobody", wantMethods: true,
			spec: &Spec{Module: "example.com/beacon", AuthDir: "pkg/auth", Name: "employees", IdentityDir: "pkg/identity", Methods: []Method{
				{Kind: app.MethodOIDC, Name: "company-sso", Authority: app.AuthorityDirectory},
			}},
		},
		{
			name: "an auth with a cast", wantMethods: true, wantFoothold: true, wantInvited: true, wantSeated: true,
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
			synced := !tt.wantFoothold
			if got := strings.Count(auth, factor); (got == 0) != synced {
				t.Errorf("%s.go promises the library's second factor for a holder %d times, want none = %v", tt.spec.Name, got, synced)
			}
			if got := strings.Contains(auth, directory); got != synced {
				t.Errorf("%s.go says the directory decides who is a super administrator = %v, want %v", tt.spec.Name, got, synced)
			}
			if got := strings.Contains(auth, fmt.Sprintf(methods, tt.spec.Name)); got != tt.wantMethods {
				t.Errorf("%s.go hands the engine the methods that give and take the auth's roles = %v, want %v", tt.spec.Name, got, tt.wantMethods)
			}
			if got := strings.Contains(auth, tenants); got != (tt.wantMethods && tt.wantFoothold) {
				t.Errorf("%s.go names the tenant's administrator roles alone to the identity client = %v, want %v", tt.spec.Name, got, tt.wantMethods && tt.wantFoothold)
			}
			foothold, ok := files[footholdFile]
			if ok != tt.wantFoothold {
				t.Fatalf("Files(%s) wrote a %s = %v, want %v", tt.spec.Name, footholdFile, ok, tt.wantFoothold)
			}
			if got := strings.Contains(string(foothold), invited); got != tt.wantInvited {
				t.Errorf("%s invites the first administrator to the Super Administrator role (%q) = %v, want %v", footholdFile, invited, got, tt.wantInvited)
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

// TestFiles_castSeed holds the cast's seed to how it treats a persona seeded earlier. It
// takes the writer it says each role on and refuses to run with none; it seeds a cast
// file read and checked as cast.json is, the package's own or one handed to it; it looks
// for the persona's account under the id the seed chose, and passes over a user of
// another id under the persona's login before any role is written; it reads what the
// account holds in each place and writes a role only where the account does not hold it,
// so an unchanged cast writes nothing; it says each role it gives on a line; and it takes
// nothing, a role that left the cast included.
func TestFiles_castSeed(t *testing.T) {
	t.Parallel()
	files, err := Files(&Spec{Module: "example.com/beacon", AuthDir: "pkg/auth", Name: "crew", IdentityDir: "pkg/identity", Methods: []Method{
		{Kind: app.MethodPassword},
		{Kind: app.MethodPersona},
	}})
	if err != nil {
		t.Fatalf("Files(crew) error = %v", err)
	}
	seed := string(files[CastGoFile])
	for _, tt := range []struct {
		name, want string
	}{
		{name: "the seed takes the writer it says each role on", want: "func (a *Auth) SeedCast(ctx context.Context, out io.Writer) (int, error)"},
		{name: "the seed of the package's own cast is the seed of a cast file", want: "return a.SeedCastFile(ctx, out, castFile)"},
		{name: "a seed with no writer is refused before anything is read or written", want: "func (a *Auth) SeedCastFile(ctx context.Context, out io.Writer, file []byte) (int, error) {\n\tif out == nil {\n\t\treturn 0, errors.New(\"the cast's seed was handed no writer to say the roles it gives on: hand it the command's output, or io.Discard to say nothing\")\n\t}"},
		{name: "a cast file handed to the seed is read and checked as cast.json is", want: "declared, err := cast.ReadCast(file)"},
		{name: "the persona's account is the one under the id the seed chose", want: "\tif _, err := api.User(ctx, id); err == nil {\n\t\treturn true, false, nil\n\t}"},
		{name: "a user of another id under the login is passed over before any role is written", want: "\t\tif taken {\n\t\t\tcontinue\n\t\t}\n\t\tif err := a.seatPersona(ctx, out, roles, p, id); err != nil {"},
		{name: "an account seeded earlier is given its roles and is not created again", want: "\t\tif err := a.seatPersona(ctx, out, roles, p, id); err != nil {\n\t\t\treturn created, err\n\t\t}\n\t\tif seeded {\n\t\t\tcontinue\n\t\t}"},
		{name: "what the account holds in a place is read", want: "held, err := manager.UserRoles(ctx, user, scope)"},
		{name: "a role the account holds is not written again", want: "\t\tif slices.Contains(holds, role) {\n\t\t\tcontinue\n\t\t}\n\t\tif err := manager.AddUserRoles(ctx, scope, user, role); err != nil {"},
		{name: "each role given is said on a line", want: `fmt.Fprintf(out, "Gave the %s auth's persona %s the role %s%s\n", Name, login, role, where)`},
	} {
		if !strings.Contains(seed, tt.want) {
			t.Errorf("%s: %s lacks %q", tt.name, CastGoFile, tt.want)
		}
	}
	for _, takes := range []string{"DeleteUserRoles", "DeleteRoleUsers"} {
		if strings.Contains(seed, takes) {
			t.Errorf("%s calls %s: the seed takes no role, one that left the cast included", CastGoFile, takes)
		}
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

// TestFiles_passwordConfirmation pins that an auth package hands the router the password
// confirmation of a waiting sign-in exactly where the auth has the password method. The
// generated router asks its session surface for PendingConfirmWithPassword only there,
// since with no password method nobody holds a password to confirm with; the rest of the
// waiting sign-in (its status, its cancel and the mailed code) is handed either way.
func TestFiles_passwordConfirmation(t *testing.T) {
	t.Parallel()
	const confirm = "func (h *Handlers) PendingConfirmWithPassword() http.HandlerFunc {\n\treturn h.Pending().ConfirmWithPassword()\n}"
	tests := []struct {
		name string
		spec *Spec
		want bool
	}{
		{
			name: "a password auth", want: true,
			spec: &Spec{Module: "example.com/beacon", AuthDir: "pkg/auth", Name: "staff", Methods: []Method{{Kind: app.MethodPassword}}},
		},
		{
			name: "a password beside a directory", want: true,
			spec: &Spec{Module: "example.com/beacon", AuthDir: "pkg/auth", Name: "tenants", Methods: []Method{
				{Kind: app.MethodGoogle, Name: "google", Slot: SlotAny, Authority: app.AuthorityApplication},
				{Kind: app.MethodPassword},
			}},
		},
		{
			name: "a directory auth whose roles the application gives",
			spec: &Spec{Module: "example.com/beacon", AuthDir: "pkg/auth", Name: "members", Methods: []Method{
				{Kind: app.MethodAzure, Name: "directory", Authority: app.AuthorityApplication},
			}},
		},
		{
			name: "a directory auth whose roles the directory gives",
			spec: &Spec{Module: "example.com/beacon", AuthDir: "pkg/auth", Name: "employees", Methods: []Method{
				{Kind: app.MethodOIDC, Name: "company-sso", Authority: app.AuthorityDirectory},
			}},
		},
		{
			name: "a directory auth with the personas of the test environments",
			spec: &Spec{Module: "example.com/beacon", AuthDir: "pkg/auth", Name: "members", Methods: []Method{
				{Kind: app.MethodGoogle, Name: "directory", Authority: app.AuthorityApplication},
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
			src := string(files[tt.spec.Name+".go"])
			if got := strings.Contains(src, confirm); got != tt.want {
				t.Errorf("%s.go hands the router the password confirmation = %v, want %v", tt.spec.Name, got, tt.want)
			}
			if !tt.want && strings.Contains(src, "ConfirmWithPassword") {
				t.Errorf("%s.go of an auth with no password method must not name ConfirmWithPassword", tt.spec.Name)
			}
			if got := strings.Contains(src, "waits for a password confirmation"); got != tt.want {
				t.Errorf("%s.go says a sign-in waits for a password confirmation = %v, want %v", tt.spec.Name, got, tt.want)
			}
			for _, kept := range []string{"func (h *Handlers) PendingStatus() http.HandlerFunc {", "func (h *Handlers) PendingCancel() http.HandlerFunc {", "func (h *Handlers) PendingVerifyCode() http.HandlerFunc {"} {
				if !strings.Contains(src, kept) {
					t.Errorf("%s.go is missing %q", tt.spec.Name, kept)
				}
			}
		})
	}
}

// TestFiles_tenantMaker pins which auth is handed what makes a tenant. Nobody creates a
// tenant directly: staff invite one person, and that person's first sign-in makes the
// tenant through the generated maker, which the generated identity client of one auth is
// constructed over. That auth is the one whose people may hold no tenant yet (its
// outlets serve the tenant record), where the application assigns the roles and
// generates the auth's management. Its Settings carry the maker, New refuses a nil one
// with a sentence, and the client is built over it; every other auth carries none of it,
// since its generated client takes no maker.
func TestFiles_tenantMaker(t *testing.T) {
	t.Parallel()
	carries := []string{
		"\tTenantMaker identity.TenantMaker\n",
		"\tif settings.TenantMaker == nil {\n\t\treturn nil, errors.New(\"staff.Settings.TenantMaker is nil: a tenant is made by an invitation's first sign-in, through the generated maker over the Tenant record (app.NewTenantMaker), which the data level hands in\")\n\t}\n",
		"staffidentity.NewClient(accessClient, authSession.API(), settings.TenantMaker, identity.WithFirstAdministratorRoles(FirstAdministratorRoles()), identity.WithAdministratorRoles(tenantAdministrators))",
	}
	policy := func(admitWithoutTenant bool) *TenantPolicy {
		return &TenantPolicy{Record: "Tenant", Reader: "app.NewTenantDeclarationReader", Directories: true, AdmitWithoutTenant: admitWithoutTenant}
	}
	tests := []struct {
		name string
		spec *Spec
		want bool
	}{
		{
			name: "the auth whose people may hold no tenant yet, with its generated management", want: true,
			spec: &Spec{Module: "example.com/beacon", AuthDir: "pkg/auth", Name: "staff", IdentityDir: "pkg/identity", Methods: []Method{{Kind: app.MethodPassword}}, TenantPolicy: policy(true)},
		},
		{
			name: "an auth whose people always hold a tenant: another auth's outlets serve the record",
			spec: &Spec{Module: "example.com/beacon", AuthDir: "pkg/auth", Name: "staff", IdentityDir: "pkg/identity", Methods: []Method{{Kind: app.MethodPassword}}, TenantPolicy: policy(false)},
		},
		{
			name: "an auth with no generated management",
			spec: &Spec{Module: "example.com/beacon", AuthDir: "pkg/auth", Name: "staff", Methods: []Method{{Kind: app.MethodPassword}}, TenantPolicy: policy(true)},
		},
		{
			name: "an auth whose roles a directory gives",
			spec: &Spec{Module: "example.com/beacon", AuthDir: "pkg/auth", Name: "staff", IdentityDir: "pkg/identity", Methods: []Method{
				{Kind: app.MethodOIDC, Name: "company-sso", Authority: app.AuthorityDirectory},
			}, TenantPolicy: policy(true)},
		},
		{
			name: "an auth of an application without tenants",
			spec: &Spec{Module: "example.com/beacon", AuthDir: "pkg/auth", Name: "staff", IdentityDir: "pkg/identity", Methods: []Method{{Kind: app.MethodPassword}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.spec.MakesTenants(); got != tt.want {
				t.Errorf("MakesTenants() = %v, want %v", got, tt.want)
			}
			files, err := Files(tt.spec)
			if err != nil {
				t.Fatalf("Files(%s) error = %v", tt.spec.Name, err)
			}
			rendered := string(files[tt.spec.Name+".go"])
			for _, text := range carries {
				if got := strings.Contains(rendered, text); got != tt.want {
					t.Errorf("%s.go holds %q = %v, want %v", tt.spec.Name, text, got, tt.want)
				}
			}
			if !tt.want && strings.Contains(rendered, "TenantMaker") {
				t.Errorf("%s.go names TenantMaker for an auth a tenant is not made through", tt.spec.Name)
			}
		})
	}
}

// TestFiles_tenantDirectories pins what an auth carries of a tenant's directory. In an
// application with tenant directories the auth whose roles the application assigns
// carries the directory reader, refuses a nil one and sets the directory admission. In
// an application whose tenants have no directories no auth carries any of it, whatever
// its methods, and the sign-in rule stands as it does everywhere: the library's tenant
// policy over the generated reader, which New still refuses nil.
func TestFiles_tenantDirectories(t *testing.T) {
	t.Parallel()
	directory := []string{
		"\tTenantDirectories signin.DirectoryReader[*cloudspanner.ReadWriteTransaction]\n",
		"\tif settings.TenantDirectories == nil {\n",
		"\tauthSession.SetDirectoryAdmission(settings.TenantDirectories, identityClient.DirectorySeated())\n",
		"// tenant alone: a tenant that is not directory only admits everyone it seats,\n",
		"// directory, makes that directory the tenant's own, all in the transaction that\n",
		"// tenant's directory are written with the invitation's consumption or not at all.\n",
	}
	everywhere := []string{
		"\tTenantDeclarations signin.DeclarationReader[*cloudspanner.ReadWriteTransaction]\n",
		"\tif settings.TenantDeclarations == nil {\n",
		"\tauthSession.SetSignInPolicy(signin.TenantPolicy(resource.SignInRoles(accessClient.UserManager()), settings.TenantDeclarations, signin.AdmitWithoutTenant()).Then(requireSecondFactor))\n",
		"\tTenantMaker identity.TenantMaker\n",
		"\tauthSession.SetInvitationAccepted(identityClient.InvitationAccepted())\n",
	}
	none := []string{
		"\t// ({{reader}}), which answers nothing for every tenant, since\n\t// the application has no tenant directories. Required; New refuses nil.\n",
		"\t// directories, so no tenant declares who may enter it and the generated reader\n\t// answers nothing for every tenant: a tenant admits everyone it seats, however they\n",
		"\t// directory is a way for an invited person to sign in and nothing more.\n",
		"\t// roles that administer it, in the transaction that consumes the invitation,\n\t// however they signed in: no tenant takes a directory.\n",
		"\t// is accepted there too: the tenant's row and the person's administrator roles are\n\t// written with the invitation's consumption or not at all.\n",
	}
	spec := func(directories bool) *Spec {
		return &Spec{
			Module: "example.com/beacon", AuthDir: "pkg/auth", Name: "staff", IdentityDir: "pkg/identity",
			Methods: []Method{
				{Kind: app.MethodPassword},
				{Kind: app.MethodGoogle, Name: "google", Slot: SlotAny, Authority: app.AuthorityApplication},
			},
			TenantPolicy: &TenantPolicy{Record: "Tenant", Reader: "app.NewTenantDeclarationReader", Directories: directories, AdmitWithoutTenant: true},
		}
	}
	tests := []struct {
		name        string
		directories bool
	}{
		{name: "an application with tenant directories", directories: true},
		{name: "an application whose tenants have no directories, with a Google method all the same"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := spec(tt.directories)
			if got := s.DirectoryAdmission(); got != tt.directories {
				t.Errorf("DirectoryAdmission() = %v, want %v", got, tt.directories)
			}
			if !s.MakesTenants() {
				t.Error("MakesTenants() = false: a tenant is made through the auth whether or not a tenant can take a directory")
			}
			files, err := Files(s)
			if err != nil {
				t.Fatalf("Files() error = %v", err)
			}
			rendered := string(files["staff.go"])
			for _, text := range everywhere {
				if !strings.Contains(rendered, text) {
					t.Errorf("staff.go lacks %q", text)
				}
			}
			for _, text := range directory {
				if got := strings.Contains(rendered, text); got != tt.directories {
					t.Errorf("staff.go holds %q = %v, want %v", text, got, tt.directories)
				}
			}
			for _, text := range none {
				text = strings.ReplaceAll(text, "{{reader}}", s.TenantPolicy.Reader)
				if got := strings.Contains(rendered, text); got == tt.directories {
					t.Errorf("staff.go holds %q = %v, want %v", text, got, !tt.directories)
				}
			}
			if !tt.directories {
				for _, word := range []string{"TenantDirectories", "SetDirectoryAdmission", "DirectoryReader", "DirectorySeated"} {
					if strings.Contains(rendered, word) {
						t.Errorf("staff.go names %q in an application whose tenants have no directories", word)
					}
				}
			}
		})
	}
}

// TestFiles_strangerThroughMethod pins what a directory method's file says of a person
// with no account, in an auth whose methods are never opened to sign-up (another auth's
// outlets serve the tenant record): with tenant directories a tenant's directory that
// names a default role is a second way in beside the invitation, and in an application
// whose tenants have no directories the invitation is the only one.
func TestFiles_strangerThroughMethod(t *testing.T) {
	t.Parallel()
	const second = "the directory of a tenant that names a default role"
	methods := []Method{
		{Kind: app.MethodGoogle, Name: "google", Slot: SlotAny, Authority: app.AuthorityApplication},
		{Kind: app.MethodAzure, Name: "entra", Slot: SlotAny, Authority: app.AuthorityApplication},
		{Kind: app.MethodOIDC, Name: "okta", Authority: app.AuthorityApplication},
	}
	for _, directories := range []bool{true, false} {
		spec := &Spec{
			Module: "example.com/beacon", AuthDir: "pkg/auth", Name: "members", Methods: methods,
			TenantPolicy: &TenantPolicy{Record: "Tenant", Reader: "app.NewTenantDeclarationReader", Directories: directories},
		}
		files, err := Files(spec)
		if err != nil {
			t.Fatalf("Files(directories %v) error = %v", directories, err)
		}
		for i := range methods {
			name := methods[i].Name + ".go"
			rendered := string(files[name])
			if rendered == "" {
				t.Fatalf("Files(directories %v) wrote no %s", directories, name)
			}
			if got := strings.Contains(strings.Join(strings.Fields(strings.ReplaceAll(rendered, "//", "")), " "), second); got != directories {
				t.Errorf("directories %v: %s says %q = %v, want %v", directories, name, second, got, directories)
			}
			if !strings.Contains(rendered, "is never opened to sign-up, since nobody") {
				t.Errorf("directories %v: %s does not say the method is never opened to sign-up", directories, name)
			}
		}
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
		{name: "an identity provider with the words its people read", method: Method{Kind: app.MethodOIDC, Name: "company-sso", Authority: app.AuthorityApplication, Label: "Lakeside ID"}},
		{name: "an identity provider whose words run over a line", method: Method{Kind: app.MethodOIDC, Name: "company-sso", Authority: app.AuthorityApplication, Label: "Lakeside\nsign-in"}, wantErr: "the words the login page shows for the provider, on one line"},
		{name: "an identity provider whose words carry a space before them", method: Method{Kind: app.MethodOIDC, Name: "company-sso", Authority: app.AuthorityApplication, Label: " Lakeside"}, wantErr: "with no space before or after them"},
		{name: "words for a Google method, which is shown by its brand", method: Method{Kind: app.MethodGoogle, Name: "directory", Authority: app.AuthorityApplication, Label: "Lakeside"}, wantErr: `a label names an organization's identity provider (the oidc method), whose words are otherwise made from its registration name; the google method is shown by what it is. To show it under other words, chain .Labeled("Lakeside") on its declaration in the generator program`},
		{name: "words for a Microsoft method, which is shown by its brand", method: Method{Kind: app.MethodAzure, Name: "entra", Authority: app.AuthorityApplication, Label: "Lakeside"}, wantErr: "the azure method is shown by what it is"},
		{name: "words for the password method, which is shown by what it is", method: Method{Kind: app.MethodPassword, Label: "Lakeside"}, wantErr: "the password method is shown by what it is"},
		{name: "words for the persona method, which is shown by what it is", method: Method{Kind: app.MethodPersona, Name: "persona", Label: "Lakeside"}, wantErr: "the persona method is shown by what it is"},
		{name: "a method impulse does not compose", method: Method{Kind: "ldap", Name: "ldap"}, wantErr: "password, azure, google, oidc or persona"},
		{name: "a method impulse does not compose, given words", method: Method{Kind: "ldap", Name: "ldap", Label: "Lakeside"}, wantErr: `"ldap" is not a sign-in method impulse composes`},
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
		{name: "an identity provider with the words its people read", method: Method{Kind: app.MethodOIDC, Name: "company-sso", Authority: app.AuthorityApplication, Label: "Lakeside ID"}, want: `generation.OIDCMethod("company-sso").Labeled("Lakeside ID")`},
		{name: "an identity provider with its words, whose token names the roles", method: Method{Kind: app.MethodOIDC, Name: "company-sso", Authority: app.AuthorityDirectory, Label: `The "Lakeside" sign-in`}, want: `generation.OIDCMethod("company-sso").Labeled("The \"Lakeside\" sign-in").SyncsRoles()`},
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

// TestDefaultWordsAreTheGeneratorsLabel holds the words the command says people read
// for an organization's identity provider added with none to the label the generation
// package gives its declaration: the brief quotes them, so the two are one rule.
func TestDefaultWordsAreTheGeneratorsLabel(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"sso", "okta", "company-sso", "id-2", "a"} {
		m := Method{Kind: app.MethodOIDC, Name: name, Authority: app.AuthorityApplication}
		if got, want := m.DefaultWords(), generation.OIDCMethod(name).Label(); got != want {
			t.Errorf("DefaultWords() of %s = %q, and the generation package's label is %q", name, got, want)
		}
		if got, want := m.Words(), generation.OIDCMethod(name).Label(); got != want {
			t.Errorf("Words() of %s with no label = %q, want the default %q", name, got, want)
		}
		m.Label = "Lakeside ID"
		if got, want := m.Words(), generation.OIDCMethod(name).Labeled("Lakeside ID").Label(); got != want {
			t.Errorf("Words() of %s with a label = %q, want %q", name, got, want)
		}
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
