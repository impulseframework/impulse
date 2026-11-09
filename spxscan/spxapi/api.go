// package spxapi implements the core logic of the API type and exposes the functionality available in the package.
// With the API type, users can create a custom API instance and override default settings to configure spxscan.
package spxapi

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"cloud.google.com/go/spanner"
	"google.golang.org/api/iterator"
)

// ErrNotFound is returned by ScanOne if there were no rows.
var ErrNotFound = errors.New("spxscan: no row was found")

// NotFound returns true if err is a not found error.
// This error is returned by ScanOne if there were no rows.
func NotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}

// APIOption is a function type that changes API configuration.
type APIOption func(api *API)

// WithStructLenient uses Row.ToStructLenient() for scanning to destination.
func WithStructLenient(lenient bool) APIOption {
	return func(api *API) {
		api.lenient = lenient
	}
}

// Default is the default instance of API with all configuration settings set to default.
var Default = &API{lenient: false} //nolint:gochecknoglobals // Default is a package-level singleton providing default configuration.

// API is the core type in spxscan. It implements all the logic and exposes functionality available in the package.
// With API type users can create a custom API instance and override default settings hence configure spxscan.
type API struct {
	lenient bool
}

// NewAPI creates a new API object with provided list of options.
func NewAPI(opts ...APIOption) *API {
	api := &API{lenient: false}
	for _, o := range opts {
		o(api)
	}

	return api
}

// Get is a high-level function that queries rows from Querier and calls the ScanOne function.
// See ScanOne for details.
func (api *API) Get(ctx context.Context, db Querier, dst any, statement spanner.Statement) error {
	iter := db.Query(ctx, statement)
	if err := api.ScanOne(dst, iter); err != nil {
		return fmt.Errorf("scanning one: %w", err)
	}

	return nil
}

// Select is a high-level function that queries rows from Querier and calls the ScanAll function.
// See ScanAll for details.
func (api *API) Select(ctx context.Context, db Querier, dst any, statement spanner.Statement) error {
	iter := db.Query(ctx, statement)
	if err := api.ScanAll(dst, iter); err != nil {
		return fmt.Errorf("scanning all: %w", err)
	}

	return nil
}

// UpdateAndGet is a package-level helper function that uses the API.Get() call inside a transaction.
// This should be used when data is being returned after an update using the THEN RETURN clause.
func (api *API) UpdateAndGet(ctx context.Context, client TxnRunner, dst any, statement spanner.Statement) error {
	_, err := client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		if err := api.Get(ctx, txn, dst, statement); err != nil {
			return fmt.Errorf("API.Get(): %w", err)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("ReadWriteTransaction(): %w", err)
	}

	return nil
}

// UpdateAndSelect is a high-level helper function that uses the API.Select() call inside a transaction.
// This should be used when data is being returned after an update using the THEN RETURN clause.
func (api *API) UpdateAndSelect(ctx context.Context, client TxnRunner, dst any, statement spanner.Statement) error {
	_, err := client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		if err := api.Select(ctx, txn, dst, statement); err != nil {
			return fmt.Errorf("API.Select(): %w", err)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("ReadWriteTransaction(): %w", err)
	}

	return nil
}

// ScanOne makes sure that there was exactly one row otherwise it returns an error.
// Use NotFound function to check if there were no rows. After iterating ScanOne
// closes the interator, and propagates any errors that could pop up.
// It scans data from that single row into the destination.
func (api *API) ScanOne(dst any, iter *spanner.RowIterator) error {
	defer iter.Stop()
	var count int
	for {
		row, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return fmt.Errorf("spanner.RowIterator.Next(): %w", err)
		}

		count++
		if count > 1 {
			return fmt.Errorf("expected 1 row, got > 1")
		}

		if api.lenient {
			if err := row.ToStructLenient(dst); err != nil {
				return fmt.Errorf("spanner.Row.ToStruct(): %w", err)
			}
		} else {
			if err := row.ToStruct(dst); err != nil {
				return fmt.Errorf("spanner.Row.ToStruct(): %w", err)
			}
		}
	}

	if count == 0 {
		return ErrNotFound
	}

	return nil
}

// ScanAll iterates all rows to the end. After iterating it closes the interator, and propagates
// any errors that could pop up. It expects that destination should be a slice. For each row it
// scans data and appends it to the destination slice. ScanAll supports both types of slices:
// slice of structs by a pointer and slice of structs by value,
// for example:
//
//	type User struct {
//	    ID    string
//	    Name  string
//	}
//
//	var usersByPtr []*User
//	var usersByValue []User
//
// Both usersByPtr and usersByValue are valid destinations for ScanAll function.
//
// Before starting, ScanAll resets the destination slice,
// so if it's not empty it will overwrite all existing elements.
func (api *API) ScanAll(dst any, iter *spanner.RowIterator) error {
	defer iter.Stop()

	dstRefect, err := parseSliceDestination(dst)
	if err != nil {
		return fmt.Errorf("parsing slice destination: %w", err)
	}
	// Make sure slice is empty.
	dstRefect.val.Set(dstRefect.val.Slice(0, 0))

	for {
		row, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return fmt.Errorf("spanner.RowIterator.Next(): %w", err)
		}

		val := reflect.New(dstRefect.elementBaseType)
		if api.lenient {
			if err := row.ToStructLenient(val.Interface()); err != nil {
				return fmt.Errorf("spanner.Row.ToStruct(): %w", err)
			}
		} else {
			if err := row.ToStruct(val.Interface()); err != nil {
				return fmt.Errorf("spanner.Row.ToStruct(): %w", err)
			}
		}
		if !dstRefect.elementByPtr {
			val = val.Elem()
		}

		dstRefect.val.Set(reflect.Append(dstRefect.val, val))
	}

	return nil
}

type sliceDestinationMeta struct {
	val             reflect.Value
	elementBaseType reflect.Type
	elementByPtr    bool
}

func parseSliceDestination(dst interface{}) (*sliceDestinationMeta, error) {
	dstValue, err := parseDestination(dst)
	if err != nil {
		return nil, err
	}

	dstType := dstValue.Type()

	if dstValue.Kind() != reflect.Slice {
		return nil, fmt.Errorf(
			"spxscan: destination must be a slice, got: %v", dstType,
		)
	}

	elementBaseType := dstType.Elem()
	var elementByPtr bool
	// If it's a slice of pointers to structs,
	// we handle it the same way as it would be slice of struct by value
	// and dereference pointers to values,
	// because eventually we work with fields.
	// But if it's a slice of primitive type e.g. or []string or []*string,
	// we must leave and pass elements as is to Rows.Scan().
	if elementBaseType.Kind() == reflect.Pointer {
		elementBaseTypeElem := elementBaseType.Elem()
		if elementBaseTypeElem.Kind() == reflect.Struct {
			elementBaseType = elementBaseTypeElem
			elementByPtr = true
		}
	}

	return &sliceDestinationMeta{
		val:             dstValue,
		elementBaseType: elementBaseType,
		elementByPtr:    elementByPtr,
	}, nil
}

func parseDestination(dst interface{}) (reflect.Value, error) {
	dstVal := reflect.ValueOf(dst)

	if !dstVal.IsValid() || (dstVal.Kind() == reflect.Pointer && dstVal.IsNil()) {
		return reflect.Value{}, fmt.Errorf("spxscan: destination must be a non nil pointer")
	}
	if dstVal.Kind() != reflect.Pointer {
		return reflect.Value{}, fmt.Errorf("spxscan: destination must be a pointer, got: %v", dstVal.Type())
	}

	return dstVal.Elem(), nil
}
