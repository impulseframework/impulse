// Demonstrates: auth.holdings, auth.generated-identity.
package integration

// generated_identity_accounts_test: the whole fleet's accounts as the generated
// management of the crew auth lists them for the Accounts page. Each account is a row of
// the users table with a line for each role it holds and until when its sign-in is
// locked by failed tries, and the list filters by the name and the email address beside
// the username. The fleet's methods on a person name the person by their row here. A
// sector's list of its people says the two facts as well: the roles each person holds in
// the sector itself, and the lock.
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
// locked, on the accounts list and on a sector's list of its people, read from the
// counts the session library keeps for the account: the latest lock still ahead among
// the rows kept under its username and under its id, each key read; and nothing for a
// lock that has passed, for an account with no count, and for the counts kept under its
// address, which limit the mail it is sent and lock no sign-in.
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

	says := func(list, name string, row map[string]any) {
		t.Helper()
		want, locked := locks[name]
		if !locked {
			if row["lockedUntil"] != nil {
				t.Errorf("%s's row of %s says locked until %v, want nothing", name, list, row["lockedUntil"])
			}

			return
		}
		got, err := time.Parse(time.RFC3339Nano, text(row, "lockedUntil"))
		if err != nil {
			t.Errorf("%s's row of %s says locked until %v, want %s: %v", name, list, row["lockedUntil"], want, err)

			return
		}
		if !got.Equal(want) {
			t.Errorf("%s's row of %s says locked until %s, want %s", name, list, got, want)
		}
	}
	listed := listedAccounts(t, w.h, "governor", "")
	for _, name := range []string{"ada", "bo", "cy", "di", "eve", "fay", "gus"} {
		row, ok := listed[name]
		if !ok {
			t.Fatalf("the accounts list does not carry %s", name)
		}
		says("the accounts", name, row)
	}
	// Anvil's people say the same of everyone Anvil holds: di is Bastion's alone.
	people := sectorList(t, w.h, "marshal", anvil, sectorPeoplePath)
	for _, name := range []string{"ada", "bo", "cy", "eve", "fay", "gus"} {
		row, ok := people[w.ids[name]]
		if !ok {
			t.Fatalf("Anvil's people do not carry %s", name)
		}
		says("Anvil's people", name, row)
	}
}

// TestGeneratedIdentityAccountsSayTheLockOfFailedSignIns holds the lock on the served
// stack, with the real session manager and its limiter: failed sign-ins through the
// sign-in route lock the pilot's name, and the pilot's row of the accounts and of Anvil's
// people says until when, the moment the list of failed sign-ins shows; headquarters
// clears it through the generated method, and the rows say nothing.
func TestGeneratedIdentityAccountsSayTheLockOfFailedSignIns(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := newServed(ctx, t)

	hq := newBrowser(t, s, consoleAPI)
	hq.signIn(ctx, "governor")
	pilotRows := func() map[string]map[string]any {
		t.Helper()
		rows := make(map[string]map[string]any, 2)
		for list, target := range map[string]string{
			"the accounts":   consoleAPI + "/" + crewListedAccountsPath + "?limit=200&filter=username:eq:pilot",
			"Anvil's people": sectorPath(anvil, sectorPeoplePath+"?limit=200&filter=username:eq:pilot"),
		} {
			status, body := hq.do(ctx, http.MethodGet, target, nil)
			assertStatus(t, status, http.StatusOK, body)
			listed := decodeRows(t, body)
			if len(listed) != 1 {
				t.Fatalf("%s filtered by the pilot's username answer %d rows, want 1: %s", list, len(listed), body)
			}
			rows[list] = listed[0]
		}

		return rows
	}
	for list, row := range pilotRows() {
		if got := row["lockedUntil"]; got != nil {
			t.Fatalf("the pilot's row of %s before any failed sign-in says locked until %v, want nothing", list, got)
		}
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
	for list, row := range pilotRows() {
		got, err := time.Parse(time.RFC3339Nano, text(row, "lockedUntil"))
		if err != nil || !got.Equal(want) {
			t.Errorf("the pilot's row of %s says locked until %s (%v), want %s, as the list of failed sign-ins shows", list, got, err, want)
		}
	}

	status, body = hq.do(ctx, http.MethodPost, consoleAPI+"/unlock-crew-sign-in", []byte(`{"name":"pilot"}`))
	assertStatus(t, status, http.StatusNoContent, body)
	for list, row := range pilotRows() {
		if got := row["lockedUntil"]; got != nil {
			t.Errorf("the pilot's row of %s after the unlock says locked until %v, want nothing", list, got)
		}
	}
}

// TestGeneratedIdentityAccountsFilter holds the accounts list's filters: by the name and
// by the email address beside the username, each answering the one account.
func TestGeneratedIdentityAccountsFilter(t *testing.T) {
	t.Parallel()
	w := newTenantListsWorld(t)
	// ada holds a role at Anvil alone, so Anvil manages the account; a role for the whole
	// application beside it makes the account the fleet's to name.
	w.giveCrewRole(t.Context(), t, "ada", "CrewCommon", accesstypes.GlobalPolicyScope())

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
	// ada holds a role at Anvil alone, so Anvil manages the account; a role for the whole
	// application beside it makes the account the fleet's to act on.
	w.giveCrewRole(ctx, t, "ada", "CrewCommon", accesstypes.GlobalPolicyScope())

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

// TestGeneratedIdentitySectorPeopleSayWhatEachHoldsThere holds the roles a sector's list
// of its people says each person holds: the roles held in the sector itself, of the
// person's own or through a group of the sector's or of the fleet's, each name once and
// in alphabetical order, a role whose period has not begun among them and one that has
// ended, or that comes through a place that has ended, not; a role held in every sector,
// in another sector or for the whole fleet is no name there. Case by case, the names are
// the roles the sector's one list of the roles held answers for the person with
// everyDomain false.
func TestGeneratedIdentitySectorPeopleSayWhatEachHoldsThere(t *testing.T) {
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
	marshal := func(method, body string) (int, []byte) { return post("marshal", sectorPath(anvil, method), body) }
	hq := func(method, body string) (int, []byte) { return post("governor", consoleAPI+"/"+method, body) }
	later := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	// The suite's two roles: R, the Engineer's, and S, the Cadet's, each a role a person
	// holds in a sector.
	const r, s = "Engineer", "Cadet"
	person := func(name string, own map[accesstypes.Role]accesstypes.PolicyScope) {
		t.Helper()
		w.ids[name] = string(makeCrewAccount(ctx, t, w.db, accesstypes.User(name)))
		if len(own) > 0 {
			w.assignCrewRoles(ctx, t, name, own)
		}
	}
	inAnvil := crewScope(t, anvil, false)
	group := func(user accesstypes.User, method, body string) string {
		t.Helper()
		status, answer := post(user, method, body)
		assertStatus(t, status, http.StatusOK, answer)

		return cell[string](t, decodeRow(t, answer), "id")
	}
	// A period that ends a moment away: the rule refuses an end that is past already, so a
	// request that took longer than the moment is given a longer one. The test waits for
	// the last such end before it reads.
	var last time.Time
	endsSoon := func(what string, give func(endsAt time.Time) (int, []byte)) {
		t.Helper()
		for _, in := range []time.Duration{2 * time.Second, 10 * time.Second} {
			ends := time.Now().UTC().Add(in).Truncate(time.Microsecond)
			status, body := give(ends)
			if status == http.StatusBadRequest {
				continue
			}
			assertStatus(t, status, http.StatusNoContent, body)
			if ends.After(last) {
				last = ends
			}

			return
		}
		t.Fatalf("%s was not given an end a moment away: each request took longer than its moment", what)
	}

	// Anvil's own group, and two of the fleet's: each holds R at Anvil; the second holds R
	// at Anvil from later on and R in every sector now.
	hands := group("marshal", sectorPath(anvil, "create-crew-sector-group"), `{"name":"Hangar hands","description":"The crew who work Anvil's hangar deck."}`)
	done(marshal("give-crew-sector-group-role", fmt.Sprintf(`{"groupId":%q,"role":%q}`, hands, r)))
	inspectors := group("governor", consoleAPI+"/create-crew-group", `{"name":"Fleet inspectors","description":"Headquarters' inspectors, sent to a sector at a time."}`)
	done(hq("give-crew-group-role", fmt.Sprintf(`{"groupId":%q,"role":%q,"domain":%q}`, inspectors, r, anvil)))
	relief := group("governor", consoleAPI+"/create-crew-group", `{"name":"Relief crews","description":"Headquarters' crews, sent where they are wanted."}`)
	done(hq("give-crew-group-role", periodBody(t, map[string]any{"groupId": relief, "role": r, "domain": anvil}, later, time.Time{})))
	done(hq("give-crew-group-role", fmt.Sprintf(`{"groupId":%q,"role":%q,"everyDomain":true}`, relief, r)))
	place := func(name, groupID string) {
		t.Helper()
		done(hq("add-crew-group-member", fmt.Sprintf(`{"groupId":%q,"userId":%q}`, groupID, w.ids[name])))
	}

	cases := []struct {
		name, holds string
		want        []string
	}{
		{name: "c1", holds: "R of their own at Anvil", want: []string{r}},
		{name: "c2", holds: "S of their own at Anvil, and R of their own there from later on", want: []string{s, r}},
		{name: "c3", holds: "S of their own at Anvil, and R of their own there for a period that has ended", want: []string{s}},
		{name: "c4", holds: "a place in Anvil's own group, which holds R there", want: []string{r}},
		{name: "c5", holds: "a place in a group of the fleet's, which holds R at Anvil", want: []string{r}},
		{name: "c6", holds: "R of their own at Anvil, and a place in a group that holds R there", want: []string{r}},
		{name: "c7", holds: "S of their own at Anvil, and a place that has ended in a group that holds R there", want: []string{s}},
		{name: "c8", holds: "S of their own at Anvil, and a place in a group of the fleet's that holds R at Anvil from later on and R in every sector now", want: []string{s}},
		{name: "c9", holds: "S of their own at Anvil, R of their own at Bastion, a role in every sector and a role for the whole fleet", want: []string{s}},
	}
	person("c1", map[accesstypes.Role]accesstypes.PolicyScope{r: inAnvil})
	person("c2", map[accesstypes.Role]accesstypes.PolicyScope{s: inAnvil})
	done(marshal("assign-crew-sector-role", periodBody(t, map[string]any{"userId": w.ids["c2"], "role": r}, later, time.Time{})))
	person("c3", map[accesstypes.Role]accesstypes.PolicyScope{s: inAnvil})
	endsSoon("c3's own R", func(endsAt time.Time) (int, []byte) {
		return marshal("assign-crew-sector-role", periodBody(t, map[string]any{"userId": w.ids["c3"], "role": r}, time.Time{}, endsAt))
	})
	person("c4", nil)
	place("c4", hands)
	person("c5", nil)
	place("c5", inspectors)
	person("c6", map[accesstypes.Role]accesstypes.PolicyScope{r: inAnvil})
	place("c6", hands)
	person("c7", map[accesstypes.Role]accesstypes.PolicyScope{s: inAnvil})
	endsSoon("c7's place in the hangar hands", func(endsAt time.Time) (int, []byte) {
		return hq("add-crew-group-member", periodBody(t, map[string]any{"groupId": hands, "userId": w.ids["c7"]}, time.Time{}, endsAt))
	})
	person("c8", map[accesstypes.Role]accesstypes.PolicyScope{s: inAnvil})
	place("c8", relief)
	person("c9", map[accesstypes.Role]accesstypes.PolicyScope{
		s: inAnvil, r: crewScope(t, bastion, false), "Archivist": crewScope(t, "", true), "CrewCommon": crewScope(t, "", false),
	})
	time.Sleep(time.Until(last.Add(100 * time.Millisecond)))

	people := sectorList(t, w.h, "marshal", anvil, sectorPeoplePath)
	for _, tc := range cases {
		row, ok := people[w.ids[tc.name]]
		if !ok {
			t.Errorf("%s (%s) is not one of Anvil's people", tc.name, tc.holds)

			continue
		}
		want := slices.Sorted(slices.Values(tc.want))
		if got := roleLines(row); !slices.Equal(got, want) {
			t.Errorf("%s (%s): Anvil's people say %q, want %q", tc.name, tc.holds, got, want)
		}
		// What Anvil's one list of the roles held answers for the person, held there itself.
		status, body := doRequestAs(t, w.h, "marshal", http.MethodGet, sectorPath(anvil, sectorHeldRolesPath+"?limit=200&filter=userId:eq:"+w.ids[tc.name]), "")
		assertStatus(t, status, http.StatusOK, body)
		var held, everywhere []string
		for _, line := range decodeRows(t, body) {
			name := cell[string](t, line, "role")
			if cell[bool](t, line, "everyDomain") {
				everywhere = append(everywhere, name)
			} else if !slices.Contains(held, name) {
				held = append(held, name)
			}
		}
		slices.Sort(held)
		if got := roleLines(row); !slices.Equal(got, held) {
			t.Errorf("%s (%s): Anvil's people say %q, and Anvil's list of the roles held says %q held there itself", tc.name, tc.holds, got, held)
		}
		if tc.name == "c8" && !slices.Contains(everywhere, r) {
			t.Errorf("c8 (%s): Anvil's list of the roles held says %q in every sector, want %s among them", tc.holds, everywhere, r)
		}
	}
}
