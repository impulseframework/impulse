package logger

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/go-playground/errors/v5"
)

// NewRequestLogger returns a middleware that logs the request and injects a Logger into
// the context. This Logger can be used during the life of the request, and all logs
// generated will be correlated to the request log.
//
// DefaultPolicy and PolicyByPrefix set the Policy each request starts with. With neither, a request starts with
// the exporter's own default and the behavior is unchanged.
//
// If not configured, request logs are sent to stderr by default.
func NewRequestLogger(e Exporter, opts ...RequestLoggerOption) func(http.Handler) http.Handler {
	o := newRequestLoggerOptions(opts)
	middleware := e.Middleware()
	if !o.hasDefault && len(o.prefixes) == 0 {
		return middleware
	}

	return func(next http.Handler) http.Handler {
		return middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if p, ok := o.startingPolicy(r.URL.Path); ok {
				FromReq(r).SetPolicy(p)
			}
			next.ServeHTTP(w, r)
		}))
	}
}

// NewCliLogger returns a function that executes the given function and creates a top-level parent log.
// The provided command string is used to identify the CLI execution in the logs.
//
// DefaultPolicy sets the Policy each run starts with; a run has no path, so PolicyByPrefix does not apply.
// Without it, a run starts with the exporter's own default: Always, unless LogAll(false) or
// NewAWSExporter(false) made it OnEvent. Under OnEvent the run's parent entry is written when a line attached
// or the function returned an error.
func NewCliLogger(e Exporter, opts ...RequestLoggerOption) func(ctx context.Context, command string, f func(context.Context) error) error {
	o := newRequestLoggerOptions(opts)
	run := e.CliRunner()
	if !o.hasDefault {
		return run
	}

	return func(ctx context.Context, command string, f func(context.Context) error) error {
		return run(ctx, command, func(ctx context.Context) error {
			FromCtx(ctx).SetPolicy(o.defaultPolicy)

			return f(ctx)
		})
	}
}

// Exporter is the interface for implementing a middleware to export logs to some destination
type Exporter interface {
	Middleware() func(http.Handler) http.Handler
	CliRunner() func(ctx context.Context, command string, f func(context.Context) error) error
	DaemonContext(ctx context.Context) context.Context
}

// RequestLoggerOption configures NewRequestLogger and NewCliLogger.
type RequestLoggerOption func(*requestLoggerOptions)

// DefaultPolicy sets the policy every request or run starts with: the application default. It overrides the
// exporter's own default, which is Always unless the exporter was built with LogAll(false) or
// NewAWSExporter(false), where it is OnEvent.
func DefaultPolicy(p Policy) RequestLoggerOption {
	return func(o *requestLoggerOptions) {
		o.defaultPolicy = p
		o.hasDefault = true
	}
}

// PolicyByPrefix sets the policy a request starts with from its path: the longest prefix that matches wins, and
// its policy is used in place of the default. It is how routes an application mounts by hand get theirs. A run
// has no path, so NewCliLogger does not apply it.
func PolicyByPrefix(prefixes map[string]Policy) RequestLoggerOption {
	return func(o *requestLoggerOptions) {
		for prefix, policy := range prefixes {
			o.prefixes = append(o.prefixes, prefixPolicy{prefix: prefix, policy: policy})
		}
		slices.SortFunc(o.prefixes, func(a, b prefixPolicy) int {
			if len(a.prefix) != len(b.prefix) {
				return len(b.prefix) - len(a.prefix)
			}

			return strings.Compare(a.prefix, b.prefix)
		})
	}
}

// requestLoggerOptions is what the options set: the policy a request or run starts with.
type requestLoggerOptions struct {
	defaultPolicy Policy
	hasDefault    bool
	prefixes      []prefixPolicy // longest prefix first
}

// newRequestLoggerOptions applies the options.
func newRequestLoggerOptions(opts []RequestLoggerOption) *requestLoggerOptions {
	o := &requestLoggerOptions{}
	for _, opt := range opts {
		opt(o)
	}

	return o
}

// startingPolicy returns the policy a request at the path starts with, and whether the options name one: the
// longest matching prefix first, then the default.
func (o *requestLoggerOptions) startingPolicy(path string) (Policy, bool) {
	for _, row := range o.prefixes {
		if strings.HasPrefix(path, row.prefix) {
			return row.policy, true
		}
	}

	return o.defaultPolicy, o.hasDefault
}

// prefixPolicy is one row of the prefix table.
type prefixPolicy struct {
	prefix string
	policy Policy
}

func requestSize(length string) int64 {
	l, err := strconv.Atoi(length)
	if err != nil {
		return 0
	}

	return int64(l)
}

func newResponseRecorder(w http.ResponseWriter) responseRecorder {
	_, isFlusher := w.(http.Flusher)
	_, isHijacker := w.(http.Hijacker)

	switch {
	case isFlusher && isHijacker:
		return &recorderFlusherHijacker{
			recorder: recorder{ResponseWriter: w},
		}
	case isFlusher:
		return &recorderFlusher{
			recorder: recorder{ResponseWriter: w},
		}
	case isHijacker:
		return &recorderHijacker{
			recorder: recorder{ResponseWriter: w},
		}
	default:
		return &recorder{
			ResponseWriter: w,
		}
	}
}

type responseRecorder interface {
	http.ResponseWriter
	Status() int
	WriteHeader(status int)
	Write(b []byte) (int, error)
	Length() int64
}

type recorder struct {
	http.ResponseWriter
	status int
	length int64
}

func (r *recorder) Status() int {
	if r.status == 0 {
		return http.StatusOK
	}

	return r.status
}

func (r *recorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *recorder) Write(b []byte) (int, error) {
	n, err := r.ResponseWriter.Write(b)
	r.length += int64(n)
	if err != nil {
		return n, errors.Wrap(err, "http.ResponseWriter.Write()")
	}

	return n, nil
}

func (r *recorder) Length() int64 {
	return r.length
}

type recorderFlusher struct {
	recorder
}

func (r *recorderFlusher) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

type recorderHijacker struct {
	recorder
}

func (r *recorderHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := r.ResponseWriter.(http.Hijacker); ok {
		conn, w, err := h.Hijack()
		if err != nil {
			return conn, w, errors.Wrap(err, "http.Hijacker.Hijack")
		}

		return conn, w, nil
	}

	return nil, nil, http.ErrNotSupported
}

type recorderFlusherHijacker struct {
	recorder
}

func (r *recorderFlusherHijacker) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *recorderFlusherHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := r.ResponseWriter.(http.Hijacker); ok {
		conn, w, err := h.Hijack()
		if err != nil {
			return conn, w, errors.Wrap(err, "http.Hijacker.Hijack")
		}

		return conn, w, nil
	}

	return nil, nil, http.ErrNotSupported
}

// generateID provides an id that matches the trace id format
func generateID() string {
	t := [16]byte{}

	_, _ = rand.Read(t[:])

	return hex.EncodeToString(t[:])
}
