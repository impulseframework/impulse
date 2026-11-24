// Demonstrates: change-history.
package integration

// This suite pins a record's history as the generated route serves it, GET <the read
// route>/changes, over the emulator and the application's own roles: a mission's change
// records newest first, a page at a time, narrowed to one field, with only the fields the
// reader can see on the mission as it stands, and a deleted mission's history to a
// reader whose grant carries no condition.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
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
	At       time.Time            `json:"at"`
	Sequence int64                `json:"sequence"`
	Source   string               `json:"source"`
	Fields   []changeHistoryField `json:"fields"`
}

// changeHistoryField is one changed field of a change record.
type changeHistoryField struct {
	Field  string          `json:"field"`
	Old    json.RawMessage `json:"old"`
	New    json.RawMessage `json:"new"`
	Secret bool            `json:"secret"`
}

// names answers the record's fields by name.
func (r changeHistoryRecord) names() []string {
	names := make([]string, 0, len(r.Fields))
	for _, field := range r.Fields {
		names = append(names, field.Field)
	}

	return names
}

// field answers the record's change of one field, and whether it has one.
func (r changeHistoryRecord) field(name string) (changeHistoryField, bool) {
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

// patchAnvil writes one consolidated operation on an Anvil record as user, holding the
// answer to 200.
func patchAnvil(t *testing.T, h http.Handler, user accesstypes.User, op, rest, value string) {
	t.Helper()

	operation := fmt.Sprintf(`{"op":%q,"path":%q}`, op, opPath(anvil, rest))
	if value != "" {
		operation = fmt.Sprintf(`{"op":%q,"path":%q,"value":%s}`, op, opPath(anvil, rest), value)
	}
	status, body := doRequestAs(t, h, user, http.MethodPatch, "/console/api/resources", "["+operation+"]")
	assertStatus(t, status, http.StatusOK, body)
}

// createMission makes an Anvil mission as the marshal, titled title, and answers its id.
func createMission(t *testing.T, h http.Handler, db *initiator.SpannerDB, title string) string {
	t.Helper()

	patchAnvil(t, h, "marshal", "add", "missions", fmt.Sprintf(`{"clientId":%q,"kindId":"rescue","title":%q,"hazard":2,"fee":100,"deadline":%q}`, clientHalvardID, title, deadline(365)))
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
// under the field's name on the wire with its old and its new value, and each made by
// the marshal's session.
func TestChangeHistory_newestFirst(t *testing.T) {
	t.Parallel()

	_, db, h := demoWorld(t)
	id := createMission(t, h, db, "History of three")
	patchAnvil(t, h, "marshal", "patch", "missions/"+id, `{"title":"History of three, renamed"}`)
	patchAnvil(t, h, "marshal", "patch", "missions/"+id, `{"fee":250}`)

	page := readHistory(t, h, "marshal", missionHistory(id, ""))
	if len(page.Changes) != 3 || page.Next != "" {
		t.Fatalf("the history holds %d records and next %q, want 3 and none: %+v", len(page.Changes), page.Next, page)
	}
	for i := 1; i < len(page.Changes); i++ {
		if page.Changes[i].At.After(page.Changes[i-1].At) {
			t.Errorf("record %d is newer than the one before it: the history is newest first", i)
		}
	}
	who := fmt.Sprintf("marshal (%s), session ", account(t, "marshal"))
	for i, record := range page.Changes {
		if !strings.HasPrefix(record.Source, who) {
			t.Errorf("record %d was made by %q, want the marshal's session %q", i, record.Source, who)
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
		patchAnvil(t, h, "marshal", "patch", "missions/"+id, fmt.Sprintf(`{"title":"Pass %d"}`, i))
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
	patchAnvil(t, h, "marshal", "patch", "missions/"+id, `{"notes":"Bring spares."}`)
	patchAnvil(t, h, "marshal", "patch", "missions/"+id, `{"title":"History of a field, renamed"}`)
	patchAnvil(t, h, "marshal", "patch", "missions/"+id, `{"notes":"Bring more spares."}`)

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
	patchAnvil(t, h, "marshal", "patch", "missions/"+id, `{"notes":"Quiet run."}`)
	patchAnvil(t, h, "marshal", "patch", "missions/"+id, `{"title":"History without notes, renamed"}`)

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
	patchAnvil(t, h, "marshal", "patch", "missions/"+id, `{"fee":900}`)
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
// missions carries no condition, reads it with the delete's record first; the cadet and
// the archivist, whose Read carries one, are answered as for a mission that is not there.
func TestChangeHistory_deleted(t *testing.T) {
	t.Parallel()

	_, db, h := demoWorld(t)
	id := createMission(t, h, db, "History of a delete")
	patchAnvil(t, h, "marshal", "patch", "missions/"+id, `{"hazard":1}`)
	patchAnvil(t, h, "marshal", "remove", "missions/"+id, "")

	page := readHistory(t, h, "marshal", missionHistory(id, ""))
	if len(page.Changes) != 3 {
		t.Fatalf("the marshal reads %d records of the deleted mission, want 3", len(page.Changes))
	}
	title, ok := page.Changes[0].field("title")
	if !ok || jsonText(t, title.Old) != "History of a delete" || jsonText(t, title.New) != "null" {
		t.Errorf("the delete's record lists %v, want the title it took, and no new value", page.Changes[0].Fields)
	}

	_, never := doRequestAs(t, h, "cadet", http.MethodGet, missionHistory("80000000-0000-4000-8000-0000000000ff", ""), "")
	for _, user := range []accesstypes.User{"cadet", "archivist"} {
		status, body := doRequestAs(t, h, user, http.MethodGet, missionHistory(id, ""), "")
		assertStatus(t, status, http.StatusNotFound, body)
		if string(body) != string(never) {
			t.Errorf("%s is told %s of the deleted mission, want what a mission that never was says: %s", user, body, never)
		}
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
	if string(body) != string(never) {
		t.Errorf("another sector's mission is answered %s, want what a mission that never was is: %s", body, never)
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
// page gave, a field the mission does not have, and a field the server never returns.
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
