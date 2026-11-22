-- Foreign keys between tables that each keep their tenant in RealmId, every one holding
-- the tenant and written as the generator prints such a key, in the order the database
-- holds the other table's columns in: the columns of that table's primary key that the
-- key references, in the key's order, up to the first one it does not reference; then the
-- rest in the order the table declares its columns.
-- A bin names a depot, which is keyed by its realm and its id.
-- A shelf names a vault, which is keyed by its id alone and declares a unique index on its
-- realm and its id: the index plays no part in the order.
-- A ticket names a gate by its code, which is not the gate's key; the gate declares its
-- realm before its code.
-- A latch names a lock by its code in the same way; the lock declares its code before its
-- realm.
-- A desk names a room by its id, which is not the first column of the room's key.
-- A mooring names a quay by its key; the quay declares its realm before its id.
CREATE TABLE Depots (
  RealmId STRING(36) NOT NULL,
  Id STRING(36) NOT NULL,
) PRIMARY KEY (RealmId, Id);

CREATE TABLE Bins (
  Id STRING(36) NOT NULL,
  RealmId STRING(36) NOT NULL,
  DepotId STRING(36) NOT NULL,

  CONSTRAINT FK_Bins_RealmIdDepotId FOREIGN KEY (RealmId, DepotId) REFERENCES Depots (RealmId, Id),
) PRIMARY KEY (Id);

CREATE TABLE Vaults (
  Id STRING(36) NOT NULL,
  RealmId STRING(36) NOT NULL,
) PRIMARY KEY (Id);

CREATE UNIQUE INDEX VaultsByRealmIdId ON Vaults (RealmId, Id);

CREATE TABLE Shelves (
  Id STRING(36) NOT NULL,
  RealmId STRING(36) NOT NULL,
  VaultId STRING(36) NOT NULL,

  CONSTRAINT FK_Shelves_VaultIdRealmId FOREIGN KEY (VaultId, RealmId) REFERENCES Vaults (Id, RealmId),
) PRIMARY KEY (Id);

CREATE TABLE Gates (
  Id STRING(36) NOT NULL,
  RealmId STRING(36) NOT NULL,
  Code STRING(36) NOT NULL,
) PRIMARY KEY (Id);

CREATE TABLE Tickets (
  Id STRING(36) NOT NULL,
  RealmId STRING(36) NOT NULL,
  GateCode STRING(36) NOT NULL,

  CONSTRAINT FK_Tickets_RealmIdGateCode FOREIGN KEY (RealmId, GateCode) REFERENCES Gates (RealmId, Code),
) PRIMARY KEY (Id);

CREATE TABLE Locks (
  Id STRING(36) NOT NULL,
  Code STRING(36) NOT NULL,
  RealmId STRING(36) NOT NULL,
) PRIMARY KEY (Id);

CREATE TABLE Latches (
  Id STRING(36) NOT NULL,
  RealmId STRING(36) NOT NULL,
  LockCode STRING(36) NOT NULL,

  CONSTRAINT FK_Latches_LockCodeRealmId FOREIGN KEY (LockCode, RealmId) REFERENCES Locks (Code, RealmId),
) PRIMARY KEY (Id);

CREATE TABLE Rooms (
  FloorId STRING(36) NOT NULL,
  RealmId STRING(36) NOT NULL,
  Id STRING(36) NOT NULL,
) PRIMARY KEY (FloorId, Id);

CREATE TABLE Desks (
  Id STRING(36) NOT NULL,
  RealmId STRING(36) NOT NULL,
  RoomId STRING(36) NOT NULL,

  CONSTRAINT FK_Desks_RealmIdRoomId FOREIGN KEY (RealmId, RoomId) REFERENCES Rooms (RealmId, Id),
) PRIMARY KEY (Id);

CREATE TABLE Quays (
  RealmId STRING(36) NOT NULL,
  Id STRING(36) NOT NULL,
) PRIMARY KEY (Id);

CREATE TABLE Moorings (
  Id STRING(36) NOT NULL,
  RealmId STRING(36) NOT NULL,
  QuayId STRING(36) NOT NULL,

  CONSTRAINT FK_Moorings_QuayIdRealmId FOREIGN KEY (QuayId, RealmId) REFERENCES Quays (Id, RealmId),
) PRIMARY KEY (Id);
