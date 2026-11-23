package identity

import (
	"context"
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
// (refuseLastAdministrator). And a person left with no role anywhere and no place in a
// group is deleted with their identity links and sessions in the same transaction
// (deleteWithLastRole), whoever removed the role. The Super Administrator role has its own: it is taken away
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
// rules of RemoveRole, and has the permission engine told once the transaction has
// committed. key is the membership, read as held by the caller.
func (m *Manager) removeMembership(ctx context.Context, txn resource.ReadWriteTransaction, scope accesstypes.PolicyScope, userID impulse.UUID, key *access.MembershipKey) error {
	// A membership whose period is over makes nobody a tenant's administrator, and its
	// removal is not the directory-only tenant's to hold.
	ended, err := m.membershipEnded(ctx, txn, userID, key)
	if err != nil {
		return err
	}
	if !ended {
		if err := m.requireEnteringToChange(ctx, scope, key.Role, nil); err != nil {
			return err
		}
	}
	switch {
	case scope.IsGlobal() && key.Role == accesstypes.SuperAdministrator:
		if err := m.requireSuperAdministrator(ctx, txn, "takes the Super administrator role away"); err != nil {
			return err
		}
		if err := m.refuseLastSuperAdministrator(ctx, txn, userID, "take the role from them"); err != nil {
			return err
		}
	case !ended && m.callerIs(ctx, userID):
		if err := m.refuseLastAdministrator(ctx, txn, scope, ownRoleGone(key)); err != nil {
			return err
		}
	}
	// What the person holds beside the membership: the roles of their own and the
	// groups they are in. Where they belong is read from what has not ended (holds);
	// the account is kept by every row that stands in the tables, ended or not (keeps).
	all, err := m.holds(ctx, txn, userID)
	if err != nil {
		return err
	}
	if err := m.refuseLeavingSeveralTenants(all, key); err != nil {
		return err
	}
	kept, err := m.keeps(ctx, txn, userID)
	if err != nil {
		return err
	}
	// The notice is mailed to the address the person confirmed, read while the account
	// is still there to read it from.
	// A membership whose period is over gave the person nothing: its removal is no
	// removal from a tenant.
	if !ended {
		deleted := !slices.ContainsFunc(kept, func(k access.MembershipKey) bool { return k != *key })
		if err := m.noticeRemoval(ctx, txn, userID, &taken{own: key, group: impulse.NilUUID, groupRole: nil}, []access.MembershipKey{*key}, deleted); err != nil {
			return err
		}
	}
	if err := m.deleteWithLastRole(ctx, txn, userID, kept, key); err != nil {
		return err
	}
	if err := m.store.DeleteMembership(ctx, txn, key); err != nil {
		return errors.Wrap(err, "identity.Store.DeleteMembership()")
	}

	return m.policyChangesAtCommit(txn)
}

// taken is what an act takes from a person, for the notice of a removal from a tenant:
// a membership of their own, their place in a group, or one role of a group they are
// in.
type taken struct {
	own       *access.MembershipKey
	group     impulse.UUID
	groupRole *access.GroupRoleKey
}

// reachWithout answers the roles a person holds that have not ended, of their own and
// through the groups they stand in, less what an act takes (gone): each as the key of
// the role where it is held. now is the instant the periods are read at.
func (m *Manager) reachWithout(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID, gone *taken, now time.Time) ([]access.MembershipKey, error) {
	own, places, err := m.standing(ctx, txn, userID)
	if err != nil {
		return nil, err
	}
	left := make([]access.MembershipKey, 0, len(own))
	skipped := false
	for i := range own {
		if gone.own != nil && !skipped && own[i] == *gone.own {
			skipped = true

			continue
		}
		left = append(left, own[i])
	}
	for _, place := range places {
		if place.ID == gone.group && gone.groupRole == nil {
			continue
		}
		holding, err := m.groupHolding(ctx, txn, place.ID, now)
		if err != nil {
			return nil, err
		}
		for _, role := range holding.roles {
			if gone.groupRole != nil && role == *gone.groupRole {
				continue
			}
			left = append(left, access.MembershipKey{Kind: role.Kind, Axis: role.Axis, Domain: role.Domain, Role: role.Role, User: User(userID)})
		}
	}

	return left, nil
}

// noticeRemoval registers the notice that an administrator removed a person from a
// tenant, for after the commit. lost are the roles the act (gone) takes from the
// person, each where it is held; the notice is owed for each tenant among them that
// the person reaches no more once the act is done: no role of their own there that has
// not ended, none through a group they stand in, and none held in every tenant at
// once, which reaches each. It says so when the account goes with the act (deleted).
// A person who leaves a tenant themselves is told nothing, nor is one who loses a role
// held for the whole application or in every tenant; a role that ends by itself is no
// removal and never reaches this, and neither does the deletion of an account or the
// clearing of a tenant, which are not removals of a person from a tenant.
func (m *Manager) noticeRemoval(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID, gone *taken, lost []access.MembershipKey, deleted bool) error {
	if m.callerIs(ctx, userID) {
		return nil
	}
	left, err := m.reachWithout(ctx, txn, userID, gone, time.Now())
	if err != nil {
		return err
	}
	everywhere := access.NewMembershipKey(accesstypes.EveryDomainPolicyScope(), User(userID), "")
	told := map[string]bool{}
	for i := range lost {
		at := &lost[i]
		if _, one := tenantScope(at); !one || told[at.Domain] {
			continue
		}
		// What still holds the person in the tenant: another role in the tenant itself,
		// or one held in every tenant at once, which reaches this one.
		if slices.ContainsFunc(left, func(key access.MembershipKey) bool {
			return key.Axis == at.Axis && ((key.Kind == at.Kind && key.Domain == at.Domain) || (key.Kind == everywhere.Kind && key.Domain == everywhere.Domain))
		}) {
			continue
		}
		told[at.Domain] = true
		address, confirmed, err := m.store.ConfirmedEmail(ctx, txn, userID)
		if err != nil {
			return errors.Wrap(err, "identity.Store.ConfirmedEmail()")
		}
		if !confirmed {
			return nil
		}
		tenant := at.Domain
		if err := m.noticeAfterCommit(txn, "the notice of a removal from "+tenant+" to user "+userID.String(), func(ctx context.Context) error {
			return m.sessions.NoticeRemovedFromTenant(ctx, address, tenant, deleted)
		}); err != nil {
			return err
		}
	}

	return nil
}

// noticePlaceTaken registers the notice owed a person an administrator takes out of a
// group: taking the person out takes each role the group holds from them, and the
// notice is owed for each tenant among those roles the person reaches no more
// (noticeRemoval). A place whose period has ended gave the person nothing, and its
// removal is no removal from a tenant. deleted says the account goes with the place.
func (m *Manager) noticePlaceTaken(ctx context.Context, txn resource.ReadWriteTransaction, groupID, userID impulse.UUID, deleted bool) error {
	holding, err := m.groupHolding(ctx, txn, groupID, time.Now())
	if err != nil {
		return err
	}
	if !slices.Contains(holding.members, User(userID)) {
		return nil
	}
	lost := make([]access.MembershipKey, 0, len(holding.roles))
	for _, role := range holding.roles {
		lost = append(lost, access.MembershipKey{Kind: role.Kind, Axis: role.Axis, Domain: role.Domain, Role: role.Role, User: User(userID)})
	}

	return m.noticeRemoval(ctx, txn, userID, &taken{own: nil, group: groupID, groupRole: nil}, lost, deleted)
}

// noticeGroupRoleTaken registers the notice owed each person of a group an
// administrator takes a role from: the role goes from everyone who stands in the group,
// and the notice is owed to each of them who reaches the role's tenant no more
// (noticeRemoval). A role whose period has ended gave nobody anything, and a role held
// for the whole application or in every tenant at once is no one tenant's: nobody is
// read for it. No account goes with a group's role: the person keeps their place in the
// group.
func (m *Manager) noticeGroupRoleTaken(ctx context.Context, txn resource.ReadWriteTransaction, groupID impulse.UUID, key *access.GroupRoleKey) error {
	if _, one := tenantScope(&access.MembershipKey{Kind: key.Kind, Axis: key.Axis, Domain: key.Domain, Role: key.Role, User: ""}); !one {
		return nil
	}
	holding, err := m.groupHolding(ctx, txn, groupID, time.Now())
	if err != nil {
		return err
	}
	if !slices.Contains(holding.roles, *key) {
		return nil
	}
	for _, member := range holding.members {
		userID, ok := UserID(member)
		if !ok {
			continue
		}
		lost := []access.MembershipKey{{Kind: key.Kind, Axis: key.Axis, Domain: key.Domain, Role: key.Role, User: member}}
		if err := m.noticeRemoval(ctx, txn, userID, &taken{own: nil, group: groupID, groupRole: key}, lost, false); err != nil {
			return err
		}
	}

	return nil
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
