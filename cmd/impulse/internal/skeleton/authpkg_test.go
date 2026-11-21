package skeleton

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/authpkg"
	"github.com/impulseframework/impulse/cmd/impulse/internal/names"
)

// candidateAuths are the auth packages each candidate carries, as authpkg renders them.
// The candidates are the rendering's source of truth for the transitions that copy them,
// and the rendering is the candidates' source of truth, so this test holds the two
// together: run with IMPULSE_UPDATE_AUTHPKG=1 to rewrite the candidates' auth packages
// from the templates after changing either.
var candidateAuths = map[string][]authpkg.Spec{
	"solo":     {staffSpec("solo")},
	"tenanted": {tenantedStaffSpec()},
	"sites":    {staffSpec("sites")},
	"outlets": {staffSpec("outlets"), {
		Module: candidateModule("outlets"), AuthDir: "pkg/auth", Name: "members",
		Doc: "the people who sign in through the organization's directory (OpenID Connect against Microsoft Entra), the portal's people",
		// The portal's people are mailed in the portal's name, the one its brand declares
		// (web/portal/brand.json), which impulse brand writes here.
		Application: "Outlets Portal",
		Methods:     []authpkg.Method{{Kind: app.MethodAzure, Name: "directory", Authority: app.AuthorityApplication}},
		// The development member's domain role (cmd/bootstrap/users.json), in every domain:
		// the one role that counts as an administrator's, and what the first one is seeded with.
		AdministratorRoles: authpkg.AdministratorRoles{EveryDomain: []string{"TenantAdministrator"}},
		IdentityDir:        identityDir("outlets"),
		TenantPolicy:       membersTenantPolicy(),
	}},
}

// staffSpec is the staff auth of each candidate: a password auth whose first administrator
// is invited to what the development administrator holds (cmd/bootstrap/users.json): the global
// roles, and the domain role in every domain where the candidate has tenants, with the
// sign-in rule over the tenant record's declaration where the candidate has one. The
// roles that count as an administrator's are the ones that give a role back:
// Administrator, and TenantAdministrator in a tenant. FeatureAdministrator turns flags
// on and off and manages nobody, so its holder is no administrator left behind.
func staffSpec(candidate string) authpkg.Spec {
	if candidate == Base {
		// The base's auth is the one description impulse new renders a first auth from,
		// so the candidate is held to it and not to a copy of it.
		return authpkg.FirstAuth(candidateModule(candidate), PlaceholderAuth)
	}
	roles := authpkg.AdministratorRoles{Global: []string{"Administrator"}, EveryDomain: []string{"TenantAdministrator"}}
	first := authpkg.AdministratorRoles{Global: []string{"Administrator", "FeatureAdministrator"}, EveryDomain: []string{"TenantAdministrator"}}
	// The outlets candidate's staff auth issues the devices outlet's tokens.
	var outlets []string
	if candidate == "outlets" {
		outlets = []string{"devices"}
	}

	return authpkg.Spec{
		Module: candidateModule(candidate), AuthDir: "pkg/auth", Name: PlaceholderAuth,
		Doc:                     "the people who sign in to the console with a password",
		Methods:                 []authpkg.Method{{Kind: app.MethodPassword}},
		AdministratorRoles:      roles,
		FirstAdministratorRoles: first,
		MachineOutlets:          outlets,
		IdentityDir:             identityDir(candidate),
		TenantPolicy:            tenantPolicy(candidate),
	}
}

// identityDir is the tree each candidate's generator program writes the management of
// its auths' people into (GenerateIdentity): the console site's in the sites candidate,
// whose console generator alone sets the option.
func identityDir(candidate string) string {
	if candidate == "sites" {
		return "apps/console/pkg/identity"
	}

	return "pkg/identity"
}

// tenantPolicy is the sign-in rule of a tenanted candidate's auths: the library's
// tenant policy over the Tenant record's declaration, read through the generated reader
// the data level hands in (the console site's, in the sites candidate, where the data
// level imports its handlers package as consoleapp). The staff auth's outlets serve the
// Tenant record in every tenanted candidate, so a person who signs up to it creates a
// tenant of their own and its rule admits a person who holds no tenant yet; nil for the
// base, which has no tenants.
func tenantPolicy(candidate string) *authpkg.TenantPolicy {
	switch candidate {
	case Base:
		return nil
	case "sites":
		return &authpkg.TenantPolicy{Record: "Tenant", Reader: "consoleapp.NewTenantDeclarationReader", Directories: true, AdmitWithoutTenant: true}
	default:
		return &authpkg.TenantPolicy{Record: "Tenant", Reader: "app.NewTenantDeclarationReader", Directories: true, AdmitWithoutTenant: true}
	}
}

// membersTenantPolicy is the sign-in rule of the outlets candidate's members auth: the
// portal's people, whose outlet does not serve the Tenant record, so nobody signs up to
// it and a person who holds no tenant is refused.
func membersTenantPolicy() *authpkg.TenantPolicy {
	return &authpkg.TenantPolicy{Record: "Tenant", Reader: "app.NewTenantDeclarationReader", Directories: true}
}

// tenantedStaffSpec is the tenanted candidate's staff auth: the password auth with a
// Google registration admitting any Google account, the wide method an administrator
// opens to sign-up in the running application: a stranger then signs in through it,
// holds no tenant, and creates one through the generated route.
func tenantedStaffSpec() authpkg.Spec {
	spec := staffSpec("tenanted")
	spec.Doc = "the people who sign in to the console with a password, or with any Google account"
	spec.Methods = append(spec.Methods, authpkg.Method{Kind: app.MethodGoogle, Name: "google", Slot: authpkg.SlotAny, Authority: app.AuthorityApplication})

	return spec
}

func candidateModule(candidate string) string {
	return "github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/" + candidate
}

// candidateMigrations are the sequence numbers each candidate's auth session migrations
// carry, by auth: the users migration and the sessions migration, in that order.
var candidateMigrations = map[string]map[string][2]int{
	"solo":     {PlaceholderAuth: {3, 4}},
	"tenanted": {PlaceholderAuth: {3, 4}},
	"sites":    {PlaceholderAuth: {3, 4}},
	"outlets":  {PlaceholderAuth: {3, 4}, "members": {9, 10}},
}

// usernameIsEmail reports an auth whose password method takes the address as the
// sign-in name, which shapes its users migration; no candidate's does.
func usernameIsEmail(spec *authpkg.Spec) bool {
	for i := range spec.Methods {
		if spec.Methods[i].UsernameIsEmail {
			return true
		}
	}

	return false
}

// TestAuthPackages holds each candidate's auth packages and their session migrations to
// the authpkg rendering.
func TestAuthPackages(t *testing.T) {
	t.Parallel()
	update := os.Getenv("IMPULSE_UPDATE_AUTHPKG") != ""
	for candidate, specs := range candidateAuths {
		for i := range specs {
			spec := specs[i]
			files, err := authpkg.Files(&spec)
			if err != nil {
				t.Fatalf("%s: authpkg.Files(%s) error = %v", candidate, spec.Name, err)
			}
			dir := filepath.Join("_candidates", candidate, filepath.FromSlash(spec.AuthDir), spec.Name)
			expected := map[string][]byte{}
			for name, data := range files {
				expected[filepath.Join(dir, name)] = data
			}
			numbers := candidateMigrations[candidate][spec.Name]
			for j, m := range authpkg.Migrations(names.Pascal(spec.Name), spec.Name, usernameIsEmail(&spec)) {
				base := filepath.Join("_candidates", candidate, "schema", "migrations", fmt.Sprintf("%06d_%s", numbers[j], m.Stem))
				expected[base+".up.sql"] = []byte(m.Up)
				expected[base+".down.sql"] = []byte(m.Down)
			}
			for file, want := range expected {
				if update {
					if err := os.WriteFile(file, want, 0o600); err != nil {
						t.Fatal(err)
					}

					continue
				}
				got, err := os.ReadFile(file)
				if err != nil {
					t.Errorf("%s: %v (run with IMPULSE_UPDATE_AUTHPKG=1 to write the auth packages)", candidate, err)

					continue
				}
				if !bytes.Equal(got, want) {
					t.Errorf("%s: %s differs from the authpkg rendering (run with IMPULSE_UPDATE_AUTHPKG=1 to rewrite it)", candidate, file)
				}
			}
		}
	}
}
