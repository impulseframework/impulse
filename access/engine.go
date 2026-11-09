package access

import (
	"context"

	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
)

// evaluator answers permission checks on the request path; the snapshot
// engine implements it. Implementations must be safe for concurrent use.
type evaluator interface {
	// checkUser returns user's scope-wide (no resource attachment) decision
	// within scope: granted on any unconditional cover, the covering
	// conditions when only conditional grants cover it — always row-free,
	// enforced at load — denied otherwise.
	checkUser(ctx context.Context, user accesstypes.User, scope accesstypes.Scope, perm accesstypes.Permission) (resourceDecision, error)

	// checkUserResources returns user's decision for each resource within
	// scope, aligned with the input order: granted on any unconditional
	// cover, the covering conditions when only conditional grants cover it,
	// denied otherwise.
	checkUserResources(
		ctx context.Context, user accesstypes.User, scope accesstypes.Scope, perm accesstypes.Permission, resources ...accesstypes.Resource,
	) ([]resourceDecision, error)

	// userDigest returns user's structural grant enumeration within scope:
	// every resource and field the user's grants reach, permission → granted
	// or conditional, with denied targets absent. Non-folding by design —
	// the answer is a pure function of the policy snapshot.
	userDigest(ctx context.Context, user accesstypes.User, scope accesstypes.Scope) (accesstypes.PermissionDigest, error)

	// userHasGrants reports whether user holds at least one grant in scope,
	// answered from the policy snapshot — the visibility question concealed
	// tenancy asks (a caller with no grants in a domain is answered as if the
	// domain did not exist).
	userHasGrants(ctx context.Context, user accesstypes.User, scope accesstypes.Scope) (bool, error)

	// userPermissions returns user's effective permissions within scope by
	// name — the management listing, answered from the same snapshot the
	// checks are.
	userPermissions(ctx context.Context, user accesstypes.User, scope accesstypes.Scope) (accesstypes.UserScopePermissions, error)

	// checkRole returns role's scope-wide decision within scope: what
	// checkUser answers a member holding only that role, minus the
	// membership lookup.
	checkRole(ctx context.Context, role accesstypes.Role, scope accesstypes.Scope, perm accesstypes.Permission) (resourceDecision, error)

	// checkRoleResources returns role's decision for each resource within
	// scope, aligned with the input order — the role twin of
	// checkUserResources.
	checkRoleResources(
		ctx context.Context, role accesstypes.Role, scope accesstypes.Scope, perm accesstypes.Permission, resources ...accesstypes.Resource,
	) ([]resourceDecision, error)

	// roleDigest returns role's structural grant enumeration within scope —
	// the role twin of userDigest.
	roleDigest(ctx context.Context, role accesstypes.Role, scope accesstypes.Scope) (accesstypes.PermissionDigest, error)

	// roleHasGrants reports whether role holds at least one grant in scope —
	// the role twin of userHasGrants.
	roleHasGrants(ctx context.Context, role accesstypes.Role, scope accesstypes.Scope) (bool, error)

	// rolePermissions returns role's effective grants within scope by name:
	// the release's file grants for a default role, the store's for a custom
	// one, inheritance folded — the role twin of userPermissions.
	rolePermissions(ctx context.Context, role accesstypes.Role, scope accesstypes.Scope) (accesstypes.RolePermissionCollection, error)
}

// policyStore is the management surface for role membership, custom role
// existence, and grants, each held where its PolicyScope says. Validation
// (role existence, empty-input checks) belongs to the callers; implementations
// only persist and query policy.
type policyStore interface {
	// Membership
	addUserRole(ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User, role accesstypes.Role) error
	deleteUserRole(ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User, role accesstypes.Role) error
	userRoles(ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User) ([]accesstypes.Role, error)
	// userMemberships lists every membership the user holds, wherever it is
	// held, as stored.
	userMemberships(ctx context.Context, user accesstypes.User) ([]policy.Membership, error)

	// Roles. A custom role is created with the description its creator gave
	// it, and listed with it.
	addRole(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, description string) error
	roles(ctx context.Context, scope accesstypes.PolicyScope) ([]RoleInfo, error)
	// deleteRole removes the role and its grants, scoped to (scope, role).
	deleteRole(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) (bool, error)
	roleExists(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) (bool, error)
	roleUsers(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) ([]accesstypes.User, error)

	// Grants. A scope-wide grant attaches a permission to no resource; it is a
	// separate write, never a distinguished resource value.
	addGrant(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, perm accesstypes.Permission, resource accesstypes.Resource, condition string) error
	addGrants(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, rows []GrantRow) error
	changeGrants(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, removals, additions []GrantRow) error
	removeGrant(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, perm accesstypes.Permission, resource accesstypes.Resource, condition string) error
	removeGrants(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, perm accesstypes.Permission, resource accesstypes.Resource) error
	addScopeWideGrant(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, perm accesstypes.Permission) error
	removeScopeWideGrant(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, perm accesstypes.Permission) error
	roleGrants(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) (accesstypes.RolePermissionCollection, error)
	roleGrantConditions(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) (map[accesstypes.Permission]map[accesstypes.Resource][]string, error)
}
