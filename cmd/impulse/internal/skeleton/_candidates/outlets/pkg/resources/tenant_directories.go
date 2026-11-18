package resources

import (
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/resource"
)

type (
	// TenantDirectory is a tenant's directory (@tenantDirectory): the organization its
	// people sign in through, as the sign-in method the directory is reached through
	// (google, azure or workos, as the session library spells them) and the connection
	// the method's identities carry, which is what the session's first auth event
	// records: a Microsoft Entra tenant by its tenant id (the portal's members come
	// through the Entra directory the members auth registers, which the development seed
	// makes North's), a Google Workspace by its domain. A tenant has one directory and a
	// directory belongs to one tenant: the table's two unique indexes hold both, and the
	// generated write paths refuse first, each with a sentence the person can act on.
	//
	// Nobody types a directory in. The row is written from a sign-in through the
	// directory: an invitation that makes a tenant, accepted through the person's
	// organization's directory, sets it as the tenant is made, and the tenant's
	// administrator, signed in through their organization's directory, makes it the
	// tenant's on the console's Sign-in page, under the record's Create grant
	// (TenantAdministrator). The create takes no method and no connection
	// from the request; the server reads both from the session. The staff auth signs in
	// by password until a directory method joins it (impulse add method), and a session
	// that came by password is refused, as is a personal account (a Google account
	// outside every Workspace, a Microsoft personal account), which is no organization's
	// directory. The administrator removes the directory under the Delete grant, which
	// is refused while the tenant is directory only; the roles it gave stand, and the
	// directory is free for another tenant. The change events are the audit.
	//
	// DefaultSeat is the role a person is given in the tenant at their first sign-in
	// through the directory, whether their account is created then or stood before; or
	// NULL, and the tenant admits the directory's people by invitation only (a stranger
	// is refused with no account, and an invitation is the way in). It is given once
	// for the link between the account and the directory: a person whose role an
	// administrator removed is not given it back by signing in. The record shows it and
	// its own update never writes it: the tenant's own methods name it and clear it.
	//
	// The tenancy column is on the row itself (the bare @domain binding), as the
	// directories record requires: a directory is the tenant's own declaration, stamped
	// from the request's tenant on create, never written by the wire. The record is on
	// the console alone, like the tenant record it declares for.
	//
	// @resource
	// @permissionScope(domain)
	// @tenantDirectory
	// @order(Method asc, Connection asc)
	TenantDirectory struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		TenantID impulse.UUID `spanner:"TenantId"`
		// @directoryMethod
		Method string `spanner:"Method"`
		// @directoryConnection
		Connection string `spanner:"Connection"`
		// @directoryDefaultSeat
		DefaultSeat *string `spanner:"DefaultSeat"`
	}
)

// Config enables change tracking: TenantDirectory mutations write DataChangeEvents rows
// in the same transaction, the audit of every directory set or removed.
func (TenantDirectory) Config() resource.Config {
	return defaultConfig().SetTrackChanges(true)
}
