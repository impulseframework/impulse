package logger

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"

	"cloud.google.com/go/logging"
	"github.com/go-test/deep"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func TestNewConsoleExporter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want *ConsoleExporter
	}{
		{
			name: "Simple Constructor",
			want: &ConsoleExporter{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := NewConsoleExporter(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NewConsoleExporter() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConsoleExporter_NoColor(t *testing.T) {
	t.Parallel()

	type fields struct {
		noColor bool
	}
	type args struct {
		v bool
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   *ConsoleExporter
	}{
		{
			name: "noColor=true",
			fields: fields{
				noColor: false,
			},
			args: args{
				v: true,
			},
			want: &ConsoleExporter{
				noColor: true,
			},
		},
		{
			name: "noColor=false",
			fields: fields{
				noColor: true,
			},
			args: args{
				v: false,
			},
			want: &ConsoleExporter{
				noColor: false,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := &ConsoleExporter{
				noColor: tt.fields.noColor,
			}
			if got := e.NoColor(tt.args.v); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ConsoleExporter.NoColor() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConsoleExporter_Middleware(t *testing.T) {
	t.Parallel()

	type fields struct {
		noColor bool
	}
	tests := []struct {
		name   string
		fields fields
		want   func(http.Handler) http.Handler
	}{
		{
			name: "call Middleware",
			fields: fields{
				noColor: true,
			},
			want: func(next http.Handler) http.Handler {
				return &consoleHandler{
					next:    next,
					noColor: true,
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
			e := &ConsoleExporter{
				noColor: tt.fields.noColor,
			}
			got := e.Middleware()(next)
			if diff := deep.Equal(got, tt.want(next)); diff != nil {
				t.Errorf("ConsoleExporter.Middleware() = %v", diff)
			}
		})
	}
}

func TestConsoleExporter_CliRunner(t *testing.T) {
	type fields struct {
		noColor bool
	}
	tests := []struct {
		name         string
		fields       fields
		opts         []RequestLoggerOption
		command      string
		fn           func(context.Context) error
		wantContains []string
		wantAbsent   []string
		wantErr      bool
	}{
		{
			name: "call CliRunner",
			fields: fields{
				noColor: true,
			},
			command: "my-command --flag",
			fn: func(c context.Context) error {
				logCtx := FromCtx(c)
				logCtx.Info("test info log")
				logCtx.AddRequestAttribute("test_key", "test_value")

				return nil
			},
			wantContains: []string{
				"INFO : test info log",
				"] my-command --flag",
				"test_key=test_value",
			},
		},
		{
			name: "call CliRunner with error",
			fields: fields{
				noColor: true,
			},
			command: "my-error-command --flag",
			fn: func(c context.Context) error {
				logCtx := FromCtx(c)
				err := errors.New("an error occurred")
				// simulate ignoring the error without explicitly logging it as Error level
				logCtx.Info(err.Error())

				return err
			},
			wantContains: []string{
				"INFO : an error occurred",
				"] my-error-command --flag",
			},
			wantErr: true,
		},
		{
			name: "on event, a quiet run writes no parent entry",
			fields: fields{
				noColor: true,
			},
			opts:    []RequestLoggerOption{DefaultPolicy(OnEvent())},
			command: "my-quiet-command",
			fn: func(_ context.Context) error {
				return nil
			},
			wantAbsent: []string{"my-quiet-command"},
		},
		{
			name: "on event, a line attached writes the parent entry",
			fields: fields{
				noColor: true,
			},
			opts:    []RequestLoggerOption{DefaultPolicy(OnEvent())},
			command: "my-chatty-command",
			fn: func(c context.Context) error {
				FromCtx(c).Warn("something to say")

				return nil
			},
			wantContains: []string{
				"WARN : something to say",
				"WARN : CLI [",
				"] my-chatty-command logCount=1",
			},
		},
		{
			name: "on event, a failed run writes the parent entry",
			fields: fields{
				noColor: true,
			},
			opts:    []RequestLoggerOption{DefaultPolicy(OnEvent())},
			command: "my-failing-command",
			fn: func(_ context.Context) error {
				return errors.New("failed")
			},
			wantContains: []string{
				"INFO : CLI [",
				"] my-failing-command logCount=0",
			},
			wantErr: true,
		},
		{
			name: "never writes nothing, even on a failed run with a line",
			fields: fields{
				noColor: true,
			},
			opts:    []RequestLoggerOption{DefaultPolicy(Never())},
			command: "my-silent-command",
			fn: func(c context.Context) error {
				FromCtx(c).Error("loud but unwritten as a parent")

				return errors.New("failed")
			},
			wantContains: []string{"ERROR: loud but unwritten as a parent"},
			wantAbsent:   []string{"my-silent-command"},
			wantErr:      true,
		},
		{
			name: "the floor drops a line below it",
			fields: fields{
				noColor: true,
			},
			opts:    []RequestLoggerOption{DefaultPolicy(Always().MinSeverity(logging.Warning))},
			command: "my-filtered-command",
			fn: func(c context.Context) error {
				FromCtx(c).Info("dropped")
				FromCtx(c).Error("kept")

				return nil
			},
			wantContains: []string{
				"ERROR: kept",
				"] my-filtered-command logCount=1",
			},
			wantAbsent: []string{"dropped"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			log.SetOutput(&buf)
			t.Cleanup(func() {
				log.SetOutput(os.Stderr)
			})

			e := &ConsoleExporter{
				noColor: tt.fields.noColor,
			}

			runner := NewCliLogger(e, tt.opts...)
			ctx := context.Background()

			err := runner(ctx, tt.command, tt.fn)
			if (err != nil) != tt.wantErr {
				t.Errorf("ConsoleExporter.CliRunner() error = %v, wantErr %v", err, tt.wantErr)
			}

			output := buf.String()
			for _, want := range tt.wantContains {
				if !strings.Contains(output, want) {
					t.Errorf("Missing %q in output: %v", want, output)
				}
			}
			for _, absent := range tt.wantAbsent {
				if strings.Contains(output, absent) {
					t.Errorf("Unexpected %q in output: %v", absent, output)
				}
			}
		})
	}
}

func Test_consoleHandler_ServeHTTP(t *testing.T) {
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
		wantParent string // how the parent line starts, after the log package's time prefix; empty when none is expected
		wantLines  int
	}{
		{
			name:       "always, quiet request",
			policy:     Always(),
			args:       args{status: http.StatusOK},
			wantParent: "INFO : GET / 200",
		},
		{
			name:       "always, a line attached",
			policy:     Always(),
			args:       args{status: http.StatusOK, lines: 1, severity: logging.Warning},
			wantParent: "WARN : GET / 200",
			wantLines:  1,
		},
		{
			name:       "always, a 500 raises the entry to error",
			policy:     Always(),
			args:       args{status: http.StatusInternalServerError},
			wantParent: "ERROR: GET / 500",
		},
		{
			name:   "on event, quiet request",
			policy: OnEvent(),
			args:   args{status: http.StatusOK},
		},
		{
			name:       "on event, a line attached",
			policy:     OnEvent(),
			args:       args{status: http.StatusOK, lines: 1, severity: logging.Info},
			wantParent: "INFO : GET / 200",
			wantLines:  1,
		},
		{
			name:       "on event, a 404 is an event",
			policy:     OnEvent(),
			args:       args{status: http.StatusNotFound},
			wantParent: "INFO : GET / 404",
		},
		{
			name:       "on event, a 500 is an event, raised to error",
			policy:     OnEvent(),
			args:       args{status: http.StatusInternalServerError},
			wantParent: "ERROR: GET / 500",
		},
		{
			name:       "sampled, the draw hits",
			policy:     Sampled(0.5),
			args:       args{status: http.StatusOK, draw: 0.25},
			wantParent: "INFO : GET / 200",
		},
		{
			name:   "sampled, the draw misses",
			policy: Sampled(0.5),
			args:   args{status: http.StatusOK, draw: 0.75},
		},
		{
			name:      "never, a 500 with an error line",
			policy:    Never(),
			args:      args{status: http.StatusInternalServerError, lines: 1, severity: logging.Error},
			wantLines: 1,
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
			wantParent: "ERROR: GET / 200",
			wantLines:  1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			log.SetOutput(&buf)
			t.Cleanup(func() {
				log.SetOutput(os.Stderr)
			})

			var handlerCalled bool
			handler := &consoleHandler{
				noColor: true,
				next: http.HandlerFunc(
					func(w http.ResponseWriter, r *http.Request) {
						l, ok := FromReq(r).lg.(*consoleLogger)
						if !ok {
							t.Fatal("Failed to get consoleLogger from request")
						}
						l.SetPolicy(tt.policy)
						l.draw = func() float64 {
							return tt.args.draw
						}
						l.reqAttributes["test_key_1"] = "test_value_1"
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

			var parent string
			var lines int
			for _, line := range strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n") {
				if line == "" {
					continue
				}
				line = line[20:] // the log package's date and time
				if strings.Contains(line, cslLogCount+"=") {
					parent = line
				} else {
					lines++
				}
			}
			if (parent != "") != (tt.wantParent != "") || !strings.HasPrefix(parent, tt.wantParent) {
				t.Errorf("parent line = %q, want one starting %q", parent, tt.wantParent)
			}
			if parent != "" && !strings.Contains(parent, "test_key_1=test_value_1") {
				t.Errorf("parent line = %q, missing the request attribute", parent)
			}
			if lines != tt.wantLines {
				t.Errorf("child lines = %d, want %d", lines, tt.wantLines)
			}
		})
	}
}

func TestNewConsoleLogger(t *testing.T) {
	t.Parallel()

	type args struct {
		r       *http.Request
		noColor bool
	}
	tests := []struct {
		name string
		args args
		want ctxLogger
	}{
		{
			name: "some request",
			args: args{
				r:       &http.Request{},
				noColor: true,
			},
			want: &consoleLogger{
				record:      record{policy: Always(), maxSeverity: logging.Info, reqAttributes: map[string]any{}},
				r:           &http.Request{},
				noColor:     true,
				rsvdReqKeys: []string{"requestSize", "responseSize", "logCount"},
				attributes:  map[string]any{},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := newConsoleLogger(tt.args.r, tt.args.noColor)
			if diff := cmp.Diff(got, tt.want, cmp.AllowUnexported(consoleLogger{}, record{}, Policy{}), cmpopts.IgnoreFields(consoleLogger{}, "r", "record.mu", "root")); diff != "" {
				t.Errorf("NewConsoleLogger() mismatch (-want +got):\n%s", diff)
			}
			if got.root != got {
				t.Errorf("NewConsoleLogger().root is not self")
			}
		})
	}
}

func Test_consoleLogger(t *testing.T) {
	type args struct {
		v  []any
		v2 any
	}
	type fields struct {
		noColor    bool
		attributes map[string]any
	}
	tests := []struct {
		name       string
		args       args
		fields     fields
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
			name: "Test with color", args: args{v: []any{"Message"}, v2: "Message"},
			fields:    fields{attributes: map[string]any{"a test key": "a test value"}},
			wantDebug: "\x1b[37mDEBUG\x1b[0m: Message", wantDebugf: "\x1b[37mDEBUG\x1b[0m: Formatted Message",
			wantInfo: "\x1b[34mINFO \x1b[0m: Message", wantInfof: "\x1b[34mINFO \x1b[0m: Formatted Message",
			wantWarn: "\x1b[33mWARN \x1b[0m: Message", wantWarnf: "\x1b[33mWARN \x1b[0m: Formatted Message",
			wantError: "\x1b[31mERROR\x1b[0m: Message", wantErrorf: "\x1b[31mERROR\x1b[0m: Formatted Message",
		},
		{
			name: "Test no color", args: args{v: []any{"Message"}, v2: "Message"},
			fields:    fields{noColor: true, attributes: map[string]any{"test_key_1": "test_value_1", "test_key_2": "test_value_2"}},
			wantDebug: "DEBUG: Message", wantDebugf: "DEBUG: Formatted Message",
			wantInfo: "INFO : Message", wantInfof: "INFO : Formatted Message",
			wantWarn: "WARN : Message", wantWarnf: "WARN : Formatted Message",
			wantError: "ERROR: Message", wantErrorf: "ERROR: Formatted Message",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			ctx := context.Background()
			log.SetOutput(&buf)
			t.Cleanup(func() { log.SetOutput(os.Stderr) })

			u, _ := url.Parse("http://some.domain.com/path")
			l := &consoleLogger{r: &http.Request{Method: http.MethodGet, URL: u}, noColor: tt.fields.noColor, attributes: tt.fields.attributes}
			l.root = l
			format := "Formatted %s"

			verifyLog := func(log, methodName, expectedPrefix string) {
				if !strings.HasPrefix(log, expectedPrefix) {
					t.Errorf("consoleLogger.%s() = %q, missing prefix %q", methodName, log, expectedPrefix)
				}

				for k, v := range tt.fields.attributes {
					attrStr := fmt.Sprintf("%s=%v", k, v)
					if !strings.Contains(log, attrStr) {
						t.Errorf("consoleLogger.%s() missing attribute %s", methodName, attrStr)
					}
				}

				if !strings.HasSuffix(log, "\n") {
					t.Errorf("consoleLogger.%s() = %q, missing suffix \\n", methodName, log)
				}
			}

			l.Debug(ctx, tt.args.v2)
			verifyLog(buf.String()[20:], "Debug", tt.wantDebug)
			buf.Reset()

			l.Debugf(ctx, format, tt.args.v...)
			verifyLog(buf.String()[20:], "Debugf", tt.wantDebugf)
			buf.Reset()

			l.Info(ctx, tt.args.v2)
			verifyLog(buf.String()[20:], "Info", tt.wantInfo)
			buf.Reset()

			l.Infof(ctx, format, tt.args.v...)
			verifyLog(buf.String()[20:], "Infof", tt.wantInfof)
			buf.Reset()

			l.Warn(ctx, tt.args.v2)
			verifyLog(buf.String()[20:], "Warn", tt.wantWarn)
			buf.Reset()

			l.Warnf(ctx, format, tt.args.v...)
			verifyLog(buf.String()[20:], "Warnf", tt.wantWarnf)
			buf.Reset()

			l.Error(ctx, tt.args.v2)
			verifyLog(buf.String()[20:], "Error", tt.wantError)
			buf.Reset()

			l.Errorf(ctx, format, tt.args.v...)
			verifyLog(buf.String()[20:], "Errorf", tt.wantErrorf)
			buf.Reset()
		})
	}
}

func Test_consoleLogger_AddRequestAttribute(t *testing.T) {
	t.Parallel()
	type fields struct {
		root        *consoleLogger
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
			name: "prefix reserved key with 'custom_'",
			fields: fields{
				root: &consoleLogger{
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
				root: &consoleLogger{
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
				root: &consoleLogger{
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
			l := &consoleLogger{
				root:        tt.fields.root,
				rsvdReqKeys: tt.fields.rsvdReqKeys,
			}
			l.AddRequestAttribute(tt.args.key, tt.args.value)
			if diff := cmp.Diff(l.root.reqAttributes, tt.want); diff != "" {
				t.Errorf("consoleLogger.AddRequestAttribute() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func Test_consoleLogger_WithAttributes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		attributes map[string]any
		want       *consoleAttributer
	}{
		{
			name: "with attributes success",
			attributes: map[string]any{
				"test_key_1": "test_value_1",
				"test_key_2": "test_value_2",
			},
			want: &consoleAttributer{
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
			l := &consoleLogger{
				attributes: tt.attributes,
			}
			got := l.WithAttributes()
			if diff := cmp.Diff(got, tt.want, cmp.AllowUnexported(consoleAttributer{}), cmpopts.IgnoreFields(consoleAttributer{}, "logger")); diff != "" {
				t.Errorf("consoleLogger.WithAttributes() mismatch (-want +got):\n%s", diff)
			}
			if a, ok := got.(*consoleAttributer); !ok {
				t.Errorf("consoleLogger.WithAttributes() type %T, want %T", got, &consoleAttributer{})
			} else if a.logger != l {
				t.Errorf("consoleLogger.WithAttributes().logger != consoleLogger")
			}
		})
	}
}

func Test_consoleAttributer_AddAttribute(t *testing.T) {
	t.Parallel()
	type args struct {
		key   string
		value any
	}
	tests := []struct {
		name       string
		args       args
		attributes map[string]any
		want       map[string]any
	}{
		{
			name: "add attribute",
			args: args{
				key:   "test_key_0",
				value: 0,
			},
			attributes: map[string]any{
				"test_key_1": 1,
				"test_key_2": "test_value_2",
			},
			want: map[string]any{
				"test_key_1": 1,
				"test_key_2": "test_value_2",
				"test_key_0": 0,
			},
		},
		{
			name: "overwrite attribute value",
			args: args{
				key:   "test_key_1",
				value: "512",
			},
			attributes: map[string]any{
				"test_key_1": 1,
				"test_key_2": "test_value_2",
			},
			want: map[string]any{
				"test_key_1": "512",
				"test_key_2": "test_value_2",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := &consoleAttributer{
				attributes: tt.attributes,
			}
			a.AddAttribute(tt.args.key, tt.args.value)
			if diff := cmp.Diff(a.attributes, tt.want); diff != "" {
				t.Errorf("consoleAttributer.AddAttribute() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func Test_consoleAttributer_Logger(t *testing.T) {
	t.Parallel()
	type fields struct {
		logger     *consoleLogger
		attributes map[string]any
	}
	tests := []struct {
		name   string
		fields fields
		want   *consoleLogger
	}{
		{
			name: "success",
			fields: fields{
				logger: &consoleLogger{
					root: &consoleLogger{
						record: record{logCount: 123},
					},
					r:           httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/test/url", http.NoBody),
					noColor:     true,
					rsvdReqKeys: []string{"test reserved request key 1", "test reserved request key 2"},
					attributes:  map[string]any{"test_key_1": "test_value_1", "test_key_2": "test_value_2"},
					record: record{
						maxSeverity:   logging.Warning,
						logCount:      456,
						reqAttributes: map[string]any{"test_req_key_1": "test_req_value_1", "test_req_key_2": "test_req_value_2"},
					},
				},
				attributes: map[string]any{"test_key_3": "test_value_3", "test_key_4": "test_value_4"},
			},
			want: &consoleLogger{
				root: &consoleLogger{
					record: record{logCount: 123},
				},
				noColor:     true,
				rsvdReqKeys: []string{"test reserved request key 1", "test reserved request key 2"},
				attributes:  map[string]any{"test_key_3": "test_value_3", "test_key_4": "test_value_4"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := &consoleAttributer{
				logger:     tt.fields.logger,
				attributes: tt.fields.attributes,
			}

			got := a.Logger()
			if diff := cmp.Diff(got, tt.want, cmp.AllowUnexported(consoleLogger{}, record{}, Policy{}), cmpopts.IgnoreFields(consoleLogger{}, "record.mu", "r")); diff != "" {
				t.Errorf("consoleAttributer.Logger() mismatch (-want +got):\n%s", diff)
			}
			gotConsoleLogger, ok := got.(*consoleLogger)
			if !ok {
				t.Errorf("consoleAttributer.Logger() type %T, want %T", got, &consoleLogger{})
				return
			}
			if gotConsoleLogger.r != a.logger.r {
				t.Error("consoleAttributer.Logger().r is NOT the original request")
			}
		})
	}
}
