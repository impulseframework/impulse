package dbtype

import (
	"time"

	"github.com/impulseframework/impulse"
)

// FactorCode is a row of the factor codes table: one code the library mailed for a
// pending sign-in's second factor. The mail carries the code and the row keeps its
// hash, salted by the pending sign-in it belongs to. It is live while it is not
// consumed and not expired; Attempts counts the guesses made at it, each counted
// before it is judged, and the Auth consumes it at the limit and judges no guess past
// it, so a code is guessed at most that many times, however the guesses arrive. A
// new code for the same pending sign-in consumes the earlier one. CreatedAt and
// ConsumedAt are the database's time; ExpiresAt is the library's deadline, written as
// given.
type FactorCode struct {
	ID impulse.UUID
	// PendingID is the pending sign-in the code was sent for.
	PendingID impulse.UUID
	// Email is the address the code was mailed to.
	Email string
	// InvitationID is the invitation the code was mailed for, null for every other code:
	// an account's own sign-in code, and a code mailed to an address a person gave. A code
	// mailed for an invitation accepts that invitation and nothing else: it is judged only
	// in a browser that carries that invitation, the codes entered for it are counted
	// against that invitation, and it is no account's second factor.
	InvitationID impulse.NullUUID
	CodeHash     []byte
	Attempts     int
	CreatedAt    time.Time
	ExpiresAt    time.Time
	ConsumedAt   *time.Time
}

// TrustedDevice is a row of the trusted devices table: a browser the person asked to
// be remembered after a second factor, which then stands in for the factor at the
// person's next sign-ins until it expires or is revoked. The browser holds an opaque
// token in the trusted device cookie and the row keeps its hash. It is live while it is
// not revoked and not expired. CreatedAt, LastUsedAt and RevokedAt are the database's
// time; ExpiresAt is the library's deadline, written as given.
type TrustedDevice struct {
	ID         impulse.UUID
	UserID     impulse.UUID
	TokenHash  []byte
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}
