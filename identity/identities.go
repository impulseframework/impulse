package identity

import (
	"context"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/tracer"
)

// UnlinkIdentity buffers the removal of one identity, the link between a user and the
// identity a directory verified for them, in the caller's transaction: once it commits,
// a sign-in through that identity no longer reaches the user. The sessions established
// through it stand. A user who holds no password keeps at least one identity, since
// with none they could not sign in at all; a user who holds a password may lose every
// one.
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
