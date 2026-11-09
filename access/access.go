// Package access implements tools to manage access to resources.
package access

import (
	"context"
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
}

// New creates a new Client over a policy store built by one of the store
// subpackages (spannerstore, postgresstore).
//
// Permission checks are answered by the snapshot engine, compiled in-memory
// from the release's default roles (WithDefaultRoles) and the policy store,
// and kept fresh by a background heartbeat plus an optional push hint (see
// WithChangeSignal). All policy writes (user management) go through the same
// store and refresh the snapshot immediately on this instance.
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
func New(store Store, opts ...Option) (*Client, error) {
	options := defaultClientOptions()
	for _, opt := range opts {
		opt(options)
	}

	var defaults *defaultRoles
	if options.roleFileSet {
		if options.collection == nil {
			return nil, errors.New("access.WithDefaultRoles(): the collection must not be nil; the role file validates against what the release declares")
		}
		config, err := options.roleFile.Parse()
		if err != nil {
			return nil, errors.Wrap(err, "access.WithDefaultRoles()")
		}
		defaults, err = compileDefaultRoles(options.collection, config)
		if err != nil {
			return nil, errors.Wrap(err, "access.WithDefaultRoles(): the role file does not validate")
		}
	}

	manager := newStoreManager(store)
	snapEngine := newSnapshotEngine(store, defaults, options)
	manager.onPolicyChange = snapEngine.policyChanged

	return &Client{
		evaluator:   snapEngine,
		snapEngine:  snapEngine,
		userManager: newUserManager(manager, defaults, snapEngine),
		store:       store,
		defaults:    defaults,
		collection:  options.collection,
	}, nil
}

// WaitReady blocks until the first policy snapshot has loaded (or ctx
// expires). Wire it into readiness probes so instances receive traffic only
// once permission checks can be answered.
func (c *Client) WaitReady(ctx context.Context) error {
	return c.snapEngine.waitReady(ctx)
}

// Close stops the Client's background policy reloading and change-signal
// watch, and waits for a change announcement still in flight (bounded by a
// few seconds), so a policy write made right before Close reaches the other
// instances before the application closes the channel the signal rides on.
// Permission checks keep answering from the last loaded snapshot.
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
// (row-referencing ones are rejected at snapshot load — no row exists for
// them to see), and row-free conditions fold to a definite answer. A
// condition folding leaves unsettled — a subject-attribute reference, which
// is data only the database can compare — is a check error too: there is no
// place to evaluate it on this path.
//
// There is no scope validation: an unknown tenant simply holds no grants, so
// the check fails closed. Callers wanting to distinguish "invalid tenant"
// from "no permission" validate the tenant in their own guard, against the
// source that owns tenants.
func (c *Client) CheckUser(ctx context.Context, env accesstypes.Environment, user accesstypes.User, scope accesstypes.Scope, perm accesstypes.Permission) (accesstypes.Decision, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	decision, err := c.evaluator.checkUser(ctx, user, scope, perm)
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

	resourceDecisions, err := c.evaluator.checkUserResources(ctx, user, scope, perm, resources...)
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
// answered from the in-memory policy snapshot (safe inside transactions; no
// store read). It is the visibility question concealed tenancy asks: an
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
