// Package roleoffixture holds the field annotation @roleOf on each struct kind: a table,
// a view and a computed resource declaring it on a string, a nullable string and a list
// of strings, which the extraction takes, and the shapes it refuses beside them (a field
// of another type, two auths named, and a request field).
package roleoffixture

import (
	"context"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/resource"
)

// Client stands in for the application's RPC client.
type Client struct{}

// Struct-level annotations only parse from a TypeSpec doc comment, so the structs sit in
// a grouped type block like real project sources do.
type (
	// Seat is a table-backed resource whose fields hold a role's name, one or many.
	//
	// @resource
	Seat struct {
		ID     impulse.UUID `spanner:"Id"`
		Role   string       `spanner:"Role"`   // @roleOf(Staff)
		Backup *string      `spanner:"Backup"` // @roleOf(Staff)
		Roles  []string     `spanner:"Roles"`  // @roleOf(Staff)
		Note   string       `spanner:"Note"`
	}

	// Bench is a table-backed resource declaring @roleOf on a number.
	//
	// @resource
	Bench struct {
		ID    impulse.UUID `spanner:"Id"`
		Count int64        `spanner:"Count"` // @roleOf(Staff)
	}

	// Stool is a table-backed resource naming two auths in one @roleOf.
	//
	// @resource
	Stool struct {
		ID   impulse.UUID `spanner:"Id"`
		Role string       `spanner:"Role"` // @roleOf(Staff, Crew)
	}

	// SeatView is a view whose fields hold a role's name, one or many.
	//
	// @virtual
	SeatView struct {
		ID     impulse.UUID `spanner:"Id" index:"true"` // @primarykey
		Role   string       `spanner:"Role"`            // @roleOf(Staff)
		Backup *string      `spanner:"Backup"`          // @roleOf(Staff)
		Roles  []string     `spanner:"Roles"`           // @roleOf(Staff)
	}

	// BenchView is a view declaring @roleOf on a boolean.
	//
	// @virtual
	BenchView struct {
		ID   impulse.UUID `spanner:"Id" index:"true"` // @primarykey
		Open bool         `spanner:"Open"`            // @roleOf(Staff)
	}

	// StoolView is a view naming two auths in one @roleOf.
	//
	// @virtual
	StoolView struct {
		ID   impulse.UUID `spanner:"Id" index:"true"` // @primarykey
		Role string       `spanner:"Role"`            // @roleOf(Staff, Crew)
	}

	// SeatLedger is a computed resource whose fields hold a role's name, one or many.
	//
	// @computed
	SeatLedger struct {
		ID     impulse.UUID // @primarykey
		Role   string       // @roleOf(Staff)
		Backup *string      // @roleOf(Staff)
		Roles  []string     // @roleOf(Staff)
	}

	// BenchLedger is a computed resource declaring @roleOf on a list of numbers.
	//
	// @computed
	BenchLedger struct {
		ID     impulse.UUID // @primarykey
		Counts []int64      // @roleOf(Staff)
	}

	// StoolLedger is a computed resource naming two auths in one @roleOf.
	//
	// @computed
	StoolLedger struct {
		ID   impulse.UUID // @primarykey
		Role string       // @roleOf(Staff, Crew)
	}

	// TakeSeat is a method declaring @roleOf on its request's role field.
	//
	// @rpc
	TakeSeat struct {
		Role string // @roleOf(Staff)
	}
)

func (*TakeSeat) Execute(context.Context, resource.ReadWriteTransaction, *Client) error {
	return nil
}
