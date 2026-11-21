-- The invitation a code was mailed for. A code mailed to an invited address accepts that
-- invitation and nothing else: it is judged only in a browser that carries that
-- invitation, and the codes entered for it are counted against that invitation,
-- whatever the browser carries. Null on an account's own sign-in code. No key ties the
-- column to the mail links table: the id is compared and counted under, and nothing of
-- the invitation is read through it.
BEGIN;

ALTER TABLE "SessionFactorCodes" ADD COLUMN "InvitationId" UUID;

COMMIT;
