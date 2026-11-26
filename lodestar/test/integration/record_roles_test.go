// Demonstrates: @holdsRoles, auth.generated-identity, auth.periods, auth.groups.
package integration

// record_roles_test: a mission shared with a person or a group by a role held on it, as the
// generated management of the crew auth serves it, against the real engine over the shipped
// role file.
//
// A mission can hold roles (@holdsRoles), and the crew's MissionObserver and MissionLead are
// held on missions (their heldOn). Held on one mission, a role's grants count on that
// mission, on its sorties, their expenses and its documents, which are under it by the
// schema, and on the squadron it names, and on nothing else: the same grants held in the
// sector reach every mission. The sector's Marshal gives and takes either role on a mission,
// to a person of the sector or to a group kept there, through the four methods of the
// mission's sharing; the lead shares the mission with observers through the same methods,
// held to the observer's role by the grant's condition.
//
// Each test writes two missions of Anvil of its own, each with a sortie, an expense and a
// document, the first naming the Hammer squadron and the second the Tongs; the third mission
// is the seed's, in Bastion. A person who holds a role on a mission alone is first one of the
// sector's people, as a person invited is, and then has their own role there taken: the
// mission's is all they hold. The people's names are none of the cast's usernames, which
// the harness runs as the cast's own accounts.

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/identity"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
	"github.com/impulseframework/impulse/resource"
)

// sharedMission is a mission of Anvil a test writes, with one row of each kind under it and
// the squadron it names.
type sharedMission struct {
	id, title, sortie, expense, document, squadron string
}

// writeSharedMissions writes the two missions of Anvil the tests share: the first names the
// Hammer squadron, the second the Tongs. Neither is one a Cadet lists (hazard 3 and 4).
func writeSharedMissions(t *testing.T, w *tenantListsWorld) (first, second sharedMission) {
	t.Helper()

	write := func(n int, title, squadron string, hazard int64) sharedMission {
		id := func(kind string) string { return fmt.Sprintf("%s-0000-4000-8000-0000000d00%02d", kind, n) }
		m := sharedMission{id: id("8d000000"), title: title, sortie: id("9d000000"), expense: id("91d00000"), document: id("a5d00000"), squadron: squadron}
		if _, err := w.db.Apply(t.Context(), []*spanner.Mutation{
			spanner.InsertMap("Missions", map[string]any{
				"Id": m.id, "SectorId": anvil, "ClientId": clientHalvardID, "KindId": "courier", "Title": title, "Hazard": hazard,
				"Fee": *big.NewRat(4000, 1), "Deadline": time.Now().Add(72 * time.Hour), "BookedByUserId": "f0000000-0000-4000-8000-000000000002",
				"BookedByName": "marshal", "AssignedSquadronId": squadron, "StatusId": "open",
			}),
			spanner.InsertMap("Sorties", map[string]any{
				"Id": m.sortie, "MissionId": m.id, "ShipId": shipKingfisherID, "PilotUserId": "f0000000-0000-4000-8000-000000000004", "LaunchedAt": time.Now().UTC(),
			}),
			spanner.InsertMap("SortieExpenses", map[string]any{"Id": m.expense, "SortieId": m.sortie, "Category": "fuel", "Amount": big.NewRat(300, 1)}),
			spanner.InsertMap("MissionDocuments", map[string]any{
				"Id": m.document, "MissionId": m.id, "Title": "Flight plan", "FileName": "plan.txt", "ContentType": "text/plain", "Size": 4,
				"StoreKey": id("5d000000"), "UploadedByUserId": "f0000000-0000-4000-8000-000000000002", "UploadedByName": "marshal",
				"UploadedAt": time.Now().UTC(), "Digest": make([]byte, sha256.Size),
			}),
		}); err != nil {
			t.Fatalf("writing the mission %q: %v", title, err)
		}

		return m
	}

	return write(1, "Chart the Anvil debris field", squadronHammerID, 4), write(2, "Ferry the relay spares", squadronTongsID, 3)
}

// sharingWorld is the world the tests of this file share: the identity world with the
// people each test seats, and the requests they make.
type sharingWorld struct {
	*tenantListsWorld
	t *testing.T
}

func newSharingWorld(t *testing.T) *sharingWorld {
	t.Helper()

	return &sharingWorld{tenantListsWorld: &tenantListsWorld{identityWorld: buildIdentityWorld(t, false), ids: make(map[string]string)}, t: t}
}

// as sends a request as the person under Anvil's address, or under another sector's where
// the path names one with sectorPath.
func (w *sharingWorld) as(user accesstypes.User, method, path, body string) (int, []byte) {
	w.t.Helper()

	return doRequestAs(w.t, w.h, user, method, path, body)
}

// done sends a method's request as the person and holds it to 204.
func (w *sharingWorld) done(user accesstypes.User, method, body string) {
	w.t.Helper()

	status, answer := w.as(user, http.MethodPost, sectorPath(anvil, method), body)
	assertStatus(w.t, status, http.StatusNoContent, answer)
}

// share is the body of a method of a mission's sharing for a person (who) or a group
// (group), with the period where one is set.
func (w *sharingWorld) share(mission, who, group, role string, startsAt, endsAt time.Time) string {
	w.t.Helper()

	fields := map[string]any{"recordId": mission, "role": role}
	if group != "" {
		fields["groupId"] = group
	} else {
		fields["userId"] = w.ids[who]
	}

	return periodBody(w.t, fields, startsAt, endsAt)
}

// enter seats a person as a Cadet of Anvil, one of the sector's people.
func (w *sharingWorld) enter(name string) {
	w.t.Helper()

	w.seat(w.t.Context(), w.t, name, "Cadet", anvil)
}

// leaveOnly takes the person's Cadet of Anvil away, by the Marshal: what the person holds on
// a mission or by a place in a group is then all they hold in the sector.
func (w *sharingWorld) leaveOnly(name string) {
	w.t.Helper()

	w.done("marshal", "remove-crew-sector-role", fmt.Sprintf(`{"userId":%q,"role":"Cadet"}`, w.ids[name]))
}

// listed lists a list of Anvil's as the person, by the rows' ids, sorted: one page of a
// hundred, the largest the sorties and their expenses serve.
func (w *sharingWorld) listed(user accesstypes.User, path string) []string {
	w.t.Helper()

	return idsOf(w.t, listRows(w.t, w.h, user, sectorPath(anvil, path+"?limit=100")))
}

// reads reports the answer to the person's read of one row of Anvil's.
func (w *sharingWorld) reads(user accesstypes.User, path string) int {
	w.t.Helper()

	status, _ := w.as(user, http.MethodGet, sectorPath(anvil, path), "")

	return status
}

// holders lists the holders of one mission as the person: each row's kind of holder, the
// holder's name and the role, sorted.
func (w *sharingWorld) holders(user accesstypes.User, filter string) []string {
	w.t.Helper()

	path := "crew-mission-holders?limit=200"
	if filter != "" {
		path += "&filter=recordId:eq:" + filter
	}
	var rows []string
	for _, row := range listRows(w.t, w.h, user, sectorPath(anvil, path)) {
		rows = append(rows, cell[string](w.t, row, "memberKind")+" "+cell[string](w.t, row, "name")+" "+cell[string](w.t, row, "role"))
	}
	slices.Sort(rows)

	return rows
}

// missionsOf lists the ids of Anvil's missions straight from the database, sorted, with the
// ones given added.
func (w *sharingWorld) missionsOf(sql string, more ...string) []string {
	w.t.Helper()

	iter := w.db.Client.Single().Query(w.t.Context(), spanner.Statement{SQL: sql, Params: map[string]any{"sector": anvil}})
	defer iter.Stop()
	var ids []string
	for {
		row, err := iter.Next()
		if err != nil {
			break
		}
		var id string
		if err := row.Columns(&id); err != nil {
			w.t.Fatalf("reading a mission's id: %v", err)
		}
		ids = append(ids, id)
	}
	ids = append(ids, more...)
	slices.Sort(ids)

	return ids
}

// TestRecordRoles_aMissionShared holds a mission shared with a person, with a group and by
// its lead: what each reaches and what each is refused, the lists that name them, and the
// mission's delete, which takes the roles held on it.
func TestRecordRoles_aMissionShared(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	w := newSharingWorld(t)
	first, second := writeSharedMissions(t, w.tenantListsWorld)

	// The grants this suite proves, pinned to the roles file (conditions-proven): the lead
	// shares the mission with observers alone.
	provesGrant(t, crew.Roles(), "MissionLead", "Execute", "GiveCrewRoleOnMission", "new.role IN ('MissionObserver')")
	provesGrant(t, crew.Roles(), "MissionLead", "Execute", "TakeCrewRoleOnMission", "new.role IN ('MissionObserver')")
	provesGrant(t, crew.Roles(), "MissionLead", "Execute", "GiveCrewGroupRoleOnMission", "new.role IN ('MissionObserver')")
	provesGrant(t, crew.Roles(), "MissionLead", "Execute", "TakeCrewGroupRoleOnMission", "new.role IN ('MissionObserver')")

	// The people: ob observes the first mission and ida runs it, each holding nothing else
	// in the sector; gil holds a place in the Marshal's group and nothing else, and the group
	// observes the first mission; kit is a Cadet of Anvil, whom the lead shares the mission
	// with; bea is a Cadet who observes the second; and the keeper runs the sector and keeps
	// its records (SectorMarshal and Registrar), so may delete a mission with a document.
	for _, name := range []string{"ob", "ida", "gil", "kit", "bea"} {
		w.enter(name)
	}
	w.seat(ctx, t, "keeper", "SectorMarshal", anvil)
	if err := w.inTransaction(ctx, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		sector := anvil
		scope, err := identity.Scope(&sector, false)
		if err != nil {
			return fmt.Errorf("identity.Scope(%s): %w", sector, err)
		}

		return w.app.CrewIdentity().AssignRole(ctx, txn, scope, accesstypes.User(w.ids["keeper"]), "Registrar")
	}); err != nil {
		t.Fatalf("giving the keeper the Registrar's role: %v", err)
	}
	var none time.Time
	w.done("marshal", "give-crew-role-on-mission", w.share(first.id, "ob", "", "MissionObserver", none, none))
	w.done("marshal", "give-crew-role-on-mission", w.share(first.id, "ida", "", "MissionLead", none, none))
	w.done("marshal", "give-crew-role-on-mission", w.share(second.id, "bea", "", "MissionObserver", none, none))
	status, body := w.as("marshal", http.MethodPost, sectorPath(anvil, "create-crew-sector-group"), `{"name":"Debris watch","description":"Watches the debris field's charting."}`)
	assertStatus(t, status, http.StatusOK, body)
	group := cell[string](t, decodeRow(t, body), "id")
	w.done("marshal", "add-crew-sector-group-member", fmt.Sprintf(`{"groupId":%q,"userId":%q}`, group, w.ids["gil"]))
	w.done("marshal", "give-crew-group-role-on-mission", w.share(first.id, "", group, "MissionObserver", none, none))
	for _, name := range []string{"ob", "ida", "gil"} {
		w.leaveOnly(name)
	}

	// The observer, and the group's member through the group: exactly the first mission,
	// what is under it and the squadron it names; nothing of the second mission, of the
	// other squadron or of another sector; and no change.
	for _, who := range []accesstypes.User{"ob", "gil"} {
		for path, want := range map[string][]string{
			"missions": {first.id}, "sorties": {first.sortie}, "sortie-expenses": {first.expense},
			"mission-documents": {first.document}, "squadrons": {first.squadron},
		} {
			if got := w.listed(who, path); !slices.Equal(got, want) {
				t.Errorf("%s lists %s %v, want %v", who, path, got, want)
			}
		}
		for path, want := range map[string]int{
			"missions/" + first.id: http.StatusOK, "sorties/" + first.sortie: http.StatusOK, "sortie-expenses/" + first.expense: http.StatusOK,
			"mission-documents/" + first.document: http.StatusOK, "squadrons/" + first.squadron: http.StatusOK,
			"missions/" + second.id: http.StatusNotFound, "sorties/" + second.sortie: http.StatusNotFound, "sortie-expenses/" + second.expense: http.StatusNotFound,
			"mission-documents/" + second.document: http.StatusNotFound, "squadrons/" + second.squadron: http.StatusNotFound,
		} {
			if got := w.reads(who, path); got != want {
				t.Errorf("%s reads %s: status %d, want %d", who, path, got, want)
			}
		}
		for _, path := range []string{"missions", "missions/" + missionBeaconID, "squadrons/" + squadronPortcullisID} {
			if status, body := w.as(who, http.MethodGet, sectorPath(bastion, path), ""); status != http.StatusForbidden && status != http.StatusNotFound {
				t.Errorf("%s reads Bastion's %s: status %d, want 403 or 404: %s", who, path, status, body)
			}
		}
		status, body := w.as(who, http.MethodPatch, "/console/api/resources", fmt.Sprintf(`[{"op":"patch","path":%q,"value":{"notes":"Seen"}}]`, opPath(anvil, "missions/"+first.id)))
		assertStatus(t, status, http.StatusForbidden, body)
	}

	// The lead runs the first mission: a sortie under it and a change to it are hers, a
	// sortie under the second mission is not, and neither is a new mission or a squadron.
	patch := func(user accesstypes.User, op, path, value string) (int, []byte) {
		return w.as(user, http.MethodPatch, "/console/api/resources", fmt.Sprintf(`[{"op":%q,"path":%q,"value":%s}]`, op, opPath(anvil, path), value))
	}
	sortie := func(mission string) string {
		return fmt.Sprintf(`{"missionId":%q,"shipId":%q,"pilotUserId":"ida","launchedAt":"2027-03-01T08:00:00Z"}`, mission, shipKingfisherID)
	}
	status, body = patch("ida", "add", "sorties", sortie(first.id))
	assertStatus(t, status, http.StatusOK, body)
	status, body = patch("ida", "patch", "missions/"+first.id, `{"notes":"Charting the outer ring first"}`)
	assertStatus(t, status, http.StatusOK, body)
	status, body = patch("ida", "add", "sorties", sortie(second.id))
	assertStatus(t, status, http.StatusForbidden, body)
	status, body = patch("ida", "add", "missions", fmt.Sprintf(`{"clientId":%q,"kindId":"courier","title":"A mission of her own","hazard":1,"fee":"100","deadline":"2027-03-01T08:00:00Z"}`, clientHalvardID))
	assertStatus(t, status, http.StatusForbidden, body)
	status, body = patch("ida", "add", "squadrons", fmt.Sprintf(`{"wingId":%q,"name":"Her own"}`, wingForgeID))
	assertStatus(t, status, http.StatusForbidden, body)

	// The lead shares the first mission with kit by the method's request, and is refused the
	// second mission and the lead's own role; she lists the first mission's holders, and the
	// sector's people are not hers to list.
	kitBefore := w.listed("kit", "missions")
	w.done("ida", "give-crew-role-on-mission", w.share(first.id, "kit", "", "MissionObserver", none, none))
	status, body = w.as("ida", http.MethodPost, sectorPath(anvil, "give-crew-role-on-mission"), w.share(second.id, "kit", "", "MissionObserver", none, none))
	if status != http.StatusForbidden && status != http.StatusNotFound {
		t.Errorf("the lead shares the second mission: status %d, want 403 or 404: %s", status, body)
	}
	status, body = w.as("ida", http.MethodPost, sectorPath(anvil, "give-crew-role-on-mission"), w.share(first.id, "kit", "", "MissionLead", none, none))
	assertStatus(t, status, http.StatusForbidden, body)
	holdersOfFirst := []string{"group Debris watch MissionObserver", "person ida MissionLead", "person kit MissionObserver", "person ob MissionObserver"}
	if got := w.holders("ida", ""); !slices.Equal(got, holdersOfFirst) {
		t.Errorf("the lead lists the holders %v, want the first mission's alone, %v", got, holdersOfFirst)
	}
	if got := w.holders("ida", second.id); len(got) != 0 {
		t.Errorf("the lead lists the second mission's holders %v, want none", got)
	}
	status, body = w.as("ida", http.MethodGet, sectorPath(anvil, sectorPeoplePath), "")
	assertStatus(t, status, http.StatusForbidden, body)

	// The roles the share dialog offers the Marshal: both roles held on a mission.
	rolesOffered := func(user accesstypes.User) (int, []string) {
		status, body := w.as(user, http.MethodGet, sectorPath(anvil, "crew-mission-roles?filter=method:eq:GiveCrewRoleOnMission"), "")
		if status != http.StatusOK {
			return status, nil
		}

		return status, namesOf(t, decodeRows(t, body), "name")
	}
	if status, got := rolesOffered("marshal"); status != http.StatusOK || !slices.Equal(got, []string{"MissionLead", "MissionObserver"}) {
		t.Errorf("the roles offered the Marshal to give on a mission: status %d, %v, want 200 and MissionLead, MissionObserver", status, got)
	}

	// The sector's administrator's role is no role held on a mission.
	status, body = w.as("marshal", http.MethodPost, sectorPath(anvil, "give-crew-role-on-mission"), w.share(first.id, "kit", "", "SectorMarshal", none, none))
	assertStatus(t, status, http.StatusBadRequest, body)
	if got, want := refusalMessage(t, body), "SectorMarshal is not a role held on a mission."; got != want {
		t.Errorf("the refusal of the Marshal's role on a mission = %q, want %q", got, want)
	}

	// A person with a role in the sector reads what it gives as before, with the mission
	// shared beside it; the Marshal reads every mission of the sector.
	if got, want := w.listed("kit", "missions"), slices.Sorted(slices.Values(append(slices.Clone(kitBefore), first.id))); !slices.Equal(got, want) {
		t.Errorf("kit, a Cadet the first mission is shared with, lists %v, want the Cadet's %v and the first mission", got, kitBefore)
	}
	if want := w.missionsOf("SELECT Id FROM Missions WHERE SectorId = @sector AND Hazard IN (1, 2)"); !slices.Equal(kitBefore, want) {
		t.Errorf("kit, a Cadet, listed %v before the mission was shared, want the missions of hazard 1 or 2, %v", kitBefore, want)
	}
	if got, want := w.listed("marshal", "missions"), w.missionsOf("SELECT Id FROM Missions WHERE SectorId = @sector"); !slices.Equal(got, want) {
		t.Errorf("the Marshal lists %d missions, want every one of the sector's %d", len(got), len(want))
	}

	// The people list: ob is one of the sector's people by the mission alone, and the sector
	// is one of ob's own.
	people := sectorList(t, w.h, "marshal", anvil, sectorPeoplePath)
	for _, name := range []string{"ob", "ida"} {
		row, listed := people[w.ids[name]]
		if !listed {
			t.Fatalf("%s is not one of Anvil's people, holding a role on one of its missions", name)
		}
		if got := fmt.Sprintf("%v %v %v %v", row["recordsHeld"], row["recordsHeldKind"], row["recordHeldName"], row["heldHere"]); got != "1 Missions "+first.title+" false" {
			t.Errorf("%s's row of Anvil's people says %s, want 1 Missions %s false: one mission held, and nothing in the sector itself", name, got, first.title)
		}
	}
	own, err := w.app.CrewIdentity().OwnTenants(w.app.ResourceClient()).List(ctx, impulse.Must(impulse.UUIDFromString(w.ids["ob"])))
	if err != nil {
		t.Fatalf("listing ob's own sectors: %v", err)
	}
	if len(own) != 1 || own[0].Domain != anvil {
		t.Errorf("ob's own sectors = %+v, want Anvil alone", own)
	}

	// The holders list of the first mission holds the rows given, and loses the one the lead
	// takes; kit reads the first mission no more.
	if got := w.holders("marshal", first.id); !slices.Equal(got, holdersOfFirst) {
		t.Errorf("the first mission's holders = %v, want %v", got, holdersOfFirst)
	}
	w.done("ida", "take-crew-role-on-mission", w.share(first.id, "kit", "", "MissionObserver", none, none))
	if got, want := w.holders("marshal", first.id), slices.DeleteFunc(slices.Clone(holdersOfFirst), func(row string) bool { return row == "person kit MissionObserver" }); !slices.Equal(got, want) {
		t.Errorf("the first mission's holders after the lead took kit's = %v, want %v", got, want)
	}
	if got := w.listed("kit", "missions"); !slices.Equal(got, kitBefore) {
		t.Errorf("kit lists %v after the lead took the role, want the Cadet's %v", got, kitBefore)
	}

	// The first mission's delete asks first, counting the roles held on it with the rest;
	// confirmed, it takes them, and the people who held only those hold nothing in the sector.
	remove := func(everythingUnder string) (int, []byte) {
		return w.as("keeper", http.MethodPatch, "/console/api/resources", fmt.Sprintf(`[{"op":"remove","path":%q%s}]`, opPath(anvil, "missions/"+first.id), everythingUnder))
	}
	status, body = remove("")
	assertStatus(t, status, http.StatusPreconditionRequired, body)
	if got, want := refusalMessage(t, body), "Delete this mission and everything under it: 1 mission document, 2 sorties, 1 sortie expense, 3 roles held on it. This can't be undone."; got != want {
		t.Errorf("the question = %q, want %q", got, want)
	}
	status, body = remove(`,"value":{"everythingUnder":true}`)
	assertStatus(t, status, http.StatusOK, body)
	params := map[string]any{"mission": first.id}
	if got := rowsStanding(t, w.tenantListsWorld, "SELECT COUNT(*) FROM CrewRecordUserRoles WHERE Resource = 'Missions' AND RecordId = @mission", params) +
		rowsStanding(t, w.tenantListsWorld, "SELECT COUNT(*) FROM CrewRecordGroupRoles WHERE Resource = 'Missions' AND RecordId = @mission", params); got != 0 {
		t.Errorf("%d roles are held on the deleted mission, want none", got)
	}
	people = sectorList(t, w.h, "marshal", anvil, sectorPeoplePath)
	for _, name := range []string{"ob", "ida"} {
		if _, listed := people[w.ids[name]]; listed {
			t.Errorf("%s is one of Anvil's people after the one mission they held a role on was deleted", name)
		}
	}
	if got := w.holders("marshal", second.id); !slices.Equal(got, []string{"person bea MissionObserver"}) {
		t.Errorf("the second mission's holders after the first's delete = %v, want bea's alone", got)
	}
}

// TestRecordRoles_periods holds a role on a mission given for a period: one whose end is
// past already is refused and gives nothing, and one whose end passes during the test stops
// counting, as does a group's role for a member whose place in the group ends.
func TestRecordRoles_periods(t *testing.T) {
	t.Parallel()
	w := newSharingWorld(t)
	first, _ := writeSharedMissions(t, w.tenantListsWorld)

	// Each is a Cadet of Anvil and keeps the role, so each lists the Cadet's missions, and
	// the first mission beside them while a role on it counts.
	for _, name := range []string{"pat", "sam", "gus"} {
		w.enter(name)
	}
	cadets := w.listed("pat", "missions")
	withFirst := slices.Sorted(slices.Values(append(slices.Clone(cadets), first.id)))

	var none time.Time
	status, body := w.as("marshal", http.MethodPost, sectorPath(anvil, "give-crew-role-on-mission"), w.share(first.id, "pat", "", "MissionObserver", none, time.Now().UTC().Add(-time.Minute)))
	assertStatus(t, status, http.StatusBadRequest, body)
	if got := w.listed("pat", "missions"); !slices.Equal(got, cadets) {
		t.Errorf("pat lists %v after a role with an end already past was refused, want the Cadet's %v", got, cadets)
	}
	if got := w.holders("marshal", first.id); len(got) != 0 {
		t.Errorf("the first mission's holders after the refused give = %v, want none", got)
	}

	// Sam's role on the mission and gus's place in the group end a moment from now; the
	// group's role has no end. The rule refuses an end that is past already, so a request
	// that took longer than the moment is given a longer one.
	status, body = w.as("marshal", http.MethodPost, sectorPath(anvil, "create-crew-sector-group"), `{"name":"Short watch","description":"Watches the field for a moment."}`)
	assertStatus(t, status, http.StatusOK, body)
	group := cell[string](t, decodeRow(t, body), "id")
	w.done("marshal", "give-crew-group-role-on-mission", w.share(first.id, "", group, "MissionObserver", none, none))
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
	endsSoon("sam's role on the mission", func(endsAt time.Time) (int, []byte) {
		return w.as("marshal", http.MethodPost, sectorPath(anvil, "give-crew-role-on-mission"), w.share(first.id, "sam", "", "MissionObserver", none, endsAt))
	})
	endsSoon("gus's place in the group", func(endsAt time.Time) (int, []byte) {
		return w.as("marshal", http.MethodPost, sectorPath(anvil, "add-crew-sector-group-member"), periodBody(t, map[string]any{"groupId": group, "userId": w.ids["gus"]}, none, endsAt))
	})
	if before := []string{w.listedJoined("sam"), w.listedJoined("gus")}; time.Now().Before(last) && !slices.Equal(before, []string{fmt.Sprint(withFirst), fmt.Sprint(withFirst)}) {
		t.Errorf("before the ends, sam and gus list %v, want each the Cadet's missions and the first, %v", before, withFirst)
	}
	time.Sleep(time.Until(last.Add(300 * time.Millisecond)))

	for _, name := range []accesstypes.User{"sam", "gus"} {
		if got := w.listed(name, "missions"); !slices.Equal(got, cadets) {
			t.Errorf("%s lists %v after the end passed, want the Cadet's %v", name, got, cadets)
		}
		// A Cadet reads no sortie, so with the role on the mission ended the read is refused.
		if got := w.reads(name, "sorties/"+first.sortie); got != http.StatusForbidden {
			t.Errorf("%s reads the first mission's sortie after the end passed: status %d, want 403", name, got)
		}
	}
	if got := w.holders("marshal", first.id); !slices.Equal(got, []string{"group Short watch MissionObserver"}) {
		t.Errorf("the first mission's holders after sam's role ended = %v, want the group's alone", got)
	}
}

// listedJoined is the person's list of Anvil's missions as one text, for a comparison of
// several people at once.
func (w *sharingWorld) listedJoined(user accesstypes.User) string {
	w.t.Helper()

	return fmt.Sprint(w.listed(user, "missions"))
}
