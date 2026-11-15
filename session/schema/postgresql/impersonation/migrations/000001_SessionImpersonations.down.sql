BEGIN;

DROP INDEX "SessionImpersonations_PrincipalUserId_idx";

DROP INDEX "SessionImpersonations_ActorUsername_idx";

DROP TABLE "SessionImpersonations";

COMMIT;
