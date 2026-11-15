package postgres

import (
	"context"
	"fmt"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/tracer"
)

// SetUserLook writes the look the person chose on the user, as sessioninfo.CleanLook
// leaves it: the person's own change. It replaces the look the user held as a whole, so
// a half the look does not name is taken off.
func (s *SessionStorageDriver) SetUserLook(ctx context.Context, userID impulse.UUID, look sessioninfo.Look) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	clean, err := sessioninfo.CleanLook(look)
	if err != nil {
		return errors.Wrap(err, "sessioninfo.CleanLook()")
	}
	tag, err := s.conn.Exec(ctx, fmt.Sprintf(`UPDATE %s SET "LookScheme" = $2, "LookPalette" = $3 WHERE "Id" = $1`, s.userTable()),
		userID, orNull(clean.Scheme), orNull(clean.Palette))
	if err != nil {
		return errors.Wrap(err, "Queryer.Exec()")
	}
	if tag.RowsAffected() == 0 {
		return httpio.NewNotFoundMessagef("user id %q does not exist", userID)
	}

	return nil
}

// orNull is a text value for a nullable column: nil for the empty one.
func orNull(value string) *string {
	if value == "" {
		return nil
	}

	return &value
}
