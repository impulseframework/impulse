package identity

import (
	"context"
	netmail "net/mail"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/tracer"
)

// DisableUser buffers the disabling of a user and the end of every session they hold
// (EndUserSessions), in the caller's transaction: once it commits, the person's next
// request is refused, and so is their next sign-in. The sign-in paths cache neither
// fact. Nobody disables themselves: the person acting, the real person behind the
// session, is refused their own account, and a session that views as someone else the
// account it views as (refuseOwnAccount). In a session that acts as someone else the
// refusal of the real person's own account is the one EnableUser and DeleteUser give
// there, in the same sentence form and with the same log line.
// A super administrator's account is disabled by a super administrator alone, since it
// removes one in effect (requireSuperAdministratorFor), and the last super administrator
// who can sign in is disabled by nobody (refuseLastSuperAdministrator).
//
// Shared rule: disabling a user ends their sessions, which the session library's
// DeactivateSessionUser does in two commits of its own.
func (m *Manager) DisableUser(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := m.refuseOwnAccount(ctx, userID, "disable"); err != nil {
		return err
	}
	if err := m.requireSuperAdministratorFor(ctx, txn, userID, "disables a super administrator's account"); err != nil {
		return err
	}
	if err := m.requireEnteringFor(ctx, txn, userID); err != nil {
		return err
	}
	if err := m.refuseLastSuperAdministrator(ctx, txn, userID, "disable their account"); err != nil {
		return err
	}
	// The sessions are read before anything is buffered: they are the ones live now.
	if err := m.endUserSessions(ctx, txn, userID); err != nil {
		return err
	}
	disabled := true
	if err := m.store.UpdateUser(ctx, txn, userID, &UserChange{Username: nil, Disabled: &disabled, PasswordChangeRequired: nil, Name: nil}); err != nil {
		return errors.Wrap(err, "identity.Store.UpdateUser()")
	}

	return nil
}

// EnableUser buffers the enabling of a disabled user, who may sign in again once it
// commits. The sessions that ended when the user was disabled stay ended. In a session
// that acts as someone else the real person behind it does not enable their own
// account, as they do not disable or delete it (refuseImpersonatorAsSubject). A super
// administrator's account is enabled by a super administrator alone, since it makes one
// who can sign in (requireSuperAdministratorFor).
func (m *Manager) EnableUser(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := m.refuseImpersonatorAsSubject(ctx, userID, "enable your own account"); err != nil {
		return err
	}
	if err := m.requireSuperAdministratorFor(ctx, txn, userID, "enables a super administrator's account"); err != nil {
		return err
	}
	if err := m.requireEnteringFor(ctx, txn, userID); err != nil {
		return err
	}
	disabled := false
	if err := m.store.UpdateUser(ctx, txn, userID, &UserChange{Username: nil, Disabled: &disabled, PasswordChangeRequired: nil, Name: nil}); err != nil {
		return errors.Wrap(err, "identity.Store.UpdateUser()")
	}

	return nil
}

// RequirePasswordChange buffers the requirement that a user change their password: once
// it commits, every request of theirs is refused, on the sessions they hold and on new
// ones, until they have changed it. The way out is theirs alone and takes the password
// they hold, so the requirement is refused for a user who holds none: nothing they could
// do would lift it. It is required of a super administrator by a super administrator
// alone: a holder who signs in through a directory and no longer knows the password is
// shut out by it until they follow a password reset link (requireSuperAdministratorFor).
//
// Shared rule: the session library reads the flag on every request and sets it nowhere,
// so this write is the one way a person comes under it.
func (m *Manager) RequirePasswordChange(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := m.refuseImpersonatorAsSubject(ctx, userID, "require a password change of your own account"); err != nil {
		return err
	}
	has, found, err := m.store.UserHasPassword(ctx, txn, userID)
	if err != nil {
		return errors.Wrap(err, "identity.Store.UserHasPassword()")
	}
	if !found {
		return accountNotFound(userID)
	}
	if err := m.requireSuperAdministratorFor(ctx, txn, userID, "requires a super administrator to change their password"); err != nil {
		return err
	}
	if err := m.requireEnteringFor(ctx, txn, userID); err != nil {
		return err
	}
	if !has {
		return httpio.NewBadRequestMessage("This account has no password, so a password change cannot be required of it.")
	}
	required := true
	if err := m.store.UpdateUser(ctx, txn, userID, &UserChange{Username: nil, Disabled: nil, PasswordChangeRequired: &required, Name: nil}); err != nil {
		return errors.Wrap(err, "identity.Store.UpdateUser()")
	}

	return nil
}

// SetEmail asks for a change of a user's address, in the caller's transaction. Nothing
// about the account changes: once the transaction commits, a link is mailed to the new
// address, and the account keeps the address it has, where its codes and links still
// go, until the person follows that link from the new mailbox. Nothing is mailed to the
// old address to confirm the change; it is told once the change is made. sent is told
// how the mailing went, once the transaction has committed: whether the link was mailed
// and, where it was not, why, in a sentence for the administrator who asked.
//
// An account always keeps an address, so one is replaced and never taken off. The
// address of a person who signs in through an organization's directory is the
// directory's to change, and the session library refuses it (the reason says so). Where
// the usernames are the addresses, the username changes with the address, when the
// link is followed.
//
// A super administrator's address is changed by a super administrator alone, and an
// administrator of a tenant the caller does not enter by one who does: the new address
// is where the person's codes and links will be mailed (requireSuperAdministratorFor,
// requireEnteringFor).
//
// Shared rule: the session library's RequestAddressChange.
func (m *Manager) SetEmail(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID, email string, sent func(sent bool, reason string)) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := m.refuseImpersonatorAsSubject(ctx, userID, "change your own email address"); err != nil {
		return err
	}
	if strings.TrimSpace(email) == "" {
		return httpio.NewBadRequestMessage("The email address is empty. An account always keeps an email address, so give the one to replace it with.")
	}
	bare, err := optionalAddress(&email)
	if err != nil {
		return err
	}
	if _, found, err := m.store.Username(ctx, txn, userID); err != nil {
		return errors.Wrap(err, "identity.Store.Username()")
	} else if !found {
		return accountNotFound(userID)
	}
	if err := m.requireSuperAdministratorFor(ctx, txn, userID, "changes a super administrator's email address"); err != nil {
		return err
	}
	if err := m.requireEnteringFor(ctx, txn, userID); err != nil {
		return err
	}
	by := askedBy(ctx)

	return m.mailAfterCommit(txn, "the link that confirms a new address for user "+userID.String(), sent, func(ctx context.Context) error {
		return m.sessions.RequestAddressChange(ctx, userID, *bare, by)
	})
}

// SetName buffers a person's name on a user, or takes the name off (nil, or nothing
// but space), in the caller's transaction. The name is what pages show for the person
// in place of the username; it identifies nobody, so nothing follows it: no session
// ends and no membership moves. A name written here stands until it is written again:
// a later sign-in through a directory fills a name only where the user holds none.
//
// Shared rule: the session library's SetUserName, which holds the name to the same
// rule (sessioninfo.CleanName) and writes it on the row.
func (m *Manager) SetName(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID, name *string) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := m.refuseImpersonatorAsSubject(ctx, userID, "change your own name"); err != nil {
		return err
	}
	clean, err := sessioninfo.CleanName(name)
	if err != nil {
		return errors.Wrap(err, "sessioninfo.CleanName()")
	}
	if _, found, err := m.store.Username(ctx, txn, userID); err != nil {
		return errors.Wrap(err, "identity.Store.Username()")
	} else if !found {
		return accountNotFound(userID)
	}
	if err := m.store.UpdateUser(ctx, txn, userID, &UserChange{Username: nil, Disabled: nil, PasswordChangeRequired: nil, Name: &NameChange{Name: clean}}); err != nil {
		return errors.Wrap(err, "identity.Store.UpdateUser()")
	}

	return nil
}

// optionalAddress validates an address that may be absent and answers it bare: the part
// a message is sent to, without a display name.
func optionalAddress(email *string) (*string, error) {
	if email == nil {
		return nil, nil
	}
	parsed, err := netmail.ParseAddress(strings.TrimSpace(*email))
	if err != nil || !strings.Contains(parsed.Address, "@") {
		return nil, httpio.NewBadRequestMessagef("%q is not an email address. Check it and type it again.", *email)
	}

	return &parsed.Address, nil
}

// RequireUser answers NotFound unless a user of the auth holds the id, read in the
// caller's transaction. A role is held by an account, and AssignRole looks nobody up (it
// serves an account a sign-in created in the same transaction: the acceptance of an
// invitation, a sign-up), so a method that gives a role to a person named in a request
// asks this first.
func (m *Manager) RequireUser(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) error {
	_, found, err := m.store.Username(ctx, txn, userID)
	if err != nil {
		return errors.Wrap(err, "identity.Store.Username()")
	}
	if !found {
		return accountNotFound(userID)
	}

	return nil
}
