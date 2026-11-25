package resource

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
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
// took, the rows it asked about and deleted, in order, and how many count and page
// statements it sent.
type cascadeRun struct {
	err    error
	round  *takenRound
	asked  []string
	made   []string
	counts int
	pages  int
}

// handedKind is a kind of row a test hands the delete (TakenKind): rows of it under the
// record, each costing cost mutations, and what its check answers. It notes its check
// with the rows asked about and what it took with the rows deleted.
type handedKind struct {
	words   DeleteWords
	rows    int64
	cost    int
	refused error
	asked   *[]string
	made    *[]string
}

func (k *handedKind) Words() DeleteWords {
	return k.words
}

func (k *handedKind) Count(context.Context, ReadWriteTransaction) (int64, error) {
	return k.rows, nil
}

func (k *handedKind) Check(context.Context, ReadWriteTransaction) error {
	*k.asked = append(*k.asked, k.words.Many+" checked")

	return k.refused
}

func (k *handedKind) Take(_ context.Context, _ ReadWriteTransaction, budget int, _ ...string) (rows int64, mutations int, more bool, err error) {
	rows = min(k.rows, int64(budget/k.cost))
	*k.made = append(*k.made, fmt.Sprintf("%d %s of %d mutations", rows, k.words.Many, budget))

	return rows, int(rows) * k.cost, rows < k.rows, nil
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

	run := &cascadeRun{}
	rows.asked, rows.made = &run.asked, &run.made
	also := make([]TakenKind, 0, len(handed))
	for _, kind := range handed {
		kind.asked, kind.made = &run.asked, &run.made
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
		run.asked, run.made, run.counts, run.pages = nil, nil, 0, 0
		taking := &takingDelete{
			asked: &recordedDelete{tree: tree, key: key},
			query: &takenQuery{tree: tree, key: key},
			txn:   spannerTxn,
			page: func(ctx context.Context, statement rowsUnderStatement, types []reflect.Type, answers int) ([]takenPageRow, error) {
				run.pages++

				return spannerTxn.readPage(ctx, statement, types, answers)
			},
			size: size,
			also: also,
		}
		tracked, _ := takenTracked(tree, false)
		round, err := taking.run(ctx, func(ctx context.Context, statement rowsUnderStatement) (int64, error) {
			run.counts++

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
// the delete is refused.
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
	folderTree := func() *DeleteTree {
		return &DeleteTree{
			Record: "CascadeFolders", Words: DeleteWords{One: "folder", Many: "folders"}, Key: []string{"Id"},
			Taken: []DeleteKind{{
				Resource: "CascadeFolders", Words: DeleteWords{One: "folder", Many: "folders"}, Key: []string{"Id"},
				Paths: []DeletePath{{{Columns: []string{"ParentId"}, Table: "CascadeFolders", References: []string{"Id"}}}},
			}},
		}
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
			name: "a folder in a folder is taken, the deepest first", tree: folderTree(), key: map[string]any{"Id": "f1"}, everythingUnder: true,
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			run := runTaking(t, db, tt.tree, tt.key, tt.rows, tt.everythingUnder)
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

// TestTakingDelete_handedKinds holds, over the emulator, the kinds code hands a delete
// (TakenKind): the question counts them after the description's kinds, in the order
// handed, two with the same words as one, once each is checked; a handed kind's check
// refuses the asking delete with nothing gone; and a round takes them after the
// description's kinds with what is left of the save's size, a kind with rows left leaving
// the record standing, and the record going once the last is taken.
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
	}{
		{
			name:       "the question counts the handed kinds after the description's, two with the same words as one",
			handed:     []*handedKind{roles(2), invitations(1), roles(3)},
			wantStatus: http.StatusPreconditionRequired, wantMessage: "Delete this project and everything under it: 2 tasks, 5 roles, 1 invitation. This can't be undone.",
			wantAsked: []string{"roles checked", "invitations checked", "roles checked"},
		},
		{
			name: "a handed kind's check refuses the asking delete with nothing gone",
			handed: func() []*handedKind {
				refusing := invitations(1)
				refusing.refused = httpio.NewForbiddenMessage("You can't withdraw these invitations.")

				return []*handedKind{roles(2), refusing}
			}(),
			wantStatus: http.StatusForbidden, wantMessage: "You can't withdraw these invitations.",
			wantAsked: []string{"roles checked", "invitations checked"},
		},
		{
			name:   "a round takes the handed kinds after the description's with what the size leaves, and rows left leave the record",
			handed: []*handedKind{roles(10), invitations(1)}, everythingUnder: true,
			// The record, the two tasks and five roles.
			size:      1 + 2 + 5,
			wantMade:  []string{"ID=plain-t1 trusted", "ID=plain-t2 trusted", "5 roles of 5 mutations"},
			wantRound: &takenRound{counted: 13, deleted: 7},
		},
		{
			name:   "a round that takes the last of the handed kinds takes the record",
			handed: []*handedKind{roles(10), invitations(1)}, everythingUnder: true,
			wantMade:  []string{"ID=plain-t1 trusted", "ID=plain-t2 trusted", "10 roles of 79997 mutations", "1 invitations of 79987 mutations"},
			wantRound: &takenRound{counted: 13, deleted: 13, finished: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			run := runTakingAlso(t, db, projectTree(), project, cascadeRows{}, tt.everythingUnder, tt.size, tt.handed...)
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
				t.Errorf("the rows and kinds asked about (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantMade, run.made); diff != "" {
				t.Errorf("the rows and kinds taken (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantRound, run.round, cmp.AllowUnexported(takenRound{})); diff != "" {
				t.Errorf("the round (-want +got):\n%s", diff)
			}
		})
	}
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
					levels, err := countLevels(ctx, spannerTxn.countRows, []countedKind{{kind: &tree.Taken[0], query: query}})
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
		if err := deleted.Buffer(ctx, txn, "test"); err != nil {
			return errors.Wrap(err, "the tracked row's own delete")
		}
		moved := NewPatchSet(NewMetadata[trackedForkRow]()).SetPatchType(UpdatePatchType)
		moved.SetKey("ID", "tb")
		moved.Set("Title", "elsewhere")
		if err := moved.Buffer(ctx, txn, "test"); err != nil {
			return errors.Wrap(err, "the tracked row's change")
		}
		record := NewPatchSet(NewMetadata[takingForkRow]()).SetPatchType(DeletePatchType).DeleteEverythingUnder()
		record.SetKey("ID", "r1")

		return record.Buffer(ctx, txn, "test")
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
		if err := row.Buffer(ctx, txn, "test"); err != nil {
			return errors.Wrap(err, "the tracked row's delete")
		}
		record := NewPatchSet(NewMetadata[underForkRow]()).SetPatchType(DeletePatchType)
		record.SetKey("ID", "r1")

		return record.Buffer(ctx, txn, "test")
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
// and the record.
func TestPatchSet_DeleteAlso(t *testing.T) {
	t.Parallel()

	db := forkDatabase(t, "delete-also")
	if _, err := db.Apply(t.Context(), []*spanner.Mutation{
		spanner.InsertMap("ForkRows", map[string]any{"Id": "r1", "Title": "record"}),
	}); err != nil {
		t.Fatalf("spanner.Client.Apply() error = %v", err)
	}
	var asked, made []string
	notes := &handedKind{words: DeleteWords{One: "note", Many: "notes"}, rows: 2, cost: 1, asked: &asked, made: &made}
	deleteRecord := func(everythingUnder bool) (*DeleteLeft, error) {
		var left *DeleteLeft
		err := NewSpannerClient(db).ExecuteFunc(t.Context(), func(ctx context.Context, txn ReadWriteTransaction) error {
			asked, made = nil, nil
			record := NewPatchSet(NewMetadata[bareForkRow]()).SetPatchType(DeletePatchType).DeleteAlso(notes)
			if everythingUnder {
				record.DeleteEverythingUnder()
			}
			record.SetKey("ID", "r1")
			if err := record.Buffer(ctx, txn, "test"); err != nil {
				return err
			}
			left = record.DeleteLeft()

			return nil
		})

		return left, err
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
}
