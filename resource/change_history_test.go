package resource

import (
	"context"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/eventsource"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// wantDataChangeEventsTable is the change history table's CREATE TABLE statement as every
// schema in the repository carries it.
const wantDataChangeEventsTable = `CREATE TABLE DataChangeEvents (
  TableName STRING(MAX) NOT NULL,
  RowId STRING(MAX) NOT NULL,
  EventTime TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp=true),
  Sequence INT64 NOT NULL,
  Operation STRING(MAX) NOT NULL,
  ChangeSet JSON,
  TenantId STRING(MAX),
  Auth STRING(MAX),
  ActorUserId STRING(MAX),
  ActorUsername STRING(MAX),
  PrincipalUserId STRING(MAX),
  PrincipalUsername STRING(MAX),
  PrincipalRole STRING(MAX),
  SessionId STRING(MAX),
  Process STRING(MAX),
  Rpc STRING(MAX),
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
		"testdata/recordednames/schema/000001_recorded_names_fixture.up.sql",
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

// TestDataChangeEventColumns holds the change history table's statement to the records a
// tracked write commits: every column the CREATE TABLE names is one a record can set, and
// every column a record sets is one the statement names, so a change to one that the
// other does not follow fails here and not at an application's first tracked write. The
// two fullest records together set them all: a session acting as a person and one acting
// as a role, each through a process, in an RPC method, on a tenant's row.
func TestDataChangeEventColumns(t *testing.T) {
	t.Parallel()

	stmts := DataChangeEventsDDL(SpannerDBType, KeepChangeHistoryForever())
	if len(stmts) == 0 {
		t.Fatalf("DataChangeEventsDDL(%s, %s) = 0 statements, want 1", SpannerDBType, "KeepChangeHistoryForever()")
	}
	statementColumns := tableColumns(t, stmts[0])

	asPerson := accountCtx("bob", bobID, &sessioninfo.Impersonation{
		Actor: "alice", ActorID: impulse.NullUUIDFromUUID(aliceID), Principal: accesstypes.UserPrincipal(bobUser),
	})
	asRole := sessionCtx("alice", &sessioninfo.Impersonation{
		Actor: "alice", ActorID: impulse.NullUUIDFromUUID(aliceID), Principal: accesstypes.RolePrincipal("Support"),
	})
	recordColumns := map[string]bool{}
	for _, ctx := range []context.Context{asPerson, asRole} {
		record := &DataChangeEvent{
			TableName: "Orders", RowID: "order-1", EventTime: spanner.CommitTimestamp, Operation: ChangeUpdate,
			ChangeSet: spanner.NullJSON{Valid: true, Value: map[string]any{}}, TenantID: "tenant-1",
			Source: eventsource.UserProcessEvent(WithRPC(WithAuth(ctx, "crew"), "CloseOrder"), "Import"),
		}
		for column := range record.Columns() {
			recordColumns[column] = true
		}
	}

	if diff := cmp.Diff(statementColumns, slices.Sorted(maps.Keys(recordColumns))); diff != "" {
		t.Errorf("the statement's columns (-) and those a record sets (+) differ:\n%s", diff)
	}
}

// tableColumns answers the columns a CREATE TABLE statement names, sorted.
func tableColumns(t *testing.T, statement string) []string {
	t.Helper()

	lines := strings.Split(statement, "\n")
	end := slices.IndexFunc(lines, func(line string) bool {
		return strings.HasPrefix(line, ") PRIMARY KEY")
	})
	if end < 1 {
		t.Fatalf("the statement has no ') PRIMARY KEY' line:\n%s", statement)
	}
	var columns []string
	for _, line := range lines[1:end] {
		columns = append(columns, strings.Fields(line)[0])
	}
	slices.Sort(columns)

	return columns
}
