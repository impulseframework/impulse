package access

import (
	"fmt"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
)

// policyFinding is one thing a policy load found in the store that the running
// release cannot use as written. Each kind is an error, so the engine reports
// it through the reload-error hook once per compiled snapshot, and a Warning,
// so the deploy check lists it beside the role file's own warnings.
type policyFinding interface {
	Warning
	error
}

// SkippedGrant reports a grant the policy store holds that the running release
// cannot use: its condition does not parse, a row-referencing condition sits
// on a scope-wide grant, or it names a permission, resource or field the
// release does not declare. The snapshot loaded without the grant, so the
// permission it would grant is denied and the rest of the policy is in force.
// One report per such grant reaches the reload-error hook
// (WithReloadErrorHandler) each time a snapshot compiles; the default hook
// writes it to the log. Test for it with errors.As. The deploy check
// (Client.CheckPolicy) lists the same grant as a Warning.
type SkippedGrant struct {
	Scope accesstypes.PolicyScope
	// Subject is the grant's holder: "role Chief" or "user dana".
	Subject    string
	Permission accesstypes.Permission
	// Resource is the dotted resource name ("Widgets", "Widgets.price"); it is
	// empty for a scope-wide grant.
	Resource  accesstypes.Resource
	Condition string
	Reason    error
}

// Error prints the grant and the reason in one sentence.
func (e *SkippedGrant) Error() string {
	target := "scope-wide"
	if e.Resource != "" {
		target = "on " + string(e.Resource)
	}
	condition := ""
	if e.Condition != "" {
		condition = fmt.Sprintf(" under condition %q", e.Condition)
	}

	return fmt.Sprintf("skipped a grant this release cannot use, so it is denied: %s in scope %s, %s %s%s: %s", e.Subject, e.Scope, e.Permission, target, condition, plainText(e.Reason))
}

// plainText renders an error as the one line a deploy prints: each wrapping
// prefix, outermost first, then the cause, without the source positions the
// error chain carries for the log.
func plainText(err error) string {
	var chain errors.Chain
	if !errors.As(err, &chain) {
		return err.Error()
	}
	parts := make([]string, 0, len(chain)+1)
	for i := len(chain) - 1; i >= 0; i-- {
		if chain[i].Prefix != "" {
			parts = append(parts, chain[i].Prefix)
		}
	}
	if chain[0].Err != nil {
		parts = append(parts, chain[0].Err.Error())
	}

	return strings.Join(parts, ": ")
}

// String renders the finding as the one line the deploy check prints.
func (e *SkippedGrant) String() string {
	return e.Error()
}

func (*SkippedGrant) warning() {}

// Unwrap returns the reason.
func (e *SkippedGrant) Unwrap() error {
	return e.Reason
}

// ShadowedRole reports a custom role in the policy store whose name a default
// role of the running release has in the same kind of scope: a later release
// introduced a default role with the custom role's name. The default wins at
// load, so the custom role's grants are skipped and its members hold the
// default role's grants. One report per such role reaches the reload-error
// hook each time a snapshot compiles, and the deploy check lists it as a
// Warning. Rename or delete the custom role to clear it.
type ShadowedRole struct {
	Scope accesstypes.PolicyScope
	Role  accesstypes.Role
}

// Error prints the role and what the load did with it.
func (e *ShadowedRole) Error() string {
	return fmt.Sprintf("skipped the grants of custom role %s in scope %s: this release has a default role of that name, which wins; rename or delete the custom role", e.Role, e.Scope)
}

// String renders the finding as the one line the deploy check prints.
func (e *ShadowedRole) String() string {
	return e.Error()
}

func (*ShadowedRole) warning() {}

// OrphanedMembership reports memberships in the policy store naming a role
// that is neither a default role of the running release nor a custom role in
// their scope: the members hold nothing from it. A retired default role
// leaves its memberships behind this way until the directory sync or an
// administrator removes them. One report per (scope, role) reaches the
// reload-error hook each time a snapshot compiles, and the deploy check lists
// it as a Warning.
type OrphanedMembership struct {
	Scope accesstypes.PolicyScope
	Role  accesstypes.Role
	// Users are the members, sorted.
	Users []accesstypes.User
}

// Error prints the role, where and by how many it is held, and that it grants
// nothing.
func (e *OrphanedMembership) Error() string {
	return fmt.Sprintf("role %s is held by %d user(s) in scope %s; no release default and no custom role has that name, so they hold nothing from it", e.Role, len(e.Users), e.Scope)
}

// String renders the finding as the one line the deploy check prints.
func (e *OrphanedMembership) String() string {
	return e.Error()
}

func (*OrphanedMembership) warning() {}

// newSkippedGrant builds the report for a grant record.
func newSkippedGrant(g *policy.Grant, reason error) *SkippedGrant {
	return &SkippedGrant{
		Scope:      g.Scope,
		Subject:    subjectLabel(g.Subject),
		Permission: g.Perm,
		Resource:   grantResourceName(g),
		Condition:  g.Condition,
		Reason:     reason,
	}
}

// subjectLabel names a grant's holder for a report.
func subjectLabel(s policy.Subject) string {
	if s.Kind == policy.SubjectRole {
		return "role " + s.Name
	}

	return "user " + s.Name
}

// grantResourceName is the dotted name a grant record addresses: the base
// resource for an endpoint or all-fields grant, base.field for a field grant,
// empty for a scope-wide grant.
func grantResourceName(g *policy.Grant) accesstypes.Resource {
	if g.Resource == "" {
		return ""
	}
	if g.Field == "" || g.Field == "*" {
		return accesstypes.Resource(g.Resource)
	}

	return joinResourceField(g.Resource, g.Field)
}

// unusableName says why a resource grant names a permission, resource or field
// the collection does not declare, or nil when it does. The rule is the role
// file's own: the dotted resource must be listed under the permission. A
// scope-wide grant is not checked: the collection lists the permissions
// resources require, which is not every permission a role may hold.
func unusableName(collection PermissionCollection, g *policy.Grant) error {
	if g.Resource == "" {
		return nil
	}

	resources, known := collection.List()[g.Perm]
	if !known {
		return errors.Newf("this release has no permission %s", g.Perm)
	}
	name := grantResourceName(g)
	if !slices.Contains(resources, name) {
		return errors.Newf("this release declares no %s on %s", g.Perm, name)
	}

	return nil
}

// sortFindings orders findings for a stable report: the skipped grants first
// in their records' order, then the shadowed roles, then the orphaned
// memberships, each group by scope then name.
func sortFindings(findings []policyFinding) {
	slices.SortStableFunc(findings, func(a, b policyFinding) int {
		if c := findingRank(a) - findingRank(b); c != 0 {
			return c
		}

		return strings.Compare(a.String(), b.String())
	})
}

func findingRank(f policyFinding) int {
	switch f.(type) {
	case *SkippedGrant:
		return 0
	case *ShadowedRole:
		return 1
	default:
		return 2
	}
}
