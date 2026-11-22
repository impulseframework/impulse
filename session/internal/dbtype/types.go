// Package dbtype contains types used by the database driver packages for session storage.
package dbtype

import (
	"strings"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/securehash"
	"github.com/impulseframework/impulse/session/internal/util"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// Session defines the structure for storing session data in the database.
type Session struct {
	ID        impulse.UUID `spanner:"Id"        db:"Id"`
	Username  string       `spanner:"Username"  db:"Username"`
	CreatedAt time.Time    `spanner:"CreatedAt" db:"CreatedAt"`
	UpdatedAt time.Time    `spanner:"UpdatedAt" db:"UpdatedAt"`
	Expired   bool         `spanner:"Expired"   db:"Expired"`
}

// SessionData pairs a Session with optional custom session data and, for an
// impersonated session, its impersonation record (nil otherwise). UserID and
// AuthenticatedAt are set only when the user columns are enabled, and AuthEvents
// only when there is an auth events table.
type SessionData struct {
	*Session
	CustomData      any
	Impersonation   *Impersonation
	UserID          impulse.NullUUID
	AuthenticatedAt *time.Time
	// ProviderSessionID is the provider's session id the sign-in carried, "" when none,
	// and ProviderSessionIssuer the issuer it belongs to.
	ProviderSessionID     string
	ProviderSessionIssuer string
	AuthEvents            []sessioninfo.AuthEvent
}

// InsertSession is the caller-supplied part of a new session row. The driver writes
// CreatedAt and UpdatedAt with the database's time at commit, never the application's.
type InsertSession struct {
	Username string `spanner:"Username"`
	Expired  bool   `spanner:"Expired"`
}

// SessionUser is a person authorized to access the application. PasswordChangeRequired
// marks a user an administrator required a new password of: the user changes it before
// anything else.
// CreatedAt is the database's time at the insert and LastSignInAt its time at the
// user's latest sign-in, nil before the first.
// Email is the user's email address, nil for none, and EmailVerifiedAt the database's
// time at which the person proved they read it, nil while it is unverified; both are
// nil on a store without the auth schema. An unverified address is trusted for
// nothing: it may be sent a link, and that is all. Name is the person's name as people
// read it ("Gale Reyes"), nil for none and on a store without the auth schema: what a
// directory asserted at a sign-in, an inviter typed, or the person wrote themselves. It
// identifies nobody: the username does.
type SessionUser struct {
	ID                     impulse.UUID     `spanner:"Id"                     db:"Id"`
	Username               string           `spanner:"Username"               db:"Username"`
	PasswordHash           *securehash.Hash `spanner:"PasswordHash"           db:"PasswordHash"`
	Disabled               bool             `spanner:"Disabled"               db:"Disabled"`
	PasswordChangeRequired bool             `spanner:"PasswordChangeRequired" db:"PasswordChangeRequired"`
	CreatedAt              time.Time        `spanner:"CreatedAt"              db:"CreatedAt"`
	LastSignInAt           *time.Time       `spanner:"LastSignInAt"           db:"LastSignInAt"`
	Email                  *string          `spanner:"Email"                  db:"Email"`
	EmailVerifiedAt        *time.Time       `spanner:"EmailVerifiedAt"        db:"EmailVerifiedAt"`
	Name                   *string          `spanner:"Name"                   db:"Name"`
	LookScheme             *string          `spanner:"LookScheme"             db:"LookScheme"`
	LookPalette            *string          `spanner:"LookPalette"            db:"LookPalette"`
}

// EmailVerified reports whether the user holds a verified address.
func (u *SessionUser) EmailVerified() bool {
	return u != nil && u.Email != nil && u.EmailVerifiedAt != nil
}

// Look is the look the person chose, the zero value for none: the two columns the row
// keeps it in, each empty where the row holds null.
func (u *SessionUser) Look() sessioninfo.Look {
	var look sessioninfo.Look
	if u == nil {
		return look
	}
	if u.LookScheme != nil {
		look.Scheme = *u.LookScheme
	}
	if u.LookPalette != nil {
		look.Palette = *u.LookPalette
	}

	return look
}

// InsertSessionUser is the caller-supplied part of a new user row. The driver writes
// CreatedAt with the database's time at commit. Email, nil for none, is the user's
// address, written unverified unless EmailVerified says the person has proven it (a
// sign-up or an invitation the person completed by mail), in which case the driver
// stamps EmailVerifiedAt with the commit time. On a store whose usernames are the
// addresses (UsernameIsEmail) the address is the username and Email must be nil or
// match it. Name, nil for none, is the person's name, written as CleanName leaves it.
// A user is created under no password change mark: the drivers write it false, and
// the application writes it when an administrator requires a new password.
type InsertSessionUser struct {
	// ID is the id the user is created under when the caller chose one; null has the
	// driver generate it.
	ID            impulse.NullUUID `spanner:"-"            db:"-"`
	Username      string           `spanner:"Username"     db:"Username"`
	PasswordHash  *securehash.Hash `spanner:"PasswordHash" db:"PasswordHash"`
	Disabled      bool             `spanner:"Disabled"     db:"Disabled"`
	Email         *string          `spanner:"Email"        db:"Email"`
	EmailVerified bool             `spanner:"-"            db:"-"`
	Name          *string          `spanner:"Name"         db:"Name"`
}

// SessionIDColumn is the primary-key column of the custom session data table, referencing the session table's primary key.
const SessionIDColumn = "SessionId"

// UserIDColumn is the primary-key column of the custom user data table, referencing the
// user table's primary key (SessionUsers.Id).
const UserIDColumn = "UserId"

// IsReservedCustomColumn checks if a given column name is reserved and therefore cannot be used as a custom session data column name.
func IsReservedCustomColumn(name string) bool {
	return name == SessionIDColumn
}

// IsReservedCustomUserColumn checks if a given column name is reserved and therefore cannot be used as a custom user data column name.
func IsReservedCustomUserColumn(name string) bool {
	return name == UserIDColumn
}

// ValidateInsertUser refuses a new user row the store cannot write: on a store whose
// usernames are the addresses (usernameIsEmail), an Email that is not the username,
// since the database derives the address from the username; a verified mark
// without an address; and a name CleanName refuses.
func ValidateInsertUser(user *InsertSessionUser, usernameIsEmail bool) error {
	if _, err := sessioninfo.CleanName(user.Name); err != nil {
		return errors.Wrap(err, "sessioninfo.CleanName()")
	}
	switch {
	case user.Email != nil && strings.TrimSpace(*user.Email) == "":
		return httpio.NewBadRequestMessage("an email address, when given, is not empty")
	case usernameIsEmail && user.Email != nil && !util.EqualASCIIFold(strings.TrimSpace(*user.Email), strings.TrimSpace(user.Username)):
		return httpio.NewBadRequestMessagef("the username is the user's email address: %q is not the username %q", *user.Email, user.Username)
	case user.EmailVerified && user.Email == nil && !usernameIsEmail:
		return httpio.NewBadRequestMessage("a verified mark needs an address")
	}

	return nil
}
