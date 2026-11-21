package signin

import (
	"context"
	"slices"

	"github.com/go-playground/errors/v5"
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
// answers it for a stranger's first sign-in: the tenant, and the default seat the
// tenant gives a person its directory vouched for, the role the sign-in seats them
// with in the tenant, or "" when the tenant admits its people by invitation only.
type DeclaredDirectory struct {
	Tenant      accesstypes.Domain
	DefaultSeat accesstypes.Role
}

// DirectoryReader reads every tenant's declaration of a directory for a stranger's
// first sign-in, in the sign-in's first transaction, typed to the storage's
// transaction like the other hooks: the tenants that declare the directory the
// identity came through, each with its default seat. A directory some tenant declares
// belongs to that tenant, and its declaration decides the stranger before the
// registration's account rule does; a directory no tenant declares, an empty answer,
// falls to the registration's rule as it would without the reader. Spanner may retry
// the transaction, so the reader must not have side effects outside it. Set it on
// the Auth with SetDirectoryAdmission, beside the hook that seats the person.
type DirectoryReader[Tx any] func(ctx context.Context, tx Tx, directory Directory) ([]DeclaredDirectory, error)

// DirectorySeat is a seat a first sign-in grants: a role in a tenant, the default seat
// of a directory the tenant declared, written by the application's hook once the
// user is committed.
type DirectorySeat struct {
	Tenant accesstypes.Domain
	Role   accesstypes.Role
}

// TenantDeclaration is what a tenant declares about who may enter it: the directory
// its people come through, and whether they come through it only. It is
// configuration data of the tenant, kept by the application and read for the library
// by a DeclarationReader. A tenant's declaration restricts that tenant's own door and
// nobody else's: a tenant that declares nothing, a nil or an empty declaration, admits
// everyone it seats, however they signed in.
type TenantDeclaration struct {
	// Directories are the directories the tenant admits its people through. A tenant
	// has one directory, and a directory is one tenant's: the application's schema and
	// its write paths hold both, so the list carries one directory at most. The
	// directory seats a stranger at their first sign-in through it (DeclaredDirectory);
	// it closes the tenant to nobody until the tenant is directory only.
	Directories []Directory
	// DirectoryOnly says the tenant's people enter through its directory only: a
	// password, a personal account, any other sign-in that is not through a directory,
	// and a sign-in through another organization's directory do not reach the tenant.
	// The tenant's own administrators alone keep a way in beside the directory, with a
	// password and a second factor (AdmitsAdministrator).
	DirectoryOnly bool
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

// TenantPolicyOption refines the tenant policy.
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
// tenant's declaration restricts that tenant alone, so the policy refuses a person
// only when every tenant that seats them is closed to the way they signed in. The
// policy decides what the session may be; which of the person's tenants the session
// then reaches is decided again, tenant by tenant, where the session's tenant list is
// computed, with TenantDeclaration.Admits over the session's first auth event, and
// with TenantDeclaration.AdmitsAdministrator for the tenant's own administrators. It
// never writes. Set it with session.Auth.SetSignInPolicy; Then chains the
// application's own rule after it, which still refuses whom it refuses.
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
//  2. A tenant's own administrators keep a way into their tenant: a person who signs
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
//     through another organization's directory or with a personal account.
//  3. A person holding a role outside any one tenant is admitted: in the global
//     partition, which is no tenant's and which no declaration governs, or in every
//     tenant domain, whose tenants the policy has no list of. It is no exemption: the
//     session then reaches a tenant only where the tenant's declaration admits it, as
//     anyone's does.
//  4. A person who is a member of no tenant is refused (no_tenant), unless the policy
//     was built with AdmitWithoutTenant.
//  5. The sign-in is admitted when one of the person's tenants admits it
//     (TenantDeclaration.Admits): a tenant that is not directory only admits every
//     sign-in, a password, a personal account, a session the application starts for a
//     user it authenticated itself, and a sign-in through any directory; a tenant that
//     is directory only admits a sign-in through its own directory alone.
//  6. Otherwise every tenant of the person is directory only and none has the way they
//     signed in as its directory: nothing would be reachable, and the sign-in is
//     refused. A sign-in through a directory (Entra, Google, WorkOS, an OpenID Connect
//     provider) is refused with connection_not_in_tenant, a sign-in with a personal
//     account (through Google or Microsoft, with an account that is in no
//     organization's directory) with directory_only_personal_account, and any other, a
//     password or a session the application starts itself, with directory_only. The
//     codes are apart so that a login page says what is true of how the person signed
//     in: a person who used no password is not told to stop using one.
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
	enters, err := p.administratorsWayIn(ctx, tx, req, method)
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
	if len(tenants) == 0 {
		if p.settings.admitWithoutTenant {
			return Allow(), nil
		}

		return denyBecause(sessioninfo.RefusedNoTenant, ErrNoTenant), nil
	}

	for _, tenant := range tenants {
		declaration, err := p.declarations(ctx, tx, tenant)
		if err != nil {
			return nil, errors.Wrapf(err, "DeclarationReader(%q)", tenant)
		}
		if declaration.Admits(method, connection) {
			return Allow(), nil
		}
	}
	if throughDirectory(method, connection) {
		return denyBecause(sessioninfo.RefusedConnectionNotInTenant, ErrConnectionNotInTenant), nil
	}
	// A directory method that came through no directory a tenant can declare is a
	// personal account: the person used no password, and the code says so.
	if method.ThroughDirectory() {
		return denyBecause(sessioninfo.RefusedDirectoryOnlyPersonalAccount, ErrDirectoryOnly), nil
	}

	return denyBecause(sessioninfo.RefusedDirectoryOnly, ErrDirectoryOnly), nil
}

// administratorsWayIn reports whether the sign-in is one the administrators' way in
// admits once it carries a second factor: a sign-in by the password method of a person
// who administers a tenant that is directory only. The tenants the person administers
// are read through the role store, and each one's declaration decides
// (TenantDeclaration.AdmitsAdministrator, the decision the session's reach into the
// tenant asks again with the session's own second factor). A sign-in by any other
// method reads nothing: the way in is the password.
func (p *tenantPolicy[Tx]) administratorsWayIn(ctx context.Context, tx Tx, req *sessioninfo.NewSessionRequest, method sessioninfo.AuthMethod) (bool, error) {
	if method != sessioninfo.MethodPassword {
		return false, nil
	}
	administered, err := p.roles.TenantsAdministered(ctx, accesstypes.User(req.UserID.String()))
	if err != nil {
		return false, errors.Wrap(err, "UserRoleManager.TenantsAdministered()")
	}
	for _, tenant := range administered {
		declaration, err := p.declarations(ctx, tx, tenant)
		if err != nil {
			return false, errors.Wrapf(err, "DeclarationReader(%q)", tenant)
		}
		if declaration.AdmitsAdministrator(method, true, true) {
			return true, nil
		}
	}

	return false, nil
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
