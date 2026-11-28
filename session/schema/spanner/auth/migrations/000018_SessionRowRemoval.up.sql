-- EndsAt is the moment a sign-in attempts row stops counting: the end of its window or
-- of its lock, whichever is later, worked out by the database. The row is removed one
-- day after it. A lock that still holds is never removed, however long it lasts, and
-- neither is a count the library keeps under a window of a hundred years.
ALTER TABLE SessionSignInAttempts ADD COLUMN EndsAt TIMESTAMP AS (GREATEST(WindowEndsAt, IFNULL(LockedUntil, WindowEndsAt))) STORED;
ALTER TABLE SessionSignInAttempts ADD ROW DELETION POLICY (OLDER_THAN(EndsAt, INTERVAL 1 DAY));

-- A mail link's row is removed 30 days after the link expired (ExpiresAt), used or not.
ALTER TABLE SessionMailLinks ADD ROW DELETION POLICY (OLDER_THAN(ExpiresAt, INTERVAL 30 DAY));
