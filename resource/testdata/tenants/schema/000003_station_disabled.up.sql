-- Whether a station is disabled, as an application's @tenantDisabled column is: its
-- people are shut out while its records stay.

ALTER TABLE Stations ADD COLUMN Disabled BOOL NOT NULL DEFAULT (FALSE);
