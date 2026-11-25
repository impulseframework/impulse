-- The fixture of the tests of a delete that takes the rows under a record: a project
-- with its tasks, which may sit under another task, the comments on its tasks, its
-- labels, which name it by its code, and the invoices and timesheets that refuse its
-- delete; and a folder, which may sit in another. There are no foreign keys: the delete
-- reads the links its description declares. Names are the fixture's own.

CREATE TABLE CascadeProjects (
  Id STRING(36) NOT NULL,
  Code STRING(36),
) PRIMARY KEY (Id);

CREATE TABLE CascadeTasks (
  Id STRING(36) NOT NULL,
  ProjectId STRING(36),
  ParentTaskId STRING(36),
  Title STRING(MAX),
) PRIMARY KEY (Id);

CREATE TABLE CascadeComments (
  Id STRING(36) NOT NULL,
  TaskId STRING(36),
  ProjectId STRING(36),
) PRIMARY KEY (Id);

CREATE TABLE CascadeLabels (
  Id STRING(36) NOT NULL,
  ProjectCode STRING(36),
) PRIMARY KEY (Id);

CREATE TABLE CascadeInvoices (
  Id STRING(36) NOT NULL,
  TaskId STRING(36),
  CheckedTaskId STRING(36),
) PRIMARY KEY (Id);

CREATE TABLE CascadeTimesheets (
  Id STRING(36) NOT NULL,
  TaskId STRING(36),
) PRIMARY KEY (Id);

CREATE TABLE CascadeFolders (
  Id STRING(36) NOT NULL,
  ParentId STRING(36),
) PRIMARY KEY (Id);
