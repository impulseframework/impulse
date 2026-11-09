CREATE TABLE PersonaRequests (
  Id STRING(36) NOT NULL,
  ClientId STRING(128) NOT NULL,
  RedirectUri STRING(MAX) NOT NULL,
  State STRING(MAX) NOT NULL,
  Nonce STRING(MAX) NOT NULL,
  Scopes ARRAY<STRING(MAX)> NOT NULL,
  ResponseType STRING(32) NOT NULL,
  ResponseMode STRING(32) NOT NULL,
  CodeChallenge STRING(128) NOT NULL,
  CodeChallengeMethod STRING(8) NOT NULL,
  LoginHint STRING(254) NOT NULL,
  Subject STRING(254) NOT NULL,
  ActorId STRING(254) NOT NULL,
  Done BOOL NOT NULL,
  AuthTime TIMESTAMP,
  Code STRING(MAX),
  CreatedAt TIMESTAMP NOT NULL,
) PRIMARY KEY (Id);

CREATE UNIQUE NULL_FILTERED INDEX PersonaRequests_Code_idx ON PersonaRequests (Code);

CREATE INDEX PersonaRequests_CreatedAt_idx ON PersonaRequests (CreatedAt);
