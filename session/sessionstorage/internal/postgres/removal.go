package postgres

import (
	"context"
	"fmt"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/logger"
)

// dueRemovalLimit is the most rows one write removes: no write pays for a backlog of
// rows that came due together, which the writes after it take in turn.
const dueRemovalLimit = 100

// idKey is the key column of every table with a removal but the sign-in attempts table.
const idKey = `"Id"`

// The order and the two conditions of the removals that count from a row's expiry, the
// period in hours.
const (
	expiresAtOrder   = `"ExpiresAt"`
	expired30DaysAgo = `"ExpiresAt" < now() - interval '720 hours'`
	expiredADayAgo   = `"ExpiresAt" < now() - interval '24 hours'`
)

// removal is the housekeeping a write carries for the rows of its table that are over.
// On Spanner the table's row deletion policy removes these rows; PostgreSQL has none, so
// the write runs this statement after its own work (a sign-in inside its transaction, in
// a savepoint) and keeps the statement's failure out of its own answer: a removal that
// fails is logged, and the write stands.
type removal struct {
	// what names the rows in the logged line.
	what string
	// key is the table's key column.
	key string
	// due is the condition a row that is over meets; $1, where it appears, is the cutoff
	// the caller passes.
	due string
	// order is the column the rows are taken by, the oldest first, through its index.
	order string
}

// The removals. Every period but the sessions' is counted in hours, as Spanner's are:
// PostgreSQL counts a day by the session's time zone, and one day a year is 23 hours
// long. The sessions are counted by the UTC day, which is always 24 hours; their
// condition in hours is a guard that takes no row out of what the day finds.
var (
	// sessionsRemoval finds the sessions that are due by the UTC day of their last
	// activity (UpdatedOn, which the database works out from UpdatedAt) and holds each to
	// UpdatedAt itself. Every session in the index's range is due, so the write that
	// carries the removal reads the rows it removes and no others, and UpdatedOn changes
	// once a day for a session in use, where an index on UpdatedAt would be written by
	// every activity update. A session goes 30 to 31 days after its last activity, never
	// sooner.
	sessionsRemoval = removal{
		what:  "sessions idle more than 30 days",
		key:   idKey,
		due:   `"UpdatedOn" < (now() AT TIME ZONE 'UTC')::date - 30 AND "UpdatedAt" < now() - interval '720 hours'`,
		order: `"UpdatedOn"`,
	}
	// attemptsRemoval takes the sign-in attempts rows that stopped counting before $1, a
	// day before the recorded failure's time.
	attemptsRemoval = removal{
		what:  "sign-in attempts rows that are over",
		key:   `"Name"`,
		due:   `"EndsAt" < $1`,
		order: `"EndsAt"`,
	}
	// mailLinksRemoval takes the links that expired more than 30 days ago, used or not.
	mailLinksRemoval = removal{
		what:  "mail links that expired more than 30 days ago",
		key:   idKey,
		due:   `"ExpiresAt" < now() - interval '720 hours'`,
		order: `"ExpiresAt"`,
	}
	// tokensRemoval takes the tokens that expired more than 30 days ago, revoked or not.
	// A refresh token that was rotated is among them only then, so one presented again
	// before it expires is still there to be caught as a reuse.
	tokensRemoval = removal{
		what:  "tokens that expired more than 30 days ago",
		key:   idKey,
		due:   expired30DaysAgo,
		order: expiresAtOrder,
	}
	// trustedDevicesRemoval takes the trusted devices that expired more than 30 days ago,
	// revoked or not.
	trustedDevicesRemoval = removal{
		what:  "trusted devices that expired more than 30 days ago",
		key:   idKey,
		due:   expired30DaysAgo,
		order: expiresAtOrder,
	}
	// deviceCodesRemoval takes the device codes that expired more than a day ago, whether
	// they were approved, denied or never decided.
	deviceCodesRemoval = removal{
		what:  "device codes that expired more than a day ago",
		key:   idKey,
		due:   expiredADayAgo,
		order: expiresAtOrder,
	}
	// pendingSignInsRemoval takes the pending sign-ins that expired more than a day ago,
	// consumed or not. Their sign-in codes go with them by the foreign key's cascade.
	pendingSignInsRemoval = removal{
		what:  "pending sign-ins that expired more than a day ago",
		key:   idKey,
		due:   expiredADayAgo,
		order: expiresAtOrder,
	}
)

// statement is the removal's statement over table: the rows that are due, at most
// dueRemovalLimit of them and the oldest first. A row of the table another transaction
// holds is skipped, never waited on; the next write takes the row.
func (r removal) statement(table string) string {
	return fmt.Sprintf(
		`DELETE FROM %[1]s WHERE %[2]s IN (SELECT %[2]s FROM %[1]s WHERE %[3]s ORDER BY %[4]s LIMIT %[5]d FOR UPDATE SKIP LOCKED)`,
		table, r.key, r.due, r.order, dueRemovalLimit)
}

// remove runs the removal over table on db and says whether it succeeded. A removal that
// fails is logged as an error, never returned: it is housekeeping for rows the write has
// nothing to do with, and the write that carried it goes on to its own answer. One that
// failed because the request was canceled is not logged: the person went away, nothing
// is wrong with the table, and the next write removes the rows. One a deadline cut short
// is logged: the removal may be what used the request's time up.
func (r removal) remove(ctx context.Context, db execer, table string, args ...any) bool {
	if _, err := db.Exec(ctx, r.statement(table), args...); err != nil {
		if !errors.Is(ctx.Err(), context.Canceled) {
			logger.FromCtx(ctx).Errorf(
				`removing %s from %s failed, and the write that carried it went on: %v (see "Rows that are over are removed" in the session README)`,
				r.what, table, err)
		}

		return false
	}

	return true
}
