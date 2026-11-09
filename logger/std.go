package logger

import (
	"context"
	"fmt"
	"log"
	"maps"
)

var _ ctxLogger = (*stdErrLogger)(nil)

type stdErrLogger struct {
	attributes map[string]any
}

// newStdErrLogger returns a new stdErrLogger
func newStdErrLogger() *stdErrLogger {
	return &stdErrLogger{attributes: map[string]any{}}
}

// Debug logs a debug message.
func (l *stdErrLogger) Debug(_ context.Context, v any) {
	l.std("DEBUG", fmt.Sprint(v))
}

// Debugf logs a debug message with format.
func (l *stdErrLogger) Debugf(_ context.Context, format string, v ...any) {
	l.std("DEBUG", fmt.Sprintf(format, v...))
}

// Info logs a info message.
func (l *stdErrLogger) Info(_ context.Context, v any) {
	l.std("INFO ", fmt.Sprint(v))
}

// Infof logs a info message with format.
func (l *stdErrLogger) Infof(_ context.Context, format string, v ...any) {
	l.std("INFO ", fmt.Sprintf(format, v...))
}

// Warn logs a warning message.
func (l *stdErrLogger) Warn(_ context.Context, v any) {
	l.std("WARN ", fmt.Sprint(v))
}

// Warnf logs a warning message with format.
func (l *stdErrLogger) Warnf(_ context.Context, format string, v ...any) {
	l.std("WARN ", fmt.Sprintf(format, v...))
}

// Error logs an error message.
func (l *stdErrLogger) Error(_ context.Context, v any) {
	l.std("ERROR", fmt.Sprint(v))
}

// Errorf logs an error message with format.
func (l *stdErrLogger) Errorf(_ context.Context, format string, v ...any) {
	l.std("ERROR", fmt.Sprintf(format, v...))
}

// AddRequestAttribute adds an attribute (key, value) for the parent request log
// For this std logger, there is no parent request log, so this is a no-op
func (l *stdErrLogger) AddRequestAttribute(_ string, _ any) {}

// SetPolicy does nothing: the std logger belongs to no request or run, so there is no parent entry to decide
func (l *stdErrLogger) SetPolicy(_ Policy) {}

// WithAttributes returns an attributer that can be used to add child (trace) log attributes
func (l *stdErrLogger) WithAttributes() attributer {
	attrs := make(map[string]any)
	maps.Copy(attrs, l.attributes)

	return &stdAttributer{logger: l, attributes: attrs}
}

// TraceID returns an empty string for the std logger
func (l *stdErrLogger) TraceID() string {
	return ""
}

func (l *stdErrLogger) std(level, msg string) {
	for k, v := range l.attributes {
		msg += fmt.Sprintf(", %s=%v", k, v)
	}

	log.Printf(level+": %s", msg)
}

type stdAttributer struct {
	logger     *stdErrLogger
	attributes map[string]any
}

// AddAttribute adds an attribute (key, value) for the child (trace) log
// If the key already exists, its value is overwritten
func (a *stdAttributer) AddAttribute(key string, value any) {
	a.attributes[key] = value
}

// Logger returns a ctxLogger with the child (trace) attributes embedded
func (a *stdAttributer) Logger() ctxLogger {
	l := newStdErrLogger()
	maps.Copy(l.attributes, a.attributes)

	return l
}
