package access

import (
	"maps"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/accesstypes/condition"
)

// Whose people a set describes, held on a role's grants. A subject set gathers rows by
// the ids of one auth's people (auth: in its @subjectSet), and a role is given to the
// people of the auth whose role file declares it. A grant of one auth's role that reads
// another auth's set is looked up by ids its holders do not have, so the set says
// nothing about them: a test of it gives them nothing, a negated test every row, and,
// where an id of theirs is also an id of the other auth's, what the set says of another
// person. So the grant is refused where the role file is loaded with its auth's name,
// and so is a custom role's grant in the engine the file is handed to, which is a role
// of the same auth (grantRule). A set that names no auth is any auth's to read. A file
// handed over with no name is held to no auth's sets where the collection knows one
// auth or none, and is refused where it knows several (validateAuthName).

// validateAuthName refuses a role file handed over under a name that is no auth the
// collection's generation knew (PermissionCollection.Auths). The name is what ties the
// file to its auth's sets: under a name no set is labeled for, every labeled set would
// read as another auth's, the check would look at none of the file's own, and a grant
// through one would be refused as another auth's. An empty name is refused where the
// collection knows several auths, since nothing then says whose roles the file's are;
// where it knows one or none an empty name is held to nothing, and a collection that
// knows no auth accepts any name.
func validateAuthName(store PermissionCollection, auth string) error {
	known := store.Auths()
	if auth == "" {
		if len(known) > 1 {
			return errors.Newf("the role file is handed over under no auth's name, and the application's generation knew several auths: %s. A role is held to the sets of its own auth's people, so hand the file over under its auth's name, which is the name of the auth's package and the name a @subjectSet says in its auth: part",
				strings.Join(known, ", "))
		}

		return nil
	}
	if len(known) == 0 || slices.Contains(known, auth) {
		return nil
	}

	return errors.Newf("the role file is handed over as the roles of %q, and the application's generation knew no auth of that name: the auths it knew are %s. Hand over the auth's name as generation knows it, which is the name of the auth's package and the name a @subjectSet says in its auth: part",
		auth, strings.Join(known, ", "))
}

// validateFileAuth holds a role configuration to the name it is handed over under: the
// name the file carries (RoleConfig.Auth) and the name handed over are the same, or the
// file is refused. A file that names one auth, handed to another auth's engine, would
// give that auth's people its roles; a file that names none, handed over under a name,
// says nothing that ties it to that auth; and a file that names one, handed over under
// none, is held to no auth's sets. A nil configuration is no file and is held to nothing.
func validateFileAuth(config *RoleConfig, auth string) error {
	if config == nil || config.Auth == auth {
		return nil
	}
	switch {
	case config.Auth == "":
		return errors.Newf(`the role file does not say whose roles it holds, and it is handed over as the roles of %q. A role file is handed over under the name it carries: add "auth": %q as the file's first key`,
			auth, auth)
	case auth == "":
		return errors.Newf(`the role file says it holds the roles of %q (its "auth"), and it is handed over under no auth's name: hand it over under the name it carries, access.WithDefaultRoles(file, %q)`,
			config.Auth, config.Auth)
	default:
		return errors.Newf(`the role file says it holds the roles of %q (its "auth"), and it is handed over as the roles of %q. An engine holds the roles of one auth, and a role file is handed over under the name it carries: hand the %s auth its own role file, or correct the file's "auth" if these are the %s auth's roles`,
			config.Auth, auth, auth, auth)
	}
}

// validateEngineAuth refuses an engine opened with no role file over a collection that
// knows several auths. Such an engine is told no auth's name, so nothing says whose
// roles it holds, and the grants of its custom roles would be held to no auth's sets.
// Where the collection knows one auth or none, it opens as an engine told an empty name
// does.
func validateEngineAuth(store PermissionCollection) error {
	known := store.Auths()
	if len(known) < 2 {
		return nil
	}

	return errors.Newf("the collection knows several auths (%s), and the engine is handed no role file, so it is told none of them. An engine holds the roles of one auth and holds each role's grants to that auth's sets of people: open it with the auth's role file under the auth's name, access.WithDefaultRoles(file, name)",
		strings.Join(known, ", "))
}

// validateRoleSetAuths holds every grant of the role configuration to the sets of the
// auth the configuration is handed over for (validateGrantSetAuth), the global roles
// first, then the domain roles, each role's permissions in name order.
func validateRoleSetAuths(store PermissionCollection, auth string, roles ScopedRoles) error {
	if auth == "" {
		return nil
	}
	for _, role := range slices.Concat(roles.Global, roles.Domain) {
		for _, perm := range slices.Sorted(maps.Keys(role.Permissions)) {
			for _, grant := range role.Permissions[perm] {
				if err := validateGrantSetAuth(store, auth, role.Name, perm, grant); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// validateGrantSetAuth refuses a grant of role, a role of auth, whose condition reads a
// subject set that describes another auth's people (anotherAuthsSet), in the role
// file's words: the role, the permission and the resource, then the reason. An empty
// auth holds the grant to no auth's sets, and so does a set that names none.
func validateGrantSetAuth(store PermissionCollection, auth string, role accesstypes.Role, perm accesstypes.Permission, grant Grant) error {
	if auth == "" || grant.Condition == "" {
		return nil
	}
	expr, err := condition.Parse(grant.Condition)
	if err != nil {
		return errors.Wrapf(err, "role %s: %s grant on %s", role, perm, grant.Resource)
	}
	if reason := anotherAuthsSet(store, auth, expr); reason != nil {
		return errors.Newf("role %s: %s grant on %s: %s", role, perm, grant.Resource, plainText(reason))
	}

	return nil
}

// anotherAuthsSet says why a condition of a role of auth may not read a set that
// describes another auth's people, naming the first such set in name order
// (condition.SubjectSets), or nil when it reads none. It is the one place the rule is
// written: the role file (validateGrantSetAuth), a write to a custom role and a load
// of the policy (grantRule.refusal) each ask it with the auth's name as its package
// names it, and add the role, the permission and the resource themselves. An empty
// auth is held to no auth's sets, and a set that names no auth is any auth's to read.
func anotherAuthsSet(store PermissionCollection, auth string, expr condition.Expr) error {
	if auth == "" {
		return nil
	}
	for _, name := range condition.SubjectSets(expr) {
		of := store.SubjectSetAuth(name)
		if of == "" || of == auth {
			continue
		}

		return errors.Newf("the condition reads subject.%s, which describes the people of %s (auth: %s in the set's @subjectSet), and this is a role of %s. The set is looked up by the ids of %s's people, so it says nothing about the people who hold this role: read a set that describes the people of %s, or give the grant to a role of %s",
			name, of, of, auth, of, auth, of)
	}

	return nil
}
