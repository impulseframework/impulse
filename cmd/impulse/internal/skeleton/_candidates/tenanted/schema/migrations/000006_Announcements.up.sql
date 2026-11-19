-- The rows of a tenant-scoped table are the tenant's own, so they go with it: the delete
-- rule on the foreign key to the tenant table (ON DELETE CASCADE) removes them in the
-- transaction that deletes the tenant, and a tenant is deleted in one act. A table
-- copied from this one keeps the rule; a tenant-scoped table without it refuses the
-- delete of a tenant that still holds one of its rows.
CREATE TABLE Announcements (
  Id STRING(36) NOT NULL,
  TenantId STRING(36) NOT NULL,
  Title STRING(MAX) NOT NULL,
  Body STRING(MAX) NOT NULL,

  CONSTRAINT CK_Announcements_Id CHECK (REGEXP_CONTAINS(Id, r'^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$')),
  CONSTRAINT FK_Announcements_TenantId FOREIGN KEY (TenantId) REFERENCES Tenants(Id) ON DELETE CASCADE,
) PRIMARY KEY (Id);

CREATE INDEX AnnouncementsByTenantIdTitle ON Announcements(TenantId, Title);
