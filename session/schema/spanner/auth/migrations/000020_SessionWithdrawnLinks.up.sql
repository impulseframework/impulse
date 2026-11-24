-- When a link was taken back. A link that is withdrawn (an administrator's Withdraw, a
-- newer link of its kind for the same account, a code ended after too many wrong
-- guesses) is consumed, as a link that was followed is, and WithdrawnAt takes the same
-- commit time, so a list of the links tells a withdrawal from an acceptance. Null on
-- every other link.
--
-- The index a tenant's list of its invitations reads without a row of the table carries
-- WithdrawnAt and MailFailedAt too: the list says what became of each invitation, and
-- reads the two for it.
ALTER TABLE SessionMailLinks ADD COLUMN WithdrawnAt TIMESTAMP OPTIONS (allow_commit_timestamp = true);
ALTER INDEX SessionMailLinksByTenantId ADD STORED COLUMN MailFailedAt;
ALTER INDEX SessionMailLinksByTenantId ADD STORED COLUMN WithdrawnAt;
