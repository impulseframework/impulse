-- Foreign keys of two columns, for how each column is paired with the column it
-- references. A berth is keyed by its dock and its slot, in that order, and is also known
-- by its name and its tag. A mooring names its berth in the key's order. A line names a
-- mooring by the mooring's slot, the second column of the mooring's own foreign key. A
-- hawser names its berth with the two columns in the other order than the berth's key,
-- and a fender names its berth by the name and the tag, in the other order than the
-- berth's columns.
CREATE TABLE Berths (
  Dock STRING(16) NOT NULL,
  Slot INT64 NOT NULL,
  Name STRING(16) NOT NULL,
  Tag INT64 NOT NULL,
) PRIMARY KEY (Dock, Slot);

CREATE TABLE Moorings (
  Id STRING(36) NOT NULL,
  BerthDock STRING(16) NOT NULL,
  BerthSlot INT64 NOT NULL,

  CONSTRAINT FK_Moorings_Berth FOREIGN KEY (BerthDock, BerthSlot) REFERENCES Berths (Dock, Slot),
) PRIMARY KEY (Id);

CREATE TABLE Lines (
  Id STRING(36) NOT NULL,
  MooringSlot INT64 NOT NULL,

  CONSTRAINT FK_Lines_MooringSlot FOREIGN KEY (MooringSlot) REFERENCES Moorings (BerthSlot),
) PRIMARY KEY (Id);

CREATE TABLE Hawsers (
  Id STRING(36) NOT NULL,
  BerthSlot INT64 NOT NULL,
  BerthDock STRING(16) NOT NULL,

  CONSTRAINT FK_Hawsers_Berth FOREIGN KEY (BerthSlot, BerthDock) REFERENCES Berths (Slot, Dock),
) PRIMARY KEY (Id);

CREATE TABLE Fenders (
  Id STRING(36) NOT NULL,
  BerthTag INT64 NOT NULL,
  BerthName STRING(16) NOT NULL,

  CONSTRAINT FK_Fenders_Berth FOREIGN KEY (BerthTag, BerthName) REFERENCES Berths (Tag, Name),
) PRIMARY KEY (Id);
