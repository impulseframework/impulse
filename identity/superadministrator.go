package identity

import (
	"context"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// The rules of the Super Administrator role (accesstypes.SuperAdministrator), the one
// role the framework defines in every auth. They are the rules of the user management,
// held here for every auth whose roles the application gives, whatever its role file
// says. In an auth whose roles come from its directory nobody gives a role by hand: the
// directory decides who is a super administrator, the session library's role
// synchronization writes and removes the role at each sign-in as it does any other, and
// none is seeded.
//
//   - the role is held in the global partition alone (the permission engine refuses a
//     membership of it anywhere else, access.UserManager.Membership);
//   - only a super administrator makes or removes another: giving the role, taking it
//     away, inviting a person to it and deleting a holder's account, which takes the
//     role with it, each take a caller who holds it, and so do disabling a holder's
//     account, which removes a super administrator in effect, and enabling one, which
//     makes one;
//   - only a super administrator changes how another signs in: a password set for a
//     holder or taken from them, a change of password required of them, a new username,
//     a new address (where the code of their second factor is mailed) and the unlinking
//     of an identity each take a caller who holds the role, since each is a way to
//     become that person or to shut them out (requireSuperAdministratorFor);
//   - giving the role ends nothing: the sessions the person holds, their bearer tokens
//     and the browsers remembered for them stand, since a person who is signed in when
//     the role is given is no risk for being signed in. What the role guards is the
//     account, which a second factor is asked for at every sign-in from then on (the
//     session library's rule, signin.SuperAdministratorFactor). What an administrator
//     set on the account before the role, a password or an address, stands too: the
//     super administrator who gives the role chooses whom to trust;
//   - there is always one who can sign in: the role is not taken from the last holder
//     whose account can sign in, and that account is neither disabled nor deleted,
//     whoever asks.
//
// A caller is a person of the request (resource.CallerFrom), and the person the second
// rule judges is the real one behind the session, its actor: a session that views as a
// super administrator is run by someone else, who holds what they hold themselves, and
// a super administrator who views as someone else is a super administrator still. A
// method an application runs in a transaction of its own has no caller, and may give
// the role: that is how a deployment seeds its first administrator, and how an accepted
// invitation gives what a super administrator wrote into it. No caller at all lifts the
// second rule alone; the third holds for everyone.

// superAdministratorKey is the membership that makes user a super administrator.
func superAdministratorKey(user accesstypes.User) access.MembershipKey {
	return access.NewMembershipKey(accesstypes.GlobalPolicyScope(), user, accesstypes.SuperAdministrator)
}

// requireSuperAdministrator refuses an act that makes or removes a super administrator
// unless the real person behind the request holds the role in this auth, read in the
// transaction (callerActor). act says what was asked, as the middle of a sentence
// ("gives the Super Administrator role"). A caller who came through another auth is
// nobody of this one and holds no role here; a call outside any request is the
// application's act and goes through.
func (m *Manager) requireSuperAdministrator(ctx context.Context, txn resource.ReadWriteTransaction, act string) error {
	caller := resource.CallerFrom(ctx)
	if caller.IsZero() {
		return nil
	}
	actor, local := callerActor(ctx, caller)
	if auth, bound := resource.AuthFrom(ctx); local && (!bound || auth == m.auth) {
		key := superAdministratorKey(actor)
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

// requireSuperAdministratorFor refuses an act on how a person signs in, or on where the
// code of their second factor is mailed, when the person holds the Super Administrator
// role and the real person behind the request does not (requireSuperAdministrator): a
// password of the caller's choosing, an address of theirs, or a sign-in the holder can no
// longer make would hand them the role or take its holder away, and it takes a super
// administrator to do either. act says what was asked, as the middle of a sentence
// ("sets a super administrator's password"). Disabling and enabling a holder's account
// ask it too, since one removes a super administrator in effect and the other makes
// one. A person who does not hold the role is
// whoever's the role file says to manage; a super administrator acts on themselves and
// on another as the role file allows; and a call outside any request goes through.
func (m *Manager) requireSuperAdministratorFor(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID, act string) error {
	super, err := m.holdsSuperAdministrator(ctx, txn, userID)
	if err != nil || !super {
		return err
	}

	return m.requireSuperAdministrator(ctx, txn, act)
}

// callerActor is the real person behind the request, as the permission store names
// them: the actor of an impersonated session (the person who started it, whoever it
// views as and whatever role it acts as), and otherwise the caller the entry check ran
// for. The entry check of an impersonated session runs with the grants of the person
// viewed as, so it passes for anyone who views as a super administrator; who makes a
// super administrator is the real person's to answer for. ok is false for an actor who
// has no account here, a person of another application, who is named by a name and
// holds no role of this auth whatever the name spells.
func callerActor(ctx context.Context, caller *resource.Caller) (actor accesstypes.User, ok bool) {
	sess, found := ctx.Value(sessioninfo.CtxSessionInfo).(*sessioninfo.SessionData)
	if !found || sess.Impersonation == nil {
		return caller.Permissions.User(), true
	}
	if !sess.Impersonation.ActorID.Valid {
		return "", false
	}

	return User(sess.Impersonation.ActorID.UUID), true
}

// refuseLastSuperAdministrator refuses an act that would leave the auth with no super
// administrator who can sign in: userID holds the role, their account is not disabled,
// and nobody else is both. act says what was asked of the person, as the end of a
// sentence ("take the Super Administrator role from them", "disable their account").
// It is refused whoever asks, a call outside any request included. A person who holds
// the role and cannot sign in is nobody's way back in, so an act on them is not judged.
//
// The holders it counts are the committed ones, read in the transaction, as no read sees
// a buffered write: two requests that each take a holder away at once are judged one
// after the other by the database, and the second finds the first's commit. A
// transaction that takes away one holder and then another would judge each against the
// rows as they were before it. No request reaches that: every method the generated
// management serves runs alone in a transaction of its own and names one person. An
// application that runs these methods in a transaction it opens itself takes the role
// from, disables or deletes one holder per transaction.
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
