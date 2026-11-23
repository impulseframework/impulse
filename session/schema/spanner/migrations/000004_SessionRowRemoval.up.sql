-- A session's row is removed 30 days after its last activity (UpdatedAt). Spanner
-- deletes it in the background, with the rows that reference it by a cascading foreign
-- key (its auth events, its custom session data). A session in use is never removed:
-- every request it serves moves UpdatedAt. One idle that long is dead whatever the
-- timeout, which the library holds to 30 days at most.
ALTER TABLE Sessions ADD ROW DELETION POLICY (OLDER_THAN(UpdatedAt, INTERVAL 30 DAY));
