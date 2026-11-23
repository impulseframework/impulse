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

// Taking a person out of a group. The administrator is told first, in a warning the act's
// own judgment answers: who leaves which group, and whether the account goes with it, as
// it does when the place is the last thing the person holds.

// The sentences of a person's removal from a group. %[1]s in the consequences is the
// person's username and %[2]s the group's name (Group.Name).
const (
	// groupMemberRemovalConsequence is the warning's consequence where the person keeps
	// their account.
	groupMemberRemovalConsequence = "%[1]s is removed from the group %[2]s. They keep the roles they hold themselves and lose the group's."

	// groupMemberRemovalLastConsequence is the warning's consequence where the place is the
	// last thing the person holds, beside the note that the account goes with it.
	groupMemberRemovalLastConsequence = "%[1]s is removed from the group %[2]s."

	// groupMemberRemovalConfirm is the words on the warning's button, and
	// groupMemberRemovalDeletesAccountConfirm the words where the account goes with the
	// place.
	groupMemberRemovalConfirm               = "Remove from the group"
	groupMemberRemovalDeletesAccountConfirm = "Remove from the group and delete the account"

	// notInGroupMessage is said of a warning asked about a person who is no longer in the
	// group: there is nobody to take out.
	notInGroupMessage = "This person is no longer in that group. Refresh the page to see who is in it."
)

// placeRemoval is the judgment of taking a person out of a group, as the removal and the
// warning before it read it (judgeGroupMemberRemoval).
type placeRemoval struct {
	// last is whether the place is the last thing that keeps the account.
	last bool
	// deletes is whether the account goes with the place: it is the last thing kept, and
	// no tenant other than the one the group is kept in manages the account
	// (managedElsewhere). managedBy is the tenant that manages it where the account stays
	// for that.
	deletes   bool
	managedBy accesstypes.Domain
}

// judgeGroupMemberRemoval holds the person's removal from the group to the rules of
// RemoveGroupMember (refuseTakingPlace), and answers whether the place is the last thing
// that keeps the account (the person holds no role of their own and is in no other
// group), and whether the account goes with it: no tenant other than the one the group is
// kept in, and none for a group kept for the whole application, manages the account. It
// writes nothing.
func (m *Manager) judgeGroupMemberRemoval(ctx context.Context, txn resource.ReadWriteTransaction, groupID, userID impulse.UUID) (*placeRemoval, error) {
	if err := m.refuseTakingPlace(ctx, txn, groupID, userID); err != nil {
		return nil, err
	}
	own, err := m.store.Memberships(ctx, txn, userID)
	if err != nil {
		return nil, errors.Wrap(err, "identity.Store.Memberships()")
	}
	places, err := m.store.UserGroups(ctx, txn, userID)
	if err != nil {
		return nil, errors.Wrap(err, "identity.Store.UserGroups()")
	}
	elsewhere := slices.ContainsFunc(places, func(place GroupPlace) bool { return place.ID != groupID })
	last := len(own) == 0 && !elsewhere
	var managing accesstypes.Domain
	var managed bool
	if last {
		var tenant accesstypes.Domain
		if i := slices.IndexFunc(places, func(place GroupPlace) bool { return place.ID == groupID }); i >= 0 {
			tenant, _ = tenantOf(places[i].Kind, places[i].Axis, places[i].Domain)
		}
		if managing, managed, err = m.managedElsewhere(ctx, txn, userID, tenant); err != nil {
			return nil, err
		}
	}

	return &placeRemoval{last: last, deletes: last && !managed, managedBy: managing}, nil
}

// GroupMemberRemovalWarning answers the warning before the person is taken out of the
// group (RemoveGroupMember), from the judgment the removal runs: who leaves which group,
// and whether the account goes with it. The refusals are the removal's own, and a person
// who is not in the group is not found: there is no consequence to say. It writes
// nothing.
func (m *Manager) GroupMemberRemovalWarning(ctx context.Context, txn resource.ReadWriteTransaction, groupID, userID impulse.UUID) (*resource.Warning, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := m.refuseImpersonatorAsSubject(ctx, userID, "take yourself out of a group"); err != nil {
		return nil, err
	}
	username, found, err := m.store.Username(ctx, txn, userID)
	if err != nil {
		return nil, errors.Wrap(err, "identity.Store.Username()")
	}
	if !found {
		return nil, accountNotFound(userID)
	}
	group, err := m.requireGroup(ctx, txn, groupID)
	if err != nil {
		return nil, err
	}
	key := access.GroupMemberKey{Group: GroupID(groupID), User: User(userID)}
	in, err := m.store.HasGroupMember(ctx, txn, &key)
	if err != nil {
		return nil, errors.Wrap(err, "identity.Store.HasGroupMember()")
	}
	if !in {
		return nil, notInGroup(username, group)
	}
	removal, err := m.judgeGroupMemberRemoval(ctx, txn, groupID, userID)
	if err != nil {
		return nil, err
	}

	return groupMemberRemovalWarning(username, group, removal, m.tenantWord), nil
}

// GroupMemberRemovalWarning answers the warning before the person is taken out of a group
// of the tenant (see Manager.GroupMemberRemovalWarning). A group kept anywhere else is not
// found in the tenant, as the removal finds it. It writes nothing.
func (t *Tenant) GroupMemberRemovalWarning(ctx context.Context, txn resource.ReadWriteTransaction, groupID, userID impulse.UUID) (*resource.Warning, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := t.requireTenantGroup(ctx, txn, groupID); err != nil {
		return nil, err
	}

	return t.manager.GroupMemberRemovalWarning(ctx, txn, groupID, userID)
}

// groupMemberRemovalWarning is the warning of the person's removal from the group: where
// the place is the last thing kept, the consequence leaves out the roles the person
// keeps, as they keep none, and the button and a note say the account goes, or a note
// says it stays where another tenant manages it. word is the application's word for a
// tenant.
func groupMemberRemovalWarning(username string, group *Group, removal *placeRemoval, word string) *resource.Warning {
	if !removal.last {
		return &resource.Warning{
			Consequence: fmt.Sprintf(groupMemberRemovalConsequence, username, group.Name),
			Names:       nil,
			Confirm:     groupMemberRemovalConfirm,
			Also:        nil,
		}
	}

	if !removal.deletes {
		return &resource.Warning{
			Consequence: fmt.Sprintf(groupMemberRemovalLastConsequence, username, group.Name),
			Names:       nil,
			Confirm:     groupMemberRemovalConfirm,
			Also:        []resource.WarningNote{{Says: fmt.Sprintf(managedElsewhereNote, word), Names: nil}},
		}
	}

	return &resource.Warning{
		Consequence: fmt.Sprintf(groupMemberRemovalLastConsequence, username, group.Name),
		Names:       nil,
		Confirm:     groupMemberRemovalDeletesAccountConfirm,
		Also:        []resource.WarningNote{{Says: deletesAccountNote, Names: nil}},
	}
}

// notInGroup is the answer to a warning asked about a person who is not in the group:
// 404 with notInGroupMessage. The person and the group stay in the cause, for the
// server's log.
func notInGroup(username string, group *Group) error {
	return httpio.NewNotFoundMessageWithError(errors.Newf("%s is not in the group %s", username, group.Name), notInGroupMessage)
}
