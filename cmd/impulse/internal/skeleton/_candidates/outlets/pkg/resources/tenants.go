package resources

import (
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/resource"
)

type (
	// Tenant is the tenant record (@tenant): the global resource whose rows are the
	// tenants. Its route name is the segment the tenant-scoped routes are served under,
	// so /console/api/tenants lists the tenants while /console/api/tenants/{tenantID}/...
	// serves them (each outlet under its own prefix), and its key is the domain in every
	// tenant-scoped URL. The generator derives the segment from it and emits
	// NewTenantRoster, the constructor of the tenant roster the data level builds and
	// starts: the tenants as the generated guard knows them, kept current on every
	// instance by what writes this record (a tenant an invitation's first sign-in makes,
	// a rename or a delete reaches the roster after its commit and publishes the tenants
	// signal), so a new tenant is usable at once, without a restart. The application thus derives its domain
	// universe from this table rather than a fixed in-code list, and a session's tenant
	// list is the roster filtered by the foothold answer of the engine of the auth the
	// session came through (resource.SessionPermissions). The roles need no row per
	// tenant: a domain role is held in every tenant domain.
	//
	// The primary key is an id generated when the tenant is made, as every record's is, and the
	// generator refuses any other key on the tenant record: the id is written into
	// every row the tenant owns and is the domain of every role held in it, so it never
	// changes and is never used twice. What people call the tenant is its name, an
	// ordinary field its administrators change; the unique index on the name
	// (schema/migrations) is what keeps two tenants from carrying one name, and an
	// application that allows that drops the index.
	//
	// Tenant itself is a GLOBAL resource — administering the tenant list is a global
	// concern. Its list comes in name order (@order) when the request names no sort.
	//
	// How a tenant comes to exist is apart from who may enter one. Nobody creates a
	// tenant directly: the record's create is refused, saying so. An administrator
	// brings an organization in by inviting one person (the staff auth's generated
	// InviteStaffTenant, on the console's Invited tenants page): the invitation carries
	// the tenant's details as the administrator entered them, held to this record's own
	// create under the Administrator role's Create grant when it is sent, and the
	// invited person's first sign-in makes the tenant, seats them as its administrator
	// and, where they came through their organization's directory, makes that directory
	// the tenant's. A person who signed up creates their own through one generated
	// route, POST /console/api/create-tenant, while an administrator has a sign-in method of
	// the staff auth open to sign-up.
	//
	// Who may enter a tenant is the record's declaration, data of the application
	// rather than deploy configuration: the directory its people sign in through
	// (TenantDirectory, one row at most, set from a sign-in through the directory and
	// never typed in) and whether its people enter through it only (DirectoryOnly).
	// Each auth's sign-in policy reads it in the sign-in's own transaction
	// (NewTenantDeclarationReader, handed to the staff and the members auth) and every
	// instance's roster keeps a copy beside the keys (NewTenantRoster, Declarations),
	// which the record's and the directories' generated write paths reload after their
	// commit; a tenant is offered to a session only when the session may enter it. The
	// record's change events (DataChangeEvents, Config) are the audit of every edit.
	//
	// @resource
	// @tenant
	// @order(Name asc)
	Tenant struct {
		ID impulse.UUID `spanner:"Id"`
		// Name is the tenant's name as its people know it: the session's tenant list
		// carries it, so the place switcher shows it to them.
		// @tenantName
		Name string `spanner:"Name"`
		// DirectoryOnly says the tenant's people enter through its directory only: no
		// other sign-in reaches the tenant, and the flag closes this tenant alone. The
		// switch is the tenant's own: the tenant's administrator (TenantAdministrator)
		// turns it with the generated methods of the tenant's scope: MakeTenantDirectoryOnly,
		// which refuses turning it on until the tenant has its directory, and unless the
		// caller is signed in through it, and EndTenantDirectoryOnly. The record shows the
		// flag and its own write paths never write it. A tenant is directory only from the
		// start where the invitation that makes it says so: that invitation is accepted
		// through the directory the tenant will be only of and no other way.
		// @directoryOnly
		DirectoryOnly bool `spanner:"DirectoryOnly"`
	}
)

// Config enables change tracking: Tenant mutations write DataChangeEvents rows in the
// same transaction, the audit of every change to what a tenant declares.
func (Tenant) Config() resource.Config {
	return defaultConfig().SetTrackChanges(true)
}
