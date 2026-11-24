//go:build skipAuth

package integration

import (
	"net/http"
	"slices"
	"testing"
)

// TestSectorPeople_readAsItsPageReadsThem proves that a sector's list of its people answers
// the read its page makes, on the served stack: the list with capabilities=Execute, by
// Anvil's marshal signed in with a password and the code mailed to her. Bastion is
// directory only and her sign-in does not enter it, so each row's offer of the methods
// that change how a person signs in asks which sectors her session enters, and the
// statement holds the query of the sectors a person administers beside the list's own.
// The list answers, and the armorer's row, a person Anvil holds who also holds a role
// for the whole fleet, offers her the removal from the sector and not the delete.
func TestSectorPeople_readAsItsPageReadsThem(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	marshal := newBrowser(t, s, consoleAPI)
	marshal.signIn(ctx, "marshal")

	status, body := marshal.do(ctx, http.MethodGet, sectorPath(anvil, sectorPeoplePath+"?limit=200&capabilities=Execute"), nil)
	assertStatus(t, status, http.StatusOK, body)
	var armorer map[string]any
	for _, row := range decodeRows(t, body) {
		if row["username"] == "armorer" {
			armorer = row
		}
	}
	if armorer == nil {
		t.Fatalf("Anvil's people do not carry the armorer: %s", body)
	}
	offered := executeOffered(t, armorer)
	if !slices.Contains(offered, "RemoveCrewSectorUser") || slices.Contains(offered, "DeleteCrewSectorUser") {
		t.Errorf("the armorer's row offers Anvil's marshal %v, want RemoveCrewSectorUser and no DeleteCrewSectorUser", offered)
	}
}
