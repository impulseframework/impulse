package resources

import (
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/resource"
)

type (
	// Tenant is the tenant record (@tenant): the global resource whose rows are the
	// tenants. Its route name is the segment the tenant-scoped routes are served under,
	// so /api/tenants lists the tenants while /api/tenants/{tenantID}/... serves them,
	// and its key is the domain in every tenant-scoped URL. The generator derives the
	// segment from it and emits NewTenantRoster, the constructor of the tenant roster the
	// data level builds and starts: the tenants as the generated guard knows them, kept
	// current on every instance by this record's generated write paths (a create or a
	// delete reaches the roster after its commit and publishes the tenants signal), so
	// a tenant created here is usable at once, without a restart. The application thus
	// derives its domain universe from this table rather than a fixed in-code list, and
	// a session's tenant list is the roster filtered by the engine's foothold answer
	// (resource.SessionPermissions). The roles need no row per tenant: a domain role is
	// held in every tenant domain.
	//
	// The primary key is an id the create path generates, as every record's is, and the
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
	// Who may create a tenant is decided in the running application, apart from who may
	// enter one. An administrator creates one through the create path above, under the
	// Administrator role's Create grant. A person who signed up creates their own
	// through one generated route, POST /api/create-tenant, while an administrator has a
	// sign-in method of the staff auth open to sign-up (the console's Sign-in page): a
	// stranger signs in first, through the staff auth's google registration (any Google
	// account) once it is open, holds no tenant, and names theirs; the route reads
	// whether sign-up is open in the transaction that writes the row, refuses while
	// every method is invite only, writes the row, seats the person with the roles
	// TenantAdministratorRoles names (app) and hands the tenant to the roster, so their
	// next request stands in it. A person who already holds a tenant is refused: one
	// tenant per person, and an administrator of one never creates another.
	//
	// Who may enter a tenant is the record's declaration, data of the application
	// rather than deploy configuration: the directory its people sign in through
	// (TenantDirectory, one row at most, set from a sign-in through the directory and
	// never typed in) and whether its people enter through it only (DirectoryOnly). The
	// sign-in policy reads it in the sign-in's own transaction
	// (NewTenantDeclarationReader, handed to the staff auth) and every instance's
	// roster keeps a copy beside the keys (NewTenantRoster, Declarations), which the
	// record's and the directories' generated write paths reload after their commit; a
	// tenant is offered to a session only when the session may enter it. The record's
	// change events (DataChangeEvents, Config) are the audit of every edit.
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
		// DirectoryOnly says the tenant's people enter through its directory only: a
		// password does not reach the tenant. Set by whoever holds the record's Update
		// grant on the column (Administrator), never at creation: a new tenant
		// declares nothing beyond its creator's directory.
		// @directoryOnly
		DirectoryOnly bool `spanner:"DirectoryOnly"`
	}
)

// Config enables change tracking: Tenant mutations write DataChangeEvents rows in the
// same transaction, the audit of every change to what a tenant declares.
func (Tenant) Config() resource.Config {
	return defaultConfig().SetTrackChanges(true)
}
