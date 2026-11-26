package resource

import (
	"context"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"google.golang.org/api/iterator"
)

// TestGeneratedCollection_RecordCondition_onSpanner holds the conditions a role held on a
// record counts under to the fixture's rows on the Spanner emulator (askOnRecord): the
// rows of each resource a holder reaches, and a task being created; the updates
// (askOnRecordUpdates), which the resource package writes on Spanner alone; and a second
// site's rows (askOnRecordSecondSite).
func TestGeneratedCollection_RecordCondition_onSpanner(t *testing.T) {
	t.Parallel()

	container := spannerEmulator(t)
	ctx := context.Background()
	db, err := container.CreateDatabase(ctx, "record-roles")
	if err != nil {
		t.Fatalf("initiator.SpannerContainer.CreateDatabase() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.DropDatabase(context.Background()); err != nil {
			t.Error(err)
		}
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := db.MigrateUp("file://testdata/recordroles/schema"); err != nil {
		t.Fatalf("initiator.SpannerDB.MigrateUp() error = %v", err)
	}
	rows := onRecordRows()
	mutations := make([]*spanner.Mutation, 0, len(rows))
	for _, row := range rows {
		mutations = append(mutations, spanner.InsertMap(row.table, row.values))
	}
	if _, err := db.Apply(ctx, mutations); err != nil {
		t.Fatalf("spanner.Client.Apply() error = %v", err)
	}

	query := func(t *testing.T, sql string, params map[string]any, each func(row *spanner.Row) error) {
		t.Helper()

		iter := db.Single().Query(ctx, spanner.Statement{SQL: sql, Params: params})
		defer iter.Stop()
		for {
			row, err := iter.Next()
			if errors.Is(err, iterator.Done) {
				return
			}
			if err != nil {
				t.Fatalf("running %s: %v", sql, err)
			}
			if err := each(row); err != nil {
				t.Fatalf("reading a row of %s: %v", sql, err)
			}
		}
	}
	spannerDB := onRecordDatabase{
		dialect: Spanner,
		keys: func(t *testing.T, sql string, params map[string]any) []string {
			t.Helper()

			var keys []string
			query(t, sql, params, func(row *spanner.Row) error {
				var key string
				if err := row.Column(0, &key); err != nil {
					return errors.Wrap(err, "spanner.Row.Column()")
				}
				keys = append(keys, key)

				return nil
			})

			return keys
		},
		holds: func(t *testing.T, sql string, params map[string]any) bool {
			t.Helper()

			var holds spanner.NullBool
			query(t, sql, params, func(row *spanner.Row) error {
				return row.Column(0, &holds)
			})

			return holds.Valid && holds.Bool
		},
	}
	askOnRecord(t, spannerDB)
	askOnRecordUpdates(t, spannerDB)
	askOnRecordSecondSite(t, spannerDB)
}
