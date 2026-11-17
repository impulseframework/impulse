// Package secretfixture holds the structs of the conditions:"secret" tests: a resource
// with a stored secret beside a field that is write-only and no secret, resources whose
// secret the generator refuses, a method whose request carries a secret, and the methods
// whose declaration the extraction refuses.
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
	// Lockbox is a resource whose Combination is a stored secret: typed resource.Secret,
	// so its column holds the value sealed. Hint is a secret its column may leave empty.
	Lockbox struct {
		ID          impulse.UUID     `spanner:"Id"`
		Name        string           `spanner:"Name"`
		Combination resource.Secret  `spanner:"Combination" conditions:"secret"`
		Hint        *resource.Secret `spanner:"Hint"        conditions:"secret"`
		Manifest    *string          `spanner:"Manifest"    conditions:"input_only"`
	}

	// Shelf is a resource with no secret.
	Shelf struct {
		ID   impulse.UUID `spanner:"Id"`
		Name string       `spanner:"Name"`
	}

	// Drawer has a key of two columns, declared in the struct in the other order than
	// the key's, and a secret bound to it.
	Drawer struct {
		Slot  string          `spanner:"Slot"`
		Chest string          `spanner:"Chest"`
		Code  resource.Secret `spanner:"Code" conditions:"secret"`
	}

	// PlainLockbox declares its secrets on the plain types that would store them as they
	// are given, and carries the secret's type on a field that does not declare one.
	PlainLockbox struct {
		ID          impulse.UUID    `spanner:"Id"`
		Combination string          `spanner:"Combination" conditions:"secret"`
		Hint        *string         `spanner:"Hint"        conditions:"secret"`
		Seal        []byte          `spanner:"Seal"        conditions:"secret"`
		Label       resource.Secret `spanner:"Label"`
	}

	// NamedLockbox names a field as the method the generated set opens its secret with.
	NamedLockbox struct {
		ID                impulse.UUID    `spanner:"Id"`
		Combination       resource.Secret `spanner:"Combination"       conditions:"secret"`
		OpenCombinationIn string          `spanner:"OpenCombinationIn"`
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

	// StoreLockbox types a request field as a stored secret, which a request never
	// carries.
	//
	// @rpc
	StoreLockbox struct {
		LockboxID   impulse.UUID
		Combination resource.Secret `conditions:"secret"`
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

func (*StoreLockbox) Execute(context.Context, resource.ReadWriteTransaction, *Client) error {
	return nil
}
