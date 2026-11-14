// Package requestfieldfixture provides parsed-struct fixtures for the request fields an
// RPC method declares for its grants' conditions (@attribute on a field of an @rpc
// struct): one method with every type a condition may read, and one struct per refusal.
package requestfieldfixture

import (
	"time"

	"cloud.google.com/go/civil"
	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse"
	"github.com/shopspring/decimal"
)

// Struct-level annotations only parse from a TypeSpec doc comment, so the methods sit
// in a grouped type block like real project sources do.
type (
	// RateThing declares one request field of each type a condition may read, a
	// pointer and a list among them, and one field it does not declare.
	//
	// @rpc
	RateThing struct {
		// @attribute(thing)
		ThingID impulse.UUID
		// @attribute(role)
		Role string
		// @attribute(note)
		Note *string
		// @attribute(level)
		Level int64
		// @attribute(count)
		Count uint32
		// @attribute(ratio)
		Ratio float64
		// @attribute(urgent)
		Urgent bool
		// @attribute(at)
		At time.Time
		// @attribute(day)
		Day civil.Date
		// @attribute(eachRole)
		Roles   []string
		Comment string
	}

	// PlainThing declares no request field.
	//
	// @rpc
	PlainThing struct {
		Role string
	}

	// JoinedField gives a request field a join path, which only a table's column has.
	//
	// @rpc
	JoinedField struct {
		// @attribute(sector, via: StationID.Sector)
		StationID impulse.UUID
	}

	// TwiceNamed declares one name on two fields.
	//
	// @rpc
	TwiceNamed struct {
		// @attribute(role)
		Role string
		// @attribute(role)
		Other string
	}

	// ReservedName takes a name the condition language keeps for itself.
	//
	// @rpc
	ReservedName struct {
		// @attribute(subject)
		Role string
	}

	// TwoLists declares two list fields.
	//
	// @rpc
	TwoLists struct {
		// @attribute(eachRole)
		Roles []string
		// @attribute(eachLevel)
		Levels []int64
	}

	// DecimalField declares a decimal, which a column may be and a request field may not.
	//
	// @rpc
	DecimalField struct {
		// @attribute(amount)
		Amount decimal.Decimal
	}

	// NullableField declares a database wrapper, which a column may be and a request
	// field may not.
	//
	// @rpc
	NullableField struct {
		// @attribute(role)
		Role spanner.NullString
	}

	// NestedField declares a struct, which has no comparison type.
	//
	// @rpc
	NestedField struct {
		// @attribute(where)
		Where struct{ X, Y int }
	}
)
