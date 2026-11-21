package resources

import "github.com/impulseframework/impulse"

type (
	// Wing groups squadrons. It is deliberately the BARE resource: a sector-scoped
	// resource whose only field annotation is the mandatory @domain binding. It pins
	// structural fail-closed enforcement: a resource-only grant exposes nothing beyond
	// the primary key.
	//
	// A wing's squadrons go with it, and their memberships with them: each reaches its
	// sector through the wing, and every link of a table's way to its sector carries the
	// delete rule, so the database removes them with the wing, whether the sector is
	// deleted or the wing alone, past their own Delete grants and with no change event.
	//
	// Demonstrates: @domain, fail-closed.bare-resource.
	//
	// @resource
	// @permissionScope(domain)
	// @order(Name asc)
	// @page(default: 25, max: 200)
	Wing struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		SectorID impulse.UUID `spanner:"SectorId"`
		Name     string       `spanner:"Name"`
	}
)
