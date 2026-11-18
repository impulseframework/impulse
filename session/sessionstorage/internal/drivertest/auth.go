package drivertest

import (
	"context"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/securehash"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/internal/signinflow"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// AuthDriver is the driver surface the users suite exercises. Both drivers
// satisfy it.
type AuthDriver interface {
	Session(ctx context.Context, sessionID impulse.UUID) (*dbtype.SessionData, error)
	InsertSession(ctx context.Context, insertSession *dbtype.InsertSession, req *sessioninfo.NewSessionRequest) (impulse.UUID, error)
	InsertImpersonatedSession(ctx context.Context, insertSession *dbtype.InsertSession, req *sessioninfo.NewSessionRequest, imp *dbtype.InsertImpersonation) (impulse.UUID, error)
	CreateUser(ctx context.Context, user *dbtype.InsertSessionUser, customData any) (*dbtype.SessionUser, error)
	User(ctx context.Context, id impulse.UUID) (*dbtype.SessionUser, error)
	UserByUserName(ctx context.Context, username string) (*dbtype.SessionUser, error)
	Users(ctx context.Context) ([]*dbtype.SessionUser, error)
	ReplaceUserPassword(ctx context.Context, id impulse.UUID, hash *securehash.Hash, changeRequired bool) error
	UpgradeUserPasswordHash(ctx context.Context, id impulse.UUID, proven, hash *securehash.Hash) error
	ChangeUserPassword(ctx context.Context, id impulse.UUID, proven, hash *securehash.Hash) error
	SetFirstUserPassword(ctx context.Context, id impulse.UUID, hash *securehash.Hash) error
	DeactivateUser(ctx context.Context, id impulse.UUID) error
	DeleteUser(ctx context.Context, id impulse.UUID) error
	Identity(ctx context.Context, method sessioninfo.AuthMethod, connection, subject string) (*dbtype.SessionIdentity, error)
	IdentitiesByUser(ctx context.Context, userID impulse.UUID) ([]*dbtype.SessionIdentity, error)
	LinkIdentity(ctx context.Context, userID impulse.UUID, identity *sessioninfo.Identity, tenant string) (*dbtype.SessionIdentity, error)
	UnlinkIdentity(ctx context.Context, identityID impulse.UUID, signsIn func(method sessioninfo.AuthMethod, connection string) bool) error
	RemoveUserPassword(ctx context.Context, userID impulse.UUID, standing []impulse.UUID) error
	RemoveProvenUserPassword(ctx context.Context, userID impulse.UUID, proven *securehash.Hash, standing []impulse.UUID) error
	DestroyUserSessions(ctx context.Context, userID impulse.UUID) error
	TouchUserSignIn(ctx context.Context, userID impulse.UUID) error
	DestroySessionsByProviderSessionID(ctx context.Context, issuer, providerSessionID string) error
	ProviderSessionIssuers(ctx context.Context, providerSessionID string) ([]string, error)
	AppendAuthEvent(ctx context.Context, sessionID impulse.UUID, event *sessioninfo.AuthEvent) error
	InsertPendingSignIn(ctx context.Context, p *dbtype.PendingSignIn) error
	PendingSignInByToken(ctx context.Context, tokenHash []byte, now time.Time) (*dbtype.PendingSignIn, error)
	ConsumePendingSignIn(ctx context.Context, tokenHash []byte, now time.Time) error
	SignInAttempts(ctx context.Context, name string) (*dbtype.SignInAttempts, error)
	RecordSignInFailure(ctx context.Context, name string, now time.Time, limit dbtype.SignInAttemptLimit) (*dbtype.SignInAttempts, error)
	ClearSignInAttempts(ctx context.Context, name string) error
	// SetSignInMethodsTableName renames the sign-in methods table; the suite points it
	// at a table that is not there.
	SetSignInMethodsTableName(name string) error
	SignInMethods(ctx context.Context) ([]*dbtype.SignInMethod, error)
	SetSignInMethod(ctx context.Context, m *dbtype.SignInMethod) error
	// SetSignInAttemptsTableName renames the sign-in attempts table; the suite points
	// it at a table that is not there.
	SetSignInAttemptsTableName(name string) error
	// SetSessionTableName renames the sessions table; the suite points it at a table
	// that is not there, to fail the half of a transaction that ends sessions.
	SetSessionTableName(name string) error
	SetUsernameIsEmail(same bool)
	UserByEmail(ctx context.Context, email string) (*dbtype.SessionUser, error)
	SetUserEmail(ctx context.Context, id impulse.UUID, email *string) error
	SetUserUsername(ctx context.Context, id impulse.UUID, newUsername string) error
	VerifyUserEmail(ctx context.Context, id impulse.UUID, email string) error
	ClaimVerifiedEmail(ctx context.Context, id impulse.UUID, email string) (bool, error)
	SetUserName(ctx context.Context, id impulse.UUID, name *string) error
	ClaimUserName(ctx context.Context, id impulse.UUID, name string) (bool, error)
	SetUserLook(ctx context.Context, id impulse.UUID, look sessioninfo.Look) error
	InsertMailLink(ctx context.Context, l *dbtype.MailLink) error
	MailLinkByToken(ctx context.Context, tokenHash []byte, now time.Time) (*dbtype.MailLink, error)
	LiveMailLinks(ctx context.Context, kind dbtype.MailLinkKind, now time.Time) ([]*dbtype.MailLink, error)
	ConsumeMailLink(ctx context.Context, tokenHash []byte, now time.Time) error
	RevokeMailLink(ctx context.Context, id impulse.UUID, now time.Time) error
	SetTokensTableName(name string) error
	SetDeviceCodesTableName(name string) error
	InsertToken(ctx context.Context, t *dbtype.Token) error
	TokenByHash(ctx context.Context, tokenHash []byte, now time.Time) (*dbtype.Token, error)
	TokensByUser(ctx context.Context, userID impulse.UUID, now time.Time) ([]*dbtype.Token, error)
	TouchToken(ctx context.Context, id impulse.UUID, now time.Time) error
	RevokeUserToken(ctx context.Context, userID, id impulse.UUID, now time.Time, reason dbtype.TokenRevocation) error
	RevokeUserTokens(ctx context.Context, userID impulse.UUID, reason dbtype.TokenRevocation) error
	ExchangeRefreshToken(ctx context.Context, tokenHash []byte, now time.Time, access, refresh *dbtype.Token) error
	InsertDeviceCode(ctx context.Context, d *dbtype.DeviceCode) error
	DeviceCodeByUserCode(ctx context.Context, userCode string, now time.Time) (*dbtype.DeviceCode, error)
	ApproveDeviceCode(ctx context.Context, id, userID impulse.UUID, now time.Time) error
	DenyDeviceCode(ctx context.Context, id impulse.UUID, now time.Time) error
	RedeemDeviceCode(ctx context.Context, deviceCodeHash []byte, now time.Time, interval time.Duration, access, refresh *dbtype.Token) (*dbtype.DeviceCode, dbtype.DeviceCodeStatus, error)
	// TokenState and DeviceCodeState read a row whatever its state: the suite's look
	// at what the live reads no longer return.
	TokenState(ctx context.Context, tokenHash []byte) (*dbtype.Token, error)
	DeviceCodeState(ctx context.Context, deviceCodeHash []byte) (*dbtype.DeviceCode, error)
	SetFactorCodesTableName(name string) error
	SetTrustedDevicesTableName(name string) error
	InsertFactorCode(ctx context.Context, c *dbtype.FactorCode, now time.Time) error
	FactorCodeByPending(ctx context.Context, pendingID impulse.UUID, now time.Time) (*dbtype.FactorCode, error)
	RecordFactorCodeAttempt(ctx context.Context, id impulse.UUID, now time.Time) (int, error)
	ConsumeFactorCode(ctx context.Context, id impulse.UUID, now time.Time) error
	InsertTrustedDevice(ctx context.Context, d *dbtype.TrustedDevice) error
	TrustedDeviceByToken(ctx context.Context, tokenHash []byte, now time.Time) (*dbtype.TrustedDevice, error)
	TouchTrustedDevice(ctx context.Context, id impulse.UUID, now time.Time) error
	RevokeTrustedDevice(ctx context.Context, tokenHash []byte, now time.Time) error
	RevokeTrustedDevices(ctx context.Context, userID impulse.UUID) error
	// FactorCodeState and TrustedDeviceState read a row whatever its state.
	FactorCodeState(ctx context.Context, id impulse.UUID) (*dbtype.FactorCode, error)
	TrustedDeviceState(ctx context.Context, tokenHash []byte) (*dbtype.TrustedDevice, error)
}

// AuthSchema names the migration set an users database is prepared with.
type AuthSchema int

const (
	// WithoutAuth is the shipped sessions schema (Sessions, SessionUsers) and the
	// impersonation migration, without the auth migrations: what a Preauth store runs
	// on. It lacks Sessions.UserId and AuthenticatedAt and the identities and auth
	// events tables.
	WithoutAuth AuthSchema = iota
	// Auth is the shipped sessions schema, the impersonation migration and the
	// auth migrations.
	Auth
	// AuthWithAppTables is Auth plus the driver package's test-only application
	// tables: HookRecords, which hooks use through HookTx, and SessionCustomData for
	// CustomStringData.
	AuthWithAppTables
	// AuthUsernameIsEmail is AuthWithAppTables with the users table in the shape of an
	// application whose usernames are email addresses: the address columns are
	// generated from the username by the driver package's test data, as the README
	// gives the DDL. The driver is configured with SetUsernameIsEmail.
	AuthUsernameIsEmail
)

// HookTx is an application hook's view of its transaction, adapted by each harness to
// its backend. It stands in for the application rows a user resolver, sign-in
// policy or custom session data resolver writes and reads. It needs the
// AuthWithAppTables schema.
type HookTx interface {
	// Record writes key=value to the HookRecords table.
	Record(ctx context.Context, key, value string) error
	// Recorded reads key from HookRecords; found is false when it is absent.
	Recorded(ctx context.Context, key string) (value string, found bool, err error)
	// UserExists reports whether the SessionUsers row userID exists.
	UserExists(ctx context.Context, userID impulse.UUID) (bool, error)
}

// AuthConfig selects how a driver over a prepared users database is configured.
type AuthConfig struct {
	// Auth enables the Sessions.UserId and AuthenticatedAt columns.
	Auth bool
	// AuthEvents attaches the SessionAuthEvents table.
	AuthEvents bool
	// Impersonation attaches the SessionImpersonations table.
	Impersonation bool
	// Accounts is what the library's standard rule finds chosen for the instance's
	// sign-in method (MethodName), applied when Resolve is nil or answers
	// signin.Standard; NoStandardRule for none.
	Accounts Accounts
	// SeededUsers is the registration's declaration that its provider's names are the
	// application's own (signin.Registration.SeededUsers).
	SeededUsers bool
	// Admits is whom the instance's sign-in method admits now, as its registration
	// fixes it; the zero value is MethodAudience. A scenario of a registration that
	// code changed after an administrator opened it names another audience here and
	// writes the row itself, opened for the audience of before.
	Admits Admits
	// Resolve is the custom account rule the instance's SignIn runs with, which the
	// harness adapts to the driver's transaction. Nil with NoStandardRule rejects every
	// unlinked identity.
	Resolve func(ctx context.Context, tx HookTx, req *sessioninfo.NewSessionRequest) (*signin.Resolution, error)
	// DefaultRole is the default role an Open method's row names, "" for none.
	DefaultRole string
	// SignUp is the hook that gives a person who signed up through an Open method its
	// default role, inside the transaction that makes the account, which the harness
	// adapts to the driver's transaction; nil for none. See signinflow.SignUpSeater.
	SignUp func(ctx context.Context, tx HookTx, userID impulse.UUID, username, method, role string) (committed func(ctx context.Context), err error)
	// Policy is the sign-in policy the instance's SignIn runs with; nil allows.
	Policy func(ctx context.Context, tx HookTx, req *sessioninfo.NewSessionRequest) (*signin.Decision, error)
	// CustomData, when set, attaches the AuthWithAppTables schema's
	// SessionCustomData table (CustomStringData) with CustomData as its resolver.
	CustomData func(ctx context.Context, tx HookTx, req *sessioninfo.NewSessionRequest) (any, error)
	// Accept is the invitation acceptor the instance's SignIn and Accept run with,
	// inside the transaction that consumes the invitation, which the harness adapts to
	// the driver's transaction; nil for none. See signinflow.InvitationAcceptor.
	Accept func(ctx context.Context, tx HookTx, userID impulse.UUID, username string, inv *sessioninfo.Invitation) (committed func(ctx context.Context), err error)
	// Admission is what a tenanted application gives a person's first sign-in through
	// a tenant's directory; nil for none.
	Admission *DirectoryAdmission
}

// MethodName is the name an instance's sign-in method is registered under: the key of
// its row in the sign-in methods table.
const MethodName = "directory"

// Accounts is what an administrator chose for an instance's sign-in method, which the
// library's standard rule reads.
type Accounts uint8

const (
	// NoStandardRule runs no standard rule: with no Resolve, every unlinked identity
	// is rejected.
	NoStandardRule Accounts = iota
	// InviteOnly is a method with no row: the standard rule admits a person with no
	// account by invitation only.
	InviteOnly
	// Open is a method whose row says it is open to sign-up, which the harness writes
	// with SeedSignInMethod before the instance is used.
	Open
)

// MethodAudience is whom the instance's sign-in method (MethodName) admits, as its
// registration would fix it: the people of one organization.
func MethodAudience() signin.Audience {
	return signin.Audience{Organizations: []string{"lakeside.edu"}}
}

// Admits is whom an instance's sign-in method admits now: one of the audiences the
// scenarios turn on, named by a small value so that a configuration stays cheap to
// hand around. The zero value is the audience of every scenario that does not turn on
// it (MethodAudience).
type Admits uint8

const (
	// AdmitsOneOrganization is MethodAudience: the people of lakeside.edu.
	AdmitsOneOrganization Admits = iota
	// AdmitsTwoOrganizations is the people of lakeside.edu and of acme.example.
	AdmitsTwoOrganizations
	// AdmitsAnyAccount is anyone with an account of the provider.
	AdmitsAnyAccount
	// AdmitsPersonalAccounts is the people of lakeside.edu, and personal accounts
	// beside them.
	AdmitsPersonalAccounts
)

// Audience is the audience the value names.
func (a Admits) Audience() signin.Audience {
	switch a {
	case AdmitsTwoOrganizations:
		return signin.Audience{Organizations: []string{"lakeside.edu", "acme.example"}}
	case AdmitsAnyAccount:
		return signin.Audience{Accounts: "a Google account"}
	case AdmitsPersonalAccounts:
		return signin.Audience{Organizations: []string{"lakeside.edu"}, Accounts: "a Microsoft personal account"}
	default:
		return MethodAudience()
	}
}

// audience is whom the instance's method admits now, as the rule compares it with the
// audience the method's row was opened for.
func (cfg *AuthConfig) audience() string {
	return cfg.Admits.Audience().Key()
}

// openedFor is the audience a row opened for audience keeps.
func openedFor(audience signin.Audience) *string {
	key := audience.Key()

	return &key
}

// SeedSignInMethod writes the row an Open method has, with cfg's default role, through
// the driver; a harness calls it once its driver is built. The row is opened for the
// audience the method has. It writes nothing for any other choice, so an invite-only
// instance runs with no row.
func SeedSignInMethod(ctx context.Context, t *testing.T, d AuthDriver, cfg AuthConfig) {
	t.Helper()

	if cfg.Accounts != Open {
		return
	}
	audience := cfg.audience()
	m := &dbtype.SignInMethod{Name: MethodName, OpenSignUp: true, OpenedFor: &audience}
	if cfg.DefaultRole != "" {
		m.DefaultRole = &cfg.DefaultRole
	}
	if err := d.SetSignInMethod(ctx, m); err != nil {
		t.Fatalf("SetSignInMethod() error = %v", err)
	}
}

// DirectoryAdmission is what an instance's SignIn and LinkConfirmed run a declared
// directory's first sign-in with: Directories reads the declaration of the directory,
// which the harness adapts to the driver's transaction; Seat seats the person with the
// default seat the declaration names, inside the transaction that links them, which
// the harness adapts to the driver's transaction, nil for none.
type DirectoryAdmission struct {
	Directories func(ctx context.Context, tx HookTx, directory signin.Directory) ([]signin.DeclaredDirectory, error)
	Seat        func(ctx context.Context, tx HookTx, userID impulse.UUID, username string, seats []signin.DirectorySeat) (committed func(ctx context.Context), err error)
	// OnlyIn reports whether tenant is the only place the user holds anything, nil for
	// an application that reads none: the rule on a first link made on a name alone
	// asks it.
	OnlyIn func(ctx context.Context, tx HookTx, userID impulse.UUID, tenant accesstypes.Domain) (bool, error)
}

// AuthInstance is a driver over a freshly prepared users database, with the
// harness's own handle on that database.
type AuthInstance struct {
	Driver AuthDriver
	Raw    any
	// SignIn establishes the session of a request that carries a verified identity:
	// the sign-in flow over the driver with the configured resolver and policy, as the
	// Auth session runs it. The harness builds it with NewSignIn. proven, at most one, is
	// the proof of the password the sign-in rests on; none is the sign-in no password
	// proved.
	SignIn func(ctx context.Context, insert *dbtype.InsertSession, req *sessioninfo.NewSessionRequest, proven ...dbtype.PasswordProof) (impulse.UUID, error)
	// Accept accepts inv for userID by hand, as the Auth does for a person already
	// signed in, with the configured acceptor. The harness builds it with NewAccept.
	Accept func(ctx context.Context, userID impulse.UUID, username, email string, inv *sessioninfo.Invitation) error
	// LinkConfirmed links identity to userID as the Auth does once the user's password
	// has confirmed the link, with the configured admission. The harness builds it with
	// NewLinkConfirmed. proven, at most one, is the proof of the password that confirmed
	// the link.
	LinkConfirmed func(ctx context.Context, userID impulse.UUID, username string, identity *sessioninfo.Identity, tenant string, proven ...dbtype.PasswordProof) error
	// SignUp makes the account of a person who signs up with a password through the
	// instance's method, consuming the mail link linkID, with the configured sign-up
	// hook. The harness builds it with NewSignUp.
	SignUp func(ctx context.Context, user *dbtype.InsertSessionUser, linkID impulse.UUID) (impulse.UUID, error)
}

// NewSignUp returns an instance's SignUp over d: the flow's password sign-up through
// MethodName with cfg's sign-up hook, given the driver's transaction through adapt.
func NewSignUp[Tx any](
	d signinflow.Driver[Tx], cfg AuthConfig, adapt func(tx Tx) HookTx,
) func(ctx context.Context, user *dbtype.InsertSessionUser, linkID impulse.UUID) (impulse.UUID, error) {
	seat := adaptSignUp(cfg, adapt)

	return func(ctx context.Context, user *dbtype.InsertSessionUser, linkID impulse.UUID) (impulse.UUID, error) {
		id, err := signinflow.SignUp(ctx, d, MethodName, cfg.audience(), user, linkID, seat)
		if err != nil {
			return impulse.NilUUID, errors.Wrap(err, "signinflow.SignUp()")
		}

		return id, nil
	}
}

// adaptSignUp is cfg's sign-up hook over the driver's transaction, nil for none.
func adaptSignUp[Tx any](cfg AuthConfig, adapt func(tx Tx) HookTx) signinflow.SignUpSeater[Tx] {
	if cfg.SignUp == nil {
		return nil
	}

	return func(ctx context.Context, tx Tx, userID impulse.UUID, username, method, role string) (func(ctx context.Context), error) {
		return cfg.SignUp(ctx, adapt(tx), userID, username, method, role)
	}
}

// oneProof is the proof a harness call was given: nil for none.
func oneProof(proven []dbtype.PasswordProof) dbtype.PasswordProof {
	if len(proven) == 0 {
		return nil
	}

	return proven[0]
}

// NewLinkConfirmed returns an instance's LinkConfirmed over d: the flow's confirmed
// link with cfg's admission, each hook given the driver's transaction through adapt.
func NewLinkConfirmed[Tx any](
	d signinflow.Driver[Tx], cfg AuthConfig, adapt func(tx Tx) HookTx,
) func(ctx context.Context, userID impulse.UUID, username string, identity *sessioninfo.Identity, tenant string, proven ...dbtype.PasswordProof) error {
	directories, seat, _ := adaptAdmission(cfg, adapt)

	return func(ctx context.Context, userID impulse.UUID, username string, identity *sessioninfo.Identity, tenant string, proven ...dbtype.PasswordProof) error {
		if err := signinflow.LinkConfirmed(ctx, d, userID, username, identity, tenant, directories, seat, oneProof(proven)); err != nil {
			return errors.Wrap(err, "signinflow.LinkConfirmed()")
		}

		return nil
	}
}

// adaptAdmission is cfg's directory admission over the driver's transaction: the
// reader, the seater and the reader of whether a tenant is all a person holds, each nil
// for none.
func adaptAdmission[Tx any](
	cfg AuthConfig, adapt func(tx Tx) HookTx,
) (signin.DirectoryReader[Tx], signinflow.DirectorySeater[Tx], signinflow.OnlyInTenant[Tx]) {
	if cfg.Admission == nil {
		return nil, nil, nil
	}
	lookup := cfg.Admission.Directories
	directories := func(ctx context.Context, tx Tx, directory signin.Directory) ([]signin.DeclaredDirectory, error) {
		return lookup(ctx, adapt(tx), directory)
	}
	var seat signinflow.DirectorySeater[Tx]
	if seater := cfg.Admission.Seat; seater != nil {
		seat = func(ctx context.Context, tx Tx, userID impulse.UUID, username string, seats []signin.DirectorySeat) (func(ctx context.Context), error) {
			return seater(ctx, adapt(tx), userID, username, seats)
		}
	}
	var onlyIn signinflow.OnlyInTenant[Tx]
	if read := cfg.Admission.OnlyIn; read != nil {
		onlyIn = func(ctx context.Context, tx Tx, userID impulse.UUID, tenant accesstypes.Domain) (bool, error) {
			return read(ctx, adapt(tx), userID, tenant)
		}
	}

	return directories, seat, onlyIn
}

// NewAccept returns an instance's Accept over d: the flow's acceptance with cfg's
// acceptor, given the driver's transaction through adapt.
func NewAccept[Tx any](
	d signinflow.Driver[Tx], cfg AuthConfig, adapt func(tx Tx) HookTx,
) func(ctx context.Context, userID impulse.UUID, username, email string, inv *sessioninfo.Invitation) error {
	accept := adaptAccept(cfg, adapt)

	return func(ctx context.Context, userID impulse.UUID, username, email string, inv *sessioninfo.Invitation) error {
		if err := signinflow.AcceptInvitation(ctx, d, userID, username, email, inv, accept); err != nil {
			return errors.Wrap(err, "signinflow.AcceptInvitation()")
		}

		return nil
	}
}

// adaptAccept is cfg's acceptor over the driver's transaction, nil for none.
func adaptAccept[Tx any](cfg AuthConfig, adapt func(tx Tx) HookTx) signinflow.InvitationAcceptor[Tx] {
	if cfg.Accept == nil {
		return nil
	}

	return func(ctx context.Context, tx Tx, userID impulse.UUID, username string, inv *sessioninfo.Invitation) (func(ctx context.Context), error) {
		return cfg.Accept(ctx, adapt(tx), userID, username, inv)
	}
}

// NewSignIn returns an instance's SignIn over d: the sign-in flow with cfg's account
// rules and policy, each given the driver's transaction through adapt.
func NewSignIn[Tx any](
	d signinflow.Driver[Tx], cfg AuthConfig, adapt func(tx Tx) HookTx,
) func(ctx context.Context, insert *dbtype.InsertSession, req *sessioninfo.NewSessionRequest, proven ...dbtype.PasswordProof) (impulse.UUID, error) {
	var rule signin.AccountRule[Tx]
	switch {
	case cfg.Resolve != nil:
		rule = func(ctx context.Context, tx Tx, req *sessioninfo.NewSessionRequest) (*signin.Resolution, error) {
			return cfg.Resolve(ctx, adapt(tx), req)
		}
	case cfg.Accounts == NoStandardRule:
		// No rule at all rejects every unlinked identity, as a nil resolution does.
		rule = func(context.Context, Tx, *sessioninfo.NewSessionRequest) (*signin.Resolution, error) { return nil, nil }
	}
	var policy signin.Policy[Tx]
	if cfg.Policy != nil {
		policy = func(ctx context.Context, tx Tx, req *sessioninfo.NewSessionRequest) (*signin.Decision, error) {
			return cfg.Policy(ctx, adapt(tx), req)
		}
	}
	directories, seat, onlyIn := adaptAdmission(cfg, adapt)
	accept := adaptAccept(cfg, adapt)
	signUp := adaptSignUp(cfg, adapt)
	return func(ctx context.Context, insert *dbtype.InsertSession, req *sessioninfo.NewSessionRequest, proven ...dbtype.PasswordProof) (impulse.UUID, error) {
		id, err := impulse.NewUUID()
		if err != nil {
			return impulse.NilUUID, errors.Wrap(err, "impulse.NewUUID()")
		}
		rules := signinflow.Rules[Tx]{
			Account: rule, Method: MethodName, Audience: cfg.audience(), SeededUsers: cfg.SeededUsers, Policy: policy, Accept: accept, SignUp: signUp,
			Directories: directories, Seat: seat, OnlyIn: onlyIn, Proven: oneProof(proven),
		}
		if err := signinflow.SignIn(ctx, d, id, insert, req, rules); err != nil {
			return impulse.NilUUID, errors.Wrap(err, "signinflow.SignIn()")
		}

		return id, nil
	}
}

// AuthHarness adapts one driver package to the users suite.
type AuthHarness struct {
	// New prepares a fresh database with schema and returns a driver configured per cfg.
	New func(ctx context.Context, t *testing.T, schema AuthSchema, cfg AuthConfig) *AuthInstance
	// CountAuthEvents counts the session's rows in SessionAuthEvents directly.
	CountAuthEvents func(ctx context.Context, t *testing.T, raw any, sessionID impulse.UUID) int
	// DeleteSession deletes the session row directly, bypassing the driver.
	DeleteSession func(ctx context.Context, t *testing.T, raw any, sessionID impulse.UUID)
	// Recorded reads key from the HookRecords table directly, as committed.
	Recorded func(ctx context.Context, t *testing.T, raw any, key string) (value string, found bool)
	// Record writes key=value to the HookRecords table directly, bypassing the driver:
	// a row the application's hooks then read in a sign-in's transaction.
	Record func(ctx context.Context, t *testing.T, raw any, key, value string)
	// PendingConsumedAt reads the ConsumedAt of the pending sign-in holding tokenHash
	// directly: nil while it is live.
	PendingConsumedAt func(ctx context.Context, t *testing.T, raw any, tokenHash []byte) *time.Time
	// MailLinkConsumedAt reads the ConsumedAt of the mail link holding tokenHash
	// directly: nil while it is live.
	MailLinkConsumedAt func(ctx context.Context, t *testing.T, raw any, tokenHash []byte) *time.Time
	// ClockLead is how far the backend's clock may run ahead of the host's; see
	// Harness.ClockLead.
	ClockLead time.Duration
}

// RunAuth runs the users conformance suite against h. Every case prepares its
// own database and runs in parallel.
func RunAuth(t *testing.T, h *AuthHarness) {
	t.Helper()

	tests := []struct {
		name string
		run  func(ctx context.Context, t *testing.T, h *AuthHarness)
	}{
		{name: "a database without the auth set keeps working: the user columns and events are never named", run: testWithoutAuthSet},
		{name: "user sessions record and read UserId and AuthenticatedAt", run: testSessionUserColumns},
		{name: "the first auth event is the sign-in method, read with the session", run: testInitialAuthEvent},
		{name: "a session insert records every auth event it carries, in order, in one write", run: testInitialAuthEvents},
		{name: "an impersonated session records its user and an impersonation event naming the actor", run: testImpersonatedSessionUser},
		{name: "a session's auth events go with it", run: testAuthEventsCascade},
		{name: "a linked identity signs in to its user without the resolver", run: testLinkedIdentitySignsIn},
		{name: "the resolver's outcome decides an unlinked identity", run: testResolverOutcomes},
		{name: "the standard account rule decides an unlinked identity by its sign-in name and by what was chosen for its method: no row is invite only, which refuses a stranger and writes nothing", run: testStandardAccountRules},
		{name: "a sign-in method's row is written whole at the database's time, replaced by the next write, and listed by name", run: testSignInMethodRows},
		{name: "a driver whose sign-in methods table is not there says so: an error naming the table, never a method read as invite only", run: testSignInMethodsMissingTable},
		{name: "an open method gives a person with no account its default role in the transaction that makes the account, once; a person an administrator created is linked and given nothing", run: testOpenMethodDefaultRole},
		{name: "an open method that names no role makes the account and gives nothing", run: testOpenMethodWithoutRole},
		{name: "a row opens its method for the audience it was opened for alone: a registration widened or narrowed in code since refuses a stranger and writes nothing, at a sign-in and at a password sign-up, and a row that names no audience opens nothing", run: testOpenRowOfAnotherAudience},
		{name: "a person with an account, and a person with an invitation, sign in the same under either choice: the invitation's role, never the default role beside it", run: testEitherChoiceSameForAccountsAndInvitations},
		{name: "a dead invitation carried through an open method gives nothing, and the person gets the method's default role", run: testOpenMethodDeadInvitation},
		{name: "a default role on an Auth with no sign-up hook fails the sign-in in its transaction: no user is created", run: testOpenMethodHookMissing},
		{name: "a failing sign-up hook fails the transaction that makes the account: no account, no link, none of the hook's writes; the next sign-in makes it with its role", run: testOpenMethodHookFails},
		{name: "a password sign-up through a method that is not open is refused: no account, the link left standing", run: testPasswordSignUpClosed},
		{name: "a password sign-up through an open method spends the link, makes the account and gives the default role in one transaction; a spent link and a held address make nothing", run: testPasswordSignUpOpen},
		{name: "a failing sign-up hook fails a password sign-up whole: no account, the link left for the next attempt", run: testPasswordSignUpHookFails},
		{name: "the standard account rule matches a user by the identity's verified address when none is under the sign-in name, and no rule provisions a second user for a held address", run: testResolveByVerifiedAddress},
		{name: "a custom account rule runs first and may leave the identity to the standard rule", run: testCustomRuleBeforeStandardRule},
		{name: "the standard rule runs once: a linked identity signs in by its link, and the username is not renamed", run: testStandardRuleRunsOnce},
		{name: "an untrusted link from another connection to a password-less user is refused; the same connection or a trusted link links", run: testLinkAcrossConnections},
		{name: "concurrent untrusted links from two connections to a password-less user link exactly one", run: testConcurrentCrossConnectionLinks},
		{name: "the sign-in policy decides after the user is known", run: testSignInPolicy},
		{name: "a disabled user is refused", run: testDisabledUserRefused},
		{name: "a rejected sign-in keeps the user resolver's own writes", run: testRejectionKeepsResolverWrites},
		{name: "a denied or held sign-in keeps the sign-in policy's own writes", run: testHeldOrRefusedKeepsHookWrites},
		{name: "the policy of a provisioning sign-in reads the new user and the rows OnProvisioned wrote, and knows it was provisioned", run: testPolicySeesProvisionedUser},
		{name: "the custom session data resolver of a provisioning sign-in reads the new user and knows it was provisioned", run: testCustomDataSeesProvisionedUser},
		{name: "a sign-in reports the user it resolved to and how", run: testSignInUserReported},
		{name: "the tenant policy decides a sign-in by the person's memberships and the declarations their tenants make, read in the sign-in's transaction", run: testTenantPolicy},
		{name: "a directory-only tenant's own administrator signs in with a password once the mailed code is given, and nobody else does", run: testTenantAdministratorsWayIn},
		{name: "a tenant's sign-in settings restrict its own door alone: a session reaches every tenant that seats the person but a directory-only tenant whose directory they did not sign in through", run: testTenantDoor},
		{name: "a first link made on a name alone, to an account that has never signed in, is made only through the directory of the one tenant the account belongs to or with the account's own invitation", run: testFirstLinkByName},
		{name: "an identity the application has not seen is linked on a name alone only as an account's first link, never as a second identity: a colleague is not linked into an account another person made under their name", run: testSecondIdentityByName},
		{name: "a provisioned username that is taken is refused as a conflict, and nothing is linked", run: testProvisionUsernameTaken},
		{name: "concurrent first sign-ins of one identity link it once", run: testConcurrentFirstSignIn},
		{name: "concurrent first sign-ins of one identity through a provisioning resolver all succeed and provision it once", run: testConcurrentFirstProvision},
		{name: "links are managed outside sign-in, and the last means of sign-in stays", run: testLinkManagement},
		{name: "an identity is unlinked from a password-less user only while another that signs them in remains", run: testUnlinkKeepsAMeansOfSignIn},
		{name: "two unlinks of a password-less user's two identities, asked at once, leave the user one", run: testUnlinksRace},
		{name: "a password is removed from a user that has an identity, with its change mark, and stays as the last means of sign-in", run: testPasswordRemoval},
		{name: "a password's removal and the unlinking of the user's one identity, asked at once, leave the user one of the two", run: testPasswordRemovalRacesUnlink},
		{name: "deleting a user deletes its identity links with it", run: testDeleteUserDeletesLinks},
		{name: "DestroyUserSessions expires the user's sessions by UserId", run: testDestroyUserSessions},
		{name: "AppendAuthEvent records a step-up after the sign-in", run: testAppendAuthEvent},
		{name: "a pending sign-in is read back by its token hash with everything it carried", run: testPendingSignInRoundTrip},
		{name: "a pending sign-in is consumed once, by a conditional update", run: testPendingSignInConsumedOnce},
		{name: "an expired pending sign-in is neither read nor consumed", run: testPendingSignInExpired},
		{name: "concurrent completions of one pending sign-in consume it once", run: testPendingSignInConcurrentConsume},
		{name: "deleting a user deletes its sessions, their auth events and its identity links by the schema's cascade", run: testDeleteUserCascades},
		{name: "a session row names an existing user or no user: the schema refuses an unknown user id", run: testSessionUserForeignKey},
		{name: "a sign-in records the provider's session id under its issuer, and a provider-initiated logout ends the sessions under that issuer and id and no other", run: testProviderSessionLogout},
		{name: "a sign-in and everything written with it take the database's time, once", run: testDatabaseTimeOnSignIn},
		{name: "a user is stored with its password change mark and the database's time, and listed by username", run: testUserAccounts},
		{name: "a password is replaced with its change mark and the user's sessions ended in one transaction: both, or neither", run: testPasswordReplaced},
		{name: "a hash upgrade, a user's own change and a user's own removal write on proof of the hash the user holds, and nothing without one", run: testProvenPasswordWrites},
		{name: "a hash upgrade, a user's own change and a user's own removal write nothing once another write of the password has committed since the user was read", run: testProvenPasswordWriteAfterAnotherWrite},
		{name: "a temporary password an operator sets stands, with its change mark, whatever write on proof of the password it replaces is asked at once", run: testProvenPasswordWriteRacesReset},
		{name: "a user's own change writes the password and ends the user's sessions in one transaction: both, or neither", run: testOwnChangeEndsSessions},
		{name: "a first password is written for a user who holds none, once, and ends no session; a user who holds one keeps it", run: testFirstPassword},
		{name: "a sign-in on proof of a password inserts its session only while the user holds that password: refused after a change, a temporary password or a removal, before the policy is asked", run: testProvenSignIn},
		{name: "a pending sign-in a password started keeps the proof, is inserted only while the user holds that password, and completes only while they do; one no password proved is untouched", run: testProvenPendingSignIn},
		{name: "a link confirmed with a password is made only while the user holds that password", run: testProvenLinkConfirmed},
		{name: "a sign-in on proof of a password and the write that replaces it, asked at once, leave no live session resting on the replaced password", run: testProvenSignInRacesPasswordWrite},
		{name: "a user's last sign-in is the database's time at the touch after a sign-in, nil before the first", run: testUserLastSignIn},
		{name: "a revocation stamps every session it expires and every record it ends with one time, the database's", run: testDatabaseTimeOnRevocation},
		{name: "a failed sign-in attempt is recorded under the name, known or not, counted in its window and locked at the limit, at the database's time", run: testSignInAttemptsRecorded},
		{name: "a failed sign-in attempt after the window ended or the lock passed opens a new window", run: testSignInAttemptsWindowAndLockPass},
		{name: "a clear deletes the name's sign-in attempts and the count starts over", run: testSignInAttemptsCleared},
		{name: "concurrent failed sign-in attempts on one name each count", run: testSignInAttemptsConcurrent},
		{name: "of failed attempts recorded at once on one name, as many as the limit are told they were counted and no more", run: testSignInAttemptsCountedAtOnce},
		{name: "a failed attempt recorded while the name is cleared is the first after the clear, never an error", run: testSignInAttemptsRecordedBesideClears},
		{name: "a driver whose sign-in attempts table is not there says so: an error naming the table, never a count of nothing", run: testSignInAttemptsMissingTable},
		{name: "a user's email address is stored in two states, unique among every address held, and read back with the user", run: testUserEmail},
		{name: "a verified address replaces what the user held, at the database's time; an address another user holds is refused", run: testUserEmailVerified},
		{name: "a sign-in refused or held before the acceptance leaves the invitation standing, nothing consumed and nothing granted", run: testInvitationKeptOnRefusal},
		{name: "a method's verified address is claimed for a user without one, once, and never over another user's", run: testClaimVerifiedEmail},
		{name: "a person's name is stored without the space around it, replaced or taken off by hand, and claimed only for a user who holds none", run: testUserName},
		{name: "the look a person chose is kept as a whole, a half it does not name taken off, and a scheme that is neither light nor dark refused", run: testUserLook},
		{name: "where the username is the address, the database derives the address, a rename is refused and a verified address renames the user", run: testUsernameIsEmail},
		{name: "a mail link is read back by its token hash with everything it carried", run: testMailLinkRoundTrip},
		{name: "a mail link is consumed once, by token or by id, and listed while it stands", run: testMailLinkConsumedOnce},
		{name: "an expired mail link is neither read, consumed nor listed", run: testMailLinkExpired},
		{name: "a verification link goes with its user", run: testMailLinkCascade},
		{name: "a sign-in that carries an invitation accepts it in the resolution's transaction: consumed, the address set, the hook's writes committed with the user", run: testInvitationSignIn},
		{name: "an invitation that died is skipped through an open method and refuses through an invite-only one, provisioning nothing", run: testInvitationDead},
		{name: "a stranger from a directory a tenant declares with a default seat is provisioned and seated with it before the policy decides; a tenant declaring it without a seat gives nothing; a second sign-in seats nothing more", run: testDirectoryDefaultSeat},
		{name: "a stranger from a directory a tenant declares with no default seat is refused with invitation_only and nothing is written, whatever the registration's rule; a user created ahead in the tenant links; an invitation is the way past, and a dead one refuses with invitation_expired", run: testDirectoryInvitationOnly},
		{name: "a directory no tenant declares falls to the registration's rule", run: testDirectoryUndeclared},
		{name: "a person whose account stands and who follows another tenant's invitation is linked at their first sign-in through their own organization's directory, the invitation accepted and the directory's default seat given in one transaction, and is not linked without the invitation; a second sign-in seats nothing; a personal account reads no declaration", run: testDirectorySeatAtFirstLink},
		{name: "a link a password confirms is a first sign-in through the directory and gives the default seat with the link; a later sign-in gives nothing back, so a removed seat stays removed; a link deleted and made again gives the seat again", run: testDirectorySeatAtConfirmedLink},
		{name: "a failing seat hook fails the transaction that links the user: no link, none of the hook's writes, at once and at a confirmed link alike", run: testDirectorySeatHookFailsAtLink},
		{name: "a default seat on an Auth with no seat hook fails the sign-in in its transaction: no user is created", run: testDirectorySeatHookMissing},
		{name: "a failing seat hook fails the transaction that provisions the user: no account, no link, none of the hook's writes; the next sign-in is provisioned and seated", run: testDirectorySeatHookFails},
		{name: "a failing invitation hook fails the transaction that consumes the invitation: the invitation stands, a stranger is not provisioned, the hook's writes are not committed, and the next attempt accepts it", run: testInvitationHookFails},
		{name: "an invitation hook's committed function runs exactly once, after the commit and before the policy decides", run: testInvitationHookCommitted},
		{name: "a password sign-in accepts the invitation it carries in a transaction of its own, and so does an acceptance by hand, each only for the address the account holds", run: testInvitationAcceptedByHand},
		{name: "an invitation that makes a tenant is read back with the tenant it carries, by its token hash and in the listing of the live ones", run: testTenantInvitationRoundTrip},
		{name: "an invitation that makes a tenant hands its acceptor the tenant and how the person signed in, through a directory, by password and by hand, in the transaction that consumes it", run: testTenantInvitationAccepted},
		{name: "an invitation whose tenant is directory only is refused a password, a personal account and a session that came through neither, and nothing is written: no account, no link, and the invitation stands", run: testTenantInvitationDirectoryOnly},
		{name: "an acceptor that refuses to make the tenant ends the sign-in on its refusal: no account, no link, none of its writes, and the invitation stands for the next attempt", run: testTenantInvitationRefusedByAcceptor},
		{name: "a token is read back by its hash with everything it carried, and a user's live tokens are listed newest first", run: testTokenRoundTrip},
		{name: "a token is touched at the database's time, revoked once by its user alone, its family with it, and every token of a user at once", run: testTokenTouchAndRevoke},
		{name: "an expired token is neither read, listed, touched nor revoked", run: testTokenExpired},
		{name: "a refresh exchange rotates the token in its family, and a rotated token presented again revokes the family", run: testRefreshExchange},
		{name: "a device code is pending until approved or denied, holds the client to its interval, and is redeemed once for the approver's tokens", run: testDeviceCodeLifecycle},
		{name: "a user's tokens and device codes go with the user", run: testTokensCascade},
		{name: "a factor code is read back by its pending sign-in while it is live, the newest standing alone, and a new code consumes the earlier one", run: testFactorCodeRoundTrip},
		{name: "a factor code counts the guesses made at it and is consumed once; a dead code is neither read, counted nor consumed", run: testFactorCodeAttemptsAndConsume},
		{name: "attempts counted against a factor code at once are each counted and answered their own count", run: testFactorCodeAttemptsAtOnce},
		{name: "of factor codes inserted at once for one pending sign-in, one stands and every other is consumed", run: testFactorCodesInsertedAtOnce},
		{name: "a trusted device is read back by its token hash while it is live, touched at the database's time, revoked once by its token and all at once by its user", run: testTrustedDeviceLifecycle},
		{name: "a factor code goes with its pending sign-in and a trusted device with its user", run: testFactorsCascade},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tt.run(t.Context(), t, h)
		})
	}
}

// newInsertSession is a fresh session row for username. Its times are the driver's.
func newInsertSession(username string) *dbtype.InsertSession {
	return &dbtype.InsertSession{Username: username}
}

// createUser creates a user, with a password hash when withPassword.
func createUser(ctx context.Context, t *testing.T, d AuthDriver, username string) *dbtype.SessionUser {
	t.Helper()

	user, err := d.CreateUser(ctx, &dbtype.InsertSessionUser{Username: username}, nil)
	if err != nil {
		t.Fatalf("CreateUser(%q) error = %v", username, err)
	}

	return user
}

func mustSession(ctx context.Context, t *testing.T, d AuthDriver, id impulse.UUID) *dbtype.SessionData {
	t.Helper()

	sess, err := d.Session(ctx, id)
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}

	return sess
}

// eventsOpts compares auth events, nil and empty slices alike. Times are compared
// exactly: an event's time is the database's, read with the session, or a caller's,
// stored as given.
var eventsOpts = cmp.Options{cmpopts.EquateEmpty()}

func testWithoutAuthSet(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, WithoutAuth, AuthConfig{Impersonation: true})
	user := createUser(ctx, t, in.Driver, "user@example.com")

	req := &sessioninfo.NewSessionRequest{
		Reason: sessioninfo.ReasonLogin, Username: user.Username, UserID: user.ID,
		Identity: &sessioninfo.Identity{Method: sessioninfo.MethodPassword, Subject: user.ID.String()},
	}
	id, err := in.Driver.InsertSession(ctx, newInsertSession(user.Username), req)
	if err != nil {
		t.Fatalf("InsertSession() without the auth set error = %v", err)
	}
	impID, err := in.Driver.InsertImpersonatedSession(ctx, newInsertSession(user.Username),
		&sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonImpersonation, Username: user.Username, UserID: user.ID},
		&dbtype.InsertImpersonation{ActorUsername: "actor", PrincipalKind: dbtype.PrincipalKindUser, PrincipalUserID: strPtr(user.Username), ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatalf("InsertImpersonatedSession() without the auth set error = %v", err)
	}

	for _, sid := range []impulse.UUID{id, impID} {
		sess := mustSession(ctx, t, in.Driver, sid)
		if sess.UserID.Valid || sess.AuthenticatedAt != nil || len(sess.AuthEvents) != 0 {
			t.Errorf("Session() = UserID %v, AuthenticatedAt %v, AuthEvents %v; want none on a driver without the auth schema", sess.UserID, sess.AuthenticatedAt, sess.AuthEvents)
		}
	}
}

func testSessionUserColumns(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true})
	user := createUser(ctx, t, in.Driver, "user@example.com")

	tests := []struct {
		name              string
		req               *sessioninfo.NewSessionRequest
		wantUserID        impulse.NullUUID
		wantAuthenticated bool
	}{
		{
			name:              "a login records the user and when it authenticated",
			req:               &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: user.Username, UserID: user.ID},
			wantUserID:        impulse.NullUUIDFromUUID(user.ID),
			wantAuthenticated: true,
		},
		{
			name: "a preauth stepping stone has no user and authenticated nothing",
			req:  &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonPreauth, Username: "stepping-stone"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := in.Driver.InsertSession(ctx, newInsertSession(tt.req.Username), tt.req)
			if err != nil {
				t.Fatalf("InsertSession() error = %v", err)
			}

			sess := mustSession(ctx, t, in.Driver, id)
			if sess.UserID != tt.wantUserID {
				t.Errorf("Session().UserID = %v, want %v", sess.UserID, tt.wantUserID)
			}
			if got := sess.AuthenticatedAt != nil; got != tt.wantAuthenticated {
				t.Fatalf("Session().AuthenticatedAt = %v, want set %v", sess.AuthenticatedAt, tt.wantAuthenticated)
			}
			if tt.wantAuthenticated {
				assertSameTime(t, "Session().AuthenticatedAt", *sess.AuthenticatedAt, sess.CreatedAt)
			}
			if len(sess.AuthEvents) != 0 {
				t.Errorf("Session().AuthEvents = %v, want none without an auth events table", sess.AuthEvents)
			}
		})
	}
}

func testInitialAuthEvent(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true, AuthEvents: true})
	user := createUser(ctx, t, in.Driver, "events@example.com")

	tests := []struct {
		name       string
		identity   *sessioninfo.Identity
		wantEvents []sessioninfo.AuthEvent
	}{
		{
			name:     "a password sign-in records the password method",
			identity: &sessioninfo.Identity{Method: sessioninfo.MethodPassword, Subject: user.ID.String(), IDPAMR: []string{"pwd"}},
			wantEvents: []sessioninfo.AuthEvent{
				{Method: sessioninfo.MethodPassword, IDPAMR: []string{"pwd"}},
			},
		},
		{
			name: "a session without an identity records no event",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := in.Driver.InsertSession(ctx, newInsertSession(user.Username), &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: user.Username, UserID: user.ID, Identity: tt.identity})
			if err != nil {
				t.Fatalf("InsertSession() error = %v", err)
			}

			sess := mustSession(ctx, t, in.Driver, id)
			for i := range tt.wantEvents {
				tt.wantEvents[i].At = sess.CreatedAt
			}
			if diff := cmp.Diff(tt.wantEvents, sess.AuthEvents, eventsOpts); diff != "" {
				t.Errorf("Session().AuthEvents mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func testInitialAuthEvents(ctx context.Context, t *testing.T, h *AuthHarness) {
	signedInAt := time.Now().Add(-time.Hour).Truncate(time.Second)

	tests := []struct {
		name       string
		cfg        AuthConfig
		identity   func(user *dbtype.SessionUser) *sessioninfo.Identity
		events     []sessioninfo.AuthEvent
		wantEvents []sessioninfo.AuthEvent
	}{
		{
			name: "a step-up completion records the sign-in and the step-up, keeping the sign-in's time",
			cfg:  AuthConfig{Auth: true, AuthEvents: true},
			identity: func(user *dbtype.SessionUser) *sessioninfo.Identity {
				return &sessioninfo.Identity{Method: sessioninfo.MethodPassword, Subject: user.ID.String()}
			},
			events: []sessioninfo.AuthEvent{
				{Method: sessioninfo.MethodPassword, At: signedInAt},
				{Method: "email-otp"},
			},
			wantEvents: []sessioninfo.AuthEvent{
				{Method: sessioninfo.MethodPassword, At: signedInAt},
				{Method: "email-otp"},
			},
		},
		{
			name: "an identity resolved in the session transaction records the events it carries",
			cfg:  AuthConfig{Auth: true, AuthEvents: true},
			identity: func(user *dbtype.SessionUser) *sessioninfo.Identity {
				return &sessioninfo.Identity{Method: sessioninfo.MethodPassword, Subject: user.ID.String()}
			},
			events: []sessioninfo.AuthEvent{
				{Method: sessioninfo.MethodPassword},
				{Method: sessioninfo.MethodLinkConfirmation},
				{Method: "email-otp", IDPAMR: []string{"otp"}},
			},
			wantEvents: []sessioninfo.AuthEvent{
				{Method: sessioninfo.MethodPassword},
				{Method: sessioninfo.MethodLinkConfirmation},
				{Method: "email-otp", IDPAMR: []string{"otp"}},
			},
		},
		{
			name:   "a session started by the application records the events it was given, with no identity",
			cfg:    AuthConfig{Auth: true, AuthEvents: true},
			events: []sessioninfo.AuthEvent{{Method: "magic-link", Connection: "app"}},
			wantEvents: []sessioninfo.AuthEvent{
				{Method: "magic-link", Connection: "app"},
			},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			in := h.New(ctx, t, Auth, tt.cfg)
			user := createUser(ctx, t, in.Driver, "steps@example.com")

			req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonStepUp, Username: user.Username, UserID: user.ID, AuthEvents: tt.events}
			if tt.identity != nil {
				req.Identity = tt.identity(user)
			}
			id, err := in.Driver.InsertSession(ctx, newInsertSession(user.Username), req)
			if err != nil {
				t.Fatalf("InsertSession() error = %v", err)
			}

			// An event without a time takes the session's creation time; one with a
			// time keeps it.
			sess := mustSession(ctx, t, in.Driver, id)
			for i := range tt.wantEvents {
				if tt.wantEvents[i].At.IsZero() {
					tt.wantEvents[i].At = sess.CreatedAt
				}
			}
			if diff := cmp.Diff(tt.wantEvents, sess.AuthEvents, eventsOpts); diff != "" {
				t.Errorf("Session().AuthEvents mismatch (-want +got):\n%s", diff)
			}
			if got := h.CountAuthEvents(ctx, t, in.Raw, id); got != len(tt.wantEvents) {
				t.Errorf("auth event rows = %d, want %d", got, len(tt.wantEvents))
			}
		})
	}
}

func testImpersonatedSessionUser(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true, AuthEvents: true, Impersonation: true})
	user := createUser(ctx, t, in.Driver, "bob@example.com")

	tests := []struct {
		name       string
		principal  accesstypes.Principal
		userID     impulse.UUID
		wantUserID impulse.NullUUID
	}{
		{
			name:       "a user principal's session belongs to the impersonated user",
			principal:  accesstypes.UserPrincipal(accesstypes.User(user.ID.String())),
			userID:     user.ID,
			wantUserID: impulse.NullUUIDFromUUID(user.ID),
		},
		{
			name:      "a role principal's session belongs to no user",
			principal: accesstypes.RolePrincipal(accesstypes.Role("Viewer")),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			imp := dbtype.NewInsertImpersonation(&sessioninfo.Impersonation{Actor: "alice@example.com", Principal: tt.principal, ExpiresAt: time.Now().Add(time.Hour)})
			id, err := in.Driver.InsertImpersonatedSession(ctx, newInsertSession(user.Username), &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonImpersonation, Username: user.Username, UserID: tt.userID}, imp)
			if err != nil {
				t.Fatalf("InsertImpersonatedSession() error = %v", err)
			}

			sess := mustSession(ctx, t, in.Driver, id)
			if sess.UserID != tt.wantUserID {
				t.Errorf("Session().UserID = %v, want %v", sess.UserID, tt.wantUserID)
			}
			want := []sessioninfo.AuthEvent{{Method: sessioninfo.MethodImpersonation, Connection: "alice@example.com", At: sess.CreatedAt}}
			if diff := cmp.Diff(want, sess.AuthEvents, eventsOpts); diff != "" {
				t.Errorf("Session().AuthEvents mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func testAuthEventsCascade(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true, AuthEvents: true})
	user := createUser(ctx, t, in.Driver, "cascade@example.com")

	id, err := in.Driver.InsertSession(ctx, newInsertSession(user.Username), &sessioninfo.NewSessionRequest{
		Reason: sessioninfo.ReasonLogin, Username: user.Username, UserID: user.ID,
		Identity: &sessioninfo.Identity{Method: sessioninfo.MethodPassword, Subject: user.ID.String()},
	})
	if err != nil {
		t.Fatalf("InsertSession() error = %v", err)
	}
	if got := h.CountAuthEvents(ctx, t, in.Raw, id); got != 1 {
		t.Fatalf("auth events after sign-in = %d, want 1", got)
	}

	h.DeleteSession(ctx, t, in.Raw, id)

	if got := h.CountAuthEvents(ctx, t, in.Raw, id); got != 0 {
		t.Errorf("auth events after the session row is deleted = %d, want 0", got)
	}
}
