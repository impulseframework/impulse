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
// The fleet's own people do not make the way in theirs. The pilot is made Headquarters,
// the role that gives the crew's roles and manages its accounts for the whole fleet,
// and her session, a password's, does not enter Bastion: she is refused every act that
// would change who administers Bastion or hand her its marshal's account. Those are
// giving herself Bastion's marshal's role, taking it from the marshal Bastion has, an
// account that starts with it, an invitation that carries it, giving it to Bastion's
// group or taking it from the group, a place in the group or the marshal's place taken
// from it, and for Bastion's marshal a password set, required to change or removed, a
// new username, a new address, an identity unlinked, and the account disabled, enabled
// or deleted, each in a sentence that says who does it. In Anvil, which admits her, she
// names a marshal as Headquarters does, and the governor, a super administrator, gives
// Bastion's role. A session that views the application as Bastion's marshal gains
// nothing either: it does not enter Bastion, and the call that Bastion's marshal makes
// in her own session is refused it.
//
// The rigger changes her password while Bastion is directory only. Her other sessions
// end, and the one the change goes on in is the continuation of the session that made
// it, a password and the code: Bastion admits it still, so its marshal is not closed out
// of the sector she administers by changing her password.
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

	// The fleet's own people do not make that way in theirs. The governor makes the pilot
	// Headquarters. Her session does not enter Bastion, so while Bastion is directory
	// only who holds its marshal's role, and the account of the marshal it has, are not
	// hers to change: a super administrator's (the governor gave the role above), or
	// Bastion's own marshal's.
	pilot := newBrowser(t, s, consoleAPI)
	pilot.signIn(ctx, "pilot")
	pilotID := crewUserID(ctx, t, governor, "pilot").String()
	give("making the pilot Headquarters", "assign-crew-role", map[string]any{"userId": pilotID, "role": "Headquarters"})
	// The rigger is in Bastion's group again, beside the role of her own, and the link
	// her sign-in through the Workspace made is the identity an unlink would name.
	give("putting the rigger in Bastion's group again", "add-crew-group-member", place)
	var riggersLink string
	for _, row := range crewList(ctx, t, governor, "crew-session-identities") {
		if row["userId"] == riggerID {
			riggersLink = cell[string](t, row, "id")
		}
	}
	if riggersLink == "" {
		t.Fatal("the rigger has no identity link, want the one her sign-in through the Workspace made")
	}
	if status, body := pilot.do(ctx, http.MethodGet, sectorPath(bastion, "wings"), nil); status != http.StatusNotFound {
		t.Fatalf("GET Bastion's wings as the pilot, Headquarters signed in with a password: status %d, want 404: Bastion keeps the fleet's own people out: %s", status, body)
	}
	const (
		roleKept    = "SectorMarshal is a role that administers a sector, and this sector admits only people who sign in through its own directory, which your sign-in does not enter. Who holds the role there is changed by one of its administrators, or by a super administrator"
		accountKept = "This person administers a sector that admits only people who sign in through its own directory, which your sign-in does not enter. Their account is changed by one of that sector's administrators, or by a super administrator"
	)
	for _, refused := range []struct {
		what   string
		method string
		body   map[string]any
		says   string
	}{
		{"giving herself Bastion's marshal's role", "assign-crew-role", map[string]any{"userId": pilotID, "role": "SectorMarshal", "domain": bastion}, roleKept},
		{"taking the role from Bastion's marshal", "remove-crew-role", atBastion, roleKept},
		{
			"making an account that starts as Bastion's marshal", "create-crew-user",
			map[string]any{"username": "pilots-friend", "password": "a password the pilot chose", "roles": []string{"SectorMarshal"}, "domain": bastion}, roleKept,
		},
		{"inviting someone to be Bastion's marshal", "invite-crew-user", map[string]any{"email": "friend@example.com", "roles": []string{"SectorMarshal"}, "domain": bastion}, roleKept},
		{"giving Bastion's group the role", "give-crew-group-role", map[string]any{"groupId": wardroom, "role": "SectorMarshal", "domain": bastion}, roleKept},
		{"taking the role from Bastion's group", "take-crew-group-role", map[string]any{"groupId": wardroom, "role": "SectorMarshal", "domain": bastion}, roleKept},
		{"putting herself in Bastion's group, which holds the role", "add-crew-group-member", map[string]any{"groupId": wardroom, "userId": pilotID}, roleKept},
		{"taking Bastion's marshal out of Bastion's group", "remove-crew-group-member", place, roleKept},
		{"setting a password for Bastion's marshal", "set-crew-temporary-password", map[string]any{"userId": riggerID, "password": "a password the pilot chose"}, accountKept},
		{"requiring Bastion's marshal to change her password", "require-crew-password-change", map[string]any{"userId": riggerID}, accountKept},
		{"removing the password of Bastion's marshal", "remove-crew-password", map[string]any{"userId": riggerID}, accountKept},
		{"renaming Bastion's marshal", "rename-crew-user", map[string]any{"userId": riggerID, "username": "rigger-renamed"}, accountKept},
		{"changing the address of Bastion's marshal", "set-crew-user-email", map[string]any{"userId": riggerID, "email": "pilot@elsewhere.example"}, accountKept},
		{"unlinking the sign-in identity of Bastion's marshal", "unlink-crew-session-identity", map[string]any{"identityId": riggersLink}, accountKept},
		{"disabling the account of Bastion's marshal", "disable-crew-user", map[string]any{"userId": riggerID}, accountKept},
		{"enabling the account of Bastion's marshal", "enable-crew-user", map[string]any{"userId": riggerID}, accountKept},
		{"deleting the account of Bastion's marshal", "delete-crew-user", map[string]any{"userId": riggerID}, accountKept},
	} {
		status, answer := pilot.do(ctx, http.MethodPost, crewMethod(refused.method), jsonBody(t, refused.body))
		if status != http.StatusForbidden || !strings.Contains(string(answer), refused.says) {
			t.Errorf("%s as the pilot, Headquarters whose session Bastion does not admit: status %d, want 403 saying %q: %s", refused.what, status, refused.says, answer)
		}
	}
	stands("the rigger, Bastion's marshal, once Headquarters was refused", rigger, []string{anvil, bastion, cinder}, "", "")
	if got := pilot.domains(ctx); slices.Contains(got, bastion) {
		t.Errorf("the pilot's chart once she was refused = %v, want Bastion out of it", got)
	}
	// Anvil admits her, and there Headquarters names a marshal as before.
	if status, answer := pilot.do(ctx, http.MethodPost, crewMethod("assign-crew-role"), jsonBody(t, map[string]any{"userId": riggerID, "role": "SectorMarshal", "domain": anvil})); status != http.StatusNoContent {
		t.Errorf("making the rigger a marshal of Anvil as the pilot, Headquarters: status %d, want 204: Anvil is not directory only: %s", status, answer)
	}

	// Nor does viewing the application as Bastion's marshal make her way in the viewer's.
	// The governor makes the rigger Headquarters too, so the whole fleet's method that
	// gives a role is hers to run, and in her own session, a password and the code, she
	// gives the bosun Bastion's marshal's role. The fleet's marshal views the application
	// as her with the right to run methods kept: that session is established by the
	// impersonation, so Bastion does not admit it, and the same call is refused it.
	give("making the rigger Headquarters", "assign-crew-role", map[string]any{"userId": riggerID, "role": "Headquarters"})
	viewing := newBrowser(t, s, consoleAPI)
	viewing.signIn(ctx, "marshal")
	asRigger := fmt.Sprintf(`{"kind":"user","principal":%q,"reason":"walkthrough","mask":["List","Read","Execute"]}`, riggerID)
	if status, body := viewing.impersonate(ctx, asRigger); status != http.StatusOK {
		t.Fatalf("the fleet's marshal viewing as the rigger with Execute kept: status %d: %s", status, body)
	}
	if info := viewing.session(ctx); info["username"] != "rigger" || info["impersonation"] == nil {
		t.Fatalf("the session after the fleet's marshal views as the rigger = %v, want the rigger's, impersonated", info)
	}
	if status, body := viewing.do(ctx, http.MethodGet, sectorPath(bastion, "wings"), nil); status != http.StatusNotFound {
		t.Errorf("GET Bastion's wings viewing as its marshal: status %d, want 404: the way in is a password sign-in's, never a session that views as the person: %s", status, body)
	}
	bosunToBastion := jsonBody(t, map[string]any{"userId": crewUserID(ctx, t, governor, "bosun").String(), "role": "SectorMarshal", "domain": bastion})
	if status, answer := viewing.do(ctx, http.MethodPost, crewMethod("assign-crew-role"), bosunToBastion); status != http.StatusForbidden || !strings.Contains(string(answer), roleKept) {
		t.Errorf("giving Bastion's marshal's role viewing as Bastion's marshal: status %d, want 403 saying %q: %s", status, roleKept, answer)
	}
	if status, answer := rigger.do(ctx, http.MethodPost, crewMethod("assign-crew-role"), bosunToBastion); status != http.StatusNoContent {
		t.Errorf("giving Bastion's marshal's role as Bastion's marshal herself, signed in with a password and the code: status %d, want 204: %s", status, answer)
	}
	give("taking Bastion's marshal's role back from the bosun", "remove-crew-role", map[string]any{"userId": crewUserID(ctx, t, governor, "bosun").String(), "role": "SectorMarshal", "domain": bastion})

	// She changes her password while Bastion is directory only. The change ends every
	// session she has and goes on in a new one, the continuation of the session that
	// made the change: a password and the code, so Bastion admits it as its marshal's
	// and she is not signed out of the sector she administers. Her other sessions are
	// over, the Workspace's among them.
	const riggersNext = "a splice in the forestay"
	change := jsonBody(t, map[string]string{"oldPassword": personaPassword, "newPassword": riggersNext})
	if status, body := rigger.do(ctx, http.MethodPost, consoleAPI+"/user/password/change", change); status != http.StatusOK {
		t.Fatalf("the rigger changing her password: status %d: %s", status, body)
	}
	stands("the rigger, Bastion's marshal, in the session her password change goes on in", rigger, []string{anvil, bastion, cinder}, "", "")
	for name, b := range map[string]*browser{"her other browser": another, "her remembered browser": remembered, "her session through the Workspace": through} {
		if sess := b.session(ctx); sess["authenticated"] != false {
			t.Errorf("%s once the rigger changed her password = %v, want no session: a change ends the person's other sessions", name, sess)
		}
	}

	if status, body := rigger.do(ctx, http.MethodGet, sectorPath(bastion, "directory-only"), nil); status != http.StatusOK || strings.TrimSpace(string(body)) != `{"directoryOnly":true}` {
		t.Fatalf("Bastion's switch as its marshal reads it, signed in with a password: status %d %s, want 200 saying it is on", status, body)
	}
	if status, body := rigger.do(ctx, http.MethodPost, sectorPath(bastion, "end-sector-directory-only"), []byte(`{}`)); status != http.StatusOK {
		t.Fatalf("turning Bastion's directory only off as its marshal, signed in with a password: status %d: %s", status, body)
	}
	if status, body := rigger.do(ctx, http.MethodGet, sectorPath(bastion, "directory-only"), nil); status != http.StatusOK || strings.TrimSpace(string(body)) != `{"directoryOnly":false}` {
		t.Errorf("Bastion's switch once its marshal turned it off: status %d %s, want 200 saying it is off", status, body)
	}
	// Through the Workspace again, in a session opened since the change ended the one
	// she had: the link stands, so nothing asks for her password.
	again := primedBrowser(ctx, t, s)
	if status, location := again.loginConsoleGoogle(ctx, "rigger"); status != http.StatusFound || location != consolePage {
		t.Fatalf("the rigger's second sign-in through the fleet's Workspace: status %d to %q, want %d to %q", status, location, http.StatusFound, consolePage)
	}
	stands("the rigger through the Workspace, once Bastion is not directory only", again, []string{anvil, bastion, cinder}, "", "")
}
