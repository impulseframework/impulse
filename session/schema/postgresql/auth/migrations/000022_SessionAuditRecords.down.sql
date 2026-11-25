BEGIN;
DROP INDEX IF EXISTS "SessionAuditRecords_TenantId_CreatedAt_idx";
DROP INDEX IF EXISTS "SessionAuditRecords_CreatedAt_idx";
DROP TABLE IF EXISTS "SessionAuditRecords";
COMMIT;
