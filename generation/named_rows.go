package generation

import (
	"fmt"
	"slices"
	"strings"

	"github.com/impulseframework/impulse/resource"
)

// namedRow is one column of a resource that names a row of another resource the
// application lists: a column whose picker lists that resource's table. A write sets it
// only to a row its caller can list, and the library holds it to that (resource.NamedRow);
// the generated resource declares each such column for it (NamedRows).
type namedRow struct {
	// Field is the column that names the row.
	Field *resourceField
	// Resource is the named row's resource, by its plural name, Table its table and
	// KeyColumn the table's column the column's value matches.
	Resource  string
	Table     string
	KeyColumn string
	// TenantColumn and TenantPath say where the named row's tenant is read, as a name
	// field's do; both are empty for a resource that belongs to no tenant.
	TenantColumn string
	TenantPath   []resource.BindingHop
}

// Literal renders the column as the resource.NamedRow the generated NamedRows method
// returns.
func (n *namedRow) Literal() string {
	var b strings.Builder
	fmt.Fprintf(&b, "{Field: %q, Resource: %q, Table: %q, KeyColumn: %q", n.Field.Name(), n.Resource, n.Table, n.KeyColumn)
	if n.TenantColumn != "" {
		fmt.Fprintf(&b, ", TenantColumn: %q", n.TenantColumn)
	}
	if len(n.TenantPath) > 0 {
		b.WriteString(", TenantPath: []resource.BindingHop{")
		for i, hop := range n.TenantPath {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "{Table: %q, JoinColumn: %q, Column: %q}", hop.Table, hop.JoinColumn, hop.Column)
		}
		b.WriteString("}")
	}
	b.WriteString("}")

	return b.String()
}

// resolveNamedRows finds, on every resource that is a table's, the columns that name a
// row of another resource the application lists (namedRowOf). It runs once the
// enumerations are resolved, where the name fields are found.
func (r *resourceGenerator) resolveNamedRows(resources []*resourceInfo) {
	byName := make(map[string]*resourceInfo, len(resources))
	for _, res := range resources {
		byName[r.pluralize(res.Name())] = res
	}
	for _, res := range resources {
		res.namedRows = nil
		if res.IsVirtual {
			// A view's rows are read, never written.
			continue
		}
		for _, field := range res.Fields {
			if row := r.namedRowOf(res, field, byName); row != nil {
				res.namedRows = append(res.namedRows, row)
			}
		}
	}
}

// namedRowOf is the declaration of field as a column that names a row of another
// resource the application lists, nil where it is none. A column is one where its
// picker's source (pointedToResource: the table its foreign key references, or the one
// a field-scope @enumerate names) is a resource of the run that is a table's, has one
// key column the column matches (pointedToKeyColumn) and registers a List. Not declared:
// a column whose source is an enumeration table, which everyone picks from under no
// grant; a table the run does not list, which has no picker and nothing to read it by; a
// view or a computed list; the column that holds the row's own tenant key, which the
// request stamps; a column the server never stores (input only); and, on a resource that
// belongs to no tenant, a column naming a tenant's row, which no request of one tenant
// writes.
func (r *resourceGenerator) namedRowOf(res *resourceInfo, field *resourceField, byName map[string]*resourceInfo) *namedRow {
	target, ok := pointedToResource(field)
	if !ok || field.IsInputOnly() {
		return nil
	}
	if _, enumeration := r.enumerationOf(target); enumeration {
		return nil
	}
	if res.DomainBinding != nil && len(res.DomainBinding.Path) == 0 && res.DomainBinding.Anchor != nil && res.DomainBinding.Anchor.Name() == field.Name() {
		return nil
	}
	pointed := byName[target]
	if pointed == nil || pointed.IsVirtual || !r.registersList(pointed) {
		return nil
	}
	keyColumn, keyed := pointedToKeyColumn(field, pointed, field.HasDeclaredEnumeration())
	if !keyed {
		return nil
	}
	row := &namedRow{Field: field, Resource: target, Table: target, KeyColumn: keyColumn}
	if pointed.IsDomainScoped() {
		if pointed.DomainBinding == nil || pointed.DomainBinding.Anchor == nil || !res.IsDomainScoped() {
			return nil
		}
		row.TenantColumn = fieldColumn(pointed.DomainBinding.Anchor)
		row.TenantPath = collectionHops(pointed.DomainBinding.Path)
	}

	return row
}

// registersList reports whether the run registers a List of res, as the generated
// collection registers it (collectResourceRegistrations): its generated list route, or a
// list Set its handlers declare by hand (@manualAddResourceSet).
func (r *resourceGenerator) registersList(res *resourceInfo) bool {
	if slices.Contains(res.ManualAddResourceSets, ListHandler) {
		return true
	}

	return r.genRoutes && !res.RoutingDisabled() && slices.Contains(resourceEndpoints(res), ListHandler)
}

// NamedRows are the resource's columns that name a row of another resource the
// application lists, in field order: what the generated NamedRows method declares.
func (r *resourceInfo) NamedRows() []*namedRow {
	return r.namedRows
}
