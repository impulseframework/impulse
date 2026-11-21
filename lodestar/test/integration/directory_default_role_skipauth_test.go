//go:build skipAuth

package integration

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
)

// TestDirectoryDefaultRole_openedWithARoleTheCallerMayGive proves how a sector's marshal
// says what the sector's directory gives a person at their first sign-in through it:
// with two methods of the sector's own, never by writing the record.
//
// OpenSectorToDirectory names the role, and from then on everyone the directory vouches
// for enters the sector with it, people nobody named. So the method warns before it
// runs, in the server's words, and the role is held to what giving it by hand needs: it
// is a role of the sector, and one the caller could give there directly. A marshal
// gives every role of the sector but their own: the grant on the method that gives a
// role says so (new.role != 'SectorMarshal'), and the same grant answers here, so a
// marshal opens the sector with Pilot and never with SectorMarshal. Without the rule a
// marshal would make marshals of everyone from a directory, which the grant refuses
// them one person at a time. MakeSectorInviteOnly clears the role, which is plain. The
// record shows the role and never writes it: a patch that carries it is refused, and so
// is a create, whoever sends it. A sector with no directory has nobody to open to.
//
// The fleet's directory is Cinder's, so the marshal, Anvil's, is given the marshal's
// role at Cinder first.
//
// Demonstrates: tenancy.default-seat.
func TestDirectoryDefaultRole_openedWithARoleTheCallerMayGive(t *testing.T) {
	t.Parallel()

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
	// Her second session comes through the fleet's directory while it is Cinder's, which
	// admits it: her password confirms the sign-in, which links the identity to her
	// account. It is the session that sets a directory, further down.
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

	rows := directories(ctx, t, marshal, cinder)
	if len(rows) != 1 {
		t.Fatalf("Cinder's directory = %+v, want the seeded fleet directory alone", rows)
	}
	// The row as a batch names it: its path under the API's prefix.
	row := "/sectors/" + cinder + "/sector-directories/" + rows[0].ID
	open := func(b *browser, role string) (int, []byte) {
		return b.do(ctx, http.MethodPost, sectorPath(cinder, "open-sector-to-directory"), jsonBody(t, map[string]string{"role": role}))
	}
	inviteOnly := func(b *browser) (int, []byte) {
		return b.do(ctx, http.MethodPost, sectorPath(cinder, "make-sector-invite-only"), []byte(`{}`))
	}
	// warns asks the method that opens the sector for its warning, with the role as
	// picked so far: what the call would do, with nothing saved.
	warns := func(b *browser, role string) (int, []byte) {
		res := b.doWith(ctx, http.MethodPost, sectorPath(cinder, "open-sector-to-directory"), jsonBody(t, map[string]string{"role": role}), map[string]string{"X-Warning": "true"})
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}

		return res.StatusCode, body
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
	if got := seat(); got != "SectorCrew" {
		t.Fatalf("Cinder's default role as seeded = %q, want SectorCrew", got)
	}

	// The warning is the server's, for the role as picked so far, and saves nothing.
	// While no role is picked it says one is to be chosen.
	const who = "Anyone who signs in through example will be able to enter this sector without an invitation"
	for role, want := range map[string]string{"Pilot": who + " and gets the role Pilot.", "": who + ". Choose the role each of them gets."} {
		status, body := warns(marshal, role)
		var said struct {
			Consequence string `json:"consequence"`
			Confirm     string `json:"confirm"`
		}
		if err := json.Unmarshal(body, &said); status != http.StatusOK || err != nil {
			t.Fatalf("asking what opening Cinder with %q would do: status %d, %v: %s", role, status, err, body)
		}
		if said.Consequence != want || said.Confirm != "Open to people who sign in through example" {
			t.Errorf("the warning of opening Cinder with %q = %q under the button %q, want %q under what the button does", role, said.Consequence, said.Confirm, want)
		}
	}
	if got := seat(); got != "SectorCrew" {
		t.Fatalf("Cinder's default role after the warnings = %q, want SectorCrew still: asking writes nothing", got)
	}

	// A role the marshal may give is named.
	if status, body := open(marshal, "Pilot"); status != http.StatusOK {
		t.Fatalf("opening Cinder to its directory with Pilot: status %d, want 200: %s", status, body)
	}
	if got := seat(); got != "Pilot" {
		t.Errorf("Cinder's default role once opened with Pilot = %q, want Pilot", got)
	}
	// The marshal's own role is refused, on the call and on its warning, in a sentence
	// that says why, and the row keeps what it held.
	const mayNotGive = "You may not give this role in this sector, so you cannot make it the role everyone from its directory gets."
	if status, body := open(marshal, "SectorMarshal"); status != http.StatusForbidden || !strings.Contains(string(body), mayNotGive) {
		t.Errorf("opening Cinder with SectorMarshal as a marshal: status %d, want 403 saying %q: %s", status, mayNotGive, body)
	}
	if status, body := warns(marshal, "SectorMarshal"); status != http.StatusForbidden || !strings.Contains(string(body), mayNotGive) {
		t.Errorf("asking what opening Cinder with SectorMarshal would do, as a marshal: status %d, want 403 saying %q: %s", status, mayNotGive, body)
	}
	// The role is picked from the roles the caller may give in the sector, and no other
	// (CrewSectorDirectoryRoles): the marshal's list leaves her own role out, as the
	// method refuses it. The governor's list is the same. A request made under a sector
	// is judged by the grant of the sector's method that gives a role, which the governor
	// holds as the marshal of every sector, and that grant gives nobody the marshal's
	// role: a marshal is made through the whole application's method. So the list leaves
	// the role out for the governor too, and the method refuses it from the governor as
	// it does from the marshal. The role file writes no condition on the list: the grant
	// of the method that gives a role is what the list reads. Each role the list offers
	// the marshal is one the method takes from her.
	offered := func(b *browser) []string {
		t.Helper()
		status, body := b.do(ctx, http.MethodGet, sectorPath(cinder, "crew-sector-directory-roles"), nil)
		if status != http.StatusOK {
			t.Fatalf("listing the roles Cinder may be opened with: status %d: %s", status, body)
		}
		var rows []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if err := json.Unmarshal(body, &rows); err != nil {
			t.Fatalf("the roles Cinder may be opened with: %v: %s", err, body)
		}
		names := make([]string, 0, len(rows))
		for _, row := range rows {
			if row.Description == "" {
				t.Errorf("the role %s is listed with no description, want what the role is for beside its name", row.Name)
			}
			names = append(names, row.Name)
		}

		return names
	}
	marshalRoles, governorRoles := offered(marshal), offered(governor)
	if slices.Contains(marshalRoles, "SectorMarshal") || !slices.Contains(marshalRoles, "Pilot") || !slices.Contains(marshalRoles, "SectorCrew") {
		t.Errorf("the roles the marshal is offered = %v, want the sector's roles she may give: Pilot and SectorCrew among them, and never SectorMarshal", marshalRoles)
	}
	if !slices.Equal(governorRoles, marshalRoles) {
		t.Errorf("the roles the governor is offered = %v, want the marshal's %v: under a sector both are judged by the grant of the sector's method that gives a role", governorRoles, marshalRoles)
	}
	if status, body := open(governor, "SectorMarshal"); status != http.StatusForbidden || !strings.Contains(string(body), mayNotGive) {
		t.Errorf("opening Cinder with SectorMarshal as the governor: status %d, want 403 saying %q, since the list leaves the role out and offers what the method takes: %s", status, mayNotGive, body)
	}
	for _, role := range marshalRoles {
		dry := marshal.doWith(ctx, http.MethodPost, sectorPath(cinder, "open-sector-to-directory"), jsonBody(t, map[string]string{"role": role}), map[string]string{"X-Dry-Run": "true"})
		if err := dry.Body.Close(); err != nil {
			t.Fatal(err)
		}
		if dry.StatusCode != http.StatusOK {
			t.Errorf("a dry run of opening Cinder with %s, a role the list offers the marshal: status %d, want 200: the list offers what the method takes", role, dry.StatusCode)
		}
	}
	// A role that is not the sector's is refused, and so is a call that names none.
	if status, body := open(marshal, "Headquarters"); status != http.StatusBadRequest || !strings.Contains(string(body), "is not a role of this sector") {
		t.Errorf("opening Cinder with a role held for the whole application: status %d, want 400 saying it is no role of the sector: %s", status, body)
	}
	if status, body := open(marshal, ""); status != http.StatusBadRequest || !strings.Contains(string(body), "Choose the role") {
		t.Errorf("opening Cinder with no role: status %d, want 400 saying to choose one: %s", status, body)
	}
	if got := seat(); got != "Pilot" {
		t.Errorf("Cinder's default role after the refusals = %q, want Pilot", got)
	}
	// A dry run writes nothing.
	dry := marshal.doWith(ctx, http.MethodPost, sectorPath(cinder, "open-sector-to-directory"), jsonBody(t, map[string]string{"role": "SectorCrew"}), map[string]string{"X-Dry-Run": "true"})
	if err := dry.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if dry.StatusCode != http.StatusOK || seat() != "Pilot" {
		t.Errorf("a dry run of opening Cinder with SectorCrew: status %d and the default role %q, want 200 and Pilot still", dry.StatusCode, seat())
	}
	// Either header takes the value true or is left off the request: a request that says
	// anything else is refused and writes nothing, on the method that opens the sector
	// and on the one that makes it invite only.
	for _, header := range []string{"X-Dry-Run", "X-Warning"} {
		for route, body := range map[string][]byte{
			"open-sector-to-directory": jsonBody(t, map[string]string{"role": "SectorCrew"}),
			"make-sector-invite-only":  []byte(`{}`),
		} {
			refused := marshal.doWith(ctx, http.MethodPost, sectorPath(cinder, route), body, map[string]string{header: "1"})
			said, err := io.ReadAll(refused.Body)
			if err != nil {
				t.Fatal(err)
			}
			if err := refused.Body.Close(); err != nil {
				t.Fatal(err)
			}
			want := "the " + header + ` header takes the value true, or is left off the request: \"1\" is not a value it takes, so the call was not run`
			if refused.StatusCode != http.StatusBadRequest || !strings.Contains(string(said), want) || seat() != "Pilot" {
				t.Errorf("%s with %s: 1: status %d %s and the default role %q, want 400 saying %s and Pilot still", route, header, refused.StatusCode, said, seat(), want)
			}
		}
	}

	// The record shows the role and never writes it: a patch that carries it is refused,
	// to the marshal and to the governor, a super administrator who holds every grant.
	for name, b := range map[string]*browser{"the marshal": marshal, "the governor": governor} {
		patch := []byte(`[{"op":"patch","path":"` + row + `","value":{"defaultSeat":"SectorMarshal"}}]`)
		if status, body := b.do(ctx, http.MethodPatch, consoleAPI+"/resources", patch); status != http.StatusBadRequest && status != http.StatusForbidden {
			t.Errorf("patching the directory's default role as %s: status %d, want a refusal: the record does not take the role: %s", name, status, body)
		}
	}
	if got := seat(); got != "Pilot" {
		t.Fatalf("Cinder's default role after the refused patches = %q, want Pilot: the record's update writes no role", got)
	}

	// Making the sector invite only clears the role, and is plain: it warns of nothing,
	// so a request for a warning is refused and clears nothing; and a sector that is
	// invite only already is left as it is.
	noWarning := marshal.doWith(ctx, http.MethodPost, sectorPath(cinder, "make-sector-invite-only"), []byte(`{}`), map[string]string{"X-Warning": "true"})
	if err := noWarning.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if noWarning.StatusCode != http.StatusBadRequest || seat() != "Pilot" {
		t.Errorf("asking the method that makes Cinder invite only for a warning: status %d and the default role %q, want 400 and Pilot still", noWarning.StatusCode, seat())
	}
	for range 2 {
		if status, body := inviteOnly(marshal); status != http.StatusOK {
			t.Fatalf("making Cinder invite only: status %d, want 200: %s", status, body)
		}
		if got := seat(); got != "" {
			t.Errorf("Cinder's default role once it is invite only = %q, want none", got)
		}
	}

	// A sector with no directory has nobody to open to: both methods say so, and the
	// warning does too. A directory is set without a default role, since the create
	// takes nothing from the request: one that names a role is refused.
	remove := `[{"op":"remove","path":"` + row + `"}]`
	if status, body := marshal.do(ctx, http.MethodPatch, consoleAPI+"/resources", []byte(remove)); status != http.StatusOK {
		t.Fatalf("removing Cinder's directory: status %d: %s", status, body)
	}
	const nobody = "This sector has no directory, so there is nobody to open it to."
	for name, call := range map[string]func() (int, []byte){
		"opening it":             func() (int, []byte) { return open(governor, "Pilot") },
		"asking for the warning": func() (int, []byte) { return warns(governor, "Pilot") },
		"making it invite only":  func() (int, []byte) { return inviteOnly(governor) },
	} {
		if status, body := call(); status != http.StatusConflict || !strings.Contains(string(body), nobody) {
			t.Errorf("%s, for a sector with no directory: status %d, want 409 saying %q: %s", name, status, nobody, body)
		}
	}
	set := func(b *browser, value string) (int, []byte) {
		return b.do(ctx, http.MethodPatch, consoleAPI+"/resources", []byte(`[{"op":"add","path":"/sectors/`+cinder+`/sector-directories","value":{`+value+`}}]`))
	}
	if status, body := set(marshal, ``); status != http.StatusConflict || !strings.Contains(string(body), "You are signed in with a password. A directory is set by signing in through it") {
		t.Errorf("setting Cinder's directory from a password session: status %d, want 409 saying how a directory is set: %s", status, body)
	}
	if status, body := set(marshal, `"defaultSeat":"SectorCrew"`); status != http.StatusBadRequest && status != http.StatusForbidden {
		t.Errorf("setting Cinder's directory with a default role from a password session: status %d, want a refusal: %s", status, body)
	}
	if rows := directories(ctx, t, governor, cinder); len(rows) != 0 {
		t.Fatalf("Cinder's directory after the refused calls = %+v, want none", rows)
	}

	// Signed in through the fleet's directory, the marshal is offered the action. The
	// governor now makes her a super administrator, since a sector with no directory
	// admits no directory session and a super administrator alone enters a sector
	// whatever its sign-in settings say. Giving the role ends nothing, so the two
	// sessions she holds are a super administrator's from then on: the one by her
	// password, and the one through the fleet's directory (through). A
	// create that names a default role is refused her whatever the role, and writes
	// nothing; the create that names nothing makes the fleet's Workspace Cinder's, with
	// no default role.
	superAdministrator := jsonBody(t, map[string]any{"userId": crewAccountID(ctx, t, governor, "marshal"), "role": "SuperAdministrator"})
	if status, body := governor.do(ctx, http.MethodPost, crewMethod("assign-crew-role"), superAdministrator); status != http.StatusNoContent {
		t.Fatalf("making the marshal a super administrator: status %d: %s", status, body)
	}
	for _, role := range []string{"SectorMarshal", "SectorCrew"} {
		if status, body := set(through, `"defaultSeat":"`+role+`"`); status != http.StatusBadRequest && status != http.StatusForbidden {
			t.Errorf("setting Cinder's directory with %s as its default role: status %d, want a refusal: %s", role, status, body)
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
	// The new row is opened under the same rule, by the grants she holds as a marshal:
	// the marshal's own role is refused, and a role she may give is named.
	if status, body := open(marshal, "SectorMarshal"); status != http.StatusForbidden {
		t.Errorf("opening the directory just set with SectorMarshal: status %d, want 403: %s", status, body)
	}
	if got := seat(); got != "" {
		t.Errorf("Cinder's default role after the refusal = %q, want none", got)
	}
	if status, body := open(marshal, "SectorCrew"); status != http.StatusOK || seat() != "SectorCrew" {
		t.Errorf("opening the directory just set with SectorCrew: status %d and the default role %q, want 200 and SectorCrew: %s", status, seat(), body)
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
