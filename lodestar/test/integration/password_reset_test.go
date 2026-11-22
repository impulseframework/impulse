//go:build skipAuth

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/session/mail"
)

// The way back in and the mail an account's changes send, on the served stack: the
// generated router's routes, the generated methods and the crew auth's real session
// manager and mail, as a deployment runs them.

// resetToken and addressToken find the token of the link a message carries: the login
// page's address with the link's kind and its token.
var (
	resetToken   = regexp.MustCompile(`/console/login#reset=([A-Za-z0-9_-]+)`)
	addressToken = regexp.MustCompile(`/console/login#address=([A-Za-z0-9_-]+)`)
)

// mailedSince are the messages the stack's auths mailed to the address after the first
// n it had sent.
func mailedSince(s *served, n int, address string) []mail.Message {
	var to []mail.Message
	for _, m := range s.mail.Messages()[n:] {
		if strings.Contains(m.To, address) {
			to = append(to, m)
		}
	}

	return to
}

// oneMailedSince is the one message mailed to the address after the first n, failing
// the test on none or several.
func oneMailedSince(t *testing.T, s *served, n int, address string) mail.Message {
	t.Helper()

	to := mailedSince(s, n, address)
	if len(to) != 1 {
		t.Fatalf("%d messages were mailed to %s, want one: %q", len(to), address, subjects(to))
	}

	return to[0]
}

// subjects and opening are what a failure says of mailed messages: their subjects, and
// the first line of one, which says who asked. A message's text holds its link further
// down, so a failure never prints the text.
func subjects(messages []mail.Message) []string {
	said := make([]string, 0, len(messages))
	for _, m := range messages {
		said = append(said, m.Subject)
	}

	return said
}

func opening(m mail.Message) string {
	return strings.SplitN(m.Text, "\n", 2)[0]
}

// tokenIn reads the token of the link a message carries.
func tokenIn(t *testing.T, link *regexp.Regexp, m mail.Message) string {
	t.Helper()

	match := link.FindStringSubmatch(m.Text)
	if match == nil {
		t.Fatalf("the message %q carries no link of the kind %s", m.Subject, link)
	}

	return match[1]
}

// offeredReset reads the methods route as the login page does and answers where a
// person asks for a reset link ("" while the page offers no "Forgot your password?")
// and where a link is opened.
func offeredReset(ctx context.Context, t *testing.T, b *browser) (request, open string) {
	t.Helper()

	status, body := b.do(ctx, http.MethodGet, consoleAPI+"/user/methods", nil)
	assertStatus(t, status, http.StatusOK, body)
	var methods struct {
		PasswordReset *struct {
			Request  string `json:"request"`
			Open     string `json:"open"`
			Complete string `json:"complete"`
		} `json:"passwordReset"`
	}
	if err := json.Unmarshal(body, &methods); err != nil {
		t.Fatalf("decoding the methods answer: %v: %s", err, body)
	}
	if methods.PasswordReset == nil {
		t.Fatalf("the methods answer names no password reset routes: %s", body)
	}

	return methods.PasswordReset.Request, methods.PasswordReset.Open
}

// TestPasswordResetLink holds the password reset link end to end. A person asks for
// their own at the sign-in page only while an administrator allows it, a choice made in
// the running application: off, the page is told no place to ask and the request is
// not found; on, both work, with no restart; and a link asked for while it was on
// stands once it is off. The request answers the same for an address no account holds
// and mails it nothing. Sending changes nothing: the password the account has signs the
// person in and their session stands until the link is used. The link opens to the
// sign-in name, takes a password under the rule every password is held to, works once,
// ends every session of the person and leaves the old password refused. A sector's
// administrator sends one for a person of the sector and the mail names the sector; a
// Headquarters administrator sends one and the mail names the application. Reset links
// are counted for the address, whoever asks: the sixth within fifteen minutes is not
// sent, the sender is told why, and a sign-in code still goes to the same address.
//
// Demonstrates: auth.password-reset.
func TestPasswordResetLink(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := newServedWith(ctx, t, servedOptions{namesTenants: true})
	sessions := s.crew.Session().API()
	const (
		resetPath   = consoleAPI + "/user/password-reset"
		userDomains = consoleAPI + "/user-domains"
		newPassword = "a password the cadet chose"
	)
	address := factorAddress("cadet")

	hq := newBrowser(t, s, consoleAPI)
	hq.signIn(ctx, "governor")
	marshal := newBrowser(t, s, consoleAPI)
	marshal.signIn(ctx, "marshal")
	// The cadet's first sign-in confirms their address with the mailed code.
	cadet := newBrowser(t, s, consoleAPI)
	cadet.signIn(ctx, "cadet")
	cadetID := crewAccountID(ctx, t, hq, "cadet")
	stranger := newBrowser(t, s, consoleAPI)
	if status, body := stranger.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("the session route before a login: status %d: %s", status, body)
	}
	ask := func(email string) (int, []byte) {
		return stranger.do(ctx, http.MethodPost, resetPath, jsonBody(t, map[string]string{"email": email}))
	}
	allow := func(on bool) {
		t.Helper()
		status, body := hq.do(ctx, http.MethodPost, crewMethod("set-crew-self-service-reset"), jsonBody(t, map[string]bool{"on": on}))
		assertStatus(t, status, http.StatusNoContent, body)
	}

	// Off until an administrator turns it on.
	if request, open := offeredReset(ctx, t, stranger); request != "" || open != resetPath+"/open" {
		t.Fatalf("the methods answer offers a reset at %q and opens a link at %q, want no place to ask and %s", request, open, resetPath+"/open")
	}
	sentBefore := len(s.mail.Messages())
	if status, body := ask(address); status != http.StatusNotFound {
		t.Fatalf("a person's own request while it is off: status %d, want 404: %s", status, body)
	}
	if status, body := marshal.do(ctx, http.MethodPost, crewMethod("set-crew-self-service-reset"), []byte(`{"on":true}`)); status != http.StatusForbidden {
		t.Fatalf("a sector's marshal turning it on: status %d, want 403: %s", status, body)
	}

	// On, in the running application: the page is told where to ask, and the request
	// answers the same for an address with an account and one with none.
	allow(true)
	if request, _ := offeredReset(ctx, t, stranger); request != resetPath {
		t.Fatalf("the methods answer offers a reset at %q once it is on, want %s", request, resetPath)
	}
	status, known := ask(address)
	assertStatus(t, status, http.StatusOK, known)
	status, unknown := ask("nobody@mail.example")
	assertStatus(t, status, http.StatusOK, unknown)
	if !bytes.Equal(known, unknown) {
		t.Errorf("the request answers %s for an address with an account and %s for one with none, want the same", known, unknown)
	}
	sessions.WaitForMail()
	if to := mailedSince(s, sentBefore, "nobody@mail.example"); len(to) != 0 {
		t.Errorf("an address no account holds was mailed %q, want nothing", subjects(to))
	}
	own := oneMailedSince(t, s, sentBefore, address)
	if !strings.Contains(own.Text, "You asked to reset the password of your Lodestar account.") {
		t.Errorf("the person's own link does not say they asked: it opens with %q", opening(own))
	}
	token := tokenIn(t, resetToken, own)

	// Off again: not offered and not found, and the link asked for while it was on
	// stands.
	allow(false)
	if request, _ := offeredReset(ctx, t, stranger); request != "" {
		t.Errorf("the methods answer still offers a reset at %q once it is off", request)
	}
	if status, body := ask(address); status != http.StatusNotFound {
		t.Errorf("a person's own request once it is off again: status %d, want 404: %s", status, body)
	}

	// Sending changed nothing: the session stands and the password signs in.
	if status, body := cadet.do(ctx, http.MethodGet, userDomains, nil); status != http.StatusOK {
		t.Fatalf("the cadet's session with a link out: status %d, want 200: %s", status, body)
	}
	if status, body := stranger.login(ctx, "cadet", personaPassword); status != http.StatusOK {
		t.Fatalf("the cadet's password with a link out: status %d, want 200: %s", status, body)
	}

	// The link opens to the sign-in name, and takes a password the judgment of its
	// strength takes.
	open := func(token string) (int, []byte) {
		return stranger.do(ctx, http.MethodPost, resetPath+"/open", jsonBody(t, map[string]string{"token": token}))
	}
	complete := func(token, password string) (int, []byte) {
		return stranger.do(ctx, http.MethodPost, resetPath+"/complete", jsonBody(t, map[string]string{"token": token, "password": password}))
	}
	status, body := open(token)
	assertStatus(t, status, http.StatusOK, body)
	if got := cell[string](t, decodeRow(t, body), "username"); got != "cadet" {
		t.Errorf("the opened link names %q, want cadet", got)
	}
	if status, body := complete(token, "kestrelvaughn"); status != http.StatusBadRequest {
		t.Errorf("a password too easy to guess: status %d, want 400: %s", status, body)
	}
	status, body = complete(token, newPassword)
	assertStatus(t, status, http.StatusOK, body)

	// Used: every session of the person is gone, the old password is refused, the new
	// one signs in, and the link is dead.
	if status, body := cadet.do(ctx, http.MethodGet, userDomains, nil); status != http.StatusUnauthorized {
		t.Errorf("the cadet's session after the reset: status %d, want 401: %s", status, body)
	}
	fresh := newBrowser(t, s, consoleAPI)
	if status, body := fresh.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("the session route before a login: status %d: %s", status, body)
	}
	if status, body := fresh.login(ctx, "cadet", personaPassword); status != http.StatusUnauthorized {
		t.Errorf("the cadet's old password after the reset: status %d, want 401: %s", status, body)
	}
	if status, body := open(token); status != http.StatusNotFound {
		t.Errorf("the used link opened again: status %d, want 404: %s", status, body)
	}
	if status, body := complete(token, "another password"); status != http.StatusNotFound {
		t.Errorf("the used link completed again: status %d, want 404: %s", status, body)
	}
	status, body = fresh.login(ctx, "cadet", newPassword)
	assertStatus(t, status, http.StatusOK, body)
	if heldForFactor(t, body) {
		fresh.secondFactor(ctx, "cadet")
	}
	if status, body := fresh.do(ctx, http.MethodGet, userDomains, nil); status != http.StatusOK {
		t.Fatalf("the cadet signed in with the new password: status %d, want 200: %s", status, body)
	}

	// A sector's administrator sends one for a person of the sector: the mail names the
	// sector. A Headquarters administrator's names the application.
	target := jsonBody(t, map[string]string{"userId": cadetID})
	sentBefore = len(s.mail.Messages())
	status, body = marshal.do(ctx, http.MethodPost, sectorPath(anvil, "send-crew-sector-user-password-reset-link"), target)
	if sent, reason := mailingAnswer(t, status, body); !sent {
		t.Fatalf("the marshal's link was not sent: %s", reason)
	}
	fromSector := oneMailedSince(t, s, sentBefore, address)
	if !strings.Contains(fromSector.Text, "An administrator of Anvil asked us to send you a link to reset the password of your Lodestar account.") {
		t.Errorf("the marshal's link does not name the sector: it opens with %q", opening(fromSector))
	}
	sentBefore = len(s.mail.Messages())
	if status, body := marshal.do(ctx, http.MethodPost, sectorPath(bastion, "send-crew-sector-user-password-reset-link"), target); status != http.StatusNotFound {
		t.Errorf("Anvil's marshal sending a link under Bastion: status %d, want 404: %s", status, body)
	}
	// Nor is the cadet found under Bastion by somebody who may send links there: the
	// governor administers every sector, and the cadet is a person of Anvil alone.
	if status, body := hq.do(ctx, http.MethodPost, sectorPath(bastion, "send-crew-sector-user-password-reset-link"), target); status != http.StatusNotFound {
		t.Errorf("the governor sending a link under Bastion to a person of Anvil alone: status %d, want 404: %s", status, body)
	}
	if n := len(s.mail.Messages()) - sentBefore; n != 0 {
		t.Errorf("%d message(s) mailed by the requests under Bastion, want none", n)
	}
	if status, body := fresh.do(ctx, http.MethodPost, sectorPath(anvil, "send-crew-sector-user-password-reset-link"), target); status != http.StatusForbidden {
		t.Errorf("a cadet sending a link: status %d, want 403: %s", status, body)
	}
	sentBefore = len(s.mail.Messages())
	status, body = hq.do(ctx, http.MethodPost, crewMethod("send-crew-user-password-reset-link"), target)
	if sent, reason := mailingAnswer(t, status, body); !sent {
		t.Fatalf("the governor's link was not sent: %s", reason)
	}
	fromHeadquarters := oneMailedSince(t, s, sentBefore, address)
	if !strings.Contains(fromHeadquarters.Text, "An administrator of Lodestar asked us to send you a link to reset the password of your Lodestar account.") {
		t.Errorf("the governor's link does not name the application: it opens with %q", opening(fromHeadquarters))
	}
	// A second link leaves the first one live: each works once.
	for _, m := range []mail.Message{fromSector, fromHeadquarters} {
		if status, body := open(tokenIn(t, resetToken, m)); status != http.StatusOK {
			t.Errorf("the link of %q: status %d, want 200: %s", m.Text[:40], status, body)
		}
	}

	// The limit: the used link cleared the count, so the two above are the first and
	// second. Three more go, and the sixth does not, whoever asks.
	for range 3 {
		status, body = hq.do(ctx, http.MethodPost, crewMethod("send-crew-user-password-reset-link"), target)
		if sent, reason := mailingAnswer(t, status, body); !sent {
			t.Fatalf("a link within the limit was not sent: %s", reason)
		}
	}
	sentBefore = len(s.mail.Messages())
	status, body = hq.do(ctx, http.MethodPost, crewMethod("send-crew-user-password-reset-link"), target)
	if sent, reason := mailingAnswer(t, status, body); sent || reason == "" {
		t.Errorf("the governor's sixth link: sent %v with the reason %q, want it not sent and the reason said", sent, reason)
	}
	status, body = marshal.do(ctx, http.MethodPost, sectorPath(anvil, "send-crew-sector-user-password-reset-link"), target)
	if sent, reason := mailingAnswer(t, status, body); sent || reason == "" {
		t.Errorf("the marshal's link past the limit: sent %v with the reason %q, want it not sent and the reason said", sent, reason)
	}
	if to := mailedSince(s, sentBefore, address); len(to) != 0 {
		t.Errorf("the address was mailed %d messages past the limit, want none", len(to))
	}
	// Reset links are counted apart from sign-in codes: the address closed to reset
	// links is still mailed the code of a sign-in.
	again := newBrowser(t, s, consoleAPI)
	if status, body := again.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("the session route before a login: status %d: %s", status, body)
	}
	status, body = again.login(ctx, "cadet", newPassword)
	assertStatus(t, status, http.StatusOK, body)
	if !heldForFactor(t, body) {
		t.Fatal("the cadet's sign-in from a browser nobody remembered is not held for the code")
	}
	again.secondFactor(ctx, "cadet")
	if status, body := again.do(ctx, http.MethodGet, userDomains, nil); status != http.StatusOK {
		t.Errorf("the cadet signed in with a mailed code while reset links are closed: status %d, want 200: %s", status, body)
	}
}

// heldSender is a mail sender that stands for a mail system which takes its time: once
// it is told to hold, it keeps each message it is asked to send until the test releases
// it, and only then hands the message to the sender behind it. entered is signaled as a
// held message arrives.
type heldSender struct {
	to      mail.Sender
	holding atomic.Bool
	entered chan struct{}
	release chan struct{}
}

// Send holds the message while the sender is holding, then sends it on.
func (h *heldSender) Send(ctx context.Context, msg mail.Message) error {
	if h.holding.Load() {
		h.entered <- struct{}{}
		<-h.release
	}
	if err := h.to.Send(ctx, msg); err != nil {
		return errors.Wrap(err, "mail.Sender.Send()")
	}

	return nil
}

// Delivers says what the sender behind it says: the held messages reach the same mailbox.
func (h *heldSender) Delivers() bool {
	return mail.Delivers(h.to)
}

// TestPasswordResetLink_closingWaitsForIt proves that closing the crew auth, which the
// application does once it has stopped serving, waits for a reset link a person was
// promised. A person's own request is answered before its link is mailed, so that
// neither the answer nor its timing says whether the address has an account; an
// application that stopped right after the answer would otherwise never send the link.
func TestPasswordResetLink_closingWaitsForIt(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	held := &heldSender{entered: make(chan struct{}, 8), release: make(chan struct{})}
	s := newServedWith(ctx, t, servedOptions{crewMailThrough: func(to mail.Sender) mail.Sender {
		held.to = to

		return held
	}})
	address := factorAddress("cadet")
	// The cadet's first sign-in confirms their address with the mailed code.
	cadet := newBrowser(t, s, consoleAPI)
	cadet.signIn(ctx, "cadet")
	if err := s.crew.Session().API().SetSelfServiceReset(ctx, true); err != nil {
		t.Fatalf("SetSelfServiceReset() error = %v", err)
	}
	stranger := newBrowser(t, s, consoleAPI)
	if status, body := stranger.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("the session route before a login: status %d: %s", status, body)
	}

	// From here on the mail system takes its time: the request is answered while its
	// link is still on the way.
	sentBefore := len(s.mail.Messages())
	held.holding.Store(true)
	status, body := stranger.do(ctx, http.MethodPost, consoleAPI+"/user/password-reset", jsonBody(t, map[string]string{"email": address}))
	assertStatus(t, status, http.StatusOK, body)
	select {
	case <-held.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("no reset link was handed to the mail system within ten seconds of the request")
	}
	if n := len(s.mail.Messages()) - sentBefore; n != 0 {
		t.Fatalf("%d message(s) reached the mailbox while the mail system held the link, want none yet", n)
	}

	closed := make(chan error, 1)
	go func() {
		closed <- s.crew.Close()
	}()
	select {
	case err := <-closed:
		t.Fatalf("crew.Auth.Close() returned (error = %v) while the link a person was promised was still being sent, want it to wait for the link", err)
	case <-time.After(300 * time.Millisecond):
	}
	close(held.release)
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("crew.Auth.Close() error = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("crew.Auth.Close() did not return within ten seconds of the mail system taking the link")
	}
	if own := oneMailedSince(t, s, sentBefore, address); resetToken.FindStringSubmatch(own.Text) == nil {
		t.Errorf("the message in the mailbox once the auth was closed, %q, carries no reset link", own.Subject)
	}
}

// TestAccountMail holds the mail an administrator's acts on an account send, and the
// ones that send none. A new address changes nothing until its mailbox confirms: the
// link goes to the new address alone, the account keeps the old one, and once the link
// is followed, by a person who is not signed in, the new address is the account's,
// confirmed, and the old one is told. A reset of the second factor tells the person;
// the last role taken from a person in a sector tells them where they were removed
// from; disabling and enabling an account tell nobody.
//
// Demonstrates: auth.email-address.
func TestAccountMail(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := newServedWith(ctx, t, servedOptions{namesTenants: true})
	const newAddress = "cadet.new@mail.example"
	address := factorAddress("cadet")

	hq := newBrowser(t, s, consoleAPI)
	hq.signIn(ctx, "governor")
	cadet := newBrowser(t, s, consoleAPI)
	cadet.signIn(ctx, "cadet")
	cadetID := crewAccountID(ctx, t, hq, "cadet")
	target := jsonBody(t, map[string]string{"userId": cadetID})
	held := func() (email spanner.NullString, verified spanner.NullTime) {
		t.Helper()

		return readColumn[spanner.NullString](ctx, t, s.db, "CrewSessionUsers", spanner.Key{cadetID}, "Email"),
			readColumn[spanner.NullTime](ctx, t, s.db, "CrewSessionUsers", spanner.Key{cadetID}, "EmailVerifiedAt")
	}
	if email, verified := held(); email.StringVal != address || !verified.Valid {
		t.Fatalf("the cadet holds %v, confirmed %v, after the first sign-in, want %s confirmed", email, verified, address)
	}

	// The address: asked for by an administrator, confirmed from the new mailbox.
	sentBefore := len(s.mail.Messages())
	if status, body := hq.do(ctx, http.MethodPost, crewMethod("set-crew-user-email"), jsonBody(t, map[string]string{"userId": cadetID, "email": "cadet at example"})); status != http.StatusBadRequest {
		t.Errorf("an address that is not one: status %d, want 400: %s", status, body)
	}
	if status, body := hq.do(ctx, http.MethodPost, crewMethod("set-crew-user-email"), target); status != http.StatusBadRequest {
		t.Errorf("a change that names no address: status %d, want 400: %s", status, body)
	}
	status, body := hq.do(ctx, http.MethodPost, crewMethod("set-crew-user-email"), jsonBody(t, map[string]string{"userId": cadetID, "email": newAddress}))
	if sent, reason := mailingAnswer(t, status, body); !sent {
		t.Fatalf("the link that confirms the new address was not sent: %s", reason)
	}
	if email, _ := held(); email.StringVal != address {
		t.Fatalf("the cadet holds %v before the new address confirmed, want %s kept", email, address)
	}
	if to := mailedSince(s, sentBefore, address); len(to) != 0 {
		t.Errorf("the old address was mailed %q before the change, want nothing: it is not asked to allow it", subjects(to))
	}
	confirmation := oneMailedSince(t, s, sentBefore, newAddress)
	if !strings.Contains(confirmation.Text, "An administrator of Lodestar asked to change the email address") {
		t.Errorf("the confirmation does not say who asked: it opens with %q", opening(confirmation))
	}
	stranger := newBrowser(t, s, consoleAPI)
	if status, body := stranger.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("the session route before a login: status %d: %s", status, body)
	}
	confirm := jsonBody(t, map[string]string{"token": tokenIn(t, addressToken, confirmation)})
	sentBefore = len(s.mail.Messages())
	status, body = stranger.do(ctx, http.MethodPost, consoleAPI+"/user/email/confirm", confirm)
	assertStatus(t, status, http.StatusOK, body)
	if email, verified := held(); email.StringVal != newAddress || !verified.Valid {
		t.Errorf("the cadet holds %v, confirmed %v, after the link, want %s confirmed", email, verified, newAddress)
	}
	notice := oneMailedSince(t, s, sentBefore, address)
	if !strings.Contains(notice.Subject, "The email address of your Lodestar account changed") {
		t.Errorf("the old address was told %q, want that the address changed", notice.Subject)
	}
	if status, body := stranger.do(ctx, http.MethodPost, consoleAPI+"/user/email/confirm", confirm); status != http.StatusNotFound {
		t.Errorf("the used link followed again: status %d, want 404: %s", status, body)
	}

	// The second factor reset tells the person, at the address they hold now.
	sentBefore = len(s.mail.Messages())
	status, body = hq.do(ctx, http.MethodPost, crewMethod("revoke-crew-user-trusted-devices"), target)
	assertStatus(t, status, http.StatusNoContent, body)
	if m := oneMailedSince(t, s, sentBefore, newAddress); !strings.Contains(m.Subject, "Your Lodestar sign-in was reset") {
		t.Errorf("the reset of the second factor mailed %q, want the notice of it", m.Subject)
	}

	// Disabling and enabling tell nobody.
	sentBefore = len(s.mail.Messages())
	for _, method := range []string{"disable-crew-user", "enable-crew-user"} {
		status, body = hq.do(ctx, http.MethodPost, crewMethod(method), target)
		assertStatus(t, status, http.StatusNoContent, body)
	}
	if sent := s.mail.Messages()[sentBefore:]; len(sent) != 0 {
		t.Errorf("disabling and enabling the account mailed %q, want nothing", subjects(sent))
	}

	// The roles in Anvil: taking one of two tells nobody, taking the last tells the
	// person where they were removed from. The account, which holds roles for the
	// whole fleet, stays.
	remove := func(role string) {
		t.Helper()
		status, body := hq.do(ctx, http.MethodPost, crewMethod("remove-crew-role"), jsonBody(t, map[string]string{"userId": cadetID, "role": role, "domain": anvil}))
		assertStatus(t, status, http.StatusNoContent, body)
	}
	sentBefore = len(s.mail.Messages())
	remove("Cadet")
	if sent := s.mail.Messages()[sentBefore:]; len(sent) != 0 {
		t.Errorf("taking one of the cadet's two roles in Anvil mailed %q, want nothing", subjects(sent))
	}
	remove("SectorCrew")
	removed := oneMailedSince(t, s, sentBefore, newAddress)
	if removed.Subject != "You were removed from Anvil" || strings.Contains(removed.Text, "deleted") {
		t.Errorf("the removal mailed %q: %s, want the notice of a removal from Anvil with the account kept", removed.Subject, removed.Text)
	}
}
