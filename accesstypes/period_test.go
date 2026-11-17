package accesstypes

import (
	"strings"
	"testing"
	"time"
)

func TestPeriod(t *testing.T) {
	t.Parallel()

	noon := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	before, after := noon.Add(-time.Hour), noon.Add(time.Hour)

	tests := []struct {
		name                         string
		period                       Period
		wantCounts, wantUp, wantOver bool
	}{
		{name: "no start and no end always counts", period: Period{}, wantCounts: true},
		{name: "before its start", period: Period{Start: after}, wantUp: true},
		{name: "at its start", period: Period{Start: noon}, wantCounts: true},
		{name: "after its start, with no end", period: Period{Start: before}, wantCounts: true},
		{name: "before its end", period: Period{End: after}, wantCounts: true},
		{name: "at its end", period: Period{End: noon}, wantOver: true},
		{name: "after its end", period: Period{Start: before.Add(-time.Hour), End: before}, wantOver: true},
		{name: "inside both", period: Period{Start: before, End: after}, wantCounts: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.period.Contains(noon); got != tt.wantCounts {
				t.Errorf("Contains() = %v, want %v", got, tt.wantCounts)
			}
			if got := tt.period.Upcoming(noon); got != tt.wantUp {
				t.Errorf("Upcoming() = %v, want %v", got, tt.wantUp)
			}
			if got := tt.period.Ended(noon); got != tt.wantOver {
				t.Errorf("Ended() = %v, want %v", got, tt.wantOver)
			}
		})
	}
}

func TestNewPeriod(t *testing.T) {
	t.Parallel()

	denver := time.FixedZone("MST", -7*3600)
	start := time.Date(2026, 11, 16, 5, 0, 0, 0, denver)
	end := start.Add(time.Hour)

	p, err := NewPeriod(&start, &end)
	if err != nil {
		t.Fatalf("NewPeriod() error = %v", err)
	}
	if p.Start.Location() != time.UTC || !p.Start.Equal(start) || !p.End.Equal(end) {
		t.Errorf("NewPeriod() = %v, want the two instants in UTC", p)
	}
	if none, err := NewPeriod(nil, nil); err != nil || !none.IsZero() {
		t.Errorf("NewPeriod(nil, nil) = %v, %v, want the zero period", none, err)
	}
	if p.StartPtr() == nil || p.EndPtr() == nil || (Period{}).StartPtr() != nil || (Period{}).EndPtr() != nil {
		t.Error("StartPtr and EndPtr answer nil for no instant and the instant otherwise")
	}

	for _, tt := range []struct {
		name       string
		start, end time.Time
	}{
		{name: "an end before the start", start: end, end: start},
		{name: "an end at the start", start: start, end: start},
	} {
		if _, err := NewPeriod(&tt.start, &tt.end); err == nil || !strings.Contains(err.Error(), "is not after its start") {
			t.Errorf("%s: NewPeriod() error = %v, want a refusal", tt.name, err)
		}
	}
}

func TestPeriod_Intersect(t *testing.T) {
	t.Parallel()

	at := func(hour int) time.Time {
		return time.Date(2026, 11, 16, hour, 0, 0, 0, time.UTC)
	}
	tests := []struct {
		name       string
		a, b, want Period
	}{
		{name: "two open periods", want: Period{}},
		{name: "one with an end", a: Period{End: at(9)}, want: Period{End: at(9)}},
		{name: "the later start and the earlier end", a: Period{Start: at(8), End: at(17)}, b: Period{Start: at(9), End: at(12)}, want: Period{Start: at(9), End: at(12)}},
		{name: "a start from one and an end from the other", a: Period{Start: at(8)}, b: Period{End: at(12)}, want: Period{Start: at(8), End: at(12)}},
		{name: "periods that never meet never count", a: Period{End: at(9)}, b: Period{Start: at(10)}, want: Period{Start: at(10), End: at(9)}},
	}
	for _, tt := range tests {
		if got := tt.a.Intersect(tt.b); got != tt.want {
			t.Errorf("%s: Intersect() = %v, want %v", tt.name, got, tt.want)
		}
		if got := tt.b.Intersect(tt.a); got != tt.want {
			t.Errorf("%s: Intersect() the other way = %v, want %v", tt.name, got, tt.want)
		}
	}
	if never := (Period{Start: at(10), End: at(9)}); never.Contains(at(9)) || never.Contains(at(10)) || never.Contains(at(11)) {
		t.Error("a period that ends before it starts counts at some instant")
	}
}

func TestPeriod_Equal(t *testing.T) {
	t.Parallel()

	noon := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	east := noon.In(time.FixedZone("east", 3600))
	tests := []struct {
		name string
		a, b Period
		want bool
	}{
		{name: "two periods with neither a start nor an end", want: true},
		{name: "the same instants", a: Period{Start: noon, End: noon.Add(time.Hour)}, b: Period{Start: noon, End: noon.Add(time.Hour)}, want: true},
		{name: "the same instants read in another location", a: Period{End: noon}, b: Period{End: east}, want: true},
		{name: "an end and none", a: Period{End: noon}, b: Period{}},
		{name: "a start and an end at one instant", a: Period{Start: noon}, b: Period{End: noon}},
		{name: "another end", a: Period{End: noon}, b: Period{End: noon.Add(time.Nanosecond)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.a.Equal(tt.b); got != tt.want {
				t.Errorf("Equal() = %v, want %v", got, tt.want)
			}
			if got := tt.b.Equal(tt.a); got != tt.want {
				t.Errorf("Equal() the other way = %v, want %v", got, tt.want)
			}
		})
	}
}
