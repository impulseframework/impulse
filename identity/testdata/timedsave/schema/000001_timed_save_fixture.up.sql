-- The timed save test's fixture: a table of records whose delete is handed the auth's
-- kinds. Its name is the fixture's own.

CREATE TABLE SavedRecords (
  Id STRING(36) NOT NULL,
) PRIMARY KEY (Id);
