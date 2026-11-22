package spanner

import (
	"context"
	"fmt"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/tracer"
)

// ownSessionRow is one of a person's own sessions as the session table holds it.
type ownSessionRow struct {
	ID        impulse.UUID       `spanner:"Id"`
	Browser   spanner.NullString `spanner:"Browser"`
	CreatedAt time.Time          `spanner:"CreatedAt"`
	UpdatedAt time.Time          `spanner:"UpdatedAt"`
}

// OwnSessions returns the user's own live sessions, as their account page lists them:
// the rows of the session table that belong to userID, are not expired and were used
// at activeSince or later, newest use first. A session that carries an impersonation
// record, live or ended, is left out: someone else established it acting as the user.
// A user with none has an empty list, never nil.
func (s *SessionStorageDriver) OwnSessions(ctx context.Context, userID impulse.UUID, activeSince time.Time) ([]*dbtype.OwnSession, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if !s.users {
		return nil, errors.New("OwnSessions needs the auth schema: use Auth storage")
	}

	notImpersonated := "TRUE"
	if s.impersonation != nil {
		notImpersonated = fmt.Sprintf("NOT EXISTS (SELECT 1 FROM %s r WHERE r.SessionId = s.Id)", s.impersonation.TableName)
	}
	stmt := spanner.NewStatement(fmt.Sprintf(`
		SELECT s.Id, s.Browser, s.CreatedAt, s.UpdatedAt
		FROM %s s
		WHERE s.UserId = @userId AND s.Expired = FALSE AND s.UpdatedAt >= @activeSince AND %s
		ORDER BY s.UpdatedAt DESC, s.Id`, s.sessionTableName, notImpersonated))
	stmt.Params["userId"] = userID.String()
	stmt.Params["activeSince"] = activeSince

	sessions := []*dbtype.OwnSession{}
	err := s.spanner.Single().Query(ctx, stmt).Do(func(row *spanner.Row) error {
		var r ownSessionRow
		if err := row.ToStruct(&r); err != nil {
			return errors.Wrap(err, "spanner.Row.ToStruct()")
		}
		own := &dbtype.OwnSession{ID: r.ID, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
		if r.Browser.Valid {
			own.Browser = &r.Browser.StringVal
		}
		sessions = append(sessions, own)

		return nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "spanner.RowIterator.Do()")
	}

	return sessions, nil
}

// TrustedDeviceCount returns how many of the user's trusted devices are live at now:
// not revoked and not expired.
func (s *SessionStorageDriver) TrustedDeviceCount(ctx context.Context, userID impulse.UUID, now time.Time) (int, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	stmt := spanner.NewStatement(fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE UserId = @userId AND RevokedAt IS NULL AND ExpiresAt > @now`, s.trustedDevicesTableName))
	stmt.Params["userId"] = userID.String()
	stmt.Params["now"] = now

	var count int64
	if err := s.spanner.Single().Query(ctx, stmt).Do(func(row *spanner.Row) error {
		return row.Column(0, &count)
	}); err != nil {
		return 0, errors.Wrap(err, "spanner.RowIterator.Do()")
	}

	return int(count), nil
}
