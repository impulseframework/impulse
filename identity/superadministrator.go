package identity

import (
	"context"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
)

// The rules of the Super Administrator role (accesstypes.SuperAdministrator), the one
// role the framework defines in every auth. They are held here for every auth, whatever
// its role file says:
//
//   - the role is held in the global partition alone (the permission engine refuses a
//     membership of it anywhere else, access.UserManager.Membership);
//   - only a super administrator makes or removes another: giving the role, taking it
//     away, inviting a person to it and deleting a holder's account, which takes the
//     role with it, each take a caller who holds it;
//   - there is always one who can sign in: the role is not taken from the last holder
//     whose account can sign in, and that account is neither disabled nor deleted,
//     whoever asks.
//
// A caller is a person of the request (resource.CallerFrom). A method an application
// runs in a transaction of its own has none, and may give the role: that is how a
// deployment seeds its first administrator, and how an accepted invitation gives what
// a super administrator wrote into it. No caller at all lifts the second rule alone;
// the third holds for everyone.

// superAdministratorKey is the membership that makes user a super administrator.
func superAdministratorKey(user accesstypes.User) access.MembershipKey {
	return access.NewMembershipKey(accesstypes.GlobalPolicyScope(), user, accesstypes.SuperAdministrator)
}

// requireSuperAdministrator refuses an act that makes or removes a super administrator
// unless the request's caller holds the role in this auth, read in the transaction.
// act says what was asked, as the middle of a sentence ("gives the Super Administrator
// role"). A caller who came through another auth is nobody of this one and holds no
// role here; a call outside any request is the application's act and goes through.
func (m *Manager) requireSuperAdministrator(ctx context.Context, txn resource.ReadWriteTransaction, act string) error {
	caller := resource.CallerFrom(ctx)
	if caller.IsZero() {
		return nil
	}
	if auth, bound := resource.AuthFrom(ctx); !bound || auth == m.auth {
		key := superAdministratorKey(caller.Permissions.User())
		held, err := m.store.HasMembership(ctx, txn, &key)
		if err != nil {
			return errors.Wrap(err, "identity.Store.HasMembership()")
		}
		if held {
			return nil
		}
	}

	return httpio.NewForbiddenMessagef("only a super administrator %s, and you do not hold the Super Administrator role", act)
}

// refuseLastSuperAdministrator refuses an act that would leave the auth with no super
// administrator who can sign in: userID holds the role, their account is not disabled,
// and nobody else is both. act says what was asked of the person, as the end of a
// sentence ("take the Super Administrator role from them", "disable their account").
// It is refused whoever asks, a call outside any request included. A person who holds
// the role and cannot sign in is nobody's way back in, so an act on them is not judged.
func (m *Manager) refuseLastSuperAdministrator(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID, act string) error {
	last, err := m.lastSuperAdministrator(ctx, txn, userID)
	if err != nil || !last {
		return err
	}
	if m.callerIs(ctx, userID) {
		return httpio.NewForbiddenMessage("you are the last super administrator who can sign in: give another person the Super Administrator role before you remove your own")
	}

	return httpio.NewForbiddenMessagef("this person is the last super administrator who can sign in: give another person the Super Administrator role before you %s", act)
}

// lastSuperAdministrator reports whether userID is the one super administrator who can
// sign in: a holder of the role whose account is not disabled, with no other such
// holder.
func (m *Manager) lastSuperAdministrator(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) (bool, error) {
	key := superAdministratorKey(User(userID))
	held, err := m.store.HasMembership(ctx, txn, &key)
	if err != nil {
		return false, errors.Wrap(err, "identity.Store.HasMembership()")
	}
	if !held {
		return false, nil
	}
	signsIn, err := m.signsIn(ctx, txn, userID)
	if err != nil || !signsIn {
		return false, err
	}
	holders, err := m.store.RoleHolders(ctx, txn, &access.RoleKey{Kind: key.Kind, Axis: key.Axis, Domain: key.Domain, Role: key.Role})
	if err != nil {
		return false, errors.Wrap(err, "identity.Store.RoleHolders()")
	}
	for _, holder := range holders {
		other, ok := UserID(holder)
		if !ok || other == userID {
			continue
		}
		signsIn, err := m.signsIn(ctx, txn, other)
		if err != nil {
			return false, err
		}
		if signsIn {
			return false, nil
		}
	}

	return true, nil
}

// signsIn reports whether the id is an account that can sign in: a user holds it and
// is not disabled.
func (m *Manager) signsIn(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) (bool, error) {
	disabled, found, err := m.store.UserDisabled(ctx, txn, userID)
	if err != nil {
		return false, errors.Wrap(err, "identity.Store.UserDisabled()")
	}

	return found && !disabled, nil
}

// holdsSuperAdministrator reports whether the person holds the role, read in the
// transaction.
func (m *Manager) holdsSuperAdministrator(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) (bool, error) {
	key := superAdministratorKey(User(userID))
	held, err := m.store.HasMembership(ctx, txn, &key)
	if err != nil {
		return false, errors.Wrap(err, "identity.Store.HasMembership()")
	}

	return held, nil
}
