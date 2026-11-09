package logger

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"cloud.google.com/go/logging"
	"github.com/go-test/deep"
)

func TestNewRequestLogger(t *testing.T) {
	disableMetaServertest(t)

	type args struct {
		e Exporter
	}
	tests := []struct {
		name string
		args args
		want func(http.Handler) http.Handler
	}{
		{
			name: "Google Exporter",
			args: args{
				e: NewGoogleCloudExporter(&logging.Client{}, "My first project"),
			},
			want: func(next http.Handler) http.Handler {
				client := &logging.Client{}

				return &gcpHandler{
					next:         next,
					parentLogger: client.Logger("request_parent_log"),
					childLogger:  client.Logger("request_child_log"),
					projectID:    "My first project",
					policy:       Always(),
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
			got := NewRequestLogger(tt.args.e)
			if diff := deep.Equal(got(next), tt.want(next)); diff != nil {
				t.Errorf("NewRequestLogger() = %v", diff)
			}
		})
	}
}

func TestNewCliLogger(t *testing.T) {
	disableMetaServertest(t)

	type args struct {
		e Exporter
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "Console Exporter",
			args: args{
				e: NewConsoleExporter(),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewCliLogger(tt.args.e)
			if got == nil {
				t.Errorf("NewCliLogger() returned nil")
			}
		})
	}
}

func Test_requestSize(t *testing.T) {
	t.Parallel()

	type args struct {
		length string
	}
	tests := []struct {
		name string
		args args
		want int64
	}{
		{
			name: "success",
			args: args{
				length: "20",
			},
			want: 20,
		},
		{
			name: "falure",
			args: args{
				length: "xxx",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := requestSize(tt.args.length); got != tt.want {
				t.Errorf("requestSize() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_recorder_Status(t *testing.T) {
	t.Parallel()

	type fields struct {
		status int
	}
	tests := []struct {
		name   string
		fields fields
		want   int
	}{
		{
			name: "Status set",
			fields: fields{
				status: http.StatusForbidden,
			},
			want: 403,
		},
		{
			name: "Status not set",
			want: 200,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w := &recorder{
				status: tt.fields.status,
			}
			if got := w.Status(); got != tt.want {
				t.Errorf("recorder.Status() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_recorder_Length(t *testing.T) {
	t.Parallel()

	type fields struct {
		ResponseWriter http.ResponseWriter
	}
	type args struct {
		b []byte
	}
	tests := []struct {
		name       string
		fields     fields
		args       args
		wantLength int64
	}{
		{
			name: "Write 10 bytes",
			fields: fields{
				ResponseWriter: &httptest.ResponseRecorder{},
			},
			args: args{
				b: []byte("0123456789"),
			},
			wantLength: 10,
		},
		{
			name: "Write 0 bytes",
			fields: fields{
				ResponseWriter: &httptest.ResponseRecorder{},
			},
			args: args{
				b: []byte(""),
			},
			wantLength: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w := &recorder{
				ResponseWriter: tt.fields.ResponseWriter,
			}
			got, err := w.Write(tt.args.b)
			if err != nil {
				t.Fatalf("recorder.Write() error = %v, wantErr %v", err, false)
			}
			if int64(got) != tt.wantLength {
				t.Errorf("recorder.Write() = %v, wantLength %v", got, tt.wantLength)
			}
			if got := w.Length(); got != tt.wantLength {
				t.Errorf("recorder.Status() = %v, wantLength %v", got, tt.wantLength)
			}
		})
	}
}

func Test_recorder_WriteHeader(t *testing.T) {
	t.Parallel()

	type fields struct {
		ResponseWriter http.ResponseWriter
	}
	type args struct {
		status int
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   int
	}{
		{
			name: "Success",
			fields: fields{
				ResponseWriter: &httptest.ResponseRecorder{},
			},
			args: args{
				status: 201,
			},
			want: 201,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w := &recorder{
				ResponseWriter: tt.fields.ResponseWriter,
			}
			w.WriteHeader(tt.args.status)
			if got := w.Status(); got != tt.want {
				t.Errorf("recorder.Status() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_recorder_Write(t *testing.T) {
	t.Parallel()

	type fields struct {
		ResponseWriter http.ResponseWriter
		status         int
	}
	type args struct {
		b []byte
	}
	tests := []struct {
		name       string
		fields     fields
		args       args
		wantLength int
		wantStatus int
		wantErr    bool
	}{
		{
			name: "No status set",
			fields: fields{
				ResponseWriter: &httptest.ResponseRecorder{},
			},
			args: args{
				b: []byte("0123456789"),
			},
			wantLength: 10,
			wantStatus: 200,
		},
		{
			name: "Status set",
			fields: fields{
				ResponseWriter: &httptest.ResponseRecorder{},
				status:         201,
			},
			args: args{
				b: []byte("01234567891234567890"),
			},
			wantLength: 20,
			wantStatus: 201,
		},
		{
			name: "Write error",
			fields: fields{
				ResponseWriter: &testResponseRecorder{err: errors.New("Bang")},
				status:         201,
			},
			args: args{
				b: []byte("01234567891234567890"),
			},
			wantLength: 20,
			wantStatus: 201,
			wantErr:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w := &recorder{
				ResponseWriter: tt.fields.ResponseWriter,
				status:         tt.fields.status,
			}
			got, err := w.Write(tt.args.b)
			if (err != nil) != tt.wantErr {
				t.Fatalf("recorder.Write() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.wantLength {
				t.Errorf("recorder.Write() = %v, wantLength %v", got, tt.wantLength)
			}
			if got := w.Status(); got != tt.wantStatus {
				t.Errorf("recorder.Status() = %v, wantStatus %v", got, tt.wantStatus)
			}
		})
	}
}

func Test_generateID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		wantLen int
	}{
		{
			name:    "Length 16",
			wantLen: 16,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := generateID(); len(got)/2 != tt.wantLen {
				t.Errorf("generateID() = %v, want len=%v", got, tt.wantLen)
			}
		})
	}
}

type testResponseRecorder struct {
	http.ResponseWriter
	err error
}

func (rw *testResponseRecorder) Write(buf []byte) (int, error) {
	return len(buf), rw.err
}

func Test_recorderFlusher_Flush(t *testing.T) {
	t.Parallel()

	type fields struct {
		recorder http.ResponseWriter
	}
	tests := []struct {
		name        string
		fields      fields
		wantFlusher bool
		flushCount  int
	}{
		{
			name: "Flusher",
			fields: fields{
				recorder: newResponseRecorder(&testResponseWriterFlusher{}),
			},
			wantFlusher: true,
			flushCount:  1,
		},
		{
			name: "No flusher",
			fields: fields{
				recorder: newResponseRecorder(&testResponseWriter{}),
			},
			wantFlusher: false,
			flushCount:  0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := tt.fields.recorder
			f, gotFlusher := r.(http.Flusher)
			if gotFlusher {
				f.Flush()
			}
			if gotFlusher != tt.wantFlusher {
				t.Fatalf("recorder foundFlusher = %v, want %v", gotFlusher, tt.wantFlusher)
			}

			if tt.wantFlusher {
				rf, ok := r.(*recorderFlusher)
				if !ok {
					t.Fatalf("recorder not a recorderFlusher")
				}
				c, ok := rf.ResponseWriter.(*testResponseWriterFlusher)
				if !ok {
					t.Fatalf("ResponseWriter not a testResponseWriterFlusher")
				}
				if c.flushed != tt.flushCount {
					t.Errorf("recorderFlusher.Flush() = %v, want %v", c.flushed, tt.flushCount)
				}
			}
		})
	}
}

func Test_recorderHijacker_Hijack(t *testing.T) {
	t.Parallel()

	type fields struct {
		recorder http.ResponseWriter
	}
	tests := []struct {
		name         string
		fields       fields
		wantHijacker bool
		hijackCount  int
	}{
		{
			name: "Hijacker",
			fields: fields{
				recorder: newResponseRecorder(&testResponseWriterHijacker{}),
			},
			wantHijacker: true,
			hijackCount:  1,
		},
		{
			name: "No hijacker",
			fields: fields{
				recorder: newResponseRecorder(&testResponseWriter{}),
			},
			wantHijacker: false,
			hijackCount:  0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := tt.fields.recorder
			h, gotHijacker := r.(http.Hijacker)
			if gotHijacker {
				_, _, err := h.Hijack()
				if err != nil {
					t.Fatalf("recorderHijacker.Hijack() error = %v", err)
				}
			}
			if gotHijacker != tt.wantHijacker {
				t.Fatalf("recorder foundHijacker = %v, want %v", gotHijacker, tt.wantHijacker)
			}

			if tt.wantHijacker {
				rh, ok := r.(*recorderHijacker)
				if !ok {
					t.Fatalf("recorder not a recorderHijacker")
				}
				c, ok := rh.ResponseWriter.(*testResponseWriterHijacker)
				if !ok {
					t.Fatalf("ResponseWriter not a testResponseWriterHijacker")
				}
				if c.hijacked != tt.hijackCount {
					t.Errorf("recorderHijacker.Hijack() = %v, want %v", c.hijacked, tt.hijackCount)
				}
			}
		})
	}
}

func Test_recorderFlusherHijacker_FlushHijack(t *testing.T) {
	t.Parallel()

	r := newResponseRecorder(&testResponseWriterFlusherHijacker{})

	f, gotFlusher := r.(http.Flusher)
	if !gotFlusher {
		t.Fatalf("expected http.Flusher")
	}
	f.Flush()

	h, gotHijacker := r.(http.Hijacker)
	if !gotHijacker {
		t.Fatalf("expected http.Hijacker")
	}
	_, _, err := h.Hijack()
	if err != nil {
		t.Fatalf("recorderFlusherHijacker.Hijack() error = %v", err)
	}

	rfh, ok := r.(*recorderFlusherHijacker)
	if !ok {
		t.Fatalf("recorder not a recorderFlusherHijacker")
	}
	c, ok := rfh.ResponseWriter.(*testResponseWriterFlusherHijacker)
	if !ok {
		t.Fatalf("ResponseWriter not a testResponseWriterFlusherHijacker")
	}
	if c.flushed != 1 {
		t.Errorf("expected 1 flush, got %d", c.flushed)
	}
	if c.hijacked != 1 {
		t.Errorf("expected 1 hijack, got %d", c.hijacked)
	}
}

type testResponseWriter struct{}

func (*testResponseWriter) Header() http.Header {
	return http.Header{}
}

func (*testResponseWriter) Write([]byte) (int, error) {
	return 0, nil
}

func (*testResponseWriter) WriteHeader(int) {
}

type testResponseWriterFlusher struct {
	testResponseWriter
	flushed int
}

func (t *testResponseWriterFlusher) Flush() {
	t.flushed++
}

type testResponseWriterHijacker struct {
	testResponseWriter
	hijacked int
}

func (t *testResponseWriterHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	t.hijacked++
	return nil, nil, nil
}

type testResponseWriterFlusherHijacker struct {
	testResponseWriter
	flushed  int
	hijacked int
}

func (t *testResponseWriterFlusherHijacker) Flush() {
	t.flushed++
}

func (t *testResponseWriterFlusherHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	t.hijacked++
	return nil, nil, nil
}

func TestNewRequestLogger_policy(t *testing.T) {
	t.Parallel()

	prefixes := map[string]Policy{
		"/files":      Never(),
		"/files/open": Always(),
	}
	type args struct {
		opts   []RequestLoggerOption
		chain  []func(http.Handler) http.Handler // middleware inside the request logger, outermost first
		handle func(r *http.Request)
		path   string
		status int
	}
	tests := []struct {
		name           string
		exporterPolicy Policy
		args           args
		wantParent     bool
	}{
		{
			name:           "no options keep the exporter's default, always",
			exporterPolicy: Always(),
			args:           args{path: "/", status: http.StatusOK},
			wantParent:     true,
		},
		{
			name:           "no options keep the exporter's default, on event",
			exporterPolicy: OnEvent(),
			args:           args{path: "/", status: http.StatusOK},
		},
		{
			name:           "DefaultPolicy overrides the exporter's default",
			exporterPolicy: OnEvent(),
			args:           args{opts: []RequestLoggerOption{DefaultPolicy(Always())}, path: "/", status: http.StatusOK},
			wantParent:     true,
		},
		{
			name:           "DefaultPolicy can demote below the exporter's default",
			exporterPolicy: Always(),
			args:           args{opts: []RequestLoggerOption{DefaultPolicy(Never())}, path: "/", status: http.StatusInternalServerError},
		},
		{
			name:           "PolicyByPrefix, the longest matching prefix wins",
			exporterPolicy: OnEvent(),
			args:           args{opts: []RequestLoggerOption{PolicyByPrefix(prefixes)}, path: "/files/open/1", status: http.StatusOK},
			wantParent:     true,
		},
		{
			name:           "PolicyByPrefix, a shorter prefix matches when the longer does not",
			exporterPolicy: Always(),
			args:           args{opts: []RequestLoggerOption{PolicyByPrefix(prefixes)}, path: "/files/closed", status: http.StatusInternalServerError},
		},
		{
			name:           "PolicyByPrefix, no prefix matches, so the default applies",
			exporterPolicy: OnEvent(),
			args:           args{opts: []RequestLoggerOption{PolicyByPrefix(prefixes), DefaultPolicy(Always())}, path: "/other", status: http.StatusOK},
			wantParent:     true,
		},
		{
			name:           "PolicyByPrefix, no prefix matches and no default, so the exporter's default applies",
			exporterPolicy: OnEvent(),
			args:           args{opts: []RequestLoggerOption{PolicyByPrefix(prefixes)}, path: "/other", status: http.StatusOK},
		},
		{
			name:           "PolicyByPrefix comes before DefaultPolicy",
			exporterPolicy: Always(),
			args:           args{opts: []RequestLoggerOption{DefaultPolicy(Always()), PolicyByPrefix(prefixes)}, path: "/files/1", status: http.StatusInternalServerError},
		},
		{
			name:           "WithPolicy overrides the starting policy",
			exporterPolicy: Always(),
			args: args{
				opts:   []RequestLoggerOption{DefaultPolicy(Always())},
				chain:  []func(http.Handler) http.Handler{WithPolicy(Never())},
				path:   "/",
				status: http.StatusInternalServerError,
			},
		},
		{
			name:           "the nearest WithPolicy wins",
			exporterPolicy: Always(),
			args: args{
				chain:  []func(http.Handler) http.Handler{WithPolicy(Never()), WithPolicy(Always())},
				path:   "/",
				status: http.StatusOK,
			},
			wantParent: true,
		},
		{
			name:           "the handler promotes its own request",
			exporterPolicy: Always(),
			args: args{
				chain: []func(http.Handler) http.Handler{WithPolicy(Never())},
				handle: func(r *http.Request) {
					FromReq(r).SetPolicy(Always())
				},
				path:   "/",
				status: http.StatusOK,
			},
			wantParent: true,
		},
		{
			name:           "the handler demotes its own request",
			exporterPolicy: Always(),
			args: args{
				handle: func(r *http.Request) {
					FromReq(r).SetPolicy(Never())
				},
				path:   "/",
				status: http.StatusInternalServerError,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			parent := &captureLogger{}
			var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.args.handle != nil {
					tt.args.handle(r)
				}
				w.WriteHeader(tt.args.status)
			})
			for i := len(tt.args.chain) - 1; i >= 0; i-- {
				handler = tt.args.chain[i](handler)
			}
			e := &captureExporter{parent: parent, policy: tt.exporterPolicy}
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.args.path, http.NoBody)
			NewRequestLogger(e, tt.args.opts...)(handler).ServeHTTP(httptest.NewRecorder(), r)

			if got := parent.calls == 1; got != tt.wantParent {
				t.Errorf("parent entry written = %v, want %v", got, tt.wantParent)
			}
		})
	}
}

func TestNewCliLogger_policy(t *testing.T) {
	t.Parallel()

	type args struct {
		opts  []RequestLoggerOption
		lines int
		err   error
	}
	tests := []struct {
		name           string
		exporterPolicy Policy
		args           args
		wantParent     bool
	}{
		{
			name:           "no options keep the exporter's default, always",
			exporterPolicy: Always(),
			wantParent:     true,
		},
		{
			name:           "no options keep the exporter's default, on event",
			exporterPolicy: OnEvent(),
		},
		{
			name:           "DefaultPolicy overrides the exporter's default",
			exporterPolicy: OnEvent(),
			args:           args{opts: []RequestLoggerOption{DefaultPolicy(Always())}},
			wantParent:     true,
		},
		{
			name:           "on event, a line attached",
			exporterPolicy: Always(),
			args:           args{opts: []RequestLoggerOption{DefaultPolicy(OnEvent())}, lines: 1},
			wantParent:     true,
		},
		{
			name:           "on event, the function returned an error",
			exporterPolicy: Always(),
			args:           args{opts: []RequestLoggerOption{DefaultPolicy(OnEvent())}, err: errors.New("failed")},
			wantParent:     true,
		},
		{
			name:           "never, even on an error",
			exporterPolicy: Always(),
			args:           args{opts: []RequestLoggerOption{DefaultPolicy(Never())}, err: errors.New("failed")},
		},
		{
			name:           "PolicyByPrefix does not apply to a run",
			exporterPolicy: OnEvent(),
			args:           args{opts: []RequestLoggerOption{PolicyByPrefix(map[string]Policy{"/": Always()})}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			parent := &captureLogger{}
			e := &captureExporter{parent: parent, policy: tt.exporterPolicy}
			err := NewCliLogger(e, tt.args.opts...)(t.Context(), "command", func(ctx context.Context) error {
				logLines(ctx, tt.args.lines, logging.Info)

				return tt.args.err
			})
			if !errors.Is(err, tt.args.err) {
				t.Errorf("NewCliLogger() error = %v, want %v", err, tt.args.err)
			}
			if got := parent.calls == 1; got != tt.wantParent {
				t.Errorf("parent entry written = %v, want %v", got, tt.wantParent)
			}
		})
	}
}

func TestWithPolicy_withoutRequestLogger(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		policy Policy
	}{
		{
			name:   "passes the request through unchanged",
			policy: Never(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got *http.Request
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r
				w.WriteHeader(http.StatusTeapot)
			})
			w := httptest.NewRecorder()
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
			WithPolicy(tt.policy)(next).ServeHTTP(w, r)

			if got != r {
				t.Errorf("WithPolicy() handed the handler a different request")
			}
			if w.Code != http.StatusTeapot {
				t.Errorf("WithPolicy() status = %d, want %d", w.Code, http.StatusTeapot)
			}
		})
	}
}

func Test_requestLoggerOptions_startingPolicy(t *testing.T) {
	t.Parallel()

	table := map[string]Policy{
		"/a":   Never(),
		"/a/b": Always(),
		"/":    OnEvent(),
	}
	type args struct {
		opts []RequestLoggerOption
		path string
	}
	tests := []struct {
		name   string
		args   args
		want   Policy
		wantOK bool
	}{
		{
			name: "no options name no policy",
			args: args{path: "/a"},
		},
		{
			name:   "the default alone",
			args:   args{opts: []RequestLoggerOption{DefaultPolicy(OnEvent())}, path: "/a"},
			want:   OnEvent(),
			wantOK: true,
		},
		{
			name:   "the longest prefix wins",
			args:   args{opts: []RequestLoggerOption{PolicyByPrefix(table)}, path: "/a/b/c"},
			want:   Always(),
			wantOK: true,
		},
		{
			name:   "a shorter prefix when the longer does not match",
			args:   args{opts: []RequestLoggerOption{PolicyByPrefix(table)}, path: "/a/x"},
			want:   Never(),
			wantOK: true,
		},
		{
			name:   "the shortest prefix catches the rest",
			args:   args{opts: []RequestLoggerOption{PolicyByPrefix(table)}, path: "/z"},
			want:   OnEvent(),
			wantOK: true,
		},
		{
			name:   "a prefix table built over several options",
			args:   args{opts: []RequestLoggerOption{PolicyByPrefix(map[string]Policy{"/a": Never()}), PolicyByPrefix(map[string]Policy{"/a/b": Always()})}, path: "/a/b"},
			want:   Always(),
			wantOK: true,
		},
		{
			name: "no prefix matches and no default",
			args: args{opts: []RequestLoggerOption{PolicyByPrefix(map[string]Policy{"/a": Never()})}, path: "/z"},
		},
		{
			name:   "no prefix matches, so the default",
			args:   args{opts: []RequestLoggerOption{PolicyByPrefix(map[string]Policy{"/a": Never()}), DefaultPolicy(Sampled(0.5))}, path: "/z"},
			want:   Sampled(0.5),
			wantOK: true,
		},
		{
			name:   "a prefix match comes before the default",
			args:   args{opts: []RequestLoggerOption{DefaultPolicy(Always()), PolicyByPrefix(map[string]Policy{"/a": Never()})}, path: "/a"},
			want:   Never(),
			wantOK: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := newRequestLoggerOptions(tt.args.opts).startingPolicy(tt.args.path)
			if ok != tt.wantOK || got != tt.want {
				t.Errorf("startingPolicy() = %v, %v, want %v, %v", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

// captureExporter is an Exporter over the Google Cloud handler and runner with capture loggers in place of a
// client, so a test sees the parent entry each request or run decided on.
type captureExporter struct {
	parent *captureLogger
	policy Policy
}

func (e *captureExporter) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return &gcpHandler{
			next:         next,
			parentLogger: e.parent,
			childLogger:  &captureLogger{},
			projectID:    "capture",
			policy:       e.policy,
		}
	}
}

func (e *captureExporter) CliRunner() func(ctx context.Context, command string, f func(context.Context) error) error {
	r := &gcpRunner{parentLogger: e.parent, childLogger: &captureLogger{}, projectID: "capture", policy: e.policy}

	return r.run
}

func (e *captureExporter) DaemonContext(ctx context.Context) context.Context {
	return newContext(ctx, newGCPLogger(e.parent, "capture", e.policy))
}

// logLines writes n lines at the severity through the logger in the context.
func logLines(ctx context.Context, n int, severity logging.Severity) {
	l := FromCtx(ctx)
	for range n {
		switch severity {
		case logging.Debug:
			l.Debug("some log")
		case logging.Info:
			l.Info("some log")
		case logging.Warning:
			l.Warn("some log")
		case logging.Error:
			l.Error("some log")
		default:
		}
	}
}
