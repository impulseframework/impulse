package identity

import (
	"context"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/tracer"
)

// GivableRoles lists the roles a person picks from to give one by hand from the whole
// application's management with one method (method: the one that gives a person a
// role, the create or the invitation that starts a person with one, or the one that
// gives a group a role, by the name its grant is written under), where the role is to
// be held (scope: the whole application, one tenant or every tenant), as the method's
// dialog offers them. They are the roles that exist there that the request's caller
// could give there with that method (RoleGrants.CouldGive), and no other: a dialog
// offers what its own method takes from the caller, so a role file says which roles a
// person hands out once, on each method's grant, and each method's list follows its
// own. A role the invitation's grant admits and the grant of the method that gives a
// person a role refuses is in the invitation's list and not in the other's.
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
func (m *Manager) GivableRoles(ctx context.Context, db resource.Executor, method accesstypes.Resource, scope accesstypes.PolicyScope) ([]access.RoleInfo, error) {
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
			return m.couldGiveIn(ctx, txn, method, scope, role)
		})

		return err
	}); err != nil {
		return nil, errors.Wrap(err, "resource.Executor.ExecuteFunc()")
	}

	return offered, nil
}

// couldGiveIn reports whether the request's caller could give the role in the scope by
// hand from the whole application's management with the method named (GivableRoles).
func (m *Manager) couldGiveIn(ctx context.Context, txn resource.ReadWriteTransaction, method accesstypes.Resource, scope accesstypes.PolicyScope, role accesstypes.Role) (bool, error) {
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
	could, err := m.grants.CouldGive(ctx, method, scope, role)
	if err != nil {
		return false, errors.Wrap(err, "identity.RoleGrants.CouldGive()")
	}

	return could, nil
}

// GivingMethod reads the one method a list of the roles to give was asked about, from
// the conditions the request's filter holds on the method (resource.FilterShape.Take):
// one equality on the name of one of the methods whose role field picks from the list
// (methods), as its grant is written in the role file. Each of those methods has a
// grant of its own, and a role one takes from the caller another may refuse, so the
// list answers for one method at a time and holds no rows for a request that names
// none, for which ok is false: a dialog that asked without its method would otherwise
// offer what its method refuses. A request that names methods any other way, by a set,
// a range or two conditions, or names a method that picks no role from the list, is
// refused with what to send; list names the list in the refusal. A filter that joins
// its conditions by OR holds none Take can read, so it names no method and is listed
// no role.
func GivingMethod(list accesstypes.Resource, methods []accesstypes.Resource, named []resource.Condition) (method accesstypes.Resource, ok bool, err error) {
	if len(named) == 0 {
		return "", false, nil
	}
	if len(named) != 1 || named[0].Operator != filterEquals {
		return "", false, httpio.NewBadRequestMessagef("%s lists the roles one method gives at a time: filter it by one method's name alone (method:eq:<name>), one of %s", list, methodNames(methods))
	}
	name, isText := named[0].Value.(string)
	if !isText || !slices.Contains(methods, accesstypes.Resource(name)) {
		return "", false, httpio.NewBadRequestMessagef("%s lists the roles one method gives at a time, and no method that picks a role from it is named %v: name one of %s (method:eq:<name>)", list, named[0].Value, methodNames(methods))
	}

	return accesstypes.Resource(name), true, nil
}

// methodNames lists methods as a refusal names them.
func methodNames(methods []accesstypes.Resource) string {
	names := make([]string, 0, len(methods))
	for _, method := range methods {
		names = append(names, string(method))
	}

	return strings.Join(names, ", ")
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
// conditions by OR holds none Take can read, so it names both places here, and no
// method the list could answer for (GivingMethod).
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
