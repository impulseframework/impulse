package accesstypes

import (
	"time"

	"github.com/go-playground/errors/v5"
)

// Period is when a role assignment counts: from its start, where it has one, until its
// end, where it has one. The zero Period has neither and always counts: an assignment
// with no start counts from the moment it is given, and one with no end until it is
// removed. A person's own role, a role a group holds and a person's place in a group
// each carry one, and everything that reads an assignment leaves out one outside its
// period: no row is written when a period begins or ends.
//
// The start is the first instant the assignment counts and the end the first it no
// longer does, so one period may end at the instant the next begins.
type Period struct {
	// Start is the first instant the assignment counts; the zero time is no start.
	Start time.Time
	// End is the first instant the assignment no longer counts; the zero time is no end.
	End time.Time
}

// NewPeriod answers the period between two optional instants, each nil for none, in UTC.
// A period that ends at or before its start never counts and is refused.
func NewPeriod(start, end *time.Time) (Period, error) {
	var p Period
	if start != nil {
		p.Start = start.UTC()
	}
	if end != nil {
		p.End = end.UTC()
	}
	if err := p.Validate(); err != nil {
		return Period{}, err
	}

	return p, nil
}

// Validate refuses a period that ends at or before its start.
func (p Period) Validate() error {
	if !p.Start.IsZero() && !p.End.IsZero() && !p.End.After(p.Start) {
		return errors.Newf("the period ends at %s, which is not after its start at %s: an assignment counts from its start until its end", p.End.Format(time.RFC3339), p.Start.Format(time.RFC3339))
	}

	return nil
}

// IsZero reports whether the period has neither a start nor an end.
func (p Period) IsZero() bool {
	return p.Start.IsZero() && p.End.IsZero()
}

// Contains reports whether an assignment with the period counts at the instant.
func (p Period) Contains(at time.Time) bool {
	return !p.Upcoming(at) && !p.Ended(at)
}

// Upcoming reports whether the period has not begun at the instant.
func (p Period) Upcoming(at time.Time) bool {
	return !p.Start.IsZero() && at.Before(p.Start)
}

// Ended reports whether the period is over at the instant: an assignment that has ended
// counts for nothing and never will again, which is what makes it safe to remove.
func (p Period) Ended(at time.Time) bool {
	return !p.End.IsZero() && !at.Before(p.End)
}

// Intersect answers the period in which both periods count: the later start and the
// earlier end. A person holds a group's role while their place in the group and the
// group's role both count. The answer may end at or before its start, and then never
// counts.
func (p Period) Intersect(other Period) Period {
	out := p
	if !other.Start.IsZero() && (out.Start.IsZero() || other.Start.After(out.Start)) {
		out.Start = other.Start
	}
	if !other.End.IsZero() && (out.End.IsZero() || other.End.Before(out.End)) {
		out.End = other.End
	}

	return out
}

// StartPtr and EndPtr answer the period's instants as a store's nullable columns hold
// them: nil for none.
func (p Period) StartPtr() *time.Time {
	if p.Start.IsZero() {
		return nil
	}
	start := p.Start

	return &start
}

// EndPtr answers the period's end, nil for none (see StartPtr).
func (p Period) EndPtr() *time.Time {
	if p.End.IsZero() {
		return nil
	}
	end := p.End

	return &end
}

// Equal reports whether the two periods have the same start and the same end, each
// compared as an instant: the same period read in another location is the same.
func (p Period) Equal(other Period) bool {
	return p.Start.Equal(other.Start) && p.End.Equal(other.End)
}
