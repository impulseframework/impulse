//go:build !skipAuth

// The sign-in rule asks what the Auth registers. An account can hold a password under
// an Auth that registers no password method: here, a release registers the password
// method beside a directory and the next release takes it out. Nobody can enter the
// password any more, so a sign-in held for it would wait for ever. The scenario runs
// through the real sign-in flow on both backends, over one database across the two
// releases.
//
// An application's own account rule can still ask for the password there. Nobody could
// enter it, so that sign-in is refused, and the server's log tells the developer what
// to change: the second scenario.

package e2e

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/impulseframework/impulse/session/azure"
	"github.com/impulseframework/impulse/session/signin"
)

// TestAuthSeams_NoPasswordMethod runs the scenario on each backend.
func TestAuthSeams_NoPasswordMethod(t *testing.T) {
	t.Parallel()

	t.Run(postgresBackend.name, func(t *testing.T) {
		t.Parallel()
		seamPasswordMethodTakenOut(t.Context(), t, postgresBackend)
	})
	t.Run(spannerBackend.name, func(t *testing.T) {
		t.Parallel()
		seamPasswordMethodTakenOut(t.Context(), t, spannerBackend)
	})
}

// seamPasswordMethodTakenOut is the scenario "a person sets a password, a release takes
// the password method out, and the person signs in through the directory for the first
// time": while the method is registered the sign-in waits for the password; once it is
// not, the same sign-in is joined to the account on the code mailed to the account's
// address and finishes, the account keeps its password, and the next sign-in goes
// through the link.
func seamPasswordMethodTakenOut[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	// The first release registers the password method beside the directory. Pat has a
	// password and has never signed in through the directory, so the sign-in under
	// their name waits for the password and links nothing.
	withPassword := oneOrganization(inviteOnly)
	withPassword.password = true
	first := newRulesApp(ctx, t, be, withPassword)
	pat := first.seed(ctx, t, "pat@lakeside.edu")
	first.confirmedAddress(ctx, t, pat, "pat@lakeside.edu")
	// Each sign-in carries claims of its own: the directory writes its issuer into them.
	claims := func() map[string]any {
		return azureClaims(azureTenant, "oid-pat", "pat@lakeside.edu")
	}

	held := first.browser(t)
	assertRedirect(t, first.entraSignIn(ctx, held, claims()), "/login", url.Values{"pending": {"confirmation"}, "returnUrl": {"/home"}})
	held.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/whoami", nil)
	if links := first.links(ctx, t, pat.String()); len(links) != 0 {
		t.Fatalf("identity links while the sign-in waits for the password = %d, want none", len(links))
	}

	// The next release takes the password method out, over the same database. Pat's
	// account still holds the password, and nothing asks for it: the sign-in is joined
	// to the account as to one created ahead with no password. Entra's word for the
	// name is all that ties the two, so the sign-in waits for the code mailed to the
	// address the account holds, and nothing is linked until the code is entered.
	second := first.release(t, be, oneOrganization(inviteOnly))
	b := second.browser(t)
	b.prime(ctx)
	waits := second.entraRoundTrip(ctx, b, claims())
	if links := second.links(ctx, t, pat.String()); len(links) != 0 {
		t.Fatalf("identity links while the sign-in waits for the code = %d, want none", len(links))
	}
	assertRedirect(t, second.confirmAddress(ctx, b, waits, "pat@lakeside.edu"), "/home", url.Values{})
	v := signedInAs(ctx, t, b, "pat@lakeside.edu")
	if v.UserID != pat.String() {
		t.Errorf("session user = %s, want the account that holds the password, %s", v.UserID, pat)
	}
	assertEventsSeen(t, v, "azure:"+azureTenant, "attested-by-evidence:"+azureTenant, "email-code")
	assertOneLink(t, second.links(ctx, t, pat.String()), azureTenant, azureTenant)

	// The link decides from here on.
	again := second.browser(t)
	assertRedirect(t, second.entraSignIn(ctx, again, claims()), "/home", url.Values{})
	if got := again.view(ctx); got.UserID != pat.String() {
		t.Errorf("second sign-in = user %s, want %s", got.UserID, pat)
	}
	if links := second.links(ctx, t, pat.String()); len(links) != 1 {
		t.Errorf("identity links after the second sign-in = %d, want the one", len(links))
	}

	// The password stays the account's: the release that registers the method again
	// finds it there.
	if user, err := second.api.User(ctx, pat); err != nil || user.PasswordHash == nil {
		t.Errorf("User() after the link = %v, %v; want the account to keep its password", user, err)
	}
}

// TestAuthSeams_ConfirmWithNoPasswordMethod runs the scenario on each backend.
func TestAuthSeams_ConfirmWithNoPasswordMethod(t *testing.T) {
	t.Parallel()

	t.Run(postgresBackend.name, func(t *testing.T) {
		t.Parallel()
		seamConfirmWithNoPasswordMethod(t.Context(), t, postgresBackend)
	})
	t.Run(spannerBackend.name, func(t *testing.T) {
		t.Parallel()
		seamConfirmWithNoPasswordMethod(t.Context(), t, spannerBackend)
	})
}

// seamConfirmWithNoPasswordMethod is the scenario "the application's own account rule
// asks for the person's password, and the Auth registers no password method": nobody
// could enter the password, so the sign-in is refused where it would wait for ever,
// nothing is linked, and the server's log tells the developer in one line what to
// change. With the rule changed as the line says, the person signs in.
func seamConfirmWithNoPasswordMethod[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	shape := oneOrganization(inviteOnly)
	shape.entraRule = true
	a := newRulesApp(ctx, t, be, shape)
	// The name is the backend's own, so that each backend's scenario finds its own line
	// in the log the two share.
	name := "pat-" + be.name + "@lakeside.edu"
	pat := a.seed(ctx, t, name)
	a.confirmedAddress(ctx, t, pat, name)
	// Each sign-in carries claims of its own: the directory writes its issuer into them.
	claims := func() map[string]any {
		return azureClaims(azureTenant, "oid-pat", name)
	}
	a.hooks.set(func(h *hooks[Tx]) {
		h.resolveAzure = func(*azure.Identity) *signin.Resolution {
			return signin.Confirm(pat, signin.Tenant(azureTenant))
		}
	})
	logged := captureServerLog(t)

	b := a.browser(t)
	assertRedirect(t, a.entraSignIn(ctx, b, claims()), "/login", url.Values{"code": {"identity_rejected"}})
	b.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/whoami", nil)
	// Nothing waits: the page a waiting sign-in reads finds no sign-in to wait on.
	b.expect(ctx, http.StatusNotFound, http.MethodGet, "/pending", nil)
	if links := a.links(ctx, t, pat.String()); len(links) != 0 {
		t.Errorf("identity links after the refused sign-in = %d, want none", len(links))
	}
	lines := logged.containing(`the account rule of sign-in method "entra" answered signin.Confirm for "` + name + `"`)
	if len(lines) != 1 || !strings.Contains(lines[0], "ERROR: ") || !strings.Contains(lines[0], "this Auth registers no password method") ||
		!strings.Contains(lines[0], "Register the password method, or have the rule answer signin.Link or signin.Reject") {
		t.Errorf("the server's log of the rule's answer = %q, want one error line that says what to change", lines)
	}

	// The rule changed as the line says: the sign-in is joined to the account and
	// finishes.
	a.hooks.set(func(h *hooks[Tx]) {
		h.resolveAzure = func(*azure.Identity) *signin.Resolution {
			return signin.Link(pat, true, signin.Tenant(azureTenant))
		}
	})
	again := a.browser(t)
	assertRedirect(t, a.entraSignIn(ctx, again, claims()), "/home", url.Values{})
	if v := signedInAs(ctx, t, again, name); v.UserID != pat.String() {
		t.Errorf("session user = %s, want the account the rule named, %s", v.UserID, pat)
	}
	if lines := logged.containing("answered signin.Confirm for \"" + name + "\""); len(lines) != 1 {
		t.Errorf("the server's log after the rule answered Link holds %d lines about a confirmation, want the one from before", len(lines))
	}
}

// serverLog is the server's log as a scenario reads it. The library logs through the
// standard logger where a request carries no logger of its own, as these applications'
// requests do, so the standard logger's output goes on to the standard error as before
// and into every capture that is open.
var serverLog = &logTee{}

// logTee copies each line the standard logger writes into the open captures.
type logTee struct {
	once sync.Once
	mu   sync.Mutex
	open []*logCapture
}

// logCapture holds the lines logged while it is open.
type logCapture struct {
	lines []string
}

// Write takes one line of the standard logger, which writes each line whole.
func (l *logTee) Write(line []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	for _, c := range l.open {
		c.lines = append(c.lines, string(line))
	}

	return len(line), nil
}

// captureServerLog opens a capture of what the server logs from here to the end of
// the test.
func captureServerLog(t *testing.T) *logCapture {
	t.Helper()

	serverLog.once.Do(func() {
		log.SetOutput(io.MultiWriter(os.Stderr, serverLog))
	})
	c := &logCapture{}
	serverLog.mu.Lock()
	serverLog.open = append(serverLog.open, c)
	serverLog.mu.Unlock()
	t.Cleanup(func() {
		serverLog.mu.Lock()
		defer serverLog.mu.Unlock()

		serverLog.open = slices.DeleteFunc(serverLog.open, func(open *logCapture) bool {
			return open == c
		})
	})

	return c
}

// containing returns the captured lines that hold text.
func (c *logCapture) containing(text string) []string {
	serverLog.mu.Lock()
	defer serverLog.mu.Unlock()

	var found []string
	for _, line := range c.lines {
		if strings.Contains(line, text) {
			found = append(found, line)
		}
	}

	return found
}
