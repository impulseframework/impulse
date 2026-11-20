package check

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/app"
)

// rolesFileAccepted verifies that every auth package's role file is one the permission
// engine accepts: the file parses the way the engine reads it when the release starts
// (access.RoleFile.Parse), which settles everything the file decides on its own, with no
// permission collection. Every role is named in the one grammar, letters and digits
// starting with a capital letter, with the scope never part of the name, and none takes
// the name of the role the framework defines in every auth, SuperAdministrator, in any
// case; every role carries a description saying what it is for; and every grant names a
// resource, with a condition that parses. A file the engine would refuse stops the release at start, so
// the refusal is a finding here, naming the file, the role and the rule. What the file
// grants is validated against the collection by the engine and the validation test
// auths-wired asks for; this reads the file alone, with one thing from the generated
// collection files beside it: the actions no role is given (the entries written with
// NoRole: true, such as the action of the generated create-your-tenant route). A grant
// that names one is a finding too, saying why, so the author reads it here before the
// engine refuses the file at start.
type rolesFileAccepted struct{}

// rolesFileAcceptedName is the check's name.
const rolesFileAcceptedName = "roles-file"

func (rolesFileAccepted) Name() string {
	return rolesFileAcceptedName
}

func (rolesFileAccepted) Describe() string {
	return "every auth package's role file (pkg/auth/<name>/roles.json) parses the way the permission engine reads it at start, with no permission collection: every role is named in the one grammar (letters and digits starting with a capital letter; the scope is never part of the name), none takes the name the framework keeps for its own role (SuperAdministrator), and each carries a description saying what it is for, and every grant names a resource with a condition that parses; no grant names an action no role is given (an entry the generated collection file, zz_gen_collection.go, writes with NoRole: true, such as the action of the create-your-tenant route), since nothing asks for a grant of it; a file the engine would refuse is a finding naming the file, the role and the rule"
}

func (c rolesFileAccepted) Run(_ context.Context, env *Env) Result {
	a := env.App
	if len(a.AuthPackages) == 0 {
		return skip(c.Name(), "no auth package (pkg/auth/<name>) holds a role file")
	}
	noRole, err := noRoleActionsOf(a)
	if err != nil {
		return fail(c.Name(), refusalWords(err))
	}

	var details, files []string
	refused := 0
	for i := range a.AuthPackages {
		p := &a.AuthPackages[i]
		rel := rolesFileOf(p)
		found := c.findings(a, p, rel, noRole)
		if len(found) > 0 {
			details = append(details, found...)
			refused++

			continue
		}
		files = append(files, rel)
	}
	if refused > 0 {
		return fail(c.Name(), fmt.Sprintf("%d role file(s) the permission engine would refuse at start", refused), details...)
	}

	return pass(c.Name(), fmt.Sprintf("%d role file(s) parse the way the permission engine reads them: %s", len(files), strings.Join(files, ", ")))
}

// findings reads one auth package's role file and answers what the engine would refuse
// it for: that it is missing or unreadable, the rule its parse breaks, or each grant
// that names an action no role is given. A file the engine accepts has none.
func (rolesFileAccepted) findings(a *app.App, p *app.AuthPackage, rel string, noRole map[accesstypes.Resource]string) []string {
	data, err := os.ReadFile(a.Abs(rel))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return []string{fmt.Sprintf("%s does not exist; the %s auth embeds its role file from there", rel, p.Name)}
	case err != nil:
		return []string{fmt.Sprintf("%s: cannot be read: %v", rel, errors.Cause(err))}
	}
	config, err := access.RoleFile(data).Parse()
	if err != nil {
		return []string{fmt.Sprintf("%s: %s", rel, refusalWords(err))}
	}

	return noRoleGrants(rel, config, noRole)
}

// collectionFile is the generated file that holds an application's permission
// collection, in the package a generator writes its routes into.
const collectionFile = "zz_gen_collection.go"

// noRoleActionsOf reads the actions no role is given out of the application's generated
// collection files, the one in each generator's routes package: the entries the
// generator writes with NoRole: true. It answers each action's name with the file that
// marks it. An action marked in one site's collection is no role's in every auth's role
// file, as it is in the collection the engine validates the files against. A generator
// that writes no routes has no collection file, and one whose file is not written yet
// marks nothing: the regeneration checks report that.
func noRoleActionsOf(a *app.App) (map[accesstypes.Resource]string, error) {
	marked := map[accesstypes.Resource]string{}
	for _, g := range a.Generators {
		routes := g.RoutesDir()
		if routes == "" {
			continue
		}
		rel := path.Join(routes, collectionFile)
		src, err := os.ReadFile(a.Abs(rel))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, errors.Newf("%s: cannot be read: %v", rel, errors.Cause(err))
		}
		f, err := parser.ParseFile(token.NewFileSet(), rel, src, parser.SkipObjectResolution)
		if err != nil {
			return nil, errors.Newf("%s does not parse, so the actions no role is given cannot be read from it: %v; regenerate the application (go generate ./...)", rel, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if name, ok := noRoleEntry(n); ok {
				if _, seen := marked[name]; !seen {
					marked[name] = rel
				}
			}

			return true
		})
	}

	return marked, nil
}

// noRoleEntry reads one entry of a generated collection: the name of a composite
// literal that carries NoRole: true beside its Name, as the generator writes an action
// no role is given.
func noRoleEntry(n ast.Node) (accesstypes.Resource, bool) {
	lit, ok := n.(*ast.CompositeLit)
	if !ok {
		return "", false
	}
	var name string
	var noRole bool
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		switch key.Name {
		case "Name":
			if value, ok := kv.Value.(*ast.BasicLit); ok && value.Kind == token.STRING {
				if unquoted, err := strconv.Unquote(value.Value); err == nil {
					name = unquoted
				}
			}
		case "NoRole":
			if value, ok := kv.Value.(*ast.Ident); ok && value.Name == "true" {
				noRole = true
			}
		}
	}

	return accesstypes.Resource(name), noRole && name != ""
}

// noRoleGrants names each grant of a parsed role file that names an action no role is
// given, in the file's order (the global roles, then the domain roles, each role's
// permissions by name), saying what the action is and what to do.
func noRoleGrants(rel string, config *access.RoleConfig, noRole map[accesstypes.Resource]string) []string {
	if len(noRole) == 0 {
		return nil
	}

	var details []string
	for _, role := range slices.Concat(config.Roles.Global, config.Roles.Domain) {
		permissions := make([]string, 0, len(role.Permissions))
		for p := range role.Permissions {
			permissions = append(permissions, string(p))
		}
		sort.Strings(permissions)
		for _, p := range permissions {
			for _, g := range role.Permissions[accesstypes.Permission(p)] {
				marks, ok := noRole[g.Resource]
				if !ok {
					continue
				}
				details = append(details, fmt.Sprintf("%s: role %s grants %s on %s, which is an action no role is given (%s writes it with NoRole: true): nothing asks for a grant of it, so the grant would do nothing, and the action is registered only so that a permission mask can refuse it to a session that acts as someone; remove the grant from the role", rel, role.Name, p, g.Resource, marks))
			}
		}
	}

	return details
}

// refusalWords is a refusal's words alone: the outermost prefix the error chain carries,
// when it carries one, and the cause, without the source locations the chain renders.
func refusalWords(err error) string {
	var chain errors.Chain
	if !errors.As(err, &chain) {
		return err.Error()
	}
	cause := errors.Cause(err).Error()
	for i := len(chain) - 1; i >= 0; i-- {
		if chain[i].Prefix != "" {
			return chain[i].Prefix + ": " + cause
		}
	}

	return cause
}
