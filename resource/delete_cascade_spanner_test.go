package resource

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/eventsource"
	"github.com/impulseframework/impulse/httpio"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// cascadeDatabase makes a database with the taken delete's fixture on the shared
// emulator.
func cascadeDatabase(t *testing.T, name string) *spanner.Client {
	t.Helper()

	container := spannerEmulator(t)
	db, err := container.CreateDatabase(context.Background(), name)
	if err != nil {
		t.Fatalf("initiator.SpannerContainer.CreateDatabase() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.DropDatabase(context.Background()); err != nil {
			t.Error(err)
		}
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := db.MigrateUp("file://testdata/deletecascade/schema"); err != nil {
		t.Fatalf("initiator.SpannerDB.MigrateUp() error = %v", err)
	}

	return db.Client
}

// cascadeRun is what one delete that takes rows did: what it answered, the round it
// took, the rows it asked about and deleted, in order, the handed kinds it counted, how
// many count, page and by-tenant statements it sent, and the text of each, in order.
type cascadeRun struct {
	err        error
	round      *takenRound
	asked      []string
	made       []string
	counted    []string
	counts     int
	pages      int
	byTenant   int
	statements []string
}

// handedKind is a kind of row a test hands the delete (TakenKind): rows of it under the
// record, each costing cost mutations, and what its check answers. It notes its check
// with the rows asked about, its count with the kinds counted and what it took with the
// rows deleted. A kind that drains has fewer rows once it has taken some, as a committed
// save leaves it. A kind with an answer takes what the answer gives, whatever its rows
// and the budget.
type handedKind struct {
	words   DeleteWords
	rows    int64
	cost    int
	drains  bool
	refused error
	answer  *handedTake
	asked   *[]string
	made    *[]string
	counted *[]string
}

// handedTake is what a handed kind's Take answers in place of what its rows and the
// budget give: a kind that makes room without taking a row, or one that cannot take
// anything.
type handedTake struct {
	rows      int64
	mutations int
	more      bool
}

func (k *handedKind) Words() DeleteWords {
	return k.words
}

func (k *handedKind) Count(context.Context, ReadWriteTransaction) (int64, error) {
	if k.counted != nil {
		*k.counted = append(*k.counted, k.words.Many+" counted")
	}

	return k.rows, nil
}

func (k *handedKind) Check(context.Context, ReadWriteTransaction) error {
	*k.asked = append(*k.asked, k.words.Many+" checked")

	return k.refused
}

func (k *handedKind) Take(_ context.Context, _ ReadWriteTransaction, budget int, _ ...eventsource.EventSource) (rows int64, mutations int, more bool, err error) {
	if k.answer != nil {
		*k.made = append(*k.made, fmt.Sprintf("%d %s and %d mutations of %d", k.answer.rows, k.words.Many, k.answer.mutations, budget))

		return k.answer.rows, k.answer.mutations, k.answer.more, nil
	}
	rows = min(k.rows, int64(budget/k.cost))
	*k.made = append(*k.made, fmt.Sprintf("%d %s of %d mutations", rows, k.words.Many, budget))
	more = rows < k.rows
	if k.drains {
		k.rows -= rows
	}

	return rows, int(rows) * k.cost, more, nil
}

// runTaking runs the delete of the record with the key as deleteTaken runs it, over
// the emulator, each kind's rows reached through rows. It asks only, where
// everythingUnder is false.
func runTaking(t *testing.T, db *spanner.Client, tree *DeleteTree, key map[string]any, rows cascadeRows, everythingUnder bool) *cascadeRun {
	t.Helper()

	return runTakingAlso(t, db, tree, key, rows, everythingUnder, 0)
}

// runTakingAlso is runTaking in a save of the given size (DeleteMutationLimit for 0),
// with kinds handed to the delete.
func runTakingAlso(t *testing.T, db *spanner.Client, tree *DeleteTree, key map[string]any, rows cascadeRows, everythingUnder bool, size int, handed ...*handedKind) *cascadeRun {
	t.Helper()

	return runTakingFor(t, db, tree, key, rows, everythingUnder, size, nil, handed...)
}

// runTakingFor is runTakingAlso for a delete whose sentences are written for the person
// given (asked), nobody where it is nil: a trusted call, told every count.
func runTakingFor(t *testing.T, db *spanner.Client, tree *DeleteTree, key map[string]any, rows cascadeRows, everythingUnder bool, size int, asked *Caller, handed ...*handedKind) *cascadeRun {
	t.Helper()

	return runTakingRefused(t, db, tree, key, rows, everythingUnder, size, asked, takingHow{}, handed...)
}

// runUnasked is runTakingAlso for a save that asks nothing (takingDelete.unasked), as a
// save on the token its question gave runs.
func runUnasked(t *testing.T, db *spanner.Client, tree *DeleteTree, key map[string]any, rows cascadeRows, size int, handed ...*handedKind) *cascadeRun {
	t.Helper()

	return runTakingRefused(t, db, tree, key, rows, true, size, nil, takingHow{unasked: true}, handed...)
}

// takingHow is how a test's delete runs beyond its record and its caller: each statement
// asked of refuses before it is sent (joinLimit), nil asking nothing; a frontier read
// pageRows rows a page (takingDelete.pageRows), the delete's own where it is zero; a save
// that asks nothing (takingDelete.unasked); the save's time limit (takingDelete.limit),
// the default where it is zero; and the row at whose asking (passesAt), or the page at
// whose reading (passesAtPage, 1 for the first the save reads), the save's time passes:
// each run of the function then hands its delete a clock of its own with an hour to run,
// whose limit becomes a nanosecond as the row of that name is asked or that page is read.
type takingHow struct {
	refuses      func(sql string) error
	pageRows     int
	unasked      bool
	limit        time.Duration
	passesAt     string
	passesAtPage int
}

// joinLimit is the tests' stand-in for the database's refusal of a statement that joins
// too many tables ("the limit"): it answers Spanner's error for a statement that joins
// more than the given number of tables, the tables a way joins being the " AS w" in its
// text, and nil for any other. With terms, a page's terms count as tables too (" AS a").
func joinLimit(tables int, terms bool) func(sql string) error {
	return func(sql string) error {
		joined := strings.Count(sql, " AS w")
		if terms {
			joined += strings.Count(sql, " AS a")
		}
		if joined > tables {
			return spanner.ToSpannerError(status.Error(codes.InvalidArgument, "Number of joins exceeds the maximum allowed limit of 20."))
		}

		return nil
	}
}

// runTakingRefused is runTakingFor run as how says: each statement asked of its refuses
// before it is sent, as deleteTaken asks PatchSet.deleteRefuses; a frontier read its
// pageRows rows a page; unasked; and with its time limit.
func runTakingRefused(t *testing.T, db *spanner.Client, tree *DeleteTree, key map[string]any, rows cascadeRows, everythingUnder bool, size int, asked *Caller, how takingHow, handed ...*handedKind) *cascadeRun {
	t.Helper()

	refuses := how.refuses
	if refuses == nil {
		refuses = func(string) error { return nil }
	}
	run := &cascadeRun{}
	rows.asked, rows.made = &run.asked, &run.made
	// clock is the clock of the run of the function going, where how names a row or a page
	// at which its time passes: the emulator may abort a transaction and run the function
	// again, and a clock already passed would be passed before the first page.
	var clock *saveClock
	if how.passesAt != "" {
		rows.onAsked = func(name string) {
			if name == how.passesAt {
				clock.limit = time.Nanosecond
			}
		}
	}
	also := make([]TakenKind, 0, len(handed))
	for _, kind := range handed {
		kind.asked, kind.made, kind.counted = &run.asked, &run.made, &run.counted
		also = append(also, kind)
	}
	for _, kinds := range [][]DeleteKind{tree.Taken, tree.RefusedBy} {
		for i := range kinds {
			kinds[i].Rows = rows
		}
	}
	err := NewSpannerClient(db).ExecuteFunc(t.Context(), func(ctx context.Context, txn ReadWriteTransaction) error {
		spannerTxn, ok := txn.(*SpannerReadWriteTransaction)
		if !ok {
			return errors.Newf("the transaction is a %T, want Spanner's", txn)
		}
		run.asked, run.made, run.counted, run.counts, run.pages, run.byTenant, run.statements = nil, nil, nil, 0, 0, 0, nil
		taking := &takingDelete{
			asked: &recordedDelete{tree: tree, key: key, caller: asked},
			query: &takenQuery{tree: tree, key: key},
			txn:   spannerTxn,
			page: func(ctx context.Context, statement rowsUnderStatement, types []reflect.Type, answers int) ([]takenPageRow, error) {
				run.pages++
				if run.pages == how.passesAtPage {
					clock.limit = time.Nanosecond
				}
				run.statements = append(run.statements, statement.SQL)
				if err := refuses(statement.SQL); err != nil {
					return nil, err
				}

				return spannerTxn.readPage(ctx, statement, types, answers)
			},
			byTenant: func(ctx context.Context, statement rowsUnderStatement) ([]tenantRows, error) {
				run.byTenant++
				run.statements = append(run.statements, statement.SQL)
				if err := refuses(statement.SQL); err != nil {
					return nil, err
				}

				return spannerTxn.countByTenant(ctx, statement)
			},
			size:     size,
			also:     also,
			pageRows: how.pageRows,
			unasked:  how.unasked,
			limit:    how.limit,
		}
		if how.passesAt != "" || how.passesAtPage != 0 {
			clock = newSaveClock(time.Hour)
			taking.clock = clock
		}
		tracked, _ := takenTracked(tree, false)
		round, err := taking.run(ctx, func(ctx context.Context, statement rowsUnderStatement) (int64, error) {
			run.counts++
			run.statements = append(run.statements, statement.SQL)
			if err := refuses(statement.SQL); err != nil {
				return 0, err
			}

			return spannerTxn.countRows(ctx, statement)
		}, tracked, everythingUnder)
		run.round, run.err = round, err

		// The test's delete buffers nothing: the transaction commits nothing either way.
		return nil
	})
	if err != nil {
		t.Fatalf("NewSpannerClient().ExecuteFunc() error = %v", err)
	}

	return run
}

// TestTakingDelete reads, over the emulator, the rows a delete takes with its record,
// a kind at a time by the kind's ways up to the record: a row under the record by two
// ways is counted once; a task under a task, three levels deep, is counted once, asked
// about once and deleted the deepest first, with a comment under a nested task found
// under it, whether or not the nested tasks hold the project's key; a folder in a
// folder is taken; a refusing row under a nested task refuses the delete in the counted
// sentence; a kind that asks nothing of
// its rows is counted and never read; a row a condition of the grant refuses refuses the
// whole delete, its page read by one statement; and a row a rule refuses refuses the
// whole delete in the rule's sentence, asked or taken alike. Nothing is deleted where
// the delete is refused. A save that asks nothing (unasked) reads only what it takes:
// it counts nothing but one row at a time, reads no refusing kind and no page's terms,
// asks no row, deletes the rows in the order a save on code's word deletes them, and is
// refused neither by a refusing row nor by a row's rule.
func TestTakingDelete(t *testing.T) {
	t.Parallel()

	db := cascadeDatabase(t, "taking-delete")
	insert := func(table string, rows ...map[string]any) []*spanner.Mutation {
		mutations := make([]*spanner.Mutation, 0, len(rows))
		for _, row := range rows {
			mutations = append(mutations, spanner.InsertMap(table, row))
		}

		return mutations
	}
	var mutations []*spanner.Mutation
	for _, group := range [][]*spanner.Mutation{
		insert("CascadeProjects", map[string]any{"Id": "two"}, map[string]any{"Id": "deep"}, map[string]any{"Id": "keyed"}, map[string]any{"Id": "mixed"}, map[string]any{"Id": "gap"}, map[string]any{"Id": "plain"}),
		insert("CascadeTasks",
			// Under two by its project, with a comment under it by both ways.
			map[string]any{"Id": "two-t1", "ProjectId": "two"},
			// Three levels deep, the nested ones holding no project.
			map[string]any{"Id": "deep-t1", "ProjectId": "deep"},
			map[string]any{"Id": "deep-t2", "ParentTaskId": "deep-t1"},
			map[string]any{"Id": "deep-t3", "ParentTaskId": "deep-t2"},
			// Three levels deep, every one holding the project.
			map[string]any{"Id": "keyed-t1", "ProjectId": "keyed"},
			map[string]any{"Id": "keyed-t2", "ProjectId": "keyed", "ParentTaskId": "keyed-t1"},
			map[string]any{"Id": "keyed-t3", "ProjectId": "keyed", "ParentTaskId": "keyed-t2"},
			// Three levels deep, the top two holding the project and the third not.
			map[string]any{"Id": "mixed-t1", "ProjectId": "mixed"},
			map[string]any{"Id": "mixed-t2", "ProjectId": "mixed", "ParentTaskId": "mixed-t1"},
			map[string]any{"Id": "mixed-t3", "ParentTaskId": "mixed-t2"},
			// Three levels deep, the top and the third holding the project and the second
			// not: levels 0 and 2 find the third.
			map[string]any{"Id": "gap-t1", "ProjectId": "gap"},
			map[string]any{"Id": "gap-t2", "ParentTaskId": "gap-t1"},
			map[string]any{"Id": "gap-t3", "ProjectId": "gap", "ParentTaskId": "gap-t2"},
			// Plain tasks, one of them titled for the condition to refuse.
			map[string]any{"Id": "plain-t1", "ProjectId": "plain", "Title": "open"},
			map[string]any{"Id": "plain-t2", "ProjectId": "plain", "Title": "secret"},
		),
		insert("CascadeComments",
			map[string]any{"Id": "two-c1", "TaskId": "two-t1", "ProjectId": "two"},
			map[string]any{"Id": "two-c2", "TaskId": "two-t1"},
			map[string]any{"Id": "deep-c3", "TaskId": "deep-t3"},
		),
		insert("CascadeInvoices", map[string]any{"Id": "deep-i1", "TaskId": "deep-t2"}),
		insert("CascadeFolders",
			map[string]any{"Id": "f1"},
			map[string]any{"Id": "f2", "ParentId": "f1"},
			map[string]any{"Id": "f3", "ParentId": "f2"},
		),
	} {
		mutations = append(mutations, group...)
	}
	if _, err := db.Apply(t.Context(), mutations); err != nil {
		t.Fatalf("spanner.Client.Apply() error = %v", err)
	}

	asksEveryRow := &takenAsks{question: true}
	project := func(id string) map[string]any {
		return map[string]any{"Id": id}
	}
	twoWays := func() *DeleteTree {
		tree := projectTree()
		tree.Taken[0].Paths = append(tree.Taken[0].Paths, DeletePath{{Columns: []string{"ProjectId"}, Table: "CascadeProjects", References: []string{"Id"}}})

		return tree
	}

	tests := []struct {
		name            string
		tree            *DeleteTree
		key             map[string]any
		rows            cascadeRows
		everythingUnder bool
		wantStatus      int
		wantMessage     string
		wantAsked       []string
		wantMade        []string
		// wantFirstMade is the rows deleted, each the first time it is deleted, where a row
		// two levels find is deleted at both.
		wantFirstMade []string
		// wantPages is the page statements the delete sent; -1 is not checked.
		wantPages int
		// unasked runs the delete as a save that asks nothing (takingDelete.unasked).
		unasked bool
	}{
		{
			name: "a comment under the project by two ways is counted once", tree: twoWays(), key: project("two"),
			wantStatus: http.StatusPreconditionRequired, wantMessage: "Delete this project and everything under it: 1 task, 2 comments. This can't be undone.",
			wantPages: 0,
		},
		{
			name: "nested tasks that hold no project are counted once and asked about once", tree: projectTree(), key: project("deep"), rows: cascadeRows{asks: asksEveryRow},
			wantStatus: http.StatusPreconditionRequired, wantMessage: "Delete this project and everything under it: 3 tasks, 1 comment. This can't be undone.",
			wantAsked: []string{"ID=deep-c3 trusted", "ID=deep-t3 trusted", "ID=deep-t2 trusted", "ID=deep-t1 trusted"},
			wantPages: -1,
		},
		{
			name: "nested tasks that hold no project are deleted the deepest first, a comment under the deepest before it", tree: projectTree(), key: project("deep"), everythingUnder: true,
			wantMade:  []string{"ID=deep-c3 trusted", "ID=deep-t3 trusted", "ID=deep-t2 trusted", "ID=deep-t1 trusted"},
			wantPages: -1,
		},
		{
			name: "nested tasks that hold the project are counted once each", tree: projectTree(), key: project("keyed"),
			wantStatus: http.StatusPreconditionRequired, wantMessage: "Delete this project and everything under it: 3 tasks. This can't be undone.",
			wantPages: 0,
		},
		{
			name: "nested tasks that hold the project are counted and asked about once each and deleted the deepest first", tree: projectTree(), key: project("keyed"), rows: cascadeRows{asks: asksEveryRow}, everythingUnder: true,
			wantAsked: []string{"ID=keyed-t3 trusted", "ID=keyed-t2 trusted", "ID=keyed-t1 trusted"},
			wantMade:  []string{"ID=keyed-t3 trusted", "ID=keyed-t2 trusted", "ID=keyed-t1 trusted"},
			wantPages: -1,
		},
		{
			name: "nested tasks the top two of which hold the project are counted and asked about once each", tree: projectTree(), key: project("mixed"), rows: cascadeRows{asks: asksEveryRow},
			wantStatus: http.StatusPreconditionRequired, wantMessage: "Delete this project and everything under it: 3 tasks. This can't be undone.",
			wantAsked: []string{"ID=mixed-t3 trusted", "ID=mixed-t2 trusted", "ID=mixed-t1 trusted"},
			wantPages: -1,
		},
		{
			name: "nested tasks the top two of which hold the project are deleted once each, the deepest first", tree: projectTree(), key: project("mixed"), everythingUnder: true,
			wantMade:  []string{"ID=mixed-t3 trusted", "ID=mixed-t2 trusted", "ID=mixed-t1 trusted"},
			wantPages: -1,
		},
		{
			// The third task is found at levels 0 and 2, so it is taken at level 2, before the
			// second, and again at level 0; the count is not asserted.
			name: "a nested task that holds the project under one that does not is deleted before the task it is under", tree: projectTree(), key: project("gap"), everythingUnder: true,
			wantFirstMade: []string{"ID=gap-t3 trusted", "ID=gap-t2 trusted", "ID=gap-t1 trusted"},
			wantPages:     -1,
		},
		{
			name: "a folder in a folder is taken, the deepest first", tree: cascadeFolderTree(), key: map[string]any{"Id": "f1"}, everythingUnder: true,
			wantMade:  []string{"ID=f3 trusted", "ID=f2 trusted"},
			wantPages: -1,
		},
		{
			name: "an invoice under a nested task refuses in the counted sentence", tree: invoicedTree(), key: project("deep"), everythingUnder: true,
			wantStatus: http.StatusConflict, wantMessage: "This project has 1 invoice, so it can't be deleted.",
			wantPages: 0,
		},
		{
			name: "a kind that asks nothing of its rows is counted and never read", tree: projectTree(), key: project("plain"),
			wantStatus: http.StatusPreconditionRequired, wantMessage: "Delete this project and everything under it: 2 tasks. This can't be undone.",
			wantPages: 0,
		},
		{
			name: "a row the grant's condition refuses refuses the whole delete, its page read once", tree: projectTree(), key: project("plain"), everythingUnder: true,
			rows:       cascadeRows{asks: &takenAsks{groups: [][]accesstypes.Resource{{"CascadeTasks"}}, terms: []string{"(CascadeTasks.Title != 'secret')"}}},
			wantStatus: http.StatusForbidden, wantMessage: "This project has tasks that you can't delete, so it can't be deleted.",
			wantAsked: []string{"ID=plain-t1 trusted", "ID=plain-t2 trusted"},
			wantPages: 1,
		},
		{
			name: "a row a rule refuses refuses a delete that asks in the rule's sentence", tree: projectTree(), key: project("plain"),
			rows:       cascadeRows{asks: &takenAsks{rule: true}, refused: map[string]cascadeRefusal{"plain-t1": {byRule: true, err: httpio.NewBadRequestMessage("A task that is being worked on can't be deleted.")}}},
			wantStatus: http.StatusBadRequest, wantMessage: "A task that is being worked on can't be deleted.",
			wantAsked: []string{"ID=plain-t1 trusted"},
			wantPages: 1,
		},
		{
			name: "a row a rule refuses refuses a delete that takes in the rule's sentence", tree: projectTree(), key: project("plain"), everythingUnder: true,
			rows:       cascadeRows{asks: &takenAsks{rule: true}, refused: map[string]cascadeRefusal{"plain-t2": {byRule: true, err: httpio.NewBadRequestMessage("A task that is being worked on can't be deleted.")}}},
			wantStatus: http.StatusBadRequest, wantMessage: "A task that is being worked on can't be deleted.",
			wantAsked: []string{"ID=plain-t1 trusted", "ID=plain-t2 trusted"},
			wantPages: 1,
		},
		{
			name: "an unasked save deletes nested tasks that hold no project as a save on code's word does, asking nothing", tree: projectTree(), key: project("deep"), rows: cascadeRows{asks: asksEveryRow}, unasked: true,
			wantMade:  []string{"ID=deep-c3 trusted", "ID=deep-t3 trusted", "ID=deep-t2 trusted", "ID=deep-t1 trusted"},
			wantPages: -1,
		},
		{
			name: "an unasked save deletes nested tasks that hold the project as a save on code's word does, asking nothing", tree: projectTree(), key: project("keyed"), rows: cascadeRows{asks: asksEveryRow}, unasked: true,
			wantMade:  []string{"ID=keyed-t3 trusted", "ID=keyed-t2 trusted", "ID=keyed-t1 trusted"},
			wantPages: -1,
		},
		{
			name: "an unasked save deletes nested tasks the top two of which hold the project as a save on code's word does, asking nothing", tree: projectTree(), key: project("mixed"), rows: cascadeRows{asks: asksEveryRow}, unasked: true,
			wantMade:  []string{"ID=mixed-t3 trusted", "ID=mixed-t2 trusted", "ID=mixed-t1 trusted"},
			wantPages: -1,
		},
		{
			name: "an unasked save deletes a nested task that holds the project under one that does not before the task it is under, asking nothing", tree: projectTree(), key: project("gap"), rows: cascadeRows{asks: asksEveryRow}, unasked: true,
			wantFirstMade: []string{"ID=gap-t3 trusted", "ID=gap-t2 trusted", "ID=gap-t1 trusted"},
			wantPages:     -1,
		},
		{
			name: "an unasked save deletes a folder in a folder, the deepest first, asking nothing", tree: cascadeFolderTree(), key: map[string]any{"Id": "f1"}, rows: cascadeRows{asks: asksEveryRow}, unasked: true,
			wantMade:  []string{"ID=f3 trusted", "ID=f2 trusted"},
			wantPages: -1,
		},
		{
			name: "an unasked save asks no refusal: the tasks over an invoice are deleted", tree: invoicedTree(), key: project("deep"), unasked: true,
			wantMade:  []string{"ID=deep-c3 trusted", "ID=deep-t3 trusted", "ID=deep-t2 trusted", "ID=deep-t1 trusted"},
			wantPages: -1,
		},
		{
			name: "an unasked save asks no row's rule: a task a rule refuses is deleted", tree: projectTree(), key: project("plain"), unasked: true,
			rows:      cascadeRows{asks: &takenAsks{rule: true}, refused: map[string]cascadeRefusal{"plain-t2": {byRule: true, err: httpio.NewBadRequestMessage("A task that is being worked on can't be deleted.")}}},
			wantMade:  []string{"ID=plain-t1 trusted", "ID=plain-t2 trusted"},
			wantPages: -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var run *cascadeRun
			if tt.unasked {
				run = runUnasked(t, db, tt.tree, tt.key, tt.rows, 0)
				readsOnlyWhatItTakes(t, run, tt.tree)
			} else {
				run = runTaking(t, db, tt.tree, tt.key, tt.rows, tt.everythingUnder)
			}
			switch {
			case tt.wantStatus == 0 && run.err != nil:
				t.Fatalf("the delete error = %v, want none", run.err)
			case tt.wantStatus != 0:
				if got := statusOf(run.err); got != tt.wantStatus {
					t.Errorf("the delete's status = %d, want %d (%v)", got, tt.wantStatus, run.err)
				}
				if got := httpio.Message(run.err); got != tt.wantMessage {
					t.Errorf("the delete's message = %q, want %q", got, tt.wantMessage)
				}
			}
			if diff := cmp.Diff(tt.wantAsked, run.asked); diff != "" {
				t.Errorf("the rows asked about (-want +got):\n%s", diff)
			}
			if tt.wantFirstMade != nil {
				var first []string
				for _, made := range run.made {
					if !slices.Contains(first, made) {
						first = append(first, made)
					}
				}
				if diff := cmp.Diff(tt.wantFirstMade, first); diff != "" {
					t.Errorf("the rows deleted, each the first time (-want +got):\n%s", diff)
				}
			} else if diff := cmp.Diff(tt.wantMade, run.made); diff != "" {
				t.Errorf("the rows deleted (-want +got):\n%s", diff)
			}
			if tt.wantPages >= 0 && run.pages != tt.wantPages {
				t.Errorf("the delete read %d page(s), want %d", run.pages, tt.wantPages)
			}
			if run.counts == 0 {
				t.Error("the delete counted nothing")
			}
		})
	}
}

// readsOnlyWhatItTakes holds what a save that asks nothing sends (takingDelete.unasked):
// each count is of one row at most, no statement names a table of the refusing kinds, and
// no page carries a term. Its round finished.
func readsOnlyWhatItTakes(t *testing.T, run *cascadeRun, tree *DeleteTree) {
	t.Helper()

	for _, sql := range run.statements {
		if strings.HasPrefix(sql, "SELECT COUNT(*)") && !strings.HasSuffix(sql, " LIMIT 1)") {
			t.Errorf("an unasked save counted more than one row:\n%s", sql)
		}
		for i := range tree.RefusedBy {
			if refusing := &tree.RefusedBy[i]; strings.Contains(sql, string(refusing.Resource)) {
				t.Errorf("an unasked save read the refusing %s:\n%s", refusing.Words.Many, sql)
			}
		}
		if termed.MatchString(sql) {
			t.Errorf("an unasked save read a page's terms:\n%s", sql)
		}
	}
	if run.err == nil && (run.round == nil || !run.round.finished) {
		t.Errorf("the unasked save's round = %+v, want one that finished", run.round)
	}
}

// termed finds a page's term in a statement (takenQuery.page: "<term> AS a1").
var termed = regexp.MustCompile(` AS a\d`)

// deepestFirst is what is wrong with rows, the rows a delete asked about or deleted in
// order, against want, the rows under the record: each must be there once, and after
// every row under it (under, each row by the row it is directly under). It is empty
// where nothing is.
func deepestFirst(rows, want []string, under map[string]string) string {
	at := make(map[string]int, len(rows))
	for i, row := range rows {
		if _, twice := at[row]; twice {
			return row + " is there twice"
		}
		at[row] = i
	}
	if diff := cmp.Diff(slices.Sorted(slices.Values(want)), slices.Sorted(slices.Values(rows))); diff != "" {
		return "the rows (-want +got):\n" + diff
	}
	for row, parent := range under {
		child, ok := at[row]
		if !ok {
			continue
		}
		if above, ok := at[parent]; ok && above < child {
			return fmt.Sprintf("%s comes before %s, which is under it", parent, row)
		}
	}

	return ""
}

// TestTakingDelete_nestedToAnyDepth holds, over the emulator, a delete that follows a kind
// nested under itself deeper than one statement joins: the record reads the levels the
// database lets it, and what is under the last of them is read from its rows' keys, a
// page at a time, as often as it must. Each case runs with the tests' stand-in for the
// database's refusal (joinLimit, "the limit"), set so that the record reads three or four
// levels and each page four, and again without it, where the emulator refuses a statement
// of more than 20 joins by itself. A chain of 25 tasks, whether or not each holds the
// project, with comments down it, is counted to its end, each row asked about once and
// deleted once, deepest first; so is a chain of 25 folders, a section under a section by
// a key of two columns, a reply under a reply by a column outside the key, replies under
// replies on tasks under tasks with a reaction on each reply (two tables that nest, one
// under the other), and a frontier of three tasks read two rows a page. A refusing
// row 20 deep refuses in the counted sentence; an assignment of another tenant on the last
// task of a chain refuses a caller checked in one tenant, in the sentence that says where
// it is; and a rule that refuses the 20th task refuses in its own, nothing asked after
// it. A ring the walk goes down through fails the delete; a chain whose rows are found at
// two levels, a page holding a row and a row under it, is no ring, and each such row is
// counted, asked about and deleted at both levels. A statement refused at the record's
// first level is the error it is, and a page refused where its count was not starts the
// run again from its first count. The cases that take the rows of the two chains of
// tasks, the folders, the sections, the replies, the two tables that nest and the wide
// frontier are run once more as a save that asks nothing (unasked), with the limit: it
// finds each start's bottom and cut by one-row reads, every row is made once and after
// every row under it, and the ring fails it as it fails the question.
func TestTakingDelete_nestedToAnyDepth(t *testing.T) { //nolint:tparallel // the cases run one after another, as the comment at their loop says
	t.Parallel()

	// staged are the rows of each record, by the record's key, each staged in a database
	// of its own: the emulator reads a statement's joins row by row, so a statement over
	// one record's rows stays quick. insert stages a row of the record being staged.
	staged := make(map[string][]*spanner.Mutation)
	record := ""
	insert := func(table string, row map[string]any) {
		staged[record] = append(staged[record], spanner.InsertMap(table, row))
	}
	// rows are the names of each record's rows, as cascadeRows names them; under is each
	// row by the row it is directly under.
	rows := make(map[string][]string)
	under := make(map[string]string)
	named := func(id string) string {
		return "ID=" + id + " trusted"
	}
	task := func(project string, i int) string {
		return fmt.Sprintf("%s-t%02d", project, i)
	}
	// chain stages n tasks of the project, each under the one before it: only the first
	// holds the project, or every one where keyed.
	chain := func(project string, n int, keyed bool) {
		record = project
		insert("CascadeProjects", map[string]any{"Id": project})
		for i := 1; i <= n; i++ {
			row := map[string]any{"Id": task(project, i)}
			if i == 1 || keyed {
				row["ProjectId"] = project
			}
			if i > 1 {
				row["ParentTaskId"] = task(project, i-1)
				under[named(task(project, i))] = named(task(project, i-1))
			}
			insert("CascadeTasks", row)
			rows[project] = append(rows[project], named(task(project, i)))
		}
	}
	comment := func(project string, on int) {
		record = project
		id := fmt.Sprintf("%s-c%02d", project, on)
		insert("CascadeComments", map[string]any{"Id": id, "TaskId": task(project, on)})
		under[named(id)] = named(task(project, on))
		rows[project] = append(rows[project], named(id))
	}
	for _, project := range []string{"deep", "keyed"} {
		chain(project, 25, project == "keyed")
		for _, on := range []int{2, 3, 8, 25} {
			comment(project, on)
		}
	}
	// An invoice under the 20th task.
	chain("billed", 25, false)
	insert("CascadeInvoices", map[string]any{"Id": "billed-i1", "TaskId": task("billed", 20)})
	// Nine tasks in a chain, then sixteen in a ring: the 10th holds the project and is
	// under the 25th.
	record = "ring"
	insert("CascadeProjects", map[string]any{"Id": "ring"})
	for i := 1; i <= 25; i++ {
		row := map[string]any{"Id": task("ring", i)}
		switch i {
		case 1:
			row["ProjectId"] = "ring"
		case 10:
			row["ProjectId"], row["ParentTaskId"] = "ring", task("ring", 25)
		default:
			row["ParentTaskId"] = task("ring", i-1)
		}
		insert("CascadeTasks", row)
	}
	// One chain of 16 tasks, two of which hold the project: the top one and the fifth. The
	// rows from the fifth down are found at two levels, four apart, so a page holds a row
	// and a row under it, and the page under it holds that row again: no ring.
	record = "twice"
	insert("CascadeProjects", map[string]any{"Id": "twice"})
	twice := []string{"p7", "p6", "p5", "p4", "p3", "p2", "p1", "y", "y1", "y2", "y3", "y4", "y5", "y6", "y7", "y8"}
	parent := ""
	for _, id := range twice {
		row := map[string]any{"Id": "twice-" + id}
		if parent != "" {
			row["ParentTaskId"] = "twice-" + parent
			under[named("twice-"+id)] = named("twice-" + parent)
		}
		if id == "p7" || id == "p3" {
			row["ProjectId"] = "twice"
		}
		insert("CascadeTasks", row)
		rows["twice"] = append(rows["twice"], named("twice-"+id))
		parent = id
	}
	// A chain of 25 folders under the record's folder.
	record = "folder-00"
	insert("CascadeFolders", map[string]any{"Id": "folder-00"})
	for i := 1; i <= 25; i++ {
		id, parent := fmt.Sprintf("folder-%02d", i), fmt.Sprintf("folder-%02d", i-1)
		insert("CascadeFolders", map[string]any{"Id": id, "ParentId": parent})
		if i > 1 {
			under[named(id)] = named(parent)
		}
		rows["folder-00"] = append(rows["folder-00"], named(id))
	}
	// 25 sections, each under the one before it in the same project.
	record = "sectioned"
	insert("CascadeProjects", map[string]any{"Id": "sectioned"})
	for i := 1; i <= 25; i++ {
		row := map[string]any{"ProjectId": "sectioned", "Id": fmt.Sprintf("s%02d", i)}
		name := fmt.Sprintf("ProjectID=sectioned,ID=s%02d trusted", i)
		if i > 1 {
			row["ParentId"] = fmt.Sprintf("s%02d", i-1)
			under[name] = fmt.Sprintf("ProjectID=sectioned,ID=s%02d trusted", i-1)
		}
		insert("CascadeSections", row)
		rows["sectioned"] = append(rows["sectioned"], name)
	}
	// 25 replies in one lane on a task, each answering the one before it.
	record = "replied"
	insert("CascadeProjects", map[string]any{"Id": "replied"})
	insert("CascadeTasks", map[string]any{"Id": "replied-t01", "ProjectId": "replied"})
	for i := 1; i <= 25; i++ {
		id := fmt.Sprintf("replied-r%02d", i)
		row := map[string]any{"Id": id, "Lane": "open"}
		if i == 1 {
			row["TaskId"] = "replied-t01"
		} else {
			row["AnswersId"] = fmt.Sprintf("replied-r%02d", i-1)
			under[named(id)] = named(fmt.Sprintf("replied-r%02d", i-1))
		}
		insert("CascadeReplies", row)
		rows["replied"] = append(rows["replied"], named(id))
	}
	// A reply in another lane answers the 25th: no way of the description reaches it.
	insert("CascadeReplies", map[string]any{"Id": "replied-other", "AnswersId": "replied-r25", "Lane": "closed"})
	// A chain of twelve tasks with an assignment of the caller's own tenant on the second
	// and one of another tenant on the last, which only a page's statements find.
	chain("far", 12, false)
	insert("CascadeAssignments", map[string]any{"Id": "far-a1", "TaskId": task("far", 2), "TenantId": "acme"})
	insert("CascadeAssignments", map[string]any{"Id": "far-a2", "TaskId": task("far", 12), "TenantId": "borealis"})
	// Fourteen tasks in a chain, twelve replies in one lane, the first on the twelfth task
	// and each next answering the one before, and a reaction on each reply.
	chain("threaded", 14, false)
	for i := 1; i <= 12; i++ {
		id, reaction := fmt.Sprintf("threaded-r%02d", i), fmt.Sprintf("threaded-e%02d", i)
		row := map[string]any{"Id": id, "Lane": "open"}
		if i == 1 {
			row["TaskId"] = task("threaded", 12)
			under[named(id)] = named(task("threaded", 12))
		} else {
			row["AnswersId"] = fmt.Sprintf("threaded-r%02d", i-1)
			under[named(id)] = named(fmt.Sprintf("threaded-r%02d", i-1))
		}
		insert("CascadeReplies", row)
		insert("CascadeReactions", map[string]any{"Id": reaction, "ReplyId": id})
		under[named(reaction)] = named(id)
		rows["threaded"] = append(rows["threaded"], named(id), named(reaction))
	}
	// Six tasks in a chain, two more under the third and one under each of those: the
	// record reads four levels at the limit, and its frontier is the fourth task and the
	// two beside it.
	chain("wide", 6, false)
	for _, i := range []string{"1", "2"} {
		x, y := "wide-x"+i, "wide-y"+i
		insert("CascadeTasks", map[string]any{"Id": x, "ParentTaskId": task("wide", 3)})
		insert("CascadeTasks", map[string]any{"Id": y, "ParentTaskId": x})
		under[named(x)], under[named(y)] = named(task("wide", 3)), named(x)
		rows["wide"] = append(rows["wide"], named(x), named(y))
	}
	databases := make(map[string]*spanner.Client, len(staged))
	for _, key := range slices.Sorted(maps.Keys(staged)) {
		databases[key] = cascadeDatabase(t, "deep-"+key)
		if _, err := databases[key].Apply(t.Context(), staged[key]); err != nil {
			t.Fatalf("spanner.Client.Apply() error = %v", err)
		}
	}

	asksEveryRow := &takenAsks{question: true}
	project := func(id string) map[string]any {
		return map[string]any{"Id": id}
	}
	workedOn := cascadeRows{asks: &takenAsks{rule: true}, refused: map[string]cascadeRefusal{task("deep", 20): {byRule: true, err: httpio.NewBadRequestMessage("A task that is being worked on can't be deleted.")}}}
	countsTwice := func(t *testing.T, run *cascadeRun) {
		t.Helper()

		if first := run.statements[0]; slices.Index(run.statements[1:], first) < 0 {
			t.Errorf("the run sent its first count once, want it started again:\n%s", first)
		}
	}
	pagedByTwo := func(t *testing.T, run *cascadeRun, _ bool) {
		t.Helper()

		if !slices.ContainsFunc(run.statements, func(sql string) bool { return strings.HasSuffix(sql, "LIMIT 2 OFFSET 2") }) {
			t.Errorf("no statement read a frontier's second page of two rows (LIMIT 2 OFFSET 2):\n%s", strings.Join(run.statements, "\n"))
		}
	}

	tests := []struct {
		name            string
		tree            *DeleteTree
		key             map[string]any
		rows            cascadeRows
		everythingUnder bool
		// limitOnly runs the case with the limit alone: the emulator refuses nothing it
		// needs by itself.
		limitOnly   bool
		limit       func(sql string) error
		wantStatus  int
		wantMessage string
		// wantError is a part of the error's words where the delete fails.
		wantError string
		// wantAsked is the rows asked about, each once and deepest first, by the record
		// whose rows they are; wantMade the same of the rows deleted.
		wantAsked string
		wantMade  string
		// wantTaken is the rows deleted as the rows asked about, but the last.
		wantTaken bool
		// twice is how many rows are asked about or deleted where rows two levels find
		// come at both: wantAsked and wantMade then hold of each row's first time.
		twice int
		// shallow is a case whose delete reads nothing from a page's ids.
		shallow bool
		// asked is whoever the delete's sentences are written for, nobody where it is nil.
		asked *Caller
		// pageRows is the most rows a page of a frontier holds (takingDelete.pageRows), the
		// delete's own where it is zero.
		pageRows int
		// check holds what else the case asks of the run, with the limit or not.
		check func(t *testing.T, run *cascadeRun, limited bool)
		// unasked runs the case once more as a save that asks nothing, with the limit.
		unasked bool
	}{
		{
			name: "a chain of tasks that hold no project is counted to its end and each row asked about once", tree: projectTree(), key: project("deep"), rows: cascadeRows{asks: asksEveryRow},
			wantStatus: http.StatusPreconditionRequired, wantMessage: "Delete this project and everything under it: 25 tasks, 4 comments. This can't be undone.",
			wantAsked: "deep",
		},
		{
			name: "a chain of tasks that hold no project is deleted, every row once and the deepest first", unasked: true, tree: projectTree(), key: project("deep"), everythingUnder: true,
			wantMade: "deep",
		},
		{
			name: "a chain of tasks that each hold the project is counted to its end and each row asked about once", tree: projectTree(), key: project("keyed"), rows: cascadeRows{asks: asksEveryRow},
			wantStatus: http.StatusPreconditionRequired, wantMessage: "Delete this project and everything under it: 25 tasks, 4 comments. This can't be undone.",
			wantAsked: "keyed",
		},
		{
			name: "a chain of tasks that each hold the project is deleted, every row once and the deepest first", unasked: true, tree: projectTree(), key: project("keyed"), everythingUnder: true,
			wantMade: "keyed",
		},
		{
			name: "a chain of folders is counted to its end", tree: cascadeFolderTree(), key: project("folder-00"), rows: cascadeRows{asks: asksEveryRow},
			wantStatus: http.StatusPreconditionRequired, wantMessage: "Delete this folder and everything under it: 25 folders. This can't be undone.",
			wantAsked: "folder-00",
		},
		{
			name: "a chain of folders is deleted, the deepest first", unasked: true, tree: cascadeFolderTree(), key: project("folder-00"), everythingUnder: true,
			wantMade: "folder-00",
		},
		{
			name: "an invoice under the 20th task refuses a delete that asks in the counted sentence", tree: invoicedTree(), key: project("billed"),
			wantStatus: http.StatusConflict, wantMessage: "This project has 1 invoice, so it can't be deleted.",
		},
		{
			name: "an invoice under the 20th task refuses a delete that takes in the counted sentence", tree: invoicedTree(), key: project("billed"), everythingUnder: true,
			wantStatus: http.StatusConflict, wantMessage: "This project has 1 invoice, so it can't be deleted.",
		},
		{
			name: "a rule that refuses the 20th task refuses a delete that asks in the rule's sentence", tree: projectTree(), key: project("deep"), rows: workedOn,
			wantStatus: http.StatusBadRequest, wantMessage: "A task that is being worked on can't be deleted.",
			check: func(t *testing.T, run *cascadeRun, _ bool) {
				t.Helper()

				if len(run.asked) == 0 || run.asked[len(run.asked)-1] != named(task("deep", 20)) {
					t.Errorf("the rows asked about = %v, want the 20th task last", run.asked)
				}
			},
		},
		{
			name: "a rule that refuses the 20th task refuses a delete that takes in the rule's sentence", tree: projectTree(), key: project("deep"), rows: workedOn, everythingUnder: true,
			wantStatus: http.StatusBadRequest, wantMessage: "A task that is being worked on can't be deleted.",
			wantTaken: true,
			check: func(t *testing.T, run *cascadeRun, _ bool) {
				t.Helper()

				if len(run.asked) == 0 || run.asked[len(run.asked)-1] != named(task("deep", 20)) {
					t.Errorf("the rows asked about = %v, want the 20th task last", run.asked)
				}
			},
		},
		{
			name: "a section under a section by a key of two columns is counted to its end", tree: deepSectionsTree(), key: project("sectioned"), rows: cascadeRows{asks: asksEveryRow},
			wantStatus: http.StatusPreconditionRequired, wantMessage: "Delete this project and everything under it: 25 sections. This can't be undone.",
			wantAsked: "sectioned",
		},
		{
			name: "a section under a section by a key of two columns is deleted, the deepest first", unasked: true, tree: deepSectionsTree(), key: project("sectioned"), everythingUnder: true,
			wantMade: "sectioned",
		},
		{
			name: "a reply under a reply by a column outside the key is counted to its end", tree: deepRepliesTree(), key: project("replied"), rows: cascadeRows{asks: asksEveryRow},
			wantStatus: http.StatusPreconditionRequired, wantMessage: "Delete this project and everything under it: 25 replies. This can't be undone.",
			wantAsked: "replied",
		},
		{
			name: "a reply under a reply by a column outside the key is deleted, the deepest first", unasked: true, tree: deepRepliesTree(), key: project("replied"), everythingUnder: true,
			wantMade: "replied",
		},
		{
			name: "an assignment of another tenant on the last task of a chain refuses a delete that asks, for a caller checked in one tenant", tree: assignedTree(), key: project("far"), asked: acmeCaller(t), limitOnly: true,
			wantStatus: http.StatusConflict, wantMessage: "This project still has 1 assignment in another tenant. It has to be removed there before the project can be deleted.",
		},
		{
			name: "an assignment of another tenant on the last task of a chain refuses a delete that takes, for a caller checked in one tenant", tree: assignedTree(), key: project("far"), asked: acmeCaller(t), everythingUnder: true, limitOnly: true,
			wantStatus: http.StatusConflict, wantMessage: "This project still has 1 assignment in another tenant. It has to be removed there before the project can be deleted.",
		},
		{
			name: "two tables that nest, one under the other, with a kind under the lower, are counted to their end and each row asked about once", tree: threadedTree(), key: project("threaded"), rows: cascadeRows{asks: asksEveryRow},
			wantStatus: http.StatusPreconditionRequired, wantMessage: "Delete this project and everything under it: 14 tasks, 12 replies, 12 reactions. This can't be undone.",
			wantAsked: "threaded",
		},
		{
			name: "two tables that nest, one under the other, with a kind under the lower, are deleted, every row once and the deepest first", unasked: true, tree: threadedTree(), key: project("threaded"), everythingUnder: true,
			wantMade: "threaded",
		},
		{
			name: "a frontier wider than one page is counted to its end and each row asked about once", tree: projectTree(), key: project("wide"), rows: cascadeRows{asks: asksEveryRow},
			limitOnly: true, pageRows: 2,
			wantStatus: http.StatusPreconditionRequired, wantMessage: "Delete this project and everything under it: 10 tasks. This can't be undone.",
			wantAsked: "wide", check: pagedByTwo,
		},
		{
			name: "a frontier wider than one page is deleted, every row once and the deepest first", unasked: true, tree: projectTree(), key: project("wide"), everythingUnder: true,
			limitOnly: true, pageRows: 2,
			wantMade: "wide", check: pagedByTwo,
		},
		{
			name: "a ring the walk goes down through fails the delete", unasked: true, tree: projectTree(), key: project("ring"), rows: cascadeRows{asks: asksEveryRow},
			wantError: "a task under the project is nested under itself",
		},
		{
			name: "a chain two rows of which hold the project is no ring: a row two levels find is counted and asked about at both", tree: projectTree(), key: project("twice"), rows: cascadeRows{asks: asksEveryRow},
			wantStatus: http.StatusPreconditionRequired, wantMessage: "Delete this project and everything under it: 28 tasks. This can't be undone.",
			wantAsked: "twice", twice: 28,
		},
		{
			name: "a chain two rows of which hold the project is no ring: it is deleted, a row two levels find at both", tree: projectTree(), key: project("twice"), everythingUnder: true,
			wantMade: "twice", twice: 28,
		},
		{
			name: "a statement refused at the record's first level is the error it is", tree: projectTree(), key: project("deep"), rows: cascadeRows{asks: asksEveryRow},
			limitOnly: true, limit: joinLimit(0, false), shallow: true,
			wantError: "Number of joins exceeds the maximum allowed limit of 20.",
			check: func(t *testing.T, run *cascadeRun, _ bool) {
				t.Helper()

				if !joinsRefused(run.err) || errors.As(run.err, new(*levelRefused)) {
					t.Errorf("the delete's error = %v, want the database's refusal as it is", run.err)
				}
			},
		},
		{
			name: "a page refused where its count was not starts the run again, and the question answers its counts", tree: projectTree(), key: project("deep"),
			rows:      cascadeRows{asks: &takenAsks{terms: []string{"(TRUE)", "(TRUE)", "(TRUE)", "(TRUE)"}}},
			limitOnly: true, limit: joinLimit(8, true),
			wantStatus: http.StatusPreconditionRequired, wantMessage: "Delete this project and everything under it: 25 tasks, 4 comments. This can't be undone.",
			// The run that started again asks again the rows the first had asked.
			check: func(t *testing.T, run *cascadeRun, _ bool) {
				t.Helper()

				countsTwice(t, run)
			},
		},
	}

	for _, tt := range tests { //nolint:paralleltest // one after another, as the comment below says
		if tt.unasked {
			// One after another, as below.
			t.Run(tt.name+", unasked, with the limit", func(t *testing.T) {
				refuses := tt.limit
				if refuses == nil {
					refuses = joinLimit(8, false)
				}
				id, ok := tt.key["Id"].(string)
				if !ok || databases[id] == nil {
					t.Fatalf("no record %v is staged", tt.key)
				}
				run := runTakingRefused(t, databases[id], tt.tree, tt.key, tt.rows, true, 0, tt.asked, takingHow{refuses: refuses, pageRows: tt.pageRows, unasked: true})
				if tt.wantError != "" {
					if run.err == nil || !strings.Contains(run.err.Error(), tt.wantError) {
						t.Errorf("the unasked save's error = %v, want one that says %q", run.err, tt.wantError)
					}

					return
				}
				if run.err != nil {
					t.Fatalf("the unasked save's error = %v, want none", run.err)
				}
				readsOnlyWhatItTakes(t, run, tt.tree)
				if wrong := deepestFirst(run.made, rows[tt.wantMade], under); wrong != "" {
					t.Errorf("the rows the unasked save deleted: %s", wrong)
				}
				if len(run.asked) > 0 {
					t.Errorf("the rows the unasked save asked about = %v, want none", run.asked)
				}
				if fromIDs := slices.DeleteFunc(slices.Clone(run.statements), func(sql string) bool { return !strings.Contains(sql, "IN UNNEST(@ids)") }); len(fromIDs) < 3 {
					t.Errorf("%d statements of the unasked save started from a page's ids, want at least 3", len(fromIDs))
				}
			})
		}
		for _, limited := range []bool{true, false} {
			if !limited && tt.limitOnly {
				continue
			}
			name := tt.name + ", with the limit"
			if !limited {
				name = tt.name + ", with the emulator's own"
			}
			// One after another: each case reads hundreds of statements in a read-write
			// transaction, and the emulator runs one at a time on a database.
			t.Run(name, func(t *testing.T) {
				var refuses func(sql string) error
				if limited {
					refuses = tt.limit
					if refuses == nil {
						refuses = joinLimit(8, false)
					}
				}
				id, ok := tt.key["Id"].(string)
				if !ok || databases[id] == nil {
					t.Fatalf("no record %v is staged", tt.key)
				}
				run := runTakingRefused(t, databases[id], tt.tree, tt.key, tt.rows, tt.everythingUnder, 0, tt.asked, takingHow{refuses: refuses, pageRows: tt.pageRows})
				switch {
				case tt.wantError != "":
					if run.err == nil || !strings.Contains(run.err.Error(), tt.wantError) {
						t.Errorf("the delete's error = %v, want one that says %q", run.err, tt.wantError)
					}
				case tt.wantStatus == 0 && run.err != nil:
					t.Fatalf("the delete error = %v, want none", run.err)
				case tt.wantStatus == 0:
					if run.round == nil || !run.round.finished {
						t.Errorf("the delete's round = %+v, want one that finished", run.round)
					}
				default:
					if got := statusOf(run.err); got != tt.wantStatus {
						t.Errorf("the delete's status = %d, want %d (%v)", got, tt.wantStatus, run.err)
					}
					if got := httpio.Message(run.err); got != tt.wantMessage {
						t.Errorf("the delete's message = %q, want %q", got, tt.wantMessage)
					}
				}
				firstTimes := func(rows []string) []string {
					if tt.twice == 0 {
						return rows
					}
					if len(rows) != tt.twice {
						t.Errorf("%d rows, want %d: %v", len(rows), tt.twice, rows)
					}
					var first []string
					for _, row := range rows {
						if !slices.Contains(first, row) {
							first = append(first, row)
						}
					}

					return first
				}
				switch {
				case tt.wantAsked != "":
					if wrong := deepestFirst(firstTimes(run.asked), rows[tt.wantAsked], under); wrong != "" {
						t.Errorf("the rows asked about: %s", wrong)
					}
				case tt.check == nil && len(run.asked) > 0:
					t.Errorf("the rows asked about = %v, want none", run.asked)
				}
				switch {
				case tt.wantMade != "":
					if wrong := deepestFirst(firstTimes(run.made), rows[tt.wantMade], under); wrong != "" {
						t.Errorf("the rows deleted: %s", wrong)
					}
				case tt.wantTaken:
					if len(run.asked) == 0 || !slices.Equal(run.made, run.asked[:len(run.asked)-1]) {
						t.Errorf("the rows deleted = %v, want the rows asked about but the last, %v", run.made, run.asked)
					}
				case len(run.made) > 0:
					t.Errorf("the rows deleted = %v, want none", run.made)
				}
				if fromIDs := slices.DeleteFunc(slices.Clone(run.statements), func(sql string) bool { return !strings.Contains(sql, "IN UNNEST(@ids)") }); len(fromIDs) < 3 && !tt.shallow {
					t.Errorf("%d statements started from a page's ids, want at least 3", len(fromIDs))
				}
				if tt.check != nil {
					tt.check(t, run, limited)
				}
			})
		}
	}
}

// TestTakingDelete_handedKinds holds, over the emulator, the kinds code hands a delete
// (TakenKind): the question counts them after the description's kinds, in the order
// handed, two with the same words as one, once each is checked, and its answer carries
// the count of every record, the handed kinds' among them; a handed kind's check refuses
// the asking delete with nothing gone; and a round takes them after the description's
// kinds with what is left of the save's size, a kind with rows left leaving the record
// standing, and the record going once the last is taken. A save on code's word counts
// each handed kind and takes the kinds with rows; a save that asks nothing (unasked)
// counts none, and takes every one in the order handed, a kind with none among them.
func TestTakingDelete_handedKinds(t *testing.T) {
	t.Parallel()

	db := cascadeDatabase(t, "taking-delete-handed")
	if _, err := db.Apply(t.Context(), []*spanner.Mutation{
		spanner.InsertMap("CascadeProjects", map[string]any{"Id": "plain"}),
		spanner.InsertMap("CascadeTasks", map[string]any{"Id": "plain-t1", "ProjectId": "plain"}),
		spanner.InsertMap("CascadeTasks", map[string]any{"Id": "plain-t2", "ProjectId": "plain"}),
	}); err != nil {
		t.Fatalf("spanner.Client.Apply() error = %v", err)
	}
	roles := func(rows int64) *handedKind {
		return &handedKind{words: DeleteWords{One: "role", Many: "roles"}, rows: rows, cost: 1}
	}
	invitations := func(rows int64) *handedKind {
		return &handedKind{words: DeleteWords{One: "invitation", Many: "invitations"}, rows: rows, cost: 1}
	}
	project := map[string]any{"Id": "plain"}

	tests := []struct {
		name            string
		handed          []*handedKind
		everythingUnder bool
		size            int
		wantStatus      int
		wantMessage     string
		wantAsked       []string
		wantMade        []string
		wantRound       *takenRound
		wantError       string
		// wantCounted is the handed kinds counted, in order, and wantRecords the count the
		// question's answer carries (-1 for none).
		wantCounted []string
		wantRecords int64
		unasked     bool
	}{
		{
			name:       "the question counts the handed kinds after the description's, two with the same words as one",
			handed:     []*handedKind{roles(2), invitations(1), roles(3)},
			wantStatus: http.StatusPreconditionRequired, wantMessage: "Delete this project and everything under it: 2 tasks, 5 roles, 1 invitation. This can't be undone.",
			wantAsked:   []string{"roles checked", "invitations checked", "roles checked"},
			wantCounted: []string{"roles counted", "invitations counted", "roles counted"},
			wantRecords: 2 + 2 + 1 + 3,
		},
		{
			name: "a handed kind's check refuses the asking delete with nothing gone",
			handed: func() []*handedKind {
				refusing := invitations(1)
				refusing.refused = httpio.NewForbiddenMessage("You can't withdraw these invitations.")

				return []*handedKind{roles(2), refusing}
			}(),
			wantStatus: http.StatusForbidden, wantMessage: "You can't withdraw these invitations.",
			wantAsked:   []string{"roles checked", "invitations checked"},
			wantCounted: []string{"roles counted", "invitations counted"},
			wantRecords: -1,
		},
		{
			name:   "a round takes the handed kinds after the description's with what the size leaves, and rows left leave the record",
			handed: []*handedKind{roles(10), invitations(1)}, everythingUnder: true,
			// The record, the two tasks and five roles.
			size:        1 + 2 + 5,
			wantMade:    []string{"ID=plain-t1 trusted", "ID=plain-t2 trusted", "5 roles of 5 mutations"},
			wantRound:   &takenRound{deleted: 7},
			wantCounted: []string{"roles counted", "invitations counted"},
			wantRecords: -1,
		},
		{
			name:   "a handed kind that can take nothing, after the description's kinds took rows, leaves the record with what the round took",
			handed: []*handedKind{{words: DeleteWords{One: "group", Many: "groups"}, rows: 1, answer: &handedTake{more: true}}}, everythingUnder: true,
			wantMade:    []string{"ID=plain-t1 trusted", "ID=plain-t2 trusted", "0 groups and 0 mutations of 79997"},
			wantRound:   &takenRound{deleted: 2},
			wantCounted: []string{"groups counted"},
			wantRecords: -1,
		},
		{
			name:            "a round in which a kind of the description cannot take one row, and nothing was taken before it, is an error",
			everythingUnder: true,
			// The record alone.
			size:        1,
			wantError:   "the delete of a project can take nothing in one save: one of its tasks costs more than a save holds",
			wantRecords: -1,
		},
		{
			name:   "a round that takes the last of the handed kinds takes the record",
			handed: []*handedKind{roles(10), invitations(1)}, everythingUnder: true,
			wantMade:    []string{"ID=plain-t1 trusted", "ID=plain-t2 trusted", "10 roles of 79997 mutations", "1 invitations of 79987 mutations"},
			wantRound:   &takenRound{deleted: 13, finished: true},
			wantCounted: []string{"roles counted", "invitations counted"},
			wantRecords: -1,
		},
		{
			name:   "a save on code's word counts each handed kind and takes only those with rows",
			handed: []*handedKind{roles(2), invitations(0), roles(1)}, everythingUnder: true,
			wantMade:    []string{"ID=plain-t1 trusted", "ID=plain-t2 trusted", "2 roles of 79997 mutations", "1 roles of 79995 mutations"},
			wantRound:   &takenRound{deleted: 5, finished: true},
			wantCounted: []string{"roles counted", "invitations counted", "roles counted"},
			wantRecords: -1,
		},
		{
			name:   "an unasked save counts no handed kind and takes every one in the order handed, one with no rows among them",
			handed: []*handedKind{roles(2), invitations(0), roles(1)}, unasked: true,
			wantMade:    []string{"ID=plain-t1 trusted", "ID=plain-t2 trusted", "2 roles of 79997 mutations", "0 invitations of 79995 mutations", "1 roles of 79995 mutations"},
			wantRound:   &takenRound{deleted: 5, finished: true},
			wantRecords: -1,
		},
		{
			name:   "an unasked save with rows of a handed kind left leaves the record",
			handed: []*handedKind{roles(10), invitations(1)}, unasked: true,
			// The record, the two tasks and five roles.
			size:        1 + 2 + 5,
			wantMade:    []string{"ID=plain-t1 trusted", "ID=plain-t2 trusted", "5 roles of 5 mutations"},
			wantRound:   &takenRound{deleted: 7},
			wantRecords: -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var run *cascadeRun
			if tt.unasked {
				run = runUnasked(t, db, projectTree(), project, cascadeRows{}, tt.size, tt.handed...)
			} else {
				run = runTakingAlso(t, db, projectTree(), project, cascadeRows{}, tt.everythingUnder, tt.size, tt.handed...)
			}
			switch {
			case tt.wantError != "":
				if run.err == nil || errors.Cause(run.err).Error() != tt.wantError || statusOf(run.err) != 0 {
					t.Errorf("the delete error = %v (status %d), want a plain error %q", run.err, statusOf(run.err), tt.wantError)
				}
			case tt.wantStatus == 0 && run.err != nil:
				t.Fatalf("the delete error = %v, want none", run.err)
			case tt.wantStatus != 0:
				if got := statusOf(run.err); got != tt.wantStatus {
					t.Errorf("the delete's status = %d, want %d (%v)", got, tt.wantStatus, run.err)
				}
				if got := httpio.Message(run.err); got != tt.wantMessage {
					t.Errorf("the delete's message = %q, want %q", got, tt.wantMessage)
				}
			}
			if diff := cmp.Diff(tt.wantAsked, run.asked); diff != "" {
				t.Errorf("the rows and kinds asked about (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantMade, run.made); diff != "" {
				t.Errorf("the rows and kinds taken (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantRound, run.round, cmp.AllowUnexported(takenRound{})); diff != "" {
				t.Errorf("the round (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantCounted, run.counted); diff != "" {
				t.Errorf("the handed kinds counted (-want +got):\n%s", diff)
			}
			if tt.wantRecords >= 0 {
				if got := questionRecords(t, run.err); got != tt.wantRecords {
					t.Errorf("the question's answer carries %d records, want %d", got, tt.wantRecords)
				}
			}
		})
	}
}

// questionRecords is the count of records the answer to err carries, as the encoder
// writes it (httpio.NewPreconditionRequiredRecords): 0 where it carries none.
func questionRecords(t *testing.T, err error) int64 {
	t.Helper()

	w := httptest.NewRecorder()
	_ = httpio.NewEncoder(w).ClientMessage(t.Context(), err)
	var body httpio.MessageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding the answer %q: %v", w.Body.String(), err)
	}

	return body.Records
}

// TestTakingDelete_anotherTenantsRow reads, over the emulator, the rows under a record of
// a tenant where a row of the record's tenant and a row of another tenant both name it:
// the count and the page of the kind's rows each find the first row alone. It holds for
// a kind that holds its tenant in a column and for one whose rows reach their tenant
// through another table, under the tenant record, whose key holds its tenant, and under
// a record whose row is read for it. With no tenant on the record, or on the kind, both
// rows are under it.
func TestTakingDelete_anotherTenantsRow(t *testing.T) {
	t.Parallel()

	db := forkDatabase(t, "another-tenants-row")
	ctx := context.Background()
	if _, err := db.Apply(ctx, []*spanner.Mutation{
		// The record whose row is read for its tenant, and what is under it.
		spanner.InsertMap("ForkRows", map[string]any{"Id": "f1", "Title": "record", "CreatedBy": "anvil"}),
		spanner.InsertMap("SealedPairRows", map[string]any{"Vault": "anvil", "Shelf": "f1"}),
		spanner.InsertMap("SealedPairRows", map[string]any{"Vault": "bastion", "Shelf": "f1"}),
		spanner.InsertMap("TrackedForkRows", map[string]any{"Id": "ta", "Title": "f1"}),
		spanner.InsertMap("TrackedForkRows", map[string]any{"Id": "tb", "Title": "f1"}),
		// What is under the tenant record anvil.
		spanner.InsertMap("ForkRows", map[string]any{"Id": "k1", "Title": "anvil", "CreatedBy": "anvil"}),
		spanner.InsertMap("ForkRows", map[string]any{"Id": "k2", "Title": "anvil", "CreatedBy": "bastion"}),
		spanner.InsertMap("TrackedForkRows", map[string]any{"Id": "va", "Title": "anvil"}),
		spanner.InsertMap("TrackedForkRows", map[string]any{"Id": "vb", "Title": "anvil"}),
		// The rows a tracked row reaches its tenant through, by its own key.
		spanner.InsertMap("ForkRows", map[string]any{"Id": "ta", "Title": "way", "CreatedBy": "anvil"}),
		spanner.InsertMap("ForkRows", map[string]any{"Id": "tb", "Title": "way", "CreatedBy": "bastion"}),
		spanner.InsertMap("ForkRows", map[string]any{"Id": "va", "Title": "way", "CreatedBy": "anvil"}),
		spanner.InsertMap("ForkRows", map[string]any{"Id": "vb", "Title": "way", "CreatedBy": "bastion"}),
	}); err != nil {
		t.Fatalf("spanner.Client.Apply() error = %v", err)
	}

	byCreator := &DomainBindingData{Column: "CreatedBy"}
	throughForkRows := &DomainBindingData{Column: "Id", Path: []BindingHop{{Table: "ForkRows", JoinColumn: "Id", Column: "CreatedBy"}}}
	text := reflect.TypeFor[string]()

	type under struct {
		kind *DeleteKind
		// mine and theirs name the row of the record's tenant and the row of the other.
		mine, theirs []string
	}
	tests := []struct {
		name string
		tree DeleteTree
		key  map[string]any
		kind under
	}{
		{
			name: "a kind with its tenant in a column, under a record whose row is read",
			tree: DeleteTree{Record: "ForkRows", Words: DeleteWords{One: "row", Many: "rows"}, Key: []string{"Id"}, Tenant: byCreator},
			key:  map[string]any{"Id": "f1"},
			kind: under{
				kind: &DeleteKind{
					Resource: "SealedPairRows", Words: DeleteWords{One: "pair", Many: "pairs"}, Key: []string{"Vault", "Shelf"}, Tenant: &DomainBindingData{Column: "Vault"},
					Paths: []DeletePath{{{Columns: []string{"Shelf"}, Table: "ForkRows", References: []string{"Id"}}}},
				},
				mine: []string{"anvil", "f1"}, theirs: []string{"bastion", "f1"},
			},
		},
		{
			name: "a kind through another table, under a record whose row is read",
			tree: DeleteTree{Record: "ForkRows", Words: DeleteWords{One: "row", Many: "rows"}, Key: []string{"Id"}, Tenant: byCreator},
			key:  map[string]any{"Id": "f1"},
			kind: under{
				kind: &DeleteKind{
					Resource: "TrackedForkRows", Words: DeleteWords{One: "tracked row", Many: "tracked rows"}, Key: []string{"Id"}, Tenant: throughForkRows,
					Paths: []DeletePath{{{Columns: []string{"Title"}, Table: "ForkRows", References: []string{"Id"}}}},
				},
				mine: []string{"ta"}, theirs: []string{"tb"},
			},
		},
		{
			name: "a kind with its tenant in a column, under the tenant record",
			tree: DeleteTree{Record: "Vaults", Words: DeleteWords{One: "vault", Many: "vaults"}, Key: []string{"Id"}, Tenant: &DomainBindingData{Column: "Id"}},
			key:  map[string]any{"Id": "anvil"},
			kind: under{
				kind: &DeleteKind{
					Resource: "ForkRows", Words: DeleteWords{One: "row", Many: "rows"}, Key: []string{"Id"}, Tenant: byCreator,
					Paths: []DeletePath{{{Columns: []string{"Title"}, Table: "Vaults", References: []string{"Id"}}}},
				},
				mine: []string{"k1"}, theirs: []string{"k2"},
			},
		},
		{
			name: "a kind through another table, under the tenant record",
			tree: DeleteTree{Record: "Vaults", Words: DeleteWords{One: "vault", Many: "vaults"}, Key: []string{"Id"}, Tenant: &DomainBindingData{Column: "Id"}},
			key:  map[string]any{"Id": "anvil"},
			kind: under{
				kind: &DeleteKind{
					Resource: "TrackedForkRows", Words: DeleteWords{One: "tracked row", Many: "tracked rows"}, Key: []string{"Id"}, Tenant: throughForkRows,
					Paths: []DeletePath{{{Columns: []string{"Title"}, Table: "Vaults", References: []string{"Id"}}}},
				},
				mine: []string{"va"}, theirs: []string{"vb"},
			},
		},
	}

	for _, tt := range tests {
		for _, variant := range []struct {
			name            string
			noRecordsTenant bool
			noKindsTenant   bool
			wantTheirsUnder bool
		}{
			{name: "the record's tenant alone"},
			{name: "no tenant on the record", noRecordsTenant: true, wantTheirsUnder: true},
			{name: "no tenant on the kind", noKindsTenant: true, wantTheirsUnder: true},
		} {
			t.Run(tt.name+", "+variant.name, func(t *testing.T) {
				t.Parallel()

				tree, kind := tt.tree, *tt.kind.kind
				if variant.noRecordsTenant {
					tree.Tenant = nil
				}
				if variant.noKindsTenant {
					kind.Tenant = nil
				}
				tree.Taken = []DeleteKind{kind}
				want := [][]any{toAny(tt.kind.mine)}
				if variant.wantTheirsUnder {
					want = append(want, toAny(tt.kind.theirs))
				}
				types := slices.Repeat([]reflect.Type{text}, len(kind.Key))

				var counted int64
				var paged []takenPageRow
				err := NewSpannerClient(db).ExecuteFunc(ctx, func(ctx context.Context, txn ReadWriteTransaction) error {
					spannerTxn, ok := txn.(*SpannerReadWriteTransaction)
					if !ok {
						return errors.Newf("the transaction is a %T, want Spanner's", txn)
					}
					query := &takenQuery{tree: &tree, key: tt.key}
					levels, err := countLevels(ctx, spannerTxn.countRows, []countedKind{{kind: &tree.Taken[0], query: query}}, -1)
					if err != nil {
						return err
					}
					counted = levels.total(&tree.Taken[0])
					statement, ok := query.page(&tree.Taken[0], 0, levels.deepest, nil, takenPageRows, 0)
					if !ok {
						return errors.New("page() wrote no read")
					}
					paged, err = spannerTxn.readPage(ctx, statement, types, 0)

					return err
				})
				if err != nil {
					t.Fatalf("NewSpannerClient().ExecuteFunc() error = %v", err)
				}
				if counted != int64(len(want)) {
					t.Errorf("the count of the rows under the record = %d, want %d", counted, len(want))
				}
				got := make([][]any, 0, len(paged))
				for _, row := range paged {
					got = append(got, row.key)
				}
				if diff := cmp.Diff(want, got); diff != "" {
					t.Errorf("the page of the rows under the record (-want +got):\n%s", diff)
				}
			})
		}
	}
}

// toAny is the strings as the values a page reads them as.
func toAny(values []string) []any {
	held := make([]any, 0, len(values))
	for _, value := range values {
		held = append(held, value)
	}

	return held
}

// takingForkRow is a ForkRows record whose delete takes the tracked rows that name it.
type takingForkRow struct {
	ID        string `spanner:"Id"`
	Title     string `spanner:"Title"`
	CreatedBy string `spanner:"CreatedBy"`
	UpdatedBy string `spanner:"UpdatedBy"`
}

func (takingForkRow) Resource() accesstypes.Resource {
	return "ForkRows"
}

func (takingForkRow) DeleteTree() *DeleteTree {
	return &DeleteTree{
		Record: "ForkRows", Words: DeleteWords{One: "row", Many: "rows"}, Key: []string{"Id"},
		Taken: []DeleteKind{{
			Resource: "TrackedForkRows", Words: DeleteWords{One: "tracked row", Many: "tracked rows"}, Key: []string{"Id"},
			Paths: []DeletePath{{{Columns: []string{"Title"}, Table: "ForkRows", References: []string{"Id"}}}},
			Rows:  NewTakenRows(func(*PatchSet[trackedForkRow], *Caller) {}),
		}},
	}
}

// TestPatchSet_deleteTaken_rowsTheSaveWrote holds, over the emulator, a delete that
// takes the rows under its record in a save that wrote two of them first: the one the
// save deleted is deleted again without an error, with the one change record its first
// delete wrote, and the one the save changed is found where it stood before the save and
// deleted, with a record of the change and one of the delete. The delete is not refused
// for the change.
func TestPatchSet_deleteTaken_rowsTheSaveWrote(t *testing.T) {
	t.Parallel()

	db := forkDatabase(t, "taken-rows-the-save-wrote")
	ctx := context.Background()
	if _, err := db.Apply(ctx, []*spanner.Mutation{
		spanner.InsertMap("ForkRows", map[string]any{"Id": "r1", "Title": "record"}),
		spanner.InsertMap("TrackedForkRows", map[string]any{"Id": "ta", "Title": "r1"}),
		spanner.InsertMap("TrackedForkRows", map[string]any{"Id": "tb", "Title": "r1"}),
		spanner.InsertMap("TrackedForkRows", map[string]any{"Id": "tc", "Title": "r1"}),
	}); err != nil {
		t.Fatalf("spanner.Client.Apply() error = %v", err)
	}

	err := NewSpannerClient(db).ExecuteFunc(ctx, func(ctx context.Context, txn ReadWriteTransaction) error {
		deleted := NewPatchSet(NewMetadata[trackedForkRow]()).SetPatchType(DeletePatchType)
		deleted.SetKey("ID", "ta")
		if err := deleted.Buffer(ctx, txn, eventsource.ProcessEvent("test")); err != nil {
			return errors.Wrap(err, "the tracked row's own delete")
		}
		moved := NewPatchSet(NewMetadata[trackedForkRow]()).SetPatchType(UpdatePatchType)
		moved.SetKey("ID", "tb")
		moved.Set("Title", "elsewhere")
		if err := moved.Buffer(ctx, txn, eventsource.ProcessEvent("test")); err != nil {
			return errors.Wrap(err, "the tracked row's change")
		}
		record := NewPatchSet(NewMetadata[takingForkRow]()).SetPatchType(DeletePatchType).DeleteEverythingUnder()
		record.SetKey("ID", "r1")

		return record.Buffer(ctx, txn, eventsource.ProcessEvent("test"))
	})
	if err != nil {
		t.Fatalf("NewSpannerClient().ExecuteFunc() error = %v, want the save committed", err)
	}

	count := func(sql, id string) int64 {
		t.Helper()

		var n int64
		stmt := spanner.NewStatement(sql)
		stmt.Params["id"] = id
		if err := db.Single().Query(ctx, stmt).Do(func(row *spanner.Row) error {
			return row.Column(0, &n)
		}); err != nil {
			t.Fatalf("counting for %s: %v", id, err)
		}

		return n
	}
	for id, want := range map[string]int64{"ta": 1, "tb": 2, "tc": 1} {
		if got := count("SELECT COUNT(*) FROM DataChangeEvents WHERE TableName = 'TrackedForkRows' AND RowId = @id", id); got != want {
			t.Errorf("the row %s has %d change record(s), want %d", id, got, want)
		}
		if got := count("SELECT COUNT(*) FROM TrackedForkRows WHERE Id = @id", id); got != 0 {
			t.Errorf("the row %s is still there", id)
		}
	}
	if row := readForkRow(t, db, "r1"); row != nil {
		t.Errorf("the record is still there: %+v", row)
	}
}

// underForkRow is a ForkRows record that declares nothing of the tracked rows that name
// it: a delete of one is refused while it has one.
type underForkRow struct {
	ID        string `spanner:"Id"`
	Title     string `spanner:"Title"`
	CreatedBy string `spanner:"CreatedBy"`
	UpdatedBy string `spanner:"UpdatedBy"`
}

func (underForkRow) Resource() accesstypes.Resource {
	return "ForkRows"
}

func (underForkRow) DeleteTree() *DeleteTree {
	return &DeleteTree{
		Record: "ForkRows", Words: DeleteWords{One: "row", Many: "rows"}, Key: []string{"Id"},
		Under: []DeleteKind{{
			Resource: "TrackedForkRows", Words: DeleteWords{One: "tracked row", Many: "tracked rows"}, Key: []string{"Id"},
			Paths: []DeletePath{{{Columns: []string{"Title"}, Table: "ForkRows", References: []string{"Id"}}}},
		}},
	}
}

// TestTransactionDeletes_refuse_whatTheDatabaseHeld holds, over the emulator, the count
// before the commit of a record that declares nothing of the rows under it: a save that
// deletes one of the rows and then the record is refused with the count of the rows as
// they stood before the save, and commits nothing.
func TestTransactionDeletes_refuse_whatTheDatabaseHeld(t *testing.T) {
	t.Parallel()

	db := forkDatabase(t, "refuse-what-the-database-held")
	ctx := context.Background()
	if _, err := db.Apply(ctx, []*spanner.Mutation{
		spanner.InsertMap("ForkRows", map[string]any{"Id": "r1", "Title": "record"}),
		spanner.InsertMap("TrackedForkRows", map[string]any{"Id": "ta", "Title": "r1"}),
		spanner.InsertMap("TrackedForkRows", map[string]any{"Id": "tb", "Title": "r1"}),
	}); err != nil {
		t.Fatalf("spanner.Client.Apply() error = %v", err)
	}

	err := NewSpannerClient(db).ExecuteFunc(ctx, func(ctx context.Context, txn ReadWriteTransaction) error {
		row := NewPatchSet(NewMetadata[trackedForkRow]()).SetPatchType(DeletePatchType)
		row.SetKey("ID", "ta")
		if err := row.Buffer(ctx, txn, eventsource.ProcessEvent("test")); err != nil {
			return errors.Wrap(err, "the tracked row's delete")
		}
		record := NewPatchSet(NewMetadata[underForkRow]()).SetPatchType(DeletePatchType)
		record.SetKey("ID", "r1")

		return record.Buffer(ctx, txn, eventsource.ProcessEvent("test"))
	})
	if want := "This row has 2 tracked rows. Delete them first."; !httpio.HasConflict(err) || httpio.Message(err) != want {
		t.Fatalf("the save's error = %v (%q), want a conflict saying %q", err, httpio.Message(err), want)
	}
	if row := readForkRow(t, db, "r1"); row == nil {
		t.Error("the record is gone, want it kept")
	}
}

// bareForkRow is a ForkRows record whose description lists nothing under it.
type bareForkRow struct {
	ID        string `spanner:"Id"`
	Title     string `spanner:"Title"`
	CreatedBy string `spanner:"CreatedBy"`
	UpdatedBy string `spanner:"UpdatedBy"`
}

func (bareForkRow) Resource() accesstypes.Resource {
	return "ForkRows"
}

func (bareForkRow) DeleteTree() *DeleteTree {
	return &DeleteTree{Record: "ForkRows", Words: DeleteWords{One: "row", Many: "rows"}, Key: []string{"Id"}}
}

// TestPatchSet_DeleteAlso holds, over the emulator, a record whose description lists
// nothing under it and that code hands a kind with rows: its delete asks first, naming
// the kind, and deletes nothing; told to take what is under it, it takes the kind's rows
// and the record. Where the kind's rows cost more than one save takes, the record
// stands, the delete says what the kind took, and sent again it goes on
// until the kind has nothing left, and the record goes.
func TestPatchSet_DeleteAlso(t *testing.T) {
	t.Parallel()

	db := forkDatabase(t, "delete-also")
	if _, err := db.Apply(t.Context(), []*spanner.Mutation{
		spanner.InsertMap("ForkRows", map[string]any{"Id": "r1", "Title": "record"}),
		spanner.InsertMap("ForkRows", map[string]any{"Id": "r2", "Title": "record"}),
		spanner.InsertMap("ForkRows", map[string]any{"Id": "r3", "Title": "record"}),
		spanner.InsertMap("ForkRows", map[string]any{"Id": "r4", "Title": "record"}),
	}); err != nil {
		t.Fatalf("spanner.Client.Apply() error = %v", err)
	}
	var asked, made []string
	notes := &handedKind{words: DeleteWords{One: "note", Many: "notes"}, rows: 2, cost: 1, asked: &asked, made: &made}
	deleteRecordOf := func(id string, kind *handedKind, everythingUnder bool, size int) (*DeleteLeft, error) {
		var left *DeleteLeft
		err := NewSpannerClient(db).ExecuteFunc(t.Context(), func(ctx context.Context, txn ReadWriteTransaction) error {
			asked, made = nil, nil
			record := NewPatchSet(NewMetadata[bareForkRow]()).SetPatchType(DeletePatchType).DeleteAlso(kind)
			if everythingUnder {
				record.DeleteEverythingUnder()
			}
			record.SetKey("ID", id)
			record.deleteSaveSize = size
			if err := record.Buffer(ctx, txn, eventsource.ProcessEvent("test")); err != nil {
				return err
			}
			left = record.DeleteLeft()

			return nil
		})

		return left, err
	}
	deleteRecord := func(everythingUnder bool) (*DeleteLeft, error) {
		return deleteRecordOf("r1", notes, everythingUnder, 0)
	}

	_, err := deleteRecord(false)
	if want := "Delete this row and everything under it: 2 notes. This can't be undone."; statusOf(err) != http.StatusPreconditionRequired || httpio.Message(err) != want {
		t.Errorf("the delete that asks = %v (%q), want a 428 saying %q", err, httpio.Message(err), want)
	}
	if diff := cmp.Diff([]string{"notes checked"}, asked); diff != "" {
		t.Errorf("the kinds the delete that asks checked (-want +got):\n%s", diff)
	}
	if readForkRow(t, db, "r1") == nil {
		t.Fatal("the delete that asks deleted the record")
	}

	left, err := deleteRecord(true)
	if err != nil {
		t.Fatalf("the delete that takes error = %v", err)
	}
	if left != nil {
		t.Errorf("DeleteLeft() = %+v, want nil", left)
	}
	if diff := cmp.Diff([]string{"2 notes of 79999 mutations"}, made); diff != "" {
		t.Errorf("what the delete that takes took (-want +got):\n%s", diff)
	}
	if row := readForkRow(t, db, "r1"); row != nil {
		t.Errorf("the record is still there: %+v", row)
	}

	// The record and two notes a save.
	many := &handedKind{words: DeleteWords{One: "note", Many: "notes"}, rows: 5, cost: 1, drains: true, asked: &asked, made: &made}
	for i, want := range []*DeleteLeft{{Deleted: 2}, {Deleted: 2}, nil} {
		left, err := deleteRecordOf("r2", many, true, 1+2)
		if err != nil {
			t.Fatalf("round %d: the delete error = %v", i+1, err)
		}
		if diff := cmp.Diff(want, left); diff != "" {
			t.Errorf("round %d: DeleteLeft() (-want +got):\n%s", i+1, diff)
		}
		if standing := readForkRow(t, db, "r2") != nil; standing != (want != nil) {
			t.Errorf("round %d: the record is there = %t, want %t", i+1, standing, want != nil)
		}
	}

	// A save that only made room for a later one, deleting no row, still answers what it
	// deleted, none, and the record stands; the save that finishes takes it.
	roomy := &handedKind{words: DeleteWords{One: "note", Many: "notes"}, rows: 2, answer: &handedTake{mutations: 3, more: true}, asked: &asked, made: &made}
	left, err = deleteRecordOf("r3", roomy, true, 0)
	if err != nil {
		t.Fatalf("the save that makes room: error = %v", err)
	}
	if diff := cmp.Diff(&DeleteLeft{Deleted: 0}, left); diff != "" {
		t.Errorf("the save that makes room: DeleteLeft() (-want +got):\n%s", diff)
	}
	if readForkRow(t, db, "r3") == nil {
		t.Error("the save that makes room deleted the record")
	}
	left, err = deleteRecordOf("r3", &handedKind{words: DeleteWords{One: "note", Many: "notes"}, rows: 2, cost: 1, asked: &asked, made: &made}, true, 0)
	if err != nil {
		t.Fatalf("the save that finishes: error = %v", err)
	}
	if left != nil {
		t.Errorf("the save that finishes: DeleteLeft() = %+v, want nil", left)
	}
	if row := readForkRow(t, db, "r3"); row != nil {
		t.Errorf("the save that finishes left the record: %+v", row)
	}

	// A save that can take nothing at all is an error, and buffers nothing.
	stuck := &handedKind{words: DeleteWords{One: "note", Many: "notes"}, rows: 2, answer: &handedTake{more: true}, asked: &asked, made: &made}
	left, err = deleteRecordOf("r4", stuck, true, 0)
	if want := "the delete of a row can take nothing in one save: its notes cost more than a save holds, taken the only way they are taken"; err == nil || errors.Cause(err).Error() != want || statusOf(err) != 0 {
		t.Errorf("the save that can take nothing: error = %v (status %d), want a plain error %q", err, statusOf(err), want)
	}
	if left != nil {
		t.Errorf("the save that can take nothing: DeleteLeft() = %+v, want nil", left)
	}
	if readForkRow(t, db, "r4") == nil {
		t.Error("the save that can take nothing deleted the record")
	}
}

// underTaskTree describes a task whose delete takes the invoices that name it, and
// lists under it, one step each, the invoices that name it or were checked against it
// and its timesheets, which it neither takes nor is refused by. A timesheet's step may
// be empty where movable says so.
func underTaskTree(movable bool) *DeleteTree {
	byTask := func(column string, mayBeEmpty bool) DeletePath {
		return DeletePath{{Columns: []string{column}, Table: "CascadeTasks", References: []string{"Id"}, MayBeEmpty: mayBeEmpty}}
	}

	return &DeleteTree{
		Record: "CascadeTasks", Words: DeleteWords{One: "task", Many: "tasks"}, Key: []string{"Id"},
		Taken: []DeleteKind{{
			Resource: "CascadeInvoices", Words: DeleteWords{One: "invoice", Many: "invoices"}, Key: []string{"Id"},
			Paths: []DeletePath{byTask("TaskId", false)},
		}},
		Under: []DeleteKind{
			{
				Resource: "CascadeInvoices", Words: DeleteWords{One: "invoice", Many: "invoices"}, Key: []string{"Id"},
				Paths: []DeletePath{byTask("CheckedTaskId", false), byTask("TaskId", false)},
			},
			{
				Resource: "CascadeTimesheets", Words: DeleteWords{One: "timesheet", Many: "timesheets"}, Key: []string{"Id"},
				Paths: []DeletePath{byTask("TaskId", movable)},
			},
		},
	}
}

// TestTakingDelete_underItNotTaken holds, over the emulator, a delete that takes rows of
// a record with a row straight under it that the description neither takes nor lists
// under refusedBy: asked or taking alike, it is refused in the counted sentence of a
// plain delete before any row is asked about, and nothing is buffered, though the taken
// kind has rows; "Move or delete" where the row's step may be empty; "records under it
// that you can't see" for a person who may not list the kind. A kind under the record by
// two steps refuses by the step no taken kind of its resource holds, and not by the one
// a taken kind holds. A record with no such row goes as before. The refusing kinds answer
// before these, and these before the caller's grants on the taken kinds. A description
// whose kinds under the record are all taken by the same step sends no statement for
// them.
func TestTakingDelete_underItNotTaken(t *testing.T) {
	t.Parallel()

	db := cascadeDatabase(t, "under-it-not-taken")
	var mutations []*spanner.Mutation
	for _, task := range []string{"plain", "sheet", "checked", "billed", "refused"} {
		mutations = append(mutations, spanner.InsertMap("CascadeTasks", map[string]any{"Id": task}))
	}
	for _, row := range []map[string]any{
		{"Id": "plain-i1", "TaskId": "plain"},
		{"Id": "sheet-i1", "TaskId": "sheet"},
		{"Id": "checked-i1", "CheckedTaskId": "checked"},
		{"Id": "billed-i1", "TaskId": "billed", "CheckedTaskId": "billed"},
		{"Id": "refused-i1", "TaskId": "refused"},
	} {
		mutations = append(mutations, spanner.InsertMap("CascadeInvoices", row))
	}
	for _, row := range []map[string]any{{"Id": "sheet-s1", "TaskId": "sheet"}, {"Id": "refused-s1", "TaskId": "refused"}} {
		mutations = append(mutations, spanner.InsertMap("CascadeTimesheets", row))
	}
	mutations = append(mutations, spanner.InsertMap("CascadeComments", map[string]any{"Id": "refused-c1", "TaskId": "refused"}))
	if _, err := db.Apply(t.Context(), mutations); err != nil {
		t.Fatalf("spanner.Client.Apply() error = %v", err)
	}
	key := func(id string) map[string]any {
		return map[string]any{"Id": id}
	}

	for _, everythingUnder := range []bool{false, true} {
		name := "asked"
		if everythingUnder {
			name = "taking"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			refused := []struct {
				name   string
				tree   *DeleteTree
				task   string
				rows   cascadeRows
				asked  *Caller
				want   string
				status int
			}{
				{name: "a row of a kind it does not take", tree: underTaskTree(false), task: "sheet", want: "This task has 1 timesheet. Delete it first."},
				{name: "a row whose step may be empty", tree: underTaskTree(true), task: "sheet", want: "This task has 1 timesheet. Move or delete it first."},
				{name: "a row by the step no taken kind holds", tree: underTaskTree(false), task: "checked", want: "This task has 1 invoice. Delete it first."},
				{name: "a row by both steps, by the one no taken kind holds", tree: underTaskTree(false), task: "billed", want: "This task has 1 invoice. Delete it first."},
				{
					name: "a kind the person may not list", tree: underTaskTree(false), task: "sheet",
					asked: whoLists(accesstypes.Decisions{"CascadeTimesheets": accesstypes.Denied()}),
					want:  "This task has records under it that you can't see, so it can't be deleted.",
				},
				{
					name: "before the caller's grants on the taken kinds", tree: underTaskTree(false), task: "sheet",
					rows: cascadeRows{denied: httpio.NewForbiddenMessage("no grant on invoices")},
					want: "This task has 1 timesheet. Delete it first.",
				},
				{
					name: "after the refusing kinds",
					tree: func() *DeleteTree {
						tree := underTaskTree(false)
						tree.RefusedBy = []DeleteKind{{
							Resource: "CascadeComments", Words: DeleteWords{One: "comment", Many: "comments"}, Key: []string{"Id"},
							Paths: []DeletePath{{{Columns: []string{"TaskId"}, Table: "CascadeTasks", References: []string{"Id"}}}},
						}}

						return tree
					}(),
					task: "refused",
					want: "This task has 1 comment, so it can't be deleted.",
				},
			}
			for _, tt := range refused {
				run := runTakingFor(t, db, tt.tree, key(tt.task), tt.rows, everythingUnder, 0, tt.asked)
				if !httpio.HasConflict(run.err) || httpio.Message(run.err) != tt.want {
					t.Errorf("%s: the delete = %v (%q), want a 409 saying %q", tt.name, run.err, httpio.Message(run.err), tt.want)
				}
				if len(run.asked) != 0 || len(run.made) != 0 {
					t.Errorf("%s: the delete asked about %v and took %v, want nothing", tt.name, run.asked, run.made)
				}
			}

			// The invoice of plain names it by the step its taken kind holds: it is not in
			// the way.
			run := runTaking(t, db, underTaskTree(false), key("plain"), cascadeRows{}, everythingUnder)
			if everythingUnder {
				if run.err != nil {
					t.Fatalf("the delete with nothing in the way = %v, want it to take what is under the task", run.err)
				}
				if diff := cmp.Diff([]string{"ID=plain-i1 trusted"}, run.made); diff != "" {
					t.Errorf("the rows the delete with nothing in the way took (-want +got):\n%s", diff)
				}
			} else if want := "Delete this task and everything under it: 1 invoice. This can't be undone."; statusOf(run.err) != http.StatusPreconditionRequired || httpio.Message(run.err) != want {
				t.Errorf("the delete with nothing in the way = %v (%q), want a 428 saying %q", run.err, httpio.Message(run.err), want)
			}

			// Every kind under the record taken by the same step: no statement for them.
			held := underTaskTree(false)
			held.Under = held.Under[:1]
			held.Under[0].Paths = held.Under[0].Paths[1:]
			bare := underTaskTree(false)
			bare.Under = nil
			heldRun := runTaking(t, db, held, key("plain"), cascadeRows{}, everythingUnder)
			bareRun := runTaking(t, db, bare, key("plain"), cascadeRows{}, everythingUnder)
			if diff := cmp.Diff(bareRun.statements, heldRun.statements); diff != "" {
				t.Errorf("the statements of a delete whose kinds under the record are all taken, against one that lists none under it (-without +with):\n%s", diff)
			}
		})
	}
}
