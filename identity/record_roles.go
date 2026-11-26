package identity

import (
	"context"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/tracer"
)

// Roles held on one record of a tenant. A tenant's method gives a person of the tenant, or
// a group kept in it, a role on one of the tenant's records (a project), which gives the
// role's grants on that record and on what is under it, and nothing else in the tenant.
// The record is named by its kind, its resource name, and its key as text; that it is a
// row the caller may act on is the method's own check, made before the method runs, so
// nothing here reads the record. What may be held on it is the access library's rule
// (access.UserManager.RecordMembership, RecordGroupRole): a domain role the role file
// marks for the kind, on a kind the permission collection marks.
//
// A role on a record is something held in the record's tenant for every rule that asks
// where a person belongs: the person is one of the tenant's people (requireTenantPerson),
// lists the tenant among their own (PersonTenants), and loses the role when they leave
// the tenant or are removed from it; and where a person belongs to one tenant
// (WithOneTenantPerPerson), a role on a record of another tenant holds them there.

// GiveRoleOn buffers the person's role on one record of the tenant, for the period, in
// the caller's transaction, and has the permission engine told once the transaction has
// committed. record is the record's kind by its resource name, and id its key as text. A
// role the person holds on the record already is given the period handed in place of the
// one it had.
//
// The role is held to the access library's rule first, then to the role's rule on a
// period (access.ValidatePeriod) and to a period that has not ended, and the person to the
// tenant's people (requireTenantPerson): a record is shared with somebody outside the
// tenant once they are invited into it. The person behind a session that acts as someone
// else gives themselves nothing.
func (t *Tenant) GiveRoleOn(
	ctx context.Context, txn resource.ReadWriteTransaction, record accesstypes.Resource, id string, userID impulse.UUID, role accesstypes.Role, period accesstypes.Period,
) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := t.manager.refuseImpersonatorAsSubject(ctx, userID, "give yourself a role"); err != nil {
		return err
	}
	key, err := t.manager.access.UserManager().RecordMembership(ctx, accesstypes.NewRecordScope(t.domain, record, id), User(userID), role)
	if err != nil {
		return errors.Wrap(err, "access.UserManager.RecordMembership()")
	}
	if err := refuseRecordPeriod(role, period); err != nil {
		return err
	}
	if err := t.requireTenantPerson(ctx, txn, userID); err != nil {
		return err
	}
	if err := t.manager.store.SetRecordMembership(ctx, txn, &key, period); err != nil {
		return errors.Wrap(err, "identity.Store.SetRecordMembership()")
	}

	return t.manager.policyChangesAtCommit(txn)
}

// TakeRoleOn buffers the removal of the person's role on one record of the tenant, and
// has the permission engine told once the transaction has committed. A role the person
// does not hold there is no error, and the role need not be one held on the record's kind
// any more. The person behind a session that acts as someone else takes nothing from
// themselves.
func (t *Tenant) TakeRoleOn(ctx context.Context, txn resource.ReadWriteTransaction, record accesstypes.Resource, id string, userID impulse.UUID, role accesstypes.Role) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := t.manager.refuseImpersonatorAsSubject(ctx, userID, "take a role from yourself"); err != nil {
		return err
	}
	key := access.NewRecordMembershipKey(accesstypes.NewRecordScope(t.domain, record, id), User(userID), role)
	if err := t.manager.store.DeleteRecordMembership(ctx, txn, &key); err != nil {
		return errors.Wrap(err, "identity.Store.DeleteRecordMembership()")
	}

	return t.manager.policyChangesAtCommit(txn)
}

// GiveGroupRoleOn buffers a group's role on one record of the tenant, for the period, as
// GiveRoleOn gives a person's: every person in the group holds the role on the record. The
// group is one kept in the tenant (requireTenantGroup), so a group kept anywhere else is
// not found here, and the role is then held to the access library's rule for a group.
func (t *Tenant) GiveGroupRoleOn(
	ctx context.Context, txn resource.ReadWriteTransaction, record accesstypes.Resource, id string, groupID impulse.UUID, role accesstypes.Role, period accesstypes.Period,
) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := t.requireTenantGroup(ctx, txn, groupID); err != nil {
		return err
	}
	key, err := t.manager.access.UserManager().RecordGroupRole(ctx, GroupID(groupID), t.scope(), accesstypes.NewRecordScope(t.domain, record, id), role)
	if err != nil {
		return errors.Wrap(err, "access.UserManager.RecordGroupRole()")
	}
	if err := refuseRecordPeriod(role, period); err != nil {
		return err
	}
	if err := t.manager.store.SetRecordGroupRole(ctx, txn, &key, period); err != nil {
		return errors.Wrap(err, "identity.Store.SetRecordGroupRole()")
	}

	return t.manager.policyChangesAtCommit(txn)
}

// TakeGroupRoleOn buffers the removal of a group's role on one record of the tenant, as
// TakeRoleOn removes a person's: a role the group does not hold there is no error.
func (t *Tenant) TakeGroupRoleOn(ctx context.Context, txn resource.ReadWriteTransaction, record accesstypes.Resource, id string, groupID impulse.UUID, role accesstypes.Role) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	key := access.NewRecordGroupRoleKey(GroupID(groupID), accesstypes.NewRecordScope(t.domain, record, id), role)
	if err := t.manager.store.DeleteRecordGroupRole(ctx, txn, &key); err != nil {
		return errors.Wrap(err, "identity.Store.DeleteRecordGroupRole()")
	}

	return t.manager.policyChangesAtCommit(txn)
}

// refuseRecordPeriod holds the period a role on a record is given for to the role's rule
// on a period (access.ValidatePeriod) and refuses one that has ended already, as a
// membership's is held.
func refuseRecordPeriod(role accesstypes.Role, period accesstypes.Period) error {
	if err := access.ValidatePeriod(role, period); err != nil {
		return errors.Wrap(err, "access.ValidatePeriod()")
	}

	return refuseEnded(period, "the role")
}

// GivableRecordRoles lists the roles a person picks from to give one on a record of the
// kind record, with the method named (method: the one that gives a person a role on such
// a record, or the one that gives a group one, by the name its grant is written under), as
// the method's dialog offers them: the roles the role file marks for the kind
// (access.UserManager.RolesHeldOn) that the request's caller could give in the request's
// tenant with that method (RoleGrants.CouldGive), and no other, as GivableRoles lists a
// tenant's roles. A call outside any request is the application's own and lists every
// one; a manager a request reaches without the role grants cannot judge it, which is an
// error and never a list. The questions are asked in one transaction of the list's own,
// on db, which writes nothing.
func (m *Manager) GivableRecordRoles(ctx context.Context, db resource.Executor, method, record accesstypes.Resource) ([]access.RoleInfo, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	roles := m.access.UserManager().RolesHeldOn(record)
	judged, err := m.judged(ctx)
	if err != nil {
		return nil, err
	}
	if !judged {
		return roles, nil
	}
	caller := resource.CallerFrom(ctx)
	domain, inTenant := caller.Scope.Domain()
	if !inTenant {
		return nil, errors.Newf("the roles to give on one of %s were asked for in a request checked in the scope %s, which names no tenant", record, caller.Scope)
	}
	scope := accesstypes.DomainPolicyScope(domain)
	var offered []access.RoleInfo
	if err := db.ExecuteFunc(ctx, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		// The database may run the function again, so each run starts its own list.
		var err error
		offered, err = rolesOffered(roles, func(role accesstypes.Role) (bool, error) {
			return m.couldGiveIn(ctx, txn, method, scope, role, nil)
		})

		return err
	}); err != nil {
		return nil, errors.Wrap(err, "resource.Executor.ExecuteFunc()")
	}

	return offered, nil
}

// GivingRecord reads the one record a list of the roles to give on a record was asked
// about, from the conditions the request's filter holds on the record
// (resource.FilterShape.Take): one equality on the record's key, as text. The list is under
// its record, each row listed for the record named, so a grant held on one record lists
// the roles to give on that record alone; it holds no rows for a request that names no
// record, for which ok is false. A request that names records any other way, by a set, a
// range or two conditions, or by a value that is no text, is refused with what to send;
// list names the list in the refusal. A filter that joins its conditions by OR holds none
// Take can read, so it names no record and is listed no role.
func GivingRecord(list accesstypes.Resource, named []resource.Condition) (record string, ok bool, err error) {
	if len(named) == 0 {
		return "", false, nil
	}
	if len(named) != 1 || named[0].Operator != filterEquals {
		return "", false, httpio.NewBadRequestMessagef("%s lists the roles to give on one record at a time. Filter it by the record's key alone (recordId:eq:<key>).", list)
	}
	key, isText := named[0].Value.(string)
	if !isText || key == "" {
		return "", false, httpio.NewBadRequestMessagef("%s lists the roles to give on one record at a time, and %v is no record's key. Filter it by the record's key (recordId:eq:<key>).", list, named[0].Value)
	}

	return key, true, nil
}

// recordHeldIn reports whether a person's role on a record is held on a record of the
// tenant: the record's tenant, on the default axis.
func recordHeldIn(key *access.RecordMembershipKey, tenant accesstypes.Domain) bool {
	return key.Axis == "" && key.Domain == string(tenant)
}

// recordPlacing spells a person's role on a record as a membership held in the record's
// tenant with no role named, as holds spells a place in a group: what places the person
// in that tenant.
func recordPlacing(key *access.RecordMembershipKey) access.MembershipKey {
	placed := access.NewMembershipKey(accesstypes.DomainPolicyScope(accesstypes.Domain(key.Domain)), key.User, "")
	placed.Axis = key.Axis

	return placed
}
