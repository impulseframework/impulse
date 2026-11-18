package staff

import (
	"net/http"

	"github.com/impulseframework/impulse/session"
)

// PasswordRegistration is the name the password method is registered under on the Auth,
// always password: the segment of its login route (user/password/login). A password
// sign-in is checked against the user's hash in the users table; the bootstrap creates
// the development logins with their passwords, and a deployed environment starts from the
// administrator the migrate command seeds (foothold.go), the rest coming from its
// administration surface, from the invitations it mails and, while an administrator has
// the method open to sign-up, from the strangers who sign up from a mailed link.
const PasswordRegistration = "password"

// PasswordLogin checks a username and password and starts the session.
func (h *Handlers) PasswordLogin() http.HandlerFunc {
	return h.password.Login()
}

// PasswordChange changes the signed-in user's password, given the old one.
func (h *Handlers) PasswordChange() http.HandlerFunc {
	return h.password.ChangeUserPassword()
}

// PasswordRemove removes the signed-in user's password, given the password, once they
// sign in through a directory; a user with no other way in keeps it.
func (h *Handlers) PasswordRemove() http.HandlerFunc {
	return h.password.RemoveUserPassword()
}

// FirstPasswordSendCode mails the signed-in person who has no password, because they
// have only ever signed in through a directory, the code they choose their first one
// with, at the address their account holds confirmed. A person at a browser someone
// else left signed in does not read that mailbox, so they add no password to the
// account.
func (h *Handlers) FirstPasswordSendCode() http.HandlerFunc {
	return h.FirstPassword().SendCode()
}

// FirstPasswordSet gives the signed-in person who has no password the one they choose,
// with the code they were mailed. An account that has a password changes it with the
// old one (PasswordChange).
func (h *Handlers) FirstPasswordSet() http.HandlerFunc {
	return h.FirstPassword().Set()
}

// InvitationSignUp accepts an invitation as a stranger with a new password: the invitee
// becomes a user under the sign-in name they give, with the
// address verified by the link, seated in the roles the invitation names.
func (h *Handlers) InvitationSignUp() http.HandlerFunc {
	return h.Invitation().SignUp()
}

// SignUpStart mails a sign-up link to the address a stranger gives; an address already
// held gets a notice instead, and the caller learns neither. The three sign-up routes
// answer 404 while the password method is invite only, which it is until an
// administrator opens it to sign-up on the application's Sign-in page.
func (h *Handlers) SignUpStart() http.HandlerFunc {
	return h.SignUp().Start()
}

// SignUpOpen reads the sign-up a mailed link carries, for the login page to offer the
// form.
func (h *Handlers) SignUpOpen() http.HandlerFunc {
	return h.SignUp().Open()
}

// SignUpComplete creates the user from the link, with the password they choose and the
// address verified, and starts their session.
func (h *Handlers) SignUpComplete() http.HandlerFunc {
	return h.SignUp().Complete()
}

// registerPassword registers the password method on the Auth, before the first request.
// Whether a stranger may sign up through it is no option here: the method is invite
// only until an administrator opens it to sign-up in the running application.
func registerPassword(authSession *Session) *session.PasswordMethod {
	return session.PasswordSignIn(authSession)
}
