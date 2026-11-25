-- Four more tables would otherwise keep every row ever written. Each row carries the
-- moment it expires (ExpiresAt), written as the library gives it, and Spanner removes
-- the row in the background once that moment is far enough back, whatever else became
-- of the row. A token is removed 30 days after it expired, revoked or not: a refresh
-- token that was rotated stays until then, so one presented again before it expires
-- is still caught as a reuse. A trusted device is removed 30 days after it expired,
-- revoked or not.
ALTER TABLE SessionTokens ADD ROW DELETION POLICY (OLDER_THAN(ExpiresAt, INTERVAL 30 DAY));
ALTER TABLE SessionTrustedDevices ADD ROW DELETION POLICY (OLDER_THAN(ExpiresAt, INTERVAL 30 DAY));

-- A device code is removed one day after it expired, whether it was approved, denied
-- or never decided.
ALTER TABLE SessionDeviceCodes ADD ROW DELETION POLICY (OLDER_THAN(ExpiresAt, INTERVAL 1 DAY));

-- A pending sign-in is removed one day after it expired, consumed or not, with the
-- sign-in codes that reference it by their cascading foreign key.
ALTER TABLE SessionPendingSignIns ADD ROW DELETION POLICY (OLDER_THAN(ExpiresAt, INTERVAL 1 DAY));
