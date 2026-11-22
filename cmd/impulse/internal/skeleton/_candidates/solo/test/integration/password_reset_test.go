package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/solo/pkg/auth/staff"
	"github.com/impulseframework/impulse/session/mail"
)

// TestPasswordReset_theWayBackIn proves, on the served stack of an application without
// tenants, how a person who forgot their password gets back in.
//
// Whether a person may ask for a reset link themselves is an administrator's choice,
// made in the running application and off until it is made: off, the sign-in page is
// told no place to ask, so it offers no "Forgot your password?", and the request is not
// found; on, the page is told where and the request is accepted, with no restart; a
// person without the grant does not make the choice. The request answers the same for
// an address no account holds, which is mailed nothing. Sending a link changes nothing:
// the password the account has still signs the person in. The link opens to the sign-in
// name, takes a new password, works once, and from then on the old password is refused
// and the new one signs in. An administrator sends a link to a person from their row,
// whatever the choice is, and the mail says an administrator asked.
func TestPasswordReset_theWayBackIn(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	sessions := s.auth.Session().API()
	const (
		resetPath   = "/api/user/password-reset"
		address     = "noor@mail.example"
		oldPassword = "a-long-password-of-my-own"
		newPassword = "a password noor chose"
	)
	admin := newBrowser(t, s)
	admin.signIn(ctx, adminUser)
	// The invited person's account holds the address the invitation was mailed to,
	// confirmed, and the password they chose.
	invited(ctx, t, s, admin, address, "noor")
	noorID := staffUserID(ctx, t, admin, "noor")
	stranger := newBrowser(t, s)
	stranger.prime(ctx)
	ask := func(email string) (int, []byte) {
		return stranger.do(ctx, http.MethodPost, resetPath, []byte(`{"email":"`+email+`"}`))
	}
	allow := func(b *browser, on bool) (int, []byte) {
		choice, err := json.Marshal(map[string]bool{"on": on})
		if err != nil {
			t.Fatal(err)
		}

		return b.do(ctx, http.MethodPost, "/api/set-staff-self-service-reset", choice)
	}

	// Off until an administrator turns it on.
	if got := offeredReset(ctx, t, stranger); got != "" {
		t.Fatalf("the methods answer offers a reset at %q before anyone chose, want no place to ask", got)
	}
	if selfServiceReset(ctx, t, admin) {
		t.Fatal("the sign-in methods list says people may reset their own password before anyone chose")
	}
	if status, body := ask(address); status != http.StatusNotFound {
		t.Fatalf("a person's own request while it is off: status %d, want 404: %s", status, body)
	}

	// On, in the running application.
	noor := newBrowser(t, s)
	noor.prime(ctx)
	status, body := noor.login(ctx, "noor", oldPassword)
	if status != http.StatusOK {
		t.Fatalf("noor's sign-in: status %d: %s", status, body)
	}
	factorIfHeld(ctx, t, noor, "noor", body)
	if status, body := allow(noor, true); status != http.StatusForbidden {
		t.Fatalf("a person without the grant turning it on: status %d, want 403: %s", status, body)
	}
	if status, body := allow(admin, true); status != http.StatusNoContent {
		t.Fatalf("the administrator turning it on: status %d, want 204: %s", status, body)
	}
	if got := offeredReset(ctx, t, stranger); got != resetPath {
		t.Fatalf("the methods answer offers a reset at %q once it is on, want %s", got, resetPath)
	}
	if !selfServiceReset(ctx, t, admin) {
		t.Error("the sign-in methods list does not say people may reset their own password once it is on")
	}
	// The choice is the row's own column: opening the method to sign-up and making it
	// invite only again, which write the same row, leave it as it stands, and it leaves
	// theirs.
	if status, body := admin.do(ctx, http.MethodPost, "/api/open-staff-sign-up", []byte(`{"name":"password","defaultRole":"FeatureAdministrator"}`)); status != http.StatusNoContent {
		t.Fatalf("POST /api/open-staff-sign-up: status %d: %s", status, body)
	}
	if !selfServiceReset(ctx, t, admin) {
		t.Error("opening the password method to sign-up turned people's own password reset off")
	}
	if status, body := allow(admin, true); status != http.StatusNoContent {
		t.Fatalf("the administrator turning it on again: status %d, want 204: %s", status, body)
	}
	if got := signInMethods(ctx, t, admin); len(got) != 1 || !got[0].OpenSignUp || got[0].DefaultRole != "FeatureAdministrator" {
		t.Errorf("the sign-in methods after the choice of a person's own reset was written again = %+v, want the password method still open with its role", got)
	}
	if status, body := admin.do(ctx, http.MethodPost, "/api/make-staff-invite-only", []byte(`{"name":"password"}`)); status != http.StatusNoContent {
		t.Fatalf("POST /api/make-staff-invite-only: status %d: %s", status, body)
	}
	if !selfServiceReset(ctx, t, admin) {
		t.Error("making the password method invite only turned people's own password reset off")
	}

	// The request answers the same whether or not the address has an account, and
	// mails only the one that has.
	before := len(s.mail.Messages())
	status, known := ask(address)
	if status != http.StatusOK {
		t.Fatalf("a person's own request once it is on: status %d, want 200: %s", status, known)
	}
	status, unknown := ask("nobody@mail.example")
	if status != http.StatusOK || !bytes.Equal(known, unknown) {
		t.Errorf("the request for an address no account holds: status %d: %s, want the answer of one with an account: %s", status, unknown, known)
	}
	sessions.WaitForMail()
	for _, m := range s.mail.Messages()[before:] {
		if strings.Contains(m.To, "nobody@mail.example") {
			t.Errorf("an address no account holds was mailed %q, want nothing", m.Subject)
		}
		// A message's text holds its link, so a failure names the message and never
		// prints it.
		if strings.Contains(m.To, address) && !strings.Contains(m.Text, "You asked to reset the password") {
			t.Errorf("the person's own link, %q, does not say they asked", m.Subject)
		}
	}
	token := mailedToken(t, s, before, "reset")

	// Sending changed nothing: the password still signs in. The link opens to the
	// sign-in name and takes the new password.
	if status, body := stranger.login(ctx, "noor", oldPassword); status != http.StatusOK {
		t.Fatalf("noor's password with a link out: status %d, want 200: %s", status, body)
	}
	status, body = stranger.do(ctx, http.MethodPost, resetPath+"/open", []byte(`{"token":"`+token+`"}`))
	if status != http.StatusOK || !strings.Contains(string(body), `"username":"noor"`) {
		t.Fatalf("the link opened: status %d: %s, want 200 naming noor", status, body)
	}
	complete, err := json.Marshal(map[string]string{"token": token, "password": newPassword})
	if err != nil {
		t.Fatal(err)
	}
	if status, body := stranger.do(ctx, http.MethodPost, resetPath+"/complete", complete); status != http.StatusOK {
		t.Fatalf("the link completed with a new password: status %d, want 200: %s", status, body)
	}

	// Used: the session noor had is gone, the old password is refused, the link is
	// dead, and the new password signs in.
	if status, body := noor.do(ctx, http.MethodGet, "/api/feature-flags", nil); status != http.StatusUnauthorized {
		t.Errorf("noor's session after the reset: status %d, want 401: %s", status, body)
	}
	fresh := newBrowser(t, s)
	fresh.prime(ctx)
	if status, body := fresh.login(ctx, "noor", oldPassword); status != http.StatusUnauthorized {
		t.Errorf("noor's old password after the reset: status %d, want 401: %s", status, body)
	}
	if status, body := stranger.do(ctx, http.MethodPost, resetPath+"/complete", complete); status != http.StatusNotFound {
		t.Errorf("the used link completed again: status %d, want 404: %s", status, body)
	}
	status, body = fresh.login(ctx, "noor", newPassword)
	if status != http.StatusOK {
		t.Fatalf("noor's new password: status %d, want 200: %s", status, body)
	}
	factorIfHeld(ctx, t, fresh, "noor", body)
	if status, body := fresh.do(ctx, http.MethodGet, "/api/feature-flags", nil); status != http.StatusOK {
		t.Errorf("GET /api/feature-flags as noor signed in with the new password: status %d, want 200: %s", status, body)
	}

	// An administrator sends a link from the person's row, whatever the choice is; a
	// person without the grant sends none.
	if status, body := allow(admin, false); status != http.StatusNoContent {
		t.Fatalf("the administrator turning it off: status %d, want 204: %s", status, body)
	}
	if status, body := ask(address); status != http.StatusNotFound {
		t.Errorf("a person's own request once it is off again: status %d, want 404: %s", status, body)
	}
	if status, body := fresh.do(ctx, http.MethodPost, "/api/send-staff-user-password-reset-link", []byte(`{"userId":"`+s.adminID+`"}`)); status != http.StatusForbidden {
		t.Errorf("a person without the grant sending a link: status %d, want 403: %s", status, body)
	}
	before = len(s.mail.Messages())
	status, body = admin.do(ctx, http.MethodPost, "/api/send-staff-user-password-reset-link", []byte(`{"userId":"`+noorID+`"}`))
	if status != http.StatusOK || !strings.Contains(string(body), `"sent":true`) {
		t.Fatalf("the administrator's link: status %d: %s, want 200 saying it was sent", status, body)
	}
	sent := s.mail.Messages()[before:]
	if len(sent) != 1 || !strings.Contains(sent[0].To, address) || !strings.Contains(sent[0].Text, "An administrator of "+staff.Application+" asked us to send you a link to reset the password") {
		// A message's text holds its link, so a failure counts the messages and never
		// prints one.
		t.Fatalf("the administrator's link mailed %d message(s), want one to %s saying an administrator asked", len(sent), address)
	}
	status, body = stranger.do(ctx, http.MethodPost, resetPath+"/open", []byte(`{"token":"`+mailedToken(t, s, before, "reset")+`"}`))
	if status != http.StatusOK || !strings.Contains(string(body), `"username":"noor"`) {
		t.Errorf("the administrator's link opened: status %d: %s, want 200 naming noor", status, body)
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

// TestPasswordReset_closingWaitsForTheLink proves that closing the staff auth, which the
// application does once it has stopped serving, waits for a reset link a person was
// promised. A person's own request is answered before its link is mailed, so that
// neither the answer nor its timing says whether the address has an account; an
// application that stopped right after the answer would otherwise never send the link.
func TestPasswordReset_closingWaitsForTheLink(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	held := &heldSender{entered: make(chan struct{}, 8), release: make(chan struct{})}
	s := newServedMailing(ctx, t, func(to mail.Sender) mail.Sender {
		held.to = to

		return held
	})
	const address = "noor@mail.example"
	admin := newBrowser(t, s)
	admin.signIn(ctx, adminUser)
	invited(ctx, t, s, admin, address, "noor")
	if err := s.auth.Session().API().SetSelfServiceReset(ctx, true); err != nil {
		t.Fatalf("SetSelfServiceReset() error = %v", err)
	}
	stranger := newBrowser(t, s)
	stranger.prime(ctx)

	// From here on the mail system takes its time: the request is answered while its
	// link is still on the way.
	before := len(s.mail.Messages())
	held.holding.Store(true)
	if status, body := stranger.do(ctx, http.MethodPost, "/api/user/password-reset", []byte(`{"email":"`+address+`"}`)); status != http.StatusOK {
		t.Fatalf("a person's own request: status %d, want 200: %s", status, body)
	}
	select {
	case <-held.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("no reset link was handed to the mail system within ten seconds of the request")
	}
	if n := len(s.mail.Messages()) - before; n != 0 {
		t.Fatalf("%d message(s) reached the mailbox while the mail system held the link, want none yet", n)
	}

	closed := make(chan error, 1)
	go func() {
		closed <- s.auth.Close()
	}()
	select {
	case err := <-closed:
		t.Fatalf("staff.Auth.Close() returned (error = %v) while the link a person was promised was still being sent, want it to wait for the link", err)
	case <-time.After(300 * time.Millisecond):
	}
	close(held.release)
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("staff.Auth.Close() error = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("staff.Auth.Close() did not return within ten seconds of the mail system taking the link")
	}
	// A message's text holds its link, so a failure counts the messages and never
	// prints one.
	sent := s.mail.Messages()[before:]
	if len(sent) != 1 || !strings.Contains(sent[0].To, address) || !strings.Contains(sent[0].Text, "/login#reset=") {
		t.Fatalf("%d message(s) in the mailbox once the auth was closed, want the one reset link, to %s", len(sent), address)
	}
}

// offeredReset reads the methods route as the login page does and answers where a person
// asks for a reset link: "" while the page offers no "Forgot your password?".
func offeredReset(ctx context.Context, t *testing.T, b *browser) string {
	t.Helper()

	status, body := b.do(ctx, http.MethodGet, "/api/user/methods", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /api/user/methods: status %d: %s", status, body)
	}
	var methods struct {
		PasswordReset *struct {
			Request string `json:"request"`
			Open    string `json:"open"`
		} `json:"passwordReset"`
	}
	if err := json.Unmarshal(body, &methods); err != nil {
		t.Fatalf("decoding the methods answer: %v: %s", err, body)
	}
	if methods.PasswordReset == nil || methods.PasswordReset.Open == "" {
		t.Fatalf("the methods answer names no place a reset link is opened: %s", body)
	}

	return methods.PasswordReset.Request
}

// selfServiceReset reads, from the list the Sign-in page draws, whether people may ask
// for a link that resets their own password: the password method's row says.
func selfServiceReset(ctx context.Context, t *testing.T, b *browser) bool {
	t.Helper()

	status, body := b.do(ctx, http.MethodGet, "/api/staff-sign-in-methods", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /api/staff-sign-in-methods: status %d: %s", status, body)
	}
	var methods []struct {
		Name             string `json:"name"`
		SelfServiceReset bool   `json:"selfServiceReset"`
	}
	if err := json.Unmarshal(body, &methods); err != nil {
		t.Fatalf("decoding the sign-in methods: %v: %s", err, body)
	}
	for _, method := range methods {
		if method.Name == "password" {
			return method.SelfServiceReset
		}
	}
	t.Fatalf("the sign-in methods list carries no password method: %s", body)

	return false
}

// staffUserID is the id of a staff account, read from the list of the auth's users as
// the browser's person.
func staffUserID(ctx context.Context, t *testing.T, b *browser, username string) string {
	t.Helper()

	status, body := b.do(ctx, http.MethodGet, "/api/staff-session-users?limit=200", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /api/staff-session-users: status %d: %s", status, body)
	}
	var users []struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	}
	if err := json.Unmarshal(body, &users); err != nil {
		t.Fatalf("decoding the users: %v: %s", err, body)
	}
	for _, user := range users {
		if user.Username == username {
			return user.ID
		}
	}
	t.Fatalf("the list of users does not carry %s: %s", username, body)

	return ""
}
