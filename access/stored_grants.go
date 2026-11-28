package access

import (
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
)

// shadowedReason is why a load of the policy leaves out a stored row of a role the
// release's role file defines in a scope of that kind: the words ShadowedRole says of
// the role after its colon.
const shadowedReason = "this release has a default role of that name, which wins; rename or delete the custom role"

// StoredGrant is one row of the role grants table as a caller that read the row in a read
// of its own holds it: where the role is held, in the stores' encoding of the scope, the
// role, and the grant. Field is empty for a grant on the resource itself, and Condition
// for a grant that holds without one.
type StoredGrant struct {
	Kind       string
	Axis       string
	Domain     string
	Role       accesstypes.Role
	Permission accesstypes.Permission
	Resource   string
	Field      string
	Condition  string
}

// SkippedStoredGrants answers why a load of the policy leaves out each row given, ""
// for a row its role holds (UserManager.SkippedStoredGrants). One grant rule is built
// for the call, so a condition read for one row is not read again for the next.
func (u *userManager) SkippedStoredGrants(grants []StoredGrant) ([]string, error) {
	rule := newGrantRule(u.collection, u.defaults.authName())
	skipped := make([]string, 0, len(grants))
	for i := range grants {
		row := &grants[i]
		scope, err := policy.ScopeFromColumns(row.Kind, row.Axis, row.Domain)
		if err != nil {
			return nil, errors.Wrap(err, "policy.ScopeFromColumns()")
		}
		if u.defaults.exists(scope, row.Role) {
			skipped = append(skipped, shadowedReason)

			continue
		}
		grant := &policy.Grant{
			Scope:     scope,
			Subject:   policy.Subject{Kind: policy.SubjectRole, Name: string(row.Role)},
			Perm:      row.Permission,
			Resource:  row.Resource,
			Field:     row.Field,
			Condition: row.Condition,
		}
		reason := ""
		if unusable := rule.unusable(grant, true, parseStoredCondition); unusable != nil {
			reason = plainText(unusable)
		}
		skipped = append(skipped, reason)
	}

	return skipped, nil
}
