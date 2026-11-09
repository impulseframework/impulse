CREATE TABLE PersonaSignIns (
  Id STRING(36) NOT NULL,
  ClientId STRING(128) NOT NULL,
  ApplicationId STRING(128) NOT NULL,
  Tenant STRING(128) NOT NULL,
  Environment STRING(16) NOT NULL,
  PullRequest INT64 NOT NULL,
  Persona STRING(254) NOT NULL,
  ActorId STRING(254) NOT NULL,
  ActorName STRING(MAX) NOT NULL,
  ActorEmail STRING(320) NOT NULL,
  SignedInAt TIMESTAMP NOT NULL,
) PRIMARY KEY (Id);

CREATE INDEX PersonaSignIns_ClientId_SignedInAt_idx ON PersonaSignIns (ClientId, SignedInAt DESC);
