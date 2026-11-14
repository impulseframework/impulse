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

// AssignRole buffers the membership that says the person holds the role in the scope,
// in the caller's transaction, and has the permission engine told once the transaction
// has committed, so the person's next request is served with the role. The person is
// named by username, the engine's name for them, and is not looked up: a user created
// earlier in the same transaction is not readable until the commit, and the caller holds
// the username. A membership the person holds already is left as it is.
//
// "Holds already" is what the database has committed: a read in a transaction does not
// see what the transaction itself buffered, so a second call for one membership in one
// transaction buffers the row again and the commit is refused. One transaction gives a
// membership once. The session library keeps to this for the two hooks of a sign-in (a
// default role the accepted invitation names too is left to the invitation).
//
// The rule is the permission engine's (access.UserManager.Membership): the role exists
// in the scope under exactly this spelling, and the username is not empty. A role of the
// release's role file is known to the engine; a custom role is read from the auth's
// roles table in the caller's transaction, so a role deleted beside the membership fails
// one of the two.
func (m *Manager) AssignRole(ctx context.Context, txn resource.ReadWriteTransaction, scope accesstypes.PolicyScope, username string, role accesstypes.Role) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	key, err := m.access.UserManager().Membership(ctx, scope, accesstypes.User(username), role, func(ctx context.Context, key access.RoleKey) (bool, error) {
		held, err := m.store.HasRole(ctx, txn, &key)
		if err != nil {
			return false, errors.Wrap(err, "identity.Store.HasRole()")
		}

		return held, nil
	})
	if err != nil {
		return errors.Wrap(err, "access.UserManager.Membership()")
	}
	held, err := m.store.HasMembership(ctx, txn, &key)
	if err != nil {
		return errors.Wrap(err, "identity.Store.HasMembership()")
	}
	if held {
		return nil
	}
	if err := m.store.InsertMembership(ctx, txn, &key); err != nil {
		return errors.Wrap(err, "identity.Store.InsertMembership()")
	}

	return m.policyChangesAtCommit(txn)
}

// RemoveRole buffers the removal of the person's membership of the role in the scope,
// and has the permission engine told once the transaction has committed. A membership
// the person does not hold is no error, and the role need not exist any more: a
// membership of a role since deleted is removed like any other.
//
// Two rules of the user management are held here. A person who removes a role from
// themselves is held to the rule on the last administrator (refuseLastAdministrator).
// And a person left with no role anywhere is deleted with their identity links and
// sessions in the same transaction (deleteWithLastRole), whoever removed the role.
//
// What the person holds is read as committed: a second removal for the same person in
// one transaction does not see the first, as no read sees a buffered write, so the
// account is deleted only when one call removes the last role. Remove one role of a
// person per transaction.
func (m *Manager) RemoveRole(ctx context.Context, txn resource.ReadWriteTransaction, scope accesstypes.PolicyScope, username string, role accesstypes.Role) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	key := access.NewMembershipKey(scope, accesstypes.User(username), role)
	held, err := m.store.HasMembership(ctx, txn, &key)
	if err != nil {
		return errors.Wrap(err, "identity.Store.HasMembership()")
	}
	if !held {
		return nil
	}

	return m.removeMembership(ctx, txn, scope, username, &key)
}

// removeMembership buffers the removal of a membership the person holds, under the two
// rules of RemoveRole, and has the permission engine told once the transaction has
// committed. key is the membership, read as held by the caller.
func (m *Manager) removeMembership(ctx context.Context, txn resource.ReadWriteTransaction, scope accesstypes.PolicyScope, username string, key *access.MembershipKey) error {
	if callerIs(ctx, username) {
		if err := m.refuseLastAdministrator(ctx, txn, scope, key); err != nil {
			return err
		}
	}
	all, err := m.store.Memberships(ctx, txn, username)
	if err != nil {
		return errors.Wrap(err, "identity.Store.Memberships()")
	}
	if err := m.deleteWithLastRole(ctx, txn, username, all, key); err != nil {
		return err
	}
	if err := m.store.DeleteMembership(ctx, txn, key); err != nil {
		return errors.Wrap(err, "identity.Store.DeleteMembership()")
	}

	return m.policyChangesAtCommit(txn)
}

// policyChangesAtCommit registers the word to the permission engine on the transaction,
// once however many memberships it writes: the engine answers from the policy it holds
// until it is told the rows changed, and a transaction that does not commit changed
// nothing.
func (m *Manager) policyChangesAtCommit(txn resource.ReadWriteTransaction) error {
	if err := resource.AfterCommitOnce(txn, m, func(context.Context) { m.access.PolicyChanged() }); err != nil {
		return errors.Wrap(err, "resource.AfterCommitOnce()")
	}

	return nil
}

// Scope answers the scope a membership is asked for in: one domain when domain names
// one, every domain when everyDomain is set, the global scope otherwise. Naming a domain
// and every domain together is refused, and so is an empty domain: a request says which
// it means.
func Scope(domain *string, everyDomain bool) (accesstypes.PolicyScope, error) {
	switch {
	case domain != nil && everyDomain:
		return accesstypes.PolicyScope{}, httpio.NewBadRequestMessage("a role is held in one domain or in every domain, not both: name the domain or set everyDomain")
	case domain != nil && *domain == "":
		return accesstypes.PolicyScope{}, httpio.NewBadRequestMessage("the domain is empty: name the domain the role is held in, or leave it out for the global scope")
	case domain != nil:
		return accesstypes.DomainPolicyScope(accesstypes.Domain(*domain)), nil
	case everyDomain:
		return accesstypes.EveryDomainPolicyScope(), nil
	default:
		return accesstypes.GlobalPolicyScope(), nil
	}
}
