CREATE TABLE SessionUsers (
  Id                 STRING(36) NOT NULL,
  Username           STRING(MAX) NOT NULL,
  -- The form a username is unique in and looked up by. A username with an @ in it reads
  -- as an email address and is matched as the library compares two addresses: its ASCII
  -- capital letters lowercased and every other character as written (LOWER over BYTES
  -- treats its argument as ASCII), so two addresses the library tells apart are two
  -- usernames. Any other username is matched in Unicode's normalization and case folding.
  NormalizedUsername STRING(MAX) AS (IF(STRPOS(Username, '@') > 0, CAST(LOWER(CAST(Username AS BYTES)) AS STRING), NORMALIZE_AND_CASEFOLD(Username))) STORED,
  PasswordHash       STRING(MAX),
  Disabled           BOOL NOT NULL DEFAULT (FALSE),
  -- An administrator required a new password of the user: they choose it before
  -- anything else is served.
  PasswordChangeRequired BOOL NOT NULL DEFAULT (FALSE),
  -- The database's time at the insert and at the latest sign-in (null before the
  -- first); the driver writes both as commit timestamps.
  CreatedAt          TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = true),
  LastSignInAt       TIMESTAMP OPTIONS (allow_commit_timestamp = true),
  CONSTRAINT CK_SessionUsersId CHECK (REGEXP_CONTAINS(Id, r'^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$')),
) PRIMARY KEY(Id);

CREATE UNIQUE INDEX SessionUsersByNormalizedUsername ON SessionUsers(NormalizedUsername);