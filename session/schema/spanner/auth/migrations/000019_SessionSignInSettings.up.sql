-- Sign-in settings: what an administrator chose, in the running application, for the
-- whole Auth. The table holds at most one row, the Auth's, under the one name it takes
-- ('auth'). RequireSecondFactor says everyone is asked for a second factor when they sign
-- in, and RememberBrowserDays for how many days a browser a person asked to be remembered
-- skips the code, 0 for browsers that are not remembered. An Auth with no row requires
-- the second factor and remembers a browser for 30 days, so the table starts empty.
-- UpdatedAt takes the commit time.
CREATE TABLE SessionSignInSettings (
    Name                STRING(MAX) NOT NULL,
    RequireSecondFactor BOOL NOT NULL,
    RememberBrowserDays INT64 NOT NULL,
    UpdatedAt           TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = true),
    CONSTRAINT CK_SessionSignInSettingsName CHECK (Name = 'auth'),
) PRIMARY KEY (Name);
