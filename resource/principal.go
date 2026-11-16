package resource

import (
	"context"
	"slices"
	"sync"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/logger"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// roleSessionUser returns the person a session with a role principal answers for. A
// session established to act as a role has no person of its own: it is its actor's,
// named as the permission store names them (the id of the actor's account, or the name
// of an actor from another application). Any other session whose principal is a role
// was given the role by the application's resolver, and stays the session's own
// person: its user, or the person viewed as when it views as someone.
func roleSessionUser(ctx context.Context) accesstypes.User {
	sess := sessioninfo.DataFromCtx(ctx)
	if imp := sess.Impersonation; imp != nil && imp.Principal.IsRole() {
		return imp.ActorUser()
	}

	return sess.User()
}

// SessionPermissions composes the request's UserPermissions from the session in
// ctx. It picks the checker by the session's principal — forUser for a user
// principal (an ordinary session, or an impersonated user), forRole for a role
// principal — admits the session into the tenants that admit the way it was
// established, lists the session's domains as the application's roster filtered
// by that admission and by the checker's HasGrants, and applies the session's
// permission mask. It is the per-request accessor for an application with
// impersonated sessions:
//
//	return resource.SessionPermissions(r.Context(), a.access.ForUser, a.access.ForRole, a.tenants.Domains, a.declarations)
//
// The roster is the application's: access holds no tenant list, so the tenant
// picker's question — which tenants may this session see — is the roster
// asked, tenant by tenant, whether the session holds a grant there and whether
// the tenant admits the session. An application without tenants passes nil for
// the roster and the declarations.
//
// The declarations are the application's too: what each tenant declares about
// who may enter it (signin.TenantDeclaration), read by domain. A tenant is
// offered only when the session may enter it, and the tenant's declaration
// decides: it admits the session's establishment, the sign-in's method and
// connection the session's first auth event records
// (sessioninfo.SessionData.EstablishedBy; signin.TenantDeclaration.Admits is
// the decision), or the session's actor is a super administrator, a holder of
// the Super Administrator role (SessionUserChecker.SuperAdministrator), who
// enters every tenant whatever its sign-in settings say, as the sign-in policy
// admits them (signin.TenantPolicy). Nobody else is exempt: a person holding
// another global role, or a role in every tenant, is held to each tenant's
// declaration like anyone. An impersonated session is established by its
// impersonation event, so a directory-only tenant refuses it unless the
// actor, the real person behind it, is a super administrator; whom the session
// is viewed as changes nothing. In a tenant the session does not
// reach it holds nothing: every check Denied, an empty digest, no foothold, and
// the tenant is not listed, so the picker never offers it and the generated
// domain guard refuses a direct request for it as it refuses a domain that is
// not a tenant (UserPermissions.Reaches). An application that keeps no
// declarations passes nil, and every tenant admits every session.
//
// A person is named as the permission store names them: by the id of their
// account (sessioninfo.SessionData.User), never by the username, which can
// change and which two auths over one database can each hold. User() is that
// id, a row condition's subject binds to it, and a column that records who did
// something holds it.
//
// A role checker has no User(): a role is not anyone. For a role principal the
// composed UserPermissions reports the session's effective identity — the
// actor who established a session that acts as a role, and otherwise the
// session's own person, whom a resolver gave the role (roleSessionUser) — so a
// row condition's subject binds to a real person and nobody's identity is
// borrowed. An application that does not operate sessions as roles passes
// RolePrincipalsUnsupported as forRole: a role principal then fails closed —
// every check Denied, an empty digest, no footholds, no domains — with the
// same User(), so a deployment that mints role principals without a checker
// is loud rather than permissive.
func SessionPermissions[U SessionUserChecker, R RolePermissions](
	ctx context.Context, forUser func(accesstypes.User) U, forRole func(accesstypes.Role) R, roster DomainRoster, declarations TenantDeclarations,
) UserPermissions {
	principal := sessioninfo.PrincipalFromCtx(ctx)
	mask := sessioninfo.MaskFromCtx(ctx)

	var checker UserPermissionChecker
	var userChecker SessionUserChecker
	var user accesstypes.User
	var isUser bool
	if role, ok := principal.Role(); ok {
		var rolePerms RolePermissions
		if forRole != nil {
			rolePerms = forRole(role)
		}
		effective := roleSessionUser(ctx)
		if rolePerms == nil {
			return Masked(unsupportedRolePermissions{user: effective, role: role}, mask)
		}
		checker = rolePrincipalPermissions{RolePermissions: rolePerms, user: effective}
	} else {
		user, _ = principal.User()
		isUser = true
		userChecker = forUser(user)
		checker = userChecker
	}

	perms := rosterPermissions{UserPermissionChecker: checker, roster: roster}
	if declarations != nil {
		actor := sessioninfo.ActorUserFromCtx(ctx)
		actorChecker := func() SessionUserChecker {
			// An ordinary session's actor is its user: the checker in hand answers for
			// them. An impersonated or role-principal session is answered for by the
			// real person behind it.
			if isUser && user == actor {
				return userChecker
			}

			return forUser(actor)
		}
		establishment, _ := sessioninfo.DataFromCtx(ctx).EstablishedBy()
		perms.admission = &admittedPermissions{
			UserPermissionChecker: checker,
			declarations:          declarations,
			establishment:         establishment,
			actor:                 actorChecker,
			reached:               map[accesstypes.Domain]bool{},
		}
		perms.UserPermissionChecker = perms.admission
	}

	return Masked(perms, mask)
}

// RolePrincipalsUnsupported is the forRole argument for SessionPermissions in
// an application that does not operate sessions as roles. It returns nil,
// which SessionPermissions replaces with a fail-closed checker for the role.
func RolePrincipalsUnsupported(accesstypes.Role) RolePermissions {
	return nil
}

// rolePrincipalPermissions completes a role checker into the checker every
// decoder consumes: User() is the session's effective identity — the actor
// who established the role-principal session — which is what a row
// condition's subject binds to at render time.
type rolePrincipalPermissions struct {
	RolePermissions
	user accesstypes.User
}

// User returns the session's effective identity.
func (r rolePrincipalPermissions) User() accesstypes.User {
	return r.user
}

// rosterPermissions completes a checker into UserPermissions by answering
// Domains from the application's roster: each tenant the roster lists where
// the checker has a foothold, sorted, and Reaches from the session's admission
// when the application keeps declarations.
type rosterPermissions struct {
	UserPermissionChecker
	roster DomainRoster
	// admission is the session's admission into tenants, the checker above when the
	// application keeps declarations; nil when it keeps none, and the session reaches
	// every domain.
	admission *admittedPermissions
}

// Domains lists the roster's domains where the checker holds a grant, sorted;
// never nil. The checker's foothold is the admitted one, so a tenant the
// session does not reach is not listed.
func (p rosterPermissions) Domains(ctx context.Context) ([]accesstypes.Domain, error) {
	domains := []accesstypes.Domain{}
	if p.roster == nil {
		return domains, nil
	}
	all, err := p.roster(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "resource.DomainRoster")
	}
	for _, domain := range all {
		has, err := p.HasGrants(ctx, accesstypes.DomainScope(domain))
		if err != nil {
			return nil, errors.Wrap(err, "resource.UserPermissionChecker.HasGrants()")
		}
		if has {
			domains = append(domains, domain)
		}
	}
	slices.Sort(domains)

	return slices.Compact(domains), nil
}

// Reaches reports whether the session may enter domain: the admission's answer,
// and every domain when the application keeps no declarations.
func (p rosterPermissions) Reaches(ctx context.Context, domain accesstypes.Domain) (bool, error) {
	if p.admission == nil {
		return true, nil
	}

	return p.admission.Reaches(ctx, domain)
}

// admittedPermissions attenuates a checker to the tenants the session reaches
// (UserPermissions.Reaches): in a tenant whose declaration refuses the way the
// session was established the session holds nothing — every check Denied, an
// empty digest, no foothold — unless the session's actor is a super
// administrator, the one person no tenant's sign-in settings bind. The global
// partition is never attenuated: a declaration is a tenant's. One value serves
// one request, so a tenant's declaration is read once and the super
// administrator question asked once, whatever is asked about the tenant after.
type admittedPermissions struct {
	UserPermissionChecker
	declarations TenantDeclarations
	// establishment is the session's first auth event, the sign-in's method and
	// connection; the zero event for a session that records none, which no directory
	// admits and a tenant that is not directory only does.
	establishment sessioninfo.AuthEvent
	// actor builds the checker the super administrator question is asked of: the
	// session's actor, the real person behind an impersonated or role-principal
	// session.
	actor func() SessionUserChecker

	mu      sync.Mutex
	reached map[accesstypes.Domain]bool
	super   *bool
}

// Reaches reports whether the session may enter domain: the tenant's declaration
// admits the session's establishment, or the session's actor is a super
// administrator.
func (a *admittedPermissions) Reaches(ctx context.Context, domain accesstypes.Domain) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if reaches, ok := a.reached[domain]; ok {
		return reaches, nil
	}
	declaration, err := a.declarations(ctx, domain)
	if err != nil {
		return false, errors.Wrapf(err, "resource.TenantDeclarations(%q)", domain)
	}
	reaches := declaration.Admits(a.establishment.Method, a.establishment.Connection)
	if !reaches {
		reaches, err = a.actorIsSuperAdministrator(ctx)
		if err != nil {
			return false, err
		}
	}
	a.reached[domain] = reaches

	return reaches, nil
}

// actorIsSuperAdministrator answers, once per value, whether the session's actor
// holds the Super Administrator role: the one exemption from a tenant's sign-in
// settings, and the question the sign-in policy asks of the person signing in
// (accesstypes.RoleCollection.HoldsSuperAdministrator is the test both come to).
// The caller holds the lock.
func (a *admittedPermissions) actorIsSuperAdministrator(ctx context.Context) (bool, error) {
	if a.super != nil {
		return *a.super, nil
	}
	super, err := a.actor().SuperAdministrator(ctx)
	if err != nil {
		return false, errors.Wrap(err, "resource.SessionUserChecker.SuperAdministrator()")
	}
	a.super = &super

	return super, nil
}

// Check answers Denied for every resource in a tenant the session does not reach,
// without consulting policy; otherwise it delegates.
func (a *admittedPermissions) Check(
	ctx context.Context, env accesstypes.Environment, scope accesstypes.Scope, perm accesstypes.Permission, resources ...accesstypes.Resource,
) (accesstypes.Decisions, error) {
	if domain, ok := scope.Domain(); ok {
		reaches, err := a.Reaches(ctx, domain)
		if err != nil {
			return nil, err
		}
		if !reaches {
			logger.FromCtx(ctx).Warnf("tenant admission: %s denied for %s on %v: the tenant %s does not admit the session's establishment", perm, a.User(), resources, domain)

			return deniedDecisions(resources), nil
		}
	}

	decisions, err := a.UserPermissionChecker.Check(ctx, env, scope, perm, resources...)
	if err != nil {
		return nil, errors.Wrap(err, "resource.UserPermissionChecker.Check()")
	}

	return decisions, nil
}

// PermissionDigest is empty in a tenant the session does not reach, so the
// frontend's digest agrees with what Check will enforce; otherwise it delegates.
func (a *admittedPermissions) PermissionDigest(ctx context.Context, scope accesstypes.Scope) (accesstypes.PermissionDigest, error) {
	if domain, ok := scope.Domain(); ok {
		reaches, err := a.Reaches(ctx, domain)
		if err != nil {
			return nil, err
		}
		if !reaches {
			return accesstypes.PermissionDigest{}, nil
		}
	}

	digest, err := a.UserPermissionChecker.PermissionDigest(ctx, scope)
	if err != nil {
		return nil, errors.Wrap(err, "resource.UserPermissionChecker.PermissionDigest()")
	}

	return digest, nil
}

// HasGrants reports a foothold in a tenant the session holds a grant in and
// reaches. The grant is asked first, from the checker's snapshot, so a tenant's
// declaration is read only for a tenant the session holds a grant in.
func (a *admittedPermissions) HasGrants(ctx context.Context, scope accesstypes.Scope) (bool, error) {
	has, err := a.UserPermissionChecker.HasGrants(ctx, scope)
	if err != nil {
		return false, errors.Wrap(err, "resource.UserPermissionChecker.HasGrants()")
	}
	domain, ok := scope.Domain()
	if !has || !ok {
		return has, nil
	}

	return a.Reaches(ctx, domain)
}

// Masked returns perms attenuated by mask: a permission the mask does not allow
// is Denied for every resource before policy is consulted — and evidenced as a
// mask denial — and the permission digest omits it. A foothold is answered
// through the masked digest, so a session masked to nothing has none and
// lists no domain. User and Reaches pass through. The unrestricted mask returns
// perms itself.
func Masked(perms UserPermissions, mask accesstypes.PermissionMask) UserPermissions {
	if mask.IsZero() {
		return perms
	}

	return &maskedPermissions{UserPermissions: perms, mask: mask}
}

type maskedPermissions struct {
	UserPermissions
	mask accesstypes.PermissionMask
}

// Check answers Denied for every resource when the mask does not allow perm,
// without consulting policy; otherwise it delegates.
func (m *maskedPermissions) Check(
	ctx context.Context, env accesstypes.Environment, scope accesstypes.Scope, perm accesstypes.Permission, resources ...accesstypes.Resource,
) (accesstypes.Decisions, error) {
	if !m.mask.Allows(perm) {
		logger.FromCtx(ctx).Warnf("impersonation: %s denied by mask (%s) for %s on %v", perm, m.mask, m.User(), resources)

		return deniedDecisions(resources), nil
	}

	decisions, err := m.UserPermissions.Check(ctx, env, scope, perm, resources...)
	if err != nil {
		return nil, errors.Wrap(err, "resource.UserPermissions.Check()")
	}

	return decisions, nil
}

// PermissionDigest delegates and then drops every permission the mask does not
// allow, so the frontend's digest agrees with what Check will enforce; a
// resource left with no permissions is dropped (absence means denied).
func (m *maskedPermissions) PermissionDigest(ctx context.Context, scope accesstypes.Scope) (accesstypes.PermissionDigest, error) {
	digest, err := m.UserPermissions.PermissionDigest(ctx, scope)
	if err != nil {
		return nil, errors.Wrap(err, "resource.UserPermissions.PermissionDigest()")
	}

	masked := make(accesstypes.PermissionDigest, len(digest))
	for res, perms := range digest {
		kept := make(map[accesstypes.Permission]accesstypes.DigestState, len(perms))
		for perm, state := range perms {
			if m.mask.Allows(perm) {
				kept[perm] = state
			}
		}
		if len(kept) > 0 {
			masked[res] = kept
		}
	}

	return masked, nil
}

// HasGrants answers through the masked digest: the session has a foothold in
// scope exactly when something the mask allows is granted there.
func (m *maskedPermissions) HasGrants(ctx context.Context, scope accesstypes.Scope) (bool, error) {
	digest, err := m.PermissionDigest(ctx, scope)
	if err != nil {
		return false, err
	}

	return len(digest) > 0, nil
}

// Domains lists the delegate's domains where the masked session still has a
// foothold; never nil.
func (m *maskedPermissions) Domains(ctx context.Context) ([]accesstypes.Domain, error) {
	domains, err := m.UserPermissions.Domains(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "resource.UserPermissions.Domains()")
	}
	kept := make([]accesstypes.Domain, 0, len(domains))
	for _, domain := range domains {
		has, err := m.HasGrants(ctx, accesstypes.DomainScope(domain))
		if err != nil {
			return nil, err
		}
		if has {
			kept = append(kept, domain)
		}
	}

	return kept, nil
}

// Reaches passes through: a mask attenuates what the session may do, never
// where it may go.
func (m *maskedPermissions) Reaches(ctx context.Context, domain accesstypes.Domain) (bool, error) {
	reaches, err := m.UserPermissions.Reaches(ctx, domain)
	if err != nil {
		return false, errors.Wrap(err, "resource.UserPermissions.Reaches()")
	}

	return reaches, nil
}

// unsupportedRolePermissions is the fail-closed checker for a role principal
// when no role checker is configured. User() is still the session's effective
// identity, so evidence names the real person even while nothing is granted.
type unsupportedRolePermissions struct {
	user accesstypes.User
	role accesstypes.Role
}

func (u unsupportedRolePermissions) Check(
	ctx context.Context, _ accesstypes.Environment, _ accesstypes.Scope, perm accesstypes.Permission, resources ...accesstypes.Resource,
) (accesstypes.Decisions, error) {
	logger.FromCtx(ctx).Warnf("impersonation: role principal %s has no role-bound checker; %s denied on %v", u.role, perm, resources)

	return deniedDecisions(resources), nil
}

func (unsupportedRolePermissions) PermissionDigest(context.Context, accesstypes.Scope) (accesstypes.PermissionDigest, error) {
	return accesstypes.PermissionDigest{}, nil
}

func (unsupportedRolePermissions) HasGrants(context.Context, accesstypes.Scope) (bool, error) {
	return false, nil
}

func (unsupportedRolePermissions) Domains(context.Context) ([]accesstypes.Domain, error) {
	return []accesstypes.Domain{}, nil
}

func (unsupportedRolePermissions) Reaches(context.Context, accesstypes.Domain) (bool, error) {
	return false, nil
}

func (u unsupportedRolePermissions) User() accesstypes.User {
	return u.user
}

// deniedDecisions is the fail-closed answer for resources: every one Denied.
func deniedDecisions(resources []accesstypes.Resource) accesstypes.Decisions {
	decisions := make(accesstypes.Decisions, len(resources))
	for _, res := range resources {
		decisions[res] = accesstypes.Denied()
	}

	return decisions
}
