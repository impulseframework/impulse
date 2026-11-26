package resource

import (
	"context"
	"sync"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/internal/request"
)

// Caller is what the generated frame knows about a request's caller once the entry
// check has run: the checker that check ran with, the scope it ran in, and the
// environment it sampled, so a body that arms a write or a read against the
// caller evaluates the same facts the entry check did (the decision instant
// included). The generated handlers stamp it into the context before the
// application's code runs: a method's frame before Execute and Warning, and a
// computed resource's frames before its List function, its Read function and the
// function that renders a file. That code reads it back with CallerFrom. Within such
// a request a zero Caller is therefore never seen; no caller means a call the
// application makes outside any request, or a handler that stamped none.
type Caller struct {
	// Permissions is the checker the entry check ran with: the session's user, or
	// the role an act-as-role session assumed, behind the session's mask.
	Permissions UserPermissions
	// Scope is the partition the request was checked in.
	Scope accesstypes.Scope
	// Env is the decision environment the entry check sampled.
	Env accesstypes.Environment
	// collection renders a conditional grant into the live check, when the
	// frame's decoder was wired to one.
	collection *GeneratedCollection
	// namedRowSources are the views a row a write names is read from, when the
	// frame's decoder was handed them (WithNamedRowSources): a write the frame's
	// body arms (Enforce) carries them.
	namedRowSources NamedRowSources
	// method and permission are what the frame's entry check asked: the method, and the
	// permission on it. A method's decode sets them on the caller it answers
	// (RPCDecoder.DecodeCaller, and ExecuteGate.Caller for the gate a
	// TargetedRPCDecoder's decode answers). Every other caller carries neither: one a
	// gate asked through Caller.ExecuteGate answers, one As answers, and one a query
	// answers. A delete's rows that go on the permission to delete the record
	// (DeleteKind.WithinReach) are reached in another tenant where the same permission
	// holds there (reachesTenant).
	method     accesstypes.Resource
	permission accesstypes.Permission
}

type callerKey struct{}

// WithCaller stamps the caller into the context for the code that runs under it. The
// generated frames call it; a handler written by hand calls it too where what it runs
// asks who the caller is.
func WithCaller(ctx context.Context, caller *Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, caller)
}

// CallerFrom returns the caller the frame stamped, or a zero Caller when none
// was: a mutation or query armed with a zero Caller refuses at execution instead
// of running unchecked. Code that reads a zero Caller as a call of the
// application's own, as the identity library's rules do, relies on every frame
// that serves a person stamping one.
func CallerFrom(ctx context.Context) *Caller {
	if caller, ok := ctx.Value(callerKey{}).(*Caller); ok && caller != nil {
		return caller
	}

	return &Caller{}
}

// WithAuth binds the request to the auth whose sessions its outlet serves, by the auth's
// name (the auth package's Name). An outlet's generated routes bind it on every request
// (live.Subscribing), so whatever answers for the request can tell the people of one auth
// from another's: two auths over one database may each hold a person of the same username,
// and they are two people. A request its outlet's group bound already (BindAuth) keeps the
// auth the group gave it: the context is returned as it is, so nothing a route mounts
// renames a request the router bound. WithAuth cannot refuse a request; the middleware an
// outlet's generated routes run under (live.Subscribing) does, where the group's auth is
// not its own.
func WithAuth(ctx context.Context, auth string) context.Context {
	if record, ok := request.AuthFrom(ctx); ok && record.ByGroup {
		return ctx
	}

	return request.WithAuth(ctx, request.Auth{Name: auth, ByGroup: false})
}

// AuthFrom returns the name of the auth the request came through, as its outlet's group
// bound it (BindAuth) or, where no group did, as its generated routes did (WithAuth). ok
// is false when nothing bound one: a route outside every outlet, a route registered by
// hand under an outlet of an application with one auth, or a call made outside any
// request.
func AuthFrom(ctx context.Context) (auth string, ok bool) {
	record, ok := request.AuthFrom(ctx)

	return record.Name, ok
}

// WithRPC hands the request the name of the RPC method it runs, as grants name the
// method. eventsource.UserEvent and eventsource.UserProcessEvent read it there, so every
// change record the method's code writes names the method in a column of its own, and
// that code passes nothing for it. A method's generated handler calls it before Execute
// runs; a handler written by hand that runs a method's code calls it too.
func WithRPC(ctx context.Context, method accesstypes.Resource) context.Context {
	return request.WithRPC(ctx, string(method))
}

// IsZero reports whether no frame stamped the caller.
func (c *Caller) IsZero() bool {
	return c == nil || c.Permissions == nil
}

// Check answers, as data, what the caller may do: the decision for perm on each
// resource (a resource, or a field of one as "Resource.field"), evaluated in the
// caller's scope and environment. A body branches on the answers where policy
// should decide what it does, without arming a write.
func (c *Caller) Check(ctx context.Context, perm accesstypes.Permission, resources ...accesstypes.Resource) (accesstypes.Decisions, error) {
	if c.IsZero() {
		return nil, errNoCaller
	}
	decisions, err := c.Permissions.Check(ctx, c.Env, c.Scope, perm, resources...)
	if err != nil {
		return nil, errors.Wrap(err, "resource.UserPermissions.Check()")
	}

	return decisions, nil
}

// As returns the caller acting through another checker in the same scope and
// environment: a body that should act as a named role composes the checker the
// way act-as-role sessions do (RolePrincipalPermissions over access.ForRole) and
// arms its operations with the result. The actor stays who it was. The checker given
// was not asked the method the request was let in by, so the caller answered carries
// no method.
func (c *Caller) As(perms UserPermissions) *Caller {
	as := *c
	as.Permissions = perms
	as.method, as.permission = "", ""

	return &as
}

// reachesTenant reports whether the permission the request was let in by holds in a
// tenant other than the caller's: the caller carries the method its entry check asked,
// and its checker answers that method Granted or Conditional for the same permission in
// the tenant, in the caller's environment. A condition is not judged there: it was
// judged where the request was made. Denied, and a caller that carries no method, do not
// reach the tenant. The checker a session composes holds nothing in a tenant the session
// does not enter (UserPermissions.Reaches), a disabled tenant among them.
func (c *Caller) reachesTenant(ctx context.Context, tenant accesstypes.Domain) (bool, error) {
	if c.IsZero() || c.method == "" {
		return false, nil
	}
	decisions, err := c.Permissions.Check(ctx, c.Env, accesstypes.DomainScope(tenant), c.permission, c.method)
	if err != nil {
		return false, errors.Wrap(err, "resource.UserPermissions.Check()")
	}
	decision, ok := decisions[c.method]

	return ok && (decision.IsGranted() || decision.IsConditional()), nil
}

// RolePrincipalPermissions is a checker acting as a role on behalf of a real
// actor: the role's permissions answer the checks, and the actor's identity is
// what row conditions bind and change events record — the same composition an
// act-as-role session gets. It lists no domain: a body acting as a role asks
// no tenant picker.
func RolePrincipalPermissions(role RolePermissions, actor accesstypes.User) UserPermissions {
	return rosterPermissions{UserPermissionChecker: rolePrincipalPermissions{RolePermissions: role, user: actor}}
}

var errNoCaller = errors.New("no caller is stamped on the context: Enforce and Check run under a generated handler of a method or of a computed resource, " +
	"which stamps the caller before the application's code, or under a hand-written handler that called resource.WithCaller")

// arm binds the caller's checker, scope, environment, and collection to the query
// set for one operation's evaluation, or records why it cannot.
func (q *QuerySet[Resource]) arm(caller *Caller, rSet *Set[Resource], requiredPermission accesstypes.Permission) {
	if caller.IsZero() {
		q.armError = errNoCaller

		return
	}
	q.env = caller.Env
	q.collection = caller.collection
	q.namedRowSources = caller.namedRowSources
	q.EnableUserPermissionEnforcement(rSet, caller.Permissions, caller.Scope, requiredPermission)
}

// Caller returns the caller the query was checked as: the checker, the scope and the
// environment its decode bound, with the collection its conditions resolve against. It
// is what a computed resource's generated frames stamp into the context (WithCaller)
// once the decode has checked the request, before the List function, the Read function
// or the function that renders a file runs, as a method's frame stamps the caller for
// its body. Such a function reads it with CallerFrom and asks as the request's own
// check did (Caller.Check, Caller.ExecuteGate), so a list that offers what a method
// takes lists what the method would accept from this caller, and nothing the function
// calls mistakes the request for a call the application makes itself. A handler
// written by hand over a computed query stamps it the same way. A QuerySet bound to no
// permissions answers a zero Caller, which every question refuses.
func (q *QuerySet[Resource]) Caller() *Caller {
	return &Caller{Permissions: q.userPermissions, Scope: q.scope, Env: q.env, collection: q.collection, namedRowSources: q.namedRowSources}
}

// Enforce arms the query against the caller: Read and List run the read
// permission gate the resource routes run (resource then requested fields, with
// conditional grants riding the query) before touching the database. A zero
// Caller refuses at execution.
func (q *QuerySet[Resource]) Enforce(caller *Caller, rSet *Set[Resource], requiredPermission accesstypes.Permission) *QuerySet[Resource] {
	q.arm(caller, rSet, requiredPermission)

	return q
}

// Enforce arms the mutation against the caller: Apply and Buffer run the full
// pipeline the resource routes run — the static field gate against the Set, the
// fold of conditional grants, the live check against the real row inside the
// transaction, and the tenancy check — before buffering anything. A refusal is
// the same Forbidden the routes answer, naming the grant that said no. A zero
// Caller refuses at execution.
func (p *PatchSet[Resource]) Enforce(caller *Caller, rSet *Set[Resource], requiredPermission accesstypes.Permission) *PatchSet[Resource] {
	p.querySet.arm(caller, rSet, requiredPermission)

	return p
}

// SetCache holds a resource's canonical permission Sets, one per operation, built
// on first use from the generated wire struct that mirrors what the resource
// routes accept. The generated resources package declares one per resource and
// shape, so hand-written operations armed with Enforce meet exactly the field
// permissions the routes enforce.
type SetCache[Resource Resourcer, Request any] struct {
	sets sync.Map
}

// For returns the Set requiring perm on every addressable field. A delete addresses
// no field, so the Set for Delete asks it of the resource alone. Construction
// errors are programming errors in generated code and panic.
func (c *SetCache[Resource, Request]) For(perm accesstypes.Permission) *Set[Resource] {
	if cached, ok := c.sets.Load(perm); ok {
		if set, ok := cached.(*Set[Resource]); ok {
			return set
		}
	}
	set, err := operationSet[Resource, Request](perm)
	if err != nil {
		panic(errors.Wrapf(err, "resource.NewSet[%s](%s)", (*new(Resource)).Resource(), perm))
	}
	if actual, loaded := c.sets.LoadOrStore(perm, set); loaded {
		if stored, ok := actual.(*Set[Resource]); ok {
			return stored
		}
	}

	return set
}

// operationSet builds the Set a generated patch or query of one operation is armed
// with. A delete carries no field: its Set asks Delete of the resource and nothing of a
// field, which NewSet refuses to build, since a Set a request is decoded against must
// ask something of every field the request may address.
func operationSet[Resource Resourcer, Request any](perm accesstypes.Permission) (*Set[Resource], error) {
	if perm == accesstypes.Delete {
		return newDeleteSet[Resource](), nil
	}

	return NewSet[Resource, Request](perm)
}
