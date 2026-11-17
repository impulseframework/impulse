package identity

import (
	"context"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/tracer"
)

// UnlinkIdentity buffers the removal of one identity, the link between a user and the
// identity a directory verified for them, in the caller's transaction: once it commits,
// a sign-in through that identity no longer reaches the user. The sessions established
// through it stand. A user who holds no password keeps at least one identity, since
// with none they could not sign in at all; a user who holds a password may lose every
// one. A super administrator's identity is unlinked by a super administrator alone, since
// it is a way they sign in (requireSuperAdministratorFor).
//
// The count that rule reads is the committed one: a second unlink of the same user's
// identities in one transaction does not see the first, as no read sees a buffered
// write. Unlink one identity of a user per transaction.
//
// Shared rule: the session library's UnlinkIdentity, with its refusal
// (sessionstorage.ErrLastSignInMethod).
func (m *Manager) UnlinkIdentity(ctx context.Context, txn resource.ReadWriteTransaction, identityID impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	userID, found, err := m.store.IdentityUser(ctx, txn, identityID)
	if err != nil {
		return errors.Wrap(err, "identity.Store.IdentityUser()")
	}
	if !found {
		return httpio.NewNotFoundMessagef("identity %q does not exist", identityID)
	}
	if err := m.requireSuperAdministratorFor(ctx, txn, userID, "unlinks a super administrator's sign-in identity"); err != nil {
		return err
	}
	hasPassword, found, err := m.store.UserHasPassword(ctx, txn, userID)
	if err != nil {
		return errors.Wrap(err, "identity.Store.UserHasPassword()")
	}
	if !found {
		return httpio.NewNotFoundMessagef("user id %q does not exist", userID)
	}
	if !hasPassword {
		links, err := m.store.Identities(ctx, txn, userID)
		if err != nil {
			return errors.Wrap(err, "identity.Store.Identities()")
		}
		if len(links) <= 1 {
			return httpio.NewConflictMessageWithError(sessionstorage.ErrLastSignInMethod, "the user has no other means of sign-in")
		}
	}
	if err := m.store.DeleteIdentity(ctx, txn, identityID); err != nil {
		return errors.Wrap(err, "identity.Store.DeleteIdentity()")
	}

	return nil
}

// RemovePassword buffers the removal of a user's password, in the caller's transaction:
// once it commits, a sign-in with the password is refused as a wrong password is, and
// the person signs in through the identities linked to them. A user who holds no
// identity keeps the password, since with neither they could not sign in at all. The
// mark that the person must change the password goes with it, and the count of failed
// sign-ins kept under their name is cleared. The sessions the person holds stand, the
// ones the password established among them, as a removed identity's do. A super
// administrator's password is removed by a super administrator alone, since it is a way
// they sign in (requireSuperAdministratorFor).
//
// The identities that rule reads are the committed ones: an unlink of the user's last
// identity in the same transaction is not seen, as no read sees a buffered write.
// Remove a person's password and unlink their identity in transactions of their own.
//
// Shared rule: the session library's RemoveSessionUserPassword, with its refusal
// (sessionstorage.ErrLastSignInMethod).
func (m *Manager) RemovePassword(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	username, found, err := m.store.Username(ctx, txn, userID)
	if err != nil {
		return errors.Wrap(err, "identity.Store.Username()")
	}
	if !found {
		return httpio.NewNotFoundMessagef("user id %q does not exist", userID)
	}
	if err := m.requireSuperAdministratorFor(ctx, txn, userID, "removes a super administrator's password"); err != nil {
		return err
	}
	hasPassword, found, err := m.store.UserHasPassword(ctx, txn, userID)
	if err != nil {
		return errors.Wrap(err, "identity.Store.UserHasPassword()")
	}
	if !found {
		return httpio.NewNotFoundMessagef("user id %q does not exist", userID)
	}
	if !hasPassword {
		return httpio.NewBadRequestMessage("the user has no password to remove")
	}
	links, err := m.store.Identities(ctx, txn, userID)
	if err != nil {
		return errors.Wrap(err, "identity.Store.Identities()")
	}
	if len(links) == 0 {
		return httpio.NewConflictMessageWithError(sessionstorage.ErrLastSignInMethod, "the user has no other means of sign-in: link a directory sign-in first")
	}
	if err := m.store.RemovePassword(ctx, txn, userID); err != nil {
		return errors.Wrap(err, "identity.Store.RemovePassword()")
	}
	if err := m.store.ClearAttempts(ctx, txn, session.SignInAttemptName(username)); err != nil {
		return errors.Wrap(err, "identity.Store.ClearAttempts()")
	}

	return nil
}
