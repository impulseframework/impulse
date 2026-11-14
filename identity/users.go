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
	"github.com/impulseframework/impulse/tracer"
)

// NewAccount is a person to create: who they are, and the roles they hold from the
// start.
type NewAccount struct {
	Username string
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
// temporary password, which the person changes before anything else is served. It
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
// library writes one (its address function); a value that is no address is refused.
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
	roles := distinctRoles(account.Roles)
	if len(roles) == 0 {
		return impulse.NilUUID, httpio.NewBadRequestMessage("at least one role is required: an account that holds no role anywhere is not kept, so none is created without one")
	}
	hash, err := m.temporaryPassword(account)
	if err != nil {
		return impulse.NilUUID, err
	}

	id, err := m.store.InsertUser(ctx, txn, &NewUser{Username: username, Email: bare})
	if err != nil {
		return impulse.NilUUID, errors.Wrap(err, "identity.Store.InsertUser()")
	}
	if hash != "" {
		if err := m.store.SetPassword(ctx, txn, id, hash, true); err != nil {
			return impulse.NilUUID, errors.Wrap(err, "identity.Store.SetPassword()")
		}
	}
	for _, role := range roles {
		if err := m.AssignRole(ctx, txn, account.Scope, username, role); err != nil {
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
// fact. Nobody disables themselves: the request's caller is refused their own account.
//
// Shared rule: disabling a user ends their sessions, which the session library's
// DeactivateSessionUser does in two commits of its own.
func (m *Manager) DisableUser(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := m.refuseOwnAccount(ctx, txn, userID, "disable"); err != nil {
		return err
	}
	// The sessions are read before anything is buffered: they are the ones live now.
	if err := m.endUserSessions(ctx, txn, userID); err != nil {
		return err
	}
	disabled := true
	if err := m.store.UpdateUser(ctx, txn, userID, &UserChange{Username: nil, Disabled: &disabled, PasswordChangeRequired: nil, Email: nil}); err != nil {
		return errors.Wrap(err, "identity.Store.UpdateUser()")
	}

	return nil
}

// EnableUser buffers the enabling of a disabled user, who may sign in again once it
// commits. The sessions that ended when the user was disabled stay ended.
func (m *Manager) EnableUser(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	disabled := false
	if err := m.store.UpdateUser(ctx, txn, userID, &UserChange{Username: nil, Disabled: &disabled, PasswordChangeRequired: nil, Email: nil}); err != nil {
		return errors.Wrap(err, "identity.Store.UpdateUser()")
	}

	return nil
}

// RequirePasswordChange buffers the requirement that a user change their password: once
// it commits, every request of theirs is refused, on the sessions they hold and on new
// ones, until they have changed it. The way out is theirs alone and takes the password
// they hold, so the requirement is refused for a user who holds none: nothing they could
// do would lift it.
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
	if !has {
		return httpio.NewBadRequestMessage("a password change can only be required of a user that has a password")
	}
	required := true
	if err := m.store.UpdateUser(ctx, txn, userID, &UserChange{Username: nil, Disabled: nil, PasswordChangeRequired: &required, Email: nil}); err != nil {
		return errors.Wrap(err, "identity.Store.UpdateUser()")
	}

	return nil
}

// SetEmail buffers an address on a user, entered by hand, or takes the address off
// (nil): an operator's entry, for a person whose address changed or who was created
// without one. The address is held unverified, whatever the one before it was, and is
// trusted for nothing until the person verifies it themselves; it reserves the address
// all the same, so one another user holds is refused by the table's unique index when
// the transaction commits.
//
// It is for an auth whose usernames are not the addresses: where they are, the address
// is the username, and it changes when the person verifies a new one.
//
// Shared rule: the session library's SetSessionUserEmail, which writes the address bare
// and clears the time it was verified.
func (m *Manager) SetEmail(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID, email *string) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	bare, err := optionalAddress(email)
	if err != nil {
		return err
	}
	if _, found, err := m.store.Username(ctx, txn, userID); err != nil {
		return errors.Wrap(err, "identity.Store.Username()")
	} else if !found {
		return httpio.NewNotFoundMessagef("user id %q does not exist", userID)
	}
	if err := m.store.UpdateUser(ctx, txn, userID, &UserChange{Username: nil, Disabled: nil, PasswordChangeRequired: nil, Email: &EmailChange{Address: bare}}); err != nil {
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
