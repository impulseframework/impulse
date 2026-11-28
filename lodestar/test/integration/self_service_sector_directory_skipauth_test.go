//go:build skipAuth

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
	"github.com/impulseframework/impulse/resource"
)

// The words the served stack answers a sector's creation and the warning of opening a
// sector to its directory with, where a person belongs to one sector.
const (
	// directoryHasASector is the create-sector route's refusal of a person whose
	// organization's directory has a sector already.
	directoryHasASector = "Google Workspace example already has a sector here, and a directory belongs to one sector. Ask that sector's administrator to invite you."
	// openTakesPeople is the note the warning of opening a sector to its directory adds
	// where a person belongs to one sector.
	openTakesPeople = "A person belongs to one sector here. People who sign in through example and belong to another sector are moved to this one the next time they sign in, and lose everything they hold in the other."
)

// createTheSectorWithTheWorkspace is the statement a person reads before creating a sector
// takes the fleet's Workspace, where a person belongs to one sector, in its creating forms.
var createTheSectorWithTheWorkspace = resource.Warning{
	Consequence: "You are stating that you are authorized to act on behalf of example.",
	Also: []resource.WarningNote{{
		Says: "From now on, people at example who sign in through it and have no account here are sent to the sector you are creating. They get in by your invitation, or with a starting role if you open the sector to them. No other sector can use example.",
	}},
	Confirm: "Create the sector with example as its directory",
}

// sectorOffer is what the create-sector route's GET answers the browser: whether it may
// create a sector, and the statement it reads first where the creation takes a directory.
type sectorOffer struct {
	Offered bool              `json:"offered"`
	Warning *resource.Warning `json:"warning"`
}

// offerOf reads the create-sector route's offer as the browser.
func offerOf(ctx context.Context, t *testing.T, b *browser) sectorOffer {
	t.Helper()

	status, body := b.do(ctx, http.MethodGet, createSectorRoute, nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s: status %d: %s", createSectorRoute, status, body)
	}
	var offer sectorOffer
	if err := json.Unmarshal(body, &offer); err != nil {
		t.Fatalf("json.Unmarshal(%s): %v", body, err)
	}

	return offer
}

// freeTheFleetWorkspace takes the fleet's Workspace off Cinder as the governor, marshal in
// every sector, so it is no sector's directory: a stranger the Workspace vouches for is
// then admitted by the Google registration's own choice, and seated nowhere.
func freeTheFleetWorkspace(ctx context.Context, t *testing.T, governor *browser) {
	t.Helper()

	rows := directories(ctx, t, governor, cinder)
	if len(rows) != 1 || rows[0].Connection != "example" {
		t.Fatalf("Cinder's directory = %+v, want the seeded fleet Workspace", rows)
	}
	remove := `[{"op":"remove","path":"/sectors/` + cinder + `/sector-directories/` + rows[0].ID + `"}]`
	if status, body := governor.do(ctx, http.MethodPatch, consoleAPI+"/resources", []byte(remove)); status != http.StatusOK {
		t.Fatalf("taking the fleet's Workspace off Cinder: status %d: %s", status, body)
	}
}

// signedUpThroughWorkspace signs a stranger in through the fleet's Workspace on a fresh
// browser, the Google registration open to sign-up, and answers the browser: the
// Workspace is one the fleet administers, so the session opens at once.
func signedUpThroughWorkspace(ctx context.Context, t *testing.T, s *served, user string) *browser {
	t.Helper()

	b := newBrowser(t, s, consoleAPI)
	if status, body := b.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET session before %s's Google login: status %d: %s", user, status, body)
	}
	if status, location := b.loginConsoleGoogle(ctx, user); status != http.StatusFound || location != consolePage {
		t.Fatalf("%s's Google login: status %d to %q, want %d to %q", user, status, location, http.StatusFound, consolePage)
	}
	if got := b.domains(ctx); len(got) != 0 {
		t.Fatalf("%s's sectors after the sign-up = %v, want none", user, got)
	}

	return b
}

// TestSelfServiceSector_signUpTakesNoDirectory proves that a sector made by sign-up takes
// no directory in an application that lets a person hold several sectors: two colleagues
// who signed in through their organization's Workspace each chart a sector, both are
// created and both stand, and neither has a directory; the offer each reads first carries
// no statement. The warning of opening a sector to its directory says nothing of moving
// people here.
func TestSelfServiceSector_signUpTakesNoDirectory(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	governor := newBrowser(t, s, consoleAPI)
	governor.signIn(ctx, "governor")

	// Cinder has the fleet's Workspace: its warning holds no note of people moved.
	status, body := askCrewWarning(ctx, governor, sectorPath(cinder, "open-sector-to-directory"), `{"role":"Pilot"}`)
	if status != http.StatusOK || strings.Contains(string(body), "A person belongs to one sector here") {
		t.Errorf("the warning of opening Cinder to its directory: status %d: %s, want 200 with no note of people moved", status, body)
	}

	freeTheFleetWorkspace(ctx, t, governor)
	openCrewSignUp(ctx, t, governor, crew.GoogleRegistration)
	charted := map[string]string{}
	for user, chart := range map[string]string{
		"ada": `{"name":"Ada's Reach","region":"Near frontier","established":"2226-10-20"}`,
		"bea": `{"name":"Bea's Reach","region":"Near frontier","established":"2226-10-21"}`,
	} {
		b := signedUpThroughWorkspace(ctx, t, s, user)
		// A person may hold several sectors, so creating one takes no directory, and
		// the offer carries no statement.
		if offer := offerOf(ctx, t, b); !offer.Offered || offer.Warning != nil {
			t.Errorf("%s's offer through the Workspace = %+v, want offered with no statement", user, offer)
		}
		status, body := b.do(ctx, http.MethodPost, createSectorRoute, []byte(chart))
		if status != http.StatusOK {
			t.Fatalf("%s charts a sector through the Workspace: status %d, want 200: %s", user, status, body)
		}
		key := createdKey(t, user+"'s sector", body)
		charted[user] = key
		if got := b.domains(ctx); !slices.Equal(got, []string{key}) {
			t.Errorf("%s's sectors after charting = %v, want %v", user, got, []string{key})
		}
		if rows := directories(ctx, t, b, key); len(rows) != 0 {
			t.Errorf("%s's sector's directory = %+v, want none: a sector made by sign-up takes no directory", user, rows)
		}
	}
	if charted["ada"] == charted["bea"] {
		t.Fatalf("both colleagues answered the sector %s, want one each", charted["ada"])
	}
	if rows := directories(ctx, t, governor, charted["ada"]); len(rows) != 0 {
		t.Errorf("Ada's sector's directory, read by the governor = %+v, want none", rows)
	}
}

// TestSelfServiceSector_oneTenantPerPerson proves the creation where a person belongs to
// one sector (identity.WithOneTenantPerPerson, on a served stack built with it): a person
// who signed in through their organization's Workspace charts a sector and it takes the
// Workspace as its directory in the row's transaction; a colleague who signed in through
// the same Workspace is then refused a sector of their own (409, saying to ask for an
// invitation) and nothing is written; a person who signed up with a personal Google
// account charts a sector with no directory. The offer of a creation that takes the
// Workspace carries the statement the person reads first, in its creating forms; a
// personal account's and a password session's carry none. The warning of opening the new
// sector to its directory says that its people who belong to another sector are moved
// here.
//
// Demonstrates: tenancy.creator-directory.
func TestSelfServiceSector_oneTenantPerPerson(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServedWith(ctx, t, servedOptions{oneTenantPerPerson: true})
	governor := newBrowser(t, s, consoleAPI)
	governor.signIn(ctx, "governor")
	freeTheFleetWorkspace(ctx, t, governor)
	openCrewSignUp(ctx, t, governor, crew.GoogleRegistration)
	openCrewSignUp(ctx, t, governor, crew.SignUpRegistration)

	// Both colleagues sign in through the Workspace while it is no sector's directory.
	ada := signedUpThroughWorkspace(ctx, t, s, "ada")
	bea := signedUpThroughWorkspace(ctx, t, s, "bea")

	// Ada's creation would take the Workspace, so the offer carries the statement she
	// reads first, in its creating forms. The governor came by password, which is no
	// organization's directory: his offer carries none.
	if offer := offerOf(ctx, t, ada); !offer.Offered || offer.Warning == nil || !reflect.DeepEqual(*offer.Warning, createTheSectorWithTheWorkspace) {
		t.Errorf("Ada's offer through the Workspace = %+v, want offered with %+v", offer, createTheSectorWithTheWorkspace)
	}
	if offer := offerOf(ctx, t, governor); offer.Warning != nil {
		t.Errorf("the governor's offer by password carries the statement %+v, want none", offer.Warning)
	}

	status, body := ada.do(ctx, http.MethodPost, createSectorRoute, []byte(`{"name":"Ada's Reach","region":"Near frontier","established":"2226-10-20"}`))
	if status != http.StatusOK {
		t.Fatalf("Ada charts a sector through the Workspace: status %d, want 200: %s", status, body)
	}
	reach := createdKey(t, "Ada's sector", body)
	if got := ada.domains(ctx); !slices.Equal(got, []string{reach}) {
		t.Errorf("Ada's sectors after charting = %v, want %v", got, []string{reach})
	}
	rows := directories(ctx, t, ada, reach)
	if len(rows) != 1 || rows[0].Method != "google" || rows[0].Connection != "example" || rows[0].DefaultSeat != nil {
		t.Fatalf("Ada's sector's directory = %+v, want the Workspace she signed in through, with no default role", rows)
	}

	// Her colleague is refused: the Workspace has a sector, and nothing is written.
	sectors := tableRows(ctx, t, s, "Sectors")
	status, body = bea.do(ctx, http.MethodPost, createSectorRoute, []byte(`{"name":"Bea's Reach","region":"Near frontier","established":"2226-10-21"}`))
	if status != http.StatusConflict || !strings.Contains(string(body), directoryHasASector) {
		t.Errorf("Bea charts a sector through the same Workspace: status %d, want 409 saying %q: %s", status, directoryHasASector, body)
	}
	if after := tableRows(ctx, t, s, "Sectors"); after != sectors {
		t.Errorf("sectors after Bea's refused creation = %d, want the %d there were", after, sectors)
	}
	if got := bea.domains(ctx); len(got) != 0 {
		t.Errorf("Bea's sectors after the refusal = %v, want none", got)
	}

	// A personal Google account is no organization's directory: its sector has none.
	cal := newBrowser(t, s, consoleAPI)
	if status, body := cal.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET session before Cal's sign-up: status %d: %s", status, body)
	}
	if status, location := cal.loginConsoleSignUp(ctx, "cal"); status != http.StatusFound || !strings.Contains(location, "pending=mfa") {
		t.Fatalf("Cal's sign-up with a personal account: status %d to %q, want %d to the second factor's wait", status, location, http.StatusFound)
	}
	cal.secondFactor(ctx, "cal")
	if offer := offerOf(ctx, t, cal); !offer.Offered || offer.Warning != nil {
		t.Errorf("Cal's offer through a personal account = %+v, want offered with no statement", offer)
	}
	status, body = cal.do(ctx, http.MethodPost, createSectorRoute, []byte(`{"name":"Cal's Reach","region":"Near frontier","established":"2226-10-22"}`))
	if status != http.StatusOK {
		t.Fatalf("Cal charts a sector with a personal account: status %d, want 200: %s", status, body)
	}
	if rows := directories(ctx, t, cal, createdKey(t, "Cal's sector", body)); len(rows) != 0 {
		t.Errorf("Cal's sector's directory = %+v, want none: a personal account is no organization's directory", rows)
	}

	// Opening Ada's sector to its directory warns that its people who belong to another
	// sector are moved here.
	status, body = askCrewWarning(ctx, ada, sectorPath(reach, "open-sector-to-directory"), `{"role":"Pilot"}`)
	if status != http.StatusOK {
		t.Fatalf("the warning of opening Ada's sector to its directory: status %d: %s", status, body)
	}
	if notes := decodeWarning(t, body).Also; !slices.ContainsFunc(notes, func(note resource.WarningNote) bool { return note.Says == openTakesPeople }) {
		t.Errorf("the warning's notes = %+v, want %q among them", notes, openTakesPeople)
	}
}
