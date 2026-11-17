BEGIN;
ALTER TABLE "SessionPendingSignIns" DROP COLUMN IF EXISTS "PasswordProof";
COMMIT;
