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

// passwordFoothold is the foothold impulse add auth writes for an auth that starts with
// the password method: the first administrator seeded with the password the
// deployment generates. SeedAdministrator is declared on line 11.
const passwordFoothold = `package staff

import (
	"context"

	"github.com/impulseframework/impulse/session"
)

const Administrator = "administrator"

func (a *Auth) SeedAdministrator(ctx context.Context, password string) (bool, error) {
	return true, a.seedFoothold(ctx, &session.CreateUserRequest{Username: Administrator, Password: &password, PasswordChangeRequired: true})
}

func (a *Auth) seedFoothold(ctx context.Context, req *session.CreateUserRequest) error {
	id := session.SeedUserID(Name, req.Username)
	req.ID = &id

	return nil
}
`

// The findings for a first administrator seeded for no registration, by what the auth
// lacks: the password method, or a seed that gives the account a password.
const (
	cannotSignInOpening = "pkg/auth/staff/foothold.go:9: the first administrator the migrate command seeds for the staff auth cannot sign in: the application's tenants have directories, where a name alone links no account that has never signed in, "
	seedWithPassword    = "seed the administrator with a password, as impulse add auth writes SeedAdministrator for an auth that starts with the method (session.CreateUserRequest with Password and PasswordChangeRequired, the migrate command handing it the secret the deployment generates)"
	nameRegistration    = ", or name the registration that is the deployment's own directory in seedFoothold: derive the id with session.SeedUserIDThrough(Name, the registration's constant, req.Username), which the sign-in rule links at that person's first sign-in through the registration while it admits one organization alone"
	cannotSignIn        = cannotSignInOpening + "the auth registers no password method, and the seed names no registration (session.SeedUserID); give the auth a password method for its first administrator (impulse add method staff password) and " + seedWithPassword + nameRegistration
	noPasswordSeeded    = cannotSignInOpening + "the auth registers the password method but its seed creates the account with no password (the session.CreateUserRequest in SeedAdministrator sets none), which nobody is there yet to give it, and the seed names no registration (session.SeedUserID); " + seedWithPassword + nameRegistration
)

// TestFirstAdministrator pins the rule on the first administrator an auth seeds where
// the application's tenants have directories. The refusal is the case the rule exists
// for: an auth that seeds its administrator with no password and for no registration
// deploys an account nobody can sign in to, and the check fails naming both ways out.
// Either way out passes: a password method with a seed that gives the account a
// password, or a session.SeedUserIDThrough the author wrote in the seed, which is taken
// as the author's word and held only to naming a registration of the auth that admits
// one organization. The password method alone is no way out, since adding it leaves
// the seed as it was; and what the package does outside the seed's two functions, for
// some other account, is not taken for the seed's.
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
			name: "an auth with the password method that seeds its first administrator with a password",
			edit: func(files map[string]string) {
				files["pkg/auth/staff/foothold.go"] = passwordFoothold
			},
			wantStatus:  Pass,
			wantSummary: "the first administrator can sign in: staff (seeded with a password)",
		},
		{
			name: "the password method beside a seed that gives no password: the administrator still cannot sign in",
			edit: func(files map[string]string) {
				files["pkg/auth/staff/foothold.go"] = plainFoothold
			},
			wantStatus:  Fail,
			wantSummary: "1 auth(s) seed a first administrator who cannot sign in",
			wantDetails: []string{noPasswordSeeded},
		},
		{
			name: "a password given to another account outside the seed is not the seed's",
			edit: func(files map[string]string) {
				files["pkg/auth/staff/foothold.go"] = plainFoothold
				files["pkg/auth/staff/operator.go"] = "package staff\n\nfunc (a *Auth) operator(password string) *session.CreateUserRequest {\n\treturn &session.CreateUserRequest{Username: \"operator\", Password: &password}\n}\n"
			},
			wantStatus:  Fail,
			wantSummary: "1 auth(s) seed a first administrator who cannot sign in",
			wantDetails: []string{noPasswordSeeded},
		},
		{
			name: "a seed that gives the password to a request built before",
			edit: func(files map[string]string) {
				files["pkg/auth/staff/foothold.go"] = strings.Replace(passwordFoothold,
					"return true, a.seedFoothold(ctx, &session.CreateUserRequest{Username: Administrator, Password: &password, PasswordChangeRequired: true})",
					"req := &session.CreateUserRequest{Username: Administrator, PasswordChangeRequired: true}\n\treq.Password = &password\n\n\treturn true, a.seedFoothold(ctx, req)", 1)
			},
			wantStatus:  Pass,
			wantSummary: "the first administrator can sign in: staff (seeded with a password)",
		},
		{
			name: "a seed whose request names a nil password gives none",
			edit: func(files map[string]string) {
				files["pkg/auth/staff/foothold.go"] = strings.Replace(plainFoothold, "&session.CreateUserRequest{Username: signInName}", "&session.CreateUserRequest{Username: signInName, Password: nil}", 1)
			},
			wantStatus:  Fail,
			wantSummary: "1 auth(s) seed a first administrator who cannot sign in",
			wantDetails: []string{noPasswordSeeded},
		},
		{
			name: "a seed that names the password method as its registration: no first sign-in links through it",
			edit: func(files map[string]string) {
				files["pkg/auth/staff/foothold.go"] = strings.Replace(plainFoothold, seedPlain, `id := session.SeedUserIDThrough(Name, "password", req.Username)`, 1)
			},
			wantStatus:  Fail,
			wantSummary: "1 auth(s) seed a first administrator who cannot sign in",
			wantDetails: []string{`pkg/auth/staff/foothold.go:9: the first administrator the migrate command seeds for the staff auth cannot sign in: it is seeded for the registration "password" (session.SeedUserIDThrough), and the auth registers no directory method under that name, so no sign-in links the account through it; name one of the auth's own directory registrations (google), the one that is the deployment's own directory`},
		},
		{
			name: "the password method beside a seed the author names the registration of",
			edit: func(files map[string]string) {
				files["pkg/auth/staff/foothold.go"] = strings.Replace(plainFoothold, seedPlain, `id := session.SeedUserIDThrough(Name, "google", req.Username)`, 1)
			},
			wantStatus:  Pass,
			wantSummary: "the first administrator can sign in: staff (seeded for the google registration)",
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
			name: "a registration named outside the seed, for some other account, is not the seed's",
			edit: func(files map[string]string) {
				directoryOnly(files, googleRegistration, seedPlain)
				files["pkg/auth/staff/operator.go"] = "package staff\n\nfunc operatorID() string {\n\treturn session.SeedUserIDThrough(Name, \"google\", \"operator\").String()\n}\n"
			},
			wantStatus:  Fail,
			wantSummary: "1 auth(s) seed a first administrator who cannot sign in",
			wantDetails: []string{cannotSignIn},
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
			wantDetails: []string{`pkg/auth/staff/foothold.go:9: the first administrator the migrate command seeds for the staff auth cannot sign in: it is seeded for the registration "workspace" (session.SeedUserIDThrough), and the auth registers no directory method under that name, so no sign-in links the account through it; name one of the auth's own directory registrations (google), the one that is the deployment's own directory`},
		},
		{
			name: "a seed for a Microsoft registration open to any tenant",
			edit: func(files map[string]string) {
				microsoft(files, "azure.AnyTenant()")
			},
			wantStatus:  Fail,
			wantSummary: "1 auth(s) seed a first administrator who cannot sign in",
			wantDetails: []string{"pkg/auth/staff/foothold.go:9: the first administrator the migrate command seeds for the staff auth cannot sign in: it is seeded for the directory registration (session.SeedUserIDThrough), which admits more than one organization (any tenant), and the sign-in rule links a seeded account only through a registration of one organization and nobody else, since any of the others could present the name; register the deployment's own directory as one tenant (azure.Tenant) and seed for that registration, or give the auth a password method and seed the administrator with a password"},
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
