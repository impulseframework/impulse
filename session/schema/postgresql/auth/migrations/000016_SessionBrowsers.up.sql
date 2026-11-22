-- Browser is the name of the browser a session was signed in from ("Chrome on Android"),
-- worked out from the request's User-Agent header when the session is made, null where
-- it was not recognized. It tells a person's sessions apart on their account page and
-- decides nothing.
BEGIN;

ALTER TABLE "Sessions" ADD COLUMN "Browser" character varying;

COMMIT;
