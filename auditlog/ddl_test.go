package auditlog

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

// wantColumns is the audit records table's thirty-five columns, in the order the table
// declares them.
func wantColumns() []string {
	return []string{
		"Id", "CreatedAt", "Act", "Form",
		"Auth", "ActorUserId", "ActorUsername", "PrincipalUserId", "PrincipalUsername", "PrincipalRole", "SessionId", "Process", "Rpc",
		"Directory", "SubjectKind", "SubjectId", "SubjectName", "TenantId", "TenantName",
		"RoleName", "OtherRoleNames", "RoleScope", "RecordKind", "RecordId", "RecordWord", "RecordName",
		"GroupId", "GroupName", "OldValue", "NewValue",
		"PeriodStart", "PeriodEnd", "SeatingTenantId", "SeatingTenantName", "Reason",
	}
}

// TestColumns pins the table's columns and their order.
func TestColumns(t *testing.T) {
	t.Parallel()

	if got, want := Columns(), wantColumns(); !slices.Equal(got, want) {
		t.Errorf("Columns() = %q, want %q", got, want)
	}
}

// TestPeriodDays pins the days a period answers: its number for a period of days, and
// none for forever or for a period that declares nothing.
func TestPeriodDays(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		period   Period
		wantDays int
		wantOK   bool
	}{
		{name: "90 days", period: KeepDays(90), wantDays: 90, wantOK: true},
		{name: "one day", period: KeepDays(1), wantDays: 1, wantOK: true},
		{name: "forever", period: KeepForever(), wantDays: 0, wantOK: false},
		{name: "the zero period", period: Period{}, wantDays: 0, wantOK: false},
		{name: "no days", period: KeepDays(0), wantDays: 0, wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if days, ok := tt.period.Days(); days != tt.wantDays || ok != tt.wantOK {
				t.Errorf("Days() = (%d, %v), want (%d, %v)", days, ok, tt.wantDays, tt.wantOK)
			}
		})
	}
}

// TestSpannerDDL pins the Spanner statements for each kind of period: the table and its two
// indexes for forever, the row deletion policy after them for a period of days, and none
// for a period that declares nothing.
func TestSpannerDDL(t *testing.T) {
	t.Parallel()

	createTable := `CREATE TABLE SessionAuditRecords (
  Id STRING(36) NOT NULL,
  CreatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = true),
  Act STRING(MAX) NOT NULL,
  Form STRING(MAX),
  Auth STRING(MAX),
  ActorUserId STRING(MAX),
  ActorUsername STRING(MAX),
  PrincipalUserId STRING(MAX),
  PrincipalUsername STRING(MAX),
  PrincipalRole STRING(MAX),
  SessionId STRING(MAX),
  Process STRING(MAX),
  Rpc STRING(MAX),
  Directory STRING(MAX),
  SubjectKind STRING(MAX) NOT NULL,
  SubjectId STRING(MAX) NOT NULL,
  SubjectName STRING(MAX),
  TenantId STRING(MAX),
  TenantName STRING(MAX),
  RoleName STRING(MAX),
  OtherRoleNames ARRAY<STRING(MAX)>,
  RoleScope STRING(MAX),
  RecordKind STRING(MAX),
  RecordId STRING(MAX),
  RecordWord STRING(MAX),
  RecordName STRING(MAX),
  GroupId STRING(MAX),
  GroupName STRING(MAX),
  OldValue STRING(MAX),
  NewValue STRING(MAX),
  PeriodStart TIMESTAMP,
  PeriodEnd TIMESTAMP,
  SeatingTenantId STRING(MAX),
  SeatingTenantName STRING(MAX),
  Reason STRING(MAX),
  CONSTRAINT CK_SessionAuditRecordsId CHECK (REGEXP_CONTAINS(Id, r'^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$')),
) PRIMARY KEY (Id)`
	byCreatedAt := "CREATE INDEX SessionAuditRecordsByCreatedAt ON SessionAuditRecords(CreatedAt DESC)"
	byTenant := "CREATE NULL_FILTERED INDEX SessionAuditRecordsByTenant ON SessionAuditRecords(TenantId, CreatedAt DESC)"

	tests := []struct {
		name   string
		period Period
		want   []string
	}{
		{name: "forever", period: KeepForever(), want: []string{createTable, byCreatedAt, byTenant}},
		{
			name:   "90 days",
			period: KeepDays(90),
			want: []string{
				createTable, byCreatedAt, byTenant,
				"ALTER TABLE SessionAuditRecords ADD ROW DELETION POLICY (OLDER_THAN(CreatedAt, INTERVAL 90 DAY))",
			},
		},
		{name: "the zero period", period: Period{}, want: nil},
		{name: "no days", period: KeepDays(0), want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := SpannerDDL("SessionAuditRecords", tt.period)
			if len(got) != len(tt.want) {
				t.Fatalf("SpannerDDL() has %d statements, want %d: %q", len(got), len(tt.want), got)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("SpannerDDL()[%d] =\n%s\nwant\n%s", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestPostgresDDL pins the PostgreSQL statements: the table with its primary key, and its
// two indexes.
func TestPostgresDDL(t *testing.T) {
	t.Parallel()

	want := []string{
		`CREATE TABLE "SessionAuditRecords"
(
    "Id" character varying NOT NULL,
    "CreatedAt" timestamp with time zone NOT NULL,
    "Act" character varying NOT NULL,
    "Form" character varying,
    "Auth" character varying,
    "ActorUserId" character varying,
    "ActorUsername" character varying,
    "PrincipalUserId" character varying,
    "PrincipalUsername" character varying,
    "PrincipalRole" character varying,
    "SessionId" character varying,
    "Process" character varying,
    "Rpc" character varying,
    "Directory" character varying,
    "SubjectKind" character varying NOT NULL,
    "SubjectId" character varying NOT NULL,
    "SubjectName" character varying,
    "TenantId" character varying,
    "TenantName" character varying,
    "RoleName" character varying,
    "OtherRoleNames" character varying[],
    "RoleScope" character varying,
    "RecordKind" character varying,
    "RecordId" character varying,
    "RecordWord" character varying,
    "RecordName" character varying,
    "GroupId" character varying,
    "GroupName" character varying,
    "OldValue" character varying,
    "NewValue" character varying,
    "PeriodStart" timestamp with time zone,
    "PeriodEnd" timestamp with time zone,
    "SeatingTenantId" character varying,
    "SeatingTenantName" character varying,
    "Reason" character varying,
    CONSTRAINT "SessionAuditRecords_pkey" PRIMARY KEY ("Id")
)`,
		`CREATE INDEX "SessionAuditRecords_CreatedAt_idx" ON "SessionAuditRecords" USING btree ("CreatedAt")`,
		`CREATE INDEX "SessionAuditRecords_TenantId_CreatedAt_idx" ON "SessionAuditRecords" USING btree ("TenantId", "CreatedAt")`,
	}

	got := PostgresDDL("SessionAuditRecords")
	if len(got) != len(want) {
		t.Fatalf("PostgresDDL() has %d statements, want %d: %q", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("PostgresDDL()[%d] =\n%s\nwant\n%s", i, got[i], want[i])
		}
	}
}

// TestDDLNamesEveryColumn holds each database's table to Columns, in its order, and to no
// column named Who or What: a row stores parts and never a sentence.
func TestDDLNamesEveryColumn(t *testing.T) {
	t.Parallel()

	spannerColumn := regexp.MustCompile(`(?m)^ {2}([A-Za-z]+) `)
	postgresColumn := regexp.MustCompile(`(?m)^ {4}"([A-Za-z]+)" `)
	tests := []struct {
		name   string
		create string
		column *regexp.Regexp
	}{
		{name: "Spanner", create: firstStatement(t, SpannerDDL("SessionAuditRecords", KeepForever())), column: spannerColumn},
		{name: "PostgreSQL", create: firstStatement(t, PostgresDDL("SessionAuditRecords")), column: postgresColumn},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got []string
			for _, match := range tt.column.FindAllStringSubmatch(tt.create, -1) {
				if match[1] != "CONSTRAINT" {
					got = append(got, match[1])
				}
			}
			if !slices.Equal(got, Columns()) {
				t.Errorf("the table's columns are %q, want Columns() %q", got, Columns())
			}
			for _, sentence := range []string{"Who", "What"} {
				if slices.Contains(got, sentence) {
					t.Errorf("the table has a column %s; a row stores parts and no sentence", sentence)
				}
			}
		})
	}
}

// TestColumnsNeedNoQuote holds every column to a name that is no reserved word of
// GoogleSQL, so a statement anyone writes against the table names each one unquoted.
func TestColumnsNeedNoQuote(t *testing.T) {
	t.Parallel()

	reserved := strings.Fields(`ALL AND ANY ARRAY AS ASC ASSERT_ROWS_MODIFIED AT BETWEEN BY CASE CAST COLLATE CONTAINS
		CREATE CROSS CUBE CURRENT DEFAULT DEFINE DESC DISTINCT ELSE END ENUM ESCAPE EXCEPT EXCLUDE EXISTS EXTRACT
		FALSE FETCH FOLLOWING FOR FROM FULL GROUP GROUPING GROUPS HASH HAVING IF IGNORE IN INNER INTERSECT INTERVAL
		INTO IS JOIN LATERAL LEFT LIKE LIMIT LOOKUP MERGE NATURAL NEW NO NOT NULL NULLS OF ON OR ORDER OUTER OVER
		PARTITION PRECEDING PROTO RANGE RECURSIVE RESPECT RIGHT ROLLUP ROWS SELECT SET SOME STRUCT TABLESAMPLE THEN
		TO TREAT TRUE UNBOUNDED UNION UNNEST USING WHEN WHERE WINDOW WITH WITHIN`)
	for _, column := range Columns() {
		if slices.Contains(reserved, strings.ToUpper(column)) {
			t.Errorf("the column %s is a reserved word of GoogleSQL, which every Spanner statement would have to quote", column)
		}
	}
}

// TestDDLCarriesThePrefix holds every statement and constraint of both databases to the
// table's name as it is handed, prefix and all.
func TestDDLCarriesThePrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		statements []string
		names      []string
	}{
		{
			name:       "Spanner",
			statements: SpannerDDL("CrewSessionAuditRecords", KeepDays(30)),
			names:      []string{"CK_CrewSessionAuditRecordsId", "CrewSessionAuditRecordsByCreatedAt", "CrewSessionAuditRecordsByTenant"},
		},
		{
			name:       "PostgreSQL",
			statements: PostgresDDL("CrewSessionAuditRecords"),
			names:      []string{`"CrewSessionAuditRecords_pkey"`, `"CrewSessionAuditRecords_CreatedAt_idx"`, `"CrewSessionAuditRecords_TenantId_CreatedAt_idx"`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if len(tt.statements) == 0 {
				t.Fatal("no statements")
			}
			all := strings.Join(tt.statements, "\n")
			for i, statement := range tt.statements {
				if !strings.Contains(statement, "CrewSessionAuditRecords") {
					t.Errorf("statement %d does not name the table: %s", i, statement)
				}
			}
			if bare, prefixed := strings.Count(all, "SessionAuditRecords"), strings.Count(all, "CrewSessionAuditRecords"); bare != prefixed {
				t.Errorf("%d names lack the prefix in:\n%s", bare-prefixed, all)
			}
			for _, name := range tt.names {
				if !strings.Contains(all, name) {
					t.Errorf("no statement names %s", name)
				}
			}
		})
	}
}

// firstStatement answers the first of a list of statements.
func firstStatement(t *testing.T, statements []string) string {
	t.Helper()

	if len(statements) == 0 {
		t.Fatal("no statements")
	}

	return statements[0]
}
