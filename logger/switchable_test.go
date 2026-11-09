package logger

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
)

// stubExporter is an Exporter that counts what it served, so a test sees which exporter a request or run started
// and finished on.
type stubExporter struct {
	started      atomic.Int32
	finished     atomic.Int32
	runs         atomic.Int32
	finishedRuns atomic.Int32
	daemons      atomic.Int32
}

func (s *stubExporter) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.started.Add(1)
			next.ServeHTTP(w, r)
			s.finished.Add(1)
		})
	}
}

func (s *stubExporter) CliRunner() func(ctx context.Context, command string, f func(context.Context) error) error {
	return func(ctx context.Context, _ string, f func(context.Context) error) error {
		s.runs.Add(1)
		err := f(ctx)
		s.finishedRuns.Add(1)

		return err
	}
}

func (s *stubExporter) DaemonContext(ctx context.Context) context.Context {
	s.daemons.Add(1)

	return ctx
}

func TestSwitchableExporter_Swap(t *testing.T) {
	t.Parallel()

	console := NewConsoleExporter()
	cloud := &stubExporter{}
	tests := []struct {
		name         string
		initial      Exporter
		next         Exporter
		wantPrevious Exporter // nil: a console exporter the switchable exporter made itself
		wantCurrent  Exporter // nil: the same
	}{
		{
			name:         "console to cloud",
			initial:      console,
			next:         cloud,
			wantPrevious: console,
			wantCurrent:  cloud,
		},
		{
			name:         "cloud back to console",
			initial:      cloud,
			next:         console,
			wantPrevious: cloud,
			wantCurrent:  console,
		},
		{
			name:        "nil initial is the console",
			next:        cloud,
			wantCurrent: cloud,
		},
		{
			name:         "nil next is the console",
			initial:      cloud,
			wantPrevious: cloud,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := NewSwitchableExporter(tt.initial)
			if previous := s.Swap(tt.next); !sameExporter(previous, tt.wantPrevious) {
				t.Errorf("Swap() previous = %T, want %T", previous, tt.wantPrevious)
			}
			if current := s.load(); !sameExporter(current, tt.wantCurrent) {
				t.Errorf("current exporter = %T, want %T", current, tt.wantCurrent)
			}
		})
	}
}

// sameExporter reports whether got is want, or a console exporter when want is nil.
func sameExporter(got, want Exporter) bool {
	if want == nil {
		_, ok := got.(*ConsoleExporter)

		return ok
	}

	return got == want
}

func TestSwitchableExporter_zeroValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
	}{
		{
			name: "serves the console until the first swap",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := &SwitchableExporter{}
			if _, ok := s.load().(*ConsoleExporter); !ok {
				t.Errorf("zero SwitchableExporter serves %T, want *ConsoleExporter", s.load())
			}
			if previous := s.Swap(&stubExporter{}); previous != nil {
				t.Errorf("Swap() on a zero SwitchableExporter returned %T, want nil", previous)
			}
		})
	}
}

func TestSwitchableExporter_Middleware(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		swapBefore        bool // swap to the second exporter before the request starts
		swapDuring        bool // swap to the second exporter while the request is in flight
		wantFirstFinished int32
		wantSecondStarted int32
		wantFirstAfter    int32 // first.finished after one more request
		wantSecondAfter   int32 // second.started after one more request
	}{
		{
			name:              "a request goes to the exporter current at its start",
			wantFirstFinished: 1,
			wantFirstAfter:    2,
		},
		{
			name:              "a request after the swap goes to the second",
			swapBefore:        true,
			wantSecondStarted: 1,
			wantSecondAfter:   2,
		},
		{
			name:              "a swap during an in-flight request leaves it on the first",
			swapDuring:        true,
			wantFirstFinished: 1,
			wantFirstAfter:    1,
			wantSecondAfter:   1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			first, second := &stubExporter{}, &stubExporter{}
			s := NewSwitchableExporter(first)
			if tt.swapBefore {
				s.Swap(second)
			}

			entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			waiting := s.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				close(entered)
				<-release
				w.WriteHeader(http.StatusOK)
			}))
			go func() {
				r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
				waiting.ServeHTTP(httptest.NewRecorder(), r)
				close(done)
			}()
			<-entered
			if tt.swapDuring {
				s.Swap(second)
			}
			close(release)
			<-done

			if got := first.finished.Load(); got != tt.wantFirstFinished {
				t.Errorf("first.finished = %d, want %d", got, tt.wantFirstFinished)
			}
			if got := second.started.Load(); got != tt.wantSecondStarted {
				t.Errorf("second.started = %d, want %d", got, tt.wantSecondStarted)
			}

			plain := s.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			plain.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody))

			if got := first.finished.Load(); got != tt.wantFirstAfter {
				t.Errorf("first.finished after one more request = %d, want %d", got, tt.wantFirstAfter)
			}
			if got := second.started.Load(); got != tt.wantSecondAfter {
				t.Errorf("second.started after one more request = %d, want %d", got, tt.wantSecondAfter)
			}
		})
	}
}

func TestSwitchableExporter_CliRunner(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		swapBefore     bool // swap to the second exporter before the run starts
		swapDuring     bool // swap to the second exporter inside the run
		wantFirstRuns  int32
		wantSecondRuns int32
	}{
		{
			name:          "a run goes to the exporter current at its start",
			wantFirstRuns: 1,
		},
		{
			name:           "a run after the swap goes to the second",
			swapBefore:     true,
			wantSecondRuns: 1,
		},
		{
			name:          "a swap during the run leaves it on the first",
			swapDuring:    true,
			wantFirstRuns: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			first, second := &stubExporter{}, &stubExporter{}
			s := NewSwitchableExporter(first)
			if tt.swapBefore {
				s.Swap(second)
			}
			err := s.CliRunner()(t.Context(), "command", func(_ context.Context) error {
				if tt.swapDuring {
					s.Swap(second)
				}

				return nil
			})
			if err != nil {
				t.Fatalf("SwitchableExporter.CliRunner() error = %v", err)
			}
			if got := first.finishedRuns.Load(); got != tt.wantFirstRuns {
				t.Errorf("first.finishedRuns = %d, want %d", got, tt.wantFirstRuns)
			}
			if got := second.runs.Load(); got != tt.wantSecondRuns {
				t.Errorf("second.runs = %d, want %d", got, tt.wantSecondRuns)
			}
		})
	}
}

func TestSwitchableExporter_DaemonContext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		swapBefore  bool
		wantFirst   int32
		wantSecond  int32
		wantContext bool
	}{
		{
			name:      "the exporter current at the call",
			wantFirst: 1,
		},
		{
			name:       "the second after a swap",
			swapBefore: true,
			wantSecond: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			first, second := &stubExporter{}, &stubExporter{}
			s := NewSwitchableExporter(first)
			if tt.swapBefore {
				s.Swap(second)
			}
			if got := s.DaemonContext(t.Context()); got == nil {
				t.Fatal("SwitchableExporter.DaemonContext() returned nil")
			}
			if got := first.daemons.Load(); got != tt.wantFirst {
				t.Errorf("first.daemons = %d, want %d", got, tt.wantFirst)
			}
			if got := second.daemons.Load(); got != tt.wantSecond {
				t.Errorf("second.daemons = %d, want %d", got, tt.wantSecond)
			}
		})
	}
}

// TestSwitchableExporter_Swap_concurrent is deliberately not a table: its one assertion is over the whole set of
// concurrent swaps, that every exporter handed in comes back exactly once, as a previous or as the current one.
func TestSwitchableExporter_Swap_concurrent(t *testing.T) {
	t.Parallel()

	const swaps = 64
	initial := &stubExporter{}
	s := NewSwitchableExporter(initial)
	previous := make(chan Exporter, swaps)
	var wg sync.WaitGroup
	for range swaps {
		wg.Add(1)
		go func() {
			defer wg.Done()
			previous <- s.Swap(&stubExporter{})
		}()
	}
	wg.Wait()
	close(previous)

	seen := map[Exporter]int{s.load(): 1}
	for e := range previous {
		seen[e]++
	}
	if len(seen) != swaps+1 {
		t.Errorf("Swap() handed back %d distinct exporters, want %d", len(seen), swaps+1)
	}
	for e, n := range seen {
		if n != 1 {
			t.Errorf("exporter %p came back %d times, want once", e, n)
		}
	}
	if seen[initial] != 1 {
		t.Error("the initial exporter never came back")
	}
}
