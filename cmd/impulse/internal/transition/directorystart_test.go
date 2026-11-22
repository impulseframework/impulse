package transition

import (
	"context"
	"encoding/json"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/go-playground/errors/v5"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/authpkg"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton"
)

// byDirectoryStart is the start of Tenant-owned users, by directory, as impulse new
// composes it: a Google method open to any Workspace, a Microsoft method open to any
// tenant, and the persona method, the application giving the roles.
func byDirectoryStart(auth string) DirectoryStart {
	return DirectoryStart{
		Auth: auth, ShapeKey: "tenant-owned-users-by-directory", ShapeName: "6 Tenant-owned users, by directory",
		Methods: []authpkg.Method{
			{Kind: app.MethodGoogle, Slot: authpkg.SlotAnyWorkspace, Authority: app.AuthorityApplication},
			{Kind: app.MethodAzure, Slot: authpkg.SlotAny, Authority: app.AuthorityApplication},
			{Kind: app.MethodPersona, Authority: app.AuthorityApplication},
		},
	}
}

// directoryRolesStart is the start of Directory roles, as impulse new composes it on
// the staff auth: one method through one organization, whose directory gives the roles.
func directoryRolesStart(kind, organization string) DirectoryStart {
	return DirectoryStart{
		Auth: "staff", ShapeKey: "directory-roles", ShapeName: "3 Directory roles", Organization: organization,
		Methods: []authpkg.Method{{Kind: kind, Slot: authpkg.SlotOne, Authority: app.AuthorityDirectory}},
	}
}

// providerIssuer is the issuer the tests name another identity provider by.
const providerIssuer = "https://id.fleet.example"

// providerStart is the start of Directory roles from another identity provider, as
// impulse new composes it on the staff auth: the provider's one method, registered under
// the application's word for the provider, with the provider named by its issuer.
func providerStart(issuer string) DirectoryStart {
	return DirectoryStart{
		Auth: "staff", ShapeKey: "directory-roles", ShapeName: "3 Directory roles", Organization: issuer,
		Methods: []authpkg.Method{{Kind: app.MethodOIDC, Name: "okta", Authority: app.AuthorityDirectory}},
	}
}

// newBase renders the base skeleton with its first auth named, as impulse new does
// before it composes an option, and reads it as an application.
func newBase(t *testing.T, auth string) *app.App {
	t.Helper()

	dir := t.TempDir()
	if _, err := skeleton.Render(&skeleton.Options{Version: "v0.1.0", WebVersion: "0.1.0", Candidate: skeleton.Base, Dir: dir, ModulePath: "example.com/acme/depot", Auth: auth}); err != nil {
		t.Fatalf("skeleton.Render() error = %v", err)
	}
	a, err := app.Discover(dir)
	if err != nil {
		t.Fatalf("app.Discover() error = %v", err)
	}

	return a
}

// regeneration is a runner whose go generate ./... does what the application's own
// generator does for an auth that declares no password method, or one whose directory
// gives the roles: it leaves the auth's generated management without the handlers named.
type regeneration struct {
	fakeExec
	a       *app.App
	removes []string
}

func (r *regeneration) Run(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, error) {
	if name == "go" && len(args) > 0 && args[0] == "generate" {
		for _, rel := range r.removes {
			if err := os.Remove(r.a.Abs(rel)); err != nil {
				return nil, errors.Wrap(err, "os.Remove()")
			}
		}
	}

	return r.fakeExec.Run(ctx, dir, env, name, args...)
}

// started applies the start to a new base whose regeneration removes the handlers named,
// and returns the application read again with the change.
func started(t *testing.T, d *DirectoryStart, removes ...string) (*app.App, *Change) {
	t.Helper()

	return startedOver(t, d, nil, removes...)
}

// startedOver applies the start to a new base somebody edited first, whose regeneration
// removes the handlers named, and returns the application read again with the change.
func startedOver(t *testing.T, d *DirectoryStart, edit func(a *app.App), removes ...string) (*app.App, *Change) {
	t.Helper()

	a := newBase(t, d.Auth)
	if edit != nil {
		edit(a)
	}
	for i, name := range removes {
		removes[i] = "pkg/identity/" + d.Auth + "handlers/zz_gen_" + name + ".go"
	}
	ch, err := d.Apply(context.Background(), a, &regeneration{a: a, removes: removes})
	if err != nil {
		t.Fatalf("DirectoryStart.Apply() error = %v", err)
	}
	after, err := app.Discover(a.Root)
	if err != nil {
		t.Fatalf("app.Discover() after the start: error = %v", err)
	}

	return after, ch
}

// granted lists the resources a role file grants anything on.
func granted(t *testing.T, a *app.App, rel string) []string {
	t.Helper()

	var file any
	if err := json.Unmarshal([]byte(read(t, a, rel)), &file); err != nil {
		t.Fatalf("%s is not JSON after the start: %v", rel, err)
	}
	var resources []string
	var walk func(v any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			if name, ok := v["resource"].(string); ok {
				resources = append(resources, name)
			}
			for _, child := range v {
				walk(child)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		}
	}
	walk(file)
	slices.Sort(resources)

	return slices.Compact(resources)
}

// skippedLine returns the change's skipped lines that begin with the prefix and hold
// every one of the texts.
func skippedLine(ch *Change, prefix string, texts ...string) []string {
	return linesWith(ch.Skipped, prefix, texts)
}

// leftLine returns the lines the change leaves to the author that begin with the prefix
// and hold every one of the texts.
func leftLine(ch *Change, prefix string, texts ...string) []string {
	return linesWith(ch.Left, prefix, texts)
}

func linesWith(lines []string, prefix string, texts []string) []string {
	var found []string
	for _, line := range lines {
		if strings.HasPrefix(line, prefix) && !slices.ContainsFunc(texts, func(text string) bool {
			return !strings.Contains(line, text)
		}) {
			found = append(found, line)
		}
	}

	return found
}

func TestDirectoryStart_Validate(t *testing.T) {
	t.Parallel()

	a := newBase(t, "staff")
	tests := []struct {
		name    string
		start   DirectoryStart
		wantErr string
	}{
		{name: "tenant-owned users, by directory", start: byDirectoryStart("staff")},
		{name: "directory roles through one Workspace", start: directoryRolesStart(app.MethodGoogle, "fleet.example")},
		{name: "directory roles through one Entra tenant", start: directoryRolesStart(app.MethodAzure, "3f2504e0-4f89-11d3-9a0c-0305e82c3301")},
		{name: "directory roles through another identity provider", start: providerStart(providerIssuer)},
		{name: "another identity provider under a path of its host", start: providerStart(providerIssuer + "/realms/fleet")},
		{name: "directory roles with no organization named keeps the placeholder", start: directoryRolesStart(app.MethodGoogle, "")},
		{name: "no method", start: DirectoryStart{Auth: "staff", ShapeName: "6 Tenant-owned users, by directory"}, wantErr: "names no sign-in method to write in the password's place"},
		{
			name: "a password method",
			start: DirectoryStart{Auth: "staff", ShapeName: "3 Directory roles", Methods: []authpkg.Method{
				{Kind: app.MethodPassword},
			}},
			wantErr: "3 Directory roles has no password sign-in, and the staff auth was given the password method",
		},
		{
			name:    "a Workspace that is not a domain",
			start:   directoryRolesStart(app.MethodGoogle, "Fleet"),
			wantErr: "--organization Fleet: a Google method's one organization is a Google Workspace, named by its domain in lowercase (fleet.example)",
		},
		{
			name:    "an Entra tenant that is not a tenant id",
			start:   directoryRolesStart(app.MethodAzure, "fleet.example"),
			wantErr: "--organization fleet.example: a Microsoft method's one organization is a Microsoft Entra tenant, named by its tenant id",
		},
		{
			name:    "an issuer that is not https",
			start:   providerStart("http://id.fleet.example"),
			wantErr: "--organization http://id.fleet.example: another identity provider's one organization is the provider, named by its issuer: an https URL with a host and no query or fragment (https://id.fleet.example)",
		},
		{
			name:    "a provider named by its domain",
			start:   providerStart("id.fleet.example"),
			wantErr: "--organization id.fleet.example: another identity provider's one organization is the provider, named by its issuer: an https URL",
		},
		{
			name:    "an issuer with a query",
			start:   providerStart(providerIssuer + "?realm=fleet"),
			wantErr: "--organization " + providerIssuer + "?realm=fleet: another identity provider's one organization is the provider, named by its issuer: an https URL",
		},
		{
			name:    "an issuer with a fragment",
			start:   providerStart(providerIssuer + "#fleet"),
			wantErr: "--organization " + providerIssuer + "#fleet: another identity provider's one organization is the provider, named by its issuer: an https URL",
		},
		{
			name:    "an issuer with no host",
			start:   providerStart("https:///fleet"),
			wantErr: "--organization https:///fleet: another identity provider's one organization is the provider, named by its issuer: an https URL",
		},
		{name: "an auth the application does not have", start: byDirectoryStart("crew"), wantErr: "crew"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.start.Validate(a)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("DirectoryStart.Validate() error = %v, want none", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("DirectoryStart.Validate() error = %v, want one containing %q", err, tt.wantErr)
			}
		})
	}
}

// TestDirectoryStart_Validate_onlyANewApplication holds the start to a new application:
// an auth that registers anything but the base's password method alone is refused, since
// no command removes a sign-in method from an application that has one.
func TestDirectoryStart_Validate_onlyANewApplication(t *testing.T) {
	t.Parallel()

	start := byDirectoryStart("members")
	after, _ := started(t, &start)

	err := start.Validate(after)
	if err == nil {
		t.Fatal("DirectoryStart.Validate() on an auth already started from the shape: no error, want a refusal")
	}
	for _, want := range []string{"the members auth registers azure, google, persona", "only when it is new", "No command removes a sign-in method from an application that has one"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("DirectoryStart.Validate() error = %v, want it to contain %q", err, want)
		}
	}
}

func TestDirectoryStart_Command(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		start DirectoryStart
		want  string
	}{
		{name: "tenant-owned users, by directory names no provider", start: byDirectoryStart("members"), want: "impulse new --shape tenant-owned-users-by-directory"},
		{
			name: "directory roles names its method and its organization", start: directoryRolesStart(app.MethodGoogle, "fleet.example"),
			want: "impulse new --shape directory-roles --method google:google --organization fleet.example",
		},
		{name: "directory roles with no organization named", start: directoryRolesStart(app.MethodAzure, ""), want: "impulse new --shape directory-roles --method azure:azure"},
		{
			name: "directory roles from another identity provider names its issuer", start: providerStart(providerIssuer),
			want: "impulse new --shape directory-roles --method oidc:okta --organization " + providerIssuer,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.start.Command(); got != tt.want {
				t.Errorf("DirectoryStart.Command() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestDirectoryStart_Meaning_mailbox holds the brief of a Directory roles start to saying
// that the environment needs its mailbox. A release of such an application invites
// nobody, so it asks for none, and the application still mails the sign-in code: to every
// sign-in the directory does not answer for, and, through Microsoft, to each person's
// first, since that directory vouches for no address, and through another identity
// provider to each person's first too, whatever the provider's token says of the
// address. A start that invites a first administrator says nothing of it here: its
// release refuses without the mailbox.
func TestDirectoryStart_Meaning_mailbox(t *testing.T) {
	t.Parallel()

	const (
		mailbox  = "Give each deployed environment its mailbox (`APP_MAIL_MAILBOX`, with the secret `APP_MAIL_APP_PASSWORD`) before anybody signs in. No release asks for one, since nobody is invited, and the application mails all the same: "
		noSender = "Without the mailbox the code is written to the server's log and reaches nobody, and the person is left at the login page's code step."
		first    = "Each person's first sign-in waits for one whatever the directory says of a second factor: Microsoft Entra ID vouches for no address, so the login page asks the person for theirs, and the code proves it."
	)
	tests := []struct {
		name  string
		start DirectoryStart
		wants []string
		never []string
	}{
		{
			name: "directory roles through Google", start: directoryRolesStart(app.MethodGoogle, "fleet.example"),
			wants: []string{
				mailbox + "a sign-in waits for a code mailed to the person's address unless the Workspace is one the deployment administers (`APP_STAFF_GOOGLE_ADMINISTERED_WORKSPACES`), or the browser is one the person asked to be remembered. " + noSender,
			},
			never: []string{first, "token says"},
		},
		{
			name: "directory roles through Microsoft", start: directoryRolesStart(app.MethodAzure, ""),
			wants: []string{
				mailbox + "a sign-in waits for a code mailed to the person's address unless the tenant is one the deployment administers (`APP_STAFF_AZURE_ADMINISTERED_TENANTS`), the directory's token says a second factor was passed, or the browser is one the person asked to be remembered. " + first + " " + noSender,
			},
		},
		{
			name: "directory roles through another identity provider", start: providerStart(providerIssuer),
			wants: []string{
				mailbox + "a sign-in waits for a code mailed to the person's address unless the provider's token says a second factor was passed (its `amr` claim names `mfa`), or the browser is one the person asked to be remembered. Each person's first sign-in waits for one whatever the token says, of a second factor or of their address (`email_verified`): the provider's word confirms no address, so the login page asks the person for theirs where the account holds none, and the code proves it. " + noSender,
			},
			never: []string{first, "Workspace", "tenant is one", "administers"},
		},
		{name: "tenant-owned users, by directory", start: byDirectoryStart("members"), never: []string{"its mailbox", "APP_MAIL_MAILBOX"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			meaning := tt.start.Meaning()
			for _, want := range tt.wants {
				if !strings.Contains(meaning, want) {
					t.Errorf("Meaning() lacks %q:\n%s", want, meaning)
				}
			}
			for _, never := range tt.never {
				if strings.Contains(meaning, never) {
					t.Errorf("Meaning() carries %q:\n%s", never, meaning)
				}
			}
		})
	}
}

// TestDirectoryStart_Apply_byDirectory holds what a new application that starts from
// Tenant-owned users, by directory is left with: an auth with no password method that
// registers Google, Microsoft and the persona sign-in, with the application giving the
// roles, and a development run that signs its administrator in as the cast's persona.
func TestDirectoryStart_Apply_byDirectory(t *testing.T) {
	t.Parallel()

	start := byDirectoryStart("members")
	a, ch := started(t, &start, "require_members_password_change", "remove_members_password", "send_members_user_password_reset_link", "set_members_self_service_reset")

	t.Run("the auth registers the three methods and no password", func(t *testing.T) {
		t.Parallel()

		var registered []string
		for i := range a.Auths {
			for j := range a.Auths[i].Methods {
				m := &a.Auths[i].Methods[j]
				registered = append(registered, m.Kind+":"+m.Name)
			}
		}
		slices.Sort(registered)
		if want := []string{"azure:azure", "google:google", "persona:persona"}; !slices.Equal(registered, want) {
			t.Errorf("the auth registers %v, want %v", registered, want)
		}
		for _, file := range []string{"google.go", "azure.go", "persona.go", "cast.go", "cast.json", "foothold.go"} {
			if !exists(a, "pkg/auth/members/"+file) {
				t.Errorf("pkg/auth/members/%s is not there", file)
			}
		}
		if exists(a, "pkg/auth/members/password.go") {
			t.Error("pkg/auth/members/password.go is still there: the auth keeps its password method")
		}
		for _, file := range []string{"members.go", "google.go", "azure.go", "persona.go", "cast.go", "foothold.go"} {
			if _, err := parser.ParseFile(token.NewFileSet(), a.Abs("pkg/auth/members/"+file), nil, parser.SkipObjectResolution); err != nil {
				t.Errorf("pkg/auth/members/%s does not parse: %v", file, err)
			}
		}
	})

	t.Run("each method is open to any organization and to no personal account", func(t *testing.T) {
		t.Parallel()

		google, azure := read(t, a, "pkg/auth/members/google.go"), read(t, a, "pkg/auth/members/azure.go")
		if !strings.Contains(google, "google.AnyWorkspace()") {
			t.Error("pkg/auth/members/google.go does not register google.AnyWorkspace(): the method is not open to any Google Workspace alone")
		}
		if !strings.Contains(azure, "azure.AnyTenant()") {
			t.Error("pkg/auth/members/azure.go does not register azure.AnyTenant(): the method is not open to any Entra tenant alone")
		}
		for file, source := range map[string]string{"google.go": google, "azure.go": azure} {
			if strings.Contains(source, "SyncsRoles") || strings.Contains(source, "WithRoleSync") {
				t.Errorf("pkg/auth/members/%s has the directory give the roles; the application gives them in this shape", file)
			}
		}
	})

	t.Run("the generator program declares the methods in the password's place", func(t *testing.T) {
		t.Parallel()

		program := read(t, a, "cmd/generate/resourcegenerator/generator.go")
		if want := `generation.Auth("example.com/acme/depot/pkg/auth/members", generation.GoogleMethod("google"), generation.AzureMethod("azure"), generation.PersonaMethod("persona")),`; !strings.Contains(program, want) {
			t.Errorf("the generator program does not declare %s", want)
		}
		if strings.Contains(program, "PasswordMethod") {
			t.Error("the generator program still declares generation.PasswordMethod(), so the router mounts the password's routes and the management serves a password's methods")
		}
	})

	t.Run("the data level reads and passes each registration's settings", func(t *testing.T) {
		t.Parallel()

		data := read(t, a, "pkg/config/data.go")
		for _, want := range []string{
			`env:"APP_MEMBERS_GOOGLE_CLIENT_ID"`, `env:"APP_MEMBERS_AZURE_CLIENT_ID"`, `env:"APP_MEMBERS_PERSONA_ISSUER"`,
			"Google: members.GoogleSettings{", "Azure: members.AzureSettings{", "Persona: members.PersonaSettings{", `LoginURL:       "/login",`,
		} {
			if !strings.Contains(data, want) {
				t.Errorf("pkg/config/data.go does not contain %s", want)
			}
		}
		if got := skippedLine(ch, "pkg/config/data.go: LoginURL is set to /login"); len(got) != 1 {
			t.Errorf("the change says %d times that LoginURL was set, want once: %q", len(got), got)
		}
	})

	t.Run("the bootstrap seeds no password and seeds the cast", func(t *testing.T) {
		t.Parallel()

		if got, want := read(t, a, "cmd/bootstrap/users.json"), "{\n  \"users\": []\n}\n"; got != want {
			t.Errorf("cmd/bootstrap/users.json = %q, want %q: an account made ahead with a password is what it held", got, want)
		}
		program := read(t, a, "cmd/bootstrap/main.go")
		if strings.Contains(program, "Password") {
			t.Error("cmd/bootstrap/main.go still names a Password")
		}
		seeded, cast := strings.Index(program, "seedIdentities(ctx, data); err != nil"), strings.Index(program, "data.Members().SeedCast(ctx, os.Stdout)")
		if cast < 0 || seeded < 0 || cast < seeded {
			t.Errorf("cmd/bootstrap/main.go does not seed the cast after the development logins (seedIdentities at %d, SeedCast at %d)", seeded, cast)
		}
		if _, err := parser.ParseFile(token.NewFileSet(), a.Abs("cmd/bootstrap/main.go"), nil, parser.SkipObjectResolution); err != nil {
			t.Errorf("cmd/bootstrap/main.go does not parse: %v", err)
		}
	})

	t.Run("the development administrator is the cast's persona", func(t *testing.T) {
		t.Parallel()

		var cast struct {
			Personas []struct {
				Login string   `json:"login"`
				Roles []string `json:"roles"`
			} `json:"personas"`
		}
		if err := json.Unmarshal([]byte(read(t, a, "pkg/auth/members/cast.json")), &cast); err != nil {
			t.Fatalf("pkg/auth/members/cast.json is not JSON: %v", err)
		}
		if len(cast.Personas) != 1 || cast.Personas[0].Login != DevelopmentPersona {
			t.Fatalf("the cast holds %+v, want the one persona %s", cast.Personas, DevelopmentPersona)
		}
		if want := []string{"SuperAdministrator", "Administrator", "FeatureAdministrator"}; !slices.Equal(cast.Personas[0].Roles, want) {
			t.Errorf("the development persona holds %v, want %v", cast.Personas[0].Roles, want)
		}
		env := read(t, a, ".envrc.template")
		for _, want := range []string{"export APP_PERSONA_USERNAME=admin\n", "export APP_MEMBERS_PERSONA_ISSUER=", "export APP_MEMBERS_GOOGLE_REDIRECT_URL=http://127.0.0.1:4300/api/user/google/callback\n", "export APP_MEMBERS_AZURE_REDIRECT_URL=http://127.0.0.1:4300/api/user/azure/callback\n"} {
			if !strings.Contains(env, want) {
				t.Errorf(".envrc.template does not contain %q", want)
			}
		}
		if !strings.Contains(read(t, a, "Procfile"), "go run -tags skipAuth .") {
			t.Error("the Procfile does not run the server under -tags skipAuth, so no development sign-in is simulated")
		}
	})

	t.Run("the first administrator is still invited", func(t *testing.T) {
		t.Parallel()

		deploy := read(t, a, deployFile)
		if !strings.Contains(deploy, "\treturn []string{members.Name}\n") {
			t.Errorf("%s no longer names the members auth among the auths that invite a first administrator; the application gives the roles, so its first administrator is invited", deployFile)
		}
	})

	t.Run("the role file grants nothing on what is no longer served", func(t *testing.T) {
		t.Parallel()

		resources := granted(t, a, "pkg/auth/members/roles.json")
		for _, gone := range []string{"RequireMembersPasswordChange", "RemoveMembersPassword", "SendMembersUserPasswordResetLink", "SetMembersSelfServiceReset"} {
			if slices.Contains(resources, gone) {
				t.Errorf("the role file still grants %s, which the application no longer serves: the permission engine refuses the file at start", gone)
			}
		}
		for _, kept := range []string{"InviteMembersUser", "AssignMembersRole", "MembersSessionUsers", "FeatureFlags"} {
			if !slices.Contains(resources, kept) {
				t.Errorf("the role file lost its grant on %s, which the application still serves", kept)
			}
		}
		if roles := read(t, a, "pkg/auth/members/roles.json"); strings.Contains(roles, "mails password reset links") {
			t.Error("the role file's description still says the role mails password reset links")
		}
	})

	t.Run("each test the start breaks is named with what becomes of it", func(t *testing.T) {
		t.Parallel()

		for _, suite := range passwordSuites {
			if suite.fate == "" && suite.application == "" {
				if named := leftLine(ch, suite.file+" "); len(named) != 0 {
					t.Errorf("%s is named, and it holds in an application that gives the roles: %q", suite.file, named)
				}

				continue
			}
			if named := leftLine(ch, suite.file+" ", suite.fate, suite.application); len(named) != 1 {
				t.Errorf("%s is named %d times among what the change leaves, with what becomes of it; want once", suite.file, len(named))
			}
		}
		if got := leftLine(ch, "", "{auth}"); len(got) != 0 {
			t.Errorf("the change names the auth by its placeholder: %q", got)
		}
		if !strings.Contains(ch.LeftNote, "the members auth has none now") {
			t.Errorf("the change does not say what the tests it leaves are: %q", ch.LeftNote)
		}
	})
}

// TestDirectoryStart_Apply_directoryRoles holds what a new application that starts from
// Directory roles is left with: an auth whose one method's directory gives every role,
// with no foothold, nobody invited and nothing seeded.
func TestDirectoryStart_Apply_directoryRoles(t *testing.T) {
	t.Parallel()

	start := directoryRolesStart(app.MethodGoogle, "fleet.example")
	a, ch := started(t, &start, "send_staff_user_password_reset_link", "assign_staff_role", "remove_staff_role", "invite_staff_user", "withdraw_staff_invitation")

	t.Run("the auth registers the one method, whose directory gives the roles", func(t *testing.T) {
		t.Parallel()

		if len(a.Auths) != 1 || len(a.Auths[0].Methods) != 1 {
			t.Fatalf("the application's auths register %+v, want one method", a.Auths)
		}
		if m := a.Auths[0].Methods[0]; m.Kind != app.MethodGoogle || m.Name != "google" || m.Authority != app.AuthorityDirectory {
			t.Errorf("the staff auth registers %s:%s with the %s's roles, want google:google with the directory's", m.Kind, m.Name, m.Authority)
		}
		for _, gone := range []string{"password.go", "foothold.go", "cast.json", "cast.go"} {
			if exists(a, "pkg/auth/staff/"+gone) {
				t.Errorf("pkg/auth/staff/%s is there, and an auth whose directory gives every role has none", gone)
			}
		}
		for _, file := range []string{"staff.go", "google.go"} {
			if _, err := parser.ParseFile(token.NewFileSet(), a.Abs("pkg/auth/staff/"+file), nil, parser.SkipObjectResolution); err != nil {
				t.Errorf("pkg/auth/staff/%s does not parse: %v", file, err)
			}
		}
		if program := read(t, a, "cmd/generate/resourcegenerator/generator.go"); !strings.Contains(program, `generation.GoogleMethod("google").SyncsRoles()),`) || strings.Contains(program, "PasswordMethod") {
			t.Error("the generator program does not declare the google method, synchronizing roles, in the password's place")
		}
	})

	t.Run("nothing in the application invites a first administrator", func(t *testing.T) {
		t.Parallel()

		taken := map[string][]string{
			deployFile:      {"Administrator", "staff.", `"example.com/acme/depot/pkg/auth/staff"`, `"time"`, "/session\""},
			migrateCommand:  {"reinvite", "InviteAdministrators", "first administrator", `"slices"`, `"strings"`, "invitation"},
			baseDataLevel:   {"StaffAdministrator", "APP_STAFF_ADMINISTRATOR", "first administrator", "The log is no mailbox", "invite its first"},
			baseEnvTemplate: {"APP_STAFF_ADMINISTRATOR", "first administrator"},
		}
		for rel, gone := range taken {
			text := read(t, a, rel)
			for _, one := range gone {
				if strings.Contains(text, one) {
					t.Errorf("%s still contains %q", rel, one)
				}
			}
			if !strings.HasSuffix(rel, ".go") {
				continue
			}
			if _, err := parser.ParseFile(token.NewFileSet(), rel, text, parser.SkipObjectResolution); err != nil {
				t.Errorf("%s does not parse: %v", rel, err)
			}
			if formatted, err := format.Source([]byte(text)); err != nil || string(formatted) != text {
				t.Errorf("%s is not as gofmt writes it (error %v)", rel, err)
			}
		}
		if exists(a, deployAdministratorsTest) {
			t.Errorf("%s is still there, and what it tests was taken out", deployAdministratorsTest)
		}
		for rel, kept := range map[string][]string{
			deployFile: {
				"// Package deploy holds the database steps a deployment runs and the development\n// bootstrap reuses: applying the schema migrations, checking the release's role policy\n// against what the permission engine's store holds, and bringing the feature\n// flags to the release's declarations.\npackage deploy\n",
				"func CheckRoles(", "func MigrateFeatures(", "func RetireSecret(",
				"import (\n\t\"context\"\n\t\"fmt\"\n\t\"io\"\n\t\"path/filepath\"\n\t\"strings\"\n\n\t\"example.com/acme/depot/pkg/config\"\n\t\"example.com/acme/depot/pkg/resources\"\n\t\"github.com/go-playground/errors/v5\"\n\t\"github.com/impulseframework/impulse/access\"\n\tinitiator \"github.com/impulseframework/impulse/db\"\n\t\"github.com/impulseframework/impulse/resource\"\n)\n",
			},
			migrateCommand: {
				// The application's own imports sort before the libraries' under this module
				// path, and stay: what is taken out of the block is the standard library's.
				"import (\n\t\"context\"\n\t\"flag\"\n\t\"fmt\"\n\t\"io\"\n\t\"os\"\n\t\"os/signal\"\n\t\"strconv\"\n\t\"time\"\n\n\t\"example.com/acme/depot/pkg/auth/staff\"\n\t\"example.com/acme/depot/pkg/config\"\n\t\"example.com/acme/depot/pkg/deploy\"\n\t\"github.com/go-playground/errors/v5\"\n)\n",
				"// Package main is the deployment's migration step: it applies the schema migrations and\n// checks the release's role policy against what the permission engine's store holds. It\n// reads the core and data configuration levels and nothing above them.\n",
				"// being retired among them. It applies no migration and goes with no other flag.\npackage main\n",
				"so -seed does not go with it; a retirement goes\n// alone.\nfunc parse(",
				"// run does what the flags ask: the report, the force, the retirement, or the\n// migrations.\nfunc run(",
				"\tcase o.retireSecret != \"\":\n\t\treturn retireSecret(ctx, settings, o.retireSecret, out)\n\t}\n\n\treturn migrate(ctx, settings, o.seed)\n}\n",
				"// migrate is the deployment's migration step itself.\nfunc migrate(ctx context.Context, settings config.DatabaseSettings, seed bool) error {\n",
				"\t\treturn errors.Wrap(err, \"deploy.CheckRoles()\")\n\t}\n\n\t// The feature flags the release declares",
				"deploy.CheckRoles(ctx, data.Staff().Access(), staff.Name)",
			},
			baseDataLevel: {
				"MailAppPassword string `env:\"APP_MAIL_APP_PASSWORD\" secret:\"true\"`\n", "StaffGoogleHostedDomain", "func (c *DataConfiguration) Staff() *staff.Auth {",
				"// relay takes over once the version is pinned; a release to any other environment is\n// refused until it is.\nfunc openMailSender(",
			},
		} {
			text := read(t, a, rel)
			for _, one := range kept {
				if !strings.Contains(text, one) {
					t.Errorf("%s does not contain %q:\n%s", rel, one, text)
				}
			}
		}
		files := strings.Join([]string{deployFile, migrateCommand, baseDataLevel, deployAdministratorsTest, baseEnvTemplate}, ", ")
		if got := linesWith(ch.Did, files+": what invites a first administrator was taken out, since the staff auth's directory gives every role", []string{
			"AdministratorAuths, InviteAdministrators and ReinviteAdministrator with what they stand on",
			"the migrate command's invitation after the role check and its -reinvite-administrator flag",
			"the data level's address setting (APP_STAFF_ADMINISTRATOR) with its accessor, and what its comment on the mail sender says of an invitation",
			"the test of what the deployment writes about an invitation",
			"the environment template's variable",
			"impulse add auth writes the invitation for an auth added later that invites a first administrator",
		}); len(got) != 1 {
			t.Errorf("the change does not say once what it took out and why: %q", ch.Did)
		}
		if got := linesWith(ch.Skipped, "", []string{"what invites a first administrator"}); len(got) != 0 {
			t.Errorf("the change hands what invites a first administrator on to the author, and it was taken out: %q", got)
		}
	})

	t.Run("the development person holds the roles the simulated directory asserts", func(t *testing.T) {
		t.Parallel()

		env := read(t, a, ".envrc.template")
		for _, want := range []string{"export APP_USERNAME=staff-dev\n", "export APP_ROLES=SuperAdministrator,Administrator,FeatureAdministrator\n", "export APP_STAFF_GOOGLE_HOSTED_DOMAIN=fleet.example\n"} {
			if !strings.Contains(env, want) {
				t.Errorf(".envrc.template does not contain %q", want)
			}
		}
		if strings.Contains(env, "APP_PERSONA_USERNAME") {
			t.Error(".envrc.template names a persona, and the persona method cannot stand beside a method whose directory gives the roles")
		}
		if got, want := read(t, a, "cmd/bootstrap/users.json"), "{\n  \"users\": []\n}\n"; got != want {
			t.Errorf("cmd/bootstrap/users.json = %q, want %q: nothing is seeded for people a directory gives every role", got, want)
		}
		if program := read(t, a, "cmd/bootstrap/main.go"); strings.Contains(program, "SeedCast") || strings.Contains(program, "Password") {
			t.Error("cmd/bootstrap/main.go seeds a cast or names a Password")
		}
	})

	t.Run("the role file grants nothing on what is no longer served", func(t *testing.T) {
		t.Parallel()

		resources := granted(t, a, "pkg/auth/staff/roles.json")
		for _, gone := range []string{"SendStaffUserPasswordResetLink", "AssignStaffRole", "RemoveStaffRole", "InviteStaffUser", "WithdrawStaffInvitation"} {
			if slices.Contains(resources, gone) {
				t.Errorf("the role file still grants %s, which the application no longer serves", gone)
			}
		}
		if !slices.Contains(resources, "StaffSessionUsers") {
			t.Error("the role file lost its grant on StaffSessionUsers, which the application still serves")
		}
	})
}

// TestDirectoryStart_Apply_directoryRolesMicrosoft holds the one organization of a
// Microsoft method: an Entra tenant, written by its tenant id.
func TestDirectoryStart_Apply_directoryRolesMicrosoft(t *testing.T) {
	t.Parallel()

	const tenant = "3f2504e0-4f89-11d3-9a0c-0305e82c3301"
	start := directoryRolesStart(app.MethodAzure, tenant)
	a, _ := started(t, &start)

	if m := a.Auths[0].Methods[0]; len(a.Auths[0].Methods) != 1 || m.Kind != app.MethodAzure || m.Authority != app.AuthorityDirectory {
		t.Errorf("the staff auth registers %+v, want the one azure method with the directory's roles", a.Auths[0].Methods)
	}
	if env, want := read(t, a, ".envrc.template"), "export APP_STAFF_AZURE_TENANT_ID="+tenant+"\n"; !strings.Contains(env, want) {
		t.Errorf(".envrc.template does not contain %q", want)
	}
	if exists(a, "pkg/auth/staff/foothold.go") || !exists(a, "pkg/auth/staff/azure.go") {
		t.Error("the staff auth keeps its foothold or has no azure.go")
	}
}

// TestDirectoryStart_Apply_directoryRolesProvider holds the one organization of another
// identity provider's method: the provider, written by its issuer as the development
// value of the method's issuer setting, in the placeholder's place.
func TestDirectoryStart_Apply_directoryRolesProvider(t *testing.T) {
	t.Parallel()

	start := providerStart(providerIssuer)
	a, ch := started(t, &start)

	if m := a.Auths[0].Methods[0]; len(a.Auths[0].Methods) != 1 || m.Kind != app.MethodOIDC || m.Name != "okta" || m.Authority != app.AuthorityDirectory {
		t.Errorf("the staff auth registers %+v, want the one okta method, an identity provider's, with the directory's roles", a.Auths[0].Methods)
	}
	env := read(t, a, ".envrc.template")
	if want := "export APP_STAFF_OKTA_ISSUER=" + providerIssuer + "\n"; !strings.Contains(env, want) {
		t.Errorf(".envrc.template does not contain %q", want)
	}
	if strings.Contains(env, placeholderProviderIssuer) {
		t.Errorf(".envrc.template keeps the placeholder issuer %s, and the start named the provider's", placeholderProviderIssuer)
	}
	if got := linesWith(ch.Did, ".envrc.template: APP_STAFF_OKTA_ISSUER="+providerIssuer, []string{"the one organization whose directory gives the roles"}); len(got) != 1 {
		t.Errorf("the change does not say once that the issuer was written as the development value: %q", ch.Did)
	}
	if exists(a, "pkg/auth/staff/foothold.go") || !exists(a, "pkg/auth/staff/okta.go") {
		t.Error("the staff auth keeps its foothold or has no okta.go")
	}
}

// TestDirectoryStart_Apply_noHandlerRemoved holds the change to saying so when the
// regeneration left the auth's generated management as it was: the role file is then
// untouched, and the change names the consequence.
func TestDirectoryStart_Apply_noHandlerRemoved(t *testing.T) {
	t.Parallel()

	start := byDirectoryStart("members")
	base := newBase(t, "members")
	before := read(t, base, "pkg/auth/members/roles.json")
	ch, err := start.Apply(context.Background(), base, &fakeExec{})
	if err != nil {
		t.Fatalf("DirectoryStart.Apply() error = %v", err)
	}
	if after := read(t, base, "pkg/auth/members/roles.json"); after != before {
		t.Error("the role file changed, and the regeneration removed no handler")
	}
	if got := skippedLine(ch, "pkg/auth/members/roles.json: the regeneration removed no handler"); len(got) != 1 {
		t.Errorf("the change does not say that no grant was taken out: %q", ch.Skipped)
	}
}
