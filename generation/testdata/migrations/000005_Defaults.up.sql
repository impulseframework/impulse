-- One table whose columns declare each kind of default the schema read carries: a BOOL
-- that is TRUE unless said, one that is FALSE, one spelled in lower case, one with no
-- default, one whose default is an expression, a nullable one with a default, and a
-- number with one.
CREATE TABLE Switches (
  Id STRING(36) NOT NULL,
  Lit BOOL NOT NULL DEFAULT (TRUE),
  Dark BOOL NOT NULL DEFAULT (FALSE),
  Low BOOL NOT NULL DEFAULT (false),
  Open BOOL NOT NULL,
  Odd BOOL NOT NULL DEFAULT (1 = 1),
  Maybe BOOL DEFAULT (TRUE),
  Count INT64 NOT NULL DEFAULT (0),
) PRIMARY KEY (Id);
