package resource

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/impulseframework/impulse/accesstypes"
)

// The tests of what keeps an instance's copies fresh when its loops do not run: the
// tenant roster's here, the feature set's in feature_fresh_spanner_test.go. Both read
// their tables on the Spanner emulator, the one database either is implemented for.

// freshBound is the backstop of the copies these tests build: the bound a lookup holds
// the copy to.
const freshBound = 5 * time.Minute

// freshLookups is how many lookups arrive together where a test counts the rereads they
// cost.
const freshLookups = 50

// movedClock is a clock a test moves, safe for a copy's own goroutines. It starts at
// freshStart.
type movedClock struct {
	nanos atomic.Int64
}

// freshStart is the instant a movedClock starts at: when a copy built right after the
// clock makes its first read.
var freshStart = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func newMovedClock() *movedClock {
	c := &movedClock{}
	c.nanos.Store(freshStart.UnixNano())

	return c
}

func (c *movedClock) now() time.Time {
	return time.Unix(0, c.nanos.Load()).UTC()
}

// setAge puts the clock age after freshStart.
func (c *movedClock) setAge(age time.Duration) {
	c.nanos.Store(freshStart.Add(age).UnixNano())
}

func (c *movedClock) advance(d time.Duration) {
	c.nanos.Add(int64(d))
}

// loggedErrors collects what a copy logs when a reread of its own fails.
type loggedErrors struct {
	mu    sync.Mutex
	lines []string
}

func (l *loggedErrors) logf(_ context.Context, format string, v ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, v...))
}

func (l *loggedErrors) list() []string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return slices.Clone(l.lines)
}

// heldClient counts the read-only transactions a copy opens, which is the number of
// reads it makes of its table, and holds a read open while a test wants one in flight:
// either before the read has taken its rows (holdBefore), or after, once it has them and
// has not yet replaced the copy (holdAfter), as a read in flight has when a change is
// written behind it.
type heldClient struct {
	Client
	reads atomic.Int64

	mu      sync.Mutex
	before  chan struct{}
	after   chan struct{}
	entered chan struct{}
}

func newHeldClient(client Client) *heldClient {
	return &heldClient{Client: client, entered: make(chan struct{}, 1024)}
}

func (c *heldClient) ReadOnlyTransaction() ReadOnlyTransactionCloser {
	c.reads.Add(1)
	c.mu.Lock()
	before, after := c.before, c.after
	c.mu.Unlock()
	if before != nil {
		c.entered <- struct{}{}
		<-before
	}

	return &heldTransaction{ReadOnlyTransactionCloser: c.Client.ReadOnlyTransaction(), after: after, entered: c.entered}
}

// holdBefore holds the reads that begin from now before they take their rows, each
// announcing itself on entered, until release is called.
func (c *heldClient) holdBefore() (entered <-chan struct{}, release func()) {
	hold := make(chan struct{})
	c.mu.Lock()
	c.before = hold
	c.mu.Unlock()

	return c.entered, func() {
		c.mu.Lock()
		c.before = nil
		c.mu.Unlock()
		close(hold)
	}
}

// holdAfter holds the reads that begin from now once they have their rows, each
// announcing itself on entered then, until release is called.
func (c *heldClient) holdAfter() (entered <-chan struct{}, release func()) {
	hold := make(chan struct{})
	c.mu.Lock()
	c.after = hold
	c.mu.Unlock()

	return c.entered, func() {
		c.mu.Lock()
		c.after = nil
		c.mu.Unlock()
		close(hold)
	}
}

// awaitRead waits for a read the client holds open to begin, and fails the test when
// none does: the reread the test was written to see was never made.
func awaitRead(t *testing.T, entered <-chan struct{}) {
	t.Helper()

	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("no read of the table began")
	}
}

// heldTransaction is a read-only transaction that waits, as it is closed, for the hold
// its client was under when it was opened: the copy closes it once its rows are read.
type heldTransaction struct {
	ReadOnlyTransactionCloser
	after   chan struct{}
	entered chan struct{}
}

func (t *heldTransaction) Close() {
	t.ReadOnlyTransactionCloser.Close()
	if t.after != nil {
		t.entered <- struct{}{}
		<-t.after
	}
}

// idleRoster starts a roster over the client as an instance that gets no CPU between
// requests has it: no loop runs (noLoop), so nothing but a lookup rereads the table, on
// a clock the test moves, with freshBound as the backstop. What the roster logs of a
// reread that failed is collected.
func idleRoster(t *testing.T, client Client, opts ...TenantRosterOption) (*TenantRoster, *movedClock, *loggedErrors) {
	t.Helper()

	clock, logged := newMovedClock(), &loggedErrors{}
	r := NewTenantRoster(client, stationsTable, stationsKey, append(opts, WithTenantBackstop(freshBound))...)
	r.noLoop = true
	r.clock = clock.now
	r.logf = logged.logf
	if err := r.Start(t.Context()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	return r, clock, logged
}

// TestTenantRoster_idleInstance_backstop pins the backstop on an instance whose loop
// does not run, on the Spanner emulator: a tenant another instance made is not seen
// inside the backstop, with no read of the table, and is seen by the first lookup past
// it; lookups arriving together past the backstop cost one reread between them, and
// every one of them is answered from it.
func TestTenantRoster_idleInstance_backstop(t *testing.T) {
	t.Parallel()

	db, client := tenantDatabase(t, "roster-idle-backstop", "alpha")
	held := newHeldClient(client)
	r, clock, _ := idleRoster(t, held)
	if got := held.reads.Load(); got != 1 {
		t.Fatalf("reads after Start() = %d, want 1", got)
	}

	// Another instance makes beta. Nothing tells this one.
	insertStations(t, db, "beta")

	for _, age := range []time.Duration{0, freshBound / 2, freshBound} {
		clock.setAge(age)
		if r.Has("beta") {
			t.Errorf("%s after the last read: the lookup saw the new tenant, want the copy's answer inside the backstop", age)
		}
	}
	if got := held.reads.Load(); got != 1 {
		t.Errorf("reads inside the backstop = %d, want still 1", got)
	}

	// Past the backstop the instance gets requests again, many at once.
	clock.setAge(freshBound + time.Second)
	entered, release := held.holdBefore()
	seen := make(chan bool, freshLookups)
	for range freshLookups {
		go func() {
			seen <- r.Has("beta")
		}()
	}
	awaitRead(t, entered)
	// The other lookups arrive while the reread is in flight. The counts below hold
	// whenever they arrive: one that comes after the reread has landed reads nothing.
	time.Sleep(50 * time.Millisecond)
	release()

	for range freshLookups {
		if !<-seen {
			t.Error("a lookup past the backstop did not see the new tenant")
		}
	}
	if got := held.reads.Load(); got != 2 {
		t.Errorf("reads after %d lookups arriving together past the backstop = %d, want 2: the start's and one for them all", freshLookups, got)
	}

	// The backstop holds again from that reread.
	clock.advance(freshBound)
	if !r.Has("beta") || !r.Has("alpha") {
		t.Error("the copy lost a tenant after the reread")
	}
	if got := held.reads.Load(); got != 2 {
		t.Errorf("reads inside the backstop of the reread = %d, want still 2", got)
	}
}

// TestTenantRoster_idleInstance_everyLookup pins that each of the roster's lookups holds
// the copy to the backstop, on the Spanner emulator: past it, the lookup rereads the
// table once and answers from what it read.
func TestTenantRoster_idleInstance_everyLookup(t *testing.T) {
	t.Parallel()

	db, client := tenantDatabase(t, "roster-idle-lookups", "alpha")

	tests := []struct {
		name string
		// sees reports whether the lookup shows the tenant made behind the copy.
		sees func(t *testing.T, r *TenantRoster, made accesstypes.Domain) bool
	}{
		{
			name: "Has",
			sees: func(_ *testing.T, r *TenantRoster, made accesstypes.Domain) bool {
				return r.Has(made)
			},
		},
		{
			name: "Domains",
			sees: func(t *testing.T, r *TenantRoster, made accesstypes.Domain) bool {
				t.Helper()
				domains, err := r.Domains(t.Context())
				if err != nil {
					t.Fatalf("Domains() error = %v", err)
				}

				return slices.Contains(domains, made)
			},
		},
		{
			name: "Declarations",
			sees: func(t *testing.T, r *TenantRoster, made accesstypes.Domain) bool {
				t.Helper()
				declaration, err := r.Declarations(t.Context(), made)
				if err != nil {
					t.Fatalf("Declarations() error = %v", err)
				}

				return declaration != nil
			},
		},
		{
			name: "Name",
			sees: func(_ *testing.T, r *TenantRoster, made accesstypes.Domain) bool {
				return r.Name(made) == "Station "+string(made)
			},
		},
		{
			name: "Named",
			sees: func(_ *testing.T, r *TenantRoster, made accesstypes.Domain) bool {
				domain, err := r.Named("Station " + string(made))

				return err == nil && domain == made
			},
		},
	}
	// The lookups share one database and each makes a tenant of its own in it, so they
	// run in order.
	for i, tt := range tests {
		held := newHeldClient(client)
		r, clock, _ := idleRoster(t, held, WithTenantDeclarations(stationColumns), WithTenantNames("Name"))
		made := accesstypes.Domain("made-" + strconv.Itoa(i))
		insertStations(t, db, made)

		if tt.sees(t, r, made) {
			t.Errorf("%s: the lookup saw the new tenant inside the backstop", tt.name)
		}
		clock.advance(freshBound + time.Second)
		if !tt.sees(t, r, made) {
			t.Errorf("%s: the first lookup past the backstop did not see the new tenant", tt.name)
		}
		if got := held.reads.Load(); got != 2 {
			t.Errorf("%s: reads = %d, want 2: the start's and the lookup's past the backstop", tt.name, got)
		}
	}
}

// TestTenantRoster_idleInstance_signal pins a tenants signal on an instance whose loop
// does not run, on the Spanner emulator: once the signal is recorded, the next lookup
// rereads the table before it answers and sees the change, inside the backstop; the
// lookups after it read nothing until another signal or the backstop; and a signal that
// arrives while a reread is in flight is not cleared by it, so the lookup after it
// rereads once more.
func TestTenantRoster_idleInstance_signal(t *testing.T) {
	t.Parallel()

	db, client := tenantDatabase(t, "roster-idle-signal", "alpha")
	ctx := t.Context()
	signals := &fakeTenantSignals{}
	held := newHeldClient(client)
	r, clock, _ := idleRoster(t, held, WithTenantSignals(signals))

	// Another instance makes beta and signals the tenants kind; the signal reaches this
	// instance, whose loop does not run.
	insertStations(t, db, "beta")
	if err := signals.Signal(ctx, KindTenants); err != nil {
		t.Fatalf("Signal() error = %v", err)
	}
	if !r.Has("beta") {
		t.Error("the first lookup after the signal did not see the new tenant")
	}
	if got := held.reads.Load(); got != 2 {
		t.Errorf("reads after the first lookup behind a signal = %d, want 2: the start's and the signal's", got)
	}
	for range 5 {
		r.Has("beta")
	}
	if got := held.reads.Load(); got != 2 {
		t.Errorf("reads for the lookups after it = %d, want still 2", got)
	}

	// A signal while a reread is in flight. The reread in flight began behind an earlier
	// signal and has its rows; then another instance makes gamma and signals.
	if err := signals.Signal(ctx, KindTenants); err != nil {
		t.Fatalf("Signal() error = %v", err)
	}
	entered, release := held.holdAfter()
	inFlight := make(chan bool, 1)
	go func() {
		inFlight <- r.Has("gamma")
	}()
	awaitRead(t, entered)
	insertStations(t, db, "gamma")
	if err := signals.Signal(ctx, KindTenants); err != nil {
		t.Fatalf("Signal() error = %v", err)
	}
	release()
	if <-inFlight {
		t.Error("the lookup whose reread began before the tenant was made saw it")
	}
	if got := held.reads.Load(); got != 3 {
		t.Fatalf("reads once the reread in flight landed = %d, want 3", got)
	}

	if !r.Has("gamma") {
		t.Error("the lookup after a signal that arrived during a reread did not see the new tenant: the reread in flight cleared the signal")
	}
	if got := held.reads.Load(); got != 4 {
		t.Errorf("reads after it = %d, want 4: one more reread for the signal that arrived during the last", got)
	}
	r.Has("gamma")
	if got := held.reads.Load(); got != 4 {
		t.Errorf("reads for the lookup after that = %d, want still 4", got)
	}

	// With nothing signaled, the backstop alone asks for the next reread.
	clock.advance(freshBound + time.Second)
	r.Has("gamma")
	if got := held.reads.Load(); got != 5 {
		t.Errorf("reads for the first lookup past the backstop = %d, want 5", got)
	}
}

// TestTenantRoster_idleInstance_failedReread pins a reread that fails on a lookup's
// path, on the Spanner emulator: it is logged, the copy as it was answers, and the next
// lookup past the backstop tries the table again.
func TestTenantRoster_idleInstance_failedReread(t *testing.T) {
	t.Parallel()

	db, client := tenantDatabase(t, "roster-idle-failed", "alpha")
	failing := &failingClient{Client: client}
	r, clock, logged := idleRoster(t, failing)

	insertStations(t, db, "beta")
	clock.advance(freshBound + time.Second)

	steps := []struct {
		name         string
		fail         bool
		wantBeta     bool
		wantFailures int64
		wantLogged   int
	}{
		{name: "the reread past the backstop fails", fail: true, wantBeta: false, wantFailures: 1, wantLogged: 1},
		{name: "the next lookup tries the table again", fail: true, wantBeta: false, wantFailures: 2, wantLogged: 2},
		{name: "the lookup after the database is back sees the new tenant", fail: false, wantBeta: true, wantFailures: 2, wantLogged: 2},
		// The database fails again, and nobody notices: inside the backstop no read is tried.
		{name: "the lookup after it reads nothing", fail: true, wantBeta: true, wantFailures: 2, wantLogged: 2},
	}
	// One roster's history, so the steps run in order, one lookup each.
	for _, step := range steps {
		failing.fail.Store(step.fail)
		domains, err := r.Domains(t.Context())
		if err != nil {
			t.Fatalf("%s: Domains() error = %v", step.name, err)
		}
		if !slices.Contains(domains, "alpha") {
			t.Errorf("%s: the copy lost alpha; the copy as it was answers", step.name)
		}
		if got := slices.Contains(domains, "beta"); got != step.wantBeta {
			t.Errorf("%s: the lookup saw beta = %v, want %v", step.name, got, step.wantBeta)
		}
		if got := failing.failures.Load(); got != step.wantFailures {
			t.Errorf("%s: failed reads so far = %d, want %d", step.name, got, step.wantFailures)
		}
		if got := logged.list(); len(got) != step.wantLogged {
			t.Errorf("%s: failures logged so far = %q, want %d", step.name, got, step.wantLogged)
		}
	}
	for _, line := range logged.list() {
		if !strings.Contains(line, "rereading the "+stationsTable+" table failed") {
			t.Errorf("logged %q, want it to say the reread of the %s table failed", line, stationsTable)
		}
	}
}

// TestTenantRoster_tick pins the loop's backstop tick, on the Spanner emulator: every
// tick rereads the table, whatever the copy's age, so on an instance that is serving it
// is the loop that rereads as each backstop ends and not the request that finds it
// ended; and the lookup after a tick reads nothing.
func TestTenantRoster_tick(t *testing.T) {
	t.Parallel()

	db, client := tenantDatabase(t, "roster-tick", "alpha")
	held := newHeldClient(client)
	r, _, _ := idleRoster(t, held)

	// The clock does not move: the copy stays inside its backstop throughout.
	insertStations(t, db, "beta")
	for tick := int64(1); tick <= 3; tick++ {
		r.tick()
		if got := held.reads.Load(); got != 1+tick {
			t.Errorf("reads after tick %d = %d, want %d: the start's and one reread a tick", tick, got, 1+tick)
		}
	}
	if !r.Has("beta") {
		t.Error("the lookup after a tick did not see the tenant the tick read")
	}
	if got := held.reads.Load(); got != 4 {
		t.Errorf("reads after the lookup behind the ticks = %d, want still 4", got)
	}
}

// TestTenantRoster_servingInstance pins what the loop still does on an instance that is
// serving, and what it costs, on the Spanner emulator: a tenants signal shows the change
// to the very next lookup for one reread, whoever made it, the lookup or the loop; and
// the loop rereads on a signal and on its tick with no lookup asking.
func TestTenantRoster_servingInstance(t *testing.T) {
	t.Parallel()

	// copyHolds reads the roster's copy without a lookup, so no lookup's own reread can
	// be what brought a tenant in.
	copyHolds := func(r *TenantRoster, domain accesstypes.Domain) bool {
		set := r.set.Load()
		if set == nil {
			return false
		}
		_, ok := (*set)[domain]

		return ok
	}

	t.Run("a signal shows the change to the next lookup for one reread", func(t *testing.T) {
		t.Parallel()

		db, client := tenantDatabase(t, "roster-serving-signal", "alpha")
		ctx := t.Context()
		signals := &fakeTenantSignals{}
		held := newHeldClient(client)
		r := NewTenantRoster(held, stationsTable, stationsKey, WithTenantSignals(signals), WithTenantBackstop(time.Hour))
		if err := r.Start(ctx); err != nil {
			t.Fatalf("Start() error = %v", err)
		}

		insertStations(t, db, "beta")
		if err := signals.Signal(ctx, KindTenants); err != nil {
			t.Fatalf("Signal() error = %v", err)
		}
		if !r.Has("beta") {
			t.Error("the first lookup after the signal did not see the new tenant")
		}
		// The loop was woken by the same signal; whichever of the two reread the table,
		// the other found the reread made.
		time.Sleep(200 * time.Millisecond)
		if got := held.reads.Load(); got != 2 {
			t.Errorf("reads = %d, want 2: the start's and one for the signal, between the loop and the lookup", got)
		}
	})

	t.Run("the loop rereads on a signal with no lookup asking", func(t *testing.T) {
		t.Parallel()

		db, client := tenantDatabase(t, "roster-serving-loop-signal", "alpha")
		ctx := t.Context()
		signals := &fakeTenantSignals{}
		r := NewTenantRoster(client, stationsTable, stationsKey, WithTenantSignals(signals), WithTenantBackstop(time.Hour))
		if err := r.Start(ctx); err != nil {
			t.Fatalf("Start() error = %v", err)
		}

		insertStations(t, db, "beta")
		if err := signals.Signal(ctx, KindTenants); err != nil {
			t.Fatalf("Signal() error = %v", err)
		}
		if !eventually(t, 5*time.Second, func() bool { return copyHolds(r, "beta") }) {
			t.Error("the loop never reread the table on the signal")
		}
	})

	t.Run("the loop rereads on its tick with no lookup asking", func(t *testing.T) {
		t.Parallel()

		db, client := tenantDatabase(t, "roster-serving-loop-tick", "alpha")
		r := NewTenantRoster(client, stationsTable, stationsKey, WithTenantBackstop(50*time.Millisecond))
		if err := r.Start(t.Context()); err != nil {
			t.Fatalf("Start() error = %v", err)
		}

		insertStations(t, db, "beta")
		if !eventually(t, 5*time.Second, func() bool { return copyHolds(r, "beta") }) {
			t.Error("the loop never reread the table on its tick")
		}
	})
}
