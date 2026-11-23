package postgres

import (
	"strings"
	"testing"

	"github.com/impulseframework/impulse/session/sessionstorage/internal/drivertest"
	"github.com/jackc/pgx/v5"
)

// TestAuth_UsernameLookupsUseTheUniqueIndex proves that a lookup by username can use the
// username's unique index, on both store shapes and for a name with an @ in it and one
// without: PostgreSQL uses an index only for a comparison under the index's own
// collation, and every lookup compares the column under the collation usernameFold
// gives it. Sequential scans are switched off for the plan, so the planner takes the
// index wherever it can, and a plan that still reads the whole table shows that it
// cannot.
func TestAuth_UsernameLookupsUseTheUniqueIndex(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name   string
		schema drivertest.AuthSchema
	}{
		{name: "a store with an address column", schema: drivertest.Auth},
		{name: "a store whose usernames are the addresses", schema: drivertest.AuthUsernameIsEmail},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()

			db, err := prepareDatabase(ctx, t, authSources(tt.schema)...)
			if err != nil {
				t.Fatalf("prepareDatabase() error = %v", err)
			}
			tx, err := db.Begin(ctx)
			if err != nil {
				t.Fatalf("Begin() error = %v", err)
			}
			// Rolled back on every path: the transaction only reads a plan.
			defer func() { _ = tx.Rollback(ctx) }()
			if _, err := tx.Exec(ctx, "SET LOCAL enable_seqscan = off"); err != nil {
				t.Fatalf("SET LOCAL enable_seqscan error = %v", err)
			}

			// A literal in place of the parameter: the comparison's collation is the same.
			for _, literal := range []string{"'kim@lakeside.edu'", "'kim'"} {
				rows, err := tx.Query(ctx, `EXPLAIN (COSTS OFF) SELECT "Id" FROM "SessionUsers" WHERE "NormalizedUsername" = `+usernameFold(literal))
				if err != nil {
					t.Fatalf("EXPLAIN error = %v", err)
				}
				lines, err := pgx.CollectRows(rows, pgx.RowTo[string])
				if err != nil {
					t.Fatalf("pgx.CollectRows() error = %v", err)
				}
				if plan := strings.Join(lines, "\n"); !strings.Contains(plan, "SessionUsers_NormalizedUsername_idx") {
					t.Errorf("a lookup by the username %s does not use the index SessionUsers_NormalizedUsername_idx; the plan:\n%s", literal, plan)
				}
			}
		})
	}
}
