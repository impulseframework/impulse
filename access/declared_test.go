package access

import (
	"slices"
	"testing"

	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/accesstypes/condition"
	"github.com/impulseframework/impulse/resource"
)

// generated is the permission collection of a release as the generator writes one:
// resource.NewGeneratedCollection over the data a generated collection file holds. A
// test that holds a rule to what a release declares hands the engine this, so the rule
// is proven against the answers a generated application gives, a permission declared
// with no resource among them (CollectionData.Permissions).
func generated(t *testing.T, data *resource.CollectionData) PermissionCollection {
	t.Helper()

	collection, err := resource.NewGeneratedCollection(*data)
	if err != nil {
		t.Fatalf("resource.NewGeneratedCollection() error = %v", err)
	}

	return collection
}

// unionOf is the permission collection of a release of several sites: UnionCollection
// over each site's collection.
func unionOf(t *testing.T, collections ...PermissionCollection) PermissionCollection {
	t.Helper()

	union, err := UnionCollection(collections...)
	if err != nil {
		t.Fatalf("UnionCollection() error = %v", err)
	}

	return union
}

// declaring is the permission collection of a release that declares the resources listed
// (a field as Resource.field) under their permissions, and nothing else: what a test that
// writes its own policy hands the Client or the engine it builds, which cannot be built
// without what its release declares. Every other answer is stubCollection's for a
// collection that was told nothing.
func declaring(list map[accesstypes.Permission][]accesstypes.Resource) PermissionCollection {
	return &stubCollection{list: list}
}

// declaringRecords is the permission collection of the release a policy was written
// under: what a test of the snapshot's compilation, which is handed its policy whole and
// pins nothing of the names, compiles the policy against. It declares every name the
// policy's grants carry, the permission of a grant that names no resource among them,
// and what a role's grant on a resource leans on, since a load holds such a grant to the
// whole of the rule a write of it is held to: a resource a role held globally is given is
// a global one and every other a domain's, and an attribute a grant's condition reads is
// one the grant's resource declares (fixtureAttributeType).
func declaringRecords(records *policy.Records) PermissionCollection {
	release := &stubCollection{
		list:       make(map[accesstypes.Permission][]accesstypes.Resource),
		scopes:     make(map[accesstypes.Resource]accesstypes.PermissionScope),
		attributes: make(map[string]accesstypes.AttributeType),
	}
	for i := range records.Grants {
		g := &records.Grants[i]
		if _, has := release.list[g.Perm]; !has {
			release.list[g.Perm] = nil
		}
		name := grantResourceName(g)
		if name == "" {
			continue
		}
		if !slices.Contains(release.list[g.Perm], name) {
			release.list[g.Perm] = append(release.list[g.Perm], name)
		}
		if g.Subject.Kind == policy.SubjectRole && g.Scope.IsGlobal() {
			release.scopes[name] = accesstypes.GlobalPermissionScope
			release.scopes[accesstypes.Resource(g.Resource)] = accesstypes.GlobalPermissionScope
		}
		expr, err := condition.Parse(g.Condition)
		if g.Condition == "" || err != nil {
			continue
		}
		for _, attribute := range condition.Bindings(expr) {
			release.attributes[g.Resource+"."+attribute] = fixtureAttributeType(attribute)
		}
	}

	return release
}

// fixtureAttributeType is the comparison type of an attribute the conditions of the
// snapshot's tests read: price is a number and unpublished a boolean, as those
// conditions compare them, and every other attribute is a string.
func fixtureAttributeType(attribute string) accesstypes.AttributeType {
	switch attribute {
	case "price":
		return accesstypes.AttributeTypeNumber
	case "unpublished":
		return accesstypes.AttributeTypeBool
	default:
		return accesstypes.AttributeTypeString
	}
}

// fixtureCollection is what the release of the engine's and the user manager's tests
// declares: every name those tests put in a policy, so a test of how the engine loads,
// reloads and answers reads the policy it wrote. A write to a custom role is held to the
// role file's whole rule, so the collection also says what those tests' writes lean on:
// reports is the one global resource, which the tests give a role held globally, and
// owner, state, status and region are the attributes their conditions read, in the
// policies they write through the user manager and in the ones they seed in the store,
// which a load holds to the same rule. What the library does with
// a name the release does not declare is pinned where a test hands over a collection of
// its own (Test_newSnapshot_skipsGrantsTheReleaseCannotUse,
// TestUserManager_grantsHeldToTheCollection), and what a write does with a grant of
// another scope or a condition that reads nothing declared in
// TestUserManager_grantsHeldToTheRoleFilesRule.
var fixtureCollection PermissionCollection = &stubCollection{
	list: map[accesstypes.Permission][]accesstypes.Resource{
		"Read":   {"employees", "employees.name", "widgets", "widgets.name"},
		"Update": {"employees", "employees.name", "employees.email", "employees.salary", "widgets", "widgets.name"},
		"List":   {"gadgets", "reports", "widgets"},
		// The permissions a role is given for the whole of a scope, attached to no resource.
		"Approve":     nil,
		"Export":      nil,
		"ViewReports": nil,
		"ViewUsers":   nil,
	},
	scopes: map[accesstypes.Resource]accesstypes.PermissionScope{"reports": accesstypes.GlobalPermissionScope},
	attributes: map[string]accesstypes.AttributeType{
		"employees.owner":  accesstypes.AttributeTypeString,
		"employees.state":  accesstypes.AttributeTypeString,
		"employees.status": accesstypes.AttributeTypeString,
		"widgets.owner":    accesstypes.AttributeTypeString,
		"widgets.region":   accesstypes.AttributeTypeString,
	},
}
