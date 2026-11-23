package resources

import "github.com/impulseframework/impulse"

type (
	// Wing groups squadrons. It is deliberately the BARE resource: a sector-scoped
	// resource whose only field annotation is the mandatory @domain binding. It pins
	// structural fail-closed enforcement: a resource-only grant exposes nothing beyond
	// the primary key.
	//
	// A wing's delete is declared (@deleteCascade): its squadrons go with it, with their
	// memberships and the hangar passes they hold, each through its own delete, and a
	// mission assigned to one of its squadrons refuses it.
	//
	// Demonstrates: @domain, fail-closed.bare-resource.
	//
	// @resource
	// @permissionScope(domain)
	// @deleteCascade(Squadron, SquadronMembership, HangarPass, refusedBy: Mission)
	// @order(Name asc)
	// @page(default: 25, max: 200)
	Wing struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		SectorID impulse.UUID `spanner:"SectorId"`
		Name     string       `spanner:"Name"`
	}
)
