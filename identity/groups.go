package identity

import (
	"context"
	"slices"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/logger"
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
// (access.UserManager.GroupRole); the rules held here are the user management's: a group
// goes only once it is emptied, a place in a group keeps an account as a role of its own
// does, and one tenant per person counts a place in a tenant's group as a role in that
// tenant.

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
	// DeleteGroup buffers the removal of a group's row.
	DeleteGroup(ctx context.Context, txn resource.ReadWriteTransaction, id impulse.UUID) error
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
		return impulse.NilUUID, httpio.NewConflictMessagef("a group named %q already exists here: choose another name", row.Name)
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

// DeleteGroup buffers the removal of a group, in the caller's transaction, and has the
// permission engine told once the transaction has committed. A group that still has a
// member or holds a role is refused until it is emptied: nobody loses a role because a
// group was deleted, each person is removed and each role taken by an act of its own.
// The members and the roles are read as committed.
func (m *Manager) DeleteGroup(ctx context.Context, txn resource.ReadWriteTransaction, groupID impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	group, err := m.requireGroup(ctx, txn, groupID)
	if err != nil {
		return err
	}
	members, err := m.store.GroupMembers(ctx, txn, groupID)
	if err != nil {
		return errors.Wrap(err, "identity.Store.GroupMembers()")
	}
	roles, err := m.store.GroupRoles(ctx, txn, groupID)
	if err != nil {
		return errors.Wrap(err, "identity.Store.GroupRoles()")
	}
	if len(members) > 0 || len(roles) > 0 {
		return httpio.NewConflictMessagef("the group %s still has %d member(s) and holds %d role(s): remove its members and take its roles away, then delete it", group.Name, len(members), len(roles))
	}
	if err := m.store.DeleteGroup(ctx, txn, groupID); err != nil {
		return errors.Wrap(err, "identity.Store.DeleteGroup()")
	}

	return m.policyChangesAtCommit(txn)
}

// AddGroupMember buffers a person's place in a group, in the caller's transaction, and
// has the permission engine told once the transaction has committed, so the person's
// next request is served with the group's roles. The person is named by the id of their
// account and is not looked up, as AssignRole looks nobody up; a person who is in the
// group already is left as they are. A group holds any account, also a person who holds
// roles in other tenants.
//
// Where a person belongs to one tenant (WithOneTenantPerPerson), a place in a tenant's
// group is a role in that tenant: it is refused for a person who holds a role, or a
// place in a group, in another one (refuseSecondTenant).
func (m *Manager) AddGroupMember(ctx context.Context, txn resource.ReadWriteTransaction, groupID, userID impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if userID.IsNil() {
		return httpio.NewBadRequestMessage("a person is put in a group: the user id is empty")
	}
	group, err := m.requireGroup(ctx, txn, groupID)
	if err != nil {
		return err
	}
	kept, err := group.kept()
	if err != nil {
		return err
	}
	if err := m.refuseSecondTenant(ctx, txn, kept, userID); err != nil {
		return err
	}
	inserted, err := m.store.InsertGroupMember(ctx, txn, &access.GroupMemberKey{Group: GroupID(groupID), User: User(userID)})
	if err != nil {
		return errors.Wrap(err, "identity.Store.InsertGroupMember()")
	}
	if !inserted {
		return nil
	}

	return m.policyChangesAtCommit(txn)
}

// RemoveGroupMember buffers the removal of a person from a group, in the caller's
// transaction, and has the permission engine told once the transaction has committed:
// the person's next request is served without the group's roles. A person who is not in
// the group is no error.
//
// A place in a group keeps an account as a role of its own does, so the rule on an
// account nothing admits applies here as it does to a role taken away (RemoveRole): a
// person left with no role of their own and no place in any group is deleted with their
// identity links and sessions, in the same transaction. What the person holds is read as
// committed.
func (m *Manager) RemoveGroupMember(ctx context.Context, txn resource.ReadWriteTransaction, groupID, userID impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	key := access.GroupMemberKey{Group: GroupID(groupID), User: User(userID)}
	in, err := m.store.HasGroupMember(ctx, txn, &key)
	if err != nil {
		return errors.Wrap(err, "identity.Store.HasGroupMember()")
	}
	if !in {
		return nil
	}
	own, err := m.store.Memberships(ctx, txn, userID)
	if err != nil {
		return errors.Wrap(err, "identity.Store.Memberships()")
	}
	places, err := m.store.UserGroups(ctx, txn, userID)
	if err != nil {
		return errors.Wrap(err, "identity.Store.UserGroups()")
	}
	elsewhere := slices.ContainsFunc(places, func(place GroupPlace) bool { return place.ID != groupID })
	if len(own) == 0 && !elsewhere {
		if err := m.deleteLeftWithNothing(ctx, txn, userID, "the group they were removed from was the last thing they held"); err != nil {
			return err
		}
	}
	if err := m.store.DeleteGroupMember(ctx, txn, &key); err != nil {
		return errors.Wrap(err, "identity.Store.DeleteGroupMember()")
	}

	return m.policyChangesAtCommit(txn)
}

// deleteLeftWithNothing deletes the account of a person who holds no role of their own
// and no place in a group once the caller's removal is applied, with their identity
// links and sessions (deleteAccount). An id no user holds has no account to delete.
func (m *Manager) deleteLeftWithNothing(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID, why string) error {
	username, found, err := m.store.Username(ctx, txn, userID)
	if err != nil {
		return errors.Wrap(err, "identity.Store.Username()")
	}
	if !found {
		return nil
	}
	if err := m.deleteAccount(ctx, txn, userID); err != nil {
		return err
	}
	logger.FromCtx(ctx).Infof("identity: %q (%s) holds no role and no place in a group: %s, so the account is deleted with its identity links and sessions", username, userID, why)

	return nil
}

// GiveGroupRole buffers the role a group holds in the scope, in the caller's
// transaction, and has the permission engine told once the transaction has committed,
// so every person in the group is served with the role at their next request. A role
// the group holds there already is left as it is.
//
// The rule is the permission engine's (access.UserManager.GroupRole): the role exists in
// the scope under exactly this spelling; a group kept in one tenant holds roles in that
// tenant alone; and no group holds the Super Administrator role, which a super
// administrator gives to one person at a time.
func (m *Manager) GiveGroupRole(ctx context.Context, txn resource.ReadWriteTransaction, groupID impulse.UUID, scope accesstypes.PolicyScope, role accesstypes.Role) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

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
	inserted, err := m.store.InsertGroupRole(ctx, txn, &key)
	if err != nil {
		return errors.Wrap(err, "identity.Store.InsertGroupRole()")
	}
	if !inserted {
		return nil
	}

	return m.policyChangesAtCommit(txn)
}

// TakeGroupRole buffers the removal of a role a group holds in the scope, in the
// caller's transaction, and has the permission engine told once the transaction has
// committed: every person in the group is served without the role at their next
// request, unless they hold it some other way. A role the group does not hold there is
// no error, and the role need not exist any more.
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
	if err := m.store.DeleteGroupRole(ctx, txn, &key); err != nil {
		return errors.Wrap(err, "identity.Store.DeleteGroupRole()")
	}

	return m.policyChangesAtCommit(txn)
}

// requireGroup reads a group in the caller's transaction, or answers NotFound.
func (m *Manager) requireGroup(ctx context.Context, txn resource.ReadWriteTransaction, groupID impulse.UUID) (*Group, error) {
	group, found, err := m.store.Group(ctx, txn, groupID)
	if err != nil {
		return nil, errors.Wrap(err, "identity.Store.Group()")
	}
	if !found {
		return nil, httpio.NewNotFoundMessagef("group id %q does not exist", groupID)
	}

	return group, nil
}

// holds answers everything that keeps a person's account and places them in a tenant or
// outside the tenants: the memberships of their own, and a key for each group they are
// in, spelled as a membership held where the group is kept with no role named. The rules
// that read where a person belongs (one tenant per person, the account nothing admits)
// read it, so a place in a tenant's group counts as a role in that tenant and a place in
// a group kept for the whole application as a role held outside the tenants. A key with
// no role is never the membership a caller removes.
func (m *Manager) holds(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) ([]access.MembershipKey, error) {
	held, err := m.store.Memberships(ctx, txn, userID)
	if err != nil {
		return nil, errors.Wrap(err, "identity.Store.Memberships()")
	}
	places, err := m.store.UserGroups(ctx, txn, userID)
	if err != nil {
		return nil, errors.Wrap(err, "identity.Store.UserGroups()")
	}
	for _, place := range places {
		held = append(held, access.MembershipKey{Kind: place.Kind, Axis: place.Axis, Domain: place.Domain, Role: "", User: User(userID)})
	}

	return held, nil
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

// tenantGroups is what a tenant holds of groups, read before a tenant is cleared: the
// groups kept in it with their members and roles, and the roles any group holds in it.
type tenantGroups struct {
	groups  []impulse.UUID
	members []access.GroupMemberKey
	roles   []access.GroupRoleKey
}

// readTenantGroups reads what a tenant holds of groups: its own groups with their
// members and the roles they hold, and the roles groups kept for the whole application
// hold in the tenant.
func (m *Manager) readTenantGroups(ctx context.Context, txn resource.ReadWriteTransaction, domain accesstypes.Domain) (*tenantGroups, error) {
	groups, err := m.store.TenantGroups(ctx, txn, domain)
	if err != nil {
		return nil, errors.Wrap(err, "identity.Store.TenantGroups()")
	}
	held := &tenantGroups{groups: groups, members: nil, roles: nil}
	for _, id := range groups {
		members, err := m.store.GroupMembers(ctx, txn, id)
		if err != nil {
			return nil, errors.Wrap(err, "identity.Store.GroupMembers()")
		}
		for _, user := range members {
			held.members = append(held.members, access.GroupMemberKey{Group: GroupID(id), User: user})
		}
		roles, err := m.store.GroupRoles(ctx, txn, id)
		if err != nil {
			return nil, errors.Wrap(err, "identity.Store.GroupRoles()")
		}
		held.roles = append(held.roles, roles...)
	}
	inTenant, err := m.store.TenantGroupRoles(ctx, txn, domain)
	if err != nil {
		return nil, errors.Wrap(err, "identity.Store.TenantGroupRoles()")
	}
	for _, key := range inTenant {
		if !slices.Contains(held.roles, key) {
			held.roles = append(held.roles, key)
		}
	}

	return held, nil
}

// clear buffers the removal of what the tenant holds of groups: every role a group holds
// in the tenant, every member of the tenant's own groups, then the groups.
func (g *tenantGroups) clear(ctx context.Context, txn resource.ReadWriteTransaction, store Store) error {
	for _, key := range g.roles {
		if err := store.DeleteGroupRole(ctx, txn, &key); err != nil {
			return errors.Wrap(err, "identity.Store.DeleteGroupRole()")
		}
	}
	for _, key := range g.members {
		if err := store.DeleteGroupMember(ctx, txn, &key); err != nil {
			return errors.Wrap(err, "identity.Store.DeleteGroupMember()")
		}
	}
	for _, id := range g.groups {
		if err := store.DeleteGroup(ctx, txn, id); err != nil {
			return errors.Wrap(err, "identity.Store.DeleteGroup()")
		}
	}

	return nil
}

// empty reports whether the tenant holds nothing of groups.
func (g *tenantGroups) empty() bool {
	return len(g.groups) == 0 && len(g.roles) == 0
}

// inGroups reports whether the group is one of the tenant's own.
func (g *tenantGroups) inGroups(id impulse.UUID) bool {
	return slices.Contains(g.groups, id)
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
		return httpio.NewNotFoundMessagef("group id %q does not exist", groupID)
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

// DeleteGroup buffers the removal of a group of the tenant, once it is emptied (see
// Manager.DeleteGroup).
func (t *Tenant) DeleteGroup(ctx context.Context, txn resource.ReadWriteTransaction, groupID impulse.UUID) error {
	if err := t.requireTenantGroup(ctx, txn, groupID); err != nil {
		return err
	}

	return t.manager.DeleteGroup(ctx, txn, groupID)
}

// AddGroupMember buffers a person's place in a group of the tenant (see
// Manager.AddGroupMember). The person is an account of the auth, read in the
// transaction: a tenant's group holds any account.
func (t *Tenant) AddGroupMember(ctx context.Context, txn resource.ReadWriteTransaction, groupID, userID impulse.UUID) error {
	if err := t.requireTenantGroup(ctx, txn, groupID); err != nil {
		return err
	}
	if err := t.manager.RequireUser(ctx, txn, userID); err != nil {
		return err
	}

	return t.manager.AddGroupMember(ctx, txn, groupID, userID)
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
