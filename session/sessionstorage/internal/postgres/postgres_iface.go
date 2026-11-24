package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Queryer is the connection the driver is made with: a pool, a connection, or a type of
// the application's own that wraps one. BeginTx begins a transaction with the options it
// is given: the driver begins every transaction of its own through it at the read
// committed isolation level (SessionStorageDriver.begin), so a wrapper passes the options
// on.
type Queryer interface {
	BeginTx(ctx context.Context, txOptions pgx.TxOptions) (pgx.Tx, error)
	Query(ctx context.Context, query string, args ...interface{}) (pgx.Rows, error)
	QueryRow(ctx context.Context, query string, args ...interface{}) pgx.Row
	Exec(ctx context.Context, query string, args ...interface{}) (pgconn.CommandTag, error)
}
