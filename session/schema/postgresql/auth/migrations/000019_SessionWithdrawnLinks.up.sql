-- When a link was taken back. A link that is withdrawn (an administrator's Withdraw, a
-- newer link of its kind for the same account, a code ended after too many wrong
-- guesses) is consumed, as a link that was followed is, and WithdrawnAt takes the same
-- time, so a list of the links tells a withdrawal from an acceptance. Null on every
-- other link.
BEGIN;

ALTER TABLE "SessionMailLinks" ADD COLUMN "WithdrawnAt" timestamp with time zone;

COMMIT;
