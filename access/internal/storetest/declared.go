package storetest

import (
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
)

// Declaring is the permission collection of a release that declares what list names
// and nothing more: each permission with the resources and fields it is granted on, a
// field as its dotted name. A Client is built over what its release declares, and holds
// every grant it loads or writes to it, so a test hands its Client the names it grants.
// Every resource is of a domain's scope, and the collection declares no condition
// vocabulary.
func Declaring(list map[accesstypes.Permission][]accesstypes.Resource) access.PermissionCollection {
	return declared(list)
}

// suiteCollection declares what the suite grants through its stores, which the Clients it
// builds over them are held to.
var suiteCollection = Declaring(map[accesstypes.Permission][]accesstypes.Resource{
	readPerm:   {employees, employees + "." + nameField, employees + "." + salaryField, widgets, widgets + "." + nameField},
	updatePerm: {widgets, widgets + "." + nameField, widgets + "." + priceField},
	"List":     {widgets},
	exportPerm: nil,
})

// declared is a permission collection that is its list alone.
type declared map[accesstypes.Permission][]accesstypes.Resource

// List implements access.PermissionCollection.
func (d declared) List() map[accesstypes.Permission][]accesstypes.Resource {
	return d
}

// Scope implements access.PermissionCollection.
func (declared) Scope(accesstypes.Resource) accesstypes.PermissionScope {
	return accesstypes.DomainPermissionScope
}

// IsResourceImmutable implements access.PermissionCollection.
func (declared) IsResourceImmutable(accesstypes.PermissionScope, accesstypes.Resource) bool {
	return false
}

// AttributeComparisonType implements access.PermissionCollection.
func (declared) AttributeComparisonType(accesstypes.PermissionScope, accesstypes.Resource, string) (accesstypes.AttributeType, bool) {
	return "", false
}

// AttributeIsColumn implements access.PermissionCollection.
func (declared) AttributeIsColumn(accesstypes.PermissionScope, accesstypes.Resource, string) bool {
	return false
}

// AttributeIsList implements access.PermissionCollection.
func (declared) AttributeIsList(accesstypes.PermissionScope, accesstypes.Resource, string) bool {
	return false
}

// SubjectSetComparisonType implements access.PermissionCollection.
func (declared) SubjectSetComparisonType(string) (accesstypes.AttributeType, bool) {
	return "", false
}

// SubjectSets implements access.PermissionCollection.
func (declared) SubjectSets(string) map[string][]string {
	return nil
}

// SubjectSetAuth implements access.PermissionCollection.
func (declared) SubjectSetAuth(string) string {
	return ""
}

// SubjectValueComparisonType implements access.PermissionCollection.
func (declared) SubjectValueComparisonType(string) (accesstypes.AttributeType, bool) {
	return "", false
}

// IsComputedResource implements access.PermissionCollection.
func (declared) IsComputedResource(accesstypes.PermissionScope, accesstypes.Resource) bool {
	return false
}

// MethodTarget implements access.PermissionCollection.
func (declared) MethodTarget(accesstypes.PermissionScope, accesstypes.Resource) (accesstypes.Resource, bool) {
	return "", false
}

// ConcealingKeys implements access.PermissionCollection.
func (declared) ConcealingKeys(accesstypes.PermissionScope, accesstypes.Resource) (order, keys []accesstypes.Tag) {
	return nil, nil
}

// RequiredOnCreate implements access.PermissionCollection.
func (declared) RequiredOnCreate(accesstypes.PermissionScope, accesstypes.Resource) []accesstypes.Tag {
	return nil
}
