package logger

import (
	"testing"

	"cloud.google.com/go/logging"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func Test_record_attach(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		policy       Policy
		lines        []logging.Severity
		wantAttached []bool
		wantCount    int
		wantMax      logging.Severity
	}{
		{
			name:         "no floor attaches every line",
			policy:       Always(),
			lines:        []logging.Severity{logging.Debug, logging.Info, logging.Warning},
			wantAttached: []bool{true, true, true},
			wantCount:    3,
			wantMax:      logging.Warning,
		},
		{
			name:         "a floor drops the lines below it",
			policy:       OnEvent().MinSeverity(logging.Warning),
			lines:        []logging.Severity{logging.Debug, logging.Info, logging.Warning, logging.Error},
			wantAttached: []bool{false, false, true, true},
			wantCount:    2,
			wantMax:      logging.Error,
		},
		{
			name:         "a dropped line does not raise the severity",
			policy:       Never().MinSeverity(logging.Error),
			lines:        []logging.Severity{logging.Warning},
			wantAttached: []bool{false},
		},
		{
			name:   "no lines",
			policy: Always(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := &record{policy: tt.policy}
			got := make([]bool, 0, len(tt.lines))
			for _, severity := range tt.lines {
				got = append(got, r.attach(severity))
			}
			if diff := cmp.Diff(got, tt.wantAttached, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("record.attach() mismatch (-want +got):\n%s", diff)
			}
			if r.logCount != tt.wantCount {
				t.Errorf("record.logCount = %d, want %d", r.logCount, tt.wantCount)
			}
			if r.maxSeverity != tt.wantMax {
				t.Errorf("record.maxSeverity = %v, want %v", r.maxSeverity, tt.wantMax)
			}
		})
	}
}

func Test_record_decide(t *testing.T) {
	t.Parallel()

	type args struct {
		attributes map[string]any
		lines      []logging.Severity
		setPolicy  []Policy // applied after the lines, in order
		failed     bool
		draw       float64
	}
	tests := []struct {
		name   string
		policy Policy
		args   args
		want   decision
	}{
		{
			name:   "the starting policy decides",
			policy: OnEvent(),
			args:   args{lines: []logging.Severity{logging.Info}, attributes: map[string]any{"k": "v"}},
			want:   decision{write: true, logCount: 1, maxSeverity: logging.Info, attributes: map[string]any{"k": "v"}},
		},
		{
			name:   "a quiet request under on event",
			policy: OnEvent(),
			want:   decision{attributes: map[string]any{}},
		},
		{
			name:   "the latest setPolicy wins",
			policy: Always(),
			args:   args{setPolicy: []Policy{Never(), OnEvent()}},
			want:   decision{attributes: map[string]any{}},
		},
		{
			name:   "setPolicy to never wins over a failed request with a line",
			policy: Always(),
			args:   args{lines: []logging.Severity{logging.Error}, setPolicy: []Policy{Never()}, failed: true},
			want:   decision{logCount: 1, maxSeverity: logging.Error, attributes: map[string]any{}},
		},
		{
			name:   "a sampled policy draws",
			policy: Sampled(0.5),
			args:   args{draw: 0.1},
			want:   decision{write: true, attributes: map[string]any{}},
		},
		{
			name:   "a floor set later keeps the lines that already attached",
			policy: OnEvent(),
			args:   args{lines: []logging.Severity{logging.Info}, setPolicy: []Policy{OnEvent().MinSeverity(logging.Error)}},
			want:   decision{write: true, logCount: 1, maxSeverity: logging.Info, attributes: map[string]any{}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := &record{
				policy:        tt.policy,
				reqAttributes: map[string]any{},
				draw: func() float64 {
					return tt.args.draw
				},
			}
			for k, v := range tt.args.attributes {
				r.addRequestAttribute(k, v)
			}
			for _, severity := range tt.args.lines {
				r.attach(severity)
			}
			for _, p := range tt.args.setPolicy {
				r.setPolicy(p)
			}

			got := r.decide(tt.args.failed)
			if diff := cmp.Diff(got, tt.want, cmp.AllowUnexported(decision{})); diff != "" {
				t.Errorf("record.decide() mismatch (-want +got):\n%s", diff)
			}
			got.attributes["added later"] = true
			if _, ok := r.reqAttributes["added later"]; ok {
				t.Error("record.decide() handed out the record's own attributes instead of a copy")
			}
		})
	}
}
