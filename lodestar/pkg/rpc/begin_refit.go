package rpc

import (
	"context"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/resource"
)

type (
	// BeginRefit moves a refit inspected -> in_refit. The declared edge is the whole
	// effect.
	//
	// Demonstrates: @transition, @transition.join-path-root.
	//
	// @rpc
	// @permissionScope(domain)
	// @transition(Refit, from: inspected, to: in_refit)
	BeginRefit struct {
		// @target
		RefitID impulse.UUID
	}
)

// Execute runs inside the handler's transaction.
func (m *BeginRefit) Execute(context.Context, resource.ReadWriteTransaction, *Client) error {
	return nil
}
