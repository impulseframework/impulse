package identity

import (
	"context"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/tracer"
)

// GivableRoles lists the roles a person picks from to give one by hand from the whole
// application's management, where the role is to be held (scope: the whole
// application, one tenant or every tenant), as the dialogs of the methods that give a
// role offer them: the one that gives a person a role, the create and the invitation
// that start a person with one, and the one that gives a group a role. They are the
// roles that exist there that the request's caller could give there by one of those
// methods (RoleGrants.CouldGive), and no other: a page offers what the caller may do,
// and a role every such method refuses this caller is left out, so a role file says
// which roles a person hands out once, on the methods' grants, and the list follows.
// One list serves the pickers of all of them, so it holds a role that one of them takes
// and another refuses, which the method then says.
//
// Two rules a role file does not write hold here too, as they hold in the methods. The
// Super Administrator role is offered for the whole application alone, and only to a
// caller who holds it: only a super administrator gives it. A tenant's administrator's
// role of a tenant that is directory only is offered only to a caller the tenant admits
// (entersToChange).
//
// A call outside any request is the application's own and lists every role that exists
// there; a manager a request reaches without the role grants cannot judge it, which is
// an error and never a list. It is a list's read, called outside any transaction with
// the database to ask in (db): the roles are read first, as the permission engine reads
// the custom ones on a client of its own, and the questions are then asked in one
// transaction of the list's own, which writes nothing.
func (m *Manager) GivableRoles(ctx context.Context, db resource.Executor, scope accesstypes.PolicyScope) ([]access.RoleInfo, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	roles, err := m.access.UserManager().Roles(ctx, scope)
	if err != nil {
		return nil, errors.Wrap(err, "access.UserManager.Roles()")
	}
	judged, err := m.judged(ctx)
	if err != nil {
		return nil, err
	}
	if !judged {
		return roles, nil
	}
	var offered []access.RoleInfo
	if err := db.ExecuteFunc(ctx, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		// The database may run the function again, so each run starts its own list.
		var err error
		offered, err = rolesOffered(roles, func(role accesstypes.Role) (bool, error) {
			return m.couldGiveIn(ctx, txn, scope, role)
		})

		return err
	}); err != nil {
		return nil, errors.Wrap(err, "resource.Executor.ExecuteFunc()")
	}

	return offered, nil
}

// couldGiveIn reports whether the request's caller could give the role in the scope by
// hand from the whole application's management (GivableRoles).
func (m *Manager) couldGiveIn(ctx context.Context, txn resource.ReadWriteTransaction, scope accesstypes.PolicyScope, role accesstypes.Role) (bool, error) {
	if role == accesstypes.SuperAdministrator {
		if !scope.IsGlobal() {
			return false, nil
		}
		err := m.requireSuperAdministrator(ctx, txn, "gives the Super Administrator role")
		switch {
		case httpio.HasForbidden(err):
			return false, nil
		case err != nil:
			return false, err
		}
	}
	if enters, err := m.entersToChange(ctx, scope, role, nil); err != nil || !enters {
		return false, err
	}
	could, err := m.grants.CouldGive(ctx, scope, role)
	if err != nil {
		return false, errors.Wrap(err, "identity.RoleGrants.CouldGive()")
	}

	return could, nil
}

// GivingPlaces are the places a list of the roles to give from the whole application's
// management (GivableRoles) lists roles for, from the conditions the request's filter
// holds on the tenant (resource.FilterShape.Take): one equality on a tenant's id lists
// that tenant's roles, and no condition, or an equality on no tenant, lists the roles
// held for the whole application and, where the application has tenants, the ones held
// in every tenant, which the request then narrows by its other conditions. tenantWord is
// the application's word for a tenant ("sector"), "" for an application without
// tenants. A request that names tenants any other way beside its other conditions, by a
// set, a range, an inequality or two conditions, is refused with what to send, in the
// application's word; list names the list in the refusal. A filter that joins its
// conditions by OR holds none Take can read, so it is answered over both places and
// matched row by row.
func GivingPlaces(list accesstypes.Resource, tenantWord string, onTenant []resource.Condition) ([]accesstypes.PolicyScope, error) {
	whole := []accesstypes.PolicyScope{accesstypes.GlobalPolicyScope()}
	if tenantWord != "" {
		whole = append(whole, accesstypes.EveryDomainPolicyScope())
	}
	if len(onTenant) == 0 {
		return whole, nil
	}
	if len(onTenant) != 1 || onTenant[0].Operator != filterEquals {
		return nil, httpio.NewBadRequestMessagef("%s lists the roles of one place at a time: filter it by one %s's id (domain:eq:<id>), or by none", list, tenantWord)
	}
	tenant, isText := onTenant[0].Value.(string)
	if !isText {
		return nil, httpio.NewBadRequestMessagef("%s lists the roles of one place at a time: the filter on domain carries no %s's id", list, tenantWord)
	}
	if tenant == "" {
		return whole, nil
	}

	return []accesstypes.PolicyScope{accesstypes.DomainPolicyScope(accesstypes.Domain(tenant))}, nil
}
