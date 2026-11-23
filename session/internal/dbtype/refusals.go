package dbtype

// The sentences a person is told when a store refuses what it is asked to write. None
// carries an id, a username, an address or a directory's subject: the refusal's cause
// keeps it, for the server's log.
const (
	// LastSignInIdentityMessage is said of the unlink of the one sign-in identity linked
	// to an account that has no password.
	LastSignInIdentityMessage = "This is the account's only way to sign in. Link another directory sign-in first, or give the account a password."

	// LastWorkingSignInIdentityMessage is said of the unlink of a sign-in identity from an
	// account that has no password, where none of the other identities linked to it can
	// sign in any more.
	LastWorkingSignInIdentityMessage = "None of the other sign-in identities linked to the account can sign in any more, so this one is its only way to sign in. " +
		"Link a directory sign-in the login page offers first, or give the account a password."

	// PasswordIsLastSignInMessage is said of the removal of the password of an account no
	// sign-in identity is linked to.
	PasswordIsLastSignInMessage = "The password is the account's only way to sign in. Link a directory sign-in first."

	// PasswordIsLastWorkingSignInMessage is said of the removal of the password of an
	// account none of whose linked sign-in identities can sign in any more.
	PasswordIsLastWorkingSignInMessage = "None of the sign-in identities linked to the account can sign in any more, so the password is its only way to sign in. " +
		"Link a directory sign-in the login page offers first."

	// PasswordChangedMessage is said of a password write that follows a proof of the
	// password, where the account no longer holds the password that was proved.
	PasswordChangedMessage = "The password has changed since it was checked. Try again."

	// NoPasswordToRemoveMessage is said of the removal of a password from an account that
	// has none.
	NoPasswordToRemoveMessage = "This account has no password to remove."

	// PasswordHeldMessage is said of a first password given to an account that has one
	// already.
	PasswordHeldMessage = "This account already has a password. It is changed by giving the old one."

	// SignInIdentityAlreadyLinkedMessage is said of a link of a sign-in identity, named by
	// its method, connection and subject, that a link joins to an account already.
	SignInIdentityAlreadyLinkedMessage = "This sign-in identity is already linked to an account."

	// UsernameTakenMessage is said of a username another account has.
	UsernameTakenMessage = "Another account already has this username. Choose another."

	// EmailInUseMessage is said of an email address another account uses.
	EmailInUseMessage = "Another account already uses this email address. Use another address."

	// UsernameIsEmailMessage is said of a rename on a store whose usernames are the
	// accounts' email addresses.
	UsernameIsEmailMessage = "Here the username is the account's email address. It changes when the person verifies a new address, never by hand."

	// OnlyDirectoryIdentityLinksMessage is said of a link of a sign-in identity that is not
	// a directory's, or that carries no subject.
	OnlyDirectoryIdentityLinksMessage = "Only a sign-in identity from a directory, with a subject, can be linked."

	// AuthEventNeedsMethodMessage is said of an auth event that names no method.
	AuthEventNeedsMethodMessage = "The auth event names no method. Give it one."

	// AuthEventOnExpiredSessionMessage is said of an auth event recorded on a session
	// that has expired.
	AuthEventOnExpiredSessionMessage = "This session has expired, so no auth event is recorded on it."

	// SessionDataOnExpiredSessionMessage is said of a change to the custom data of a
	// session that has expired.
	SessionDataOnExpiredSessionMessage = "This session has expired, so its data cannot be changed."

	// EmptyEmailMessage is said of a new account whose email address is given and empty.
	EmptyEmailMessage = "The email address is empty. Give an address, or leave it out."

	// EmailIsNotUsernameMessage is said of a new account, on a store whose usernames are
	// the accounts' email addresses, whose email address is not its username.
	EmailIsNotUsernameMessage = "Here the username is the account's email address, and the two given differ. Give the same address for both."

	// VerifiedWithoutEmailMessage is said of a new account marked as having a verified
	// address that gives none.
	VerifiedWithoutEmailMessage = "The address is marked as verified, and none is given. Give the address, or leave the mark off."
)
