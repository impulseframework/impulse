package condition

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// TestRequestFieldClassification pins what the deploy-time validation of an Execute
// grant reads off a condition: the names it reads off the row, the names it reads
// under new., and whether every new. atom compares against literals alone.
func TestRequestFieldClassification(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		source           string
		wantPreImage     []string
		wantPostImage    []string
		wantLiteralsOnly bool
	}{
		{
			name:             "a request field alone",
			source:           "new.role != 'Administrator'",
			wantPostImage:    []string{"role"},
			wantLiteralsOnly: true,
		},
		{
			name:             "a request field in a list of literals",
			source:           "new.role IN ('Reader', 'Editor')",
			wantPostImage:    []string{"role"},
			wantLiteralsOnly: true,
		},
		{
			name:             "a fact of the row beside a request field",
			source:           "heldElsewhere = false AND new.level <= 3",
			wantPreImage:     []string{"heldElsewhere"},
			wantPostImage:    []string{"level"},
			wantLiteralsOnly: true,
		},
		{
			name:             "the row alone reads no request field",
			source:           "state = 'open' AND owner = subject",
			wantPreImage:     []string{"owner", "state"},
			wantLiteralsOnly: true,
		},
		{
			name:          "a request field against the caller's roles is not a literal",
			source:        "new.role IN subject.ownRoles",
			wantPostImage: []string{"role"},
		},
		{
			name:          "a request field against the caller is not a literal",
			source:        "new.owner = subject",
			wantPostImage: []string{"owner"},
		},
		{
			name:          "a request field against an attribute of the row is not a literal",
			source:        "new.level <= ceiling",
			wantPreImage:  []string{"ceiling"},
			wantPostImage: []string{"level"},
		},
		{
			name:             "a null test on a request field compares nothing",
			source:           "new.note IS NULL",
			wantPostImage:    []string{"note"},
			wantLiteralsOnly: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			expr, err := Parse(tt.source)
			if err != nil {
				t.Fatalf("Parse(%q) error = %v", tt.source, err)
			}
			if got := PreImageBindings(expr); !slices.Equal(got, tt.wantPreImage) {
				t.Errorf("PreImageBindings() = %v, want %v", got, tt.wantPreImage)
			}
			if got := PostImageBindings(expr); !slices.Equal(got, tt.wantPostImage) {
				t.Errorf("PostImageBindings() = %v, want %v", got, tt.wantPostImage)
			}
			if got := PostImageAgainstLiteralsOnly(expr); got != tt.wantLiteralsOnly {
				t.Errorf("PostImageAgainstLiteralsOnly() = %v, want %v", got, tt.wantLiteralsOnly)
			}
		})
	}
}

// TestSettlePostImage pins how a condition is settled against a decoded request: an
// atom that reads new. becomes a constant, what reads the row is left standing, and an
// atom that reads no value never permits, whichever side of a negation it stands on.
func TestSettlePostImage(t *testing.T) {
	t.Parallel()

	launch := time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		source string
		post   map[string]any
		// want is the settled expression's text; TRUE and FALSE are the constants.
		want    string
		wantErr string
	}{
		{name: "a string that passes", source: "new.role != 'Administrator'", post: map[string]any{"role": "Reader"}, want: "TRUE"},
		{name: "a string that fails", source: "new.role != 'Administrator'", post: map[string]any{"role": "Administrator"}, want: "FALSE"},
		{name: "a list of literals", source: "new.role IN ('Reader', 'Editor')", post: map[string]any{"role": "Editor"}, want: "TRUE"},
		{name: "a number", source: "new.level <= 3", post: map[string]any{"level": int64(4)}, want: "FALSE"},
		{name: "a float", source: "new.ratio < 0.5", post: map[string]any{"ratio": 0.25}, want: "TRUE"},
		{name: "a bool", source: "new.urgent = false", post: map[string]any{"urgent": false}, want: "TRUE"},
		{name: "an instant", source: "new.at >= '2027-01-01T00:00:00Z'", post: map[string]any{"at": launch}, want: "TRUE"},
		{name: "a date", source: "new.day < '2027-01-01'", post: map[string]any{"day": Date{Year: 2027, Month: time.March, Day: 1}}, want: "FALSE"},
		{name: "no value never permits", source: "new.role != 'Administrator'", post: map[string]any{"role": nil}, want: "FALSE"},
		{name: "no value never permits under a negation", source: "NOT (new.role = 'Administrator')", post: map[string]any{"role": nil}, want: "FALSE"},
		{name: "a null test reads the absence itself", source: "new.note IS NULL", post: map[string]any{"note": nil}, want: "TRUE"},
		{name: "the row's part is left standing", source: "heldElsewhere = false AND new.level <= 3", post: map[string]any{"level": int64(2)}, want: "heldElsewhere = false"},
		{name: "a failed request part settles the whole conjunction", source: "heldElsewhere = false AND new.level <= 3", post: map[string]any{"level": int64(9)}, want: "FALSE"},
		{name: "a passed request part settles the whole disjunction", source: "heldElsewhere = false OR new.level <= 3", post: map[string]any{"level": int64(1)}, want: "TRUE"},
		{name: "a failed request part leaves the other side of a disjunction", source: "heldElsewhere = false OR new.level <= 3", post: map[string]any{"level": int64(9)}, want: "heldElsewhere = false"},
		{name: "a condition that reads no request field is returned as it is", source: "state = 'open'", post: map[string]any{}, want: "state = 'open'"},
		{name: "a name the request does not carry is a fault, not a refusal", source: "new.role != 'Administrator'", post: map[string]any{}, wantErr: "new.role names no proposed value"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			expr, err := Parse(tt.source)
			if err != nil {
				t.Fatalf("Parse(%q) error = %v", tt.source, err)
			}
			got, err := SettlePostImage(expr, tt.post)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("SettlePostImage() error = %v, want one containing %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("SettlePostImage() error = %v", err)
			}
			if got.String() != tt.want {
				t.Errorf("SettlePostImage() = %s, want %s", got.String(), tt.want)
			}
		})
	}
}

// TestAllOf pins the conjunction a list field settles to: one element that fails
// refuses, the elements that pass drop out, and a part left standing stands once.
func TestAllOf(t *testing.T) {
	t.Parallel()

	row, err := Parse("heldElsewhere = false")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	tests := []struct {
		name     string
		operands []Expr
		want     string
	}{
		{name: "every element passes", operands: []Expr{Truth{Value: true}, Truth{Value: true}}, want: "TRUE"},
		{name: "one element fails", operands: []Expr{Truth{Value: true}, Truth{Value: false}, row}, want: "FALSE"},
		{name: "the row's part stands once", operands: []Expr{row, Truth{Value: true}, row}, want: "heldElsewhere = false"},
		{name: "nothing to judge permits", operands: nil, want: "TRUE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := AllOf(tt.operands).String(); got != tt.want {
				t.Errorf("AllOf() = %s, want %s", got, tt.want)
			}
		})
	}
}
