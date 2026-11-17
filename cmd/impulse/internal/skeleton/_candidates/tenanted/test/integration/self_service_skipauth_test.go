//go:build skipAuth

package integration

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// googleOpenWarns is what the Sign-in page's dialog shows before an administrator opens
// the google registration, which admits any Google account.
const googleOpenWarns = "Anyone with a Google account will be able to sign up and create a tenant of their own, without an invitation."

// TestSelfService_signInFirstThroughOpenSignUp proves the order a tenant of one's own is
// created in: a stranger signs in first, through the staff auth's google registration
// (any Google account), holds nothing, and then names their tenant. The registration
// starts invite only, so the stranger is refused at first and no account is made; an
// administrator opens it to sign-up in the running application, behind a warning that
// says how wide it is, and from then a name the auth has never seen becomes a user.
// Made invite only again, the person who signed up meanwhile signs in as before. Under the session library's skipAuth build tag
// Google is simulated: the login route sends the browser straight to the callback, and
// the callback signs in whoever APP_GOOGLE_USERNAME names. The test sets that variable,
// so it does not run in parallel.
//
// The new tenant's directory is the one its creator came through, set from the sign-in
// in the row's transaction and never typed: the Workspace is Wander's from then on. A
// directory belongs to one tenant, so a second person from the same Workspace, signed
// in before Wander existed, is refused a tenant of their own with a sentence that says
// the organization has one and what to do, and nothing is written.
func TestSelfService_signInFirstThroughOpenSignUp(t *testing.T) {
	ctx := t.Context()
	s := newServed(ctx, t)
	b := newBrowser(t, s)

	// The methods route offers the registration beside the password form.
	status, body := b.do(ctx, http.MethodGet, "/api/user/methods", nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"google"`) {
		t.Fatalf("GET /api/user/methods: status %d, want 200 naming the google registration: %s", status, body)
	}

	// Invite only, as the registration starts: the stranger is refused at the callback,
	// which sends the browser to the login page with the code, and no account is made.
	t.Setenv("APP_GOOGLE_USERNAME", "wanderer")
	refusedNotInvited(ctx, t, s, "wanderer")

	// The administrator opens the registration: the warning names how wide it is, in the
	// server's words, and the words for the button say what it does.
	admin := signedIn(ctx, t, s, adminUser)
	status, body = askWarning(ctx, admin, "/api/open-staff-sign-up", `{"name":"google"}`)
	var warning resource.Warning
	if err := json.Unmarshal(body, &warning); status != http.StatusOK || err != nil || warning.Consequence != googleOpenWarns || warning.Confirm != "Open to anyone with a Google account" {
		t.Fatalf("the warning of opening the google registration: status %d: %s, want %q", status, body, googleOpenWarns)
	}
	if status, body := admin.do(ctx, http.MethodPost, "/api/open-staff-sign-up", []byte(`{"name":"google"}`)); status != http.StatusNoContent {
		t.Fatalf("POST /api/open-staff-sign-up: status %d: %s", status, body)
	}

	// Signing in: the login route sends the browser to the simulated directory, which
	// is the callback.
	status, location := b.redirect(ctx, "/api/user/google/login?returnUrl="+url.QueryEscape("/dashboard"))
	if status != http.StatusFound {
		t.Fatalf("GET /api/user/google/login: status %d, want %d to the directory", status, http.StatusFound)
	}
	// The account's Workspace is not one the deployment administers, so the callback
	// sends the browser to the login page's code step (pending=mfa), and the sign-in code
	// opens the session, going on to /dashboard.
	status, location = b.redirect(ctx, location)
	if status != http.StatusFound || location != "/login?pending=mfa&returnUrl="+url.QueryEscape("/dashboard") {
		t.Fatalf("the callback answered %d to %q, want %d to the login page's code step, returning to /dashboard", status, location, http.StatusFound)
	}
	b.secondFactor(ctx, "wanderer", false)
	status, body = b.do(ctx, http.MethodGet, "/api/user/session", nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"wanderer"`) {
		t.Fatalf("GET /api/user/session after the sign-in: status %d: %s", status, body)
	}

	// A second person from the same Workspace signs in too, before any tenant has it.
	t.Setenv("APP_GOOGLE_USERNAME", "rival")
	rival := signedInThroughGoogle(ctx, t, s)

	// Signed in and holding nothing, the stranger names their tenant and stands in it.
	if domains := userDomains(ctx, t, b); len(domains) != 0 {
		t.Fatalf("user-domains before = %v, want none", domains)
	}
	if status, body := b.do(ctx, http.MethodPost, "/api/create-tenant", []byte(`{"name":"Wander"}`)); status != http.StatusOK {
		t.Fatalf("POST /api/create-tenant: status %d: %s", status, body)
	}
	names := tenantNames(ctx, t, b)
	if held := slices.Collect(maps.Values(names)); !slices.Equal(held, []string{"Wander"}) {
		t.Fatalf("the tenants the stranger holds after = %v, want Wander alone", held)
	}
	workspace := sessionDirectory(ctx, t, b)
	if workspace == nil {
		t.Fatal("the wanderer's session reports no directory, want the Workspace the sign-in came through")
	}
	for wander := range names {
		if status, body := b.do(ctx, http.MethodGet, "/api/tenants/"+wander+"/announcements", nil); status != http.StatusOK {
			t.Errorf("GET Wander's announcements: status %d, want 200: %s", status, body)
		}
		// The Workspace the wanderer came through is Wander's directory.
		if rows := directories(ctx, t, b, wander); len(rows) != 1 || rows[0] != *workspace {
			t.Errorf("Wander's directory = %+v, want the Workspace %+v its creator signed in through", rows, *workspace)
		}
	}

	// The Workspace has a tenant now, and a directory belongs to one: the rival is
	// refused a tenant of their own, told the organization has one and what to do, and
	// holds nothing after.
	status, body = rival.do(ctx, http.MethodPost, "/api/create-tenant", []byte(`{"name":"Rival"}`))
	if status != http.StatusConflict || !strings.Contains(string(body), "already has a tenant here, and a directory belongs to one tenant. Ask that tenant's administrator to invite you.") {
		t.Errorf("POST /api/create-tenant from a Workspace that has a tenant: status %d, want 409 saying the organization has one: %s", status, body)
	}
	if domains := userDomains(ctx, t, rival); len(domains) != 0 {
		t.Errorf("the rival's tenants after the refusal = %v, want none", domains)
	}
	if held := slices.Sorted(maps.Values(tenantNames(ctx, t, admin))); !slices.Equal(held, []string{"North", "South", "Wander"}) {
		t.Errorf("the tenants after the refusal = %v, want North, South and Wander alone: the refused create wrote nothing", held)
	}

	// Invite only again: the wanderer, who has an account, signs in as before. (A new
	// stranger from the simulated Workspace would be answered by Wander's row now, the
	// Workspace being its directory, so the registration's own refusal is proven above,
	// before any tenant had it.)
	if status, body := admin.do(ctx, http.MethodPost, "/api/make-staff-invite-only", []byte(`{"name":"google"}`)); status != http.StatusNoContent {
		t.Fatalf("POST /api/make-staff-invite-only: status %d: %s", status, body)
	}
	t.Setenv("APP_GOOGLE_USERNAME", "wanderer")
	again := signedInThroughGoogle(ctx, t, s)
	if held := slices.Collect(maps.Values(tenantNames(ctx, t, again))); !slices.Equal(held, []string{"Wander"}) {
		t.Errorf("the tenants the wanderer holds once the registration is invite only again = %v, want Wander alone", held)
	}
}

// refusedNotInvited signs the person APP_GOOGLE_USERNAME names in through the google
// registration and holds the answer to the refusal a person with no account and no
// invitation meets at a method that is invite only: the callback sends the browser to
// the login page with the code, no session opens, and no account is made.
func refusedNotInvited(ctx context.Context, t *testing.T, s *served, username string) {
	t.Helper()

	b := newBrowser(t, s)
	status, location := b.redirect(ctx, "/api/user/google/login?returnUrl="+url.QueryEscape("/dashboard"))
	if status != http.StatusFound {
		t.Fatalf("GET /api/user/google/login as %s: status %d, want %d to the directory", username, status, http.StatusFound)
	}
	if status, location = b.redirect(ctx, location); status != http.StatusFound || !strings.HasSuffix(location, "/login?code="+string(sessioninfo.RefusedNotInvited)) {
		t.Fatalf("the callback of %s while the registration is invite only: %d to %q, want %d to the login page naming %s", username, status, location, http.StatusFound, sessioninfo.RefusedNotInvited)
	}
	if status, body := b.do(ctx, http.MethodGet, "/api/user/session", nil); status != http.StatusOK || !strings.Contains(string(body), `"authenticated":false`) {
		t.Errorf("GET /api/user/session after the refusal of %s: status %d: %s, want no session", username, status, body)
	}
	if accountExists(ctx, t, s, username) {
		t.Errorf("an account was made for %s, refused at a registration that is invite only, want none", username)
	}
}
