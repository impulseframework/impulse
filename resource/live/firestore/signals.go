package firestore

import (
	"context"
	"slices"
	"sync"
	"time"

	cloudfirestore "cloud.google.com/go/firestore"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/logger"
	"github.com/impulseframework/impulse/resource/live"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The reopen backoff: how long the subscriber waits before reopening a listener
// Firestore ended, first and at most.
const (
	ReopenFirst = time.Second
	ReopenMost  = 30 * time.Second
)

// The two bounds on a listener's silence. Firestore sends a listener's first snapshot
// as soon as the listen is established, so a listener without one after
// FirstSnapshotWithin is taken as never established and reopened (an established
// stream that has gone silent was seen on the emulator under load, where a fresh
// listener on the same database saw the write within seconds). And whatever a stream
// does, every ReconcileEvery the subscriber reads the document itself and fires the
// kinds that advanced, so a silent stream costs at most that much staleness.
const (
	FirstSnapshotWithin = 30 * time.Second
	ReconcileEvery      = 5 * time.Minute
	reconcileReadWithin = 30 * time.Second
)

// The signals document's fields under each kind.
const (
	signalAtField = "at"
	signalByField = "by"
)

// errSignalLimit is what a Signal fails with when the service's own limit ended it
// (live.SignalTimeout) and not the caller's context.
var errSignalLimit = errors.New("live: the signal was not sent within the live service's own limit")

// signalsState is the signaler's and the subscriber's state: the per-kind write in
// flight and the write that covers the signals made meanwhile, and the one listener
// with its consumers.
type signalsState struct {
	mu sync.Mutex
	// limit is the service's own limit on one Signal, its wait and its write together:
	// live.SignalTimeout outside a test.
	limit time.Duration
	// writes is the coalescing state per kind.
	writes map[live.Kind]*signalWrite
	// onWrite, when set, observes every write of the document before it is sent, with
	// the context it is sent under, and what it returns observes the write's outcome;
	// the package's tests count and hold writes through it.
	onWrite func(ctx context.Context, kind live.Kind) (written func(err error))
	// onSnapshot, when set, observes the times each snapshot of the document carries
	// before the kinds it advances fire; the package's tests explain a count through it.
	onSnapshot func(times map[live.Kind]time.Time)

	// consumers are the subscriptions per kind, by id, run in id order.
	consumers map[live.Kind]map[int]func()
	nextID    int
	// seen is each kind's time as of the last snapshot handled: a kind fires when its
	// time advances past it.
	seen map[live.Kind]time.Time
	// listening says the listener was started; endListener ends the current listener
	// (the test's way of ending it as Firestore would) and the loop reopens it.
	listening   bool
	endListener context.CancelFunc
	reopenFirst time.Duration
	reopenMost  time.Duration
	// firstWithin bounds a listener's first snapshot and reconcileEvery paces the
	// subscriber's own reads of the document; lateReopens counts the listeners reopened
	// for a first snapshot that never came.
	firstWithin    time.Duration
	reconcileEvery time.Duration
	lateReopens    int
	// fireMu serializes the deliveries of the listener and the reconcile reads, so the
	// subscriptions of one snapshot run in order and never beside another's.
	fireMu sync.Mutex
	// stallFirst and ignoreListener are the package's tests' hooks: stallFirst is how
	// many listeners' first snapshots are swallowed as a stream that never speaks would
	// swallow them, and ignoreListener drops every snapshot the listener delivers so the
	// reconcile reads alone wake the subscriptions.
	stallFirst     int
	ignoreListener bool
}

// signalWrite is one kind's coalescing state.
type signalWrite struct {
	// inFlight says the kind's write slot is taken: a write of the kind is being sent,
	// or the write that covers the signals made during the last one is owed and one of
	// their callers is about to make it.
	inFlight bool
	// covering is the write that covers every Signal of the kind made since the write
	// in flight began, nil until one is made. When the write in flight ends, the slot
	// passes to it, and the signals made from then on are covered by the next.
	covering *coveringWrite
}

// coveringWrite is the one write that covers every Signal of a kind made while the
// write before it was in flight. The callers it covers wait for it and one of them
// makes it, inside its own call: the service sends nothing on a goroutine of its own,
// so nothing is left to send once the callers have returned.
type coveringWrite struct {
	// waiting counts the callers the write covers that are still in their call, the one
	// making the write among them. The signals mutex guards it.
	waiting int
	// due says the write before this one has ended, and this one is to be made. The
	// signals mutex guards it.
	due bool
	// turn holds one token from the moment the write is due until a waiting caller takes
	// it and makes the write. A caller whose own context ended under its write puts the
	// token back for the next.
	turn chan struct{}
	// done is closed once the write has been made or refused, and err is its outcome.
	done chan struct{}
	err  error
}

// newSignalsState returns the empty state with the default backoff.
func newSignalsState() signalsState {
	return signalsState{
		writes:         make(map[live.Kind]*signalWrite),
		consumers:      make(map[live.Kind]map[int]func()),
		seen:           make(map[live.Kind]time.Time),
		limit:          live.SignalTimeout,
		reopenFirst:    ReopenFirst,
		reopenMost:     ReopenMost,
		firstWithin:    FirstSnapshotWithin,
		reconcileEvery: ReconcileEvery,
	}
}

// signalsDoc returns the application's signals document.
func (s *Service) signalsDoc() *cloudfirestore.DocumentRef {
	return s.client.Collection(ApplicationCollection).Doc(SignalsDocument)
}

// Signal writes the kind's field of the signals document, {at: the server timestamp,
// by: this instance}, merged so the other kinds' fields stay: one write, which every
// instance's listener observes as the kind's time advancing.
//
// While a write of the kind is in flight a later Signal of the kind makes no write of
// its own at once: it waits for the one write that covers every Signal made meanwhile
// (a signal carries nothing but the fact of a change, so one write that begins after
// the last of those calls covers them all). That write is made when the one in flight
// ends, by one of the callers waiting for it, inside its call and under its context,
// and each of them returns when it has been made, with its outcome: a covering write
// that fails is the failure of every caller that waited for it. So a burst of signals
// costs two writes where the channel answers, and Signal never returns leaving a write
// to be sent behind it: an instance that gets no CPU once its requests have answered
// owes nothing.
//
// Every Signal has the service's own limit (live.SignalTimeout) on top of its caller's
// context, for its wait and its write together: a caller that brings no deadline, as a
// write path that signals under its request's context does, is not held by a channel
// that does not answer. A Signal the limit ends fails with errSignalLimit; a caller
// whose own deadline is shorter keeps it, and fails with its context's error.
//
// No caller's time ends another caller's signal. A caller whose time ends while it
// waits returns, and the write stays with the callers still waiting; one whose time
// ends under the covering write it is making fails alone, and another waiting caller
// makes the write under its own. So a covering write has what is left of its maker's
// time, a channel that does not answer is tried by each waiting caller in turn while
// its own time lasts, and no call makes more than one write or waits past its context
// or the limit. A Signal made once the write in flight has ended is not covered by the
// write that is then due, which may begin before it: it waits for the next. The error
// is returned for the caller to log; the caller's contract stays log and never fail the
// request.
func (s *Service) Signal(ctx context.Context, kind live.Kind) error {
	ctx, cancel := context.WithTimeoutCause(ctx, s.signals.limit, errSignalLimit)
	defer cancel()
	covering := s.beginWrite(kind)
	if covering != nil {
		return s.awaitCovering(ctx, kind, covering)
	}
	// The slot is passed on however the write ends, a panic included: a slot left taken
	// would hold every later signal of the kind for the length of its context.
	defer s.endWrite(kind)

	return s.writeSignal(ctx, kind)
}

// awaitCovering waits, as one of the callers the covering write covers, until the write
// has been made, by another of them or by this one when the turn comes to it, or until
// the caller's time ends: its context, or the service's limit on its signal.
func (s *Service) awaitCovering(ctx context.Context, kind live.Kind, covering *coveringWrite) error {
	select {
	case <-covering.done:
		return covering.err
	case <-covering.turn:
		return s.makeCovering(ctx, kind, covering)
	case <-ctx.Done():
		return s.leaveCovering(ctx, kind, covering)
	}
}

// beginWrite takes the kind's write slot and returns nil: the caller writes. Where the
// slot is taken it returns the write that covers the caller's signal, with the caller
// counted among those waiting for it.
func (s *Service) beginWrite(kind live.Kind) *coveringWrite {
	s.signals.mu.Lock()
	defer s.signals.mu.Unlock()
	write := s.signals.writes[kind]
	if write == nil {
		write = &signalWrite{}
		s.signals.writes[kind] = write
	}
	if !write.inFlight {
		write.inFlight = true

		return nil
	}
	if write.covering == nil {
		write.covering = &coveringWrite{turn: make(chan struct{}, 1), done: make(chan struct{})}
	}
	write.covering.waiting++

	return write.covering
}

// endWrite ends the write the caller made with the kind's slot.
func (s *Service) endWrite(kind live.Kind) {
	s.signals.mu.Lock()
	defer s.signals.mu.Unlock()
	s.passSlot(kind)
}

// passSlot passes the kind's write slot on from a write that has ended: to the write
// that covers the signals made meanwhile, which becomes due and whose turn one of the
// callers waiting for it takes, or to nobody where no caller waits. The signals made
// from now on are covered by the write after it. The caller holds the signals mutex.
func (s *Service) passSlot(kind live.Kind) {
	write := s.signals.writes[kind]
	covering := write.covering
	write.covering = nil
	if covering == nil || covering.waiting == 0 {
		write.inFlight = false

		return
	}
	covering.due = true
	covering.turn <- struct{}{}
}

// makeCovering makes the covering write, as the waiting caller that took its turn and
// under that caller's context, and hands its outcome to every caller waiting for it.
// Where the caller's own time has ended, before the write or under it, the failure
// is the caller's alone and the turn goes back to the callers still waiting. The turn is
// settled however the write ends, a panic included: a turn that was neither used nor
// given back would hold the slot, and every caller waiting, for good.
func (s *Service) makeCovering(ctx context.Context, kind live.Kind, covering *coveringWrite) (err error) {
	made := false
	defer func() {
		if made {
			s.finishCovering(kind, covering, err)

			return
		}
		s.returnTurn(kind, covering)
	}()
	if ctx.Err() != nil {
		return timeEnded(ctx)
	}
	err = s.writeSignal(ctx, kind)
	// A write that failed under a context that has ended is the caller's own failure.
	made = err == nil || ctx.Err() == nil

	return err
}

// finishCovering records the covering write as made, with its outcome, for every caller
// waiting for it, and passes the slot on.
func (s *Service) finishCovering(kind live.Kind, covering *coveringWrite, err error) {
	s.signals.mu.Lock()
	defer s.signals.mu.Unlock()
	covering.err = err
	close(covering.done)
	s.passSlot(kind)
}

// returnTurn gives the covering write's turn back, from a caller that leaves without
// having made the write: to the callers still waiting, or, where none is left, to
// nobody, and the slot passes on.
func (s *Service) returnTurn(kind live.Kind, covering *coveringWrite) {
	s.signals.mu.Lock()
	defer s.signals.mu.Unlock()
	covering.waiting--
	if covering.waiting == 0 {
		s.passSlot(kind)

		return
	}
	covering.turn <- struct{}{}
}

// leaveCovering is a waiting caller whose time ended: the covering write's outcome is
// its own where the write was made meanwhile, and otherwise it leaves the write to the
// callers still waiting and returns what ended its time. Where the write is due and it
// was the last one waiting, nobody is owed the write and the slot passes on.
func (s *Service) leaveCovering(ctx context.Context, kind live.Kind, covering *coveringWrite) error {
	s.signals.mu.Lock()
	defer s.signals.mu.Unlock()
	select {
	case <-covering.done:
		return covering.err
	default:
	}
	covering.waiting--
	if covering.due && covering.waiting == 0 {
		s.passSlot(kind)
	}

	return timeEnded(ctx)
}

// timeEnded is the failure of a Signal whose time ended before the write that covers it
// was made. It carries what ended the time: the caller's context's error, or
// errSignalLimit where the service's own limit came first.
func timeEnded(ctx context.Context) error {
	return errors.Wrap(context.Cause(ctx), "the signal's time ended before the write that covers it was made")
}

// writeSignal is the one write: the kind's field set with merge.
func (s *Service) writeSignal(ctx context.Context, kind live.Kind) error {
	s.signals.mu.Lock()
	onWrite := s.signals.onWrite
	s.signals.mu.Unlock()
	var written func(err error)
	if onWrite != nil {
		written = onWrite(ctx, kind)
	}
	err := s.setSignal(ctx, kind)
	if written != nil {
		written(err)
	}

	return err
}

// setSignal sets the kind's field of the signals document, the other kinds' kept.
func (s *Service) setSignal(ctx context.Context, kind live.Kind) error {
	field := string(kind)
	data := map[string]any{
		field: map[string]any{
			signalAtField: cloudfirestore.ServerTimestamp,
			signalByField: s.instance,
		},
	}
	if _, err := s.signalsDoc().Set(ctx, data, cloudfirestore.Merge(cloudfirestore.FieldPath{field})); err != nil {
		if cause := context.Cause(ctx); cause != nil {
			// The signal's time ended under the write. The client's error does not say
			// whose time it was, the caller's or the service's limit; the cause does.
			return errors.Wrapf(cause, "firestore.DocumentRef.Set(): %v", err)
		}

		return errors.Wrap(err, "firestore.DocumentRef.Set()")
	}

	return nil
}

// Subscribe runs onSignal on every signal of the kind from now on, on the listener's
// goroutine. The first Subscribe opens this instance's one listener on the signals
// document and returns without waiting for it: nothing here waits on the network, so
// a backend that is slow to answer never holds a caller, the signals mutex or a
// consumer's shutdown. The listener's first snapshot is handled like every later one,
// against times not seen yet, so a subscription made before it arrives is woken once
// for each kind the document already holds (the state at the start, which the
// subscription cannot tell from a signal written just after it was made; a wake is a
// nudge to reread, never a fact) and no signal after Subscribe returns is missed; a
// subscription made after the first snapshot hears the signals after it alone. A
// subscription made later joins the running listener. stop ends this subscription
// alone; the listener lives until Close.
func (s *Service) Subscribe(kind live.Kind, onSignal func()) (func(), error) {
	if err := s.startListening(); err != nil {
		return nil, err
	}
	s.signals.mu.Lock()
	defer s.signals.mu.Unlock()
	id := s.signals.nextID
	s.signals.nextID++
	if s.signals.consumers[kind] == nil {
		s.signals.consumers[kind] = make(map[int]func())
	}
	s.signals.consumers[kind][id] = onSignal

	return func() {
		s.signals.mu.Lock()
		defer s.signals.mu.Unlock()
		delete(s.signals.consumers[kind], id)
	}, nil
}

// startListening opens the listener once and hands it to the loop, which reads its
// snapshots; it blocks on nothing, so the first snapshot seeds each kind's time and
// wakes the subscriptions already made, on the loop's goroutine.
func (s *Service) startListening() error {
	s.signals.mu.Lock()
	defer s.signals.mu.Unlock()
	if s.lifetime.Err() != nil {
		return errors.New("live/firestore: the service is closed")
	}
	if s.signals.listening {
		return nil
	}
	snapshots, end := s.openListener()
	s.signals.listening = true
	s.signals.endListener = end
	go s.listen(snapshots, end)
	go s.reconcile()

	return nil
}

// openListener opens a listener on the signals document under its own context, which
// ends it, under the service's lifetime, which ends every listener.
func (s *Service) openListener() (*cloudfirestore.DocumentSnapshotIterator, context.CancelFunc) {
	ctx, end := context.WithCancel(s.lifetime)

	return s.signalsDoc().Snapshots(ctx), end
}

// listen is the listener's loop: every snapshot fires the kinds whose time advanced
// past the last seen, the first snapshot of all against no time seen (so it wakes the
// subscriptions made before it for the kinds the document holds); a listener that
// ends while the service lives is logged and reopened after the backoff, and the
// reopened listener's first snapshot is handled like any other, so every kind whose
// time advanced while the listener was down fires once.
func (s *Service) listen(snapshots *cloudfirestore.DocumentSnapshotIterator, end context.CancelFunc) {
	delay := s.signals.reopenFirst
	for {
		delivered, late, err := s.deliver(snapshots, end)
		snapshots.Stop()
		end()
		if s.lifetime.Err() != nil {
			return
		}
		if delivered {
			delay = s.signals.reopenFirst
		}
		switch {
		case late:
			s.signals.mu.Lock()
			s.signals.lateReopens++
			s.signals.mu.Unlock()
			logger.FromCtx(s.lifetime).Warnf("live: the signals listener delivered no first snapshot within %s and is taken as never established; reopening in %s", s.signals.firstWithin, delay)
		default:
			logger.FromCtx(s.lifetime).Errorf("live: the signals listener ended; reopening in %s: %v", delay, err)
		}
		select {
		case <-s.lifetime.Done():
			return
		case <-time.After(delay):
		}
		delay = min(delay*2, s.signals.reopenMost)
		snapshots, end = s.openListener()
		s.signals.mu.Lock()
		s.signals.endListener = end
		s.signals.mu.Unlock()
	}
}

// deliver fires the kinds each snapshot advances until the listener ends, reporting
// whether it delivered any snapshot, whether it ended for want of a first snapshot
// within the bound (the watchdog ends the listener itself, through end), and why it
// ended otherwise.
func (s *Service) deliver(snapshots *cloudfirestore.DocumentSnapshotIterator, end context.CancelFunc) (delivered, late bool, err error) {
	watchdog := time.AfterFunc(s.signals.firstWithin, end)
	first := true
	for {
		snapshot, err := snapshots.Next()
		if err != nil {
			if first && !watchdog.Stop() {
				return delivered, true, nil
			}

			return delivered, false, errors.Wrap(err, "firestore.DocumentSnapshotIterator.Next()")
		}
		if first && s.swallowFirst() {
			continue
		}
		if first {
			watchdog.Stop()
			first = false
		}
		delivered = true
		if s.listenerIgnored() {
			continue
		}
		s.fire(snapshot)
	}
}

// swallowFirst is the tests' stalled stream: it takes one of the first snapshots the
// tests asked to have swallowed, and reports whether this one was.
func (s *Service) swallowFirst() bool {
	s.signals.mu.Lock()
	defer s.signals.mu.Unlock()
	if s.signals.stallFirst == 0 {
		return false
	}
	s.signals.stallFirst--

	return true
}

// listenerIgnored reports the tests' hook that drops the listener's snapshots.
func (s *Service) listenerIgnored() bool {
	s.signals.mu.Lock()
	defer s.signals.mu.Unlock()

	return s.signals.ignoreListener
}

// reconcile is the subscriber's own reading of the document, every reconcileEvery
// under the service's lifetime: the kinds whose time advanced past the last seen fire
// as a snapshot's would, so a listener that has gone silent without ending (never
// seen on Firestore itself, seen on the emulator under load) costs at most one period
// of staleness. A read that fails is logged and the next period reads again.
func (s *Service) reconcile() {
	ticker := time.NewTicker(s.signals.reconcileEvery)
	defer ticker.Stop()
	for {
		select {
		case <-s.lifetime.Done():
			return
		case <-ticker.C:
		}
		ctx, cancel := context.WithTimeout(s.lifetime, reconcileReadWithin)
		snapshot, err := s.signalsDoc().Get(ctx)
		cancel()
		if err != nil && status.Code(err) != codes.NotFound {
			if s.lifetime.Err() == nil {
				logger.FromCtx(s.lifetime).Errorf("live: the signals reconcile read failed; the next is in %s: %v", s.signals.reconcileEvery, err)
			}

			continue
		}
		s.fire(snapshot)
	}
}

// fire runs the subscriptions of every kind whose time the snapshot advanced past the
// last seen, in subscription order, and notes the new times.
func (s *Service) fire(snapshot *cloudfirestore.DocumentSnapshot) {
	s.signals.fireMu.Lock()
	defer s.signals.fireMu.Unlock()
	times := signalTimes(snapshot)
	s.signals.mu.Lock()
	if s.signals.onSnapshot != nil {
		s.signals.onSnapshot(times)
	}
	kinds := make([]live.Kind, 0, len(times))
	for kind := range times {
		kinds = append(kinds, kind)
	}
	slices.Sort(kinds)
	var wake []func()
	for _, kind := range kinds {
		at := times[kind]
		if !at.After(s.signals.seen[kind]) {
			continue
		}
		s.signals.seen[kind] = at
		ids := make([]int, 0, len(s.signals.consumers[kind]))
		for id := range s.signals.consumers[kind] {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		for _, id := range ids {
			wake = append(wake, s.signals.consumers[kind][id])
		}
	}
	s.signals.mu.Unlock()

	for _, onSignal := range wake {
		onSignal()
	}
}

// signalTimes reads each kind's time out of a snapshot of the signals document: every
// field holding a map with a timestamp at "at". A document that does not exist, or a
// field of another shape, holds no kind.
func signalTimes(snapshot *cloudfirestore.DocumentSnapshot) map[live.Kind]time.Time {
	times := make(map[live.Kind]time.Time)
	if snapshot == nil || !snapshot.Exists() {
		return times
	}
	for field, value := range snapshot.Data() {
		entry, ok := value.(map[string]any)
		if !ok {
			continue
		}
		at, ok := entry[signalAtField].(time.Time)
		if !ok {
			continue
		}
		times[live.Kind(field)] = at
	}

	return times
}

// dropListener ends the current listener as Firestore would end it on its own: the
// loop logs it and reopens after the backoff. The package's tests drive the reopen
// through it.
func (s *Service) dropListener() {
	s.signals.mu.Lock()
	end := s.signals.endListener
	s.signals.mu.Unlock()
	if end != nil {
		end()
	}
}

// stallFirstSnapshots makes the next n listeners' first snapshots go unseen, as a
// stream that never speaks would leave them, so the package's tests watch the first
// snapshot bound reopen the listener.
func (s *Service) stallFirstSnapshots(n int) {
	s.signals.mu.Lock()
	defer s.signals.mu.Unlock()
	s.signals.stallFirst = n
}

// ignoreListenerSnapshots drops every snapshot the listener delivers, so the package's
// tests watch the reconcile reads wake the subscriptions on their own.
func (s *Service) ignoreListenerSnapshots() {
	s.signals.mu.Lock()
	defer s.signals.mu.Unlock()
	s.signals.ignoreListener = true
}

// lateReopenCount is how many listeners were reopened for a first snapshot that never came.
func (s *Service) lateReopenCount() int {
	s.signals.mu.Lock()
	defer s.signals.mu.Unlock()

	return s.signals.lateReopens
}

// signalsIdle reports whether no signal write is in flight or owed for any kind.
func (s *Service) signalsIdle() bool {
	s.signals.mu.Lock()
	defer s.signals.mu.Unlock()
	for _, write := range s.signals.writes {
		if write.inFlight {
			return false
		}
	}

	return true
}

// signalsWaiting is how many callers wait for the kind's covering write that is not yet
// due: the signals made since the write in flight began.
func (s *Service) signalsWaiting(kind live.Kind) int {
	s.signals.mu.Lock()
	defer s.signals.mu.Unlock()
	write := s.signals.writes[kind]
	if write == nil || write.covering == nil {
		return 0
	}

	return write.covering.waiting
}

// observeWrites sets the function every signal write is reported to before it is sent,
// with the context it is sent under; what the function returns, when it returns
// something, is told the write's outcome.
func (s *Service) observeWrites(onWrite func(ctx context.Context, kind live.Kind) (written func(err error))) {
	s.signals.mu.Lock()
	defer s.signals.mu.Unlock()
	s.signals.onWrite = onWrite
}

// observeSnapshots sets the function every snapshot's times are reported to before the
// kinds it advances fire.
func (s *Service) observeSnapshots(onSnapshot func(times map[live.Kind]time.Time)) {
	s.signals.mu.Lock()
	defer s.signals.mu.Unlock()
	s.signals.onSnapshot = onSnapshot
}
