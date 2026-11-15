// Package computedresources provides Lodestar's computed resources: read-only resources
// whose rows come from application-written query logic instead of a table or a
// subquery. The generated handlers check permissions eagerly at decode time (there is no
// library execution underneath to defer to) and then call this package's List and Read
// functions. A List function is correct with no query code at all, because the handler
// applies the request's filter, sort, and page over the rows it yields; a body that runs
// its own SQL may take pieces of that work (Filter().Take, TakeSort, TakePage) and push
// them down. The ledger pushes down; the hazard board folds. The wire cannot tell.
package computedresources

import (
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/resource"
)

// Client carries application dependencies into computed-resource query logic. The type
// exists to exercise the generated ComputedClient wiring.
type Client struct{}

// NewClient constructs a Client.
func NewClient() *Client {
	return &Client{}
}

// sectorOf returns the sector the request was checked in, as the id the sector's rows
// carry in their tenancy column. The generated computed handlers check permissions in the
// URL sector's partition and hand the List and Read functions the QuerySet that carries
// that scope, so a sector-scoped computed resource partitions its rows on exactly the
// value the permission check ran against. A global scope here is a wiring error: the
// resource is declared @domain. A sector's key is an id, so the scope's domain is an
// id's text, and one that is not names no sector.
//
// Demonstrates: computed.scope.
func sectorOf[Resource resource.Resourcer](qSet *resource.QuerySet[Resource]) (impulse.UUID, error) {
	domain, ok := qSet.Scope().Domain()
	if !ok {
		return impulse.NilUUID, errors.Newf("%s is sector-scoped but was checked in the global scope", qSet.Resource())
	}
	sector, err := impulse.UUIDFromString(string(domain))
	if err != nil {
		return impulse.NilUUID, errors.Wrapf(err, "impulse.UUIDFromString(): the sector %s of %s", domain, qSet.Resource())
	}

	return sector, nil
}
