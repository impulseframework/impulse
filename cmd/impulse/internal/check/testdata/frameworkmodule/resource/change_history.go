package resource

import "strconv"

// ChangeHistoryPeriod is how long an application keeps its change records.
type ChangeHistoryPeriod struct {
	days    int
	forever bool
}

// DataChangeEventsDDL is the fixture's copy of the library's statements.
func DataChangeEventsDDL(dbType DBType, period ChangeHistoryPeriod) []string {
	switch dbType {
	case SpannerDBType:
		createTable := `CREATE TABLE DataChangeEvents (
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
		switch {
		case period.forever:
			return []string{createTable}
		case period.days >= 1:
			return []string{
				createTable,
				"ALTER TABLE DataChangeEvents ADD ROW DELETION POLICY (OLDER_THAN(EventTime, INTERVAL " + strconv.Itoa(period.days) + " DAY))",
			}
		default:
			return nil
		}
	default:
		return nil
	}
}
