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
)

const (
	// plainFoothold is the foothold impulse add auth writes for an auth it names no
	// registration for: the first administrator seeded with no password, under an id
	// that says nothing of where its name is believed from. SeedAdministrator is
	// declared on line 9.
	plainFoothold = `package staff

import (
	"context"

	"github.com/impulseframework/impulse/session"
)

func (a *Auth) SeedAdministrator(ctx context.Context, signInName string) (bool, error) {
	return true, a.seedFoothold(ctx, &session.CreateUserRequest{Username: signInName})
}

func (a *Auth) seedFoothold(ctx context.Context, req *session.CreateUserRequest) error {
	id := session.SeedUserID(Name, req.Username)
	req.ID = &id

	return nil
}
`
	seedPlain = "id := session.SeedUserID(Name, req.Username)"
	// The directory registration the fixture's auth signs its people in through, in the
	// forms the rule tells apart.
	googleRegistration = `google.SignIn(s, "google", nil, &google.Config{RoleSync: google.DisableRoleSync()})`
	passwordMethod     = "\tsession.PasswordSignIn(s)\n"
)

// cannotSignIn is the finding for a first administrator seeded for no registration.
const cannotSignIn = "pkg/auth/staff/foothold.go:9: the first administrator the migrate command seeds for the staff auth cannot sign in: the application's tenants have directories, where a name alone links no account that has never signed in, and the auth registers no password method and seeds its administrator for no registration (session.SeedUserID); give the auth a password method for its first administrator (impulse add method staff password), or name the registration that is the deployment's own directory in seedFoothold: derive the id with session.SeedUserIDThrough(Name, the registration's constant, req.Username), which the sign-in rule links at that person's first sign-in through the registration while it admits one organization alone"

// TestFirstAdministrator pins the rule on the first administrator an auth seeds where
// the application's tenants have directories. The refusal is the case the rule exists
// for: an auth with no password method that seeds its administrator for no
// registration deploys an account nobody can sign in to, and the check fails naming
// both ways out. Either way out passes: the password method, or a
// session.SeedUserIDThrough the author wrote, which is taken as the author's word and
// held only to naming a registration of the auth that admits one organization.
func TestFirstAdministrator(t *testing.T) {
	t.Parallel()

	// directoryOnly is the fixture's auth with no password method and the foothold
	// that seeds its first administrator under seed.
	directoryOnly := func(files map[string]string, registration, seed string) {
		auth := strings.Replace(fmt.Sprintf(admittingAuth, "google.DisableRoleSync()"), passwordMethod, "", 1)
		auth = strings.Replace(auth, `google.SignIn(s, "google", nil, &google.Config{RoleSync: google.DisableRoleSync()})`, registration, 1)
		files["pkg/auth/staff/staff.go"] = auth
		files["pkg/auth/staff/foothold.go"] = strings.Replace(plainFoothold, seedPlain, seed, 1)
	}
	// microsoft is that auth with a Microsoft registration in place of the Google one,
	// its tenants slot written as impulse add auth writes it, by a function of the
	// file, which is the form the scan reads the registration's width from; the seed
	// names the registration.
	microsoft := func(files map[string]string, tenants string) {
		directoryOnly(files,
			"tenants := directorySlot()\n\tazure.SignIn(s, \"directory\", nil, &azure.Config{Tenants: tenants, RoleSync: signin.DisableRoleSync()})",
			`id := session.SeedUserIDThrough(Name, "directory", req.Username)`)
		auth := strings.Replace(files["pkg/auth/staff/staff.go"], `"github.com/impulseframework/impulse/session/google"`, `"github.com/impulseframework/impulse/session/azure"`, 1)
		files["pkg/auth/staff/staff.go"] = auth + "\nfunc directorySlot() azure.TenantSet {\n\treturn " + tenants + "\n}\n"
	}
	tests := []struct {
		name        string
		edit        func(files map[string]string)
		wantStatus  Status
		wantSummary string
		wantDetails []string
	}{
		{
			name:        "an auth with the password method signs its first administrator in with it",
			wantStatus:  Pass,
			wantSummary: "the first administrator can sign in: staff (signs in with a password)",
		},
		{
			name: "no password method and a seed for no registration: the administrator cannot sign in",
			edit: func(files map[string]string) {
				directoryOnly(files, googleRegistration, seedPlain)
			},
			wantStatus:  Fail,
			wantSummary: "1 auth(s) seed a first administrator who cannot sign in",
			wantDetails: []string{cannotSignIn},
		},
		{
			name: "a seed the author names the registration of, by a constant of the package",
			edit: func(files map[string]string) {
				directoryOnly(files, googleRegistration, "id := session.SeedUserIDThrough(Name, GoogleRegistration, req.Username)")
				files["pkg/auth/staff/google.go"] = "package staff\n\n// GoogleRegistration is the registration's name.\nconst GoogleRegistration = \"google\"\n"
			},
			wantStatus:  Pass,
			wantSummary: "the first administrator can sign in: staff (seeded for the google registration)",
		},
		{
			name: "a seed that names the registration by a value the check cannot read is the author's word",
			edit: func(files map[string]string) {
				directoryOnly(files, googleRegistration, "id := session.SeedUserIDThrough(Name, a.ownDirectory(), req.Username)")
			},
			wantStatus:  Pass,
			wantSummary: "the first administrator can sign in: staff (seeded for the registration its foothold names)",
		},
		{
			name: "a seed for a registration the auth does not have",
			edit: func(files map[string]string) {
				directoryOnly(files, googleRegistration, `id := session.SeedUserIDThrough(Name, "workspace", req.Username)`)
			},
			wantStatus:  Fail,
			wantSummary: "1 auth(s) seed a first administrator who cannot sign in",
			wantDetails: []string{`pkg/auth/staff/foothold.go:9: the first administrator the migrate command seeds for the staff auth cannot sign in: it is seeded for the registration "workspace" (session.SeedUserIDThrough), and the auth registers no method under that name, so no sign-in comes through it; name one of the auth's own registrations (google), the one that is the deployment's own directory`},
		},
		{
			name: "a seed for a Microsoft registration open to any tenant",
			edit: func(files map[string]string) {
				microsoft(files, "azure.AnyTenant()")
			},
			wantStatus:  Fail,
			wantSummary: "1 auth(s) seed a first administrator who cannot sign in",
			wantDetails: []string{"pkg/auth/staff/foothold.go:9: the first administrator the migrate command seeds for the staff auth cannot sign in: it is seeded for the directory registration (session.SeedUserIDThrough), which admits more than one organization (any tenant), and the sign-in rule links a seeded account only through a registration of one organization and nobody else, since any of the others could present the name; register the deployment's own directory as one tenant (azure.Tenant) and seed for that registration, or give the auth a password method for its first administrator"},
		},
		{
			name: "a seed for a Microsoft registration of one tenant",
			edit: func(files map[string]string) {
				microsoft(files, `azure.Tenant("")`)
			},
			wantStatus:  Pass,
			wantSummary: "the first administrator can sign in: staff (seeded for the directory registration)",
		},
		{
			name: "an auth that seeds no first administrator is not held",
			edit: func(files map[string]string) {
				directoryOnly(files, googleRegistration, seedPlain)
				delete(files, "pkg/auth/staff/foothold.go")
			},
			wantStatus:  Skip,
			wantSummary: "no auth whose roles the application assigns seeds a first administrator",
		},
		{
			name: "an auth whose directory writes the roles seeds nobody by name",
			edit: func(files map[string]string) {
				directoryOnly(files, `google.SignIn(s, "google", nil, &google.Config{RoleSync: google.RoleSync()})`, seedPlain)
			},
			wantStatus:  Skip,
			wantSummary: "no auth whose roles the application assigns seeds a first administrator",
		},
		{
			name: "a tenant record declaring no directories links a seed on its name",
			edit: func(files map[string]string) {
				directoryOnly(files, googleRegistration, seedPlain)
				delete(files, "pkg/resources/tenant_directories.go")
			},
			wantStatus:  Skip,
			wantSummary: "no tenant record declares directories, so an account seeded ahead of its first sign-in is linked on its name",
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
			got := firstAdministrator{}.Run(context.Background(), &Env{App: a})
			want := Result{Name: firstAdministrator{}.Name(), Status: tt.wantStatus, Summary: tt.wantSummary, Details: tt.wantDetails}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("Run() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
