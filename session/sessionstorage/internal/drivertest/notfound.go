package drivertest

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/internal/dbtype"
)

// notFoundInWords fails unless err is a NotFound whose one message is sentence and
// carries none of asked, which the error's text, the server's log, keeps.
func notFoundInWords(t *testing.T, what string, err error, sentence string, asked ...string) {
	t.Helper()

	if !httpio.HasNotFound(err) {
		t.Fatalf("%s error = %v, want a NotFound saying %q", what, err, sentence)
	}
	messages := httpio.Messages(err)
	if len(messages) != 1 || messages[0] != sentence {
		t.Errorf("%s: the person is told %q, want only %q", what, messages, sentence)
	}
	for _, word := range asked {
		if strings.Contains(strings.Join(messages, " "), word) {
			t.Errorf("%s: the person is told %q, which carries %q", what, messages, word)
		}
		if !strings.Contains(err.Error(), word) {
			t.Errorf("%s: the error's text, which the server's log prints, is %q: it does not carry %q", what, err.Error(), word)
		}
	}
}

// testNotFoundInWords holds a driver to what it answers for an account, a session, a
// sign-in identity, a token, a device code or a mailed link it does not hold: a
// NotFound in one plain sentence, the same on both databases, that carries none of what
// it was asked for (an id, a username, an address, a directory's subject), which the
// refusal's cause keeps for the server's log. A token, a device code or a link is asked
// for by a secret or by an id of its own, which neither the sentence nor the cause holds.
func testNotFoundInWords(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true})
	nobody := impulse.Must(impulse.NewUUID())

	t.Run("an account", func(t *testing.T) {
		_, err := in.Driver.User(ctx, nobody)
		notFoundInWords(t, "User() of an id no account has", err, dbtype.AccountNotFoundMessage, nobody.String())
	})
	t.Run("a username", func(t *testing.T) {
		_, err := in.Driver.UserByUserName(ctx, "nobody.here")
		notFoundInWords(t, "UserByUserName() of a username no account has", err, dbtype.UsernameNotFoundMessage, "nobody.here")
	})
	t.Run("an email address", func(t *testing.T) {
		_, err := in.Driver.UserByEmail(ctx, "nobody.here@lakeside.edu")
		notFoundInWords(t, "UserByEmail() of an address no account has", err, dbtype.EmailNotFoundMessage, "nobody.here@lakeside.edu")
	})
	t.Run("a session", func(t *testing.T) {
		_, err := in.Driver.Session(ctx, nobody)
		notFoundInWords(t, "Session() of an id no session has", err, dbtype.SessionNotFoundMessage, nobody.String())
	})
	t.Run("a sign-in identity", func(t *testing.T) {
		err := in.Driver.UnlinkIdentity(ctx, nobody, nil, nil)
		notFoundInWords(t, "UnlinkIdentity() of an id no identity has", err, dbtype.SignInIdentityNotFoundMessage, nobody.String())
	})
	t.Run("a sign-in identity that is linked to no account", func(t *testing.T) {
		unlinked := lakeside("subject-nobody")
		_, err := in.Driver.Identity(ctx, unlinked.Method, unlinked.Connection, unlinked.Subject)
		notFoundInWords(t, "Identity() of an identity no link holds", err, dbtype.SignInIdentityNotLinkedMessage, unlinked.Connection, unlinked.Subject)
	})
	t.Run("a token", func(t *testing.T) {
		_, err := in.Driver.TokenByHash(ctx, tokenHash(9), time.Now())
		notFoundInWords(t, "TokenByHash() of a hash no token has", err, dbtype.TokenNotFoundMessage)
		err = in.Driver.RevokeUserToken(ctx, nobody, nobody, time.Now(), dbtype.TokenRevokedByHand, nil)
		notFoundInWords(t, "RevokeUserToken() of an id no token has", err, dbtype.TokenNotFoundMessage)
	})
	t.Run("a refresh token", func(t *testing.T) {
		access, refresh := newFamily(nobody, tokenHash(10), tokenHash(11))
		err := in.Driver.ExchangeRefreshToken(ctx, tokenHash(9), time.Now(), access, refresh)
		notFoundInWords(t, "ExchangeRefreshToken() of a hash no token has", err, dbtype.RefreshTokenNotFoundMessage)
	})
	t.Run("a device code", func(t *testing.T) {
		_, err := in.Driver.DeviceCodeByUserCode(ctx, "NOBODY42", time.Now())
		notFoundInWords(t, "DeviceCodeByUserCode() of a code nobody was given", err, dbtype.DeviceCodeNotPendingMessage)
		err = in.Driver.ApproveDeviceCode(ctx, nobody, nobody, time.Now())
		notFoundInWords(t, "ApproveDeviceCode() of an id no code has", err, dbtype.DeviceCodeNotPendingMessage)
		err = in.Driver.DenyDeviceCode(ctx, nobody, time.Now())
		notFoundInWords(t, "DenyDeviceCode() of an id no code has", err, dbtype.DeviceCodeNotPendingMessage)
		access, refresh := newFamily(nobody, tokenHash(10), tokenHash(11))
		_, _, err = in.Driver.RedeemDeviceCode(ctx, tokenHash(9), time.Now(), devicePollInterval, access, refresh, nil)
		notFoundInWords(t, "RedeemDeviceCode() of a hash no code has", err, dbtype.DeviceCodeNotRedeemableMessage)
	})
	t.Run("a mailed link", func(t *testing.T) {
		_, err := in.Driver.MailLinkByToken(ctx, tokenHash(9), time.Now())
		notFoundInWords(t, "MailLinkByToken() of a hash no link has", err, dbtype.MailLinkNotLiveMessage)
		err = in.Driver.ConsumeMailLink(ctx, tokenHash(9), time.Now())
		notFoundInWords(t, "ConsumeMailLink() of a hash no link has", err, dbtype.MailLinkNotLiveMessage)
		err = in.Driver.FailMailLink(ctx, nobody, time.Now())
		notFoundInWords(t, "FailMailLink() of an id no link has", err, dbtype.MailLinkNotLiveMessage)
	})
	// A sign-up through a method open to it reaches the store's consume of the link in the
	// sign-up's transaction, and hands the person the store's answer.
	t.Run("a mailed link a sign-up follows", func(t *testing.T) {
		open := h.New(ctx, t, Auth, AuthConfig{Auth: true, Accounts: Open})
		_, err := open.SignUp(ctx, signUpUser("nobody.here@lakeside.edu"), nobody)
		notFoundInWords(t, "SignUp() with a link id no live link has", err, dbtype.MailLinkNotLiveMessage)
	})
}
