package postgres

import (
	"context"
	"fmt"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/tracer"
	"github.com/jackc/pgx/v5"
)

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
	tag, err := s.conn.Exec(ctx, fmt.Sprintf(`UPDATE %s SET "Name" = $2 WHERE "Id" = $1`, s.userTable()), userID, clean)
	if err != nil {
		return errors.Wrap(err, "Queryer.Exec()")
	}
	if tag.RowsAffected() == 0 {
		return httpio.NewNotFoundMessagef("user id %q does not exist", userID)
	}

	return nil
}

// ClaimUserName is ClaimUserNameIn in a transaction of its own.
func (s *SessionStorageDriver) ClaimUserName(ctx context.Context, userID impulse.UUID, name string) (bool, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	var claimed bool
	err := s.Transact(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		claimed, err = s.ClaimUserNameIn(ctx, tx, userID, name)

		return err
	})
	if err != nil {
		return false, err
	}

	return claimed, nil
}

// ClaimUserNameIn writes name on the user when the user holds none, and reports
// whether it wrote: the name a directory asserted at a sign-in, or the one an accepted
// invitation carried. A user that holds a name keeps it, so a name the person wrote
// themselves is never replaced. A name that is nothing but space, one too long to
// keep and an unknown user write nothing.
func (s *SessionStorageDriver) ClaimUserNameIn(ctx context.Context, tx pgx.Tx, userID impulse.UUID, name string) (bool, error) {
	clean, err := sessioninfo.CleanName(&name)
	if err != nil || clean == nil {
		return false, nil //nolint:nilerr // a name the store would refuse is not claimed; the sign-in it came with stands
	}
	tag, err := tx.Exec(ctx, fmt.Sprintf(`UPDATE %s SET "Name" = $2 WHERE "Id" = $1 AND "Name" IS NULL`, s.userTable()), userID, *clean)
	if err != nil {
		return false, errors.Wrap(err, "pgx.Tx.Exec()")
	}

	return tag.RowsAffected() > 0, nil
}
