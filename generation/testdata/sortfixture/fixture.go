// Package sortfixture holds one field of each kind of type a resource declares, each
// tagged with whether the server orders a list by it. Two tests read it: the resource
// package's, which asks the query decoder's rule about each field's running type, and
// the generator's, which asks its own rule about each field's declared type. The
// generated metadata tells a table which columns take a sort, so the two rules must
// answer alike for every field here.
package sortfixture

import (
	"time"

	"cloud.google.com/go/civil"
	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse"
	"github.com/shopspring/decimal"
)

// Rank is a named text type, as an enumerated key is declared.
type Rank string

// Marks is a named list.
type Marks []string

// Hold is a plain struct, as a nested computed field is declared.
type Hold struct {
	Bays int64
}

// Words is a named text type whose value is not the value its column holds, as a mask
// read as words is declared: it carries the mark the server refuses a sort by.
type Words string

// Unordered marks Words as no value a list is ordered by.
func (Words) Unordered() {
	// A marker: nothing calls it.
}

// Shapes carries the field shapes. The orders tag is yes on a field the server orders a
// list by and no on one whose sort it refuses.
type Shapes struct {
	Text          string                 `orders:"yes"`
	TextPointer   *string                `orders:"yes"`
	Named         Rank                   `orders:"yes"`
	Flag          bool                   `orders:"yes"`
	Count         int64                  `orders:"yes"`
	CountPointer  *int64                 `orders:"yes"`
	Ratio         float64                `orders:"yes"`
	Small         float32                `orders:"yes"`
	At            time.Time              `orders:"yes"`
	AtPointer     *time.Time             `orders:"yes"`
	On            civil.Date             `orders:"yes"`
	OnPointer     *civil.Date            `orders:"yes"`
	Price         decimal.Decimal        `orders:"yes"`
	NullPrice     decimal.NullDecimal    `orders:"yes"`
	ID            impulse.UUID           `orders:"yes"`
	IDPointer     *impulse.UUID          `orders:"yes"`
	NullID        impulse.NullUUID       `orders:"yes"`
	NullRank      impulse.NullEnum[Rank] `orders:"yes"`
	NullText      spanner.NullString     `orders:"yes"`
	NullAt        spanner.NullTime       `orders:"yes"`
	List          []string               `orders:"no"`
	NamedList     Marks                  `orders:"no"`
	Bytes         []byte                 `orders:"no"`
	Lookup        map[string]string      `orders:"no"`
	Nested        Hold                   `orders:"no"`
	NestedPointer *Hold                  `orders:"no"`
	NestedList    []Hold                 `orders:"no"`
	Fixed         [4]int64               `orders:"no"`
	Document      spanner.NullJSON       `orders:"no"`
	Marked        Words                  `orders:"no"`
	MarkedPointer *Words                 `orders:"no"`
}
