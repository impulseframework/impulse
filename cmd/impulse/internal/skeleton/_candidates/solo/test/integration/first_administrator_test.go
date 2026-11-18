package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/session/sessioninfo"

	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/solo/pkg/auth/staff"
)

// The first administrator's passwords: the one a deployment generates for the seed
// (APP_STAFF_ADMINISTRATOR_PASSWORD), and the one the person chooses at the first
// sign-in.
const (
	generatedPassword = "a-generated-password"
	chosenPassword    = "a-password-of-their-own"
)

// TestFirstAdministrator_firstSignIn drives the first sign-in of a deployed environment
// the way the login page does. The migrate command seeded the staff auth's first
// administrator into an empty users table with the password the deployment generated,
// marked for change. The password is accepted and the sign-in waits for the second
// factor; the account holds no address, so the person gives one, and the code mailed
// there opens the session and proves the address. The session stands under the mark: the
// session read says the password must change, and every other request is refused with
// password_change_required until the person posts the change. The change clears the
// mark and the session serves; the generated password then signs nobody in, and the one
// the person chose does.
func TestFirstAdministrator_firstSignIn(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	deployed(ctx, t, s)

	b := newBrowser(t, s)
	if status, body := b.do(ctx, http.MethodGet, "/api/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET /api/user/session before login: status %d: %s", status, body)
	}
	status, body := b.login(ctx, staff.Administrator, generatedPassword)
	if status != http.StatusOK || !heldForFactor(t, body) {
		t.Fatalf("login with the generated password: status %d: %s, want 200 with the sign-in held for the second factor (mfaIsRequired)", status, body)
	}
	if pending := b.pending(ctx); pending.Reason != string(sessioninfo.PendingMFA) || pending.EmailCode == nil || !pending.EmailCode.EnrollEmail {
		t.Fatalf("the pending sign-in = %+v, want the second factor's wait with an address to give: the seeded account holds none", pending)
	}
	b.secondFactor(ctx, staff.Administrator, false)

	// The session stands, the address the code went to is the account's, proven, and the
	// password must change before anything else is served.
	sess := b.session(ctx)
	if !sess.Authenticated || sess.Username != staff.Administrator || !sess.PasswordChangeRequired {
		t.Fatalf("session after the code = %+v, want authenticated as %s with the password to change", sess, staff.Administrator)
	}
	if sess.Email != factorAddress(staff.Administrator) || !sess.EmailVerified {
		t.Errorf("session after the code = %+v, want the address %s held and proven by the code", sess, factorAddress(staff.Administrator))
	}
	if status, body := b.do(ctx, http.MethodGet, "/api/permission-digest", nil); status != http.StatusForbidden || !strings.Contains(string(body), string(sessioninfo.RefusedPasswordChangeRequired)) {
		t.Fatalf("GET /api/permission-digest before the change: status %d: %s, want 403 naming %s", status, body, sessioninfo.RefusedPasswordChangeRequired)
	}

	// The change, as the login page posts it: the generated password and the new one.
	if status, body := b.changePassword(ctx, generatedPassword, chosenPassword); status != http.StatusOK {
		t.Fatalf("POST /api/user/password/change: status %d: %s", status, body)
	}
	if sess := b.session(ctx); !sess.Authenticated || sess.Username != staff.Administrator || sess.PasswordChangeRequired {
		t.Fatalf("session after the change = %+v, want authenticated as %s with no password to change", sess, staff.Administrator)
	}
	if status, body := b.do(ctx, http.MethodGet, "/api/permission-digest", nil); status != http.StatusOK {
		t.Fatalf("GET /api/permission-digest after the change: status %d: %s", status, body)
	}

	// From another browser the generated password is refused as a wrong one is, and the
	// chosen one signs the person in, with the code sent to the address they now hold;
	// the person asks for this browser to be remembered.
	other := newBrowser(t, s)
	if status, body := other.do(ctx, http.MethodGet, "/api/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET /api/user/session before the second login: status %d: %s", status, body)
	}
	if status, body := other.login(ctx, staff.Administrator, generatedPassword); status != http.StatusUnauthorized {
		t.Fatalf("login with the generated password after the change: status %d: %s, want 401", status, body)
	}
	status, body = other.login(ctx, staff.Administrator, chosenPassword)
	if status != http.StatusOK || !heldForFactor(t, body) {
		t.Fatalf("login with the chosen password: status %d: %s, want 200 with the sign-in held for the second factor", status, body)
	}
	if pending := other.pending(ctx); pending.EmailCode == nil || pending.EmailCode.EnrollEmail {
		t.Fatalf("the pending sign-in with the chosen password = %+v, want the code offered to the address the account holds", pending)
	}
	other.secondFactor(ctx, staff.Administrator, true)
	if sess := other.session(ctx); !sess.Authenticated || sess.PasswordChangeRequired {
		t.Fatalf("session with the chosen password = %+v, want authenticated with no password to change", sess)
	}
	if other.trustedCookie() == nil {
		t.Error("the browser asked to be remembered holds no trusted-device cookie")
	}
}

// deployed makes the stack what a deployed environment starts from. The suites' stack
// opens with the development login the bootstrap creates, which a deployment never has:
// it leaves, so the staff auth's users table holds no rows, and the migrate command's
// seed (staff.Auth.SeedAdministrator) then creates the first administrator with the
// password the deployment generated.
func deployed(ctx context.Context, t *testing.T, s *served) {
	t.Helper()

	api := s.auth.Session().API()
	users, err := api.Users(ctx)
	if err != nil {
		t.Fatalf("session.AuthAPI.Users() error = %v", err)
	}
	for _, user := range users {
		if err := api.DeleteSessionUser(ctx, user.ID); err != nil {
			t.Fatalf("session.AuthAPI.DeleteSessionUser(%s) error = %v", user.Username, err)
		}
	}
	seeded, err := s.auth.SeedAdministrator(ctx, generatedPassword)
	if err != nil || !seeded {
		t.Fatalf("staff.Auth.SeedAdministrator() = %v, %v, want the first administrator seeded", seeded, err)
	}
}

// changePassword posts the password change the way the login page's change form does,
// and returns the route's answer.
func (b *browser) changePassword(ctx context.Context, oldPassword, newPassword string) (status int, body []byte) {
	b.t.Helper()

	change, err := json.Marshal(map[string]string{"oldPassword": oldPassword, "newPassword": newPassword})
	if err != nil {
		b.t.Fatal(err)
	}

	return b.do(ctx, http.MethodPost, "/api/user/password/change", change)
}
