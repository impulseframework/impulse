package logger

import (
	"context"
	"net/http"
)

type key int

const (
	logKey key = iota
)

// fromCtx gets the logger out of the context.
// If no logger is stored in the context, a stderr logger is returned.
func fromCtx(ctx context.Context) ctxLogger {
	if ctx == nil {
		return newStdErrLogger()
	}
	l, ok := ctx.Value(logKey).(ctxLogger)
	if !ok {
		return newStdErrLogger()
	}

	return l
}

// fromReq gets the logger in the request's context.
func fromReq(r *http.Request) ctxLogger {
	if r == nil {
		return newStdErrLogger()
	}

	return fromCtx(r.Context())
}

// newContext returns a copy of the parent context and associates it with the provided logger.
func newContext(ctx context.Context, l ctxLogger) context.Context {
	return context.WithValue(ctx, logKey, l)
}

// ctxLogger defines the logging interface with context
type ctxLogger interface {
	// Debug logs a debug message.
	Debug(ctx context.Context, v any)
	// Debugf logs a debug message with format.
	Debugf(ctx context.Context, format string, v ...any)
	// Info logs a info message.
	Info(ctx context.Context, v any)
	// Infof logs a info message with format.
	Infof(ctx context.Context, format string, v ...any)
	// Warn logs a warning message.
	Warn(ctx context.Context, v any)
	// Warnf logs a warning message with format.
	Warnf(ctx context.Context, format string, v ...any)
	// Error logs an error message.
	Error(ctx context.Context, v any)
	// Errorf logs an error message with format.
	Errorf(ctx context.Context, format string, v ...any)

	// AddRequestAttribute adds an attribute (kv) for the parent request log
	// If the key matches a reserved key, it will be prefixed with "custom_"
	// If the key already exists, its value is overwritten
	AddRequestAttribute(key string, value any)

	// SetPolicy replaces the Policy of the request or run the logger belongs to
	SetPolicy(p Policy)

	// WithAttributes returns an attributer that can be used to add child (trace) log attributes
	WithAttributes() attributer

	// TraceID returns the trace ID of the request logs
	TraceID() string
}

// attributer defines the interface for adding attributes for child (trace) logs
type attributer interface {
	// AddAttribute adds an attribute (kv) for the child (trace) log
	// If the key matches a reserved key, it will be prefixed with "custom_"
	// If the key already exists, its value is overwritten
	AddAttribute(key string, value any)

	// Logger returns a ctxLogger with the child (trace) attributes embedded
	Logger() ctxLogger
}
