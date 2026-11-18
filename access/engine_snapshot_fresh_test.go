package access

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
)

// idleBound is the heartbeat interval of the engines these tests build: the bound a
// check holds the snapshot to.
const idleBound = time.Minute

// countedStore is a policy store that counts the reads of the policy made of it, and
// holds a read open while a test wants one in flight. A held read has taken the store's
// rows before it waits, as a read in flight has: what is written while it waits is not
// in it.
type countedStore struct {
	*fakeStore
	reads atomic.Int64

	mu      sync.Mutex
	hold    chan struct{}
	entered chan struct{}
}

func newCountedStore(store *fakeStore) *countedStore {
	return &countedStore{fakeStore: store, entered: make(chan struct{}, 1024)}
}

func (s *countedStore) ReadPolicy(ctx context.Context) (*policy.Records, error) {
	s.reads.Add(1)
	records, err := s.fakeStore.ReadPolicy(ctx)

	s.mu.Lock()
	hold := s.hold
	s.mu.Unlock()
	if hold != nil {
		s.entered <- struct{}{}
		<-hold
	}

	return records, err
}

// holdReads holds open the reads that begin from now, each announcing itself on entered
// once it has taken the store's rows, until release is called.
func (s *countedStore) holdReads() (entered <-chan struct{}, release func()) {
	hold := make(chan struct{})
	s.mu.Lock()
	s.hold = hold
	s.mu.Unlock()

	return s.entered, func() {
		s.mu.Lock()
		s.hold = nil
		s.mu.Unlock()
		close(hold)
	}
}

// awaitRead waits for a read the store holds open to begin, and fails the test when
// none does: the read the test was written to see was never made.
func awaitRead(t *testing.T, entered <-chan struct{}) {
	t.Helper()

	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("no read of the policy store began")
	}
}

// idleEngine builds an engine as an instance that gets no CPU between requests has it:
// no reload loop and no signal watch run (noLoops), so nothing but a check reads the
// store, on a clock the test moves, with idleBound as the heartbeat interval. The
// reload errors the engine reports are counted.
func idleEngine(t *testing.T, store Store) (e *snapshotEngine, clock *movedClock, reported *atomic.Int64) {
	t.Helper()

	reported = new(atomic.Int64)
	opts := defaultClientOptions()
	opts.heartbeatInterval = idleBound
	opts.onReloadError = func(error) { reported.Add(1) }
	clock = newMovedClock(noon)
	e = newSnapshotEngine(store, nil, opts)
	e.noLoops = true
	e.clock = clock.now
	t.Cleanup(func() {
		if err := e.close(); err != nil {
			t.Errorf("snapshotEngine.close() error = %v", err)
		}
	})

	return e, clock, reported
}

// advance moves the clock on by d.
func (c *movedClock) advance(d time.Duration) {
	c.nanos.Add(int64(d))
}

// lists asks the engine whether erin lists the resource in tenant1.
func lists(e *snapshotEngine, resource accesstypes.Resource) (bool, error) {
	got, err := e.checkUserResources(context.Background(), accesstypes.NewEnvironment(), "erin", tenant1Scope, "List", resource)
	if err != nil {
		return false, err
	}

	return got[0].granted, nil
}

// listsWidgets asks the engine whether erin lists widgets in tenant1: what the write
// grantWidgets makes, as another instance's, turns from denied to granted.
func listsWidgets(t *testing.T, e *snapshotEngine) bool {
	t.Helper()

	granted, err := lists(e, "widgets")
	if err != nil {
		t.Fatalf("checkUserResources() error = %v, want an answer from a snapshot", err)
	}

	return granted
}

// answer is what a check made on a goroutine of its own came to.
type answer struct {
	granted bool
	err     error
}

// Test_snapshotEngine_idleInstance_bound pins the bound on an instance whose loops do
// not run: a change another instance wrote is not seen inside the heartbeat interval,
// with no read of the store, and is seen by the first check past it; checks arriving
// together past the bound cost one read of the store between them, and every one of
// them is answered from it.
func Test_snapshotEngine_idleInstance_bound(t *testing.T) {
	t.Parallel()

	const checks = 50

	store := newCountedStore(engineFakeStore(t))
	e, clock, _ := idleEngine(t, store)

	if listsWidgets(t, e) {
		t.Fatal("widgets granted before the change")
	}
	if got := store.reads.Load(); got != 1 {
		t.Fatalf("store reads for the first check = %d, want 1: the first load", got)
	}

	// Another instance grants widgets. Nothing tells this one.
	grantWidgets(t, store.fakeStore)

	for _, age := range []time.Duration{0, idleBound / 2, idleBound} {
		clock.set(noon.Add(age))
		if listsWidgets(t, e) {
			t.Errorf("%s after the last read: the check saw the change, want the snapshot's answer inside the bound", age)
		}
	}
	if got := store.reads.Load(); got != 1 {
		t.Errorf("store reads inside the bound = %d, want still 1", got)
	}

	// Past the bound the instance gets requests again, many at once.
	clock.set(noon.Add(idleBound + time.Second))
	entered, release := store.holdReads()
	answers := make(chan answer, checks)
	for range checks {
		go func() {
			granted, err := lists(e, "widgets")
			answers <- answer{granted: granted, err: err}
		}()
	}
	awaitRead(t, entered)
	// The other checks arrive while the read is in flight. The counts below hold
	// whenever they arrive: one that comes after the read has landed reads nothing.
	time.Sleep(50 * time.Millisecond)
	release()

	for range checks {
		if got := <-answers; got.err != nil || !got.granted {
			t.Errorf("a check past the bound = (%v, %v), want the change seen", got.granted, got.err)
		}
	}
	if got := store.reads.Load(); got != 2 {
		t.Errorf("store reads after %d checks arriving together past the bound = %d, want 2: the first load and one read for them all", checks, got)
	}

	// The bound holds again from that read.
	clock.advance(idleBound)
	if !listsWidgets(t, e) {
		t.Error("widgets denied after the reload")
	}
	if got := store.reads.Load(); got != 2 {
		t.Errorf("store reads inside the bound of the reload = %d, want still 2", got)
	}
}

// Test_snapshotEngine_idleInstance_hint pins a hint on an instance whose loops do not
// run: once the hint is recorded, the next check reads the store before it answers and
// sees the change, inside the bound; the checks after it read nothing until another
// hint or the bound; and a hint that arrives while a read is in flight is not cleared
// by that read, so the check after it reads once more.
func Test_snapshotEngine_idleInstance_hint(t *testing.T) {
	t.Parallel()

	store := newCountedStore(engineFakeStore(t))
	e, clock, _ := idleEngine(t, store)
	listsGadgets := func() bool {
		t.Helper()
		granted, err := lists(e, "gadgets")
		if err != nil {
			t.Fatalf("checkUserResources() error = %v", err)
		}

		return granted
	}

	if listsWidgets(t, e) {
		t.Fatal("widgets granted before the change")
	}

	// Another instance grants widgets and announces it; the announcement reaches this
	// instance, whose loop does not run.
	grantWidgets(t, store.fakeStore)
	e.hinted()
	if !listsWidgets(t, e) {
		t.Error("the first check after the hint did not see the change")
	}
	if got := store.reads.Load(); got != 2 {
		t.Errorf("store reads after the first check behind a hint = %d, want 2: the first load and the hint's read", got)
	}
	for range 5 {
		listsWidgets(t, e)
	}
	if got := store.reads.Load(); got != 2 {
		t.Errorf("store reads for the checks after it = %d, want still 2", got)
	}

	// A hint while a read is in flight. The read in flight began behind an earlier
	// hint and has taken the store's rows; then another instance grants gadgets and
	// announces it.
	e.hinted()
	entered, release := store.holdReads()
	inFlight := make(chan answer, 1)
	go func() {
		granted, err := lists(e, "gadgets")
		inFlight <- answer{granted: granted, err: err}
	}()
	awaitRead(t, entered)
	if err := store.InsertGrant(context.Background(), tenant1Policy, "Editor", "List", "gadgets", "", ""); err != nil {
		t.Fatalf("InsertGrant() error = %v", err)
	}
	e.hinted()
	release()
	if got := <-inFlight; got.err != nil || got.granted {
		t.Errorf("the check whose read began before the grant = (%v, %v), want it answered from that read", got.granted, got.err)
	}
	if got := store.reads.Load(); got != 3 {
		t.Fatalf("store reads once the read in flight landed = %d, want 3", got)
	}

	if !listsGadgets() {
		t.Error("the check after a hint that arrived during a read did not see the change: the read in flight cleared the hint")
	}
	if got := store.reads.Load(); got != 4 {
		t.Errorf("store reads after it = %d, want 4: one more read for the hint that arrived during the last", got)
	}
	listsGadgets()
	if got := store.reads.Load(); got != 4 {
		t.Errorf("store reads for the check after that = %d, want still 4", got)
	}

	// With nothing announced, the bound alone asks for the next read.
	clock.advance(idleBound + time.Second)
	listsGadgets()
	if got := store.reads.Load(); got != 5 {
		t.Errorf("store reads for the first check past the bound = %d, want 5", got)
	}
}

// Test_snapshotEngine_idleInstance_failedRead pins a store read that fails on the
// check's path: it is reported, the last good snapshot answers, and the next check
// past the bound tries the store again.
func Test_snapshotEngine_idleInstance_failedRead(t *testing.T) {
	t.Parallel()

	store := newCountedStore(engineFakeStore(t))
	e, clock, reported := idleEngine(t, store)

	if listsWidgets(t, e) {
		t.Fatal("widgets granted before the change")
	}
	grantWidgets(t, store.fakeStore)
	clock.advance(idleBound + time.Second)

	steps := []struct {
		name         string
		storeErr     error
		wantGranted  bool
		wantReads    int64
		wantReported int64
	}{
		{name: "the read past the bound fails", storeErr: errors.New("store down"), wantGranted: false, wantReads: 2, wantReported: 1},
		{name: "the next check tries the store again", storeErr: errors.New("store down"), wantGranted: false, wantReads: 3, wantReported: 2},
		{name: "the check after the store is back sees the change", storeErr: nil, wantGranted: true, wantReads: 4, wantReported: 2},
		{name: "the check after it reads nothing", storeErr: nil, wantGranted: true, wantReads: 4, wantReported: 2},
	}
	// One engine's history, so the steps run in order.
	for _, step := range steps {
		store.setFail(step.storeErr)
		if got := listsWidgets(t, e); got != step.wantGranted {
			t.Errorf("%s: widgets granted = %v, want %v", step.name, got, step.wantGranted)
		}
		if got := store.reads.Load(); got != step.wantReads {
			t.Errorf("%s: store reads so far = %d, want %d", step.name, got, step.wantReads)
		}
		if got := reported.Load(); got != step.wantReported {
			t.Errorf("%s: reload errors reported so far = %d, want %d", step.name, got, step.wantReported)
		}
	}
}

// Test_snapshotEngine_servingInstance pins what the loops still do on an instance that
// is serving, and what they cost: a hint shows the change to the very next check, a
// local write likewise, each for one read of the store, whoever made it, the check or
// the loop; and the loop reloads on a hint and on its tick with no check asking.
func Test_snapshotEngine_servingInstance(t *testing.T) {
	t.Parallel()

	// peeks reads the engine's current snapshot without asking the engine anything, so
	// no check's own read can be what brought a change in.
	peeks := func(e *snapshotEngine) bool {
		snap := e.peek()

		return snap != nil && snap.decideUserResources("erin", tenant1Scope, "List", "widgets")[0].granted
	}

	t.Run("a hint shows the change to the next check for one read of the store", func(t *testing.T) {
		t.Parallel()

		store := newCountedStore(engineFakeStore(t))
		sig := newFakeSignal()
		opts := defaultClientOptions()
		opts.signal = sig
		e := testEngine(t, store, opts)
		if listsWidgets(t, e) {
			t.Fatal("widgets granted before the change")
		}
		select {
		case <-sig.watchStarted:
		case <-time.After(5 * time.Second):
			t.Fatal("ChangeSignal.Watch was never started")
		}
		before := store.reads.Load()

		grantWidgets(t, store.fakeStore)
		sig.trigger()
		if !listsWidgets(t, e) {
			t.Error("the first check after the hint did not see the change")
		}
		// The loop was woken by the same hint; whichever of the two read the store, the
		// other found the read made.
		time.Sleep(100 * time.Millisecond)
		if got := store.reads.Load() - before; got != 1 {
			t.Errorf("store reads for one hint = %d, want 1 between the loop and the check", got)
		}
	})

	t.Run("a local write shows to the next check for one read of the store", func(t *testing.T) {
		t.Parallel()

		store := newCountedStore(engineFakeStore(t))
		e := testEngine(t, store, nil)
		if listsWidgets(t, e) {
			t.Fatal("widgets granted before the write")
		}
		before := store.reads.Load()

		grantWidgets(t, store.fakeStore)
		e.policyChanged()
		if !listsWidgets(t, e) {
			t.Error("the first check after the write did not see it")
		}
		time.Sleep(100 * time.Millisecond)
		if got := store.reads.Load() - before; got != 1 {
			t.Errorf("store reads for one local write = %d, want 1 between the loop and the check", got)
		}
	})

	t.Run("the loop reloads on a hint with no check asking", func(t *testing.T) {
		t.Parallel()

		store := newCountedStore(engineFakeStore(t))
		sig := newFakeSignal()
		opts := defaultClientOptions()
		opts.signal = sig
		e := testEngine(t, store, opts)
		if listsWidgets(t, e) {
			t.Fatal("widgets granted before the change")
		}
		select {
		case <-sig.watchStarted:
		case <-time.After(5 * time.Second):
			t.Fatal("ChangeSignal.Watch was never started")
		}

		grantWidgets(t, store.fakeStore)
		sig.trigger()
		waitUntil(t, "the loop never reloaded on the hint", func() bool {
			return peeks(e)
		})
	})

	t.Run("the loop reloads on its tick with no check asking", func(t *testing.T) {
		t.Parallel()

		store := newCountedStore(engineFakeStore(t))
		opts := defaultClientOptions()
		opts.heartbeatInterval = 20 * time.Millisecond
		e := newSnapshotEngine(store, nil, opts)
		// The engine's clock does not move, so the snapshot never passes its bound:
		// what reloads is the tick asking for the read, as the loop's ticker must.
		e.clock = newMovedClock(noon).now
		t.Cleanup(func() {
			if err := e.close(); err != nil {
				t.Errorf("snapshotEngine.close() error = %v", err)
			}
		})
		if err := e.waitReady(context.Background()); err != nil {
			t.Fatalf("waitReady() error = %v", err)
		}

		grantWidgets(t, store.fakeStore)
		waitUntil(t, "the loop never reloaded on its tick", func() bool {
			return peeks(e)
		})
	})
}

// Test_snapshotEngine_tick pins the loop's heartbeat: every tick reads the store,
// whatever the snapshot's age, so on an instance that is serving it is the loop that
// reads as each interval ends and not the check that finds it ended; and the check
// after a tick reads nothing.
func Test_snapshotEngine_tick(t *testing.T) {
	t.Parallel()

	store := newCountedStore(engineFakeStore(t))
	e, _, _ := idleEngine(t, store)
	if listsWidgets(t, e) {
		t.Fatal("widgets granted before the change")
	}

	// The clock does not move: the snapshot stays inside its bound throughout.
	grantWidgets(t, store.fakeStore)
	for tick := int64(1); tick <= 3; tick++ {
		e.tick()
		if got := store.reads.Load(); got != 1+tick {
			t.Errorf("store reads after tick %d = %d, want %d: the first load and one read a tick", tick, got, 1+tick)
		}
	}
	if !listsWidgets(t, e) {
		t.Error("the check after a tick did not see the change the tick read")
	}
	if got := store.reads.Load(); got != 4 {
		t.Errorf("store reads after the check behind the ticks = %d, want still 4", got)
	}
}

// Test_snapshotEngine_closed pins that a closed engine reads the store no more: its
// checks are answered from the last snapshot, whatever is written, announced or past
// the bound, and nothing is reported.
func Test_snapshotEngine_closed(t *testing.T) {
	t.Parallel()

	store := newCountedStore(engineFakeStore(t))
	e, clock, reported := idleEngine(t, store)
	if listsWidgets(t, e) {
		t.Fatal("widgets granted before the change")
	}
	if err := e.close(); err != nil {
		t.Fatalf("close() error = %v", err)
	}
	reads := store.reads.Load()

	grantWidgets(t, store.fakeStore)
	e.policyChanged()
	e.hinted()
	clock.advance(idleBound + time.Second)
	if listsWidgets(t, e) {
		t.Error("a closed engine's check saw a change, want the last snapshot's answer")
	}
	if got := store.reads.Load(); got != reads {
		t.Errorf("store reads after close = %d, want %d: a closed engine reads nothing", got, reads)
	}
	if got := reported.Load(); got != 0 {
		t.Errorf("reload errors reported = %d, want 0", got)
	}
}
