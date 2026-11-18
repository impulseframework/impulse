package access

import (
	"context"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
)

// UserResourceChecker is the engine surface a UserChecker delegates to: the
// check call that answers enforcement, the digest and foothold calls that
// answer the frontend's advisory questions, and the questions of who is a
// super administrator and who administers a tenant. *Client satisfies it; test
// doubles that script permission checks satisfy it with the same check method
// they already fake plus digest, foothold, super administrator and tenant
// administrator stubs.
type UserResourceChecker interface {
	CheckUserResources(
		ctx context.Context, env accesstypes.Environment, user accesstypes.User, scope accesstypes.Scope, perm accesstypes.Permission, resources ...accesstypes.Resource,
	) (accesstypes.Decisions, error)

	UserPermissionDigest(ctx context.Context, user accesstypes.User, scope accesstypes.Scope) (accesstypes.PermissionDigest, error)

	UserHasGrants(ctx context.Context, user accesstypes.User, scope accesstypes.Scope) (bool, error)

	UserIsSuperAdministrator(ctx context.Context, user accesstypes.User) (bool, error)

	UserAdministersTenant(ctx context.Context, user accesstypes.User, domain accesstypes.Domain) (bool, error)
}

// UserChecker is the request-bound permission checker for one user: the
// canonical implementation of the resource package's UserPermissions seam.
// Its method set structurally satisfies that interface without either package
// importing the other; an application's per-request accessor is one line —
// return client.ForUser(user).
//
// A UserChecker binds the user only. Scope and Environment arrive per check
// from the caller, so one value serves every check in a request.
type UserChecker struct {
	checker UserResourceChecker
	user    accesstypes.User
}

// NewUserChecker returns a UserChecker for user over checker. Applications
// use Client.ForUser (or Controller.ForUser); this constructor exists so test
// doubles implementing UserResourceChecker can satisfy Controller.ForUser in
// one line.
func NewUserChecker(checker UserResourceChecker, user accesstypes.User) *UserChecker {
	return &UserChecker{checker: checker, user: user}
}

// Check returns the Decision for perm on each of resources within scope for
// the bound user. It is a pure delegate to CheckUserResources — see there for
// the Decision, grouping, Environment, and scope semantics.
func (u *UserChecker) Check(
	ctx context.Context, env accesstypes.Environment, scope accesstypes.Scope, perm accesstypes.Permission, resources ...accesstypes.Resource,
) (accesstypes.Decisions, error) {
	decisions, err := u.checker.CheckUserResources(ctx, env, u.user, scope, perm, resources...)
	if err != nil {
		return nil, errors.Wrap(err, "access.UserResourceChecker.CheckUserResources()")
	}

	return decisions, nil
}

// PermissionDigest returns the bound user's structural grant enumeration
// within scope — the frontend digest payload. It is a pure delegate to
// UserPermissionDigest; see Client.UserPermissionDigest for the non-folding
// semantics.
func (u *UserChecker) PermissionDigest(ctx context.Context, scope accesstypes.Scope) (accesstypes.PermissionDigest, error) {
	digest, err := u.checker.UserPermissionDigest(ctx, u.user, scope)
	if err != nil {
		return nil, errors.Wrap(err, "access.UserResourceChecker.UserPermissionDigest()")
	}

	return digest, nil
}

// HasGrants reports whether the bound user holds at least one grant in scope
// — the foothold a tenant picker filters the application's tenant list by,
// and the visibility question concealed tenancy asks. It is a pure delegate
// to UserHasGrants; see Client.UserHasGrants for the foothold semantics.
func (u *UserChecker) HasGrants(ctx context.Context, scope accesstypes.Scope) (bool, error) {
	has, err := u.checker.UserHasGrants(ctx, u.user, scope)
	if err != nil {
		return false, errors.Wrap(err, "access.UserResourceChecker.UserHasGrants()")
	}

	return has, nil
}

// SuperAdministrator reports whether the bound user holds the Super
// Administrator role: the question the resource package asks of a session's
// actor before it holds the session to a tenant's sign-in settings. It is a
// pure delegate to UserIsSuperAdministrator.
func (u *UserChecker) SuperAdministrator(ctx context.Context) (bool, error) {
	is, err := u.checker.UserIsSuperAdministrator(ctx, u.user)
	if err != nil {
		return false, errors.Wrap(err, "access.UserResourceChecker.UserIsSuperAdministrator()")
	}

	return is, nil
}

// AdministersTenant reports whether the bound user administers the tenant:
// the question the resource package asks of a session's actor before a
// directory-only tenant keeps a password session out. It is a pure delegate
// to UserAdministersTenant.
func (u *UserChecker) AdministersTenant(ctx context.Context, domain accesstypes.Domain) (bool, error) {
	administers, err := u.checker.UserAdministersTenant(ctx, u.user, domain)
	if err != nil {
		return false, errors.Wrap(err, "access.UserResourceChecker.UserAdministersTenant()")
	}

	return administers, nil
}

// User returns the bound user.
func (u *UserChecker) User() accesstypes.User {
	return u.user
}
