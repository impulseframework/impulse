// Package namefixture provides parsed-struct fixtures for the name a resource's rows are
// known by (@name) and the name field the generator adds beside each id that points to a
// row with one: a tenant record and its tenant-scoped resources, a global resource, a
// view, a key into an enumeration table, and each shape the annotation refuses. The
// schema behind the table-backed structs is synthetic (nameFixtureTables in the tests),
// and Grades is an enumeration table there.
package namefixture

import (
	"github.com/impulseframework/impulse"
)

// Struct-level annotations only parse from a TypeSpec doc comment, so the structs sit
// in a grouped type block like real project sources do.
type (
	// Base is the tenant record, known by its @tenantName.
	//
	// @resource
	// @tenant
	Base struct {
		ID impulse.UUID `spanner:"Id"`
		// @tenantName
		Title string `spanner:"Title"`
	}

	// Wing is a tenant's resource known by its call sign.
	//
	// @resource
	// @permissionScope(domain)
	Wing struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		BaseID impulse.UUID `spanner:"BaseId"`
		// @name
		CallSign string `spanner:"CallSign"`
	}

	// Kit is a global resource known by a label it may lack.
	//
	// @resource
	Kit struct {
		ID impulse.UUID `spanner:"Id"`
		// @name
		Label *string `spanner:"Label"`
	}

	// Crate is a tenant's resource that says nothing of what its rows are known by.
	//
	// @resource
	// @permissionScope(domain)
	Crate struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		BaseID impulse.UUID `spanner:"BaseId"`
		Weight int64        `spanner:"Weight"`
	}

	// Member is a global view known by the name it shows.
	//
	// @virtual
	Member struct {
		ID impulse.UUID `spanner:"Id" index:"true"` // @primarykey
		// @name
		Shown string `spanner:"Shown"`
	}

	// Permit is a tenant's resource pointing to each kind of row: its tenant, a named
	// resource of the tenant by a foreign key and by a declared enumeration, a global
	// named resource, a resource with no name, an enumeration table, and a view.
	//
	// @resource
	// @permissionScope(domain)
	Permit struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		BaseID   impulse.UUID  `spanner:"BaseId"`
		WingID   impulse.UUID  `spanner:"WingId"`
		LeadWing *impulse.UUID `spanner:"LeadWing"` // @enumerate(Wings)
		KitID    impulse.UUID  `spanner:"KitId"`
		CrateID  impulse.UUID  `spanner:"CrateId"`
		Grade    string        `spanner:"Grade"`
		MemberID impulse.UUID  `spanner:"MemberId"` // @enumerate(Members)
	}

	// Ledger is a tenant's resource whose list is also asked across tenants: no name of
	// a tenant's row is read there, and an enumeration table's and a global resource's are.
	//
	// @resource
	// @permissionScope(domain)
	// @listAcrossTenants
	Ledger struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		BaseID impulse.UUID `spanner:"BaseId"`
		WingID impulse.UUID `spanner:"WingId"`
		KitID  impulse.UUID `spanner:"KitId"`
		Grade  string       `spanner:"Grade"`
	}

	// Stock is a global resource: it is asked inside no tenant, so it reads no name of a
	// tenant's row.
	//
	// @resource
	Stock struct {
		ID     impulse.UUID `spanner:"Id"`
		WingID impulse.UUID `spanner:"WingId"`
		KitID  impulse.UUID `spanner:"KitId"`
	}

	// Roster is a view pointing to a named resource by a declared enumeration.
	//
	// @virtual
	Roster struct {
		ID    impulse.UUID `spanner:"Id" index:"true"` // @primarykey
		KitID impulse.UUID `spanner:"KitId"`           // @enumerate(Kits)
	}

	// Clash declares a field with the name the generator adds beside its WingID.
	//
	// @resource
	// @permissionScope(domain)
	Clash struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		BaseID   impulse.UUID `spanner:"BaseId"`
		WingID   impulse.UUID `spanner:"WingId"`
		WingName string       `spanner:"WingName"`
	}

	// Pair points to a wing through two fields the generator would name alike.
	//
	// @resource
	// @permissionScope(domain)
	Pair struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		BaseID impulse.UUID `spanner:"BaseId"`
		WingID impulse.UUID `spanner:"WingId"`
		Wing   impulse.UUID `spanner:"Wing"` // @enumerate(Wings)
	}

	// Counted marks a number as its name.
	//
	// @resource
	Counted struct {
		ID impulse.UUID `spanner:"Id"`
		// @name
		Count int64 `spanner:"Count"`
	}

	// Twice marks two fields.
	//
	// @resource
	Twice struct {
		ID impulse.UUID `spanner:"Id"`
		// @name
		First string `spanner:"First"`
		// @name
		Second string `spanner:"Second"`
	}

	// Fort is a tenant record marking its name with @name.
	//
	// @resource
	// @tenant
	Fort struct {
		ID impulse.UUID `spanner:"Id"`
		// @name
		Title string `spanner:"Title"`
	}

	// Tally is a computed resource marking a name, which no statement could look up.
	//
	// @computed
	Tally struct {
		// @primarykey
		ID impulse.UUID
		// @name
		Shown string
	}

	// Grade backs the enumeration table and marks a name it needs none of.
	//
	// @resource
	Grade struct {
		ID string `spanner:"Id"`
		// @name
		Description string `spanner:"Description"`
	}
)
