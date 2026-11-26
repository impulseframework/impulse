// Demonstrates: change-history, change-tracking, event-source.
package integration

// This suite pins a record's history as the generated route serves it, GET <the read
// route>/changes, over the emulator and the application's own roles: a mission's change
// records newest first, a page at a time, narrowed to one field, with only the fields the
// reader can see on the mission as it stands, and a deleted mission's history to a
// reader whose grant carries no condition.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	initiator "github.com/impulseframework/impulse/db"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
)

// changeHistoryPage is a page of a record's history as the route answers it.
type changeHistoryPage struct {
	Changes []changeHistoryRecord `json:"changes"`
	Next    string                `json:"next"`
}

// changeHistoryRecord is one change record of a page.
type changeHistoryRecord struct {
	At        time.Time            `json:"at"`
	Sequence  int64                `json:"sequence"`
	Operation string               `json:"operation"`
	Source    string               `json:"source"`
	Fields    []changeHistoryField `json:"fields"`
}

// changeHistoryField is one changed field of a change record.
type changeHistoryField struct {
	Field   string          `json:"field"`
	Old     json.RawMessage `json:"old"`
	New     json.RawMessage `json:"new"`
	OldName string          `json:"oldName"`
	NewName string          `json:"newName"`
	Secret  bool            `json:"secret"`
}

// names answers the record's fields by name.
func (r *changeHistoryRecord) names() []string {
	names := make([]string, 0, len(r.Fields))
	for _, field := range r.Fields {
		names = append(names, field.Field)
	}

	return names
}

// field answers the record's change of one field, and whether it has one.
func (r *changeHistoryRecord) field(name string) (changeHistoryField, bool) {
	for _, field := range r.Fields {
		if field.Field == name {
			return field, true
		}
	}

	return changeHistoryField{}, false
}

// jsonText renders a value of a change record as text: a string as it is, a number in
// its digits, JSON null as null.
func jsonText(t *testing.T, raw json.RawMessage) string {
	t.Helper()

	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("json.Unmarshal(%s) error = %v", raw, err)
	}
	if value == nil {
		return "null"
	}

	return fmt.Sprint(value)
}

// missionHistory is the history route of one Anvil mission, with its query.
func missionHistory(missionID, query string) string {
	target := sectorPath(anvil, "missions/"+missionID+"/changes")
	if query != "" {
		target += "?" + query
	}

	return target
}

// readHistory asks for a page of a history as user and decodes it, holding the answer
// to 200.
func readHistory(t *testing.T, h http.Handler, user accesstypes.User, target string) changeHistoryPage {
	t.Helper()

	status, body := doRequestAs(t, h, user, http.MethodGet, target, "")
	assertStatus(t, status, http.StatusOK, body)
	var page changeHistoryPage
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("json.Unmarshal() error = %v: %s", err, body)
	}

	return page
}

// patchAnvil writes one consolidated operation on an Anvil record as the marshal,
// holding the answer to 200.
func patchAnvil(t *testing.T, h http.Handler, op, rest, value string) {
	t.Helper()

	operation := fmt.Sprintf(`{"op":%q,"path":%q}`, op, opPath(anvil, rest))
	if value != "" {
		operation = fmt.Sprintf(`{"op":%q,"path":%q,"value":%s}`, op, opPath(anvil, rest), value)
	}
	status, body := doRequestAs(t, h, "marshal", http.MethodPatch, "/console/api/resources", "["+operation+"]")
	assertStatus(t, status, http.StatusOK, body)
}

// createMission makes an Anvil mission as the marshal, titled title, and answers its id.
func createMission(t *testing.T, h http.Handler, db *initiator.SpannerDB, title string) string {
	t.Helper()

	patchAnvil(t, h, "add", "missions", fmt.Sprintf(`{"clientId":%q,"kindId":"rescue","title":%q,"hazard":2,"fee":100,"deadline":%q}`, clientHalvardID, title, deadline(365)))
	stmt := spanner.NewStatement("SELECT Id FROM Missions WHERE Title = @title")
	stmt.Params["title"] = title
	var id string
	if err := db.Single().Query(t.Context(), stmt).Do(func(row *spanner.Row) error {
		return row.Column(0, &id)
	}); err != nil || id == "" {
		t.Fatalf("finding the mission titled %q: %v", title, err)
	}

	return id
}

// setMissionStatus moves a mission to a status past the API, as a transition would,
// writing no change record.
func setMissionStatus(t *testing.T, db *initiator.SpannerDB, missionID, status string) {
	t.Helper()

	if _, err := db.Apply(t.Context(), []*spanner.Mutation{
		spanner.UpdateMap("Missions", map[string]any{"Id": missionID, "StatusId": status}),
	}); err != nil {
		t.Fatalf("setting the mission's status to %s: %v", status, err)
	}
}

// TestChangeHistory_newestFirst pins a mission's history to the marshal, who may read
// every field: a create and two changes are three records, newest first, each change
// under the field's name on the wire with its old and its new value, each saying what
// kind of write it was, and each made by the marshal, on the mission's own route.
func TestChangeHistory_newestFirst(t *testing.T) {
	t.Parallel()

	_, db, h := demoWorld(t)
	id := createMission(t, h, db, "History of three")
	patchAnvil(t, h, "patch", "missions/"+id, `{"title":"History of three, renamed"}`)
	patchAnvil(t, h, "patch", "missions/"+id, `{"fee":250}`)

	page := readHistory(t, h, "marshal", missionHistory(id, ""))
	if len(page.Changes) != 3 || page.Next != "" {
		t.Fatalf("the history holds %d records and next %q, want 3 and none: %+v", len(page.Changes), page.Next, page)
	}
	for i := 1; i < len(page.Changes); i++ {
		if page.Changes[i].At.After(page.Changes[i-1].At) {
			t.Errorf("record %d is newer than the one before it: the history is newest first", i)
		}
	}
	for i, record := range page.Changes {
		if record.Source != "marshal" {
			t.Errorf("record %d was made by %q, want the marshal", i, record.Source)
		}
		if want := []string{"update", "update", "create"}[i]; record.Operation != want {
			t.Errorf("record %d is a %q, want a %q", i, record.Operation, want)
		}
	}

	tests := []struct {
		record   int
		field    string
		old, new string
	}{
		{record: 0, field: "fee", old: "100", new: "250"},
		{record: 1, field: "title", old: "History of three", new: "History of three, renamed"},
		{record: 2, field: "title", old: "null", new: "History of three"},
		{record: 2, field: "kindId", old: "null", new: "rescue"},
	}
	for _, tt := range tests {
		change, ok := page.Changes[tt.record].field(tt.field)
		if !ok {
			t.Errorf("record %d lists %v, want %s among them", tt.record, page.Changes[tt.record].names(), tt.field)

			continue
		}
		if got, want := jsonText(t, change.Old)+" -> "+jsonText(t, change.New), tt.old+" -> "+tt.new; got != want {
			t.Errorf("record %d's %s = %s, want %s", tt.record, tt.field, got, want)
		}
	}
	if names := page.Changes[0].names(); len(names) != 1 {
		t.Errorf("the change of the fee lists %v, want the fee alone", names)
	}
}

// TestChangeHistory_pages pins the page: more than fifty changes are fifty records and
// next, and after=<next> answers the rest, with no record twice and none missed.
func TestChangeHistory_pages(t *testing.T) {
	t.Parallel()

	_, db, h := demoWorld(t)
	id := createMission(t, h, db, "History of many")
	const changes = 55
	for i := range changes {
		patchAnvil(t, h, "patch", "missions/"+id, fmt.Sprintf(`{"title":"Pass %d"}`, i))
	}

	first := readHistory(t, h, "marshal", missionHistory(id, ""))
	if len(first.Changes) != 50 || first.Next == "" {
		t.Fatalf("the first page holds %d records and next %q, want 50 and a next", len(first.Changes), first.Next)
	}
	rest := readHistory(t, h, "marshal", missionHistory(id, "after="+url.QueryEscape(first.Next)))
	if len(rest.Changes) != changes+1-50 || rest.Next != "" {
		t.Fatalf("the second page holds %d records and next %q, want %d and none", len(rest.Changes), rest.Next, changes+1-50)
	}

	seen := map[string]bool{}
	for i, record := range append(first.Changes, rest.Changes...) {
		key := fmt.Sprintf("%s/%d", record.At.Format(time.RFC3339Nano), record.Sequence)
		if seen[key] {
			t.Errorf("record %s is on both pages", key)
		}
		seen[key] = true
		want := "null"
		if i < changes {
			want = fmt.Sprintf("Pass %d", changes-1-i)
		}
		if title, ok := record.field("title"); i < changes && (!ok || jsonText(t, title.New) != want) {
			t.Errorf("record %d set the title to %v, want %q: a record is missing or out of order", i, record.Fields, want)
		}
	}
}

// TestChangeHistory_field pins field=: only the records that changed that field.
func TestChangeHistory_field(t *testing.T) {
	t.Parallel()

	_, db, h := demoWorld(t)
	id := createMission(t, h, db, "History of a field")
	patchAnvil(t, h, "patch", "missions/"+id, `{"notes":"Bring spares."}`)
	patchAnvil(t, h, "patch", "missions/"+id, `{"title":"History of a field, renamed"}`)
	patchAnvil(t, h, "patch", "missions/"+id, `{"notes":"Bring more spares."}`)

	for _, tt := range []struct {
		field string
		want  []string
	}{
		{field: "notes", want: []string{"Bring more spares.", "Bring spares."}},
		{field: "title", want: []string{"History of a field, renamed", "History of a field"}},
	} {
		page := readHistory(t, h, "marshal", missionHistory(id, "field="+tt.field))
		var got []string
		for _, record := range page.Changes {
			change, ok := record.field(tt.field)
			if !ok {
				t.Errorf("field=%s answers a record that lists %v", tt.field, record.names())

				continue
			}
			got = append(got, jsonText(t, change.New))
		}
		if strings.Join(got, "|") != strings.Join(tt.want, "|") {
			t.Errorf("field=%s answers %q, want %q", tt.field, got, tt.want)
		}
	}
}

// TestChangeHistory_grantLeavesFieldOut pins the quartermaster's history of a mission:
// her grant reads the mission without its notes, so the notes are in no record, and a
// record that changed the notes alone is listed with no field.
func TestChangeHistory_grantLeavesFieldOut(t *testing.T) {
	t.Parallel()

	_, db, h := demoWorld(t)
	id := createMission(t, h, db, "History without notes")
	patchAnvil(t, h, "patch", "missions/"+id, `{"notes":"Quiet run."}`)
	patchAnvil(t, h, "patch", "missions/"+id, `{"title":"History without notes, renamed"}`)

	page := readHistory(t, h, "quartermaster", missionHistory(id, ""))
	if len(page.Changes) != 3 {
		t.Fatalf("the quartermaster reads %d records, want 3", len(page.Changes))
	}
	for i, record := range page.Changes {
		if _, ok := record.field("notes"); ok {
			t.Errorf("record %d lists the notes to a grant that leaves them out", i)
		}
	}
	if names := page.Changes[0].names(); len(names) != 1 || names[0] != "title" {
		t.Errorf("the change of the title lists %v, want the title", names)
	}
	if names := page.Changes[1].names(); len(names) != 0 {
		t.Errorf("the change of the notes alone lists %v, want no field: it is listed, with when and by whom", names)
	}
	status, body := doRequestAs(t, h, "quartermaster", http.MethodGet, missionHistory(id, "field=notes"), "")
	assertStatus(t, status, http.StatusForbidden, body)
}

// TestChangeHistory_maskedCell pins the archivist's history of a mission: her grant shows
// a mission's fee once it is completed, so while it has failed the fee is in no record
// and asking for its changes is refused, and once it is completed the same request shows
// the fee in every record that changed it, the earlier ones too.
func TestChangeHistory_maskedCell(t *testing.T) {
	t.Parallel()

	_, db, h := demoWorld(t)
	// The grant this suite proves, pinned to the roles file (conditions-proven).
	provesGrant(t, crew.Roles(), "Archivist", "Read", "Missions", "state = 'completed'")

	id := createMission(t, h, db, "History of a fee")
	patchAnvil(t, h, "patch", "missions/"+id, `{"fee":900}`)
	setMissionStatus(t, db, id, "failed")

	failed := readHistory(t, h, "archivist", missionHistory(id, ""))
	if len(failed.Changes) != 2 {
		t.Fatalf("the archivist reads %d records of the failed mission, want 2", len(failed.Changes))
	}
	for i, record := range failed.Changes {
		if _, ok := record.field("fee"); ok {
			t.Errorf("record %d lists the fee while the fee's cell is masked", i)
		}
	}
	status, body := doRequestAs(t, h, "archivist", http.MethodGet, missionHistory(id, "field=fee"), "")
	assertStatus(t, status, http.StatusForbidden, body)

	setMissionStatus(t, db, id, "completed")
	completed := readHistory(t, h, "archivist", missionHistory(id, ""))
	if len(completed.Changes) != 2 {
		t.Fatalf("the archivist reads %d records of the completed mission, want 2", len(completed.Changes))
	}
	for i, want := range []string{"100 -> 900", "null -> 100"} {
		change, ok := completed.Changes[i].field("fee")
		if !ok {
			t.Errorf("record %d lists %v once the mission is completed, want the fee", i, completed.Changes[i].names())

			continue
		}
		if got := jsonText(t, change.Old) + " -> " + jsonText(t, change.New); got != want {
			t.Errorf("record %d's fee = %s, want %s", i, got, want)
		}
	}
	readHistory(t, h, "archivist", missionHistory(id, "field=fee"))
}

// TestChangeHistory_deleted pins a deleted mission's history: the marshal, whose Read on
// missions carries no condition, reads it with the delete's record first, a delete, then
// the update and the create; the cadet and the archivist, whose Read carries one, are
// answered as for a mission that is not there.
func TestChangeHistory_deleted(t *testing.T) {
	t.Parallel()

	_, db, h := demoWorld(t)
	id := createMission(t, h, db, "History of a delete")
	patchAnvil(t, h, "patch", "missions/"+id, `{"hazard":1}`)
	patchAnvil(t, h, "remove", "missions/"+id, "")

	page := readHistory(t, h, "marshal", missionHistory(id, ""))
	if len(page.Changes) != 3 {
		t.Fatalf("the marshal reads %d records of the deleted mission, want 3", len(page.Changes))
	}
	for i, want := range []string{"delete", "update", "create"} {
		if page.Changes[i].Operation != want {
			t.Errorf("record %d of the deleted mission is a %q, want a %q", i, page.Changes[i].Operation, want)
		}
	}
	title, ok := page.Changes[0].field("title")
	if !ok || jsonText(t, title.Old) != "History of a delete" || jsonText(t, title.New) != "null" {
		t.Errorf("the delete's record lists %v, want the title it took, and no new value", page.Changes[0].Fields)
	}

	_, never := doRequestAs(t, h, "cadet", http.MethodGet, missionHistory("80000000-0000-4000-8000-0000000000ff", ""), "")
	for _, user := range []accesstypes.User{"cadet", "archivist"} {
		status, body := doRequestAs(t, h, user, http.MethodGet, missionHistory(id, ""), "")
		assertStatus(t, status, http.StatusNotFound, body)
		if !bytes.Equal(body, never) {
			t.Errorf("%s is told %s of the deleted mission, want what a mission that never was says: %s", user, body, never)
		}
	}
}

// TestChangeHistory_impersonated pins the sentence of a change a session acting as
// someone else made: the governor, acting as the role Dispatcher, extends a mission's
// deadline on the mission's own route, and the mission's history names the real person
// first, then the role, and no method.
//
// Demonstrates: change-history, event-source.
func TestChangeHistory_impersonated(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	greer := newBrowser(t, s, consoleAPI)
	greer.signIn(ctx, "governor")
	status, body := greer.impersonate(ctx, `{"kind":"role","principal":"Dispatcher","reason":"history"}`)
	assertStatus(t, status, http.StatusOK, body)
	status, body = greer.do(ctx, http.MethodPatch, "/console/api/resources",
		fmt.Appendf(nil, `[{"op":"patch","path":%q,"value":{"deadline":%q}}]`, opPath(anvil, "missions/"+missionConvoyID), deadline(27)))
	assertStatus(t, status, http.StatusOK, body)

	status, body = greer.do(ctx, http.MethodGet, missionHistory(missionConvoyID, ""), nil)
	assertStatus(t, status, http.StatusOK, body)
	var page changeHistoryPage
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("json.Unmarshal() error = %v: %s", err, body)
	}
	if len(page.Changes) == 0 {
		t.Fatalf("the convoy's history holds no record: %s", body)
	}
	if newest := page.Changes[0]; newest.Source != "governor, acting as the role Dispatcher" || newest.Operation != "update" {
		t.Errorf("the newest record is a %q by %q, want an update by %q", newest.Operation, newest.Source, "governor, acting as the role Dispatcher")
	}
}

// TestChangeHistory_otherTenant pins a mission of another sector asked for under this
// sector's route: not found, in the words of a mission that never was, though the mission
// has records under its own sector.
func TestChangeHistory_otherTenant(t *testing.T) {
	t.Parallel()

	_, _, h := demoWorld(t)
	status, body := doRequestAs(t, h, "governor", http.MethodPatch, "/console/api/resources",
		fmt.Sprintf(`[{"op":"patch","path":%q,"value":{"title":"Beacon, renamed"}}]`, opPath(bastion, "missions/"+missionBeaconID)))
	assertStatus(t, status, http.StatusOK, body)
	if page := readHistory(t, h, "governor", sectorPath(bastion, "missions/"+missionBeaconID+"/changes")); len(page.Changes) != 1 {
		t.Fatalf("Bastion's own route answers %d records of the beacon mission, want 1", len(page.Changes))
	}

	status, never := doRequestAs(t, h, "governor", http.MethodGet, missionHistory("80000000-0000-4000-8000-0000000000ff", ""), "")
	assertStatus(t, status, http.StatusNotFound, never)
	status, body = doRequestAs(t, h, "governor", http.MethodGet, missionHistory(missionBeaconID, ""), "")
	assertStatus(t, status, http.StatusNotFound, body)
	if !bytes.Equal(body, never) {
		t.Errorf("another sector's mission is answered %s, want what a mission that never was is: %s", body, never)
	}
}

// TestChangeHistory_commitTime pins a field stamped with the commit's own time: a ship
// renamed twice through its route, read by the marshal, who sees updatedAt on the ship,
// lists updatedAt on each record with the record's own time as its new value and what the
// ship held before as its old one, never the stand-in the field held until the commit.
func TestChangeHistory_commitTime(t *testing.T) {
	t.Parallel()

	ctx, db, h := demoWorld(t)
	target := sectorPath(anvil, "ships/"+shipKingfisherID+"/changes")
	patchAnvil(t, h, "patch", "ships/"+shipKingfisherID, `{"name":"Kingfisher Once"}`)
	first := readColumn[spanner.NullTime](ctx, t, db, "Ships", spanner.Key{shipKingfisherID}, "UpdatedAt")
	if !first.Valid {
		t.Fatal("the ship's UpdatedAt is not stamped by its rename")
	}
	patchAnvil(t, h, "patch", "ships/"+shipKingfisherID, `{"name":"Kingfisher Twice"}`)

	page := readHistory(t, h, "marshal", target)
	if len(page.Changes) != 2 {
		t.Fatalf("the ship's history holds %d records, want 2: %+v", len(page.Changes), page.Changes)
	}
	for i, record := range page.Changes {
		stamped, ok := record.field("updatedAt")
		if !ok {
			t.Errorf("record %d lists %v, want updatedAt among them", i, record.names())

			continue
		}
		var at time.Time
		if err := json.Unmarshal(stamped.New, &at); err != nil {
			t.Errorf("record %d: updatedAt's new value %s is no time: %v", i, stamped.New, err)

			continue
		}
		if !at.Equal(record.At) {
			t.Errorf("record %d: updatedAt's new value = %s, want the record's own time %s", i, at.Format(time.RFC3339Nano), record.At.Format(time.RFC3339Nano))
		}
	}

	// Newest first: the second rename's old value is the first rename's stamp, and the
	// first rename's is none, since the seed writes no UpdatedAt.
	if stamped, ok := page.Changes[0].field("updatedAt"); ok {
		var old time.Time
		if err := json.Unmarshal(stamped.Old, &old); err != nil || !old.Equal(first.Time) {
			t.Errorf("the second rename's updatedAt old value = %s, want the first rename's stamp %s", stamped.Old, first.Time.Format(time.RFC3339Nano))
		}
	}
	if stamped, ok := page.Changes[1].field("updatedAt"); ok {
		if got := jsonText(t, stamped.Old); got != "null" {
			t.Errorf("the first rename's updatedAt old value = %s, want null", got)
		}
	}
}

// storedChange is one field's entry of a change record as the database holds it.
type storedChange struct {
	Old     any
	New     any
	OldName string
	NewName string
}

// storedChangeSets reads past the API the change sets of one row of a table, oldest first.
func storedChangeSets(t *testing.T, db *initiator.SpannerDB, table, rowID string) []map[string]storedChange {
	t.Helper()

	stmt := spanner.NewStatement("SELECT TO_JSON_STRING(ChangeSet) FROM DataChangeEvents WHERE TableName = @table AND RowId = @row ORDER BY EventTime, Sequence")
	stmt.Params["table"] = table
	stmt.Params["row"] = rowID
	var changeSets []map[string]storedChange
	if err := db.Single().Query(t.Context(), stmt).Do(func(row *spanner.Row) error {
		var text string
		if err := row.Column(0, &text); err != nil {
			return fmt.Errorf("spanner.Row.Column(): %w", err)
		}
		var changeSet map[string]storedChange
		if err := json.Unmarshal([]byte(text), &changeSet); err != nil {
			return fmt.Errorf("json.Unmarshal(): %w", err)
		}
		changeSets = append(changeSets, changeSet)

		return nil
	}); err != nil {
		t.Fatalf("reading the change records of %s %s: %v", table, rowID, err)
	}

	return changeSets
}

// TestChangeHistory_keepsNames pins the names a mission's change records keep, as the
// database holds them: a claim through claim-mission keeps the squadron's name beside
// its id, though missions are listed across tenants and so have no name field for it,
// and the frame's own stamp of the state keeps both statuses' descriptions, read from
// the enumeration table.
func TestChangeHistory_keepsNames(t *testing.T) {
	t.Parallel()

	_, db, h := demoWorld(t)
	id := createMission(t, h, db, "Claimed, with names")
	status, body := doRequestAs(t, h, "marshal", http.MethodPost, sectorPath(anvil, "claim-mission"),
		fmt.Sprintf(`{"missionId":%q,"squadronId":%q}`, id, squadronHammerID))
	assertStatus(t, status, http.StatusOK, body)

	var squadron, state *storedChange
	for _, changeSet := range storedChangeSets(t, db, "Missions", id) {
		if change, ok := changeSet["AssignedSquadronID"]; ok {
			squadron = &change
		}
		if change, ok := changeSet["StatusID"]; ok {
			state = &change
		}
	}
	if squadron == nil || state == nil {
		t.Fatalf("the mission's change records hold the squadron's change %v and the state's %v, want both", squadron, state)
	}
	if got, want := *squadron, (storedChange{Old: nil, New: squadronHammerID, OldName: "", NewName: "Hammer"}); got != want {
		t.Errorf("the claim's AssignedSquadronID entry = %+v, want %+v", got, want)
	}
	if got, want := *state, (storedChange{Old: "open", New: "claimed", OldName: "Open", NewName: "Claimed"}); got != want {
		t.Errorf("the stamp's StatusID entry = %+v, want %+v", got, want)
	}
}

// TestChangeHistory_names pins the names a mission's history answers beside the ids of
// the rows its fields pointed at: a claim through claim-mission is answered with the
// squadron's name, and the frame's own stamp of the state, a record of its own, with the
// two statuses' words; once the squadron is renamed the same record still answers the
// name it had. The quartermaster, who reads a mission's squadron and may not list the
// squadrons' names, is answered the id alone, and the statuses' words, which every reader
// of the state reads.
//
// Demonstrates: change-history.names.
func TestChangeHistory_names(t *testing.T) {
	t.Parallel()

	_, db, h := demoWorld(t)
	id := createMission(t, h, db, "Claimed, with names on its history")
	status, body := doRequestAs(t, h, "marshal", http.MethodPost, sectorPath(anvil, "claim-mission"),
		fmt.Sprintf(`{"missionId":%q,"squadronId":%q}`, id, squadronHammerID))
	assertStatus(t, status, http.StatusOK, body)
	if _, err := db.Apply(t.Context(), []*spanner.Mutation{
		spanner.UpdateMap("Squadrons", map[string]any{"Id": squadronHammerID, "Name": "Hammer, renamed"}),
	}); err != nil {
		t.Fatalf("renaming the squadron: %v", err)
	}

	// newest answers the newest change of the squadron and of the state, as user reads
	// the history.
	newest := func(user accesstypes.User) (squadron, state changeHistoryField, separate bool) {
		t.Helper()

		page := readHistory(t, h, user, missionHistory(id, ""))
		squadronAt, stateAt := -1, -1
		for i := range page.Changes {
			if change, ok := page.Changes[i].field("assignedSquadronId"); ok && squadronAt < 0 {
				squadron, squadronAt = change, i
			}
			if change, ok := page.Changes[i].field("statusId"); ok && stateAt < 0 {
				state, stateAt = change, i
			}
		}
		if squadronAt < 0 || stateAt < 0 {
			t.Fatalf("%s reads no change of the squadron or of the state: %+v", user, page)
		}

		return squadron, state, squadronAt != stateAt
	}

	squadron, state, separate := newest("marshal")
	if got, want := jsonText(t, squadron.New)+" "+squadron.OldName+" -> "+squadron.NewName, squadronHammerID+"  -> Hammer"; got != want {
		t.Errorf("the marshal reads the claim's squadron as %q, want %q: the name as it was", got, want)
	}
	if got, want := jsonText(t, state.Old)+" -> "+jsonText(t, state.New)+": "+state.OldName+" -> "+state.NewName, "open -> claimed: Open -> Claimed"; got != want {
		t.Errorf("the marshal reads the stamp of the state as %q, want %q", got, want)
	}
	if !separate {
		t.Errorf("the claim and the stamp of the state are one record, want the stamp a record of its own")
	}

	squadron, state, _ = newest("quartermaster")
	if got, want := jsonText(t, squadron.New)+" "+squadron.OldName+" -> "+squadron.NewName, squadronHammerID+"  -> "; got != want {
		t.Errorf("the quartermaster reads the claim's squadron as %q, want %q: the id alone", got, want)
	}
	if got, want := state.OldName+" -> "+state.NewName, "Open -> Claimed"; got != want {
		t.Errorf("the quartermaster reads the stamp's statuses as %q, want %q", got, want)
	}
}

// TestChangeHistory_keepsClientNames pins the name a mission's change record keeps for
// its client, whom the mission's picker lists from the sector's roster of clients, a
// view: the name is read from the clients' table the roster's rows are rows of, by the
// column's foreign key, and answered to a reader who may list the clients' names; a
// reader who may list the roster's names and not the clients' is answered the id alone,
// and refused nothing.
func TestChangeHistory_keepsClientNames(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db, err := prepareDatabase(ctx, t, migrationsSource, demoSeedSource)
	if err != nil {
		t.Fatal(err)
	}
	writer := newTestApp(db, grants{accesstypes.Create: withFields(missionsResource, "clientId", "kindId", "title", "hazard", "fee", "deadline")})
	status, body := doRequest(t, writer, http.MethodPatch, "/console/api/resources",
		fmt.Sprintf(`[{"op":"add","path":%q,"value":{"clientId":%q,"kindId":"rescue","title":"Booked for a client","hazard":1,"fee":100,"deadline":%q}}]`, opPath(anvil, "missions"), clientHalvardID, deadline(365)))
	assertStatus(t, status, http.StatusOK, body)
	ids, _ := decodeRow(t, body)["missions"].([]any)
	if len(ids) != 1 {
		t.Fatalf("created ids = %v, want one mission id: %s", ids, body)
	}
	id, _ := ids[0].(string)

	changeSets := storedChangeSets(t, db, "Missions", id)
	if len(changeSets) != 1 {
		t.Fatalf("the mission has %d change records, want its create's", len(changeSets))
	}
	if got, want := changeSets[0]["ClientID"], (storedChange{Old: nil, New: clientHalvardID, OldName: "", NewName: "Halvard Freight"}); got != want {
		t.Errorf("the create's ClientID entry = %+v, want %+v: the name Clients.Name holds", got, want)
	}

	read := withFields(missionsResource, "clientId", "title")
	for _, tt := range []struct {
		name string
		list accesstypes.Resource
		want string
	}{
		{name: "a reader who may list the clients' names", list: fieldResource("Clients", "name"), want: "Halvard Freight"},
		{name: "a reader who may list the roster's names and not the clients'", list: fieldResource("ClientRosters", "name"), want: ""},
	} {
		h := newTestApp(db, grants{accesstypes.Read: read, accesstypes.List: {tt.list}})
		page := readHistory(t, h, "integration-test-user", missionHistory(id, ""))
		if len(page.Changes) != 1 {
			t.Fatalf("%s reads %d records, want the create's", tt.name, len(page.Changes))
		}
		client, ok := page.Changes[0].field("clientId")
		if !ok {
			t.Fatalf("%s reads the fields %v, want clientId among them", tt.name, page.Changes[0].names())
		}
		if got := jsonText(t, client.New) + " " + client.NewName; got != clientHalvardID+" "+tt.want {
			t.Errorf("%s reads the client as %q, want %q", tt.name, got, clientHalvardID+" "+tt.want)
		}
	}
}

// conditionalAccess is staticAccess with the resources it names granted under a condition,
// whatever the permission: a role that may read them on some rows and not on others.
type conditionalAccess struct {
	*staticAccess
	conditional []accesstypes.Resource
}

func (c *conditionalAccess) ForUser(user accesstypes.User) *access.UserChecker {
	return access.NewUserChecker(c, user)
}

func (c *conditionalAccess) CheckUserResources(ctx context.Context, env accesstypes.Environment, user accesstypes.User, scope accesstypes.Scope, perm accesstypes.Permission, names ...accesstypes.Resource) (accesstypes.Decisions, error) {
	decisions, err := c.staticAccess.CheckUserResources(ctx, env, user, scope, perm, names...)
	if err != nil {
		return nil, err
	}
	condition, err := accesstypes.NewCondition("hazard IN (1, 2)")
	if err != nil {
		return nil, fmt.Errorf("accesstypes.NewCondition(): %w", err)
	}
	for _, res := range names {
		if slices.Contains(c.conditional, res) {
			decisions[res] = accesstypes.Conditional(accesstypes.ConditionGroup{Resources: []accesstypes.Resource{res}, Condition: condition})
		}
	}

	return decisions, nil
}

// TestChangeHistory_keepsHolderNames pins the names a hangar pass's history keeps for its
// holder, as each was when the change was made. A person is kept by the account's
// username, read from the crew auth's accounts and not from the sector's list of its
// people, which shows their name: it is answered to a reader who may list the people's
// usernames outright, and the id alone to one who may list their display names alone, or
// their usernames under a condition. A group is kept by its name, read from the crew
// auth's groups within the sector, and the record still answers that name once the group
// is renamed. Each pass's delete keeps its holder's name again, as it then is.
func TestChangeHistory_keepsHolderNames(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db, err := prepareDatabase(ctx, t, migrationsSource, demoSeedSource)
	if err != nil {
		t.Fatal(err)
	}
	person := makeCrewAccount(ctx, t, db, "rook")
	groupUUID, err := impulse.NewUUID()
	if err != nil {
		t.Fatalf("impulse.NewUUID() error = %v", err)
	}
	group := groupUUID.String()
	key := access.NewMembershipKey(accesstypes.DomainPolicyScope(anvil), "", "")
	if _, err := db.Apply(ctx, []*spanner.Mutation{
		spanner.UpdateMap("CrewSessionUsers", map[string]any{"Id": string(person), "Name": "Rook Vance"}),
		spanner.InsertMap("CrewGroups", map[string]any{
			"Id": group, "Kind": key.Kind, "Axis": key.Axis, "DomainId": key.Domain, "Name": "Dock watch", "Description": "Keeps the dock, for the history's suite.",
			"UpdatedAt": spanner.CommitTimestamp,
		}),
	}); err != nil {
		t.Fatalf("writing the person's name and the group: %v", err)
	}

	passes := accesstypes.Resource("HangarPasses")
	writer := newTestApp(db, grants{accesstypes.Create: withFields(passes, "hangarId", "userId", "groupId", "levelId"), accesstypes.Delete: {passes}})
	write := func(op, path, value string) []byte {
		t.Helper()

		operation := fmt.Sprintf(`{"op":%q,"path":%q}`, op, opPath(anvil, path))
		if value != "" {
			operation = fmt.Sprintf(`{"op":%q,"path":%q,"value":%s}`, op, opPath(anvil, path), value)
		}
		status, body := doRequest(t, writer, http.MethodPatch, "/console/api/resources", "["+operation+"]")
		assertStatus(t, status, http.StatusOK, body)

		return body
	}
	create := func(holder string) string {
		t.Helper()

		body := write("add", "hangar-passes", fmt.Sprintf(`{"hangarId":%q,%s,"levelId":"visit"}`, hangarAnvilDockID, holder))
		ids, _ := decodeRow(t, body)["hangarPasses"].([]any)
		if len(ids) != 1 {
			t.Fatalf("created ids = %v, want one pass id: %s", ids, body)
		}
		id, _ := ids[0].(string)

		return id
	}
	read := withFields(passes, "hangarId", "userId", "groupId", "levelId")
	people, groups := fieldResource("CrewSectorUsers", "username"), fieldResource("CrewSectorGroups", "name")
	// stored answers one field's entry of the newest of a pass's change records, as the
	// database holds it.
	stored := func(pass, field string) storedChange {
		t.Helper()

		changeSets := storedChangeSets(t, db, "HangarPasses", pass)
		if len(changeSets) == 0 {
			t.Fatalf("the pass %s has no change record", pass)
		}

		return changeSets[len(changeSets)-1][field]
	}
	// holderName answers the newName the pass's history answers for one field of its
	// create, as a reader the access answers for reads it.
	holderName := func(controller access.Controller, id, field string) string {
		t.Helper()

		page := readHistory(t, newTestAppWithAccess(db, controller), "integration-test-user", sectorPath(anvil, "hangar-passes/"+id+"/changes"))
		created := page.Changes[len(page.Changes)-1]
		change, ok := created.field(field)
		if !ok {
			t.Fatalf("the create's record lists %v, want %s among them", created.names(), field)
		}

		return change.NewName
	}

	personPass := create(fmt.Sprintf(`"userId":%q`, person))
	if got := stored(personPass, "UserID").NewName; got != "rook" {
		t.Errorf("the create's UserID entry keeps %q, want the username rook, not the person's name", got)
	}
	for _, tt := range []struct {
		name       string
		controller access.Controller
		want       string
	}{
		{name: "a reader of the people's usernames", controller: &staticAccess{g: grants{accesstypes.Read: read, accesstypes.List: {people}}}, want: "rook"},
		{name: "a reader of the people's display names alone", controller: &staticAccess{g: grants{accesstypes.Read: read, accesstypes.List: {fieldResource("CrewSectorUsers", "displayName")}}}, want: ""},
		{name: "a reader of the people's usernames under a condition", controller: &conditionalAccess{staticAccess: &staticAccess{g: grants{accesstypes.Read: read}}, conditional: []accesstypes.Resource{people}}, want: ""},
	} {
		if got := holderName(tt.controller, personPass, "userId"); got != tt.want {
			t.Errorf("%s reads the holder's name %q, want %q", tt.name, got, tt.want)
		}
	}

	groupPass := create(fmt.Sprintf(`"groupId":%q`, group))
	if got := stored(groupPass, "GroupID").NewName; got != "Dock watch" {
		t.Errorf("the create's GroupID entry keeps %q, want the group's name", got)
	}
	if _, err := db.Apply(ctx, []*spanner.Mutation{spanner.UpdateMap("CrewGroups", map[string]any{"Id": group, "Name": "Night watch"})}); err != nil {
		t.Fatalf("renaming the group: %v", err)
	}
	if got := holderName(&staticAccess{g: grants{accesstypes.Read: read, accesstypes.List: {groups}}}, groupPass, "groupId"); got != "Dock watch" {
		t.Errorf("a reader of the groups' names reads the renamed group's pass as %q, want the name it had", got)
	}
	if got := holderName(&staticAccess{g: grants{accesstypes.Read: read}}, groupPass, "groupId"); got != "" {
		t.Errorf("a reader who may not list the groups' names reads %q, want the id alone", got)
	}

	write("remove", "hangar-passes/"+groupPass, "")
	write("remove", "hangar-passes/"+personPass, "")
	for _, tt := range []struct {
		pass, field, want string
	}{
		{pass: groupPass, field: "GroupID", want: "Night watch"},
		{pass: personPass, field: "UserID", want: "rook"},
	} {
		if got := stored(tt.pass, tt.field).OldName; got != tt.want {
			t.Errorf("the delete's %s entry keeps %q, want %q, the holder's name as it then was", tt.field, got, tt.want)
		}
	}
}

// TestChangeHistory_keepsAccountNames pins the names a pilot's history keeps for its
// account, one of the whole application's accounts: the account's username, read from the
// crew auth's accounts by the id alone, as it was when the change was made. A pilot made
// for rook and moved onto finch's account keeps rook's username on its create and on its
// move, and finch's on the move, and still answers both once rook's account is deleted: to
// a reader who may list the accounts' usernames, and the ids alone, refused nothing, to a
// reader who may not.
func TestChangeHistory_keepsAccountNames(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db, err := prepareDatabase(ctx, t, migrationsSource, demoSeedSource)
	if err != nil {
		t.Fatal(err)
	}
	rook := string(makeCrewAccount(ctx, t, db, "rook"))
	finch := string(makeCrewAccount(ctx, t, db, "finch"))
	if _, err := db.Apply(ctx, []*spanner.Mutation{spanner.UpdateMap("CrewSessionUsers", map[string]any{"Id": rook, "Name": "Rook Vance"})}); err != nil {
		t.Fatalf("writing rook's name: %v", err)
	}

	pilots := accesstypes.Resource("Pilots")
	writer := newTestApp(db, grants{
		accesstypes.Create: withFields(pilots, "userId", "displayName", "clearance", "feeLimit"),
		accesstypes.Update: withFields(pilots, "userId"),
	})
	write := func(op, path, value string) []byte {
		t.Helper()

		status, body := doRequest(t, writer, http.MethodPatch, "/console/api/resources", fmt.Sprintf(`[{"op":%q,"path":%q,"value":%s}]`, op, path, value))
		assertStatus(t, status, http.StatusOK, body)

		return body
	}
	ids, _ := decodeRow(t, write("add", "/pilots", fmt.Sprintf(`{"userId":%q,"displayName":"Rook Vance","clearance":1,"feeLimit":100}`, rook)))["pilots"].([]any)
	if len(ids) != 1 {
		t.Fatalf("created ids = %v, want one pilot id", ids)
	}
	pilot, _ := ids[0].(string)
	write("patch", "/pilots/"+pilot, fmt.Sprintf(`{"userId":%q}`, finch))

	changeSets := storedChangeSets(t, db, "Pilots", pilot)
	if len(changeSets) != 2 {
		t.Fatalf("the pilot has %d change records, want its create's and its move's", len(changeSets))
	}
	for i, want := range []storedChange{
		{Old: nil, New: rook, OldName: "", NewName: "rook"},
		{Old: rook, New: finch, OldName: "rook", NewName: "finch"},
	} {
		if got := changeSets[i]["UserID"]; got != want {
			t.Errorf("record %d's UserID entry = %+v, want %+v: each account's username, not the person's name", i, got, want)
		}
	}

	if _, err := db.Apply(ctx, []*spanner.Mutation{spanner.Delete("CrewSessionUsers", spanner.Key{rook})}); err != nil {
		t.Fatalf("deleting rook's account: %v", err)
	}
	read := withFields(pilots, "userId", "displayName")
	usernames := fieldResource("CrewSessionUsers", "username")
	for _, tt := range []struct {
		name       string
		controller access.Controller
		named      bool
	}{
		{name: "a reader of the accounts' usernames", controller: &staticAccess{g: grants{accesstypes.Read: read, accesstypes.List: {usernames}}}, named: true},
		{name: "a reader who may not list the accounts' usernames", controller: &staticAccess{g: grants{accesstypes.Read: read}}},
	} {
		page := readHistory(t, newTestAppWithAccess(db, tt.controller), "integration-test-user", consoleAPI+"/pilots/"+pilot+"/changes")
		if len(page.Changes) != 2 {
			t.Fatalf("%s reads %d records, want the move's and the create's", tt.name, len(page.Changes))
		}
		move, moved := page.Changes[0].field("userId")
		made, created := page.Changes[1].field("userId")
		if !moved || !created {
			t.Fatalf("%s reads the fields %v and %v, want userId in both", tt.name, page.Changes[0].names(), page.Changes[1].names())
		}
		wantMove, wantMade := [4]string{rook, "", finch, ""}, [2]string{rook, ""}
		if tt.named {
			wantMove, wantMade = [4]string{rook, "rook", finch, "finch"}, [2]string{rook, "rook"}
		}
		if got := [4]string{jsonText(t, move.Old), move.OldName, jsonText(t, move.New), move.NewName}; got != wantMove {
			t.Errorf("%s reads the move as %q, want %q", tt.name, got, wantMove)
		}
		if got := [2]string{jsonText(t, made.New), made.NewName}; got != wantMade {
			t.Errorf("%s reads the create as %q, want %q", tt.name, got, wantMade)
		}
	}
}

// TestChangeHistory_untracked pins a resource that does not track its changes: the
// hangars answer 404 to a reader of the hangar, and 403 to a person who may not read it.
func TestChangeHistory_untracked(t *testing.T) {
	t.Parallel()

	_, h, _ := sharedWorld(t)
	target := sectorPath(anvil, "hangars/"+hangarAnvilDockID+"/changes")
	status, body := doRequestAs(t, h, "marshal", http.MethodGet, target, "")
	assertStatus(t, status, http.StatusNotFound, body)
	status, body = doRequestAs(t, h, "archivist", http.MethodGet, target, "")
	assertStatus(t, status, http.StatusForbidden, body)
}

// TestChangeHistory_refusals pins what the route refuses: a person who may not read
// missions, a parameter other than after and field, a place to continue from that no
// page gave, a field the mission does not have, a field the server never returns, and a
// secret, in the words of a field the record does not have.
func TestChangeHistory_refusals(t *testing.T) {
	t.Parallel()

	_, h, _ := sharedWorld(t)
	tests := []struct {
		name        string
		user        accesstypes.User
		target      string
		wantStatus  int
		wantMessage string
	}{
		{name: "no Read on missions", user: "yeoman", target: missionHistory(missionHaulerID, ""), wantStatus: http.StatusForbidden},
		{name: "another parameter", user: "marshal", target: missionHistory(missionHaulerID, "limit=5"), wantStatus: http.StatusBadRequest, wantMessage: "A record's history takes two parameters, after and field. Take limit out of the request."},
		{name: "a malformed after", user: "marshal", target: missionHistory(missionHaulerID, "after=yesterday"), wantStatus: http.StatusBadRequest, wantMessage: `The history cannot be continued from \"yesterday\". Use the value of next from the page before.`},
		{name: "an unknown field", user: "marshal", target: missionHistory(missionHaulerID, "field=color"), wantStatus: http.StatusBadRequest, wantMessage: `This record has no field named \"color\".`},
		{name: "a field the server never returns", user: "marshal", target: sectorPath(anvil, "distress-calls/90000000-0000-4000-8000-0000000000ff/changes?field=transcript"), wantStatus: http.StatusBadRequest, wantMessage: `This record has no field named \"transcript\".`},
		{name: "a secret", user: "marshal", target: sectorPath(anvil, "ships/"+shipKingfisherID+"/changes?field=transponderKey"), wantStatus: http.StatusBadRequest, wantMessage: `This record has no field named \"transponderKey\".`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status, body := doRequestAs(t, h, tt.user, http.MethodGet, tt.target, "")
			assertStatus(t, status, tt.wantStatus, body)
			if tt.wantMessage != "" && !strings.Contains(string(body), tt.wantMessage) {
				t.Errorf("the refusal says %s, want %q", body, tt.wantMessage)
			}
		})
	}
}
