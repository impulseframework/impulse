package integration

// This suite pins a refit's tasks, whose struct lists its key fields the other way round
// from the table's key (Id, TaskNumber), on the served routes over the emulator: a task's
// own add, patches and remove take the row the route names, and its history holds every
// one of them under the one key the table spells, a remove taken with its refit included.

import (
	"fmt"
	"net/http"
	"testing"

	"cloud.google.com/go/spanner"
	initiator "github.com/impulseframework/impulse/db"
	"google.golang.org/grpc/codes"
)

// refitTaskOp is one consolidated operation on a refit task of Anvil, with its value
// where value is not empty.
func refitTaskOp(op, refitID string, taskNumber int64, value string) string {
	path := opPath(anvil, fmt.Sprintf("refit-tasks/%s/%d", refitID, taskNumber))
	if value == "" {
		return fmt.Sprintf(`[{"op":%q,"path":%q}]`, op, path)
	}

	return fmt.Sprintf(`[{"op":%q,"path":%q,"value":%s}]`, op, path, value)
}

// sendAsMarshal sends a batch as the marshal, whose grants on refits and their tasks
// carry no condition, holding the answer to 200.
func sendAsMarshal(t *testing.T, h http.Handler, ops string) {
	t.Helper()

	status, body := doRequestAs(t, h, "marshal", http.MethodPatch, "/console/api/resources", ops)
	assertStatus(t, status, http.StatusOK, body)
}

// refitTaskRow reads a task's instructions, done and notes past the API, and whether the
// row stands.
func refitTaskRow(t *testing.T, db *initiator.SpannerDB, refitID string, taskNumber int64) (instructions string, done bool, notes spanner.NullString, stands bool) {
	t.Helper()

	row, err := db.Single().ReadRow(t.Context(), "RefitTasks", spanner.Key{refitID, taskNumber}, []string{"Instructions", "Done", "Notes"})
	if spanner.ErrCode(err) == codes.NotFound {
		return "", false, spanner.NullString{}, false
	}
	if err != nil {
		t.Fatalf("ReadRow(RefitTasks, %s, %d): %v", refitID, taskNumber, err)
	}
	if err := row.Columns(&instructions, &done, &notes); err != nil {
		t.Fatalf("row.Columns: %v", err)
	}

	return instructions, done, notes, true
}

// refitTaskChangeRows counts the change records of the table RefitTasks filed under
// rowID.
func refitTaskChangeRows(t *testing.T, db *initiator.SpannerDB, rowID string) int64 {
	t.Helper()

	iter := db.Single().Query(t.Context(), spanner.Statement{
		SQL:    "SELECT COUNT(*) FROM DataChangeEvents WHERE TableName = 'RefitTasks' AND RowId = @row",
		Params: map[string]any{"row": rowID},
	})
	defer iter.Stop()
	row, err := iter.Next()
	if err != nil {
		t.Fatalf("counting the change records of RefitTasks %s: %v", rowID, err)
	}
	var count int64
	if err := row.Columns(&count); err != nil {
		t.Fatalf("row.Columns: %v", err)
	}

	return count
}

// assertOperations holds a history page to its records' operations, newest first.
func assertOperations(t *testing.T, page changeHistoryPage, want ...string) {
	t.Helper()

	got := make([]string, 0, len(page.Changes))
	for _, record := range page.Changes {
		got = append(got, record.Operation)
	}
	if fmt.Sprint(got) != fmt.Sprint(want) || page.Next != "" {
		t.Errorf("the history holds %v and next %q, want %v and none", got, page.Next, want)
	}
}

// TestRefitTaskKeyOrder_ownWrites pins a task's own writes on the seeded refit under way:
// an add, two patches and a remove each take the row at the route's key, and the task's
// history is the four of them, newest first, under the RowId the table's key spells.
func TestRefitTaskKeyOrder_ownWrites(t *testing.T) {
	t.Parallel()

	_, db, h := demoWorld(t)
	const number = 61

	sendAsMarshal(t, h, refitTaskOp("add", refitSamaritanID, number, `{"instructions":"Torque the aft coupling"}`))
	if instructions, _, _, stands := refitTaskRow(t, db, refitSamaritanID, number); !stands || instructions != "Torque the aft coupling" {
		t.Fatalf("after the add the task stands %v with instructions %q, want it to stand with the instructions sent", stands, instructions)
	}

	sendAsMarshal(t, h, refitTaskOp("patch", refitSamaritanID, number, `{"done":true}`))
	if _, done, _, stands := refitTaskRow(t, db, refitSamaritanID, number); !stands || !done {
		t.Fatalf("after the patch the task stands %v and done %v, want it done", stands, done)
	}
	sendAsMarshal(t, h, refitTaskOp("patch", refitSamaritanID, number, `{"notes":"Coupling torqued twice"}`))
	if _, _, notes, stands := refitTaskRow(t, db, refitSamaritanID, number); !stands || notes.StringVal != "Coupling torqued twice" {
		t.Fatalf("after the second patch the task stands %v with notes %v, want the notes sent", stands, notes)
	}

	sendAsMarshal(t, h, refitTaskOp("remove", refitSamaritanID, number, ""))
	if _, _, _, stands := refitTaskRow(t, db, refitSamaritanID, number); stands {
		t.Fatal("the task stands after its remove answered 200")
	}

	page := readHistory(t, h, "marshal", sectorPath(anvil, fmt.Sprintf("refit-tasks/%s/%d/changes", refitSamaritanID, number)))
	assertOperations(t, page, "delete", "update", "update", "create")

	if got := refitTaskChangeRows(t, db, fmt.Sprintf("%s|%d", refitSamaritanID, number)); got != 4 {
		t.Errorf("DataChangeEvents holds %d rows of the task under the table's key, want 4", got)
	}
	if got := refitTaskChangeRows(t, db, fmt.Sprintf("%d|%s", number, refitSamaritanID)); got != 0 {
		t.Errorf("DataChangeEvents holds %d rows of the task under the struct's order, want none", got)
	}
}

// TestRefitTaskKeyOrder_takenWithItsRefit pins a task deleted with its refit: the refit's
// remove with everything under it takes the task, and the task's history holds that
// delete after its own create and update, under the one RowId.
func TestRefitTaskKeyOrder_takenWithItsRefit(t *testing.T) {
	t.Parallel()

	_, db, h := demoWorld(t)
	const (
		marshal = "f0000000-0000-4000-8000-000000000004"
		hull    = "7d000000-0000-4000-8000-0000000e0001"
		bay     = "ad000000-0000-4000-8000-0000000e0001"
	)
	if _, err := db.Apply(t.Context(), []*spanner.Mutation{
		spanner.Insert("Ships", []string{"Id", "HangarId", "ClassId", "Registry", "Name"}, []any{hull, hangarAnvilDockID, shipClassKestrelID, "KEY-0001", "Key Order Hull"}),
		spanner.InsertMap("Refits", map[string]any{"Id": bay, "ShipId": hull, "StatusId": "docked", "OpenedByUserId": marshal, "OpenedByName": "marshal"}),
	}); err != nil {
		t.Fatalf("writing the ship and its refit: %v", err)
	}

	sendAsMarshal(t, h, refitTaskOp("add", bay, 1, `{"instructions":"Strip the old plating"}`))
	sendAsMarshal(t, h, refitTaskOp("patch", bay, 1, `{"done":true}`))

	// The remove that takes the task is asked first, and goes ahead on the question's
	// token, in the same session.
	keepSession(t, "marshal")
	status, body := doRequestAs(t, h, "marshal", http.MethodPatch, "/console/api/resources", fmt.Sprintf(`[{"op":"remove","path":%q}]`, opPath(anvil, "refits/"+bay)))
	assertStatus(t, status, http.StatusPreconditionRequired, body)
	sendAsMarshal(t, h, fmt.Sprintf(`[{"op":"remove","path":%q,"value":{"everythingUnder":true,"token":%q}}]`, opPath(anvil, "refits/"+bay), deleteToken(t, body)))
	if _, err := db.Single().ReadRow(t.Context(), "Refits", spanner.Key{bay}, []string{"Id"}); spanner.ErrCode(err) != codes.NotFound {
		t.Errorf("reading the refit after its remove: %v, want it gone", err)
	}
	if _, _, _, stands := refitTaskRow(t, db, bay, 1); stands {
		t.Error("the task stands after its refit was removed with everything under it")
	}

	page := readHistory(t, h, "marshal", sectorPath(anvil, fmt.Sprintf("refit-tasks/%s/1/changes", bay)))
	assertOperations(t, page, "delete", "update", "create")

	if got := refitTaskChangeRows(t, db, bay+"|1"); got != 3 {
		t.Errorf("DataChangeEvents holds %d rows of the task under the table's key, want 3", got)
	}
}
