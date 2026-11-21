// Package members is the members auth: the people who sign in through the organization's directory (OpenID Connect against Microsoft Entra), the portal's people, who hold their roles in the
// members permission store.
//
// An auth is a package. It owns its session manager, one Auth session with the sign-in
// methods registered on it (here directory), with its own session, user, identity, auth
// event, pending sign-in, sign-in attempt, mail link, sign-in code and trusted device
// tables and cookies; its permission
// store, with its own table prefix, whose engine signals policy changes to the
// application's other instances through the live service; and its role file, embedded
// beside it, so the release's default roles travel with the binary. Each method is its
// own file in this package, named for its registration.
// A site or an outlet binds to an auth by composing its handlers, and two auths on one
// database and one host never collide, because everything an auth names carries its
// name. The same name in this auth and in another is two unrelated principals. The
// package is named for the population, never for how it signs in: the members can gain
// or lose a sign-in method without the package moving.
//
// The auth mails: an administrator invites a person by address, with the roles the
// invitation seats, and the invitee follows the link to accept it; a signed-in person
// adds an address and proves it by following the link sent to it.
// An address an administrator sets for a person is theirs once its mailbox confirms it,
// by the link the auth mails there.
// The links open the login page of the surface that binds to this auth
// (Settings.MailLoginURL), through the sender the data level hands in (Settings.Mail).
//
// Every session carries a second factor. A sign-in the password admitted, or a directory
// the deployment does not administer, waits for the sign-in code the auth mails to the
// person's verified address (a person who holds none gives one first), unless the
// browser is one the person asked to be remembered after a code (the members-trusted
// cookie); a sign-in through a directory the deployment administers, or whose token says
// a second factor was passed, carries the directory's and is asked for nothing. Every
// account a person signs in to has a confirmed address, so a person who comes through a
// directory that does not vouch for theirs (Microsoft Entra ID vouches for none)
// confirms one at their first sign-in with a code mailed to it, whatever the directory
// attests. An
// administrator makes the server forget the browsers a person asked to be remembered,
// from the person's account page, so their next sign-in asks again.
package members

import (
	"context"
	_ "embed" // the role file rides in the binary
	"net/http"
	"time"

	cloudspanner "cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/access/spannerstore"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/outlets/pkg/identity/membersidentity"
	"github.com/impulseframework/impulse/identity"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/azure"
	"github.com/impulseframework/impulse/session/mail"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/session/signin"
)

const (
	// Name is the auth's name: the cookie its sessions ride in.
	Name = "members"
	// TablePrefix is the name's PascalCase form, which prefixes every table the auth owns.
	TablePrefix = "Members"
	// Application is the application's name as the mail the auth sends says it: the
	// subject and the first line of an invitation name it.
	Application = "Outlets Portal"

	// XSRFCookie is the cookie the auth issues its XSRF token in. It carries the auth's name
	// so two auths on one host never overwrite each other's token; the browser echoes it in
	// the X-XSRF-TOKEN header, so the web app that binds to this auth names the same cookie.
	XSRFCookie = Name + "-xsrf"
	// TrustedDeviceCookie is the cookie a browser the person asked to be remembered after a
	// sign-in code carries its token in; it carries the auth's name as XSRFCookie does.
	TrustedDeviceCookie = Name + "-trusted"

	// The auth's tables, which schema/migrations creates: the sessions and users of the
	// Auth session (a person is one user whichever way they sign in; an identity the
	// directory verifies links to its user through the identities table), the auth events
	// each session records, the pending sign-ins that wait for a confirmation, the
	// sign-in attempts the password method's attempt limiter counts per sign-in name, the
	// sign-in methods (what an administrator chose for each: open to sign-up, or invite
	// only, which a method with no row is), and the mail links: the invitations, address
	// verifications and sign-ups the auth mails, each a single-use token its follower
	// proves; the tokens: the bearer credentials of the machine outlets whose tokens this
	// auth issues (the application tokens its people issue and the access and refresh
	// tokens of the device grants they approve); the device codes: the device
	// authorizations command-line clients wait on; and the second factor's tables: the
	// sign-in codes mailed to people and the browsers they asked to be remembered. The
	// names are spelled out in full so the tool's session-tables check, which reads the
	// literals, finds their migrations.
	sessionsTable       = "MembersSessions"
	usersTable          = "MembersSessionUsers"
	identitiesTable     = "MembersSessionIdentities"
	eventsTable         = "MembersSessionAuthEvents"
	pendingTable        = "MembersSessionPendingSignIns"
	attemptsTable       = "MembersSessionSignInAttempts"
	methodsTable        = "MembersSessionSignInMethods"
	mailLinksTable      = "MembersSessionMailLinks"
	tokensTable         = "MembersSessionTokens"
	deviceCodesTable    = "MembersSessionDeviceCodes"
	factorCodesTable    = "MembersSessionFactorCodes"
	trustedDevicesTable = "MembersSessionTrustedDevices"
)

// roleFile is the auth's default roles: the role file beside this package, embedded in the
// binary so the roles travel with the release. The file is the complete statement of the
// default roles; the store holds only the custom roles and the memberships written at run
// time, and a sign-in holds what its memberships name. A global role is held in the global
// partition and a domain role in every tenant domain, so nothing provisions the file per
// tenant.
//
//go:embed roles.json
var roleFile access.RoleFile

// Roles returns the auth's role file, which New hands to the permission engine and the
// tests validate and read.
func Roles() access.RoleFile {
	return roleFile
}

// Settings are the auth's settings: the collection the permission engine holds every
// grant to, the live service it signals through, and the environment-derived values, the
// settings of each sign-in method among them.
type Settings struct {
	// Collection is the generated permission collection: the resources, permissions and
	// fields the release declares, which the permission engine holds every grant to. The
	// role file validates against it when the engine opens, a grant written to a custom
	// role is refused unless the collection lists what it names, and a stored grant that
	// names anything else is skipped and reported each time the policy loads. The package
	// that constructs the auth passes it in, the generated router's Collection() or, where
	// the application has several sites, the union of theirs (access.UnionCollection);
	// this package imports no router. Required; New refuses nil, a nil
	// pointer of a collection's type included (access.IsNilCollection).
	Collection access.PermissionCollection
	// Signals is the application's live service: the permission engine signals the policy
	// kind on it after every policy write and follows the kind from every other instance
	// (access.WithChangeSignal), so a role, grant or membership written on one instance
	// reaches every instance's snapshot at once rather than at the engine's next
	// heartbeat. Required, since every application wires a live service; New refuses nil.
	Signals Signals
	// CookieKey signs session cookies: a Base64-encoded string of at least 32 bytes of
	// cryptographically secure random data.
	CookieKey string
	// SessionTimeout is the idle timeout of a browser session.
	SessionTimeout time.Duration
	// Mail sends the auth's mail: the invitation an administrator sends to an address
	// and the link that verifies an address a person adds.
	// The application's one sender, which the data level opens: the Workspace relay
	// (session/mail/workspace) where the deployment names a mailbox, and the log (mail.Log)
	// in development, where nothing is sent and a developer follows the link from the log.
	// Required; New refuses nil.
	Mail mail.Sender
	// MailLoginURL is the absolute address of the login page of the surface that binds
	// to this auth (https://host/login), which every mailed link opens with its token in
	// the query: the application's public address and the surface's login path.
	MailLoginURL string
	// LoginURL is the browser page a refused or waiting sign-in returns to, with the
	// reason as a code in the query (?code=, a sessioninfo.LoginRefusalCode, never text, or
	// ?pending= for a sign-in that waits): the login page of the surface that binds to
	// this auth, which holds the sentence for each code.
	LoginURL string
	// Directory is the directory registration's settings: how the application is known to
	// Microsoft, read from the environment (APP_MEMBERS_DIRECTORY_TENANT_ID, APP_MEMBERS_DIRECTORY_ADMINISTERED_TENANTS, APP_MEMBERS_DIRECTORY_CLIENT_ID, APP_MEMBERS_DIRECTORY_CLIENT_SECRET, APP_MEMBERS_DIRECTORY_REDIRECT_URL).
	Directory DirectorySettings
	// TenantDeclarations reads what a tenant declares about who may enter it, in the
	// sign-in's transaction, which the sign-in rule decides with: the generated reader
	// over the Tenant record and its directories (app.NewTenantDeclarationReader).
	// Required; New refuses nil.
	TenantDeclarations signin.DeclarationReader[*cloudspanner.ReadWriteTransaction]
	// TenantDirectories reads every tenant's declaration of one directory with the
	// default seat its row carries, in the sign-in's transaction, which the directory
	// admission decides a stranger's first sign-in with: the generated reader
	// (app.NewTenantDirectoryReader). Required; New refuses nil.
	TenantDirectories signin.DirectoryReader[*cloudspanner.ReadWriteTransaction]
}

// Signals is the channel the auth's permission engine signals a policy change on and
// follows policy changes from: the application's live service, which carries one signals
// document per application with a field per kind of change (resource.SignalKind; the
// engine's kind is resource.KindPolicy).
type Signals interface {
	resource.Signaler
	resource.SignalSubscriber
}

// Session is the auth's session manager: one Auth session over the Spanner store, whose
// hooks are typed to the store's transaction.
type Session = session.Auth[session.NoCustomData, session.NoCustomData, *cloudspanner.ReadWriteTransaction]

// Auth is the members auth: its permission store and its session manager with the
// sign-in methods registered on it.
type Auth struct {
	access   *access.Client
	handlers *Handlers
	// identity is the auth's identity client: the generated management of its people
	// (pkg/identity), built over the engine and the session manager above.
	identity *membersidentity.Client
}

// Handlers is the auth's served surface, the shape the generated router names for the
// outlet bound to it: the Auth's middleware and handlers, what the login page is told,
// the pending sign-in handlers, each method's handlers under its registration name
// (in the method's own file).
type Handlers struct {
	*Session
	directory *azure.Method
}

// PendingStatus reports the sign-in that waits for the second factor, if any.
func (h *Handlers) PendingStatus() http.HandlerFunc {
	return h.Pending().Status()
}

// PendingCancel discards a waiting sign-in.
func (h *Handlers) PendingCancel() http.HandlerFunc {
	return h.Pending().Cancel()
}

// PendingSendCode mails the sign-in code to the address of the person whose sign-in
// waits for the second factor, and again when they ask for another.
func (h *Handlers) PendingSendCode() http.HandlerFunc {
	return h.Pending().SendCode()
}

// PendingVerifyCode completes a waiting sign-in with the code the person was mailed,
// remembering the browser when they ask.
func (h *Handlers) PendingVerifyCode() http.HandlerFunc {
	return h.Pending().VerifyCode()
}

// PendingEnrollEmail takes the address the sign-in code goes to, from a person whose
// sign-in waits for the second factor and who holds no address yet.
func (h *Handlers) PendingEnrollEmail() http.HandlerFunc {
	return h.Pending().EnrollEmail()
}

// InvitationOpen reads the invitation a mailed link carries: where it seats the invitee
// and how they may accept it, for the login page to offer.
func (h *Handlers) InvitationOpen() http.HandlerFunc {
	return h.Invitation().Open()
}

// InvitationAccept accepts an invitation as the signed-in person, or as a person who
// signs in through a registration the invitation admits: the roles it names are seated
// once the link is consumed.
func (h *Handlers) InvitationAccept() http.HandlerFunc {
	return h.Invitation().Accept()
}

// EmailAdd starts an address change for the signed-in person: a verification link is
// mailed to the address, and nothing is stored until it is followed.
func (h *Handlers) EmailAdd() http.HandlerFunc {
	return h.Email().Add()
}

// EmailVerify completes an address change from its link: the address is stored verified.
func (h *Handlers) EmailVerify() http.HandlerFunc {
	return h.Email().Verify()
}

// EmailConfirm confirms an address an administrator set for a person, from the link
// mailed to the new address: the account holds the address it had until then. The
// reader of the link confirms it signed in or not, since they may have lost the mailbox
// their sign-in code goes to.
func (h *Handlers) EmailConfirm() http.HandlerFunc {
	return h.Email().Confirm()
}

// DeviceAuthorize starts a command-line client's device authorization ({"client_id",
// "outlet"}, form-encoded or JSON): the client is told the code the person types and
// the device code it polls with. The generated router mounts it under the machine
// outlet whose tokens this auth issues, in front of the token check.
func (h *Handlers) DeviceAuthorize() http.HandlerFunc {
	return h.Device().Authorize()
}

// DeviceToken answers the client's polls (grant_type device_code) with its access and
// refresh tokens once a person approved, and exchanges a refresh token for the next
// pair (grant_type refresh_token); a rotated refresh token presented again revokes the
// grant's family. Mounted beside DeviceAuthorize.
func (h *Handlers) DeviceToken() http.HandlerFunc {
	return h.Device().Token()
}

// DeviceLookup answers the approval page which client asks, for which outlet, for the
// code the person typed (?userCode=).
func (h *Handlers) DeviceLookup() http.HandlerFunc {
	return h.Device().Lookup()
}

// DeviceApprove approves the device authorization the signed-in person's code names
// ({"userCode"}), as them: the client's next poll receives tokens acting as them.
func (h *Handlers) DeviceApprove() http.HandlerFunc {
	return h.Device().Approve()
}

// DeviceDeny denies the device authorization the code names ({"userCode"}).
func (h *Handlers) DeviceDeny() http.HandlerFunc {
	return h.Device().Deny()
}

// TokensList lists the signed-in person's live tokens: the application tokens they
// issued and the tokens of the device grants they approved, never a secret.
func (h *Handlers) TokensList() http.HandlerFunc {
	return h.ApplicationTokens().List()
}

// TokensCreate issues an application token for a program of the signed-in person's own
// ({"name", "outlet", "expiresIn"}), the secret shown this once.
func (h *Handlers) TokensCreate() http.HandlerFunc {
	return h.ApplicationTokens().Create()
}

// TokensRevoke revokes one of the signed-in person's tokens ({"id"}), a device grant's
// with its family.
func (h *Handlers) TokensRevoke() http.HandlerFunc {
	return h.ApplicationTokens().Revoke()
}

// New opens the auth's permission store and session manager over the database and
// registers the sign-in methods. The permission engine parses and validates the role file
// against the collection before it opens, so a release whose default roles are wrong does
// not start, blocks until its first policy snapshot is loaded, and signals its policy
// writes through the live service (access.WithChangeSignal over the policy kind), which it
// refuses to open without. Beside the role file's roles the engine defines one of the
// framework's in every auth, Super Administrator (accesstypes.SuperAdministrator), held
// for the whole application alone: its holder gives and takes any role of the auth, enters
// every tenant, and signs in with a second factor whatever the sign-in rule asks.
func New(ctx context.Context, db *cloudspanner.Client, settings *Settings) (*Auth, error) {
	if access.IsNilCollection(settings.Collection) {
		return nil, errors.New("members.Settings.Collection is nil: the permission engine holds every grant to the permission collection the release declares; pass the generated router's Collection(), or where the application has several sites the union of theirs (the configuration's Collection(), over access.UnionCollection)")
	}
	if settings.Signals == nil {
		return nil, errors.New("members.Settings.Signals is nil: the permission engine signals policy changes through the application's live service, which every application wires")
	}
	if settings.Mail == nil {
		return nil, errors.New("members.Settings.Mail is nil: the auth mails its invitations and address verifications through the application's sender (the Workspace relay deployed, the log in development), which the data level opens")
	}
	if settings.TenantDeclarations == nil {
		return nil, errors.New("members.Settings.TenantDeclarations is nil: the sign-in rule reads what each tenant declares about who may enter it through the generated reader (app.NewTenantDeclarationReader), which the data level hands in")
	}
	if settings.TenantDirectories == nil {
		return nil, errors.New("members.Settings.TenantDirectories is nil: the directory admission reads what the tenants declare about one directory, its default seat included, through the generated reader (app.NewTenantDirectoryReader), which the data level hands in")
	}
	store, err := spannerstore.New(db, spannerstore.WithPrefix(TablePrefix))
	if err != nil {
		return nil, errors.Wrap(err, "spannerstore.New()")
	}
	_, tenantAdministrators := AdministratorRoles()
	accessClient, err := access.New(store, settings.Collection,
		access.WithDefaultRoles(Roles()),
		access.WithChangeSignal(access.ChangeSignalFunc(announcePolicy(settings.Signals), watchPolicy(settings.Signals))),
		// What no role file takes from a super administrator: the methods that give and
		// take the auth's roles, as the generated management names them.
		access.WithSuperAdministratorMethods(membersidentity.SuperAdministratorMethods()...),
		// Who administers a tenant: a person holding one of these roles in the tenant
		// itself, by a membership of their own or through the tenant's own group. A
		// tenant that admits its people through its directory only still admits them,
		// with a password and a second factor, so its administrators have a way in when
		// its directory has a problem; the sign-in rule and each request ask the engine.
		access.WithTenantAdministratorRoles(tenantAdministrators...),
	)
	if err != nil {
		return nil, errors.Wrap(err, "access.New()")
	}
	if err := accessClient.WaitReady(ctx); err != nil {
		return nil, errors.Wrap(err, "access.Client.WaitReady()")
	}

	events, err := sessionstorage.NewAuthEventsTable(eventsTable)
	if err != nil {
		return nil, errors.Wrap(err, "sessionstorage.NewAuthEventsTable()")
	}

	// One Auth session every method establishes: one cookie, one session table, one
	// middleware chain. The methods are registered on it below, before the first request.
	// The Auth is built knowing who holds the Super Administrator role, which is why the
	// permission engine opens first: a holder signs in with a second factor whatever the
	// sign-in rule set below asks of anyone else, held for the mailed code by a rule of
	// the library's own after the application's. The memberships are read outside the
	// sign-in's transaction (resource.SignInRoles).
	authSession, err := session.NewAuth[session.NoCustomData, session.NoCustomData](
		sessionstorage.NewSpannerAuth(db, sessionstorage.WithAuthEvents(events)),
		settings.CookieKey,
		session.SuperAdministratorsBy(signin.SuperAdministrators(resource.SignInRoles(accessClient.UserManager()))),
		session.MailSettings{Sender: settings.Mail, Application: Application, LoginURL: settings.MailLoginURL},
		session.WithSessionTableName(sessionsTable),
		session.WithUserTableName(usersTable),
		session.WithIdentitiesTableName(identitiesTable),
		session.WithPendingSignInsTableName(pendingTable),
		session.WithSignInAttemptsTableName(attemptsTable),
		session.WithSignInMethodsTableName(methodsTable),
		session.WithMailLinksTableName(mailLinksTable),
		session.WithTokensTableName(tokensTable),
		session.WithDeviceCodesTableName(deviceCodesTable),
		session.WithFactorCodesTableName(factorCodesTable),
		session.WithTrustedDevicesTableName(trustedDevicesTable),
		session.WithTrustedDeviceCookieName(TrustedDeviceCookie),
		session.WithCookieName(Name),
		session.WithXSRFCookieName(XSRFCookie),
		session.WithSessionTimeout(settings.SessionTimeout),
	)
	if err != nil {
		return nil, errors.Wrap(err, "session.NewAuth()")
	}

	handlers := &Handlers{Session: authSession}
	handlers.directory, err = registerDirectory(authSession, accessClient, settings)
	if err != nil {
		return nil, err
	}

	// The sign-in rule: the library's tenant policy. A person's tenants are where they
	// hold a role (the engine's memberships), and what each tenant admits is its
	// declaration, read in the sign-in's transaction. A declaration restricts its own
	// tenant alone: a tenant that is not directory only admits everyone it seats,
	// however they signed in, and a directory-only tenant admits a sign-in through its
	// own directory (its method and the connection its identities carry) and nothing
	// else, so a person is refused only when every tenant that seats them is closed to
	// the way they signed in; a person who holds a role outside any one
	// tenant (a global role, or one held in every tenant) is admitted on the library's
	// checks, and a super administrator before any of it. A directory-only tenant's own
	// administrator who signs in with a password is not refused either: the sign-in is
	// held for the mailed code, and the session then enters that tenant
	// (access.WithTenantAdministratorRoles names the roles).
	// A person who holds no tenant is refused (no_tenant): nobody signs up to this auth
	// and creates a Tenant of their own, since another auth's outlets serve the
	// record or a directory gives this one's roles.
	// Which tenants the session then reaches is decided again, tenant by tenant, where
	// its tenant list is computed (UserPermissions): a super administrator's session
	// reaches every tenant, a tenant's own administrator's reaches theirs with a
	// password and a second factor, and nobody else's reaches one whose declaration
	// their sign-in does not meet. The memberships are read outside the sign-in's transaction
	// (resource.SignInRoles): the Spanner client refuses the engine's read under the
	// transaction's context as a nested transaction. A sign-in the rule admits then
	// carries a second factor (requireSecondFactor).
	authSession.SetSignInPolicy(signin.TenantPolicy(resource.SignInRoles(accessClient.UserManager()), settings.TenantDeclarations).Then(requireSecondFactor))

	// The auth's identity client (pkg/identity, generated): the rules that manage the
	// auth's people, over the engine that reads its memberships and the API of the session
	// manager that signs them in, which makes the link an invitation mails.
	// The client knows the roles that count as a tenant's administrator's
	// (identity.WithAdministratorRoles, the ones held in every tenant), so a person who
	// removes a role from themselves in a tenant is judged by the rule on the tenant's
	// last administrator. The application's last administrator is its last super
	// administrator, kept by the client's own rule.
	// It knows where the first administrator's roles are held, too
	// (identity.WithFirstAdministratorRoles over FirstAdministratorRoles): that one
	// invitation is sent for the whole application and gives each role where the
	// foothold declares it, for the whole application or in every tenant.
	identityClient, err := membersidentity.NewClient(accessClient, authSession.API(), identity.WithFirstAdministratorRoles(FirstAdministratorRoles()), identity.WithAdministratorRoles(tenantAdministrators))
	if err != nil {
		return nil, errors.Wrap(err, "membersidentity.NewClient()")
	}

	// A role, a place in a group and a group's role may each be given for a period, and
	// count inside it alone. A row whose period has ended goes when its person next signs
	// in: the session library calls the identity client's SignedIn once the session is
	// committed, apart from the sign-in, so a failure is logged and the session stands.
	// It removes the person's ended roles and places and nothing else, never the
	// account, and no job runs on a schedule.
	authSession.SetSignedInHook(identityClient.SignedIn(resource.NewSpannerClient(db)))

	// The directory admission: what a person gets at their first sign-in through a
	// tenant's directory, before the sign-in method's own choice is reached. The row of
	// the tenant whose directory the identity came through decides. A row with a default
	// seat creates a stranger and gives them that role in the tenant in one transaction
	// (the identity client's DirectorySeated, which the session library runs inside the
	// transaction that links the account to the directory), and the sign-in rule then
	// finds the membership; a row with none refuses a stranger (invitation_only) and no
	// account is created, unless they followed an invitation link, which is the way in.
	// A person whose account stood before is given the default seat too, at the sign-in
	// that links the account to the directory, where they hold no role in the tenant; a
	// role an administrator removes afterwards is not given back by signing in. A
	// directory no tenant has falls to the method, which is invite only: an account
	// stands before the sign-in, or an invitation comes with it. The auth's roles are the
	// application's, so the admission is set here; an auth whose directory writes the
	// roles sets none.
	authSession.SetDirectoryAdmission(settings.TenantDirectories, identityClient.DirectorySeated())

	// An accepted invitation gives its roles, in the tenant it names or for the whole
	// application, in the transaction that consumes the invitation (the identity
	// client's InvitationAccepted, which the session library runs inside it): the
	// invitation is spent and its roles given together or neither is, and the sign-in
	// rule that follows finds the memberships.
	authSession.SetInvitationAccepted(identityClient.InvitationAccepted())

	// A person who signs up through a sign-in method an administrator opened is given
	// the method's default role in the transaction that makes their account (the
	// identity client's SignedUp, which the session library runs inside it), so no
	// account is left holding nothing.
	// No method of this auth is opened to sign-up here: nobody who signed up to it could
	// be given a Tenant, so the hook has nobody to seat.
	authSession.SetSignUpSeated(identityClient.SignedUp())

	return &Auth{access: accessClient, handlers: handlers, identity: identityClient}, nil
}

// Access returns the auth's permission engine: the handlers check against it, and its
// UserManager writes custom roles, their grants, and role memberships.
func (a *Auth) Access() *access.Client {
	return a.access
}

// Session returns the auth's session manager.
func (a *Auth) Session() *Session {
	return a.handlers.Session
}

// Handlers returns the auth's served surface.
func (a *Auth) Handlers() *Handlers {
	return a.handlers
}

// Identity returns the auth's identity client: what the generated methods that manage
// the auth's people run over (pkg/identity/membersidentity), on their routes and
// in a transaction the application opens itself. The roles a sign-in gives are written
// through the same client, by the hooks New sets.
func (a *Auth) Identity() *membersidentity.Client {
	return a.identity
}

// Close waits for the mail the auth is still sending, then releases the permission
// engine, which ends its subscription to the policy kind. A reset link a person asked
// for is mailed after their request is answered (session.AuthAPI.WaitForMail), so an
// application that has stopped serving closes the auth before it closes the database,
// and no link a person was promised is lost.
func (a *Auth) Close() error {
	a.handlers.API().WaitForMail()
	if err := a.access.Close(); err != nil {
		return errors.Wrap(err, "access.Client.Close()")
	}

	return nil
}

// requireSecondFactor is the sign-in rule every session ends with: the second factor, the
// sign-in code mailed to the person's address (sessioninfo.MethodEmailCode), unless the
// sign-in carries one already, a remembered browser's token or the attestation of a
// directory the deployment administers or whose token says a second factor was passed,
// which the library takes before it asks. A sign-in the rule holds waits for the code
// (?pending=mfa on the login page), where the person asks for it, enters it and may ask
// for the browser to be remembered.
func requireSecondFactor(context.Context, *cloudspanner.ReadWriteTransaction, *sessioninfo.NewSessionRequest) (*signin.Decision, error) {
	return signin.RequireMFA(sessioninfo.MethodEmailCode), nil
}

// announcePolicy is the sending half of the engine's change signal: after a policy write
// (a role, a grant, a membership) the engine signals the policy kind on the live service,
// and every instance subscribed to the kind rereads its snapshot. A failure is the
// engine's to log and never fails the write, since every instance also rereads at its
// heartbeat.
func announcePolicy(signals Signals) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		if err := signals.Signal(ctx, resource.KindPolicy); err != nil {
			return errors.Wrap(err, "resource.Signaler.Signal()")
		}

		return nil
	}
}

// watchPolicy is the receiving half: it subscribes to the policy kind, runs onChange on
// every signal of it until ctx ends, and ends the subscription on return.
func watchPolicy(signals Signals) func(ctx context.Context, onChange func()) error {
	return func(ctx context.Context, onChange func()) error {
		stop, err := signals.Subscribe(resource.KindPolicy, onChange)
		if err != nil {
			return errors.Wrap(err, "resource.SignalSubscriber.Subscribe()")
		}
		defer stop()
		<-ctx.Done()

		return nil
	}
}
