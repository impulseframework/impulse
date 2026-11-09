package access

import (
	"context"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access/internal/signaltest"
)

// policyKind is the kind the tests' adapter publishes and follows, standing
// in for the policy kind the application's channel declares.
const policyKind = "policy"

// policySignal adapts the fake channel the way an application adapts its
// own: announce publishes the policy kind; watch subscribes to it and blocks
// until ctx ends, so the subscription's stop runs when Watch returns.
func policySignal(channel *signaltest.Channel) ChangeSignal {
	return ChangeSignalFunc(
		func(ctx context.Context) error {
			return channel.Signal(ctx, policyKind)
		},
		func(ctx context.Context, onChange func()) error {
			stop, err := channel.Subscribe(policyKind, onChange)
			if err != nil {
				return errors.Wrap(err, "signaltest.Channel.Subscribe()")
			}
			defer stop()
			<-ctx.Done()

			return nil
		},
	)
}

// waitUntil polls check until it passes or the deadline expires.
func waitUntil(t *testing.T, timeout time.Duration, msg string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatal(msg)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func Test_ChangeSignalFunc(t *testing.T) {
	t.Parallel()

	errAnnounce := errors.New("announce refused")
	errWatch := errors.New("subscription lost")

	tests := []struct {
		name        string
		announceErr error // what the announce func answers
		hints       int   // onChange calls the watch func makes before it blocks on ctx
		watchErr    error // what the watch func answers once ctx ends
	}{
		{name: "announce reaches the announce func and watch delivers every onChange", hints: 2},
		{name: "the announce func's error is Announce's", announceErr: errAnnounce, hints: 1},
		{name: "the watch func's error is Watch's", watchErr: errWatch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var announces, hints atomic.Int64
			sig := ChangeSignalFunc(
				func(context.Context) error {
					announces.Add(1)

					return tt.announceErr
				},
				func(ctx context.Context, onChange func()) error {
					for range tt.hints {
						onChange()
					}
					<-ctx.Done()

					return tt.watchErr
				},
			)

			if err := sig.Announce(context.Background()); !errors.Is(err, tt.announceErr) {
				t.Fatalf("Announce() error = %v, want %v", err, tt.announceErr)
			}
			if got := announces.Load(); got != 1 {
				t.Fatalf("announce func ran %d times, want 1", got)
			}

			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() {
				done <- sig.Watch(ctx, func() { hints.Add(1) })
			}()
			waitUntil(t, 5*time.Second, "Watch did not deliver the watch func's onChange calls", func() bool {
				return hints.Load() == int64(tt.hints)
			})
			select {
			case err := <-done:
				t.Fatalf("Watch() returned %v before ctx ended", err)
			case <-time.After(50 * time.Millisecond):
			}

			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, tt.watchErr) {
					t.Fatalf("Watch() error = %v, want %v", err, tt.watchErr)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Watch() did not return after ctx ended")
			}
		})
	}
}

func Test_ChangeSignalFunc_refusesNil(t *testing.T) {
	t.Parallel()

	announce := func(context.Context) error {
		return nil
	}
	watch := func(ctx context.Context, _ func()) error {
		<-ctx.Done()

		return nil
	}

	tests := []struct {
		name     string
		announce func(ctx context.Context) error
		watch    func(ctx context.Context, onChange func()) error
	}{
		{name: "nil announce", watch: watch},
		{name: "nil watch", announce: announce},
		{name: "both nil"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			defer func() {
				if recover() == nil {
					t.Fatal("ChangeSignalFunc() did not panic")
				}
			}()
			ChangeSignalFunc(tt.announce, tt.watch)
		})
	}
}

// Test_snapshotEngine_policyPropagation drives two engines over one store
// through each propagation path: the adapter over one in-memory channel with
// the heartbeat pinned out of the picture, and the heartbeat alone with no
// signal configured.
func Test_snapshotEngine_policyPropagation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		withSignal bool
		heartbeat  time.Duration
	}{
		{name: "through the change signal", withSignal: true, heartbeat: time.Hour},
		{name: "through the heartbeat alone", withSignal: false, heartbeat: 20 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			store := engineFakeStore(t)
			channel := &signaltest.Channel{}
			newEngine := func() *snapshotEngine {
				opts := defaultClientOptions()
				opts.heartbeatInterval = tt.heartbeat
				if tt.withSignal {
					opts.signal = policySignal(channel)
				}
				e := newSnapshotEngine(store, nil, opts)
				t.Cleanup(func() {
					if err := e.close(); err != nil {
						t.Errorf("snapshotEngine.close() error = %v", err)
					}
				})

				return e
			}
			writer, reader := newEngine(), newEngine()

			for name, e := range map[string]*snapshotEngine{"writer": writer, "reader": reader} {
				if got, err := e.checkUserResources(ctx, "erin", tenant1Scope, "List", "widgets"); err != nil || got[0].granted {
					t.Fatalf("%s checkUserResources() = (%v, %v), want widgets denied before the change", name, got, err)
				}
			}
			if tt.withSignal {
				// Both watches must be subscribed before the write, or the
				// announce is lost and the pinned heartbeat would hide it.
				waitUntil(t, 5*time.Second, "the engines never subscribed to the policy kind", func() bool {
					return channel.Subscribers(policyKind) == 2
				})
			}

			grantWidgets(t, store)
			writer.policyChanged()

			if got, err := writer.checkUserResources(ctx, "erin", tenant1Scope, "List", "widgets"); err != nil || !got[0].granted {
				t.Fatalf("writer checkUserResources() = (%v, %v), want granted immediately", got, err)
			}
			waitUntil(t, 5*time.Second, "the change never reached the reader engine", func() bool {
				got, err := reader.checkUserResources(ctx, "erin", tenant1Scope, "List", "widgets")
				if err != nil {
					t.Fatalf("reader checkUserResources() error = %v", err)
				}

				return got[0].granted
			})

			if signals := channel.Signals(); tt.withSignal != slices.Contains(signals, policyKind) {
				t.Fatalf("channel.Signals() = %v, want the policy kind signaled = %v", signals, tt.withSignal)
			}
		})
	}
}
