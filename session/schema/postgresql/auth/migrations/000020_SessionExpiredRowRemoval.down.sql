BEGIN;
DROP INDEX IF EXISTS "SessionDeviceCodes_ExpiresAt_idx";
DROP INDEX IF EXISTS "SessionTrustedDevices_ExpiresAt_idx";
DROP INDEX IF EXISTS "SessionTokens_ExpiresAt_idx";
COMMIT;
