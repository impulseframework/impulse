-- The email address on a user, in two states. Email is the address as the person gave
-- it, NormalizedEmail the form uniqueness and lookups use (the address with its ASCII
-- capital letters lowercased and every other character as written, which is how the
-- library compares two addresses), and EmailVerifiedAt the database's time at which the
-- person proved they read the mailbox by following a link sent to it; null while the
-- address is unverified. An address is trusted for nothing
-- until it is verified: code that sends a link may use an unverified address, code that
-- relies on one (linking a social identity, a recovery) requires the time. An
-- administrator may put an address on a user by hand, unverified, which reserves it:
-- uniqueness holds among every address held, verified or not. No address is required.
--
-- An application whose usernames are email addresses declares the two as one value
-- instead, generated from the username by the database so they cannot diverge; the
-- README's "Users and their email address" gives that DDL in place of the first two
-- statements here.
ALTER TABLE SessionUsers ADD COLUMN Email STRING(MAX);
-- LOWER over BYTES treats its argument as ASCII and leaves every byte above 127 as it
-- is, so it folds nothing but the ASCII letters.
ALTER TABLE SessionUsers ADD COLUMN NormalizedEmail STRING(MAX) AS (CAST(LOWER(CAST(Email AS BYTES)) AS STRING)) STORED;
ALTER TABLE SessionUsers ADD COLUMN EmailVerifiedAt TIMESTAMP OPTIONS (allow_commit_timestamp = true);
CREATE UNIQUE NULL_FILTERED INDEX SessionUsersByNormalizedEmail ON SessionUsers(NormalizedEmail);

-- The username as written is unique. The index is in the order a list of people is read
-- in, by username, and carries the columns such a list shows, so a page of people can be
-- read from it alone.
CREATE UNIQUE INDEX SessionUsersByUsername ON SessionUsers(Username) STORING (Email, EmailVerifiedAt, Disabled, PasswordChangeRequired, CreatedAt, LastSignInAt);
