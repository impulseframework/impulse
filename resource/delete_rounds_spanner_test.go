package resource

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/eventsource"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/spxscan"
)

// roundTree is the description of a CascadeProjects record whose delete takes the
// comments on its tasks and its tasks, which may sit under another task, each task's
// delete armed by arm.
func roundTree(arm func(patchSet *PatchSet[roundTask], caller *Caller)) *DeleteTree {
	return &DeleteTree{
		Record: "CascadeProjects", Words: DeleteWords{One: "project", Many: "projects"}, Key: []string{"Id"},
		Taken: []DeleteKind{
			{
				Resource: "CascadeComments", Words: DeleteWords{One: "comment", Many: "comments"}, Key: []string{"Id"},
				Paths: []DeletePath{{
					{Columns: []string{"TaskId"}, Table: "CascadeTasks", References: []string{"Id"}},
					{Columns: []string{"ProjectId"}, Table: "CascadeProjects", References: []string{"Id"}},
				}},
				Rows: NewTakenRows(func(*PatchSet[roundComment], *Caller) {}),
			},
			{
				Resource: "CascadeTasks", Words: DeleteWords{One: "task", Many: "tasks"}, Key: []string{"Id"},
				Paths:  []DeletePath{{{Columns: []string{"ProjectId"}, Table: "CascadeProjects", References: []string{"Id"}}}},
				Within: []DeleteLink{{Columns: []string{"ParentTaskId"}, Table: "CascadeTasks", References: []string{"Id"}}},
				Rows:   NewTakenRows(arm),
			},
		},
	}
}

// roundProject is a CascadeProjects record whose delete takes its tasks and the
// comments on them (roundTree).
type roundProject struct {
	ID   string             `spanner:"Id"`
	Code spanner.NullString `spanner:"Code"`
}

func (roundProject) Resource() accesstypes.Resource {
	return "CascadeProjects"
}

func (roundProject) DeleteTree() *DeleteTree {
	return roundTree(func(*PatchSet[roundTask], *Caller) {})
}

// trackedRoundProject is roundProject whose own changes are recorded too.
type trackedRoundProject struct {
	ID   string             `spanner:"Id"`
	Code spanner.NullString `spanner:"Code"`
}

func (trackedRoundProject) Resource() accesstypes.Resource {
	return "CascadeProjects"
}

func (trackedRoundProject) Config() Config {
	return Config{}.SetTrackChanges(true)
}

func (trackedRoundProject) DefaultConfig() Config {
	return Config{}
}

func (trackedRoundProject) DeleteTree() *DeleteTree {
	return roundTree(func(*PatchSet[roundTask], *Caller) {})
}

// refusingTasks says the tasks of a ruledRoundProject may not be deleted.
var refusingTasks atomic.Bool

// ruledRoundProject is roundProject whose tasks' delete asks a rule, which refuses while
// refusingTasks holds.
type ruledRoundProject struct {
	ID   string             `spanner:"Id"`
	Code spanner.NullString `spanner:"Code"`
}

func (ruledRoundProject) Resource() accesstypes.Resource {
	return "CascadeProjects"
}

func (ruledRoundProject) DeleteTree() *DeleteTree {
	return roundTree(func(patchSet *PatchSet[roundTask], _ *Caller) {
		patchSet.RegisterValidateDeleteFunc(func(context.Context, ReadWriteTransaction) error {
			if refusingTasks.Load() {
				return httpio.NewBadRequestMessage("A task that is being worked on can't be deleted.")
			}

			return nil
		})
	})
}

// invoicedRoundProject is roundProject whose delete is refused while an invoice is under
// one of its tasks (the refusing kind of invoicedTree).
type invoicedRoundProject struct {
	ID   string             `spanner:"Id"`
	Code spanner.NullString `spanner:"Code"`
}

func (invoicedRoundProject) Resource() accesstypes.Resource {
	return "CascadeProjects"
}

func (invoicedRoundProject) DeleteTree() *DeleteTree {
	tree := roundTree(func(*PatchSet[roundTask], *Caller) {})
	tree.RefusedBy = invoicedTree().RefusedBy[:1]

	return tree
}

// roundTask is a CascadeTasks row, whose changes are recorded.
type roundTask struct {
	ID           string             `spanner:"Id"`
	ProjectID    spanner.NullString `spanner:"ProjectId"`
	ParentTaskID spanner.NullString `spanner:"ParentTaskId"`
	Title        spanner.NullString `spanner:"Title"`
}

func (roundTask) Resource() accesstypes.Resource {
	return "CascadeTasks"
}

func (roundTask) Config() Config {
	return Config{}.SetTrackChanges(true)
}

func (roundTask) DefaultConfig() Config {
	return Config{}
}

// roundComment is a CascadeComments row.
type roundComment struct {
	ID        string             `spanner:"Id"`
	TaskID    spanner.NullString `spanner:"TaskId"`
	ProjectID spanner.NullString `spanner:"ProjectId"`
}

func (roundComment) Resource() accesstypes.Resource {
	return "CascadeComments"
}

// deleteRound runs one request's delete of the record R with the id, told to take what
// is under it, in a save that holds size mutations (DeleteMutationLimit for 0), handed
// source as who made the change, and returns the delete's answer (PatchSet.DeleteLeft).
func deleteRound[R Resourcer](t *testing.T, db *spanner.Client, id string, size int, source eventsource.EventSource) (*DeleteLeft, error) {
	t.Helper()

	return deleteRoundRefused[R](t, db, id, size, source, nil)
}

// deleteRoundRefused is deleteRound with each statement of the delete asked of refuses
// before it is sent (PatchSet.deleteRefuses; joinLimit), nil asking nothing.
func deleteRoundRefused[R Resourcer](t *testing.T, db *spanner.Client, id string, size int, source eventsource.EventSource, refuses func(sql string) error) (*DeleteLeft, error) {
	t.Helper()

	var left *DeleteLeft
	err := NewSpannerClient(db).ExecuteFunc(t.Context(), func(ctx context.Context, txn ReadWriteTransaction) error {
		record := NewPatchSet(NewMetadata[R]()).SetPatchType(DeletePatchType).DeleteEverythingUnder()
		record.SetKey("ID", id)
		record.deleteSaveSize = size
		record.deleteRefuses = refuses
		if err := record.Buffer(ctx, txn, source); err != nil {
			return err
		}
		left = record.DeleteLeft()

		return nil
	})

	return left, err
}

// askRound runs one request's delete of the record R with the id that asks before it
// takes anything, each statement asked of refuses first (PatchSet.deleteRefuses), and
// returns what it answered: the question, where the delete would take rows.
func askRound[R Resourcer](t *testing.T, db *spanner.Client, id string, refuses func(sql string) error) error {
	t.Helper()

	var asked error
	if err := NewSpannerClient(db).ExecuteFunc(t.Context(), func(ctx context.Context, txn ReadWriteTransaction) error {
		record := NewPatchSet(NewMetadata[R]()).SetPatchType(DeletePatchType)
		record.SetKey("ID", id)
		record.deleteRefuses = refuses
		asked = record.Buffer(ctx, txn, eventsource.ProcessEvent("test"))

		// The question deletes nothing: the transaction commits nothing either way.
		return nil
	}); err != nil {
		t.Fatalf("NewSpannerClient().ExecuteFunc() error = %v", err)
	}

	return asked
}

// roundRecord is a change record of a delete in rounds, read past the library: what kind
// of write it was, the row's tenant and who made it.
type roundRecord struct {
	Operation string             `spanner:"Operation"`
	TenantID  spanner.NullString `spanner:"TenantId"`
	eventSourceColumns
}

// roundRecords reads the change records of one row past the library.
func roundRecords(t *testing.T, db *spanner.Client, table, rowID string) []roundRecord {
	t.Helper()

	var records []roundRecord
	stmt := spanner.Statement{
		SQL:    "SELECT Operation, TenantId, " + strings.Join(eventsource.ColumnNames(), ", ") + " FROM DataChangeEvents WHERE TableName = @table AND RowId = @rowId",
		Params: map[string]any{"table": table, "rowId": rowID},
	}
	if err := spxscan.Select(t.Context(), db.Single(), &records, stmt); err != nil {
		t.Fatalf("reading the change records of %s %s: %v", table, rowID, err)
	}

	return records
}

// roundCount runs a count past the library, with the project's id as @project.
func roundCount(t *testing.T, db *spanner.Client, sql, project string) int64 {
	t.Helper()

	var n int64
	stmt := spanner.NewStatement(sql)
	stmt.Params["project"] = project
	if err := db.Single().Query(t.Context(), stmt).Do(func(row *spanner.Row) error {
		return row.Column(0, &n)
	}); err != nil {
		t.Fatalf("counting %q: %v", sql, err)
	}

	return n
}

// roundState is what the database holds of a project: its own row, its tasks and the
// comments on them.
type roundState struct {
	record   bool
	tasks    int64
	comments int64
}

func readRoundState(t *testing.T, db *spanner.Client, project string) roundState {
	t.Helper()

	return roundState{
		record:   roundCount(t, db, "SELECT COUNT(*) FROM CascadeProjects WHERE Id = @project", project) == 1,
		tasks:    roundCount(t, db, "SELECT COUNT(*) FROM CascadeTasks WHERE ProjectId = @project", project),
		comments: roundCount(t, db, "SELECT COUNT(*) FROM CascadeComments WHERE STARTS_WITH(Id, @project)", project),
	}
}

// TestPatchSet_deleteTaken_rounds holds, over the emulator, a delete larger than one
// save. A project whose tasks sit three deep, every one holding the project, with keys
// that put a task before the tasks under it, is deleted at a small save size: each
// request takes what fits, the deepest first, says what it deleted, and
// leaves the project standing, until the last takes the project too and says nothing.
// After every commit no task or comment that is left names a task that is gone, and
// every save stays within the size. Each task has the one change record of its delete.
// A rule that starts refusing between two rounds refuses the round, which commits
// nothing, while what earlier rounds took stays gone, and the delete finishes once the
// rule allows again; so does a refusing row that turns up between two rounds, for every
// save of a delete code told to take the rows asks everything again. A delete that fits
// one save deletes the record and says nothing.
func TestPatchSet_deleteTaken_rounds(t *testing.T) {
	t.Parallel()

	db := cascadeDatabase(t, "delete-rounds")
	task := func(id, project, parent string) *spanner.Mutation {
		row := map[string]any{"Id": id, "ProjectId": project}
		if parent != "" {
			row["ParentTaskId"] = parent
		}

		return spanner.InsertMap("CascadeTasks", row)
	}
	comment := func(id, task string) *spanner.Mutation {
		return spanner.InsertMap("CascadeComments", map[string]any{"Id": id, "TaskId": task})
	}
	if _, err := db.Apply(t.Context(), []*spanner.Mutation{
		spanner.InsertMap("CascadeProjects", map[string]any{"Id": "big"}),
		task("big-1a", "big", ""), task("big-1b", "big", ""), task("big-1c", "big", ""),
		task("big-2a", "big", "big-1a"), task("big-2b", "big", "big-1b"), task("big-2c", "big", "big-1c"),
		task("big-3a", "big", "big-2a"), task("big-3b", "big", "big-2b"), task("big-3c", "big", "big-2c"),
		comment("big-c1", "big-3a"), comment("big-c2", "big-1a"),
		spanner.InsertMap("CascadeProjects", map[string]any{"Id": "ruled"}),
		task("ruled-1", "ruled", ""), task("ruled-2", "ruled", ""), task("ruled-3", "ruled", ""), task("ruled-4", "ruled", ""), task("ruled-5", "ruled", ""),
		spanner.InsertMap("CascadeProjects", map[string]any{"Id": "invoiced"}),
		task("invoiced-1", "invoiced", ""), task("invoiced-2", "invoiced", ""), task("invoiced-3", "invoiced", ""), task("invoiced-4", "invoiced", ""), task("invoiced-5", "invoiced", ""),
		spanner.InsertMap("CascadeProjects", map[string]any{"Id": "small"}),
		task("small-1", "small", ""), task("small-2", "small", "small-1"), comment("small-c1", "small-2"),
		spanner.InsertMap("CascadeProjects", map[string]any{"Id": "two"}),
		task("two-1", "two", ""), task("two-2", "two", ""),
		spanner.InsertMap("CascadeProjects", map[string]any{"Id": "exact"}),
		task("exact-1", "exact", ""), task("exact-2", "exact", ""),
	}); err != nil {
		t.Fatalf("spanner.Client.Apply() error = %v", err)
	}

	// What one row's delete costs, by the package's own count: a task's carries its change
	// record, by the process that deletes it.
	const recordCost, commentCost = 1, 1
	taskCost := rowDeleteMutations + ChangeRecordMutations(eventsource.ProcessEvent("test"))

	t.Run("a delete larger than one save runs in rounds, the deepest first, and the record goes last", func(t *testing.T) {
		t.Parallel()

		// The record, a comment and two tasks.
		size := recordCost + commentCost + 2*taskCost
		rounds := []*DeleteLeft{{Deleted: 3}, {Deleted: 2}, {Deleted: 2}, {Deleted: 3}, nil}
		before := readRoundState(t, db, "big")
		for i, want := range rounds {
			got, err := deleteRound[roundProject](t, db, "big", size, eventsource.ProcessEvent("test"))
			if err != nil {
				t.Fatalf("round %d: the delete error = %v", i+1, err)
			}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("round %d: DeleteLeft() (-want +got):\n%s", i+1, diff)
			}
			after := readRoundState(t, db, "big")
			if after.record != (want != nil) {
				t.Errorf("round %d: the record is there = %t, want %t", i+1, after.record, want != nil)
			}
			mutations := (before.tasks-after.tasks)*int64(taskCost) + (before.comments-after.comments)*commentCost
			if !after.record {
				mutations += recordCost
			}
			if mutations > int64(size) {
				t.Errorf("round %d: the save cost %d mutations, more than the %d it holds", i+1, mutations, size)
			}
			if got := roundCount(t, db, "SELECT COUNT(*) FROM CascadeTasks t WHERE t.ProjectId = @project AND t.ParentTaskId IS NOT NULL AND NOT EXISTS (SELECT 1 FROM CascadeTasks p WHERE p.Id = t.ParentTaskId)", "big"); got != 0 {
				t.Errorf("round %d: %d task(s) left name a task that is gone", i+1, got)
			}
			if got := roundCount(t, db, "SELECT COUNT(*) FROM CascadeComments c WHERE STARTS_WITH(c.Id, @project) AND NOT EXISTS (SELECT 1 FROM CascadeTasks p WHERE p.Id = c.TaskId)", "big"); got != 0 {
				t.Errorf("round %d: %d comment(s) left name a task that is gone", i+1, got)
			}
			before = after
		}
		if got := roundCount(t, db, "SELECT COUNT(*) FROM CascadeTasks WHERE ProjectId = @project", "big"); got != 0 {
			t.Errorf("%d task(s) are left once the delete finished", got)
		}
		if got := roundCount(t, db, "SELECT COUNT(DISTINCT RowId) FROM DataChangeEvents WHERE TableName = 'CascadeTasks' AND STARTS_WITH(RowId, @project)", "big"); got != 9 {
			t.Errorf("%d task(s) have a change record, want 9", got)
		}
		if got := roundCount(t, db, "SELECT COUNT(*) FROM DataChangeEvents WHERE TableName = 'CascadeTasks' AND STARTS_WITH(RowId, @project)", "big"); got != 9 {
			t.Errorf("the tasks have %d change records, want one each, 9", got)
		}
	})

	t.Run("a rule that starts refusing between rounds refuses the round, and the delete goes on once it allows", func(t *testing.T) {
		t.Parallel()

		// The record and two tasks.
		size := recordCost + 2*taskCost
		if got, err := deleteRound[ruledRoundProject](t, db, "ruled", size, eventsource.ProcessEvent("test")); err != nil || !cmp.Equal(got, &DeleteLeft{Deleted: 2}) {
			t.Fatalf("the first round = %+v, %v, want {Deleted: 2}", got, err)
		}

		refusingTasks.Store(true)
		_, err := deleteRound[ruledRoundProject](t, db, "ruled", size, eventsource.ProcessEvent("test"))
		refusingTasks.Store(false)
		if want := "A task that is being worked on can't be deleted."; statusOf(err) != http.StatusBadRequest || httpio.Message(err) != want {
			t.Errorf("the refused round's error = %v (%q), want a 400 saying %q", err, httpio.Message(err), want)
		}
		if state := readRoundState(t, db, "ruled"); !state.record || state.tasks != 3 {
			t.Errorf("after the refused round the record is there = %t with %d tasks, want it there with the 3 the first round left", state.record, state.tasks)
		}

		if got, err := deleteRound[ruledRoundProject](t, db, "ruled", size, eventsource.ProcessEvent("test")); err != nil || !cmp.Equal(got, &DeleteLeft{Deleted: 2}) {
			t.Fatalf("the round once the rule allows = %+v, %v, want {Deleted: 2}", got, err)
		}
		if got, err := deleteRound[ruledRoundProject](t, db, "ruled", size, eventsource.ProcessEvent("test")); err != nil || got != nil {
			t.Fatalf("the last round = %+v, %v, want nil", got, err)
		}
		if state := readRoundState(t, db, "ruled"); state.record || state.tasks != 0 {
			t.Errorf("once the delete finished the record is there = %t with %d tasks, want both gone", state.record, state.tasks)
		}
	})

	t.Run("a refusing row that turns up between rounds refuses the next round", func(t *testing.T) {
		t.Parallel()

		// The record and two tasks.
		size := recordCost + 2*taskCost
		if got, err := deleteRound[invoicedRoundProject](t, db, "invoiced", size, eventsource.ProcessEvent("test")); err != nil || !cmp.Equal(got, &DeleteLeft{Deleted: 2}) {
			t.Fatalf("the first round = %+v, %v, want {Deleted: 2}", got, err)
		}

		if _, err := db.Apply(t.Context(), []*spanner.Mutation{spanner.InsertMap("CascadeInvoices", map[string]any{"Id": "invoiced-i1", "TaskId": "invoiced-4"})}); err != nil {
			t.Fatalf("spanner.Client.Apply() error = %v", err)
		}
		_, err := deleteRound[invoicedRoundProject](t, db, "invoiced", size, eventsource.ProcessEvent("test"))
		if want := "This project has 1 invoice, so it can't be deleted."; statusOf(err) != http.StatusConflict || httpio.Message(err) != want {
			t.Errorf("the second round's error = %v (%q), want a 409 saying %q", err, httpio.Message(err), want)
		}
		if state := readRoundState(t, db, "invoiced"); !state.record || state.tasks != 3 {
			t.Errorf("after the refused round the record is there = %t with %d tasks, want it there with the 3 the first round left", state.record, state.tasks)
		}

		if _, err := db.Apply(t.Context(), []*spanner.Mutation{spanner.Delete("CascadeInvoices", spanner.Key{"invoiced-i1"})}); err != nil {
			t.Fatalf("spanner.Client.Apply() error = %v", err)
		}
		if got, err := deleteRound[invoicedRoundProject](t, db, "invoiced", size, eventsource.ProcessEvent("test")); err != nil || !cmp.Equal(got, &DeleteLeft{Deleted: 2}) {
			t.Fatalf("the round once the invoice is gone = %+v, %v, want {Deleted: 2}", got, err)
		}
		if got, err := deleteRound[invoicedRoundProject](t, db, "invoiced", size, eventsource.ProcessEvent("test")); err != nil || got != nil {
			t.Fatalf("the last round = %+v, %v, want nil", got, err)
		}
		if state := readRoundState(t, db, "invoiced"); state.record || state.tasks != 0 {
			t.Errorf("once the delete finished the record is there = %t with %d tasks, want both gone", state.record, state.tasks)
		}
	})

	t.Run("each round's change records name that round's own request", func(t *testing.T) {
		t.Parallel()

		// The record, tracked, and one task: the first round takes a task and comes back
		// full. The second has room for two: it takes the other task, and with room left,
		// the record.
		record := rowDeleteMutations + ChangeRecordMutations(eventsource.ProcessEvent("first round"))
		if got, err := deleteRound[trackedRoundProject](t, db, "two", record+taskCost, eventsource.ProcessEvent("first round")); err != nil || !cmp.Equal(got, &DeleteLeft{Deleted: 1}) {
			t.Fatalf("the first round = %+v, %v, want {Deleted: 1}", got, err)
		}
		var first string
		for _, id := range []string{"two-1", "two-2"} {
			if len(roundRecords(t, db, "CascadeTasks", id)) == 1 {
				first = id
			}
		}
		if first == "" {
			t.Fatal("no task has a change record after the first round")
		}
		if got, err := deleteRound[trackedRoundProject](t, db, "two", record+2*taskCost, eventsource.ProcessEvent("second round")); err != nil || got != nil {
			t.Fatalf("the second round = %+v, %v, want nil", got, err)
		}

		second := "two-1"
		if first == "two-1" {
			second = "two-2"
		}
		for _, row := range []struct {
			table, id, process string
		}{
			{table: "CascadeTasks", id: first, process: "first round"},
			{table: "CascadeTasks", id: second, process: "second round"},
			{table: "CascadeProjects", id: "two", process: "second round"},
		} {
			records := roundRecords(t, db, row.table, row.id)
			if len(records) != 1 {
				t.Errorf("%s %s has %d change records, want the one of its delete", row.table, row.id, len(records))

				continue
			}
			want := eventSourceColumns{Process: spanner.NullString{StringVal: row.process, Valid: true}}
			if got := records[0]; got.Operation != string(ChangeDelete) || got.eventSourceColumns != want || got.TenantID.Valid {
				t.Errorf("%s %s's change record = %+v, want a delete by the process %q and no other column of who or of a tenant", row.table, row.id, got, row.process)
			}
		}
	})

	t.Run("a save is filled by what each task's change record costs, not by the most a record can cost", func(t *testing.T) {
		t.Parallel()

		// The record and both tasks at what their change records cost; counted at the most a
		// record can cost, the same size holds the record and one task.
		size := recordCost + 2*taskCost
		if most := rowDeleteMutations + ChangeRecordMutations(); (size-recordCost)/most != 1 {
			t.Fatalf("the save of %d holds %d tasks at %d a task, want 1", size, (size-recordCost)/most, most)
		}
		if got, err := deleteRound[roundProject](t, db, "exact", size, eventsource.ProcessEvent("test")); err != nil || !cmp.Equal(got, &DeleteLeft{Deleted: 2}) {
			t.Fatalf("the first round = %+v, %v, want {Deleted: 2}", got, err)
		}
		if state := readRoundState(t, db, "exact"); !state.record || state.tasks != 0 {
			t.Errorf("after the first round the record is there = %t with %d tasks, want it there with none", state.record, state.tasks)
		}
	})

	t.Run("a delete that fits one save deletes the record and says nothing is left", func(t *testing.T) {
		t.Parallel()

		got, err := deleteRound[roundProject](t, db, "small", 0, eventsource.ProcessEvent("test"))
		if err != nil {
			t.Fatalf("the delete error = %v", err)
		}
		if got != nil {
			t.Errorf("DeleteLeft() = %+v, want nil", got)
		}
		if state := readRoundState(t, db, "small"); state != (roundState{}) {
			t.Errorf("after the delete the database holds %+v of the project, want nothing", state)
		}
	})
}

// TestPatchSet_deleteTaken_roundsNestedToAnyDepth holds, over the emulator and through
// PatchSet.Buffer, a delete larger than one save of a project whose tasks sit 25 deep in
// three chains, every one holding the project, with the tests' stand-in for the
// database's refusal of a statement that joins too many tables (joinLimit) set so that
// the record reads four levels itself: the question counts every task across the pages
// it goes down from; the first round fills inside the deepest page and says what is
// left; every round deletes only tasks with nothing under them; the rounds delete as
// many as the question counted, and the record goes in the last. A save whose page at
// the record's own levels is refused after it took the rows under a page answers what
// it took, and the next request goes on.
func TestPatchSet_deleteTaken_roundsNestedToAnyDepth(t *testing.T) {
	t.Parallel()

	// chains stages the project in a database of its own, three chains of tasks as deep as
	// given, every one holding the project.
	chains := func(t *testing.T, project string, deep int) *spanner.Client {
		t.Helper()

		db := cascadeDatabase(t, "rounds-"+project)
		mutations := []*spanner.Mutation{spanner.InsertMap("CascadeProjects", map[string]any{"Id": project})}
		for _, chain := range []string{"a", "b", "c"} {
			for i := 1; i <= deep; i++ {
				row := map[string]any{"Id": fmt.Sprintf("%s-%s%02d", project, chain, i), "ProjectId": project}
				if i > 1 {
					row["ParentTaskId"] = fmt.Sprintf("%s-%s%02d", project, chain, i-1)
				}
				mutations = append(mutations, spanner.InsertMap("CascadeTasks", row))
			}
		}
		if _, err := db.Apply(t.Context(), mutations); err != nil {
			t.Fatalf("spanner.Client.Apply() error = %v", err)
		}

		return db
	}
	limit := joinLimit(8, false)
	orphans := func(t *testing.T, db *spanner.Client, project string) int64 {
		t.Helper()

		return roundCount(t, db, "SELECT COUNT(*) FROM CascadeTasks t WHERE t.ProjectId = @project AND t.ParentTaskId IS NOT NULL AND NOT EXISTS (SELECT 1 FROM CascadeTasks p WHERE p.Id = t.ParentTaskId)", project)
	}
	const recordCost = 1
	taskCost := rowDeleteMutations + ChangeRecordMutations(eventsource.ProcessEvent("test"))

	t.Run("a deep delete in rounds takes the deepest tasks first, and the record last", func(t *testing.T) {
		t.Parallel()

		db := chains(t, "tall", 25)

		asked := askRound[roundProject](t, db, "tall", limit)
		if want := "Delete this project and everything under it: 75 tasks. This can't be undone."; statusOf(asked) != http.StatusPreconditionRequired || httpio.Message(asked) != want {
			t.Fatalf("the question = %v (%q), want a 428 saying %q", asked, httpio.Message(asked), want)
		}

		// The record and ten tasks: the first round takes the three deepest levels and one
		// task of the next, inside the deepest page.
		size := recordCost + 10*taskCost
		remaining := int64(75)
		for round := 1; ; round++ {
			left, err := deleteRoundRefused[roundProject](t, db, "tall", size, eventsource.ProcessEvent("test"), limit)
			if err != nil {
				t.Fatalf("round %d: the delete error = %v", round, err)
			}
			state := readRoundState(t, db, "tall")
			if round == 1 {
				if diff := cmp.Diff(&DeleteLeft{Deleted: 10}, left); diff != "" {
					t.Errorf("round 1: DeleteLeft() (-want +got):\n%s", diff)
				}
				for _, deepest := range []string{"tall-a25", "tall-b25", "tall-c25", "tall-a23", "tall-c23"} {
					if got := roundCount(t, db, "SELECT COUNT(*) FROM CascadeTasks WHERE Id = @project", deepest); got != 0 {
						t.Errorf("round 1: %s is there, want it taken among the deepest", deepest)
					}
				}
			}
			if got := orphans(t, db, "tall"); got != 0 {
				t.Errorf("round %d: %d task(s) left name a task that is gone", round, got)
			}
			if left == nil {
				if state.record || state.tasks != 0 {
					t.Errorf("round %d: the last round left the record there = %t with %d tasks, want both gone", round, state.record, state.tasks)
				}

				break
			}
			if !state.record {
				t.Fatalf("round %d: the record is gone with %d tasks left to delete", round, state.tasks)
			}
			// What each round deleted comes off what the rounds before it left, so the rounds
			// together delete what the question counted.
			remaining -= left.Deleted
			if state.tasks != remaining {
				t.Errorf("round %d: DeleteLeft() = %+v with %d tasks in the database, want the %d the question counted less what the rounds deleted", round, left, state.tasks, remaining)
			}
			if round > 10 {
				t.Fatalf("round %d: the delete has not finished", round)
			}
		}
	})

	t.Run("a save whose page is refused after it took rows answers what it took", func(t *testing.T) {
		t.Parallel()

		db := chains(t, "late", 9)

		// The record reads four levels; its own pages are refused, so the save takes the 15
		// tasks under its frontier and stops at its deepest level.
		recordsPages := func(sql string) error {
			if err := limit(sql); err != nil {
				return err
			}
			tasksPage := strings.HasPrefix(sql, "SELECT u.k1 FROM (SELECT `CascadeTasks`.`Id` AS k1 FROM") || strings.HasPrefix(sql, "SELECT u.k1 FROM ((SELECT `CascadeTasks`.`Id` AS k1 FROM")
			if tasksPage && !strings.Contains(sql, "UNNEST") && !strings.HasSuffix(sql, fmt.Sprintf("LIMIT %d", takenPageRows)) {
				return limit(strings.Repeat(" AS w", 9))
			}

			return nil
		}
		left, err := deleteRoundRefused[roundProject](t, db, "late", 0, eventsource.ProcessEvent("test"), recordsPages)
		if err != nil {
			t.Fatalf("the refused round's error = %v, want none", err)
		}
		if diff := cmp.Diff(&DeleteLeft{Deleted: 15}, left); diff != "" {
			t.Errorf("the refused round's DeleteLeft() (-want +got):\n%s", diff)
		}
		if state := readRoundState(t, db, "late"); !state.record || state.tasks != 12 {
			t.Errorf("after the refused round the record is there = %t with %d tasks, want it there with 12", state.record, state.tasks)
		}
		if got := orphans(t, db, "late"); got != 0 {
			t.Errorf("after the refused round %d task(s) left name a task that is gone", got)
		}
		if left, err := deleteRoundRefused[roundProject](t, db, "late", 0, eventsource.ProcessEvent("test"), limit); err != nil || left != nil {
			t.Fatalf("the next round = %+v, %v, want nil", left, err)
		}
		if state := readRoundState(t, db, "late"); state.record || state.tasks != 0 {
			t.Errorf("once the delete finished the record is there = %t with %d tasks, want both gone", state.record, state.tasks)
		}
	})
}

// pacedKind is a kind of row a test hands a delete (TakenKind) that takes its rows one
// after another, asking DeleteSaveOver once it has taken one, as the library's kinds do.
// Its rows are what the saves that committed left: the rows a save takes go once its
// commit has landed (AfterCommit), since the emulator may abort a save's transaction and
// run its function again, and what the aborted run took is still there.
type pacedKind struct {
	rows int64
}

func (*pacedKind) Words() DeleteWords {
	return DeleteWords{One: "paced row", Many: "paced rows"}
}

func (k *pacedKind) Count(context.Context, ReadWriteTransaction) (int64, error) {
	return k.rows, nil
}

func (*pacedKind) Check(context.Context, ReadWriteTransaction) error {
	return nil
}

func (k *pacedKind) Take(ctx context.Context, txn ReadWriteTransaction, budget int, _ ...eventsource.EventSource) (rows int64, mutations int, more bool, err error) {
	for rows < k.rows {
		if rows > 0 && DeleteSaveOver(ctx) {
			break
		}
		if mutations+1 > budget {
			break
		}
		rows++
		mutations++
	}
	took := rows
	if err := AfterCommit(txn, func(context.Context) {
		k.rows -= took
	}); err != nil {
		return 0, 0, false, err
	}

	return rows, mutations, rows < k.rows, nil
}

// innerDelete is a kind a test hands a delete (TakenKind) whose Take buffers the delete of
// another project told to take what is under it, on the context Take was handed, as the
// library's kind of a tenant's groups deletes each group. left is what that delete left
// (PatchSet.DeleteLeft).
type innerDelete struct {
	project string
	left    *DeleteLeft
}

func (*innerDelete) Words() DeleteWords {
	return DeleteWords{One: "inner project", Many: "inner projects"}
}

func (*innerDelete) Count(context.Context, ReadWriteTransaction) (int64, error) {
	return 1, nil
}

func (*innerDelete) Check(context.Context, ReadWriteTransaction) error {
	return nil
}

func (k *innerDelete) Take(ctx context.Context, txn ReadWriteTransaction, _ int, eventSource ...eventsource.EventSource) (rows int64, mutations int, more bool, err error) {
	inner := NewPatchSet(NewMetadata[roundProject]()).SetPatchType(DeletePatchType).DeleteEverythingUnder()
	inner.SetKey("ID", k.project)
	if err := inner.Buffer(ctx, txn, eventSource...); err != nil {
		return 0, 0, false, err
	}
	k.left = inner.DeleteLeft()
	if k.left != nil {
		return k.left.Deleted, int(k.left.Deleted), true, nil
	}

	return 1, 1, false, nil
}

// deleteSave runs one request's delete of the record R with the id, told to take what is
// under it, in a save whose time is limit (PatchSet.deleteSaveTime; the transaction's for
// 0), handed kinds, and returns the delete's answer (PatchSet.DeleteLeft).
func deleteSave[R Resourcer](t *testing.T, db *spanner.Client, id string, limit time.Duration, kinds ...TakenKind) (*DeleteLeft, error) {
	t.Helper()

	var left *DeleteLeft
	err := NewSpannerClient(db).ExecuteFunc(t.Context(), func(ctx context.Context, txn ReadWriteTransaction) error {
		record := NewPatchSet(NewMetadata[R]()).SetPatchType(DeletePatchType).DeleteEverythingUnder().DeleteAlso(kinds...)
		record.SetKey("ID", id)
		record.deleteSaveTime = limit
		if err := record.Buffer(ctx, txn, eventsource.ProcessEvent("test")); err != nil {
			return err
		}
		left = record.DeleteLeft()

		return nil
	})

	return left, err
}

// TestPatchSet_deleteTaken_saveTime holds, over the emulator and through PatchSet.Buffer,
// a save's time limit beside its budget. A project with five tasks, two of them under
// another, at a limit of one nanosecond, loses the first page each save reads, the two
// nested tasks and then the three they were under, and goes in the third save; with no
// limit (below zero), and with the default, it goes in one save. A handed kind that asks
// DeleteSaveOver between its rows takes one a save at the same limit. A save whose time
// has passed before its first row takes the first page it reads rows from whole, on
// code's word and asking nothing alike. A save that reaches its first row within its time
// is ended by its time inside that page, and so is a page read after the first: no more
// of its rows is asked, and none is buffered once the save is over; nor is a page read
// after the first taken whole where it comes back with the time passed. A delete buffered
// inside a save, on the context a handed kind's Take is handed, runs on that save's
// clock: it takes its first page and says rows are left, where a clock of its own would
// have let it take all five and the project. The default limit is pinned.
func TestPatchSet_deleteTaken_saveTime(t *testing.T) {
	t.Parallel()

	db := cascadeDatabase(t, "delete-save-time")
	var mutations []*spanner.Mutation
	for _, project := range []string{"timed", "unlimited", "default", "handed", "handed-two", "inner", "outer", "unasked", "asked"} {
		mutations = append(mutations, spanner.InsertMap("CascadeProjects", map[string]any{"Id": project}))
		if project == "handed" || project == "handed-two" || project == "outer" {
			continue
		}
		for i := 1; i <= 5; i++ {
			task := map[string]any{"Id": fmt.Sprintf("%s-%d", project, i), "ProjectId": project}
			if i > 3 {
				// The fourth and fifth tasks sit under the first: two pages of tasks, the
				// nested two at level 1, then the three at level 0.
				task["ParentTaskId"] = project + "-1"
			}
			mutations = append(mutations, spanner.InsertMap("CascadeTasks", task))
		}
	}
	if _, err := db.Apply(t.Context(), mutations); err != nil {
		t.Fatalf("spanner.Client.Apply() error = %v", err)
	}

	t.Run("at a limit of one nanosecond each save takes the first page it reads rows from, and the project goes in the third", func(t *testing.T) {
		t.Parallel()

		for round, want := range []struct {
			left  *DeleteLeft
			state roundState
		}{
			{left: &DeleteLeft{Deleted: 2}, state: roundState{record: true, tasks: 3, comments: 0}},
			{left: &DeleteLeft{Deleted: 3}, state: roundState{record: true, tasks: 0, comments: 0}},
			{left: nil, state: roundState{record: false, tasks: 0, comments: 0}},
		} {
			left, err := deleteSave[roundProject](t, db, "timed", time.Nanosecond)
			if err != nil {
				t.Fatalf("round %d: the delete error = %v", round+1, err)
			}
			if diff := cmp.Diff(want.left, left); diff != "" {
				t.Errorf("round %d: DeleteLeft() (-want +got):\n%s", round+1, diff)
			}
			if state := readRoundState(t, db, "timed"); state != want.state {
				t.Errorf("round %d: the database holds %+v of the project, want %+v", round+1, state, want.state)
			}
		}
	})

	for _, tt := range []struct {
		name    string
		project string
		limit   time.Duration
	}{
		{name: "with no limit the project goes in one save", project: "unlimited", limit: -1},
		{name: "with the default limit the project goes in one save", project: "default", limit: 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if left, err := deleteSave[roundProject](t, db, tt.project, tt.limit); err != nil || left != nil {
				t.Fatalf("the delete = %+v, %v, want nil", left, err)
			}
			if state := readRoundState(t, db, tt.project); state != (roundState{}) {
				t.Errorf("after the delete the database holds %+v of the project, want nothing", state)
			}
		})
	}

	t.Run("a handed kind that asks DeleteSaveOver takes one row a save at a limit of one nanosecond", func(t *testing.T) {
		t.Parallel()

		paced := &pacedKind{rows: 3}
		for round, want := range []*DeleteLeft{{Deleted: 1}, {Deleted: 1}, nil} {
			left, err := deleteSave[roundProject](t, db, "handed", time.Nanosecond, paced)
			if err != nil {
				t.Fatalf("round %d: the delete error = %v", round+1, err)
			}
			if diff := cmp.Diff(want, left); diff != "" {
				t.Errorf("round %d: DeleteLeft() (-want +got):\n%s", round+1, diff)
			}
		}
		if paced.rows != 0 {
			t.Errorf("%d paced rows are left, want none", paced.rows)
		}
	})

	t.Run("a save over once a handed kind took its last row leaves the next handed kind to the next save", func(t *testing.T) {
		t.Parallel()

		first, next := &pacedKind{rows: 1}, &pacedKind{rows: 2}
		for round, want := range []*DeleteLeft{{Deleted: 1}, {Deleted: 1}, nil} {
			left, err := deleteSave[roundProject](t, db, "handed-two", time.Nanosecond, first, next)
			if err != nil {
				t.Fatalf("round %d: the delete error = %v", round+1, err)
			}
			if diff := cmp.Diff(want, left); diff != "" {
				t.Errorf("round %d: DeleteLeft() (-want +got):\n%s", round+1, diff)
			}
			if round == 0 && next.rows != 2 {
				t.Errorf("after the first save the next kind has %d rows, want its 2: its Take was called after the save was over", next.rows)
			}
		}
		if first.rows != 0 || next.rows != 0 {
			t.Errorf("paced rows left: %d and %d, want none", first.rows, next.rows)
		}
	})

	t.Run("a save on code's word at a limit of one nanosecond asks and takes the first page it reads rows from, and leaves the next page to the next save", func(t *testing.T) {
		t.Parallel()

		run := runTakingRefused(t, db, projectTree(), map[string]any{"Id": "asked"}, cascadeRows{asks: &takenAsks{question: true}}, true, 0, nil, takingHow{limit: time.Nanosecond})
		if run.err != nil {
			t.Fatalf("the save's error = %v", run.err)
		}
		if diff := cmp.Diff([]string{"ID=asked-4 trusted", "ID=asked-5 trusted"}, run.asked); diff != "" {
			t.Errorf("the rows the save asked about (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]string{"ID=asked-4 trusted", "ID=asked-5 trusted"}, run.made); diff != "" {
			t.Errorf("the rows the save deleted (-want +got):\n%s", diff)
		}
		if run.round == nil || run.round.finished {
			t.Errorf("the save's round = %+v, want one that did not finish", run.round)
		}
	})

	t.Run("a save that asks nothing takes the first page it reads rows from at a limit of one nanosecond and does not finish", func(t *testing.T) {
		t.Parallel()

		run := runTakingRefused(t, db, projectTree(), map[string]any{"Id": "unasked"}, cascadeRows{}, true, 0, nil, takingHow{unasked: true, limit: time.Nanosecond})
		if run.err != nil {
			t.Fatalf("the save's error = %v", run.err)
		}
		if diff := cmp.Diff([]string{"ID=unasked-4 trusted", "ID=unasked-5 trusted"}, run.made); diff != "" {
			t.Errorf("the rows the save deleted (-want +got):\n%s", diff)
		}
		if run.round == nil || run.round.finished {
			t.Errorf("the save's round = %+v, want one that did not finish", run.round)
		}
	})

	t.Run("a save that reaches its first row within its time is ended by its time inside that page", func(t *testing.T) {
		t.Parallel()

		run := runTakingRefused(t, db, projectTree(), map[string]any{"Id": "asked"}, cascadeRows{asks: &takenAsks{question: true}}, true, 0, nil, takingHow{passesAt: "ID=asked-4 trusted"})
		if run.err != nil {
			t.Fatalf("the save's error = %v", run.err)
		}
		if diff := cmp.Diff([]string{"ID=asked-4 trusted"}, run.asked); diff != "" {
			t.Errorf("the rows the save asked about (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]string{"ID=asked-4 trusted"}, run.made); diff != "" {
			t.Errorf("the rows the save deleted (-want +got):\n%s", diff)
		}
		if run.round == nil || run.round.finished {
			t.Errorf("the save's round = %+v, want one that did not finish", run.round)
		}
	})

	t.Run("the time passing inside a page read after the first cuts it", func(t *testing.T) {
		t.Parallel()

		run := runTakingRefused(t, db, projectTree(), map[string]any{"Id": "asked"}, cascadeRows{asks: &takenAsks{question: true}}, true, 0, nil, takingHow{passesAt: "ID=asked-1 trusted"})
		if run.err != nil {
			t.Fatalf("the save's error = %v", run.err)
		}
		if diff := cmp.Diff([]string{"ID=asked-4 trusted", "ID=asked-5 trusted", "ID=asked-1 trusted"}, run.asked); diff != "" {
			t.Errorf("the rows the save asked about (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]string{"ID=asked-4 trusted", "ID=asked-5 trusted"}, run.made); diff != "" {
			t.Errorf("the rows the save deleted (-want +got):\n%s", diff)
		}
		if run.round == nil || run.round.finished {
			t.Errorf("the save's round = %+v, want one that did not finish", run.round)
		}
	})

	t.Run("a page read after the first that comes back with the time passed is not taken whole", func(t *testing.T) {
		t.Parallel()

		run := runTakingRefused(t, db, projectTree(), map[string]any{"Id": "asked"}, cascadeRows{asks: &takenAsks{question: true}}, true, 0, nil, takingHow{passesAtPage: 2})
		if run.err != nil {
			t.Fatalf("the save's error = %v", run.err)
		}
		if diff := cmp.Diff([]string{"ID=asked-4 trusted", "ID=asked-5 trusted", "ID=asked-1 trusted"}, run.asked); diff != "" {
			t.Errorf("the rows the save asked about (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]string{"ID=asked-4 trusted", "ID=asked-5 trusted"}, run.made); diff != "" {
			t.Errorf("the rows the save deleted (-want +got):\n%s", diff)
		}
		if run.round == nil || run.round.finished {
			t.Errorf("the save's round = %+v, want one that did not finish", run.round)
		}
	})

	t.Run("a delete buffered inside a save runs on that save's clock", func(t *testing.T) {
		t.Parallel()

		inner := &innerDelete{project: "inner"}
		left, err := deleteSave[roundProject](t, db, "outer", time.Nanosecond, inner)
		if err != nil {
			t.Fatalf("the delete error = %v", err)
		}
		if diff := cmp.Diff(&DeleteLeft{Deleted: 2}, inner.left); diff != "" {
			t.Errorf("the inner delete's DeleteLeft() (-want +got):\n%s", diff)
		}
		if left == nil {
			t.Error("the outer delete's DeleteLeft() = nil, want the rows its save took")
		}
		if state := readRoundState(t, db, "inner"); !state.record || state.tasks != 3 {
			t.Errorf("after the save the inner project is there = %t with %d tasks, want it there with 3", state.record, state.tasks)
		}
	})

	t.Run("the default limit is pinned", func(t *testing.T) {
		t.Parallel()

		// The number is provisional: it is to be chosen from measurements of large deletes
		// on the real service, and changes only on purpose.
		if DefaultDeleteSaveTime != 30*time.Second {
			t.Errorf("DefaultDeleteSaveTime = %s, want 30s", DefaultDeleteSaveTime)
		}
	})
}
