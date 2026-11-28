-- A session names its user and is not held to it by a foreign key: a deleted user's
-- sessions are ended in code, and their rows stay until the timed removal takes them.
BEGIN;

ALTER TABLE "Sessions" DROP CONSTRAINT "Sessions_UserId_fkey";

COMMIT;
