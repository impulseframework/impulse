-- The tenant an invitation makes. An invitation that brings an organization into the
-- application names no tenant and carries no roles: it carries the tenant its first
-- sign-in makes, as a JSON document. The document holds what people will call the
-- tenant (name), which the invitation page shows before the sign-in; whether the tenant
-- is entered through its directory only (directoryOnly), which holds the acceptance to
-- a sign-in through an organization's directory; and the application's own details of
-- the tenant (details), which the library stores, hands to the application's
-- acceptance hook and serves nowhere. Null on every other link.
BEGIN;

ALTER TABLE "SessionMailLinks" ADD COLUMN "NewTenant" character varying;

COMMIT;
