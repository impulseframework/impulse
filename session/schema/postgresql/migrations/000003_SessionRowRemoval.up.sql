-- UpdatedOn is the UTC day of a session's last activity (UpdatedAt), worked out by the
-- database. PostgreSQL has no row deletion policy, so the store removes a session's row
-- once UpdatedOn is more than 30 days back, with the rows that reference it by a
-- cascading foreign key, as it writes a new session: a sign-in, a Preauth store's
-- Login, a session the application starts, an impersonated session. It reads the
-- sessions that are due by this index. The sessions are indexed by the day, not by
-- UpdatedAt: an index on UpdatedAt would be written by every activity update, the most
-- frequent write there is, while UpdatedOn changes once a day for a session in use, and
-- an update that changes no indexed column writes no index while its row's page has
-- room. A session in use is never removed: every request it serves moves UpdatedAt.
BEGIN;

ALTER TABLE "Sessions" ADD COLUMN "UpdatedOn" date GENERATED ALWAYS AS (("UpdatedAt" AT TIME ZONE 'UTC')::date) STORED;
CREATE INDEX "Sessions_UpdatedOn_idx" ON "Sessions" USING btree ("UpdatedOn");

COMMIT;
