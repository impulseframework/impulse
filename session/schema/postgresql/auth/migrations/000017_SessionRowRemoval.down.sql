BEGIN;
DROP INDEX IF EXISTS "Sessions_UpdatedOn_idx";
ALTER TABLE "Sessions" DROP COLUMN IF EXISTS "UpdatedOn";
DROP INDEX IF EXISTS "SessionMailLinks_ExpiresAt_idx";
DROP INDEX IF EXISTS "SessionSignInAttempts_EndsAt_idx";
ALTER TABLE "SessionSignInAttempts" DROP COLUMN IF EXISTS "EndsAt";
COMMIT;
