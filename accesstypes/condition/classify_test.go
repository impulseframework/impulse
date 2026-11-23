package condition

import (
	"slices"
	"testing"
	"time"
)

// TestClassification pins the lexical classification the load-time and
// deploy-time validations build on: row-free-ness, the referenced binding,
// subject-set, and subject-value names, and whether the expression reads the
// time of the request alone, which the permission digest reports.
func TestClassification(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		source            string
		wantRowFree       bool
		wantBindings      []string
		wantSubjectSets   []string
		wantSubjectValues []string
		wantUsesNow       bool
		wantUsesPostImage bool
		wantComparesAttrs bool
		wantTimeOnly      bool
	}{
		{
			name:         "environment window is row-free",
			source:       "now < '2027-03-01T00:00:00Z' AND now >= '2027-01-01T00:00:00Z'",
			wantRowFree:  true,
			wantUsesNow:  true,
			wantTimeOnly: true,
		},
		{
			name:         "an expiry reads the time alone",
			source:       "now < '2099-01-01T00:00:00Z'",
			wantRowFree:  true,
			wantUsesNow:  true,
			wantTimeOnly: true,
		},
		{
			name:         "a negated instant reads the time alone",
			source:       "NOT (now >= '2027-01-01T00:00:00Z')",
			wantRowFree:  true,
			wantUsesNow:  true,
			wantTimeOnly: true,
		},
		{
			name:         "a row term ORed with an expiry reads the row",
			source:       "state = 'draft' OR now < '2099-01-01T00:00:00Z'",
			wantBindings: []string{"state"},
			wantUsesNow:  true,
		},
		{
			name:         "the subject compared with a row reads the row",
			source:       "owner = subject",
			wantBindings: []string{"owner"},
		},
		{
			name:              "now against a subject value is row-free, yet reads a fact about the caller",
			source:            "now < subject.contractEnd",
			wantRowFree:       true,
			wantSubjectValues: []string{"contractEnd"},
			wantUsesNow:       true,
		},
		{
			name:            "subject-set membership references a row",
			source:          "crew IN subject.crews",
			wantBindings:    []string{"crew"},
			wantSubjectSets: []string{"crews"},
		},
		{
			name:         "plain comparison references a row",
			source:       "state = 'open'",
			wantBindings: []string{"state"},
		},
		{
			name:         "null test references a row",
			source:       "assignee IS NULL",
			wantBindings: []string{"assignee"},
		},
		{
			name:              "post-image collapses to the binding name",
			source:            "assignee IS NULL AND new.assignee = subject",
			wantBindings:      []string{"assignee"},
			wantUsesPostImage: true,
		},
		{
			name:              "threshold pattern references the subject value",
			source:            "new.estimatedCost <= subject.approvalLimit",
			wantBindings:      []string{"estimatedCost"},
			wantSubjectValues: []string{"approvalLimit"},
			wantUsesPostImage: true,
		},
		{
			name:              "old-vs-new references both sides' bindings",
			source:            "new.priority <= priority AND state = 'draft'",
			wantBindings:      []string{"priority", "state"},
			wantUsesPostImage: true,
			wantComparesAttrs: true,
		},
		{
			name:         "bindings dedupe and sort",
			source:       "b = 1 AND a = 2 AND new.b = 3 AND contractEnd > now",
			wantBindings: []string{"a", "b", "contractEnd"},
			wantUsesNow:  true,

			wantUsesPostImage: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			expr, err := Parse(tt.source)
			if err != nil {
				t.Fatalf("Parse(%q) error = %v", tt.source, err)
			}

			if got := RowFree(expr); got != tt.wantRowFree {
				t.Errorf("RowFree() = %v, want %v", got, tt.wantRowFree)
			}
			if got := Bindings(expr); !slices.Equal(got, tt.wantBindings) {
				t.Errorf("Bindings() = %v, want %v", got, tt.wantBindings)
			}
			if got := SubjectSets(expr); !slices.Equal(got, tt.wantSubjectSets) {
				t.Errorf("SubjectSets() = %v, want %v", got, tt.wantSubjectSets)
			}
			if got := SubjectValues(expr); !slices.Equal(got, tt.wantSubjectValues) {
				t.Errorf("SubjectValues() = %v, want %v", got, tt.wantSubjectValues)
			}
			if got := ComparesAttributes(expr); got != tt.wantComparesAttrs {
				t.Errorf("ComparesAttributes() = %v, want %v", got, tt.wantComparesAttrs)
			}
			if got := UsesNow(expr); got != tt.wantUsesNow {
				t.Errorf("UsesNow() = %v, want %v", got, tt.wantUsesNow)
			}
			if got := UsesPostImage(expr); got != tt.wantUsesPostImage {
				t.Errorf("UsesPostImage() = %v, want %v", got, tt.wantUsesPostImage)
			}
			if got := TimeOnly(expr); got != tt.wantTimeOnly {
				t.Errorf("TimeOnly() = %v, want %v", got, tt.wantTimeOnly)
			}
			assertTimeOnlyFolds(t, expr, tt.wantTimeOnly)
		})
	}
}

// assertTimeOnlyFolds pins what TimeOnly promises the digest's readers: an
// expression that reads the time alone folds to one Truth for facts that
// carry an instant and a zone, and a row-free expression that is not time
// only (it names a fact about the caller) is left for the database.
func assertTimeOnlyFolds(t *testing.T, expr Expr, timeOnly bool) {
	t.Helper()

	if !timeOnly && !RowFree(expr) {
		return
	}
	facts := NewFacts().WithNow(time.Date(2026, 11, 22, 15, 30, 0, 0, time.UTC)).WithZone(time.UTC)
	folded, err := Fold(expr, facts)
	if err != nil {
		t.Fatalf("Fold() error = %v", err)
	}
	if _, settled := folded.(Truth); settled != timeOnly {
		t.Errorf("Fold() = %v, settled to one Truth = %v, want %v", folded, settled, timeOnly)
	}
}
