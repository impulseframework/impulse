package authpkg

// secondFactorRule is the sign-in rule every auth package ends with, as the template
// writes it into the package and impulse upgrade's second-factor recipe writes it into an
// auth package that set none (SecondFactorRule).
const secondFactorRule = `// requireSecondFactor is the sign-in rule every session ends with: the second factor, the
// sign-in code mailed to the person's address (sessioninfo.MethodEmailCode), unless the
// sign-in carries one already, a remembered browser's token or the attestation of a
// directory the deployment administers or whose token says a second factor was passed,
// which the library takes before it asks. A sign-in the rule holds waits for the code
// (?pending=mfa on the login page), where the person asks for it, enters it and may ask
// for the browser to be remembered.
func requireSecondFactor(context.Context, *cloudspanner.ReadWriteTransaction, *sessioninfo.NewSessionRequest) (*signin.Decision, error) {
	return signin.RequireMFA(sessioninfo.MethodEmailCode), nil
}
`

// authTemplate renders the Auth file: the package, its tables, its permission store, its
// role file, the Auth session with the methods registered on it, and the served surface.
const authTemplate = `// Package {{.Name}} is the {{.Name}} auth: {{.Doc}}, who hold their roles in the
// {{.Name}} permission store.
//
// An auth is a package. It owns its session manager, one Auth session with the sign-in
// methods registered on it (here {{range $i, $m := .Methods}}{{if $i}}, {{end}}{{$m.Name}}{{end}}), with its own session, user, identity, auth
// event, pending sign-in, sign-in attempt, mail link, sign-in code and trusted device
// tables and cookies; its permission
// store, with its own table prefix, whose engine signals policy changes to the
// application's other instances through the live service; and its role file, embedded
// beside it, so the release's default roles travel with the binary. Each method is its
// own file in this package, named for its registration.
// A site or an outlet binds to an auth by composing its handlers, and two auths on one
// database and one host never collide, because everything an auth names carries its
// name. The same name in this auth and in another is two unrelated principals. The
// package is named for the population, never for how it signs in: the {{.Name}} can gain
// or lose a sign-in method without the package moving.
//
// The auth mails: an administrator invites a person by address, with the roles the
// invitation seats, and the invitee follows the link to accept it; a signed-in person
// adds an address and proves it by following the link sent to it{{if and .Password .SignUpChoice}}; and, while an
// administrator has the password method open to sign-up, a stranger signs up from a link
// mailed to the address they give{{end}}.
// An address an administrator sets for a person is theirs once its mailbox confirms it,
// by the link the auth mails there{{if .Password}}; and a person who forgot their password gets back
// in by a link mailed to the address they confirmed, which an administrator sends or,
// while an administrator allows it, the person asks for at the sign-in page{{end}}.
// The links open the login page of the surface that binds to this auth
// (Settings.MailLoginURL), through the sender the data level hands in (Settings.Mail).
//
// Every session carries a second factor. A sign-in the password admitted, or a directory
// the deployment does not administer, waits for the sign-in code the auth mails to the
// person's verified address (a person who holds none gives one first), unless the
// browser is one the person asked to be remembered after a code (the {{.Name}}-trusted
// cookie); a sign-in through a directory the deployment administers, or whose token says
// a second factor was passed, carries the directory's and is asked for nothing.{{if .Directory}} Every
// account a person signs in to has a confirmed address, so a person who comes through a
// directory that does not vouch for theirs (Microsoft Entra ID vouches for none)
// confirms one at their first sign-in with a code mailed to it, whatever the directory
// attests.{{end}} An
// administrator makes the server forget the browsers a person asked to be remembered,
// from the person's account page, so their next sign-in asks again.
package {{.Name}}

import (
	"context"
	_ "embed" // the role file rides in the binary
	"net/http"
	"time"

	cloudspanner "cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/access/spannerstore"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session"
{{- range .Imports}}
	"{{.}}"
{{- end}}
	"github.com/impulseframework/impulse/session/mail"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/go-playground/errors/v5"
)

const (
	// Name is the auth's name: the cookie its sessions ride in.
	Name = "{{.Name}}"
	// TablePrefix is the name's PascalCase form, which prefixes every table the auth owns.
	TablePrefix = "{{.Pascal}}"
	// Application is the application's name as the mail the auth sends says it: the
	// subject and the first line of an invitation name it.
	Application = "{{.Application}}"

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
	sessionsTable    = "{{.Pascal}}Sessions"
	usersTable       = "{{.Pascal}}SessionUsers"
	identitiesTable  = "{{.Pascal}}SessionIdentities"
	eventsTable      = "{{.Pascal}}SessionAuthEvents"
	pendingTable     = "{{.Pascal}}SessionPendingSignIns"
	attemptsTable    = "{{.Pascal}}SessionSignInAttempts"
	methodsTable     = "{{.Pascal}}SessionSignInMethods"
	mailLinksTable   = "{{.Pascal}}SessionMailLinks"
	tokensTable      = "{{.Pascal}}SessionTokens"
	deviceCodesTable = "{{.Pascal}}SessionDeviceCodes"
	factorCodesTable    = "{{.Pascal}}SessionFactorCodes"
	trustedDevicesTable = "{{.Pascal}}SessionTrustedDevices"
{{- range .Outlets}}

	// {{.Pascal}}Outlet is a machine outlet whose tokens the auth issues: the generator
	// program declares it with Tokens of this package, so the generated router composes
	// the auth's token check in front of the outlet's routes, mounts the device flow's
	// endpoints under it, and serves the token routes on the auth's session outlet. A
	// request there carries a bearer token the auth issued: an application token a person
	// issued, or one a command-line client obtained with a person's approval. New
	// registers the outlet on the Auth before the first request, as it registers the
	// sign-in methods.
	{{.Pascal}}Outlet = "{{.Name}}"
{{- end}}
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

// Settings are the auth's settings: the collection the role file validates against, the
// live service the permission engine signals through, and the environment-derived values,
// the settings of each sign-in method among them.
type Settings struct {
	// Collection is the generated permission collection: the resources, permissions and
	// fields the release declares, which the role file validates against when the engine
	// opens. The package that constructs the auth passes it in; this package imports no
	// router.
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
	// Mail sends the auth's mail: the invitation an administrator sends to an address{{if .Password}},
	// the link that verifies an address a person adds, the link a sign-up starts from, and
	// the code a person who has no password chooses their first one with.{{else}}
	// and the link that verifies an address a person adds.{{end}}
	// The application's one sender, which the data level opens: the Workspace relay
	// (session/mail/workspace) where the deployment names a mailbox, and the log (mail.Log)
	// in development, where nothing is sent and a developer follows the link from the log.
	// Required; New refuses nil.
	Mail mail.Sender
	// MailLoginURL is the absolute address of the login page of the surface that binds
	// to this auth (https://host/login), which every mailed link opens with its token in
	// the query: the application's public address and the surface's login path.
	MailLoginURL string
{{- if .Outlets}}
	// DevicePage is the console page where a signed-in person approves a command-line
	// client's request to act as them, which a device authorization tells the client
	// (verification_uri): a path in the application, the mount path of the surface that
	// binds to this auth joined with /device ("/console/device"), where the console
	// mounts the library's device approval page.
	DevicePage string
{{- end}}
{{- if .Directory}}
	// LoginURL is the browser page a refused or waiting sign-in returns to, with the
	// reason as a code in the query (?code=, a sessioninfo.LoginRefusalCode, never text, or
	// ?pending= for a sign-in that waits): the login page of the surface that binds to
	// this auth, which holds the sentence for each code.
	LoginURL string
{{- end}}
{{- range .Methods}}{{if .Directory}}
	// {{.Pascal}} is the {{.Name}} registration's settings: how the application is known to
	// {{.Provider}}, read from the environment ({{join .Variables ", "}}).
	{{.Pascal}} {{.SettingsType}}
{{- end}}{{end}}
{{- if .TenantPolicy}}
{{- if .TenantPolicy.Directories}}
	// TenantDeclarations reads what a tenant declares about who may enter it, in the
	// sign-in's transaction, which the sign-in rule decides with: the generated reader
	// over the {{.TenantPolicy.Record}} record and its directories ({{.TenantPolicy.Reader}}).
	// Required; New refuses nil.
{{- else}}
	// TenantDeclarations reads what a tenant declares about who may enter it, which the
	// sign-in rule decides with: the generated reader over the {{.TenantPolicy.Record}} record
	// ({{.TenantPolicy.Reader}}), which answers nothing for every tenant, since
	// the application has no tenant directories. Required; New refuses nil.
{{- end}}
	TenantDeclarations signin.DeclarationReader[*cloudspanner.ReadWriteTransaction]
{{- end}}
{{- if .DirectoryAdmission}}
	// TenantDirectories reads every tenant's declaration of one directory with the
	// default seat its row carries, in the sign-in's transaction, which the directory
	// admission decides a stranger's first sign-in with: the generated reader
	// ({{.TenantPolicy.DirectoryReader}}). Required; New refuses nil.
	TenantDirectories signin.DirectoryReader[*cloudspanner.ReadWriteTransaction]
{{- end}}
{{- if .MakesTenants}}
	// TenantMaker makes a {{.TenantPolicy.Record}} from an accepted invitation: the generated maker over
	// the record's own create patch ({{.TenantPolicy.Maker}}). Nobody creates a tenant
	// directly: staff invite one person with the tenant's details, which the auth's
	// identity client checks with the maker when the invitation is sent, and that
	// person's first sign-in makes the tenant with it. Required; New refuses nil.
	TenantMaker identity.TenantMaker
{{- end}}
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

// Auth is the {{.Name}} auth: its permission store and its session manager with the
// sign-in methods registered on it.
type Auth struct {
	access   *access.Client
	handlers *Handlers
{{- if .IdentityPackage}}
	// identity is the auth's identity client: the generated management of its people
	// ({{.IdentityDir}}), built over the engine and the session manager above.
	identity *{{.Name}}identity.Client
{{- end}}
}

// Handlers is the auth's served surface, the shape the generated router names for the
// outlet bound to it: the Auth's middleware and handlers, what the login page is told,
// the pending sign-in handlers, each method's handlers under its registration name
// (in the method's own file){{if .Outlets}}, and the token check of each machine outlet whose tokens
// the auth issues{{end}}.
type Handlers struct {
	*Session
{{- range .Methods}}
	{{.Field}} {{.Type}}
{{- end}}
{{- range .Outlets}}
	{{.Name}} *session.MachineOutlet
{{- end}}
}
{{- range .Outlets}}

// {{.Pascal}}Tokens returns the {{.Name}} outlet's token check (router.{{.Pascal}}Tokens): the
// machine outlet registered on the Auth under {{.Pascal}}Outlet, whose ValidateToken binds
// each request under the outlet's prefix to the person its bearer token acts for.
func (h *Handlers) {{.Pascal}}Tokens() *session.MachineOutlet {
	return h.{{.Name}}
}
{{- end}}

// PendingStatus reports the sign-in that waits for {{if .Password}}a password confirmation{{else}}the second factor{{end}}, if any.
func (h *Handlers) PendingStatus() http.HandlerFunc {
	return h.Pending().Status()
}
{{- if .Password}}

// PendingConfirmWithPassword completes a waiting sign-in with the user's password.
func (h *Handlers) PendingConfirmWithPassword() http.HandlerFunc {
	return h.Pending().ConfirmWithPassword()
}
{{- end}}

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
// for the whole application alone
{{- if .RolesSynced}}: its holder enters every tenant. The directory writes
// this auth's roles, so the directory decides who is a super administrator: the role is
// given to a person the directory names for it and taken from one it no longer names,
// the deployment invites nobody to it, and the library asks a holder for no second factor
// of its own.
{{- else}}: its holder gives and takes any role of the auth, enters
// every tenant, and signs in with a second factor whatever the sign-in rule asks.
{{- end}}
func New(ctx context.Context, db *cloudspanner.Client, settings *Settings) (*Auth, error) {
	if settings.Signals == nil {
		return nil, errors.New("{{.Name}}.Settings.Signals is nil: the permission engine signals policy changes through the application's live service, which every application wires")
	}
	if settings.Mail == nil {
		return nil, errors.New("{{.Name}}.Settings.Mail is nil: the auth mails its invitations and address verifications{{if .Password}} and its sign-up links{{end}} through the application's sender (the Workspace relay deployed, the log in development), which the data level opens")
	}
{{- if .TenantPolicy}}
	if settings.TenantDeclarations == nil {
		return nil, errors.New("{{.Name}}.Settings.TenantDeclarations is nil: the sign-in rule reads what each tenant declares about who may enter it through the generated reader ({{.TenantPolicy.Reader}}), which the data level hands in")
	}
{{- end}}
{{- if .DirectoryAdmission}}
	if settings.TenantDirectories == nil {
		return nil, errors.New("{{.Name}}.Settings.TenantDirectories is nil: the directory admission reads what the tenants declare about one directory, its default seat included, through the generated reader ({{.TenantPolicy.DirectoryReader}}), which the data level hands in")
	}
{{- end}}
{{- if .MakesTenants}}
	if settings.TenantMaker == nil {
		return nil, errors.New("{{.Name}}.Settings.TenantMaker is nil: a tenant is made by an invitation's first sign-in, through the generated maker over the {{.TenantPolicy.Record}} record ({{.TenantPolicy.Maker}}), which the data level hands in")
	}
{{- end}}
	store, err := spannerstore.New(db, spannerstore.WithPrefix(TablePrefix))
	if err != nil {
		return nil, errors.Wrap(err, "spannerstore.New()")
	}
{{- if .Foothold}}
	_, tenantAdministrators := AdministratorRoles()
{{- end}}
	accessClient, err := access.New(store, settings.Collection,
		access.WithDefaultRoles(Roles(), Name),
		access.WithChangeSignal(access.ChangeSignalFunc(announcePolicy(settings.Signals), watchPolicy(settings.Signals))),
{{- if .IdentityPackage}}
		// What no role file takes from a super administrator: the methods that give and
		// take the auth's roles, as the generated management names them.
		access.WithSuperAdministratorMethods({{.Name}}identity.SuperAdministratorMethods()...),
{{- end}}
{{- if .Foothold}}
		// Who administers a tenant: a person holding one of these roles in the tenant
		// itself, by a membership of their own or through the tenant's own group. A
		// tenant that admits its people through its directory only still admits them,
		// with a password and a second factor, so its administrators have a way in when
		// its directory has a problem; the sign-in rule and each request ask the engine.
		access.WithTenantAdministratorRoles(tenantAdministrators...),
{{- end}}
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
{{- if .RolesSynced}}
	// The Auth is built knowing who holds the Super Administrator role, as every auth
	// with roles is, which is why the permission engine opens first. The library's own
	// second factor for a holder holds nobody who signs in through a directory that
	// writes the roles: there the directory is the authority, and the sign-in rule set
	// below asks of a holder what it asks of anyone. The memberships are read outside
	// the sign-in's transaction (resource.SignInRoles).
{{- else}}
	// The Auth is built knowing who holds the Super Administrator role, which is why the
	// permission engine opens first: a holder signs in with a second factor whatever the
	// sign-in rule set below asks of anyone else, held for the mailed code by a rule of
	// the library's own after the application's. The memberships are read outside the
	// sign-in's transaction (resource.SignInRoles).
{{- end}}
	authSession, err := session.NewAuth[session.NoCustomData, session.NoCustomData](
		sessionstorage.NewSpannerAuth(db, sessionstorage.WithAuthEvents(events)),
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
		session.WithMail(settings.Mail, session.MailSettings{Application: Application, LoginURL: settings.MailLoginURL}),
		session.WithCookieName(Name),
		session.WithXSRFCookieName(XSRFCookie),
		session.WithSessionTimeout(settings.SessionTimeout),
{{- if .Outlets}}
		session.WithDeviceVerificationURL(settings.DevicePage),
{{- end}}
	)
	if err != nil {
		return nil, errors.Wrap(err, "session.NewAuth()")
	}

	handlers := &Handlers{Session: authSession}
{{- range .Methods}}
{{- if .Fallible}}
	handlers.{{.Field}}, err = register{{.Pascal}}(authSession, accessClient, settings)
	if err != nil {
		return nil, err
	}
{{- else if .Directory}}
	handlers.{{.Field}} = register{{.Pascal}}(authSession, accessClient, settings)
{{- else}}
	handlers.{{.Field}} = register{{.Pascal}}(authSession)
{{- end}}
{{- end}}
{{- range .Outlets}}
	// The {{.Name}} outlet, registered before the first request as the methods are: its
	// token check is what the generated router composes in front of the outlet's routes.
	// Registration closes with the first use of the Auth, so it happens here rather than
	// where the App is built, which a bootstrap or a test harness may reach after
	// creating users through the API.
	handlers.{{.Name}} = session.RegisterMachineOutlet(authSession, {{.Pascal}}Outlet)
{{- end}}
{{- if .TenantPolicy}}
{{- if .TenantPolicy.Directories}}

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
{{- else}}

	// The sign-in rule: the library's tenant policy. A person's tenants are where they
	// hold a role (the engine's memberships). The application has no tenant
	// directories, so no tenant declares who may enter it and the generated reader
	// answers nothing for every tenant: a tenant admits everyone it seats, however they
	// signed in, a person who holds a role outside any one tenant (a global role, or
	// one held in every tenant) is admitted on the library's checks, and a super
	// administrator before any of it. A sign-in method through an organization's
	// directory is a way for an invited person to sign in and nothing more.
{{- end}}
{{- if .TenantPolicy.AdmitWithoutTenant}}
	// A person who holds no tenant is admitted too (signin.AdmitWithoutTenant): a
	// person who signs up through a sign-in method an administrator opened signs in
	// first and creates a {{.TenantPolicy.Record}} of their own, and keeps their account when the method
	// is made invite only again. Nobody gets an account that way while every method is
	// invite only, and the create-your-own route refuses while none is open.
{{- else}}
	// A person who holds no tenant is refused (no_tenant): nobody signs up to this auth
	// and creates a {{.TenantPolicy.Record}} of their own, since another auth's outlets serve the
	// record or a directory gives this one's roles.
{{- end}}
{{- if .TenantPolicy.Directories}}
	// Which tenants the session then reaches is decided again, tenant by tenant, where
	// its tenant list is computed (UserPermissions): a super administrator's session
	// reaches every tenant, a tenant's own administrator's reaches theirs with a
	// password and a second factor, and nobody else's reaches one whose declaration
	// their sign-in does not meet. The memberships are read outside the sign-in's transaction
	// (resource.SignInRoles): the Spanner client refuses the engine's read under the
	// transaction's context as a nested transaction. A sign-in the rule admits then
	// carries a second factor (requireSecondFactor).
{{- else}}
	// Which tenants the session then reaches is where its tenant list is computed
	// (UserPermissions): a super administrator's session reaches every tenant, and
	// anybody else's the tenants that seat them. The memberships are read outside the
	// sign-in's transaction (resource.SignInRoles): the Spanner client refuses the
	// engine's read under the transaction's context as a nested transaction. A sign-in
	// the rule admits then carries a second factor (requireSecondFactor).
{{- end}}
	authSession.SetSignInPolicy(signin.TenantPolicy(resource.SignInRoles(accessClient.UserManager()), settings.TenantDeclarations{{if .TenantPolicy.AdmitWithoutTenant}}, signin.AdmitWithoutTenant(){{end}}).Then(requireSecondFactor))
{{- else}}

	// The sign-in rule: every session carries a second factor (requireSecondFactor).
	authSession.SetSignInPolicy(requireSecondFactor)
{{- end}}
{{- if .IdentityPackage}}

	// The auth's identity client ({{.IdentityDir}}, generated): the rules that manage the
	// auth's people, over the engine that reads its memberships and the API of the session
	// manager that signs them in, whose hasher makes the hash of a temporary password one
	// of its methods sets.{{if .Foothold}} The client knows the roles that count as a
	// tenant's administrator's (identity.WithAdministratorRoles, the ones held in every
	// tenant), so a person who removes a role from themselves in a tenant is judged by
	// the rule on the tenant's last administrator. The application's last administrator
	// is its last super administrator, kept by the client's own rule.{{end}}
{{- if .MakesTenants}}
	//
	// The client makes the tenants too. Nobody creates a {{.TenantPolicy.Record}} directly: staff invite
	// one person with the tenant's details, which the client checks against the record's
	// own create under the sender's grants, and that person's first sign-in makes the
{{- if .TenantPolicy.Directories}}
	// tenant through the generated maker (settings.TenantMaker), seats them with the
	// roles that administer it and, where they came through an organization's
	// directory, makes that directory the tenant's own, all in the transaction that
	// consumes the invitation.
{{- else}}
	// tenant through the generated maker (settings.TenantMaker) and seats them with the
	// roles that administer it, in the transaction that consumes the invitation,
	// however they signed in: no tenant takes a directory.
{{- end}}
{{- end}}
	identityClient, err := {{.Name}}identity.NewClient(accessClient, authSession.API(){{if .MakesTenants}}, settings.TenantMaker{{end}}{{if .Foothold}}, identity.WithAdministratorRoles(tenantAdministrators){{end}})
	if err != nil {
		return nil, errors.Wrap(err, "{{.Name}}identity.NewClient()")
	}
{{- if not .RolesSynced}}

	// A role, a place in a group and a group's role may each be given for a period, and
	// count inside it alone. A row whose period has ended goes when its person next signs
	// in: the session library calls the identity client's SignedIn once the session is
	// committed, apart from the sign-in, so a failure is logged and the session stands.
	// It removes the person's ended roles and places and nothing else, never the
	// account, and no job runs on a schedule.
	authSession.SetSignedInHook(identityClient.SignedIn(resource.NewSpannerClient(db)))
{{- end}}
{{- if .DirectoryAdmission}}

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
{{- if .SignUpChoice}}
	// directory no tenant has falls to the method's choice, which is invite only until an
	// administrator opens the method to sign-up. The auth's roles are the
{{- else}}
	// directory no tenant has falls to the method, which is invite only: an account
	// stands before the sign-in, or an invitation comes with it. The auth's roles are the
{{- end}}
	// application's, so the admission is set here; an auth whose directory writes the
	// roles sets none.
	authSession.SetDirectoryAdmission(settings.TenantDirectories, identityClient.DirectorySeated())
{{- end}}
{{- if not .RolesSynced}}

	// An accepted invitation gives its roles, in the tenant it names or for the whole
	// application, in the transaction that consumes the invitation (the identity
	// client's InvitationAccepted, which the session library runs inside it): the
	// invitation is spent and its roles given together or neither is, and the sign-in
	// rule that follows finds the memberships.{{if .MakesTenants}} An invitation that makes a tenant
{{- if .TenantPolicy.Directories}}
	// is accepted there too: the tenant's row, the person's administrator roles and the
	// tenant's directory are written with the invitation's consumption or not at all.
{{- else}}
	// is accepted there too: the tenant's row and the person's administrator roles are
	// written with the invitation's consumption or not at all.
{{- end}}{{end}}
	authSession.SetInvitationAccepted(identityClient.InvitationAccepted())

	// A person who signs up through a sign-in method an administrator opened is given
	// the method's default role in the transaction that makes their account (the
	// identity client's SignedUp, which the session library runs inside it), so no
	// account is left holding nothing.
{{- if .TenantPolicy}}
{{- if .TenantPolicy.AdmitWithoutTenant}}
	// Here a person who signs up holds no role of the whole application: they create a
	// {{.TenantPolicy.Record}} of their own, and an open method names no default role.
{{- else}}
	// No method of this auth is opened to sign-up here: nobody who signed up to it could
	// be given a {{.TenantPolicy.Record}}, so the hook has nobody to seat.
{{- end}}
{{- end}}
	authSession.SetSignUpSeated(identityClient.SignedUp())
{{- end}}
{{- end}}

	return &Auth{access: accessClient, handlers: handlers{{if .IdentityPackage}}, identity: identityClient{{end}}}, nil
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
{{- if .IdentityPackage}}

// Identity returns the auth's identity client: what the generated methods that manage
// the auth's people run over ({{.IdentityDir}}/{{.Name}}identity), on their routes and
// in a transaction the application opens itself.{{if not .RolesSynced}} The roles a sign-in gives are written
// through the same client, by the hooks New sets.{{end}}
func (a *Auth) Identity() *{{.Name}}identity.Client {
	return a.identity
}
{{- end}}

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

` + secondFactorRule + `
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
`

// loginFormTemplate renders the password method's file: the method that signs in from the
// login page's form.
const loginFormTemplate = `package {{.Auth}}

import (
	"net/http"

	"github.com/impulseframework/impulse/session"
)

// {{.Registration}} is the name the password method is registered under on the Auth,
// always password: the segment of its login route (user/password/login). A password
// sign-in is checked against the user's hash in the users table; the bootstrap creates
// the development logins with their passwords, and a deployed environment starts from the
// first administrator the migrate command invites (foothold.go), who chooses a password
// from the mailed link, the rest coming from its
{{- if .SignUpChoice}}
// administration surface, from the invitations it mails and, while an administrator has
// the method open to sign-up, from the strangers who sign up from a mailed link.
{{- else}}
// administration surface and from the invitations it mails.
{{- end}}
const {{.Registration}} = "password"

// PasswordLogin checks a username and password and starts the session.
func (h *Handlers) PasswordLogin() http.HandlerFunc {
	return h.{{.Field}}.Login()
}

// PasswordChange changes the signed-in user's password, given the old one.
func (h *Handlers) PasswordChange() http.HandlerFunc {
	return h.{{.Field}}.ChangeUserPassword()
}

// PasswordRemove removes the signed-in user's password, given the password, once they
// sign in through a directory; a user with no other way in keeps it.
func (h *Handlers) PasswordRemove() http.HandlerFunc {
	return h.{{.Field}}.RemoveUserPassword()
}

// FirstPasswordSendCode mails the signed-in person who has no password, because they
// have only ever signed in through a directory, the code they choose their first one
// with, at the address their account holds confirmed. Whoever asks must read that
// mailbox to finish, so the password is tied to it.
func (h *Handlers) FirstPasswordSendCode() http.HandlerFunc {
	return h.FirstPassword().SendCode()
}

// FirstPasswordSet gives the signed-in person who has no password the one they choose,
// with the code they were mailed. An account that has a password changes it with the
// old one (PasswordChange).
func (h *Handlers) FirstPasswordSet() http.HandlerFunc {
	return h.FirstPassword().Set()
}

// PasswordResetRequest takes a person's own request for a link that resets their
// password, served while an administrator has turned that on: the answer is the same
// whether or not the address has an account, and an address with none is mailed
// nothing.
func (h *Handlers) PasswordResetRequest() http.HandlerFunc {
	return h.PasswordReset().Request()
}

// PasswordResetOpen opens a password reset link, the person's own or one an
// administrator sent, and answers the username it resets the password of.
func (h *Handlers) PasswordResetOpen() http.HandlerFunc {
	return h.PasswordReset().Open()
}

// PasswordResetComplete sets the new password a reset link's reader chose: the link is
// spent, the old password is refused from then on, every session of the person ends,
// and they sign in with the new password.
func (h *Handlers) PasswordResetComplete() http.HandlerFunc {
	return h.PasswordReset().Complete()
}

// InvitationSignUp accepts an invitation as a stranger with a new password: the invitee
// becomes a user under the sign-in name they give{{if .UsernameIsEmail}}, the invited address{{end}}, with the
// address verified by the link, seated in the roles the invitation names.
func (h *Handlers) InvitationSignUp() http.HandlerFunc {
	return h.Invitation().SignUp()
}


// SignUpStart mails a sign-up link to the address a stranger gives; an address already
// held gets a notice instead, and the caller learns neither. The three sign-up routes
{{- if .SignUpChoice}}
// answer 404 while the password method is invite only, which it is until an
// administrator opens it to sign-up on the application's Sign-in page.
{{- else}}
// answer 404 while the password method is invite only, which it always is here: no
// method of this auth is opened to sign-up, since nobody who signed up to it could be
// given a tenant.
{{- end}}
func (h *Handlers) SignUpStart() http.HandlerFunc {
	return h.SignUp().Start()
}

// SignUpOpen reads the sign-up a mailed link carries, for the login page to offer the
// form.
func (h *Handlers) SignUpOpen() http.HandlerFunc {
	return h.SignUp().Open()
}

// SignUpComplete creates the user from the link, with the password they choose and the
// address verified, and starts their session.
func (h *Handlers) SignUpComplete() http.HandlerFunc {
	return h.SignUp().Complete()
}

// register{{.Pascal}} registers the password method on the Auth, before the first request.
{{- if .SignUpChoice}}
// Whether a stranger may sign up through it is no option here: the method is invite
// only until an administrator opens it to sign-up in the running application.
{{- else}}
// The method is invite only, and no method of this auth is opened to sign-up: a person
// gets an account by an invitation, or from an administrator.
{{- end}}
{{- if .UsernameIsEmail}}
// The sign-in name is the person's email address (session.UsernameIsEmail): the users
// surface derives the address from it, and a verified change of address renames the user.
{{- end}}
func register{{.Pascal}}(authSession *Session) *session.PasswordMethod {
	return session.PasswordSignIn(authSession{{if .UsernameIsEmail}}, session.UsernameIsEmail(){{end}})
}
`

// footholdTemplate renders the foothold file: the roles that count as an administrator's
// and, where the deployment invites the auth's first administrator, the invitation the
// migrate command asks for while nobody of the auth holds the Super Administrator role.
const footholdTemplate = `package {{.Name}}
{{if .InvitesAdministrator}}
import (
	"context"
	"slices"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/session"
)
{{else}}
import "github.com/impulseframework/impulse/accesstypes"
{{end}}
// AdministratorRoles are the roles that count as an administrator's, by where they are
// held: the ones whose holder can give a role back.
{{- if .AdministratorAuthored}} A person removes one held in a tenant
// from themselves only while another person holds one there
// (identity.WithAdministratorRoles), so nobody is the last to leave and a tenant always
// has someone who manages its people. The application as a whole is kept by the Super
// Administrator role instead (accesstypes.SuperAdministrator), whose last holder keeps it
// whoever asks. A role that manages nothing of people is not among them, whoever holds it.
{{- else}} None until they are authored with
// the role file (roles.json); the rule on a tenant's last administrator judges nobody
// until then.
{{- end}}
func AdministratorRoles() (global, everyDomain []accesstypes.Role) {
	return {{.AdministratorGlobal}}, {{.AdministratorEveryDomain}}
}
{{- if .InvitesAdministrator}}

// FirstAdministratorRoles are the application's roles the first administrator's
// invitation gives, by where they are held, beside the Super Administrator role every
// first administrator holds (firstAdministratorRoleNames):
{{- if .AdministratorAuthored}} the administrator's roles and
// what else the development identities hold (cmd/bootstrap/users.json), so the first
// administrator can do what the development logins can.
{{- else}} none until they are authored with the
// role file (roles.json), since the invitation names nothing the file does not define,
// and the first administrator holds the Super Administrator role alone until then.
{{- end}}
func FirstAdministratorRoles() (global, everyDomain []accesstypes.Role) {
	return {{.FirstAdministratorGlobal}}, {{.FirstAdministratorEveryDomain}}
}

// AdministratorVariable is the setting the placement names the auth's first
// administrator in, for each environment: the person's email address. The data level
// reads it, and the migrate command hands it to InviteAdministrator.
const AdministratorVariable = "{{.AdministratorVariable}}"

// InviteAdministrator invites the auth's first administrator, the way into a deployed
// environment: while nobody of the auth holds the Super Administrator role, the migrate
// command has an invitation mailed to email, the address the placement names
// (AdministratorVariable), which gives the Super Administrator role and the
// application's own roles for the first administrator (FirstAdministratorRoles). The
// person opens the link and signs in by a method of the auth, as everyone invited after
// them does.
{{- if .Password}}
// With the password method they choose a password there, their address proven by the
// link.
{{- end}}
{{- if .Directory}}
// Through a directory the invitation is what ties the sign-in to the roles, whoever
// else the directory admits and however many organizations the method is open to, and
// the address the directory gives must be the invited one.
{{- end}}
// No account is created ahead of them and no password is generated, so nothing stands
// that anybody else could sign in with. From there they invite the others. The
// development bootstrap creates its own logins instead (cmd/bootstrap/users.json).
//
// Every release runs it, and it answers what it did (session.FirstAdministrator, never
// the link): an invitation that stands for the address is left as it is and nothing is
// mailed; one that ran out (seven days) is replaced by a new one; one that stands for an
// address the placement no longer names, or for roles the release no longer names, is
// withdrawn, so its link no longer opens, and the address named now is mailed. Once
// somebody holds the Super Administrator role nobody is invited, nothing is mailed and
// no mailbox is asked for: every person after the first comes from the administration
// surface. An invitation of the first administrator's that still stands then, for an
// address the placement no longer names, is withdrawn all the same.
//
// The role is what is asked, read from the auth's own permission engine: an account
// that holds nothing (a person who signed up, one a directory seated, a persona who
// holds no such role) administers nothing and stops no invitation, and a holder whose
// account is disabled is a holder still. A persona the environment's cast seeds holding
// the role is a holder, so the cast is seeded before this runs.
//
// It refuses, with nothing mailed, while nobody holds the role and the placement names
// no address, and while nobody holds it and the application has no mailbox to send as:
// an invitation's link is never written to a log, since whoever opens it becomes the
// administrator, so the mailbox comes before the first release.
func (a *Auth) InviteAdministrator(ctx context.Context, email string) (*session.FirstAdministrator, error) {
	roles, err := firstAdministratorRoleNames()
	if err != nil {
		return nil, err
	}
	invited, err := a.handlers.API().InviteFirstAdministrator(ctx, a.access.UserManager(), &session.FirstAdministratorRequest{Email: email, RoleNames: roles})
	if err != nil {
		return nil, errors.Wrapf(err, "the %s auth's first administrator (%s)", Name, AdministratorVariable)
	}

	return invited, nil
}

// ReinviteAdministrator withdraws the first administrator's standing invitation and
// mails a fresh one to email, for a message that was lost: the earlier link no longer
// opens. It is what the migrate command's -reinvite-administrator runs, on demand from
// the deployment's operations. It refuses once somebody holds the Super Administrator
// role, since somebody is then there to invite the others, and is InviteAdministrator
// in every other respect.
func (a *Auth) ReinviteAdministrator(ctx context.Context, email string) (*session.FirstAdministrator, error) {
	roles, err := firstAdministratorRoleNames()
	if err != nil {
		return nil, err
	}
	invited, err := a.handlers.API().ReinviteFirstAdministrator(ctx, a.access.UserManager(), &session.FirstAdministratorRequest{Email: email, RoleNames: roles})
	if err != nil {
		return nil, errors.Wrapf(err, "the %s auth's first administrator (%s)", Name, AdministratorVariable)
	}

	return invited, nil
}

// firstAdministratorRoleNames are the roles the first administrator's invitation
// carries: the role the framework defines in every auth (accesstypes.SuperAdministrator),
// held for the whole application, whose holder alone makes another, and the
// application's own (FirstAdministratorRoles). The acceptance gives each role where the
// role file holds it, a global role for the whole application and a domain role in every
// tenant, so the invitation names nothing the file does not define where
// FirstAdministratorRoles says it is held: a role the release does not ship there is
// refused here, before an invitation is mailed that nobody could accept.
func firstAdministratorRoleNames() ([]string, error) {
	defined, err := Roles().Parse()
	if err != nil {
		return nil, errors.Wrap(err, "access.RoleFile.Parse()")
	}
	defines := func(roles []*access.Role, role accesstypes.Role) bool {
		return slices.ContainsFunc(roles, func(r *access.Role) bool {
			return r.Name == role
		})
	}
	global, everyDomain := FirstAdministratorRoles()
	names := []string{string(accesstypes.SuperAdministrator)}
	for _, role := range global {
		if !defines(defined.Roles.Global, role) {
			return nil, errors.Newf("the %s auth's first administrator is given the role %q for the whole application (FirstAdministratorRoles), which the auth's role file does not define as a global role: name a role roles.json defines there", Name, role)
		}
		names = append(names, string(role))
	}
	for _, role := range everyDomain {
		if !defines(defined.Roles.Domain, role) || defines(defined.Roles.Global, role) {
			return nil, errors.Newf("the %s auth's first administrator is given the role %q in every tenant (FirstAdministratorRoles), which the auth's role file does not define as a domain role alone: name a role roles.json defines there", Name, role)
		}
		names = append(names, string(role))
	}

	return names, nil
}
{{- else}}

// The deployment invites no first administrator for this auth. The application
// generates no management of the auth's people (generation.GenerateIdentity), so nothing
// gives an accepted invitation its roles, and an invitation mailed to the first
// administrator would let them in holding nothing. impulse check refuses the auth as it
// stands (first-administrator), since nobody can be invited as its first administrator
// and an account seeded by hand is refused too: have the application generate the
// management, build the auth's identity client from it, and invite the first
// administrator as impulse add auth writes it for such an auth.
{{- end}}
`

// azureTemplate renders a Microsoft Entra method's file.
const azureTemplate = `package {{.Auth}}

import (
{{- range .StdImports}}
	"{{.}}"
{{- end}}
{{ range .Imports}}
	"{{.}}"
{{- end}}
)

// {{.Registration}} is the name the {{.Name}} registration goes under on the Auth: the
// segment of its login, callback and front-channel logout routes (user/{{.Name}}/login).
// In development the simulated directory signs it in as {{.SimulatedUsername}}, or the
// shared APP_USERNAME when that is unset, with the roles {{.SimulatedRoles}} or APP_ROLES
// name.
const {{.Registration}} = "{{.Name}}"

// {{.SettingsType}} is how the application is known to Microsoft for the {{.Name}}
// registration, read from the environment. Under the session library's skipAuth build tag
// the directory is simulated and only RedirectURL is read: it is where the simulated
// sign-in returns.
type {{.SettingsType}} struct {
{{- range .Fields}}
	// {{.Name}} is {{.Doc}}.
	{{.Name}} string
{{- end}}
}

// {{.Pascal}}Login sends the browser to Microsoft, to sign in with an account the
// registration admits.
func (h *Handlers) {{.Pascal}}Login() http.HandlerFunc {
	return h.{{.Field}}.Login()
}

// {{.Pascal}}Callback receives the browser back from Microsoft, resolves the identity,
// applies the account rule and starts the session.
func (h *Handlers) {{.Pascal}}Callback() http.HandlerFunc {
	return h.{{.Field}}.Callback()
}

// {{.Pascal}}FrontChannelLogout ends the session Microsoft reports signed out: the
// front-channel logout the app registration names.
func (h *Handlers) {{.Pascal}}FrontChannelLogout() http.HandlerFunc {
	return h.{{.Field}}.FrontChannelLogout()
}

// register{{.Pascal}} registers the {{.Name}} registration on the Auth, before the first
// request.
{{- if eq .Authority "directory"}}
// The directory is the authority for role membership: every sign-in reconciles the
// person's roles to the token's application roles (signin.RoleSync), removing any it does
// not name, and a sign-in naming no known role is refused. Hand-assigned roles do not
// survive it, so nothing in the application assigns roles in this store, and the
// registration's declaration on every outlet chains .SyncsRoles(), so the generated
// management shows these people's roles as the directory's and never writes them.
{{- else}}
// The application is the authority for role membership (signin.DisableRoleSync): the
// directory proves who someone is and the application decides what they may do, so a
// sign-in neither reads nor changes the person's roles.
{{- end}}
{{- if .SyncsRoles}}
// A person Microsoft verifies for the first time becomes a user under their user
// principal name, with no password, linked by the directory's immutable tenant and
// object ids so a renamed account stays the same person, holding the roles the
// directory names: the directory decides who the method's people are, so the method
// has no invite-only or open choice.
{{- else}}
// A person Microsoft verifies for the first time, who has no account, needs an
{{- if .SignUpChoice}}
// invitation: the method is invite only until an administrator opens it to sign-up on
// the application's Sign-in page, and anyone else is refused (not_invited). A person
{{- else if .TenantDirectories}}
// invitation, or the directory of a tenant that names a default role: the method is
// invite only and is never opened to sign-up, since nobody who signed up to this auth
// could be given a tenant, and anyone else is refused (not_invited). A person
{{- else}}
// invitation: the method is invite only and is never opened to sign-up, since nobody
// who signed up to this auth could be given a tenant, and anyone else is refused
// (not_invited). A person
{{- end}}
// admitted becomes a user under their user principal name, with no password, linked by
// the directory's immutable tenant and object ids so a renamed account stays the same
// person; a user created beforehand under that name is linked and signed in.
{{- end}}
func register{{.Pascal}}(authSession *Session, accessClient *access.Client, settings *Settings) (*azure.Method{{if .Fallible}}, error{{end}}) {
{{- if .SlotFallible}}
	tenants, err := {{.Field}}Slot(settings)
	if err != nil {
		return nil, err
	}
{{- else if .Administered}}
	tenants := {{.Field}}Slot(settings)
{{- else}}
	tenants := {{.Field}}Slot()
{{- end}}
	_ = accessClient

	return azure.SignIn(authSession, {{.Registration}}, nil, &azure.Config{
		Tenants:      tenants,
		ClientID:     settings.{{.Pascal}}.ClientID,
		ClientSecret: settings.{{.Pascal}}.ClientSecret,
		RedirectURL:  settings.{{.Pascal}}.RedirectURL,
		RoleSync:     {{.RoleSyncExpr}},
		LoginURL:     settings.LoginURL,
	}){{if .Fallible}}, nil{{end}}
}
{{- if .Administered}}

// {{.Field}}Slot is the registration's organization slot, {{.SlotWords}}, with the
// tenants the deployment administers declared as attesting (AdministeredTenants): a
// sign-in from one carries the tenant's own second factor, which the deployment vouches
// for, and is not asked for the sign-in code. bedrock reads the registration's tenant
// shape from this function, for the app registration's supported account types.
{{- if eq .SlotKind "one" "one-personal"}}
func {{.Field}}Slot(settings *Settings) (azure.TenantSet, error) {
	tenant := azure.Tenant(settings.{{.Pascal}}.TenantID)
	for _, id := range parse{{.Pascal}}Administered(settings.{{.Pascal}}.AdministeredTenants) {
		if !strings.EqualFold(id, settings.{{.Pascal}}.TenantID) {
			return azure.TenantSet{}, errors.Newf("{{.Registration}}: the administered tenant %q is not the tenant admitted, %s", id, settings.{{.Pascal}}.TenantID)
		}
		tenant = tenant.Attesting()
	}

	return {{if eq .SlotKind "one"}}tenant{{else}}azure.Tenants(tenant, azure.PersonalAccounts()){{end}}, nil
}
{{- else if eq .SlotKind "listed"}}
func {{.Field}}Slot(settings *Settings) (azure.TenantSet, error) {
	return parse{{.Pascal}}Tenants(settings.{{.Pascal}}.Tenants, parse{{.Pascal}}Administered(settings.{{.Pascal}}.AdministeredTenants))
}
{{- else}}
func {{.Field}}Slot(settings *Settings) azure.TenantSet {
	administered := parse{{.Pascal}}Administered(settings.{{.Pascal}}.AdministeredTenants)
	sets := make([]azure.TenantSet, 0, len(administered)+{{if eq .SlotKind "any-personal"}}2{{else}}1{{end}})
	sets = append(sets, azure.AnyTenant(){{if eq .SlotKind "any-personal"}}, azure.PersonalAccounts(){{end}})
	for _, id := range administered {
		sets = append(sets, azure.Tenant(id).Attesting())
	}

	return azure.Tenants(sets...)
}
{{- end}}

// parse{{.Pascal}}Administered reads the administered tenants from their setting: the
// tenant ids, comma-separated, lower-cased; none for an empty setting.
func parse{{.Pascal}}Administered(value string) []string {
	var ids []string
	for _, id := range strings.Split(value, ",") {
		if id = strings.ToLower(strings.TrimSpace(id)); id != "" {
			ids = append(ids, id)
		}
	}

	return ids
}
{{- else}}

// {{.Field}}Slot is the registration's organization slot, {{.SlotWords}}: there is no
// organization to administer, so it reads no setting. bedrock reads the registration's
// tenant shape from this function, as it does from every registration's slot function.
func {{.Field}}Slot() azure.TenantSet {
	return azure.PersonalAccounts()
}
{{- end}}
{{- if eq .SlotKind "listed"}}

// parse{{.Pascal}}Tenants reads the listed tenants from their setting: <name>=<tenant id>,
// comma-separated, each declared attesting when its id is among the administered. The
// name is the tenant key every link made through the tenant carries, so it is never
// changed once people have signed in. An administered tenant that is not listed is
// refused.
func parse{{.Pascal}}Tenants(value string, administered []string) (azure.TenantSet, error) {
	var sets []azure.TenantSet
	attesting := map[string]bool{}
	for _, entry := range strings.Split(value, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		name, id, ok := strings.Cut(entry, "=")
		if !ok || name == "" || id == "" {
			return azure.TenantSet{}, errors.Newf("{{.Registration}}: tenant %q is not <name>=<tenant id>", entry)
		}
		id = strings.TrimSpace(id)
		tenant := azure.NamedTenant(id, strings.TrimSpace(name))
		if slices.Contains(administered, strings.ToLower(id)) {
			tenant = tenant.Attesting()
			attesting[strings.ToLower(id)] = true
		}
		sets = append(sets, tenant)
	}
	if len(sets) == 0 {
		return azure.TenantSet{}, errors.New("{{.Registration}}: no tenant listed; set the tenants as <name>=<tenant id>, comma-separated")
	}
	for _, id := range administered {
		if !attesting[id] {
			return azure.TenantSet{}, errors.Newf("{{.Registration}}: the administered tenant %q is not among the tenants listed", id)
		}
	}

	return azure.Tenants(sets...), nil
}
{{- end}}
`

// googleTemplate renders a Google method's file.
const googleTemplate = `package {{.Auth}}

import (
{{- range .StdImports}}
	"{{.}}"
{{- end}}
{{ range .Imports}}
	"{{.}}"
{{- end}}
)

// {{.Registration}} is the name the {{.Name}} registration goes under on the Auth: the
// segment of its login and callback routes (user/{{.Name}}/login), and the connection every
// link made through its Workspace carries. In development the simulated directory signs
// it in as {{.SimulatedUsername}}, or the shared APP_USERNAME when that is unset, with the
// groups {{.SimulatedRoles}} or APP_ROLES name.
const {{.Registration}} = "{{.Name}}"

// {{.SettingsType}} is how the application is known to Google for the {{.Name}}
// registration, read from the environment. Under the session library's skipAuth build tag
// Google is simulated and only the fields the simulated sign-in presents are read.
type {{.SettingsType}} struct {
{{- range .Fields}}
	// {{.Name}} is {{.Doc}}.
	{{.Name}} string
{{- end}}
}

// {{.Pascal}}Login sends the browser to Google, to sign in with an account the
// registration admits.
func (h *Handlers) {{.Pascal}}Login() http.HandlerFunc {
	return h.{{.Field}}.Login()
}

// {{.Pascal}}Callback receives the browser back from Google, resolves the identity,
// applies the account rule and starts the session. Google has no directory-initiated
// logout, so there is no front-channel route: the session's own logout route ends it.
func (h *Handlers) {{.Pascal}}Callback() http.HandlerFunc {
	return h.{{.Field}}.Callback()
}

// register{{.Pascal}} registers the {{.Name}} registration on the Auth, before the first
// request.
{{- if eq .Authority "directory"}}
// The directory's groups are the authority for role membership (google.RoleSync): every
// sign-in reconciles the person's roles to the Google Groups the directory places them in,
// those named by the group prefix, read through the Cloud Identity Groups API with the
// person's own sign-in token as far as the group lookup reaches, removing any role no
// group names; a sign-in in no role group is refused. Hand-assigned roles do not survive
// it, so nothing in the application assigns roles in this store, and the registration's
// declaration on every outlet chains .SyncsRoles(), so the generated management shows
// these people's roles as the directory's and never writes them.
{{- else}}
// The application is the authority for role membership (google.DisableRoleSync): Google
// proves who someone is and the application decides what they may do, so a sign-in
// neither reads nor changes the person's roles.
{{- end}}
{{- if .SyncsRoles}}
// A person Google verifies for the first time becomes a user under their verified
// email, with no password, linked by Google's immutable subject so a renamed account
// stays the same person, holding the roles the directory's groups name: the directory
// decides who the method's people are, so the method has no invite-only or open choice.
{{- else}}
{{- if .SignUpChoice}}
// A person Google verifies for the first time, who has no account, needs an invitation:
// the method is invite only until an administrator opens it to sign-up on the
// application's Sign-in page, and anyone else is refused (not_invited). A person
{{- else if .TenantDirectories}}
// A person Google verifies for the first time, who has no account, needs an invitation,
// or the directory of a tenant that names a default role: the method is invite only and
// is never opened to sign-up, since nobody who signed up to this auth could be given a
// tenant, and anyone else is refused (not_invited). A person
{{- else}}
// A person Google verifies for the first time, who has no account, needs an
// invitation: the method is invite only and is never opened to sign-up, since nobody
// who signed up to this auth could be given a tenant, and anyone else is refused
// (not_invited). A person
{{- end}}
// admitted becomes a user under their verified email, with no password, linked by
// Google's immutable subject so a renamed account stays the same person; a user created
// beforehand under that email is linked and signed in.
{{- end}}
{{- if eq .Slot "any"}}
// The slot admits any Google account, with any Workspace named beside it: a Workspace
// account's identity then carries its Workspace's domain as its connection, which is
// what a tenant's declaration names a Google directory by, and an account outside a
// Workspace carries the provider itself (google.AnyAccountConnection).
{{- end}}
func register{{.Pascal}}(authSession *Session, accessClient *access.Client, settings *Settings) (*google.Method{{if .Fallible}}, error{{end}}) {
{{- if .SlotFallible}}
	workspaces, err := {{.Field}}Slot(settings)
	if err != nil {
		return nil, err
	}
{{- else}}
	workspaces := {{.Field}}Slot(settings)
{{- end}}
{{- if eq .Authority "directory"}}
	lookup, err := google.ParseGroupLookup(settings.{{.Pascal}}.GroupLookup)
	if err != nil {
		return nil, errors.Wrap(err, "google.ParseGroupLookup()")
	}
{{- end}}
	_ = accessClient

	return google.SignIn(authSession, {{.Registration}}, nil, &google.Config{
		ClientID:     settings.{{.Pascal}}.ClientID,
		ClientSecret: settings.{{.Pascal}}.ClientSecret,
		RedirectURL:  settings.{{.Pascal}}.RedirectURL,
		Workspaces:   workspaces,
		RoleSync:     {{.RoleSyncExpr}},
		LoginURL:     settings.LoginURL,
	}){{if .Fallible}}, nil{{end}}
}

// {{.Field}}Slot is the registration's organization slot, {{.SlotWords}}, with the
// Workspaces the deployment administers declared as attesting (AdministeredWorkspaces):
// a sign-in from one carries the Workspace's own second factor, which the deployment
// vouches for, and is not asked for the sign-in code.
{{- if eq .SlotKind "one" "mixed"}}
func {{.Field}}Slot(settings *Settings) (google.WorkspaceSet, error) {
	workspace := google.Workspace({{.Registration}}, settings.{{.Pascal}}.HostedDomain)
	for _, domain := range parse{{.Pascal}}Administered(settings.{{.Pascal}}.AdministeredWorkspaces) {
		if !strings.EqualFold(domain, settings.{{.Pascal}}.HostedDomain) {
			return google.WorkspaceSet{}, errors.Newf("{{.Registration}}: the administered Workspace %q is not the Workspace admitted, %s", domain, settings.{{.Pascal}}.HostedDomain)
		}
		workspace = workspace.Attesting()
	}

	return {{if eq .SlotKind "one"}}workspace{{else}}google.Workspaces(workspace, google.AnyGoogleAccount()){{end}}, nil
}
{{- else if eq .SlotKind "listed"}}
func {{.Field}}Slot(settings *Settings) (google.WorkspaceSet, error) {
	return parse{{.Pascal}}Workspaces(settings.{{.Pascal}}.Workspaces, parse{{.Pascal}}Administered(settings.{{.Pascal}}.AdministeredWorkspaces))
}
{{- else}}
func {{.Field}}Slot(settings *Settings) google.WorkspaceSet {
	administered := parse{{.Pascal}}Administered(settings.{{.Pascal}}.AdministeredWorkspaces)
	sets := make([]google.WorkspaceSet, 0, len(administered)+{{if eq .SlotKind "any"}}2{{else}}1{{end}})
	sets = append(sets, google.AnyWorkspace(){{if eq .SlotKind "any"}}, google.AnyGoogleAccount(){{end}})
	for _, domain := range administered {
		// Named by its domain: the connection a Workspace account's link carries under the
		// open entry, so the links made before the Workspace was declared keep theirs.
		sets = append(sets, google.Workspace(domain, domain).Attesting())
	}

	return google.Workspaces(sets...)
}
{{- end}}

// parse{{.Pascal}}Administered reads the administered Workspaces from their setting: the
// domains, comma-separated, lower-cased; none for an empty setting.
func parse{{.Pascal}}Administered(value string) []string {
	var domains []string
	for _, domain := range strings.Split(value, ",") {
		if domain = strings.ToLower(strings.TrimSpace(domain)); domain != "" {
			domains = append(domains, domain)
		}
	}

	return domains
}
{{- if eq .SlotKind "listed"}}

// parse{{.Pascal}}Workspaces reads the listed Workspaces from their setting:
// <name>=<domain>[+<domain>], comma-separated, each declared attesting when one of its
// domains is among the administered. The name is the connection every link made through
// the Workspace carries, so it is never changed once people have signed in. An
// administered Workspace that is not listed is refused.
func parse{{.Pascal}}Workspaces(value string, administered []string) (google.WorkspaceSet, error) {
	var sets []google.WorkspaceSet
	attesting := map[string]bool{}
	for _, entry := range strings.Split(value, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		name, domains, ok := strings.Cut(entry, "=")
		if !ok || name == "" || domains == "" {
			return google.WorkspaceSet{}, errors.Newf("{{.Registration}}: Workspace %q is not <name>=<domain>[+<domain>]", entry)
		}
		workspace := google.Workspace(strings.TrimSpace(name), strings.Split(domains, "+")...)
		for _, domain := range strings.Split(domains, "+") {
			if slices.Contains(administered, strings.ToLower(domain)) {
				workspace = workspace.Attesting()
				attesting[strings.ToLower(domain)] = true
			}
		}
		sets = append(sets, workspace)
	}
	if len(sets) == 0 {
		return google.WorkspaceSet{}, errors.New("{{.Registration}}: no Workspace listed; set the Workspaces as <name>=<domain>[+<domain>], comma-separated")
	}
	for _, domain := range administered {
		if !attesting[domain] {
			return google.WorkspaceSet{}, errors.Newf("{{.Registration}}: the administered Workspace %q is not among the Workspaces listed", domain)
		}
	}

	return google.Workspaces(sets...), nil
}
{{- end}}
`

// SecondFactorRule is the text of requireSecondFactor, the sign-in rule every auth
// package ends with, as the auth template writes it: a package-level function over the
// Spanner transaction (imported as cloudspanner), sessioninfo and signin, returning
// signin.RequireMFA(sessioninfo.MethodEmailCode). impulse upgrade's second-factor recipe
// and impulse check --fix write it into an auth package that has none.
func SecondFactorRule() string {
	return secondFactorRule
}

// oidcTemplate renders the file of a method that signs in through an organization's own
// identity provider: the session library's generic OpenID Connect method, registered in
// every environment.
const oidcTemplate = `package {{.Auth}}

import (
{{- range .StdImports}}
	"{{.}}"
{{- end}}
{{ range .Imports}}
	"{{.}}"
{{- end}}
)

// {{.Registration}} is the name the {{.Name}} registration goes under on the Auth: the
// segment of its login and callback routes (user/{{.Name}}/login), and the connection every
// link made through the provider carries, so it is never changed once people have signed
// in. In development the simulated provider signs it in as {{.SimulatedUsername}}, or the
// shared APP_USERNAME when that is unset, with the roles {{.SimulatedRoles}} or APP_ROLES
// name.
const {{.Registration}} = "{{.Name}}"

// {{.SettingsType}} is how the application is known to the identity provider for the
// {{.Name}} registration, read from the environment. The provider is one organization's,
// an Okta or a Keycloak for instance, known by its issuer alone. Under the session
// library's skipAuth build tag the provider is simulated and only the fields the simulated
// sign-in presents are read.
type {{.SettingsType}} struct {
{{- range .Fields}}
	// {{.Name}} is {{.Doc}}.
	{{.Name}} string
{{- end}}
}

// {{.Pascal}}Login sends the browser to the identity provider to sign in.
func (h *Handlers) {{.Pascal}}Login() http.HandlerFunc {
	return h.{{.Field}}.Login()
}

// {{.Pascal}}Callback receives the browser back from the provider, resolves the identity,
// applies the account rule and starts the session. The method has no logout the provider
// starts, so there is no front-channel route: the session's own logout route ends it.
func (h *Handlers) {{.Pascal}}Callback() http.HandlerFunc {
	return h.{{.Field}}.Callback()
}

// register{{.Pascal}} registers the {{.Name}} registration on the Auth, before the first
// request. The provider is a standard OpenID Connect issuer: its configuration is read at
// <Issuer>/.well-known/openid-configuration, and every sign-in's token must carry the
// issuer. The configuration names the client's secret, which is also how impulse reads
// this registration as an identity provider's and not the persona method's, a public
// client of the same library method.
{{- if eq .Authority "directory"}}
// The provider is the authority for role membership (signin.RoleSync): every sign-in
// reconciles the person's roles to the role names the ID token carries in its roles
// claim, removing any role the claim does not name; a sign-in naming no role the role
// file defines is refused. The provider has to be set up to send that claim. Hand-assigned
// roles do not survive it, so nothing in the application assigns roles in this store, and
// the registration's declaration on every outlet chains .SyncsRoles(), so the generated
// management shows these people's roles as the provider's and never writes them.
{{- else}}
// The application is the authority for role membership (signin.DisableRoleSync): the
// provider proves who someone is and the application decides what they may do, so a
// sign-in neither reads nor changes the person's roles.
{{- end}}
{{- if .SyncsRoles}}
// Everyone the provider signs in is a member of its organization and becomes a user
// under the name the provider presents (the preferred username, or the email when it
// sends none), with no password, linked by the provider's subject so a renamed account
// stays the same person, holding the roles the token names: the provider decides who
// the method's people are, so the method has no invite-only or open choice.
{{- else}}
// A person the provider verifies for the first time, who has no account, needs an
{{- if .SignUpChoice}}
// invitation: the method is invite only until an administrator opens it to sign-up on
// the application's Sign-in page, and anyone else is refused (not_invited). A person
{{- else if .TenantDirectories}}
// invitation, or the directory of a tenant that names a default role: the method is
// invite only and is never opened to sign-up, since nobody who signed up to this auth
// could be given a tenant, and anyone else is refused (not_invited). A person
{{- else}}
// invitation: the method is invite only and is never opened to sign-up, since nobody
// who signed up to this auth could be given a tenant, and anyone else is refused
// (not_invited). A person
{{- end}}
// admitted becomes a user under the name the provider presents (the preferred username,
// or the email when it sends none), with no password, linked by the provider's subject
// so a renamed account stays the same person; a user created beforehand under that name
// is linked and signed in.
{{- end}}
// A sign-in whose token says a second factor was used (its amr claim names mfa) carries
// that proof; any other is asked for the sign-in code the Auth mails.
func register{{.Pascal}}(authSession *Session, accessClient *access.Client, settings *Settings) *oidc.Method {
	_ = accessClient

	return oidc.SignIn(authSession, {{.Registration}}, nil, &oidc.Config{
		Issuer:       settings.{{.Pascal}}.Issuer,
		ClientID:     settings.{{.Pascal}}.ClientID,
		ClientSecret: settings.{{.Pascal}}.ClientSecret,
		RedirectURL:  settings.{{.Pascal}}.RedirectURL,
		Connection:   {{.Registration}},
		RoleSync:     {{.RoleSyncExpr}},
		LoginURL:     settings.LoginURL,
	})
}
`

// personaTemplate renders the persona method's file: the session library's generic OpenID
// Connect method against the persona provider, registered only where the provider is
// configured.
const personaTemplate = `package {{.Auth}}

import (
{{- range .StdImports}}
	"{{.}}"
{{- end}}
{{ range .Imports}}
	"{{.}}"
{{- end}}
)

// {{.Registration}} is the name the {{.Name}} registration goes under on the Auth: the
// segment of its login and callback routes (user/{{.Name}}/login), and the connection every
// link made through the persona provider carries. In development the simulated provider
// signs it in as {{.SimulatedUsername}}, or the shared APP_USERNAME when that is unset, with
// the roles {{.SimulatedRoles}} or APP_ROLES name.
const {{.Registration}} = "{{.Name}}"

// {{.SettingsType}} is how the application is known to the persona provider for the
// {{.Name}} registration, read from the environment. The provider is configured in the
// first environment and its pull requests alone: an empty Issuer leaves the method
// unregistered, so a later environment never offers it, and nothing in the build differs
// between them. Under the session library's skipAuth build tag the provider is simulated
// and only the fields the simulated sign-in presents are read.
type {{.SettingsType}} struct {
{{- range .Fields}}
	// {{.Name}} is {{.Doc}}.
	{{.Name}} string
{{- end}}
}

// {{.Pascal}}Login sends the browser to the persona provider, where the person picks a
// persona of this deployment's cast, or is signed in straight away as the persona the
// login_hint query parameter names. Nil where the method is not registered: the generated
// router mounts the route and tells the login page of the method only where it is.
func (h *Handlers) {{.Pascal}}Login() http.HandlerFunc {
	if h.{{.Field}} == nil {
		return nil
	}

	return h.{{.Field}}.Login()
}

// {{.Pascal}}Callback receives the browser back from the provider, resolves the persona's
// identity, links it to the seeded account of the same login and starts the session. Nil
// where the method is not registered.
func (h *Handlers) {{.Pascal}}Callback() http.HandlerFunc {
	if h.{{.Field}} == nil {
		return nil
	}

	return h.{{.Field}}.Callback()
}

// register{{.Pascal}} registers the {{.Name}} registration on the Auth, before the first
// request, where the provider is configured, and answers nil where it is not: the issuer
// is a placement value of the first environment and its pull requests, so a later
// environment never registers the method. The provider is a standard OpenID Connect
// issuer and the application a public client: no secret, and PKCE binds the code to the
// login that asked for it. The application is the authority for role membership
// (signin.DisableRoleSync): the provider says which persona signed in, and the cast the
// migrate command seeded says what the persona may do. The provider attests (Attesting): the person signed in to the provider's host to
// pick a persona, which is the whole proof in a test environment, so a persona sign-in is
// never asked for the sign-in code. The method is left invite only, and declares seeded
// users (SeededUsers):
// the persona's identity (its login as the subject) is linked to the account seeded
// under that login (cast.json, SeedCast), and a persona the seed does not know is refused.
// The declaration takes the provider's names for the application's own: a seeded account
// has no password and has never signed in, so nothing but its login ties it to a sign-in,
// and without it an application whose tenants have directories links such an account
// only through the directory of the one tenant it belongs to, or with its own
// invitation, and a persona holds its roles in every tenant or in none.
func register{{.Pascal}}(authSession *Session, accessClient *access.Client, settings *Settings) *oidc.Method {
	_ = accessClient
	if settings.{{.Pascal}}.Issuer == "" {
		return nil
	}

	return oidc.SignIn(authSession, {{.Registration}}, nil, &oidc.Config{
		Issuer:      settings.{{.Pascal}}.Issuer,
		ClientID:    settings.{{.Pascal}}.ClientID,
		RedirectURL: settings.{{.Pascal}}.RedirectURL,
		Connection:  {{.Registration}},
		Attesting:   true,
		RoleSync:    {{.RoleSyncExpr}},
		SeededUsers: true,
		LoginURL:    settings.LoginURL,
	})
}
`

// castTemplate renders the persona method's cast.go: the cast file embedded, read for the
// deploy's registration and seeded by the migrate command.
const castTemplate = `package {{.Auth}}

import (
	"context"
	_ "embed" // the cast rides in the binary
	"fmt"
	"io"
	"slices"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/cast"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session"
)

// castFile is the cast of the persona sign-in: the personas a test environment seeds and
// the persona provider offers, declared beside this package in cast.json (login, name,
// address and roles, each role one the role file defines or the framework's Super
// Administrator). The deploy registers the cast
// with the provider after traffic (bedrock deploy persona register), and the migrate command
// seeds its accounts in the environments that take the seed (SeedCast).
//
//go:embed cast.json
var castFile []byte

// Cast reads the cast file.
func Cast() (*cast.Cast, error) {
	read, err := cast.ReadCast(castFile)
	if err != nil {
		return nil, errors.Wrap(err, "cast.ReadCast(): cast.json")
	}

	return read, nil
}

// SeedCast seeds the cast file this package embeds (SeedCastFile over cast.json): the
// migrate command calls it in the environments that take the seed, before it invites a
// first administrator, and out is where it says each role it gives.
func (a *Auth) SeedCast(ctx context.Context, out io.Writer) (int, error) {
	return a.SeedCastFile(ctx, out, castFile)
}

// SeedCastFile seeds the cast a cast file declares, read and checked as cast.json is
// (cast.ReadCast): a password-less user under each persona's login, with the persona's
// name and address, holding the roles its entry names, a global role in the global
// partition and a domain role in every tenant domain, as the first administrator's
// roles are; a persona its entry names a super administrator holds the framework's role
// (accesstypes.SuperAdministrator) in the global partition. A role is held by the id of
// an account, so the seed chooses the id of a persona's account (session.SeedUserID, the
// same on every run), writes the roles under it, and creates the user under it last: a
// run that stops between the two is repeated at the next deploy.
//
// A persona seeded earlier is brought up to its entry: the account under the id the seed
// chose, whatever login it carries now, is given every role the entry names that it does
// not hold, so a role added to the cast reaches the environments seeded before it at the
// next deploy, and a persona added to the cast arrives then too. Nothing is taken: a
// role that left the cast stays on the account until somebody removes it. A role the
// account holds now, of its own or through a group, is left as it is, the period it is
// held for included, so a run over an unchanged cast writes nothing; a role it held for
// a period that has ended or has not begun is not held, and is given with no period. A
// user of another id who holds a persona's login is not the seed's: it is given nothing,
// since the seed never gives a role to an account because of the name it carries.
//
// It says each role it gives on out, one line each, and nothing where there is nothing to
// give, and answers how many users it created; with no writer it refuses before it reads
// or writes anything (io.Discard says nothing). The persona sign-in links the provider's
// identity to the user of the same login at the first sign-in (the method is invite
// only, as every sign-in method starts, and declares seeded users), so a persona the
// seed never made is refused there. SeedCast is this over the package's own cast file; a
// suite that changes a cast between two runs hands it each.
func (a *Auth) SeedCastFile(ctx context.Context, out io.Writer, file []byte) (int, error) {
	if out == nil {
		return 0, errors.New("the cast's seed was handed no writer to say the roles it gives on: hand it the command's output, or io.Discard to say nothing")
	}
	declared, err := cast.ReadCast(file)
	if err != nil {
		return 0, errors.Wrap(err, "cast.ReadCast()")
	}
	roles, err := Roles().Parse()
	if err != nil {
		return 0, errors.Wrap(err, "access.RoleFile.Parse()")
	}
	api := a.handlers.API()
	created := 0
	for i := range declared.Personas {
		p := &declared.Personas[i]
		id := session.SeedUserID(Name, p.Login)
		seeded, taken, err := a.personaAccount(ctx, p.Login, id)
		if err != nil {
			return created, err
		}
		if taken {
			continue
		}
		if err := a.seatPersona(ctx, out, roles, p, id); err != nil {
			return created, err
		}
		if seeded {
			continue
		}
		req := &session.CreateUserRequest{ID: impulse.NullUUIDFromUUID(id), Username: p.Login}
		if p.Email != "" {
			req.Email = &p.Email
		}
		if p.Name != "" {
			req.Name = &p.Name
		}
		if _, err := api.CreateSessionUser(ctx, req); err != nil {
			return created, errors.Wrapf(err, "session.AuthAPI.CreateSessionUser(): %s", p.Login)
		}
		created++
	}

	return created, nil
}

// personaAccount reports what stands where a persona's account goes: seeded, when the
// account the seed made for it exists, the one under the id the seed chose, under the
// persona's login or, for a persona renamed since, under another; and taken, when there
// is no such account and a user of another id holds the persona's login, an account the
// seed did not make and leaves as it is.
func (a *Auth) personaAccount(ctx context.Context, login string, id impulse.UUID) (seeded, taken bool, err error) {
	api := a.handlers.API()
	if _, err := api.User(ctx, id); err == nil {
		return true, false, nil
	} else if !httpio.HasNotFound(err) {
		return false, false, errors.Wrapf(err, "session.AuthAPI.User(): the account the seed chose for %s", login)
	}
	if _, err := api.UserByUsername(ctx, login); err == nil {
		return false, true, nil
	} else if !httpio.HasNotFound(err) {
		return false, false, errors.Wrapf(err, "session.AuthAPI.UserByUsername(): %s", login)
	}

	return false, false, nil
}

// seatPersona brings a persona's account up to the roles its entry names, each role
// where the role file holds it: a global role in the global partition, a domain role in
// every tenant domain. The Super Administrator role is the framework's, in no role file,
// and is held in the global partition. Any other role the file does not define is
// refused, since the seed names nothing the release does not.
func (a *Auth) seatPersona(ctx context.Context, out io.Writer, roles *access.RoleConfig, p *cast.Persona, userID impulse.UUID) error {
	var global, everyDomain []accesstypes.Role
	for _, name := range p.Roles {
		role := accesstypes.Role(name)
		switch {
		case role == accesstypes.SuperAdministrator, definesRole(roles.Roles.Global, role):
			global = append(global, role)
		case definesRole(roles.Roles.Domain, role):
			everyDomain = append(everyDomain, role)
		default:
			return errors.Newf("cast.json: the persona %s holds the role %q, which the %s auth's role file does not define", p.Login, name, Name)
		}
	}
	user := accesstypes.User(userID.String())
	if err := a.givePersona(ctx, out, p.Login, user, accesstypes.GlobalPolicyScope(), global, ""); err != nil {
		return err
	}

	return a.givePersona(ctx, out, p.Login, user, accesstypes.EveryDomainPolicyScope(), everyDomain, " in every tenant domain")
}

// givePersona gives a persona's account the roles its entry names in one place that the
// account does not hold there now, and says each on out, a domain role with the words
// that it is held in every tenant domain. A role the account holds, of its own or through a
// group, is not written again, so it keeps the period it is held for; nothing the entry
// does not name is touched.
func (a *Auth) givePersona(ctx context.Context, out io.Writer, login string, user accesstypes.User, scope accesstypes.PolicyScope, named []accesstypes.Role, where string) error {
	if len(named) == 0 {
		return nil
	}
	manager := a.access.UserManager()
	held, err := manager.UserRoles(ctx, user, scope)
	if err != nil {
		return errors.Wrapf(err, "access.UserManager.UserRoles(): %s", login)
	}
	holds := held[scope]
	for _, role := range named {
		if slices.Contains(holds, role) {
			continue
		}
		if err := manager.AddUserRoles(ctx, scope, user, role); err != nil {
			return errors.Wrapf(err, "access.UserManager.AddUserRoles(): %s, the role %s%s", login, role, where)
		}
		holds = append(holds, role)
		fmt.Fprintf(out, "Gave the %s auth's persona %s the role %s%s\n", Name, login, role, where)
	}

	return nil
}

// definesRole reports whether the role file defines the role at the scope.
func definesRole(defined []*access.Role, role accesstypes.Role) bool {
	for _, r := range defined {
		if r.Name == role {
			return true
		}
	}

	return false
}
`
