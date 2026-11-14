package identity

import (
	"context"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/tracer"
)

// RenameUser buffers a new username for a user, in the caller's transaction, and with
// it everything that names the person by username: their live sessions, the standing
// impersonation records they hold as actor (and the sessions of those that act as a
// role, which carry the actor's own name), and every membership, since the permission
// store names a person by username and a person renamed keeps their roles only if the
// rows follow the name. A name another user holds is a Conflict; the same name in
// another case is the person's own.
//
// Shared rule: the session library's ChangeSessionUserUsername, which renames the user,
// the live sessions and the standing records in one transaction of its own; the
// memberships follow through the hook the application sets for it (RolesFollowRename).
func (m *Manager) RenameUser(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID, username string) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	name := strings.TrimSpace(username)
	if name == "" {
		return httpio.NewBadRequestMessage("a username is required")
	}
	old, found, err := m.store.Username(ctx, txn, userID)
	if err != nil {
		return errors.Wrap(err, "identity.Store.Username()")
	}
	if !found {
		return httpio.NewNotFoundMessagef("user id %q does not exist", userID)
	}
	if name == old {
		return nil
	}
	holder, held, err := m.store.UserID(ctx, txn, name)
	if err != nil {
		return errors.Wrap(err, "identity.Store.UserID()")
	}
	if held && holder != userID {
		return httpio.NewConflictMessagef("a user named %q already exists", name)
	}

	// Everything is read before anything is buffered: the sessions that carry the
	// name, and the records the person holds.
	sessions, err := m.store.LiveSessions(ctx, txn, userID)
	if err != nil {
		return errors.Wrap(err, "identity.Store.LiveSessions()")
	}
	records, err := m.store.ImpersonationsHeldBy(ctx, txn, old)
	if err != nil {
		return errors.Wrap(err, "identity.Store.ImpersonationsHeldBy()")
	}
	for _, record := range records {
		if !record.ActsAsRole || slices.Contains(sessions, record.SessionID) {
			continue
		}
		expired, exists, err := m.store.SessionExpired(ctx, txn, record.SessionID)
		if err != nil {
			return errors.Wrap(err, "identity.Store.SessionExpired()")
		}
		if exists && !expired {
			sessions = append(sessions, record.SessionID)
		}
	}
	moved, err := m.moveMemberships(ctx, txn, old, name)
	if err != nil {
		return err
	}

	if err := m.store.UpdateUser(ctx, txn, userID, &UserChange{Username: &name, Disabled: nil, PasswordChangeRequired: nil, Email: nil}); err != nil {
		return errors.Wrap(err, "identity.Store.UpdateUser()")
	}
	for _, id := range sessions {
		if err := m.store.RenameSession(ctx, txn, id, name); err != nil {
			return errors.Wrap(err, "identity.Store.RenameSession()")
		}
	}
	for _, record := range records {
		if err := m.store.RenameImpersonationActor(ctx, txn, record.SessionID, name); err != nil {
			return errors.Wrap(err, "identity.Store.RenameImpersonationActor()")
		}
	}
	if moved {
		return m.policyChangesAtCommit(txn)
	}

	return nil
}

// DeleteUser buffers the removal of a user, in the caller's transaction: every session
// they hold is ended, their identity links and every membership they hold go, then the
// row, and the permission engine is told once the transaction has committed. Nobody
// deletes themselves: the request's caller is refused their own account.
//
// Shared rule: the session library's DeleteSessionUser; the memberships are the user
// management's, which removes every one the person holds with the account.
func (m *Manager) DeleteUser(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	username, found, err := m.store.Username(ctx, txn, userID)
	if err != nil {
		return errors.Wrap(err, "identity.Store.Username()")
	}
	if !found {
		return httpio.NewNotFoundMessagef("user id %q does not exist", userID)
	}
	if callerIs(ctx, username) {
		return httpio.NewForbiddenMessage("you cannot delete your own account")
	}
	held, err := m.store.Memberships(ctx, txn, username)
	if err != nil {
		return errors.Wrap(err, "identity.Store.Memberships()")
	}
	if err := m.deleteAccount(ctx, txn, userID); err != nil {
		return err
	}
	for _, key := range held {
		if err := m.store.DeleteMembership(ctx, txn, &key); err != nil {
			return errors.Wrap(err, "identity.Store.DeleteMembership()")
		}
	}
	if len(held) > 0 {
		return m.policyChangesAtCommit(txn)
	}

	return nil
}

// SetTemporaryPassword buffers a password an operator chose for a user, in the caller's
// transaction: the person must change it before anything else is served, every session
// they hold is ended, and the count of failed sign-ins kept under their name is
// cleared, so a person locked out signs in with the new password at once. The hash is
// the session library's, made by the password method's hasher.
//
// Shared rule: the session library's ResetSessionUserPassword, which does the same in
// commits of its own.
func (m *Manager) SetTemporaryPassword(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID, password string) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	username, found, err := m.store.Username(ctx, txn, userID)
	if err != nil {
		return errors.Wrap(err, "identity.Store.Username()")
	}
	if !found {
		return httpio.NewNotFoundMessagef("user id %q does not exist", userID)
	}
	hash, err := m.sessions.HashPassword(password)
	if err != nil {
		return errors.Wrap(err, "identity.SessionAPI.HashPassword()")
	}
	// The sessions are read before anything is buffered: they are the ones live now.
	if err := m.endUserSessions(ctx, txn, userID); err != nil {
		return err
	}
	if err := m.store.SetPassword(ctx, txn, userID, hash, true); err != nil {
		return errors.Wrap(err, "identity.Store.SetPassword()")
	}
	if err := m.store.ClearAttempts(ctx, txn, session.SignInAttemptName(username)); err != nil {
		return errors.Wrap(err, "identity.Store.ClearAttempts()")
	}

	return nil
}

// RevokeTrustedDevices buffers an administrator's reset of a user's second factor, in
// the caller's transaction: every browser the person asked to be remembered is
// forgotten, so their next sign-in from any of them asks for the factor. The sessions
// that stand are not touched (EndUserSessions ends them).
//
// Shared rule: the session library's ResetFactors.
func (m *Manager) RevokeTrustedDevices(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	devices, err := m.store.TrustedDevices(ctx, txn, userID)
	if err != nil {
		return errors.Wrap(err, "identity.Store.TrustedDevices()")
	}
	for _, id := range devices {
		if err := m.store.RevokeTrustedDevice(ctx, txn, id); err != nil {
			return errors.Wrap(err, "identity.Store.RevokeTrustedDevice()")
		}
	}

	return nil
}

// UnlockSignIn buffers the removal of the count of failed sign-ins kept under a name,
// in the caller's transaction, and with it the lock the count earned: once it commits,
// the name may sign in again. The name is what was typed into the username field,
// whether or not a user holds it, and is keyed as the session library keys it
// (session.SignInAttemptName); a name with no count is no error.
//
// Each instance of the application trusts what it remembers of a name for a few
// seconds, a lock included, so the lock is gone from every instance within that time
// of the commit, never at once. The locks the session library holds against a client's
// address and cookie are in each instance's memory alone and are not cleared: they
// throttle a client that kept trying, whatever name it tried.
func (m *Manager) UnlockSignIn(ctx context.Context, txn resource.ReadWriteTransaction, name string) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if strings.TrimSpace(name) == "" {
		return httpio.NewBadRequestMessage("a name is required: the name the failed sign-ins are counted under")
	}
	if err := m.store.ClearAttempts(ctx, txn, session.SignInAttemptName(name)); err != nil {
		return errors.Wrap(err, "identity.Store.ClearAttempts()")
	}

	return nil
}
