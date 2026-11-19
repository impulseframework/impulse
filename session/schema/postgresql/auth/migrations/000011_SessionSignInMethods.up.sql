-- Sign-in methods: what an administrator chose, in the running application, for one
-- sign-in method of the Auth, keyed by the name the method is registered under
-- ("password", or the application's name for an external registration). OpenSignUp says
-- the method admits a person who holds no account and no invitation, and OpenedFor whom
-- the method admitted when it was opened, as its registration spells its audience: the
-- row opens the method for that audience alone, so a registration widened in code since
-- is invite only until it is opened again. DefaultRole names the role such a person is
-- given with their account, and holds nothing in an application whose people get their
-- roles in a tenant. A method with no row is invite
-- only, so the table starts empty and every method starts invite only. SelfServiceReset,
-- read on the password method's row alone, says a person may ask at the sign-in page for
-- a link that resets their own password; it starts false, and with no row nobody may.
BEGIN;

CREATE TABLE "SessionSignInMethods"
(
    "Name" character varying NOT NULL,
    "OpenSignUp" boolean NOT NULL,
    "OpenedFor" character varying,
    "DefaultRole" character varying,
    "SelfServiceReset" boolean NOT NULL DEFAULT false,
    "UpdatedAt" timestamp with time zone NOT NULL,
    CONSTRAINT "SessionSignInMethods_pkey" PRIMARY KEY ("Name")
);

COMMIT;
