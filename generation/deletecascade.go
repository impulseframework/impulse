package generation

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/generation/parser/genlang"
	"github.com/impulseframework/impulse/internal/words"
	"github.com/impulseframework/impulse/resource"
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
// in both lists, or twice in one, has no one meaning. The record's own kind is read
// like any other: the schema says whether a row of it is under another
// (declaredDeleteTree).
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
	// MayBeEmpty marks a step the row can be taken off without being deleted: a foreign
	// key with a column that takes NULL and is no part of the row's own key
	// (tableMetadata.mayBeEmpty). An interleave is never one, since a row's key is
	// never changed. Like Cascades it is no part of what makes two links the same step.
	MayBeEmpty bool
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
	// Tenant is how a row of the kind resolves to its tenant (resource.DeleteKind.Tenant
	// and resource.DeleteTree.Tenant): the kind's @domain, and for the tenant record its
	// key column, the row being its tenant. It is nil for a kind whose rows are no
	// tenant's, and for the tenant record under a tenant.
	Tenant *resource.DomainBindingData
	// Within is the kind's links to itself: a row under a row of its own kind
	// (selfLinks). It is written for the taken kinds alone, since a delete that takes
	// a row takes the rows of its kind under it too. The record's own kind has none:
	// its links to itself are its ways up to the record.
	Within []deleteLink
	// Deletes marks a kind the description is written with the delete of one of its
	// rows for (resource.DeleteKind.Rows): each taken kind of a record that declares
	// its cascade, and of the tenant record. Rule marks one that declares a delete rule
	// (@validateDeleteType), which that delete registers.
	Deletes bool
	Rule    bool
	// Trusted marks a kind whose row's delete is not armed against the caller of the
	// record's delete: each kind the tenant record's delete takes. The grant to delete
	// a tenant answers for every row under it, which is what a tenant's delete means,
	// and a person who may delete a tenant need hold no role in it. The kind's delete
	// rule is asked all the same.
	Trusted bool
	// Subject marks an armed kind whose record decides what a person may do
	// (resourceInfo.SubjectGuarded): the delete of one of its rows also asks the
	// record's questions as a removal (RefuseImpersonatorAs<Name>Subject), as the
	// record's own write does. It is set once every record's subject columns are known
	// (attachSubjectKinds), and never on a Trusted kind.
	Subject bool
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
// "ship class" and "ship classes", "PilotID" is "pilot id" and "pilot IDs"). The tenant
// record takes the label it declares.
func (c *client) deleteWords(res *resourceInfo) (one, many string) {
	name := res.Name()
	if res.IsTenant && res.TenantLabel != "" {
		name = res.TenantLabel
	}

	return words.InWords(name), words.InWords(c.pluralize(name))
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

// mayBeEmpty reports whether a row of the table can leave the columns of a foreign key
// empty: one of them takes NULL and is no part of the row's key. The database holds a
// foreign key only for a row with a value in every column of it, so such a row is under
// nothing by that key once the column is cleared, and a key column is never changed.
func (t *tableMetadata) mayBeEmpty(columns []string) bool {
	return slices.ContainsFunc(columns, func(name string) bool {
		column, ok := t.Columns[name]

		return ok && column.IsNullable && !column.IsPrimaryKey
	})
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
// cascades when any of them does, since the database then removes the row, and may be
// empty only when each of them may. A table's foreign keys to itself are not among them
// (selfLinks).
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
			up = addDeleteLink(up, foreignKey.link(table))
		}
		if len(up) > 0 {
			links[name] = up
		}
	}

	return links
}

// selfLinks is every table's foreign keys to itself: a row under a row of its own kind
// (a folder in a folder). No way up to a record is walked through one, so schemaLinks
// leaves them out; a plain delete of a row counts the rows that name it (kindsUnder),
// and a declared delete follows them row by row (declaredDeleteTree).
func (c *client) selfLinks() map[string][]deleteLink {
	links := make(map[string][]deleteLink)
	for name, table := range c.tableMap {
		for _, foreignKey := range table.ForeignKeys {
			if foreignKey.ReferencedTable == name {
				links[name] = addDeleteLink(links[name], foreignKey.link(table))
			}
		}
	}

	return links
}

// link is the foreign key as a step up to the table it references, from the table that
// holds it.
func (f *foreignKeyMeta) link(from *tableMetadata) deleteLink {
	return deleteLink{
		Columns:    f.Columns,
		Table:      f.ReferencedTable,
		References: f.ReferencedColumns,
		Cascades:   f.DeleteRule == cascadeDeleteAction,
		MayBeEmpty: from.mayBeEmpty(f.Columns),
	}
}

// addDeleteLink adds a step to a table's steps, folding it into the same step where
// that is there already: the step cascades when either does, and may be empty only when
// both may, since the row is held by each of them.
func addDeleteLink(up []deleteLink, link deleteLink) []deleteLink {
	if i := slices.IndexFunc(up, link.equal); i >= 0 {
		up[i].Cascades = up[i].Cascades || link.Cascades
		up[i].MayBeEmpty = up[i].MayBeEmpty && link.MayBeEmpty

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

// kindsUnder is the kinds whose rows refuse a plain delete of the record, by table
// name: each kind with a step straight up to the record that the database does not
// follow, and no way up to the record that the database follows all along, since the
// database removes that kind's rows with the record. The record's own kind is one
// where a row names another row of its kind and no such link is followed by the
// database. Each kind carries those steps alone, one path each. A table no resource
// declares has no words and no grant to ask of, and is left to the database's refusal.
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
		kinds = append(kinds, c.deleteKindOf(kind, paths))
	}
	followed := slices.ContainsFunc(self[record], func(link deleteLink) bool {
		return link.Cascades
	})
	if paths := straight(self[record]); len(paths) > 0 && !followed {
		kinds = append(kinds, c.deleteKindOf(res, paths))
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
			tree, err := c.tenantDeleteTree(res, resources, byTable, links, self)
			if err != nil {
				return err
			}
			res.DeleteTree = tree
		case res.deleteCascade != nil:
			tree, err := c.declaredDeleteTree(res, byTable, byName, links, self)
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

// attachSubjectKinds marks each armed kind of every record's description whose record
// decides what a person may do (deleteKind.Subject), so the delete of a row taken with
// a record asks what the row's own removal asks. It runs once the run's last pass has
// given every record the columns facts are read through (attachSubjectReadThroughs),
// which is after the descriptions are built. A kind the tenant record's delete takes
// (Trusted) is never marked.
func (r *resourceGenerator) attachSubjectKinds() {
	byName := make(map[string]*resourceInfo, len(r.resources))
	for _, res := range r.resources {
		byName[res.Name()] = res
	}
	for _, res := range r.resources {
		if res.DeleteTree == nil {
			continue
		}
		for _, kind := range res.DeleteTree.Taken {
			if !kind.Deletes || kind.Trusted {
				continue
			}
			if record, ok := byName[kind.Name]; ok {
				kind.Subject = record.SubjectGuarded()
			}
		}
	}
}

// declaredDeleteTree holds a record's @deleteCascade to the schema and writes its
// description. Every kind it names is a resource with rows under the record, and every
// kind with rows under the record is named: taken with it, or refusing its delete. What
// is under a refusing kind alone needs no place in either list, since no row of it is
// under the record without a row of the kind that refuses.
//
// The record's own kind is one of them where a row of it is under another (a folder in
// a folder): it is named like any other kind, and its ways up are its links to itself.
// Taken, it is the last kind deleted before the record, since everything under a row
// of it goes first. A taken kind with a link to itself carries the link (Within), so
// the delete finds a row under a row of the kind, however deep, and each taken kind is
// written with the delete of one of its rows (Deletes).
func (c *client) declaredDeleteTree(res *resourceInfo, byTable, byName map[string]*resourceInfo, links, self map[string][]deleteLink) (*deleteTree, error) {
	record := c.pluralize(res.Name())
	decl := res.deleteCascade
	all := pathsUnder(links, record, nil)
	// The record's ways up from a row of its own kind: one for each link to itself.
	for _, link := range self[record] {
		all[record] = append(all[record], []deleteLink{link})
	}

	taken, refusing, err := c.namedDeleteKinds(res, byName, all)
	if err != nil {
		return nil, err
	}

	// Every kind with a way up to the record that passes no refusing kind has its place
	// in a list, the record's own kind among them.
	reached := pathsUnder(links, record, refusing)
	if len(all[record]) > 0 {
		reached[record] = all[record]
	}
	if err := unlistedDeleteKind(res, byTable, reached, taken, refusing); err != nil {
		return nil, err
	}

	// A way in reached passes no refusing kind, so every table on it is a taken kind; a
	// named kind with none is under the record through a refusing kind alone.
	tree := &deleteTree{Record: c.deleteKindOf(res, nil)}
	var own *deleteKind
	for _, name := range decl.Taken {
		table := c.pluralize(name)
		paths := reached[table]
		if len(paths) == 0 {
			return nil, errors.Newf("@%s on %s takes %s, but a %s is under a %s only through a kind that refuses the delete, so there is never one to take. Remove %s from the list.",
				deleteCascadeKeyword, res.Name(), name, name, res.Name(), name)
		}
		kind := c.deleteKindOf(byName[name], paths)
		kind.Deletes, kind.Rule = true, byName[name].HasValidateDeleteType()
		if table == record {
			own = kind

			continue
		}
		kind.Within = self[table]
		tree.Taken = append(tree.Taken, kind)
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
	if own != nil {
		// A row of the record's own kind goes after everything under it, whatever the
		// kind, and before the record.
		tree.Taken = append(tree.Taken, own)
	}

	return tree, nil
}

// namedDeleteKinds holds each kind a record's @deleteCascade names to the schema and
// answers them by table: the kinds taken with the record, then the kinds that refuse its
// delete. all holds each table's ways up to the record, the record's own among them.
func (c *client) namedDeleteKinds(res *resourceInfo, byName map[string]*resourceInfo, all map[string][][]deleteLink) (taken, refusing map[string]bool, err error) {
	record := c.pluralize(res.Name())
	decl := res.deleteCascade
	taken = make(map[string]bool, len(decl.Taken))
	refusing = make(map[string]bool, len(decl.RefusedBy))
	for _, name := range slices.Concat(decl.Taken, decl.RefusedBy) {
		kind, ok := byName[name]
		if !ok {
			return nil, nil, errors.Newf("@%s on %s names %s, which is not a resource of this package. Name the resources whose rows are under a %s.",
				deleteCascadeKeyword, res.Name(), name, res.Name())
		}
		table := c.pluralize(name)
		if table == record && len(all[table]) == 0 {
			return nil, nil, errors.Newf("@%s on %s names %s, but no foreign key puts a %s under a %s. Remove %s from the list.",
				deleteCascadeKeyword, res.Name(), name, name, res.Name(), name)
		}
		if len(all[table]) == 0 {
			return nil, nil, errors.Newf("@%s on %s names %s, but no foreign key or interleave puts a %s under a %s. Remove %s from the list.",
				deleteCascadeKeyword, res.Name(), name, name, res.Name(), name)
		}
		if slices.Contains(decl.Taken, name) {
			if kind.IsEnumeration() {
				return nil, nil, errors.Newf("@%s on %s takes %s, which is a read-only table (@%s): its rows are never deleted. List %s under %s, or remove it.",
					deleteCascadeKeyword, res.Name(), name, enumerateKeyword, name, deleteCascadeRefusedByKey)
			}
			taken[table] = true

			continue
		}
		refusing[table] = true
	}

	return taken, refusing, nil
}

// unlistedDeleteKind refuses a record's @deleteCascade that leaves out a kind with rows
// under the record: reached holds each table with a way up to the record that passes no
// refusing kind, and every one of them is taken or refuses the delete.
func unlistedDeleteKind(res *resourceInfo, byTable map[string]*resourceInfo, reached map[string][][]deleteLink, taken, refusing map[string]bool) error {
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
			return errors.Newf("@%s on %s: the table %s is under a %s (%s), and no resource of this package declares it, so a delete in code could not remove its rows. Declare a resource for %s and list it.",
				deleteCascadeKeyword, res.Name(), table, res.Name(), way, table)
		}

		return errors.Newf("@%s on %s: a %s is under a %s (%s), and the list names it neither as taken nor under %s. List %s to delete it with the %s, or under %s to keep a %s that has one from being deleted.",
			deleteCascadeKeyword, res.Name(), kind.Name(), res.Name(), way, deleteCascadeRefusedByKey, kind.Name(), res.Name(), deleteCascadeRefusedByKey, res.Name())
	}

	return nil
}

// tenantDeleteTree writes the tenant record's description, which the library declares:
// a tenant's delete takes every kind of the application's rows that carry the tenant's
// key or lead to a row that does, and nothing refuses it. A row carries the tenant's key
// by a foreign key to the tenant record or as the column a tenant-scoped resource is
// read by.
//
// Each kind is written with the delete of one of its rows (Deletes), so the tenant's
// delete takes the rows in code, each through its kind's own delete: its delete rule
// (Rule), its change record, its files. That delete is not armed against the caller
// (Trusted). A kind with a link to itself carries the link (Within), so a row under a
// row of its kind is found however deep.
func (c *client) tenantDeleteTree(tenant *resourceInfo, resources []*resourceInfo, byTable map[string]*resourceInfo, links, self map[string][]deleteLink) (*deleteTree, error) {
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
	if len(tenantKey) == 1 {
		// The tenant record's row is its tenant: a row under it is of the tenant whose
		// key is the record's.
		tree.Record.Tenant = &resource.DomainBindingData{Column: tenantKey[0]}
	}
	for table, paths := range reached {
		kind, ok := byTable[table]
		if !ok || kind.IsEnumeration() {
			// A table no resource declares, the library's own among them, is not the
			// application's row to delete in code.
			continue
		}
		taken := c.deleteKindOf(kind, paths)
		taken.Deletes, taken.Rule, taken.Trusted = true, kind.HasValidateDeleteType(), true
		taken.Within = self[table]
		tree.Taken = append(tree.Taken, taken)
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
// the record (none for the record itself), and the tenant its rows resolve to where the
// resource declares one (@domain). The tenant record declares none: a tenant row under
// a tenant row is of no tenant, and the tenant record's own description says its row
// is its tenant (tenantDeleteTree).
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
	if !res.IsTenant && res.DomainBinding != nil {
		kind.Tenant = &resource.DomainBindingData{
			Column: fieldColumn(res.DomainBinding.Anchor),
			Path:   collectionHops(res.DomainBinding.Path),
		}
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

// TakesRowsUnder reports whether a delete of the record takes rows under it when it is
// told to: the record declares its cascade, which always lists a kind as taken, or it
// is the tenant record and the application has a kind of row under a tenant. Its
// delete patch then carries WithEverythingUnder.
func (r *resourceInfo) TakesRowsUnder() bool {
	return (r.deleteCascade != nil || r.IsTenant) && r.DeleteTree != nil && len(r.DeleteTree.Taken) > 0
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
	b.WriteString(deleteTenantLiteral(tree.Record.Tenant))
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
		b.WriteString(deleteTenantLiteral(kind.Tenant))
		b.WriteString("Paths: []resource.DeletePath{\n")
		for _, path := range kind.Paths {
			b.WriteString("{\n")
			for _, link := range path {
				b.WriteString(linkLiteral(link))
			}
			b.WriteString("},\n")
		}
		b.WriteString("},\n")
		if len(kind.Within) > 0 {
			b.WriteString("Within: []resource.DeleteLink{\n")
			for _, link := range kind.Within {
				b.WriteString(linkLiteral(link))
			}
			b.WriteString("},\n")
		}
		if kind.Deletes {
			writeTakenRows(b, kind)
		}
		b.WriteString("},\n")
	}
	b.WriteString("},\n")
}

// deleteTenantLiteral is the tenant a row resolves to as the Go value a description is
// written with (DeleteKind.Tenant, DeleteTree.Tenant), on a line of its own, and nothing
// for a row that is no tenant's.
func deleteTenantLiteral(tenant *resource.DomainBindingData) string {
	if tenant == nil {
		return ""
	}

	return fmt.Sprintf("Tenant: &resource.DomainBindingData{Column: %q%s},\n", tenant.Column, bindingHopsLiteral(tenant.Path))
}

// linkLiteral is one step as the Go value a description is written with
// (resource.DeleteLink), on a line of its own.
func linkLiteral(link deleteLink) string {
	return fmt.Sprintf("{Columns: %s, Table: %q, References: %s%s},\n", stringsLiteral(link.Columns), link.Table, stringsLiteral(link.References), mayBeEmptyLiteral(link))
}

// writeTakenRows writes how a delete of the record deletes a row of a taken kind with
// it (resource.NewTakenRows): the kind's generated delete patch around the row's patch
// set, with the kind's delete rule registered where it declares one, armed against the
// caller of the record's delete unless that call is trusted. An armed row of a record
// that decides what a person may do is also asked that record's questions as a removal
// (RefuseImpersonatorAs<Name>Subject), which the record's own write asks. A kind the
// tenant record's delete takes (Trusted) is never armed: the closure takes no caller,
// and only the kind's delete rule is asked of the row.
func writeTakenRows(b *strings.Builder, kind *deleteKind) {
	if kind.Trusted {
		if !kind.Rule {
			fmt.Fprintf(b, "Rows: resource.NewTakenRows(func(*resource.PatchSet[%s], *resource.Caller) {}),\n", kind.Name)

			return
		}
		fmt.Fprintf(b, "Rows: resource.NewTakenRows(func(patchSet *resource.PatchSet[%s], _ *resource.Caller) {\n", kind.Name)
		fmt.Fprintf(b, "patch := &%sDeletePatch{patchSet: patchSet}\n", kind.Name)
		b.WriteString("patch.registerValidateFunc()\n")
		b.WriteString("}),\n")

		return
	}
	fmt.Fprintf(b, "Rows: resource.NewTakenRows(func(patchSet *resource.PatchSet[%s], caller *resource.Caller) {\n", kind.Name)
	fmt.Fprintf(b, "patch := &%sDeletePatch{patchSet: patchSet}\n", kind.Name)
	if kind.Rule {
		b.WriteString("patch.registerValidateFunc()\n")
	}
	b.WriteString("if !caller.IsZero() {\n")
	b.WriteString("patch.Enforce(caller)\n")
	if kind.Subject {
		b.WriteString("patchSet.RegisterDeleteCheckFunc(func(ctx context.Context, txn resource.ReadWriteTransaction) error {\n")
		fmt.Fprintf(b, "return RefuseImpersonatorAs%sSubject(ctx, txn, resource.OperationDelete, patchSet.PrimaryKey(), patchSet)\n", kind.Name)
		b.WriteString("})\n")
	}
	b.WriteString("}\n")
	b.WriteString("}),\n")
}

// mayBeEmptyLiteral is the field a step that may be empty adds to its literal, and
// nothing for any other step.
func mayBeEmptyLiteral(link deleteLink) string {
	if !link.MayBeEmpty {
		return ""
	}

	return ", MayBeEmpty: true"
}

// stringsLiteral is a list of strings as a Go slice literal.
func stringsLiteral(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, fmt.Sprintf("%q", value))
	}

	return "[]string{" + strings.Join(quoted, ", ") + "}"
}
