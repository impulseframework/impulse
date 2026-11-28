package rpc

import (
	"context"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/resource"
)

type (
	// ScrapShip is the second multi-from edge, on the second workflow: three bays
	// (inspected, in_refit, flight_test) send a ship to the scrapyard. Only the
	// Sector Marshal holds it. A scrapped ship does not come back, so the method
	// carries a class of the application's own, irreversible: someone who acts as
	// another person can ask for a session that refuses the class, and stay clear of
	// what cannot be undone.
	//
	// Demonstrates: @transition.multi-from, @class.
	//
	// @rpc
	// @permissionScope(domain)
	// @transition(Refit, from: inspected, in_refit, flight_test, to: scrapped)
	// @class(irreversible)
	ScrapShip struct {
		// @target
		RefitID impulse.UUID
	}
)

// Execute runs inside the handler's transaction.
func (m *ScrapShip) Execute(context.Context, resource.ReadWriteTransaction, *Client) error {
	return nil
}
