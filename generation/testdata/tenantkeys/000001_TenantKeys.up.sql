-- Foreign keys between tables that each keep their tenant in RealmId, every one holding
-- the tenant and written as the generator prints such a key: the columns of the other
-- table's primary key first, in that key's order, then the rest. A bin names a depot,
-- which is keyed by its realm and its id; a shelf names a vault, which is keyed by its id
-- alone and declares a unique index on its realm and its id.
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
