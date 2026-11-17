package session

import (
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/session/internal/attempts"
)

// ChangeSessionUserPasswordRequest takes in the user information for changing a SessionUser password
type ChangeSessionUserPasswordRequest struct {
	OldPassword string
	NewPassword string
}

// CreateUserRequest takes in the user information for creating a new SessionUser. A nil
// Password creates a password-less user. PasswordChangeRequired marks the password as
// one an operator set: the user changes it before anything else is served (see the
// README's "Users and the forced password change"); it needs a Password. Email is
// the address the operator enters, unverified (see "Users and their email address").
// Name is the person's name as people read it (see "A person's name").
type CreateUserRequest struct {
	// ID is the id the new user is created under, for a seed that must know the id
	// before the user exists: one that names its people in rows of its own (who filed a
	// row, whose record a row is), so a development database reads the same on every
	// machine, and one that gives a user its roles before it creates the user
	// (SeedUserID). Null, as everywhere else, has the library generate one. It is set by
	// Go callers alone and never read from a request body. An id another user holds
	// fails, and so does the nil id.
	ID                     impulse.NullUUID `json:"-"`
	Username               string           `json:"username"`
	Password               *string          `json:"password"`
	Disabled               bool             `json:"disabled"`
	PasswordChangeRequired bool             `json:"passwordChangeRequired"`
	// Email is the user's email address as an operator enters it, unverified: it
	// reserves the address, and a link may be sent to it. Nil for none. When the
	// username is the address (UsernameIsEmail) it is nil or the username.
	Email *string `json:"email"`
	// Name is the person's name as people read it, nil for none: kept without the
	// space around it, and refused when longer than sessioninfo.MaxNameLength.
	Name *string `json:"name"`
}

// seedUserNamespace is the namespace the ids of seeded users are derived in.
var seedUserNamespace = impulse.UUIDFromName(impulse.NilUUID, "github.com/impulseframework/impulse/session: a seeded user")

// SeedUserID returns the id a seed creates a user under: the id of the username within
// the auth named auth, the same on every run, and the same for a username spelled in
// another case, which the users table holds to be the same name. A role is held by the
// id of an account, so a seed that gives a user its roles needs the id before the user
// exists. It writes the roles under this id first and creates the user under it last
// (CreateUserRequest.ID): the user's row then says the seed is done, a run that stopped
// before it is repeated whole, and the same memberships are written again instead of a
// second set under another id. No other user can come to hold the id: every other id is
// generated, and a request body cannot name one.
func SeedUserID(auth, username string) impulse.UUID {
	return impulse.UUIDFromName(seedUserNamespace, auth+"\x00"+attempts.Normalize(username))
}

// seedThroughNamespace is the namespace the ids of seeded users who sign in through a
// directory are derived in, apart from seedUserNamespace: no id of one kind is an id of
// the other.
var seedThroughNamespace = impulse.UUIDFromName(impulse.NilUUID, "github.com/impulseframework/impulse/session: a seeded user who signs in through a registration")

// SeedUserIDThrough returns the id a seed creates a user under who holds no password
// and signs in through a directory: the first administrator of an auth whose roles the
// application gives. It is SeedUserID with the name of the sign-in registration the
// person comes through, so the account's id carries the deployment's word twice: that
// the account is a seed's, which no request can make (CreateUserRequest.ID), and which
// of the Auth's registrations the name is believed from.
//
// The second is what the first sign-in needs. Such an account has no password and no
// link, so nothing but its name ties it to whoever signs in under it, and a directory
// asserts the names it likes. In an application whose tenants have directories the
// standard rule therefore links an account created ahead only when something vouches
// for it (SetDirectoryAdmission), and for this account the deployment does: the Auth's
// directory admission recognizes the id (DirectorySeatedHook.Seeded) and links the
// account at its person's first sign-in through that registration, where the
// registration admits one organization and nobody else. A sign-in under the name
// through any other registration, a customer's own provider among them, or through a
// registration open to more than one organization, is refused as a stranger's is.
func SeedUserIDThrough(auth, registration, username string) impulse.UUID {
	return impulse.UUIDFromName(seedThroughNamespace, auth+"\x00"+registration+"\x00"+attempts.Normalize(username))
}
