package generation

import (
	"cmp"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/generation/parser"
	"github.com/impulseframework/impulse/generation/parser/genlang"
	"github.com/impulseframework/impulse/resource"
)

// markDeletedWith marks each field of a table-backed struct that says a row of the record
// goes with the group or the account of an auth's its column names (@deletedWith). The
// mark is held to the schema once the run's resources are all known
// (resolveLibraryDeleteTrees).
func markDeletedWith(pStruct *parser.Struct, fields []*resourceField, annotations genlang.StructAnnotations) {
	for i, structField := range pStruct.Fields() {
		if !annotations.Fields[i].Has(deletedWithKeyword) {
			continue
		}
		for _, field := range fields {
			if field.Field == structField {
				field.deletedWith = true
			}
		}
	}
}

// rejectDeletedWith refuses @deletedWith on a field of a kind that has no rows of a table
// to delete: a view, a computed resource and a method. what is the kind as the sentence
// says it, with its article and its annotation ("a view (@virtual)").
func rejectDeletedWith(pStruct *parser.Struct, annotations genlang.StructAnnotations, what string) error {
	for i, field := range pStruct.Fields() {
		if annotations.Fields[i].Has(deletedWithKeyword) {
			return errors.Newf("@%s on %s.%s: %s is %s, and only a row of a table goes with a group or an account. Put @%s on the column of the table's own @%s struct, or remove it.",
				deletedWithKeyword, pStruct.Name(), field.Name(), pStruct.Name(), what, deletedWithKeyword, resourceKeyword)
		}
	}

	return nil
}

// ApplicationCanName reports whether the record is one of an auth's that a column of the
// application's can name and say its row goes with it (@deletedWith): an auth's group or
// its account record. Its description is written a second time over the application's
// records (resolveLibraryDeleteTrees), and its delete patch can leave the application's
// rows out (WithLibraryRowsAlone).
func (r *resourceInfo) ApplicationCanName() bool {
	return r.isGroupRecord() || r.isAccountRecord()
}

// isGroupRecord reports whether the record is an auth's group: its declaration over the
// groups table.
func (r *resourceInfo) isGroupRecord() bool {
	return r.identity != nil && !r.IsVirtual && r.Name() == r.identity.auth+"Group"
}

// isAccountRecord reports whether the record is an auth's account record: its declaration
// over the session library's users table.
func (r *resourceInfo) isAccountRecord() bool {
	return r.identity != nil && !r.IsVirtual && r.Name() == r.identity.auth+"SessionUser"
}

// TakenFromAnotherPackage reports whether a delete of a record of another package takes
// rows of the record: a record of an auth's whose description takes the record's kind
// (resolveLibraryDeleteTrees). The record's file then declares the function that delete
// deletes a row through (<Name>TakenRows).
func (r *resourceInfo) TakenFromAnotherPackage() bool {
	return r.takenFromAnotherPackage
}

// TakenRowsLiteral is how a delete of a record of another package deletes a row of the
// record (resource.NewTakenRows), as the Go value the record's <Name>TakenRows returns:
// what a description of the record's own package writes for a kind of it that is not
// trusted, with the record's delete rule and, where the record decides what a person may
// do, its questions as a removal.
func (r *resourceInfo) TakenRowsLiteral() string {
	var b strings.Builder
	writeTakenRowsValue(&b, &deleteKind{Name: r.Name(), Rule: r.HasValidateDeleteType(), Subject: r.SubjectGuarded()})

	return b.String()
}

// resolveLibraryDeleteTrees writes a second time the description of each record of an
// auth's that a column of the application's can name, once the run's resources are all
// known: an auth's group and its account record (libraryDeleteTree). It first holds each
// column that says a row goes with what it names (@deletedWith) to the schema
// (deletedWithStep), and refuses every one that names no group and no account of an
// auth's. The application's records are the run's table-backed resources of no auth's
// that are no view and have a table in the schema. An account's description names the
// application's tenant record, where it has one, for its sentences (resource.DeleteTenants).
func (r *resourceGenerator) resolveLibraryDeleteTrees(resources []*resourceInfo) error {
	application := make(map[string]*resourceInfo)
	var records []*resourceInfo
	library := make(map[string]*resourceInfo)
	var named []*resourceInfo
	var tenant *resourceInfo
	for _, res := range resources {
		table := r.pluralize(res.Name())
		if _, ok := r.tableMap[table]; !ok || res.IsVirtual {
			continue
		}
		switch {
		case res.identity == nil:
			application[table] = res
			records = append(records, res)
			if res.IsTenant {
				tenant = res
			}
		case res.ApplicationCanName():
			library[table] = res
			named = append(named, res)
		}
	}

	// marked holds, by the library record's table, each record's steps up to it from the
	// columns that say its row goes with that record, by the record's table.
	marked := make(map[string]map[string][]deleteLink)
	var errs []error
	for _, res := range records {
		for _, field := range res.Fields {
			if !field.deletedWith {
				continue
			}
			step, err := r.deletedWithStep(res, field, application, library)
			if err != nil {
				errs = append(errs, err)

				continue
			}
			if marked[step.Table] == nil {
				marked[step.Table] = make(map[string][]deleteLink)
			}
			table := r.pluralize(res.Name())
			if !slices.ContainsFunc(marked[step.Table][table], step.equal) {
				marked[step.Table][table] = append(marked[step.Table][table], step)
			}
		}
	}
	if len(errs) > 0 {
		return errors.Wrapf(errors.Join(errs...), "@%s annotation error", deletedWithKeyword)
	}

	var tenants *resource.DeleteTenants
	if tenant != nil {
		tenants = &resource.DeleteTenants{
			Table: r.pluralize(tenant.Name()),
			Key:   spannerColumn(tenant.PrimaryKey()),
			Word:  r.directoryTenantWord(tenant),
		}
		if tenant.TenantNameField != nil {
			tenants.Name = spannerColumn(tenant.TenantNameField)
		}
	}
	links := r.schemaLinks()
	self := r.selfLinks()
	cascading := cascadingLinks(links)
	for _, record := range named {
		r.libraryDeleteTree(record, resources, records, application, marked[r.pluralize(record.Name())], tenants, links, self, cascading)
	}

	return nil
}

// deletedWithStep holds a column that says a row of the record goes with what it names
// (@deletedWith) to the schema, and answers the column's step up to the table it names:
// the column is one the schema holds, alone, by a foreign key to the key of the groups
// table or the users table of an auth's (library). A column of the tenant record is
// refused first, whatever it names: a tenant is deleted by its own delete. A column the
// schema holds to a record of the application's (the tenant record and a read-only table
// each in a sentence of its own), a read-only table and any other column are refused,
// each in a sentence that says what to write.
func (c *client) deletedWithStep(res *resourceInfo, field *resourceField, application, library map[string]*resourceInfo) (deleteLink, error) {
	if res.IsTenant {
		return deleteLink{}, errors.Newf("@%s on %s.%s: %s is the tenant record, and a tenant is deleted by its own delete. Remove @%s.",
			deletedWithKeyword, res.Name(), field.Name(), res.Name(), deletedWithKeyword)
	}
	table := c.tableMap[c.pluralize(res.Name())]
	column := fieldColumn(field)
	for _, foreignKey := range table.ForeignKeys {
		target, ok := application[foreignKey.ReferencedTable]
		if !ok || !slices.Contains(foreignKey.Columns, column) {
			continue
		}
		one, _ := c.deleteWords(target)
		_, many := c.deleteWords(res)
		switch {
		case target.IsTenant:
			return deleteLink{}, errors.Newf("@%s on %s.%s: %s names a tenant, and a tenant's delete takes every row under the tenant already. Remove @%s.",
				deletedWithKeyword, res.Name(), field.Name(), field.Name(), deletedWithKeyword)
		case target.IsEnumeration():
			return deleteLink{}, errors.Newf("@%s on %s.%s: %s names %s, a read-only table (@%s) whose rows are never deleted. Remove @%s.",
				deletedWithKeyword, res.Name(), field.Name(), field.Name(), withArticle(one), enumerateKeyword, deletedWithKeyword)
		}

		return deleteLink{}, errors.Newf("@%s on %s.%s: %s names %s, a record of this application. Say on %s that its %s go with it: @%s(%s).",
			deletedWithKeyword, res.Name(), field.Name(), field.Name(), withArticle(one), target.Name(), many, deleteCascadeKeyword, res.Name())
	}
	if res.IsEnumeration() {
		return deleteLink{}, errors.Newf("@%s on %s.%s: %s is a read-only table (@%s): its rows are never deleted. Remove @%s.",
			deletedWithKeyword, res.Name(), field.Name(), res.Name(), enumerateKeyword, deletedWithKeyword)
	}
	for _, foreignKey := range table.ForeignKeys {
		if _, ok := library[foreignKey.ReferencedTable]; ok && slices.Equal(foreignKey.Columns, []string{column}) && slices.Equal(foreignKey.ReferencedColumns, c.tableMap[foreignKey.ReferencedTable].keyColumns()) {
			return foreignKey.link(table), nil
		}
	}

	return deleteLink{}, errors.Newf("@%s on %s.%s: the schema holds %s to no group and no account of an auth by a foreign key. @%s goes on the column that names a group or an account of the library's; add the foreign key, or remove @%s.",
		deletedWithKeyword, res.Name(), field.Name(), column, deletedWithKeyword, deletedWithKeyword)
}

// libraryDeleteTree writes the description of an auth's group or account record a second
// time, over the application's records. Under is the kinds whose rows refuse a plain
// delete of the record (kindsUnder), the application's records and the auth's own
// declarations known by table, and no other auth's. Taken is the application's kinds the
// record's delete takes, deepest first, and then the record's own (a group's members and
// its roles, as its first description has them). marked holds, by the record's table,
// each record's steps up to the library's record from the columns that say its row goes
// with it:
//
//   - The record is taken, its ways those steps alone. A column of the record that names
//     the group and says nothing is no way of the taken kind: it stays under Under.
//   - Where the record declares its cascade, each kind its delete takes is taken too,
//     each of its ways up to the record followed by each of the steps, and each kind that
//     refuses its delete refuses the group's, its ways extended the same way. The
//     record's own kind among what its delete takes is no second kind: the record carries
//     its links to itself (Within).
//   - Where the record declares none, each kind whose rows refuse a plain delete of it
//     refuses the group's, its ways extended the same way.
//
// Kinds of one table from more than one record or column are one kind in a list, their
// ways joined, each once. Every kind of the application's is checked as whoever asked for
// the delete (AsCaller), but a kind an account's delete takes, which goes on the
// permission to delete the account where it reaches (WithinReach); and a taken one is
// deleted through the function its record's package declares for it (Package,
// <Name>TakenRows). An account's description with a kind marked WithinReach names the
// tenants (tenants, nil where the application has no tenant record). RefusedBy is the
// record's own, then the application's, by table. A record no record of the
// application's names keeps the lists its first description has, and one with none has
// none.
func (c *client) libraryDeleteTree(record *resourceInfo, resources, records []*resourceInfo, application map[string]*resourceInfo, marked map[string][]deleteLink, tenants *resource.DeleteTenants, links, self, cascading map[string][]deleteLink) {
	byTable := make(map[string]*resourceInfo, len(application))
	for table, res := range application {
		byTable[table] = res
	}
	for _, res := range resources {
		if res.identity == record.identity && !res.IsVirtual {
			byTable[c.pluralize(res.Name())] = res
		}
	}
	under := c.kindsUnder(record, byTable, links, self, cascading)
	for _, kind := range under {
		kind.AsCaller = application[kind.Table] != nil
	}

	pkg := c.resource.Package()
	taken, refusing := c.applicationKinds(records, marked, self)
	account := record.isAccountRecord()
	for _, kind := range taken {
		kind.Package = pkg
		if account {
			kind.WithinReach = true
		} else {
			kind.AsCaller = true
		}
		if res, ok := application[kind.Table]; ok {
			res.takenFromAnotherPackage = true
		}
	}
	for _, kind := range refusing {
		kind.AsCaller = true
	}
	sortDeepestFirst(taken)
	slices.SortFunc(refusing, func(a, b *deleteKind) int {
		return cmp.Compare(a.Table, b.Table)
	})

	tree := record.DeleteTree
	if tree == nil {
		if len(under)+len(taken)+len(refusing) == 0 {
			return
		}
		tree = &deleteTree{Record: c.deleteKindOf(record, nil)}
	}
	described := &deleteTree{
		Record:    tree.Record,
		Taken:     slices.Concat(taken, tree.Taken),
		RefusedBy: slices.Concat(tree.RefusedBy, refusing),
		Under:     under,
	}
	if account && len(taken) > 0 {
		described.Tenants = tenants
	}
	record.DeleteTree = described
}

// applicationKinds is what the application's records that name a library record add to its
// description, through the steps marked from each up to it: a marked record and the kinds
// its own delete takes (@deleteCascade) are taken, the kinds that refuse its delete
// refuse, and the kinds under a record with no @deleteCascade refuse.
func (c *client) applicationKinds(records []*resourceInfo, marked, self map[string][]deleteLink) (taken, refusing []*deleteKind) {
	for _, res := range records {
		table := c.pluralize(res.Name())
		steps := marked[table]
		if len(steps) == 0 {
			continue
		}
		paths := make([][]deleteLink, 0, len(steps))
		for _, step := range steps {
			paths = append(paths, []deleteLink{step})
		}
		own := c.deleteKindOf(res, paths)
		own.Deletes, own.Rule = true, res.HasValidateDeleteType()
		switch {
		case res.deleteCascade != nil && res.DeleteTree != nil:
			for _, kind := range res.DeleteTree.Taken {
				if kind.Table == table {
					own.Within = self[table]

					continue
				}
				taken = addLibraryKind(taken, extendedKind(kind, steps))
			}
			for _, kind := range res.DeleteTree.RefusedBy {
				refusing = addLibraryKind(refusing, extendedKind(kind, steps))
			}
		case res.DeleteTree != nil:
			for _, kind := range res.DeleteTree.Under {
				refusing = addLibraryKind(refusing, extendedKind(kind, steps))
			}
		}
		taken = addLibraryKind(taken, own)
	}

	return taken, refusing
}

// extendedKind is a kind of a record's description as a kind of a group's or an
// account's: a copy whose ways up to the record are each followed by each of the record's
// steps up to the group or the account.
func extendedKind(kind *deleteKind, steps []deleteLink) *deleteKind {
	extended := *kind
	extended.Paths = nil
	for _, path := range kind.Paths {
		for _, step := range steps {
			extended.Paths = append(extended.Paths, append(slices.Clone(path), step))
		}
	}
	extended.Within = slices.Clone(kind.Within)
	extended.Trusted, extended.Subject = false, false

	return &extended
}

// addLibraryKind adds a kind to a list of a group's or an account's description, joining
// it to the kind of the same table where the list holds one: each way and each link to
// itself once.
func addLibraryKind(kinds []*deleteKind, kind *deleteKind) []*deleteKind {
	i := slices.IndexFunc(kinds, func(k *deleteKind) bool { return k.Table == kind.Table })
	if i < 0 {
		return append(kinds, kind)
	}
	for _, path := range kind.Paths {
		if !slices.ContainsFunc(kinds[i].Paths, func(way []deleteLink) bool { return slices.EqualFunc(way, path, deleteLink.equal) }) {
			kinds[i].Paths = append(kinds[i].Paths, path)
		}
	}
	for _, link := range kind.Within {
		if !slices.ContainsFunc(kinds[i].Within, link.equal) {
			kinds[i].Within = append(kinds[i].Within, link)
		}
	}

	return kinds
}
