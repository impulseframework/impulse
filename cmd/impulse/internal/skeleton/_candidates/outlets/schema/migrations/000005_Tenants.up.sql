-- A tenant's key is an id the create path generates, never a word a person types: it is
-- written into every row the tenant owns and is the domain of every role held in it,
-- so it never changes and is never used twice. The name is what people call the
-- tenant, changed like any field; the unique index below keeps two tenants from
-- carrying one name, and an application that allows that drops it.
CREATE TABLE Tenants (
  Id STRING(36) NOT NULL,
  Name STRING(MAX) NOT NULL,
  -- The tenant's people enter through its directories only: a password does not
  -- reach a tenant whose flag is true. Half of what the tenant declares about who may
  -- enter it; the other half is TenantDirectories.
  DirectoryOnly BOOL NOT NULL DEFAULT (FALSE),

  CONSTRAINT CK_Tenants_Id CHECK (REGEXP_CONTAINS(Id, r'^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$')),
) PRIMARY KEY (Id);

CREATE UNIQUE INDEX TenantsByName ON Tenants(Name);

-- The directories a tenant admits its people through, one row per declaration: the
-- sign-in method (google, azure, workos) and the connection the method's identities
-- carry (a Google Workspace by its domain, a Microsoft Entra tenant by its tenant id).
-- One domain, one directory by default; a second directory is a second row.
-- DefaultSeat names the role a stranger the directory vouches for is seated with in the
-- tenant at their first sign-in; NULL says the tenant admits them by invitation only.
CREATE TABLE TenantDirectories (
  Id STRING(36) NOT NULL,
  TenantId STRING(36) NOT NULL,
  Method STRING(16) NOT NULL,
  Connection STRING(MAX) NOT NULL,
  DefaultSeat STRING(64),

  CONSTRAINT CK_TenantDirectories_Id CHECK (REGEXP_CONTAINS(Id, r'^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$')),
  CONSTRAINT CK_TenantDirectories_Method CHECK (Method IN ('google', 'azure', 'workos')),
  CONSTRAINT FK_TenantDirectories_TenantId FOREIGN KEY (TenantId) REFERENCES Tenants(Id),
) PRIMARY KEY (Id);

CREATE UNIQUE INDEX TenantDirectoriesByTenantIdMethodConnection ON TenantDirectories(TenantId, Method, Connection);
