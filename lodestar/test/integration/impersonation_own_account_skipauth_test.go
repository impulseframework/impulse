//go:build skipAuth

package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// TestImpersonatedSessions_ownAccountRoutes proves on the served stack that a person's
// own account is written by the person alone. The rigger adds an address, issues a
// token and is shown two device authorizations. The marshal, viewing as the rigger, asks
// every account route that writes for the same things, with the rigger's own
// verification link, token and codes in hand, and is refused each one: a view that
// keeps Execute by the route itself, which names the impersonated session, and the
// default view, which is read-only, by the console's read-only backstop, which the
// routes are mounted behind. Nothing is written: the rigger's address, token and codes
// stand, and the rigger then does each thing herself.
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
	writing := newBrowser(t, s, consoleAPI)
	writing.signIn(ctx, "marshal")
	keepsExecute := fmt.Sprintf(`{"kind":"user","principal":%q,"reason":"walkthrough","mask":["List","Read","Execute"]}`, account(t, "rigger"))
	if status, body := writing.impersonate(ctx, keepsExecute); status != http.StatusOK {
		t.Fatalf("the marshal viewing as the rigger with Execute kept: status %d: %s", status, body)
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
	for _, route := range routes {
		want := route.operation + " is not permitted in an impersonated session"
		if status, body := post(writing, route.path, route.body); status != http.StatusForbidden || !strings.Contains(body, want) {
			t.Errorf("%s in a view that keeps Execute: status %d, want 403 saying %q", route.operation, status, want)
		}
		if status, body := post(readOnly, route.path, route.body); status != http.StatusForbidden || !strings.Contains(body, "this session is read-only") {
			t.Errorf("%s in a read-only view: status %d, want 403 from the read-only backstop", route.operation, status)
		}
	}

	// Nothing was written: no mail went out, the rigger's address is the one she signed
	// in with, and her one token stands.
	if got := len(s.mail.Messages()); got != sent {
		t.Errorf("messages mailed while the marshal asked = %d, want none", got-sent)
	}
	if sess := own.session(ctx); sess["email"] != factorAddress("rigger") {
		t.Errorf("the rigger's address after the marshal's requests = %v, want the one she holds", sess["email"])
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
