// record_roles.go is what a collection says about the records a role can be held on
// (@holdsRoles): which resources are such records, what is under each by foreign keys and
// what each names, and where each auth keeps the roles held on them. The generator writes
// it as data; the permission engine reads the answers (RecordsHoldingRoles, RecordWords,
// RecordReach) through the optional interface it asks a collection for.

package resource

import (
	"cmp"
	"maps"
	"slices"

	"github.com/impulseframework/impulse/accesstypes"
)

// RecordData marks a resource as a record a role can be held on.
type RecordData struct {
	// Key is the key's one column.
	Key string

	// One is what a sentence calls one row, in lower case ("project"), and Many what it
	// calls many ("projects").
	One  string
	Many string

	// Names are the rows a record names, in the order of the record's columns' names.
	Names []RecordNameData
}

// clone is a copy of the record's data that shares no slice with it.
func (d *RecordData) clone() *RecordData {
	copied := *d
	copied.Names = slices.Clone(d.Names)

	return &copied
}

// RecordNameData is one foreign key of a record to a row it names.
type RecordNameData struct {
	// Resource is the resource named (Clients), and Column the record's column that
	// names it (ClientId).
	Resource accesstypes.Resource
	Column   string

	// Attribute is the attribute of Resource the test reads, its referenced column.
	Attribute string

	// One and Many are the named resource's words ("client", "clients").
	One  string
	Many string
}

// RecordWayData is one way a row of a resource is under a record.
type RecordWayData struct {
	// Record is the record the row is under (Projects).
	Record accesstypes.Resource

	// Attribute is the resource's attribute that resolves to the record's key by this way.
	Attribute string

	// Self marks the way of the record's own rows: the row is the record itself.
	Self bool
}

// RecordRoleStoreData is where one auth keeps the roles held on records.
type RecordRoleStoreData struct {
	// Auth is the auth, as its package names it.
	Auth string

	// UserRoles is the table of people's roles, and GroupRoles the table of groups' roles,
	// "" where the auth's management has no groups.
	UserRoles  string
	GroupRoles string

	// GroupSet is the subject set of a person's groups, "" with GroupRoles.
	GroupSet string
}

// setResourceHoldsRoles marks res within scope as a record a role can be held on, replacing
// an earlier mark; a nil record takes the mark away.
func (g *GeneratedCollection) setResourceHoldsRoles(scope accesstypes.PermissionScope, res accesstypes.Resource, record *RecordData) {
	if record == nil {
		delete(g.holdsRoles[scope], res)

		return
	}
	if g.holdsRoles[scope] == nil {
		g.holdsRoles[scope] = make(map[accesstypes.Resource]*RecordData)
	}
	g.holdsRoles[scope][res] = record.clone()
}

// setResourceUnder records the ways the rows of res within scope are under the records,
// replacing earlier ones; none takes them away.
func (g *GeneratedCollection) setResourceUnder(scope accesstypes.PermissionScope, res accesstypes.Resource, ways []RecordWayData) {
	if len(ways) == 0 {
		delete(g.under[scope], res)

		return
	}
	if g.under[scope] == nil {
		g.under[scope] = make(map[accesstypes.Resource][]RecordWayData)
	}
	g.under[scope][res] = sortedWays(ways)
}

// sortedWays is a copy of ways sorted by the record they lead to, each record's ways kept
// in the order given: a way's attribute is numbered by that order.
func sortedWays(ways []RecordWayData) []RecordWayData {
	sorted := slices.Clone(ways)
	slices.SortStableFunc(sorted, func(a, b RecordWayData) int {
		return cmp.Compare(a.Record, b.Record)
	})

	return sorted
}

// addRecordRoleStore records where an auth keeps the roles held on records, replacing an
// earlier store of the same auth, the stores kept sorted by auth.
func (g *GeneratedCollection) addRecordRoleStore(store RecordRoleStoreData) {
	g.recordRoleStores = slices.DeleteFunc(g.recordRoleStores, func(kept RecordRoleStoreData) bool {
		return kept.Auth == store.Auth
	})
	g.recordRoleStores = append(g.recordRoleStores, store)
	slices.SortFunc(g.recordRoleStores, func(a, b RecordRoleStoreData) int {
		return cmp.Compare(a.Auth, b.Auth)
	})
}

// heldRecord is the mark of the record res, whatever its scope; ok is false for a
// resource that is no record a role can be held on.
func (g *GeneratedCollection) heldRecord(res accesstypes.Resource) (*RecordData, bool) {
	for _, records := range g.holdsRoles {
		if record, ok := records[res]; ok {
			return record, true
		}
	}

	return nil, false
}

// RecordsHoldingRoles lists the resources marked as records a role can be held on
// (CollectionResource.HoldsRoles), sorted. A nil collection marks none.
func (g *GeneratedCollection) RecordsHoldingRoles() []accesstypes.Resource {
	if g == nil {
		return nil
	}
	var records []accesstypes.Resource
	for _, marked := range g.holdsRoles {
		records = slices.AppendSeq(records, maps.Keys(marked))
	}
	slices.Sort(records)

	return records
}

// RecordWords are what a sentence calls one row of res and many, in lower case: a
// marked record's words, else the words a record gives the resource it names, else ""
// twice.
func (g *GeneratedCollection) RecordWords(res accesstypes.Resource) (one, many string) {
	if g == nil {
		return "", ""
	}
	if record, ok := g.heldRecord(res); ok {
		return record.One, record.Many
	}
	for _, marked := range g.RecordsHoldingRoles() {
		record, _ := g.heldRecord(marked)
		for _, name := range record.Names {
			if name.Resource == res {
				return name.One, name.Many
			}
		}
	}

	return "", ""
}

// RecordReach says how a row of res stands to a record of the kind record: it is the
// record itself (self, the record being marked), it is under it by a way that is not the
// record's own (under), or the record names it (named). A resource apart is none of the
// three.
func (g *GeneratedCollection) RecordReach(record, res accesstypes.Resource) (self, under, named bool) {
	if g == nil {
		return false, false, false
	}
	data, marked := g.heldRecord(record)
	self = marked && res == record
	for _, ways := range g.under {
		for _, way := range ways[res] {
			if way.Record == record && !way.Self {
				under = true
			}
		}
	}
	if marked {
		named = slices.ContainsFunc(data.Names, func(name RecordNameData) bool {
			return name.Resource == res
		})
	}

	return self, under, named
}
