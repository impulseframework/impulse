-- The fixture of the tests of a save at repeatable read: a client, and the orders that
-- name it by a foreign key with no action on delete, so the database refuses a commit
-- that deletes a client an order still names. Unlike the fixtures before it, this one has
-- a foreign key: what is tried is the database's own refusal. Names are the fixture's own.

CREATE TABLE CascadeClients (
  Id STRING(36) NOT NULL,
) PRIMARY KEY (Id);

CREATE TABLE CascadeOrders (
  Id STRING(36) NOT NULL,
  ClientId STRING(36),
  CONSTRAINT FK_CascadeOrdersClient FOREIGN KEY (ClientId) REFERENCES CascadeClients (Id),
) PRIMARY KEY (Id);
