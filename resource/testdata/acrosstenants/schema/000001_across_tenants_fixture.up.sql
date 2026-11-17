-- The list across tenants' fixture: crates, each in a yard (the tenant, a column of the
-- row); seals, each in the yard of the crate it seals (the tenant through a join path);
-- the crews a person holds in a yard, which a subject set reads; and a crate's tags, a
-- child table the crates' search box also searches. Names are the fixture's own.

CREATE TABLE Crates (
  Id STRING(36) NOT NULL,
  Yard STRING(36) NOT NULL,
  Label STRING(MAX) NOT NULL,
  Owner STRING(MAX) NOT NULL,
  Team STRING(MAX),
  Weight INT64 NOT NULL,
  Value INT64,
  Label_Search TOKENLIST AS (TOKENIZE_SUBSTRING(Label)) HIDDEN,
) PRIMARY KEY (Id);

CREATE SEARCH INDEX CratesSearch ON Crates(Label_Search) STORING (Yard, Owner, Team);

CREATE TABLE Seals (
  Id STRING(36) NOT NULL,
  CrateId STRING(36) NOT NULL,
  Mark STRING(MAX) NOT NULL,
) PRIMARY KEY (Id);

CREATE TABLE YardCrews (
  Id STRING(36) NOT NULL,
  Yard STRING(36) NOT NULL,
  UserId STRING(MAX) NOT NULL,
  Team STRING(MAX) NOT NULL,
) PRIMARY KEY (Id);

CREATE TABLE CrateTags (
  Id STRING(36) NOT NULL,
  CrateId STRING(36) NOT NULL,
  Yard STRING(36) NOT NULL,
  Name STRING(MAX) NOT NULL,
  Team STRING(MAX),
  Name_Search TOKENLIST AS (TOKENIZE_SUBSTRING(Name)) HIDDEN,
) PRIMARY KEY (Id);

CREATE SEARCH INDEX CrateTagsSearch ON CrateTags(Name_Search) STORING (CrateId, Yard, Team);
