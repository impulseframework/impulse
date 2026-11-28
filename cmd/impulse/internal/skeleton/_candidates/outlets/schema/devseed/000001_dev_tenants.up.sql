-- The development tenants, under fixed ids: a tenant's key is an id the application
-- generates, and the development data fixes the two so the suites can name them. The
-- development logins name a tenant by its name (cmd/bootstrap/users.json).
INSERT INTO Tenants (Id, Name, CreatedAt) VALUES ('11111111-1111-4111-8111-111111111111', 'North', PENDING_COMMIT_TIMESTAMP());
INSERT INTO Tenants (Id, Name, CreatedAt) VALUES ('22222222-2222-4222-8222-222222222222', 'South', PENDING_COMMIT_TIMESTAMP());
-- The portal's members come through the Microsoft Entra directory the members auth
-- registers (APP_MEMBERS_DIRECTORY_TENANT_ID, the development placeholder). A tenant has
-- one directory and a directory belongs to one tenant, so it is North's alone: a member
-- reaches North through it, and South has no directory until a sign-in sets one. In a
-- deployment nobody writes this row by hand; it is set from a sign-in through the
-- directory.
INSERT INTO TenantDirectories (Id, TenantId, Method, Connection) VALUES ('10000000-0000-4000-8000-000000000001', '11111111-1111-4111-8111-111111111111', 'azure', '00000000-0000-0000-0000-000000000000');
