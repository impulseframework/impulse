-- A tenant column in several foreign keys: a harbor is the tenant, and every other table
-- keeps its harbor in HarborId. A wharf names its harbor alone; a yard names its harbor
-- and a wharf of that harbor; a zone names its harbor, a yard of that harbor and another
-- zone of that harbor; a pier names a wharf and a yard of its harbor and has no foreign
-- key of its own to the harbor. A shed names a yard of its harbor, and a dock a shed of
-- its harbor, neither with a foreign key of its own to the harbor: the harbor is carried
-- into them by keys of two columns alone, through one table and through two. Every
-- HarborId reads the harbor's Id. A foreign key of two columns names them in the order the
-- emulator holds the referenced columns in, the referenced table's Id first, as the order
-- of a foreign key's columns is checked (refuseCrossedForeignKeys).
CREATE TABLE Harbors (
  Id STRING(36) NOT NULL,
) PRIMARY KEY (Id);

CREATE TABLE Wharves (
  Id STRING(36) NOT NULL,
  HarborId STRING(36) NOT NULL,

  CONSTRAINT FK_Wharves_HarborId FOREIGN KEY (HarborId) REFERENCES Harbors (Id),
) PRIMARY KEY (Id);

CREATE TABLE Yards (
  Id STRING(36) NOT NULL,
  HarborId STRING(36) NOT NULL,
  WharfId STRING(36) NOT NULL,

  CONSTRAINT FK_Yards_HarborId FOREIGN KEY (HarborId) REFERENCES Harbors (Id),
  CONSTRAINT FK_Yards_HarborIdWharfId FOREIGN KEY (WharfId, HarborId) REFERENCES Wharves (Id, HarborId),
) PRIMARY KEY (Id);

CREATE TABLE Zones (
  Id STRING(36) NOT NULL,
  HarborId STRING(36) NOT NULL,
  YardId STRING(36) NOT NULL,
  InsideZoneId STRING(36),

  CONSTRAINT FK_Zones_HarborId FOREIGN KEY (HarborId) REFERENCES Harbors (Id),
  CONSTRAINT FK_Zones_HarborIdYardId FOREIGN KEY (YardId, HarborId) REFERENCES Yards (Id, HarborId),
  CONSTRAINT FK_Zones_HarborIdInsideZoneId FOREIGN KEY (InsideZoneId, HarborId) REFERENCES Zones (Id, HarborId),
) PRIMARY KEY (Id);

CREATE TABLE Piers (
  Id STRING(36) NOT NULL,
  HarborId STRING(36) NOT NULL,
  WharfId STRING(36) NOT NULL,
  YardId STRING(36) NOT NULL,

  CONSTRAINT FK_Piers_HarborIdWharfId FOREIGN KEY (WharfId, HarborId) REFERENCES Wharves (Id, HarborId),
  CONSTRAINT FK_Piers_HarborIdYardId FOREIGN KEY (YardId, HarborId) REFERENCES Yards (Id, HarborId),
) PRIMARY KEY (Id);

CREATE TABLE Sheds (
  Id STRING(36) NOT NULL,
  HarborId STRING(36) NOT NULL,
  YardId STRING(36) NOT NULL,

  CONSTRAINT FK_Sheds_HarborIdYardId FOREIGN KEY (YardId, HarborId) REFERENCES Yards (Id, HarborId),
) PRIMARY KEY (Id);

CREATE TABLE Docks (
  Id STRING(36) NOT NULL,
  HarborId STRING(36) NOT NULL,
  ShedId STRING(36) NOT NULL,

  CONSTRAINT FK_Docks_HarborIdShedId FOREIGN KEY (ShedId, HarborId) REFERENCES Sheds (Id, HarborId),
) PRIMARY KEY (Id);
