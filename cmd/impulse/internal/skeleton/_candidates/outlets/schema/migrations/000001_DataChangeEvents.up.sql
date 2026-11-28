-- The change history: one record for every create, update and delete of a tracked row.
-- The key puts a row's records together, newest first, so reading a page of one record's
-- history reads that page and nothing else. The statement is the resource package's
-- (resource.DataChangeEventsDDL); impulse check holds the two together.
CREATE TABLE DataChangeEvents (
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
) PRIMARY KEY (TableName, RowId, EventTime DESC, Sequence DESC);

-- Nothing removes a change record: this application keeps its change history forever
-- until its author declares a period. An application under no record-keeping law may
-- declare one (resource.KeepChangeHistoryDays), which adds a row deletion policy to this
-- table. Section 24 of the resource package's reference says what such a policy does:
-- read it before adding one.

-- The pipeline's migration guard reads this database's migration version through the
-- migrations_reader database role, which holds select on the migrations table alone,
-- and the environment layer grants the test environment's deploy identity the role.
-- Created once, by this migration, after the runner has made the table. The emulator
-- accepts the statements and keeps no role.
CREATE ROLE migrations_reader;
GRANT SELECT ON TABLE SchemaMigrations TO ROLE migrations_reader;
