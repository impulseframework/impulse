package resource

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/eventsource"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/internal/spannertxn"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/spxscan/spxapi"
	"google.golang.org/grpc/codes"
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
// first run and takes them in the second. A create beside the delete is run only in the
// save, its own rule asked there alone: not at all where the question is answered. Run
// without asking, and on a transaction wrapped by hand, the question is read in the save.
// A delete of a row that takes nothing with it asks nothing in the first run: its own
// rule is asked in the save alone. A write buffered straight on the transaction of the
// first run is dropped.
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
	// besideRule is the rule the create beside a delete asks: it notes where each
	// transaction it is handed sends its reads.
	besideRule := &questionRuleNotes{}
	// readAt answers where each transaction a rule was handed sent its reads, in order.
	readAt := func(notes *questionRuleNotes) []string {
		noted := notes.noted()
		at := make([]string, 0, len(noted))
		for _, reads := range noted {
			place, _ := readsAt(t, reads)
			at = append(at, place)
		}

		return at
	}
	// deleteOf buffers the project's delete on the transaction, handed the kind, and,
	// where created names one, the create of another project before it, which asks
	// besideRule.
	deleteOf := func(ctx context.Context, txn ReadWriteTransaction, id string, kind *notingKind, everythingUnder bool, created string) error {
		if created != "" {
			create := NewPatchSet(NewMetadata[questionProject]()).SetPatchType(CreatePatchType)
			create.RegisterValidateCreateFunc(besideRule.validate)
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
	besideRule.reset(false)
	runs, err = deleteThrough(true, "beside", &notingKind{rows: 1, calls: nil}, false, "beside-new")
	asked(name, err)
	check(name, runs, []string{"read-only"}, "where each run of the function read")
	if at := readAt(besideRule); len(at) != 0 {
		t.Errorf("%s: the create's rule read %v, want it not asked: only the delete is run where the request is asked", name, at)
	}
	standing(name, "beside", untouched)
	if got := roundCount(t, db, "SELECT COUNT(*) FROM CascadeProjects WHERE Id = @project", "beside-new"); got != 0 {
		t.Errorf("%s: %d projects made, want none", name, got)
	}
	name = "a create beside a delete that asks of a project with nothing under it"
	besideRule.reset(false)
	runs, err = deleteThrough(true, "beside-empty", &notingKind{rows: 0, calls: nil}, false, "beside-empty-new")
	if err != nil {
		t.Errorf("%s error = %v, want the project deleted and the other made", name, err)
	}
	check(name, runs, []string{"read-only", "in the save"}, "where each run of the function read")
	check(name, readAt(besideRule), []string{"in the save"}, "where the create's rule read")
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

	// 8. A delete of a row that takes nothing with it.
	name = "a delete of a task, which takes nothing with it, run asking"
	plainRule := &questionRuleNotes{}
	runs = nil
	err = NewSpannerClient(db).ExecuteDeleteFunc(t.Context(), true, func(ctx context.Context, txn ReadWriteTransaction) error {
		at, _ := readsAt(t, txn.SpannerReadOnlyTransaction())
		runs = append(runs, at)
		task := NewPatchSet(NewMetadata[roundTask]()).SetPatchType(DeletePatchType)
		task.RegisterValidateDeleteFunc(plainRule.validate)
		task.SetKey("ID", "wrapped-1")

		return task.Buffer(ctx, txn, eventsource.ProcessEvent("test"))
	})
	if err != nil {
		t.Errorf("%s error = %v, want the task deleted", name, err)
	}
	check(name, runs, []string{"read-only", "in the save"}, "where each run of the function read")
	check(name, readAt(plainRule), []string{"in the save"}, "where the task's rule read")
	if got := roundCount(t, db, "SELECT COUNT(*) FROM CascadeTasks WHERE Id = @project", "wrapped-1"); got != 0 {
		t.Errorf("%s: %d tasks left, want none", name, got)
	}

	// 9. A write buffered straight on the transaction of the first run.
	name = "a write buffered straight on the transaction of the first run"
	err = NewSpannerClient(db).ExecuteDeleteFunc(t.Context(), true, func(_ context.Context, txn ReadWriteTransaction) error {
		direct := NewPatchSet(NewMetadata[questionProject]()).SetPatchType(CreatePatchType)
		direct.SetKey("ID", "direct-new")
		if err := txn.BufferMap(direct, map[string]any{"Id": "direct-new"}); err != nil {
			return errors.Wrap(err, "ReadWriteTransaction.BufferMap()")
		}
		if err := txn.BufferStruct(direct); err != nil {
			return errors.Wrap(err, "ReadWriteTransaction.BufferStruct()")
		}

		return httpio.NewBadRequestMessage("Nothing more is run.")
	})
	if got := httpio.Message(err); got != "Nothing more is run." {
		t.Errorf("%s: the run answered %v (%q), want the function's own refusal", name, err, got)
	}
	if got := roundCount(t, db, "SELECT COUNT(*) FROM CascadeProjects WHERE Id = @project", "direct-new"); got != 0 {
		t.Errorf("%s: %d projects made, want none", name, got)
	}
}

// tokenRule is the rule on a tokenProject's tasks.
var tokenRule = &questionRuleNotes{}

// tokenProject is questionProject with a rule of its own (tokenRule), for the test of the
// delete's token.
type tokenProject struct {
	ID   string             `spanner:"Id"`
	Code spanner.NullString `spanner:"Code"`
}

func (tokenProject) Resource() accesstypes.Resource {
	return "CascadeProjects"
}

func (tokenProject) DeleteTree() *DeleteTree {
	return roundTree(func(patchSet *PatchSet[roundTask], _ *Caller) {
		patchSet.RegisterValidateDeleteFunc(tokenRule.validate)
	})
}

// failingKind is a kind handed a project's delete (TakenKind) whose Take fails: one row
// of it is counted, and it checks nothing.
type failingKind struct{}

func (failingKind) Words() DeleteWords {
	return DeleteWords{One: "note", Many: "notes"}
}

func (failingKind) Count(context.Context, ReadWriteTransaction) (int64, error) {
	return 1, nil
}

func (failingKind) Check(context.Context, ReadWriteTransaction) error {
	return nil
}

func (failingKind) Take(context.Context, ReadWriteTransaction, int, ...eventsource.EventSource) (rows int64, mutations int, more bool, err error) {
	return 0, 0, false, errors.New("the notes cannot be taken")
}

// tokenDelete is one delete of a request run through ExecuteDeleteFunc: of the project id,
// on the token (DeleteWithToken; empty for a delete that asks), or on code's word, handed
// the kind where there is one, with the save's size and time and the stand-in for the
// database's refusal (PatchSet.deleteRefuses) where the test sets them.
type tokenDelete struct {
	id       string
	token    string
	code     bool
	kind     TakenKind
	size     int
	saveTime time.Duration
	refuses  func(sql string) error
}

// tokenRun is what a request's deletes answered: where each run of its function read
// (readsAt), what each delete left (DeleteLeft) in the last run, and the error.
type tokenRun struct {
	runs  []string
	lefts []*DeleteLeft
	err   error
}

// runTokenDeletes runs the deletes in one function through ExecuteDeleteFunc on the
// context, asking first where asks says so, as RemovesAsk would; dryRun ends the function
// with ErrDryRun once it buffered them.
func runTokenDeletes[R Resourcer](t *testing.T, db *spanner.Client, ctx context.Context, asks, dryRun bool, deletes ...tokenDelete) *tokenRun {
	t.Helper()

	run := &tokenRun{runs: nil, lefts: nil, err: nil}
	run.err = NewSpannerClient(db).ExecuteDeleteFunc(ctx, asks, func(ctx context.Context, txn ReadWriteTransaction) error {
		at, _ := readsAt(t, txn.SpannerReadOnlyTransaction())
		run.runs = append(run.runs, at)
		run.lefts = nil
		for _, d := range deletes {
			record := NewPatchSet(NewMetadata[R]()).SetPatchType(DeletePatchType)
			if d.kind != nil {
				record.DeleteAlso(d.kind)
			}
			if d.code {
				record.DeleteEverythingUnder()
			} else {
				record.DeleteWithToken(d.token)
			}
			record.SetKey("ID", d.id)
			record.deleteSaveSize, record.deleteSaveTime, record.deleteRefuses = d.size, d.saveTime, d.refuses
			if err := record.Buffer(ctx, txn, eventsource.ProcessEvent("test")); err != nil {
				return err
			}
			run.lefts = append(run.lefts, record.DeleteLeft())
		}
		if dryRun {
			return ErrDryRun
		}

		return nil
	})

	return run
}

// tokenOf reads the token the answer to err carries (httpio.MessageResponse.Token), "" for
// none.
func tokenOf(t *testing.T, err error) string {
	t.Helper()

	w := httptest.NewRecorder()
	_ = httpio.NewEncoder(w).ClientMessage(t.Context(), err)
	var body httpio.MessageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding the answer %q: %v", w.Body.String(), err)
	}

	return body.Token
}

// storedToken is a row of DeleteTokens as the test reads it.
type storedToken struct {
	TableName     string             `spanner:"TableName"`
	RowKey        string             `spanner:"RowKey"`
	SessionID     string             `spanner:"SessionId"`
	PrincipalUser spanner.NullString `spanner:"PrincipalUser"`
	PrincipalRole spanner.NullString `spanner:"PrincipalRole"`
	ExpiresAt     time.Time          `spanner:"ExpiresAt"`
}

// readToken reads the token's row, nil where there is none.
func readToken(t *testing.T, db *spanner.Client, id string) *storedToken {
	t.Helper()

	row, err := db.Single().ReadRow(t.Context(), "DeleteTokens", spanner.Key{id}, []string{"TableName", "RowKey", "SessionId", "PrincipalUser", "PrincipalRole", "ExpiresAt"})
	if spanner.ErrCode(err) == codes.NotFound {
		return nil
	}
	if err != nil {
		t.Fatalf("reading the token %q: %v", id, err)
	}
	var token storedToken
	if err := row.ToStruct(&token); err != nil {
		t.Fatalf("reading the token %q: %v", id, err)
	}

	return &token
}

// tokenRows counts the rows of DeleteTokens.
func tokenRows(t *testing.T, db *spanner.Client) int64 {
	t.Helper()

	return roundCount(t, db, "SELECT COUNT(*) FROM DeleteTokens WHERE @project = @project", "")
}

// TestExecuteDeleteFunc_token holds, over the emulator, a request's delete of a project
// that goes on the token its question gave (PatchSet.DeleteWithToken). The question,
// asked on a context that carries a session, writes the token's row and answers it beside
// its count; a delete on the token takes everything with no rule, count or check asked
// and deletes the token; a delete with no token, and one whose token does not hold (of
// another session, another person, a role, another project, expired, used, or none), is
// answered with the question again and a new token, and nothing is gone, or with the
// rule's refusal where a rule refuses. Several saves go on one token, its expiry moved on
// by each, across descents and at a time limit too; a save that fails deletes the token,
// a dry run keeps it. A context with no session is given no token. A delete on code's
// word goes as it always did, asking everything in every save, and reads or writes no
// token. Two deletes in one request, one on its token, ask the other's question alone.
func TestExecuteDeleteFunc_token(t *testing.T) { //nolint:maintidx // one case after another, each over the one database
	t.Parallel()

	db := cascadeDatabase(t, "delete-token")
	var staged []*spanner.Mutation
	tasks := map[string]int{
		"asked": 2, "none": 2, "four": 2, "other": 2, "refused": 2, "several": 5, "fails": 3, "dry": 2,
		"nosession": 2, "code": 2, "code-nosession": 2, "invoiced": 2, "pair-a": 2, "pair-b": 3, "timed": 5,
		"bare": 0,
	}
	for project, n := range tasks {
		staged = append(staged, spanner.InsertMap("CascadeProjects", map[string]any{"Id": project}))
		for i := 1; i <= n; i++ {
			staged = append(staged, spanner.InsertMap("CascadeTasks", map[string]any{"Id": fmt.Sprintf("%s-%d", project, i), "ProjectId": project}))
		}
	}
	staged = append(staged, spanner.InsertMap("CascadeInvoices", map[string]any{"Id": "invoiced-i1", "TaskId": "invoiced-1"}))
	if _, err := db.Apply(t.Context(), staged); err != nil {
		t.Fatalf("spanner.Client.Apply() error = %v", err)
	}

	sessionAs := func(id string, principal accesstypes.Principal) context.Context {
		return context.WithValue(t.Context(), sessioninfo.CtxSessionInfo, &sessioninfo.SessionData{
			SessionInfo: &sessioninfo.SessionInfo{ID: impulse.Must(impulse.UUIDFromString(id))},
			Principal:   principal,
		})
	}
	ada := sessionAs("0190d000-0000-7000-8000-000000000001", accesstypes.UserPrincipal("ada"))
	adaElsewhere := sessionAs("0190d000-0000-7000-8000-000000000002", accesstypes.UserPrincipal("ada"))
	bobInAdas := sessionAs("0190d000-0000-7000-8000-000000000001", accesstypes.UserPrincipal("bob"))
	roleInAdas := sessionAs("0190d000-0000-7000-8000-000000000001", accesstypes.RolePrincipal("Auditor"))
	adaSession := "0190d000-0000-7000-8000-000000000001"

	const recordCost = 1
	taskCost := rowDeleteMutations + ChangeRecordMutations(eventsource.ProcessEvent("test"))
	question := func(tasks, notes int) string {
		if notes == 0 {
			return fmt.Sprintf("Delete this project and everything under it: %d tasks. This can't be undone.", tasks)
		}

		return fmt.Sprintf("Delete this project and everything under it: %d tasks, %d note. This can't be undone.", tasks, notes)
	}
	// asked holds err to the question with a token, and answers the token.
	asked := func(name, want string, err error) string {
		t.Helper()
		if statusOf(err) != http.StatusPreconditionRequired || httpio.Message(err) != want {
			t.Errorf("%s = %v (%q), want a 428 saying %q", name, err, httpio.Message(err), want)
		}
		token := tokenOf(t, err)
		if token == "" {
			t.Errorf("%s: the 428 carries no token", name)
		}

		return token
	}
	ask := func(ctx context.Context, project string, kind TakenKind) string {
		t.Helper()
		run := runTokenDeletes[tokenProject](t, db, ctx, true, false, tokenDelete{id: project, kind: kind})
		notes := 0
		if kind != nil {
			notes = 1
		}

		return asked("the question of "+project, question(tasks[project], notes), run.err)
	}
	check := func(name string, got, want any, what string) {
		t.Helper()
		if diff := cmp.Diff(want, got, cmp.AllowUnexported(roundState{})); diff != "" {
			t.Errorf("%s: %s (-want +got):\n%s", name, what, diff)
		}
	}
	untouched := func(project string) roundState {
		return roundState{record: true, tasks: int64(tasks[project]), comments: 0}
	}
	gone := roundState{record: false, tasks: 0, comments: 0}

	// 1. The question writes a token.
	name := "the question"
	tokenRule.reset(false)
	kind := &notingKind{rows: 1, calls: nil}
	run := runTokenDeletes[tokenProject](t, db, ada, true, false, tokenDelete{id: "asked", kind: kind})
	token := asked(name, question(2, 1), run.err)
	check(name, questionRecords(t, run.err), int64(3), "the records the question counted")
	check(name, run.runs, []string{"read-only"}, "where each run of the function read")
	stored := readToken(t, db, token)
	if stored == nil {
		t.Fatalf("%s: no row of DeleteTokens holds the token %q", name, token)
	}
	check(name, *stored, storedToken{
		TableName: "CascadeProjects", RowKey: rowMatchKey(KeySet{}.Add("ID", "asked")), SessionID: adaSession,
		PrincipalUser: spanner.NullString{StringVal: "ada", Valid: true}, PrincipalRole: spanner.NullString{}, ExpiresAt: stored.ExpiresAt,
	}, "the token's row")
	if off := time.Until(stored.ExpiresAt) - DeleteTokenLife; off < -10*time.Second || off > 10*time.Second {
		t.Errorf("%s: the token expires at %s, want DeleteTokenLife from now", name, stored.ExpiresAt)
	}
	check(name, readRoundState(t, db, "asked"), untouched("asked"), "the project")

	// 2. A delete on it.
	name = "a delete on the token"
	tokenRule.reset(false)
	kind.calls = nil
	run = runTokenDeletes[tokenProject](t, db, ada, false, false, tokenDelete{id: "asked", token: token, kind: kind})
	if run.err != nil {
		t.Fatalf("%s: error = %v", name, run.err)
	}
	check(name, run.runs, []string{"in the save"}, "where each run of the function read")
	check(name, len(tokenRule.noted()), 0, "the times the rule was asked")
	var called []string
	for _, call := range kind.calls {
		called = append(called, call.method)
	}
	check(name, called, []string{"Take"}, "the handed kind's calls")
	check(name, readRoundState(t, db, "asked"), gone, "the project")
	if readToken(t, db, token) != nil {
		t.Errorf("%s: the token's row is there, want it deleted with the project", name)
	}
	used := token

	// 3. No token.
	name = "a delete with no token"
	tokenRule.reset(false)
	run = runTokenDeletes[tokenProject](t, db, ada, true, false, tokenDelete{id: "none"})
	asked(name, question(2, 0), run.err)
	check(name, run.runs, []string{"read-only"}, "where each run of the function read")
	rule := tokenRule.noted()
	for _, reads := range rule {
		if at, _ := readsAt(t, reads); at != "read-only" {
			t.Errorf("%s: the rule read %s, want read-only", name, at)
		}
	}
	check(name, len(rule), 2, "the times the rule was asked")
	check(name, readRoundState(t, db, "none"), untouched("none"), "the project")

	// 4. Tokens that do not hold.
	expired := "expired-token"
	if _, err := db.Apply(t.Context(), []*spanner.Mutation{spanner.InsertMap("DeleteTokens", map[string]any{
		"Id": expired, "TableName": "CascadeProjects", "RowKey": rowMatchKey(KeySet{}.Add("ID", "four")), "SessionId": adaSession,
		"PrincipalUser": "ada", "CreatedAt": spanner.CommitTimestamp, "ExpiresAt": time.Now().Add(-time.Minute),
	})}); err != nil {
		t.Fatalf("spanner.Client.Apply() error = %v", err)
	}
	for _, tt := range []struct {
		name  string
		token string
	}{
		{name: "another session's", token: ask(adaElsewhere, "four", nil)},
		{name: "another person's", token: ask(bobInAdas, "four", nil)},
		{name: "a role's, where the session is a person's", token: ask(roleInAdas, "four", nil)},
		{name: "another project's", token: ask(ada, "other", nil)},
		{name: "an expired one", token: expired},
		{name: "one already used", token: used},
		{name: "an id no row has", token: "no-such-token"},
	} {
		name = "a delete on " + tt.name + " token"
		run = runTokenDeletes[tokenProject](t, db, ada, false, false, tokenDelete{id: "four", token: tt.token})
		if next := asked(name, question(2, 0), run.err); next == tt.token {
			t.Errorf("%s: the question gave the token again, want a new one", name)
		}
		check(name, run.runs, []string{"in the save", "read-only"}, "where each run of the function read")
		check(name, readRoundState(t, db, "four"), untouched("four"), "the project")
	}

	// 5. A token that does not hold, while the rule refuses.
	name = "a delete on a token that does not hold, refused by its rule"
	tokenRule.reset(true)
	run = runTokenDeletes[tokenProject](t, db, ada, false, false, tokenDelete{id: "refused", token: "no-such-token"})
	if want := "A task that is being worked on can't be deleted."; statusOf(run.err) != http.StatusBadRequest || httpio.Message(run.err) != want {
		t.Errorf("%s = %v (%q), want a 400 saying %q", name, run.err, httpio.Message(run.err), want)
	}
	check(name, readRoundState(t, db, "refused"), untouched("refused"), "the project")
	tokenRule.reset(false)

	// 6. Several saves on one token.
	name = "several saves on one token"
	token = ask(ada, "several", nil)
	// The token's write, the record and two tasks, a mutation short of three: a save that
	// did not take the token's write off its budget would hold a third.
	size := tokenWriteMutations + recordCost + 3*taskCost - 1
	expires := readToken(t, db, token).ExpiresAt
	for save, want := range []*DeleteLeft{{Deleted: 2}, {Deleted: 2}, nil} {
		run = runTokenDeletes[tokenProject](t, db, ada, false, false, tokenDelete{id: "several", token: token, size: size})
		if run.err != nil {
			t.Fatalf("%s: save %d: error = %v", name, save+1, run.err)
		}
		check(name, run.lefts, []*DeleteLeft{want}, fmt.Sprintf("what save %d left", save+1))
		if want == nil {
			break
		}
		stored := readToken(t, db, token)
		if stored == nil {
			t.Fatalf("%s: save %d deleted the token with the project standing", name, save+1)
		}
		if !stored.ExpiresAt.After(expires) {
			t.Errorf("%s: save %d left the token expiring at %s, want after %s", name, save+1, stored.ExpiresAt, expires)
		}
		expires = stored.ExpiresAt
	}
	check(name, readRoundState(t, db, "several"), gone, "the project")
	if readToken(t, db, token) != nil {
		t.Errorf("%s: the token's row is there, want it deleted in the last save", name)
	}

	// 7. A save that fails.
	name = "a save that fails"
	token = ask(ada, "fails", failingKind{})
	size = tokenWriteMutations + recordCost + 2*taskCost
	run = runTokenDeletes[tokenProject](t, db, ada, false, false, tokenDelete{id: "fails", token: token, kind: failingKind{}, size: size})
	if run.err != nil {
		t.Fatalf("%s: the first save: error = %v", name, run.err)
	}
	run = runTokenDeletes[tokenProject](t, db, ada, false, false, tokenDelete{id: "fails", token: token, kind: failingKind{}, size: size})
	if run.err == nil || !strings.Contains(run.err.Error(), "the notes cannot be taken") {
		t.Errorf("%s: the second save's error = %v, want the kind's", name, run.err)
	}
	check(name, readRoundState(t, db, "fails"), roundState{record: true, tasks: 1, comments: 0}, "the project after the failed save")
	if readToken(t, db, token) != nil {
		t.Errorf("%s: the token's row is there, want it deleted after the failed save", name)
	}
	run = runTokenDeletes[tokenProject](t, db, ada, false, false, tokenDelete{id: "fails", token: token, kind: failingKind{}, size: size})
	asked(name+", sent again", "Delete this project and everything under it: 1 task, 1 note. This can't be undone.", run.err)

	// 8. A dry run.
	name = "a dry run on a token"
	token = ask(ada, "dry", nil)
	run = runTokenDeletes[tokenProject](t, db, ada, false, true, tokenDelete{id: "dry", token: token})
	if !errors.Is(run.err, ErrDryRun) {
		t.Errorf("%s: error = %v, want ErrDryRun", name, run.err)
	}
	if readToken(t, db, token) == nil {
		t.Errorf("%s: the token's row is gone, want it kept", name)
	}
	check(name, readRoundState(t, db, "dry"), untouched("dry"), "the project")

	// A dry run of a request that asks and has nothing to ask runs in the save too.
	name = "a dry run that asks about a project with nothing under it"
	run = runTokenDeletes[tokenProject](t, db, ada, true, true, tokenDelete{id: "bare"})
	if !errors.Is(run.err, ErrDryRun) {
		t.Errorf("%s: error = %v, want ErrDryRun", name, run.err)
	}
	check(name, run.runs, []string{"read-only", "in the save"}, "where each run of the function read")
	check(name, readRoundState(t, db, "bare"), untouched("bare"), "the project")

	// 9. No session.
	name = "a question with no session"
	before := tokenRows(t, db)
	run = runTokenDeletes[tokenProject](t, db, t.Context(), true, false, tokenDelete{id: "nosession"})
	if statusOf(run.err) != http.StatusPreconditionRequired || tokenOf(t, run.err) != "" {
		t.Errorf("%s = %v with the token %q, want a 428 with none", name, run.err, tokenOf(t, run.err))
	}
	check(name, tokenRows(t, db), before, "the rows of DeleteTokens")
	run = runTokenDeletes[tokenProject](t, db, t.Context(), false, false, tokenDelete{id: "nosession", token: ask(ada, "nosession", nil)})
	if statusOf(run.err) != http.StatusPreconditionRequired || tokenOf(t, run.err) != "" {
		t.Errorf("%s, a delete on a token = %v with the token %q, want a 428 with none", name, run.err, tokenOf(t, run.err))
	}
	check(name, readRoundState(t, db, "nosession"), untouched("nosession"), "the project")

	// 10. Code's word.
	for _, tt := range []struct {
		name    string
		ctx     context.Context
		project string
	}{
		{name: "code's word, on a context with a session", ctx: ada, project: "code"},
		{name: "code's word, on a context with no session", ctx: t.Context(), project: "code-nosession"},
	} {
		name = tt.name
		before = tokenRows(t, db)
		kind = &notingKind{rows: 1, calls: nil}
		size = recordCost + 1*taskCost
		for save := 1; save <= 3; save++ {
			tokenRule.reset(false)
			counted := 0
			for _, call := range kind.calls {
				if call.method == "Count" {
					counted++
				}
			}
			run = runTokenDeletes[tokenProject](t, db, tt.ctx, false, false, tokenDelete{id: tt.project, code: true, kind: kind, size: size})
			if run.err != nil {
				t.Fatalf("%s: save %d: error = %v", name, save, run.err)
			}
			got := 0
			for _, call := range kind.calls {
				if call.method == "Count" {
					got++
				}
			}
			check(name, got-counted, 1, fmt.Sprintf("the handed kind's counts in save %d", save))
			if save < 3 {
				check(name, len(tokenRule.noted()), 1, fmt.Sprintf("the times the rule was asked in save %d", save))
			}
		}
		check(name, readRoundState(t, db, tt.project), gone, "the project")
		check(name, tokenRows(t, db), before, "the rows of DeleteTokens")
	}
	name = "code's word, with an invoice under the project"
	run = runTokenDeletes[invoicedRoundProject](t, db, ada, false, false, tokenDelete{id: "invoiced", code: true})
	if want := "This project has 1 invoice, so it can't be deleted."; statusOf(run.err) != http.StatusConflict || httpio.Message(run.err) != want {
		t.Errorf("%s = %v (%q), want a 409 saying %q", name, run.err, httpio.Message(run.err), want)
	}
	check(name, readRoundState(t, db, "invoiced"), untouched("invoiced"), "the project")

	// 11. Two deletes in one request, one on its token.
	name = "a delete on its token beside one that asks"
	token = ask(ada, "pair-a", nil)
	tokenRule.reset(false)
	run = runTokenDeletes[tokenProject](t, db, ada, true, false, tokenDelete{id: "pair-a", token: token}, tokenDelete{id: "pair-b"})
	given := asked(name, question(3, 0), run.err)
	if stored := readToken(t, db, given); stored == nil || stored.RowKey != rowMatchKey(KeySet{}.Add("ID", "pair-b")) {
		t.Errorf("%s: the token given is %+v, want one for the project that asks", name, stored)
	}
	check(name, run.runs, []string{"read-only"}, "where each run of the function read")
	check(name, len(tokenRule.noted()), 3, "the times the rule was asked: the tasks of the delete that asks alone")
	check(name, readRoundState(t, db, "pair-a"), untouched("pair-a"), "the project on its token")
	check(name, readRoundState(t, db, "pair-b"), untouched("pair-b"), "the project that asks")

	// 13. The time limit on a token.
	name = "a time limit of one nanosecond on a token"
	token = ask(ada, "timed", nil)
	expires = readToken(t, db, token).ExpiresAt
	for save := 1; save <= 6; save++ {
		run = runTokenDeletes[tokenProject](t, db, ada, false, false, tokenDelete{id: "timed", token: token, saveTime: time.Nanosecond})
		if run.err != nil {
			t.Fatalf("%s: save %d: error = %v", name, save, run.err)
		}
		want := &DeleteLeft{Deleted: 1}
		if save == 6 {
			want = nil
		}
		check(name, run.lefts, []*DeleteLeft{want}, fmt.Sprintf("what save %d left", save))
		if save < 6 {
			stored := readToken(t, db, token)
			if stored == nil || !stored.ExpiresAt.After(expires) {
				t.Fatalf("%s: save %d left the token %+v, want it there and expiring after %s", name, save, stored, expires)
			}
			expires = stored.ExpiresAt
		}
	}
	check(name, readRoundState(t, db, "timed"), gone, "the project")
	if readToken(t, db, token) != nil {
		t.Errorf("%s: the token's row is there, want it deleted in the sixth save", name)
	}
}

// TestExecuteDeleteFunc_tokenAcrossDescents holds, over the emulator, several saves on one
// token of a project whose tasks sit 25 deep in three chains, with the tests' stand-in for
// the database's refusal of a statement that joins too many tables (joinLimit): the
// question counts every task and gives the token; every save on it deletes only tasks
// with nothing under them; the saves delete as many as the question counted, and the
// record and the token go in the last.
func TestExecuteDeleteFunc_tokenAcrossDescents(t *testing.T) {
	t.Parallel()

	db := cascadeDatabase(t, "delete-token-deep")
	mutations := []*spanner.Mutation{spanner.InsertMap("CascadeProjects", map[string]any{"Id": "tall"})}
	for _, chain := range []string{"a", "b", "c"} {
		for i := 1; i <= 25; i++ {
			row := map[string]any{"Id": fmt.Sprintf("tall-%s%02d", chain, i), "ProjectId": "tall"}
			if i > 1 {
				row["ParentTaskId"] = fmt.Sprintf("tall-%s%02d", chain, i-1)
			}
			mutations = append(mutations, spanner.InsertMap("CascadeTasks", row))
		}
	}
	if _, err := db.Apply(t.Context(), mutations); err != nil {
		t.Fatalf("spanner.Client.Apply() error = %v", err)
	}
	ctx := context.WithValue(t.Context(), sessioninfo.CtxSessionInfo, &sessioninfo.SessionData{
		SessionInfo: &sessioninfo.SessionInfo{ID: impulse.Must(impulse.UUIDFromString("0190d000-0000-7000-8000-000000000003"))},
		Principal:   accesstypes.UserPrincipal("ada"),
	})
	limit := joinLimit(8, false)

	run := runTokenDeletes[roundProject](t, db, ctx, true, false, tokenDelete{id: "tall", refuses: limit})
	if want := "Delete this project and everything under it: 75 tasks. This can't be undone."; statusOf(run.err) != http.StatusPreconditionRequired || httpio.Message(run.err) != want {
		t.Fatalf("the question = %v (%q), want a 428 saying %q", run.err, httpio.Message(run.err), want)
	}
	token := tokenOf(t, run.err)
	remaining := questionRecords(t, run.err)

	// The token's write, the record and ten tasks.
	size := tokenWriteMutations + 1 + 10*(rowDeleteMutations+ChangeRecordMutations(eventsource.ProcessEvent("test")))
	for save := 1; save <= 20; save++ {
		run = runTokenDeletes[roundProject](t, db, ctx, false, false, tokenDelete{id: "tall", token: token, size: size, refuses: limit})
		if run.err != nil {
			t.Fatalf("save %d: error = %v", save, run.err)
		}
		if got := roundCount(t, db, "SELECT COUNT(*) FROM CascadeTasks t WHERE t.ProjectId = @project AND t.ParentTaskId IS NOT NULL AND NOT EXISTS (SELECT 1 FROM CascadeTasks p WHERE p.Id = t.ParentTaskId)", "tall"); got != 0 {
			t.Errorf("save %d: %d task(s) left name a task that is gone", save, got)
		}
		state := readRoundState(t, db, "tall")
		left := run.lefts[0]
		if left == nil {
			if state.record || state.tasks != 0 {
				t.Errorf("save %d, the last: the record is there = %t with %d tasks, want both gone", save, state.record, state.tasks)
			}
			if readToken(t, db, token) != nil {
				t.Error("the token's row is there, want it deleted in the last save")
			}

			return
		}
		remaining -= left.Deleted
		if !state.record || state.tasks != remaining {
			t.Errorf("save %d: the record is there = %t with %d tasks, want it there with the %d the question counted less what the saves deleted", save, state.record, state.tasks, remaining)
		}
		if readToken(t, db, token) == nil {
			t.Fatalf("save %d: the token's row is gone with the record standing", save)
		}
	}
	t.Error("twenty saves did not finish the delete")
}
