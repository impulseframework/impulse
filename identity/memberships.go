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

// User answers a person as the permission store names them: the id of their account,
// as text. A username can change, and two auths over one database can each hold the
// same one; an account's id does neither, so a membership, a session's principal and
// the subject of a row condition all carry it, and the username is what people read.
func User(userID impulse.UUID) accesstypes.User {
	return accesstypes.User(userID.String())
}

// UserID answers the account a permission store's user names; ok is false for a user
// that is no account's id (the actor of a session that acts as a role from another
// application, who has no account here).
func UserID(user accesstypes.User) (userID impulse.UUID, ok bool) {
	id, err := impulse.UUIDFromString(string(user))
	if err != nil {
		return impulse.NilUUID, false
	}

	return id, true
}

// AssignRole buffers the membership that says the person holds the role in the scope,
// in the caller's transaction, and has the permission engine told once the transaction
// has committed, so the person's next request is served with the role. The person is
// named by the id of their account, the engine's name for them (User), and is not
// looked up: a user created earlier in the same transaction is not readable until the
// commit, and the caller holds the id. A membership the person holds already is left
// as it is.
//
// "Holds already" is what the database has committed and what this transaction's own
// patches have written: the store writes the row as a create-unless-there (the generated
// patch's OrSkip), which asks the transaction's record of the rows it wrote before it
// reads the stored row, since no read sees a buffered write. So a second call for one
// membership in one transaction is left as it is too. That record belongs to the library
// transaction the call is handed. The two hooks of a sign-in each wrap the session's
// transaction on their own and share none, which is why the session library keeps a
// default role the accepted invitation names too out of the directory hook's hands.
//
// The rule is the permission engine's (access.UserManager.Membership): the role exists
// in the scope under exactly this spelling, and the person is somebody (the nil id is
// refused). A role of the release's role file is known
// to the engine; a custom role is read from the auth's roles table in the caller's
// transaction, so a role deleted beside the membership fails one of the two.
//
// Where a person belongs to one tenant (WithOneTenantPerPerson), a role in a tenant is
// refused for a person who holds a role in another one, as a Conflict that never says
// which (refuseSecondTenant). A role for the whole application or in every tenant is
// given whatever the person holds, and its holder is given a role in any tenant.
//
// The Super Administrator role is given in the global scope alone, and by a caller who
// holds it (requireSuperAdministrator): only a super administrator makes another. A
// call outside any request is the application's own act and gives it.
func (m *Manager) AssignRole(ctx context.Context, txn resource.ReadWriteTransaction, scope accesstypes.PolicyScope, userID impulse.UUID, role accesstypes.Role) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if userID.IsNil() {
		return httpio.NewBadRequestMessage("a role is given to a person: the user id is empty")
	}
	if role == accesstypes.SuperAdministrator && !scope.IsGlobal() {
		return httpio.NewBadRequestMessagef("the %s role is held for the whole application alone, never in a %s or in every %s: give it with no %s named", string(role), m.tenantWord, m.tenantWord, m.tenantWord)
	}
	key, err := m.access.UserManager().Membership(ctx, scope, User(userID), role, func(ctx context.Context, key access.RoleKey) (bool, error) {
		held, err := m.store.HasRole(ctx, txn, &key)
		if err != nil {
			return false, errors.Wrap(err, "identity.Store.HasRole()")
		}

		return held, nil
	})
	if err != nil {
		return errors.Wrap(err, "access.UserManager.Membership()")
	}
	if role == accesstypes.SuperAdministrator {
		if err := m.requireSuperAdministrator(ctx, txn, "gives the Super Administrator role"); err != nil {
			return err
		}
	}
	if err := m.refuseSecondTenant(ctx, txn, scope, userID); err != nil {
		return err
	}
	inserted, err := m.store.InsertMembership(ctx, txn, &key)
	if err != nil {
		return errors.Wrap(err, "identity.Store.InsertMembership()")
	}
	if !inserted {
		return nil
	}

	return m.policyChangesAtCommit(txn)
}

// RemoveRole buffers the removal of the person's membership of the role in the scope,
// and has the permission engine told once the transaction has committed. The person is
// named by the id of their account. A membership the person does not hold is no error,
// and the role need not exist any more: a membership of a role since deleted is removed
// like any other.
//
// Two rules of the user management are held here. A person who removes a role from
// themselves in a tenant is held to the rule on the tenant's last administrator
// (refuseLastAdministrator). And a person left with no role anywhere is deleted with
// their identity links and sessions in the same transaction (deleteWithLastRole),
// whoever removed the role. The Super Administrator role has its own: it is taken away
// by a caller who holds it, and never from the last holder who can sign in, whoever
// asks (requireSuperAdministrator, refuseLastSuperAdministrator).
// Where a person belongs to one tenant (WithOneTenantPerPerson) there is a third: the
// last role a person holds outside the tenants stays while they hold roles in more than
// one tenant (refuseLeavingSeveralTenants).
//
// What the person holds is read as committed: a second removal for the same person in
// one transaction does not see the first, as no read sees a buffered write, so the
// account is deleted only when one call removes the last role. Remove one role of a
// person per transaction.
func (m *Manager) RemoveRole(ctx context.Context, txn resource.ReadWriteTransaction, scope accesstypes.PolicyScope, userID impulse.UUID, role accesstypes.Role) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	key := access.NewMembershipKey(scope, User(userID), role)
	held, err := m.store.HasMembership(ctx, txn, &key)
	if err != nil {
		return errors.Wrap(err, "identity.Store.HasMembership()")
	}
	if !held {
		return nil
	}

	return m.removeMembership(ctx, txn, scope, userID, &key)
}

// removeMembership buffers the removal of a membership the person holds, under the
// rules of RemoveRole, and has the permission engine told once the transaction has
// committed. key is the membership, read as held by the caller.
func (m *Manager) removeMembership(ctx context.Context, txn resource.ReadWriteTransaction, scope accesstypes.PolicyScope, userID impulse.UUID, key *access.MembershipKey) error {
	switch {
	case scope.IsGlobal() && key.Role == accesstypes.SuperAdministrator:
		if err := m.requireSuperAdministrator(ctx, txn, "takes the Super Administrator role away"); err != nil {
			return err
		}
		if err := m.refuseLastSuperAdministrator(ctx, txn, userID, "take the role from them"); err != nil {
			return err
		}
	case m.callerIs(ctx, userID):
		if err := m.refuseLastAdministrator(ctx, txn, scope, key); err != nil {
			return err
		}
	}
	all, err := m.store.Memberships(ctx, txn, userID)
	if err != nil {
		return errors.Wrap(err, "identity.Store.Memberships()")
	}
	if err := m.refuseLeavingSeveralTenants(all, key); err != nil {
		return err
	}
	if err := m.deleteWithLastRole(ctx, txn, userID, all, key); err != nil {
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
