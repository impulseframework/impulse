// holdings.go is what the user manager says of what is held and why: the reason a
// person's role carries, and the one list of a person's roles with where each comes
// from. The store keeps the rows; everything here is read from them, and nothing here
// is policy: the permission engine reads none of it.

package access

import (
	"context"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/tracer"
)

// MaxReason is the longest reason a person's role carries, in characters: one line a
// list shows beside the role.
const MaxReason = 500

// ValidateReason holds the reason a person's role is given for to its shape and answers
// it as it is kept: one line of plain text of at most MaxReason characters, less the
// space around it. No reason is the empty text, and so is one of spaces alone. A line
// break, and any other character that cannot be shown, is refused: the reason is read in
// a list, beside the role and when it ends. It is the one statement of the rule, asked by
// every write of a person's role here and by a caller that writes the row in a
// transaction of its own (UserManager.Membership).
func ValidateReason(reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	switch {
	case utf8.RuneCountInString(reason) > MaxReason:
		return "", httpio.NewBadRequestMessagef("the reason is longer than %d characters: say it in one line", MaxReason)
	case strings.ContainsFunc(reason, unicode.IsControl):
		return "", httpio.NewBadRequestMessage("the reason is one line of plain text, and this one holds a line break or a character that cannot be shown: type it again as one line")
	}

	return reason, nil
}

// HeldRole is one role a person holds, as UserManager.UserRolesHeld lists it, with where
// it comes from: a membership of the person's own (Group is nil), with the period it
// counts in and the reason it was given for, or a group the person is in that holds the
// role (Group names it). A role of the person's own is taken away from the person; one
// that comes through a group goes when the person leaves the group or the group loses
// the role.
type HeldRole struct {
	Scope accesstypes.PolicyScope
	Role  accesstypes.Role
	// Group is the group the role comes through, nil for a role of the person's own.
	Group *GroupInfo
	// Period and Reason are the membership's own: the zero Period and no reason for a
	// role that comes through a group.
	Period accesstypes.Period
	Reason string
}

// holdingStore is the management surface's part for what an assignment carries beside
// its period: the reason of a person's role. It stands beside policyStore, and one store
// manager implements both.
type holdingStore interface {
	// assignUserRole gives the membership its period and its reason, replacing both on
	// one that is there.
	assignUserRole(ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User, role accesstypes.Role, period accesstypes.Period, reason string) error
	// userAssignments lists every membership of the user's own that has not ended, each
	// with its period and its reason.
	userAssignments(ctx context.Context, user accesstypes.User) ([]policy.Assignment, error)
}

// managementStore is everything the user manager asks of the store: the policy rows and
// what is said of them.
type managementStore interface {
	policyStore
	holdingStore
}

var _ managementStore = (*storeManager)(nil)

func (m *storeManager) assignUserRole(ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User, role accesstypes.Role, period accesstypes.Period, reason string) error {
	if err := m.store.InsertUserRole(ctx, scope, user, role, period, reason); err != nil {
		return errors.Wrapf(err, "access.Store.InsertUserRole(): role %q to %q", role, user)
	}
	m.notifyPolicyChange()

	return nil
}

func (m *storeManager) userAssignments(ctx context.Context, user accesstypes.User) ([]policy.Assignment, error) {
	assignments, err := m.store.ListUserAssignments(ctx, user)
	if err != nil {
		return nil, errors.Wrapf(err, "access.Store.ListUserAssignments(): user %q", user)
	}

	return assignments, nil
}

// UserRolesHeld returns the roles user holds as one list, each with where it comes
// from, sorted by where it is held, then by role, a role of the person's own before the
// same role through a group, and groups by name. A role held both ways is listed once
// for each way.
//
// The roles of the person's own are the memberships that have not ended, each with its
// period and its reason: one whose period has not begun is listed, as the person will
// hold it. The roles that come through a group are the ones UserRoles counts: held now
// by a group the person is in now, and given by it to its members (never the Super
// Administrator role, and for a group kept in one tenant the roles held in that tenant
// alone).
func (u *userManager) UserRolesHeld(ctx context.Context, user accesstypes.User) ([]HeldRole, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	own, err := u.store.userAssignments(ctx, user)
	if err != nil {
		return nil, err
	}
	held := make([]HeldRole, 0, len(own))
	for i := range own {
		held = append(held, HeldRole{Scope: own[i].Scope, Role: own[i].Role, Group: nil, Period: own[i].Period, Reason: own[i].Reason})
	}
	through, err := u.store.userGroupRoles(ctx, user)
	if err != nil {
		return nil, err
	}
	groups, err := u.store.userGroups(ctx, user)
	if err != nil {
		return nil, err
	}
	named := make(map[accesstypes.Group]GroupInfo, len(groups))
	for _, group := range groupInfos(groups) {
		named[group.ID] = group
	}
	for i := range through {
		if groupRoleRefusal(through[i].Group.Scope, through[i].Scope, through[i].Role) != "" {
			continue
		}
		group, listed := named[through[i].Group.ID]
		if !listed {
			// The person left the group between the two reads: the role is not theirs.
			continue
		}
		held = append(held, HeldRole{Scope: through[i].Scope, Role: through[i].Role, Group: &group, Period: accesstypes.Period{}, Reason: ""})
	}
	slices.SortStableFunc(held, func(a, b HeldRole) int {
		return compareHeldRoles(&a, &b)
	})

	return held, nil
}

// compareHeldRoles orders a person's roles by where each is held, then by role, a role
// of the person's own first and the groups a role comes through by name then id.
func compareHeldRoles(a, b *HeldRole) int {
	if c := policy.CompareScopes(a.Scope, b.Scope); c != 0 {
		return c
	}
	if c := strings.Compare(string(a.Role), string(b.Role)); c != 0 {
		return c
	}
	switch {
	case a.Group == nil && b.Group == nil:
		return 0
	case a.Group == nil:
		return -1
	case b.Group == nil:
		return 1
	}
	if c := strings.Compare(a.Group.Name, b.Group.Name); c != 0 {
		return c
	}

	return strings.Compare(string(a.Group.ID), string(b.Group.ID))
}
