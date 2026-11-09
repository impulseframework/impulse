package postgresstore

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/go-playground/errors/v5"
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
