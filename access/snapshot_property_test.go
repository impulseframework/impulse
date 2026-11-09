package access

// The randomized companion of Test_snapshot_zeroConditionsMatchesRBAC (design
// plan §11): over seeded random condition-free policies — grants of every
// shape the store can hold (scope-wide, base, field, all-fields, user-direct)
// and memberships of both kinds (user to role, role to role) across several
// scopes — the decision path grants exactly where the pre-ABAC unconditional
// rules grant, for users and roles alike, scope-wide and per resource, and
// nothing is ever Conditional. The fixed sweep pins one hand-written policy;
// this one pins the invariant over the policy space. The generator is seeded,
// so a failure reproduces and prints its policy.

import (
	"math/rand/v2"
	"testing"
	"time"

	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
)

// The policy generator's vocabulary.
var (
	randomPolicyUsers     = []string{"ada", "bo", "cy", "dee"}
	randomPolicyRoles     = []accesstypes.Role{"Editor", "Auditor", "Chief", "Intern"}
	randomPolicyScopes    = []accesstypes.Scope{tenant1Scope, tenant2Scope, accesstypes.GlobalScope()}
	randomPolicyPerms     = []accesstypes.Permission{"Read", "List", "Create", "Fly"}
	randomPolicyResources = []string{"employees", "documents", "widgets", "budgets"}
	randomPolicyFields    = []string{"name", "salary", "title"}
)

// genConditionFreePolicy draws one condition-free policy: up to two dozen
// grants — nine in ten attached to a resource as an endpoint, all-fields, or
// single-field grant, the rest scope-wide — for roles and, one in four, users
// directly; and up to ten memberships, one in four a role inheriting another
// (cycles included, which the compiler tolerates).
func genConditionFreePolicy(rng *rand.Rand) *policy.Records {
	records := &policy.Records{}

	for range rng.IntN(25) {
		grant := policy.Grant{
			Scope:   pickOne(rng, randomPolicyScopes).PolicyScope(),
			Perm:    pickOne(rng, randomPolicyPerms),
			Subject: roleSubject(string(pickOne(rng, randomPolicyRoles))),
		}
		if rng.IntN(4) == 0 {
			grant.Subject = userSubject(pickOne(rng, randomPolicyUsers))
		}
		if rng.IntN(10) > 0 {
			grant.Resource = pickOne(rng, randomPolicyResources)
			switch rng.IntN(3) {
			case 0:
				// An endpoint grant on the resource itself.
			case 1:
				grant.Field = "*"
			default:
				grant.Field = pickOne(rng, randomPolicyFields)
			}
		}
		records.Grants = append(records.Grants, grant)
	}

	for range rng.IntN(10) {
		membership := policy.Membership{
			Scope:  pickOne(rng, randomPolicyScopes).PolicyScope(),
			Member: userSubject(pickOne(rng, randomPolicyUsers)),
			Role:   pickOne(rng, randomPolicyRoles),
		}
		if rng.IntN(4) == 0 {
			membership.Member = roleSubject(string(pickOne(rng, randomPolicyRoles)))
		}
		records.Memberships = append(records.Memberships, membership)
	}

	return records
}

func pickOne[T any](rng *rand.Rand, values []T) T {
	return values[rng.IntN(len(values))]
}

// Test_snapshot_zeroConditionsMatchesRBAC_random sweeps every random policy
// over every user and role the vocabulary names plus a stranger, every scope,
// every permission plus an unknown one, and every resource shape — base,
// known field, unknown field, all-fields, and an unknown resource — against
// the rbac oracle.
func Test_snapshot_zeroConditionsMatchesRBAC_random(t *testing.T) {
	t.Parallel()

	users := []accesstypes.User{"ada", "bo", "cy", "dee", "stranger"}
	roles := append(append([]accesstypes.Role{}, randomPolicyRoles...), "Ghost")
	perms := append(append([]accesstypes.Permission{}, randomPolicyPerms...), "Unknown")
	resources := make([]accesstypes.Resource, 0, 2+(3+len(randomPolicyFields))*len(randomPolicyResources))
	resources = append(resources, "spaceships", "spaceships.crew")
	for _, base := range randomPolicyResources {
		resources = append(resources, accesstypes.Resource(base), accesstypes.Resource(base+".*"), accesstypes.Resource(base+".unknown"))
		for _, field := range randomPolicyFields {
			resources = append(resources, accesstypes.Resource(base+"."+field))
		}
	}

	rng := rand.New(rand.NewPCG(20260911, 13))
	for i := range 300 {
		records := genConditionFreePolicy(rng)
		snap, _, err := newSnapshot(records, nil, nil, time.Now())
		if err != nil {
			t.Fatalf("case %d: newSnapshot() error = %v\npolicy: %+v", i, err, records)
		}

		for _, scope := range randomPolicyScopes {
			for _, perm := range perms {
				for _, user := range users {
					assertMatchesRBACOracle(t, snap, string(user), snap.userGrants(scope, user), scope, perm, resources,
						snap.checkUser(user, scope, perm), snap.decideUserResources(user, scope, perm, resources...))
				}
				for _, role := range roles {
					assertMatchesRBACOracle(t, snap, string(role), snap.roleGrants(scope, role), scope, perm, resources,
						snap.checkRole(role, scope, perm), snap.decideRoleResources(role, scope, perm, resources...))
				}
			}
		}
		if t.Failed() {
			t.Fatalf("case %d diverged from the rbac oracle\npolicy: %+v", i, records)
		}
	}
}
