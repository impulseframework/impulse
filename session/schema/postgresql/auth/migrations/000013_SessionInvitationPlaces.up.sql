-- The place an invitation's roles are held in. An invitation names one tenant
-- (TenantId), every tenant (EveryTenant), or neither, which is the application's global
-- surface. The acceptance gives each role in the place the row keeps and nowhere else,
-- whatever roles exist by then: an invitation for every tenant gives nothing in the
-- global surface, and one for the global surface gives nothing in every tenant. False on
-- every other link.
BEGIN;

ALTER TABLE "SessionMailLinks" ADD COLUMN "EveryTenant" boolean NOT NULL DEFAULT false;

COMMIT;
