-- The name lookups' fixture: hangar passes, each in a sector (the tenant, a column of the
-- row), pointing to a squadron of a sector (a tenant's row by its column), to a crew, which
-- reaches its sector through its wing (a tenant's row by a way through another table), and
-- to a pass level (an enumeration table). Names are the fixture's own.

CREATE TABLE Wings (
  Id STRING(36) NOT NULL,
  SectorId STRING(36) NOT NULL,
  Name STRING(MAX) NOT NULL,
) PRIMARY KEY (Id);

CREATE TABLE Crews (
  Id STRING(36) NOT NULL,
  WingId STRING(36) NOT NULL,
  Name STRING(MAX) NOT NULL,
) PRIMARY KEY (Id);

CREATE TABLE Squadrons (
  Id STRING(36) NOT NULL,
  SectorId STRING(36) NOT NULL,
  Name STRING(MAX) NOT NULL,
  Rank INT64 NOT NULL,
  Name_Search TOKENLIST AS (TOKENIZE_SUBSTRING(Name)) HIDDEN,
) PRIMARY KEY (Id);

CREATE SEARCH INDEX SquadronsSearch ON Squadrons(Name_Search) STORING (SectorId, Rank);

CREATE TABLE PassLevels (
  Id STRING(36) NOT NULL,
  Description STRING(MAX) NOT NULL,
) PRIMARY KEY (Id);

CREATE TABLE HangarPasses (
  Id STRING(36) NOT NULL,
  SectorId STRING(36) NOT NULL,
  SquadronId STRING(36),
  LevelId STRING(36) NOT NULL,
  CrewId STRING(36),
) PRIMARY KEY (Id);
