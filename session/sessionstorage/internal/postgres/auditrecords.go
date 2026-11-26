package postgres

import (
	"context"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/auditlog"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// auditRecordsRemoval takes the audit records older than the period the Auth keeps them
// for, $1 hours (SetAuditRecordsPeriod), the oldest first: the housekeeping of a write
// that inserts one. Spanner's table removes them by its row deletion policy.
var auditRecordsRemoval = removal{
	what:  "audit records older than the Auth keeps them",
	key:   idKey,
	due:   `"CreatedAt" < now() - make_interval(hours => $1::int)`,
	order: `"CreatedAt"`,
}

// statementRunner runs a write's statements: the connection, or a transaction.
type statementRunner interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// auditRecordsTable is the audit records table's name, quoted.
func (s *SessionStorageDriver) auditRecordsTable() string {
	return pgx.Identifier{s.auditRecordsTableName}.Sanitize()
}

// SetAuditRecordsPeriod sets how long the audit records are kept: a write that inserts
// one removes the rows older than period's days, counted in hours as the other removals
// are (removal). With KeepForever, or a period that declares nothing, no row is removed.
func (s *SessionStorageDriver) SetAuditRecordsPeriod(period auditlog.Period) {
	days, ok := period.Days()
	if !ok {
		s.auditRecordsHours = 0

		return
	}
	s.auditRecordsHours = days * 24
}

// recordedWrite runs write, the work of a method that is handed the record of its act. A
// nil record runs it on the connection, as the method runs with none; a record runs it
// in a transaction of its own that inserts the record after write's work
// (InsertAuditRecordIn), so the act and its row commit together or neither does.
func (s *SessionStorageDriver) recordedWrite(ctx context.Context, record *auditlog.Record, write func(ctx context.Context, db statementRunner) error) error {
	if record == nil {
		return write(ctx, s.conn)
	}

	return s.Transact(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := write(ctx, tx); err != nil {
			return err
		}

		return s.InsertAuditRecordIn(ctx, tx, record)
	})
}

// InsertAuditRecordIn runs the INSERT of record, one row of the Auth's audit log, inside
// tx, the transaction of the act it records, and then, in a savepoint of tx, removes the
// rows older than the period the Auth keeps them for (removeOldAuditRecords). A nil
// record writes nothing; a record that does not validate is an error, and the act's
// transaction does not commit.
func (s *SessionStorageDriver) InsertAuditRecordIn(ctx context.Context, tx pgx.Tx, record *auditlog.Record) error {
	if record == nil {
		return nil
	}
	columns, err := record.Columns()
	if err != nil {
		return errors.Wrap(err, "auditlog.Record.Columns()")
	}
	statement, args := auditlog.PostgresInsert(s.auditRecordsTableName, columns)
	if _, err := tx.Exec(ctx, statement, args...); err != nil {
		return errors.Wrap(err, "pgx.Tx.Exec()")
	}

	return s.removeOldAuditRecords(ctx, tx)
}

// removeOldAuditRecords removes, inside a savepoint of tx, the audit records older than
// the period the Auth keeps them for, at most dueRemovalLimit of them, as a sign-in
// removes the idle sessions (removeIdleSessions): a removal that fails is rolled back to
// the savepoint and logged, and the write goes on; a savepoint that cannot be opened,
// released or rolled back is the write's error. An Auth that keeps its records forever
// removes nothing.
func (s *SessionStorageDriver) removeOldAuditRecords(ctx context.Context, tx pgx.Tx) error {
	if s.auditRecordsHours == 0 {
		return nil
	}
	savepoint, err := tx.Begin(ctx)
	if err != nil {
		return errors.Wrap(err, "pgx.Tx.Begin()")
	}
	if !auditRecordsRemoval.remove(ctx, savepoint, s.auditRecordsTable(), s.auditRecordsHours) {
		if err := savepoint.Rollback(ctx); err != nil {
			return errors.Wrap(err, "pgx.Tx.Rollback()")
		}

		return nil
	}
	if err := savepoint.Commit(ctx); err != nil {
		return errors.Wrap(err, "pgx.Tx.Commit()")
	}

	return nil
}
