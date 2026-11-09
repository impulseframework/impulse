package logger

import (
	"context"
	"net/http"
	"sync/atomic"
)

// SwitchableExporter is an Exporter whose destination can be replaced while the application runs: a device
// starts on the console, moves to the cloud once it can reach it, and comes back to the console when it loses
// that. Middleware, CliRunner and DaemonContext delegate to the exporter current when the request or run
// starts, and that request or run finishes on it even if a swap happens meanwhile. A zero SwitchableExporter
// serves the console exporter until the first Swap.
type SwitchableExporter struct {
	current atomic.Pointer[Exporter]
}

// NewSwitchableExporter returns a SwitchableExporter that starts on the initial exporter. A nil initial
// exporter means the console exporter.
func NewSwitchableExporter(initial Exporter) *SwitchableExporter {
	s := &SwitchableExporter{}
	s.Swap(initial)

	return s
}

// Swap replaces the current exporter and returns the previous one, so the caller can flush or close whatever
// stands behind it; the Exporter interface has no Close, so that is done on the concrete type the caller built.
// A nil next exporter means the console exporter. Swap is safe for concurrent use, and a request or run that
// already started finishes on the exporter it started with.
func (s *SwitchableExporter) Swap(next Exporter) (previous Exporter) {
	if next == nil {
		next = NewConsoleExporter()
	}
	if old := s.current.Swap(&next); old != nil {
		return *old
	}

	return nil
}

// Middleware returns a middleware that hands each request to the exporter current when the request starts.
func (s *SwitchableExporter) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.load().Middleware()(next).ServeHTTP(w, r)
		})
	}
}

// CliRunner returns a function that runs the given function under the exporter current when the run starts.
func (s *SwitchableExporter) CliRunner() func(ctx context.Context, command string, f func(context.Context) error) error {
	return func(ctx context.Context, command string, f func(context.Context) error) error {
		return s.load().CliRunner()(ctx, command, f)
	}
}

// DaemonContext returns a context with a logger from the exporter current at the call.
func (s *SwitchableExporter) DaemonContext(ctx context.Context) context.Context {
	return s.load().DaemonContext(ctx)
}

// load returns the current exporter.
func (s *SwitchableExporter) load() Exporter {
	if e := s.current.Load(); e != nil {
		return *e
	}

	return NewConsoleExporter()
}

var _ Exporter = (*SwitchableExporter)(nil)
