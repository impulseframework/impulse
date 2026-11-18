//go:build skipAuth

package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestDirectoryOnly_aSectorsOwnMarshalEntersWithAPassword proves the administrators' way
// into their own sector on the served stack: a sector that is directory only still
// admits a person who holds the marshal's role in that sector itself, signed in with a
// password and the code sent to their email, so that a sector whose directory has a
// problem can be mended by its own marshal.
//
// Bastion is directory only. The rigger is one of its crew and signs in with a password
// and the code: she is kept out, her chart says why, and Bastion's list of its people
// says she cannot enter. The marshal's role at Bastion from an hour on changes nothing,
// since an assignment outside its period is no assignment. Held through a group of
// Bastion's own, or held herself, the role opens Bastion to the session she already has,
// at its next request, and the list says nothing of her any more. A browser of hers
// that the server does not remember is held for the code, as every password sign-in of
// the crew is, and reaches nothing before it; one she asked to be remembered is not
// asked again and enters. The way in is the password's alone: the same person, signed
// in through the fleet's Workspace, which is another sector's directory, holds the same
// role and is kept out, with the reason. A marshal in every sector, as the fleet's own
// team is, is kept out too, which the test of the switch's rules beside this one shows.
// The role taken away closes the door at the next request of the sessions she has, not
// at her next sign-in. Given it again, she turns Bastion's directory only off from the
// inside, which is what the way in is for.
//
// Demonstrates: tenancy.directory-only, auth.second-factor, auth.trusted-device.
func TestDirectoryOnly_aSectorsOwnMarshalEntersWithAPassword(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)

	governor := newBrowser(t, s, consoleAPI)
	governor.signIn(ctx, "governor")
	rigger := newBrowser(t, s, consoleAPI)
	rigger.signIn(ctx, "rigger")
	riggerID := crewUserID(ctx, t, governor, "rigger").String()

	const (
		keptOut             = "Has not signed in through this sector's directory, so cannot enter while this sector is directory only"
		closedToHerSignIn   = "Bastion admits only people who sign in through its own directory. You signed in with a password."
		throughTheWorkspace = "Bastion admits only people who sign in through its own directory. You signed in through Google Workspace example."
	)
	// stands fails the test unless the browser's chart, the reason its chart gives for
	// Bastion, the answer of Bastion's routes and the rigger's row in Bastion's list of
	// its people are as given: closed is the reason for a session Bastion keeps out and
	// empty for one it admits, and listed is what the row says of the person, whichever
	// session of hers asks.
	stands := func(what string, b *browser, chart []string, closed, listed string) {
		t.Helper()

		if got := b.domains(ctx); !slices.Equal(got, chart) {
			t.Errorf("%s: the chart = %v, want %v", what, got, chart)
		}
		if got := b.closed(ctx)[bastion]; got != closed {
			t.Errorf("%s: the chart names Bastion closed saying %q, want %q", what, got, closed)
		}
		want := http.StatusNotFound
		if closed == "" {
			want = http.StatusOK
		}
		if status, body := b.do(ctx, http.MethodGet, sectorPath(bastion, "wings"), nil); status != want {
			t.Errorf("%s: GET Bastion's wings: status %d, want %d: %s", what, status, want, body)
		}
		if got := cannotEnter(ctx, t, governor)["rigger"]; got != listed {
			t.Errorf("%s: Bastion's list of its people says of the rigger %q, want %q", what, got, listed)
		}
	}
	give := func(what, method string, body map[string]any) {
		t.Helper()

		if status, answer := governor.do(ctx, http.MethodPost, crewMethod(method), jsonBody(t, body)); status != http.StatusNoContent {
			t.Fatalf("%s as the governor: status %d, want 204: %s", what, status, answer)
		}
	}
	atBastion := map[string]any{"userId": riggerID, "role": "SectorMarshal", "domain": bastion}

	// One of Bastion's crew, with a password and the code: kept out.
	stands("the rigger, of Bastion's crew", rigger, []string{anvil}, closedToHerSignIn, keptOut)

	// The marshal's role at Bastion from an hour on is not hers yet.
	fromLater := map[string]any{"startsAt": time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano)}
	for key, value := range atBastion {
		fromLater[key] = value
	}
	give("giving the rigger the marshal's role at Bastion from an hour on", "assign-crew-role", fromLater)
	stands("the rigger, whose marshal's role at Bastion has not begun", rigger, []string{anvil}, closedToHerSignIn, keptOut)
	give("taking the role that has not begun back", "remove-crew-role", atBastion)

	// A group of Bastion's own holds the marshal's role at Bastion, and the rigger is put
	// in it: the role is held in Bastion itself, so it counts as her own would. Out of
	// the group she is out of Bastion, at the next request.
	status, body := governor.do(ctx, http.MethodPost, sectorPath(bastion, "create-crew-sector-group"), []byte(`{"name":"Bastion wardroom","description":"The people who run Bastion."}`))
	if status != http.StatusOK {
		t.Fatalf("making a group of Bastion's as the governor: status %d: %s", status, body)
	}
	wardroom := cell[string](t, decodeRow(t, body), "id")
	give("giving Bastion's group the marshal's role at Bastion", "give-crew-group-role", map[string]any{"groupId": wardroom, "role": "SectorMarshal", "domain": bastion})
	place := map[string]any{"groupId": wardroom, "userId": riggerID}
	give("putting the rigger in Bastion's group", "add-crew-group-member", place)
	stands("the rigger, a marshal of Bastion through a group of Bastion's own", rigger, []string{anvil, bastion}, "", "")
	give("taking the rigger out of Bastion's group", "remove-crew-group-member", place)
	stands("the rigger, out of Bastion's group again", rigger, []string{anvil}, closedToHerSignIn, keptOut)

	// The role is hers at Bastion itself. The session she has enters at its next
	// request: what a session reaches is decided on each one.
	give("giving the rigger the marshal's role at Bastion", "assign-crew-role", atBastion)
	stands("the rigger, a marshal of Bastion, with a password and the code", rigger, []string{anvil, bastion}, "", "")
	if got := cannotEnter(ctx, t, rigger)["bosun"]; got != keptOut {
		t.Errorf("Bastion's list of its people as the rigger reads it says of the bosun %q, want %q: the way in is its marshal's alone", got, keptOut)
	}

	// A browser the server does not remember: the password is held for the code, and
	// until the code is entered there is no session and nothing of Bastion answers. The
	// code opens the session, and it enters Bastion.
	another := primedBrowser(ctx, t, s)
	if status, body := another.login(ctx, "rigger", personaPassword); status != http.StatusOK || !heldForFactor(t, body) {
		t.Fatalf("the rigger's password sign-in from another browser: status %d: %s, want 200 holding the sign-in for the code", status, body)
	}
	if sess := another.session(ctx); sess["authenticated"] != false {
		t.Fatalf("the session while the sign-in waits for the code = %v, want none", sess)
	}
	if status, body := another.do(ctx, http.MethodGet, sectorPath(bastion, "wings"), nil); status != http.StatusUnauthorized {
		t.Errorf("GET Bastion's wings with the password and without the code: status %d, want 401: %s", status, body)
	}
	another.secondFactor(ctx, "rigger")
	stands("the rigger's other browser, once the code is entered", another, []string{anvil, bastion}, "", "")

	// The browser she asked to be remembered is not asked for the code again, for as
	// long as it is remembered, and enters Bastion.
	s.codeMu.Lock()
	token := s.trusted[consoleAPI+" rigger"]
	s.codeMu.Unlock()
	if token == nil {
		t.Fatal("the stack holds no remembered browser of the rigger's, want the one her first sign-in asked for")
	}
	remembered := primedBrowser(ctx, t, s)
	remembered.remember(token)
	if status, body := remembered.login(ctx, "rigger", personaPassword); status != http.StatusOK || heldForFactor(t, body) {
		t.Fatalf("the rigger's password sign-in from the browser she asked to be remembered: status %d: %s, want 200 and no code asked", status, body)
	}
	stands("the rigger's remembered browser", remembered, []string{anvil, bastion}, "", "")

	// The way in is the password's alone. The rigger signs in through the fleet's
	// Workspace, Cinder's directory and not Bastion's, and proves the account is hers
	// with her password: the link is her first sign-in through Cinder's directory, so it
	// seats her at Cinder, and the session is the Workspace's. It holds the marshal's
	// role at Bastion as her others do and is kept out, with the reason; the list says
	// nothing of her, since she has a way in, and her password sessions go on entering.
	through := primedBrowser(ctx, t, s)
	held := consoleLogin + "?pending=confirmation&returnUrl=" + url.QueryEscape(consolePage)
	if status, location := through.loginConsoleGoogle(ctx, "rigger"); status != http.StatusFound || location != held {
		t.Fatalf("the rigger's sign-in through the fleet's Workspace: status %d to %q, want %d to %q", status, location, http.StatusFound, held)
	}
	if status, body := through.do(ctx, http.MethodPost, consoleAPI+"/user/pending/confirm", jsonBody(t, map[string]string{"password": personaPassword})); status != http.StatusOK {
		t.Fatalf("confirming the sign-in with the rigger's password: status %d: %s", status, body)
	}
	if sess := through.session(ctx); sess["authenticated"] != true || sess["username"] != "rigger" {
		t.Fatalf("the session after the confirmation = %v, want authenticated as the rigger", sess)
	}
	stands("the rigger, a marshal of Bastion, signed in through another sector's directory", through, []string{anvil, cinder}, throughTheWorkspace, "")
	stands("the rigger's password session beside it", rigger, []string{anvil, bastion, cinder}, "", "")

	// The role taken away closes the door at the next request of every session she has.
	give("taking the marshal's role at Bastion back from the rigger", "remove-crew-role", atBastion)
	for name, b := range map[string]*browser{"the session she had": rigger, "her other browser": another, "her remembered browser": remembered} {
		stands(fmt.Sprintf("the rigger once the role is taken away, %s", name), b, []string{anvil, cinder}, closedToHerSignIn, keptOut)
	}
	stands("the rigger once the role is taken away, through the Workspace", through, []string{anvil, cinder}, throughTheWorkspace, keptOut)

	// Given it again, she mends the sector from the inside: she reads the switch and
	// turns it off, signed in with her password, and Bastion admits its people however
	// they signed in.
	give("giving the rigger the marshal's role at Bastion again", "assign-crew-role", atBastion)
	if status, body := rigger.do(ctx, http.MethodGet, sectorPath(bastion, "directory-only"), nil); status != http.StatusOK || strings.TrimSpace(string(body)) != `{"directoryOnly":true}` {
		t.Fatalf("Bastion's switch as its marshal reads it, signed in with a password: status %d %s, want 200 saying it is on", status, body)
	}
	if status, body := rigger.do(ctx, http.MethodPost, sectorPath(bastion, "end-sector-directory-only"), []byte(`{}`)); status != http.StatusOK {
		t.Fatalf("turning Bastion's directory only off as its marshal, signed in with a password: status %d: %s", status, body)
	}
	if status, body := rigger.do(ctx, http.MethodGet, sectorPath(bastion, "directory-only"), nil); status != http.StatusOK || strings.TrimSpace(string(body)) != `{"directoryOnly":false}` {
		t.Errorf("Bastion's switch once its marshal turned it off: status %d %s, want 200 saying it is off", status, body)
	}
	stands("the rigger through the Workspace, once Bastion is not directory only", through, []string{anvil, bastion, cinder}, "", "")
}
