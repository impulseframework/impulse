-- EndsAt is the moment a sign-in attempts row stops counting: the end of its window or
-- of its lock, whichever is later, worked out by the database. The store removes a row
-- one day after it, when it records a failed attempt, and a mail link 30 days after the
-- link expired (ExpiresAt), when it inserts a link. It reads the rows that are due by
-- these indexes.
BEGIN;

ALTER TABLE "SessionSignInAttempts" ADD COLUMN "EndsAt" timestamp with time zone GENERATED ALWAYS AS (GREATEST("WindowEndsAt", COALESCE("LockedUntil", "WindowEndsAt"))) STORED;
CREATE INDEX "SessionSignInAttempts_EndsAt_idx" ON "SessionSignInAttempts" USING btree ("EndsAt");
CREATE INDEX "SessionMailLinks_ExpiresAt_idx" ON "SessionMailLinks" USING btree ("ExpiresAt");

COMMIT;
