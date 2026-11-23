// holdings.go is what the user manager says of what is held and why: the reason a
// person's role carries, the one list of a person's roles with where each comes from,
// what each group of a place holds, and who loses a role a group stops holding. The
// store keeps the rows; everything here is read from them, and nothing here is policy:
// the permission engine reads none of it.

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
		return "", httpio.NewBadRequestMessagef("The reason is longer than %d characters. Say it in one line.", MaxReason)
	case strings.ContainsFunc(reason, unicode.IsControl):
		return "", httpio.NewBadRequestMessage("The reason is one line of plain text, and this one holds a line break or a character that cannot be shown. Type it again as one line.")
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

// GroupContent is one group of people as UserManager.GroupContents lists it, with what
// it holds: how many people are in it and the roles it holds, each with where it is
// held. Both are what has not ended, an assignment whose period has not begun among it,
// so a group that holds nothing by this answer is one DeleteGroup removes.
type GroupContent struct {
	GroupInfo

	People int
	Roles  []GroupHolding
}

// GroupHolding is one role a group holds: where it is held and the period the group
// holds it for, the zero Period for none.
type GroupHolding struct {
	Scope  accesstypes.PolicyScope
	Role   accesstypes.Role
	Period accesstypes.Period
}

// holdingStore is the management surface's part for what an assignment carries beside
// its period and for the listings of what is held: the reason of a person's role, and
// what the groups of a place hold. It stands beside policyStore, and one store manager
// implements both.
type holdingStore interface {
	// assignUserRole gives the membership its period and its reason, replacing both on
	// one that is there.
	assignUserRole(ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User, role accesstypes.Role, period accesstypes.Period, reason string) error
	// assignCustomUserRole writes what assignUserRole writes, only while a row of the
	// custom role is held where the membership reads it, and wraps policy.ErrNoRole
	// where none is.
	assignCustomUserRole(ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User, role accesstypes.Role, period accesstypes.Period, reason string) error
	// userAssignments lists every membership of the user's own that has not ended, each
	// with its period and its reason.
	userAssignments(ctx context.Context, user accesstypes.User) ([]policy.Assignment, error)
	// groupMemberCounts and groupRolesIn say what the groups kept in scope hold that has
	// not ended: how many people each has, and the roles each holds.
	groupMemberCounts(ctx context.Context, scope accesstypes.PolicyScope) (map[accesstypes.Group]int, error)
	groupRolesIn(ctx context.Context, scope accesstypes.PolicyScope) ([]policy.Membership, error)
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

func (m *storeManager) assignCustomUserRole(ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User, role accesstypes.Role, period accesstypes.Period, reason string) error {
	if err := m.store.InsertCustomUserRole(ctx, scope, user, role, period, reason); err != nil {
		return errors.Wrapf(err, "access.Store.InsertCustomUserRole(): role %q to %q", role, user)
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

func (m *storeManager) groupMemberCounts(ctx context.Context, scope accesstypes.PolicyScope) (map[accesstypes.Group]int, error) {
	counts, err := m.store.CountGroupMembersIn(ctx, scope)
	if err != nil {
		return nil, errors.Wrapf(err, "access.Store.CountGroupMembersIn(): scope %q", scope)
	}

	return counts, nil
}

func (m *storeManager) groupRolesIn(ctx context.Context, scope accesstypes.PolicyScope) ([]policy.Membership, error) {
	roles, err := m.store.ListGroupRolesIn(ctx, scope)
	if err != nil {
		return nil, errors.Wrapf(err, "access.Store.ListGroupRolesIn(): scope %q", scope)
	}

	return roles, nil
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

// GroupContents returns the groups kept in scope, sorted by name, each with how many
// people are in it and the roles it holds: what a list of groups says of each, so a
// person does not open every group to learn what it is for. Both count what has not
// ended, an assignment whose period has not begun among it, which is what DeleteGroup
// refuses a group for: a group listed with no people and no roles is one it removes. A
// role the group's members do not hold through it (the Super Administrator role, and for
// a group kept in one tenant a role held outside it) is listed too, since its row stands
// in a deletion's way like any other.
func (u *userManager) GroupContents(ctx context.Context, scope accesstypes.PolicyScope) ([]GroupContent, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	rows, err := u.store.groups(ctx, scope)
	if err != nil {
		return nil, err
	}
	counts, err := u.store.groupMemberCounts(ctx, scope)
	if err != nil {
		return nil, err
	}
	roles, err := u.store.groupRolesIn(ctx, scope)
	if err != nil {
		return nil, err
	}
	held := make(map[accesstypes.Group][]GroupHolding)
	for i := range roles {
		group := accesstypes.Group(roles[i].Member.Name)
		held[group] = append(held[group], GroupHolding{Scope: roles[i].Scope, Role: roles[i].Role, Period: roles[i].Period})
	}
	contents := make([]GroupContent, 0, len(rows))
	for _, group := range groupInfos(rows) {
		contents = append(contents, GroupContent{GroupInfo: group, People: counts[group.ID], Roles: held[group.ID]})
	}

	return contents, nil
}

// GroupRoleLosers returns the people who lose role in scope when group stops holding it
// there, sorted: the people in the group now who hold the role there through this group
// and in no other way. A person who also holds the role there themselves, or through
// another group, keeps it and is not listed; nor is one who holds it in every tenant,
// themselves or through a group, when scope is one tenant, since the role stays theirs
// in that tenant too. Nobody is listed while the group does not give the role: it does
// not hold it there, the period it holds it for has not begun or has ended, or the role
// is one its members do not hold through it.
func (u *userManager) GroupRoleLosers(ctx context.Context, group accesstypes.Group, scope accesstypes.PolicyScope, role accesstypes.Role) ([]accesstypes.User, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	holders, err := u.RoleHolders(ctx, scope, role)
	if err != nil {
		return nil, err
	}
	everywhere := make(map[accesstypes.User]struct{})
	if _, oneTenant := scope.Domain(); oneTenant {
		wide, err := u.RoleHolders(ctx, accesstypes.EveryDomainPolicyScope(), role)
		if err != nil {
			return nil, err
		}
		for i := range wide {
			everywhere[wide[i].User] = struct{}{}
		}
	}
	losers := make([]accesstypes.User, 0)
	for i := range holders {
		if _, kept := everywhere[holders[i].User]; kept || holders[i].Own {
			continue
		}
		if len(holders[i].Groups) == 1 && holders[i].Groups[0].ID == group {
			losers = append(losers, holders[i].User)
		}
	}

	return losers, nil
}
