-- The fixture of the tests that hold a group a row names to the row's tenant: an auth's
-- groups table as the permission store keeps one (a group is kept in one tenant, or for
-- the whole application with an empty DomainId), a tenant's table whose rows name a
-- group and carry their tenant (Yard), and a table whose rows name a group and reach
-- their tenant through a parent. Names are the fixture's own.

CREATE TABLE CastGroups (
  Id STRING(36) NOT NULL,
  DomainId STRING(128) NOT NULL,
  Name STRING(128) NOT NULL,
) PRIMARY KEY (Id);

CREATE TABLE GroupShares (
  Id STRING(36) NOT NULL,
  Yard STRING(36) NOT NULL,
  GroupId STRING(36),
  Note STRING(MAX) NOT NULL,
  CONSTRAINT FK_GroupShares_GroupId FOREIGN KEY (GroupId) REFERENCES CastGroups (Id),
) PRIMARY KEY (Id);

CREATE TABLE GroupSeals (
  Id STRING(36) NOT NULL,
  ShareId STRING(36) NOT NULL,
  GroupId STRING(36),
  CONSTRAINT FK_GroupSeals_ShareId FOREIGN KEY (ShareId) REFERENCES GroupShares (Id),
  CONSTRAINT FK_GroupSeals_GroupId FOREIGN KEY (GroupId) REFERENCES CastGroups (Id),
) PRIMARY KEY (Id);
