// Demonstrates: @attribute.view, @target.view, @attribute, @target, execute-condition, capability-envelope, virtual.keyed-read.
package integration

// sector_pilots_test: a view served as a tenant record, a fact about its row that the
// view's query computes, and a method whose target is the row.
//
// SectorPilots is a sector's pilots, keyed by the pilot's id; a pilot who flies for two
// sectors is a row in each. fliesElsewhere says whether a pilot also flies for another
// sector. The Wing Commander's grants turn on it: the clearance field of the list and
// of the read, and Execute on SetSectorPilotClearance, each under `fliesElsewhere =
// false`. The Sector Marshal holds the fields and the method with no condition.
//
// The seed's squadrons give the cast: Vela, Lior and Wilde fly for Anvil alone; Dunn and
// Pax fly for Anvil and for Bastion; Greer flies with no squadron. The test adds one
// pilot who flies for Bastion alone, Cass, and seats the Wing Commander at Bastion too,
// both in a world of its own.

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
)

const (
	pilotGreerID = "30000000-0000-4000-8000-000000000001" // Governor Greer: flies with no squadron
	pilotCassID  = "30000000-0000-4000-8000-000000000003" // Cadet Cass: Portcullis (bastion) alone, in this test's world
	pilotVelaID  = "30000000-0000-4000-8000-000000000005" // Veteran Vela: Hammer (anvil) alone
	pilotDunnID  = "30000000-0000-4000-8000-000000000007" // Dispatcher Dunn: Hammer and Tongs (anvil), Portcullis (bastion)
	pilotWildeID = "30000000-0000-4000-8000-000000000010" // Wing Commander Wilde: Hammer (anvil) alone

	setSectorPilotClearance = "SetSectorPilotClearance"
)

// newSectorPilotsWorld is a world of the test's own with the real engines: Cass joins
// Portcullis, so Bastion has a pilot Anvil does not, and the Wing Commander is seated at
// Bastion beside Anvil, so the same grants are asked from both sectors.
func newSectorPilotsWorld(t *testing.T) *identityWorld {
	t.Helper()
	ctx := t.Context()
	w := newIdentityWorld(t)

	if _, err := w.db.Apply(ctx, []*spanner.Mutation{
		spanner.InsertMap("SquadronMemberships", map[string]any{"SquadronId": squadronPortcullisID, "UserId": "cadet"}),
	}); err != nil {
		t.Fatalf("putting Cass on Portcullis: %v", err)
	}
	if err := w.engine.UserManager().AddUserRoles(ctx, accesstypes.DomainPolicyScope(bastion), "wingco", "WingCommander"); err != nil {
		t.Fatalf("seating the Wing Commander at Bastion: %v", err)
	}
	if err := waitForDecision(ctx, w.engine, "wingco", bastion, accesstypes.List, "SectorPilots"); err != nil {
		t.Fatalf("the Wing Commander's seat at Bastion never took effect: %v", err)
	}

	return w
}

// sectorPilots lists a sector's pilots as the user, by pilot id.
func sectorPilots(t *testing.T, h http.Handler, user accesstypes.User, sector, query string) map[string]map[string]any {
	t.Helper()

	status, body := doRequestAs(t, h, user, http.MethodGet, sectorPath(sector, "sector-pilots?limit=200"+query), "")
	assertStatus(t, status, http.StatusOK, body)

	return rowsByID(t, decodeRows(t, body), "pilotId")
}

// TestSectorPilotsView holds the view as a tenant record: each sector lists its own
// pilots, a pilot of two sectors is a row in each with that sector's count, the keyed
// read finds a row within the sector alone, and the clearance field is answered only
// where the fact its grant names holds.
func TestSectorPilotsView(t *testing.T) {
	t.Parallel()
	w := newSectorPilotsWorld(t)

	// The field grants this suite proves, pinned to the roles file (conditions-proven).
	provesGrant(t, crew.Roles(), "WingCommander", "List", "SectorPilots", "fliesElsewhere = false")
	provesGrant(t, crew.Roles(), "WingCommander", "Read", "SectorPilots", "fliesElsewhere = false")

	type pilot struct {
		squadrons      float64
		fliesElsewhere bool
	}
	lists := []struct {
		name   string
		user   accesstypes.User
		sector string
		want   map[string]pilot
	}{
		{
			name:   "Anvil's pilots, the two who also fly for Bastion among them",
			user:   "marshal",
			sector: anvil,
			want: map[string]pilot{
				pilotVelaID:  {squadrons: 1},
				pilotLeadID:  {squadrons: 1},
				pilotWildeID: {squadrons: 1},
				pilotDunnID:  {squadrons: 2, fliesElsewhere: true},
				pilotPaxID:   {squadrons: 1, fliesElsewhere: true},
			},
		},
		{
			name:   "Bastion's pilots: the same two, each a row of Bastion's, and Cass",
			user:   "wingco",
			sector: bastion,
			want: map[string]pilot{
				pilotDunnID: {squadrons: 1, fliesElsewhere: true},
				pilotPaxID:  {squadrons: 1, fliesElsewhere: true},
				pilotCassID: {squadrons: 1},
			},
		},
	}
	for _, tt := range lists {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rows := sectorPilots(t, w.h, tt.user, tt.sector, "")
			if len(rows) != len(tt.want) {
				t.Errorf("the list holds %d pilots, want %d: %v", len(rows), len(tt.want), rows)
			}
			for id, want := range tt.want {
				row := rows[id]
				if row == nil {
					t.Errorf("pilot %s is missing from the list", id)

					continue
				}
				got := pilot{squadrons: cell[float64](t, row, "squadrons"), fliesElsewhere: cell[bool](t, row, "fliesElsewhere")}
				if got != want {
					t.Errorf("pilot %s = %+v, want %+v", id, got, want)
				}
			}
		})
	}

	reads := []struct {
		name       string
		sector     string
		pilot      string
		wantStatus int
	}{
		{name: "a pilot of both sectors is read at Anvil", sector: anvil, pilot: pilotPaxID, wantStatus: http.StatusOK},
		{name: "and at Bastion", sector: bastion, pilot: pilotPaxID, wantStatus: http.StatusOK},
		{name: "Anvil's pilot is not found at Bastion", sector: bastion, pilot: pilotVelaID, wantStatus: http.StatusNotFound},
		{name: "Bastion's pilot is not found at Anvil", sector: anvil, pilot: pilotCassID, wantStatus: http.StatusNotFound},
		{name: "a pilot of no squadron is in no sector", sector: anvil, pilot: pilotGreerID, wantStatus: http.StatusNotFound},
	}
	for _, tt := range reads {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status, body := doRequestAs(t, w.h, "wingco", http.MethodGet, sectorPath(tt.sector, "sector-pilots/"+tt.pilot), "")
			assertStatus(t, status, tt.wantStatus, body)
			if tt.wantStatus != http.StatusOK {
				return
			}
			if got := cell[string](t, decodeRow(t, body), "pilotId"); got != tt.pilot {
				t.Errorf("the read answered pilot %s, want %s", got, tt.pilot)
			}
		})
	}

	// The clearance is one grant of the Wing Commander's with a condition on the fact:
	// it is answered on the rows of pilots who fly for the sector alone and left out of
	// the others, in the list and in the keyed read; the Marshal's carries none.
	clearances := []struct {
		name string
		user accesstypes.User
		want map[string]bool
	}{
		{name: "the Wing Commander sees the clearance of Anvil's own", user: "wingco", want: map[string]bool{pilotVelaID: true, pilotLeadID: true, pilotWildeID: true, pilotDunnID: false, pilotPaxID: false}},
		{name: "the Marshal sees every pilot's", user: "marshal", want: map[string]bool{pilotVelaID: true, pilotLeadID: true, pilotWildeID: true, pilotDunnID: true, pilotPaxID: true}},
	}
	for _, tt := range clearances {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rows := sectorPilots(t, w.h, tt.user, anvil, "")
			for id, want := range tt.want {
				if _, got := rows[id]["clearance"]; got != want {
					t.Errorf("pilot %s: clearance in the list = %v, want %v", id, got, want)
				}
				status, body := doRequestAs(t, w.h, tt.user, http.MethodGet, sectorPath(anvil, "sector-pilots/"+id), "")
				assertStatus(t, status, http.StatusOK, body)
				if _, got := decodeRow(t, body)["clearance"]; got != want {
					t.Errorf("pilot %s: clearance in the read = %v, want %v", id, got, want)
				}
			}
		})
	}
}

// TestSectorPilotsMethod holds the method whose target is a row of the view: the row's
// Execute answer lists it exactly where the grant's condition holds, it runs there, it
// is refused with the uniform refusal where the condition fails, and a pilot who does
// not fly for the request's sector is not found, whichever sector they do fly for.
func TestSectorPilotsMethod(t *testing.T) {
	t.Parallel()
	w := newSectorPilotsWorld(t)
	ctx := t.Context()

	// The Execute grant this suite proves, pinned to the roles file (conditions-proven).
	provesGrant(t, crew.Roles(), "WingCommander", "Execute", setSectorPilotClearance, "fliesElsewhere = false")

	envelopes := []struct {
		name   string
		user   accesstypes.User
		sector string
		want   map[string]bool
	}{
		{name: "the Wing Commander at Anvil: on the pilots who fly for Anvil alone", user: "wingco", sector: anvil, want: map[string]bool{pilotVelaID: true, pilotLeadID: true, pilotWildeID: true, pilotDunnID: false, pilotPaxID: false}},
		{name: "the Wing Commander at Bastion: on Cass alone", user: "wingco", sector: bastion, want: map[string]bool{pilotCassID: true, pilotDunnID: false, pilotPaxID: false}},
		{name: "the Marshal at Anvil: on every pilot", user: "marshal", sector: anvil, want: map[string]bool{pilotVelaID: true, pilotLeadID: true, pilotWildeID: true, pilotDunnID: true, pilotPaxID: true}},
	}
	for _, tt := range envelopes {
		t.Run("the row's Execute answer, "+tt.name, func(t *testing.T) {
			rows := sectorPilots(t, w.h, tt.user, tt.sector, "&capabilities=Execute")
			if len(rows) != len(tt.want) {
				t.Errorf("the list holds %d pilots, want %d", len(rows), len(tt.want))
			}
			for id, want := range tt.want {
				caps, _ := rows[id]["zzCapabilities"].(map[string]any)
				methods, _ := caps["Execute"].([]any)
				if got := slices.Contains(methods, any(setSectorPilotClearance)); got != want {
					t.Errorf("pilot %s: Execute answer %v lists the method = %v, want %v", id, methods, got, want)
				}
			}
		})
	}

	// The fires run in order: each allowed one writes the registry, and the next reads it.
	fires := []struct {
		name       string
		user       accesstypes.User
		sector     string
		pilot      string
		clearance  int
		wantStatus int
		// wantBody is a part of the refusal's words; wantClearance is the registry's
		// value after the call.
		wantBody      string
		wantClearance int64
	}{
		{name: "the Wing Commander rates a pilot of Anvil alone", user: "wingco", sector: anvil, pilot: pilotVelaID, clearance: 4, wantStatus: http.StatusNoContent, wantClearance: 4},
		{name: "a pilot who also flies for Bastion is refused, in the uniform words", user: "wingco", sector: anvil, pilot: pilotPaxID, clearance: 5, wantStatus: http.StatusForbidden, wantBody: "SetSectorPilotClearance may not run against SectorPilot " + pilotPaxID, wantClearance: 3},
		{name: "Bastion's pilot is not found at Anvil", user: "wingco", sector: anvil, pilot: pilotCassID, clearance: 5, wantStatus: http.StatusNotFound, wantBody: "SectorPilot " + pilotCassID + " does not exist", wantClearance: 2},
		{name: "a pilot of no squadron is not found", user: "wingco", sector: anvil, pilot: pilotGreerID, clearance: 0, wantStatus: http.StatusNotFound, wantBody: "SectorPilot " + pilotGreerID + " does not exist", wantClearance: 5},
		{name: "the same grant at Bastion rates Cass", user: "wingco", sector: bastion, pilot: pilotCassID, clearance: 3, wantStatus: http.StatusNoContent, wantClearance: 3},
		{name: "Anvil's pilot is not found at Bastion", user: "wingco", sector: bastion, pilot: pilotVelaID, clearance: 0, wantStatus: http.StatusNotFound, wantBody: "SectorPilot " + pilotVelaID + " does not exist", wantClearance: 4},
		{name: "the shared pilot is refused at Bastion as at Anvil", user: "wingco", sector: bastion, pilot: pilotPaxID, clearance: 5, wantStatus: http.StatusForbidden, wantBody: "SetSectorPilotClearance may not run against SectorPilot " + pilotPaxID, wantClearance: 3},
		{name: "the Marshal's grant carries no condition: the shared pilot is rated", user: "marshal", sector: anvil, pilot: pilotPaxID, clearance: 4, wantStatus: http.StatusNoContent, wantClearance: 4},
		{name: "and Bastion's pilot is still not found at Anvil", user: "marshal", sector: anvil, pilot: pilotCassID, clearance: 5, wantStatus: http.StatusNotFound, wantBody: "SectorPilot " + pilotCassID + " does not exist", wantClearance: 3},
		{name: "a caller with no grant is refused before any row is looked for", user: "cadet", sector: anvil, pilot: pilotVelaID, clearance: 0, wantStatus: http.StatusForbidden, wantBody: "does not have Execute", wantClearance: 4},
		{name: "a clearance out of range is the body's to refuse, once the row is found", user: "wingco", sector: anvil, pilot: pilotVelaID, clearance: 9, wantStatus: http.StatusBadRequest, wantBody: "clearance 9 is not a clearance", wantClearance: 4},
	}
	for _, tt := range fires {
		t.Run(tt.name, func(t *testing.T) {
			status, body := doRequestAs(t, w.h, tt.user, http.MethodPost, sectorPath(tt.sector, "set-sector-pilot-clearance"), fmt.Sprintf(`{"pilotId":%q,"clearance":%d}`, tt.pilot, tt.clearance))
			assertStatus(t, status, tt.wantStatus, body)
			if tt.wantBody != "" && !strings.Contains(string(body), tt.wantBody) {
				t.Errorf("the answer %s lacks %q", body, tt.wantBody)
			}
			if got := readColumn[int64](ctx, t, w.db, "Pilots", spanner.Key{tt.pilot}, "Clearance"); got != tt.wantClearance {
				t.Errorf("the registry's clearance for %s = %d, want %d", tt.pilot, got, tt.wantClearance)
			}
		})
	}

	// The fact moved with the data: once Pax leaves Portcullis, Pax flies for Anvil
	// alone, and the Wing Commander's grant admits the same call it refused.
	if _, err := w.db.Apply(ctx, []*spanner.Mutation{spanner.Delete("SquadronMemberships", spanner.Key{squadronPortcullisID, "pilot"})}); err != nil {
		t.Fatalf("taking Pax off Portcullis: %v", err)
	}
	status, body := doRequestAs(t, w.h, "wingco", http.MethodPost, sectorPath(anvil, "set-sector-pilot-clearance"), fmt.Sprintf(`{"pilotId":%q,"clearance":2}`, pilotPaxID))
	assertStatus(t, status, http.StatusNoContent, body)
	if got := readColumn[int64](ctx, t, w.db, "Pilots", spanner.Key{pilotPaxID}, "Clearance"); got != 2 {
		t.Errorf("Pax's clearance after leaving Bastion = %d, want 2", got)
	}
	status, body = doRequestAs(t, w.h, "wingco", http.MethodPost, sectorPath(bastion, "set-sector-pilot-clearance"), fmt.Sprintf(`{"pilotId":%q,"clearance":2}`, pilotPaxID))
	assertStatus(t, status, http.StatusNotFound, body)
}
