package storetest

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
)

// Transaction opens a transaction of the application's on the store's database, as the
// frame of a request does, reads and writes in it so that it is open at the database,
// runs f under the transaction's context, and rolls the transaction back. Each store's
// tests supply it for their database. A database that makes the client run the
// transaction again runs f again.
type Transaction func(t *testing.T, f func(ctx context.Context))

// engineBound is the heartbeat interval of the engine whose bound the suite waits out
// on the wall clock.
const engineBound = 300 * time.Millisecond

// tenant1Request is where the suite's checks are made: a request in tenant1.
var tenant1Request = accesstypes.DomainScope("tenant1")

// RunEngine holds a permission engine over an empty, ready store to what a check made
// inside an open transaction of another request sees. A role is taken away and the
// write committed; then, while a request is inside its transaction, the engine learns
// of it, and that request's check, made under the transaction's context, is the first
// after. It must see the role gone, in each way the engine learns: told of a write of
// its own instance's, hinted by another instance, and told nothing while the heartbeat
// interval passes.
//
// The engine reads the store on its own context whoever asks, so the read these checks
// are answered from, the check's own or the engine's loop's, begins while the
// transaction is open and runs beside it, never under it. A store whose client refuses
// a read under a transaction's context, or whose database does not serve a read beside
// an open transaction, fails here: the read fails or does not return, the failure
// reaches the reload-error handler, and the check answers from the snapshot before the
// change.
func RunEngine(t *testing.T, store access.Store, within Transaction) {
	t.Helper()
	ctx := t.Context()

	if err := store.InsertRole(ctx, tenant1, editor, editorDescription); err != nil {
		t.Fatalf("InsertRole() error = %v", err)
	}
	if err := store.InsertGrant(ctx, tenant1, editor, readPerm, employees, "", ""); err != nil {
		t.Fatalf("InsertGrant() error = %v", err)
	}
	for _, user := range []accesstypes.User{alice, bob, carol} {
		if err := store.InsertUserRole(ctx, tenant1, user, editor, accesstypes.Period{}); err != nil {
			t.Fatalf("InsertUserRole(%q) error = %v", user, err)
		}
	}

	t.Run("taken away on this instance", func(t *testing.T) { runEngineLocalWrite(t, store, within) })
	t.Run("taken away on another instance and announced", func(t *testing.T) { runEngineHint(t, store, within) })
	t.Run("taken away on another instance with nothing announced", func(t *testing.T) { runEngineBound(t, store, within) })
}

// runEngineLocalWrite: a request of this instance takes the role away and commits; the
// engine is told (Client.PolicyChanged, as the manager's own writes tell it) while
// another request is inside its transaction, and that request's check is the first
// after.
func runEngineLocalWrite(t *testing.T, store access.Store, within Transaction) {
	t.Helper()
	ctx := t.Context()

	counted := &readCounter{Store: store}
	client, reported := newEngine(t, counted, access.WithHeartbeatInterval(time.Hour))
	if !readsEmployees(ctx, t, client, alice) {
		t.Fatalf("%s does not read employees before the role is taken away", alice)
	}
	if err := store.DeleteUserRole(ctx, tenant1, alice, editor); err != nil {
		t.Fatalf("DeleteUserRole() error = %v", err)
	}
	reads := counted.reads.Load()

	var runs int64
	within(t, func(txnCtx context.Context) {
		runs++
		client.PolicyChanged()
		if readsEmployees(txnCtx, t, client, alice) {
			t.Errorf("the first check after the write, made inside an open transaction, still let %s read employees", alice)
		}
	})

	if got := counted.reads.Load() - reads; got != runs {
		t.Errorf("policy reads = %d for %d times the engine was told of the write, want one each, between the check and the engine's loop", got, runs)
	}
	if got := reported.list(); len(got) != 0 {
		t.Errorf("reload errors reported = %v, want none", got)
	}
}

// runEngineHint: another instance takes the role away and announces it; the hint
// reaches this instance while a request of its own is inside its transaction, and that
// request's check is the first after the hint.
func runEngineHint(t *testing.T, store access.Store, within Transaction) {
	t.Helper()
	ctx := t.Context()

	hints := &hintChannel{watching: make(chan struct{})}
	counted := &readCounter{Store: store}
	client, reported := newEngine(t, counted, access.WithHeartbeatInterval(time.Hour), access.WithChangeSignal(hints))
	if !readsEmployees(ctx, t, client, bob) {
		t.Fatalf("%s does not read employees before the role is taken away", bob)
	}
	select {
	case <-hints.watching:
	case <-time.After(10 * time.Second):
		t.Fatal("the engine never watched its change signal")
	}
	// The other instance's write, straight to the store.
	if err := store.DeleteUserRole(ctx, tenant1, bob, editor); err != nil {
		t.Fatalf("DeleteUserRole() error = %v", err)
	}
	reads := counted.reads.Load()

	var runs int64
	within(t, func(txnCtx context.Context) {
		runs++
		hints.deliver()
		if readsEmployees(txnCtx, t, client, bob) {
			t.Errorf("the first check after the hint, made inside an open transaction, still let %s read employees", bob)
		}
	})

	if got := counted.reads.Load() - reads; got != runs {
		t.Errorf("policy reads = %d for %d hints, want one each, between the check and the engine's loop", got, runs)
	}
	if got := reported.list(); len(got) != 0 {
		t.Errorf("reload errors reported = %v, want none", got)
	}
}

// runEngineBound: another instance takes the role away and nothing tells this one; a
// request of its own is inside its transaction while the heartbeat interval passes,
// and its check past the interval is answered from a read that began inside the
// transaction.
func runEngineBound(t *testing.T, store access.Store, within Transaction) {
	t.Helper()
	ctx := t.Context()

	client, reported := newEngine(t, store, access.WithHeartbeatInterval(engineBound))
	if !readsEmployees(ctx, t, client, carol) {
		t.Fatalf("%s does not read employees before the role is taken away", carol)
	}
	if err := store.DeleteUserRole(ctx, tenant1, carol, editor); err != nil {
		t.Fatalf("DeleteUserRole() error = %v", err)
	}

	within(t, func(txnCtx context.Context) {
		// Whatever a check is answered from was read no longer ago than the interval:
		// past the interval, from a read that began after the transaction opened.
		time.Sleep(engineBound + 100*time.Millisecond)
		if readsEmployees(txnCtx, t, client, carol) {
			t.Errorf("a check made inside an open transaction, past the heartbeat interval, still let %s read employees", carol)
		}
	})

	if got := reported.list(); len(got) != 0 {
		t.Errorf("reload errors reported = %v, want none", got)
	}
}

// readCounter counts the reads of the policy made of a store.
type readCounter struct {
	access.Store
	reads atomic.Int64
}

func (c *readCounter) ReadPolicy(ctx context.Context) (*policy.Records, error) {
	c.reads.Add(1)
	records, err := c.Store.ReadPolicy(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "access.Store.ReadPolicy()")
	}

	return records, nil
}

// hintChannel is a change signal a test delivers hints on by hand: what an engine
// watches when the test is the other instance.
type hintChannel struct {
	mu       sync.Mutex
	onChange func()
	watching chan struct{}
	once     sync.Once
}

func (h *hintChannel) Announce(context.Context) error {
	return nil
}

func (h *hintChannel) Watch(ctx context.Context, onChange func()) error {
	h.mu.Lock()
	h.onChange = onChange
	h.mu.Unlock()
	h.once.Do(func() { close(h.watching) })
	<-ctx.Done()

	return nil
}

// deliver hands the engine a hint, as its watch does when another instance announces.
func (h *hintChannel) deliver() {
	h.mu.Lock()
	onChange := h.onChange
	h.mu.Unlock()
	onChange()
}

// reportedErrors collects what an engine hands its reload-error handler.
type reportedErrors struct {
	mu   sync.Mutex
	errs []error
}

func (r *reportedErrors) collect(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errs = append(r.errs, err)
}

func (r *reportedErrors) list() []error {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]error(nil), r.errs...)
}

// newEngine builds a Client over the store, closed when the test ends, and what it
// reports to its reload-error handler.
func newEngine(t *testing.T, store access.Store, opts ...access.Option) (*access.Client, *reportedErrors) {
	t.Helper()

	reported := &reportedErrors{}
	client, err := access.New(store, append(opts, access.WithReloadErrorHandler(reported.collect))...)
	if err != nil {
		t.Fatalf("access.New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("Client.Close() error = %v", err)
		}
	})

	return client, reported
}

// readsEmployees asks the engine whether user reads employees in tenant1.
func readsEmployees(ctx context.Context, t *testing.T, client *access.Client, user accesstypes.User) bool {
	t.Helper()

	decisions, err := client.CheckUserResources(ctx, accesstypes.NewEnvironment(), user, tenant1Request, readPerm, employees)
	if err != nil {
		t.Fatalf("CheckUserResources() error = %v", err)
	}

	return decisions[employees].IsGranted()
}
