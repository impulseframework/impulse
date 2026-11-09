package logger

import (
	"context"
	"net/http"
	"reflect"
	"testing"
)

func Test_fromCtx(t *testing.T) {
	t.Parallel()

	type testLogger struct {
		ctxLogger
	}

	type args struct {
		ctx context.Context
	}
	tests := []struct {
		name string
		args args
		want ctxLogger
	}{
		{
			name: "logger from ctx",
			args: args{
				newContext(context.Background(), &testLogger{}),
			},
			want: &testLogger{},
		},
		{
			name: "StdErrLogger: ctx nil",
			want: &stdErrLogger{attributes: map[string]any{}},
		},
		{
			name: "StdErrLogger: ctx empty",
			args: args{
				ctx: context.Background(),
			},
			want: &stdErrLogger{attributes: map[string]any{}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := fromCtx(tt.args.ctx); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("fromContext() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_fromReq(t *testing.T) {
	t.Parallel()

	type args struct {
		r *http.Request
	}
	tests := []struct {
		name string
		args args
		want ctxLogger
	}{
		{
			name: "nil request",
			want: &stdErrLogger{attributes: map[string]any{}},
		},
		{
			name: "empty request ctx",
			args: args{
				r: &http.Request{},
			},
			want: &stdErrLogger{attributes: map[string]any{}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := fromReq(tt.args.r); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("fromRequest() = %v, want %v", got, tt.want)
			}
		})
	}
}
