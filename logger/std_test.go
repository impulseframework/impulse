package logger

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func Test_stdErrLogger(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	type args struct {
		v  []any
		v2 any
	}
	tests := []struct {
		name       string
		args       args
		attributes map[string]any
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
			name: "Test 1",
			args: args{
				v:  []any{"Message"},
				v2: "Message",
			},
			attributes: map[string]any{"test_key_1": "test_value_1", "test_key_2": "test_value_2"},
			wantDebug:  "DEBUG: Message",
			wantDebugf: "DEBUG: Formatted Message",
			wantInfo:   "INFO : Message",
			wantInfof:  "INFO : Formatted Message",
			wantWarn:   "WARN : Message",
			wantWarnf:  "WARN : Formatted Message",
			wantError:  "ERROR: Message",
			wantErrorf: "ERROR: Formatted Message",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			l := &stdErrLogger{attributes: tt.attributes}
			format := "Formatted %s"

			verifyLog := func(log, methodName, expectedPrefix string) {
				if !strings.HasPrefix(log, expectedPrefix) {
					t.Errorf("stdErrLogger.%s() = %q, missing prefix %q", methodName, log, expectedPrefix)
				}

				for k, v := range tt.attributes {
					attrStr := fmt.Sprintf("%s=%v", k, v)
					if !strings.Contains(log, attrStr) {
						t.Errorf("stdErrLogger.%s() missing attribute %s", methodName, attrStr)
					}
				}

				if !strings.HasSuffix(log, "\n") {
					t.Errorf("stdErrLogger.%s() = %q, missing suffix \\n", methodName, log)
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

func Test_stdErrLogger_WithAttributes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		attributes map[string]any
		want       *stdAttributer
	}{
		{
			name: "with attributes success",
			attributes: map[string]any{
				"test_key_1": "test_value_1",
				"test_key_2": "test_value_2",
			},
			want: &stdAttributer{
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
			l := &stdErrLogger{
				attributes: tt.attributes,
			}
			got := l.WithAttributes()
			if diff := cmp.Diff(got, tt.want, cmp.AllowUnexported(stdAttributer{}), cmpopts.IgnoreFields(stdAttributer{}, "logger")); diff != "" {
				t.Errorf("stdErrLogger.WithAttributes() mismatch (-want +got):\n%s", diff)
			}
			if a, ok := got.(*stdAttributer); !ok {
				t.Errorf("stdErrLogger.WithAttributes() type %T, want %T", got, &stdAttributer{})
			} else if a.logger != l {
				t.Errorf("stdErrLogger.WithAttributes().logger != stdErrLogger")
			}
		})
	}
}

func Test_stdAttributer_AddAttribute(t *testing.T) {
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
			name: "success adding attribute",
			args: args{
				key:   "test_key_0",
				value: "test_value_0",
			},
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
			name: "success overwriting attribute",
			args: args{
				key:   "test_key_1",
				value: 512,
			},
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
			a := &stdAttributer{
				attributes: tt.attributes,
				logger:     &stdErrLogger{},
			}
			a.AddAttribute(tt.args.key, tt.args.value)
			if diff := cmp.Diff(a.attributes, tt.want); diff != "" {
				t.Errorf("stdAttributer.AddAttribute() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func Test_stdAttributer_Logger(t *testing.T) {
	t.Parallel()
	type fields struct {
		logger     *stdErrLogger
		attributes map[string]any
	}
	tests := []struct {
		name   string
		fields fields
		want   *stdErrLogger
	}{
		{
			name: "success getting logger",
			fields: fields{
				logger: &stdErrLogger{
					attributes: map[string]any{"test_key_1": "test_value_1", "test_key_2": "test_value_2"},
				},
				attributes: map[string]any{"test_key_3": "test_value_3", "test_key_4": "test_value_4"},
			},
			want: &stdErrLogger{
				attributes: map[string]any{"test_key_3": "test_value_3", "test_key_4": "test_value_4"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := &stdAttributer{
				logger:     tt.fields.logger,
				attributes: tt.fields.attributes,
			}

			got := a.Logger()
			if diff := cmp.Diff(got, tt.want, cmp.AllowUnexported(stdErrLogger{})); diff != "" {
				t.Errorf("stdAttributer.Logger() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
