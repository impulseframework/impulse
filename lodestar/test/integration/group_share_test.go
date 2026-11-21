// Demonstrates: @subjectSet.on-groups.
package integration

// The group share suite: a hangar pass that names one of the crew's groups, kept in the
// permission store, as its holder. The pass is every member's while their place in the
// group counts, at the level the pass carries and until it expires, exactly as a pass in
// a person's own name or in a squadron's: the who: of both pass sets tests GroupID IN
// subject.groups beside the person and the squadron, and subject.groups is the set the
// library's group members table declares.
//
// Every person here holds the Armorer's role in the sector and nothing else, so what
// they reach of the hangars they reach through a pass: the grants read hangar IN
// subject.hangarsOpen (List, Read) and hangar IN subject.hangarsRun (Update), and name
// nothing of the passes or of the groups.
//
// The cast's own share is in the seed: the Bay keepers, a group of Anvil's, hold a pass
// to run the Quarantine Bay, and the sentry is in the group and holds no pass and flies
// with no squadron.

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
)

const (
	sentry = accesstypes.User("sentry")

	// The Bay keepers' pass to run the Quarantine Bay, which the bootstrap writes once
	// the group exists (cmd/bootstrap/users.json).
	passBayKeepersQuarantineID = "61000000-0000-4000-8000-000000000006"

	// Bastion's one hangar.
	hangarBastionSlipID = "60000000-0000-4000-8000-000000000003"

	// The passes the suite writes straight into the table, past the create.
	passSlipHandsBastionSlipID   = "61000000-0000-4000-8000-0000000000b1"
	passRefusedID                = "61000000-0000-4000-8000-0000000000b2"
	passSlipKeepersBastionSlipID = "61000000-0000-4000-8000-0000000000b3"
	passNobodyAndGroupID         = "61000000-0000-4000-8000-0000000000b4"
	passBastionOwnID             = "61000000-0000-4000-8000-0000000000b5"
	passDockSlipHandsID          = "61000000-0000-4000-8000-0000000000b6"
	passBayInspectorsID          = "61000000-0000-4000-8000-0000000000b7"

	// groupNotKeptRefusal is the library's answer to a pass that names a group its
	// sector does not keep.
	groupNotKeptRefusal = "groupId names a group that is not one of this sector's own. Choose a group kept in this sector: a group kept in another sector, or for the whole application, cannot be named here."

	// An id that is no pass: nothing writes it.
	passNoneID = "61000000-0000-4000-8000-0000000000bf"
)

// groupShareWorld is a world of the test's own: the seeded rows, the cast's accounts and
// the engines over the same database, so the groups the permission store keeps and the
// passes that name them are in one place, as they are in a served application.
type groupShareWorld struct {
	*identityWorld
	ctx context.Context
}

// newGroupShareWorld builds the world and writes the passes the cast's groups hold.
func newGroupShareWorld(t *testing.T) *groupShareWorld {
	t.Helper()

	w := &groupShareWorld{identityWorld: newIdentityWorld(t), ctx: t.Context()}
	if err := seedDemoGroupPasses(w.ctx, w.db); err != nil {
		t.Fatal(err)
	}

	return w
}

// armorerAt makes a person up and gives them the Armorer's role in each sector named:
// they hold no pass, fly with no squadron and are in no group.
func (w *groupShareWorld) armorerAt(t *testing.T, username accesstypes.User, sectors ...string) accesstypes.User {
	t.Helper()

	person := makeCrewAccount(w.ctx, t, w.db, username)
	for _, sector := range sectors {
		if err := w.engine.UserManager().AddUserRoles(w.ctx, accesstypes.DomainPolicyScope(accesstypes.Domain(sector)), person, "Armorer"); err != nil {
			t.Fatalf("access.UserManager.AddUserRoles(%s, Armorer, %s) error = %v", username, sector, err)
		}
	}

	return person
}

// post runs one of Anvil's methods as the Marshal, who keeps its groups, and answers the
// status and the body.
func (w *groupShareWorld) post(t *testing.T, method, value string) (status int, body []byte) {
	t.Helper()

	return doRequestAs(t, w.h, "marshal", http.MethodPost, sectorPath(anvil, method), value)
}

// hangars is the sector's hangars as the person lists them, by id.
func (w *groupShareWorld) hangars(t *testing.T, user accesstypes.User, sector string) []string {
	t.Helper()

	status, body := doRequestAs(t, w.h, user, http.MethodGet, sectorPath(sector, "hangars"), "")
	assertStatus(t, status, http.StatusOK, body)

	return idsOf(t, decodeRows(t, body))
}

// rezone writes the zone of one of Anvil's hangars as the person and answers the status:
// the write the run level decides.
func (w *groupShareWorld) rezone(t *testing.T, user accesstypes.User, hangar, zone string) int {
	t.Helper()

	status, _ := doRequestAs(t, w.h, user, http.MethodPatch, sectorPath(anvil, "hangars"),
		fmt.Sprintf(`[{"op":"patch","path":"/%s","value":{"zone":%q}}]`, hangar, zone))

	return status
}

// groupsOffered is what the group picker on a pass offers in Anvil: the sector's own list
// of its groups, as the Marshal, who writes its passes, reads it, the id of each by its
// name.
func (w *groupShareWorld) groupsOffered(t *testing.T) map[string]string {
	t.Helper()

	status, body := doRequestAs(t, w.h, "marshal", http.MethodGet, sectorPath(anvil, sectorGroupsPath+"?limit=200"), "")
	assertStatus(t, status, http.StatusOK, body)
	offered := make(map[string]string)
	for _, row := range decodeRows(t, body) {
		offered[cell[string](t, row, "name")] = cell[string](t, row, "id")
	}

	return offered
}

// createPass writes a pass in Anvil as the Marshal, through the route the console's form
// uses, and answers the status, the body and the id of the pass where one was written.
func (w *groupShareWorld) createPass(t *testing.T, value string) (status int, body []byte, id string) {
	t.Helper()

	status, body = doRequestAs(t, w.h, "marshal", http.MethodPatch, consoleAPI+"/resources",
		fmt.Sprintf(`[{"op":"add","path":%q,"value":%s}]`, opPath(anvil, "hangar-passes"), value))
	if status != http.StatusOK {
		return status, body, ""
	}
	ids, _ := decodeRow(t, body)["hangarPasses"].([]any)
	if len(ids) != 1 {
		t.Fatalf("created ids = %v, want one pass id: %s", ids, body)
	}
	id, _ = ids[0].(string)

	return status, body, id
}

// groupPass is the value of a pass on Anvil's dock that names a group.
func groupPass(group, level string) string {
	return fmt.Sprintf(`{"hangarId":%q,"groupId":%q,"levelId":%q}`, hangarAnvilDockID, group, level)
}

// passes counts the passes a query finds, read straight from the database.
func (w *groupShareWorld) passes(t *testing.T, where string, params map[string]any) int64 {
	t.Helper()

	iter := w.db.Client.Single().Query(w.ctx, spanner.Statement{SQL: "SELECT COUNT(*) FROM HangarPasses WHERE " + where, Params: params})
	defer iter.Stop()
	row, err := iter.Next()
	if err != nil {
		t.Fatalf("counting the passes where %s: %v", where, err)
	}
	var n int64
	if err := row.Columns(&n); err != nil {
		t.Fatalf("reading the count of the passes where %s: %v", where, err)
	}

	return n
}

// updatePass sends an update of one pass as the person, through the route the console
// uses, in Anvil, whatever sector the pass is in, and answers the status and the body.
func (w *groupShareWorld) updatePass(t *testing.T, user accesstypes.User, pass, value string) (status int, body []byte) {
	t.Helper()

	return doRequestAs(t, w.h, user, http.MethodPatch, consoleAPI+"/resources",
		fmt.Sprintf(`[{"op":"patch","path":%q,"value":%s}]`, opPath(anvil, "hangar-passes/"+pass), value))
}

// holderOf is who a pass names, read straight from the table.
func (w *groupShareWorld) holderOf(t *testing.T, pass string) string {
	t.Helper()

	row, err := w.db.Single().ReadRow(w.ctx, "HangarPasses", spanner.Key{pass}, []string{"UserId", "SquadronId", "GroupId"})
	if err != nil {
		t.Fatalf("reading the holder of the pass %s: %v", pass, err)
	}
	var user, squadron, group spanner.NullString
	if err := row.Columns(&user, &squadron, &group); err != nil {
		t.Fatalf("reading the holder columns of the pass %s: %v", pass, err)
	}

	return fmt.Sprintf("person %s, squadron %s, group %s", user, squadron, group)
}

// passClerk makes a person up under the username and gives them, in Anvil, a role of the
// test's own that is handed Update on a pass and on every column of one, the three
// holder columns among them. No role file may grant those three (a role file that
// grants Update on an immutable column is refused when it is read), and the release's
// engine writes a role no grant the release does not declare, so the rows are written
// to the store by an engine that was told nothing of what the release declares
// (engineThatChecksNoName). The release's engine holds what the collection offers:
// the hangar, the level and the expiry are the teller's to write, and the three holder
// rows are skipped, because the collection takes Update off an immutable column. So the
// role is the most a role can hold of a pass, and a holder column is nobody's to write
// whatever a store holds.
func (w *groupShareWorld) passClerk(t *testing.T, username accesstypes.User) {
	t.Helper()

	const clerk accesstypes.Role = "PassClerk"
	manager := w.engine.UserManager()
	held := accesstypes.DomainPolicyScope(anvil)
	if err := manager.AddRole(w.ctx, held, clerk, "Is handed every column of a hangar pass."); err != nil {
		t.Fatalf("access.UserManager.AddRole() error = %v", err)
	}
	if err := engineThatChecksNoName(t, w.db).UserManager().AddRoleGrants(w.ctx, held, clerk,
		access.GrantRow{Permission: accesstypes.Update, Resource: "HangarPasses"},
		access.GrantRow{Permission: accesstypes.Update, Resource: "HangarPasses.hangarId"},
		access.GrantRow{Permission: accesstypes.Update, Resource: "HangarPasses.userId"},
		access.GrantRow{Permission: accesstypes.Update, Resource: "HangarPasses.squadronId"},
		access.GrantRow{Permission: accesstypes.Update, Resource: "HangarPasses.groupId"},
		access.GrantRow{Permission: accesstypes.Update, Resource: "HangarPasses.levelId"},
		access.GrantRow{Permission: accesstypes.Update, Resource: "HangarPasses.expiresAt"},
	); err != nil {
		t.Fatalf("access.UserManager.AddRoleGrants() error = %v", err)
	}
	person := makeCrewAccount(w.ctx, t, w.db, username)
	if err := manager.AddUserRoles(w.ctx, held, person, clerk); err != nil {
		t.Fatalf("access.UserManager.AddUserRoles() error = %v", err)
	}
	checker := w.engine.ForUser(person)
	scope := accesstypes.DomainScope(anvil)
	// The rows are written in one call, so once one of them counts they have all been read.
	for _, column := range []accesstypes.Resource{"HangarPasses.hangarId", "HangarPasses.levelId", "HangarPasses.expiresAt"} {
		if d := decisionFor(w.ctx, t, checker, scope, accesstypes.Update, column, time.Now().Add(15*time.Second)); !d.IsGranted() {
			t.Fatalf("Update on %s for %s, who holds %s = %s, want granted before the cases run", column, username, clerk, d)
		}
	}
	for _, column := range []accesstypes.Resource{"HangarPasses.userId", "HangarPasses.squadronId", "HangarPasses.groupId"} {
		if d := decisionFor(w.ctx, t, checker, scope, accesstypes.Update, column, time.Now()); d.IsGranted() {
			t.Errorf("Update on %s for %s, whose role %s was handed it = %s, want it denied: the column is immutable, and the engine skips a grant the collection does not offer", column, username, clerk, d)
		}
	}
}

// refusalOf is the sentence a refused request answers with.
func refusalOf(t *testing.T, body []byte) string {
	t.Helper()

	message, _ := decodeRow(t, body)["message"].(string)

	return message
}

// TestGroupShare_persona holds the cast's own share, read from the seed: the sentry
// holds the Armorer's role in Anvil, no pass and no squadron, and is one of the Bay
// keepers, who hold a pass to run the Quarantine Bay. He sees the bay, through the group
// alone, and no other hangar. A pass in another sector that names a group he is in opens
// nothing there until he holds a role there: that part writes its own group and pass, in
// a world of its own.
func TestGroupShare_persona(t *testing.T) {
	t.Parallel()

	_, h, _ := sharedWorld(t)
	provesArmorerGrants(t)

	t.Run("the list is the bay his group runs, and not the dock", func(t *testing.T) {
		t.Parallel()

		status, body := doRequestAs(t, h, sentry, http.MethodGet, sectorPath(anvil, "hangars"), "")
		assertStatus(t, status, http.StatusOK, body)
		if got, want := idsOf(t, decodeRows(t, body)), []string{hangarQuarantineID}; !slices.Equal(got, want) {
			t.Errorf("the hangars open to the sentry = %v, want %v: the bay, through the Bay keepers", got, want)
		}
	})

	t.Run("he reads the bay by its id", func(t *testing.T) {
		t.Parallel()

		status, body := doRequestAs(t, h, sentry, http.MethodGet, sectorPath(anvil, "hangars/"+hangarQuarantineID), "")
		assertStatus(t, status, http.StatusOK, body)
	})

	t.Run("a sector where he holds no role is concealed, whatever its passes say", func(t *testing.T) {
		t.Parallel()

		// A world of the subtest's own, since it writes a group and a pass the shared
		// world's other suites would read: Bastion keeps a group with the sentry in it,
		// and the group holds a pass to run Bastion's slip.
		w := newGroupShareWorld(t)
		manager := w.engine.UserManager()
		slipKeepers, err := manager.AddGroup(w.ctx, accesstypes.DomainPolicyScope(bastion), "Slip keepers", "The crew who keep Bastion's slip.")
		if err != nil {
			t.Fatalf("access.UserManager.AddGroup(Slip keepers) error = %v", err)
		}
		if err := manager.AddGroupUsers(w.ctx, slipKeepers, account(t, sentry)); err != nil {
			t.Fatalf("access.UserManager.AddGroupUsers(Slip keepers, sentry) error = %v", err)
		}
		pass := spanner.Insert("HangarPasses", []string{"Id", "SectorId", "HangarId", "GroupId", "LevelId"}, []any{passSlipKeepersBastionSlipID, bastion, hangarBastionSlipID, string(slipKeepers), "run"})
		if _, err := w.db.Apply(w.ctx, []*spanner.Mutation{pass}); err != nil {
			t.Fatalf("writing Bastion's pass in the name of the sentry's group: %v", err)
		}

		// The pass names a group of Bastion's that he is in, and he holds no role in
		// Bastion: a pass narrows what a role gives and gives nothing by itself, so the
		// sector is concealed from him.
		status, body := doRequestAs(t, w.h, sentry, http.MethodGet, sectorPath(bastion, "hangars"), "")
		assertStatus(t, status, http.StatusNotFound, body)

		// The same pass opens the slip once he holds the Armorer's role there: it was a
		// pass that counts all along, and the role was the one thing missing.
		if err := manager.AddUserRoles(w.ctx, accesstypes.DomainPolicyScope(bastion), account(t, sentry), "Armorer"); err != nil {
			t.Fatalf("access.UserManager.AddUserRoles(sentry, Armorer, Bastion) error = %v", err)
		}
		if got, want := w.hangars(t, sentry, bastion), []string{hangarBastionSlipID}; !slices.Equal(got, want) {
			t.Errorf("the hangars of Bastion open to the sentry once he is its Armorer = %v, want %v: the slip, through the Slip keepers", got, want)
		}
	})
}

// TestGroupShare_levels holds a pass in a group's name from its writing to its expiry:
// the Marshal picks the group from the sector's own, the pass is every member's and
// nobody else's, a visit opens the hangar and does not let a member run it, the run
// level does, and a pass that has expired gives nothing. Each step changes what the next
// one reads, so the steps are one test and each failure names its step.
func TestGroupShare_levels(t *testing.T) {
	t.Parallel()

	w := newGroupShareWorld(t)
	provesArmorerGrants(t)
	keeper := w.armorerAt(t, "keeper", anvil)
	w.armorerAt(t, "outsider", anvil)

	open := func(when string, user accesstypes.User, want ...string) {
		t.Helper()
		if got := w.hangars(t, user, anvil); !slices.Equal(got, want) {
			t.Errorf("%s: the hangars open to %s = %v, want %v", when, user, got, want)
		}
	}
	runs := func(when string, user accesstypes.User, zone string, want int) {
		t.Helper()
		if got := w.rezone(t, user, hangarAnvilDockID, zone); got != want {
			t.Errorf("%s: %s re-zones the dock: status = %d, want %d", when, user, got, want)
		}
	}

	// The Marshal makes a group in her sector and puts the keeper in it. The group holds
	// no pass yet, and a place in it gives no hangar.
	status, body := w.post(t, "create-crew-sector-group", `{"name":"Dock hands","description":"The crew who keep Anvil Dock One."}`)
	assertStatus(t, status, http.StatusOK, body)
	group := cell[string](t, decodeRow(t, body), "id")
	status, body = w.post(t, "add-crew-sector-group-member", fmt.Sprintf(`{"groupId":%q,"userId":%q}`, group, keeper))
	assertStatus(t, status, http.StatusNoContent, body)
	open("before the group holds a pass", "keeper")

	// The picker on the pass offers the sector's groups, the new one among them, and a
	// pass written with the id it offers holds: at the visit level the dock is open to
	// the group's member, who may not run it.
	if got := w.groupsOffered(t)["Dock hands"]; got != group {
		t.Fatalf("the group picker offers Dock hands as %q, want the id the create answered, %q", got, group)
	}
	status, body, pass := w.createPass(t, groupPass(group, "visit"))
	assertStatus(t, status, http.StatusOK, body)
	open("the group visits the dock", "keeper", hangarAnvilDockID)
	status, body = doRequestAs(t, w.h, "keeper", http.MethodGet, sectorPath(anvil, "hangars/"+hangarAnvilDockID), "")
	assertStatus(t, status, http.StatusOK, body)
	runs("the group visits the dock", "keeper", "apron", http.StatusForbidden)

	// At the run level the member re-zones the dock. A person outside the group holds
	// the same role and reaches nothing: the pass is the group's, not the sector's.
	if _, err := w.db.Apply(w.ctx, []*spanner.Mutation{spanner.UpdateMap("HangarPasses", map[string]any{"Id": pass, "LevelId": "run"})}); err != nil {
		t.Fatalf("raising the group's pass to the run level: %v", err)
	}
	runs("the group runs the dock", "keeper", "apron", http.StatusOK)
	if got := readColumn[string](w.ctx, t, w.db, "Hangars", spanner.Key{hangarAnvilDockID}, "Zone"); got != "apron" {
		t.Errorf("the dock's zone = %q after the member's write, want apron", got)
	}
	open("the group runs the dock", "outsider")
	runs("the group runs the dock", "outsider", "dock", http.StatusForbidden)

	// A pass that has expired gives nothing, with nobody deleting it, and counts again
	// once it is extended.
	if _, err := w.db.Apply(w.ctx, []*spanner.Mutation{spanner.UpdateMap("HangarPasses", map[string]any{"Id": pass, "ExpiresAt": time.Now().UTC().Add(-time.Hour)})}); err != nil {
		t.Fatalf("expiring the group's pass: %v", err)
	}
	open("the group's pass has expired", "keeper")
	runs("the group's pass has expired", "keeper", "dock", http.StatusForbidden)
	if _, err := w.db.Apply(w.ctx, []*spanner.Mutation{spanner.UpdateMap("HangarPasses", map[string]any{"Id": pass, "ExpiresAt": time.Now().UTC().AddDate(0, 1, 0)})}); err != nil {
		t.Fatalf("extending the group's pass: %v", err)
	}
	open("the group's pass is extended", "keeper", hangarAnvilDockID)
	runs("the group's pass is extended", "keeper", "dock", http.StatusOK)
}

// TestGroupShare_places holds the pass to the person's place in the group: a place that
// has not begun, or that has ended, gives nothing; a person taken out of the group loses
// the hangar at their next request; and a group that is deleted leaves no pass that
// names it. It runs on the cast's own share, the Bay keepers' pass to run the Quarantine
// Bay.
func TestGroupShare_places(t *testing.T) {
	t.Parallel()

	w := newGroupShareWorld(t)
	provesArmorerGrants(t)
	novice := w.armorerAt(t, "novice", anvil)

	open := func(when string, user accesstypes.User, want ...string) {
		t.Helper()
		if got := w.hangars(t, user, anvil); !slices.Equal(got, want) {
			t.Errorf("%s: the hangars open to %s = %v, want %v", when, user, got, want)
		}
	}
	runs := func(when string, user accesstypes.User, zone string, want int) {
		t.Helper()
		if got := w.rezone(t, user, hangarQuarantineID, zone); got != want {
			t.Errorf("%s: %s re-zones the bay: status = %d, want %d", when, user, got, want)
		}
	}
	group := w.groupsOffered(t)["Bay keepers"]
	if group == "" {
		t.Fatal("Anvil's groups do not list the Bay keepers")
	}
	// A place in the group names the person as the permission store does, by the id of
	// their account.
	place := func(person accesstypes.User) map[string]any {
		return map[string]any{"groupId": group, "userId": string(person)}
	}
	member := func(person accesstypes.User) string {
		return periodBody(t, place(person), time.Time{}, time.Time{})
	}

	// The sentry is in the group and runs the bay. The novice holds the same role and is
	// not in it.
	open("as the cast is seeded", sentry, hangarQuarantineID)
	runs("as the cast is seeded", sentry, "isolation", http.StatusOK)
	open("before the novice is in the group", "novice")

	// A place that begins later gives nothing until it begins.
	later := time.Now().UTC().AddDate(0, 0, 7).Truncate(time.Microsecond)
	status, body := w.post(t, "add-crew-sector-group-member", periodBody(t, place(novice), later, time.Time{}))
	assertStatus(t, status, http.StatusNoContent, body)
	open("the novice's place begins in a week", "novice")
	runs("the novice's place begins in a week", "novice", "quarantine", http.StatusForbidden)

	// Given again with no start, the place counts at the next request.
	status, body = w.post(t, "add-crew-sector-group-member", member(novice))
	assertStatus(t, status, http.StatusNoContent, body)
	open("the novice's place counts", "novice", hangarQuarantineID)
	runs("the novice's place counts", "novice", "quarantine", http.StatusOK)

	// A place that has ended gives nothing, with nobody taking the person out. The
	// method refuses an end that is past already, so the end is written as the clock
	// would have brought it.
	ended := spanner.UpdateMap("CrewGroupMembers", map[string]any{"GroupId": group, "UserId": string(novice), "EndsAt": time.Now().UTC().Add(-time.Minute)})
	if _, err := w.db.Apply(w.ctx, []*spanner.Mutation{ended}); err != nil {
		t.Fatalf("ending the novice's place: %v", err)
	}
	open("the novice's place has ended", "novice")
	runs("the novice's place has ended", "novice", "isolation", http.StatusForbidden)

	// Taken out of the group, the sentry loses the bay at his next request.
	status, body = w.post(t, "remove-crew-sector-group-member", member(account(t, sentry)))
	assertStatus(t, status, http.StatusNoContent, body)
	open("the sentry is out of the group", sentry)
	runs("the sentry is out of the group", sentry, "isolation", http.StatusForbidden)

	// The group is deleted, and its pass goes with it: no pass is left that names a group
	// nobody keeps. Nobody whose place counts is left in it; the novice's ended place does
	// not stand in the way, and goes with the group.
	if got := w.passes(t, "GroupId = @group", map[string]any{"group": group}); got != 1 {
		t.Fatalf("the passes that name the Bay keepers = %d before the group is deleted, want 1", got)
	}
	status, body = w.post(t, "delete-crew-sector-group", fmt.Sprintf(`{"groupId":%q}`, group))
	assertStatus(t, status, http.StatusNoContent, body)
	if got := w.passes(t, "GroupId = @group OR Id = @pass", map[string]any{"group": group, "pass": passBayKeepersQuarantineID}); got != 0 {
		t.Errorf("the passes that name the Bay keepers = %d after the group is deleted, want none", got)
	}
	if got := w.passes(t, "GroupId IS NOT NULL AND GroupId NOT IN (SELECT Id FROM CrewGroups)", nil); got != 0 {
		t.Errorf("%d passes name a group that is not kept, want none", got)
	}
}

// TestGroupShare_tenants holds a group to the sector that keeps it. subject.groups is
// read under the request's sector: in a sector's request it holds the groups kept in
// that sector, and neither another sector's group nor a group kept for the whole fleet.
// So a pass of Anvil's that names one of those opens nothing, shown with a row written
// straight into the table, past the library, which refuses to write one
// (TestGroupShare_groupNotKept); the picker offers neither; and the same person reaches
// Bastion's hangar through Bastion's group, in Bastion alone.
func TestGroupShare_tenants(t *testing.T) {
	t.Parallel()

	w := newGroupShareWorld(t)
	provesArmorerGrants(t)
	keeper := w.armorerAt(t, "keeper", anvil, bastion)
	manager := w.engine.UserManager()

	// Three groups with the keeper in each: one of Bastion's, one kept for the whole
	// fleet, and one of Anvil's.
	groupOf := func(scope accesstypes.PolicyScope, name string) string {
		t.Helper()

		id, err := manager.AddGroup(w.ctx, scope, name, "A group of the group share suite.")
		if err != nil {
			t.Fatalf("access.UserManager.AddGroup(%s) error = %v", name, err)
		}
		if err := manager.AddGroupUsers(w.ctx, id, keeper); err != nil {
			t.Fatalf("access.UserManager.AddGroupUsers(%s) error = %v", name, err)
		}

		return string(id)
	}
	slipHands := groupOf(accesstypes.DomainPolicyScope(bastion), "Slip hands")
	inspectors := groupOf(accesstypes.GlobalPolicyScope(), "Fleet inspectors")
	dockHands := groupOf(accesstypes.DomainPolicyScope(anvil), "Dock hands")

	open := func(when, sector string, want ...string) {
		t.Helper()
		if got := w.hangars(t, "keeper", sector); !slices.Equal(got, want) {
			t.Errorf("%s: the hangars open to the keeper = %v, want %v", when, got, want)
		}
	}

	// The picker in Anvil offers Anvil's groups and no other.
	offered := w.groupsOffered(t)
	if offered["Dock hands"] != dockHands {
		t.Errorf("the group picker in Anvil offers Dock hands as %q, want %q", offered["Dock hands"], dockHands)
	}
	for _, name := range []string{"Slip hands", "Fleet inspectors"} {
		if id, listed := offered[name]; listed {
			t.Errorf("the group picker in Anvil offers %s (%s), which Anvil does not keep", name, id)
		}
	}

	// A pass of Anvil's that names Bastion's group opens nothing in Anvil: the keeper is
	// in the group, and the group is not Anvil's. No request writes such a pass, so the
	// row goes straight into the table.
	past := func(id, hangar, group string) {
		t.Helper()
		pass := spanner.Insert("HangarPasses", []string{"Id", "SectorId", "HangarId", "GroupId", "LevelId"}, []any{id, anvil, hangar, group, "run"})
		if _, err := w.db.Apply(w.ctx, []*spanner.Mutation{pass}); err != nil {
			t.Fatalf("writing the pass %s past the library: %v", id, err)
		}
	}
	past(passDockSlipHandsID, hangarAnvilDockID, slipHands)
	open("a pass of Anvil's names Bastion's group", anvil)
	if got := w.rezone(t, "keeper", hangarAnvilDockID, "apron"); got != http.StatusForbidden {
		t.Errorf("the keeper re-zones Anvil's dock through Bastion's group: status = %d, want 403", got)
	}

	// A pass of Anvil's that names a group kept for the whole fleet opens nothing in
	// Anvil either: a sector's request counts the sector's own groups.
	past(passBayInspectorsID, hangarQuarantineID, inspectors)
	open("a pass of Anvil's names a group of the whole fleet", anvil)
	if got := w.rezone(t, "keeper", hangarQuarantineID, "isolation"); got != http.StatusForbidden {
		t.Errorf("the keeper re-zones Anvil's bay through a group of the whole fleet: status = %d, want 403", got)
	}

	// The same person, the same kind of pass, and a group Anvil keeps: the dock opens.
	status, body, _ := w.createPass(t, groupPass(dockHands, "visit"))
	assertStatus(t, status, http.StatusOK, body)
	open("a pass of Anvil's names Anvil's group", anvil, hangarAnvilDockID)

	// Bastion's group opens Bastion's hangar, in Bastion: each sector's request reads
	// its own passes and its own groups.
	slip := spanner.Insert("HangarPasses", []string{"Id", "SectorId", "HangarId", "GroupId", "LevelId"}, []any{passSlipHandsBastionSlipID, bastion, hangarBastionSlipID, slipHands, "visit"})
	if _, err := w.db.Apply(w.ctx, []*spanner.Mutation{slip}); err != nil {
		t.Fatalf("writing Bastion's pass in the name of its group: %v", err)
	}
	open("a pass of Bastion's names Bastion's group", bastion, hangarBastionSlipID)
	open("after Bastion's pass, in Anvil", anvil, hangarAnvilDockID)
}

// TestGroupShare_groupNotKept holds the refusal of a pass that names a group its sector
// does not keep, by requests written by hand, since the picker offers no such group: a
// create in Anvil that names Bastion's group, a group kept for the whole fleet, or an id
// that is no group's is refused with the library's one sentence, which says which groups
// a pass may name and nothing of what another sector keeps, and writes no row; the same
// request with a group Anvil keeps is written. The rule is the library's (the generated
// GroupColumns of HangarPass), so nothing in Lodestar states it.
func TestGroupShare_groupNotKept(t *testing.T) {
	t.Parallel()

	w := newGroupShareWorld(t)
	manager := w.engine.UserManager()
	groupOf := func(scope accesstypes.PolicyScope, name string) string {
		t.Helper()

		id, err := manager.AddGroup(w.ctx, scope, name, "A group of the group share suite.")
		if err != nil {
			t.Fatalf("access.UserManager.AddGroup(%s) error = %v", name, err)
		}

		return string(id)
	}
	slipHands := groupOf(accesstypes.DomainPolicyScope(bastion), "Slip hands")
	inspectors := groupOf(accesstypes.GlobalPolicyScope(), "Fleet inspectors")
	dockHands := groupOf(accesstypes.DomainPolicyScope(anvil), "Dock hands")

	tests := []struct {
		name  string
		group string
	}{
		{name: "a group Bastion keeps", group: slipHands},
		{name: "a group kept for the whole fleet", group: inspectors},
		{name: "an id that is no group's", group: "62000000-0000-4000-8000-0000000000ff"},
	}
	for _, tt := range tests {
		status, body, _ := w.createPass(t, groupPass(tt.group, "run"))
		if status != http.StatusBadRequest {
			t.Errorf("a pass of Anvil's that names %s: status = %d, want 400: %s", tt.name, status, body)
		}
		if got := refusalOf(t, body); got != groupNotKeptRefusal {
			t.Errorf("a pass of Anvil's that names %s is answered\n  %s\nwant\n  %s", tt.name, got, groupNotKeptRefusal)
		}
		if n := w.passes(t, "GroupId = @group", map[string]any{"group": tt.group}); n != 0 {
			t.Errorf("a pass of Anvil's that names %s: %d passes name the group, want none written", tt.name, n)
		}
	}

	status, body, id := w.createPass(t, groupPass(dockHands, "run"))
	assertStatus(t, status, http.StatusOK, body)
	if n := w.passes(t, "Id = @id AND GroupId = @group", map[string]any{"id": id, "group": dockHands}); n != 1 {
		t.Errorf("a pass of Anvil's that names Anvil's own group: %d rows written, want 1", n)
	}
}

// TestGroupShare_oneHolder holds a pass to one holder: a person, a squadron or a group.
// The create refuses a pass that names two of them, or none, in words whoever fills the
// form can act on, and the table's holder check refuses the same row from a writer that
// does not come through the create. An empty person is nobody to both: alone it is no
// holder, and beside a group it leaves the group the one holder.
func TestGroupShare_oneHolder(t *testing.T) {
	t.Parallel()

	w := newGroupShareWorld(t)
	group := w.groupsOffered(t)["Bay keepers"]
	if group == "" {
		t.Fatal("Anvil's groups do not list the Bay keepers")
	}
	person := string(account(t, armorer))
	written := w.passes(t, "TRUE", nil)

	refused := []struct {
		name  string
		value string
		says  string
	}{
		{
			name:  "a person and a group",
			value: fmt.Sprintf(`{"hangarId":%q,"userId":%q,"groupId":%q,"levelId":"visit"}`, hangarAnvilDockID, person, group),
			says:  "a pass has one holder",
		},
		{
			name:  "a squadron and a group",
			value: fmt.Sprintf(`{"hangarId":%q,"squadronId":%q,"groupId":%q,"levelId":"visit"}`, hangarAnvilDockID, squadronTongsID, group),
			says:  "a pass has one holder",
		},
		{
			name:  "all three",
			value: fmt.Sprintf(`{"hangarId":%q,"userId":%q,"squadronId":%q,"groupId":%q,"levelId":"visit"}`, hangarAnvilDockID, person, squadronTongsID, group),
			says:  "a pass has one holder",
		},
		{
			name:  "no holder",
			value: fmt.Sprintf(`{"hangarId":%q,"levelId":"visit"}`, hangarAnvilDockID),
			says:  "a pass needs a holder",
		},
		{
			name:  "an empty person, who is nobody",
			value: fmt.Sprintf(`{"hangarId":%q,"userId":"","levelId":"visit"}`, hangarAnvilDockID),
			says:  "a pass needs a holder",
		},
	}
	for _, tt := range refused {
		status, body, _ := w.createPass(t, tt.value)
		if status != http.StatusBadRequest {
			t.Errorf("a pass that names %s: status = %d, want 400: %s", tt.name, status, body)

			continue
		}
		if !strings.Contains(string(body), tt.says) || !strings.Contains(string(body), "a person, a squadron or a group") {
			t.Errorf("the refusal of a pass that names %s = %s, want it to say %q and name the three holders", tt.name, body, tt.says)
		}
	}

	// The table holds the same rule for a writer that does not come through the create.
	rows := []struct {
		name    string
		columns []string
		values  []any
	}{
		{
			name:    "a person and a group",
			columns: []string{"Id", "SectorId", "HangarId", "UserId", "GroupId", "LevelId"},
			values:  []any{passRefusedID, anvil, hangarAnvilDockID, person, group, "visit"},
		},
		{
			name:    "a squadron and a group",
			columns: []string{"Id", "SectorId", "HangarId", "SquadronId", "GroupId", "LevelId"},
			values:  []any{passRefusedID, anvil, hangarAnvilDockID, squadronTongsID, group, "visit"},
		},
		{
			name:    "no holder",
			columns: []string{"Id", "SectorId", "HangarId", "LevelId"},
			values:  []any{passRefusedID, anvil, hangarAnvilDockID, "visit"},
		},
		{
			name:    "an empty person, who is nobody",
			columns: []string{"Id", "SectorId", "HangarId", "UserId", "LevelId"},
			values:  []any{passRefusedID, anvil, hangarAnvilDockID, "", "visit"},
		},
	}
	for _, tt := range rows {
		_, err := w.db.Apply(w.ctx, []*spanner.Mutation{spanner.Insert("HangarPasses", tt.columns, tt.values)})
		if err == nil {
			t.Errorf("the table took a pass that names %s, want it refused by CK_HangarPasses_Holder", tt.name)

			continue
		}
		if !strings.Contains(err.Error(), "CK_HangarPasses_Holder") {
			t.Errorf("the table refused a pass that names %s with %v, want the refusal of CK_HangarPasses_Holder", tt.name, err)
		}
	}
	if got := w.passes(t, "TRUE", nil); got != written {
		t.Errorf("the passes after the refused writes = %d, want the %d there were", got, written)
	}

	// One holder of each kind is taken.
	status, body, _ := w.createPass(t, groupPass(group, "visit"))
	assertStatus(t, status, http.StatusOK, body)
	status, body, _ = w.createPass(t, fmt.Sprintf(`{"hangarId":%q,"userId":%q,"levelId":"visit"}`, hangarAnvilDockID, person))
	assertStatus(t, status, http.StatusOK, body)
	status, body, _ = w.createPass(t, fmt.Sprintf(`{"hangarId":%q,"squadronId":%q,"levelId":"visit"}`, hangarAnvilDockID, squadronTongsID))
	assertStatus(t, status, http.StatusOK, body)

	// An empty person is nobody, so a pass that names one beside a group has one holder,
	// the group: the create takes it, and so does the table.
	status, body, _ = w.createPass(t, fmt.Sprintf(`{"hangarId":%q,"userId":"","groupId":%q,"levelId":"visit"}`, hangarAnvilDockID, group))
	assertStatus(t, status, http.StatusOK, body)
	nobody := spanner.Insert("HangarPasses", []string{"Id", "SectorId", "HangarId", "UserId", "GroupId", "LevelId"}, []any{passNobodyAndGroupID, anvil, hangarAnvilDockID, "", group, "visit"})
	if _, err := w.db.Apply(w.ctx, []*spanner.Mutation{nobody}); err != nil {
		t.Errorf("the table refused a pass that names an empty person beside a group: %v, want it taken as the group's", err)
	}
}

// TestGroupShare_holderOnUpdate holds a pass to the holder it was written with. The three
// holder columns are immutable, so an update that names any of them is refused with the
// framework's answer for an immutable field, 400 and "json field <name> is immutable"
// (for two, "json fields <name> and <name> are immutable", in the order of the names),
// whoever sends it: the Marshal, whose update grant names the level and the expiry; the
// teller, whose role is of the test's own and was handed Update on every column of a
// pass, the three holder columns among them, which no role file may grant and the
// engine skips (passClerk); and the armorer, whose role names nothing of the passes.
// Nothing of a refused update is written, and the level and the expiry, which are no
// holder, are still theirs to change who hold them.
func TestGroupShare_holderOnUpdate(t *testing.T) {
	t.Parallel()

	w := newGroupShareWorld(t)
	group := w.groupsOffered(t)["Bay keepers"]
	if group == "" {
		t.Fatal("Anvil's groups do not list the Bay keepers")
	}
	person := string(account(t, armorer))
	status, body, pass := w.createPass(t, fmt.Sprintf(`{"hangarId":%q,"userId":%q,"levelId":"visit"}`, hangarAnvilDockID, person))
	assertStatus(t, status, http.StatusOK, body)

	// holder is who the pass names, read straight from the table.
	holder := func() string {
		t.Helper()

		return w.holderOf(t, pass)
	}
	heldByThePerson := fmt.Sprintf("person %s, squadron <null>, group <null>", person)
	if got := holder(); got != heldByThePerson {
		t.Fatalf("the pass as written names %s, want %s", got, heldByThePerson)
	}

	// The Marshal writes what her grant names, so the pass is one she may change.
	status, body = w.updatePass(t, "marshal", pass, `{"levelId":"run"}`)
	assertStatus(t, status, http.StatusOK, body)

	// The teller holds every column of a pass a role can, and writes one that is no holder.
	const teller = accesstypes.User("teller")
	w.passClerk(t, teller)
	status, body = w.updatePass(t, teller, pass, `{"expiresAt":"2999-01-01T00:00:00Z"}`)
	assertStatus(t, status, http.StatusOK, body)

	// An update that names a holder column is refused with the one answer, alone and
	// beside a column the sender may write, and writes nothing.
	for _, sender := range []accesstypes.User{"marshal", teller, armorer} {
		for _, tt := range holderUpdates(string(account(t, sentry)), group) {
			status, body := w.updatePass(t, sender, pass, tt.value)
			if status != http.StatusBadRequest {
				t.Errorf("the update by %s that names %s: status = %d, want 400: %s", sender, tt.name, status, body)
			} else if got := refusalOf(t, body); got != tt.says {
				t.Errorf("the update by %s that names %s is refused with %q, want %q", sender, tt.name, got, tt.says)
			}
			if got := holder(); got != heldByThePerson {
				t.Errorf("after the update by %s that names %s the pass names %s, want %s", sender, tt.name, got, heldByThePerson)
			}
		}
	}
	if got := readColumn[string](w.ctx, t, w.db, "HangarPasses", spanner.Key{pass}, "LevelId"); got != "run" {
		t.Errorf("the level of the pass = %q after the refused updates, want run: a refused update writes none of its columns", got)
	}
}

// TestGroupShare_holderRefusalIsOneAnswer holds the refusal of a holder's change to one
// answer wherever the update points: at a pass of the request's sector, at a pass of
// another sector, and at an id that is no pass. The framework refuses an immutable field
// as it decodes the request, before it reads a grant or a row, so the three answer the
// same status and the same sentence and nothing tells a pass that is there, in a sector
// the sender cannot see, from one that is not. An update that names two holder columns,
// or all three, is refused in one sentence that names each of them in the order of their
// names, so it too is the same answer at all three. What an update may still change, the
// level, tells them apart no better: both are the 404 of a row that is not the sector's,
// in the same words.
func TestGroupShare_holderRefusalIsOneAnswer(t *testing.T) {
	t.Parallel()

	w := newGroupShareWorld(t)
	group := w.groupsOffered(t)["Bay keepers"]
	if group == "" {
		t.Fatal("Anvil's groups do not list the Bay keepers")
	}
	person := string(account(t, armorer))
	status, body, own := w.createPass(t, fmt.Sprintf(`{"hangarId":%q,"userId":%q,"levelId":"visit"}`, hangarAnvilDockID, person))
	assertStatus(t, status, http.StatusOK, body)
	// Bastion's pass, which no request in Anvil reads, written straight into the table.
	theirs := spanner.Insert("HangarPasses", []string{"Id", "SectorId", "HangarId", "UserId", "LevelId"}, []any{passBastionOwnID, bastion, hangarBastionSlipID, person, "visit"})
	if _, err := w.db.Apply(w.ctx, []*spanner.Mutation{theirs}); err != nil {
		t.Fatalf("writing Bastion's pass: %v", err)
	}
	if got := w.passes(t, "Id = @id", map[string]any{"id": passNoneID}); got != 0 {
		t.Fatalf("the passes with the id that is to be no pass = %d, want 0", got)
	}
	heldInBastion := w.holderOf(t, passBastionOwnID)

	const teller = accesstypes.User("teller")
	w.passClerk(t, teller)
	for _, sender := range []accesstypes.User{"marshal", teller} {
		for _, tt := range holderUpdates(string(account(t, sentry)), group) {
			ownStatus, ownBody := w.updatePass(t, sender, own, tt.value)
			if ownStatus != http.StatusBadRequest || refusalOf(t, ownBody) != tt.says {
				t.Errorf("the update by %s of the sector's pass that names %s: status = %d, want 400 saying %q: %s", sender, tt.name, ownStatus, tt.says, ownBody)
			}
			for _, other := range []struct {
				name string
				id   string
			}{
				{name: "another sector's pass", id: passBastionOwnID},
				{name: "an id that is no pass", id: passNoneID},
			} {
				// Every update of a holder column has one answer, whether it names one of
				// them or several, so this is the answer the sector's own pass gave.
				status, body := w.updatePass(t, sender, other.id, tt.value)
				if status != ownStatus || refusalOf(t, body) != tt.says {
					t.Errorf("the update by %s of %s that names %s answers %d %s, want the answer for the sector's own pass, %d saying %q", sender, other.name, tt.name, status, body, ownStatus, tt.says)
				}
			}
		}
	}
	if got := w.holderOf(t, passBastionOwnID); got != heldInBastion {
		t.Errorf("after the refused updates Bastion's pass names %s, want %s", got, heldInBastion)
	}

	// The level is the teller's to write on the sector's pass. On Bastion's pass and on
	// no pass the update finds no row of the sector, and says so in the same words.
	status, body = w.updatePass(t, teller, own, `{"levelId":"run"}`)
	assertStatus(t, status, http.StatusOK, body)
	theirStatus, theirBody := w.updatePass(t, teller, passBastionOwnID, `{"levelId":"run"}`)
	noneStatus, noneBody := w.updatePass(t, teller, passNoneID, `{"levelId":"run"}`)
	if theirStatus != http.StatusNotFound || noneStatus != http.StatusNotFound {
		t.Errorf("the level written to another sector's pass answers %d and to no pass %d, want 404 for both: %s | %s", theirStatus, noneStatus, theirBody, noneBody)
	}
	if got, want := strings.ReplaceAll(string(theirBody), passBastionOwnID, "the id"), strings.ReplaceAll(string(noneBody), passNoneID, "the id"); got != want {
		t.Errorf("the level written to another sector's pass answers %s, and to no pass %s: want the same words, the id apart", theirBody, noneBody)
	}
	if got := readColumn[string](w.ctx, t, w.db, "HangarPasses", spanner.Key{passBastionOwnID}, "LevelId"); got != "visit" {
		t.Errorf("the level of Bastion's pass = %q after the update sent through Anvil, want visit", got)
	}
}

// holderUpdate is one update that names a holder column, and the one sentence the
// refusal says. An update that names two of them is refused for both, in the order of
// their names.
type holderUpdate struct {
	name  string
	value string
	says  string
}

// holderUpdates is every way an update names a holder column: each of the three set to
// a value and to nothing, one beside a column that is no holder, the change of holder a
// role that held the columns could once make, the person taken off and a group put on,
// in both orders of the body, and all three at once.
func holderUpdates(person, group string) []holderUpdate {
	const (
		userID     = "json field userId is immutable"
		squadronID = "json field squadronId is immutable"
		groupID    = "json field groupId is immutable"
		both       = "json fields groupId and userId are immutable"
		all        = "json fields groupId, squadronId and userId are immutable"
	)

	return []holderUpdate{
		{name: "another person", value: fmt.Sprintf(`{"userId":%q}`, person), says: userID},
		{name: "no person", value: `{"userId":null}`, says: userID},
		{name: "an empty person", value: `{"userId":""}`, says: userID},
		{name: "a squadron", value: fmt.Sprintf(`{"squadronId":%q}`, squadronTongsID), says: squadronID},
		{name: "no squadron", value: `{"squadronId":null}`, says: squadronID},
		{name: "a group", value: fmt.Sprintf(`{"groupId":%q}`, group), says: groupID},
		{name: "no group", value: `{"groupId":null}`, says: groupID},
		{name: "a group beside the level", value: fmt.Sprintf(`{"groupId":%q,"levelId":"visit"}`, group), says: groupID},
		{name: "the person taken off and a group put on", value: fmt.Sprintf(`{"userId":null,"groupId":%q}`, group), says: both},
		{name: "the same two with the group first in the body", value: fmt.Sprintf(`{"groupId":%q,"userId":null}`, group), says: both},
		{name: "all three at once", value: fmt.Sprintf(`{"userId":null,"squadronId":%q,"groupId":%q}`, squadronTongsID, group), says: all},
	}
}
