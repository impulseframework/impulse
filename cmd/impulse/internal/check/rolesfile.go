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
// engine accepts when the release starts. The file parses the way the engine reads it
// (access.RoleFile.Parse), which settles everything the file decides on its own: every
// role is named in the one grammar, letters and digits starting with a capital letter,
// with the scope never part of the name, and none takes the name of the role the
// framework defines in every auth, SuperAdministrator, in any case; every role carries a
// description saying what it is for; and every grant names a resource, with a condition
// that parses. And where the application's generators have written a permission
// collection (App.Served), every grant is on what the application serves: a resource the
// collection names, at the scope of the role that grants it, for a permission the
// resource takes, and on fields the resource has for that permission, whether among a
// grant's fields or granted alone (Resource.field). No grant names an action no role is
// given either: an entry the generated collection files write with NoRole: true, such as
// the action of the generated create-your-tenant route. That is the one finding such a
// grant gets, saying why, since the engine refuses the grant for it before it looks at
// the action's scope or permission. A file the engine would refuse
// stops the release at start, so the refusal is a finding here, naming the file, the
// role and the rule. A grant's condition against the collection's vocabulary stays with
// the engine and the validation test auths-wired asks for.
type rolesFileAccepted struct{}

// rolesFileAcceptedName is the check's name.
const rolesFileAcceptedName = "roles-file"

func (rolesFileAccepted) Name() string {
	return rolesFileAcceptedName
}

func (rolesFileAccepted) Describe() string {
	return "every auth package's role file (pkg/auth/<name>/roles.json) parses the way the permission engine reads it at start: every role is named in the one grammar (letters and digits starting with a capital letter; the scope is never part of the name), none takes the name the framework keeps for its own role (SuperAdministrator), and each carries a description saying what it is for, and every grant names a resource with a condition that parses; and, where the application's generators have written a permission collection (zz_gen_collection.go), every grant is on what the application serves: a resource the collection names, at the role's scope, for a permission the resource takes and on fields it has, and no grant names an action no role is given (an entry the collection writes with NoRole: true, such as the action of the create-your-tenant route), since nothing asks for a grant of it; a file the engine would refuse is a finding naming the file, the role and the rule"
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

	served, err := a.Served()
	if err != nil {
		return fail(c.Name(), "the application's permission collection cannot be read, so what its role files may grant is not known", refusalWords(err)+": run go generate ./... so the generator writes it again")
	}

	var details, files []string
	refused := 0
	for i := range a.AuthPackages {
		p := &a.AuthPackages[i]
		rel := rolesFileOf(p)
		found := c.findings(a, p, rel, noRole, served)
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
	if served == nil {
		return pass(c.Name(), fmt.Sprintf("%d role file(s) parse the way the permission engine reads them: %s", len(files), strings.Join(files, ", ")))
	}

	return pass(c.Name(), fmt.Sprintf("%d role file(s) parse the way the permission engine reads them and grant only what the application serves (%s): %s", len(files), strings.Join(served.Files, ", "), strings.Join(files, ", ")))
}

// unservedGrants are the grants of a role file the permission engine refuses against the
// application's collection, each as a finding: a grant on a resource the application
// does not serve, at the other scope than its role's, for a permission the resource does
// not take, or on a field the resource does not have for the permission, among the
// grant's fields or granted alone (Resource.field). A nil served, an application whose
// generators have written no collection, has none to find. A grant of an action no role
// is given, by its name or as all its fields (Action.*), is not judged here: noRoleGrants
// names it, and the engine refuses it for that before it looks at where the action is
// served.
func unservedGrants(rel string, config *access.RoleConfig, served *app.Served, noRole map[accesstypes.Resource]string) []string {
	if served == nil || config == nil {
		return nil
	}
	var findings []string
	scopes := []struct {
		scope string
		roles []*access.Role
	}{
		{app.ScopeGlobal, config.Roles.Global},
		{app.ScopeDomain, config.Roles.Domain},
	}
	for _, s := range scopes {
		for _, role := range s.roles {
			permissions := make([]string, 0, len(role.Permissions))
			for permission := range role.Permissions {
				permissions = append(permissions, string(permission))
			}
			sort.Strings(permissions)
			for _, permission := range permissions {
				for _, grant := range role.Permissions[accesstypes.Permission(permission)] {
					if _, marked := noRole[heldName(grant.Resource)]; marked {
						continue
					}
					if finding := unservedGrant(served, s.scope, string(role.Name), permission, grant); finding != "" {
						findings = append(findings, rel+": "+finding)
					}
				}
			}
		}
	}

	return findings
}

// unservedGrant is what is wrong with one grant against what the application serves,
// with what to do, or empty for a grant the engine accepts. A grant of one field alone,
// written Resource.field (as a role grants a field beside a grant of the resource that
// carries a condition), is judged as that field of the resource, and a grant of all a
// resource's fields, Resource.*, as the resource: the engine reads both so.
func unservedGrant(served *app.Served, scope, role, permission string, grant access.Grant) string {
	resource := string(grant.Resource)
	base, field, dotted := strings.Cut(string(heldName(grant.Resource)), ".")
	r, ok := served.Resource(base)
	switch {
	case !ok:
		return fmt.Sprintf("the %s role %s grants %s on %s, which the application does not serve (no resource of that name is in %s): take the grant out of the role, or serve the resource and run go generate ./...", scope, role, permission, resource, strings.Join(served.Files, ", "))
	case r.Scope != scope:
		return fmt.Sprintf("%s is a %s role and grants %s on %s, a %s resource: a role's grants are at its own scope, so move the grant to a %[5]s role", role, scope, permission, resource, r.Scope)
	case dotted && !r.FieldTakes(field, permission):
		return fmt.Sprintf("the %s role %s grants %s on %s, a field %s does not have for %[3]s: take the grant out of the role, or run go generate ./... if the resource just gained the field", scope, role, permission, resource, base)
	case dotted:
		return ""
	case !r.Takes(permission):
		return fmt.Sprintf("the %s role %s grants %s on %s, which takes %s and not %[3]s: take the grant out of the role, or grant a permission the resource takes", scope, role, permission, resource, takenWords(r.Permissions))
	}
	var missing []string
	for _, field := range grant.Fields {
		if !r.FieldTakes(string(field), permission) {
			missing = append(missing, string(field))
		}
	}
	if len(missing) > 0 {
		return fmt.Sprintf("the %s role %s grants %s on %s with the field(s) %s, which the resource does not have for %[3]s: take them out of the grant's fields, or run go generate ./... if the resource just gained them", scope, role, permission, resource, strings.Join(missing, ", "))
	}

	return ""
}

// heldName is the name the permission engine holds a role file's grant to: the
// resource's own for a grant of all its fields (Resource.*), and the name as written
// for every other, as the engine's declaredName reads it. The name is cut with
// strings.Cut, since accesstypes.Resource.ResourceAndTag panics on a name with a
// second dot and a role file can hold any text.
func heldName(res accesstypes.Resource) accesstypes.Resource {
	if base, field, dotted := strings.Cut(string(res), "."); dotted && field == "*" {
		return accesstypes.Resource(base)
	}

	return res
}

// takenWords names the permissions a resource takes, or says it takes none.
func takenWords(permissions []string) string {
	if len(permissions) == 0 {
		return "no permission"
	}

	return strings.Join(permissions, ", ")
}

// findings reads one auth package's role file and answers what the engine would refuse
// it for: that it is missing or unreadable, the rule its parse breaks, each grant that
// names an action no role is given, or each grant on what the application does not
// serve. A file the engine accepts has none.
func (rolesFileAccepted) findings(a *app.App, p *app.AuthPackage, rel string, noRole map[accesstypes.Resource]string, served *app.Served) []string {
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

	return append(noRoleGrants(rel, config, noRole), unservedGrants(rel, config, served, noRole)...)
}

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
		rel := path.Join(routes, app.CollectionFileName)
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
// given, by its name or as all its fields (Action.*, held to the action's name as the
// engine holds it), in the file's order (the global roles, then the domain roles, each
// role's permissions by name), saying what the action is and what to do.
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
				marks, ok := noRole[heldName(g.Resource)]
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
