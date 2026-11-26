// Package keyorderfixture provides a parsed-struct fixture for a table whose struct lists its
// key fields in another order than the table's key.
package keyorderfixture

import (
	"time"

	"github.com/impulseframework/impulse"
)

// Struct-level annotations only parse from a TypeSpec doc comment, so the struct sits in a
// grouped type block like real project sources do.
type (
	// InvoiceLine is keyed by its invoice and then its number in the table,
	// PRIMARY KEY (InvoiceId, LineNumber), and lists the number first here.
	//
	// @resource
	InvoiceLine struct {
		LineNumber  int64        `spanner:"LineNumber"`
		InvoiceID   impulse.UUID `spanner:"InvoiceId"`
		Description string       `spanner:"Description"`
		UpdatedAt   *time.Time   `spanner:"UpdatedAt" output_only_update_fn:"resource.CommitTimestampPtr"`
	}
)
