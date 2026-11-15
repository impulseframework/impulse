CREATE TABLE StaffRoles (
  Kind STRING(16) NOT NULL,
  Axis STRING(128) NOT NULL,
  DomainId STRING(128) NOT NULL,
  Role STRING(128) NOT NULL,
  Description STRING(MAX) NOT NULL,
  UpdatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = true),
  CONSTRAINT StaffRolesKind CHECK (Kind IN ('global', 'domain', 'every')),
) PRIMARY KEY (Kind, Axis, DomainId, Role);

CREATE TABLE StaffUserRoles (
  Kind STRING(16) NOT NULL,
  Axis STRING(128) NOT NULL,
  DomainId STRING(128) NOT NULL,
  Role STRING(128) NOT NULL,
  UserId STRING(320) NOT NULL,
  CreatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = true),
  CONSTRAINT StaffUserRolesKind CHECK (Kind IN ('global', 'domain', 'every')),
) PRIMARY KEY (Kind, Axis, DomainId, Role, UserId);

CREATE INDEX StaffUserRolesByUserId ON StaffUserRoles (UserId, Kind, Axis, DomainId);

CREATE TABLE StaffRoleGrants (
  Kind STRING(16) NOT NULL,
  Axis STRING(128) NOT NULL,
  DomainId STRING(128) NOT NULL,
  Role STRING(128) NOT NULL,
  Permission STRING(64) NOT NULL,
  Resource STRING(128) NOT NULL,
  Field STRING(128) NOT NULL,
  Condition STRING(MAX) NOT NULL,
  UpdatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = true),
  CONSTRAINT StaffRoleGrantsKind CHECK (Kind IN ('global', 'domain', 'every')),
) PRIMARY KEY (Kind, Axis, DomainId, Role, Permission, Resource, Field, Condition),
  INTERLEAVE IN PARENT StaffRoles ON DELETE CASCADE;
