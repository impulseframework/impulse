-- The auth's audit log: one row for each act that changes an account, who holds what, or
-- how people sign in. A row holds parts and no sentence: when, the act by its fixed name,
-- who acted, whom or what it was done to, the tenant it was done in, the role, the group,
-- the old and new value and the period where the act has them, and the reason where there
-- was one. Each name is kept as it was when the act happened, beside its id. No row holds
-- a password, a token, a code or a link. Rows are written by the library alone and never
-- changed. The store removes the rows older than the period the Auth is given, as it
-- writes.
BEGIN;

CREATE TABLE "SessionAuditRecords"
(
    "Id" character varying NOT NULL,
    "CreatedAt" timestamp with time zone NOT NULL,
    "Act" character varying NOT NULL,
    "Form" character varying,
    "Auth" character varying,
    "ActorUserId" character varying,
    "ActorUsername" character varying,
    "PrincipalUserId" character varying,
    "PrincipalUsername" character varying,
    "PrincipalRole" character varying,
    "SessionId" character varying,
    "Process" character varying,
    "Rpc" character varying,
    "Directory" character varying,
    "SubjectKind" character varying NOT NULL,
    "SubjectId" character varying NOT NULL,
    "SubjectName" character varying,
    "TenantId" character varying,
    "TenantName" character varying,
    "RoleName" character varying,
    "OtherRoleNames" character varying[],
    "RoleScope" character varying,
    "RecordKind" character varying,
    "RecordId" character varying,
    "RecordWord" character varying,
    "RecordName" character varying,
    "GroupId" character varying,
    "GroupName" character varying,
    "OldValue" character varying,
    "NewValue" character varying,
    "PeriodStart" timestamp with time zone,
    "PeriodEnd" timestamp with time zone,
    "SeatingTenantId" character varying,
    "SeatingTenantName" character varying,
    "Reason" character varying,
    CONSTRAINT "SessionAuditRecords_pkey" PRIMARY KEY ("Id")
);

CREATE INDEX "SessionAuditRecords_CreatedAt_idx" ON "SessionAuditRecords" USING btree ("CreatedAt");
CREATE INDEX "SessionAuditRecords_TenantId_CreatedAt_idx" ON "SessionAuditRecords" USING btree ("TenantId", "CreatedAt");

COMMIT;
