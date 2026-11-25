package resource

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The feature flags these tests declare, both inserted disabled by the migration.
const (
	debriefsFlag Feature = "debriefs"
	beaconsFlag  Feature = "beacons"
)

// flipper is who flips a flag in these tests: another instance's administrator.
var flipper = ProcessEvent("harbormaster (test)")

// loadIdleFeatures loads the flags over the client, not yet followed, with the hooks of
// an instance that gets no CPU between requests: no loop will run (noLoop), so nothing
// but a lookup rereads the table, on a clock the test moves, with freshBound as the
// backstop. What the set logs of a reread that failed is collected. The database holds
// debriefsFlag and beaconsFlag, both off; base is the client the fixture writes through.
func loadIdleFeatures(t *testing.T, base, client Client) (*FeatureSet, *movedClock, *loggedErrors) {
	t.Helper()
	ctx := t.Context()

	if err := MigrateFeatures(ctx, base, []FeatureDeclaration{
		{Name: debriefsFlag, Description: "Debriefs.", Constant: "Debriefs"},
		{Name: beaconsFlag, Description: "Beacons.", Constant: "Beacons"},
	}); err != nil {
		t.Fatalf("MigrateFeatures() error = %v", err)
	}
	clock, logged := newMovedClock(), &loggedErrors{}
	features, err := LoadFeatures(ctx, client, WithFeatureBackstop(freshBound), func(s *FeatureSet) {
		s.noLoop = true
		s.clock = clock.now
		s.logf = logged.logf
	})
	if err != nil {
		t.Fatalf("LoadFeatures() error = %v", err)
	}

	return features, clock, logged
}

// idleFeatures is loadIdleFeatures followed for the life of the test.
func idleFeatures(t *testing.T, base, client Client, signals SignalSubscriber) (*FeatureSet, *movedClock, *loggedErrors) {
	t.Helper()

	features, clock, logged := loadIdleFeatures(t, base, client)
	if err := features.Follow(t.Context(), signals); err != nil {
		t.Fatalf("Follow() error = %v", err)
	}

	return features, clock, logged
}

// flip turns the flag on in the database, as another instance's flip commits it:
// nothing tells this instance's copy.
func flip(t *testing.T, base Client, name Feature) {
	t.Helper()

	if err := SetFeatureEnabled(t.Context(), base, name, true, flipper); err != nil {
		t.Fatalf("SetFeatureEnabled() error = %v", err)
	}
}

// TestFeatureSet_idleInstance_backstop pins the backstop on an instance whose loop does
// not run, on the Spanner emulator: a flag another instance flipped is not seen inside
// the backstop, with no read of the table, and is seen by the first lookup past it;
// lookups arriving together past the backstop cost one reread between them, and every
// one of them is answered from it.
func TestFeatureSet_idleInstance_backstop(t *testing.T) {
	t.Parallel()

	_, base := featureDatabase(t, "features-idle-backstop")
	held := newHeldClient(base)
	features, clock, _ := idleFeatures(t, base, held, &fakeSignals{})
	if got := held.reads.Load(); got != 1 {
		t.Fatalf("reads after LoadFeatures() = %d, want 1", got)
	}

	// Another instance turns debriefs on. Nothing tells this one.
	flip(t, base, debriefsFlag)

	for _, age := range []time.Duration{0, freshBound / 2, freshBound} {
		clock.setAge(age)
		if features.Enabled(debriefsFlag) {
			t.Errorf("%s after the last read: the lookup saw the flip, want the copy's answer inside the backstop", age)
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
			seen <- features.Enabled(debriefsFlag)
		}()
	}
	awaitRead(t, entered)
	// The other lookups arrive while the reread is in flight. The counts below hold
	// whenever they arrive: one that comes after the reread has landed reads nothing.
	time.Sleep(50 * time.Millisecond)
	release()

	for range freshLookups {
		if !<-seen {
			t.Error("a lookup past the backstop did not see the flip")
		}
	}
	if got := held.reads.Load(); got != 2 {
		t.Errorf("reads after %d lookups arriving together past the backstop = %d, want 2: the load's and one for them all", freshLookups, got)
	}

	// The backstop holds again from that reread.
	clock.advance(freshBound)
	if !features.Enabled(debriefsFlag) {
		t.Error("the copy lost the flip after the reread")
	}
	if got := held.reads.Load(); got != 2 {
		t.Errorf("reads inside the backstop of the reread = %d, want still 2", got)
	}
}

// TestFeatureSet_idleInstance_everyLookup pins that each of the set's lookups holds the
// copy to the backstop, on the Spanner emulator: past it, the lookup rereads the table
// once and answers from what it read.
func TestFeatureSet_idleInstance_everyLookup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// sees reports whether the lookup shows debriefs on.
		sees func(features *FeatureSet) bool
	}{
		{
			name: "Enabled",
			sees: func(features *FeatureSet) bool {
				return features.Enabled(debriefsFlag)
			},
		},
		{
			name: "Flag",
			sees: func(features *FeatureSet) bool {
				flag, ok := features.Flag(debriefsFlag)

				return ok && flag.Enabled && flag.UpdatedBy == flipper.String()
			},
		},
		{
			name: "EnabledNames",
			sees: func(features *FeatureSet) bool {
				return slices.Contains(features.EnabledNames(), string(debriefsFlag))
			},
		},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, base := featureDatabase(t, "features-idle-lookup-"+strconv.Itoa(i))
			held := newHeldClient(base)
			features, clock, _ := idleFeatures(t, base, held, &fakeSignals{})
			flip(t, base, debriefsFlag)

			if tt.sees(features) {
				t.Error("the lookup saw the flip inside the backstop")
			}
			clock.setAge(freshBound + time.Second)
			if !tt.sees(features) {
				t.Error("the first lookup past the backstop did not see the flip")
			}
			if got := held.reads.Load(); got != 2 {
				t.Errorf("reads = %d, want 2: the load's and the lookup's past the backstop", got)
			}
		})
	}
}

// TestFeatureSet_idleInstance_signal pins a features signal on an instance whose loop
// does not run, on the Spanner emulator: once the signal is recorded, the next lookup
// rereads the table before it answers and sees the flip, inside the backstop; the
// lookups after it read nothing until another signal or the backstop; and a signal that
// arrives while a reread is in flight is not cleared by it, so the lookup after it
// rereads once more.
func TestFeatureSet_idleInstance_signal(t *testing.T) {
	t.Parallel()

	_, base := featureDatabase(t, "features-idle-signal")
	ctx := t.Context()
	signals := &fakeSignals{}
	held := newHeldClient(base)
	features, clock, _ := idleFeatures(t, base, held, signals)

	// Another instance turns debriefs on and signals the features kind; the signal
	// reaches this instance, whose loop does not run.
	flip(t, base, debriefsFlag)
	if err := signals.Signal(ctx, KindFeatures); err != nil {
		t.Fatalf("Signal() error = %v", err)
	}
	if !features.Enabled(debriefsFlag) {
		t.Error("the first lookup after the signal did not see the flip")
	}
	if got := held.reads.Load(); got != 2 {
		t.Errorf("reads after the first lookup behind a signal = %d, want 2: the load's and the signal's", got)
	}
	for range 5 {
		features.Enabled(debriefsFlag)
	}
	if got := held.reads.Load(); got != 2 {
		t.Errorf("reads for the lookups after it = %d, want still 2", got)
	}

	// A signal while a reread is in flight. The reread in flight began behind an earlier
	// signal and has its rows; then another instance turns beacons on and signals.
	if err := signals.Signal(ctx, KindFeatures); err != nil {
		t.Fatalf("Signal() error = %v", err)
	}
	entered, release := held.holdAfter()
	inFlight := make(chan bool, 1)
	go func() {
		inFlight <- features.Enabled(beaconsFlag)
	}()
	awaitRead(t, entered)
	flip(t, base, beaconsFlag)
	if err := signals.Signal(ctx, KindFeatures); err != nil {
		t.Fatalf("Signal() error = %v", err)
	}
	release()
	if <-inFlight {
		t.Error("the lookup whose reread began before the flip saw it")
	}
	if got := held.reads.Load(); got != 3 {
		t.Fatalf("reads once the reread in flight landed = %d, want 3", got)
	}

	if !features.Enabled(beaconsFlag) {
		t.Error("the lookup after a signal that arrived during a reread did not see the flip: the reread in flight cleared the signal")
	}
	if got := held.reads.Load(); got != 4 {
		t.Errorf("reads after it = %d, want 4: one more reread for the signal that arrived during the last", got)
	}
	features.Enabled(beaconsFlag)
	if got := held.reads.Load(); got != 4 {
		t.Errorf("reads for the lookup after that = %d, want still 4", got)
	}

	// With nothing signaled, the backstop alone asks for the next reread.
	clock.advance(freshBound + time.Second)
	features.Enabled(beaconsFlag)
	if got := held.reads.Load(); got != 5 {
		t.Errorf("reads for the first lookup past the backstop = %d, want 5", got)
	}
}

// TestFeatureSet_idleInstance_failedReread pins a reread that fails on a lookup's path,
// on the Spanner emulator: it is logged and the copy as it was answers; the lookups that
// come in the pause after it read nothing and log nothing, whatever is signaled
// meanwhile; and the first lookup after the pause tries the table again. A flip of this
// instance ends the pause, though its own reload failed: the database took the flip, so
// the next lookup reads, and a read that fails then begins a pause of its own.
func TestFeatureSet_idleInstance_failedReread(t *testing.T) {
	t.Parallel()

	_, base := featureDatabase(t, "features-idle-failed")
	failing := &failingClient{Client: base}
	held := newHeldClient(failing)
	signals := &fakeSignals{}
	features, clock, logged := idleFeatures(t, base, held, signals)

	flip(t, base, debriefsFlag)
	clock.setAge(freshBound + time.Second)
	ended, end := context.WithCancel(t.Context())
	end()
	// failedReload is a flip of this instance whose request had ended when its handler
	// came to reload: the flip is recorded, and the reload's own read fails.
	failedReload := func() {
		if err := features.Reload(ended); err == nil {
			t.Error("Reload() under a context that has ended error = nil, want the read's failure")
		}
	}

	steps := []struct {
		name string
		// wait is how long after the step before it the lookup is made, and do what
		// happens right before the lookup.
		wait time.Duration
		do   func()
		// fail says the database fails the lookup's read, if it makes one.
		fail         bool
		wantOn       bool
		wantReads    int64
		wantFailures int64
		wantLogged   int
	}{
		{name: "the reread past the backstop fails", fail: true, wantOn: false, wantReads: 1, wantFailures: 1, wantLogged: 1},
		{
			name: "a lookup at the end of the pause reads nothing, though the database is back",
			wait: featureReadPause - time.Nanosecond, wantOn: false, wantReads: 0, wantFailures: 1, wantLogged: 1,
		},
		{
			name: "a features signal inside the pause does not make a lookup read",
			do: func() {
				if err := signals.Signal(t.Context(), KindFeatures); err != nil {
					t.Errorf("Signal() error = %v", err)
				}
			},
			wantOn: false, wantReads: 0, wantFailures: 1, wantLogged: 1,
		},
		{
			name: "the first lookup after the pause tries the table again",
			wait: time.Nanosecond, fail: true, wantOn: false, wantReads: 1, wantFailures: 2, wantLogged: 2,
		},
		{
			name:   "a lookup inside the next pause, with no flip of this instance, reads nothing",
			wantOn: false, wantReads: 0, wantFailures: 2, wantLogged: 2,
		},
		{
			name: "a flip of this instance inside the pause, whose own reload failed, makes the next lookup read; that read fails and is logged",
			do:   failedReload,
			fail: true, wantOn: false, wantReads: 1, wantFailures: 3, wantLogged: 3,
		},
		{
			name:   "the read that failed after the flip began a pause of its own: a lookup inside it reads nothing",
			wantOn: false, wantReads: 0, wantFailures: 3, wantLogged: 3,
		},
		{
			name:   "a second flip inside that pause makes the next lookup read, and it sees the flag on",
			do:     failedReload,
			wantOn: true, wantReads: 1, wantFailures: 3, wantLogged: 3,
		},
		// The database fails again, and nobody notices: inside the backstop no read is tried.
		{name: "the lookup after it reads nothing", fail: true, wantOn: true, wantReads: 0, wantFailures: 3, wantLogged: 3},
	}
	// One set's history, so the steps run in order, one lookup each.
	for _, step := range steps {
		clock.advance(step.wait)
		if step.do != nil {
			step.do()
		}
		failing.fail.Store(step.fail)
		before := held.reads.Load()
		flag, ok := features.Flag(debriefsFlag)
		if !ok {
			t.Errorf("%s: the copy lost the flag; the copy as it was answers", step.name)
		}
		if flag.Enabled != step.wantOn {
			t.Errorf("%s: the lookup saw the flag on = %v, want %v", step.name, flag.Enabled, step.wantOn)
		}
		if got := held.reads.Load() - before; got != step.wantReads {
			t.Errorf("%s: reads the lookup made = %d, want %d", step.name, got, step.wantReads)
		}
		if got := failing.failures.Load(); got != step.wantFailures {
			t.Errorf("%s: failed reads so far = %d, want %d", step.name, got, step.wantFailures)
		}
		if got := logged.list(); len(got) != step.wantLogged {
			t.Errorf("%s: failures logged so far = %q, want %d", step.name, got, step.wantLogged)
		}
	}
	for _, line := range logged.list() {
		if !strings.Contains(line, "feature flags: rereading the table failed") {
			t.Errorf("logged %q, want it to say the reread of the feature flags failed", line)
		}
	}
}

// TestFeatureSet_reloadThatFailed pins what a reload that failed leaves behind, on the
// Spanner emulator: the flip the caller reloaded for is recorded, so the next lookup
// rereads the table under the set's own context and sees it, inside the backstop and
// with no signal, and the lookups after it read nothing. It is the instance that
// flipped a flag whose request ended before its handler reread: without the record it
// would not show its own flip until the backstop.
func TestFeatureSet_reloadThatFailed(t *testing.T) {
	t.Parallel()

	_, base := featureDatabase(t, "features-reload-failed")
	held := newHeldClient(base)
	features, _, logged := idleFeatures(t, base, held, &fakeSignals{})

	// This instance commits the flip, and the request that made it has ended by the
	// time its handler rereads.
	flip(t, base, debriefsFlag)
	ended, end := context.WithCancel(t.Context())
	end()
	if err := features.Reload(ended); err == nil {
		t.Fatal("Reload() under a context that has ended error = nil, want the read's failure")
	}
	reads := held.reads.Load()

	if !features.Enabled(debriefsFlag) {
		t.Error("the lookup after a reload that failed did not see the flip this instance made")
	}
	if got := held.reads.Load() - reads; got != 1 {
		t.Errorf("reads for the lookup after a reload that failed = %d, want 1", got)
	}
	if !features.Enabled(debriefsFlag) || features.Enabled(beaconsFlag) {
		t.Error("the lookups after the reread did not answer from it")
	}
	if got := held.reads.Load() - reads; got != 1 {
		t.Errorf("reads after the lookups behind the reread = %d, want still 1", got)
	}
	if got := logged.list(); len(got) != 0 {
		t.Errorf("failures logged = %q, want none: the caller has the reload's error, and the lookup's reread succeeded", got)
	}
}

// TestFeatureSet_keptLife pins the life a copy is kept fresh for, on the Spanner
// emulator: a set that was loaded and never followed stays as it was read, whatever its
// age, and one whose Follow has ended rereads no more.
func TestFeatureSet_keptLife(t *testing.T) {
	t.Parallel()

	t.Run("a set never followed stays as it was read", func(t *testing.T) {
		t.Parallel()

		_, base := featureDatabase(t, "features-life-unfollowed")
		held := newHeldClient(base)
		features, clock, _ := loadIdleFeatures(t, base, held)

		flip(t, base, debriefsFlag)
		clock.setAge(freshBound + time.Second)
		if features.Enabled(debriefsFlag) {
			t.Error("a set never followed saw a flip made behind it")
		}
		if got := held.reads.Load(); got != 1 {
			t.Errorf("reads = %d, want 1: the load's alone", got)
		}
	})

	t.Run("a set whose Follow has ended rereads no more", func(t *testing.T) {
		t.Parallel()

		_, base := featureDatabase(t, "features-life-ended")
		held := newHeldClient(base)
		signals := &fakeSignals{}
		features, clock, logged := loadIdleFeatures(t, base, held)
		ctx, end := context.WithCancel(t.Context())
		if err := features.Follow(ctx, signals); err != nil {
			t.Fatalf("Follow() error = %v", err)
		}
		end()

		flip(t, base, debriefsFlag)
		clock.setAge(freshBound + time.Second)
		if features.Enabled(debriefsFlag) {
			t.Error("a set whose Follow ended saw a flip made behind it")
		}
		if got := held.reads.Load(); got != 1 {
			t.Errorf("reads = %d, want 1: the load's alone", got)
		}
		if got := logged.list(); len(got) != 0 {
			t.Errorf("failures logged = %q, want none", got)
		}
	})
}

// TestFeatureSet_tick pins the loop's backstop tick, on the Spanner emulator: every tick
// rereads the table, whatever the copy's age, so on an instance that is serving it is
// the loop that rereads as each backstop ends and not the request that finds it ended;
// and the lookup after a tick reads nothing.
func TestFeatureSet_tick(t *testing.T) {
	t.Parallel()

	_, base := featureDatabase(t, "features-tick")
	held := newHeldClient(base)
	features, _, _ := idleFeatures(t, base, held, &fakeSignals{})

	// The clock does not move: the copy stays inside its backstop throughout.
	flip(t, base, debriefsFlag)
	for tick := int64(1); tick <= 3; tick++ {
		features.tick()
		if got := held.reads.Load(); got != 1+tick {
			t.Errorf("reads after tick %d = %d, want %d: the load's and one reread a tick", tick, got, 1+tick)
		}
	}
	if !features.Enabled(debriefsFlag) {
		t.Error("the lookup after a tick did not see the flip the tick read")
	}
	if got := held.reads.Load(); got != 4 {
		t.Errorf("reads after the lookup behind the ticks = %d, want still 4", got)
	}
}

// TestFeatureSet_servingInstance pins what the loop still does on an instance that is
// serving, and what it costs, on the Spanner emulator: a features signal shows the flip
// to the very next lookup for one reread, whoever made it, the lookup or the loop; and
// the loop rereads on a signal and on its tick with no lookup asking.
func TestFeatureSet_servingInstance(t *testing.T) {
	t.Parallel()

	// copyHolds reads the set's copy without a lookup, so no lookup's own reread can be
	// what brought a flip in.
	copyHolds := func(features *FeatureSet, name Feature) bool {
		features.mu.RLock()
		defer features.mu.RUnlock()

		return features.flags[name].Enabled
	}
	served := func(
		t *testing.T, name string, client func(base Client) Client, backstop time.Duration, opts ...FeatureOption,
	) (base Client, features *FeatureSet, signals *fakeSignals) {
		t.Helper()
		ctx := t.Context()

		_, base = featureDatabase(t, name)
		if err := MigrateFeatures(ctx, base, []FeatureDeclaration{{Name: debriefsFlag, Description: "Debriefs.", Constant: "Debriefs"}}); err != nil {
			t.Fatalf("MigrateFeatures() error = %v", err)
		}
		features, err := LoadFeatures(ctx, client(base), append(opts, WithFeatureBackstop(backstop))...)
		if err != nil {
			t.Fatalf("LoadFeatures() error = %v", err)
		}
		signals = &fakeSignals{}
		if err := features.Follow(ctx, signals); err != nil {
			t.Fatalf("Follow() error = %v", err)
		}

		return base, features, signals
	}
	plain := func(base Client) Client {
		return base
	}

	t.Run("a signal shows the flip to the next lookup for one reread", func(t *testing.T) {
		t.Parallel()

		var held *heldClient
		base, features, signals := served(t, "features-serving-signal", func(base Client) Client {
			held = newHeldClient(base)

			return held
		}, time.Hour)

		flip(t, base, debriefsFlag)
		if err := signals.Signal(t.Context(), KindFeatures); err != nil {
			t.Fatalf("Signal() error = %v", err)
		}
		if !features.Enabled(debriefsFlag) {
			t.Error("the first lookup after the signal did not see the flip")
		}
		// The loop was woken by the same signal; whichever of the two reread the table,
		// the other found the reread made.
		time.Sleep(200 * time.Millisecond)
		if got := held.reads.Load(); got != 2 {
			t.Errorf("reads = %d, want 2: the load's and one for the signal, between the loop and the lookup", got)
		}
	})

	t.Run("the loop rereads on a signal with no lookup asking", func(t *testing.T) {
		t.Parallel()

		base, features, signals := served(t, "features-serving-loop-signal", plain, time.Hour)
		flip(t, base, debriefsFlag)
		if err := signals.Signal(t.Context(), KindFeatures); err != nil {
			t.Fatalf("Signal() error = %v", err)
		}
		if !eventually(t, 5*time.Second, func() bool { return copyHolds(features, debriefsFlag) }) {
			t.Error("the loop never reread the table on the signal")
		}
	})

	t.Run("the loop rereads on its tick with no lookup asking", func(t *testing.T) {
		t.Parallel()

		// The set's clock does not move, so the copy never passes its backstop: what
		// rereads is the tick asking for the read, as the loop's ticker must.
		still := newMovedClock()
		base, features, _ := served(t, "features-serving-loop-tick", plain, 50*time.Millisecond, func(s *FeatureSet) {
			s.clock = still.now
		})
		flip(t, base, debriefsFlag)
		if !eventually(t, 5*time.Second, func() bool { return copyHolds(features, debriefsFlag) }) {
			t.Error("the loop never reread the table on its tick")
		}
	})
}
