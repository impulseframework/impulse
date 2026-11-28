package dbtype

import (
	"crypto/sha256"
	"crypto/subtle"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/securehash"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// SessionIdentity is an identity link row. Its fields match
// sessionstorage.SessionIdentity, which converts from it.
type SessionIdentity struct {
	ID          impulse.UUID           `spanner:"Id"          db:"Id"`
	UserID      impulse.UUID           `spanner:"UserId"      db:"UserId"`
	Method      sessioninfo.AuthMethod `spanner:"Method"      db:"Method"`
	Connection  string                 `spanner:"Connection"  db:"Connection"`
	Subject     string                 `spanner:"Subject"     db:"Subject"`
	Tenant      *string                `spanner:"Tenant"      db:"Tenant"`
	EmailAtLink *string                `spanner:"EmailAtLink" db:"EmailAtLink"`
	CreatedAt   time.Time              `spanner:"CreatedAt"   db:"CreatedAt"`
	LastUsedAt  time.Time              `spanner:"LastUsedAt"  db:"LastUsedAt"`
}

// IsExternal reports whether identity is resolved through the identities table. A
// password identity names its user directly (req.UserID), and an impersonation
// identity belongs to the impersonation flow.
func IsExternal(identity *sessioninfo.Identity) bool {
	return identity != nil && identity.Method != sessioninfo.MethodPassword && identity.Method != sessioninfo.MethodImpersonation &&
		identity.Method != sessioninfo.MethodApplication
}

// ErrLastSignInMethod is returned when UnlinkIdentity would remove the last identity
// that can sign in a user that has no password, and when RemoveUserPassword would
// remove the password of a user none of whose identities can; see sessionstorage for
// its documentation.
var ErrLastSignInMethod = errors.New("sessionstorage: the identity is the user's last means of sign-in")

// UnlinkKeepsSignIn judges the unlink of the identity unlinked from a user that holds
// no password and whose identities are links, as the unlinking transaction read them.
// It is nil while another of them is a means of sign-in by signsIn, the caller's
// judgment of a link by its method and connection, and the refusal otherwise
// (ErrLastSignInMethod, a Conflict): an identity that signs nobody in is no means of
// sign-in, however many of them remain. A nil signsIn judges none to be one.
func UnlinkKeepsSignIn(links []*SessionIdentity, unlinked impulse.UUID, signsIn func(method sessioninfo.AuthMethod, connection string) bool) error {
	others := 0
	for _, link := range links {
		if link.ID == unlinked {
			continue
		}
		if signsIn != nil && signsIn(link.Method, link.Connection) {
			return nil
		}
		others++
	}
	if others > 0 {
		return httpio.NewConflictMessageWithError(ErrLastSignInMethod, LastWorkingSignInIdentityMessage)
	}

	return httpio.NewConflictMessageWithError(ErrLastSignInMethod, LastSignInIdentityMessage)
}

// ErrPasswordChanged is returned when a password write that follows a proof of the
// password finds that the user no longer holds the hash that was proved; see
// sessionstorage for its documentation.
var ErrPasswordChanged = errors.New("sessionstorage: the user's password has changed since it was proved")

// ErrNoAuthEvents is returned when an auth event is appended to a session of a store
// that records none; see sessionstorage for its documentation.
var ErrNoAuthEvents = errors.New("auth events are not configured: attach WithAuthEvents")

// ErrPasswordHeld is returned when a user's first password is written for a user who
// holds a password already; see sessionstorage for its documentation.
var ErrPasswordHeld = errors.New("sessionstorage: the user already holds a password")

// PasswordProof names the password a sign-in proved, without holding it: the SHA-256
// of the password hash the password was compared against, in the text the users table
// stores. Whatever is inserted on the strength of a password just entered (a session, a
// pending sign-in, a confirmed link) carries it, and is inserted only while the user's
// row still answers to it, compared in the inserting transaction (Check): a password
// changed, reset or removed since it was proved gets nothing. A pending sign-in keeps it
// in its row, for its completion to be held the same way; the hash itself is kept
// nowhere but the users table, and its digest says nothing a guess could be tried
// against, since the salt is part of what was digested. The nil proof is the sign-in no
// password proved, which is held to nothing.
type PasswordProof []byte

// ProofOf is the proof of a password that was compared against hash: nil for no hash.
func ProofOf(hash *securehash.Hash) (PasswordProof, error) {
	if hash == nil {
		return nil, nil
	}
	text, err := hash.MarshalText()
	if err != nil {
		return nil, errors.Wrap(err, "securehash.Hash.MarshalText()")
	}

	return ProofOfStored(string(text)), nil
}

// ProofOfStored is the proof a user row answers to whose password hash column holds
// text.
func ProofOfStored(text string) PasswordProof {
	sum := sha256.Sum256([]byte(text))

	return sum[:]
}

// Check answers nil while a user whose row answers to held (User.PasswordProof) still
// holds the password p proved, and ErrPasswordChanged (a Conflict) once the row holds
// another hash, or none. The nil proof proved nothing and is held to nothing.
func (p PasswordProof) Check(held PasswordProof) error {
	if p == nil {
		return nil
	}
	if len(held) == 0 || subtle.ConstantTimeCompare(p, held) != 1 {
		return httpio.NewConflictMessageWithError(ErrPasswordChanged, PasswordChangedMessage)
	}

	return nil
}

// User is the part of a user row the sign-in flow decides on.
type User struct {
	ID          impulse.UUID
	Username    string
	HasPassword bool
	Disabled    bool
	// PasswordProof is the proof the row's password hash answers to (ProofOfStored), nil
	// for a user with no password, as UserIn and LockUserIn read it: what a sign-in's
	// own proof is checked against in the transaction that inserts on its strength.
	PasswordProof PasswordProof
	// EmailConfirmed says the row holds an address its person has confirmed, as UserIn
	// and LockUserIn read it: a sign-in through a directory that does not vouch for the
	// address is held for a mailed code while it is false (signinflow.decideSignIn).
	EmailConfirmed bool
	// EmailHeld says the row holds an address, confirmed or not, as UserIn and
	// LockUserIn read it: a first link made on a name alone waits for the code mailed
	// to it, and is refused where the row holds none (signinflow.nameAloneHold).
	EmailHeld bool
	// Email is the address the row holds, nil for none, and EmailConfirmedAt when its
	// person confirmed it, nil for an address not confirmed and for none, as UserIn and
	// LockUserIn read them: a confirmation by a mailed link is decided on them
	// (signinflow.ConfirmAddressByLink).
	Email            *string
	EmailConfirmedAt *time.Time
}

// CodeAddress is the address a mailed sign-in code confirms, carried by the sign-in that
// completes a wait with the account's own code: it is written on the account, confirmed,
// in the transaction that inserts the session, or with the acceptance of an invitation
// that rests on that code, and only while the account still holds what it held when the
// code was checked (signinflow.decideAndInsert, signinflow.acceptInvitation).
type CodeAddress struct {
	// Held is the address the account held when the code was checked, byte for byte,
	// nil for none.
	Held *string
	// Email is the address the code confirms: Held's own spelling where the account
	// holds an address, else the address the person gave, which the code was mailed to.
	Email string
	// Written is set by the sign-in: the address was written, in the transaction that
	// committed.
	Written bool
}

// SignInConflict is a driver's classification of the error a sign-in's transaction
// ended with.
type SignInConflict int

const (
	// NoSignInConflict is any other error.
	NoSignInConflict SignInConflict = iota
	// UsernameConflict is the users table refusing a provisioned user's username,
	// which another user already has.
	UsernameConflict
	// IdentityConflict is the identities table refusing a link of an identity that a
	// racing sign-in linked meanwhile.
	IdentityConflict
	// LockConflict is the database ending the transaction to break a deadlock or a
	// serialization failure between racing sign-ins; the sign-in runs once more.
	LockConflict
	// EmailConflict is the users table refusing an address another user holds: one that
	// a racing transaction wrote on another account at the same moment.
	EmailConflict
)
