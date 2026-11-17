//go:build skipAuth

package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// TestSuperAdministrator_entersEverySector proves the one exemption from a sector's
// sign-in settings on the served stack. Bastion admits its people through its directory
// only and has none (the fleet's directory is Cinder's, and a directory belongs to one
// sector), so no sign-in meets its declaration. The governor, a super administrator
// signed in with the password, charts
// Bastion and reads it all the same. Nobody else is exempt: the dispatcher, who holds
// global roles and a role at Bastion, and the archivist, who holds a role in every
// sector, chart the sectors that admit a password and Bastion answers them as a sector
// that does not exist. The exemption is the actor's: the rigger's session started by the
// governor enters Bastion, where the rigger's own does not, and the governor's session
// started by the marshal, who is no super administrator, stays outside it. The same
// person answers for who makes a super administrator: the marshal, viewing as the
// governor with Execute kept, is refused the role for herself.
//
// Demonstrates: auth.super-administrator.
func TestSuperAdministrator_entersEverySector(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)

	governor := newBrowser(t, s, consoleAPI)
	governor.signIn(ctx, "governor")
	if declared := directories(ctx, t, governor, bastion); len(declared) != 0 {
		t.Fatalf("Bastion's directory = %+v, want none: the fleet's directory is Cinder's, and a directory belongs to one sector", declared)
	}

	// enters fails the test unless the browser's chart and Bastion's answer agree with want.
	enters := func(who string, b *browser, chart []string) {
		t.Helper()
		if got := b.domains(ctx); !slices.Equal(got, chart) {
			t.Errorf("%s's chart = %v, want %v", who, got, chart)
		}
		want := http.StatusNotFound
		if slices.Contains(chart, bastion) {
			want = http.StatusOK
		}
		if status, body := b.do(ctx, http.MethodGet, sectorPath(bastion, "wings"), nil); status != want {
			t.Errorf("GET Bastion's wings as %s: status %d, want %d: %s", who, status, want, body)
		}
	}

	enters("the governor, a super administrator", governor, []string{anvil, bastion, cinder})

	dispatcher := newBrowser(t, s, consoleAPI)
	dispatcher.signIn(ctx, "dispatcher")
	enters("the dispatcher, who holds global roles and a role at Bastion", dispatcher, []string{anvil})

	archivist := newBrowser(t, s, consoleAPI)
	archivist.signIn(ctx, "archivist")
	enters("the archivist, who holds a role in every sector", archivist, []string{anvil, cinder})

	rigger := newBrowser(t, s, consoleAPI)
	rigger.signIn(ctx, "rigger")
	enters("the rigger", rigger, []string{anvil})

	// The exemption follows the actor of an impersonated session, never its subject.
	asRigger := newBrowser(t, s, consoleAPI)
	asRigger.signIn(ctx, "governor")
	if status, body := asRigger.impersonate(ctx, viewAs(t, "rigger", "walkthrough")); status != http.StatusOK {
		t.Fatalf("the governor viewing as the rigger: status %d: %s", status, body)
	}
	if info := asRigger.session(ctx); info["username"] != "rigger" {
		t.Fatalf("the session after the governor views as the rigger = %v, want the rigger's", info)
	}
	enters("the rigger's session started by a super administrator", asRigger, []string{anvil, bastion})

	asGovernor := newBrowser(t, s, consoleAPI)
	asGovernor.signIn(ctx, "marshal")
	if status, body := asGovernor.impersonate(ctx, viewAs(t, "governor", "walkthrough")); status != http.StatusOK {
		t.Fatalf("the marshal viewing as the governor: status %d: %s", status, body)
	}
	if info := asGovernor.session(ctx); info["username"] != "governor" {
		t.Fatalf("the session after the marshal views as the governor = %v, want the governor's", info)
	}
	enters("a super administrator's session started by the marshal, who is none", asGovernor, []string{anvil, cinder})

	// Nor does viewing as a super administrator make one: the marshal starts a session
	// as the governor that keeps Execute, whose entry check runs with the governor's
	// grants, and is refused the role for herself, since the real person behind the
	// session holds none. The governor's own session gives it.
	acting := newBrowser(t, s, consoleAPI)
	acting.signIn(ctx, "marshal")
	withExecute := fmt.Sprintf(`{"kind":"user","principal":%q,"reason":"walkthrough","mask":["List","Read","Execute"]}`, account(t, "governor"))
	if status, body := acting.impersonate(ctx, withExecute); status != http.StatusOK {
		t.Fatalf("the marshal viewing as the governor with Execute kept: status %d: %s", status, body)
	}
	toMarshal := fmt.Sprintf(`{"userId":%q,"role":"SuperAdministrator"}`, account(t, "marshal"))
	status, body := acting.do(ctx, http.MethodPost, crewMethod("assign-crew-role"), []byte(toMarshal))
	if status != http.StatusForbidden || !strings.Contains(string(body), "only a super administrator gives the Super Administrator role, and you do not hold the Super Administrator role") {
		t.Errorf("the marshal, viewing as the governor, giving herself the role: status %d, want 403 naming the rule: %s", status, body)
	}
	if status, body := governor.do(ctx, http.MethodPost, crewMethod("assign-crew-role"), []byte(toMarshal)); status != http.StatusNoContent {
		t.Errorf("the governor making the marshal a super administrator: status %d, want 204: %s", status, body)
	}
}

// TestSuperAdministrator_aPersonGivenTheRoleSignsInAgain proves on the served stack that
// giving the Super Administrator role ends what the person held before it, in the
// transaction that gives it. The marshal holds a session of her own and one she started
// as the rigger, whose reach is hers, a token she issued for the droids outlet, and a
// browser she asked to be remembered, which signs her in on her password alone. The
// governor gives her the role: both sessions and the token are refused from then on, so
// nothing issued before the role acts with it, and the remembered browser is asked for
// the code, so she meets the second factor afresh. The governor's own session, the
// giver's, stands. The marshal signs in again, and the role asked for her a second
// time, which she holds already, ends nothing: the token she issues as a super
// administrator stands.
//
// Demonstrates: auth.super-administrator.
func TestSuperAdministrator_aPersonGivenTheRoleSignsInAgain(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	userDomains := consoleAPI + "/user-domains"

	governor := newBrowser(t, s, consoleAPI)
	governor.signIn(ctx, "governor")
	own := newBrowser(t, s, consoleAPI)
	own.signIn(ctx, "marshal")
	viewing := newBrowser(t, s, consoleAPI)
	viewing.signIn(ctx, "marshal")
	if status, body := viewing.impersonate(ctx, viewAs(t, "rigger", "walkthrough")); status != http.StatusOK {
		t.Fatalf("the marshal viewing as the rigger: status %d: %s", status, body)
	}
	// answers fails the test unless each of the marshal's sessions answers want.
	answers := func(when string, sessions map[string]*browser, want int) {
		t.Helper()
		for name, b := range sessions {
			if status, body := b.do(ctx, http.MethodGet, userDomains, nil); status != want {
				t.Errorf("%s %s: status %d, want %d: %s", name, when, status, want, body)
			}
		}
	}
	before := map[string]*browser{"the marshal's own session": own, "the marshal's session as the rigger": viewing}
	answers("before the role", before, http.StatusOK)
	// issue has the marshal issue herself a token for the droids outlet; the answer is
	// never printed, since it carries the secret.
	issue := func(b *browser) string {
		t.Helper()
		status, body := b.do(ctx, http.MethodPost, consoleAPI+"/user/tokens", []byte(`{"name":"patrol script","outlet":"droids"}`))
		var issued struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal(body, &issued); err != nil || status != http.StatusOK || issued.Token == "" {
			t.Fatalf("the marshal issuing a token: status %d, want 200 with the token", status)
		}

		return issued.Token
	}
	token := issue(own)
	if status, body := droid(ctx, t, s, token, http.MethodGet, "/droids/features", nil); status != http.StatusOK {
		t.Fatalf("the marshal's token before the role: status %d, want 200: %s", status, body)
	}
	// The browser she asked to be remembered signs in on the password alone.
	remembered := newBrowser(t, s, consoleAPI)
	remembered.signIn(ctx, "marshal")
	remembered.logout(ctx)
	if status, body := remembered.login(ctx, "marshal", personaPassword); status != http.StatusOK || heldForFactor(t, body) {
		t.Fatalf("the remembered browser's sign-in before the role: status %d: %s, want 200 with no second factor asked", status, body)
	}

	toMarshal := fmt.Sprintf(`{"userId":%q,"role":"SuperAdministrator"}`, account(t, "marshal"))
	if status, body := governor.do(ctx, http.MethodPost, crewMethod("assign-crew-role"), []byte(toMarshal)); status != http.StatusNoContent {
		t.Fatalf("the governor making the marshal a super administrator: status %d, want 204: %s", status, body)
	}
	before["the marshal's session in the remembered browser"] = remembered
	answers("after the marshal was given the role", before, http.StatusUnauthorized)
	answers("after giving the role", map[string]*browser{"the governor's session, the giver's": governor}, http.StatusOK)
	if status, body := droid(ctx, t, s, token, http.MethodGet, "/droids/features", nil); status != http.StatusUnauthorized {
		t.Errorf("the token the marshal issued before the role, after it: status %d, want 401: %s", status, body)
	}
	// The browser is remembered no longer: her password is followed by the code.
	if status, body := remembered.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET session before the once-remembered browser's sign-in: status %d: %s", status, body)
	}
	if status, body := remembered.login(ctx, "marshal", personaPassword); status != http.StatusOK || !heldForFactor(t, body) {
		t.Fatalf("the once-remembered browser's sign-in after the role: status %d: %s, want 200 holding the sign-in for the second factor", status, body)
	}
	remembered.secondFactor(ctx, "marshal")
	if sess := remembered.session(ctx); sess["authenticated"] != true {
		t.Fatalf("session after the role and the code = %v, want the marshal signed in", sess)
	}

	again := newBrowser(t, s, consoleAPI)
	again.signIn(ctx, "marshal")
	held := issue(again)
	if status, body := governor.do(ctx, http.MethodPost, crewMethod("assign-crew-role"), []byte(toMarshal)); status != http.StatusNoContent {
		t.Fatalf("the governor giving the marshal the role she holds: status %d, want 204: %s", status, body)
	}
	answers("after the role was asked for her again", map[string]*browser{
		"the session the marshal signed in to as a super administrator": again,
		"the session she met the code for":                              remembered,
	}, http.StatusOK)
	if status, body := droid(ctx, t, s, held, http.MethodGet, "/droids/features", nil); status != http.StatusOK {
		t.Errorf("the token the marshal issued as a super administrator, after the role was asked for her again: status %d, want 200: %s", status, body)
	}
}
