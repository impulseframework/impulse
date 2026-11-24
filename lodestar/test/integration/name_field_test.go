package integration

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/resource/live"
)

// TestNameField_passesByLevel pins a list sorted by, and showing, the name of the row an
// id points to. The marshal reads Anvil's hangar passes with each pass's level by name:
// every pass carries the name beside its level's key, one name a key, and the list
// sorted by the name walks two rows a page in the order of the names, the key breaking
// ties, the same rows forward and back. Passes of one level are equal names the cursor
// has to cross.
func TestNameField_passesByLevel(t *testing.T) {
	t.Parallel()

	_, h, _ := sharedWorld(t)

	// The passes as they stand, with the name each carries. Other tests add passes to
	// the shared world, so every walk below is held to these by their ids.
	type pass struct {
		id   string
		name string
	}
	var passes []pass
	rr := doRequestRecordedAs(t, h, "marshal", http.MethodGet, sectorPath(anvil, "hangar-passes?columns=id,levelId,levelName&sort=id&limit=200"), "")
	assertStatus(t, rr.Code, http.StatusOK, rr.Body.Bytes())
	levels := make(map[string]string)
	alike := make(map[string]int)
	tie := 0
	for _, row := range decodeRows(t, rr.Body.Bytes()) {
		p := pass{id: cell[string](t, row, "id"), name: cell[string](t, row, "levelName")}
		level := cell[string](t, row, "levelId")
		if p.name == "" {
			t.Errorf("pass %s of level %s carries an empty name", p.id, level)
		}
		if known, ok := levels[level]; ok && known != p.name {
			t.Errorf("level %s is named %q and %q", level, known, p.name)
		}
		levels[level] = p.name
		alike[p.name]++
		tie = max(tie, alike[p.name])
		passes = append(passes, p)
	}
	if len(alike) < 2 || tie < 3 {
		t.Fatalf("the seeded passes carry %d level names over %d passes, %d at most alike; the walk needs two names and three passes alike", len(alike), len(passes), tie)
	}

	ids := make([]string, 0, len(passes))
	for _, p := range passes {
		ids = append(ids, p.id)
	}
	held := url.QueryEscape("id:in:(" + strings.Join(ids, ",") + ")")

	for _, direction := range []string{"asc", "desc"} {
		t.Run(direction, func(t *testing.T) {
			t.Parallel()

			// The key always ascends, whichever way the names run.
			sorted := slices.Clone(passes)
			slices.SortFunc(sorted, func(a, b pass) int {
				byName := cmp.Compare(a.name, b.name)
				if direction == "desc" {
					byName = -byName
				}

				return cmp.Or(byName, cmp.Compare(a.id, b.id))
			})
			want := make([]string, 0, len(sorted))
			for _, p := range sorted {
				want = append(want, p.id)
			}

			target := sectorPath(anvil, fmt.Sprintf("hangar-passes?columns=id,levelName&sort=levelName:%s&limit=2&filter=%s", direction, held))
			forward, backward, pages := walkBothWays(t, h, "marshal", target, projectedKeys("id", []string{"id", "levelName"}))
			if !slices.Equal(forward, want) {
				t.Errorf("forward walk = %v, want %v", forward, want)
			}
			if !slices.Equal(backward, want) {
				t.Errorf("backward walk = %v, want %v", backward, want)
			}
			if wantPages := (len(want) + 1) / 2; pages != wantPages {
				t.Errorf("pages = %d, want %d of two rows", pages, wantPages)
			}
		})
	}
}

// TestNameField_fixedValueNames pins that a column of fixed values sorts by what it
// shows: Anvil's missions sorted by their status's name come in the order of the
// names, not of the keys the rows store, each row carrying the name beside its key.
func TestNameField_fixedValueNames(t *testing.T) {
	t.Parallel()

	_, h, _ := sharedWorld(t)

	for _, direction := range []string{"asc", "desc"} {
		t.Run(direction, func(t *testing.T) {
			t.Parallel()

			rr := doRequestRecordedAs(t, h, "marshal", http.MethodGet, sectorPath(anvil, "missions?columns=id,statusId,statusName&sort=statusName:"+direction+"&limit=100"), "")
			assertStatus(t, rr.Code, http.StatusOK, rr.Body.Bytes())
			rows := decodeRows(t, rr.Body.Bytes())
			var names []string
			keys := make(map[string]string)
			for _, row := range rows {
				name := cell[string](t, row, "statusName")
				if key := cell[string](t, row, "statusId"); keys[key] == "" {
					keys[key] = name
				} else if keys[key] != name {
					t.Errorf("status %s is named %q and %q", key, keys[key], name)
				}
				names = append(names, name)
			}
			if len(keys) < 2 {
				t.Fatalf("the missions show %d statuses over %d rows; the order needs two", len(keys), len(rows))
			}
			inOrder := slices.IsSorted(names)
			if direction == "desc" {
				inOrder = slices.IsSortedFunc(names, func(a, b string) int { return cmp.Compare(b, a) })
			}
			if !inOrder {
				t.Errorf("missions sorted by statusName %s came with the names %v", direction, names)
			}
		})
	}
}

// namedRow is one row of a list as the name tests read it: the row's id, the id it holds
// of the row it points to, and the name the list shows beside that id, empty where the
// row shows none.
type namedRow struct {
	id   string
	key  string
	name string
}

// namedRows reads a list as the person, each row's id, the id field and the name field
// beside it; a row that leaves either out reads it as empty.
func namedRows(t *testing.T, h http.Handler, user accesstypes.User, target, idField, nameField string) []namedRow {
	t.Helper()

	var rows []namedRow
	for _, row := range listRows(t, h, user, target) {
		r := namedRow{id: cell[string](t, row, "id")}
		r.key, _ = row[idField].(string)
		r.name, _ = row[nameField].(string)
		rows = append(rows, r)
	}

	return rows
}

// heldTo is the filter that holds a list to the rows read first: other tests add passes
// to the shared world, so every check below is held to these by their ids.
func heldTo(rows []namedRow) string {
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.id)
	}

	return "id:in:(" + strings.Join(ids, ",") + ")"
}

// byName is the order of a list sorted by a name: by the name, a row that shows none
// first ascending and last descending, where the database puts NULL, and the key
// ascending among equal names, whichever way the names run.
func byName(rows []namedRow, direction string) []string {
	sorted := slices.Clone(rows)
	slices.SortFunc(sorted, func(a, b namedRow) int {
		order := cmp.Compare(a.name, b.name)
		if direction == "desc" {
			order = -order
		}

		return cmp.Or(order, cmp.Compare(a.id, b.id))
	})
	ids := make([]string, 0, len(sorted))
	for _, r := range sorted {
		ids = append(ids, r.id)
	}

	return ids
}

// idsBesideName reads the ids of a page whose rows carry the id and the name field and
// nothing else, a row that shows no name leaving the name out.
func idsBesideName(nameField string) func(t *testing.T, body []byte) []string {
	return func(t *testing.T, body []byte) []string {
		t.Helper()

		var ids []string
		for _, row := range decodeRows(t, body) {
			for column := range row {
				if column != "id" && column != nameField {
					t.Errorf("row carries %s beside id and %s: %v", column, nameField, row)
				}
			}
			ids = append(ids, cell[string](t, row, "id"))
		}

		return ids
	}
}

// walkByName walks a sector's list sorted by the name field two rows a page, forward and
// back, held to the rows given, and holds each walk to the order byName gives.
func walkByName(t *testing.T, h http.Handler, user accesstypes.User, sector, list, nameField string, rows []namedRow) {
	t.Helper()

	for _, direction := range []string{"asc", "desc"} {
		t.Run(nameField+" "+direction, func(t *testing.T) {
			want := byName(rows, direction)
			target := sectorPath(sector, fmt.Sprintf("%s?columns=id,%s&sort=%s:%s&limit=2&filter=%s", list, nameField, nameField, direction, url.QueryEscape(heldTo(rows))))
			forward, backward, pages := walkBothWays(t, h, user, target, idsBesideName(nameField))
			if !slices.Equal(forward, want) {
				t.Errorf("forward walk = %v, want %v", forward, want)
			}
			if !slices.Equal(backward, want) {
				t.Errorf("backward walk = %v, want %v", backward, want)
			}
			if wantPages := (len(want) + 1) / 2; pages != wantPages {
				t.Errorf("pages = %d, want %d of two rows", pages, wantPages)
			}
		})
	}
}

// filteredIDs is the ids of a sector's list under a filter, held to the rows given,
// sorted.
func filteredIDs(t *testing.T, h http.Handler, user accesstypes.User, sector, list, filter string, rows []namedRow) []string {
	t.Helper()

	target := sectorPath(sector, list+"?columns=id&limit=200&filter="+url.QueryEscape(heldTo(rows)+","+filter))

	return idsOf(t, listRows(t, h, user, target))
}

// idsWhere is the ids of the rows that hold, sorted.
func idsWhere(rows []namedRow, holds func(namedRow) bool) []string {
	var ids []string
	for _, r := range rows {
		if holds(r) {
			ids = append(ids, r.id)
		}
	}
	slices.Sort(ids)

	return ids
}

// namesByID reads a list's rows as the person, the name of each by its key.
func namesByID(t *testing.T, h http.Handler, user accesstypes.User, target, key, name string) map[string]string {
	t.Helper()

	names := make(map[string]string)
	for _, row := range listRows(t, h, user, target) {
		names[cell[string](t, row, key)] = cell[string](t, row, name)
	}

	return names
}

// TestNameField_passesBySquadron pins a list shown, sorted and filtered by the name of a
// row of another table. The marshal reads Anvil's hangar passes with each pass's
// squadron by name: every pass that names a squadron carries the name the squadrons'
// list gives it, and a pass that names none carries no name; the list sorted by the name
// walks two rows a page in the order of the names, two passes of Tongs crossing the
// cursor and the passes that name no squadron together where the database puts NULL;
// the squadrons' names answer contains, through the squadrons' search index; and the
// filter by the id, and a squadron's own list of its passes, give what they gave before.
func TestNameField_passesBySquadron(t *testing.T) {
	t.Parallel()

	_, h, _ := sharedWorld(t)

	squadrons := namesByID(t, h, "marshal", sectorPath(anvil, "squadrons?columns=id,name&limit=200"), "id", "name")
	passes := namedRows(t, h, "marshal", sectorPath(anvil, "hangar-passes?columns=id,squadronId,squadronName&sort=id&limit=200"), "squadronId", "squadronName")
	perSquadron := make(map[string]int)
	unnamed := 0
	for _, p := range passes {
		if p.key == "" {
			if p.name != "" {
				t.Errorf("pass %s names no squadron and shows the name %q", p.id, p.name)
			}
			unnamed++

			continue
		}
		if want := squadrons[p.key]; p.name != want {
			t.Errorf("pass %s of squadron %s shows %q, want %q, the squadron's name", p.id, p.key, p.name, want)
		}
		perSquadron[p.key]++
	}
	if len(perSquadron) < 2 || perSquadron[squadronTongsID] < 2 || unnamed == 0 {
		t.Fatalf("the seeded passes name %d squadrons, %d passes of Tongs and %d of no squadron; the walk needs two squadrons, two passes of one and one of none", len(perSquadron), perSquadron[squadronTongsID], unnamed)
	}

	walkByName(t, h, "marshal", anvil, "hangar-passes", "squadronName", passes)

	t.Run("contains, through the squadrons' search index", func(t *testing.T) {
		const part = "ong"
		want := idsWhere(passes, func(p namedRow) bool { return strings.Contains(strings.ToLower(p.name), part) })
		if len(want) == 0 || len(want) == len(passes) {
			t.Fatalf("%q is in the names of %d of %d passes; the filter needs some in and some out", part, len(want), len(passes))
		}
		if got := filteredIDs(t, h, "marshal", anvil, "hangar-passes", "squadronName:contains:"+part, passes); !slices.Equal(got, want) {
			t.Errorf("squadronName:contains:%s = %v, want %v", part, got, want)
		}
	})
	t.Run("the filter by the id, and a squadron's own list of its passes", func(t *testing.T) {
		want := idsWhere(passes, func(p namedRow) bool { return p.key == squadronTongsID })
		for _, filter := range []string{"squadronId:in:(" + squadronTongsID + ")", "squadronId:eq:" + squadronTongsID} {
			if got := filteredIDs(t, h, "marshal", anvil, "hangar-passes", filter, passes); !slices.Equal(got, want) {
				t.Errorf("%s = %v, want %v", filter, got, want)
			}
		}
	})
}

// passValue is the value of a pass on Anvil's dock, at the visit level, held by the
// field given.
func passValue(holder, id string) string {
	return fmt.Sprintf(`{"hangarId":%q,%q:%q,"levelId":"visit"}`, hangarAnvilDockID, holder, id)
}

// createPassAs writes a pass in Anvil as the person, through the route the console's
// form uses, and answers its id.
func createPassAs(t *testing.T, h http.Handler, user accesstypes.User, value string) string {
	t.Helper()

	status, body := doRequestAs(t, h, user, http.MethodPatch, consoleAPI+"/resources",
		fmt.Sprintf(`[{"op":"add","path":%q,"value":%s}]`, opPath(anvil, "hangar-passes"), value))
	assertStatus(t, status, http.StatusOK, body)
	ids, _ := decodeRow(t, body)["hangarPasses"].([]any)
	if len(ids) != 1 {
		t.Fatalf("created ids = %v, want one pass id: %s", ids, body)
	}
	id, _ := ids[0].(string)

	return id
}

// TestNameField_passesByPersonAndGroup pins the names of a pass's holder read from two
// views, the sector's people (CrewSectorUsers, known by DisplayName) and the sector's
// groups (CrewSectorGroups, known by Name). In a world of its own the marshal writes a
// pass for a person of Anvil who has no name and one for each of Anvil's two groups:
// every pass shows its person's name as the sector's people list gives it, the nameless
// person's being the username, and its group's name as the sector's groups list gives
// it; the list sorts by each name both ways; and each name filters by the whole name
// beside the ids.
func TestNameField_passesByPersonAndGroup(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	w := newIdentityWorld(t)
	username := "nameless-" + identitySuffix()
	nameless := makeCrewAccount(ctx, t, w.db, accesstypes.User(username))
	if err := w.engine.UserManager().AddUserRoles(ctx, accesstypes.DomainPolicyScope(anvil), nameless, "Armorer"); err != nil {
		t.Fatalf("access.UserManager.AddUserRoles(%s, Armorer, Anvil) error = %v", username, err)
	}
	groups := namesByID(t, w.h, "marshal", sectorPath(anvil, sectorGroupsPath+"?columns=id,name&limit=200"), "id", "name")
	if len(groups) < 2 {
		t.Fatalf("Anvil keeps %d groups; the walk needs two", len(groups))
	}
	createPassAs(t, w.h, "marshal", passValue("userId", string(nameless)))
	for id := range groups {
		createPassAs(t, w.h, "marshal", passValue("groupId", id))
	}

	people := namesByID(t, w.h, "marshal", sectorPath(anvil, "crew-sector-users?columns=id,displayName&limit=200"), "id", "displayName")
	if got := people[string(nameless)]; got != username {
		t.Errorf("the sector's people list the nameless person as %q, want the username %q", got, username)
	}
	for _, tt := range []struct {
		idField, nameField string
		names              map[string]string
		accepted           []string
	}{
		{idField: "userId", nameField: "userName", names: people, accepted: []string{username}},
		{idField: "groupId", nameField: "groupName", names: groups},
	} {
		passes := namedRows(t, w.h, "marshal", sectorPath(anvil, "hangar-passes?columns=id,"+tt.idField+","+tt.nameField+"&sort=id&limit=200"), tt.idField, tt.nameField)
		held := make(map[string]int)
		for _, p := range passes {
			if want := tt.names[p.key]; p.name != want {
				t.Errorf("pass %s holding %s %q shows %s %q, want %q", p.id, tt.idField, p.key, tt.nameField, p.name, want)
			}
			if p.name != "" {
				held[p.name]++
			}
		}
		for _, name := range tt.accepted {
			if held[name] == 0 {
				t.Errorf("no pass shows %s %q", tt.nameField, name)
			}
		}
		if len(held) < 2 {
			t.Fatalf("the passes show %d names of %s; the walk needs two", len(held), tt.nameField)
		}

		walkByName(t, w.h, "marshal", anvil, "hangar-passes", tt.nameField, passes)

		for name := range held {
			want := idsWhere(passes, func(p namedRow) bool { return p.name == name })
			filter := fmt.Sprintf("%s:eq:%q", tt.nameField, name)
			if got := filteredIDs(t, w.h, "marshal", anvil, "hangar-passes", filter, passes); !slices.Equal(got, want) {
				t.Errorf("%s = %v, want %v", filter, got, want)
			}
		}
	}
}

// TestNameField_aSquadronOfAnotherSector pins that a name is read in the request's own
// tenant: a pass of Anvil that names a squadron of Bastion, written straight into the
// table since the foreign key holds no sector and nothing refuses it, shows its
// squadron's id and no name to Anvil's marshal, and matches squadronName:isnull with the
// passes that name no squadron.
func TestNameField_aSquadronOfAnotherSector(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	w := newIdentityWorld(t)
	stray := impulse.Must(impulse.NewUUID()).String()
	if _, err := w.db.Apply(ctx, []*spanner.Mutation{spanner.InsertMap("HangarPasses", map[string]any{
		"Id": stray, "SectorId": anvil, "HangarId": hangarAnvilDockID, "SquadronId": squadronPortcullisID, "LevelId": "visit",
	})}); err != nil {
		t.Fatalf("writing a pass of Anvil for Portcullis: %v", err)
	}

	passes := namedRows(t, w.h, "marshal", sectorPath(anvil, "hangar-passes?columns=id,squadronId,squadronName&sort=id&limit=200"), "squadronId", "squadronName")
	i := slices.IndexFunc(passes, func(p namedRow) bool { return p.id == stray })
	if i < 0 {
		t.Fatalf("Anvil's passes %v hold no pass %s", passes, stray)
	}
	if got := passes[i]; got.key != squadronPortcullisID || got.name != "" {
		t.Errorf("the pass for Portcullis shows squadronId %q and squadronName %q, want %s and no name", got.key, got.name, squadronPortcullisID)
	}
	want := idsWhere(passes, func(p namedRow) bool { return p.name == "" })
	if got := filteredIDs(t, w.h, "marshal", anvil, "hangar-passes", "squadronName:isnull", passes); !slices.Equal(got, want) || !slices.Contains(got, stray) {
		t.Errorf("squadronName:isnull = %v, want %v, the pass for Portcullis among them", got, want)
	}
}

// namesHeader reads the lists an answer says its request registered for its names
// (live.NamesHeader); nil where it names none.
func namesHeader(t *testing.T, header http.Header) *live.Names {
	t.Helper()

	value := header.Get(live.NamesHeader)
	if value == "" {
		return nil
	}
	decoded, err := url.PathUnescape(value)
	if err != nil {
		t.Fatalf("url.PathUnescape(%s) error = %v", live.NamesHeader, err)
	}
	var names live.Names
	if err := json.Unmarshal([]byte(decoded), &names); err != nil {
		t.Fatalf("json.Unmarshal(%s) error = %v", live.NamesHeader, err)
	}

	return &names
}

// TestNameField_aRenameTellsTheTab pins that an open list is told when a name it shows
// changes. The marshal's tab holds Anvil's passes by the squadron's name, and the
// governor's Bastion's: each answer names the squadrons' list of its own sector as the
// list the names are read from, and once a squadron of Anvil is renamed the marshal's
// change set carries Anvil's list of squadrons and the governor's carries none. A tab
// that holds the passes by the person's name registers the sector's people, and a person
// renamed reaches it: the users table belongs to no sector, so the people lists of every
// sector are told.
func TestNameField_aRenameTellsTheTab(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	fake := live.NewFake()
	w := newIdentityWorldWithLive(t, fake)
	subscribe := func(user accesstypes.User, tab, sector, columns string) *live.Names {
		t.Helper()

		rr := doRequestRecordedWithHeaders(t, w.h, user, http.MethodGet, versioned(sectorPath(sector, "hangar-passes"), liveSeed)+"&columns="+columns, "", liveHeaders(tab))
		assertStatus(t, rr.Code, http.StatusOK, rr.Body.Bytes())

		return namesHeader(t, rr.Header())
	}
	squadronsOf := func(sector string) *live.Names {
		return &live.Names{
			Of:    live.SubscriptionRequest{Resource: "HangarPasses", Domain: sector},
			Names: []live.SubscriptionRequest{{Resource: "Squadrons", Domain: sector, NameField: "name"}},
		}
	}
	for _, tt := range []struct {
		user        accesstypes.User
		tab, sector string
	}{{user: "marshal", tab: liveTabAnvil, sector: anvil}, {user: "governor", tab: liveTabBastion, sector: bastion}} {
		if got, want := subscribe(tt.user, tt.tab, tt.sector, "id,squadronName"), squadronsOf(tt.sector); !namesEqual(got, want) {
			t.Errorf("%s's answer names %+v, want %+v", tt.user, got, want)
		}
	}

	status, body := doRequestAs(t, w.h, "marshal", http.MethodPatch, consoleAPI+"/resources",
		fmt.Sprintf(`[{"op":"patch","path":%q,"value":{"name":"Hammer Two"}}]`, opPath(anvil, "squadrons/"+squadronHammerID)))
	assertStatus(t, status, http.StatusOK, body)
	if got := changeNames(fake.Changes(principalID("marshal"))); !slices.Contains(got, "list|Squadrons||"+anvil+"|false") {
		t.Errorf("the marshal's change set = %v, want Anvil's list of squadrons in it", got)
	}
	for _, change := range changeNames(fake.Changes(principalID("governor"))) {
		if strings.Contains(change, "|Squadrons|") {
			t.Errorf("the governor's change set holds %s: Bastion's tab is told of a squadron of Anvil", change)
		}
	}

	person := w.seedCrewUser(ctx, t, "renamed-"+identitySuffix())
	if err := w.engine.UserManager().AddUserRoles(ctx, accesstypes.DomainPolicyScope(anvil), accesstypes.User(person.String()), "Armorer"); err != nil {
		t.Fatalf("access.UserManager.AddUserRoles(Armorer, Anvil) error = %v", err)
	}
	want := &live.Names{
		Of:    live.SubscriptionRequest{Resource: "HangarPasses", Domain: anvil},
		Names: []live.SubscriptionRequest{{Resource: "CrewSectorUsers", Domain: anvil, NameField: "displayName"}},
	}
	if got := subscribe("marshal", "tab-anvil-people", anvil, "id,userName"); !namesEqual(got, want) {
		t.Errorf("the marshal's answer by the person's name names %+v, want %+v", got, want)
	}
	status, body = doRequestAs(t, w.h, "governor", http.MethodPost, consoleAPI+"/set-crew-user-name", fmt.Sprintf(`{"userId":%q,"name":"Renamed Rhea"}`, person))
	assertStatus(t, status, http.StatusNoContent, body)
	if got := changeNames(fake.Changes(principalID("marshal"))); !slices.Contains(got, "resource|CrewSectorUsers|||false") {
		t.Errorf("the marshal's change set = %v, want the sector's people in it", got)
	}
}

// namesEqual reports whether two answers name the same lists.
func namesEqual(got, want *live.Names) bool {
	if got == nil || want == nil {
		return got == want
	}

	return got.Of == want.Of && slices.Equal(got.Names, want.Names)
}

// TestNameField_rostersAndAssignments pins the two views over the squadron memberships
// that joined names of their own and now carry the ids alone: the squadron rosters and
// the pilot assignments of Anvil, as the marshal reads them, show each squadron's name
// as the squadrons' list gives it and each pilot's as the pilots' list gives it, the
// values the views joined; each sorts by the name; and the squadron's name filters.
func TestNameField_rostersAndAssignments(t *testing.T) {
	t.Parallel()

	_, h, _ := sharedWorld(t)

	squadrons := namesByID(t, h, "marshal", sectorPath(anvil, "squadrons?columns=id,name&limit=200"), "id", "name")
	pilots := namesByID(t, h, "marshal", consoleAPI+"/pilots?columns=id,displayName&limit=all", "id", "displayName")
	for _, tt := range []struct {
		list    string
		columns string
		names   map[string]map[string]string // the name field: the id field beside it and the names it reads
	}{
		{list: "squadron-rosters", columns: "squadronId,userId,squadronName,pilotId,pilotName", names: map[string]map[string]string{"squadronName": nil, "pilotName": pilots}},
		{list: "pilot-assignments", columns: "squadronId,userId,squadronName,wingName", names: map[string]map[string]string{"squadronName": squadrons}},
	} {
		rows := listRows(t, h, "marshal", sectorPath(anvil, tt.list+"?columns="+tt.columns+"&limit=200"))
		if len(rows) < 2 {
			t.Fatalf("%s holds %d rows of Anvil; the checks need two", tt.list, len(rows))
		}
		for nameField, names := range tt.names {
			if names == nil {
				// The roster's squadron name is its own column, joined as before.
				names = squadrons
			}
			idField := strings.TrimSuffix(nameField, "Name") + "Id"
			for _, row := range rows {
				id, _ := row[idField].(string)
				name, _ := row[nameField].(string)
				if want := names[id]; name != want {
					t.Errorf("%s row %v shows %s %q, want %q", tt.list, row, nameField, name, want)
				}
			}
			for _, direction := range []string{"asc", "desc"} {
				sorted := listRows(t, h, "marshal", sectorPath(anvil, tt.list+"?columns="+tt.columns+"&limit=200&sort="+nameField+":"+direction))
				var shown []string
				for _, row := range sorted {
					name, _ := row[nameField].(string)
					shown = append(shown, name)
				}
				inOrder := slices.IsSorted(shown)
				if direction == "desc" {
					inOrder = slices.IsSortedFunc(shown, func(a, b string) int { return cmp.Compare(b, a) })
				}
				if !inOrder || len(sorted) != len(rows) {
					t.Errorf("%s sorted by %s %s came with the names %v over %d rows, want them in order over %d", tt.list, nameField, direction, shown, len(sorted), len(rows))
				}
			}
		}
		filtered := listRows(t, h, "marshal", sectorPath(anvil, tt.list+"?columns="+tt.columns+"&limit=200&filter="+url.QueryEscape(`squadronName:eq:"Tongs"`)))
		want := 0
		for _, row := range rows {
			if row["squadronId"] == squadronTongsID {
				want++
			}
		}
		if len(filtered) != want || want == 0 {
			t.Errorf("%s filtered by squadronName Tongs gives %d rows, want %d, Tongs' own", tt.list, len(filtered), want)
		}
		for _, row := range filtered {
			if row["squadronId"] != squadronTongsID {
				t.Errorf("%s filtered by squadronName Tongs gives %v", tt.list, row)
			}
		}
	}
}
