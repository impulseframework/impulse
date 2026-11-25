package identity

import (
	"context"
	"fmt"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/tracer"
)

// Taking one role away from a person. The administrator is told first, in a warning the
// act's own judgment answers: whose role goes, and where it is held.

// The sentences of a role's removal. %[1]s in the consequences is the person's username,
// %[2]s the role in words (roleInWords), %[3]s the application's word for a tenant
// (WithTenantWord) and %[4]s what the application calls the tenant
// (SessionAPI.TenantName).
const (
	// roleRemovalConsequence is the warning's consequence for a role held for the whole
	// application, which names no place.
	roleRemovalConsequence = "%[1]s loses the role %[2]s."

	// roleRemovalEveryConsequence is the warning's consequence for a role held in every
	// tenant.
	roleRemovalEveryConsequence = "%[1]s loses the role %[2]s in every %[3]s."

	// roleRemovalNamedConsequence is the warning's consequence for a role held in one
	// tenant the application names.
	roleRemovalNamedConsequence = "%[1]s loses the role %[2]s in the %[3]s %[4]s."

	// roleRemovalHereConsequence is the warning's consequence for a role held in one
	// tenant the application does not name, and for the tenant's own method.
	roleRemovalHereConsequence = "%[1]s loses the role %[2]s in this %[3]s."

	// roleRemovalConfirm is the words on the warning's button.
	roleRemovalConfirm = "Take the role away"

	// roleNotHeldMessage is said of a warning asked about a role the person no longer
	// holds: there is nothing to take away.
	roleNotHeldMessage = "This person no longer holds that role. Refresh the page to see what they hold."
)

// judgeRoleRemoval holds the removal of a membership the person holds to the rules of
// RemoveRole, as the removal and the warning before it read them. key is the membership,
// read as held by the caller. It writes nothing.
func (m *Manager) judgeRoleRemoval(ctx context.Context, txn resource.ReadWriteTransaction, scope accesstypes.PolicyScope, userID impulse.UUID, key *access.MembershipKey) error {
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
	// groups they are in, read from what has not ended (holds).
	all, err := m.holds(ctx, txn, userID)
	if err != nil {
		return err
	}

	return m.refuseLeavingSeveralTenants(all, key)
}

// RoleRemovalWarning answers the warning before the person's membership of the role in
// the scope is taken away (RemoveRole), from the judgment the removal runs: whose role
// goes, and where it is held. A tenant is named by what
// the application calls it (SessionAPI.TenantName), and as "this" tenant where it names
// none. The refusals are the removal's own, and a role the person no longer holds is not
// found: there is no consequence to say. It writes nothing.
func (m *Manager) RoleRemovalWarning(ctx context.Context, txn resource.ReadWriteTransaction, scope accesstypes.PolicyScope, userID impulse.UUID, role accesstypes.Role) (*resource.Warning, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := m.refuseImpersonatorAsSubject(ctx, userID, "take a role from yourself"); err != nil {
		return nil, err
	}
	username, found, err := m.store.Username(ctx, txn, userID)
	if err != nil {
		return nil, errors.Wrap(err, "identity.Store.Username()")
	}
	if !found {
		return nil, accountNotFound(userID)
	}
	key := access.NewMembershipKey(scope, User(userID), role)
	held, err := m.store.HasMembership(ctx, txn, &key)
	if err != nil {
		return nil, errors.Wrap(err, "identity.Store.HasMembership()")
	}
	if !held {
		return nil, roleNotHeld(username, scope, role)
	}
	if err := m.judgeRoleRemoval(ctx, txn, scope, userID, &key); err != nil {
		return nil, err
	}

	return roleRemovalWarning(m.roleRemovalConsequence(scope, username, role)), nil
}

// RoleRemovalWarning answers the warning before a role the person holds in the tenant is
// taken away (RemoveRole), read as the removal reads the role and from the judgment it
// runs: the role goes in "this" tenant. The refusals are the removal's own, and a role the person holds
// neither way is not found: there is no consequence to say. It writes nothing.
func (t *Tenant) RoleRemovalWarning(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID, role accesstypes.Role) (*resource.Warning, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if role == "" {
		return nil, httpio.NewBadRequestMessage(noRoleNamedMessage)
	}
	if err := t.manager.refuseImpersonatorAsSubject(ctx, userID, "take a role from yourself"); err != nil {
		return nil, err
	}
	username, key, err := t.heldRole(ctx, txn, userID, role)
	if err != nil {
		return nil, err
	}
	if key == nil {
		return nil, roleNotHeld(username, t.scope(), role)
	}
	if err := t.manager.judgeRoleRemoval(ctx, txn, t.scope(), userID, key); err != nil {
		return nil, err
	}
	consequence := fmt.Sprintf(roleRemovalHereConsequence, username, roleInWords(role), t.manager.tenantWord)

	return roleRemovalWarning(consequence), nil
}

// roleRemovalConsequence is the warning's consequence of taking the role from the person
// in the scope: no place for the whole application, every tenant, or the one tenant by
// what the application calls it and otherwise as "this" tenant.
func (m *Manager) roleRemovalConsequence(scope accesstypes.PolicyScope, username string, role accesstypes.Role) string {
	word := m.tenantWord
	switch {
	case scope.IsGlobal():
		return fmt.Sprintf(roleRemovalConsequence, username, roleInWords(role))
	case scope.IsEveryDomain():
		return fmt.Sprintf(roleRemovalEveryConsequence, username, roleInWords(role), word)
	}
	domain, _ := scope.Domain()
	if name := m.sessions.TenantName(domain); name != "" {
		return fmt.Sprintf(roleRemovalNamedConsequence, username, roleInWords(role), word, name)
	}

	return fmt.Sprintf(roleRemovalHereConsequence, username, roleInWords(role), word)
}

// roleRemovalWarning is the warning of a role's removal with the consequence.
func roleRemovalWarning(consequence string) *resource.Warning {
	return &resource.Warning{Consequence: consequence, Names: nil, Confirm: roleRemovalConfirm, Also: nil}
}

// roleNotHeld is the answer to a warning asked about a role the person does not hold in
// the scope: 404 with roleNotHeldMessage. The person, the role and the scope stay in the
// cause, for the server's log.
func roleNotHeld(username string, scope accesstypes.PolicyScope, role accesstypes.Role) error {
	return httpio.NewNotFoundMessageWithError(errors.Newf("%s does not hold the role %s in %s", username, role, scope), roleNotHeldMessage)
}
