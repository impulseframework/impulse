package spxapi

import (
	"context"
	"time"

	"cloud.google.com/go/spanner"
)

type Querier interface {
	Query(ctx context.Context, statement spanner.Statement) *spanner.RowIterator
}

type TxnRunner interface {
	ReadWriteTransaction(ctx context.Context, f func(context.Context, *spanner.ReadWriteTransaction) error) (commitTimestamp time.Time, err error)
}
