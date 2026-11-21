package access

import (
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/accesstypes/condition"
)

// grantRule is the rule a role's grant on a resource is held to, by a write that adds
// the grant to a custom role (userManager.requireDeclared) and by a load of the policy
// that finds the grant in the store (snapshot.intern). Both ask refusal, so a load
// skips exactly the stored grants a write would refuse, and for the same reason.
//
// The rule is the role file's own (expandRoleGrants), asked in this order:
//
//   - The names. The grant is not of an action no role is given, and its permission,
//     resource and field are ones the permission collection lists (unlistedName).
//   - The place. The resource is held in a scope, the role's own, and Update is not
//     given on what the release marks immutable (misplacedGrant).
//   - The condition. The text parses, reads only the attributes the resource declares
//     and the subject vocabulary the application declares, and is one the grant's
//     permission can be judged by (validateConditionReads). A field's condition is held
//     to its resource's attributes.
//
// A rule is made for one collection and asked about many grants: it reads the
// collection's List once and remembers what the condition's part answered.
type grantRule struct {
	collection PermissionCollection
	listed     map[accesstypes.Permission][]accesstypes.Resource
	reads      map[conditionUse]error
}

// conditionUse is one use of a condition text: the permission and the resource of the
// grant that carries it. What the release declares for a use is the same for every
// role that holds such a grant.
type conditionUse struct {
	perm     accesstypes.Permission
	resource string
	text     string
}

// rulePart is a part of the rule a grant can break. A write says each part in its own
// sentence (brokenRule.badRequest).
type rulePart uint8

const (
	// noRoleActionPart is a grant of an action no role is given.
	noRoleActionPart rulePart = iota + 1
	// namesPart is a permission, a resource or a field the collection does not list.
	namesPart
	// placePart is a resource held in no scope or in another scope than the role's,
	// and Update on what the release marks immutable.
	placePart
	// conditionPart is a condition that does not parse, or that reads what the release
	// does not declare or cannot judge the grant's permission by.
	conditionPart
)

// brokenRule is why a grant is refused: the part of the rule it breaks and the reason,
// which is the whole of what a load reports the grant skipped for.
type brokenRule struct {
	part   rulePart
	reason error
}

// newGrantRule is the rule as collection declares it.
func newGrantRule(collection PermissionCollection) *grantRule {
	return &grantRule{
		collection: collection,
		listed:     collection.List(),
		reads:      make(map[conditionUse]error),
	}
}

// refusal says what part of the rule g breaks, or nil when it is a grant a role may
// hold. g is a role's grant on a resource: its subject names the role, its scope is
// where the role is held, and its resource is not empty. parse compiles the grant's
// condition; it is the caller's, so a write and a load each say a text that does not
// parse in their own words, and a load compiles a text once.
func (r *grantRule) refusal(g *policy.Grant, parse func(text string) (condition.Expr, error)) *brokenRule {
	name := grantResourceName(g)
	if isNoRoleAction(r.collection, name) {
		return &brokenRule{part: noRoleActionPart, reason: errors.Newf("%s is an action no role is given", name)}
	}
	if err := unlistedName(r.listed, g); err != nil {
		return &brokenRule{part: namesPart, reason: err}
	}

	scope := r.collection.Scope(name)
	if scope == "" {
		return &brokenRule{part: placePart, reason: errors.Newf("resource %s does not require a permission or does not exist", name)}
	}
	declared := accesstypes.DomainPermissionScope
	if g.Scope.IsGlobal() {
		declared = accesstypes.GlobalPermissionScope
	}
	if err := misplacedGrant(r.collection, accesstypes.Role(g.Subject.Name), declared, g.Perm, name, scope); err != nil {
		return &brokenRule{part: placePart, reason: err}
	}

	if g.Condition == "" {
		return nil
	}
	expr, err := parse(g.Condition)
	if err != nil {
		return &brokenRule{part: conditionPart, reason: err}
	}
	use := conditionUse{perm: g.Perm, resource: g.Resource, text: g.Condition}
	err, asked := r.reads[use]
	if !asked {
		err = validateConditionReads(r.collection, g.Perm, accesstypes.Resource(g.Resource), expr)
		r.reads[use] = err
	}
	if err != nil {
		return &brokenRule{part: conditionPart, reason: err}
	}

	return nil
}

// parseAuthoredCondition compiles a condition a person wrote, and says one that does
// not parse as the role file's validation does (accesstypes.NewCondition): the error is
// the parser's own, and the write says it with the role, the permission and the resource
// (brokenRule.badRequest), as the role file does.
func parseAuthoredCondition(text string) (condition.Expr, error) {
	compiled, err := accesstypes.NewCondition(text)
	if err != nil {
		return nil, err //nolint:wrapcheck // wrapped where it is said, with the role, the permission and the resource (grantConditionError)
	}

	return compiled.Expr(), nil
}

// parseStoredCondition compiles a condition the policy store holds, and says one that
// does not parse in the load's words: the sentence the report of a skipped grant carries
// (SkippedGrant) and a role's listing says of the row (RoleGrant.Skipped).
func parseStoredCondition(text string) (condition.Expr, error) {
	expr, err := condition.Parse(text)
	if err != nil {
		return nil, errors.Wrap(err, "the condition does not parse")
	}

	return expr, nil
}

// unusable says why the running release cannot use a grant a load found, or nil when
// it can. stored is whether the grant came from the policy store.
//
// What a role holds on a resource in the store is asked the whole rule (refusal). The
// rest is held to its names, to a condition that parses and, for a grant for the whole
// of a scope, to a condition that reads no row:
//
//   - A grant of a role the release declares was held to the whole rule when the role
//     file was read (expandRoleGrants), and is not asked again.
//   - A grant for the whole of a scope names a permission and nothing else, and the
//     write that adds one is held to the permission (userManager.requireDeclaredPermission).
//   - A grant given straight to a person names no role, so there is no role's scope to
//     hold it to. No store writes one.
func (r *grantRule) unusable(g *policy.Grant, stored bool, parse func(text string) (condition.Expr, error)) error {
	if stored && g.Subject.Kind == policy.SubjectRole && g.Resource != "" {
		if broken := r.refusal(g, parse); broken != nil {
			return broken.reason
		}

		return nil
	}

	if err := unlistedName(r.listed, g); err != nil {
		return err
	}
	if g.Condition == "" {
		return nil
	}
	expr, err := parse(g.Condition)
	if err != nil {
		return err
	}
	if g.Resource == "" && !condition.RowFree(expr) {
		return errors.New("a row-referencing condition on a scope-wide grant: a grant attached to no resource has no row for it to see")
	}

	return nil
}
