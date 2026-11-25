-- The fixture of the tests of the names a change record keeps: the change history table as
-- an application's first migration carries it; ships, which belong to no tenant and carry a
-- code two ships may share (Ships); pilots, each of a harbor (the tenant, a column of the
-- row: Pilots); ranks, an enumeration table (Ranks); and voyages, each of a harbor, which
-- point at a ship by its key and by its code, at a pilot and at a rank (Voyages). No table
-- has a foreign key, so a ship a voyage points at can be deleted. Names are the fixture's
-- own.

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

-- The table's two statements are resource.DataChangeEventsDDL's, which a test pins.
ALTER TABLE DataChangeEvents ADD ROW DELETION POLICY (OLDER_THAN(EventTime, INTERVAL 365 DAY));

CREATE TABLE Ships (
  Id STRING(36) NOT NULL,
  Code STRING(36),
  Name STRING(MAX),
) PRIMARY KEY (Id);

CREATE TABLE Pilots (
  Id STRING(36) NOT NULL,
  HarborId STRING(36) NOT NULL,
  Name STRING(MAX) NOT NULL,
) PRIMARY KEY (Id);

CREATE TABLE Ranks (
  Id STRING(36) NOT NULL,
  Description STRING(MAX) NOT NULL,
) PRIMARY KEY (Id);

CREATE TABLE Voyages (
  Id STRING(36) NOT NULL,
  HarborId STRING(36) NOT NULL,
  ShipId STRING(36),
  ShipCode STRING(36),
  PilotId STRING(36),
  RankId STRING(36),
  Note STRING(MAX),
) PRIMARY KEY (Id);
