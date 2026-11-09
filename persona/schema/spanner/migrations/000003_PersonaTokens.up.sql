CREATE TABLE PersonaTokens (
  Id STRING(36) NOT NULL,
  RequestId STRING(36) NOT NULL,
  ClientId STRING(128) NOT NULL,
  Subject STRING(254) NOT NULL,
  Audience ARRAY<STRING(MAX)> NOT NULL,
  Scopes ARRAY<STRING(MAX)> NOT NULL,
  ExpiresAt TIMESTAMP NOT NULL,
) PRIMARY KEY (Id);

CREATE INDEX PersonaTokens_ExpiresAt_idx ON PersonaTokens (ExpiresAt);
