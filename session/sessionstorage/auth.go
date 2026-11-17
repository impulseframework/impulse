package sessionstorage

import (
	"context"
	"time"

	cloudspanner "cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/securehash"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/internal/signinflow"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage/internal/postgres"
	"github.com/impulseframework/impulse/session/sessionstorage/internal/spanner"
	"github.com/impulseframework/impulse/session/signin"
	"github.com/impulseframework/impulse/tracer"
	"github.com/jackc/pgx/v5"
)

// ErrLastSignInMethod is returned when UnlinkIdentity would remove the last identity of
// a user that has no password, and when RemoveUserPassword would remove the password of
// a user none of whose identities can sign them in: either would leave the user no
// means of sign-in. It is a Conflict client message.
var ErrLastSignInMethod = dbtype.ErrLastSignInMethod

// ErrPasswordChanged is returned by the password writes that follow a proof of the
// password (UpgradeUserPasswordHash, ChangeUserPassword and RemoveProvenUserPassword)
// and by the inserts made on the strength of one (SignIn, HoldSignIn and
// LinkConfirmedIdentity with a PasswordProof) when the user no longer holds the hash
// that was proved: the password was changed, reset or removed after the caller read
// it. Nothing is written, so the other write stands whole. It is a Conflict client
// message.
var ErrPasswordChanged = dbtype.ErrPasswordChanged

// PasswordProof names the password a sign-in proved, without holding it: a digest of
// the hash the password was compared against. A session, a pending sign-in or a
// confirmed link that carries one is inserted only while the user still holds that
// password, compared in the inserting transaction (ErrPasswordChanged otherwise), and a
// pending sign-in keeps it in its row so its completion is held the same way. The nil
// proof is the sign-in no password proved, which is held to nothing.
type PasswordProof = dbtype.PasswordProof

// ProofOfPassword is the proof of a password that was compared against hash, the hash
// the user was read with: nil for a user with no password.
func ProofOfPassword(hash *securehash.Hash) (PasswordProof, error) {
	proof, err := dbtype.ProofOf(hash)
	if err != nil {
		return nil, errors.Wrap(err, "dbtype.ProofOf()")
	}

	return proof, nil
}

// SessionIdentity is a stored link from an external identity to a user.
type SessionIdentity struct {
	ID          impulse.UUID           `spanner:"Id"          db:"Id"`
	UserID      impulse.UUID           `spanner:"UserId"      db:"UserId"`
	Method      sessioninfo.AuthMethod `spanner:"Method"      db:"Method"`
	Connection  string                 `spanner:"Connection"  db:"Connection"`
	Subject     string                 `spanner:"Subject"     db:"Subject"`
	Tenant      *string                `spanner:"Tenant"      db:"Tenant"`
	EmailAtLink *string                `spanner:"EmailAtLink" db:"EmailAtLink"`
	CreatedAt   time.Time              `spanner:"CreatedAt"   db:"CreatedAt"`
	LastUsedAt  time.Time              `spanner:"LastUsedAt"  db:"LastUsedAt"`
}

// AuthEventsTable names the table that records how each session was authenticated.
type AuthEventsTable struct {
	tableName string
}

// NewAuthEventsTable validates the auth events table name.
func NewAuthEventsTable(tableName string) (*AuthEventsTable, error) {
	if !validIdentifier.MatchString(tableName) {
		return nil, errors.Newf("invalid table name: %s. Table names must start with a letter or underscore, followed by up to 127 letters, numbers, or underscores.", tableName)
	}

	return &AuthEventsTable{tableName: tableName}, nil
}

type authEventsOption struct{ table *AuthEventsTable }

func (o authEventsOption) applySpanner(driver *spanner.SessionStorageDriver) {
	driver.SetAuthEvents(&spanner.AuthEventsConfig{TableName: o.table.tableName})
}

func (o authEventsOption) applyPostgres(driver *postgres.SessionStorageDriver) {
	driver.SetAuthEvents(&postgres.AuthEventsConfig{TableName: o.table.tableName})
}

// WithAuthEvents enables recording auth events in the given table.
func WithAuthEvents(table *AuthEventsTable) Option {
	return authEventsOption{table: table}
}

// AuthStore is the storage behind an Auth session: users (SessionUsers), sessions with
// UserId, identity links (SessionIdentities), auth events and pending sign-ins
// (SessionPendingSignIns), over one database. Tx is
// the database's transaction type, pgx.Tx or *spanner.ReadWriteTransaction, which the
// sign-in hooks receive; NewPostgresAuth and NewSpannerAuth fix it.
type AuthStore[Tx any] interface {
	BaseStore
	UserStore
	// Users lists every user, by username.
	Users(ctx context.Context) ([]*SessionUser, error)
	// SetUserPassword sets a user's password hash and whether the user must change the
	// password before anything else is served (SessionUser.PasswordChangeRequired), in
	// one write: true for a password an operator set, false for one the user chose.
	// It follows no proof of the password it replaces and overwrites whatever the row
	// holds: it is an operator's write, or the end of a flow that proved who the person
	// is by other means. A user's own change is ChangeUserPassword.
	SetUserPassword(ctx context.Context, id impulse.UUID, hash *securehash.Hash, changeRequired bool) error
	// ChangeUserPassword replaces the password a user proved with one the user chose,
	// clears the password change mark and expires every session of the user, as
	// DestroyUserSessions does, in one transaction: the password is changed and the
	// sessions ended together, or neither is. proven is the hash the caller compared the
	// user's old password against, and the write lands only while the row still holds
	// it: a password changed, reset or removed since the caller read it stands as it is
	// (ErrPasswordChanged), with no session ended, so a temporary password an operator
	// set meanwhile is not overwritten on proof of the password it replaced. A write with
	// no proven hash writes nothing, and one with no new hash is refused.
	ChangeUserPassword(ctx context.Context, id impulse.UUID, proven, hash *securehash.Hash) error
	// CreateSession inserts a session that has no identity to resolve and returns its
	// ID: a session the application starts for a user it authenticated itself, or a
	// regeneration. A configured custom session data resolver runs inside the insert
	// transaction. A pending sign-in has no session row; see HoldSignIn.
	CreateSession(ctx context.Context, req *sessioninfo.NewSessionRequest) (impulse.UUID, error)
	// SignIn establishes the session of a sign-in that carries a verified identity
	// (req.Identity) and returns its ID. An external identity resolves to its user by its
	// link, or by the account rules when it has none: rules.Account, the registration's
	// own, when it is set, and the library's standard rule when the former is nil or
	// answers signin.Standard, which reads what an administrator chose for the method
	// rules.Method names, the tenants' declarations of the
	// directory deciding a stranger first when rules.Directories is set; a password
	// identity names its user in req.UserID. rules.Policy then decides, and the
	// session is inserted with its auth events and custom session data. The sign-in
	// runs in two transactions that each commit unless a hook or the storage fails: a
	// refusal (a *sessioninfo.LoginRefusal) or a wait (a *signin.PendingSignInError)
	// keeps the hooks' own writes and writes no session. See the README's "Storage and
	// schema". A sign-in that rests on a password proof (rules.Proven) inserts its
	// session only while the user still holds the password that was proved, compared in
	// the transaction that inserts: once it was changed, reset or removed the sign-in
	// ends with ErrPasswordChanged, before the policy is asked, and no session is
	// written.
	SignIn(ctx context.Context, req *sessioninfo.NewSessionRequest, rules SignInRules[Tx]) (impulse.UUID, error)
	// Identity returns the link for (method, connection, subject), or a NotFound error.
	Identity(ctx context.Context, method sessioninfo.AuthMethod, connection, subject string) (*SessionIdentity, error)
	// IdentitiesByUser lists a user's identity links.
	IdentitiesByUser(ctx context.Context, userID impulse.UUID) ([]*SessionIdentity, error)
	// LinkIdentity links an identity to a user outside the sign-in flow.
	LinkIdentity(ctx context.Context, userID impulse.UUID, identity *sessioninfo.Identity, tenant string) (*SessionIdentity, error)
	// LinkConfirmedIdentity links an identity to the user whose password confirmed the
	// link, in one transaction, and gives the person the default seat of the tenant
	// whose directory the identity came through: directories reads the declaration and
	// seat gives the seat, handed the transaction, as at a sign-in that makes the link
	// itself (SignInRules; both nil for none). The link is made and the person seated
	// together, or neither. An identity linked meanwhile is a Conflict. proven is the
	// proof of the password that confirmed the link: the link is made only while the
	// user still holds it, compared in the transaction that links (ErrPasswordChanged
	// otherwise, nothing written).
	LinkConfirmedIdentity(
		ctx context.Context, userID impulse.UUID, username string, identity *sessioninfo.Identity, tenant string,
		directories signin.DirectoryReader[Tx], seat signinflow.DirectorySeater[Tx], proven PasswordProof,
	) error
	// UnlinkIdentity removes a link. It refuses to remove the last means of sign-in of
	// a user that has no password.
	UnlinkIdentity(ctx context.Context, identityID impulse.UUID) error
	// RemoveUserPassword removes a user's password, and with it the mark that the user
	// must change it. standing names the identities linked to the user that the caller
	// judged a means of sign-in (the links a sign-in is still possible through): the
	// removal goes through only while one of them is still linked to the user, read in
	// the transaction that removes the password, so a removal and an unlink that race
	// cannot both go through, and it refuses to remove the last means of sign-in
	// otherwise (ErrLastSignInMethod), a user with no identity and one whose identities
	// sign nobody in alike. The store does not judge a link: an identity standing does
	// not name is no means of sign-in to it. The user's sessions stand. A user that
	// holds no password is a BadRequest. It follows no proof of the password: it is an
	// administrator's removal. A user's own removal is RemoveProvenUserPassword.
	RemoveUserPassword(ctx context.Context, userID impulse.UUID, standing []impulse.UUID) error
	// RemoveProvenUserPassword removes the password a user proved, as RemoveUserPassword
	// removes one, under the same rule of the identities that stand. proven is the hash
	// the caller compared the user's password against, and the removal goes through
	// only while the row still holds it: a password changed, reset or removed since the
	// caller read it stands as it is (ErrPasswordChanged), answered before the user's
	// other means of sign-in are counted. A removal with no proven hash removes nothing.
	RemoveProvenUserPassword(ctx context.Context, userID impulse.UUID, proven *securehash.Hash, standing []impulse.UUID) error
	// SetTokensTableName sets the name of the tokens table (SessionTokens).
	SetTokensTableName(name string) error
	// SetDeviceCodesTableName sets the name of the device codes table
	// (SessionDeviceCodes).
	SetDeviceCodesTableName(name string) error
	// InsertToken inserts a token: created at the database's time, unused and not
	// revoked, whatever t carries.
	InsertToken(ctx context.Context, t *Token) error
	// Token returns the token holding tokenHash that is live now: not revoked and not
	// expired. Any other is NotFound.
	Token(ctx context.Context, tokenHash []byte) (*Token, error)
	// Tokens lists the live tokens of a user, newest first.
	Tokens(ctx context.Context, userID impulse.UUID) ([]*Token, error)
	// TouchToken moves a live token's LastUsedAt to the database's time; a token that
	// is not live is left as it is.
	TouchToken(ctx context.Context, id impulse.UUID) error
	// RevokeUserToken revokes the live token id of userID with reason: the token
	// alone, or its whole family when it has one. A token that is not live, or not the
	// user's, is NotFound.
	RevokeUserToken(ctx context.Context, userID, id impulse.UUID, reason TokenRevocation) error
	// RevokeUserTokens revokes every live token of a user with reason.
	RevokeUserTokens(ctx context.Context, userID impulse.UUID, reason TokenRevocation) error
	// ExchangeRefreshToken rotates the live refresh token holding tokenHash, in one
	// transaction: it is revoked as rotated and access and refresh are inserted in its
	// family, as its user, for its outlet and client, which the store fills in on the
	// two. A token that is not a live refresh token is NotFound; one already rotated is
	// a reuse, its whole family is revoked, and the error is ErrTokenReuse.
	ExchangeRefreshToken(ctx context.Context, tokenHash []byte, access, refresh *Token) error
	// InsertDeviceCode inserts a device authorization: created at the database's time
	// and pending, whatever d carries.
	InsertDeviceCode(ctx context.Context, d *DeviceCode) error
	// DeviceCode returns the device authorization userCode names that is pending now:
	// not decided, not consumed, not expired. Any other is NotFound.
	DeviceCode(ctx context.Context, userCode string) (*DeviceCode, error)
	// ApproveDeviceCode records that userID approved the pending device code id. A
	// code that is not pending is NotFound, so one decision stands.
	ApproveDeviceCode(ctx context.Context, id, userID impulse.UUID) error
	// DenyDeviceCode records that the person denied the pending device code id. A code
	// that is not pending is NotFound.
	DenyDeviceCode(ctx context.Context, id impulse.UUID) error
	// RedeemDeviceCode is the client's poll of the device code holding deviceCodeHash,
	// in one transaction: an approved code is consumed, once, and access and refresh
	// are inserted as the approving user's for the code's outlet and client, which the
	// store fills in on the two; a pending code polled before interval passed since the
	// last poll is slow_down and nothing is written; otherwise the status says what
	// the client learned. A code that is not there or was consumed is NotFound.
	RedeemDeviceCode(ctx context.Context, deviceCodeHash []byte, interval time.Duration, access, refresh *Token) (*DeviceCode, DeviceCodeStatus, error)
	// DestroyUserSessions expires every session of a user, by UserId.
	DestroyUserSessions(ctx context.Context, userID impulse.UUID) error
	// TouchUserSignIn moves a user's LastSignInAt to the database's time. The Auth calls
	// it once a sign-in has established its session, as a write of its own: inside the
	// sign-in's transaction the update would upgrade the share lock the flow holds on
	// the user row and deadlock two sign-ins of one user.
	TouchUserSignIn(ctx context.Context, userID impulse.UUID) error
	// AppendAuthEvent records a step-up or confirmation on an existing session.
	AppendAuthEvent(ctx context.Context, sessionID impulse.UUID, event *sessioninfo.AuthEvent) error
	// DestroySessionsByProviderSessionID expires every live session whose sign-in
	// carried the provider's session id under issuer, as a provider-initiated logout
	// asks; an impersonated session's record ends Logout. Both columns match: a session
	// recorded without an issuer never does.
	DestroySessionsByProviderSessionID(ctx context.Context, issuer, providerSessionID string) error
	// ProviderSessionIssuers lists the distinct issuers under which live sessions carry
	// the provider's session id, for a provider-initiated logout that names no issuer on
	// a registration serving several: the handler ends the sessions under those the
	// registration declares. A session recorded without an issuer is left out.
	ProviderSessionIssuers(ctx context.Context, providerSessionID string) ([]string, error)
	// SetIdentitiesTableName sets the name of the identities table. (default: SessionIdentities)
	SetIdentitiesTableName(name string) error
	// SetPendingSignInsTableName sets the name of the pending sign-ins table. (default: SessionPendingSignIns)
	SetPendingSignInsTableName(name string) error
	// HoldSignIn stores a sign-in that waits for a confirmation or the application's MFA
	// step and returns its row's ID. p.TokenHash is the hash of the opaque token the
	// browser's pending cookie carries. A sign-in that rests on a password proof
	// (p.PasswordProof) is stored only while its user still holds the password that was
	// proved, compared in the transaction that inserts the row (ErrPasswordChanged
	// otherwise), and the row keeps the proof for the completion to be held to.
	HoldSignIn(ctx context.Context, p *PendingSignIn) (impulse.UUID, error)
	// PendingSignIn returns the live pending sign-in holding tokenHash: not consumed and
	// not expired. Any other is NotFound.
	PendingSignIn(ctx context.Context, tokenHash []byte) (*PendingSignIn, error)
	// ConsumePendingSignIn consumes the live pending sign-in holding tokenHash by a
	// conditional update, so it is consumed once; a pending sign-in that is not live
	// (consumed, expired or unknown) is NotFound.
	ConsumePendingSignIn(ctx context.Context, tokenHash []byte) error
	// SetSignInAttemptsTableName sets the name of the sign-in attempts table. (default: SessionSignInAttempts)
	SetSignInAttemptsTableName(name string) error
	// SignInAttempts returns the sign-in attempts record of name, the submitted name as
	// the Auth normalized it: its failed password attempts in the window and its lock.
	// A name with no record is NotFound.
	SignInAttempts(ctx context.Context, name string) (*SignInAttempts, error)
	// RecordSignInFailure records a failed password attempt on name, at the time of
	// the call, under limit, and returns the record as written: the failure counts in
	// the name's window, or opens one, and the failure that reaches limit.Failures
	// locks the name for limit.Lock; a name that is locked is left as it is. The read
	// and the write are one transaction, so concurrent failures each count.
	RecordSignInFailure(ctx context.Context, name string, limit SignInAttemptLimit) (*SignInAttempts, error)
	// ClearSignInAttempts deletes the sign-in attempts record of name: a successful
	// password check, an MFA completion and a password change reset the count.
	ClearSignInAttempts(ctx context.Context, name string) error
	// SetMailLinksTableName sets the name of the mail links table. (default: SessionMailLinks)
	SetMailLinksTableName(name string) error
	// SetSignInMethodsTableName sets the name of the sign-in methods table. (default: SessionSignInMethods)
	SetSignInMethodsTableName(name string) error
	// SignInMethods lists the rows of the sign-in methods table, by name: what an
	// administrator chose for each sign-in method they made a choice for. A method with
	// no row is invite only and is not among them.
	SignInMethods(ctx context.Context) ([]*SignInMethod, error)
	// SetSignInMethod writes the row of the sign-in method m names, replacing the one
	// it had. It holds no rule: whether the name is a registered method, and whether an
	// open method names a role, are the caller's to decide.
	SetSignInMethod(ctx context.Context, m *SignInMethod) error
	// SignUp makes the account of a person who signs up with a password through the
	// method registered under method, in one transaction, and returns its id: the method
	// must be open to sign-up for audience, whom its registration admits
	// (signin.Audience.Key), as its row says in that transaction, the mail link linkID
	// is consumed, the user row is written, and seat gives the method's default role,
	// handed the transaction (nil for none). A method that is not open and a link that
	// is no longer live are NotFound, a taken username or address a Conflict, and
	// nothing is written by a refusal.
	SignUp(ctx context.Context, method, audience string, user *InsertSessionUser, linkID impulse.UUID, seat signinflow.SignUpSeater[Tx]) (impulse.UUID, error)
	// SetUsernameIsEmail declares the users table in the shape where the address is
	// the username, generated by the database: the store never writes Email, refuses
	// a rename, and writes a verified address as the username (see the README's
	// "Users and their email address"). The password method's UsernameIsEmail option
	// sets it.
	SetUsernameIsEmail(same bool)
	// UsernameIsEmail reports whether the address is the username.
	UsernameIsEmail() bool
	// UserByEmail returns the user holding the address, verified or not, matched
	// without regard to case, or a NotFound error.
	UserByEmail(ctx context.Context, email string) (*SessionUser, error)
	// SetUserEmail puts an address on a user by hand, unverified, or takes it off
	// (nil): an administrator's entry, which reserves the address. An address another
	// user holds is a Conflict; on a store whose usernames are the addresses the
	// entry is refused.
	SetUserEmail(ctx context.Context, id impulse.UUID, email *string) error
	// VerifyUserEmail writes the address the person has just proven they read,
	// verified at the database's time, replacing whatever the user held; on a store
	// whose usernames are the addresses it is written as the username, renaming the
	// user and its live sessions. An address another user holds is a Conflict.
	VerifyUserEmail(ctx context.Context, id impulse.UUID, email string) error
	// ClaimVerifiedEmail writes email on the user as verified when the user holds no
	// verified address yet and no other user holds it, and reports whether it wrote:
	// a Google sign-in's verified address. On a store whose usernames are the
	// addresses only the verified time is written, and only when the address is the
	// username.
	ClaimVerifiedEmail(ctx context.Context, id impulse.UUID, email string) (bool, error)
	// SetUserName writes the person's name on a user, or takes it off (nil, or nothing
	// but space): the person's own change. It replaces whatever the user held; a name
	// longer than the store keeps is a BadRequest.
	SetUserName(ctx context.Context, id impulse.UUID, name *string) error
	// ClaimUserName writes name on the user when the user holds none, and reports
	// whether it wrote: the name a directory asserted at a sign-in. A user that holds
	// a name keeps it, so a name the person wrote themselves is never replaced.
	ClaimUserName(ctx context.Context, id impulse.UUID, name string) (bool, error)
	// SetUserLook writes the look the person chose on a user (sessioninfo.Look): the
	// person's own change, and nobody else's. It replaces the look held as a whole, so
	// a half the look does not name is taken off; a scheme that is neither light nor
	// dark, and a palette id longer than the store keeps, is a BadRequest.
	SetUserLook(ctx context.Context, id impulse.UUID, look sessioninfo.Look) error
	// InsertMailLink stores a mailed link's row: l.TokenHash is the hash of the opaque
	// token the mail carries. The row's ID is set on l.
	InsertMailLink(ctx context.Context, l *MailLink) error
	// MailLink returns the live mail link holding tokenHash: not consumed and not
	// expired. Any other is NotFound.
	MailLink(ctx context.Context, tokenHash []byte) (*MailLink, error)
	// LiveMailLinks lists the live links of kind, oldest first: the invitations that
	// still stand.
	LiveMailLinks(ctx context.Context, kind MailLinkKind) ([]*MailLink, error)
	// ConsumeMailLink consumes the live mail link holding tokenHash by a conditional
	// update, so it is consumed once; a link that is not live is NotFound.
	ConsumeMailLink(ctx context.Context, tokenHash []byte) error
	// RevokeMailLink consumes the live mail link id: an invitation withdrawn by hand.
	// A link that is not live is NotFound.
	RevokeMailLink(ctx context.Context, id impulse.UUID) error
	// SetFactorCodesTableName sets the name of the factor codes table.
	SetFactorCodesTableName(name string) error
	// SetTrustedDevicesTableName sets the name of the trusted devices table.
	SetTrustedDevicesTableName(name string) error
	// InsertFactorCode consumes the earlier live codes of c's pending sign-in and
	// inserts c, in one transaction, so one code stands for a pending sign-in at a
	// time; it gives c its ID.
	InsertFactorCode(ctx context.Context, c *FactorCode) error
	// FactorCode returns the live code of pendingID, or NotFound.
	FactorCode(ctx context.Context, pendingID impulse.UUID) (*FactorCode, error)
	// RecordFactorCodeAttempt counts a wrong code against the live code id and
	// returns the count; NotFound when it is not live.
	RecordFactorCodeAttempt(ctx context.Context, id impulse.UUID) (int, error)
	// ConsumeFactorCode marks the live code id consumed, by a conditional update, so
	// a code is used once; NotFound when it is not live.
	ConsumeFactorCode(ctx context.Context, id impulse.UUID) error
	// InsertTrustedDevice inserts d, giving it its ID.
	InsertTrustedDevice(ctx context.Context, d *TrustedDevice) error
	// TrustedDevice returns the device holding tokenHash that is live (not revoked,
	// not expired), or NotFound.
	TrustedDevice(ctx context.Context, tokenHash []byte) (*TrustedDevice, error)
	// TouchTrustedDevice moves the LastUsedAt of the live device id to the database's
	// time.
	TouchTrustedDevice(ctx context.Context, id impulse.UUID) error
	// RevokeTrustedDevice revokes the live device holding tokenHash; NotFound when it
	// is not live.
	RevokeTrustedDevice(ctx context.Context, tokenHash []byte) error
	// RevokeTrustedDevices revokes every device of userID that is not revoked yet: an
	// administrator's reset of the person's factors.
	RevokeTrustedDevices(ctx context.Context, userID impulse.UUID) error
	// AcceptInvitation accepts inv for user userID in one transaction: the
	// invitation is consumed, the user's address is set from it when the user holds
	// no verified address, and accept, the application's hook (nil for none), gives
	// what the invitation names, handed the transaction; what the hook asks to have
	// run after the commit runs once it has landed. An invitation that is no longer
	// live is NotFound (signin.ErrInvitationExpired its cause), and nothing is
	// written; a hook that fails is its error, and nothing is written either.
	AcceptInvitation(ctx context.Context, userID impulse.UUID, username, email string, inv *sessioninfo.Invitation, accept signinflow.InvitationAcceptor[Tx]) error
}

// MailLink is a row of the mail links table: one single-use link the library mailed.
type MailLink = dbtype.MailLink

// MailLinkKind is what a mailed link does when it is followed.
type MailLinkKind = dbtype.MailLinkKind

// The mail link kinds.
const (
	MailLinkInvitation   = dbtype.MailLinkInvitation
	MailLinkVerification = dbtype.MailLinkVerification
	MailLinkSignUp       = dbtype.MailLinkSignUp
)

// SignInMethod is a row of the sign-in methods table: what an administrator chose for
// one sign-in method, whether it is open to sign-up and the role a person who signs up
// through it is given.
type SignInMethod = dbtype.SignInMethod

// SignInAttempts is the sign-in attempts record of a name: the failed password
// attempts in its window and the lock they earned.
type SignInAttempts = dbtype.SignInAttempts

// SignInAttemptLimit is the rule a failure is recorded under: Failures within Window
// lock the name for Lock.
type SignInAttemptLimit = dbtype.SignInAttemptLimit

// PendingSignIn is a sign-in held in the pending sign-ins table until its confirmation
// or MFA step.
type PendingSignIn = dbtype.PendingSignIn

// FactorCode is a row of the factor codes table: one code the library mailed for a
// pending sign-in's second factor. See "The second factor" in the README.
type FactorCode = dbtype.FactorCode

// TrustedDevice is a row of the trusted devices table: a browser the person asked to
// be remembered after a second factor. See "The second factor" in the README.
type TrustedDevice = dbtype.TrustedDevice

// Token is a row of the tokens table: one bearer credential of a machine outlet. See
// dbtype.Token.
type Token = dbtype.Token

// TokenKind is what a token is: an application token a person issued, or the access
// or refresh token of a device grant.
type TokenKind = dbtype.TokenKind

// The token kinds.
const (
	TokenApplication = dbtype.TokenApplication
	TokenAccess      = dbtype.TokenAccess
	TokenRefresh     = dbtype.TokenRefresh
)

// TokenRevocation says why a token was revoked.
type TokenRevocation = dbtype.TokenRevocation

// The token revocations.
const (
	TokenRevokedByHand   = dbtype.TokenRevokedByHand
	TokenRevokedRotated  = dbtype.TokenRevokedRotated
	TokenRevokedReuse    = dbtype.TokenRevokedReuse
	TokenRevokedWithUser = dbtype.TokenRevokedWithUser
)

// ErrTokenReuse is the error of a refresh exchange given a token already rotated: the
// family is revoked.
var ErrTokenReuse = dbtype.ErrTokenReuse

// DeviceCode is a row of the device codes table: one device authorization a
// command-line client started. See dbtype.DeviceCode.
type DeviceCode = dbtype.DeviceCode

// DeviceCodeStatus is what a client's poll of its device code learned.
type DeviceCodeStatus = dbtype.DeviceCodeStatus

// The device code statuses.
const (
	DevicePending  = dbtype.DevicePending
	DeviceSlowDown = dbtype.DeviceSlowDown
	DeviceApproved = dbtype.DeviceApproved
	DeviceDenied   = dbtype.DeviceDenied
	DeviceExpired  = dbtype.DeviceExpired
)

var (
	_ AuthStore[pgx.Tx]                             = (*Auth[pgx.Tx])(nil)
	_ AuthStore[*cloudspanner.ReadWriteTransaction] = (*Auth[*cloudspanner.ReadWriteTransaction])(nil)
)

// Auth is the storage for Auth sessions: their users, identities and auth events. Tx is
// the database's transaction type.
type Auth[Tx any] struct {
	userStorage
	// flow is the driver as the sign-in flow drives it, typed by its transaction.
	flow signinflow.Driver[Tx]
	// custom attaches the custom data configurations to the driver.
	custom customDataAttacher[Tx]
}

// NewSpannerAuth creates Spanner Auth storage over the auth schema (the base
// migrations plus schema/spanner/auth/migrations). Attach the auth events and
// impersonation tables with WithAuthEvents and WithImpersonation, and custom session
// and user data with the store's WithCustomSessionData and WithCustomUserData.
func NewSpannerAuth(client *cloudspanner.Client, opts ...SpannerOption) *Auth[*cloudspanner.ReadWriteTransaction] {
	driver := spanner.NewSessionStorageDriver(client)
	driver.EnableAuth()
	for _, opt := range opts {
		opt.applySpanner(driver)
	}

	return &Auth[*cloudspanner.ReadWriteTransaction]{userStorage: userStorage{sessionStorage: sessionStorage{db: driver}}, flow: driver, custom: spannerCustomData{driver}}
}

// NewPostgresAuth creates PostgreSQL Auth storage over the auth schema (the base
// migrations plus schema/postgresql/auth/migrations). Attach the auth events and
// impersonation tables with WithAuthEvents and WithImpersonation, and custom session
// and user data with the store's WithCustomSessionData and WithCustomUserData.
func NewPostgresAuth(pg postgres.Queryer, opts ...PostgresOption) *Auth[pgx.Tx] {
	driver := postgres.NewSessionStorageDriver(pg)
	driver.EnableAuth()
	for _, opt := range opts {
		opt.applyPostgres(driver)
	}

	return &Auth[pgx.Tx]{userStorage: userStorage{sessionStorage: sessionStorage{db: driver}}, flow: driver, custom: postgresCustomData{driver}}
}

// SignInRules are the application's rules a sign-in runs under, handed to
// AuthStore.SignIn: the registration's own account rule (nil for none) and the name the
// sign-in's method is registered under, by which the standard rule reads what an
// administrator chose for it ("" for a sign-in through no registered method), the
// sign-in policy (nil for none), the invitation acceptor (nil for none), the hook that
// gives a person who signed up through an open method its default role (nil for none),
// and a tenanted application's reader of the tenants' directory declarations with the
// hook that seats a person at their first sign-in through a tenant's directory (both
// nil for none). They carry the proof of the password the sign-in rests on too (nil for
// none), which the session's insert is held to.
type SignInRules[Tx any] struct {
	Account signin.AccountRule[Tx]
	Method  string
	// Audience is whom the method's registration admits now (signin.Audience.Key): the
	// method's row opens it only for the audience it was opened for.
	Audience    string
	Policy      signin.Policy[Tx]
	Accept      signinflow.InvitationAcceptor[Tx]
	SignUp      signinflow.SignUpSeater[Tx]
	Directories signin.DirectoryReader[Tx]
	Seat        signinflow.DirectorySeater[Tx]
	// TrustedDevice reports whether the browser signing in is a trusted device of
	// the user, which stands in for the second factor a RequireMFA asks; nil for none.
	TrustedDevice signinflow.TrustedDeviceCheck
	// Proven is the proof of the password the sign-in rests on (ProofOfPassword): the
	// one just entered at a login or a confirmation, or the one a pending sign-in was
	// held with. Nil when no password was proved.
	Proven PasswordProof
}

// SignIn implements AuthStore.
func (a *Auth[Tx]) SignIn(ctx context.Context, req *sessioninfo.NewSessionRequest, rules SignInRules[Tx]) (impulse.UUID, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	id, err := impulse.NewUUID()
	if err != nil {
		return impulse.NilUUID, errors.Wrap(err, "impulse.NewUUID()")
	}
	session := &dbtype.InsertSession{Username: req.Username}

	flowRules := signinflow.Rules[Tx]{
		Account: rules.Account, Method: rules.Method, Audience: rules.Audience, Policy: rules.Policy, Accept: rules.Accept, SignUp: rules.SignUp,
		Directories: rules.Directories, Seat: rules.Seat, TrustedDevice: rules.TrustedDevice, Proven: rules.Proven,
	}
	if err := signinflow.SignIn(ctx, a.flow, id, session, req, flowRules); err != nil {
		return impulse.NilUUID, errors.Wrap(err, "signinflow.SignIn()")
	}

	return id, nil
}

// AcceptInvitation implements AuthStore.
func (a *Auth[Tx]) AcceptInvitation(ctx context.Context, userID impulse.UUID, username, email string, inv *sessioninfo.Invitation, accept signinflow.InvitationAcceptor[Tx]) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := signinflow.AcceptInvitation(ctx, a.flow, userID, username, email, inv, accept); err != nil {
		return errors.Wrap(err, "signinflow.AcceptInvitation()")
	}

	return nil
}

// SetMailLinksTableName implements AuthStore.
func (a *Auth[Tx]) SetMailLinksTableName(name string) error {
	if err := a.db.SetMailLinksTableName(name); err != nil {
		return errors.Wrap(err, "db.SetMailLinksTableName()")
	}

	return nil
}

// SetSignInMethodsTableName implements AuthStore.
func (a *Auth[Tx]) SetSignInMethodsTableName(name string) error {
	if err := a.db.SetSignInMethodsTableName(name); err != nil {
		return errors.Wrap(err, "db.SetSignInMethodsTableName()")
	}

	return nil
}

// SignInMethods implements AuthStore.
func (a *Auth[Tx]) SignInMethods(ctx context.Context) ([]*SignInMethod, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	methods, err := a.db.SignInMethods(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "db.SignInMethods()")
	}

	return methods, nil
}

// SetSignInMethod implements AuthStore.
func (a *Auth[Tx]) SetSignInMethod(ctx context.Context, m *SignInMethod) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := a.db.SetSignInMethod(ctx, m); err != nil {
		return errors.Wrap(err, "db.SetSignInMethod()")
	}

	return nil
}

// SignUp implements AuthStore.
func (a *Auth[Tx]) SignUp(ctx context.Context, method, audience string, user *InsertSessionUser, linkID impulse.UUID, seat signinflow.SignUpSeater[Tx]) (impulse.UUID, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	id, err := signinflow.SignUp(ctx, a.flow, method, audience, user, linkID, seat)
	if err != nil {
		return impulse.NilUUID, errors.Wrap(err, "signinflow.SignUp()")
	}

	return id, nil
}

// SetUsernameIsEmail implements AuthStore.
func (a *Auth[Tx]) SetUsernameIsEmail(same bool) {
	a.db.SetUsernameIsEmail(same)
}

// UsernameIsEmail implements AuthStore.
func (a *Auth[Tx]) UsernameIsEmail() bool {
	return a.db.UsernameIsEmail()
}

// UserByEmail implements AuthStore.
func (a *Auth[Tx]) UserByEmail(ctx context.Context, email string) (*SessionUser, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	user, err := a.db.UserByEmail(ctx, email)
	if err != nil {
		return nil, errors.Wrap(err, "db.UserByEmail()")
	}

	return user, nil
}

// SetUserEmail implements AuthStore.
func (a *Auth[Tx]) SetUserEmail(ctx context.Context, id impulse.UUID, email *string) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := a.db.SetUserEmail(ctx, id, email); err != nil {
		return errors.Wrap(err, "db.SetUserEmail()")
	}

	return nil
}

// VerifyUserEmail implements AuthStore.
func (a *Auth[Tx]) VerifyUserEmail(ctx context.Context, id impulse.UUID, email string) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := a.db.VerifyUserEmail(ctx, id, email); err != nil {
		return errors.Wrap(err, "db.VerifyUserEmail()")
	}

	return nil
}

// ClaimVerifiedEmail implements AuthStore.
func (a *Auth[Tx]) ClaimVerifiedEmail(ctx context.Context, id impulse.UUID, email string) (bool, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	claimed, err := a.db.ClaimVerifiedEmail(ctx, id, email)
	if err != nil {
		return false, errors.Wrap(err, "db.ClaimVerifiedEmail()")
	}

	return claimed, nil
}

// SetUserName implements AuthStore.
func (a *Auth[Tx]) SetUserName(ctx context.Context, id impulse.UUID, name *string) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := a.db.SetUserName(ctx, id, name); err != nil {
		return errors.Wrap(err, "db.SetUserName()")
	}

	return nil
}

// SetUserLook implements AuthStore.
func (a *Auth[Tx]) SetUserLook(ctx context.Context, id impulse.UUID, look sessioninfo.Look) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := a.db.SetUserLook(ctx, id, look); err != nil {
		return errors.Wrap(err, "db.SetUserLook()")
	}

	return nil
}

// ClaimUserName implements AuthStore.
func (a *Auth[Tx]) ClaimUserName(ctx context.Context, id impulse.UUID, name string) (bool, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	claimed, err := a.db.ClaimUserName(ctx, id, name)
	if err != nil {
		return false, errors.Wrap(err, "db.ClaimUserName()")
	}

	return claimed, nil
}

// InsertMailLink implements AuthStore.
func (a *Auth[Tx]) InsertMailLink(ctx context.Context, l *MailLink) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	id, err := impulse.NewUUID()
	if err != nil {
		return errors.Wrap(err, "impulse.NewUUID()")
	}
	l.ID = id
	if err := a.db.InsertMailLink(ctx, l); err != nil {
		return errors.Wrap(err, "db.InsertMailLink()")
	}

	return nil
}

// MailLink implements AuthStore.
func (a *Auth[Tx]) MailLink(ctx context.Context, tokenHash []byte) (*MailLink, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	l, err := a.db.MailLinkByToken(ctx, tokenHash, time.Now())
	if err != nil {
		return nil, errors.Wrap(err, "db.MailLinkByToken()")
	}

	return l, nil
}

// LiveMailLinks implements AuthStore.
func (a *Auth[Tx]) LiveMailLinks(ctx context.Context, kind MailLinkKind) ([]*MailLink, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	links, err := a.db.LiveMailLinks(ctx, kind, time.Now())
	if err != nil {
		return nil, errors.Wrap(err, "db.LiveMailLinks()")
	}

	return links, nil
}

// ConsumeMailLink implements AuthStore.
func (a *Auth[Tx]) ConsumeMailLink(ctx context.Context, tokenHash []byte) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := a.db.ConsumeMailLink(ctx, tokenHash, time.Now()); err != nil {
		return errors.Wrap(err, "db.ConsumeMailLink()")
	}

	return nil
}

// RevokeMailLink implements AuthStore.
func (a *Auth[Tx]) RevokeMailLink(ctx context.Context, id impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := a.db.RevokeMailLink(ctx, id, time.Now()); err != nil {
		return errors.Wrap(err, "db.RevokeMailLink()")
	}

	return nil
}

// SetFactorCodesTableName implements AuthStore.
func (a *Auth[Tx]) SetFactorCodesTableName(name string) error {
	if err := a.db.SetFactorCodesTableName(name); err != nil {
		return errors.Wrap(err, "db.SetFactorCodesTableName()")
	}

	return nil
}

// SetTrustedDevicesTableName implements AuthStore.
func (a *Auth[Tx]) SetTrustedDevicesTableName(name string) error {
	if err := a.db.SetTrustedDevicesTableName(name); err != nil {
		return errors.Wrap(err, "db.SetTrustedDevicesTableName()")
	}

	return nil
}

// InsertFactorCode implements AuthStore.
func (a *Auth[Tx]) InsertFactorCode(ctx context.Context, c *FactorCode) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	id, err := impulse.NewUUID()
	if err != nil {
		return errors.Wrap(err, "impulse.NewUUID()")
	}
	c.ID = id
	if err := a.db.InsertFactorCode(ctx, c, time.Now()); err != nil {
		return errors.Wrap(err, "db.InsertFactorCode()")
	}

	return nil
}

// FactorCode implements AuthStore.
func (a *Auth[Tx]) FactorCode(ctx context.Context, pendingID impulse.UUID) (*FactorCode, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	c, err := a.db.FactorCodeByPending(ctx, pendingID, time.Now())
	if err != nil {
		return nil, errors.Wrap(err, "db.FactorCodeByPending()")
	}

	return c, nil
}

// RecordFactorCodeAttempt implements AuthStore.
func (a *Auth[Tx]) RecordFactorCodeAttempt(ctx context.Context, id impulse.UUID) (int, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	attempts, err := a.db.RecordFactorCodeAttempt(ctx, id, time.Now())
	if err != nil {
		return 0, errors.Wrap(err, "db.RecordFactorCodeAttempt()")
	}

	return attempts, nil
}

// ConsumeFactorCode implements AuthStore.
func (a *Auth[Tx]) ConsumeFactorCode(ctx context.Context, id impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := a.db.ConsumeFactorCode(ctx, id, time.Now()); err != nil {
		return errors.Wrap(err, "db.ConsumeFactorCode()")
	}

	return nil
}

// InsertTrustedDevice implements AuthStore.
func (a *Auth[Tx]) InsertTrustedDevice(ctx context.Context, d *TrustedDevice) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	id, err := impulse.NewUUID()
	if err != nil {
		return errors.Wrap(err, "impulse.NewUUID()")
	}
	d.ID = id
	if err := a.db.InsertTrustedDevice(ctx, d); err != nil {
		return errors.Wrap(err, "db.InsertTrustedDevice()")
	}

	return nil
}

// TrustedDevice implements AuthStore.
func (a *Auth[Tx]) TrustedDevice(ctx context.Context, tokenHash []byte) (*TrustedDevice, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	d, err := a.db.TrustedDeviceByToken(ctx, tokenHash, time.Now())
	if err != nil {
		return nil, errors.Wrap(err, "db.TrustedDeviceByToken()")
	}

	return d, nil
}

// TouchTrustedDevice implements AuthStore.
func (a *Auth[Tx]) TouchTrustedDevice(ctx context.Context, id impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := a.db.TouchTrustedDevice(ctx, id, time.Now()); err != nil {
		return errors.Wrap(err, "db.TouchTrustedDevice()")
	}

	return nil
}

// RevokeTrustedDevice implements AuthStore.
func (a *Auth[Tx]) RevokeTrustedDevice(ctx context.Context, tokenHash []byte) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := a.db.RevokeTrustedDevice(ctx, tokenHash, time.Now()); err != nil {
		return errors.Wrap(err, "db.RevokeTrustedDevice()")
	}

	return nil
}

// RevokeTrustedDevices implements AuthStore.
func (a *Auth[Tx]) RevokeTrustedDevices(ctx context.Context, userID impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := a.db.RevokeTrustedDevices(ctx, userID); err != nil {
		return errors.Wrap(err, "db.RevokeTrustedDevices()")
	}

	return nil
}

// Users implements AuthStore.
func (a *Auth[Tx]) Users(ctx context.Context) ([]*SessionUser, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	users, err := a.db.Users(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "db.Users()")
	}

	return users, nil
}

// SetUserPassword implements AuthStore.
func (a *Auth[Tx]) SetUserPassword(ctx context.Context, id impulse.UUID, hash *securehash.Hash, changeRequired bool) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := a.db.SetUserPassword(ctx, id, hash, changeRequired); err != nil {
		return errors.Wrap(err, "db.SetUserPassword()")
	}

	return nil
}

// ChangeUserPassword implements AuthStore.
func (a *Auth[Tx]) ChangeUserPassword(ctx context.Context, id impulse.UUID, proven, hash *securehash.Hash) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if hash == nil {
		return errors.New("ChangeUserPassword needs the new password's hash: RemoveProvenUserPassword removes a password")
	}
	if err := a.db.ChangeUserPassword(ctx, id, proven, hash); err != nil {
		return errors.Wrap(err, "db.ChangeUserPassword()")
	}

	return nil
}

// Identity implements AuthStore.
func (a *Auth[Tx]) Identity(ctx context.Context, method sessioninfo.AuthMethod, connection, subject string) (*SessionIdentity, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	link, err := a.db.Identity(ctx, method, connection, subject)
	if err != nil {
		return nil, errors.Wrap(err, "db.Identity()")
	}

	return (*SessionIdentity)(link), nil
}

// IdentitiesByUser implements AuthStore.
func (a *Auth[Tx]) IdentitiesByUser(ctx context.Context, userID impulse.UUID) ([]*SessionIdentity, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	links, err := a.db.IdentitiesByUser(ctx, userID)
	if err != nil {
		return nil, errors.Wrap(err, "db.IdentitiesByUser()")
	}

	identities := make([]*SessionIdentity, len(links))
	for i, link := range links {
		identities[i] = (*SessionIdentity)(link)
	}

	return identities, nil
}

// LinkIdentity implements AuthStore.
func (a *Auth[Tx]) LinkIdentity(ctx context.Context, userID impulse.UUID, identity *sessioninfo.Identity, tenant string) (*SessionIdentity, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	link, err := a.db.LinkIdentity(ctx, userID, identity, tenant)
	if err != nil {
		return nil, errors.Wrap(err, "db.LinkIdentity()")
	}

	return (*SessionIdentity)(link), nil
}

// LinkConfirmedIdentity implements AuthStore.
func (a *Auth[Tx]) LinkConfirmedIdentity(
	ctx context.Context, userID impulse.UUID, username string, identity *sessioninfo.Identity, tenant string,
	directories signin.DirectoryReader[Tx], seat signinflow.DirectorySeater[Tx], proven PasswordProof,
) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := signinflow.LinkConfirmed(ctx, a.flow, userID, username, identity, tenant, directories, seat, proven); err != nil {
		return errors.Wrap(err, "signinflow.LinkConfirmed()")
	}

	return nil
}

// UnlinkIdentity implements AuthStore.
func (a *Auth[Tx]) UnlinkIdentity(ctx context.Context, identityID impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := a.db.UnlinkIdentity(ctx, identityID); err != nil {
		return errors.Wrap(err, "db.UnlinkIdentity()")
	}

	return nil
}

// RemoveUserPassword implements AuthStore.
func (a *Auth[Tx]) RemoveUserPassword(ctx context.Context, userID impulse.UUID, standing []impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := a.db.RemoveUserPassword(ctx, userID, standing); err != nil {
		return errors.Wrap(err, "db.RemoveUserPassword()")
	}

	return nil
}

// RemoveProvenUserPassword implements AuthStore.
func (a *Auth[Tx]) RemoveProvenUserPassword(ctx context.Context, userID impulse.UUID, proven *securehash.Hash, standing []impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := a.db.RemoveProvenUserPassword(ctx, userID, proven, standing); err != nil {
		return errors.Wrap(err, "db.RemoveProvenUserPassword()")
	}

	return nil
}

// DestroyUserSessions implements AuthStore.
func (a *Auth[Tx]) DestroyUserSessions(ctx context.Context, userID impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := a.db.DestroyUserSessions(ctx, userID); err != nil {
		return errors.Wrap(err, "db.DestroyUserSessions()")
	}

	return nil
}

// TouchUserSignIn implements AuthStore.
func (a *Auth[Tx]) TouchUserSignIn(ctx context.Context, userID impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := a.db.TouchUserSignIn(ctx, userID); err != nil {
		return errors.Wrap(err, "db.TouchUserSignIn()")
	}

	return nil
}

// AppendAuthEvent implements AuthStore.
func (a *Auth[Tx]) AppendAuthEvent(ctx context.Context, sessionID impulse.UUID, event *sessioninfo.AuthEvent) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := a.db.AppendAuthEvent(ctx, sessionID, event); err != nil {
		return errors.Wrap(err, "db.AppendAuthEvent()")
	}

	return nil
}

// HoldSignIn implements AuthStore.
func (a *Auth[Tx]) HoldSignIn(ctx context.Context, p *PendingSignIn) (impulse.UUID, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	id, err := impulse.NewUUID()
	if err != nil {
		return impulse.NilUUID, errors.Wrap(err, "impulse.NewUUID()")
	}
	p.ID = id
	if err := a.db.InsertPendingSignIn(ctx, p); err != nil {
		return impulse.NilUUID, errors.Wrap(err, "db.InsertPendingSignIn()")
	}

	return id, nil
}

// PendingSignIn implements AuthStore.
func (a *Auth[Tx]) PendingSignIn(ctx context.Context, tokenHash []byte) (*PendingSignIn, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	p, err := a.db.PendingSignInByToken(ctx, tokenHash, time.Now())
	if err != nil {
		return nil, errors.Wrap(err, "db.PendingSignInByToken()")
	}

	return p, nil
}

// ConsumePendingSignIn implements AuthStore.
func (a *Auth[Tx]) ConsumePendingSignIn(ctx context.Context, tokenHash []byte) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := a.db.ConsumePendingSignIn(ctx, tokenHash, time.Now()); err != nil {
		return errors.Wrap(err, "db.ConsumePendingSignIn()")
	}

	return nil
}

// SetIdentitiesTableName implements AuthStore.
func (a *Auth[Tx]) SetIdentitiesTableName(name string) error {
	if err := a.db.SetIdentitiesTableName(name); err != nil {
		return errors.Wrap(err, "db.SetIdentitiesTableName()")
	}

	return nil
}

// SetPendingSignInsTableName implements AuthStore.
func (a *Auth[Tx]) SetPendingSignInsTableName(name string) error {
	if err := a.db.SetPendingSignInsTableName(name); err != nil {
		return errors.Wrap(err, "db.SetPendingSignInsTableName()")
	}

	return nil
}

// DestroySessionsByProviderSessionID implements AuthStore.
func (a *Auth[Tx]) DestroySessionsByProviderSessionID(ctx context.Context, issuer, providerSessionID string) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := a.db.DestroySessionsByProviderSessionID(ctx, issuer, providerSessionID); err != nil {
		return errors.Wrap(err, "db.DestroySessionsByProviderSessionID()")
	}

	return nil
}

// ProviderSessionIssuers implements AuthStore.
func (a *Auth[Tx]) ProviderSessionIssuers(ctx context.Context, providerSessionID string) ([]string, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	issuers, err := a.db.ProviderSessionIssuers(ctx, providerSessionID)
	if err != nil {
		return nil, errors.Wrap(err, "db.ProviderSessionIssuers()")
	}

	return issuers, nil
}

// SetSignInAttemptsTableName implements AuthStore.
func (a *Auth[Tx]) SetSignInAttemptsTableName(name string) error {
	if err := a.db.SetSignInAttemptsTableName(name); err != nil {
		return errors.Wrap(err, "db.SetSignInAttemptsTableName()")
	}

	return nil
}

// SignInAttempts implements AuthStore.
func (a *Auth[Tx]) SignInAttempts(ctx context.Context, name string) (*SignInAttempts, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	record, err := a.db.SignInAttempts(ctx, name)
	if err != nil {
		return nil, errors.Wrap(err, "db.SignInAttempts()")
	}

	return record, nil
}

// RecordSignInFailure implements AuthStore.
func (a *Auth[Tx]) RecordSignInFailure(ctx context.Context, name string, limit SignInAttemptLimit) (*SignInAttempts, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	record, err := a.db.RecordSignInFailure(ctx, name, time.Now(), limit)
	if err != nil {
		return nil, errors.Wrap(err, "db.RecordSignInFailure()")
	}

	return record, nil
}

// ClearSignInAttempts implements AuthStore.
func (a *Auth[Tx]) ClearSignInAttempts(ctx context.Context, name string) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := a.db.ClearSignInAttempts(ctx, name); err != nil {
		return errors.Wrap(err, "db.ClearSignInAttempts()")
	}

	return nil
}

// SetTokensTableName implements AuthStore.
func (a *Auth[Tx]) SetTokensTableName(name string) error {
	if err := a.db.SetTokensTableName(name); err != nil {
		return errors.Wrap(err, "db.SetTokensTableName()")
	}

	return nil
}

// SetDeviceCodesTableName implements AuthStore.
func (a *Auth[Tx]) SetDeviceCodesTableName(name string) error {
	if err := a.db.SetDeviceCodesTableName(name); err != nil {
		return errors.Wrap(err, "db.SetDeviceCodesTableName()")
	}

	return nil
}

// InsertToken implements AuthStore.
func (a *Auth[Tx]) InsertToken(ctx context.Context, t *Token) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := a.db.InsertToken(ctx, t); err != nil {
		return errors.Wrap(err, "db.InsertToken()")
	}

	return nil
}

// Token implements AuthStore.
func (a *Auth[Tx]) Token(ctx context.Context, tokenHash []byte) (*Token, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	t, err := a.db.TokenByHash(ctx, tokenHash, time.Now())
	if err != nil {
		return nil, errors.Wrap(err, "db.TokenByHash()")
	}

	return t, nil
}

// Tokens implements AuthStore.
func (a *Auth[Tx]) Tokens(ctx context.Context, userID impulse.UUID) ([]*Token, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	tokens, err := a.db.TokensByUser(ctx, userID, time.Now())
	if err != nil {
		return nil, errors.Wrap(err, "db.TokensByUser()")
	}

	return tokens, nil
}

// TouchToken implements AuthStore.
func (a *Auth[Tx]) TouchToken(ctx context.Context, id impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := a.db.TouchToken(ctx, id, time.Now()); err != nil {
		return errors.Wrap(err, "db.TouchToken()")
	}

	return nil
}

// RevokeUserToken implements AuthStore.
func (a *Auth[Tx]) RevokeUserToken(ctx context.Context, userID, id impulse.UUID, reason TokenRevocation) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := a.db.RevokeUserToken(ctx, userID, id, time.Now(), reason); err != nil {
		return errors.Wrap(err, "db.RevokeUserToken()")
	}

	return nil
}

// RevokeUserTokens implements AuthStore.
func (a *Auth[Tx]) RevokeUserTokens(ctx context.Context, userID impulse.UUID, reason TokenRevocation) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := a.db.RevokeUserTokens(ctx, userID, reason); err != nil {
		return errors.Wrap(err, "db.RevokeUserTokens()")
	}

	return nil
}

// ExchangeRefreshToken implements AuthStore.
func (a *Auth[Tx]) ExchangeRefreshToken(ctx context.Context, tokenHash []byte, access, refresh *Token) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := a.db.ExchangeRefreshToken(ctx, tokenHash, time.Now(), access, refresh); err != nil {
		return errors.Wrap(err, "db.ExchangeRefreshToken()")
	}

	return nil
}

// InsertDeviceCode implements AuthStore.
func (a *Auth[Tx]) InsertDeviceCode(ctx context.Context, d *DeviceCode) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := a.db.InsertDeviceCode(ctx, d); err != nil {
		return errors.Wrap(err, "db.InsertDeviceCode()")
	}

	return nil
}

// DeviceCode implements AuthStore.
func (a *Auth[Tx]) DeviceCode(ctx context.Context, userCode string) (*DeviceCode, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	d, err := a.db.DeviceCodeByUserCode(ctx, userCode, time.Now())
	if err != nil {
		return nil, errors.Wrap(err, "db.DeviceCodeByUserCode()")
	}

	return d, nil
}

// ApproveDeviceCode implements AuthStore.
func (a *Auth[Tx]) ApproveDeviceCode(ctx context.Context, id, userID impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := a.db.ApproveDeviceCode(ctx, id, userID, time.Now()); err != nil {
		return errors.Wrap(err, "db.ApproveDeviceCode()")
	}

	return nil
}

// DenyDeviceCode implements AuthStore.
func (a *Auth[Tx]) DenyDeviceCode(ctx context.Context, id impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := a.db.DenyDeviceCode(ctx, id, time.Now()); err != nil {
		return errors.Wrap(err, "db.DenyDeviceCode()")
	}

	return nil
}

// RedeemDeviceCode implements AuthStore.
func (a *Auth[Tx]) RedeemDeviceCode(ctx context.Context, deviceCodeHash []byte, interval time.Duration, access, refresh *Token) (code *DeviceCode, status DeviceCodeStatus, err error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	d, status, err := a.db.RedeemDeviceCode(ctx, deviceCodeHash, time.Now(), interval, access, refresh)
	if err != nil {
		return nil, "", errors.Wrap(err, "db.RedeemDeviceCode()")
	}

	return d, status, nil
}
