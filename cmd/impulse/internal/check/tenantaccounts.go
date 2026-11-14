package check

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/go-playground/errors/v5"

	"github.com/impulseframework/impulse/accesstypes/condition"
	"github.com/impulseframework/impulse/app"
)

// tenantAccounts holds the grants a tenant's role carries on the generated methods that
// change a person's account. An account is one for the whole application: the person a
// tenant's administrator disables, renames or deletes is disabled, renamed or deleted
// for every tenant that holds them and for the application's own staff. The generated
// management leaves what a tenant may do to such a person to the role file, as a
// condition on the method's grant over the facts the tenant's list of people carries
// (heldElsewhere, heldGlobally), so the rule can differ between applications. A grant
// with no condition that reads the person's row states no rule at all: every tenant's
// administrator may then change any account their tenant holds a role for. The check
// names each such grant.
type tenantAccounts struct{}

// tenantAccountsName is the check's name.
const tenantAccountsName = "tenant-accounts"

func (tenantAccounts) Name() string { return tenantAccountsName }

func (tenantAccounts) Describe() string {
	return "every grant a tenant's role carries on a generated method that changes a person's account (disable, enable, delete, rename, set the address, set a temporary password, require a password change, end the sessions, forget the trusted devices, unlink a sign-in identity) carries a condition that reads the person's row"
}

// Meaning explains the obligation for the handoff brief.
func (tenantAccounts) Meaning() string {
	return "A person's account is one for the whole application, so a method a tenant runs on it reaches every tenant that holds the person and the application's own staff. The generated management leaves the rule to the role file: each tenant method that changes an account names a row of the tenant's list of people (`<Auth><Tenant>Users`), whose facts a grant's condition reads, `heldElsewhere` (the person also holds a role in another tenant, or in every tenant) and `heldGlobally` (the person holds a role of the whole application). Give a domain role each such method under a condition over the row, `heldElsewhere = false AND heldGlobally = false` where an account is a tenant's to change only while that tenant alone holds the person (the tenanted skeleton's rule), or the condition your application's rule is. A grant with no condition, or with one that reads the request or the clock alone, states no rule about the person, and each line under the check names one. Every conditional grant is then proven by a test case (`conditions-proven`)."
}

// accountUser ends the name of a method that acts on the person's account as a whole.
const accountUser = "User"

// tenantAccountActs are the generated tenant methods that change a person's account, as
// the generator names them around the auth and the tenant record: the whole
// application's method name with the tenant record's name after the auth's. The methods
// that give or take a role in the tenant, invite to it or withdraw its invitation change
// what is the tenant's own and are not among them, and the method that creates a person
// names no row to read.
var tenantAccountActs = []struct{ before, after string }{
	{"Disable", accountUser},
	{"Enable", accountUser},
	{"Delete", accountUser},
	{"Rename", accountUser},
	{"Set", "UserEmail"},
	{"Set", "TemporaryPassword"},
	{"Require", "PasswordChange"},
	{"End", "UserSessions"},
	{"Revoke", "UserTrustedDevices"},
	{"Unlink", "SessionIdentity"},
}

// tenantAccountMethods names the account-changing methods of one auth's management under
// one tenant record.
func tenantAccountMethods(auth, tenant string) []string {
	methods := make([]string, 0, len(tenantAccountActs))
	for _, act := range tenantAccountActs {
		methods = append(methods, act.before+pascal(auth)+tenant+act.after)
	}

	return methods
}

func (c tenantAccounts) Run(_ context.Context, env *Env) Result {
	a := env.App
	methods := managedTenantMethods(a)
	if len(methods) == 0 {
		return skip(c.Name(), "no program generates the management of an auth's people beside a tenant record")
	}

	var details, lines []string
	held := 0
	for _, f := range engineRolesFiles(a) {
		account := methods[f.Auth.Path]
		if len(account) == 0 {
			continue
		}
		doc, err := readRolesFile(a.Abs(f.Path))
		if err != nil {
			details = append(details, fmt.Sprintf("%s: cannot be read: %v", f.Path, errors.Cause(err)))

			continue
		}
		heldHere := 0
		for _, role := range doc.Roles.Domain {
			for _, g := range role.Permissions["Execute"] {
				if !slices.Contains(account, g.Resource) {
					continue
				}
				if problem := accountGrantProblem(g.Condition); problem != "" {
					details = append(details, fmt.Sprintf("%s: the tenant role %s is granted Execute on %s %s, so every tenant's holder of the role may change the account of a person another tenant or the whole application also holds; state the application's rule in the grant's condition over the person's row (heldElsewhere = false AND heldGlobally = false keeps an account to the tenant that alone holds the person)", f.Path, role.Name, g.Resource, problem))

					continue
				}
				heldHere++
			}
		}
		held += heldHere
		lines = append(lines, fmt.Sprintf("%s: %d grant(s) on the %s auth's account methods, each under a condition that reads the person's row", f.Path, heldHere, f.Auth.Name))
	}
	if len(details) > 0 {
		return fail(c.Name(), fmt.Sprintf("%d grant(s) let a tenant change a person's account with no rule about the person", len(details)), details...)
	}

	return passWithDetails(c.Name(), fmt.Sprintf("%d grant(s) of a tenant's roles on the methods that change a person's account, each under a condition that reads the person's row", held), lines...)
}

// accountGrantProblem says what a grant's condition lacks, or nothing where it reads the
// person's row.
func accountGrantProblem(text string) string {
	if strings.TrimSpace(text) == "" {
		return "with no condition"
	}
	expr, err := condition.Parse(text)
	if err != nil {
		// The roles-file check names a condition the engine cannot read.
		return fmt.Sprintf("under %q, which does not parse", text)
	}
	if len(condition.PreImageBindings(expr)) == 0 {
		return fmt.Sprintf("under %q, which reads nothing of the person's row", text)
	}

	return ""
}

// managedTenantMethods lists, by auth package path, the account-changing tenant methods
// the application's programs generate: for each site whose program generates the
// management of its auths' people (GenerateIdentity) and whose resource package declares
// a tenant record, the methods of each auth its outlets declare.
func managedTenantMethods(a *app.App) map[string][]string {
	methods := map[string][]string{}
	p := a.Profile()
	for i := range p.Sites {
		site := &p.Sites[i]
		if site.Generator == nil || site.Generator.IdentityDir() == "" || !site.Tenanted() {
			continue
		}
		outlets := site.AllOutlets()
		for j := range outlets {
			auth := outlets[j].Auth
			if auth == nil {
				continue
			}
			for k := range a.AuthPackages {
				pkg := &a.AuthPackages[k]
				if pkg.Path != auth.ImportPath {
					continue
				}
				for _, method := range tenantAccountMethods(pkg.Name, site.TenantRecord.Name) {
					if !slices.Contains(methods[pkg.Path], method) {
						methods[pkg.Path] = append(methods[pkg.Path], method)
					}
				}
			}
		}
	}
	for path := range methods {
		sort.Strings(methods[path])
	}

	return methods
}
