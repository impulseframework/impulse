// Package logger is an HTTP request logger that implements correlated logging to one of several supported platforms.
// Each HTTP request is logged as the parent log, with all logs generated during the request as child logs.
//
// The Logging destination is configured with an Exporter. This package provides Exporters for Google Cloud Logging, AWS CloudWatch,
// and Console Logging.
//
// The GoogleCloudExporter will also correlate logs to Cloud Trace if you instrument your code with tracing.
//
// The AWSExporter supports log correlation to AWS X-Ray if you instrument your code with tracing.
package logger

//go:generate mockgen -source context.go -package $GOPACKAGE -destination mock_context_test.go

import (
	"context"
	"net/http"
)

const (
	parentLogEntry = "Parent Log Entry"
	customPrefix   = "custom_"
)

// Logger implements logging methods for this package
type Logger struct {
	ctx context.Context
	lg  ctxLogger
}

// FromCtx returns the logger from the context. If
// no logger is found, it will write to stderr
func FromCtx(ctx context.Context) *Logger {
	return &Logger{
		ctx: ctx,
		lg:  fromCtx(ctx),
	}
}

// NewCtx associates the logger with the context and returns the resulting context
func NewCtx(ctx context.Context, l *Logger) context.Context {
	return newContext(ctx, l.lg)
}

// FromReq returns the logger from the http request. If
// no logger is found, it will write to stderr
func FromReq(r *http.Request) *Logger {
	return &Logger{
		ctx: r.Context(),
		lg:  fromReq(r),
	}
}

// TraceID returns the trace ID of the request logs
func (l *Logger) TraceID() string {
	return l.lg.TraceID()
}

// Debug logs a debug message.
func (l *Logger) Debug(v any) {
	l.lg.Debug(l.ctx, v)
}

// Debugf logs a debug message with format.
func (l *Logger) Debugf(format string, v ...any) {
	l.lg.Debugf(l.ctx, format, v...)
}

// Info logs a info message.
func (l *Logger) Info(v any) {
	l.lg.Info(l.ctx, v)
}

// Infof logs a info message with format.
func (l *Logger) Infof(format string, v ...any) {
	l.lg.Infof(l.ctx, format, v...)
}

// Warn logs a warning message.
func (l *Logger) Warn(v any) {
	l.lg.Warn(l.ctx, v)
}

// Warnf logs a warning message with format.
func (l *Logger) Warnf(format string, v ...any) {
	l.lg.Warnf(l.ctx, format, v...)
}

// Error logs an error message.
func (l *Logger) Error(v any) {
	l.lg.Error(l.ctx, v)
}

// Errorf logs an error message with format.
func (l *Logger) Errorf(format string, v ...any) {
	l.lg.Errorf(l.ctx, format, v...)
}

// AddRequestAttribute adds an attribute (kv) for the parent request log and returns a reference to the original logger for method chaining purposes
// If the key matches a reserved key, it will be prefixed with "custom_"
// If the key already exists, its value is overwritten
func (l *Logger) AddRequestAttribute(key string, value any) *Logger {
	l.lg.AddRequestAttribute(key, value)

	return l
}

// SetPolicy replaces the Policy of the request or run this logger belongs to: a handler promotes or demotes its
// own request. The nearest declaration wins, so this call counts over the request logger's options and over any
// WithPolicy on the way down. Without a request logger behind the context it does nothing.
func (l *Logger) SetPolicy(p Policy) {
	l.lg.SetPolicy(p)
}

// WithAttributes returns an AttributerLogger that can be used to add child (trace) log attributes
func (l *Logger) WithAttributes() *AttributerLogger {
	return &AttributerLogger{
		logger:     l,
		attributer: l.lg.WithAttributes(),
	}
}

// AttributerLogger holds the atrributes and logger used for creating a child logger
type AttributerLogger struct {
	logger     *Logger
	attributer attributer
}

// AddAttribute adds an attribute (kv) for the child (trace) log and returns a reference to the original AttributerLogger for method chaining purposes
// If the key matches a reserved key, it will be prefixed with "custom_"
// If the key already exists, its value is overwritten
func (a *AttributerLogger) AddAttribute(key string, value any) *AttributerLogger {
	a.attributer.AddAttribute(key, value)

	return a
}

// Logger returns a Logger with the child (trace) attributes embedded
func (a *AttributerLogger) Logger() *Logger {
	return &Logger{
		ctx: a.logger.ctx,
		lg:  a.attributer.Logger(),
	}
}
