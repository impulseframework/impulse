package resource

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/spxscan/spxapi"
	"google.golang.org/api/iterator"
)

// Every change record of a tenant resource's row carries the key of the tenant the row
// belongs to (DataChangeEvent.TenantID), so one tenant's history is read by the tenant
// alone, and a deleted row's records stay in it. The patch set writes it for every
// writer that writes through a patch (a generated route, a method, a job, a bootstrap,
// a delete that takes rows with it): a write armed in a tenant's request takes the
// request's tenant, which the tenancy check has held the row to, and any other write
// follows the row's way to its tenant (TenantBinding), from the patch's own value or
// the stored row's, one point read for each step. A read inside a transaction does not
// see the transaction's buffered writes, so each step first settles which row it names,
// by a buffered write that holds the column the step joins on or by the row's key as the
// database holds it, and answers with what the transaction's own patches last wrote to
// that row; the transaction remembers each read of the database.
// The change history never stops a write: a row with no way to a tenant carries the
// empty string. The tenant is the row's at the moment the record is buffered, by what
// the transaction has written until then: a later write of the same transaction (the
// row's parent moved to another tenant, the parent's key given to a new row) does not
// change a record already buffered, and a row the transaction changes by a statement of
// its own, past the patches, is not seen.

// TenantBinding is a tenant resource's way to its tenant: Field is the struct field the
// way leaves through, which holds the tenant's key when Path is empty, or the foreign
// key Path continues from to the column that holds it.
type TenantBinding struct {
	Field accesstypes.Field
	Path  []BindingHop
}

// tenantBinder is the generated declaration of a tenant resource's way to its tenant
// (TenantBinding).
type tenantBinder interface {
	TenantBinding() TenantBinding
}

// pointReader answers column of the row of table whose keyColumn holds value, and whether
// there is such a row. The value and the answer are carried as they are held (a patch's
// Go value, or the column as Spanner returned it), so a path through a key that is no
// text is followed in the key's own type.
type pointReader func(ctx context.Context, table, keyColumn string, value any, column string) (next any, found bool, err error)

// pointRead names one point read: column of the row of table whose keyColumn holds the
// value spelled value (columnText).
type pointRead struct {
	table     string
	keyColumn string
	value     string
	column    string
}

// pointAnswer is what one point read answered: the column's value as held, and whether
// there is such a row.
type pointAnswer struct {
	value any
	found bool
}

// bufferedColumns is one write a patch of the transaction buffered: its patch type, the
// row it wrote (rowMatchKey), the row's key by column (keyByColumn) and its spelling
// (keySpelling), both empty when the patch keeps none, and the columns it was given.
type bufferedColumns struct {
	patchType PatchType
	row       string
	key       map[string]any
	keyText   string
	columns   map[string]any
}

// keyedRead names one read of a row by its key by column: column of the row of table
// whose key is spelled key (keySpelling).
type keyedRead struct {
	table  string
	key    string
	column string
}

// storedRow is what one read of a row by another unique column answered: whether there
// is such a row, its key by column spelled (keySpelling), and the column as held.
type storedRow struct {
	found bool
	key   string
	value any
}

// transactionRows is the record a write transaction keeps for the point reads that
// follow a row to its parent: the columns each create, update and insert-or-update of
// its patches buffered, by table in buffered order, which no read of the transaction
// sees, and what each read of the database answered, by the question it asked. What is
// remembered is what the database holds; the buffered writes are laid over it at each
// read. Like the other records of a transaction it belongs to one attempt: a retried
// transaction runs its function again over a fresh record.
type transactionRows struct {
	writes   map[string][]bufferedColumns
	reads    map[pointRead]pointAnswer
	rowReads map[pointRead]storedRow
	keyReads map[keyedRead]pointAnswer
}

// newTransactionRows returns an empty record.
func newTransactionRows() *transactionRows {
	return &transactionRows{
		writes:   make(map[string][]bufferedColumns),
		reads:    make(map[pointRead]pointAnswer),
		rowReads: make(map[pointRead]storedRow),
		keyReads: make(map[keyedRead]pointAnswer),
	}
}

// note records the columns a patch buffered for one row, which row is its key as
// rowMatchKey spells it, and key the row's key by column, nil when the patch keeps none.
// A key that cannot be spelled is not kept. A delete is not noted.
func (t *transactionRows) note(table accesstypes.Resource, patchType PatchType, row string, key, columns map[string]any) {
	if patchType == DeletePatchType {
		return
	}
	keyText, err := keySpelling(key)
	if err != nil || keyText == "" {
		key, keyText = nil, ""
	}
	t.writes[string(table)] = append(t.writes[string(table)], bufferedColumns{
		patchType: patchType,
		row:       row,
		key:       maps.Clone(key),
		keyText:   keyText,
		columns:   maps.Clone(columns),
	})
}

// keySpelling spells a row's key by column in one way whatever order it was given in:
// each column by name order, with its value's spelling (columnText). An empty key is
// the empty string.
func keySpelling(key map[string]any) (string, error) {
	var spelled strings.Builder
	for _, column := range slices.Sorted(maps.Keys(key)) {
		value, err := columnText(key[column])
		if err != nil {
			return "", errors.Wrapf(err, "key column %s", column)
		}
		fmt.Fprintf(&spelled, "%d:%s=%d:%s;", len(column), column, len(value), value)
	}

	return spelled.String(), nil
}

// ownColumn answers a column of one row, which row is its key as rowMatchKey spells it,
// from what the transaction's patches buffered for that row, newest first: a write that
// set the column answers its value as held, a create that did not answers nil, and an
// update or an insert-or-update that did not is passed over. known is false when no
// buffered write of the row says.
func (t *transactionRows) ownColumn(table, row, column string) (value any, known bool) {
	writes := t.writes[table]
	for i := len(writes) - 1; i >= 0; i-- {
		if writes[i].row != row {
			continue
		}
		if value, ok := writes[i].columns[column]; ok {
			return value, true
		}
		if writes[i].patchType == CreatePatchType {
			return nil, true
		}
	}

	return nil, false
}

// bufferedOwnColumn answers the row's value of one field from what the transaction's
// patches buffered for the row (ownColumn). A transaction that keeps no record of its
// buffered columns (the Mock's, PostgreSQL) is not asked, and known is false.
func (p *PatchSet[Resource]) bufferedOwnColumn(txn ReadWriteTransaction, field accesstypes.Field) (value any, known bool) {
	recorder, ok := txn.(rowsRecorder)
	if !ok {
		return nil, false
	}
	column, ok := p.querySet.rMeta.dbFieldMap(txn.DBType())[field]
	if !ok {
		return nil, false
	}

	return recorder.transactionRows().ownColumn(string(p.Resource()), rowMatchKey(p.PrimaryKey()), column.ColumnName)
}

// namedWrite answers the write that names the row a point read is by: the newest
// buffered write of the table that holds the read's key column, spelled as the read's
// value (columnText). A write whose value there cannot be spelled is passed over. named
// is false when no buffered write holds it.
func (t *transactionRows) namedWrite(read pointRead) (write bufferedColumns, named bool) {
	writes := t.writes[read.table]
	for i := len(writes) - 1; i >= 0; i-- {
		value, ok := writes[i].columns[read.keyColumn]
		if !ok {
			continue
		}
		spelled, err := columnText(value)
		if err != nil || spelled != read.value {
			continue
		}

		return writes[i], true
	}

	return bufferedColumns{}, false
}

// keyColumnsBeside answers the key columns of the table's rows, in name order, when a
// buffered write of the table keeps its key by column and does not hold column: a write
// that may have changed a row a read by column names, without saying so. ok is false
// when no buffered write is such.
func (t *transactionRows) keyColumnsBeside(table, column string) (columns []string, ok bool) {
	writes := t.writes[table]
	for i := len(writes) - 1; i >= 0; i-- {
		if writes[i].keyText == "" {
			continue
		}
		if _, holds := writes[i].columns[column]; holds {
			continue
		}

		return slices.Sorted(maps.Keys(writes[i].key)), true
	}

	return nil, false
}

// writeOfKey answers the newest buffered write of the table whose key by column is
// spelled key (keySpelling).
func (t *transactionRows) writeOfKey(table, key string) (write bufferedColumns, ok bool) {
	writes := t.writes[table]
	for i := len(writes) - 1; i >= 0; i-- {
		if writes[i].keyText == key {
			return writes[i], true
		}
	}

	return bufferedColumns{}, false
}

// remembered answers a point read the transaction made before.
func (t *transactionRows) remembered(read pointRead) (pointAnswer, bool) {
	answer, ok := t.reads[read]

	return answer, ok
}

// remember records what a point read answered.
func (t *transactionRows) remember(read pointRead, answer pointAnswer) {
	t.reads[read] = answer
}

// rowsRecorder is how the patch machinery reaches a transaction's record of the columns
// its patches buffered and the point reads it made: the Spanner wrapper satisfies it. A
// transaction that does not (the Mock's, PostgreSQL) has neither, and every point read
// on it goes to the database.
type rowsRecorder interface {
	transactionRows() *transactionRows
}

// transactionRows answers the transaction's record of buffered columns and point reads.
func (c *SpannerReadWriteTransaction) transactionRows() *transactionRows {
	return c.rows
}

// keyedPatch is how the transaction asks a buffered patch for its row's key by column
// (keyByColumn).
type keyedPatch interface {
	keyByColumn(dbType DBType) (key map[string]any, ok bool)
}

// patchKeyByColumn answers a buffered patch's row's key by column on Spanner, nil when
// the patch keeps none.
func patchKeyByColumn(patch PatchSetMetadata) map[string]any {
	keyed, ok := patch.(keyedPatch)
	if !ok {
		return nil
	}
	key, ok := keyed.keyByColumn(SpannerDBType)
	if !ok {
		return nil
	}

	return key
}

// transactionReader is a transaction's point reader (transactionPointReader).
type transactionReader struct {
	txn            ReadWriteTransaction
	rows           *transactionRows
	notImplemented string
}

// transactionPointReader returns the transaction's point reader. A read first settles
// which row it names: a buffered write that holds the key column with the read's value
// (namedWrite); otherwise, when the table has a buffered write that keeps its key and
// not the key column, the row the database holds under the value, named again by its
// key. A named row answers with what the transaction's patches last wrote to its column
// (ownColumn), or, where none wrote it, the stored row's column. A read that names no
// row is answered by what the transaction read before, then the database. Each read of
// the database is remembered by its question, the value by its spelling (columnText).
// Reads are on Spanner alone: on another database the first read the database would
// answer is refused with notImplemented. A transaction with no tables to ask (the
// Mock's) is asked nothing, and answers that there is no such row.
func transactionPointReader(txn ReadWriteTransaction, notImplemented string) pointReader {
	reader := &transactionReader{txn: txn, notImplemented: notImplemented}
	if recorder, ok := txn.(rowsRecorder); ok {
		reader.rows = recorder.transactionRows()
	}

	return reader.read
}

// read answers column of the row of table whose keyColumn holds value (pointReader).
func (r *transactionReader) read(ctx context.Context, table, keyColumn string, value any, column string) (any, bool, error) {
	spelled, err := columnText(value)
	if err != nil {
		return nil, false, errors.Wrapf(err, "%s.%s", table, keyColumn)
	}
	read := pointRead{table: table, keyColumn: keyColumn, value: spelled, column: column}
	if r.rows != nil {
		if write, named := r.rows.namedWrite(read); named {
			if answer, known := r.rows.ownColumn(table, write.row, column); known {
				return answer, true, nil
			}
			// A row keyed by the column read by is read as it always was.
			if write.keyText != "" && !keyIsColumn(write.key, keyColumn) {
				return r.storedByKey(ctx, table, write, column)
			}
		} else if keyColumns, ok := r.rows.keyColumnsBeside(table, keyColumn); ok {
			return r.namedByDatabase(ctx, read, value, keyColumns)
		}
	}

	return r.stored(ctx, read, value)
}

// keyIsColumn reports whether a key by column is the one column.
func keyIsColumn(key map[string]any, column string) bool {
	_, ok := key[column]

	return ok && len(key) == 1
}

// querier answers what the transaction's reads ask: nil for a transaction with no
// tables to ask, and the refusal on a database the reads are not written for.
func (r *transactionReader) querier() (spxapi.Querier, error) {
	if r.txn.DBType() != SpannerDBType {
		return nil, errors.Newf("%s is not implemented for %s", r.notImplemented, r.txn.DBType())
	}

	return r.txn.SpannerReadOnlyTransaction(), nil
}

// stored answers a read that names no buffered row: what the transaction read before,
// then the database, whose answer it remembers.
func (r *transactionReader) stored(ctx context.Context, read pointRead, value any) (any, bool, error) {
	if r.rows != nil {
		if answer, ok := r.rows.remembered(read); ok {
			return answer.value, answer.found, nil
		}
	}
	querier, err := r.querier()
	if err != nil || querier == nil {
		return nil, false, err
	}
	next, found, err := spannerPointRead(ctx, querier, read.table, read.keyColumn, value, read.column)
	if err != nil {
		return nil, false, err
	}
	if r.rows != nil {
		r.rows.remember(read, pointAnswer{value: next, found: found})
	}

	return next, found, nil
}

// storedByKey answers column of a buffered row no buffered write of which says it: the
// stored row's, read by the row's key by column and remembered by its spelling.
func (r *transactionReader) storedByKey(ctx context.Context, table string, write bufferedColumns, column string) (any, bool, error) {
	read := keyedRead{table: table, key: write.keyText, column: column}
	if answer, ok := r.rows.keyReads[read]; ok {
		return answer.value, answer.found, nil
	}
	querier, err := r.querier()
	if err != nil || querier == nil {
		return nil, false, err
	}
	columns := slices.Sorted(maps.Keys(write.key))
	where := make([]string, 0, len(columns))
	params := make(map[string]any, len(columns))
	for i, keyColumn := range columns {
		param := fmt.Sprintf("k%d", i)
		where = append(where, "`"+keyColumn+"` = @"+param)
		params[param] = write.key[keyColumn]
	}
	stmt := spanner.Statement{
		SQL:    "SELECT `" + column + "` FROM `" + table + "` WHERE " + strings.Join(where, " AND "),
		Params: params,
	}
	values, found, err := spannerFirstRow(ctx, querier, stmt)
	if err != nil {
		return nil, false, err
	}
	answer := pointAnswer{found: found}
	if found {
		answer.value = values[0]
	}
	r.rows.keyReads[read] = answer

	return answer.value, answer.found, nil
}

// namedByDatabase answers a read by a column that is not the key of the table's rows,
// when a buffered write may have changed the row without holding that column: the
// database names the row under the value by its key, read once with the column and
// remembered, and a buffered write of that row answers with its newest say on the
// column (ownColumn); otherwise the column just read answers.
func (r *transactionReader) namedByDatabase(ctx context.Context, read pointRead, value any, keyColumns []string) (any, bool, error) {
	row, ok := r.rows.rowReads[read]
	if !ok {
		querier, err := r.querier()
		if err != nil || querier == nil {
			return nil, false, err
		}
		selected := make([]string, 0, len(keyColumns)+1)
		for _, keyColumn := range keyColumns {
			selected = append(selected, "`"+keyColumn+"`")
		}
		selected = append(selected, "`"+read.column+"`")
		stmt := spanner.Statement{
			SQL:    "SELECT " + strings.Join(selected, ", ") + " FROM `" + read.table + "` WHERE `" + read.keyColumn + "` = @value",
			Params: map[string]any{"value": value},
		}
		values, found, err := spannerFirstRow(ctx, querier, stmt)
		if err != nil {
			return nil, false, err
		}
		if found {
			key := make(map[string]any, len(keyColumns))
			for i, keyColumn := range keyColumns {
				key[keyColumn] = values[i]
			}
			keyText, err := keySpelling(key)
			if err != nil {
				return nil, false, errors.Wrapf(err, "the key of a row of %s", read.table)
			}
			row = storedRow{found: true, key: keyText, value: values[len(keyColumns)]}
		}
		r.rows.rowReads[read] = row
	}
	if !row.found {
		return nil, false, nil
	}
	if write, ok := r.rows.writeOfKey(read.table, row.key); ok {
		if answer, known := r.rows.ownColumn(read.table, write.row, read.column); known {
			return answer, true, nil
		}
	}

	return row.value, true, nil
}

// spannerPointRead reads column of the row of table whose keyColumn holds value, the
// value bound in its own type, and answers the column as Spanner returned it (a
// spanner.GenericColumnValue), and whether there is such a row.
func spannerPointRead(ctx context.Context, querier spxapi.Querier, table, keyColumn string, value any, column string) (next any, found bool, err error) {
	stmt := spanner.Statement{
		SQL:    "SELECT `" + column + "` FROM `" + table + "` WHERE `" + keyColumn + "` = @value",
		Params: map[string]any{"value": value},
	}
	values, found, err := spannerFirstRow(ctx, querier, stmt)
	if err != nil || !found {
		return nil, false, err
	}

	return values[0], true, nil
}

// spannerFirstRow runs a statement and answers each column of its first row as Spanner
// returned it (a spanner.GenericColumnValue), and whether there is a row.
func spannerFirstRow(ctx context.Context, querier spxapi.Querier, stmt spanner.Statement) (values []any, found bool, err error) {
	it := querier.Query(ctx, stmt)
	defer it.Stop()

	row, err := it.Next()
	if err != nil {
		if errors.Is(err, iterator.Done) {
			return nil, false, nil
		}

		return nil, false, errors.Wrap(err, "spanner.RowIterator.Next()")
	}
	values = make([]any, row.Size())
	for i := range values {
		var value spanner.GenericColumnValue
		if err := row.Column(i, &value); err != nil {
			return nil, false, errors.Wrap(err, "spanner.Row.Column()")
		}
		values[i] = value
	}

	return values, true, nil
}

// changeRecordTenantNotImplemented is the refusal of a read a change record's tenant
// would need on a database the reads are not written for.
const changeRecordTenantNotImplemented = "finding the tenant of a changed row"

// changeRecordTenant answers the tenant a change record of the patch's row carries: the
// key of the tenant the row belongs to once this write is made, by what the transaction
// has buffered until then, the empty string for a resource
// that declares no way to a tenant or a row whose way is empty. deleted is a delete's
// change set, which holds the row's values as they were; any other patch passes nil.
func (p *PatchSet[Resource]) changeRecordTenant(ctx context.Context, txn ReadWriteTransaction, deleted map[accesstypes.Field]DiffElem) (string, error) {
	binding := p.querySet.rMeta.tenantBinding
	if binding == nil {
		return "", nil
	}

	// A write armed in a tenant's request: the tenancy check holds the row to the
	// request's tenant, so nothing is read.
	tenancy, err := p.mutationTenancy()
	if err != nil {
		return "", err
	}
	if tenancy != nil && tenancy.domain != "" {
		return string(tenancy.domain), nil
	}

	anchor, err := p.tenantAnchor(ctx, txn, binding.Field, deleted)
	if err != nil {
		return "", err
	}
	// A row that names a parent that is not there carries no tenant: the commit fails on
	// its foreign key, and the record is never written.
	tenant, _, err := followTenantPath(ctx, transactionPointReader(txn, changeRecordTenantNotImplemented), binding.Field, anchor, binding.Path)
	if err != nil {
		return "", err
	}

	return tenant, nil
}

// followTenantPath follows a row's way to its tenant from anchor, the row's value of
// field as held: each step is one point read by the value the step before answered, as
// held. It answers the tenant's key, spelled (columnText). An anchor, or a step's answer,
// whose spelling is empty ends the way with no tenant, the empty string; missing is true
// when a step names a row that is not there.
func followTenantPath(ctx context.Context, read pointReader, field accesstypes.Field, anchor any, path []BindingHop) (tenant string, missing bool, err error) {
	value := anchor
	for _, hop := range path {
		spelled, err := columnText(value)
		if err != nil {
			return "", false, errors.Wrapf(err, "the way to the tenant of field %s", field)
		}
		if spelled == "" {
			return "", false, nil
		}
		next, found, err := read(ctx, hop.Table, hop.JoinColumn, value, hop.Column)
		if err != nil {
			return "", false, errors.Wrapf(err, "reading %s.%s", hop.Table, hop.Column)
		}
		if !found {
			return "", true, nil
		}
		value = next
	}
	tenant, err = columnText(value)
	if err != nil {
		return "", false, errors.Wrapf(err, "the tenant of field %s", field)
	}

	return tenant, false, nil
}

// tenantAnchor reads the row's value of the field its way to its tenant leaves through,
// as held: the patch's own value, the patch's key, nothing for a create that does not
// set it, what the transaction's patches buffered for the row before, a deleted row's
// value as the delete read it, and otherwise the stored row's, one point read on the
// primary key. A row that is not stored, and a transaction with no tables to ask (the
// Mock's), answer nil.
func (p *PatchSet[Resource]) tenantAnchor(ctx context.Context, txn ReadWriteTransaction, field accesstypes.Field, deleted map[accesstypes.Field]DiffElem) (any, error) {
	switch {
	case p.IsSet(field):
		return p.Get(field), nil
	case p.Key(field) != nil:
		return p.Key(field), nil
	case p.patchType == CreatePatchType:
		return nil, nil
	}
	if value, known := p.bufferedOwnColumn(txn, field); known {
		return value, nil
	}
	if p.patchType == DeletePatchType {
		return deleted[field].Old, nil
	}

	if txn.DBType() != SpannerDBType {
		return nil, errors.Newf("%s is not implemented for %s", changeRecordTenantNotImplemented, txn.DBType())
	}
	if txn.SpannerReadOnlyTransaction() == nil {
		return nil, nil
	}
	value, found, err := p.storedRowValue(ctx, txn, field)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}

	return value, nil
}
