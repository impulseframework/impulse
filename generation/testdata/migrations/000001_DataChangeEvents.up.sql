CREATE TABLE DataChangeEvents (
  TableName STRING(MAX) NOT NULL,
  RowId STRING(MAX) NOT NULL,
  Sequence INT64 NOT NULL,
  EventTime TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp=true),
  EventSource STRING(MAX) NOT NULL,
  ChangeSet JSON,
  TenantId STRING(MAX) NOT NULL,
) PRIMARY KEY (TableName, RowId, Sequence, EventTime);

CREATE INDEX DataChangeEventsByTenant ON DataChangeEvents(TenantId, EventTime DESC, TableName, RowId, Sequence DESC);
