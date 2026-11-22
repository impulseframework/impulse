//go:build skipAuth

package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// TestSuperAdministrator_entersEverySector proves the super administrator's exemption
// from a sector's sign-in settings on the served stack. Bastion admits its people
// through its directory only, and its directory is the persona provider of the test
// environments, which no password session came through. The governor, a super
// administrator signed in with the password, charts Bastion and reads it all the same.
// Nobody else here is exempt, since none of them is a marshal of Bastion itself, the
// one other person a directory-only sector admits by password
// (TestDirectoryOnly_aSectorsOwnMarshalEntersWithAPassword): the dispatcher, who holds
// global roles and a role at Bastion, and the archivist, who holds a role in every
// sector, chart the sectors that are not directory only, and Bastion, which their
// charts name closed with the reason, answers them as a sector that does not exist.
// The exemption is the actor's: the rigger's session started by the
// governor enters Bastion, where the rigger's own does not, and the governor's session
// started by the marshal, who is no super administrator, stays outside it. The same
// person answers for who makes a super administrator: the marshal, acting as the
// Headquarters role, which gives the crew's roles, is refused the role for the cadet.
//
// Demonstrates: auth.super-administrator.
func TestSuperAdministrator_entersEverySector(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)

	governor := newBrowser(t, s, consoleAPI)
	governor.signIn(ctx, "governor")
	if declared := directories(ctx, t, governor, bastion); len(declared) != 1 || declared[0].Method != string(sessioninfo.MethodOIDC) || declared[0].Connection != crew.PersonaRegistration {
		t.Fatalf("Bastion's directory = %+v, want the seeded persona provider: a sector that is directory only has the directory it is only of", declared)
	}

	// enters fails the test unless the browser's chart and Bastion's answer agree with
	// want. Everyone here holds a seat at Bastion, so the chart of a person's own
	// sign-in that does not light it names it closed, with the reason; a session that
	// views the fleet as someone else is refused Bastion the same and is told of no
	// closed sector, since that word is for a person's own sign-in.
	enters := func(who string, b *browser, chart []string, ownSignIn bool) {
		t.Helper()
		if got := b.domains(ctx); !slices.Equal(got, chart) {
			t.Errorf("%s's chart = %v, want %v", who, got, chart)
		}
		want := http.StatusNotFound
		if slices.Contains(chart, bastion) {
			want = http.StatusOK
		}
		reason, closed := b.closed(ctx)[bastion]
		wantClosed := ownSignIn && !slices.Contains(chart, bastion)
		if closed != wantClosed || (closed && !strings.HasPrefix(reason, "Bastion admits only people who sign in through its own directory. ")) {
			t.Errorf("%s's chart names Bastion closed = %v (%q), want closed = %v, with the reason", who, closed, reason, wantClosed)
		}
		if status, body := b.do(ctx, http.MethodGet, sectorPath(bastion, "wings"), nil); status != want {
			t.Errorf("GET Bastion's wings as %s: status %d, want %d: %s", who, status, want, body)
		}
	}

	enters("the governor, a super administrator", governor, []string{anvil, bastion, cinder}, true)

	dispatcher := newBrowser(t, s, consoleAPI)
	dispatcher.signIn(ctx, "dispatcher")
	enters("the dispatcher, who holds global roles and a role at Bastion", dispatcher, []string{anvil}, true)

	archivist := newBrowser(t, s, consoleAPI)
	archivist.signIn(ctx, "archivist")
	enters("the archivist, who holds a role in every sector", archivist, []string{anvil, cinder}, true)

	rigger := newBrowser(t, s, consoleAPI)
	rigger.signIn(ctx, "rigger")
	enters("the rigger", rigger, []string{anvil}, true)

	// The exemption follows the actor of an impersonated session, never its subject.
	asRigger := newBrowser(t, s, consoleAPI)
	asRigger.signIn(ctx, "governor")
	if status, body := asRigger.impersonate(ctx, viewAs(t, "rigger", "walkthrough")); status != http.StatusOK {
		t.Fatalf("the governor viewing as the rigger: status %d: %s", status, body)
	}
	if info := asRigger.session(ctx); info["username"] != "rigger" {
		t.Fatalf("the session after the governor views as the rigger = %v, want the rigger's", info)
	}
	enters("the rigger's session started by a super administrator", asRigger, []string{anvil, bastion}, false)

	asGovernor := newBrowser(t, s, consoleAPI)
	asGovernor.signIn(ctx, "marshal")
	if status, body := asGovernor.impersonate(ctx, viewAs(t, "governor", "walkthrough")); status != http.StatusOK {
		t.Fatalf("the marshal viewing as the governor: status %d: %s", status, body)
	}
	if info := asGovernor.session(ctx); info["username"] != "governor" {
		t.Fatalf("the session after the marshal views as the governor = %v, want the governor's", info)
	}
	enters("a super administrator's session started by the marshal, who is none", asGovernor, []string{anvil, cinder}, false)

	// Nor does acting as someone make one a super administrator: the marshal acts as
	// the Headquarters role, which gives the crew's roles, and is refused the role for a
	// third person, since the real person behind the session holds none. The console's
	// session that acts as a person never comes so far: its mask refuses every action
	// that changes who may do what, and a role for herself is refused whatever the
	// session, since nobody is the subject of what they do while they act as someone
	// else (impersonation_subject_skipauth_test). The identity package's tests hold the
	// rule to the actor of a session that acts as a super administrator. The governor's
	// own session gives the role.
	acting := newBrowser(t, s, consoleAPI)
	acting.signIn(ctx, "marshal")
	if status, body := acting.impersonate(ctx, `{"kind":"role","principal":"Headquarters","reason":"walkthrough"}`); status != http.StatusOK {
		t.Fatalf("the marshal acting as the Headquarters role: status %d: %s", status, body)
	}
	toCadet := fmt.Sprintf(`{"userId":%q,"role":"SuperAdministrator"}`, account(t, "cadet"))
	status, body := acting.do(ctx, http.MethodPost, crewMethod("assign-crew-role"), []byte(toCadet))
	if status != http.StatusForbidden || !strings.Contains(string(body), "Only a super administrator gives the Super Administrator role, and you do not hold the Super Administrator role") {
		t.Errorf("the marshal, acting as the Headquarters role, giving the cadet the role: status %d, want 403 naming the rule: %s", status, body)
	}
	toMarshal := fmt.Sprintf(`{"userId":%q,"role":"SuperAdministrator"}`, account(t, "marshal"))
	if status, body := governor.do(ctx, http.MethodPost, crewMethod("assign-crew-role"), []byte(toMarshal)); status != http.StatusNoContent {
		t.Errorf("the governor making the marshal a super administrator: status %d, want 204: %s", status, body)
	}
}

// TestSuperAdministrator_givingTheRoleEndsNothing proves on the served stack that giving
// the Super Administrator role ends nothing the person held before it. The marshal
// holds a session of her own and one she started as the rigger, a token she issued for
// the droids outlet, and a browser she asked to be remembered, which signs her in on
// her password alone. The governor gives her the role: the three sessions answer as
// before, the token acts as before, and the remembered browser still signs her in on
// her password alone. The role is in force on a session she holds, with no new
// sign-in: the session she started as the rigger, who is seated at Bastion, enters
// Bastion from then on, which admits only its directory's people and a super
// administrator, since the exemption follows the person behind a session. A browser she
// never asked to be remembered is asked for the code at her next sign-in.
//
// Demonstrates: auth.super-administrator.
func TestSuperAdministrator_givingTheRoleEndsNothing(t *testing.T) {
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
	// answers fails the test unless each of the sessions answers want.
	answers := func(when string, sessions map[string]*browser, want int) {
		t.Helper()
		for name, b := range sessions {
			if status, body := b.do(ctx, http.MethodGet, userDomains, nil); status != want {
				t.Errorf("%s %s: status %d, want %d: %s", name, when, status, want, body)
			}
		}
	}
	// The browser she asked to be remembered signs in on the password alone.
	remembered := newBrowser(t, s, consoleAPI)
	remembered.signIn(ctx, "marshal")
	remembered.logout(ctx)
	if status, body := remembered.login(ctx, "marshal", personaPassword); status != http.StatusOK || heldForFactor(t, body) {
		t.Fatalf("the remembered browser's sign-in before the role: status %d: %s, want 200 with no second factor asked", status, body)
	}
	held := map[string]*browser{
		"the marshal's own session":                       own,
		"the marshal's session as the rigger":             viewing,
		"the marshal's session in the remembered browser": remembered,
	}
	answers("before the role", held, http.StatusOK)
	if got, want := viewing.domains(ctx), []string{anvil}; !slices.Equal(got, want) {
		t.Fatalf("the chart of the session the marshal started as the rigger, before the role = %v, want %v: Bastion closed to it", got, want)
	}
	// The marshal issues herself a token for the droids outlet; the answer is never
	// printed, since it carries the secret.
	status, body := own.do(ctx, http.MethodPost, consoleAPI+"/user/tokens", []byte(`{"name":"patrol script","outlet":"droids"}`))
	var issued struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &issued); err != nil || status != http.StatusOK || issued.Token == "" {
		t.Fatalf("the marshal issuing a token: status %d, want 200 with the token", status)
	}
	if status, body := droid(ctx, t, s, issued.Token, http.MethodGet, "/droids/features", nil); status != http.StatusOK {
		t.Fatalf("the marshal's token before the role: status %d, want 200: %s", status, body)
	}

	toMarshal := fmt.Sprintf(`{"userId":%q,"role":"SuperAdministrator"}`, account(t, "marshal"))
	if status, body := governor.do(ctx, http.MethodPost, crewMethod("assign-crew-role"), []byte(toMarshal)); status != http.StatusNoContent {
		t.Fatalf("the governor making the marshal a super administrator: status %d, want 204: %s", status, body)
	}
	answers("after the marshal was given the role", held, http.StatusOK)
	answers("after giving the role", map[string]*browser{"the governor's session, the giver's": governor}, http.StatusOK)
	if status, body := droid(ctx, t, s, issued.Token, http.MethodGet, "/droids/features", nil); status != http.StatusOK {
		t.Errorf("the token the marshal issued before the role, after it: status %d, want 200: %s", status, body)
	}
	// A session she held acts with the role from then on: the rigger's seat at Bastion
	// opens to the session she started as the rigger.
	if got, want := viewing.domains(ctx), []string{anvil, bastion}; !slices.Equal(got, want) {
		t.Errorf("the chart of the session the marshal started as the rigger, after the role = %v, want %v", got, want)
	}
	// The browser is remembered still: her password is followed by no code.
	remembered.logout(ctx)
	if status, body := remembered.login(ctx, "marshal", personaPassword); status != http.StatusOK || heldForFactor(t, body) {
		t.Errorf("the remembered browser's sign-in after the role: status %d: %s, want 200 with no second factor asked", status, body)
	}
	// A browser she never asked to be remembered is asked for the code.
	fresh := newBrowser(t, s, consoleAPI)
	if status, body := fresh.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET session before the marshal's sign-in from another browser: status %d: %s", status, body)
	}
	if status, body := fresh.login(ctx, "marshal", personaPassword); status != http.StatusOK || !heldForFactor(t, body) {
		t.Errorf("the marshal's sign-in from a browser she never asked to be remembered: status %d: %s, want 200 holding the sign-in for the second factor", status, body)
	}
}
