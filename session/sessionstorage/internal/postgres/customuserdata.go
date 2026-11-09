package postgres

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/tracer"
	"github.com/jackc/pgx/v5"
)

// CustomUserDataConfig configures the custom user data table for the PostgreSQL driver.
// It is populated by the public sessionstorage package from a validated typed unit; the
// driver performs no validation of its own.
type CustomUserDataConfig struct {
	// TableName is the name of the custom user data table.
	TableName string
	// Codec maps the consumer's struct type U to its columns and provides the erased
	// value/scan operations. It is always non-nil.
	Codec *dbtype.CustomDataCodec
}

// SetCustomUserData attaches the custom user data configuration to the driver.
func (s *SessionStorageDriver) SetCustomUserData(config *CustomUserDataConfig) {
	s.customUserData = config
}

// CustomUserDataType returns the struct type the attached custom user data
// configuration was built for, or nil when no configuration is attached.
func (s *SessionStorageDriver) CustomUserDataType() reflect.Type {
	if s.customUserData == nil {
		return nil
	}

	return s.customUserData.Codec.StructType()
}

// userDataParentTable returns the user table the custom user data table references.
func (s *SessionStorageDriver) userDataParentTable() string {
	return s.userTableName
}

// CustomUserData returns the custom user data row for the given user ID as *U. A user
// with no custom data row yields a zero-value *U.
func (s *SessionStorageDriver) CustomUserData(ctx context.Context, userID impulse.UUID) (any, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if s.customUserData == nil {
		return nil, errors.New("custom user data config is not set")
	}

	data := s.customUserData.Codec.NewStruct()

	rows, err := s.conn.Query(ctx, s.customUserDataQuery(""), userID)
	if err != nil {
		return nil, errors.Wrap(err, "Queryer.Query()")
	}
	defer rows.Close()

	if rows.Next() {
		fieldAddrs, err := s.customUserData.Codec.FieldAddrs(data)
		if err != nil {
			return nil, errors.Wrap(err, "dbtype.CustomDataCodec.FieldAddrs()")
		}
		if err := rows.Scan(fieldAddrs...); err != nil {
			return nil, errors.Wrap(err, "pgx.Rows.Scan()")
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, errors.Wrap(err, "pgx.Rows.Err()")
	}

	return data, nil
}

// UpdateCustomUserData updates the custom user data for an existing user via a
// transactional read-modify-write: the current row is read FOR UPDATE (zero-value
// struct when no row exists), mutate is applied, and the full row is written back. The
// user's existence is verified inside the same transaction; a mutate error aborts the
// transaction with nothing written.
func (s *SessionStorageDriver) UpdateCustomUserData(ctx context.Context, userID impulse.UUID, mutate func(data any) error) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if s.customUserData == nil {
		return errors.New("custom user data config is not set")
	}

	txn, err := s.conn.Begin(ctx)
	if err != nil {
		return errors.Wrap(err, "Queryer.Begin()")
	}
	defer func() {
		_ = txn.Rollback(ctx)
	}()

	existsQuery := fmt.Sprintf(`SELECT 1 FROM %s WHERE "Id" = $1 FOR UPDATE`, pgx.Identifier{s.userDataParentTable()}.Sanitize())
	var one int
	if err := txn.QueryRow(ctx, existsQuery, userID).Scan(&one); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return httpio.NewNotFoundMessagef("user id %q does not exist", userID)
		}

		return errors.Wrap(err, "pgx.Tx.QueryRow().Scan()")
	}

	data := s.customUserData.Codec.NewStruct()

	rows, err := txn.Query(ctx, s.customUserDataQuery(" FOR UPDATE"), userID)
	if err != nil {
		return errors.Wrap(err, "pgx.Tx.Query()")
	}
	if rows.Next() {
		fieldAddrs, err := s.customUserData.Codec.FieldAddrs(data)
		if err != nil {
			rows.Close()

			return errors.Wrap(err, "dbtype.CustomDataCodec.FieldAddrs()")
		}
		if err := rows.Scan(fieldAddrs...); err != nil {
			rows.Close()

			return errors.Wrap(err, "pgx.Rows.Scan()")
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return errors.Wrap(err, "pgx.Rows.Err()")
	}

	if err := mutate(data); err != nil {
		return errors.Wrap(err, "custom user data mutate func")
	}

	if err := insertCustomRow(ctx, txn, s.customUserData.TableName, s.customUserData.Codec, dbtype.UserIDColumn, userID, data, true); err != nil {
		return err
	}

	if err := txn.Commit(ctx); err != nil {
		return errors.Wrap(err, "tx.Commit()")
	}

	return nil
}

// customUserDataQuery builds the custom user data row select, with suffix appended
// (e.g. " FOR UPDATE").
func (s *SessionStorageDriver) customUserDataQuery(suffix string) string {
	columns := s.customUserData.Codec.Columns()
	selectCols := make([]string, len(columns))
	for i, col := range columns {
		selectCols[i] = pgx.Identifier{col}.Sanitize()
	}

	return fmt.Sprintf(`SELECT %s FROM %s WHERE %s = $1%s`,
		strings.Join(selectCols, ", "),
		pgx.Identifier{s.customUserData.TableName}.Sanitize(),
		pgx.Identifier{dbtype.UserIDColumn}.Sanitize(),
		suffix,
	)
}
