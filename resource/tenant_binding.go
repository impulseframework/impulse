package resource

import (
	"context"
	"maps"

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
// see the transaction's buffered writes, so each step looks first at what the
// transaction's own patches buffered, and the transaction remembers each point read.
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
// row it wrote (rowMatchKey) and the columns it was given.
type bufferedColumns struct {
	patchType PatchType
	row       string
	columns   map[string]any
}

// transactionRows is the record a write transaction keeps for the point reads that
// follow a row to its parent: the columns each create, update and insert-or-update of
// its patches buffered, by table in buffered order, which no read of the transaction
// sees, and the answer to each point read it made. Like the other records of a
// transaction it belongs to one attempt: a retried transaction runs its function again
// over a fresh record.
type transactionRows struct {
	writes map[string][]bufferedColumns
	reads  map[pointRead]pointAnswer
}

// newTransactionRows returns an empty record.
func newTransactionRows() *transactionRows {
	return &transactionRows{
		writes: make(map[string][]bufferedColumns),
		reads:  make(map[pointRead]pointAnswer),
	}
}

// note records the columns a patch buffered for one row, which row is its key as
// rowMatchKey spells it. A delete is not noted.
func (t *transactionRows) note(table accesstypes.Resource, patchType PatchType, row string, columns map[string]any) {
	if patchType == DeletePatchType {
		return
	}
	t.writes[string(table)] = append(t.writes[string(table)], bufferedColumns{patchType: patchType, row: row, columns: maps.Clone(columns)})
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

// buffered answers a point read from what the transaction's patches buffered, newest
// first: a write whose key column is spelled as the read's value (columnText) is a write
// of the row, and a write whose key cannot be spelled is passed over. The answer is the
// buffered value as held. known is false when no buffered write says: no write of the
// row, or only updates that did not set the column. A create of the row that did not set
// the column answers nil.
func (t *transactionRows) buffered(read pointRead) (answer pointAnswer, known bool) {
	writes := t.writes[read.table]
	for i := len(writes) - 1; i >= 0; i-- {
		key, ok := writes[i].columns[read.keyColumn]
		if !ok {
			continue
		}
		keyText, err := columnText(key)
		if err != nil || keyText != read.value {
			continue
		}
		if value, ok := writes[i].columns[read.column]; ok {
			return pointAnswer{value: value, found: true}, true
		}
		if writes[i].patchType == CreatePatchType {
			return pointAnswer{value: nil, found: true}, true
		}
	}

	return pointAnswer{}, false
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

// transactionPointReader returns the transaction's point reader: what its own patches
// buffered, then what it read before, then the database, whose answer it remembers by
// the value's spelling (columnText). Reads are on Spanner alone: on another database
// the first read the database would answer is refused with notImplemented. A
// transaction with no tables to ask (the Mock's) is asked nothing, and answers that
// there is no such row.
func transactionPointReader(txn ReadWriteTransaction, notImplemented string) pointReader {
	var rows *transactionRows
	if recorder, ok := txn.(rowsRecorder); ok {
		rows = recorder.transactionRows()
	}

	return func(ctx context.Context, table, keyColumn string, value any, column string) (any, bool, error) {
		spelled, err := columnText(value)
		if err != nil {
			return nil, false, errors.Wrapf(err, "%s.%s", table, keyColumn)
		}
		read := pointRead{table: table, keyColumn: keyColumn, value: spelled, column: column}
		if rows != nil {
			if answer, known := rows.buffered(read); known {
				return answer.value, answer.found, nil
			}
			if answer, ok := rows.remembered(read); ok {
				return answer.value, answer.found, nil
			}
		}
		if txn.DBType() != SpannerDBType {
			return nil, false, errors.Newf("%s is not implemented for %s", notImplemented, txn.DBType())
		}
		querier := txn.SpannerReadOnlyTransaction()
		if querier == nil {
			return nil, false, nil
		}
		next, found, err := spannerPointRead(ctx, querier, table, keyColumn, value, column)
		if err != nil {
			return nil, false, err
		}
		if rows != nil {
			rows.remember(read, pointAnswer{value: next, found: found})
		}

		return next, found, nil
	}
}

// spannerPointRead reads column of the row of table whose keyColumn holds value, the
// value bound in its own type, and answers the column as Spanner returned it (a
// spanner.GenericColumnValue), and whether there is such a row.
func spannerPointRead(ctx context.Context, querier spxapi.Querier, table, keyColumn string, value any, column string) (next any, found bool, err error) {
	stmt := spanner.Statement{
		SQL:    "SELECT `" + column + "` FROM `" + table + "` WHERE `" + keyColumn + "` = @value",
		Params: map[string]any{"value": value},
	}
	it := querier.Query(ctx, stmt)
	defer it.Stop()

	row, err := it.Next()
	if err != nil {
		if errors.Is(err, iterator.Done) {
			return nil, false, nil
		}

		return nil, false, errors.Wrap(err, "spanner.RowIterator.Next()")
	}
	var answer spanner.GenericColumnValue
	if err := row.Column(0, &answer); err != nil {
		return nil, false, errors.Wrap(err, "spanner.Row.Column()")
	}

	return answer, true, nil
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
