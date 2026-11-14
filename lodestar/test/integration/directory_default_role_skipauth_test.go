//go:build skipAuth

package integration

import (
	"net/http"
	"testing"

	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
)

// TestDirectoryDefaultRole_aMarshalNamesNoMarshal proves the limit the crew's role file
// puts on the role a sector's directory gives a stranger at their first sign-in.
//
// A marshal gives every role of the sector but their own: the grants on the methods that
// give a role say so (new.role != 'SectorMarshal'). A directory's default seat gives a
// role too, to everyone the directory vouches for, with nobody signed in, so the
// marshal's Update grant on the directories record carries the same limit over the seat
// the request writes (new.defaultSeat): a seat is named, changed and cleared, but never
// with the marshal's own role. A directory is declared without a seat, since the
// marshal's Create grant carries the method and the connection alone, so every seat is
// named by an update, under the limit. Without it a marshal would make marshals of every
// stranger from a directory, which the grants on the methods refuse them one person at
// a time.
//
// Demonstrates: tenancy.default-seat.
func TestDirectoryDefaultRole_aMarshalNamesNoMarshal(t *testing.T) {
	t.Parallel()

	// The conditional grant this test proves, pinned to the role file.
	provesGrant(t, crew.Roles(), "SectorMarshal", "Update", "SectorDirectories", "new.defaultSeat IS NULL OR new.defaultSeat != 'SectorMarshal'")

	ctx := t.Context()
	s := newServed(ctx, t)
	marshal := newBrowser(t, s, consoleAPI)
	marshal.signIn(ctx, "marshal")

	rows := directories(ctx, t, marshal, anvil)
	if len(rows) != 1 {
		t.Fatalf("Anvil's directories = %+v, want the seeded fleet directory alone", rows)
	}
	// The row as a batch names it: its path under the API's prefix.
	row := "/sectors/" + anvil + "/sector-directories/" + rows[0].ID
	patch := func(value string) (int, []byte) {
		return marshal.do(ctx, http.MethodPatch, consoleAPI+"/resources", []byte(`[{"op":"patch","path":"`+row+`","value":{"defaultSeat":`+value+`}}]`))
	}
	seat := func() string {
		t.Helper()
		rows := directories(ctx, t, marshal, anvil)
		if len(rows) != 1 {
			t.Fatalf("Anvil's directories = %+v, want one", rows)
		}
		if rows[0].DefaultSeat == nil {
			return ""
		}

		return *rows[0].DefaultSeat
	}

	// A role the marshal may give is named.
	if status, body := patch(`"SectorCrew"`); status != http.StatusOK {
		t.Fatalf("naming SectorCrew as Anvil's default seat: status %d, want 200: %s", status, body)
	}
	// The marshal's own role is refused, and the row keeps what it held.
	if status, body := patch(`"SectorMarshal"`); status != http.StatusForbidden {
		t.Errorf("naming SectorMarshal as Anvil's default seat: status %d, want 403: %s", status, body)
	}
	if got := seat(); got != "SectorCrew" {
		t.Errorf("Anvil's default seat after the refusal = %q, want SectorCrew", got)
	}
	// Clearing the seat writes no role: the sector admits by invitation only again.
	if status, body := patch(`null`); status != http.StatusOK {
		t.Fatalf("clearing Anvil's default seat: status %d, want 200: %s", status, body)
	}
	if got := seat(); got != "" {
		t.Errorf("Anvil's default seat once cleared = %q, want none", got)
	}

	// A directory is declared without a seat: the marshal's Create grant does not carry
	// the field, so a declaration that names one is refused whatever role it names, and
	// the way to a seat is the update above.
	withdraw := `[{"op":"remove","path":"` + row + `"}]`
	if status, body := marshal.do(ctx, http.MethodPatch, consoleAPI+"/resources", []byte(withdraw)); status != http.StatusOK {
		t.Fatalf("withdrawing Anvil's directory: status %d: %s", status, body)
	}
	declare := func(seat string) (int, []byte) {
		return marshal.do(ctx, http.MethodPatch, consoleAPI+"/resources", []byte(`[{"op":"add","path":"/sectors/`+anvil+`/sector-directories","value":{"method":"google","connection":"example"`+seat+`}}]`))
	}
	for _, role := range []string{"SectorMarshal", "SectorCrew"} {
		if status, body := declare(`,"defaultSeat":"` + role + `"`); status != http.StatusForbidden {
			t.Errorf("declaring Anvil's directory with %s as its default seat: status %d, want 403: %s", role, status, body)
		}
	}
	if rows := directories(ctx, t, marshal, anvil); len(rows) != 0 {
		t.Errorf("Anvil's directories after the refused declarations = %+v, want none", rows)
	}
	if status, body := declare(``); status != http.StatusOK {
		t.Fatalf("declaring Anvil's directory with no default seat: status %d, want 200: %s", status, body)
	}
	if got := seat(); got != "" {
		t.Errorf("Anvil's default seat as declared = %q, want none", got)
	}
}
