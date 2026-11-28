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

// signInMethodColumns are the sign-in methods table's columns, in signInMethodRow's
// order.
const signInMethodColumns = `"Name", "OpenSignUp", "OpenedFor", "DefaultRole", "SelfServiceReset", "UpdatedAt"`

// signInMethodRow is a sign-in method's row as the table holds it.
type signInMethodRow struct {
	Name             string    `db:"Name"`
	OpenSignUp       bool      `db:"OpenSignUp"`
	OpenedFor        *string   `db:"OpenedFor"`
	DefaultRole      *string   `db:"DefaultRole"`
	SelfServiceReset bool      `db:"SelfServiceReset"`
	UpdatedAt        time.Time `db:"UpdatedAt"`
}

func (r *signInMethodRow) record() *dbtype.SignInMethod {
	return &dbtype.SignInMethod{
		Name: r.Name, OpenSignUp: r.OpenSignUp, OpenedFor: r.OpenedFor, DefaultRole: r.DefaultRole, SelfServiceReset: r.SelfServiceReset, UpdatedAt: r.UpdatedAt,
	}
}

func (s *SessionStorageDriver) signInMethodsTable() string {
	return pgx.Identifier{s.signInMethodsTableName}.Sanitize()
}

// SignInMethodIn reads the row of the sign-in method registered under name inside tx,
// or nil when the method has none, which is a method that is invite only. A read that
// fails, as it does when the table is missing, is returned as its own error, never
// taken for "no row". The row is held against a change until tx ends (FOR SHARE), so a
// sign-in that read the method open commits before the change that makes it invite
// only does, and none commits after it on what it read before.
func (s *SessionStorageDriver) SignInMethodIn(ctx context.Context, tx pgx.Tx, name string) (*dbtype.SignInMethod, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	query := fmt.Sprintf(`SELECT %s FROM %s WHERE "Name" = $1 FOR SHARE`, signInMethodColumns, s.signInMethodsTable())

	var row signInMethodRow
	if err := pgxscan.Get(ctx, tx, &row, query, name); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}

		return nil, errors.Wrap(err, "pgxscan.Get()")
	}

	return row.record(), nil
}

// SignInMethods lists every row of the sign-in methods table, by name: the methods an
// administrator has made a choice for. A method with no row is not among them.
func (s *SessionStorageDriver) SignInMethods(ctx context.Context) ([]*dbtype.SignInMethod, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	query := fmt.Sprintf(`SELECT %s FROM %s ORDER BY "Name"`, signInMethodColumns, s.signInMethodsTable())

	var rows []*signInMethodRow
	if err := pgxscan.Select(ctx, s.conn, &rows, query); err != nil {
		return nil, errors.Wrap(err, "pgxscan.Select()")
	}
	methods := make([]*dbtype.SignInMethod, 0, len(rows))
	for _, row := range rows {
		methods = append(methods, row.record())
	}

	return methods, nil
}

// SetSignInMethod writes the row of the sign-in method m names, replacing the one it
// had, with UpdatedAt the database's time.
func (s *SessionStorageDriver) SetSignInMethod(ctx context.Context, m *dbtype.SignInMethod, record *auditlog.Record) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	query := fmt.Sprintf(`INSERT INTO %s (%s) VALUES ($1, $2, $3, $4, $5, now())
		ON CONFLICT ("Name") DO UPDATE SET "OpenSignUp" = EXCLUDED."OpenSignUp", "OpenedFor" = EXCLUDED."OpenedFor", "DefaultRole" = EXCLUDED."DefaultRole",
			"SelfServiceReset" = EXCLUDED."SelfServiceReset", "UpdatedAt" = EXCLUDED."UpdatedAt"`,
		s.signInMethodsTable(), signInMethodColumns)

	return s.recordedWrite(ctx, record, func(ctx context.Context, db statementRunner) error {
		if _, err := db.Exec(ctx, query, m.Name, m.OpenSignUp, m.OpenedFor, m.DefaultRole, m.SelfServiceReset); err != nil {
			return errors.Wrap(err, "Exec()")
		}

		return nil
	})
}

// SetSelfServiceReset writes whether a person may ask for their own password reset
// link on the row of the sign-in method registered under method, with UpdatedAt the
// database's time, and leaves what else the row says as it stands. It is one
// statement: a method with no row gets one that is invite only, and a row there has
// the one field and its time written, never the row read and written back.
func (s *SessionStorageDriver) SetSelfServiceReset(ctx context.Context, method string, on bool, record *auditlog.Record) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	query := fmt.Sprintf(`INSERT INTO %s (%s) VALUES ($1, false, NULL, NULL, $2, now())
		ON CONFLICT ("Name") DO UPDATE SET "SelfServiceReset" = EXCLUDED."SelfServiceReset", "UpdatedAt" = EXCLUDED."UpdatedAt"`,
		s.signInMethodsTable(), signInMethodColumns)

	return s.recordedWrite(ctx, record, func(ctx context.Context, db statementRunner) error {
		if _, err := db.Exec(ctx, query, method, on); err != nil {
			return errors.Wrap(err, "Exec()")
		}

		return nil
	})
}
