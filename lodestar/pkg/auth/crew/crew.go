// Package crew is the crew auth: the people who sign in to the console, with a password
// or through the fleet's Google directory, and hold their roles in the crew permission
// store.
//
// An auth is a package. It owns its session manager, one Auth session with the sign-in
// methods registered on it, with its own session, user, identity, auth event and pending
// sign-in tables and cookie; its permission store, with its own table prefix; and its
// role configuration. A site or an outlet binds to an auth by composing its handlers, and
// two auths on one database and one host never collide, because everything an auth names
// carries its name. The package is named for the population, never for the method: the
// crew gained a directory sign-in beside the password without the package moving.
//
// Three ways in, one population. The password method checks a persona's password, and the
// personas' roles are the bootstrap's (cmd/bootstrap/users.json); it is registered with
// open sign-up, so a stranger with an address may create a crew login with a password
// from a mailed link, holding no role, as the Google sign-up registration admits one with
// a Google account. The Google method
// provisions whoever the directory verifies under the verified email, holding no role
// until an operator seats them: the crew's roles are the application's on every method
// (google.DisableRoleSync), since a registration whose directory writes the roles stands
// alone on its auth, and the crew auth has three ways in. The two methods meet in one
// users table, one session table and one cookie, so a session opened either way is a
// crew session. In development the directory is simulated and APP_GOOGLE_USERNAME in
// .envrc says who the Google sign-in is. The sign-up registration is the third way, for a
// person on no manifest and in no directory: any Google account, open sign-up, so the
// first sign-in makes a crew login holding no role and no sector. Such a person's one way
// to a sector is to chart their own, the Sector record's self-service creation shape; in
// development APP_SIGN_UP_USERNAME says who the simulated sign-up is. The directory as
// the authority for roles is the members auth's shape (pkg/auth/members), where the
// directory's registration stands alone.
//
// Invitations are the way an administrator brings a person in: the auth's generated
// management mails a link carrying the roles, into the whole service or into a sector,
// and whoever follows it and signs in, by any method or by creating a password login,
// is given those roles by the acceptance hook, in the transaction that consumes the
// invitation (New sets it from the auth's identity client). A person's
// email address is held apart from their username, added and verified by the person
// through a mailed link, or entered unverified by an operator.
//
// Every crew session carries a second factor, whichever way the person signed in
// (requireSecondFactor, the sign-in rule's last word): the fleet's
// directory attests for its people when the deployment says the fleet administers it,
// a browser a person asked to be remembered stands in for the factor, and otherwise the
// login page runs the sign-in code the auth mails. Headquarters makes the server forget
// a person's remembered browsers from the person's account
// (RevokeCrewUserTrustedDevices).
//
// A person who forgot their password gets back in by a link mailed to the address they
// confirmed (session.Auth.PasswordReset, whose three handlers the generated router
// mounts): Headquarters or a sector's marshal sends one from the person's row, and the
// person asks for one at the sign-in page while Headquarters allows it
// (SetCrewSelfServiceReset). An address Headquarters sets for a person is theirs once
// its mailbox confirms it (session.EmailHandlers.Confirm).
//
// Demonstrates: auth.password, auth.two-populations, auth.multi-method, auth.directory-roles, auth.open-sign-up, auth.invitation, auth.email-address, auth.password-sign-up, auth.password-reset, auth.second-factor, auth.trusted-device.
package crew

import (
	"context"
	"crypto/sha256"
	_ "embed" // the role file rides in the binary
	"net/http"
	"time"

	cloudspanner "cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/access/spannerstore"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/identity"
	"github.com/impulseframework/impulse/lodestar/pkg/auth"
	"github.com/impulseframework/impulse/lodestar/pkg/identity/crewidentity"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/google"
	"github.com/impulseframework/impulse/session/oidc"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/session/signin"
)

const (
	// Name is the auth's name. It is the name of this package's directory, the name
	// generation knows the auth by, the name a @subjectSet says in its auth: part,
	// the name the role file is handed over under (access.WithDefaultRoles(Roles(),
	// Name)), and the cookie its sessions ride in. It is not a setting: an engine
	// handed a name generation does not know refuses to open, so renaming the auth
	// renames the directory and this constant together.
	Name = "crew"
	// TablePrefix is the name's PascalCase form, which prefixes every table the auth owns.
	TablePrefix = "Crew"

	// XSRFCookie is the cookie the auth issues its XSRF token in. It carries the auth's name
	// so two auths on one host never overwrite each other's token; the browser echoes it in
	// the X-XSRF-TOKEN header, so the web app that binds to this auth names the same cookie.
	XSRFCookie = Name + "-xsrf"
	// TrustedDeviceCookie is the cookie a browser a crew member asked to be remembered
	// after the sign-in code holds its token in, for the trusted-device period; it
	// carries the auth's name for the same reason, since the members' cookie lives on
	// the same host.
	TrustedDeviceCookie = Name + "-trusted"

	// GoogleRegistration is the name the Google method is registered under on the Auth:
	// the segment of its login and callback routes (user/google/login) and, in
	// development, the registration whose simulated person APP_GOOGLE_USERNAME names.
	GoogleRegistration = "google"
	// SignUpRegistration is the name the open sign-up method is registered under: any
	// Google account, through user/sign-up/login, and in development the registration
	// whose simulated person APP_SIGN_UP_USERNAME names.
	SignUpRegistration = "sign-up"
	// PersonaRegistration is the name the persona method is registered under: the sign-in
	// of the test environments through the persona provider (user/persona/login), where a
	// reviewer, a tester or an agent picks a persona of the cast (cast.json) and is signed
	// in as it with its roles; the connection every persona identity's link carries; and
	// in development the registration whose simulated person APP_PERSONA_USERNAME names.
	PersonaRegistration = "persona"

	// The auth's tables, which schema/migrations creates: the sessions and users of the
	// Auth session, the identity links of its Google sign-ins, the auth events each
	// session records, the pending sign-ins that wait for a confirmation or an MFA step,
	// and the impersonation record the library joins into every session read, so a
	// view-as or act-as-role session carries who established it, the sign-in attempts
	// the password method's attempt limiter counts per sign-in name, the mail links
	// (the invitations, verifications and sign-ups the auth mails, each a single-use
	// token), and the second factor's: the sign-in codes mailed for a pending sign-in
	// and the browsers people asked to be remembered. The names are spelled out in full
	// (not TablePrefix + "SessionUsers") so the tool's session-tables check, which
	// reads the literals, finds their migrations.
	sessionsTable       = "CrewSessions"
	usersTable          = "CrewSessionUsers"
	identitiesTable     = "CrewSessionIdentities"
	eventsTable         = "CrewSessionAuthEvents"
	pendingTable        = "CrewSessionPendingSignIns"
	attemptsTable       = "CrewSessionSignInAttempts"
	methodsTable        = "CrewSessionSignInMethods"
	mailLinksTable      = "CrewSessionMailLinks"
	factorCodesTable    = "CrewSessionFactorCodes"
	trustedDevicesTable = "CrewSessionTrustedDevices"
	// The bearer tokens the auth issues for its machine outlet and the device
	// authorizations command-line clients start, which the token page and the device
	// flow keep.
	tokensTable      = "CrewSessionTokens"
	deviceCodesTable = "CrewSessionDeviceCodes"
	// The records of the sessions held as someone else, which the watch desk lists
	// through the auth's generated resource.
	impersonationsTable = "CrewSessionImpersonations"

	// DroidsOutlet is the machine outlet the auth issues tokens for: the droids'
	// telemetry channel (/droids), which the generator program declares Tokens of this
	// package, so the generated router composes the auth's token check in front of the
	// outlet's routes, mounts the device flow's endpoints under it, and serves the token
	// routes on the console. A request there carries a bearer token the auth issued: the
	// droid service account's configured key, seeded as its application token
	// (SeedToken), or a token a crew member issued on the console, or one a command-line
	// client obtained with a crew member's approval. New registers the outlet on the
	// Auth before the first request, as it registers the sign-in methods.
	DroidsOutlet = "droids"
	// seededTokenName names the application token SeedToken keeps for a service
	// account's configured key.
	seededTokenName = "configured key"
	// seededTokenLifetime is how long a seeded token lives: the configured key has no
	// expiry of its own, so the token takes a century and is replaced when the key is.
	seededTokenLifetime = 100

	// Application is the fleet's name as the mail the auth sends says it.
	Application = "Lodestar"

	// impersonationTimeout is the configured hard cap on an impersonated session; the
	// mint route shortens every view-as below it with MaxDuration.
	impersonationTimeout = 4 * time.Hour
)

// AdministratorRoles are the roles whose holders administer, by where they are held:
// Headquarters in the whole service, SectorMarshal in a sector (the governor holds it
// in every sector). The auth's generated management judges a person who takes a
// sector's role from themselves by the sector's (identity.WithAdministratorRoles, the
// last-owner rule): a marshal leaves a sector once another marshal holds it, and the
// last one is refused; and the first person into a sector is given the sector's, the
// person invited to found it at their first sign-in and the pioneer on the
// create-sector route (app.SectorAdministratorRoles). The whole service is kept by the
// role the framework defines in every auth instead, Super Administrator
// (accesstypes.SuperAdministrator), which the governor holds: its last holder keeps the
// role and the account, whoever asks.
//
// Demonstrates: tenancy.creator-seat.
func AdministratorRoles() (global, everyDomain []accesstypes.Role) {
	return []accesstypes.Role{"Headquarters"}, []accesstypes.Role{"SectorMarshal"}
}

// sectorAdministratorRoles are the roles that administer a sector, the ones
// AdministratorRoles names as held in every sector: what the identity client judges a
// person who takes a sector's role from themselves by, and what it gives the person
// whose first sign-in makes a sector from an invitation.
func sectorAdministratorRoles() []accesstypes.Role {
	_, everyDomain := AdministratorRoles()

	return everyDomain
}

// rolesFile is the auth's default roles: the release's own policy for the crew, which
// travels with the binary and is handed to the permission engine at New. The store holds
// no row for these roles; a login holds one by name alone, and the file is the complete
// statement of what each role may do. A global role is held in the global partition and a
// domain role in every sector.
//
//go:embed roles.json
var rolesFile []byte

// Roles returns the auth's role file.
func Roles() access.RoleFile {
	return access.RoleFile(rolesFile)
}

// Settings are the auth's environment-derived settings.
type Settings struct {
	// CookieKey signs session cookies: a Base64-encoded string of at least 32 bytes of
	// cryptographically secure random data.
	CookieKey string
	// SessionTimeout is the idle timeout of a browser session.
	SessionTimeout time.Duration
	// LoginURL is the browser page a refused or waiting Google sign-in returns to, with
	// the reason as a code in the query (?code=, a sessioninfo.LoginRefusalCode, never
	// text) or the pending step (?pending=): the console's login page, which holds the
	// sentence for each code.
	LoginURL string
	// Directory identifies the application to the fleet's Google directory, the crew's
	// second way in.
	Directory auth.Directory
	// SignUpRedirectURL is the browser-facing callback of the sign-up registration, the
	// crew's third way in: any Google account, under the same client as the directory,
	// returned to its own callback (user/sign-up/callback).
	SignUpRedirectURL string
	// Persona identifies the application to the persona provider, the crew's way in for
	// the test environments: set in a pull-request environment and the first environment,
	// where the method is registered, and empty everywhere else, where it is not.
	Persona auth.Persona
	// Mail is how the auth sends its invitations, address verifications and sign-up
	// links: the configuration's sender, and the console's login page as an absolute
	// URL, where every link leads. Required; New refuses a nil sender.
	Mail auth.Mail
	// DevicePage is the console page where a signed-in crew member approves a
	// command-line client's request to act as them, which a device authorization tells
	// the client (verification_uri): a path in the application, the console's mount
	// path joined with /device, where the console mounts the library's device approval
	// page.
	DevicePage string
	// Collection is the generated permission collection: the resources, permissions and
	// fields the release declares, which the permission engine holds every grant to. The
	// role file validates against it when the engine opens, a grant written to a custom
	// role is refused unless the collection lists what it names, and a stored grant that
	// names anything else is skipped and reported each time the policy loads. The router
	// package generates it (router.Collection()) and imports this one, so the
	// configuration passes it in. Required; New refuses nil, a nil
	// pointer of a collection's type included (access.IsNilCollection).
	Collection access.PermissionCollection
	// ChangeSignal carries the engine's policy-change hints between the application's
	// instances (auth.PolicySignal over the live service): the engine announces after
	// every policy write it makes and rereads on every hint it receives, the heartbeat
	// left as the backstop. Required.
	ChangeSignal access.ChangeSignal
	// TenantDeclarations reads what a sector declares about who may enter it, in the
	// sign-in's transaction: the generated reader over the Sector record and its
	// directories (app.NewSectorDeclarationReader), which the sign-in rule decides with.
	// Required; New refuses nil.
	TenantDeclarations signin.DeclarationReader[*cloudspanner.ReadWriteTransaction]
	// TenantDirectories reads every sector's declaration of one directory with the
	// default seat its row carries, in the sign-in's transaction: the generated reader
	// (app.NewSectorDirectoryReader), which the directory admission decides a stranger's
	// first sign-in with. Required; New refuses nil.
	TenantDirectories signin.DirectoryReader[*cloudspanner.ReadWriteTransaction]
	// TenantMaker makes a sector from an accepted invitation: the generated maker over
	// the Sector record's create patch (app.NewSectorMaker), which the crew's identity
	// client checks an invitation's details with when Headquarters sends one and makes
	// the sector with at the invited person's first sign-in. Required; New refuses nil.
	TenantMaker identity.TenantMaker
}

// required refuses settings that lack what the auth is not built without, each refusal
// saying what the missing piece is for.
func (s *Settings) required() error {
	switch {
	case access.IsNilCollection(s.Collection):
		return errors.New("crew.Settings.Collection is required: the permission engine holds every grant to the permission collection the release declares; pass the generated router's (router.Collection())")
	case s.ChangeSignal == nil:
		return errors.New("crew.Settings.ChangeSignal is required: the engine announces and watches policy changes through it")
	case s.TenantDeclarations == nil:
		return errors.New("crew.Settings.TenantDeclarations is required: the sign-in rule reads what each sector declares about who may enter it through the generated reader (app.NewSectorDeclarationReader)")
	case s.TenantDirectories == nil:
		return errors.New("crew.Settings.TenantDirectories is required: the directory admission reads what each sector declares about one directory, its default seat included, through the generated reader (app.NewSectorDirectoryReader)")
	case s.TenantMaker == nil:
		return errors.New("crew.Settings.TenantMaker is required: a sector is made by an invitation's first sign-in, through the generated maker over the Sector record (app.NewSectorMaker)")
	case s.Mail.Sender == nil:
		return errors.New("crew.Settings.Mail.Sender is required: the auth mails its invitations, address verifications and sign-up links through it (the configuration's driver, or mail.Log in development)")
	}

	return nil
}

// Session is the auth's session manager: one Auth session over the Spanner store, whose
// hooks are typed to the store's transaction.
type Session = session.Auth[session.NoCustomData, session.NoCustomData, *cloudspanner.ReadWriteTransaction]

// Auth is the crew auth: its permission store and its session manager with the sign-in
// methods registered on it.
type Auth struct {
	access   *access.Client
	handlers *Handlers
	// identity is the auth's identity client: the generated management of the crew
	// (pkg/identity), built over the engine and the session manager above.
	identity *crewidentity.Client
}

// Handlers is the auth's served surface, the shape the generated router names for the
// console outlet (router.DefaultSession): the Auth's middleware and handlers, what the
// login page is told, the pending sign-in handlers, and the handlers of each sign-in
// method under its registration name. The App embeds it.
type Handlers struct {
	*Session
	store    *sessionstorage.Auth[*cloudspanner.ReadWriteTransaction]
	password *session.PasswordMethod
	google   *google.Method
	signUp   *google.Method
	persona  *oidc.Method
	droids   *session.MachineOutlet
}

// DroidsTokens returns the droids outlet's token check (router.DroidsTokens): the
// machine outlet registered on the Auth under DroidsOutlet, whose ValidateToken binds
// each request on /droids to the account its bearer token acts for.
func (h *Handlers) DroidsTokens() *session.MachineOutlet {
	return h.droids
}

// DeviceAuthorize starts a command-line client's device authorization ({"client_id",
// "outlet"}, form-encoded or JSON): the client is told the code the person types and
// the device code it polls with. The generated router mounts it under the droids
// outlet, in front of the token check.
func (h *Handlers) DeviceAuthorize() http.HandlerFunc {
	return h.Device().Authorize()
}

// DeviceToken answers the client's polls (grant_type device_code) with its access and
// refresh tokens once a crew member approved, and exchanges a refresh token for the
// next pair (grant_type refresh_token); a rotated refresh token presented again revokes
// the grant's family. Mounted beside DeviceAuthorize.
func (h *Handlers) DeviceToken() http.HandlerFunc {
	return h.Device().Token()
}

// DeviceLookup answers the console's approval page which client asks, for which
// outlet, for the code the person typed (?userCode=).
func (h *Handlers) DeviceLookup() http.HandlerFunc {
	return h.Device().Lookup()
}

// DeviceApprove approves the device authorization the signed-in crew member's code
// names ({"userCode"}), as them: the client's next poll receives tokens acting as them.
func (h *Handlers) DeviceApprove() http.HandlerFunc {
	return h.Device().Approve()
}

// DeviceDeny denies the device authorization the code names ({"userCode"}).
func (h *Handlers) DeviceDeny() http.HandlerFunc {
	return h.Device().Deny()
}

// TokensList lists the signed-in crew member's live tokens: the application tokens
// they issued and the tokens of the device grants they approved, never a secret.
func (h *Handlers) TokensList() http.HandlerFunc {
	return h.ApplicationTokens().List()
}

// TokensCreate issues an application token for a program of the signed-in crew
// member's own ({"name", "outlet", "expiresIn"}), the secret shown this once.
func (h *Handlers) TokensCreate() http.HandlerFunc {
	return h.ApplicationTokens().Create()
}

// TokensRevoke revokes one of the signed-in crew member's tokens ({"id"}), a device
// grant's with its family.
func (h *Handlers) TokensRevoke() http.HandlerFunc {
	return h.ApplicationTokens().Revoke()
}

// SeedToken makes secret the application token of the service account username on
// outlet: a machine's configured credential (the droids' APP_DROIDS_API_KEY), kept the
// way a person's own token is, hashed in the store, bound to the outlet, acting as the
// account, so the token check and the permission checks treat the droid like any
// other caller. The account is found by its username: its roles are held by the id of
// the account, as everyone's are, so the account that holds them is the one whoever
// gave them created (the bootstrap creates the droid's, without a password, and gives
// it its roles). An account the auth has never seen is created without a password and
// holds no role until it is given one. A secret the store already holds live is left
// alone, and a changed one replaces the account's seeded token on the outlet, so a
// rotated key takes effect at the next start.
//
// Demonstrates: outlet.tokens, machine-identity.
func (h *Handlers) SeedToken(ctx context.Context, username, outlet, secret string) error {
	hash := sha256.Sum256([]byte(secret))
	_, err := h.store.Token(ctx, hash[:])
	if err == nil {
		return nil
	}
	if !httpio.HasNotFound(err) {
		return errors.Wrap(err, "sessionstorage.Auth.Token()")
	}

	api := h.API()
	var userID impulse.UUID
	user, err := api.UserByUsername(ctx, username)
	switch {
	case err == nil:
		userID = user.ID
	case httpio.HasNotFound(err):
		id, err := api.CreateSessionUser(ctx, &session.CreateUserRequest{Username: username})
		if err != nil {
			return errors.Wrapf(err, "session.AuthAPI.CreateSessionUser(): service account %s", username)
		}
		userID = id
	default:
		return errors.Wrap(err, "session.AuthAPI.UserByUsername()")
	}

	tokens, err := h.store.Tokens(ctx, userID)
	if err != nil {
		return errors.Wrap(err, "sessionstorage.Auth.Tokens()")
	}
	for _, tok := range tokens {
		if tok.Outlet != outlet || tok.Name != seededTokenName {
			continue
		}
		if err := h.store.RevokeUserToken(ctx, userID, tok.ID, sessionstorage.TokenRevokedByHand); err != nil {
			return errors.Wrap(err, "sessionstorage.Auth.RevokeUserToken()")
		}
	}
	id, err := impulse.NewUUID()
	if err != nil {
		return errors.Wrap(err, "impulse.NewUUID()")
	}
	seeded := &sessionstorage.Token{
		ID: id, TokenHash: hash[:], Kind: sessionstorage.TokenApplication, UserID: userID, Outlet: outlet, Name: seededTokenName,
		ExpiresAt: time.Now().AddDate(seededTokenLifetime, 0, 0),
	}
	if err := h.store.InsertToken(ctx, seeded); err != nil {
		return errors.Wrap(err, "sessionstorage.Auth.InsertToken()")
	}

	return nil
}

// PasswordLogin is the password method's login: {"username", "password"} in, the session
// cookie out.
func (h *Handlers) PasswordLogin() http.HandlerFunc {
	return h.password.Login()
}

// PasswordChange is the password method's change: {"oldPassword", "newPassword"} in
// from a signed-in person, who may be one whose operator-set password must change before
// anything else is served. It validates the session itself, so the generated router
// mounts it outside ValidateSession, behind the XSRF guard.
func (h *Handlers) PasswordChange() http.HandlerFunc {
	return h.password.ChangeUserPassword()
}

// PasswordRemove is the password method's removal: {"password"} in from a signed-in
// person who has moved to signing in through the fleet's directory and no longer wants
// the password as a second way in. A person with no directory link keeps it, since
// nothing else would sign them in. It validates the session itself, as the change does,
// so the generated router mounts it beside the change, outside ValidateSession, behind
// the XSRF guard.
func (h *Handlers) PasswordRemove() http.HandlerFunc {
	return h.password.RemoveUserPassword()
}

// FirstPasswordSendCode mails the signed-in person who has no password, because they
// have only ever signed in through the fleet's directory, the code they choose their
// first one with, at the address their login holds confirmed (no body in). It is how a
// sector's marshal who came in through the directory gets the password a directory-only
// sector asks of whoever turns the switch on, and keeps a way in if the directory fails.
// Whoever asks must read that mailbox to finish, so the password is tied to it.
//
// Demonstrates: tenancy.directory-only.
func (h *Handlers) FirstPasswordSendCode() http.HandlerFunc {
	return h.FirstPassword().SendCode()
}

// FirstPasswordSet gives the signed-in person who has no password the one they choose,
// with the code they were mailed ({"code", "password"}). A login that has a password
// changes it with the old one (PasswordChange).
func (h *Handlers) FirstPasswordSet() http.HandlerFunc {
	return h.FirstPassword().Set()
}

// PasswordResetRequest takes a person's own request for a link that resets their
// password ({"email"}), served while an administrator has turned that on: the answer is
// the same whether or not the address has a login, and an address with none is mailed
// nothing.
func (h *Handlers) PasswordResetRequest() http.HandlerFunc {
	return h.PasswordReset().Request()
}

// PasswordResetOpen opens a password reset link ({"token"}), the person's own or one an
// administrator sent, and answers the username it resets the password of.
func (h *Handlers) PasswordResetOpen() http.HandlerFunc {
	return h.PasswordReset().Open()
}

// PasswordResetComplete sets the new password a reset link's reader chose ({"token",
// "password"}): the link is spent, the old password is refused from then on, every
// session of the person ends, and they sign in with the new password.
func (h *Handlers) PasswordResetComplete() http.HandlerFunc {
	return h.PasswordReset().Complete()
}

// GoogleLogin sends the browser to the fleet's Google directory.
func (h *Handlers) GoogleLogin() http.HandlerFunc {
	return h.google.Login()
}

// GoogleCallback receives the browser back from the directory, provisions or finds the
// person, reconciles their roles and starts the session.
func (h *Handlers) GoogleCallback() http.HandlerFunc {
	return h.google.Callback()
}

// SignUpLogin sends the browser to Google to sign up with any Google account: the
// crew's third way in, for a person the manifest and the directory do not know.
func (h *Handlers) SignUpLogin() http.HandlerFunc {
	return h.signUp.Login()
}

// SignUpCallback receives the browser back from Google, makes a crew login of a person
// it has never seen (open sign-up) holding no role, and starts the session.
func (h *Handlers) SignUpCallback() http.HandlerFunc {
	return h.signUp.Callback()
}

// PersonaLogin sends the browser to the persona provider, where the person picks a
// persona of this deployment's cast, or is signed in straight away as the persona the
// login_hint query parameter names (the machine path). Nil where the method is not
// registered: the generated router mounts the route and tells the login page of the
// method only where it is, so the same build offers it in the test environments alone.
//
// Demonstrates: auth.persona.
func (h *Handlers) PersonaLogin() http.HandlerFunc {
	if h.persona == nil {
		return nil
	}

	return h.persona.Login()
}

// PersonaCallback receives the browser back from the persona provider, links the
// persona's identity to the crew login of the same name (the cast's seeded account) and
// starts the session. Nil where the method is not registered.
func (h *Handlers) PersonaCallback() http.HandlerFunc {
	if h.persona == nil {
		return nil
	}

	return h.persona.Callback()
}

// InvitationOpen opens the invitation the login page was sent to ({"token"}): it
// answers the address and the sector the invitation names, and sets the cookie the
// sign-in that follows carries, so whichever method the person signs in with accepts it.
func (h *Handlers) InvitationOpen() http.HandlerFunc {
	return h.Invitation().Open()
}

// InvitationAccept accepts the opened invitation as the signed-in person, who proved
// they read the mailbox; the acceptance hook seats them with the roles it carries.
func (h *Handlers) InvitationAccept() http.HandlerFunc {
	return h.Invitation().Accept()
}

// InvitationSignUp creates the invited person's crew login with a password
// ({"username", "password"}), the invitation's address verified, and signs them in with
// the invitation accepted on the way.
func (h *Handlers) InvitationSignUp() http.HandlerFunc {
	return h.Invitation().SignUp()
}

// EmailAdd mails a verification link to the address the signed-in person gives
// ({"email"}); the address is held unverified until the link is followed.
func (h *Handlers) EmailAdd() http.HandlerFunc {
	return h.Email().Add()
}

// EmailVerify marks the address verified from the mailed link's token ({"token"}).
func (h *Handlers) EmailVerify() http.HandlerFunc {
	return h.Email().Verify()
}

// EmailConfirm confirms an address an administrator set for a person, from the link
// mailed to the new address ({"token"}): the login holds the address it had until then.
// The reader of the link confirms it signed in or not, since they may have lost the
// mailbox their sign-in code goes to.
func (h *Handlers) EmailConfirm() http.HandlerFunc {
	return h.Email().Confirm()
}

// SignUpStart mails a sign-up link to the address a stranger gives ({"email"}): the
// password method's open sign-up, which answers alike whatever the address's state.
func (h *Handlers) SignUpStart() http.HandlerFunc {
	return h.SignUp().Start()
}

// SignUpOpen opens the sign-up link ({"token"}) and answers the address it was sent to.
func (h *Handlers) SignUpOpen() http.HandlerFunc {
	return h.SignUp().Open()
}

// SignUpComplete creates the crew login from the opened link with the username and
// password the person chose ({"token", "username", "password"}), the address verified,
// holding no role and no sector, and signs them in.
func (h *Handlers) SignUpComplete() http.HandlerFunc {
	return h.SignUp().Complete()
}

// PendingStatus reports the sign-in that waits for a password confirmation or for the
// second factor, if any: for the factor, the state of the sign-in code (emailCode).
func (h *Handlers) PendingStatus() http.HandlerFunc {
	return h.Pending().Status()
}

// PendingConfirmWithPassword completes a waiting sign-in with the user's password.
func (h *Handlers) PendingConfirmWithPassword() http.HandlerFunc {
	return h.Pending().ConfirmWithPassword()
}

// PendingCancel discards a waiting sign-in.
func (h *Handlers) PendingCancel() http.HandlerFunc {
	return h.Pending().Cancel()
}

// PendingSendCode mails the sign-in code to the person whose sign-in waits for the
// second factor: the address they hold, verified or not, which the code then verifies.
func (h *Handlers) PendingSendCode() http.HandlerFunc {
	return h.Pending().SendCode()
}

// PendingVerifyCode completes a waiting sign-in with the code entered ({"code",
// "rememberDevice"}): a wrong code is refused (invalid_code) and the fifth kills the
// code, a code entered while none stands asks for a new one (code_expired), and
// rememberDevice keeps the browser trusted for the period.
func (h *Handlers) PendingVerifyCode() http.HandlerFunc {
	return h.Pending().VerifyCode()
}

// PendingEnrollEmail takes the address a person who holds none gives for the code
// ({"email"}): the code is mailed there, and the address is written on them verified
// once it is entered.
func (h *Handlers) PendingEnrollEmail() http.HandlerFunc {
	return h.Pending().EnrollEmail()
}

// New opens the auth's permission store and session manager over the database. The
// permission engine validates the role file against the collection and refuses to start
// on a file that does not parse or grants what the release does not declare; it then
// blocks until its first policy snapshot is loaded. It announces its policy writes and
// watches the other instances' through the change signal, which is required.
func New(ctx context.Context, db *cloudspanner.Client, settings *Settings) (*Auth, error) {
	if err := settings.required(); err != nil {
		return nil, err
	}
	store, err := spannerstore.New(db, spannerstore.WithPrefix(TablePrefix))
	if err != nil {
		return nil, errors.Wrap(err, "spannerstore.New()")
	}
	// Beside the role file's roles the engine defines the framework's own in every
	// auth, Super Administrator (accesstypes.SuperAdministrator), held for the whole
	// service alone. What no role file takes from its holder is named here: the methods
	// that give and take the crew's roles, as the generated management names them
	// (access.WithSuperAdministratorMethods), so a super administrator gives any role of
	// the crew to anyone, in headquarters and in every sector.
	//
	// The engine is told who administers a sector as well: a person who holds
	// SectorMarshal in the sector itself, by a membership of their own or through the
	// sector's own group (access.WithTenantAdministratorRoles over the roles
	// AdministratorRoles names for a sector). A directory-only sector still admits its
	// own marshal with a password and the mailed code, so Bastion's marshal has a way in
	// when its directory has a problem; the governor, who holds the role in every
	// sector, administers none of them in this sense and is kept out by the switch like
	// anyone, but for the Super Administrator role she also holds.
	//
	// Demonstrates: auth.super-administrator, tenancy.directory-only.
	accessClient, err := access.New(store, settings.Collection, access.WithDefaultRoles(Roles(), Name), access.WithChangeSignal(settings.ChangeSignal),
		access.WithSuperAdministratorMethods(crewidentity.SuperAdministratorMethods()...),
		access.WithTenantAdministratorRoles(sectorAdministratorRoles()...))
	if err != nil {
		return nil, errors.Wrap(err, "access.New()")
	}
	if err := accessClient.WaitReady(ctx); err != nil {
		return nil, errors.Wrap(err, "access.Client.WaitReady()")
	}

	// The impersonation record rides beside the sessions, so StartImpersonatedSession
	// can mint view-as and act-as-role sessions, and the auth events table records how
	// each session was authenticated.
	impersonation, err := sessionstorage.NewImpersonationTable(impersonationsTable)
	if err != nil {
		return nil, errors.Wrap(err, "sessionstorage.NewImpersonationTable()")
	}
	events, err := sessionstorage.NewAuthEventsTable(eventsTable)
	if err != nil {
		return nil, errors.Wrap(err, "sessionstorage.NewAuthEventsTable()")
	}

	// One Auth session that every method establishes, which mails as the fleet
	// (session.WithMail): the invitations the auth's generated management sends, the
	// verification of an address a person adds, and the sign-up links, each leading to
	// the console's login page with its token in the query.
	//
	// The Auth is built knowing who holds the Super Administrator role
	// (session.SuperAdministratorsBy, an argument no Auth is built without): a holder
	// signs in with a second factor whatever the sign-in rule set below asks of anyone
	// else, held for the mailed code by a rule of the library's own after the crew's.
	// Here the crew's rule asks everyone already, so the library's changes nothing a
	// person sees; it is what keeps the factor on a super administrator if the crew's
	// rule is ever relaxed.
	sessionStore := sessionstorage.NewSpannerAuth(db, sessionstorage.WithImpersonation(impersonation), sessionstorage.WithAuthEvents(events))
	crewSession, err := session.NewAuth[session.NoCustomData, session.NoCustomData](
		sessionStore,
		settings.CookieKey,
		session.SuperAdministratorsBy(signin.SuperAdministrators(resource.SignInRoles(accessClient.UserManager()))),
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
		session.WithMail(settings.Mail.Sender, session.MailSettings{Application: Application, LoginURL: settings.Mail.LoginURL}),
		session.WithCookieName(Name),
		session.WithXSRFCookieName(XSRFCookie),
		session.WithSessionTimeout(settings.SessionTimeout),
		session.WithImpersonationTimeout(impersonationTimeout),
		session.WithDeviceVerificationURL(settings.DevicePage),
	)
	if err != nil {
		return nil, errors.Wrap(err, "session.NewAuth()")
	}

	// The methods, registered before the first request: the password method under the
	// library's name, and the Google method under GoogleRegistration. The Google method's
	// organization slot is the crew's one Workspace, named by the connection setting
	// (the name every directory identity's link carries) and checked against the hosted
	// domain, declared attesting where the deployment says the fleet administers it
	// (auth.Directory.Administered), so a sign-in through it carries the Workspace's
	// second factor and is never asked for the code. Its account rule is the library's standard one, members of the directory:
	// a person the directory verifies for the first time becomes a crew member under the
	// verified email, with no password (the directory is their way in) and no role until
	// an operator seats them. The application is the authority for the crew's roles
	// (google.DisableRoleSync): the sign-in neither reads nor writes them. A registration
	// whose directory writes the roles stands alone on its auth, and the crew auth has
	// three ways in; the members auth is the one whose directory ranks its people. A
	// verified email that is a username a password persona already holds links the
	// identity to that persona once their password confirms it (the directory's word
	// alone is not enough), so the development login APP_GOOGLE_USERNAME names is one
	// that is not on the manifest.
	//
	// Whether a method admits a person with no account and no invitation is no part of
	// its registration: every method here starts invite only, and a fleet administrator
	// opens one to sign-up, or makes it invite only again, on the console's Sign-in page
	// (the row of the method in CrewSessionSignInMethods, which every sign-in reads in
	// its own transaction). While the password method is open a stranger gives an
	// address, follows the mailed link and chooses a username and a password, and the
	// login holds no role and no sector until they chart their own or an operator
	// seats them, exactly as a person who signs up through the sign-up registration
	// does. The router mounts the sign-up routes whatever was chosen, and they answer
	// 404 while the method is invite only.
	password := session.PasswordSignIn(crewSession)
	googleMethod := google.SignIn(crewSession, GoogleRegistration, nil, &google.Config{
		ClientID:     settings.Directory.ClientID,
		ClientSecret: settings.Directory.ClientSecret,
		RedirectURL:  settings.Directory.RedirectURL,
		Workspaces:   settings.Directory.Workspace(),
		RoleSync:     google.DisableRoleSync(),
		LoginURL:     settings.LoginURL,
	})
	// The sign-up registration, under SignUpRegistration: any Google account
	// (google.AnyGoogleAccount), which is how wide the method is, and the application as
	// the authority for roles (google.DisableRoleSync: the sign-in neither reads nor
	// writes roles). Once an administrator opens it to sign-up, a person the auth has
	// never seen becomes a crew login under the verified email and holds nothing until
	// they chart a sector of their own or an operator seats them; until then they are
	// refused (not_invited). It shares the directory's client; only the callback is its
	// own.
	signUpMethod := google.SignIn(crewSession, SignUpRegistration, nil, &google.Config{
		ClientID:     settings.Directory.ClientID,
		ClientSecret: settings.Directory.ClientSecret,
		RedirectURL:  settings.SignUpRedirectURL,
		Workspaces:   google.AnyGoogleAccount(),
		RoleSync:     google.DisableRoleSync(),
		LoginURL:     settings.LoginURL,
	})

	// The persona method, under PersonaRegistration, where the provider is configured
	// (registerPersona): the sign-in of the test environments.
	personaMethod := registerPersona(crewSession, settings)

	// The sign-in rule: the library's tenant policy. A person's sectors are where they
	// hold a role (the engine's memberships), and what each sector admits is its
	// declaration, read in the sign-in's transaction. A sector's declaration restricts
	// its own door alone: a sector that is not directory only admits everyone it
	// seats, however they signed in, and a directory-only sector admits a sign-in
	// through its own directory and nothing else. So a person is refused only when
	// every sector that seats them is directory only and none has the way they signed
	// in as its directory (the bosun, whose only sector is Bastion, is refused
	// directory_only by password). A super administrator is admitted whatever a
	// sector declares; a directory-only sector's own marshal who signs in with a
	// password is held for the mailed code and then enters that sector (the engine
	// names who administers a sector); a person holding a role outside any one sector (a global role,
	// or one held in every sector) is admitted and then enters a sector only where
	// its declaration admits their session, as anyone does; a sign-in whose directory
	// names the roles (the client's) is admitted on the library's checks;
	// a person who holds no sector is admitted too (signin.AdmitWithoutTenant), since
	// the pioneer signs up first and charts her sector after. Which sectors the session
	// then reaches is decided again, sector by sector, where its chart is computed
	// (app.UserPermissions): every sector that seats the person, but a directory-only
	// sector whose directory they did not sign in through, which stays on the chart
	// marked closed, with the reason. The memberships are read outside the sign-in's transaction
	// (resource.SignInRoles): the Spanner client refuses the engine's read under the
	// transaction's context as a nested transaction.
	// The rule's last word is the second factor every crew session carries
	// (requireSecondFactor): a sign-in the tenant policy admits waits for the
	// code the auth mails, unless the fleet's directory attested for the person or the
	// browser is one they asked to be remembered.
	//
	// Demonstrates: tenancy.declaration, tenancy.directory-only, auth.second-factor.
	crewSession.SetSignInPolicy(signin.TenantPolicy(resource.SignInRoles(accessClient.UserManager()), settings.TenantDeclarations, signin.AdmitWithoutTenant()).Then(requireSecondFactor))

	// The crew's identity client (pkg/identity, generated): the rules that manage the
	// auth's people, over the engine that reads its memberships and the API of the
	// session manager that signs them in, whose hasher makes the hash of a temporary
	// password one of its methods sets. A membership one of its methods commits is
	// announced by that engine, on this instance at the commit and on every other
	// through the policy signal. The client knows the roles that administer a sector
	// (identity.WithAdministratorRoles, the ones held in every sector), so a person who
	// removes a role from themselves in a sector is judged by the rule on its last
	// administrator: no sector is left without a marshal. The service as a whole is
	// kept by the client's own rule on the last super administrator, whose role and
	// account stay whoever asks. The store under it writes a
	// membership as a create that leaves a row that is there alone (the generated create
	// patch's OrSkip), so a role given twice is written once and keeps the time it was
	// first given.
	//
	// The client makes the sectors too. Nobody charts a sector directly: Headquarters
	// invites one person with the sector's details, which the client checks against the
	// Sector record's own create under the sender's grants, and that person's first
	// sign-in makes the sector through the generated maker (settings.TenantMaker), seats
	// them with the roles that administer it and, where they came through an
	// organization's directory, makes that directory the sector's own, all in the
	// transaction that consumes the invitation.
	//
	// Demonstrates: auth.generated-identity, OrSkip, tenancy.invited-tenant.
	identityClient, err := crewidentity.NewClient(accessClient, crewSession.API(), settings.TenantMaker, identity.WithAdministratorRoles(sectorAdministratorRoles()))
	if err != nil {
		return nil, errors.Wrap(err, "crewidentity.NewClient()")
	}

	// The two ways a sign-in gives a person roles, each a hook of the identity client
	// that the session library runs inside the transaction of the step that causes it
	// and hands that transaction, so the role is written with the step or not at all.
	// They are set here, where the auth is built, so no crew auth is open without them.
	//
	// The directory admission: what a fleet member gets at their first sign-in through
	// a sector's directory, before the registration's account rule is reached. The row
	// of the sector whose directory the identity came through decides: a row with a
	// default seat creates a stranger and seats them with it in one transaction (Cinder
	// seats them SectorCrew), and the sign-in rule then finds the membership; a row with
	// none refuses a stranger (invitation_only), and no account is created, unless they
	// followed an invitation link, which is the way in. A person whose account stood
	// before is given the default seat too, at the sign-in that links the account to
	// the directory, where they hold no role in the sector: a persona who signs in
	// through the fleet's directory for the first time and confirms with their password
	// is seated at Cinder then. It is given once for a link, so a role a marshal removes
	// afterwards is not given back by signing in. A directory no sector has falls to
	// the registration's rule as before (the pioneer's sign-up, a personal account,
	// which is no sector's directory). The crew's roles are the application's, so the
	// admission is set on this auth; an auth whose directory writes the roles (the
	// members' directory) is the authority for everyone it holds and sets none.
	//
	// The invitation's acceptance: an accepted invitation seats the person with the
	// roles it carries, in the sector it names or in the global partition, in the
	// transaction that consumes the invitation, so the invitation is spent and its
	// roles given together or neither is, and the sign-in rule sees the new seat: a
	// stranger invited into Bastion signs in admitted.
	//
	// The sign-up: a person who signs up through a method an administrator opened is
	// handed to the identity client in the transaction that makes their account
	// (SignedUp). The fleet has sectors, so an open method names no default role and
	// the person holds nothing until they chart a sector of their own.
	//
	// Demonstrates: tenancy.default-seat, auth.invitation, auth.open-sign-up.
	crewSession.SetDirectoryAdmission(settings.TenantDirectories, identityClient.DirectorySeated())
	crewSession.SetInvitationAccepted(identityClient.InvitationAccepted())
	crewSession.SetSignUpSeated(identityClient.SignedUp())
	// What a sign-in clears. A role, a place in a group and a group's role given for a
	// period count inside it alone, and nothing is written when one ends: the row goes
	// when its person next signs in. The session library calls this hook once the
	// session is committed, and the identity client removes the person's ended roles and
	// places, nothing else and never the account; a failure is logged and the session
	// stands. The members' roles are their directory's and carry no period: no hook.
	//
	// Demonstrates: auth.periods.
	crewSession.SetSignedInHook(identityClient.SignedIn(resource.NewSpannerClient(db)))

	// The machine outlet the auth issues tokens for, registered before the first
	// request as the methods are: the droids' channel, whose token check the generated
	// router composes in front of its routes. Registration closes with the first use of
	// the Auth, so it happens here rather than where the App is built, which a bootstrap
	// or a test harness may reach after creating users through the API.
	droids := session.RegisterMachineOutlet(crewSession, DroidsOutlet)

	return &Auth{
		access:   accessClient,
		handlers: &Handlers{Session: crewSession, store: sessionStore, password: password, google: googleMethod, signUp: signUpMethod, persona: personaMethod, droids: droids},
		identity: identityClient,
	}, nil
}

// registerPersona registers the persona method on the Auth where the provider is
// configured, and answers nil where it is not: the session library's generic OpenID
// Connect method (oidc.SignIn) against the persona provider the placement names, the
// application a public client (no secret; PKCE binds the code to the login that asked for
// it). The provider attests (Attesting): the person signed in to the provider's host to
// pick a persona, which is the whole proof in a test environment, so a persona sign-in is
// never asked for the code. The application is the authority for roles
// (signin.DisableRoleSync), and the method is left invite only and declares seeded
// users (SeededUsers): the persona's identity
// (its login as the subject) is linked to the crew login seeded under that login
// (cast.json, SeedCast), and a persona the seed never made is refused. The declaration
// says the provider's names are the application's own. A seeded login has no password
// and has never signed in, so nothing but its name ties it to a sign-in, and a sector's
// directory asserts the names it likes: without the declaration such a login is linked
// only through the directory of the one sector it belongs to, or with its own
// invitation, and the cast holds its roles in every sector or in none. An empty issuer
// registers nothing, so a later environment never offers the method and nothing in the
// build differs.
//
// Demonstrates: auth.persona.
func registerPersona(crewSession *Session, settings *Settings) *oidc.Method {
	if settings.Persona.Issuer == "" {
		return nil
	}

	return oidc.SignIn(crewSession, PersonaRegistration, nil, &oidc.Config{
		Issuer:      settings.Persona.Issuer,
		ClientID:    settings.Persona.ClientID,
		RedirectURL: settings.Persona.RedirectURL,
		Connection:  PersonaRegistration,
		Attesting:   true,
		RoleSync:    signin.DisableRoleSync(),
		SeededUsers: true,
		LoginURL:    settings.LoginURL,
	})
}

// PersonaRegistered reports whether the persona method is registered on the Auth: the
// persona provider is configured, as it is in the test environments alone.
func (a *Auth) PersonaRegistered() bool {
	return a.handlers.persona != nil
}

// Access returns the auth's permission engine: the handlers check against it, and its
// UserManager writes roles, grants, and role assignments.
func (a *Auth) Access() *access.Client {
	return a.access
}

// Session returns the auth's session manager, for the bootstrap's user creation and the
// impersonation routes' API.
func (a *Auth) Session() *Session {
	return a.handlers.Session
}

// Handlers returns the auth's served surface.
func (a *Auth) Handlers() *Handlers {
	return a.handlers
}

// Identity returns the crew's identity client: what the generated methods that manage
// the crew run over (pkg/identity/crewidentity), on their routes and in a transaction
// the application opens itself. The roles a sign-in gives are written through the same
// client, by the hooks New sets.
func (a *Auth) Identity() *crewidentity.Client {
	return a.identity
}

// Close waits for the mail the auth is still sending, then releases the permission
// engine. A reset link a crew member asked for is mailed after their request is answered
// (session.AuthAPI.WaitForMail), so the configuration closes the auth once the server
// has stopped and before it closes the database, and no link a person was promised is
// lost.
func (a *Auth) Close() error {
	a.handlers.API().WaitForMail()
	if err := a.access.Close(); err != nil {
		return errors.Wrap(err, "access.Client.Close()")
	}

	return nil
}

// requireSecondFactor is the last word of the crew's sign-in rule, chained after the
// tenant policy (signin.Policy.Then): every session carries a second factor, with the
// sign-in code the library mails as the factor the login page runs
// (signin.RequireMFA(sessioninfo.MethodEmailCode)). The library satisfies it before it
// holds the sign-in: a directory the fleet administers attests for its people
// (auth.Directory.Administered), a browser the person asked to be remembered after a code
// stands in for the next thirty days, and otherwise the sign-in waits for the code the
// person reads from their mail. The requirement is the rule's, not a tenant's, so no
// sector turns it off. Each auth package holds its own, as impulse writes it and as
// impulse check reads it; the members' is the same rule.
func requireSecondFactor(context.Context, *cloudspanner.ReadWriteTransaction, *sessioninfo.NewSessionRequest) (*signin.Decision, error) {
	return signin.RequireMFA(sessioninfo.MethodEmailCode), nil
}
