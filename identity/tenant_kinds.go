package identity

import (
	"cmp"
	"context"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/eventsource"
	"github.com/impulseframework/impulse/logger"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/tracer"
)

// TenantDeleteCosts is what the delete of one row of each of an auth's tables costs in a
// commit, in mutations, as resource.DeleteCost counts one (Store.DeleteCosts). A table
// the schema does not hold costs nothing: no row of it is deleted.
type TenantDeleteCosts struct {
	// Membership is a row of the memberships table.
	Membership int
	// Group, GroupMember and GroupRole are a row of each of the group tables.
	Group       int
	GroupMember int
	GroupRole   int
	// RecordMembership and RecordGroupRole are a row of each of the tables of the roles
	// held on one record, a person's and a group's, with the change record its removal
	// writes.
	RecordMembership int
	RecordGroupRole  int
	// Role is a row of the roles table, a custom role. Its grants go by the database's
	// rule with it.
	Role int
	// Identity is an identity link, and User a row of the users table, without what the
	// database removes with it (Store.RowsWithUser).
	Identity int
	User     int
	// Invitation is the withdrawal of one invitation: the columns it writes, and an entry
	// of each index that holds one of them.
	Invitation int
	// SessionEnd is the ending of one session of an account that is deleted, and
	// ImpersonationEnd the ending of one impersonation record it holds: the columns each
	// writes, and an entry of each index that holds one of them.
	SessionEnd       int
	ImpersonationEnd int
}

// TenantKinds answers what the auth holds for a tenant as kinds of the tenant's delete
// (resource.TakenKind): the accounts that are the tenant's alone, the roles held in it,
// the roles held on its records, its groups, its custom roles and its invitations that
// still stand. The generated delete of the tenant record hands them to the delete, which
// counts them in its question, asks them before anything goes, and takes them in its
// rounds after the application's own rows. An account is the tenant's alone where
// everything it holds is in the tenant, a role on one of its records among it, or the
// tenant's directory manages it and it holds nothing elsewhere; a person who holds
// anything elsewhere, a role on another tenant's record among it, or whom another
// tenant's directory manages, stays.
//
// Each kind reads its rows as the transaction reads them, every time it is asked, so a
// row an earlier round took is not counted again. Each takes its rows in key order while
// the next one's cost fits what the round has left, and says rows are left when one did
// not fit. A kind that changed who holds what has the permission engine told once the
// transaction commits.
func (m *Manager) TenantKinds(domain accesstypes.Domain) []resource.TakenKind {
	held := &tenantHeld{manager: m, domain: domain}

	return []resource.TakenKind{
		&tenantAccounts{held},
		&tenantRolesHeld{held},
		&tenantRecordRoles{held},
		&tenantGroups{held},
		&tenantCustomRoles{held},
		&tenantInvitations{held},
	}
}

// tenantHeld is the tenant whose delete the kinds take part in, and the manager whose
// store holds the auth's rows of it.
type tenantHeld struct {
	manager *Manager
	domain  accesstypes.Domain
}

// inTenant reports whether a membership is held in the tenant itself: a membership held
// in every tenant is no one tenant's.
func (h *tenantHeld) inTenant(key *access.MembershipKey) bool {
	at := access.NewMembershipKey(accesstypes.DomainPolicyScope(h.domain), "", "")

	return key.Kind == at.Kind && key.Axis == at.Axis && key.Domain == at.Domain
}

// policyChanged has the permission engine told at the commit, where a kind took rows that
// say who holds what.
func (h *tenantHeld) policyChanged(txn resource.ReadWriteTransaction, rows int64) error {
	if rows == 0 {
		return nil
	}

	return h.manager.policyChangesAtCommit(txn)
}

// tenantAccount is an account that belongs to the tenant alone, with what it holds there:
// its memberships, all in the tenant, and its places, all in the tenant's groups.
type tenantAccount struct {
	id          impulse.UUID
	username    string
	memberships []access.MembershipKey
	places      []GroupPlace
}

// accounts reads the accounts that are the tenant's alone, in key order: each person who
// holds a membership in the tenant, a place in one of its groups or a role on one of its
// records, or whom the tenant's directory manages, whose every membership is in the
// tenant, every place in one of its groups and every role on a record that has not ended
// on one of its records, and whom no other tenant's directory manages. A person who holds
// a role for the whole application, in every tenant, in another tenant or on another
// tenant's record stays, and so does an id that is no account's.
func (h *tenantHeld) accounts(ctx context.Context, txn resource.ReadWriteTransaction) ([]tenantAccount, error) {
	store := h.manager.store
	held, err := store.TenantMemberships(ctx, txn, h.domain)
	if err != nil {
		return nil, errors.Wrap(err, "identity.Store.TenantMemberships()")
	}
	groups, err := store.TenantGroups(ctx, txn, h.domain)
	if err != nil {
		return nil, errors.Wrap(err, "identity.Store.TenantGroups()")
	}
	managed, err := store.DirectoryUsers(ctx, txn, h.domain)
	if err != nil {
		return nil, errors.Wrap(err, "identity.Store.DirectoryUsers()")
	}
	onRecords, _, err := store.TenantRecordRoles(ctx, txn, h.domain)
	if err != nil {
		return nil, errors.Wrap(err, "identity.Store.TenantRecordRoles()")
	}

	people := make([]accesstypes.User, 0, len(held)+len(managed)+len(onRecords))
	for _, key := range held {
		people = append(people, key.User)
	}
	for _, key := range onRecords {
		people = append(people, key.User)
	}
	for _, id := range groups {
		members, err := store.GroupMembers(ctx, txn, id)
		if err != nil {
			return nil, errors.Wrap(err, "identity.Store.GroupMembers()")
		}
		people = append(people, members...)
	}
	for _, id := range managed {
		people = append(people, User(id))
	}
	ids := make([]impulse.UUID, 0, len(people))
	seen := make(map[impulse.UUID]bool, len(people))
	for _, user := range people {
		if id, ok := UserID(user); ok && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	slices.SortFunc(ids, func(a, b impulse.UUID) int { return strings.Compare(a.String(), b.String()) })

	var accounts []tenantAccount
	for _, id := range ids {
		account, alone, err := h.alone(ctx, txn, id, groups)
		if err != nil {
			return nil, err
		}
		if alone {
			accounts = append(accounts, *account)
		}
	}

	return accounts, nil
}

// alone reads whether the account of the id is the tenant's alone (accounts), and what it
// holds there where it is. groups are the tenant's own groups.
func (h *tenantHeld) alone(ctx context.Context, txn resource.ReadWriteTransaction, id impulse.UUID, groups []impulse.UUID) (*tenantAccount, bool, error) {
	store := h.manager.store
	memberships, err := store.Memberships(ctx, txn, id)
	if err != nil {
		return nil, false, errors.Wrap(err, "identity.Store.Memberships()")
	}
	if slices.ContainsFunc(memberships, func(key access.MembershipKey) bool { return !h.inTenant(&key) }) {
		return nil, false, nil
	}
	places, err := store.UserGroups(ctx, txn, id)
	if err != nil {
		return nil, false, errors.Wrap(err, "identity.Store.UserGroups()")
	}
	if slices.ContainsFunc(places, func(place GroupPlace) bool { return !slices.Contains(groups, place.ID) }) {
		return nil, false, nil
	}
	records, err := store.RecordMemberships(ctx, txn, id)
	if err != nil {
		return nil, false, errors.Wrap(err, "identity.Store.RecordMemberships()")
	}
	if slices.ContainsFunc(records, func(key access.RecordMembershipKey) bool { return !recordHeldIn(&key, h.domain) }) {
		return nil, false, nil
	}
	if elsewhere, err := h.manager.managedElsewhere(ctx, txn, id, h.domain); err != nil {
		return nil, false, err
	} else if elsewhere {
		return nil, false, nil
	}
	username, found, err := store.Username(ctx, txn, id)
	if err != nil {
		return nil, false, errors.Wrap(err, "identity.Store.Username()")
	}
	if !found {
		return nil, false, nil
	}

	return &tenantAccount{id: id, username: username, memberships: memberships, places: places}, true, nil
}

// refuseImpersonator refuses the tenant's delete where one of the accounts is the real
// person's behind a session that acts as someone else: nobody is the subject of what they
// do there, and the delete would delete their own account.
func (h *tenantHeld) refuseImpersonator(ctx context.Context, accounts []tenantAccount) error {
	for i := range accounts {
		if err := h.manager.refuseImpersonatorAsSubject(ctx, accounts[i].id, "delete the only tenant your own account holds a role in"); err != nil {
			return err
		}
	}

	return nil
}

// tenantAccounts is the kind of the accounts that are the tenant's alone. One costs its
// memberships, its places, its identity links, its row, the ending of its sessions and of
// the impersonation records it holds, and what the database removes with the row, and is
// taken whole or not at all.
type tenantAccounts struct {
	*tenantHeld
}

// Words are what the question calls the accounts.
func (*tenantAccounts) Words() resource.DeleteWords {
	return resource.DeleteWords{One: "account", Many: "accounts"}
}

// Count is the number of accounts that are the tenant's alone.
func (k *tenantAccounts) Count(ctx context.Context, txn resource.ReadWriteTransaction) (int64, error) {
	accounts, err := k.accounts(ctx, txn)
	if err != nil {
		return 0, err
	}

	return int64(len(accounts)), nil
}

// Check refuses the delete, in a session that acts as someone else, where the real
// person's own account is one of the tenant's alone (refuseImpersonatorAsSubject).
func (k *tenantAccounts) Check(ctx context.Context, txn resource.ReadWriteTransaction) error {
	accounts, err := k.accounts(ctx, txn)
	if err != nil {
		return err
	}

	return k.refuseImpersonator(ctx, accounts)
}

// Take asks every account what Check asks before any is taken, then takes them in key
// order while the next one's cost fits the budget: the person's memberships in the
// tenant, their places in its groups, then the account with its identity links, its
// sessions ended first (deleteAccount).
func (k *tenantAccounts) Take(ctx context.Context, txn resource.ReadWriteTransaction, budget int, _ ...eventsource.EventSource) (rows int64, mutations int, more bool, err error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	accounts, err := k.accounts(ctx, txn)
	if err != nil {
		return 0, 0, false, err
	}
	if err := k.refuseImpersonator(ctx, accounts); err != nil {
		return 0, 0, false, err
	}
	costs := k.manager.store.DeleteCosts()
	for i := range accounts {
		cost, err := k.cost(ctx, txn, &accounts[i], &costs)
		if err != nil {
			return rows, mutations, false, err
		}
		if mutations+cost > budget {
			return rows, mutations, true, k.policyChanged(txn, rows)
		}
		if err := k.take(ctx, txn, &accounts[i]); err != nil {
			return rows, mutations, false, err
		}
		rows++
		mutations += cost
	}

	return rows, mutations, false, k.policyChanged(txn, rows)
}

// cost is what taking the account costs in the commit: its memberships, its places, its
// identity links, its row, the ending of its sessions and of the impersonation records it
// holds, and what the database removes with the row.
func (k *tenantAccounts) cost(ctx context.Context, txn resource.ReadWriteTransaction, account *tenantAccount, costs *TenantDeleteCosts) (int, error) {
	links, err := k.manager.store.Identities(ctx, txn, account.id)
	if err != nil {
		return 0, errors.Wrap(err, "identity.Store.Identities()")
	}
	endings, err := k.manager.sessionEndings(ctx, txn, account.id)
	if err != nil {
		return 0, err
	}
	ended := 0
	for i := range endings {
		if endings[i].session {
			ended += costs.SessionEnd
		}
		if endings[i].record {
			ended += costs.ImpersonationEnd
		}
	}
	withUser, err := k.manager.store.RowsWithUser(ctx, txn, account.id)
	if err != nil {
		return 0, errors.Wrap(err, "identity.Store.RowsWithUser()")
	}

	return len(account.memberships)*costs.Membership + len(account.places)*costs.GroupMember +
		len(links)*costs.Identity + costs.User + ended + withUser, nil
}

// take buffers the delete of the account and of what it holds in the tenant.
func (k *tenantAccounts) take(ctx context.Context, txn resource.ReadWriteTransaction, account *tenantAccount) error {
	store := k.manager.store
	for i := range account.memberships {
		if err := store.DeleteMembership(ctx, txn, &account.memberships[i]); err != nil {
			return errors.Wrap(err, "identity.Store.DeleteMembership()")
		}
	}
	for _, place := range account.places {
		if err := store.DeleteGroupMember(ctx, txn, &access.GroupMemberKey{Group: GroupID(place.ID), User: User(account.id)}); err != nil {
			return errors.Wrap(err, "identity.Store.DeleteGroupMember()")
		}
	}
	if err := k.manager.deleteAccount(ctx, txn, account.id); err != nil {
		return err
	}
	logger.FromCtx(ctx).Infof("identity: %q belonged to the tenant %s alone, which is deleted, so the account is deleted with its identity links; its sessions are ended", account.username, k.domain)

	return nil
}

// tenantRolesHeld is the kind of the roles held in the tenant: every membership held in
// it, whoever holds it, and every role a group kept for the whole application holds in
// it. The roles the tenant's own groups hold go with the groups. A membership an account
// of the tenant's alone held is taken with the account too, and is found and deleted again
// here in the same save, as every delete finds the rows the database held before the save.
type tenantRolesHeld struct {
	*tenantHeld
}

// Words are what the question calls the roles held.
func (*tenantRolesHeld) Words() resource.DeleteWords {
	return resource.DeleteWords{One: "role held", Many: "roles held"}
}

// rolesHeld reads the memberships held in the tenant and the roles groups kept for the
// whole application hold there, each in key order.
func (k *tenantRolesHeld) rolesHeld(ctx context.Context, txn resource.ReadWriteTransaction) ([]access.MembershipKey, []access.GroupRoleKey, error) {
	store := k.manager.store
	held, err := store.TenantMemberships(ctx, txn, k.domain)
	if err != nil {
		return nil, nil, errors.Wrap(err, "identity.Store.TenantMemberships()")
	}
	groups, err := store.TenantGroups(ctx, txn, k.domain)
	if err != nil {
		return nil, nil, errors.Wrap(err, "identity.Store.TenantGroups()")
	}
	roles, err := store.TenantGroupRoles(ctx, txn, k.domain)
	if err != nil {
		return nil, nil, errors.Wrap(err, "identity.Store.TenantGroupRoles()")
	}
	roles = slices.DeleteFunc(roles, func(key access.GroupRoleKey) bool {
		return slices.ContainsFunc(groups, func(id impulse.UUID) bool { return GroupID(id) == key.Group })
	})
	slices.SortFunc(held, func(a, b access.MembershipKey) int {
		return cmp.Or(strings.Compare(a.Kind, b.Kind), strings.Compare(a.Axis, b.Axis), strings.Compare(a.Domain, b.Domain),
			strings.Compare(string(a.Role), string(b.Role)), strings.Compare(string(a.User), string(b.User)))
	})
	slices.SortFunc(roles, func(a, b access.GroupRoleKey) int { return compareGroupRoles(&a, &b) })

	return held, roles, nil
}

// compareGroupRoles orders the rows of a group's roles by their table's key.
func compareGroupRoles(a, b *access.GroupRoleKey) int {
	return cmp.Or(strings.Compare(string(a.Group), string(b.Group)), strings.Compare(a.Kind, b.Kind), strings.Compare(a.Axis, b.Axis),
		strings.Compare(a.Domain, b.Domain), strings.Compare(string(a.Role), string(b.Role)))
}

// Count is the number of roles held in the tenant.
func (k *tenantRolesHeld) Count(ctx context.Context, txn resource.ReadWriteTransaction) (int64, error) {
	held, roles, err := k.rolesHeld(ctx, txn)
	if err != nil {
		return 0, err
	}

	return int64(len(held) + len(roles)), nil
}

// Check asks nothing: the tenant's delete asks one permission, to delete a tenant.
func (k *tenantRolesHeld) Check(context.Context, resource.ReadWriteTransaction) error {
	return nil
}

// Take takes the memberships, then the groups' roles, while the next one's cost fits the
// budget: one row of its table each.
func (k *tenantRolesHeld) Take(ctx context.Context, txn resource.ReadWriteTransaction, budget int, _ ...eventsource.EventSource) (rows int64, mutations int, more bool, err error) {
	held, roles, err := k.rolesHeld(ctx, txn)
	if err != nil {
		return 0, 0, false, err
	}
	store := k.manager.store
	costs := store.DeleteCosts()
	for i := range held {
		if mutations+costs.Membership > budget {
			return rows, mutations, true, k.policyChanged(txn, rows)
		}
		if err := store.DeleteMembership(ctx, txn, &held[i]); err != nil {
			return rows, mutations, false, errors.Wrap(err, "identity.Store.DeleteMembership()")
		}
		rows++
		mutations += costs.Membership
	}
	for i := range roles {
		if mutations+costs.GroupRole > budget {
			return rows, mutations, true, k.policyChanged(txn, rows)
		}
		if err := store.DeleteGroupRole(ctx, txn, &roles[i]); err != nil {
			return rows, mutations, false, errors.Wrap(err, "identity.Store.DeleteGroupRole()")
		}
		rows++
		mutations += costs.GroupRole
	}

	return rows, mutations, false, k.policyChanged(txn, rows)
}

// tenantRecordRoles is the kind of the roles held on the tenant's records: every role a
// person holds on one, whatever its period, and every role a group kept anywhere but in
// the tenant holds on one. The roles the tenant's own groups hold on its records go with
// the groups, as their roles in the tenant do. A role an account of the tenant's alone
// held on one of its records is taken here, after the account.
type tenantRecordRoles struct {
	*tenantHeld
}

// Words are what the question calls the roles held on the tenant's records.
func (*tenantRecordRoles) Words() resource.DeleteWords {
	return resource.DeleteWords{One: "role held on a record", Many: "roles held on records"}
}

// rolesHeld reads the roles people hold on the tenant's records and the ones groups kept
// elsewhere hold there, each in key order.
func (k *tenantRecordRoles) rolesHeld(ctx context.Context, txn resource.ReadWriteTransaction) ([]access.RecordMembershipKey, []access.RecordGroupRoleKey, error) {
	store := k.manager.store
	people, roles, err := store.TenantRecordRoles(ctx, txn, k.domain)
	if err != nil {
		return nil, nil, errors.Wrap(err, "identity.Store.TenantRecordRoles()")
	}
	groups, err := store.TenantGroups(ctx, txn, k.domain)
	if err != nil {
		return nil, nil, errors.Wrap(err, "identity.Store.TenantGroups()")
	}
	roles = slices.DeleteFunc(roles, func(key access.RecordGroupRoleKey) bool {
		return slices.ContainsFunc(groups, func(id impulse.UUID) bool { return GroupID(id) == key.Group })
	})

	return people, roles, nil
}

// Count is the number of roles held on the tenant's records that the kind takes.
func (k *tenantRecordRoles) Count(ctx context.Context, txn resource.ReadWriteTransaction) (int64, error) {
	people, roles, err := k.rolesHeld(ctx, txn)
	if err != nil {
		return 0, err
	}

	return int64(len(people) + len(roles)), nil
}

// Check asks nothing: the tenant's delete asks one permission, to delete a tenant.
func (k *tenantRecordRoles) Check(context.Context, resource.ReadWriteTransaction) error {
	return nil
}

// Take takes the people's roles, then the groups', while the next one's cost fits the
// budget: one row of its table each.
func (k *tenantRecordRoles) Take(ctx context.Context, txn resource.ReadWriteTransaction, budget int, _ ...eventsource.EventSource) (rows int64, mutations int, more bool, err error) {
	people, roles, err := k.rolesHeld(ctx, txn)
	if err != nil {
		return 0, 0, false, err
	}

	return k.manager.takeRecordRoles(ctx, txn, people, roles, budget)
}

// tenantGroups is the kind of the groups kept in the tenant. One costs the group, its
// members, its roles and its roles on the tenant's records, which its own delete takes
// with it.
type tenantGroups struct {
	*tenantHeld
}

// Words are what the question calls the groups.
func (*tenantGroups) Words() resource.DeleteWords {
	return resource.DeleteWords{One: "group", Many: "groups"}
}

// Count is the number of groups kept in the tenant.
func (k *tenantGroups) Count(ctx context.Context, txn resource.ReadWriteTransaction) (int64, error) {
	groups, err := k.manager.store.TenantGroups(ctx, txn, k.domain)
	if err != nil {
		return 0, errors.Wrap(err, "identity.Store.TenantGroups()")
	}

	return int64(len(groups)), nil
}

// Check asks nothing: the tenant's delete asks one permission, to delete a tenant.
func (k *tenantGroups) Check(context.Context, resource.ReadWriteTransaction) error {
	return nil
}

// Take deletes the groups in key order while the next one's cost fits the budget, each by
// the store's delete of a group with everything under it. A group its own delete could not
// take in one save leaves rows of the kind. A group whose cost does not fit what the round
// has left is taken in part, its members, then its roles and then its roles on records,
// one at a time while the next fits, and goes in the round in which the rest of it fits.
// The store's delete leaves the application's rows out (DeleteGroupWithTenant): the
// tenant's delete took those under the tenant before this kind is reached.
func (k *tenantGroups) Take(ctx context.Context, txn resource.ReadWriteTransaction, budget int, _ ...eventsource.EventSource) (rows int64, mutations int, more bool, err error) {
	store := k.manager.store
	groups, err := store.TenantGroups(ctx, txn, k.domain)
	if err != nil {
		return 0, 0, false, errors.Wrap(err, "identity.Store.TenantGroups()")
	}
	slices.SortFunc(groups, func(a, b impulse.UUID) int { return strings.Compare(a.String(), b.String()) })
	_, onRecords, err := store.TenantRecordRoles(ctx, txn, k.domain)
	if err != nil {
		return 0, 0, false, errors.Wrap(err, "identity.Store.TenantRecordRoles()")
	}
	costs := store.DeleteCosts()
	for _, id := range groups {
		members, err := store.GroupMembers(ctx, txn, id)
		if err != nil {
			return rows, mutations, false, errors.Wrap(err, "identity.Store.GroupMembers()")
		}
		roles, err := store.GroupRoles(ctx, txn, id)
		if err != nil {
			return rows, mutations, false, errors.Wrap(err, "identity.Store.GroupRoles()")
		}
		held := slices.DeleteFunc(slices.Clone(onRecords), func(key access.RecordGroupRoleKey) bool { return key.Group != GroupID(id) })
		cost := costs.Group + len(members)*costs.GroupMember + len(roles)*costs.GroupRole + len(held)*costs.RecordGroupRole
		if mutations+cost > budget {
			taken, spent, err := k.takeInPart(ctx, txn, id, &groupRows{members: members, roles: roles, onRecords: held}, budget-mutations)
			if err != nil {
				return rows, mutations + spent, false, err
			}

			return rows, mutations + spent, true, k.policyChanged(txn, rows+taken)
		}
		left, err := store.DeleteGroupWithTenant(ctx, txn, id)
		if err != nil {
			return rows, mutations, false, errors.Wrap(err, "identity.Store.DeleteGroupWithTenant()")
		}
		mutations += cost
		if left != nil {
			return rows, mutations, true, k.policyChanged(txn, rows+left.Deleted)
		}
		rows++
	}

	return rows, mutations, false, k.policyChanged(txn, rows)
}

// groupRows are the rows a group's delete takes with it: its members, its roles and its
// roles on records.
type groupRows struct {
	members   []accesstypes.User
	roles     []access.GroupRoleKey
	onRecords []access.RecordGroupRoleKey
}

// takeInPart deletes what fits the budget of a group too large for it: its members in key
// order, then its roles in key order, then its roles on records in key order, one row
// each, while the next one's cost fits. The group's own row stays for a later round. It
// reports the rows it deleted and their cost.
func (k *tenantGroups) takeInPart(ctx context.Context, txn resource.ReadWriteTransaction, id impulse.UUID, rows *groupRows, budget int) (taken int64, mutations int, err error) {
	store := k.manager.store
	costs := store.DeleteCosts()
	members, roles := rows.members, rows.roles
	slices.SortFunc(members, func(a, b accesstypes.User) int { return strings.Compare(string(a), string(b)) })
	slices.SortFunc(roles, func(a, b access.GroupRoleKey) int { return compareGroupRoles(&a, &b) })
	for _, member := range members {
		if mutations+costs.GroupMember > budget {
			return taken, mutations, nil
		}
		if err := store.DeleteGroupMember(ctx, txn, &access.GroupMemberKey{Group: GroupID(id), User: member}); err != nil {
			return taken, mutations, errors.Wrap(err, "identity.Store.DeleteGroupMember()")
		}
		taken++
		mutations += costs.GroupMember
	}
	for i := range roles {
		if mutations+costs.GroupRole > budget {
			return taken, mutations, nil
		}
		if err := store.DeleteGroupRole(ctx, txn, &roles[i]); err != nil {
			return taken, mutations, errors.Wrap(err, "identity.Store.DeleteGroupRole()")
		}
		taken++
		mutations += costs.GroupRole
	}
	for i := range rows.onRecords {
		if mutations+costs.RecordGroupRole > budget {
			return taken, mutations, nil
		}
		if err := store.DeleteRecordGroupRole(ctx, txn, &rows.onRecords[i]); err != nil {
			return taken, mutations, errors.Wrap(err, "identity.Store.DeleteRecordGroupRole()")
		}
		taken++
		mutations += costs.RecordGroupRole
	}

	return taken, mutations, nil
}

// tenantCustomRoles is the kind of the custom roles made in the tenant, taken together
// when they all fit. One costs its row; its grants go by the database's rule with it.
type tenantCustomRoles struct {
	*tenantHeld
}

// Words are what the question calls the custom roles.
func (*tenantCustomRoles) Words() resource.DeleteWords {
	return resource.DeleteWords{One: "custom role", Many: "custom roles"}
}

// Count is the number of custom roles made in the tenant.
func (k *tenantCustomRoles) Count(ctx context.Context, txn resource.ReadWriteTransaction) (int64, error) {
	roles, err := k.manager.store.TenantRoles(ctx, txn, k.domain)
	if err != nil {
		return 0, errors.Wrap(err, "identity.Store.TenantRoles()")
	}

	return int64(roles), nil
}

// Check asks nothing: the tenant's delete asks one permission, to delete a tenant.
func (k *tenantCustomRoles) Check(context.Context, resource.ReadWriteTransaction) error {
	return nil
}

// Take deletes every custom role of the tenant at once where their cost fits the budget,
// and none where it does not.
func (k *tenantCustomRoles) Take(ctx context.Context, txn resource.ReadWriteTransaction, budget int, _ ...eventsource.EventSource) (rows int64, mutations int, more bool, err error) {
	store := k.manager.store
	roles, err := store.TenantRoles(ctx, txn, k.domain)
	if err != nil {
		return 0, 0, false, errors.Wrap(err, "identity.Store.TenantRoles()")
	}
	if roles == 0 {
		return 0, 0, false, nil
	}
	cost := store.DeleteCosts().Role
	if roles*cost > budget {
		return 0, 0, true, nil
	}
	deleted, err := store.DeleteTenantRoles(ctx, txn, k.domain)
	if err != nil {
		return 0, 0, false, errors.Wrap(err, "identity.Store.DeleteTenantRoles()")
	}

	return int64(deleted), deleted * cost, false, k.policyChanged(txn, int64(deleted))
}

// tenantInvitations is the kind of the invitations into the tenant that still stand, each
// withdrawn as WithdrawInvitation withdraws one, so no link admits a person into a tenant
// that is gone. A withdrawal gives nobody anything and takes nothing anybody holds, so the
// permission engine is not told of it.
type tenantInvitations struct {
	*tenantHeld
}

// Words are what the question calls the invitations.
func (*tenantInvitations) Words() resource.DeleteWords {
	return resource.DeleteWords{One: "invitation", Many: "invitations"}
}

// Count is the number of invitations into the tenant that still stand.
func (k *tenantInvitations) Count(ctx context.Context, txn resource.ReadWriteTransaction) (int64, error) {
	invitations, err := k.manager.store.TenantInvitations(ctx, txn, k.domain)
	if err != nil {
		return 0, errors.Wrap(err, "identity.Store.TenantInvitations()")
	}

	return int64(len(invitations)), nil
}

// Check asks nothing: the tenant's delete asks one permission, to delete a tenant.
func (k *tenantInvitations) Check(context.Context, resource.ReadWriteTransaction) error {
	return nil
}

// Take withdraws the invitations in key order while the next one's cost fits the budget.
func (k *tenantInvitations) Take(ctx context.Context, txn resource.ReadWriteTransaction, budget int, _ ...eventsource.EventSource) (rows int64, mutations int, more bool, err error) {
	store := k.manager.store
	invitations, err := store.TenantInvitations(ctx, txn, k.domain)
	if err != nil {
		return 0, 0, false, errors.Wrap(err, "identity.Store.TenantInvitations()")
	}
	slices.SortFunc(invitations, func(a, b impulse.UUID) int { return strings.Compare(a.String(), b.String()) })
	cost := store.DeleteCosts().Invitation
	for _, id := range invitations {
		if mutations+cost > budget {
			return rows, mutations, true, nil
		}
		if err := store.ConsumeMailLink(ctx, txn, id); err != nil {
			return rows, mutations, false, errors.Wrap(err, "identity.Store.ConsumeMailLink()")
		}
		rows++
		mutations += cost
	}

	return rows, mutations, false, nil
}
