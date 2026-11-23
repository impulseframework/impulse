//go:build skipAuth

package integration

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestImpersonatedSessions_ownAccountRoutes proves on the served stack that a person's
// own account is written by the person alone. The rigger adds an address, issues a
// token and is shown two device authorizations. The marshal, viewing as the rigger and
// acting as her (the governor gives her the grant to act as a person), asks
// every account route that writes for the same things, with the rigger's own
// verification link, token and codes in hand, and is refused each one: a session that
// acts as the rigger by the route itself, which names the impersonated session, and the
// view, which is read-only, by the read-only mask, which the generated router
// mounts in front of the routes itself (they stand in front of the console's hook,
// which mounts the mask for the console's own routes alone). Nothing is written: the
// rigger's address, token and codes stand, and the rigger then does each thing herself.
func TestImpersonatedSessions_ownAccountRoutes(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	const address = "rigger-own-account@example.com"
	post := func(b *browser, path, body string) (int, string) {
		status, answer := b.do(ctx, http.MethodPost, consoleAPI+path, []byte(body))

		return status, string(answer)
	}

	own := newBrowser(t, s, consoleAPI)
	own.signIn(ctx, "rigger")
	readOnly := newBrowser(t, s, consoleAPI)
	readOnly.signIn(ctx, "marshal")
	if status, body := readOnly.impersonate(ctx, viewAs(t, "rigger", "walkthrough")); status != http.StatusOK {
		t.Fatalf("the marshal viewing as the rigger: status %d: %s", status, body)
	}
	giveMarshalStandIn(ctx, t, s)
	writing := newBrowser(t, s, consoleAPI)
	writing.signIn(ctx, "marshal")
	if status, body := writing.impersonate(ctx, actAs(t, "rigger", "walkthrough", `,"mask":["List","Read","Execute"]`)); status != http.StatusOK {
		t.Fatalf("the marshal acting as the rigger with List, Read and Execute kept: status %d: %s", status, body)
	}

	// What the rigger holds before anyone looks: a verification link she asked for, a
	// token, and two device authorizations waiting for her word.
	if status, body := post(own, "/user/email", `{"email":"`+address+`"}`); status != http.StatusOK {
		t.Fatalf("the rigger adding an address: status %d: %s", status, body)
	}
	_, link := lastMailTo(t, s, address, "verify")
	status, body := post(own, "/user/tokens", `{"name":"rigging script","outlet":"droids"}`)
	var issued struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &issued); err != nil || status != http.StatusOK || issued.ID == "" {
		t.Fatalf("the rigger issuing a token: status %d, want 200 with the token's id (the answer is not printed: it carries the secret)", status)
	}
	userCode := func() string {
		t.Helper()
		status, body := droid(ctx, t, s, "", http.MethodPost, "/droids/oauth/device/authorize", []byte(`{"client_id":"corral","outlet":"droids"}`))
		var started map[string]any
		if err := json.Unmarshal(body, &started); err != nil || status != http.StatusOK {
			t.Fatalf("POST oauth/device/authorize: status %d", status)
		}
		code, _ := started["user_code"].(string)
		if code == "" {
			t.Fatal("the device authorization carries no user code")
		}

		return code
	}
	approved, denied := userCode(), userCode()
	sent := len(s.mail.Messages())

	routes := []struct{ operation, path, body string }{
		{"Email().Add()", "/user/email", `{"email":"marshal-takes-it@example.com"}`},
		{"Email().Verify()", "/user/email/verify", `{"token":"` + link + `"}`},
		{"ApplicationTokens().Create()", "/user/tokens", `{"name":"the marshal's","outlet":"droids"}`},
		{"ApplicationTokens().Revoke()", "/user/tokens/revoke", `{"id":"` + issued.ID + `"}`},
		{"Device().Approve()", "/user/device/approve", `{"userCode":"` + approved + `"}`},
		{"Device().Deny()", "/user/device/deny", `{"userCode":"` + denied + `"}`},
		{"Invitation().Accept()", "/user/invitation/accept", ``},
	}
	ownAccount := "You are acting as someone else, and this session cannot change their account."
	for _, route := range routes {
		if status, body := post(writing, route.path, route.body); status != http.StatusForbidden || !strings.Contains(body, ownAccount) {
			t.Errorf("%s in a session that acts as the rigger: status %d, want 403 saying %q", route.operation, status, ownAccount)
		}
		if status, body := post(readOnly, route.path, route.body); status != http.StatusForbidden || !strings.Contains(body, "You are acting as someone else, and this session cannot change anything.") {
			t.Errorf("%s in a read-only view: status %d, want 403 from the read-only mask", route.operation, status)
		}
	}

	// The person's own username, where they are signed in, their remembered browsers and
	// their sectors are refused as the person's own name is, by the handler itself, in
	// either session: no read-only mask stands in front of them.
	for _, route := range []struct{ operation, method, path, body string }{
		{"ChangeUsername()", http.MethodPost, "/user/username", `{"username":"marshal-took-it"}`},
		{"UsernameFree()", http.MethodPost, "/user/username/free", `{"username":"marshal-took-it"}`},
		{"OwnSessions()", http.MethodGet, "/user/sessions", ``},
		{"EndOwnSession()", http.MethodPost, "/user/sessions/end", `{"sessionId":"00000000-0000-4000-8000-000000000000"}`},
		{"EndOtherSessions()", http.MethodPost, "/user/sessions/end-others", `{}`},
		{"ForgetBrowsers()", http.MethodPost, "/user/browser/forget-all", ``},
		{"OwnTenants()", http.MethodGet, "/user/tenants", ``},
		{"LeaveTenant()", http.MethodPost, "/user/tenants/leave", `{"domain":"a1111111-1111-4111-8111-111111111111","deletesAccount":false}`},
	} {
		for _, asker := range []struct {
			who string
			b   *browser
		}{{"a session that acts as the rigger", writing}, {"a read-only view", readOnly}} {
			var body []byte
			if route.body != "" {
				body = []byte(route.body)
			}
			if status, answer := asker.b.do(ctx, route.method, consoleAPI+route.path, body); status != http.StatusForbidden || !strings.Contains(string(answer), ownAccount) {
				t.Errorf("%s in %s: status %d, want 403 saying %q", route.operation, asker.who, status, ownAccount)
			}
		}
	}

	// Nothing was written: no mail went out, the rigger's address and username are the
	// ones she signed in with, and her one token stands. Her own session stands too: she
	// does each thing herself below.
	if got := len(s.mail.Messages()); got != sent {
		t.Errorf("messages mailed while the marshal asked = %d, want none", got-sent)
	}
	if sess := own.session(ctx); sess["email"] != factorAddress("rigger") || sess["username"] != "rigger" {
		t.Errorf("the rigger's address and username after the marshal's requests = %v, %v, want the ones she holds", sess["email"], sess["username"])
	}
	status, list := own.do(ctx, http.MethodGet, consoleAPI+"/user/tokens", nil)
	var listed []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(list, &listed); err != nil || status != http.StatusOK || len(listed) != 1 || listed[0].ID != issued.ID {
		t.Errorf("the rigger's tokens after the marshal's requests: status %d, %d listed, want her one token alone", status, len(listed))
	}

	// The rigger does each thing herself: the link, the codes and the token were all
	// still hers to use. No invitation waits in her browser, which is the route's own
	// answer and no refusal.
	for _, act := range []struct {
		what, path, body string
		want             int
	}{
		{"verifying her address", "/user/email/verify", `{"token":"` + link + `"}`, http.StatusOK},
		{"approving a device", "/user/device/approve", `{"userCode":"` + approved + `"}`, http.StatusOK},
		{"denying a device", "/user/device/deny", `{"userCode":"` + denied + `"}`, http.StatusOK},
		{"revoking her token", "/user/tokens/revoke", `{"id":"` + issued.ID + `"}`, http.StatusOK},
		{"accepting an invitation her browser does not carry", "/user/invitation/accept", ``, http.StatusNotFound},
	} {
		if status, body := post(own, act.path, act.body); status != act.want {
			t.Errorf("the rigger %s: status %d, want %d: %s", act.what, status, act.want, body)
		}
	}
	if sess := own.session(ctx); sess["email"] != address || sess["emailVerified"] != true {
		t.Errorf("the rigger's address after she verified it = %v (verified %v), want %s verified", sess["email"], sess["emailVerified"], address)
	}
}
