-- The update-or-create and create-unless-there tests' fixture: a table with a column a
-- create fills and a column an update stamps, and a table that keeps change records,
-- with the table the records go to. Names are the fixture's own.

CREATE TABLE ForkRows (
  Id STRING(36) NOT NULL,
  Title STRING(MAX) NOT NULL,
  CreatedBy STRING(36),
  UpdatedBy STRING(36),
) PRIMARY KEY (Id);

CREATE TABLE TrackedForkRows (
  Id STRING(36) NOT NULL,
  Title STRING(MAX) NOT NULL,
) PRIMARY KEY (Id);

CREATE TABLE DataChangeEvents (
  TableName STRING(MAX) NOT NULL,
  RowId STRING(MAX) NOT NULL,
  Sequence INT64 NOT NULL,
  EventTime TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp=true),
  EventSource STRING(MAX) NOT NULL,
  ChangeSet JSON,
) PRIMARY KEY (TableName, RowId, Sequence, EventTime);

-- The secret-field tests' fixture: a table that keeps change records and has a column
-- its resource declares a secret.
CREATE TABLE SealedRows (
  Id STRING(36) NOT NULL,
  Title STRING(MAX) NOT NULL,
  AccessCode STRING(MAX),
) PRIMARY KEY (Id);
