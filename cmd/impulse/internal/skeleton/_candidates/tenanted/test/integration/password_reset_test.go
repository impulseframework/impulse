package integration

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// resetLink finds the token of a password reset link in a message the staff auth mailed.
var resetLink = regexp.MustCompile(`http://tenanted\.test/login#reset=([A-Za-z0-9_-]+)`)

// TestPasswordReset_aTenantsAdministratorSendsTheLink proves, on the served stack of an
// application with tenants, that a tenant's administrator sends a person of the tenant a
// link that resets their password, and what the link does.
//
// North's administrator invites a person into north, who creates their account from the
// invitation: it holds the invited address, confirmed, and the password they chose. The
// administrator then sends them a reset link through the tenant's own method. The link
// goes to the address the person confirmed and says an administrator asked. Sending it
// changes nothing: the person's session stands and their password signs in. The link
// opens to the sign-in name, takes a new password and works once; from then on the
// session the person had is gone, the old password is refused and the new one signs in.
// The administrator of another tenant is answered as if the person did not exist, and a
// person asking at the sign-in page is not found until the application's administrator
// lets people ask, a choice north's administrator does not make.
func TestPasswordReset_aTenantsAdministratorSendsTheLink(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	const (
		address     = "nell@north.example"
		newPassword = "a password nell chose"
	)
	admin := signedIn(ctx, t, s, adminUser)
	member := signedIn(ctx, t, s, memberUser)

	// The invited person: an account with a confirmed address and a password, holding a
	// role in north alone.
	status, body := tenantPost(ctx, member, north, "invite-staff-tenant-user", map[string]any{"email": address, "name": "Nell", "roles": []string{"TenantAdministrator"}})
	if status != http.StatusOK {
		t.Fatalf("inviting %s into north: status %d: %s", address, status, body)
	}
	nell := newBrowser(t, s)
	if status, page := showInvitation(ctx, t, nell, mailedInvitation(t, s, address)); status != http.StatusOK {
		t.Fatalf("the invitation page: status %d: %v", status, page)
	}
	status, body = nell.do(ctx, http.MethodPost, "/api/user/invitation/sign-up", []byte(`{"username":"nell","password":"`+invitedPassword+`"}`))
	if status != http.StatusOK {
		t.Fatalf("signing up at the invitation: status %d: %s", status, body)
	}
	if heldForFactor(t, body) {
		nell.secondFactor(ctx, "nell")
	}
	var nellID string
	for id, row := range tenantRows(ctx, t, member, north, "staff-tenant-users") {
		if row["username"] == "nell" {
			nellID = id
		}
	}
	if nellID == "" {
		t.Fatal("north's people do not carry nell after the invitation was accepted")
	}

	// A person asking for themselves is not found until the application's administrator
	// lets people ask, which a tenant's administrator does not decide.
	stranger := newBrowser(t, s)
	if status, body := stranger.do(ctx, http.MethodGet, "/api/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET /api/user/session before signing in: status %d: %s", status, body)
	}
	if status, body := stranger.do(ctx, http.MethodPost, "/api/user/password-reset", []byte(`{"email":"`+address+`"}`)); status != http.StatusNotFound {
		t.Errorf("a person's own request before anyone chose: status %d, want 404: %s", status, body)
	}
	if status, body := member.do(ctx, http.MethodPost, "/api/set-staff-self-service-reset", []byte(`{"on":true}`)); status != http.StatusForbidden {
		t.Errorf("a tenant's administrator letting people reset their own password: status %d, want 403: %s", status, body)
	}
	if status, body := admin.do(ctx, http.MethodPost, "/api/set-staff-self-service-reset", []byte(`{"on":true}`)); status != http.StatusNoContent {
		t.Errorf("the application's administrator letting people reset their own password: status %d, want 204: %s", status, body)
	}
	if status, body := stranger.do(ctx, http.MethodPost, "/api/user/password-reset", []byte(`{"email":"nobody@north.example"}`)); status != http.StatusOK {
		t.Errorf("a person's own request once it is on: status %d, want 200: %s", status, body)
	}

	// The tenant's administrator sends the link; another tenant's is answered as if the
	// person did not exist.
	target := map[string]any{"userId": nellID}
	before := len(s.mail.Messages())
	if status, body := tenantPost(ctx, member, south, "send-staff-tenant-user-password-reset-link", target); status != http.StatusNotFound {
		t.Errorf("north's administrator sending a link under south: status %d, want 404: %s", status, body)
	}
	// Nor is the person found under south by somebody who may send links there: the
	// application's administrator holds the tenant administrator's role in every tenant,
	// and nell is a person of north alone.
	if status, body := tenantPost(ctx, admin, south, "send-staff-tenant-user-password-reset-link", target); status != http.StatusNotFound {
		t.Errorf("an administrator of south sending a link under south to a person of north alone: status %d, want 404: %s", status, body)
	}
	if n := len(s.mail.Messages()) - before; n != 0 {
		t.Errorf("%d message(s) mailed by the requests under south, want none", n)
	}
	status, body = tenantPost(ctx, member, north, "send-staff-tenant-user-password-reset-link", target)
	if status != http.StatusOK || !strings.Contains(string(body), `"sent":true`) {
		t.Fatalf("north's administrator sending nell a link: status %d: %s, want 200 saying it was sent", status, body)
	}
	sent := s.mail.Messages()[before:]
	if len(sent) != 1 || !strings.Contains(sent[0].To, address) || !strings.Contains(sent[0].Text, "asked us to send you a link to reset the password") || !strings.HasPrefix(sent[0].Text, "An administrator of ") {
		// A message's text holds its link, so a failure counts the messages and never
		// prints one.
		t.Fatalf("the link mailed %d message(s), want one to %s saying an administrator asked", len(sent), address)
	}
	match := resetLink.FindStringSubmatch(sent[0].Text)
	if match == nil {
		t.Fatalf("the message %q carries no reset link", sent[0].Subject)
	}
	token := match[1]

	// Sending changed nothing.
	if status, body := nell.do(ctx, http.MethodGet, "/api/user-domains", nil); status != http.StatusOK {
		t.Fatalf("nell's session with a link out: status %d, want 200: %s", status, body)
	}
	if status, body := stranger.login(ctx, "nell", invitedPassword); status != http.StatusOK {
		t.Fatalf("nell's password with a link out: status %d, want 200: %s", status, body)
	}

	// The link opens to the sign-in name and takes the new password, once.
	status, body = stranger.do(ctx, http.MethodPost, "/api/user/password-reset/open", []byte(`{"token":"`+token+`"}`))
	if status != http.StatusOK || !strings.Contains(string(body), `"username":"nell"`) {
		t.Fatalf("the link opened: status %d: %s, want 200 naming nell", status, body)
	}
	complete, err := json.Marshal(map[string]string{"token": token, "password": newPassword})
	if err != nil {
		t.Fatal(err)
	}
	if status, body := stranger.do(ctx, http.MethodPost, "/api/user/password-reset/complete", complete); status != http.StatusOK {
		t.Fatalf("the link completed with a new password: status %d, want 200: %s", status, body)
	}
	if status, body := stranger.do(ctx, http.MethodPost, "/api/user/password-reset/complete", complete); status != http.StatusNotFound {
		t.Errorf("the used link completed again: status %d, want 404: %s", status, body)
	}
	if status, body := nell.do(ctx, http.MethodGet, "/api/user-domains", nil); status != http.StatusUnauthorized {
		t.Errorf("nell's session after the reset: status %d, want 401: %s", status, body)
	}
	fresh := newBrowser(t, s)
	if status, body := fresh.do(ctx, http.MethodGet, "/api/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET /api/user/session before signing in: status %d: %s", status, body)
	}
	if status, body := fresh.login(ctx, "nell", invitedPassword); status != http.StatusUnauthorized {
		t.Errorf("nell's old password after the reset: status %d, want 401: %s", status, body)
	}
	status, body = fresh.login(ctx, "nell", newPassword)
	if status != http.StatusOK {
		t.Fatalf("nell's new password: status %d, want 200: %s", status, body)
	}
	if heldForFactor(t, body) {
		fresh.secondFactor(ctx, "nell")
	}
	if status, body := fresh.do(ctx, http.MethodGet, "/api/user-domains", nil); status != http.StatusOK {
		t.Errorf("GET /api/user-domains as nell signed in with the new password: status %d, want 200: %s", status, body)
	}
}
