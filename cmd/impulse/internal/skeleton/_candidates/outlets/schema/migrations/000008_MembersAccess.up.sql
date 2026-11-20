CREATE TABLE MembersRoles (
  Kind STRING(16) NOT NULL,
  Axis STRING(128) NOT NULL,
  DomainId STRING(128) NOT NULL,
  Role STRING(128) NOT NULL,
  Description STRING(MAX) NOT NULL,
  UpdatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = true),
  CONSTRAINT MembersRolesKind CHECK (Kind IN ('global', 'domain', 'every')),
) PRIMARY KEY (Kind, Axis, DomainId, Role);

CREATE TABLE MembersUserRoles (
  Kind STRING(16) NOT NULL,
  Axis STRING(128) NOT NULL,
  DomainId STRING(128) NOT NULL,
  Role STRING(128) NOT NULL,
  UserId STRING(320) NOT NULL,
  StartsAt TIMESTAMP,
  EndsAt TIMESTAMP,
  Reason STRING(MAX),
  CreatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = true),
  CONSTRAINT MembersUserRolesKind CHECK (Kind IN ('global', 'domain', 'every')),
) PRIMARY KEY (Kind, Axis, DomainId, Role, UserId);

CREATE INDEX MembersUserRolesByUserId ON MembersUserRoles (UserId, Kind, Axis, DomainId);

CREATE TABLE MembersRoleGrants (
  Kind STRING(16) NOT NULL,
  Axis STRING(128) NOT NULL,
  DomainId STRING(128) NOT NULL,
  Role STRING(128) NOT NULL,
  Permission STRING(64) NOT NULL,
  Resource STRING(128) NOT NULL,
  Field STRING(128) NOT NULL,
  Condition STRING(MAX) NOT NULL,
  UpdatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = true),
  CONSTRAINT MembersRoleGrantsKind CHECK (Kind IN ('global', 'domain', 'every')),
) PRIMARY KEY (Kind, Axis, DomainId, Role, Permission, Resource, Field, Condition),
  INTERLEAVE IN PARENT MembersRoles ON DELETE CASCADE;

CREATE TABLE MembersGroups (
  Id STRING(36) NOT NULL,
  Kind STRING(16) NOT NULL,
  Axis STRING(128) NOT NULL,
  DomainId STRING(128) NOT NULL,
  Name STRING(128) NOT NULL,
  Description STRING(MAX) NOT NULL,
  UpdatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = true),
  CONSTRAINT MembersGroupsKind CHECK (Kind IN ('global', 'domain')),
) PRIMARY KEY (Id);

CREATE UNIQUE INDEX MembersGroupsByName ON MembersGroups (Kind, Axis, DomainId, Name);

CREATE TABLE MembersGroupMembers (
  GroupId STRING(36) NOT NULL,
  UserId STRING(320) NOT NULL,
  StartsAt TIMESTAMP,
  EndsAt TIMESTAMP,
  CreatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = true),
  CONSTRAINT MembersGroupMembersGroup FOREIGN KEY (GroupId) REFERENCES MembersGroups (Id),
) PRIMARY KEY (GroupId, UserId);

CREATE INDEX MembersGroupMembersByUserId ON MembersGroupMembers (UserId, GroupId);

CREATE TABLE MembersGroupRoles (
  GroupId STRING(36) NOT NULL,
  Kind STRING(16) NOT NULL,
  Axis STRING(128) NOT NULL,
  DomainId STRING(128) NOT NULL,
  Role STRING(128) NOT NULL,
  StartsAt TIMESTAMP,
  EndsAt TIMESTAMP,
  CreatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = true),
  CONSTRAINT MembersGroupRolesKind CHECK (Kind IN ('global', 'domain', 'every')),
  CONSTRAINT MembersGroupRolesGroup FOREIGN KEY (GroupId) REFERENCES MembersGroups (Id),
) PRIMARY KEY (GroupId, Kind, Axis, DomainId, Role);

CREATE INDEX MembersGroupRolesByScope ON MembersGroupRoles (Kind, Axis, DomainId, Role);
