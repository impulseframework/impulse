package generation

import (
	"fmt"
	"slices"
	"strings"

	"github.com/impulseframework/impulse/resource"
)

// namedRow is one column of a resource, or one picked field of a method's request, that
// names a row of another resource the application lists: a column or a field whose picker
// lists that resource's table, a view or a computed list. A write sets a column only to a
// row its caller can list, and a method takes in a field only such a row; the library holds
// both to that (resource.NamedRow, resource.Caller.HoldPicked). The generated resource
// declares each such column for it (NamedRows), and a method's generated handler each such
// field (PickedRows).
type namedRow struct {
	// Field is the column, or the request's field, that names the row.
	Field namedField
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

// namedField is the column of a resource, or the field of a method's request, that names a
// row: the generated declaration says it by its Go name.
type namedField interface {
	Name() string
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
	byName, lists := r.namedRowKinds(resources)
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

// namedRowKinds are the run's tables and views, and its computed lists, by plural name:
// where a column or a field's picker source is looked for.
func (r *resourceGenerator) namedRowKinds(resources []*resourceInfo) (byName map[string]*resourceInfo, lists map[string]*computedResource) {
	byName = make(map[string]*resourceInfo, len(resources))
	for _, res := range resources {
		byName[r.pluralize(res.Name())] = res
	}
	lists = make(map[string]*computedResource, len(r.computedResources))
	for _, list := range r.computedResources {
		lists[r.pluralize(list.Name())] = list
	}

	return byName, lists
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
// list, which has no picker and nothing to read it by; a list of roles (a source whose key
// is marked @roleOf), whose rows are the roles a person hands out, which the grant of the
// method that hands them out says; the column that holds the row's own tenant key, which
// the request stamps; a column the server never stores (input only); and, on a resource
// that belongs to no tenant, a column naming a tenant's row, which no request of one
// tenant writes.
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
		row := r.computedNamedRow(res.IsDomainScoped(), field, target, list)
		if row != nil {
			row.Stored = r.storedNamedRow(res, field, byName)
		}

		return row
	case pointed.IsVirtual:
		row := r.viewNamedRow(res.IsDomainScoped(), field, target, pointed)
		if row != nil {
			row.Stored = r.storedNamedRow(res, field, byName)
		}

		return row
	}
	if !r.registersList(pointed) || keyHoldsRoles(pointed) {
		return nil
	}
	keyColumn, keyed := pointedToKeyColumn(field, pointed, field.HasDeclaredEnumeration())
	if !keyed {
		return nil
	}

	return tableNamedRow(res.IsDomainScoped(), field, target, keyColumn, pointed)
}

// tableNamedRow is the declaration of a column or a field naming a row of the table
// pointed, read by keyColumn: the table, its key column and its tenant facts, nil where the
// table is a tenant's and its tenant cannot be said, or the request it is named in
// (inTenant) is of no tenant.
func tableNamedRow(inTenant bool, field namedField, table, keyColumn string, pointed *resourceInfo) *namedRow {
	row := &namedRow{Field: field, Resource: table, Table: table, KeyColumn: keyColumn}
	if pointed.IsDomainScoped() {
		if pointed.DomainBinding == nil || pointed.DomainBinding.Anchor == nil || !inTenant {
			return nil
		}
		row.TenantColumn = fieldColumn(pointed.DomainBinding.Anchor)
		row.TenantPath = collectionHops(pointed.DomainBinding.Path)
	}

	return row
}

// viewNamedRow is the declaration of a column or a field picked from the view pointed:
// its own key, a List the run registers, a key that is no role's, and, for a tenant's
// view, the view's bare @domain, named in a request of a tenant (inTenant). The caller
// adds the table a column's foreign key references (Stored).
func (r *resourceGenerator) viewNamedRow(inTenant bool, field namedField, target string, pointed *resourceInfo) *namedRow {
	if !r.registersList(pointed) || keyHoldsRoles(pointed) {
		return nil
	}
	keyColumn, keyed := soleKeyColumn(pointed)
	if !keyed {
		return nil
	}
	row := &namedRow{Field: field, Resource: target, Table: target, KeyColumn: keyColumn, View: true, view: pointed}
	if pointed.IsDomainScoped() {
		binding := pointed.DomainBinding
		if binding == nil || binding.Anchor == nil || len(binding.Path) > 0 || !inTenant {
			return nil
		}
		row.TenantColumn = fieldColumn(binding.Anchor)
	}

	return row
}

// computedNamedRow is the declaration of a column or a field picked from the computed
// list: one @primarykey field that is no role's, and a list the run serves; a tenant's
// list, named in a request of a tenant (inTenant), asked in the request's tenant. The
// caller adds the table a column's foreign key references (Stored).
func (r *resourceGenerator) computedNamedRow(inTenant bool, field namedField, target string, list *computedResource) *namedRow {
	if len(list.PrimaryKeys()) != 1 || !r.genRoutes || list.RoutingDisabled() || list.SuppressListHandler {
		return nil
	}
	if list.PrimaryKey().RoleOf() != "" {
		return nil
	}
	row := &namedRow{Field: field, Resource: target, Computed: true, list: list}
	if list.IsDomainScoped() {
		if !inTenant {
			return nil
		}
		row.GrantInTenant = true
	}

	return row
}

// keyHoldsRoles reports whether a key of the table or the view holds a role's name
// (@roleOf): its rows are a list of roles.
func keyHoldsRoles(pointed *resourceInfo) bool {
	for _, key := range pointed.PrimaryKeys() {
		if key.RoleOf() != "" {
			return true
		}
	}

	return false
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
	stored := tableNamedRow(res.IsDomainScoped(), field, field.ReferencedResource, keyColumn, table)
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

// resolvePickedRows finds, on every method a person calls through a generated handler,
// the fields of its request that name a row of another resource the application lists
// (pickedRowOf). It runs once the methods are parsed, before their handlers render, and
// before the named-row sources do. A method whose handler is written by hand holds what it
// takes itself.
func (r *resourceGenerator) resolvePickedRows(methods []*rpcMethodInfo) {
	byName, lists := r.namedRowKinds(r.resources)
	for _, method := range methods {
		method.pickedRows = nil
		if method.SuppressHandler {
			continue
		}
		for _, field := range method.Fields {
			if row := r.pickedRowOf(method, field, byName, lists); row != nil {
				method.pickedRows = append(method.pickedRows, row)
			}
		}
	}
}

// pickedRowOf is the declaration of a method's request field as one that names a row of
// another resource the application lists, nil where it is none. The field is one of the
// request's own with a field-scope @enumerate, and its source is declared as a column's
// is (namedRowOf): a table of the run with one key column that registers a List, read by
// that key with its tenant facts; a view; a computed list. A method names no row its own
// request made, so none carries Stored. Not declared: a source that is an enumeration
// table, one the run does not know or that serves no list, a list of roles (its key marked
// @roleOf: the method's own grant says which roles a person hands out), a tenant's source
// on a method of the whole application, which is asked in no tenant, and the tenant record
// on such a method: the tenant it names is the place it acts in, as a tenant's method takes
// its tenant from its address, and what the caller may do there is for the method's own
// grant and its body to say. A scheduled method has no caller, and is not among the
// methods asked.
func (r *resourceGenerator) pickedRowOf(method *rpcMethodInfo, field *rpcField, byName map[string]*resourceInfo, lists map[string]*computedResource) *namedRow {
	if !field.IsEnumerated() || field.Enumeration != "" {
		return nil
	}
	target := field.EnumeratedResource()
	inTenant := method.IsDomainScoped()
	pointed := byName[target]
	switch {
	case pointed == nil:
		list := lists[target]
		if list == nil {
			return nil
		}

		return r.computedNamedRow(inTenant, field, target, list)
	case pointed.IsVirtual:
		return r.viewNamedRow(inTenant, field, target, pointed)
	case pointed.IsTenant && !inTenant:
		return nil
	}
	if !r.registersList(pointed) || keyHoldsRoles(pointed) {
		return nil
	}
	keyColumn, keyed := soleKeyColumn(pointed)
	if !keyed {
		return nil
	}

	return tableNamedRow(inTenant, field, target, keyColumn, pointed)
}

// PickedRows are the method's request fields that name a row of another resource the
// application lists, in field order: what the generated handler holds to the rows its
// caller can list (resource.Caller.HoldPicked).
func (r *rpcMethodInfo) PickedRows() []*namedRow {
	return r.pickedRows
}
