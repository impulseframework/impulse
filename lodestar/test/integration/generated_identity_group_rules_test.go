// Demonstrates: auth.groups, auth.periods, auth.tenant-methods, execute-condition.request-field.
package integration

// generated_identity_group_rules_test: a group gives nobody a power that giving the role
// directly would not.
//
// A change to a group is a change to what people hold, so the generated management
// judges it as the role changes it makes, beside the grant of the group's own method:
// a person is put in a group by someone who may give them each role the group holds,
// and taken out by someone who may take each away. A sector puts its own people in its
// groups, and an account from outside it is put there by headquarters. Nobody makes
// what they hold themselves last longer than it was given for. And a sector's list of
// the roles held through groups shows, of a role a group holds two ways, the one that
// counts.
//
// The suite drives the methods as served, against the real engine over the shipped role
// file, whose grants on the methods that change a group's people carry no condition:
// what holds a sector's Marshal here is the grant on the method that gives a role.

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
)

// seatMarshal has headquarters give a person the Marshal's role at Anvil: the cast's
// Marshal has no account in the worlds of this suite, and a rule about what a person
// does to themselves needs a Marshal who has one.
func seatMarshal(t *testing.T, w *tenantListsWorld, person string) {
	t.Helper()

	status, body := doRequestAs(t, w.h, "governor", http.MethodPost, consoleAPI+"/assign-crew-role",
		fmt.Sprintf(`{"userId":%q,"role":"SectorMarshal","domain":%q}`, w.ids[person], anvil))
	assertStatus(t, status, http.StatusNoContent, body)
}

// requireNoCondition fails unless the role file grants the role the permission on the
// resource with no condition: the suite's refusals are then the rule's, and no
// condition of the role file's on the method under test.
func requireNoCondition(t *testing.T, role accesstypes.Role, permission accesstypes.Permission, res accesstypes.Resource) {
	t.Helper()

	config, err := crew.Roles().Parse()
	if err != nil {
		t.Fatalf("access.RoleFile.Parse() error = %v", err)
	}
	for _, r := range slices.Concat(config.Roles.Global, config.Roles.Domain) {
		if r.Name != role {
			continue
		}
		for _, g := range r.Permissions[permission] {
			if g.Resource == res && g.Condition == "" {
				return
			}
		}
	}
	t.Fatalf("the role file does not grant %s %s on %s with no condition, which this suite reads its refusals against", role, permission, res)
}

// TestGeneratedIdentityGroupMembersAreTheSectorsPeople holds who a sector puts in its
// groups: a person the sector already has. An account that is not one of its people is
// answered as a person not found there, as an id no account holds is, whoever asks under
// the sector's address; headquarters puts such an account in the sector's group through
// the whole fleet's form.
func TestGeneratedIdentityGroupMembersAreTheSectorsPeople(t *testing.T) {
	t.Parallel()
	w := newTenantListsWorld(t)

	post := func(user accesstypes.User, method, body string) (int, []byte) {
		return doRequestAs(t, w.h, user, http.MethodPost, sectorPath(anvil, method), body)
	}
	status, body := post("marshal", "create-crew-sector-group", `{"name":"Hangar hands","description":"The crew who work Anvil's hangar deck."}`)
	assertStatus(t, status, http.StatusOK, body)
	group := cell[string](t, decodeRow(t, body), "id")
	member := func(id string) string { return fmt.Sprintf(`{"groupId":%q,"userId":%q}`, group, id) }
	inGroup := func() []string {
		var names []string
		for _, row := range decodeRows(t, mustList(t, w, "marshal", sectorGroupPeoplePath)) {
			if cell[string](t, row, "groupId") == group {
				names = append(names, cell[string](t, row, "username"))
			}
		}
		slices.Sort(names)

		return names
	}

	// Di is a Veteran at Bastion and holds a role for the whole fleet. She holds nothing
	// at Anvil, so she is none of its people, and its Marshal does not bring her in.
	di := w.ids["di"]
	if _, listed := sectorList(t, w.h, "marshal", anvil, sectorPeoplePath)[di]; listed {
		t.Fatal("di is one of Anvil's people before anything is given, and the case needs an account from outside the sector")
	}
	status, refused := post("marshal", "add-crew-sector-group-member", member(di))
	assertStatus(t, status, http.StatusNotFound, refused)

	// An id no account holds is answered the same, so the answer says nothing of whether
	// an id is an account somewhere else.
	const nobody = "99999999-9999-4999-8999-999999999999"
	status, unknown := post("marshal", "add-crew-sector-group-member", member(nobody))
	assertStatus(t, status, http.StatusNotFound, unknown)
	if got, want := strings.ReplaceAll(string(refused), di, "ID"), strings.ReplaceAll(string(unknown), nobody, "ID"); got != want {
		t.Errorf("an account from outside the sector is answered %s, and an id no account holds %s: the two differ beyond the id", got, want)
	}

	// The sector's form takes the sector's people whoever asks under its address.
	status, body = post("governor", "add-crew-sector-group-member", member(di))
	assertStatus(t, status, http.StatusNotFound, body)
	if got := inGroup(); len(got) != 0 {
		t.Fatalf("the group's people after the refusals = %v, want none", got)
	}

	// Headquarters, which manages the whole fleet's people, puts her in the sector's
	// group through the whole fleet's form. She is one of Anvil's people from then on.
	status, body = doRequestAs(t, w.h, "governor", http.MethodPost, consoleAPI+"/add-crew-group-member", member(di))
	assertStatus(t, status, http.StatusNoContent, body)
	if _, listed := sectorList(t, w.h, "marshal", anvil, sectorPeoplePath)[di]; !listed {
		t.Error("Anvil's people leave out di, whom headquarters put in one of its groups")
	}

	// A person the sector has is put in its group by its Marshal, and so is di now.
	status, body = post("marshal", "add-crew-sector-group-member", member(w.ids["ada"]))
	assertStatus(t, status, http.StatusNoContent, body)
	status, body = post("marshal", "add-crew-sector-group-member", member(di))
	assertStatus(t, status, http.StatusNoContent, body)
	if got, want := inGroup(), []string{"ada", "di"}; !slices.Equal(got, want) {
		t.Errorf("the group's people = %v, want %v", got, want)
	}
}

// mustList lists one of Anvil's lists as the user and answers the body.
func mustList(t *testing.T, w *tenantListsWorld, user accesstypes.User, path string) []byte {
	t.Helper()

	status, body := doRequestAs(t, w.h, user, http.MethodGet, sectorPath(anvil, path+"?limit=200"), "")
	assertStatus(t, status, http.StatusOK, body)

	return body
}

// TestGeneratedIdentityGroupChangesAreJudgedAsRoleChanges holds a change to who is in a
// group to what the role changes it makes need. Anvil's Marshal gives and takes every
// role of her sector but the Marshal's own (the conditions on the two grants below), and
// the role file grants her the two methods that change a group's people with no
// condition. So she puts a person in, and takes one out of, a group that holds roles she
// may give, and neither for a group that holds the Marshal's role: a Marshal puts there
// neither another person nor themselves. Headquarters, which gives the Marshal's role,
// still does both.
func TestGeneratedIdentityGroupChangesAreJudgedAsRoleChanges(t *testing.T) {
	t.Parallel()
	w := newTenantListsWorld(t)

	// The grants this suite proves, pinned to the roles file (conditions-proven), and the
	// ones it needs to carry no condition.
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "AssignCrewSectorRole", "new.role != 'SectorMarshal'")
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "RemoveCrewSectorRole", "role != 'SectorMarshal' AND everyDomain = false")
	requireNoCondition(t, "SectorMarshal", "Execute", "AddCrewSectorGroupMember")
	requireNoCondition(t, "SectorMarshal", "Execute", "RemoveCrewSectorGroupMember")

	post := func(user accesstypes.User, method, body string) (int, []byte) {
		return doRequestAs(t, w.h, user, http.MethodPost, sectorPath(anvil, method), body)
	}
	hq := func(method, body string) (int, []byte) {
		return doRequestAs(t, w.h, "governor", http.MethodPost, consoleAPI+"/"+method, body)
	}
	makeGroup := func(name string) string {
		t.Helper()
		status, body := post("marshal", "create-crew-sector-group", fmt.Sprintf(`{"name":%q,"description":"A group of Anvil's."}`, name))
		assertStatus(t, status, http.StatusOK, body)

		return cell[string](t, decodeRow(t, body), "id")
	}
	member := func(group, id string) string { return fmt.Sprintf(`{"groupId":%q,"userId":%q}`, group, id) }
	ada := w.ids["ada"]
	marshalHolds := func(person string) bool {
		_, held := sectorRolesThroughGroups(t, w)[person+" SectorMarshal"]

		return held
	}

	// The bridge watch holds the Marshal's role at Anvil, which headquarters gave it: the
	// Marshal may not give the role to a group, as she may not give it to a person.
	bridge := makeGroup("Bridge watch")
	status, body := post("marshal", "give-crew-sector-group-role", fmt.Sprintf(`{"groupId":%q,"role":"SectorMarshal"}`, bridge))
	assertStatus(t, status, http.StatusForbidden, body)
	status, body = hq("give-crew-group-role", fmt.Sprintf(`{"groupId":%q,"role":"SectorMarshal","domain":%q}`, bridge, anvil))
	assertStatus(t, status, http.StatusNoContent, body)

	// Putting a person in the bridge watch gives them the Marshal's role. A Marshal may
	// not give it, so she puts nobody there: the Marshal does not put a Cadet there, and
	// eve, whom headquarters made a Marshal of Anvil, does not put herself there.
	seatMarshal(t, w, "eve")
	for _, tt := range []struct {
		actor  accesstypes.User
		person string
	}{
		{actor: "marshal", person: ada},
		{actor: "eve", person: w.ids["eve"]},
	} {
		status, body = post(tt.actor, "add-crew-sector-group-member", member(bridge, tt.person))
		assertStatus(t, status, http.StatusForbidden, body)
		if said := refusalMessage(t, body); !strings.HasSuffix(said, " gives them the role Sector marshal, which you may not give them.") || strings.Contains(said, "SectorMarshal") {
			t.Errorf("the refusal of a place in the bridge watch, asked by %s = %s, want one sentence of the role a Marshal may not give, named in words", tt.actor, body)
		}
	}
	// Under the sector's address headquarters' governor is one more Marshal, of every
	// sector, and is held to what giving the role there needs as she is.
	status, body = post("governor", "add-crew-sector-group-member", member(bridge, ada))
	assertStatus(t, status, http.StatusForbidden, body)
	if marshalHolds("ada") {
		t.Fatal("ada holds the Marshal's role through the bridge watch after every request to put her there was refused")
	}

	// Headquarters gives the Marshal's role to a person through the whole fleet's form,
	// and so puts a person in the bridge watch through it.
	status, body = hq("add-crew-group-member", member(bridge, ada))
	assertStatus(t, status, http.StatusNoContent, body)
	if !marshalHolds("ada") {
		t.Fatal("ada does not hold the Marshal's role through the bridge watch, where headquarters put her")
	}

	// Taking her out takes the Marshal's role from her, which the Marshal may not do to a
	// fellow Marshal. Headquarters does.
	status, body = post("marshal", "remove-crew-sector-group-member", member(bridge, ada))
	assertStatus(t, status, http.StatusForbidden, body)
	if said := refusalMessage(t, body); !strings.HasSuffix(said, " takes away the role Sector marshal, which you may not take from them.") || strings.Contains(said, "SectorMarshal") {
		t.Errorf("the refusal to take ada out of the bridge watch = %s, want one sentence of the role the Marshal may not take away, named in words", body)
	}
	if !marshalHolds("ada") {
		t.Fatal("ada lost the Marshal's role through the bridge watch to a request that was refused")
	}
	status, body = hq("remove-crew-group-member", member(bridge, ada))
	assertStatus(t, status, http.StatusNoContent, body)
	if marshalHolds("ada") {
		t.Error("ada holds the Marshal's role through the bridge watch after headquarters took her out")
	}

	// The hangar hands hold roles the Marshal may give, so who is in the group is hers
	// to say.
	hangar := makeGroup("Hangar hands")
	for _, role := range []string{"Engineer", "SectorCrew"} {
		status, body = post("marshal", "give-crew-sector-group-role", fmt.Sprintf(`{"groupId":%q,"role":%q}`, hangar, role))
		assertStatus(t, status, http.StatusNoContent, body)
	}
	status, body = post("marshal", "add-crew-sector-group-member", member(hangar, ada))
	assertStatus(t, status, http.StatusNoContent, body)
	if _, held := sectorRolesThroughGroups(t, w)["ada Engineer"]; !held {
		t.Error("ada does not hold the Engineer's role through the hangar hands, where the Marshal put her")
	}
	status, body = post("marshal", "remove-crew-sector-group-member", member(hangar, ada))
	assertStatus(t, status, http.StatusNoContent, body)
	if _, held := sectorRolesThroughGroups(t, w)["ada Engineer"]; held {
		t.Error("ada holds the Engineer's role through the hangar hands after the Marshal took her out")
	}
}

// TestGeneratedIdentityGroupChangesReadThePersonsRow holds the same where the grant on
// the method that gives a role reads the person it is given to, and not the role alone.
// A custom role of Anvil's gives roles to people who are not the fleet's own (the fact
// heldGlobally on the person's row of the sector's people), and the role file knows
// nothing of it. Its holder puts in a group that holds a role the people she may give
// the role to directly, and nobody else.
func TestGeneratedIdentityGroupChangesReadThePersonsRow(t *testing.T) {
	t.Parallel()
	w := newTenantListsWorld(t)
	ctx := t.Context()

	const seater accesstypes.Role = "Seater"
	held := accesstypes.DomainPolicyScope(anvil)
	manager := w.engine.UserManager()
	if err := manager.AddRole(ctx, held, seater, "Seats Anvil's own crew."); err != nil {
		t.Fatalf("access.UserManager.AddRole() error = %v", err)
	}
	if err := manager.AddRoleGrants(ctx, held, seater,
		access.GrantRow{Permission: accesstypes.Execute, Resource: "AssignCrewSectorRole", Condition: "new.role != 'SectorMarshal' AND heldGlobally = false"},
		access.GrantRow{Permission: accesstypes.Execute, Resource: "AddCrewSectorGroupMember", Condition: ""},
	); err != nil {
		t.Fatalf("access.UserManager.AddRoleGrants() error = %v", err)
	}
	ada := accesstypes.User(w.ids["ada"])
	if err := manager.AddUserRoles(ctx, held, ada, seater); err != nil {
		t.Fatalf("access.UserManager.AddUserRoles() error = %v", err)
	}
	// The store writes signal a reload and the swap is asynchronous: once the grant with
	// no condition shows, every answer below reads a policy that holds the whole role.
	if d := decisionFor(ctx, t, w.engine.ForUser(ada), accesstypes.DomainScope(anvil), accesstypes.Execute, "AddCrewSectorGroupMember", time.Now().Add(15*time.Second)); !d.IsGranted() {
		t.Fatalf("Execute on AddCrewSectorGroupMember for ada, who holds %s = %s, want granted before the cases run", seater, d)
	}

	post := func(user accesstypes.User, method, body string) (int, []byte) {
		return doRequestAs(t, w.h, user, http.MethodPost, sectorPath(anvil, method), body)
	}
	status, body := post("marshal", "create-crew-sector-group", `{"name":"Hangar hands","description":"The crew who work Anvil's hangar deck."}`)
	assertStatus(t, status, http.StatusOK, body)
	hangar := cell[string](t, decodeRow(t, body), "id")
	status, body = post("marshal", "give-crew-sector-group-role", fmt.Sprintf(`{"groupId":%q,"role":"Engineer"}`, hangar))
	assertStatus(t, status, http.StatusNoContent, body)

	// Fay holds a role for the whole fleet, and bo holds none: he is a Pilot of two
	// sectors. The grant on the method that gives a role tells them apart.
	people := sectorList(t, w.h, "marshal", anvil, sectorPeoplePath)
	if !cell[bool](t, people[w.ids["fay"]], "heldGlobally") || cell[bool](t, people[w.ids["bo"]], "heldGlobally") {
		t.Fatalf("heldGlobally of fay and of bo = %v, %v, want true and false", people[w.ids["fay"]]["heldGlobally"], people[w.ids["bo"]]["heldGlobally"])
	}
	for _, tt := range []struct {
		person string
		want   int
	}{
		{person: "fay", want: http.StatusForbidden},
		{person: "bo", want: http.StatusNoContent},
	} {
		// The role given directly, and the same role given through the group.
		status, body = post("ada", "assign-crew-sector-role", fmt.Sprintf(`{"userId":%q,"role":"Harbormaster"}`, w.ids[tt.person]))
		assertStatus(t, status, tt.want, body)
		status, body = post("ada", "add-crew-sector-group-member", fmt.Sprintf(`{"groupId":%q,"userId":%q}`, hangar, w.ids[tt.person]))
		assertStatus(t, status, tt.want, body)
		if tt.want == http.StatusForbidden && !strings.HasSuffix(refusalMessage(t, body), " gives them the role Engineer, which you may not give them.") {
			t.Errorf("the refusal to put %s in the hangar hands = %s, want the sentence of the role ada may not give them", tt.person, body)
		}
		if _, through := sectorRolesThroughGroups(t, w)[tt.person+" Engineer"]; through != (tt.want == http.StatusNoContent) {
			t.Errorf("%s holds the Engineer's role through the hangar hands: %v, and the request was answered %d", tt.person, through, tt.want)
		}
	}
}

// sectorRolesThroughGroups lists the roles Anvil's people hold through a group, as the
// Marshal reads them, each keyed by the person's username and the role.
func sectorRolesThroughGroups(t *testing.T, w *tenantListsWorld) map[string]map[string]any {
	t.Helper()

	rows := make(map[string]map[string]any)
	for _, row := range decodeRows(t, mustList(t, w, "marshal", sectorRolesThroughGroupPath)) {
		rows[cell[string](t, row, "username")+" "+cell[string](t, row, "role")] = row
	}

	return rows
}

// TestGeneratedIdentityNobodyLengthensTheirOwnHolding holds the rule on one's own
// period: a person who may give a role and holds it until a day does not give it to
// themselves for longer. They shorten their own, and give another person any period.
func TestGeneratedIdentityNobodyLengthensTheirOwnHolding(t *testing.T) {
	t.Parallel()
	w := newTenantListsWorld(t)

	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "AssignCrewSectorRole", "new.role != 'SectorMarshal'")

	assign := func(user accesstypes.User, person, role string, endsAt time.Time) (int, []byte) {
		return doRequestAs(t, w.h, user, http.MethodPost, sectorPath(anvil, "assign-crew-sector-role"),
			periodBody(t, map[string]any{"userId": person, "role": role}, time.Time{}, endsAt))
	}
	week := time.Now().UTC().Add(7 * 24 * time.Hour).Truncate(time.Microsecond)
	held := func(person string) accesstypes.Period {
		t.Helper()
		row := sectorRoles(t, w.h, "marshal", anvil)[person+"/Engineer"]
		if row == nil {
			t.Fatalf("the roles held at Anvil leave out the Engineer's role of %s", person)
		}

		return periodOf(t, row)
	}

	// Headquarters makes eve a Marshal of Anvil and gives her the Engineer's role there
	// for a week. It is a role a Marshal may give, so the grant alone would let her give
	// it to herself for good.
	eve := w.ids["eve"]
	seatMarshal(t, w, "eve")
	status, body := doRequestAs(t, w.h, "governor", http.MethodPost, consoleAPI+"/assign-crew-role",
		periodBody(t, map[string]any{"userId": eve, "role": "Engineer", "domain": anvil}, time.Time{}, week))
	assertStatus(t, status, http.StatusNoContent, body)

	for name, endsAt := range map[string]time.Time{"with no end": {}, "until a later day": week.Add(24 * time.Hour)} {
		status, body = assign("eve", eve, "Engineer", endsAt)
		assertStatus(t, status, http.StatusForbidden, body)
		if !strings.Contains(string(body), "you cannot make your own role last longer than it was given for") {
			t.Errorf("the refusal of eve's own role %s = %s, want it to say nobody lengthens their own", name, body)
		}
	}
	if got := held(eve); !got.End.Equal(week) {
		t.Fatalf("eve's own role ends %s after the refusals, want the week it was given for (%s)", got.End, week)
	}

	// She gives it up sooner, and gives another person the role for as long as she likes.
	sooner := week.Add(-24 * time.Hour)
	status, body = assign("eve", eve, "Engineer", sooner)
	assertStatus(t, status, http.StatusNoContent, body)
	if got := held(eve); !got.End.Equal(sooner) {
		t.Errorf("eve's own role ends %s, want the sooner day she gave it (%s)", got.End, sooner)
	}
	status, body = assign("eve", w.ids["ada"], "Engineer", week)
	assertStatus(t, status, http.StatusNoContent, body)
	status, body = assign("eve", w.ids["ada"], "Engineer", time.Time{})
	assertStatus(t, status, http.StatusNoContent, body)
	if got := held(w.ids["ada"]); !got.End.IsZero() {
		t.Errorf("ada's role ends %s, want no end: eve gave it to her for good", got.End)
	}

	// Another Marshal, who is not eve, gives eve's role a longer period.
	status, body = assign("marshal", eve, "Engineer", time.Time{})
	assertStatus(t, status, http.StatusNoContent, body)
	if got := held(eve); !got.End.IsZero() {
		t.Errorf("eve's own role ends %s, want no end: the Marshal gave it to her for good", got.End)
	}
}

// TestGeneratedIdentityRolesThroughGroupsShowTheOneThatCounts holds a sector's list of
// the roles held through groups where a group of the whole fleet holds a role both in
// the sector and in every sector: it is one row, the one that counts now. A row of the
// sector's own that has not begun does not hide the role the person holds through every
// sector today.
func TestGeneratedIdentityRolesThroughGroupsShowTheOneThatCounts(t *testing.T) {
	t.Parallel()
	w := newTenantListsWorld(t)

	hq := func(method, body string) {
		t.Helper()
		status, answer := doRequestAs(t, w.h, "governor", http.MethodPost, consoleAPI+"/"+method, body)
		assertStatus(t, status, http.StatusNoContent, answer)
	}
	status, body := doRequestAs(t, w.h, "governor", http.MethodPost, consoleAPI+"/create-crew-group", `{"name":"Fleet inspectors","description":"Headquarters' inspectors."}`)
	assertStatus(t, status, http.StatusOK, body)
	group := cell[string](t, decodeRow(t, body), "id")
	hq("add-crew-group-member", fmt.Sprintf(`{"groupId":%q,"userId":%q}`, group, w.ids["bo"]))

	later := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	atCinder := map[string]any{"groupId": group, "role": "Engineer", "domain": cinder}
	everywhere := map[string]any{"groupId": group, "role": "Engineer", "everyDomain": true}
	// shown answers the rows of bo's Engineer role through the group at Cinder: for each,
	// whether it is the one held in every sector and whether it has begun.
	shown := func() []string {
		t.Helper()
		status, body := doRequestAs(t, w.h, "governor", http.MethodGet, sectorPath(cinder, sectorRolesThroughGroupPath+"?limit=200"), "")
		assertStatus(t, status, http.StatusOK, body)
		var rows []string
		for _, row := range decodeRows(t, body) {
			if cell[string](t, row, "groupId") != group || cell[string](t, row, "role") != "Engineer" {
				continue
			}
			where, when := "at Cinder", "counts"
			if cell[bool](t, row, "everyDomain") {
				where = "in every sector"
			}
			if periodOf(t, row).Upcoming(time.Now()) {
				when = "not begun"
			}
			rows = append(rows, where+", "+when)
		}

		return rows
	}
	cinderRefits := func() int {
		status, _ := doRequestAs(t, w.h, "bo", http.MethodGet, sectorPath(cinder, "refits"), "")

		return status
	}

	steps := []struct {
		name   string
		give   map[string]any
		from   time.Time
		want   []string
		refits int
	}{
		{name: "held in every sector alone", give: everywhere, want: []string{"in every sector, counts"}, refits: http.StatusOK},
		{name: "the sector's own row has not begun: the one held in every sector is shown", give: atCinder, from: later, want: []string{"in every sector, counts"}, refits: http.StatusOK},
		{name: "both count: the sector's own row", give: atCinder, want: []string{"at Cinder, counts"}, refits: http.StatusOK},
		{name: "the one held in every sector has not begun: the sector's own row", give: everywhere, from: later, want: []string{"at Cinder, counts"}, refits: http.StatusOK},
		{name: "neither has begun: the sector's own row", give: atCinder, from: later, want: []string{"at Cinder, not begun"}, refits: http.StatusNotFound},
	}
	for _, step := range steps {
		hq("give-crew-group-role", periodBody(t, step.give, step.from, time.Time{}))
		if got := shown(); !slices.Equal(got, step.want) {
			t.Errorf("%s: Cinder's list shows %v, want %v", step.name, got, step.want)
		}
		if got := cinderRefits(); got != step.refits {
			t.Errorf("%s: bo lists Cinder's refits with status %d, want %d", step.name, got, step.refits)
		}
	}
}
