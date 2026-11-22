-- Foreign keys of two columns that name their columns in another order than the key they
-- reference, layered over testdata/migrations, whose berth is keyed by its dock and its
-- slot and also known by its name and its tag. A hawser names its berth with the two
-- columns in the other order than the berth's key, and a fender names its berth by the
-- tag and the name, in the other order than the database holds those two columns in.
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
