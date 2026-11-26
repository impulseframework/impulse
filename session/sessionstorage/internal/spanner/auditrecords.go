package spanner

import (
	"context"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/auditlog"
)

// auditCreatedAtColumn is the audit records table's column of when a row was written: the
// commit's time.
const auditCreatedAtColumn = "CreatedAt"

// auditRecordMutations answers the mutation that writes record with the act it records:
// one row of the audit records table, whose columns are the record's
// (auditlog.Record.Columns) and CreatedAt the commit's time, and none for a nil record. A
// record that does not validate is an error, and the act writes nothing. The row is
// removed by the table's row deletion policy, written into the application's migration
// (auditlog.SpannerDDL).
func (s *SessionStorageDriver) auditRecordMutations(record *auditlog.Record) ([]*spanner.Mutation, error) {
	if record == nil {
		return nil, nil
	}
	columns, err := record.Columns()
	if err != nil {
		return nil, errors.Wrap(err, "auditlog.Record.Columns()")
	}
	columns[auditCreatedAtColumn] = spanner.CommitTimestamp

	return []*spanner.Mutation{spanner.InsertMap(s.auditRecordsTableName, columns)}, nil
}

// bufferAuditRecord buffers record in txn, the transaction of the act it records
// (auditRecordMutations); a nil record buffers nothing.
func (s *SessionStorageDriver) bufferAuditRecord(txn *spanner.ReadWriteTransaction, record *auditlog.Record) error {
	mutations, err := s.auditRecordMutations(record)
	if err != nil {
		return err
	}
	if len(mutations) == 0 {
		return nil
	}
	if err := txn.BufferWrite(mutations); err != nil {
		return errors.Wrap(err, "spanner.ReadWriteTransaction.BufferWrite()")
	}

	return nil
}

// InsertAuditRecordIn buffers record, one row of the Auth's audit log, inside tx: the
// sign-in flow's write of the row of an act it does in a transaction of its own
// (signinflow.Driver.InsertAuditRecordIn). A nil record writes nothing.
func (s *SessionStorageDriver) InsertAuditRecordIn(_ context.Context, tx *spanner.ReadWriteTransaction, record *auditlog.Record) error {
	return s.bufferAuditRecord(tx, record)
}
