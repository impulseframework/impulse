//go:build skipAuth

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/lodestar/pkg/auth"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// TestDirectoryOnly_theSwitchIsHeldToItsRules proves a sector's directory-only switch
// end to end on the served stack: what it does while it is on, the rules of turning it
// on, and what the server says it would do before it is saved.
//
// Bastion is directory only, and its directory is the persona provider of the test
// environments (oidc, persona), which the stack registers here. A sector's sign-in
// settings restrict its own door alone: the warden, a marshal in every sector signed in
// through the persona provider, enters Bastion through its directory and every other
// sector however he signed in, and the rigger, by password, enters Anvil and finds
// Bastion on her chart marked closed, with the reason. Bastion's list of its people is
// made from who holds a role there, not from who can sign in: the rigger stays in it,
// and while Bastion is directory only her row says she cannot enter; the warden's, who
// signed in through the directory, and the governor's, a super administrator, say
// nothing.
//
// The switch is the sector's own: its marshal turns it, with the sector's two methods
// (MakeSectorDirectoryOnly and EndSectorDirectoryOnly, in the sector's scope), and reads
// it in the sector, and the global Sectors record shows the flag and never writes it, so
// a patch of the record that carries the flag, and a chart that carries it, change
// nothing. While the switch is on the sector's routes, the two methods among them, are
// served to the people the sector admits: a person who holds the grant in every sector
// and signed in with a password is answered as for a sector that is not there, since
// the fleet's own team, seated everywhere, is who the switch shuts out. It is turned
// off by a marshal who signed in through the directory, by a marshal of that sector
// itself who signed in with a password and the code (the test beside this one), or by a
// super administrator, whom no sector's sign-in settings shut out. Turning it off is
// plain and warns of nothing: the governor, a super administrator, does it by password,
// the rigger enters Bastion, and no row says anything. Turning it on is refused unless
// the person is signed in through the sector's directory themselves, until the sector
// has a directory, and until the person has a password of their own, which is their way
// back in if the directory has a problem, each with a sentence the person can act on,
// on the method, on its dry run and on the warning a page asks first; a dry run writes
// nothing; and a caller without the method's grant is refused by the grants, before any
// rule answers. The warden, signed in through the persona provider, has never had a
// password: he is refused, chooses one on a code mailed to his confirmed address (no
// password without the code, and none for an account that has one), changes it and
// goes on in a session that is still the one his directory opened, is then told what
// it would do by the method's warning, the sentence, the people it names and the words
// for the button, and turns it on; once it is on the warning has nothing to say. The
// persona sign-in sets a process variable, so it holds the lock the other simulated
// sign-ins hold.
//
// Demonstrates: tenancy.directory-only, tenancy.declaration, auth.persona.
func TestDirectoryOnly_theSwitchIsHeldToItsRules(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServedWith(ctx, t, servedOptions{persona: &auth.Persona{Issuer: "https://persona.example.com", ClientID: "lodestar-pr12"}})
	if _, err := s.crew.SeedCast(ctx, io.Discard); err != nil {
		t.Fatalf("SeedCast() error = %v", err)
	}

	governor := newBrowser(t, s, consoleAPI)
	governor.signIn(ctx, "governor")
	rigger := newBrowser(t, s, consoleAPI)
	rigger.signIn(ctx, "rigger")
	warden := newBrowser(t, s, consoleAPI)
	warden.signInAsPersona(ctx, "warden")
	hq := newBrowser(t, s, consoleAPI)
	hq.signInAsPersona(ctx, "hq")
	rookie := newBrowser(t, s, consoleAPI)
	rookie.signInAsPersona(ctx, "rookie")

	// A persona's session came through the persona provider, an organization's
	// directory to the sign-in rule: the session read names it.
	if got := sessionDirectory(t, warden.session(ctx)); got == nil || got.Method != string(sessioninfo.MethodOIDC) || got.Connection != crew.PersonaRegistration {
		t.Fatalf("the warden's session reports the directory %+v, want the persona provider (oidc, persona)", got)
	}

	// While Bastion is directory only. The warden enters it through its directory, and
	// Anvil and Cinder too, which are not directory only and admit him however he
	// signed in. The rigger, by password, does not, and her chart says why.
	if got, want := warden.domains(ctx), []string{anvil, bastion, cinder}; !slices.Equal(got, want) {
		t.Errorf("the warden's chart = %v, want %v: Bastion through its directory, the others however he signed in", got, want)
	}
	if got := warden.closed(ctx); len(got) != 0 {
		t.Errorf("the sectors the warden's chart names closed = %v, want none", got)
	}
	if status, body := warden.do(ctx, http.MethodGet, sectorPath(bastion, "wings"), nil); status != http.StatusOK {
		t.Errorf("GET Bastion's wings as the warden, signed in through its directory: status %d, want 200: %s", status, body)
	}
	byPassword := map[string]string{bastion: "Bastion admits only people who sign in through its own directory. You signed in with a password."}
	if got, want := rigger.domains(ctx), []string{anvil}; !slices.Equal(got, want) {
		t.Errorf("the rigger's chart = %v, want %v", got, want)
	}
	if got := rigger.closed(ctx); !maps.Equal(got, byPassword) {
		t.Errorf("the sectors the rigger's chart names closed = %v, want %v", got, byPassword)
	}

	// Bastion's list of its people keeps everyone who holds a role there, and says who
	// the switch keeps out: the people with no sign-in through its directory.
	keptOut := "Has not signed in through this sector's directory, so cannot enter while this sector is directory only"
	people := cannotEnter(ctx, t, warden)
	for username, want := range map[string]string{"rigger": keptOut, "bosun": keptOut, "pilot": keptOut, "warden": "", "governor": ""} {
		got, listed := people[username]
		if !listed || got != want {
			t.Errorf("Bastion's people: %s is listed = %v saying %q, want listed saying %q", username, listed, got, want)
		}
	}

	// The governor's row says nothing because he is a super administrator now. An
	// assignment outside its period is no assignment, to the list as to the sign-in rule
	// and the session's reach: the library gives the Super Administrator role no period
	// by any write, so the two rows here are written past it, one that has ended, for
	// the pilot, and one that has not begun, for the bosun. Neither makes its person a
	// super administrator, and the list goes on saying the switch keeps each of them
	// out.
	pilotID, bosunID := crewUserID(ctx, t, governor, "pilot"), crewUserID(ctx, t, governor, "bosun")
	membership := func(user impulse.UUID, period map[string]any) *spanner.Mutation {
		row := map[string]any{"Kind": "global", "Axis": "", "DomainId": "", "Role": string(accesstypes.SuperAdministrator), "UserId": user.String(), "CreatedAt": spanner.CommitTimestamp}
		maps.Copy(row, period)

		return spanner.InsertMap("CrewUserRoles", row)
	}
	if _, err := s.db.Apply(ctx, []*spanner.Mutation{
		membership(pilotID, map[string]any{"EndsAt": time.Now().Add(-time.Hour)}),
		membership(bosunID, map[string]any{"StartsAt": time.Now().Add(time.Hour)}),
	}); err != nil {
		t.Fatalf("writing the two Super Administrator rows outside their period: %v", err)
	}
	people = cannotEnter(ctx, t, warden)
	for username, when := range map[string]string{"pilot": "has ended", "bosun": "has not begun"} {
		if got := people[username]; got != keptOut {
			t.Errorf("Bastion's people: %s, whose Super Administrator assignment %s, is listed saying %q, want %q: an assignment outside its period exempts nobody", username, when, got, keptOut)
		}
	}
	if _, err := s.db.Apply(ctx, []*spanner.Mutation{
		spanner.Delete("CrewUserRoles", crewMembershipKey(string(accesstypes.SuperAdministrator), userOf(pilotID))),
		spanner.Delete("CrewUserRoles", crewMembershipKey(string(accesstypes.SuperAdministrator), userOf(bosunID))),
	}); err != nil {
		t.Fatalf("removing the two rows again: %v", err)
	}

	// The switch is the sector's own: two methods and a read under the sector, the
	// methods each to Execute on it there, which the sector's marshal holds, and the
	// read under the grant of either.
	method := map[bool]string{true: "make-sector-directory-only", false: "end-sector-directory-only"}
	turn := func(b *browser, sector string, on bool) (int, []byte) {
		return b.do(ctx, http.MethodPost, sectorPath(sector, method[on]), []byte(`{}`))
	}
	state := func(b *browser, sector string) (int, string) {
		status, body := b.do(ctx, http.MethodGet, sectorPath(sector, "directory-only"), nil)

		return status, strings.TrimSpace(string(body))
	}
	// warns asks the method that turns the switch on for its warning: what the call
	// would do, with nothing saved.
	warns := func(b *browser, sector string) (int, []byte) {
		res := b.doWith(ctx, http.MethodPost, sectorPath(sector, method[true]), []byte(`{}`), map[string]string{"X-Warning": "true"})
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}

		return res.StatusCode, body
	}
	const on, off = `{"directoryOnly":true}`, `{"directoryOnly":false}`
	// The marshal reads the switch in the sector: he does not read the global record.
	if status, got := state(warden, bastion); status != http.StatusOK || got != on {
		t.Errorf("Bastion's switch as the warden reads it: status %d %s, want 200 %s", status, got, on)
	}
	if status, got := state(warden, anvil); status != http.StatusOK || got != off {
		t.Errorf("Anvil's switch as the warden reads it: status %d %s, want 200 %s", status, got, off)
	}

	// While the switch is on, Bastion's routes are served to the people Bastion admits,
	// the switch's own among them. The rigger is given the marshal's role in every
	// sector, as the fleet's own team holds it, so she holds the grant of both methods
	// at Bastion, and signed in with a password and the code: the method that would
	// turn it off, the one that would turn it on and the read answer her as a sector
	// that is not there, and nothing changes. The role held in every sector is not the
	// role held in Bastion itself, which is what opens the administrators' way in. A
	// marshal who signed in through the directory turns it off, or a super
	// administrator.
	riggerID := crewUserID(ctx, t, governor, "rigger").String()
	asMarshal := jsonBody(t, map[string]any{"userId": riggerID, "role": "SectorMarshal", "everyDomain": true})
	if status, body := governor.do(ctx, http.MethodPost, consoleAPI+"/assign-crew-role", asMarshal); status != http.StatusOK && status != http.StatusNoContent {
		t.Fatalf("giving the rigger the marshal's role in every sector as the governor: status %d: %s", status, body)
	}
	if got, want := rigger.domains(ctx), []string{anvil, cinder}; !slices.Equal(got, want) {
		t.Fatalf("the rigger's chart as a marshal in every sector, signed in with a password = %v, want %v: the role reaches every sector but the one that is directory only", got, want)
	}
	if got := cannotEnter(ctx, t, warden)["rigger"]; got != keptOut {
		t.Errorf("Bastion's people: the rigger, a marshal in every sector who has a password, is listed saying %q, want %q: the way in is for a marshal of Bastion itself", got, keptOut)
	}
	for name, call := range map[string]func() (int, []byte){
		"turning it off":       func() (int, []byte) { return turn(rigger, bastion, false) },
		"turning it on":        func() (int, []byte) { return turn(rigger, bastion, true) },
		"asking for a warning": func() (int, []byte) { return warns(rigger, bastion) },
		"reading the switch": func() (int, []byte) {
			return rigger.do(ctx, http.MethodGet, sectorPath(bastion, "directory-only"), nil)
		},
	} {
		if status, body := call(); status != http.StatusNotFound || strings.Contains(string(body), "Persona") {
			t.Errorf("%s at Bastion as a marshal in every sector who signed in with a password, while Bastion is directory only: status %d, want 404 that says nothing of its directory: %s", name, status, body)
		}
	}
	if status, got := state(warden, bastion); status != http.StatusOK || got != on {
		t.Fatalf("Bastion's switch after the calls of a marshal it keeps out: status %d %s, want 200 %s: nothing was written", status, got, on)
	}
	takeBack := jsonBody(t, map[string]any{"userId": riggerID, "role": "SectorMarshal", "everyDomain": true})
	if status, body := governor.do(ctx, http.MethodPost, consoleAPI+"/remove-crew-role", takeBack); status != http.StatusOK && status != http.StatusNoContent {
		t.Fatalf("taking the marshal's role in every sector back from the rigger: status %d: %s", status, body)
	}

	// Turning it off is plain, and warns of nothing: a request for a warning is refused
	// and turns nothing. The governor, a super administrator, whom no sector's sign-in
	// settings shut out, turns it off, signed in with a password. The rigger enters
	// Bastion at once, and no row of its people says anything.
	noWarning := governor.doWith(ctx, http.MethodPost, sectorPath(bastion, method[false]), []byte(`{}`), map[string]string{"X-Warning": "true"})
	if err := noWarning.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if noWarning.StatusCode != http.StatusBadRequest {
		t.Errorf("asking the method that turns the switch off for a warning: status %d, want 400: it declares none", noWarning.StatusCode)
	}
	if status, got := state(warden, bastion); status != http.StatusOK || got != on {
		t.Fatalf("Bastion's switch after the refused request for a warning: status %d %s, want 200 %s: the call was not run", status, got, on)
	}
	if status, body := turn(governor, bastion, false); status != http.StatusOK {
		t.Fatalf("turning Bastion's directory only off as the governor, by password: status %d: %s", status, body)
	}
	if status, got := state(warden, bastion); status != http.StatusOK || got != off {
		t.Errorf("Bastion's switch once it is turned off: status %d %s, want 200 %s", status, got, off)
	}
	if got, want := rigger.domains(ctx), []string{anvil, bastion}; !slices.Equal(got, want) {
		t.Errorf("the rigger's chart once Bastion is not directory only = %v, want %v", got, want)
	}
	if got := rigger.closed(ctx); len(got) != 0 {
		t.Errorf("the sectors the rigger's chart names closed once Bastion is not directory only = %v, want none", got)
	}
	for username, sentence := range cannotEnter(ctx, t, warden) {
		if sentence != "" {
			t.Errorf("Bastion's people while it is not directory only: %s's row says %q, want nothing", username, sentence)
		}
	}

	// The global record shows the flag and never writes it. A patch of the record that
	// carries it is refused, whoever sends it, the governor, a super administrator who
	// holds every grant, included; and a sector is not charted directory only, or charted
	// at all: nobody creates a sector directly. A sector is directory only from the start
	// where the invitation that makes it says so, and that invitation is accepted through
	// the directory the sector will be only of (TestInvitedSector).
	patchRecord := jsonBody(t, []map[string]any{{"op": "patch", "path": "/sectors/" + bastion, "value": map[string]any{"directoryOnly": true}}})
	if status, body := governor.do(ctx, http.MethodPatch, consoleAPI+"/resources", patchRecord); status != http.StatusBadRequest {
		t.Errorf("patching the Sectors record's directoryOnly as the governor: status %d, want 400, the record does not take the flag: %s", status, body)
	}
	chartClosed := jsonBody(t, []map[string]any{{"op": "add", "path": "/sectors", "value": map[string]any{"name": "Flint", "region": "Near frontier", "established": "2226-11-02", "directoryOnly": true}}})
	if status, body := governor.do(ctx, http.MethodPatch, consoleAPI+"/resources", chartClosed); status != http.StatusBadRequest || !strings.Contains(string(body), "a sector is not created directly") {
		t.Errorf("charting a sector that is directory only from the start: status %d, want 400 saying how a sector is made: %s", status, body)
	}
	if status, body := governor.do(ctx, http.MethodGet, consoleAPI+"/sectors", nil); status != http.StatusOK || strings.Contains(string(body), "Flint") {
		t.Errorf("the sectors after the refused chart: status %d, want 200 and no Flint: %s", status, body)
	}
	if status, got := state(warden, bastion); status != http.StatusOK || got != off {
		t.Fatalf("Bastion's switch after the refused patch of the record: status %d %s, want 200 %s: nothing was written", status, got, off)
	}

	// Turning it on is refused unless the person is signed in through the sector's
	// directory themselves. The governor holds the grant and signed in with a password:
	// the warning a page asks first and the method are refused in the same words, which
	// say what to do, and nothing is written.
	notThroughIt := "Directory only is turned on by a person who is signed in through this sector's directory, OpenID Connect provider Persona: that shows the directory works, and that you will not shut yourself out. You signed in with a password. Sign in through OpenID Connect provider Persona and try again."
	if status, body := warns(governor, bastion); status != http.StatusConflict || !strings.Contains(string(body), notThroughIt) {
		t.Errorf("asking what directory only would do to Bastion, signed in with a password: status %d, want 409 saying %q: %s", status, notThroughIt, body)
	}
	if status, body := turn(governor, bastion, true); status != http.StatusConflict || !strings.Contains(string(body), notThroughIt) {
		t.Errorf("turning Bastion's directory only on, signed in with a password: status %d, want 409 saying %q: %s", status, notThroughIt, body)
	}
	if got, want := rigger.domains(ctx), []string{anvil, bastion}; !slices.Equal(got, want) {
		t.Fatalf("the rigger's chart after the refused call = %v, want %v: nothing was written", got, want)
	}

	// A sector with no directory has nothing to be only of. The warden, through the
	// persona provider, is refused for Anvil on the warning and on the method, and for
	// Cinder, whose directory is the fleet's Workspace and not the one he came through.
	noDirectory := "This sector has no directory, so it cannot be directory only: nobody could enter it. Set its directory first: sign in through your organization's directory, make it this sector's directory, then turn directory only on."
	if status, body := warns(warden, anvil); status != http.StatusConflict || !strings.Contains(string(body), noDirectory) {
		t.Errorf("asking what directory only would do to Anvil, which has no directory: status %d, want 409 saying %q: %s", status, noDirectory, body)
	}
	if status, body := turn(warden, anvil, true); status != http.StatusConflict || !strings.Contains(string(body), noDirectory) {
		t.Errorf("turning directory only on for Anvil, which has no directory: status %d, want 409 saying %q: %s", status, noDirectory, body)
	}
	anotherDirectory := "Directory only is turned on by a person who is signed in through this sector's directory, Google Workspace example: that shows the directory works, and that you will not shut yourself out. You signed in through OpenID Connect provider Persona. Sign in through Google Workspace example and try again."
	if status, body := warns(warden, cinder); status != http.StatusConflict || !strings.Contains(string(body), anotherDirectory) {
		t.Errorf("asking what directory only would do to Cinder, signed in through another sector's directory: status %d, want 409 saying %q: %s", status, anotherDirectory, body)
	}
	if status, body := turn(warden, cinder, true); status != http.StatusConflict || !strings.Contains(string(body), anotherDirectory) {
		t.Errorf("turning directory only on for Cinder, signed in through another sector's directory: status %d, want 409 saying %q: %s", status, anotherDirectory, body)
	}
	if got, want := rigger.domains(ctx), []string{anvil, bastion}; !slices.Equal(got, want) {
		t.Fatalf("the rigger's chart after the refusals = %v, want %v: Anvil stays as it was", got, want)
	}

	// The caller's grants answer before the rules do. The rookie, a cadet at Bastion,
	// is signed in through Bastion's directory and holds no grant on either method: the
	// methods, the warning and the read are refused as on any method, and say nothing of
	// the sector's directory or of how he signed in. Headquarters holds the global record
	// and no role in the sector: the sector's routes do not answer her at all. A sector
	// that does not exist is not found.
	saysTheRule := func(body []byte) bool {
		return strings.Contains(string(body), "Persona") || strings.Contains(string(body), "signed in") || strings.Contains(string(body), "Sign in") || strings.Contains(string(body), `"directoryOnly"`)
	}
	if status, body := turn(rookie, bastion, true); status != http.StatusForbidden || saysTheRule(body) {
		t.Errorf("turning Bastion's directory only on as the rookie, who holds no grant on the method: status %d, want 403 that says nothing of the rule: %s", status, body)
	}
	if status, body := turn(rookie, bastion, false); status != http.StatusForbidden || saysTheRule(body) {
		t.Errorf("turning Bastion's directory only off as the rookie, who holds no grant on the method: status %d, want 403 that says nothing of the rule: %s", status, body)
	}
	if status, body := warns(rookie, bastion); status != http.StatusForbidden || saysTheRule(body) {
		t.Errorf("asking what directory only would do as the rookie: status %d, want 403 that says nothing of the rule: %s", status, body)
	}
	if status, body := rookie.do(ctx, http.MethodGet, sectorPath(bastion, "directory-only"), nil); status != http.StatusForbidden || saysTheRule(body) {
		t.Errorf("reading Bastion's switch as the rookie: status %d, want 403 that says nothing of the switch: %s", status, body)
	}
	for name, call := range map[string]func() (int, []byte){
		"the method":  func() (int, []byte) { return turn(hq, bastion, true) },
		"the warning": func() (int, []byte) { return warns(hq, bastion) },
	} {
		if status, body := call(); (status != http.StatusNotFound && status != http.StatusForbidden) || saysTheRule(body) {
			t.Errorf("%s as headquarters, who holds no role in Bastion: status %d, want a refusal that says nothing of the rule: %s", name, status, body)
		}
	}
	if status, body := warns(warden, "f0000000-0000-4000-8000-00000000dead"); status != http.StatusNotFound {
		t.Errorf("asking what directory only would do to a sector that does not exist: status %d, want 404: %s", status, body)
	}

	// Turning it on is refused until the person has a password of their own, where their
	// auth has passwords: a marshal of a directory-only sector enters it with a password
	// and a code sent to their email, and that is the way back in if the directory has a
	// problem. The warden signed in through Bastion's directory and has never had a
	// password: the warning a page asks first, the dry run and the method are refused in
	// the same words, which say what to do, and nothing is written.
	noPassword := "Directory only is turned on by a person who has a password: an administrator of this sector enters it with a password and a code sent to their email, and that is the way back in if the directory has a problem. Choose a password on Your account first, then try again."
	rehearses := func(b *browser, sector string) (int, []byte) {
		res := b.doWith(ctx, http.MethodPost, sectorPath(sector, method[true]), []byte(`{}`), map[string]string{"X-Dry-Run": "true"})
		defer res.Body.Close()
		said, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}

		return res.StatusCode, said
	}
	for name, call := range map[string]func() (int, []byte){
		"the warning": func() (int, []byte) { return warns(warden, bastion) },
		"the dry run": func() (int, []byte) { return rehearses(warden, bastion) },
		"the method":  func() (int, []byte) { return turn(warden, bastion, true) },
	} {
		if status, body := call(); status != http.StatusConflict || !strings.Contains(string(body), noPassword) {
			t.Errorf("%s of turning Bastion's directory only on as the warden, who has no password: status %d, want 409 saying %q: %s", name, status, noPassword, body)
		}
	}
	if status, got := state(warden, bastion); status != http.StatusOK || got != off {
		t.Fatalf("Bastion's switch after the calls of a marshal who has no password: status %d %s, want 200 %s: nothing was written", status, got, off)
	}

	// He chooses one on Your account. A person who has no password chooses their first
	// only on a code mailed to their confirmed address, which the persona provider
	// attested at his sign-in: a password posted before any code was asked for is
	// refused, and so is one posted with a code that is not the one mailed. An account
	// that has a password is refused the code, since its person changes the password
	// with the one they have. The right code sets the password, ends no session, and
	// the account takes no second first password.
	chosen := func(code string) []byte {
		return jsonBody(t, map[string]string{"code": code, "password": personaPassword})
	}
	firstPassword := consoleAPI + "/user/first-password"
	if status, body := warden.do(ctx, http.MethodPost, firstPassword, chosen("000000")); status != http.StatusConflict || !strings.Contains(string(body), "That code has expired or was already used. Ask for a new one") {
		t.Errorf("choosing a first password before any code was mailed: status %d, want 409 saying to ask for a code: %s", status, body)
	}
	if status, body := rigger.do(ctx, http.MethodPost, firstPassword+"/code", nil); status != http.StatusConflict || !strings.Contains(string(body), "Your account already has a password. To change it, enter the one you have and the new one") {
		t.Errorf("asking for the code of a first password as the rigger, who has a password: status %d, want 409 saying the account has one: %s", status, body)
	}
	// The simulated persona provider vouches for the warden under her login name, which
	// no message can be sent to: her account is told it has no confirmed address, and
	// she adds one and confirms it from the link mailed to it. The code goes there.
	if status, body := warden.do(ctx, http.MethodPost, firstPassword+"/code", nil); status != http.StatusConflict || !strings.Contains(string(body), "Your account has no confirmed email address") {
		t.Errorf("asking for the code of a first password with an address no message can be sent to: status %d, want 409 saying the account has no confirmed address: %s", status, body)
	}
	const wardensAddress = "warden@fleet.example"
	if status, body := warden.do(ctx, http.MethodPost, consoleAPI+"/user/email", jsonBody(t, map[string]string{"email": wardensAddress})); status != http.StatusOK {
		t.Fatalf("adding the warden's address: status %d: %s", status, body)
	}
	_, token := lastMailTo(t, s, wardensAddress, "verify")
	if status, body := warden.do(ctx, http.MethodPost, consoleAPI+"/user/email/verify", jsonBody(t, map[string]string{"token": token})); status != http.StatusOK {
		t.Fatalf("confirming the warden's address from the mailed link: status %d: %s", status, body)
	}
	code := firstPasswordCode(ctx, t, warden)
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	if status, body := warden.do(ctx, http.MethodPost, firstPassword, chosen(wrong)); status != http.StatusBadRequest || !strings.Contains(string(body), "That is not the code we sent. Check it and try again") {
		t.Errorf("choosing a first password with a code that was not the one mailed: status %d, want 400 saying so: %s", status, body)
	}
	for name, call := range map[string]func() (int, []byte){
		"the warning": func() (int, []byte) { return warns(warden, bastion) },
		"the method":  func() (int, []byte) { return turn(warden, bastion, true) },
	} {
		if status, body := call(); status != http.StatusConflict || !strings.Contains(string(body), noPassword) {
			t.Errorf("%s of turning Bastion's directory only on after the refused first passwords: status %d, want 409 saying %q, no password was set: %s", name, status, noPassword, body)
		}
	}
	if status, body := warden.do(ctx, http.MethodPost, firstPassword, chosen(code)); status != http.StatusOK {
		t.Fatalf("choosing a first password with the code that was mailed: status %d: %s", status, body)
	}
	if sess := warden.session(ctx); sess["authenticated"] != true || sess["username"] != "warden" {
		t.Errorf("the warden's session after he chose a password = %v, want it standing: a first password ends no session", sess)
	}
	if got := sessionDirectory(t, warden.session(ctx)); got == nil || got.Connection != crew.PersonaRegistration {
		t.Errorf("the warden's session after he chose a password reports the directory %+v, want the persona provider still: the session is the one his directory opened", got)
	}
	if status, body := warden.do(ctx, http.MethodPost, firstPassword+"/code", nil); status != http.StatusConflict || !strings.Contains(string(body), "Your account already has a password") {
		t.Errorf("asking for the code of a first password once the warden has a password: status %d, want 409 saying the account has one: %s", status, body)
	}

	// He changes the password he chose, with the one he has. The change ends his
	// sessions and goes on in a new one, which is the continuation of the session that
	// made the change: still the one his directory opened, never a password's. Every
	// step from here is taken in it, the switch turned on and a sector entered that
	// admits its directory alone among them.
	const wardensNext = "a lantern over the yard"
	change := jsonBody(t, map[string]string{"oldPassword": personaPassword, "newPassword": wardensNext})
	if status, body := warden.do(ctx, http.MethodPost, consoleAPI+"/user/password/change", change); status != http.StatusOK {
		t.Fatalf("the warden changing his password: status %d: %s", status, body)
	}
	if sess := warden.session(ctx); sess["authenticated"] != true || sess["username"] != "warden" {
		t.Fatalf("the warden's session after he changed his password = %v, want him signed in: the change goes on in a new session", sess)
	}
	if got := sessionDirectory(t, warden.session(ctx)); got == nil || got.Method != string(sessioninfo.MethodOIDC) || got.Connection != crew.PersonaRegistration {
		t.Errorf("the warden's session after he changed his password reports the directory %+v, want the persona provider still: the new session carries on the one that made the change", got)
	}

	// The method that the old body named is gone, and neither method takes a body that
	// says which way: each turns the switch one way. A dry run writes nothing.
	if status, body := warden.do(ctx, http.MethodPost, sectorPath(bastion, "set-sector-directory-only"), []byte(`{"directoryOnly":true}`)); status != http.StatusNotFound && status != http.StatusMethodNotAllowed {
		t.Errorf("calling the method that took a body saying which way: status %d, want it gone: %s", status, body)
	}
	if status, body := warden.do(ctx, http.MethodPost, sectorPath(bastion, method[true]), []byte(`{"directoryOnly":false}`)); status != http.StatusBadRequest {
		t.Errorf("calling the method that turns the switch on with a body that says off: status %d, want 400: the method takes no such field: %s", status, body)
	}
	dry := warden.doWith(ctx, http.MethodPost, sectorPath(bastion, method[true]), []byte(`{}`), map[string]string{"X-Dry-Run": "true"})
	if err := dry.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if dry.StatusCode != http.StatusOK {
		t.Errorf("a dry run of turning Bastion's directory only on, through its directory: status %d, want 200", dry.StatusCode)
	}
	if status, got := state(warden, bastion); status != http.StatusOK || got != off {
		t.Fatalf("Bastion's switch after the dry run: status %d %s, want 200 %s: a dry run writes nothing", status, got, off)
	}
	// Either header takes the value true or is left off the request. A request that says
	// anything else is refused and turns nothing: a client that sends "1" to rehearse
	// closing a sector is told so, and the sector stays open.
	for _, header := range []string{"X-Dry-Run", "X-Warning"} {
		for _, route := range []string{method[true], method[false]} {
			refused := warden.doWith(ctx, http.MethodPost, sectorPath(bastion, route), []byte(`{}`), map[string]string{header: "1"})
			said, err := io.ReadAll(refused.Body)
			if err != nil {
				t.Fatal(err)
			}
			if err := refused.Body.Close(); err != nil {
				t.Fatal(err)
			}
			want := "the " + header + ` header takes the value true, or is left off the request: \"1\" is not a value it takes, so the call was not run`
			if refused.StatusCode != http.StatusBadRequest || !strings.Contains(string(said), want) {
				t.Errorf("%s with %s: 1: status %d %s, want 400 saying %s", route, header, refused.StatusCode, said, want)
			}
		}
	}
	if status, got := state(warden, bastion); status != http.StatusOK || got != off {
		t.Fatalf("Bastion's switch after the refused headers: status %d %s, want 200 %s: a refused call turns nothing", status, got, off)
	}

	// The warden, signed in through Bastion's directory, asks the method for its warning.
	// The server writes the words: who will be able to enter, how many of the people
	// seated there will not, and what the button says, and it names the people to him
	// in a list of their own, since a marshal lists the sector's people, and it says
	// that a marshal of the sector itself who has a password still gets in. The warden,
	// headquarters and the rookie signed in through the directory and the governor is a
	// super administrator, so none of them is among them; the rigger, the bosun and the
	// pilot, who sign in with a password and are no marshal of Bastion's own, are.
	status, body := warns(warden, bastion)
	if status != http.StatusOK {
		t.Fatalf("asking what directory only would do to Bastion, signed in through its directory: status %d: %s", status, body)
	}
	var would struct {
		Consequence string   `json:"consequence"`
		Names       []string `json:"names"`
		Confirm     string   `json:"confirm"`
	}
	if err := json.Unmarshal(body, &would); err != nil {
		t.Fatalf("the answer is not an object: %v: %s", err, body)
	}
	if would.Confirm != "Admit only people who sign in through Persona" {
		t.Errorf("the button says %q, want what it does", would.Confirm)
	}
	named := func(username string) bool {
		return slices.ContainsFunc(would.Names, func(name string) bool { return name == username || strings.HasSuffix(name, " ("+username+")") })
	}
	for _, username := range []string{"rigger", "bosun", "pilot"} {
		if !named(username) {
			t.Errorf("the people named = %v, want %s among them: seated at Bastion, with no sign-in through its directory", would.Names, username)
		}
	}
	for _, username := range []string{"warden", "hq", "rookie", "governor"} {
		if named(username) {
			t.Errorf("the people named = %v, want %s left out", would.Names, username)
		}
	}
	// The count takes in the people of every auth the sector seats, and a marshal lists
	// the crew alone: the portal's members seated at Bastion are counted and named to
	// nobody who may not list them, so the sentence says the list is the people the
	// warden may list. A part of them is never read as all of them.
	const onlyPersona = "Only people who sign in through Persona will be able to enter this sector. An administrator of this sector who has a password can still sign in with it and a code sent to their email."
	var counted int
	if _, err := fmt.Sscanf(strings.TrimPrefix(would.Consequence, onlyPersona), " %d people", &counted); err != nil {
		t.Fatalf("the consequence = %q, want %q and then how many people lose access: %v", would.Consequence, onlyPersona, err)
	}
	if counted <= len(would.Names) {
		t.Errorf("the consequence counts %d people and the warning names %d, want more counted than named: the portal's members are counted, and a marshal does not list them", counted, len(would.Names))
	}
	if want := fmt.Sprintf("%s %d people seated here sign in another way and will lose access. The %d of them you may list are named here.", onlyPersona, counted, len(would.Names)); would.Consequence != want {
		t.Errorf("the consequence = %q, want %q", would.Consequence, want)
	}
	if got, want := rigger.domains(ctx), []string{anvil, bastion}; !slices.Equal(got, want) {
		t.Fatalf("the rigger's chart after the warning = %v, want %v: asking writes nothing", got, want)
	}
	if status, got := state(warden, bastion); status != http.StatusOK || got != off {
		t.Fatalf("Bastion's switch after the warning: status %d %s, want 200 %s: asking writes nothing", status, got, off)
	}

	// He turns it on, with the sector's method. The rigger is kept out again at once,
	// her chart and Bastion's list of its people say so, the global record shows the
	// flag, and the warden, through the directory, still enters.
	if status, body := turn(warden, bastion, true); status != http.StatusOK {
		t.Fatalf("turning Bastion's directory only on as its marshal, signed in through its directory: status %d: %s", status, body)
	}
	if status, got := state(warden, bastion); status != http.StatusOK || got != on {
		t.Errorf("Bastion's switch once it is turned on: status %d %s, want 200 %s", status, got, on)
	}
	if status, body := governor.do(ctx, http.MethodGet, consoleAPI+"/sectors/"+bastion, nil); status != http.StatusOK || !strings.Contains(string(body), `"directoryOnly":true`) {
		t.Errorf("the Sectors record of Bastion once it is turned on: status %d, want 200 showing the flag: %s", status, body)
	}
	if got, want := rigger.domains(ctx), []string{anvil}; !slices.Equal(got, want) {
		t.Errorf("the rigger's chart once Bastion is directory only again = %v, want %v", got, want)
	}
	if got := rigger.closed(ctx); !maps.Equal(got, byPassword) {
		t.Errorf("the sectors the rigger's chart names closed = %v, want %v", got, byPassword)
	}
	if got := cannotEnter(ctx, t, warden)["rigger"]; got != keptOut {
		t.Errorf("Bastion's people once it is directory only again: the rigger's row says %q, want %q", got, keptOut)
	}
	if got, want := warden.domains(ctx), []string{anvil, bastion, cinder}; !slices.Equal(got, want) {
		t.Errorf("the warden's chart once Bastion is directory only again = %v, want %v", got, want)
	}
	// Turned on already, there is nothing to turn on: the warning says so and names
	// neither the directory nor the people, to the governor by password and to the warden
	// through the directory alike. Who the switch keeps out now is the people list's to say.
	onAlready := "This sector is directory only already: only people who sign in through its directory enter it. There is nothing to turn on."
	for name, asks := range map[string]*browser{"the governor, by password": governor, "the warden, through the directory": warden} {
		if status, body := warns(asks, bastion); status != http.StatusConflict || !strings.Contains(string(body), onAlready) || strings.Contains(string(body), "rigger") || strings.Contains(string(body), "Persona") {
			t.Errorf("asking what directory only would do to Bastion while it is on, as %s: status %d, want 409 saying %q and naming nobody: %s", name, status, onAlready, body)
		}
	}
	// And a call that leaves it on is plain too.
	if status, body := turn(governor, bastion, true); status != http.StatusOK {
		t.Errorf("turning directory only on for a sector that is directory only already, signed in with a password: status %d, want 200: %s", status, body)
	}

	// A decision, pinned as it is built. The warden holds the marshal's role in every
	// sector and in no sector of its own, and his session is one Bastion's directory
	// opened: in Bastion he does what the role file says of a marshal, turning directory
	// only off included, which no rule holds. He turns it on again, as he did before.
	if status, body := turn(warden, bastion, false); status != http.StatusOK {
		t.Fatalf("turning Bastion's directory only off as a marshal in every sector, signed in through its directory: status %d, want 200: %s", status, body)
	}
	if status, got := state(warden, bastion); status != http.StatusOK || got != off {
		t.Errorf("Bastion's switch once the warden turned it off: status %d %s, want 200 %s", status, got, off)
	}
	if status, body := turn(warden, bastion, true); status != http.StatusOK {
		t.Errorf("turning Bastion's directory only on again as the warden: status %d, want 200: %s", status, body)
	}
}

// cannotEnter reads Bastion's list of its people as the browser's user: each person's
// username with what their row says of the directory-only switch, empty for a person
// it does not keep out.
func cannotEnter(ctx context.Context, t *testing.T, b *browser) map[string]string {
	t.Helper()

	rows := map[string]string{}
	for _, row := range crewSectorList(ctx, t, b, bastion, sectorPeoplePath) {
		sentence, _ := row["cannotEnter"].(string)
		rows[cell[string](t, row, "username")] = sentence
	}

	return rows
}

// firstPasswordCode asks, as the browser's user, for the code a person chooses their
// first password with, and reads it from the message the auth mailed to their confirmed
// address. One request at a time asks for a code on the stack, so the message read is
// this one's.
func firstPasswordCode(ctx context.Context, t *testing.T, b *browser) string {
	t.Helper()

	s := b.served
	s.codeMu.Lock()
	defer s.codeMu.Unlock()
	before := len(s.mail.Messages())
	if status, body := b.do(ctx, http.MethodPost, b.prefix+"/user/first-password/code", nil); status != http.StatusOK {
		t.Fatalf("asking for the code of a first password: status %d: %s", status, body)
	}
	for _, m := range s.mail.Messages()[before:] {
		if match := codeSubject.FindStringSubmatch(m.Subject); match != nil {
			return match[1] + match[2]
		}
	}
	t.Fatalf("no code for a first password was mailed after the request (%d messages before)", before)

	return ""
}

// signInAsPersona signs the browser in on the console as a persona of the cast through
// the simulated persona provider: the login route sends the browser to the callback,
// which signs in whoever APP_PERSONA_USERNAME names. The provider attests for the
// person, so no code follows.
func (b *browser) signInAsPersona(ctx context.Context, login string) {
	b.t.Helper()

	if status, body := b.do(ctx, http.MethodGet, b.prefix+"/user/session", nil); status != http.StatusOK {
		b.t.Fatalf("GET session before %s's login: status %d: %s", login, status, body)
	}
	simulatedLogin.Lock()
	defer simulatedLogin.Unlock()
	if err := os.Setenv("APP_PERSONA_USERNAME", login); err != nil {
		b.t.Fatal(err)
	}
	if status, location := b.loginThrough(ctx, "/user/"+crew.PersonaRegistration+"/login", consolePage); status != http.StatusFound || location != consolePage {
		b.t.Fatalf("%s's persona login: status %d to %q, want %d to %q", login, status, location, http.StatusFound, consolePage)
	}
	if sess := b.session(ctx); sess["authenticated"] != true || sess["username"] != login {
		b.t.Fatalf("the session after %s's persona login = %v, want authenticated as the persona", login, sess)
	}
}
