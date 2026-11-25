package identity

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/tracer"
)

// Groups of people. A group is a record of the auth: it holds roles the way a person
// does, and a person put in a group holds the group's roles wherever the group holds
// them, until the person leaves the group or the group loses the role. A group is kept
// for the whole application or in one tenant. A tenant's group is managed in the tenant
// and holds roles there alone; a group kept for the whole application holds roles
// wherever a person can. No group holds the Super Administrator role, so a group is no
// way around the rule that only a super administrator makes another.
//
// The rules of what a group may hold are the permission engine's
// (access.UserManager.GroupRole); the rules held here are the user management's: a
// group's delete takes its members and the roles it holds with it, after a question that
// counts them, a place in a group keeps an account as a role of its own does, and one
// tenant per person counts a place in a tenant's group as a role in that tenant.
//
// A group gives nobody a power that giving the role directly would not. A change to who
// is in a group, or to what a group holds, is judged as the role changes it makes
// (grants.go); the rules of a role's removal are asked of a person's removal from a
// group and of a role taken from one (the tenant's last administrator, one tenant per
// person); nobody lengthens their own holding through a group; and a tenant puts into
// its groups the people it already has.

// GroupStore is what the group rules need of one auth's tables: single reads and single
// buffered writes over the groups, their members and the roles they hold, which the
// generated package of the auth implements over its own typed queries and patches. An
// auth whose schema holds no group tables, or whose roles a directory writes, answers
// that it keeps none.
type GroupStore interface {
	// InsertGroup buffers a new group and answers the id the row is created under.
	InsertGroup(ctx context.Context, txn resource.ReadWriteTransaction, group *access.GroupRow) (impulse.UUID, error)
	// Group reads a group; found is false where no group holds the id.
	Group(ctx context.Context, txn resource.ReadWriteTransaction, id impulse.UUID) (group *Group, found bool, err error)
	// GroupNamed reports whether a group is kept in the place under the name.
	GroupNamed(ctx context.Context, txn resource.ReadWriteTransaction, place *access.GroupRow) (bool, error)
	// DescribeGroup buffers a new description on a group.
	DescribeGroup(ctx context.Context, txn resource.ReadWriteTransaction, id impulse.UUID, description string) error
	// DeleteGroup buffers the delete of a group, which takes its members and the roles
	// it holds with it (the group's @deleteCascade). Told everythingUnder, it takes them;
	// not told, a group with either deletes nothing and answers the question a person is
	// asked first (428). It answers what one save left (nil where the group went), and
	// is not armed against a caller: the rows are taken trusted.
	DeleteGroup(ctx context.Context, txn resource.ReadWriteTransaction, id impulse.UUID, everythingUnder bool) (*resource.DeleteLeft, error)
	// TenantGroups lists the groups kept in one tenant.
	TenantGroups(ctx context.Context, txn resource.ReadWriteTransaction, domain accesstypes.Domain) ([]impulse.UUID, error)

	// GroupMembers lists the people in a group, each by the id of their account.
	GroupMembers(ctx context.Context, txn resource.ReadWriteTransaction, id impulse.UUID) ([]accesstypes.User, error)
	// HasGroupMember reports whether the member row is committed.
	HasGroupMember(ctx context.Context, txn resource.ReadWriteTransaction, key *access.GroupMemberKey) (bool, error)
	// InsertGroupMember buffers a member row unless the person is in the group already,
	// committed or written earlier in this transaction, and reports whether it did.
	InsertGroupMember(ctx context.Context, txn resource.ReadWriteTransaction, key *access.GroupMemberKey) (inserted bool, err error)
	// DeleteGroupMember buffers the removal of a member row.
	DeleteGroupMember(ctx context.Context, txn resource.ReadWriteTransaction, key *access.GroupMemberKey) error
	// UserGroups lists the groups a person is in, each with where it is kept.
	UserGroups(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) ([]GroupPlace, error)

	// GroupRoles lists the roles a group holds, wherever each is held.
	GroupRoles(ctx context.Context, txn resource.ReadWriteTransaction, id impulse.UUID) ([]access.GroupRoleKey, error)
	// HasGroupRole reports whether the row of a group's role is committed.
	HasGroupRole(ctx context.Context, txn resource.ReadWriteTransaction, key *access.GroupRoleKey) (bool, error)
	// InsertGroupRole buffers the row of a group's role unless the group holds the role
	// there already, and reports whether it did.
	InsertGroupRole(ctx context.Context, txn resource.ReadWriteTransaction, key *access.GroupRoleKey) (inserted bool, err error)
	// DeleteGroupRole buffers the removal of the row of a group's role.
	DeleteGroupRole(ctx context.Context, txn resource.ReadWriteTransaction, key *access.GroupRoleKey) error
	// TenantGroupRoles lists every role a group holds in one tenant, whichever group
	// holds it: the rows of the group roles table under the tenant's own scope.
	TenantGroupRoles(ctx context.Context, txn resource.ReadWriteTransaction, domain accesstypes.Domain) ([]access.GroupRoleKey, error)
	// GroupsHolding lists the groups whose row says they hold the role where the key
	// says it is held, whatever the row's period: the rows of the group roles table
	// under that scope and role.
	GroupsHolding(ctx context.Context, txn resource.ReadWriteTransaction, key *access.RoleKey) ([]impulse.UUID, error)

	// TenantPerson reports whether the account is one of a tenant's people: a row of the
	// tenant's own list of the auth's people, the list a tenant's method names a person
	// from, read in the transaction. An auth of an application with no tenant record
	// has no such list and finds nobody.
	TenantPerson(ctx context.Context, txn resource.ReadWriteTransaction, domain accesstypes.Domain, userID impulse.UUID) (bool, error)
}

// Group is a group of people as the rules read it: where it is kept, in the stores'
// encoding of a scope, and its name.
type Group struct {
	Kind   string
	Axis   string
	Domain string
	Name   string
}

// GroupPlace is one group a person is in, with where the group is kept.
type GroupPlace struct {
	ID     impulse.UUID
	Kind   string
	Axis   string
	Domain string
}

// GroupID answers a group as the permission store names it: the id of its row, as text.
func GroupID(id impulse.UUID) accesstypes.Group {
	return accesstypes.Group(id.String())
}

// kept answers where the group is kept.
func (g *Group) kept() (accesstypes.PolicyScope, error) {
	scope, err := access.GroupScope(g.Kind, g.Axis, g.Domain)
	if err != nil {
		return accesstypes.PolicyScope{}, errors.Wrap(err, "access.GroupScope()")
	}

	return scope, nil
}

// CreateGroup buffers a new group of people kept in the scope, in the caller's
// transaction, and answers its id. The group is held to the permission store's rules
// (access.NewGroup): it is kept for the whole application or in one tenant, and carries
// a name and a description. A name another group kept in the same place holds is
// refused, as committed; the table's unique index stands behind the read. A new group
// has no member and holds no role, so nobody's permissions change.
func (m *Manager) CreateGroup(ctx context.Context, txn resource.ReadWriteTransaction, scope accesstypes.PolicyScope, name, description string) (impulse.UUID, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	row, err := access.NewGroup(scope, name, description)
	if err != nil {
		return impulse.NilUUID, errors.Wrap(err, "access.NewGroup()")
	}
	taken, err := m.store.GroupNamed(ctx, txn, &row)
	if err != nil {
		return impulse.NilUUID, errors.Wrap(err, "identity.Store.GroupNamed()")
	}
	if taken {
		return impulse.NilUUID, httpio.NewConflictMessagef("A group named %q already exists here. Choose another name.", row.Name)
	}
	id, err := m.store.InsertGroup(ctx, txn, &row)
	if err != nil {
		return impulse.NilUUID, errors.Wrap(err, "identity.Store.InsertGroup()")
	}

	return id, nil
}

// DescribeGroup buffers a new description on a group, in the caller's transaction. An
// empty description is refused: a group says who is in it.
func (m *Manager) DescribeGroup(ctx context.Context, txn resource.ReadWriteTransaction, groupID impulse.UUID, description string) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	group, err := m.requireGroup(ctx, txn, groupID)
	if err != nil {
		return err
	}
	row, err := access.NewGroup(accesstypes.GlobalPolicyScope(), group.Name, description)
	if err != nil {
		return errors.Wrap(err, "access.NewGroup()")
	}
	if err := m.store.DescribeGroup(ctx, txn, groupID, row.Description); err != nil {
		return errors.Wrap(err, "identity.Store.DescribeGroup()")
	}

	return nil
}

// DeleteGroup deletes a group with its members and the roles it holds, in the caller's
// transaction, and has the permission engine told once the transaction has committed.
// Asked without everythingUnder of a group that has either, it deletes nothing and
// answers the question a person is asked first, with their counts; asked with it, it
// takes them. Each member and each role is asked what its own removal asks
// (RemoveGroupMember, TakeGroupRole), so a group whose going would leave a tenant
// without an administrator is refused. A member or a role whose period has ended gives
// nobody anything and is asked nothing: the delete takes it with the rest, and counts it
// in the question. It answers what one save left where the group had more under it than
// one save holds (nil once the group is gone).
func (m *Manager) DeleteGroup(ctx context.Context, txn resource.ReadWriteTransaction, groupID impulse.UUID, everythingUnder bool) (*resource.DeleteLeft, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if _, err := m.requireGroup(ctx, txn, groupID); err != nil {
		return nil, err
	}
	periods, err := m.store.GroupPeriods(ctx, txn, groupID)
	if err != nil {
		return nil, errors.Wrap(err, "identity.Store.GroupPeriods()")
	}
	now := time.Now()
	roles, err := m.store.GroupRoles(ctx, txn, groupID)
	if err != nil {
		return nil, errors.Wrap(err, "identity.Store.GroupRoles()")
	}
	for i := range roles {
		if periods.Roles[roles[i]].Ended(now) {
			continue
		}
		scope, err := roles[i].Scope()
		if err != nil {
			return nil, errors.Wrap(err, "access.GroupRoleKey.Scope()")
		}
		if err := m.refuseTakingGroupRole(ctx, txn, groupID, scope, &roles[i]); err != nil {
			return nil, err
		}
	}
	members, err := m.store.GroupMembers(ctx, txn, groupID)
	if err != nil {
		return nil, errors.Wrap(err, "identity.Store.GroupMembers()")
	}
	for _, member := range members {
		if periods.Members[member].Ended(now) {
			continue
		}
		userID, ok := UserID(member)
		if !ok {
			return nil, errors.Newf("the group %s has a member %q that is no account's id", groupID, member)
		}
		if err := m.refuseImpersonatorAsSubject(ctx, userID, "take yourself out of a group"); err != nil {
			return nil, err
		}
		if err := m.refuseTakingPlace(ctx, txn, groupID, userID); err != nil {
			return nil, err
		}
	}
	left, err := m.store.DeleteGroup(ctx, txn, groupID, everythingUnder)
	if err != nil {
		return nil, errors.Wrap(err, "identity.Store.DeleteGroup()")
	}

	return left, m.policyChangesAtCommit(txn)
}

// AddGroupMemberFor buffers a person's place in a group, in the caller's transaction, and
// has the permission engine told once the transaction has committed, so the person's
// next request is served with the group's roles. The person is named by the id of their
// account and is not looked up, as AssignRole looks nobody up. A group holds any account,
// also a person who holds roles in other tenants.
//
// The place is given for the period: the person holds the group's roles while their
// place and the group's role both count, and a person who is in the group already is
// given the period handed in place of the one their place had. AddGroupMember gives the
// place with no period.
//
// Where a person belongs to one tenant (WithOneTenantPerPerson), a place in a tenant's
// group is a role in that tenant: it is refused for a person who holds a role, or a
// place in a group, in another one (refuseSecondTenant).
//
// A request that puts a person in a group gives them each role the group holds, and
// needs what giving them each role directly needs (refuseGivingThrough). And nobody
// lengthens their own holding: the caller does not give their own place a period that
// lasts longer than the one it has (refuseLongerForOneself). A period that cuts the
// caller's own place short is held to what leaving the group is held to: the last
// administrator of a tenant's own keeps the place that makes them so
// (refuseLeavingAsLastAdministrator).
func (m *Manager) AddGroupMemberFor(ctx context.Context, txn resource.ReadWriteTransaction, groupID, userID impulse.UUID, period accesstypes.Period) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := m.refuseImpersonatorAsSubject(ctx, userID, "put yourself in a group"); err != nil {
		return err
	}
	if userID.IsNil() {
		return httpio.NewBadRequestMessage("No person is named. Name the person to put in the group.")
	}
	if err := period.Validate(); err != nil {
		return httpio.NewBadRequestMessageWithError(err, access.PeriodNeverCountsMessage)
	}
	if err := refuseEnded(period, "the place in the group"); err != nil {
		return err
	}
	group, err := m.requireGroup(ctx, txn, groupID)
	if err != nil {
		return err
	}
	kept, err := group.kept()
	if err != nil {
		return err
	}
	if judged, err := m.judged(ctx); err != nil {
		return err
	} else if judged {
		holding, err := m.groupHolding(ctx, txn, groupID, time.Now())
		if err != nil {
			return err
		}
		act := fmt.Sprintf("putting a person in the group %s gives them", group.Name)
		if err := m.refuseGivingThrough(ctx, txn, kept, holding.roles, []accesstypes.User{User(userID)}, act); err != nil {
			return err
		}
	}
	if err := m.refuseSecondTenant(ctx, txn, kept, userID); err != nil {
		return err
	}
	key := access.GroupMemberKey{Group: GroupID(groupID), User: User(userID)}
	inserted, err := m.store.InsertGroupMember(ctx, txn, &key)
	if err != nil {
		return errors.Wrap(err, "identity.Store.InsertGroupMember()")
	}
	changed, err := m.settlePlacePeriod(ctx, txn, groupID, userID, &key, period, inserted, m.callerIs(ctx, userID))
	if err != nil {
		return err
	}
	if !inserted && !changed {
		return nil
	}

	return m.policyChangesAtCommit(txn)
}

// RemoveGroupMember buffers the removal of a person from a group, in the caller's
// transaction, and has the permission engine told once the transaction has committed:
// the person's next request is served without the group's roles. A person who is not in
// the group is no error.
//
// Where the place is the last thing the person holds, the account stays, holding
// nothing.
//
// Taking a person out of a group takes each of the group's roles from them, so the
// removal is held to what a role's removal is: a request needs what taking each role
// from the person directly needs (refuseTakingThrough); a person who takes themselves
// out keeps a place that makes them the last administrator of a tenant
// (refuseLastAdministrator); and where a person belongs to one tenant, the last thing
// they hold outside the tenants stays while they hold roles in more than one
// (refuseSeveralTenantsLeft). A place whose period has ended gives the person nothing,
// and goes whoever asks.
//
// GroupMemberRemovalWarning answers what a call would do, from the same judgment, and
// writes nothing.
func (m *Manager) RemoveGroupMember(ctx context.Context, txn resource.ReadWriteTransaction, groupID, userID impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := m.refuseImpersonatorAsSubject(ctx, userID, "take yourself out of a group"); err != nil {
		return err
	}
	key := access.GroupMemberKey{Group: GroupID(groupID), User: User(userID)}
	in, err := m.store.HasGroupMember(ctx, txn, &key)
	if err != nil {
		return errors.Wrap(err, "identity.Store.HasGroupMember()")
	}
	if !in {
		return nil
	}
	if err := m.refuseTakingPlace(ctx, txn, groupID, userID); err != nil {
		return err
	}
	if err := m.store.DeleteGroupMember(ctx, txn, &key); err != nil {
		return errors.Wrap(err, "identity.Store.DeleteGroupMember()")
	}

	return m.policyChangesAtCommit(txn)
}

// judgeTakingPlace holds a person's removal from the group to what the caller may take
// away, for a call that is judged (judged): each role the group holds is one the caller
// could take from the person directly, where the group is kept (refuseTakingThrough).
func (m *Manager) judgeTakingPlace(ctx context.Context, txn resource.ReadWriteTransaction, group *Group, roles []access.GroupRoleKey, user accesstypes.User) error {
	judged, err := m.judged(ctx)
	if err != nil {
		return err
	}
	if !judged {
		return nil
	}
	kept, err := group.kept()
	if err != nil {
		return err
	}
	act := fmt.Sprintf("taking a person out of the group %s takes away", group.Name)

	return m.refuseTakingThrough(ctx, txn, kept, roles, []accesstypes.User{user}, act)
}

// refuseLeavingAsLastAdministrator holds the caller's loss of their own place in a group
// to the rule on a tenant's last administrator, for each role the group gives them
// (refuseLastAdministrator): the place goes with their removal from the group, and from
// an instant on with a period of their giving that cuts it short.
func (m *Manager) refuseLeavingAsLastAdministrator(
	ctx context.Context, txn resource.ReadWriteTransaction, groupID impulse.UUID, group *Group, roles []access.GroupRoleKey, user accesstypes.User,
) error {
	kept, err := group.kept()
	if err != nil {
		return err
	}
	for i := range roles {
		gone := &removal{
			user: user, role: roles[i].Role, group: GroupID(groupID), kept: &kept, wholeGroup: false,
			unnamed: "You cannot take yourself out of a group that gives you a role in " + withArticle(m.tenantWord) + ".",
			before:  "leave the group that gives you yours",
		}
		scope, err := roles[i].Scope()
		if err != nil {
			return errors.Wrap(err, "access.GroupRoleKey.Scope()")
		}
		if err := m.refuseLastAdministrator(ctx, txn, scope, gone); err != nil {
			return err
		}
	}

	return nil
}

// refuseShorterPlace holds a period the caller gives their own place in a group, one
// that cuts the place short, to what leaving the group is held to
// (refuseLeavingAsLastAdministrator): the roles read are the ones the group holds that
// have not ended.
func (m *Manager) refuseShorterPlace(ctx context.Context, txn resource.ReadWriteTransaction, groupID impulse.UUID, user accesstypes.User) error {
	group, err := m.requireGroup(ctx, txn, groupID)
	if err != nil {
		return err
	}
	holding, err := m.groupHolding(ctx, txn, groupID, time.Now())
	if err != nil {
		return err
	}

	return m.refuseLeavingAsLastAdministrator(ctx, txn, groupID, group, holding.roles, user)
}

// groupRoleGone is a role as the caller, who is in the group, loses it with the group,
// as the rule on a tenant's last administrator reads it: the role taken from the group,
// and a period of the caller's giving that cuts the group's role short. kept is where
// the group is kept.
func (m *Manager) groupRoleGone(caller accesstypes.User, groupID impulse.UUID, kept *accesstypes.PolicyScope, role accesstypes.Role) *removal {
	return &removal{
		user: caller, role: role, group: GroupID(groupID), kept: kept, wholeGroup: true,
		unnamed: "You cannot take a role away from a group you are in, where the group holds it in " + withArticle(m.tenantWord) + ".",
		before:  "take it from the group that gives you yours",
	}
}

// refuseTakingPlace applies the rules of a role's removal to a person's removal from a
// group, which takes each role the group holds from them (RemoveGroupMember).
func (m *Manager) refuseTakingPlace(ctx context.Context, txn resource.ReadWriteTransaction, groupID, userID impulse.UUID) error {
	group, err := m.requireGroup(ctx, txn, groupID)
	if err != nil {
		return err
	}
	now := time.Now()
	holding, err := m.groupHolding(ctx, txn, groupID, now)
	if err != nil {
		return err
	}
	user := User(userID)
	if !holding.periods.Members[user].Ended(now) {
		if err := m.judgeTakingPlace(ctx, txn, group, holding.roles, user); err != nil {
			return err
		}
		if m.callerIs(ctx, userID) {
			if err := m.refuseLeavingAsLastAdministrator(ctx, txn, groupID, group, holding.roles, user); err != nil {
				return err
			}
		}
	}
	// One tenant per person reads where the person belongs from what has not ended.
	held, places, err := m.standing(ctx, txn, userID)
	if err != nil {
		return err
	}
	removed := access.MembershipKey{Kind: group.Kind, Axis: group.Axis, Domain: group.Domain, Role: "", User: user}
	for _, place := range places {
		if place.ID != groupID {
			held = append(held, access.MembershipKey{Kind: place.Kind, Axis: place.Axis, Domain: place.Domain, Role: "", User: user})
		}
	}

	return m.refuseSeveralTenantsLeft(&removed, held)
}

// GiveGroupRoleFor buffers the role a group holds in the scope, in the caller's
// transaction, and has the permission engine told once the transaction has committed,
// so every person in the group is served with the role at their next request. The role
// is given for the period, and a role the group holds there already is given the period
// handed in place of the one it had. GiveGroupRole gives the role with no period.
//
// The rule is the permission engine's (access.UserManager.GroupRole): the role exists in
// the scope under exactly this spelling; a group kept in one tenant holds roles in that
// tenant alone; and no group holds the Super Administrator role, which a super
// administrator gives to one person at a time.
//
// A request that gives a group a role gives it to everyone in the group, and needs what
// giving it to each of them directly needs (refuseGivingThrough). And nobody lengthens
// their own holding: a caller who is in the group does not give a role the group holds a
// period that lasts longer than the one it has (refuseLongerForOneself). A period that
// cuts the role short is held to what taking it from the group is held to: a caller in
// the group whom the role makes the last administrator of a tenant's own does not give
// it one (refuseLastAdministrator).
func (m *Manager) GiveGroupRoleFor(
	ctx context.Context, txn resource.ReadWriteTransaction, groupID impulse.UUID, scope accesstypes.PolicyScope, role accesstypes.Role, period accesstypes.Period,
) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := period.Validate(); err != nil {
		return httpio.NewBadRequestMessageWithError(err, access.PeriodNeverCountsMessage)
	}
	if err := refuseEnded(period, "the group's role"); err != nil {
		return err
	}
	group, err := m.requireGroup(ctx, txn, groupID)
	if err != nil {
		return err
	}
	kept, err := group.kept()
	if err != nil {
		return err
	}
	key, err := m.access.UserManager().GroupRole(ctx, GroupID(groupID), kept, scope, role, func(ctx context.Context, key access.RoleKey) (bool, error) {
		held, err := m.store.HasRole(ctx, txn, &key)
		if err != nil {
			return false, errors.Wrap(err, "identity.Store.HasRole()")
		}

		return held, nil
	})
	if err != nil {
		return errors.Wrap(err, "access.UserManager.GroupRole()")
	}
	holding, err := m.groupHolding(ctx, txn, groupID, time.Now())
	if err != nil {
		return err
	}
	if judged, err := m.judged(ctx); err != nil {
		return err
	} else if judged {
		act := fmt.Sprintf("giving the group %s a role gives everyone in it", group.Name)
		if err := m.refuseGivingThrough(ctx, txn, kept, []access.GroupRoleKey{key}, holding.members, act); err != nil {
			return err
		}
	}
	// The role is one of the caller's own holdings where the caller is in the group.
	var gone *removal
	if caller, known := m.callerUser(ctx); known && slices.Contains(holding.members, caller) {
		gone = m.groupRoleGone(caller, groupID, &kept, role)
	}
	inserted, err := m.store.InsertGroupRole(ctx, txn, &key)
	if err != nil {
		return errors.Wrap(err, "identity.Store.InsertGroupRole()")
	}
	changed, err := m.settleGroupRolePeriod(ctx, txn, groupID, &key, period, inserted, gone)
	if err != nil {
		return err
	}
	if !inserted && !changed {
		return nil
	}

	return m.policyChangesAtCommit(txn)
}

// TakeGroupRole buffers the removal of a role a group holds in the scope, in the
// caller's transaction, and has the permission engine told once the transaction has
// committed: every person in the group is served without the role at their next
// request, unless they hold it some other way. A role the group does not hold there is
// no error, and the role need not exist any more.
//
// Taking a role from a group takes it from everyone in the group, so the removal is held
// to what a role's removal is: a request needs what taking the role from each of them
// directly needs (refuseTakingThrough), and a caller who is in the group does not take
// away the role that makes them the last administrator of a tenant
// (refuseLastAdministrator). A role whose period has ended gives nobody anything, and
// goes whoever asks.
func (m *Manager) TakeGroupRole(ctx context.Context, txn resource.ReadWriteTransaction, groupID impulse.UUID, scope accesstypes.PolicyScope, role accesstypes.Role) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	key := access.NewGroupRoleKey(GroupID(groupID), scope, role)
	held, err := m.store.HasGroupRole(ctx, txn, &key)
	if err != nil {
		return errors.Wrap(err, "identity.Store.HasGroupRole()")
	}
	if !held {
		return nil
	}
	if err := m.refuseTakingGroupRole(ctx, txn, groupID, scope, &key); err != nil {
		return err
	}
	if err := m.store.DeleteGroupRole(ctx, txn, &key); err != nil {
		return errors.Wrap(err, "identity.Store.DeleteGroupRole()")
	}

	return m.policyChangesAtCommit(txn)
}

// refuseTakingGroupRole applies the rules of a role's removal to a role taken from a
// group, which takes it from everyone in the group (TakeGroupRole).
func (m *Manager) refuseTakingGroupRole(ctx context.Context, txn resource.ReadWriteTransaction, groupID impulse.UUID, scope accesstypes.PolicyScope, key *access.GroupRoleKey) error {
	group, err := m.requireGroup(ctx, txn, groupID)
	if err != nil {
		return err
	}
	now := time.Now()
	holding, err := m.groupHolding(ctx, txn, groupID, now)
	if err != nil {
		return err
	}
	if holding.periods.Roles[*key].Ended(now) {
		return nil
	}
	kept, err := group.kept()
	if err != nil {
		return err
	}
	if judged, err := m.judged(ctx); err != nil {
		return err
	} else if judged {
		act := fmt.Sprintf("taking a role from the group %s takes away from everyone in it", group.Name)
		if err := m.refuseTakingThrough(ctx, txn, kept, []access.GroupRoleKey{*key}, holding.members, act); err != nil {
			return err
		}
	}
	caller, known := m.callerUser(ctx)
	if !known || !slices.Contains(holding.members, caller) {
		return nil
	}

	return m.refuseLastAdministrator(ctx, txn, scope, m.groupRoleGone(caller, groupID, &kept, key.Role))
}

// requireGroup reads a group in the caller's transaction, or answers NotFound.
func (m *Manager) requireGroup(ctx context.Context, txn resource.ReadWriteTransaction, groupID impulse.UUID) (*Group, error) {
	group, found, err := m.store.Group(ctx, txn, groupID)
	if err != nil {
		return nil, errors.Wrap(err, "identity.Store.Group()")
	}
	if !found {
		return nil, groupNotFound(groupID)
	}

	return group, nil
}

// holds answers everything that places a person in a tenant or outside the tenants: the
// memberships of their own, and a key for each group they are in, spelled as a
// membership held where the group is kept with no role named. The rules that read where
// a person belongs (one tenant per person) read it, so a place in a tenant's group
// counts as a role in that tenant and a place in a group kept for the whole application
// as a role held outside the tenants. A key with no role is never the membership a
// caller removes. An assignment whose period has ended places a person nowhere, and is
// left out (standing); one whose period has not begun places them where it will count.
func (m *Manager) holds(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) ([]access.MembershipKey, error) {
	rows, err := m.readHeld(ctx, txn, userID)
	if err != nil {
		return nil, err
	}

	return rows.placingAt(time.Now()), nil
}

// removeGroupPlaces buffers the removal of every place in a group the person has, for an
// account that goes: a place left behind would keep its group from ever being deleted.
// It reports whether the person was in a group.
func (m *Manager) removeGroupPlaces(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) (bool, error) {
	places, err := m.store.UserGroups(ctx, txn, userID)
	if err != nil {
		return false, errors.Wrap(err, "identity.Store.UserGroups()")
	}
	for _, place := range places {
		if err := m.store.DeleteGroupMember(ctx, txn, &access.GroupMemberKey{Group: GroupID(place.ID), User: User(userID)}); err != nil {
			return false, errors.Wrap(err, "identity.Store.DeleteGroupMember()")
		}
	}

	return len(places) > 0, nil
}

// requireTenantGroup reads a group of the tenant, or answers NotFound: a group kept
// anywhere else is not found in the tenant, whoever asks.
func (t *Tenant) requireTenantGroup(ctx context.Context, txn resource.ReadWriteTransaction, groupID impulse.UUID) error {
	group, err := t.manager.requireGroup(ctx, txn, groupID)
	if err != nil {
		return err
	}
	kept, err := group.kept()
	if err != nil {
		return err
	}
	if kept != t.scope() {
		return groupNotFound(groupID)
	}

	return nil
}

// CreateGroup buffers a new group kept in the tenant (see Manager.CreateGroup).
func (t *Tenant) CreateGroup(ctx context.Context, txn resource.ReadWriteTransaction, name, description string) (impulse.UUID, error) {
	return t.manager.CreateGroup(ctx, txn, t.scope(), name, description)
}

// DescribeGroup buffers a new description on a group of the tenant (see
// Manager.DescribeGroup).
func (t *Tenant) DescribeGroup(ctx context.Context, txn resource.ReadWriteTransaction, groupID impulse.UUID, description string) error {
	if err := t.requireTenantGroup(ctx, txn, groupID); err != nil {
		return err
	}

	return t.manager.DescribeGroup(ctx, txn, groupID, description)
}

// DeleteGroup deletes a group of the tenant with its members and the roles it holds
// (see Manager.DeleteGroup).
func (t *Tenant) DeleteGroup(ctx context.Context, txn resource.ReadWriteTransaction, groupID impulse.UUID, everythingUnder bool) (*resource.DeleteLeft, error) {
	if err := t.requireTenantGroup(ctx, txn, groupID); err != nil {
		return nil, err
	}

	return t.manager.DeleteGroup(ctx, txn, groupID, everythingUnder)
}

// requireTenantPerson holds a person a tenant's method names to the tenant's people: the
// rows of the tenant's own list, which hold who has a role there. An account that is not
// one of them is not found in the tenant, whoever asks and whatever it holds elsewhere,
// so a tenant's method never says whether an id belongs to somebody of another tenant.
func (t *Tenant) requireTenantPerson(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) error {
	theirs, err := t.manager.store.TenantPerson(ctx, txn, t.domain, userID)
	if err != nil {
		return errors.Wrap(err, "identity.Store.TenantPerson()")
	}
	if !theirs {
		return accountNotFound(userID)
	}

	return nil
}

// AddGroupMember buffers a person's place in a group of the tenant, with no period (see
// AddGroupMemberFor).
func (t *Tenant) AddGroupMember(ctx context.Context, txn resource.ReadWriteTransaction, groupID, userID impulse.UUID) error {
	var none accesstypes.Period

	return t.AddGroupMemberFor(ctx, txn, groupID, userID, none)
}

// RemoveGroupMember buffers the removal of a person from a group of the tenant (see
// Manager.RemoveGroupMember).
func (t *Tenant) RemoveGroupMember(ctx context.Context, txn resource.ReadWriteTransaction, groupID, userID impulse.UUID) error {
	if err := t.requireTenantGroup(ctx, txn, groupID); err != nil {
		return err
	}

	return t.manager.RemoveGroupMember(ctx, txn, groupID, userID)
}

// GiveGroupRole buffers a role a group of the tenant holds in the tenant, the one place
// a tenant's group holds a role (see Manager.GiveGroupRole).
func (t *Tenant) GiveGroupRole(ctx context.Context, txn resource.ReadWriteTransaction, groupID impulse.UUID, role accesstypes.Role) error {
	if err := t.requireTenantGroup(ctx, txn, groupID); err != nil {
		return err
	}

	return t.manager.GiveGroupRole(ctx, txn, groupID, t.scope(), role)
}

// TakeGroupRole buffers the removal of a role a group of the tenant holds in the tenant
// (see Manager.TakeGroupRole).
func (t *Tenant) TakeGroupRole(ctx context.Context, txn resource.ReadWriteTransaction, groupID impulse.UUID, role accesstypes.Role) error {
	if err := t.requireTenantGroup(ctx, txn, groupID); err != nil {
		return err
	}

	return t.manager.TakeGroupRole(ctx, txn, groupID, t.scope(), role)
}
