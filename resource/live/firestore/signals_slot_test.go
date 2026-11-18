package firestore

import (
	"context"
	"errors"
	"testing"

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
}
