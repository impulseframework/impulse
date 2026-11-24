CREATE TABLE DataChangeEvents (
  TableName STRING(MAX) NOT NULL,
  RowId STRING(MAX) NOT NULL,
  Sequence INT64 NOT NULL,
  EventTime TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp=true),
  EventSource STRING(MAX) NOT NULL,
  ChangeSet JSON,
  TenantId STRING(MAX) NOT NULL,
) PRIMARY KEY (TableName, RowId, Sequence, EventTime);

-- One tenant's change records, newest first. A table of no tenant leaves TenantId empty.
CREATE INDEX DataChangeEventsByTenant ON DataChangeEvents(TenantId, EventTime DESC, TableName, RowId, Sequence DESC);

-- Nothing removes a change record: this application keeps its change history forever
-- until its author declares a period. An application under no record-keeping law may
-- declare one (resource.KeepChangeHistoryDays), which adds a row deletion policy to this
-- table. Section 24 of the resource package's reference says what such a policy does:
-- read it before adding one. The table's statements are the resource package's
-- (resource.DataChangeEventsDDL).

-- The pipeline's migration guard reads this database's migration version through the
-- migrations_reader database role, which holds select on the migrations table alone,
-- and the environment layer grants the test environment's deploy identity the role.
-- Created once, by this migration, after the runner has made the table. The emulator
-- accepts the statements and keeps no role.
CREATE ROLE migrations_reader;
GRANT SELECT ON TABLE SchemaMigrations TO ROLE migrations_reader;
