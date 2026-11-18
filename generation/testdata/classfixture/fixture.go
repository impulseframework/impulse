// Package classfixture provides parsed fixtures for the @class annotation: methods and
// registrations made by hand that carry classes, and each declaration the generator
// refuses.
package classfixture

import (
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
)

const (
	// ResetLogin is registered by hand and carries the library's credential class.
	// @manualAddResource(Execute)
	// @class(credential)
	ResetLogin accesstypes.Resource = "ResetLogin"

	// ExportLedger carries two classes of the application's own.
	// @manualAddResource(List, domain)
	// @class(money_movement)
	// @class(sensitive)
	ExportLedger accesstypes.Resource = "ExportLedger"

	// PlainThing is registered by hand and carries no class.
	// @manualAddResource(Execute)
	PlainThing accesstypes.Resource = "PlainThing"

	// Orphan carries a class and registers nothing.
	// @class(credential)
	Orphan accesstypes.Resource = "Orphan"

	// Misnamed names a class as no class is named.
	// @manualAddResource(Execute)
	// @class(Money Movement)
	Misnamed accesstypes.Resource = "Misnamed"
)

type (
	// RotateKey is a method of the credential class.
	//
	// @rpc
	// @class(credential)
	RotateKey struct {
		KeyID impulse.UUID
	}

	// WireFunds carries two classes, one list and out of order.
	//
	// @rpc
	// @class(sensitive, money_movement)
	WireFunds struct {
		Amount int64
	}

	// Launch carries no class.
	//
	// @rpc
	Launch struct {
		Name string
	}

	// Twice names one class twice.
	//
	// @rpc
	// @class(credential)
	// @class(credential)
	Twice struct {
		Name string
	}

	// Shouted names a class as no class is named.
	//
	// @rpc
	// @class(Credential)
	Shouted struct {
		Name string
	}

	// Vault is a resource, which is no action.
	//
	// @resource
	// @class(credential)
	Vault struct {
		ID   impulse.UUID `spanner:"Id"`
		Name string       `spanner:"Name"`
	}
)
