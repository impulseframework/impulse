package access

import (
	"context"
	"testing"
	"time"
)

// TestSnapshotEngine_closeWaitsForAnnouncement holds Close to the announcement a
// write started: a write right before Close, the migrate command's seed, must
// reach the other instances before the application closes the channel the signal
// rides on, so close returns only once the announcement in flight has run.
func TestSnapshotEngine_closeWaitsForAnnouncement(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// slow holds the announcement until the test releases it; otherwise it finishes at once.
		slow bool
	}{
		{name: "an announcement still in flight holds close until it has run", slow: true},
		{name: "an announcement already done holds nothing", slow: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			release := make(chan struct{})
			announced := make(chan struct{})
			signal := ChangeSignalFunc(
				func(ctx context.Context) error {
					if tt.slow {
						select {
						case <-release:
						case <-ctx.Done():
							return ctx.Err()
						}
					}
					close(announced)

					return nil
				},
				func(ctx context.Context, _ func()) error {
					<-ctx.Done()

					return nil
				},
			)
			s := newSnapshotEngine(nil, nil, &clientOptions{signal: signal, onReloadError: func(error) {}})

			s.announceAsync()
			if !tt.slow {
				<-announced
			}

			closed := make(chan struct{})
			go func() {
				_ = s.close()
				close(closed)
			}()

			if tt.slow {
				select {
				case <-closed:
					t.Fatal("close() returned while the announcement was still in flight")
				case <-time.After(50 * time.Millisecond):
				}
				close(release)
			}
			select {
			case <-closed:
			case <-time.After(5 * time.Second):
				t.Fatal("close() did not return after the announcement ran")
			}
			select {
			case <-announced:
			default:
				t.Fatal("close() returned before the announcement ran")
			}
		})
	}
}
