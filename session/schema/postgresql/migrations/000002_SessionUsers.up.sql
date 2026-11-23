BEGIN;

-- Table: SessionUsers

-- DROP TABLE "SessionUsers";

CREATE TABLE "SessionUsers" (
  "Id"            UUID NOT NULL,
  "Username"      character varying NOT NULL,
  -- The form a username is unique in and looked up by. A username with an @ in it reads
  -- as an email address and is matched as the library compares two addresses: its ASCII
  -- capital letters lowercased and every other character as written, so two addresses
  -- the library tells apart are two usernames. translate() changes those 26 letters and
  -- nothing else. Any other username is matched in Unicode's normalization and full case
  -- folding. The column names its collation, so the folding is the same whatever
  -- collation the database was created with, and a lookup that compares under the same
  -- collation uses the unique index.
  "NormalizedUsername" character varying COLLATE "pg_unicode_fast" GENERATED ALWAYS AS (CASE WHEN strpos("Username", '@') > 0 THEN translate("Username", 'ABCDEFGHIJKLMNOPQRSTUVWXYZ', 'abcdefghijklmnopqrstuvwxyz') ELSE casefold(normalize("Username") COLLATE "pg_unicode_fast") END) STORED,
  "PasswordHash"  character varying,
  "Disabled"      BOOL NOT NULL DEFAULT (FALSE),
  -- An administrator required a new password of the user: they choose it before
  -- anything else is served.
  "PasswordChangeRequired" BOOL NOT NULL DEFAULT (FALSE),
  -- The database's time at the insert and at the latest sign-in (null before the
  -- first); the driver writes both with now().
  "CreatedAt"     timestamp with time zone NOT NULL DEFAULT now(),
  "LastSignInAt"  timestamp with time zone,
  CONSTRAINT "SessionUsers_pkey" PRIMARY KEY ("Id")
);

-- DROP INDEX "SessionUsers_NormalizedUsername_idx";

CREATE UNIQUE INDEX "SessionUsers_NormalizedUsername_idx"
    ON "SessionUsers" USING btree
    ("NormalizedUsername");

COMMIT;