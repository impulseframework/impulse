package resources

import (
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/resource"
)

type (
	// SectorDirectory is a sector's directory (@tenantDirectory): the organization its
	// people sign in through, as the sign-in method the directory is reached through
	// (google, azure, workos or oidc, as the session library spells them) and the
	// connection the method's identities carry, which is what the session's first auth
	// event records: the fleet's Google Workspace by the name both auths' Google
	// registrations give it (example, the connection setting), a Microsoft Entra tenant
	// by its tenant id, an OpenID Connect provider by its registration's connection. A
	// sector has one directory and a directory belongs to one sector: the table's two
	// unique indexes hold both, and the generated write paths refuse first, each with a
	// sentence the person can act on. The seed makes the fleet's Workspace Cinder's, and
	// the persona provider of the test environments (oidc, persona) Bastion's, the
	// sector that is directory only.
	//
	// Nobody types a directory in. The row is written from a sign-in through the
	// directory: the first sign-in of the person invited to found a sector sets the one
	// they came through, with the role the invitation names for its people to start
	// with (a sector its creator charts takes none, unless the application holds a
	// person to one sector, where the create-sector route sets the one its creator came
	// through); and afterwards the sector's marshal, signed in through their organization's directory,
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
	// signing in. The record shows the role and its own update never writes it: the
	// marshal names it with the sector's own method OpenSectorToDirectory, behind the
	// warning the Sign-in page's dialog shows, and clears it with MakeSectorInviteOnly.
	// The role is held to one the marshal could give directly, so a marshal, who gives
	// every role of the sector but their own, opens the sector with no marshal's role.
	//
	// AsksSecondFactor says the sector's marshal has said the directory asks its people
	// for a second factor of its own. A fleet member who signs in through the directory
	// is then not asked for the mailed code, in this sector and in every sector that
	// gave them a role; the governor, and anyone who signs in with a password, are asked
	// as before. It is the sector's word and nothing checks it. The record shows it and
	// its own update never writes it: the marshal says it with the sector's own method
	// VouchForSectorDirectorySecondFactor, behind the warning the Sign-in page's dialog
	// shows, and takes it back with StopVouchingForSectorDirectorySecondFactor.
	//
	// The tenancy column is on the row itself (the bare @domain binding), as the
	// directories record requires: a directory is the sector's own declaration, stamped
	// from the request's sector on create, never written by the wire.
	//
	// Demonstrates: tenancy.declaration, tenancy.creator-directory, tenancy.default-seat, tenancy.directory-second-factor.
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
		// A directory is set without a seat: the create takes nothing from the request,
		// since the server sets the method and the connection from the session.
		//
		// @directoryDefaultSeat
		DefaultSeat *string `spanner:"DefaultSeat"`
		// @directorySecondFactor
		AsksSecondFactor bool `spanner:"AsksSecondFactor"`
	}
)

// Config enables change tracking: SectorDirectory mutations write DataChangeEvents rows
// in the same transaction, the audit of every directory set or removed.
func (SectorDirectory) Config() resource.Config {
	return defaultConfig().SetTrackChanges(true)
}
