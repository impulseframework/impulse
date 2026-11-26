package identity

import (
	"context"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/auditlog"
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
// call outside any request is the application's own act and gives it. Giving it ends
// nothing: the sessions the person holds, their bearer tokens and the browsers
// remembered for their second factor stand, and the session they are signed in with
// acts with the role at its next request. A person who is signed in when the role is
// given is no risk for being signed in; the account is what the role guards, and a
// second factor is asked for at every sign-in from then on. What an administrator set
// on the account before the role, a password or an address, is left as it is too: the
// super administrator who gives the role chooses whom to trust.
//
// The membership is given for the period: it counts from the period's start, where it
// has one, until its end, where it has one, and a membership the person holds already
// is given the period handed in place of the one it had (periods.go). Whoever may give
// the role gives it a period, and the Super Administrator role takes none
// (access.ValidatePeriod). AssignRole gives the membership with no period. Nobody
// lengthens their own holding: a caller who may give the role does not give their own
// membership a period that lasts longer than the one it has, and someone else who may
// give it does (refuseLongerForOneself). Nor does the last administrator of a tenant's
// own cut their role there short: a period of the caller's giving that takes their own
// role away from now on, or from an instant sooner than the one it had, is refused as
// the role's removal is, in the same words (refuseLastAdministrator).
//
// reason says why the role is given: one line of plain text, empty for none
// (access.ValidateReason). It is kept with the membership and shown wherever the roles a
// person holds are listed. A membership the person holds already is given the reason
// handed in place of the one it had, with the period, so a role given again with no
// reason carries none: nothing edits a reason by itself (reasons.go).
func (m *Manager) AssignRoleFor(
	ctx context.Context, txn resource.ReadWriteTransaction, scope accesstypes.PolicyScope, userID impulse.UUID, role accesstypes.Role, period accesstypes.Period, reason string,
) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := m.refuseImpersonatorAsSubject(ctx, userID, "give yourself a role"); err != nil {
		return err
	}
	reason, err := access.ValidateReason(reason)
	if err != nil {
		return errors.Wrap(err, "access.ValidateReason()")
	}

	return m.assignRole(ctx, txn, scope, userID, role, &given{period: period, reason: reason, lengthens: false})
}

// given is what a membership is given with: the period it counts in and the reason it
// is given for, as the rules left it. lengthens says the act may make the caller's own
// membership last longer than it did, which only the taking of a role for a set time
// does (TakeRole).
type given struct {
	period    accesstypes.Period
	reason    string
	lengthens bool
}

// assignRole buffers the membership under the rules of AssignRoleFor, with what it is
// given with.
func (m *Manager) assignRole(ctx context.Context, txn resource.ReadWriteTransaction, scope accesstypes.PolicyScope, userID impulse.UUID, role accesstypes.Role, given *given) error {
	period := given.period
	if userID.IsNil() {
		return httpio.NewBadRequestMessage("No person is named. Name the person the role is for.")
	}
	if err := access.ValidatePeriod(role, period); err != nil {
		return errors.Wrap(err, "access.ValidatePeriod()")
	}
	if err := refuseEnded(period, "the role"); err != nil {
		return err
	}
	if role == accesstypes.SuperAdministrator && !scope.IsGlobal() {
		const (
			said  = "The %s role is held for the whole application alone, never in %s or in every %s. Give it with no %s named."
			cause = "the %s role is held for the whole application alone, never in a %s or in every %s: give it with no %s named"
		)

		return httpio.NewBadRequestMessageWithErrorf(errors.Newf(cause, string(role), m.tenantWord, m.tenantWord, m.tenantWord),
			said, roleInWords(role), withArticle(m.tenantWord), m.tenantWord, m.tenantWord)
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
		if err := m.requireSuperAdministrator(ctx, txn, "gives the Super administrator role"); err != nil {
			return err
		}
	}
	if err := m.requireEnteringToChange(ctx, scope, role, nil); err != nil {
		return err
	}
	if err := m.refuseSecondTenant(ctx, txn, scope, userID); err != nil {
		return err
	}
	inserted, err := m.store.InsertMembership(ctx, txn, &key)
	if err != nil {
		return errors.Wrap(err, "identity.Store.InsertMembership()")
	}
	changed, err := m.settleMembershipPeriod(ctx, txn, scope, userID, &key, given, inserted, m.callerIs(ctx, userID))
	if err != nil {
		return err
	}
	if !inserted && !changed {
		return nil
	}
	// The row names the role where it is held, with the period and the reason the
	// membership now has.
	gave, err := m.accountSubject(ctx, txn, userID)
	if err != nil {
		return err
	}
	gave.Act, gave.Form, gave.RoleNames, gave.Period, gave.Reason = auditlog.ActRoleGiven, roleFormOf(ctx), []string{string(role)}, given.period, given.reason
	gave.RoleScope, gave.TenantID = auditlog.ScopeOf(scope)
	if err := m.record(ctx, txn, gave); err != nil {
		return err
	}

	return m.policyChangesAtCommit(txn)
}

// RemoveRole buffers the removal of the person's membership of the role in the scope,
// and has the permission engine told once the transaction has committed. The person is
// named by the id of their account. A membership the person does not hold is no error,
// and the role need not exist any more: a membership of a role since deleted is removed
// like any other.
//
// A person who removes a role from themselves in a tenant is held to the rule on the
// tenant's last administrator (refuseLastAdministrator). The Super Administrator role has
// its own rule: it is taken away by a caller who holds it, and never from the last holder
// who can sign in, whoever asks (requireSuperAdministrator, refuseLastSuperAdministrator).
// Where a person belongs to one tenant (WithOneTenantPerPerson) there is another: the
// last role a person holds outside the tenants stays while they hold roles in more than
// one tenant (refuseLeavingSeveralTenants). Where the role is the last thing the person
// holds, the account stays, holding nothing.
//
// What the person holds is read as committed: a second removal for the same person in
// one transaction does not see the first, as no read sees a buffered write. Remove one
// role of a person per transaction.
//
// RoleRemovalWarning answers what a call would do, from the same judgment, and writes
// nothing.
func (m *Manager) RemoveRole(ctx context.Context, txn resource.ReadWriteTransaction, scope accesstypes.PolicyScope, userID impulse.UUID, role accesstypes.Role) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := m.refuseImpersonatorAsSubject(ctx, userID, "take a role from yourself"); err != nil {
		return err
	}
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
// rules of RemoveRole as its judgment holds them (judgeRoleRemoval), and has the
// permission engine told once the transaction has committed. key is the membership, read
// as held by the caller.
func (m *Manager) removeMembership(ctx context.Context, txn resource.ReadWriteTransaction, scope accesstypes.PolicyScope, userID impulse.UUID, key *access.MembershipKey) error {
	if err := m.judgeRoleRemoval(ctx, txn, scope, userID, key); err != nil {
		return err
	}
	took, err := m.accountSubject(ctx, txn, userID)
	if err != nil {
		return err
	}
	if err := m.store.DeleteMembership(ctx, txn, key); err != nil {
		return errors.Wrap(err, "identity.Store.DeleteMembership()")
	}
	took.Act, took.RoleNames = auditlog.ActRoleTaken, []string{string(key.Role)}
	took.RoleScope, took.TenantID = auditlog.ScopeOf(scope)
	if err := m.record(ctx, txn, took); err != nil {
		return err
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
		return accesstypes.PolicyScope{}, httpio.NewBadRequestMessage("A role is held in one tenant or in every tenant, not both. Send domain or everyDomain, not the two together.")
	case domain != nil && *domain == "":
		return accesstypes.PolicyScope{}, httpio.NewBadRequestMessage("The tenant named is empty. Name the tenant the role is held in, or leave domain out to give the role for the whole application.")
	case domain != nil:
		return accesstypes.DomainPolicyScope(accesstypes.Domain(*domain)), nil
	case everyDomain:
		return accesstypes.EveryDomainPolicyScope(), nil
	default:
		return accesstypes.GlobalPolicyScope(), nil
	}
}
