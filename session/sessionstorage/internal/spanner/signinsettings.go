package spanner

import (
	"context"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/auditlog"
	"github.com/impulseframework/impulse/internal/spannertxn"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/tracer"
	"google.golang.org/api/iterator"
)

// The sign-in settings table's columns that hold a setting.
const (
	requireSecondFactorColumn = "RequireSecondFactor"
	rememberBrowserDaysColumn = "RememberBrowserDays"
)

// signInSettingsColumns are the sign-in settings table's columns.
var signInSettingsColumns = []string{nameColumn, requireSecondFactorColumn, rememberBrowserDaysColumn, updatedAtColumn}

// signInSettingsRow is the sign-in settings' row as the table holds it.
type signInSettingsRow struct {
	Name                string    `spanner:"Name"`
	RequireSecondFactor bool      `spanner:"RequireSecondFactor"`
	RememberBrowserDays int64     `spanner:"RememberBrowserDays"`
	UpdatedAt           time.Time `spanner:"UpdatedAt"`
}

func (r *signInSettingsRow) record() *dbtype.SignInSettings {
	return &dbtype.SignInSettings{RequireSecondFactor: r.RequireSecondFactor, RememberBrowserDays: r.RememberBrowserDays, UpdatedAt: r.UpdatedAt}
}

// readSignInSettings reads the Auth's row of the sign-in settings table through read,
// or nil when there is none. A read that fails, as it does when the table is missing,
// is returned naming the table, never taken for "no row", since no row is a second
// factor required of everyone.
func readSignInSettings(ctx context.Context, read rowReader, table string) (*dbtype.SignInSettings, error) {
	iter := read(ctx, table, spanner.Key{dbtype.SignInSettingsName}, signInSettingsColumns)
	defer iter.Stop()

	row, err := iter.Next()
	if errors.Is(err, iterator.Done) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.Wrapf(err, "reading %s", table)
	}
	var r signInSettingsRow
	if err := row.ToStruct(&r); err != nil {
		return nil, errors.Wrap(err, "spanner.Row.ToStruct()")
	}

	return r.record(), nil
}

// SignInSettings reads the Auth's row of the sign-in settings table, or nil when there
// is none, which requires the second factor and remembers a browser for the default
// period.
func (s *SessionStorageDriver) SignInSettings(ctx context.Context) (*dbtype.SignInSettings, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	return readSignInSettings(ctx, s.spanner.Single().Read, s.signInSettingsTableName)
}

// SignInSettingsIn reads the Auth's row of the sign-in settings table inside tx, or nil
// when there is none.
func (s *SessionStorageDriver) SignInSettingsIn(ctx context.Context, tx *spanner.ReadWriteTransaction) (*dbtype.SignInSettings, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	return readSignInSettings(ctx, tx.Read, s.signInSettingsTableName)
}

// SetRequireSecondFactor writes whether everyone is asked for a second factor on the
// Auth's row of the sign-in settings table, with UpdatedAt the commit time, and leaves
// how long a browser is remembered as it stands. With no row, the row is made with the
// default period.
func (s *SessionStorageDriver) SetRequireSecondFactor(ctx context.Context, on bool, record *auditlog.Record) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	return s.setSignInSetting(ctx, requireSecondFactorColumn, on, rememberBrowserDaysColumn, dbtype.DefaultRememberBrowserDays, record)
}

// SetRememberBrowserDays writes for how many days a remembered browser skips the code on
// the Auth's row of the sign-in settings table, with UpdatedAt the commit time, and
// leaves whether a second factor is required as it stands. With no row, the row is made
// with the second factor required.
func (s *SessionStorageDriver) SetRememberBrowserDays(ctx context.Context, days int64, record *auditlog.Record) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	return s.setSignInSetting(ctx, rememberBrowserDaysColumn, days, requireSecondFactorColumn, true, record)
}

// setSignInSetting writes value to column on the Auth's row of the sign-in settings
// table, with UpdatedAt the commit time. The row is read inside a serializable
// transaction that buffers the write: a row there has the one field and its time
// updated, never the row read and written back, and with no row the row is made with
// other at its default. record, nil for none, is buffered with the write.
func (s *SessionStorageDriver) setSignInSetting(ctx context.Context, column string, value any, other string, otherDefault any, record *auditlog.Record) error {
	_, err := spannertxn.Serializable(ctx, s.spanner, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		standing, err := s.SignInSettingsIn(ctx, txn)
		if err != nil {
			return err
		}
		row := map[string]any{nameColumn: dbtype.SignInSettingsName, column: value, updatedAtColumn: spanner.CommitTimestamp}
		write := spanner.UpdateMap(s.signInSettingsTableName, row)
		if standing == nil {
			row[other] = otherDefault
			write = spanner.InsertMap(s.signInSettingsTableName, row)
		}
		if err := txn.BufferWrite([]*spanner.Mutation{write}); err != nil {
			return errors.Wrap(err, "spanner.ReadWriteTransaction.BufferWrite()")
		}

		return s.bufferAuditRecord(txn, record)
	})
	if err != nil {
		return errors.Wrap(err, "spannertxn.Serializable()")
	}

	return nil
}
