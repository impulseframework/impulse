package resource

import (
	"fmt"
	"reflect"

	"github.com/impulseframework/impulse/accesstypes"
)

// nameOfTagKey is the struct tag a generated request type marks a name field with. Its
// value is the id field the name belongs to (nameof:"SquadronID").
const nameOfTagKey = "nameof"

// NameField describes one name field of a resource: the name of the row an id field of
// the resource points to, which the generator declares beside the id wherever the
// pointed-to resource says what its rows are known by. A name field is read-only and
// stored nowhere. It is no column of the resource, so it carries no grant of its own
// and no statement names it as a column: the name is looked up in the pointed-to
// table by the id.
type NameField struct {
	// Field is the name field as the generated request types spell it, and Key the id
	// field it stands beside.
	Field accesstypes.Field
	Key   accesstypes.Field
	// Resource is the pointed-to resource and Tag its name field: reading a name takes
	// the List grant on that field as well as the grant on the id. Both are empty for
	// an enumeration table, whose names every page carries already.
	Resource accesstypes.Resource
	Tag      accesstypes.Tag
	// Table is the pointed-to table, KeyColumn the column the id matches there, and
	// NameColumn the column holding the name.
	Table      string
	KeyColumn  string
	NameColumn string
	// TenantColumn is the pointed-to table's tenant column, which a lookup holds to the
	// request's tenant; empty where the pointed-to resource belongs to no tenant.
	TenantColumn string
}

// GoLiteral renders the name field as the body of a Go composite literal, the form the
// generator writes one in.
func (n *NameField) GoLiteral() string {
	return fmt.Sprintf("{Field: %q, Key: %q, Resource: %q, Tag: %q, Table: %q, KeyColumn: %q, NameColumn: %q, TenantColumn: %q}",
		n.Field, n.Key, n.Resource, n.Tag, n.Table, n.KeyColumn, n.NameColumn, n.TenantColumn)
}

// isNameField reports whether a request type's field is a name field: one the
// generator declared beside an id, marked by its nameof tag. Such a field is not a
// column of the resource, so the paths that read a request type's fields as columns
// (the field mapper, the permission registration) leave it out.
func isNameField(tag reflect.StructTag) bool {
	_, ok := tag.Lookup(nameOfTagKey)

	return ok
}
