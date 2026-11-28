// Demonstrates: cell-masking, pii, tenancy.concealed, login-manifest, auth.groups, auth.periods.
package integration

// The persona-view suite (design plan §4): each persona's first screen, derived from
// the §7 grants read against the seed — non-empty where it should be, empty where it
// should be, concealed where they hold nothing.

import (
	"net/http"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
)

func TestDemoPersonaViews(t *testing.T) {
	t.Parallel()

	_, h, _ := sharedWorld(t)

	// The grant this suite proves, pinned to the roles file (conditions-proven).
	provesGrant(t, crew.Roles(), "BulletinOfficer", "Execute", "IssueBulletin", "now < '2099-06-30T00:00:00Z'")

	tests := []struct {
		name       string
		user       accesstypes.User
		target     string
		wantStatus int
		wantRows   int
		check      func(t *testing.T, respBody []byte)
	}{
		{name: "the governor sees every Anvil mission", user: "governor", target: sectorPath(anvil, "missions?limit=200"), wantStatus: http.StatusOK, wantRows: 30},
		{name: "the governor's first page is the descriptor's default of twenty-five", user: "governor", target: sectorPath(anvil, "missions"), wantStatus: http.StatusOK, wantRows: 25},
		{name: "the governor sees Cinder", user: "governor", target: sectorPath(cinder, "missions"), wantStatus: http.StatusOK, wantRows: 1},
		{name: "the marshal sees every Anvil mission", user: "marshal", target: sectorPath(anvil, "missions?limit=200"), wantStatus: http.StatusOK, wantRows: 30},
		{name: "the marshal's second sector is concealed", user: "marshal", target: sectorPath(bastion, "missions"), wantStatus: http.StatusNotFound},
		{name: "the cadet's board is the two low-hazard classes", user: "cadet", target: sectorPath(anvil, "missions?limit=200"), wantStatus: http.StatusOK, wantRows: 14},
		// The deckhand holds no role in a sector of their own: Anvil's deck crew, a
		// group kept in the sector, holds Cadet and SectorCrew there, and the deckhand
		// is in it. What the group holds is the deckhand's wherever the group holds it,
		// and nowhere else.
		{name: "the deckhand's board is the cadet's, held through the deck crew alone", user: "deckhand", target: sectorPath(anvil, "missions?limit=200"), wantStatus: http.StatusOK, wantRows: 14},
		{name: "the deckhand sees the sector's hangars as the deck crew does", user: "deckhand", target: sectorPath(anvil, "hangars"), wantStatus: http.StatusOK, wantRows: 2},
		{name: "the deck crew holds nothing in Bastion, so the sector is concealed from the deckhand", user: "deckhand", target: sectorPath(bastion, "missions"), wantStatus: http.StatusNotFound},
		{name: "the deck crew gives no hangar deck, as the cadet's own roles give none", user: "deckhand", target: sectorPath(anvil, "refits"), wantStatus: http.StatusForbidden},
		// The relief officer is on a tour of Anvil: the cadet's roles there, each given
		// with an end thirty days from the day the world is made. Inside the tour they
		// grant what the roles grant; what follows its end is the periods' own suite.
		{name: "the relief officer's board is the cadet's while her tour lasts", user: "relief", target: sectorPath(anvil, "missions?limit=200"), wantStatus: http.StatusOK, wantRows: 14},
		{name: "the tour is of Anvil alone, so Bastion is concealed from the relief officer", user: "relief", target: sectorPath(bastion, "missions"), wantStatus: http.StatusNotFound},
		{
			name: "the archivist's fee is redacted until a mission completed",
			user: "archivist", target: sectorPath(anvil, "missions?limit=200"), wantStatus: http.StatusOK, wantRows: 7,
			check: func(t *testing.T, respBody []byte) {
				t.Helper()
				for _, row := range decodeRows(t, respBody) {
					_, hasFee := row["fee"]
					_, hasSettlement := row["settlement"]
					completed := row["statusId"] == "completed"
					if hasFee != completed || hasSettlement != completed {
						t.Errorf("mission %v: fee %v settlement %v visible, want both = %v", row["id"], hasFee, hasSettlement, completed)
					}
				}
			},
		},
		{
			name: "the archivist's distress calls carry no caller contact",
			user: "archivist", target: sectorPath(anvil, "distress-calls?limit=100"), wantStatus: http.StatusOK, wantRows: 12,
			check: func(t *testing.T, respBody []byte) {
				t.Helper()
				for _, row := range decodeRows(t, respBody) {
					if _, ok := row["callerContact"]; ok {
						t.Errorf("callerContact visible to the archivist: %v", row)
					}
				}
			},
		},
		{name: "the engineer runs the hangar deck", user: "engineer", target: sectorPath(anvil, "refits?limit=100"), wantStatus: http.StatusOK, wantRows: 9},
		{name: "the quartermaster sees every expense", user: "quartermaster", target: sectorPath(anvil, "sortie-expenses?limit=100"), wantStatus: http.StatusOK, wantRows: 12},
		{name: "the supercargo's hold", user: "supercargo", target: sectorPath(anvil, "consignments?limit=100"), wantStatus: http.StatusOK, wantRows: 13},
		{name: "the supercargo's first page is the hold's default of ten", user: "supercargo", target: sectorPath(anvil, "consignments"), wantStatus: http.StatusOK, wantRows: 10},
		{name: "the hazard analyst's board, every row through limit=all", user: "hazards", target: sectorPath(anvil, "sector-hazard-boards?limit=all"), wantStatus: http.StatusOK, wantRows: 32},
		{name: "the yeoman's standing orders, a key-less list served whole on a bare GET", user: "yeoman", target: "/console/api/standing-orders", wantStatus: http.StatusOK, wantRows: 6},
		{name: "the purser's expense manifests, one per Anvil mission", user: "purser", target: sectorPath(anvil, "expense-manifests?limit=200"), wantStatus: http.StatusOK, wantRows: 30},
		{name: "the marshal holds no purse", user: "marshal", target: sectorPath(anvil, "expense-manifests"), wantStatus: http.StatusForbidden},
		{name: "the registrar's document register, empty until a document is attached", user: "registrar", target: sectorPath(anvil, "mission-documents"), wantStatus: http.StatusOK, wantRows: 0},
		{name: "the registrar's register is Anvil's alone", user: "registrar", target: sectorPath(bastion, "mission-documents"), wantStatus: http.StatusNotFound},
		{name: "the photographer lists the hangar's tasks", user: "photographer", target: sectorPath(anvil, "refit-tasks?limit=100"), wantStatus: http.StatusOK},
		{name: "the photographer's hangar is Anvil's alone", user: "photographer", target: sectorPath(bastion, "refit-tasks"), wantStatus: http.StatusNotFound},
		{name: "the cadet has no hangar deck", user: "cadet", target: sectorPath(anvil, "refits"), wantStatus: http.StatusForbidden},
		{name: "the armorer's hangars are the two a pass opens to his squadron", user: "armorer", target: sectorPath(anvil, "hangars"), wantStatus: http.StatusOK, wantRows: 2},
		{name: "the armorer holds nothing else of the sector: no wings", user: "armorer", target: sectorPath(anvil, "wings"), wantStatus: http.StatusForbidden},
		{name: "the sentry's hangar is the one a pass opens to his group", user: "sentry", target: sectorPath(anvil, "hangars"), wantStatus: http.StatusOK, wantRows: 1},
		{name: "the sentry holds nothing else of the sector: no wings", user: "sentry", target: sectorPath(anvil, "wings"), wantStatus: http.StatusForbidden},
		{name: "the bulletin officer issues a bulletin while the authorization holds", user: "marshal", target: "/console/api/issue-bulletin", wantStatus: http.StatusOK},
		{name: "a crew member without the authorization is refused", user: "cadet", target: "/console/api/issue-bulletin", wantStatus: http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			method, body := http.MethodGet, ""
			if tt.target == "/console/api/issue-bulletin" {
				method, body = http.MethodPost, `{"announcement":"All hands: drill at 0600"}`
			}
			status, respBody := doRequestAs(t, h, tt.user, method, tt.target, body)
			if status != tt.wantStatus {
				t.Fatalf("status = %d, want %d: %s", status, tt.wantStatus, respBody)
			}
			if tt.wantRows > 0 {
				if rows := decodeRows(t, respBody); len(rows) != tt.wantRows {
					t.Fatalf("rows = %d, want %d: %s", len(rows), tt.wantRows, respBody)
				}
			}
			if tt.check != nil {
				tt.check(t, respBody)
			}
		})
	}
}
