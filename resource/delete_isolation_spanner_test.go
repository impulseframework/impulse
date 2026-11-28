package resource

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/internal/spannertxn"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"google.golang.org/grpc/codes"
)

// TestRepeatableRead_onTheEmulator runs a transaction at repeatable read on the emulator
// every test of a save runs on: it reads a row by a query and buffers the insert of
// another, and both rows are there after the commit.
func TestRepeatableRead_onTheEmulator(t *testing.T) {
	t.Parallel()

	db := cascadeDatabase(t, "repeatable-read")
	if _, err := db.Apply(t.Context(), []*spanner.Mutation{
		spanner.Insert("CascadeProjects", []string{"Id"}, []any{"read-first"}),
	}); err != nil {
		t.Fatalf("spanner.Client.Apply() error = %v", err)
	}
	var read []string
	_, err := spannertxn.RepeatableRead(t.Context(), db, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		read = nil
		if err := txn.Query(ctx, spanner.Statement{
			SQL:    "SELECT Id FROM CascadeProjects WHERE Id = @id",
			Params: map[string]any{"id": "read-first"},
		}).Do(func(row *spanner.Row) error {
			var id string
			if err := row.Columns(&id); err != nil {
				return errors.Wrap(err, "spanner.Row.Columns()")
			}
			read = append(read, id)

			return nil
		}); err != nil {
			return errors.Wrap(err, "spanner.RowIterator.Do()")
		}

		if err := txn.BufferWrite([]*spanner.Mutation{
			spanner.Insert("CascadeProjects", []string{"Id"}, []any{"written-after"}),
		}); err != nil {
			return errors.Wrap(err, "spanner.ReadWriteTransaction.BufferWrite()")
		}

		return nil
	})
	if err != nil {
		t.Fatalf("spannertxn.RepeatableRead() error = %v, want the insert committed", err)
	}
	if len(read) != 1 || read[0] != "read-first" {
		t.Errorf("spannertxn.RepeatableRead() read %v, want [read-first]", read)
	}
	for _, id := range []string{"read-first", "written-after"} {
		if got := roundCount(t, db, "SELECT COUNT(*) FROM CascadeProjects WHERE Id = @project", id); got != 1 {
			t.Errorf("spannertxn.RepeatableRead(): %d projects %q after the commit, want one", got, id)
		}
	}
}

// TestRepeatableRead_aForeignKeyStillRefuses deletes a client an order still names by a
// foreign key with no action on delete, at repeatable read and at the serializable level:
// the database refuses both commits with the same code, and both rows stay.
func TestRepeatableRead_aForeignKeyStillRefuses(t *testing.T) {
	t.Parallel()

	db := cascadeDatabase(t, "repeatable-read-fk")
	if _, err := db.Apply(t.Context(), []*spanner.Mutation{
		spanner.Insert("CascadeClients", []string{"Id"}, []any{"named"}),
		spanner.Insert("CascadeOrders", []string{"Id", "ClientId"}, []any{"naming", "named"}),
	}); err != nil {
		t.Fatalf("spanner.Client.Apply() error = %v", err)
	}
	deleteClient := func(_ context.Context, txn *spanner.ReadWriteTransaction) error {
		if err := txn.BufferWrite([]*spanner.Mutation{spanner.Delete("CascadeClients", spanner.Key{"named"})}); err != nil {
			return errors.Wrap(err, "spanner.ReadWriteTransaction.BufferWrite()")
		}

		return nil
	}
	_, repeatable := spannertxn.RepeatableRead(t.Context(), db, deleteClient)
	_, serializable := spannertxn.Serializable(t.Context(), db, deleteClient)
	if repeatable == nil {
		t.Fatal("spannertxn.RepeatableRead() error = nil, want the foreign key to refuse the commit")
	}
	if serializable == nil {
		t.Fatal("spannertxn.Serializable() error = nil, want the foreign key to refuse the commit")
	}
	if got, want := spanner.ErrCode(repeatable), spanner.ErrCode(serializable); got != want {
		t.Errorf("spannertxn.RepeatableRead() error code = %v, want %v as at the serializable level (%v)", got, want, repeatable)
	}
	if got := spanner.ErrCode(repeatable); got != codes.FailedPrecondition {
		t.Errorf("spannertxn.RepeatableRead() error code = %v, want %v (%v)", got, codes.FailedPrecondition, repeatable)
	}
	if got := roundCount(t, db, "SELECT COUNT(*) FROM CascadeClients WHERE Id = @project", "named"); got != 1 {
		t.Errorf("%d clients named after the refused deletes, want one", got)
	}
	if got := roundCount(t, db, "SELECT COUNT(*) FROM CascadeOrders WHERE ClientId = @project", "named"); got != 1 {
		t.Errorf("%d orders naming the client after the refused deletes, want one", got)
	}
}

// TestExecuteDeleteFunc_theSaveRunsAtRepeatableRead holds which runs of a request's
// function are handed a transaction opened at repeatable read: the run of a save
// (DeleteRunSave) is; a plain run, both runs of a request that asks and finds nothing to
// ask, and ExecuteFunc's run are not.
func TestExecuteDeleteFunc_theSaveRunsAtRepeatableRead(t *testing.T) {
	t.Parallel()

	client := NewSpannerClient(cascadeDatabase(t, "save-repeatable-read"))
	for _, tt := range []struct {
		name string
		run  func(f func(context.Context, ReadWriteTransaction) error) error
		want []string
	}{
		{
			name: "a save",
			run: func(f func(context.Context, ReadWriteTransaction) error) error {
				return client.ExecuteDeleteFunc(t.Context(), DeleteRunSave, f)
			},
			want: []string{"in the save, at repeatable read"},
		},
		{
			name: "a plain run",
			run: func(f func(context.Context, ReadWriteTransaction) error) error {
				return client.ExecuteDeleteFunc(t.Context(), DeleteRunPlain, f)
			},
			want: []string{"in the save, serializable"},
		},
		{
			name: "a request that asks, with nothing to ask",
			run: func(f func(context.Context, ReadWriteTransaction) error) error {
				return client.ExecuteDeleteFunc(t.Context(), DeleteRunAsks, f)
			},
			want: []string{"read-only, serializable", "in the save, serializable"},
		},
		{
			name: "ExecuteFunc",
			run: func(f func(context.Context, ReadWriteTransaction) error) error {
				return client.ExecuteFunc(t.Context(), f)
			},
			want: []string{"in the save, serializable"},
		},
	} {
		var runs []string
		err := tt.run(func(_ context.Context, txn ReadWriteTransaction) error {
			wrapped, ok := txn.(*SpannerReadWriteTransaction)
			if !ok {
				return errors.Newf("the function was handed a %T", txn)
			}
			at, _ := readsAt(t, txn.SpannerReadOnlyTransaction())
			level := "serializable"
			if wrapped.repeatableRead {
				level = "at repeatable read"
			}
			runs = append(runs, at+", "+level)

			return nil
		})
		if err != nil {
			t.Errorf("%s: error = %v", tt.name, err)
		}
		if diff := cmp.Diff(tt.want, runs); diff != "" {
			t.Errorf("%s: where each run of the function read, and at which level (-want +got):\n%s", tt.name, diff)
		}
	}
}

// TestExecuteDeleteFunc_aSaveOnATokenThatDoesNotHold holds a save (DeleteRunSave) whose
// token does not hold to what a plain run's is: the save's transaction ends, the request
// is asked, and the answer is the question with a new token; nothing is deleted.
func TestExecuteDeleteFunc_aSaveOnATokenThatDoesNotHold(t *testing.T) {
	t.Parallel()

	db := cascadeDatabase(t, "save-token-refused")
	var staged []*spanner.Mutation
	for _, project := range []string{"saved", "plain"} {
		staged = append(staged, spanner.InsertMap("CascadeProjects", map[string]any{"Id": project}))
		for i := 1; i <= 2; i++ {
			staged = append(staged, spanner.InsertMap("CascadeTasks", map[string]any{"Id": fmt.Sprintf("%s-%d", project, i), "ProjectId": project}))
		}
	}
	if _, err := db.Apply(t.Context(), staged); err != nil {
		t.Fatalf("spanner.Client.Apply() error = %v", err)
	}
	ctx := context.WithValue(t.Context(), sessioninfo.CtxSessionInfo, &sessioninfo.SessionData{
		SessionInfo: &sessioninfo.SessionInfo{ID: impulse.Must(impulse.UUIDFromString("0190d000-0000-7000-8000-000000000004"))},
		Principal:   accesstypes.UserPrincipal("ada"),
	})
	const want = "Delete this project and everything under it: 2 tasks. This can't be undone."
	for _, tt := range []struct {
		project string
		run     DeleteRun
	}{
		{project: "saved", run: DeleteRunSave},
		{project: "plain", run: DeleteRunPlain},
	} {
		run := runTokenDeletes[roundProject](t, db, ctx, tt.run, false, tokenDelete{id: tt.project, token: "no-such-token"})
		if statusOf(run.err) != http.StatusPreconditionRequired || httpio.Message(run.err) != want {
			t.Errorf("%s: the answer = %v (%q), want a 428 saying %q", tt.project, run.err, httpio.Message(run.err), want)
		}
		if token := tokenOf(t, run.err); token == "" || token == "no-such-token" {
			t.Errorf("%s: the question gave the token %q, want a new one", tt.project, token)
		}
		if diff := cmp.Diff([]string{"in the save", "read-only"}, run.runs); diff != "" {
			t.Errorf("%s: where each run of the function read (-want +got):\n%s", tt.project, diff)
		}
		if diff := cmp.Diff(roundState{record: true, tasks: 2, comments: 0}, readRoundState(t, db, tt.project), cmp.AllowUnexported(roundState{})); diff != "" {
			t.Errorf("%s: the project (-want +got):\n%s", tt.project, diff)
		}
	}
}
