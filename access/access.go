// Package access implements tools to manage access to resources.
package access

import (
	"context"
	"reflect"
	"slices"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/accesstypes/condition"
	"github.com/impulseframework/impulse/tracer"
)

var _ Controller = &Client{}

// Client is the main access control client for permission checking and user management.
type Client struct {
	evaluator   evaluator
	snapEngine  *snapshotEngine
	userManager *userManager
	store       Store
	defaults    *defaultRoles
	collection  PermissionCollection
	// superMethods are the methods the client was built to let a super
	// administrator execute (WithSuperAdministratorMethods).
	superMethods []accesstypes.Resource
	// tenantAdministrators are the roles the client was told administer a
	// tenant (WithTenantAdministratorRoles).
	tenantAdministrators []accesstypes.Role
}

// New creates a new Client over a policy store built by one of the store
// subpackages (spannerstore, postgresstore) and the permission collection of
// the running release: what the release declares, its permissions, resources
// and fields. The collection is required, and New refuses a nil one, a nil
// pointer of a collection's type included. A stored grant naming what the
// release does not declare (after a rollback, or once a release drops a
// resource or a permission) is left out of the snapshot and reported as a
// SkippedGrant instead of being carried as a grant nothing can check, a grant
// that names a permission and no resource among them; the writes that give a
// custom role a grant refuse a name the release does not declare
// (UserManager.AddRoleGrant, UserManager.AddRolePermission), by the rule the
// load skips by, and hold a grant on a resource to the rest of the role
// file's rule as well, its scope and its condition; and the role file
// validates against it.
//
// Permission checks are answered by the snapshot engine, compiled in-memory
// from the release's default roles (WithDefaultRoles) and the policy store.
// The snapshot is kept fresh by the checks themselves: one that finds it older
// than the heartbeat interval (WithHeartbeatInterval), or behind a change this
// instance wrote or was hinted of (WithChangeSignal), reads the store before
// it answers, on the engine's own context and never the caller's, so a check
// is safe inside a database transaction. A background heartbeat and the push
// hint do the same ahead of the checks; nothing depends on them, so an
// instance that got no CPU between requests is no staler than the interval.
// All policy writes (user management) go through the same store and are read
// by the next check on this instance.
//
// The store's values — domains, users, resources — are opaque labels to
// access: referential validity belongs to the callers that write them, and
// checks fail closed on anything unknown. Where a request is — a tenant
// domain or the global partition — is an accesstypes.Scope; where policy is
// held — the global partition, one domain or every domain — is an
// accesstypes.PolicyScope; neither is ever a distinguished value.
//
// New fails when the role file does not parse or does not validate against
// the collection: a release whose default roles are wrong does not start.
func New(store Store, collection PermissionCollection, opts ...Option) (*Client, error) {
	if IsNilCollection(collection) {
		return nil, errors.New("the collection must not be nil: pass the permission collection the release declares, which every grant is held to: the generated router's Collection(), or for an application with several sites the union of theirs (access.UnionCollection)")
	}
	options := defaultClientOptions()
	for _, opt := range opts {
		opt(options)
	}
	options.collection = collection

	// Every Client defines the Super Administrator role, with or without a role
	// file: the file's roles are compiled beside it.
	config := &RoleConfig{}
	if options.roleFileSet {
		var err error
		config, err = options.roleFile.Parse()
		if err != nil {
			return nil, errors.Wrap(err, "access.WithDefaultRoles()")
		}
	}
	// The methods a super administrator always runs are the application's wiring, not
	// the role file's content: one the release does not declare is refused as that,
	// before the file's grants are compiled.
	if _, err := superAdministratorRole(collection, options.superMethods); err != nil {
		return nil, errors.Wrap(err, "access.New()")
	}
	defaults, err := compileDefaultRoles(collection, config, options.superMethods)
	if err != nil {
		if !options.roleFileSet {
			return nil, errors.Wrap(err, "access.New()")
		}

		return nil, errors.Wrap(err, "access.WithDefaultRoles(): the role file does not validate")
	}

	manager := newStoreManager(store)
	snapEngine := newSnapshotEngine(store, defaults, options)
	manager.onPolicyChange = snapEngine.policyChanged

	return &Client{
		evaluator:            snapEngine,
		snapEngine:           snapEngine,
		userManager:          newUserManager(manager, defaults, snapEngine, options.tenantAdministrators, collection),
		store:                store,
		defaults:             defaults,
		collection:           collection,
		superMethods:         slices.Clone(options.superMethods),
		tenantAdministrators: slices.Clone(options.tenantAdministrators),
	}, nil
}

// IsNilCollection reports whether collection is no collection: the nil
// interface, or a nil pointer of a collection's type, which a variable nothing
// was assigned to hands over (var collection *resource.GeneratedCollection).
// The interface that holds such a pointer is not nil, so a comparison with nil
// lets it through, and the first question the engine asks it panics. New and
// UnionCollection refuse what it reports, and an auth that takes the collection
// as a setting asks it before it opens anything, so that its refusal names the
// setting: this is the one place that decides what a nil collection is.
//
// A struct value that wraps a nil collection is not reported: nothing short of
// asking it a question tells it from a collection. New asks its first question
// of the collection, the actions it lists, before it reads the role file or the
// store, so an application that hands one over stops when it starts.
func IsNilCollection(collection PermissionCollection) bool {
	if collection == nil {
		return true
	}
	value := reflect.ValueOf(collection)

	return value.Kind() == reflect.Pointer && value.IsNil()
}

// WaitReady blocks until the first policy snapshot has loaded (or ctx
// expires). Wire it into readiness probes so instances receive traffic only
// once permission checks can be answered.
func (c *Client) WaitReady(ctx context.Context) error {
	return c.snapEngine.waitReady(ctx)
}

// Close stops the Client's policy reloading, the background loop's and the
// checks' own, and its change-signal watch. A policy write hands its
// announcement to the change signal before it returns, so a write that has
// returned before Close has announced already, and the application closes
// the channel the signal rides on after Close. Close does not wait for a
// write still running on another goroutine. Permission checks keep answering
// from the last loaded snapshot, and read the store no more.
func (c *Client) Close() error {
	return c.snapEngine.close()
}

// Handlers returns the Handlers for enforcing access control
func (c *Client) Handlers(logHandler LogHandler) Handlers {
	return newHandler(c, logHandler)
}

// CheckPolicy reads the policy store once and reports everything a deploy
// should hear about before the release takes traffic: the shapes the role
// file carries that are legal and probably not what the author wanted
// (GrantWarning, ConcealingKeyWarning), and what the store holds that this
// release cannot use as written — a grant it would skip (*SkippedGrant), a
// custom role a default of the same name shadows (*ShadowedRole), memberships
// naming a role nothing defines (*OrphanedMembership). It writes nothing: the
// default roles are the release's and the store holds no row for them, so
// there is nothing to reconcile. A migrate step calls it and prints each
// warning on its own line; a store that cannot be read is the error.
func (c *Client) CheckPolicy(ctx context.Context) ([]Warning, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	records, err := c.store.ReadPolicy(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "access.Store.ReadPolicy()")
	}
	_, findings, err := newSnapshot(records, c.defaults, c.collection, time.Now())
	if err != nil {
		return nil, errors.Wrap(err, "newSnapshot()")
	}

	var warnings []Warning
	if c.defaults != nil {
		warnings = append(warnings, c.defaults.warnings...)
	}
	for _, f := range findings {
		warnings = append(warnings, f)
	}

	return warnings, nil
}

// CheckUser returns the Decision for whether user holds perm scope-wide —
// attached to no resource — within scope.
//
// The Environment is the per-request decision context (sampled once per
// request). The engine folds condition facts against it at check time; a
// condition referencing an attribute the Environment does not carry is a
// check error, never a silent allow or deny. The answer here is always
// Granted or Denied: a scope-wide grant carries only row-free conditions
// (a row-referencing one is left out at snapshot load and reported as a
// SkippedGrant — no row exists for it to see), and row-free conditions fold
// to a definite answer. A condition folding leaves unsettled — a
// subject-attribute reference, which is data only the database can compare —
// is a check error too: there is no place to evaluate it on this path.
//
// There is no scope validation: an unknown tenant simply holds no grants, so
// the check fails closed. Callers wanting to distinguish "invalid tenant"
// from "no permission" validate the tenant in their own guard, against the
// source that owns tenants.
func (c *Client) CheckUser(ctx context.Context, env accesstypes.Environment, user accesstypes.User, scope accesstypes.Scope, perm accesstypes.Permission) (accesstypes.Decision, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	decision, err := c.evaluator.checkUser(ctx, env, user, scope, perm)
	if err != nil {
		return accesstypes.Denied(), err
	}
	settled, err := settleScopeWide(decision, factsFor(env, user))
	if err != nil {
		return accesstypes.Denied(), errors.Wrapf(err, "scope-wide conditions for user %q in scope %s", user, scope)
	}

	return settled, nil
}

// CheckUserResources returns the Decision for each resource for whether user
// holds perm on it within scope — all answered from a single policy
// snapshot, so a revocation can never land between two answers of the same
// call. A resource is either a parent name ("employees") or a single field
// on a parent ("employees.name"). Per resource: Denied when no grant covers
// it, Granted when at least one unconditional grant covers it (conditions on
// other grants are moot), Conditional when only conditional grants cover it.
//
// Condition facts fold before the Decisions build: each covering set's
// any-of combination is evaluated against the environment's instant and the
// user's identity, so a set folding TRUE answers Granted, one folding FALSE
// answers Denied, and a Conditional decision carries only what the database
// must still evaluate — the engine folds facts, and never renders SQL. A
// condition referencing an attribute the Environment does not carry is a
// check error, never a silent allow or deny.
//
// Grouping is the engine's job — only the engine owns grant-set identity: a
// Conditional decision carries one ConditionGroup whose Resources lists
// every resource checked in this call sharing that covering-grant set, and
// the same group value appears in each member's Decision, so callers
// deduplicate by sorted-Resources equality. The distinct groups are exactly
// a write check's boolean list — OR across a group's grants inside its
// Condition, AND across groups computed by the caller — and a denial names
// the failing group's Resources. See CheckUser for the Environment and scope
// semantics.
func (c *Client) CheckUserResources(
	ctx context.Context, env accesstypes.Environment, user accesstypes.User, scope accesstypes.Scope, perm accesstypes.Permission, resources ...accesstypes.Resource,
) (accesstypes.Decisions, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	resourceDecisions, err := c.evaluator.checkUserResources(ctx, env, user, scope, perm, resources...)
	if err != nil {
		return nil, err
	}

	decisions, err := newDecisions(resources, resourceDecisions, factsFor(env, user))
	if err != nil {
		return nil, errors.Wrapf(err, "conditions for user %q in scope %s", user, scope)
	}

	return decisions, nil
}

// factsFor builds the condition-folding facts for one user check: the
// checked user's identity on top of the environment's facts. Absence stays
// absent — a condition referencing a missing fact fails the fold loudly,
// which is the designed posture.
func factsFor(env accesstypes.Environment, user accesstypes.User) condition.Facts {
	return environmentFacts(env).WithSubject(string(user))
}

// environmentFacts builds the facts every check shares: the request's instant
// when the environment carries one, and nothing else. A role check folds with
// exactly these — a role has no identity of its own, so no subject fact is
// available to it.
func environmentFacts(env accesstypes.Environment) condition.Facts {
	facts := condition.NewFacts()
	if now, ok := env.Now(); ok {
		facts = facts.WithNow(now)
	}
	if zone, ok := env.Zone(); ok {
		facts = facts.WithZone(zone)
	}

	return facts
}

// settleScopeWide translates a scope-wide engine decision into the public
// Decision: granted and denied pass through, and conditional coverage —
// always row-free, enforced at snapshot load — folds against the facts to a
// definite answer. A condition folding leaves unsettled needs data no
// scope-wide check can reach, and is an error rather than a guess.
func settleScopeWide(decision resourceDecision, facts condition.Facts) (accesstypes.Decision, error) {
	switch {
	case decision.granted:
		return accesstypes.Granted(), nil
	case len(decision.conditions) == 0:
		return accesstypes.Denied(), nil
	}

	result, err := condition.Fold(anyOf(decision.exprs), facts)
	if err != nil {
		return accesstypes.Denied(), errors.Wrap(err, "condition.Fold()")
	}
	truth, settled := result.(condition.Truth)
	if !settled {
		return accesstypes.Denied(), errors.Newf("did not fold to a definite answer: %q needs data no scope-wide check can reach", result)
	}
	if !truth.Value {
		return accesstypes.Denied(), nil
	}

	return accesstypes.Granted(), nil
}

// CheckRole returns the Decision for whether role holds perm scope-wide —
// attached to no resource — within scope: what CheckUser answers a member
// holding only that role, minus the membership lookup. It is the scope-wide
// check for a session operating as a role principal, and an introspection
// tool for policy tooling.
//
// The Environment and scope semantics are CheckUser's. A role has no identity
// of its own, so the folding facts carry the request's instant and nothing
// else; a scope-wide condition that needs a subject is a check error here
// exactly as it is for a user.
func (c *Client) CheckRole(ctx context.Context, env accesstypes.Environment, role accesstypes.Role, scope accesstypes.Scope, perm accesstypes.Permission) (accesstypes.Decision, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	decision, err := c.evaluator.checkRole(ctx, role, scope, perm)
	if err != nil {
		return accesstypes.Denied(), err
	}
	settled, err := settleScopeWide(decision, environmentFacts(env))
	if err != nil {
		return accesstypes.Denied(), errors.Wrapf(err, "scope-wide conditions for role %q in scope %s", role, scope)
	}

	return settled, nil
}

// CheckRoleResources returns the Decision for each resource for whether role
// holds perm on it within scope — the role twin of CheckUserResources, with
// the same single-snapshot, Decision, grouping, Environment, and scope
// semantics. A Conditional decision's payload is what the database must still
// evaluate; a subject term in it is bound at render time to the session's
// effective identity by the resource layer, never here — a role has no
// identity of its own.
func (c *Client) CheckRoleResources(
	ctx context.Context, env accesstypes.Environment, role accesstypes.Role, scope accesstypes.Scope, perm accesstypes.Permission, resources ...accesstypes.Resource,
) (accesstypes.Decisions, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	resourceDecisions, err := c.evaluator.checkRoleResources(ctx, role, scope, perm, resources...)
	if err != nil {
		return nil, err
	}

	decisions, err := newDecisions(resources, resourceDecisions, environmentFacts(env))
	if err != nil {
		return nil, errors.Wrapf(err, "conditions for role %q in scope %s", role, scope)
	}

	return decisions, nil
}

// UserHasGrants reports whether user holds at least one grant in scope,
// answered from the in-memory policy snapshot (safe inside transactions: a
// reload it waits on runs on the engine's own context). It is the visibility
// question concealed tenancy asks: an
// application hiding tenant existence answers a caller with no grants in a
// domain exactly as if the domain did not exist, while a caller with any
// foothold proceeds to ordinary permission checks. It is also the predicate a
// tenant picker filters the application's own tenant list by — access holds
// no tenant list, so it never enumerates domains. Role membership that
// resolves to no grants is not a foothold; a membership held in every domain
// is a foothold in each.
func (c *Client) UserHasGrants(ctx context.Context, user accesstypes.User, scope accesstypes.Scope) (bool, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	return c.evaluator.userHasGrants(ctx, user, scope)
}

// UserIsSuperAdministrator reports whether user holds the Super Administrator
// role (accesstypes.SuperAdministrator), answered from the in-memory policy
// snapshot like the checks (safe inside transactions). It is
// the question a session's reach into a tenant asks of the session's actor:
// a super administrator enters every tenant, whatever the tenant's sign-in
// settings say. The one other person a tenant's sign-in settings do not keep
// out is that tenant's own administrator (UserAdministersTenant), in that
// tenant alone.
func (c *Client) UserIsSuperAdministrator(ctx context.Context, user accesstypes.User) (bool, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	return c.evaluator.userIsSuperAdministrator(ctx, user)
}

// SuperAdministratorMethods returns the methods the client lets a super
// administrator execute whatever the role file grants anybody: the ones it
// was built with (WithSuperAdministratorMethods), none without the option.
// The identity library holds the engine it is handed to the methods its
// auth's generated management names (identity.New), so an application that
// built the engine without them does not start.
func (c *Client) SuperAdministratorMethods() []accesstypes.Resource {
	return slices.Clone(c.superMethods)
}

// UserPermissionDigest returns user's structural grant enumeration within
// scope: every resource and field the user's grants reach, each mapping
// permission → granted (an unconditional cover exists) or conditional (only
// conditional grants cover it), with denied targets absent — fail-closed by
// construction. It is the payload of the frontend's per-scope permission
// digest and is advisory only; enforcement stays with the Check seams.
//
// Unlike the Check seams the digest never folds: it reports grant structure
// with no environment instant and no row data, so a payload is stable for
// the life of a policy snapshot and caches cleanly per scope. Scope-wide
// grants attach to no resource and are not enumerated. See CheckUser for the
// scope semantics — an unknown tenant simply holds no grants, so its digest
// is empty.
func (c *Client) UserPermissionDigest(ctx context.Context, user accesstypes.User, scope accesstypes.Scope) (accesstypes.PermissionDigest, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	return c.evaluator.userDigest(ctx, user, scope)
}

// ForUser returns the request-bound permission checker for user — the
// canonical implementation of the resource package's UserPermissions seam.
// See UserChecker.
func (c *Client) ForUser(user accesstypes.User) *UserChecker {
	return NewUserChecker(c, user)
}

// RoleHasGrants reports whether role holds at least one grant in scope, own
// or inherited, answered from the in-memory policy snapshot — the foothold
// question UserHasGrants answers for a user, asked for a session operating as
// the role: concealed tenancy answers a role with no grants in a domain
// exactly as if the domain did not exist. A role that exists but resolves to
// no grants has no foothold; a domain default role has one in every domain.
func (c *Client) RoleHasGrants(ctx context.Context, role accesstypes.Role, scope accesstypes.Scope) (bool, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	return c.evaluator.roleHasGrants(ctx, role, scope)
}

// RolePermissionDigest returns role's structural grant enumeration within
// scope — the frontend digest payload for a session operating as the role,
// with UserPermissionDigest's semantics: granted or conditional per resource
// and permission, denied targets absent, nothing folded, scope-wide grants
// not enumerated.
func (c *Client) RolePermissionDigest(ctx context.Context, role accesstypes.Role, scope accesstypes.Scope) (accesstypes.PermissionDigest, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	return c.evaluator.roleDigest(ctx, role, scope)
}

// ForRole returns the request-bound permission checker for role — the
// canonical implementation of the resource package's RolePermissions seam,
// for sessions operating as a role principal. See RoleChecker.
func (c *Client) ForRole(role accesstypes.Role) *RoleChecker {
	return NewRoleChecker(c, role)
}

// UserManager returns the UserManager for managing users, roles, and permissions.
func (c *Client) UserManager() UserManager {
	return c.userManager
}
