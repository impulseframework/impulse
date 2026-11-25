-- The feature flag tests' fixture: the two tables resource.FeatureFlagsDDL(SpannerDBType)
-- declares, as an application copies them into a migration. TestFeatureFlagsDDL pins
-- this file to the function's statements.

CREATE TABLE FeatureFlags (
  Name STRING(64) NOT NULL,
  Description STRING(MAX) NOT NULL,
  Enabled BOOL NOT NULL,
  UpdatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = true),
  UpdatedBy STRING(MAX) NOT NULL,
) PRIMARY KEY (Name);

CREATE TABLE FeatureFlagChanges (
  Name STRING(64) NOT NULL,
  ChangedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = true),
  Enabled BOOL NOT NULL,
  Auth STRING(MAX),
  ActorUserId STRING(MAX),
  ActorUsername STRING(MAX),
  PrincipalUserId STRING(MAX),
  PrincipalUsername STRING(MAX),
  PrincipalRole STRING(MAX),
  SessionId STRING(MAX),
  Process STRING(MAX),
  Rpc STRING(MAX),
) PRIMARY KEY (Name, ChangedAt);
