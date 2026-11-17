//go:build skipAuth

package integration

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
)

// TestDirectoryDefaultRole_aMarshalNamesNoMarshal proves the limit the crew's role file
// puts on the role a sector's directory gives a person at their first sign-in through
// it.
//
// A marshal gives every role of the sector but their own: the grants on the methods that
// give a role say so (new.role != 'SectorMarshal'). A directory's default role gives a
// role too, to everyone the directory vouches for, with nobody signed in, so the
// marshal's Update grant on the directories record carries the same limit over the role
// the request writes (new.defaultSeat): a default role is named, changed and cleared,
// but never with the marshal's own role. A directory is set without one, since the
// marshal's Create grant carries no field (the server sets the directory from the
// session), so every default role is named by an update, under the limit. Without it a
// marshal would make marshals of everyone from a directory, which the grants on the
// methods refuse them one person at a time.
//
// The fleet's directory is Cinder's, so the marshal, Anvil's, is given the marshal's
// role at Cinder first.
//
// Demonstrates: tenancy.default-seat.
func TestDirectoryDefaultRole_aMarshalNamesNoMarshal(t *testing.T) {
	t.Parallel()

	// The conditional grant this test proves, pinned to the role file.
	provesGrant(t, crew.Roles(), "SectorMarshal", "Update", "SectorDirectories", "new.defaultSeat IS NULL OR new.defaultSeat != 'SectorMarshal'")

	ctx := t.Context()
	s := newServed(ctx, t)
	governor := newBrowser(t, s, consoleAPI)
	governor.signIn(ctx, "governor")
	atCinder := jsonBody(t, map[string]any{"userId": crewAccountID(ctx, t, governor, "marshal"), "role": "SectorMarshal", "domain": cinder})
	if status, body := governor.do(ctx, http.MethodPost, crewMethod("assign-crew-role"), atCinder); status != http.StatusNoContent {
		t.Fatalf("making the marshal Cinder's marshal too: status %d: %s", status, body)
	}
	marshal := newBrowser(t, s, consoleAPI)
	marshal.signIn(ctx, "marshal")

	rows := directories(ctx, t, marshal, cinder)
	if len(rows) != 1 {
		t.Fatalf("Cinder's directory = %+v, want the seeded fleet directory alone", rows)
	}
	// The row as a batch names it: its path under the API's prefix.
	row := "/sectors/" + cinder + "/sector-directories/" + rows[0].ID
	patch := func(value string) (int, []byte) {
		return marshal.do(ctx, http.MethodPatch, consoleAPI+"/resources", []byte(`[{"op":"patch","path":"`+row+`","value":{"defaultSeat":`+value+`}}]`))
	}
	seat := func() string {
		t.Helper()
		rows := directories(ctx, t, marshal, cinder)
		if len(rows) != 1 {
			t.Fatalf("Cinder's directory = %+v, want one", rows)
		}
		if rows[0].DefaultSeat == nil {
			return ""
		}

		return *rows[0].DefaultSeat
	}

	// A role the marshal may give is named.
	if status, body := patch(`"Pilot"`); status != http.StatusOK {
		t.Fatalf("naming Pilot as Cinder's default role: status %d, want 200: %s", status, body)
	}
	// The marshal's own role is refused, and the row keeps what it held.
	if status, body := patch(`"SectorMarshal"`); status != http.StatusForbidden {
		t.Errorf("naming SectorMarshal as Cinder's default role: status %d, want 403: %s", status, body)
	}
	if got := seat(); got != "Pilot" {
		t.Errorf("Cinder's default role after the refusal = %q, want Pilot", got)
	}
	// Clearing it writes no role: the sector admits by invitation only again.
	if status, body := patch(`null`); status != http.StatusOK {
		t.Fatalf("clearing Cinder's default role: status %d, want 200: %s", status, body)
	}
	if got := seat(); got != "" {
		t.Errorf("Cinder's default role once cleared = %q, want none", got)
	}

	// A directory is set without a default role: the marshal's Create grant carries no
	// field, so a create that names one is refused whatever role it names, and the way
	// to a default role is the update above. The create that names nothing is the
	// action of a person signed in through a directory: the marshal came by password,
	// so it is refused with how a directory is set, and Cinder is left with none.
	remove := `[{"op":"remove","path":"` + row + `"}]`
	if status, body := marshal.do(ctx, http.MethodPatch, consoleAPI+"/resources", []byte(remove)); status != http.StatusOK {
		t.Fatalf("removing Cinder's directory: status %d: %s", status, body)
	}
	set := func(b *browser, value string) (int, []byte) {
		return b.do(ctx, http.MethodPatch, consoleAPI+"/resources", []byte(`[{"op":"add","path":"/sectors/`+cinder+`/sector-directories","value":{`+value+`}}]`))
	}
	if status, body := set(marshal, ``); status != http.StatusConflict || !strings.Contains(string(body), "You are signed in with a password. A directory is set by signing in through it") {
		t.Errorf("setting Cinder's directory from a password session: status %d, want 409 saying how a directory is set: %s", status, body)
	}
	// Her grants answer before that rule does: a create that names a default role is
	// refused by the grant, which carries no field, however she is signed in.
	if status, body := set(marshal, `"defaultSeat":"SectorCrew"`); status != http.StatusForbidden {
		t.Errorf("setting Cinder's directory with a default role from a password session: status %d, want 403: %s", status, body)
	}
	if rows := directories(ctx, t, marshal, cinder); len(rows) != 0 {
		t.Fatalf("Cinder's directory after the refused call = %+v, want none", rows)
	}

	// Signed in through the fleet's directory, the marshal is offered the action. Her
	// password confirms the sign-in, which links the identity to her account, and the
	// governor makes her one of the fleet's staff (a role of the whole application),
	// since a sector with no directory admits no directory session. A create that names
	// a default role is refused her whatever the role, and writes nothing; the create
	// that names nothing makes the fleet's Workspace Cinder's, with no default role.
	staff := jsonBody(t, map[string]any{"userId": crewAccountID(ctx, t, governor, "marshal"), "role": "CrewCommon"})
	if status, body := governor.do(ctx, http.MethodPost, crewMethod("assign-crew-role"), staff); status != http.StatusNoContent {
		t.Fatalf("making the marshal one of the fleet's staff: status %d: %s", status, body)
	}
	through := newBrowser(t, s, consoleAPI)
	if status, body := through.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET session before the marshal's Google login: status %d: %s", status, body)
	}
	held := consoleLogin + "?pending=confirmation&returnUrl=" + url.QueryEscape(consolePage)
	if status, location := through.loginConsoleGoogle(ctx, "marshal"); status != http.StatusFound || location != held {
		t.Fatalf("the marshal's Google login: status %d to %q, want %d to %q", status, location, http.StatusFound, held)
	}
	if status, body := through.do(ctx, http.MethodPost, consoleAPI+"/user/pending/confirm", jsonBody(t, map[string]string{"password": personaPassword})); status != http.StatusOK {
		t.Fatalf("confirming the sign-in with the marshal's password: status %d: %s", status, body)
	}
	for _, role := range []string{"SectorMarshal", "SectorCrew"} {
		if status, body := set(through, `"defaultSeat":"`+role+`"`); status != http.StatusForbidden {
			t.Errorf("setting Cinder's directory with %s as its default role: status %d, want 403: %s", role, status, body)
		}
	}
	if rows := directories(ctx, t, marshal, cinder); len(rows) != 0 {
		t.Fatalf("Cinder's directory after the refused calls = %+v, want none", rows)
	}
	if status, body := set(through, ``); status != http.StatusOK {
		t.Fatalf("making the fleet's Workspace Cinder's directory from a sign-in through it: status %d, want 200: %s", status, body)
	}
	rows = directories(ctx, t, marshal, cinder)
	if len(rows) != 1 || rows[0].Connection != "example" || rows[0].DefaultSeat != nil {
		t.Fatalf("Cinder's directory = %+v, want the fleet's Workspace with no default role", rows)
	}
	// The new row is held to the same limit: the marshal's own role is refused.
	row = "/sectors/" + cinder + "/sector-directories/" + rows[0].ID
	if status, body := patch(`"SectorMarshal"`); status != http.StatusForbidden {
		t.Errorf("naming SectorMarshal as the default role of the directory just set: status %d, want 403: %s", status, body)
	}
	if got := seat(); got != "" {
		t.Errorf("Cinder's default role after the refusal = %q, want none", got)
	}
}

// TestDirectoryDefaultRole_givenAtTheFirstSignInThroughTheDirectory proves when a
// sector's default role is given to a person whose account stood before: at their first
// sign-in through the sector's directory, the one that links the account to it, and
// once for that link.
//
// The cadet is a password persona, seated in Anvil and nowhere else. The fleet's
// directory is Cinder's, whose row names SectorCrew. Her first sign-in through the
// fleet's directory is held for her password, which links the identity to her account,
// and the link gives her Cinder's default role. Her second sign-in finds the link and
// gives nothing. The governor removes the role, and signing in again does not give it
// back: an administrator who wants one person out removes the role and leaves the
// link. The governor then deletes the link, and her next sign-in through the directory
// is a first one again: it links the account and, since she holds no role at Cinder,
// gives the default role.
//
// Demonstrates: tenancy.default-seat.
func TestDirectoryDefaultRole_givenAtTheFirstSignInThroughTheDirectory(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	const cadet = "cadet"
	governor := newBrowser(t, s, consoleAPI)
	governor.signIn(ctx, "governor")
	cadetID := crewAccountID(ctx, t, governor, cadet)
	atCinder := func() []string {
		t.Helper()

		return rolesHeldInSector(ctx, t, governor, cinder, cadet)
	}
	// linked signs the cadet in through the fleet's directory on a fresh browser, with
	// the password that confirms the identity when the sign-in is held for it (her
	// first sign-in through the directory, and the first after her link is deleted).
	linked := func(confirm bool) {
		t.Helper()
		b := newBrowser(t, s, consoleAPI)
		if status, body := b.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
			t.Fatalf("GET session before the cadet's login: status %d: %s", status, body)
		}
		status, location := b.loginConsoleGoogle(ctx, cadet)
		want := consolePage
		if confirm {
			want = consoleLogin + "?pending=confirmation&returnUrl=" + url.QueryEscape(consolePage)
		}
		if status != http.StatusFound || location != want {
			t.Fatalf("the cadet's Google login: status %d to %q, want %d to %q", status, location, http.StatusFound, want)
		}
		if confirm {
			if status, body := b.do(ctx, http.MethodPost, consoleAPI+"/user/pending/confirm", jsonBody(t, map[string]string{"password": personaPassword})); status != http.StatusOK {
				t.Fatalf("confirming the sign-in with the cadet's password: status %d: %s", status, body)
			}
		}
		if sess := b.session(ctx); sess["authenticated"] != true || sess["username"] != cadet {
			t.Fatalf("the cadet's session after the directory signed her in = %v, want authenticated as %s", sess, cadet)
		}
	}

	if held := atCinder(); len(held) != 0 {
		t.Fatalf("the cadet's roles at Cinder before her first sign-in through its directory = %v, want none", held)
	}
	// The first sign-in through the directory links the account and gives the role.
	linked(true)
	if held := atCinder(); !slices.Equal(held, []string{"SectorCrew"}) {
		t.Fatalf("the cadet's roles at Cinder after her first sign-in through its directory = %v, want the default role", held)
	}
	// The second finds the link and gives nothing more.
	linked(false)
	if held := atCinder(); !slices.Equal(held, []string{"SectorCrew"}) {
		t.Errorf("the cadet's roles at Cinder after her second sign-in = %v, want the one default role and nothing more", held)
	}

	// The role is removed, and signing in does not give it back.
	take := jsonBody(t, map[string]any{"userId": cadetID, "role": "SectorCrew", "domain": cinder})
	if status, body := governor.do(ctx, http.MethodPost, crewMethod("remove-crew-role"), take); status != http.StatusNoContent {
		t.Fatalf("removing the cadet's role at Cinder: status %d: %s", status, body)
	}
	linked(false)
	if held := atCinder(); len(held) != 0 {
		t.Errorf("the cadet's roles at Cinder after signing in with the role removed = %v, want none: a removed role is not given back", held)
	}

	// The link is deleted: the next sign-in through the directory links the account
	// again and gives the default role, since she holds no role at Cinder.
	var links []string
	for _, row := range crewList(ctx, t, governor, "crew-session-identities") {
		if row["userId"] == cadetID {
			links = append(links, cell[string](t, row, "id"))
		}
	}
	if len(links) != 1 {
		t.Fatalf("the cadet's identity links = %v, want the one her first sign-in through the directory made", links)
	}
	if status, body := governor.do(ctx, http.MethodPost, crewMethod("unlink-crew-session-identity"), jsonBody(t, map[string]string{"identityId": links[0]})); status != http.StatusNoContent {
		t.Fatalf("deleting the cadet's link: status %d: %s", status, body)
	}
	linked(true)
	if held := atCinder(); !slices.Equal(held, []string{"SectorCrew"}) {
		t.Errorf("the cadet's roles at Cinder after the sign-in that linked her account again = %v, want the default role", held)
	}
}
