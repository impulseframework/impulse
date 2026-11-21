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

// pastThePause is a little longer than the engine leaves the store alone after a reload
// of its own failed (one second). Where the suite has turned the loop's reload away it
// waits this long, so the check that follows is past the pause and reads the store.
const pastThePause = 1100 * time.Millisecond

// tenant1Request is where the suite's checks are made: a request in tenant1.
var tenant1Request = accesstypes.DomainScope("tenant1")

// RunEngine holds a permission engine over an empty, ready store to what a check made
// inside an open transaction of another request sees. A role is taken away and the
// write committed; then, while a request is inside its transaction, the engine learns
// of it, and that request's check, made under the transaction's context, is the first
// to reach the store after. It must see the role gone, in each way the engine learns:
// told of a write of its own instance's, hinted by another instance, and told nothing
// while the heartbeat interval passes.
//
// In the first two the engine's loop is turned away from the store when it comes to
// reload (gatedStore), as a store that answers an error turns it away, so the reload
// is the check's own to make, inside the transaction, once the pause the engine keeps
// after a failed reload has passed: the case in which an engine that reloaded under
// the caller's context was refused by the Spanner client and answered from the
// snapshot before the write. The engine reads the store on its own context
// whoever asks, so the check's read runs beside the open transaction and never under
// it. A store whose client refuses a read under a transaction's context, or whose
// database does not serve a read beside an open transaction, fails here: the read
// fails or does not return, and the check answers from the snapshot before the change.
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
		if err := store.InsertUserRole(ctx, tenant1, user, editor, accesstypes.Period{}, ""); err != nil {
			t.Fatalf("InsertUserRole(%q) error = %v", user, err)
		}
	}

	t.Run("taken away on this instance", func(t *testing.T) { runEngineLocalWrite(t, store, within) })
	t.Run("taken away on another instance and announced", func(t *testing.T) { runEngineHint(t, store, within) })
	t.Run("taken away on another instance with nothing announced", func(t *testing.T) { runEngineBound(t, store, within) })
}

// runEngineLocalWrite: a request of this instance takes the role away and commits; the
// engine is told (Client.PolicyChanged, as the manager's own writes tell it) while
// another request is inside its transaction, the loop's reload is turned away, and that
// request's check is the first to reach the store after, once the pause has passed.
func runEngineLocalWrite(t *testing.T, store access.Store, within Transaction) {
	t.Helper()
	ctx := t.Context()

	gated := &gatedStore{Store: store}
	client, reported := newEngine(t, gated, access.WithHeartbeatInterval(time.Hour))
	if !readsEmployees(ctx, t, client, alice) {
		t.Fatalf("%s does not read employees before the role is taken away", alice)
	}
	if err := store.DeleteUserRole(ctx, tenant1, alice, editor); err != nil {
		t.Fatalf("DeleteUserRole() error = %v", err)
	}
	reads := gated.reads.Load()

	var runs int
	within(t, func(txnCtx context.Context) {
		runs++
		gated.turnAway.Store(true)
		client.PolicyChanged()
		reported.await(t, runs)
		gated.turnAway.Store(false)
		time.Sleep(pastThePause)
		if readsEmployees(txnCtx, t, client, alice) {
			t.Errorf("the first check to read the store after the write, made inside an open transaction, still let %s read employees", alice)
		}
	})

	if got := gated.reads.Load() - reads; got != int64(runs) {
		t.Errorf("policy reads that reached the store = %d for %d times the engine was told of the write, want one each: the check's own, made inside the transaction", got, runs)
	}
	reported.onlyTurnedAway(t, runs)
}

// runEngineHint: another instance takes the role away and announces it; the hint
// reaches this instance while a request of its own is inside its transaction, the
// loop's reload is turned away, and that request's check is the first to reach the
// store after the hint, once the pause has passed.
func runEngineHint(t *testing.T, store access.Store, within Transaction) {
	t.Helper()
	ctx := t.Context()

	hints := &hintChannel{watching: make(chan struct{})}
	gated := &gatedStore{Store: store}
	client, reported := newEngine(t, gated, access.WithHeartbeatInterval(time.Hour), access.WithChangeSignal(hints))
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
	reads := gated.reads.Load()

	var runs int
	within(t, func(txnCtx context.Context) {
		runs++
		gated.turnAway.Store(true)
		hints.deliver()
		reported.await(t, runs)
		gated.turnAway.Store(false)
		time.Sleep(pastThePause)
		if readsEmployees(txnCtx, t, client, bob) {
			t.Errorf("the first check to read the store after the hint, made inside an open transaction, still let %s read employees", bob)
		}
	})

	if got := gated.reads.Load() - reads; got != int64(runs) {
		t.Errorf("policy reads that reached the store = %d for %d hints, want one each: the check's own, made inside the transaction", got, runs)
	}
	reported.onlyTurnedAway(t, runs)
}

// runEngineBound: another instance takes the role away and nothing tells this one; a
// request of its own is inside its transaction while the heartbeat interval passes,
// and its check past the interval sees the role gone, whichever read the store for it:
// the loop on its tick, or the check itself beside the open transaction. The engine's
// loop runs here, so this holds on an engine whose loop alone keeps the interval too;
// that a check keeps it with no loop running is pinned in the access package, on an
// engine built without its loops.
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
		// past the interval, from a read that began after the role was taken away.
		time.Sleep(engineBound + 100*time.Millisecond)
		if readsEmployees(txnCtx, t, client, carol) {
			t.Errorf("a check made inside an open transaction, past the heartbeat interval, still let %s read employees", carol)
		}
	})

	if got := reported.list(); len(got) != 0 {
		t.Errorf("reload errors reported = %v, want none", got)
	}
}

// errTurnedAway is what a gatedStore answers a read of the policy it turns away.
var errTurnedAway = errors.New("the read was turned away by the test")

// gatedStore is a store a test can turn the engine's reads of the policy away from,
// and counts the ones it hands on. While turnAway is set a read answers errTurnedAway
// without reaching the store: how a test has the engine's loop fail its reload, so
// that the next reload is a check's to make.
type gatedStore struct {
	access.Store
	reads    atomic.Int64
	turnAway atomic.Bool
}

func (g *gatedStore) ReadPolicy(ctx context.Context) (*policy.Records, error) {
	if g.turnAway.Load() {
		return nil, errTurnedAway
	}
	g.reads.Add(1)
	records, err := g.Store.ReadPolicy(ctx)
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

// await waits until the handler has been handed n errors: the engine's loop has tried
// its reload and been turned away, and has let go of the reload.
func (r *reportedErrors) await(t *testing.T, n int) {
	t.Helper()

	for deadline := time.Now().Add(10 * time.Second); len(r.list()) < n; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("reload errors reported = %v, want %d: the engine's loop never tried to reload", r.list(), n)
		}
	}
}

// onlyTurnedAway holds what the handler was handed to the n reloads the test turned
// away: nothing else failed, and no refusal of a read was reported or dropped beside
// them.
func (r *reportedErrors) onlyTurnedAway(t *testing.T, n int) {
	t.Helper()

	got := r.list()
	if len(got) != n {
		t.Errorf("reload errors reported = %v, want the %d reloads the test turned away and no other", got, n)
	}
	for _, err := range got {
		if !errors.Is(err, errTurnedAway) {
			t.Errorf("reload error reported = %v, want only the reloads the test turned away", err)
		}
	}
}

// newEngine builds a Client over the store, closed when the test ends, and what it
// reports to its reload-error handler.
func newEngine(t *testing.T, store access.Store, opts ...access.Option) (*access.Client, *reportedErrors) {
	t.Helper()

	reported := &reportedErrors{}
	client, err := access.New(store, suiteCollection, append(opts, access.WithReloadErrorHandler(reported.collect))...)
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
