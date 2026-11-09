CREATE TABLE PersonaRegistrations (
  ClientId STRING(128) NOT NULL,
  ApplicationClientId STRING(128) NOT NULL,
  Environment STRING(16) NOT NULL,
  PullRequest INT64 NOT NULL,
  RedirectUris ARRAY<STRING(MAX)> NOT NULL,
  Personas STRING(MAX) NOT NULL,
  RegisteredBy STRING(320) NOT NULL,
  RegisteredAt TIMESTAMP NOT NULL,
  ExpiresAt TIMESTAMP NOT NULL,
) PRIMARY KEY (ClientId);

CREATE INDEX PersonaRegistrations_ExpiresAt_idx ON PersonaRegistrations (ExpiresAt);
