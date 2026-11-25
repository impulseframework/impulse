//go:build skipAuth

package integration

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
	"github.com/impulseframework/impulse/resource"
)

// The statement a person reads before a sector takes the fleet's Workspace, in Lodestar's
// word, as the Workspace's registration names it.
var takeTheWorkspace = resource.Warning{
	Consequence: "You are stating that you are authorized to act on behalf of example.",
	Also: []resource.WarningNote{{
		Says: "From now on, people at example who sign in through it and have no account here are sent to this sector. They get in by your invitation, or with a starting role if you open the sector to them. No other sector can use example.",
	}},
	Confirm: "Make example this sector's directory",
}

// TestSectorDirectoryTake_theMarshalStatesTheyAreAuthorized proves the method by which a
// sector takes the directory its marshal is signed in through (TakeSectorDirectory), on
// the served stack. Nobody checks who may act for an organization, so the act is
// attested: asked for its warning (X-Warning: true) by a marshal of a sector with no
// directory who signed in through the fleet's Workspace, it answers the statement, that
// they are authorized to act on behalf of the Workspace, what taking it does, and the
// words for the button, and writes nothing; called, the sector has the Workspace as its
// directory, with no starting role. Every refusal answers the call and the ask alike, in
// its own words, and neither says the statement: a crew member who holds no grant on the
// method is refused as on any method; a session that came by password, a personal
// Google account, a sector that has its directory, and a directory another sector has
// are each refused with what to do. The record's own create is refused: no role grants
// Create on it any more, so the grants refuse it, the marshal's and the governor's alike
// (to a role file that still grants it, the create answers the sentence that names the
// method, which the generator's own test holds). The directory sign-ins set a process
// variable, so their logins hold the lock the other simulated sign-ins hold.
func TestSectorDirectoryTake_theMarshalStatesTheyAreAuthorized(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	governor := newBrowser(t, s, consoleAPI)
	governor.signIn(ctx, "governor")

	// The navigator comes through the fleet's Workspace, which seats him at Cinder as
	// crew; the governor then takes the Workspace off Cinder, so no sector has it.
	navigator := newBrowser(t, s, consoleAPI)
	if status, body := navigator.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET session before the navigator's login: status %d: %s", status, body)
	}
	if status, location := navigator.loginConsoleGoogle(ctx, navigatorUser); status != http.StatusFound || location != consolePage {
		t.Fatalf("the navigator's Google login: status %d to %q, want %d to %q", status, location, http.StatusFound, consolePage)
	}
	freeTheFleetWorkspace(ctx, t, governor)

	take := func(b *browser, sector string) (int, []byte) {
		return b.do(ctx, http.MethodPost, sectorPath(sector, "take-sector-directory"), []byte(`{}`))
	}
	ask := func(b *browser, sector string) (int, []byte) {
		return askCrewWarning(ctx, b, sectorPath(sector, "take-sector-directory"), `{}`)
	}
	// refusedAlike holds the call and the ask to one refusal: the status, the words, no
	// statement, and nothing written.
	refusedAlike := func(step string, b *browser, sector string, wantStatus int, want string) {
		t.Helper()

		before := tableRows(ctx, t, s, "SectorDirectories")
		for name, call := range map[string]func(*browser, string) (int, []byte){"the call": take, "the ask": ask} {
			status, body := call(b, sector)
			if status != wantStatus || !strings.Contains(string(body), want) || strings.Contains(string(body), "You are stating") {
				t.Errorf("%s, %s: status %d, want %d saying %q and no statement: %s", step, name, status, wantStatus, want, body)
			}
		}
		if after := tableRows(ctx, t, s, "SectorDirectories"); after != before {
			t.Errorf("%s: the directories table holds %d row(s), want the %d it held", step, after, before)
		}
	}

	// The navigator is crew at Cinder, which holds no grant on the method: he is
	// refused as on any method, before a rule answers or the statement is said.
	refusedAlike("taking Cinder's directory as its crew", navigator, cinder, http.StatusForbidden, "")

	// The governor is marshal in every sector, and is signed in with a password.
	refusedAlike("taking Anvil's directory from a password session", governor, anvil, http.StatusConflict,
		"You are signed in with a password. A directory is set by signing in through it")

	// Made Anvil's marshal, the navigator asks for the statement: it is said in the
	// Workspace's name, and nothing is written.
	marshalOfAnvil := jsonBody(t, map[string]any{"userId": crewAccountID(ctx, t, governor, navigatorUser), "role": "SectorMarshal", "domain": anvil})
	if status, body := governor.do(ctx, http.MethodPost, crewMethod("assign-crew-role"), marshalOfAnvil); status != http.StatusNoContent {
		t.Fatalf("making the navigator Anvil's marshal: status %d: %s", status, body)
	}
	events := len(changeSources(ctx, t, s, "SectorDirectories"))
	status, body := ask(navigator, anvil)
	if status != http.StatusOK {
		t.Fatalf("asking for the statement before Anvil takes the Workspace: status %d, want 200: %s", status, body)
	}
	if got := decodeWarning(t, body); !reflect.DeepEqual(got, takeTheWorkspace) {
		t.Errorf("the statement before Anvil takes the Workspace = %+v, want %+v", got, takeTheWorkspace)
	}
	if rows := directories(ctx, t, governor, anvil); len(rows) != 0 {
		t.Errorf("Anvil's directory after the statement was asked = %+v, want none", rows)
	}
	if got := len(changeSources(ctx, t, s, "SectorDirectories")); got != events {
		t.Errorf("change events on SectorDirectories after the statement was asked = %d, want the %d there were", got, events)
	}

	// Called, Anvil takes the Workspace the navigator is signed in through.
	if status, body := take(navigator, anvil); status != http.StatusOK {
		t.Fatalf("Anvil takes the Workspace: status %d, want 200: %s", status, body)
	}
	rows := directories(ctx, t, governor, anvil)
	if len(rows) != 1 || rows[0].Method != "google" || rows[0].Connection != "example" || rows[0].DefaultSeat != nil {
		t.Fatalf("Anvil's directory = %+v, want the Workspace the navigator is signed in through, with no starting role", rows)
	}
	if got := len(changeSources(ctx, t, s, "SectorDirectories")); got != events+1 {
		t.Errorf("change events on SectorDirectories after the take = %d, want %d", got, events+1)
	}

	// A sector has one directory, and a directory belongs to one sector.
	refusedAlike("taking the Workspace again for Anvil", navigator, anvil, http.StatusConflict,
		"Google Workspace example is already this sector's directory.")
	marshalOfCinder := jsonBody(t, map[string]any{"userId": crewAccountID(ctx, t, governor, navigatorUser), "role": "SectorMarshal", "domain": cinder})
	if status, body := governor.do(ctx, http.MethodPost, crewMethod("assign-crew-role"), marshalOfCinder); status != http.StatusNoContent {
		t.Fatalf("making the navigator Cinder's marshal: status %d: %s", status, body)
	}
	refusedAlike("taking Anvil's Workspace for Cinder", navigator, cinder, http.StatusConflict,
		"Google Workspace example already has a sector here, and a directory belongs to one sector. Ask that sector's administrator to invite you.")

	// A personal Google account is no organization's directory: Cal, who signed up with
	// one and charted a sector of her own, is its marshal and is refused.
	openCrewSignUp(ctx, t, governor, crew.SignUpRegistration)
	cal := newBrowser(t, s, consoleAPI)
	if status, body := cal.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET session before Cal's sign-up: status %d: %s", status, body)
	}
	if status, location := cal.loginConsoleSignUp(ctx, "cal"); status != http.StatusFound || !strings.Contains(location, "pending=mfa") {
		t.Fatalf("Cal's sign-up with a personal account: status %d to %q, want %d to the second factor's wait", status, location, http.StatusFound)
	}
	cal.secondFactor(ctx, "cal")
	status, body = cal.do(ctx, http.MethodPost, createSectorRoute, []byte(`{"name":"Cal's Reach","region":"Near frontier","established":"2226-10-22"}`))
	if status != http.StatusOK {
		t.Fatalf("Cal charts a sector with a personal account: status %d, want 200: %s", status, body)
	}
	refusedAlike("taking a personal Google account for Cal's sector", cal, createdKey(t, "Cal's sector", body), http.StatusConflict,
		"A personal Google account is not an organization's directory, so it cannot be a sector's directory. Sign in with your organization's account (Google Workspace or Microsoft Entra) and try again.")

	// The record's own create is refused: the marshal's role grants the method in its
	// place, so the grants refuse the create, the navigator's through the Workspace and
	// the governor's alike.
	create := []byte(`[{"op":"add","path":"/sectors/` + cinder + `/sector-directories","value":{}}]`)
	for name, b := range map[string]*browser{"the navigator, Cinder's marshal": navigator, "the governor, marshal in every sector": governor} {
		if status, body := b.do(ctx, http.MethodPatch, consoleAPI+"/resources", create); status != http.StatusForbidden {
			t.Errorf("creating Cinder's directory as %s: status %d, want 403: %s", name, status, body)
		}
	}
	if rows := directories(ctx, t, governor, cinder); len(rows) != 0 {
		t.Errorf("Cinder's directory after the refused creates = %+v, want none", rows)
	}
}
