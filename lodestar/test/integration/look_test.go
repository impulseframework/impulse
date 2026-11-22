//go:build skipAuth

package integration

import (
	"context"
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
// look that follows the person's device is kept as light and dark are, a scheme that is
// not light, dark or device is refused, an impersonated session neither writes the look
// of the person it acts as nor changes it, and the portal's people keep theirs the same
// way.
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

	// The marshal's look follows their device: chosen on one browser, read on another.
	choosesDevice(ctx, t, own, consoleAPI)
	again := newBrowser(t, s, consoleAPI)
	again.signIn(ctx, "marshal")
	if scheme, palette, _ := lookOf(again.session(ctx)); scheme != "device" || palette != "" {
		t.Errorf("the marshal's look on a second browser = %s/%s, want device alone", scheme, palette)
	}

	// A portal person's look is kept with their account the same way.
	portalSignIn := func() *browser {
		t.Helper()
		b := newBrowser(t, s, portalAPI)
		if status, body := b.do(ctx, http.MethodGet, portalAPI+"/user/session", nil); status != http.StatusOK {
			t.Fatalf("GET %s/user/session before the login: status %d: %s", portalAPI, status, body)
		}
		if status, location := b.loginPortal(ctx, clientUser, "ClientPortal"); status != http.StatusFound || location != portalPage {
			t.Fatalf("the portal login of %s: status %d to %q, want %d to %q", clientUser, status, location, http.StatusFound, portalPage)
		}

		return b
	}
	choosesDevice(ctx, t, portalSignIn(), portalAPI)
	if scheme, palette, _ := lookOf(portalSignIn().session(ctx)); scheme != "device" || palette != "" {
		t.Errorf("the portal person's look on a second browser = %s/%s, want device alone", scheme, palette)
	}
}

// choosesDevice posts the look that follows the person's device on b, under the outlet's
// API, and fails the test unless it is answered as kept.
func choosesDevice(ctx context.Context, t *testing.T, b *browser, outlet string) {
	t.Helper()

	status, body := b.do(ctx, http.MethodPost, outlet+"/user/look", []byte(`{"scheme":"device"}`))
	assertStatus(t, status, http.StatusOK, body)
	var kept map[string]any
	if err := json.Unmarshal(body, &kept); err != nil || kept["scheme"] != "device" || kept["palette"] != nil {
		t.Errorf("POST %s/user/look of the device's scheme answered %s (%v), want it as kept", outlet, body, err)
	}
}
