package identity

import (
	"context"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/tracer"
)

// DirectoryRole holds the role a tenant is opened to its directory's people with to its
// rules, in the caller's transaction, and writes nothing. The role is given to every
// person the directory vouches for at their first sign-in through it
// (DirectorySeated), to people nobody named, so the person who names it is held to what
// giving it by hand needs:
//
//   - the role exists in the tenant, by the rule a membership's role is held to: a role
//     of the release's role file held in a tenant, or a custom role of this tenant;
//   - the caller could give it there directly: the grant of the method that gives a
//     role in the tenant admits it for that role (RoleGrants.MayGive). The grant is
//     asked for no person in particular, since the people are not known yet, so a grant
//     whose condition reads the person a role is given to admits no default role.
//
// A role the caller may not give is Forbidden, and the grant answers before the role is
// looked for, so the refusal says the same of a role the tenant holds and of one it
// does not: a caller learns of no role the grant keeps from them. A role the caller
// could give that the tenant does not hold is a BadRequest. Each is a sentence the
// person can act on. A call outside any request is the application's own act and is
// not judged by the grant; a manager a request reaches without the role grants cannot
// judge it, which is an error and never a pass.
func (m *Manager) DirectoryRole(ctx context.Context, txn resource.ReadWriteTransaction, tenant accesstypes.Domain, role accesstypes.Role) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	scope := accesstypes.DomainPolicyScope(tenant)
	judged, err := m.judged(ctx)
	if err != nil {
		return err
	}
	if judged {
		may, err := m.grants.MayGive(ctx, txn, scope, impulse.NilUUID, role)
		if err != nil {
			return errors.Wrap(err, "identity.RoleGrants.MayGive()")
		}
		if !may {
			return httpio.NewForbiddenMessagef("you may not give the role %s in this %s, so you cannot make it the role everyone from its directory gets: choose a role you may give, or ask someone who may give this one", string(role), m.tenantWord)
		}
	}
	exists, err := m.roleExistsIn(ctx, txn, role, []accesstypes.PolicyScope{scope})
	if err != nil {
		return err
	}
	if !exists {
		return httpio.NewBadRequestMessagef("%q is not a role of this %s: choose one of the roles the picker lists", string(role), m.tenantWord)
	}

	return nil
}
