//go:build skipAuth

package integration

// tenant_removal_list_skipauth_test: the list a sector's People page reads, as a sector's
// administrator whose session another sector does not admit.
//
// A row of a sector's people offers some acts only to a caller whose session enters the
// tenants the person administers (@callerEnters), and keeps some off the caller's own
// row (@notOnOwnAccount); Disable and Delete are both, and the removal from the sector is
// the second alone. The list answers every row's acts in one statement, and the
// statement asks which tenants the caller enters only while the session does not enter
// some tenant: the lists' world admits every session to every sector, so its suites never
// render that check. Here the caller is the dev seed's marshal, Marshal Maren (Sector
// marshal at Anvil, and the crew's roles for the whole application), signed in with a
// password. The seed makes Bastion directory only, so his session does not enter it. The
// statement holds the check once, however the acts held to it are marked, and the list
// answers within the joins Spanner allows a statement.

import (
	"net/http"
	"slices"
	"testing"
)

// TestTenantRemoval_aMarshalKeptOutOfASectorListsHisOwnSectorsPeople holds the list a
// sector's People page reads, as Anvil's marshal whose session Bastion does not admit:
// it answers 200, the marshal's own row offers none of Disable, Delete and the removal
// from the sector, and the row of a person of Anvil's alone offers all three.
func TestTenantRemoval_aMarshalKeptOutOfASectorListsHisOwnSectorsPeople(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := newServed(ctx, t)

	governor := newBrowser(t, s, consoleAPI)
	governor.signIn(ctx, "governor")
	marshal := newBrowser(t, s, consoleAPI)
	marshal.signIn(ctx, "marshal")

	if status, body := governor.do(ctx, http.MethodGet, consoleAPI+"/sectors/"+bastion, nil); status != http.StatusOK || decodeRow(t, body)["directoryOnly"] != true {
		t.Fatalf("the Sectors record of Bastion: status %d, want 200 with directoryOnly true, so that the marshal's password session does not enter it: %s", status, body)
	}

	// A person of Anvil's alone, with no address: the conditions of the marshal's grants
	// on the row hold, so the row offers him every act on them.
	hand := s.seedCrewAccount(ctx, t, "anvil-hand", nil).String()
	if status, body := governor.do(ctx, http.MethodPost, crewMethod("assign-crew-role"), jsonBody(t, map[string]any{"userId": hand, "role": "Cadet", "domain": anvil})); status != http.StatusNoContent {
		t.Fatalf("giving the person the role Cadet at Anvil as the governor: status %d, want 204: %s", status, body)
	}
	own := crewUserID(ctx, t, governor, "marshal").String()

	status, body := marshal.do(ctx, http.MethodGet, sectorPath(anvil, sectorPeoplePath+"?limit=200&capabilities=Execute"), nil)
	if status != http.StatusOK {
		t.Fatalf("Anvil's people with capabilities=Execute as the marshal: status %d, want 200: %s", status, body)
	}
	rows := rowsByID(t, decodeRows(t, body), "id")
	if rows[own] == nil || rows[hand] == nil {
		t.Fatalf("Anvil's people as the marshal hold the marshal's row %t and the person's %t, want both", rows[own] != nil, rows[hand] != nil)
	}
	ownOffers := executeOffered(t, rows[own])
	handOffers := executeOffered(t, rows[hand])
	for _, method := range []string{"DeleteCrewSectorUser", "DisableCrewSectorUser", "RemoveCrewSectorUser"} {
		if slices.Contains(ownOffers, method) {
			t.Errorf("the marshal's own row among Anvil's people offers %s: %v", method, ownOffers)
		}
		if !slices.Contains(handOffers, method) {
			t.Errorf("the row of a person of Anvil's alone offers %v, want %s among them", handOffers, method)
		}
	}
}
