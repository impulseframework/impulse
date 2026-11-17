package identity

import (
	"context"
	"slices"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/sessioninfo"
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
// the person signs in through the identities linked to them. A user keeps the password
// unless an identity linked to them can sign them in, since with neither they could not
// sign in at all: an identity counts when the session library says a sign-in through it
// is possible (SessionAPI.SignsInThrough), so a link left from a sign-in method the
// Auth no longer offers, or from a connection none of its registrations takes, does
// not. The rule holds for everyone, the last super administrator among them, whose
// password is their way back in like anyone's. The mark that the person must change
// the password goes with it, and the count of failed sign-ins kept under their name is
// cleared. The sessions the person holds stand, the ones the password established among
// them, as a removed identity's do. A super administrator's password is removed by a
// super administrator alone, since it is a way they sign in
// (requireSuperAdministratorFor).
//
// The identities that rule reads are the committed ones, read in the transaction that
// removes the password: an unlink in another transaction and this removal are judged
// one after the other by the database. An unlink of the user's last identity in the
// same transaction is not seen, as no read sees a buffered write. Remove a person's
// password and unlink their identity in transactions of their own.
//
// Shared rule: the session library's RemoveSessionUserPassword, with its refusal
// (sessionstorage.ErrLastSignInMethod) and its judgment of the links that count.
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
	switch {
	case len(links) == 0:
		return httpio.NewConflictMessageWithError(sessionstorage.ErrLastSignInMethod, "the user has no other means of sign-in: link a directory sign-in first")
	case len(m.signInRegistrations(links)) == 0:
		return httpio.NewConflictMessageWithError(sessionstorage.ErrLastSignInMethod,
			"none of the identities linked to the user can sign them in any more, so the password is their only means of sign-in: link a directory sign-in the login page offers first")
	}
	if err := m.store.RemovePassword(ctx, txn, userID); err != nil {
		return errors.Wrap(err, "identity.Store.RemovePassword()")
	}
	if err := m.store.ClearAttempts(ctx, txn, session.SignInAttemptName(username)); err != nil {
		return errors.Wrap(err, "identity.Store.ClearAttempts()")
	}

	return nil
}

// signInRegistrations names the sign-in registrations of the Auth's login page that the
// links can sign their person in through, each once, in the order the links first reach
// them: the session library's judgment of each link (SessionAPI.SignsInThrough). It is
// empty when none of the links counts as a means of sign-in.
func (m *Manager) signInRegistrations(links []Link) []string {
	var names []string
	for _, link := range links {
		for _, name := range m.sessions.SignsInThrough(link.Method, link.Connection) {
			if !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}

	return names
}

// PasswordWay is the name of the password among the ways a person signs in
// (SignInWay.Registration): the name the password method is registered under.
const PasswordWay = string(sessioninfo.MethodPassword)

// SignInWay is one way a person signs in, as an administrator's page reads it: the
// person's password, or a sign-in registration of the Auth's login page that an
// identity linked to them can sign them in through.
type SignInWay struct {
	// Registration is the name the way is registered under on the Auth: PasswordWay
	// for the password, the application's name for a directory sign-in ("google",
	// "lakeside-entra").
	Registration string
	// Password says the way is the person's password; every other way is a directory
	// sign-in.
	Password bool
}

// SignInWays lists how a person signs in: the password where they hold one, then each
// sign-in registration of the Auth's login page that an identity linked to them can
// sign them in through, once however many identities reach it. It is what a page needs
// to offer the removal of a person's password only where RemovePassword would go
// through, a password held and another way in, and to say what the removal leaves; an
// identity that signs nobody in is no way and is not listed. The password's hash, an
// identity's subject and everything else of either stay unread: the answer is whether,
// and through what. found is false where no user holds the id. It reads and writes
// nothing else, so it runs in any read.
//
// The caller guards who may ask about whom: the generated management serves it to a
// person who may list the identities of the person asked about.
func (m *Manager) SignInWays(ctx context.Context, txn resource.ReadOnlyTransaction, userID impulse.UUID) (ways []SignInWay, found bool, err error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	hasPassword, found, err := m.store.UserHasPassword(ctx, txn, userID)
	if err != nil {
		return nil, false, errors.Wrap(err, "identity.Store.UserHasPassword()")
	}
	if !found {
		return nil, false, nil
	}
	links, err := m.store.Identities(ctx, txn, userID)
	if err != nil {
		return nil, false, errors.Wrap(err, "identity.Store.Identities()")
	}
	if hasPassword {
		ways = append(ways, SignInWay{Registration: PasswordWay, Password: true})
	}
	for _, name := range m.signInRegistrations(links) {
		ways = append(ways, SignInWay{Registration: name})
	}

	return ways, true, nil
}
