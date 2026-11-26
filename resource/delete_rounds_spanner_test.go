package resource

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

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
// source as who made the change, and returns what the delete left (PatchSet.DeleteLeft).
func deleteRound[R Resourcer](t *testing.T, db *spanner.Client, id string, size int, source eventsource.EventSource) (*DeleteLeft, error) {
	t.Helper()

	var left *DeleteLeft
	err := NewSpannerClient(db).ExecuteFunc(t.Context(), func(ctx context.Context, txn ReadWriteTransaction) error {
		record := NewPatchSet(NewMetadata[R]()).SetPatchType(DeletePatchType).DeleteEverythingUnder()
		record.SetKey("ID", id)
		record.deleteSaveSize = size
		if err := record.Buffer(ctx, txn, source); err != nil {
			return err
		}
		left = record.DeleteLeft()

		return nil
	})

	return left, err
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
// request takes what fits, the deepest first, says what it deleted and what is left, and
// leaves the project standing, until the last takes the project too and says nothing.
// After every commit no task or comment that is left names a task that is gone, and
// every save stays within the size. Each task has the one change record of its delete.
// A rule that starts refusing between two rounds refuses the round, which commits
// nothing, while what earlier rounds took stays gone, and the delete finishes once the
// rule allows again. A delete that fits one save deletes the record and says nothing.
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
		rounds := []*DeleteLeft{{Deleted: 3, Left: 8}, {Deleted: 2, Left: 6}, {Deleted: 2, Left: 4}, {Deleted: 3, Left: 1}, nil}
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
		if got, err := deleteRound[ruledRoundProject](t, db, "ruled", size, eventsource.ProcessEvent("test")); err != nil || !cmp.Equal(got, &DeleteLeft{Deleted: 2, Left: 3}) {
			t.Fatalf("the first round = %+v, %v, want {Deleted: 2, Left: 3}", got, err)
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

		if got, err := deleteRound[ruledRoundProject](t, db, "ruled", size, eventsource.ProcessEvent("test")); err != nil || !cmp.Equal(got, &DeleteLeft{Deleted: 2, Left: 1}) {
			t.Fatalf("the round once the rule allows = %+v, %v, want {Deleted: 2, Left: 1}", got, err)
		}
		if got, err := deleteRound[ruledRoundProject](t, db, "ruled", size, eventsource.ProcessEvent("test")); err != nil || got != nil {
			t.Fatalf("the last round = %+v, %v, want nil", got, err)
		}
		if state := readRoundState(t, db, "ruled"); state.record || state.tasks != 0 {
			t.Errorf("once the delete finished the record is there = %t with %d tasks, want both gone", state.record, state.tasks)
		}
	})

	t.Run("each round's change records name that round's own request", func(t *testing.T) {
		t.Parallel()

		// The record, tracked, and one task: the first round takes a task and comes back
		// full. The second has room for two: it takes the other task, and with room left,
		// the record.
		record := rowDeleteMutations + ChangeRecordMutations(eventsource.ProcessEvent("first round"))
		if got, err := deleteRound[trackedRoundProject](t, db, "two", record+taskCost, eventsource.ProcessEvent("first round")); err != nil || !cmp.Equal(got, &DeleteLeft{Deleted: 1, Left: 1}) {
			t.Fatalf("the first round = %+v, %v, want {Deleted: 1, Left: 1}", got, err)
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
		if got, err := deleteRound[roundProject](t, db, "exact", size, eventsource.ProcessEvent("test")); err != nil || !cmp.Equal(got, &DeleteLeft{Deleted: 2, Left: 0}) {
			t.Fatalf("the first round = %+v, %v, want {Deleted: 2, Left: 0}", got, err)
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
