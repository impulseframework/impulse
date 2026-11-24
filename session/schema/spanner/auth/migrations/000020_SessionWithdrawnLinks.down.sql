ALTER INDEX SessionMailLinksByTenantId DROP STORED COLUMN WithdrawnAt;
ALTER INDEX SessionMailLinksByTenantId DROP STORED COLUMN MailFailedAt;
ALTER TABLE SessionMailLinks DROP COLUMN WithdrawnAt;
