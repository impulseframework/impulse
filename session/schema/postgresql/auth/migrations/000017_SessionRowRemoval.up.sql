-- EndsAt is the moment a sign-in attempts row stops counting: the end of its window or
-- of its lock, whichever is later, worked out by the database. UpdatedOn is the UTC day
-- of a session's last activity (UpdatedAt), worked out by the database. PostgreSQL has
-- no row deletion policy, so the store removes the rows that are over as it writes: a
-- sign-in attempts row one day after EndsAt, when it records a failed attempt; a mail
-- link 30 days after the link expired (ExpiresAt), when it inserts a link; a session
-- once UpdatedOn is more than 30 days back, when a person signs in. It reads the rows
-- that are due by these indexes. The sessions are indexed by the day, not by UpdatedAt:
-- an index on UpdatedAt would be written by every activity update, the most frequent
-- write there is, while UpdatedOn changes once a day for a session in use, and an
-- update that changes no indexed column writes no index while its row's page has room.
BEGIN;

ALTER TABLE "SessionSignInAttempts" ADD COLUMN "EndsAt" timestamp with time zone GENERATED ALWAYS AS (GREATEST("WindowEndsAt", COALESCE("LockedUntil", "WindowEndsAt"))) STORED;
ALTER TABLE "Sessions" ADD COLUMN "UpdatedOn" date GENERATED ALWAYS AS (("UpdatedAt" AT TIME ZONE 'UTC')::date) STORED;
CREATE INDEX "SessionSignInAttempts_EndsAt_idx" ON "SessionSignInAttempts" USING btree ("EndsAt");
CREATE INDEX "SessionMailLinks_ExpiresAt_idx" ON "SessionMailLinks" USING btree ("ExpiresAt");
CREATE INDEX "Sessions_UpdatedOn_idx" ON "Sessions" USING btree ("UpdatedOn");

COMMIT;
