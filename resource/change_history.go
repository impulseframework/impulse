package resource

import "strconv"

// ChangeHistoryPeriod is how long an application keeps its change records, as its schema
// declares it. KeepChangeHistoryDays and KeepChangeHistoryForever make one. The zero value
// declares nothing, and DataChangeEventsDDL renders no statement for it, so a period is
// always written down.
type ChangeHistoryPeriod struct {
	days    int
	forever bool
}

// KeepChangeHistoryDays declares that a change record is removed once it is older than
// days, by a row deletion policy on the table. It is for an application under no
// record-keeping law: the policy removes the oldest records of rows that are still there,
// waits for nothing, cannot be stopped for one record, and is dropped by Spanner when a
// database is restored from a backup. A number below one declares nothing.
func KeepChangeHistoryDays(days int) ChangeHistoryPeriod {
	if days < 1 {
		return ChangeHistoryPeriod{}
	}

	return ChangeHistoryPeriod{days: days, forever: false}
}

// KeepChangeHistoryForever declares that no change record is ever removed, which is what
// an application made by impulse new starts with.
func KeepChangeHistoryForever() ChangeHistoryPeriod {
	return ChangeHistoryPeriod{days: 0, forever: true}
}

// DataChangeEventsDDL is the statements of the DataChangeEvents table, where a resource
// that tracks its changes writes one record for each create, update and delete, for the
// application database's type. An application copies them into its first migration;
// tests pin Lodestar's and each skeleton's to them. The first statement creates the
// table: the row, the time, what kind of write it was, what changed, the key of the
// tenant the row belongs to (not set for a row of no tenant), and who made the change, in
// nine columns of which a record sets those that hold something. Its key puts a row's
// records together, newest first, so a page of one record's history is one range of the
// table, and the table has no other index. For a period of days a second statement gives
// the table a row deletion policy on EventTime, by which Spanner removes each record older
// than the period in the background; for a period of forever there is no second
// statement. There are statements for Spanner alone: the table gets its PostgreSQL
// statements when the package writes to PostgreSQL. A period that declares nothing has
// none.
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
