//go:build !skipAuth

// The sign-in rule asks what the Auth registers. An account can hold a password under
// an Auth that registers no password method: here, a release registers the password
// method beside a directory and the next release takes it out. Nobody can enter the
// password any more, so a sign-in held for it would wait for ever. The scenario runs
// through the real sign-in flow on both backends, over one database across the two
// releases.

package e2e

import (
	"context"
	"net/http"
	"net/url"
	"testing"
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
// not, the same sign-in is joined to the account and finishes, the account keeps its
// password, and the next sign-in goes through the link.
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
	// to the account as to one created ahead with no password.
	second := first.release(t, be, oneOrganization(inviteOnly))
	b := second.browser(t)
	assertRedirect(t, second.entraSignIn(ctx, b, claims()), "/home", url.Values{})
	v := signedInAs(ctx, t, b, "pat@lakeside.edu")
	if v.UserID != pat.String() {
		t.Errorf("session user = %s, want the account that holds the password, %s", v.UserID, pat)
	}
	assertEventsSeen(t, v, "azure:"+azureTenant, "attested-by-evidence:"+azureTenant)
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
