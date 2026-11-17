-- The custom session data table of an application on a Preauth, whose database has the
-- base set alone: the session's row is written in the transaction that inserts the
-- session, so the foreign key holds at the commit.
CREATE TABLE SessionCustomData (
    SessionId STRING(36) NOT NULL,
    Partner   STRING(MAX) NOT NULL,
    CONSTRAINT FK_SessionCustomData_Sessions FOREIGN KEY (SessionId) REFERENCES Sessions(Id) ON DELETE CASCADE,
) PRIMARY KEY (SessionId);
