package resource

import (
	"context"
	"net/http"
	"slices"
	"sync"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/eventsource"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/internal/spannertxn"
	"github.com/impulseframework/impulse/spxscan/spxapi"
)

// questionRule is the rule on a questionProject's tasks.
var questionRule = &questionRuleNotes{}

// questionRuleNotes is what the rule on a questionProject's tasks is told and what it
// notes: whether it refuses, and where each transaction it is handed sends its reads,
// through which it reads once.
type questionRuleNotes struct {
	mu      sync.Mutex
	refuses bool
	reads   []spxapi.Querier
}

// reset clears the notes and says whether the rule refuses from now on.
func (r *questionRuleNotes) reset(refuses bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.refuses, r.reads = refuses, nil
}

// validate is the rule: it reads through the transaction it is handed, notes where the
// read went, and refuses while the notes say so.
func (r *questionRuleNotes) validate(ctx context.Context, txn ReadWriteTransaction) error {
	reads := txn.SpannerReadOnlyTransaction()
	if err := readOne(ctx, reads); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reads = append(r.reads, reads)
	if r.refuses {
		return httpio.NewBadRequestMessage("A task that is being worked on can't be deleted.")
	}

	return nil
}

// noted answers where each transaction the rule was handed sent its reads, in order.
func (r *questionRuleNotes) noted() []spxapi.Querier {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.reads)
}

// questionProject is roundProject whose tasks' delete asks a rule that notes where the
// transaction it is handed reads, and refuses when the test says so (questionRule).
type questionProject struct {
	ID   string             `spanner:"Id"`
	Code spanner.NullString `spanner:"Code"`
}

func (questionProject) Resource() accesstypes.Resource {
	return "CascadeProjects"
}

func (questionProject) DeleteTree() *DeleteTree {
	return roundTree(func(patchSet *PatchSet[roundTask], _ *Caller) {
		patchSet.RegisterValidateDeleteFunc(questionRule.validate)
	})
}

// notedCall is one call of a notingKind: the method, and where the transaction it was
// handed sends its reads.
type notedCall struct {
	method string
	reads  spxapi.Querier
}

// notingKind is a kind handed a project's delete (TakenKind), its rows "notes", each
// costing one mutation. Each of Count, Check and Take reads once through the
// transaction it is handed and notes where the read went; Take takes every row and
// buffers nothing.
type notingKind struct {
	rows  int64
	calls []notedCall
}

func (k *notingKind) Words() DeleteWords {
	return DeleteWords{One: "note", Many: "notes"}
}

func (k *notingKind) Count(ctx context.Context, txn ReadWriteTransaction) (int64, error) {
	if err := k.note(ctx, "Count", txn); err != nil {
		return 0, err
	}

	return k.rows, nil
}

func (k *notingKind) Check(ctx context.Context, txn ReadWriteTransaction) error {
	return k.note(ctx, "Check", txn)
}

func (k *notingKind) Take(ctx context.Context, txn ReadWriteTransaction, _ int, _ ...eventsource.EventSource) (rows int64, mutations int, more bool, err error) {
	if err := k.note(ctx, "Take", txn); err != nil {
		return 0, 0, false, err
	}

	return k.rows, int(k.rows), false, nil
}

// note reads once through the transaction and notes the call.
func (k *notingKind) note(ctx context.Context, method string, txn ReadWriteTransaction) error {
	reads := txn.SpannerReadOnlyTransaction()
	if err := readOne(ctx, reads); err != nil {
		return err
	}
	k.calls = append(k.calls, notedCall{method: method, reads: reads})

	return nil
}

// readOne runs one statement through reads to its end.
func readOne(ctx context.Context, reads spxapi.Querier) error {
	if err := reads.Query(ctx, spanner.Statement{SQL: "SELECT 1"}).Do(func(*spanner.Row) error {
		return nil
	}); err != nil {
		return errors.Wrap(err, "spanner.RowIterator.Do()")
	}

	return nil
}

// readsAt says where reads go: "read-only", through the read-only transaction of a
// question transaction, which it answers, or "in the save", through a read-write
// transaction.
func readsAt(t *testing.T, reads spxapi.Querier) (string, *spanner.ReadOnlyTransaction) {
	t.Helper()

	switch q := reads.(type) {
	case *spanner.ReadOnlyTransaction:
		return "read-only", q
	case *spanner.ReadWriteTransaction:
		return "in the save", nil
	default:
		t.Fatalf("a transaction sends its reads to a %T", reads)

		return "", nil
	}
}

// TestExecuteDeleteFunc_asksFirst holds, over the emulator, a request that may hold a
// delete that asks first. Run asking, a delete of a project with rows under it is
// answered the question from one run on a transaction that reads read-only: the rule its
// taken rows ask and the Count and Check of a kind code handed it each read there, through
// one read-only transaction, and nothing is gone. A project with nothing under it is
// counted read-only, then counted once more and deleted in a second run, in the save. A
// rule that refuses refuses read-only. A delete told to take the rows asks nothing in the
// first run and takes them in the second. A create beside the delete is made only in the
// save: not at all where the question is answered. Run without asking, and on a
// transaction wrapped by hand, the question is read in the save.
func TestExecuteDeleteFunc_asksFirst(t *testing.T) {
	t.Parallel()

	db := cascadeDatabase(t, "delete-asks-first")
	var staged []*spanner.Mutation
	for _, project := range []string{"asks", "empty", "refused", "takes", "beside", "beside-empty", "inside", "wrapped"} {
		staged = append(staged, spanner.InsertMap("CascadeProjects", map[string]any{"Id": project}))
		if project == "empty" || project == "beside-empty" {
			continue
		}
		for _, task := range []string{"-1", "-2"} {
			staged = append(staged, spanner.InsertMap("CascadeTasks", map[string]any{"Id": project + task, "ProjectId": project}))
		}
	}
	if _, err := db.Apply(t.Context(), staged); err != nil {
		t.Fatalf("spanner.Client.Apply() error = %v", err)
	}
	// deleteOf buffers the project's delete on the transaction, handed the kind, and,
	// where created names one, the create of another project before it.
	deleteOf := func(ctx context.Context, txn ReadWriteTransaction, id string, kind *notingKind, everythingUnder bool, created string) error {
		if created != "" {
			create := NewPatchSet(NewMetadata[questionProject]()).SetPatchType(CreatePatchType)
			create.SetKey("ID", created)
			if err := create.Buffer(ctx, txn, eventsource.ProcessEvent("test")); err != nil {
				return err
			}
		}
		record := NewPatchSet(NewMetadata[questionProject]()).SetPatchType(DeletePatchType).DeleteAlso(kind)
		if everythingUnder {
			record.DeleteEverythingUnder()
		}
		record.SetKey("ID", id)

		return record.Buffer(ctx, txn, eventsource.ProcessEvent("test"))
	}
	// deleteThrough runs deleteOf through ExecuteDeleteFunc, and answers where each run of
	// the function read.
	deleteThrough := func(asks bool, id string, kind *notingKind, everythingUnder bool, created string) (runs []string, err error) {
		err = NewSpannerClient(db).ExecuteDeleteFunc(t.Context(), asks, func(ctx context.Context, txn ReadWriteTransaction) error {
			at, _ := readsAt(t, txn.SpannerReadOnlyTransaction())
			runs = append(runs, at)

			return deleteOf(ctx, txn, id, kind, everythingUnder, created)
		})

		return runs, err
	}
	// where answers where the rule's reads went and where each call of the kind's went,
	// and the read-only transactions they went through, each once.
	where := func(kind *notingKind) (rule, calls []string, snapshots []*spanner.ReadOnlyTransaction) {
		add := func(snapshot *spanner.ReadOnlyTransaction) {
			if snapshot != nil && !slices.Contains(snapshots, snapshot) {
				snapshots = append(snapshots, snapshot)
			}
		}
		for _, reads := range questionRule.noted() {
			at, snapshot := readsAt(t, reads)
			rule = append(rule, at)
			add(snapshot)
		}
		for _, call := range kind.calls {
			at, snapshot := readsAt(t, call.reads)
			calls = append(calls, call.method+" "+at)
			add(snapshot)
		}

		return rule, calls, snapshots
	}
	const question = "Delete this project and everything under it: 2 tasks, 1 note. This can't be undone."
	check := func(name string, got, want []string, what string) {
		t.Helper()
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("%s: %s (-want +got):\n%s", name, what, diff)
		}
	}
	asked := func(name string, err error) {
		t.Helper()
		if statusOf(err) != http.StatusPreconditionRequired || httpio.Message(err) != question {
			t.Errorf("%s = %v (%q), want a 428 saying %q", name, err, httpio.Message(err), question)
		}
	}
	standing := func(name, project string, want roundState) {
		t.Helper()
		if got := readRoundState(t, db, project); got != want {
			t.Errorf("%s: the project holds %+v, want %+v", name, got, want)
		}
	}
	untouched := roundState{record: true, tasks: 2, comments: 0}
	gone := roundState{record: false, tasks: 0, comments: 0}

	// 1. Rows under the record.
	name := "a delete that asks of a project with rows under it"
	questionRule.reset(false)
	kind := &notingKind{rows: 1, calls: nil}
	runs, err := deleteThrough(true, "asks", kind, false, "")
	asked(name, err)
	rule, calls, snapshots := where(kind)
	check(name, runs, []string{"read-only"}, "where each run of the function read")
	check(name, rule, []string{"read-only", "read-only"}, "where the rule read")
	check(name, calls, []string{"Count read-only", "Check read-only"}, "where the kind read")
	if len(snapshots) != 1 {
		t.Errorf("%s read through %d read-only transactions, want one", name, len(snapshots))
	}
	standing(name, "asks", untouched)

	// 2. Nothing under it.
	name = "a delete that asks of a project with nothing under it"
	questionRule.reset(false)
	kind = &notingKind{rows: 0, calls: nil}
	runs, err = deleteThrough(true, "empty", kind, false, "")
	if err != nil {
		t.Errorf("%s error = %v, want the project deleted", name, err)
	}
	rule, calls, _ = where(kind)
	check(name, runs, []string{"read-only", "in the save"}, "where each run of the function read")
	check(name, rule, nil, "where the rule read")
	check(name, calls, []string{"Count read-only", "Count in the save"}, "where the kind read")
	standing(name, "empty", gone)

	// 3. The rule refuses.
	name = "a delete that asks while the rule refuses"
	questionRule.reset(true)
	kind = &notingKind{rows: 1, calls: nil}
	const refusal = "A task that is being worked on can't be deleted."
	runs, err = deleteThrough(true, "refused", kind, false, "")
	if statusOf(err) != http.StatusBadRequest || httpio.Message(err) != refusal {
		t.Errorf("%s = %v (%q), want a 400 saying %q", name, err, httpio.Message(err), refusal)
	}
	rule, _, _ = where(kind)
	check(name, runs, []string{"read-only"}, "where each run of the function read")
	if len(rule) == 0 || slices.ContainsFunc(rule, func(at string) bool { return at != "read-only" }) {
		t.Errorf("%s: the rule read %v, want read-only", name, rule)
	}
	standing(name, "refused", untouched)

	// 4. A delete told to take the rows.
	name = "a delete told to take the rows, run asking"
	questionRule.reset(false)
	kind = &notingKind{rows: 1, calls: nil}
	runs, err = deleteThrough(true, "takes", kind, true, "")
	if err != nil {
		t.Errorf("%s error = %v, want the rows taken", name, err)
	}
	rule, calls, _ = where(kind)
	check(name, runs, []string{"read-only", "in the save"}, "where each run of the function read")
	check(name, rule, []string{"in the save", "in the save"}, "where the rule read")
	check(name, calls, []string{"Count in the save", "Take in the save"}, "where the kind read")
	standing(name, "takes", gone)

	// 5. Another patch beside the delete.
	name = "a create beside a delete that asks of a project with rows under it"
	questionRule.reset(false)
	runs, err = deleteThrough(true, "beside", &notingKind{rows: 1, calls: nil}, false, "beside-new")
	asked(name, err)
	check(name, runs, []string{"read-only"}, "where each run of the function read")
	standing(name, "beside", untouched)
	if got := roundCount(t, db, "SELECT COUNT(*) FROM CascadeProjects WHERE Id = @project", "beside-new"); got != 0 {
		t.Errorf("%s: %d projects made, want none", name, got)
	}
	name = "a create beside a delete that asks of a project with nothing under it"
	runs, err = deleteThrough(true, "beside-empty", &notingKind{rows: 0, calls: nil}, false, "beside-empty-new")
	if err != nil {
		t.Errorf("%s error = %v, want the project deleted and the other made", name, err)
	}
	check(name, runs, []string{"read-only", "in the save"}, "where each run of the function read")
	standing(name, "beside-empty", gone)
	if got := roundCount(t, db, "SELECT COUNT(*) FROM CascadeProjects WHERE Id = @project", "beside-empty-new"); got != 1 {
		t.Errorf("%s: %d projects made, want one", name, got)
	}

	// 6. Not asking.
	name = "a delete that asks of a project with rows under it, run without asking first"
	questionRule.reset(false)
	kind = &notingKind{rows: 1, calls: nil}
	runs, err = deleteThrough(false, "inside", kind, false, "")
	asked(name, err)
	rule, calls, _ = where(kind)
	check(name, runs, []string{"in the save"}, "where each run of the function read")
	check(name, rule, []string{"in the save", "in the save"}, "where the rule read")
	check(name, calls, []string{"Count in the save", "Check in the save"}, "where the kind read")
	standing(name, "inside", untouched)

	// 7. A transaction wrapped by hand.
	name = "a delete that asks on a transaction wrapped by hand"
	questionRule.reset(false)
	kind = &notingKind{rows: 1, calls: nil}
	_, err = spannertxn.Serializable(t.Context(), db, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		return deleteOf(ctx, NewSpannerReadWriteTransaction(txn), "wrapped", kind, false, "")
	})
	asked(name, err)
	rule, calls, _ = where(kind)
	check(name, rule, []string{"in the save", "in the save"}, "where the rule read")
	check(name, calls, []string{"Count in the save", "Check in the save"}, "where the kind read")
	standing(name, "wrapped", untouched)
}
