package access

import (
	"context"

	"github.com/impulseframework/impulse/accesstypes"
)

var _ Controller = &Client{}

// Controller is the main interface for access control operations.
type Controller interface {
	// CheckUser returns the Decision for whether user holds perm scope-wide
	// (attached to no resource) within scope. env is the request's decision
	// context; the check folds environment-referencing conditions against it
	// and fails loudly when a referenced attribute is absent.
	CheckUser(
		ctx context.Context, env accesstypes.Environment, user accesstypes.User, scope accesstypes.Scope, perm accesstypes.Permission,
	) (accesstypes.Decision, error)

	// CheckUserResources returns the Decision for each resource for whether
	// user holds perm on it within scope, all answered from one policy
	// snapshot. env is the request's decision context; the check folds
	// environment-referencing conditions against it and fails loudly when a
	// referenced attribute is absent.
	CheckUserResources(
		ctx context.Context, env accesstypes.Environment, user accesstypes.User, scope accesstypes.Scope, perm accesstypes.Permission, resources ...accesstypes.Resource,
	) (accesstypes.Decisions, error)

	// CheckRole returns the Decision for whether role holds perm scope-wide
	// within scope: what CheckUser answers a member holding only that role,
	// minus the membership lookup. env is the request's decision context;
	// the check folds environment-referencing conditions against it and
	// fails loudly when a referenced attribute is absent.
	CheckRole(
		ctx context.Context, env accesstypes.Environment, role accesstypes.Role, scope accesstypes.Scope, perm accesstypes.Permission,
	) (accesstypes.Decision, error)

	// CheckRoleResources returns the Decision for each resource for whether
	// role holds perm on it within scope, all answered from one policy
	// snapshot — the role twin of CheckUserResources, with the same
	// Environment, grouping, and fail-closed semantics.
	CheckRoleResources(
		ctx context.Context, env accesstypes.Environment, role accesstypes.Role, scope accesstypes.Scope, perm accesstypes.Permission, resources ...accesstypes.Resource,
	) (accesstypes.Decisions, error)

	// UserHasGrants reports whether user holds at least one grant in scope,
	// answered from the in-memory policy snapshot — the visibility question
	// concealed tenancy asks and the predicate a tenant picker filters the
	// application's tenant list by (see Client.UserHasGrants).
	UserHasGrants(ctx context.Context, user accesstypes.User, scope accesstypes.Scope) (bool, error)

	// UserPermissionDigest returns user's structural grant enumeration within
	// scope — the frontend digest payload: resource → permission → granted or
	// conditional, denied targets absent, nothing folded (see
	// Client.UserPermissionDigest).
	UserPermissionDigest(ctx context.Context, user accesstypes.User, scope accesstypes.Scope) (accesstypes.PermissionDigest, error)

	// ForUser returns the request-bound permission checker for user, whose
	// method set structurally satisfies the resource package's UserPermissions
	// seam. Test doubles implement it in one line over NewUserChecker.
	ForUser(user accesstypes.User) *UserChecker

	// RoleHasGrants reports whether role holds at least one grant in scope —
	// the foothold a session operating as the role has there (see
	// Client.RoleHasGrants).
	RoleHasGrants(ctx context.Context, role accesstypes.Role, scope accesstypes.Scope) (bool, error)

	// RolePermissionDigest returns role's structural grant enumeration within
	// scope — the frontend digest payload for a session operating as the role
	// (see Client.RolePermissionDigest).
	RolePermissionDigest(ctx context.Context, role accesstypes.Role, scope accesstypes.Scope) (accesstypes.PermissionDigest, error)

	// ForRole returns the request-bound permission checker for role, whose
	// method set structurally satisfies the resource package's
	// RolePermissions seam. Test doubles implement it in one line over
	// NewRoleChecker.
	ForRole(role accesstypes.Role) *RoleChecker

	// UserManager returns the UserManager for managing users, roles, and permissions.
	UserManager() UserManager

	// PolicyChanged tells the engine that policy rows were committed outside
	// its own write path: an application wrote a membership inside a
	// transaction of its own (see UserManager.Membership) and committed it.
	// The next check reloads the policy before it answers (see
	// Client.PolicyChanged).
	PolicyChanged()

	// Handlers returns HTTP handlers for access management with validation and logging.
	Handlers(handler LogHandler) Handlers
}

var _ UserManager = &userManager{}

// UserManager manages role membership, custom roles and their grants. Where a
// membership, a role or a grant is held is an accesstypes.PolicyScope: the
// global partition, one tenant domain, or every tenant domain. Scopes are
// opaque partition labels: no method validates tenant existence — the
// application owns its tenant list and validates tenants at its own
// boundaries.
//
// Roles are of two kinds. The release's default roles come from the role file
// (WithDefaultRoles): a global default is held in the global partition, a
// domain default in every tenant domain; they exist without a store row, a
// membership names them by name, their grants are the file's and are read
// here, never written. Custom roles are the store's: created, granted and
// deleted through this interface, held where they were created. A default
// role's name is taken: a custom role cannot be created under it, and a
// custom role that predates a default of the same name is shadowed by it (see
// ShadowedRole).
//
// The base-name/Resources-suffix pairing is the API's naming standard: the
// base method addresses a permission held scope-wide (attached to no
// resource); the Resources variant addresses specific resources.
type UserManager interface {
	// AddRoleUsers assigns role to users, the memberships held in scope.
	// Errors if role doesn't exist in scope: a default role of scope's kind
	// or a custom role held in scope — or, for one domain, in every domain.
	// A custom role held in one domain alone cannot be assigned in every
	// domain.
	AddRoleUsers(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, users ...accesstypes.User) error

	// AddUserRoles assigns roles to user, the memberships held in scope.
	// Errors if any role doesn't exist in scope, as AddRoleUsers defines it.
	AddUserRoles(ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User, roles ...accesstypes.Role) error

	// DeleteRoleUsers removes users from role, the memberships held in scope.
	// Errors if role doesn't exist.
	DeleteRoleUsers(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, users ...accesstypes.User) error

	// DeleteUserRoles removes user's memberships of roles held in scope.
	DeleteUserRoles(ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User, roles ...accesstypes.Role) error

	// Membership answers the key of the row that says user holds role in
	// scope, for a caller that writes it inside a transaction of its own
	// instead of through AddUserRoles. Errors as AddUserRoles does: role
	// doesn't exist in scope, or user is empty. A custom role is read through
	// held, the caller's reader of the roles table over that transaction; a
	// default role needs no read. The caller commits the row and then calls
	// Controller.PolicyChanged. A row to remove needs no check and is spelled
	// by NewMembershipKey.
	Membership(ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User, role accesstypes.Role, held RoleReader) (MembershipKey, error)

	// UserRoles returns user's memberships as stored, keyed by where each is
	// held: a membership held in every domain is one entry under
	// EveryDomainPolicyScope, never one per domain. With no scopes it lists
	// every membership the user holds; with scopes, the memberships held in
	// each, an entry per scope asked.
	UserRoles(ctx context.Context, user accesstypes.User, scopes ...accesstypes.PolicyScope) (accesstypes.RoleCollection, error)

	// UserPermissions returns user's effective permissions in each scope —
	// what the user holds there, from memberships held in the scope and in
	// every domain, default and custom roles alike, answered from the policy
	// snapshot. At least one scope is required.
	UserPermissions(ctx context.Context, user accesstypes.User, scopes ...accesstypes.Scope) (accesstypes.UserPermissionCollection, error)

	// AddRole creates a custom role held in scope, with the description
	// saying what it is for: one or two plain sentences, refused when empty.
	// The name follows the one grammar every role name does, letters and
	// digits starting with a capital letter (see Role), and a name outside it
	// is refused. Errors if the role exists in scope, or its name is a
	// default role's.
	AddRole(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, description string) error

	// ResolveRole returns the role scope holds under name, matched without
	// regard to case, in the spelling the store holds it: a default role of
	// scope's kind, a custom role held in scope, or — for one domain — a
	// custom role held in every domain. The exact spelling wins when it
	// exists; otherwise the one role that matches case-insensitively. Two
	// such roles with no exact match is an error, never a guess. A
	// directory names roles without regard to case (a Google group is an
	// email address), so the name it asserts resolves here to the spelling
	// the role file or AddRole gave the role.
	ResolveRole(ctx context.Context, scope accesstypes.PolicyScope, name string) (accesstypes.Role, bool, error)

	// Roles returns the roles that exist in scope, sorted by name, each with
	// its description: the default roles of scope's kind, described by the
	// role file, and the custom roles held in scope, and in every domain for
	// one domain, described as AddRole was told. A name is listed once
	// however many places hold it: a default before a custom role it
	// shadows, the scope's own custom role before one held in every domain.
	Roles(ctx context.Context, scope accesstypes.PolicyScope) ([]RoleInfo, error)

	// DeleteRole removes a custom role held in scope with its grants. Returns
	// false with an error if the role has members in scope, or is a default
	// role. The delete is scoped to the given scope.
	DeleteRole(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) (bool, error)

	// AddRolePermission grants permission to role scope-wide: the permission is
	// held with no resource attachment. Errors if role is not a custom role
	// held in scope.
	AddRolePermission(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, permission accesstypes.Permission) error

	// DeleteRolePermission removes a scope-wide permission from role. Errors
	// if role is not a custom role held in scope.
	DeleteRolePermission(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, permission accesstypes.Permission) error

	// AddRolePermissionResources grants permission on resources to role.
	// Errors if role is not a custom role held in scope.
	AddRolePermissionResources(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, permission accesstypes.Permission, resources ...accesstypes.Resource) error

	// AddRoleGrant grants permission on one resource to role, limited by
	// condition ("" is unconditional). Errors if role is not a custom role
	// held in scope.
	AddRoleGrant(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, permission accesstypes.Permission, resource accesstypes.Resource, condition string) error

	// AddRoleGrants writes the grants to role as one store write rather than
	// one per grant; grants the role already holds are left as they are.
	// Errors if role is not a custom role held in scope or a grant names no
	// resource.
	AddRoleGrants(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, grants ...GrantRow) error

	// ChangeRoleGrants removes the removals from role and adds the additions
	// to it as one store write: a reader sees the role's grants before the
	// change or after it, never between, and a failure leaves them as they
	// were. Additions the role already holds are left as they are; removals
	// it does not hold are ignored. Errors if role is not a custom role held
	// in scope or a grant names no resource.
	ChangeRoleGrants(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, removals, additions []GrantRow) error

	// DeleteRolePermissionResources removes every grant of permission on the
	// resources from role, whatever their conditions. Errors if role is not
	// a custom role held in scope.
	DeleteRolePermissionResources(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, permission accesstypes.Permission, resources ...accesstypes.Resource) error

	// DeleteRoleGrant removes one grant: permission on resource under
	// condition ("" is the unconditional grant); other conditions on the
	// resource stay. Errors if role is not a custom role held in scope.
	DeleteRoleGrant(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, permission accesstypes.Permission, resource accesstypes.Resource, condition string) error

	// DeleteAllRolePermissions removes all permissions from role, scope-wide
	// and resource-specific alike. Errors if role is not a custom role held
	// in scope.
	DeleteAllRolePermissions(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) error

	// RoleUsers returns the users whose membership of role is held in scope,
	// as stored.
	RoleUsers(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) ([]accesstypes.User, error)

	// RolePermissions returns the permissions role holds in scope — a place a
	// request is — and how each is granted (scope-wide, on resources, or
	// both), inheritance folded, answered from the policy snapshot: the
	// file's grants for a default role, the store's for a custom one. Errors
	// if role doesn't exist in scope.
	RolePermissions(ctx context.Context, scope accesstypes.Scope, role accesstypes.Role) (accesstypes.RolePermissionCollection, error)

	// RoleGrants returns the role's resource grants as held, keyed by
	// permission and resource, with the conditions each resource is granted
	// under (sorted; "" is the unconditional grant): the store's rows for a
	// custom role held in scope (or, for one domain, in every domain), the
	// file's grants for a default role. Scope-wide grants are not included.
	// Errors if role doesn't exist in scope.
	RoleGrants(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) (map[accesstypes.Permission]map[accesstypes.Resource][]string, error)
}
