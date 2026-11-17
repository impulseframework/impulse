//go:build skipAuth

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	cloudspanner "cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// TestTenantDeclaration_whoMayEnterASector proves the Sector record's declaration end to
// end on the served stack. A sector has one directory and a directory belongs to one
// sector: the seed makes the fleet's directory (google, example) Cinder's, whose row
// names SectorCrew as its default role, and makes Bastion admit its people through its
// directory only. The rigger, by password, holding SectorCrew in Anvil and Bastion and
// no global role, charts Anvil alone and Bastion answers her as a sector that does not
// exist; the bosun, whose only sector is Bastion, is refused at sign-in naming the
// rule; the navigator, through the fleet's directory on no manifest, is created at his
// first sign-in seated at Cinder.
//
// Nobody types a directory in. The governor, signed in with a password, is told by the
// session read that he came through no directory, and his call is refused with how a
// directory is set, as is a request that types a method and a connection. The
// navigator, a marshal signed in through the fleet's Workspace, is refused a second
// sector for it and refused setting it again where it is, each with what to do; once
// Cinder's is removed the Workspace is free, and he makes it Bastion's with a create
// that names no directory. Bastion is directory only, so its directory is not removed
// until headquarters opens it to passwords. The default role is the row's: with none
// named, a stranger from the fleet's directory is refused by invitation only and no
// account is created; with SectorCrew named on Bastion's row, the same stranger's next
// sign-in creates her seated there, and her directory session enters the sector a
// password does not reach. The roles a directory gave stand once it is removed. Every
// edit leaves a change event. The directory sign-in sets a process variable, so the
// test does not run in parallel with the other directory sign-ins (simulatedLogin).
//
// Demonstrates: tenancy.declaration, tenancy.directory-only, tenancy.default-seat.
func TestTenantDeclaration_whoMayEnterASector(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)

	governor := newBrowser(t, s, consoleAPI)
	governor.signIn(ctx, "governor")

	// The rigger: a password session, admitted since Anvil admits passwords; Bastion,
	// directory only, is not hers to enter.
	rigger := newBrowser(t, s, consoleAPI)
	rigger.signIn(ctx, "rigger")
	if got, want := rigger.domains(ctx), []string{anvil}; !slices.Equal(got, want) {
		t.Errorf("the rigger's chart = %v, want %v: Bastion admits its people through its directory only", got, want)
	}
	if status, body := rigger.do(ctx, http.MethodGet, sectorPath(bastion, "wings"), nil); status != http.StatusNotFound {
		t.Errorf("GET Bastion's wings as the rigger: status %d, want 404: %s", status, body)
	}
	if status, body := rigger.do(ctx, http.MethodGet, sectorPath(anvil, "wings"), nil); status != http.StatusOK {
		t.Errorf("GET Anvil's wings as the rigger: status %d, want 200: %s", status, body)
	}

	// The bosun: every sector of hers is directory only, so the password is refused.
	bosun := newBrowser(t, s, consoleAPI)
	if status, body := bosun.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET session before the bosun's login: status %d: %s", status, body)
	}
	status, body := bosun.login(ctx, "bosun", personaPassword)
	if status != http.StatusUnauthorized {
		t.Fatalf("the bosun's password sign-in: status %d, want 401: %s", status, body)
	}
	var refusal struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &refusal); err != nil || refusal.Code != string(sessioninfo.RefusedDirectoryOnly) {
		t.Errorf("the bosun's refusal code = %q (err %v), want %q: %s", refusal.Code, err, sessioninfo.RefusedDirectoryOnly, body)
	}

	// The seed: the fleet's directory is Cinder's alone, and Anvil and Bastion have none.
	cinderRows := directories(ctx, t, governor, cinder)
	if len(cinderRows) != 1 || cinderRows[0].Method != string(sessioninfo.MethodGoogle) || cinderRows[0].Connection != "example" || cinderRows[0].DefaultSeat == nil || *cinderRows[0].DefaultSeat != "SectorCrew" {
		t.Fatalf("Cinder's directory = %+v, want the seeded fleet directory naming SectorCrew as its default role", cinderRows)
	}
	for name, sector := range map[string]string{"Anvil": anvil, "Bastion": bastion} {
		if rows := directories(ctx, t, governor, sector); len(rows) != 0 {
			t.Fatalf("%s's directory = %+v, want none: a directory belongs to one sector", name, rows)
		}
	}

	// The navigator: through the fleet's directory, on no manifest. The crew's roles are
	// the application's, so his first sign-in creates him with the default role of the
	// sector whose directory he came through: Cinder's SectorCrew.
	navigator := newBrowser(t, s, consoleAPI)
	if status, body := navigator.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET session before the navigator's login: status %d: %s", status, body)
	}
	if status, location := navigator.loginConsoleGoogle(ctx, navigatorUser); status != http.StatusFound || location != consolePage {
		t.Fatalf("the navigator's Google login: status %d to %q, want %d to %q", status, location, http.StatusFound, consolePage)
	}
	if got, want := navigator.domains(ctx), []string{cinder}; !slices.Equal(got, want) {
		t.Fatalf("the navigator's chart after the first sign-in = %v, want %v: Cinder's row names SectorCrew as its default role", got, want)
	}
	if held := rolesHeldInSector(ctx, t, governor, cinder, navigatorUser); !slices.Equal(held, []string{"SectorCrew"}) {
		t.Fatalf("the navigator's roles at Cinder = %v, want SectorCrew alone, given by the directory's default role", held)
	}

	// The session read says which organization's directory a person is signed in
	// through, which is what the Sign-in page offers to make the sector's: the fleet's
	// Workspace for the navigator, none for the governor, who came by password.
	if got := sessionDirectory(t, navigator.session(ctx)); got == nil || got.Method != string(sessioninfo.MethodGoogle) || got.Connection != "example" {
		t.Fatalf("the navigator's session reports the directory %+v, want the fleet's Workspace", got)
	}
	if got := sessionDirectory(t, governor.session(ctx)); got != nil {
		t.Fatalf("the governor's password session reports the directory %+v, want none", got)
	}

	// A directory is never typed in. The governor holds the marshal's role in every
	// sector, and is signed in with a password: his call is refused with what to do
	// instead, and a request that names a method and a connection is refused as naming
	// fields the record does not take. Neither writes.
	makeDirectoryOf := func(sector string) []byte {
		return []byte(`[{"op":"add","path":"/sectors/` + sector + `/sector-directories","value":{}}]`)
	}
	if status, body := governor.do(ctx, http.MethodPatch, consoleAPI+"/resources", makeDirectoryOf(anvil)); status != http.StatusConflict || !strings.Contains(string(body), "You are signed in with a password. A directory is set by signing in through it") {
		t.Errorf("setting Anvil's directory from a password session: status %d, want 409 saying how a directory is set: %s", status, body)
	}
	typed := `[{"op":"add","path":"/sectors/` + anvil + `/sector-directories","value":{"method":"google","connection":"typed.example"}}]`
	if status, body := governor.do(ctx, http.MethodPatch, consoleAPI+"/resources", []byte(typed)); status != http.StatusBadRequest {
		t.Errorf("typing a directory in: status %d, want 400: %s", status, body)
	}
	if rows := directories(ctx, t, governor, anvil); len(rows) != 0 {
		t.Fatalf("Anvil's directory after the refused calls = %+v, want none", rows)
	}

	// The navigator is made a marshal in every sector and one of the fleet's own staff
	// (a role of the whole application): a sector with no directory admits no directory
	// session, so it is staff, who reach a sector whatever it declares, who set a
	// sector's first directory once the sector exists.
	navigatorID := crewAccountID(ctx, t, governor, navigatorUser)
	for _, role := range []map[string]any{
		{"userId": navigatorID, "role": "CrewCommon"},
		{"userId": navigatorID, "role": "SectorMarshal", "everyDomain": true},
	} {
		if status, body := governor.do(ctx, http.MethodPost, crewMethod("assign-crew-role"), jsonBody(t, role)); status != http.StatusNoContent {
			t.Fatalf("giving the navigator %v: status %d: %s", role, status, body)
		}
	}
	if got, want := navigator.domains(ctx), []string{anvil, bastion, cinder}; !slices.Equal(got, want) {
		t.Fatalf("the navigator's chart as staff = %v, want %v", got, want)
	}

	// A sector has one directory, and a directory belongs to one sector: the fleet's
	// Workspace is Cinder's, so setting it again there, and setting it on Anvil or on
	// Bastion, are each refused with the rule and what to do.
	if status, body := navigator.do(ctx, http.MethodPatch, consoleAPI+"/resources", makeDirectoryOf(cinder)); status != http.StatusConflict || !strings.Contains(string(body), "Google Workspace example is already this sector's directory.") {
		t.Errorf("setting Cinder's directory a second time: status %d, want 409 saying it is the sector's already: %s", status, body)
	}
	for name, sector := range map[string]string{"Anvil": anvil, "Bastion": bastion} {
		status, body := navigator.do(ctx, http.MethodPatch, consoleAPI+"/resources", makeDirectoryOf(sector))
		if want := "Google Workspace example already has a sector here, and a directory belongs to one sector. Ask that sector's administrator to invite you."; status != http.StatusConflict || !strings.Contains(string(body), want) {
			t.Errorf("setting Cinder's directory on %s: status %d, want 409 saying %q: %s", name, status, want, body)
		}
		if rows := directories(ctx, t, governor, sector); len(rows) != 0 {
			t.Errorf("%s's directory after the refusal = %+v, want none", name, rows)
		}
	}

	// Cinder's directory is removed: the role it gave the navigator stands, and the
	// Workspace is free for another sector. The navigator makes it Bastion's, and the
	// row carries the directory his session came through, which the request never named.
	removeCinders := `[{"op":"remove","path":"/sectors/` + cinder + `/sector-directories/` + cinderRows[0].ID + `"}]`
	if status, body := navigator.do(ctx, http.MethodPatch, consoleAPI+"/resources", []byte(removeCinders)); status != http.StatusOK {
		t.Fatalf("removing Cinder's directory: status %d: %s", status, body)
	}
	if held := rolesHeldInSector(ctx, t, governor, cinder, navigatorUser); !slices.Contains(held, "SectorCrew") {
		t.Errorf("the navigator's roles at Cinder once its directory is removed = %v, want SectorCrew to stand", held)
	}
	if status, body := navigator.do(ctx, http.MethodPatch, consoleAPI+"/resources", makeDirectoryOf(bastion)); status != http.StatusOK {
		t.Fatalf("making the fleet's Workspace Bastion's directory: status %d: %s", status, body)
	}
	bastionRows := directories(ctx, t, governor, bastion)
	if len(bastionRows) != 1 || bastionRows[0].Method != string(sessioninfo.MethodGoogle) || bastionRows[0].Connection != "example" || bastionRows[0].DefaultSeat != nil {
		t.Fatalf("Bastion's directory = %+v, want the fleet's Workspace the navigator is signed in through, with no default role", bastionRows)
	}
	bastionRow := "/sectors/" + bastion + "/sector-directories/" + bastionRows[0].ID

	// Bastion is directory only: its people enter through its directory alone, so the
	// directory is not removed, and the refusal says to turn directory only off first.
	if status, body := navigator.do(ctx, http.MethodPatch, consoleAPI+"/resources", []byte(`[{"op":"remove","path":"`+bastionRow+`"}]`)); status != http.StatusConflict || !strings.Contains(string(body), "This sector is directory only") || !strings.Contains(string(body), "Turn directory only off first, then remove the directory.") {
		t.Errorf("removing the directory of a sector that is directory only: status %d, want 409 saying what to do first: %s", status, body)
	}
	if rows := directories(ctx, t, governor, bastion); len(rows) != 1 {
		t.Fatalf("Bastion's directory after the refused removal = %+v, want it kept", rows)
	}

	// The default role is the row's, set under Update on SectorDirectories, which the
	// governor holds as marshal in every sector. Bastion's row names none, and no other
	// sector has the directory, so a stranger from the fleet's directory is refused by
	// invitation only: the browser lands on the login page with the code, no session
	// opens, and no account is created.
	drifter := newBrowser(t, s, consoleAPI)
	if status, body := drifter.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET session before the drifter's login: status %d: %s", status, body)
	}
	status, location := drifter.loginConsoleGoogle(ctx, drifterUser)
	if want := consoleLogin + "?code=" + string(sessioninfo.RefusedInvitationOnly); status != http.StatusFound || location != want {
		t.Fatalf("the drifter's Google login with no default role on the row: status %d to %q, want %d to %q", status, location, http.StatusFound, want)
	}
	if sess := drifter.session(ctx); sess["authenticated"] != false {
		t.Errorf("the drifter's session after the refusal = %v, want none", sess)
	}
	if row := crewAccount(ctx, t, governor, drifterUser); row != nil {
		t.Errorf("the drifter's account after the refusal = %v, want none created", row)
	}

	// Bastion's row names SectorCrew, and the drifter's next sign-in creates her seated
	// there. Her session came through Bastion's directory, so it enters the sector a
	// password does not reach: her chart lights Bastion, and the rigger's still does not.
	nameSeat := `[{"op":"patch","path":"` + bastionRow + `","value":{"defaultSeat":"SectorCrew"}}]`
	if status, body := governor.do(ctx, http.MethodPatch, consoleAPI+"/resources", []byte(nameSeat)); status != http.StatusOK {
		t.Fatalf("naming Bastion's default role: status %d: %s", status, body)
	}
	if status, location := drifter.loginConsoleGoogle(ctx, drifterUser); status != http.StatusFound || location != consolePage {
		t.Fatalf("the drifter's Google login once Bastion's row names a default role: status %d to %q, want %d to %q", status, location, http.StatusFound, consolePage)
	}
	if got, want := drifter.domains(ctx), []string{bastion}; !slices.Equal(got, want) {
		t.Errorf("the drifter's chart = %v, want %v: Bastion's row seats her, and her session came through its directory", got, want)
	}
	if held := rolesHeldInSector(ctx, t, governor, bastion, drifterUser); !slices.Equal(held, []string{"SectorCrew"}) {
		t.Errorf("the drifter's roles at Bastion = %v, want SectorCrew alone, given by the directory's default role", held)
	}
	if got, want := rigger.domains(ctx), []string{anvil}; !slices.Equal(got, want) {
		t.Errorf("the rigger's chart with Bastion still directory only = %v, want %v", got, want)
	}
	// Cleared with an explicit null, the way the Sign-in page clears it, the row names
	// no default role again.
	clearSeat := `[{"op":"patch","path":"` + bastionRow + `","value":{"defaultSeat":null}}]`
	if status, body := governor.do(ctx, http.MethodPatch, consoleAPI+"/resources", []byte(clearSeat)); status != http.StatusOK {
		t.Fatalf("clearing Bastion's default role: status %d: %s", status, body)
	}
	if rows := directories(ctx, t, governor, bastion); len(rows) != 1 || rows[0].DefaultSeat != nil {
		t.Fatalf("Bastion's directory once the default role is cleared = %+v, want the row with none", rows)
	}

	// Headquarters opens Bastion to passwords: the rigger's chart lights it and the
	// bosun signs in. The directory may be removed now, and the role it gave the drifter
	// stands.
	openBastion := `[{"op":"patch","path":"/sectors/` + bastion + `","value":{"directoryOnly":false}}]`
	if status, body := governor.do(ctx, http.MethodPatch, consoleAPI+"/resources", []byte(openBastion)); status != http.StatusOK {
		t.Fatalf("opening Bastion to passwords: status %d: %s", status, body)
	}
	if got, want := rigger.domains(ctx), []string{anvil, bastion}; !slices.Equal(got, want) {
		t.Errorf("the rigger's chart once Bastion admits passwords = %v, want %v", got, want)
	}
	if status, body := bosun.login(ctx, "bosun", personaPassword); status != http.StatusOK {
		t.Errorf("the bosun's password sign-in once Bastion admits passwords: status %d, want 200: %s", status, body)
	}
	if status, body := navigator.do(ctx, http.MethodPatch, consoleAPI+"/resources", []byte(`[{"op":"remove","path":"`+bastionRow+`"}]`)); status != http.StatusOK {
		t.Errorf("removing Bastion's directory once it admits passwords: status %d: %s", status, body)
	}
	if held := rolesHeldInSector(ctx, t, governor, bastion, drifterUser); !slices.Equal(held, []string{"SectorCrew"}) {
		t.Errorf("the drifter's roles at Bastion once its directory is removed = %v, want the role it gave to stand", held)
	}

	// Every edit is audited: the records' change events carry the declaration's writes.
	events := changeEvents(ctx, t, s, "Sectors", "SectorDirectories")
	if events["Sectors"] < 1 || events["SectorDirectories"] < 5 {
		t.Errorf("change events = %v, want the flag write on Sectors and the two removals, the setting and the two default role writes on SectorDirectories", events)
	}
}

// directoryOfSession is a directory as the session read names one.
type directoryOfSession struct {
	Method     string
	Connection string
}

// sessionDirectory reads the organization's directory a session came through from the
// session read; nil when it reports none, as for a password or a personal account.
func sessionDirectory(t *testing.T, sess map[string]any) *directoryOfSession {
	t.Helper()

	raw, ok := sess["directory"]
	if !ok || raw == nil {
		return nil
	}
	fields, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("the session read's directory = %v, want an object naming a method and a connection", raw)
	}
	method, _ := fields["method"].(string)
	connection, _ := fields["connection"].(string)

	return &directoryOfSession{Method: method, Connection: connection}
}

// drifterUser is a stranger from the fleet's directory on no manifest, whom no row seats
// until Bastion's names a default role.
const drifterUser = "drifter"

// sectorDirectory is a row of the directories record as the console lists it.
type sectorDirectory struct {
	ID          string  `json:"id"`
	Method      string  `json:"method"`
	Connection  string  `json:"connection"`
	DefaultSeat *string `json:"defaultSeat"`
}

// directories lists the sector's rows of the directories record on the console: one at most.
func directories(ctx context.Context, t *testing.T, b *browser, sector string) []sectorDirectory {
	t.Helper()

	status, body := b.do(ctx, http.MethodGet, sectorPath(sector, "sector-directories"), nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s's directories: status %d: %s", sector, status, body)
	}
	var rows []sectorDirectory
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("the directories are not a list: %v: %s", err, body)
	}

	return rows
}

// changeEvents counts the change events written for each table.
func changeEvents(ctx context.Context, t *testing.T, s *served, tables ...string) map[string]int64 {
	t.Helper()

	counts := map[string]int64{}
	for _, table := range tables {
		iter := s.db.Client.Single().Query(ctx, cloudspanner.Statement{
			SQL:    "SELECT COUNT(*) FROM DataChangeEvents WHERE TableName = @table",
			Params: map[string]any{"table": table},
		})
		row, err := iter.Next()
		if err != nil {
			t.Fatalf("counting change events of %s: %v", table, err)
		}
		var n int64
		if err := row.Columns(&n); err != nil {
			t.Fatalf("reading the count of %s: %v", table, err)
		}
		iter.Stop()
		counts[table] = n
	}

	return counts
}
