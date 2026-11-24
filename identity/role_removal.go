package identity

import (
	"context"
	"fmt"
	"slices"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/tracer"
)

// Taking one role away from a person. The administrator is told first, in a warning the
// act's own judgment answers: whose role goes, where it is held, and whether the account
// goes with it, as it does when the role is the last thing the person holds.

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

	// roleRemovalConfirm is the words on the warning's button, and
	// roleRemovalDeletesAccountConfirm the words where the account goes with the role.
	roleRemovalConfirm               = "Take the role away"
	roleRemovalDeletesAccountConfirm = "Take the role away and delete the account"

	// deletesAccountNote is the warning's note where the account goes with the role, and
	// with a place in a group (GroupMemberRemovalWarning).
	deletesAccountNote = "They hold nothing else, so their account is deleted with its sign-in identities and sessions."

	// managedElsewhereNote is the note of the three warnings (a removal from a tenant, a
	// role's and a place's) where the person then holds nothing and the account stays,
	// because another tenant manages it (managedElsewhere). %s is the application's word
	// for a tenant.
	managedElsewhereNote = "They then hold nothing, but their account stays: the %s whose directory they sign in through manages it."

	// roleNotHeldMessage is said of a warning asked about a role the person no longer
	// holds: there is nothing to take away.
	roleNotHeldMessage = "This person no longer holds that role. Refresh the page to see what they hold."
)

// roleRemoval is the judgment of taking a membership away, as the removal and the
// warning before it read it (judgeRoleRemoval).
type roleRemoval struct {
	// ended is whether the membership's period is over.
	ended bool
	// kept is what keeps the account, the membership among it: every row that stands in
	// the tables, ended or not (keeps).
	kept []access.MembershipKey
	// last is whether the membership is the last thing kept.
	last bool
	// deletes is whether the account goes with the membership: it is the last thing kept,
	// and no tenant other than the membership's manages the account (managedElsewhere).
	// managedBy is the tenant that manages it where the account stays for that.
	deletes   bool
	managedBy accesstypes.Domain
}

// judgeRoleRemoval holds the removal of a membership the person holds to the rules of
// RemoveRole, and answers what it does: whether the membership has ended, what keeps the
// account, and whether the membership is the last of it. key is the membership, read as
// held by the caller. It writes nothing.
func (m *Manager) judgeRoleRemoval(ctx context.Context, txn resource.ReadWriteTransaction, scope accesstypes.PolicyScope, userID impulse.UUID, key *access.MembershipKey) (*roleRemoval, error) {
	// A membership whose period is over makes nobody a tenant's administrator, and its
	// removal is not the directory-only tenant's to hold.
	ended, err := m.membershipEnded(ctx, txn, userID, key)
	if err != nil {
		return nil, err
	}
	if !ended {
		if err := m.requireEnteringToChange(ctx, scope, key.Role, nil); err != nil {
			return nil, err
		}
	}
	switch {
	case scope.IsGlobal() && key.Role == accesstypes.SuperAdministrator:
		if err := m.requireSuperAdministrator(ctx, txn, "takes the Super administrator role away"); err != nil {
			return nil, err
		}
		if err := m.refuseLastSuperAdministrator(ctx, txn, userID, "take the role from them"); err != nil {
			return nil, err
		}
	case !ended && m.callerIs(ctx, userID):
		if err := m.refuseLastAdministrator(ctx, txn, scope, ownRoleGone(key)); err != nil {
			return nil, err
		}
	}
	// What the person holds beside the membership: the roles of their own and the
	// groups they are in. Where they belong is read from what has not ended (holds);
	// the account is kept by every row that stands in the tables, ended or not (keeps).
	all, err := m.holds(ctx, txn, userID)
	if err != nil {
		return nil, err
	}
	if err := m.refuseLeavingSeveralTenants(all, key); err != nil {
		return nil, err
	}
	kept, err := m.keeps(ctx, txn, userID)
	if err != nil {
		return nil, err
	}
	last := !slices.ContainsFunc(kept, func(k access.MembershipKey) bool { return k != *key })
	var managing accesstypes.Domain
	var elsewhere bool
	if last {
		// The membership's tenant, and none for a role held for the whole application or
		// in every tenant.
		var tenant accesstypes.Domain
		if _, held := tenantScope(key); held {
			tenant = accesstypes.Domain(key.Domain)
		}
		if managing, elsewhere, err = m.managedElsewhere(ctx, txn, userID, tenant); err != nil {
			return nil, err
		}
	}

	return &roleRemoval{ended: ended, kept: kept, last: last, deletes: last && !elsewhere, managedBy: managing}, nil
}

// RoleRemovalWarning answers the warning before the person's membership of the role in
// the scope is taken away (RemoveRole), from the judgment the removal runs: whose role
// goes, where it is held, and whether the account goes with it. A tenant is named by what
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
	removal, err := m.judgeRoleRemoval(ctx, txn, scope, userID, &key)
	if err != nil {
		return nil, err
	}

	return roleRemovalWarning(m.roleRemovalConsequence(scope, username, role), removal, m.tenantWord), nil
}

// RoleRemovalWarning answers the warning before a role the person holds in the tenant is
// taken away (RemoveRole), read as the removal reads the role and from the judgment it
// runs: the role goes in "this" tenant, and the account with it where it is the last
// thing the person holds. The refusals are the removal's own, and a role the person holds
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
	removal, err := t.manager.judgeRoleRemoval(ctx, txn, t.scope(), userID, key)
	if err != nil {
		return nil, err
	}
	consequence := fmt.Sprintf(roleRemovalHereConsequence, username, roleInWords(role), t.manager.tenantWord)

	return roleRemovalWarning(consequence, removal, t.manager.tenantWord), nil
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

// roleRemovalWarning is the warning of a role's removal with the consequence: the button
// and a note say the account goes where it goes with the membership, and a note says it
// stays where the membership is the last thing kept and another tenant manages the
// account. word is the application's word for a tenant.
func roleRemovalWarning(consequence string, removal *roleRemoval, word string) *resource.Warning {
	if !removal.last {
		return &resource.Warning{Consequence: consequence, Names: nil, Confirm: roleRemovalConfirm, Also: nil}
	}
	if !removal.deletes {
		return &resource.Warning{
			Consequence: consequence,
			Names:       nil,
			Confirm:     roleRemovalConfirm,
			Also:        []resource.WarningNote{{Says: fmt.Sprintf(managedElsewhereNote, word), Names: nil}},
		}
	}

	return &resource.Warning{
		Consequence: consequence,
		Names:       nil,
		Confirm:     roleRemovalDeletesAccountConfirm,
		Also:        []resource.WarningNote{{Says: deletesAccountNote, Names: nil}},
	}
}

// roleNotHeld is the answer to a warning asked about a role the person does not hold in
// the scope: 404 with roleNotHeldMessage. The person, the role and the scope stay in the
// cause, for the server's log.
func roleNotHeld(username string, scope accesstypes.PolicyScope, role accesstypes.Role) error {
	return httpio.NewNotFoundMessageWithError(errors.Newf("%s does not hold the role %s in %s", username, role, scope), roleNotHeldMessage)
}
