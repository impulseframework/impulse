package check

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/impulseframework/impulse/app"
)

// changeHistoryMigration is the library's statements for 365 days as an application's
// first migration carries them.
const changeHistoryMigration = `-- The change history.

CREATE TABLE DataChangeEvents (
  TableName STRING(MAX) NOT NULL,
  RowId STRING(MAX) NOT NULL,
  Sequence INT64 NOT NULL,
  EventTime TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp=true),
  EventSource STRING(MAX) NOT NULL,
  ChangeSet JSON,
  TenantId STRING(MAX) NOT NULL,
) PRIMARY KEY (TableName, RowId, Sequence, EventTime);

CREATE INDEX DataChangeEventsByTenant ON DataChangeEvents(TenantId, EventTime DESC, TableName, RowId, Sequence DESC);

ALTER TABLE DataChangeEvents ADD ROW DELETION POLICY (OLDER_THAN(EventTime, INTERVAL 365 DAY));
`

// The change history statements the cases vary: the table's tenant column, and the
// library's index.
const (
	changeHistoryTenantLine = "  TenantId STRING(MAX) NOT NULL,\n"
	changeHistoryIndex      = "CREATE INDEX DataChangeEventsByTenant ON DataChangeEvents(TenantId, EventTime DESC, TableName, RowId, Sequence DESC);\n"
)

func TestChangeHistory(t *testing.T) {
	t.Parallel()

	moduleDir, err := filepath.Abs(filepath.Join("testdata", "frameworkmodule"))
	if err != nil {
		t.Fatal(err)
	}
	oldModuleDir, err := filepath.Abs(filepath.Join("testdata", "oldframeworkmodule"))
	if err != nil {
		t.Fatal(err)
	}
	libraryDir, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	const migration = "schema/migrations/000001_DataChangeEvents.up.sql"
	application := func(first string, more map[string]string) map[string]string {
		files := map[string]string{
			"cmd/generate/resourcegenerator/main.go": featureProgram,
			"pkg/resources/doc.go":                   "package resources\n",
			"web/angular.json":                       "{}\n",
			migration:                                first,
		}
		for rel, content := range more {
			files[rel] = content
		}

		return files
	}
	withoutTenant := strings.Replace(changeHistoryMigration, changeHistoryTenantLine, "", 1)
	withoutIndex := strings.Replace(changeHistoryMigration, changeHistoryIndex, "", 1)
	columnFinding := migration + ": the DataChangeEvents table has no TenantId column. Every tracked write fills the columns resource.DataChangeEventsDDL declares (resource/change_history.go), so a tracked write to this table fails. Add the column in a new migration under schema/migrations; change the migration that creates the table instead if no database has run it yet."
	indexFinding := "no migration creates the index DataChangeEventsByTenant as resource.DataChangeEventsDDL declares it (resource/change_history.go). One tenant's change records are read by it. Copy the statement into a new migration under schema/migrations."
	passed := "DataChangeEvents carries the columns and the index of the resource package's statements"
	library := map[string]fakeAnswer{goListResource: {out: moduleDir + "\n"}}

	tests := []struct {
		name        string
		files       map[string]string
		answers     map[string]fakeAnswer
		wantStatus  Status
		wantSummary string
		wantDetails []string
		wantCalls   []string
	}{
		{
			name:       "no site generator",
			files:      map[string]string{"pkg/resources/doc.go": "package resources\n", migration: changeHistoryMigration},
			wantStatus: Skip, wantSummary: "no site generator",
		},
		{
			name:       "no migration creates the table",
			files:      application("CREATE TABLE Ships (\n  Id STRING(36) NOT NULL,\n) PRIMARY KEY (Id);\n", nil),
			wantStatus: Skip, wantSummary: "no migration creates the DataChangeEvents table",
		},
		{
			name:       "the table and the index as the library declares them",
			files:      application(changeHistoryMigration, nil),
			answers:    library,
			wantStatus: Pass, wantSummary: passed,
			wantCalls: []string{goListResource},
		},
		{
			name:       "the library's own statements",
			files:      application(changeHistoryMigration, nil),
			answers:    map[string]fakeAnswer{goListResource: {out: libraryDir + "\n"}},
			wantStatus: Pass, wantSummary: passed,
			wantCalls: []string{goListResource},
		},
		{
			name: "the column created by a table statement laid out another way",
			files: application("CREATE TABLE `DataChangeEvents` (TableName STRING(MAX) NOT NULL, RowId STRING(MAX) NOT NULL, Sequence INT64 NOT NULL, "+
				"EventTime TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp=true), EventSource STRING(MAX) NOT NULL, ChangeSet JSON, "+
				"tenantid STRING(MAX) NOT NULL, Origin STRING(MAX)) PRIMARY KEY (TableName, RowId, Sequence, EventTime);\n\n"+changeHistoryIndex, nil),
			answers:    library,
			wantStatus: Pass, wantSummary: passed,
			wantCalls: []string{goListResource},
		},
		{
			name: "the column added by a later migration",
			files: application(strings.Replace(withoutTenant, changeHistoryIndex, "", 1), map[string]string{
				"schema/migrations/000007_ChangeTenant.up.sql": "-- The tenant of each record.\nalter table DataChangeEvents\n  add column if not exists `TenantId` STRING(MAX) NOT NULL DEFAULT (\"\");\n\n" + changeHistoryIndex,
			}),
			answers:    library,
			wantStatus: Pass, wantSummary: passed,
			wantCalls: []string{goListResource},
		},
		{
			name:       "a table from before the tenant column",
			files:      application(strings.Replace(withoutTenant, changeHistoryIndex, "", 1), nil),
			answers:    library,
			wantStatus: Fail, wantSummary: "2 change history problem(s)",
			wantDetails: []string{columnFinding, indexFinding},
			wantCalls:   []string{goListResource},
		},
		{
			name: "the column named only in a comment and inside another name",
			files: application(strings.Replace(changeHistoryMigration, changeHistoryTenantLine, "  TenantIdentity STRING(MAX),\n  -- TenantId comes later.\n  /* TenantId STRING(MAX) NOT NULL,\n     once the records are filled. */\n", 1), map[string]string{
				"schema/migrations/000007_ChangeTenant.up.sql": "-- ALTER TABLE DataChangeEvents ADD COLUMN TenantId STRING(MAX) NOT NULL;\nSELECT 1;\n",
			}),
			answers:    library,
			wantStatus: Fail, wantSummary: "1 change history problem(s)",
			wantDetails: []string{columnFinding},
			wantCalls:   []string{goListResource},
		},
		{
			name:       "no index",
			files:      application(withoutIndex, nil),
			answers:    library,
			wantStatus: Fail, wantSummary: "1 change history problem(s)",
			wantDetails: []string{indexFinding},
			wantCalls:   []string{goListResource},
		},
		{
			name:       "the index written in lower case, spaced another way, with IF NOT EXISTS",
			files:      application(strings.Replace(changeHistoryMigration, changeHistoryIndex, "create index if not exists DataChangeEventsByTenant on DataChangeEvents (TenantId, EventTime desc, TableName, RowId, Sequence desc);\n", 1), nil),
			answers:    library,
			wantStatus: Pass, wantSummary: passed,
			wantCalls: []string{goListResource},
		},
		{
			name:       "an index of that name over other columns",
			files:      application(strings.Replace(changeHistoryMigration, changeHistoryIndex, "CREATE INDEX DataChangeEventsByTenant ON DataChangeEvents(TenantId, EventTime DESC);\n", 1), nil),
			answers:    library,
			wantStatus: Fail, wantSummary: "1 change history problem(s)",
			wantDetails: []string{indexFinding},
			wantCalls:   []string{goListResource},
		},
		{
			name:       "the framework module predates the library's change history statements",
			files:      application(changeHistoryMigration, nil),
			answers:    map[string]fakeAnswer{goListResource: {out: oldModuleDir + "\n"}},
			wantStatus: Skip, wantSummary: "the framework module the application pins declares no resource.DataChangeEventsDDL",
			wantCalls: []string{goListResource},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			write := func(rel, content string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			write("go.mod", "module example.com/harbor\n\ngo 1.26.6\n")
			for rel, content := range tt.files {
				write(rel, content)
			}
			a, err := app.Discover(root)
			if err != nil {
				t.Fatalf("app.Discover() error = %v", err)
			}
			exec := &fakeExec{t: t, answers: tt.answers}
			got := changeHistory{}.Run(context.Background(), &Env{App: a, Exec: exec})
			want := Result{Name: "change-history", Status: tt.wantStatus, Summary: tt.wantSummary, Details: tt.wantDetails}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("Run() mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantCalls, exec.calls); diff != "" {
				t.Errorf("commands mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestChangeHistoryMeaning(t *testing.T) {
	t.Parallel()

	meaning := Meaning(changeHistoryName)
	for _, want := range []string{"`TenantId`", "resource.DataChangeEventsDDL(resource.SpannerDBType, period)", "ALTER TABLE DataChangeEvents ADD COLUMN"} {
		if !strings.Contains(meaning, want) {
			t.Errorf("Meaning() lacks %q", want)
		}
	}
}
