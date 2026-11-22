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
  Sequence INT64 NOT NULL,
  EventTime TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp=true),
  EventSource STRING(MAX) NOT NULL,
  ChangeSet JSON,
  TenantId STRING(MAX) NOT NULL,
) PRIMARY KEY (TableName, RowId, Sequence, EventTime)`
		createIndex := "CREATE INDEX DataChangeEventsByTenant ON DataChangeEvents(TenantId, EventTime DESC, TableName, RowId, Sequence DESC)"
		switch {
		case period.forever:
			return []string{createTable, createIndex}
		case period.days >= 1:
			return []string{
				createTable,
				createIndex,
				"ALTER TABLE DataChangeEvents ADD ROW DELETION POLICY (OLDER_THAN(EventTime, INTERVAL " + strconv.Itoa(period.days) + " DAY))",
			}
		default:
			return nil
		}
	default:
		return nil
	}
}
