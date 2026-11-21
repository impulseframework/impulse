-- The fixture of the questions about a named person and the subject sets: hubs, each
-- in a zone; the memberships and the loans a person's sets are read from; and the
-- profile a person's home hub is read from. Names are the fixture's own.

CREATE TABLE Zones (
  Id STRING(36) NOT NULL,
  Climate STRING(MAX),
) PRIMARY KEY (Id);

CREATE TABLE Hubs (
  Id STRING(36) NOT NULL,
  Region STRING(MAX),
  ZoneId STRING(36),
) PRIMARY KEY (Id);

CREATE TABLE Memberships (
  Id STRING(36) NOT NULL,
  UserId STRING(MAX),
  HubId STRING(36),
) PRIMARY KEY (Id);

CREATE TABLE Loans (
  Id STRING(36) NOT NULL,
  UserId STRING(MAX),
  HubId STRING(36),
  LentHubId STRING(36),
) PRIMARY KEY (Id);

CREATE TABLE Profiles (
  UserId STRING(MAX) NOT NULL,
  HomeHubId STRING(36),
) PRIMARY KEY (UserId);
