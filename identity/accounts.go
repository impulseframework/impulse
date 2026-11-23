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
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/tracer"
)

// RenameUser buffers a new username for a user, in the caller's transaction, and with
// it what carries the name for people to read: their live sessions, and the standing
// impersonation records they hold as actor (and the sessions of those that act as a
// role, which carry the actor's own name). Nothing the person holds follows the name:
// the permission store names a person by the id of their account, so the roles stay
// where they are. A name another user holds is a Conflict; the same name in another
// case is the person's own. The name is a typed one, held to sessioninfo.UsernameRefusal.
// An administrator's rename is not held to the rule of a person's own choice, that a name
// with an @ in it is their own confirmed address: an administrator gives an account the
// name its directory will send, and is who renames an account that holds a name it
// should not. A super administrator is renamed by a super administrator alone, since the
// name is what the person signs in with (requireSuperAdministratorFor).
//
// Shared rule: the session library's ChangeSessionUserUsername, which renames the user,
// the live sessions and the standing records in one transaction of its own.
func (m *Manager) RenameUser(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID, username string) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := m.refuseImpersonatorAsSubject(ctx, userID, "rename your own account"); err != nil {
		return err
	}
	name := strings.TrimSpace(username)
	if name == "" {
		return httpio.NewBadRequestMessage("The username is empty. Type a username.")
	}
	if err := sessioninfo.UsernameRefusal(name); err != nil {
		return err
	}
	old, found, err := m.store.Username(ctx, txn, userID)
	if err != nil {
		return errors.Wrap(err, "identity.Store.Username()")
	}
	if !found {
		return accountNotFound(userID)
	}
	if err := m.requireSuperAdministratorFor(ctx, txn, userID, "renames a super administrator"); err != nil {
		return err
	}
	if err := m.requireEnteringFor(ctx, txn, userID); err != nil {
		return err
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
	if err := m.store.UpdateUser(ctx, txn, userID, &UserChange{Username: &name, Disabled: nil, PasswordChangeRequired: nil, Name: nil}); err != nil {
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

	return nil
}

// DeleteUser buffers the removal of a user, in the caller's transaction: every session
// they hold is ended, their identity links, every membership they hold and their place
// in every group go, then the row, and the permission engine is told once the
// transaction has committed. Nobody
// deletes themselves: the person acting, the real person behind the session, is refused
// their own account, and a session that views as someone else the account it views as
// (refuseOwnAccount). In a session that acts as someone else the refusal of the real
// person's own account is the one DisableUser and EnableUser give there, in the same
// sentence form and with the same log line. A super
// administrator's account is deleted by a super administrator alone, and the last one
// who can sign in by nobody (requireSuperAdministrator, refuseLastSuperAdministrator).
//
// Shared rule: the session library's DeleteSessionUser; the memberships are the user
// management's, which removes every one the person holds with the account.
func (m *Manager) DeleteUser(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	_, found, err := m.store.Username(ctx, txn, userID)
	if err != nil {
		return errors.Wrap(err, "identity.Store.Username()")
	}
	if !found {
		return accountNotFound(userID)
	}
	if err := m.refuseOwnAccount(ctx, userID, "delete"); err != nil {
		return err
	}
	// The account's roles go with it, the Super Administrator role among them: only a
	// super administrator removes another, and never the last one who can sign in.
	super, err := m.holdsSuperAdministrator(ctx, txn, userID)
	if err != nil {
		return err
	}
	if super {
		if err := m.requireSuperAdministrator(ctx, txn, "deletes a super administrator's account, since the role goes with it"); err != nil {
			return err
		}
		if err := m.refuseLastSuperAdministrator(ctx, txn, userID, "delete their account"); err != nil {
			return err
		}
	}
	if err := m.requireEnteringFor(ctx, txn, userID); err != nil {
		return err
	}
	held, err := m.store.Memberships(ctx, txn, userID)
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
	inGroups, err := m.removeGroupPlaces(ctx, txn, userID)
	if err != nil {
		return err
	}
	if len(held) > 0 || inGroups {
		return m.policyChangesAtCommit(txn)
	}

	return nil
}

// resetLinksEndAtCommit registers the withdrawal of the user's standing password reset
// links for after the commit of a transaction that writes or removes their password: a
// link mailed for the password before does not reset the one after.
func (m *Manager) resetLinksEndAtCommit(txn resource.ReadWriteTransaction, userID impulse.UUID) error {
	return m.noticeAfterCommit(txn, "the withdrawal of the password reset links of user "+userID.String(), func(ctx context.Context) error {
		return m.sessions.EndPasswordResetLinks(ctx, userID)
	})
}

// SendPasswordResetLink has a link that resets a user's password mailed to the address
// they confirmed, once the caller's transaction commits. Nothing about the account
// changes when it is sent: the password it has signs the person in, and their sessions
// stand, until the link is used. The message says who asked: an administrator of the
// tenant the request was checked in, or of the application. sent is told how the
// mailing went, once the transaction has committed: whether the link was mailed and,
// where it was not, why, in a sentence for the administrator who asked (the account has
// no password to reset or no confirmed address, or the address was mailed too often).
//
// The link goes to the person's own mailbox and to nowhere the caller names, so it is
// the caller's to send for whoever the role file lets them manage; an account of
// another auth, and no account, are NotFound. A super administrator's account is acted
// on by a super administrator alone, and sending its holder a reset link is acting on
// it: anybody else is refused (requireSuperAdministratorFor).
//
// Shared rule: the session library's SendPasswordReset, which counts the message
// against the limit on reset links to an address, the person's own requests with it.
func (m *Manager) SendPasswordResetLink(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID, sent func(sent bool, reason string)) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := m.refuseImpersonatorAsSubject(ctx, userID, "send yourself a password reset link"); err != nil {
		return err
	}
	if _, found, err := m.store.Username(ctx, txn, userID); err != nil {
		return errors.Wrap(err, "identity.Store.Username()")
	} else if !found {
		return accountNotFound(userID)
	}
	if err := m.requireSuperAdministratorFor(ctx, txn, userID, "sends a super administrator a password reset link"); err != nil {
		return err
	}
	by := askedBy(ctx)

	return m.mailAfterCommit(txn, "the password reset link of user "+userID.String(), sent, func(ctx context.Context) error {
		return m.sessions.SendPasswordReset(ctx, userID, by)
	})
}

// RevokeTrustedDevices buffers an administrator's reset of a user's second factor, in
// the caller's transaction: every browser the person asked to be remembered is
// forgotten, so their next sign-in from any of them asks for the factor. The sessions
// that stand are not touched (EndUserSessions ends them). Once the transaction has
// committed the person is told, at the address they confirmed, that their sign-in was
// reset.
//
// Shared rule: the session library's ResetFactors.
func (m *Manager) RevokeTrustedDevices(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := m.refuseImpersonatorAsSubject(ctx, userID, "revoke your own trusted devices"); err != nil {
		return err
	}
	devices, err := m.store.TrustedDevices(ctx, txn, userID)
	if err != nil {
		return errors.Wrap(err, "identity.Store.TrustedDevices()")
	}
	for _, id := range devices {
		if err := m.store.RevokeTrustedDevice(ctx, txn, id); err != nil {
			return errors.Wrap(err, "identity.Store.RevokeTrustedDevice()")
		}
	}

	return m.noticeAfterCommit(txn, "the notice of a second factor reset to user "+userID.String(), func(ctx context.Context) error {
		return m.sessions.NoticeFactorReset(ctx, userID)
	})
}

// UnlockSignIn buffers the removal of the count of failed sign-ins kept under a name,
// in the caller's transaction, and with it the lock the count earned: once it commits,
// the name may sign in again. The name is what was typed into the username field,
// whether or not a user holds it, and is keyed as the session library keys it; a name
// with no count is no error. The table also keeps the session library's own counts (the
// mail an address was sent, its sign-in codes, its reset links, the wrong sign-in codes
// entered for an account, the guesses at a first password's code) under keys no name
// folds to, and a row of one, named as the list shows it, is cleared the same way
// (session.SignInAttemptKey). Clearing the row of the wrong codes entered for an
// account (CODE-GUESS: and the account's id) is how an administrator lets an account
// whose sign-in codes are spent take codes again.
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
		return httpio.NewBadRequestMessage("No name is given. Give the name the failed sign-ins are counted under.")
	}
	if err := m.refuseImpersonatorsName(ctx, txn, name); err != nil {
		return err
	}
	if err := m.store.ClearAttempts(ctx, txn, session.SignInAttemptKey(name)); err != nil {
		return errors.Wrap(err, "identity.Store.ClearAttempts()")
	}

	return nil
}
