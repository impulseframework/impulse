// Package identity holds the rules of managing the people of one auth: creating a
// person, disabling one and ending their sessions, seating one in a role. An
// application does not call it directly. The code generator writes, for every auth an
// application declares, the resources over the auth's own tables and the methods that
// change them; each method's body is one call into this package, so the rules are
// compiled and tested once and are the same for every auth of every application.
//
// Every rule runs inside the caller's transaction (resource.ReadWriteTransaction): a
// generated handler's, or one an application opened to create a person beside its own
// rows and commit them together. Nothing here commits, and nothing reads a row a sibling
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
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session"
)

// Store is what the rules need of one auth's tables: single reads and single buffered
// writes, which the generated package of the auth implements over its own typed queries
// and patches. An implementation holds no rule: it maps the values it is given onto the
// auth's columns and answers what it read.
type Store interface {
	// InsertUser buffers a new user and answers the id the row is created under.
	InsertUser(ctx context.Context, txn resource.ReadWriteTransaction, user *NewUser) (impulse.UUID, error)
	// UpdateUser buffers a change to a user: every field of change that is set.
	UpdateUser(ctx context.Context, txn resource.ReadWriteTransaction, id impulse.UUID, change *UserChange) error
	// SetPassword buffers a user's password hash, as the session library's hasher
	// rendered it, and whether the person must change the password before anything
	// else is served. An auth with no password method has no such write.
	SetPassword(ctx context.Context, txn resource.ReadWriteTransaction, id impulse.UUID, hash string, changeRequired bool) error
	// ClearAttempts buffers the removal of the count of failed sign-ins kept under a
	// name (session.SignInAttemptName); a name with no count is no error.
	ClearAttempts(ctx context.Context, txn resource.ReadWriteTransaction, name string) error
	// UserHasPassword reads whether a user holds a password; found is false where no
	// user holds the id. The hash itself is read by nothing.
	UserHasPassword(ctx context.Context, txn resource.ReadWriteTransaction, id impulse.UUID) (has, found bool, err error)

	// IdentityUser reads the user an identity links to; found is false where no
	// identity holds the id. An auth that keeps no identities finds none.
	IdentityUser(ctx context.Context, txn resource.ReadWriteTransaction, id impulse.UUID) (userID impulse.UUID, found bool, err error)
	// Identities lists the identities linked to a user.
	Identities(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) ([]impulse.UUID, error)
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
	// UserID reads the id of the user who holds a username, compared as the auth
	// compares names at a sign-in; found is false where none does.
	UserID(ctx context.Context, txn resource.ReadWriteTransaction, username string) (id impulse.UUID, found bool, err error)
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
	// Memberships lists every membership the username holds, wherever it is held.
	Memberships(ctx context.Context, txn resource.ReadWriteTransaction, username string) ([]access.MembershipKey, error)
	// RoleHolders lists who holds the role where the key says it is held.
	RoleHolders(ctx context.Context, txn resource.ReadWriteTransaction, key *access.RoleKey) ([]accesstypes.User, error)
	// InsertMembership buffers a membership row.
	InsertMembership(ctx context.Context, txn resource.ReadWriteTransaction, key *access.MembershipKey) error
	// DeleteMembership buffers the removal of a membership row.
	DeleteMembership(ctx context.Context, txn resource.ReadWriteTransaction, key *access.MembershipKey) error

	// InsertInvitation buffers the mail links row of a prepared invitation, as the
	// session library says one is written (session.InvitationRow).
	InsertInvitation(ctx context.Context, txn resource.ReadWriteTransaction, row *session.InvitationRow) error
	// MailLink reads one link the auth mailed; found is false where no link holds the
	// id.
	MailLink(ctx context.Context, txn resource.ReadWriteTransaction, id impulse.UUID) (link *MailLink, found bool, err error)
	// ConsumeMailLink buffers the consumption of one link: it is then dead.
	ConsumeMailLink(ctx context.Context, txn resource.ReadWriteTransaction, id impulse.UUID) error

	// TrustedDevices lists the browsers the user asked to be remembered after the
	// second factor that are not revoked. An auth that keeps none answers none.
	TrustedDevices(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) ([]impulse.UUID, error)
	// RevokeTrustedDevice buffers the revocation of one remembered browser.
	RevokeTrustedDevice(ctx context.Context, txn resource.ReadWriteTransaction, id impulse.UUID) error
}

// SessionAPI is what the rules take from the session library's Auth, the one that signs
// the auth's people in: the API of the application's own Auth satisfies it
// (session.Auth.API). The password method's hasher is the Auth's, so a password written
// here is one the sign-in can check.
type SessionAPI interface {
	// HashPassword hashes a password as the users table holds one; an empty password
	// is refused.
	HashPassword(password string) (string, error)
	// PrepareInvitation checks and makes an invitation without storing or mailing it:
	// the row to store, and the step that mails its link once the row is committed.
	// The token, the message and the sender stay in the session library.
	PrepareInvitation(ctx context.Context, req *session.InvitationRequest) (session.PreparedInvitation, error)
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

// NewUser is a user row as InsertUser writes it.
type NewUser struct {
	Username string
	// Email is the address the person is reached at, nil for none. It is written
	// unverified.
	Email *string
}

// UserChange is a change to a user row: a nil field is left as it is.
type UserChange struct {
	Username               *string
	Disabled               *bool
	PasswordChangeRequired *bool
	// Email, where set, replaces the address the row holds. An address written here was
	// entered by hand, so the row is left unverified: the time the address it held was
	// verified goes with it.
	Email *EmailChange
}

// EmailChange is the address a change writes on a user row: Address, nil for none.
type EmailChange struct {
	Address *string
}

// Token is a bearer token as the rules read it.
type Token struct {
	// Family is the device authorization the token belongs to, nil for a token that
	// stands alone.
	Family *impulse.UUID
	// ExpiresAt is when the token stops being accepted, and Revoked whether it was
	// revoked before that.
	ExpiresAt time.Time
	Revoked   bool
}

// Manager applies the rules to one auth: over the store of its tables, with the
// permission engine that reads its memberships.
type Manager struct {
	store    Store
	access   access.Controller
	sessions SessionAPI
	// administrators are the roles whose holders administer the application and a
	// tenant, nil where the application named none (WithAdministratorRoles).
	administrators *administratorRoles
	// now is the application's clock, which the session library holds a deadline
	// against (a token's expiry).
	now func() time.Time
}

// administratorRoles are the roles whose holders administer, by where they are held.
type administratorRoles struct {
	global []accesstypes.Role
	domain []accesstypes.Role
}

// Option configures a manager.
type Option func(*Manager)

// WithAdministratorRoles names the roles whose holders administer: global, the ones
// held in the global scope, and domain, the ones held in a tenant. The rule on a
// person removing a role from themselves reads them (RemoveRole): nobody gives up an
// administrator role nobody else would hold. Without them a person removes no role
// from themselves at all.
func WithAdministratorRoles(global, domain []accesstypes.Role) Option {
	return func(m *Manager) {
		m.administrators = &administratorRoles{global: global, domain: domain}
	}
}

// New returns the manager of one auth. controller is the permission engine the auth's
// memberships are read by: the rule a membership's role is held to is its own
// (access.UserManager.Membership), and it is told once a membership is committed.
// sessions is the API of the session library's Auth that signs the auth's people in.
func New(store Store, controller access.Controller, sessions SessionAPI, opts ...Option) (*Manager, error) {
	if store == nil {
		return nil, errors.New("identity.New(): the store is nil")
	}
	if controller == nil {
		return nil, errors.New("identity.New(): the permission engine is nil; a membership's role is held to its rule, and it is told once a membership is committed")
	}
	if sessions == nil {
		return nil, errors.New("identity.New(): the session API is nil; pass the API of the Auth that signs the auth's people in (session.Auth.API), whose hasher a password written here is checked by")
	}
	m := &Manager{store: store, access: controller, sessions: sessions, administrators: nil, now: time.Now}
	for _, opt := range opts {
		opt(m)
	}

	return m, nil
}
