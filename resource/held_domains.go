package resource

import (
	"context"
	"slices"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/logger"
)

// HeldDomains lists the roster's domains where the session's person holds a grant,
// sorted; never nil. The grant is the person's own, asked before the admission
// attenuates it: in a tenant the session does not reach the admitted checker holds
// nothing, and the person holds the tenant all the same. A session that runs with a
// role's grants asks the checker of the real person behind it, never the role's: a
// role holds what its grants say in every tenant alike, which is nobody's tenants. A
// person with no account here holds none.
func (p rosterPermissions) HeldDomains(ctx context.Context) ([]accesstypes.Domain, error) {
	held := []accesstypes.Domain{}
	if p.roster == nil {
		return held, nil
	}
	checker := p.UserPermissionChecker
	if p.admission != nil {
		checker = p.admission.UserPermissionChecker
	}
	if p.person != nil {
		checker = p.person()
		if checker == nil {
			return held, nil
		}
	}
	all, err := p.roster(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "resource.DomainRoster")
	}
	for _, domain := range all {
		has, err := checker.HasGrants(ctx, accesstypes.DomainScope(domain))
		if err != nil {
			return nil, errors.Wrap(err, "resource.UserPermissionChecker.HasGrants()")
		}
		if has {
			held = append(held, domain)
		}
	}
	slices.Sort(held)

	return slices.Compact(held), nil
}

// HeldDomains passes through: a mask attenuates what the session may do, and takes no
// tenant from the person the session acts as.
func (m *maskedPermissions) HeldDomains(ctx context.Context) ([]accesstypes.Domain, error) {
	held, err := m.UserPermissions.HeldDomains(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "resource.UserPermissions.HeldDomains()")
	}

	return held, nil
}

// HeldDomains refuses: the session acts as a role the application runs no sessions as,
// so there is no checker to ask what anybody holds. A rule that counts a person's
// tenants is not told none, which would let the rule pass; it is refused, as every
// check of such a session is denied.
func (u unsupportedRolePermissions) HeldDomains(ctx context.Context) ([]accesstypes.Domain, error) {
	logger.FromCtx(ctx).Warnf("impersonation: role principal %s has no role-bound checker; the tenants its person holds are not read", u.role)

	return nil, httpio.NewForbiddenMessagef("this session acts as the role %s, which this application runs no sessions as: what its person holds cannot be read, so nothing that depends on it is done", u.role)
}
