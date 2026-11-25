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
	// records: a Google Workspace by its domain (the staff auth's google registration
	// admits any Workspace and writes the account's domain), a Microsoft Entra tenant by
	// its tenant id. A tenant has one directory and a directory belongs to one tenant:
	// the table's two unique indexes hold both, and the generated write paths refuse
	// first, each with a sentence the person can act on.
	//
	// Nobody types a directory in. The row is written from a sign-in through the
	// directory: an invitation that makes a tenant, accepted through the person's
	// organization's directory, sets it as the tenant is made (a tenant created through
	// the create-your-tenant route takes none), and afterwards the tenant's
	// administrator, signed in through their organization's directory, makes it the
	// tenant's on the Sign-in page with the tenant's own method TakeTenantDirectory,
	// under its Execute grant (TenantAdministrator), after a statement that they are
	// authorized to act on behalf of the organization, under which they type a
	// phrase; the record's own create is refused. The method takes nothing from the
	// request; the server reads the method and the connection from the session. A
	// personal account (a Google account outside every Workspace, a Microsoft
	// personal account) is no organization's directory and is refused, as is a
	// session that came by password. The administrator removes the directory under
	// the Delete grant, which is refused while the tenant is directory only; the roles
	// it gave stand, and the directory is free for another tenant. The change events
	// are the audit.
	// A tenant that is deleted takes its directory with it, directory only or not: the
	// directory is a row under the tenant, and the tenant's delete takes every row under
	// it in the same transaction, each through its kind's own delete (Tenant.DeleteTree).
	//
	// DefaultSeat is the role a person is given in the tenant at their first sign-in
	// through the directory, whether their account is created then or stood before; or
	// NULL, and the tenant admits the directory's people by invitation only (a stranger
	// is refused with no account, and an invitation is the way in). It is given once
	// for the link between the account and the directory: a person whose role an
	// administrator removed is not given it back by signing in. The record shows it and
	// its own update never writes it: the tenant's own methods name it and clear it.
	//
	// AsksSecondFactor says the tenant's administrator has said the directory asks its
	// people for a second factor of its own. A person who signs in through the directory
	// is then not asked for the mailed code, in this tenant and in every tenant that gave
	// them a role; a super administrator, and anyone who signs in with a password, are
	// asked as before. It is the tenant's word and nothing checks it. The record shows it
	// and its own update never writes it: the tenant's own methods say it and take it
	// back.
	//
	// The tenancy column is on the row itself (the bare @domain binding), as the
	// directories record requires: a directory is the tenant's own declaration, stamped
	// from the request's tenant on create, never written by the wire.
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
		// @directorySecondFactor
		AsksSecondFactor bool `spanner:"AsksSecondFactor"`
	}
)

// Config enables change tracking: TenantDirectory mutations write DataChangeEvents rows
// in the same transaction, the audit of every directory set or removed.
func (TenantDirectory) Config() resource.Config {
	return defaultConfig().SetTrackChanges(true)
}
