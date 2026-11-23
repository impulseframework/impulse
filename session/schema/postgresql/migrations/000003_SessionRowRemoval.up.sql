-- A session's row is removed 30 days after its last activity (UpdatedAt). PostgreSQL has
-- no row deletion policy, so the store removes the rows that are due when a person signs
-- in, and reads them by this index.
BEGIN;

CREATE INDEX "Sessions_UpdatedAt_idx" ON "Sessions" USING btree ("UpdatedAt");

COMMIT;
