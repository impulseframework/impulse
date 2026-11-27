-- The fixture of the tests of a delete that follows a kind nested under itself deeper than
-- one statement joins: a project's sections, keyed by the project and their own id, each
-- of which may sit under another section of the same project, a key of several columns;
-- and the replies on a task, each of which may answer another reply in the same lane, a
-- link to a column outside the key. There are no foreign keys, as in the fixtures before
-- it. Names are the fixture's own.

CREATE TABLE CascadeSections (
  ProjectId STRING(36) NOT NULL,
  Id STRING(36) NOT NULL,
  ParentId STRING(36),
) PRIMARY KEY (ProjectId, Id);

CREATE TABLE CascadeReplies (
  Id STRING(36) NOT NULL,
  TaskId STRING(36),
  Lane STRING(36),
  AnswersId STRING(36),
) PRIMARY KEY (Id);
