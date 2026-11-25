package auditlog

import (
	"strconv"
	"strings"
)

// Period is how long an auth keeps its audit records, as its schema or its options declare
// it. KeepDays and KeepForever make one. The zero value declares nothing, and SpannerDDL
// renders no statement for it, so a period is always written down.
type Period struct {
	days    int
	forever bool
}

// KeepDays declares that an audit record is removed once it is older than days. A number
// below one declares nothing.
func KeepDays(days int) Period {
	if days < 1 {
		return Period{days: 0, forever: false}
	}

	return Period{days: days, forever: false}
}

// KeepForever declares that no audit record is ever removed.
func KeepForever() Period {
	return Period{days: 0, forever: true}
}

// idCheck is the check the table's Id carries on Spanner: a UUID in its canonical form.
const idCheck = `r'^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'`

// column is one column of the audit records table, with its type on each database.
type column struct {
	name     string
	spanner  string
	postgres string
}

// columns is the audit records table's columns, in the order its statements declare them:
// when and what, who acted (the nine columns every table that keeps who acted gives them),
// whom or what it was done to and where, and the act's other parts.
func columns() []column {
	return []column{
		{name: "Id", spanner: "STRING(36) NOT NULL", postgres: "character varying NOT NULL"},
		{name: "At", spanner: "TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = true)", postgres: "timestamp with time zone NOT NULL"},
		requiredText("Act"),
		text("Form"),
		text("Auth"),
		text("ActorUserId"),
		text("ActorUsername"),
		text("PrincipalUserId"),
		text("PrincipalUsername"),
		text("PrincipalRole"),
		text("SessionId"),
		text("Process"),
		text("Rpc"),
		text("Directory"),
		requiredText("SubjectKind"),
		requiredText("SubjectId"),
		text("SubjectName"),
		text("TenantId"),
		text("TenantName"),
		text("RoleName"),
		{name: "OtherRoleNames", spanner: "ARRAY<STRING(MAX)>", postgres: "character varying[]"},
		text("RoleScope"),
		text("GroupId"),
		text("GroupName"),
		text("OldValue"),
		text("NewValue"),
		{name: "PeriodStart", spanner: "TIMESTAMP", postgres: "timestamp with time zone"},
		{name: "PeriodEnd", spanner: "TIMESTAMP", postgres: "timestamp with time zone"},
		text("SeatingTenantId"),
		text("SeatingTenantName"),
		text("Reason"),
	}
}

// text is a column of text a row may leave NULL.
func text(name string) column {
	return column{name: name, spanner: "STRING(MAX)", postgres: "character varying"}
}

// requiredText is a column of text every row sets.
func requiredText(name string) column {
	return column{name: name, spanner: "STRING(MAX) NOT NULL", postgres: "character varying NOT NULL"}
}

// Columns lists the audit records table's columns, in the order its statements declare
// them.
func Columns() []string {
	all := columns()
	names := make([]string, 0, len(all))
	for _, c := range all {
		names = append(names, c.name)
	}

	return names
}

// SpannerDDL is the statements of an auth's audit records table on Spanner, under the
// table's name with the auth's prefix (CrewSessionAuditRecords). The first creates the
// table, the second the index {table}ByAt, by which the whole log is read newest first,
// and the third the null-filtered index {table}ByTenant, by which one tenant's rows are
// read newest first. For a period of days a fourth gives the table a row deletion policy
// on At, by which Spanner removes each row older than the period in the background; for a
// period of forever there is no fourth. A period that declares nothing has none.
func SpannerDDL(table string, period Period) []string {
	if !period.forever && period.days < 1 {
		return nil
	}

	var create strings.Builder
	create.WriteString("CREATE TABLE " + table + " (\n")
	for _, c := range columns() {
		create.WriteString("  " + c.name + " " + c.spanner + ",\n")
	}
	create.WriteString("  CONSTRAINT CK_" + table + "Id CHECK (REGEXP_CONTAINS(Id, " + idCheck + ")),\n")
	create.WriteString(") PRIMARY KEY (Id)")

	statements := []string{
		create.String(),
		"CREATE INDEX " + table + "ByAt ON " + table + "(At DESC)",
		"CREATE NULL_FILTERED INDEX " + table + "ByTenant ON " + table + "(TenantId, At DESC)",
	}
	if !period.forever {
		statements = append(statements, "ALTER TABLE "+table+" ADD ROW DELETION POLICY (OLDER_THAN(At, INTERVAL "+strconv.Itoa(period.days)+" DAY))")
	}

	return statements
}

// PostgresDDL is the statements of an auth's audit records table on PostgreSQL, under the
// table's name with the auth's prefix: the table with its primary key, and its two
// indexes, by time and by tenant and time. PostgreSQL has no row deletion policy: the
// session library's store removes the rows older than the Auth's period as it writes one.
func PostgresDDL(table string) []string {
	var create strings.Builder
	create.WriteString(`CREATE TABLE "` + table + `"` + "\n(\n")
	for _, c := range columns() {
		create.WriteString(`    "` + c.name + `" ` + c.postgres + ",\n")
	}
	create.WriteString(`    CONSTRAINT "` + table + `_pkey" PRIMARY KEY ("Id")` + "\n)")

	return []string{
		create.String(),
		`CREATE INDEX "` + table + `_At_idx" ON "` + table + `" USING btree ("At")`,
		`CREATE INDEX "` + table + `_TenantId_At_idx" ON "` + table + `" USING btree ("TenantId", "At")`,
	}
}
