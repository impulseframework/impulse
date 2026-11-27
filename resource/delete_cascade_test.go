package resource

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/eventsource"
	"github.com/impulseframework/impulse/httpio"
)

// cascadeRows is how the tests of a delete that takes rows reach a kind's rows: the
// fields its key is held in, what the kind asks of its rows, and the rows it asked and
// deleted, in order.
type cascadeRows struct {
	// refused answers a row's check by the row's key: the error, and whether it is the
	// row's rule that refuses.
	refused map[string]cascadeRefusal
	// denied is what the caller's grants on the kind answer; nil passes.
	denied error
	// asks is what the kind asks of its rows; nil asks nothing.
	asks  *takenAsks
	asked *[]string
	made  *[]string
}

// cascadeRefusal is what a row's check answers.
type cascadeRefusal struct {
	err    error
	byRule bool
}

func (cascadeRows) columns(DBType) map[string]takenColumn {
	return map[string]takenColumn{
		"Id":        {field: "ID", typ: reflect.TypeFor[string]()},
		"ProjectId": {field: "ProjectID", typ: reflect.TypeFor[string]()},
	}
}

func (r cascadeRows) row(fields []accesstypes.Field, values []any, caller *Caller) takenRow {
	name := make([]string, 0, len(fields))
	for i, field := range fields {
		name = append(name, fmt.Sprintf("%s=%v", field, values[i]))
	}
	who := "trusted"
	if !caller.IsZero() {
		who = "armed"
	}
	row := &cascadeRow{name: strings.Join(name, ",") + " " + who, asked: r.asked, made: r.made}
	if refusal, ok := r.refused[fmt.Sprint(values...)]; ok {
		row.refusal = refusal
	}

	return row
}

func (r cascadeRows) kind(*Caller) takenKind {
	return &cascadeKind{denied: r.denied, asks: r.asks}
}

func (cascadeRows) tracked() bool {
	return false
}

// cascadeKind is the delete of a test kind's rows as one.
type cascadeKind struct {
	denied error
	asks   *takenAsks
}

func (k *cascadeKind) checkPermissions(context.Context, DBType) error {
	return k.denied
}

func (k *cascadeKind) takenAsks(DBType) (*takenAsks, error) {
	if k.asks == nil {
		return &takenAsks{}, nil
	}

	return k.asks, nil
}

// cascadeRow is the delete of one row a test's delete takes. Its check answers its
// refusal, and refuses as the grant's condition does where a term the page read of it
// answered false.
type cascadeRow struct {
	name    string
	refusal cascadeRefusal
	asked   *[]string
	made    *[]string
}

func (r *cascadeRow) checkTaken(_ context.Context, _ ReadWriteTransaction, _ *takenAsks, answers []spanner.NullBool) (byRule bool, err error) {
	if r.asked != nil {
		*r.asked = append(*r.asked, r.name)
	}
	if r.refusal.err != nil {
		return r.refusal.byRule, r.refusal.err
	}
	for _, answer := range answers {
		if !answer.Valid || !answer.Bool {
			return false, httpio.NewForbiddenMessage("the condition does not hold for " + r.name)
		}
	}

	return false, nil
}

func (r *cascadeRow) bufferTaken(context.Context, ReadWriteTransaction, ...eventsource.EventSource) error {
	*r.made = append(*r.made, r.name)

	return nil
}

// projectTree describes a record whose delete takes three kinds: its tasks, which name
// it and may be under another task, the comments on its tasks, and its labels, which
// name it by its code and not by its key.
func projectTree() *DeleteTree {
	return &DeleteTree{
		Record: "CascadeProjects",
		Words:  DeleteWords{One: "project", Many: "projects"},
		Key:    []string{"Id"},
		Taken: []DeleteKind{
			{
				Resource: "CascadeComments",
				Words:    DeleteWords{One: "comment", Many: "comments"},
				Key:      []string{"Id"},
				Paths: []DeletePath{{
					{Columns: []string{"TaskId"}, Table: "CascadeTasks", References: []string{"Id"}},
					{Columns: []string{"ProjectId"}, Table: "CascadeProjects", References: []string{"Id"}},
				}},
			},
			{
				Resource: "CascadeLabels",
				Words:    DeleteWords{One: "label", Many: "labels"},
				Key:      []string{"Id"},
				Paths:    []DeletePath{{{Columns: []string{"ProjectCode"}, Table: "CascadeProjects", References: []string{"Code"}}}},
			},
			{
				Resource: "CascadeTasks",
				Words:    DeleteWords{One: "task", Many: "tasks"},
				Key:      []string{"Id"},
				Paths:    []DeletePath{{{Columns: []string{"ProjectId"}, Table: "CascadeProjects", References: []string{"Id"}}}},
				Within:   []DeleteLink{{Columns: []string{"ParentTaskId"}, Table: "CascadeTasks", References: []string{"Id"}}},
			},
		},
	}
}

// cascadeFolderTree describes a folder whose delete takes the folders in it, each of which may
// hold folders of its own: the record's own kind, nested under itself.
func cascadeFolderTree() *DeleteTree {
	return &DeleteTree{
		Record: "CascadeFolders", Words: DeleteWords{One: "folder", Many: "folders"}, Key: []string{"Id"},
		Taken: []DeleteKind{{
			Resource: "CascadeFolders", Words: DeleteWords{One: "folder", Many: "folders"}, Key: []string{"Id"},
			Paths: []DeletePath{{{Columns: []string{"ParentId"}, Table: "CascadeFolders", References: []string{"Id"}}}},
		}},
	}
}

// deepSectionsTree describes a project whose delete takes its sections, each of which may
// sit under another section of the same project: a table keyed by several columns that
// nests.
func deepSectionsTree() *DeleteTree {
	return &DeleteTree{
		Record: "CascadeProjects", Words: DeleteWords{One: "project", Many: "projects"}, Key: []string{"Id"},
		Taken: []DeleteKind{{
			Resource: "CascadeSections", Words: DeleteWords{One: "section", Many: "sections"}, Key: []string{"ProjectId", "Id"},
			Paths:  []DeletePath{{{Columns: []string{"ProjectId"}, Table: "CascadeProjects", References: []string{"Id"}}}},
			Within: []DeleteLink{{Columns: []string{"ProjectId", "ParentId"}, Table: "CascadeSections", References: []string{"ProjectId", "Id"}}},
		}},
	}
}

// deepRepliesTree describes a project whose delete takes the replies on its tasks, each
// of which may answer another reply in the same lane: a table that nests by a link to a
// column outside its key.
func deepRepliesTree() *DeleteTree {
	return &DeleteTree{
		Record: "CascadeProjects", Words: DeleteWords{One: "project", Many: "projects"}, Key: []string{"Id"},
		Taken: []DeleteKind{{
			Resource: "CascadeReplies", Words: DeleteWords{One: "reply", Many: "replies"}, Key: []string{"Id"},
			Paths: []DeletePath{{
				{Columns: []string{"TaskId"}, Table: "CascadeTasks", References: []string{"Id"}},
				{Columns: []string{"ProjectId"}, Table: "CascadeProjects", References: []string{"Id"}},
			}},
			Within: []DeleteLink{{Columns: []string{"AnswersId", "Lane"}, Table: "CascadeReplies", References: []string{"Id", "Lane"}}},
		}},
	}
}

// assignedTree is projectTree with the assignments on the project's tasks in place of its
// comments and labels. An assignment is a tenant's by a column of its own and goes on the
// permission the request was let in by (WithinReach), so one in a tenant that permission
// does not reach refuses the project's delete.
func assignedTree() *DeleteTree {
	tree := projectTree()
	tree.Taken = []DeleteKind{
		{
			Resource: "CascadeAssignments", Words: DeleteWords{One: "assignment", Many: "assignments"}, Key: []string{"Id"},
			Paths: tree.Taken[0].Paths, Tenant: &DomainBindingData{Column: "TenantId"}, WithinReach: true,
		},
		tree.Taken[2],
	}

	return tree
}

// threadedTree describes a project whose delete takes its tasks, each of which may be
// under another task, the replies on them, each of which may answer another reply, and
// the reactions to the replies: two tables that nest, one under the other, with a kind
// under the lower.
func threadedTree() *DeleteTree {
	tree := projectTree()
	tree.Taken = []DeleteKind{
		{
			Resource: "CascadeReactions", Words: DeleteWords{One: "reaction", Many: "reactions"}, Key: []string{"Id"},
			Paths: []DeletePath{{
				{Columns: []string{"ReplyId"}, Table: "CascadeReplies", References: []string{"Id"}},
				{Columns: []string{"TaskId"}, Table: "CascadeTasks", References: []string{"Id"}},
				{Columns: []string{"ProjectId"}, Table: "CascadeProjects", References: []string{"Id"}},
			}},
		},
		deepRepliesTree().Taken[0],
		tree.Taken[2],
	}

	return tree
}

// invoicedTree is projectTree with two kinds that refuse the project's delete: an
// invoice, which names a task and may name a second one it was checked against, and a
// timesheet, which names a task.
func invoicedTree() *DeleteTree {
	underTask := func(column string) DeletePath {
		return DeletePath{
			{Columns: []string{column}, Table: "CascadeTasks", References: []string{"Id"}},
			{Columns: []string{"ProjectId"}, Table: "CascadeProjects", References: []string{"Id"}},
		}
	}
	tree := projectTree()
	tree.RefusedBy = []DeleteKind{
		{
			Resource: "CascadeInvoices",
			Words:    DeleteWords{One: "invoice", Many: "invoices"},
			Key:      []string{"Id"},
			Paths:    []DeletePath{underTask("TaskId"), underTask("CheckedTaskId")},
		},
		{
			Resource: "CascadeTimesheets",
			Words:    DeleteWords{One: "timesheet", Many: "timesheets"},
			Key:      []string{"Id"},
			Paths:    []DeletePath{underTask("TaskId")},
		},
	}

	return tree
}

// TestRecordedDelete_refuseRow pins what a kind or a row that may not go answers the
// whole delete with: its rule's own sentence where its rule refuses; where the person
// may not delete it, or may not reach it, a 403 that names the kind if they may list it
// and says only that there are records if they may not; and anything else as the
// failure it is.
func TestRecordedDelete_refuseRow(t *testing.T) {
	t.Parallel()

	tree := projectTree()
	tasks := &tree.Taken[2]

	tests := []struct {
		name        string
		refusal     cascadeRefusal
		caller      *Caller
		wantStatus  int
		wantMessage string
		wantFailure bool
	}{
		{
			name:        "a row its rule refuses answers in the rule's sentence",
			refusal:     cascadeRefusal{byRule: true, err: httpio.NewBadRequestMessage("A task that is being worked on can't be deleted.")},
			wantStatus:  http.StatusBadRequest,
			wantMessage: "A task that is being worked on can't be deleted.",
		},
		{
			name:        "a row the person may not delete",
			refusal:     cascadeRefusal{err: httpio.NewForbiddenMessage("user (u1) does not have (Delete) on CascadeTasks")},
			caller:      whoLists(nil),
			wantStatus:  http.StatusForbidden,
			wantMessage: "This project has tasks that you can't delete, so it can't be deleted.",
		},
		{
			name:        "a row the person may not reach",
			refusal:     cascadeRefusal{err: httpio.NewNotFoundMessage("CascadeTasks t9 not found")},
			caller:      whoLists(nil),
			wantStatus:  http.StatusForbidden,
			wantMessage: "This project has tasks that you can't delete, so it can't be deleted.",
		},
		{
			name:        "a row of a kind the person may not list is not named",
			refusal:     cascadeRefusal{err: httpio.NewForbiddenMessage("user (u1) does not have (Delete) on CascadeTasks")},
			caller:      whoLists(accesstypes.Decisions{"CascadeTasks": accesstypes.Denied()}),
			wantStatus:  http.StatusForbidden,
			wantMessage: "This project has records under it that you can't delete, so it can't be deleted.",
		},
		{
			name:        "a check that fails is a failure, not a refusal",
			refusal:     cascadeRefusal{err: errors.New("the database is away")},
			wantFailure: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			asked := &recordedDelete{tree: tree, key: map[string]any{"Id": "p1"}, caller: tt.caller}
			err := asked.refuseRow(t.Context(), tasks, tt.refusal.byRule, tt.refusal.err)
			if tt.wantFailure {
				if err == nil || httpio.Message(err) != "" {
					t.Fatalf("refuseRow() error = %v, want a failure with no sentence for the person", err)
				}

				return
			}
			if got := statusOf(err); got != tt.wantStatus {
				t.Errorf("refuseRow() status = %d, want %d (%v)", got, tt.wantStatus, err)
			}
			if got := httpio.Message(err); got != tt.wantMessage {
				t.Errorf("refuseRow() message = %q, want %q", got, tt.wantMessage)
			}
		})
	}
}

// statusOf is the status a refusal answers with, 0 for an error that is no refusal.
func statusOf(err error) int {
	for status, has := range map[int]func(error) bool{
		http.StatusBadRequest:           httpio.HasBadRequest,
		http.StatusForbidden:            httpio.HasForbidden,
		http.StatusConflict:             httpio.HasConflict,
		http.StatusPreconditionRequired: httpio.HasPreconditionRequired,
	} {
		if has(err) {
			return status
		}
	}

	return 0
}

// TestRecordedDelete_question pins what a person is asked before a delete takes the
// rows under a record: the record's kind, then each kind counted with its rows at every
// level, the kind nearest the record first; a kind the person may not list is not counted for them, and
// the sentence then says there are records they can't see; with no kind they may list it
// only asks. The kinds code handed the delete follow the description's, each told to
// whoever asks. A trusted kind is counted for a person who may not list it, and an armed
// kind beside it is not.
func TestRecordedDelete_question(t *testing.T) {
	t.Parallel()

	tree := projectTree()
	levels := &takenLevels{deepest: 1, rows: map[*DeleteKind][]int64{&tree.Taken[2]: {1, 2}, &tree.Taken[0]: {1, 0}, &tree.Taken[1]: {0}}}
	trusted := projectTree()
	trusted.Taken[2].Trusted = true
	trustedLevels := &takenLevels{deepest: 1, rows: map[*DeleteKind][]int64{&trusted.Taken[2]: {1, 2}, &trusted.Taken[0]: {1, 0}, &trusted.Taken[1]: {0}}}
	handed := []kindRows{
		{kind: &DeleteKind{Words: DeleteWords{One: "role", Many: "roles"}, Trusted: true}, rows: 5},
		{kind: &DeleteKind{Words: DeleteWords{One: "invitation", Many: "invitations"}, Trusted: true}, rows: 1},
	}
	listsNothing := whoLists(accesstypes.Decisions{"CascadeComments": accesstypes.Denied(), "CascadeTasks": accesstypes.Denied()})

	tests := []struct {
		name   string
		tree   *DeleteTree
		levels *takenLevels
		handed []kindRows
		caller *Caller
		want   string
	}{
		{
			name: "a trusted call is told every kind",
			want: "Delete this project and everything under it: 3 tasks, 1 comment. This can't be undone.",
		},
		{
			name:   "a person who may list every kind",
			caller: whoLists(nil),
			want:   "Delete this project and everything under it: 3 tasks, 1 comment. This can't be undone.",
		},
		{
			name:   "a kind the person may not list is not counted for them",
			caller: whoLists(accesstypes.Decisions{"CascadeComments": accesstypes.Denied()}),
			want:   "Delete this project and everything under it: 3 tasks, and records you can't see. This can't be undone.",
		},
		{
			name:   "a person who may list none of the kinds is only asked",
			caller: listsNothing,
			want:   "Delete this project and everything under it? This can't be undone.",
		},
		{
			name:   "the kinds code handed the delete follow the description's",
			handed: handed,
			caller: whoLists(nil),
			want:   "Delete this project and everything under it: 3 tasks, 1 comment, 5 roles, 1 invitation. This can't be undone.",
		},
		{
			name:   "the kinds code handed the delete are told to a person who may list none of the description's",
			handed: handed,
			caller: listsNothing,
			want:   "Delete this project and everything under it: 5 roles, 1 invitation, and records you can't see. This can't be undone.",
		},
		{
			name: "a trusted kind is counted for a person who may not list it, and an armed kind beside it is not", tree: trusted, levels: trustedLevels,
			caller: listsNothing,
			want:   "Delete this project and everything under it: 3 tasks, and records you can't see. This can't be undone.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tree, levels := tree, levels
			if tt.tree != nil {
				tree, levels = tt.tree, tt.levels
			}
			asked := &recordedDelete{tree: tree, key: map[string]any{"Id": "p1"}, caller: tt.caller}
			got, err := asked.question(t.Context(), levels, tt.handed)
			if err != nil {
				t.Fatalf("question() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("question() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestDecoder_readsWhatARemoveSays pins a remove operation's value: "everythingUnder":
// true makes the delete one that takes the rows under its record, a remove that says
// nothing of it takes the record's row alone, and a value that is not an object, or
// says it with anything but true or false, is a 400.
func TestDecoder_readsWhatARemoveSays(t *testing.T) {
	t.Parallel()

	rSet, err := NewSet[tenancyResource, tenancyCreateRequest](accesstypes.Create, accesstypes.Update, accesstypes.Delete)
	if err != nil {
		t.Fatalf("NewSet() error = %v", err)
	}
	decoder, err := NewDecoder[tenancyResource, tenancyCreateRequest](rSet)
	if err != nil {
		t.Fatalf("NewDecoder() error = %v", err)
	}

	tests := []struct {
		name    string
		value   string
		want    bool
		wantBad bool
	}{
		{name: "a remove that says so", value: `{"everythingUnder":true}`, want: true},
		{name: "a remove that says no", value: `{"everythingUnder":false}`},
		{name: "a remove with the value an operation without one is given", value: `{}`},
		{name: "a remove with no value at all"},
		{name: "a remove whose value says something else", value: `{"name":"dock four"}`},
		{name: "a remove that says it with a word", value: `{"everythingUnder":"yes"}`, wantBad: true},
		{name: "a remove whose value is a list", value: `[true]`, wantBad: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for name, decode := range map[string]func(oper *Operation) (*PatchSet[tenancyResource], error){
				"DecodeOperation": func(oper *Operation) (*PatchSet[tenancyResource], error) {
					return decoder.DecodeOperation(oper, renderStubPermissions{}, accesstypes.GlobalScope())
				},
				"DecodeOperationWithoutPermissions": decoder.DecodeOperationWithoutPermissions,
			} {
				oper := &Operation{Type: OperationDelete, Req: &http.Request{Body: io.NopCloser(strings.NewReader(tt.value))}}
				patchSet, err := decode(oper)
				if tt.wantBad {
					if err == nil || !httpio.HasBadRequest(err) {
						t.Fatalf("%s() error = %v, want a 400", name, err)
					}

					continue
				}
				if err != nil {
					t.Fatalf("%s() error = %v", name, err)
				}
				if patchSet.everythingUnder != tt.want {
					t.Errorf("%s(): the delete takes everything under its record = %v, want %v", name, patchSet.everythingUnder, tt.want)
				}
			}
		})
	}
}

// Test_takenQuery pins a kind's path statement as Spanner takes it: the kind's rows read
// under their table's name, under the record by the kind's ways at the level; a kind
// three steps under the record names no row above it, only the record's key; above level
// 0 the rows the record's key reaches by a declared way are left to level 0; and a page
// reads each row's key and the answers asked of it, in key order, after the rows of the
// pages before it.
func Test_takenQuery(t *testing.T) {
	t.Parallel()

	tree := projectTree()
	replies := DeleteKind{
		Resource: "CascadeReplies",
		Words:    DeleteWords{One: "reply", Many: "replies"},
		Key:      []string{"Id"},
		Paths: []DeletePath{{
			{Columns: []string{"CommentId"}, Table: "CascadeComments", References: []string{"Id"}},
			{Columns: []string{"TaskId"}, Table: "CascadeTasks", References: []string{"Id"}},
			{Columns: []string{"ProjectId"}, Table: "CascadeProjects", References: []string{"Id"}},
		}},
	}
	query := &takenQuery{tree: tree, key: map[string]any{"Id": "p1"}}

	// With the deepest level 0, a level leaves nothing out.
	statement, ok := query.count(&replies, 0, 0)
	if !ok {
		t.Fatal("count() of the replies wrote no statement")
	}
	if want := "SELECT COUNT(*) FROM `CascadeReplies` WHERE (EXISTS (SELECT 1 FROM `CascadeComments` AS w0_1, `CascadeTasks` AS w0_2 " +
		"WHERE w0_1.`Id` = `CascadeReplies`.`CommentId` AND w0_2.`Id` = w0_1.`TaskId` AND w0_2.`ProjectId` = @d1))"; statement.SQL != want {
		t.Errorf("count() of the replies =\n%s\nwant\n%s", statement.SQL, want)
	}
	if diff := cmp.Diff([]any{"p1"}, statement.Args); diff != "" {
		t.Errorf("count() of the replies names values other than the record's key (-want +got):\n%s", diff)
	}

	// With the deepest level 2, a level below it leaves out what the next level finds, and
	// level 1 compares nothing of the task's own project.
	tasks, labels := &tree.Taken[2], &tree.Taken[1]
	asks := &takenAsks{groups: [][]accesstypes.Resource{{"CascadeTasks"}}, terms: []string{"(CascadeTasks.Title != @_c1)"}, params: map[string]any{"_c1": "secret"}}
	page, ok := query.page(tasks, 1, 2, asks, takenPageRows, takenPageRows)
	if !ok {
		t.Fatal("page() of the tasks at level 1 wrote no read")
	}
	if want := "SELECT `CascadeTasks`.`Id`, (CascadeTasks.Title != @_c1) AS a1 FROM `CascadeTasks` " +
		"WHERE (EXISTS (SELECT 1 FROM `CascadeTasks` AS w0_1 WHERE w0_1.`Id` = `CascadeTasks`.`ParentTaskId` AND w0_1.`ProjectId` = @d1)) " +
		"AND NOT COALESCE((EXISTS (SELECT 1 FROM `CascadeTasks` AS w1_1, `CascadeTasks` AS w1_2 WHERE w1_1.`Id` = `CascadeTasks`.`ParentTaskId` " +
		"AND w1_2.`Id` = w1_1.`ParentTaskId` AND w1_2.`ProjectId` = @d2)), FALSE) ORDER BY `CascadeTasks`.`Id` LIMIT 1000 OFFSET 1000"; page.SQL != want {
		t.Errorf("page() of the tasks at level 1 =\n%s\nwant\n%s", page.SQL, want)
	}
	if diff := cmp.Diff(map[string]any{"_c1": "secret", "d1": "p1", "d2": "p1"}, page.spannerStatement().Params); diff != "" {
		t.Errorf("page() of the tasks at level 1 values (-want +got):\n%s", diff)
	}
	if page, ok := query.page(tasks, 2, 2, nil, takenPageRows, 0); !ok || strings.Contains(page.SQL, "NOT") {
		t.Errorf("page() of the tasks at the deepest level = %q (written %t), want a read that leaves nothing out", page.SQL, ok)
	}
	statement, ok = query.count(tasks, 0, 2)
	if !ok {
		t.Fatal("count() of the tasks at level 0 wrote no statement")
	}
	if want := "SELECT COUNT(*) FROM `CascadeTasks` WHERE ((`CascadeTasks`.`ProjectId` = @d1)) " +
		"AND NOT COALESCE((EXISTS (SELECT 1 FROM `CascadeTasks` AS w1_1 WHERE w1_1.`Id` = `CascadeTasks`.`ParentTaskId` AND w1_1.`ProjectId` = @d2)), FALSE)"; statement.SQL != want {
		t.Errorf("count() of the tasks at level 0 =\n%s\nwant\n%s", statement.SQL, want)
	}
	if statement, ok := query.count(tasks, 0, 0); !ok || strings.Contains(statement.SQL, "NOT") {
		t.Errorf("count() of the tasks at level 0 with the deepest level 0 = %q (written %t), want a count that leaves nothing out", statement.SQL, ok)
	}
	for _, deepest := range []int{0, 2} {
		if statement, ok := query.count(labels, 0, deepest); !ok || strings.Contains(statement.SQL, "NOT") {
			t.Errorf("count() of the labels at level 0 with the deepest level %d = %q (written %t), want a count that leaves nothing out", deepest, statement.SQL, ok)
		}
	}
	if _, ok := query.count(labels, 1, 2); ok {
		t.Error("count() of the labels at level 1 wrote a statement, want none: no way of theirs has a place for a link to a row of the same table")
	}

	// A page's statements compare the last step of each way with the page's keys, bound
	// once by name: a key of one column against the keys themselves.
	ids := []string{"t1", "t2"}
	underTasks := &takenQuery{tree: tree, key: map[string]any{"Id": "p1"}, under: &takenUnder{table: "CascadeTasks", key: []string{"Id"}, ids: ids}}
	statement, ok = underTasks.count(tasks, 0, 1)
	if !ok {
		t.Fatal("count() of the tasks under a page of tasks wrote no statement")
	}
	if want := "SELECT COUNT(*) FROM `CascadeTasks` WHERE ((`CascadeTasks`.`ParentTaskId` IN UNNEST(@ids))) " +
		"AND NOT COALESCE((EXISTS (SELECT 1 FROM `CascadeTasks` AS w1_1 WHERE w1_1.`Id` = `CascadeTasks`.`ParentTaskId` AND w1_1.`ParentTaskId` IN UNNEST(@ids))), FALSE)"; statement.SQL != want {
		t.Errorf("count() of the tasks under a page of tasks =\n%s\nwant\n%s", statement.SQL, want)
	}
	if diff := cmp.Diff(map[string]any{"ids": ids}, statement.spannerStatement().Params); diff != "" {
		t.Errorf("count() of the tasks under a page of tasks values (-want +got):\n%s", diff)
	}
	comments := &tree.Taken[0]
	statement, ok = underTasks.count(comments, 0, 0)
	if !ok {
		t.Fatal("count() of the comments under a page of tasks wrote no statement")
	}
	if want := "SELECT COUNT(*) FROM `CascadeComments` WHERE ((`CascadeComments`.`TaskId` IN UNNEST(@ids)))"; statement.SQL != want {
		t.Errorf("count() of the comments under a page of tasks =\n%s\nwant\n%s", statement.SQL, want)
	}
	page, ok = underTasks.page(tasks, 1, 1, asks, takenPageRows, 0)
	if !ok {
		t.Fatal("page() of the tasks under a page of tasks wrote no read")
	}
	if want := "SELECT `CascadeTasks`.`Id`, (CascadeTasks.Title != @_c1) AS a1 FROM `CascadeTasks` " +
		"WHERE (EXISTS (SELECT 1 FROM `CascadeTasks` AS w0_1 WHERE w0_1.`Id` = `CascadeTasks`.`ParentTaskId` AND w0_1.`ParentTaskId` IN UNNEST(@ids))) " +
		"ORDER BY `CascadeTasks`.`Id` LIMIT 1000"; page.SQL != want {
		t.Errorf("page() of the tasks under a page of tasks =\n%s\nwant\n%s", page.SQL, want)
	}
	if diff := cmp.Diff(map[string]any{"_c1": "secret", "ids": ids}, page.spannerStatement().Params); diff != "" {
		t.Errorf("page() of the tasks under a page of tasks values (-want +got):\n%s", diff)
	}
	// Under a crossed page, a comment counts only with a link of the tasks between it and
	// the page: the comments on the page's own tasks were counted where the page was read.
	crossed := &takenQuery{tree: tree, key: map[string]any{"Id": "p1"}, under: &takenUnder{table: "CascadeTasks", key: []string{"Id"}, ids: ids, crossed: true}}
	if statement, ok := crossed.count(comments, 0, 1); ok {
		t.Errorf("count() of the comments at level 0 under a crossed page = %q, want none", statement.SQL)
	}
	statement, ok = crossed.count(comments, 1, 1)
	if !ok {
		t.Fatal("count() of the comments at level 1 under a crossed page wrote no statement")
	}
	if want := "SELECT COUNT(*) FROM `CascadeComments` WHERE (EXISTS (SELECT 1 FROM `CascadeTasks` AS w0_1 WHERE w0_1.`Id` = `CascadeComments`.`TaskId` AND w0_1.`ParentTaskId` IN UNNEST(@ids)))"; statement.SQL != want {
		t.Errorf("count() of the comments at level 1 under a crossed page =\n%s\nwant\n%s", statement.SQL, want)
	}
	// Where a page's terms name a value ids, the page's keys take another name.
	named := &takenAsks{terms: []string{"(CascadeTasks.Title != @ids)"}, params: map[string]any{"ids": "secret"}}
	page, ok = underTasks.page(tasks, 0, 0, named, takenPageRows, 0)
	if !ok {
		t.Fatal("page() of the tasks with a term that names ids wrote no read")
	}
	if want := "SELECT `CascadeTasks`.`Id`, (CascadeTasks.Title != @ids) AS a1 FROM `CascadeTasks` WHERE ((`CascadeTasks`.`ParentTaskId` IN UNNEST(@ids2))) ORDER BY `CascadeTasks`.`Id` LIMIT 1000"; page.SQL != want {
		t.Errorf("page() of the tasks with a term that names ids =\n%s\nwant\n%s", page.SQL, want)
	}
	if diff := cmp.Diff(map[string]any{"ids": "secret", "ids2": ids}, page.spannerStatement().Params); diff != "" {
		t.Errorf("page() of the tasks with a term that names ids values (-want +got):\n%s", diff)
	}

	// A key of several columns: the step's columns, in the key's order, as one value
	// against the page's keys as rows.
	sections := deepSectionsTree()
	keys := []struct{ C1, C2 string }{{"p1", "s1"}}
	underSections := &takenQuery{tree: sections, key: map[string]any{"Id": "p1"}, under: &takenUnder{table: "CascadeSections", key: []string{"ProjectId", "Id"}, ids: keys}}
	statement, ok = underSections.count(&sections.Taken[0], 0, 0)
	if !ok {
		t.Fatal("count() of the sections under a page of sections wrote no statement")
	}
	if want := "SELECT COUNT(*) FROM `CascadeSections` WHERE ((STRUCT(`CascadeSections`.`ProjectId` AS C1, `CascadeSections`.`ParentId` AS C2) IN UNNEST(@ids)))"; statement.SQL != want {
		t.Errorf("count() of the sections under a page of sections =\n%s\nwant\n%s", statement.SQL, want)
	}
	if diff := cmp.Diff(map[string]any{"ids": keys}, statement.spannerStatement().Params); diff != "" {
		t.Errorf("count() of the sections under a page of sections values (-want +got):\n%s", diff)
	}

	// A link to a column outside the key: the page's own row is read, and its key
	// compared with the page's.
	answers := deepRepliesTree()
	underReplies := &takenQuery{tree: answers, key: map[string]any{"Id": "p1"}, under: &takenUnder{table: "CascadeReplies", key: []string{"Id"}, ids: []string{"r1"}}}
	page, ok = underReplies.page(&answers.Taken[0], 0, 0, nil, takenPageRows, 0)
	if !ok {
		t.Fatal("page() of the replies under a page of replies wrote no read")
	}
	if want := "SELECT `CascadeReplies`.`Id` FROM `CascadeReplies` WHERE (EXISTS (SELECT 1 FROM `CascadeReplies` AS w0_r " +
		"WHERE `CascadeReplies`.`AnswersId` = w0_r.`Id` AND `CascadeReplies`.`Lane` = w0_r.`Lane` AND w0_r.`Id` IN UNNEST(@ids))) " +
		"ORDER BY `CascadeReplies`.`Id` LIMIT 1000"; page.SQL != want {
		t.Errorf("page() of the replies under a page of replies =\n%s\nwant\n%s", page.SQL, want)
	}
}
