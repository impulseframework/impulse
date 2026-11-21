package access

import (
	"slices"

	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
)

// declaring is the permission collection of a release that declares the resources listed
// (a field as Resource.field) under their permissions, and nothing else: what a test that
// writes its own policy hands the Client or the engine it builds, which cannot be built
// without what its release declares. Every other answer is stubCollection's for a
// collection that was told nothing.
func declaring(list map[accesstypes.Permission][]accesstypes.Resource) PermissionCollection {
	return &stubCollection{list: list}
}

// declaringRecords is the permission collection of a release that declares every name the
// policy's grants carry: what a test of the snapshot's compilation, which is handed its
// policy whole and pins nothing of the names, compiles the policy against.
func declaringRecords(records *policy.Records) PermissionCollection {
	list := make(map[accesstypes.Permission][]accesstypes.Resource)
	for i := range records.Grants {
		g := &records.Grants[i]
		if name := grantResourceName(g); name != "" && !slices.Contains(list[g.Perm], name) {
			list[g.Perm] = append(list[g.Perm], name)
		}
	}

	return declaring(list)
}

// fixtureCollection is what the release of the engine's and the user manager's tests
// declares: every name those tests put in a policy, so a test of how the engine loads,
// reloads and answers reads the policy it wrote. What the library does with a name the
// release does not declare is pinned where a test hands over a collection of its own
// (Test_newSnapshot_skipsGrantsTheReleaseCannotUse,
// TestUserManager_grantsHeldToTheCollection).
var fixtureCollection = declaring(map[accesstypes.Permission][]accesstypes.Resource{
	"Read":   {"employees", "employees.name", "widgets", "widgets.name"},
	"Update": {"employees", "employees.name", "employees.email", "employees.salary", "widgets", "widgets.name"},
	"List":   {"gadgets", "reports", "widgets"},
	// The permissions a role is given for the whole of a scope, attached to no resource.
	"Export":      nil,
	"ViewReports": nil,
	"ViewUsers":   nil,
})
