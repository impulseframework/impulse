-- Foreign keys to columns that are not the other table's key. A gate is keyed by Id and
-- also known by its Code, and a pass names its gate by the code. A stub names its pass
-- by two columns that are no key, and by the pass's key as well. A seal names its pass
-- by the same two columns as a stub does: two foreign keys to one set of columns.
CREATE TABLE Gates (
  Id STRING(36) NOT NULL,
  Code STRING(16) NOT NULL,
) PRIMARY KEY (Id);

CREATE TABLE Passes (
  Id STRING(36) NOT NULL,
  GateCode STRING(16) NOT NULL,
  Zone STRING(16) NOT NULL,
  Number INT64 NOT NULL,

  CONSTRAINT FK_Passes_GateCode FOREIGN KEY (GateCode) REFERENCES Gates(Code),
) PRIMARY KEY (Id);

CREATE TABLE Stubs (
  Id STRING(36) NOT NULL,
  PassZone STRING(16) NOT NULL,
  PassNumber INT64 NOT NULL,
  PassId STRING(36) NOT NULL,

  CONSTRAINT FK_Stubs_Pass FOREIGN KEY (PassZone, PassNumber) REFERENCES Passes(Zone, Number),
  CONSTRAINT FK_Stubs_PassId FOREIGN KEY (PassId) REFERENCES Passes(Id),
) PRIMARY KEY (Id);

CREATE TABLE Seals (
  Id STRING(36) NOT NULL,
  PassZone STRING(16) NOT NULL,
  PassNumber INT64 NOT NULL,

  CONSTRAINT FK_Seals_Pass FOREIGN KEY (PassZone, PassNumber) REFERENCES Passes(Zone, Number),
) PRIMARY KEY (Id);
