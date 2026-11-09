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
	"github.com/go-test/deep"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestNewGoogleCloudExporter(t *testing.T) {
	t.Parallel()

	type args struct {
		client    *logging.Client
		projectID string
		opts      []logging.LoggerOption
	}
	tests := []struct {
		name string
		args args
		want *GoogleCloudExporter
	}{
		{
			name: "Simple Constructor",
			args: args{
				client:    &logging.Client{},
				projectID: "My Project ID",
				opts:      []logging.LoggerOption{logging.ConcurrentWriteLimit(5)},
			},
			want: &GoogleCloudExporter{
				projectID: "My Project ID",
				client:    &logging.Client{},
				opts:      []logging.LoggerOption{logging.ConcurrentWriteLimit(5)},
				policy:    Always(),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := NewGoogleCloudExporter(tt.args.client, tt.args.projectID, tt.args.opts...)
			if diff := cmp.Diff(got, tt.want, cmp.AllowUnexported(GoogleCloudExporter{}, Policy{}, logging.Client{}), cmpopts.IgnoreFields(logging.Client{}, "client", "loggers", "mu")); diff != "" {
				t.Errorf("NewGoogleCloudExporter() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestGoogleCloudExporter_LogAll(t *testing.T) {
	t.Parallel()
	type fields struct {
		policy Policy
	}
	type args struct {
		v bool
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   *GoogleCloudExporter
	}{
		{
			name: "logAll=true is the always policy",
			fields: fields{
				policy: OnEvent(),
			},
			args: args{
				v: true,
			},
			want: &GoogleCloudExporter{
				policy: Always(),
			},
		},
		{
			name: "logAll=false is the on event policy",
			fields: fields{
				policy: Always(),
			},
			args: args{
				v: false,
			},
			want: &GoogleCloudExporter{
				policy: OnEvent(),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := &GoogleCloudExporter{
				policy: tt.fields.policy,
			}
			got := e.LogAll(tt.args.v)
			if diff := cmp.Diff(got, tt.want, cmp.AllowUnexported(GoogleCloudExporter{}, Policy{})); diff != "" {
				t.Errorf("GoogleCloudExporter.LogAll() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestGoogleCloudExporter_Middleware(t *testing.T) {
	disableMetaServertest(t)

	type fields struct {
		projectID string
		client    *logging.Client
		opts      []logging.LoggerOption
		policy    Policy
	}
	tests := []struct {
		name   string
		fields fields
		want   func(http.Handler) http.Handler
	}{
		{
			name: "call Middleware",
			fields: fields{
				projectID: "My other project",
				client:    &logging.Client{},
				opts:      []logging.LoggerOption{logging.ConcurrentWriteLimit(5)},
				policy:    OnEvent(),
			},
			want: func(next http.Handler) http.Handler {
				client := &logging.Client{}
				opts := []logging.LoggerOption{logging.ConcurrentWriteLimit(5)}

				return &gcpHandler{
					next:         next,
					parentLogger: client.Logger("request_parent_log", opts...),
					childLogger:  client.Logger("request_child_log", opts...),
					projectID:    "My other project",
					policy:       OnEvent(),
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
			e := &GoogleCloudExporter{
				projectID: tt.fields.projectID,
				client:    tt.fields.client,
				opts:      tt.fields.opts,
				policy:    tt.fields.policy,
			}
			got := e.Middleware()(next)
			if diff := deep.Equal(got, tt.want(next)); diff != nil {
				t.Errorf("GoogleCloudExporter.Middleware() = %v", diff)
			}
		})
	}
}

func TestGoogleCloudExporter_CliRunner(t *testing.T) {
	disableMetaServertest(t)

	type fields struct {
		projectID string
		client    *logging.Client
	}
	tests := []struct {
		name   string
		fields fields
	}{
		{
			name: "runner from the exporter",
			fields: fields{
				projectID: "My project",
				client:    &logging.Client{},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &GoogleCloudExporter{
				projectID: tt.fields.projectID,
				client:    tt.fields.client,
			}
			if got := e.CliRunner(); got == nil {
				t.Errorf("GoogleCloudExporter.CliRunner() returned nil")
			}
		})
	}
}

func Test_gcpRunner_run(t *testing.T) {
	t.Parallel()

	type args struct {
		lines    int
		severity logging.Severity
		err      error
		draw     float64
	}
	tests := []struct {
		name         string
		policy       Policy
		args         args
		wantParent   bool
		wantSeverity logging.Severity
		wantChildren int
	}{
		{
			name:       "always, quiet run",
			policy:     Always(),
			wantParent: true,
		},
		{
			name:       "always, failed run",
			policy:     Always(),
			args:       args{err: errors.New("failed")},
			wantParent: true,
		},
		{
			name:   "on event, quiet run",
			policy: OnEvent(),
		},
		{
			name:         "on event, a line attached",
			policy:       OnEvent(),
			args:         args{lines: 1, severity: logging.Warning},
			wantParent:   true,
			wantSeverity: logging.Warning,
			wantChildren: 1,
		},
		{
			name:       "on event, failed run",
			policy:     OnEvent(),
			args:       args{err: errors.New("failed")},
			wantParent: true,
		},
		{
			name:       "sampled, the draw hits",
			policy:     Sampled(0.5),
			args:       args{draw: 0.25},
			wantParent: true,
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
		},
		{
			name:         "never, failed run with an error line",
			policy:       Never(),
			args:         args{lines: 1, severity: logging.Error, err: errors.New("failed")},
			wantChildren: 1,
		},
		{
			name:   "floor drops the line, so nothing attached",
			policy: OnEvent().MinSeverity(logging.Warning),
			args:   args{lines: 1, severity: logging.Info},
		},
		{
			name:         "floor admits the line",
			policy:       OnEvent().MinSeverity(logging.Warning),
			args:         args{lines: 1, severity: logging.Error},
			wantParent:   true,
			wantSeverity: logging.Error,
			wantChildren: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			parent, child := &captureLogger{}, &captureLogger{}
			runner := &gcpRunner{parentLogger: parent, childLogger: child, projectID: "my-project", policy: tt.policy}
			err := runner.run(t.Context(), "gcp-command --flag", func(ctx context.Context) error {
				gcpLgr, ok := FromCtx(ctx).lg.(*gcpLogger)
				if !ok {
					t.Fatal("Failed to get gcpLogger from context")
				}
				gcpLgr.draw = func() float64 {
					return tt.args.draw
				}
				FromCtx(ctx).AddRequestAttribute("test_gcp_key", "test_gcp_value")
				logLines(ctx, tt.args.lines, tt.args.severity)

				return tt.args.err
			})
			if !errors.Is(err, tt.args.err) {
				t.Errorf("gcpRunner.run() error = %v, want %v", err, tt.args.err)
			}
			if got := parent.calls == 1; got != tt.wantParent {
				t.Fatalf("parent entry written = %v, want %v", got, tt.wantParent)
			}
			if child.calls != tt.wantChildren {
				t.Errorf("child entries = %d, want %d", child.calls, tt.wantChildren)
			}
			if !tt.wantParent {
				return
			}
			if parent.e.Severity != tt.wantSeverity {
				t.Errorf("Severity = %v, want %v", parent.e.Severity, tt.wantSeverity)
			}
			pl, ok := parent.e.Payload.(map[string]any)
			if !ok {
				t.Fatalf("Payload type %T, want map[string]any", parent.e.Payload)
			}
			if pl["command"] != "gcp-command --flag" || pl["test_gcp_key"] != "test_gcp_value" {
				t.Errorf("Payload = %v, missing the command or the request attribute", pl)
			}
		})
	}
}

func Test_gcpHandler_ServeHTTP(t *testing.T) {
	t.Parallel()

	type args struct {
		status   int
		lines    int
		severity logging.Severity
		draw     float64
	}
	tests := []struct {
		name         string
		policy       Policy
		args         args
		wantParent   bool
		wantSeverity logging.Severity
		wantChildren int
	}{
		{
			name:       "always, quiet request",
			policy:     Always(),
			args:       args{status: http.StatusOK},
			wantParent: true,
		},
		{
			name:         "always, a line attached",
			policy:       Always(),
			args:         args{status: http.StatusOK, lines: 1, severity: logging.Info},
			wantParent:   true,
			wantSeverity: logging.Info,
			wantChildren: 1,
		},
		{
			name:         "always, a 500 raises the entry to error",
			policy:       Always(),
			args:         args{status: http.StatusInternalServerError},
			wantParent:   true,
			wantSeverity: logging.Error,
		},
		{
			name:   "on event, quiet request",
			policy: OnEvent(),
			args:   args{status: http.StatusOK},
		},
		{
			name:         "on event, a line attached",
			policy:       OnEvent(),
			args:         args{status: http.StatusOK, lines: 1, severity: logging.Warning},
			wantParent:   true,
			wantSeverity: logging.Warning,
			wantChildren: 1,
		},
		{
			name:       "on event, a 404 is an event",
			policy:     OnEvent(),
			args:       args{status: http.StatusNotFound},
			wantParent: true,
		},
		{
			name:         "on event, a 500 is an event, raised to error",
			policy:       OnEvent(),
			args:         args{status: http.StatusInternalServerError},
			wantParent:   true,
			wantSeverity: logging.Error,
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
		},
		{
			name:         "never, a 500 with an error line",
			policy:       Never(),
			args:         args{status: http.StatusInternalServerError, lines: 1, severity: logging.Error},
			wantChildren: 1,
		},
		{
			name:   "floor drops the line, so nothing attached",
			policy: OnEvent().MinSeverity(logging.Warning),
			args:   args{status: http.StatusOK, lines: 1, severity: logging.Info},
		},
		{
			name:         "floor admits the line",
			policy:       OnEvent().MinSeverity(logging.Warning),
			args:         args{status: http.StatusOK, lines: 1, severity: logging.Error},
			wantParent:   true,
			wantSeverity: logging.Error,
			wantChildren: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var handlerCalled bool
			var traceID string
			parent, child := &captureLogger{}, &captureLogger{}
			handler := &gcpHandler{
				parentLogger: parent,
				childLogger:  child,
				projectID:    "my-big-project",
				policy:       tt.policy,
				next: http.HandlerFunc(
					func(w http.ResponseWriter, r *http.Request) {
						gcpLgr, ok := FromReq(r).lg.(*gcpLogger)
						if !ok {
							t.Fatalf("Req() = %v, wanted: %T", gcpLgr, &gcpLogger{})
						}
						gcpLgr.draw = func() float64 {
							return tt.args.draw
						}
						traceID = gcpLgr.traceID
						gcpLgr.reqAttributes["test_key_1"] = "test_value_1"
						gcpLgr.reqAttributes["test_key_2"] = "test_value_2"
						logLines(r.Context(), tt.args.lines, tt.args.severity)

						w.WriteHeader(tt.args.status)
						handlerCalled = true
					},
				),
			}

			w := httptest.NewRecorder()
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
			handler.ServeHTTP(w, r)

			if !handlerCalled {
				t.Fatal("Failed to call handler")
			}
			if got := parent.calls == 1; got != tt.wantParent {
				t.Fatalf("parent entry written = %v, want %v", got, tt.wantParent)
			}
			if child.calls != tt.wantChildren {
				t.Errorf("child entries = %d, want %d", child.calls, tt.wantChildren)
			}
			if !tt.wantParent {
				return
			}
			if parent.e.Severity != tt.wantSeverity {
				t.Errorf("Severity = %v, want %v", parent.e.Severity, tt.wantSeverity)
			}
			if parent.e.Trace != traceID {
				t.Errorf("Trace = %v, want %v", parent.e.Trace, traceID)
			}

			wantPayload := map[string]any{
				"message":    "Parent Log Entry",
				"test_key_1": "test_value_1",
				"test_key_2": "test_value_2",
			}
			if diff := cmp.Diff(parent.e.Payload, wantPayload); diff != "" {
				t.Errorf("Payload mismatch (-want +got):\n%s", diff)
			}

			if parent.e.HTTPRequest.Status != tt.args.status {
				t.Errorf("Status = %v, want %v", parent.e.HTTPRequest.Status, tt.args.status)
			}
		})
	}
}

func Test_gcpTraceIDFromRequest(t *testing.T) {
	t.Parallel()
	type args struct {
		mockReq   func(traceStr string) (*http.Request, string)
		projectID string
		traceStr  string
	}
	tests := []struct {
		name            string
		args            args
		wantTracePrefix string
		wantTraceStr    string
	}{
		// The order these are significant
		{
			// This test relies on the global tracing provider NOT being set
			name: "no trace in request",
			args: args{
				mockReq: func(wantTraceStr string) (*http.Request, string) {
					return &http.Request{URL: &url.URL{}}, wantTraceStr
				},
				projectID: "my-project",
				traceStr:  "105445aa7843bc8bf206b12000100000",
			},
			wantTracePrefix: "projects/my-project/traces/",
			wantTraceStr:    "105445aa7843bc8bf206b12000100000",
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
				projectID: "my-project",
			},
			wantTracePrefix: "projects/my-project/traces/",
		},
		{
			// With the global tracing provider set, this test shows that
			// trace Propagation is a higher priority then trace in request context
			name: "with propagation span in headers",
			args: args{
				mockReq: func(wantTraceStr string) (r *http.Request, traceStr string) {
					r = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
					r.Header.Add("X-Cloud-Trace-Context", wantTraceStr+"/1;o=1")

					return r, wantTraceStr
				},
				projectID: "my-project",
			},
			wantTracePrefix: "projects/my-project/traces/",
			wantTraceStr:    "105445aa7843bc8bf206b12000100000",
		},
		{
			name: "with propagation span in headers without span ID",
			args: args{
				mockReq: func(wantTraceStr string) (r *http.Request, traceStr string) {
					r = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
					r.Header.Add("X-Cloud-Trace-Context", wantTraceStr)

					return r, wantTraceStr
				},
				projectID: "my-project",
			},
			wantTracePrefix: "projects/my-project/traces/",
			wantTraceStr:    "105445aa7843bc8bf206b12000100000",
		},
		{
			name: "with malformed propagation header",
			args: args{
				mockReq: func(wantTraceStr string) (r *http.Request, traceStr string) {
					r = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
					r.Header.Add("X-Cloud-Trace-Context", "not-a-trace-id/1;o=1")

					return r, wantTraceStr
				},
				projectID: "my-project",
				traceStr:  "105445aa7843bc8bf206b12000100000",
			},
			wantTracePrefix: "projects/my-project/traces/",
			wantTraceStr:    "105445aa7843bc8bf206b12000100000",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r, traceStr := tt.args.mockReq(tt.wantTraceStr)
			want := tt.wantTracePrefix + traceStr

			if got := gcpTraceIDFromRequest(r, tt.args.projectID, func() string { return tt.args.traceStr }); got != want {
				t.Errorf("gcpTraceIDFromRequest() = %v, want %v", got, want)
			}
		})
	}
}

func Test_newGCPLogger(t *testing.T) {
	t.Parallel()

	type args struct {
		lg      *logging.Logger
		traceID string
		policy  Policy
	}
	tests := []struct {
		name string
		args args
		want ctxLogger
	}{
		{
			name: "new",
			args: args{
				lg:      &logging.Logger{},
				traceID: "hello",
				policy:  OnEvent(),
			},
			want: &gcpLogger{
				record:     record{policy: OnEvent(), reqAttributes: map[string]any{}},
				logger:     &logging.Logger{},
				traceID:    "hello",
				rsvdKeys:   []string{"message"},
				attributes: map[string]any{},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := newGCPLogger(tt.args.lg, tt.args.traceID, tt.args.policy)
			if diff := cmp.Diff(got, tt.want, cmp.AllowUnexported(gcpLogger{}, record{}, Policy{}), cmpopts.IgnoreFields(gcpLogger{}, "logger", "record.mu", "root")); diff != "" {
				t.Errorf("newGCPLogger() mismatch (-want +got):\n%s", diff)
			}
			if got.root != got {
				t.Errorf("newGCPLogger().root is not self")
			}
		})
	}
}

func Test_gcpLogger(t *testing.T) {
	t.Parallel()

	type fields struct {
		attributes map[string]any
		traceID    string
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
				attributes: map[string]any{"a test key": "a test value"},
				traceID:    "123987",
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
				attributes: map[string]any{"test_key_1": "test_value_1", "test_key_2": "test_value_2"},
				traceID:    "987123",
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

			l := &gcpLogger{
				logger: &testLogger{
					buf: &buf,
				},
				attributes: tt.fields.attributes,
				traceID:    tt.fields.traceID,
			}
			l.root = l

			verifyOutput := func(output, methodName, expectedMsgVal string, expectedSeverity logging.Severity) {
				expectedVals := []string{
					"message=" + expectedMsgVal,
					"trace=" + tt.fields.traceID,
					"severity=" + expectedSeverity.String(),
					"span=" + span.SpanContext().SpanID().String(),
					"trace_sampled=" + fmt.Sprint(span.SpanContext().IsSampled()),
				}
				for k, v := range tt.fields.attributes {
					expectedVals = append(expectedVals, slog.Any(k, v).String())
				}
				for _, v := range expectedVals {
					if !strings.Contains(output, v) {
						t.Errorf("gcpLogger.%s() = %q, missing: %q", methodName, output, v)
					}
				}
			}

			l.Debug(ctx, tt.args.v2)
			verifyOutput(buf.String(), "Debug", tt.wantDebug, logging.Debug)
			buf.Reset()

			l.Debugf(ctx, tt.args.format, tt.args.v...)
			verifyOutput(buf.String(), "Debugf", tt.wantDebugf, logging.Debug)
			buf.Reset()

			l.Info(ctx, tt.args.v2)
			verifyOutput(buf.String(), "Info", tt.wantInfo, logging.Info)
			buf.Reset()

			l.Infof(ctx, tt.args.format, tt.args.v...)
			verifyOutput(buf.String(), "Infof", tt.wantInfof, logging.Info)
			buf.Reset()

			l.Warn(ctx, tt.args.v2)
			verifyOutput(buf.String(), "Warn", tt.wantWarn, logging.Warning)
			buf.Reset()

			l.Warnf(ctx, tt.args.format, tt.args.v...)
			verifyOutput(buf.String(), "Warnf", tt.wantWarnf, logging.Warning)
			buf.Reset()

			l.Error(ctx, tt.args.v2)
			verifyOutput(buf.String(), "Error", tt.wantError, logging.Error)
			buf.Reset()

			l.Errorf(ctx, tt.args.format, tt.args.v...)
			verifyOutput(buf.String(), "Errorf", tt.wantErrorf, logging.Error)
			buf.Reset()

			if l.TraceID() != tt.fields.traceID {
				t.Errorf("TraceID() = %v, want %v", l.TraceID(), tt.fields.traceID)
			}
		})
	}
}

func Test_gcpLogger_AddRequestAttribute(t *testing.T) {
	t.Parallel()
	type fields struct {
		root     *gcpLogger
		rsvdKeys []string
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
				root: &gcpLogger{
					record: record{reqAttributes: map[string]any{"test_key_2": "test_value_2"}},
				},
				rsvdKeys: []string{"test_key 1", "test_key"},
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
				root: &gcpLogger{
					record: record{reqAttributes: map[string]any{"test_key_2": "test_value_2"}},
				},
				rsvdKeys: []string{"test_key 1"},
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
				root: &gcpLogger{
					record: record{reqAttributes: map[string]any{"test_key_2": "test_value_2"}},
				},
				rsvdKeys: []string{"test_key 1"},
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
			l := &gcpLogger{
				root:     tt.fields.root,
				rsvdKeys: tt.fields.rsvdKeys,
			}
			l.AddRequestAttribute(tt.args.key, tt.args.value)
			if diff := cmp.Diff(l.root.reqAttributes, tt.want); diff != "" {
				t.Errorf("gcpLogger.AddRequestAttribute() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func Test_gcpLogger_WithAttributes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		attributes map[string]any
		want       *gcpAttributer
	}{
		{
			name: "with attributes success",
			attributes: map[string]any{
				"test_key_1": "test_value_1",
				"test_key_2": "test_value_2",
			},
			want: &gcpAttributer{
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
			l := &gcpLogger{
				attributes: tt.attributes,
			}
			got := l.WithAttributes()
			if diff := cmp.Diff(got, tt.want, cmp.AllowUnexported(gcpAttributer{}), cmpopts.IgnoreFields(gcpAttributer{}, "logger")); diff != "" {
				t.Errorf("gcpLogger.WithAttributes() mismatch (-want +got):\n%s", diff)
			}
			if a, ok := got.(*gcpAttributer); !ok {
				t.Errorf("gcpLogger.WithAttributes() type %T, want %T", got, &gcpAttributer{})
			} else if a.logger != l {
				t.Errorf("gcpLogger.WithAttributes().logger != gcpLogger")
			}
		})
	}
}

func Test_gcpAttributer_AddAttribute(t *testing.T) {
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
			name: "prefix reserved key",
			args: args{
				key:   "test_key_0",
				value: "test_value_0",
			},
			rsvdKeys: []string{"test_key 0", "test_key_0"},
			attributes: map[string]any{
				"test_key_1": 1,
				"test_key_2": "test_value_2",
			},
			want: map[string]any{
				"test_key_1":        1,
				"test_key_2":        "test_value_2",
				"custom_test_key_0": "test_value_0",
			},
		},
		{
			name: "add attribute (non-reserved key)",
			args: args{
				key:   "test_key_0",
				value: "test_value_0",
			},
			rsvdKeys: []string{"test_key 0"},
			attributes: map[string]any{
				"test_key_1": 1,
				"test_key_2": "test_value_2",
			},
			want: map[string]any{
				"test_key_1": 1,
				"test_key_2": "test_value_2",
				"test_key_0": "test_value_0",
			},
		},
		{
			name: "overwrite attribute value",
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
			a := &gcpAttributer{
				attributes: tt.attributes,
				logger:     &gcpLogger{rsvdKeys: tt.rsvdKeys},
			}
			a.AddAttribute(tt.args.key, tt.args.value)
			if diff := cmp.Diff(a.attributes, tt.want); diff != "" {
				t.Errorf("gcpAttributer.AddAttribute() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func Test_gcpAttributer_Logger(t *testing.T) {
	t.Parallel()
	type fields struct {
		logger     *gcpLogger
		attributes map[string]any
	}
	tests := []struct {
		name   string
		fields fields
		want   *gcpLogger
	}{
		{
			name: "success getting logger",
			fields: fields{
				logger: &gcpLogger{
					root: &gcpLogger{
						traceID: "root trace id",
					},
					logger:     &testLogger{},
					traceID:    "1234567890",
					rsvdKeys:   []string{"test reserved key 1", "test reserved key 2"},
					attributes: map[string]any{"test_key_1": "test_value_1", "test_key_2": "test_value_2"},
					record: record{
						maxSeverity:   logging.Warning,
						logCount:      2,
						reqAttributes: map[string]any{"test_req_key_1": "test_req_value_1", "test_req_key_2": "test_req_value_2"},
					},
				},
				attributes: map[string]any{"test_key_3": "test_value_3", "test_key_4": "test_value_4"},
			},
			want: &gcpLogger{
				root: &gcpLogger{
					traceID: "root trace id",
				},
				traceID:    "1234567890",
				rsvdKeys:   []string{"test reserved key 1", "test reserved key 2"},
				attributes: map[string]any{"test_key_3": "test_value_3", "test_key_4": "test_value_4"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := &gcpAttributer{
				logger:     tt.fields.logger,
				attributes: tt.fields.attributes,
			}

			got := a.Logger()
			if diff := cmp.Diff(got, tt.want, cmp.AllowUnexported(gcpLogger{}, record{}, Policy{}), cmpopts.IgnoreFields(gcpLogger{}, "record.mu", "logger")); diff != "" {
				t.Errorf("gcpAttributer.Logger() mismatch (-want +got):\n%s", diff)
			}
			gotGcpLogger, ok := got.(*gcpLogger)
			if !ok {
				t.Errorf("gcpAttributer.Logger() type %T, want %T", got, &gcpLogger{})
				return
			}
			if gotGcpLogger.logger != a.logger.logger {
				t.Errorf("got gcpLogger.logger is NOT the original logger")
			}
		})
	}
}

func disableMetaServertest(t *testing.T) {
	t.Helper()

	// Fix issue when logging.Client attempts to detect its
	// env by querying GCE_METADATA_HOST and nothing is there
	// so your test is very slow. This tries to causes the
	// detection to fail faster and not hang your test so long
	curEnv := os.Getenv("GCE_METADATA_HOST")
	t.Cleanup(func() { _ = os.Setenv("GCE_METADATA_HOST", curEnv) })
	_ = os.Setenv("GCE_METADATA_HOST", "localhost")
}

type testLogger struct {
	buf *bytes.Buffer
}

func (t *testLogger) Log(e logging.Entry) {
	var logStr strings.Builder
	logStr.WriteString("trace=" + e.Trace + " severity=" + e.Severity.String() + " span=" + e.SpanID + " trace_sampled=" + fmt.Sprint(e.TraceSampled))
	attrs, ok := e.Payload.(map[string]any)
	if ok {
		for k, v := range attrs {
			vStr, ok := v.(string)
			if ok {
				logStr.WriteString(" " + k + "=" + vStr)
			}
		}
	}
	_, _ = t.buf.WriteString(logStr.String())
}

type captureLogger struct {
	e     logging.Entry
	calls int
}

func (c *captureLogger) Log(e logging.Entry) {
	c.calls++
	c.e = e
}
