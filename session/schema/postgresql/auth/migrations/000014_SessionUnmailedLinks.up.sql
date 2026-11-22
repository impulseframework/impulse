-- When a link's message could not be mailed. Such a link ends where the mail failed: it
-- is consumed, as a link that was followed or withdrawn is, and MailFailedAt takes the
-- same time, so a list of the links tells it from one that was used. Null on every
-- other link.
BEGIN;

ALTER TABLE "SessionMailLinks" ADD COLUMN "MailFailedAt" timestamp with time zone;

COMMIT;
