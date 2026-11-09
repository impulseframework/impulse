// Package sessionstorage implements database storage for session data.
// There are implementations for both Spanner and Postgres.
package sessionstorage

import (
	"context"
	"reflect"
	"time"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/securehash"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage/internal/postgres"
	"github.com/impulseframework/impulse/session/sessionstorage/internal/spanner"
)

// BaseStore defines an interface for managing session storage.
type BaseStore interface {
	// Session returns the session information from the database for given sessionID
	Session(ctx context.Context, sessionID impulse.UUID) (*sessioninfo.SessionData, error)
	// UpdateCustomSessionData updates the custom session data for an active session via
	// a transactional read-modify-write: mutate receives the current row as *T for the
	// configured struct type (zero-value when no row exists) and the full row is written
	// back; a mutate error aborts with nothing written. For genuine mid-session updates
	// only — initial population belongs in the session-creation transaction.
	UpdateCustomSessionData(ctx context.Context, sessionID impulse.UUID, mutate func(data any) error) error
	// CustomDataType returns the struct type the attached custom session data
	// configuration was built for, or nil when no configuration is attached.
	CustomDataType() reflect.Type
	// CustomUserDataType returns the struct type the attached custom user data
	// configuration was built for, or nil when no configuration is attached.
	CustomUserDataType() reflect.Type
	// UpdateSessionActivity updates the database with the current time for the session activity
	UpdateSessionActivity(ctx context.Context, sessionID impulse.UUID) error
	// DestroySession marks the session as expired. When an impersonation table is
	// configured, an impersonated session's record is ended with reason Logout.
	DestroySession(ctx context.Context, sessionID impulse.UUID) error
	// ImpersonationEnabled reports whether an impersonation record table is configured
	// (WithImpersonation). Impersonated sessions can only be created, read, and
	// evidenced when it is.
	ImpersonationEnabled() bool
	// EndImpersonation records how an impersonated session ended, once: it sets EndedAt
	// and EndReason on the session's impersonation record when the record exists and
	// has not already ended, and is a no-op otherwise. It errors when no impersonation
	// table is configured.
	EndImpersonation(ctx context.Context, sessionID impulse.UUID, reason sessioninfo.ImpersonationEndReason) error
	// CreateImpersonatedSession creates a new session for the request together with
	// its impersonation record, atomically, and returns the session ID. Custom session
	// data follows the session type's creation semantics inside the same transaction
	// (per-call data, or the configured resolver receiving ReasonImpersonation). No user
	// record is consulted or written: the request's Username is the
	// session's effective identity as the session type resolved it. It errors when no
	// impersonation table is configured.
	CreateImpersonatedSession(ctx context.Context, req *sessioninfo.NewSessionRequest, imp *sessioninfo.Impersonation) (impulse.UUID, error)
	// DestroyImpersonatedSessions expires every live impersonated session established
	// by actor and ends their records with reason Revoked. It errors when no
	// impersonation table is configured.
	DestroyImpersonatedSessions(ctx context.Context, actor string) error
	// ActiveImpersonations lists the impersonated sessions that are live, newest first:
	// record not ended, hard cap not passed, session row not expired, and session
	// activity after activeSince. q narrows the listing by actor and/or principal. It
	// errors when no impersonation table is configured.
	ActiveImpersonations(ctx context.Context, activeSince time.Time, q *sessioninfo.ImpersonationQuery) ([]*sessioninfo.Impersonation, error)
	// DestroyImpersonatedSession expires one live impersonated session and ends its record
	// with reason, atomically — the single-session form of DestroyImpersonatedSessions
	// (Revoked) and the storage step of EndImpersonation (Released). A session that is
	// not impersonated, or whose record has already ended, is left untouched. It errors
	// when no impersonation table is configured.
	DestroyImpersonatedSession(ctx context.Context, sessionID impulse.UUID, reason sessioninfo.ImpersonationEndReason) error
	// SetSessionTableName sets the name of the session table.
	SetSessionTableName(name string) error
	// SetUserTableName sets the name of the user table.
	SetUserTableName(name string) error
}

var _ PreauthStore = (*Preauth)(nil)

// PreauthStore defines an interface for managing pre-authenticated session storage.
type PreauthStore interface {
	// NewSession creates a new session in the database, returning its id. customData
	// is nil or *T for the configured custom session data struct type; when non-nil it
	// is written atomically with the session insert and requires a custom session data
	// configuration on the storage.
	NewSession(ctx context.Context, username string, customData any) (impulse.UUID, error)
	// DestroyAllUserSessions destroys all sessions for a given user
	DestroyAllUserSessions(ctx context.Context, username string) error

	// shared storage methods
	BaseStore
}

// SessionUser is a person authorized to access the application.
type SessionUser = dbtype.SessionUser

// InsertSessionUser defines the structure for inserting a new SessionUser.
type InsertSessionUser = dbtype.InsertSessionUser

// UserStore is the user management of a store that has users: the Auth storage.
type UserStore interface {
	// User returns a session user for give user id
	User(ctx context.Context, id impulse.UUID) (*SessionUser, error)
	// UserByUsername returns a session user for give username
	UserByUserName(ctx context.Context, username string) (*SessionUser, error)
	// SetUserUsername updates the user username and, atomically, the Username
	// on every active session row for that user.
	SetUserUsername(ctx context.Context, id impulse.UUID, newUsername string) error
	// SetUserPasswordHash updates the user password hash
	SetUserPasswordHash(ctx context.Context, id impulse.UUID, hash *securehash.Hash) error
	// ActivateUser activates a user
	ActivateUser(ctx context.Context, id impulse.UUID) error
	// CreateUser creates a new user. customData is nil or *U for the configured custom
	// user data struct type; when non-nil it is written atomically with the user insert
	// and requires a custom user data configuration on the storage.
	CreateUser(ctx context.Context, user *InsertSessionUser, customData any) (*SessionUser, error)
	// DeactivateUser deactivates a user
	DeactivateUser(ctx context.Context, id impulse.UUID) error
	// DeleteUser deletes a user
	DeleteUser(ctx context.Context, id impulse.UUID) error
	// CustomUserData returns the custom user data row for the given user ID as *U for
	// the configured struct type. A user with no custom data row yields a zero-value *U.
	CustomUserData(ctx context.Context, userID impulse.UUID) (any, error)
	// UpdateCustomUserData updates the custom user data for an existing user via a
	// transactional read-modify-write: mutate receives the current row as *U (zero-value
	// when no row exists) and the full row is written back; a mutate error aborts with
	// nothing written.
	UpdateCustomUserData(ctx context.Context, userID impulse.UUID, mutate func(data any) error) error
}

var (
	_ db = (*spanner.SessionStorageDriver)(nil)
	_ db = (*postgres.SessionStorageDriver)(nil)
)

// db defines an interface for database operations related to session management.
type db interface {
	// Session returns the session information from the database for given sessionID.
	Session(ctx context.Context, sessionID impulse.UUID) (*dbtype.SessionData, error)
	// InsertSession inserts a Session into the database and returns its id. When a custom
	// session data configuration with a resolver is attached, the resolver runs within the
	// same transaction as the session insert; a resolver error aborts the insert.
	InsertSession(ctx context.Context, insertSession *dbtype.InsertSession, req *sessioninfo.NewSessionRequest) (impulse.UUID, error)
	// UpdateCustomSessionData updates the custom session data for an active session via a
	// transactional read-modify-write of the full row.
	UpdateCustomSessionData(ctx context.Context, sessionID impulse.UUID, mutate func(data any) error) error
	// CustomDataType returns the struct type of the attached custom session data configuration, or nil.
	CustomDataType() reflect.Type
	// CustomUserDataType returns the struct type of the attached custom user data configuration, or nil.
	CustomUserDataType() reflect.Type
	// CustomUserData returns the custom user data row for the given user ID as *U
	// (zero-value *U when no row exists).
	CustomUserData(ctx context.Context, userID impulse.UUID) (any, error)
	// UpdateCustomUserData updates the custom user data for an existing user via a
	// transactional read-modify-write of the full row.
	UpdateCustomUserData(ctx context.Context, userID impulse.UUID, mutate func(data any) error) error
	// UpdateSessionActivity updates the session activity column with the current time.
	UpdateSessionActivity(ctx context.Context, sessionID impulse.UUID) error
	// DestroySession marks the session as expired, ending an impersonated session's record with reason Logout.
	DestroySession(ctx context.Context, sessionID impulse.UUID) error
	// DestroySessionsByProviderSessionID expires every live session whose sign-in carried
	// the provider's session id under issuer: both columns match, a session without an
	// issuer never does.
	DestroySessionsByProviderSessionID(ctx context.Context, issuer, providerSessionID string) error
	// ProviderSessionIssuers lists the distinct issuers under which live sessions carry
	// the provider's session id, for a provider-initiated logout that names no issuer. A
	// session recorded without an issuer is left out: it is out of a logout's reach.
	ProviderSessionIssuers(ctx context.Context, providerSessionID string) ([]string, error)
	// SetIdentitiesTableName sets the name of the identities table.
	SetIdentitiesTableName(name string) error
	// SetPendingSignInsTableName sets the name of the pending sign-ins table.
	SetPendingSignInsTableName(name string) error
	// InsertPendingSignIn inserts a pending sign-in.
	InsertPendingSignIn(ctx context.Context, p *dbtype.PendingSignIn) error
	// PendingSignInByToken returns the pending sign-in holding tokenHash that is live at now, or NotFound.
	PendingSignInByToken(ctx context.Context, tokenHash []byte, now time.Time) (*dbtype.PendingSignIn, error)
	// ConsumePendingSignIn marks the live pending sign-in holding tokenHash consumed at now by a conditional update; NotFound when it is not live.
	ConsumePendingSignIn(ctx context.Context, tokenHash []byte, now time.Time) error
	// SetSignInAttemptsTableName sets the name of the sign-in attempts table.
	SetSignInAttemptsTableName(name string) error
	// SignInAttempts returns the sign-in attempts record of name, or NotFound when there is none.
	SignInAttempts(ctx context.Context, name string) (*dbtype.SignInAttempts, error)
	// RecordSignInFailure records a failed attempt on name at now under limit, in one transaction, and returns the record as written.
	RecordSignInFailure(ctx context.Context, name string, now time.Time, limit dbtype.SignInAttemptLimit) (*dbtype.SignInAttempts, error)
	// ClearSignInAttempts deletes the sign-in attempts record of name, if there is one.
	ClearSignInAttempts(ctx context.Context, name string) error
	// SetMailLinksTableName sets the name of the mail links table.
	SetMailLinksTableName(name string) error
	// InsertMailLink inserts a mail link.
	InsertMailLink(ctx context.Context, l *dbtype.MailLink) error
	// MailLinkByToken returns the mail link holding tokenHash that is live at now, or NotFound.
	MailLinkByToken(ctx context.Context, tokenHash []byte, now time.Time) (*dbtype.MailLink, error)
	// LiveMailLinks lists the links of kind that are live at now, oldest first.
	LiveMailLinks(ctx context.Context, kind dbtype.MailLinkKind, now time.Time) ([]*dbtype.MailLink, error)
	// ConsumeMailLink marks the live mail link holding tokenHash consumed at now by a conditional update; NotFound when it is not live.
	ConsumeMailLink(ctx context.Context, tokenHash []byte, now time.Time) error
	// RevokeMailLink marks the live mail link id consumed at now; NotFound when it is not live.
	RevokeMailLink(ctx context.Context, id impulse.UUID, now time.Time) error
	// SetTokensTableName sets the name of the tokens table.
	SetTokensTableName(name string) error
	// SetDeviceCodesTableName sets the name of the device codes table.
	SetDeviceCodesTableName(name string) error
	// InsertToken inserts t.
	InsertToken(ctx context.Context, t *dbtype.Token) error
	// TokenByHash returns the token holding tokenHash that is live at now, or NotFound.
	TokenByHash(ctx context.Context, tokenHash []byte, now time.Time) (*dbtype.Token, error)
	// TokensByUser lists the tokens of userID that are live at now, newest first.
	TokensByUser(ctx context.Context, userID impulse.UUID, now time.Time) ([]*dbtype.Token, error)
	// TouchToken moves the LastUsedAt of the live token id to the database's time.
	TouchToken(ctx context.Context, id impulse.UUID, now time.Time) error
	// RevokeUserToken revokes the live token id of userID, its family with it; NotFound when it is not live or not the user's.
	RevokeUserToken(ctx context.Context, userID, id impulse.UUID, now time.Time, reason dbtype.TokenRevocation) error
	// RevokeUserTokens revokes every live token of userID.
	RevokeUserTokens(ctx context.Context, userID impulse.UUID, reason dbtype.TokenRevocation) error
	// ExchangeRefreshToken rotates the live refresh token holding tokenHash into access and refresh, in one transaction;
	// NotFound when it is not live, dbtype.ErrTokenReuse (the family revoked) when it was rotated before.
	ExchangeRefreshToken(ctx context.Context, tokenHash []byte, now time.Time, access, refresh *dbtype.Token) error
	// InsertDeviceCode inserts d.
	InsertDeviceCode(ctx context.Context, d *dbtype.DeviceCode) error
	// DeviceCodeByUserCode returns the device code userCode names that is pending at now, or NotFound.
	DeviceCodeByUserCode(ctx context.Context, userCode string, now time.Time) (*dbtype.DeviceCode, error)
	// ApproveDeviceCode records userID's approval of the pending device code id; NotFound when it is not pending.
	ApproveDeviceCode(ctx context.Context, id, userID impulse.UUID, now time.Time) error
	// DenyDeviceCode records the denial of the pending device code id; NotFound when it is not pending.
	DenyDeviceCode(ctx context.Context, id impulse.UUID, now time.Time) error
	// RedeemDeviceCode is the client's poll of the device code holding deviceCodeHash: an approved code is consumed once
	// and access and refresh inserted; otherwise the status says what the client learned. NotFound when there is none.
	RedeemDeviceCode(ctx context.Context, deviceCodeHash []byte, now time.Time, interval time.Duration, access, refresh *dbtype.Token) (*dbtype.DeviceCode, dbtype.DeviceCodeStatus, error)
	// SetFactorCodesTableName sets the name of the factor codes table.
	SetFactorCodesTableName(name string) error
	// SetTrustedDevicesTableName sets the name of the trusted devices table.
	SetTrustedDevicesTableName(name string) error
	// InsertFactorCode consumes the earlier codes of c's pending sign-in that are live at now and inserts c, in one transaction.
	InsertFactorCode(ctx context.Context, c *dbtype.FactorCode, now time.Time) error
	// FactorCodeByPending returns the newest code of pendingID that is live at now, or NotFound.
	FactorCodeByPending(ctx context.Context, pendingID impulse.UUID, now time.Time) (*dbtype.FactorCode, error)
	// RecordFactorCodeAttempt counts a wrong code against the code id that is live at now and returns the count; NotFound when it is not live.
	RecordFactorCodeAttempt(ctx context.Context, id impulse.UUID, now time.Time) (int, error)
	// ConsumeFactorCode marks the live code id consumed at now by a conditional update; NotFound when it is not live.
	ConsumeFactorCode(ctx context.Context, id impulse.UUID, now time.Time) error
	// InsertTrustedDevice inserts d.
	InsertTrustedDevice(ctx context.Context, d *dbtype.TrustedDevice) error
	// TrustedDeviceByToken returns the device holding tokenHash that is live at now, or NotFound.
	TrustedDeviceByToken(ctx context.Context, tokenHash []byte, now time.Time) (*dbtype.TrustedDevice, error)
	// TouchTrustedDevice moves the LastUsedAt of the live device id to the database's time.
	TouchTrustedDevice(ctx context.Context, id impulse.UUID, now time.Time) error
	// RevokeTrustedDevice revokes the live device holding tokenHash; NotFound when it is not live.
	RevokeTrustedDevice(ctx context.Context, tokenHash []byte, now time.Time) error
	// RevokeTrustedDevices revokes every device of userID that is not revoked yet.
	RevokeTrustedDevices(ctx context.Context, userID impulse.UUID) error
	// SetUsernameIsEmail declares the users table in the shape where the address is the username.
	SetUsernameIsEmail(same bool)
	// UsernameIsEmail reports whether the address is the username.
	UsernameIsEmail() bool
	// UserByEmail returns the user holding the address, verified or not, or NotFound.
	UserByEmail(ctx context.Context, email string) (*dbtype.SessionUser, error)
	// SetUserEmail puts an address on the user by hand, unverified, or takes it off (nil).
	SetUserEmail(ctx context.Context, id impulse.UUID, email *string) error
	// VerifyUserEmail writes the address the person proved they read, verified at the database's time.
	VerifyUserEmail(ctx context.Context, id impulse.UUID, email string) error
	// ClaimVerifiedEmail writes email on the user as verified when the user holds no verified address and no other user holds it, and reports whether it did.
	ClaimVerifiedEmail(ctx context.Context, id impulse.UUID, email string) (bool, error)
	// ImpersonationEnabled reports whether an impersonation record table is configured.
	ImpersonationEnabled() bool
	// InsertImpersonatedSession inserts a session row and its impersonation record atomically,
	// honoring the request's custom session data semantics exactly as InsertSession does.
	InsertImpersonatedSession(ctx context.Context, insertSession *dbtype.InsertSession, req *sessioninfo.NewSessionRequest, imp *dbtype.InsertImpersonation) (impulse.UUID, error)
	// EndImpersonation sets EndedAt and EndReason on a live impersonation record; a no-op for
	// sessions that are not impersonated or whose record has already ended.
	EndImpersonation(ctx context.Context, sessionID impulse.UUID, reason string) error
	// DestroyImpersonatedSessions expires every live impersonated session established by actor
	// and ends their records with reason Revoked.
	DestroyImpersonatedSessions(ctx context.Context, actor string) error
	// ActiveImpersonations lists live impersonation records joined to their session rows, newest first:
	// record not ended, hard cap not passed, session not expired, session UpdatedAt after activeSince,
	// narrowed by q's actor and/or principal.
	ActiveImpersonations(ctx context.Context, activeSince time.Time, q *sessioninfo.ImpersonationQuery) ([]*dbtype.Impersonation, error)
	// DestroyImpersonatedSession expires one live impersonated session and ends its record with
	// reason, in one transaction; a no-op for sessions without a live record.
	DestroyImpersonatedSession(ctx context.Context, sessionID impulse.UUID, reason string) error
	// SetSessionTableName sets the name of the session table.
	SetSessionTableName(name string) error
	// SetUserTableName sets the name of the user table.
	SetUserTableName(name string) error

	//
	// Password specific methods
	//

	// User returns a session user for give user id
	User(ctx context.Context, id impulse.UUID) (*dbtype.SessionUser, error)
	// UserByUsername returns a session user for give username
	UserByUserName(ctx context.Context, username string) (*dbtype.SessionUser, error)
	// SetUserUsername updates the user username and, atomically, the Username
	// on every active session row for that user.
	SetUserUsername(ctx context.Context, id impulse.UUID, newUsername string) error
	// SetUserPasswordHash updates the user password hash, leaving the password change mark as it is.
	SetUserPasswordHash(ctx context.Context, id impulse.UUID, hash *securehash.Hash) error
	// SetUserPassword updates the user password hash and the password change mark in one write.
	SetUserPassword(ctx context.Context, id impulse.UUID, hash *securehash.Hash, changeRequired bool) error
	// ActivateUser activates a user
	ActivateUser(ctx context.Context, id impulse.UUID) error
	// CreateUser creates a new user; non-nil customData (*U) is written atomically with the user insert.
	CreateUser(ctx context.Context, insertSessionUser *dbtype.InsertSessionUser, customData any) (*dbtype.SessionUser, error)
	// Users lists every user, by username.
	Users(ctx context.Context) ([]*dbtype.SessionUser, error)
	// DeactivateUser deactivates a user
	DeactivateUser(ctx context.Context, id impulse.UUID) error
	// DeleteUser deletes a user
	DeleteUser(ctx context.Context, id impulse.UUID) error
	// DestroyAllUserSessions destroys all sessions for a given user
	DestroyAllUserSessions(ctx context.Context, username string) error

	//
	// User specific methods
	//

	// Identity returns the link for (method, connection, subject), or a NotFound error.
	Identity(ctx context.Context, method sessioninfo.AuthMethod, connection, subject string) (*dbtype.SessionIdentity, error)
	// IdentitiesByUser lists a user's links, oldest first.
	IdentitiesByUser(ctx context.Context, userID impulse.UUID) ([]*dbtype.SessionIdentity, error)
	// LinkIdentity links an identity to a user outside the sign-in flow.
	LinkIdentity(ctx context.Context, userID impulse.UUID, identity *sessioninfo.Identity, tenant string) (*dbtype.SessionIdentity, error)
	// UnlinkIdentity removes a link, refusing to remove a user's last means of sign-in.
	UnlinkIdentity(ctx context.Context, identityID impulse.UUID) error
	// DestroyUserSessions expires every session of a user, by UserId.
	DestroyUserSessions(ctx context.Context, userID impulse.UUID) error
	// TouchUserSignIn moves a user's LastSignInAt to the database's time, as its own write.
	TouchUserSignIn(ctx context.Context, userID impulse.UUID) error
	// AppendAuthEvent records an auth event on a live session.
	AppendAuthEvent(ctx context.Context, sessionID impulse.UUID, event *sessioninfo.AuthEvent) error
}
