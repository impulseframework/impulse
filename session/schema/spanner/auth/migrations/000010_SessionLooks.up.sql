-- The look a person chose for the application's pages: light or dark (LookScheme), and
-- one of the palettes the application offers, by the id the application gives it
-- (LookPalette). Null for none chosen. The person's own pages write it and nothing else
-- does, so it follows the person to every browser they sign in from.
ALTER TABLE SessionUsers ADD COLUMN LookScheme STRING(MAX);
ALTER TABLE SessionUsers ADD COLUMN LookPalette STRING(MAX);
ALTER TABLE SessionUsers ADD CONSTRAINT CK_SessionUsersLookScheme CHECK (LookScheme IN ('light', 'dark'));
