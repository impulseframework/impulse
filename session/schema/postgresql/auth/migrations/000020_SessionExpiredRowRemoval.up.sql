-- PostgreSQL has no row deletion policy, so the store removes the rows of four more
-- tables that are over as it writes a row of the same table: a token and a trusted
-- device 30 days after it expired (ExpiresAt), a device code and a pending sign-in one
-- day after it expired. A pending sign-in's sign-in codes go with it by their foreign
-- key. The store reads the rows that are due by these indexes. The pending sign-ins
-- table has its own already (SessionPendingSignIns_ExpiresAt_idx).
BEGIN;

CREATE INDEX "SessionTokens_ExpiresAt_idx" ON "SessionTokens" USING btree ("ExpiresAt");
CREATE INDEX "SessionTrustedDevices_ExpiresAt_idx" ON "SessionTrustedDevices" USING btree ("ExpiresAt");
CREATE INDEX "SessionDeviceCodes_ExpiresAt_idx" ON "SessionDeviceCodes" USING btree ("ExpiresAt");

COMMIT;
