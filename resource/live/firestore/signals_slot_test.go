package firestore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/impulseframework/impulse/resource/live"
)

// slotService is a service with nothing but its signals state: what the write slot's
// steps need. No step of the slot sends anything.
func slotService() *Service {
	return &Service{signals: newSignalsState()}
}

// turnIsOffered reports whether the covering write's turn is there to take, and takes it.
func turnIsOffered(covering *coveringWrite) bool {
	select {
	case <-covering.turn:
		return true
	default:
		return false
	}
}

// panics reports whether f panicked.
func panics(f func()) (panicked bool) {
	defer func() {
		panicked = recover() != nil
	}()
	f()

	return false
}

// isMade reports whether the covering write has been recorded as made.
func isMade(covering *coveringWrite) bool {
	select {
	case <-covering.done:
		return true
	default:
		return false
	}
}

// TestService_writeSlot pins the steps of a kind's write slot, with no write sent: who
// writes, which write covers a signal, when that write is due and whose it is to make,
// and that the slot is free once nobody is owed a write. A slot left taken would hold
// every later signal of the kind for the length of its context.
func TestService_writeSlot(t *testing.T) {
	t.Parallel()

	const kind = live.KindFeatures
	ended, end := context.WithCancel(t.Context())
	end()
	errRefused := errors.New("the store refused the write")

	t.Run("a signal made with the slot free writes, and frees the slot", func(t *testing.T) {
		t.Parallel()

		s := slotService()
		if covering := s.beginWrite(kind); covering != nil {
			t.Fatal("beginWrite() with the slot free returned a covering write to wait for, want the caller to write")
		}
		if s.signalsIdle() {
			t.Error("signalsIdle() = true with a write in flight")
		}
		if covering := s.beginWrite(live.KindTenants); covering != nil {
			t.Error("beginWrite() of another kind waits for the write in flight, want each kind its own slot")
		}
		s.endWrite(live.KindTenants)
		s.endWrite(kind)
		if !s.signalsIdle() {
			t.Error("signalsIdle() = false after the only write ended")
		}
	})

	t.Run("the signals made during a write wait for one covering write, due when that write ends", func(t *testing.T) {
		t.Parallel()

		s := slotService()
		s.beginWrite(kind)
		covering := s.beginWrite(kind)
		if covering == nil {
			t.Fatal("beginWrite() during a write in flight returned nil, want the covering write to wait for")
		}
		if again := s.beginWrite(kind); again != covering {
			t.Error("a second signal during the write got a covering write of its own, want the same one")
		}
		if got := s.signalsWaiting(kind); got != 2 {
			t.Errorf("signalsWaiting() = %d, want 2", got)
		}
		if turnIsOffered(covering) {
			t.Fatal("the covering write's turn is offered while the write before it is in flight")
		}

		s.endWrite(kind)
		if s.signalsIdle() {
			t.Error("signalsIdle() = true with the covering write owed")
		}
		// A signal made now is not covered by a write that may already have begun.
		later := s.beginWrite(kind)
		if later == nil || later == covering {
			t.Errorf("a signal made once the covering write was due joined it (or wrote), want the next covering write")
		}
		if !turnIsOffered(covering) {
			t.Fatal("the covering write's turn is not offered once the write before it ended")
		}
		if turnIsOffered(covering) {
			t.Error("the covering write's turn is offered twice, want one caller to make it")
		}
		if turnIsOffered(later) {
			t.Error("the next covering write's turn is offered while the one before it is not made")
		}
	})

	t.Run("the covering write's outcome is every waiting caller's, and the slot passes on", func(t *testing.T) {
		t.Parallel()

		for _, outcome := range []error{nil, errRefused} {
			s := slotService()
			s.beginWrite(kind)
			covering := s.beginWrite(kind)
			s.beginWrite(kind)
			s.endWrite(kind)
			if !turnIsOffered(covering) {
				t.Fatal("the covering write's turn is not offered")
			}
			if isMade(covering) {
				t.Fatal("the covering write is recorded as made before anybody made it")
			}

			s.finishCovering(kind, covering, outcome)
			if !isMade(covering) {
				t.Fatalf("the covering write is not recorded as made (outcome %v)", outcome)
			}
			if !errors.Is(covering.err, outcome) || (outcome == nil && covering.err != nil) {
				t.Errorf("the covering write's outcome = %v, want %v", covering.err, outcome)
			}
			// A waiting caller whose context ends as the write is made takes the outcome.
			if err := s.leaveCovering(ended, kind, covering); !errors.Is(err, outcome) || (outcome == nil && err != nil) {
				t.Errorf("leaveCovering() once the write was made error = %v, want the write's outcome %v", err, outcome)
			}
			if !s.signalsIdle() {
				t.Errorf("signalsIdle() = false once the covering write was made and no signal was made since (outcome %v)", outcome)
			}
		}
	})

	t.Run("the slot passes from one covering write to the next", func(t *testing.T) {
		t.Parallel()

		s := slotService()
		s.beginWrite(kind)
		covering := s.beginWrite(kind)
		s.endWrite(kind)
		next := s.beginWrite(kind)
		if !turnIsOffered(covering) {
			t.Fatal("the covering write's turn is not offered")
		}

		s.finishCovering(kind, covering, nil)
		if !turnIsOffered(next) {
			t.Error("the next covering write's turn is not offered once the one before it was made")
		}
		if s.signalsIdle() {
			t.Error("signalsIdle() = true with the next covering write owed")
		}
		s.finishCovering(kind, next, nil)
		if !s.signalsIdle() {
			t.Error("signalsIdle() = false once the last covering write was made")
		}
	})

	t.Run("a caller that leaves before the write is due leaves it to the others, and a write nobody waits for is not owed", func(t *testing.T) {
		t.Parallel()

		s := slotService()
		s.beginWrite(kind)
		covering := s.beginWrite(kind)
		s.beginWrite(kind)

		if err := s.leaveCovering(ended, kind, covering); !errors.Is(err, context.Canceled) {
			t.Errorf("leaveCovering() error = %v, want the caller's context's", err)
		}
		if got := s.signalsWaiting(kind); got != 1 {
			t.Errorf("signalsWaiting() = %d after one of two left, want 1", got)
		}
		if err := s.leaveCovering(ended, kind, covering); !errors.Is(err, context.Canceled) {
			t.Errorf("leaveCovering() error = %v, want the caller's context's", err)
		}
		if s.signalsIdle() {
			t.Error("signalsIdle() = true while the first write is in flight")
		}

		s.endWrite(kind)
		if turnIsOffered(covering) {
			t.Error("the covering write's turn is offered with nobody waiting for it")
		}
		if !s.signalsIdle() {
			t.Error("signalsIdle() = false: the slot is held for a covering write nobody waits for")
		}
	})

	t.Run("the last caller to leave a write that is due frees the slot", func(t *testing.T) {
		t.Parallel()

		s := slotService()
		s.beginWrite(kind)
		covering := s.beginWrite(kind)
		s.beginWrite(kind)
		s.endWrite(kind)

		if err := s.leaveCovering(ended, kind, covering); !errors.Is(err, context.Canceled) {
			t.Errorf("leaveCovering() error = %v, want the caller's context's", err)
		}
		if s.signalsIdle() {
			t.Error("signalsIdle() = true with a caller still waiting for the covering write")
		}
		if err := s.leaveCovering(ended, kind, covering); !errors.Is(err, context.Canceled) {
			t.Errorf("leaveCovering() error = %v, want the caller's context's", err)
		}
		if !s.signalsIdle() {
			t.Error("signalsIdle() = false: the slot is held for a covering write whose callers have all left")
		}
	})

	t.Run("the last caller to leave a write that is due passes the slot to the signals made since", func(t *testing.T) {
		t.Parallel()

		s := slotService()
		s.beginWrite(kind)
		covering := s.beginWrite(kind)
		s.endWrite(kind)
		next := s.beginWrite(kind)

		if err := s.leaveCovering(ended, kind, covering); !errors.Is(err, context.Canceled) {
			t.Errorf("leaveCovering() error = %v, want the caller's context's", err)
		}
		if !turnIsOffered(next) {
			t.Error("the next covering write's turn is not offered once the callers of the one before it had all left")
		}
	})

	t.Run("a caller that gives its turn back hands the write to the callers still waiting", func(t *testing.T) {
		t.Parallel()

		s := slotService()
		s.beginWrite(kind)
		covering := s.beginWrite(kind)
		s.beginWrite(kind)
		s.endWrite(kind)
		if !turnIsOffered(covering) {
			t.Fatal("the covering write's turn is not offered")
		}

		s.returnTurn(kind, covering)
		if isMade(covering) {
			t.Error("the covering write is recorded as made by a caller that did not make it")
		}
		if !turnIsOffered(covering) {
			t.Fatal("the turn a caller gave back is not offered to the caller still waiting")
		}
		s.returnTurn(kind, covering)
		if turnIsOffered(covering) {
			t.Error("the turn is offered with nobody left to take it")
		}
		if !s.signalsIdle() {
			t.Error("signalsIdle() = false: the slot is held for a covering write whose callers have all left")
		}
	})

	t.Run("a signal has the service's own limit, two seconds, unless a test sets another", func(t *testing.T) {
		t.Parallel()

		if got := slotService().signals.limit; got != 2*time.Second {
			t.Errorf("the service's limit on a signal = %s, want two seconds (live.SignalTimeout)", got)
		}
	})

	t.Run("a signal that waits for another's write is ended by the service's limit, with the limit's error", func(t *testing.T) {
		t.Parallel()

		const limit = 50 * time.Millisecond
		s := slotService()
		s.signals.limit = limit
		// A write of the kind is in flight, and stays so.
		s.beginWrite(kind)

		asked := time.Now()
		answer := make(chan error, 1)
		go func() {
			answer <- s.Signal(t.Context(), kind)
		}()
		select {
		case err := <-answer:
			if !errors.Is(err, errSignalLimit) {
				t.Errorf("Signal() under a context with no deadline error = %v, want the limit's (%v)", err, errSignalLimit)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("Signal() under a context with no deadline did not return at its limit of %s: the write in flight holds it", limit)
		}
		if took := time.Since(asked); took < limit {
			t.Errorf("Signal() returned after %s, want it to wait out its limit of %s", took, limit)
		}
		if got := s.signalsWaiting(kind); got != 0 {
			t.Errorf("signalsWaiting() = %d after the limit ended the only waiting signal, want 0", got)
		}
		s.endWrite(kind)
		if !s.signalsIdle() {
			t.Error("signalsIdle() = false: the slot is held for a signal the limit ended")
		}
	})

	t.Run("a caller's deadline shorter than the limit ends its signal, with the context's error", func(t *testing.T) {
		t.Parallel()

		const own = 50 * time.Millisecond
		s := slotService()
		s.beginWrite(kind)
		ctx, cancel := context.WithTimeout(t.Context(), own)
		defer cancel()

		asked := time.Now()
		err := s.Signal(ctx, kind)
		if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, errSignalLimit) {
			t.Errorf("Signal() error = %v, want the caller's own deadline's and not the limit's", err)
		}
		if took := time.Since(asked); took >= live.SignalTimeout {
			t.Errorf("Signal() returned after %s, want the caller's own %s, well short of the limit of %s", took, own, live.SignalTimeout)
		}
	})

	t.Run("a write that panics frees the slot", func(t *testing.T) {
		t.Parallel()

		s := slotService()
		s.observeWrites(func(context.Context, live.Kind) func(error) {
			panic("the write panicked")
		})
		if !panics(func() {
			_ = s.Signal(t.Context(), kind)
		}) {
			t.Fatal("Signal() did not panic with the write")
		}
		if !s.signalsIdle() {
			t.Error("signalsIdle() = false after a write that panicked: every later signal of the kind would wait for a slot nobody holds")
		}
	})

	t.Run("a covering write that panics gives its turn back", func(t *testing.T) {
		t.Parallel()

		s := slotService()
		s.beginWrite(kind)
		covering := s.beginWrite(kind)
		s.beginWrite(kind)
		s.endWrite(kind)
		if !turnIsOffered(covering) {
			t.Fatal("the covering write's turn is not offered")
		}
		s.observeWrites(func(context.Context, live.Kind) func(error) {
			panic("the write panicked")
		})

		if !panics(func() {
			_ = s.makeCovering(t.Context(), kind, covering)
		}) {
			t.Fatal("makeCovering() did not panic with the write")
		}
		if isMade(covering) {
			t.Error("the covering write is recorded as made, though it panicked")
		}
		if !turnIsOffered(covering) {
			t.Error("the turn of a covering write that panicked is not offered to the caller still waiting")
		}
	})

	t.Run("a caller whose context has ended makes no write with its turn", func(t *testing.T) {
		t.Parallel()

		s := slotService()
		var tried bool
		s.observeWrites(func(context.Context, live.Kind) func(error) {
			tried = true

			return nil
		})
		s.beginWrite(kind)
		covering := s.beginWrite(kind)
		s.beginWrite(kind)
		s.endWrite(kind)
		if !turnIsOffered(covering) {
			t.Fatal("the covering write's turn is not offered")
		}

		// The service has no client, so a write that was tried panics once it is told of.
		err := func() (err error) {
			defer func() {
				if recover() != nil {
					err = errors.New("the write was tried on a service with no client")
				}
			}()

			return s.makeCovering(ended, kind, covering)
		}()
		if tried {
			t.Fatal("a write was tried under a context that had ended")
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("makeCovering() under a context that has ended error = %v, want the context's", err)
		}
		if isMade(covering) {
			t.Error("the covering write is recorded as made, want it left to the caller still waiting")
		}
		if !turnIsOffered(covering) {
			t.Error("the turn is not offered to the caller still waiting")
		}
	})

	t.Run("a signal's context carries the time its limit ends it, its deadline unless the caller's own is sooner", func(t *testing.T) {
		t.Parallel()

		// sentUnder returns the context a signal's write is sent under. The service has
		// no client, so the write is stopped where it is told of.
		sentUnder := func(t *testing.T, ctx context.Context) context.Context {
			t.Helper()

			s := slotService()
			var sent context.Context
			s.observeWrites(func(ctx context.Context, _ live.Kind) func(error) {
				sent = ctx
				panic("the write is stopped here")
			})
			if !panics(func() {
				_ = s.Signal(ctx, kind)
			}) {
				t.Fatal("Signal() sent a write on a service with no client")
			}

			return sent
		}

		before := time.Now()
		sent := sentUnder(t, t.Context())
		limitAt, ok := sent.Value(limitKey{}).(time.Time)
		if !ok {
			t.Fatal("the context of a signal's write does not carry the time the limit ends it")
		}
		if limitAt.Before(before.Add(live.SignalTimeout)) || limitAt.After(time.Now().Add(live.SignalTimeout)) {
			t.Errorf("the limit ends the signal at %s, want %s after it was made (between %s and %s)", limitAt, live.SignalTimeout, before.Add(live.SignalTimeout), time.Now().Add(live.SignalTimeout))
		}
		if deadline, ok := sent.Deadline(); !ok || !deadline.Equal(limitAt) {
			t.Errorf("the deadline of a signal whose caller brings none = %s (%t), want the limit's (%s)", deadline, ok, limitAt)
		}

		later, cancelLater := context.WithTimeout(t.Context(), 2*live.SignalTimeout)
		defer cancelLater()
		sent = sentUnder(t, later)
		limitAt, _ = sent.Value(limitKey{}).(time.Time)
		if deadline, ok := sent.Deadline(); !ok || !deadline.Equal(limitAt) {
			t.Errorf("the deadline of a signal whose caller's own is later than the limit = %s (%t), want the limit's (%s)", deadline, ok, limitAt)
		}

		sooner, cancelSooner := context.WithTimeout(t.Context(), live.SignalTimeout/2)
		defer cancelSooner()
		own, _ := sooner.Deadline()
		sent = sentUnder(t, sooner)
		limitAt, _ = sent.Value(limitKey{}).(time.Time)
		if deadline, ok := sent.Deadline(); !ok || !deadline.Equal(own) || !limitAt.After(own) {
			t.Errorf("the deadline of a signal whose caller's own is sooner = %s (%t) with the limit at %s, want the caller's (%s) and the limit after it", deadline, ok, limitAt, own)
		}
	})
}

// Test_whatEnded pins what ends a signal's time and when: its context's end, with the
// context's cause, and before that its deadline passing by the clock, which the client
// acts on without waiting for the context. A deadline that has passed is told for the
// service's limit or the caller's own by the time the limit ends the signal, which the
// signal's context carries.
func Test_whatEnded(t *testing.T) {
	t.Parallel()

	errGone := errors.New("the request is gone")
	passed := time.Now().Add(-time.Second)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	withCause, cancelCause := context.WithCancelCause(t.Context())
	cancelCause(errGone)
	// The parallel cases run once this function has returned: the contexts are released
	// when they have.
	ahead, cancelAhead := context.WithTimeout(t.Context(), time.Hour)
	t.Cleanup(cancelAhead)
	limited, cancelLimited := context.WithDeadlineCause(context.WithValue(t.Context(), limitKey{}, passed), passed, errSignalLimit)
	t.Cleanup(cancelLimited)

	tests := []struct {
		name string
		ctx  context.Context
		want error
	}{
		{name: "a context with no deadline, not ended", ctx: t.Context()},
		{name: "a deadline still ahead", ctx: ahead},
		{name: "a context that was canceled", ctx: canceled, want: context.Canceled},
		{name: "a context that ended with a cause", ctx: withCause, want: errGone},
		{name: "a context the limit ended", ctx: limited, want: errSignalLimit},
		{name: "a deadline that has passed, the context not ended and no limit known", ctx: overdue{Context: t.Context(), deadline: passed}, want: context.DeadlineExceeded},
		{
			name: "a deadline that has passed and is the limit's, the context not ended",
			ctx:  overdue{Context: context.WithValue(t.Context(), limitKey{}, passed), deadline: passed},
			want: errSignalLimit,
		},
		{
			name: "a deadline that has passed and is sooner than the limit, the context not ended",
			ctx:  overdue{Context: context.WithValue(t.Context(), limitKey{}, passed.Add(time.Minute)), deadline: passed},
			want: context.DeadlineExceeded,
		},
		{
			name: "a deadline that has passed under a context that was canceled: the context says what ended it",
			ctx:  overdue{Context: canceled, deadline: passed},
			want: context.Canceled,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := whatEnded(tt.ctx)
			if tt.want == nil {
				if got != nil {
					t.Errorf("whatEnded() = %v, want nil: the time lasts", got)
				}

				return
			}
			if !errors.Is(got, tt.want) {
				t.Errorf("whatEnded() = %v, want %v", got, tt.want)
			}
			if !errors.Is(tt.want, errSignalLimit) && errors.Is(got, errSignalLimit) {
				t.Errorf("whatEnded() = %v, want it not to name the limit", got)
			}
		})
	}
}
