package spanner

import (
	"context"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/tracer"
	"google.golang.org/grpc/codes"
)

// The columns of the user table that hold the look the person chose.
const (
	lookSchemeColumn  = "LookScheme"
	lookPaletteColumn = "LookPalette"
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
	row := map[string]any{
		idColumn:          userID,
		lookSchemeColumn:  spanner.NullString{StringVal: clean.Scheme, Valid: clean.Scheme != ""},
		lookPaletteColumn: spanner.NullString{StringVal: clean.Palette, Valid: clean.Palette != ""},
	}
	if _, err := s.spanner.Apply(ctx, []*spanner.Mutation{spanner.UpdateMap(s.userTableName, row)}); err != nil {
		if spanner.ErrCode(err) == codes.NotFound {
			return dbtype.AccountNotFound(userID)
		}

		return errors.Wrap(err, "spanner.Client.Apply()")
	}

	return nil
}
