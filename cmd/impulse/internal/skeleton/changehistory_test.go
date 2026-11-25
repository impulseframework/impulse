package skeleton

import (
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/auditlog"
	"github.com/impulseframework/impulse/cmd/impulse/internal/names"
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
	if len(statements) != 1 {
		t.Fatalf("resource.DataChangeEventsDDL(SpannerDBType, KeepChangeHistoryForever()) declares %d statements, want 1", len(statements))
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

// TestCandidateAuditRecordsMigration holds each auth's sessions migration in every
// candidate to the audit records table's statements as the auditlog package declares them,
// under the auth's prefix and kept forever: the table a new application's auth writes its
// audit log to.
func TestCandidateAuditRecordsMigration(t *testing.T) {
	t.Parallel()

	for candidate, specs := range candidateAuths {
		for i := range specs {
			spec := specs[i]
			t.Run(candidate+"/"+spec.Name, func(t *testing.T) {
				t.Parallel()

				sub, err := FS(candidate)
				if err != nil {
					t.Fatalf("FS() error = %v", err)
				}
				prefix := names.Pascal(spec.Name)
				file := fmt.Sprintf("schema/migrations/%06d_%sSessions.up.sql", candidateMigrations[candidate][spec.Name][1], prefix)
				migration, err := fs.ReadFile(sub, file)
				if err != nil {
					t.Fatalf("fs.ReadFile(%s) error = %v", file, err)
				}
				statements := auditlog.SpannerDDL(prefix+"SessionAuditRecords", auditlog.KeepForever())
				if len(statements) == 0 {
					t.Fatal("auditlog.SpannerDDL() declares no statement")
				}
				for _, stmt := range statements {
					if !strings.Contains(string(migration), stmt+";") {
						t.Errorf("the candidate's %s does not carry the statement:\n%s\ncopy the statements of auditlog.SpannerDDL into it", file, stmt)
					}
				}
			})
		}
	}
}
