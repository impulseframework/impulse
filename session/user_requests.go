package session

import (
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/session/internal/attempts"
	"github.com/impulseframework/impulse/session/internal/passwordstrength"
)

// ChangeSessionUserPasswordRequest takes in the user information for changing a SessionUser password
type ChangeSessionUserPasswordRequest struct {
	OldPassword string
	NewPassword string
}

// CreateUserRequest takes in the user information for creating a new SessionUser. A nil
// Password creates a password-less user. Password is the seam for a seed or an import,
// which creates an account that already holds a password: a person's own account is
// made by an invitation or a sign-up, where the person chooses theirs. Email is
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
	ID       impulse.NullUUID `json:"-"`
	Username string           `json:"username"`
	Password *string          `json:"password"`
	Disabled bool             `json:"disabled"`
	// Email is the user's email address as an operator enters it, unverified: it
	// reserves the address, and a link may be sent to it. Nil for none. When the
	// username is the address (UsernameIsEmail) it is nil or the username.
	Email *string `json:"email"`
	// Name is the person's name as people read it, nil for none: kept without the
	// space around it, and refused when longer than sessioninfo.MaxNameLength.
	Name *string `json:"name"`
}

// details are what the request says of the person, which a password it carries is judged
// against: the username, the name and the address, "" for one it gives none of.
func (r *CreateUserRequest) details() passwordstrength.Details {
	who := passwordstrength.Details{Username: r.Username}
	if r.Name != nil {
		who.Name = *r.Name
	}
	if r.Email != nil {
		who.Email = *r.Email
	}

	return who
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
