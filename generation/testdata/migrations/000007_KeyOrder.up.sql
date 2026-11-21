-- Foreign keys of two columns, for how each column is paired with the column it
-- references. A berth is keyed by its dock and its slot, in that order, and is also known
-- by its name and its tag. A mooring names its berth in the key's order. A line names a
-- mooring by the mooring's slot, the second column of the mooring's own foreign key. A
-- spring names its berth by a foreign key the schema gives no name, and a painter by one
-- added once the table is there. (The foreign keys that name a berth in another order
-- than its key are in testdata/crossedkeys: the generator refuses them.)
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

CREATE TABLE Springs (
  Id STRING(36) NOT NULL,
  BerthDock STRING(16) NOT NULL,
  BerthSlot INT64 NOT NULL,

  FOREIGN KEY (BerthDock, BerthSlot) REFERENCES Berths (Dock, Slot),
) PRIMARY KEY (Id);

CREATE TABLE Painters (
  Id STRING(36) NOT NULL,
  BerthDock STRING(16) NOT NULL,
  BerthSlot INT64 NOT NULL,
) PRIMARY KEY (Id);

ALTER TABLE Painters ADD CONSTRAINT FK_Painters_Berth FOREIGN KEY (BerthDock, BerthSlot) REFERENCES Berths (Dock, Slot);
