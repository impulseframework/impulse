-- The three sectors of the demo world (design plan §2): Anvil is home, Bastion is where a
-- role stops at a border, Cinder is the burned-out frontier most personas cannot see.
-- They sit under fixed ids: a sector's key is an id the application generates, and the
-- demo world fixes the three so the suites and the walkthrough can name them. The
-- personas name a sector by its name (cmd/bootstrap/users.json).
INSERT INTO Sectors (Id, Name, Region, Established) VALUES ('a1111111-1111-4111-8111-111111111111', 'Anvil', 'Inner frontier', DATE '2201-03-14');
-- Bastion admits its people through its directories only: a password does not reach it.
INSERT INTO Sectors (Id, Name, Region, Established, DirectoryOnly) VALUES ('b2222222-2222-4222-8222-222222222222', 'Bastion', 'Relay corridor', DATE '2207-09-01', TRUE);
INSERT INTO Sectors (Id, Name, Region, Established) VALUES ('c3333333-3333-4333-8333-333333333333', 'Cinder', 'Outer frontier', DATE '2214-06-30');
-- Every sector declares the fleet's Google Workspace, named example by both auths'
-- registrations (APP_CREW_OIDC_CONNECTION, APP_MEMBERS_OIDC_CONNECTION): the navigator,
-- who signs in through it, and the client, whose company directory it stands in for,
-- reach the sectors they hold a role in. Anvil and Bastion admit a fleet member nobody
-- has seated by invitation only (no default seat); Cinder, the frontier short of hands,
-- seats one as SectorCrew at their first sign-in.
INSERT INTO SectorDirectories (Id, SectorId, Method, Connection) VALUES ('e0000000-0000-4000-8000-000000000001', 'a1111111-1111-4111-8111-111111111111', 'google', 'example');
INSERT INTO SectorDirectories (Id, SectorId, Method, Connection) VALUES ('e0000000-0000-4000-8000-000000000002', 'b2222222-2222-4222-8222-222222222222', 'google', 'example');
INSERT INTO SectorDirectories (Id, SectorId, Method, Connection, DefaultSeat) VALUES ('e0000000-0000-4000-8000-000000000003', 'c3333333-3333-4333-8333-333333333333', 'google', 'example', 'SectorCrew');
