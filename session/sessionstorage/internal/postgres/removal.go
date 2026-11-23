package postgres

import (
	"context"
	"fmt"

	"github.com/impulseframework/impulse/logger"
)

// dueRemovalLimit is the most rows one write removes: no write pays for a backlog of
// rows that came due together, which the writes after it take in turn.
const dueRemovalLimit = 100

// removal is the housekeeping a write carries for the rows of its table that are over.
// On Spanner the table's row deletion policy removes these rows; PostgreSQL has none, so
// the write runs this statement after its own work and outside its own answer: a removal
// that fails is logged, and the write stands.
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

// The three removals. The periods are counted in hours, as Spanner's are: PostgreSQL
// counts a day by the session's time zone, and one day a year is 23 hours long.
var (
	// sessionsRemoval finds the sessions that are due by when they were made, which no
	// request moves, and then holds them to their last activity: a session idle 30 days
	// was made at least 30 days ago, and an index on UpdatedAt would be written by every
	// session's activity.
	sessionsRemoval = removal{
		what:  "sessions idle more than 30 days",
		key:   `"Id"`,
		due:   `"CreatedAt" < now() - interval '720 hours' AND "UpdatedAt" < now() - interval '720 hours'`,
		order: `"CreatedAt"`,
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
		key:   `"Id"`,
		due:   `"ExpiresAt" < now() - interval '720 hours'`,
		order: `"ExpiresAt"`,
	}
)

// statement is the removal's statement over table: the rows that are due, at most
// dueRemovalLimit of them and the oldest first. A row another transaction holds is
// skipped, never waited on, so the removal waits on nobody; the next write takes the row.
func (r removal) statement(table string) string {
	return fmt.Sprintf(
		`DELETE FROM %[1]s WHERE %[2]s IN (SELECT %[2]s FROM %[1]s WHERE %[3]s ORDER BY %[4]s LIMIT %[5]d FOR UPDATE SKIP LOCKED)`,
		table, r.key, r.due, r.order, dueRemovalLimit)
}

// remove runs the removal over table on db and says whether it succeeded. A removal that
// fails is logged as an error, never returned: it is housekeeping for rows the write has
// nothing to do with, and the write that carried it goes on to its own answer.
func (r removal) remove(ctx context.Context, db execer, table string, args ...any) bool {
	if _, err := db.Exec(ctx, r.statement(table), args...); err != nil {
		logger.FromCtx(ctx).Errorf(
			`removing %s from %s failed, and the write that carried it went on: %v (see "Rows that are over are removed" in the session README)`,
			r.what, table, err)

		return false
	}

	return true
}
