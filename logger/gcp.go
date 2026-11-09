package logger

import (
	"context"
	"encoding/hex"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"cloud.google.com/go/logging"
	"go.opentelemetry.io/otel/trace"
)

const gcpMessageKey = "message"

// GoogleCloudExporter implements exporting to Google Cloud Logging
type GoogleCloudExporter struct {
	projectID string
	client    *logging.Client
	opts      []logging.LoggerOption
	policy    Policy
}

// NewGoogleCloudExporter returns a configured GoogleCloudExporter
func NewGoogleCloudExporter(client *logging.Client, projectID string, opts ...logging.LoggerOption) *GoogleCloudExporter {
	return &GoogleCloudExporter{
		projectID: projectID,
		client:    client,
		opts:      opts,
		policy:    Always(),
	}
}

// LogAll sets the exporter's default policy: true is Always, the default, and false is OnEvent, which writes a
// request only when a line attached to it or it failed. A DefaultPolicy option on the request logger overrides
// this default.
func (e *GoogleCloudExporter) LogAll(v bool) *GoogleCloudExporter {
	e.policy = logAllPolicy(v)

	return e
}

// Middleware returns a middleware that exports logs to Google Cloud Logging
func (e *GoogleCloudExporter) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return &gcpHandler{
			next:         next,
			parentLogger: e.client.Logger("request_parent_log", e.opts...),
			childLogger:  e.client.Logger("request_child_log", e.opts...),
			projectID:    e.projectID,
			policy:       e.policy,
		}
	}
}

// CliRunner returns a function that executes the given function and creates a top-level parent log.
func (e *GoogleCloudExporter) CliRunner() func(context.Context, string, func(context.Context) error) error {
	r := &gcpRunner{
		parentLogger: e.client.Logger("request_parent_log", e.opts...),
		childLogger:  e.client.Logger("request_child_log", e.opts...),
		projectID:    e.projectID,
		policy:       e.policy,
	}

	return r.run
}

// DaemonContext returns a context with a logger that writes directly to the parent log, unbuffered.
func (e *GoogleCloudExporter) DaemonContext(ctx context.Context) context.Context {
	parentLogger := e.client.Logger("request_parent_log", e.opts...)
	l := newGCPLogger(parentLogger, gcpTraceIDFromContext(ctx, e.projectID), e.policy)

	return newContext(ctx, l)
}

type gcpHandler struct {
	next         http.Handler
	parentLogger logger
	childLogger  logger
	projectID    string
	policy       Policy
}

func (g *gcpHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	begin := time.Now()
	traceID := gcpTraceIDFromRequest(r, g.projectID, generateID)
	l := newGCPLogger(g.childLogger, traceID, g.policy)
	r = r.WithContext(newContext(r.Context(), l))
	sw := newResponseRecorder(w)

	g.next.ServeHTTP(sw, r)

	d := l.decide(sw.Status() >= http.StatusBadRequest)
	if !d.write {
		return
	}

	// status code should also set the minimum maxSeverity to Error
	maxSeverity := d.maxSeverity
	if sw.Status() > 499 && maxSeverity < logging.Error {
		maxSeverity = logging.Error
	}

	sc := trace.SpanFromContext(r.Context()).SpanContext()

	d.attributes[gcpMessageKey] = parentLogEntry

	g.parentLogger.Log(logging.Entry{
		Timestamp:    begin,
		Severity:     maxSeverity,
		Trace:        traceID,
		SpanID:       sc.SpanID().String(),
		TraceSampled: sc.IsSampled(),
		Payload:      d.attributes,
		HTTPRequest: &logging.HTTPRequest{
			Request:      r,
			RequestSize:  requestSize(r.Header.Get("Content-Length")),
			Latency:      time.Since(begin),
			Status:       sw.Status(),
			ResponseSize: sw.Length(),
			RemoteIP:     r.Header.Get("X-Forwarded-For"),
		},
	})
}

// gcpRunner runs a command-line function under a parent log entry.
type gcpRunner struct {
	parentLogger logger
	childLogger  logger
	projectID    string
	policy       Policy
}

func (g *gcpRunner) run(ctx context.Context, command string, f func(context.Context) error) error {
	begin := time.Now()
	traceID := gcpTraceIDFromContext(ctx, g.projectID)
	l := newGCPLogger(g.childLogger, traceID, g.policy)
	ctx = newContext(ctx, l)

	err := f(ctx)

	d := l.decide(err != nil)
	if !d.write {
		return err
	}

	sc := trace.SpanFromContext(ctx).SpanContext()
	d.attributes[gcpMessageKey] = fmt.Sprintf("CLI [%s] %s", time.Since(begin), command)
	d.attributes["latency"] = time.Since(begin)
	d.attributes["command"] = command

	g.parentLogger.Log(logging.Entry{
		Timestamp:    begin,
		Severity:     d.maxSeverity,
		Trace:        traceID,
		SpanID:       sc.SpanID().String(),
		TraceSampled: sc.IsSampled(),
		Payload:      d.attributes,
	})

	return err
}

// gcpTraceIDFromRequest formats a trace_id value for GCP Stackdriver
func gcpTraceIDFromRequest(r *http.Request, projectID string, idgen func() string) string {
	var traceID string
	if sc := trace.SpanFromContext(r.Context()).SpanContext(); sc.IsValid() {
		traceID = sc.TraceID().String()
	} else if id, ok := traceIDFromHeader(r.Header.Get("X-Cloud-Trace-Context")); ok {
		traceID = id
	} else {
		traceID = idgen()
	}

	return fmt.Sprintf("projects/%s/traces/%s", projectID, traceID)
}

// gcpTraceIDFromContext formats a trace_id value from the span in the context, or from a generated id when the
// context carries no valid span.
func gcpTraceIDFromContext(ctx context.Context, projectID string) string {
	var traceID string
	if sc := trace.SpanFromContext(ctx).SpanContext(); sc.IsValid() {
		traceID = sc.TraceID().String()
	} else {
		traceID = generateID()
	}

	return fmt.Sprintf("projects/%s/traces/%s", projectID, traceID)
}

// traceIDFromHeader extracts the trace ID from an X-Cloud-Trace-Context header
// value, which GCP infrastructure (load balancers, App Engine, etc.) formats as
// "TRACE_ID/SPAN_ID;o=OPTIONS" where TRACE_ID is 32 hex characters.
// See https://cloud.google.com/trace/docs/trace-context
func traceIDFromHeader(h string) (string, bool) {
	if i := strings.IndexByte(h, '/'); i >= 0 {
		h = h[:i]
	}
	buf, err := hex.DecodeString(h)
	if err != nil || len(buf) != 16 {
		return "", false
	}

	return hex.EncodeToString(buf), true
}

// logger interface exists for testability
type logger interface {
	Log(e logging.Entry)
}

var _ ctxLogger = (*gcpLogger)(nil)

type gcpLogger struct {
	record
	root       *gcpLogger
	logger     logger
	traceID    string
	rsvdKeys   []string
	attributes map[string]any // attributes for child (trace) logs
}

func newGCPLogger(lg logger, traceID string, policy Policy) *gcpLogger {
	l := &gcpLogger{
		record:     record{policy: policy, reqAttributes: make(map[string]any)},
		logger:     lg,
		traceID:    traceID,
		rsvdKeys:   []string{gcpMessageKey},
		attributes: make(map[string]any),
	}
	l.root = l // root is self

	return l
}

// newChild returns a new child gcpLogger. The record is only used in the root logger, never the child.
func (l *gcpLogger) newChild() *gcpLogger {
	return &gcpLogger{
		root:       l.root,
		logger:     l.logger,
		traceID:    l.traceID,
		rsvdKeys:   l.rsvdKeys,
		attributes: make(map[string]any),
	}
}

// Debug logs a debug message.
func (l *gcpLogger) Debug(ctx context.Context, v any) {
	l.log(ctx, logging.Debug, v)
}

// Debugf logs a debug message with format.
func (l *gcpLogger) Debugf(ctx context.Context, format string, v ...any) {
	l.log(ctx, logging.Debug, fmt.Sprintf(format, v...))
}

// Info logs a info message.
func (l *gcpLogger) Info(ctx context.Context, v any) {
	l.log(ctx, logging.Info, v)
}

// Infof logs a info message with format.
func (l *gcpLogger) Infof(ctx context.Context, format string, v ...any) {
	l.log(ctx, logging.Info, fmt.Sprintf(format, v...))
}

// Warn logs a warning message.
func (l *gcpLogger) Warn(ctx context.Context, v any) {
	l.log(ctx, logging.Warning, v)
}

// Warnf logs a warning message with format.
func (l *gcpLogger) Warnf(ctx context.Context, format string, v ...any) {
	l.log(ctx, logging.Warning, fmt.Sprintf(format, v...))
}

// Error logs an error message.
func (l *gcpLogger) Error(ctx context.Context, v any) {
	l.log(ctx, logging.Error, v)
}

// Errorf logs an error message with format.
func (l *gcpLogger) Errorf(ctx context.Context, format string, v ...any) {
	l.log(ctx, logging.Error, fmt.Sprintf(format, v...))
}

// AddRequestAttribute adds an attribute (key, value) for the parent request log
// If the key matches a reserved key, it will be prefixed with "custom_"
// If the key already exists, its value is overwritten
func (l *gcpLogger) AddRequestAttribute(key string, value any) {
	if slices.Contains(l.rsvdKeys, key) {
		key = customPrefix + key
	}

	l.root.addRequestAttribute(key, value)
}

// SetPolicy replaces the policy of the request or run this logger belongs to.
func (l *gcpLogger) SetPolicy(p Policy) {
	l.root.setPolicy(p)
}

// WithAttributes returns an attributer that can be used to add child (trace) log attributes
func (l *gcpLogger) WithAttributes() attributer {
	attrs := make(map[string]any)
	maps.Copy(attrs, l.attributes)

	return &gcpAttributer{logger: l, attributes: attrs}
}

// TraceID returns the trace ID of the request logs
func (l *gcpLogger) TraceID() string {
	return l.traceID
}

func (l *gcpLogger) log(ctx context.Context, severity logging.Severity, msg any) {
	if !l.root.attach(severity) {
		return
	}

	if err, ok := msg.(error); ok {
		msg = err.Error()
	}

	span := trace.SpanFromContext(ctx)
	attrs := make(map[string]any)
	maps.Copy(attrs, l.attributes)
	attrs[gcpMessageKey] = msg

	l.logger.Log(
		logging.Entry{
			Payload:      attrs,
			Severity:     severity,
			Trace:        l.traceID,
			SpanID:       span.SpanContext().SpanID().String(),
			TraceSampled: span.SpanContext().IsSampled(),
		},
	)
}

var _ attributer = (*gcpAttributer)(nil)

type gcpAttributer struct {
	logger     *gcpLogger
	attributes map[string]any
}

// AddAttribute adds an attribute (key, value) for the child (trace) log
// If the key matches a reserved key, it will be prefixed with "custom_"
// If the key already exists, its value is overwritten
func (a *gcpAttributer) AddAttribute(key string, value any) {
	if slices.Contains(a.logger.rsvdKeys, key) {
		key = customPrefix + key
	}

	a.attributes[key] = value
}

// Logger returns a ctxLogger with the child (trace) attributes embedded
func (a *gcpAttributer) Logger() ctxLogger {
	l := a.logger.newChild()
	maps.Copy(l.attributes, a.attributes)

	return l
}
