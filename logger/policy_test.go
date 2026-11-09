package logger

import (
	"math"
	"testing"

	"cloud.google.com/go/logging"
)

func TestSampled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		fraction  float64
		wantPanic bool
	}{
		{
			name:     "a fraction in range",
			fraction: 0.5,
		},
		{
			name:     "the whole",
			fraction: 1,
		},
		{
			name:     "a sliver",
			fraction: 1e-9,
		},
		{
			name:      "zero is refused",
			fraction:  0,
			wantPanic: true,
		},
		{
			name:      "a negative fraction is refused",
			fraction:  -0.1,
			wantPanic: true,
		},
		{
			name:      "above one is refused",
			fraction:  1.5,
			wantPanic: true,
		},
		{
			name:      "not a number is refused",
			fraction:  math.NaN(),
			wantPanic: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			defer func() {
				if got := recover() != nil; got != tt.wantPanic {
					t.Errorf("Sampled(%v) panicked = %v, want %v", tt.fraction, got, tt.wantPanic)
				}
			}()
			got := Sampled(tt.fraction)
			if got.mode != policySampled || got.fraction != tt.fraction {
				t.Errorf("Sampled(%v) = %+v", tt.fraction, got)
			}
		})
	}
}

func TestPolicy_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		policy Policy
		want   string
	}{
		{
			name:   "always",
			policy: Always(),
			want:   "always",
		},
		{
			name:   "the zero value is always",
			policy: Policy{},
			want:   "always",
		},
		{
			name:   "on event",
			policy: OnEvent(),
			want:   "on event",
		},
		{
			name:   "sampled",
			policy: Sampled(0.1),
			want:   "sampled at 0.1",
		},
		{
			name:   "sampled at the whole",
			policy: Sampled(1),
			want:   "sampled at 1",
		},
		{
			name:   "never",
			policy: Never(),
			want:   "never",
		},
		{
			name:   "with a floor",
			policy: OnEvent().MinSeverity(logging.Warning),
			want:   "on event, Warning and above",
		},
		{
			name:   "sampled with a floor",
			policy: Sampled(0.25).MinSeverity(logging.Error),
			want:   "sampled at 0.25, Error and above",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.policy.String(); got != tt.want {
				t.Errorf("Policy.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPolicy_MinSeverity(t *testing.T) {
	t.Parallel()

	type args struct {
		severity logging.Severity
		line     logging.Severity
	}
	tests := []struct {
		name       string
		policy     Policy
		args       args
		wantAdmits bool
	}{
		{
			name:       "no floor admits debug",
			policy:     Always(),
			args:       args{severity: logging.Default, line: logging.Debug},
			wantAdmits: true,
		},
		{
			name:   "a floor at warning drops info",
			policy: OnEvent(),
			args:   args{severity: logging.Warning, line: logging.Info},
		},
		{
			name:       "a floor at warning admits warning",
			policy:     OnEvent(),
			args:       args{severity: logging.Warning, line: logging.Warning},
			wantAdmits: true,
		},
		{
			name:       "a floor at warning admits error",
			policy:     Sampled(0.5),
			args:       args{severity: logging.Warning, line: logging.Error},
			wantAdmits: true,
		},
		{
			name:   "a floor at error drops warning",
			policy: Never(),
			args:   args{severity: logging.Error, line: logging.Warning},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.policy.MinSeverity(tt.args.severity)
			if got.minSeverity != tt.args.severity {
				t.Errorf("Policy.MinSeverity() floor = %v, want %v", got.minSeverity, tt.args.severity)
			}
			if got.mode != tt.policy.mode || got.fraction != tt.policy.fraction {
				t.Errorf("Policy.MinSeverity() changed the word: got %v, had %v", got, tt.policy)
			}
			if tt.policy.minSeverity != logging.Default {
				t.Errorf("Policy.MinSeverity() changed its receiver: %v", tt.policy)
			}
			if admits := got.admits(tt.args.line); admits != tt.wantAdmits {
				t.Errorf("Policy.admits(%v) = %v, want %v", tt.args.line, admits, tt.wantAdmits)
			}
		})
	}
}

func TestPolicy_decide(t *testing.T) {
	t.Parallel()

	type args struct {
		logCount int
		failed   bool
		draw     float64
	}
	tests := []struct {
		name   string
		policy Policy
		args   args
		want   bool
	}{
		{
			name:   "always, quiet",
			policy: Always(),
			want:   true,
		},
		{
			name:   "always, failed",
			policy: Always(),
			args:   args{failed: true},
			want:   true,
		},
		{
			name:   "on event, quiet",
			policy: OnEvent(),
		},
		{
			name:   "on event, a line attached",
			policy: OnEvent(),
			args:   args{logCount: 1},
			want:   true,
		},
		{
			name:   "on event, failed",
			policy: OnEvent(),
			args:   args{failed: true},
			want:   true,
		},
		{
			name:   "sampled, quiet and the draw hits",
			policy: Sampled(0.5),
			args:   args{draw: 0.49},
			want:   true,
		},
		{
			name:   "sampled, quiet and the draw misses",
			policy: Sampled(0.5),
			args:   args{draw: 0.5},
		},
		{
			name:   "sampled, the draw misses but a line attached",
			policy: Sampled(0.5),
			args:   args{logCount: 1, draw: 0.9},
			want:   true,
		},
		{
			name:   "sampled, the draw misses but failed",
			policy: Sampled(0.5),
			args:   args{failed: true, draw: 0.9},
			want:   true,
		},
		{
			name:   "sampled at the whole, every quiet request",
			policy: Sampled(1),
			args:   args{draw: 0.999},
			want:   true,
		},
		{
			name:   "never, failed with lines",
			policy: Never(),
			args:   args{logCount: 3, failed: true},
		},
		{
			name:   "the floor does not change the decision",
			policy: OnEvent().MinSeverity(logging.Error),
			args:   args{logCount: 1},
			want:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			draw := func() float64 {
				return tt.args.draw
			}
			if got := tt.policy.decide(tt.args.logCount, tt.args.failed, draw); got != tt.want {
				t.Errorf("Policy.decide() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_logAllPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		logAll bool
		want   Policy
	}{
		{
			name:   "on is always",
			logAll: true,
			want:   Always(),
		},
		{
			name: "off is on event",
			want: OnEvent(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := logAllPolicy(tt.logAll); got != tt.want {
				t.Errorf("logAllPolicy(%v) = %v, want %v", tt.logAll, got, tt.want)
			}
		})
	}
}
