// Demonstrates: auth.periods, auth.groups, auth.generated-identity, auth.tenant-methods, @subjectSet.global.
package integration

// generated_identity_periods_test: the period of an assignment as the generated
// management of the crew auth serves it.
//
// A role given to a person, a role given to a group and a person's place in a group may
// each carry a start and an end, and count inside the period alone. The suite gives each
// of the three a period through the served methods, against the real engine over the
// shipped role file, and asks three times: before the period, inside it and after it.
// Nothing is written between the questions but the periods themselves: a role that has
// not begun grants nothing, one inside its period grants what the role grants, and one
// that has ended grants nothing while its row still stands in the table. The lists
// follow: a sector's list of its people holds who holds something at the moment, and
// the lists of roles carry the period and leave out a row that has ended.
//
// A period is changed by giving again, by whoever may give the role there; the grant
// that holds a Marshal to the roles she may give holds her with a period too; and the
// Super Administrator role takes none.
//
// The people are the ones of the lists' suite: ada and fay, Cadets at Anvil, and di, a
// Veteran at Bastion who holds nothing at Anvil until she is given a role there.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
)

// periodBody is a method's request with a period: the fields given, and the two times
// where each is set.
func periodBody(t *testing.T, fields map[string]any, startsAt, endsAt time.Time) string {
	t.Helper()

	body := make(map[string]any, len(fields)+2)
	for key, value := range fields {
		body[key] = value
	}
	if !startsAt.IsZero() {
		body["startsAt"] = startsAt.Format(time.RFC3339Nano)
	}
	if !endsAt.IsZero() {
		body["endsAt"] = endsAt.Format(time.RFC3339Nano)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	return string(raw)
}

// periodOf reads the period a list's row carries: each time, or the zero time where the
// row holds none.
func periodOf(t *testing.T, row map[string]any) accesstypes.Period {
	t.Helper()

	read := func(key string) time.Time {
		raw, ok := row[key].(string)
		if !ok {
			if row[key] != nil {
				t.Fatalf("cell %q = %v (%T), want a time or nothing, in row %v", key, row[key], row[key], row)
			}

			return time.Time{}
		}
		at, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			t.Fatalf("cell %q = %q is no time: %v", key, raw, err)
		}

		return at
	}

	return accesstypes.Period{Start: read("startsAt"), End: read("endsAt")}
}

// rowsStanding counts the rows a query over the permission store's tables finds, read
// straight from the database.
func rowsStanding(t *testing.T, w *tenantListsWorld, sql string, params map[string]any) int64 {
	t.Helper()

	iter := w.db.Client.Single().Query(t.Context(), spanner.Statement{SQL: sql, Params: params})
	defer iter.Stop()
	row, err := iter.Next()
	if err != nil {
		t.Fatalf("counting with %q: %v", sql, err)
	}
	var n int64
	if err := row.Columns(&n); err != nil {
		t.Fatalf("reading the count of %q: %v", sql, err)
	}

	return n
}

// TestGeneratedIdentityPeriods holds a role of a person's own, a role of a group and a
// place in a group, each given for a period, from before the period to after it.
func TestGeneratedIdentityPeriods(t *testing.T) {
	t.Parallel()
	w := newTenantListsWorld(t)

	// The grants this suite proves, pinned to the roles file (conditions-proven): who
	// may give a role gives it its period, held to the same condition.
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "AssignCrewSectorRole", "new.role != 'SectorMarshal'")
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "GiveCrewSectorGroupRole", "new.role != 'SectorMarshal'")
	provesGrant(t, crew.Roles(), "SectorCrew", "List", "CrewSectorGroups", "group IN subject.groups")

	post := func(user accesstypes.User, method, body string) (int, []byte) {
		return doRequestAs(t, w.h, user, http.MethodPost, sectorPath(anvil, method), body)
	}
	give := func(user accesstypes.User, method, body string) {
		t.Helper()
		status, answer := post(user, method, body)
		assertStatus(t, status, http.StatusNoContent, answer)
	}
	list := func(user accesstypes.User, path string) []map[string]any {
		t.Helper()
		status, body := doRequestAs(t, w.h, user, http.MethodGet, sectorPath(anvil, path+"?limit=200"), "")
		assertStatus(t, status, http.StatusOK, body)

		return decodeRows(t, body)
	}
	answers := func(user accesstypes.User, path string) int {
		status, _ := doRequestAs(t, w.h, user, http.MethodGet, sectorPath(anvil, path), "")

		return status
	}
	// What each question asks: the Engineer's refits and the Harbormaster's ships.
	const refits, ships = "refits", "ships"

	// A period far ahead: nothing given with it counts while the suite runs.
	later := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	role := func(person, name string) map[string]any { return map[string]any{"userId": w.ids[person], "role": name} }

	// The Marshal makes a group, the night watch. It holds the Harbormaster's role with
	// no period and the Engineer's from later on; fay is in it with no period, and ada
	// from later on. Ada holds SectorCrew herself, which lists the groups she is in, and
	// is given the Engineer's role of her own from later on. Di is given the Cadet's role
	// at Anvil from later on, by headquarters: she is not one of Anvil's people yet.
	status, body := post("marshal", "create-crew-sector-group", `{"name":"Night watch","description":"Keeps Anvil's deck through the night."}`)
	assertStatus(t, status, http.StatusOK, body)
	group := cell[string](t, decodeRow(t, body), "id")
	groupRole := func(name string) map[string]any { return map[string]any{"groupId": group, "role": name} }
	place := func(person string) map[string]any { return map[string]any{"groupId": group, "userId": w.ids[person]} }
	diAtAnvil := map[string]any{"userId": w.ids["di"], "role": "Cadet", "domain": anvil}
	giveDi := func(startsAt, endsAt time.Time) (int, []byte) {
		return doRequestAs(t, w.h, "governor", http.MethodPost, consoleAPI+"/assign-crew-role", periodBody(t, diAtAnvil, startsAt, endsAt))
	}

	give("marshal", "assign-crew-sector-role", periodBody(t, role("ada", "SectorCrew"), time.Time{}, time.Time{}))
	give("marshal", "assign-crew-sector-role", periodBody(t, role("ada", "Engineer"), later, time.Time{}))
	give("marshal", "give-crew-sector-group-role", periodBody(t, groupRole("Harbormaster"), time.Time{}, time.Time{}))
	give("marshal", "give-crew-sector-group-role", periodBody(t, groupRole("Engineer"), later, time.Time{}))
	give("marshal", "add-crew-sector-group-member", periodBody(t, place("fay"), time.Time{}, time.Time{}))
	give("marshal", "add-crew-sector-group-member", periodBody(t, place("ada"), later, time.Time{}))
	status, body = giveDi(later, time.Time{})
	assertStatus(t, status, http.StatusNoContent, body)

	// The questions, asked before, inside and after: what each person may do, who the
	// sector's people are, which groups ada sees, and what the lists of roles hold.
	type answer struct {
		adaRefits, adaShips, fayRefits, fayShips int
		diListed                                 bool
		adaGroups                                []string
		roles, places, groupRoles, through       []string
	}
	ask := func() answer {
		t.Helper()
		got := answer{adaRefits: answers("ada", refits), adaShips: answers("ada", ships), fayRefits: answers("fay", refits), fayShips: answers("fay", ships)}
		_, got.diListed = sectorList(t, w.h, "marshal", anvil, sectorPeoplePath)[w.ids["di"]]
		got.adaGroups = namesOf(t, list("ada", sectorGroupsPath), "name")
		for key := range sectorRoles(t, w.h, "marshal", anvil) {
			for _, person := range []string{"ada", "di"} {
				if name, ok := strings.CutPrefix(key, w.ids[person]+"/"); ok && (name == "Engineer" || person == "di") {
					got.roles = append(got.roles, person+" "+name)
				}
			}
		}
		for _, row := range list("marshal", sectorGroupPeoplePath) {
			if cell[string](t, row, "groupId") == group {
				got.places = append(got.places, cell[string](t, row, "username"))
			}
		}
		for _, row := range list("marshal", sectorGroupRolesPath) {
			if cell[string](t, row, "groupId") == group {
				got.groupRoles = append(got.groupRoles, cell[string](t, row, "role"))
			}
		}
		for _, row := range list("marshal", sectorRolesThroughGroupPath) {
			if cell[string](t, row, "groupId") == group {
				got.through = append(got.through, cell[string](t, row, "username")+" "+cell[string](t, row, "role"))
			}
		}
		for _, names := range [][]string{got.roles, got.places, got.groupRoles, got.through} {
			slices.Sort(names)
		}

		return got
	}
	check := func(when string, got, want answer) {
		t.Helper()
		if fmt.Sprintf("%+v", got) != fmt.Sprintf("%+v", want) {
			t.Errorf("%s:\n got %+v\nwant %+v", when, got, want)
		}
	}
	const denied, served = http.StatusForbidden, http.StatusOK

	// Before. Nothing that is given from later on counts: ada has neither the refits nor
	// the ships and sees no group; fay has the ships, through her place and the role
	// with no period, and not the refits; di is not one of Anvil's people. The lists of
	// roles and of places hold each row that has not begun, with its period.
	check("before the periods begin", ask(), answer{
		adaRefits: denied, adaShips: denied, fayRefits: denied, fayShips: served, diListed: false, adaGroups: []string{},
		roles: []string{"ada Engineer", "di Cadet"}, places: []string{"ada", "fay"}, groupRoles: []string{"Engineer", "Harbormaster"},
		through: []string{"ada Engineer", "ada Harbormaster", "fay Engineer", "fay Harbormaster"},
	})
	if got := periodOf(t, sectorRoles(t, w.h, "marshal", anvil)[w.ids["ada"]+"/Engineer"]); !got.Equal(accesstypes.Period{Start: later}) {
		t.Errorf("the period of ada's Engineer in the list = %+v, want from %s", got, later)
	}
	for _, row := range list("marshal", sectorRolesThroughGroupPath) {
		if cell[string](t, row, "groupId") != group {
			continue
		}
		// A role held through a group counts where the place's period and the group
		// role's meet: fay's Harbormaster has neither, and the three others begin later.
		want := accesstypes.Period{Start: later}
		if cell[string](t, row, "username") == "fay" && cell[string](t, row, "role") == "Harbormaster" {
			want = accesstypes.Period{}
		}
		if got := periodOf(t, row); !got.Equal(want) {
			t.Errorf("the period of %s's %s through the night watch = %+v, want %+v", cell[string](t, row, "username"), cell[string](t, row, "role"), got, want)
		}
	}

	// Who may not give the role may not give it a period, and the Marshal's own limit
	// stands with one: a Cadet gives nothing, and no Marshal mints a Marshal for a day.
	status, body = post("fay", "assign-crew-sector-role", periodBody(t, role("ada", "Engineer"), time.Time{}, later))
	assertStatus(t, status, http.StatusForbidden, body)
	status, body = post("marshal", "assign-crew-sector-role", periodBody(t, role("ada", "SectorMarshal"), time.Time{}, later))
	assertStatus(t, status, http.StatusForbidden, body)
	// A period that never counts, and one that is over already, are refused in words.
	status, body = post("marshal", "assign-crew-sector-role", periodBody(t, role("ada", "Engineer"), later, later.Add(-time.Minute)))
	assertStatus(t, status, http.StatusBadRequest, body)
	status, body = post("marshal", "assign-crew-sector-role", periodBody(t, role("ada", "Engineer"), time.Time{}, time.Now().UTC().Add(-time.Minute)))
	assertStatus(t, status, http.StatusBadRequest, body)
	// The Super Administrator role takes no end and no start: there is always one.
	for _, period := range []accesstypes.Period{{End: later}, {Start: later}} {
		status, body = doRequestAs(t, w.h, "governor", http.MethodPost, consoleAPI+"/assign-crew-role",
			periodBody(t, map[string]any{"userId": w.ids["di"], "role": string(accesstypes.SuperAdministrator)}, period.Start, period.End))
		assertStatus(t, status, http.StatusBadRequest, body)
		if !strings.Contains(string(body), "takes no end and no start") {
			t.Errorf("the refusal of a period on the Super Administrator role = %s, want it to say the role takes none", body)
		}
	}

	// Inside. Each is given again, with no start and an end far ahead: the new period
	// replaces the old one, and what begins now counts at the next request.
	give("marshal", "assign-crew-sector-role", periodBody(t, role("ada", "Engineer"), time.Time{}, later))
	give("marshal", "give-crew-sector-group-role", periodBody(t, groupRole("Engineer"), time.Time{}, later))
	give("marshal", "add-crew-sector-group-member", periodBody(t, place("ada"), time.Time{}, later))
	status, body = giveDi(time.Time{}, later)
	assertStatus(t, status, http.StatusNoContent, body)
	inside := answer{
		adaRefits: served, adaShips: served, fayRefits: served, fayShips: served, diListed: true, adaGroups: []string{"Night watch"},
		roles: []string{"ada Engineer", "di Cadet"}, places: []string{"ada", "fay"}, groupRoles: []string{"Engineer", "Harbormaster"},
		through: []string{"ada Engineer", "ada Harbormaster", "fay Engineer", "fay Harbormaster"},
	}
	check("inside the periods", ask(), inside)
	if got := periodOf(t, sectorRoles(t, w.h, "marshal", anvil)[w.ids["ada"]+"/Engineer"]); !got.Equal(accesstypes.Period{End: later}) {
		t.Errorf("the period of ada's Engineer in the list, given again = %+v, want until %s and no start", got, later)
	}

	// After. Each is given once more with an end a moment away, and the suite waits for
	// the last of them. The rule refuses an end that is past already, so a request that
	// took longer than the moment is given a longer one.
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
	endsSoon("ada's Engineer", func(endsAt time.Time) (int, []byte) {
		return post("marshal", "assign-crew-sector-role", periodBody(t, role("ada", "Engineer"), time.Time{}, endsAt))
	})
	endsSoon("the night watch's Engineer", func(endsAt time.Time) (int, []byte) {
		return post("marshal", "give-crew-sector-group-role", periodBody(t, groupRole("Engineer"), time.Time{}, endsAt))
	})
	endsSoon("ada's place in the night watch", func(endsAt time.Time) (int, []byte) {
		return post("marshal", "add-crew-sector-group-member", periodBody(t, place("ada"), time.Time{}, endsAt))
	})
	endsSoon("di's Cadet at Anvil", func(endsAt time.Time) (int, []byte) { return giveDi(time.Time{}, endsAt) })

	standing := func() [4]int64 {
		t.Helper()

		return [4]int64{
			rowsStanding(t, w, "SELECT COUNT(*) FROM CrewUserRoles WHERE UserId = @user AND Role = 'Engineer' AND DomainId = @domain", map[string]any{"user": w.ids["ada"], "domain": anvil}),
			rowsStanding(t, w, "SELECT COUNT(*) FROM CrewUserRoles WHERE UserId = @user AND Role = 'Cadet' AND DomainId = @domain", map[string]any{"user": w.ids["di"], "domain": anvil}),
			rowsStanding(t, w, "SELECT COUNT(*) FROM CrewGroupRoles WHERE GroupId = @group AND Role = 'Engineer'", map[string]any{"group": group}),
			rowsStanding(t, w, "SELECT COUNT(*) FROM CrewGroupMembers WHERE GroupId = @group AND UserId = @user", map[string]any{"group": group, "user": w.ids["ada"]}),
		}
	}
	written := rowsStanding(t, w, "SELECT COUNT(*) FROM CrewUserRoles", nil) + rowsStanding(t, w, "SELECT COUNT(*) FROM CrewGroupRoles", nil) + rowsStanding(t, w, "SELECT COUNT(*) FROM CrewGroupMembers", nil)
	if time.Now().Before(last) {
		check("inside the periods, a moment before they end", ask(), inside)
	}
	time.Sleep(time.Until(last.Add(300 * time.Millisecond)))

	// Nothing was written when the periods ended, and every row still stands; each
	// counts for nothing. Ada has neither the refits nor the ships and sees no group;
	// fay keeps the ships and loses the refits, her place standing and the group's role
	// over; di is not one of Anvil's people; and the lists leave the ended rows out.
	if got := standing(); got != [4]int64{1, 1, 1, 1} {
		t.Errorf("the rows of the four ended assignments = %v, want each still in its table", got)
	}
	if got := rowsStanding(t, w, "SELECT COUNT(*) FROM CrewUserRoles", nil) + rowsStanding(t, w, "SELECT COUNT(*) FROM CrewGroupRoles", nil) + rowsStanding(t, w, "SELECT COUNT(*) FROM CrewGroupMembers", nil); got != written {
		t.Errorf("the rows of the three tables = %d after the periods ended, want %d as before: nothing is written when a period ends", got, written)
	}
	check("after the periods end", ask(), answer{
		adaRefits: denied, adaShips: denied, fayRefits: denied, fayShips: served, diListed: false, adaGroups: []string{},
		roles: nil, places: []string{"fay"}, groupRoles: []string{"Harbormaster"}, through: []string{"fay Harbormaster"},
	})

	// Ending a role now is taking it away, and what has ended is given again like any
	// role: the Marshal gives ada the Engineer's with no period, and it stands.
	give("marshal", "assign-crew-sector-role", periodBody(t, role("ada", "Engineer"), time.Time{}, time.Time{}))
	if got := answers("ada", refits); got != served {
		t.Errorf("ada lists the refits with the role given again and no period: status = %d, want 200", got)
	}
	if got := periodOf(t, sectorRoles(t, w.h, "marshal", anvil)[w.ids["ada"]+"/Engineer"]); !got.IsZero() {
		t.Errorf("the period of ada's Engineer, given again with none = %+v, want none", got)
	}
}
