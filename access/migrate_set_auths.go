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
// another auth's set is looked up by ids its holders do not have: it gives them nothing,
// or, where an id of theirs is also an id of the other auth's, another person's rows. So
// the grant is refused where the role file is loaded with its auth's name, and so is a
// custom role's grant in the engine the file is handed to, which is a role of the same
// auth (grantRule). A set that names no auth is any auth's to read, and a file handed
// over with no name is held to no auth's sets.

// validateAuthName refuses a role file handed over under a name that is no auth the
// collection's generation knew (PermissionCollection.Auths). The name is what ties the
// file to its auth's sets: under a name no set is labeled for, every labeled set would
// read as another auth's, the check would look at none of the file's own, and a grant
// through one would be refused as another auth's. An empty name is held to nothing, and
// a collection that knows no auth accepts any name.
func validateAuthName(store PermissionCollection, auth string) error {
	if auth == "" {
		return nil
	}
	known := store.Auths()
	if len(known) == 0 || slices.Contains(known, auth) {
		return nil
	}

	return errors.Newf("the role file is handed over as the roles of %q, and the application's generation knew no auth of that name: the auths it knew are %s. Hand over the auth's name as generation knows it, which is the name of the auth's package and the name a @subjectSet says in its auth: part",
		auth, strings.Join(known, ", "))
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

// anotherAuthsSet says why a condition of a role of auth may not read the first set it
// reads, in the order it reads them, that describes another auth's people, or nil when
// it reads none. It is the one place the rule is written: the role file
// (validateGrantSetAuth), a write to a custom role and a load of the policy
// (grantRule.refusal) each ask it with the auth's name as its package names it, and
// add the role, the permission and the resource themselves. An empty auth is held to
// no auth's sets, and a set that names no auth is any auth's to read.
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
