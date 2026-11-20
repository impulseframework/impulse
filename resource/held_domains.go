package resource

import (
	"context"
	"slices"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
)

// heldDomainLister is what the permissions SessionPermissions composes answer beside
// UserPermissions: every tenant the session's person holds a grant in, whatever the
// session's mask leaves of it and whether or not the session's sign-in enters it.
// HeldDomains asks it.
type heldDomainLister interface {
	HeldDomains(ctx context.Context) ([]accesstypes.Domain, error)
}

var (
	_ heldDomainLister = rosterPermissions{}
	_ heldDomainLister = (*maskedPermissions)(nil)
)

// HeldDomains lists every tenant the session's person holds: the domains of the
// application's roster where the person holds at least one grant, sorted; never nil.
//
// It answers a question about the person, where Domains answers one about the session.
// Domains lists where this session stands, so it leaves out a tenant the session's
// permission mask leaves nothing of, and a tenant that is closed to the way the session
// signed in (ClosedDomains). The person holds both. A rule that counts a person's
// tenants asks here, so that no mask a session runs under makes a tenant not theirs:
// the generated create-your-tenant route does, for its rule that a person creates one
// tenant, and a session that acts as a person under a mask that keeps the creation and
// hides the person's tenant is refused a second one like the person themselves.
//
// The permissions SessionPermissions composes answer it. In a session that acts as a
// person, the person is the one acted as, whose grants the session runs with. Any other
// UserPermissions answers with the domains it lists, which is all it can say.
func HeldDomains(ctx context.Context, perms UserPermissions) ([]accesstypes.Domain, error) {
	lister, ok := perms.(heldDomainLister)
	if !ok {
		listed, err := perms.Domains(ctx)
		if err != nil {
			return nil, errors.Wrap(err, "resource.UserPermissions.Domains()")
		}
		if listed == nil {
			listed = []accesstypes.Domain{}
		}

		return listed, nil
	}
	held, err := lister.HeldDomains(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "resource.HeldDomains()")
	}

	return held, nil
}

// HeldDomains lists the roster's domains where the checker holds a grant, sorted; never
// nil. The grant is the checker's own, asked before the admission attenuates it: in a
// tenant the session does not reach the admitted checker holds nothing, and the person
// holds the tenant all the same.
func (p rosterPermissions) HeldDomains(ctx context.Context) ([]accesstypes.Domain, error) {
	held := []accesstypes.Domain{}
	if p.roster == nil {
		return held, nil
	}
	checker := p.UserPermissionChecker
	if p.admission != nil {
		checker = p.admission.UserPermissionChecker
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
	return HeldDomains(ctx, m.UserPermissions)
}
