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
// (LeaveTenant). Where that was the last thing the person held, the account stays, holding
// nothing. The administrator is told first, in a warning the act's own judgment answers,
// which roles go.

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

	// removalStaysNote is the warning's note where the person stays one of the tenant's
	// people another way.
	removalStaysNote = "They remain one of this %s's people through a role held outside it, which this does not take away."
)

// TenantRemoval is the judgment of a person's removal from a tenant, as the warning before
// it reads it (JudgeTenantRemoval).
type TenantRemoval struct {
	Username string   // the person's username
	Roles    []string // the roles held in the tenant itself, in words, sorted, each once
	InGroups bool     // the person has a place in a group kept in the tenant
	Stays    bool     // the person is one of the tenant's people another way now: by something whose period has begun and has not ended
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
// person's next request is served without anything the tenant gave them.
//
// Every row the person has in the tenant goes, ended or not: each membership of their own
// held in it and each place in a group kept in it, as when they leave it themselves
// (LeaveTenant). Each role of theirs on one of the tenant's records that has not ended
// goes too, under the removal's own grant: no grant is asked for each. What reaches the
// tenant another way stays: a role held in every tenant, a role for the whole application
// and a place in a group kept for the whole application. Where the removal takes the last
// thing the person holds, the account stays, holding nothing.
//
// Nobody removes their own account (they leave the tenant from their own account page),
// the account their session views as, or, in a session that acts as someone else, the
// real person behind it. The removal gives nobody a power that taking each role away
// directly would not: each membership that has not ended is held to the rule on a
// directory-only tenant's administrators (requireEnteringToChange) and to the grant of the
// method that takes one role away, asked for that role (RoleGrants.MayTake), and each
// place to what the role grants ask of taking the person out of the group
// (refuseTakingPlace). The grant of the group's own method is not asked: the removal's
// own grant says who takes a person out of the tenant, their places in its groups with
// them. The rule that keeps a tenant's last administrator is not asked: it holds a
// person's own leaving, and another person removes them.
//
// What the person holds is read as committed: remove one person from one tenant per
// transaction.
func (m *Manager) RemoveFromTenant(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID, domain accesstypes.Domain) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := m.refuseImpersonatorAsSubject(ctx, userID, removingYourselfAct(m.tenantWord)); err != nil {
		return err
	}
	_, judged, err := m.judgeTenantRemoval(ctx, txn, userID, domain)
	if err != nil {
		return err
	}
	if err := m.applyLeaving(ctx, txn, userID, domain, judged); err != nil {
		return err
	}
	logger.FromCtx(ctx).Infof("identity: %s was removed from the %s %s by an administrator: %d role(s) of their own, %d on its records and %d place(s) in its groups are removed",
		User(userID), m.tenantWord, domain, len(judged.memberships), len(judged.records), len(judged.places))

	return m.policyChangesAtCommit(txn)
}

// RemovalWarning answers the warning before the person's removal from the tenant, from the
// judgment the removal runs (JudgeTenantRemoval): what the removal takes, the roles it
// takes by name, and, where the person stays one of the tenant's people another way, a
// note that says so.
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
	var also []resource.WarningNote
	if removal.Stays {
		also = []resource.WarningNote{{Says: fmt.Sprintf(removalStaysNote, word), Names: nil}}
	}

	return &resource.Warning{
		Consequence: fmt.Sprintf(consequence, removal.Username, word),
		Names:       names,
		Confirm:     fmt.Sprintf(removalConfirm, word),
		Also:        also,
	}, nil
}

// RemovePerson removes the person from the tenant (RemoveFromTenant).
func (t *Tenant) RemovePerson(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) error {
	if err := t.manager.refuseImpersonatorAsSubject(ctx, userID, removingYourselfAct(t.manager.tenantWord)); err != nil {
		return err
	}

	return t.manager.RemoveFromTenant(ctx, txn, userID, t.domain)
}

// removingYourselfAct is a removal from a tenant as the rule on the person who
// impersonates names it (refuseImpersonatorAsSubject), which each exported method asks
// first, before the judgment.
func removingYourselfAct(tenantWord string) string {
	return "remove yourself from " + withArticle(tenantWord)
}

// judgeTenantRemoval is the judgment JudgeTenantRemoval answers and RemoveFromTenant acts
// on, each refusal ending it with nothing written: the removal as the warning reads it, and
// the rows that go (judgeLeaving, whose refusal is a person's own leaving's and is not read
// here). The real person behind a session that acts as someone else is refused before it,
// by the method that asks it.
func (m *Manager) judgeTenantRemoval(
	ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID, domain accesstypes.Domain,
) (removal *TenantRemoval, judged *leaving, err error) {
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
	judged, err = m.judgeLeaving(ctx, txn, holds, domain)
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

	return &TenantRemoval{Username: username, Roles: roles, InGroups: inGroups, Stays: stays}, judged, nil
}

// refuseRemovingFromTenant holds what a removal takes in the tenant, of what has not
// ended, to the rules of taking each away: a membership of the person's own to the rule
// on a directory-only tenant's administrators and to the grant of the method that takes
// one role away (RoleGrants.MayTake), and a place in a group of the tenant to what the
// role grants ask of taking the person out of the group (refuseTakingPlace). The grant of
// the group's own method is not asked: the removal's own grant says who takes a person
// out of the tenant, their places in its groups with them. The grants are asked only of
// a request: a call outside any request is the application's own act. It answers
// whether the person has a place in a group of the tenant.
func (m *Manager) refuseRemovingFromTenant(ctx context.Context, txn resource.ReadWriteTransaction, holds *tenantHolds, userID impulse.UUID, domain accesstypes.Domain) (inGroups bool, err error) {
	requested := !resource.CallerFrom(ctx).IsZero()
	if requested && m.grants == nil {
		return false, errors.New("a request removes a person from a tenant and the manager holds no role grants to judge it by: build it with identity.WithRoleGrants, as the generated client does")
	}
	scope := accesstypes.DomainPolicyScope(domain)
	act := "removing this person from the " + m.tenantWord + " takes away"
	for _, key := range holds.own {
		if !heldIn(key.Kind, key.Axis, key.Domain, domain) {
			continue
		}
		if err := m.requireEnteringToChange(ctx, scope, key.Role, nil); err != nil {
			return false, err
		}
		if !requested {
			continue
		}
		allowed, err := m.grants.MayTake(ctx, txn, scope, userID, key.Role)
		if err != nil {
			return false, errors.Wrap(err, "identity.RoleGrants")
		}
		if !allowed {
			return false, httpio.NewForbiddenMessageWithError(
				errors.Newf("%s the role %s, which you may not take from them: someone who may take the role away does it", act, string(key.Role)),
				sentenceStart(act)+" the role "+roleInWords(key.Role)+", which you may not take from them.",
			)
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
// they hold in the tenant itself is gone: among what they hold elsewhere whose period has
// begun and has not ended, a membership of their own held in every tenant at once, or a
// place in a group kept elsewhere that holds a role in every tenant at once or in the
// tenant itself.
func (m *Manager) staysInTenant(ctx context.Context, txn resource.ReadWriteTransaction, holds *tenantHolds, domain accesstypes.Domain) (bool, error) {
	for _, key := range holds.own {
		if !heldIn(key.Kind, key.Axis, key.Domain, domain) && heldInEveryTenant(key.Kind, key.Axis, key.Domain) &&
			holds.rows.periods.Memberships[key].Contains(holds.now) {
			return true, nil
		}
	}
	for _, place := range holds.places {
		if heldIn(place.Kind, place.Axis, place.Domain, domain) {
			continue
		}
		if !holds.rows.periods.Places[access.GroupMemberKey{Group: GroupID(place.ID), User: User(holds.rows.userID)}].Contains(holds.now) {
			continue
		}
		holding, err := m.groupHolding(ctx, txn, place.ID, holds.now)
		if err != nil {
			return false, err
		}
		for _, role := range holding.roles {
			if !holding.periods.Roles[role].Contains(holds.now) {
				continue
			}
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
