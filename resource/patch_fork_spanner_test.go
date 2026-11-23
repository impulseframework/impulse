package resource

import (
	"context"
	"sync"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/httpio"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// storedForkRow is a ForkRows row as the database holds it.
type storedForkRow struct {
	Title     string
	CreatedBy string
	UpdatedBy string
}

// readForkRow reads one ForkRows row past the library; nil when it is not there. A NULL
// column reads as the empty string.
func readForkRow(t *testing.T, client *spanner.Client, id string) *storedForkRow {
	t.Helper()

	row, err := client.Single().ReadRow(context.Background(), "ForkRows", spanner.Key{id}, []string{"Title", "CreatedBy", "UpdatedBy"})
	if err != nil {
		if spanner.ErrCode(err) == codes.NotFound {
			return nil
		}
		t.Fatalf("spanner.ReadOnlyTransaction.ReadRow(%s) error = %v", id, err)
	}
	var title string
	var createdBy, updatedBy spanner.NullString
	if err := row.Columns(&title, &createdBy, &updatedBy); err != nil {
		t.Fatalf("spanner.Row.Columns(%s) error = %v", id, err)
	}

	return &storedForkRow{Title: title, CreatedBy: createdBy.StringVal, UpdatedBy: updatedBy.StringVal}
}

// forkDatabase makes a database with the fixture's tables on the shared emulator.
func forkDatabase(t *testing.T, name string) *spanner.Client {
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
	if err := db.MigrateUp("file://testdata/patchfork/schema"); err != nil {
		t.Fatalf("initiator.SpannerDB.MigrateUp() error = %v", err)
	}

	return db.Client
}

// TestSpannerClient_fork pins update-or-create and create-unless-there over the
// emulator: each decides from the stored row and from what its own transaction wrote,
// runs the create or the update whole, and commits where two plain creates of one row
// are refused.
func TestSpannerClient_fork(t *testing.T) {
	t.Parallel()

	db := forkDatabase(t, "patch-fork")
	ctx := context.Background()
	if _, err := db.Apply(ctx, []*spanner.Mutation{
		spanner.InsertMap("ForkRows", map[string]any{"Id": "there-1", "Title": "stored", "CreatedBy": "someone"}),
		spanner.InsertMap("ForkRows", map[string]any{"Id": "there-2", "Title": "stored", "CreatedBy": "someone"}),
		spanner.InsertMap("ForkRows", map[string]any{"Id": "there-3", "Title": "stored", "CreatedBy": "someone"}),
	}); err != nil {
		t.Fatalf("spanner.Client.Apply() error = %v", err)
	}

	tests := []struct {
		name string
		// patches are buffered in order in one transaction.
		patches func() []*PatchSet[forkRow]
		id      string
		// wantRefused says the commit is refused as a conflict.
		wantRefused  bool
		wantOutcomes []Outcome
		// want is the row afterwards; nil for no row.
		want *storedForkRow
	}{
		{
			name: "update-or-create of a row that is not there creates it with the create's default",
			patches: func() []*PatchSet[forkRow] {
				return []*PatchSet[forkRow]{forkPatchOf(UpdatePatchType, "new-1", "asked").OrCreate(nil)}
			},
			id:           "new-1",
			wantOutcomes: []Outcome{OutcomeCreated},
			want:         &storedForkRow{Title: "asked", CreatedBy: "creator"},
		},
		{
			name: "update-or-create of a row that is there updates it with the update's stamp",
			patches: func() []*PatchSet[forkRow] {
				return []*PatchSet[forkRow]{forkPatchOf(UpdatePatchType, "there-1", "asked").OrCreate(nil)}
			},
			id:           "there-1",
			wantOutcomes: []Outcome{OutcomeUpdated},
			want:         &storedForkRow{Title: "asked", CreatedBy: "someone", UpdatedBy: "updater"},
		},
		{
			name: "create-unless-there of a row that is there leaves it as it is",
			patches: func() []*PatchSet[forkRow] {
				return []*PatchSet[forkRow]{forkPatchOf(CreatePatchType, "there-2", "asked").OrSkip()}
			},
			id:           "there-2",
			wantOutcomes: []Outcome{OutcomeSkipped},
			want:         &storedForkRow{Title: "stored", CreatedBy: "someone"},
		},
		{
			name: "create-unless-there of a row that is not there creates it",
			patches: func() []*PatchSet[forkRow] {
				return []*PatchSet[forkRow]{forkPatchOf(CreatePatchType, "new-2", "asked").OrSkip()}
			},
			id:           "new-2",
			wantOutcomes: []Outcome{OutcomeCreated},
			want:         &storedForkRow{Title: "asked", CreatedBy: "creator"},
		},
		{
			name: "two plain creates of one row in one transaction are refused at commit",
			patches: func() []*PatchSet[forkRow] {
				return []*PatchSet[forkRow]{
					forkPatchOf(CreatePatchType, "new-3", "first"),
					forkPatchOf(CreatePatchType, "new-3", "second"),
				}
			},
			id:          "new-3",
			wantRefused: true,
		},
		{
			name: "two create-unless-there of one row in one transaction: the second skips and the commit lands",
			patches: func() []*PatchSet[forkRow] {
				return []*PatchSet[forkRow]{
					forkPatchOf(CreatePatchType, "new-4", "first").OrSkip(),
					forkPatchOf(CreatePatchType, "new-4", "second").OrSkip(),
				}
			},
			id:           "new-4",
			wantOutcomes: []Outcome{OutcomeCreated, OutcomeSkipped},
			want:         &storedForkRow{Title: "first", CreatedBy: "creator"},
		},
		{
			name: "two update-or-create of one row in one transaction: an insert and an update in one commit",
			patches: func() []*PatchSet[forkRow] {
				return []*PatchSet[forkRow]{
					forkPatchOf(UpdatePatchType, "new-5", "first").OrCreate(nil),
					forkPatchOf(UpdatePatchType, "new-5", "second").OrCreate(nil),
				}
			},
			id:           "new-5",
			wantOutcomes: []Outcome{OutcomeCreated, OutcomeUpdated},
			want:         &storedForkRow{Title: "second", CreatedBy: "creator", UpdatedBy: "updater"},
		},
		{
			name: "a row the transaction deleted is created again",
			patches: func() []*PatchSet[forkRow] {
				gone := NewPatchSet(NewMetadata[forkRow]()).SetPatchType(DeletePatchType)
				gone.SetKey("ID", "there-3")

				return []*PatchSet[forkRow]{gone, forkPatchOf(UpdatePatchType, "there-3", "asked").OrCreate(nil)}
			},
			id:           "there-3",
			wantOutcomes: []Outcome{"", OutcomeCreated},
			want:         &storedForkRow{Title: "asked", CreatedBy: "creator"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Each case owns its row; the emulator aborts concurrent read-write
			// transactions and the client retries them.
			t.Parallel()

			client := NewSpannerClient(db)
			var outcomes []Outcome
			err := client.ExecuteFunc(ctx, func(ctx context.Context, txn ReadWriteTransaction) error {
				outcomes = nil
				for _, p := range tt.patches() {
					if err := p.Buffer(ctx, txn); err != nil {
						return err
					}
					outcomes = append(outcomes, p.Outcome())
				}

				return nil
			})
			if tt.wantRefused {
				if err == nil || !httpio.HasConflict(err) {
					t.Fatalf("ExecuteFunc() error = %v, want the commit refused as a conflict", err)
				}
			} else {
				if err != nil {
					t.Fatalf("ExecuteFunc() error = %v", err)
				}
				if diff := cmp.Diff(tt.wantOutcomes, outcomes); diff != "" {
					t.Errorf("outcomes mismatch (-want +got):\n%s", diff)
				}
			}
			if diff := cmp.Diff(tt.want, readForkRow(t, db, tt.id)); diff != "" {
				t.Errorf("row %s mismatch (-want +got):\n%s", tt.id, diff)
			}
		})
	}
}

// A transaction that aborts runs again. The row was not there on the first attempt and
// another writer has created it by the second: the patch updates, and nothing the first
// attempt's create put on it reaches the row.
func TestSpannerClient_fork_decidesAgainAfterAnAbort(t *testing.T) {
	t.Parallel()

	db := forkDatabase(t, "patch-fork-abort")
	ctx := context.Background()
	client := NewSpannerClient(db)

	// The patch is built once, outside the function the client retries.
	p := forkPatchOf(UpdatePatchType, "raced", "asked").OrCreate(nil)
	attempt := 0
	var outcomes []Outcome
	if err := client.ExecuteFunc(ctx, func(ctx context.Context, txn ReadWriteTransaction) error {
		attempt++
		if err := p.Buffer(ctx, txn); err != nil {
			return err
		}
		outcomes = append(outcomes, p.Outcome())
		if attempt == 1 {
			// Another writer creates the row, in a transaction of its own, and this
			// attempt is lost.
			if _, err := db.Apply(context.Background(), []*spanner.Mutation{
				spanner.InsertMap("ForkRows", map[string]any{"Id": "raced", "Title": "theirs", "CreatedBy": "the other writer"}),
			}); err != nil {
				return errors.Wrap(err, "spanner.Client.Apply()")
			}

			return status.Error(codes.Aborted, "forced abort")
		}

		return nil
	}); err != nil {
		t.Fatalf("ExecuteFunc() error = %v", err)
	}

	if diff := cmp.Diff([]Outcome{OutcomeCreated, OutcomeUpdated}, outcomes); diff != "" {
		t.Errorf("outcomes by attempt mismatch (-want +got):\n%s", diff)
	}
	want := &storedForkRow{Title: "asked", CreatedBy: "the other writer", UpdatedBy: "updater"}
	if diff := cmp.Diff(want, readForkRow(t, db, "raced")); diff != "" {
		t.Errorf("row mismatch (-want +got):\n%s", diff)
	}
}

// Writers racing to update-or-create one row: the read is inside each transaction, so
// exactly one of them creates the row and every other updates it, and none is refused.
func TestSpannerClient_fork_racingWriters(t *testing.T) {
	t.Parallel()

	db := forkDatabase(t, "patch-fork-race")
	ctx := context.Background()
	client := NewSpannerClient(db)

	const writers = 6
	outcomes := make([]Outcome, writers)
	errs := make([]error, writers)
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := forkPatchOf(UpdatePatchType, "contested", "asked").OrCreate(nil)
			errs[i] = p.Apply(ctx, client)
			outcomes[i] = p.Outcome()
		}()
	}
	wg.Wait()

	created := 0
	for i, err := range errs {
		if err != nil {
			t.Errorf("writer %d: Apply() error = %v", i, err)
		}
		if outcomes[i] == OutcomeCreated {
			created++
		}
	}
	if created != 1 {
		t.Errorf("%d writer(s) created the row, want exactly one: %v", created, outcomes)
	}
	want := &storedForkRow{Title: "asked", CreatedBy: "creator", UpdatedBy: "updater"}
	if diff := cmp.Diff(want, readForkRow(t, db, "contested")); diff != "" {
		t.Errorf("row mismatch (-want +got):\n%s", diff)
	}
}

// A resource that keeps change records: each case writes the record of the path it ran,
// and a row its own transaction created is updated like any other, its record's old
// values the ones the create gave.
func TestSpannerClient_fork_changeRecords(t *testing.T) {
	t.Parallel()

	db := forkDatabase(t, "patch-fork-tracked")
	ctx := context.Background()
	client := NewSpannerClient(db)

	tracked := func(patchType PatchType, id, title string) *PatchSet[trackedForkRow] {
		p := NewPatchSet(NewMetadata[trackedForkRow]()).SetPatchType(patchType)
		p.SetKey("ID", id)
		p.Set("Title", title)

		return p
	}
	records := func(id string) int64 {
		t.Helper()

		var n int64
		stmt := spanner.NewStatement("SELECT COUNT(*) FROM DataChangeEvents WHERE TableName = 'TrackedForkRows' AND RowId = @id")
		stmt.Params["id"] = id
		if err := db.Single().Query(ctx, stmt).Do(func(row *spanner.Row) error {
			return row.Column(0, &n)
		}); err != nil {
			t.Fatalf("counting change records of %s: %v", id, err)
		}

		return n
	}

	// Created, then updated: one record each.
	for i, title := range []string{"first", "second"} {
		p := tracked(UpdatePatchType, "row-1", title).OrCreate(nil)
		if err := p.Apply(ctx, client, "test"); err != nil {
			t.Fatalf("Apply() %d error = %v", i, err)
		}
		if got, want := records("row-1"), int64(i+1); got != want {
			t.Errorf("after Apply() %d the row has %d change record(s), want %d", i, got, want)
		}
	}

	// The same values again, as a task delivered twice asks: no error, and no record,
	// for an update-or-create and for a plain update alike.
	for name, repeat := range map[string]*PatchSet[trackedForkRow]{
		"an update-or-create": tracked(UpdatePatchType, "row-1", "second").OrCreate(nil),
		"a plain update":      tracked(UpdatePatchType, "row-1", "second"),
	} {
		if err := repeat.Apply(ctx, client, "test"); err != nil {
			t.Errorf("%s that changes nothing: Apply() error = %v, want none", name, err)
		}
		if got := records("row-1"); got != 2 {
			t.Errorf("after %s that changes nothing the row has %d change record(s), want the 2 it had", name, got)
		}
	}
	// The low-level insert-or-update keeps its refusal.
	lowLevel := tracked(CreateOrUpdatePatchType, "row-1", "second")
	if err := lowLevel.Apply(ctx, client, "test"); err == nil || !httpio.HasBadRequest(err) {
		t.Errorf("a low-level insert-or-update that changes nothing: Apply() error = %v, want its 400", err)
	}
	// One whose row is not there creates it, and one that changes it then changes it: one
	// record each.
	for i, title := range []string{"made", "changed"} {
		if err := tracked(CreateOrUpdatePatchType, "row-3", title).Apply(ctx, client, "test"); err != nil {
			t.Fatalf("a low-level insert-or-update: Apply() %d error = %v", i, err)
		}
		if got, want := records("row-3"), int64(i+1); got != want {
			t.Errorf("after a low-level insert-or-update's Apply() %d the row has %d change record(s), want %d", i, got, want)
		}
	}

	// A row this transaction created: a plain update of it commits, and so does an
	// update-or-create, which updates; each writes its own record.
	title := func(id string) string {
		t.Helper()

		row, err := db.Single().ReadRow(ctx, "TrackedForkRows", spanner.Key{id}, []string{"Title"})
		if err != nil {
			t.Fatalf("reading %s: %v", id, err)
		}
		var title string
		if err := row.Column(0, &title); err != nil {
			t.Fatalf("reading the title of %s: %v", id, err)
		}

		return title
	}
	for _, second := range []struct {
		name    string
		id      string
		patch   *PatchSet[trackedForkRow]
		outcome Outcome
	}{
		{"a plain update", "row-2", tracked(UpdatePatchType, "row-2", "again"), ""},
		{"an update-or-create", "row-4", tracked(UpdatePatchType, "row-4", "again").OrCreate(nil), OutcomeUpdated},
	} {
		if err := client.ExecuteFunc(ctx, func(ctx context.Context, txn ReadWriteTransaction) error {
			if err := tracked(CreatePatchType, second.id, "new").Buffer(ctx, txn, "test"); err != nil {
				return err
			}

			return second.patch.Buffer(ctx, txn, "test")
		}); err != nil {
			t.Errorf("%s of a row its transaction created: ExecuteFunc() error = %v", second.name, err)

			continue
		}
		if got := title(second.id); got != "again" {
			t.Errorf("%s of a row its transaction created: the row holds %q, want %q", second.name, got, "again")
		}
		if got := records(second.id); got != 2 {
			t.Errorf("%s of a row its transaction created: the row has %d change record(s), want 2", second.name, got)
		}
		if got := second.patch.Outcome(); got != second.outcome {
			t.Errorf("%s of a row its transaction created: Outcome() = %q, want %q", second.name, got, second.outcome)
		}
	}

	// A create-unless-there of a row this transaction created skips.
	var skipped Outcome
	if err := client.ExecuteFunc(ctx, func(ctx context.Context, txn ReadWriteTransaction) error {
		if err := tracked(CreatePatchType, "row-5", "new").Buffer(ctx, txn, "test"); err != nil {
			return err
		}
		p := tracked(CreatePatchType, "row-5", "again").OrSkip()
		if err := p.Buffer(ctx, txn, "test"); err != nil {
			return err
		}
		skipped = p.Outcome()

		return nil
	}); err != nil {
		t.Fatalf("a create and a create-unless-there of one row: ExecuteFunc() error = %v", err)
	}
	if skipped != OutcomeSkipped {
		t.Errorf("the create-unless-there's Outcome() = %q, want %q", skipped, OutcomeSkipped)
	}
	if got := records("row-5"); got != 1 {
		t.Errorf("row-5 has %d change record(s), want the create's one", got)
	}
}
