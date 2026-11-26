package generation

import (
	"fmt"
	"slices"
	"strings"

	"github.com/impulseframework/impulse/resource"
)

// namedRow is one column of a resource that names a row of another resource the
// application lists: a column whose picker lists that resource's table, a view or a
// computed list. A write sets it only to a row its caller can list, and the library holds
// it to that (resource.NamedRow); the generated resource declares each such column for it
// (NamedRows).
type namedRow struct {
	// Field is the column that names the row.
	Field *resourceField
	// Resource is the named row's resource, by its plural name, Table its table and
	// KeyColumn the table's column the column's value matches. For a view, Table is the
	// view's plural name and KeyColumn its own key; for a computed list both are empty.
	Resource  string
	Table     string
	KeyColumn string
	// TenantColumn and TenantPath say where the named row's tenant is read, as a name
	// field's do; both are empty for a resource that belongs to no tenant. A view's tenant
	// is a column of its own, and a computed list has neither.
	TenantColumn string
	TenantPath   []resource.BindingHop
	// View says the row is a view's, read from the view's query (resource.NamedRow.View).
	View bool
	// Computed says the row is a computed list's, asked of the list's own function
	// (resource.NamedRow.Computed).
	Computed bool
	// GrantInTenant says the List on a computed list of a tenant is asked in the request's
	// tenant (resource.NamedRow.GrantInTenant).
	GrantInTenant bool
	// Stored is the table the column's foreign key references, for a view's or a computed
	// list's row: its Table, KeyColumn, TenantColumn and TenantPath, as a column naming a
	// row of that table states them. Nil where the column has no foreign key into a table
	// of the run, or those facts cannot be stated.
	Stored *namedRow

	// view and list are the view or the computed list the row is picked from, whose home's
	// named-row sources hand it to the library (namedRowSourcesOf); nil for a table's.
	view *resourceInfo
	list *computedResource
}

// Literal renders the column as the resource.NamedRow the generated NamedRows method
// returns: the fields every column states, then each of View, Computed, GrantInTenant and
// Stored where it is set.
func (n *namedRow) Literal() string {
	var b strings.Builder
	fmt.Fprintf(&b, "{Field: %q, Resource: %q, Table: %q, KeyColumn: %q", n.Field.Name(), n.Resource, n.Table, n.KeyColumn)
	n.writeTenant(&b)
	if n.View {
		b.WriteString(", View: true")
	}
	if n.Computed {
		b.WriteString(", Computed: true")
	}
	if n.GrantInTenant {
		b.WriteString(", GrantInTenant: true")
	}
	if n.Stored != nil {
		fmt.Fprintf(&b, ", Stored: &resource.NamedRow{Table: %q, KeyColumn: %q", n.Stored.Table, n.Stored.KeyColumn)
		n.Stored.writeTenant(&b)
		b.WriteString("}")
	}
	b.WriteString("}")

	return b.String()
}

// writeTenant writes the row's tenant facts, each where it is set.
func (n *namedRow) writeTenant(b *strings.Builder) {
	if n.TenantColumn != "" {
		fmt.Fprintf(b, ", TenantColumn: %q", n.TenantColumn)
	}
	if len(n.TenantPath) > 0 {
		b.WriteString(", TenantPath: []resource.BindingHop{")
		for i, hop := range n.TenantPath {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(b, "{Table: %q, JoinColumn: %q, Column: %q}", hop.Table, hop.JoinColumn, hop.Column)
		}
		b.WriteString("}")
	}
}

// resolveNamedRows finds, on every resource that is a table's, the columns that name a
// row of another resource the application lists (namedRowOf). It runs once the
// enumerations are resolved, where the name fields are found.
func (r *resourceGenerator) resolveNamedRows(resources []*resourceInfo) {
	byName := make(map[string]*resourceInfo, len(resources))
	for _, res := range resources {
		byName[r.pluralize(res.Name())] = res
	}
	lists := make(map[string]*computedResource, len(r.computedResources))
	for _, list := range r.computedResources {
		lists[r.pluralize(list.Name())] = list
	}
	for _, res := range resources {
		res.namedRows = nil
		if res.IsVirtual {
			// A view's rows are read, never written.
			continue
		}
		for _, field := range res.Fields {
			if row := r.namedRowOf(res, field, byName, lists); row != nil {
				res.namedRows = append(res.namedRows, row)
			}
		}
	}
}

// namedRowOf is the declaration of field as a column that names a row of another
// resource the application lists, nil where it is none. Its picker's source
// (pointedToResource: the table its foreign key references, or the resource a
// field-scope @enumerate names) is looked for among the run's tables and views and, where
// it is neither, among its computed lists:
//
//   - a table: one key column the column matches (pointedToKeyColumn), and a List the run
//     registers;
//   - a view: one key column of its own and a List the run registers, its tenant a column
//     of its own projection (a bare @domain) where it is a tenant's;
//   - a computed list: one @primarykey field, and a list the run serves; a tenant's list
//     is asked in the request's tenant (GrantInTenant).
//
// A view's or a computed list's row carries the table the column's foreign key
// references where it has one (Stored). Not declared: a column whose source is an
// enumeration table, which everyone picks from under no grant; a source the run does not
// list, which has no picker and nothing to read it by; the column that holds the row's
// own tenant key, which the request stamps; a column the server never stores (input
// only); and, on a resource that belongs to no tenant, a column naming a tenant's row,
// which no request of one tenant writes.
func (r *resourceGenerator) namedRowOf(res *resourceInfo, field *resourceField, byName map[string]*resourceInfo, lists map[string]*computedResource) *namedRow {
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
	switch {
	case pointed == nil:
		list := lists[target]
		if list == nil {
			return nil
		}

		return r.computedNamedRow(res, field, target, list, byName)
	case pointed.IsVirtual:
		return r.viewNamedRow(res, field, target, pointed, byName)
	}
	if !r.registersList(pointed) {
		return nil
	}
	keyColumn, keyed := pointedToKeyColumn(field, pointed, field.HasDeclaredEnumeration())
	if !keyed {
		return nil
	}

	return tableNamedRow(res, field, target, keyColumn, pointed)
}

// tableNamedRow is the declaration of a column naming a row of the table pointed, read by
// keyColumn: the table, its key column and its tenant facts, nil where the table is a
// tenant's and its tenant cannot be said for res.
func tableNamedRow(res *resourceInfo, field *resourceField, table, keyColumn string, pointed *resourceInfo) *namedRow {
	row := &namedRow{Field: field, Resource: table, Table: table, KeyColumn: keyColumn}
	if pointed.IsDomainScoped() {
		if pointed.DomainBinding == nil || pointed.DomainBinding.Anchor == nil || !res.IsDomainScoped() {
			return nil
		}
		row.TenantColumn = fieldColumn(pointed.DomainBinding.Anchor)
		row.TenantPath = collectionHops(pointed.DomainBinding.Path)
	}

	return row
}

// viewNamedRow is the declaration of a column picked from the view pointed: its own key,
// a List the run registers, and, for a tenant's view, the view's bare @domain, on a
// resource that is a tenant's too.
func (r *resourceGenerator) viewNamedRow(res *resourceInfo, field *resourceField, target string, pointed *resourceInfo, byName map[string]*resourceInfo) *namedRow {
	if !r.registersList(pointed) {
		return nil
	}
	keyColumn, keyed := pointedToKeyColumn(field, pointed, true)
	if !keyed {
		return nil
	}
	row := &namedRow{Field: field, Resource: target, Table: target, KeyColumn: keyColumn, View: true, view: pointed}
	if pointed.IsDomainScoped() {
		binding := pointed.DomainBinding
		if binding == nil || binding.Anchor == nil || len(binding.Path) > 0 || !res.IsDomainScoped() {
			return nil
		}
		row.TenantColumn = fieldColumn(binding.Anchor)
	}
	row.Stored = r.storedNamedRow(res, field, byName)

	return row
}

// computedNamedRow is the declaration of a column picked from the computed list: one
// @primarykey field and a list the run serves; a tenant's list, on a resource that is a
// tenant's too, asked in the request's tenant.
func (r *resourceGenerator) computedNamedRow(res *resourceInfo, field *resourceField, target string, list *computedResource, byName map[string]*resourceInfo) *namedRow {
	if len(list.PrimaryKeys()) != 1 || !r.genRoutes || list.RoutingDisabled() || list.SuppressListHandler {
		return nil
	}
	row := &namedRow{Field: field, Resource: target, Computed: true, list: list}
	if list.IsDomainScoped() {
		if !res.IsDomainScoped() {
			return nil
		}
		row.GrantInTenant = true
	}
	row.Stored = r.storedNamedRow(res, field, byName)

	return row
}

// storedNamedRow is the table a column picked from a view or a computed list references
// by its foreign key, as a column naming a row of that table states it (tableNamedRow);
// nil where the column has no foreign key into a table of the run that is not an
// enumeration table, or its facts cannot be stated.
func (r *resourceGenerator) storedNamedRow(res *resourceInfo, field *resourceField, byName map[string]*resourceInfo) *namedRow {
	if !field.IsForeignKey || field.ReferencedResource == "" {
		return nil
	}
	if _, enumeration := r.enumerationOf(field.ReferencedResource); enumeration {
		return nil
	}
	table := byName[field.ReferencedResource]
	if table == nil || table.IsVirtual {
		return nil
	}
	keyColumn, keyed := pointedToKeyColumn(field, table, false)
	if !keyed {
		return nil
	}
	stored := tableNamedRow(res, field, field.ReferencedResource, keyColumn, table)
	if stored != nil {
		stored.Field = nil
	}

	return stored
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
