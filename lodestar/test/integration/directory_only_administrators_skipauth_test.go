//go:build skipAuth

package integration

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/session/signin"
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
// the role that gives the crew's roles for the whole fleet and manages the accounts no
// sector manages, and her session, a password's, does not enter Bastion. An account a
// sector manages is not Headquarters' to change at all, and the rigger's sign-in through
// the Workspace made her account Cinder's, so the governor first takes that link off
// from Cinder's side: she is then the fleet's to manage, holding roles in three sectors
// and signing in through no sector's directory, and there the rule on a directory-only
// sector's marshal is what refuses Headquarters. The pilot is refused every act that
// would change who administers Bastion or hand her its marshal's account. Those are
// giving herself Bastion's marshal's role, taking it from the marshal Bastion has, an
// invitation that carries it, giving it to Bastion's group or taking it from the group,
// a place in the group or the marshal's place taken from it, and for Bastion's marshal
// a password required to change or removed, a new username, a new address, an identity
// unlinked, and the account disabled, enabled or deleted, each in a sentence that says
// who does it. Nor do the rows she reads offer her those acts on the marshal's account,
// which the bosun's row offers her and the marshal's row offers the governor; the row
// and the server count the marshal alike whichever way she holds the role, one that has
// not begun included. Nor do the rows of Bastion's group offer her to put a person in it
// or take one out while the group holds the marshal's role there, begun or not, which
// they offer the governor; with the role taken from the group they offer her both, as
// the rows of a group of the whole fleet's and of one of Anvil's do. Nor do the rows of
// the roles she reads offer her to take the marshal's role at Bastion away, from a person
// who holds it of their own, begun or not, or from Bastion's group, which they offer the
// governor; a role at Anvil, a role held in every sector and a role of the whole fleet's
// group they offer her. In Anvil, which
// admits her, she names a marshal as Headquarters does, and the governor, a super administrator, gives
// Bastion's role. The rigger then signs in through the Workspace again, which makes her
// account Cinder's once more. A session established by an impersonation gains nothing either: one
// that views the application as Bastion's marshal does not enter Bastion, and one the
// marshal herself starts as the Headquarters role, from a browser Bastion admits, does
// not enter it and is refused the call her own session makes. Reading is held by the
// same rule and by nothing beside it: from a browser Bastion admits, Bastion's marshal
// views as one of the crew of every sector, and acts as the marshal's role, and each
// session reads the wings of Anvil, which admits any sign-in, and not Bastion's.
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
	// The rigger is in Bastion's group again, beside the role of her own. The link her
	// sign-in through the Workspace made makes her account Cinder's to manage, and an
	// account a sector manages is not Headquarters' to change at all, so the governor, a
	// marshal of Cinder, takes that link off from Cinder's side; she holds a password, so
	// it is not her last way in. She then holds roles in three sectors and signs in
	// through no sector's directory: the fleet manages her account, and what refuses
	// Headquarters below is the rule on a directory-only sector's marshal.
	give("putting the rigger in Bastion's group again", "add-crew-group-member", place)
	var workspaceLink string
	for _, row := range crewList(ctx, t, governor, "crew-session-identities") {
		if row["userId"] == riggerID {
			workspaceLink = cell[string](t, row, "id")
		}
	}
	if workspaceLink == "" {
		t.Fatal("the rigger has no identity link, want the one her sign-in through the Workspace made")
	}
	if status, body := governor.do(ctx, http.MethodPost, sectorPath(cinder, "unlink-crew-sector-session-identity"), jsonBody(t, map[string]string{"identityId": workspaceLink})); status != http.StatusNoContent {
		t.Fatalf("taking the rigger's link through the Workspace off from Cinder's side as the governor: status %d, want 204: %s", status, body)
	}
	// The identity an unlink would name is one of a personal Google account's, written as
	// the crew's open Google sign-up writes one, under the connection of any Google
	// account, which no sector's directory can be.
	id, err := impulse.NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	riggersLink := id.String()
	if _, err := s.db.Apply(ctx, []*spanner.Mutation{spanner.InsertMap("CrewSessionIdentities", map[string]any{
		"Id": riggersLink, "UserId": riggerID, "Method": "google", "Connection": signin.GoogleAnyAccountConnection, "Subject": "personal-subject-of-rigger",
		"CreatedAt": spanner.CommitTimestamp, "LastUsedAt": spanner.CommitTimestamp,
	})}); err != nil {
		t.Fatalf("writing the rigger's identity of a personal Google account: %v", err)
	}
	if status, body := pilot.do(ctx, http.MethodGet, sectorPath(bastion, "wings"), nil); status != http.StatusNotFound {
		t.Fatalf("GET Bastion's wings as the pilot, Headquarters signed in with a password: status %d, want 404: Bastion keeps the fleet's own people out: %s", status, body)
	}
	const (
		roleKept    = "Sector marshal is a role that administers a sector, and this sector admits only people who sign in through its own directory, which your sign-in does not enter. Who holds the role there is changed by one of its administrators, or by a super administrator."
		accountKept = "This person administers a sector that admits only people who sign in through its own directory, which your sign-in does not enter. Their account is changed by one of that sector's administrators, or by a super administrator."
	)
	// What the server refuses the pilot on the account of Bastion's marshal, the rows do
	// not offer her. The seven acts on an account below are each a method that names the
	// person's row; the unlink names the identity's row. Each is in the row of the bosun,
	// who administers no sector, so what is missing from the rigger's is the row's and not
	// the grant's, and the governor, a super administrator, is offered every one on the
	// rigger's row. The acts the server does not refuse her stay, as on any other row.
	accountActs := []string{"DeleteCrewUser", "DisableCrewUser", "EnableCrewUser", "RemoveCrewPassword", "RenameCrewUser", "RequireCrewPasswordChange", "SetCrewUserEmail"}
	const unlink = "UnlinkCrewSessionIdentity"
	acts := func(offered []string) []string {
		return slices.DeleteFunc(slices.Clone(offered), func(method string) bool {
			return !slices.Contains(accountActs, method)
		})
	}
	others := func(offered []string) []string {
		return slices.DeleteFunc(slices.Clone(offered), func(method string) bool {
			return slices.Contains(accountActs, method)
		})
	}
	bosunID := crewUserID(ctx, t, governor, "bosun").String()
	// The bosun holds a role at Bastion alone, so Bastion would manage his account; a role
	// for the whole application makes it the fleet's to manage, and he still administers
	// no sector.
	give("giving the bosun Crew common for the whole application", "assign-crew-role", map[string]any{"userId": bosunID, "role": "CrewCommon"})
	pilotOnRigger := offeredOnRow(ctx, t, pilot, crewListedAccountsPath, riggerID)
	pilotOnBosun := offeredOnRow(ctx, t, pilot, crewListedAccountsPath, bosunID)
	if got := acts(pilotOnRigger); len(got) != 0 {
		t.Errorf("the rigger's row of the fleet's accounts, as the pilot reads it, offers %v, want none of the acts on the account of Bastion's marshal", got)
	}
	if got := acts(pilotOnBosun); !slices.Equal(got, accountActs) {
		t.Errorf("the bosun's row of the fleet's accounts, as the pilot reads it, offers the acts on an account %v, want %v", got, accountActs)
	}
	if got, want := others(pilotOnRigger), others(pilotOnBosun); len(want) == 0 || !slices.Equal(got, want) {
		t.Errorf("the rigger's row of the fleet's accounts, as the pilot reads it, offers beside them %v, want %v, as the bosun's row does", got, want)
	}
	if got := acts(offeredOnPage(ctx, t, pilot, crewListedAccountsPath, riggerID)); len(got) != 0 {
		t.Errorf("the rigger's account's page, as the pilot reads it, offers %v, want none of the acts on the account of Bastion's marshal", got)
	}
	if got := acts(offeredOnRow(ctx, t, governor, crewListedAccountsPath, riggerID)); !slices.Equal(got, accountActs) {
		t.Errorf("the rigger's row of the fleet's accounts, as the governor reads it, offers the acts on an account %v, want %v", got, accountActs)
	}
	if offered := offeredOnRow(ctx, t, pilot, "crew-listed-identities", riggersLink); slices.Contains(offered, unlink) {
		t.Errorf("the rigger's sign-in identity, as the pilot reads it, offers %v, want no %s", offered, unlink)
	}
	if offered := offeredOnRow(ctx, t, governor, "crew-listed-identities", riggersLink); !slices.Contains(offered, unlink) {
		t.Errorf("the rigger's sign-in identity, as the governor reads it, offers %v, want %s", offered, unlink)
	}
	for _, refused := range []struct {
		what   string
		method string
		body   map[string]any
		says   string
	}{
		{"giving herself Bastion's marshal's role", "assign-crew-role", map[string]any{"userId": pilotID, "role": "SectorMarshal", "domain": bastion}, roleKept},
		{"taking the role from Bastion's marshal", "remove-crew-role", atBastion, roleKept},
		{"inviting someone to be Bastion's marshal", "invite-crew-user", map[string]any{"email": "friend@example.com", "roles": []string{"SectorMarshal"}, "domain": bastion}, roleKept},
		{"giving Bastion's group the role", "give-crew-group-role", map[string]any{"groupId": wardroom, "role": "SectorMarshal", "domain": bastion}, roleKept},
		{"taking the role from Bastion's group", "take-crew-group-role", map[string]any{"groupId": wardroom, "role": "SectorMarshal", "domain": bastion}, roleKept},
		{"putting herself in Bastion's group, which holds the role", "add-crew-group-member", map[string]any{"groupId": wardroom, "userId": pilotID}, roleKept},
		{"taking Bastion's marshal out of Bastion's group", "remove-crew-group-member", place, roleKept},
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
	// What the server refuses the pilot on Bastion's group, its rows do not offer her: the
	// group's row of the fleet's groups, read in the list or alone, does not offer to put a
	// person in it, nor the marshal's row of the group's people to take her out, while
	// the group holds the marshal's role there, one that has not begun included. The
	// governor is offered both on the same rows. Taken from the group, the role leaves
	// the group's rows as any other's: both are offered the pilot, and her call runs. A
	// group kept for the whole fleet, though it holds Bastion's marshal's role, and one
	// of Anvil's, which admits her, offer her both all along. Whatever else the row of
	// Bastion's group offers her, the row of Anvil's offers her too, so what is missing
	// is the row's and not the grant's.
	const (
		addMember    = "AddCrewGroupMember"
		removeMember = "RemoveCrewGroupMember"
	)
	offersGroup := func(what string, b *browser, who, group, member string, add, remove bool) {
		t.Helper()

		if got := slices.Contains(offeredOnRow(ctx, t, b, "crew-listed-groups", group), addMember); got != add {
			t.Errorf("%s: the group's row of the fleet's groups, as %s reads it, offers %s: %t, want %t", what, who, addMember, got, add)
		}
		if got := slices.Contains(offeredOnGroupMember(ctx, t, b, group, member), removeMember); got != remove {
			t.Errorf("%s: the member's row of the group's people, as %s reads it, offers %s: %t, want %t", what, who, removeMember, got, remove)
		}
	}
	withoutAdd := func(offered []string) []string {
		return slices.DeleteFunc(slices.Clone(offered), func(method string) bool {
			return method == addMember
		})
	}
	makeGroup := func(what string, body map[string]any) string {
		t.Helper()

		status, answer := governor.do(ctx, http.MethodPost, crewMethod("create-crew-group"), jsonBody(t, body))
		if status != http.StatusOK {
			t.Fatalf("%s as the governor: status %d, want 200: %s", what, status, answer)
		}

		return cell[string](t, decodeRow(t, answer), "id")
	}
	wardroomRole := map[string]any{"groupId": wardroom, "role": "SectorMarshal", "domain": bastion}
	offersGroup("Bastion's group, which holds the marshal's role there", pilot, "the pilot", wardroom, riggerID, false, false)
	offersGroup("Bastion's group, which holds the marshal's role there", governor, "the governor", wardroom, riggerID, true, true)
	if offered := offeredOnPage(ctx, t, pilot, "crew-listed-groups", wardroom); slices.Contains(offered, addMember) {
		t.Errorf("Bastion's group read alone, as the pilot reads it, offers %v, want no %s", offered, addMember)
	}
	if offered := offeredOnPage(ctx, t, governor, "crew-listed-groups", wardroom); !slices.Contains(offered, addMember) {
		t.Errorf("Bastion's group read alone, as the governor reads it, offers %v, want %s", offered, addMember)
	}
	pilotOnWardroom := offeredOnRow(ctx, t, pilot, "crew-listed-groups", wardroom)

	give("taking the marshal's role from Bastion's group", "take-crew-group-role", wardroomRole)
	laterRole := map[string]any{"startsAt": time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano)}
	for key, value := range wardroomRole {
		laterRole[key] = value
	}
	give("giving Bastion's group the marshal's role there from an hour on", "give-crew-group-role", laterRole)
	offersGroup("Bastion's group, whose marshal's role there has not begun", pilot, "the pilot", wardroom, riggerID, false, false)
	offersGroup("Bastion's group, whose marshal's role there has not begun", governor, "the governor", wardroom, riggerID, true, true)
	if status, answer := pilot.do(ctx, http.MethodPost, crewMethod("remove-crew-group-member"), jsonBody(t, place)); status != http.StatusForbidden || !strings.Contains(string(answer), roleKept) {
		t.Errorf("taking Bastion's marshal out of Bastion's group as the pilot, the group's role not begun: status %d, want 403 saying %q: %s", status, roleKept, answer)
	}

	give("taking the role that has not begun from Bastion's group", "take-crew-group-role", wardroomRole)
	offersGroup("Bastion's group, which holds no role", pilot, "the pilot", wardroom, riggerID, true, true)
	offersGroup("Bastion's group, which holds no role", governor, "the governor", wardroom, riggerID, true, true)
	if status, answer := pilot.do(ctx, http.MethodPost, crewMethod("remove-crew-group-member"), jsonBody(t, place)); status != http.StatusNoContent {
		t.Errorf("taking the rigger out of Bastion's group as the pilot, the group holding no role: status %d, want 204: %s", status, answer)
	}
	give("putting the rigger back in Bastion's group", "add-crew-group-member", place)
	give("giving Bastion's group the marshal's role there again", "give-crew-group-role", wardroomRole)

	fleetGroup := makeGroup("making a group kept for the whole fleet", map[string]any{"name": "Fleet watch", "description": "The fleet's own watch."})
	anvilGroup := makeGroup("making a group of Anvil's", map[string]any{"name": "Anvil wardroom", "description": "The people who run Anvil.", "domain": anvil})
	give("giving the fleet's group Bastion's marshal's role", "give-crew-group-role", map[string]any{"groupId": fleetGroup, "role": "SectorMarshal", "domain": bastion})
	give("giving Anvil's group Anvil's marshal's role", "give-crew-group-role", map[string]any{"groupId": anvilGroup, "role": "SectorMarshal", "domain": anvil})
	for _, group := range []struct{ what, id string }{{"the fleet's group, which holds Bastion's marshal's role", fleetGroup}, {"Anvil's group, which holds Anvil's marshal's role", anvilGroup}} {
		give("putting the bosun in "+group.what, "add-crew-group-member", map[string]any{"groupId": group.id, "userId": bosunID})
		offersGroup(group.what, pilot, "the pilot", group.id, bosunID, true, true)
		offersGroup(group.what, governor, "the governor", group.id, bosunID, true, true)
		give("taking the bosun out of "+group.what, "remove-crew-group-member", map[string]any{"groupId": group.id, "userId": bosunID})
	}
	if got, want := withoutAdd(pilotOnWardroom), withoutAdd(offeredOnRow(ctx, t, pilot, "crew-listed-groups", anvilGroup)); len(want) == 0 || !slices.Equal(got, want) {
		t.Errorf("Bastion's group's row, as the pilot reads it, offers beside %s %v, want %v, as the row of Anvil's group does", addMember, got, want)
	}

	// What the server refuses the pilot on who holds Bastion's marshal's role, the rows of
	// the roles she reads do not offer her: taking the role at Bastion from a person who
	// holds it of their own, begun or not, on the rows of the roles people hold and of the
	// roles held for a set time, and from Bastion's group, begun or not, on the rows of the
	// roles the groups hold. The governor is offered each. A role at Anvil, a role held in
	// every sector and a role of the whole fleet's group are offered her, and where she
	// runs one the call runs. The bosun, who administers no sector, is given each role of
	// a person's in turn and loses it before the next.
	const (
		removeRole    = "RemoveCrewRole"
		takeGroupRole = "TakeCrewGroupRole"
		heldRoles     = "crew-held-roles"
		takenRoles    = "crew-taken-roles"
		groupRoles    = "crew-listed-group-roles"
	)
	roleOffers := func(what, list, by, id, sector, method string, offered bool) {
		t.Helper()

		if got := slices.Contains(offeredOnRole(ctx, t, pilot, list, by, id, sector), method); got != offered {
			t.Errorf("%s: its row of %s, as the pilot reads it, offers %s: %t, want %t", what, list, method, got, offered)
		}
		if got := slices.Contains(offeredOnRole(ctx, t, governor, list, by, id, sector), method); !got {
			t.Errorf("%s: its row of %s, as the governor reads it, offers %s: false, want true", what, list, method)
		}
	}
	bosunRole := func(sector string, period map[string]any) map[string]any {
		role := map[string]any{"userId": bosunID, "role": "SectorMarshal", "everyDomain": true}
		if sector != "" {
			role = map[string]any{"userId": bosunID, "role": "SectorMarshal", "domain": sector}
		}
		for key, value := range period {
			role[key] = value
		}

		return role
	}
	roleOffers("the rigger's own marshal's role at Bastion", heldRoles, "userId", riggerID, bastion, removeRole, false)

	give("giving the bosun the marshal's role at Bastion from an hour on", "assign-crew-role", bosunRole(bastion, map[string]any{"startsAt": time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano)}))
	roleOffers("the bosun's marshal's role at Bastion from an hour on", heldRoles, "userId", bosunID, bastion, removeRole, false)
	if status, answer := pilot.do(ctx, http.MethodPost, crewMethod("remove-crew-role"), jsonBody(t, bosunRole(bastion, nil))); status != http.StatusForbidden || !strings.Contains(string(answer), roleKept) {
		t.Errorf("taking the bosun's marshal's role at Bastion, not begun, as the pilot: status %d, want 403 saying %q: %s", status, roleKept, answer)
	}
	give("taking the bosun's marshal's role at Bastion that has not begun", "remove-crew-role", bosunRole(bastion, nil))

	give("giving the bosun the marshal's role at Anvil", "assign-crew-role", bosunRole(anvil, nil))
	roleOffers("the bosun's marshal's role at Anvil", heldRoles, "userId", bosunID, anvil, removeRole, true)
	if status, answer := pilot.do(ctx, http.MethodPost, crewMethod("remove-crew-role"), jsonBody(t, bosunRole(anvil, nil))); status != http.StatusNoContent {
		t.Errorf("taking the bosun's marshal's role at Anvil as the pilot: status %d, want 204: Anvil admits her: %s", status, answer)
	}

	give("giving the bosun the marshal's role in every sector", "assign-crew-role", bosunRole("", nil))
	roleOffers("the bosun's marshal's role in every sector", heldRoles, "userId", bosunID, "", removeRole, true)
	give("taking the bosun's marshal's role in every sector", "remove-crew-role", bosunRole("", nil))

	// A role given with an end and a reason is a row of the roles held for a set time and
	// of the roles people hold, and each says the same of it.
	forATime := map[string]any{"endsAt": time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339Nano), "reason": "Covering the sector's watch"}
	for _, sector := range []struct {
		name, id string
		offered  bool
	}{{"Bastion", bastion, false}, {"Anvil", anvil, true}} {
		what := "the bosun's marshal's role at " + sector.name + ", given with an end and a reason"
		give("giving "+what, "assign-crew-role", bosunRole(sector.id, forATime))
		roleOffers(what, takenRoles, "userId", bosunID, sector.id, removeRole, sector.offered)
		roleOffers(what, heldRoles, "userId", bosunID, sector.id, removeRole, sector.offered)
		give("taking "+what, "remove-crew-role", bosunRole(sector.id, nil))
	}

	roleOffers("Bastion's group's marshal's role at Bastion", groupRoles, "groupId", wardroom, bastion, takeGroupRole, false)
	give("taking the marshal's role from Bastion's group", "take-crew-group-role", wardroomRole)
	give("giving Bastion's group the marshal's role there from an hour on", "give-crew-group-role", laterRole)
	roleOffers("Bastion's group's marshal's role at Bastion from an hour on", groupRoles, "groupId", wardroom, bastion, takeGroupRole, false)
	if status, answer := pilot.do(ctx, http.MethodPost, crewMethod("take-crew-group-role"), jsonBody(t, wardroomRole)); status != http.StatusForbidden || !strings.Contains(string(answer), roleKept) {
		t.Errorf("taking the marshal's role from Bastion's group, not begun, as the pilot: status %d, want 403 saying %q: %s", status, roleKept, answer)
	}
	give("taking the role that has not begun from Bastion's group", "take-crew-group-role", wardroomRole)
	give("giving Bastion's group the marshal's role there once more", "give-crew-group-role", wardroomRole)
	roleOffers("the fleet's group's marshal's role at Bastion", groupRoles, "groupId", fleetGroup, bastion, takeGroupRole, true)
	roleOffers("Anvil's group's marshal's role at Anvil", groupRoles, "groupId", anvilGroup, anvil, takeGroupRole, true)
	anvilGroupRole := map[string]any{"groupId": anvilGroup, "role": "SectorMarshal", "domain": anvil}
	if status, answer := pilot.do(ctx, http.MethodPost, crewMethod("take-crew-group-role"), jsonBody(t, anvilGroupRole)); status != http.StatusNoContent {
		t.Errorf("taking Anvil's marshal's role from Anvil's group as the pilot: status %d, want 204: Anvil admits her: %s", status, answer)
	}
	give("giving Anvil's group Anvil's marshal's role back", "give-crew-group-role", anvilGroupRole)

	// The row and the server count Bastion's marshal alike, whichever way she holds the
	// role: by a role of her own alone, through Bastion's group alone, and by a role that
	// has not begun, which counts for both though it opens Bastion to nobody yet. With
	// neither the row offers the pilot the acts again and the server runs them. The call
	// renames the rigger to the name she has, which changes nothing where it runs.
	sameName := map[string]any{"userId": riggerID, "username": "rigger"}
	judged := func(how string, administers bool) {
		t.Helper()

		offered := acts(offeredOnRow(ctx, t, pilot, crewListedAccountsPath, riggerID))
		status, answer := pilot.do(ctx, http.MethodPost, crewMethod("rename-crew-user"), jsonBody(t, sameName))
		if !administers {
			if !slices.Equal(offered, accountActs) {
				t.Errorf("the rigger's row as the pilot reads it, the rigger %s, offers the acts on an account %v, want %v", how, offered, accountActs)
			}
			if status != http.StatusNoContent {
				t.Errorf("renaming the rigger to her own name as the pilot, the rigger %s: status %d, want 204: %s", how, status, answer)
			}

			return
		}
		if len(offered) != 0 {
			t.Errorf("the rigger's row as the pilot reads it, the rigger Bastion's marshal %s, offers %v, want none of the acts on her account", how, offered)
		}
		if status != http.StatusForbidden || !strings.Contains(string(answer), accountKept) {
			t.Errorf("renaming the rigger to her own name as the pilot, the rigger Bastion's marshal %s: status %d, want 403 saying %q: %s", how, status, accountKept, answer)
		}
	}
	give("taking the rigger out of Bastion's group", "remove-crew-group-member", place)
	judged("by a role of her own alone", true)
	give("putting the rigger in Bastion's group", "add-crew-group-member", place)
	give("taking the rigger's own marshal's role at Bastion", "remove-crew-role", atBastion)
	judged("through Bastion's group alone", true)
	give("taking the rigger out of Bastion's group", "remove-crew-group-member", place)
	give("giving the rigger the marshal's role at Bastion from an hour on", "assign-crew-role", fromLater)
	judged("by a role of her own that has not begun", true)
	give("taking the role that has not begun back", "remove-crew-role", atBastion)
	judged("by neither", false)
	give("giving the rigger the marshal's role at Bastion once more", "assign-crew-role", atBastion)
	give("putting the rigger in Bastion's group once more", "add-crew-group-member", place)
	stands("the rigger, Bastion's marshal, once Headquarters was refused", rigger, []string{anvil, bastion, cinder}, "", "")
	if got := pilot.domains(ctx); slices.Contains(got, bastion) {
		t.Errorf("the pilot's chart once she was refused = %v, want Bastion out of it", got)
	}
	// Anvil admits her, and there Headquarters names a marshal as before.
	if status, answer := pilot.do(ctx, http.MethodPost, crewMethod("assign-crew-role"), jsonBody(t, map[string]any{"userId": riggerID, "role": "SectorMarshal", "domain": anvil})); status != http.StatusNoContent {
		t.Errorf("making the rigger a marshal of Anvil as the pilot, Headquarters: status %d, want 204: Anvil is not directory only: %s", status, answer)
	}

	// The rigger signs in through the fleet's Workspace again, in a new browser: the link
	// was taken off, so the sign-in is held for her password and links her account once
	// more, which makes it Cinder's again. This is her session through the Workspace from
	// here on.
	through = primedBrowser(ctx, t, s)
	if status, location := through.loginConsoleGoogle(ctx, "rigger"); status != http.StatusFound || location != held {
		t.Fatalf("the rigger's sign-in through the fleet's Workspace once the link was taken off: status %d to %q, want %d to %q", status, location, http.StatusFound, held)
	}
	if status, body := through.do(ctx, http.MethodPost, consoleAPI+"/user/pending/confirm", jsonBody(t, map[string]string{"password": personaPassword})); status != http.StatusOK {
		t.Fatalf("confirming the sign-in through the Workspace with the rigger's password: status %d: %s", status, body)
	}
	if sess := through.session(ctx); sess["authenticated"] != true || sess["username"] != "rigger" {
		t.Fatalf("the session after the confirmation = %v, want authenticated as the rigger", sess)
	}

	// Nor does a session established by an impersonation make its way in the person's.
	// The governor makes the rigger Headquarters too, so the whole fleet's method that
	// gives a role is hers to run, and in her own session, a password and the code, she
	// gives the bosun Bastion's marshal's role. The fleet's marshal views as her, which
	// is reading only, and Bastion does not admit the view. Then the rigger herself, in a
	// browser Bastion admits, acts as the Headquarters role (acting as a person refuses
	// every action that changes who may do what): the session is established by the
	// impersonation, so Bastion does not admit it, though the same person's own session
	// a moment before was admitted, and the call her own session makes is refused it.
	give("making the rigger Headquarters", "assign-crew-role", map[string]any{"userId": riggerID, "role": "Headquarters"})
	give("giving the rigger the grant to act as a role", "assign-crew-role", map[string]any{"userId": riggerID, "role": "CommandStaff"})
	viewing := newBrowser(t, s, consoleAPI)
	viewing.signIn(ctx, "marshal")
	if status, body := viewing.impersonate(ctx, viewAs(t, "rigger", "walkthrough")); status != http.StatusOK {
		t.Fatalf("the fleet's marshal viewing as the rigger: status %d: %s", status, body)
	}
	if info := viewing.session(ctx); info["username"] != "rigger" || info["impersonation"] == nil {
		t.Fatalf("the session after the fleet's marshal views as the rigger = %v, want the rigger's, impersonated", info)
	}
	if status, body := viewing.do(ctx, http.MethodGet, sectorPath(bastion, "wings"), nil); status != http.StatusNotFound {
		t.Errorf("GET Bastion's wings viewing as its marshal: status %d, want 404: the way in is a password sign-in's, never a session that views as the person: %s", status, body)
	}
	acting := newBrowser(t, s, consoleAPI)
	acting.signIn(ctx, "rigger")
	if status, body := acting.do(ctx, http.MethodGet, sectorPath(bastion, "wings"), nil); status != http.StatusOK {
		t.Fatalf("GET Bastion's wings as the rigger in a second browser of her own, before it acts as anyone: status %d, want 200: %s", status, body)
	}
	if status, body := acting.impersonate(ctx, `{"kind":"role","principal":"Headquarters","reason":"walkthrough"}`); status != http.StatusOK {
		t.Fatalf("the rigger acting as the Headquarters role: status %d: %s", status, body)
	}
	if status, body := acting.do(ctx, http.MethodGet, sectorPath(bastion, "wings"), nil); status != http.StatusNotFound {
		t.Errorf("GET Bastion's wings as the rigger acting as the Headquarters role: status %d, want 404: the way in is a password sign-in's, never a session an impersonation established: %s", status, body)
	}
	bosunToBastion := jsonBody(t, map[string]any{"userId": crewUserID(ctx, t, governor, "bosun").String(), "role": "SectorMarshal", "domain": bastion})
	if status, answer := acting.do(ctx, http.MethodPost, crewMethod("assign-crew-role"), bosunToBastion); status != http.StatusForbidden || !strings.Contains(string(answer), roleKept) {
		t.Errorf("giving Bastion's marshal's role as the rigger acting as the Headquarters role: status %d, want 403 saying %q: %s", status, roleKept, answer)
	}

	// The two reads above are refused by more than the rule that Bastion is directory
	// only. The fleet's marshal's own sign-in never entered Bastion, so a session that
	// kept it would be kept out as he is; and the Headquarters role holds nothing in a
	// sector, so a session that acts as it has no foothold in any sector, admitted or
	// not. These two are refused by that rule and nothing else: each is a session the
	// rigger starts from a browser Bastion admits, as someone or something that reads a
	// sector's wings, and each reads Anvil's wings, which admits any sign-in, and not
	// Bastion's. She views as the surveyor, one of the crew of every sector, and she
	// acts as the marshal's role, the one she holds in Bastion herself.
	for _, started := range []struct {
		as   string
		body string
	}{
		{"viewing as the surveyor, one of the crew of every sector", viewAs(t, "surveyor", "walkthrough")},
		{"acting as the SectorMarshal role", `{"kind":"role","principal":"SectorMarshal","reason":"walkthrough"}`},
	} {
		reading := newBrowser(t, s, consoleAPI)
		reading.signIn(ctx, "rigger")
		if status, body := reading.do(ctx, http.MethodGet, sectorPath(bastion, "wings"), nil); status != http.StatusOK {
			t.Fatalf("GET Bastion's wings as the rigger in a browser of her own, before it is %s: status %d, want 200: %s", started.as, status, body)
		}
		if status, body := reading.impersonate(ctx, started.body); status != http.StatusOK {
			t.Fatalf("the rigger %s: status %d: %s", started.as, status, body)
		}
		if status, body := reading.do(ctx, http.MethodGet, sectorPath(anvil, "wings"), nil); status != http.StatusOK {
			t.Errorf("GET Anvil's wings as the rigger %s: status %d, want 200: the session reads a sector's wings where the sector admits it: %s", started.as, status, body)
		}
		if status, body := reading.do(ctx, http.MethodGet, sectorPath(bastion, "wings"), nil); status != http.StatusNotFound {
			t.Errorf("GET Bastion's wings as the rigger %s: status %d, want 404: the session reads a sector's wings, and Bastion admits a sign-in through its directory or its marshal's password, never a session an impersonation established: %s", started.as, status, body)
		}
	}
	give("taking the grant to act as a role back from the rigger", "remove-crew-role", map[string]any{"userId": riggerID, "role": "CommandStaff"})
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

	if status, body := rigger.do(ctx, http.MethodGet, sectorPath(bastion, "sign-in-settings"), nil); status != http.StatusOK || !strings.HasPrefix(strings.TrimSpace(string(body)), `{"directoryOnly":true,`) {
		t.Fatalf("Bastion's switch as its marshal reads it, signed in with a password: status %d %s, want 200 saying it is on", status, body)
	}
	if status, body := rigger.do(ctx, http.MethodPost, sectorPath(bastion, "end-sector-directory-only"), []byte(`{}`)); status != http.StatusOK {
		t.Fatalf("turning Bastion's directory only off as its marshal, signed in with a password: status %d: %s", status, body)
	}
	if status, body := rigger.do(ctx, http.MethodGet, sectorPath(bastion, "sign-in-settings"), nil); status != http.StatusOK || !strings.HasPrefix(strings.TrimSpace(string(body)), `{"directoryOnly":false`) {
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

// offeredOnRow is what the row with the id offers the browser's user in one of the
// crew's lists of the whole fleet, read with capabilities=Execute as a list page reads
// it: the methods of the row's Execute answer, sorted.
func offeredOnRow(ctx context.Context, t *testing.T, b *browser, list, id string) []string {
	t.Helper()

	status, body := b.do(ctx, http.MethodGet, crewMethod(list+"?limit=200&capabilities=Execute"), nil)
	assertStatus(t, status, http.StatusOK, body)
	for _, row := range decodeRows(t, body) {
		if row["id"] == id {
			return executeOffered(t, row)
		}
	}
	t.Fatalf("the list %s does not carry the row %s: %s", list, id, body)

	return nil
}

// offeredOnRole is what the row of a marshal's role offers the browser's user in one of
// the crew's lists of roles of the whole fleet, read with capabilities=Execute as a list
// page reads it: the row found by its key's columns, the person or the group (the column
// by, holding id), the sector the role is held in (empty for every sector) and the role,
// the person's own where the list names a group a role comes through. The methods of the
// row's Execute answer, sorted.
func offeredOnRole(ctx context.Context, t *testing.T, b *browser, list, by, id, sector string) []string {
	t.Helper()

	status, body := b.do(ctx, http.MethodGet, crewMethod(list+"?limit=200&capabilities=Execute&filter="+by+":eq:"+id), nil)
	assertStatus(t, status, http.StatusOK, body)
	for _, row := range decodeRows(t, body) {
		own := by == "groupId" || row["groupId"] == nil || row["groupId"] == ""
		if row[by] == id && row["domainId"] == sector && row["role"] == "SectorMarshal" && own {
			return executeOffered(t, row)
		}
	}
	t.Fatalf("the list %s does not carry the marshal's role of %s in the sector %q: %s", list, id, sector, body)

	return nil
}

// offeredOnGroupMember is what the row of the person in the group offers the browser's
// user in the crew's list of the people of the fleet's groups, read with
// capabilities=Execute as a list page reads it: the methods of the row's Execute answer,
// sorted.
func offeredOnGroupMember(ctx context.Context, t *testing.T, b *browser, group, user string) []string {
	t.Helper()

	status, body := b.do(ctx, http.MethodGet, crewMethod("crew-group-users?limit=200&capabilities=Execute"), nil)
	assertStatus(t, status, http.StatusOK, body)
	for _, row := range decodeRows(t, body) {
		if row["groupId"] == group && row["userId"] == user {
			return executeOffered(t, row)
		}
	}
	t.Fatalf("the people of the fleet's groups do not carry %s in the group %s: %s", user, group, body)

	return nil
}

// offeredOnPage is what the row with the id offers the browser's user read alone, as
// its page reads it, with capabilities=Execute: the methods of its Execute answer,
// sorted.
func offeredOnPage(ctx context.Context, t *testing.T, b *browser, list, id string) []string {
	t.Helper()

	status, body := b.do(ctx, http.MethodGet, crewMethod(list+"/"+id+"?capabilities=Execute"), nil)
	assertStatus(t, status, http.StatusOK, body)

	return executeOffered(t, decodeRow(t, body))
}

// executeOffered is the row's Execute answer (zzCapabilities), sorted.
func executeOffered(t *testing.T, row map[string]any) []string {
	t.Helper()

	capabilities, ok := row["zzCapabilities"].(map[string]any)
	if !ok {
		t.Fatalf("the row %v carries no zzCapabilities", row["id"])
	}
	offered, _ := capabilities["Execute"].([]any)
	methods := make([]string, 0, len(offered))
	for _, method := range offered {
		methods = append(methods, fmt.Sprint(method))
	}
	slices.Sort(methods)

	return methods
}
