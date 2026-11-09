package spanner

import (
	"context"
	"reflect"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/tracer"
	"google.golang.org/grpc/codes"
)

// CustomUserDataConfig configures the custom user data table for the Spanner driver.
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

	row, err := s.spanner.Single().ReadRow(ctx, s.customUserData.TableName, spanner.Key{userID}, s.customUserData.Codec.Columns())
	switch {
	case err == nil:
		if err := row.ToStruct(data); err != nil {
			return nil, errors.Wrap(err, "spanner.Row.ToStruct()")
		}
	case spanner.ErrCode(err) == codes.NotFound:
		// No custom user data row: zero-value *U.
	default:
		return nil, errors.Wrap(err, "spanner.ReadOnlyTransaction.ReadRow()")
	}

	return data, nil
}

// UpdateCustomUserData updates the custom user data for an existing user via a
// transactional read-modify-write: the current row is read (zero-value struct when no
// row exists), mutate is applied, and the full row is written back. The user's
// existence is verified inside the same transaction; a mutate error aborts the
// transaction with nothing written.
func (s *SessionStorageDriver) UpdateCustomUserData(ctx context.Context, userID impulse.UUID, mutate func(data any) error) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if s.customUserData == nil {
		return errors.New("custom user data config is not set")
	}

	_, err := s.spanner.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		if _, err := txn.ReadRow(ctx, s.userDataParentTable(), spanner.Key{userID}, []string{"Id"}); err != nil {
			if spanner.ErrCode(err) == codes.NotFound {
				return httpio.NewNotFoundMessagef("user id %q does not exist", userID)
			}

			return errors.Wrap(err, "spanner.ReadWriteTransaction.ReadRow()")
		}

		data := s.customUserData.Codec.NewStruct()

		row, err := txn.ReadRow(ctx, s.customUserData.TableName, spanner.Key{userID}, s.customUserData.Codec.Columns())
		switch {
		case err == nil:
			if err := row.ToStruct(data); err != nil {
				return errors.Wrap(err, "spanner.Row.ToStruct()")
			}
		case spanner.ErrCode(err) == codes.NotFound:
			// No custom user data row yet: mutate receives the zero value and the
			// write below creates the row.
		default:
			return errors.Wrap(err, "spanner.ReadWriteTransaction.ReadRow()")
		}

		if err := mutate(data); err != nil {
			return errors.Wrap(err, "custom user data mutate func")
		}

		m, err := customRowMutation(s.customUserData.TableName, s.customUserData.Codec, dbtype.UserIDColumn, userID, data, spanner.InsertOrUpdateMap)
		if err != nil {
			return err
		}
		if err := txn.BufferWrite([]*spanner.Mutation{m}); err != nil {
			return errors.Wrap(err, "txn.BufferWrite()")
		}

		return nil
	})
	if err != nil {
		return errors.Wrap(err, "spanner.Client.ReadWriteTransaction()")
	}

	return nil
}
