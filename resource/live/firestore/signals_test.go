package firestore_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/impulseframework/impulse/resource/live"
	livefirestore "github.com/impulseframework/impulse/resource/live/firestore"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The waits the signals tests allow: a signal reaches a listener within settle, and a
// kind that must stay quiet is watched for quiet. settle is long because it is only ever
// waited out on a failure: on a loaded runner, where the Firestore emulator shares the
// machine with the Spanner emulator and the generation suite, it has taken over ten
// seconds, and once over forty-five, to establish a listener on a database created a
// moment before, and a passing wait ends the instant the signal arrives.
const (
	settle = 2 * time.Minute
	quiet  = 500 * time.Millisecond
)

// counter counts the signals one subscription heard.
type counter struct {
	mu sync.Mutex
	n  int
	// heard is signaled on every count, so a waiter wakes without polling.
	heard chan struct{}
}

func newCounter() *counter {
	return &counter{heard: make(chan struct{}, 64)}
}

func (c *counter) signal() {
	c.mu.Lock()
	c.n++
	c.mu.Unlock()
	c.heard <- struct{}{}
}

func (c *counter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.n
}

// waitFor waits until the counter reaches want, failing the test past settle.
func (c *counter) waitFor(t *testing.T, who string, want int) {
	t.Helper()

	deadline := time.After(settle)
	for c.count() < want {
		select {
		case <-c.heard:
		case <-deadline:
			t.Fatalf("%s heard %d signals within %s, want %d", who, c.count(), settle, want)
		}
	}
}

// subscribe subscribes a counter to the kind on the service, stopped when the test ends.
func subscribe(t *testing.T, svc *livefirestore.Service, kind live.Kind) *counter {
	t.Helper()

	c := newCounter()
	stop, err := svc.Subscribe(kind, c.signal)
	if err != nil {
		t.Fatalf("Subscribe(%s) error = %v", kind, err)
	}
	t.Cleanup(stop)

	return c
}

// signal writes a signal of the kind through the service, failing the test on an error.
// Two answers are retried, both the test's allowance for the emulator and nothing the
// service does. The emulator answers a write as already canceled when it rides the
// connection whose listener streams were canceled a moment before (the drop in the
// reopen test), although the document is written and the listeners go on to see it; a
// real Firestore never cancels a call whose context is alive. And on a loaded runner the
// emulator has taken longer over a write than the service's own limit on a signal
// allows, where an application logs the failure and its subscribers reread at their
// backstop.
func signal(t *testing.T, svc *livefirestore.Service, kind live.Kind) {
	t.Helper()

	var err error
	for attempt := range 5 {
		if attempt > 0 {
			time.Sleep(200 * time.Millisecond)
		}
		err = svc.Signal(t.Context(), kind)
		if err == nil {
			return
		}
		if t.Context().Err() != nil || (!strings.Contains(err.Error(), "already cancel") && !errors.Is(err, livefirestore.ErrSignalLimit)) {
			break
		}
		t.Logf("Signal(%s) attempt %d answered %v; retrying", kind, attempt+1, err)
	}
	t.Fatalf("Signal(%s) error = %v", kind, err)
}

// snapshotLog records the times every snapshot an instance's listener received carried,
// so a count that disagrees with a test's expectation is explained by what Firestore
// delivered rather than guessed at.
type snapshotLog struct {
	mu   sync.Mutex
	seen []string
}

// logSnapshots attaches a snapshotLog to the service.
func logSnapshots(svc *livefirestore.Service) *snapshotLog {
	l := &snapshotLog{}
	livefirestore.ObserveSnapshots(svc, func(times map[live.Kind]time.Time) {
		kinds := make([]string, 0, len(times))
		for kind, at := range times {
			kinds = append(kinds, string(kind)+"@"+at.UTC().Format("15:04:05.000000"))
		}
		sort.Strings(kinds)
		l.mu.Lock()
		l.seen = append(l.seen, "{"+strings.Join(kinds, " ")+"}")
		l.mu.Unlock()
	})

	return l
}

// count is the number of snapshots received.
func (l *snapshotLog) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return len(l.seen)
}

func (l *snapshotLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return strings.Join(l.seen, " ")
}

// expect is one counter's expectation: no wake at all, or at least n when the count is
// a floor. A count is a floor where a write may race a listener's own delivery: under
// load the emulator has delivered one write as more than one advancing snapshot, and a
// wake is a nudge to reread, so a kind woken more often than its writes is not wrong,
// while a kind woken less often, or a kind that must stay quiet waking at all, is.
type expect struct {
	n     int
	floor bool
}

// none expects no wake at all: the kind must stay quiet.
func none() expect {
	return expect{}
}

// atLeast expects n wakes or more.
func atLeast(n int) expect {
	return expect{n: n, floor: true}
}

// instance is one service under test with the log of the snapshots its listener received.
type instance struct {
	svc *livefirestore.Service
	log *snapshotLog
}

// explainOnFailure prints, when the test fails, what each instance's listener received,
// what the signals document holds when read back through each instance's own client
// with a plain get, and what a fresh listener opened on the database receives first, so
// a wait that ran out is read off three facts rather than guessed at: a kind the document
// lacks is a write that never landed; a kind the document holds that the instance's
// listener never received, while a fresh listener's first snapshot carries it, is an
// update the emulator did not deliver on an established stream (the listener having
// received its first, empty snapshot). The service is not changed for the test: a
// listener that ends with an error is reopened; one that falls silent is not detected,
// and whether that is the emulator's or the service's is what the three facts decide.
func explainOnFailure(t *testing.T, database, emulator string, instances ...instance) {
	t.Helper()
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		// t.Context() is done once Cleanup runs; the diagnosis has its own allowance.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for i, in := range instances {
			times, err := livefirestore.ReadSignals(ctx, in.svc)
			t.Logf("instance %d: its listener received %s; the document read back through its own client holds %s (error: %v)", i+1, in.log, formatTimes(times), err)
		}
		fresh, err := livefirestore.New(ctx, livefirestore.Config{ProjectID: testProject, DatabaseID: database, EmulatorHost: emulator})
		if err != nil {
			t.Logf("a fresh listener could not be opened: %v", err)

			return
		}
		defer func() {
			if err := fresh.Close(); err != nil {
				t.Logf("closing the fresh listener: %v", err)
			}
		}()
		freshLog := logSnapshots(fresh)
		stop, err := fresh.Subscribe(live.KindFeatures, func() {})
		if err != nil {
			t.Logf("a fresh listener could not subscribe: %v", err)

			return
		}
		defer stop()
		for freshLog.count() == 0 && ctx.Err() == nil {
			time.Sleep(50 * time.Millisecond)
		}
		t.Logf("a fresh listener on the same database received %s within %s", freshLog, 30*time.Second)
	})
}

// formatTimes writes the kinds a read of the document holds the way snapshotLog writes
// a snapshot, so the two read alike.
func formatTimes(times map[live.Kind]time.Time) string {
	kinds := make([]string, 0, len(times))
	for kind, at := range times {
		kinds = append(kinds, string(kind)+"@"+at.UTC().Format("15:04:05.000000"))
	}
	sort.Strings(kinds)

	return "{" + strings.Join(kinds, " ") + "}"
}

// checkCounts compares the counts with the expectations and explains a mismatch with
// what each instance's listener received.
func checkCounts(t *testing.T, what string, want []expect, got []int, logs ...*snapshotLog) {
	t.Helper()

	ok := len(want) == len(got)
	for i := range want {
		if !ok {
			break
		}
		if want[i].floor && got[i] < want[i].n || !want[i].floor && got[i] != want[i].n {
			ok = false
		}
	}
	if ok {
		return
	}
	wants := make([]string, 0, len(want))
	for _, w := range want {
		if w.floor {
			wants = append(wants, fmt.Sprintf(">=%d", w.n))
		} else {
			wants = append(wants, fmt.Sprintf("%d", w.n))
		}
	}
	delivered := make([]string, 0, len(logs))
	for i, l := range logs {
		delivered = append(delivered, fmt.Sprintf("instance %d received %s", i+1, l))
	}
	t.Errorf("%s: counts %v, want [%s]; %s", what, got, strings.Join(wants, " "), strings.Join(delivered, "; "))
}

// settled waits the quiet period and returns the counts, in order, so a test can pin
// that nothing more arrived.
func settled(counters ...*counter) []int {
	time.Sleep(quiet)
	counts := make([]int, 0, len(counters))
	for _, c := range counters {
		counts = append(counts, c.count())
	}

	return counts
}

// TestService_signals pins the signals over the emulator with two instances on one
// document: a signal of a kind wakes that kind's subscriptions on both instances and
// no other kind's, the state at the start is not a signal, a subscription made after
// the listener began hears the signals after it, and a stopped subscription hears
// nothing more while the others go on.
func TestService_signals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// run drives two instances on one document and returns the counts to pin, after
		// the quiet period.
		run  func(t *testing.T, first, second *livefirestore.Service) []int
		want []expect
	}{
		{
			name: "a signal of a kind wakes its subscriptions on both instances and never another kind's",
			run: func(t *testing.T, first, second *livefirestore.Service) []int {
				t.Helper()
				firstFeatures := subscribe(t, first, live.KindFeatures)
				firstTenants := subscribe(t, first, live.KindTenants)
				secondFeatures := subscribe(t, second, live.KindFeatures)
				signal(t, first, live.KindFeatures)
				firstFeatures.waitFor(t, "the first instance's features subscription", 1)
				secondFeatures.waitFor(t, "the second instance's features subscription", 1)

				return settled(firstFeatures, firstTenants, secondFeatures)
			},
			want: []expect{atLeast(1), none(), atLeast(1)},
		},
		{
			name: "the other kind wakes its own subscription alone",
			run: func(t *testing.T, first, second *livefirestore.Service) []int {
				t.Helper()
				firstFeatures := subscribe(t, first, live.KindFeatures)
				firstTenants := subscribe(t, first, live.KindTenants)
				secondFeatures := subscribe(t, second, live.KindFeatures)
				signal(t, second, live.KindTenants)
				firstTenants.waitFor(t, "the first instance's tenants subscription", 1)

				return settled(firstFeatures, firstTenants, secondFeatures)
			},
			want: []expect{none(), atLeast(1), none()},
		},
		{
			name: "a subscription made before the first snapshot is woken once for a kind the document holds; one made after hears only the signals after it",
			run: func(t *testing.T, first, second *livefirestore.Service) []int {
				t.Helper()
				// Written before either instance listens: the state at the start.
				signal(t, first, live.KindPolicy)
				// The first instance's listener opens here; its first snapshot holds the
				// policy kind, so the subscription made before it wakes once.
				firstPolicy := subscribe(t, first, live.KindPolicy)
				firstPolicy.waitFor(t, "the first instance's policy subscription, at the start", 1)
				signal(t, first, live.KindPolicy)
				firstPolicy.waitFor(t, "the first instance's policy subscription", 2)
				// The second instance starts listening now, with two signals in the past:
				// one wake, not two.
				secondPolicy := subscribe(t, second, live.KindPolicy)
				secondPolicy.waitFor(t, "the second instance's policy subscription, at the start", 1)
				// A subscription made after the first instance's listener began hears
				// nothing of the past.
				late := subscribe(t, first, live.KindPolicy)
				if got := settled(secondPolicy, late); got[0] != 1 || got[1] != 0 {
					t.Fatalf("after the start: second instance %d (want one wake), late subscription %d (want none): the past is one nudge at most", got[0], got[1])
				}
				signal(t, second, live.KindPolicy)
				firstPolicy.waitFor(t, "the first instance's policy subscription", 3)
				secondPolicy.waitFor(t, "the second instance's policy subscription", 2)
				late.waitFor(t, "the late subscription", 1)

				return settled(firstPolicy, secondPolicy, late)
			},
			want: []expect{atLeast(3), atLeast(2), atLeast(1)},
		},
		{
			name: "a stopped subscription hears nothing more; the others go on",
			run: func(t *testing.T, first, second *livefirestore.Service) []int {
				t.Helper()
				stopped := newCounter()
				stop, err := first.Subscribe(live.KindFeatures, stopped.signal)
				if err != nil {
					t.Fatalf("Subscribe() error = %v", err)
				}
				kept := subscribe(t, first, live.KindFeatures)
				other := subscribe(t, second, live.KindFeatures)
				signal(t, first, live.KindFeatures)
				stopped.waitFor(t, "the subscription to be stopped", 1)
				kept.waitFor(t, "the kept subscription", 1)
				other.waitFor(t, "the other instance's subscription", 1)
				// The first signal's deliveries are complete after the quiet period, so
				// what the stopped subscription hears from here on is the second signal's.
				time.Sleep(quiet)
				before := stopped.count()
				stop()
				signal(t, first, live.KindFeatures)
				kept.waitFor(t, "the kept subscription", 2)
				other.waitFor(t, "the other instance's subscription", 2)
				counts := settled(stopped, kept, other)
				counts[0] -= before

				return counts
			},
			want: []expect{none(), atLeast(2), atLeast(2)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			database := databaseFor(t)
			first := newServiceIn(t, database)
			second := newServiceIn(t, database)
			firstLog, secondLog := logSnapshots(first), logSnapshots(second)
			explainOnFailure(t, database, firestoreEmulator(t), instance{first, firstLog}, instance{second, secondLog})
			checkCounts(t, "signal counts", tt.want, tt.run(t, first, second), firstLog, secondLog)
		})
	}
}

// TestService_signals_reopen pins the one place that owns reconnection: a listener that
// ends is reopened after the backoff, and on the reopen every kind whose time advanced
// while it was down fires once, on both instances, while a kind that did not stays
// quiet.
func TestService_signals_reopen(t *testing.T) {
	t.Parallel()

	database := databaseFor(t)
	// The first wait is long enough to signal twice while the listener is down, and
	// short enough to settle well inside the deadline.
	backoff := livefirestore.WithReopenBackoff(time.Second, time.Second)
	first := newServiceIn(t, database, backoff)
	second := newServiceIn(t, database, backoff)
	firstLog, secondLog := logSnapshots(first), logSnapshots(second)
	explainOnFailure(t, database, firestoreEmulator(t), instance{first, firstLog}, instance{second, secondLog})
	firstFeatures := subscribe(t, first, live.KindFeatures)
	firstTenants := subscribe(t, first, live.KindTenants)
	firstPolicy := subscribe(t, first, live.KindPolicy)
	secondFeatures := subscribe(t, second, live.KindFeatures)
	secondTenants := subscribe(t, second, live.KindTenants)
	// Subscribe waits on nothing, so a signal heard on both instances is what proves
	// both listeners live before they are dropped; otherwise a drop could land before a
	// listener's first snapshot and the reopen would count one advance more.
	signal(t, first, live.KindFeatures)
	firstFeatures.waitFor(t, "the first instance's features subscription", 1)
	secondFeatures.waitFor(t, "the second instance's features subscription", 1)

	livefirestore.DropListener(first)
	livefirestore.DropListener(second)
	// Two kinds advance while both listeners are down, the features kind twice.
	signal(t, first, live.KindFeatures)
	signal(t, first, live.KindFeatures)
	signal(t, first, live.KindTenants)

	firstFeatures.waitFor(t, "the first instance's features subscription", 2)
	firstTenants.waitFor(t, "the first instance's tenants subscription", 1)
	secondFeatures.waitFor(t, "the second instance's features subscription", 2)
	secondTenants.waitFor(t, "the second instance's tenants subscription", 1)
	// The kinds that advanced while the listeners were down wake once each on the
	// reopen, the writes before the drop having been heard; the kind that did not
	// advance stays quiet.
	checkCounts(t, "signal counts after the reopen", []expect{atLeast(2), atLeast(1), none(), atLeast(2), atLeast(1)},
		settled(firstFeatures, firstTenants, firstPolicy, secondFeatures, secondTenants), firstLog, secondLog)

	// The reopened listener is a working one: a later signal reaches it at once.
	signal(t, second, live.KindPolicy)
	firstPolicy.waitFor(t, "the first instance's policy subscription", 1)
}

// unhurried is the option of a service whose signals the service's own limit does not
// end: what the tests take that hold a write in flight while they arrange the callers
// around it, or that end the callers' own contexts, so that what ends a signal in them
// is what the test ended.
func unhurried() livefirestore.Option {
	return livefirestore.WithSignalLimit(settle)
}

// writeWatch observes one service's signal writes: it counts the ones begun and the
// ones ended, keeps the context each was sent under and how it ended, and holds the
// ones a test names in flight, before they are sent, until the test lets them go.
type writeWatch struct {
	mu       sync.Mutex
	begun    int
	ended    int
	contexts []context.Context
	outcomes map[int]writeOutcome
	held     map[int]chan struct{}
	// reached is told the number of each held write as it begins.
	reached chan int
}

// writeOutcome is how one write ended: its error, and the error of the context it was
// sent under at that moment, nil where the context had not ended.
type writeOutcome struct {
	err    error
	ctxErr error
}

// watchWrites watches the service's signal writes.
func watchWrites(svc *livefirestore.Service) *writeWatch {
	w := &writeWatch{outcomes: make(map[int]writeOutcome), held: make(map[int]chan struct{}), reached: make(chan int, 16)}
	livefirestore.ObserveWrites(svc, func(ctx context.Context, _ live.Kind) func(error) {
		w.mu.Lock()
		w.begun++
		n := w.begun
		w.contexts = append(w.contexts, ctx)
		hold := w.held[n]
		w.mu.Unlock()
		if hold != nil {
			w.reached <- n
			<-hold
		}

		return func(err error) {
			ctxErr := ctx.Err()
			w.mu.Lock()
			defer w.mu.Unlock()
			w.ended++
			w.outcomes[n] = writeOutcome{err: err, ctxErr: ctxErr}
		}
	})

	return w
}

// hold holds the service's nth write in flight from the moment it begins, and returns
// what lets it go; the test's end lets it go too.
func (w *writeWatch) hold(t *testing.T, n int) (release func()) {
	t.Helper()

	hold := make(chan struct{})
	w.mu.Lock()
	w.held[n] = hold
	w.mu.Unlock()
	release = sync.OnceFunc(func() {
		close(hold)
	})
	t.Cleanup(release)

	return release
}

// awaitHeld waits until the nth write has begun and is held.
func (w *writeWatch) awaitHeld(t *testing.T, n int) {
	t.Helper()

	select {
	case got := <-w.reached:
		if got != n {
			t.Fatalf("write %d is held, want write %d", got, n)
		}
	case <-time.After(settle):
		t.Fatalf("write %d did not begin within %s", n, settle)
	}
}

// counts returns how many writes have begun and how many have ended.
func (w *writeWatch) counts() (begun, ended int) {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.begun, w.ended
}

// sentUnder returns the context the nth write was sent under.
func (w *writeWatch) sentUnder(t *testing.T, n int) context.Context {
	t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.contexts) < n {
		t.Fatalf("%d writes have begun, want write %d among them", len(w.contexts), n)
	}

	return w.contexts[n-1]
}

// outcome returns how the nth write ended.
func (w *writeWatch) outcome(t *testing.T, n int) writeOutcome {
	t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	outcome, ok := w.outcomes[n]
	if !ok {
		t.Fatalf("write %d has not ended", n)
	}

	return outcome
}

// signaling makes a signal of the features kind on a goroutine of its own, as a request
// of the instance would, and returns where its answer arrives.
func signaling(ctx context.Context, svc *livefirestore.Service) <-chan error {
	answer := make(chan error, 1)
	go func() {
		answer <- svc.Signal(ctx, live.KindFeatures)
	}()

	return answer
}

// awaitWaiting waits until n callers wait for the features kind's covering write that
// is not yet due: the signals made since the write in flight began.
func awaitWaiting(t *testing.T, svc *livefirestore.Service, n int) {
	t.Helper()

	deadline := time.Now().Add(settle)
	for {
		waiting := livefirestore.SignalsWaiting(svc, live.KindFeatures)
		if waiting == n {
			return
		}
		if waiting > n {
			t.Fatalf("callers waiting for the covering write that is not yet due = %d, want %d: a signal joined a write made before it", waiting, n)
		}
		if time.Now().After(deadline) {
			t.Fatalf("callers waiting for the covering write = %d within %s, want %d", waiting, settle, n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// answered waits for a signal's answer.
func answered(t *testing.T, who string, answer <-chan error) error {
	t.Helper()

	select {
	case err := <-answer:
		return err
	case <-time.After(settle):
		t.Fatalf("%s did not return within %s", who, settle)

		return nil
	}
}

// stillWaiting fails the test if the signal has answered: it is to wait for a write
// that has not been made.
func stillWaiting(t *testing.T, who string, answer <-chan error) {
	t.Helper()

	select {
	case err := <-answer:
		t.Fatalf("%s returned (%v) before the write that covers it was made", who, err)
	case <-time.After(100 * time.Millisecond):
	}
}

// nothingSending fails the test unless the service has made exactly the given number of
// writes, every one of them ended, and holds no write slot: what must be so at the
// moment the last overlapping caller has returned, with no wait.
func nothingSending(t *testing.T, svc *livefirestore.Service, writes *writeWatch, want int) {
	t.Helper()

	if begun, ended := writes.counts(); begun != want || ended != want {
		t.Errorf("as the last caller returned: writes begun = %d, ended = %d, want %d and %d", begun, ended, want, want)
	}
	if !livefirestore.SignalsIdle(svc) {
		t.Error("as the last caller returned: a signal write is in flight or owed, want nothing left to send")
	}
}

// commitRefuser refuses the commit a test names among the ones a service sends, as a
// store that refuses a write does, and passes every other call on.
type commitRefuser struct {
	commits atomic.Int64
	refused atomic.Int64
}

// errCommitRefused is what the refused commit answers.
const errCommitRefused = "the store refused the write"

func (r *commitRefuser) unary(ctx context.Context, method string, req, reply any, conn *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	if strings.HasSuffix(method, "/Commit") && r.commits.Add(1) == r.refused.Load() {
		return status.Error(codes.PermissionDenied, errCommitRefused) //nolint:wrapcheck // the status is the answer the client is to see, as the store would give it
	}

	return invoker(ctx, method, req, reply, conn, opts...)
}

// TestService_Signal_coalesces pins the publisher's coalescing: a burst of signals of
// one kind while a write of it is in flight is one covering write, made once the write
// in flight has ended, and every signal of the burst waits for it. When the last of them
// has returned the service has made two writes and nothing is left to send: no write is
// made later, on a goroutine no caller waits for. The subscribers still wake, since the
// covering write begins after the last call.
func TestService_Signal_coalesces(t *testing.T) {
	t.Parallel()

	const burst = 10
	database := databaseFor(t)
	writer := newServiceIn(t, database, unhurried())
	reader := newServiceIn(t, database)
	features := subscribe(t, reader, live.KindFeatures)
	writes := watchWrites(writer)
	release := writes.hold(t, 1)

	first := signaling(t.Context(), writer)
	writes.awaitHeld(t, 1)
	covered := make([]<-chan error, 0, burst)
	for range burst {
		covered = append(covered, signaling(t.Context(), writer))
	}
	awaitWaiting(t, writer, burst)
	for _, answer := range covered {
		stillWaiting(t, "a signal made during the write in flight", answer)
	}

	release()
	if err := answered(t, "the first signal", first); err != nil {
		t.Fatalf("the first Signal() error = %v", err)
	}
	for _, answer := range covered {
		if err := answered(t, "a signal of the burst", answer); err != nil {
			t.Errorf("a Signal() of the burst error = %v", err)
		}
	}
	nothingSending(t, writer, writes, 2)

	// The reader wakes at least once for the burst, and at most once per write.
	features.waitFor(t, "the reader's features subscription", 1)
	if n := settled(features)[0]; n > 2 {
		t.Errorf("the reader heard %d signals, want at most one per write (2)", n)
	}
	if begun, _ := writes.counts(); begun != 2 {
		t.Errorf("writes = %d once the callers had long returned, want still 2: the write in flight and one covering write for the %d signals made during it", begun, burst)
	}
}

// TestService_Signal_coveringWriteFails pins a covering write the store refuses: its
// failure is the failure of every caller that waited for it, the first caller's own
// write having succeeded, and the slot is free for the next signal.
func TestService_Signal_coveringWriteFails(t *testing.T) {
	t.Parallel()

	const burst = 5
	database := databaseFor(t)
	refuser := &commitRefuser{}
	writer := newServiceIn(t, database, unhurried(), livefirestore.WithDialOptions(grpc.WithChainUnaryInterceptor(refuser.unary)))
	writes := watchWrites(writer)
	release := writes.hold(t, 1)

	first := signaling(t.Context(), writer)
	writes.awaitHeld(t, 1)
	covered := make([]<-chan error, 0, burst)
	for range burst {
		covered = append(covered, signaling(t.Context(), writer))
	}
	awaitWaiting(t, writer, burst)
	// The first write is held before it is sent, so the covering write is the second
	// commit the service sends.
	refuser.refused.Store(2)

	release()
	if err := answered(t, "the first signal", first); err != nil {
		t.Fatalf("the first Signal() error = %v, want its own write to succeed", err)
	}
	for _, answer := range covered {
		err := answered(t, "a signal of the burst", answer)
		if err == nil || !strings.Contains(err.Error(), errCommitRefused) {
			t.Errorf("a Signal() the refused write covered error = %v, want the covering write's failure", err)
		}
	}
	nothingSending(t, writer, writes, 2)

	if err := writer.Signal(t.Context(), live.KindFeatures); err != nil {
		t.Errorf("Signal() after the refused covering write error = %v, want the slot free and the write taken", err)
	}
	nothingSending(t, writer, writes, 3)
}

// TestService_Signal_duringTheCoveringWrite pins which write covers a signal: one made
// once the covering write has begun is not covered by it, since that write may have
// reached the subscribers before the change the signal is for, and waits for the next.
func TestService_Signal_duringTheCoveringWrite(t *testing.T) {
	t.Parallel()

	database := databaseFor(t)
	writer := newServiceIn(t, database, unhurried())
	writes := watchWrites(writer)
	releaseFirst := writes.hold(t, 1)
	releaseCovering := writes.hold(t, 2)

	first := signaling(t.Context(), writer)
	writes.awaitHeld(t, 1)
	second := signaling(t.Context(), writer)
	awaitWaiting(t, writer, 1)
	releaseFirst()
	if err := answered(t, "the first signal", first); err != nil {
		t.Fatalf("the first Signal() error = %v", err)
	}
	writes.awaitHeld(t, 2)

	third := signaling(t.Context(), writer)
	awaitWaiting(t, writer, 1)
	stillWaiting(t, "a signal made during the covering write", third)
	stillWaiting(t, "the signal whose covering write is in flight", second)

	releaseCovering()
	if err := answered(t, "the second signal", second); err != nil {
		t.Errorf("the second Signal() error = %v", err)
	}
	if err := answered(t, "the third signal", third); err != nil {
		t.Errorf("the third Signal() error = %v", err)
	}
	nothingSending(t, writer, writes, 3)
}

// TestService_Signal_callerWhoseContextEnds pins what one caller's context ending does
// to a covering write: the caller returns its context's error, and the write is made by
// a caller still waiting, where there is one, and by nobody where there is none. No
// caller's leaving fails another's signal, and none leaves the slot taken.
func TestService_Signal_callerWhoseContextEnds(t *testing.T) {
	t.Parallel()

	type callerKey struct{}
	// caller is a request with a context of its own, which names it.
	caller := func(t *testing.T, name string) (ctx context.Context, end context.CancelFunc) {
		t.Helper()

		return context.WithCancel(context.WithValue(t.Context(), callerKey{}, name))
	}

	t.Run("a waiting caller leaves, and the write stays with the caller still waiting", func(t *testing.T) {
		t.Parallel()

		writer := newServiceIn(t, databaseFor(t), unhurried())
		writes := watchWrites(writer)
		release := writes.hold(t, 1)
		first := signaling(t.Context(), writer)
		writes.awaitHeld(t, 1)
		leaving, leave := caller(t, "leaving")
		defer leave()
		left := signaling(leaving, writer)
		stayed := signaling(t.Context(), writer)
		awaitWaiting(t, writer, 2)

		leave()
		if err := answered(t, "the caller whose context ended", left); !errors.Is(err, context.Canceled) {
			t.Errorf("Signal() of the caller whose context ended error = %v, want its context's", err)
		}
		stillWaiting(t, "the caller still waiting", stayed)

		release()
		if err := answered(t, "the first signal", first); err != nil {
			t.Errorf("the first Signal() error = %v", err)
		}
		if err := answered(t, "the caller still waiting", stayed); err != nil {
			t.Errorf("Signal() of the caller still waiting error = %v, want the covering write made for it", err)
		}
		nothingSending(t, writer, writes, 2)
	})

	t.Run("the only waiting caller leaves, and no covering write is made", func(t *testing.T) {
		t.Parallel()

		writer := newServiceIn(t, databaseFor(t), unhurried())
		writes := watchWrites(writer)
		release := writes.hold(t, 1)
		first := signaling(t.Context(), writer)
		writes.awaitHeld(t, 1)
		leaving, leave := caller(t, "leaving")
		defer leave()
		left := signaling(leaving, writer)
		awaitWaiting(t, writer, 1)

		leave()
		if err := answered(t, "the caller whose context ended", left); !errors.Is(err, context.Canceled) {
			t.Errorf("Signal() of the caller whose context ended error = %v, want its context's", err)
		}
		release()
		if err := answered(t, "the first signal", first); err != nil {
			t.Errorf("the first Signal() error = %v", err)
		}
		nothingSending(t, writer, writes, 1)
		time.Sleep(quiet)
		nothingSending(t, writer, writes, 1)
	})

	t.Run("the caller making the covering write loses its context, and another waiting caller makes it", func(t *testing.T) {
		t.Parallel()

		database := databaseFor(t)
		writer := newServiceIn(t, database, unhurried())
		reader := newServiceIn(t, database)
		features := subscribe(t, reader, live.KindFeatures)
		writes := watchWrites(writer)
		releaseFirst := writes.hold(t, 1)
		releaseCovering := writes.hold(t, 2)
		first := signaling(t.Context(), writer)
		writes.awaitHeld(t, 1)
		ends := make(map[string]context.CancelFunc)
		answers := make(map[string]<-chan error)
		for _, name := range []string{"one", "other"} {
			ctx, end := caller(t, name)
			t.Cleanup(end)
			ends[name] = end
			answers[name] = signaling(ctx, writer)
		}
		awaitWaiting(t, writer, 2)
		releaseFirst()
		if err := answered(t, "the first signal", first); err != nil {
			t.Fatalf("the first Signal() error = %v", err)
		}
		writes.awaitHeld(t, 2)

		// Whichever caller took the turn loses its context under its write.
		maker, _ := writes.sentUnder(t, 2).Value(callerKey{}).(string)
		waiter := "one"
		if maker == "one" {
			waiter = "other"
		}
		if ends[maker] == nil {
			t.Fatalf("the covering write was sent under the context of %q, want one of the two callers waiting for it", maker)
		}
		ends[maker]()
		releaseCovering()

		// The write's own failure is the caller's: the client answers a call whose
		// context was canceled with its own error for it.
		if err := answered(t, "the caller that lost its context", answers[maker]); err == nil || !strings.Contains(err.Error(), context.Canceled.Error()) {
			t.Errorf("Signal() of the caller that lost its context under the covering write error = %v, want its write's failure for the context", err)
		}
		if err := answered(t, "the caller still waiting", answers[waiter]); err != nil {
			t.Errorf("Signal() of the caller still waiting error = %v, want the covering write made by it", err)
		}
		nothingSending(t, writer, writes, 3)
		if got, _ := writes.sentUnder(t, 3).Value(callerKey{}).(string); got != waiter {
			t.Errorf("the write after the failed one was sent under the context of %q, want the caller still waiting (%q)", got, waiter)
		}
		features.waitFor(t, "the reader's features subscription", 1)
	})

	t.Run("the caller making the covering write loses its context with nobody else waiting, and the slot is free", func(t *testing.T) {
		t.Parallel()

		writer := newServiceIn(t, databaseFor(t), unhurried())
		writes := watchWrites(writer)
		releaseFirst := writes.hold(t, 1)
		releaseCovering := writes.hold(t, 2)
		first := signaling(t.Context(), writer)
		writes.awaitHeld(t, 1)
		making, end := caller(t, "making")
		defer end()
		made := signaling(making, writer)
		awaitWaiting(t, writer, 1)
		releaseFirst()
		if err := answered(t, "the first signal", first); err != nil {
			t.Fatalf("the first Signal() error = %v", err)
		}
		writes.awaitHeld(t, 2)

		end()
		releaseCovering()
		if err := answered(t, "the caller that lost its context", made); err == nil || !strings.Contains(err.Error(), context.Canceled.Error()) {
			t.Errorf("Signal() of the caller that lost its context under the covering write error = %v, want its write's failure for the context", err)
		}
		nothingSending(t, writer, writes, 2)

		if err := writer.Signal(t.Context(), live.KindFeatures); err != nil {
			t.Errorf("Signal() after it error = %v, want the slot free and the write taken", err)
		}
		nothingSending(t, writer, writes, 3)
	})
}

// TestService_Signal_makerWhoseDeadlinePassed pins what becomes of a covering write the
// client refuses for its maker's deadline before the maker's context has ended. A
// context ends when its timer runs, which on a busy machine is some time after its
// deadline, and the client does not wait for it: it answers a call whose deadline has
// passed DeadlineExceeded, the context still open. That is the maker's own time ending,
// as a context that ended under the write is: the maker fails alone, with its deadline's
// error, and a caller still waiting makes the write under its own context.
func TestService_Signal_makerWhoseDeadlinePassed(t *testing.T) {
	t.Parallel()

	const (
		// late is how many callers of one round wait under a deadline that has passed.
		late = 3
		// rounds bounds the rounds made until one of those callers took the turn. Which
		// waiting caller takes it is the scheduler's choice: the late callers are made
		// first, and in the round where the caller whose context lasts takes it all the
		// same, no write is refused and the round shows nothing.
		rounds = 40
	)
	type callerKey struct{}
	writer := newServiceIn(t, databaseFor(t), unhurried())
	writes := watchWrites(writer)

	// With the slot free the caller writes itself, and the refusal is its own.
	if err := writer.Signal(livefirestore.Overdue(t.Context()), live.KindFeatures); !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, livefirestore.ErrSignalLimit) {
		t.Errorf("Signal() with the slot free, under a deadline that had passed, error = %v, want its own deadline's and not the limit's", err)
	}
	if outcome := writes.outcome(t, 1); outcome.ctxErr != nil {
		t.Errorf("the context of that write had ended (%v) when the client refused it, want it open", outcome.ctxErr)
	}
	nothingSending(t, writer, writes, 1)

	for round := range rounds {
		begun, _ := writes.counts()
		firstWrite := begun + 1
		release := writes.hold(t, firstWrite)
		first := signaling(t.Context(), writer)
		writes.awaitHeld(t, firstWrite)
		lateAnswers := make([]<-chan error, 0, late)
		for range late {
			lateAnswers = append(lateAnswers, signaling(livefirestore.Overdue(context.WithValue(t.Context(), callerKey{}, "late")), writer))
			awaitWaiting(t, writer, len(lateAnswers))
		}
		lasting := signaling(context.WithValue(t.Context(), callerKey{}, "lasting"), writer)
		awaitWaiting(t, writer, late+1)

		release()
		if err := answered(t, "the first signal", first); err != nil {
			t.Fatalf("round %d: the first Signal() error = %v", round, err)
		}
		if err := answered(t, "the caller whose context lasts", lasting); err != nil {
			t.Fatalf("round %d: Signal() of the caller whose context lasts error = %v, want the covering write made for it: a write refused for its maker's deadline is that maker's failure alone", round, err)
		}
		var failed int
		for _, answer := range lateAnswers {
			err := answered(t, "a caller whose deadline had passed", answer)
			if err == nil {
				// Covered by the write the caller whose context lasts made.
				continue
			}
			failed++
			if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, livefirestore.ErrSignalLimit) {
				t.Errorf("round %d: Signal() of a caller whose deadline had passed error = %v, want its own deadline's and not the limit's", round, err)
			}
		}

		// The writes of the round after the first: the ones the late callers made, each
		// refused, and last the one made under the context that lasts.
		_, ended := writes.counts()
		var refused int
		for n := firstWrite + 1; n <= ended; n++ {
			maker, _ := writes.sentUnder(t, n).Value(callerKey{}).(string)
			outcome := writes.outcome(t, n)
			switch {
			case maker == "late":
				refused++
				if outcome.err == nil || !strings.Contains(outcome.err.Error(), "code = DeadlineExceeded") {
					t.Errorf("round %d: write %d, sent under a deadline that had passed, error = %v, want the client's DeadlineExceeded", round, n, outcome.err)
				}
				if outcome.ctxErr != nil {
					t.Errorf("round %d: the context of write %d had ended (%v) when the client refused it, want it open: the refusal for a context that ended is another test's", round, n, outcome.ctxErr)
				}
			case maker != "lasting" || n != ended:
				t.Errorf("round %d: write %d of %d was sent under the context of %q, want the late callers' refused writes and then the lasting caller's", round, n, ended, maker)
			case outcome.err != nil:
				t.Errorf("round %d: the write made under the context that lasts error = %v", round, outcome.err)
			}
		}
		if failed != refused {
			t.Errorf("round %d: %d late callers failed and %d writes were refused, want every refused write its maker's failure and nobody else's", round, failed, refused)
		}
		nothingSending(t, writer, writes, ended)
		if refused > 0 {
			t.Logf("round %d: %d writes were refused for their makers' deadlines, each with its context open, and the caller whose context lasts made the write", round, refused)

			return
		}
	}
	t.Fatalf("in %d rounds no caller whose deadline had passed took the covering write's turn, so no write was refused for its maker's deadline", rounds)
}

// TestService_Signal_manyCallers runs many callers signaling one kind at once, some of
// them under a context that ends within milliseconds, while they wait or under the
// write they make, and pins what must hold whatever the interleaving: every caller
// returns; a caller that is answered nil was covered by a write that began after its
// call, succeeded, and had ended when the caller returned; a caller whose context
// lasted is answered nil; the writes of the kind never overlap; no call makes more than
// one write; and nothing is left to send once the last caller has returned.
func TestService_Signal_manyCallers(t *testing.T) {
	t.Parallel()

	const (
		callers = 24
		rounds  = 15
	)
	type write struct {
		begun, ended int64
		err          error
	}
	type call struct {
		asked, returned int64
		// lasting says the call's context was the test's own, which does not end.
		lasting bool
		err     error
	}
	writer := newServiceIn(t, databaseFor(t), unhurried())
	var (
		// seq orders the calls and the writes: every call takes a number as it is made
		// and as it returns, every write as it begins and as it ends.
		seq        atomic.Int64
		running    atomic.Int64
		overlapped atomic.Bool
		mu         sync.Mutex
		writes     []write
	)
	// callKey names, on a call's context, the call a write is sent for.
	type callKey struct{}
	writesOf := make(map[int]int)
	livefirestore.ObserveWrites(writer, func(ctx context.Context, _ live.Kind) func(error) {
		if running.Add(1) > 1 {
			overlapped.Store(true)
		}
		begun := seq.Add(1)
		maker, _ := ctx.Value(callKey{}).(int)

		return func(err error) {
			ended := seq.Add(1)
			running.Add(-1)
			mu.Lock()
			defer mu.Unlock()
			writes = append(writes, write{begun: begun, ended: ended, err: err})
			writesOf[maker]++
		}
	})

	calls := make(chan call, callers*rounds)
	var wg sync.WaitGroup
	for c := range callers {
		wg.Go(func() {
			for r := range rounds {
				// Every call is numbered from one, so a write sent under no call's context
				// counts under none of them.
				ctx, cancel := context.WithValue(t.Context(), callKey{}, 1+c*rounds+r), context.CancelFunc(func() {})
				lasting := (c+r)%3 != 0
				if !lasting {
					ctx, cancel = context.WithTimeout(ctx, time.Duration(1+(c*7+r*3)%8)*time.Millisecond)
				}
				asked := seq.Add(1)
				err := writer.Signal(ctx, live.KindFeatures)
				returned := seq.Add(1)
				cancel()
				calls <- call{asked: asked, returned: returned, lasting: lasting, err: err}
			}
		})
	}
	returned := make(chan struct{})
	go func() {
		wg.Wait()
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(settle):
		t.Fatalf("the callers did not all return within %s: a signal is held by a write nobody makes", settle)
	}
	close(calls)

	if !livefirestore.SignalsIdle(writer) || running.Load() != 0 {
		t.Errorf("as the last caller returned: a signal write is in flight or owed (%d being sent), want nothing left to send", running.Load())
	}
	if overlapped.Load() {
		t.Error("two writes of one kind were being sent at once")
	}
	mu.Lock()
	defer mu.Unlock()
	for maker, made := range writesOf {
		if maker == 0 {
			t.Errorf("%d writes were sent under the context of no call, want every write made inside a call", made)
		} else if made > 1 {
			t.Errorf("call %d made %d writes, want no call to make more than one", maker, made)
		}
	}
	var answered, covered int
	for made := range calls {
		answered++
		if made.err != nil {
			// The emulator answers a call as already canceled when it rides a connection
			// on which calls were canceled a moment before (see signal); no other failure
			// of a call whose context lasted is expected, the service's or the store's.
			if made.lasting && !strings.Contains(made.err.Error(), "already cancel") {
				t.Errorf("a Signal() whose context lasted error = %v, want it covered by a write", made.err)
			}

			continue
		}
		covered++
		if !slices.ContainsFunc(writes, func(w write) bool {
			return w.err == nil && w.begun > made.asked && w.ended < made.returned
		}) {
			t.Errorf("a Signal() made at %d and answered nil at %d was covered by no write that began after it, succeeded and had ended by then", made.asked, made.returned)
		}
	}
	if answered != callers*rounds {
		t.Errorf("calls answered = %d, want %d", answered, callers*rounds)
	}
	if covered == 0 || len(writes) >= covered {
		t.Errorf("%d of %d calls were covered by %d writes: the callers did not overlap, so the run pinned nothing of what it is for", covered, answered, len(writes))
	}
}

// hungChannel is a channel that does not answer: every commit a service sends waits for
// its context to end and fails with the context's error, as a call to a backend that
// never answers does. It counts the commits tried and keeps the deadline of each. Every
// commit has one: the Firestore client gives a commit sixty seconds of its own where
// the caller's context sets none, which is how long a signal's write waited on such a
// channel before the service had a limit.
type hungChannel struct {
	mu        sync.Mutex
	deadlines []time.Time
}

func (h *hungChannel) unary(ctx context.Context, method string, req, reply any, conn *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	if !strings.HasSuffix(method, "/Commit") {
		return invoker(ctx, method, req, reply, conn, opts...)
	}
	deadline, _ := ctx.Deadline()
	h.mu.Lock()
	h.deadlines = append(h.deadlines, deadline)
	h.mu.Unlock()
	<-ctx.Done()

	return status.FromContextError(ctx.Err()).Err() //nolint:wrapcheck // the status is the answer the client is to see, as a call that ran out of time gives it
}

// tried returns the deadline of each commit tried so far, in the order they were tried.
func (h *hungChannel) tried() []time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()

	return slices.Clone(h.deadlines)
}

// hungService is a service on a channel that does not answer, with the service's own
// limit on a signal as an application runs it: no option of the tests' lengthens it.
func hungService(t *testing.T) (*livefirestore.Service, *hungChannel) {
	t.Helper()

	channel := &hungChannel{}
	svc, err := livefirestore.New(t.Context(), livefirestore.Config{
		ProjectID:    testProject,
		DatabaseID:   databaseFor(t),
		EmulatorHost: firestoreEmulator(t),
	}, livefirestore.WithDialOptions(grpc.WithChainUnaryInterceptor(channel.unary)))
	if err != nil {
		t.Fatalf("firestore.New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := svc.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	return svc, channel
}

// timed is a signal's answer, when it was asked for and how long the call took.
type timed struct {
	err   error
	asked time.Time
	took  time.Duration
}

// timedSignal makes a signal of the features kind on a goroutine of its own and returns
// where its answer arrives, with how long the call took. Its asked and took count from
// when that goroutine runs, which on a loaded runner may be some time after timedSignal
// returns: a time counted from before the call, such as the caller's own deadline, is
// held against when the call returned (asked plus took), not against took.
func timedSignal(ctx context.Context, svc *livefirestore.Service) <-chan timed {
	answer := make(chan timed, 1)
	go func() {
		asked := time.Now()
		err := svc.Signal(ctx, live.KindFeatures)
		answer <- timed{err: err, asked: asked, took: time.Since(asked)}
	}()

	return answer
}

// atTheLimit waits for a signal's answer, for a few times the service's limit and no
// longer: a signal that is still out then is held by the channel.
func atTheLimit(t *testing.T, who string, answer <-chan timed) timed {
	t.Helper()

	select {
	case got := <-answer:
		return got
	case <-time.After(5 * live.SignalTimeout):
		t.Fatalf("%s did not return within %s: the channel that does not answer holds it", who, 5*live.SignalTimeout)

		return timed{}
	}
}

// TestService_Signal_limit pins the service's own limit on a signal, on a channel that
// does not answer: a caller that brings no deadline returns at the limit with the
// limit's error, where it would otherwise wait as long as the channel does; a caller
// with a shorter deadline of its own returns at that, with its context's error; and a
// caller that waits for another's write is held to its own limit as the one that writes
// is: the limit covers its wait and its write together, so the write it makes once the
// first has failed has what is left of its two seconds and no more. Each tries the
// channel at most once.
func TestService_Signal_limit(t *testing.T) {
	t.Parallel()

	const (
		// slack is what a loaded runner may add to a call that returns at its time.
		slack = 3 * time.Second
		// deadlineSlack is what it may add between a test's taking the time and the
		// service's setting the deadline a moment later, which no load on the channel
		// touches: a deadline is a time on the clock, not a wait.
		deadlineSlack = 500 * time.Millisecond
	)

	t.Run("a caller with no deadline returns at the limit with the limit's error", func(t *testing.T) {
		t.Parallel()

		svc, channel := hungService(t)
		got := atTheLimit(t, "the signal", timedSignal(t.Context(), svc))

		if !errors.Is(got.err, livefirestore.ErrSignalLimit) {
			t.Errorf("Signal() error = %v, want the limit's (%v)", got.err, livefirestore.ErrSignalLimit)
		}
		if got.took < live.SignalTimeout-50*time.Millisecond || got.took > live.SignalTimeout+slack {
			t.Errorf("Signal() took %s, want the limit of %s", got.took, live.SignalTimeout)
		}
		tried := channel.tried()
		if len(tried) != 1 {
			t.Fatalf("commits tried = %d, want 1", len(tried))
		}
		if by := tried[0].Sub(got.asked); by < live.SignalTimeout-50*time.Millisecond || by > live.SignalTimeout+deadlineSlack {
			t.Errorf("the write was sent under a deadline %s after the signal was asked for, want the limit of %s", by, live.SignalTimeout)
		}
		if !livefirestore.SignalsIdle(svc) {
			t.Error("a signal write is in flight or owed after the signal returned")
		}
	})

	t.Run("a caller's shorter deadline still wins, with its context's error", func(t *testing.T) {
		t.Parallel()

		const own = 100 * time.Millisecond
		svc, _ := hungService(t)
		// The caller's time counts from when its context is made, as its deadline does,
		// and not from the signal's asked (see timedSignal).
		made := time.Now()
		ctx, cancel := context.WithTimeout(t.Context(), own)
		defer cancel()
		got := atTheLimit(t, "the signal", timedSignal(ctx, svc))

		if !errors.Is(got.err, context.DeadlineExceeded) || errors.Is(got.err, livefirestore.ErrSignalLimit) {
			t.Errorf("Signal() error = %v, want the caller's own deadline's and not the limit's", got.err)
		}
		if took := got.asked.Add(got.took).Sub(made); took < own-10*time.Millisecond || took >= live.SignalTimeout {
			t.Errorf("Signal() returned %s after the caller made its context, want the caller's own %s, well short of the limit of %s", took, own, live.SignalTimeout)
		}
	})

	t.Run("a caller that waits for another's write is held to its own limit too", func(t *testing.T) {
		t.Parallel()

		svc, channel := hungService(t)
		first := timedSignal(t.Context(), svc)
		deadline := time.Now().Add(settle)
		for len(channel.tried()) == 0 {
			if time.Now().After(deadline) {
				t.Fatalf("the first signal's write did not begin within %s", settle)
			}
			time.Sleep(5 * time.Millisecond)
		}
		second := timedSignal(t.Context(), svc)

		var waited timed
		for who, answer := range map[string]<-chan timed{"the signal that wrote": first, "the signal that waited": second} {
			got := atTheLimit(t, who, answer)
			if !errors.Is(got.err, livefirestore.ErrSignalLimit) {
				t.Errorf("%s: Signal() error = %v, want the limit's (%v)", who, got.err, livefirestore.ErrSignalLimit)
			}
			if got.took < live.SignalTimeout-50*time.Millisecond || got.took > live.SignalTimeout+slack {
				t.Errorf("%s: Signal() took %s, want the limit of %s from its own call", who, got.took, live.SignalTimeout)
			}
			if answer == second {
				waited = got
			}
		}
		tried := channel.tried()
		if len(tried) > 2 {
			t.Errorf("commits tried = %d, want no more than one for each of the two signals", len(tried))
		}
		// The write the waiting signal made, where its turn came before its time ended:
		// it was sent with what was left of the signal's own two seconds, counted from
		// when the signal was asked for, and not with two seconds of its own.
		if len(tried) == 2 {
			if by := tried[1].Sub(waited.asked); by < live.SignalTimeout-50*time.Millisecond || by > live.SignalTimeout+deadlineSlack {
				t.Errorf("the waiting signal's write was sent under a deadline %s after the signal was asked for, want the limit of %s for its wait and its write together", by, live.SignalTimeout)
			}
		}
		if !livefirestore.SignalsIdle(svc) {
			t.Error("a signal write is in flight or owed after both signals returned")
		}
	})
}

// TestService_Signal_failure pins the error path: a closed client's write fails and the
// error is returned for the caller to log, and a fresh client signals again.
func TestService_Signal_failure(t *testing.T) {
	t.Parallel()

	database := databaseFor(t)
	closed, err := livefirestore.New(t.Context(), livefirestore.Config{
		ProjectID:    testProject,
		DatabaseID:   database,
		EmulatorHost: firestoreEmulator(t),
	})
	if err != nil {
		t.Fatalf("firestore.New() error = %v", err)
	}
	if err := closed.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := closed.Signal(t.Context(), live.KindFeatures); err == nil {
		t.Fatal("Signal() on a closed client error = nil, want the write's failure")
	}
	if _, err := closed.Subscribe(live.KindFeatures, func() {}); err == nil {
		t.Fatal("Subscribe() on a closed client error = nil, want a refusal")
	}

	fresh := newServiceIn(t, database)
	features := subscribe(t, fresh, live.KindFeatures)
	signal(t, fresh, live.KindFeatures)
	features.waitFor(t, "the fresh client's features subscription", 1)
}

// TestService_signals_firstSnapshotBound pins the first bound on a listener's silence: a
// listener whose first snapshot never arrives is taken as never established and reopened
// after the bound, and the reopened listener's first snapshot wakes the subscriptions
// made before it.
func TestService_signals_firstSnapshotBound(t *testing.T) {
	t.Parallel()

	database := databaseFor(t)
	svc := newServiceIn(t, database, livefirestore.WithReopenBackoff(time.Second, time.Second), livefirestore.WithFirstSnapshotWithin(5*time.Second))
	log := logSnapshots(svc)
	explainOnFailure(t, database, firestoreEmulator(t), instance{svc, log})
	// The document holds the kind before any listener opens, so the first snapshot a
	// listener delivers wakes the subscription; the first listener's is swallowed, as a
	// stream that never speaks would leave it.
	signal(t, svc, live.KindFeatures)
	livefirestore.StallFirstSnapshots(svc, 1)
	features := subscribe(t, svc, live.KindFeatures)
	features.waitFor(t, "the features subscription, through the reopened listener", 1)
	if got := livefirestore.LateReopens(svc); got < 1 {
		t.Errorf("LateReopens() = %d, want at least 1: the subscription woke without the bound reopening the listener", got)
	}
	// The reopened listener is a working one: a later signal reaches it at once.
	signal(t, svc, live.KindFeatures)
	features.waitFor(t, "the features subscription", 2)
}

// TestService_signals_reconcile pins the second bound: whatever the listener delivers,
// the subscriber's own read of the document every period fires the kinds that advanced,
// and never a kind that did not, so a stream silent without ending costs one period of
// staleness at most.
func TestService_signals_reconcile(t *testing.T) {
	t.Parallel()

	database := databaseFor(t)
	first := newServiceIn(t, database, livefirestore.WithReconcileEvery(2*time.Second))
	second := newServiceIn(t, database)
	firstLog, secondLog := logSnapshots(first), logSnapshots(second)
	explainOnFailure(t, database, firestoreEmulator(t), instance{first, firstLog}, instance{second, secondLog})
	// The first instance's listener is made a silent one: it delivers nothing, so what
	// its subscriptions hear, they hear from the reconcile reads.
	livefirestore.IgnoreListenerSnapshots(first)
	firstFeatures := subscribe(t, first, live.KindFeatures)
	firstTenants := subscribe(t, first, live.KindTenants)
	secondFeatures := subscribe(t, second, live.KindFeatures)
	signal(t, second, live.KindFeatures)
	secondFeatures.waitFor(t, "the second instance's features subscription", 1)
	firstFeatures.waitFor(t, "the first instance's features subscription, through the reconcile read", 1)
	signal(t, second, live.KindFeatures)
	firstFeatures.waitFor(t, "the first instance's features subscription, through the next reconcile read", 2)
	checkCounts(t, "signal counts after the reconcile reads", []expect{atLeast(2), none(), atLeast(1)},
		settled(firstFeatures, firstTenants, secondFeatures), firstLog, secondLog)
}
