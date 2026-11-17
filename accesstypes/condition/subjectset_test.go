package condition

import (
	"slices"
	"strings"
	"testing"
)

// TestParseWho pins the shape of a subject set's who: part: one test or several
// joined by OR, each tying a column of the row to the requester, and nothing
// else. A who: that does not tie the row to the requester would make the set
// everyone's rows, so every other form is refused with the form to write.
func TestParseWho(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		source  string
		want    []WhoTest
		wantErr string
	}{
		{name: "the requester's own column", source: "UserID = subject", want: []WhoTest{{Column: "UserID"}}},
		{name: "a column in another set", source: "GroupID IN subject.groups", want: []WhoTest{{Column: "GroupID", Set: "groups"}}},
		{
			name:   "several tests joined by OR",
			source: "UserID = subject OR GroupID IN subject.groups OR DeputyID = subject",
			want:   []WhoTest{{Column: "UserID"}, {Column: "GroupID", Set: "groups"}, {Column: "DeputyID"}},
		},
		{name: "parentheses and keyword case are the grammar's", source: "(UserID = subject) or GroupID in subject.groups", want: []WhoTest{{Column: "UserID"}, {Column: "GroupID", Set: "groups"}}},
		{name: "a test against a literal", source: "Permission = 'develop'", wantErr: `who: "Permission = 'develop'" does not tie the row to the requester`},
		{name: "a literal test beside a requester test", source: "UserID = subject OR Permission = 'develop'", wantErr: `who: "Permission = 'develop'" does not tie the row to the requester`},
		{name: "tests joined by AND", source: "UserID = subject AND Permission = 'develop'", wantErr: `who: "UserID = subject AND Permission = 'develop'" does not tie the row to the requester`},
		{name: "everyone but the requester", source: "UserID != subject", wantErr: `who: "UserID != subject" does not tie the row to the requester`},
		{name: "a negated test", source: "NOT (UserID = subject)", wantErr: `who: "NOT UserID = subject" does not tie the row to the requester`},
		{name: "a column outside another set", source: "GroupID NOT IN subject.groups", wantErr: `who: "GroupID NOT IN subject.groups" does not tie the row to the requester`},
		{name: "a null test", source: "UserID IS NOT NULL", wantErr: `who: "UserID IS NOT NULL" does not tie the row to the requester`},
		{name: "a subject value", source: "Rank = subject.rank", wantErr: `who: "Rank = subject.rank" does not tie the row to the requester`},
		{name: "a proposed value", source: "new.UserID = subject", wantErr: `who: "new.UserID = subject" does not tie the row to the requester`},
		{name: "a literal list", source: "UserID IN ('u1', 'u2')", wantErr: `who: "UserID IN ('u1', 'u2')" does not tie the row to the requester`},
		{name: "text the grammar refuses", source: "UserID = ", wantErr: "who: condition: expected an operand at position 9; each who: test is written Column = subject or Column IN subject.<set>, and several are joined by OR"},
		{name: "nothing written", source: "  ", wantErr: "who: condition: empty condition"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseWho(tt.source)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ParseWho(%q) error = %v, want one containing %q", tt.source, err, tt.wantErr)
				}
				if !strings.Contains(err.Error(), "Column = subject or Column IN subject.<set>") {
					t.Errorf("ParseWho(%q) error = %v, want it to say what to write", tt.source, err)
				}

				return
			}
			if err != nil {
				t.Fatalf("ParseWho(%q) error = %v", tt.source, err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("ParseWho(%q) = %v, want %v", tt.source, got, tt.want)
			}

			// The tests read back as the condition they were written as.
			again, err := ParseWho(WhoExpr(got).String())
			if err != nil || !slices.Equal(again, got) {
				t.Errorf("ParseWho(WhoExpr(%v).String()) = %v, %v; want the same tests", got, again, err)
			}
		})
	}
}

// TestParseWhere pins the shape of a subject set's where: part: a condition over
// the row's own columns, compared to literals and to now, and the refusal of
// everything that reads something else.
func TestParseWhere(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		source  string
		want    string
		wantErr string
	}{
		{name: "a comparison to a literal", source: "Permission = 'develop'", want: "Permission = 'develop'"},
		{
			name:   "the worked example",
			source: "Permission IN ('view', 'develop') AND (ExpiresAt IS NULL OR ExpiresAt > now)",
			want:   "Permission IN ('view', 'develop') AND (ExpiresAt IS NULL OR ExpiresAt > now)",
		},
		{name: "the orderings, NOT and a number", source: "NOT (Rank < 3) AND Seats >= 2.5 AND Active != false", want: "NOT Rank < 3 AND Seats >= 2.5 AND Active != false"},
		{name: "the requester", source: "UserID = subject", wantErr: `where: "UserID = subject" names the requester; a where: reads the row's own columns alone, and the test that ties a row to the requester belongs in who:`},
		{name: "a subject value", source: "Rank <= subject.rank", wantErr: `where: "Rank <= subject.rank" names the requester`},
		{name: "a subject set", source: "Permission = 'x' AND GroupID IN subject.groups", wantErr: `where: "GroupID IN subject.groups" names the requester`},
		{name: "a proposed value", source: "new.Permission = 'develop'", wantErr: `where: "new.Permission = 'develop'" reads a proposed value (new.); a set's rows are read as the database holds them, so name the column alone`},
		{name: "a proposed value under a null test", source: "new.ExpiresAt IS NULL", wantErr: `where: "new.ExpiresAt IS NULL" reads a proposed value (new.)`},
		{name: "now on the left", source: "now < '2027-01-01T00:00:00Z'", wantErr: `where: "now < '2027-01-01T00:00:00Z'" does not read a column of the row; write the column on the left, as in ExpiresAt > now`},
		{name: "a temporal function", source: "dayOfWeek(now, local) IN ('mon')", wantErr: `where: "dayOfWeek(now, local) IN ('mon')" calls dayOfWeek, which the database does not evaluate; a where: test compares a column of the row to a literal or to now`},
		{name: "a column on the right", source: "StartsAt < EndsAt", wantErr: "an attribute may stand on the right only against a new.-qualified left side (old-vs-new); a where: test compares a column of the row to a literal or to now"},
		{name: "nothing written", source: "", wantErr: "where: condition: empty condition; a where: test compares a column of the row to a literal or to now"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			expr, err := ParseWhere(tt.source)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ParseWhere(%q) error = %v, want one containing %q", tt.source, err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("ParseWhere(%q) error = %v", tt.source, err)
			}
			if got := expr.String(); got != tt.want {
				t.Errorf("ParseWhere(%q) = %q, want %q", tt.source, got, tt.want)
			}
		})
	}
}

// TestRenameBindings pins the rendering of a subject set's parts in the schema's
// words: every name the row's columns go by is replaced, and the facts are not.
func TestRenameBindings(t *testing.T) {
	t.Parallel()

	columns := map[string]string{"UserID": "UserId", "GroupID": "GroupId", "ExpiresAt": "ExpiresAt", "Level": "Level", "Rank": "Rank"}
	rename := func(name string) string {
		return columns[name]
	}

	tests := []struct {
		source string
		want   string
	}{
		{source: "UserID = subject OR GroupID IN subject.groups", want: "UserId = subject OR GroupId IN subject.groups"},
		{source: "Level IN ('view', 'develop') AND (ExpiresAt IS NULL OR NOT (ExpiresAt <= now))", want: "Level IN ('view', 'develop') AND (ExpiresAt IS NULL OR NOT ExpiresAt <= now)"},
		{source: "NOT (UserID IS NULL OR GroupID = 'none')", want: "NOT (UserId IS NULL OR GroupId = 'none')"},
		{source: "new.Rank <= Rank AND now < '2027-01-01T00:00:00Z' AND dayOfWeek(now, local) = 'mon'", want: "new.Rank <= Rank AND now < '2027-01-01T00:00:00Z' AND dayOfWeek(now, local) = 'mon'"},
	}
	for _, tt := range tests {
		t.Run(tt.source, func(t *testing.T) {
			t.Parallel()

			expr, err := Parse(tt.source)
			if err != nil {
				t.Fatalf("Parse(%q) error = %v", tt.source, err)
			}
			if got := RenameBindings(expr, rename).String(); got != tt.want {
				t.Errorf("RenameBindings(%q) = %q, want %q", tt.source, got, tt.want)
			}
			if got := expr.String(); strings.Contains(got, "UserId") || strings.Contains(got, "GroupId") {
				t.Errorf("RenameBindings changed the expression it was given: %q", got)
			}
		})
	}
}
