package resource

import (
	"context"
	"slices"

	"github.com/go-playground/errors/v5"
)

// The work a transaction owes once it has committed. A write the database holds is not
// always the whole of a change: a permission engine answers from a snapshot until it is
// told its tables changed, and a cache holds a row until it is told the row moved. The
// code that buffers such a write registers the telling on the transaction (AfterCommit),
// so it runs when the commit lands and never when it does not, and whoever composes the
// write with others in one transaction has nothing to remember.

// afterCommit is a transaction's record of the functions to run once it has committed.
type afterCommit struct {
	hooks []func(ctx context.Context)
	keys  []any
}

// newAfterCommit returns an empty record.
func newAfterCommit() *afterCommit {
	return &afterCommit{}
}

// record notes f. A registration under a key the record already holds is dropped; a nil
// key never matches another.
func (a *afterCommit) record(key any, f func(ctx context.Context)) {
	if key != nil {
		if slices.Contains(a.keys, key) {
			return
		}
		a.keys = append(a.keys, key)
	}
	a.hooks = append(a.hooks, f)
}

// run calls the recorded functions in the order they were registered, detached from the
// request's cancellation: the rows are committed whatever the client did since.
func (a *afterCommit) run(ctx context.Context) {
	if len(a.hooks) == 0 {
		return
	}
	ctx = context.WithoutCancel(ctx)
	for _, hook := range a.hooks {
		hook(ctx)
	}
}

// commitRecorder is how AfterCommit reaches a transaction's record: the Spanner and Mock
// wrappers satisfy it. A transaction that does not (Postgres) cannot promise the call, so
// the registration is refused instead of dropped.
type commitRecorder interface {
	recordAfterCommit(key any, f func(ctx context.Context))
}

// AfterCommit registers f to run once the transaction has committed, after the commit
// and before ExecuteFunc returns, in registration order. A transaction that does not
// commit, for any reason, runs nothing, and a transaction Spanner retries runs only what
// the attempt that committed registered. f runs detached from the request's
// cancellation and answers nothing: the rows are committed, so a failure in it is f's
// own to log.
//
// A transaction an application wraps itself and commits outside ExecuteFunc has no
// executor to make the calls: run them with Committed after the commit.
func AfterCommit(txn ReadWriteTransaction, f func(ctx context.Context)) error {
	return afterCommitOnce(txn, nil, f)
}

// AfterCommitOnce is AfterCommit for work that is owed once however many writes ask for
// it: a registration under a key the transaction already holds is dropped. The key is
// compared with ==, so it is a comparable value that names the work (the pointer of the
// engine to tell, a constant).
func AfterCommitOnce(txn ReadWriteTransaction, key any, f func(ctx context.Context)) error {
	if key == nil {
		return errors.New("resource.AfterCommitOnce(): the key is nil; a key names the work that is owed once, and AfterCommit registers without one")
	}

	return afterCommitOnce(txn, key, f)
}

func afterCommitOnce(txn ReadWriteTransaction, key any, f func(ctx context.Context)) error {
	if f == nil {
		return errors.New("resource.AfterCommit(): the function is nil")
	}
	recorder, ok := txn.(commitRecorder)
	if !ok {
		return errors.Newf("resource.AfterCommit(): a %T cannot promise a call after its commit; run the write in a transaction of the resource client (ExecuteFunc)", txn)
	}
	recorder.recordAfterCommit(key, f)

	return nil
}
