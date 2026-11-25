package signin

import (
	"context"
	"slices"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// Directory is a directory a tenant admits its people through: a sign-in method and
// the connection its identities carry, the values of Identity.Method and
// Identity.Connection, which the session's first auth event records too. A Microsoft
// Entra tenant is named by its tenant id under MethodAzure, a Google Workspace by the
// name the registration gives it under MethodGoogle, a WorkOS connection by its id
// under MethodWorkOS.
type Directory struct {
	Method     sessioninfo.AuthMethod
	Connection string
}

// DeclaredDirectory is one tenant's declaration of a directory, as a DirectoryReader
// answers it for a sign-in through the directory: the tenant, and the default seat the
// tenant gives a person its directory vouched for who holds nothing in the tenant, the
// role the sign-in seats them with in the tenant at every sign-in through the
// directory, or "" when the tenant admits its people by invitation only, and
// whether the tenant says the directory asks for a second factor of its own.
type DeclaredDirectory struct {
	Tenant      accesstypes.Domain
	DefaultSeat accesstypes.Role
	// SecondFactor says the tenant has said its directory asks its people for a second
	// factor of its own. A sign-in through the directory is then not held for the
	// mailed code the Auth's sign-in settings ask of everyone, wherever the person
	// works; a hold a sign-in policy answers stands, and the word is not read at a
	// sign-in through a method whose directory writes the roles.
	SecondFactor bool
}

// DirectoryReader reads every tenant's declaration of a directory for a sign-in through
// it, in the sign-in's first transaction, typed to the storage's transaction like the
// other hooks: the tenants that declare the directory the identity came through, each
// with its default seat, which is offered at every sign-in through the directory by an
// account that is not disabled, a stranger's first and a linked identity's alike. A
// directory some tenant declares
// belongs to that tenant, and its declaration decides the stranger before the
// registration's account rule does; a directory no tenant declares, an empty answer,
// falls to the registration's rule as it would without the reader. The library asks
// the reader again at each sign-in, in the transaction that decides it: for the
// directories of the account's identity links, which the sign-in policy is told
// (sessioninfo.SignInUser.Directories), and, where the Auth's sign-in settings would
// hold the sign-in for the mailed code, for the word of the tenant whose directory the
// sign-in came through (DeclaredDirectory.SecondFactor). The library orders nothing
// between that read and the application's write of the word: a reader that must not
// let a sign-in in flight commit on a word taken back meanwhile reads the row under a
// lock that write waits for, as the library's PostgreSQL driver reads its own sign-in
// settings row (FOR SHARE). Spanner may retry the transaction, so the reader must not
// have side effects outside it. Set it on the Auth with SetDirectoryAdmission, beside
// the hook that seats the person.
type DirectoryReader[Tx any] func(ctx context.Context, tx Tx, directory Directory) ([]DeclaredDirectory, error)

// DirectorySeat is a seat a sign-in through a directory offers: a role in a tenant,
// the default seat of a directory the tenant declared, written by the application's
// hook where the person holds nothing in the tenant.
type DirectorySeat struct {
	Tenant accesstypes.Domain
	Role   accesstypes.Role
}

// TenantDeclaration is what a tenant declares about who may enter it: the directory
// its people come through, and whether they come through it only. It is
// configuration data of the tenant, kept by the application and read for the library
// by a DeclarationReader. A tenant's declaration restricts that tenant's own door, and,
// while the tenant is directory only, how the accounts of its own directory sign in: a
// tenant that declares nothing, a nil or an empty declaration, admits everyone it seats,
// however they signed in.
type TenantDeclaration struct {
	// Directories are the directories the tenant admits its people through. A tenant
	// has one directory, and a directory is one tenant's: the application's schema and
	// its write paths hold both, so the list carries one directory at most. The
	// directory seats a person who signs in through it and holds nothing in the tenant,
	// at every sign-in (DeclaredDirectory); it closes the tenant to nobody until the
	// tenant is directory only.
	Directories []Directory
	// DirectoryOnly says the tenant's people enter through its directory only: a
	// password, a personal account, any other sign-in that is not through a directory,
	// and a sign-in through another organization's directory do not reach the tenant.
	// The tenant's own administrators alone keep a way in beside the directory, with a
	// password and a second factor (AdmitsAdministrator). An account that signs in
	// through the tenant's directory is then refused every other sign-in, whichever
	// tenants seat the person (TenantPolicy, rule 2).
	DirectoryOnly bool
	// Disabled says the tenant is disabled: its people are shut out while its records
	// stay. A disabled tenant seats nobody at a sign-in (TenantPolicy), and no session
	// enters it but a super administrator's. It changes nothing else the tenant
	// declares: its directory and its directory-only setting stand, and decide again
	// once the tenant is enabled.
	Disabled bool
}

// IsDisabled reports whether the tenant is disabled. A nil declaration declares nothing
// and is not disabled. The tenant policy asks it at sign-in, and the application asks it
// again when the session reaches one tenant, each before Admits or AdmitsAdministrator,
// which say what the tenant's sign-in settings admit and nothing of whether the tenant
// is disabled.
func (d *TenantDeclaration) IsDisabled() bool {
	return d != nil && d.Disabled
}

// Admits reports whether the declaration admits a session established through method
// and connection, as the session's first auth event records them
// (sessioninfo.SessionData.EstablishedBy). A tenant that is not directory only admits
// every establishment: a password, a personal account, a step the application recorded
// under its own name, and a sign-in through any organization's directory, the tenant's
// own or another's. A tenant that is directory only admits a sign-in through its own
// directory, the (method, connection) it declares, and nothing else, but for its own
// administrators, who are asked of AdmitsAdministrator. A personal
// account (Directory.PersonalAccounts: any Google account, a Microsoft personal
// account) is no directory, which no tenant can declare, so a directory-only tenant
// refuses it as it refuses a password. The
// tenant policy decides with it at sign-in, over every tenant of the person, and the
// application decides with it again when the session reaches one tenant, where the
// session's tenant list is computed. It reads nothing: a nil declaration declares
// nothing.
func (d *TenantDeclaration) Admits(method sessioninfo.AuthMethod, connection string) bool {
	if d == nil || !d.DirectoryOnly {
		return true
	}

	return throughDirectory(method, connection) && d.declares(Directory{Method: method, Connection: connection})
}

// AdmitsAdministrator reports whether the declaration admits a session by the
// administrators' way in: a tenant that is directory only still admits its own
// administrators, so a tenant whose directory is broken, or whose settings went wrong,
// is not a tenant nobody can mend. It holds when the tenant is directory only, the
// session was established by the password method (method), the session carries a second
// factor (secondFactor: the mailed code, or a remembered browser standing in for it,
// sessioninfo.SessionData.SecondFactor), and the person administers the tenant
// (administers): they hold one of the roles the application names as a tenant's
// administrator's in the tenant itself, inside its period, by a membership of their own
// or through a group kept in that tenant (the access library's
// Controller.UserAdministersTenant). A role held in every tenant, or through a group
// kept for the whole application, is no such holding, so the application's own people
// are kept out with everyone else. An administrator who signed in through another
// organization's directory or with a personal account is kept out too: the way in is
// the password, and nothing wider. It is the one decision both places ask: the tenant
// policy at sign-in, with the second factor it is about to demand taken as given, and
// the application when the session reaches the tenant, with the session's own. It
// opens no other tenant: the same session reaches the person's other tenants by those
// tenants' own declarations.
func (d *TenantDeclaration) AdmitsAdministrator(method sessioninfo.AuthMethod, secondFactor, administers bool) bool {
	return d != nil && d.DirectoryOnly && method == sessioninfo.MethodPassword && secondFactor && administers
}

// throughDirectory reports whether a sign-in by method through connection came through
// a directory a tenant can declare: a directory method, and not one of the two values
// that stand for everyone who is in no organization.
func throughDirectory(method sessioninfo.AuthMethod, connection string) bool {
	return method.ThroughDirectory() && !(Directory{Method: method, Connection: connection}).PersonalAccounts()
}

// declares reports whether directory is one of the declared directories.
func (d *TenantDeclaration) declares(directory Directory) bool {
	return d != nil && slices.Contains(d.Directories, directory)
}

// DeclarationReader reads a tenant's declaration for the tenant policy, in the
// sign-in's transaction, typed to the storage's transaction like the other hooks
// (pgx.Tx on PostgreSQL, *spanner.ReadWriteTransaction on Spanner). It answers nil for
// a tenant that declares nothing. Spanner may retry the transaction, so the reader
// must not have side effects outside it.
type DeclarationReader[Tx any] func(ctx context.Context, tx Tx, tenant accesstypes.Domain) (*TenantDeclaration, error)

// TenantPolicyOption refines the tenant policy. PasswordsDisabledByTenants takes none,
// since no option changes whether a sign-in with a password is refused as directory
// only: an option that could must be taken by it too, or the check and the policy part.
type TenantPolicyOption func(settings *tenantPolicySettings)

// tenantPolicySettings are the tenant policy's options.
type tenantPolicySettings struct {
	admitWithoutTenant bool
}

// AdmitWithoutTenant admits a person who is a member of no tenant instead of refusing
// them (no_tenant). The self-service shape of an application needs it: a stranger
// signs in, then creates their organization, whose first membership the application
// writes; until then the session reaches no tenant.
func AdmitWithoutTenant() TenantPolicyOption {
	return func(settings *tenantPolicySettings) {
		settings.admitWithoutTenant = true
	}
}

// tenantPolicy is the tenant policy's state: the role store the memberships are read
// from, the reader of a tenant's declaration, and the options.
type tenantPolicy[Tx any] struct {
	roles        UserRoleManager
	declarations DeclarationReader[Tx]
	settings     tenantPolicySettings
}

// TenantPolicy is the library's sign-in rule for an application whose users belong to
// tenants: a person signs in when one of their tenants admits the way they signed in.
// The person's tenants are the domains where they hold a role, read through roles, the
// access library's user manager, and what each tenant admits is its declaration
// (TenantDeclaration), read through declarations in the sign-in's transaction. A
// tenant's declaration restricts that tenant's door, and a directory-only tenant also
// keeps the accounts that sign in through its directory to that directory (rule 2);
// beyond those accounts the policy refuses a person only when every tenant that seats
// them is closed to the way they signed in. The policy decides what the session may
// be; which of the person's tenants the session then reaches is decided again, tenant
// by tenant, where the session's tenant list is computed, with
// TenantDeclaration.Admits over the session's first auth event, and with
// TenantDeclaration.AdmitsAdministrator for the tenant's own administrators. It
// never writes. Set it with session.Auth.SetSignInPolicy; Then chains the
// application's own rule after it, which still refuses whom it refuses.
//
// A tenant that is disabled (TenantDeclaration.Disabled) seats nobody. The policy
// passes it over wherever it reads the tenants a person holds or administers, so a
// person whose only tenant is disabled is refused as a member of no tenant is, and a
// person with another tenant signs in by what that tenant declares. Being disabled
// changes nothing else: a disabled tenant that is directory only still keeps the
// accounts of its directory to that directory (rule 2).
//
// The rules, in order, once the user is known:
//
//  1. A super administrator, a person holding the Super Administrator role
//     (accesstypes.RoleCollection.HoldsSuperAdministrator), is admitted on the
//     library's own checks, whatever any tenant declares: the role is the way back in,
//     and no tenant's sign-in settings shut its holder out. A sign-in whose method
//     synchronizes roles is admitted here too (the directory names the person's roles,
//     and the synchronization, which runs once the session stands, refuses a person
//     with none); the application's own directories are admitted by the registration's
//     organization slot already.
//  2. A tenant that is directory only keeps the accounts that sign in through its
//     directory to that directory. An account that holds an identity link through the
//     directory of a directory-only tenant (sessioninfo.SignInUser.Directories, which
//     the session library reads with the reader set with
//     session.Auth.SetDirectoryAdmission) is refused every sign-in that tenant's
//     declaration does not admit, whichever tenants the person holds roles in, a role
//     outside any one tenant included: a password, a personal account, a session the
//     application starts itself and a sign-in through another organization's
//     directory, each with the code rule 7 gives it. The tenant's own administrators
//     are the exception: a person who administers that tenant and signs in with a
//     password is held for the mailed code, as in rule 3; an administrator of another
//     tenant is not. Neither is an administrator of a tenant that is disabled. Nothing
//     is taken from the account: with the tenant's directory
//     only turned off, the password signs the person in again. An Auth that sets no
//     directory admission hands the policy no directories, and this rule then refuses
//     nobody.
//  3. A tenant's own administrators keep a way into their tenant: a person who signs
//     in with a password and administers a tenant that is directory only
//     (UserRoleManager.TenantsAdministered, TenantDeclaration.AdmitsAdministrator) is
//     not refused, and the sign-in is held for the library's second factor, the code
//     it mails (RequireMFA with sessioninfo.MethodEmailCode), whatever else would have
//     admitted it and though the application's own rule would hold nobody: the session
//     reaches that tenant only while it carries a second factor, so one is asked at
//     the sign-in. A rule chained after this one (Then) that holds the sign-in for a
//     factor of its own has its hold stand in this one's place, and the session then
//     carries that factor. A browser the person asked to be remembered passes it, for the
//     period the library remembers one. Nobody but a super administrator and a
//     tenant's own administrators, in their own tenant, enters a directory-only tenant
//     another way than through its directory: a person who holds the administrator's
//     role in every tenant, or through a group kept for the whole application, is
//     held to the declaration like anyone, and so is an administrator who signed in
//     through another organization's directory or with a personal account. A tenant
//     that is disabled gives no such way in.
//  4. A person holding a role outside any one tenant is admitted: in the global
//     partition, which is no tenant's and which no declaration governs, or in every
//     tenant domain, whose tenants the policy has no list of. It is no exemption: the
//     session then reaches a tenant only where the tenant's declaration admits it, as
//     anyone's does.
//  5. A person who is a member of no tenant, or whose every tenant is disabled, is
//     refused (no_tenant), unless the policy was built with AdmitWithoutTenant.
//  6. The sign-in is admitted when one of the person's tenants admits it
//     (TenantDeclaration.Admits): a tenant that is not directory only admits every
//     sign-in, a password, a personal account, a session the application starts for a
//     user it authenticated itself, and a sign-in through any directory; a tenant that
//     is directory only admits a sign-in through its own directory alone.
//  7. Otherwise every tenant of the person is directory only and none has the way they
//     signed in as its directory: nothing would be reachable, and the sign-in is
//     refused. A sign-in through a directory (Entra, Google, WorkOS, an OpenID Connect
//     provider) is refused with connection_not_in_tenant, a sign-in with a personal
//     account (through Google or Microsoft, with an account that is in no
//     organization's directory) with directory_only_personal_account, a password with
//     directory_only, and any other, a session the application starts itself, with
//     directory_only_session. The codes are apart so that a login page says what is
//     true of how the person signed in: a person who used no password is not told to
//     stop using one.
//
// A refusal is a sessioninfo.LoginRefusal with its code, and errors.Is matches
// ErrSignInDenied and the rule's own cause (ErrNoTenant, ErrConnectionNotInTenant,
// ErrDirectoryOnly). A role store or reader error fails the sign-in (internal_error).
// A nil roles or declarations is a wiring mistake and panics.
//
// A stranger, a person no user was created for, never reaches the policy as such: the
// account rules decide them first, and in a tenanted application the tenants'
// declarations decide before the registration's rule, through the directory reader
// and seat hook set with session.Auth.SetDirectoryAdmission (DirectoryReader): a
// default seat admits and seats the person, so the policy then finds the membership,
// and a declaration without one refuses them by invitation only, unless they carry
// another tenant's invitation, which seats them in the tenant that invited them.
//
// PasswordsDisabledByTenants asks the same rules outside a sign-in, for one person: whether
// a password would be refused them as directory_only.
func TenantPolicy[Tx any](roles UserRoleManager, declarations DeclarationReader[Tx], opts ...TenantPolicyOption) Policy[Tx] {
	if roles == nil {
		panic("signin: TenantPolicy was given no role store: pass the access library's user manager")
	}
	if declarations == nil {
		panic("signin: TenantPolicy was given no declaration reader: pass the application's reader of a tenant's declaration")
	}
	p := &tenantPolicy[Tx]{roles: roles, declarations: declarations}
	for _, opt := range opts {
		opt(&p.settings)
	}

	return p.decide
}

// PasswordDisabledCheck answers whether the user's password is disabled: whether a
// sign-in of theirs with a password would be refused because the tenants' sign-in
// settings refuse it, whether or not they hold one. directories are the directories
// the user's account signs in through (sessioninfo.SignInUser.Directories). A page
// asks it to know whether to offer a password at all. PasswordsDisabledByTenants
// builds the library's own.
type PasswordDisabledCheck func(ctx context.Context, userID impulse.UUID, directories []sessioninfo.UserDirectory) (bool, error)

// outsideSignIn is the transaction the tenant policy is asked under outside a sign-in:
// there is none, and the reader PasswordsDisabledByTenants is given takes none.
type outsideSignIn struct{}

// PasswordsDisabledByTenants is the tenant policy's own decision for a sign-in with a
// password, asked for one person outside any sign-in: the check answers true exactly
// when TenantPolicy, over the same role store and the same declarations, would refuse
// that person's sign-in with a password as directory_only, so when a directory-only
// tenant's directory signs their account in and they do not administer that tenant, or
// when every tenant that seats them is directory only. It answers false for a super
// administrator, for an administrator of a directory-only tenant of theirs (the
// administrators' way in, held for the mailed code), for a person who holds a role
// outside any one tenant, for a person a tenant that is not directory only seats and
// whose account no directory-only tenant's directory signs in, and for a person who is
// a member of no tenant (no_tenant is another refusal, and with AdmitWithoutTenant no
// refusal). It is decide itself, asked with a password identity, not a second statement
// of the rules, so the answer follows the policy wherever the policy changes. The Auth
// hands the check the account's directories (sessionstorage.AuthStore.UserDirectories),
// as a sign-in hands them to the policy.
//
// roles is the access library's user manager, as for TenantPolicy; declarations reads
// a tenant's declaration with no sign-in transaction, answering nil for a tenant that
// declares nothing (on Spanner resource.OutsideSignIn over the sign-in's reader). The
// check reads the person's memberships and their tenants' declarations and never
// writes. It answers for the tenant policy alone: a rule chained after it (Then) is
// not asked, since a chained rule is never asked outside a sign-in. An error of the
// role store or the reader is returned as the policy returns it. Set it with
// session.Auth.SetPasswordDisabledCheck. A nil roles or declarations is a wiring
// mistake and panics.
func PasswordsDisabledByTenants(
	roles UserRoleManager,
	declarations func(ctx context.Context, tenant accesstypes.Domain) (*TenantDeclaration, error),
) PasswordDisabledCheck {
	if roles == nil {
		panic("signin: PasswordsDisabledByTenants was given no role store: pass the access library's user manager")
	}
	if declarations == nil {
		panic("signin: PasswordsDisabledByTenants was given no declaration reader: pass a reader of a tenant's declaration " +
			"that needs no transaction (on Spanner, resource.OutsideSignIn over the sign-in's reader)")
	}
	p := &tenantPolicy[outsideSignIn]{
		roles: roles,
		declarations: func(ctx context.Context, _ outsideSignIn, tenant accesstypes.Domain) (*TenantDeclaration, error) {
			return declarations(ctx, tenant)
		},
	}

	return func(ctx context.Context, userID impulse.UUID, directories []sessioninfo.UserDirectory) (bool, error) {
		req := &sessioninfo.NewSessionRequest{
			UserID:   userID,
			User:     &sessioninfo.SignInUser{ID: userID, Directories: directories},
			Identity: &sessioninfo.Identity{Method: sessioninfo.MethodPassword},
		}
		decision, err := p.decide(ctx, outsideSignIn{}, req)
		if err != nil {
			return false, err
		}

		return decision.Outcome() == DenySignIn && decision.Refusal() == sessioninfo.RefusedDirectoryOnly, nil
	}
}

// decide is the policy: see TenantPolicy for the rules.
func (p *tenantPolicy[Tx]) decide(ctx context.Context, tx Tx, req *sessioninfo.NewSessionRequest) (*Decision, error) {
	if req.RoleNames != nil {
		return Allow(), nil
	}
	held, err := p.roles.UserRoles(ctx, accesstypes.User(req.UserID.String()))
	if err != nil {
		return nil, errors.Wrap(err, "UserRoleManager.UserRoles()")
	}
	if held.HoldsSuperAdministrator() {
		return Allow(), nil
	}
	method, connection := establishment(req)
	reads := newDecisionReads(p, tx, req.UserID)
	kept, err := reads.keptToDirectory(ctx, req.User, method, connection)
	if err != nil {
		return nil, err
	}
	if kept != nil {
		return kept, nil
	}
	enters, err := reads.administratorsWayIn(ctx, method)
	if err != nil {
		return nil, err
	}
	if enters {
		return RequireMFA(sessioninfo.MethodEmailCode), nil
	}
	outside, tenants := membership(held)
	if outside {
		return Allow(), nil
	}

	seated := false
	for _, tenant := range tenants {
		declaration, err := reads.declaration(ctx, tenant)
		if err != nil {
			return nil, err
		}
		if declaration.IsDisabled() {
			continue
		}
		seated = true
		if declaration.Admits(method, connection) {
			return Allow(), nil
		}
	}
	if !seated {
		if p.settings.admitWithoutTenant {
			return Allow(), nil
		}

		return denyBecause(sessioninfo.RefusedNoTenant, ErrNoTenant), nil
	}

	return closedTo(method, connection), nil
}

// closedTo is the refusal of a sign-in that came through none of the directories the
// tenants keep their people to, with the code that is true of how the person signed
// in: connection_not_in_tenant through an organization's directory,
// directory_only_personal_account with a personal account, directory_only for a
// password, and directory_only_session for anything else, a session the application
// starts itself.
func closedTo(method sessioninfo.AuthMethod, connection string) *Decision {
	if throughDirectory(method, connection) {
		return denyBecause(sessioninfo.RefusedConnectionNotInTenant, ErrConnectionNotInTenant)
	}
	// A directory method that came through no directory a tenant can declare is a
	// personal account: the person used no password, and the code says so.
	if method.ThroughDirectory() {
		return denyBecause(sessioninfo.RefusedDirectoryOnlyPersonalAccount, ErrDirectoryOnly)
	}
	if method == sessioninfo.MethodPassword {
		return denyBecause(sessioninfo.RefusedDirectoryOnly, ErrDirectoryOnly)
	}

	// The person typed no password: the code says so.
	return denyBecause(sessioninfo.RefusedDirectoryOnlySession, ErrDirectoryOnly)
}

// membership reads the person's memberships: outside when a role is held outside any
// one tenant, in the global partition or in every tenant domain, and the tenants where
// a role is held, in a fixed order, so the declarations are read the same way at every
// sign-in.
func membership(held accesstypes.RoleCollection) (outside bool, tenants []accesstypes.Domain) {
	for scope, roles := range held {
		if len(roles) == 0 {
			continue
		}
		if scope.IsGlobal() || scope.IsEveryDomain() {
			outside = true

			continue
		}
		if domain, ok := scope.Domain(); ok && domain != "" {
			tenants = append(tenants, domain)
		}
	}
	slices.Sort(tenants)

	return outside, tenants
}

// establishment is how the sign-in proves who the person is: the identity's method
// and connection, which the session's first auth event will record.
func establishment(req *sessioninfo.NewSessionRequest) (method sessioninfo.AuthMethod, connection string) {
	if req.Identity == nil {
		return "", ""
	}

	return req.Identity.Method, req.Identity.Connection
}

// decisionReads are what one decision reads beyond the memberships, each read once and
// kept for the rules after the one that read it: a tenant's declaration, and the
// tenants the person administers.
type decisionReads[Tx any] struct {
	policy *tenantPolicy[Tx]
	tx     Tx
	user   accesstypes.User
	// declared are the declarations read, nil kept for a tenant that declares nothing.
	declared map[accesstypes.Domain]*TenantDeclaration
	// administered are the tenants the person administers, once administeredRead.
	administered     []accesstypes.Domain
	administeredRead bool
}

// newDecisionReads starts the reads of one decision of p, in tx, about the person
// whose account is userID.
func newDecisionReads[Tx any](p *tenantPolicy[Tx], tx Tx, userID impulse.UUID) *decisionReads[Tx] {
	return &decisionReads[Tx]{
		policy:   p,
		tx:       tx,
		user:     accesstypes.User(userID.String()),
		declared: map[accesstypes.Domain]*TenantDeclaration{},
	}
}

// declaration is tenant's declaration, read through the policy's reader the first
// time it is asked for and answered from what was read after.
func (r *decisionReads[Tx]) declaration(ctx context.Context, tenant accesstypes.Domain) (*TenantDeclaration, error) {
	if declaration, read := r.declared[tenant]; read {
		return declaration, nil
	}
	declaration, err := r.policy.declarations(ctx, r.tx, tenant)
	if err != nil {
		return nil, errors.Wrapf(err, "DeclarationReader(%q)", tenant)
	}
	r.declared[tenant] = declaration

	return declaration, nil
}

// tenantsAdministered are the tenants the person administers, read through the role
// store the first time they are asked for and answered from what was read after.
func (r *decisionReads[Tx]) tenantsAdministered(ctx context.Context) ([]accesstypes.Domain, error) {
	if r.administeredRead {
		return r.administered, nil
	}
	administered, err := r.policy.roles.TenantsAdministered(ctx, r.user)
	if err != nil {
		return nil, errors.Wrap(err, "UserRoleManager.TenantsAdministered()")
	}
	r.administered, r.administeredRead = administered, true

	return administered, nil
}

// keptToDirectory is rule 2: the decision for an account that signs in through the
// directory of a directory-only tenant, nil when the rule does not decide. The tenants
// that keep the account to their directory are the tenants user.Directories names (a
// nil user names none) whose declaration is directory only and lists that directory,
// each read once, in the order the request names them; the closed ones are those whose
// declaration does not admit this sign-in. With none closed the rules after it decide.
// Otherwise a sign-in by the password method of a person who administers every closed
// tenant, none of them disabled, is held for the mailed code, and every other sign-in
// is refused as rule 7 refuses it (closedTo). What the person administers is read for a
// password sign-in alone.
func (r *decisionReads[Tx]) keptToDirectory(ctx context.Context, user *sessioninfo.SignInUser, method sessioninfo.AuthMethod, connection string) (*Decision, error) {
	if user == nil {
		return nil, nil
	}
	var closed []accesstypes.Domain
	anyDisabled := false
	for _, directory := range user.Directories {
		for _, tenant := range directory.Tenants {
			declaration, err := r.declaration(ctx, tenant)
			if err != nil {
				return nil, err
			}
			if declaration == nil || !declaration.DirectoryOnly || !declaration.declares(Directory{Method: directory.Method, Connection: directory.Connection}) {
				continue
			}
			if !declaration.Admits(method, connection) {
				closed = append(closed, tenant)
				anyDisabled = anyDisabled || declaration.IsDisabled()
			}
		}
	}
	if len(closed) == 0 {
		return nil, nil
	}
	if method == sessioninfo.MethodPassword {
		administered, err := r.tenantsAdministered(ctx)
		if err != nil {
			return nil, err
		}
		notAdministered := func(tenant accesstypes.Domain) bool {
			return !slices.Contains(administered, tenant)
		}
		if !anyDisabled && !slices.ContainsFunc(closed, notAdministered) {
			return RequireMFA(sessioninfo.MethodEmailCode), nil
		}
	}

	return closedTo(method, connection), nil
}

// administratorsWayIn reports whether the sign-in is one the administrators' way in
// admits once it carries a second factor: a sign-in by the password method of a person
// who administers a tenant that is directory only. The tenants the person administers
// are read through the role store, and each one's declaration decides
// (TenantDeclaration.AdmitsAdministrator, the decision the session's reach into the
// tenant asks again with the session's own second factor); a disabled tenant is passed
// over, since it gives no way in. A sign-in by any other method reads nothing: the way
// in is the password.
func (r *decisionReads[Tx]) administratorsWayIn(ctx context.Context, method sessioninfo.AuthMethod) (bool, error) {
	if method != sessioninfo.MethodPassword {
		return false, nil
	}
	administered, err := r.tenantsAdministered(ctx)
	if err != nil {
		return false, err
	}
	for _, tenant := range administered {
		declaration, err := r.declaration(ctx, tenant)
		if err != nil {
			return false, err
		}
		if declaration.IsDisabled() {
			continue
		}
		if declaration.AdmitsAdministrator(method, true, true) {
			return true, nil
		}
	}

	return false, nil
}
