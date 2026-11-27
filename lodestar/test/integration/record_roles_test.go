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
// held to the observer's role by the grant's condition, and names only a person or a group
// a second role of hers, held in the sector, lists: her role on the mission lists neither
// the sector's people nor its groups. The Muster Clerk's role lists both and gives nothing
// else, so a lead who holds it shares her mission with any person or group of the sector,
// and the seed's harbormaster holds it. Each role given or taken on a mission is a row of
// the sector's audit log, which names the mission by its word and its title as they were.
//
// Each test writes two missions of Anvil of its own, each with a sortie, an expense and a
// document, the first naming the Hammer squadron and the second the Tongs; the third mission
// is the seed's, in Bastion. A person who holds a role on a mission alone is first one of the
// sector's people, as a person invited is, and then has their own role there taken: the
// mission's is all they hold. The people's names are none of the cast's usernames, which
// the harness runs as the cast's own accounts; the test of the seed's harbormaster shares
// with the cast's own.

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
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
func (w *sharingWorld) as(user accesstypes.User, method, path, body string) (status int, answer []byte) {
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

// loggedOnMissions is what Anvil's audit log says, as its marshal reads it, of each role
// given to or taken from the subject named on a mission, oldest first.
func (w *sharingWorld) loggedOnMissions(subjectName string) []string {
	w.t.Helper()

	status, body := w.as("marshal", http.MethodGet, sectorPath(anvil, crewSectorAuditPath+"?limit=200"), "")
	assertStatus(w.t, status, http.StatusOK, body)
	var what []string
	for _, row := range decodeRows(w.t, body) {
		if said := text(row, "what"); text(row, "subjectName") == subjectName && strings.Contains(said, " on the mission ") {
			what = append(what, said)
		}
	}
	slices.Reverse(what)

	return what
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
// its lead: what each reaches and what each is refused, the lists that name them, who
// manages the account of a person who holds the mission alone, and the mission's delete,
// which takes the roles held on it.
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
	// The fleet's administrators disable an account no sector manages.
	provesGrant(t, crew.Roles(), "Headquarters", "Execute", "DisableCrewUser", "managedByTenant = false")

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

	// The lead runs the first mission: a change to it is hers. A sortie names its ship, and
	// she lists no ship, so a sortie under either mission is answered as one that names a
	// row that is not there (TestRecordRoles_aRowAWriteNames); neither is a new mission or a
	// squadron hers.
	patch := func(user accesstypes.User, op, path, value string) (int, []byte) {
		return w.as(user, http.MethodPatch, "/console/api/resources", fmt.Sprintf(`[{"op":%q,"path":%q,"value":%s}]`, op, opPath(anvil, path), value))
	}
	status, body = patch("ida", "add", "sorties", sortieOn(first.id))
	assertStatus(t, status, http.StatusNotFound, body)
	status, body = patch("ida", "patch", "missions/"+first.id, `{"notes":"Charting the outer ring first"}`)
	assertStatus(t, status, http.StatusOK, body)
	status, body = patch("ida", "add", "sorties", sortieOn(second.id))
	assertStatus(t, status, http.StatusNotFound, body)
	status, body = patch("ida", "add", "missions", fmt.Sprintf(`{"clientId":%q,"kindId":"courier","title":"A mission of her own","hazard":1,"fee":"100","deadline":"2027-03-01T08:00:00Z"}`, clientHalvardID))
	assertStatus(t, status, http.StatusForbidden, body)
	status, body = patch("ida", "add", "squadrons", fmt.Sprintf(`{"wingId":%q,"name":"Her own"}`, wingForgeID))
	assertStatus(t, status, http.StatusForbidden, body)

	// The lead may share the first mission, and the sector's people are not hers to list: the
	// method's request names kit from that list, so her share with kit is answered as a row
	// that is not there, and kit lists what kit listed before. Given a role of the test's own
	// that lists the sector's people and gives nothing on missions, she shares the first
	// mission with kit by the method's request, and is refused the second mission and the
	// lead's own role; she lists the first mission's holders. The role is then taken back, so
	// the mission is again all she holds in the sector.
	kitBefore := w.listed("kit", "missions")
	status, body = w.as("ida", http.MethodGet, sectorPath(anvil, sectorPeoplePath), "")
	assertStatus(t, status, http.StatusForbidden, body)
	status, body = w.as("ida", http.MethodPost, sectorPath(anvil, "give-crew-role-on-mission"), w.share(first.id, "kit", "", "MissionObserver", none, none))
	assertStatus(t, status, http.StatusNotFound, body)
	if got, want := refusalMessage(t, body), "A record this one refers to does not exist."; got != want {
		t.Errorf("the lead sharing her mission with one of the sector's people, whom she does not list: the refusal = %q, want %q", got, want)
	}
	if got := w.listed("kit", "missions"); !slices.Equal(got, kitBefore) {
		t.Errorf("kit lists %v after the lead's refused share, want %v", got, kitBefore)
	}
	ida := accesstypes.User(w.ids["ida"])
	w.giveRoleOfItsOwn(ida, "SectorPeopleLister", access.GrantRow{Permission: accesstypes.List, Resource: "CrewSectorUsers"})
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
	if err := w.engine.UserManager().DeleteUserRoles(ctx, accesstypes.DomainPolicyScope(anvil), ida, "SectorPeopleLister"); err != nil {
		t.Fatalf("access.UserManager.DeleteUserRoles(ida, SectorPeopleLister) error = %v", err)
	}

	// The roles the share dialog offers, asked for the method and the mission the dialog is
	// opened on: the Marshal is offered both roles held on a mission, on any of the sector's,
	// and nothing where the request names no mission.
	rolesOffered := func(user accesstypes.User, filter string) (int, []string) {
		status, body := w.as(user, http.MethodGet, sectorPath(anvil, "crew-mission-roles?filter="+filter), "")
		if status != http.StatusOK {
			t.Logf("the roles offered %s with %s: %s", user, filter, body)

			return status, nil
		}

		return status, namesOf(t, decodeRows(t, body), "name")
	}
	onMission := func(mission string) string { return "method:eq:GiveCrewRoleOnMission,recordId:eq:" + mission }
	for _, mission := range []string{first.id, second.id} {
		if status, got := rolesOffered("marshal", onMission(mission)); status != http.StatusOK || !slices.Equal(got, []string{"MissionLead", "MissionObserver"}) {
			t.Errorf("the roles offered the Marshal to give on %s: status %d, %v, want 200 and MissionLead, MissionObserver", mission, status, got)
		}
	}
	if status, got := rolesOffered("marshal", "method:eq:GiveCrewRoleOnMission"); status != http.StatusOK || len(got) != 0 {
		t.Errorf("the roles offered the Marshal with no mission named: status %d, %v, want 200 and none", status, got)
	}
	// The lead holds the list and its fields under her role's one condition, the mission she
	// holds it on, so she filters it by the method and the mission: on her mission the one
	// role she may give, on the other mission none, and none where she names no mission.
	if status, got := rolesOffered("ida", onMission(first.id)); status != http.StatusOK || !slices.Equal(got, []string{"MissionObserver"}) {
		t.Errorf("the roles offered the lead to give on her mission: status %d, %v, want 200 and MissionObserver", status, got)
	}
	if status, got := rolesOffered("ida", onMission(second.id)); status != http.StatusOK || len(got) != 0 {
		t.Errorf("the roles offered the lead to give on the other mission: status %d, %v, want 200 and none", status, got)
	}
	if status, got := rolesOffered("ida", "method:eq:GiveCrewRoleOnMission"); status != http.StatusOK || len(got) != 0 {
		t.Errorf("the roles offered the lead with no mission named: status %d, %v, want 200 and none", status, got)
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

	// The people list: ob is one of the sector's people by the mission alone, which is
	// something held in the sector, so the sector manages ob's account; and the sector is
	// one of ob's own.
	people := sectorList(t, w.h, "marshal", anvil, sectorPeoplePath)
	for _, name := range []string{"ob", "ida"} {
		row, listed := people[w.ids[name]]
		if !listed {
			t.Fatalf("%s is not one of Anvil's people, holding a role on one of its missions", name)
		}
		if got := fmt.Sprintf("%v %v %v %v %v", row["recordsHeld"], row["recordsHeldKind"], row["recordHeldName"], row["heldHere"], row["managedHere"]); got != "1 Missions "+first.title+" true true" {
			t.Errorf("%s's row of Anvil's people says %s, want 1 Missions %s true true: one mission held, held in the sector, which manages the account", name, got, first.title)
		}
	}
	own, err := w.app.CrewIdentity().OwnTenants(w.app.ResourceClient()).List(ctx, impulse.Must(impulse.UUIDFromString(w.ids["ob"])))
	if err != nil {
		t.Fatalf("listing ob's own sectors: %v", err)
	}
	if len(own) != 1 || own[0].Domain != anvil {
		t.Errorf("ob's own sectors = %+v, want Anvil alone", own)
	}

	// The whole fleet's accounts say the same of ob: Anvil manages the account. So hq, who
	// holds Headquarters and nothing else, and whose disable is granted where no sector
	// manages the account, is refused it, and Anvil's Marshal disables and enables it.
	w.ids["hq"] = string(makeCrewAccount(ctx, t, w.db, "hq"))
	w.giveCrewRole(ctx, t, "hq", "Headquarters", accesstypes.GlobalPolicyScope())
	if row := listedAccounts(t, w.h, "hq", "")["ob"]; row == nil || row["managedByTenant"] != true || text(row, "managingTenant") != anvil {
		t.Errorf("ob's row of the accounts = %v, want managedByTenant true and managingTenant Anvil's key, as Anvil's row says managedHere", row)
	}
	status, body = w.as("hq", http.MethodPost, crewMethod("disable-crew-user"), fmt.Sprintf(`{"userId":%q}`, w.ids["ob"]))
	assertStatus(t, status, http.StatusForbidden, body)
	for _, method := range []string{"disable-crew-sector-user", "enable-crew-sector-user"} {
		w.done("marshal", method, fmt.Sprintf(`{"userId":%q}`, w.ids["ob"]))
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
	if got, want := refusalMessage(t, body), "Delete this mission and everything under it: 1 mission document, 1 sortie, 1 sortie expense, 3 roles held on it. This can't be undone."; got != want {
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

	// Anvil's audit log keeps each role given and taken on a mission, naming the mission by
	// its word and its title as they were: the rows still read so once it is deleted. The
	// refused gives wrote none, and the delete writes none of its own.
	onFirst := " on the mission " + first.title
	logged := map[string][]string{
		"kit":          {"Gave the role Mission observer" + onFirst, "Took the role Mission observer" + onFirst},
		"Debris watch": {"Gave the group the role Mission observer" + onFirst},
		"bea":          {"Gave the role Mission observer on the mission " + second.title},
	}
	for subject, want := range logged {
		if got := w.loggedOnMissions(subject); !slices.Equal(got, want) {
			t.Errorf("Anvil's log of the roles on missions of %s = %q, want %q", subject, got, want)
		}
	}

	// The Marshal removes bea, who holds the second mission alone, from the sector by the
	// removal's own request: her role on the mission goes with her.
	status, body = w.removeFromSector(t, "marshal", "bea")
	assertStatus(t, status, http.StatusNoContent, body)
	if got := rowsStanding(t, w.tenantListsWorld, "SELECT COUNT(*) FROM CrewRecordUserRoles WHERE UserId = @user", map[string]any{"user": w.ids["bea"]}); got != 0 {
		t.Errorf("%d roles of bea's are held on missions after her removal from the sector, want none", got)
	}
	if got := w.holders("marshal", second.id); len(got) != 0 {
		t.Errorf("the second mission's holders after bea's removal = %v, want none", got)
	}
	if _, listed := sectorList(t, w.h, "marshal", anvil, sectorPeoplePath)[w.ids["bea"]]; listed {
		t.Error("bea is one of Anvil's people after her removal from the sector")
	}
}

// sortieOn is the body of a sortie under the mission, flown by the Kingfisher.
func sortieOn(mission string) string {
	return fmt.Sprintf(`{"missionId":%q,"shipId":%q,"pilotUserId":"ida","launchedAt":"2027-03-01T08:00:00Z"}`, mission, shipKingfisherID)
}

// TestRecordRoles_aRowAWriteNames holds a write by a role held on a mission to the rows its
// writer can list: a column that names another row is set only to a row the person's own
// list of that kind would show, and any other is answered as a row that is not there. The
// lead of the first mission lists no ship, so her sortie, which names one, is refused; a
// lead who also holds the Engineer's role in the sector, which lists the ships and gives
// nothing on missions, adds it under her mission and not under the other. The lead adds an
// expense to her mission's sortie and not to the other's, and cannot point her mission at a
// squadron it does not name, nor read that squadron, until the Marshal points it there.
func TestRecordRoles_aRowAWriteNames(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	w := newSharingWorld(t)
	first, second := writeSharedMissions(t, w.tenantListsWorld)

	w.enter("ida")
	w.seat(ctx, t, "rex", "Engineer", anvil)
	var none time.Time
	for _, name := range []string{"ida", "rex"} {
		w.done("marshal", "give-crew-role-on-mission", w.share(first.id, name, "", "MissionLead", none, none))
	}
	w.leaveOnly("ida")

	patch := func(user accesstypes.User, op, path, value string) (int, []byte) {
		return w.as(user, http.MethodPatch, "/console/api/resources", fmt.Sprintf(`[{"op":%q,"path":%q,"value":%s}]`, op, opPath(anvil, path), value))
	}
	notThere := func(status int, body []byte, what string) {
		t.Helper()
		assertStatus(t, status, http.StatusNotFound, body)
		if got, want := refusalMessage(t, body), "A record this one refers to does not exist."; got != want {
			t.Errorf("%s: the refusal = %q, want %q", what, got, want)
		}
	}

	status, body := patch("ida", "add", "sorties", sortieOn(first.id))
	notThere(status, body, "the lead's sortie, naming a ship she cannot list")
	status, body = patch("rex", "add", "sorties", sortieOn(first.id))
	assertStatus(t, status, http.StatusOK, body)
	status, body = patch("rex", "add", "sorties", sortieOn(second.id))
	notThere(status, body, "the second lead's sortie under the mission she does not hold")

	expense := func(sortie string) string {
		return fmt.Sprintf(`{"sortieId":%q,"category":"fuel","amount":40,"memo":"Outer ring"}`, sortie)
	}
	status, body = patch("ida", "add", "sortie-expenses", expense(first.sortie))
	assertStatus(t, status, http.StatusOK, body)
	status, body = patch("ida", "add", "sortie-expenses", expense(second.sortie))
	notThere(status, body, "the lead's expense on the other mission's sortie")

	pointAt := func(user accesstypes.User, squadron string) (int, []byte) {
		return patch(user, "patch", "missions/"+first.id, fmt.Sprintf(`{"assignedSquadronId":%q}`, squadron))
	}
	status, body = pointAt("ida", second.squadron)
	notThere(status, body, "the lead pointing her mission at a squadron it does not name")
	if got := w.reads("ida", "squadrons/"+second.squadron); got != http.StatusNotFound {
		t.Errorf("the lead reads the squadron her mission does not name: status %d, want 404", got)
	}
	status, body = pointAt("marshal", second.squadron)
	assertStatus(t, status, http.StatusOK, body)
	if got := w.reads("ida", "squadrons/"+second.squadron); got != http.StatusOK {
		t.Errorf("the lead reads the squadron the Marshal pointed her mission at: status %d, want 200", got)
	}
}

// TestRecordRoles_heldElsewhere holds a role on a mission of another sector to be held
// elsewhere: a Cadet of Anvil whom Anvil manages, once they hold a role on Bastion's beacon
// mission too, is said heldElsewhere and not managedHere by Anvil's list of its people and
// managed by no sector by the whole fleet's accounts, and Anvil's marshal, whose disable is
// granted where the sector manages the account, is refused it.
func TestRecordRoles_heldElsewhere(t *testing.T) {
	t.Parallel()
	w := newSharingWorld(t)

	w.enter("cy")
	facts := func() string {
		row, listed := sectorList(t, w.h, "marshal", anvil, sectorPeoplePath)[w.ids["cy"]]
		if !listed {
			t.Fatal("cy is not one of Anvil's people, holding a Cadet's role there")
		}

		return fmt.Sprintf("%v %v", row["heldElsewhere"], row["managedHere"])
	}
	if got := facts(); got != "false true" {
		t.Errorf("cy, a Cadet of Anvil alone, is said heldElsewhere managedHere %s, want false true", got)
	}

	// The role on Bastion's mission, written as its give writes it: Bastion has no marshal
	// among the cast.
	if _, err := w.db.Apply(t.Context(), []*spanner.Mutation{spanner.InsertMap("CrewRecordUserRoles", map[string]any{
		"Axis": "", "DomainId": bastion, "Resource": "Missions", "RecordId": missionBeaconID, "Role": "MissionObserver",
		"UserId": w.ids["cy"], "CreatedAt": spanner.CommitTimestamp,
	})}); err != nil {
		t.Fatalf("writing cy's role on Bastion's beacon mission: %v", err)
	}
	if got := facts(); got != "true false" {
		t.Errorf("cy, a Cadet of Anvil with a role on a mission of Bastion, is said heldElsewhere managedHere %s, want true false", got)
	}
	// The whole fleet's accounts agree: Bastion, asked for the mission, holds cy elsewhere
	// too, so no sector manages the account.
	if row := listedAccounts(t, w.h, "governor", "")["cy"]; row == nil || row["managedByTenant"] != false || row["managingTenant"] != nil {
		t.Errorf("cy's row of the accounts = %v, want managedByTenant false and no managingTenant", row)
	}
	status, body := w.as("marshal", http.MethodPost, sectorPath(anvil, "disable-crew-sector-user"), fmt.Sprintf(`{"userId":%q}`, w.ids["cy"]))
	assertStatus(t, status, http.StatusForbidden, body)
}

// TestRecordRoles_aGroupsPlaces holds putting a person in a group that holds a role on a
// mission to what giving them that role on the mission needs. A person of the sector whose
// role puts people in groups and shares no mission is refused, in the sentence of the role
// they may not give, and the person is not put in; the same person puts someone in a group
// that holds no role on a mission; the Marshal, who shares every mission, puts them in. The
// first mission's lead, who may also put people in groups, puts someone in a group that
// observes her mission, and is refused one that observes another: her grant is read against
// the mission named. And giving a group a role on a mission needs what giving it to each
// person in the group needs: a person who may give a group a role on a mission and give no
// person one puts someone in a group that holds nothing, and is then refused giving that
// group the role; they give it to a group with nobody in it, and the Marshal to the first.
func TestRecordRoles_aGroupsPlaces(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	w := newSharingWorld(t)
	first, second := writeSharedMissions(t, w.tenantListsWorld)

	// ada and ida seat people in Anvil's groups (a role made in the sector with that grant,
	// beside their Cadet's, and the list of the sector's people the request names a person
	// from); ida also leads the first mission. noa seats people in the groups and gives a
	// group a role on a mission, and gives no person one (a second role made so, which lists
	// the sector's people and its groups). kit and lex are put in the groups.
	for _, name := range []string{"ada", "ida", "kit", "lex", "noa"} {
		w.enter(name)
	}
	const seater accesstypes.Role = "Seater"
	held := accesstypes.DomainPolicyScope(anvil)
	manager := w.engine.UserManager()
	if err := manager.AddRole(ctx, held, seater, "Seats Anvil's own crew in its groups."); err != nil {
		t.Fatalf("access.UserManager.AddRole() error = %v", err)
	}
	if err := manager.AddRoleGrants(ctx, held, seater,
		access.GrantRow{Permission: accesstypes.Execute, Resource: "AddCrewSectorGroupMember"},
		access.GrantRow{Permission: accesstypes.List, Resource: "CrewSectorUsers"},
	); err != nil {
		t.Fatalf("access.UserManager.AddRoleGrants() error = %v", err)
	}
	const groupSharer accesstypes.Role = "GroupSharer"
	if err := manager.AddRole(ctx, held, groupSharer, "Seats Anvil's crew in its groups and shares a mission with a group."); err != nil {
		t.Fatalf("access.UserManager.AddRole() error = %v", err)
	}
	if err := manager.AddRoleGrants(ctx, held, groupSharer,
		access.GrantRow{Permission: accesstypes.Execute, Resource: "AddCrewSectorGroupMember"},
		access.GrantRow{Permission: accesstypes.Execute, Resource: "GiveCrewGroupRoleOnMission"},
		access.GrantRow{Permission: accesstypes.List, Resource: "CrewSectorUsers"},
		access.GrantRow{Permission: accesstypes.List, Resource: "CrewSectorGroups"},
	); err != nil {
		t.Fatalf("access.UserManager.AddRoleGrants() error = %v", err)
	}
	if err := manager.AddUserRoles(ctx, held, accesstypes.User(w.ids["noa"]), groupSharer); err != nil {
		t.Fatalf("access.UserManager.AddUserRoles(noa) error = %v", err)
	}
	for _, name := range []string{"ada", "ida"} {
		if err := manager.AddUserRoles(ctx, held, accesstypes.User(w.ids[name]), seater); err != nil {
			t.Fatalf("access.UserManager.AddUserRoles(%s) error = %v", name, err)
		}
	}
	// The store writes signal a reload and the swap is asynchronous: once ida's grant shows,
	// every answer below reads a policy that holds the roles for all three.
	if d := decisionFor(ctx, t, w.engine.ForUser(accesstypes.User(w.ids["ida"])), accesstypes.DomainScope(anvil), accesstypes.Execute, "AddCrewSectorGroupMember", time.Now().Add(15*time.Second)); !d.IsGranted() {
		t.Fatalf("Execute on AddCrewSectorGroupMember for ida, who holds %s = %s, want granted before the cases run", seater, d)
	}
	var none time.Time
	w.done("marshal", "give-crew-role-on-mission", w.share(first.id, "ida", "", "MissionLead", none, none))

	group := func(name, description string) string {
		t.Helper()
		status, body := w.as("marshal", http.MethodPost, sectorPath(anvil, "create-crew-sector-group"), fmt.Sprintf(`{"name":%q,"description":%q}`, name, description))
		assertStatus(t, status, http.StatusOK, body)

		return cell[string](t, decodeRow(t, body), "id")
	}
	debris := group("Debris watch", "Watches the debris field's charting.")
	w.done("marshal", "give-crew-group-role-on-mission", w.share(first.id, "", debris, "MissionObserver", none, none))
	relay := group("Relay watch", "Watches the relay spares' ferrying.")
	w.done("marshal", "give-crew-group-role-on-mission", w.share(second.id, "", relay, "MissionObserver", none, none))
	hangar := group("Hangar hands", "The crew who work Anvil's hangar deck.")

	put := func(user accesstypes.User, group, person string) (int, []byte) {
		t.Helper()

		return w.as(user, http.MethodPost, sectorPath(anvil, "add-crew-sector-group-member"), fmt.Sprintf(`{"groupId":%q,"userId":%q}`, group, w.ids[person]))
	}
	inGroup := func(group, person string) bool {
		t.Helper()
		for _, row := range decodeRows(t, mustList(t, w.tenantListsWorld, "marshal", sectorGroupPeoplePath)) {
			if cell[string](t, row, "groupId") == group && cell[string](t, row, "username") == person {
				return true
			}
		}

		return false
	}
	refused := func(status int, body []byte, group string) {
		t.Helper()
		assertStatus(t, status, http.StatusForbidden, body)
		want := "Putting a person in the group " + group + " gives them the role Mission observer on one mission, which you may not give them there."
		if got := refusalMessage(t, body); got != want {
			t.Errorf("the refusal = %q, want %q", got, want)
		}
	}

	status, body := put("ada", debris, "kit")
	refused(status, body, "Debris watch")
	if inGroup(debris, "kit") {
		t.Error("kit is in the debris watch after ada was refused")
	}
	status, body = put("ada", hangar, "kit")
	assertStatus(t, status, http.StatusNoContent, body)
	status, body = put("marshal", debris, "kit")
	assertStatus(t, status, http.StatusNoContent, body)
	if !inGroup(debris, "kit") {
		t.Error("kit is not in the debris watch after the Marshal put him in")
	}

	status, body = put("ida", debris, "lex")
	assertStatus(t, status, http.StatusNoContent, body)
	status, body = put("ida", relay, "lex")
	refused(status, body, "Relay watch")
	if inGroup(relay, "lex") {
		t.Error("lex is in the relay watch after ida was refused")
	}

	// noa puts kit in a group that holds nothing, and may then not give that group the
	// observer's role on the first mission: it would give kit the role, which noa may not
	// give him. A group with nobody in it is given the role; the Marshal gives it to kit's.
	spares, standby := group("Spares crew", "Keeps the spares."), group("Standby", "Nobody yet.")
	status, body = put("noa", spares, "kit")
	assertStatus(t, status, http.StatusNoContent, body)
	status, body = w.as("noa", http.MethodPost, sectorPath(anvil, "give-crew-group-role-on-mission"), w.share(first.id, "", spares, "MissionObserver", none, none))
	assertStatus(t, status, http.StatusForbidden, body)
	if got, want := refusalMessage(t, body), "Giving the group Spares crew a role gives everyone in it the role Mission observer on one mission, which you may not give them there."; got != want {
		t.Errorf("the refusal = %q, want %q", got, want)
	}
	if got := w.holders("marshal", first.id); slices.Contains(got, "group Spares crew MissionObserver") {
		t.Errorf("the first mission's holders = %v, and noa was refused the spares crew's role", got)
	}
	w.done("noa", "give-crew-group-role-on-mission", w.share(first.id, "", standby, "MissionObserver", none, none))
	w.done("marshal", "give-crew-group-role-on-mission", w.share(first.id, "", spares, "MissionObserver", none, none))
}

// TestRecordRoles_periods holds a role on a mission given for a period: one whose end is
// past already is refused and gives nothing, and one whose end passes during the test stops
// counting, as does a group's role for a member whose place in the group ends. Nobody makes
// their own role on a mission last longer than it was given for.
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

	// Nobody lengthens their own role on a mission: Wren, a marshal of the sector who may give
	// any, gives herself an observer's role on the first mission until an instant, and then the
	// same role with no end, which is refused in the library's sentence.
	w.seat(t.Context(), t, "wren", "SectorMarshal", anvil)
	own := func(endsAt time.Time) string {
		return w.share(first.id, "wren", "", "MissionObserver", none, endsAt)
	}
	w.done("wren", "give-crew-role-on-mission", own(time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)))
	status, body = w.as("wren", http.MethodPost, sectorPath(anvil, "give-crew-role-on-mission"), own(none))
	assertStatus(t, status, http.StatusForbidden, body)
	if got, want := refusalMessage(t, body), "You cannot make your own role last longer than it was given for."; got != want {
		t.Errorf("a marshal lengthening her own role on a mission is refused saying %q, want %q", got, want)
	}
}

// listedJoined is the person's list of Anvil's missions as one text, for a comparison of
// several people at once.
func (w *sharingWorld) listedJoined(user accesstypes.User) string {
	w.t.Helper()

	return fmt.Sprint(w.listed(user, "missions"))
}

// TestRecordRoles_aLeadSharesWithHerOwnGroup holds a lead's share with a group she is in.
// She holds Sector Crew in Anvil and nothing else, which lists the groups she is in, and
// the Marshal makes her the first mission's lead and puts her in one of two groups. She
// gives her group the observer's role on her mission, and the Marshal's list of the
// mission's holders names it; she is refused the group she is not in and a person of the
// sector, whom no role of hers lists, each as a row that is not there. Given the Muster
// Clerk's role in Anvil, she shares the mission with both; a person of Bastion alone she
// still cannot name, and once the role is taken back neither can she a person of Anvil.
func TestRecordRoles_aLeadSharesWithHerOwnGroup(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	w := newSharingWorld(t)
	first, _ := writeSharedMissions(t, w.tenantListsWorld)

	w.seat(ctx, t, "lyn", "SectorCrew", anvil)
	w.enter("kit")
	group := func(name string) string {
		t.Helper()
		status, body := w.as("marshal", http.MethodPost, sectorPath(anvil, "create-crew-sector-group"), fmt.Sprintf(`{"name":%q,"description":"A group of the test's."}`, name))
		assertStatus(t, status, http.StatusOK, body)

		return cell[string](t, decodeRow(t, body), "id")
	}
	hers, other := group("Outer ring"), group("Inner ring")
	w.done("marshal", "add-crew-sector-group-member", fmt.Sprintf(`{"groupId":%q,"userId":%q}`, hers, w.ids["lyn"]))
	var none time.Time
	w.done("marshal", "give-crew-role-on-mission", w.share(first.id, "lyn", "", "MissionLead", none, none))

	w.done("lyn", "give-crew-group-role-on-mission", w.share(first.id, "", hers, "MissionObserver", none, none))
	holders := []string{"group Outer ring MissionObserver", "person lyn MissionLead"}
	if got := w.holders("marshal", first.id); !slices.Equal(got, holders) {
		t.Errorf("the first mission's holders after the lead shared it with her group = %v, want %v", got, holders)
	}
	notThere := func(status int, body []byte, what string) {
		t.Helper()
		assertStatus(t, status, http.StatusNotFound, body)
		if got, want := refusalMessage(t, body), "A record this one refers to does not exist."; got != want {
			t.Errorf("%s: the refusal = %q, want %q", what, got, want)
		}
	}
	status, body := w.as("lyn", http.MethodPost, sectorPath(anvil, "give-crew-group-role-on-mission"), w.share(first.id, "", other, "MissionObserver", none, none))
	notThere(status, body, "the lead sharing her mission with a group she is not in")
	status, body = w.as("lyn", http.MethodPost, sectorPath(anvil, "give-crew-role-on-mission"), w.share(first.id, "kit", "", "MissionObserver", none, none))
	notThere(status, body, "the lead sharing her mission with one of the sector's people")
	if got := w.holders("marshal", first.id); !slices.Equal(got, holders) {
		t.Errorf("the first mission's holders after the lead's refused shares = %v, want %v", got, holders)
	}

	// The Marshal makes her a Muster Clerk of Anvil, which lists the sector's people and its
	// groups. She shares her mission with the person and with the group she is not in, and
	// still with no one outside the sector; the role taken back, a person of the sector is
	// again a row that is not there. A role given or taken shows in the engine a moment
	// later, so each half waits for it.
	w.enter("pip")
	w.seat(ctx, t, "jem", "Cadet", bastion)
	lyn := w.engine.ForUser(accesstypes.User(w.ids["lyn"]))
	clerk := fmt.Sprintf(`{"userId":%q,"role":"MusterClerk"}`, w.ids["lyn"])
	w.done("marshal", "assign-crew-sector-role", clerk)
	for _, res := range []accesstypes.Resource{"CrewSectorUsers", "CrewSectorGroups"} {
		if d := decisionFor(ctx, t, lyn, accesstypes.DomainScope(anvil), accesstypes.List, res, time.Now().Add(15*time.Second)); !d.IsGranted() {
			t.Fatalf("List on %s for the lead, given MusterClerk = %s, want granted before the cases run", res, d)
		}
	}
	w.done("lyn", "give-crew-role-on-mission", w.share(first.id, "kit", "", "MissionObserver", none, none))
	w.done("lyn", "give-crew-group-role-on-mission", w.share(first.id, "", other, "MissionObserver", none, none))
	holders = []string{"group Inner ring MissionObserver", "group Outer ring MissionObserver", "person kit MissionObserver", "person lyn MissionLead"}
	if got := w.holders("marshal", first.id); !slices.Equal(got, holders) {
		t.Errorf("the first mission's holders after the lead, a Muster Clerk, shared it = %v, want %v", got, holders)
	}
	status, body = w.as("lyn", http.MethodPost, sectorPath(anvil, "give-crew-role-on-mission"), w.share(first.id, "jem", "", "MissionObserver", none, none))
	notThere(status, body, "the lead, a Muster Clerk of Anvil, sharing her mission with a person of Bastion alone")
	if got := w.holders("marshal", first.id); !slices.Equal(got, holders) {
		t.Errorf("the first mission's holders after the lead named a person of Bastion = %v, want %v", got, holders)
	}
	w.done("marshal", "remove-crew-sector-role", clerk)
	deadline := time.Now().Add(15 * time.Second)
	for decisionFor(ctx, t, lyn, accesstypes.DomainScope(anvil), accesstypes.List, "CrewSectorUsers", time.Now()).IsGranted() {
		if time.Now().After(deadline) {
			t.Fatalf("List on CrewSectorUsers for the lead is still granted after MusterClerk was taken back, want denied before the case runs")
		}
		time.Sleep(50 * time.Millisecond)
	}
	status, body = w.as("lyn", http.MethodPost, sectorPath(anvil, "give-crew-role-on-mission"), w.share(first.id, "pip", "", "MissionObserver", none, none))
	notThere(status, body, "the lead sharing her mission with a second person of the sector, her Muster Clerk's role taken back")
	if got := w.holders("marshal", first.id); !slices.Equal(got, holders) {
		t.Errorf("the first mission's holders after the lead's last refused share = %v, want %v", got, holders)
	}
}

// TestRecordRoles_theSeededHarbormasterLeads holds the seed's Harbormaster Hollis, who holds
// the Muster Clerk's role at Anvil beside her own and Sector Crew: made the lead of a
// mission by the Marshal, she shares it with the cadet, one of the sector's people. The
// world writes the cast's accounts, since the sector's people are accounts and the tests
// above make their own people.
func TestRecordRoles_theSeededHarbormasterLeads(t *testing.T) {
	t.Parallel()
	w := &sharingWorld{tenantListsWorld: &tenantListsWorld{identityWorld: newIdentityWorld(t), ids: make(map[string]string)}, t: t}
	first, _ := writeSharedMissions(t, w.tenantListsWorld)
	for _, persona := range []accesstypes.User{"harbormaster", "cadet"} {
		w.ids[string(persona)] = string(account(t, persona))
	}

	var none time.Time
	w.done("marshal", "give-crew-role-on-mission", w.share(first.id, "harbormaster", "", "MissionLead", none, none))
	w.done("harbormaster", "give-crew-role-on-mission", w.share(first.id, "cadet", "", "MissionObserver", none, none))
	holders := []string{"person cadet MissionObserver", "person harbormaster MissionLead"}
	if got := w.holders("marshal", first.id); !slices.Equal(got, holders) {
		t.Errorf("the first mission's holders after Hollis, its lead, shared it with the cadet = %v, want %v", got, holders)
	}
}
