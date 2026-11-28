package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/georgysavva/scany/v2/pgxscan"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/auditlog"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/tracer"
	"github.com/jackc/pgx/v5"
)

// signInSettingsColumns are the sign-in settings table's columns, in signInSettingsRow's
// order.
const signInSettingsColumns = `"Name", "RequireSecondFactor", "RememberBrowserDays", "UpdatedAt"`

// signInSettingsRow is the sign-in settings' row as the table holds it.
type signInSettingsRow struct {
	Name                string    `db:"Name"`
	RequireSecondFactor bool      `db:"RequireSecondFactor"`
	RememberBrowserDays int64     `db:"RememberBrowserDays"`
	UpdatedAt           time.Time `db:"UpdatedAt"`
}

func (r *signInSettingsRow) record() *dbtype.SignInSettings {
	return &dbtype.SignInSettings{RequireSecondFactor: r.RequireSecondFactor, RememberBrowserDays: r.RememberBrowserDays, UpdatedAt: r.UpdatedAt}
}

func (s *SessionStorageDriver) signInSettingsTable() string {
	return pgx.Identifier{s.signInSettingsTableName}.Sanitize()
}

// readSignInSettings reads the Auth's row of the sign-in settings table through q with
// query, or nil when there is none. A read that fails, as it does when the table is
// missing, is returned as its own error, never taken for "no row", since no row is a
// second factor required of everyone.
func readSignInSettings(ctx context.Context, q pgxscan.Querier, query string) (*dbtype.SignInSettings, error) {
	var row signInSettingsRow
	if err := pgxscan.Get(ctx, q, &row, query, dbtype.SignInSettingsName); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}

		return nil, errors.Wrap(err, "pgxscan.Get()")
	}

	return row.record(), nil
}

// SignInSettings reads the Auth's row of the sign-in settings table, or nil when there
// is none, which requires the second factor and remembers a browser for the default
// period.
func (s *SessionStorageDriver) SignInSettings(ctx context.Context) (*dbtype.SignInSettings, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	query := fmt.Sprintf(`SELECT %s FROM %s WHERE "Name" = $1`, signInSettingsColumns, s.signInSettingsTable())

	return readSignInSettings(ctx, s.conn, query)
}

// SignInSettingsIn reads the Auth's row of the sign-in settings table inside tx, or nil
// when there is none. The row is held against a change until tx ends (FOR SHARE), so a
// sign-in that read the second factor not required commits before the change that
// requires it does, and none commits after it on what it read before.
func (s *SessionStorageDriver) SignInSettingsIn(ctx context.Context, tx pgx.Tx) (*dbtype.SignInSettings, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	query := fmt.Sprintf(`SELECT %s FROM %s WHERE "Name" = $1 FOR SHARE`, signInSettingsColumns, s.signInSettingsTable())

	return readSignInSettings(ctx, tx, query)
}

// SetRequireSecondFactor writes whether everyone is asked for a second factor on the
// Auth's row of the sign-in settings table, with UpdatedAt the database's time, and
// leaves how long a browser is remembered as it stands. It is one statement: with no
// row, the row is made with the default period, and a row there has the one field and
// its time written, never the row read and written back.
func (s *SessionStorageDriver) SetRequireSecondFactor(ctx context.Context, on bool, record *auditlog.Record) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	query := fmt.Sprintf(`INSERT INTO %s (%s) VALUES ($1, $2, $3, now())
		ON CONFLICT ("Name") DO UPDATE SET "RequireSecondFactor" = EXCLUDED."RequireSecondFactor", "UpdatedAt" = EXCLUDED."UpdatedAt"`,
		s.signInSettingsTable(), signInSettingsColumns)

	return s.recordedWrite(ctx, record, func(ctx context.Context, db statementRunner) error {
		if _, err := db.Exec(ctx, query, dbtype.SignInSettingsName, on, dbtype.DefaultRememberBrowserDays); err != nil {
			return errors.Wrap(err, "Exec()")
		}

		return nil
	})
}

// SetRememberBrowserDays writes for how many days a remembered browser skips the code on
// the Auth's row of the sign-in settings table, with UpdatedAt the database's time, and
// leaves whether a second factor is required as it stands. It is one statement: with no
// row, the row is made with the second factor required, and a row there has the one
// field and its time written, never the row read and written back.
func (s *SessionStorageDriver) SetRememberBrowserDays(ctx context.Context, days int64, record *auditlog.Record) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	query := fmt.Sprintf(`INSERT INTO %s (%s) VALUES ($1, true, $2, now())
		ON CONFLICT ("Name") DO UPDATE SET "RememberBrowserDays" = EXCLUDED."RememberBrowserDays", "UpdatedAt" = EXCLUDED."UpdatedAt"`,
		s.signInSettingsTable(), signInSettingsColumns)

	return s.recordedWrite(ctx, record, func(ctx context.Context, db statementRunner) error {
		if _, err := db.Exec(ctx, query, dbtype.SignInSettingsName, days); err != nil {
			return errors.Wrap(err, "Exec()")
		}

		return nil
	})
}
