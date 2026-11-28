// Package spannertxn opens the read-write transactions the library runs on Spanner, each
// at an isolation level it names.
//
// The library's stores and its resource client keep their rules by reading before they
// write: a sign-in reads the user's row and inserts a session, the removal of a means of
// sign-in reads the others the person holds, a role's insert reads the names it would be
// taken for, the delete of a record counts the rows under it. Each rule holds only while
// Spanner serializes the read with what another transaction writes at the same moment,
// which is the serializable isolation level. At repeatable read only two writes of one row
// conflict, so two such transactions that read the same rows and write different ones both
// commit.
//
// One kind of transaction is opened at repeatable read instead (RepeatableRead): a save of
// a delete that takes the rows under a record. What such a save reads is a record nobody
// uses any more, so it gains nothing from having its reads serialized with other writers,
// and at the serializable level every row it reads stays locked against them until it
// commits. At repeatable read its reads lock nothing. Spanner still aborts it where
// another transaction has written a row it writes, and a foreign key still refuses a
// commit that would leave a row naming a row it deleted.
//
// The Spanner client runs a read-write transaction that names no level at the level the
// application set for the client (spanner.ClientConfig.TransactionOptions). So the library
// opens none that way: every read-write transaction of the library is opened by
// Serializable or by RepeatableRead, and this package's test refuses a file of the module
// that opens one itself.
package spannertxn

import (
	"context"
	"time"

	"cloud.google.com/go/spanner"
	sppb "cloud.google.com/go/spanner/apiv1/spannerpb"
)

// Serializable runs f in a read-write transaction of client at the serializable isolation
// level, whatever level the application set for the client, and returns the commit time.
// It is spanner.Client.ReadWriteTransaction with the level named: the client runs f again
// when Spanner aborts the transaction, and the error is the client's own, not wrapped, so
// a caller reads its code and wraps it with the words that name its transaction.
func Serializable(ctx context.Context, client *spanner.Client, f func(context.Context, *spanner.ReadWriteTransaction) error) (time.Time, error) {
	var options spanner.TransactionOptions
	options.IsolationLevel = sppb.TransactionOptions_SERIALIZABLE
	resp, err := client.ReadWriteTransactionWithOptions(ctx, f, options)

	return resp.CommitTs, err //nolint:wrapcheck // each caller wraps it with the words that name its transaction
}

// RepeatableRead runs f in a read-write transaction of client at the repeatable read
// isolation level, whatever level the application set for the client, and returns the
// commit time. Its reads see the database as it stood at one moment and lock nothing;
// Spanner aborts it at the commit where another transaction wrote a row it writes, and the
// client then runs f again. It is for a save of a delete that takes the rows under a
// record, and nothing else opens one. The error is the client's own, not wrapped, as
// Serializable's is.
func RepeatableRead(ctx context.Context, client *spanner.Client, f func(context.Context, *spanner.ReadWriteTransaction) error) (time.Time, error) {
	var options spanner.TransactionOptions
	options.IsolationLevel = sppb.TransactionOptions_REPEATABLE_READ
	resp, err := client.ReadWriteTransactionWithOptions(ctx, f, options)

	return resp.CommitTs, err //nolint:wrapcheck // each caller wraps it with the words that name its transaction, as Serializable's do
}
