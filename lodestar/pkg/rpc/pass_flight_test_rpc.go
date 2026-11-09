package rpc

import (
	"context"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/resource"
)

type (
	// PassFlightTest moves a refit flight_test -> cleared and stamps the ship's
	// LastRefitAt with the commit timestamp — the one transition that owns that
	// business event. (Marked file: see StartFlightTest.)
	//
	// Demonstrates: @transition, transition-owned-timestamp, test-suffixed-method.
	//
	// @rpc
	// @permissionScope(domain)
	// @transition(Refit, from: flight_test, to: cleared)
	PassFlightTest struct {
		// @target
		RefitID impulse.UUID
	}
)

// Execute runs inside the handler's transaction.
func (m *PassFlightTest) Execute(ctx context.Context, txn resource.ReadWriteTransaction, _ *Client) error {
	return stampShipRefitted(ctx, txn, m.RefitID)
}
