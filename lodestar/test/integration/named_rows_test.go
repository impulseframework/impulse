package integration

// named_rows_test: a column picked from a view or from a computed list, held to the rows its
// writer can list, as a column naming a table's row is (TestRecordRoles_aRowAWriteNames), and
// a method's picked field, held to the rows its caller can list. A role of the crew's file
// that sets such a column or runs such a method lists what it picks from, or is refused the
// write by its own grant (a mission's lead and its client); the refusal of a writer who
// lists nothing is shown with a role made in the test.
// A mission's client is picked from the client roster, a view of every client in every
// sector; a hangar pass's person and group are picked from the sector's people and its
// groups, the views of the crew's generated management; a mission's briefing sheet is
// picked from the briefing catalog, a list Go serves. A create or an update that sets one
// of them to a row the writer's own list would not show is answered as a row that is not
// there, and a value the row already holds is not asked again. A method's request that
// names, in a picked field, a row its caller's list would not show is answered the same way,
// before the method runs.

import (
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
)

// referredRowMissing is the library's answer to a write that names a row its writer
// cannot list.
const referredRowMissing = "A record this one refers to does not exist."

// namedRowsWorld is the sharing world with the requests the tests of this file make.
type namedRowsWorld struct {
	*sharingWorld
}

// write sends one operation of a patch as the person, on a path of Anvil's.
func (w *namedRowsWorld) write(user accesstypes.User, op, path, value string) (status int, body []byte) {
	w.t.Helper()

	return w.as(user, http.MethodPatch, consoleAPI+"/resources", fmt.Sprintf(`[{"op":%q,"path":%q,"value":%s}]`, op, opPath(anvil, path), value))
}

// notThere holds an answer to the library's 404 for a row the writer cannot list.
func (w *namedRowsWorld) notThere(status int, body []byte, what string) {
	w.t.Helper()

	assertStatus(w.t, status, http.StatusNotFound, body)
	if got := refusalMessage(w.t, body); got != referredRowMissing {
		w.t.Errorf("%s: the refusal = %q, want %q", what, got, referredRowMissing)
	}
}

// roleOfItsOwn makes a person up and gives them, in Anvil, a role made for the test that
// holds the grants and nothing else (giveRoleOfItsOwn).
func (w *namedRowsWorld) roleOfItsOwn(username accesstypes.User, role accesstypes.Role, rows ...access.GrantRow) {
	w.t.Helper()

	w.giveRoleOfItsOwn(makeCrewAccount(w.t.Context(), w.t, w.db, username), role, rows...)
}

// giveRoleOfItsOwn gives the person, in Anvil, a role made for the test that holds the
// grants and nothing else, and waits until the engine answers each grant.
func (w *sharingWorld) giveRoleOfItsOwn(person accesstypes.User, role accesstypes.Role, rows ...access.GrantRow) {
	w.t.Helper()
	ctx := w.t.Context()

	manager := w.engine.UserManager()
	held := accesstypes.DomainPolicyScope(anvil)
	if err := manager.AddRole(ctx, held, role, "Is made for the test."); err != nil {
		w.t.Fatalf("access.UserManager.AddRole(%s) error = %v", role, err)
	}
	if err := manager.AddRoleGrants(ctx, held, role, rows...); err != nil {
		w.t.Fatalf("access.UserManager.AddRoleGrants(%s) error = %v", role, err)
	}
	if err := manager.AddUserRoles(ctx, held, person, role); err != nil {
		w.t.Fatalf("access.UserManager.AddUserRoles(%s, %s) error = %v", person, role, err)
	}
	checker := w.engine.ForUser(person)
	for _, row := range rows {
		if d := decisionFor(ctx, w.t, checker, accesstypes.DomainScope(anvil), row.Permission, row.Resource, time.Now().Add(15*time.Second)); !d.IsGranted() {
			w.t.Fatalf("%s on %s for %s, who holds %s = %s, want granted before the cases run", row.Permission, row.Resource, person, role, d)
		}
	}
}

// missionValue is the value of a mission of Anvil's for Halvard, a client on every sector's
// roster, with the briefing sheet's field as given (a JSON value), or without it where it is
// empty.
func missionValue(briefing string) string {
	sheet := ""
	if briefing != "" {
		sheet = `,"briefingTemplateId":` + briefing
	}

	return fmt.Sprintf(`{"clientId":%q,"kindId":"courier","title":"Picked rows","hazard":1,"fee":100,"deadline":%q%s}`, clientHalvardID, deadline(365), sheet)
}

// TestNamedRows_aColumnPickedFromAView holds a column picked from a view to the rows its
// writer's list of the view shows. The lead of the first mission sets neither its client
// nor its briefing sheet: her role is refused both by its own grant. A person whose role
// changes a mission's client and lists no client roster cannot point the mission at
// another client, and may send the client it has; the Marshal, who lists the roster,
// points it there. A person
// whose role adds missions and lists no roster cannot add one; the booking agent, who
// lists it, adds it. A person whose role adds hangar passes and lists neither the
// sector's people nor its groups names neither in a pass; the Marshal names each, and
// cannot name an account that is not one of the sector's people.
func TestNamedRows_aColumnPickedFromAView(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	w := &namedRowsWorld{sharingWorld: newSharingWorld(t)}
	first, _ := writeSharedMissions(t, w.tenantListsWorld)

	w.enter("ida")
	var none time.Time
	w.done("marshal", "give-crew-role-on-mission", w.share(first.id, "ida", "", "MissionLead", none, none))
	w.leaveOnly("ida")

	pointAt := func(user accesstypes.User, client string) (int, []byte) {
		return w.write(user, "patch", "missions/"+first.id, fmt.Sprintf(`{"clientId":%q}`, client))
	}
	status, body := pointAt("ida", clientMeridianID)
	assertStatus(t, status, http.StatusForbidden, body)
	if got := readColumn[string](ctx, t, w.db, "Missions", spanner.Key{first.id}, "ClientId"); got != clientHalvardID {
		t.Errorf("the mission's client after the lead's refused change = %q, want Halvard's", got)
	}
	status, body = w.write("ida", "patch", "missions/"+first.id, `{"briefingTemplateId":"dispatch"}`)
	assertStatus(t, status, http.StatusForbidden, body)

	w.roleOfItsOwn("rue", "ClientChanger",
		access.GrantRow{Permission: accesstypes.Update, Resource: "Missions"},
		access.GrantRow{Permission: accesstypes.Update, Resource: "Missions.clientId"},
	)
	status, body = pointAt("rue", clientMeridianID)
	w.notThere(status, body, "a mission pointed at another client by a role that lists no roster")
	status, body = pointAt("rue", clientHalvardID)
	assertStatus(t, status, http.StatusOK, body)
	status, body = pointAt("marshal", clientMeridianID)
	assertStatus(t, status, http.StatusOK, body)

	w.roleOfItsOwn("mae", "MissionWriter",
		access.GrantRow{Permission: accesstypes.Create, Resource: "Missions"},
		access.GrantRow{Permission: accesstypes.Create, Resource: "Missions.clientId"},
		access.GrantRow{Permission: accesstypes.Create, Resource: "Missions.kindId"},
		access.GrantRow{Permission: accesstypes.Create, Resource: "Missions.title"},
		access.GrantRow{Permission: accesstypes.Create, Resource: "Missions.hazard"},
		access.GrantRow{Permission: accesstypes.Create, Resource: "Missions.fee"},
		access.GrantRow{Permission: accesstypes.Create, Resource: "Missions.deadline"},
	)
	status, body = w.write("mae", "add", "missions", missionValue(""))
	w.notThere(status, body, "a mission added by a role that lists no roster")
	status, body = w.write("booking", "add", "missions", missionValue(""))
	assertStatus(t, status, http.StatusOK, body)

	// A pass names its hangar, so its writer lists the hangars.
	w.roleOfItsOwn("pip", "PassWriter",
		access.GrantRow{Permission: accesstypes.Create, Resource: "HangarPasses"},
		access.GrantRow{Permission: accesstypes.Create, Resource: "HangarPasses.hangarId"},
		access.GrantRow{Permission: accesstypes.Create, Resource: "HangarPasses.userId"},
		access.GrantRow{Permission: accesstypes.Create, Resource: "HangarPasses.groupId"},
		access.GrantRow{Permission: accesstypes.Create, Resource: "HangarPasses.levelId"},
		access.GrantRow{Permission: accesstypes.List, Resource: "Hangars"},
	)
	w.enter("una")
	bayKeepers := bayKeepersOf(t, w.sharingWorld)
	stranger := makeCrewAccount(ctx, t, w.db, "vic")
	personPass := func(person string) string {
		return fmt.Sprintf(`{"hangarId":%q,"userId":%q,"levelId":"visit"}`, hangarAnvilDockID, person)
	}
	status, body = w.write("pip", "add", "hangar-passes", personPass(w.ids["una"]))
	w.notThere(status, body, "a pass naming a person, by a role that lists no person of the sector")
	status, body = w.write("pip", "add", "hangar-passes", groupPass(bayKeepers, "visit"))
	w.notThere(status, body, "a pass naming a group, by a role that lists no group of the sector")
	status, body = w.write("marshal", "add", "hangar-passes", personPass(w.ids["una"]))
	assertStatus(t, status, http.StatusOK, body)
	status, body = w.write("marshal", "add", "hangar-passes", groupPass(bayKeepers, "visit"))
	assertStatus(t, status, http.StatusOK, body)
	status, body = w.write("marshal", "add", "hangar-passes", personPass(string(stranger)))
	w.notThere(status, body, "the Marshal's pass naming an account that is not one of the sector's people")
}

// bayKeepersOf reads the id of the Bay keepers, a group of Anvil's the seed writes.
func bayKeepersOf(t *testing.T, w *sharingWorld) string {
	t.Helper()

	iter := w.db.Client.Single().Query(t.Context(), spanner.Statement{
		SQL:    `SELECT Id FROM CrewGroups WHERE Name = 'Bay keepers' AND DomainId = @sector`,
		Params: map[string]any{"sector": anvil},
	})
	defer iter.Stop()
	row, err := iter.Next()
	if err != nil {
		t.Fatalf("reading the Bay keepers' id: %v", err)
	}
	var id string
	if err := row.Columns(&id); err != nil {
		t.Fatalf("reading the Bay keepers' id: %v", err)
	}

	return id
}

// TestNamedRows_aColumnPickedFromAComputedList holds a column picked from a computed list
// to the rows its writer's list shows: the briefing catalog, which the global role
// BriefingClerk alone lists. A marshal of Anvil who does not hold it sets a mission's
// sheet neither on a create nor on an update; once the role is given they set it on both,
// and they still cannot set a sheet the catalog does not hold. A mission with no sheet
// names nothing.
func TestNamedRows_aColumnPickedFromAComputedList(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	w := &namedRowsWorld{sharingWorld: newSharingWorld(t)}
	first, _ := writeSharedMissions(t, w.tenantListsWorld)
	w.seat(ctx, t, "sol", "SectorMarshal", anvil)

	sheet := func(briefing string) string {
		return fmt.Sprintf(`{"briefingTemplateId":%s}`, briefing)
	}
	status, body := w.write("sol", "add", "missions", missionValue(`"dispatch"`))
	w.notThere(status, body, "a marshal who lists no catalog adding a mission on the dispatch brief")
	status, body = w.write("sol", "patch", "missions/"+first.id, sheet(`"dispatch"`))
	w.notThere(status, body, "a marshal who lists no catalog putting a mission on the dispatch brief")

	sol := accesstypes.User(w.ids["sol"])
	if err := w.engine.UserManager().AddUserRoles(ctx, accesstypes.GlobalPolicyScope(), sol, "BriefingClerk"); err != nil {
		t.Fatalf("access.UserManager.AddUserRoles(sol, BriefingClerk) error = %v", err)
	}
	if d := decisionFor(ctx, t, w.engine.ForUser(sol), accesstypes.GlobalScope(), accesstypes.List, "BriefingTemplates", time.Now().Add(15*time.Second)); !d.IsGranted() {
		t.Fatalf("List on BriefingTemplates for sol, given BriefingClerk = %s, want granted", d)
	}
	status, body = w.write("sol", "add", "missions", missionValue(`"dispatch"`))
	assertStatus(t, status, http.StatusOK, body)
	status, body = w.write("sol", "patch", "missions/"+first.id, sheet(`"dispatch"`))
	assertStatus(t, status, http.StatusOK, body)

	status, body = w.write("sol", "add", "missions", missionValue(`"night-watch"`))
	w.notThere(status, body, "a mission added on a sheet the catalog does not hold")
	status, body = w.write("sol", "patch", "missions/"+first.id, sheet(`"night-watch"`))
	w.notThere(status, body, "a mission put on a sheet the catalog does not hold")

	status, body = w.write("sol", "add", "missions", missionValue("null"))
	assertStatus(t, status, http.StatusOK, body)
	status, body = w.write("sol", "patch", "missions/"+first.id, sheet("null"))
	assertStatus(t, status, http.StatusOK, body)
}

// TestNamedRows_aMethodsPickedField holds a method's picked field to the rows its caller can
// list, before the method runs: the ships a launch names, the squadrons a claim names, and
// the briefing catalog a briefing is compiled on. A person whose role of the test's own may
// launch and lists no ship is answered as if the ship were not there, and the mission stays
// claimed; the flight lead, who lists Anvil's ships, cannot name another sector's ship and
// launches on one of Anvil's. A person whose role may claim and lists no squadron is
// answered the same, and so is the dry run of that claim; a cadet, who lists the
// squadrons, claims. A marshal who does not hold BriefingClerk, which alone lists the
// catalog, compiles on no template of it, and compiles the standard sheet; given the role,
// they compile on one, and on none the catalog does not hold. No method of Lodestar's that
// declares a warning picks a row, so no warning is asked here.
func TestNamedRows_aMethodsPickedField(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	w := &namedRowsWorld{sharingWorld: newSharingWorld(t)}

	launch := func(user accesstypes.User, ship string) (int, []byte) {
		return w.as(user, http.MethodPost, sectorPath(anvil, "launch-mission"), fmt.Sprintf(`{"missionId":%q,"shipId":%q,"pilotUserId":"lead"}`, missionCorvidID, ship))
	}
	w.roleOfItsOwn("lark", "MissionLauncher", access.GrantRow{Permission: accesstypes.Execute, Resource: "LaunchMission"})
	status, body := launch("lark", shipKingfisherID)
	w.notThere(status, body, "a launch by a role that lists no ship")
	if got := readColumn[string](ctx, t, w.db, "Missions", spanner.Key{missionCorvidID}, "StatusId"); got != "claimed" {
		t.Errorf("the mission's status after the refused launch = %q, want claimed", got)
	}
	status, body = launch("lead", shipBastionWatchID)
	w.notThere(status, body, "the flight lead launching on another sector's ship")
	status, body = launch("lead", shipKingfisherID)
	assertStatus(t, status, http.StatusOK, body)

	claim := fmt.Sprintf(`{"missionId":%q,"squadronId":%q}`, missionHaulerID, squadronHammerID)
	w.roleOfItsOwn("clay", "MissionClaimer", access.GrantRow{Permission: accesstypes.Execute, Resource: "ClaimMission"})
	status, body = w.as("clay", http.MethodPost, sectorPath(anvil, "claim-mission"), claim)
	w.notThere(status, body, "a claim by a role that lists no squadron")
	status, body = doDryRunAs(t, w.h, "clay", sectorPath(anvil, "claim-mission"), claim)
	w.notThere(status, body, "the dry run of a claim by a role that lists no squadron")
	if got := readColumn[string](ctx, t, w.db, "Missions", spanner.Key{missionHaulerID}, "StatusId"); got != "open" {
		t.Errorf("the mission's status after the refused claims = %q, want open", got)
	}
	status, body = w.as("cadet", http.MethodPost, sectorPath(anvil, "claim-mission"), fmt.Sprintf(`{"missionId":%q,"squadronId":%q}`, missionQuarantineID, squadronTongsID))
	assertStatus(t, status, http.StatusOK, body)

	compile := func(body string) (int, []byte) {
		return w.as("sol", http.MethodPost, sectorPath(anvil, "compile-briefing"), body)
	}
	w.seat(ctx, t, "sol", "SectorMarshal", anvil)
	status, body = compile(`{"templateId":"dispatch"}`)
	w.notThere(status, body, "a briefing on a template, compiled by a marshal who lists no catalog")
	status, body = compile(`{}`)
	assertStatus(t, status, http.StatusOK, body)
	sol := accesstypes.User(w.ids["sol"])
	if err := w.engine.UserManager().AddUserRoles(ctx, accesstypes.GlobalPolicyScope(), sol, "BriefingClerk"); err != nil {
		t.Fatalf("access.UserManager.AddUserRoles(sol, BriefingClerk) error = %v", err)
	}
	if d := decisionFor(ctx, t, w.engine.ForUser(sol), accesstypes.GlobalScope(), accesstypes.List, "BriefingTemplates", time.Now().Add(15*time.Second)); !d.IsGranted() {
		t.Fatalf("List on BriefingTemplates for sol, given BriefingClerk = %s, want granted", d)
	}
	status, body = compile(`{"templateId":"dispatch"}`)
	assertStatus(t, status, http.StatusOK, body)
	status, body = compile(`{"templateId":"night-watch"}`)
	w.notThere(status, body, "a briefing on a template the catalog does not hold")
}

// TestNamedRows_theRolesListWhatTheyActOn holds the engine's answers in the whole
// application for three roles of the crew's file, each held alone: Headquarters lists the
// sectors it gives roles in, and the ship classes, the pilots and their certifications it
// manages; the Surveyor lists the sectors whose records it corrects; the Adjutant lists the
// pilots a commendation is for. A person who holds Headquarters and nothing else lists the
// ship classes and the pilots.
func TestNamedRows_theRolesListWhatTheyActOn(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	w := &namedRowsWorld{sharingWorld: newSharingWorld(t)}

	for _, held := range []struct {
		name, role string
		lists      []accesstypes.Resource
	}{
		{name: "hale", role: "Headquarters", lists: []accesstypes.Resource{"Sectors", "ShipClasses", "Pilots", "PilotCertifications"}},
		{name: "sutton", role: "Surveyor", lists: []accesstypes.Resource{"Sectors"}},
		{name: "addy", role: "Adjutant", lists: []accesstypes.Resource{"Pilots"}},
	} {
		w.ids[held.name] = string(makeCrewAccount(ctx, t, w.db, accesstypes.User(held.name)))
		w.giveCrewRole(ctx, t, held.name, held.role, accesstypes.GlobalPolicyScope())
		checker := w.engine.ForUser(accesstypes.User(w.ids[held.name]))
		for _, res := range held.lists {
			if d := decisionFor(ctx, t, checker, accesstypes.GlobalScope(), accesstypes.List, res, time.Now().Add(15*time.Second)); !d.IsGranted() {
				t.Errorf("List on %s for a person who holds %s alone = %s, want granted", res, held.role, d)
			}
		}
	}
	for _, path := range []string{"ship-classes", "pilots"} {
		status, body := w.as("hale", http.MethodGet, consoleAPI+"/"+path+"?limit=all", "")
		assertStatus(t, status, http.StatusOK, body)
		if rows := decodeRows(t, body); len(rows) == 0 {
			t.Errorf("a person who holds Headquarters alone lists no %s", path)
		}
	}
}

// TestNamedRows_aMusterClerk holds the Muster Clerk's role held alone in Anvil: the engine
// grants its List on the sector's people and on its groups in Anvil and in no other sector.
// Served, it lists Anvil's people by their username and their names and nothing else of an
// account, and Anvil's groups, and nothing more: not who is in a group, not a role to give,
// not the sector's missions.
func TestNamedRows_aMusterClerk(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	w := newTenantListsWorld(t)

	w.seat(ctx, t, "mona", "MusterClerk", anvil)
	checker := w.engine.ForUser(accesstypes.User(w.ids["mona"]))
	for _, res := range []accesstypes.Resource{"CrewSectorUsers", "CrewSectorGroups"} {
		if d := decisionFor(ctx, t, checker, accesstypes.DomainScope(anvil), accesstypes.List, res, time.Now().Add(15*time.Second)); !d.IsGranted() {
			t.Fatalf("List on %s in Anvil for a Muster Clerk of Anvil = %s, want granted", res, d)
		}
		if d := decisionFor(ctx, t, checker, accesstypes.DomainScope(bastion), accesstypes.List, res, time.Now()); d.IsGranted() {
			t.Errorf("List on %s in Bastion for a Muster Clerk of Anvil = %s, want denied", res, d)
		}
	}

	get := func(path string) (int, []byte) {
		t.Helper()

		return doRequestAs(t, w.h, "mona", http.MethodGet, sectorPath(anvil, path), "")
	}
	status, body := get(sectorPeoplePath + "?limit=200")
	assertStatus(t, status, http.StatusOK, body)
	people := decodeRows(t, body)
	if len(people) == 0 {
		t.Errorf("a Muster Clerk of Anvil lists none of its people: %s", body)
	}
	for _, row := range people {
		for _, field := range []string{"username", "displayName"} {
			if _, ok := row[field]; !ok {
				t.Errorf("a row of Anvil's people, as a Muster Clerk lists it, carries no %s: %v", field, row)
			}
		}
		for field := range row {
			if !slices.Contains([]string{"id", "username", "name", "displayName"}, field) {
				t.Errorf("a row of Anvil's people, as a Muster Clerk lists it, carries %s, want the username and the names alone: %v", field, row)
			}
		}
	}
	status, body = get(sectorGroupsPath + "?limit=200")
	assertStatus(t, status, http.StatusOK, body)
	if groups := decodeRows(t, body); len(groups) == 0 {
		t.Errorf("a Muster Clerk of Anvil lists none of its groups: %s", body)
	}
	status, body = get(sectorGroupPeoplePath + "?limit=200")
	assertStatus(t, status, http.StatusForbidden, body)
	status, body = doRequestAs(t, w.h, "mona", http.MethodPost, sectorPath(anvil, "assign-crew-sector-role"), fmt.Sprintf(`{"userId":%q,"role":"Cadet"}`, w.ids["fay"]))
	assertStatus(t, status, http.StatusForbidden, body)
	status, body = get("missions")
	assertStatus(t, status, http.StatusForbidden, body)
}
