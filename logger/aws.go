package logger

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"slices"
	"time"

	"cloud.google.com/go/logging"
	"go.opentelemetry.io/otel/trace"
)

const (
	awsTraceIDKey        = "trace_id"
	awsSpanIDKey         = "span_id"
	awsHTTPElapsedKey    = "http.elapsed"
	awsHTTPMethodKey     = "http.method"
	awsHTTPURLKey        = "http.url"
	awsHTTPStatusCodeKey = "http.status_code"
	awsHTTPRespLengthKey = "http.response.length"
	awsHTTPUserAgentKey  = "http.user_agent"
	awsHTTPRemoteIPKey   = "http.remote_ip"
	awsHTTPSchemeKey     = "http.scheme"
	awsHTTPProtoKey      = "http.proto"
)

// AWSExporter is an Exporter that logs to stdout in JSON format to be sent to cloudwatch
type AWSExporter struct {
	// policy is the exporter's default: Always, or OnEvent when built with logAll false
	policy Policy
}

// NewAWSExporter returns a new AWSExporter. logAll names its default policy: true is Always, and false is
// OnEvent, which writes a request only when a line attached to it or it failed. A DefaultPolicy option on the
// request logger overrides this default.
func NewAWSExporter(logAll bool) *AWSExporter {
	return &AWSExporter{
		policy: logAllPolicy(logAll),
	}
}

// Middleware returns a middleware that logs the request and injects a Logger into the context.
func (e *AWSExporter) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return &awsHandler{
			next:   next,
			logger: slog.New(slog.NewJSONHandler(os.Stdout, nil)),
			policy: e.policy,
		}
	}
}

// CliRunner returns a function that executes the given function and creates a top-level parent log.
func (e *AWSExporter) CliRunner() func(context.Context, string, func(context.Context) error) error {
	r := &awsRunner{
		logger: slog.New(slog.NewJSONHandler(os.Stdout, nil)),
		policy: e.policy,
	}

	return r.run
}

// DaemonContext returns a context with a logger that writes directly to stdout, unbuffered.
func (e *AWSExporter) DaemonContext(ctx context.Context) context.Context {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	l := newAWSLogger(logger, awsTraceIDFromContext(ctx), e.policy)

	return newContext(ctx, l)
}

type awsHandler struct {
	next   http.Handler
	logger awslog
	policy Policy
}

// ServeHTTP implements http.Handler
//
// This performs pre and post request logic for logging
func (h *awsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	begin := time.Now()
	xrayTraceID := awsTraceIDFromRequest(r, generateID)
	l := newAWSLogger(h.logger, xrayTraceID, h.policy)
	r = r.WithContext(newContext(r.Context(), l))
	sw := newResponseRecorder(w)

	h.next.ServeHTTP(sw, r)

	d := l.decide(sw.Status() >= http.StatusBadRequest)
	if !d.write {
		return
	}

	maxSeverity := d.maxSeverity
	if sw.Status() > 499 && maxSeverity < logging.Error {
		maxSeverity = logging.Error
	}

	sc := trace.SpanFromContext(r.Context()).SpanContext()

	logAttr := []slog.Attr{
		slog.Any(awsTraceIDKey, xrayTraceID),
		slog.Any(awsSpanIDKey, sc.SpanID().String()),
		slog.String(awsHTTPElapsedKey, time.Since(begin).String()),
	}
	logAttr = append(logAttr, httpAttributes(r, sw)...)
	for k, v := range d.attributes {
		logAttr = append(logAttr, slog.Any(k, v))
	}

	h.logger.LogAttrs(r.Context(), slogLevel(maxSeverity), parentLogEntry, logAttr...)
}

// awsRunner runs a command-line function under a parent log entry.
type awsRunner struct {
	logger awslog
	policy Policy
}

func (a *awsRunner) run(ctx context.Context, command string, f func(context.Context) error) error {
	begin := time.Now()
	traceID := awsTraceIDFromContext(ctx)
	l := newAWSLogger(a.logger, traceID, a.policy)
	ctx = newContext(ctx, l)

	err := f(ctx)

	d := l.decide(err != nil)
	if !d.write {
		return err
	}

	sc := trace.SpanFromContext(ctx).SpanContext()

	logAttr := []slog.Attr{
		slog.Any(awsTraceIDKey, traceID),
		slog.Any(awsSpanIDKey, sc.SpanID().String()),
		slog.String(awsHTTPElapsedKey, time.Since(begin).String()),
		slog.String(awsHTTPMethodKey, "CLI"),
		slog.String(awsHTTPURLKey, fmt.Sprintf("[%s] %s", time.Since(begin), command)),
	}
	for k, v := range d.attributes {
		logAttr = append(logAttr, slog.Any(k, v))
	}

	a.logger.LogAttrs(ctx, slogLevel(d.maxSeverity), parentLogEntry, logAttr...)

	return err
}

// awslog is the slog surface the AWS exporter writes through; it exists for testability
type awslog interface {
	LogAttrs(ctx context.Context, level slog.Level, msg string, attrs ...slog.Attr)
}

var _ ctxLogger = (*awsLogger)(nil)

type awsLogger struct {
	record
	root        *awsLogger
	logger      awslog
	traceID     string
	rsvdKeys    []string
	rsvdReqKeys []string
	attributes  map[string]any // attributes for child (trace) logs
}

func newAWSLogger(logger awslog, traceID string, policy Policy) *awsLogger {
	l := &awsLogger{
		record:   record{policy: policy, maxSeverity: logging.Info, reqAttributes: make(map[string]any)},
		logger:   logger,
		traceID:  traceID,
		rsvdKeys: []string{awsTraceIDKey, awsSpanIDKey},
		rsvdReqKeys: []string{
			awsTraceIDKey, awsSpanIDKey,
			awsHTTPElapsedKey, awsHTTPMethodKey, awsHTTPURLKey, awsHTTPStatusCodeKey, awsHTTPRespLengthKey, awsHTTPUserAgentKey, awsHTTPRemoteIPKey, awsHTTPSchemeKey, awsHTTPProtoKey,
		},
		attributes: make(map[string]any),
	}
	l.root = l // root is self

	return l
}

// newChild returns a new child awsLogger. The record is only used in the root logger, never the child.
func (l *awsLogger) newChild() *awsLogger {
	return &awsLogger{
		root:        l.root,
		logger:      l.logger,
		traceID:     l.traceID,
		rsvdKeys:    l.rsvdKeys,
		rsvdReqKeys: l.rsvdReqKeys,
		attributes:  make(map[string]any),
	}
}

// Debug logs a debug message.
func (l *awsLogger) Debug(ctx context.Context, v any) {
	l.log(ctx, logging.Debug, fmt.Sprint(v))
}

// Debugf logs a debug message with format.
func (l *awsLogger) Debugf(ctx context.Context, format string, v ...any) {
	l.log(ctx, logging.Debug, fmt.Sprintf(format, v...))
}

// Info logs a info message.
func (l *awsLogger) Info(ctx context.Context, v any) {
	l.log(ctx, logging.Info, fmt.Sprint(v))
}

// Infof logs a info message with format.
func (l *awsLogger) Infof(ctx context.Context, format string, v ...any) {
	l.log(ctx, logging.Info, fmt.Sprintf(format, v...))
}

// Warn logs a warning message.
func (l *awsLogger) Warn(ctx context.Context, v any) {
	l.log(ctx, logging.Warning, fmt.Sprint(v))
}

// Warnf logs a warning message with format.
func (l *awsLogger) Warnf(ctx context.Context, format string, v ...any) {
	l.log(ctx, logging.Warning, fmt.Sprintf(format, v...))
}

// Error logs an error message.
func (l *awsLogger) Error(ctx context.Context, v any) {
	l.log(ctx, logging.Error, fmt.Sprint(v))
}

// Errorf logs an error message with format.
func (l *awsLogger) Errorf(ctx context.Context, format string, v ...any) {
	l.log(ctx, logging.Error, fmt.Sprintf(format, v...))
}

// AddRequestAttribute adds an attribute (key, value) for the parent request log
// If the key matches a reserved key, it will be prefixed with "custom_"
// If the key already exists, its value is overwritten
func (l *awsLogger) AddRequestAttribute(key string, value any) {
	if slices.Contains(l.rsvdReqKeys, key) {
		key = customPrefix + key
	}

	l.root.addRequestAttribute(key, value)
}

// SetPolicy replaces the policy of the request or run this logger belongs to.
func (l *awsLogger) SetPolicy(p Policy) {
	l.root.setPolicy(p)
}

// WithAttributes returns an attributer that can be used to add child (trace) log attributes
func (l *awsLogger) WithAttributes() attributer {
	attrs := make(map[string]any)
	maps.Copy(attrs, l.attributes)

	return &awsAttributer{logger: l, attributes: attrs}
}

// TraceID returns the trace ID of the request logs
func (l *awsLogger) TraceID() string {
	return l.traceID
}

func (l *awsLogger) log(ctx context.Context, severity logging.Severity, message string) {
	if !l.root.attach(severity) {
		return
	}

	span := trace.SpanFromContext(ctx)
	attr := make([]slog.Attr, 0, 2+len(l.attributes))
	attr = append(attr,
		slog.String(awsTraceIDKey, l.traceID),
		slog.String(awsSpanIDKey, span.SpanContext().SpanID().String()),
	)
	for k, v := range l.attributes {
		attr = append(attr, slog.Any(k, v))
	}
	l.logger.LogAttrs(ctx, slogLevel(severity), message, attr...)
}

var _ attributer = (*awsAttributer)(nil)

type awsAttributer struct {
	logger     *awsLogger
	attributes map[string]any
}

// AddAttribute adds an attribute (key, value) for the child (trace) log
// If the key matches a reserved key, it will be prefixed with "custom_"
// If the key already exists, its value is overwritten
func (a *awsAttributer) AddAttribute(key string, value any) {
	if slices.Contains(a.logger.rsvdKeys, key) {
		key = customPrefix + key
	}

	a.attributes[key] = value
}

// Logger returns a ctxLogger with the child (trace) attributes embedded
func (a *awsAttributer) Logger() ctxLogger {
	l := a.logger.newChild()
	maps.Copy(l.attributes, a.attributes)

	return l
}

// httpAttributes returns a slice of slog.Attr for the http request and response
func httpAttributes(r *http.Request, sw responseRecorder) []slog.Attr {
	return []slog.Attr{
		slog.String(awsHTTPMethodKey, r.Method),
		slog.String(awsHTTPURLKey, r.URL.String()),
		slog.Int(awsHTTPStatusCodeKey, sw.Status()),
		slog.Int64(awsHTTPRespLengthKey, sw.Length()),
		slog.String(awsHTTPUserAgentKey, r.UserAgent()),
		slog.String(awsHTTPRemoteIPKey, r.RemoteAddr),
		slog.String(awsHTTPSchemeKey, r.URL.Scheme),
		slog.String(awsHTTPProtoKey, r.Proto),
	}
}

// awsTraceIDFromRequest retrieves the trace id from the request if possible
func awsTraceIDFromRequest(r *http.Request, idgen func() string) string {
	var traceID string
	sc := trace.SpanFromContext(r.Context()).SpanContext()
	if sc.IsValid() {
		traceID = sc.TraceID().String()
	} else {
		traceID = idgen()
	}

	return traceID
}

// awsTraceIDFromContext returns the trace id of the span in the context, or a generated one when the context
// carries no valid span.
func awsTraceIDFromContext(ctx context.Context) string {
	if sc := trace.SpanFromContext(ctx).SpanContext(); sc.IsValid() {
		return sc.TraceID().String()
	}

	return generateID()
}

// slogLevel is the level the AWS exporter writes a severity at: the four severities this package logs at map
// one to one, and anything lower than Info is Debug.
func slogLevel(severity logging.Severity) slog.Level {
	switch {
	case severity >= logging.Error:
		return slog.LevelError
	case severity >= logging.Warning:
		return slog.LevelWarn
	case severity >= logging.Info:
		return slog.LevelInfo
	default:
		return slog.LevelDebug
	}
}
