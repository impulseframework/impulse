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

// deleteTokensMigration is the library's statements as an application's migration
// carries them.
const deleteTokensMigration = `-- The delete tokens.

CREATE TABLE DeleteTokens (
  Id STRING(MAX) NOT NULL,
  TableName STRING(MAX) NOT NULL,
  RowKey STRING(MAX) NOT NULL,
  SessionId STRING(MAX) NOT NULL,
  PrincipalUser STRING(MAX),
  PrincipalRole STRING(MAX),
  CreatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp=true),
  ExpiresAt TIMESTAMP NOT NULL,
) PRIMARY KEY (Id);

ALTER TABLE DeleteTokens ADD ROW DELETION POLICY (OLDER_THAN(ExpiresAt, INTERVAL 1 DAY));
`

// The delete tokens statements the cases vary: the table's role column and its key.
const (
	deleteTokensRoleLine = "  PrincipalRole STRING(MAX),\n"
	deleteTokensKey      = ") PRIMARY KEY (Id);\n"
)

func TestDeleteTokens(t *testing.T) {
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
	const (
		first     = "schema/migrations/000001_Baseline.up.sql"
		migration = "schema/migrations/000006_DeleteTokens.up.sql"
		baseline  = "CREATE TABLE Ships (\n  Id STRING(36) NOT NULL,\n) PRIMARY KEY (Id);\n"
	)
	application := func(more map[string]string) map[string]string {
		files := map[string]string{
			"cmd/generate/resourcegenerator/main.go": featureProgram,
			"pkg/resources/doc.go":                   "package resources\n",
			"web/angular.json":                       "{}\n",
			first:                                    baseline,
		}
		for rel, content := range more {
			files[rel] = content
		}

		return files
	}
	withoutRole := strings.Replace(deleteTokensMigration, deleteTokensRoleLine, "", 1)
	columnFinding := migration + ": the DeleteTokens table has no PrincipalRole column. A delete's question fills the columns resource.DeleteTokensDDL declares (resource/delete_token.go), so every delete of a record with rows under it fails. Add the column in a new migration under schema/migrations; change the migration that creates the table instead if no database has run it yet."
	keyFinding := migration + ": DeleteTokens is keyed by (TableName, RowKey, Id), and the resource package's statement keys it by (Id). A table's key cannot be altered: correct the migration that creates the table, and make again any database made from the old statement."
	passed := "DeleteTokens carries the columns and the key of the resource package's statements"
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
			files:      map[string]string{"pkg/resources/doc.go": "package resources\n", migration: deleteTokensMigration},
			wantStatus: Skip, wantSummary: "no site generator",
		},
		{
			name:       "the table as the library declares it",
			files:      application(map[string]string{migration: deleteTokensMigration}),
			answers:    library,
			wantStatus: Pass, wantSummary: passed,
			wantCalls: []string{goListResource},
		},
		{
			name:       "the library's own statements",
			files:      application(map[string]string{migration: deleteTokensMigration}),
			answers:    map[string]fakeAnswer{goListResource: {out: libraryDir + "\n"}},
			wantStatus: Pass, wantSummary: passed,
			wantCalls: []string{goListResource},
		},
		{
			name: "the table laid out another way",
			files: application(map[string]string{migration: "create table `DeleteTokens` (Id STRING(MAX) NOT NULL, tablename STRING(MAX) NOT NULL, RowKey STRING(MAX) NOT NULL, " +
				"SessionId STRING(MAX) NOT NULL, PrincipalUser STRING(MAX), `PrincipalRole` STRING(MAX), CreatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp=true), " +
				"ExpiresAt TIMESTAMP NOT NULL) primary key (`Id`);\n"}),
			answers:    library,
			wantStatus: Pass, wantSummary: passed,
			wantCalls: []string{goListResource},
		},
		{
			name: "the column added by a later migration",
			files: application(map[string]string{
				migration: withoutRole,
				"schema/migrations/000007_DeleteTokenRole.up.sql": "-- Tokens given to a role.\nalter table DeleteTokens\n  add column if not exists `PrincipalRole` STRING(MAX);\n",
			}),
			answers:    library,
			wantStatus: Pass, wantSummary: passed,
			wantCalls: []string{goListResource},
		},
		{
			name:       "no migration creates the table",
			files:      application(nil),
			answers:    library,
			wantStatus: Fail, wantSummary: "1 delete token problem(s)",
			wantDetails: []string{"no migration creates the DeleteTokens table, where the question a delete asks first leaves its token; copy its statements from resource.DeleteTokensDDL(resource.SpannerDBType) into a new migration under schema/migrations"},
			wantCalls:   []string{goListResource},
		},
		{
			name:       "a column missing",
			files:      application(map[string]string{migration: withoutRole}),
			answers:    library,
			wantStatus: Fail, wantSummary: "1 delete token problem(s)",
			wantDetails: []string{columnFinding},
			wantCalls:   []string{goListResource},
		},
		{
			name:       "another key",
			files:      application(map[string]string{migration: strings.Replace(deleteTokensMigration, deleteTokensKey, ") PRIMARY KEY (TableName, RowKey, Id);\n", 1)}),
			answers:    library,
			wantStatus: Fail, wantSummary: "1 delete token problem(s)",
			wantDetails: []string{keyFinding},
			wantCalls:   []string{goListResource},
		},
		{
			name:       "a column missing and another key",
			files:      application(map[string]string{migration: strings.Replace(withoutRole, deleteTokensKey, ") PRIMARY KEY (TableName, RowKey, Id);\n", 1)}),
			answers:    library,
			wantStatus: Fail, wantSummary: "2 delete token problem(s)",
			wantDetails: []string{columnFinding, keyFinding},
			wantCalls:   []string{goListResource},
		},
		{
			name:       "the framework module predates the library's delete tokens statements",
			files:      application(nil),
			answers:    map[string]fakeAnswer{goListResource: {out: oldModuleDir + "\n"}},
			wantStatus: Skip, wantSummary: "the framework module the application pins declares no resource.DeleteTokensDDL",
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
			got := deleteTokens{}.Run(context.Background(), &Env{App: a, Exec: exec})
			want := Result{Name: "delete-tokens", Status: tt.wantStatus, Summary: tt.wantSummary, Details: tt.wantDetails}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("Run() mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantCalls, exec.calls); diff != "" {
				t.Errorf("commands mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestDeleteTokensMeaning(t *testing.T) {
	t.Parallel()

	meaning := Meaning(deleteTokensName)
	for _, want := range []string{"`DeleteTokens`", "resource.DeleteTokensDDL(resource.SpannerDBType)", "a token the delete brings back"} {
		if !strings.Contains(meaning, want) {
			t.Errorf("Meaning() lacks %q", want)
		}
	}
}
