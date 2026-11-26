// recordroles.go reads the records a role can be held on (@holdsRoles): a tenant's record
// a role whose heldOn names it is given on one row of, so that the role counts on that
// row, on the rows under it by foreign keys and on the rows it names, and nowhere else.
// The generator marks the record in the collection, follows the schema from the record
// down to every tenant's table written by the application beneath it, and gives each
// such table, and each table the record names, the attributes the collection's condition
// for the role reads (resource.RecordCondition). The attributes are the generator's own:
// the collection carries them beside the written ones, and nothing that lists the
// bindings an author wrote lists them.

package generation

import (
	"cmp"
	"maps"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/generation/parser"
	"github.com/impulseframework/impulse/generation/parser/genlang"
	"github.com/impulseframework/impulse/resource"
)

// rejectHoldsRoles refuses @holdsRoles on a struct with no table of its own: a view, a
// computed resource or a method.
func rejectHoldsRoles(pStruct *parser.Struct, annotations genlang.StructAnnotations) error {
	if !annotations.Struct.Has(holdsRolesKeyword) {
		return nil
	}

	return errors.Newf("struct %s: @%s marks a record a role can be held on, and %s has no table of its own. Write it on a resource with a table.", pStruct.Name(), holdsRolesKeyword, pStruct.Name())
}

// isTenantsTable reports whether the resource is a table-backed resource of a tenant
// written by the application: what a role held on a record reaches beneath the record,
// or names. The tenant record is none, and neither is a view.
func (c *client) isTenantsTable(res *resourceInfo) bool {
	if res.IsVirtual || res.IsTenant || res.identity != nil {
		return false
	}
	if _, ok := c.tableMap[c.pluralize(res.Name())]; !ok {
		return false
	}

	return res.PermissionScope == accesstypes.DomainPermissionScope && res.DomainBinding != nil
}

// resolveRecordRoles reads, for each record the resources mark as one a role can be held
// on, what the schema puts under it and what it names, once every resource is extracted.
// A way down is followed through the tenant's tables written by the application alone:
// a row under the record only through another table, or through the tenant record, is not
// reached (pathsUnder stops there), and a table's foreign keys to itself are no way
// (schemaLinks leaves them out).
func (c *client) resolveRecordRoles(resources []*resourceInfo) error {
	var marked []*resourceInfo
	for _, res := range resources {
		if res.holdsRoles {
			marked = append(marked, res)
		}
	}
	if len(marked) == 0 {
		return nil
	}

	tenants := make(map[string]*resourceInfo, len(resources))
	for _, res := range resources {
		if c.isTenantsTable(res) {
			tenants[c.pluralize(res.Name())] = res
		}
	}
	stop := make(map[string]bool, len(c.tableMap))
	for table := range c.tableMap {
		if _, ok := tenants[table]; !ok {
			stop[table] = true
		}
	}
	links := c.schemaLinks()
	for _, record := range marked {
		if err := c.resolveRecord(record, tenants, links, stop); err != nil {
			return err
		}
	}

	return nil
}

// refuseRecordMark refuses a mark on a resource no role can be held on one row of: the
// tenant record, whose role is the role held in the tenant; a resource of no tenant; and
// a table keyed otherwise than by one text column, which the store's rows name a record
// by.
func (c *client) refuseRecordMark(record *resourceInfo, table *tableMetadata) error {
	name := record.Name()
	switch {
	case record.IsTenant:
		return errors.Newf("struct %s: %s is the tenant record, and a role held on it is the role held in the tenant. Take @%s off.", name, name, holdsRolesKeyword)
	case record.PermissionScope != accesstypes.DomainPermissionScope || record.DomainBinding == nil:
		return errors.Newf("struct %s: a role held on one record is a tenant's role, and %s is not a tenant's resource (@%s(domain) and @%s). Take @%s off, or make the resource a tenant's.",
			name, name, permissionScopeKeyword, domainKeyword, holdsRolesKeyword)
	}
	key := table.keyColumns()
	if len(key) == 1 && strings.HasPrefix(table.Columns[key[0]].SpannerType, "STRING") {
		return nil
	}
	columns := make([]string, 0, len(key))
	for _, column := range key {
		columns = append(columns, strings.TrimSpace(column+" "+table.Columns[column].SpannerType))
	}

	return errors.Newf("struct %s: a record that holds roles is keyed by one text column, and %s is keyed by (%s). Take @%s off, or key the table by one id column.",
		name, c.pluralize(name), strings.Join(columns, ", "), holdsRolesKeyword)
}

// resolveRecord reads one record's ways and names (resolveRecordRoles): each tenant's
// table under it gets one attribute for each of its ways down from the record, numbered
// from 0 in the order pathsUnder gives them, the record itself one on its key, and each
// tenant's table the record names by a foreign key one on the column the key references,
// numbered from 0 in the order of the record's columns.
func (c *client) resolveRecord(record *resourceInfo, tenants map[string]*resourceInfo, links map[string][]deleteLink, stop map[string]bool) error {
	table := c.pluralize(record.Name())
	meta := c.tableMap[table]
	if err := c.refuseRecordMark(record, meta); err != nil {
		return err
	}
	kind := accesstypes.Resource(table)
	key := meta.keyColumns()[0]
	one, many := c.deleteWords(record)

	paths := pathsUnder(links, table, stop)
	for _, under := range slices.Sorted(maps.Keys(paths)) {
		row, ok := tenants[under]
		if !ok {
			continue
		}
		for n, path := range paths[under] {
			column, hops, wide := recordWayColumn(key, path)
			if wide != nil {
				return errors.Newf("struct %s: %s is under %s by %s, and that foreign key matches %d columns at once, where a role held on one %s is followed one column at a time. Name the row above by its one key column, or take @%s off %s.",
					record.Name(), under, table, pathWords(under, path), len(wide.Columns), one, holdsRolesKeyword, record.Name())
			}
			attribute := resource.RecordWayAttribute(kind, n)
			row.recordAttributes = append(row.recordAttributes, resource.AttributeData{Name: attribute, Column: column, Type: resource.AttributeTypeString, Path: hops})
			row.recordWays = append(row.recordWays, resource.RecordWayData{Record: kind, Attribute: attribute})
		}
	}
	self := resource.RecordWayAttribute(kind, 0)
	record.recordAttributes = append(record.recordAttributes, resource.AttributeData{Name: self, Column: key, Type: resource.AttributeTypeString})
	record.recordWays = append(record.recordWays, resource.RecordWayData{Record: kind, Attribute: self, Self: true})

	names, err := c.recordNames(record, table, tenants, links)
	if err != nil {
		return err
	}
	record.heldRecord = &resource.RecordData{Key: key, One: one, Many: many, Names: names}

	return nil
}

// recordNames reads the rows a record names: each foreign key of the record to a tenant's
// table written by the application, the tenant record's never, in the order of its
// columns' names. The named table gets the attribute the name's test reads, on the column
// the key references.
func (c *client) recordNames(record *resourceInfo, table string, tenants map[string]*resourceInfo, links map[string][]deleteLink) ([]resource.RecordNameData, error) {
	kind := accesstypes.Resource(table)
	one, _ := c.deleteWords(record)
	steps := slices.Clone(links[table])
	slices.SortFunc(steps, func(a, b deleteLink) int {
		return cmp.Compare(strings.Join(a.Columns, ","), strings.Join(b.Columns, ","))
	})

	var names []resource.RecordNameData
	for _, step := range steps {
		named, ok := tenants[step.Table]
		if !ok {
			continue
		}
		if len(step.Columns) != 1 {
			return nil, errors.Newf("struct %s: %s names %s by %s, and that foreign key matches %d columns at once, where a role held on one %s is followed one column at a time. Name the row above by its one key column, or take @%s off %s.",
				record.Name(), table, step.Table, step.from(table), len(step.Columns), one, holdsRolesKeyword, record.Name())
		}
		attribute := resource.RecordNameAttribute(kind, len(names))
		namedOne, namedMany := c.deleteWords(named)
		names = append(names, resource.RecordNameData{
			Resource: accesstypes.Resource(step.Table), Column: step.Columns[0], Attribute: attribute, One: namedOne, Many: namedMany,
		})
		named.recordAttributes = append(named.recordAttributes, resource.AttributeData{Name: attribute, Column: step.References[0], Type: resource.AttributeTypeString})
	}

	return names, nil
}

// recordWayColumn follows the record's key down one way from the record to the row: the
// row's column that holds, or leads by the hops returned to, the key of the record the
// row is under. A step whose referenced columns hold what is followed reads its own
// column in the same place with no hop (an interleaved table opens its key with its
// parent's); a step of one column reads the table above through a hop; a step of several
// columns that holds none of it is returned as wide, and no way is read.
func recordWayColumn(key string, path []deleteLink) (column string, hops []resource.BindingHop, wide *deleteLink) {
	need := key
	for i := len(path) - 1; i >= 0; i-- {
		step := path[i]
		if at := slices.Index(step.References, need); at >= 0 {
			need = step.Columns[at]

			continue
		}
		if len(step.Columns) != 1 {
			return "", nil, &path[i]
		}
		hops = append([]resource.BindingHop{{Table: step.Table, JoinColumn: step.References[0], Column: need}}, hops...)
		need = step.Columns[0]
	}

	return need, hops, nil
}

// collectRecordRoleRegistrations marks in the collection the records a role can be held
// on and the ways each resource's rows are under them, and, when the run marks one, where
// each auth whose management the run generates keeps the roles held on records: its
// people's table, and its groups' with the set of a person's groups where it keeps
// groups. A table the schema does not hold refuses the run.
func (r *resourceGenerator) collectRecordRoleRegistrations(b *resource.CollectionBuilder) error {
	var first *resourceInfo
	for _, res := range r.resources {
		name := accesstypes.Resource(r.pluralize(res.Name()))
		scope := scopeOrGlobal(res.PermissionScope)
		if res.heldRecord != nil {
			b.SetResourceHoldsRoles(scope, name, res.heldRecord)
			if first == nil {
				first = res
			}
		}
		if len(res.recordWays) > 0 {
			b.SetResourceUnder(scope, name, res.recordWays)
		}
	}
	if first == nil {
		return nil
	}

	for _, inst := range r.identity {
		store := resource.RecordRoleStoreData{Auth: inst.authName(), UserRoles: inst.policy.RecordUserRoles}
		if inst.groupSet != "" {
			store.GroupRoles, store.GroupSet = inst.policy.RecordGroupRoles, inst.groupSet
		}
		for _, table := range []string{store.UserRoles, store.GroupRoles} {
			if table == "" {
				continue
			}
			if _, ok := r.tableMap[table]; !ok {
				return errors.Newf("struct %s: the roles held on one %s are kept for Auth(%q) in the table %s, and the schema holds none. Add the policy store's tables for the auth to the schema.",
					first.Name(), first.heldRecord.One, inst.importPath, table)
			}
		}
		b.AddRecordRoleStore(store)
	}

	return nil
}
