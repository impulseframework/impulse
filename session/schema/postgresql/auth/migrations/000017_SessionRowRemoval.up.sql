-- EndsAt is the moment a sign-in attempts row stops counting: the end of its window or
-- of its lock, whichever is later, worked out by the database. PostgreSQL has no row
-- deletion policy, so the store removes the rows that are over as it writes: a sign-in
-- attempts row one day after EndsAt, when it records a failed attempt; a mail link 30
-- days after the link expired (ExpiresAt), when it inserts a link; a session 30 days
-- after its last activity (UpdatedAt), when a person signs in. It reads the rows that
-- are due by these indexes. The sessions are read by when they were made (CreatedAt),
-- which no request moves: an index on UpdatedAt would be written by every session's
-- activity, and a session idle 30 days was made at least 30 days ago.
BEGIN;

ALTER TABLE "SessionSignInAttempts" ADD COLUMN "EndsAt" timestamp with time zone GENERATED ALWAYS AS (GREATEST("WindowEndsAt", COALESCE("LockedUntil", "WindowEndsAt"))) STORED;
CREATE INDEX "SessionSignInAttempts_EndsAt_idx" ON "SessionSignInAttempts" USING btree ("EndsAt");
CREATE INDEX "SessionMailLinks_ExpiresAt_idx" ON "SessionMailLinks" USING btree ("ExpiresAt");
CREATE INDEX "Sessions_CreatedAt_idx" ON "Sessions" USING btree ("CreatedAt");

COMMIT;
