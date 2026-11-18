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
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// Impersonation is granted with a mask. To let someone impersonate a person with no
// mask is to give them everything that person holds, and the mask is the control: so
// an application declares each kind of impersonation it offers with the grant that
// allows it and the widest mask that grant gives (Impersonations), the person who
// starts a session may ask for a narrower mask and never a wider one, and the
// session's mask is the grant's, narrowed by the request.

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
// (router.Collection()).
type ImpersonationActions interface {
	// HasPermission reports whether the application registers permission on res
	// within scope.
	HasPermission(scope accesstypes.PermissionScope, permission accesstypes.Permission, res accesstypes.Resource) bool
	// ClassesInUse lists the classes the application's actions carry.
	ClassesInUse() []accesstypes.ActionClass
}

// Impersonations is what an application declares of the impersonation it offers: its
// kinds, each with its grant and its widest mask. It turns a person's request to start
// a session into the ImpersonationRequest the session library establishes (Request),
// so no application writes the grant's check and the mask's arithmetic by hand. One
// value is built when the application starts and serves every request.
type Impersonations struct {
	kinds   map[string]ImpersonationKind
	names   []string
	actions ImpersonationActions
}

// NewImpersonations builds the declaration from the kinds of impersonation the
// application offers and holds it to the application's actions, so a declaration that
// could not mean what it says is refused when the application starts: a kind with no
// name, a name two kinds share, a grant that is no action of the application or that
// two kinds share (a grant carries one widest mask), and a mask that refuses a class
// no action carries or a permission the application does not register on the resource
// it names.
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
// sentence about the mask, what it names that the application does not have: a class
// no action carries, or a permission the application registers on no resource of the
// name. It answers "" for a mask whose every refusal names something. A refusal that
// names nothing would refuse nothing, and whoever wrote it would believe otherwise.
func (i *Impersonations) unknown(mask accesstypes.PermissionMask) string {
	inUse := i.actions.ClassesInUse()
	for _, refusal := range mask.Refusals() {
		if class, ok := refusal.Class(); ok {
			if !slices.Contains(inUse, class) {
				return fmt.Sprintf("refuses the class %q, which no action of the application carries (the classes in use: %s)", string(class), classList(inUse))
			}

			continue
		}
		perm, res, _ := refusal.Named()
		if !registered(i.actions, perm, res) {
			return fmt.Sprintf("refuses %s on %q, and the application registers no %s on a resource of that name", perm, string(res), perm)
		}
	}

	return ""
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

// GrantCheck answers whether the person asking holds a grant: Execute on the action,
// in the place the application checks it. resource.HoldsAction builds one from the
// request's permissions.
type GrantCheck func(ctx context.Context, grant accesstypes.Resource) (bool, error)

// Request turns what a person asks for into the request the session library
// establishes, or refuses it with nothing started. It asks holds whether the person
// holds the grant of the kind they named, and computes the session's mask: the kind's
// widest when the request names none, and otherwise the request's, which may only be
// narrower (accesstypes.PermissionMask.Narrow: the kinds are the request's, each of
// which the grant must give, and the refusals are joined). A request for more than the
// grant gives is refused, never trimmed, so nobody believes they hold what they do not.
//
// The answer is refused with a client error a handler passes on as it is: 400 for a
// kind the application does not offer, for no principal, and for a mask that refuses a
// class or a resource the application does not have; 403 for a person who does not
// hold the grant, and for a mask wider than the grant's.
//
// When ctx carries a validated session, the request names its user as the actor and it
// as the actor's own session, which is what establishing from a signed-in session
// requires. A server-to-server caller sets Actor and ActorRealm on the result.
func (i *Impersonations) Request(ctx context.Context, ask *ImpersonationAsk, holds GrantCheck) (*ImpersonationRequest, error) {
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
		return nil, httpio.NewForbiddenMessagef("starting a session of the kind %q takes Execute on %s, which you do not hold", kind.name, kind.grant)
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

	req := &ImpersonationRequest{
		Principal:   accesstypes.UserPrincipal(accesstypes.User(ask.Principal)),
		Mask:        mask,
		Reason:      ask.Reason,
		MaxDuration: ask.MaxDuration,
	}
	if kind.asRole {
		req.Principal = accesstypes.RolePrincipal(accesstypes.Role(ask.Principal))
	}
	if current, ok := ctx.Value(sessioninfo.CtxSessionInfo).(*sessioninfo.SessionData); ok && current != nil && current.SessionInfo != nil {
		req.Actor = current.Username
		req.SourceSessionID = impulse.NullUUID{UUID: current.ID, Valid: !current.ID.IsNil()}
	}

	return req, nil
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
