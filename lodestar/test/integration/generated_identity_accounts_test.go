// Demonstrates: auth.holdings, auth.generated-identity.
package integration

// generated_identity_accounts_test: the whole fleet's accounts as the generated
// management of the crew auth lists them for the Accounts page. Each account is a row of
// the users table with a line for each role it holds and until when its sign-in is
// locked by failed tries, and the list filters by the name and the email address beside
// the username. The fleet's methods on a person name the person by their row here.
//
// The suites drive the list as served, against the real engine over the shipped role
// file, on the Spanner emulator: the list is a query the generator writes in Spanner's
// SQL. The people are the ones of the lists' suite (generated_identity_lists_test.go),
// with more of the test's own, and the cast's personas on the served stack.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/identity"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session"
)

// sectorNames are the seed's sectors by the names people call them by.
var sectorNames = map[string]string{anvil: "Anvil", bastion: "Bastion", cinder: "Cinder"}

// listedAccounts reads the accounts list as the user, filtered by the filter where one is
// given, and answers the rows by username.
func listedAccounts(t *testing.T, h http.Handler, user accesstypes.User, filter string) map[string]map[string]any {
	t.Helper()

	target := consoleAPI + "/" + crewListedAccountsPath + "?limit=200"
	if filter != "" {
		target += "&filter=" + url.QueryEscape(filter)
	}
	status, body := doRequestAs(t, h, user, http.MethodGet, target, "")
	assertStatus(t, status, http.StatusOK, body)
	rows := make(map[string]map[string]any)
	for _, row := range decodeRows(t, body) {
		rows[cell[string](t, row, "username")] = row
	}

	return rows
}

// roleLines reads the lines of what an account holds, in the order served.
func roleLines(row map[string]any) []string {
	listed, _ := row["roles"].([]any)
	lines := make([]string, 0, len(listed))
	for _, line := range listed {
		lines = append(lines, fmt.Sprint(line))
	}

	return lines
}

// assignCrewRoles gives the person the roles, each where its scope says, in one
// transaction, by the account's id through the identity client's AssignRole.
func (w *tenantListsWorld) assignCrewRoles(ctx context.Context, t *testing.T, name string, roles map[accesstypes.Role]accesstypes.PolicyScope) {
	t.Helper()

	if err := w.inTransaction(ctx, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		for role, scope := range roles {
			if err := w.app.CrewIdentity().AssignRole(ctx, txn, scope, accesstypes.User(w.ids[name]), role); err != nil {
				return fmt.Errorf("crewidentity.Client.AssignRole(%s, %s): %w", name, role, err)
			}
		}

		return nil
	}); err != nil {
		t.Fatalf("giving %s their roles: %v", name, err)
	}
}

// crewScope is the scope of a role held in the sector, in every sector (everyDomain), or
// for the whole fleet (neither).
func crewScope(t *testing.T, sector string, everyDomain bool) accesstypes.PolicyScope {
	t.Helper()

	var domain *string
	if sector != "" {
		domain = &sector
	}
	scope, err := identity.Scope(domain, everyDomain)
	if err != nil {
		t.Fatalf("identity.Scope(%q, %t): %v", sector, everyDomain, err)
	}

	return scope
}

// TestGeneratedIdentityAccountsSayWhatEachHolds holds the lines of what an account
// holds: a role held for the whole fleet by its name, one held in every sector and one
// held in a sector by where, the sector by its name, whether the person holds the role
// themselves or through a group of a sector's or of the fleet's, each line once and in
// alphabetical order. They are the lines the one list of the person's roles makes.
func TestGeneratedIdentityAccountsSayWhatEachHolds(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	w := newTenantListsWorld(t)

	post := func(user accesstypes.User, path, body string) (int, []byte) {
		return doRequestAs(t, w.h, user, http.MethodPost, path, body)
	}
	done := func(status int, body []byte) {
		t.Helper()
		assertStatus(t, status, http.StatusNoContent, body)
	}

	// Zed holds two roles for the whole fleet, one in each sector and one in every
	// sector, of his own.
	w.ids["zed"] = string(makeCrewAccountAt(ctx, t, w.db, "zed", "zed@example.com"))
	w.assignCrewRoles(ctx, t, "zed", map[accesstypes.Role]accesstypes.PolicyScope{
		"CrewCommon": crewScope(t, "", false),
		"Surveyor":   crewScope(t, "", false),
		"Cadet":      crewScope(t, anvil, false),
		"Pilot":      crewScope(t, bastion, false),
		"Veteran":    crewScope(t, cinder, false),
		"Archivist":  crewScope(t, "", true),
	})
	// Anvil's group gives him the Engineer's role there, and the Cadet's he holds already.
	status, body := post("marshal", sectorPath(anvil, "create-crew-sector-group"), `{"name":"Hangar hands","description":"The crew who work Anvil's hangar deck."}`)
	assertStatus(t, status, http.StatusOK, body)
	hangar := cell[string](t, decodeRow(t, body), "id")
	done(post("marshal", sectorPath(anvil, "add-crew-sector-group-member"), fmt.Sprintf(`{"groupId":%q,"userId":%q}`, hangar, w.ids["zed"])))
	done(post("marshal", sectorPath(anvil, "give-crew-sector-group-role"), fmt.Sprintf(`{"groupId":%q,"role":"Engineer"}`, hangar)))
	done(post("marshal", sectorPath(anvil, "give-crew-sector-group-role"), fmt.Sprintf(`{"groupId":%q,"role":"Cadet"}`, hangar)))
	// The fleet's group gives him a role in every sector, one at Bastion and one for the
	// whole fleet.
	status, body = post("governor", consoleAPI+"/create-crew-group", `{"name":"Fleet inspectors","description":"Headquarters' inspectors, sent to a sector at a time."}`)
	assertStatus(t, status, http.StatusOK, body)
	inspectors := cell[string](t, decodeRow(t, body), "id")
	done(post("governor", consoleAPI+"/add-crew-group-member", fmt.Sprintf(`{"groupId":%q,"userId":%q}`, inspectors, w.ids["zed"])))
	done(post("governor", consoleAPI+"/give-crew-group-role", fmt.Sprintf(`{"groupId":%q,"role":"Pilot","everyDomain":true}`, inspectors)))
	done(post("governor", consoleAPI+"/give-crew-group-role", fmt.Sprintf(`{"groupId":%q,"role":"Harbormaster","domain":%q}`, inspectors, bastion)))
	done(post("governor", consoleAPI+"/give-crew-group-role", fmt.Sprintf(`{"groupId":%q,"role":"BriefingClerk"}`, inspectors)))

	// Ten lines, the Cadet's at Anvil once though he holds it two ways.
	want := []string{
		"Archivist in every sector",
		"BriefingClerk",
		"Cadet in sector Anvil",
		"CrewCommon",
		"Engineer in sector Anvil",
		"Harbormaster in sector Bastion",
		"Pilot in every sector",
		"Pilot in sector Bastion",
		"Surveyor",
		"Veteran in sector Cinder",
	}
	got := roleLines(listedAccounts(t, w.h, "governor", "username:eq:zed")["zed"])
	if !slices.Equal(got, want) {
		t.Errorf("the lines of what zed holds:\n got %q\nwant %q", got, want)
	}

	// The one list of his roles says the same: a line made of each of its rows, the
	// sector by its name, each once, in order.
	status, body = doRequestAs(t, w.h, "governor", http.MethodGet, consoleAPI+"/"+heldRolesPath+"?limit=200&filter=userId:eq:"+w.ids["zed"], "")
	assertStatus(t, status, http.StatusOK, body)
	var made []string
	for _, row := range decodeRows(t, body) {
		line := cell[string](t, row, "role")
		switch kind := cell[string](t, row, "kind"); kind {
		case "every":
			line += " in every sector"
		case "domain":
			name, ok := sectorNames[cell[string](t, row, "domainId")]
			if !ok {
				t.Fatalf("the row %v holds a role in a sector the seed does not name", row)
			}
			line += " in sector " + name
		}
		if !slices.Contains(made, line) {
			made = append(made, line)
		}
	}
	slices.Sort(made)
	if !slices.Equal(got, made) {
		t.Errorf("the lines of what zed holds against the one list of his roles:\n got %q\nmade %q", got, made)
	}
}

// TestGeneratedIdentityAccountsSayTheLock holds until when an account's sign-in is
// locked, read from the counts the session library keeps for the account: the latest
// lock still ahead among the rows kept under its username and under its id, each key
// read; and nothing for a lock that has passed, for an account with no count, and for
// the counts kept under its address, which limit the mail it is sent and lock no
// sign-in.
func TestGeneratedIdentityAccountsSayTheLock(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	w := newTenantListsWorld(t)
	w.seat(ctx, t, "gus", "Cadet", anvil)

	now := time.Now().UTC().Truncate(time.Microsecond)
	keys := func(name, email string) []string {
		t.Helper()
		id, err := impulse.UUIDFromString(w.ids[name])
		if err != nil {
			t.Fatalf("impulse.UUIDFromString(%s): %v", w.ids[name], err)
		}

		return session.AccountAttemptKeys(id, name, email)
	}
	var rows []*spanner.Mutation
	lock := func(key string, until time.Time) {
		rows = append(rows, spanner.InsertOrUpdateMap("CrewSessionSignInAttempts", map[string]any{
			"Name": key, "Failures": int64(5), "WindowEndsAt": now.Add(time.Hour), "LockedUntil": until, "UpdatedAt": spanner.CommitTimestamp,
		}))
	}
	// Ada, bo and cy are each locked under one of the three keys of an account; di under
	// all three, the latest lock under her id's count of the first password's guesses;
	// eve's address is locked from mail under each of its keys; fay's lock has passed;
	// and gus has no count at all.
	locks := map[string]time.Time{}
	for i, name := range []string{"ada", "bo", "cy"} {
		locks[name] = now.Add(time.Duration(i+1) * time.Hour)
		lock(keys(name, "")[i], locks[name])
	}
	di := keys("di", "")
	lock(di[0], now.Add(time.Hour))
	lock(di[1], now.Add(3*time.Hour))
	lock(di[2], now.Add(2*time.Hour))
	locks["di"] = now.Add(3 * time.Hour)
	for _, key := range keys("eve", "eve@example.com")[3:] {
		lock(key, now.Add(time.Hour))
	}
	lock(keys("fay", "")[0], now.Add(-time.Minute))
	if _, err := w.db.Apply(ctx, rows); err != nil {
		t.Fatalf("writing the counts: %v", err)
	}

	listed := listedAccounts(t, w.h, "governor", "")
	for _, name := range []string{"ada", "bo", "cy", "di", "eve", "fay", "gus"} {
		row, ok := listed[name]
		if !ok {
			t.Fatalf("the accounts list does not carry %s", name)
		}
		want, locked := locks[name]
		if !locked {
			if row["lockedUntil"] != nil {
				t.Errorf("%s's row says locked until %v, want nothing", name, row["lockedUntil"])
			}

			continue
		}
		got, err := time.Parse(time.RFC3339Nano, text(row, "lockedUntil"))
		if err != nil {
			t.Errorf("%s's row says locked until %v, want %s: %v", name, row["lockedUntil"], want, err)

			continue
		}
		if !got.Equal(want) {
			t.Errorf("%s's row says locked until %s, want %s", name, got, want)
		}
	}
}

// TestGeneratedIdentityAccountsSayTheLockOfFailedSignIns holds the lock on the served
// stack, with the real session manager and its limiter: failed sign-ins through the
// sign-in route lock the pilot's name, and the pilot's row of the accounts says until
// when, the moment the list of failed sign-ins shows; headquarters clears it through the
// generated method, and the row says nothing.
func TestGeneratedIdentityAccountsSayTheLockOfFailedSignIns(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := newServed(ctx, t)

	hq := newBrowser(t, s, consoleAPI)
	hq.signIn(ctx, "governor")
	pilotRow := func() map[string]any {
		t.Helper()
		status, body := hq.do(ctx, http.MethodGet, consoleAPI+"/"+crewListedAccountsPath+"?limit=200&filter=username:eq:pilot", nil)
		assertStatus(t, status, http.StatusOK, body)
		rows := decodeRows(t, body)
		if len(rows) != 1 {
			t.Fatalf("the accounts list filtered by the pilot's username answers %d rows, want 1: %s", len(rows), body)
		}

		return rows[0]
	}
	if got := pilotRow()["lockedUntil"]; got != nil {
		t.Fatalf("the pilot's row before any failed sign-in says locked until %v, want nothing", got)
	}

	stranger := newBrowser(t, s, consoleAPI)
	if status, body := stranger.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("the session route before a login: status %d: %s", status, body)
	}
	const failuresToLock = 5 // the session library's default limit
	for range failuresToLock {
		if status, body := stranger.login(ctx, "pilot", "not the password"); status != http.StatusUnauthorized {
			t.Fatalf("a wrong password: status %d, want 401: %s", status, body)
		}
	}

	status, body := hq.do(ctx, http.MethodGet, consoleAPI+"/crew-session-sign-in-attempts?limit=200", nil)
	assertStatus(t, status, http.StatusOK, body)
	var shown string
	for _, row := range decodeRows(t, body) {
		if cell[string](t, row, "name") == "pilot" {
			shown = text(row, "lockedUntil")
		}
	}
	if shown == "" {
		t.Fatalf("the list of failed sign-ins does not show the pilot locked: %s", body)
	}
	want, err := time.Parse(time.RFC3339Nano, shown)
	if err != nil {
		t.Fatalf("the failed sign-ins list's lock %q: %v", shown, err)
	}
	got, err := time.Parse(time.RFC3339Nano, text(pilotRow(), "lockedUntil"))
	if err != nil || !got.Equal(want) {
		t.Errorf("the pilot's row says locked until %s (%v), want %s, as the list of failed sign-ins shows", got, err, want)
	}

	status, body = hq.do(ctx, http.MethodPost, consoleAPI+"/unlock-crew-sign-in", []byte(`{"name":"pilot"}`))
	assertStatus(t, status, http.StatusNoContent, body)
	if got := pilotRow()["lockedUntil"]; got != nil {
		t.Errorf("the pilot's row after the unlock says locked until %v, want nothing", got)
	}
}

// TestGeneratedIdentityAccountsFilter holds the accounts list's filters: by the name and
// by the email address beside the username, each answering the one account.
func TestGeneratedIdentityAccountsFilter(t *testing.T) {
	t.Parallel()
	w := newTenantListsWorld(t)

	status, body := doRequestAs(t, w.h, "governor", http.MethodPost, consoleAPI+"/set-crew-user-name", fmt.Sprintf(`{"userId":%q,"name":"Ada of Anvil"}`, w.ids["ada"]))
	assertStatus(t, status, http.StatusNoContent, body)
	for _, filter := range []string{"name:eq:Ada of Anvil", "email:eq:ada@example.com", "username:eq:ada"} {
		rows := listedAccounts(t, w.h, "governor", filter)
		if _, ok := rows["ada"]; len(rows) != 1 || !ok {
			t.Errorf("the accounts filtered by %q answer %d rows, want ada's alone: %v", filter, len(rows), rows)
		}
	}
}

// TestGeneratedIdentityAccountsAreReadAsGranted holds the role file's grants on the
// accounts list: the command staff list the usernames alone, as they list them from the
// users table, so their rows hold neither the name, the address, what each holds nor
// the lock, and a filter on the name is refused them as a filter on any field a caller is
// not granted is.
func TestGeneratedIdentityAccountsAreReadAsGranted(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	w := newTenantListsWorld(t)
	w.ids["staffer"] = string(makeCrewAccount(ctx, t, w.db, "staffer"))
	w.assignCrewRoles(ctx, t, "staffer", map[accesstypes.Role]accesstypes.PolicyScope{"CommandStaff": crewScope(t, "", false)})

	rows := listedAccounts(t, w.h, "staffer", "")
	if _, ok := rows["ada"]; !ok {
		t.Fatalf("the command staff's accounts list does not carry ada: %v", rows)
	}
	for name, row := range rows {
		for _, field := range []string{"roles", "lockedUntil", "name", "email"} {
			if value, shown := row[field]; shown {
				t.Errorf("%s's row, as the command staff read it, carries %s = %v, and the role is granted the username alone", name, field, value)
			}
		}
	}
	status, body := doRequestAs(t, w.h, "staffer", http.MethodGet, consoleAPI+"/"+crewListedAccountsPath+"?filter="+url.QueryEscape("name:eq:Ada"), "")
	assertStatus(t, status, http.StatusForbidden, body)
}

// TestGeneratedIdentityAccountsOfferTheMethods holds what a row of the accounts list
// offers: the fleet's methods on a person name the person by the row, so headquarters
// is offered them on another person's row, and on its own row every one of them but
// Disable and Delete.
func TestGeneratedIdentityAccountsOfferTheMethods(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	w := newTenantListsWorld(t)
	w.ids["chief"] = string(makeCrewAccount(ctx, t, w.db, "chief"))
	w.assignCrewRoles(ctx, t, "chief", map[accesstypes.Role]accesstypes.PolicyScope{"Headquarters": crewScope(t, "", false)})

	offered := func(id string) []string {
		t.Helper()

		return accountExecuteAnswer(t, w.h, "chief", id)
	}
	onAccount := []string{
		"DeleteCrewUser", "DisableCrewUser", "EnableCrewUser", "EndCrewUserSessions", "RemoveCrewPassword", "RenameCrewUser",
		"RequireCrewPasswordChange", "RevokeCrewUserTrustedDevices", "SendCrewUserPasswordResetLink", "SetCrewUserEmail", "SetCrewUserName",
	}
	if got := offered(w.ids["ada"]); !slices.Equal(got, onAccount) {
		t.Errorf("ada's row offers headquarters %v, want %v", got, onAccount)
	}
	own := slices.DeleteFunc(slices.Clone(onAccount), func(method string) bool {
		return method == "DeleteCrewUser" || method == "DisableCrewUser"
	})
	if got := offered(w.ids["chief"]); !slices.Equal(got, own) {
		t.Errorf("headquarters' own row offers it %v, want %v: neither Disable nor Delete", got, own)
	}
}
