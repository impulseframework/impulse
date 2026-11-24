package skeleton

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/resource"
)

// TestCandidateChangeHistoryMigration holds every candidate's first migration to the
// change history table's statements as the resource package declares them, with what a
// new application starts with: every record kept. A candidate's migration carries no row
// deletion policy.
func TestCandidateChangeHistoryMigration(t *testing.T) {
	t.Parallel()

	candidates, err := Candidates()
	if err != nil {
		t.Fatalf("Candidates() error = %v", err)
	}
	statements := resource.DataChangeEventsDDL(resource.SpannerDBType, resource.KeepChangeHistoryForever())
	if len(statements) != 2 {
		t.Fatalf("resource.DataChangeEventsDDL(SpannerDBType, KeepChangeHistoryForever()) declares %d statements, want 2", len(statements))
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
			if strings.Contains(string(migration), "ROW DELETION POLICY") {
				t.Errorf("the candidate's schema/migrations/000001_DataChangeEvents.up.sql carries a row deletion policy; a new application keeps its change history forever until its author declares a period")
			}
		})
	}
}
