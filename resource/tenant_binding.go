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
// empty string.

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

// pointReader answers the text of column of the row of table whose keyColumn holds
// value, and whether there is such a row.
type pointReader func(ctx context.Context, table, keyColumn, value, column string) (text string, found bool, err error)

// pointRead names one point read: column of the row of table whose keyColumn holds
// value.
type pointRead struct {
	table     string
	keyColumn string
	value     string
	column    string
}

// pointAnswer is what one point read answered.
type pointAnswer struct {
	text  string
	found bool
}

// bufferedColumns is one write a patch of the transaction buffered: its patch type and
// the columns it was given.
type bufferedColumns struct {
	patchType PatchType
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

// note records the columns a patch buffered. A delete is not noted.
func (t *transactionRows) note(table accesstypes.Resource, patchType PatchType, columns map[string]any) {
	if patchType == DeletePatchType {
		return
	}
	t.writes[string(table)] = append(t.writes[string(table)], bufferedColumns{patchType: patchType, columns: maps.Clone(columns)})
}

// buffered answers a point read from what the transaction's patches buffered, newest
// first. known is false when no buffered write says: no write of the row, or only
// updates that did not set the column. A create of the row that did not set the column
// answers the empty string.
func (t *transactionRows) buffered(read pointRead) (answer pointAnswer, known bool, err error) {
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
			text, err := columnText(value)
			if err != nil {
				return pointAnswer{}, false, errors.Wrapf(err, "%s.%s", read.table, read.column)
			}

			return pointAnswer{text: text, found: true}, true, nil
		}
		if writes[i].patchType == CreatePatchType {
			return pointAnswer{text: "", found: true}, true, nil
		}
	}

	return pointAnswer{}, false, nil
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
// buffered, then what it read before, then the database, whose answer it remembers.
// Reads are on Spanner alone: on another database the first read the database would
// answer is refused with notImplemented. A transaction with no tables to ask (the
// Mock's) is asked nothing, and answers that there is no such row.
func transactionPointReader(txn ReadWriteTransaction, notImplemented string) pointReader {
	var rows *transactionRows
	if recorder, ok := txn.(rowsRecorder); ok {
		rows = recorder.transactionRows()
	}

	return func(ctx context.Context, table, keyColumn, value, column string) (string, bool, error) {
		read := pointRead{table: table, keyColumn: keyColumn, value: value, column: column}
		if rows != nil {
			answer, known, err := rows.buffered(read)
			if err != nil {
				return "", false, err
			}
			if known {
				return answer.text, answer.found, nil
			}
			if answer, ok := rows.remembered(read); ok {
				return answer.text, answer.found, nil
			}
		}
		if txn.DBType() != SpannerDBType {
			return "", false, errors.Newf("%s is not implemented for %s", notImplemented, txn.DBType())
		}
		querier := txn.SpannerReadOnlyTransaction()
		if querier == nil {
			return "", false, nil
		}
		text, found, err := spannerPointText(ctx, querier, pointTextStatement(SpannerDBType, table, keyColumn, column, value))
		if err != nil {
			return "", false, err
		}
		if rows != nil {
			rows.remember(read, pointAnswer{text: text, found: found})
		}

		return text, found, nil
	}
}

// spannerPointText runs a statement that yields one text value, and says whether it
// yielded a row at all.
func spannerPointText(ctx context.Context, querier spxapi.Querier, stmt *Statement) (string, bool, error) {
	it := querier.Query(ctx, stmt.SpannerStatement())
	defer it.Stop()

	row, err := it.Next()
	if err != nil {
		if errors.Is(err, iterator.Done) {
			return "", false, nil
		}

		return "", false, errors.Wrap(err, "spanner.RowIterator.Next()")
	}
	var value spanner.NullString
	if err := row.Column(0, &value); err != nil {
		return "", false, errors.Wrap(err, "spanner.Row.Column()")
	}

	return value.StringVal, true, nil
}

// changeRecordTenantNotImplemented is the refusal of a read a change record's tenant
// would need on a database the reads are not written for.
const changeRecordTenantNotImplemented = "finding the tenant of a changed row"

// changeRecordTenant answers the tenant a change record of the patch's row carries: the
// key of the tenant the row belongs to after the write, the empty string for a resource
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
	if len(binding.Path) == 0 || anchor == "" {
		return anchor, nil
	}

	read := transactionPointReader(txn, changeRecordTenantNotImplemented)
	tenant := anchor
	for _, hop := range binding.Path {
		next, found, err := read(ctx, hop.Table, hop.JoinColumn, tenant, hop.Column)
		if err != nil {
			return "", errors.Wrapf(err, "reading %s.%s", hop.Table, hop.Column)
		}
		if !found {
			// The row names a parent that is not there: the commit fails on its foreign
			// key, and the record is never written.
			return "", nil
		}
		tenant = next
	}

	return tenant, nil
}

// tenantAnchor reads the row's value of the field its way to its tenant leaves through,
// as text: the patch's own value, the patch's key, a deleted row's value as the delete
// read it, nothing for a create that does not set it, and otherwise the stored row's,
// one point read on the primary key. A row that is not stored, and a transaction with
// no tables to ask (the Mock's), answer the empty string.
func (p *PatchSet[Resource]) tenantAnchor(ctx context.Context, txn ReadWriteTransaction, field accesstypes.Field, deleted map[accesstypes.Field]DiffElem) (string, error) {
	switch {
	case p.IsSet(field):
		text, err := columnText(p.Get(field))
		if err != nil {
			return "", errors.Wrapf(err, "field %s", field)
		}

		return text, nil
	case p.Key(field) != nil:
		text, err := columnText(p.Key(field))
		if err != nil {
			return "", errors.Wrapf(err, "field %s", field)
		}

		return text, nil
	case p.patchType == DeletePatchType:
		text, err := columnText(deleted[field].Old)
		if err != nil {
			return "", errors.Wrapf(err, "field %s", field)
		}

		return text, nil
	case p.patchType == CreatePatchType:
		return "", nil
	}

	if txn.DBType() != SpannerDBType {
		return "", errors.Newf("%s is not implemented for %s", changeRecordTenantNotImplemented, txn.DBType())
	}
	if txn.SpannerReadOnlyTransaction() == nil {
		return "", nil
	}
	text, found, err := p.rowText(ctx, txn, field)
	if err != nil {
		return "", err
	}
	if !found {
		return "", nil
	}

	return text, nil
}
