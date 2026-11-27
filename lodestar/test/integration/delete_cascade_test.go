// Demonstrates: @deleteCascade, @deleteCascade.own-kind, @deleteCascade.other-columns.
package integration

// This suite pins what a record declares its delete takes (@deleteCascade). A mission
// names its sorties, their expenses and its documents as going with it; a client names
// its contacts, and a mission or a consignment as refusing its delete. The generator
// holds each list to the schema and writes the description the first test reads. A
// wing, a hangar, a refit and a sortie declare theirs too
// (TestDeleteCascade_fourRecords).
//
// The rest is the delete itself on the served routes. A delete of a mission with rows
// under it deletes nothing unless the request says it takes them: it is answered 428
// with the question a person is asked first, which counts each kind they may see, and
// carries a token. The same delete saying so, with that token, in the same session (the
// remove operation's value {"everythingUnder": true, "token": "…"}) deletes the rows in
// code, deepest first and the mission last, in the one transaction:
// the mission's change record is written and a document's stored file is released. Every
// row is checked first as its own delete would check it, so one row the person may not
// delete, or one row its rule refuses, stops the whole delete with nothing gone. A
// client with a mission is refused whatever the request says. A record whose rows cost
// more than one commit takes is deleted in rounds: each request takes what fits, deepest
// first, and answers what it deleted, and the same request sent again
// goes on until the record goes with the last of them. The deletes are the Spanner write
// path's.
//
// A distress call and its relays are the two links that are no key to another table's
// key. A relay names its call by its sector and the case number, columns of the call that
// are not its key, and an answer names the relay it answers, a row of its own table: a
// call's delete takes its relays and every answer under them, and a relay's delete its
// answers.

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"google.golang.org/grpc/codes"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/lodestar/pkg/resources"
	"github.com/impulseframework/impulse/lodestar/pkg/router"
	"github.com/impulseframework/impulse/resource"
)

// TestDeleteCascade_description reads the descriptions the generator wrote for two of the
// records that declare one: the kinds taken in delete order, deepest first and kinds of
// one depth in reverse order of their names, with each kind's way up to the record, and
// the kinds that refuse the delete.
func TestDeleteCascade_description(t *testing.T) {
	t.Parallel()

	kinds := func(list []resource.DeleteKind) []accesstypes.Resource {
		names := make([]accesstypes.Resource, 0, len(list))
		for i := range list {
			names = append(names, list[i].Resource)
		}

		return names
	}

	mission := resources.Mission{}.DeleteTree()
	// An expense is on a sortie, so it is deleted before the sortie it is on. Sorties and
	// documents are both straight under the mission, so the question, which reads the order
	// backward, names the documents first.
	if got, want := kinds(mission.Taken), []accesstypes.Resource{"SortieExpenses", "Sorties", "MissionDocuments"}; !slices.Equal(got, want) {
		t.Errorf("a mission's delete takes %v, want %v", got, want)
	}
	if len(mission.RefusedBy) != 0 {
		t.Errorf("a mission's delete is refused by %v, want nothing", kinds(mission.RefusedBy))
	}
	wantWay := []resource.DeletePath{{
		{Columns: []string{"SortieId"}, Table: "Sorties", References: []string{"Id"}},
		{Columns: []string{"MissionId"}, Table: "Missions", References: []string{"Id"}},
	}}
	if got := mission.Taken[0].Paths; !reflect.DeepEqual(got, wantWay) {
		t.Errorf("an expense's way up to its mission is %v, want %v", got, wantWay)
	}
	// A document stores a file, and its release is in what a row's delete costs.
	if got := mission.Taken[2].Cost.Files; got != 1 {
		t.Errorf("a mission document's cost counts %d stored files, want 1", got)
	}

	client := resources.Client{}.DeleteTree()
	if got, want := kinds(client.Taken), []accesstypes.Resource{"ClientContacts"}; !slices.Equal(got, want) {
		t.Errorf("a client's delete takes %v, want %v", got, want)
	}
	if got, want := kinds(client.RefusedBy), []accesstypes.Resource{"Missions", "Consignments"}; !slices.Equal(got, want) {
		t.Errorf("a client's delete is refused by %v, want %v", got, want)
	}

	// A relay is under its call by its sector and the case number, columns of the call
	// outside its key, and under the relay it answers by that relay's id and the sector,
	// which the call's description carries too.
	answers := resource.DeleteLink{Columns: []string{"AnswersRelayId", "SectorId"}, Table: "DistressRelays", References: []string{"Id", "SectorId"}, MayBeEmpty: true}
	call := resources.DistressCall{}.DeleteTree()
	if got, want := kinds(call.Taken), []accesstypes.Resource{"DistressRelays"}; !slices.Equal(got, want) {
		t.Fatalf("a distress call's delete takes %v, want %v", got, want)
	}
	bySectorAndCaseNumber := []resource.DeletePath{{{Columns: []string{"SectorId", "CaseNumber"}, Table: "DistressCalls", References: []string{"SectorId", "CaseNumber"}}}}
	if got := call.Taken[0].Paths; !reflect.DeepEqual(got, bySectorAndCaseNumber) {
		t.Errorf("a relay's way up to its call is %v, want %v", got, bySectorAndCaseNumber)
	}
	if got, want := call.Taken[0].Within, []resource.DeleteLink{answers}; !reflect.DeepEqual(got, want) {
		t.Errorf("a relay is under a relay by %v, want %v", got, want)
	}

	// A relay lists its own kind: an answer's way up to the relay is its link to it.
	relay := resources.DistressRelay{}.DeleteTree()
	if got, want := kinds(relay.Taken), []accesstypes.Resource{"DistressRelays"}; !slices.Equal(got, want) {
		t.Fatalf("a distress relay's delete takes %v, want %v", got, want)
	}
	if got, want := relay.Taken[0].Paths, []resource.DeletePath{{answers}}; !reflect.DeepEqual(got, want) {
		t.Errorf("an answer's way up to its relay is %v, want %v", got, want)
	}
	if got := relay.Taken[0].Within; len(got) != 0 {
		t.Errorf("a relay's own kind carries the links %v beside its ways up, want none", got)
	}
}

// cascadeMission is a mission a test writes with rows under it: two sorties, one expense
// on the first sortie, and one document whose file is in the Documents store.
type cascadeMission struct {
	mission, sortieA, sortieB, expense, document, storeKey string
}

// rows are the mission's rows by table, the mission's own among them.
func (m *cascadeMission) rows() map[string][]string {
	return map[string][]string{
		"Missions":         {m.mission},
		"Sorties":          {m.sortieA, m.sortieB},
		"SortieExpenses":   {m.expense},
		"MissionDocuments": {m.document},
	}
}

// writeCascadeMission writes mission number n in Anvil with its rows, the expense in
// the given category, and stores the document's file.
func writeCascadeMission(t *testing.T, db interface {
	Apply(ctx context.Context, ms []*spanner.Mutation, opts ...spanner.ApplyOption) (time.Time, error)
}, stores *testStores, n int, category string,
) cascadeMission {
	t.Helper()

	id := func(kind string, row int) string {
		return fmt.Sprintf("%s00000-0000-4000-8000-0000000c%02d%02d", kind, n, row)
	}
	m := cascadeMission{
		mission: id("8c0", 1), sortieA: id("9c0", 1), sortieB: id("9c0", 2), expense: id("91c", 1), document: id("a5c", 1), storeKey: id("5c0", 1),
	}
	sortie := func(sortieID string) *spanner.Mutation {
		return spanner.InsertMap("Sorties", map[string]any{
			"Id": sortieID, "MissionId": m.mission, "ShipId": shipLanternID, "PilotUserId": "f0000000-0000-4000-8000-000000000004", "LaunchedAt": time.Now().UTC(),
		})
	}
	if _, err := db.Apply(t.Context(), []*spanner.Mutation{
		spanner.InsertMap("Missions", map[string]any{
			"Id": m.mission, "SectorId": anvil, "ClientId": clientHalvardID, "KindId": "courier", "Title": fmt.Sprintf("Cascade run %d", n), "Hazard": 1,
			"Fee": *big.NewRat(100, 1), "Deadline": time.Now().Add(24 * time.Hour), "BookedByUserId": "f0000000-0000-4000-8000-000000000004", "BookedByName": "marshal", "StatusId": "stood_down",
		}),
		sortie(m.sortieA),
		sortie(m.sortieB),
		spanner.InsertMap("SortieExpenses", map[string]any{"Id": m.expense, "SortieId": m.sortieA, "Category": category, "Amount": big.NewRat(750, 1)}),
		spanner.InsertMap("MissionDocuments", map[string]any{
			"Id": m.document, "MissionId": m.mission, "Title": "Manifest", "FileName": "manifest.txt", "ContentType": "text/plain", "Size": 8,
			"StoreKey": m.storeKey, "UploadedByUserId": "f0000000-0000-4000-8000-000000000004", "UploadedByName": "marshal", "UploadedAt": time.Now().UTC(), "Digest": make([]byte, sha256.Size),
		}),
	}); err != nil {
		t.Fatalf("spanner.Apply(mission %d): %v", n, err)
	}
	if err := stores.documents.Put(t.Context(), m.storeKey, "text/plain", strings.NewReader("manifest")); err != nil {
		t.Fatalf("storing the document's file: %v", err)
	}

	return m
}

// TestDeleteCascade_takesWhatIsUnder pins the delete of a record that declares what goes
// with it, on the served routes. The cases run in order over missions of their own.
func TestDeleteCascade_takesWhatIsUnder(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	// The deletes that go ahead bring the token their question gave, in its session.
	keepSession(t, defaultUser)

	db, err := prepareDatabase(ctx, t, migrationsSource, demoSeedSource)
	if err != nil {
		t.Fatal(err)
	}
	stores := newTestStores()
	as := func(g grants) http.Handler {
		return router.NewTestRouter(newAppWithStores(db, &staticAccess{g: g}, stores))
	}
	stands := func(t *testing.T, table, id string) bool {
		t.Helper()

		_, err := db.Single().ReadRow(ctx, table, spanner.Key{id}, []string{"Id"})
		if err != nil && spanner.ErrCode(err) != codes.NotFound {
			t.Fatalf("ReadRow(%s, %s): %v", table, id, err)
		}

		return err == nil
	}
	// allStand and allGone check every row of a mission, and its document's file.
	allStand := func(t *testing.T, m cascadeMission) {
		t.Helper()

		for table, ids := range m.rows() {
			for _, id := range ids {
				if !stands(t, table, id) {
					t.Errorf("%s %s is gone, though the delete was not made", table, id)
				}
			}
		}
		if !slices.Contains(stores.documents.Keys(), m.storeKey) {
			t.Errorf("the document's file %s is gone, though the delete was not made", m.storeKey)
		}
	}
	allGone := func(t *testing.T, m cascadeMission) {
		t.Helper()

		for table, ids := range m.rows() {
			for _, id := range ids {
				if stands(t, table, id) {
					t.Errorf("%s %s stands after the mission was deleted with everything under it", table, id)
				}
			}
		}
		if slices.Contains(stores.documents.Keys(), m.storeKey) {
			t.Errorf("the document's file %s is still stored after its row was deleted", m.storeKey)
		}
	}
	remove := func(m cascadeMission, everythingUnder bool) string {
		if !everythingUnder {
			return fmt.Sprintf(`[{"op":"remove","path":%q}]`, opPath(anvil, "missions/"+m.mission))
		}

		return fmt.Sprintf(`[{"op":"remove","path":%q,"value":{"everythingUnder":true}}]`, opPath(anvil, "missions/"+m.mission))
	}
	// removeOn is the remove that goes ahead on the token the question gave.
	removeOn := func(m cascadeMission, token string) string {
		return fmt.Sprintf(`[{"op":"remove","path":%q,"value":{"everythingUnder":true,"token":%q}}]`, opPath(anvil, "missions/"+m.mission), token)
	}
	const batch = "/console/api/resources"
	under := []accesstypes.Resource{"Sorties", "SortieExpenses", "MissionDocuments"}
	everything := grants{accesstypes.Delete: append([]accesstypes.Resource{missionsResource}, under...), accesstypes.List: under}

	t.Run("a delete that does not say it takes the rows is asked the question, and nothing is gone", func(t *testing.T) {
		m := writeCascadeMission(t, db, stores, 1, "supplies")

		status, body := doRequest(t, as(everything), http.MethodPatch, batch, remove(m, false))
		assertStatus(t, status, http.StatusPreconditionRequired, body)
		if got, want := refusalMessage(t, body), "Delete this mission and everything under it: 1 mission document, 2 sorties, 1 sortie expense. This can't be undone."; got != want {
			t.Errorf("message = %q, want %q", got, want)
		}
		allStand(t, m)
		token := deleteToken(t, body)

		// The same delete, saying so on the question's token, takes them all in the one
		// transaction.
		before := countChangeEvents(ctx, t, db, "Missions")
		status, body = doRequest(t, as(everything), http.MethodPatch, batch, removeOn(m, token))
		assertStatus(t, status, http.StatusOK, body)
		allGone(t, m)
		if got := countChangeEvents(ctx, t, db, "Missions") - before; got != 1 {
			t.Errorf("the mission's delete wrote %d change records for Missions, want 1", got)
		}
	})

	t.Run("the question counts only the kinds the person may see", func(t *testing.T) {
		m := writeCascadeMission(t, db, stores, 2, "supplies")
		sortiesSeen := grants{accesstypes.Delete: everything[accesstypes.Delete], accesstypes.List: {"Sorties"}}

		status, body := doRequest(t, as(sortiesSeen), http.MethodPatch, batch, remove(m, false))
		assertStatus(t, status, http.StatusPreconditionRequired, body)
		if got, want := refusalMessage(t, body), "Delete this mission and everything under it: 2 sorties, and records you can't see. This can't be undone."; got != want {
			t.Errorf("message = %q, want %q", got, want)
		}
		allStand(t, m)
	})

	t.Run("one row the person may not delete stops the whole delete", func(t *testing.T) {
		m := writeCascadeMission(t, db, stores, 3, "supplies")
		noExpenses := grants{accesstypes.Delete: {missionsResource, "Sorties", "MissionDocuments"}, accesstypes.List: under}

		for _, everythingUnder := range []bool{false, true} {
			status, body := doRequest(t, as(noExpenses), http.MethodPatch, batch, remove(m, everythingUnder))
			assertStatus(t, status, http.StatusForbidden, body)
			if got, want := refusalMessage(t, body), "This mission has sortie expenses that you can't delete, so it can't be deleted."; got != want {
				t.Errorf("message = %q, want %q", got, want)
			}
		}
		allStand(t, m)

		// A kind the person may not list either is not named for them.
		unseen := grants{accesstypes.Delete: noExpenses[accesstypes.Delete], accesstypes.List: {"Sorties", "MissionDocuments"}}
		status, body := doRequest(t, as(unseen), http.MethodPatch, batch, remove(m, true))
		assertStatus(t, status, http.StatusForbidden, body)
		if got, want := refusalMessage(t, body), "This mission has records under it that you can't delete, so it can't be deleted."; got != want {
			t.Errorf("message = %q, want %q", got, want)
		}
		allStand(t, m)
	})

	t.Run("one row its rule refuses stops the whole delete, in the rule's own sentence", func(t *testing.T) {
		m := writeCascadeMission(t, db, stores, 4, "penalty")

		status, body := doRequest(t, as(everything), http.MethodPatch, batch, remove(m, true))
		assertStatus(t, status, http.StatusBadRequest, body)
		if got, want := refusalMessage(t, body), "A penalty stays on the sortie's record and can't be deleted."; got != want {
			t.Errorf("message = %q, want %q", got, want)
		}
		allStand(t, m)
	})

	t.Run("a record with nothing under it is deleted without a question", func(t *testing.T) {
		const bare = "8c000000-0000-4000-8000-0000000c0501"
		if _, err := db.Apply(ctx, []*spanner.Mutation{spanner.InsertMap("Missions", map[string]any{
			"Id": bare, "SectorId": anvil, "ClientId": clientHalvardID, "KindId": "courier", "Title": "Cascade run 5", "Hazard": 1,
			"Fee": *big.NewRat(100, 1), "Deadline": time.Now().Add(24 * time.Hour), "BookedByUserId": "f0000000-0000-4000-8000-000000000004", "BookedByName": "marshal", "StatusId": "stood_down",
		})}); err != nil {
			t.Fatalf("spanner.Apply(): %v", err)
		}

		status, body := doRequest(t, as(grants{accesstypes.Delete: {missionsResource}}), http.MethodPatch, batch, remove(cascadeMission{mission: bare}, false))
		assertStatus(t, status, http.StatusOK, body)
		if stands(t, "Missions", bare) {
			t.Error("the mission with nothing under it stands after its delete")
		}
	})

	t.Run("a client's contacts go with it once the request says so, and a mission refuses it either way", func(t *testing.T) {
		const (
			loneHarbor = "10000000-0000-4000-8000-0000000000c1"
			hale       = "31000000-0000-4000-8000-0000000000c1"
			ines       = "31000000-0000-4000-8000-0000000000c2"
			clients    = "/console/api/clients"
		)
		if _, err := db.Apply(ctx, []*spanner.Mutation{
			spanner.InsertMap("Clients", map[string]any{
				"Id": loneHarbor, "Name": "Lone Harbor Freight", "ContactName": "Harbor Desk", "ContactEmail": "desk@lone-harbor.example", "Trusted": true,
			}),
			spanner.InsertMap("ClientContacts", map[string]any{"Id": hale, "UserId": "f1000000-0000-4000-8000-0000000000c1", "ClientId": loneHarbor, "DisplayName": "Harbor Hale"}),
			spanner.InsertMap("ClientContacts", map[string]any{"Id": ines, "UserId": "f1000000-0000-4000-8000-0000000000c2", "ClientId": loneHarbor, "DisplayName": "Harbor Ines"}),
		}); err != nil {
			t.Fatalf("spanner.Apply(): %v", err)
		}
		h := as(grants{
			accesstypes.Delete: {"Clients", "ClientContacts"},
			accesstypes.List:   {"ClientContacts", missionsResource, "Consignments"},
		})

		status, body := doRequest(t, h, http.MethodPatch, clients, fmt.Sprintf(`[{"op":"remove","path":"/%s"}]`, loneHarbor))
		assertStatus(t, status, http.StatusPreconditionRequired, body)
		if got, want := refusalMessage(t, body), "Delete this client and everything under it: 2 client contacts. This can't be undone."; got != want {
			t.Errorf("message = %q, want %q", got, want)
		}
		for table, id := range map[string]string{"Clients": loneHarbor, "ClientContacts": hale} {
			if !stands(t, table, id) {
				t.Errorf("%s %s is gone after the question", table, id)
			}
		}
		token := deleteToken(t, body)

		// Halvard Freight has missions and consignments, which a client lists under
		// refusedBy: saying it takes everything changes nothing. Its missions are the
		// seeded ten and those the cases above booked for it and left standing, so the
		// sentence's count is read from the table.
		booked := 0
		if err := db.Single().Query(ctx, spanner.Statement{
			SQL:    "SELECT Id FROM Missions WHERE ClientId = @client",
			Params: map[string]any{"client": clientHalvardID},
		}).Do(func(*spanner.Row) error {
			booked++

			return nil
		}); err != nil {
			t.Fatalf("Query(Missions): %v", err)
		}
		if booked <= 10 {
			t.Fatalf("Halvard Freight has %d missions, want more than the seeded 10", booked)
		}
		status, body = doRequest(t, h, http.MethodPatch, clients, fmt.Sprintf(`[{"op":"remove","path":"/%s","value":{"everythingUnder":true}}]`, clientHalvardID))
		assertStatus(t, status, http.StatusConflict, body)
		if got, want := refusalMessage(t, body), fmt.Sprintf("This client has %d missions and 5 consignments, so it can't be deleted.", booked); got != want {
			t.Errorf("message = %q, want %q", got, want)
		}
		if !stands(t, "Clients", clientHalvardID) {
			t.Error("Halvard Freight is gone after its refused delete")
		}

		status, body = doRequest(t, h, http.MethodPatch, clients, fmt.Sprintf(`[{"op":"remove","path":"/%s","value":{"everythingUnder":true,"token":%q}}]`, loneHarbor, token))
		assertStatus(t, status, http.StatusOK, body)
		for _, row := range [][2]string{{"Clients", loneHarbor}, {"ClientContacts", hale}, {"ClientContacts", ines}} {
			if stands(t, row[0], row[1]) {
				t.Errorf("%s %s stands after the client was deleted with everything under it", row[0], row[1])
			}
		}
	})

	t.Run("a remove operation's value that is no such object is a 400", func(t *testing.T) {
		m := writeCascadeMission(t, db, stores, 6, "supplies")

		ops := fmt.Sprintf(`[{"op":"remove","path":%q,"value":{"everythingUnder":"yes"}}]`, opPath(anvil, "missions/"+m.mission))
		status, body := doRequest(t, as(everything), http.MethodPatch, batch, ops)
		assertStatus(t, status, http.StatusBadRequest, body)
		allStand(t, m)
	})
}

// takenRow is a row a case of TestDeleteCascade_fourRecords reads back: its table, its
// key and a column of the table.
type takenRow struct {
	table  string
	key    spanner.Key
	column string
}

// rowByID is the row of the table whose key is its Id.
func rowByID(table, id string) takenRow {
	return takenRow{table: table, key: spanner.Key{id}, column: "Id"}
}

// TestDeleteCascade_fourRecords pins the declared deletes (@deleteCascade) of a wing, a
// hangar, a refit and a sortie, on the served routes: a wing takes its squadrons with
// their memberships and the hangar passes they hold, a hangar its passes and its ships
// with their refits and tasks, a refit its tasks, and a sortie its expenses. A delete
// with rows under the record is asked first, with a count of each kind, and deletes
// nothing; the same delete saying it takes them deletes every row through its own
// delete, so a task's photo is released, an expense's rule is asked and a tracked row's
// change record is written. An expense's rule refuses its sortie's delete whatever the
// request says. Each case writes the rows it deletes, in Anvil, and the person may
// delete and list every kind.
func TestDeleteCascade_fourRecords(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	// The deletes that go ahead bring the token their question gave, in its session.
	keepSession(t, defaultUser)

	db, err := prepareDatabase(ctx, t, migrationsSource, demoSeedSource)
	if err != nil {
		t.Fatal(err)
	}
	stores := newTestStores()
	h := router.NewTestRouter(newAppWithStores(db, &staticAccess{g: grants{
		accesstypes.Delete: {"Wings", "Squadrons", "SquadronMemberships", "HangarPasses", "Hangars", "Ships", "Refits", "RefitTasks", "Sorties", "SortieExpenses"},
		accesstypes.List:   {"Squadrons", "SquadronMemberships", "HangarPasses", "Ships", "Refits", "RefitTasks", "Sorties", "SortieExpenses"},
	}}, stores))
	const (
		batch   = "/console/api/resources"
		marshal = "f0000000-0000-4000-8000-000000000004"
	)

	// Every row a case writes has an id of its own: a kind's three hex digits, then a
	// count over the whole test. The cases run side by side, so the count is atomic, and
	// what a case asks of the database is about its own rows.
	var written atomic.Int64
	id := func(kind string) string {
		return fmt.Sprintf("%s00000-0000-4000-8000-0000000d%04d", kind, written.Add(1))
	}
	// changes counts the change records of one row.
	changes := func(t *testing.T, table, rowID string) int64 {
		t.Helper()

		iter := db.Single().Query(ctx, spanner.Statement{
			SQL:    "SELECT COUNT(*) FROM DataChangeEvents WHERE TableName = @table AND RowId = @row",
			Params: map[string]any{"table": table, "row": rowID},
		})
		defer iter.Stop()
		row, err := iter.Next()
		if err != nil {
			t.Fatalf("counting the change records of %s %s: %v", table, rowID, err)
		}
		var count int64
		if err := row.Columns(&count); err != nil {
			t.Fatalf("row.Columns: %v", err)
		}

		return count
	}
	write := func(t *testing.T, rows ...*spanner.Mutation) {
		t.Helper()

		if _, err := db.Apply(ctx, rows); err != nil {
			t.Fatalf("spanner.Apply(): %v", err)
		}
	}
	stands := func(t *testing.T, row takenRow) bool {
		t.Helper()

		_, err := db.Single().ReadRow(ctx, row.table, row.key, []string{row.column})
		if err != nil && spanner.ErrCode(err) != codes.NotFound {
			t.Fatalf("ReadRow(%s, %v): %v", row.table, row.key, err)
		}

		return err == nil
	}
	allStand := func(t *testing.T, rows []takenRow) {
		t.Helper()

		for _, row := range rows {
			if !stands(t, row) {
				t.Errorf("%s %v is gone, though the delete was not made", row.table, row.key)
			}
		}
	}
	remove := func(path string, everythingUnder bool) string {
		if !everythingUnder {
			return fmt.Sprintf(`[{"op":"remove","path":%q}]`, path)
		}

		return fmt.Sprintf(`[{"op":"remove","path":%q,"value":{"everythingUnder":true}}]`, path)
	}
	// askedThenTaken sends the delete of the record at path twice. Plain, it is asked the
	// question and every row stands, with the stored files; saying it takes the rows, on
	// the question's token, it deletes every row and releases the files.
	askedThenTaken := func(t *testing.T, target, path, question string, rows []takenRow, files ...string) {
		t.Helper()

		status, body := doRequest(t, h, http.MethodPatch, target, remove(path, false))
		assertStatus(t, status, http.StatusPreconditionRequired, body)
		if got := refusalMessage(t, body); got != question {
			t.Errorf("message = %q, want %q", got, question)
		}
		allStand(t, rows)
		for _, key := range files {
			if !slices.Contains(stores.files.Keys(), key) {
				t.Errorf("the stored file %s is gone, though the delete was not made", key)
			}
		}

		status, body = doRequest(t, h, http.MethodPatch, target, fmt.Sprintf(`[{"op":"remove","path":%q,"value":{"everythingUnder":true,"token":%q}}]`, path, deleteToken(t, body)))
		assertStatus(t, status, http.StatusOK, body)
		for _, row := range rows {
			if stands(t, row) {
				t.Errorf("%s %v stands after its record was deleted with everything under it", row.table, row.key)
			}
		}
		for _, key := range files {
			if slices.Contains(stores.files.Keys(), key) {
				t.Errorf("the stored file %s is still stored after its row was deleted", key)
			}
		}
	}
	// refused sends the delete of the record at path, plain and saying it takes the rows:
	// each is refused in the sentence, and every row stands.
	refused := func(t *testing.T, path string, wantStatus int, want string, rows []takenRow) {
		t.Helper()

		for _, everythingUnder := range []bool{false, true} {
			status, body := doRequest(t, h, http.MethodPatch, batch, remove(path, everythingUnder))
			assertStatus(t, status, wantStatus, body)
			if got := refusalMessage(t, body); got != want {
				t.Errorf("message = %q, want %q", got, want)
			}
		}
		allStand(t, rows)
	}

	wing := func(wingID string) *spanner.Mutation {
		return spanner.Insert("Wings", []string{"Id", "SectorId", "Name"}, []any{wingID, anvil, "Cascade Wing"})
	}
	squadron := func(squadronID, wingID string) *spanner.Mutation {
		return spanner.Insert("Squadrons", []string{"Id", "WingId", "Name"}, []any{squadronID, wingID, "Cascade Squadron"})
	}
	membership := func(squadronID string) *spanner.Mutation {
		return spanner.Insert("SquadronMemberships", []string{"SquadronId", "UserId"}, []any{squadronID, marshal})
	}
	membershipRow := func(squadronID string) takenRow {
		return takenRow{table: "SquadronMemberships", key: spanner.Key{squadronID, marshal}, column: "UserId"}
	}
	// pass is a pass into the hangar, held by a person or by a squadron (column).
	pass := func(passID, hangarID, column, holder string) *spanner.Mutation {
		return spanner.InsertMap("HangarPasses", map[string]any{"Id": passID, "SectorId": anvil, "HangarId": hangarID, column: holder, "LevelId": "visit"})
	}
	hangar := func(hangarID string) *spanner.Mutation {
		return spanner.Insert("Hangars", []string{"Id", "SectorId", "Name", "Zone"}, []any{hangarID, anvil, "Cascade Dock", "dock"})
	}
	ship := func(shipID, hangarID string) *spanner.Mutation {
		return spanner.Insert("Ships", []string{"Id", "HangarId", "ClassId", "Registry", "Name"}, []any{shipID, hangarID, shipClassKestrelID, "CAS-" + shipID[32:], "Cascade Hull"})
	}
	refit := func(refitID, shipID string) *spanner.Mutation {
		return spanner.InsertMap("Refits", map[string]any{"Id": refitID, "ShipId": shipID, "StatusId": "docked", "OpenedByUserId": marshal, "OpenedByName": "marshal"})
	}
	// task is the refit's task n, with a photo where photo is a stored file's key.
	task := func(refitID string, n int64, photo string) *spanner.Mutation {
		row := map[string]any{"Id": refitID, "TaskNumber": n, "Instructions": fmt.Sprintf("Cascade task %d", n)}
		if photo != "" {
			row["PhotoKey"] = photo
		}

		return spanner.InsertMap("RefitTasks", row)
	}
	taskRow := func(refitID string, n int64) takenRow {
		return takenRow{table: "RefitTasks", key: spanner.Key{refitID, n}, column: "Id"}
	}
	// mission is a stood-down mission, assigned to no squadron.
	mission := func(missionID string) *spanner.Mutation {
		return spanner.InsertMap("Missions", map[string]any{
			"Id": missionID, "SectorId": anvil, "ClientId": clientHalvardID, "KindId": "courier", "Title": "Cascade run", "Hazard": 1,
			"Fee": *big.NewRat(100, 1), "Deadline": time.Now().Add(24 * time.Hour), "BookedByUserId": marshal, "BookedByName": "marshal", "StatusId": "stood_down",
		})
	}
	sortie := func(sortieID, missionID, shipID string) *spanner.Mutation {
		return spanner.InsertMap("Sorties", map[string]any{"Id": sortieID, "MissionId": missionID, "ShipId": shipID, "PilotUserId": marshal, "LaunchedAt": time.Now().UTC()})
	}
	expense := func(expenseID, sortieID, category string) *spanner.Mutation {
		return spanner.InsertMap("SortieExpenses", map[string]any{"Id": expenseID, "SortieId": sortieID, "Category": category, "Amount": big.NewRat(750, 1)})
	}

	t.Run("a refit's tasks are asked about, then taken, and a task's photo is released", func(t *testing.T) {
		t.Parallel()

		hull, bay, photo := id("7d0"), id("ad0"), id("5d1")
		write(t, ship(hull, hangarAnvilDockID), refit(bay, hull), task(bay, 1, photo), task(bay, 2, ""))
		if err := stores.files.Put(ctx, photo, "image/png", strings.NewReader("PNG hull seals")); err != nil {
			t.Fatalf("storing the task's photo: %v", err)
		}

		askedThenTaken(t, batch, opPath(anvil, "refits/"+bay), "Delete this refit and everything under it: 2 refit tasks. This can't be undone.",
			[]takenRow{rowByID("Refits", bay), taskRow(bay, 1), taskRow(bay, 2)}, photo)
		if got := changes(t, "Refits", bay); got != 1 {
			t.Errorf("the refit's delete wrote %d change records for it, want 1", got)
		}
		if !stands(t, rowByID("Ships", hull)) {
			t.Error("the refit's ship is gone with the refit")
		}
	})

	t.Run("a sortie's expenses are asked about, then taken, and a penalty refuses the delete", func(t *testing.T) {
		t.Parallel()

		run, flight, fuel, supplies := id("8d0"), id("9d0"), id("9d1"), id("9d1")
		write(t, mission(run), sortie(flight, run, shipLanternID), expense(fuel, flight, "fuel"), expense(supplies, flight, "supplies"))

		askedThenTaken(t, batch, opPath(anvil, "sorties/"+flight), "Delete this sortie and everything under it: 2 sortie expenses. This can't be undone.",
			[]takenRow{rowByID("Sorties", flight), rowByID("SortieExpenses", fuel), rowByID("SortieExpenses", supplies)})
		if !stands(t, rowByID("Missions", run)) {
			t.Error("the sortie's mission is gone with the sortie")
		}

		fined, penalty := id("9d0"), id("9d1")
		write(t, sortie(fined, run, shipLanternID), expense(penalty, fined, "penalty"))
		refused(t, opPath(anvil, "sorties/"+fined), http.StatusBadRequest, "A penalty stays on the sortie's record and can't be deleted.",
			[]takenRow{rowByID("Sorties", fined), rowByID("SortieExpenses", penalty)})
	})

	t.Run("a hangar's pass and its ship, with the ship's refit and task, are asked about with every count, then taken", func(t *testing.T) {
		t.Parallel()

		dock, visit, hull, bay := id("6d0"), id("6d1"), id("7d0"), id("ad0")
		write(t, hangar(dock), pass(visit, dock, "UserId", marshal), ship(hull, dock), refit(bay, hull), task(bay, 1, ""))

		askedThenTaken(t, sectorPath(anvil, "hangars"), "/"+dock, "Delete this hangar and everything under it: 1 hangar pass, 1 ship, 1 refit, 1 refit task. This can't be undone.",
			[]takenRow{rowByID("Hangars", dock), rowByID("HangarPasses", visit), rowByID("Ships", hull), rowByID("Refits", bay), taskRow(bay, 1)})
		if got := changes(t, "Ships", hull); got != 1 {
			t.Errorf("the hangar's delete wrote %d change records for the ship it took, want 1", got)
		}
		if got := changes(t, "Refits", bay); got != 1 {
			t.Errorf("the hangar's delete wrote %d change records for the refit it took, want 1", got)
		}
		if !stands(t, rowByID("Ships", shipStubbornMuleID)) {
			t.Error("a ship of Anvil Dock One is gone with another hangar")
		}
	})

	t.Run("a wing's squadron, with its membership and pass, is asked about, then taken", func(t *testing.T) {
		t.Parallel()

		forge, flight, visit := id("4d0"), id("5d0"), id("6d1")
		write(t, wing(forge), squadron(flight, forge), membership(flight), pass(visit, hangarAnvilDockID, "SquadronId", flight))

		askedThenTaken(t, batch, opPath(anvil, "wings/"+forge), "Delete this wing and everything under it: 1 squadron, 1 hangar pass, 1 squadron membership. This can't be undone.",
			[]takenRow{rowByID("Wings", forge), rowByID("Squadrons", flight), membershipRow(flight), rowByID("HangarPasses", visit)})
	})
}

// TestDeleteCascade_byAnotherColumnAndUnderItsOwnKind pins the delete over the two links
// that are no key to another table's key, on the served routes. Two calls are written
// with their relays. The first call has a relay, an answer to it, and an answer to that
// answer which names the second call's case number, so it is under the first call
// through the relay it answers alone. The second call has a relay of its own, and a
// thread of three under it. A third call has a thread of 25 relays, each answering the one
// before it, deeper than the database joins in one statement: its question counts every
// one of them, and its delete takes them all.
func TestDeleteCascade_byAnotherColumnAndUnderItsOwnKind(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	// The deletes that go ahead bring the token their question gave, in its session.
	keepSession(t, defaultUser)

	db, err := prepareDatabase(ctx, t, migrationsSource, demoSeedSource)
	if err != nil {
		t.Fatal(err)
	}

	const (
		firstCall    = "dc000000-0000-4000-8000-0000000c0001"
		secondCall   = "dc000000-0000-4000-8000-0000000c0002"
		firstNumber  = "DC-CASCADE-0001"
		secondNumber = "DC-CASCADE-0002"
		heard        = "dc100000-0000-4000-8000-0000000c0001" // on the first call
		turning      = "dc100000-0000-4000-8000-0000000c0002" // answers heard
		crossed      = "dc100000-0000-4000-8000-0000000c0003" // answers turning, names the second call
		apart        = "dc100000-0000-4000-8000-0000000c0004" // on the second call
		threadTop    = "dc100000-0000-4000-8000-0000000c0005" // on the second call
		threadMiddle = "dc100000-0000-4000-8000-0000000c0006" // answers threadTop
		threadEnd    = "dc100000-0000-4000-8000-0000000c0007" // answers threadMiddle
		deepCall     = "dc000000-0000-4000-8000-0000000c0003"
		deepNumber   = "DC-CASCADE-0003"
		batch        = "/console/api/resources"
	)
	// deepThread is the third call's thread, each relay answering the one before it.
	deepThread := make([]string, 0, 25)
	for i := range 25 {
		deepThread = append(deepThread, fmt.Sprintf("dc200000-0000-4000-8000-%012x", i+1))
	}
	call := func(id, number string) *spanner.Mutation {
		return spanner.InsertMap("DistressCalls", map[string]any{
			"Id": id, "SectorId": anvil, "Summary": "Cascade call " + number, "Severity": 1, "CaseNumber": number,
			"FiledByUserId": "f0000000-0000-4000-8000-000000000004", "FiledByName": "marshal",
		})
	}
	relay := func(id, number, answers string) *spanner.Mutation {
		row := map[string]any{
			"Id": id, "SectorId": anvil, "CaseNumber": number, "Message": "Relay " + id, "RelayedByName": "marshal", "RelayedAt": spanner.CommitTimestamp,
		}
		if answers != "" {
			row["AnswersRelayId"] = answers
		}

		return spanner.InsertMap("DistressRelays", row)
	}
	mutations := []*spanner.Mutation{
		call(firstCall, firstNumber),
		call(secondCall, secondNumber),
		relay(heard, firstNumber, ""),
		relay(turning, firstNumber, heard),
		relay(crossed, secondNumber, turning),
		relay(apart, secondNumber, ""),
		relay(threadTop, secondNumber, ""),
		relay(threadMiddle, secondNumber, threadTop),
		relay(threadEnd, secondNumber, threadMiddle),
		call(deepCall, deepNumber),
	}
	for i, id := range deepThread {
		answers := ""
		if i > 0 {
			answers = deepThread[i-1]
		}
		mutations = append(mutations, relay(id, deepNumber, answers))
	}
	if _, err := db.Apply(ctx, mutations); err != nil {
		t.Fatalf("spanner.Apply(): %v", err)
	}

	stands := func(t *testing.T, table, id string) bool {
		t.Helper()

		_, err := db.Single().ReadRow(ctx, table, spanner.Key{id}, []string{"Id"})
		if err != nil && spanner.ErrCode(err) != codes.NotFound {
			t.Fatalf("ReadRow(%s, %s): %v", table, id, err)
		}

		return err == nil
	}
	relaysStanding := func(t *testing.T) []string {
		t.Helper()

		var standing []string
		for _, id := range []string{heard, turning, crossed, apart, threadTop, threadMiddle, threadEnd} {
			if stands(t, "DistressRelays", id) {
				standing = append(standing, id)
			}
		}

		return standing
	}
	remove := func(path string, everythingUnder bool) string {
		if !everythingUnder {
			return fmt.Sprintf(`[{"op":"remove","path":%q}]`, opPath(anvil, path))
		}

		return fmt.Sprintf(`[{"op":"remove","path":%q,"value":{"everythingUnder":true}}]`, opPath(anvil, path))
	}
	h := newTestApp(db, grants{
		accesstypes.Delete: {"DistressCalls", "DistressRelays"},
		accesstypes.List:   {"DistressRelays"},
	})

	// goesAhead asks the delete of the record at path its question, then sends it on the
	// question's token.
	goesAhead := func(t *testing.T, path string) (int, []byte) {
		t.Helper()

		status, body := doRequest(t, h, http.MethodPatch, batch, remove(path, false))
		assertStatus(t, status, http.StatusPreconditionRequired, body)
		ops := fmt.Sprintf(`[{"op":"remove","path":%q,"value":{"everythingUnder":true,"token":%q}}]`, opPath(anvil, path), deleteToken(t, body))

		return doRequest(t, h, http.MethodPatch, batch, ops)
	}

	t.Run("a trusted delete of a call that is not there does nothing", func(t *testing.T) {
		missing := impulse.Must(impulse.UUIDFromString("dc000000-0000-4000-8000-0000000c0009"))
		client := resource.NewSpannerClient(db.Client)
		for name, patch := range map[string]*resources.DistressCallDeletePatch{
			"plain":                    resources.NewDistressCallDeletePatch(missing),
			"with everything under it": resources.NewDistressCallDeletePatch(missing).WithEverythingUnder(),
		} {
			if err := patch.Apply(ctx, client); err != nil {
				t.Errorf("the delete %s: DistressCallDeletePatch.Apply() error = %v, want none", name, err)
			}
		}
		if got := relaysStanding(t); len(got) != 7 || !stands(t, "DistressCalls", firstCall) || !stands(t, "DistressCalls", secondCall) {
			t.Errorf("after the deletes %d of the 7 relays stand, the first call stands: %v, the second: %v; want everything", len(got), stands(t, "DistressCalls", firstCall), stands(t, "DistressCalls", secondCall))
		}
	})

	t.Run("a call's question counts its relays by the case number and the answers under them", func(t *testing.T) {
		status, body := doRequest(t, h, http.MethodPatch, batch, remove("distress-calls/"+firstCall, false))
		assertStatus(t, status, http.StatusPreconditionRequired, body)
		if got, want := refusalMessage(t, body), "Delete this distress call and everything under it: 3 distress relays. This can't be undone."; got != want {
			t.Errorf("message = %q, want %q", got, want)
		}
		if got := relaysStanding(t); len(got) != 7 || !stands(t, "DistressCalls", firstCall) {
			t.Errorf("after the question %d of the 7 relays stand, and the call stands: %v; want everything", len(got), stands(t, "DistressCalls", firstCall))
		}
	})

	t.Run("a relay's question counts the answers under it, however deep", func(t *testing.T) {
		status, body := doRequest(t, h, http.MethodPatch, batch, remove("distress-relays/"+threadTop, false))
		assertStatus(t, status, http.StatusPreconditionRequired, body)
		if got, want := refusalMessage(t, body), "Delete this distress relay and everything under it: 2 distress relays. This can't be undone."; got != want {
			t.Errorf("message = %q, want %q", got, want)
		}
		if got := relaysStanding(t); len(got) != 7 {
			t.Errorf("after the question %d of the 7 relays stand, want all", len(got))
		}
	})

	t.Run("a relay goes with its answers once the request says so", func(t *testing.T) {
		status, body := goesAhead(t, "distress-relays/"+threadTop)
		assertStatus(t, status, http.StatusOK, body)
		if got, want := relaysStanding(t), []string{heard, turning, crossed, apart}; !slices.Equal(got, want) {
			t.Errorf("the relays standing are %v, want %v", got, want)
		}
	})

	t.Run("a relay with no answer is deleted without a question", func(t *testing.T) {
		status, body := doRequest(t, h, http.MethodPatch, batch, remove("distress-relays/"+apart, false))
		assertStatus(t, status, http.StatusOK, body)
		if got, want := relaysStanding(t), []string{heard, turning, crossed}; !slices.Equal(got, want) {
			t.Errorf("the relays standing are %v, want %v", got, want)
		}
	})

	t.Run("a call goes with its relays and the answer that names another call", func(t *testing.T) {
		status, body := goesAhead(t, "distress-calls/"+firstCall)
		assertStatus(t, status, http.StatusOK, body)
		if got := relaysStanding(t); len(got) != 0 {
			t.Errorf("the relays standing are %v, want none", got)
		}
		if stands(t, "DistressCalls", firstCall) {
			t.Error("the first call stands after its delete")
		}
		if !stands(t, "DistressCalls", secondCall) {
			t.Error("the second call is gone, though only an answer that named it was under the first")
		}
	})

	t.Run("a call with no relay left is deleted without a question", func(t *testing.T) {
		status, body := doRequest(t, h, http.MethodPatch, batch, remove("distress-calls/"+secondCall, false))
		assertStatus(t, status, http.StatusOK, body)
		if stands(t, "DistressCalls", secondCall) {
			t.Error("the second call stands after its delete")
		}
	})

	deepStanding := func(t *testing.T) int {
		t.Helper()

		standing := 0
		for _, id := range deepThread {
			if stands(t, "DistressRelays", id) {
				standing++
			}
		}

		return standing
	}

	t.Run("a call's question counts a thread of relays deeper than one statement joins", func(t *testing.T) {
		status, body := doRequest(t, h, http.MethodPatch, batch, remove("distress-calls/"+deepCall, false))
		assertStatus(t, status, http.StatusPreconditionRequired, body)
		if got, want := refusalMessage(t, body), "Delete this distress call and everything under it: 25 distress relays. This can't be undone."; got != want {
			t.Errorf("message = %q, want %q", got, want)
		}
		if got := deepStanding(t); got != 25 || !stands(t, "DistressCalls", deepCall) {
			t.Errorf("after the question %d of the 25 relays stand, and the call stands: %v; want everything", got, stands(t, "DistressCalls", deepCall))
		}
	})

	t.Run("a call goes with a thread of relays deeper than one statement joins", func(t *testing.T) {
		status, body := goesAhead(t, "distress-calls/"+deepCall)
		assertStatus(t, status, http.StatusOK, body)
		if got := deepStanding(t); got != 0 {
			t.Errorf("%d of the 25 relays stand after the call's delete, want none", got)
		}
		if stands(t, "DistressCalls", deepCall) {
			t.Error("the third call stands after its delete")
		}
	})
}

// TestDistressRelay_namesACallOfItsOwnSector pins that a relay names a call of its own
// sector: its foreign key to its call is its sector and the case number. A relay added in
// Bastion with the number of Anvil's call is refused as one with a number no call has, in
// the same status and the same sentence, and no relay is written; the same relay added in
// Anvil is created. Its writer lists the calls, and Bastion's list shows no call with either
// number, so both are answered 404 before the commit, where the key would refuse them too.
func TestDistressRelay_namesACallOfItsOwnSector(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	db, err := prepareDatabase(ctx, t, migrationsSource, demoSeedSource)
	if err != nil {
		t.Fatal(err)
	}

	const (
		anvilCall   = "dc000000-0000-4000-8000-0000000c0021"
		anvilNumber = "DC-OWN-SECTOR-0001"
		batch       = "/console/api/resources"
	)
	if _, err := db.Apply(ctx, []*spanner.Mutation{spanner.InsertMap("DistressCalls", map[string]any{
		"Id": anvilCall, "SectorId": anvil, "Summary": "Own sector call", "Severity": 1, "CaseNumber": anvilNumber,
		"FiledByUserId": "f0000000-0000-4000-8000-000000000004", "FiledByName": "marshal",
	})}); err != nil {
		t.Fatalf("spanner.Apply(): %v", err)
	}
	h := newTestApp(db, grants{accesstypes.Create: withFields("DistressRelays", "caseNumber", "message"), accesstypes.List: {"DistressCalls"}})
	add := func(t *testing.T, sector, number string) (int, []byte) {
		t.Helper()

		return doRequest(t, h, http.MethodPatch, batch,
			fmt.Sprintf(`[{"op":"add","path":%q,"value":{"caseNumber":%q,"message":"Heard on the outer lane"}}]`, opPath(sector, "distress-relays"), number))
	}
	relays := func(t *testing.T, sector, number string) int64 {
		t.Helper()

		var n int64
		err := db.Single().Query(ctx, spanner.Statement{
			SQL:    "SELECT COUNT(*) FROM DistressRelays WHERE SectorId = @sector AND CaseNumber = @number",
			Params: map[string]any{"sector": sector, "number": number},
		}).Do(func(row *spanner.Row) error { return row.Column(0, &n) })
		if err != nil {
			t.Fatalf("counting the relays of %s with %s: %v", sector, number, err)
		}

		return n
	}

	for _, tt := range []struct{ name, number string }{
		{name: "a number no call has", number: "DC-OWN-SECTOR-NONE"},
		{name: "the number of another sector's call", number: anvilNumber},
	} {
		t.Run("a relay added in Bastion with "+tt.name+" is refused, and nothing is written", func(t *testing.T) {
			t.Parallel()

			status, body := add(t, bastion, tt.number)
			assertStatus(t, status, http.StatusNotFound, body)
			if got, want := refusalMessage(t, body), "A record this one refers to does not exist."; got != want {
				t.Errorf("message = %q, want %q", got, want)
			}
			if n := relays(t, bastion, tt.number); n != 0 {
				t.Errorf("Bastion holds %d relay(s) with %s, want none", n, tt.number)
			}
		})
	}

	t.Run("the same relay added in Anvil is created", func(t *testing.T) {
		t.Parallel()

		status, body := add(t, anvil, anvilNumber)
		assertStatus(t, status, http.StatusOK, body)
		if n := relays(t, anvil, anvilNumber); n != 1 {
			t.Errorf("Anvil holds %d relay(s) with %s, want the one created", n, anvilNumber)
		}
	})
}

// TestDistressCall_caseNumberIsUniqueWithinASector pins the index that holds a case
// number, written as rows: the number of a call of Anvil is taken by a call of Bastion,
// and a second call of Anvil with it is refused.
func TestDistressCall_caseNumberIsUniqueWithinASector(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	db, err := prepareDatabase(ctx, t, migrationsSource, demoSeedSource)
	if err != nil {
		t.Fatal(err)
	}

	const number = "DC-UNIQUE-0001"
	call := func(id, sector string) []*spanner.Mutation {
		return []*spanner.Mutation{spanner.InsertMap("DistressCalls", map[string]any{
			"Id": id, "SectorId": sector, "Summary": "Unique call " + id, "Severity": 1, "CaseNumber": number,
			"FiledByUserId": "f0000000-0000-4000-8000-000000000004", "FiledByName": "marshal",
		})}
	}
	if _, err := db.Apply(ctx, call("dc000000-0000-4000-8000-0000000c0031", anvil)); err != nil {
		t.Fatalf("writing Anvil's call: %v", err)
	}
	if _, err := db.Apply(ctx, call("dc000000-0000-4000-8000-0000000c0032", bastion)); err != nil {
		t.Errorf("writing a call of Bastion with the number of Anvil's: %v, want it written", err)
	}
	if _, err := db.Apply(ctx, call("dc000000-0000-4000-8000-0000000c0033", anvil)); spanner.ErrCode(err) != codes.AlreadyExists {
		t.Errorf("writing a second call of Anvil with the number: err = %v, want the unique index to refuse it", err)
	}
}

// TestDeleteCascade_rounds pins a delete whose rows cost more than one commit takes. A
// sortie costs three mutations, its delete and an entry in each of two indexes, so a
// mission with 26,700 sorties is over the 80,000 a commit takes. Asked first, the delete
// counts them all. Confirmed, each request takes what fits and answers 202 with the rows
// it deleted alone, and the sorties that stand are the question's count less those
// deleted so far; the mission stands after every round but the last, and the same
// request sent again goes on until it answers 200 with nothing left. The mission keeps a
// change record, and its delete writes one; its sorties keep none.
//
// The test runs on its own, never in parallel: the first round's save, of about 80,000
// mutations, holds up the reads of every database of the emulator the package's tests
// share, and a read of another test held up behind it can pass its deadline.
func TestDeleteCascade_rounds(t *testing.T) {
	ctx := t.Context()
	// Every round brings the token the question gave, in its session.
	keepSession(t, defaultUser)

	db, err := prepareDatabase(ctx, t, migrationsSource, demoSeedSource)
	if err != nil {
		t.Fatal(err)
	}

	const (
		mission = "8c000000-0000-4000-8000-0000000c0701"
		sorties = 26_700
		part    = 1_000
	)
	if _, err := db.Apply(ctx, []*spanner.Mutation{spanner.InsertMap("Missions", map[string]any{
		"Id": mission, "SectorId": anvil, "ClientId": clientHalvardID, "KindId": "courier", "Title": "The long haul", "Hazard": 1,
		"Fee": *big.NewRat(100, 1), "Deadline": time.Now().Add(24 * time.Hour), "BookedByUserId": "f0000000-0000-4000-8000-000000000004", "BookedByName": "marshal", "StatusId": "stood_down",
	})}); err != nil {
		t.Fatalf("spanner.Apply(Missions): %v", err)
	}
	launched := time.Now().UTC()
	for first := 0; first < sorties; first += part {
		rows := make([]*spanner.Mutation, 0, part)
		for n := first; n < min(first+part, sorties); n++ {
			rows = append(rows, spanner.Insert("Sorties",
				[]string{"Id", "MissionId", "ShipId", "PilotUserId", "LaunchedAt"},
				[]any{fmt.Sprintf("9c700000-0000-4000-8000-%012d", n), mission, shipLanternID, "f0000000-0000-4000-8000-000000000004", launched}))
		}
		if _, err := db.Apply(ctx, rows); err != nil {
			t.Fatalf("spanner.Apply(Sorties from %d): %v", first, err)
		}
	}
	count := func(sql string) int64 {
		t.Helper()

		var n int64
		if err := db.Single().Query(ctx, spanner.Statement{SQL: sql, Params: map[string]any{"mission": mission}}).Do(func(row *spanner.Row) error {
			return row.Columns(&n)
		}); err != nil {
			t.Fatalf("counting %q: %v", sql, err)
		}

		return n
	}
	sortiesLeft := func() int64 {
		t.Helper()

		return count("SELECT COUNT(*) FROM Sorties WHERE MissionId = @mission")
	}
	missionStands := func() bool {
		t.Helper()

		return count("SELECT COUNT(*) FROM Missions WHERE Id = @mission") == 1
	}

	h := newTestApp(db, grants{
		accesstypes.Delete: {missionsResource, "Sorties", "SortieExpenses", "MissionDocuments"},
		accesstypes.List:   {"Sorties"},
	})
	remove := func(value string) (int, []byte) {
		ops := fmt.Sprintf(`[{"op":"remove","path":%q%s}]`, opPath(anvil, "missions/"+mission), value)

		return doRequest(t, h, http.MethodPatch, "/console/api/resources", ops)
	}

	status, body := remove("")
	assertStatus(t, status, http.StatusPreconditionRequired, body)
	if got, want := refusalMessage(t, body), "Delete this mission and everything under it: 26,700 sorties. This can't be undone."; got != want {
		t.Errorf("the question = %q, want %q", got, want)
	}
	var asked struct {
		Records int64 `json:"records"`
	}
	if err := json.Unmarshal(body, &asked); err != nil {
		t.Fatalf("the question answered %s: %v", body, err)
	}
	if asked.Records != sorties {
		t.Errorf("the question counted %d records, want the %d sorties", asked.Records, sorties)
	}
	token := deleteToken(t, body)
	if n := sortiesLeft(); n != sorties || !missionStands() {
		t.Fatalf("after the question %d of the %d sorties stand and the mission stands = %v, want all of them and the mission", n, sorties, missionStands())
	}

	events := countChangeEvents(ctx, t, db, "Missions")
	const most = 10
	rounds := 0
	var deleted int64
	for ; rounds < most; rounds++ {
		status, body = remove(fmt.Sprintf(`,"value":{"everythingUnder":true,"token":%q}`, token))
		if status != http.StatusAccepted {
			break
		}
		var left map[string]int64
		if err := json.Unmarshal(body, &left); err != nil {
			t.Fatalf("round %d answered %s: %v", rounds+1, body, err)
		}
		if _, ok := left["deleted"]; !ok || len(left) != 1 || left["deleted"] <= 0 {
			t.Errorf("round %d answered %s, want the rows it deleted alone", rounds+1, body)
		}
		deleted += left["deleted"]
		if n := sortiesLeft(); n != sorties-deleted {
			t.Errorf("after round %d %d sorties stand, want the question's %d less the %d deleted", rounds+1, n, sorties, deleted)
		}
		if !missionStands() {
			t.Errorf("the mission is gone after round %d, which left rows under it", rounds+1)
		}
	}
	assertStatus(t, status, http.StatusOK, body)
	if rounds == 0 {
		t.Error("the delete went in one request, want more than one round")
	}
	if n := sortiesLeft(); n != 0 {
		t.Errorf("%d sorties stand after the last round, want none", n)
	}
	if missionStands() {
		t.Error("the mission stands after the last round, want it deleted")
	}
	if got := countChangeEvents(ctx, t, db, "Missions") - events; got != 1 {
		t.Errorf("the mission's delete wrote %d change record(s), want 1", got)
	}
}
