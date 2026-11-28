-- The auth's audit log: one row for each act that changes an account, who holds what, or
-- how people sign in. A row holds parts and no sentence: when, the act by its fixed name,
-- who acted, whom or what it was done to, the tenant it was done in, the role, the group,
-- the old and new value and the period where the act has them, and the reason where there
-- was one. Each name is kept as it was when the act happened, beside its id. No row holds
-- a password, a token, a code or a link. Rows are written by the library alone and never
-- changed. An application that keeps them for a period adds the row deletion policy the
-- auditlog package gives (Spanner); on PostgreSQL the store removes them as it writes.
CREATE TABLE SessionAuditRecords (
  Id STRING(36) NOT NULL,
  CreatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = true),
  Act STRING(MAX) NOT NULL,
  Form STRING(MAX),
  Auth STRING(MAX),
  ActorUserId STRING(MAX),
  ActorUsername STRING(MAX),
  PrincipalUserId STRING(MAX),
  PrincipalUsername STRING(MAX),
  PrincipalRole STRING(MAX),
  SessionId STRING(MAX),
  Process STRING(MAX),
  Rpc STRING(MAX),
  Directory STRING(MAX),
  SubjectKind STRING(MAX) NOT NULL,
  SubjectId STRING(MAX) NOT NULL,
  SubjectName STRING(MAX),
  TenantId STRING(MAX),
  TenantName STRING(MAX),
  RoleName STRING(MAX),
  OtherRoleNames ARRAY<STRING(MAX)>,
  RoleScope STRING(MAX),
  RecordKind STRING(MAX),
  RecordId STRING(MAX),
  RecordWord STRING(MAX),
  RecordName STRING(MAX),
  GroupId STRING(MAX),
  GroupName STRING(MAX),
  OldValue STRING(MAX),
  NewValue STRING(MAX),
  PeriodStart TIMESTAMP,
  PeriodEnd TIMESTAMP,
  SeatingTenantId STRING(MAX),
  SeatingTenantName STRING(MAX),
  Reason STRING(MAX),
  CONSTRAINT CK_SessionAuditRecordsId CHECK (REGEXP_CONTAINS(Id, r'^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$')),
) PRIMARY KEY (Id);

CREATE INDEX SessionAuditRecordsByCreatedAt ON SessionAuditRecords(CreatedAt DESC);
CREATE NULL_FILTERED INDEX SessionAuditRecordsByTenant ON SessionAuditRecords(TenantId, CreatedAt DESC);
