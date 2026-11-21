package access

import (
	"fmt"
	"slices"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes/condition"
)

// The set no role reads (UnreadSetWarning). An application declares a subject set
// (@subjectSet) so that a grant can test a row against it (hangarId IN
// subject.hangarsOpen). A set no grant reads does nothing: the people it gathers hold
// nothing through it, and nobody is told. A set is read when a grant's condition names
// it, or when a set that is read builds on it in its who: part (GroupID IN
// subject.groups), since the first set then holds what the second yields. A set describes
// the people of one auth, and only that auth's roles can give them anything through it:
// an application with several auths says which on each set (auth: in the @subjectSet), the
// collection lists each auth its own, and one auth's check never looks at another's. It
// is a warning and never a refusal, because a set may be declared one change before the
// first grant that reads it.

// UnreadSetWarning is one subject set the application declares for an auth's people that
// no grant of the auth's role file reads, directly or through another set's who: part.
type UnreadSetWarning struct {
	// Set is the set's name, as a condition writes it after "subject.".
	Set string
}

func (UnreadSetWarning) warning() {}

// String renders the warning as one line: what is declared, that nothing reads it, and
// what closes it.
func (w UnreadSetWarning) String() string {
	return fmt.Sprintf("subject set %s is declared (@subjectSet) and no role reads it: no grant's condition names subject.%s, and no set a grant reads builds on it in its who: part, so nobody holds anything through it. Write the grant that reads it, or accept this warning where the role file is validated until that grant is written. If the set describes another auth's people, say so on the set (auth: in its @subjectSet) and hand this auth's name to WithDefaultRoles and to ValidateRoles, and this auth's check no longer looks at it.",
		w.Set, w.Set)
}

// unreadSetWarnings flags each subject set the collection declares for the auth's people
// that no grant of the auth's role configuration reads, in name order. A set is read when
// a condition names it, and so is every set a read set builds on, however long the chain.
func unreadSetWarnings(store PermissionCollection, auth string, config *RoleConfig) ([]Warning, error) {
	if store == nil || config == nil {
		return nil, nil
	}
	declared := store.SubjectSets(auth)
	if len(declared) == 0 {
		return nil, nil
	}

	read := make(map[string]struct{})
	var follow []string
	mark := func(name string) {
		if _, seen := read[name]; seen {
			return
		}
		read[name] = struct{}{}
		follow = append(follow, name)
	}
	for _, role := range slices.Concat(config.Roles.Global, config.Roles.Domain) {
		for _, grants := range role.Permissions {
			for _, grant := range grants {
				if grant.Condition == "" {
					continue
				}
				expr, err := condition.Parse(grant.Condition)
				if err != nil {
					return nil, errors.Wrapf(err, "role %s: condition %q", role.Name, grant.Condition)
				}
				for _, name := range condition.SubjectSets(expr) {
					mark(name)
				}
			}
		}
	}
	for len(follow) > 0 {
		name := follow[0]
		follow = follow[1:]
		for _, built := range declared[name] {
			mark(built)
		}
	}

	names := make([]string, 0, len(declared))
	for name := range declared {
		if _, ok := read[name]; !ok {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	warnings := make([]Warning, 0, len(names))
	for _, name := range names {
		warnings = append(warnings, UnreadSetWarning{Set: name})
	}

	return warnings, nil
}
