// Package acrossfixture provides parsed-struct fixtures for the list across tenants
// (@listAcrossTenants): tenant-scoped resources that declare it, one struct per
// outcome of the declaration's rules and of the index check, with the table map
// (acrossFixtureTables in the tests) carrying the index shapes the check reads.
package acrossfixture

import (
	"time"

	"github.com/impulseframework/impulse"
)

// Struct-level annotations only parse from a TypeSpec doc comment, so the structs sit
// in grouped type blocks like real project sources do.
type (
	// Shipment is listed across tenants in an order an index of its own leads with,
	// storing the tenant column: silent.
	//
	// @resource
	// @permissionScope(domain)
	// @listAcrossTenants
	// @order(DueAt desc)
	Shipment struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		TenantID string    `spanner:"TenantId"`
		DueAt    time.Time `spanner:"DueAt"`
		Label    string    `spanner:"Label"`
	}

	// Parcel is listed across tenants with the tenant's own index alone, the one that
	// leads with the tenant column: the tenant's list is served, the list across
	// tenants is not, and it warns naming the index it wants.
	//
	// @resource
	// @permissionScope(domain)
	// @listAcrossTenants
	// @order(DueAt asc, Label desc)
	Parcel struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		TenantID string    `spanner:"TenantId"`
		DueAt    time.Time `spanner:"DueAt"`
		Label    string    `spanner:"Label"`
	}

	// Carton has an index in its order that does not carry the tenant column: warns,
	// since the filter on the tenant would read every row the index names.
	//
	// @resource
	// @permissionScope(domain)
	// @listAcrossTenants
	// @order(DueAt asc)
	Carton struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		TenantID string    `spanner:"TenantId"`
		DueAt    time.Time `spanner:"DueAt"`
	}

	// Pallet has an index in its order with the tenant column later in the key: silent.
	//
	// @resource
	// @permissionScope(domain)
	// @listAcrossTenants
	// @order(DueAt asc)
	Pallet struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		TenantID string    `spanner:"TenantId"`
		DueAt    time.Time `spanner:"DueAt"`
	}

	// Drum keys its rows by the tenant column and a sequence: every index of the
	// table carries the primary key, so the index in its order carries the tenant
	// column, and it is silent.
	//
	// @resource
	// @permissionScope(domain)
	// @listAcrossTenants
	// @order(Label asc)
	Drum struct {
		// @domain
		TenantID string `spanner:"TenantId"`
		Sequence int64  `spanner:"Sequence"`
		Label    string `spanner:"Label"`
	}

	// Crate declares no order: the list across tenants is in primary-key order, which
	// the table serves, and it is silent.
	//
	// @resource
	// @permissionScope(domain)
	// @listAcrossTenants
	Crate struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		TenantID string `spanner:"TenantId"`
		Label    string `spanner:"Label"`
	}

	// Seal resolves its tenant through a join path: its lists carry the join-path
	// warning, and the list across tenants adds none of its own.
	//
	// @resource
	// @permissionScope(domain)
	// @listAcrossTenants
	// @order(Mark asc)
	Seal struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain(via: TenantID)
		CrateID impulse.UUID `spanner:"CrateId"`
		Mark    string       `spanner:"Mark"`
	}

	// Ledger is undeclared: a tenant-scoped resource served under the tenant's segment
	// alone.
	//
	// @resource
	// @permissionScope(domain)
	// @order(DueAt asc)
	Ledger struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		TenantID string    `spanner:"TenantId"`
		DueAt    time.Time `spanner:"DueAt"`
	}

	// Depot is global and declares the list across tenants: refused, since a global
	// resource has no tenant to read across.
	//
	// @resource
	// @listAcrossTenants
	Depot struct {
		ID   impulse.UUID `spanner:"Id"`
		Name string       `spanner:"Name"`
	}

	// Vault suppresses its list and declares the list across tenants: refused, since
	// there is no list to ask without the segment.
	//
	// @resource
	// @permissionScope(domain)
	// @suppress(listHandler)
	// @listAcrossTenants
	Vault struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		TenantID string `spanner:"TenantId"`
	}

	// Board is a view listed across tenants: accepted, and silent, since a view's
	// index facts are tags, not schema.
	//
	// @virtual
	// @permissionScope(domain)
	// @listAcrossTenants
	// @order(DueAt desc)
	Board struct {
		// @primarykey
		ID impulse.UUID `spanner:"Id" uniqueindex:"true"`
		// @domain
		TenantID string    `spanner:"TenantId"`
		DueAt    time.Time `spanner:"DueAt"`
	}

	// GlobalBoard is a global view that declares the list across tenants: refused.
	//
	// @virtual
	// @listAcrossTenants
	GlobalBoard struct {
		// @primarykey
		ID impulse.UUID `spanner:"Id" uniqueindex:"true"`
	}

	// Tally is a computed resource that declares the list across tenants: refused,
	// since its rows come from the application's own function.
	//
	// @computed
	// @permissionScope(domain)
	// @listAcrossTenants
	Tally struct {
		// @primarykey
		ID    impulse.UUID
		Count int64
	}

	// Recount is an RPC method that declares the list across tenants: refused.
	//
	// @rpc
	// @permissionScope(domain)
	// @listAcrossTenants
	Recount struct {
		Label string
	}
)
