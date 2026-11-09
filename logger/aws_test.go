package logger

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"cloud.google.com/go/logging"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestNewAWSExporter(t *testing.T) {
	t.Parallel()
	type args struct {
		logAll bool
	}
	tests := []struct {
		name string
		args args
		want *AWSExporter
	}{
		{
			name: "TestNewAWSExporter with logall true",
			args: args{
				logAll: true,
			},
			want: &AWSExporter{
				policy: Always(),
			},
		},
		{
			name: "TestNewAWSExporter with logall false",
			args: args{
				logAll: false,
			},
			want: &AWSExporter{
				policy: OnEvent(),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := NewAWSExporter(tt.args.logAll)

			if diff := cmp.Diff(got, tt.want, cmp.AllowUnexported(AWSExporter{}, Policy{})); diff != "" {
				t.Errorf("NewAWSExporter() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestAWSExporter_Middleware(t *testing.T) {
	t.Parallel()

	type fields struct {
		policy Policy
	}
	tests := []struct {
		name   string
		fields fields
		want   func(http.Handler) http.Handler
	}{
		{
			name: "TestAWSExporter_Middleware",
			fields: fields{
				policy: Always(),
			},
			want: func(next http.Handler) http.Handler {
				return &awsHandler{
					next:   next,
					logger: slog.New(slog.NewJSONHandler(os.Stdout, nil)).WithGroup("request_parent_log"),
					policy: Always(),
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {})
			e := &AWSExporter{
				policy: tt.fields.policy,
			}

			got := e.Middleware()(next)
			if diff := cmp.Diff(got, tt.want(next), cmpopts.IgnoreUnexported(awsHandler{})); diff != "" {
				t.Errorf("AWSExporter.Middleware() = %s", diff)
			}
		})
	}
}

func TestAWSExporter_CliRunner(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		logAll bool
	}{
		{
			name:   "runner from the exporter",
			logAll: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := NewAWSExporter(tt.logAll).CliRunner(); got == nil {
				t.Errorf("AWSExporter.CliRunner() returned nil")
			}
		})
	}
}

func Test_awsRunner_run(t *testing.T) {
	t.Parallel()

	type args struct {
		lines    int
		severity logging.Severity
		err      error
		draw     float64
	}
	tests := []struct {
		name       string
		policy     Policy
		args       args
		wantParent bool
		wantLevel  slog.Level
		wantCalls  int
	}{
		{
			name:       "always, quiet run",
			policy:     Always(),
			wantParent: true,
			wantLevel:  slog.LevelInfo,
			wantCalls:  1,
		},
		{
			name:       "always, failed run",
			policy:     Always(),
			args:       args{err: errors.New("failed")},
			wantParent: true,
			wantLevel:  slog.LevelInfo,
			wantCalls:  1,
		},
		{
			name:   "on event, quiet run",
			policy: OnEvent(),
		},
		{
			name:       "on event, a line attached",
			policy:     OnEvent(),
			args:       args{lines: 1, severity: logging.Warning},
			wantParent: true,
			wantLevel:  slog.LevelWarn,
			wantCalls:  2,
		},
		{
			name:       "on event, failed run",
			policy:     OnEvent(),
			args:       args{err: errors.New("failed")},
			wantParent: true,
			wantLevel:  slog.LevelInfo,
			wantCalls:  1,
		},
		{
			name:       "sampled, the draw hits",
			policy:     Sampled(0.5),
			args:       args{draw: 0.25},
			wantParent: true,
			wantLevel:  slog.LevelInfo,
			wantCalls:  1,
		},
		{
			name:   "sampled, the draw misses",
			policy: Sampled(0.5),
			args:   args{draw: 0.75},
		},
		{
			name:       "sampled, the draw misses but the run failed",
			policy:     Sampled(0.5),
			args:       args{draw: 0.75, err: errors.New("failed")},
			wantParent: true,
			wantLevel:  slog.LevelInfo,
			wantCalls:  1,
		},
		{
			name:      "never, failed run with an error line",
			policy:    Never(),
			args:      args{lines: 1, severity: logging.Error, err: errors.New("failed")},
			wantCalls: 1,
		},
		{
			name:   "floor drops the line, so nothing attached",
			policy: OnEvent().MinSeverity(logging.Warning),
			args:   args{lines: 1, severity: logging.Info},
		},
		{
			name:       "floor admits the line",
			policy:     OnEvent().MinSeverity(logging.Warning),
			args:       args{lines: 1, severity: logging.Error},
			wantParent: true,
			wantLevel:  slog.LevelError,
			wantCalls:  2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			l := &captureSLogger{}
			runner := &awsRunner{logger: l, policy: tt.policy}
			err := runner.run(t.Context(), "aws-command --test", func(ctx context.Context) error {
				awsLgr, ok := FromCtx(ctx).lg.(*awsLogger)
				if !ok {
					t.Fatal("Failed to get awsLogger from context")
				}
				awsLgr.draw = func() float64 {
					return tt.args.draw
				}
				FromCtx(ctx).AddRequestAttribute("test_aws_key", "test_aws_value")
				logLines(ctx, tt.args.lines, tt.args.severity)

				return tt.args.err
			})
			if !errors.Is(err, tt.args.err) {
				t.Errorf("awsRunner.run() error = %v, want %v", err, tt.args.err)
			}
			if got := l.msg == parentLogEntry; got != tt.wantParent {
				t.Fatalf("parent entry written = %v, want %v", got, tt.wantParent)
			}
			if l.calls != tt.wantCalls {
				t.Errorf("calls = %d, want %d", l.calls, tt.wantCalls)
			}
			if tt.wantParent && l.level != tt.wantLevel {
				t.Errorf("Level = %v, want %v", l.level, tt.wantLevel)
			}
		})
	}
}

func Test_awsHandler_ServeHTTP(t *testing.T) {
	t.Parallel()

	type args struct {
		status   int
		lines    int
		severity logging.Severity
		draw     float64
	}
	tests := []struct {
		name       string
		policy     Policy
		args       args
		wantParent bool
		wantLevel  slog.Level
		wantCalls  int
	}{
		{
			name:       "always, quiet request",
			policy:     Always(),
			args:       args{status: http.StatusOK},
			wantParent: true,
			wantLevel:  slog.LevelInfo,
			wantCalls:  1,
		},
		{
			name:       "always, a line attached",
			policy:     Always(),
			args:       args{status: http.StatusOK, lines: 1, severity: logging.Info},
			wantParent: true,
			wantLevel:  slog.LevelInfo,
			wantCalls:  2,
		},
		{
			name:       "always, a 500 raises the entry to error",
			policy:     Always(),
			args:       args{status: http.StatusInternalServerError},
			wantParent: true,
			wantLevel:  slog.LevelError,
			wantCalls:  1,
		},
		{
			name:   "on event, quiet request",
			policy: OnEvent(),
			args:   args{status: http.StatusOK},
		},
		{
			name:       "on event, a line attached",
			policy:     OnEvent(),
			args:       args{status: http.StatusOK, lines: 1, severity: logging.Warning},
			wantParent: true,
			wantLevel:  slog.LevelWarn,
			wantCalls:  2,
		},
		{
			name:       "on event, a 404 is an event",
			policy:     OnEvent(),
			args:       args{status: http.StatusNotFound},
			wantParent: true,
			wantLevel:  slog.LevelInfo,
			wantCalls:  1,
		},
		{
			name:       "on event, a 500 is an event, raised to error",
			policy:     OnEvent(),
			args:       args{status: http.StatusInternalServerError},
			wantParent: true,
			wantLevel:  slog.LevelError,
			wantCalls:  1,
		},
		{
			name:   "on event, a 304 is not an event",
			policy: OnEvent(),
			args:   args{status: http.StatusNotModified},
		},
		{
			name:       "sampled, the draw hits",
			policy:     Sampled(0.5),
			args:       args{status: http.StatusOK, draw: 0.25},
			wantParent: true,
			wantLevel:  slog.LevelInfo,
			wantCalls:  1,
		},
		{
			name:   "sampled, the draw misses",
			policy: Sampled(0.5),
			args:   args{status: http.StatusOK, draw: 0.75},
		},
		{
			name:       "sampled, the draw misses but the request failed",
			policy:     Sampled(0.5),
			args:       args{status: http.StatusBadRequest, draw: 0.75},
			wantParent: true,
			wantLevel:  slog.LevelInfo,
			wantCalls:  1,
		},
		{
			name:      "never, a 500 with an error line",
			policy:    Never(),
			args:      args{status: http.StatusInternalServerError, lines: 1, severity: logging.Error},
			wantCalls: 1,
		},
		{
			name:   "floor drops the line, so nothing attached",
			policy: OnEvent().MinSeverity(logging.Warning),
			args:   args{status: http.StatusOK, lines: 1, severity: logging.Info},
		},
		{
			name:       "floor admits the line",
			policy:     OnEvent().MinSeverity(logging.Warning),
			args:       args{status: http.StatusOK, lines: 1, severity: logging.Error},
			wantParent: true,
			wantLevel:  slog.LevelError,
			wantCalls:  2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var handlerCalled bool

			l := &captureSLogger{}
			handler := &awsHandler{
				logger: l,
				policy: tt.policy,
				next: http.HandlerFunc(
					func(w http.ResponseWriter, r *http.Request) {
						awsLgr, ok := FromReq(r).lg.(*awsLogger)
						if !ok {
							t.Fatal("Failed to get awsLogger from request")
						}
						awsLgr.draw = func() float64 {
							return tt.args.draw
						}
						awsLgr.reqAttributes["test_req_key_1"] = "test_req_value_1"
						awsLgr.reqAttributes["test_req_key_2"] = "test_req_value_2"
						logLines(r.Context(), tt.args.lines, tt.args.severity)

						w.WriteHeader(tt.args.status)
						handlerCalled = true
					},
				),
			}

			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
			handler.ServeHTTP(httptest.NewRecorder(), r)

			if !handlerCalled {
				t.Fatal("Failed to call handler")
			}
			if got := l.msg == parentLogEntry; got != tt.wantParent {
				t.Fatalf("parent entry written = %v, want %v", got, tt.wantParent)
			}
			if l.calls != tt.wantCalls {
				t.Errorf("calls = %d, want %d", l.calls, tt.wantCalls)
			}
			if !tt.wantParent {
				return
			}
			if l.level != tt.wantLevel {
				t.Errorf("Level = %v, want %v", l.level, tt.wantLevel)
			}
			if len(l.attrs) != 13 {
				t.Errorf("Expected %d request attributes, got %d", 13, len(l.attrs))
			}
		})
	}
}

func Test_awsTraceIDFromRequest(t *testing.T) {
	t.Parallel()
	type args struct {
		mockReq  func(traceStr string) (*http.Request, string)
		traceStr string
	}
	tests := []struct {
		name         string
		args         args
		wantTraceStr string
		wantBlankStr bool
	}{
		// The order these are significant
		{
			// This test relies on the global tracing provider NOT being set
			name: "no trace in request",
			args: args{
				mockReq: func(wantTraceStr string) (*http.Request, string) {
					return &http.Request{URL: &url.URL{}}, wantTraceStr
				},
				traceStr: "105445aa7843bc8bf206b12000100000",
			},
			wantTraceStr: "105445aa7843bc8bf206b12000100000",
			wantBlankStr: false,
		},
		{
			// This test sets the global tracing provider (I don't think this can be un-done)
			name: "with trace in request",
			args: args{
				mockReq: func(_ string) (r *http.Request, traceStr string) {
					otel.SetTracerProvider(sdktrace.NewTracerProvider())
					ctx, span := otel.Tracer("test/examples").Start(context.Background(), "test trace")

					r = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
					r = r.WithContext(ctx)

					return r, span.SpanContext().TraceID().String()
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r, want := tt.args.mockReq(tt.wantTraceStr)

			if got := awsTraceIDFromRequest(r, func() string { return tt.args.traceStr }); got != want && (got == "0000000000000000") != tt.wantBlankStr {
				t.Errorf("awsTraceIDFromRequest() = %v, want %v", got, want)
			}
		})
	}
}

func Test_newAWSLogger(t *testing.T) {
	t.Parallel()

	type args struct {
		logger  awslog
		traceID string
		policy  Policy
	}
	tests := []struct {
		name string
		args args
		want *awsLogger
	}{
		{
			name: "Test_newAWSLogger",
			args: args{
				logger:  &testSlogger{},
				traceID: "1234567890",
				policy:  OnEvent(),
			},
			want: &awsLogger{
				record:      record{policy: OnEvent(), maxSeverity: logging.Info, reqAttributes: map[string]any{}},
				logger:      &testSlogger{},
				traceID:     "1234567890",
				rsvdKeys:    []string{"trace_id", "span_id"},
				rsvdReqKeys: []string{"trace_id", "span_id", "http.elapsed", "http.method", "http.url", "http.status_code", "http.response.length", "http.user_agent", "http.remote_ip", "http.scheme", "http.proto"},
				attributes:  map[string]any{},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := newAWSLogger(tt.args.logger, tt.args.traceID, tt.args.policy)
			if diff := cmp.Diff(got, tt.want, cmpopts.IgnoreFields(awsLogger{}, "logger", "record.mu", "root"), cmp.AllowUnexported(awsLogger{}, record{}, Policy{})); diff != "" {
				t.Errorf("newAWSLogger() mismatch (-want +got):\n%s", diff)
			}
			if got.root != got {
				t.Errorf("newAWSLogger().root is not self")
			}
		})
	}
}

func Test_awsLogger(t *testing.T) {
	t.Parallel()

	type fields struct {
		traceID    string
		attributes map[string]any
	}
	type args struct {
		format string
		v      []any
		v2     any
	}
	tests := []struct {
		name       string
		fields     fields
		args       args
		wantDebug  string
		wantDebugf string
		wantInfo   string
		wantInfof  string
		wantWarn   string
		wantWarnf  string
		wantError  string
		wantErrorf string
	}{
		{
			name: "Strings",
			fields: fields{
				traceID:    "1234567890",
				attributes: map[string]any{"a test key": "a test value"},
			},
			args: args{
				format: "Formatted %s",
				v:      []any{"Message"},
				v2:     "Message",
			},
			wantDebug:  "Message",
			wantDebugf: "Formatted Message",
			wantInfo:   "Message",
			wantInfof:  "Formatted Message",
			wantWarn:   "Message",
			wantWarnf:  "Formatted Message",
			wantError:  "Message",
			wantErrorf: "Formatted Message",
		},
		{
			name: "String & Error",
			fields: fields{
				traceID:    "1234567890",
				attributes: map[string]any{"test_key_1": "test_value_1", "test_key_2": "test_value_2"},
			},
			args: args{
				format: "Formatted %s",
				v:      []any{"Message"},
				v2:     errors.New("Message"),
			},
			wantDebug:  "Message",
			wantDebugf: "Formatted Message",
			wantInfo:   "Message",
			wantInfof:  "Formatted Message",
			wantWarn:   "Message",
			wantWarnf:  "Formatted Message",
			wantError:  "Message",
			wantErrorf: "Formatted Message",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, span := otel.Tracer("test tracer").Start(context.Background(), "a test")
			var buf bytes.Buffer

			l := &awsLogger{
				logger: &testSlogger{
					buf: &buf,
				},
				attributes: tt.fields.attributes,
				traceID:    tt.fields.traceID,
			}
			l.root = l

			verifyLog := func(log, methodName, expectedPrefix string, expectedLvl slog.Level) {
				if !strings.HasPrefix(log, expectedPrefix) {
					t.Errorf("awsLogger.%s() = %q, missing prefix %q", methodName, log, expectedPrefix)
				}

				expectedVals := []string{
					"trace_id=" + tt.fields.traceID,
					"span_id=" + span.SpanContext().SpanID().String(),
					"level=" + expectedLvl.String(),
				}
				for k, v := range tt.fields.attributes {
					expectedVals = append(expectedVals, slog.Any(k, v).String())
				}
				for _, v := range expectedVals {
					if !strings.Contains(log, v) {
						t.Errorf("awsLogger.%s() = %q, missing: %q", methodName, log, v)
					}
				}
			}

			l.Debug(ctx, tt.args.v2)
			verifyLog(buf.String(), "Debug", tt.wantDebug, slog.LevelDebug)
			buf.Reset()

			l.Debugf(ctx, tt.args.format, tt.args.v...)
			verifyLog(buf.String(), "Debugf", tt.wantDebugf, slog.LevelDebug)
			buf.Reset()

			l.Info(ctx, tt.args.v2)
			verifyLog(buf.String(), "Info", tt.wantInfo, slog.LevelInfo)
			buf.Reset()

			l.Infof(ctx, tt.args.format, tt.args.v...)
			verifyLog(buf.String(), "Infof", tt.wantInfof, slog.LevelInfo)
			buf.Reset()

			l.Warn(ctx, tt.args.v2)
			verifyLog(buf.String(), "Warn", tt.wantWarn, slog.LevelWarn)
			buf.Reset()

			l.Warnf(ctx, tt.args.format, tt.args.v...)
			verifyLog(buf.String(), "Warnf", tt.wantWarnf, slog.LevelWarn)
			buf.Reset()

			l.Error(ctx, tt.args.v2)
			verifyLog(buf.String(), "Error", tt.wantError, slog.LevelError)
			buf.Reset()

			l.Errorf(ctx, tt.args.format, tt.args.v...)
			verifyLog(buf.String(), "Errorf", tt.wantErrorf, slog.LevelError)
			buf.Reset()

			if l.TraceID() != tt.fields.traceID {
				t.Errorf("awsLogger.TraceID() = %v, want %v", l.TraceID(), tt.fields.traceID)
			}
		})
	}
}

func Test_awsLogger_AddRequestAttribute(t *testing.T) {
	t.Parallel()
	type fields struct {
		root        *awsLogger
		rsvdReqKeys []string
	}
	type args struct {
		key   string
		value any
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   map[string]any
	}{
		{
			name: "prefix reserved key",
			fields: fields{
				root: &awsLogger{
					record: record{reqAttributes: map[string]any{"test_key_2": "test_value_2"}},
				},
				rsvdReqKeys: []string{"test_key 1", "test_key"},
			},
			args: args{
				key:   "test_key",
				value: 512,
			},
			want: map[string]any{"test_key_2": "test_value_2", "custom_test_key": 512},
		},
		{
			name: "add request attribute (non-reserved key)",
			fields: fields{
				root: &awsLogger{
					record: record{reqAttributes: map[string]any{"test_key_2": "test_value_2"}},
				},
				rsvdReqKeys: []string{"test_key 1"},
			},
			args: args{
				key:   "test_key",
				value: 512,
			},
			want: map[string]any{"test_key_2": "test_value_2", "test_key": 512},
		},
		{
			name: "overwrite request attribute value",
			fields: fields{
				root: &awsLogger{
					record: record{reqAttributes: map[string]any{"test_key_2": "test_value_2"}},
				},
				rsvdReqKeys: []string{"test_key 1"},
			},
			args: args{
				key:   "test_key_2",
				value: 512,
			},
			want: map[string]any{"test_key_2": 512},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			l := &awsLogger{
				root:        tt.fields.root,
				rsvdReqKeys: tt.fields.rsvdReqKeys,
			}
			l.AddRequestAttribute(tt.args.key, tt.args.value)
			if diff := cmp.Diff(l.root.reqAttributes, tt.want); diff != "" {
				t.Errorf("awsLogger.AddRequestAttribute() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func Test_awsLogger_WithAttributes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		attributes map[string]any
		want       *awsAttributer
	}{
		{
			name: "with attributes success",
			attributes: map[string]any{
				"test_key_1": "test_value_1",
				"test_key_2": "test_value_2",
			},
			want: &awsAttributer{
				attributes: map[string]any{
					"test_key_1": "test_value_1",
					"test_key_2": "test_value_2",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			l := &awsLogger{
				attributes: tt.attributes,
			}
			got := l.WithAttributes()
			if diff := cmp.Diff(got, tt.want, cmp.AllowUnexported(awsAttributer{}), cmpopts.IgnoreFields(awsAttributer{}, "logger")); diff != "" {
				t.Errorf("awsLogger.WithAttributes() mismatch (-want +got):\n%s", diff)
			}
			if a, ok := got.(*awsAttributer); !ok {
				t.Errorf("awsLogger.WithAttributes() type %T, want %T", got, &awsAttributer{})
			} else if a.logger != l {
				t.Errorf("awsLogger.WithAttributes().logger != awsLogger")
			}
		})
	}
}

func Test_awsAttributer_AddAttribute(t *testing.T) {
	t.Parallel()
	type args struct {
		key   string
		value any
	}
	tests := []struct {
		name       string
		args       args
		rsvdKeys   []string
		attributes map[string]any
		want       map[string]any
	}{
		{
			name: "prefixing reserved key",
			args: args{
				key:   "test_key_0",
				value: 512,
			},
			rsvdKeys: []string{"test_key 0", "test_key_0"},
			attributes: map[string]any{
				"test_key_1": 1,
				"test_key_2": "test_value_2",
			},
			want: map[string]any{
				"test_key_1":        1,
				"test_key_2":        "test_value_2",
				"custom_test_key_0": 512,
			},
		},
		{
			name: "add attribute (non-reserved key)",
			args: args{
				key:   "test_key_0",
				value: 512,
			},
			rsvdKeys: []string{"test_key 0"},
			attributes: map[string]any{
				"test_key_1": 1,
				"test_key_2": "test_value_2",
			},
			want: map[string]any{
				"test_key_1": 1,
				"test_key_2": "test_value_2",
				"test_key_0": 512,
			},
		},
		{
			name: "overwriting attribute value",
			args: args{
				key:   "test_key_1",
				value: 512,
			},
			rsvdKeys: []string{"test_key 0"},
			attributes: map[string]any{
				"test_key_1": 1,
				"test_key_2": "test_value_2",
			},
			want: map[string]any{
				"test_key_1": 512,
				"test_key_2": "test_value_2",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := &awsAttributer{
				attributes: tt.attributes,
				logger:     &awsLogger{rsvdKeys: tt.rsvdKeys},
			}
			a.AddAttribute(tt.args.key, tt.args.value)
			if diff := cmp.Diff(a.attributes, tt.want); diff != "" {
				t.Errorf("awsAttributer.AddAttribute() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func Test_awsAttributer_Logger(t *testing.T) {
	t.Parallel()
	type fields struct {
		logger     *awsLogger
		attributes map[string]any
	}
	tests := []struct {
		name   string
		fields fields
		want   *awsLogger
	}{
		{
			name: "success getting logger",
			fields: fields{
				logger: &awsLogger{
					root: &awsLogger{
						traceID: "root trace id",
					},
					logger:      &testSlogger{},
					traceID:     "1234567890",
					rsvdKeys:    []string{"test reserved key 1", "test reserved key 2"},
					rsvdReqKeys: []string{"test reserved request key 1", "test reserved request key 2"},
					attributes:  map[string]any{"test_key_1": "test_value_1", "test_key_2": "test_value_2"},
					record: record{
						maxSeverity:   logging.Warning,
						logCount:      2,
						reqAttributes: map[string]any{"test_req_key_1": "test_req_value_1", "test_req_key_2": "test_req_value_2"},
					},
				},
				attributes: map[string]any{"test_key_3": "test_value_3", "test_key_4": "test_value_4"},
			},
			want: &awsLogger{
				root: &awsLogger{
					traceID: "root trace id",
				},
				traceID:     "1234567890",
				rsvdKeys:    []string{"test reserved key 1", "test reserved key 2"},
				rsvdReqKeys: []string{"test reserved request key 1", "test reserved request key 2"},
				attributes:  map[string]any{"test_key_3": "test_value_3", "test_key_4": "test_value_4"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := &awsAttributer{
				logger:     tt.fields.logger,
				attributes: tt.fields.attributes,
			}

			got := a.Logger()
			if diff := cmp.Diff(got, tt.want, cmp.AllowUnexported(awsLogger{}, record{}, Policy{}), cmpopts.IgnoreFields(awsLogger{}, "record.mu", "logger")); diff != "" {
				t.Errorf("awsAttributer.Logger() mismatch (-want +got):\n%s", diff)
			}
			gotAwsLogger, ok := got.(*awsLogger)
			if !ok {
				t.Errorf("awsAttributer.Logger() type %T, want %T", got, &awsLogger{})
				return
			}
			if gotAwsLogger.logger != a.logger.logger {
				t.Errorf("got awsLogger.logger is NOT the original logger")
			}
		})
	}
}

type testSlogger struct {
	buf *bytes.Buffer
}

func (t *testSlogger) LogAttrs(_ context.Context, level slog.Level, msg string, attrs ...slog.Attr) {
	_, _ = fmt.Fprint(t.buf, msg, "level="+level.String(), attrs)
}

type captureSLogger struct {
	ctx   context.Context
	level slog.Level
	msg   string
	attrs []slog.Attr
	calls int
}

func (c *captureSLogger) LogAttrs(ctx context.Context, level slog.Level, msg string, attrs ...slog.Attr) {
	c.calls++
	c.ctx = ctx
	c.level = level
	c.msg = msg
	c.attrs = attrs
}
