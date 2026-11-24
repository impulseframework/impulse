// Package tenantfixture provides parsed-struct fixtures for the tenant record's capture
// rules (@tenant): the well-formed record beside a tenant-scoped resource, and each
// shape the annotation refuses. A tenant record is keyed by an id its create path
// generates (impulse.UUID), and a tenant-scoped record carries that id in its tenant
// column, as the id or as its text. The schema behind the table-backed structs is
// synthetic (tenantFixtureTables in the tests); Docks is keyed twice there, and the key
// of Depots is a foreign key.
package tenantfixture

import (
	"time"

	"cloud.google.com/go/civil"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/resource"
)

// Struct-level annotations only parse from a TypeSpec doc comment, so the structs sit
// in a grouped type block like real project sources do.
type (
	// Sector is the well-formed tenant record: global, table-backed, one key, an id its
	// create path generates.
	//
	// @resource
	// @tenant
	Sector struct {
		ID   impulse.UUID `spanner:"Id"`
		Name string       `spanner:"Name"`
	}

	// Berth is tenant-scoped: served under the record's segment.
	//
	// @resource
	// @permissionScope(domain)
	Berth struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		SectorID impulse.UUID `spanner:"SectorId"`
		Name     string       `spanner:"Name"`
	}

	// Outpost declares @tenant on a tenant-scoped resource.
	//
	// @resource
	// @tenant
	// @permissionScope(domain)
	Outpost struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		SectorID impulse.UUID `spanner:"SectorId"`
	}

	// Dock declares @tenant on a resource the schema keys twice.
	//
	// @resource
	// @tenant
	Dock struct {
		SectorID impulse.UUID `spanner:"SectorId"`
		Number   int64        `spanner:"Number"`
	}

	// Hangar declares @tenant on a resource whose key is a string a person types, not an
	// id its create path generates.
	//
	// @resource
	// @tenant
	Hangar struct {
		ID   string `spanner:"Id"`
		Name string `spanner:"Name"`
	}

	// Depot declares @tenant on a resource whose id is the key of another table's row (a
	// foreign key in the schema): the caller names it, and its create path generates
	// none.
	//
	// @resource
	// @tenant
	Depot struct {
		ID   impulse.UUID `spanner:"Id"`
		Name string       `spanner:"Name"`
	}

	// Region is a second tenant record beside Sector.
	//
	// @resource
	// @tenant
	Region struct {
		ID impulse.UUID `spanner:"Id"`
	}

	// Ledger declares @tenant on a view.
	//
	// @virtual
	// @tenant
	Ledger struct {
		// @primarykey
		ID   impulse.UUID `spanner:"Id" uniqueindex:"true"`
		Name string       `spanner:"Name"`
	}

	// Tally declares @tenant on a computed resource.
	//
	// @computed
	// @tenant
	Tally struct {
		ID    impulse.UUID // @primarykey
		Total int64
	}

	// Reindex declares @tenant on a method.
	//
	// @rpc
	// @tenant
	Reindex struct {
		Input string
	}

	// Station is a tenant record a person who signs up creates through the generated
	// create-your-station route, where the route serves. Beside the key, the id the
	// route generates, it carries a plain column, a nullable one, a column of a type
	// from another package and a server-owned one.
	//
	// @resource
	// @tenant
	Station struct {
		ID      impulse.UUID `spanner:"Id"`
		Name    string       `spanner:"Name"`
		Motto   *string      `spanner:"Motto"`
		Opened  civil.Date   `spanner:"Opened"`
		Derived string       `spanner:"Derived" conditions:"output_only"`
	}

	// Colony is a second tenant record with a create-your-colony route where it serves.
	//
	// @resource
	// @tenant
	Colony struct {
		ID   impulse.UUID `spanner:"Id"`
		Name string       `spanner:"Name"`
	}

	// Enclave still declares the retired creation argument, with the value that was
	// its default.
	//
	// @resource
	// @tenant(creation: administrator)
	Enclave struct {
		ID impulse.UUID `spanner:"Id"`
	}

	// Outland still declares the retired creation argument, with the value that let a
	// stranger create a tenant.
	//
	// @resource
	// @tenant(creation: self-service)
	Outland struct {
		ID impulse.UUID `spanner:"Id"`
	}

	// Frontier still declares the retired creation argument, with the value that kept
	// both ways.
	//
	// @resource
	// @tenant(creation: both)
	Frontier struct {
		ID impulse.UUID `spanner:"Id"`
	}

	// Citadel is a tenant record with every kind of field an invitation that makes a
	// tenant meets: the ones it carries (the name people call the citadel by, a field
	// the create may leave out, a date, a yes or no) and the ones it does not (the key,
	// a secret, a write-only field and a field the server owns).
	//
	// @resource
	// @tenant
	Citadel struct {
		ID impulse.UUID `spanner:"Id"`
		// @tenantName
		Name       string           `spanner:"Name"`
		Motto      *string          `spanner:"Motto"`
		Founded    civil.Date       `spanner:"Founded"`
		Walled     bool             `spanner:"Walled"`
		Passphrase *resource.Secret `spanner:"Passphrase" conditions:"secret"`
		Cipher     *string          `spanner:"Cipher"     conditions:"input_only"`
		Sealed     string           `spanner:"Sealed"     conditions:"output_only"`
	}

	// Keep is a tenant record whose create requires a secret: its column takes no NULL
	// and has no default.
	//
	// @resource
	// @tenant
	Keep struct {
		ID         impulse.UUID    `spanner:"Id"`
		Name       string          `spanner:"Name"`
		Passphrase resource.Secret `spanner:"Passphrase" conditions:"secret"`
	}

	// Redoubt is a tenant record whose create requires a write-only field.
	//
	// @resource
	// @tenant
	Redoubt struct {
		ID     impulse.UUID `spanner:"Id"`
		Name   string       `spanner:"Name"`
		Cipher string       `spanner:"Cipher" conditions:"input_only"`
	}

	// Province passes the shape without its key.
	//
	// @resource
	// @tenant(self-service)
	Province struct {
		ID impulse.UUID `spanner:"Id"`
	}

	// CreateStation is a method named as the Station record's create-your-station
	// handler would be.
	//
	// @rpc
	CreateStation struct {
		Input string
	}

	// Bastion names an argument @tenant does not take.
	//
	// @resource
	// @tenant(shape: both)
	Bastion struct {
		ID impulse.UUID `spanner:"Id"`
	}

	// Harbor is a tenant record that declares who may enter a harbor: its people enter
	// through its directories only when DirectoryOnly says so (@directoryOnly), and
	// HarborDirectory holds the directories.
	//
	// @resource
	// @tenant
	Harbor struct {
		ID   impulse.UUID `spanner:"Id"`
		Name string       `spanner:"Name"`
		// @directoryOnly
		DirectoryOnly bool `spanner:"DirectoryOnly"`
	}

	// HarborDirectory is the well-formed directories record: tenant-scoped through the
	// bare @domain column, which holds the harbor's id, with its method and connection
	// columns, the default seat a stranger it vouches for gets at their first sign-in,
	// and the harbor's word that its directory asks its people for a second factor of
	// its own.
	//
	// @resource
	// @permissionScope(domain)
	// @tenantDirectory
	HarborDirectory struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		HarborID impulse.UUID `spanner:"HarborId"`
		// @directoryMethod
		Method string `spanner:"Method"`
		// @directoryConnection
		Connection string `spanner:"Connection"`
		// @directoryDefaultSeat
		DefaultSeat *string `spanner:"DefaultSeat"`
		// @directorySecondFactor
		AsksSecondFactor bool `spanner:"AsksSecondFactor"`
	}

	// Landing is a well-formed directories record with the harbor's word for its
	// directory's second factor and without the default seat column.
	//
	// @resource
	// @permissionScope(domain)
	// @tenantDirectory
	Landing struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		HarborID impulse.UUID `spanner:"HarborId"`
		// @directoryMethod
		Method string `spanner:"Method"`
		// @directoryConnection
		Connection string `spanner:"Connection"`
		// @directorySecondFactor
		AsksSecondFactor bool `spanner:"AsksSecondFactor"`
	}

	// Mole is a well-formed directories record with the default seat column and without
	// the harbor's word for its directory's second factor.
	//
	// @resource
	// @permissionScope(domain)
	// @tenantDirectory
	Mole struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		HarborID impulse.UUID `spanner:"HarborId"`
		// @directoryMethod
		Method string `spanner:"Method"`
		// @directoryConnection
		Connection string `spanner:"Connection"`
		// @directoryDefaultSeat
		DefaultSeat *string `spanner:"DefaultSeat"`
	}

	// Breakwater is a well-formed directories record without the default seat column:
	// every harbor admits the strangers its directories vouch for by invitation only.
	//
	// @resource
	// @permissionScope(domain)
	// @tenantDirectory
	Breakwater struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		HarborID impulse.UUID `spanner:"HarborId"`
		// @directoryMethod
		Method string `spanner:"Method"`
		// @directoryConnection
		Connection string `spanner:"Connection"`
	}

	// Marina is a well-formed directories record whose tenant column is a string: it
	// holds the harbor's id as its text, which is what the create-your-tenant route
	// hands it.
	//
	// @resource
	// @permissionScope(domain)
	// @tenantDirectory
	Marina struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		HarborID string `spanner:"HarborId"`
		// @directoryMethod
		Method string `spanner:"Method"`
		// @directoryConnection
		Connection string `spanner:"Connection"`
	}

	// Causeway is a directories record whose table carries no unique index on the tenant
	// column: nothing in the schema keeps a harbor to one directory.
	//
	// @resource
	// @permissionScope(domain)
	// @tenantDirectory
	Causeway struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		HarborID impulse.UUID `spanner:"HarborId"`
		// @directoryMethod
		Method string `spanner:"Method"`
		// @directoryConnection
		Connection string `spanner:"Connection"`
	}

	// Seawall is a directories record whose table carries no unique index on the method
	// and the connection together: nothing in the schema keeps a directory to one
	// harbor. The index the table does carry, on the harbor with the two, keeps neither
	// rule.
	//
	// @resource
	// @permissionScope(domain)
	// @tenantDirectory
	Seawall struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		HarborID impulse.UUID `spanner:"HarborId"`
		// @directoryMethod
		Method string `spanner:"Method"`
		// @directoryConnection
		Connection string `spanner:"Connection"`
	}

	// Pontoon is a directories record keyed by a name the request would have to type:
	// a directory is set from a sign-in, which names no key.
	//
	// @resource
	// @permissionScope(domain)
	// @tenantDirectory
	Pontoon struct {
		Name string `spanner:"Name"`
		// @domain
		HarborID impulse.UUID `spanner:"HarborId"`
		// @directoryMethod
		Method string `spanner:"Method"`
		// @directoryConnection
		Connection string `spanner:"Connection"`
	}

	// Lighthouse is a directories record whose tenant column is a nullable string:
	// neither the id nor its text, so the create-your-tenant route has no value to
	// hand it.
	//
	// @resource
	// @permissionScope(domain)
	// @tenantDirectory
	Lighthouse struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		HarborID *string `spanner:"HarborId"`
		// @directoryMethod
		Method string `spanner:"Method"`
		// @directoryConnection
		Connection string `spanner:"Connection"`
	}

	// Slipway declares @directoryDefaultSeat on a resource that is not the directories
	// record.
	//
	// @resource
	// @permissionScope(domain)
	Slipway struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		HarborID impulse.UUID `spanner:"HarborId"`
		// @directoryDefaultSeat
		Seat string `spanner:"Seat"`
	}

	// Wharf is a directories record whose default seat is a BOOL.
	//
	// @resource
	// @permissionScope(domain)
	// @tenantDirectory
	Wharf struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		HarborID impulse.UUID `spanner:"HarborId"`
		// @directoryMethod
		Method string `spanner:"Method"`
		// @directoryConnection
		Connection string `spanner:"Connection"`
		// @directoryDefaultSeat
		Seated bool `spanner:"Seated"`
	}

	// Bollard is a directories record declaring @directoryDefaultSeat on two columns.
	//
	// @resource
	// @permissionScope(domain)
	// @tenantDirectory
	Bollard struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		HarborID impulse.UUID `spanner:"HarborId"`
		// @directoryMethod
		Method string `spanner:"Method"`
		// @directoryConnection
		Connection string `spanner:"Connection"`
		// @directoryDefaultSeat
		Seat string `spanner:"Seat"`
		// @directoryDefaultSeat
		Rank string `spanner:"Rank"`
	}

	// Gangway declares @directorySecondFactor on a resource that is not the directories
	// record.
	//
	// @resource
	// @permissionScope(domain)
	Gangway struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		HarborID impulse.UUID `spanner:"HarborId"`
		// @directorySecondFactor
		Vouched bool `spanner:"Vouched"`
	}

	// Capstan is a directories record whose word for its directory's second factor is a
	// string.
	//
	// @resource
	// @permissionScope(domain)
	// @tenantDirectory
	Capstan struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		HarborID impulse.UUID `spanner:"HarborId"`
		// @directoryMethod
		Method string `spanner:"Method"`
		// @directoryConnection
		Connection string `spanner:"Connection"`
		// @directorySecondFactor
		Vouched string `spanner:"Vouched"`
	}

	// Cleat is a directories record declaring @directorySecondFactor on two columns.
	//
	// @resource
	// @permissionScope(domain)
	// @tenantDirectory
	Cleat struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		HarborID impulse.UUID `spanner:"HarborId"`
		// @directoryMethod
		Method string `spanner:"Method"`
		// @directoryConnection
		Connection string `spanner:"Connection"`
		// @directorySecondFactor
		Vouched bool `spanner:"Vouched"`
		// @directorySecondFactor
		Asks bool `spanner:"Asks"`
	}

	// Pier declares @directoryOnly on a resource that is not the tenant record.
	//
	// @resource
	Pier struct {
		ID impulse.UUID `spanner:"Id"`
		// @directoryOnly
		Closed bool `spanner:"Closed"`
	}

	// Quay declares @directoryOnly on a string column of a tenant record.
	//
	// @resource
	// @tenant
	Quay struct {
		ID impulse.UUID `spanner:"Id"`
		// @directoryOnly
		Mode string `spanner:"Mode"`
	}

	// Beacon declares @tenantDirectory on a global resource.
	//
	// @resource
	// @tenantDirectory
	Beacon struct {
		ID         impulse.UUID `spanner:"Id"`
		Method     string       `spanner:"Method"`
		Connection string       `spanner:"Connection"`
	}

	// Buoy declares @directoryMethod on a resource that is not the directories record.
	//
	// @resource
	// @permissionScope(domain)
	Buoy struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		HarborID impulse.UUID `spanner:"HarborId"`
		// @directoryMethod
		Method string `spanner:"Method"`
	}

	// Mooring is a directories record with no connection column.
	//
	// @resource
	// @permissionScope(domain)
	// @tenantDirectory
	Mooring struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		HarborID impulse.UUID `spanner:"HarborId"`
		// @directoryMethod
		Method     string `spanner:"Method"`
		Connection string `spanner:"Connection"`
	}

	// Jetty declares @directoryMethod on two columns.
	//
	// @resource
	// @permissionScope(domain)
	// @tenantDirectory
	Jetty struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		HarborID impulse.UUID `spanner:"HarborId"`
		// @directoryMethod
		Method string `spanner:"Method"`
		// @directoryMethod
		Kind string `spanner:"Kind"`
		// @directoryConnection
		Connection string `spanner:"Connection"`
	}

	// Anchorage is a directories record whose tenant is resolved through a join path,
	// through Berth's own hop.
	//
	// @resource
	// @permissionScope(domain)
	// @tenantDirectory
	Anchorage struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain(via: SectorID)
		BerthID impulse.UUID `spanner:"BerthId"`
		// @directoryMethod
		Method string `spanner:"Method"`
		// @directoryConnection
		Connection string `spanner:"Connection"`
	}

	// Lagoon is a tenant record whose people call one a service district
	// (@tenant(label: ...)) and that marks its name column (@tenantName), which the
	// session's tenant list carries.
	//
	// @resource
	// @tenant(label: "Service district")
	Lagoon struct {
		ID impulse.UUID `spanner:"Id"`
		// @tenantName
		Name string `spanner:"Name"`
	}

	// Reef marks a name on a resource that is not the tenant record.
	//
	// @resource
	Reef struct {
		ID impulse.UUID `spanner:"Id"`
		// @tenantName
		Name string `spanner:"Name"`
	}

	// Atoll marks its name on a column that is not a string.
	//
	// @resource
	// @tenant
	Atoll struct {
		ID impulse.UUID `spanner:"Id"`
		// @tenantName
		Rank int64 `spanner:"Rank"`
	}

	// Islet marks two name columns.
	//
	// @resource
	// @tenant
	Islet struct {
		ID impulse.UUID `spanner:"Id"`
		// @tenantName
		Name string `spanner:"Name"`
		// @tenantName
		Title *string `spanner:"Title"`
	}

	// Cove marks when a cove was made (@tenantSince) on a timestamp the database writes.
	//
	// @resource
	// @tenant
	Cove struct {
		ID impulse.UUID `spanner:"Id"`
		// @tenantSince
		MadeAt time.Time `spanner:"MadeAt" conditions:"output_only"`
	}

	// Creek marks when a creek was made on a nullable timestamp.
	//
	// @resource
	// @tenant
	Creek struct {
		ID impulse.UUID `spanner:"Id"`
		// @tenantSince
		Opened *time.Time `spanner:"Opened"`
	}

	// Strait marks when it was made on a resource that is not the tenant record.
	//
	// @resource
	Strait struct {
		ID impulse.UUID `spanner:"Id"`
		// @tenantSince
		MadeAt time.Time `spanner:"MadeAt"`
	}

	// Delta marks when a delta was made on a date, which is no timestamp.
	//
	// @resource
	// @tenant
	Delta struct {
		ID impulse.UUID `spanner:"Id"`
		// @tenantSince
		Founded civil.Date `spanner:"Founded"`
	}

	// Fjord marks when a fjord was made on a string.
	//
	// @resource
	// @tenant
	Fjord struct {
		ID impulse.UUID `spanner:"Id"`
		// @tenantSince
		Made string `spanner:"Made"`
	}

	// Sound marks two columns as when a sound was made.
	//
	// @resource
	// @tenant
	Sound struct {
		ID impulse.UUID `spanner:"Id"`
		// @tenantSince
		MadeAt time.Time `spanner:"MadeAt"`
		// @tenantSince
		ChartedAt *time.Time `spanner:"ChartedAt"`
	}

	// Shoal declares a label the screen cannot show as written.
	//
	// @resource
	// @tenant(label: "Shoal's")
	Shoal struct {
		ID impulse.UUID `spanner:"Id"`
	}
)
