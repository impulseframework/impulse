package access

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/impulseframework/impulse/accesstypes"
)

// announcedSignal is a change signal a test watches the engine announce on. It counts
// the announcements begun and ended, keeps the context of each, and makes one do what
// the test asks: fail with an error, take some time, wait until the test lets it go, or
// wait for its context to end as a channel that does not answer does.
type announcedSignal struct {
	begun atomic.Int64
	ended atomic.Int64

	mu       sync.Mutex
	err      error
	takes    time.Duration
	hold     chan struct{}
	hangs    bool
	entered  chan struct{}
	contexts []context.Context
}

func newAnnouncedSignal() *announcedSignal {
	return &announcedSignal{entered: make(chan struct{}, 1024)}
}

func (s *announcedSignal) Announce(ctx context.Context) error {
	s.begun.Add(1)
	defer s.ended.Add(1)

	s.mu.Lock()
	err, takes, hold, hangs := s.err, s.takes, s.hold, s.hangs
	s.contexts = append(s.contexts, ctx)
	s.mu.Unlock()

	switch {
	case hangs:
		<-ctx.Done()

		return fmt.Errorf("the channel did not answer: %w", ctx.Err())
	case hold != nil:
		s.entered <- struct{}{}
		select {
		case <-hold:
		case <-ctx.Done():
			return fmt.Errorf("the announcement was cut short: %w", ctx.Err())
		}
	case takes > 0:
		time.Sleep(takes)
	}

	return err
}

func (s *announcedSignal) Watch(ctx context.Context, _ func()) error {
	<-ctx.Done()

	return nil
}

// holdAnnouncements holds the announcements that begin from now, each making itself
// known on entered as it begins, until release is called.
func (s *announcedSignal) holdAnnouncements() (entered <-chan struct{}, release func()) {
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

// lastContext returns the context of the signal's latest announcement.
func (s *announcedSignal) lastContext(t *testing.T) context.Context {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.contexts) == 0 {
		t.Fatal("nothing was announced")
	}

	return s.contexts[len(s.contexts)-1]
}

// announcingClient is a Client over the engine's fake store that announces on the
// signal, with the errors its reload-error hook receives collected.
func announcingClient(t *testing.T, signal ChangeSignal) (client *Client, reported func() []error) {
	t.Helper()

	return announcingClientOver(t, engineFakeStore(t), signal)
}

// announcingClientOver is a Client over store that announces on the signal, with the
// errors its reload-error hook receives collected.
func announcingClientOver(t *testing.T, store Store, signal ChangeSignal) (client *Client, reported func() []error) {
	t.Helper()

	var (
		mu       sync.Mutex
		received []error
	)
	client, err := New(store, WithChangeSignal(signal), WithReloadErrorHandler(func(err error) {
		mu.Lock()
		defer mu.Unlock()
		received = append(received, err)
	}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	return client, func() []error {
		mu.Lock()
		defer mu.Unlock()

		return slices.Clone(received)
	}
}

// policyWrites are the two ways a policy write reaches the engine: a write of the user
// manager's own, and one an application committed itself and told the Client of.
func policyWrites() []struct {
	name  string
	write func(ctx context.Context, client *Client) error
} {
	return []struct {
		name  string
		write func(ctx context.Context, client *Client) error
	}{
		{
			name: "a write of the user manager's",
			write: func(ctx context.Context, client *Client) error {
				return client.UserManager().AddRolePermissionResources(ctx, tenant1Policy, "Editor", "List", "widgets")
			},
		},
		{
			name: "a write the application committed and told the Client of",
			write: func(_ context.Context, client *Client) error {
				client.PolicyChanged()

				return nil
			},
		},
	}
}

// TestClient_policyWrite_announcesBeforeItReturns pins that a policy write hands its
// announcement to the change signal before it returns: while the signal holds the
// announcement the write is committed and has not returned, and it returns once the
// signal has taken it. The announcement runs under the engine's own timeout, not the
// caller's context.
func TestClient_policyWrite_announcesBeforeItReturns(t *testing.T) {
	t.Parallel()

	for _, tt := range policyWrites() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			signal := newAnnouncedSignal()
			client, reported := announcingClient(t, signal)
			entered, release := signal.holdAnnouncements()

			type callKey struct{}
			ctx := context.WithValue(t.Context(), callKey{}, "the caller")
			asked := time.Now()
			written := make(chan error, 1)
			go func() {
				written <- tt.write(ctx, client)
			}()
			select {
			case <-entered:
			case <-time.After(10 * time.Second):
				t.Fatal("the write announced nothing")
			}
			select {
			case err := <-written:
				t.Fatalf("the write returned (%v) while its announcement was still being handed over", err)
			case <-time.After(100 * time.Millisecond):
			}

			announced := signal.lastContext(t)
			if announced.Value(callKey{}) != nil {
				t.Error("the announcement ran under the caller's context")
			}
			// The announcement began after the write was asked for, so its deadline is no
			// sooner than the timeout after that moment, and little later.
			if deadline, ok := announced.Deadline(); !ok || deadline.Sub(asked) < defaultAnnounceTimeout || deadline.Sub(asked) > defaultAnnounceTimeout+time.Second {
				t.Errorf("the announcement's deadline = %v (set %v), %s after the write was asked for, want the engine's own %s", deadline, ok, deadline.Sub(asked), defaultAnnounceTimeout)
			}

			release()
			select {
			case err := <-written:
				if err != nil {
					t.Errorf("the write error = %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("the write did not return once its announcement was handed over")
			}
			if begun, ended := signal.begun.Load(), signal.ended.Load(); begun != 1 || ended != 1 {
				t.Errorf("announcements begun = %d, ended = %d, want 1 and 1", begun, ended)
			}
			if got := reported(); len(got) != 0 {
				t.Errorf("errors reported = %v, want none", got)
			}
		})
	}
}

// TestClient_policyWrite_nothingIsLeftPublishing pins that no announcement of the
// engine's outlives the write it follows: the moment a write has returned, every call
// the engine made to the signal has returned, though each takes a while. An announcement
// made on a goroutine of its own would still be running, or not yet begun.
func TestClient_policyWrite_nothingIsLeftPublishing(t *testing.T) {
	t.Parallel()

	for _, tt := range policyWrites() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			signal := newAnnouncedSignal()
			signal.takes = 20 * time.Millisecond
			client, _ := announcingClient(t, signal)

			if err := tt.write(t.Context(), client); err != nil {
				t.Fatalf("the write error = %v", err)
			}
			if begun, ended := signal.begun.Load(), signal.ended.Load(); begun != 1 || ended != 1 {
				t.Errorf("as the write returned: announcements begun = %d, ended = %d, want 1 and 1", begun, ended)
			}

			// Writes made together each wait for their own announcement.
			const writers = 8
			var wg sync.WaitGroup
			for range writers {
				wg.Go(func() {
					client.PolicyChanged()
				})
			}
			wg.Wait()
			if begun, ended := signal.begun.Load(), signal.ended.Load(); begun != 1+writers || ended != 1+writers {
				t.Errorf("as %d writes made together returned: announcements begun = %d, ended = %d, want %d and %d", writers, begun, ended, 1+writers, 1+writers)
			}
		})
	}
}

// TestClient_policyWrite_failedAnnouncement pins an announcement that fails or runs
// out of time: the write, which is committed, still succeeds and is seen by the next
// check on this instance, and the failure reaches the reload-error hook once, named as
// the announcement's.
func TestClient_policyWrite_failedAnnouncement(t *testing.T) {
	t.Parallel()

	errRefused := errors.New("the channel refused the signal")
	tests := []struct {
		name    string
		prepare func(signal *announcedSignal, engine *snapshotEngine)
		wantErr error
	}{
		{
			name: "the signal refuses the announcement",
			prepare: func(signal *announcedSignal, _ *snapshotEngine) {
				signal.err = errRefused
			},
			wantErr: errRefused,
		},
		{
			name: "the signal does not answer and the announcement's time runs out",
			prepare: func(signal *announcedSignal, engine *snapshotEngine) {
				signal.hangs = true
				engine.announceTimeout = 20 * time.Millisecond
			},
			wantErr: context.DeadlineExceeded,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			signal := newAnnouncedSignal()
			client, reported := announcingClient(t, signal)
			tt.prepare(signal, client.snapEngine)
			ctx := t.Context()
			listsWidgets := func() bool {
				t.Helper()
				decided, err := client.snapEngine.checkUserResources(ctx, accesstypes.NewEnvironment(), "erin", tenant1Scope, "List", "widgets")
				if err != nil {
					t.Fatalf("checkUserResources() error = %v", err)
				}

				return decided[0].granted
			}
			// The snapshot is loaded before the write, so the check after the write sees
			// it only because the write told the engine before it announced.
			if listsWidgets() {
				t.Fatal("widgets granted before the write")
			}

			written := make(chan error, 1)
			go func() {
				written <- client.UserManager().AddRolePermissionResources(ctx, tenant1Policy, "Editor", "List", "widgets")
			}()
			select {
			case err := <-written:
				if err != nil {
					t.Fatalf("AddRolePermissionResources() error = %v, want the write to succeed whatever became of its announcement", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("AddRolePermissionResources() did not return: the announcement was not held to its timeout")
			}

			got := reported()
			if len(got) != 1 || !errors.Is(got[0], tt.wantErr) {
				t.Fatalf("errors reported = %v, want the one failure of the announcement (%v)", got, tt.wantErr)
			}
			if want := "ChangeSignal.Announce()"; !strings.Contains(got[0].Error(), want) {
				t.Errorf("the error reported = %q, want it to name %s", got[0], want)
			}
			if begun, ended := signal.begun.Load(), signal.ended.Load(); begun != 1 || ended != 1 {
				t.Errorf("announcements begun = %d, ended = %d, want 1 and 1", begun, ended)
			}

			// The write is committed, and this instance's next check sees it.
			if !listsWidgets() {
				t.Error("the check after the write did not see it, though only its announcement failed")
			}
		})
	}
}
