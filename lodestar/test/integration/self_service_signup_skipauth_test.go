//go:build skipAuth

package integration

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// TestSelfServiceSignUp_signInFirstThenChart proves the order a sector of one's own is
// charted in, on the served stack: the pioneer signs in first, through the crew auth's
// sign-up registration (any Google account; simulated under the skipAuth tag as
// APP_SIGN_UP_USERNAME), and then names her own sector through the generated
// create-sector route, standing in it at once. The registration starts invite only, so
// she is refused at first (not_invited) and no crew login is made; the governor opens it
// to sign-up, behind a warning that says it admits anyone with a Google account, and
// from then a name the auth has never seen becomes a crew login holding no role and no
// sector. Made invite only again, the next stranger is refused, and the pioneer, who has
// an account, signs in as before.
//
// She signs up with a personal Google account, which stands for everyone who is in no
// organization and so is no sector's directory: the route sets none on Ember, Ember
// admits her as it admits a password, the session read reports no directory for her,
// and her attempt to make her account Ember's directory is refused with what to do. A
// sector made by sign-up takes no directory whichever way its maker signed in, unless
// the application holds a person to one sector
// (self_service_sector_directory_skipauth_test.go proves both).
//
// Any Google account is a directory the fleet does not administer, so her sign-up
// waits for the second factor: the browser lands on the login page with the pending
// marker, she gives an address for the sign-in code (a sign-up holds none), and the
// code opens the session.
//
// Demonstrates: tenancy.self-service, tenancy.creator-directory, auth.open-sign-up, auth.skipauth-directory, auth.second-factor.
func TestSelfServiceSignUp_signInFirstThenChart(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	b := newBrowser(t, s, consoleAPI)

	if status, body := b.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET session before the sign-up: status %d: %s", status, body)
	}
	// Invite only, as the registration starts: she is refused, and no login is made.
	accounts := tableRows(ctx, t, s, "CrewSessionUsers")
	if status, location := b.loginConsoleSignUp(ctx, string(pioneerUser)); status != http.StatusFound || !strings.Contains(location, "code="+string(sessioninfo.RefusedNotInvited)) {
		t.Fatalf("console sign-up while the registration is invite only: status %d to %q, want %d to the login page naming %s", status, location, http.StatusFound, sessioninfo.RefusedNotInvited)
	}
	if after := tableRows(ctx, t, s, "CrewSessionUsers"); after != accounts {
		t.Fatalf("crew logins after the refused sign-up = %d, want the %d there were: nothing is written", after, accounts)
	}

	// The governor opens the registration: the warning names how wide it is.
	hq := newBrowser(t, s, consoleAPI)
	hq.signIn(ctx, "governor")
	const warns = "Anyone with a Google account will be able to sign up and create a sector of their own, without an invitation."
	if status, body := askCrewWarning(ctx, hq, openSignUpRoute, `{"name":"sign-up"}`); status != http.StatusOK || !strings.Contains(string(body), warns) || !strings.Contains(string(body), "Open to anyone with a Google account") {
		t.Fatalf("the warning of opening the sign-up registration: status %d: %s, want %q", status, body, warns)
	}
	openCrewSignUp(ctx, t, hq, crew.SignUpRegistration)

	held := consoleLogin + "?pending=mfa&returnUrl=" + url.QueryEscape(consolePage)
	if status, location := b.loginConsoleSignUp(ctx, string(pioneerUser)); status != http.StatusFound || location != held {
		t.Fatalf("console sign-up: status %d to %q, want %d to %q, the login page with the second factor's wait", status, location, http.StatusFound, held)
	}
	b.secondFactor(ctx, string(pioneerUser))
	sess := b.session(ctx)
	if sess["authenticated"] != true || sess["username"] != string(pioneerUser) {
		t.Fatalf("session after the sign-up = %v, want authenticated as %s", sess, pioneerUser)
	}

	// Signed in and holding nothing: her chart is dark.
	if got := b.domains(ctx); len(got) != 0 {
		t.Fatalf("user-domains after the sign-up = %v, want none", got)
	}

	// She charts Ember, by its fields alone, and stands in it: the row under the id the
	// route generated and answered, her marshal's seat and the roster.
	status, body := b.do(ctx, http.MethodPost, createSectorRoute, []byte(chartEmber))
	if status != http.StatusOK {
		t.Fatalf("POST %s: status %d: %s", createSectorRoute, status, body)
	}
	ember := createdKey(t, "charting Ember", body)
	if got, want := b.domains(ctx), []string{ember}; !slices.Equal(got, want) {
		t.Errorf("user-domains after charting = %v, want %v", got, want)
	}
	if status, body := b.do(ctx, http.MethodGet, consoleAPI+"/sectors/"+ember+"/missions", nil); status != http.StatusOK {
		t.Errorf("GET Ember's flight deck: status %d, want 200: %s", status, body)
	}
	// Ember has no directory: a personal Google account is nobody's, so the route set
	// none, and her chart lights Ember because a personal account is admitted as a
	// password is. The session read reports no directory for her.
	if rows := directories(ctx, t, b, ember); len(rows) != 0 {
		t.Errorf("Ember's directory = %+v, want none: the pioneer signed up with a personal Google account", rows)
	}
	if got := sessionDirectory(t, b.session(ctx)); got != nil {
		t.Errorf("the pioneer's session reports the directory %+v, want none: a personal account is no organization's directory", got)
	}
	// She is Ember's marshal, and making her personal account Ember's directory is
	// refused with what to do; nothing is written.
	status, body = b.do(ctx, http.MethodPost, sectorPath(ember, "take-sector-directory"), []byte(`{}`))
	if want := "A personal Google account is not an organization's directory, so it cannot be a sector's directory. Sign in with your organization's account (Google Workspace or Microsoft Entra) and try again."; status != http.StatusConflict || !strings.Contains(string(body), want) {
		t.Errorf("making a personal Google account Ember's directory: status %d, want 409 saying %q: %s", status, want, body)
	}
	if rows := directories(ctx, t, b, ember); len(rows) != 0 {
		t.Errorf("Ember's directory after the refusal = %+v, want none", rows)
	}
	// One sector per person: a second is refused.
	if status, body := b.do(ctx, http.MethodPost, createSectorRoute, []byte(chartEmberAgain)); status != http.StatusForbidden {
		t.Errorf("POST %s again: status %d, want 403: %s", createSectorRoute, status, body)
	}

	// Invite only again: the next stranger is refused and no login is made, and the
	// pioneer, who has an account, signs in as before and still stands in Ember.
	makeCrewInviteOnly(ctx, t, hq, crew.SignUpRegistration)
	accounts = tableRows(ctx, t, s, "CrewSessionUsers")
	latecomer := newBrowser(t, s, consoleAPI)
	if status, location := latecomer.loginConsoleSignUp(ctx, "latecomer"); status != http.StatusFound || !strings.Contains(location, "code="+string(sessioninfo.RefusedNotInvited)) {
		t.Errorf("console sign-up of a stranger once invite only again: status %d to %q, want %d to the login page naming %s", status, location, http.StatusFound, sessioninfo.RefusedNotInvited)
	}
	if after := tableRows(ctx, t, s, "CrewSessionUsers"); after != accounts {
		t.Errorf("crew logins after the refused sign-up = %d, want the %d there were", after, accounts)
	}
	again := newBrowser(t, s, consoleAPI)
	if status, location := again.loginConsoleSignUp(ctx, string(pioneerUser)); status != http.StatusFound || location != held {
		t.Fatalf("the pioneer's sign-in once the registration is invite only: status %d to %q, want %d to %q, as before", status, location, http.StatusFound, held)
	}
	again.secondFactor(ctx, string(pioneerUser))
	if got, want := again.domains(ctx), []string{ember}; !slices.Equal(got, want) {
		t.Errorf("the pioneer's sectors once the registration is invite only again = %v, want %v", got, want)
	}
}

// loginConsoleSignUp signs the browser in through the crew auth's sign-up registration
// as the named person, simulated by the skipAuth build from APP_SIGN_UP_USERNAME, and
// returns the callback's status and where it sent the browser.
func (b *browser) loginConsoleSignUp(ctx context.Context, user string) (status int, location string) {
	b.t.Helper()

	simulatedLogin.Lock()
	defer simulatedLogin.Unlock()
	if err := os.Setenv("APP_SIGN_UP_USERNAME", user); err != nil {
		b.t.Fatal(err)
	}

	return b.loginThrough(ctx, "/user/"+crew.SignUpRegistration+"/login", consolePage)
}
