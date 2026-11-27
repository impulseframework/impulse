package resource

// DeleteTokensDDL is the fixture's copy of the library's statements.
func DeleteTokensDDL(dbType DBType) []string {
	switch dbType {
	case SpannerDBType:
		return []string{
			`CREATE TABLE DeleteTokens (
  Id STRING(MAX) NOT NULL,
  TableName STRING(MAX) NOT NULL,
  RowKey STRING(MAX) NOT NULL,
  SessionId STRING(MAX) NOT NULL,
  PrincipalUser STRING(MAX),
  PrincipalRole STRING(MAX),
  CreatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp=true),
  ExpiresAt TIMESTAMP NOT NULL,
) PRIMARY KEY (Id)`,
			"ALTER TABLE DeleteTokens ADD ROW DELETION POLICY (OLDER_THAN(ExpiresAt, INTERVAL 1 DAY))",
		}
	default:
		return nil
	}
}
