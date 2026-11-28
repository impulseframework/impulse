-- The delete tokens: one row for each delete a person was asked about and may go ahead
-- with, written when the question passes and deleted with the record. The statements are
-- the library's own (resource.DeleteTokensDDL(resource.SpannerDBType)), which a test pins.

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
