// Package defaultfixture provides a parsed-struct fixture for the default a schema
// declares on a yes or no column: TRUE, FALSE, false in lower case, none, an expression
// that is neither word, a nullable column with a default, and a column with a default
// that is not a BOOL, over the synthetic table map defaultFixtureTables in the tests.
package defaultfixture

import "github.com/impulseframework/impulse"

// Struct-level annotations only parse from a TypeSpec doc comment, so the struct sits in
// a grouped type block like real project sources do.
type (
	// Switch carries one field for each default a column can declare: Lit is on unless
	// said, Dark and Low are off unless said (spelled in upper and in lower case), Open
	// declares nothing, Odd declares an expression, Maybe is a nullable column that is
	// on unless said, and Count is a number with a default.
	//
	// @resource
	Switch struct {
		ID    impulse.UUID `spanner:"Id"`
		Lit   bool         `spanner:"Lit"`
		Dark  bool         `spanner:"Dark"`
		Low   bool         `spanner:"Low"`
		Open  bool         `spanner:"Open"`
		Odd   bool         `spanner:"Odd"`
		Maybe *bool        `spanner:"Maybe"`
		Count int64        `spanner:"Count"`
	}
)
