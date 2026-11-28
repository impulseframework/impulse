package dbtype

import (
	"strings"
	"testing"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// TestNotFound pins what both stores answer for what they are asked for and do not hold:
// a 404 whose message is one sentence that says what is missing and carries none of
// what was asked for, and whose cause keeps it, for the server's log.
func TestNotFound(t *testing.T) {
	t.Parallel()

	id := impulse.Must(impulse.NewUUID())

	tests := []struct {
		name     string
		err      error
		sentence string
		// kept is in the cause, and not in the message: what was asked for, or the store's
		// own words for what is missing.
		kept []string
	}{
		{name: "an account", err: AccountNotFound(id), sentence: "This account does not exist.", kept: []string{id.String()}},
		{name: "a username", err: UsernameNotFound("dana.r"), sentence: "No account has this username.", kept: []string{"dana.r"}},
		{name: "an address", err: EmailNotFound("dana@example.com"), sentence: "No account has this email address.", kept: []string{"dana@example.com"}},
		{name: "a session", err: SessionNotFound(id), sentence: "This session does not exist.", kept: []string{id.String()}},
		{name: "a sign-in identity", err: SignInIdentityNotFound(id), sentence: "This sign-in identity does not exist.", kept: []string{id.String()}},
		{
			name: "a sign-in identity that is linked to no account", err: SignInIdentityNotLinked(sessioninfo.MethodWorkOS, "conn_lakeside", "subject-41"),
			sentence: "This sign-in identity is not linked to an account.", kept: []string{string(sessioninfo.MethodWorkOS), "conn_lakeside", "subject-41"},
		},
		{name: "a token", err: TokenNotFound(), sentence: "This token is already revoked or expired, or it does not exist.", kept: []string{"no live token"}},
		{name: "a refresh token", err: RefreshTokenNotFound(), sentence: "This refresh token is no longer valid. Sign in again.", kept: []string{"no live refresh token"}},
		{name: "a device code no device is waiting on", err: DeviceCodeNotPending(), sentence: "No device is waiting for this code. Check the code, or start again on the device.", kept: []string{"no pending device code"}},
		{
			name: "a device code that cannot be redeemed", err: DeviceCodeNotRedeemable(),
			sentence: "This device code is unknown or was already used. Start again on the device.", kept: []string{"no device code to redeem"},
		},
		{name: "a mailed link", err: MailLinkNotLive(), sentence: "That link has expired or was already used. Ask for a new one.", kept: []string{"no live mail link"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if !httpio.HasNotFound(tt.err) {
				t.Fatalf("error = %v, want a NotFound", tt.err)
			}
			messages := httpio.Messages(tt.err)
			if len(messages) != 1 || messages[0] != tt.sentence {
				t.Errorf("the person is told %q, want only %q", messages, tt.sentence)
			}
			for _, word := range tt.kept {
				if strings.Contains(strings.Join(messages, " "), word) {
					t.Errorf("the person is told %q, which carries %q", messages, word)
				}
				if !strings.Contains(tt.err.Error(), word) {
					t.Errorf("the error's text, which the server's log prints, is %q: it does not carry %q", tt.err.Error(), word)
				}
			}
		})
	}
}
