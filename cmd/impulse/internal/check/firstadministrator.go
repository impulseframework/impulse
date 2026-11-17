package check

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/go-playground/errors/v5"

	"github.com/impulseframework/impulse/app"
)

// firstAdministrator verifies that the first administrator an auth's migrate command
// seeds can sign in where the application's tenants have directories. An auth whose
// people sign in through a directory and whose roles the application gives seeds its
// first administrator with no password, under the sign-in name the directory will
// present (SeedAdministrator in the package's foothold.go). Such an account has never
// signed in, so nothing but its name ties it to whoever signs in next, and where
// tenants have directories the sign-in rule links no account on a name alone: a
// directory asserts the names it likes. The rule links the seed only when the
// deployment says which registration its name is believed from, by the id the seed
// creates the account under (session.SeedUserIDThrough), and only through that
// registration while it admits one organization and nobody else.
//
// impulse add auth writes that id where it cannot be wrong, for an auth whose one
// directory method is of one Microsoft Entra tenant or one Google Workspace. For any
// other auth (several directory methods, one open to more than one organization, an
// organization's identity provider) it seeds for no registration and says so in a
// comment, and the deployment then seeds an administrator who can never sign in.
// Nothing compiles against that, and this check refuses it before anything is
// deployed.
//
// The check reads what the package shows: its methods, whether it declares
// SeedAdministrator, and whether it derives an id with session.SeedUserIDThrough. A
// SeedUserIDThrough the author wrote is the author's word that the registration it
// names is the deployment's own directory, and is taken as that; the check holds it
// only to what it can read, that the registration is one of the auth's and, for a
// Microsoft registration, that it admits one tenant and no personal accounts.
type firstAdministrator struct{}

// firstAdministratorName is the check's name.
const firstAdministratorName = "first-administrator"

func (firstAdministrator) Name() string {
	return firstAdministratorName
}

func (firstAdministrator) Describe() string {
	return "in an application whose tenant record declares directories (@tenantDirectory), the first administrator each auth's migrate command seeds can sign in: an auth whose roles the application assigns either registers the password method, or seeds its first administrator for the registration that is the deployment's own directory (session.SeedUserIDThrough in the auth package), one of the auth's own that admits one organization"
}

// The foothold's shape as impulse add auth writes it: the method the migrate command
// seeds the first administrator with, and the two derivations of the seed's id.
const (
	seedAdministratorFunc = "SeedAdministrator"
	seedThroughFunc       = "SeedUserIDThrough"
	seedPlainFunc         = "SeedUserID"
)

func (c firstAdministrator) Run(_ context.Context, env *Env) Result {
	a := env.App
	if len(a.AuthPackages) == 0 {
		return skip(c.Name(), "no auth package (pkg/auth/<name>) constructs a session Auth")
	}
	if !declaresDirectories(a) {
		return skip(c.Name(), "no tenant record declares directories, so an account seeded ahead of its first sign-in is linked on its name")
	}

	var details, held []string
	for i := range a.AuthPackages {
		p := &a.AuthPackages[i]
		if !assignsRoles(a, p) {
			continue // the directory writes the roles, or no construction is read: nobody is seeded by name
		}
		auth := authOf(a, p)
		if slices.ContainsFunc(auth.Methods, func(m app.Method) bool { return m.Kind == app.MethodPassword }) {
			held = append(held, p.Name+" (signs in with a password)")

			continue
		}
		foothold, err := readFoothold(a, p.Dir)
		if err != nil {
			return fail(c.Name(), err.Error())
		}
		if foothold.seeds == "" {
			continue // the package seeds no first administrator
		}
		finding, through := judgeFoothold(p, auth, foothold)
		if finding != "" {
			details = append(details, finding)

			continue
		}
		held = append(held, fmt.Sprintf("%s (seeded for %s)", p.Name, through))
	}

	switch {
	case len(details) > 0:
		return fail(c.Name(), fmt.Sprintf("%d auth(s) seed a first administrator who cannot sign in", len(details)), details...)
	case len(held) == 0:
		return skip(c.Name(), "no auth whose roles the application assigns seeds a first administrator")
	default:
		return pass(c.Name(), "the first administrator can sign in: "+strings.Join(held, ", "))
	}
}

// declaresDirectories reports an application one of whose sites declares a directories
// record: its tenants have directories, and the sign-in rule links no account on a
// name alone.
func declaresDirectories(a *app.App) bool {
	p := a.Profile()

	return slices.ContainsFunc(p.Sites, func(site app.Site) bool { return site.Directories != nil })
}

// foothold is what an auth package shows of the first administrator it seeds.
type foothold struct {
	// seeds is where the package declares SeedAdministrator (file:line), empty where it
	// declares none.
	seeds string
	// named reports that the package derives an id with session.SeedUserIDThrough, and
	// through is the registration the call names, its second argument, where that is a
	// string or a constant of the package; empty where it is written any other way.
	named   bool
	through string
}

// readFoothold reads the hand-written files of an auth package for its foothold.
func readFoothold(a *app.App, dir string) (*foothold, error) {
	entries, err := os.ReadDir(a.Abs(dir))
	if err != nil {
		return nil, errors.Wrap(err, "os.ReadDir()")
	}
	found := &foothold{}
	constants := map[string]string{}
	var argument ast.Expr
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || isTestFile(name) || strings.HasPrefix(name, "zz_gen_") {
			continue
		}
		rel := path.Join(dir, name)
		src, err := os.ReadFile(a.Abs(rel))
		if err != nil {
			return nil, errors.Wrap(err, "os.ReadFile()")
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, rel, src, parser.SkipObjectResolution)
		if err != nil {
			return nil, errors.Wrap(err, "parser.ParseFile()")
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.FuncDecl:
				if node.Name.Name == seedAdministratorFunc && found.seeds == "" {
					found.seeds = fmt.Sprintf("%s:%d", rel, fset.Position(node.Pos()).Line)
				}
			case *ast.ValueSpec:
				for i, ident := range node.Names {
					if i < len(node.Values) {
						if value, ok := stringLiteral(node.Values[i]); ok {
							constants[ident.Name] = value
						}
					}
				}
			case *ast.CallExpr:
				if sel, ok := node.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == seedThroughFunc && len(node.Args) == 3 && argument == nil {
					argument = node.Args[1]
				}
			}

			return true
		})
	}
	if argument == nil {
		return found, nil
	}
	found.named = true
	if ident, ok := argument.(*ast.Ident); ok {
		found.through = constants[ident.Name]
	} else if value, ok := stringLiteral(argument); ok {
		found.through = value
	}

	return found, nil
}

// stringLiteral is the value of a string literal expression.
func stringLiteral(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}

	return value, true
}

// judgeFoothold holds an auth that seeds a first administrator with no password to the
// registration its seed names: finding says what is wrong and what to do, and through
// says the registration, for the summary, where nothing is.
func judgeFoothold(p *app.AuthPackage, auth *app.Auth, f *foothold) (finding, through string) {
	const remedy = "give the auth a password method for its first administrator (impulse add method %[1]s password), or name the registration that is the deployment's own directory in seedFoothold: derive the id with session.SeedUserIDThrough(Name, the registration's constant, req.Username), which the sign-in rule links at that person's first sign-in through the registration while it admits one organization alone"
	if !f.named {
		return fmt.Sprintf("%s: the first administrator the migrate command seeds for the %s auth cannot sign in: the application's tenants have directories, where a name alone links no account that has never signed in, and the auth registers no password method and seeds its administrator for no registration (session.%s); ", f.seeds, p.Name, seedPlainFunc) +
			fmt.Sprintf(remedy, p.Name), ""
	}
	if f.through == "" {
		// The argument is not a string or a constant of the package: the author's word,
		// which the check cannot read further.
		return "", "the registration its foothold names"
	}
	i := slices.IndexFunc(auth.Methods, func(m app.Method) bool { return m.Name == f.through })
	if i < 0 {
		return fmt.Sprintf("%s: the first administrator the migrate command seeds for the %s auth cannot sign in: it is seeded for the registration %q (session.%s), and the auth registers no method under that name, so no sign-in comes through it; name one of the auth's own registrations (%s), the one that is the deployment's own directory", f.seeds, p.Name, f.through, seedThroughFunc, registrationNames(auth)), ""
	}
	if m := &auth.Methods[i]; m.Kind == app.MethodAzure && m.Tenants != "" && (m.Tenants != app.TenantsOne || m.PersonalAccounts) {
		return fmt.Sprintf("%s: the first administrator the migrate command seeds for the %s auth cannot sign in: it is seeded for the %s registration (session.%s), which admits more than one organization (%s), and the sign-in rule links a seeded account only through a registration of one organization and nobody else, since any of the others could present the name; register the deployment's own directory as one tenant (azure.Tenant) and seed for that registration, or give the auth a password method for its first administrator", f.seeds, p.Name, f.through, seedThroughFunc, microsoftWidth(m)), ""
	}

	return "", "the " + f.through + " registration"
}

// registrationNames are the names of an auth's directory registrations, for a finding.
func registrationNames(auth *app.Auth) string {
	var names []string
	for i := range auth.Methods {
		if m := &auth.Methods[i]; m.Directory() && m.Name != "" {
			names = append(names, m.Name)
		}
	}
	if len(names) == 0 {
		return "it registers none"
	}

	return strings.Join(names, ", ")
}

// microsoftWidth says whom a Microsoft registration admits beyond one tenant.
func microsoftWidth(m *app.Method) string {
	var width string
	switch m.Tenants {
	case app.TenantsListed:
		width = "the tenants it lists"
	case app.TenantsAny:
		width = "any tenant"
	case app.TenantsPersonal:
		return "Microsoft personal accounts"
	default:
		width = "one tenant"
	}
	if m.PersonalAccounts {
		width += " and Microsoft personal accounts"
	}

	return width
}

// Meaning explains the invariant for the brief.
func (firstAdministrator) Meaning() string {
	return "An auth whose people sign in through a directory and whose roles the application gives starts from a first administrator the migrate command seeds with no password (`SeedAdministrator` in `pkg/auth/<name>/foothold.go`), under the sign-in name the placement names, and that person's first sign-in through the directory links to the account. Where the application's tenants have directories (a struct annotated `@tenantDirectory`), a name alone links no account that has never signed in, since a directory asserts the names it likes; the sign-in rule links the seed only when its id says which registration the name is believed from (`session.SeedUserIDThrough(Name, <Registration>, req.Username)` in `seedFoothold`), and only through that registration while it admits one organization and nobody else. `impulse add auth` writes that id for an auth whose one directory method is of one Microsoft Entra tenant or one Google Workspace. For any other auth (several directory methods, a method open to more than one organization, an organization's identity provider) it seeds for no registration, and the administrator the deployment seeds can never sign in. Either give the auth a password method for its first administrator (`impulse add method <name> password`), or, where one registration is the deployment's own directory and only its administrators give its sign-in names, say so in `seedFoothold` by deriving the id with `session.SeedUserIDThrough` over that registration's constant. Naming a registration is the deployment's word that whoever its directory presents under the name is the administrator; the check takes it as that, and holds it to naming one of the auth's own registrations and, for a Microsoft registration, one that admits one tenant and no personal accounts."
}
