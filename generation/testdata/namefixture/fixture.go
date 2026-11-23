// Package namefixture provides parsed-struct fixtures for the name a resource's rows are
// known by (@name) and the name field the generator adds beside each id that points to a
// row with one: a tenant record and its tenant-scoped resources, a global resource, a
// view, a key into an enumeration table, a default order by a name field, each way a
// filter may name a name field and may not, and each shape the annotation refuses. The schema behind the table-backed structs is synthetic
// (nameFixtureTables in the tests), and Grades is an enumeration table there.
package namefixture

import (
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/resource"
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

	// Bay is a resource of a tenant that reaches its tenant through its wing, known by
	// its code.
	//
	// @resource
	// @permissionScope(domain)
	Bay struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain(via: BaseID)
		WingID impulse.UUID `spanner:"WingId"`
		// @name
		Code string `spanner:"Code"`
	}

	// Berth is a tenant's resource pointing to a row that reaches its tenant through
	// another table.
	//
	// @resource
	// @permissionScope(domain)
	Berth struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		BaseID impulse.UUID `spanner:"BaseId"`
		BayID  impulse.UUID `spanner:"BayId"`
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

	// Member is a global view known by the name it shows: an id pointing into it is given
	// a name field, read from the view's query.
	//
	// @virtual
	Member struct {
		ID impulse.UUID `spanner:"Id" index:"true"` // @primarykey
		// @name
		Shown string `spanner:"Shown"`
	}

	// Hangar is a tenant's view known by a door sign a filter may name, holding its rows
	// to their tenant on a column of its own.
	//
	// @virtual
	// @permissionScope(domain)
	Hangar struct {
		ID impulse.UUID `spanner:"Id" index:"true"` // @primarykey
		// @domain
		BaseID impulse.UUID `spanner:"BaseId"`
		// @name
		Door string `spanner:"Door" allow_filter:"true"`
	}

	// Beacon is a global view known by an indexed signal that also carries a table's
	// search fact, which a name read from the view does not answer.
	//
	// @virtual
	Beacon struct {
		ID impulse.UUID `spanner:"Id" index:"true"` // @primarykey
		// @name
		Signal string `spanner:"Signal" index:"true" search:"Seals.Motto"`
	}

	// Patrol is a view that says nothing of what its rows are known by.
	//
	// @virtual
	Patrol struct {
		ID    impulse.UUID `spanner:"Id" index:"true"` // @primarykey
		Route string       `spanner:"Route"`
	}

	// Convoy is a view keyed by two columns, known by its label: no one id names one of
	// its rows.
	//
	// @virtual
	Convoy struct {
		ID  impulse.UUID `spanner:"Id" index:"true"` // @primarykey
		Leg int64        `spanner:"Leg"`             // @primarykey
		// @name
		Label string `spanner:"Label"`
	}

	// Manifest is a tenant's resource pointing by indexed ids into a tenant's view and a
	// global one, each known by a name, and into a view that says nothing of its rows'
	// names.
	//
	// @resource
	// @permissionScope(domain)
	Manifest struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		BaseID   impulse.UUID `spanner:"BaseId"`
		HangarID impulse.UUID `spanner:"HangarId"` // @enumerate(Hangars)
		BeaconID impulse.UUID `spanner:"BeaconId"` // @enumerate(Beacons)
		PatrolID impulse.UUID `spanner:"PatrolId"` // @enumerate(Patrols)
	}

	// Tour is a tenant's resource whose list is also asked across tenants, pointing into a
	// tenant's view and a global one: only the global view's name is read.
	//
	// @resource
	// @permissionScope(domain)
	// @listAcrossTenants
	Tour struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		BaseID   impulse.UUID `spanner:"BaseId"`
		HangarID impulse.UUID `spanner:"HangarId"` // @enumerate(Hangars)
		MemberID impulse.UUID `spanner:"MemberId"` // @enumerate(Members)
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

	// Vault marks a secret as its name.
	//
	// @resource
	Vault struct {
		ID impulse.UUID `spanner:"Id"`
		// @name
		Phrase resource.Secret `spanner:"Phrase" conditions:"secret"`
	}

	// Drop marks a write-only field as its name.
	//
	// @resource
	Drop struct {
		ID impulse.UUID `spanner:"Id"`
		// @name
		Slip *string `spanner:"Slip" conditions:"input_only"`
	}

	// Folder marks the store key of its file as its name.
	//
	// @resource
	Folder struct {
		ID impulse.UUID `spanner:"Id"`
		// @name
		// @file
		Scan string `spanner:"Scan"`
	}

	// Sortie is a tenant's resource listed in the order of the wings its rows point to,
	// by name, and then of their kits, last name first.
	//
	// @resource
	// @permissionScope(domain)
	// @order(WingName asc, KitName desc)
	Sortie struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		BaseID impulse.UUID `spanner:"BaseId"`
		WingID impulse.UUID `spanner:"WingId"`
		KitID  impulse.UUID `spanner:"KitId"`
	}

	// Muster is a view listed in the order of the kits its rows point to, by name.
	//
	// @virtual
	// @order(KitName asc)
	Muster struct {
		ID    impulse.UUID `spanner:"Id" index:"true"` // @primarykey
		KitID impulse.UUID `spanner:"KitId"`           // @enumerate(Kits)
	}

	// Detour orders by the name of a row that has none: a crate declares no @name.
	//
	// @resource
	// @permissionScope(domain)
	// @order(CrateName asc)
	Detour struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		BaseID  impulse.UUID `spanner:"BaseId"`
		CrateID impulse.UUID `spanner:"CrateId"`
	}

	// Badge is a global resource known by an indexed title.
	//
	// @resource
	Badge struct {
		ID impulse.UUID `spanner:"Id"`
		// @name
		Title string `spanner:"Title"`
	}

	// Seal is a global resource known by a motto its search index answers, which no
	// index of its own leads.
	//
	// @resource
	Seal struct {
		ID impulse.UUID `spanner:"Id"`
		// @name
		Motto string `spanner:"Motto"`
	}

	// Ward is a global resource known by an indexed alias that is PII.
	//
	// @resource
	Ward struct {
		ID impulse.UUID `spanner:"Id"`
		// @name
		Alias string `spanner:"Alias" conditions:"pii"`
	}

	// Docket is a global resource pointing to a name in each way a filter may name it, and
	// in the ways it may not: an indexed id to an indexed name, to an enumeration table, to
	// a name its search index answers and to a PII name; an allow_filter id to a name its
	// search index answers; an id that is not filterable; and an indexed id to a name that
	// is not filterable where it lives.
	//
	// @resource
	Docket struct {
		ID           impulse.UUID `spanner:"Id"`
		BadgeID      impulse.UUID `spanner:"BadgeId"`
		Grade        string       `spanner:"Grade"`
		SealID       impulse.UUID `spanner:"SealId"`
		SpareSealID  impulse.UUID `allow_filter:"true" spanner:"SpareSealId"`
		LooseBadgeID impulse.UUID `spanner:"LooseBadgeId"`
		KitID        impulse.UUID `spanner:"KitId"`
		WardID       impulse.UUID `spanner:"WardId"`
	}

	// Astray orders by a name no field of it could be given.
	//
	// @resource
	// @permissionScope(domain)
	// @order(SquadName asc)
	Astray struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		BaseID impulse.UUID `spanner:"BaseId"`
		WingID impulse.UUID `spanner:"WingId"`
	}
)
