package access

import (
	"fmt"
	"slices"

	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
)

// Groups of people in the compiled policy. A group holds roles the way a person
// does, and a person in a group holds the group's roles wherever the group
// holds them. The stores keep the two facts apart (a group's roles, a group's
// members); the compiler folds them when a snapshot is built, into one
// membership for each person and each role of each group they are in, so a
// check reads a person's grants exactly as it does without groups and costs
// what it costs without them. Nothing of a group survives the fold: when a
// person leaves a group, a group loses a role, or a group's rows are gone, the
// next snapshot simply holds no membership from it.

// SkippedGroupRole reports a role the policy store holds for a group of people
// that the running release does not give the group's members: the group's own
// row is not there, the role is the Super Administrator role, which a person
// holds by a membership of their own alone, or the group is kept in one tenant
// and the role is held outside it. The snapshot loaded without the role, so
// nobody holds it through the group and the rest of the policy is in force.
// One report per such row reaches the reload-error hook
// (WithReloadErrorHandler) each time a snapshot compiles, and the deploy check
// (Client.CheckPolicy) lists it as a Warning. The management surface refuses
// to write such a row, so one is the mark of a row written by hand.
type SkippedGroupRole struct {
	Group accesstypes.Group
	Scope accesstypes.PolicyScope
	Role  accesstypes.Role
	// Reason says why the group's members do not hold the role.
	Reason string
}

// Error prints the group, the role and why its members do not hold it.
func (e *SkippedGroupRole) Error() string {
	return fmt.Sprintf("skipped the role %s that group %s holds in scope %s: %s", e.Role, e.Group, e.Scope, e.Reason)
}

// String renders the finding as the one line the deploy check prints.
func (e *SkippedGroupRole) String() string {
	return e.Error()
}

func (*SkippedGroupRole) warning() {}

// groupRoleRefusal says why a group kept where kept says does not give its
// members role in scope, "" when it does. It is the one statement of what a
// group may hold, asked by the snapshot's fold, by the listing of what a person
// holds (UserRoles) and by every write of a group's role:
//
//   - the Super Administrator role is held by a membership of a person's own
//     alone, so only a super administrator makes another and a group is no way
//     around it;
//   - a group kept in one tenant holds roles in that tenant alone, never in
//     another, in every tenant or for the whole application.
//
// A group kept for the whole application holds roles wherever a person can.
func groupRoleRefusal(kept, scope accesstypes.PolicyScope, role accesstypes.Role) string {
	if role == accesstypes.SuperAdministrator {
		return fmt.Sprintf("the %s role is held by a person's own membership alone, never through a group", role)
	}
	if domain, inDomain := kept.Domain(); inDomain && scope != kept {
		return fmt.Sprintf("the group is kept in the tenant %s and holds roles there alone", domain)
	}

	return ""
}

// foldGroups replaces the roles groups hold by the memberships of the people
// in them: for each role of each group, one membership for each member, held
// where the group holds the role. Memberships of users and role inheritance
// edges pass through as they are. A group's role its members do not hold
// (groupRoleRefusal, or a group whose own row is gone) is left out and
// reported. A person who holds a role of their own and through a group, or
// through two groups, comes out with the membership more than once; the
// compiler keeps a role once per person, so the two neither add up nor cancel.
func foldGroups(records *policy.Records) ([]policy.Membership, []policyFinding) {
	heldByGroup := func(m policy.Membership) bool { return m.Member.Kind == policy.SubjectGroup }
	if !slices.ContainsFunc(records.Memberships, heldByGroup) {
		// No group holds a role: the memberships are the people's own, as they are.
		return records.Memberships, nil
	}

	kept := make(map[accesstypes.Group]accesstypes.PolicyScope, len(records.Groups))
	for _, group := range records.Groups {
		kept[group.ID] = group.Scope
	}
	members := make(map[accesstypes.Group][]accesstypes.User)
	for _, member := range records.GroupMembers {
		members[member.Group] = append(members[member.Group], member.User)
	}

	folded := make([]policy.Membership, 0, len(records.Memberships))
	var findings []policyFinding
	for _, m := range records.Memberships {
		if !heldByGroup(m) {
			folded = append(folded, m)

			continue
		}
		group := accesstypes.Group(m.Member.Name)
		where, exists := kept[group]
		reason := "the group's own row is not there"
		if exists {
			reason = groupRoleRefusal(where, m.Scope, m.Role)
		}
		if reason != "" {
			findings = append(findings, &SkippedGroupRole{Group: group, Scope: m.Scope, Role: m.Role, Reason: reason})

			continue
		}
		for _, user := range members[group] {
			folded = append(folded, policy.Membership{Scope: m.Scope, Member: policy.Subject{Kind: policy.SubjectUser, Name: string(user)}, Role: m.Role})
		}
	}

	return folded, findings
}
