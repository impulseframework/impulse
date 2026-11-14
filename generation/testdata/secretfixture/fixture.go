// Package secretfixture holds the structs of the conditions:"secret" tests: a resource
// with a secret beside a field that is write-only and no secret, a method whose request
// carries a secret, and the methods whose declaration the extraction refuses.
package secretfixture

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
	// Lockbox is a resource whose Combination is a secret.
	Lockbox struct {
		ID          impulse.UUID `spanner:"Id"`
		Name        string       `spanner:"Name"`
		Combination string       `spanner:"Combination" conditions:"secret"`
		Manifest    *string      `spanner:"Manifest"    conditions:"input_only"`
	}

	// OpenLockbox is a method whose request carries a secret a dialog can mask: text.
	//
	// @rpc
	OpenLockbox struct {
		LockboxID   impulse.UUID
		Combination string `validate:"required" conditions:"secret"`
		Note        *string
	}

	// SealLockbox is a method whose secret is bytes, which a dialog draws no control over.
	//
	// @rpc
	SealLockbox struct {
		LockboxID impulse.UUID
		Seal      []byte `conditions:"secret"`
	}

	// DialLockbox declares a number a secret, which a dialog would draw in the open.
	//
	// @rpc
	DialLockbox struct {
		LockboxID impulse.UUID
		Number    int64 `conditions:"secret"`
	}

	// PickLockbox declares a picked field a secret, whose values the dialog lists.
	//
	// @rpc
	PickLockbox struct {
		LockboxID impulse.UUID `conditions:"secret"` // @enumerate(Lockboxes)
	}
)

func (*OpenLockbox) Execute(context.Context, resource.ReadWriteTransaction, *Client) error {
	return nil
}

func (*SealLockbox) Execute(context.Context, resource.ReadWriteTransaction, *Client) error {
	return nil
}

func (*DialLockbox) Execute(context.Context, resource.ReadWriteTransaction, *Client) error {
	return nil
}

func (*PickLockbox) Execute(context.Context, resource.ReadWriteTransaction, *Client) error {
	return nil
}
