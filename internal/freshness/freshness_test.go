package freshness

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	testBound   = time.Minute
	testTimeout = 5 * time.Second
	// testRequests is how many requests arrive together where a test counts the reads
	// they cost.
	testRequests = 50
)

var errStoreDown = errors.New("the store is down")

// lifeKey names the value a test puts on the context a copy is kept under, and callKey
// the one it puts on a caller's context, so a read can tell which context it was handed.
type (
	lifeKey struct{}
	callKey struct{}
)

// testClock is a clock a test moves. It counts how often it is read, which is how a test
// knows that a request has arrived at the Gate: a request that finds a copy to judge by
// its age reads the clock once as it arrives, after it has taken what it arrives to.
type testClock struct {
	nanos atomic.Int64
	reads atomic.Int64
}

func newTestClock() *testClock {
	c := new(testClock)
	c.nanos.Store(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC).UnixNano())

	return c
}

func (c *testClock) now() time.Time {
	c.reads.Add(1)

	return time.Unix(0, c.nanos.Load()).UTC()
}

func (c *testClock) advance(d time.Duration) {
	c.nanos.Add(int64(d))
}

// testStore is the store a test's Gate reads. It counts the reads, keeps the context of
// each, fails a read with the error set when the read began, holds a read open while a
// test wants one in flight, and reports two reads running at once.
type testStore struct {
	reads      atomic.Int64
	running    atomic.Int64
	overlapped atomic.Bool

	mu       sync.Mutex
	err      error
	hold     chan struct{}
	entered  chan struct{}
	contexts []context.Context
}

func newTestStore() *testStore {
	s := new(testStore)
	s.entered = make(chan struct{}, 1024)

	return s
}

func (s *testStore) read(ctx context.Context) error {
	if s.running.Add(1) > 1 {
		s.overlapped.Store(true)
	}
	defer s.running.Add(-1)
	s.reads.Add(1)

	s.mu.Lock()
	err, hold := s.err, s.hold
	s.contexts = append(s.contexts, ctx)
	s.mu.Unlock()

	if hold != nil {
		s.entered <- struct{}{}
		select {
		case <-hold:
		case <-ctx.Done():
			return fmt.Errorf("the read was cut short: %w", ctx.Err())
		}
	}

	return err
}

// fail sets what the reads that begin from now fail with, nil for a store that answers.
func (s *testStore) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

// holdReads holds open the reads that begin from now, each announcing itself on entered
// as it begins, until release is called; the reads that begin after release run through.
func (s *testStore) holdReads() (entered <-chan struct{}, release func()) {
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

// lastContext returns the context of the store's latest read.
func (s *testStore) lastContext(t *testing.T) context.Context {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.contexts) == 0 {
		t.Fatal("the store was not read")
	}

	return s.contexts[len(s.contexts)-1]
}

// testReports is where a test's Gate reports its failed reads.
type testReports struct {
	count atomic.Int64

	mu   sync.Mutex
	ctx  context.Context
	last error
}

func (r *testReports) failed(ctx context.Context, err error) {
	r.count.Add(1)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ctx, r.last = ctx, err
}

// testGate is a Gate over a test's store, clock and reports, not yet kept.
func testGate(store *testStore, clock *testClock, reports *testReports) *Gate {
	return New(store.read, testBound, testTimeout, clock.now, reports.failed)
}

// keptGate is a testGate kept for the life of the test, whose copy has been read once:
// an instance that is serving.
func keptGate(t *testing.T) (*Gate, *testStore, *testClock, *testReports) {
	t.Helper()

	store, clock, reports := newTestStore(), newTestClock(), new(testReports)
	g := testGate(store, clock, reports)
	g.Keep(context.WithValue(t.Context(), lifeKey{}, "the life of the copy"))
	if err := g.Read(t.Context()); err != nil {
		t.Fatalf("Read() error = %v", err)
	}

	return g, store, clock, reports
}

// waitFor polls until the condition holds, and fails the test when it does not in time.
func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()

	for deadline := time.Now().Add(10 * time.Second); !condition(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting until %s", what)
		}
	}
}

// TestGate_Freshen_bound pins the bound: a request is answered from a copy no older
// than its bound with no read of the store, the first request past the bound reads the
// store before it is answered, and the bound holds again from that read.
func TestGate_Freshen_bound(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		age       time.Duration
		wantReads int64
	}{
		{name: "a copy just read answers with no read", age: 0, wantReads: 0},
		{name: "a copy inside its bound answers with no read", age: testBound - time.Nanosecond, wantReads: 0},
		{name: "a copy exactly at its bound answers with no read", age: testBound, wantReads: 0},
		{name: "a copy past its bound is read again before it answers", age: testBound + time.Nanosecond, wantReads: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			g, store, clock, _ := keptGate(t)
			clock.advance(tt.age)

			before := store.reads.Load()
			if err := g.Freshen(); err != nil {
				t.Fatalf("Freshen() error = %v", err)
			}
			if got := store.reads.Load() - before; got != tt.wantReads {
				t.Errorf("store reads for a request at age %s = %d, want %d", tt.age, got, tt.wantReads)
			}

			// Whatever the first request did, the ones right behind it read nothing: the
			// copy is inside its bound, by the read made just now or by the one before.
			before = store.reads.Load()
			for range 3 {
				if err := g.Freshen(); err != nil {
					t.Fatalf("Freshen() error = %v", err)
				}
			}
			if tt.wantReads == 1 {
				if got := store.reads.Load() - before; got != 0 {
					t.Errorf("store reads for the requests after the one that read = %d, want 0 until the bound passes again", got)
				}
			}
		})
	}
}

// TestGate_Freshen_oneReadServesEveryRequestWaiting pins that requests arriving together
// at a copy past its bound cost one read of the store between them, whether the read
// succeeds or fails: no read per request, and no request retrying a read it waited on.
func TestGate_Freshen_oneReadServesEveryRequestWaiting(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		err         error
		wantReports int64
	}{
		{name: "the read succeeds and answers them all", err: nil, wantReports: 0},
		{name: "the read fails, is reported once, and answers them all with its failure", err: errStoreDown, wantReports: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			g, store, clock, reports := keptGate(t)
			clock.advance(testBound + time.Second)
			store.fail(tt.err)
			entered, release := store.holdReads()

			before, arrivals := store.reads.Load(), clock.reads.Load()
			errs := make(chan error, testRequests)
			for range testRequests {
				go func() {
					errs <- g.Freshen()
				}()
			}
			<-entered
			// Every request has arrived while the read is in flight: each read the clock
			// once as it came, and the one that got the lock twice more, for its second
			// look at the copy and for the beginning of its read.
			waitFor(t, "every request has arrived at the Gate", func() bool {
				return clock.reads.Load()-arrivals >= testRequests+2
			})
			release()

			for range testRequests {
				if err := <-errs; !errors.Is(err, tt.err) {
					t.Errorf("Freshen() error = %v, want %v", err, tt.err)
				}
			}
			if got := store.reads.Load() - before; got != 1 {
				t.Errorf("store reads for %d requests arriving together = %d, want 1", testRequests, got)
			}
			if got := reports.count.Load(); got != tt.wantReports {
				t.Errorf("failed reads reported = %d, want %d", got, tt.wantReports)
			}
			if store.overlapped.Load() {
				t.Error("two reads of the store ran at once")
			}
		})
	}
}

// TestGate_Nudge pins what an announcement does inside the bound: the next request
// reads the store before it is answered, the ones after it do not read again until
// another announcement or the bound, and announcements made before one read are all
// held by it.
func TestGate_Nudge(t *testing.T) {
	t.Parallel()

	g, store, clock, _ := keptGate(t)
	steps := []struct {
		name      string
		do        func()
		wantReads int64
	}{
		{name: "with nothing announced a request reads nothing", do: func() {}, wantReads: 0},
		{name: "the first request after an announcement reads the store", do: g.Nudge, wantReads: 1},
		{name: "the request after it reads nothing", do: func() {}, wantReads: 0},
		{name: "two announcements before a request cost it one read", do: func() { g.Nudge(); g.Nudge() }, wantReads: 1},
		{name: "the request after it reads nothing", do: func() {}, wantReads: 0},
		{name: "the bound still asks for a read with nothing announced", do: func() { clock.advance(testBound + time.Second) }, wantReads: 1},
		{name: "an announcement right after a read asks for another", do: g.Nudge, wantReads: 1},
	}
	// One Gate's history, so the steps run in order.
	for _, step := range steps {
		step.do()
		before := store.reads.Load()
		if err := g.Freshen(); err != nil {
			t.Fatalf("%s: Freshen() error = %v", step.name, err)
		}
		if got := store.reads.Load() - before; got != step.wantReads {
			t.Errorf("%s: store reads = %d, want %d", step.name, got, step.wantReads)
		}
	}
}

// TestGate_Nudge_duringARead pins that a read already in flight when an announcement
// arrives does not clear it: the request that arrives after the announcement is not
// answered by that read and makes one more, where without the announcement the read in
// flight answers it.
func TestGate_Nudge_duringARead(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		nudge     bool
		wantReads int64
	}{
		{name: "an announcement during the read forces one more read after it", nudge: true, wantReads: 2},
		{name: "with no announcement the read in flight answers the request that waited on it", nudge: false, wantReads: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			g, store, clock, _ := keptGate(t)
			clock.advance(testBound + time.Second)
			entered, release := store.holdReads()

			before, arrivals := store.reads.Load(), clock.reads.Load()
			first := make(chan error, 1)
			go func() {
				first <- g.Freshen()
			}()
			<-entered
			if tt.nudge {
				g.Nudge()
			}
			second := make(chan error, 1)
			go func() {
				second <- g.Freshen()
			}()
			if !tt.nudge {
				// The second request has arrived and waits on the read in flight: the first
				// read the clock three times, the second once as it came.
				waitFor(t, "the second request has arrived at the Gate", func() bool {
					return clock.reads.Load()-arrivals >= 4
				})
			}
			release()

			for name, done := range map[string]chan error{"first": first, "second": second} {
				if err := <-done; err != nil {
					t.Errorf("the %s request: Freshen() error = %v", name, err)
				}
			}
			if got := store.reads.Load() - before; got != tt.wantReads {
				t.Errorf("store reads = %d, want %d", got, tt.wantReads)
			}

			// Whichever read answered the second request held everything announced.
			before = store.reads.Load()
			if err := g.Freshen(); err != nil {
				t.Fatalf("Freshen() error = %v", err)
			}
			if got := store.reads.Load() - before; got != 0 {
				t.Errorf("store reads for the request after them = %d, want 0", got)
			}
		})
	}
}

// TestGate_Freshen_failure pins a read that fails: it is reported once with the context
// the copy is kept under, its error is returned for the caller to answer from the copy
// it holds, the next request tries the store again, and once a read succeeds the bound
// holds again.
func TestGate_Freshen_failure(t *testing.T) {
	t.Parallel()

	g, store, clock, reports := keptGate(t)
	clock.advance(testBound + time.Second)

	steps := []struct {
		name        string
		storeErr    error
		wantReads   int64
		wantReports int64
	}{
		{name: "the read past the bound fails", storeErr: errStoreDown, wantReads: 1, wantReports: 1},
		{name: "the next request tries the store again", storeErr: errStoreDown, wantReads: 1, wantReports: 2},
		{name: "the request after the store is back reads it", storeErr: nil, wantReads: 1, wantReports: 2},
		{name: "the request after a read that succeeded reads nothing", storeErr: nil, wantReads: 0, wantReports: 2},
	}
	// One Gate's history, so the steps run in order.
	for _, step := range steps {
		store.fail(step.storeErr)
		before := store.reads.Load()
		wantErr := step.storeErr
		if step.wantReads == 0 {
			wantErr = nil
		}
		if err := g.Freshen(); !errors.Is(err, wantErr) {
			t.Errorf("%s: Freshen() error = %v, want %v", step.name, err, wantErr)
		}
		if got := store.reads.Load() - before; got != step.wantReads {
			t.Errorf("%s: store reads = %d, want %d", step.name, got, step.wantReads)
		}
		if got := reports.count.Load(); got != step.wantReports {
			t.Errorf("%s: failed reads reported so far = %d, want %d", step.name, got, step.wantReports)
		}
	}

	reports.mu.Lock()
	defer reports.mu.Unlock()
	if !errors.Is(reports.last, errStoreDown) {
		t.Errorf("the failure reported = %v, want %v", reports.last, errStoreDown)
	}
	if reports.ctx == nil || reports.ctx.Value(lifeKey{}) == nil {
		t.Error("the failure was not reported with the context the copy is kept under")
	}
}

// TestGate_Keep pins the life a copy is kept fresh for: before Keep nothing is read,
// whatever was announced; from Keep a request reads the store when the copy is due,
// the first time because there is no read to judge; and once the life has ended no
// request reads again and none reports a failure.
func TestGate_Keep(t *testing.T) {
	t.Parallel()

	store, clock, reports := newTestStore(), newTestClock(), new(testReports)
	g := testGate(store, clock, reports)

	g.Nudge()
	if err := g.Freshen(); err != nil {
		t.Fatalf("Freshen() before Keep error = %v", err)
	}
	if got := store.reads.Load(); got != 0 {
		t.Fatalf("store reads before Keep = %d, want 0", got)
	}

	ctx, end := context.WithCancel(t.Context())
	g.Keep(ctx)
	if err := g.Freshen(); err != nil {
		t.Fatalf("Freshen() error = %v", err)
	}
	if got := store.reads.Load(); got != 1 {
		t.Fatalf("store reads for the first request of a copy that is kept = %d, want 1", got)
	}

	end()
	g.Nudge()
	clock.advance(testBound + time.Second)
	store.fail(errStoreDown)
	if err := g.Freshen(); err != nil {
		t.Errorf("Freshen() after the life ended error = %v, want nil: the copy answers as it stands", err)
	}
	if got := store.reads.Load(); got != 1 {
		t.Errorf("store reads after the life ended = %d, want still 1", got)
	}
	if got := reports.count.Load(); got != 0 {
		t.Errorf("failed reads reported = %d, want 0", got)
	}
}

// TestGate_Keep_endsDuringARead pins that a read cut short by the end of the copy's
// life is no failure of the store: its error is returned and nothing is reported.
func TestGate_Keep_endsDuringARead(t *testing.T) {
	t.Parallel()

	store, clock, reports := newTestStore(), newTestClock(), new(testReports)
	g := testGate(store, clock, reports)
	ctx, end := context.WithCancel(t.Context())
	g.Keep(ctx)
	entered, release := store.holdReads()
	defer release()

	done := make(chan error, 1)
	go func() {
		done <- g.Freshen()
	}()
	<-entered
	end()

	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("Freshen() error = %v, want %v", err, context.Canceled)
	}
	if got := reports.count.Load(); got != 0 {
		t.Errorf("failed reads reported = %d, want 0: the life ended, the store did not fail", got)
	}
}

// TestGate_Freshen_readContext pins the context a request's read runs under: the one
// the copy is kept under, held to the Gate's timeout, so a store that does not answer
// fails the read at the timeout and the failure is reported.
func TestGate_Freshen_readContext(t *testing.T) {
	t.Parallel()

	t.Run("the read runs under the context the copy is kept under, with a deadline", func(t *testing.T) {
		t.Parallel()

		g, store, clock, _ := keptGate(t)
		clock.advance(testBound + time.Second)
		asked := time.Now()
		if err := g.Freshen(); err != nil {
			t.Fatalf("Freshen() error = %v", err)
		}

		ctx := store.lastContext(t)
		if ctx.Value(lifeKey{}) == nil {
			t.Error("the read's context is not the one the copy is kept under")
		}
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("the read's context carries no deadline")
		}
		if got := deadline.Sub(asked); got <= 0 || got > testTimeout+time.Second {
			t.Errorf("the read's deadline is %s after the request, want within the timeout of %s", got, testTimeout)
		}
	})

	t.Run("a store that does not answer fails the read at the timeout", func(t *testing.T) {
		t.Parallel()

		store, clock, reports := newTestStore(), newTestClock(), new(testReports)
		g := New(store.read, testBound, 20*time.Millisecond, clock.now, reports.failed)
		g.Keep(t.Context())
		_, release := store.holdReads()
		defer release()

		if err := g.Freshen(); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Freshen() error = %v, want %v", err, context.DeadlineExceeded)
		}
		if got := reports.count.Load(); got != 1 {
			t.Errorf("failed reads reported = %d, want 1", got)
		}
	})
}

// TestGate_Read pins the read a caller asks for outright: it reads whatever the copy's
// age, before the copy is kept too, and the copy it leaves is judged from it; it runs
// under the caller's context held to the timeout; and its failure is the caller's
// alone, neither reported nor shared with a request that waited on it.
func TestGate_Read(t *testing.T) {
	t.Parallel()

	t.Run("it reads every time, and the bound counts from it", func(t *testing.T) {
		t.Parallel()

		store, clock, reports := newTestStore(), newTestClock(), new(testReports)
		g := testGate(store, clock, reports)
		for range 2 {
			if err := g.Read(t.Context()); err != nil {
				t.Fatalf("Read() before Keep error = %v", err)
			}
		}
		if got := store.reads.Load(); got != 2 {
			t.Fatalf("store reads for two calls = %d, want 2", got)
		}

		g.Keep(t.Context())
		clock.advance(testBound)
		if err := g.Freshen(); err != nil {
			t.Fatalf("Freshen() error = %v", err)
		}
		if got := store.reads.Load(); got != 2 {
			t.Errorf("store reads for a request inside the bound of the read asked for = %d, want still 2", got)
		}
	})

	t.Run("it runs under the caller's context, held to the timeout", func(t *testing.T) {
		t.Parallel()

		g, store, _, _ := keptGate(t)
		asked := time.Now()
		if err := g.Read(context.WithValue(t.Context(), callKey{}, "the caller")); err != nil {
			t.Fatalf("Read() error = %v", err)
		}
		ctx := store.lastContext(t)
		if ctx.Value(callKey{}) == nil {
			t.Error("the read's context is not the caller's")
		}
		if deadline, ok := ctx.Deadline(); !ok || deadline.Sub(asked) > testTimeout+time.Second {
			t.Errorf("the read's deadline = %v (set %v), want within the timeout of %s", deadline, ok, testTimeout)
		}

		ended, end := context.WithCancel(t.Context())
		end()
		entered, release := store.holdReads()
		defer release()
		if err := g.Read(ended); !errors.Is(err, context.Canceled) {
			t.Errorf("Read() under a context that has ended error = %v, want %v", err, context.Canceled)
		}
		<-entered
	})

	t.Run("its failure is the caller's alone", func(t *testing.T) {
		t.Parallel()

		g, store, clock, reports := keptGate(t)
		clock.advance(testBound + time.Second)
		store.fail(errStoreDown)
		entered, release := store.holdReads()

		before, arrivals := store.reads.Load(), clock.reads.Load()
		asked := make(chan error, 1)
		go func() {
			asked <- g.Read(t.Context())
		}()
		<-entered
		// The reads that begin from now succeed; the one in flight began failing.
		store.fail(nil)
		waiting := make(chan error, 1)
		go func() {
			waiting <- g.Freshen()
		}()
		// The read asked for read the clock once as it began, the request once as it came.
		waitFor(t, "the request has arrived at the Gate", func() bool {
			return clock.reads.Load()-arrivals >= 2
		})
		release()

		if err := <-asked; !errors.Is(err, errStoreDown) {
			t.Errorf("Read() error = %v, want %v", err, errStoreDown)
		}
		if err := <-waiting; err != nil {
			t.Errorf("Freshen() error = %v, want nil: a request that waited on a caller's failed read makes its own", err)
		}
		if got := store.reads.Load() - before; got != 2 {
			t.Errorf("store reads = %d, want 2: the caller's and the request's own", got)
		}
		if got := reports.count.Load(); got != 0 {
			t.Errorf("failed reads reported = %d, want 0: the caller has the error", got)
		}
	})

	t.Run("an announcement during it is not cleared by it", func(t *testing.T) {
		t.Parallel()

		g, store, _, _ := keptGate(t)
		entered, release := store.holdReads()
		asked := make(chan error, 1)
		go func() {
			asked <- g.Read(t.Context())
		}()
		<-entered
		g.Nudge()
		release()
		if err := <-asked; err != nil {
			t.Fatalf("Read() error = %v", err)
		}

		before := store.reads.Load()
		if err := g.Freshen(); err != nil {
			t.Fatalf("Freshen() error = %v", err)
		}
		if got := store.reads.Load() - before; got != 1 {
			t.Errorf("store reads for the request after the announcement = %d, want 1", got)
		}
	})
}

// TestGate_concurrent runs requests, announcements and reads asked for outright against
// one Gate at once, on the wall clock with a bound that keeps passing, for the race
// detector to watch; whatever the interleaving, the store is read by one at a time.
func TestGate_concurrent(t *testing.T) {
	t.Parallel()

	store, reports := newTestStore(), new(testReports)
	g := New(store.read, 100*time.Microsecond, testTimeout, time.Now, reports.failed)
	g.Keep(t.Context())

	deadline := time.Now().Add(200 * time.Millisecond)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for time.Now().Before(deadline) {
				if err := g.Freshen(); err != nil {
					t.Errorf("Freshen() error = %v", err)

					return
				}
			}
		})
	}
	wg.Go(func() {
		for time.Now().Before(deadline) {
			g.Nudge()
			time.Sleep(50 * time.Microsecond)
		}
	})
	wg.Go(func() {
		for time.Now().Before(deadline) {
			if err := g.Read(t.Context()); err != nil {
				t.Errorf("Read() error = %v", err)

				return
			}
			time.Sleep(200 * time.Microsecond)
		}
	})
	wg.Wait()

	if store.overlapped.Load() {
		t.Error("two reads of the store ran at once")
	}
	if store.reads.Load() == 0 {
		t.Error("the store was never read")
	}
}
