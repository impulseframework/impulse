-- A tenant's key is an id generated when the tenant is made, never a word a person types: it is
-- written into every row the tenant owns and is the domain of every role held in it,
-- so it never changes and is never used twice. The name is what people call the
-- tenant, changed like any field; the unique index below keeps two tenants from
-- carrying one name, and an application that allows that drops it.
CREATE TABLE Tenants (
  Id STRING(36) NOT NULL,
  Name STRING(MAX) NOT NULL,
  -- When the tenant was made: the time of the commit that made the row, written by the
  -- record's create and never changed. The list of tenants says it as Since.
  CreatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = true),
  -- The tenant's people enter through its directory only: no other sign-in reaches a
  -- tenant whose flag is true, a password included. Half of what the tenant declares
  -- about who may enter it; the other half is its row of TenantDirectories.
  DirectoryOnly BOOL NOT NULL DEFAULT (FALSE),

  CONSTRAINT CK_Tenants_Id CHECK (REGEXP_CONTAINS(Id, r'^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$')),
) PRIMARY KEY (Id);

CREATE UNIQUE INDEX TenantsByName ON Tenants(Name);

-- A tenant's directory, the organization its people sign in through: the sign-in method
-- (google, azure, workos, oidc) and the connection the method's identities carry (a
-- Google Workspace by its domain, a Microsoft Entra tenant by its tenant id, an
-- organization's own OpenID Connect provider by its registration's). Nobody types a
-- row in: it is written from a sign-in through the directory, when the tenant is made
-- by a person who came through it or when its administrator, signed in through it,
-- makes it the tenant's.
-- A tenant has one directory and a directory belongs to one tenant, which the two
-- unique indexes below hold.
-- DefaultSeat names the role a person is given in the tenant at their first sign-in
-- through the directory; NULL says the tenant admits them by invitation only.
-- AsksSecondFactor says the tenant's administrator has said the directory asks its
-- people for a second factor of its own, so a sign-in through it is not asked for the
-- mailed code.
CREATE TABLE TenantDirectories (
  Id STRING(36) NOT NULL,
  TenantId STRING(36) NOT NULL,
  Method STRING(16) NOT NULL,
  Connection STRING(MAX) NOT NULL,
  DefaultSeat STRING(64),
  AsksSecondFactor BOOL NOT NULL DEFAULT (FALSE),

  CONSTRAINT CK_TenantDirectories_Id CHECK (REGEXP_CONTAINS(Id, r'^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$')),
  CONSTRAINT CK_TenantDirectories_Method CHECK (Method IN ('google', 'azure', 'workos', 'oidc')),
  CONSTRAINT FK_TenantDirectories_TenantId FOREIGN KEY (TenantId) REFERENCES Tenants(Id),
) PRIMARY KEY (Id);

CREATE UNIQUE INDEX TenantDirectoriesByTenantId ON TenantDirectories(TenantId);
CREATE UNIQUE INDEX TenantDirectoriesByMethodConnection ON TenantDirectories(Method, Connection);
