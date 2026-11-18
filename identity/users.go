package identity

import (
	"context"
	netmail "net/mail"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/tracer"
)

// NewAccount is a person to create: who they are, and the roles they hold from the
// start.
type NewAccount struct {
	Username string
	// Name is the person's name as people read it, nil for none: what pages show for
	// the person in place of the username. The person may change it themselves.
	Name *string
	// Email is the person's address, entered unverified; nil for none, and always nil
	// where the auth's usernames are the addresses.
	Email *string
	// Scope is where the roles are held, and Roles the roles: at least one.
	Scope accesstypes.PolicyScope
	Roles []accesstypes.Role
	// Password is a temporary password an operator chose, which the person changes
	// before anything else is served; nil for a person who signs in another way or
	// chooses their own from an invitation. PasswordRequired marks an auth where the
	// password is the only way in, so a person created without one could never sign
	// in. Both are for an auth with a password method alone.
	Password         *string
	PasswordRequired bool
}

// CreateUser buffers a new user and the memberships that give them their roles, in the
// caller's transaction. Without a password the person signs in through a directory, or
// follows an invitation and chooses their own; with one (NewAccount.Password) it is a
// temporary password, which the person changes before anything else is served. In an
// application whose tenants have directories, the sign-in that first links such an
// account, on the name the directory asserts, is made through the directory of the one
// tenant the person belongs to or with their own invitation (the session library's rule
// on a first link made on a name alone): a person created in two tenants, or holding a
// role in every tenant or outside them, is invited or given a temporary password. It
// answers the id the row is created under, so the caller may write its own rows beside
// it before anything is committed. A username or an address another user holds is
// refused by the table's unique indexes when the transaction commits.
//
// A person is created holding a role: an account that holds no role anywhere is not
// kept (RemoveRole deletes one with its last role), so none is created without one.
// Every role is held in the one scope and is held to the rule a role assigned later is
// (AssignRole): it exists there under exactly this spelling.
//
// Shared rule: an address is written bare, the part a message is sent to, as the session
// library writes one (its address function); a value that is no address is refused. A
// name is held to the session library's rule of one (sessioninfo.CleanName): without
// the space around it, none where nothing is left, refused where it is too long.
func (m *Manager) CreateUser(ctx context.Context, txn resource.ReadWriteTransaction, account *NewAccount) (impulse.UUID, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	username := strings.TrimSpace(account.Username)
	if username == "" {
		return impulse.NilUUID, httpio.NewBadRequestMessage("a user needs a username")
	}
	bare, err := optionalAddress(account.Email)
	if err != nil {
		return impulse.NilUUID, err
	}
	name, err := sessioninfo.CleanName(account.Name)
	if err != nil {
		return impulse.NilUUID, errors.Wrap(err, "sessioninfo.CleanName()")
	}
	roles := distinctRoles(account.Roles)
	if len(roles) == 0 {
		return impulse.NilUUID, httpio.NewBadRequestMessage("at least one role is required: an account that holds no role anywhere is not kept, so none is created without one")
	}
	hash, err := m.temporaryPassword(account)
	if err != nil {
		return impulse.NilUUID, err
	}

	id, err := m.store.InsertUser(ctx, txn, &NewUser{Username: username, Name: name, Email: bare})
	if err != nil {
		return impulse.NilUUID, errors.Wrap(err, "identity.Store.InsertUser()")
	}
	if hash != "" {
		if err := m.store.SetPassword(ctx, txn, id, hash, true); err != nil {
			return impulse.NilUUID, errors.Wrap(err, "identity.Store.SetPassword()")
		}
	}
	for _, role := range roles {
		if err := m.AssignRole(ctx, txn, account.Scope, id, role); err != nil {
			return impulse.NilUUID, err
		}
	}

	return id, nil
}

// temporaryPassword hashes the password a new account is created with, "" for none. A
// password is required where it is the only way in, and an empty one is never a
// password.
func (m *Manager) temporaryPassword(account *NewAccount) (string, error) {
	if account.PasswordRequired && (account.Password == nil || *account.Password == "") {
		return "", httpio.NewBadRequestMessage("a password is required: the password is the way in")
	}
	if account.Password == nil {
		return "", nil
	}
	if *account.Password == "" {
		return "", httpio.NewBadRequestMessage("an empty password is not a password: leave it out for a person who signs in another way")
	}
	hash, err := m.sessions.HashPassword(*account.Password)
	if err != nil {
		return "", errors.Wrap(err, "identity.SessionAPI.HashPassword()")
	}

	return hash, nil
}

// distinctRoles returns the roles a request names, each once, in the order given: a
// role named twice is one membership, and two inserts of it would fail the commit.
func distinctRoles(roles []accesstypes.Role) []accesstypes.Role {
	distinct := make([]accesstypes.Role, 0, len(roles))
	for _, role := range roles {
		if !slices.Contains(distinct, role) {
			distinct = append(distinct, role)
		}
	}

	return distinct
}

// DisableUser buffers the disabling of a user and the end of every session they hold
// (EndUserSessions), in the caller's transaction: once it commits, the person's next
// request is refused, and so is their next sign-in. The sign-in paths cache neither
// fact. Nobody disables themselves: the person acting, the real person behind the
// session, is refused their own account, and a session that views as someone else the
// account it views as (refuseOwnAccount).
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
// commits. The sessions that ended when the user was disabled stay ended. A super
// administrator's account is enabled by a super administrator alone, since it makes one
// who can sign in (requireSuperAdministratorFor).
func (m *Manager) EnableUser(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

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
// shut out by it until someone sets them another (requireSuperAdministratorFor).
//
// Shared rule: the session library refuses the flag on a user without a password where
// it creates one (CreateSessionUser), and reads it on every request.
func (m *Manager) RequirePasswordChange(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	has, found, err := m.store.UserHasPassword(ctx, txn, userID)
	if err != nil {
		return errors.Wrap(err, "identity.Store.UserHasPassword()")
	}
	if !found {
		return httpio.NewNotFoundMessagef("user id %q does not exist", userID)
	}
	if err := m.requireSuperAdministratorFor(ctx, txn, userID, "requires a super administrator to change their password"); err != nil {
		return err
	}
	if err := m.requireEnteringFor(ctx, txn, userID); err != nil {
		return err
	}
	if !has {
		return httpio.NewBadRequestMessage("a password change can only be required of a user that has a password")
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

	if strings.TrimSpace(email) == "" {
		return httpio.NewBadRequestMessage("an address is required: an account always keeps an email address, so one is replaced and never taken off")
	}
	bare, err := optionalAddress(&email)
	if err != nil {
		return err
	}
	if _, found, err := m.store.Username(ctx, txn, userID); err != nil {
		return errors.Wrap(err, "identity.Store.Username()")
	} else if !found {
		return httpio.NewNotFoundMessagef("user id %q does not exist", userID)
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

	clean, err := sessioninfo.CleanName(name)
	if err != nil {
		return errors.Wrap(err, "sessioninfo.CleanName()")
	}
	if _, found, err := m.store.Username(ctx, txn, userID); err != nil {
		return errors.Wrap(err, "identity.Store.Username()")
	} else if !found {
		return httpio.NewNotFoundMessagef("user id %q does not exist", userID)
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
		return nil, httpio.NewBadRequestMessagef("%q is not an email address", *email)
	}

	return &parsed.Address, nil
}

// RequireUser answers NotFound unless a user of the auth holds the id, read in the
// caller's transaction. A role is held by an account, and AssignRole looks nobody up (it
// serves a user created in its own transaction), so a method that gives a role to a
// person named in a request asks this first.
func (m *Manager) RequireUser(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) error {
	_, found, err := m.store.Username(ctx, txn, userID)
	if err != nil {
		return errors.Wrap(err, "identity.Store.Username()")
	}
	if !found {
		return httpio.NewNotFoundMessagef("user id %q does not exist", userID)
	}

	return nil
}
