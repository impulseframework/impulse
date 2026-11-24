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
  EventTime TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp=true),
  Sequence INT64 NOT NULL,
  EventSource STRING(MAX) NOT NULL,
  ChangeSet JSON,
  TenantId STRING(MAX) NOT NULL,
) PRIMARY KEY (TableName, RowId, EventTime DESC, Sequence DESC);

-- The change history's row deletion policy, as an application's first migration carries
-- it. The table's two statements are resource.DataChangeEventsDDL's, which a test pins.
ALTER TABLE DataChangeEvents ADD ROW DELETION POLICY (OLDER_THAN(EventTime, INTERVAL 365 DAY));

-- The secret-field tests' fixture: a table that keeps change records and has a column
-- its resource declares a secret, which holds the value sealed.
CREATE TABLE SealedRows (
  Id STRING(36) NOT NULL,
  Title STRING(MAX) NOT NULL,
  AccessCode BYTES(MAX),
) PRIMARY KEY (Id);

-- The secret-field tests' second fixture: a table with a key of two columns, which a
-- retirement of a version of the application secret reads beside SealedRows.
CREATE TABLE SealedPairRows (
  Vault STRING(36) NOT NULL,
  Shelf STRING(36) NOT NULL,
  Number BYTES(MAX),
) PRIMARY KEY (Vault, Shelf);
