package spxapi

import (
	"context"
	"errors"
	"fmt"
	"iter"

	"cloud.google.com/go/spanner"
	"google.golang.org/api/iterator"
)

// Select is a high-level function that queries rows from Querier and calls the ScanAll function.
// See ScanAll for details.
func SelectSeq[T any](ctx context.Context, api *API, db Querier, statement spanner.Statement) iter.Seq2[*T, error] {
	rowIter := db.Query(ctx, statement)

	return ScanSeq[T](api, rowIter)
}

// ScanSeq returns a interator that iterates all rows to the end. After iterating it closes the
// interator, and propagates any errors that could pop up.
func ScanSeq[T any](api *API, rowIter *spanner.RowIterator) iter.Seq2[*T, error] {
	return func(yield func(*T, error) bool) {
		defer rowIter.Stop()

		for {
			row, err := rowIter.Next()
			if errors.Is(err, iterator.Done) {
				break
			}
			if err != nil {
				yield(nil, fmt.Errorf("spanner.RowIterator.Next(): %w", err))

				return
			}

			val := new(T)
			if api.lenient {
				if err := row.ToStructLenient(val); err != nil {
					yield(nil, fmt.Errorf("spanner.Row.ToStruct(): %w", err))

					return
				}
			} else {
				if err := row.ToStruct(val); err != nil {
					yield(nil, fmt.Errorf("spanner.Row.ToStruct(): %w", err))

					return
				}
			}

			if !yield(val, nil) {
				return
			}
		}
	}
}
