package identity

import (
	"context"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/tracer"
)

// GivableRoles lists the roles a person picks from to give one by hand from the whole
// application's management with one method (method: the one that gives a person a
// role, the invitation that starts a person with one, or the one that gives a group a
// role, by the name its grant is written under), where the role is to
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
//
// It is the list of a method that gives a person a role, or starts one with it. The
// method that gives a group a role picks from GivableGroupRoles, which holds each role
// to the group's own rule too.
func (m *Manager) GivableRoles(ctx context.Context, db resource.Executor, method accesstypes.Resource, scope accesstypes.PolicyScope) ([]access.RoleInfo, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	return m.givable(ctx, db, method, scope, nil)
}

// GivableGroupRoles lists the roles a person picks from to give a group one by hand
// from the whole application's management, with the method that gives a group a role
// (method), where the role is to be held (scope): the roles GivableRoles lists there
// for the method that the group may hold there. The group's rule is the one the method
// holds its row to before it writes it (access.GroupMayHold), asked of where the
// group's row says it is kept, read in the list's transaction, so the list offers what
// the method takes: never the Super Administrator role, which a person holds through
// no group, and for a group kept in one tenant that tenant's roles alone, so no role
// for the whole application, for every tenant or for another tenant. A group that does
// not exist is listed no role, as a group that may hold none there is: the list never
// says whether an id is a group's, and the method refuses the id when it is asked. The
// rule is the group's and not the caller's: a call outside any request is held to it
// too. A tenant's administrator's role of a tenant that is directory only is held to that
// rule as the method holds it: for a group kept in that tenant it is offered only to a
// caller the tenant admits, and a group kept for the whole application makes nobody the
// tenant's administrator, so its list offers the role to any caller the grant lets give it.
func (m *Manager) GivableGroupRoles(ctx context.Context, db resource.Executor, method accesstypes.Resource, groupID impulse.UUID, scope accesstypes.PolicyScope) ([]access.RoleInfo, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	return m.givable(ctx, db, method, scope, &groupID)
}

// givable lists the roles that exist in scope that the request's caller could give
// there with method (GivableRoles) and, where groupID names a group, that the group may
// hold there (GivableGroupRoles).
func (m *Manager) givable(ctx context.Context, db resource.Executor, method accesstypes.Resource, scope accesstypes.PolicyScope, groupID *impulse.UUID) ([]access.RoleInfo, error) {
	roles, err := m.access.UserManager().Roles(ctx, scope)
	if err != nil {
		return nil, errors.Wrap(err, "access.UserManager.Roles()")
	}
	judged, err := m.judged(ctx)
	if err != nil {
		return nil, err
	}
	if !judged && groupID == nil {
		return roles, nil
	}
	var offered []access.RoleInfo
	if err := db.ExecuteFunc(ctx, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		mayHold, kept, err := m.groupMayHold(ctx, txn, groupID, scope)
		if err != nil {
			return err
		}
		// The database may run the function again, so each run starts its own list.
		offered, err = rolesOffered(roles, func(role accesstypes.Role) (bool, error) {
			if !mayHold(role) {
				return false, nil
			}
			if !judged {
				return true, nil
			}

			return m.couldGiveIn(ctx, txn, method, scope, role, kept)
		})

		return err
	}); err != nil {
		return nil, errors.Wrap(err, "resource.Executor.ExecuteFunc()")
	}

	return offered, nil
}

// groupMayHold answers the rule a list of the roles to give holds each role to for the
// group named: whether the group, kept where its row says, may hold the role in scope
// (access.GroupMayHold), and where the group is kept (kept), which the tenant rule asks
// as the method asks it (couldGiveIn). With no group named every role passes and kept
// is nil, since a person is given the role; a group that does not exist passes none, so
// the list holds no role and no grant is asked about it.
func (m *Manager) groupMayHold(
	ctx context.Context, txn resource.ReadWriteTransaction, groupID *impulse.UUID, scope accesstypes.PolicyScope,
) (mayHold func(accesstypes.Role) bool, kept *accesstypes.PolicyScope, err error) {
	if groupID == nil {
		return func(accesstypes.Role) bool {
			return true
		}, nil, nil
	}
	group, found, err := m.store.Group(ctx, txn, *groupID)
	if err != nil {
		return nil, nil, errors.Wrap(err, "identity.Store.Group()")
	}
	if !found {
		return func(accesstypes.Role) bool {
			return false
		}, nil, nil
	}
	place, err := group.kept()
	if err != nil {
		return nil, nil, err
	}

	return func(role accesstypes.Role) bool {
		return access.GroupMayHold(place, scope, role)
	}, &place, nil
}

// couldGiveIn reports whether the request's caller could give the role in the scope by
// hand from the whole application's management with the method named (GivableRoles).
// kept is where the group is kept for a group's list (GivableGroupRoles) and nil for a
// person's, so the tenant rule is asked as the method asks it (entersToChange).
func (m *Manager) couldGiveIn(
	ctx context.Context, txn resource.ReadWriteTransaction, method accesstypes.Resource, scope accesstypes.PolicyScope, role accesstypes.Role, kept *accesstypes.PolicyScope,
) (bool, error) {
	if role == accesstypes.SuperAdministrator {
		if !scope.IsGlobal() {
			return false, nil
		}
		err := m.requireSuperAdministrator(ctx, txn, "gives the Super administrator role")
		switch {
		case httpio.HasForbidden(err):
			return false, nil
		case err != nil:
			return false, err
		}
	}
	if enters, err := m.entersToChange(ctx, scope, role, kept); err != nil || !enters {
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
		return "", false, httpio.NewBadRequestMessagef("%s lists the roles one method gives at a time. Filter it by one method's name alone (method:eq:<name>), one of %s.", list, methodNames(methods))
	}
	name, isText := named[0].Value.(string)
	if !isText || !slices.Contains(methods, accesstypes.Resource(name)) {
		return "", false, httpio.NewBadRequestMessagef(
			"%s lists the roles one method gives at a time, and no method that picks a role from it is named %v. Name one of %s (method:eq:<name>).",
			list, named[0].Value, methodNames(methods),
		)
	}

	return accesstypes.Resource(name), true, nil
}

// GivingGroup reads the group a list of the roles to give was asked about, from the
// conditions the request's filter holds on the group (resource.FilterShape.Take).
// method is the one GivingMethod read, "" where the request named none, and
// groupMethod the method that gives a group a role. That method is asked with the
// group beside it, one equality on the group's id, and the list then holds what that
// group may hold (Manager.GivableGroupRoles); asked with no group it holds no rows, for
// which ok is false, since a dialog that asked without its group would otherwise offer
// a role the method refuses that group. Any other method gives a person a role and
// names no group: group is nil and ok is true. A request that names a group beside
// another method or beside none, or names groups any other way, by a set, a range, two
// conditions or a value that is no id, is refused with what to send; list names the
// list in the refusal.
func GivingGroup(list, groupMethod, method accesstypes.Resource, named []resource.Condition) (group *impulse.UUID, ok bool, err error) {
	if method != groupMethod {
		if len(named) != 0 {
			return nil, false, httpio.NewBadRequestMessagef(
				"%s takes a group beside the method that gives a group a role alone. Name the two together (method:eq:%s,group:eq:<id>), or take the filter on group away.",
				list, groupMethod)
		}

		return nil, true, nil
	}
	if len(named) == 0 {
		return nil, false, nil
	}
	if len(named) != 1 || named[0].Operator != filterEquals {
		return nil, false, httpio.NewBadRequestMessagef("%s lists the roles one group may be given at a time. Filter it by one group's id alone (group:eq:<id>).", list)
	}
	text, _ := named[0].Value.(string)
	id, parseErr := impulse.UUIDFromString(text)
	if parseErr != nil {
		return nil, false, httpio.NewBadRequestMessagef("%s lists the roles one group may be given at a time, and %v is no group's id. Filter it by one group's id (group:eq:<id>).", list, named[0].Value)
	}

	return &id, true, nil
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
		return nil, httpio.NewBadRequestMessagef("%s lists the roles of one place at a time. Filter it by one %s's id (domain:eq:<id>), or by none.", list, tenantWord)
	}
	tenant, isText := onTenant[0].Value.(string)
	if !isText {
		return nil, httpio.NewBadRequestMessagef(
			"%s lists the roles of one place at a time, and the filter on domain carries no %s's id. Filter it by one %s's id (domain:eq:<id>), or by none.",
			list, tenantWord, tenantWord,
		)
	}
	if tenant == "" {
		return whole, nil
	}

	return []accesstypes.PolicyScope{accesstypes.DomainPolicyScope(accesstypes.Domain(tenant))}, nil
}
