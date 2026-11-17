package resources

import (
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/resource"
)

type (
	// SectorDirectory is a sector's directory (@tenantDirectory): the organization its
	// people sign in through, as the sign-in method the directory is reached through
	// (google, azure or workos, as the session library spells them) and the connection
	// the method's identities carry, which is what the session's first auth event
	// records: the fleet's Google Workspace by the name both auths' Google registrations
	// give it (example, the connection setting), a Microsoft Entra tenant by its tenant
	// id. A sector has one directory and a directory belongs to one sector: the table's
	// two unique indexes hold both, and the generated write paths refuse first, each
	// with a sentence the person can act on. The seed makes the fleet's Workspace
	// Cinder's.
	//
	// Nobody types a directory in. The row is written from a sign-in through the
	// directory: the create-sector route sets the one its creator came through, and
	// afterwards the sector's marshal, signed in through their organization's directory,
	// makes it the sector's on the Sign-in page, under the record's Create grant
	// (SectorMarshal; the governor holds it in every sector). The create takes no method
	// and no connection from the request; the server reads both from the session. A
	// personal account (the pioneer's Google account, a Microsoft personal account) is
	// no organization's directory and is refused, as is a session that came by
	// password. The marshal removes the directory under the Delete grant, which is
	// refused while the sector is directory only; the roles it gave stand, and the
	// directory is free for another sector. The change events are the audit.
	//
	// DefaultSeat is the role a fleet member is given in the sector at their first
	// sign-in through the directory, whether their account is created then or stood
	// before (Cinder gives SectorCrew); or NULL, and the sector admits the directory's
	// people by invitation only: a stranger is refused with no account, and an
	// invitation is the way in. It is given once for the link between the account and
	// the directory: a person whose role a marshal removed is not given it back by
	// signing in. The marshal sets it on the Sign-in page, a picker over the sector's
	// roles.
	//
	// The tenancy column is on the row itself (the bare @domain binding), as the
	// directories record requires: a directory is the sector's own declaration, stamped
	// from the request's sector on create, never written by the wire.
	//
	// Demonstrates: tenancy.declaration, tenancy.creator-directory, tenancy.default-seat.
	//
	// @resource
	// @permissionScope(domain)
	// @tenantDirectory
	// @order(Method asc, Connection asc)
	SectorDirectory struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		SectorID impulse.UUID `spanner:"SectorId"`
		// @directoryMethod
		Method string `spanner:"Method"`
		// @directoryConnection
		Connection string `spanner:"Connection"`
		// The role is an attribute a grant's condition reads: the marshal's Update grant
		// names no role a marshal may not give (new.defaultSeat), as the grants on the
		// methods that give a role do. A directory is set without a seat (the marshal's
		// Create grant carries no field: the server sets the method and the connection
		// from the session), so every seat is named by an update, under that limit.
		//
		// @attribute(defaultSeat)
		// @directoryDefaultSeat
		DefaultSeat *string `spanner:"DefaultSeat"`
	}
)

// Config enables change tracking: SectorDirectory mutations write DataChangeEvents rows
// in the same transaction, the audit of every directory set or removed.
func (SectorDirectory) Config() resource.Config {
	return defaultConfig().SetTrackChanges(true)
}
