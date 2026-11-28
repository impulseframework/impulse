package dbtype

import "testing"

// TestRefusals pins the sentences a person is told when a store refuses what it is asked
// to write, word for word.
func TestRefusals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		constant string
		sentence string
	}{
		{name: "the last sign-in identity", constant: LastSignInIdentityMessage, sentence: "This is the account's only way to sign in. Link another directory sign-in first, or give the account a password."},
		{
			name: "the last sign-in identity that signs in", constant: LastWorkingSignInIdentityMessage,
			sentence: "None of the other sign-in identities linked to the account can sign in any more, so this one is its only way to sign in. Link a directory sign-in the login page offers first, or give the account a password.",
		},
		{name: "the password, the last way to sign in", constant: PasswordIsLastSignInMessage, sentence: "The password is the account's only way to sign in. Link a directory sign-in first."},
		{
			name: "the password, the last way that signs in", constant: PasswordIsLastWorkingSignInMessage,
			sentence: "None of the sign-in identities linked to the account can sign in any more, so the password is its only way to sign in. Link a directory sign-in the login page offers first.",
		},
		{name: "a password changed under a write", constant: PasswordChangedMessage, sentence: "The password has changed since it was checked. Try again."},
		{name: "no password to remove", constant: NoPasswordToRemoveMessage, sentence: "This account has no password to remove."},
		{name: "a password held already", constant: PasswordHeldMessage, sentence: "This account already has a password. It is changed by giving the old one."},
		{name: "a sign-in identity linked already", constant: SignInIdentityAlreadyLinkedMessage, sentence: "This sign-in identity is already linked to an account."},
		{name: "a username taken", constant: UsernameTakenMessage, sentence: "Another account already has this username. Choose another."},
		{name: "an address in use", constant: EmailInUseMessage, sentence: "Another account already uses this email address. Use another address."},
		{name: "a username that is the address", constant: UsernameIsEmailMessage, sentence: "Here the username is the account's email address. It changes when the person verifies a new address, never by hand."},
		{name: "only a directory's identity links", constant: OnlyDirectoryIdentityLinksMessage, sentence: "Only a sign-in identity from a directory, with a subject, can be linked."},
		{name: "an auth event with no method", constant: AuthEventNeedsMethodMessage, sentence: "The auth event names no method. Give it one."},
		{name: "an auth event on an expired session", constant: AuthEventOnExpiredSessionMessage, sentence: "This session has expired, so no auth event is recorded on it."},
		{name: "session data on an expired session", constant: SessionDataOnExpiredSessionMessage, sentence: "This session has expired, so its data cannot be changed."},
		{name: "an empty address", constant: EmptyEmailMessage, sentence: "The email address is empty. Give an address, or leave it out."},
		{name: "an address that is not the username", constant: EmailIsNotUsernameMessage, sentence: "Here the username is the account's email address, and the two given differ. Give the same address for both."},
		{name: "a verified mark with no address", constant: VerifiedWithoutEmailMessage, sentence: "The address is marked as verified, and none is given. Give the address, or leave the mark off."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.constant != tt.sentence {
				t.Errorf("the constant reads %q, want %q", tt.constant, tt.sentence)
			}
		})
	}
}
