-- A person's name, as people read it ("Gale Reyes"). It identifies nobody: the
-- username does. Null on a user for none.
--
-- On a user it is what a directory asserted at a sign-in, what the inviter typed, or
-- what the person wrote themselves: a sign-in fills it only where the user holds none,
-- so the person's own change stands. A pending sign-in carries the name its identity
-- asserted, with the identity's other attributes, and an invitation the name its
-- sender gave the person.
--
-- The two indexes a list reads without a row of the table carry the name too: the
-- account by its exact username, and a tenant's invitations.
ALTER TABLE SessionUsers ADD COLUMN Name STRING(MAX);
ALTER INDEX SessionUsersByUsername ADD STORED COLUMN Name;
ALTER TABLE SessionPendingSignIns ADD COLUMN Name STRING(MAX) NOT NULL DEFAULT ('');
ALTER TABLE SessionMailLinks ADD COLUMN Name STRING(MAX);
ALTER INDEX SessionMailLinksByTenant ADD STORED COLUMN Name;
