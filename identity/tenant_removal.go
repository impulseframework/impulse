package identity

import (
	"context"
	"fmt"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/logger"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/tracer"
)

// A person's removal from a tenant. An administrator removes a person from a tenant in one
// act: every row the person has in the tenant goes, as when they leave it themselves
// (LeaveTenant), and an account left holding nothing is deleted with its sign-in
// identities and sessions. The administrator is told first, in a warning the act's own
// judgment answers, which roles go and whether the account goes with them.

// The sentences of a removal from a tenant. Each %s is the application's word for a
// tenant (WithTenantWord); %[1]s in the consequences is the person's username.
const (
	// removingYourselfMessage is said to a person who names their own account: they leave
	// a tenant from their own account page (LeaveTenant).
	removingYourselfMessage = "You cannot remove yourself from this %s here. Leave it from your own account page."

	// removingYourselfUnnamedMessage is said to a person who names their own account
	// where the application named no administrator roles (WithAdministratorRoles), so
	// nobody leaves a tenant themselves.
	removingYourselfUnnamedMessage = "You cannot remove yourself from this %s. Ask another of its administrators to remove you."

	// removingViewedAsMessage is said in a session that views as someone else, of the
	// account it views as.
	removingViewedAsMessage = "You cannot remove the account you are viewing as from this %s."

	// noTenantToRemoveFromMessage is said of a request that names no tenant.
	noTenantToRemoveFromMessage = "Name the %s to remove the person from."

	// nothingToRemoveMessage is said of a person who holds nothing in the tenant itself
	// now: a role held in every tenant, a role for the whole application and a place in a
	// group kept for the whole application are not the tenant's to take.
	nothingToRemoveMessage = "This person holds nothing in this %s itself, so there is nothing to remove here."

	// removalConsequence is the warning's consequence for a person in none of the
	// tenant's groups, and removalWithGroupsConsequence for one in some.
	removalConsequence           = "%[1]s is removed from this %[2]s: every role they hold in it is taken away."
	removalWithGroupsConsequence = "%[1]s is removed from this %[2]s: every role they hold in it is taken away, and they are taken out of its groups."

	// removalConfirm is the words on the warning's button.
	removalConfirm = "Remove from this %s"

	// removalDeletesAccountNote is the warning's note where the account goes with the
	// removal.
	removalDeletesAccountNote = "They hold nothing anywhere else, so their account is deleted with its sign-in identities and sessions."

	// removalStaysNote is the warning's note where the person stays one of the tenant's
	// people another way.
	removalStaysNote = "Their account stays, and they remain one of this %s's people through a role held outside it, which this does not take away."

	// removalKeepsAccountNote is the warning's note where the account stays and the
	// person is no longer one of the tenant's people.
	removalKeepsAccountNote = "Their account stays, because they hold something outside this %s."
)

// TenantRemoval is the judgment of a person's removal from a tenant, as the warning before
// it reads it (JudgeTenantRemoval).
type TenantRemoval struct {
	Username       string   // the person's username
	Roles          []string // the roles held in the tenant itself, in words, sorted, each once
	InGroups       bool     // the person has a place in a group kept in the tenant
	DeletesAccount bool     // the account goes with the removal
	Stays          bool     // the person stays one of the tenant's people another way
}

// JudgeTenantRemoval judges the person's removal from the tenant as RemoveFromTenant judges
// it, refuses each case it refuses, and writes nothing: the warning before the act reads
// it, so the two cannot disagree. What the person holds is read as committed.
func (m *Manager) JudgeTenantRemoval(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID, domain accesstypes.Domain) (*TenantRemoval, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := m.refuseImpersonatorAsSubject(ctx, userID, removingYourselfAct(m.tenantWord)); err != nil {
		return nil, err
	}
	removal, _, err := m.judgeTenantRemoval(ctx, txn, userID, domain)
	if err != nil {
		return nil, err
	}

	return removal, nil
}

// RemoveFromTenant buffers the person's removal from the tenant, in the caller's
// transaction, and has the permission engine told once the transaction has committed: the
// person's next request is served without anything the tenant gave them. It answers
// whether the account went with it.
//
// Every row the person has in the tenant goes, ended or not: each membership of their own
// held in it and each place in a group kept in it, as when they leave it themselves
// (LeaveTenant). What reaches the tenant another way stays: a role held in every tenant, a
// role for the whole application and a place in a group kept for the whole application.
// The account goes when the person then holds nothing that has not ended, by the
// database's own clock for what has ended elsewhere, as for a leaving.
//
// Nobody removes their own account (they leave the tenant from their own account page),
// the account their session views as, or, in a session that acts as someone else, the
// real person behind it. Each membership's removal is held to the rule on a directory-only
// tenant's administrators (requireEnteringToChange), and each place's to what taking the
// person out of the group needs (refuseTakingPlace). The rule that keeps a tenant's last
// administrator is not asked: it holds a person's own leaving, and another person removes
// them. The person is told at the address they confirmed, read before anything goes,
// unless they stay one of the tenant's people another way.
//
// What the person holds is read as committed: remove one person from one tenant per
// transaction.
func (m *Manager) RemoveFromTenant(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID, domain accesstypes.Domain) (deleted bool, err error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := m.refuseImpersonatorAsSubject(ctx, userID, removingYourselfAct(m.tenantWord)); err != nil {
		return false, err
	}
	removal, judged, err := m.judgeTenantRemoval(ctx, txn, userID, domain)
	if err != nil {
		return false, err
	}
	if !removal.Stays {
		address, confirmed, err := m.store.ConfirmedEmail(ctx, txn, userID)
		if err != nil {
			return false, errors.Wrap(err, "identity.Store.ConfirmedEmail()")
		}
		if confirmed {
			if err := m.noticeAfterCommit(txn, "the notice of a removal from "+string(domain)+" to user "+userID.String(), func(ctx context.Context) error {
				return m.sessions.NoticeRemovedFromTenant(ctx, address, string(domain), judged.deletesAccount)
			}); err != nil {
				return false, err
			}
		}
	}
	if judged.deletesAccount {
		if err := m.deleteLeftWithNothing(ctx, txn, userID, "they were removed from the last "+m.tenantWord+" they were in"); err != nil {
			return false, err
		}
	}
	for _, memberships := range [][]access.MembershipKey{judged.memberships, judged.endedMemberships} {
		for i := range memberships {
			if err := m.store.DeleteMembership(ctx, txn, &memberships[i]); err != nil {
				return false, errors.Wrap(err, "identity.Store.DeleteMembership()")
			}
		}
	}
	for _, places := range [][]access.GroupMemberKey{judged.places, judged.endedPlaces} {
		for i := range places {
			if err := m.store.DeleteGroupMember(ctx, txn, &places[i]); err != nil {
				return false, errors.Wrap(err, "identity.Store.DeleteGroupMember()")
			}
		}
	}
	logger.FromCtx(ctx).Infof("identity: %s was removed from the %s %s by an administrator: %d role(s) of their own and %d place(s) in its groups are removed",
		User(userID), m.tenantWord, domain, len(judged.memberships), len(judged.places))
	if err := m.policyChangesAtCommit(txn); err != nil {
		return false, err
	}

	return judged.deletesAccount, nil
}

// RemovalWarning answers the warning before the person's removal from the tenant, from the
// judgment the removal runs (JudgeTenantRemoval): what the removal takes, the roles it
// takes by name, and what becomes of the account.
func (t *Tenant) RemovalWarning(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) (*resource.Warning, error) {
	if err := t.manager.refuseImpersonatorAsSubject(ctx, userID, removingYourselfAct(t.manager.tenantWord)); err != nil {
		return nil, err
	}
	removal, err := t.manager.JudgeTenantRemoval(ctx, txn, userID, t.domain)
	if err != nil {
		return nil, err
	}
	word := t.manager.tenantWord
	consequence := removalConsequence
	if removal.InGroups {
		consequence = removalWithGroupsConsequence
	}
	var names []string
	if len(removal.Roles) > 0 {
		names = removal.Roles
	}
	var note string
	switch {
	case removal.DeletesAccount:
		note = removalDeletesAccountNote
	case removal.Stays:
		note = fmt.Sprintf(removalStaysNote, word)
	default:
		note = fmt.Sprintf(removalKeepsAccountNote, word)
	}

	return &resource.Warning{
		Consequence: fmt.Sprintf(consequence, removal.Username, word),
		Names:       names,
		Confirm:     fmt.Sprintf(removalConfirm, word),
		Also:        []resource.WarningNote{{Says: note, Names: nil}},
	}, nil
}

// RemovePerson removes the person from the tenant (RemoveFromTenant).
func (t *Tenant) RemovePerson(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) error {
	if err := t.manager.refuseImpersonatorAsSubject(ctx, userID, removingYourselfAct(t.manager.tenantWord)); err != nil {
		return err
	}
	_, err := t.manager.RemoveFromTenant(ctx, txn, userID, t.domain)

	return err
}

// removingYourselfAct is a removal from a tenant as the rule on the person who
// impersonates names it (refuseImpersonatorAsSubject), which each exported method asks
// first, before the judgment.
func removingYourselfAct(tenantWord string) string {
	return "remove yourself from " + withArticle(tenantWord)
}

// judgeTenantRemoval is the judgment JudgeTenantRemoval answers and RemoveFromTenant acts
// on, each refusal ending it with nothing written: the removal as the warning reads it, and
// the rows that go (judgeLeaving, whose refusal is a person's own leaving's and is not
// read here). The real person behind a session that acts as someone else is refused
// before it, by the method that asks it.
func (m *Manager) judgeTenantRemoval(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID, domain accesstypes.Domain) (*TenantRemoval, *leaving, error) {
	if m.actorIs(ctx, userID) {
		if m.administrators == nil {
			return nil, nil, httpio.NewForbiddenMessagef(removingYourselfUnnamedMessage, m.tenantWord)
		}

		return nil, nil, httpio.NewForbiddenMessagef(removingYourselfMessage, m.tenantWord)
	}
	if m.callerIs(ctx, userID) {
		return nil, nil, httpio.NewForbiddenMessagef(removingViewedAsMessage, m.tenantWord)
	}
	if domain == "" {
		return nil, nil, httpio.NewBadRequestMessagef(noTenantToRemoveFromMessage, m.tenantWord)
	}
	username, found, err := m.store.Username(ctx, txn, userID)
	if err != nil {
		return nil, nil, errors.Wrap(err, "identity.Store.Username()")
	}
	if !found {
		return nil, nil, accountNotFound(userID)
	}
	holds, err := m.readTenantHolds(ctx, txn, userID)
	if err != nil {
		return nil, nil, err
	}
	held, err := m.tenantsHeld(ctx, txn, holds)
	if err != nil {
		return nil, nil, err
	}
	roles, in := held[domain]
	if !in {
		return nil, nil, httpio.NewNotFoundMessagef(nothingToRemoveMessage, m.tenantWord)
	}
	judged, err := m.judgeLeaving(ctx, txn, holds, domain)
	if err != nil {
		return nil, nil, err
	}
	inGroups, err := m.refuseRemovingFromTenant(ctx, txn, holds, userID, domain)
	if err != nil {
		return nil, nil, err
	}
	stays, err := m.staysInTenant(ctx, txn, holds, domain)
	if err != nil {
		return nil, nil, err
	}

	return &TenantRemoval{Username: username, Roles: roles, InGroups: inGroups, DeletesAccount: judged.deletesAccount, Stays: stays}, judged, nil
}

// refuseRemovingFromTenant holds what a removal takes in the tenant, of what has not
// ended, to the rules of taking each away: a membership of the person's own to the rule
// on a directory-only tenant's administrators, and a place in a group of the tenant to
// what taking the person out of the group needs. It answers whether the person has a
// place in a group of the tenant.
func (m *Manager) refuseRemovingFromTenant(ctx context.Context, txn resource.ReadWriteTransaction, holds *tenantHolds, userID impulse.UUID, domain accesstypes.Domain) (inGroups bool, err error) {
	for _, key := range holds.own {
		if !heldIn(key.Kind, key.Axis, key.Domain, domain) {
			continue
		}
		if err := m.requireEnteringToChange(ctx, accesstypes.DomainPolicyScope(domain), key.Role, nil); err != nil {
			return false, err
		}
	}
	for _, place := range holds.places {
		if !heldIn(place.Kind, place.Axis, place.Domain, domain) {
			continue
		}
		inGroups = true
		if err := m.refuseTakingPlace(ctx, txn, place.ID, userID); err != nil {
			return false, err
		}
	}

	return inGroups, nil
}

// staysInTenant reports whether the person stays one of the tenant's people once what
// they hold in the tenant itself is gone: among what they hold elsewhere that has not
// ended, a membership of their own held in every tenant at once, or a place in a group
// kept elsewhere that holds a role in every tenant at once or in the tenant itself.
func (m *Manager) staysInTenant(ctx context.Context, txn resource.ReadWriteTransaction, holds *tenantHolds, domain accesstypes.Domain) (bool, error) {
	for _, key := range holds.own {
		if !heldIn(key.Kind, key.Axis, key.Domain, domain) && heldInEveryTenant(key.Kind, key.Axis, key.Domain) {
			return true, nil
		}
	}
	for _, place := range holds.places {
		if heldIn(place.Kind, place.Axis, place.Domain, domain) {
			continue
		}
		holding, err := m.groupHolding(ctx, txn, place.ID, holds.now)
		if err != nil {
			return false, err
		}
		for _, role := range holding.roles {
			if heldInEveryTenant(role.Kind, role.Axis, role.Domain) || heldIn(role.Kind, role.Axis, role.Domain, domain) {
				return true, nil
			}
		}
	}

	return false, nil
}

// heldInEveryTenant reports whether a row's kind, axis and domain say it is held in every
// tenant at once, on the default axis, which reaches each tenant.
func heldInEveryTenant(kind, axis, domain string) bool {
	everywhere := access.NewMembershipKey(accesstypes.EveryDomainPolicyScope(), "", "")

	return axis == "" && kind == everywhere.Kind && domain == everywhere.Domain
}
