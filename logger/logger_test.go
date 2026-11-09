package logger

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"cloud.google.com/go/logging"
	"github.com/google/go-cmp/cmp"
	"go.uber.org/mock/gomock"
)

func TestLogger(t *testing.T) {
	t.Parallel()

	type args struct {
		v  []any
		v2 any
	}
	tests := []struct {
		name       string
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
			args: args{
				v:  []any{"Message"},
				v2: "Message",
			},
			wantDebug:  "Debug: Message, testCtxValue",
			wantDebugf: "Debugf: Formatted Message, testCtxValue",
			wantInfo:   "Info: Message, testCtxValue",
			wantInfof:  "Infof: Formatted Message, testCtxValue",
			wantWarn:   "Warn: Message, testCtxValue",
			wantWarnf:  "Warnf: Formatted Message, testCtxValue",
			wantError:  "Error: Message, testCtxValue",
			wantErrorf: "Errorf: Formatted Message, testCtxValue",
		},
		{
			name: "String & Error",
			args: args{
				v:  []any{"Message"},
				v2: errors.New("Message"),
			},
			wantDebug:  "Debug: Message, testCtxValue",
			wantDebugf: "Debugf: Formatted Message, testCtxValue",
			wantInfo:   "Info: Message, testCtxValue",
			wantInfof:  "Infof: Formatted Message, testCtxValue",
			wantWarn:   "Warn: Message, testCtxValue",
			wantWarnf:  "Warnf: Formatted Message, testCtxValue",
			wantError:  "Error: Message, testCtxValue",
			wantErrorf: "Errorf: Formatted Message, testCtxValue",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			ctxLgr := &testCtxLogger{buf: &buf}
			ctx := newContext(context.WithValue(context.Background(), ctxLgr, " testCtxValue"), ctxLgr)

			r := &http.Request{}
			r = r.WithContext(ctx)

			for _, l := range []*Logger{FromCtx(ctx), FromReq(r)} {
				format := "Formatted %s"

				l.Debug(tt.args.v2)
				if s := buf.String(); s != tt.wantDebug {
					t.Errorf("Logger.Debug() = %q, wantValue %q", s, tt.wantDebug)
				}
				buf.Reset()

				l.Debugf(format, tt.args.v...)
				if s := buf.String(); s != tt.wantDebugf {
					t.Errorf("Logger.Debugf() = %q, wantValue %q", s, tt.wantDebugf)
				}
				buf.Reset()

				l.Info(tt.args.v2)
				if s := buf.String(); s != tt.wantInfo {
					t.Errorf("Logger.Info() = %q, wantValue %q", s, tt.wantInfo)
				}
				buf.Reset()

				l.Infof(format, tt.args.v...)
				if s := buf.String(); s != tt.wantInfof {
					t.Errorf("Logger.Infof() = %q, wantValue %q", s, tt.wantInfof)
				}
				buf.Reset()

				l.Warn(tt.args.v2)
				if s := buf.String(); s != tt.wantWarn {
					t.Errorf("Logger.Warn() = %q, wantValue %q", s, tt.wantWarn)
				}
				buf.Reset()

				l.Warnf(format, tt.args.v...)
				if s := buf.String(); s != tt.wantWarnf {
					t.Errorf("Logger.Warnf() = %q, wantValue %q", s, tt.wantWarnf)
				}
				buf.Reset()

				l.Error(tt.args.v2)
				if s := buf.String(); s != tt.wantError {
					t.Errorf("Logger.Error() = %q, wantValue %q", s, tt.wantError)
				}
				buf.Reset()

				l.Errorf(format, tt.args.v...)
				if s := buf.String(); s != tt.wantErrorf {
					t.Errorf("Logger.Errorf() = %q, wantValue %q", s, tt.wantErrorf)
				}
				buf.Reset()
			}
		})
	}
}

func TestLogger_AddRequestAttribute(t *testing.T) {
	t.Parallel()
	type args struct {
		key   string
		value any
	}
	tests := []struct {
		name    string
		args    args
		prepare func(l *MockctxLogger)
	}{
		{
			name: "success adding request attribute",
			args: args{
				key:   "new_req_key",
				value: "new_req_value",
			},
			prepare: func(l *MockctxLogger) {
				l.EXPECT().AddRequestAttribute("new_req_key", "new_req_value").Times(1)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctxLgr := NewMockctxLogger(gomock.NewController(t))
			tt.prepare(ctxLgr)
			l := &Logger{lg: ctxLgr}
			if got := l.AddRequestAttribute(tt.args.key, tt.args.value); got != l {
				t.Error("Logger.AddRequestAttribute() did not return reference to original Logger (self)")
			}
		})
	}
}

func TestLogger_SetPolicy(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		policy  Policy
		prepare func(l *MockctxLogger)
	}{
		{
			name:   "the policy reaches the request's logger",
			policy: OnEvent().MinSeverity(logging.Warning),
			prepare: func(l *MockctxLogger) {
				l.EXPECT().SetPolicy(OnEvent().MinSeverity(logging.Warning)).Times(1)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctxLgr := NewMockctxLogger(gomock.NewController(t))
			tt.prepare(ctxLgr)
			l := &Logger{lg: ctxLgr}
			l.SetPolicy(tt.policy)
		})
	}
}

func TestLogger_WithAttributes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		prepare        func(l *MockctxLogger)
		wantAttributer attributer
	}{
		{
			name: "Logger with attributes success",
			prepare: func(l *MockctxLogger) {
				l.EXPECT().WithAttributes().Return(&consoleAttributer{
					attributes: map[string]any{
						"with_attributes_test_key": "with_attributes_test_value",
					},
				}).Times(1)
			},
			wantAttributer: &consoleAttributer{
				attributes: map[string]any{
					"with_attributes_test_key": "with_attributes_test_value",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctxLgr := NewMockctxLogger(gomock.NewController(t))
			tt.prepare(ctxLgr)
			l := &Logger{lg: ctxLgr}

			want := &AttributerLogger{
				logger:     l,
				attributer: tt.wantAttributer,
			}
			if got := l.WithAttributes(); !reflect.DeepEqual(got, want) {
				t.Errorf("Logger.WithAttributes() = %v, want %v", got, want)
			}
		})
	}
}

func TestAttributerLogger_AddAttribute(t *testing.T) {
	t.Parallel()
	type args struct {
		key   string
		value any
	}
	tests := []struct {
		name    string
		args    args
		prepare func(a *Mockattributer)
	}{
		{
			name: "success adding attribute",
			args: args{key: "new_key", value: "new_value"},
			prepare: func(a *Mockattributer) {
				a.EXPECT().AddAttribute("new_key", "new_value").Times(1)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mockAttributer := NewMockattributer(gomock.NewController(t))
			tt.prepare(mockAttributer)
			a := &AttributerLogger{
				logger:     &Logger{},
				attributer: mockAttributer,
			}
			if got := a.AddAttribute(tt.args.key, tt.args.value); got != a {
				t.Error("AttributerLogger.AddAttribute() did not return reference to original AttributerLogger (self)")
			}
		})
	}
}

func TestAttributerLogger_Logger(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		prepare    func(a *Mockattributer)
		wantCtxLgr *testCtxLogger
	}{
		{
			name: "success getting Logger",
			prepare: func(a *Mockattributer) {
				a.EXPECT().Logger().Return(&testCtxLogger{
					buf: bytes.NewBufferString("buffer for TestAttributerLogger_Logger"),
				}).Times(1)
			},
			wantCtxLgr: &testCtxLogger{
				buf: bytes.NewBufferString("buffer for TestAttributerLogger_Logger"),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockAttributer := NewMockattributer(gomock.NewController(t))
			tt.prepare(mockAttributer)
			a := &AttributerLogger{
				logger:     &Logger{ctx: context.Background()},
				attributer: mockAttributer,
			}

			got := a.Logger()
			if got.ctx != a.logger.ctx {
				t.Error("AttributerLogger.Logger().ctx NOT original logger's ctx")
			}
			gotCtxLgr, ok := got.lg.(*testCtxLogger)
			if !ok {
				t.Errorf("AttributerLogger.Logger().lg type %T, expected %T", got.lg, &testCtxLogger{})
				return
			}
			if diff := cmp.Diff(gotCtxLgr, tt.wantCtxLgr, cmp.AllowUnexported(testCtxLogger{}, bytes.Buffer{})); diff != "" {
				t.Errorf("AttributerLogger.Logger().lg mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

var _ ctxLogger = &testCtxLogger{}

type testCtxLogger struct {
	buf *bytes.Buffer
}

func (l *testCtxLogger) Debug(ctx context.Context, v any) {
	l.buf.WriteString("Debug: " + fmt.Sprint(v) + "," + fmt.Sprint(ctx.Value(l)))
}

func (l *testCtxLogger) Debugf(ctx context.Context, format string, v ...any) {
	l.buf.WriteString("Debugf: " + fmt.Sprintf(format, v...) + "," + fmt.Sprint(ctx.Value(l)))
}

func (l *testCtxLogger) Info(ctx context.Context, v any) {
	l.buf.WriteString("Info: " + fmt.Sprint(v) + "," + fmt.Sprint(ctx.Value(l)))
}

func (l *testCtxLogger) Infof(ctx context.Context, format string, v ...any) {
	l.buf.WriteString("Infof: " + fmt.Sprintf(format, v...) + "," + fmt.Sprint(ctx.Value(l)))
}

func (l *testCtxLogger) Warn(ctx context.Context, v any) {
	l.buf.WriteString("Warn: " + fmt.Sprint(v) + "," + fmt.Sprint(ctx.Value(l)))
}

func (l *testCtxLogger) Warnf(ctx context.Context, format string, v ...any) {
	l.buf.WriteString("Warnf: " + fmt.Sprintf(format, v...) + "," + fmt.Sprint(ctx.Value(l)))
}

func (l *testCtxLogger) Error(ctx context.Context, v any) {
	l.buf.WriteString("Error: " + fmt.Sprint(v) + "," + fmt.Sprint(ctx.Value(l)))
}

func (l *testCtxLogger) Errorf(ctx context.Context, format string, v ...any) {
	l.buf.WriteString("Errorf: " + fmt.Sprintf(format, v...) + "," + fmt.Sprint(ctx.Value(l)))
}

func (l *testCtxLogger) AddRequestAttribute(_ string, _ any) {}

func (l *testCtxLogger) SetPolicy(_ Policy) {}

func (l *testCtxLogger) WithAttributes() attributer {
	return &Mockattributer{}
}

func (l *testCtxLogger) TraceID() string {
	return "testTraceID"
}
