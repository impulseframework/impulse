package resource

import (
	"fmt"
	"maps"
	"reflect"
	"strings"

	"github.com/impulseframework/impulse/accesstypes"
)

// nameOfTagKey is the struct tag a generated request type marks a name field with. Its
// value is the id field the name belongs to (nameof:"SquadronID").
const nameOfTagKey = "nameof"

// NameField describes one name field of a resource: the name of the row an id field of
// the resource points to, which the generator declares beside the id wherever the
// pointed-to resource says what its rows are known by. A name field is read-only and
// stored nowhere. It is no column of the resource, so no statement names it as a
// column: the name is looked up in the pointed-to table by the id. Reading it takes two
// grants: the request's own grant on the id field, and the List grant on the
// pointed-to resource's name field (Resource and Tag).
type NameField struct {
	// Field is the name field as the generated request types spell it, and Key the id
	// field it stands beside.
	Field accesstypes.Field
	Key   accesstypes.Field
	// Resource is the pointed-to resource and Tag its name field: reading a name takes
	// the List grant on that field as well as the grant on the id, asked in the
	// request's tenant where the resource belongs to one (TenantColumn) and globally
	// where it does not; Tag is empty where the name is the resource's key, whose
	// grant is the resource's own. Both are empty for an enumeration table, whose
	// names every page carries already.
	Resource accesstypes.Resource
	Tag      accesstypes.Tag
	// Table is the pointed-to table, KeyColumn the column the id matches there, and
	// NameColumn the column holding the name.
	Table      string
	KeyColumn  string
	NameColumn string
	// View says the pointed-to resource is a view: Table is its resource's name, and a
	// statement reads the view's query in the table's place, with the view's
	// parameters, from the views the decoder was handed (WithNameViews).
	View bool
	// TenantColumn is the pointed-to table's tenant column, which a lookup holds to the
	// request's tenant; empty where the pointed-to resource belongs to no tenant. Where
	// that resource reaches its tenant through other tables, TenantColumn is the column
	// its way leaves through and TenantPath the way, each hop as the resource's own
	// tenant binding walks it.
	TenantColumn string
	TenantPath   []BindingHop
	// Features are the feature flags the name stands behind: the pointed-to
	// resource's, then its name field's, each only where it is declared; empty where
	// neither is gated, and for an enumeration table. While any of them is off the
	// name is unknown to a request, as a name beside a hidden id is.
	Features []Feature
}

// GoLiteral renders the name field as the body of a Go composite literal, the form the
// generator writes one in.
func (n *NameField) GoLiteral() string {
	var path strings.Builder
	if len(n.TenantPath) > 0 {
		path.WriteString(", TenantPath: []resource.BindingHop{")
		for i, hop := range n.TenantPath {
			if i > 0 {
				path.WriteString(", ")
			}
			fmt.Fprintf(&path, "{Table: %q, JoinColumn: %q, Column: %q}", hop.Table, hop.JoinColumn, hop.Column)
		}
		path.WriteString("}")
	}
	if len(n.Features) > 0 {
		path.WriteString(featuresLiteral(n.Features))
	}

	view := ""
	if n.View {
		view = ", View: true"
	}

	return fmt.Sprintf("{Field: %q, Key: %q, Resource: %q, Tag: %q, Table: %q, KeyColumn: %q, NameColumn: %q%s, TenantColumn: %q%s}",
		n.Field, n.Key, n.Resource, n.Tag, n.Table, n.KeyColumn, n.NameColumn, view, n.TenantColumn, path.String())
}

// NameViews maps a view's resource to the view, for the decoders whose names are read
// from views (WithNameViews).
type NameViews map[accesstypes.Resource]View

// MergeNameViews returns one map holding every entry of its arguments, a later
// argument's entry replacing an earlier one's.
func MergeNameViews(sets ...NameViews) NameViews {
	merged := make(NameViews)
	for _, set := range sets {
		maps.Copy(merged, set)
	}

	return merged
}

// featuresLiteral renders flags as the Features element of a Go composite literal,
// with its leading separator: , Features: []resource.Feature{"a", "b"}.
func featuresLiteral(features []Feature) string {
	quoted := make([]string, 0, len(features))
	for _, feature := range features {
		quoted = append(quoted, fmt.Sprintf("%q", feature))
	}

	return ", Features: []resource.Feature{" + strings.Join(quoted, ", ") + "}"
}

// isNameField reports whether a request type's field is a name field: one the
// generator declared beside an id, marked by its nameof tag. Such a field is not a
// column of the resource, so the paths that read a request type's fields as columns
// (the field mapper, the permission registration) leave it out.
func isNameField(tag reflect.StructTag) bool {
	_, ok := tag.Lookup(nameOfTagKey)

	return ok
}
