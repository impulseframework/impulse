// Package scan allows scanning data into Go structs and other composite types, when working with spanner library native interface.
package spxscan

import (
	"context"
	"iter"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/spxscan/spxapi"
)

// Get is a package-level helper function that uses the spxapi.Default object.
// See API.Get for details.
func Get(ctx context.Context, db spxapi.Querier, dst any, statement spanner.Statement) error {
	if err := spxapi.Default.Get(ctx, db, dst, statement); err != nil {
		return errors.Wrap(err, "spxapi.API.Get()")
	}

	return nil
}

// Select is a package-level helper function that uses the spxapi.Default object.
// See API.Select for details.
func Select(ctx context.Context, db spxapi.Querier, dst any, statement spanner.Statement) error {
	if err := spxapi.Default.Select(ctx, db, dst, statement); err != nil {
		return errors.Wrap(err, "spxapi.API.Select()")
	}

	return nil
}

// SelectSeq is a package-level helper function that uses the spxapi.Default object.
// See API.Select for details.
func SelectSeq[T any](ctx context.Context, db spxapi.Querier, statement spanner.Statement) iter.Seq2[*T, error] {
	return spxapi.SelectSeq[T](ctx, spxapi.Default, db, statement)
}

// UpdateAndGet is a package-level helper function that uses the spxapi.Default object.
// See API.UpdateAndGet for details.
func UpdateAndGet(ctx context.Context, client spxapi.TxnRunner, dst any, statement spanner.Statement) error {
	if err := spxapi.Default.UpdateAndGet(ctx, client, dst, statement); err != nil {
		return errors.Wrap(err, "spxapi.API.UpdateAndGet()")
	}

	return nil
}

// UpdateAndSelect is a package-level helper function that uses the spxapi.Default object.
// See API.UpdateAndSelect for details.
func UpdateAndSelect(ctx context.Context, client spxapi.TxnRunner, dst any, statement spanner.Statement) error {
	if err := spxapi.Default.UpdateAndSelect(ctx, client, dst, statement); err != nil {
		return errors.Wrap(err, "spxapi.API.UpdateAndSelect()")
	}

	return nil
}
