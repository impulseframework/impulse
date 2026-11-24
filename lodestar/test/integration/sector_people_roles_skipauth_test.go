//go:build skipAuth

package integration

import (
	"fmt"
	"net/http"
	"slices"
	"testing"
)

// TestSectorPeople_rolesReadAsItsPageReadsThem proves that a sector's list of its people
// answers, with what each person holds in the sector, the read its page makes, on the
// served stack: the list with capabilities=Execute, by Anvil's marshal signed in with a
// password and the code mailed to her, whose session does not enter Bastion, so the
// statement holds the query of the sectors a person administers beside the list's own.
// The list answers, and the armorer's row says the roles Anvil's one list of the roles
// held answers the same caller for the armorer held at Anvil itself, each once, in order.
func TestSectorPeople_rolesReadAsItsPageReadsThem(t *testing.T) {
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
	listed, ok := armorer["roles"].([]any)
	if !ok {
		t.Fatalf("the armorer's row carries no list of roles: %v", armorer)
	}
	got := make([]string, 0, len(listed))
	for _, role := range listed {
		got = append(got, fmt.Sprint(role))
	}

	status, body = marshal.do(ctx, http.MethodGet, sectorPath(anvil, sectorHeldRolesPath+"?limit=200&filter=userId:eq:"+cell[string](t, armorer, "id")), nil)
	assertStatus(t, status, http.StatusOK, body)
	var want []string
	for _, row := range decodeRows(t, body) {
		if name := cell[string](t, row, "role"); !cell[bool](t, row, "everyDomain") && !slices.Contains(want, name) {
			want = append(want, name)
		}
	}
	slices.Sort(want)
	if len(want) == 0 || !slices.Equal(got, want) {
		t.Errorf("the armorer's row of Anvil's people says %q, want %q, the roles Anvil's list of the roles held answers for the armorer held at Anvil itself", got, want)
	}
}
