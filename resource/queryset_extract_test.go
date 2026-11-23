package resource

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestExtractWithClause(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		query      string
		wantWith   string
		wantRemain string
	}{
		{
			name:       "no WITH clause",
			query:      "SELECT * FROM table",
			wantWith:   "",
			wantRemain: "SELECT * FROM table",
		},
		{
			name:       "simple WITH clause",
			query:      "WITH a AS (SELECT 1) SELECT * FROM a",
			wantWith:   "WITH a AS (SELECT 1)",
			wantRemain: " SELECT * FROM a",
		},
		{
			name:       "multiple WITH clauses (CTE)",
			query:      "WITH a AS (SELECT 1), b AS (SELECT 2) SELECT * FROM a CROSS JOIN b",
			wantWith:   "WITH a AS (SELECT 1), b AS (SELECT 2)",
			wantRemain: " SELECT * FROM a CROSS JOIN b",
		},
		{
			name:       "nested WITH clause",
			query:      "WITH a AS (WITH b AS (SELECT 1) SELECT * FROM b) SELECT * FROM a",
			wantWith:   "WITH a AS (WITH b AS (SELECT 1) SELECT * FROM b)",
			wantRemain: " SELECT * FROM a",
		},
		{
			name:       "newline formatting",
			query:      "WITH a AS (\n\tSELECT 1\n)\nSELECT * FROM a",
			wantWith:   "WITH a AS (\n\tSELECT 1\n)",
			wantRemain: "\nSELECT * FROM a",
		},
		{
			name: "newline formatting",
			query: `
-- Comment
WITH RankedThings AS (
    SELECT
        ci.CreatedAt,
        ci.EndedAt,
        ci.UpdatedAt,
        ROW_NUMBER() OVER (
            PARTITION BY c.Id 
            ORDER BY ci.CreatedAt DESC
        ) AS RowNum
    FROM ThatThings AS ci
    JOIN ThingActiveInstances AS cai ON cai.ThingInstanceId = ci.ThingInstanceId
    JOIN Things AS c ON c.Id = cai.ThingId
)
SELECT
    CreatedAt,
    EndedAt,
    UpdatedAt
FROM RankedThings
WHERE RowNum = 1`,
			wantWith: `
-- Comment
WITH RankedThings AS (
    SELECT
        ci.CreatedAt,
        ci.EndedAt,
        ci.UpdatedAt,
        ROW_NUMBER() OVER (
            PARTITION BY c.Id 
            ORDER BY ci.CreatedAt DESC
        ) AS RowNum
    FROM ThatThings AS ci
    JOIN ThingActiveInstances AS cai ON cai.ThingInstanceId = ci.ThingInstanceId
    JOIN Things AS c ON c.Id = cai.ThingId
)`,
			wantRemain: `
SELECT
    CreatedAt,
    EndedAt,
    UpdatedAt
FROM RankedThings
WHERE RowNum = 1`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotWith, gotRemain := extractWithClause(tt.query)
			if diff := cmp.Diff(tt.wantWith, gotWith); diff != "" {
				t.Errorf("extractWithClause() withClause mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantRemain, gotRemain); diff != "" {
				t.Errorf("extractWithClause() remainingQuery mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestWithTableNames pins the WITH tables a view's query opens with: each item's name
// at the clause's own depth, after WITH or a comma, with or without a column list, and
// none from a WITH inside a body.
func TestWithTableNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{
			name:  "no WITH clause",
			query: "SELECT Id, Name FROM Squadrons",
		},
		{
			name:  "one WITH table",
			query: "WITH Squadrons AS (SELECT Id, Name FROM Units) SELECT Id FROM Squadrons",
			want:  []string{"Squadrons"},
		},
		{
			name:  "two WITH tables, the keyword in lower case",
			query: "with Pilots AS (SELECT Id FROM Crews), Squadrons AS (SELECT Id FROM Units, Pilots) SELECT Id FROM Squadrons",
			want:  []string{"Pilots", "Squadrons"},
		},
		{
			name:  "a WITH table with a column list",
			query: "WITH Squadrons (Id, Name) AS (SELECT Id, Label FROM Units) SELECT Id FROM Squadrons",
			want:  []string{"Squadrons"},
		},
		{
			name:  "a recursive WITH",
			query: "WITH RECURSIVE Squadrons AS (SELECT 1 AS Id UNION ALL SELECT Id + 1 FROM Squadrons WHERE Id < 3) SELECT Id FROM Squadrons",
			want:  []string{"Squadrons"},
		},
		{
			name:  "a WITH inside a WITH table's body",
			query: "WITH Wrapped AS (WITH Squadrons AS (SELECT Id FROM Units) SELECT Id FROM Squadrons) SELECT Id FROM Wrapped",
			want:  []string{"Wrapped"},
		},
		{
			name:  "a WITH inside the query's body",
			query: "SELECT Id FROM (WITH Squadrons AS (SELECT Id FROM Units) SELECT Id FROM Squadrons)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.want, withTableNames(tt.query)); diff != "" {
				t.Errorf("withTableNames() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
