package resource

import (
	"context"
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestAfterCommit pins the registration over the Mock client, whose ExecuteFunc follows
// the Spanner client's: the functions run once the function returns nil, in registration
// order, a function that fails runs none, a registration under a key the transaction
// holds is dropped, and the work runs detached from the request's cancellation.
func TestAfterCommit(t *testing.T) {
	t.Parallel()

	type engine struct{ name string }
	first, second := &engine{"first"}, &engine{"second"}

	tests := []struct {
		name string
		fn   func(t *testing.T, txn ReadWriteTransaction, ran *[]string) error
		// wantErr is the function's own failure; nil for a commit.
		wantErr bool
		want    []string
	}{
		{
			name: "a commit runs what was registered, in order",
			fn: func(t *testing.T, txn ReadWriteTransaction, ran *[]string) error {
				t.Helper()
				mustAfterCommit(t, txn, func(context.Context) { *ran = append(*ran, "a") })
				mustAfterCommit(t, txn, func(context.Context) { *ran = append(*ran, "b") })

				return nil
			},
			want: []string{"a", "b"},
		},
		{
			name: "a function that fails runs nothing",
			fn: func(t *testing.T, txn ReadWriteTransaction, ran *[]string) error {
				t.Helper()
				mustAfterCommit(t, txn, func(context.Context) { *ran = append(*ran, "a") })

				return ErrDryRun
			},
			wantErr: true,
		},
		{
			name: "work owed once is registered once per key",
			fn: func(t *testing.T, txn ReadWriteTransaction, ran *[]string) error {
				t.Helper()
				for _, e := range []*engine{first, second, first, first, second} {
					if err := AfterCommitOnce(txn, e, func(context.Context) { *ran = append(*ran, e.name) }); err != nil {
						t.Fatalf("AfterCommitOnce() error = %v", err)
					}
				}
				mustAfterCommit(t, txn, func(context.Context) { *ran = append(*ran, "unkeyed") })
				mustAfterCommit(t, txn, func(context.Context) { *ran = append(*ran, "unkeyed") })

				return nil
			},
			want: []string{"first", "second", "unkeyed", "unkeyed"},
		},
		{
			name: "a transaction that registers nothing runs nothing",
			fn: func(*testing.T, ReadWriteTransaction, *[]string) error {
				return nil
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var ran []string
			client := NewMockClient(&bufferingTxn{}, nil, nil)
			err := client.ExecuteFunc(t.Context(), func(_ context.Context, txn ReadWriteTransaction) error {
				return tt.fn(t, txn, &ran)
			})
			if (err != nil) != tt.wantErr {
				t.Fatalf("ExecuteFunc() error = %v, wantErr %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(tt.want, ran); diff != "" {
				t.Errorf("work run after the commit (-want +got):\n%s", diff)
			}
		})
	}
}

// TestAfterCommit_detached pins that the work outlives the request: the commit landed,
// so a request canceled since still owes it.
func TestAfterCommit_detached(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	var ctxErr error
	ran := false
	client := NewMockClient(&bufferingTxn{}, nil, nil)
	if err := client.ExecuteFunc(ctx, func(_ context.Context, txn ReadWriteTransaction) error {
		mustAfterCommit(t, txn, func(ctx context.Context) {
			ran = true
			ctxErr = ctx.Err()
		})
		cancel()

		return nil
	}); err != nil {
		t.Fatalf("ExecuteFunc() error = %v", err)
	}
	if !ran {
		t.Fatal("the registered work did not run")
	}
	if ctxErr != nil {
		t.Errorf("the work's context is done (%v), want it detached from the request's cancellation", ctxErr)
	}
}

// TestAfterCommit_refusals pins what a registration refuses: a transaction that cannot
// promise the call (the registration is refused, never dropped), a nil function, and a
// nil key for work owed once.
func TestAfterCommit_refusals(t *testing.T) {
	t.Parallel()

	noop := func(context.Context) {}
	txn := NewMockReadWriteTransaction(&bufferingTxn{})

	tests := []struct {
		name string
		call func() error
		want string
	}{
		{name: "a transaction with no record", call: func() error { return AfterCommit(&bufferingTxn{}, noop) }, want: "cannot promise a call after its commit"},
		{name: "a nil function", call: func() error { return AfterCommit(txn, nil) }, want: "the function is nil"},
		{name: "a nil key", call: func() error { return AfterCommitOnce(txn, nil, noop) }, want: "the key is nil"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.call()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want one saying %q", err, tt.want)
			}
		})
	}
}

// TestReadWriteTransaction_Committed_outsideExecutor pins the way out for a transaction
// committed outside ExecuteFunc: the caller runs the work itself, after its commit.
func TestReadWriteTransaction_Committed_outsideExecutor(t *testing.T) {
	t.Parallel()

	txn := NewMockReadWriteTransaction(&bufferingTxn{})
	var ran []string
	mustAfterCommit(t, txn, func(context.Context) { ran = append(ran, "a") })
	if len(ran) != 0 {
		t.Fatalf("work ran at registration: %v", ran)
	}
	txn.Committed(t.Context())
	if diff := cmp.Diff([]string{"a"}, ran); diff != "" {
		t.Errorf("work run by Committed() (-want +got):\n%s", diff)
	}
}

// TestSpannerClient_ExecuteFunc_afterCommit pins the registration over the emulator: a
// commit runs the work once the rows are in, a commit Spanner refuses and a function
// that fails run nothing, and a retried transaction runs what its committing attempt
// registered, not its first.
func TestSpannerClient_ExecuteFunc_afterCommit(t *testing.T) {
	t.Parallel()

	container := spannerEmulator(t)
	ctx := context.Background()
	db, err := container.CreateDatabase(ctx, "after-commit")
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
	if err := db.MigrateUp("file://testdata/filerelease/schema"); err != nil {
		t.Fatalf("initiator.SpannerDB.MigrateUp() error = %v", err)
	}
	if _, err := db.Apply(ctx, []*spanner.Mutation{
		spanner.InsertMap("FileRows", map[string]any{"Id": "row-1", "Title": "one", "StoreKey": "key-1"}),
		spanner.InsertMap("FileRows", map[string]any{"Id": "row-2", "Title": "two", "StoreKey": "key-2"}),
		spanner.InsertMap("FileRefs", map[string]any{"Id": "ref-1", "FileRowId": "row-2"}),
	}); err != nil {
		t.Fatalf("spanner.Client.Apply() error = %v", err)
	}

	insert := func(id string) func(ctx context.Context, txn ReadWriteTransaction) error {
		return func(ctx context.Context, txn ReadWriteTransaction) error {
			p := NewPatchSet(NewMetadata[fileRow]()).SetPatchType(CreatePatchType)
			p.SetKey("ID", id)
			p.Set("Title", id)
			p.Set("StoreKey", "key-"+id)

			return p.Buffer(ctx, txn)
		}
	}

	tests := []struct {
		name    string
		fn      func(attempt int, ran *[]string) func(ctx context.Context, txn ReadWriteTransaction) error
		wantErr bool
		want    []string
		// wantRow is a row the case writes, present exactly when the work ran.
		wantRow string
	}{
		{
			name: "a commit runs the work once the row is in",
			fn: func(_ int, ran *[]string) func(ctx context.Context, txn ReadWriteTransaction) error {
				return func(ctx context.Context, txn ReadWriteTransaction) error {
					if err := insert("committed")(ctx, txn); err != nil {
						return err
					}

					return AfterCommit(txn, func(context.Context) {
						seen := "missing"
						if rowExists(t, db.Client, "committed") {
							seen = "present"
						}
						*ran = append(*ran, "row "+seen)
					})
				}
			},
			want:    []string{"row present"},
			wantRow: "committed",
		},
		{
			name: "a commit the database refuses runs nothing",
			fn: func(_ int, ran *[]string) func(ctx context.Context, txn ReadWriteTransaction) error {
				return func(ctx context.Context, txn ReadWriteTransaction) error {
					// row-2 is referenced by ref-1: the delete is refused at commit.
					p := NewPatchSet(NewMetadata[fileRow]()).SetPatchType(DeletePatchType)
					p.SetKey("ID", "row-2")
					if err := p.Buffer(ctx, txn); err != nil {
						return err
					}

					return AfterCommit(txn, func(context.Context) { *ran = append(*ran, "ran") })
				}
			},
			wantErr: true,
		},
		{
			name: "a function that fails after registering runs nothing",
			fn: func(_ int, ran *[]string) func(ctx context.Context, txn ReadWriteTransaction) error {
				return func(_ context.Context, txn ReadWriteTransaction) error {
					if err := AfterCommit(txn, func(context.Context) { *ran = append(*ran, "ran") }); err != nil {
						return err
					}

					return errors.New("the function fails")
				}
			},
			wantErr: true,
		},
		{
			name: "a retried transaction runs what the committing attempt registered",
			fn: func(attempt int, ran *[]string) func(ctx context.Context, txn ReadWriteTransaction) error {
				return func(ctx context.Context, txn ReadWriteTransaction) error {
					if attempt == 1 {
						if err := AfterCommit(txn, func(context.Context) { *ran = append(*ran, "first attempt") }); err != nil {
							return err
						}

						return status.Error(codes.Aborted, "forced abort")
					}
					if err := insert("retried")(ctx, txn); err != nil {
						return err
					}

					return AfterCommit(txn, func(context.Context) { *ran = append(*ran, "committing attempt") })
				}
			},
			want:    []string{"committing attempt"},
			wantRow: "retried",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := NewSpannerClient(db.Client, WithFileStore(&fakeStore{}))
			var ran []string
			attempt := 0
			err := client.ExecuteFunc(ctx, func(ctx context.Context, txn ReadWriteTransaction) error {
				attempt++

				return tt.fn(attempt, &ran)(ctx, txn)
			})
			if (err != nil) != tt.wantErr {
				t.Fatalf("ExecuteFunc() error = %v, wantErr %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(tt.want, ran); diff != "" {
				t.Errorf("work run after the commit (-want +got):\n%s", diff)
			}
			if tt.wantRow != "" && !rowExists(t, db.Client, tt.wantRow) {
				t.Errorf("row %s is missing, want it committed", tt.wantRow)
			}
		})
	}
}

// mustAfterCommit registers f, failing the test when the transaction refuses it.
func mustAfterCommit(t *testing.T, txn ReadWriteTransaction, f func(ctx context.Context)) {
	t.Helper()
	if err := AfterCommit(txn, f); err != nil {
		t.Fatalf("AfterCommit() error = %v", err)
	}
}
