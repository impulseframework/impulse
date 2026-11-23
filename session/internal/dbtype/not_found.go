package dbtype

import (
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// The sentences a person is told when what a store is asked for is not there. None
// carries an id, a username or an address: the refusal's cause keeps it, for the
// server's log.
const (
	// AccountNotFoundMessage is said of an account no user id names.
	AccountNotFoundMessage = "This account does not exist."

	// UsernameNotFoundMessage is said of a username no account has.
	UsernameNotFoundMessage = "No account has this username."

	// EmailNotFoundMessage is said of an email address no account has.
	EmailNotFoundMessage = "No account has this email address."

	// SessionNotFoundMessage is said of a session no session id names.
	SessionNotFoundMessage = "This session does not exist."

	// SignInIdentityNotFoundMessage is said of a sign-in identity no identity id names.
	SignInIdentityNotFoundMessage = "This sign-in identity does not exist."

	// SignInIdentityNotLinkedMessage is said of a sign-in identity, named by its method,
	// connection and subject, that no link joins to an account.
	SignInIdentityNotLinkedMessage = "This sign-in identity is not linked to an account."
)

// AccountNotFound is the answer for an account that is not there: 404 with
// AccountNotFoundMessage. The account's id stays in the cause, for the server's log.
func AccountNotFound(userID impulse.UUID) error {
	return httpio.NewNotFoundMessageWithError(errors.Newf("user id %q does not exist", userID), AccountNotFoundMessage)
}

// UsernameNotFound is the answer for a username no account has: 404 with
// UsernameNotFoundMessage. The username stays in the cause, for the server's log.
func UsernameNotFound(username string) error {
	return httpio.NewNotFoundMessageWithError(errors.Newf("username %q does not exist", username), UsernameNotFoundMessage)
}

// EmailNotFound is the answer for an email address no account has: 404 with
// EmailNotFoundMessage. The address stays in the cause, for the server's log.
func EmailNotFound(email string) error {
	return httpio.NewNotFoundMessageWithError(errors.Newf("no user holds the email address %q", email), EmailNotFoundMessage)
}

// SessionNotFound is the answer for a session that is not there: 404 with
// SessionNotFoundMessage. The session's id stays in the cause, for the server's log.
func SessionNotFound(sessionID impulse.UUID) error {
	return httpio.NewNotFoundMessageWithError(errors.Newf("session %q not found", sessionID), SessionNotFoundMessage)
}

// SignInIdentityNotFound is the answer for a sign-in identity that is not there: 404
// with SignInIdentityNotFoundMessage. The identity's id stays in the cause, for the
// server's log.
func SignInIdentityNotFound(identityID impulse.UUID) error {
	return httpio.NewNotFoundMessageWithError(errors.Newf("identity %q does not exist", identityID), SignInIdentityNotFoundMessage)
}

// SignInIdentityNotLinked is the answer for a sign-in identity that is linked to no
// account: 404 with SignInIdentityNotLinkedMessage. Its method, connection and subject
// stay in the cause, for the server's log.
func SignInIdentityNotLinked(method sessioninfo.AuthMethod, connection, subject string) error {
	return httpio.NewNotFoundMessageWithError(errors.Newf("identity (%s, %q, %q) is not linked", method, connection, subject), SignInIdentityNotLinkedMessage)
}
