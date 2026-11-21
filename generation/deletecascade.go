package generation

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/generation/parser/genlang"
)

const (
	// deleteCascadeKeyword declares what a delete of a record takes with it and what
	// refuses it: @deleteCascade(Project, Task, refusedBy: Invoice).
	deleteCascadeKeyword = "deleteCascade"
	// deleteCascadeRefusedByKey opens the list of kinds whose rows refuse the delete.
	deleteCascadeRefusedByKey = "refusedBy"
)

// deleteCascadeSpec is the argument shape of @deleteCascade: the kinds the delete
// takes, then the kinds that refuse it.
func deleteCascadeSpec() *genlang.ArgSpec {
	return &genlang.ArgSpec{
		Positional:    1,
		AnyPositional: true,
		Keys:          []string{deleteCascadeRefusedByKey},
		Multi:         []string{deleteCascadeRefusedByKey},
	}
}

// deleteCascadeDecl is a resource's @deleteCascade as its author wrote it: the names of
// the kinds a delete of the record takes, and of the kinds that refuse it.
type deleteCascadeDecl struct {
	Taken     []string
	RefusedBy []string
}

// parseDeleteCascade reads a resource's @deleteCascade. A kind is named once: a kind
// in both lists, or twice in one, has no one meaning, and a record is never a kind
// under itself.
func parseDeleteCascade(record string, arg genlang.Arg) (*deleteCascadeDecl, error) {
	invocations, err := arg.ParseInvocations(deleteCascadeSpec())
	if err != nil {
		return nil, errors.Wrapf(err, "@%s on %s", deleteCascadeKeyword, record)
	}
	if len(invocations) != 1 {
		return nil, errors.Newf("@%s on %s is written %d times; write it once, with every kind in the one list", deleteCascadeKeyword, record, len(invocations))
	}

	decl := &deleteCascadeDecl{
		Taken:     invocations[0].Positional,
		RefusedBy: invocations[0].List(deleteCascadeRefusedByKey),
	}

	seen := make(map[string]struct{}, len(decl.Taken)+len(decl.RefusedBy))
	for _, kind := range slices.Concat(decl.Taken, decl.RefusedBy) {
		if kind == record {
			return nil, errors.Newf("@%s on %s names %s itself; list the kinds of record under a %s, not the record", deleteCascadeKeyword, record, record, record)
		}
		if _, dup := seen[kind]; dup {
			return nil, errors.Newf("@%s on %s names %s twice; a kind is either taken with the %s or refuses its delete, and is listed once", deleteCascadeKeyword, record, kind, record)
		}
		seen[kind] = struct{}{}
	}

	return decl, nil
}

// deleteLink is one step from a row up toward the record it is under: the row's Columns
// hold the key of a row of Table, in that table's References columns.
type deleteLink struct {
	Columns    []string
	Table      string
	References []string
	// Cascades marks a step the database follows on a delete: the row goes with the
	// row it is under (a foreign key or an interleave declared ON DELETE CASCADE).
	// It is no part of what makes two links the same step (equal).
	Cascades bool
}

// from spells the link from the table it leaves: "Tasks.ProjectId to Projects.Id".
func (l deleteLink) from(table string) string {
	return fmt.Sprintf("%s.%s to %s.%s", table, strings.Join(l.Columns, "+"), l.Table, strings.Join(l.References, "+"))
}

// deleteKind is one kind of row under a record, as the description is written with it.
type deleteKind struct {
	// Name is the kind's Go name and Table its table, which is its resource.
	Name  string
	Table string
	// One and Many are the kind's words in a sentence.
	One, Many string
	// Key is the kind's primary key columns, in key order.
	Key []string
	// Paths are the ways from a row of the kind up to the record, nearest step first.
	Paths [][]deleteLink
	// IndexEntries is the number of indexes on the table besides its key, and Files the
	// number of stored files a row holds a key to: what a row's delete costs beyond
	// the delete itself.
	IndexEntries int
	Files        int
	// RemovedWith is the tables whose row's delete makes the database remove rows of
	// the kind (removedWith). It is written for the kinds under Under alone.
	RemovedWith []string
}

// depth is the kind's longest way up to the record, in steps. A kind is always deeper
// than a kind its rows are under, so deleting by depth, deepest first, never deletes a
// row before the rows under it.
func (k *deleteKind) depth() int {
	depth := 0
	for _, path := range k.Paths {
		depth = max(depth, len(path))
	}

	return depth
}

// deleteTree is the description of what a delete of a record takes with it and what
// refuses it, as the generator writes it into the record's file (resource.DeleteTree).
type deleteTree struct {
	Record *deleteKind
	// Taken is in delete order: deepest first.
	Taken     []*deleteKind
	RefusedBy []*deleteKind
	// Under is the kinds whose rows refuse a plain delete of the record (kindsUnder),
	// by table name.
	Under []*deleteKind
}

// deleteWords is the one place a kind of record gets its words for a sentence about a
// delete: the resource's name in words, one and many, in lower case ("ShipClass" is
// "ship class" and "ship classes"). The tenant record takes the label it declares.
func (c *client) deleteWords(res *resourceInfo) (one, many string) {
	name := res.Name()
	if res.IsTenant && res.TenantLabel != "" {
		name = res.TenantLabel
	}

	return lowerFirst(screenWords(name)), lowerFirst(screenWords(c.pluralize(name)))
}

// keyColumns is the table's primary key columns in key order.
func (t *tableMetadata) keyColumns() []string {
	type keyColumn struct {
		name    string
		ordinal int64
	}

	var key []keyColumn
	for name, column := range t.Columns {
		if column.IsPrimaryKey {
			key = append(key, keyColumn{name: name, ordinal: column.KeyOrdinalPosition})
		}
	}
	slices.SortFunc(key, func(a, b keyColumn) int {
		return cmp.Or(cmp.Compare(a.ordinal, b.ordinal), cmp.Compare(a.name, b.name))
	})

	columns := make([]string, 0, len(key))
	for _, column := range key {
		columns = append(columns, column.name)
	}

	return columns
}

// secondaryIndexes is the number of indexes on the table besides its key.
func (t *tableMetadata) secondaryIndexes() int {
	count := 0
	for _, index := range t.Indexes {
		if !index.PrimaryKey {
			count++
		}
	}

	return count
}

// schemaLinks is every table's steps up to the tables its rows are under: one for each
// foreign key the schema read resolved, and one for an interleave, whose rows open
// their key with their parent's. Two foreign keys on the same columns to the same table
// are one step, and so are an interleave and a foreign key on its columns: the one step
// cascades when any of them does, since the database then removes the row. A table's
// foreign keys to itself are not among them (selfLinks).
func (c *client) schemaLinks() map[string][]deleteLink {
	links := make(map[string][]deleteLink, len(c.tableMap))
	for name, table := range c.tableMap {
		var up []deleteLink
		if table.ParentTable != "" {
			if parent, ok := c.tableMap[table.ParentTable]; ok {
				key := parent.keyColumns()
				up = append(up, deleteLink{Columns: key, Table: table.ParentTable, References: key, Cascades: table.OnDeleteCascade})
			}
		}
		for _, foreignKey := range table.ForeignKeys {
			if foreignKey.ReferencedTable == "" || foreignKey.ReferencedTable == name {
				continue
			}
			up = addDeleteLink(up, foreignKey.link())
		}
		if len(up) > 0 {
			links[name] = up
		}
	}

	return links
}

// selfLinks is every table's foreign keys to itself: a row under a row of its own kind
// (a folder in a folder). No way up to a record is walked through one, so schemaLinks
// leaves them out; a plain delete of a row counts the rows that name it (kindsUnder).
func (c *client) selfLinks() map[string][]deleteLink {
	links := make(map[string][]deleteLink)
	for name, table := range c.tableMap {
		for _, foreignKey := range table.ForeignKeys {
			if foreignKey.ReferencedTable == name {
				links[name] = addDeleteLink(links[name], foreignKey.link())
			}
		}
	}

	return links
}

// link is the foreign key as a step up to the table it references.
func (f *foreignKeyMeta) link() deleteLink {
	return deleteLink{Columns: f.Columns, Table: f.ReferencedTable, References: f.ReferencedColumns, Cascades: f.DeleteRule == cascadeDeleteAction}
}

// addDeleteLink adds a step to a table's steps, folding it into the same step where
// that is there already: the step cascades when either does.
func addDeleteLink(up []deleteLink, link deleteLink) []deleteLink {
	if i := slices.IndexFunc(up, link.equal); i >= 0 {
		up[i].Cascades = up[i].Cascades || link.Cascades

		return up
	}

	return append(up, link)
}

// cascadingLinks is the steps the database follows on a delete, of every table's steps.
func cascadingLinks(links map[string][]deleteLink) map[string][]deleteLink {
	cascading := make(map[string][]deleteLink, len(links))
	for table, up := range links {
		for _, link := range up {
			if link.Cascades {
				cascading[table] = append(cascading[table], link)
			}
		}
	}

	return cascading
}

// removedWith is the tables whose row's delete makes the database remove rows of the
// table: every table up from it by steps the database follows on a delete, the table
// itself among them where a row goes with the row of its own kind it is under. In name
// order.
func removedWith(cascading, self map[string][]deleteLink, table string) []string {
	reached := make(map[string]bool)
	var walk func(from string)
	walk = func(from string) {
		for _, link := range slices.Concat(cascading[from], self[from]) {
			if !link.Cascades || reached[link.Table] {
				continue
			}
			reached[link.Table] = true
			walk(link.Table)
		}
	}
	walk(table)

	return slices.Sorted(maps.Keys(reached))
}

// kindsUnder is the kinds whose rows refuse a plain delete of the record, by table
// name: each kind with a step straight up to the record that the database does not
// follow, and no way up to the record that the database follows all along, since the
// database removes that kind's rows with the record. The record's own kind is one
// where a row names another row of its kind and no such link is followed by the
// database. Each kind carries those steps alone, one path each, and the tables its rows
// go with at the database (removedWith). A table no resource declares has no words and
// no grant to ask of, and is left to the database's refusal.
func (c *client) kindsUnder(res *resourceInfo, byTable map[string]*resourceInfo, links, self, cascading map[string][]deleteLink) []*deleteKind {
	record := c.pluralize(res.Name())
	swept := pathsUnder(cascading, record, nil)

	straight := func(up []deleteLink) [][]deleteLink {
		var paths [][]deleteLink
		for _, link := range up {
			if link.Table == record && !link.Cascades {
				paths = append(paths, []deleteLink{link})
			}
		}

		return paths
	}

	var kinds []*deleteKind
	for table, up := range links {
		paths := straight(up)
		kind, ok := byTable[table]
		if len(paths) == 0 || len(swept[table]) > 0 || !ok {
			continue
		}
		under := c.deleteKindOf(kind, paths)
		under.RemovedWith = removedWith(cascading, self, table)
		kinds = append(kinds, under)
	}
	followed := slices.ContainsFunc(self[record], func(link deleteLink) bool {
		return link.Cascades
	})
	if paths := straight(self[record]); len(paths) > 0 && !followed {
		under := c.deleteKindOf(res, paths)
		under.RemovedWith = removedWith(cascading, self, record)
		kinds = append(kinds, under)
	}
	slices.SortFunc(kinds, func(a, b *deleteKind) int {
		return cmp.Compare(a.Table, b.Table)
	})

	return kinds
}

// equal reports whether two links are the same step.
func (l deleteLink) equal(other deleteLink) bool {
	return l.Table == other.Table && slices.Equal(l.Columns, other.Columns) && slices.Equal(l.References, other.References)
}

// pathsUnder is every way from a table up to the record, by table: the tables under
// the record, each with its paths, nearest step first. A way never passes a table
// twice, so a row that hangs under the record only through another row of its own kind
// is not found by it, and never through the tables stop names: what is under one of
// those alone is not followed.
func pathsUnder(links map[string][]deleteLink, record string, stop map[string]bool) map[string][][]deleteLink {
	// under[t] is the tables with a step up to t, each with that step.
	type step struct {
		table string
		link  deleteLink
	}
	under := make(map[string][]step)
	for table, up := range links {
		for _, link := range up {
			under[link.Table] = append(under[link.Table], step{table: table, link: link})
		}
	}
	for table := range under {
		slices.SortFunc(under[table], func(a, b step) int {
			return cmp.Or(cmp.Compare(a.table, b.table), cmp.Compare(strings.Join(a.link.Columns, ","), strings.Join(b.link.Columns, ",")))
		})
	}

	paths := make(map[string][][]deleteLink)
	var walk func(table string, up []deleteLink, passed []string)
	walk = func(table string, up []deleteLink, passed []string) {
		for _, s := range under[table] {
			if s.table == record || slices.Contains(passed, s.table) {
				continue
			}
			path := append([]deleteLink{s.link}, up...)
			paths[s.table] = append(paths[s.table], path)
			if stop[s.table] {
				continue
			}
			walk(s.table, path, append(slices.Clone(passed), s.table))
		}
	}
	walk(record, nil, nil)

	return paths
}

// pathWords spells a way up to the record for a refusal: "Tasks.ProjectId to
// Projects.Id, then Projects.ClientId to Clients.Id".
func pathWords(table string, path []deleteLink) string {
	steps := make([]string, 0, len(path))
	for _, link := range path {
		steps = append(steps, link.from(table))
		table = link.Table
	}

	return strings.Join(steps, ", then ")
}

// resolveDeleteTrees writes each record's description of its delete once every
// resource is extracted: a record that declares one (@deleteCascade), held to what the
// schema puts under it, and the tenant record, whose description the library declares.
// Every record, with a declaration or without, is also given the kinds whose rows
// refuse a plain delete of it (kindsUnder), and a description to carry them where it
// had none. A read-only table (@enumerate) is never deleted from, and gets none.
func (c *client) resolveDeleteTrees(resources []*resourceInfo) error {
	byTable := make(map[string]*resourceInfo, len(resources))
	byName := make(map[string]*resourceInfo, len(resources))
	for _, res := range resources {
		byTable[c.pluralize(res.Name())] = res
		byName[res.Name()] = res
	}
	links := c.schemaLinks()
	self := c.selfLinks()
	cascading := cascadingLinks(links)

	for _, res := range resources {
		switch {
		case res.IsTenant && res.deleteCascade != nil:
			return errors.Newf("@%s on %s: %s is the tenant record, and the library declares what a tenant's delete takes: every row under the tenant, with nothing listed to refuse it. Remove @%s from %s.",
				deleteCascadeKeyword, res.Name(), res.Name(), deleteCascadeKeyword, res.Name())
		case res.IsTenant:
			tree, err := c.tenantDeleteTree(res, resources, byTable, links)
			if err != nil {
				return err
			}
			res.DeleteTree = tree
		case res.deleteCascade != nil:
			tree, err := c.declaredDeleteTree(res, byTable, byName, links)
			if err != nil {
				return err
			}
			res.DeleteTree = tree
		}

		if res.IsEnumeration() {
			continue
		}
		if _, ok := c.tableMap[c.pluralize(res.Name())]; !ok {
			continue
		}
		under := c.kindsUnder(res, byTable, links, self, cascading)
		if len(under) == 0 {
			continue
		}
		if res.DeleteTree == nil {
			res.DeleteTree = &deleteTree{Record: c.deleteKindOf(res, nil)}
		}
		res.DeleteTree.Under = under
	}

	return nil
}

// declaredDeleteTree holds a record's @deleteCascade to the schema and writes its
// description. Every kind it names is a resource with rows under the record, and every
// kind with rows under the record is named: taken with it, or refusing its delete. What
// is under a refusing kind alone needs no place in either list, since no row of it is
// under the record without a row of the kind that refuses.
func (c *client) declaredDeleteTree(res *resourceInfo, byTable, byName map[string]*resourceInfo, links map[string][]deleteLink) (*deleteTree, error) {
	record := c.pluralize(res.Name())
	decl := res.deleteCascade
	all := pathsUnder(links, record, nil)

	taken := make(map[string]bool, len(decl.Taken))
	refusing := make(map[string]bool, len(decl.RefusedBy))
	for _, name := range slices.Concat(decl.Taken, decl.RefusedBy) {
		kind, ok := byName[name]
		if !ok {
			return nil, errors.Newf("@%s on %s names %s, which is not a resource of this package. Name the resources whose rows are under a %s.",
				deleteCascadeKeyword, res.Name(), name, res.Name())
		}
		table := c.pluralize(name)
		if len(all[table]) == 0 {
			return nil, errors.Newf("@%s on %s names %s, but no foreign key or interleave puts a %s under a %s. Remove %s from the list.",
				deleteCascadeKeyword, res.Name(), name, name, res.Name(), name)
		}
		if slices.Contains(decl.Taken, name) {
			if kind.IsEnumeration() {
				return nil, errors.Newf("@%s on %s takes %s, which is a read-only table (@%s): its rows are never deleted. List %s under %s, or remove it.",
					deleteCascadeKeyword, res.Name(), name, enumerateKeyword, name, deleteCascadeRefusedByKey)
			}
			taken[table] = true

			continue
		}
		refusing[table] = true
	}

	// Every kind with a way up to the record that passes no refusing kind has its place
	// in a list.
	reached := pathsUnder(links, record, refusing)
	tables := make([]string, 0, len(reached))
	for table := range reached {
		tables = append(tables, table)
	}
	slices.Sort(tables)
	for _, table := range tables {
		if taken[table] || refusing[table] {
			continue
		}
		way := pathWords(table, reached[table][0])
		kind, ok := byTable[table]
		if !ok {
			return nil, errors.Newf("@%s on %s: the table %s is under a %s (%s), and no resource of this package declares it, so a delete in code could not remove its rows. Declare a resource for %s and list it.",
				deleteCascadeKeyword, res.Name(), table, res.Name(), way, table)
		}

		return nil, errors.Newf("@%s on %s: a %s is under a %s (%s), and the list names it neither as taken nor under %s. List %s to delete it with the %s, or under %s to keep a %s that has one from being deleted.",
			deleteCascadeKeyword, res.Name(), kind.Name(), res.Name(), way, deleteCascadeRefusedByKey, kind.Name(), res.Name(), deleteCascadeRefusedByKey, res.Name())
	}

	// A way in reached passes no refusing kind, so every table on it is a taken kind; a
	// named kind with none is under the record through a refusing kind alone.
	tree := &deleteTree{Record: c.deleteKindOf(res, nil)}
	for _, name := range decl.Taken {
		table := c.pluralize(name)
		paths := reached[table]
		if len(paths) == 0 {
			return nil, errors.Newf("@%s on %s takes %s, but a %s is under a %s only through a kind that refuses the delete, so there is never one to take. Remove %s from the list.",
				deleteCascadeKeyword, res.Name(), name, name, res.Name(), name)
		}
		tree.Taken = append(tree.Taken, c.deleteKindOf(byName[name], paths))
	}
	for _, name := range decl.RefusedBy {
		table := c.pluralize(name)
		paths := reached[table]
		if len(paths) == 0 {
			return nil, errors.Newf("@%s on %s lists %s under %s, but a %s is under a %s only through another kind that refuses the delete already. Remove %s from the list.",
				deleteCascadeKeyword, res.Name(), name, deleteCascadeRefusedByKey, name, res.Name(), name)
		}
		tree.RefusedBy = append(tree.RefusedBy, c.deleteKindOf(byName[name], paths))
	}
	sortDeepestFirst(tree.Taken)

	return tree, nil
}

// tenantDeleteTree writes the tenant record's description, which the library declares:
// a tenant's delete takes every kind of the application's rows that carry the tenant's
// key or lead to a row that does, and nothing refuses it. A row carries the tenant's key
// by a foreign key to the tenant record or as the column a tenant-scoped resource is
// read by.
func (c *client) tenantDeleteTree(tenant *resourceInfo, resources []*resourceInfo, byTable map[string]*resourceInfo, links map[string][]deleteLink) (*deleteTree, error) {
	record := c.pluralize(tenant.Name())
	tenantTable, err := c.tableMetadataFor(tenant.Name())
	if err != nil {
		return nil, err
	}
	tenantKey := tenantTable.keyColumns()

	// The schema's steps, and one for each resource read by a column holding the
	// tenant's key with no foreign key on it.
	withKeys := make(map[string][]deleteLink, len(links))
	for table, up := range links {
		withKeys[table] = slices.Clone(up)
	}
	if len(tenantKey) == 1 {
		for _, res := range resources {
			if res.IsTenant || res.DomainBinding == nil || len(res.DomainBinding.Path) > 0 || res.DomainBinding.Anchor == nil {
				continue
			}
			column, ok := res.DomainBinding.Anchor.LookupTag(spannerTagKey)
			if !ok {
				continue
			}
			table := c.pluralize(res.Name())
			link := deleteLink{Columns: []string{column}, Table: record, References: tenantKey}
			if !slices.ContainsFunc(withKeys[table], link.equal) {
				withKeys[table] = append(withKeys[table], link)
			}
		}
	}

	reached := pathsUnder(withKeys, record, nil)
	tree := &deleteTree{Record: c.deleteKindOf(tenant, nil)}
	for table, paths := range reached {
		kind, ok := byTable[table]
		if !ok || kind.IsEnumeration() {
			// A table no resource declares, the library's own among them, is not the
			// application's row to delete in code.
			continue
		}
		tree.Taken = append(tree.Taken, c.deleteKindOf(kind, paths))
	}
	sortDeepestFirst(tree.Taken)

	return tree, nil
}

// sortDeepestFirst puts kinds in delete order: the deepest first, and kinds of one
// depth by name.
func sortDeepestFirst(kinds []*deleteKind) {
	slices.SortFunc(kinds, func(a, b *deleteKind) int {
		return cmp.Or(cmp.Compare(b.depth(), a.depth()), cmp.Compare(a.Table, b.Table))
	})
}

// deleteKindOf describes a resource as a kind in a description, with its ways up to
// the record (none for the record itself).
func (c *client) deleteKindOf(res *resourceInfo, paths [][]deleteLink) *deleteKind {
	one, many := c.deleteWords(res)
	kind := &deleteKind{
		Name:  res.Name(),
		Table: c.pluralize(res.Name()),
		One:   one,
		Many:  many,
		Paths: paths,
		Files: len(res.FileKeyFields()),
	}
	if table, err := c.tableMetadataFor(res.Name()); err == nil {
		kind.Key = table.keyColumns()
		kind.IndexEntries = table.secondaryIndexes()
	}

	return kind
}

// HasDeleteTree reports whether the record's file carries a description of its delete.
func (r *resourceInfo) HasDeleteTree() bool {
	return r.DeleteTree != nil
}

// DeclaresDeleteCascade reports whether the record declares what its delete takes with
// it and what refuses it (@deleteCascade).
func (r *resourceInfo) DeclaresDeleteCascade() bool {
	return r.deleteCascade != nil
}

// DeleteTreeLiteral is the record's description as the Go value its file returns
// (resource.DeleteTree).
func (r *resourceInfo) DeleteTreeLiteral() string {
	var b strings.Builder
	tree := r.DeleteTree

	b.WriteString("&resource.DeleteTree{\n")
	fmt.Fprintf(&b, "Record: %q,\n", tree.Record.Table)
	fmt.Fprintf(&b, "Words: resource.DeleteWords{One: %q, Many: %q},\n", tree.Record.One, tree.Record.Many)
	fmt.Fprintf(&b, "Key: %s,\n", stringsLiteral(tree.Record.Key))
	fmt.Fprintf(&b, "Cost: resource.DeleteCost{IndexEntries: %d, Files: %d},\n", tree.Record.IndexEntries, tree.Record.Files)
	writeDeleteKinds(&b, "Taken", tree.Taken)
	writeDeleteKinds(&b, "RefusedBy", tree.RefusedBy)
	writeDeleteKinds(&b, "Under", tree.Under)
	b.WriteString("}")

	return b.String()
}

// writeDeleteKinds writes one list of a description's kinds, and nothing for an empty
// one.
func writeDeleteKinds(b *strings.Builder, field string, kinds []*deleteKind) {
	if len(kinds) == 0 {
		return
	}

	fmt.Fprintf(b, "%s: []resource.DeleteKind{\n", field)
	for _, kind := range kinds {
		b.WriteString("{\n")
		fmt.Fprintf(b, "Resource: %q,\n", kind.Table)
		fmt.Fprintf(b, "Words: resource.DeleteWords{One: %q, Many: %q},\n", kind.One, kind.Many)
		fmt.Fprintf(b, "Key: %s,\n", stringsLiteral(kind.Key))
		fmt.Fprintf(b, "Cost: resource.DeleteCost{IndexEntries: %d, Files: %d},\n", kind.IndexEntries, kind.Files)
		b.WriteString("Paths: []resource.DeletePath{\n")
		for _, path := range kind.Paths {
			b.WriteString("{\n")
			for _, link := range path {
				fmt.Fprintf(b, "{Columns: %s, Table: %q, References: %s},\n", stringsLiteral(link.Columns), link.Table, stringsLiteral(link.References))
			}
			b.WriteString("},\n")
		}
		b.WriteString("},\n")
		if len(kind.RemovedWith) > 0 {
			fmt.Fprintf(b, "RemovedWith: %s,\n", stringsLiteral(kind.RemovedWith))
		}
		b.WriteString("},\n")
	}
	b.WriteString("},\n")
}

// stringsLiteral is a list of strings as a Go slice literal.
func stringsLiteral(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, fmt.Sprintf("%q", value))
	}

	return "[]string{" + strings.Join(quoted, ", ") + "}"
}
