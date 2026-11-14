// membership.go is what an application needs of the policy store to write a membership in
// a transaction of its own. The manager's AddUserRoles and DeleteUserRoles each commit by
// themselves, so a membership written beside an application's own rows (a person created
// and seated in one commit) cannot go through them. The application writes the row itself,
// through the resource it declares over the memberships table, and three things stay the
// store's: how a scope is spelled in the row's columns, the rule a membership's role is
// held to, and the word to the engine that the policy changed once the row is committed.
// The rule reads the roles table, and that read is the caller's too: a read through the
// store's own connection would stand outside the transaction the row is written in.

package access

import (
	"context"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/tracer"
)

// MembershipKey is the key of one row of the memberships table (<Prefix><Store>UserRoles),
// its columns in key order. Kind, Axis and Domain spell the scope the membership is held
// in (the stores' encoding, which the table's CHECK constraint holds the kind to), Role is
// the role in the spelling the store holds it, and User is the person as the engine names
// them.
type MembershipKey struct {
	Kind   string
	Axis   string
	Domain string
	Role   accesstypes.Role
	User   accesstypes.User
}

// RoleKey is the key of one row of the roles table (<Prefix><Store>Roles), its columns in
// key order: where a custom role is held, in the stores' encoding of the scope, and the
// role in the spelling the store holds it.
type RoleKey struct {
	Kind   string
	Axis   string
	Domain string
	Role   accesstypes.Role
}

// RoleReader reports whether the roles table holds the row of a key. The caller of
// UserManager.Membership supplies one over the transaction it writes the membership in
// (through the resource it declares over the roles table), so the role is read in the
// transaction that seats a person in it: a role deleted beside it fails one of the two.
type RoleReader func(ctx context.Context, key RoleKey) (bool, error)

// NewMembershipKey spells the membership of user in role, held in scope, as the store's row
// key. It checks nothing: it is the form for reading a row or deleting one, which the
// manager's DeleteUserRoles does whether or not the role still exists. A row to be written
// comes from UserManager.Membership, which holds it to the rule first.
func NewMembershipKey(scope accesstypes.PolicyScope, user accesstypes.User, role accesstypes.Role) MembershipKey {
	kind, axis, domain := policy.ScopeColumns(scope)

	return MembershipKey{Kind: kind, Axis: axis, Domain: domain, Role: role, User: user}
}

// Membership answers the key of the row that says user holds role in scope, for a caller
// that writes it inside a transaction of its own instead of through AddUserRoles. The row
// is held to the rule AddUserRoles holds its own to: role exists in scope under exactly
// that spelling (a default role of scope's kind, a custom role held in scope, or for one
// domain a custom role held in every domain), and user is not empty. A default role is
// answered from the release's role file; a custom role is read through held, the caller's
// reader of the roles table, at most twice. The caller commits the row and then tells the
// engine with Controller.PolicyChanged.
func (u *userManager) Membership(ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User, role accesstypes.Role, held RoleReader) (MembershipKey, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if held == nil {
		return MembershipKey{}, errors.New("access.UserManager.Membership(): the reader of the roles table is nil; pass one over the transaction the membership is written in")
	}
	found, err := u.roleExistsBy(ctx, scope, role, func(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) (bool, error) {
		kind, axis, domain := policy.ScopeColumns(scope)
		exists, err := held(ctx, RoleKey{Kind: kind, Axis: axis, Domain: domain, Role: role})
		if err != nil {
			return false, errors.Wrapf(err, "access.RoleReader(): role %q in scope %q", role, scope)
		}

		return exists, nil
	})
	if err != nil {
		return MembershipKey{}, err
	}
	if !found {
		return MembershipKey{}, httpio.NewNotFoundMessagef("role %q is not a valid role. Please check that the role exists.", role)
	}
	if user == "" {
		return MembershipKey{}, httpio.NewBadRequestMessage("user cannot be empty string")
	}

	return NewMembershipKey(scope, user, role), nil
}

// PolicyChanged tells the engine that policy rows were committed outside its own write
// path: an application wrote a membership inside a transaction of its own and committed
// it. The next check on this instance reloads the policy before it answers, as after the
// manager's own writes, the background loop is nudged, and the other instances are hinted
// through the change signal. Called when nothing changed, it costs one read of the policy
// store.
func (c *Client) PolicyChanged() {
	c.snapEngine.policyChanged()
}
