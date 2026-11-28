package identity

import (
	"context"
	"fmt"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/tracer"
)

// Taking a person out of a group. The administrator is told first, in a warning the act's
// own judgment answers: who leaves which group.

// The sentences of a person's removal from a group. %[1]s in the consequences is the
// person's username and %[2]s the group's name (Group.Name).
const (
	// groupMemberRemovalConsequence is the warning's consequence.
	groupMemberRemovalConsequence = "%[1]s is removed from the group %[2]s. They keep the roles they hold themselves and lose the group's."

	// groupMemberRemovalConfirm is the words on the warning's button.
	groupMemberRemovalConfirm = "Remove from the group"

	// notInGroupMessage is said of a warning asked about a person who is no longer in the
	// group: there is nobody to take out.
	notInGroupMessage = "This person is no longer in that group. Refresh the page to see who is in it."
)

// GroupMemberRemovalWarning answers the warning before the person is taken out of the
// group (RemoveGroupMember), from the judgment the removal runs: who leaves which group.
// The refusals are the removal's own (refuseTakingPlace), and a person who is not in the
// group is not found: there is no consequence to say. It writes nothing.
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
	if err := m.refuseTakingPlace(ctx, txn, groupID, userID); err != nil {
		return nil, err
	}

	return groupMemberRemovalWarning(username, group), nil
}

// GroupMemberRemovalWarning answers the warning before the person is taken out of a group
// of the tenant (see Manager.GroupMemberRemovalWarning). A group kept anywhere else is not
// found in the tenant, as the removal finds it. It writes nothing.
func (t *Tenant) GroupMemberRemovalWarning(ctx context.Context, txn resource.ReadWriteTransaction, groupID, userID impulse.UUID) (*resource.Warning, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if _, err := t.requireTenantGroup(ctx, txn, groupID); err != nil {
		return nil, err
	}

	return t.manager.GroupMemberRemovalWarning(ctx, txn, groupID, userID)
}

// groupMemberRemovalWarning is the warning of the person's removal from the group.
func groupMemberRemovalWarning(username string, group *Group) *resource.Warning {
	return &resource.Warning{
		Consequence: fmt.Sprintf(groupMemberRemovalConsequence, username, group.Name),
		Names:       nil,
		Confirm:     groupMemberRemovalConfirm,
		Also:        nil,
	}
}

// notInGroup is the answer to a warning asked about a person who is not in the group:
// 404 with notInGroupMessage. The person and the group stay in the cause, for the
// server's log.
func notInGroup(username string, group *Group) error {
	return httpio.NewNotFoundMessageWithError(errors.Newf("%s is not in the group %s", username, group.Name), notInGroupMessage)
}
