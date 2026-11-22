package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/georgysavva/scany/v2/pgxscan"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/tracer"
	"github.com/jackc/pgx/v5"
)

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
		notImpersonated = fmt.Sprintf(`NOT EXISTS (SELECT 1 FROM %s r WHERE r."SessionId" = s."Id")`, pgx.Identifier{s.impersonation.TableName}.Sanitize())
	}
	query := fmt.Sprintf(`
		SELECT s."Id", s."Browser", s."CreatedAt", s."UpdatedAt"
		FROM %s s
		WHERE s."UserId" = $1 AND s."Expired" = FALSE AND s."UpdatedAt" >= $2 AND %s
		ORDER BY s."UpdatedAt" DESC, s."Id"`, s.sessionTable(), notImpersonated)

	var sessions []*dbtype.OwnSession
	if err := pgxscan.Select(ctx, s.conn, &sessions, query, userID, activeSince); err != nil {
		return nil, errors.Wrap(err, "pgxscan.Select()")
	}
	if sessions == nil {
		sessions = []*dbtype.OwnSession{}
	}

	return sessions, nil
}

// DestroyOtherOwnSessions ends, in one statement, every session OwnSessions lists for
// userID with no cut, except keep: each takes Expired and UpdatedAt as DestroySession
// writes them. It returns how many of those were used at activeSince or later before the
// write, the ones the person's list showed. No impersonation record is touched: an own
// session has none.
func (s *SessionStorageDriver) DestroyOtherOwnSessions(ctx context.Context, userID, keep impulse.UUID, activeSince time.Time) (int, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if !s.users {
		return 0, errors.New("DestroyOtherOwnSessions needs the auth schema: use Auth storage")
	}

	notImpersonated := "TRUE"
	if s.impersonation != nil {
		notImpersonated = fmt.Sprintf(`NOT EXISTS (SELECT 1 FROM %s r WHERE r."SessionId" = s."Id")`, pgx.Identifier{s.impersonation.TableName}.Sanitize())
	}
	query := fmt.Sprintf(`
		WITH others AS (
			SELECT s."Id", s."UpdatedAt" >= $3 AS live
			FROM %[1]s s
			WHERE s."UserId" = $1 AND s."Expired" = FALSE AND s."Id" <> $2 AND %[2]s
			FOR UPDATE OF s
		)
		UPDATE %[1]s e SET "Expired" = TRUE, "UpdatedAt" = now()
		FROM others
		WHERE e."Id" = others."Id"
		RETURNING others.live`, s.sessionTable(), notImpersonated)

	rows, err := s.conn.Query(ctx, query, userID, keep, activeSince)
	if err != nil {
		return 0, errors.Wrap(err, "Queryer.Query()")
	}
	lives, err := pgx.CollectRows(rows, pgx.RowTo[bool])
	if err != nil {
		return 0, errors.Wrap(err, "pgx.CollectRows()")
	}
	count := 0
	for _, live := range lives {
		if live {
			count++
		}
	}

	return count, nil
}

// TrustedDeviceCount returns how many of the user's trusted devices are live at now:
// not revoked and not expired.
func (s *SessionStorageDriver) TrustedDeviceCount(ctx context.Context, userID impulse.UUID, now time.Time) (int, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	query := fmt.Sprintf(`SELECT count(*) FROM %s WHERE "UserId" = $1 AND "RevokedAt" IS NULL AND "ExpiresAt" > $2`, s.trustedDevicesTable())
	var count int
	if err := s.conn.QueryRow(ctx, query, userID, now).Scan(&count); err != nil {
		return 0, errors.Wrap(err, "pgx.Row.Scan()")
	}

	return count, nil
}
