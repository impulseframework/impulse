package session

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/internal/sessioncore"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// Impersonation is granted with a mask. To let someone impersonate a person with no
// mask is to give them everything that person holds, and the mask is the control: so
// an application declares each kind of impersonation it offers with the grant that
// allows it and the widest mask that grant gives (Impersonations), the person who
// starts a session may ask for a narrower mask and never a wider one, and the
// session's mask is the grant's, narrowed by the request. The declaration is the only
// way to start an impersonated session: a kind that gives everything says so, with
// accesstypes.AllowAll(), where the application is read.

// ImpersonationKind is one kind of impersonation an application offers: what a request
// names it by, the grant that allows starting a session of the kind, whether its
// sessions act as a person or as a role, and the widest mask the grant gives. Declare
// one with ImpersonatesUser or ImpersonatesRole.
type ImpersonationKind struct {
	name   string
	grant  accesstypes.Resource
	asRole bool
	widest accesstypes.PermissionMask
}

// ImpersonatesUser declares a kind of impersonation whose sessions act as a person.
// name is what a request names the kind by. grant is the action a person must hold
// Execute on to start a session of the kind, registered by hand in the application
// (@manualAddResource(Execute)). widest is the widest mask the grant gives, and it is
// not optional: a kind that gives everything the person holds says so, with
// accesstypes.AllowAll(), where the application is read.
//
//	session.ImpersonatesUser("view", resources.ViewAsUser, accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.List, accesstypes.Read))
//	session.ImpersonatesUser("act", resources.ActAsUser, accesstypes.AllowAll().Refusing(accesstypes.RefuseClass(router.ClassCredential)))
func ImpersonatesUser(name string, grant accesstypes.Resource, widest accesstypes.PermissionMask) ImpersonationKind {
	return ImpersonationKind{name: name, grant: grant, widest: widest}
}

// ImpersonatesRole declares a kind of impersonation whose sessions act as a role: the
// session answers for the person who started it, and holds what the role holds. name,
// grant and widest are ImpersonatesUser's.
func ImpersonatesRole(name string, grant accesstypes.Resource, widest accesstypes.PermissionMask) ImpersonationKind {
	return ImpersonationKind{name: name, grant: grant, asRole: true, widest: widest}
}

// Name returns what a request names the kind by.
func (k ImpersonationKind) Name() string {
	return k.name
}

// Grant returns the action a person must hold Execute on to start a session of the
// kind.
func (k ImpersonationKind) Grant() accesstypes.Resource {
	return k.grant
}

// AsRole reports whether the kind's sessions act as a role; otherwise they act as a
// person.
func (k ImpersonationKind) AsRole() bool {
	return k.asRole
}

// Widest returns the widest mask the kind's grant gives.
func (k ImpersonationKind) Widest() accesstypes.PermissionMask {
	return k.widest
}

// ImpersonationActions is what a declaration of impersonation is held to: the
// application's actions and the classes they carry. The generated collection is one
// (router.Collection()); an application with no generated collection names its actions
// itself (NamedActions).
type ImpersonationActions interface {
	// HasPermission reports whether the application registers permission on res
	// within scope.
	HasPermission(scope accesstypes.PermissionScope, permission accesstypes.Permission, res accesstypes.Resource) bool
	// ClassesInUse lists the classes the application's actions carry.
	ClassesInUse() []accesstypes.ActionClass
	// TagsRequiring lists the fields of the record res that carry permission
	// themselves, which are the fields a check asks about one by one.
	TagsRequiring(res accesstypes.Resource, permission accesstypes.Permission) []accesstypes.Tag
}

// NamedActions is what a declaration is held to in an application with no generated
// permission collection: a session type used on its own, whose grants are names its own
// check answers for. Each name given is an action of the application; none carries a
// class, and the application has no records. Such an application's kinds give masks of
// permission kinds, and may refuse the actions named here by name.
//
//	session.NewImpersonations(session.NamedActions("ViewAsUser", "ActAsUser"),
//	    session.ImpersonatesUser("view", "ViewAsUser", accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.List, accesstypes.Read)),
//	    session.ImpersonatesUser("act", "ActAsUser", accesstypes.AllowAll()))
func NamedActions(actions ...accesstypes.Resource) ImpersonationActions {
	return namedActions(slices.Clone(actions))
}

// namedActions is NamedActions' list.
type namedActions []accesstypes.Resource

// HasPermission reports Execute on a named action, in the application's one scope.
func (n namedActions) HasPermission(scope accesstypes.PermissionScope, permission accesstypes.Permission, res accesstypes.Resource) bool {
	return scope == accesstypes.GlobalPermissionScope && permission == accesstypes.Execute && slices.Contains(n, res)
}

// ClassesInUse reports none: a named action carries no class.
func (namedActions) ClassesInUse() []accesstypes.ActionClass {
	return nil
}

// TagsRequiring reports none: the application has no records.
func (namedActions) TagsRequiring(accesstypes.Resource, accesstypes.Permission) []accesstypes.Tag {
	return nil
}

// Impersonations is what an application declares of the impersonation it offers: its
// kinds, each with its grant and its widest mask. It is the only way to start an
// impersonated session: it alone makes the ImpersonationRequest one starts from
// (Request for a person signed in here, RequestFor for a server-side caller), so no
// session is started that no kind allows, and no application writes the grant's check
// and the mask's arithmetic by hand. One value is built when the application starts
// and serves every request.
type Impersonations struct {
	kinds   map[string]ImpersonationKind
	names   []string
	actions ImpersonationActions
}

// NewImpersonations builds the declaration from the kinds of impersonation the
// application offers and holds it to the application's actions, so a declaration that
// could not mean what it says is refused when the application starts: a kind with no
// name, a name two kinds share, a grant that is no action of the application or that
// two kinds share (a grant carries one widest mask), and a mask that keeps a kind that
// is no permission kind, or refuses a class no action carries, or names an action, a
// record or a field the application does not have.
func NewImpersonations(actions ImpersonationActions, kinds ...ImpersonationKind) (*Impersonations, error) {
	if actions == nil {
		return nil, errors.New("the impersonation an application offers is held to its actions: pass the generated collection (router.Collection())")
	}

	declared := &Impersonations{kinds: make(map[string]ImpersonationKind, len(kinds)), actions: actions}
	grants := make(map[accesstypes.Resource]string, len(kinds))
	for _, kind := range kinds {
		if kind.name == "" {
			return nil, errors.Newf("a kind of impersonation has no name (its grant is %q): a request names the kind it asks for", kind.grant)
		}
		if _, taken := declared.kinds[kind.name]; taken {
			return nil, errors.Newf("two kinds of impersonation are named %q: a request names one kind", kind.name)
		}
		if !registered(actions, accesstypes.Execute, kind.grant) {
			return nil, errors.Newf(
				"the kind %q is allowed by the grant %q, which is no action of the application: register it by hand (@manualAddResource(Execute) on the constant) and give it to a role",
				kind.name, kind.grant,
			)
		}
		if other, shared := grants[kind.grant]; shared {
			return nil, errors.Newf("the kinds %q and %q are both allowed by the grant %q: a grant carries one widest mask, so each kind takes a grant of its own", other, kind.name, kind.grant)
		}
		if problem := declared.unknown(kind.widest); problem != "" {
			return nil, errors.Newf("the widest mask of the kind %q %s", kind.name, problem)
		}
		grants[kind.grant] = kind.name
		declared.kinds[kind.name] = kind
		declared.names = append(declared.names, kind.name)
	}

	return declared, nil
}

// MustNewImpersonations is NewImpersonations panicking on a declaration it refuses, for
// an application that builds its declaration where it is constructed: the refusal is a
// mistake in the program, and it stops the application as it starts.
func MustNewImpersonations(actions ImpersonationActions, kinds ...ImpersonationKind) *Impersonations {
	declared, err := NewImpersonations(actions, kinds...)
	if err != nil {
		panic(err)
	}

	return declared
}

// registered reports whether the application registers perm on res, for the whole
// application or for a tenant.
func registered(actions ImpersonationActions, perm accesstypes.Permission, res accesstypes.Resource) bool {
	return actions.HasPermission(accesstypes.GlobalPermissionScope, perm, res) || actions.HasPermission(accesstypes.DomainPermissionScope, perm, res)
}

// unknown holds a mask to the application's actions and says, as the rest of a
// sentence about the mask, what it names that the application does not have: a kind
// that is no permission kind, a class no action carries, or an action, a record or a
// field of no such name. It answers "" for a mask that names nothing of the sort. A
// kind or a refusal that names nothing would keep or refuse nothing, and whoever wrote
// it would believe otherwise.
func (i *Impersonations) unknown(mask accesstypes.PermissionMask) string {
	if kinds := mask.UnknownKinds(); len(kinds) > 0 {
		return fmt.Sprintf("keeps the kind %q, which is no permission kind: the kinds are %s, spelled so", string(kinds[0]), permissionList(accesstypes.PermissionKinds()))
	}

	inUse := i.actions.ClassesInUse()
	for _, refusal := range mask.Refusals() {
		if class, ok := refusal.Class(); ok {
			if !slices.Contains(inUse, class) {
				return fmt.Sprintf("refuses the class %q, which no action of the application carries (the classes in use: %s)", string(class), classList(inUse))
			}

			continue
		}
		perm, res, _ := refusal.Named()
		if problem := i.unknownNamed(perm, res); problem != "" {
			return problem
		}
	}

	return ""
}

// unknownNamed holds one refusal that names a resource to the application's actions,
// and answers as unknown does. Three things are something to refuse: a permission the
// application registers on the name (Execute on an action, Update on a record); a
// field of a record that carries the permission itself, which a check asks about by
// its own name (Record.field); and any of the five permissions of a record on a name
// that is a record, whether or not the application offers that one, so the three
// writes of a record that is never deleted are refused in one breath
// (accesstypes.RefuseWrites) and the refusal holds as it reads.
func (i *Impersonations) unknownNamed(perm accesstypes.Permission, res accesstypes.Resource) string {
	if registered(i.actions, perm, res) {
		return ""
	}
	if record, field, isField := strings.Cut(string(res), "."); isField {
		if slices.Contains(i.actions.TagsRequiring(accesstypes.Resource(record), perm), accesstypes.Tag(field)) {
			return ""
		}

		return fmt.Sprintf(
			"refuses %s on the field %q, and the application checks no %s on a field of that name: "+
				"a field is asked about by itself only when it carries the permission, so name such a field, or name the record to refuse all of it",
			perm, string(res), perm,
		)
	}
	if perm != accesstypes.Execute && isRecord(i.actions, res) {
		return ""
	}

	return fmt.Sprintf("refuses %s on %q, and the application registers no %s on a resource of that name", perm, string(res), perm)
}

// recordPermissions are the permissions a record is registered with; an action is
// registered with Execute.
var recordPermissions = []accesstypes.Permission{accesstypes.List, accesstypes.Read, accesstypes.Create, accesstypes.Update, accesstypes.Delete}

// isRecord reports whether the application has a record named res: it registers one of
// a record's permissions on the name, for the whole application or for a tenant.
func isRecord(actions ImpersonationActions, res accesstypes.Resource) bool {
	return slices.ContainsFunc(recordPermissions, func(perm accesstypes.Permission) bool {
		return registered(actions, perm, res)
	})
}

// classList renders the classes in use for a refusal.
func classList(classes []accesstypes.ActionClass) string {
	if len(classes) == 0 {
		return "none"
	}
	names := make([]string, len(classes))
	for i, class := range classes {
		names[i] = string(class)
	}

	return strings.Join(names, ", ")
}

// Kinds returns the declared kinds, in the order they were declared.
func (i *Impersonations) Kinds() []ImpersonationKind {
	kinds := make([]ImpersonationKind, len(i.names))
	for n, name := range i.names {
		kinds[n] = i.kinds[name]
	}

	return kinds
}

// ImpersonationAsk is what a person asks for when they start an impersonated session:
// the kind, whom or what to act as, and optionally a narrower mask than the kind gives.
type ImpersonationAsk struct {
	// Kind names the declared kind of impersonation.
	Kind string
	// Principal is whom or what the session acts as: for a kind that acts as a person,
	// the person as the permission store names them (the id of their account, for a
	// session type whose users are accounts); for a kind that acts as a role, the
	// role's name.
	Principal string
	// Mask is the mask the person asks for. The zero mask asks for none, and the
	// session takes the kind's widest. One that names kinds keeps those, each of which
	// the kind must give; what it refuses is refused beside what the kind refuses.
	// Choosing a narrower mask is a precaution anyone may take.
	Mask accesstypes.PermissionMask
	// Reason is the free-text justification recorded on the impersonation record.
	Reason string
	// MaxDuration shortens the session's hard cap below the configured impersonation
	// timeout; zero or a longer value leaves the cap unchanged.
	MaxDuration time.Duration
}

// ImpersonationActor names the real person a session is started on behalf of, for a
// call that arrives on no session of this application: a server-side caller, which has
// established who is acting by its own means. A call that arrives on a person's own
// signed-in session names nobody: the declaration reads the actor off that session
// (Impersonations.Request).
type ImpersonationActor struct {
	// Name is the actor: as the system that signed them in names them, or, with no
	// Realm, the username of a person of this application. Required.
	Name string
	// Realm names the application or identity provider that signed the actor in, when
	// it is not this application ("admin-portal"). The actor then holds nothing here
	// and is recorded under the name given. Leave it empty only for a person of this
	// application, whose own live session the call names in SessionID.
	Realm string
	// SessionID is the actor's own session. With a Realm it is their session in the
	// other system, optional, kept for correlation across the two. With no Realm it is
	// required and verified: a live session of this application, not itself
	// impersonated, that carries the actor's username.
	SessionID impulse.NullUUID
}

// GrantCheck answers whether whoever is asking holds a grant: Execute on the action,
// in the place the application checks it. resource.HoldsAction builds one from the
// request's permissions, for a person signed in here. A server-side caller passes a
// check of its own: whether the system that handed the actor over may start a session
// of the kind, by whatever it authenticated that system with.
type GrantCheck func(ctx context.Context, grant accesstypes.Resource) (bool, error)

// Request turns what a person signed in here asks for into the request an impersonated
// session starts from, or refuses it with nothing started. It is the way a route behind
// ValidateSession starts one: the actor is the user of the validated session the call
// arrives on, and that session is the actor's own, which the library verifies when the
// session is established. It asks holds whether the person holds the grant of the kind
// they named, and computes the session's mask: the kind's widest when the request names
// none, and otherwise the request's, which may only be narrower
// (accesstypes.PermissionMask.Narrow: the kinds are the request's, each of which the
// grant must give, and the refusals are joined). A request for more than the grant
// gives is refused, never trimmed, so nobody believes they hold what they do not.
//
// The answer is refused with a client error a handler passes on as it is: 400 for a
// kind the application does not offer, for no principal, and for a mask that keeps a
// kind that is no permission kind or refuses a class or a resource the application
// does not have; 403 for a person who does not hold the grant, and for a mask wider
// than the grant's. A call that carries no validated session is a mistake in the
// program and answers a plain error: a server-side caller names its actor with
// RequestFor.
func (i *Impersonations) Request(ctx context.Context, ask *ImpersonationAsk, holds GrantCheck) (*ImpersonationRequest, error) {
	current, ok := ctx.Value(sessioninfo.CtxSessionInfo).(*sessioninfo.SessionData)
	if !ok || current == nil || current.SessionInfo == nil {
		return nil, errors.New(
			"session.Impersonations.Request() reads the actor off the signed-in session the call arrives on, and this call carries none: " +
				"mount the route behind ValidateSession, or name the actor of a server-side call with RequestFor",
		)
	}

	return i.request(ctx, &ImpersonationActor{
		Name:      current.Username,
		SessionID: impulse.NullUUID{UUID: current.ID, Valid: !current.ID.IsNil()},
	}, ask, holds, "starting a session of the kind %q takes Execute on %s, which you do not hold")
}

// RequestFor is Request for a server-side caller: a call that arrives on no session of
// this application and names the actor itself, typically a hand-off from the system
// that signed the actor in (an administration application starting a session in a
// partner portal). The caller declares its kinds as any application does and passes
// its own check as holds: whether the calling system, which the handler has
// authenticated, may start a session of the kind for this actor. The kind's widest
// mask, the narrowing and the refusals are Request's.
//
// An actor with a Realm was signed in by another system and is recorded under the name
// given. An actor with no Realm is a person of this application, whose own live
// session the call names (ImpersonationActor.SessionID) and the library verifies. A
// call with no actor's name is refused (400). Every session type takes the request
// alike: AuthAPI.StartImpersonatedSession and PreauthAPI.StartImpersonatedSession.
//
// Two calls are mistakes in the program and answer a plain error. One that arrives on
// a validated session of this application names nobody: the actor is that session's
// user, which Request reads off it, so a signed-in person cannot name another. And an
// actor with neither a Realm nor a SessionID is nobody the library can vouch for.
func (i *Impersonations) RequestFor(ctx context.Context, actor ImpersonationActor, ask *ImpersonationAsk, holds GrantCheck) (*ImpersonationRequest, error) {
	if current, ok := ctx.Value(sessioninfo.CtxSessionInfo).(*sessioninfo.SessionData); ok && current != nil {
		return nil, errors.New(
			"session.Impersonations.RequestFor() names the actor of a call that arrives on no session of this application, and this call carries a signed-in session: " +
				"its user is the actor, so make the request with Request, which reads the actor off that session",
		)
	}
	if actor.Name == "" {
		return nil, httpio.NewBadRequestMessage("an impersonated session acts on behalf of someone: name the actor (ImpersonationActor.Name)")
	}
	if actor.Realm == "" && !actor.SessionID.Valid {
		return nil, errors.Newf(
			"the actor %q is named with no Realm and no SessionID: name the system that signed them in (ImpersonationActor.Realm), "+
				"or, for a person of this application, their own live session (ImpersonationActor.SessionID)",
			actor.Name,
		)
	}

	return i.request(ctx, &actor, ask, holds, "this caller may not start a session of the kind %q: the application's check of the kind's grant, %s, refused it")
}

// request is what Request and RequestFor share once the actor is known: the kind, the
// grant's check and the mask's arithmetic. It is the one place a request an
// impersonated session starts from is made. notHeld is the sentence a caller the check
// refuses is answered with, which takes the kind's name and its grant: a person signed
// in here does not hold the grant, and a server-side caller's own check said no.
func (i *Impersonations) request(ctx context.Context, actor *ImpersonationActor, ask *ImpersonationAsk, holds GrantCheck, notHeld string) (*ImpersonationRequest, error) {
	if i == nil {
		return nil, errors.New("the application declares no impersonation: declare the kinds it offers (session.NewImpersonations) before a request is made")
	}
	// An impersonated session starts no other: refused here, before the grant is asked
	// about under the impersonated person's permissions, as it is when a session starts.
	if sessioncore.ImpersonatedContext(ctx) {
		return nil, httpio.NewForbiddenMessage("an impersonated session cannot establish another impersonated session")
	}
	if holds == nil {
		return nil, errors.New(
			"a request to impersonate is made against a check of the kind's grant, and none was passed: " +
				"pass resource.HoldsAction over the request's permissions, or the server-side caller's own check",
		)
	}
	if ask == nil {
		// Nothing asked names no kind, and is refused as a kind nobody offers.
		ask = &ImpersonationAsk{}
	}
	kind, ok := i.kinds[ask.Kind]
	if !ok {
		return nil, httpio.NewBadRequestMessagef("%q is not a kind of impersonation this application offers: it offers %s", ask.Kind, strings.Join(i.names, ", "))
	}
	if ask.Principal == "" {
		return nil, httpio.NewBadRequestMessagef("a session of the kind %q acts as someone or something: name the principal", kind.name)
	}

	held, err := holds(ctx, kind.grant)
	if err != nil {
		return nil, errors.Wrap(err, "session.GrantCheck()")
	}
	if !held {
		return nil, httpio.NewForbiddenMessagef(notHeld, kind.name, kind.grant)
	}

	if problem := i.unknown(ask.Mask); problem != "" {
		return nil, httpio.NewBadRequestMessagef("the mask asked for %s", problem)
	}
	mask, err := kind.widest.Narrow(ask.Mask)
	if err != nil {
		var wider *accesstypes.WiderMaskError
		if !errors.As(err, &wider) {
			return nil, errors.Wrap(err, "accesstypes.PermissionMask.Narrow()")
		}

		return nil, httpio.NewForbiddenMessagef(
			"the kind %q gives %s, and the request asks for %s as well: ask for no more than the kind gives, or name no mask to take all of it",
			kind.name, kind.widest.Words(), permissionList(wider.Beyond),
		)
	}

	principal := accesstypes.UserPrincipal(accesstypes.User(ask.Principal))
	if kind.asRole {
		principal = accesstypes.RolePrincipal(accesstypes.Role(ask.Principal))
	}

	return &ImpersonationRequest{
		kind: kind.name,
		core: sessioncore.ImpersonationRequest{
			Actor:           actor.Name,
			ActorRealm:      actor.Realm,
			SourceSessionID: actor.SessionID,
			Principal:       principal,
			Mask:            mask,
			Reason:          ask.Reason,
			MaxDuration:     ask.MaxDuration,
		},
	}, nil
}

// permissionList renders permission kinds for a sentence: "Update", "Update and Delete".
func permissionList(perms []accesstypes.Permission) string {
	names := make([]string, len(perms))
	for i, perm := range perms {
		names[i] = string(perm)
	}
	if len(names) < 2 {
		return strings.Join(names, "")
	}

	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}
