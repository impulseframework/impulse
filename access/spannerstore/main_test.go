package spannerstore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/db"
)

var container *db.SpannerContainer

// TestMain is a wrapper for the test suite. It creates a Cloud Spanner
// emulator container and runs the test suite.
func TestMain(m *testing.M) {
	os.Exit(testMain(m))
}

func testMain(m *testing.M) int {
	ctx := context.Background()
	c, err := db.NewSpannerContainer(ctx, "latest")
	if err != nil {
		fmt.Println(err)

		return 2
	}
	container = c
	defer func() {
		if err := c.Close(); err != nil {
			fmt.Println(err)
		}
	}()

	return m.Run()
}

// prepareStore creates a database for the test, applies the store's DDL
// through the same migration tooling apps use, and returns the ready store —
// so the shipped DDL is exactly what the suite runs against.
//
// NOTE: db-initiator's migrations require the jtwatson/migrate fork (see the
// replace directive in go.mod): upstream golang-migrate closes the spanner
// clients handed to it, which are db-initiator's shared container clients.
func prepareStore(ctx context.Context, t *testing.T, opts ...Option) (*Store, *db.SpannerDB) {
	t.Helper()

	database, err := container.CreateDatabase(ctx, strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-")))
	if err != nil {
		t.Fatalf("db.SpannerContainer.CreateDatabase(): %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Logf("db.SpannerDB.Close(): %v", err)
		}
	})

	store, err := New(database.Client, opts...)
	if err != nil {
		t.Fatalf("spannerstore.New(): %v", err)
	}
	if err := applyDDL(t, database, store); err != nil {
		t.Fatalf("applying DDL: %v", err)
	}

	return store, database
}

func applyDDL(t *testing.T, database *db.SpannerDB, store *Store) error {
	t.Helper()

	dir := t.TempDir()
	migration := strings.Join(store.DDL(), ";\n") + ";\n"
	if err := os.WriteFile(filepath.Join(dir, "000001_access.up.sql"), []byte(migration), 0o600); err != nil {
		return errors.Wrap(err, "os.WriteFile()")
	}

	if err := database.MigrateUp("file://" + dir); err != nil {
		return errors.Wrap(err, "db.SpannerDB.MigrateUp()")
	}

	return nil
}
