-- The fixture of the conditions a role held on a record counts under: projects, each
-- naming a client; tasks in a project and from another; comments on a task, with a body;
-- invoices apart; and the groups, their members and the roles held on records of the auth
-- staff, with the columns the access library's tables give them. Names are the fixture's
-- own.

CREATE TABLE Clients (
  Id STRING(36) NOT NULL,
  DomainId STRING(128) NOT NULL,
) PRIMARY KEY (Id);

CREATE TABLE Projects (
  Id STRING(36) NOT NULL,
  DomainId STRING(128) NOT NULL,
  ClientId STRING(36),
  CONSTRAINT ProjectsClient FOREIGN KEY (ClientId) REFERENCES Clients (Id),
) PRIMARY KEY (Id);

CREATE TABLE Tasks (
  Id STRING(36) NOT NULL,
  DomainId STRING(128) NOT NULL,
  ProjectId STRING(36),
  FormerProjectId STRING(36),
  CONSTRAINT TasksProject FOREIGN KEY (ProjectId) REFERENCES Projects (Id),
  CONSTRAINT TasksFormerProject FOREIGN KEY (FormerProjectId) REFERENCES Projects (Id),
) PRIMARY KEY (Id);

CREATE TABLE Comments (
  Id STRING(36) NOT NULL,
  DomainId STRING(128) NOT NULL,
  TaskId STRING(36),
  Body STRING(MAX),
  CONSTRAINT CommentsTask FOREIGN KEY (TaskId) REFERENCES Tasks (Id),
) PRIMARY KEY (Id);

CREATE TABLE Milestones (
  Id STRING(36) NOT NULL,
  DomainId STRING(128) NOT NULL,
  ProjectId STRING(36),
  CONSTRAINT MilestonesProject FOREIGN KEY (ProjectId) REFERENCES Projects (Id),
) PRIMARY KEY (Id);

CREATE TABLE Invoices (
  Id STRING(36) NOT NULL,
  DomainId STRING(128) NOT NULL,
) PRIMARY KEY (Id);

CREATE TABLE StaffGroups (
  Id STRING(36) NOT NULL,
  DomainId STRING(128) NOT NULL,
) PRIMARY KEY (Id);

CREATE TABLE StaffGroupMembers (
  GroupId STRING(36) NOT NULL,
  UserId STRING(320) NOT NULL,
  StartsAt TIMESTAMP,
  EndsAt TIMESTAMP,
  CONSTRAINT StaffGroupMembersGroup FOREIGN KEY (GroupId) REFERENCES StaffGroups (Id),
) PRIMARY KEY (GroupId, UserId);

CREATE TABLE StaffRecordUserRoles (
  Axis STRING(128) NOT NULL,
  DomainId STRING(128) NOT NULL,
  Resource STRING(128) NOT NULL,
  RecordId STRING(MAX) NOT NULL,
  Role STRING(128) NOT NULL,
  UserId STRING(320) NOT NULL,
  StartsAt TIMESTAMP,
  EndsAt TIMESTAMP,
) PRIMARY KEY (Axis, DomainId, Resource, RecordId, Role, UserId);

CREATE TABLE StaffRecordGroupRoles (
  Axis STRING(128) NOT NULL,
  DomainId STRING(128) NOT NULL,
  Resource STRING(128) NOT NULL,
  RecordId STRING(MAX) NOT NULL,
  Role STRING(128) NOT NULL,
  GroupId STRING(36) NOT NULL,
  StartsAt TIMESTAMP,
  EndsAt TIMESTAMP,
  CONSTRAINT StaffRecordGroupRolesGroup FOREIGN KEY (GroupId) REFERENCES StaffGroups (Id),
) PRIMARY KEY (Axis, DomainId, Resource, RecordId, Role, GroupId);
