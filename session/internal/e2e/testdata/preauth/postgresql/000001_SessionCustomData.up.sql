-- The custom session data table of an application on a Preauth, whose database has the
-- base set alone: the session's row is written in the transaction that inserts the
-- session.
BEGIN;

CREATE TABLE "SessionCustomData"
(
    "SessionId" UUID NOT NULL,
    "Partner" character varying NOT NULL,
    CONSTRAINT "SessionCustomData_pkey" PRIMARY KEY ("SessionId"),
    CONSTRAINT "SessionCustomData_SessionId_fkey" FOREIGN KEY ("SessionId") REFERENCES "Sessions" ("Id") ON DELETE CASCADE
);

COMMIT;
