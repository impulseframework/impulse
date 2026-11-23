-- Sign-in settings: what an administrator chose, in the running application, for the
-- whole Auth. The table holds at most one row, the Auth's, under the one name it takes
-- ('auth'). RequireSecondFactor says everyone is asked for a second factor when they sign
-- in, and RememberBrowserDays for how many days a browser a person asked to be remembered
-- skips the code, 0 for browsers that are not remembered. An Auth with no row requires
-- the second factor and remembers a browser for 30 days, so the table starts empty.
BEGIN;

CREATE TABLE "SessionSignInSettings"
(
    "Name" character varying NOT NULL,
    "RequireSecondFactor" boolean NOT NULL,
    "RememberBrowserDays" bigint NOT NULL,
    "UpdatedAt" timestamp with time zone NOT NULL,
    CONSTRAINT "SessionSignInSettings_pkey" PRIMARY KEY ("Name"),
    CONSTRAINT "SessionSignInSettings_Name_check" CHECK ("Name" = 'auth')
);

COMMIT;
