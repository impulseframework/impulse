package check

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"sort"
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
// resource takes, and on fields the resource has for that permission. A file the engine
// would refuse stops the release at start, so the refusal is a finding here, naming the
// file, the role and the rule. A grant's condition against the collection's vocabulary
// stays with the engine and the validation test auths-wired asks for.
type rolesFileAccepted struct{}

// rolesFileAcceptedName is the check's name.
const rolesFileAcceptedName = "roles-file"

func (rolesFileAccepted) Name() string {
	return rolesFileAcceptedName
}

func (rolesFileAccepted) Describe() string {
	return "every auth package's role file (pkg/auth/<name>/roles.json) parses the way the permission engine reads it at start: every role is named in the one grammar (letters and digits starting with a capital letter; the scope is never part of the name), none takes the name the framework keeps for its own role (SuperAdministrator), and each carries a description saying what it is for, and every grant names a resource with a condition that parses; and, where the application's generators have written a permission collection (zz_gen_collection.go), every grant is on what the application serves: a resource the collection names, at the role's scope, for a permission the resource takes and on fields it has; a file the engine would refuse is a finding naming the file, the role and the rule"
}

func (c rolesFileAccepted) Run(_ context.Context, env *Env) Result {
	a := env.App
	if len(a.AuthPackages) == 0 {
		return skip(c.Name(), "no auth package (pkg/auth/<name>) holds a role file")
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
		data, err := os.ReadFile(a.Abs(rel))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			details = append(details, fmt.Sprintf("%s does not exist; the %s auth embeds its role file from there", rel, p.Name))
			refused++

			continue
		case err != nil:
			details = append(details, fmt.Sprintf("%s: cannot be read: %v", rel, errors.Cause(err)))
			refused++

			continue
		}
		config, err := access.RoleFile(data).Parse()
		if err != nil {
			details = append(details, fmt.Sprintf("%s: %s", rel, refusalWords(err)))
			refused++

			continue
		}
		if unserved := unservedGrants(rel, config, served); len(unserved) > 0 {
			details = append(details, unserved...)
			refused++

			continue
		}
		files = append(files, rel)
	}
	if len(details) > 0 {
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
// not take, or on a field the resource does not have for the permission. A nil served,
// an application whose generators have written no collection, has none to find.
func unservedGrants(rel string, config *access.RoleConfig, served *app.Served) []string {
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
// with what to do, or empty for a grant the engine accepts.
func unservedGrant(served *app.Served, scope, role, permission string, grant access.Grant) string {
	resource := string(grant.Resource)
	r, ok := served.Resource(resource)
	switch {
	case !ok:
		return fmt.Sprintf("the %s role %s grants %s on %s, which the application does not serve (no resource of that name is in %s): take the grant out of the role, or serve the resource and run go generate ./...", scope, role, permission, resource, strings.Join(served.Files, ", "))
	case r.Scope != scope:
		return fmt.Sprintf("%s is a %s role and grants %s on %s, a %s resource: a role's grants are at its own scope, so move the grant to a %[5]s role", role, scope, permission, resource, r.Scope)
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

// takenWords names the permissions a resource takes, or says it takes none.
func takenWords(permissions []string) string {
	if len(permissions) == 0 {
		return "no permission"
	}

	return strings.Join(permissions, ", ")
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
