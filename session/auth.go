package session

import (
	"context"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/impulseframework/impulse/accesstypes"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/cookie"
	"github.com/impulseframework/impulse/session/internal/basesession"
	internalcookie "github.com/impulseframework/impulse/session/internal/cookie"
	"github.com/impulseframework/impulse/session/internal/sessioncore"
	"github.com/impulseframework/impulse/session/internal/signinflow"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/session/signin"
	"github.com/impulseframework/impulse/tracer"
)

var _ basesession.Handlers = &Auth[NoCustomData, NoCustomData, struct{}]{}

// authSettings holds Auth-only settings.
type authSettings struct {
	identityLinked IdentityLinkedHook
	pendingHook    PendingHook
	pendingTimeout time.Duration
	pendingCookie  string
	// identitiesTable, pendingSignInsTable, signInAttemptsTable, mailLinksTable,
	// signInMethodsTable and signInSettingsTable name the group's tables; nil keeps the
	// store's defaults.
	identitiesTable     *string
	pendingSignInsTable *string
	signInAttemptsTable *string
	mailLinksTable      *string
	signInMethodsTable  *string
	signInSettingsTable *string
	// mail is how the Auth sends mail: NewAuth's argument, validated.
	mail *mailSettings
	// invitationCookie names the cookie that carries an opened invitation into the
	// sign-in.
	invitationCookie string
	// tokens are the settings of the machine outlets' tokens and the device flow.
	tokens tokenSettings
	// tokensTable and deviceCodesTable name the group's tables; nil keeps the store's
	// defaults.
	tokensTable      *string
	deviceCodesTable *string
	// factors are the settings of the second factor the library runs: the email
	// code and the trusted devices.
	factors factorSettings
	// factorCodesTable and trustedDevicesTable name the group's tables; nil keeps
	// the store's defaults.
	factorCodesTable    *string
	trustedDevicesTable *string
}

// authOption is an Auth settings option.
type authOption struct {
	sessioncore.AuthOptionMarker
	apply func(*authSettings)
}

// Default pending-identity settings.
const (
	defaultPendingTimeout    = 10 * time.Minute
	defaultPendingCookieName = "auth-pending"
)

// defaultInvitationCookieName names the cookie that carries an opened invitation.
const defaultInvitationCookieName = "auth-invitation"

// IdentityLinkedHook is called after an external identity is linked to a user,
// for example to notify the user.
type IdentityLinkedHook = func(ctx context.Context, userID impulse.UUID, identity *sessioninfo.Identity) error

// WithIdentityLinked sets the hook called after every identity link, whether made by a
// pending identity's password confirmation or by the account rules during a sign-in
// (LinkIdentity or ProvisionUser). A resolver's link is committed before the sign-in
// policy runs, so it is reported even when the policy then denies the sign-in or holds
// it for MFA. An error from the hook is logged. The link and the sign-in stand.
func WithIdentityLinked(hook IdentityLinkedHook) AuthOption {
	return authOption{apply: func(s *authSettings) { s.identityLinked = hook }}
}

// PendingHook is called after a sign-in handler holds a sign-in as a pending identity,
// for example to send an MFA code or choose the next page. It runs after the pending
// identity is stored and its cookie set, and cannot change either.
//
// The hook may write the response itself, and the handler then writes nothing more.
// Otherwise it returns where the browser goes: Callback() redirects there and the JSON
// handlers return it as "redirectUrl". An empty URL keeps the default. The URL must be
// the application's own, never built from request input.
//
// An error discards the pending identity and fails the sign-in. See the README's "The
// redirect and response contract".
type PendingHook = func(ctx context.Context, w http.ResponseWriter, r *http.Request, pending *sessioninfo.PendingIdentity) (redirectURL string, err error)

// WithPendingHook sets the hook called when PasswordMethod.Login(), an external
// method's Callback() or Pending().ConfirmWithPassword() holds a sign-in as a pending
// identity. The AuthAPI methods never call it. They return a
// *signin.PendingSignInError instead.
func WithPendingHook(hook PendingHook) AuthOption {
	return authOption{apply: func(s *authSettings) { s.pendingHook = hook }}
}

// WithPendingTimeout sets how long a pending identity waits for confirmation or MFA.
// (default: 10m)
func WithPendingTimeout(d time.Duration) AuthOption {
	return authOption{apply: func(s *authSettings) { s.pendingTimeout = d }}
}

// WithPendingCookieName sets the name of the pending cookie, which carries the opaque
// token of the browser's pending sign-in. (default: "auth-pending")
func WithPendingCookieName(name string) AuthOption {
	return authOption{apply: func(s *authSettings) { s.pendingCookie = name }}
}

// WithIdentitiesTableName sets the name of the group's identities table, beside
// WithSessionTableName and WithUserTableName. (default: SessionIdentities)
func WithIdentitiesTableName(name string) AuthOption {
	return authOption{apply: func(s *authSettings) { s.identitiesTable = &name }}
}

// WithPendingSignInsTableName sets the name of the group's pending sign-ins table.
// (default: SessionPendingSignIns)
func WithPendingSignInsTableName(name string) AuthOption {
	return authOption{apply: func(s *authSettings) { s.pendingSignInsTable = &name }}
}

// WithSignInAttemptsTableName sets the name of the group's sign-in attempts table.
// (default: SessionSignInAttempts)
func WithSignInAttemptsTableName(name string) AuthOption {
	return authOption{apply: func(s *authSettings) { s.signInAttemptsTable = &name }}
}

// WithMailLinksTableName sets the name of the group's mail links table. (default:
// SessionMailLinks)
func WithMailLinksTableName(name string) AuthOption {
	return authOption{apply: func(s *authSettings) { s.mailLinksTable = &name }}
}

// WithSignInMethodsTableName sets the name of the group's sign-in methods table.
// (default: SessionSignInMethods)
func WithSignInMethodsTableName(name string) AuthOption {
	return authOption{apply: func(s *authSettings) { s.signInMethodsTable = &name }}
}

// WithSignInSettingsTableName sets the name of the group's sign-in settings table.
// (default: SessionSignInSettings)
func WithSignInSettingsTableName(name string) AuthOption {
	return authOption{apply: func(s *authSettings) { s.signInSettingsTable = &name }}
}

// WithInvitationCookieName sets the name of the cookie that carries an invitation the
// browser opened into the sign-in. (default: auth-invitation)
func WithInvitationCookieName(name string) AuthOption {
	return authOption{apply: func(s *authSettings) { s.invitationCookie = name }}
}

// WithFactorCodesTableName sets the name of the group's factor codes table. (default:
// SessionFactorCodes)
func WithFactorCodesTableName(name string) AuthOption {
	return authOption{apply: func(s *authSettings) { s.factorCodesTable = &name }}
}

// WithTrustedDevicesTableName sets the name of the group's trusted devices table.
// (default: SessionTrustedDevices)
func WithTrustedDevicesTableName(name string) AuthOption {
	return authOption{apply: func(s *authSettings) { s.trustedDevicesTable = &name }}
}

// SignedIn is a session a sign-in established, as the SignedInHook receives it.
type SignedIn struct {
	// SessionID is the new session's ID.
	SessionID impulse.UUID
	// UserID and Username are the user the session belongs to.
	UserID   impulse.UUID
	Username string
	// Reason is how the session came about: ReasonLogin for a password or external
	// sign-in, ReasonExternalAuth for one the application started
	// (StartAuthenticatedSession), ReasonStepUp for an MFA completion,
	// ReasonIdentityLinked for a password confirmation.
	Reason sessioninfo.NewSessionReason
	// Events are the auth events the session recorded, the sign-in's method first.
	Events []sessioninfo.AuthEvent
}

// SignedInHook is called once for every session a sign-in establishes, after the
// transaction that inserted it has committed: a password or external sign-in, a
// confirmation, an MFA completion, a session the application started. It is never
// called for a refused sign-in or one held as a pending identity. An error is logged
// and the session stands.
type SignedInHook = func(ctx context.Context, signedIn *SignedIn) error

// nameTables applies the Auth's own table-name options to the store, whose setters
// validate each name (dbtype.ValidateTableName); a refusal is reported under the
// option's name. The base options (session and user tables) go the same way through
// sessioncore.NewBaseSession.
func nameTables[Tx any](storage sessionstorage.AuthStore[Tx], settings *authSettings) error {
	for _, table := range []struct {
		option string
		name   *string
		set    func(string) error
	}{
		{option: "WithIdentitiesTableName", name: settings.identitiesTable, set: storage.SetIdentitiesTableName},
		{option: "WithPendingSignInsTableName", name: settings.pendingSignInsTable, set: storage.SetPendingSignInsTableName},
		{option: "WithSignInAttemptsTableName", name: settings.signInAttemptsTable, set: storage.SetSignInAttemptsTableName},
		{option: "WithMailLinksTableName", name: settings.mailLinksTable, set: storage.SetMailLinksTableName},
		{option: "WithSignInMethodsTableName", name: settings.signInMethodsTable, set: storage.SetSignInMethodsTableName},
		{option: "WithSignInSettingsTableName", name: settings.signInSettingsTable, set: storage.SetSignInSettingsTableName},
		{option: "WithTokensTableName", name: settings.tokensTable, set: storage.SetTokensTableName},
		{option: "WithDeviceCodesTableName", name: settings.deviceCodesTable, set: storage.SetDeviceCodesTableName},
		{option: "WithFactorCodesTableName", name: settings.factorCodesTable, set: storage.SetFactorCodesTableName},
		{option: "WithTrustedDevicesTableName", name: settings.trustedDevicesTable, set: storage.SetTrustedDevicesTableName},
	} {
		if table.name == nil {
			continue
		}
		if err := table.set(*table.name); err != nil {
			return errors.Wrapf(err, "%s()", table.option)
		}
	}

	return nil
}

// Auth is one session type that any registered sign-in method can establish. Auth,
// sessions, cookies, XSRF, custom data, impersonation and the middleware work the same
// for every method.
//
// Register sign-in methods with PasswordSignIn, azure.SignIn and google.SignIn after
// NewAuth and before serving the first request (the WorkOS method is in an internal
// package, which an application cannot import). Each returns the method's handlers.
//
// Every session belongs to a user (SessionUsers). A sign-in either establishes a
// session under a new session ID, becomes a pending identity that waits for a password
// confirmation or the application's MFA step, or is refused with a
// sessioninfo.LoginRefusalCode. See the README's "Auth sessions (multiple sign-in
// methods)" for the routes and the redirect contract.
//
// Tx is the storage's transaction type (pgx.Tx, *spanner.ReadWriteTransaction), taken
// from the storage NewAuth is given. The sign-in policy and every external method's user
// resolver receive it, so a hook written for the other backend does not compile.
type Auth[SessionData, UserData, Tx any] struct {
	baseSession *basesession.BaseSession
	storage     sessionstorage.AuthStore[Tx]
	settings    authSettings
	// policy is the rule a sign-in is decided by, nil when the library's own checks
	// decide alone: the application's sign-in rule (applicationPolicy, set by
	// SetSignInPolicy before the Auth is in use), with the rule on a super
	// administrator's second factor after it where the Auth has super administrators.
	policy            signin.Policy[Tx]
	applicationPolicy signin.Policy[Tx]
	// superAdministrators reports who holds the Super Administrator role, nil for an
	// Auth built with NoSuperAdministrators. NewAuth is told it and nothing changes it.
	superAdministrators signin.SuperAdministratorCheck
	// signedIn is the application's after-sign-in hook, nil when it set none. Set by
	// SetSignedInHook before the Auth is in use.
	signedIn SignedInHook
	// ownTenants answers the own-tenants handlers (OwnTenants, LeaveTenant), nil when the
	// application set none. Set by SetOwnTenants before the Auth is in use.
	ownTenants *OwnTenantsHook
	// passwordDisabledCheck says whose password the tenants' sign-in settings disable
	// (signin.PasswordsDisabledByTenants), nil when the application set none. Set by
	// SetPasswordDisabledCheck before the Auth is in use.
	passwordDisabledCheck signin.PasswordDisabledCheck

	// cookies is the session cookie client. It also writes the external methods' state
	// cookies.
	cookies *internalcookie.Client
	// pendingCookies writes the pending-identity cookie.
	pendingCookies *internalcookie.Client
	// invitationCookies writes and reads the invitation cookie.
	invitationCookies *internalcookie.Client
	// trustedCookies writes and reads the trusted device cookie.
	trustedCookies *internalcookie.Client
	// acceptInvitation is the application's invitation acceptor, nil for none.
	acceptInvitation signinflow.InvitationAcceptor[Tx]
	userRenamed      UserRenamedHook
	// directories reads the tenants' declarations of a directory for a stranger's
	// first sign-in, and directorySeated seats the person they admit; both nil for
	// none. Set by SetDirectoryAdmission before the Auth is in use.
	directories     signin.DirectoryReader[Tx]
	directorySeated signinflow.DirectorySeater[Tx]
	// directoryOnlyIn reports whether a tenant is the only place a person holds
	// anything: with directories it decides a first link made on a name alone. Set
	// with them.
	directoryOnlyIn signinflow.OnlyInTenant[Tx]
	// signUpSeated gives a person who signed up through an open method the method's
	// default role, nil for none.
	signUpSeated signinflow.SignUpSeater[Tx]
	// tenantNames names the tenant an invitation seats a person in, for the invitation
	// screen; nil for none. Set by SetTenantNames before the Auth is in use.
	tenantNames TenantNamer
	// methods are the registered sign-in methods. Read them through registered.
	methods signInMethods[Tx]
	// background counts the work the Auth does once a request is answered (later),
	// which AuthAPI.WaitForMail waits for.
	background sync.WaitGroup
}

// SuperAdministrators is what NewAuth is told about the holders of the Super
// Administrator role (accesstypes.SuperAdministrator), the role the access library
// defines in every auth. An Auth is not built without one: a holder who signed in with
// no second factor would be the one account no tenant's sign-in settings shut out, behind
// a password alone. Build one with SuperAdministratorsBy, or with NoSuperAdministrators
// for an application that has no permission engine; the zero value is refused.
type SuperAdministrators struct {
	// none is NoSuperAdministrators' word, and checked SuperAdministratorsBy's, with the
	// check it was given; the zero value carries neither.
	none    bool
	checked bool
	check   signin.SuperAdministratorCheck
}

// SuperAdministratorsBy tells an Auth who its super administrators are: the people
// check reports. Pass signin.SuperAdministrators over the access library's user manager
// (on Spanner over resource.SignInRoles of it, as the tenant policy is), built before
// the Auth. The Auth then holds a holder's sign-in for the code it mails, whatever the
// application's sign-in rule asks of anyone else (signin.SuperAdministratorFactor).
// NewAuth refuses a nil check. A sign-in through a method that synchronizes roles is
// not held: there the directory is the authority on who is a super administrator, and
// the application's own rule is asked of that person as of anyone.
func SuperAdministratorsBy(check signin.SuperAdministratorCheck) SuperAdministrators {
	return SuperAdministrators{none: false, checked: true, check: check}
}

// NoSuperAdministrators tells an Auth that nobody of its application is a super
// administrator, because the application has no permission engine and so no role of any
// kind. An application whose people hold roles passes SuperAdministratorsBy: with this,
// a person who holds the Super Administrator role would sign in as anyone else does.
func NoSuperAdministrators() SuperAdministrators {
	return SuperAdministrators{none: true, checked: false, check: nil}
}

// validate refuses what NewAuth cannot build an Auth from: no word on the super
// administrators at all, and a word with no check behind it.
func (s SuperAdministrators) validate() error {
	switch {
	case s.none:
		return nil
	case !s.checked:
		return errors.New("NewAuth was not told who the Auth's super administrators are: pass session.SuperAdministratorsBy(signin.SuperAdministrators(...)) over the access library's user manager, " +
			"so a holder of the Super Administrator role signs in with a second factor, or session.NoSuperAdministrators() for an application with no permission engine")
	case s.check == nil:
		return errors.New("session.SuperAdministratorsBy was given no check: pass signin.SuperAdministrators over the access library's user manager")
	default:
		return nil
	}
}

// NewAuth creates an Auth session for the given storage. The storage's custom session
// and user data configurations must be built for SessionData and UserData. Tx is
// inferred from the storage: an application writes
// NewAuth[SessionData, UserData](store, cookieKey, superAdministrators, mail, options...).
//
// superAdministrators says who holds the Super Administrator role
// (SuperAdministratorsBy, or NoSuperAdministrators for an application with no permission
// engine). It is an argument and not an option so that no Auth is built without it: a
// holder always signs in with a second factor, whether or not the application's sign-in
// rule asks anyone for one. A sign-in the rule allows, or any sign-in where no rule is
// set, is held for the code the library mails when the person holds the role, unless
// the sign-in already carries a second factor as any account's may (the directory's
// attestation, or a browser the person asked to be remembered).
//
// mail is how the Auth sends mail (MailSettings: the sender, the application's name and
// its login page). It is an argument and not an option because nothing works without
// it: an invitation, a sign-up, a password reset, the code a sign-in is held for and the
// notices that tell a person their account changed all go by mail. NewAuth refuses a nil
// sender, an empty application name and a login URL that is not absolute.
//
// A sign-in is decided by the library's own checks: the user exists and is enabled,
// the identity is linked or resolved, roles reconcile. An application with rules of
// its own sets a sign-in policy with SetSignInPolicy. Each external method's first
// sign-in rule is given to that method's registration.
// cookieKey: A Base64-encoded string representing at least 32 bytes
// of cryptographically secure random data.
func NewAuth[SessionData, UserData, Tx any](
	storage sessionstorage.AuthStore[Tx], cookieKey string, superAdministrators SuperAdministrators, mail MailSettings, options ...AuthOption,
) (*Auth[SessionData, UserData, Tx], error) {
	if storage == nil {
		return nil, errors.New("storage is required: pass sessionstorage.NewSpannerAuth or NewPostgresAuth")
	}
	if err := sessioncore.VerifyCustomDataTypes[SessionData, UserData](storage); err != nil {
		return nil, err
	}

	settings := authSettings{
		pendingTimeout: defaultPendingTimeout, pendingCookie: defaultPendingCookieName, invitationCookie: defaultInvitationCookieName, tokens: defaultTokenSettings(),
		factors: defaultFactorSettings,
	}
	var cookieOpts []internalcookie.Option
	for _, opt := range options {
		switch o := opt.(type) {
		case authOption:
			o.apply(&settings)
		case CookieOption:
			cookieOpts = append(cookieOpts, internalcookie.Option(o))
		}
	}
	if settings.pendingTimeout <= 0 {
		return nil, errors.New("the pending timeout must be positive")
	}
	settings.mail = &mailSettings{sender: mail.Sender, settings: mail}
	if err := settings.mail.validate(); err != nil {
		return nil, err
	}
	if err := settings.tokens.validate(); err != nil {
		return nil, err
	}
	if err := settings.factors.validate(); err != nil {
		return nil, err
	}
	if err := superAdministrators.validate(); err != nil {
		return nil, err
	}
	if err := nameTables(storage, &settings); err != nil {
		return nil, err
	}

	baseSession, cookieClient, err := sessioncore.NewBaseSession(storage, cookieKey, options)
	if err != nil {
		return nil, err
	}
	cookies, err := newAuthCookies(cookieKey, cookieClient.CookieName, cookieOpts, &settings)
	if err != nil {
		return nil, err
	}

	a := &Auth[SessionData, UserData, Tx]{
		baseSession:         baseSession,
		storage:             storage,
		settings:            settings,
		superAdministrators: superAdministrators.check,
		cookies:             cookieClient,
		pendingCookies:      cookies.pending,
		invitationCookies:   cookies.invitation,
		trustedCookies:      cookies.trusted,
	}
	a.policy = a.decidingPolicy()
	a.methods.init(storage)

	return a, nil
}

// authCookies are the cookie clients of the Auth's own cookies, beside the session's.
type authCookies struct {
	pending, invitation, trusted *internalcookie.Client
}

// newAuthCookies builds the clients of the pending, invitation and trusted device
// cookies under the session's cookie key and options, refusing a name that is empty
// or that another of the four cookies has, or that the invitation cookie's companion
// takes (seatedCookie).
func newAuthCookies(cookieKey, sessionCookie string, opts []internalcookie.Option, settings *authSettings) (*authCookies, error) {
	names := []struct {
		what, name string
	}{
		{what: "pending", name: settings.pendingCookie},
		{what: "invitation", name: settings.invitationCookie},
		{what: "trusted device", name: settings.factors.trustedCookie},
	}
	taken := map[string]bool{sessionCookie: true}
	clients := make([]*internalcookie.Client, 0, len(names))
	for _, n := range names {
		if n.name == "" || taken[n.name] {
			return nil, errors.Newf("the %s cookie name %q must be set and differ from the session cookie name and the Auth's other cookie names", n.what, n.name)
		}
		taken[n.name] = true
		client, err := internalcookie.NewCookieClient(cookieKey, append(slices.Clone(opts), internalcookie.WithCookieName(n.name))...)
		if err != nil {
			return nil, errors.Wrap(err, "cookie.NewCookieClient()")
		}
		clients = append(clients, client)
	}
	// The tenant an accepted invitation leads to rides in a cookie named after the
	// invitation cookie (seatedCookie), which no other cookie may be named.
	if seated := settings.invitationCookie + seatedCookieSuffix; taken[seated] {
		return nil, errors.Newf(
			"the cookie name %q is the one the invitation cookie %q keeps for the tenant an accepted invitation leads to: give the other cookie another name",
			seated, settings.invitationCookie,
		)
	}

	return &authCookies{pending: clients[0], invitation: clients[1], trusted: clients[2]}, nil
}

// SetSignInPolicy sets the application's sign-in rule: asked once a sign-in's user is
// known and before any session exists, for every sign-in method and for a session the
// application starts for a user it authenticated itself (AuthAPI.StartAuthenticatedSession),
// with signin.Allow, signin.Deny or signin.RequireMFA. Without a policy the library's
// own checks decide. After the policy, or in its place where there is none, the Auth's
// sign-in settings are read: while they ask everyone for a second factor, a sign-in the
// policy allows is held for the library's mailed code unless it already carries a
// second factor (AuthAPI.SetSecondFactorRequired).
// Set it after NewAuth and before serving the first request; a nil
// policy, a second policy, or a policy set once the Auth is in use is a wiring
// mistake and panics, as a misregistered sign-in method does.
func (a *Auth[SessionData, UserData, Tx]) SetSignInPolicy(policy signin.Policy[Tx]) {
	if policy == nil {
		panic("session: SetSignInPolicy was given nil: pass the application's rule, or set no policy and the library's checks decide")
	}
	a.methods.mu.Lock()
	defer a.methods.mu.Unlock()

	if a.methods.inUse.Load() {
		panic("session: the sign-in policy was set after the Auth served a request: set it before serving")
	}
	if a.applicationPolicy != nil {
		panic("session: the sign-in policy was set twice: one Auth has one rule")
	}
	a.applicationPolicy = policy
	a.policy = a.decidingPolicy()
}

// decidingPolicy is the rule a sign-in is decided by: the application's rule, and after
// it the super administrator's second factor where the Auth has super administrators.
// The caller holds the methods' lock, or is NewAuth, before the Auth is anyone's.
func (a *Auth[SessionData, UserData, Tx]) decidingPolicy() signin.Policy[Tx] {
	if a.superAdministrators == nil {
		return a.applicationPolicy
	}

	return signin.SuperAdministratorFactor(a.applicationPolicy, a.superAdministrators)
}

// SetSignedInHook sets the application's after-sign-in hook, called once for every
// session a sign-in establishes, whatever the method, after the transaction that
// inserted it has committed, with the user and the sign-in's events; never for a
// refusal or a sign-in held for confirmation or MFA. An error from the hook is logged
// and the session stands. Set it after NewAuth and before serving the first request; a
// nil hook, a second hook, or a hook set once the Auth is in use is a wiring mistake
// and panics, as SetSignInPolicy does.
func (a *Auth[SessionData, UserData, Tx]) SetSignedInHook(hook SignedInHook) {
	if hook == nil {
		panic("session: SetSignedInHook was given nil: pass the application's hook, or set none")
	}
	a.methods.mu.Lock()
	defer a.methods.mu.Unlock()

	if a.methods.inUse.Load() {
		panic("session: the after-sign-in hook was set after the Auth served a request: set it before serving")
	}
	if a.signedIn != nil {
		panic("session: the after-sign-in hook was set twice: one Auth has one hook")
	}
	a.signedIn = hook
}

// SetOwnTenants sets the hook that answers OwnTenants and LeaveTenant: the application's
// rules of who belongs where, which the generated identity client's OwnTenants holds.
// Without it both handlers answer 404 and the methods answer names neither route. Set it
// after NewAuth and before the routes are wired: the methods answer is built when Methods
// is called, so a hook set after it would be served and never told to the page. A hook
// with no List or no Leave, a hook set once the Auth is in use, one set after Methods was
// called, or a second hook is a wiring mistake and panics, as SetSignedInHook does.
func (a *Auth[SessionData, UserData, Tx]) SetOwnTenants(hook OwnTenantsHook) {
	if hook.List == nil || hook.Leave == nil {
		panic("session: SetOwnTenants was given a hook with no List or no Leave: pass the identity client's OwnTenants, or set none")
	}
	a.methods.mu.Lock()
	defer a.methods.mu.Unlock()

	if a.methods.inUse.Load() {
		panic("session: the own-tenants hook was set after the Auth served a request: set it before serving")
	}
	if len(a.methods.lists) > 0 {
		panic("session: the own-tenants hook was set after Methods was called, so the methods answer names no tenants paths: set it before the routes are wired")
	}
	if a.ownTenants != nil {
		panic("session: the own-tenants hook was set twice: one Auth has one")
	}
	a.ownTenants = &hook
}

// SetPasswordDisabledCheck sets the check that says whose password the tenants' sign-in
// settings disable: the tenant policy's own decision for a sign-in with a password,
// signin.PasswordsDisabledByTenants over the role store and the declarations the policy
// reads. With it, what a person signs in with (SignInMeans) says so (PasswordDisabled),
// so their account page draws no Password line, and the person is refused a first
// password (FirstPassword(), 403); a password their account holds stays. The check is
// handed the directories the account signs in through, read with the reader set with
// SetDirectoryAdmission, and none on an Auth that sets no directory admission. Without
// the check, and on an Auth with no password method, nobody's password is disabled. Set
// it after NewAuth and before serving the first request; a nil check, a second check, or
// a check set once the Auth is in use is a wiring mistake and panics, as SetOwnTenants
// does.
func (a *Auth[SessionData, UserData, Tx]) SetPasswordDisabledCheck(check signin.PasswordDisabledCheck) {
	if check == nil {
		panic("session: SetPasswordDisabledCheck was given nil: pass signin.PasswordsDisabledByTenants over the role store and the declarations the tenant policy reads, or set none")
	}
	a.methods.mu.Lock()
	defer a.methods.mu.Unlock()

	if a.methods.inUse.Load() {
		panic("session: the password-disabled check was set after the Auth served a request: set it before serving")
	}
	if a.passwordDisabledCheck != nil {
		panic("session: the password-disabled check was set twice: one Auth has one")
	}
	a.passwordDisabledCheck = check
}

// UserRenamedHook is told that a user's username changed by the library's own doing:
// under a password method declared UsernameIsEmail, the person who verifies a new
// address is renamed to it, live sessions included, as a rename does. Role memberships
// are held by the id of the account and follow nothing; an application that keys
// anything of its own by the username moves it from the old name to the new here. The
// hook runs after the rename is written and outside any transaction; an error is logged
// and answered to the person as the verification having failed, with the address
// verified and the username changed, so the hook is written to be safe to run again
// (adding under the new name before removing under the old).
type UserRenamedHook = func(ctx context.Context, userID impulse.UUID, from, to string) error

// SetUserRenamed sets the hook a rename the library performs is reported to. An
// application whose password method declares UsernameIsEmail and that keys anything of
// its own by the username needs one. Set it after NewAuth and before serving the first
// request; a nil hook, a second hook, or a hook set once the Auth is in use panics.
func (a *Auth[SessionData, UserData, Tx]) SetUserRenamed(hook UserRenamedHook) {
	if hook == nil {
		panic("session: SetUserRenamed was given nil: pass the application's hook, or set none")
	}
	a.methods.mu.Lock()
	defer a.methods.mu.Unlock()

	if a.methods.inUse.Load() {
		panic("session: the rename hook was set after the Auth served a request: set it before serving")
	}
	if a.userRenamed != nil {
		panic("session: the rename hook was set twice: one Auth has one hook")
	}
	a.userRenamed = hook
}

// InvitationAcceptedHook is the application's half of an invitation's acceptance: it
// gives what the invitation names, its roles in its tenant, to the user who accepted
// it, inside the transaction that consumes the invitation, which it is handed (tx, the
// storage's transaction type): the invitation is spent and its roles given together,
// or neither. It runs for every acceptance: the one a sign-in or a sign-up performs
// with the invitation the browser carries, and Invitation().Accept() for a person
// already signed in. The user is named by id, the key role memberships are held under,
// and by username, what a log line reads; a user the same transaction makes (one a
// directory sign-in provisions, and the account of a sign-up with a password through
// the invitation) is buffered in it and is not readable until the commit, so the hook
// writes under the id it is given and looks nobody up.
//
// For an invitation that makes a tenant (inv.NewTenant), the hook makes the tenant and
// seats the user in it, and sets inv.MadeTenantID to the key of the tenant it made,
// each time it runs. The library reads the field once the acceptance has committed,
// and the application opens for the person in that tenant, as it opens in the tenant
// an invitation names (Authenticated's invitedTenant). A hook that leaves it empty
// lands the person as any sign-in does.
//
// The hook reads and writes through tx alone. The Spanner emulator allows one
// read-write transaction at a time, so a second transaction, or a read through another
// client, inside the hook waits on the transaction the hook runs in and never returns.
//
// committed, nil for none, is work the library runs once that transaction has
// committed and before the sign-in policy decides: the word to a permission engine
// that its rows changed, so the policy sees the membership. Spanner runs a
// transaction's closure again when it aborts, so the hook may be called more than
// once for one acceptance; only the committed function of the call whose transaction
// committed is run, once. A hook that fails fails the transaction and ends the sign-in
// with its error: nothing is consumed, a user the sign-in would have made is not
// created, and the invitation stands for the next attempt.
type InvitationAcceptedHook[Tx any] = func(ctx context.Context, tx Tx, userID impulse.UUID, username string, inv *sessioninfo.Invitation) (committed func(ctx context.Context), err error)

// SetInvitationAccepted sets the application's invitation acceptor, the hook that
// grants what an invitation names when it is accepted. An Auth that sends invitations
// (AuthAPI.Invite) needs one, or an acceptance grants nothing. Set it after NewAuth
// and before serving the first request; a nil hook, a second hook, or a hook set once
// the Auth is in use is a wiring mistake and panics, as SetSignInPolicy does.
func (a *Auth[SessionData, UserData, Tx]) SetInvitationAccepted(hook InvitationAcceptedHook[Tx]) {
	if hook == nil {
		panic("session: SetInvitationAccepted was given nil: pass the application's hook, or set none")
	}
	a.methods.mu.Lock()
	defer a.methods.mu.Unlock()

	if a.methods.inUse.Load() {
		panic("session: the invitation hook was set after the Auth served a request: set it before serving")
	}
	if a.acceptInvitation != nil {
		panic("session: the invitation hook was set twice: one Auth has one hook")
	}
	a.acceptInvitation = hook
}

// Authenticated answers {"authenticated", "sessionId", "username", "name", "look",
// "passwordChangeRequired", "email", "emailVerified", "emailHint", "invitedTenant", "impersonation"}. A session whose user is
// missing or disabled is reported as not authenticated. passwordChangeRequired is set
// while an administrator's required password change stands (see "Users and the forced
// password change" in the README): the session read is answered under the mark, so the
// login page can show the change form. email is the user's address when they hold one,
// with emailVerified saying whether they have proven it; emailHint, present when the
// user holds no address, is the address a directory asserted at a sign-in, offered as
// a prefill for the add-address step and trusted for nothing. name is the person's
// name as people read it, present when the user holds one: what a page shows for the
// person in place of the username, which stays what identifies them. look is the look
// the person chose ({"scheme", "palette"}, each present where they chose it), present
// when the user holds one: what their pages draw themselves in on every browser.
// directory is the organization's directory the session was established through
// ({"method", "connection"}: a Google Workspace by its connection, a Microsoft Entra
// tenant by its tenant id), present for a sign-in through one and absent for a
// password, for a personal account, which is no organization's directory, and for an
// impersonated session: what a page offers the person to make a tenant's directory.
// invitedTenant is the tenant an invitation the person has just accepted seated them
// in, by this sign-in or by hand since, the tenant the invitation names or the one its
// acceptance made: present on the first session read after the acceptance and on no
// later one, and never on an impersonated session. It is where the application opens
// for the person, whatever else they hold. sessionId is the id of the session the
// request arrived on: what a list of sessions marks as the reader's own. It is no
// secret: the cookie that carries a session is sealed with the server's key, and the
// id alone opens nothing.
func (a *Auth[S, U, Tx]) Authenticated() http.HandlerFunc {
	type directory struct {
		Method     sessioninfo.AuthMethod `json:"method"`
		Connection string                 `json:"connection"`
	}
	type response struct {
		Authenticated bool `json:"authenticated"`
		// SessionID is the id of the session the request arrived on, absent from the
		// answer of a request that is not signed in.
		SessionID              string                             `json:"sessionId,omitempty"`
		Username               string                             `json:"username"`
		Name                   *string                            `json:"name,omitempty"`
		Look                   *sessioninfo.Look                  `json:"look,omitempty"`
		PasswordChangeRequired bool                               `json:"passwordChangeRequired"`
		Email                  *string                            `json:"email,omitempty"`
		EmailVerified          bool                               `json:"emailVerified"`
		EmailHint              string                             `json:"emailHint,omitempty"`
		Impersonation          *basesession.ImpersonationResponse `json:"impersonation,omitempty"`
		// BrowserRemembered says the browser carries a remembered entry of the second
		// factor for the person, so their account page offers to forget it (ForgetBrowser).
		// Never for an impersonated session, which may not forget it.
		BrowserRemembered bool `json:"browserRemembered"`
		// Directory is the organization's directory the session was established
		// through, nil for any other session.
		Directory *directory `json:"directory,omitempty"`
		// InvitedTenant is the tenant an invitation the person has just accepted seated
		// them in, told once (takeSeated).
		InvitedTenant string `json:"invitedTenant,omitempty"`
	}

	return a.serving(a.baseSession.Handle(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		ctx, err := a.validate(ctx)
		if err != nil {
			if httpio.HasUnauthorized(err) {
				return httpio.NewEncoder(w).Ok(response{})
			}

			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}

		imp, _ := sessioninfo.ImpersonationFromCtx(ctx)
		user := sessioninfo.UserFromCtx(ctx)
		var through *directory
		if established, ok := sessioninfo.DataFromCtx(ctx).EstablishedBy(); ok {
			if d := (signin.Directory{Method: established.Method, Connection: established.Connection}); d.Organization() {
				through = &directory{Method: d.Method, Connection: d.Connection}
			}
		}
		var invited string
		if imp == nil && user != nil {
			invited = a.takeSeated(w, r, user.ID)
		}

		return httpio.NewEncoder(w).Ok(response{
			Authenticated:          true,
			SessionID:              sessioninfo.IDFromCtx(ctx).String(),
			Username:               sessioninfo.FromCtx(ctx).Username,
			Name:                   user.Name,
			Look:                   lookOf(user),
			PasswordChangeRequired: user.PasswordChangeRequired,
			Email:                  user.Email,
			EmailVerified:          user.EmailVerified(),
			EmailHint:              a.emailHint(ctx, user),
			Impersonation:          basesession.NewImpersonationResponse(imp),
			BrowserRemembered:      imp == nil && user != nil && browserRemembered(ctx, user.ID),
			Directory:              through,
			InvitedTenant:          invited,
		})
	}))
}

// lookOf is the look the user holds as the session read answers it: nil for none, so
// the answer leaves it out.
func lookOf(user *sessioninfo.UserInfo) *sessioninfo.Look {
	if user == nil || user.Look.IsZero() {
		return nil
	}
	look := user.Look

	return &look
}

// Logout destroys the current session.
func (a *Auth[S, U, Tx]) Logout() http.HandlerFunc {
	return a.serving(a.baseSession.Logout())
}

// StartSession restores or initializes the session. It also makes the browser's
// pending identity, if any, available to the Pending handlers and the AuthAPI pending
// methods, the invitation and the trusted device the browser carries to the sign-in,
// and the browser's name to the session a sign-in makes.
func (a *Auth[S, U, Tx]) StartSession(next http.Handler) http.Handler {
	return a.serving(a.baseSession.StartSession(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := withBrowser(a.withTrustedDeviceCookie(a.withInvitationCookie(a.withPendingCookie(r), r), r), r)
		next.ServeHTTP(w, r.WithContext(ctx))
	})))
}

// ValidateSession validates the session and loads its user by UserId. A session
// with no user, or whose user is missing or disabled, is refused with 401. The
// user is available through sessioninfo.UserFromCtx. A foreign actor's
// role-principal impersonation has no user and carries only its username. A user an
// administrator required a new password of is refused with 403 and {"message", "code":
// "password_change_required"} until the change: route the password change, the
// session read and the logout outside this middleware, since they validate on their
// own and are what the user reaches meanwhile. An impersonated session is not held to
// the mark.
func (a *Auth[S, U, Tx]) ValidateSession(next http.Handler) http.Handler {
	return a.serving(a.baseSession.Handle(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		ctx, err := a.validateRequest(ctx)
		if err != nil {
			// The password change mark is a coded refusal; anything else is the usual
			// client message.
			var refusal *sessioninfo.LoginRefusal
			if errors.As(err, &refusal) {
				return writeSignInError(ctx, w, err)
			}

			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}

		next.ServeHTTP(w, r.WithContext(ctx))

		return nil
	}))
}

// DirectorySeatedHook is the application's half of a tenant directory's admission: what
// it alone can read and write of a person's roles, in two functions.
//
// Seat seats the user at their sign-in through a tenant's directory, named by id (the
// key an application's memberships are held under) and by username (what a log line
// reads), with seats, the default seat of the tenant whose directory the identity came
// through, inside the sign-in's transaction, which it is handed: where that transaction
// links the identity to the user, the link is made and the person seated together, or
// neither, whether it makes the account too or the account stood before, and whether
// the link is made at once or once the user's password has confirmed it. The library
// calls it at every sign-in through the directory by an account that is not disabled,
// whether the link is made then or stood before. It gives a seat only where the person
// holds nothing in the tenant: a person who holds anything there keeps what they hold.
// So a person an administrator removed from the tenant is given the default seat again
// the next time they sign in through the directory; a role with no privileges keeps one
// person out while the tenant is open, and making the tenant invite only keeps everyone
// out who is not invited. A seat the invitation this sign-in consumes names too (the
// same role in the same tenant) is not among seats: InvitationAcceptedHook gives it, in
// the same transaction, and the two hooks never write one membership twice. It is not
// called for no seats. It is held to what InvitationAcceptedHook is: tx alone for its
// reads and writes, a user the transaction made not readable in it, a committed function
// (nil for none) the library runs once the transaction has committed and before the
// sign-in policy decides, and an error that fails the transaction and ends the sign-in,
// so no account is left holding nothing and no link is left that gave nothing.
//
// OnlyIn reports whether tenant is the only place the user holds anything, read through
// tx: the user holds a role, or a place in a group, in tenant itself, and nothing in
// another tenant, in every tenant or outside the tenants. The library asks it before it
// links an identity, on its sign-in name alone, to an account that has no password and
// has never signed in. A directory asserts the names it likes, and the link hands
// whoever it signs in everything the account holds, so such a link is made only through
// the directory of the one tenant the person belongs to, which is given nothing the
// tenant's own administrators could not give, or by a sign-in that carries the person's
// own invitation; any other sign-in under the name is refused as a person no account
// was created for is. The deployment's own first administrator, who belongs to no
// tenant, is no such account: the deployment invites the person
// (AuthAPI.InviteFirstAdministrator), and their invitation is what vouches.
type DirectorySeatedHook[Tx any] struct {
	Seat   func(ctx context.Context, tx Tx, userID impulse.UUID, username string, seats []signin.DirectorySeat) (committed func(ctx context.Context), err error)
	OnlyIn func(ctx context.Context, tx Tx, userID impulse.UUID, tenant accesstypes.Domain) (bool, error)
}

// SetDirectoryAdmission sets what a tenanted application gives a person's sign-in
// through a tenant's directory: reader, which reads the declaration of the directory
// the identity came through (signin.DirectoryReader: the tenant whose directory it is,
// since a directory belongs to one tenant), and hook, which seats the person with the
// default seat the declaration names, at every sign-in through the directory, where the
// person holds nothing in the tenant. With them the standard rule asks
// the tenant first: its declaration decides before the sign-in method's own choice,
// open or invite only. A default seat admits a stranger with it, and a declaration naming none
// refuses a stranger by invitation only (invitation_only, unless the sign-in carries
// an invitation), with no account created. A person who has an account is no stranger.
// One with a password confirms the link with it, and the default seat is given with the
// link when they hold nothing in the tenant, as at every later sign-in through the
// directory. One with no password who has never signed
// in (an account the application created ahead) is tied to the sign-in by nothing but
// the name the directory asserts, so the identity is linked only when the directory is
// that of the one tenant the person belongs to, holding a role there and nothing
// anywhere else (hook.OnlyIn), or the sign-in carries the person's own invitation; any
// other sign-in under the name is refused as a stranger is, and says nothing of the
// account. A directory no tenant declares, and a personal
// account, which is no organization's directory (signin.Directory.PersonalAccounts),
// fall to the sign-in method's choice as without them.
//
// The reader also tells the sign-in policy which tenant's directory an account signs
// in through: at each sign-in the library reads the directories of the account's
// identity links and the tenants that declare each (sessioninfo.SignInUser.Directories),
// by which the tenant policy keeps a directory-only tenant's directory people to that
// directory.
//
// Set the two after NewAuth and before serving the first request; a nil reader, a hook
// missing either function, a second call, or a call once the Auth is in use is a wiring
// mistake and panics, as SetSignInPolicy does.
func (a *Auth[SessionData, UserData, Tx]) SetDirectoryAdmission(reader signin.DirectoryReader[Tx], hook DirectorySeatedHook[Tx]) {
	if reader == nil {
		panic("session: SetDirectoryAdmission was given no reader: pass the application's reader of the tenants' directory declarations")
	}
	if hook.Seat == nil || hook.OnlyIn == nil {
		panic("session: SetDirectoryAdmission was given a hook without its two functions: pass the application's hook that seats the person and reads whether a tenant is the only place they hold anything")
	}
	a.methods.mu.Lock()
	defer a.methods.mu.Unlock()

	if a.methods.inUse.Load() {
		panic("session: the directory admission was set after the Auth served a request: set it before serving")
	}
	if a.directories != nil {
		panic("session: the directory admission was set twice: one Auth has one")
	}
	a.directories, a.directorySeated, a.directoryOnlyIn = reader, hook.Seat, hook.OnlyIn
}

// SignUpSeatedHook is the application's half of a sign-up through a method an
// administrator opened: it gives the person the default role the method's row names,
// inside the transaction that makes their account, which it is handed, so the account
// is made and holds the role together, or neither. The user is named by id (the key an
// application's memberships are held under) and by username (what a log line reads);
// method is the name the method is registered under, and role the row's default role,
// as the application wrote it there. The library calls the hook for a person the open
// method alone admitted and made an account for: a person who came with an invitation
// is given the invitation's roles (InvitationAcceptedHook), and one whose account stood
// before signs in as before and is given nothing. It is not called where the row names
// no role, which is what an application whose people get their roles in a tenant
// writes. It is held to what InvitationAcceptedHook is: tx alone for its reads and
// writes, a user the transaction made not readable in it, a committed function (nil
// for none) the library runs once the transaction has committed and before the sign-in
// policy decides, and an error that fails the transaction and ends the sign-up, so no
// account is left holding nothing.
type SignUpSeatedHook[Tx any] = func(ctx context.Context, tx Tx, userID impulse.UUID, username, method, role string) (committed func(ctx context.Context), err error)

// SetSignUpSeated sets the hook that gives a person who signed up through an open
// sign-in method the method's default role. Whether a method is open is no setting of
// the Auth's: an administrator chooses it in the running application, for each method,
// and the choice is a row of the Auth's sign-in methods table (Tables.SignInMethods),
// which each sign-in reads in its own transaction; a method with no row is invite only.
// A row that names a role on an Auth with no hook fails the sign-up, so no account is
// made holding nothing. Set it after NewAuth and before serving the first request; a
// nil hook, a second call, or a call once the Auth is in use is a wiring mistake and
// panics, as SetDirectoryAdmission does.
func (a *Auth[SessionData, UserData, Tx]) SetSignUpSeated(hook SignUpSeatedHook[Tx]) {
	if hook == nil {
		panic("session: SetSignUpSeated was given no hook: pass the application's hook that gives the default role")
	}
	a.methods.mu.Lock()
	defer a.methods.mu.Unlock()

	if a.methods.inUse.Load() {
		panic("session: the sign-up hook was set after the Auth served a request: set it before serving")
	}
	if a.signUpSeated != nil {
		panic("session: the sign-up hook was set twice: one Auth has one")
	}
	a.signUpSeated = hook
}

// TenantNamer names a tenant by its key: what people call it, "" for a tenant it does
// not know. The application's tenant roster (resource.TenantRoster) is one, answering
// from its copy with no read.
type TenantNamer interface {
	Name(tenant accesstypes.Domain) string
}

// SetTenantNames sets what names the tenant an invitation seats a person in. The
// invitation screen is read by a person who is not signed in, who can read no tenant
// record and to whom the tenant's key, an id, says nothing: with names, the opened
// invitation (Invitation().Open()) and the invitation a sign-in methods answer reports
// carry the tenant's name beside its key (tenantName), and the screen says where the
// person is invited. Without them, or for a tenant the namer does not know, the name is
// empty and the screen names no tenant. A signed-in person's own tenants (OwnTenants)
// are named the same way, each by its key where the namer does not know it. Set it after
// NewAuth and before serving the first request; a nil namer, a second call, or a call
// once the Auth is in use is a wiring mistake and panics, as SetDirectoryAdmission does.
func (a *Auth[SessionData, UserData, Tx]) SetTenantNames(names TenantNamer) {
	if names == nil {
		panic("session: SetTenantNames was given nil: pass the application's tenant roster, or set none")
	}
	a.methods.mu.Lock()
	defer a.methods.mu.Unlock()

	if a.methods.inUse.Load() {
		panic("session: the tenant names were set after the Auth served a request: set them before serving")
	}
	if a.tenantNames != nil {
		panic("session: the tenant names were set twice: one Auth has one namer")
	}
	a.tenantNames = names
}

// tenantName is what the invitation screen calls the tenant an invitation seats a
// person in, and the own-tenants list a tenant the person holds something in: its name
// from the namer (SetTenantNames), "" for the application's global surface, for no namer
// and for a tenant the namer does not know.
func (a *Auth[SessionData, UserData, Tx]) tenantName(tenantID string) string {
	if tenantID == "" || a.tenantNames == nil {
		return ""
	}

	return a.tenantNames.Name(accesstypes.Domain(tenantID))
}

// SetXSRFToken sets the XSRF token.
func (a *Auth[S, U, Tx]) SetXSRFToken(next http.Handler) http.Handler {
	return a.serving(a.baseSession.SetXSRFToken(next))
}

// ValidateXSRFToken validates the XSRF token.
func (a *Auth[S, U, Tx]) ValidateXSRFToken(next http.Handler) http.Handler {
	return a.serving(a.baseSession.ValidateXSRFToken(next))
}

// EnforceReadOnlyMask refuses writes from read-only impersonated sessions.
func (a *Auth[S, U, Tx]) EnforceReadOnlyMask(next http.Handler) http.Handler {
	return a.serving(a.baseSession.EnforceReadOnlyMask(next))
}

// EndImpersonation ends the current impersonated session.
func (a *Auth[S, U, Tx]) EndImpersonation() http.HandlerFunc {
	return a.serving(a.baseSession.EndImpersonation())
}

// PendingHandlers are the HTTP handlers for pending identities.
type PendingHandlers struct {
	status              http.HandlerFunc
	confirmWithPassword http.HandlerFunc
	cancel              http.HandlerFunc
	sendCode            http.HandlerFunc
	verifyCode          http.HandlerFunc
	enrollEmail         http.HandlerFunc
}

// Status returns {"reason", "email", "expiresAt", "returnUrl"[, "factors"]} for the
// current pending identity, and for one that waits for MFA and may take the library's
// email code, the code's state under "emailCode": the masked address the code goes to
// ("codeEmail"), whether one is standing ("codeSent", "codeExpiresAt") and whether
// the user must first give an address ("enrollEmail"); see "The second factor" in the
// README. It answers 404 when there is none, and 401 with {"message", "code":
// "pending_expired"} when it has expired.
func (h *PendingHandlers) Status() http.HandlerFunc { return h.status }

// SendCode mails the pending identity's user a one-time code for the second factor,
// to the address the user holds, and answers {"codeEmail", "codeSent",
// "codeExpiresAt"}: the masked address and when the code stops being accepted. A code
// already standing is replaced. It answers 409 when the pending identity does not
// wait for MFA, when the sign-in policy does not accept the email code, or when the
// user holds no address (the page then asks for one and posts it to EnrollEmail());
// an address sent five codes within fifteen minutes and using none is refused with
// too_many_emails and the wait; a code that is used resets the count. Route it
// behind StartSession and ValidateXSRFToken.
func (h *PendingHandlers) SendCode() http.HandlerFunc {
	return h.sendCode
}

// VerifyCode accepts {"code", "rememberDevice"} and completes the pending identity
// when code is the one sent: the session starts under a new ID, recording the
// sign-in's events and an email-code event, and the address the code went to is
// written on the user verified when it was not. With rememberDevice the browser is
// remembered as a trusted device of the user for the days the sign-in settings say
// (AuthAPI.SetRememberedBrowserDays), so its next sign-ins skip the factor; while they
// say 0, it is not remembered. It answers as the password login does:
// {"mfaIsRequired": false}, or the policy's next wait. A wrong code is 401 with
// {"code": "invalid_code"} and the code stands with one fewer attempt; a code entered
// while none stands, because none was sent or the one sent expired, was used, took its
// last attempt or was mailed to an address the account no longer holds, is 401 with
// {"code": "code_expired"}, and the page asks for a new one. Route it as SendCode.
func (h *PendingHandlers) VerifyCode() http.HandlerFunc {
	return h.verifyCode
}

// EnrollEmail accepts {"email"} from a pending identity whose user holds no address,
// and mails that address the code as SendCode does; the address is written on the user,
// verified, as the session the code completes starts. It answers as SendCode does, and
// 409 when the user already holds an address: the code goes there, and an administrator
// changes it. An address another account holds is answered as sent and told so by mail
// to that address, as the add-address step does, so the screen reveals nothing. Route
// it as SendCode.
func (h *PendingHandlers) EnrollEmail() http.HandlerFunc {
	return h.enrollEmail
}

// ConfirmWithPassword accepts {"password"}. On success it links the pending identity to
// its user and starts the session. It answers {"mfaIsRequired": bool}. When the
// sign-in policy requires MFA after the link, the pending identity remains with reason
// "mfa", the PendingHook runs, and the application completes it with
// AuthAPI.CompletePending. A wrong password is 401 and leaves the pending identity.
func (h *PendingHandlers) ConfirmWithPassword() http.HandlerFunc { return h.confirmWithPassword }

// Cancel discards the pending identity.
func (h *PendingHandlers) Cancel() http.HandlerFunc { return h.cancel }

// Pending returns the pending-identity handlers.
func (a *Auth[S, U, Tx]) Pending() *PendingHandlers {
	return &PendingHandlers{
		status:              a.serving(a.pendingStatus()),
		confirmWithPassword: a.serving(a.pendingConfirm()),
		cancel:              a.serving(a.pendingCancel()),
		sendCode:            a.serving(a.pendingSendCode()),
		verifyCode:          a.serving(a.pendingVerifyCode()),
		enrollEmail:         a.serving(a.pendingEnrollEmail()),
	}
}

// API provides programmatic access to the Auth session.
func (a *Auth[S, U, Tx]) API() *AuthAPI[S, U, Tx] { return &AuthAPI[S, U, Tx]{auth: a} }

// AuthAPI provides programmatic access to an Auth session.
type AuthAPI[SessionData, UserData, Tx any] struct {
	auth *Auth[SessionData, UserData, Tx]
}

// Cookie returns the underlying cookie client.
func (p *AuthAPI[S, U, Tx]) Cookie() *cookie.Client { return p.auth.shared().Cookie() }
