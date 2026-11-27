package resource

import (
	"context"
	"fmt"
	"iter"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/spxscan/spxapi"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var _ Client = (*PostgresClient)(nil)

// PostgresClient is a wrapper around the database.
type PostgresClient struct {
	postgres *pgxpool.Pool
	stores   *fileStores
	// secrets are the application's secret keys, nil when the client was built with
	// none.
	secrets *SecretKeys
}

// NewPostgresClient creates a new Client. WithFileStore and WithNamedFileStore hand it
// the stores the generated handlers read off it, the options the Spanner client takes; a
// store wired twice, or two stores on one location, panics here. WithSecretKeys hands it
// the keys a patch's secret fields are sealed with.
func NewPostgresClient(db *pgxpool.Pool, opts ...ClientOption) *PostgresClient {
	stores, secrets := applyClientOptions(opts)

	return &PostgresClient{
		postgres: db,
		stores:   stores,
		secrets:  secrets,
	}
}

// secretKeys returns the application's secret keys, nil when the client holds none.
func (c *PostgresClient) secretKeys() *SecretKeys {
	return c.secrets
}

// DBType returns the database type.
func (c *PostgresClient) DBType() DBType {
	return PostgresDBType
}

// FileStore returns the store wired under name, nil when none is.
func (c *PostgresClient) FileStore(name StoreName) FileStore {
	return c.stores.get(name)
}

// Close closes the database connection.
func (c *PostgresClient) Close() {
	c.postgres.Close()
}

// PostgresReadOnlyTransaction panics because it is not implemented for the PostgresClient.
func (c *PostgresClient) PostgresReadOnlyTransaction() any {
	panic("PostgresReadOnlyTransaction() not implemented for PostgresClient")
}

// ExecuteFunc executes a function within a read-write transaction.
func (c *PostgresClient) ExecuteFunc(_ context.Context, _ func(ctx context.Context, _ ReadWriteTransaction) error) error {
	_ = newPostgresPostgresReadWriteTransaction(nil).WithSecretKeys(c.secrets)
	panic("ExecuteFunc() not implemented for PostgresClient")
}

// ExecuteDeleteFunc panics, as ExecuteFunc does: PostgreSQL has no write path.
func (c *PostgresClient) ExecuteDeleteFunc(_ context.Context, _ bool, _ func(ctx context.Context, _ ReadWriteTransaction) error) error {
	panic("ExecuteDeleteFunc() not implemented for PostgresClient")
}

// ReadOnlyTransaction returns a ReadOnlyTransaction that can be used for multiple reads from the database.
// You must call Close() when the ReadOnlyTransaction is no longer needed to release resources on the server.
func (c *PostgresClient) ReadOnlyTransaction() ReadOnlyTransactionCloser {
	return newPostgresReadOnlyTransaction()
}

// SpannerReadOnlyTransaction panics because it is not implemented for the PostgresClient.
func (c *PostgresClient) SpannerReadOnlyTransaction() spxapi.Querier {
	panic("PostgresClient.SpannerReadOnlyTransaction() should never be called.")
}

var _ Reader[nilResource] = (*postgresReader[nilResource])(nil)

// postgresReader is a reader implementation for Postgres.
type postgresReader[Resource Resourcer] struct {
	readTxn func() any
}

// DBType returns the database type.
func (c *postgresReader[Resource]) DBType() DBType {
	return PostgresDBType
}

// Read reads a single resource from the database.
func (c *postgresReader[Resource]) Read(_ context.Context, _ *Statement) (*Row[Resource], error) {
	panic("Read() not implemented for PostgresReader[Resource]")
}

// List reads a list of resources from the database.
func (c *postgresReader[Resource]) List(_ context.Context, _ *Statement) iter.Seq2[*Row[Resource], error] {
	panic("List() not implemented for PostgresReader[Resource]")
}

// Count runs a COUNT(*) statement and returns its one value.
func (c *postgresReader[Resource]) Count(_ context.Context, _ *Statement) (int64, error) {
	panic("Count() not implemented for PostgresReader[Resource]")
}

var _ ReadOnlyTransactionCloser = (*PostgresReadOnlyTransaction)(nil)

// PostgresReadOnlyTransaction represents a database transaction that can only be used for reads.
type PostgresReadOnlyTransaction struct {
	resourceRowIndex map[string]int
}

// newPostgresReadOnlyTransaction creates a new PostgresReadOnlyTransaction
func newPostgresReadOnlyTransaction() ReadOnlyTransactionCloser {
	panic("newPostgresReadOnlyTransaction() not implemented for PostgresReader[Resource]")
}

// Close closes the readonly transaction
func (c *PostgresReadOnlyTransaction) Close() {
	panic("Close() not implemented for PostgresReader[Resource]")
}

// PostgresReadOnlyTransaction returns a read-only transaction for the Postgres client.
func (c *PostgresReadOnlyTransaction) PostgresReadOnlyTransaction() any {
	panic("PostgresReadOnlyTransaction() not implemented for PostgresReader[Resource]")
}

// SpannerReadOnlyTransaction panics because it is not implemented for the PostgresReadOnlyTransaction.
func (c *PostgresReadOnlyTransaction) SpannerReadOnlyTransaction() spxapi.Querier {
	panic("PostgresReadOnlyTransaction.SpannerReadOnlyTransaction() should never be called.")
}

var _ ReadWriteTransaction = (*PostgresReadWriteTransaction)(nil)

// PostgresReadWriteTransaction represents a database transaction that can be used for both reads and writes.
type PostgresReadWriteTransaction struct {
	postgres         *pgxpool.Pool
	resourceRowIndex map[string]int
	// secrets are the secret keys of the client the transaction runs on, nil when it
	// holds none.
	secrets *SecretKeys
}

func newPostgresPostgresReadWriteTransaction(txn *pgxpool.Pool) *PostgresReadWriteTransaction {
	return &PostgresReadWriteTransaction{
		postgres:         txn,
		resourceRowIndex: make(map[string]int),
		secrets:          nil,
	}
}

// WithSecretKeys gives the transaction the application's secret keys, which a patch's
// secret fields are sealed with, and returns the transaction.
func (c *PostgresReadWriteTransaction) WithSecretKeys(keys *SecretKeys) *PostgresReadWriteTransaction {
	c.secrets = keys

	return c
}

// secretKeys returns the secret keys the transaction seals with, nil when it holds none.
func (c *PostgresReadWriteTransaction) secretKeys() *SecretKeys {
	return c.secrets
}

// Read reads a single resource from the database.
func (c *PostgresReadWriteTransaction) Read(_ context.Context, _ Resourcer, _ any, _ *Statement) error {
	panic("Read() not implemented for PostgresReadWriteTransaction")
}

// List reads a list of resources from the database.
func (c *PostgresReadWriteTransaction) List(_ context.Context, _ Resourcer, _ []any, _ *Statement) error {
	panic("you should never call List() on a PostgresReadWriteTransaction")
}

// DBType returns the database type.
func (c *PostgresReadWriteTransaction) DBType() DBType {
	return PostgresDBType
}

// DataChangeEventIndex provides a sequence number for data change events on the same Resource inside the same transaction.
func (c *PostgresReadWriteTransaction) DataChangeEventIndex(res accesstypes.Resource, rowID string) int {
	indexID := fmt.Sprintf("%s_%s", res, rowID)
	c.resourceRowIndex[indexID]++

	return c.resourceRowIndex[indexID]
}

// postgresQuerier is what a read inside a PostgreSQL transaction runs a statement on:
// a pool, a connection or a pgx transaction.
type postgresQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// PostgresReadOnlyTransaction returns what a read inside the transaction runs a
// statement on, and nil for a transaction that holds no connection: a caller that
// cannot run its statement on the answer refuses, it does not guess.
func (c *PostgresReadWriteTransaction) PostgresReadOnlyTransaction() any {
	if c.postgres == nil {
		return nil
	}

	return c.postgres
}

// BufferMap panics because it is not implemented for the PostgresReadWriteTransaction.
func (c *PostgresReadWriteTransaction) BufferMap(_ PatchSetMetadata, _ map[string]any) error {
	panic("BufferMap() not implemented for PostgresReadWriteTransaction")
}

// BufferStruct panics because it is not implemented for the PostgresReadWriteTransaction.
func (c *PostgresReadWriteTransaction) BufferStruct(_ PatchSetMetadata) error {
	panic("BufferStruct() not implemented for PostgresReadWriteTransaction")
}

// SpannerReadOnlyTransaction panics because it is not implemented for the PostgresReadWriteTransaction.
func (c *PostgresReadWriteTransaction) SpannerReadOnlyTransaction() spxapi.Querier {
	panic("PostgresReadWriteTransaction.SpannerReadOnlyTransaction() should never be called.")
}
