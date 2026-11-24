// Package identity holds the rules of managing the people of one auth: inviting a
// person, disabling one and ending their sessions, seating one in a role. An
// application does not call it directly. The code generator writes, for every auth an
// application declares, the resources over the auth's own tables and the methods that
// change them; each method's body is one call into this package, so the rules are
// compiled and tested once and are the same for every auth of every application.
//
// How a tenant comes to exist is among the rules. Nobody creates a tenant directly: staff
// of the auth a tenant is made through invite one person with the tenant's details
// (Manager.InviteTenant, the details held to the tenant record's own create for the
// sender by Manager.CheckInvitedTenant), and that person's first sign-in makes the
// tenant through the application's generated maker (TenantMaker), seats them with the
// roles that administer a tenant and, where they came through their organization's
// directory, makes it the tenant's own, in the transaction that consumes the invitation.
// Manager.InvitedTenants lists the invitations that still stand, and
// Manager.WithdrawTenantInvitation withdraws one.
//
// Every rule runs inside the caller's transaction (resource.ReadWriteTransaction): a
// generated handler's, or one an application opened to write its own rows beside the
// change and commit them together. Nothing here commits, and nothing reads a row a sibling
// call buffered in the same transaction, which Spanner applies only at the commit: a
// rule takes what it needs from its caller.
//
// The sign-in paths do not come through here. The session library reads and writes the
// same tables in its own storage, on its own transactions, and is unchanged by this
// package; what couples the two is the schema, the few rules both hold (each marked
// "shared rule" where this package holds its half), and the word to the permission
// engine once a membership is committed.
package identity

import (
	"context"
	"slices"
	"sync/atomic"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// Store is what the rules need of one auth's tables: single reads and single buffered
// writes, which the generated package of the auth implements over its own typed queries
// and patches. An implementation holds no rule: it maps the values it is given onto the
// auth's columns and answers what it read.
type Store interface {
	// UpdateUser buffers a change to a user: every field of change that is set.
	UpdateUser(ctx context.Context, txn resource.ReadWriteTransaction, id impulse.UUID, change *UserChange) error
	// RemovePassword buffers the removal of a user's password, and with it the mark that
	// the person must change it. An auth with no password method has no such write.
	RemovePassword(ctx context.Context, txn resource.ReadWriteTransaction, id impulse.UUID) error
	// ClearAttempts buffers the removal of the count of failed sign-ins kept under a
	// name (session.SignInAttemptName); a name with no count is no error.
	ClearAttempts(ctx context.Context, txn resource.ReadWriteTransaction, name string) error
	// UserHasPassword reads whether a user holds a password; found is false where no
	// user holds the id. The hash itself is read by nothing. It writes nothing, so a
	// read outside a write asks it too (SignInWays).
	UserHasPassword(ctx context.Context, txn resource.ReadOnlyTransaction, id impulse.UUID) (has, found bool, err error)

	// IdentityUser reads the user an identity links to; found is false where no
	// identity holds the id. An auth that keeps no identities finds none.
	IdentityUser(ctx context.Context, txn resource.ReadWriteTransaction, id impulse.UUID) (userID impulse.UUID, found bool, err error)
	// Identities lists the identities linked to a user, each with the sign-in method
	// and the connection it came through, which say whether it can still sign the
	// person in. It writes nothing, so a read outside a write asks it too (SignInWays).
	Identities(ctx context.Context, txn resource.ReadOnlyTransaction, userID impulse.UUID) ([]Link, error)
	// ManagingTenants lists the tenants whose own list of their people says they manage
	// the user's account (managedHere on the user's row of the tenant's list), as the
	// transaction reads it. A removal that would leave a person holding nothing asks it:
	// an account another tenant manages is not deleted with what one tenant takes. A
	// schema with no tenant record, a tenant record that declares no directory and an
	// auth that keeps no identities answer none, since there no tenant manages an
	// account that holds nothing in it.
	ManagingTenants(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) ([]accesstypes.Domain, error)
	// DeleteIdentity buffers the removal of an identity.
	DeleteIdentity(ctx context.Context, txn resource.ReadWriteTransaction, id impulse.UUID) error

	// Token reads a bearer token; found is false where no token holds the id. An auth
	// that issues no tokens finds none.
	Token(ctx context.Context, txn resource.ReadWriteTransaction, id impulse.UUID) (token *Token, found bool, err error)
	// FamilyTokens lists the tokens of a family that are not revoked.
	FamilyTokens(ctx context.Context, txn resource.ReadWriteTransaction, family impulse.UUID) ([]impulse.UUID, error)
	// RevokeToken buffers the revocation of one token, with the reason.
	RevokeToken(ctx context.Context, txn resource.ReadWriteTransaction, id impulse.UUID, reason string) error

	// Username reads a user's username; found is false where no user holds the id.
	Username(ctx context.Context, txn resource.ReadWriteTransaction, id impulse.UUID) (username string, found bool, err error)
	// UserDisabled reads whether a user is disabled; found is false where no user holds
	// the id. The rule on the last super administrator reads it: a holder of the role
	// who cannot sign in is nobody's way back in.
	UserDisabled(ctx context.Context, txn resource.ReadWriteTransaction, id impulse.UUID) (disabled, found bool, err error)
	// UserID reads the id of the user who holds a username, compared as the auth
	// compares names at a sign-in; found is false where none does. It is how a new
	// name is found taken. The permission store names a person by the id of their
	// account; the username stays on what carries it for reading, the person's live
	// sessions and the impersonation records they hold as actor, which a rename
	// rewrites (RenameUser).
	UserID(ctx context.Context, txn resource.ReadWriteTransaction, username string) (id impulse.UUID, found bool, err error)
	// UserByVerifiedEmail answers the id of the user who has confirmed the address,
	// compared as the table's unique index compares addresses; found is false when no
	// user holds it, and when the one who does has not confirmed it.
	UserByVerifiedEmail(ctx context.Context, txn resource.ReadWriteTransaction, email string) (userID impulse.UUID, found bool, err error)
	// ConfirmedEmail answers the address the user has confirmed, and found false for a
	// user who has confirmed none (an address nobody confirmed is not answered) and
	// for no such user.
	ConfirmedEmail(ctx context.Context, txn resource.ReadWriteTransaction, id impulse.UUID) (email string, found bool, err error)
	// DeleteUser buffers the removal of a user row.
	DeleteUser(ctx context.Context, txn resource.ReadWriteTransaction, id impulse.UUID) error

	// LiveSessions lists the sessions of the user that have not ended.
	LiveSessions(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) ([]impulse.UUID, error)
	// SessionExpired reads whether a session has ended; found is false where no
	// session holds the id.
	SessionExpired(ctx context.Context, txn resource.ReadWriteTransaction, id impulse.UUID) (expired, found bool, err error)
	// EndSession buffers the end of one session.
	EndSession(ctx context.Context, txn resource.ReadWriteTransaction, id impulse.UUID) error
	// RenameSession buffers a new username on one session.
	RenameSession(ctx context.Context, txn resource.ReadWriteTransaction, id impulse.UUID, username string) error

	// Impersonation reads the impersonation record of a session: found is false where
	// the session has none, and live says a record that is found has not ended. An
	// auth that keeps no such records finds none.
	Impersonation(ctx context.Context, txn resource.ReadWriteTransaction, sessionID impulse.UUID) (live, found bool, err error)
	// ImpersonationsHeldBy lists the sessions whose record has not ended and names the
	// username as its actor, a person of this auth (the record's realm is empty). An
	// auth that keeps no such records answers none.
	ImpersonationsHeldBy(ctx context.Context, txn resource.ReadWriteTransaction, username string) ([]HeldImpersonation, error)
	// RenameImpersonationActor buffers a new actor's name on a session's record.
	RenameImpersonationActor(ctx context.Context, txn resource.ReadWriteTransaction, sessionID impulse.UUID, username string) error
	// EndImpersonation buffers the end of a session's impersonation record, with the
	// reason it ended.
	EndImpersonation(ctx context.Context, txn resource.ReadWriteTransaction, sessionID impulse.UUID, reason string) error

	// HasRole reports whether the roles table holds the row of a custom role, read in
	// the transaction.
	HasRole(ctx context.Context, txn resource.ReadWriteTransaction, key *access.RoleKey) (bool, error)
	// HasMembership reports whether the membership row is committed.
	HasMembership(ctx context.Context, txn resource.ReadWriteTransaction, key *access.MembershipKey) (bool, error)
	// Memberships lists every membership the user holds, wherever it is held. A
	// membership names its person by the id of their account (User).
	Memberships(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) ([]access.MembershipKey, error)
	// RoleHolders lists who holds the role where the key says it is held, each by the
	// id of their account.
	RoleHolders(ctx context.Context, txn resource.ReadWriteTransaction, key *access.RoleKey) ([]accesstypes.User, error)
	// InsertMembership buffers a membership row unless the membership is there already,
	// committed or written earlier in this transaction, and reports whether it did. A
	// membership that is there is left exactly as it is, the time it was given included.
	InsertMembership(ctx context.Context, txn resource.ReadWriteTransaction, key *access.MembershipKey) (inserted bool, err error)
	// DeleteMembership buffers the removal of a membership row.
	DeleteMembership(ctx context.Context, txn resource.ReadWriteTransaction, key *access.MembershipKey) error
	// TenantMemberships lists every membership held in one tenant, whoever holds it:
	// the rows of the memberships table under the tenant's own scope. A membership
	// held in every tenant is no row of one tenant and is not listed.
	TenantMemberships(ctx context.Context, txn resource.ReadWriteTransaction, domain accesstypes.Domain) ([]access.MembershipKey, error)
	// DeleteTenantRoles buffers the removal of every custom role made in one tenant
	// with its grants: the rows of the roles table and of the grants table under the
	// tenant's own scope. It answers how many roles go.
	DeleteTenantRoles(ctx context.Context, txn resource.ReadWriteTransaction, domain accesstypes.Domain) (roles int, err error)

	// InsertInvitation buffers the mail links row of a prepared invitation, as the
	// session library says one is written (session.InvitationRow).
	InsertInvitation(ctx context.Context, txn resource.ReadWriteTransaction, row *session.InvitationRow) error
	// MailLink reads one link the auth mailed; found is false where no link holds the
	// id.
	MailLink(ctx context.Context, txn resource.ReadWriteTransaction, id impulse.UUID) (link *MailLink, found bool, err error)
	// ConsumeMailLink buffers the consumption of one link: it is then dead.
	ConsumeMailLink(ctx context.Context, txn resource.ReadWriteTransaction, id impulse.UUID) error
	// TenantInvitations lists the invitations into one tenant that are not consumed:
	// neither accepted nor withdrawn. An auth that mails no links answers none.
	TenantInvitations(ctx context.Context, txn resource.ReadWriteTransaction, domain accesstypes.Domain) ([]impulse.UUID, error)
	// InvitedTenants lists the invitations that make a tenant and are not consumed,
	// neither accepted nor withdrawn, oldest first, each with the document its
	// NewTenant column holds. reader is the caller's transaction or the application's
	// client. An auth that mails no links answers none.
	InvitedTenants(ctx context.Context, reader resource.ReadOnlyTransaction) ([]InvitedTenantRow, error)

	// SignInMethodRows lists the rows of the sign-in methods table: what an
	// administrator chose for each method that has a row. reader is the caller's
	// transaction or the application's client. A schema that holds no such table
	// answers none, which is every method invite only.
	SignInMethodRows(ctx context.Context, reader resource.ReadOnlyTransaction) ([]SignInMethodRow, error)
	// SetSignInMethod buffers who may sign up through the method the row names
	// (OpenSignUp, OpenedFor, DefaultRole), the three written together: the row is
	// created where the method has none, and those three replaced where it has one.
	// SelfServiceReset is not read from the row and stands as it is. The write keeps
	// who made it and when (the table's change events).
	SetSignInMethod(ctx context.Context, txn resource.ReadWriteTransaction, row *SignInMethodRow) error
	// SetSelfServiceReset writes whether a person may ask for their own password reset
	// link on the row of the sign-in method of the name, and nothing else of the row:
	// a method with no row gets one that is invite only.
	SetSelfServiceReset(ctx context.Context, txn resource.ReadWriteTransaction, name string, on bool) error
	// SignInSettingsRow reads the row of the sign-in settings table: what an
	// administrator chose for every sign-in of the auth. reader is the caller's
	// transaction or the application's client. It answers nil where there is no row,
	// and a schema that holds no such table answers nil.
	SignInSettingsRow(ctx context.Context, reader resource.ReadOnlyTransaction) (*SignInSettingsRow, error)
	// SetRequireSecondFactor writes whether everyone is asked for a second factor on the
	// row, and nothing else of it: where there is no row, one is created with the days a
	// browser is remembered at session.DefaultRememberedBrowserDays. The write keeps who
	// made it and when (the table's change events).
	SetRequireSecondFactor(ctx context.Context, txn resource.ReadWriteTransaction, on bool) error
	// SetRememberBrowserDays writes for how many days a remembered browser skips the
	// second factor on the row, and nothing else of it: where there is no row, one is
	// created with the second factor required. The write keeps who made it and when.
	SetRememberBrowserDays(ctx context.Context, txn resource.ReadWriteTransaction, days int64) error

	// TrustedDevices lists the browsers the user asked to be remembered after the
	// second factor that are not revoked. An auth that keeps none answers none.
	TrustedDevices(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) ([]impulse.UUID, error)
	// RevokeTrustedDevice buffers the revocation of one remembered browser.
	RevokeTrustedDevice(ctx context.Context, txn resource.ReadWriteTransaction, id impulse.UUID) error

	// GroupStore is the auth's groups of people, their members and the roles they hold
	// (groups.go).
	GroupStore
	// PeriodStore is the period of an assignment: a person's role, a person's place in
	// a group and a group's role (periods.go).
	PeriodStore
}

// SessionAPI is what the rules take from the session library's Auth, the one that signs
// the auth's people in: the API of the application's own Auth satisfies it
// (session.Auth.API). No rule here writes a password: a person chooses their own, from
// an invitation or a reset link, in the session library's storage.
type SessionAPI interface {
	// PrepareInvitation checks and makes an invitation without storing or mailing it:
	// the row to store, and the step that mails its link once the row is committed.
	// The token, the message and the sender stay in the session library.
	PrepareInvitation(ctx context.Context, req *session.InvitationRequest) (session.PreparedInvitation, error)
	// SignInMethods lists the sign-in methods the Auth registers, in the login page's
	// order: each with how wide it is and whether its directory gives the roles.
	SignInMethods() []session.RegisteredMethod
	// SignsInThrough names the sign-in registrations of the Auth's login page that an
	// identity of this method and connection can sign its person in through, and none
	// for an identity that signs nobody in: its method is no registration the page
	// offers any more, or no registration of the method takes its connection. It is the
	// one judgment of which links count as a means of sign-in, the session library's
	// own (session.AuthAPI.SignsInThrough): the removal of a password is refused, and
	// offered, by it.
	SignsInThrough(method sessioninfo.AuthMethod, connection string) []string
	// DirectoryName answers what the Auth's sign-in registrations call the directory an
	// identity of this method and connection came through: the name a registration that
	// takes the connection gives it, and the label of the button a person signs in to
	// it with, each "" where the registrations have none
	// (session.AuthAPI.DirectoryName). A sentence that names a directory to a person
	// says it by them, never by the connection.
	DirectoryName(method sessioninfo.AuthMethod, connection string) (name, label string)
	// TenantName answers what people call a tenant, by its key, and "" where the
	// application named none or does not know this one (session.AuthAPI.TenantName). A
	// sentence that names a tenant to a person says it by this, never by the key.
	TenantName(tenant accesstypes.Domain) string
	// SessionTimeout answers how long a session may sit idle before it times out
	// (session.AuthAPI.SessionTimeout). The generated list of the Auth's sessions says
	// which of them are live by it.
	SessionTimeout() time.Duration
	// RequestAddressChange mails the link that confirms a new address for the user, to
	// that address, and changes nothing: the account keeps the address it has until the
	// link is followed (session.AuthAPI.RequestAddressChange). The message says who
	// asked (by).
	RequestAddressChange(ctx context.Context, userID impulse.UUID, email string, by session.AskedBy) error
	// SendPasswordReset mails the user a link that resets their password, to the address
	// they confirmed, and changes nothing until the link is used
	// (session.AuthAPI.SendPasswordReset). The message says who asked (by).
	SendPasswordReset(ctx context.Context, userID impulse.UUID, by session.AskedBy) error
	// EndPasswordResetLinks withdraws the user's standing password reset links, once a
	// write or a removal of their password here has committed: a link mailed for the
	// password before does not reset the one after
	// (session.AuthAPI.EndPasswordResetLinks).
	EndPasswordResetLinks(ctx context.Context, userID impulse.UUID) error
	// NoticeFactorReset tells the user, at the address they confirmed, that an
	// administrator reset their second factor (session.AuthAPI.NoticeFactorReset).
	NoticeFactorReset(ctx context.Context, userID impulse.UUID) error
	// NoticeRemovedFromTenant tells a person, at the confirmed address read before the
	// removal, that an administrator removed them from the tenant, and says so when
	// their account went with it (session.AuthAPI.NoticeRemovedFromTenant).
	NoticeRemovedFromTenant(ctx context.Context, to, tenant string, accountDeleted bool) error
}

// Link is one identity linked to a user, as the rules read it: its id, and the sign-in
// method and the connection it came through (the identities table's Method and
// Connection), which say whether a sign-in through it is still possible.
type Link struct {
	ID         impulse.UUID
	Method     sessioninfo.AuthMethod
	Connection string
}

// MailLink is a link the auth mailed, as the rules read it.
type MailLink struct {
	// Invitation says the link is an invitation (the others are an address's
	// verification and a sign-up).
	Invitation bool
	// Consumed says the link was followed or withdrawn, and ExpiresAt is when it stops
	// being accepted.
	Consumed  bool
	ExpiresAt time.Time
}

// HeldImpersonation is one standing impersonation record a person holds as its actor.
type HeldImpersonation struct {
	// SessionID is the session the record belongs to.
	SessionID impulse.UUID
	// ActsAsRole marks a record whose session acts as a role: such a session carries
	// the actor's own username, where one that views as a user carries that user's.
	ActsAsRole bool
}

// UserChange is a change to a user row: a nil field is left as it is. The address is
// not among them: an address reaches a row confirmed, through the session library, or
// not at all (Manager.SetEmail).
type UserChange struct {
	Username               *string
	Disabled               *bool
	PasswordChangeRequired *bool
	// Name, where set, replaces the name the row holds.
	Name *NameChange
}

// NameChange is the name a change writes on a user row: Name, nil for none.
type NameChange struct {
	Name *string
}

// Token is a bearer token as the rules read it.
type Token struct {
	// UserID is the account the token signs in as, the person a revocation is done to.
	UserID impulse.UUID
	// FamilyID is the device authorization the token belongs to, nil for a token that
	// stands alone.
	FamilyID *impulse.UUID
	// ExpiresAt is when the token stops being accepted, and Revoked whether it was
	// revoked before that.
	ExpiresAt time.Time
	Revoked   bool
}

// Manager applies the rules to one auth: over the store of its tables, with the
// permission engine that reads its memberships.
type Manager struct {
	// auth is the auth's name, as its outlets' generated routes bind it to a request.
	auth     string
	store    Store
	access   access.Controller
	sessions SessionAPI
	// administrators are the roles whose holders administer a tenant, nil where the
	// application named none (WithAdministratorRoles). The application as a whole is
	// administered by the holders of the Super Administrator role, which needs no naming.
	administrators *administratorRoles
	// firstAdministrator are the roles the first administrator's invitation gives, by
	// where they are held; nil where the application named none
	// (WithFirstAdministratorRoles), and the invitation then gives every role in the
	// one place it stores, as any other does.
	firstAdministrator *firstAdministratorRoles
	// superMethods are the methods that give and take the auth's roles for the whole
	// application, which the permission engine must let a super administrator execute
	// (WithSuperAdministratorMethods); New holds the engine to it.
	superMethods []accesstypes.Resource
	// oneTenant is set where a person belongs to one tenant (WithOneTenantPerPerson),
	// and holds what the hooks of a sign-in have given so far; nil where a person may
	// hold roles in several tenants.
	oneTenant *signInTenants
	// tenantWord is what the application calls one tenant, as a sentence a person reads
	// says it ("customer"); "tenant" where the application named none (WithTenantWord).
	tenantWord string
	// grants answer what the request's caller may do to a person's role, which the
	// rules of a group ask (WithRoleGrants); nil where the manager was built without
	// them.
	grants RoleGrants
	// tenants makes a tenant from an accepted invitation (WithTenantMaker); nil where
	// the auth's people create none that way.
	tenants TenantMaker
	// tables are the names of the auth's tables a sign-in writes, as the live pages know
	// them (WithAccountTables); a table left unnamed is not announced.
	tables AccountTables
	// announcer is the function that publishes what a sign-in wrote to the open pages
	// (AnnounceWith), nil until one is handed over.
	announcer atomic.Pointer[announceFunc]
	// now is the application's clock, which the session library holds a deadline
	// against (a token's expiry).
	now func() time.Time
}

// administratorRoles are the roles whose holders administer a tenant.
type administratorRoles struct {
	domain []accesstypes.Role
}

// firstAdministratorRoles are the roles the first administrator's invitation gives, by
// where they are held: global for the whole application, everyDomain in every tenant.
type firstAdministratorRoles struct {
	global      []accesstypes.Role
	everyDomain []accesstypes.Role
}

// Option configures a manager.
type Option func(*Manager)

// WithAdministratorRoles names the roles whose holders administer a tenant: the domain
// roles a person holds in the tenant itself, by a membership of their own or through a
// group kept in the tenant. The rule on a person removing a role from themselves in a
// tenant reads them (RemoveRole): nobody gives up a tenant's administrator role nobody
// else would hold there. A person who holds one in every tenant is none of the tenant's
// own administrators, and does not stand in for its last one. Without them a person
// removes no role from themselves in a tenant at all.
//
// The application as a whole needs no such list: its last administrator is the last
// super administrator, the holder of the role the framework defines
// (accesstypes.SuperAdministrator), who is protected whoever asks. A person gives up any
// other role they hold in the global partition, since a super administrator gives it
// back.
func WithAdministratorRoles(domain []accesstypes.Role) Option {
	return func(m *Manager) {
		m.administrators = &administratorRoles{domain: domain}
	}
}

// WithFirstAdministratorRoles names the roles the first administrator's invitation
// gives, by where they are held: global for the whole application, everyDomain in every
// tenant. They are the two lists the auth's first-administrator setup declares
// (FirstAdministratorRoles in the auth package's foothold file), handed here as that
// function returns them.
//
// The first administrator's invitation is the one invitation whose roles are held in
// two places. The deployment sends it for the whole application, so its row stores no
// tenant, and the acceptance gives each of its roles where these lists say
// (giveInvitedRoles): a role everyDomain names in every tenant, a role both lists name
// in both places, and every other in the whole application, which is where the Super
// Administrator role it always carries is held. The lists are the application's own
// declaration, read when the person accepts; nothing is looked up by what kind of role
// a name happens to be.
//
// No other invitation reads the lists: one a user sent, one for a tenant or for every
// tenant, and one that does not give the Super Administrator role each give their roles
// in the one place they store, whatever is named here. Without the option the first
// administrator's invitation does too, so one that names a role of the tenants is not
// accepted.
func WithFirstAdministratorRoles(global, everyDomain []accesstypes.Role) Option {
	return func(m *Manager) {
		m.firstAdministrator = &firstAdministratorRoles{global: global, everyDomain: everyDomain}
	}
}

// WithSuperAdministratorMethods names the methods that give and take the auth's roles
// for the whole application, the ones the auth's generated management serves. A super
// administrator gives and takes any role whatever the role file grants, which the
// permission engine provides when it is built with the same methods
// (access.WithSuperAdministratorMethods). New holds the access library's engine to it
// and refuses one built without them, so an application that forgot the option does not
// start with a super administrator who can be locked out of the roles. The generated
// client passes the methods; an application does not.
func WithSuperAdministratorMethods(methods ...accesstypes.Resource) Option {
	return func(m *Manager) {
		m.superMethods = methods
	}
}

// WithTenantWord names what the application calls one tenant, as the middle of a
// sentence says it ("customer", "service district"): a refusal that speaks of a tenant
// says the application's word, the one its pages show. The generated client passes the
// tenant record's word. Without it, and for an empty word, the word is "tenant".
func WithTenantWord(word string) Option {
	return func(m *Manager) {
		if word != "" {
			m.tenantWord = word
		}
	}
}

// AccountTables names the tables of an auth whose rows a sign-in writes, as the live
// pages know them (the Resource of the generated type over each): its accounts, its
// sessions, its sign-in identities, its role memberships and its mail links.
type AccountTables struct {
	Users, Sessions, Identities, Memberships, MailLinks accesstypes.Resource
}

// WithAccountTables names the auth's tables a sign-in writes, which the manager announces
// to the open pages once it is handed a function to announce through (AnnounceWith). The
// generated client passes them. A table left empty is not announced, and nil names none;
// the manager keeps a copy, so a later change to tables is not seen.
func WithAccountTables(tables *AccountTables) Option {
	return func(m *Manager) {
		if tables != nil {
			m.tables = *tables
		}
	}
}

// New returns the manager of one auth. auth is the auth's name, the one its outlets'
// generated routes bind to a request (the auth package's Name): the rules that keep a
// person from acting on themselves take a caller for a person of this auth only when the
// request came through it. controller is the permission engine the auth's
// memberships are read by: the rule a membership's role is held to is its own
// (access.UserManager.Membership), and it is told once a membership is committed.
// sessions is the API of the session library's Auth that signs the auth's people in.
func New(auth string, store Store, controller access.Controller, sessions SessionAPI, opts ...Option) (*Manager, error) {
	if auth == "" {
		return nil, errors.New("identity.New(): the auth's name is empty; pass the auth package's Name, the name the auth's outlets bind to a request")
	}
	if store == nil {
		return nil, errors.New("identity.New(): the store is nil")
	}
	if controller == nil {
		return nil, errors.New("identity.New(): the permission engine is nil; a membership's role is held to its rule, and it is told once a membership is committed")
	}
	if sessions == nil {
		return nil, errors.New("identity.New(): the session API is nil; pass the API of the Auth that signs the auth's people in (session.Auth.API), whose hasher a password written here is checked by")
	}
	m := &Manager{
		auth: auth, store: store, access: controller, sessions: sessions,
		administrators: nil, firstAdministrator: nil, superMethods: nil, oneTenant: nil, tenantWord: "tenant", grants: nil, tenants: nil,
		tables: AccountTables{Users: "", Sessions: "", Identities: "", Memberships: "", MailLinks: ""}, announcer: atomic.Pointer[announceFunc]{}, now: time.Now,
	}
	for _, opt := range opts {
		opt(m)
	}
	if err := m.requireSuperAdministratorMethods(); err != nil {
		return nil, err
	}

	return m, nil
}

// superAdministratorEngine is the access library's own engine as New holds it to the
// methods: it says which methods it was built to let a super administrator execute
// (access.Client.SuperAdministratorMethods). A controller that does not say, such as the
// scripted one a test stands in for the engine, answers its checks as scripted and is
// held to nothing.
type superAdministratorEngine interface {
	SuperAdministratorMethods() []accesstypes.Resource
}

// requireSuperAdministratorMethods refuses a permission engine that was not built to let
// a super administrator execute the methods that give and take the auth's roles
// (WithSuperAdministratorMethods): the engine says what it was built with, so nothing is
// read from the database.
func (m *Manager) requireSuperAdministratorMethods() error {
	engine, says := m.access.(superAdministratorEngine)
	if !says {
		return nil
	}
	built := engine.SuperAdministratorMethods()
	for _, method := range m.superMethods {
		if !slices.Contains(built, method) {
			return errors.Newf("identity.New(): the permission engine of the %s auth does not let a super administrator execute %s, a method that gives or takes the auth's roles; %s",
				m.auth, method, "build the engine with access.WithSuperAdministratorMethods and the methods the auth's generated management names (its SuperAdministratorMethods)")
		}
	}

	return nil
}

// announceFunc publishes a collector's rows and tables to the open pages.
type announceFunc = func(ctx context.Context, touched *resource.TouchedRows)

// AnnounceWith hands the manager the function that publishes what a sign-in wrote to the
// open pages. The session library writes a sign-in in its own store, where no generated
// handler publishes it, so the manager announces it from the hooks it answers: the
// session and the account by their ids, the identities and the memberships as whole
// tables, since a hook is not told which of their rows a sign-in wrote.
//
// The first function handed over is kept; nil and a later one are ignored, and a sign-in
// on another goroutine reads it safely. Until one is handed over nothing is announced.
func (m *Manager) AnnounceWith(publish func(ctx context.Context, touched *resource.TouchedRows)) {
	if publish == nil {
		return
	}
	m.announcer.CompareAndSwap(nil, &publish)
}

// signInAnnouncement is what one moment of a sign-in announces: the rows its own
// transaction handed the collector, and the rows and tables the moment names.
type signInAnnouncement struct {
	publish announceFunc
	touched *resource.TouchedRows
}

// announcing starts a moment's announcement: ctx carrying a fresh collector, which the
// moment's own transaction hands its rows to, and the announcement. Where no function was
// handed over (AnnounceWith) ctx is answered as it is, with a nil announcement that
// records and publishes nothing.
func (m *Manager) announcing(ctx context.Context) (context.Context, *signInAnnouncement) {
	publish := m.announcer.Load()
	if publish == nil {
		return ctx, nil
	}
	ctx, touched := resource.CollectTouchedRows(ctx)

	return ctx, &signInAnnouncement{publish: *publish, touched: touched}
}

// row names one row of table by its id; a table the auth left unnamed is skipped.
func (a *signInAnnouncement) row(table accesstypes.Resource, id impulse.UUID) *signInAnnouncement {
	if a == nil || table == "" {
		return a
	}
	a.touched.Touch(table, resource.RowChange{Key: resource.RowKey(id), Deleted: false})

	return a
}

// whole names tables changed in rows the moment cannot name; a table the auth left
// unnamed is skipped.
func (a *signInAnnouncement) whole(tables ...accesstypes.Resource) *signInAnnouncement {
	if a == nil {
		return nil
	}
	for _, table := range tables {
		if table != "" {
			a.touched.TouchTable(table)
		}
	}

	return a
}

// announce publishes what the moment collected.
func (a *signInAnnouncement) announce(ctx context.Context) {
	if a == nil {
		return
	}
	a.publish(ctx, a.touched)
}
