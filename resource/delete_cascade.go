package resource

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"google.golang.org/api/iterator"
)

// A delete of a record that declares what goes with it (@deleteCascade) takes those rows
// in code, inside the delete's own transaction, when the request says so
// (PatchSet.DeleteEverythingUnder). The rows are found a kind at a time, by one statement
// that follows the kind's declared ways up to the record (DeleteKind.Paths). A kind that
// sits under itself (DeleteKind.Within) is found level by level, by the same statement
// with the kind's link to itself repeated, until a level finds nothing.
//
// Before anything is deleted, the kinds are checked as their own deletes would check
// them: the caller's grants on each kind once, then, for a kind whose rows have anything
// to be asked (what the kind's generated code asks of a delete, a condition on the
// grant, a tenant the row must be found in, the kind's delete rule), each of its rows,
// read a page at a time with the answers of the grant's conditions. The whole is sized
// against what one commit takes (DeleteTree.Mutations). Nothing goes unless everything
// may go, and a refusal says why. Then the rows are deleted deepest first and the record
// last, each through the delete of one row (bufferTaken), so each has its change record,
// is announced to open pages, and has its stored files released.
//
// A delete that does not say it takes the rows, of a record that has some, deletes
// nothing and is answered with the question a person is asked first: "Delete this
// client and everything under it: 2 client contacts. This can't be undone." It is a 428,
// answered once the whole check has passed, so the question is never asked of a delete
// that would be refused.

// takenPageRows is the most rows one read of a page of a kind's rows brings.
const takenPageRows = 1000

// TakenRows is how a delete that takes a kind's rows reaches the delete of one of them.
// The generator writes one for each kind under DeleteTree.Taken of a record that
// declares its cascade (NewTakenRows), so a taken row goes through the kind's own
// delete: its grants, what its generated write asks of a removal, its rule
// (@validateDeleteType), its change record, its files.
type TakenRows interface {
	// columns are the kind's columns on the database type, each with the field that
	// holds it and the field's Go type.
	columns(dbType DBType) map[string]takenColumn
	// row is the delete of the kind's row with the given key, field by field in key
	// order, armed against the caller, or trusted for a zero Caller.
	row(fields []accesstypes.Field, values []any, caller *Caller) takenRow
	// kind is the delete of the kind's rows as one, armed as each row's is, with no key:
	// what the kind asks before any of its rows is read.
	kind(caller *Caller) takenKind
	// tracked reports whether a change record is written for the removal of a row.
	tracked() bool
}

// takenColumn is a column of a kind's table as its resource holds it.
type takenColumn struct {
	field accesstypes.Field
	typ   reflect.Type
}

// takenRow is the delete of one row that a record's delete takes with it: the patch set
// of the row's own delete.
type takenRow interface {
	checkTaken(ctx context.Context, txn ReadWriteTransaction, asks *takenAsks, answers []spanner.NullBool) (byRule bool, err error)
	bufferTaken(ctx context.Context, txn ReadWriteTransaction, eventSource ...string) error
}

// takenKind is the delete of a kind's rows as one (TakenRows.kind): the caller's grants
// on the kind's resource, and what the kind's rows are asked.
type takenKind interface {
	checkPermissions(ctx context.Context, dbType DBType) error
	takenAsks(dbType DBType) (*takenAsks, error)
}

// takenRows is TakenRows for one resource.
type takenRows[R Resourcer] struct {
	arm func(patchSet *PatchSet[R], caller *Caller)
}

// NewTakenRows returns how a delete of a record deletes a row of the resource R with
// it. arm makes the patch set of one row's delete into the resource's own delete: it
// registers the resource's delete rule, and arms the delete against the caller unless
// the caller is zero, which is a trusted call. Where the resource's rows decide what a
// person may do, it also registers what the resource's generated write asks of a
// removal (RegisterDeleteCheckFunc). The generator writes the call, with the resource's
// generated delete patch.
func NewTakenRows[R Resourcer](arm func(patchSet *PatchSet[R], caller *Caller)) TakenRows {
	return &takenRows[R]{arm: arm}
}

// columns are the resource's columns on the database type.
func (r *takenRows[R]) columns(dbType DBType) map[string]takenColumn {
	return columnsOf(NewMetadata[R](), dbType)
}

// row is the delete of the resource's row with the given key.
func (r *takenRows[R]) row(fields []accesstypes.Field, values []any, caller *Caller) takenRow {
	patchSet := NewPatchSet(NewMetadata[R]()).SetPatchType(DeletePatchType)
	for i, field := range fields {
		patchSet.SetKey(field, values[i])
	}
	r.arm(patchSet, caller)

	return patchSet
}

// kind is the delete of the resource's rows as one, with no key.
func (r *takenRows[R]) kind(caller *Caller) takenKind {
	patchSet := NewPatchSet(NewMetadata[R]()).SetPatchType(DeletePatchType)
	r.arm(patchSet, caller)

	return patchSet
}

// tracked reports whether the resource's changes are recorded.
func (r *takenRows[R]) tracked() bool {
	return NewMetadata[R]().trackChanges
}

// columnsOf are a resource's columns on the database type, each with its field and the
// field's Go type.
func columnsOf[R Resourcer](rMeta *Metadata[R], dbType DBType) map[string]takenColumn {
	fields := rMeta.dbFieldMap(dbType)
	columns := make(map[string]takenColumn, len(fields))
	for field, meta := range fields {
		columns[meta.ColumnName] = takenColumn{field: field, typ: meta.fieldType}
	}

	return columns
}

// takenAsks is what a kind's rows are asked, beyond the caller's grants on the kind: the
// question the kind's generated code registered (question) and its delete rule (rule),
// asked of each row, and the answers a page of the rows is read with (terms), one
// boolean each, for the row as its own delete's check reads it: first whether it is in
// the request's tenant (tenancy), then whether each condition the grant carries holds
// (groups, by the resources each names). params are the values the terms name.
type takenAsks struct {
	question bool
	rule     bool
	tenancy  bool
	groups   [][]accesstypes.Resource
	terms    []string
	params   map[string]any
}

// asksRows reports whether the kind's rows have anything to be asked. A kind that asks
// nothing of its rows is not read before it is taken: it is counted.
func (a *takenAsks) asksRows() bool {
	return a.question || a.rule || len(a.terms) > 0
}

// takenAsks is what the delete the patch set carries asks of a row of its resource,
// once its grants have been checked (checkPermissions): its terms are what
// writeCheckStatement reads of the one row a delete names, the conditions lowered over
// the row read under its table's name, and the tenant predicate as a term of its own in
// place of the condition that locates the row.
func (p *PatchSet[Resource]) takenAsks(dbType DBType) (*takenAsks, error) {
	asks := &takenAsks{question: p.deleteCheckFunc != nil, rule: p.validateDeleteFunc != nil}
	groups, err := p.writeConditionGroups()
	if err != nil {
		return nil, err
	}
	tenancy, err := p.mutationTenancy()
	if err != nil {
		return nil, err
	}
	if len(groups) == 0 && !tenancy.locatesRow() {
		return asks, nil
	}
	if dbType != SpannerDBType {
		return nil, errors.Newf("conditional grant and tenancy enforcement is not implemented for %s", dbType)
	}
	q := p.querySet
	if q.collection == nil {
		return nil, errors.Newf("resource %s carries conditional decisions but no generated collection is wired to render them", q.Resource())
	}

	gen := newSQLGenerator(Spanner)
	registry := newParamRegistry()
	bindings, _ := q.collection.Bindings(q.collection.Scope(q.Resource()), q.Resource())
	_, partitioned := q.scope.Domain()
	lctx := &loweringContext{
		outer:       string(q.Resource()),
		bindings:    bindings,
		collection:  q.collection,
		partitioned: partitioned,
	}
	if tenancy.locatesRow() {
		sql, err := q.tenancyPredicate(dbType, registry)
		if err != nil {
			return nil, err
		}
		if sql != "" {
			asks.tenancy = true
			asks.terms = append(asks.terms, sql)
		}
	}
	for _, group := range groups {
		sql, err := lowerToSQL(group.expr, lctx, gen, registry)
		if err != nil {
			return nil, err
		}
		asks.groups = append(asks.groups, group.resources)
		asks.terms = append(asks.terms, "("+sql+")")
	}
	asks.params = make(map[string]any)
	if err := p.mergeCheckParams(registry, asks.params); err != nil {
		return nil, err
	}

	return asks, nil
}

// checkTaken asks of the patch set's row what its own delete asks of it (checkDelete),
// beyond the grants, which were asked of its kind once: the question the generated code
// registered, then the answers a page read of the row (answers, in the order of
// asks.terms), then the delete rule. A row not in the request's tenant is answered as
// no row, and a condition that does not hold for it, or holds NULL, as the grant's
// refusal, both as enforceWriteConditions answers them. byRule says a refusal is the
// row's own reason, the question's or the rule's, whose sentence is passed on.
func (p *PatchSet[Resource]) checkTaken(ctx context.Context, txn ReadWriteTransaction, asks *takenAsks, answers []spanner.NullBool) (byRule bool, err error) {
	if p.deleteCheckFunc != nil {
		if err := p.deleteCheckFunc(ctx, txn); err != nil {
			return true, errors.Wrap(err, "deleteCheckFunc()")
		}
	}

	// The tenant's term, where there is one, comes before the groups'.
	first := 0
	if asks.tenancy {
		first = 1
	}
	for i, answer := range answers {
		if answer.Valid && answer.Bool {
			continue
		}
		if i < first {
			return false, recordNotFound[Resource](p.PrimaryKey().RowID())
		}

		return false, p.querySet.conditionRefused(asks.groups[i-first])
	}

	if p.validateDeleteFunc != nil {
		if err := p.validateDeleteFunc(ctx, txn); err != nil {
			return true, errors.Wrap(err, "validateDeleteFunc()")
		}
	}

	return false, nil
}

// takenQuery writes a kind's path statement at a level for the delete of one record: the
// record's description and key, and the rows of each table the statement leaves out
// (written: the rows the transaction itself deleted, which the refusing kinds' count
// leaves out).
type takenQuery struct {
	tree    *DeleteTree
	key     map[string]any
	written map[accesstypes.Resource][]map[string]any
}

// condition is the condition that a row of the kind, read under its table's name, is
// under the record at the level: by one of its ways at the level (DeleteTree.waysAt),
// and, above level 0, by none of its ways at level 0, so a row the record's key reaches
// by a declared way is counted at level 0 alone. ok is false where the kind has no way
// at the level, or where the transaction deleted more rows of a table the ways read
// than a statement leaves out (deleteExclusionLimit).
func (q *takenQuery) condition(w *rowsUnderWriter, kind *DeleteKind, level int) (string, bool) {
	ways := q.tree.waysAt(kind, level)
	if len(ways) == 0 || q.excludesTooMany(kind, ways) {
		return "", false
	}
	outer := w.quote(string(kind.Resource))
	condition := w.anyWay(ways, q.key, outer)
	if level > 0 {
		// A way that compares an empty column is NULL, not false: COALESCE makes it false,
		// so a row no way at level 0 reaches is kept.
		condition += " AND NOT COALESCE(" + w.anyWay(q.tree.waysAt(kind, 0), q.key, outer) + ", FALSE)"
	}

	return condition + w.ofRecordsTenant(outer+".", kind, q.tree, q.key) + w.notWritten(outer, kind.Resource), true
}

// excludesTooMany reports whether the transaction deleted more rows of the kind's table,
// or of a table one of the ways reads, than a statement leaves out by key.
func (q *takenQuery) excludesTooMany(kind *DeleteKind, ways []DeletePath) bool {
	if len(q.written[kind.Resource]) > deleteExclusionLimit {
		return true
	}
	for _, way := range ways {
		for _, link := range way[:len(way)-1] {
			if len(q.written[accesstypes.Resource(link.Table)]) > deleteExclusionLimit {
				return true
			}
		}
	}

	return false
}

// count is the count of the kind's rows at the level; ok is false where the kind is not
// counted there (condition).
func (q *takenQuery) count(kind *DeleteKind, level int) (rowsUnderStatement, bool) {
	w := &rowsUnderWriter{dbType: SpannerDBType, written: q.written}
	condition, ok := q.condition(w, kind, level)
	if !ok {
		return rowsUnderStatement{}, false
	}

	return rowsUnderStatement{SQL: fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s", w.quote(string(kind.Resource)), condition), Args: w.args}, true
}

// page is the read of one page of the kind's rows at the level, in key order, after the
// first offset rows: each row's key, then each of the terms asks reads of it (none
// where asks is nil: a page read to delete its rows).
func (q *takenQuery) page(kind *DeleteKind, level int, asks *takenAsks, offset int) (rowsUnderStatement, bool) {
	w := &rowsUnderWriter{dbType: SpannerDBType, written: q.written}
	condition, ok := q.condition(w, kind, level)
	if !ok {
		return rowsUnderStatement{}, false
	}
	outer := w.quote(string(kind.Resource))
	key := make([]string, 0, len(kind.Key))
	for _, column := range kind.Key {
		key = append(key, outer+"."+w.quote(column))
	}
	columns := slices.Clone(key)
	var named map[string]any
	if asks != nil {
		for i, term := range asks.terms {
			columns = append(columns, fmt.Sprintf("%s AS a%d", term, i+1))
		}
		named = asks.params
	}
	sql := fmt.Sprintf("SELECT %s FROM %s WHERE %s ORDER BY %s LIMIT %d", strings.Join(columns, ", "), outer, condition, strings.Join(key, ", "), takenPageRows)
	if offset > 0 {
		sql += fmt.Sprintf(" OFFSET %d", offset)
	}

	return rowsUnderStatement{SQL: sql, Args: w.args, Named: named}, true
}

// countedKind is a kind a delete counts, with the query its statements are written by.
type countedKind struct {
	kind  *DeleteKind
	query *takenQuery
}

// takenLevels are the rows under the record of each kind a delete counts, level by
// level (rows, by kind, at each level from 0), and the deepest level that has a row of
// one of them.
type takenLevels struct {
	deepest int
	rows    map[*DeleteKind][]int64
}

// countLevels counts each kind's rows at level 0, then, level by level from level 1,
// the rows of each kind with a place on a way for a link to a row of the same table
// (DeleteTree.waysAt), stopping at the first level where none of them has a row. A
// kind's count is the sum of its levels (total).
func countLevels(ctx context.Context, count rowCounter, kinds []countedKind) (*takenLevels, error) {
	levels := &takenLevels{rows: make(map[*DeleteKind][]int64, len(kinds))}
	nested := slices.DeleteFunc(slices.Clone(kinds), func(k countedKind) bool {
		return len(k.query.tree.waysAt(k.kind, 1)) == 0
	})
	for level := 0; ; level++ {
		asked := kinds
		if level > 0 {
			asked = nested
		}
		found := false
		for _, k := range asked {
			var rows int64
			if statement, ok := k.query.count(k.kind, level); ok {
				var err error
				if rows, err = count(ctx, statement); err != nil {
					return nil, errors.Wrapf(err, "counting the %s under a %s", k.kind.Words.Many, k.query.tree.Words.One)
				}
			}
			levels.rows[k.kind] = append(levels.rows[k.kind], rows)
			found = found || rows > 0
		}
		if level > 0 && !found {
			return levels, nil
		}
		if found {
			levels.deepest = level
		}
	}
}

// total is the kind's rows under the record, every level summed.
func (l *takenLevels) total(kind *DeleteKind) int64 {
	var rows int64
	for _, at := range l.rows[kind] {
		rows += at
	}

	return rows
}

// at is the kind's rows at the level.
func (l *takenLevels) at(kind *DeleteKind, level int) int64 {
	if level >= len(l.rows[kind]) {
		return 0
	}

	return l.rows[kind][level]
}

// takenPageRow is one row of a page: its key, column by column in key order, in the Go
// types the kind's resource holds them in, and what the page's terms answered of it.
type takenPageRow struct {
	key     []any
	answers []spanner.NullBool
}

// readPage reads the rows a page statement finds, inside the transaction: each row's
// key, in the given types, and as many answers after it.
func (c *SpannerReadWriteTransaction) readPage(ctx context.Context, statement rowsUnderStatement, types []reflect.Type, answers int) ([]takenPageRow, error) {
	it := c.txn.Query(ctx, statement.spannerStatement())
	defer it.Stop()

	var rows []takenPageRow
	for {
		row, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, errors.Wrap(err, "spanner.RowIterator.Next()")
		}
		read := takenPageRow{key: make([]any, 0, len(types)), answers: make([]spanner.NullBool, answers)}
		for i, typ := range types {
			value := reflect.New(typ)
			if err := row.Column(i, value.Interface()); err != nil {
				return nil, errors.Wrap(err, "spanner.Row.Column()")
			}
			read.key = append(read.key, value.Elem().Interface())
		}
		for i := range answers {
			if err := row.Column(len(types)+i, &read.answers[i]); err != nil {
				return nil, errors.Wrap(err, "spanner.Row.Column()")
			}
		}
		rows = append(rows, read)
	}

	return rows, nil
}

// takenDelete is one row a delete takes, with its kind.
type takenDelete struct {
	kind *DeleteKind
	row  takenRow
}

// takingDelete is the delete of one record that takes the rows under it, as deleteTaken
// runs it inside the delete's transaction.
type takingDelete struct {
	asked *recordedDelete
	query *takenQuery
	txn   *SpannerReadWriteTransaction
	// caller is the person each row's delete is armed against, a zero Caller where the
	// record's delete runs trusted.
	caller *Caller
	// page reads one page of a kind's rows (SpannerReadWriteTransaction.readPage).
	page func(ctx context.Context, statement rowsUnderStatement, types []reflect.Type, answers int) ([]takenPageRow, error)
}

// keyFields are the fields that hold the kind's key columns, in key order, and their Go
// types.
func (d *takingDelete) keyFields(kind *DeleteKind) ([]accesstypes.Field, []reflect.Type, error) {
	columns := kind.Rows.columns(d.txn.DBType())
	fields := make([]accesstypes.Field, 0, len(kind.Key))
	types := make([]reflect.Type, 0, len(kind.Key))
	for _, column := range kind.Key {
		held, ok := columns[column]
		if !ok {
			return nil, nil, errors.Newf("no field of %s holds its key column %s", kind.Resource, column)
		}
		fields = append(fields, held.field)
		types = append(types, held.typ)
	}

	return fields, types, nil
}

// rowsAt calls each with the delete of each of the kind's rows at the level, page by
// page in key order, with what the page's terms answered of the row (asks; none where
// it is nil). It stops at the first error each answers.
func (d *takingDelete) rowsAt(ctx context.Context, kind *DeleteKind, level int, asks *takenAsks, each func(taken takenDelete, answers []spanner.NullBool) error) error {
	fields, types, err := d.keyFields(kind)
	if err != nil {
		return err
	}
	answers := 0
	if asks != nil {
		answers = len(asks.terms)
	}
	for offset := 0; ; offset += takenPageRows {
		statement, ok := d.query.page(kind, level, asks, offset)
		if !ok {
			return nil
		}
		rows, err := d.page(ctx, statement, types, answers)
		if err != nil {
			return errors.Wrapf(err, "reading the %s under a %s", kind.Words.Many, d.query.tree.Words.One)
		}
		for _, row := range rows {
			if err := each(takenDelete{kind: kind, row: kind.Rows.row(fields, row.key, d.caller)}, row.answers); err != nil {
				return err
			}
		}
		if len(rows) < takenPageRows {
			return nil
		}
	}
}

// deleteTaken takes the rows the record's description lists as going with it, before
// the record's own row is deleted: the whole procedure this file opens with. answered
// says the rows under the record were answered for here, so nothing is left to count
// under it before the commit. It does nothing for a record that lists none, for a
// description written before its kinds had their deletes (DeleteKind.Rows), and on a
// transaction that is not Spanner's (the Mock's has no rows to read; PostgreSQL has no
// write path): the count before the commit answers for those.
//
// The rows are read as the database held them before the transaction: a row the same
// transaction changed is found where it stood, and a row it deleted is found and deleted
// again. The answers come in this order: a record with rows that refuse its delete
// (refusedBy), counted level by level as the taken kinds are, is refused in the
// sentence that names them, the rows the transaction itself deleted left out; then a
// kind with rows under the record that the caller's grants refuse; then a delete with
// more rows under it than one commit takes; then the first row of a kind that asks
// anything of its rows that is refused, each row asked as its own delete would ask it.
// A row is checked as the person the record's own delete is armed against, in that
// delete's scope, and runs trusted where the record's delete does. The sentences are
// written for whoever asked (deleteCaller).
func (p *PatchSet[Resource]) deleteTaken(ctx context.Context, txn ReadWriteTransaction, eventSource ...string) (answered bool, err error) {
	tree := p.querySet.rMeta.deleteTree
	if tree == nil || len(tree.Taken) == 0 {
		return false, nil
	}
	spannerTxn, ok := txn.(*SpannerReadWriteTransaction)
	if !ok {
		return false, nil
	}
	key, ok := p.keyByColumn(txn.DBType())
	if !ok {
		return false, nil
	}
	tracked, ok := takenTracked(tree, p.querySet.rMeta.trackChanges)
	if !ok {
		return false, nil
	}

	taking := &takingDelete{
		asked:  &recordedDelete{tree: tree, key: key, caller: p.deleteCaller(ctx)},
		query:  &takenQuery{tree: tree, key: key},
		txn:    spannerTxn,
		caller: p.querySet.Caller(),
		page:   spannerTxn.readPage,
	}

	return taking.run(ctx, spannerTxn.countRows, tracked, p.everythingUnder, eventSource...)
}

// run is the delete, each count statement run by count: the counts, the refusing kinds'
// refusal, then the taken kinds' checks and the rows taken (take).
func (d *takingDelete) run(ctx context.Context, count rowCounter, tracked map[accesstypes.Resource]bool, everythingUnder bool, eventSource ...string) (bool, error) {
	levels, err := d.count(ctx, count)
	if err != nil {
		return false, err
	}
	if err := d.asked.refusedNow(ctx, levels); err != nil {
		return false, err
	}

	return d.take(ctx, levels, tracked, everythingUnder, eventSource...)
}

// count counts the refusing kinds' rows and the taken kinds' under the record, level by
// level, the refusing kinds leaving out the rows the transaction itself deleted.
func (d *takingDelete) count(ctx context.Context, count rowCounter) (*takenLevels, error) {
	refusing := &takenQuery{tree: d.query.tree, key: d.query.key, written: d.txn.deleted.gone}
	kinds := make([]countedKind, 0, len(d.query.tree.RefusedBy)+len(d.query.tree.Taken))
	for i := range d.query.tree.RefusedBy {
		kinds = append(kinds, countedKind{kind: &d.query.tree.RefusedBy[i], query: refusing})
	}
	for i := range d.query.tree.Taken {
		kinds = append(kinds, countedKind{kind: &d.query.tree.Taken[i], query: d.query})
	}

	return countLevels(ctx, count, kinds)
}

// take checks the taken kinds and their rows, once no refusing row is in the way, and
// takes the rows where the request says so (everythingUnder), deepest level first and in
// the description's order within a level; without it the person is asked the question.
// With no row under the record the delete goes on as a plain one.
func (d *takingDelete) take(ctx context.Context, levels *takenLevels, tracked map[accesstypes.Resource]bool, everythingUnder bool, eventSource ...string) (bool, error) {
	tree := d.query.tree
	rows := make(map[accesstypes.Resource]int, len(tree.Taken))
	asks := make(map[*DeleteKind]*takenAsks, len(tree.Taken))
	for i := range tree.Taken {
		kind := &tree.Taken[i]
		total := levels.total(kind)
		if total == 0 {
			continue
		}
		rows[kind.Resource] += int(total)
		armed := kind.Rows.kind(d.caller)
		if err := armed.checkPermissions(ctx, d.txn.DBType()); err != nil {
			return false, d.asked.refuseRow(ctx, kind, false, err)
		}
		kindAsks, err := armed.takenAsks(d.txn.DBType())
		if err != nil {
			return false, err
		}
		asks[kind] = kindAsks
	}
	if len(rows) == 0 {
		return true, nil
	}
	if tree.Mutations(rows, func(res accesstypes.Resource) bool { return tracked[res] }) > DeleteMutationLimit {
		return false, httpio.NewConflictMessage(deleteTooManySentence(tree))
	}

	if err := d.eachLevel(levels, func(kind *DeleteKind, level int) error {
		if !asks[kind].asksRows() {
			return nil
		}

		return d.rowsAt(ctx, kind, level, asks[kind], func(taken takenDelete, answers []spanner.NullBool) error {
			byRule, err := taken.row.checkTaken(ctx, d.txn, asks[kind], answers)
			if err != nil {
				return d.asked.refuseRow(ctx, kind, byRule, err)
			}

			return nil
		})
	}); err != nil {
		return false, err
	}

	if !everythingUnder {
		question, err := d.asked.question(ctx, levels)
		if err != nil {
			return false, err
		}

		return false, httpio.NewPreconditionRequiredMessage(question)
	}

	if err := d.eachLevel(levels, func(kind *DeleteKind, level int) error {
		return d.rowsAt(ctx, kind, level, nil, func(taken takenDelete, _ []spanner.NullBool) error {
			if err := taken.row.bufferTaken(ctx, d.txn, eventSource...); err != nil {
				return errors.Wrapf(err, "deleting a %s under a %s", kind.Words.One, tree.Words.One)
			}

			return nil
		})
	}); err != nil {
		return false, err
	}

	return true, nil
}

// eachLevel calls each with every taken kind that has rows at a level, from the deepest
// level to level 0, and in the description's order within a level.
func (d *takingDelete) eachLevel(levels *takenLevels, each func(kind *DeleteKind, level int) error) error {
	for level := levels.deepest; level >= 0; level-- {
		for i := range d.query.tree.Taken {
			kind := &d.query.tree.Taken[i]
			if levels.at(kind, level) == 0 {
				continue
			}
			if err := each(kind, level); err != nil {
				return err
			}
		}
	}

	return nil
}

// takenTracked reports whether each taken kind's changes are recorded, and the record's
// own (record), for the size of the delete. ok is false for a description whose taken
// kinds do not all have their deletes (DeleteKind.Rows).
func takenTracked(tree *DeleteTree, record bool) (tracked map[accesstypes.Resource]bool, ok bool) {
	tracked = map[accesstypes.Resource]bool{tree.Record: record}
	for i := range tree.Taken {
		kind := &tree.Taken[i]
		if kind.Rows == nil {
			return nil, false
		}
		if kind.Resource == tree.Record {
			continue
		}
		tracked[kind.Resource] = kind.Rows.tracked()
	}

	return tracked, true
}

// refusedNow refuses the delete of a record that has rows of a kind its description
// lists under refusedBy, in the sentence that names them. The rows are the ones under
// the record as the database held them before the transaction, at every level, less
// the ones the transaction itself deleted.
func (d *recordedDelete) refusedNow(ctx context.Context, levels *takenLevels) error {
	var refusing []kindRows
	for i := range d.tree.RefusedBy {
		kind := &d.tree.RefusedBy[i]
		if rows := levels.total(kind); rows > 0 {
			refusing = append(refusing, kindRows{kind: kind, rows: rows})
		}
	}
	if len(refusing) == 0 {
		return nil
	}
	seen, err := d.seen(ctx, refusing)
	if err != nil {
		return err
	}

	return httpio.NewConflictMessage(deleteRefusedSentence(d.tree, seen, true))
}

// refuseRow is the answer to a delete over a kind under the record that refused: a
// refusal that is the row's own (byRule: its question's or its rule's) in its own
// sentence; one of the person's grants, a row they may not delete or may not reach, in
// a sentence that names the kind where the person may see it.
func (d *recordedDelete) refuseRow(ctx context.Context, kind *DeleteKind, byRule bool, err error) error {
	switch {
	case byRule:
		return err
	case !httpio.HasForbidden(err) && !httpio.HasNotFound(err):
		return errors.Wrapf(err, "checking a %s under a %s", kind.Words.One, d.tree.Words.One)
	}

	seen, serr := d.seen(ctx, []kindRows{{kind: kind, rows: 1}})
	if serr != nil {
		return serr
	}

	return httpio.NewForbiddenMessageWithError(err, deleteNotAllowedSentence(d.tree, kind, len(seen) > 0))
}

// question is what the person is asked before the delete takes the rows counted: the
// kinds they may see, each with its count, the kind nearest the record first.
func (d *recordedDelete) question(ctx context.Context, levels *takenLevels) (string, error) {
	var kinds []kindRows
	for i := range slices.Backward(d.tree.Taken) {
		kind := &d.tree.Taken[i]
		if rows := levels.total(kind); rows > 0 {
			kinds = append(kinds, kindRows{kind: kind, rows: rows})
		}
	}
	seen, err := d.seen(ctx, kinds)
	if err != nil {
		return "", err
	}

	return deleteQuestion(d.tree, seen, len(seen) < len(kinds)), nil
}

// deleteQuestion is the sentence a person is asked before a delete takes the rows under
// a record: "Delete this client and everything under it: 14 projects, 230 tasks, 41
// documents. This can't be undone." Kinds the person may not see are not counted for
// them (unseen).
func deleteQuestion(tree *DeleteTree, seen []kindRows, unseen bool) string {
	ask := "Delete " + tree.rowNameInside() + " and everything under it"
	counts := make([]string, 0, len(seen))
	for _, k := range seen {
		counts = append(counts, k.kind.Words.Count(int(k.rows)))
	}

	switch {
	case len(counts) == 0:
		return ask + "? This can't be undone."
	case unseen:
		return ask + ": " + strings.Join(counts, ", ") + ", and records you can't see. This can't be undone."
	default:
		return ask + ": " + strings.Join(counts, ", ") + ". This can't be undone."
	}
}

// deleteNotAllowedSentence is the sentence that refuses a delete over a row under the
// record that the person may not delete. It names the kind where they may see it.
func deleteNotAllowedSentence(tree *DeleteTree, kind *DeleteKind, seen bool) string {
	if !seen {
		return tree.RowName() + " has records under it that you can't delete, so it can't be deleted."
	}

	return tree.RowName() + " has " + kind.Words.Many + " that you can't delete, so it can't be deleted."
}

// deleteTooManySentence is the sentence that refuses a delete whose rows cost more than
// one commit takes (DeleteMutationLimit).
func deleteTooManySentence(tree *DeleteTree) string {
	return tree.RowName() + " has too many records under it to delete at once. Delete some of them first."
}
