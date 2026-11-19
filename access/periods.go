package access

import (
	"context"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/tracer"
)

// The period of an assignment. A person's role, a person's place in a group and a
// group's role may each carry a start and an end (accesstypes.Period), and count only
// between them: a role taken for a task goes away by itself, and nobody has to remember
// to remove it. The period is kept with the assignment's row as two instants, and no row
// is written when one begins or ends. Everything that reads assignments applies it:
//
//   - a check reads the snapshot compiled for its instant, the one a grant's condition
//     folds now with (the Environment's, and the engine's own clock where the Environment
//     carries none), so an assignment outside its period grants nothing (snapshotEngine);
//   - the listings of what a person or a group holds (UserRoles, RoleUsers, GroupUsers,
//     GroupRoles, UserGroups) leave out an assignment outside its period, read at the
//     database's present, so the sign-in rule and the list of a person's tenants do too.
//
// Changing a period is assigning again: the Assign methods give the assignment the
// period handed, and the Add methods give it none, so a role given again with no end
// stands until it is removed. Ending an assignment now is removing it.

// ValidatePeriod refuses a period an assignment of role cannot carry: one that ends at
// or before its start, which would never count, and any period on the Super
// Administrator role. There is always a super administrator, and an end could leave
// none; a start could too, since the rule that keeps the last one counts who holds the
// role, and a holder whose period has not begun is nobody's way back in. It is the one
// statement of the rule, asked by every write of a person's role here and by a caller
// that writes the row in a transaction of its own (UserManager.Membership).
func ValidatePeriod(role accesstypes.Role, period accesstypes.Period) error {
	if err := period.Validate(); err != nil {
		return httpio.NewBadRequestMessage(err.Error())
	}
	if role == accesstypes.SuperAdministrator && !period.IsZero() {
		return httpio.NewBadRequestMessagef("the %s role takes no end and no start: there is always a super administrator, and a period could leave none. Give the role with no period, and remove it when it is no longer wanted", string(role))
	}

	return nil
}

// AssignUserRole gives user the role in scope for the period, replacing the period of an
// assignment that is there. Errors as AddUserRoles does, and if the period is one the
// role cannot carry (ValidatePeriod).
func (u *userManager) AssignUserRole(ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User, role accesstypes.Role, period accesstypes.Period) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()
	u, announce := u.holdAnnouncement()
	defer announce()

	return u.assignUserRoles(ctx, scope, user, period, role)
}

// AssignGroupUser puts user in group for the period, replacing the period of a place
// that is there. Errors as AddGroupUsers does, and if the period ends at or before its
// start.
func (u *userManager) AssignGroupUser(ctx context.Context, group accesstypes.Group, user accesstypes.User, period accesstypes.Period) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()
	u, announce := u.holdAnnouncement()
	defer announce()

	return u.assignGroupUsers(ctx, group, period, user)
}

// AssignGroupRole gives group the role in scope for the period, replacing the period of
// one the group holds there. Errors as AddGroupRoles does, and if the period ends at or
// before its start.
func (u *userManager) AssignGroupRole(ctx context.Context, group accesstypes.Group, scope accesstypes.PolicyScope, role accesstypes.Role, period accesstypes.Period) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()
	u, announce := u.holdAnnouncement()
	defer announce()

	return u.assignGroupRoles(ctx, group, scope, period, role)
}
