package resources

import (
	"cloud.google.com/go/civil"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/resource"
)

type (
	// Sector is the tenant record (@tenant): the table whose rows are the sectors, the
	// permission domains every sector-scoped URL names. The route segment and the route
	// parameter derive from it (/console/api/sectors lists the sectors while
	// /console/api/sectors/{sectorID}/... serves the sector-scoped routes), and every
	// instance holds a roster of its keys (resource.TenantRoster, built by the generated
	// NewSectorRoster) that the generated DomainGuard asks before a sector-scoped request
	// runs. The application derives its domain universe from this table rather than a
	// fixed in-code list, and the roster keeps up without a restart: the generated paths
	// that make a sector and delete one add and remove it in the writing instance's
	// roster after the commit and signal the tenants kind, so every other instance
	// reloads at once, and a role held in every sector reaches the new sector with
	// nothing written for it. Charting a sector is a data change, not a release, a
	// migration or a new login.
	//
	// The primary key is an id the record's create generates, as every record's is, and the
	// generator refuses any other key on the tenant record: the id is written into every
	// row the sector owns and is the domain of every role held in it, so it never changes
	// and is never used twice. What people call the sector is its name, an ordinary field
	// whoever holds the record's Update grant changes; the unique index on the name
	// (schema/migrations) is what keeps two sectors from carrying one name, and an
	// application that allows that drops the index.
	//
	// A sector that is deleted leaves nothing of itself behind: the generated delete path
	// removes, in the transaction that deletes the row, the roles people held in the
	// sector, the custom roles made in it and the invitations into it that still stood,
	// in the crew's tables and the members' alike (each auth's generated identity client,
	// ClearTenant), and a person who held roles in that sector alone goes with it. No
	// role Lodestar ships holds Delete on Sectors; the suite gives one to prove it.
	//
	// Sector itself is a GLOBAL resource: administering the sector list is a global
	// concern. The place switcher lists only the sectors a login holds a grant in.
	//
	// Nobody creates a sector directly: a create operation on the record is refused,
	// saying how a sector is made. There are two ways, apart from who may enter one.
	// Headquarters and the surveyor bring an organization in by inviting one person
	// (POST /console/api/invite-crew-sector, the crew auth's generated InviteCrewSector):
	// the invitation carries the sector's details, the fields below as staff entered
	// them, checked against this record's create under the sender's Create grant on
	// Sectors when it is sent, and the role the people of the sector's directory start
	// with. The invited person's first sign-in makes the sector from those details
	// (the generated NewSectorMaker), seats them as its marshal and, where they signed
	// in through an organization's directory, makes that directory the sector's own, in
	// one transaction; the invited person enters nothing. Until then no sector exists,
	// the invitations that wait are listed with their details for whoever may read
	// them on this record (CrewInvitedSectors), and Headquarters changes one by
	// withdrawing it and sending another. An invitation that says directory only is
	// accepted through a directory alone, and a first sign-in through a directory
	// another sector has is refused; either way nothing is made and the invitation
	// stands. The other way is the pioneer's, one generated route (POST
	// /console/api/create-sector) for a person who signed in holding no sector, and names
	// their own. The pioneer's way is open while a fleet administrator has a sign-in
	// method of the crew auth open to sign-up (the console's Sign-in page), which is how
	// she comes to hold a login at all: through the sign-up registration, any Google
	// account. The route reads that in the transaction that writes the row, and refuses
	// while every method is invite only. It writes the row, seats the person with the
	// roles the App names (SectorAdministratorRoles: SectorMarshal) and hands the sector
	// to the roster, so their next request stands in it; a person who already holds a
	// sector is refused, the governor included, since an administrator of one sector
	// never creates another there.
	//
	// Who may enter a sector is the record's declaration, data of the application rather
	// than deploy configuration: the directory its people sign in through
	// (SectorDirectory, one row at most, set from a sign-in through the directory and
	// never typed in) and whether its people enter through it only (DirectoryOnly). A
	// sector has one directory and a directory belongs to one sector. The crew and the
	// members auths' sign-in rule reads the declaration in the sign-in's own transaction
	// (NewSectorDeclarationReader) and every instance's roster keeps a copy beside the
	// keys (NewSectorRoster, Declarations), which this record's and the directories'
	// generated write paths reload after their commit, so a sector is offered to a
	// session only when the session may enter it. A sector's declaration restricts its
	// own door alone: a session reaches every sector that seats the person, except a
	// sector that is directory only and whose directory is not the one they signed in
	// through. The navigator, through the fleet's directory (the seed makes it
	// Cinder's), reaches Cinder and every other sector that seats him and is not
	// directory only; the rigger, by password, reaches Anvil and not Bastion, which
	// admits its people through its directory only (the seed makes the persona
	// provider its directory) and stays on her chart marked closed, with the reason;
	// the bosun, whose only sector is Bastion, is refused at sign-in. The pioneer signs
	// up with a personal Google account, which is no organization's directory: the
	// create-sector route sets none on her Ember, and
	// Ember admits her as it admits a password. A person who charts a sector while
	// signed in through an organization's directory has it set on the new sector in
	// the row's transaction, and is refused when another sector has it. The record's
	// change events (DataChangeEvents, Config) are the audit of every edit.
	//
	// Demonstrates: tenancy.tenant-record, tenancy.run-time-tenant, tenancy.invited-tenant, tenancy.deleted-tenant, tenancy.concealed, tenancy.self-service, tenancy.declaration, tenancy.directory-only, tenancy.creator-directory, @order, @page.
	//
	// @resource
	// @tenant
	// @order(Name asc)
	// @page(default: 25, max: 200)
	Sector struct {
		ID impulse.UUID `spanner:"Id"`
		// Name is the sector's name as its crew know it: the session's sector list
		// carries it, so the place switcher shows it to them.
		// @tenantName
		Name        string     `spanner:"Name"`
		Region      string     `spanner:"Region"`
		Established civil.Date `spanner:"Established"`
		// DirectoryOnly says the sector's people enter through its directory only: no
		// other sign-in reaches the sector, and the flag closes this sector alone. The
		// switch is the sector's own: the sector's marshal (SectorMarshal) turns it with the
		// generated methods of the sector's scope: MakeSectorDirectoryOnly, which refuses
		// turning it on until the sector has its directory, and unless the caller is signed
		// in through it, and EndSectorDirectoryOnly. The record shows the flag and its own
		// write paths never write it. A sector is directory only from the start where the
		// invitation that makes it says so: that invitation is accepted through the
		// directory the sector will be only of and no other way, and the first sign-in
		// sets the flag with the directory.
		// @directoryOnly
		DirectoryOnly bool `spanner:"DirectoryOnly"`
	}
)

// Config enables change tracking: Sector mutations write DataChangeEvents rows in the
// same transaction, the audit of every change to what a sector declares.
func (Sector) Config() resource.Config {
	return defaultConfig().SetTrackChanges(true)
}
