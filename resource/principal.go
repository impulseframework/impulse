package resource

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/logger"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// roleSessionUser returns the person a session with a role principal answers for. A
// session established to act as a role has no person of its own: it is its actor's,
// named as the permission store names them (the id of the actor's account, or the name
// of an actor from another application). Any other session whose principal is a role
// was given the role by the application's resolver, and stays the session's own
// person, its user: it is an ordinary session, since a session that acts as someone
// else is checked as its record says and the resolver is never asked about one.
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
//	return resource.SessionPermissions(r.Context(), a.access.ForUser, a.access.ForRole, a.tenants.Domains, a.declarations, a.collection)
//
// The roster is the application's: access holds no tenant list, so the tenant
// picker's question — which tenants may this session see — is the roster
// asked, tenant by tenant, whether the session holds a grant there and whether
// the tenant admits the session. An application without tenants passes nil for
// the roster and the declarations.
//
// The declarations are the application's too: what each tenant declares about
// who may enter it (signin.TenantDeclaration), read by domain. A tenant's
// declaration restricts that tenant's own door and nobody else's: the session
// reaches every tenant that seats the person, except a tenant that is directory
// only and whose directory is not the one the person signed in through. A
// tenant is offered only when the session may enter it, and the tenant's
// declaration decides: it admits the session's establishment, the sign-in's
// method and connection the session's first auth event records
// (sessioninfo.SessionData.EstablishedBy; signin.TenantDeclaration.Admits is
// the decision), or the session's actor is a super administrator, a holder of
// the Super Administrator role (SessionUserChecker.SuperAdministrator), who
// enters every tenant whatever its sign-in settings say, as the sign-in policy
// admits them (signin.TenantPolicy), or the session's actor administers the
// tenant and came by the administrators' way in: a tenant that is directory
// only still admits its own administrators, in a session established with a
// password that carries a second factor
// (signin.TenantDeclaration.AdmitsAdministrator is the decision, the one the
// sign-in policy asks; SessionUserChecker.AdministersTenant says who
// administers, asked at every request, so the way in closes at the next
// request once the role is taken away or its period ends). Nobody else enters
// a directory-only tenant another way than through its directory: a person
// holding another global role, or a role in every tenant, the administrator's
// among them, is held to each tenant's declaration like anyone, and so is the
// tenant's administrator who signed in through another organization's
// directory, with a personal account, or with a password and no second
// factor. An impersonated session is established by its
// impersonation event, so a directory-only tenant refuses it unless the
// actor, the real person behind it, is a super administrator; whom the session
// is viewed as changes nothing. In a tenant the session does not
// reach it holds nothing: every check Denied, an empty digest, no foothold, and
// the tenant is not among its domains, so the picker never offers it as a place
// to open and the generated domain guard refuses a direct request for it as it
// refuses a domain that is not a tenant (UserPermissions.Reaches). The person
// holds a seat there all the same, so the tenant list of a person's own sign-in
// carries the tenant closed, with the reason (ClosedDomains, UserDomainsHandler);
// an impersonated session and a session that acts as a role are told of none. An
// application that keeps no declarations passes nil, and every tenant admits
// every session.
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
//
// The classes are the application's generated collection (router.Collection()),
// built once and kept: what each action carries (@class). The mask of an
// impersonated session may refuse a class of actions, the credential actions
// for one, and the masked check asks the collection which actions those are
// (Masked). The argument is not optional because the refusal must not be: with
// nil, a mask that refuses a class refuses every check.
func SessionPermissions[U SessionUserChecker, R RolePermissions](
	ctx context.Context, forUser func(accesstypes.User) U, forRole func(accesstypes.Role) R, roster DomainRoster, declarations TenantDeclarations, classes ActionClasses,
) UserPermissions {
	principal := sessioninfo.PrincipalFromCtx(ctx)
	mask := sessioninfo.MaskFromCtx(ctx)

	var checker UserPermissionChecker
	var userChecker SessionUserChecker
	var user accesstypes.User
	var isUser bool
	var person func() UserPermissionChecker
	if role, ok := principal.Role(); ok {
		var rolePerms RolePermissions
		if forRole != nil {
			rolePerms = forRole(role)
		}
		effective := roleSessionUser(ctx)
		if rolePerms == nil {
			return Masked(unsupportedRolePermissions{user: effective, role: role}, mask, classes)
		}
		checker = rolePrincipalPermissions{RolePermissions: rolePerms, user: effective}
		// The session runs with a role's grants, and its person is somebody: what they
		// hold is their own checker's to say, never the role's. An actor another
		// application authenticated has no account here and holds nothing of this
		// application, whatever their name spells.
		imp, impersonated := sessioninfo.ImpersonationFromCtx(ctx)
		foreign := impersonated && imp.Principal.IsRole() && !imp.IsLocalActor()
		person = func() UserPermissionChecker {
			if foreign {
				return nil
			}

			return forUser(effective)
		}
	} else {
		user, _ = principal.User()
		isUser = true
		userChecker = forUser(user)
		checker = userChecker
	}

	perms := rosterPermissions{UserPermissionChecker: checker, roster: roster, person: person}
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
		// An actor another application authenticated has no account here and holds no
		// role of this one, whatever their name spells: the record names them by a
		// name, which the question is never asked under.
		imp, impersonated := sessioninfo.ImpersonationFromCtx(ctx)
		establishment, _ := sessioninfo.DataFromCtx(ctx).EstablishedBy()
		_, secondFactor := sessioninfo.DataFromCtx(ctx).SecondFactor()
		perms.admission = &admittedPermissions{
			UserPermissionChecker: checker,
			declarations:          declarations,
			establishment:         establishment,
			secondFactor:          secondFactor,
			actor:                 actorChecker,
			foreignActor:          impersonated && !imp.IsLocalActor(),
			reached:               map[accesstypes.Domain]bool{},
		}
		perms.UserPermissionChecker = perms.admission
		perms.ownSignIn = isUser && !impersonated
	}

	return Masked(perms, mask, classes)
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
	// ownSignIn says the session is a person's own sign-in: a user principal that
	// nobody is viewing the application as. Only such a session is told which tenants
	// are closed to it (ClosedDomains).
	ownSignIn bool
	// person builds the checker of the session's person where the checker above is not
	// theirs: a session that runs with a role's grants, whose person is the real person
	// behind it. It answers nil for a person with no account here. It is nil itself
	// where the checker above is the person's own. HeldDomains asks it.
	person func() UserPermissionChecker
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
// administrator, whom no tenant's sign-in settings bind, or the tenant's own
// administrator come by the administrators' way in, a password and a second
// factor. The global partition is never attenuated: a declaration is a
// tenant's. One value serves one request, so a tenant's declaration is read
// once, the super administrator question asked once and the tenant
// administrator question once for a tenant, whatever is asked about the tenant
// after; the next request asks again. The session's tenant list alone asks
// once more of a tenant that is closed to a password session, to tell the
// tenant's own administrator that the code is all that is missing
// (opensWithSecondFactor).
type admittedPermissions struct {
	UserPermissionChecker
	declarations TenantDeclarations
	// establishment is the session's first auth event, the sign-in's method and
	// connection; the zero event for a session that records none, which a tenant that
	// is directory only refuses and any other tenant admits.
	establishment sessioninfo.AuthEvent
	// secondFactor says the session carries a second factor
	// (sessioninfo.SessionData.SecondFactor), which the administrators' way into a
	// directory-only tenant asks for beside the password.
	secondFactor bool
	// actor builds the checker the super administrator question and the tenant
	// administrator question are asked of: the session's actor, the real person
	// behind an impersonated or role-principal session.
	actor func() SessionUserChecker
	// foreignActor marks a session whose actor another application authenticated:
	// a person with no account here, who is no super administrator of this
	// application and administers no tenant of it.
	foreignActor bool

	mu      sync.Mutex
	reached map[accesstypes.Domain]bool
	super   *bool
}

// Reaches reports whether the session may enter domain: the tenant's declaration
// admits the session's establishment, or the session's actor is a super
// administrator, or the actor administers the tenant and the declaration admits
// the session by the administrators' way in.
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
	if !reaches {
		reaches, err = a.administratorsWayIn(ctx, declaration, domain)
		if err != nil {
			return false, err
		}
	}
	a.reached[domain] = reaches

	return reaches, nil
}

// administratorsWayIn answers whether the tenant's declaration admits the session
// by the administrators' way in (signin.TenantDeclaration.AdmitsAdministrator, the
// decision the sign-in policy made when it held the sign-in for its second factor):
// a tenant that is directory only, a session established with a password that
// carries a second factor, and an actor who administers the tenant. The engine is
// asked who administers only where the rest of the decision already holds, and it is
// asked at every request, so a role taken away, or one whose period has ended, closes
// the way at the next request. The caller holds the lock.
func (a *admittedPermissions) administratorsWayIn(ctx context.Context, declaration *signin.TenantDeclaration, domain accesstypes.Domain) (bool, error) {
	if a.foreignActor || !declaration.AdmitsAdministrator(a.establishment.Method, a.secondFactor, true) {
		return false, nil
	}
	administers, err := a.actor().AdministersTenant(ctx, domain)
	if err != nil {
		return false, errors.Wrap(err, "resource.SessionUserChecker.AdministersTenant()")
	}

	return declaration.AdmitsAdministrator(a.establishment.Method, a.secondFactor, administers), nil
}

// opensWithSecondFactor answers whether the tenant's declaration would admit the
// session by the administrators' way in had the session a second factor, which it
// does not carry: a tenant that is directory only, a session established with a
// password, and an actor who administers the tenant. The decision is the one Reaches
// asks (signin.TenantDeclaration.AdmitsAdministrator), with the factor taken as given,
// so what the tenant list tells the person and what a sign-in with the code then
// reaches cannot disagree. A session that carries a second factor is answered no: the
// factor is not what keeps it out.
func (a *admittedPermissions) opensWithSecondFactor(ctx context.Context, domain accesstypes.Domain) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.foreignActor || a.secondFactor {
		return false, nil
	}
	declaration, err := a.declarations(ctx, domain)
	if err != nil {
		return false, errors.Wrapf(err, "resource.TenantDeclarations(%q)", domain)
	}
	if !declaration.AdmitsAdministrator(a.establishment.Method, true, true) {
		return false, nil
	}
	administers, err := a.actor().AdministersTenant(ctx, domain)
	if err != nil {
		return false, errors.Wrap(err, "resource.SessionUserChecker.AdministersTenant()")
	}

	return declaration.AdmitsAdministrator(a.establishment.Method, true, administers), nil
}

// actorIsSuperAdministrator answers, once per value, whether the session's actor
// holds the Super Administrator role: the exemption from a tenant's sign-in
// settings that holds in every tenant, and the question the sign-in policy asks of
// the person signing in (accesstypes.RoleCollection.HoldsSuperAdministrator is the
// test both come to).
// The caller holds the lock.
func (a *admittedPermissions) actorIsSuperAdministrator(ctx context.Context) (bool, error) {
	if a.super != nil {
		return *a.super, nil
	}
	if a.foreignActor {
		super := false
		a.super = &super

		return false, nil
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

// ActionClasses answers what the masked check of an impersonated session asks about a
// name, as the generated collection does: the classes an action carries within a
// permission scope (GeneratedCollection.ActionClasses) and the classes a write of a
// record carries (GeneratedCollection.WriteClasses), since a mask may refuse a class,
// and whether the application registers a permission under the name at all
// (GeneratedCollection.HasPermission), since what a name the application does not
// register is, and the classes it carries, cannot be known.
type ActionClasses interface {
	// ActionClasses reports the classes the action carries within scope.
	ActionClasses(scope accesstypes.PermissionScope, action accesstypes.Resource) []accesstypes.ActionClass
	// WriteClasses reports the classes the write permission of record carries within
	// scope: the generator gives the class access to the create, the change and the
	// delete of a record that decides what a person may do.
	WriteClasses(scope accesstypes.PermissionScope, permission accesstypes.Permission, record accesstypes.Resource) []accesstypes.ActionClass
	// HasPermission reports whether the application registers permission on res within
	// scope.
	HasPermission(scope accesstypes.PermissionScope, permission accesstypes.Permission, res accesstypes.Resource) bool
}

// Masked returns perms attenuated by mask. A check is Denied before policy is
// consulted, and evidenced as a mask denial, when the mask does not keep the
// permission's kind, when it refuses a class the resource carries, or when it refuses
// the permission on the resource by name; the permission digest leaves the same
// entries out, so no page offers what the check would refuse. A foothold is answered
// through the masked digest, so a session masked to nothing has none and lists no
// domain. User and Reaches pass through. The unrestricted mask returns perms itself.
//
// classes answers the classes each action carries: the application's generated
// collection. A mask that refuses a class cannot be judged without them, so with nil
// classes such a mask refuses every check: nothing is allowed on a guess. For the same
// reason such a mask refuses Execute on a name that is no action of the application
// (one classes registers no Execute on, for the whole application or for a tenant):
// the name carries no class anyone declared, so the check would otherwise let it
// through whatever the mask refuses. The declaration of a kind holds a mask to the
// application's actions by the same rule when the application starts
// (session.NewImpersonations). And it refuses any other permission on a name the
// application registers nothing under: such a name may be an action of another
// collection, or of none, and a check that cannot say what it is asked about does not
// let it through a mask written to refuse a class. A record the application registers
// carries a class on the writes the generator gave one, those of a record that decides
// what a person may do (CollectionResource.WriteClasses); every other permission of a
// record carries none and is left to policy.
func Masked(perms UserPermissions, mask accesstypes.PermissionMask, classes ActionClasses) UserPermissions {
	if mask.IsZero() {
		return perms
	}

	return newMaskedPermissions(perms, mask, classes)
}

type maskedPermissions struct {
	UserPermissions
	mask accesstypes.PermissionMask
	// classes answers the classes an action carries; nil when the application handed
	// none.
	classes ActionClasses
	// refusesClasses says the mask refuses at least one class, so a check needs the
	// classes of what it is asked about.
	refusesClasses bool
}

// newMaskedPermissions is perms under a mask that restricts something, with what
// answers the classes of an action.
func newMaskedPermissions(perms UserPermissions, mask accesstypes.PermissionMask, classes ActionClasses) *maskedPermissions {
	masked := &maskedPermissions{UserPermissions: perms, mask: mask, classes: classes}
	for _, refusal := range mask.Refusals() {
		if _, ok := refusal.Class(); ok {
			masked.refusesClasses = true
		}
	}

	return masked
}

// refuses reports whether the mask refuses perm on res, and why. The classes asked
// about are those of the name res is checked under: an action's own, and for a record
// the classes its write perm carries, which the generator gives the writes of a record
// that decides what a person may do (a field is judged by its record). A class is
// carried by the name whichever scope registers it, so the classes of both scopes are
// asked: a check made in one scope of a name classed in the other is refused with it,
// never let through for want of a class.
//
// Under a mask that refuses a class, a permission on a name the application does not
// hold is refused: what the name carries is not nothing, it is unknown. For Execute the
// name must be an action of the application, one it registers Execute on. For any
// other permission the application must register something under the name. A name
// spelled wrong, a name checked by hand and never registered, and a name of a
// collection other than the one the check was handed are each such a name, and each
// would otherwise pass a mask written to refuse a whole class.
func (m *maskedPermissions) refuses(perm accesstypes.Permission, res accesstypes.Resource) (why string, refused bool) {
	if !m.refusesClasses {
		return m.mask.Refuses(perm, res)
	}
	if m.classes == nil {
		return "the mask refuses a class of actions and the application handed the check no classes (resource.SessionPermissions takes the generated collection)", true
	}
	base, _, _ := strings.Cut(string(res), ".")
	name := accesstypes.Resource(base)
	switch {
	case perm == accesstypes.Execute && !m.registersAction(name):
		return fmt.Sprintf(
			"the mask refuses a class of actions and %q is no action of the application, so the classes it carries cannot be known: "+
				"register it by hand (@manualAddResource(Execute) on the constant, with the @class it carries) or correct the name",
			string(name),
		), true
	case perm != accesstypes.Execute && !m.registers(name):
		return fmt.Sprintf(
			"the mask refuses a class of actions and the application registers nothing under %q, so what the name is cannot be known: "+
				"register it by hand (@manualAddResource(%s) on the constant) or correct the name",
			string(name), perm,
		), true
	}
	classes := slices.Concat(
		m.classes.ActionClasses(accesstypes.GlobalPermissionScope, name),
		m.classes.ActionClasses(accesstypes.DomainPermissionScope, name),
		m.classes.WriteClasses(accesstypes.GlobalPermissionScope, perm, name),
		m.classes.WriteClasses(accesstypes.DomainPermissionScope, perm, name),
	)

	return m.mask.Refuses(perm, res, classes...)
}

// registersAction reports whether the application registers Execute on action, for the
// whole application or for a tenant: the rule the declaration of a kind holds a grant
// and a mask's named action to (session.NewImpersonations).
func (m *maskedPermissions) registersAction(action accesstypes.Resource) bool {
	return m.classes.HasPermission(accesstypes.GlobalPermissionScope, accesstypes.Execute, action) ||
		m.classes.HasPermission(accesstypes.DomainPermissionScope, accesstypes.Execute, action)
}

// registers reports whether the application registers any permission under name, for
// the whole application or for a tenant: the name is one of its records or one of its
// actions. The question is of the name and not of the permission asked, so a record
// the application registers is left to policy for each of its permissions, the ones it
// registers and the ones it does not.
func (m *maskedPermissions) registers(name accesstypes.Resource) bool {
	for _, scope := range scopeVocabulary {
		for _, permission := range permissionVocabulary {
			if m.classes.HasPermission(scope, permission, name) {
				return true
			}
		}
	}

	return false
}

// Check answers Denied, without consulting policy, for every resource the mask
// refuses perm on: all of them when the mask does not keep the kind, and otherwise
// each one whose class or whose name the mask refuses. The others are delegated, and
// answered as policy answers them.
func (m *maskedPermissions) Check(
	ctx context.Context, env accesstypes.Environment, scope accesstypes.Scope, perm accesstypes.Permission, resources ...accesstypes.Resource,
) (accesstypes.Decisions, error) {
	if !m.mask.Allows(perm) {
		logMaskKindDenied(ctx, perm, m.mask, m.User(), resources...)

		return deniedDecisions(resources), nil
	}

	var refused []accesstypes.Resource
	passed := make([]accesstypes.Resource, 0, len(resources))
	for _, res := range resources {
		why, refuses := m.refuses(perm, res)
		if !refuses {
			passed = append(passed, res)

			continue
		}
		logMaskRefusal(ctx, perm, m.mask, m.User(), res, why)
		refused = append(refused, res)
	}
	if len(refused) > 0 && len(passed) == 0 {
		return deniedDecisions(refused), nil
	}

	decisions, err := m.UserPermissions.Check(ctx, env, scope, perm, passed...)
	if err != nil {
		return nil, errors.Wrap(err, "resource.UserPermissions.Check()")
	}
	if len(refused) == 0 {
		return decisions, nil
	}

	// What policy answered is its own: the refusals are added to a copy.
	merged := make(accesstypes.Decisions, len(resources))
	maps.Copy(merged, decisions)
	maps.Copy(merged, deniedDecisions(refused))

	return merged, nil
}

// PermissionDigest delegates and then drops every entry the mask refuses, by its
// kind, its resource's class or its resource's name, so the frontend's digest agrees
// with what Check will enforce; a resource left with no permissions is dropped
// (absence means denied).
func (m *maskedPermissions) PermissionDigest(ctx context.Context, scope accesstypes.Scope) (accesstypes.PermissionDigest, error) {
	digest, err := m.UserPermissions.PermissionDigest(ctx, scope)
	if err != nil {
		return nil, errors.Wrap(err, "resource.UserPermissions.PermissionDigest()")
	}

	masked := make(accesstypes.PermissionDigest, len(digest))
	for res, perms := range digest {
		kept := make(map[accesstypes.Permission]accesstypes.DigestState, len(perms))
		for perm, state := range perms {
			if _, refuses := m.refuses(perm, res); !refuses {
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

// MaskRefusesAction reports whether the permission mask of the session in the context
// refuses the action, and why in a few words for the log. It is the mask's part of a
// check alone, with no policy behind it, for an action no grant admits. The generated
// create-your-tenant route is one: any signed-in person who holds no tenant may call
// it, so no permission is checked there for a mask to deny, and in a session that acts
// as a person it would make the person acted as the new tenant's administrator
// whatever the mask said. The route carries an action's name instead, and asks this.
//
// The mask refuses the action as it refuses any other (Masked): when it does not keep
// Execute, when it refuses a class the action carries, or when it refuses the action by
// name. A session with no mask is refused nothing: an ordinary session, and an
// impersonated one whose kind gives everything, since to let someone act as a person
// with no mask is to give them what the person holds.
//
// classes answers the classes the action carries: the application's generated
// collection. As in Masked, a mask that refuses a class cannot be judged without them,
// so with nil classes such a mask refuses the action, and it refuses an action the
// collection does not hold (a name it registers no Execute on): the action is to be
// registered, by the generator or by hand (@manualAddResource(Execute) on its constant,
// with the @class it carries), under the name the handler asks about. A name spelled
// wrong would otherwise pass every mask that refuses by class, and nothing would say
// so.
//
// It logs nothing and writes no sentence: a route that answers whether an act is
// offered asks it, and the act itself asks RefuseActionUnderMask. It panics when no
// session is in the context, as sessioninfo.MaskFromCtx does.
func MaskRefusesAction(ctx context.Context, classes ActionClasses, action accesstypes.Resource) (why string, refused bool) {
	mask := sessioninfo.MaskFromCtx(ctx)
	if mask.IsZero() {
		return "", false
	}

	return newMaskedPermissions(nil, mask, classes).refuses(accesstypes.Execute, action)
}

// RefuseActionUnderMask refuses the action when the permission mask of the session in
// the context refuses it (MaskRefusesAction), for a handler whose action no grant
// admits. The refusal is logged as the masked check logs its own, so one line says a
// mask denied something whichever of the two asked, and the person is answered 403
// with what the session cannot do: act is the end of "this session cannot", as in
// "create a tenant". It returns nil outside a masked session and for an action the
// mask keeps.
//
//	if err := resource.RefuseActionUnderMask(ctx, router.Collection(), "CreateTenant", "create a tenant"); err != nil {
//		return httpio.NewEncoder(w).ClientMessage(ctx, err)
//	}
func RefuseActionUnderMask(ctx context.Context, classes ActionClasses, action accesstypes.Resource, act string) error {
	why, refused := MaskRefusesAction(ctx, classes, action)
	if !refused {
		return nil
	}
	mask := sessioninfo.MaskFromCtx(ctx)
	if mask.Allows(accesstypes.Execute) {
		logMaskRefusal(ctx, accesstypes.Execute, mask, maskedSessionUser(ctx), action, why)
	} else {
		logMaskKindDenied(ctx, accesstypes.Execute, mask, maskedSessionUser(ctx), action)
	}

	return httpio.NewForbiddenMessagef(
		"you are acting as someone else, and this session cannot %s: %s. What a session that acts as someone may do is set when it starts; start one that may, or stop acting as them first", act, why,
	)
}

// maskedSessionUser is who the log names beside a mask's denial of an action no grant
// admits: the person the session acts as, and for a session that acts as a role the
// person it answers for (roleSessionUser), as SessionPermissions names them.
func maskedSessionUser(ctx context.Context) accesstypes.User {
	principal := sessioninfo.PrincipalFromCtx(ctx)
	if _, ok := principal.Role(); ok {
		return roleSessionUser(ctx)
	}
	user, _ := principal.User()

	return user
}

// logMaskKindDenied logs the denial of a permission kind the mask does not keep, on
// every resource asked about.
func logMaskKindDenied(ctx context.Context, perm accesstypes.Permission, mask accesstypes.PermissionMask, user accesstypes.User, resources ...accesstypes.Resource) {
	logger.FromCtx(ctx).Warnf("impersonation: %s denied by mask (%s) for %s on %v", perm, mask, user, resources)
}

// logMaskRefusal logs the denial of a permission on one resource the mask refuses
// inside a kind it keeps, with why: the class or the name the mask refuses.
func logMaskRefusal(ctx context.Context, perm accesstypes.Permission, mask accesstypes.PermissionMask, user accesstypes.User, res accesstypes.Resource, why string) {
	logger.FromCtx(ctx).Warnf("impersonation: %s denied by mask (%s) for %s on %s: %s", perm, mask, user, res, why)
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

// HoldsAction returns the check of one person's hold on an action: whether the
// request's permissions answer Granted for Execute on it in scope. A Conditional
// answer is not a hold: an action checked by name has no row to judge a condition
// against. It is the check a route written by hand makes of its own gate, and the
// GrantCheck an application hands session.Impersonations.Request, which asks it about
// the grant of the kind of impersonation a person asked for.
func HoldsAction(perms UserPermissions, scope accesstypes.Scope) func(ctx context.Context, action accesstypes.Resource) (bool, error) {
	return func(ctx context.Context, action accesstypes.Resource) (bool, error) {
		decisions, err := perms.Check(ctx, RequestEnvironment(), scope, accesstypes.Execute, action)
		if err != nil {
			return false, errors.Wrap(err, "resource.UserPermissions.Check()")
		}

		return decisions[action].IsGranted(), nil
	}
}
