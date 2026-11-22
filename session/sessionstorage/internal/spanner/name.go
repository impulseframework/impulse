package spanner

import (
	"context"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/internal/spannertxn"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/tracer"
	"google.golang.org/grpc/codes"
)

// nameColumn is the column of the user table that holds the person's name.
const nameColumn = "Name"

// SetUserName writes the person's name on the user, or takes it off (nil, or nothing
// but space): the person's own change, or an administrator's. It replaces whatever the
// user held.
func (s *SessionStorageDriver) SetUserName(ctx context.Context, userID impulse.UUID, name *string) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	clean, err := sessioninfo.CleanName(name)
	if err != nil {
		return errors.Wrap(err, "sessioninfo.CleanName()")
	}
	row := map[string]any{idColumn: userID, nameColumn: spanner.NullString{}}
	if clean != nil {
		row[nameColumn] = spanner.NullString{StringVal: *clean, Valid: true}
	}
	if _, err := s.spanner.Apply(ctx, []*spanner.Mutation{spanner.UpdateMap(s.userTableName, row)}); err != nil {
		if spanner.ErrCode(err) == codes.NotFound {
			return httpio.NewNotFoundMessagef("user id %q does not exist", userID)
		}

		return errors.Wrap(err, "spanner.Client.Apply()")
	}

	return nil
}

// ClaimUserName is ClaimUserNameIn in a transaction of its own.
func (s *SessionStorageDriver) ClaimUserName(ctx context.Context, userID impulse.UUID, name string) (bool, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	var claimed bool
	_, err := spannertxn.Serializable(ctx, s.spanner, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		var err error
		claimed, err = s.ClaimUserNameIn(ctx, txn, userID, name)

		return err
	})
	if err != nil {
		return false, errors.Wrap(err, "spannertxn.Serializable()")
	}

	return claimed, nil
}

// ClaimUserNameIn writes name on the user when the user holds none, and reports
// whether it wrote: the name a directory asserted at a sign-in, or the one an accepted
// invitation carried. A user that holds a name keeps it, so a name the person wrote
// themselves is never replaced. A name that is nothing but space, one too long to
// keep and an unknown user write nothing. The check is a read in the transaction and
// the write a buffered mutation, so a user inserted earlier in the same transaction,
// whose row no read sees yet, is not claimed for: its insert carries the name.
func (s *SessionStorageDriver) ClaimUserNameIn(ctx context.Context, tx *spanner.ReadWriteTransaction, userID impulse.UUID, name string) (bool, error) {
	clean, err := sessioninfo.CleanName(&name)
	if err != nil || clean == nil {
		return false, nil //nolint:nilerr // a name the store would refuse is not claimed; the sign-in it came with stands
	}
	row, err := tx.ReadRow(ctx, s.userTableName, spanner.Key{userID.String()}, []string{nameColumn})
	if err != nil {
		if spanner.ErrCode(err) == codes.NotFound {
			return false, nil
		}

		return false, errors.Wrap(err, "spanner.ReadWriteTransaction.ReadRow()")
	}
	var held spanner.NullString
	if err := row.Column(0, &held); err != nil {
		return false, errors.Wrap(err, "spanner.Row.Column()")
	}
	if held.Valid {
		return false, nil
	}
	if err := tx.BufferWrite([]*spanner.Mutation{spanner.UpdateMap(s.userTableName, map[string]any{idColumn: userID, nameColumn: *clean})}); err != nil {
		return false, errors.Wrap(err, "spanner.ReadWriteTransaction.BufferWrite()")
	}

	return true, nil
}
