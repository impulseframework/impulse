// Package integration drives access against a real PostgreSQL container:
// cross-client policy propagation over the typed policy tables, through the
// change signal and through the heartbeat alone.
package integration

import (
	"context"
	"fmt"
	"os"
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

// prepareDatabase creates a new empty database for the test; each test
// applies its store's DDL itself.
func prepareDatabase(ctx context.Context, t *testing.T) (*db.PostgresDatabase, error) {
	t.Helper()

	database, err := container.CreateDatabase(ctx, t.Name())
	if err != nil {
		return nil, errors.Wrapf(err, "db.PostgresContainer.CreateDatabase()")
	}

	t.Cleanup(database.Close)

	return database, nil
}
