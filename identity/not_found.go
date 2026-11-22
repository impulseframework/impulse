package identity

import (
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
)

// The sentences a person is told when what they act on is not there: each says the thing
// in the word its list uses, and none carries an id, an auth's name or a sign-in
// method's, which the refusal's cause keeps for the server's log.
const (
	// accountNotFoundMessage is said of an account, in the whole application's pages and
	// in a tenant's alike: a tenant's method answers the same for a person of another
	// tenant as for nobody at all.
	accountNotFoundMessage = "This account does not exist."

	// groupNotFoundMessage is said of a group, and of a group kept in another tenant.
	groupNotFoundMessage = "This group does not exist."

	// signInIdentityNotFoundMessage is said of an identity a directory verified for a
	// person.
	signInIdentityNotFoundMessage = "This sign-in identity does not exist."

	// invitationNotFoundMessage is said of an invitation, and of an invitation that makes
	// a tenant where an ordinary one is withdrawn, so the grant that withdraws an
	// ordinary invitation does not learn that one stands.
	invitationNotFoundMessage = "This invitation does not exist."

	// invitationNoLongerStandsMessage is said of an invitation that was there and is
	// dead: it tells an administrator why the row they pressed is.
	invitationNoLongerStandsMessage = "This invitation no longer stands: it was accepted, withdrawn or has expired."

	// sessionNotFoundMessage is said of a session.
	sessionNotFoundMessage = "This session does not exist."

	// impersonationNotFoundMessage is said of a session that was established as nobody
	// else, where an impersonation is ended.
	impersonationNotFoundMessage = "This impersonation does not exist."

	// tokenNotFoundMessage is said of a token that is revoked, expired or not there, the
	// three alike: the list keeps revoked tokens, so the likely case comes first.
	tokenNotFoundMessage = "This token is already revoked or expired, or it does not exist."

	// signInMethodNotFoundMessage is said of a sign-in method the auth does not register.
	signInMethodNotFoundMessage = "This way to sign in does not exist."
)

// accountNotFound is the answer for an account that is not there: 404 with
// accountNotFoundMessage. The account's id stays in the cause, for the server's log.
func accountNotFound(userID impulse.UUID) error {
	return httpio.NewNotFoundMessageWithError(errors.Newf("user id %q does not exist", userID), accountNotFoundMessage)
}

// groupNotFound is the answer for a group that is not there: 404 with
// groupNotFoundMessage. The group's id stays in the cause, for the server's log.
func groupNotFound(groupID impulse.UUID) error {
	return httpio.NewNotFoundMessageWithError(errors.Newf("group id %q does not exist", groupID), groupNotFoundMessage)
}

// signInIdentityNotFound is the answer for a sign-in identity that is not there: 404
// with signInIdentityNotFoundMessage. The identity's id stays in the cause, for the
// server's log.
func signInIdentityNotFound(identityID impulse.UUID) error {
	return httpio.NewNotFoundMessageWithError(errors.Newf("identity %q does not exist", identityID), signInIdentityNotFoundMessage)
}

// invitationNotFound is the answer for an invitation that is not there: 404 with
// invitationNotFoundMessage. The invitation's id stays in the cause, for the server's
// log.
func invitationNotFound(invitationID impulse.UUID) error {
	return httpio.NewNotFoundMessageWithError(errors.Newf("invitation %q does not exist", invitationID), invitationNotFoundMessage)
}

// invitationNoLongerStands is the answer for an invitation that was accepted, withdrawn
// or has expired: 404 with invitationNoLongerStandsMessage. cause says which invitation
// it was, with its id, for the server's log.
func invitationNoLongerStands(cause error) error {
	return httpio.NewNotFoundMessageWithError(cause, invitationNoLongerStandsMessage)
}

// sessionNotFound is the answer for a session that is not there: 404 with
// sessionNotFoundMessage. The session's id stays in the cause, for the server's log.
func sessionNotFound(sessionID impulse.UUID) error {
	return httpio.NewNotFoundMessageWithError(errors.Newf("session %q does not exist", sessionID), sessionNotFoundMessage)
}

// impersonationNotFound is the answer for a session that has no impersonation record:
// 404 with impersonationNotFoundMessage. The session's id stays in the cause, for the
// server's log.
func impersonationNotFound(sessionID impulse.UUID) error {
	return httpio.NewNotFoundMessageWithError(errors.Newf("session %q has no impersonation record", sessionID), impersonationNotFoundMessage)
}

// tokenNotFound is the answer for a token that is revoked, expired or not there: 404 with
// tokenNotFoundMessage. The token's id stays in the cause, for the server's log.
func tokenNotFound(tokenID impulse.UUID) error {
	return httpio.NewNotFoundMessageWithError(errors.Newf("no live token %q", tokenID), tokenNotFoundMessage)
}

// signInMethodNotFound is the answer for a sign-in method the auth registers under no
// name of the request's: 404 with signInMethodNotFoundMessage. The auth's name, the name
// asked for and the names the auth registers stay in the cause, for the server's log.
func signInMethodNotFound(auth, name string, methods []string) error {
	return httpio.NewNotFoundMessageWithError(
		errors.Newf("the %s auth has no sign-in method named %q: its methods are %s", auth, name, strings.Join(methods, ", ")),
		signInMethodNotFoundMessage,
	)
}
