package access

import (
	"maps"
	"slices"

	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
)

// Roles held on one record of a tenant in the compiled policy. A role the role file
// marks as held on a kind of record (Role.HeldOn) becomes, inside the snapshot, a role
// of its own for each kind it names, named role@kind: the role's grants on what a
// record of the kind reaches, each under one more condition, the one the permission
// collection answers for the role on the record (recordCondition), and none of its
// grants on anything else. A membership held on a record becomes a membership of that
// role in the record's tenant, a group's once for each person in the group. From there
// the compiler reads them as any role's and any membership's, so a check, the digest and
// UserHasGrants answer for a record's holder as they answer for anyone, and a person who
// holds the role in the tenant as well holds the tenant's unconditional grants. The @ is
// outside the role name grammar, so no role a file declares or a store keeps can carry
// the name, and no listing of roles or of what a person holds shows one.

// heldRoleName is the name the snapshot holds a role held on a record of the kind
// record under.
func heldRoleName(role accesstypes.Role, record accesstypes.Resource) accesstypes.Role {
	return accesstypes.Role(string(role) + "@" + string(record))
}

// compileRecordRoles compiles the roles held on records: the grants of the snapshot's own roles
// for them, and the memberships the records' rows give, with the rows that give nothing
// reported.
func compileRecordRoles(records *policy.Records, defaults *defaultRoles, collection PermissionCollection) ([]policy.Grant, []policy.Membership, []policyFinding) {
	grants := recordRoleGrants(defaults, collection)
	memberships, findings := recordRoleMemberships(records, defaults)

	return grants, memberships, findings
}

// recordRoleGrants renders, for each domain role held on a kind of record, the role's
// grants as the role held there gives them, held in every domain: each grant on a
// resource the record reaches, or an Execute grant on a method whose target it reaches,
// under the condition the collection answers for the grant's permission, joined by AND to
// the grant's own. A grant the record does not reach, one for the whole of a scope and one
// on a method with no target are left out.
func recordRoleGrants(defaults *defaultRoles, collection PermissionCollection) []policy.Grant {
	every := accesstypes.EveryDomainPolicyScope()
	auth := defaults.authName()
	var grants []policy.Grant
	for _, role := range slices.Sorted(maps.Keys(defaults.kind(every))) {
		set, _ := defaults.grantsOf(every, role)
		for _, record := range defaults.heldOn(role) {
			given := grantRecords(every, heldRoleName(role, record), set)
			for i := range given {
				g := given[i]
				if g.Resource == "" {
					continue
				}
				target := accesstypes.Resource(g.Resource)
				if g.Perm == accesstypes.Execute {
					method, targeted := collection.MethodTarget(accesstypes.DomainPermissionScope, target)
					if !targeted {
						continue
					}
					target = method
				}
				held, reached := recordCondition(collection, auth, role, record, target, g.Perm)
				if !reached {
					continue
				}
				if g.Condition != "" {
					held = "(" + g.Condition + ") AND (" + held + ")"
				}
				g.Condition = held
				grants = append(grants, g)
			}
		}
	}

	return grants
}

// The reasons a row of a role held on a record gives nothing.
const (
	recordRoleNotHeldThere = "the role is not one held on a record of this kind"
	recordGroupGone        = "the group's own row is not there"
	recordGroupElsewhere   = "the group is not kept in the record's tenant, and a role on a record is held by a person or by a group of that tenant"
)

// recordRoleMemberships turns each role held on a record into a membership of the role
// the snapshot holds for it, in the record's tenant, with its period: a person's as it
// is, and a group's once for each person in the group, counting while the person's place
// and the group's role both count. A row of a role the role file does not mark for the
// record's kind, a group's row whose group is gone, and a group's row of a group kept
// anywhere but the record's tenant give nothing and are reported.
func recordRoleMemberships(records *policy.Records, defaults *defaultRoles) ([]policy.Membership, []policyFinding) {
	if len(records.RecordMemberships) == 0 {
		return nil, nil
	}
	kept := make(map[accesstypes.Group]accesstypes.PolicyScope, len(records.Groups))
	for _, group := range records.Groups {
		kept[group.ID] = group.Scope
	}
	members := make(map[accesstypes.Group][]policy.GroupMember)
	for _, member := range records.GroupMembers {
		members[member.Group] = append(members[member.Group], member)
	}

	var memberships []policy.Membership
	var findings []policyFinding
	for i := range records.RecordMemberships {
		m := &records.RecordMemberships[i]
		skip := func(reason string) {
			findings = append(findings, &SkippedRecordRole{Scope: m.Scope, Role: m.Role, Member: m.Member.Name, Group: m.Member.Kind == policy.SubjectGroup, Reason: reason})
		}
		if !slices.Contains(defaults.heldOn(m.Role), m.Scope.Resource()) {
			skip(recordRoleNotHeldThere)

			continue
		}
		tenant := accesstypes.DomainPolicyScope(m.Scope.Domain())
		role := heldRoleName(m.Role, m.Scope.Resource())
		switch m.Member.Kind {
		case policy.SubjectUser:
			memberships = append(memberships, policy.Membership{Scope: tenant, Member: m.Member, Role: role, Period: m.Period})
		case policy.SubjectGroup:
			group := accesstypes.Group(m.Member.Name)
			where, exists := kept[group]
			switch {
			case !exists:
				skip(recordGroupGone)
			case where != tenant:
				skip(recordGroupElsewhere)
			default:
				for _, member := range members[group] {
					memberships = append(memberships, policy.Membership{
						Scope:  tenant,
						Member: policy.Subject{Kind: policy.SubjectUser, Name: string(member.User)},
						Role:   role,
						Period: m.Period.Intersect(member.Period),
					})
				}
			}
		default:
			// A role on a record is held by a person or by a group: no store reads another holder.
		}
	}

	return memberships, findings
}
