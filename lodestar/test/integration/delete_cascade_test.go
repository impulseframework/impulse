// Demonstrates: @deleteCascade, @deleteCascade.own-kind, @deleteCascade.other-columns.
package integration

// This suite pins what a record declares its delete takes (@deleteCascade). A mission
// names its sorties, their expenses and its documents as going with it; a client names
// its contacts, and a mission or a consignment as refusing its delete. The generator
// holds each list to the schema and writes the description the first test reads.
//
// The rest is the delete itself on the served routes. A delete of a mission with rows
// under it deletes nothing unless the request says it takes them: it is answered 428
// with the question a person is asked first, which counts each kind they may see. The
// same delete saying so (the remove operation's value {"everythingUnder": true})
// deletes the rows in code, deepest first and the mission last, in the one transaction:
// the mission's change record is written and a document's stored file is released. Every
// row is checked first as its own delete would check it, so one row the person may not
// delete, or one row its rule refuses, stops the whole delete with nothing gone. A
// client with a mission is refused whatever the request says, and a record whose rows
// cost more than one commit takes is refused too. The deletes are the Spanner write
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
	"fmt"
	"math/big"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"google.golang.org/grpc/codes"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/lodestar/pkg/resources"
	"github.com/impulseframework/impulse/lodestar/pkg/router"
	"github.com/impulseframework/impulse/resource"
)

// TestDeleteCascade_description reads the descriptions the generator wrote for the two
// records that declare one: the kinds taken in delete order, deepest first, with each
// kind's way up to the record, and the kinds that refuse the delete.
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
	// An expense is on a sortie, so it is deleted before the sortie it is on.
	if got, want := kinds(mission.Taken), []accesstypes.Resource{"SortieExpenses", "MissionDocuments", "Sorties"}; !slices.Equal(got, want) {
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
	if got := mission.Taken[1].Cost.Files; got != 1 {
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
	const batch = "/console/api/resources"
	under := []accesstypes.Resource{"Sorties", "SortieExpenses", "MissionDocuments"}
	everything := grants{accesstypes.Delete: append([]accesstypes.Resource{missionsResource}, under...), accesstypes.List: under}

	t.Run("a delete that does not say it takes the rows is asked the question, and nothing is gone", func(t *testing.T) {
		m := writeCascadeMission(t, db, stores, 1, "supplies")

		status, body := doRequest(t, as(everything), http.MethodPatch, batch, remove(m, false))
		assertStatus(t, status, http.StatusPreconditionRequired, body)
		if got, want := refusalMessage(t, body), "Delete this mission and everything under it: 2 sorties, 1 mission document, 1 sortie expense. This can't be undone."; got != want {
			t.Errorf("message = %q, want %q", got, want)
		}
		allStand(t, m)

		// The same delete, saying so, takes them all in the one transaction.
		before := countChangeEvents(ctx, t, db, "Missions")
		status, body = doRequest(t, as(everything), http.MethodPatch, batch, remove(m, true))
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

	t.Run("a request that changes a row under the record and deletes the record is refused, and nothing is gone", func(t *testing.T) {
		m := writeCascadeMission(t, db, stores, 8, "penalty")
		// This person may update a sortie and may not delete one, nor an expense.
		updatesSorties := grants{
			accesstypes.Delete: {missionsResource, "MissionDocuments"},
			accesstypes.Update: withFields("Sorties", "debrief"),
			accesstypes.List:   under,
		}
		debrief := func(sortie string) string {
			return fmt.Sprintf(`{"op":"patch","path":%q,"value":{"debrief":"Back in one piece"}}`, opPath(anvil, "sorties/"+sortie))
		}
		// A read inside the transaction shows each sortie as it was before the request,
		// so the delete cannot tell what it would take: it is refused before any row is
		// checked, and neither the sorties nor the penalty on one of them go with the
		// mission unchecked.
		ops := fmt.Sprintf(`[%s,%s,{"op":"remove","path":%q,"value":{"everythingUnder":true}}]`, debrief(m.sortieA), debrief(m.sortieB), opPath(anvil, "missions/"+m.mission))

		status, body := doRequest(t, as(updatesSorties), http.MethodPatch, batch, ops)
		assertStatus(t, status, http.StatusConflict, body)
		if got, want := refusalMessage(t, body), "This request also changes a record under this mission. Send the change and the delete separately."; got != want {
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

		status, body = doRequest(t, h, http.MethodPatch, clients, fmt.Sprintf(`[{"op":"remove","path":"/%s","value":{"everythingUnder":true}}]`, loneHarbor))
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

// TestDeleteCascade_byAnotherColumnAndUnderItsOwnKind pins the delete over the two links
// that are no key to another table's key, on the served routes. Two calls are written
// with their relays. The first call has a relay, an answer to it, and an answer to that
// answer which names the second call's case number, so it is under the first call
// through the relay it answers alone. The second call has a relay of its own, and a
// thread of three under it.
func TestDeleteCascade_byAnotherColumnAndUnderItsOwnKind(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

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
		batch        = "/console/api/resources"
	)
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
	if _, err := db.Apply(ctx, []*spanner.Mutation{
		call(firstCall, firstNumber),
		call(secondCall, secondNumber),
		relay(heard, firstNumber, ""),
		relay(turning, firstNumber, heard),
		relay(crossed, secondNumber, turning),
		relay(apart, secondNumber, ""),
		relay(threadTop, secondNumber, ""),
		relay(threadMiddle, secondNumber, threadTop),
		relay(threadEnd, secondNumber, threadMiddle),
	}); err != nil {
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
		status, body := doRequest(t, h, http.MethodPatch, batch, remove("distress-relays/"+threadTop, true))
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
		status, body := doRequest(t, h, http.MethodPatch, batch, remove("distress-calls/"+firstCall, true))
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
}

// TestDistressRelay_namesACallOfItsOwnSector pins that a relay names a call of its own
// sector: its foreign key to its call is its sector and the case number. A relay added in
// Bastion with the number of Anvil's call is refused as one with a number no call has, in
// the same status and the same sentence, and no relay is written; the same relay added in
// Anvil is created.
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
	h := newTestApp(db, grants{accesstypes.Create: withFields("DistressRelays", "caseNumber", "message")})
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
			assertStatus(t, status, http.StatusConflict, body)
			if got, want := refusalMessage(t, body), "This distress relay refers to a record that does not exist, or holds a value that is too long for its field."; got != want {
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

// TestDeleteCascade_tooManyRowsAtOnce pins the refusal of a delete whose rows cost more
// than one commit takes. A sortie costs three mutations, its delete and an entry in each
// of two indexes, so a mission with 26,700 sorties is over the 80,000 a commit takes: the
// delete is refused in a sentence that says so, whatever the request says, and nothing
// is gone.
func TestDeleteCascade_tooManyRowsAtOnce(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

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

	h := newTestApp(db, grants{
		accesstypes.Delete: {missionsResource, "Sorties", "SortieExpenses", "MissionDocuments"},
		accesstypes.List:   {"Sorties"},
	})
	for _, value := range []string{"", `,"value":{"everythingUnder":true}`} {
		ops := fmt.Sprintf(`[{"op":"remove","path":%q%s}]`, opPath(anvil, "missions/"+mission), value)
		status, body := doRequest(t, h, http.MethodPatch, "/console/api/resources", ops)
		assertStatus(t, status, http.StatusConflict, body)
		if got, want := refusalMessage(t, body), "This mission has too many records under it to delete at once. Delete some of them first."; got != want {
			t.Errorf("message = %q, want %q", got, want)
		}
	}

	var left int64
	if err := db.Single().Query(ctx, spanner.Statement{SQL: "SELECT COUNT(*) FROM Sorties WHERE MissionId = @mission", Params: map[string]any{"mission": mission}}).Do(func(row *spanner.Row) error {
		return row.Columns(&left)
	}); err != nil {
		t.Fatalf("counting the mission's sorties: %v", err)
	}
	if left != sorties {
		t.Errorf("%d of the mission's %d sorties stand after its refused delete", left, sorties)
	}
}
