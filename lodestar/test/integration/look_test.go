//go:build skipAuth

package integration

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/resource"
)

// assertHeldBack fails unless the response is the refusal of a request that asked a
// call with no dry run or no warning for one: 400, naming the header. It closes the
// body.
func assertHeldBack(t *testing.T, resp *http.Response, header string) {
	t.Helper()
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if want := "the " + header + " header asks for "; resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), want) {
		t.Errorf("%s %s under %s: answered %d %s, want 400 saying %q", resp.Request.Method, resp.Request.URL.Path, header, resp.StatusCode, body, want)
	}
}

// lookOf is the look a session read answers, and whether the answer carries one.
func lookOf(info map[string]any) (scheme, palette string, present bool) {
	look, present := info["look"].(map[string]any)
	scheme, _ = look["scheme"].(string)
	palette, _ = look["palette"].(string)

	return scheme, palette, present
}

// TestLookFollowsThePerson holds the look a person chose to their account on the served
// stack: the console's methods list names where it is kept, the person's change is
// answered as kept and is what the session read of another browser of theirs answers, a
// scheme that is neither light nor dark is refused, an impersonated session neither
// writes the look of the person it acts as nor changes it, and the portal's people keep
// theirs the same way. The session's own routes have no dry run and no warning, so a
// request that asks one of them for either is refused naming the header and changes
// nothing.
//
// Demonstrates: webapp.theme-choice.
func TestLookFollowsThePerson(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := newServed(ctx, t)

	for outlet, want := range map[string]string{consoleAPI: "/console/api/user/look", portalAPI: "/portal/api/user/look"} {
		status, body := newBrowser(t, s, outlet).do(ctx, http.MethodGet, outlet+"/user/methods", nil)
		assertStatus(t, status, http.StatusOK, body)
		var list map[string]any
		if err := json.Unmarshal(body, &list); err != nil || list["changeLook"] != want {
			t.Errorf("%s/user/methods names changeLook %v (%v), want %s", outlet, list["changeLook"], err, want)
		}
	}

	// The cadet chooses a look on one browser.
	first := newBrowser(t, s, consoleAPI)
	first.signIn(ctx, "cadet")
	if scheme, palette, present := lookOf(first.session(ctx)); present {
		t.Fatalf("a person who chose nothing: the session read answers the look %s/%s, want none", scheme, palette)
	}
	// The session's own routes have no dry run and no warning: a request that asks for
	// either is refused naming the header, signed in or not, and the call is not run.
	for _, header := range []string{resource.DryRunHeader, resource.WarningHeader} {
		asks := map[string]string{header: "true"}
		assertHeldBack(t, newBrowser(t, s, consoleAPI).doWith(ctx, http.MethodGet, consoleAPI+"/user/methods", nil, asks), header)
		assertHeldBack(t, first.doWith(ctx, http.MethodPost, consoleAPI+"/user/look", []byte(`{"scheme":"light"}`), asks), header)
	}
	if scheme, palette, present := lookOf(first.session(ctx)); present {
		t.Fatalf("after the refused requests the session read answers the look %s/%s, want none: the call was not run", scheme, palette)
	}

	status, body := first.do(ctx, http.MethodPost, consoleAPI+"/user/look", []byte(`{"scheme":"dark","palette":" workspace "}`))
	assertStatus(t, status, http.StatusOK, body)
	var kept map[string]any
	if err := json.Unmarshal(body, &kept); err != nil || kept["scheme"] != "dark" || kept["palette"] != "workspace" {
		t.Fatalf("POST user/look answered %s (%v), want the look as kept", body, err)
	}

	// Another browser of theirs starts with it.
	second := newBrowser(t, s, consoleAPI)
	second.signIn(ctx, "cadet")
	if scheme, palette, _ := lookOf(second.session(ctx)); scheme != "dark" || palette != "workspace" {
		t.Errorf("the look on a second browser = %s/%s, want dark/workspace", scheme, palette)
	}

	// A look the rule refuses changes nothing.
	status, body = second.do(ctx, http.MethodPost, consoleAPI+"/user/look", []byte(`{"scheme":"sepia"}`))
	assertStatus(t, status, http.StatusBadRequest, body)

	// The marshal views as the cadet: the look is the cadet's own to choose.
	marshal := newBrowser(t, s, consoleAPI)
	marshal.signIn(ctx, "marshal")
	status, body = marshal.impersonate(ctx, viewAs(t, "cadet", "walkthrough"))
	assertStatus(t, status, http.StatusOK, body)
	status, body = marshal.do(ctx, http.MethodPost, consoleAPI+"/user/look", []byte(`{"scheme":"light"}`))
	assertStatus(t, status, http.StatusForbidden, body)
	if scheme, palette, _ := lookOf(first.session(ctx)); scheme != "dark" || palette != "workspace" {
		t.Errorf("the cadet's look after the refused changes = %s/%s, want dark/workspace", scheme, palette)
	}

	// Another person's look is their own: the marshal's account holds none.
	own := newBrowser(t, s, consoleAPI)
	own.signIn(ctx, "marshal")
	if scheme, palette, present := lookOf(own.session(ctx)); present {
		t.Errorf("the marshal's look = %s/%s, want none: only the cadet chose one", scheme, palette)
	}
}
