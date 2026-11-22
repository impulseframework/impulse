-- The fixture of the tests that hold each change record to the tenant of its row: the
-- change history table as an application's first migration carries it, a tenant's table
-- (Yards), a table whose rows carry their yard (Docks), a table whose rows reach it
-- through a dock they may leave empty (Berths), a table whose rows reach it through a
-- berth and its dock (Moorings), and a table of no tenant (Notices). Every resource of the
-- fixture tracks its changes. Names are the fixture's own.

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

-- The table's three statements are resource.DataChangeEventsDDL's, which a test pins.
ALTER TABLE DataChangeEvents ADD ROW DELETION POLICY (OLDER_THAN(EventTime, INTERVAL 365 DAY));

CREATE TABLE Yards (
  Id STRING(36) NOT NULL,
  Name STRING(MAX) NOT NULL,
) PRIMARY KEY (Id);

CREATE TABLE Docks (
  Id STRING(36) NOT NULL,
  YardId STRING(36) NOT NULL,
  Name STRING(MAX) NOT NULL,
  CONSTRAINT FK_Docks_YardId FOREIGN KEY (YardId) REFERENCES Yards (Id),
) PRIMARY KEY (Id);

CREATE TABLE Berths (
  Id STRING(36) NOT NULL,
  DockId STRING(36),
  Name STRING(MAX) NOT NULL,
  CONSTRAINT FK_Berths_DockId FOREIGN KEY (DockId) REFERENCES Docks (Id),
) PRIMARY KEY (Id);

CREATE TABLE Moorings (
  Id STRING(36) NOT NULL,
  BerthId STRING(36) NOT NULL,
  Name STRING(MAX) NOT NULL,
  CONSTRAINT FK_Moorings_BerthId FOREIGN KEY (BerthId) REFERENCES Berths (Id),
) PRIMARY KEY (Id);

CREATE TABLE Notices (
  Id STRING(36) NOT NULL,
  Body STRING(MAX) NOT NULL,
) PRIMARY KEY (Id);
