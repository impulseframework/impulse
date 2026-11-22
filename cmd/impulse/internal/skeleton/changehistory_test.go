package skeleton

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/resource"
)

// TestCandidateChangeHistoryMigration holds every candidate's first migration to the
// change history table's statements as the resource package declares them, with the
// period a new application starts with: 365 days. A new application writes its change
// records to the table its candidate's migration makes.
func TestCandidateChangeHistoryMigration(t *testing.T) {
	t.Parallel()

	candidates, err := Candidates()
	if err != nil {
		t.Fatalf("Candidates() error = %v", err)
	}
	statements := resource.DataChangeEventsDDL(resource.SpannerDBType, resource.KeepChangeHistoryDays(365))
	if len(statements) != 3 {
		t.Fatalf("resource.DataChangeEventsDDL(SpannerDBType, KeepChangeHistoryDays(365)) declares %d statements, want 3", len(statements))
	}

	for _, candidate := range candidates {
		t.Run(candidate.Name, func(t *testing.T) {
			t.Parallel()

			sub, err := FS(candidate.Name)
			if err != nil {
				t.Fatalf("FS() error = %v", err)
			}
			migration, err := fs.ReadFile(sub, "schema/migrations/000001_DataChangeEvents.up.sql")
			if err != nil {
				t.Fatalf("fs.ReadFile() error = %v", err)
			}
			for _, stmt := range statements {
				if !strings.Contains(string(migration), stmt+";") {
					t.Errorf("the candidate's schema/migrations/000001_DataChangeEvents.up.sql does not carry the statement:\n%s\ncopy the statements of resource.DataChangeEventsDDL into it", stmt)
				}
			}
		})
	}
}
