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
// the grant is refused where the role file is loaded with its auth's name. A set that
// names no auth is any auth's to read, and a file handed over with no name is held to
// no auth's sets.

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
// subject set that describes another auth's people. It is the one place the rule is
// written: whatever accepts a role's grant for an auth, the role file today, calls it
// with the auth's name as its package names it. An empty auth holds the grant to no
// auth's sets, and so does a set that names none.
func validateGrantSetAuth(store PermissionCollection, auth string, role accesstypes.Role, perm accesstypes.Permission, grant Grant) error {
	if auth == "" || grant.Condition == "" {
		return nil
	}
	expr, err := condition.Parse(grant.Condition)
	if err != nil {
		return errors.Wrapf(err, "role %s: %s grant on %s", role, perm, grant.Resource)
	}
	for _, name := range condition.SubjectSets(expr) {
		of := store.SubjectSetAuth(name)
		if of == "" || of == auth {
			continue
		}

		return errors.Newf("role %s: %s grant on %s: the condition reads subject.%s, which describes the people of %s (auth: %s in the set's @subjectSet), and this role file is handed over as the roles of %s; the set is looked up by the ids of %s's people, so the role's holders get nothing of their own through it: read a set that describes the people of %s, or move the grant to a role of %s",
			role, perm, grant.Resource, name, of, of, auth, of, auth, of)
	}

	return nil
}
