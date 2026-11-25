// Demonstrates: auth.holdings, auth.groups, auth.generated-identity.
package integration

// generated_identity_holdings_test: what is held and why, as the generated management of
// the crew auth serves it for the pages that say it.
//
// A person's roles are one list, a role of their own beside each role that comes to them
// through a group, and the row says which. The lists of groups say how many people each
// group has and which roles it holds, and the roles a group holds say how many people
// lose each when the group stops holding it. A role of a person's own carries the reason
// it was given for, read by whoever the role file grants the field
// (generated_identity_reasons_test.go has the taking of a role for a set time).
//
// The suites drive the lists and methods as served, against the real engine over the
// shipped role file, on the Spanner emulator: the lists are queries the generator writes
// in Spanner's SQL, and the generated management has no other database. The people are
// the ones of the lists' suite (ada and fay Cadets at Anvil, bo a Pilot at Anvil and at
// Bastion, cy an Archivist in every sector, di a Veteran at Bastion, eve a Pilot at Anvil
// and in every sector), the cast's Marshal of Anvil, and the governor, who runs
// headquarters and is the fleet's super administrator.

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/impulseframework/impulse/accesstypes"
)

// The lists of what is held, as the generated routes name them: a sector's one list of
// roles, and the whole fleet's lists.
const (
	sectorHeldRolesPath  = "crew-sector-held-roles"
	heldRolesPath        = "crew-held-roles"
	listedGroupsPath     = "crew-listed-groups"
	listedGroupRolesPath = "crew-listed-group-roles"
)

// heldRole is one row of a list of the roles people hold, as a test compares it: who,
// which role, the group it comes through (none for a role of the person's own), and what
// the row says of the group and of why.
type heldRole struct {
	Username, Role, Group string
	GlobalGroup           bool
	GroupPage             string
	Reason                string
}

// heldRoles reads the rows of a list of the roles people hold, in the order served.
func heldRoles(t *testing.T, rows []map[string]any) []heldRole {
	t.Helper()

	held := make([]heldRole, 0, len(rows))
	for _, row := range rows {
		global, _ := row["globalGroup"].(bool)
		held = append(held, heldRole{
			Username: text(row, "username"), Role: cell[string](t, row, "role"), Group: text(row, "groupName"),
			GlobalGroup: global, GroupPage: text(row, "groupPage"), Reason: text(row, "reason"),
		})
		if (text(row, "groupId") == "") != (text(row, "groupName") == "") {
			t.Errorf("the row %v names a group by its id and not by its name, or the other way round", row)
		}
	}

	return held
}

// TestGeneratedIdentityRolesAreOneList holds a person's roles as one list: a role of
// their own beside each role that comes through a group, the row naming the group, a
// sector's group by a page of its own and the whole fleet's group as not the sector's;
// the reason a role was given for beside it; the sector's own list of the roles people
// hold themselves as it was, with the reason; a role that comes through a group not
// taken away from the person, and the person's own role of the same name taken away
// alone; and the reason read by whoever holds the field and nobody else.
func TestGeneratedIdentityRolesAreOneList(t *testing.T) {
	t.Parallel()
	w := newTenantListsWorld(t)

	post := func(user accesstypes.User, method, body string) (int, []byte) {
		return doRequestAs(t, w.h, user, http.MethodPost, sectorPath(anvil, method), body)
	}
	hq := func(method, body string) (int, []byte) {
		return doRequestAs(t, w.h, "governor", http.MethodPost, consoleAPI+"/"+method, body)
	}
	done := func(status int, body []byte) {
		t.Helper()
		assertStatus(t, status, http.StatusNoContent, body)
	}
	adas := func(user accesstypes.User, path string) []map[string]any {
		t.Helper()
		status, body := doRequestAs(t, w.h, user, http.MethodGet, path+"?limit=200&filter=userId:eq:"+w.ids["ada"], "")
		assertStatus(t, status, http.StatusOK, body)

		return decodeRows(t, body)
	}

	// Ada is a Cadet at Anvil. She is put in a group of the sector's and in one of the
	// whole fleet's, each of which holds the Engineer's role at Anvil, and is given the
	// role herself too, until later and for a reason.
	status, body := post("marshal", "create-crew-sector-group", `{"name":"Hangar hands","description":"The crew who work Anvil's hangar deck."}`)
	assertStatus(t, status, http.StatusOK, body)
	hangar := cell[string](t, decodeRow(t, body), "id")
	done(post("marshal", "add-crew-sector-group-member", fmt.Sprintf(`{"groupId":%q,"userId":%q}`, hangar, w.ids["ada"])))
	done(post("marshal", "give-crew-sector-group-role", fmt.Sprintf(`{"groupId":%q,"role":"Engineer"}`, hangar)))
	status, body = hq("create-crew-group", `{"name":"Fleet inspectors","description":"Headquarters' inspectors, sent to a sector at a time."}`)
	assertStatus(t, status, http.StatusOK, body)
	inspectors := cell[string](t, decodeRow(t, body), "id")
	done(hq("add-crew-group-member", fmt.Sprintf(`{"groupId":%q,"userId":%q}`, inspectors, w.ids["ada"])))
	done(hq("give-crew-group-role", fmt.Sprintf(`{"groupId":%q,"role":"Engineer","domain":%q}`, inspectors, anvil)))
	const why = "Covering the hangar while the chief is away"
	later := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	done(post("marshal", "assign-crew-sector-role", periodBody(t, map[string]any{"userId": w.ids["ada"], "role": "Engineer", "reason": "  " + why + " "}, time.Time{}, later)))

	// The sector's one list: her own roles first, then the same role through each group,
	// by the group's name. A sector's group has a page in the sector; the fleet's has none
	// there, and the row says it is the fleet's.
	want := []heldRole{
		{Username: "ada", Role: "Cadet"},
		{Username: "ada", Role: "Engineer", Reason: why},
		{Username: "ada", Role: "Engineer", Group: "Fleet inspectors", GlobalGroup: true},
		{Username: "ada", Role: "Engineer", Group: "Hangar hands", GroupPage: hangar},
	}
	rows := adas("marshal", sectorPath(anvil, sectorHeldRolesPath))
	if got := heldRoles(t, rows); !slices.Equal(got, want) {
		t.Errorf("the roles ada holds at Anvil, as one list:\n got %+v\nwant %+v", got, want)
	}
	if got := periodOf(t, rows[1]); !got.Equal(accesstypes.Period{End: later}) {
		t.Errorf("the period of ada's own Engineer in the one list = %+v, want until %s", got, later)
	}
	// The whole fleet's one list says the same of her, with where each role is held.
	fleet := adas("governor", consoleAPI+"/"+heldRolesPath)
	wantFleet := []heldRole{want[0], want[1], {Username: "ada", Role: "Engineer", Group: "Fleet inspectors"}, {Username: "ada", Role: "Engineer", Group: "Hangar hands"}}
	if got := heldRoles(t, fleet); !slices.Equal(got, wantFleet) {
		t.Errorf("the roles ada holds, as the whole fleet's one list:\n got %+v\nwant %+v", got, wantFleet)
	}
	for _, row := range fleet {
		if kind, sector := cell[string](t, row, "kind"), cell[string](t, row, "domainId"); kind != "domain" || sector != anvil {
			t.Errorf("the fleet's row %v is held in %s %s, want in the sector Anvil", row, kind, sector)
		}
	}
	// The sector's list of the roles people hold themselves is as it was: no row comes
	// through a group, each is read by its person and its role, and it carries the reason.
	own := sectorRoles(t, w.h, "marshal", anvil)
	if got := text(own[w.ids["ada"]+"/Engineer"], "reason"); got != why {
		t.Errorf("the reason of ada's own Engineer in the sector's list of roles = %q, want %q", got, why)
	}
	status, body = doRequestAs(t, w.h, "marshal", http.MethodGet, sectorPath(anvil, sectorRolesPath+"/"+w.ids["ada"]+"/Engineer"), "")
	assertStatus(t, status, http.StatusOK, body)
	if got := text(decodeRow(t, body), "reason"); got != why {
		t.Errorf("the reason of ada's own Engineer, read by its key = %q, want %q", got, why)
	}

	// Who reads the reason is the role file's to say, field by field. Bo is made a
	// Dispatcher, who sees who holds which role in the sector's own crew and nothing of
	// why, and who holds no grant on the one list.
	done(post("marshal", "assign-crew-sector-role", fmt.Sprintf(`{"userId":%q,"role":"Dispatcher"}`, w.ids["bo"])))
	seen := false
	for key, row := range sectorRoles(t, w.h, "bo", anvil) {
		if _, shown := row["reason"]; shown {
			t.Errorf("the Dispatcher's row %s carries the reason %v, and the role holds no grant on it", key, row["reason"])
		}
		seen = seen || key == w.ids["ada"]+"/Engineer"
	}
	if !seen {
		t.Error("the Dispatcher's list of the roles held at Anvil leaves out ada's Engineer, the row that carries a reason")
	}
	status, body = doRequestAs(t, w.h, "bo", http.MethodGet, sectorPath(anvil, sectorHeldRolesPath), "")
	assertStatus(t, status, http.StatusForbidden, body)

	// A role that comes through a group is not taken away from the person: the request
	// that names the group's row is refused in words that say where it is changed, and
	// one that names a group the role does not come through finds no row.
	status, body = post("marshal", "remove-crew-sector-role", fmt.Sprintf(`{"userId":%q,"role":"Engineer","groupId":%q}`, w.ids["ada"], hangar))
	assertStatus(t, status, http.StatusBadRequest, body)
	if !strings.Contains(string(body), "comes to the person through a group") || !strings.Contains(string(body), "on the group's page") {
		t.Errorf("the refusal to take a group's role away from a person = %s, want it to say where the role is changed", body)
	}
	status, body = post("marshal", "remove-crew-sector-role", fmt.Sprintf(`{"userId":%q,"role":"Cadet","groupId":%q}`, w.ids["ada"], hangar))
	assertStatus(t, status, http.StatusNotFound, body)
	if got := heldRoles(t, adas("marshal", sectorPath(anvil, sectorHeldRolesPath))); !slices.Equal(got, want) {
		t.Errorf("the roles ada holds after the two refused removals:\n got %+v\nwant %+v", got, want)
	}

	// The request that names no group names her own role, which goes; what she holds
	// through the groups stays, and she still works the hangar by it.
	done(post("marshal", "remove-crew-sector-role", fmt.Sprintf(`{"userId":%q,"role":"Engineer"}`, w.ids["ada"])))
	want = []heldRole{want[0], want[2], want[3]}
	if got := heldRoles(t, adas("marshal", sectorPath(anvil, sectorHeldRolesPath))); !slices.Equal(got, want) {
		t.Errorf("the roles ada holds once her own Engineer is taken away:\n got %+v\nwant %+v", got, want)
	}
	if status, _ := doRequestAs(t, w.h, "ada", http.MethodGet, sectorPath(anvil, "refits"), ""); status != http.StatusOK {
		t.Errorf("ada lists the refits through the groups: status = %d, want 200", status)
	}
	// A role she holds through a group alone is no role of her own to take away.
	status, body = post("marshal", "remove-crew-sector-role", fmt.Sprintf(`{"userId":%q,"role":"Engineer"}`, w.ids["ada"]))
	assertStatus(t, status, http.StatusNotFound, body)
	if got := heldRoles(t, adas("marshal", sectorPath(anvil, sectorHeldRolesPath))); !slices.Equal(got, want) {
		t.Errorf("the roles ada holds after a removal that named no row:\n got %+v\nwant %+v", got, want)
	}
}

// TestGeneratedIdentityHeldRolesAnswerTheRoleQuestion holds the question a list's role
// column asks when its filter menu opens, as the browser sends it to the whole fleet's
// list of the roles people hold: the roles named left out, the rows sorted by role, one
// row, and the role column. Asked, as the first question is while no role is known, for a
// row whose role holds a value, the list answers one row, holding the first role in order.
// Named every role the list holds but one, it answers one row, holding the role left out;
// named every role, it answers none.
func TestGeneratedIdentityHeldRolesAnswerTheRoleQuestion(t *testing.T) {
	t.Parallel()
	w := newTenantListsWorld(t)
	path := consoleAPI + "/" + heldRolesPath

	status, body := doRequestAs(t, w.h, "governor", http.MethodGet, path+"?limit=200", "")
	assertStatus(t, status, http.StatusOK, body)
	rows := decodeRows(t, body)
	if len(rows) == 200 {
		t.Fatalf("the list of the roles people hold filled a page of 200 rows, want it whole in one")
	}
	var held []string
	for _, row := range rows {
		if role := cell[string](t, row, "role"); !slices.Contains(held, role) {
			held = append(held, role)
		}
	}
	slices.Sort(held)
	if len(held) < 2 {
		t.Fatalf("the roles the list holds = %v, want two at least", held)
	}

	ask := func(filter string) []map[string]any {
		t.Helper()
		query := url.Values{
			"filter":  {filter},
			"sort":    {"role:asc"},
			"limit":   {"1"},
			"columns": {"role"},
		}
		status, body := doRequestAs(t, w.h, "governor", http.MethodGet, path+"?"+query.Encode(), "")
		assertStatus(t, status, http.StatusOK, body)

		return decodeRows(t, body)
	}

	notin := func(known []string) string {
		return "role:notin:(" + strings.Join(known, ",") + ")"
	}

	// The first question, while the browser knows no role: any row whose cell holds one.
	if answer := ask("role:isnotnull"); len(answer) != 1 || cell[string](t, answer[0], "role") != held[0] {
		t.Errorf("asked for a role, any, the list answered %v, want one row holding %s, the first of %v", answer, held[0], held)
	}
	left := held[len(held)/2]
	answer := ask(notin(slices.DeleteFunc(slices.Clone(held), func(role string) bool { return role == left })))
	if len(answer) != 1 || cell[string](t, answer[0], "role") != left {
		t.Errorf("asked for a role other than %v, the list answered %v, want one row holding %s", held, answer, left)
	}
	if answer := ask(notin(held)); len(answer) != 0 {
		t.Errorf("asked for a role other than every one it holds, %v, the list answered %v, want no row", held, answer)
	}
}

// heldRoleAnswers is the methods the Execute answer of each row of a sector's one list
// of roles lists for the user, by the person's id, the role and the group's id, joined
// as "id/role/group".
func heldRoleAnswers(t *testing.T, h http.Handler, user accesstypes.User, sector string) map[string][]string {
	t.Helper()

	status, body := doRequestAs(t, h, user, http.MethodGet, sectorPath(sector, sectorHeldRolesPath+"?limit=200&capabilities=Execute"), "")
	assertStatus(t, status, http.StatusOK, body)
	answers := make(map[string][]string)
	for _, row := range decodeRows(t, body) {
		caps, _ := row["zzCapabilities"].(map[string]any)
		listed, _ := caps["Execute"].([]any)
		methods := make([]string, 0, len(listed))
		for _, method := range listed {
			methods = append(methods, fmt.Sprint(method))
		}
		answers[cell[string](t, row, "userId")+"/"+cell[string](t, row, "role")+"/"+text(row, "groupId")] = methods
	}

	return answers
}

// groupRow is what a list of groups says a group holds: how many people, and its roles,
// a line each.
type groupRow struct {
	People float64
	Roles  []string
}

// groupsHeld reads what a list of groups says each group holds, by the group's name.
func groupsHeld(t *testing.T, rows []map[string]any) map[string]groupRow {
	t.Helper()

	held := make(map[string]groupRow, len(rows))
	for _, row := range rows {
		lines, _ := row["roles"].([]any)
		roles := make([]string, 0, len(lines))
		for _, line := range lines {
			roles = append(roles, fmt.Sprint(line))
		}
		held[cell[string](t, row, "name")] = groupRow{People: cell[float64](t, row, "people"), Roles: roles}
	}

	return held
}

// TestGeneratedIdentityGroupsSayWhatTheyHold holds what a list of groups says of each:
// how many people it has and the roles it holds, a line each. A place or a role whose
// period has not begun counts, one that has ended does not; the whole fleet's list says
// where a role is held when that is not where the group is kept, the sector by its name.
// A group's delete asks first whatever the list says, counting every place and role the
// group has, ended or not, and takes them all once confirmed.
func TestGeneratedIdentityGroupsSayWhatTheyHold(t *testing.T) {
	t.Parallel()
	w := newTenantListsWorld(t)

	post := func(user accesstypes.User, method, body string) (int, []byte) {
		return doRequestAs(t, w.h, user, http.MethodPost, sectorPath(anvil, method), body)
	}
	hq := func(method, body string) (int, []byte) {
		return doRequestAs(t, w.h, "governor", http.MethodPost, consoleAPI+"/"+method, body)
	}
	done := func(status int, body []byte) {
		t.Helper()
		assertStatus(t, status, http.StatusNoContent, body)
	}
	sector := func() map[string]groupRow {
		t.Helper()
		status, body := doRequestAs(t, w.h, "marshal", http.MethodGet, sectorPath(anvil, sectorGroupsPath+"?limit=200"), "")
		assertStatus(t, status, http.StatusOK, body)

		return groupsHeld(t, decodeRows(t, body))
	}
	fleet := func() map[string]groupRow {
		t.Helper()
		status, body := doRequestAs(t, w.h, "governor", http.MethodGet, consoleAPI+"/"+listedGroupsPath+"?limit=200", "")
		assertStatus(t, status, http.StatusOK, body)

		return groupsHeld(t, decodeRows(t, body))
	}
	holds := func(what string, got, want groupRow) {
		t.Helper()
		if got.People != want.People || !slices.Equal(got.Roles, want.Roles) {
			t.Errorf("%s: the list says %v people and the roles %q, want %v and %q", what, got.People, got.Roles, want.People, want.Roles)
		}
	}

	// A new group holds nothing.
	status, body := post("marshal", "create-crew-sector-group", `{"name":"Night watch","description":"Keeps Anvil's deck through the night."}`)
	assertStatus(t, status, http.StatusOK, body)
	watch := cell[string](t, decodeRow(t, body), "id")
	holds("a new group", sector()["Night watch"], groupRow{Roles: []string{}})

	// Ada is in it from now and fay from later; it holds the Engineer's role from now
	// and the Harbormaster's from later. What has not begun is held: it stands in a
	// deletion's way like the rest.
	later := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	place := func(person string) map[string]any { return map[string]any{"groupId": watch, "userId": w.ids[person]} }
	role := func(name string) map[string]any { return map[string]any{"groupId": watch, "role": name} }
	done(post("marshal", "add-crew-sector-group-member", periodBody(t, place("ada"), time.Time{}, time.Time{})))
	done(post("marshal", "add-crew-sector-group-member", periodBody(t, place("fay"), later, time.Time{})))
	done(post("marshal", "give-crew-sector-group-role", periodBody(t, role("Engineer"), time.Time{}, time.Time{})))
	done(post("marshal", "give-crew-sector-group-role", periodBody(t, role("Harbormaster"), later, time.Time{})))
	holds("two people and two roles, one of each not begun", sector()["Night watch"], groupRow{People: 2, Roles: []string{"Engineer", "Harbormaster"}})
	// The cast's own group, which the seed fills: the deck crew holds two roles, and the
	// deckhand's place in it stands whether or not this world wrote the account.
	holds("the deck crew", sector()["Deck crew"], groupRow{People: 1, Roles: []string{"Cadet", "SectorCrew"}})
	// The whole fleet's list holds a sector's groups too, each role by its name alone,
	// since it is held where the group is kept.
	holds("the night watch in the fleet's list", fleet()["Night watch"], groupRow{People: 2, Roles: []string{"Engineer", "Harbormaster"}})

	// What has ended is not held: fay's place and the Harbormaster's role end, and the
	// rows stand in the tables.
	endNow(t, w.db, "CrewGroupMembers", map[string]any{"GroupId": watch, "UserId": w.ids["fay"]})
	endNow(t, w.db, "CrewGroupRoles", map[string]any{"GroupId": watch, "Kind": "domain", "Axis": "", "DomainId": anvil, "Role": "Harbormaster"})
	holds("one place and one role ended", sector()["Night watch"], groupRow{People: 1, Roles: []string{"Engineer"}})

	// The delete asks first, and its question counts the ended rows with the rest. It is
	// asked through headquarters' method, which names the group by its row of the whole
	// fleet's list.
	asked := func(want string) {
		t.Helper()
		status, body := hq("delete-crew-group", fmt.Sprintf(`{"groupId":%q}`, watch))
		assertStatus(t, status, http.StatusPreconditionRequired, body)
		if got := refusalMessage(t, body); got != want {
			t.Errorf("the question = %q, want %q", got, want)
		}
	}
	asked("Delete this group and everything under it: 2 roles, 2 members. This can't be undone.")
	// Emptied of what has not ended, the list says it holds nothing, and the delete
	// still asks, counting the ended place and the ended role; confirmed, it takes them.
	done(post("marshal", "remove-crew-sector-group-member", fmt.Sprintf(`{"groupId":%q,"userId":%q}`, watch, w.ids["ada"])))
	done(post("marshal", "take-crew-sector-group-role", fmt.Sprintf(`{"groupId":%q,"role":"Engineer"}`, watch)))
	holds("emptied", sector()["Night watch"], groupRow{Roles: []string{}})
	asked("Delete this group and everything under it: 1 role, 1 member. This can't be undone.")
	done(hq("delete-crew-group", fmt.Sprintf(`{"groupId":%q,"everythingUnder":true}`, watch)))
	if _, listed := sector()["Night watch"]; listed {
		t.Error("the night watch is listed after it was deleted")
	}
	for _, table := range []string{"CrewGroupMembers", "CrewGroupRoles"} {
		if n := rowsStanding(t, w, "SELECT COUNT(*) FROM "+table+" WHERE GroupId = @group", map[string]any{"group": watch}); n != 0 {
			t.Errorf("%s holds %d ended row(s) of the deleted night watch, want none", table, n)
		}
	}

	// A group of the whole fleet's holds roles in one sector, in every sector and for
	// the whole fleet: its line for each says where, the sector by its name, and a role
	// held where the group is kept is its name alone.
	status, body = hq("create-crew-group", `{"name":"Fleet inspectors","description":"Headquarters' inspectors, sent to a sector at a time."}`)
	assertStatus(t, status, http.StatusOK, body)
	inspectors := cell[string](t, decodeRow(t, body), "id")
	done(hq("add-crew-group-member", fmt.Sprintf(`{"groupId":%q,"userId":%q}`, inspectors, w.ids["bo"])))
	done(hq("give-crew-group-role", fmt.Sprintf(`{"groupId":%q,"role":"Engineer","domain":%q}`, inspectors, anvil)))
	done(hq("give-crew-group-role", fmt.Sprintf(`{"groupId":%q,"role":"Pilot","everyDomain":true}`, inspectors)))
	done(hq("give-crew-group-role", fmt.Sprintf(`{"groupId":%q,"role":"CrewCommon"}`, inspectors)))
	holds("the fleet's inspectors", fleet()["Fleet inspectors"], groupRow{People: 1, Roles: []string{"CrewCommon", "Engineer in sector Anvil", "Pilot in every sector"}})
	if _, listed := sector()["Fleet inspectors"]; listed {
		t.Error("Anvil's groups list the fleet's inspectors, a group kept for the whole fleet")
	}
	// The fleet's methods name a group by its row of that list: the delete of one that
	// holds something asks first, and its row is read by the group's id.
	status, body = hq("delete-crew-group", fmt.Sprintf(`{"groupId":%q}`, inspectors))
	assertStatus(t, status, http.StatusPreconditionRequired, body)
	if got, want := refusalMessage(t, body), "Delete this group and everything under it: 3 roles, 1 member. This can't be undone."; got != want {
		t.Errorf("the question = %q, want %q", got, want)
	}
	status, body = doRequestAs(t, w.h, "governor", http.MethodGet, consoleAPI+"/"+listedGroupsPath+"/"+inspectors, "")
	assertStatus(t, status, http.StatusOK, body)
	if got := cell[string](t, decodeRow(t, body), "name"); got != "Fleet inspectors" {
		t.Errorf("the group read by its id is named %q, want Fleet inspectors", got)
	}
}

// losing reads how many people lose each role a list of group roles holds, by the
// group's id, where the role is held and the role, joined as "group kind/domain role".
func losing(t *testing.T, rows []map[string]any) map[string]float64 {
	t.Helper()

	counts := make(map[string]float64, len(rows))
	for _, row := range rows {
		where := "domain/" + anvil
		if kind, listed := row["kind"].(string); listed {
			where = kind + "/" + text(row, "domainId")
		}
		counts[cell[string](t, row, "groupId")+" "+where+" "+cell[string](t, row, "role")] = cell[float64](t, row, "losing")
	}

	return counts
}

// TestGeneratedIdentityWhoLosesAGroupRole holds how many people lose a role when a group
// stops holding it: the people in the group now who hold the role there in no other way.
// A person who holds it themselves, through another group, or in every sector does not
// lose it; a person whose place has not begun does not hold it yet; nobody loses a role
// the group holds from later on; a role of the person's own that has ended is no other
// way; and a fleet's group that holds a role both in a sector and in every sector loses
// nobody with the sector's.
func TestGeneratedIdentityWhoLosesAGroupRole(t *testing.T) {
	t.Parallel()
	w := newTenantListsWorld(t)
	ctx := t.Context()

	// Gil is a Cadet at Anvil and nothing else, as ada is.
	w.seat(ctx, t, "gil", "Cadet", anvil)

	post := func(method, body string) (int, []byte) {
		return doRequestAs(t, w.h, "marshal", http.MethodPost, sectorPath(anvil, method), body)
	}
	hq := func(method, body string) (int, []byte) {
		return doRequestAs(t, w.h, "governor", http.MethodPost, consoleAPI+"/"+method, body)
	}
	done := func(status int, body []byte) {
		t.Helper()
		assertStatus(t, status, http.StatusNoContent, body)
	}
	group := func(name string) string {
		t.Helper()
		status, body := post("create-crew-sector-group", fmt.Sprintf(`{"name":%q,"description":"A group of this suite."}`, name))
		assertStatus(t, status, http.StatusOK, body)

		return cell[string](t, decodeRow(t, body), "id")
	}
	counts := func() map[string]float64 {
		t.Helper()
		status, body := doRequestAs(t, w.h, "marshal", http.MethodGet, sectorPath(anvil, sectorGroupRolesPath+"?limit=200"), "")
		assertStatus(t, status, http.StatusOK, body)

		return losing(t, decodeRows(t, body))
	}
	fleetCounts := func() map[string]float64 {
		t.Helper()
		status, body := doRequestAs(t, w.h, "governor", http.MethodGet, consoleAPI+"/"+listedGroupRolesPath+"?limit=200", "")
		assertStatus(t, status, http.StatusOK, body)

		return losing(t, decodeRows(t, body))
	}
	loses := func(what string, got map[string]float64, key string, want float64) {
		t.Helper()
		count, listed := got[key]
		if !listed || count != want {
			t.Errorf("%s: %v lose it (listed = %v), want %v", what, count, listed, want)
		}
	}

	// The hangar hands hold the Pilot's role at Anvil. In the group: ada, who holds it
	// in no other way; bo, a Pilot at Anvil himself; fay, who is in the flight line too,
	// which holds the role as well; cy, given the role in every sector; and gil, whose
	// place begins later.
	later := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	hangar, line := group("Hangar hands"), group("Flight line")
	place := func(in, person string) string { return fmt.Sprintf(`{"groupId":%q,"userId":%q}`, in, w.ids[person]) }
	for _, person := range []string{"ada", "bo", "fay", "cy"} {
		done(post("add-crew-sector-group-member", place(hangar, person)))
	}
	done(post("add-crew-sector-group-member", periodBody(t, map[string]any{"groupId": hangar, "userId": w.ids["gil"]}, later, time.Time{})))
	done(post("add-crew-sector-group-member", place(line, "fay")))
	done(post("give-crew-sector-group-role", fmt.Sprintf(`{"groupId":%q,"role":"Pilot"}`, hangar)))
	done(post("give-crew-sector-group-role", fmt.Sprintf(`{"groupId":%q,"role":"Pilot"}`, line)))
	done(hq("assign-crew-role", fmt.Sprintf(`{"userId":%q,"role":"Pilot","everyDomain":true}`, w.ids["cy"])))
	// The Harbormaster's role nobody holds in another way, and the Engineer's the group
	// holds from later on.
	done(post("give-crew-sector-group-role", fmt.Sprintf(`{"groupId":%q,"role":"Harbormaster"}`, hangar)))
	done(post("give-crew-sector-group-role", periodBody(t, map[string]any{"groupId": hangar, "role": "Engineer"}, later, time.Time{})))

	at := func(in, role string) string { return in + " domain/" + anvil + " " + role }
	got := counts()
	loses("the Pilot's role of the hangar hands: ada alone", got, at(hangar, "Pilot"), 1)
	loses("the Pilot's role of the flight line: fay holds it through the hangar hands too", got, at(line, "Pilot"), 0)
	loses("the Harbormaster's role of the hangar hands: the four whose place counts", got, at(hangar, "Harbormaster"), 4)
	loses("the Engineer's role the hangar hands hold from later on", got, at(hangar, "Engineer"), 0)
	// The whole fleet's list of the roles groups hold counts the same.
	fleet := fleetCounts()
	loses("the fleet's list, the Pilot's role of the hangar hands", fleet, at(hangar, "Pilot"), 1)
	loses("the fleet's list, the Harbormaster's role of the hangar hands", fleet, at(hangar, "Harbormaster"), 4)

	// Ada is given the role herself: nobody loses it with the group.
	done(post("assign-crew-sector-role", fmt.Sprintf(`{"userId":%q,"role":"Pilot"}`, w.ids["ada"])))
	loses("the Pilot's role once ada holds it herself", counts(), at(hangar, "Pilot"), 0)
	// Her own role ends, its row still standing: it is no other way any more.
	endNow(t, w.db, "CrewUserRoles", map[string]any{"Kind": "domain", "Axis": "", "DomainId": anvil, "Role": "Pilot", "UserId": w.ids["ada"]})
	loses("the Pilot's role once ada's own has ended", counts(), at(hangar, "Pilot"), 1)
	// The flight line's role ends too: fay holds it through the hangar hands alone.
	endNow(t, w.db, "CrewGroupRoles", map[string]any{"GroupId": line, "Kind": "domain", "Axis": "", "DomainId": anvil, "Role": "Pilot"})
	got = counts()
	loses("the Pilot's role once the flight line's has ended", got, at(hangar, "Pilot"), 2)
	if _, listed := got[at(line, "Pilot")]; listed {
		t.Error("the flight line's Pilot role is listed after it ended")
	}

	// A group of the whole fleet's holds the Engineer's role at Anvil and in every
	// sector, and di is in it: taking the sector's away leaves her the role there by the
	// other, and taking the one held in every sector away loses it for her.
	status, body := hq("create-crew-group", `{"name":"Fleet inspectors","description":"Headquarters' inspectors, sent to a sector at a time."}`)
	assertStatus(t, status, http.StatusOK, body)
	inspectors := cell[string](t, decodeRow(t, body), "id")
	done(hq("add-crew-group-member", fmt.Sprintf(`{"groupId":%q,"userId":%q}`, inspectors, w.ids["di"])))
	done(hq("give-crew-group-role", fmt.Sprintf(`{"groupId":%q,"role":"Engineer","domain":%q}`, inspectors, anvil)))
	done(hq("give-crew-group-role", fmt.Sprintf(`{"groupId":%q,"role":"Engineer","everyDomain":true}`, inspectors)))
	fleet = fleetCounts()
	loses("the inspectors' Engineer at Anvil, held in every sector too", fleet, at(inspectors, "Engineer"), 0)
	loses("the inspectors' Engineer in every sector", fleet, inspectors+" every/ Engineer", 1)
	done(hq("take-crew-group-role", fmt.Sprintf(`{"groupId":%q,"role":"Engineer","everyDomain":true}`, inspectors)))
	loses("the inspectors' Engineer at Anvil, once it is the one way", fleetCounts(), at(inspectors, "Engineer"), 1)
}
