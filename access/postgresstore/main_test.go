package postgresstore

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access/internal/storetest"
	"github.com/impulseframework/impulse/db"
)

var container *db.PostgresContainer

// TestMain is a wrapper for the test suite. It creates a new PostgresContainer and runs the test suite.
func TestMain(m *testing.M) {
	ctx := context.Background()
	c, err := db.NewPostgresContainer(ctx, "latest")
	if err != nil {
		fmt.Println(err)
		os.Exit(2)
	}
	container = c

	exitCode := m.Run()

	c.Close()
	if err := c.Terminate(ctx); err != nil {
		fmt.Println(err)
	}

	os.Exit(exitCode)
}

// prepareStore creates a database for the test, applies the store's DDL, and
// returns the ready store — so the shipped DDL is exactly what the suite runs
// against.
func prepareStore(ctx context.Context, t *testing.T, opts ...Option) (*Store, *db.PostgresDatabase) {
	t.Helper()

	database, err := container.CreateDatabase(ctx, strings.ReplaceAll(t.Name(), "/", "_"))
	if err != nil {
		t.Fatalf("db.PostgresContainer.CreateDatabase(): %v", err)
	}
	t.Cleanup(database.Close)

	store, err := New(database.Pool, opts...)
	if err != nil {
		t.Fatalf("postgresstore.New(): %v", err)
	}
	if err := applyDDL(ctx, database, store); err != nil {
		t.Fatalf("applying DDL: %v", err)
	}

	return store, database
}

func applyDDL(ctx context.Context, database *db.PostgresDatabase, store *Store) error {
	for _, stmt := range store.DDL() {
		if _, err := database.Exec(ctx, stmt); err != nil {
			return errors.Wrapf(err, "executing DDL statement %q", stmt)
		}
	}

	return nil
}

// openTransaction is the open transaction of another request the engine's suite makes
// its checks inside: a transaction on the store's pool that has read the roles table
// and changed a row of it, so it holds a lock on a row the engine's reload reads, with
// f run under the context the transaction runs under. It is rolled back.
func openTransaction(database *db.PostgresDatabase) storetest.Transaction {
	return func(t *testing.T, f func(ctx context.Context)) {
		t.Helper()
		ctx := t.Context()

		tx, err := database.Begin(ctx)
		if err != nil {
			t.Fatalf("pgxpool.Pool.Begin() error = %v", err)
		}
		defer func() {
			if err := tx.Rollback(ctx); err != nil {
				t.Errorf("pgx.Tx.Rollback() error = %v", err)
			}
		}()
		var roles int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM "AccessRoles"`).Scan(&roles); err != nil {
			t.Fatalf("pgx.Tx.QueryRow() error = %v", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE "AccessRoles" SET "Description" = 'Held by an open transaction.' WHERE "Role" = 'Editor'`); err != nil {
			t.Fatalf("pgx.Tx.Exec() error = %v", err)
		}

		f(ctx)
	}
}
