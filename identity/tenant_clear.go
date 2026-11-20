package identity

import (
	"context"
	"slices"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/logger"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/tracer"
)

// ClearTenant buffers the removal of everything the auth holds for a tenant, in the
// transaction that deletes the tenant's row, so a tenant that goes leaves nothing of
// itself in the auth's tables:
//
//   - every membership held in the tenant, whoever holds it;
//   - every group kept in the tenant, with its members and the roles it holds, and every
//     role a group kept for the whole application holds in the tenant;
//   - every custom role made in the tenant, with its grants;
//   - every invitation into the tenant that still stands, withdrawn as
//     WithdrawInvitation withdraws one, so no link admits a person into a tenant that
//     is gone.
//
// A person the tenant's memberships and the places in its groups were the only things
// held by then holds no role anywhere and no place in a group, and is deleted with their
// identity links and sessions in the same transaction, by the rule on an account nothing
// admits (deleteWithLastRole). A person who holds a role
// anywhere else stays, and so does a role held in every tenant, which is no one
// tenant's.
//
// In a session that acts as someone else, the account of the person who started the
// session is not one of those: nobody is the subject of what they do there, so a tenant
// whose going would delete the real person's own account is not cleared, and the
// delete of the tenant is refused with the rule's sentence and its log line
// (refuseImpersonatorAsSubject), as RemoveRole and RemoveFromGroup refuse the removal
// that would delete it. The rule is asked once everything is read and before anything
// is buffered.
//
// The generated delete of the tenant record calls it for each auth whose management the
// program generates, before the transaction commits; the permission engine is told once
// it has. Everything is read before anything is buffered. A tenant the auth holds
// nothing for is no error.
func (m *Manager) ClearTenant(ctx context.Context, txn resource.ReadWriteTransaction, domain accesstypes.Domain) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if domain == "" {
		return errors.New("a tenant was to be cleared and no tenant was named")
	}
	held, err := m.store.TenantMemberships(ctx, txn, domain)
	if err != nil {
		return errors.Wrap(err, "identity.Store.TenantMemberships()")
	}
	groups, err := m.readTenantGroups(ctx, txn, domain)
	if err != nil {
		return err
	}
	leaving, err := m.leftWithNothing(ctx, txn, held, groups)
	if err != nil {
		return err
	}
	for _, person := range leaving {
		if err := m.refuseImpersonatorAsSubject(ctx, person.id, "delete the only tenant your own account holds a role in"); err != nil {
			return err
		}
	}
	invitations, err := m.store.TenantInvitations(ctx, txn, domain)
	if err != nil {
		return errors.Wrap(err, "identity.Store.TenantInvitations()")
	}

	for _, key := range held {
		if err := m.store.DeleteMembership(ctx, txn, &key); err != nil {
			return errors.Wrap(err, "identity.Store.DeleteMembership()")
		}
	}
	if err := groups.clear(ctx, txn, m.store); err != nil {
		return err
	}
	for _, person := range leaving {
		if err := m.deleteAccount(ctx, txn, person.id); err != nil {
			return err
		}
		logger.FromCtx(ctx).Infof("identity: %q held roles in the tenant %s alone, which is deleted, so the account is deleted with its identity links and sessions", person.username, domain)
	}
	roles, err := m.store.DeleteTenantRoles(ctx, txn, domain)
	if err != nil {
		return errors.Wrap(err, "identity.Store.DeleteTenantRoles()")
	}
	for _, id := range invitations {
		if err := m.store.ConsumeMailLink(ctx, txn, id); err != nil {
			return errors.Wrap(err, "identity.Store.ConsumeMailLink()")
		}
	}
	if len(held) == 0 && roles == 0 && groups.empty() {
		return nil
	}

	return m.policyChangesAtCommit(txn)
}

// leavingAccount is an account the removal of a tenant's memberships leaves holding no
// role anywhere.
type leavingAccount struct {
	id       impulse.UUID
	username string
}

// leftWithNothing answers the accounts that hold nothing but what the tenant holds of
// them: the people whose memberships are all the tenant's and whose places in groups are
// all in the tenant's own groups. held is every membership of the tenant and groups what
// it holds of groups; a membership or a place whose user is no account's id, or the id of
// an account that is gone, has no account.
func (m *Manager) leftWithNothing(ctx context.Context, txn resource.ReadWriteTransaction, held []access.MembershipKey, groups *tenantGroups) ([]leavingAccount, error) {
	var (
		leaving  []leavingAccount
		seen     = make(map[accesstypes.User]bool, len(held))
		inTenant = make(map[access.MembershipKey]bool, len(held))
		people   = make([]accesstypes.User, 0, len(held)+len(groups.members))
	)
	for _, key := range held {
		inTenant[key] = true
		people = append(people, key.User)
	}
	for _, member := range groups.members {
		people = append(people, member.User)
	}
	for _, user := range people {
		if seen[user] {
			continue
		}
		seen[user] = true
		id, ok := UserID(user)
		if !ok {
			continue
		}
		all, err := m.store.Memberships(ctx, txn, id)
		if err != nil {
			return nil, errors.Wrap(err, "identity.Store.Memberships()")
		}
		if slices.ContainsFunc(all, func(other access.MembershipKey) bool { return !inTenant[other] }) {
			continue
		}
		places, err := m.store.UserGroups(ctx, txn, id)
		if err != nil {
			return nil, errors.Wrap(err, "identity.Store.UserGroups()")
		}
		if slices.ContainsFunc(places, func(place GroupPlace) bool { return !groups.inGroups(place.ID) }) {
			continue
		}
		username, found, err := m.store.Username(ctx, txn, id)
		if err != nil {
			return nil, errors.Wrap(err, "identity.Store.Username()")
		}
		if found {
			leaving = append(leaving, leavingAccount{id: id, username: username})
		}
	}

	return leaving, nil
}
