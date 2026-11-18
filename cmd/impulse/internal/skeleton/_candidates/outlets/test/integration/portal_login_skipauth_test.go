//go:build skipAuth

package integration

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/impulseframework/impulse/session/sessioninfo"
)

// The portal's people sign in through a directory. Under the session library's skipAuth
// build tag the directory is simulated: the login route sends the browser straight to the
// callback, and the callback takes the username from APP_USERNAME (and the role claims
// from APP_ROLES, which the members auth ignores) on every login, so one test process
// signs in as many members as it likes, one at a time.
var simulatedLogin sync.Mutex

// loginPortal signs the browser in as user through the simulated directory and returns
// the callback's status and where it sent the browser: the page the login named. A
// directory vouches for no address, so the first sign-in of a person whose account holds
// none waits at the login page for the code that confirms one: the helper gives the
// address and answers the code as the person would, and returns the page the sign-in
// then goes on to. A sign-in held for anything else is returned as the callback left it.
func (b *browser) loginPortal(ctx context.Context, user string) (status int, location string) {
	b.t.Helper()

	status, location = b.directoryRoundTrip(ctx, user)
	held, err := url.Parse(location)
	if err != nil {
		b.t.Fatalf("the callback sent the browser to %q: %v", location, err)
	}
	if status != http.StatusFound || held.Query().Get("pending") != string(sessioninfo.PendingMFA) {
		return status, location
	}

	s := b.served
	s.codeMu.Lock()
	defer s.codeMu.Unlock()
	pending := b.pending(ctx)
	if pending.EmailCode == nil || !pending.EmailCode.EnrollEmail {
		return status, location
	}
	b.verifyCodeLocked(ctx, user, b.askCodeLocked(ctx, true, portalAddress(user)), false)

	return http.StatusFound, held.Query().Get("returnUrl")
}

// directoryRoundTrip is the round trip alone: the login route, then the callback the
// simulated directory sends the browser straight to, whose status and destination it
// returns.
func (b *browser) directoryRoundTrip(ctx context.Context, user string) (status int, location string) {
	b.t.Helper()

	simulatedLogin.Lock()
	defer simulatedLogin.Unlock()
	if err := os.Setenv("APP_USERNAME", user); err != nil {
		b.t.Fatal(err)
	}

	status, location = b.redirect(ctx, b.prefix+"/user/directory/login?returnUrl="+url.QueryEscape(portalPage))
	if status != http.StatusFound {
		b.t.Fatalf("GET %s/user/directory/login: status %d, want %d to the directory", b.prefix, status, http.StatusFound)
	}

	return b.redirect(ctx, location)
}

// portalAddress is the address a portal person gives for the code: their sign-in name
// where it is an address, as a directory's names are, and the development login's
// address otherwise.
func portalAddress(user string) string {
	if strings.Contains(user, "@") {
		return user
	}

	return factorAddress(user)
}
