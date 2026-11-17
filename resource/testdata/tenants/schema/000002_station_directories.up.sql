-- The declaration the tenant record carries about who may enter a station: the
-- directory-only flag on the record and the directory a station admits its people
-- through, as an application's @directoryOnly column and @tenantDirectory table are,
-- the row with the seat a person gets at their first sign-in through it (the
-- @directoryDefaultSeat column), NULL for none. A station has one directory and a
-- directory is one station's: the two unique indexes hold both.

ALTER TABLE Stations ADD COLUMN DirectoryOnly BOOL NOT NULL DEFAULT (FALSE);

CREATE TABLE StationDirectories (
  Id STRING(36) NOT NULL,
  StationId STRING(64) NOT NULL,
  Method STRING(16) NOT NULL,
  Connection STRING(MAX) NOT NULL,
  DefaultSeat STRING(64),

  CONSTRAINT FK_StationDirectories_StationId FOREIGN KEY (StationId) REFERENCES Stations(Id),
) PRIMARY KEY (Id);

CREATE UNIQUE INDEX StationDirectoriesByStationId ON StationDirectories(StationId);

CREATE UNIQUE INDEX StationDirectoriesByMethodConnection ON StationDirectories(Method, Connection);
