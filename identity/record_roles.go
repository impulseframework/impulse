package identity

import (
	"context"
	"slices"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/auditlog"
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

// RecordPeriods are the periods of the roles held on one record, each by its row's key: a
// person's (People) and a group's (Groups). A role given with no period has the zero
// period. A store that keeps no roles on records answers both empty, and one that keeps no
// groups an empty Groups.
type RecordPeriods struct {
	People map[access.RecordMembershipKey]accesstypes.Period
	Groups map[access.RecordGroupRoleKey]accesstypes.Period
}

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
//
// Nobody lengthens their own holding: a caller does not give their own role on the
// record, or a role of a group they are in, a period that lasts longer than the one it
// has (refuseLongerForOneself).
//
// A role given, or given another period, writes its row of the audit log (recordRoleOn);
// the same role given again for the same period writes none.
func (t *Tenant) GiveRoleOn(
	ctx context.Context, txn resource.ReadWriteTransaction, record accesstypes.Resource, id string, userID impulse.UUID, role accesstypes.Role, period accesstypes.Period,
) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := t.manager.refuseImpersonatorAsSubject(ctx, userID, "give yourself a role"); err != nil {
		return err
	}
	at := accesstypes.NewRecordScope(t.domain, record, id)
	key, err := t.manager.access.UserManager().RecordMembership(ctx, at, User(userID), role)
	if err != nil {
		return errors.Wrap(err, "access.UserManager.RecordMembership()")
	}
	if err := refuseRecordPeriod(role, period); err != nil {
		return err
	}
	if err := t.requireTenantPerson(ctx, txn, userID); err != nil {
		return err
	}
	periods, err := t.manager.store.RecordPeriods(ctx, txn, at)
	if err != nil {
		return errors.Wrap(err, "identity.Store.RecordPeriods()")
	}
	had, held := periods.People[key]
	if held && t.manager.callerIs(ctx, userID) {
		if err := refuseLongerForOneself(true, had, period, "your own role"); err != nil {
			return err
		}
	}
	if err := t.manager.store.SetRecordMembership(ctx, txn, &key, period); err != nil {
		return errors.Wrap(err, "identity.Store.SetRecordMembership()")
	}
	if !held || !had.Equal(period) {
		gave, err := t.manager.accountSubject(ctx, txn, userID)
		if err != nil {
			return err
		}
		gave.Act, gave.Period = auditlog.ActRoleGiven, period
		if err := t.recordRoleOn(ctx, txn, gave, at, role); err != nil {
			return err
		}
	}

	return t.manager.policyChangesAtCommit(txn)
}

// TakeRoleOn buffers the removal of the person's role on one record of the tenant, and
// has the permission engine told once the transaction has committed. A role the person
// does not hold there is no error, and the role need not be one held on the record's kind
// any more. The person behind a session that acts as someone else takes nothing from
// themselves. A role the person held there writes its row of the audit log
// (recordRoleOn).
func (t *Tenant) TakeRoleOn(ctx context.Context, txn resource.ReadWriteTransaction, record accesstypes.Resource, id string, userID impulse.UUID, role accesstypes.Role) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := t.manager.refuseImpersonatorAsSubject(ctx, userID, "take a role from yourself"); err != nil {
		return err
	}
	at := accesstypes.NewRecordScope(t.domain, record, id)
	key := access.NewRecordMembershipKey(at, User(userID), role)
	periods, err := t.manager.store.RecordPeriods(ctx, txn, at)
	if err != nil {
		return errors.Wrap(err, "identity.Store.RecordPeriods()")
	}
	if err := t.manager.store.DeleteRecordMembership(ctx, txn, &key); err != nil {
		return errors.Wrap(err, "identity.Store.DeleteRecordMembership()")
	}
	if _, held := periods.People[key]; held {
		took, err := t.manager.accountSubject(ctx, txn, userID)
		if err != nil {
			return err
		}
		took.Act = auditlog.ActRoleTaken
		if err := t.recordRoleOn(ctx, txn, took, at, role); err != nil {
			return err
		}
	}

	return t.manager.policyChangesAtCommit(txn)
}

// recordRoleOn buffers the row of the audit log of a role given or taken on one record of
// the tenant. r holds the act, its subject and the period given; the row names the role,
// held on the record (auditlog.RoleScopeRecord) in the tenant, and the record by its kind,
// its key, and the word and the name the store reads for it (Store.RecordNamed), so that
// the row reads as the record was called when the act was done.
func (t *Tenant) recordRoleOn(ctx context.Context, txn resource.ReadWriteTransaction, r *auditlog.Record, at accesstypes.RecordScope, role accesstypes.Role) error {
	word, name, err := t.manager.store.RecordNamed(ctx, txn, at)
	if err != nil {
		return errors.Wrap(err, "identity.Store.RecordNamed()")
	}
	r.RoleNames, r.RoleScope, r.TenantID = []string{string(role)}, auditlog.RoleScopeRecord, string(t.domain)
	r.RecordKind, r.RecordID, r.RecordWord, r.RecordName = string(at.Resource()), at.Record(), word, name

	return t.manager.record(ctx, txn, r)
}

// GiveGroupRoleOn buffers a group's role on one record of the tenant, for the period, as
// GiveRoleOn gives a person's: every person in the group holds the role on the record. The
// group is one kept in the tenant (requireTenantGroup), so a group kept anywhere else is
// not found here, and the role is then held to the access library's rule for a group.
//
// Nobody lengthens their own holding: a caller does not give their own role on the
// record, or a role of a group they are in, a period that lasts longer than the one it
// has (refuseLongerForOneself).
//
// A role given, or given another period, writes its row of the audit log, as GiveRoleOn's
// does.
func (t *Tenant) GiveGroupRoleOn(
	ctx context.Context, txn resource.ReadWriteTransaction, record accesstypes.Resource, id string, groupID impulse.UUID, role accesstypes.Role, period accesstypes.Period,
) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	group, err := t.requireTenantGroup(ctx, txn, groupID)
	if err != nil {
		return err
	}
	at := accesstypes.NewRecordScope(t.domain, record, id)
	key, err := t.manager.access.UserManager().RecordGroupRole(ctx, GroupID(groupID), t.scope(), at, role)
	if err != nil {
		return errors.Wrap(err, "access.UserManager.RecordGroupRole()")
	}
	if err := refuseRecordPeriod(role, period); err != nil {
		return err
	}
	periods, err := t.manager.store.RecordPeriods(ctx, txn, at)
	if err != nil {
		return errors.Wrap(err, "identity.Store.RecordPeriods()")
	}
	had, held := periods.Groups[key]
	if held {
		if err := t.refuseLongerGroupRole(ctx, txn, groupID, had, period); err != nil {
			return err
		}
	}
	if err := t.manager.store.SetRecordGroupRole(ctx, txn, &key, period); err != nil {
		return errors.Wrap(err, "identity.Store.SetRecordGroupRole()")
	}
	if !held || !had.Equal(period) {
		gave := groupRecord(auditlog.ActGroupRoleGiven, groupID, group.Name, string(t.domain))
		gave.Period = period
		if err := t.recordRoleOn(ctx, txn, gave, at, role); err != nil {
			return err
		}
	}

	return t.manager.policyChangesAtCommit(txn)
}

// refuseLongerGroupRole refuses a caller who is one of the group's people, whose place in
// it has not ended, a period for a role the group holds on the record that lasts longer
// than the one it has (had). A call outside any request has no caller.
func (t *Tenant) refuseLongerGroupRole(ctx context.Context, txn resource.ReadWriteTransaction, groupID impulse.UUID, had, period accesstypes.Period) error {
	caller, known := t.manager.callerUser(ctx)
	if !known {
		return nil
	}
	holding, err := t.manager.groupHolding(ctx, txn, groupID, time.Now())
	if err != nil {
		return err
	}
	if !slices.Contains(holding.members, caller) {
		return nil
	}

	return refuseLongerForOneself(true, had, period, "a role of a group you are in")
}

// TakeGroupRoleOn buffers the removal of a group's role on one record of the tenant, as
// TakeRoleOn removes a person's: a role the group does not hold there is no error, and one
// it held writes its row of the audit log.
func (t *Tenant) TakeGroupRoleOn(ctx context.Context, txn resource.ReadWriteTransaction, record accesstypes.Resource, id string, groupID impulse.UUID, role accesstypes.Role) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	at := accesstypes.NewRecordScope(t.domain, record, id)
	key := access.NewRecordGroupRoleKey(GroupID(groupID), at, role)
	periods, err := t.manager.store.RecordPeriods(ctx, txn, at)
	if err != nil {
		return errors.Wrap(err, "identity.Store.RecordPeriods()")
	}
	if err := t.manager.store.DeleteRecordGroupRole(ctx, txn, &key); err != nil {
		return errors.Wrap(err, "identity.Store.DeleteRecordGroupRole()")
	}
	if _, held := periods.Groups[key]; held {
		group, err := t.manager.requireGroup(ctx, txn, groupID)
		if err != nil {
			return err
		}
		took := groupRecord(auditlog.ActGroupRoleTaken, groupID, group.Name, string(t.domain))
		if err := t.recordRoleOn(ctx, txn, took, at, role); err != nil {
			return err
		}
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
		return "", false, httpio.NewBadRequestMessagef(
			"%s lists the roles to give on one record at a time, and %v is no record's key. Filter it by the record's key (recordId:eq:<key>).",
			list, named[0].Value)
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
