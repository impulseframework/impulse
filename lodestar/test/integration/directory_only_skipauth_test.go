//go:build skipAuth

package integration

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

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
// Turning directory only off is plain: headquarters does it by password, the rigger
// enters Bastion, and no row says anything. Turning it on is refused unless the person
// is signed in through the sector's directory themselves, and until the sector has a
// directory, each with a sentence the person can act on, on the write and on the read a
// page asks first; nobody charts a sector directly, directory only or not (a sector is
// directory only from the start where its invitation says so and is accepted through
// the directory it will be only of: TestInvitedSector); and a
// caller without the grant is refused by the grants, before any rule answers. Headquarters signed in through the persona provider is told what it would
// do, the sentence and the people it names, and turns it on; once it is on the read has
// nothing to say. One request that turns it on and removes the directory is refused. The persona sign-in sets a process variable, so it holds the lock the
// other simulated sign-ins hold.
//
// Demonstrates: tenancy.directory-only, tenancy.declaration, auth.persona.
func TestDirectoryOnly_theSwitchIsHeldToItsRules(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServedWith(ctx, t, servedOptions{persona: &auth.Persona{Issuer: "https://persona.example.com", ClientID: "lodestar-pr12"}})
	if _, err := s.crew.SeedCast(ctx); err != nil {
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
	keptOut := "Has not signed in through persona, so cannot enter while this sector is directory only"
	people := cannotEnter(ctx, t, warden, bastion)
	for username, want := range map[string]string{"rigger": keptOut, "bosun": keptOut, "pilot": keptOut, "warden": "", "governor": ""} {
		got, listed := people[username]
		if !listed || got != want {
			t.Errorf("Bastion's people: %s is listed = %v saying %q, want listed saying %q", username, listed, got, want)
		}
	}

	// Turning it off is plain: headquarters, signed in with a password, does it. The
	// rigger enters Bastion at once, and no row of its people says anything.
	setDirectoryOnly := func(sector string, on bool) []byte {
		return jsonBody(t, []map[string]any{{"op": "patch", "path": "/sectors/" + sector, "value": map[string]any{"directoryOnly": on}}})
	}
	if status, body := governor.do(ctx, http.MethodPatch, consoleAPI+"/resources", setDirectoryOnly(bastion, false)); status != http.StatusOK {
		t.Fatalf("turning Bastion's directory only off as headquarters, by password: status %d: %s", status, body)
	}
	if got, want := rigger.domains(ctx), []string{anvil, bastion}; !slices.Equal(got, want) {
		t.Errorf("the rigger's chart once Bastion is not directory only = %v, want %v", got, want)
	}
	if got := rigger.closed(ctx); len(got) != 0 {
		t.Errorf("the sectors the rigger's chart names closed once Bastion is not directory only = %v, want none", got)
	}
	for username, sentence := range cannotEnter(ctx, t, warden, bastion) {
		if sentence != "" {
			t.Errorf("Bastion's people while it is not directory only: %s's row says %q, want nothing", username, sentence)
		}
	}

	// Turning it on is refused unless the person is signed in through the sector's
	// directory themselves. The governor holds the grant (Headquarters) and signed in
	// with a password: the read a page asks first and the write are refused in the same
	// words, which say what to do, and nothing is written.
	consequence := func(b *browser, sector string) (int, []byte) {
		return b.do(ctx, http.MethodGet, consoleAPI+"/directory-only?domain="+url.QueryEscape(sector), nil)
	}
	notThroughIt := "Directory only is turned on by a person who is signed in through this sector's directory, OpenID Connect provider persona: that shows the directory works, and that you will not shut yourself out. You signed in with a password. Sign in through OpenID Connect provider persona and try again."
	if status, body := consequence(governor, bastion); status != http.StatusConflict || !strings.Contains(string(body), notThroughIt) {
		t.Errorf("asking what directory only would do to Bastion, signed in with a password: status %d, want 409 saying %q: %s", status, notThroughIt, body)
	}
	if status, body := governor.do(ctx, http.MethodPatch, consoleAPI+"/resources", setDirectoryOnly(bastion, true)); status != http.StatusConflict || !strings.Contains(string(body), notThroughIt) {
		t.Errorf("turning Bastion's directory only on, signed in with a password: status %d, want 409 saying %q: %s", status, notThroughIt, body)
	}
	if got, want := rigger.domains(ctx), []string{anvil, bastion}; !slices.Equal(got, want) {
		t.Fatalf("the rigger's chart after the refused write = %v, want %v: nothing was written", got, want)
	}

	// A sector with no directory has nothing to be only of. Headquarters through the
	// persona provider is refused for Anvil on the read and on the write, and for
	// Cinder, whose directory is the fleet's Workspace and not the one he came through.
	noDirectory := "This sector has no directory, so it cannot be directory only: nobody could enter it. Set its directory first: sign in through your organization's directory, make it this sector's directory, then turn directory only on."
	if status, body := consequence(hq, anvil); status != http.StatusConflict || !strings.Contains(string(body), noDirectory) {
		t.Errorf("asking what directory only would do to Anvil, which has no directory: status %d, want 409 saying %q: %s", status, noDirectory, body)
	}
	if status, body := hq.do(ctx, http.MethodPatch, consoleAPI+"/resources", setDirectoryOnly(anvil, true)); status != http.StatusConflict || !strings.Contains(string(body), noDirectory) {
		t.Errorf("turning directory only on for Anvil, which has no directory: status %d, want 409 saying %q: %s", status, noDirectory, body)
	}
	anotherDirectory := "Directory only is turned on by a person who is signed in through this sector's directory, Google Workspace example: that shows the directory works, and that you will not shut yourself out. You signed in through OpenID Connect provider persona. Sign in through Google Workspace example and try again."
	if status, body := consequence(hq, cinder); status != http.StatusConflict || !strings.Contains(string(body), anotherDirectory) {
		t.Errorf("asking what directory only would do to Cinder, signed in through another sector's directory: status %d, want 409 saying %q: %s", status, anotherDirectory, body)
	}
	if status, body := hq.do(ctx, http.MethodPatch, consoleAPI+"/resources", setDirectoryOnly(cinder, true)); status != http.StatusConflict || !strings.Contains(string(body), anotherDirectory) {
		t.Errorf("turning directory only on for Cinder, signed in through another sector's directory: status %d, want 409 saying %q: %s", status, anotherDirectory, body)
	}
	if got, want := rigger.domains(ctx), []string{anvil, bastion}; !slices.Equal(got, want) {
		t.Fatalf("the rigger's chart after the refusals = %v, want %v: Anvil stays as it was", got, want)
	}

	// Nor is a sector charted directory only from the start, or charted at all: nobody
	// creates a sector directly, so the create is refused with how a sector is made,
	// whatever it carries. A sector that is directory only from the start is made by an
	// invitation that says so, accepted through the directory it will be only of.
	chartClosed := jsonBody(t, []map[string]any{{"op": "add", "path": "/sectors", "value": map[string]any{"name": "Flint", "region": "Near frontier", "established": "2226-11-02", "directoryOnly": true}}})
	if status, body := hq.do(ctx, http.MethodPatch, consoleAPI+"/resources", chartClosed); status != http.StatusBadRequest || !strings.Contains(string(body), "a sector is not created directly") {
		t.Errorf("charting a sector that is directory only from the start: status %d, want 400 saying how a sector is made: %s", status, body)
	}
	if status, body := governor.do(ctx, http.MethodGet, consoleAPI+"/sectors", nil); status != http.StatusOK || strings.Contains(string(body), "Flint") {
		t.Errorf("the sectors after the refused chart: status %d, want 200 and no Flint: %s", status, body)
	}

	// The caller's grants answer before the rules do: the warden holds no Update on the
	// sector record, so the read and the write are refused as on any record, and say
	// nothing of the sector's directory or of how he signed in. A sector that does not
	// exist is not found.
	saysTheRule := func(body []byte) bool {
		return strings.Contains(string(body), "persona") || strings.Contains(string(body), "signed in") || strings.Contains(string(body), "Sign in")
	}
	if status, body := consequence(warden, bastion); status != http.StatusForbidden || saysTheRule(body) {
		t.Errorf("asking what directory only would do as the warden, who holds no grant on the switch: status %d, want 403 that says nothing of the rule: %s", status, body)
	}
	if status, body := warden.do(ctx, http.MethodPatch, consoleAPI+"/resources", setDirectoryOnly(bastion, true)); status != http.StatusForbidden || saysTheRule(body) {
		t.Errorf("turning Bastion's directory only on as the warden: status %d, want 403 that says nothing of the rule: %s", status, body)
	}
	if status, body := consequence(hq, "f0000000-0000-4000-8000-00000000dead"); status != http.StatusNotFound {
		t.Errorf("asking what directory only would do to a sector that does not exist: status %d, want 404: %s", status, body)
	}
	if status, body := hq.do(ctx, http.MethodGet, consoleAPI+"/directory-only", nil); status != http.StatusBadRequest {
		t.Errorf("asking what directory only would do with no sector named: status %d, want 400: %s", status, body)
	}

	// One request cannot turn directory only on and remove the directory it is only of,
	// whichever comes first, though each of the two alone would be made: a sector nobody
	// enters would be left. The warden is signed in through Bastion's directory and, as
	// its marshal, may remove it; made headquarters for the moment, he may write the
	// switch too. Both operations are refused together, and nothing is written.
	var wardenID string
	for _, row := range crewSectorList(ctx, t, governor, bastion, sectorPeoplePath) {
		if cell[string](t, row, "username") == "warden" {
			wardenID = cell[string](t, row, "id")
		}
	}
	if wardenID == "" {
		t.Fatal("Bastion's people do not list the warden")
	}
	headquarters := jsonBody(t, map[string]any{"userId": wardenID, "role": "Headquarters"})
	if status, body := governor.do(ctx, http.MethodPost, crewMethod("assign-crew-role"), headquarters); status != http.StatusNoContent {
		t.Fatalf("making the warden headquarters: status %d: %s", status, body)
	}
	bastionDirectory := directories(ctx, t, governor, bastion)
	if len(bastionDirectory) != 1 {
		t.Fatalf("Bastion's directory = %+v, want the one it is seeded with", bastionDirectory)
	}
	removeIt := map[string]any{"op": "remove", "path": "/sectors/" + bastion + "/sector-directories/" + bastionDirectory[0].ID}
	turnItOn := map[string]any{"op": "patch", "path": "/sectors/" + bastion, "value": map[string]any{"directoryOnly": true}}
	for name, operations := range map[string][]map[string]any{
		"the switch first":  {turnItOn, removeIt},
		"the removal first": {removeIt, turnItOn},
	} {
		if status, body := warden.do(ctx, http.MethodPatch, consoleAPI+"/resources", jsonBody(t, operations)); status != http.StatusConflict || !strings.Contains(string(body), "This same request") {
			t.Errorf("turning Bastion directory only and removing its directory in one request, %s: status %d, want 409 saying the request does both: %s", name, status, body)
		}
	}
	if rows := directories(ctx, t, governor, bastion); len(rows) != 1 || rows[0].ID != bastionDirectory[0].ID {
		t.Errorf("Bastion's directory after the refused requests = %+v, want the one it had", rows)
	}
	if got, want := rigger.domains(ctx), []string{anvil, bastion}; !slices.Equal(got, want) {
		t.Fatalf("the rigger's chart after the refused requests = %v, want %v: Bastion is not directory only", got, want)
	}
	if status, body := governor.do(ctx, http.MethodPost, crewMethod("remove-crew-role"), headquarters); status != http.StatusNoContent {
		t.Fatalf("taking headquarters back from the warden: status %d: %s", status, body)
	}

	// Headquarters, signed in through Bastion's directory, asks what it would do. The
	// server writes the words: who will be able to enter, how many of the people seated
	// there will not, each of them named, and what the button says. The warden and
	// headquarters signed in through the directory and the governor is a super
	// administrator, so none of the three is among them; the rigger, the bosun and the
	// pilot, who sign in with a password, are.
	status, body := consequence(hq, bastion)
	if status != http.StatusOK {
		t.Fatalf("asking what directory only would do to Bastion, signed in through its directory: status %d: %s", status, body)
	}
	var would struct {
		Directory  string `json:"directory"`
		Connection string `json:"connection"`
		Sentence   string `json:"sentence"`
		People     []struct {
			UserID   string  `json:"userId"`
			Username string  `json:"username"`
			Name     *string `json:"name"`
		} `json:"people"`
		Confirm string `json:"confirm"`
	}
	if err := json.Unmarshal(body, &would); err != nil {
		t.Fatalf("the answer is not an object: %v: %s", err, body)
	}
	if would.Directory != "OpenID Connect provider persona" || would.Connection != "persona" || would.Confirm != "Admit only people who sign in through persona" {
		t.Errorf("the answer names the directory %q (%q) and the button %q, want Bastion's persona provider and what the button does", would.Directory, would.Connection, would.Confirm)
	}
	named := make([]string, 0, len(would.People))
	for _, person := range would.People {
		named = append(named, person.Username)
		if person.UserID == "" {
			t.Errorf("the answer names %s with no account id", person.Username)
		}
	}
	if !slices.IsSorted(named) {
		t.Errorf("the people named = %v, want them by username", named)
	}
	for _, username := range []string{"rigger", "bosun", "pilot"} {
		if !slices.Contains(named, username) {
			t.Errorf("the people named = %v, want %s among them: seated at Bastion, with no sign-in through its directory", named, username)
		}
	}
	for _, username := range []string{"warden", "hq", "governor"} {
		if slices.Contains(named, username) {
			t.Errorf("the people named = %v, want %s left out", named, username)
		}
	}
	if want := "Only people who sign in through persona will be able to enter this sector. " + plural(len(named)) + " and will lose access."; would.Sentence != want {
		t.Errorf("the sentence = %q, want %q", would.Sentence, want)
	}
	if got, want := rigger.domains(ctx), []string{anvil, bastion}; !slices.Equal(got, want) {
		t.Fatalf("the rigger's chart after the read = %v, want %v: the read writes nothing", got, want)
	}

	// He turns it on. The rigger is kept out again at once, her chart and Bastion's
	// list of its people say so, and the warden, through the directory, still enters.
	if status, body := hq.do(ctx, http.MethodPatch, consoleAPI+"/resources", setDirectoryOnly(bastion, true)); status != http.StatusOK {
		t.Fatalf("turning Bastion's directory only on as headquarters, signed in through its directory: status %d: %s", status, body)
	}
	if got, want := rigger.domains(ctx), []string{anvil}; !slices.Equal(got, want) {
		t.Errorf("the rigger's chart once Bastion is directory only again = %v, want %v", got, want)
	}
	if got := rigger.closed(ctx); !maps.Equal(got, byPassword) {
		t.Errorf("the sectors the rigger's chart names closed = %v, want %v", got, byPassword)
	}
	if got := cannotEnter(ctx, t, warden, bastion)["rigger"]; got != keptOut {
		t.Errorf("Bastion's people once it is directory only again: the rigger's row says %q, want %q", got, keptOut)
	}
	if got, want := warden.domains(ctx), []string{anvil, bastion, cinder}; !slices.Equal(got, want) {
		t.Errorf("the warden's chart once Bastion is directory only again = %v, want %v", got, want)
	}
	// Turned on already, there is nothing to turn on: the read says so and names neither
	// the directory nor the people, to the governor by password and to headquarters
	// through the directory alike. Who the switch keeps out now is the people list's to say.
	onAlready := "This sector is directory only already: only people who sign in through its directory enter it. There is nothing to turn on."
	for name, asks := range map[string]*browser{"the governor, by password": governor, "headquarters, through the directory": hq} {
		if status, body := consequence(asks, bastion); status != http.StatusConflict || !strings.Contains(string(body), onAlready) || strings.Contains(string(body), "rigger") || strings.Contains(string(body), "persona") {
			t.Errorf("asking what directory only would do to Bastion while it is on, as %s: status %d, want 409 saying %q and naming nobody: %s", name, status, onAlready, body)
		}
	}
	// And a write that leaves it on is plain too.
	if status, body := governor.do(ctx, http.MethodPatch, consoleAPI+"/resources", setDirectoryOnly(bastion, true)); status != http.StatusOK {
		t.Errorf("writing directory only on a sector that is directory only already, signed in with a password: status %d, want 200: %s", status, body)
	}
}

// plural is how the consequence counts the people a sector's switch would keep out.
func plural(n int) string {
	switch n {
	case 0:
		return "Everyone seated here has signed in through it"
	case 1:
		return "1 person seated here signs in another way"
	default:
		return strconv.Itoa(n) + " people seated here sign in another way"
	}
}

// cannotEnter reads a sector's list of its people as the browser's user: each person's
// username with what their row says of the directory-only switch, empty for a person
// it does not keep out.
func cannotEnter(ctx context.Context, t *testing.T, b *browser, sector string) map[string]string {
	t.Helper()

	rows := map[string]string{}
	for _, row := range crewSectorList(ctx, t, b, sector, sectorPeoplePath) {
		sentence, _ := row["cannotEnter"].(string)
		rows[cell[string](t, row, "username")] = sentence
	}

	return rows
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
