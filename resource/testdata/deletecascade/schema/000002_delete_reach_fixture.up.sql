-- The fixture of the tests of a delete whose kinds go on the permission to delete the
-- record, as far as it reaches: an account; the assignments that go with it, each a
-- tenant's by a column of its own, which may also name a task; the notes that go with
-- it, each a tenant's through the board it is on; the filters that go with it, which are
-- no tenant's; and the companies, the tenants, each with its name. There are no foreign
-- keys, as in the first fixture. Names are the fixture's own.

CREATE TABLE CascadeAccounts (
  Id STRING(36) NOT NULL,
) PRIMARY KEY (Id);

CREATE TABLE CascadeAssignments (
  Id STRING(36) NOT NULL,
  AccountId STRING(36),
  TaskId STRING(36),
  TenantId STRING(36),
) PRIMARY KEY (Id);

CREATE TABLE CascadeBoards (
  Id STRING(36) NOT NULL,
  TenantId STRING(36),
) PRIMARY KEY (Id);

CREATE TABLE CascadeNotes (
  Id STRING(36) NOT NULL,
  AccountId STRING(36),
  BoardId STRING(36),
) PRIMARY KEY (Id);

CREATE TABLE CascadeFilters (
  Id STRING(36) NOT NULL,
  AccountId STRING(36),
) PRIMARY KEY (Id);

CREATE TABLE CascadeCompanies (
  Id STRING(36) NOT NULL,
  Name STRING(MAX),
) PRIMARY KEY (Id);
