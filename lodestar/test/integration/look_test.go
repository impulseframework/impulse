//go:build skipAuth

package integration

import (
	"encoding/json"
	"net/http"
	"testing"
)

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
// theirs the same way.
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
