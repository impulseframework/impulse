//go:build skipAuth

package integration

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"maps"
	"math/big"
	"net/http"
	"slices"
	"testing"
	"time"

	"cloud.google.com/go/civil"
	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"google.golang.org/grpc/codes"
)

// TestTenantDelete_aDeletedSectorTakesItsRolesAndInvitations proves a sector that is
// deleted leaves nothing of itself behind in either auth, on the served stack. The
// generated delete of the Sector record takes, as kinds of its own delete, what each
// auth holds for the sector: the roles people held in it, its groups with their members
// and roles, the custom role made in it with its grants, the invitations into it that
// still stood, which are withdrawn, and the accounts that were the sector's alone, all in
// the transaction that deletes the row, so nothing is left for a later sector to pick
// up. An account is the sector's alone where it held roles there and nowhere else, or
// where the sector's directory signs the person in and they hold nothing: it goes with
// its sign-in identities, and its sessions are ended, their rows left for the timed
// removal. A person who holds a role elsewhere stays, in the crew and among the members
// alike, and so does a person another sector's directory signs in. The sector is made
// the way every sector is, by an invitation: its founder, the first marshal, holds roles
// there alone and goes with it.
//
// The sector's directory goes with it in the same request, though the sector is
// directory only by then: nothing is turned off or removed first, the directory's
// removal leaves its change event, another sector's directory stands, and the directory
// is free for another sector, which the schema refused while Ember had it. The marshal,
// seated in Ember and in Anvil, signs in afterwards as before and reaches Anvil.
// Ember's rows go with it too: one in each of the tables that carry a sector's key (a
// wing, a hangar, a pass into it, a mission, a consignment, a droid's report, a distress
// call and a relay of it), while the rows of the sectors beside it stand. And one in
// each of the eight tables that reach their sector through another row: a squadron in
// Ember's wing with a member, a ship in Ember's hangar with a refit and its task, a
// sortie of Ember's mission with an expense, and a document of that mission. The
// sector's delete takes each of them in code, through its kind's own delete, so a kind
// that keeps a change record keeps one of the row that went. The ship flew Ember's own
// sortie and the droid's report is of it: those two references say nothing of a delete,
// and hold nothing here, since what names the ship goes in the same transaction.
//
// A sector that is not disabled is not deleted: the remove is refused, saying to
// disable it first, and nothing is gone. A sector with rows under it is not deleted by
// a request that does not say so: the plain remove is answered with the question a
// person is asked first, which counts every kind of the sector's rows and of what the
// auths hold for it, for whoever may delete the sector, and nothing is gone. The request
// that goes ahead says it takes everything under the sector.
//
// No role the release ships deletes a sector, so the suite makes one that does: a custom
// role of the whole application holding Delete on Sectors, given to the governor.
//
// Demonstrates: tenancy.deleted-tenant.
func TestTenantDelete_aDeletedSectorTakesItsRolesAndInvitations(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	hq := newBrowser(t, s, consoleAPI)
	hq.signIn(ctx, "governor")
	marshal := newBrowser(t, s, consoleAPI)
	marshal.signIn(ctx, "marshal")

	const (
		closer          accesstypes.Role = "SectorCloser"
		watch           accesstypes.Role = "EmberWatch"
		hand            accesstypes.User = "emberhand"
		drifter         accesstypes.User = "ember-drifter"
		commuter        accesstypes.User = "ember-commuter"
		local           accesstypes.User = "ember-local"
		visitor         accesstypes.User = "ember-visitor"
		sectorsResource                  = accesstypes.Resource("Sectors")
	)
	crewManager, membersManager := s.access.UserManager(), s.members.UserManager()
	global := accesstypes.GlobalPolicyScope()
	if err := crewManager.AddRole(ctx, global, closer, "Closes a sector the fleet has left."); err != nil {
		t.Fatalf("access.UserManager.AddRole(%s) error = %v", closer, err)
	}
	if err := crewManager.AddRoleGrants(ctx, global, closer, access.GrantRow{Permission: accesstypes.Delete, Resource: sectorsResource}); err != nil {
		t.Fatalf("access.UserManager.AddRoleGrants(%s) error = %v", closer, err)
	}
	// The permission store names a person by the id of their account: the governor's and
	// the marshal's are the cast's fixed ids.
	governorUser, marshalUser := account(t, "governor"), account(t, "marshal")
	if err := crewManager.AddUserRoles(ctx, global, governorUser, closer); err != nil {
		t.Fatalf("access.UserManager.AddUserRoles(governor, %s) error = %v", closer, err)
	}
	if d := decisionFor(ctx, t, s.access.ForUser(governorUser), accesstypes.GlobalScope(), accesstypes.Delete, sectorsResource, time.Now().Add(15*time.Second)); !d.IsGranted() {
		t.Fatalf("Delete on Sectors for the governor holding %s = %s, want granted before the sector is made", closer, d)
	}

	// Headquarters invites Ember's founder, whose first sign-in makes the sector and
	// seats them as its marshal.
	ember := foundSector(ctx, t, s, hq, "Ember", "Near frontier", "2226-10-20", "ember-founder")
	founderID := crewAccountID(ctx, t, hq, "ember-founder")
	founderUser := accesstypes.User(founderID)
	emberScope := accesstypes.DomainPolicyScope(accesstypes.Domain(ember))
	anvilScope := accesstypes.DomainPolicyScope(anvil)

	// The crew in Ember: the marshal of Anvil is given a role there; a person whose
	// account is there already is given a role there and holds one nowhere else; a
	// person Cinder's directory signs in is given a role there; an invitation into Ember
	// stands; a custom role is made in Ember; and a group of Ember's holds a role there,
	// with the marshal and the person seated in Ember alone in it.
	status, body := hq.do(ctx, http.MethodPost, consoleAPI+"/assign-crew-role", []byte(`{"userId":"`+string(marshalUser)+`","role":"Pilot","domain":"`+ember+`"}`))
	assertStatus(t, status, http.StatusNoContent, body)
	handAddress := "hand@example.com"
	handID := s.seedCrewAccount(ctx, t, string(hand), &handAddress).String()
	handUser := accesstypes.User(handID)
	status, body = hq.do(ctx, http.MethodPost, consoleAPI+"/assign-crew-role", []byte(`{"userId":"`+handID+`","role":"Cadet","domain":"`+ember+`"}`))
	assertStatus(t, status, http.StatusNoContent, body)
	visitorID := s.seedCrewAccount(ctx, t, string(visitor), nil).String()
	status, body = hq.do(ctx, http.MethodPost, consoleAPI+"/assign-crew-role", []byte(`{"userId":"`+visitorID+`","role":"Cadet","domain":"`+ember+`"}`))
	assertStatus(t, status, http.StatusNoContent, body)
	status, body = hq.do(ctx, http.MethodPost, sectorPath(ember, "create-crew-sector-group"), []byte(`{"name":"Ember watch","description":"Keeps Ember's lights on."}`))
	assertStatus(t, status, http.StatusOK, body)
	emberGroup := cell[string](t, decodeRow(t, body), "id")
	for _, member := range []string{string(marshalUser), handID} {
		status, body = hq.do(ctx, http.MethodPost, sectorPath(ember, "add-crew-sector-group-member"), []byte(`{"groupId":"`+emberGroup+`","userId":"`+member+`"}`))
		assertStatus(t, status, http.StatusNoContent, body)
	}
	status, body = hq.do(ctx, http.MethodPost, sectorPath(ember, "give-crew-sector-group-role"), []byte(`{"groupId":"`+emberGroup+`","role":"Cadet"}`))
	assertStatus(t, status, http.StatusNoContent, body)
	// What a sign-in left of each: an identity, under the fleet's own connection, which
	// is no sector's directory, or under Cinder's directory for the person it signs in,
	// and a session.
	founderSession := leftBySignIn(ctx, t, s, "ember-founder", founderID, "google", "fleet")
	handSession := leftBySignIn(ctx, t, s, hand, handID, "google", "fleet")
	visitorSession := leftBySignIn(ctx, t, s, visitor, visitorID, "google", "example")
	status, body = hq.do(ctx, http.MethodPost, sectorPath(ember, "invite-crew-sector-user"), []byte(`{"email":"invited@example.com","roles":["Cadet"]}`))
	assertStatus(t, status, http.StatusOK, body)
	var invitation invited
	if err := json.Unmarshal(body, &invitation); err != nil || invitation.ID == "" {
		t.Fatalf("invite-crew-sector-user answered %s (%v), want the invitation's id", body, err)
	}
	if err := crewManager.AddRole(ctx, emberScope, watch, "Stands watch over Ember."); err != nil {
		t.Fatalf("access.UserManager.AddRole(%s) error = %v", watch, err)
	}

	// The members in Ember: the drifter holds the portal's role there and nowhere else,
	// the commuter holds it there and in Anvil, and an invitation into Ember stands.
	drifterID, commuterID, memberLink := newID(t), newID(t), newID(t)
	drifterUser, commuterUser := accesstypes.User(drifterID), accesstypes.User(commuterID)
	hash := sha256.Sum256([]byte(memberLink))
	if _, err := s.db.Apply(ctx, []*spanner.Mutation{
		spanner.Insert("MembersSessionUsers", []string{"Id", "Username", "CreatedAt"}, []any{drifterID, string(drifter), spanner.CommitTimestamp}),
		spanner.Insert("MembersSessionUsers", []string{"Id", "Username", "CreatedAt"}, []any{commuterID, string(commuter), spanner.CommitTimestamp}),
		spanner.InsertMap("MembersSessionMailLinks", map[string]any{
			"Id": memberLink, "TokenHash": hash[:], "Kind": "invitation", "Email": "member@example.com", "TenantId": ember,
			"RoleNames": []string{"ClientPortal"}, "CreatedAt": spanner.CommitTimestamp, "ExpiresAt": time.Now().Add(time.Hour),
		}),
	}); err != nil {
		t.Fatalf("writing the members: %v", err)
	}
	if err := membersManager.AddUserRoles(ctx, emberScope, drifterUser, "ClientPortal"); err != nil {
		t.Fatalf("access.UserManager.AddUserRoles(%s, Ember) error = %v", drifter, err)
	}
	if err := membersManager.AddUserRoles(ctx, emberScope, commuterUser, "ClientPortal"); err != nil {
		t.Fatalf("access.UserManager.AddUserRoles(%s, Ember) error = %v", commuter, err)
	}
	if err := membersManager.AddUserRoles(ctx, anvilScope, commuterUser, "ClientPortal"); err != nil {
		t.Fatalf("access.UserManager.AddUserRoles(%s, Anvil) error = %v", commuter, err)
	}

	// Before the delete, Ember holds what the suite gave it.
	if got := marshal.domains(ctx); !slices.Contains(got, ember) {
		t.Fatalf("the marshal's sectors once given a role in Ember = %v, want Ember among them", got)
	}
	if !crewAccountExists(t, s.db, handID) {
		t.Fatal("the person seated in Ember alone has no account")
	}
	if n := rowsWhere(ctx, t, s, "CrewGroupMembers", "GroupId", emberGroup) + rowsWhere(ctx, t, s, "CrewGroupRoles", "GroupId", emberGroup); n != 3 {
		t.Fatalf("Ember's group holds %d member(s) and role(s) before the delete, want the two members and the role", n)
	}
	for _, held := range []struct {
		name    string
		manager access.UserManager
		user    accesstypes.User
	}{
		{name: "Ember's founder, its first marshal", manager: crewManager, user: founderUser},
		{name: "the marshal", manager: crewManager, user: marshalUser},
		{name: "the person seated in Ember alone", manager: crewManager, user: handUser},
		{name: "the person Cinder's directory signs in", manager: crewManager, user: accesstypes.User(visitorID)},
		{name: "the drifter, a member", manager: membersManager, user: drifterUser},
		{name: "the commuter, a member", manager: membersManager, user: commuterUser},
	} {
		if roles := rolesHeldIn(ctx, t, held.manager, held.user, emberScope); len(roles) == 0 {
			t.Fatalf("%s holds no role in Ember before the delete, want one", held.name)
		}
	}

	if !hasRole(ctx, t, crewManager, emberScope, watch) {
		t.Fatalf("the custom role %s made in Ember does not exist before the delete", watch)
	}
	if readColumn[spanner.NullTime](ctx, t, s.db, "CrewSessionMailLinks", spanner.Key{invitation.ID}, "ConsumedAt").Valid {
		t.Fatal("the crew's invitation into Ember does not stand before the delete")
	}
	if readColumn[spanner.NullTime](ctx, t, s.db, "MembersSessionMailLinks", spanner.Key{memberLink}, "ConsumedAt").Valid {
		t.Fatal("the members' invitation into Ember does not stand before the delete")
	}

	// Ember is directory only by now, with an organization's directory, written here as
	// rows: what its marshal would have declared on the sector's Sign-in page. While
	// Ember has the directory no other sector takes it, which the schema holds.
	const entra = "6f2c1b7e-0000-4000-8000-0000000000e1"
	directoryRow := func(sector string) []*spanner.Mutation {
		return []*spanner.Mutation{spanner.Insert("SectorDirectories", []string{"Id", "SectorId", "Method", "Connection"}, []any{newID(t), sector, "azure", entra})}
	}
	if _, err := s.db.Apply(ctx, append(directoryRow(ember), spanner.Update("Sectors", []string{"Id", "DirectoryOnly"}, []any{ember, true}))); err != nil {
		t.Fatalf("making Ember directory only with a directory: %v", err)
	}
	if _, err := s.db.Apply(ctx, directoryRow(anvil)); spanner.ErrCode(err) != codes.AlreadyExists {
		t.Fatalf("giving Anvil the directory Ember has: err = %v, want the unique index to refuse it", err)
	}
	// A person Ember's directory signed in, who holds nothing: the account is Ember's
	// alone.
	localID := s.seedCrewAccount(ctx, t, string(local), nil).String()
	localSession := leftBySignIn(ctx, t, s, local, localID, "azure", entra)
	// Ember holds rows of its own by now, written here as rows: one in each table that
	// carries a sector's key, and one in each table that reaches its sector through
	// another row. The pass is into Ember's own hangar, in a person's name; the ship is
	// in that hangar, flew the sortie of Ember's mission, and is the one the droid read.
	emberWing, emberHangar, emberMission := newID(t), newID(t), newID(t)
	emberSquadron, emberShip, emberRefit, emberSortie := newID(t), newID(t), newID(t), newID(t)
	if _, err := s.db.Apply(ctx, []*spanner.Mutation{
		spanner.Insert("Wings", []string{"Id", "SectorId", "Name"}, []any{emberWing, ember, "Ember Wing"}),
		spanner.Insert("Hangars", []string{"Id", "SectorId", "Name", "Zone"}, []any{emberHangar, ember, "Ember Dock", "dock"}),
		spanner.Insert("HangarPasses", []string{"Id", "SectorId", "HangarId", "UserId", "LevelId"}, []any{newID(t), ember, emberHangar, string(marshalUser), "visit"}),
		spanner.InsertMap("Missions", map[string]any{
			"Id": emberMission, "SectorId": ember, "ClientId": clientHalvardID, "KindId": "courier", "Title": "Ember's last run", "Hazard": 1,
			"Fee": *big.NewRat(100, 1), "Deadline": time.Now().Add(24 * time.Hour), "BookedByUserId": string(governorUser), "BookedByName": "governor", "StatusId": "open",
		}),
		spanner.InsertMap("Consignments", map[string]any{
			"Id": newID(t), "SectorId": ember, "ClientId": clientHalvardID, "BondCode": "EMBER-0001", "Description": "Ash samples", "Mass": 1.5, "ExpiresOn": civil.DateOf(time.Now().AddDate(0, 1, 0)),
		}),
		spanner.InsertMap("DistressCalls", map[string]any{
			"Id": newID(t), "SectorId": ember, "Summary": "Lights out in Ember", "Severity": 1, "CaseNumber": "EMBER-CASE-0001", "FiledByUserId": string(governorUser), "FiledByName": "governor",
		}),
		// A relay names its call by the case number, with no delete rule on that key: it
		// goes with the sector by its own key to it.
		spanner.InsertMap("DistressRelays", map[string]any{
			"Id": newID(t), "SectorId": ember, "CaseNumber": "EMBER-CASE-0001", "Message": "Ember has gone dark", "RelayedByName": "governor", "RelayedAt": spanner.CommitTimestamp,
		}),
		spanner.Insert("Squadrons", []string{"Id", "WingId", "Name"}, []any{emberSquadron, emberWing, "Cinderfall"}),
		spanner.Insert("SquadronMemberships", []string{"SquadronId", "UserId"}, []any{emberSquadron, string(marshalUser)}),
		spanner.Insert("Ships", []string{"Id", "HangarId", "ClassId", "Registry", "Name"}, []any{emberShip, emberHangar, shipClassKestrelID, "EMB-001", "Last Light"}),
		spanner.InsertMap("Refits", map[string]any{
			"Id": emberRefit, "ShipId": emberShip, "StatusId": "docked", "OpenedByUserId": string(governorUser), "OpenedByName": "governor",
		}),
		spanner.Insert("RefitTasks", []string{"Id", "TaskNumber", "Instructions"}, []any{emberRefit, 1, "Check the running lights"}),
		spanner.InsertMap("Sorties", map[string]any{
			"Id": emberSortie, "MissionId": emberMission, "ShipId": emberShip, "PilotUserId": string(marshalUser), "LaunchedAt": time.Now(),
		}),
		spanner.InsertMap("SortieExpenses", map[string]any{
			"Id": newID(t), "SortieId": emberSortie, "Category": "fuel", "Amount": *big.NewRat(50, 1),
		}),
		spanner.InsertMap("MissionDocuments", map[string]any{
			"Id": newID(t), "MissionId": emberMission, "Title": "Ember's manifest", "FileName": "manifest.txt", "ContentType": "text/plain", "Size": 0,
			"StoreKey": newID(t), "UploadedByUserId": string(governorUser), "UploadedByName": "governor", "UploadedAt": time.Now(), "Digest": make([]byte, sha256.Size),
		}),
		spanner.InsertMap("DroidReports", map[string]any{
			"Id": newID(t), "SectorId": ember, "ShipId": emberShip, "Subsystem": "hull", "Reading": 0.5, "RecordedAt": time.Now(),
		}),
	}); err != nil {
		t.Fatalf("writing Ember's rows: %v", err)
	}
	rowsBefore := map[string]int64{}
	for _, table := range sectorKeyedTables {
		if n := sectorRows(ctx, t, s, table, ember); n != 1 {
			t.Fatalf("%s holds %d row(s) of Ember before the delete, want the one written", table, n)
		}
		rowsBefore[table] = sectorRows(ctx, t, s, table, "")
	}
	// Each row that reaches Ember through another row, by the table, the column and
	// the value that finds it.
	throughEmber := []struct{ table, column, value string }{
		{"Squadrons", "Id", emberSquadron},
		{"SquadronMemberships", "SquadronId", emberSquadron},
		{"Ships", "Id", emberShip},
		{"Refits", "Id", emberRefit},
		{"RefitTasks", "Id", emberRefit},
		{"Sorties", "Id", emberSortie},
		{"SortieExpenses", "SortieId", emberSortie},
		{"MissionDocuments", "MissionId", emberMission},
	}
	throughBefore := map[string]int64{}
	for _, row := range throughEmber {
		if n := rowsWhere(ctx, t, s, row.table, row.column, row.value); n != 1 {
			t.Fatalf("%s holds %d row(s) of Ember before the delete, want the one written", row.table, n)
		}
		throughBefore[row.table] = rowsWhere(ctx, t, s, row.table, "", "")
	}
	directoriesBefore := sectorDirectoryRows(ctx, t, s, "")
	// The kinds of Ember's rows that keep a change record, each with one row of Ember's.
	tracked := []string{"SectorDirectories", "Missions", "Ships", "Refits"}
	eventsBefore := changeEvents(ctx, t, s, tracked...)
	// nothingGone fails the test unless Ember, its directory and each of its rows stand
	// after the refused request, and no change event was written.
	nothingGone := func(after string) {
		t.Helper()
		if n := rowsWhere(ctx, t, s, "Sectors", "Id", ember); n != 1 {
			t.Fatalf("Sectors holds %d row(s) of Ember after %s, want the one: a refused delete deletes nothing", n, after)
		}
		if n := sectorDirectoryRows(ctx, t, s, ember); n != 1 {
			t.Errorf("Ember's directory after %s: %d row(s), want the one", after, n)
		}
		for _, table := range sectorKeyedTables {
			if n := sectorRows(ctx, t, s, table, ember); n != 1 {
				t.Errorf("%s holds %d row(s) of Ember after %s, want the one written", table, n, after)
			}
		}
		for _, row := range throughEmber {
			if n := rowsWhere(ctx, t, s, row.table, row.column, row.value); n != 1 {
				t.Errorf("%s holds %d row(s) of Ember after %s, want the one written", row.table, n, after)
			}
		}
		if events := changeEvents(ctx, t, s, tracked...); !maps.Equal(events, eventsBefore) {
			t.Errorf("change events after %s: %v, want %v: nothing was deleted", after, events, eventsBefore)
		}
		if n := rowsWhere(ctx, t, s, "CrewGroups", "Id", emberGroup); n != 1 {
			t.Errorf("Ember's group after %s: %d row(s), want the one", after, n)
		}
		for _, id := range []string{founderID, handID, localID, visitorID} {
			if !crewAccountExists(t, s.db, id) {
				t.Errorf("the account %s is gone after %s, want it standing", id, after)
			}
		}
		for _, session := range []string{founderSession, handSession, localSession, visitorSession} {
			if n := rowsWhere(ctx, t, s, "CrewSessions", "Id", session); n != 1 || readColumn[bool](ctx, t, s.db, "CrewSessions", spanner.Key{session}, "Expired") {
				t.Errorf("the session %s after %s is ended or gone, want it standing", session, after)
			}
		}
	}

	// A sector that is not disabled is not deleted, whether or not the request says it
	// takes what is under the sector: each is refused, saying to disable it first.
	remove := []byte(`[{"op":"remove","path":"/sectors/` + ember + `"}]`)
	everythingUnder := []byte(`[{"op":"remove","path":"/sectors/` + ember + `","value":{"everythingUnder":true}}]`)
	const disableFirst = "Disable this sector before deleting it."
	for _, refused := range []struct {
		name string
		body []byte
	}{
		{name: "the plain remove", body: remove},
		{name: "the remove that says it takes everything under the sector", body: everythingUnder},
	} {
		status, body = hq.do(ctx, http.MethodPatch, consoleAPI+"/resources", refused.body)
		assertStatus(t, status, http.StatusConflict, body)
		if got := refusalMessage(t, body); got != disableFirst {
			t.Errorf("%s of Ember while it is not disabled: the refusal = %q, want %q", refused.name, got, disableFirst)
		}
		nothingGone(refused.name + " of a sector that is not disabled")
	}
	// Ember is disabled, by the record's update, by the governor, who holds
	// Headquarters.
	setSectorDisabled(ctx, t, hq, ember, true)

	// A request that does not say it takes what is under the sector is asked the
	// question, and nothing is gone: not the sector, not its directory, not a row.
	//
	// The question counts every kind of the sector's rows, and then what the two auths
	// hold for it, for whoever may delete the sector, whatever they may list there: one
	// of each of Ember's rows, the kind nearest the sector first, a droid's report among
	// them; the four accounts that were Ember's alone (its founder's, the person's seated
	// there alone, the one its directory signed in, and the drifter's among the members);
	// the six roles held in it; its group; its custom role; and the crew's invitation
	// and the members'. The governor, a super administrator, is asked it.
	status, body = hq.do(ctx, http.MethodPatch, consoleAPI+"/resources", remove)
	assertStatus(t, status, http.StatusPreconditionRequired, body)
	const counted = "Delete this sector and everything under it: 1 consignment, 1 distress call, 1 hangar, 1 sector directory, " +
		"1 wing, 1 distress relay, 1 ship, 1 squadron, 1 droid report, 1 hangar pass, 1 mission, 1 refit, " +
		"1 squadron membership, 1 mission document, 1 refit task, 1 sortie, 1 sortie expense, " +
		"4 accounts, 6 roles held, 1 group, 1 custom role, 2 invitations. This can't be undone."
	if got := refusalMessage(t, body); got != counted {
		t.Errorf("the governor's question = %q, want %q", got, counted)
	}
	// So is a person who may list none of the sector's kinds there: the deckhand, given
	// the closing role, may delete the sector, and holds no role in Ember or in every
	// sector.
	deckhand := newBrowser(t, s, consoleAPI)
	deckhand.signIn(ctx, "deckhand")
	deckhandUser := account(t, "deckhand")
	if err := crewManager.AddUserRoles(ctx, global, deckhandUser, closer); err != nil {
		t.Fatalf("access.UserManager.AddUserRoles(deckhand, %s) error = %v", closer, err)
	}
	if d := decisionFor(ctx, t, s.access.ForUser(deckhandUser), accesstypes.GlobalScope(), accesstypes.Delete, sectorsResource, time.Now().Add(15*time.Second)); !d.IsGranted() {
		t.Fatalf("Delete on Sectors for the deckhand holding %s = %s, want granted before the question", closer, d)
	}
	status, body = deckhand.do(ctx, http.MethodPatch, consoleAPI+"/resources", remove)
	assertStatus(t, status, http.StatusPreconditionRequired, body)
	if got := refusalMessage(t, body); got != counted {
		t.Errorf("the deckhand's question = %q, want %q", got, counted)
	}
	nothingGone("the question")

	// One request that says so, with nothing before it but the disable: directory only
	// is not turned off and the directory is not removed first.
	status, body = hq.do(ctx, http.MethodPatch, consoleAPI+"/resources", everythingUnder)
	assertStatus(t, status, http.StatusOK, body)

	// The directory went with the sector and every other sector's directory stands.
	if n := sectorDirectoryRows(ctx, t, s, ember); n != 0 {
		t.Errorf("Ember's directory after the delete: %d row(s), want none: the directory goes with its sector", n)
	}
	if n := sectorDirectoryRows(ctx, t, s, ""); n != directoriesBefore-1 {
		t.Errorf("SectorDirectories holds %d row(s) after the delete, want %d: Ember's alone is gone", n, directoriesBefore-1)
	}
	// Each row of a kind that keeps a change record left one, the directory's among
	// them: the sector's delete took the row through its kind's own delete.
	eventsAfter := changeEvents(ctx, t, s, tracked...)
	for _, table := range tracked {
		if eventsAfter[table] != eventsBefore[table]+1 {
			t.Errorf("change events of %s: %d before the delete and %d after, want one more: the removal of Ember's row is in the audit", table, eventsBefore[table], eventsAfter[table])
		}
	}
	// Ember's rows went with it, in every table that carries a sector's key, and the rows
	// of the sectors beside it stand.
	for _, table := range sectorKeyedTables {
		if n := sectorRows(ctx, t, s, table, ember); n != 0 {
			t.Errorf("%s holds %d row(s) of Ember after the delete, want none: a sector's rows go with it", table, n)
		}
		if n := sectorRows(ctx, t, s, table, ""); n != rowsBefore[table]-1 {
			t.Errorf("%s holds %d row(s) after the delete, want %d: Ember's alone is gone", table, n, rowsBefore[table]-1)
		}
	}
	// So did the rows that reach Ember through another row, each through its kind's own
	// delete, and the rows of the same tables that are other sectors' stand.
	for _, row := range throughEmber {
		if n := rowsWhere(ctx, t, s, row.table, row.column, row.value); n != 0 {
			t.Errorf("%s holds %d row(s) of Ember after the delete, want none: a row that reaches its sector through another row goes with the sector", row.table, n)
		}
		if n := rowsWhere(ctx, t, s, row.table, "", ""); n != throughBefore[row.table]-1 {
			t.Errorf("%s holds %d row(s) after the delete, want %d: Ember's alone is gone", row.table, n, throughBefore[row.table]-1)
		}
	}
	// The directory is free for another sector.
	if _, err := s.db.Apply(ctx, directoryRow(anvil)); err != nil {
		t.Errorf("giving Anvil the directory Ember had: %v, want it free once Ember is deleted", err)
	}
	// The marshal signs in as before and reaches Anvil.
	again := newBrowser(t, s, consoleAPI)
	again.signIn(ctx, "marshal")
	if got := again.domains(ctx); !slices.Equal(got, []string{anvil}) {
		t.Errorf("the marshal's sectors at a sign-in after Ember is deleted = %v, want Anvil alone", got)
	}

	// Gone at once, for the governor and for the marshal alike.
	for name, b := range map[string]*browser{"the governor": hq, "the marshal": marshal} {
		if status, body := b.do(ctx, http.MethodGet, sectorPath(ember, "missions"), nil); status != http.StatusNotFound {
			t.Errorf("GET Ember's missions as %s after the sector is deleted: status %d, want 404: %s", name, status, body)
		}
	}
	if got := marshal.domains(ctx); !slices.Equal(got, []string{anvil}) {
		t.Errorf("the marshal's sectors after Ember is deleted = %v, want Anvil alone", got)
	}

	// Neither auth's permission store holds a role in the deleted sector: not the
	// founder's, not the marshal's, not the person's who was created there, and not
	// the members'.
	for _, gone := range []struct {
		name    string
		manager access.UserManager
		user    accesstypes.User
	}{
		{name: "the crew's store, for Ember's founder", manager: crewManager, user: founderUser},
		{name: "the crew's store, for the marshal", manager: crewManager, user: marshalUser},
		{name: "the crew's store, for the person seated in Ember alone", manager: crewManager, user: handUser},
		{name: "the members' store, for the drifter", manager: membersManager, user: drifterUser},
		{name: "the members' store, for the commuter", manager: membersManager, user: commuterUser},
	} {
		if roles := rolesHeldIn(ctx, t, gone.manager, gone.user, emberScope); len(roles) != 0 {
			t.Errorf("%s holds %v in the deleted sector, want none", gone.name, roles)
		}
	}
	// The custom role made in Ember went with it.
	if hasRole(ctx, t, crewManager, emberScope, watch) {
		t.Errorf("the custom role %s made in Ember still exists, want it deleted with the sector", watch)
	}
	// So did Ember's group, with its members and its role.
	for _, table := range []struct{ name, column string }{{"CrewGroups", "Id"}, {"CrewGroupMembers", "GroupId"}, {"CrewGroupRoles", "GroupId"}} {
		if n := rowsWhere(ctx, t, s, table.name, table.column, emberGroup); n != 0 {
			t.Errorf("%s holds %d row(s) of Ember's group after the delete, want none", table.name, n)
		}
	}

	// A person who holds a role elsewhere stays, with that role, and so does the person
	// Cinder's directory signs in, with the identity and the session, holding nothing in
	// Ember.
	if got := crewHolds(t, s.db, "marshal"); !slices.Contains(got, "domain/"+anvil+" SectorMarshal") {
		t.Errorf("the marshal holds %v after the delete, want Anvil's marshal role kept", got)
	}
	if !crewAccountExists(t, s.db, visitorID) {
		t.Error("the account of the person Cinder's directory signs in is gone, want it standing: Cinder manages it")
	}
	if got := crewHolds(t, s.db, string(visitor)); len(got) != 0 {
		t.Errorf("the person Cinder's directory signs in holds %v after the delete, want nothing", got)
	}
	if n := rowsWhere(ctx, t, s, "CrewSessionIdentities", "UserId", visitorID); n != 1 {
		t.Errorf("the person Cinder's directory signs in has %d sign-in identities after the delete, want the one", n)
	}
	if n := rowsWhere(ctx, t, s, "CrewSessions", "Id", visitorSession); n != 1 || readColumn[bool](ctx, t, s.db, "CrewSessions", spanner.Key{visitorSession}, "Expired") {
		t.Error("the session of the person Cinder's directory signs in is ended or gone after the delete, want it standing")
	}
	// The accounts that were Ember's alone are gone with their sign-in identities: its
	// founder's, the person's seated in Ember alone, and the one Ember's directory signed
	// in, who held nothing. Their sessions are ended, and the sessions' rows stand until
	// the timed removal takes them.
	for _, gone := range []struct{ name, id, session string }{
		{name: "Ember's founder", id: founderID, session: founderSession},
		{name: "the person seated in Ember alone", id: handID, session: handSession},
		{name: "the person Ember's directory signed in", id: localID, session: localSession},
	} {
		if crewAccountExists(t, s.db, gone.id) {
			t.Errorf("the account of %s still exists, want it deleted with the sector: it was Ember's alone", gone.name)
		}
		if n := rowsWhere(ctx, t, s, "CrewSessionIdentities", "UserId", gone.id); n != 0 {
			t.Errorf("%s has %d sign-in identities after the delete, want none: they go with the account", gone.name, n)
		}
		if n := rowsWhere(ctx, t, s, "CrewSessions", "Id", gone.session); n != 1 {
			t.Errorf("%s's session: %d row(s) after the delete, want the row left for the timed removal", gone.name, n)
		} else if !readColumn[bool](ctx, t, s.db, "CrewSessions", spanner.Key{gone.session}, "Expired") {
			t.Errorf("%s's session is not ended after the account was deleted", gone.name)
		}
	}
	if got := rolesHeldIn(ctx, t, membersManager, commuterUser, anvilScope); !slices.Equal(got, []accesstypes.Role{"ClientPortal"}) {
		t.Errorf("the commuter holds %v in Anvil after the delete, want ClientPortal kept", got)
	}
	if _, err := s.db.Single().ReadRow(ctx, "MembersSessionUsers", spanner.Key{commuterID}, []string{"Id"}); err != nil {
		t.Errorf("the commuter's account after the delete: %v, want it kept: the commuter holds a role in Anvil", err)
	}
	if _, err := s.db.Single().ReadRow(ctx, "MembersSessionUsers", spanner.Key{drifterID}, []string{"Id"}); spanner.ErrCode(err) != codes.NotFound {
		t.Errorf("the account of the member who held roles in Ember alone: err = %v, want not found: it was Ember's alone", err)
	}

	// The invitations into Ember are withdrawn, the crew's and the members': each link
	// is consumed.
	if !readColumn[spanner.NullTime](ctx, t, s.db, "CrewSessionMailLinks", spanner.Key{invitation.ID}, "ConsumedAt").Valid {
		t.Error("the crew's invitation into the deleted sector still stands, want it withdrawn")
	}
	if !readColumn[spanner.NullTime](ctx, t, s.db, "MembersSessionMailLinks", spanner.Key{memberLink}, "ConsumedAt").Valid {
		t.Error("the members' invitation into the deleted sector still stands, want it withdrawn")
	}
}

// leftBySignIn writes what a sign-in leaves of a person of the crew: an identity under
// the method and the connection, and a session. It answers the session's id.
func leftBySignIn(ctx context.Context, t *testing.T, s *served, username accesstypes.User, userID, method, connection string) string {
	t.Helper()

	identity, session := newID(t), newID(t)
	if _, err := s.db.Apply(ctx, []*spanner.Mutation{
		spanner.InsertMap("CrewSessionIdentities", map[string]any{
			"Id": identity, "UserId": userID, "Method": method, "Connection": connection, "Subject": "subject-of-" + string(username),
			"CreatedAt": spanner.CommitTimestamp, "LastUsedAt": spanner.CommitTimestamp,
		}),
		spanner.InsertMap("CrewSessions", map[string]any{
			"Id": session, "Username": string(username), "Expired": false, "UserId": userID,
			"CreatedAt": spanner.CommitTimestamp, "UpdatedAt": spanner.CommitTimestamp,
		}),
	}); err != nil {
		t.Fatalf("writing %s's sign-in identity and session: %v", username, err)
	}

	return session
}

// sectorKeyedTables are Lodestar's tables that carry a sector's key on the row itself,
// the directories record apart: a sector's delete takes each one's rows of the sector.
var sectorKeyedTables = []string{"Wings", "Hangars", "HangarPasses", "Missions", "Consignments", "DroidReports", "DistressCalls", "DistressRelays"}

// sectorDirectoryRows counts the rows of the directories record, a sector's own or, for
// an empty sector, every one, as committed.
func sectorDirectoryRows(ctx context.Context, t *testing.T, s *served, sector string) int64 {
	t.Helper()

	return sectorRows(ctx, t, s, "SectorDirectories", sector)
}

// sectorRows counts the rows of a table that carries a sector's key, a sector's own or,
// for an empty sector, every one, as committed.
func sectorRows(ctx context.Context, t *testing.T, s *served, table, sector string) int64 {
	t.Helper()

	if sector == "" {
		return rowsWhere(ctx, t, s, table, "", "")
	}

	return rowsWhere(ctx, t, s, table, "SectorId", sector)
}

// rowsWhere counts the rows of a table whose column holds the value or, for an empty
// column, every one, as committed. The table and the column are the test's own words.
func rowsWhere(ctx context.Context, t *testing.T, s *served, table, column, value string) int64 {
	t.Helper()

	statement := spanner.Statement{SQL: "SELECT COUNT(*) FROM " + table}
	if column != "" {
		statement = spanner.Statement{SQL: "SELECT COUNT(*) FROM " + table + " WHERE " + column + " = @value", Params: map[string]any{"value": value}}
	}
	iter := s.db.Single().Query(ctx, statement)
	defer iter.Stop()
	row, err := iter.Next()
	if err != nil {
		t.Fatalf("counting the rows of %s: %v", table, err)
	}
	var n int64
	if err := row.Columns(&n); err != nil {
		t.Fatalf("reading the count of %s: %v", table, err)
	}

	return n
}

// rolesHeldIn reads the roles the user holds in the one scope, as the auth's permission
// store holds them.
func rolesHeldIn(ctx context.Context, t *testing.T, manager access.UserManager, user accesstypes.User, scope accesstypes.PolicyScope) []accesstypes.Role {
	t.Helper()

	held, err := manager.UserRoles(ctx, user, scope)
	if err != nil {
		t.Fatalf("access.UserManager.UserRoles(%s) error = %v", user, err)
	}

	return held[scope]
}

// hasRole reports whether the role exists in the scope, as the auth's permission store
// lists the scope's roles.
func hasRole(ctx context.Context, t *testing.T, manager access.UserManager, scope accesstypes.PolicyScope, role accesstypes.Role) bool {
	t.Helper()

	roles, err := manager.Roles(ctx, scope)
	if err != nil {
		t.Fatalf("access.UserManager.Roles() error = %v", err)
	}

	return slices.ContainsFunc(roles, func(held access.RoleInfo) bool {
		return held.Name == role
	})
}

// newID answers a new id's text.
func newID(t *testing.T) string {
	t.Helper()

	id, err := impulse.NewUUID()
	if err != nil {
		t.Fatalf("impulse.NewUUID() error = %v", err)
	}

	return id.String()
}
