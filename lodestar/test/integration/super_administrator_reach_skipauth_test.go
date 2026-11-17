//go:build skipAuth

package integration

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// TestSuperAdministrator_entersEverySector proves the one exemption from a sector's
// sign-in settings on the served stack. Bastion admits its people through its directories
// only, and the governor withdraws the one directory it declares, so no sign-in meets its
// declaration. The governor, a super administrator signed in with the password, charts
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
	declared := directories(ctx, t, governor, bastion)
	if len(declared) != 1 {
		t.Fatalf("Bastion's directories = %+v, want the seeded fleet directory alone", declared)
	}
	withdraw := `[{"op":"remove","path":"/sectors/` + bastion + `/sector-directories/` + declared[0].ID + `"}]`
	if status, body := governor.do(ctx, http.MethodPatch, consoleAPI+"/resources", []byte(withdraw)); status != http.StatusOK {
		t.Fatalf("withdrawing Bastion's directory: status %d: %s", status, body)
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
