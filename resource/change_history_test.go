package resource

import (
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// wantDataChangeEventsTable is the change history table's CREATE TABLE statement as every
// schema in the repository carries it.
const wantDataChangeEventsTable = `CREATE TABLE DataChangeEvents (
  TableName STRING(MAX) NOT NULL,
  RowId STRING(MAX) NOT NULL,
  EventTime TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp=true),
  Sequence INT64 NOT NULL,
  EventSource STRING(MAX) NOT NULL,
  ChangeSet JSON,
  TenantId STRING(MAX) NOT NULL,
) PRIMARY KEY (TableName, RowId, EventTime DESC, Sequence DESC)`

// TestDataChangeEventsDDL pins the change history table's statements for each database
// and period, and pins the test fixture migration to the Spanner statements for 365 days,
// so the fixture the emulator tests run on is what an application copies into its own
// first migration.
func TestDataChangeEventsDDL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		dbType     DBType
		period     ChangeHistoryPeriod
		periodText string
		want       []string
	}{
		{
			name:       "Spanner, 365 days: the table and its row deletion policy",
			dbType:     SpannerDBType,
			period:     KeepChangeHistoryDays(365),
			periodText: "KeepChangeHistoryDays(365)",
			want: []string{
				wantDataChangeEventsTable,
				"ALTER TABLE DataChangeEvents ADD ROW DELETION POLICY (OLDER_THAN(EventTime, INTERVAL 365 DAY))",
			},
		},
		{
			name:       "Spanner, 30 days: the policy carries the period's number",
			dbType:     SpannerDBType,
			period:     KeepChangeHistoryDays(30),
			periodText: "KeepChangeHistoryDays(30)",
			want: []string{
				wantDataChangeEventsTable,
				"ALTER TABLE DataChangeEvents ADD ROW DELETION POLICY (OLDER_THAN(EventTime, INTERVAL 30 DAY))",
			},
		},
		{
			name:       "Spanner, forever: the table alone",
			dbType:     SpannerDBType,
			period:     KeepChangeHistoryForever(),
			periodText: "KeepChangeHistoryForever()",
			want:       []string{wantDataChangeEventsTable},
		},
		{
			name:       "Spanner, a period never declared: no statement",
			dbType:     SpannerDBType,
			period:     ChangeHistoryPeriod{},
			periodText: "ChangeHistoryPeriod{}",
		},
		{
			name:       "Spanner, zero days declares nothing",
			dbType:     SpannerDBType,
			period:     KeepChangeHistoryDays(0),
			periodText: "KeepChangeHistoryDays(0)",
		},
		{
			name:       "Spanner, a negative number of days declares nothing",
			dbType:     SpannerDBType,
			period:     KeepChangeHistoryDays(-1),
			periodText: "KeepChangeHistoryDays(-1)",
		},
		{
			name:       "Postgres, 365 days: no statement",
			dbType:     PostgresDBType,
			period:     KeepChangeHistoryDays(365),
			periodText: "KeepChangeHistoryDays(365)",
		},
		{
			name:       "Postgres, forever: no statement",
			dbType:     PostgresDBType,
			period:     KeepChangeHistoryForever(),
			periodText: "KeepChangeHistoryForever()",
		},
		{
			name:       "an unknown database has no statements",
			dbType:     DBType("other"),
			period:     KeepChangeHistoryDays(365),
			periodText: "KeepChangeHistoryDays(365)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := DataChangeEventsDDL(tt.dbType, tt.period)
			if len(got) != len(tt.want) {
				t.Fatalf("DataChangeEventsDDL(%s, %s) = %d statements, want %d", tt.dbType, tt.periodText, len(got), len(tt.want))
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("DataChangeEventsDDL(%s, %s) mismatch (-want +got):\n%s", tt.dbType, tt.periodText, diff)
			}
		})
	}

	for _, path := range []string{
		"testdata/patchfork/schema/000001_patch_fork_fixture.up.sql",
		"testdata/changetenant/schema/000001_change_tenant_fixture.up.sql",
	} {
		t.Run("the fixture migration is the Spanner statements for 365 days: "+path, func(t *testing.T) {
			t.Parallel()

			fixture, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("os.ReadFile() error = %v", err)
			}
			for _, stmt := range DataChangeEventsDDL(SpannerDBType, KeepChangeHistoryDays(365)) {
				if !strings.Contains(string(fixture), stmt+";") {
					t.Errorf("the fixture migration does not carry the statement:\n%s", stmt)
				}
			}
		})
	}
}

// TestDataChangeEventColumns holds the change history table's statement to the record a
// tracked write commits: the columns the CREATE TABLE names are DataChangeEvent's spanner
// tags, in order, so a change to one that the other does not follow fails here and not at
// an application's first tracked write.
func TestDataChangeEventColumns(t *testing.T) {
	t.Parallel()

	stmts := DataChangeEventsDDL(SpannerDBType, KeepChangeHistoryForever())
	if len(stmts) == 0 {
		t.Fatalf("DataChangeEventsDDL(%s, %s) = 0 statements, want 1", SpannerDBType, "KeepChangeHistoryForever()")
	}
	lines := strings.Split(stmts[0], "\n")
	end := slices.IndexFunc(lines, func(line string) bool {
		return strings.HasPrefix(line, ") PRIMARY KEY")
	})
	if end < 1 {
		t.Fatalf("the statement has no ') PRIMARY KEY' line:\n%s", stmts[0])
	}
	var statementColumns []string
	for _, line := range lines[1:end] {
		statementColumns = append(statementColumns, strings.Fields(line)[0])
	}

	var recordColumns []string
	record := reflect.TypeFor[DataChangeEvent]()
	for i := range record.NumField() {
		recordColumns = append(recordColumns, record.Field(i).Tag.Get("spanner"))
	}

	if !slices.Equal(recordColumns, statementColumns) {
		t.Errorf("DataChangeEvent's columns are %v, and the statement's are %v", recordColumns, statementColumns)
	}
}
