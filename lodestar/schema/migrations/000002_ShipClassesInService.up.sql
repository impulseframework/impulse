-- InService records whether the yards still build this hull. A class enters the catalog in
-- service and leaves it only when somebody says so, so the column declares the yes itself: a
-- row that names no value is in service, the generator carries the default into the browser
-- metadata, and the console's form starts the box checked with nothing written in its config.
ALTER TABLE ShipClasses ADD COLUMN InService BOOL NOT NULL DEFAULT (TRUE);
