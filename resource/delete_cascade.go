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
// (PatchSet.DeleteEverythingUnder). The rows are found by walking down from the record
// on rows (takenWalk): each taken kind's rows whose columns hold what the record, or a
// row found above them, is referenced by, level by level until a level finds nothing
// new. A row under a row of its own kind is found that way too (DeleteKind.Within), and
// so is a row whose foreign key references columns that are not the other table's key,
// since each level is matched on the columns the link references.
//
// Before anything is deleted, every row found is checked as its own delete would check
// it (PatchSet.checkDelete: the caller's grants on the row, what the kind's generated
// code asks of a delete, then the kind's delete rule), and the whole is sized against
// what one commit takes (DeleteTree.Mutations). Nothing goes unless everything may go,
// and a refusal says why. Then the rows are deleted deepest first and the record last,
// each through the delete of one row (bufferCheckedDelete), so each has its change
// record, is announced to open pages, and has its stored files released.
//
// A delete that does not say it takes the rows, of a record that has some, deletes
// nothing and is answered with the question a person is asked first: "Delete this
// client and everything under it: 2 client contacts. This can't be undone." It is a 428,
// answered once the whole check has passed, so the question is never asked of a delete
// that would be refused.

// takenParameterLimit is the most values one read of a level's rows names: a query takes
// fewer than a thousand parameters, and a level with more rows above it is read in
// parts.
const takenParameterLimit = 900

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
	checkDelete(ctx context.Context, txn ReadWriteTransaction) (byRule bool, err error)
	bufferTaken(ctx context.Context, txn ReadWriteTransaction, eventSource ...string) error
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

// takenWalk finds the rows a record's delete takes with it.
type takenWalk struct {
	tree *DeleteTree
	// limit is the most mutations the delete may cost (DeleteMutationLimit): the walk
	// stops once the rows found cost more.
	limit int
	// tracked reports whether a kind's changes are recorded, the record's own among
	// them.
	tracked func(accesstypes.Resource) bool
	// under reads the kind's rows that one link puts under any of the rows above, each
	// row by column, with at least the kind's key and the columns the taken kinds' rows
	// under it are found by (DeleteTree.reads). It reads no more than most rows.
	under func(ctx context.Context, kind *DeleteKind, link DeleteLink, above []map[string]any, most int) ([]map[string]any, error)
	// deleted reports whether the transaction itself deleted the row: the walk leaves
	// it out with what is under it, which the row's own delete answered for.
	deleted func(res accesstypes.Resource, key map[string]any) bool
	// changed reports whether the transaction itself updated the row, or inserted or
	// updated it: the walk ends at such a row.
	changed func(res accesstypes.Resource, key map[string]any) bool
}

// takenFound is what a walk found: each taken kind's rows under the record, by column,
// in the order they were found, so a row is never before a row it was found under.
type takenFound struct {
	rows map[accesstypes.Resource][]map[string]any
	// tooMany says the walk stopped because the rows found so far already cost more
	// than one commit takes.
	tooMany bool
	// changed says the walk stopped at a row under the record that the transaction
	// itself changed. The read shows the row as it was before the transaction, so what
	// the delete would take cannot be told, and the delete is refused.
	changed bool
}

// count is the number of rows found of each kind.
func (f *takenFound) count() map[accesstypes.Resource]int {
	counts := make(map[accesstypes.Resource]int, len(f.rows))
	for kind, rows := range f.rows {
		counts[kind] = len(rows)
	}

	return counts
}

// keyOf is a row's key by column, from the columns a walk read of it.
func keyOf(key []string, row map[string]any) map[string]any {
	values := make(map[string]any, len(key))
	for _, column := range key {
		values[column] = row[column]
	}

	return values
}

// find walks down from the record's row: at each level every taken kind's rows under
// the rows the level before found, by each link that puts them there, until a level
// finds no row it had not found. A row the transaction itself deleted is left out with
// what is under it: its own delete answered for those. The walk ends at a row the
// transaction itself changed (changed), since the read shows the row as it was before
// the change, and as soon as the rows found cost more than one commit takes (tooMany).
func (w *takenWalk) find(ctx context.Context, recordRow map[string]any) (*takenFound, error) {
	found := &takenFound{rows: make(map[accesstypes.Resource][]map[string]any)}
	seen := map[string]struct{}{rowOfTable(w.tree.Record, keyOf(w.tree.Key, recordRow)): {}}
	level := map[string][]map[string]any{string(w.tree.Record): {recordRow}}
	for len(level) > 0 {
		next := make(map[string][]map[string]any)
		for i := range w.tree.Taken {
			kind := &w.tree.Taken[i]
			for _, link := range kind.stepsUp() {
				if len(level[link.Table]) == 0 {
					continue
				}
				// One row more than the delete may cost in mutations is already too many.
				rows, err := w.under(ctx, kind, link, level[link.Table], w.limit+1)
				if err != nil {
					return nil, err
				}
				for _, row := range rows {
					key := keyOf(kind.Key, row)
					identity := rowOfTable(kind.Resource, key)
					if _, ok := seen[identity]; ok {
						continue
					}
					seen[identity] = struct{}{}
					if w.deleted(kind.Resource, key) {
						continue
					}
					if w.changed(kind.Resource, key) {
						found.changed = true

						return found, nil
					}
					found.rows[kind.Resource] = append(found.rows[kind.Resource], row)
					next[string(kind.Resource)] = append(next[string(kind.Resource)], row)
				}
				if w.tree.Mutations(found.count(), w.tracked) > w.limit {
					found.tooMany = true

					return found, nil
				}
			}
		}
		level = next
	}

	return found, nil
}

// holdsNothing reports whether a column's value is NULL as its field holds it: a nil
// pointer, or a Null wrapper that is not valid.
func holdsNothing(value any) bool {
	if value == nil {
		return true
	}
	if null, ok := value.(interface{ IsNull() bool }); ok {
		return null.IsNull()
	}
	v := reflect.ValueOf(value)

	return v.Kind() == reflect.Pointer && v.IsNil()
}

// rowsStatement is the read of the named columns of a table's rows that meet the
// condition.
func rowsStatement(writer *rowsUnderWriter, table accesstypes.Resource, columns []string, condition string) rowsUnderStatement {
	quoted := make([]string, 0, len(columns))
	for _, column := range columns {
		quoted = append(quoted, writer.quote(column))
	}
	sql := fmt.Sprintf("SELECT %s FROM %s WHERE %s", strings.Join(quoted, ", "), writer.quote(string(table)), condition)

	return rowsUnderStatement{SQL: sql, Args: writer.args}
}

// takenStatements are the reads of the kind's rows that one link puts under any of the
// rows above: the rows whose columns hold what the link references of one of them. A
// row above that holds nothing in a referenced column has no row under it by the link,
// as the database holds a foreign key only for a row with a value in every column of
// it. The rows above are read for in parts, so no read names more values than a query
// takes (takenParameterLimit). Where the kind's rows and the record are a tenant's, a
// read finds the rows of the record's tenant alone (ofRecordsTenant): a row of another
// tenant that names one of the rows above is not under the record.
func takenStatements(dbType DBType, tree *DeleteTree, recordKey map[string]any, kind *DeleteKind, link DeleteLink, above []map[string]any, columns []string) ([]rowsUnderStatement, error) {
	var statements []rowsUnderStatement
	room := takenParameterLimit - tenantValues(kind, tree, recordKey)
	for part := range slices.Chunk(above, max(1, room/len(link.Columns))) {
		writer := &rowsUnderWriter{dbType: dbType}
		condition, err := writer.underAbove("", kind, link, part)
		if err != nil {
			return nil, err
		}
		if condition == "" {
			continue
		}
		if tenant := writer.ofRecordsTenant(writer.quote(string(kind.Resource))+".", kind, tree, recordKey); tenant != "" {
			condition = "(" + condition + ")" + tenant
		}
		statements = append(statements, rowsStatement(writer, kind.Resource, columns, condition))
	}

	return statements, nil
}

// underAbove is the condition that a row of the kind, its columns spelled after the
// prefix, is under one of the rows above by the link: its columns hold what the link
// references of one of them. It is empty where none of the rows above holds a value in
// every referenced column.
func (w *rowsUnderWriter) underAbove(prefix string, kind *DeleteKind, link DeleteLink, above []map[string]any) (string, error) {
	conditions := make([]string, 0, len(above))
	for _, row := range above {
		values := make([]any, 0, len(link.Columns))
		for _, column := range link.References {
			value, ok := row[column]
			if !ok {
				return "", errors.Newf("the rows of %s were read without %s, which %s references", link.Table, column, kind.Resource)
			}
			if holdsNothing(value) {
				values = nil

				break
			}
			values = append(values, value)
		}
		if len(values) == 0 {
			continue
		}
		equal := make([]string, 0, len(link.Columns))
		for i, column := range link.Columns {
			equal = append(equal, fmt.Sprintf("%s%s = %s", prefix, w.quote(column), w.value(values[i])))
		}
		conditions = append(conditions, "("+strings.Join(equal, " AND ")+")")
	}

	return strings.Join(conditions, " OR "), nil
}

// refusingCount counts the rows that refuse a record's delete from under the rows a
// walk found. The count before the walk (recordedDelete.refusedNow) finds a refusing
// row by its ways up to the record, and a way never passes a table twice: a row under
// a task that is under the record only through another task is on none of them. This
// count looks under every row the walk found, so such a row refuses the delete in the
// counted sentence, before any row is checked, asked about or deleted, and the delete
// never reaches the database's own foreign key refusal at the commit.
type refusingCount struct {
	tree *DeleteTree
	// under names the kind's rows that one link puts under any of the rows above, each
	// row by its key in one text, leaving out the rows the transaction itself deleted or
	// updated.
	under func(ctx context.Context, kind *DeleteKind, link DeleteLink, above []map[string]any) ([]string, error)
}

// rows counts each refusing kind's rows under the rows the walk found, by each link
// that puts them there, and returns the kinds that have one, in the description's
// order. A row under two of the rows found is counted once.
func (c *refusingCount) rows(ctx context.Context, found *takenFound) ([]kindRows, error) {
	var refusing []kindRows
	for i := range c.tree.RefusedBy {
		kind := &c.tree.RefusedBy[i]
		names := make(map[string]struct{})
		for _, link := range kind.stepsUp() {
			above := found.rows[accesstypes.Resource(link.Table)]
			if len(above) == 0 {
				continue
			}
			under, err := c.under(ctx, kind, link, above)
			if err != nil {
				return nil, err
			}
			for _, name := range under {
				names[name] = struct{}{}
			}
		}
		if len(names) > 0 {
			refusing = append(refusing, kindRows{kind: kind, rows: int64(len(names))})
		}
	}

	return refusing, nil
}

// refusingStatements are the reads of the key of the refusing kind's rows that one link
// puts under any of the rows above, leaving out the rows of the kind the transaction
// itself deleted (written) and, where the kind's rows and the record are a tenant's,
// the rows of another tenant (ofRecordsTenant). The rows above are read for in parts, so no read names
// more values than a query takes (takenParameterLimit). There is none where the
// transaction deleted more rows of the kind than a statement leaves out by key
// (deleteExclusionLimit), or so many that no room is left for a row above: the kind is
// then not counted here, and the commit answers for it. The reads are spelled for
// Spanner, the one database a delete takes rows on (deleteTaken).
func refusingStatements(tree *DeleteTree, recordKey map[string]any, kind *DeleteKind, link DeleteLink, above, written []map[string]any) ([]rowsUnderStatement, error) {
	if len(written) > deleteExclusionLimit {
		return nil, nil
	}
	room := takenParameterLimit - len(written)*len(kind.Key) - tenantValues(kind, tree, recordKey)
	if room < len(link.Columns) {
		return nil, nil
	}

	quoter := &rowsUnderWriter{dbType: SpannerDBType}
	key := make([]string, 0, len(kind.Key))
	for _, column := range kind.Key {
		key = append(key, "t0."+quoter.quote(column))
	}
	var statements []rowsUnderStatement
	for part := range slices.Chunk(above, room/len(link.Columns)) {
		writer := &rowsUnderWriter{dbType: SpannerDBType, written: map[accesstypes.Resource][]map[string]any{kind.Resource: written}}
		condition, err := writer.underAbove("t0.", kind, link, part)
		if err != nil {
			return nil, err
		}
		if condition == "" {
			continue
		}
		sql := fmt.Sprintf("SELECT %s FROM %s AS t0 WHERE (%s)%s%s",
			strings.Join(key, ", "), writer.quote(string(kind.Resource)), condition,
			writer.ofRecordsTenant("t0.", kind, tree, recordKey), writer.notWritten("t0", kind.Resource))
		statements = append(statements, rowsUnderStatement{SQL: sql, Args: writer.args})
	}

	return statements, nil
}

// takenReader reads the rows a walk asks for inside the delete's transaction.
type takenReader struct {
	tree *DeleteTree
	// key is the key of the record the rows are read under.
	key map[string]any
	txn *SpannerReadWriteTransaction
	// columns are each taken kind's columns, and the record's own.
	columns map[accesstypes.Resource]map[string]takenColumn
}

// recordRow is the record's row as the walk starts from it: its key, and any other
// column a taken kind's rows are found by, read from the row. there is false where the
// row was read and is not stored: nothing is under a row that is not there.
func (r *takenReader) recordRow(ctx context.Context, key map[string]any) (row map[string]any, there bool, err error) {
	columns := r.tree.reads(r.tree.Record, r.tree.Key)
	if len(columns) == len(r.tree.Key) {
		return key, true, nil
	}

	writer := &rowsUnderWriter{dbType: SpannerDBType}
	equal := make([]string, 0, len(r.tree.Key))
	for _, column := range r.tree.Key {
		equal = append(equal, fmt.Sprintf("%s = %s", writer.quote(column), writer.value(key[column])))
	}
	rows, err := r.txn.readColumns(ctx, rowsStatement(writer, r.tree.Record, columns, strings.Join(equal, " AND ")), columns, r.columns[r.tree.Record], 1)
	if err != nil {
		return nil, false, errors.Wrapf(err, "reading the %s", r.tree.Words.One)
	}
	if len(rows) == 0 {
		return nil, false, nil
	}

	return rows[0], true, nil
}

// under reads the kind's rows that one link puts under any of the rows above, and no
// more than most of them.
func (r *takenReader) under(ctx context.Context, kind *DeleteKind, link DeleteLink, above []map[string]any, most int) ([]map[string]any, error) {
	columns := r.tree.reads(kind.Resource, kind.Key)
	statements, err := takenStatements(SpannerDBType, r.tree, r.key, kind, link, above, columns)
	if err != nil {
		return nil, err
	}
	var rows []map[string]any
	for _, statement := range statements {
		if len(rows) >= most {
			break
		}
		found, err := r.txn.readColumns(ctx, statement, columns, r.columns[kind.Resource], most-len(rows))
		if err != nil {
			return nil, errors.Wrapf(err, "reading the %s under a %s", kind.Words.Many, r.tree.Words.One)
		}
		rows = append(rows, found...)
	}

	return rows, nil
}

// refusing names the refusing kind's rows that one link puts under any of the rows
// above, each by its key in one text, leaving out the rows the transaction itself
// deleted.
func (r *takenReader) refusing(ctx context.Context, kind *DeleteKind, link DeleteLink, above []map[string]any) ([]string, error) {
	statements, err := refusingStatements(r.tree, r.key, kind, link, above, r.txn.deleted.gone[kind.Resource])
	if err != nil {
		return nil, err
	}
	var names []string
	for _, statement := range statements {
		found, err := r.txn.rowNames(ctx, statement)
		if err != nil {
			return nil, errors.Wrapf(err, "reading the %s under a %s", kind.Words.Many, r.tree.Words.One)
		}
		names = append(names, found...)
	}

	return names, nil
}

// rowNames reads the rows a statement finds, inside the transaction, and names each by
// the values of its columns in one text, so two readings of one row give one name.
func (c *SpannerReadWriteTransaction) rowNames(ctx context.Context, statement rowsUnderStatement) ([]string, error) {
	it := c.txn.Query(ctx, statement.spannerStatement())
	defer it.Stop()

	var names []string
	for {
		row, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, errors.Wrap(err, "spanner.RowIterator.Next()")
		}
		values := make([]string, 0, row.Size())
		for i := range row.Size() {
			var value spanner.GenericColumnValue
			if err := row.Column(i, &value); err != nil {
				return nil, errors.Wrap(err, "spanner.Row.Column()")
			}
			values = append(values, fmt.Sprint(value.Value.AsInterface()))
		}
		names = append(names, strings.Join(values, "\x00"))
	}

	return names, nil
}

// readColumns reads the named columns of the rows a statement finds, inside the
// transaction, each value in the Go type the table's resource holds the column in. It
// reads no more than most rows.
func (c *SpannerReadWriteTransaction) readColumns(ctx context.Context, statement rowsUnderStatement, columns []string, types map[string]takenColumn, most int) ([]map[string]any, error) {
	it := c.txn.Query(ctx, statement.spannerStatement())
	defer it.Stop()

	var rows []map[string]any
	for len(rows) < most {
		row, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, errors.Wrap(err, "spanner.RowIterator.Next()")
		}
		values := make(map[string]any, len(columns))
		for i, column := range columns {
			held, ok := types[column]
			if !ok {
				return nil, errors.Newf("no field of the table's resource holds the column %s", column)
			}
			value := reflect.New(held.typ)
			if err := row.Column(i, value.Interface()); err != nil {
				return nil, errors.Wrap(err, "spanner.Row.Column()")
			}
			values[column] = value.Elem().Interface()
		}
		rows = append(rows, values)
	}

	return rows, nil
}

// takenDelete is one row a delete takes, with its kind.
type takenDelete struct {
	kind *DeleteKind
	row  takenRow
}

// takenDeletes are the deletes of the rows found, in the order they are buffered: the
// kinds as the description lists them, deepest first, and a kind's rows the last found
// first, so a row found under a row of its own kind goes before it. Each is armed
// against the caller, or trusted for a zero Caller.
func takenDeletes(tree *DeleteTree, dbType DBType, found *takenFound, caller *Caller) ([]takenDelete, error) {
	var deletes []takenDelete
	for i := range tree.Taken {
		kind := &tree.Taken[i]
		rows := found.rows[kind.Resource]
		if len(rows) == 0 {
			continue
		}
		columns := kind.Rows.columns(dbType)
		fields := make([]accesstypes.Field, 0, len(kind.Key))
		for _, column := range kind.Key {
			held, ok := columns[column]
			if !ok {
				return nil, errors.Newf("no field of %s holds its key column %s", kind.Resource, column)
			}
			fields = append(fields, held.field)
		}
		for _, row := range slices.Backward(rows) {
			values := make([]any, 0, len(kind.Key))
			for _, column := range kind.Key {
				values = append(values, row[column])
			}
			deletes = append(deletes, takenDelete{kind: kind, row: kind.Rows.row(fields, values, caller)})
		}
	}

	return deletes, nil
}

// deleteTaken takes the rows the record's description lists as going with it, before
// the record's own row is deleted: the whole procedure this file opens with. answered
// says the rows under the record were answered for here, so nothing is left to count
// under it before the commit. It does nothing for a record that lists none, for a
// description written before its kinds had their deletes (DeleteKind.Rows), and on a
// transaction that is not Spanner's (the Mock's has no rows to read; PostgreSQL has no
// write path): the count before the commit answers for those.
//
// A record with rows that refuse its delete (refusedBy) is refused before its other rows
// are looked at, in the sentence that names them. The rows are the ones under the record
// as the delete is buffered: what refuses it must be gone before it in the transaction.
// A refusing row under a row the walk alone reaches (a task under a task) is counted
// once the walk has found the rows, and refuses the delete in the same sentence before
// any of them is checked, asked about or deleted (refusingCount). A delete with more
// rows under it than one commit takes is refused for that first: the walk stopped
// short, so a count under what it found would be short too.
//
// A row under the record that the same transaction changed (an update, or an insert or
// update) refuses the delete before anything else is looked at, whatever the delete
// says: the reads here show the row as it was before the transaction, so neither what
// the row is under after it nor what is under the row can be told. The request sends
// the change and the delete separately. A row the transaction deleted is left out with
// what is under it, since its own delete answered for those.
//
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

	reader, tracked, ok := p.takenReaderOf(tree, key, spannerTxn)
	if !ok {
		return false, nil
	}

	asked := &recordedDelete{tree: tree, key: key, caller: p.deleteCaller(ctx)}
	if err := asked.refusedNow(ctx, spannerTxn); err != nil {
		return false, err
	}

	walk := &takenWalk{
		tree:  tree,
		limit: DeleteMutationLimit,
		tracked: func(res accesstypes.Resource) bool {
			return tracked[res]
		},
		under:   reader.under,
		deleted: spannerTxn.deleted.deletedRow,
		changed: spannerTxn.deleted.changedRow,
	}
	recordRow, there, err := reader.recordRow(ctx, key)
	if err != nil {
		return false, err
	}
	if !there {
		return true, nil
	}
	found, err := walk.find(ctx, recordRow)
	if err != nil {
		return false, err
	}
	if found.changed {
		return false, httpio.NewConflictMessage(deleteWithChangeSentence(tree))
	}
	if len(found.rows) == 0 {
		return true, nil
	}
	if found.tooMany {
		return false, httpio.NewConflictMessage(deleteTooManySentence(tree))
	}
	if err := asked.refusedUnder(ctx, &refusingCount{tree: tree, under: reader.refusing}, found); err != nil {
		return false, err
	}

	deletes, err := takenDeletes(tree, txn.DBType(), found, p.querySet.Caller())
	if err != nil {
		return false, err
	}
	for _, taken := range deletes {
		if err := asked.mayTake(ctx, txn, taken); err != nil {
			return false, err
		}
	}

	if !p.everythingUnder {
		question, err := asked.question(ctx, found)
		if err != nil {
			return false, err
		}

		return false, httpio.NewPreconditionRequiredMessage(question)
	}

	for _, taken := range deletes {
		if err := taken.row.bufferTaken(ctx, txn, eventSource...); err != nil {
			return false, errors.Wrapf(err, "deleting a %s under a %s", taken.kind.Words.One, tree.Words.One)
		}
	}

	return true, nil
}

// takenReaderOf is the reader of the rows under the record, and whether each kind keeps
// change history, for a description whose every taken kind has its deletes
// (DeleteKind.Rows). ok is false where one has none.
func (p *PatchSet[Resource]) takenReaderOf(tree *DeleteTree, key map[string]any, txn *SpannerReadWriteTransaction) (reader *takenReader, tracked map[accesstypes.Resource]bool, ok bool) {
	reader = &takenReader{
		tree:    tree,
		key:     key,
		txn:     txn,
		columns: map[accesstypes.Resource]map[string]takenColumn{tree.Record: columnsOf(p.querySet.rMeta, txn.DBType())},
	}
	tracked = map[accesstypes.Resource]bool{tree.Record: p.querySet.rMeta.trackChanges}
	for i := range tree.Taken {
		kind := &tree.Taken[i]
		if kind.Rows == nil {
			return nil, nil, false
		}
		if kind.Resource == tree.Record {
			continue
		}
		reader.columns[kind.Resource] = kind.Rows.columns(txn.DBType())
		tracked[kind.Resource] = kind.Rows.tracked()
	}

	return reader, tracked, true
}

// refusedNow refuses the delete of a record that has rows of a kind its description
// lists under refusedBy, in the sentence that names them. The rows are the ones under
// the record as the delete is buffered.
func (d *recordedDelete) refusedNow(ctx context.Context, txn *SpannerReadWriteTransaction) error {
	refusing, err := d.rowsOf(ctx, txn.DBType(), d.tree.RefusedBy, txn.deleted.gone, txn.countRows)
	if err != nil {
		return err
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

// refusedUnder refuses the delete of a record with a row of a refusing kind under one
// of the rows the walk found, in the sentence that names the kinds with their counts.
func (d *recordedDelete) refusedUnder(ctx context.Context, count *refusingCount, found *takenFound) error {
	refusing, err := count.rows(ctx, found)
	if err != nil {
		return err
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

// mayTake checks one row the delete would take as the row's own delete checks it. A row
// its rule refuses, or what its kind asks of a delete refuses, answers in that sentence.
// A row the person may not delete, or may not reach, refuses the whole delete in a
// sentence that names the kind where the person may see it.
func (d *recordedDelete) mayTake(ctx context.Context, txn ReadWriteTransaction, taken takenDelete) error {
	byRule, err := taken.row.checkDelete(ctx, txn)
	switch {
	case err == nil:
		return nil
	case byRule:
		return err
	case !httpio.HasForbidden(err) && !httpio.HasNotFound(err):
		return errors.Wrapf(err, "checking a %s under a %s", taken.kind.Words.One, d.tree.Words.One)
	}

	seen, serr := d.seen(ctx, []kindRows{{kind: taken.kind, rows: 1}})
	if serr != nil {
		return serr
	}

	return httpio.NewForbiddenMessageWithError(err, deleteNotAllowedSentence(d.tree, taken.kind, len(seen) > 0))
}

// question is what the person is asked before the delete takes the rows found: the
// kinds they may see, each with its count, the kind nearest the record first.
func (d *recordedDelete) question(ctx context.Context, found *takenFound) (string, error) {
	var kinds []kindRows
	for i := range slices.Backward(d.tree.Taken) {
		kind := &d.tree.Taken[i]
		if rows := len(found.rows[kind.Resource]); rows > 0 {
			kinds = append(kinds, kindRows{kind: kind, rows: int64(rows)})
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

// deleteWithChangeSentence is the sentence a person reads when a delete would take a row
// that the same request changes.
func deleteWithChangeSentence(tree *DeleteTree) string {
	return "This request also changes a record under " + tree.rowNameInside() + ". Send the change and the delete separately."
}

// deleteTooManySentence is the sentence that refuses a delete whose rows cost more than
// one commit takes (DeleteMutationLimit).
func deleteTooManySentence(tree *DeleteTree) string {
	return tree.RowName() + " has too many records under it to delete at once. Delete some of them first."
}
