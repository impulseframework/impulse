package check

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton"
)

const (
	// directoriesRecord is the directories record, annotated @tenantDirectory (line 9),
	// with its default seat column.
	directoriesRecord = `package resources

type (
	// TenantDirectory is a directory a tenant admits its people through.
	//
	// @resource
	// @permissionScope(domain)
	// @tenantDirectory
	TenantDirectory struct {
		ID string ` + "`spanner:\"Id\"`" + `
		// @domain
		TenantID string ` + "`spanner:\"TenantId\"`" + `
		// @directoryMethod
		Method string ` + "`spanner:\"Method\"`" + `
		// @directoryConnection
		Connection string ` + "`spanner:\"Connection\"`" + `
		// @directoryDefaultSeat
		DefaultSeat *string ` + "`spanner:\"DefaultSeat\"`" + `
	}
)
`
	// admittingAuth is an auth package whose roles the application assigns (the
	// password method and a Google registration), handing its role file to the engine
	// and setting the directory admission over the reader its Settings carry; the
	// role-synchronization slot is left to fill in.
	admittingAuth = `package staff

import (
	"context"
	_ "embed"

	cloudspanner "cloud.google.com/go/spanner"
	"example.com/harbor/pkg/identity/staffidentity"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/google"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/session/signin"
)

const Name = "staff"

//go:embed roles.json
var roleFile access.RoleFile

// Roles is the release's default roles for the staff auth.
func Roles() access.RoleFile { return roleFile }

type Session = session.Auth[session.NoCustomData, session.NoCustomData, *cloudspanner.ReadWriteTransaction]

// Settings carries the generated directory reader.
type Settings struct {
	TenantDirectories signin.DirectoryReader[*cloudspanner.ReadWriteTransaction]
}

type Auth struct {
	session *Session
}

func New(ctx context.Context, db *cloudspanner.Client, key string, collection access.PermissionCollection, settings *Settings) (*Auth, error) {
	engine, err := access.New(nil, access.WithDefaultRoles(collection, Roles()))
	if err != nil {
		return nil, err
	}
	s, err := session.NewAuth[session.NoCustomData, session.NoCustomData](sessionstorage.NewSpannerAuth(db), key, session.SuperAdministratorsBy(signin.SuperAdministrators(resource.SignInRoles(engine.UserManager()))))
	if err != nil {
		return nil, err
	}
	session.PasswordSignIn(s)
	google.SignIn(s, "google", nil, &google.Config{RoleSync: %s})
	identityClient, err := staffidentity.NewClient(engine, s.API())
	if err != nil {
		return nil, err
	}
	s.SetDirectoryAdmission(settings.TenantDirectories, identityClient.DirectorySeated())
	s.SetInvitationAccepted(identityClient.InvitationAccepted())

	return &Auth{session: s}, nil
}

func (a *Auth) Session() *Session { return a.session }
`
	admissionCall = "\ts.SetDirectoryAdmission(settings.TenantDirectories, identityClient.DirectorySeated())\n"
	// seatRoles grants the tenant's administrator the two methods that say how a tenant
	// admits its directory's people; noSeatRoles grants the one that makes it invite only
	// and not the one that opens it.
	seatRoles   = `{"roles": {"global": [], "domain": [{"name": "TenantAdministrator", "permissions": {"Create": [{"resource": "TenantDirectories"}], "Execute": [{"resource": "OpenTenantToDirectory"}, {"resource": "MakeTenantInviteOnly"}]}}]}}`
	noSeatRoles = `{"roles": {"global": [], "domain": [{"name": "TenantAdministrator", "permissions": {"Create": [{"resource": "TenantDirectories"}], "Execute": [{"resource": "MakeTenantInviteOnly"}]}}]}}`
)

// admitted lays out a flat tenanted application with the skeletons' wiring: the tenant
// record and its directories record, and the staff auth assigning its roles.
func admitted() map[string]string {
	return map[string]string{
		"cmd/generate/main.go": program("pkg/resources",
			`generation.GenerateHandlers("app"),`,
			`generation.GenerateRoutes("pkg/router", "api"),`,
		),
		"pkg/resources/tenants.go":            tenantRecord,
		"pkg/resources/tenant_directories.go": directoriesRecord,
		"pkg/auth/staff/staff.go":             fmt.Sprintf(admittingAuth, "google.DisableRoleSync()"),
		"pkg/auth/staff/roles.json":           seatRoles,
	}
}

// The findings the check writes, by the piece missing.
const (
	noSeatColumn = "TenantDirectory declares no column annotated @directoryDefaultSeat; the tenant directory record carries a nullable DefaultSeat column (STRING(64), annotated @directoryDefaultSeat; a migration adds it to the directories table), what a stranger the directory vouches for is seated with at their first sign-in, named by the tenant's administrator on the Sign-in page"
	noAdmission  = "nothing in the %s auth calls SetDirectoryAdmission, and the application assigns its roles (no method is registered with RoleSync); the auth carries the generated directory reader in its Settings (TenantDirectories, NewTenantDirectoryReader) and sets SetDirectoryAdmission(settings.TenantDirectories, identityClient.DirectorySeated()) beside its sign-in policy, with the hook of the identity client it builds from its generated management, so a stranger from a declared directory is created holding the row's role in one transaction or refused by invitation only (invitation_only) with no account, before the registration's account rule"
	noSeatGrant  = "grants Execute on %s; the tenant's administrator role (a domain role) holds it, beside the other method that says how a tenant admits its directory's people, so the tenant's administrator opens the tenant to its directory and makes it invite only again on the Sign-in page; the record's own update never writes the default seat"
	// seatGranted is the summary's part for the two grants, in a role file.
	seatGranted = "Execute on OpenTenantToDirectory granted in %[1]s, Execute on MakeTenantInviteOnly granted in %[1]s"
)

func TestDirectoryAdmission(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		edit        func(files map[string]string)
		wantStatus  Status
		wantSummary string
		wantDetails []string
	}{
		{
			name:        "the skeletons' wiring",
			wantStatus:  Pass,
			wantSummary: "directories record TenantDirectory (default seat DefaultSeat); admission set by staff; " + fmt.Sprintf(seatGranted, "pkg/auth/staff/roles.json"),
		},
		{
			name: "a record without the default seat column",
			edit: func(files map[string]string) {
				files["pkg/resources/tenant_directories.go"] = strings.Replace(directoriesRecord, "\t\t// @directoryDefaultSeat\n", "", 1)
			},
			wantStatus:  Fail,
			wantSummary: "1 directory admission wiring problem(s)",
			wantDetails: []string{"pkg/resources/tenant_directories.go:9: " + noSeatColumn},
		},
		{
			name: "an auth that never sets the admission",
			edit: func(files map[string]string) {
				files["pkg/auth/staff/staff.go"] = strings.Replace(files["pkg/auth/staff/staff.go"], admissionCall, "", 1)
			},
			wantStatus:  Fail,
			wantSummary: "1 directory admission wiring problem(s)",
			wantDetails: []string{"pkg/auth/staff: " + fmt.Sprintf(noAdmission, "staff")},
		},
		{
			name: "an auth whose directory writes the roles sets none",
			edit: func(files map[string]string) {
				files["pkg/auth/staff/staff.go"] = strings.Replace(fmt.Sprintf(admittingAuth, "google.RoleSync()"), admissionCall, "", 1)
			},
			wantStatus:  Pass,
			wantSummary: "directories record TenantDirectory (default seat DefaultSeat); admission set by no auth (every auth's roles are its directory's); " + fmt.Sprintf(seatGranted, "pkg/auth/staff/roles.json"),
		},
		{
			name: "no role grants the method that opens a tenant to its directory",
			edit: func(files map[string]string) {
				files["pkg/auth/staff/roles.json"] = noSeatRoles
			},
			wantStatus:  Fail,
			wantSummary: "1 directory admission wiring problem(s)",
			wantDetails: []string{"pkg/resources/tenant_directories.go:9: no role in pkg/auth/staff/roles.json " + fmt.Sprintf(noSeatGrant, "OpenTenantToDirectory")},
		},
		{
			name: "a grant of Update on the default seat is no grant of the methods",
			edit: func(files map[string]string) {
				files["pkg/auth/staff/roles.json"] = `{"roles": {"global": [], "domain": [{"name": "TenantAdministrator", "permissions": {"Update": [{"resource": "TenantDirectories", "fields": ["defaultSeat"]}]}}]}}`
			},
			wantStatus:  Fail,
			wantSummary: "2 directory admission wiring problem(s)",
			wantDetails: []string{
				"pkg/resources/tenant_directories.go:9: no role in pkg/auth/staff/roles.json " + fmt.Sprintf(noSeatGrant, "OpenTenantToDirectory"),
				"pkg/resources/tenant_directories.go:9: no role in pkg/auth/staff/roles.json " + fmt.Sprintf(noSeatGrant, "MakeTenantInviteOnly"),
			},
		},
		{
			name: "no role file reaches the engine",
			edit: func(files map[string]string) {
				files["pkg/auth/staff/staff.go"] = strings.Replace(files["pkg/auth/staff/staff.go"], "access.WithDefaultRoles(collection, Roles())", "nil", 1)
			},
			wantStatus:  Fail,
			wantSummary: "2 directory admission wiring problem(s)",
			wantDetails: []string{
				"pkg/resources/tenant_directories.go:9: no role file reaches access.WithDefaultRoles (see auths-wired), so no role " + fmt.Sprintf(noSeatGrant, "OpenTenantToDirectory"),
				"pkg/resources/tenant_directories.go:9: no role file reaches access.WithDefaultRoles (see auths-wired), so no role " + fmt.Sprintf(noSeatGrant, "MakeTenantInviteOnly"),
			},
		},
		{
			name: "a role file that cannot be read",
			edit: func(files map[string]string) {
				files["pkg/auth/staff/roles.json"] = "{"
			},
			wantStatus:  Fail,
			wantSummary: "3 directory admission wiring problem(s)",
			wantDetails: []string{
				"pkg/auth/staff/roles.json: cannot be read: unexpected end of JSON input",
				"pkg/resources/tenant_directories.go:9: no role in pkg/auth/staff/roles.json " + fmt.Sprintf(noSeatGrant, "OpenTenantToDirectory"),
				"pkg/resources/tenant_directories.go:9: no role in pkg/auth/staff/roles.json " + fmt.Sprintf(noSeatGrant, "MakeTenantInviteOnly"),
			},
		},
		{
			name: "a plural the program overrides",
			edit: func(files map[string]string) {
				files["cmd/generate/main.go"] = program("pkg/resources",
					`generation.GenerateHandlers("app"),`,
					`generation.GenerateRoutes("pkg/router", "api"),`,
					`generation.WithPluralOverrides(map[string]string{"TenantDirectory": "DirectoryDeclarations"}),`,
				)
				files["pkg/auth/staff/roles.json"] = strings.ReplaceAll(seatRoles, "TenantDirectories", "DirectoryDeclarations")
			},
			// The methods carry the tenant record's name, whatever the directories
			// record's resource is called.
			wantStatus:  Pass,
			wantSummary: "directories record TenantDirectory (default seat DefaultSeat); admission set by staff; " + fmt.Sprintf(seatGranted, "pkg/auth/staff/roles.json"),
		},
		{
			name: "a seat column named on the wire by its json tag",
			edit: func(files map[string]string) {
				files["pkg/resources/tenant_directories.go"] = strings.Replace(directoriesRecord, "`spanner:\"DefaultSeat\"`", "`json:\"seat\" spanner:\"DefaultSeat\"`", 1)
			},
			wantStatus:  Pass,
			wantSummary: "directories record TenantDirectory (default seat DefaultSeat); admission set by staff; " + fmt.Sprintf(seatGranted, "pkg/auth/staff/roles.json"),
		},
		{
			name: "a tenant record declaring no directories",
			edit: func(files map[string]string) {
				delete(files, "pkg/resources/tenant_directories.go")
			},
			wantStatus:  Skip,
			wantSummary: "no tenant record declares directories",
		},
		{
			name: "no site generator",
			edit: func(files map[string]string) {
				files["cmd/generate/main.go"] = program("pkg/sharedresources", `generation.GenerateTypescript("web/src"),`)
			},
			wantStatus:  Skip,
			wantSummary: "no site generator",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			files := admitted()
			if tt.edit != nil {
				tt.edit(files)
			}
			root := t.TempDir()
			write := func(rel, content string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			write("go.mod", "module example.com/harbor\n\ngo 1.26.6\n")
			for rel, content := range files {
				write(rel, content)
			}

			a, err := app.Discover(root)
			if err != nil {
				t.Fatalf("app.Discover() error = %v", err)
			}
			got := directoryAdmission{}.Run(context.Background(), &Env{App: a})
			want := Result{Name: directoryAdmission{}.Name(), Status: tt.wantStatus, Summary: tt.wantSummary, Details: tt.wantDetails}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("Run() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// candidateEdit is one change to a rendered candidate: the text of a file replaced
// once, which the file must carry.
type candidateEdit struct {
	file, old, new string
}

// renderCandidate renders a skeleton candidate into a temporary directory, applies the
// edits, and discovers the application.
func renderCandidate(t *testing.T, candidate string, edits ...candidateEdit) *app.App {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "beacon")
	if _, err := skeleton.Render(&skeleton.Options{Version: "v0.1.0", WebVersion: "0.1.0", Candidate: candidate, Dir: dir, ModulePath: "example.com/acme/beacon"}); err != nil {
		t.Fatalf("skeleton.Render() error = %v", err)
	}
	for _, e := range edits {
		p := filepath.Join(dir, filepath.FromSlash(e.file))
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), e.old) {
			t.Fatalf("%s of the %s candidate does not carry %q", e.file, candidate, e.old)
		}
		if err := os.WriteFile(p, []byte(strings.Replace(string(data), e.old, e.new, 1)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	a, err := app.Discover(dir)
	if err != nil {
		t.Fatalf("app.Discover() error = %v", err)
	}

	return a
}

// TestDirectoryAdmissionCandidates holds the skeleton candidates to the check: the
// tenanted shapes carry the wiring, the solo shape declares no directories, and a
// candidate with a piece removed fails naming it.
func TestDirectoryAdmissionCandidates(t *testing.T) {
	t.Parallel()

	const (
		candidateCall = "\tauthSession.SetDirectoryAdmission(settings.TenantDirectories, identityClient.DirectorySeated())\n"
		// The grant of the method that opens a tenant to its directory, in the
		// administrator role's Execute list.
		candidateGrant = "            {\n              \"resource\": \"OpenTenantToDirectory\"\n            },\n"
	)

	tests := []struct {
		name        string
		candidate   string
		edits       []candidateEdit
		wantStatus  Status
		wantSummary string
		wantDetails []string
	}{
		{
			name: "solo", candidate: "solo",
			wantStatus: Skip, wantSummary: "no tenant record declares directories",
		},
		{
			name: "tenanted", candidate: "tenanted",
			wantStatus: Pass, wantSummary: "directories record TenantDirectory (default seat DefaultSeat); admission set by staff; " + fmt.Sprintf(seatGranted, "pkg/auth/staff/roles.json"),
		},
		{
			name: "outlets", candidate: "outlets",
			wantStatus: Pass, wantSummary: "directories record TenantDirectory (default seat DefaultSeat); admission set by members, staff; " + fmt.Sprintf(seatGranted, "pkg/auth/staff/roles.json"),
		},
		{
			name: "sites", candidate: "sites",
			wantStatus: Pass, wantSummary: "directories record TenantDirectory (default seat DefaultSeat); admission set by staff; " + fmt.Sprintf(seatGranted, "pkg/auth/staff/roles.json"),
		},
		{
			name: "tenanted without the default seat column", candidate: "tenanted",
			edits:      []candidateEdit{{file: "pkg/resources/tenant_directories.go", old: "\t\t// @directoryDefaultSeat\n"}},
			wantStatus: Fail, wantSummary: "1 directory admission wiring problem(s)",
			wantDetails: []string{"pkg/resources/tenant_directories.go:49: " + noSeatColumn},
		},
		{
			name: "outlets without the members admission names the members auth", candidate: "outlets",
			edits:      []candidateEdit{{file: "pkg/auth/members/members.go", old: candidateCall}},
			wantStatus: Fail, wantSummary: "1 directory admission wiring problem(s)",
			wantDetails: []string{"pkg/auth/members: " + fmt.Sprintf(noAdmission, "members")},
		},
		{
			name: "tenanted without the grant of the method that opens a tenant to its directory", candidate: "tenanted",
			edits:      []candidateEdit{{file: "pkg/auth/staff/roles.json", old: candidateGrant}},
			wantStatus: Fail, wantSummary: "1 directory admission wiring problem(s)",
			wantDetails: []string{"pkg/resources/tenant_directories.go:49: no role in pkg/auth/staff/roles.json " + fmt.Sprintf(noSeatGrant, "OpenTenantToDirectory")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := renderCandidate(t, tt.candidate, tt.edits...)
			got := directoryAdmission{}.Run(context.Background(), &Env{App: a})
			want := Result{Name: directoryAdmission{}.Name(), Status: tt.wantStatus, Summary: tt.wantSummary, Details: tt.wantDetails}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("Run() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestDirectoryAdmissionMeaning(t *testing.T) {
	t.Parallel()

	meaning := directoryAdmission{}.Meaning()
	for _, want := range []string{"`@tenantDirectory`", "`@directoryDefaultSeat`", "`authSession.SetDirectoryAdmission(settings.TenantDirectories, identityClient.DirectorySeated())`", "`New<Record>DirectoryReader`", "`Open<Record>ToDirectory`", "`Make<Record>InviteOnly`", "its own update never writes it"} {
		if !strings.Contains(meaning, want) {
			t.Errorf("Meaning() lacks %q", want)
		}
	}
}
