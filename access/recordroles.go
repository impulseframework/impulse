package access

import (
	"slices"

	"github.com/impulseframework/impulse/accesstypes"
)

// recordRoles is what a permission collection answers beside PermissionCollection when
// it marks records a role can be held on: a kind of record of a tenant (a project) that
// a person or a group can be given a role on, so that the role's grants count on that
// record and what it reaches, and nowhere else. A collection that does not answer marks
// no record, as one written by hand does. The generated collection answers
// (resource.GeneratedCollection), and UnionCollection answers for its members.
type recordRoles interface {
	// RecordsHoldingRoles lists the resources marked as records a role can be held on,
	// sorted.
	RecordsHoldingRoles() []accesstypes.Resource

	// RecordWords are what a sentence calls one row of the resource and many, in lower
	// case.
	RecordWords(res accesstypes.Resource) (one, many string)

	// RecordReach says how a row of res stands to a record of the kind: it is the record
	// (self), it is under it by foreign keys (under), or the record names it (named).
	RecordReach(record, res accesstypes.Resource) (self, under, named bool)

	// RecordCondition is the condition under which a grant of perm on res counts for a
	// holder of role on a record of the kind, for the people of auth: Create asks it of a
	// row being created, and Update of the row as stored and as the update leaves it. ok
	// is false where the role held there gives nothing on res.
	RecordCondition(auth string, role accesstypes.Role, record, res accesstypes.Resource, perm accesstypes.Permission) (condition string, ok bool)
}

// recordPlaces is what a collection answers when it can spell how a resource stands under
// a record, column by column, for two collections to be compared by (UnionCollection).
type recordPlaces interface {
	// RecordPlace spells how a row of res stands to a record of the kind record: a line for
	// each way the row is under it, in the ways' order, then a line for each name of res
	// by the record, in the names' order. It is nil where res stands apart from the record.
	RecordPlace(record, res accesstypes.Resource) []string
}

// markedRecords lists the records store marks as ones a role can be held on, none for a
// collection that does not answer.
func markedRecords(store PermissionCollection) []accesstypes.Resource {
	marker, ok := store.(recordRoles)
	if !ok {
		return nil
	}

	return marker.RecordsHoldingRoles()
}

// holdsRoles reports whether store marks res as a record a role can be held on.
func holdsRoles(store PermissionCollection, res accesstypes.Resource) bool {
	marker, ok := store.(recordRoles)

	return ok && slices.Contains(marker.RecordsHoldingRoles(), res)
}

// recordWords returns what a sentence calls one row of res and many: the collection's
// words, or the resource's name itself where the collection does not answer or answers
// nothing.
func recordWords(store PermissionCollection, res accesstypes.Resource) (one, many string) {
	if marker, ok := store.(recordRoles); ok {
		one, many = marker.RecordWords(res)
	}
	if one == "" {
		one = string(res)
	}
	if many == "" {
		many = string(res)
	}

	return one, many
}

// recordReach says how a row of res stands to a record of the kind record, as store
// answers it; a collection that does not answer reaches nothing.
func recordReach(store PermissionCollection, record, res accesstypes.Resource) (self, under, named bool) {
	marker, ok := store.(recordRoles)
	if !ok {
		return false, false, false
	}

	return marker.RecordReach(record, res)
}

// recordCondition returns the condition under which a grant of perm on res counts for a
// holder of role on a record of the kind record, for the people of auth, as store answers
// it; a collection that does not answer gives nothing.
func recordCondition(store PermissionCollection, auth string, role accesstypes.Role, record, res accesstypes.Resource, perm accesstypes.Permission) (string, bool) {
	marker, ok := store.(recordRoles)
	if !ok {
		return "", false
	}

	return marker.RecordCondition(auth, role, record, res, perm)
}
