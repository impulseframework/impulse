-- The three sectors of the demo world (design plan §2): Anvil is home, Bastion is where a
-- role stops at a border, Cinder is the burned-out frontier most personas cannot see.
-- They sit under fixed ids: a sector's key is an id the application generates, and the
-- demo world fixes the three so the suites and the walkthrough can name them. The
-- personas name a sector by its name (cmd/bootstrap/users.json).
INSERT INTO Sectors (Id, Name, Region, Established, ChartedAt) VALUES ('a1111111-1111-4111-8111-111111111111', 'Anvil', 'Inner frontier', DATE '2201-03-14', TIMESTAMP '2026-01-12T16:00:00Z');
-- Bastion admits its people through its directory only: nobody who signs in another way
-- reaches it, with a password, a personal account or another sector's directory.
INSERT INTO Sectors (Id, Name, Region, Established, ChartedAt, DirectoryOnly) VALUES ('b2222222-2222-4222-8222-222222222222', 'Bastion', 'Relay corridor', DATE '2207-09-01', TIMESTAMP '2026-03-03T16:00:00Z', TRUE);
INSERT INTO Sectors (Id, Name, Region, Established, ChartedAt) VALUES ('c3333333-3333-4333-8333-333333333333', 'Cinder', 'Outer frontier', DATE '2214-06-30', TIMESTAMP '2026-06-22T16:00:00Z');
-- A sector has one directory and a directory belongs to one sector. The fleet's Google
-- Workspace, named example by both auths' registrations (APP_CREW_OIDC_CONNECTION,
-- APP_MEMBERS_OIDC_CONNECTION), is Cinder's: the frontier short of hands gives a fleet
-- member who holds nothing in Cinder the SectorCrew role when they sign in through it.
-- Bastion's is the persona provider of the test environments (the crew auth's persona
-- registration: an OpenID Connect provider, whose identities carry the connection
-- persona), so a sector that is directory only has the directory it is only of; its row
-- names no default seat, so Bastion seats nobody a marshal did not. Anvil has no
-- directory. In a deployment nobody writes these rows by hand: a directory is set from a
-- sign-in through it.
INSERT INTO SectorDirectories (Id, SectorId, Method, Connection) VALUES ('e0000000-0000-4000-8000-000000000002', 'b2222222-2222-4222-8222-222222222222', 'oidc', 'persona');
INSERT INTO SectorDirectories (Id, SectorId, Method, Connection, DefaultSeat) VALUES ('e0000000-0000-4000-8000-000000000003', 'c3333333-3333-4333-8333-333333333333', 'google', 'example', 'SectorCrew');
