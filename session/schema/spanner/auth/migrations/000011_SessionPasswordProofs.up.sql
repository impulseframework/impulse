-- The proof of the password a pending sign-in rests on (PasswordProof): the SHA-256 of
-- the password hash the password was compared against, never the hash itself. Null for
-- a sign-in no password proved (a directory sign-in that waits for its confirmation or
-- its factor). A row that carries one is inserted only while its user still holds that
-- password, and completes only while they do: a password changed, reset or removed in
-- between leaves the pending sign-in nothing to complete.
ALTER TABLE SessionPendingSignIns ADD COLUMN PasswordProof BYTES(32);
