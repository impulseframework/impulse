//go:build !skipAuth

// An invitation's link only opens the invitation, through the real handlers on both
// backends: wherever the sign-in does not itself prove the invited address, the
// acceptance waits for the code mailed to that address, and until the code is entered
// nothing is written. The codes of an invitation are counted under the invitation, so
// whoever holds a forwarded link uses up nothing of the invited person's own sign-in,
// and an invitation takes twenty-five codes and no more, however long it stands. A code
// is held to the invitation it was mailed for, whatever the browser that enters it
// carries.

package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/internal/attempts"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// TestAuthSeams_InvitationByMailedCode runs the scenarios of an invitation accepted by
// the code mailed to its address on each backend.
func TestAuthSeams_InvitationByMailedCode(t *testing.T) {
	t.Parallel()

	t.Run(postgresBackend.name, func(t *testing.T) {
		t.Parallel()
		runInvitationByMailedCodeSeams(t, postgresBackend)
	})
	t.Run(spannerBackend.name, func(t *testing.T) {
		t.Parallel()
		runInvitationByMailedCodeSeams(t, spannerBackend)
	})
}

func runInvitationByMailedCodeSeams[Tx any](t *testing.T, be backend[Tx]) {
	tests := []struct {
		name string
		run  func(ctx context.Context, t *testing.T, be backend[Tx])
	}{
		{
			name: "a sign-up with a password through an invitation makes nobody on the link alone: it waits for the code mailed to the invited address, five wrong codes end that code with nothing written, and the code makes the account with the invited address confirmed and accepts the invitation",
			run:  seamInvitedSignUpByCode[Tx],
		},
		{
			name: "an account with a password that has not confirmed the invited address accepts nothing by signing in from the link: the sign-in waits for the code mailed to the invited address, and the account takes the address, confirmed, with the acceptance",
			run:  seamInvitedPasswordAccountByCode[Tx],
		},
		{
			name: "a person signed in whose account has not confirmed the invited address is refused the acceptance by hand in words that say what to do, and accepts by signing in again from the link and entering the code",
			run:  seamInvitedByHandNeedsTheCode[Tx],
		},
		{
			name: "the codes of an invitation are counted under the invitation: the holder of a forwarded link asks for them until they are refused and ends the code with wrong guesses, and the invited person is still mailed the codes of their own sign-in",
			run:  seamInvitationCodesCountedApart[Tx],
		},
		{
			name: "an invitation takes twenty-five codes and no more: the twenty-fifth wrong code is answered as the invitation spent, and no code is mailed or taken for it afterward, in another sign-in or once the count of the codes mailed has started again",
			run:  seamInvitationSpent[Tx],
		},
		{
			name: "a right code before the invitation is spent still accepts: after twenty-four wrong codes the code mailed makes the account and accepts the invitation",
			run:  seamInvitationRightCodeWithinBudget[Tx],
		},
		{
			name: "the code mailed for an invitation is held to that invitation: entered in a browser that dropped the invitation's cookie, or that carries another invitation's, the right code is refused, takes its place in its own invitation's count and writes nothing, and the code of an invitation that is spent or was withdrawn accepts nothing",
			run:  seamInvitationCodeHeldToItsInvitation[Tx],
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tt.run(t.Context(), t, be)
		})
	}
}

// invitedCodeSentence is what a person signed in is told when their acceptance by hand
// needs the code mailed to email.
func invitedCodeSentence(email string) string {
	return "To accept this invitation, sign in again from the invitation's link. A code will be sent to " + email + " to confirm the address is yours."
}

// signUpHeld posts a sign-up with a password through the invitation b opened, which
// must be held for the code mailed to invited with nothing written: no account under
// username and nobody signed in. It asks for the code and returns it.
func (a *authApp[Tx]) signUpHeld(ctx context.Context, b *browser, username, invited string) string {
	b.t.Helper()

	body := b.expect(ctx, http.StatusOK, http.MethodPost, "/invitation/sign-up", map[string]string{"username": username, "password": seedPassword}, b.xsrfToken())
	if !strings.Contains(string(body), `"mfaIsRequired":true`) {
		b.t.Fatalf("the sign-up through the invitation = %s, want it held for the code mailed to the invited address", body)
	}
	a.assertNoRow(ctx, b.t, b, username)
	b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/send", nil)

	return a.lastCode(b.t, invited)
}

// signUpInvited signs a person up with a password through the invitation b opened: the
// sign-up is held with nothing written (signUpHeld), and the code mailed to invited
// makes the account and signs the person in.
func (a *authApp[Tx]) signUpInvited(ctx context.Context, b *browser, username, invited string) {
	b.t.Helper()

	code := a.signUpHeld(ctx, b, username, invited)
	if body := b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/verify", map[string]any{"code": code}); !strings.Contains(string(body), `"mfaIsRequired":false`) {
		b.t.Fatalf("the code of the sign-up = %s, want the account made and signed in", body)
	}
}

// address is the address the account under username holds, "" for none, and whether
// its person has confirmed it.
func (a *authApp[Tx]) address(ctx context.Context, t *testing.T, username string) (email string, confirmed bool) {
	t.Helper()

	user, err := a.api.UserByUsername(ctx, username)
	if err != nil {
		t.Fatalf("UserByUsername(%s) error = %v", username, err)
	}
	if user.Email == nil {
		return "", user.EmailVerified()
	}

	return *user.Email, user.EmailVerified()
}

// wrongCode is a code that is not code.
func wrongCode(code string) string {
	if code == "000000" {
		return "111111"
	}

	return "000000"
}

// assertCodeEnded enters a wrong code in b until the code that stands is ended: four
// are refused as wrong, the fifth ends the code, and the right one, code, is dead with
// it.
func assertCodeEnded(ctx context.Context, t *testing.T, b *browser, code string) {
	t.Helper()

	wrong := wrongCode(code)
	for i := 1; i <= 4; i++ {
		if status, body := b.do(ctx, http.MethodPost, "/pending/code/verify", map[string]any{"code": wrong}); status != http.StatusUnauthorized || !strings.Contains(string(body), `"code":"invalid_code"`) {
			t.Fatalf("wrong code #%d = %d %s, want 401 invalid_code", i, status, body)
		}
	}
	if status, body := b.do(ctx, http.MethodPost, "/pending/code/verify", map[string]any{"code": wrong}); status != http.StatusUnauthorized || !strings.Contains(string(body), `"code":"code_expired"`) {
		t.Errorf("the fifth wrong code = %d %s, want 401 code_expired", status, body)
	}
	if status, body := b.do(ctx, http.MethodPost, "/pending/code/verify", map[string]any{"code": code}); status != http.StatusUnauthorized || !strings.Contains(string(body), `"code":"code_expired"`) {
		t.Errorf("the right code after five wrong ones = %d %s, want 401 code_expired", status, body)
	}
}

// assertCodeGoesToInvited fails unless the wait in b takes the email code alone, mailed
// to the address masked shows, asks for no address, and refuses one typed anyway with
// nothing mailed.
func (a *authApp[Tx]) assertCodeGoesToInvited(ctx context.Context, t *testing.T, b *browser, masked string) {
	t.Helper()

	status := b.pendingStatus(ctx)
	if status.Reason != "mfa" || len(status.Factors) != 1 || status.Factors[0] != "email-code" || status.EmailCode == nil ||
		status.EmailCode.EnrollEmail || status.EmailCode.CodeEmail != masked {
		t.Errorf("pending status = %+v (emailCode %+v), want the email code alone, going to %s, with no address asked for", status, status.EmailCode, masked)
	}
	mailed := len(a.mail.Messages())
	if code, body := b.do(ctx, http.MethodPost, "/pending/email", map[string]string{"email": "mallory@elsewhere.example"}); code != http.StatusConflict || !strings.Contains(string(body), "goes to the invited address") {
		t.Errorf("an address typed for the code = %d %s, want 409 saying the code goes to the invited address", code, body)
	}
	if n := len(a.mail.Messages()) - mailed; n != 0 {
		t.Errorf("%d message(s) mailed for the address typed, want none", n)
	}
}

// seamInvitedSignUpByCode is the scenario "a sign-up with a password through an
// invitation makes nobody on the link alone".
func seamInvitedSignUpByCode[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newTenantInvitationApp(ctx, t, be)
	const invited = "pat@elsewhere.example"
	inv, err := a.api.Invite(ctx, &session.InvitationRequest{Email: invited})
	if err != nil {
		t.Fatalf("Invite() error = %v", err)
	}

	b := a.browser(t)
	b.prime(ctx)
	a.openInvitation(ctx, t, b)

	// The link and a password chosen: nobody is made, nobody is signed in, nothing is
	// mailed until the page asks, and the invitation stands with nothing given.
	mailed := len(a.mail.Messages())
	body := b.expect(ctx, http.StatusOK, http.MethodPost, "/invitation/sign-up", map[string]string{"username": "patl", "password": seedPassword}, b.xsrfToken())
	if !strings.Contains(string(body), `"mfaIsRequired":true`) {
		t.Fatalf("the sign-up through the invitation = %s, want it held for the code mailed to the invited address", body)
	}
	a.assertNoRow(ctx, t, b, "patl")
	if n := len(a.mail.Messages()) - mailed; n != 0 {
		t.Errorf("%d message(s) mailed by the held sign-up, want none before the page asks for the code", n)
	}
	a.assertStanding(ctx, t, "while the sign-up waits for the code", inv)
	a.assertCodeGoesToInvited(ctx, t, b, "p•••@elsewhere.example")
	// The application's own step for a second factor completes no sign-up.
	b.expect(ctx, http.StatusConflict, http.MethodPost, "/mfa", nil)
	// A code entered before one was sent: none stands.
	if status, body := b.do(ctx, http.MethodPost, "/pending/code/verify", map[string]any{"code": "000000"}); status != http.StatusUnauthorized || !strings.Contains(string(body), `"code":"code_expired"`) {
		t.Errorf("verify before a code was sent = %d %s, want 401 code_expired", status, body)
	}

	// The code is mailed to the invited address. Five wrong guesses end it, and the
	// right one is dead with it: still nobody is made and the invitation stands.
	b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/send", nil)
	assertCodeEnded(ctx, t, b, a.lastCode(t, invited))
	a.assertNoRow(ctx, t, b, "patl")
	a.assertStanding(ctx, t, "after the code was ended by wrong guesses", inv)
	if handed := a.handed(); len(handed) != 0 {
		t.Errorf("the hook was handed %d invitations before any code was accepted, want none", len(handed))
	}

	// A fresh code, entered: the account is made with the password chosen and the
	// invited address confirmed, the person is signed in, and the invitation is
	// accepted, the hook told the person signed in with a password.
	b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/send", nil)
	replay := b.clone()
	code := a.lastCode(t, invited)
	if body := b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/verify", map[string]any{"code": code}); !strings.Contains(string(body), `"mfaIsRequired":false`) {
		t.Fatalf("the code of the sign-up = %s, want the account made and signed in", body)
	}
	assertEventsSeen(t, signedInAs(ctx, t, b, "patl"), "password", "email-code")
	if email, confirmed := b.authenticatedEmail(ctx); email != invited || !confirmed {
		t.Errorf("the new account's address = %q (confirmed %v), want the invited address confirmed by the code", email, confirmed)
	}
	a.assertStanding(ctx, t, "after the acceptance")
	handed := a.handed()
	if len(handed) != 1 || handed[0].ID != inv.ID || handed[0].SignInMethod != sessioninfo.MethodPassword {
		t.Fatalf("the hook was handed %+v, want the one invitation, accepted by a sign-in with a password", handed)
	}

	// The wait and its code were used: a copy of the cookies makes no second account
	// and signs nobody in.
	replay.expect(ctx, http.StatusUnauthorized, http.MethodPost, "/pending/code/verify", map[string]any{"code": code})
	replay.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/whoami", nil)

	// The account signs in with the password the person chose.
	again := a.browser(t)
	again.prime(ctx)
	if status, mfa := again.loginAs(ctx, "patl"); status != http.StatusOK || mfa {
		t.Errorf("the new account's password sign-in = %d (mfaIsRequired %v), want 200 with nothing more asked", status, mfa)
	}
}

// seamInvitedPasswordAccountByCode is the scenario "an account with a password that has
// not confirmed the invited address accepts nothing by signing in from the link".
func seamInvitedPasswordAccountByCode[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newTenantInvitationApp(ctx, t, be)

	// Sam's account holds no address. Whoever holds the link mailed to an address and
	// knows the account's password proves nothing of the address.
	a.seed(ctx, t, "sam")
	const invited = "sam@lakeside.edu"
	forSam, err := a.api.Invite(ctx, &session.InvitationRequest{Email: invited})
	if err != nil {
		t.Fatalf("Invite() error = %v", err)
	}
	b := a.browser(t)
	b.prime(ctx)
	a.openInvitation(ctx, t, b)
	if status, mfa := b.loginAs(ctx, "sam"); status != http.StatusOK || !mfa {
		t.Fatalf("the password sign-in from the invitation = %d (mfaIsRequired %v), want 200 held for the code mailed to the invited address", status, mfa)
	}
	b.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/whoami", nil)
	a.assertStanding(ctx, t, "while the sign-in waits for the code", forSam)
	if email, _ := a.address(ctx, t, "sam"); email != "" {
		t.Errorf("the account's address while the sign-in waits = %q, want none taken before the code", email)
	}
	if handed := a.handed(); len(handed) != 0 {
		t.Errorf("the hook was handed %d invitations by the link alone, want none", len(handed))
	}
	a.assertCodeGoesToInvited(ctx, t, b, "s•••@lakeside.edu")

	// The code mailed to the invited address accepts: the account takes the address,
	// confirmed, and the invitation is consumed.
	b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/send", nil)
	if body := b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/verify", map[string]any{"code": a.lastCode(t, invited)}); !strings.Contains(string(body), `"mfaIsRequired":false`) {
		t.Fatalf("the code of the acceptance = %s, want the sign-in completed", body)
	}
	assertEventsSeen(t, signedInAs(ctx, t, b, "sam"), "password", "email-code")
	if email, confirmed := a.address(ctx, t, "sam"); email != invited || !confirmed {
		t.Errorf("the account's address after the acceptance = %q (confirmed %v), want the invited address confirmed by the code", email, confirmed)
	}
	a.assertStanding(ctx, t, "after the acceptance")
	if handed := a.handed(); len(handed) != 1 || handed[0].ID != forSam.ID {
		t.Fatalf("the hook was handed %+v, want the one invitation", handed)
	}

	// Lee's account holds the invited address as an administrator entered it, never
	// confirmed: the link does not confirm it either. The sign-in waits for the code,
	// which goes to that address, and the code confirms it with the acceptance.
	const entered = "lee@lakeside.edu"
	a.seedWithEmail(ctx, t, "lee", entered)
	forLee, err := a.api.Invite(ctx, &session.InvitationRequest{Email: entered})
	if err != nil {
		t.Fatalf("Invite() error = %v", err)
	}
	l := a.browser(t)
	l.prime(ctx)
	a.openInvitation(ctx, t, l)
	if status, mfa := l.loginAs(ctx, "lee"); status != http.StatusOK || !mfa {
		t.Fatalf("the password sign-in from the invitation = %d (mfaIsRequired %v), want 200 held for the code", status, mfa)
	}
	a.assertStanding(ctx, t, "while the second sign-in waits for the code", forLee)
	if _, confirmed := a.address(ctx, t, "lee"); confirmed {
		t.Error("the account's address is confirmed by the link alone, want it confirmed by the code")
	}
	l.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/send", nil)
	if body := l.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/verify", map[string]any{"code": a.lastCode(t, entered)}); !strings.Contains(string(body), `"mfaIsRequired":false`) {
		t.Fatalf("the code of the acceptance = %s, want the sign-in completed", body)
	}
	signedInAs(ctx, t, l, "lee")
	if email, confirmed := a.address(ctx, t, "lee"); email != entered || !confirmed {
		t.Errorf("the account's address after the acceptance = %q (confirmed %v), want %s confirmed by the code", email, confirmed, entered)
	}
	a.assertStanding(ctx, t, "after both acceptances")
}

// seamInvitedByHandNeedsTheCode is the scenario "a person signed in whose account has
// not confirmed the invited address is refused the acceptance by hand".
func seamInvitedByHandNeedsTheCode[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newTenantInvitationApp(ctx, t, be)
	a.seed(ctx, t, "kim")
	const invited = "kim@lakeside.edu"

	// Kim is signed in, and opens an invitation mailed to an address the account has
	// never shown to be its own. A session cannot wait for a code: the acceptance is
	// refused with what to do, and nothing is written.
	kim := a.browser(t)
	kim.prime(ctx)
	if status, mfa := kim.loginAs(ctx, "kim"); status != http.StatusOK || mfa {
		t.Fatalf("password login = %d (mfaIsRequired %v), want 200", status, mfa)
	}
	inv, err := a.api.Invite(ctx, &session.InvitationRequest{Email: invited})
	if err != nil {
		t.Fatalf("Invite() error = %v", err)
	}
	a.openInvitation(ctx, t, kim)
	var refused struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(kim.expect(ctx, http.StatusForbidden, http.MethodPost, "/invitation/accept", nil, kim.xsrfToken()), &refused); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if refused.Message != invitedCodeSentence(invited) {
		t.Errorf("the refused acceptance says %q, want %q", refused.Message, invitedCodeSentence(invited))
	}
	a.assertStanding(ctx, t, "after the refused acceptance", inv)
	if email, _ := a.address(ctx, t, "kim"); email != "" {
		t.Errorf("the account's address after the refused acceptance = %q, want none taken", email)
	}
	if handed := a.handed(); len(handed) != 0 {
		t.Errorf("the hook was handed %d invitations by the refused acceptance, want none", len(handed))
	}

	// Kim does what the sentence says, in the browser that is still signed in and still
	// carries the invitation: signs in again, and the code mailed to the invited address
	// accepts.
	if status, mfa := kim.loginAs(ctx, "kim"); status != http.StatusOK || !mfa {
		t.Fatalf("the password sign-in from the invitation = %d (mfaIsRequired %v), want 200 held for the code", status, mfa)
	}
	kim.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/send", nil)
	if body := kim.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/verify", map[string]any{"code": a.lastCode(t, invited)}); !strings.Contains(string(body), `"mfaIsRequired":false`) {
		t.Fatalf("the code of the acceptance = %s, want the sign-in completed", body)
	}
	signedInAs(ctx, t, kim, "kim")
	if email, confirmed := a.address(ctx, t, "kim"); email != invited || !confirmed {
		t.Errorf("the account's address after the acceptance = %q (confirmed %v), want the invited address confirmed by the code", email, confirmed)
	}
	a.assertStanding(ctx, t, "after the acceptance")

	// The account holds the address confirmed now: its next invitation is accepted by
	// hand.
	if _, err := a.api.Invite(ctx, &session.InvitationRequest{Email: invited}); err != nil {
		t.Fatalf("Invite() error = %v", err)
	}
	a.openInvitation(ctx, t, kim)
	kim.expect(ctx, http.StatusOK, http.MethodPost, "/invitation/accept", nil, kim.xsrfToken())
	a.assertStanding(ctx, t, "after the acceptance by hand")
}

// seamInvitationCodesCountedApart is the scenario "the codes of an invitation are
// counted under the invitation". The application takes the mailed code at every
// sign-in, so the invited person's own sign-in depends on the codes mailed to their
// address: what the holder of a forwarded link asks for must use up none of them.
func seamInvitationCodesCountedApart[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newRulesApp(ctx, t, be, oneOrganization(inviteOnly))
	const address = "pat@lakeside.edu"

	// Pat has an account, made with their address, signs in through the organization's
	// directory and has confirmed the address by the code mailed to it.
	a.createUserAt(ctx, t, address, address)
	pat := passwordOnlyClaims("oid-pat", address)
	a.entraPerson(ctx, t, pat)
	a.hooks.set(func(h *hooks[Tx]) { h.policy = requireEmailCodeTx })

	// An administrator invites Pat's address, and the mail is forwarded to Mallory, a
	// person of the same directory who has no account. The directory proves no
	// address, so Mallory's sign-in from the link waits for the invitation's code.
	inv, err := a.api.Invite(ctx, &session.InvitationRequest{Email: address})
	if err != nil {
		t.Fatalf("Invite() error = %v", err)
	}
	token := lastInvitationToken(t, a.mail)
	m := a.browser(t)
	m.prime(ctx)
	m.expect(ctx, http.StatusOK, http.MethodPost, "/invitation", map[string]string{"token": token}, m.xsrfToken())
	mallory := azureClaims(azureTenant, "oid-mallory", "mallory@lakeside.edu")
	delete(mallory, "email")
	assertRedirect(t, a.entraRoundTrip(ctx, m, mallory), "/login", url.Values{"pending": {"mfa"}, "returnUrl": {"/home"}})

	// Mallory asks for the code until the invitation's count is at its limit, and ends
	// the last one with wrong guesses.
	for i := 1; i <= 5; i++ {
		if status, body := m.do(ctx, http.MethodPost, "/pending/code/send", nil); status != http.StatusOK {
			t.Fatalf("code #%d of the invitation = %d %s, want 200", i, status, body)
		}
	}
	last := a.lastCode(t, address)
	sent := len(a.mail.Messages())
	if status, body := m.do(ctx, http.MethodPost, "/pending/code/send", nil); status != http.StatusTooManyRequests || !strings.Contains(string(body), `"code":"too_many_emails"`) {
		t.Errorf("the sixth code of the invitation = %d %s, want 429 too_many_emails", status, body)
	}
	if got := len(a.mail.Messages()); got != sent {
		t.Errorf("messages after the refused send = %d, want %d: nothing is mailed", got, sent)
	}
	assertCodeEnded(ctx, t, m, last)
	a.assertNoRow(ctx, t, m, "mallory@lakeside.edu")
	if standing := a.standingInvitations(ctx, t); len(standing) != 1 || standing[0].ID != inv.ID {
		t.Fatalf("standing invitations after the forwarded link was tried = %+v, want the invitation still standing", standing)
	}

	// Pat signs in as on any day: the sign-in is held for the code, and the code is
	// mailed. The invitation's count is not the count of Pat's sign-in codes.
	p := a.browser(t)
	p.prime(ctx)
	assertRedirect(t, a.entraRoundTrip(ctx, p, pat), "/login", url.Values{"pending": {"mfa"}, "returnUrl": {"/home"}})
	if status, body := p.do(ctx, http.MethodPost, "/pending/code/send", nil); status != http.StatusOK {
		t.Fatalf("the invited person's own sign-in code = %d %s, want 200: the codes of the invitation are counted apart", status, body)
	}
	if got := len(a.mail.Messages()); got != sent+1 {
		t.Errorf("messages after the invited person's own code = %d, want %d", got, sent+1)
	}
	if body := p.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/verify", map[string]any{"code": a.lastCode(t, address)}); !strings.Contains(string(body), `"mfaIsRequired":false`) {
		t.Fatalf("the invited person's own code = %s, want the sign-in completed", body)
	}
	signedInAs(ctx, t, p, address)
	if standing := a.standingInvitations(ctx, t); len(standing) != 1 {
		t.Errorf("standing invitations after a sign-in that carried no invitation = %d, want the one", len(standing))
	}
}

// invitationSpentSentence is what a person is told of an invitation that takes no more
// codes.
const invitationSpentSentence = "This invitation can't be used any more. Ask the person who invited you to send a new one."

// assertInvitationSpent fails unless the wait in b is refused its code, asked for and
// entered, in the words of an invitation that takes no more, with nothing mailed.
func (a *authApp[Tx]) assertInvitationSpent(ctx context.Context, t *testing.T, b *browser, when string) {
	t.Helper()

	mailed := len(a.mail.Messages())
	for _, request := range []struct {
		path string
		body any
	}{
		{path: "/pending/code/send"},
		{path: "/pending/code/verify", body: map[string]any{"code": "000000"}},
	} {
		status, body := b.do(ctx, http.MethodPost, request.path, request.body)
		if status != http.StatusUnauthorized || !strings.Contains(string(body), `"code":"invitation_spent"`) || !strings.Contains(string(body), invitationSpentSentence) {
			t.Errorf("POST %s %s = %d %s, want 401 invitation_spent saying %q", request.path, when, status, body, invitationSpentSentence)
		}
	}
	if n := len(a.mail.Messages()) - mailed; n != 0 {
		t.Errorf("%d message(s) mailed %s, want none: a spent invitation mails nothing", n, when)
	}
}

// seamInvitationSpent is the scenario "an invitation takes twenty-five codes and no
// more".
func seamInvitationSpent[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newRulesApp(ctx, t, be, oneOrganization(inviteOnly))
	const address = "pat@lakeside.edu"

	// An administrator invites Pat's address, and the mail is forwarded to Mallory, a
	// person of the same directory who has no account. The directory proves no
	// address, so Mallory's sign-in from the link waits for the invitation's code.
	inv, err := a.api.Invite(ctx, &session.InvitationRequest{Email: address})
	if err != nil {
		t.Fatalf("Invite() error = %v", err)
	}
	token := lastInvitationToken(t, a.mail)
	holder := func(oid, username string) *browser {
		b := a.browser(t)
		b.prime(ctx)
		b.expect(ctx, http.StatusOK, http.MethodPost, "/invitation", map[string]string{"token": token}, b.xsrfToken())
		claims := azureClaims(azureTenant, oid, username)
		delete(claims, "email")
		assertRedirect(t, a.entraRoundTrip(ctx, b, claims), "/login", url.Values{"pending": {"mfa"}, "returnUrl": {"/home"}})

		return b
	}
	m := holder("oid-mallory", "mallory@lakeside.edu")

	// Five codes, the most that are mailed within fifteen minutes, and five wrong
	// guesses at each: four are wrong, the fifth ends its code. The last of them is the
	// invitation's twenty-fifth, and is answered as the invitation spent.
	for sent := 1; sent <= 5; sent++ {
		if status, body := m.do(ctx, http.MethodPost, "/pending/code/send", nil); status != http.StatusOK {
			t.Fatalf("code #%d of the invitation = %d %s, want 200", sent, status, body)
		}
		wrong := wrongCode(a.lastCode(t, address))
		for guess := 1; guess <= 5; guess++ {
			want := "invalid_code"
			switch {
			case sent == 5 && guess == 5:
				want = "invitation_spent"
			case guess == 5:
				want = "code_expired"
			}
			if status, body := m.do(ctx, http.MethodPost, "/pending/code/verify", map[string]any{"code": wrong}); status != http.StatusUnauthorized || !strings.Contains(string(body), `"code":"`+want+`"`) {
				t.Fatalf("wrong code #%d of code #%d = %d %s, want 401 %s", guess, sent, status, body, want)
			}
		}
	}
	a.assertInvitationSpent(ctx, t, m, "after twenty-five wrong codes")

	// The count is the invitation's and outlasts it: twenty-five, closed to codes for
	// longer than the invitation stands.
	record, err := a.store.SignInAttempts(ctx, attempts.OwnKey(attempts.KindInvitationGuess, inv.ID.String()))
	if err != nil {
		t.Fatalf("SignInAttempts() of the invitation's codes entered error = %v", err)
	}
	if record.Failures != 25 || record.LockedUntil == nil || !record.LockedUntil.After(inv.ExpiresAt) || !record.WindowEndsAt.After(inv.ExpiresAt) {
		t.Errorf("the invitation's count of codes entered = %+v, want twenty-five, kept and closed for longer than the invitation stands (%v)", record, inv.ExpiresAt)
	}

	// Fifteen minutes on, the count of the codes mailed for the invitation starts again:
	// here it is cleared, as the time passing would. The invitation still takes no code
	// and mails none, in Mallory's sign-in and in a sign-in started afresh by another
	// holder of the link.
	if err := a.store.ClearSignInAttempts(ctx, attempts.OwnKey(attempts.KindInvitation, inv.ID.String())); err != nil {
		t.Fatalf("ClearSignInAttempts() of the invitation's codes mailed error = %v", err)
	}
	a.assertInvitationSpent(ctx, t, m, "once the count of the codes mailed has started again")
	a.assertInvitationSpent(ctx, t, holder("oid-nico", "nico@lakeside.edu"), "in another sign-in that carries the invitation")

	// Nobody was made, and the invitation stands as it was: only its codes are over.
	a.assertNoRow(ctx, t, m, "mallory@lakeside.edu")
	a.assertNoRow(ctx, t, m, "nico@lakeside.edu")
	if standing := a.standingInvitations(ctx, t); len(standing) != 1 || standing[0].ID != inv.ID {
		t.Errorf("standing invitations after the invitation's codes were spent = %+v, want the invitation still listed for the administrator to withdraw or replace", standing)
	}
}

// seamInvitationRightCodeWithinBudget is the scenario "a right code before the
// invitation is spent still accepts".
func seamInvitationRightCodeWithinBudget[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newTenantInvitationApp(ctx, t, be)
	const invited = "pat@elsewhere.example"
	inv, err := a.api.Invite(ctx, &session.InvitationRequest{Email: invited})
	if err != nil {
		t.Fatalf("Invite() error = %v", err)
	}
	b := a.browser(t)
	b.prime(ctx)
	a.openInvitation(ctx, t, b)

	// Pat signs up with a password and mistypes the code: four codes ended by five
	// wrong guesses each, and four wrong guesses at the fifth. Twenty-four wrong codes.
	code := a.signUpHeld(ctx, b, "patl", invited)
	for sent := 1; sent <= 5; sent++ {
		if sent > 1 {
			b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/send", nil)
			code = a.lastCode(t, invited)
		}
		for guess := 1; guess <= 5 && (sent < 5 || guess < 5); guess++ {
			want := "invalid_code"
			if guess == 5 {
				want = "code_expired"
			}
			if status, body := b.do(ctx, http.MethodPost, "/pending/code/verify", map[string]any{"code": wrongCode(code)}); status != http.StatusUnauthorized || !strings.Contains(string(body), `"code":"`+want+`"`) {
				t.Fatalf("wrong code #%d of code #%d = %d %s, want 401 %s", guess, sent, status, body, want)
			}
		}
	}
	a.assertNoRow(ctx, t, b, "patl")
	a.assertStanding(ctx, t, "after twenty-four wrong codes", inv)

	// The twenty-fifth code entered is the right one: the account is made, the invited
	// address confirmed, and the invitation accepted.
	if body := b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/verify", map[string]any{"code": code}); !strings.Contains(string(body), `"mfaIsRequired":false`) {
		t.Fatalf("the right code, the twenty-fifth entered = %s, want the account made and signed in", body)
	}
	signedInAs(ctx, t, b, "patl")
	if email, confirmed := b.authenticatedEmail(ctx); email != invited || !confirmed {
		t.Errorf("the new account's address = %q (confirmed %v), want the invited address confirmed by the code", email, confirmed)
	}
	a.assertStanding(ctx, t, "after the acceptance")
	if handed := a.handed(); len(handed) != 1 || handed[0].ID != inv.ID {
		t.Errorf("the hook was handed %+v, want the one invitation accepted", handed)
	}
}

// withoutCookie is a user agent that holds every cookie b holds but the one named name,
// which b must hold.
func (b *browser) withoutCookie(name string) *browser {
	b.t.Helper()

	jar, err := cookiejar.New(nil)
	if err != nil {
		b.t.Fatalf("cookiejar.New() error = %v", err)
	}
	held := b.client.Jar.Cookies(b.base)
	kept := make([]*http.Cookie, 0, len(held))
	for _, c := range held {
		if c.Name != name {
			kept = append(kept, c)
		}
	}
	if len(kept) != len(held)-1 {
		b.t.Fatalf("the browser holds %d cookie(s), %d of them not named %s; want it to hold that one", len(held), len(kept), name)
	}
	jar.SetCookies(b.base, kept)
	client := *b.client
	client.Jar = jar

	return &browser{t: b.t, client: &client, base: b.base}
}

// codesEntered is the count of the codes entered for the invitation id.
func (a *tenantInvitationApp[Tx]) codesEntered(ctx context.Context, t *testing.T, id impulse.UUID) int64 {
	t.Helper()

	record, err := a.store.SignInAttempts(ctx, attempts.OwnKey(attempts.KindInvitationGuess, id.String()))
	if err != nil {
		if httpio.HasNotFound(err) {
			return 0
		}
		t.Fatalf("SignInAttempts() of the invitation's codes entered error = %v", err)
	}
	if record == nil {
		return 0
	}

	return record.Failures
}

// seamInvitationCodeHeldToItsInvitation is the scenario "the code mailed for an
// invitation is held to that invitation".
func seamInvitationCodeHeldToItsInvitation[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newTenantInvitationApp(ctx, t, be)

	// Sam's account has a password and holds no address. Its person holds the link of
	// an invitation mailed to Pat, opens it and signs in: the sign-in waits for the
	// code mailed to Pat's address, which Sam's person does not read and can only guess.
	a.seed(ctx, t, "sam")
	const invited = "pat@lakeside.edu"
	forPat, err := a.api.Invite(ctx, &session.InvitationRequest{Email: invited})
	if err != nil {
		t.Fatalf("Invite() error = %v", err)
	}
	b := a.browser(t)
	b.prime(ctx)
	a.openInvitation(ctx, t, b)
	if status, mfa := b.loginAs(ctx, "sam"); status != http.StatusOK || !mfa {
		t.Fatalf("the password sign-in from the invitation = %d (mfaIsRequired %v), want 200 held for the code mailed to the invited address", status, mfa)
	}
	forLee, err := a.api.Invite(ctx, &session.InvitationRequest{Email: "lee@lakeside.edu"})
	if err != nil {
		t.Fatalf("Invite() error = %v", err)
	}
	leesToken := lastInvitationToken(t, a.mail)
	b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/send", nil)
	code := a.lastCode(t, invited)

	// refused enters the code mailed for Pat's invitation, the right one, in br, which
	// must be refused with want and sign nobody in. Nothing is written by it: both
	// invitations stand, nothing was handed to the hook and the account holds no
	// address. Pat's invitation must then count wantEntered codes entered, and Lee's
	// none.
	refused := func(br *browser, want string, wantEntered int64, when string) {
		t.Helper()

		if status, body := br.do(ctx, http.MethodPost, "/pending/code/verify", map[string]any{"code": code}); status != http.StatusUnauthorized || !strings.Contains(string(body), `"code":"`+want+`"`) {
			t.Fatalf("the right code %s = %d %s, want 401 %s", when, status, body, want)
		}
		br.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/whoami", nil)
		a.assertStanding(ctx, t, "after the code "+when, forPat, forLee)
		if email, _ := a.address(ctx, t, "sam"); email != "" {
			t.Fatalf("the account's address after the code %s = %q, want none: the invited address is written by the acceptance alone", when, email)
		}
		if handed := a.handed(); len(handed) != 0 {
			t.Fatalf("the hook was handed %d invitation(s) after the code %s, want none", len(handed), when)
		}
		if got := a.codesEntered(ctx, t, forPat.ID); got != wantEntered {
			t.Fatalf("the count of codes entered for the invitation the code was mailed for, after the code %s = %d, want %d", when, got, wantEntered)
		}
		if got := a.codesEntered(ctx, t, forLee.ID); got != 0 {
			t.Fatalf("the count of codes entered for the other invitation, after the code %s = %d, want none: no code was mailed for it", when, got)
		}
	}

	// The code is entered in a browser that dropped the invitation's cookie, and then in
	// one that carries the cookie of Lee's invitation instead. Each time it is refused
	// unjudged and takes a place in the count of Pat's invitation, never of Lee's.
	dropped := b.withoutCookie("auth-invitation")
	refused(dropped, "invitation_expired", 1, "entered without the invitation's cookie")
	other := dropped.clone()
	other.expect(ctx, http.StatusOK, http.MethodPost, "/invitation", map[string]string{"token": leesToken}, other.xsrfToken())
	refused(other, "pending_expired", 2, "entered with another invitation's cookie")

	// Twenty-three more, and Pat's invitation has taken its twenty-five. It takes no
	// more: not in a browser without its cookie, and not in the one that carries it,
	// where the code still stands and is the right one.
	for entered := int64(3); entered <= 25; entered++ {
		refused(dropped, "invitation_expired", entered, "entered again without the invitation's cookie")
	}
	refused(dropped, "invitation_spent", 25, "of a spent invitation, entered without its cookie")
	refused(b, "invitation_spent", 25, "of a spent invitation, entered in the browser that carries it")

	// Kit's account waits the same way for the code of an invitation mailed to Kim, and
	// the invitation is withdrawn before the code is entered: the code accepts nothing
	// and is no second factor of Kit's account, in the browser that still carries the
	// invitation's cookie.
	a.seed(ctx, t, "kit")
	const withdrawn = "kim@lakeside.edu"
	forKim, err := a.api.Invite(ctx, &session.InvitationRequest{Email: withdrawn})
	if err != nil {
		t.Fatalf("Invite() error = %v", err)
	}
	k := a.browser(t)
	k.prime(ctx)
	a.openInvitation(ctx, t, k)
	if status, mfa := k.loginAs(ctx, "kit"); status != http.StatusOK || !mfa {
		t.Fatalf("the password sign-in from the second invitation = %d (mfaIsRequired %v), want 200 held for the code", status, mfa)
	}
	k.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/send", nil)
	kimsCode := a.lastCode(t, withdrawn)
	if err := a.api.RevokeInvitation(ctx, forKim.ID); err != nil {
		t.Fatalf("RevokeInvitation() error = %v", err)
	}
	if status, body := k.do(ctx, http.MethodPost, "/pending/code/verify", map[string]any{"code": kimsCode}); status != http.StatusUnauthorized || !strings.Contains(string(body), `"code":"invitation_expired"`) {
		t.Fatalf("the right code of a withdrawn invitation = %d %s, want 401 invitation_expired", status, body)
	}
	k.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/whoami", nil)
	if email, _ := a.address(ctx, t, "kit"); email != "" {
		t.Errorf("the account's address after the code of a withdrawn invitation = %q, want none", email)
	}
	a.assertStanding(ctx, t, "after the code of a withdrawn invitation", forPat, forLee)
}
