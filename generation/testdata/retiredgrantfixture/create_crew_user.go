// Package retiredgrantfixture holds a method an application declares itself under a
// name the generated management of its crew auth once used, the direct create of an
// account. A role file that grants it grants the application's own method, which the
// generator's refusal of the grants of methods that are gone leaves alone.
package retiredgrantfixture

import (
	"context"

	"github.com/impulseframework/impulse/resource"
)

// Client stands in for the application's RPC client.
type Client struct{}

type (
	// CreateCrewUser is the application's own method, whatever it does.
	//
	// @rpc
	CreateCrewUser struct{}
)

func (*CreateCrewUser) Execute(context.Context, resource.ReadWriteTransaction, *Client) error {
	return nil
}
