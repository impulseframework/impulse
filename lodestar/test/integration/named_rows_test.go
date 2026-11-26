package integration

// named_rows_test: a column picked from a view or from a computed list, held to the rows its
// writer can list, as a column naming a table's row is (TestRecordRoles_aRowAWriteNames).
// A mission's client is picked from the client roster, a view of every client in every
// sector; a hangar pass's person and group are picked from the sector's people and its
// groups, the views of the crew's generated management; a mission's briefing sheet is
// picked from the briefing catalog, a list Go serves. A create or an update that sets one
// of them to a row the writer's own list would not show is answered as a row that is not
// there, and a value the row already holds is not asked again.

import (
	"fmt"
	"net/http"
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
// holds the grants and nothing else, and waits until the engine answers each grant.
func (w *namedRowsWorld) roleOfItsOwn(username accesstypes.User, role accesstypes.Role, rows ...access.GrantRow) {
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
	person := makeCrewAccount(ctx, w.t, w.db, username)
	if err := manager.AddUserRoles(ctx, held, person, role); err != nil {
		w.t.Fatalf("access.UserManager.AddUserRoles(%s, %s) error = %v", username, role, err)
	}
	checker := w.engine.ForUser(person)
	for _, row := range rows {
		if d := decisionFor(ctx, w.t, checker, accesstypes.DomainScope(anvil), row.Permission, row.Resource, time.Now().Add(15*time.Second)); !d.IsGranted() {
			w.t.Fatalf("%s on %s for %s, who holds %s = %s, want granted before the cases run", row.Permission, row.Resource, username, role, d)
		}
	}
}

// missionValue is the value of a mission of Anvil's for the client, with the briefing
// sheet's field as given (a JSON value), or without it where it is empty.
func missionValue(client, briefing string) string {
	sheet := ""
	if briefing != "" {
		sheet = `,"briefingTemplateId":` + briefing
	}

	return fmt.Sprintf(`{"clientId":%q,"kindId":"courier","title":"Picked rows","hazard":1,"fee":100,"deadline":%q%s}`, client, deadline(365), sheet)
}

// TestNamedRows_aColumnPickedFromAView holds a column picked from a view to the rows its
// writer's list of the view shows. The lead of the first mission holds nothing else and
// lists no client roster: she cannot point her mission at another client, and she may
// send the client it has; the Marshal, who lists the roster, points it there. A person
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
	w.notThere(status, body, "the lead pointing her mission at a client, listing no roster")
	status, body = pointAt("ida", clientHalvardID)
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
	status, body = w.write("mae", "add", "missions", missionValue(clientHalvardID, ""))
	w.notThere(status, body, "a mission added by a role that lists no roster")
	status, body = w.write("booking", "add", "missions", missionValue(clientHalvardID, ""))
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
	status, body := w.write("sol", "add", "missions", missionValue(clientHalvardID, `"dispatch"`))
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
	status, body = w.write("sol", "add", "missions", missionValue(clientHalvardID, `"dispatch"`))
	assertStatus(t, status, http.StatusOK, body)
	status, body = w.write("sol", "patch", "missions/"+first.id, sheet(`"dispatch"`))
	assertStatus(t, status, http.StatusOK, body)

	status, body = w.write("sol", "add", "missions", missionValue(clientHalvardID, `"night-watch"`))
	w.notThere(status, body, "a mission added on a sheet the catalog does not hold")
	status, body = w.write("sol", "patch", "missions/"+first.id, sheet(`"night-watch"`))
	w.notThere(status, body, "a mission put on a sheet the catalog does not hold")

	status, body = w.write("sol", "add", "missions", missionValue(clientHalvardID, "null"))
	assertStatus(t, status, http.StatusOK, body)
	status, body = w.write("sol", "patch", "missions/"+first.id, sheet("null"))
	assertStatus(t, status, http.StatusOK, body)
}
